import copy
import unittest

from cluster_evidence import RESOURCES, snapshot
from cluster_readiness import assess


def cluster_fixture(config):
    def obj(name):
        return {"metadata": {"name": name, "uid": name + "-uid", "namespace": config["namespace"], "generation": 1}}

    def owner(kind, name):
        return {"kind": kind, "name": name, "uid": name + "-uid", "controller": True}

    data = {resource: {"items": []} for resource in RESOURCES}
    data["namespace"] = obj(config["namespace"])
    data["namespace"]["metadata"]["labels"] = {"apexload.io/fault-run": config["run_id"]}
    data["nodes"] = {"items": []}
    for suffix in ("-control-plane", "-worker", "-worker2"):
        node = obj(config["cluster"] + suffix)
        node["metadata"]["labels"] = {"node-role.kubernetes.io/control-plane": ""} if suffix == "-control-plane" else {}
        node["status"] = {"conditions": [{"type": "Ready", "status": "True"}, {"type": "DiskPressure", "status": "False"}]}
        data["nodes"]["items"].append(node)
    for resource, name in (("deployments", "controller"), ("replicasets", "controller-rs"), ("statefulsets", "generator")):
        workload = obj(name)
        workload.update(spec={"replicas": 1}, status={"observedGeneration": 1, "replicas": 1, "readyReplicas": 1,
                                                    "availableReplicas": 1, "updatedReplicas": 1})
        if resource == "replicasets":
            workload["metadata"]["ownerReferences"] = [owner("Deployment", "controller")]
        data[resource]["items"] = [workload]
    for name, ref in (("controller-rs-pod", owner("ReplicaSet", "controller-rs")),
                      ("generator-0", owner("StatefulSet", "generator")), ("target", None)):
        pod = obj(name)
        if ref:
            pod["metadata"]["ownerReferences"] = [ref]
        pod["spec"] = {"nodeName": config["cluster"] + "-worker"}
        pod["status"] = {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}, {"type": "ContainersReady", "status": "True"}],
                         "containerStatuses": [{"name": "app", "ready": True, "restartCount": 0, "state": {"running": {}}}]}
        data["pods"]["items"].append(pod)
    pvc = obj("data-generator-0")
    pvc.update(spec={"volumeName": "volume-1", "storageClassName": "standard", "accessModes": ["ReadWriteOnce"]}, status={"phase": "Bound"})
    data["persistentvolumeclaims"]["items"] = [pvc]
    return data


class ClusterReadinessTest(unittest.TestCase):
    def setUp(self):
        self.config = dict(run_id="a" * 16, cluster="apexload-fault-" + "a" * 16, namespace="apexload-fault")
        self.data = cluster_fixture(self.config)
        self.baseline = self.capture()

    def capture(self):
        return snapshot(self.config, lambda *args: copy.deepcopy(self.data[args[1]]))

    def reasons(self):
        result = assess(self.baseline, self.capture())
        self.assertFalse(result["ready"])
        return [issue["reason"] for issue in result["issues"]]

    def test_ready_stack_and_normal_managed_pod_replacement(self):
        self.assertTrue(assess(self.baseline, self.capture())["ready"])
        for pod in self.data["pods"]["items"][:2]:
            pod["metadata"]["uid"] += "-replacement"
            pod["spec"]["nodeName"] = self.config["cluster"] + "-worker2"
        self.data["pods"]["items"][0]["metadata"]["name"] = "controller-rs-newpod"
        self.assertTrue(assess(self.baseline, self.capture())["ready"])

    def test_readiness_requires_current_generation_and_full_replica_state(self):
        original = copy.deepcopy(self.data)
        for field, value in (("readyReplicas", 0), ("availableReplicas", 0), ("updatedReplicas", 0),
                             ("replicas", 2), ("observedGeneration", 0), ("observedGeneration", None)):
            with self.subTest(field=field):
                self.data = copy.deepcopy(original)
                self.data["deployments"]["items"][0]["status"][field] = value
                self.reasons()
        self.data = copy.deepcopy(original)
        del self.data["deployments"]["items"][0]["spec"]["replicas"]
        self.assertIn("unknown_replica_state", self.reasons())

    def test_empty_missing_and_replaced_objects_cannot_pass(self):
        original = copy.deepcopy(self.data)
        for resource in ("deployments", "statefulsets", "persistentvolumeclaims", "nodes", "pods"):
            for replacement in (False, True):
                with self.subTest(resource=resource, replacement=replacement):
                    self.data = copy.deepcopy(original)
                    if replacement:
                        # A bare pod's identity is mandatory; managed pods can change.
                        item = self.data[resource]["items"][-1]
                        item["metadata"]["uid"] += "-replacement"
                    else:
                        self.data[resource]["items"] = []
                    if resource == "nodes" and not replacement:
                        continue  # The collector rejects fewer than three nodes.
                    self.assertIn("identity_missing_or_replaced", self.reasons())
        empty = self.capture()
        for key in ("deployments", "statefulsets", "pods"):
            empty["resources"][key]["items"] = []
        self.assertFalse(assess(empty, empty)["ready"])

    def test_namespace_identity_and_evidence_gaps_fail_closed(self):
        current = copy.deepcopy(self.baseline)
        current["namespace"]["uid"] += "-new"
        self.assertEqual(assess(self.baseline, current)["issues"][0]["reason"], "identity_changed")
        for resource in ("pods", "deployments", "statefulsets", "replicasets", "persistentvolumeclaims"):
            current = copy.deepcopy(self.baseline)
            current["resources"][resource] = {"status": "unavailable"}
            self.assertFalse(assess(self.baseline, current)["ready"])
        self.assertFalse(assess(self.baseline, {})["ready"])
        current = copy.deepcopy(self.baseline)
        current["resources"]["pods"]["items"][0].pop("controller_owner")
        self.assertFalse(assess(self.baseline, current)["ready"])

    def test_unready_terminating_and_unknown_container_pods_block(self):
        original = copy.deepcopy(self.data)
        for mutation in (lambda p: p["status"].update(phase="Failed"),
                         lambda p: p["status"].update(conditions=[]),
                         lambda p: p["status"].update(containerStatuses=[]),
                         lambda p: p["status"]["containerStatuses"][0].update(ready=False),
                         lambda p: p["metadata"].update(deletionTimestamp="2026-09-27T00:00:00Z"),
                         lambda p: p["spec"].update(nodeName="missing")):
            self.data = copy.deepcopy(original)
            mutation(self.data["pods"]["items"][0])
            self.assertIn("not_ready", self.reasons())
        self.data = copy.deepcopy(original)
        self.data["pods"]["items"][-1]["status"]["phase"] = "Succeeded"
        self.assertIn("baseline_pod_completed", self.reasons())

    def test_completed_jobs_and_finished_init_containers_do_not_block(self):
        job = copy.deepcopy(self.data["pods"]["items"][0])
        job["metadata"].update(name="finished-job", uid="job-uid")
        job["metadata"]["ownerReferences"][0].update(kind="Job", name="job", uid="job-uid")
        job["status"]["phase"] = "Succeeded"
        self.data["pods"]["items"].append(job)
        self.data["pods"]["items"][0]["status"]["initContainerStatuses"] = [
            {"name": "setup", "ready": False, "restartCount": 0, "state": {"terminated": {"exitCode": 0}}}]
        self.assertTrue(assess(self.baseline, self.capture())["ready"])

    def test_node_pressure_and_duplicate_conditions_block(self):
        for conditions in ([{"type": "Ready", "status": "Unknown"}],
                           [{"type": "Ready", "status": "True"}, {"type": "Ready", "status": "True"}],
                           [{"type": "Ready", "status": "True"}, {"type": "DiskPressure", "status": "True"}]):
            self.data["nodes"]["items"][1]["status"]["conditions"] = conditions
            self.reasons()
        self.data = cluster_fixture(self.config)
        self.data["nodes"]["items"][1]["metadata"]["deletionTimestamp"] = "2026-09-27T00:00:00Z"
        self.assertIn("not_ready", self.reasons())

    def test_volume_rebinding_and_deletion_block_even_while_bound(self):
        for field, value in (("volumeName", "new-volume"), ("storageClassName", "other"), ("accessModes", ["ReadWriteMany"])):
            data = copy.deepcopy(self.data)
            self.data["persistentvolumeclaims"]["items"][0]["spec"][field] = value
            self.assertIn("binding_changed", self.reasons())
            self.data = data
        self.data["persistentvolumeclaims"]["items"][0]["metadata"]["deletionTimestamp"] = "2026-09-27T00:00:00Z"
        self.assertIn("not_bound", self.reasons())

    def test_pod_counts_and_owner_chain_back_up_workload_status(self):
        self.data["pods"]["items"].pop(0)
        self.assertIn("pod_inventory_not_converged", self.reasons())
        self.data = cluster_fixture(self.config)
        self.data["replicasets"]["items"][0]["metadata"]["ownerReferences"][0]["uid"] = "wrong"
        self.assertIn("unsupported_or_missing_owner", self.reasons())
        self.data = cluster_fixture(self.config)
        self.data["pods"]["items"][0]["metadata"]["ownerReferences"][0]["uid"] = "wrong"
        self.assertIn("owner_missing_or_replaced", self.reasons())

    def test_autoscaling_uses_current_desired_counts_but_cannot_hide_scale_to_zero(self):
        # Baseline one, now two converged replicas: KEDA need not return to baseline.
        workload = self.data["statefulsets"]["items"][0]
        workload["spec"]["replicas"] = 2
        workload["metadata"]["generation"] = 2
        workload["status"] = dict(observedGeneration=2, replicas=2, readyReplicas=2, updatedReplicas=2)
        pod = copy.deepcopy(self.data["pods"]["items"][1])
        pod["metadata"].update(name="generator-1", uid="generator-1-uid")
        self.data["pods"]["items"].append(pod)
        self.assertTrue(assess(self.baseline, self.capture())["ready"])
        workload["spec"]["replicas"] = 0
        workload["status"] = dict(observedGeneration=2)
        self.data["pods"]["items"] = [p for p in self.data["pods"]["items"] if not p["metadata"]["name"].startswith("generator-")]
        self.assertIn("scaled_to_zero", self.reasons())


if __name__ == "__main__":
    unittest.main()
