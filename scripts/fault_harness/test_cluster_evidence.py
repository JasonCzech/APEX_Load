import copy
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest

from cluster_evidence import EvidenceError, MAX_ARTIFACT_BYTES, MAX_ITEMS, RESOURCES, snapshot, write_snapshot


class ClusterEvidenceTest(unittest.TestCase):
    def setUp(self):
        self.config = {"run_id": "a" * 16, "cluster": "apexload-fault-" + "a" * 16, "namespace": "apexload-fault"}
        self.namespace = {"metadata": {"name": "apexload-fault", "uid": "namespace-uid", "labels": {"apexload.io/fault-run": "a" * 16}}}
        self.nodes = {"items": [{"metadata": {"name": self.config["cluster"] + suffix, "uid": "node" + suffix},
                                "status": {"conditions": [{"type": "Ready", "status": "True", "message": "SECRET"}]}}
                               for suffix in ("-control-plane", "-worker", "-worker2")]}
        self.resources = {key: {"items": []} for key in RESOURCES}
        self.calls = []
        self.errors = {}

    def obj(self, name="example"):
        return {"metadata": {"name": name, "uid": name + "-uid", "namespace": self.config["namespace"],
                             "annotations": {"private": "SECRET"}, "labels": {"token": "SECRET"}}}

    def kube(self, *args):
        self.calls.append(args)
        self.assertEqual(args[0], "get")
        if args[1] in self.errors:
            raise self.errors[args[1]]
        if args[1] == "namespace":
            return copy.deepcopy(self.namespace)
        if args[1] == "nodes":
            return copy.deepcopy(self.nodes)
        return copy.deepcopy(self.resources[args[1]])

    def test_snapshot_keeps_topology_scaling_and_digests_but_excludes_sensitive_fields(self):
        pod = self.obj("generator-0")
        pod["spec"] = {"nodeName": self.config["cluster"] + "-worker", "containers": [{"env": [{"value": "SECRET"}]}]}
        pod["status"] = {"phase": "Running", "conditions": [{"type": "Ready", "status": "False", "message": "SECRET"}],
                         "containerStatuses": [{"name": "generator", "ready": False, "restartCount": 2,
                                                "image": "SECRET", "imageID": "SECRET@sha256:" + "b" * 64,
                                                "state": {"waiting": {"message": "SECRET"}}}]}
        self.resources["pods"]["items"] = [pod]
        hpa = self.obj("generator-hpa")
        hpa.update(spec={"minReplicas": 2, "maxReplicas": 8, "scaleTargetRef": {"name": "generator", "kind": "StatefulSet"},
                         "metrics": [{"query": "SECRET"}]}, status={"currentReplicas": 2, "desiredReplicas": 4})
        self.resources["horizontalpodautoscalers"]["items"] = [hpa]
        keda = self.obj("generator-demand")
        keda.update(spec={"minReplicaCount": 2, "maxReplicaCount": 8, "triggers": [{"metadata": {"password": "SECRET"}}]},
                    status={"hpaName": "generator-hpa", "conditions": [{"type": "Active", "status": "True"}]})
        self.resources["scaledobjects.keda.sh"]["items"] = [keda]
        event = self.obj("scheduling-event")
        event.update(type="Warning", reason="FailedScheduling", message="SECRET", count=3,
                     involvedObject={"kind": "Pod", "name": "generator-0", "uid": "generator-0-uid"},
                     lastTimestamp="2026-09-27T12:00:00Z")
        self.resources["events"]["items"] = [event]
        claim = self.obj("data-generator-0")
        claim.update(spec={"volumeName": "volume-1", "storageClassName": "standard", "accessModes": ["ReadWriteOnce"]}, status={"phase": "Bound"})
        self.resources["persistentvolumeclaims"]["items"] = [claim]
        deployment = self.obj("controller")
        deployment.update(spec={"replicas": 2, "template": {"secret": "SECRET"}}, status={"replicas": 2, "readyReplicas": 1})
        self.resources["deployments"]["items"] = [deployment]
        evidence = snapshot(self.config, self.kube)
        self.assertTrue(evidence["inventory_complete"])  # observed does not mean healthy
        resources = evidence["resources"]
        self.assertEqual(resources["pods"]["items"][0]["containers"][0]["image_digest"], "sha256:" + "b" * 64)
        self.assertEqual(resources["pods"]["items"][0]["containers"][0]["restarts"], 2)
        self.assertEqual(resources["horizontalpodautoscalers"]["items"][0]["status"]["desiredReplicas"], 4)
        self.assertEqual(resources["persistentvolumeclaims"]["items"][0]["phase"], "Bound")
        self.assertEqual(resources["deployments"]["items"][0]["status"]["readyReplicas"], 1)
        self.assertNotIn("SECRET", json.dumps(evidence))
        self.assertEqual(self.calls[0], ("get", "namespace", "apexload-fault"))
        self.assertEqual(self.calls[-1], self.calls[0])
        self.assertNotIn("secrets", [call[1] for call in self.calls])

    def test_missing_crd_and_transport_errors_are_gaps_not_empty_successes(self):
        for error in (RuntimeError("SECRET forbidden"), TimeoutError("SECRET timeout")):
            with self.subTest(error=type(error)):
                self.errors["scaledobjects.keda.sh"] = error
                evidence = snapshot(self.config, self.kube)
                self.assertFalse(evidence["inventory_complete"])
                self.assertEqual(evidence["resources"]["scaledobjects.keda.sh"], {"status": "unavailable"})
                self.assertNotIn("SECRET", json.dumps(evidence))

    def test_invalid_inventory_is_not_silently_truncated_or_accepted(self):
        wrong_namespace = self.obj()
        wrong_namespace["metadata"]["namespace"] = "other"
        for payload in ({"items": [self.obj()] * (MAX_ITEMS + 1)}, {"items": [], "metadata": {"continue": "SECRET"}},
                        {"items": [wrong_namespace]}, {"items": [self.obj(), self.obj()]}, {"items": None},
                        {"items": [{**self.obj(), "status": {"conditions": None}}]}):
            with self.subTest(payload=str(payload)[:60]):
                self.resources["pods"] = payload
                evidence = snapshot(self.config, self.kube)
                self.assertEqual(evidence["resources"]["pods"], {"status": "invalid_response"})
                self.assertFalse(evidence["inventory_complete"])

    def test_counts_and_unknown_states_fail_closed(self):
        for value in (True, -1, float("nan"), "2", 2**64):
            with self.subTest(value=value):
                self.resources["horizontalpodautoscalers"]["items"] = [{**self.obj(), "status": {"desiredReplicas": value}}]
                self.assertEqual(snapshot(self.config, self.kube)["resources"]["horizontalpodautoscalers"]["status"], "invalid_response")
        self.resources["pods"]["items"] = [{**self.obj(), "status": {"phase": "SECRET"}}]
        evidence = snapshot(self.config, self.kube)
        self.assertEqual(evidence["resources"]["pods"]["status"], "invalid_response")
        self.assertNotIn("SECRET", json.dumps(evidence))

    def test_wrong_owner_or_foreign_nodes_abort_before_resource_reads(self):
        self.namespace["metadata"]["labels"]["apexload.io/fault-run"] = "other"
        with self.assertRaises(EvidenceError):
            snapshot(self.config, self.kube)
        self.assertEqual(len(self.calls), 1)
        self.namespace["metadata"]["labels"]["apexload.io/fault-run"] = "a" * 16
        self.nodes["items"][0]["metadata"]["name"] = "production-node"
        with self.assertRaises(EvidenceError):
            snapshot(self.config, self.kube)
        self.assertFalse(any(call[1] in RESOURCES for call in self.calls))

    def test_namespace_replacement_invalidates_snapshot(self):
        def kube(*args):
            if args[1] == "events":
                self.namespace["metadata"]["uid"] = "replacement"
            return self.kube(*args)
        with self.assertRaises(EvidenceError):
            snapshot(self.config, kube)

    def test_cli_uses_explicit_read_only_scope_and_retains_partial_inventory(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            kubeconfig = root / "kubeconfig"
            kubeconfig.touch()
            config = {**self.config, "context": "kind-" + self.config["cluster"], "kubeconfig": str(kubeconfig),
                      "baseline_seconds": 10, "fault_seconds": 30, "recovery_seconds": 20,
                      "expected_vusers": 10, "minimum_target_rps": 5, "observation_command": ["unused"]}
            config_path = root / "config.json"
            config_path.write_text(json.dumps(config))
            (root / "responses.json").write_text(json.dumps({"namespace": self.namespace, "nodes": self.nodes, **self.resources}))
            kubectl = root / "kubectl"
            kubectl.write_text(r"""#!/usr/bin/env python3
import json, sys
from pathlib import Path
root = Path(__file__).parent
args = sys.argv[1:]
with (root / 'calls.jsonl').open('a') as stream:
    stream.write(json.dumps(args) + '\n')
resource = args[args.index('get') + 1]
if resource == 'events' and (root / 'fail-events').exists():
    print('SECRET kubeconfig plugin stderr', file=sys.stderr)
    sys.exit(1)
print(json.dumps(json.loads((root / 'responses.json').read_text())[resource]))
""")
            kubectl.chmod(0o700)
            argv = [sys.executable, str(Path(__file__).with_name("cluster_evidence.py")), "--config", str(config_path), "--output", str(root / "out")]
            environment = {**os.environ, "PATH": str(root) + os.pathsep + os.environ.get("PATH", "")}
            result = subprocess.run(argv, env=environment, capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stderr)
            evidence = json.loads((root / "out/cluster.json").read_text())
            self.assertTrue(evidence["inventory_complete"])
            self.assertEqual(stat.S_IMODE((root / "out").stat().st_mode), 0o700)
            for call in (root / "calls.jsonl").read_text().splitlines():
                args = json.loads(call)
                self.assertEqual(args[args.index("--kubeconfig") + 1], str(kubeconfig))
                self.assertEqual(args[args.index("--context") + 1], config["context"])
                self.assertEqual(args[args.index("-n") + 1], config["namespace"])
                self.assertIn("get", args)
            result = subprocess.run(argv, env=environment, capture_output=True, text=True, timeout=15)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(json.loads((root / "out/cluster.json").read_text()), evidence)
            (root / "fail-events").touch()
            argv[-1] = str(root / "partial")
            result = subprocess.run(argv, env=environment, capture_output=True, text=True, timeout=15)
            self.assertNotEqual(result.returncode, 0)
            partial = json.loads((root / "partial/cluster.json").read_text())
            self.assertFalse(partial["inventory_complete"])
            self.assertEqual(partial["resources"]["events"], {"status": "unavailable"})
            self.assertNotIn("SECRET", result.stdout + result.stderr + json.dumps(partial))

    def test_private_artifact_refuses_overwrite_and_symlinks(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "cluster.json"
            write_snapshot(path, {"complete": False})
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                write_snapshot(path, {"complete": True})
            link = Path(root) / "link"
            link.symlink_to(path)
            with self.assertRaises(FileExistsError):
                write_snapshot(link, {})
            self.assertEqual(json.loads(path.read_text()), {"complete": False})
            oversized = Path(root) / "oversized.json"
            with self.assertRaises(EvidenceError):
                write_snapshot(oversized, {"value": "x" * MAX_ARTIFACT_BYTES})
            self.assertFalse(oversized.exists())


if __name__ == "__main__":
    unittest.main()
