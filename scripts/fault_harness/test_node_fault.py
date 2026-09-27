import json
import hashlib
import copy
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from node_fault import GateError, ObservationSafetyError, Harness, public_observation, validate_config, validate_observation
from test_cluster_readiness import cluster_fixture
from cluster_sampler import ClusterSampler


class HarnessTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        kubeconfig = Path(self.temp.name) / "kubeconfig"
        kubeconfig.touch()
        self.config = dict(run_id="a" * 16, cluster="apexload-fault-" + "a" * 16,
                           context="kind-apexload-fault-" + "a" * 16, namespace="apexload-fault",
                           kubeconfig=str(kubeconfig), baseline_seconds=10, fault_seconds=30,
                           recovery_seconds=20, expected_vusers=10, minimum_target_rps=5,
                           observation_command=["observe"])
        self.node = self.config["cluster"] + "-worker"
        self.now, self.killed, self.started = 100.0, False, []
        self.fault_mode = None
        self.fault_at = None
        self.bad_observation_sent = False
        self.harness_count = 0
        self.labels = {"io.x-k8s.kind.cluster": self.config["cluster"], "io.x-k8s.kind.role": "worker"}
        self.namespace_owner = self.config["run_id"]
        self.clock_patch = patch("node_fault.time.time", lambda: self.now)
        self.clock_patch.start()
        self.addCleanup(self.clock_patch.stop)

    def observation(self):
        return dict(run_id=self.config["run_id"], target_instance_id="a" * 32, observed_at_unix=self.now, target_requests=int(self.now * 10),
                    observed_vusers=10, keda_total_demand=10, controller_restarts=0, status="RUNNING",
                    assignments=[dict(group="http", shard="0", pod="generator-0", node=self.node, generation=1)])

    def execute(self, argv, timeout=20):
        if argv[0] == "observe":
            data = self.observation()
            if self.killed and self.fault_mode == "outage-once" and not self.bad_observation_sent:
                self.bad_observation_sent = True
                raise GateError("temporary observation transport failure")
            if self.killed and self.fault_mode == "interrupt":
                raise KeyboardInterrupt
            if self.fault_mode == "no-keda":
                data["keda_total_demand"] = 0
            if self.killed and self.fault_mode == "restart":
                data["controller_restarts"] = 1
            if self.killed and self.fault_mode == "no-recovery":
                data["observed_vusers"] = 0
            if self.killed and self.fault_mode == "duplicate-once" and not self.bad_observation_sent:
                data["assignments"] *= 2
                self.bad_observation_sent = True
            if self.killed and self.fault_mode == "terminal-after-recovery" and self.now - self.fault_at > 15:
                data["status"] = "FAILED"
            if self.killed and self.fault_mode == "reset-after-outage":
                if self.now - self.fault_at == 1:
                    raise GateError("temporary observation transport failure")
                if self.now - self.fault_at >= 2:
                    data["target_requests"] -= 500
            if self.killed and self.fault_mode == "target-restart-after-outage":
                if self.now - self.fault_at == 1:
                    raise GateError("temporary observation transport failure")
                if self.now - self.fault_at >= 2:
                    # The replacement counter already exceeds its predecessor.
                    data["target_instance_id"] = "b" * 32
                    data["target_requests"] += 500
            if self.killed and self.fault_mode == "stale-target-restart-once" and not self.bad_observation_sent:
                data["target_instance_id"] = "b" * 32
                data["observed_at_unix"] -= 10
                self.bad_observation_sent = True
            if self.killed and self.fault_mode == "missing-target-identity":
                del data["target_instance_id"]
            if self.fault_mode == "target-restart-baseline" and self.now >= 105:
                data["target_instance_id"] = "b" * 32
            return json.dumps(data)
        if argv[:2] == ["docker", "kill"]:
            self.killed = True
            self.fault_at = self.now
            if self.fault_mode == "kill-timeout":
                raise subprocess.TimeoutExpired(argv, 20)
            return ""
        if argv[:2] == ["docker", "start"]:
            self.started.append(argv[-1])
            return ""
        if argv[:2] == ["docker", "inspect"]:
            return json.dumps([dict(Id="immutable-id", Image="sha256:image", Name="/" + self.node,
                                    Config=dict(Labels=self.labels), State=dict(Running=True))])
        if "namespace" in argv:
            return json.dumps(dict(metadata=dict(labels={"apexload.io/fault-run": self.namespace_owner})))
        return json.dumps(dict(items=[dict(metadata=dict(name=self.config["cluster"] + suffix, labels=labels))
                         for suffix, labels in [("-control-plane", {"node-role.kubernetes.io/control-plane": ""}),
                                                ("-worker", {}), ("-worker2", {})]]))

    def harness(self):
        def sleep(seconds):
            self.now += seconds
        self.harness_count += 1
        return Harness(self.config, Path(self.temp.name) / f"evidence-{self.harness_count}", self.execute, lambda: self.now, sleep)

    def enable_cluster_evidence(self, mode=None):
        self.config["capture_cluster_evidence"] = True
        original = self.execute
        self.inventory_reads = []
        namespace_calls = 0

        def execute(argv, timeout=20):
            nonlocal namespace_calls
            if argv[0] != "kubectl":
                return original(argv, timeout)
            resource = argv[argv.index("get") + 1]
            self.inventory_reads.append((resource, self.killed, bool(self.started)))
            if resource == "namespace":
                namespace_calls += 1
                if mode == "after-restore-error" and self.started:
                    raise GateError("SECRET transport error")
                value = json.loads(original(argv, timeout))
                value["metadata"].update(name=self.config["namespace"], uid="namespace-uid")
                if mode == "ownership-change" and namespace_calls > 1:
                    value["metadata"]["labels"]["apexload.io/fault-run"] = "other"
                return json.dumps(value)
            if resource == "nodes":
                value = json.loads(original(argv, timeout))
                for index, node in enumerate(value["items"]):
                    node["metadata"]["uid"] = "node-" + str(index)
                return json.dumps(value)
            if mode == "missing-keda" and resource == "scaledobjects.keda.sh":
                raise GateError("SECRET missing CRD")
            return json.dumps({"items": []})
        self.execute = execute

    def enable_readiness(self, mode=None, budget=12):
        self.config.update(capture_cluster_evidence=True, restoration_readiness_seconds=budget)
        original = self.execute
        self.cluster_data = cluster_fixture(self.config)
        self.readiness_reads = []
        self.restored_at = None

        def execute(argv, timeout=20):
            if argv[0] != "kubectl":
                result = original(argv, timeout)
                if argv[:2] == ["docker", "start"]:
                    self.restored_at = self.now
                return result
            resource = argv[argv.index("get") + 1]
            self.readiness_reads.append((resource, self.killed, bool(self.started), timeout, self.now))
            data = copy.deepcopy(self.cluster_data[resource])
            if self.started:
                if mode == "slow":
                    self.now += timeout
                    raise subprocess.TimeoutExpired(argv, timeout, stderr="SECRET")
                if mode == "outage":
                    raise GateError("SECRET cluster unavailable")
                if mode == "events-outage" and resource == "events":
                    raise GateError("SECRET events unavailable")
                if mode == "late-ready":
                    self.now += 1
                if resource == "pods" and (mode == "never-ready" or (mode == "delayed" and self.now - self.restored_at < 5)):
                    data["items"][0]["status"]["conditions"][0]["status"] = "False"
                if mode == "namespace-replaced" and resource == "namespace":
                    data["metadata"]["uid"] = "replacement"
            return json.dumps(data)
        self.execute = execute

    def test_readiness_retries_after_restoration_without_delaying_fault_cleanup(self):
        self.enable_readiness("delayed")
        harness = self.harness()
        harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertTrue(all(not killed or restored for _, killed, restored, _, _ in self.readiness_reads))
        report = json.loads((harness.output / "cluster-readiness.json").read_text())
        self.assertTrue(report["ready"])
        self.assertEqual(report["attempts"], 2)
        self.assertEqual(report["elapsed_seconds"], 5)
        kinds = [event["event"] for event in harness.events]
        self.assertLess(kinds.index("node_restored"), kinds.index("cluster_readiness_started"))
        self.assertIn("traffic_gate_passed", kinds)
        self.assertEqual(kinds[-1], "cluster_readiness_passed")
        event = harness.events[-1]
        path = harness.output / event["file"]
        self.assertEqual(event["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertTrue((harness.output / "cluster-restored.json").is_file())

    def test_readiness_deadline_and_unavailable_cluster_fail_after_traffic_pass(self):
        for mode in ("never-ready", "outage", "slow", "namespace-replaced", "late-ready"):
            with self.subTest(mode=mode):
                self.now, self.killed, self.started = 100.0, False, []
                self.execute = HarnessTest.execute.__get__(self)
                self.enable_readiness(mode, budget=10)
                harness = self.harness()
                with self.assertRaisesRegex(GateError, "restoration readiness deadline"):
                    harness.run(self.node)
                self.assertEqual(self.started, ["immutable-id"])
                report = json.loads((harness.output / "cluster-readiness.json").read_text())
                self.assertFalse(report["ready"])
                self.assertTrue(report["deadline_exceeded"])
                self.assertEqual(report["elapsed_seconds"], 10)
                self.assertIn("traffic_gate_passed", [e["event"] for e in harness.events])
                self.assertEqual(harness.events[-1]["event"], "cluster_readiness_failed")
                for _, _, restored, timeout, at in self.readiness_reads:
                    if restored:
                        self.assertGreater(timeout, 0)
                        self.assertLessEqual(at + timeout, self.restored_at + 10)
                self.assertNotIn("SECRET", json.dumps(harness.events) + json.dumps(report))

    def test_unready_or_unsupported_baseline_prevents_injection(self):
        for failure in ("unready", "unsupported-owner", "no-workloads"):
            with self.subTest(failure=failure):
                self.execute = HarnessTest.execute.__get__(self)
                self.enable_readiness()
                if failure == "unready":
                    self.cluster_data["pods"]["items"][0]["status"]["phase"] = "Pending"
                elif failure == "unsupported-owner":
                    self.cluster_data["pods"]["items"][0]["metadata"]["ownerReferences"][0]["kind"] = "DaemonSet"
                else:
                    self.cluster_data["deployments"]["items"] = []
                    self.cluster_data["statefulsets"]["items"] = []
                with self.assertRaisesRegex(GateError, "baseline is not ready"):
                    self.harness().run(self.node)
                self.assertFalse(self.killed)

    def test_readiness_failure_keeps_original_safety_error_and_skips_on_interrupt(self):
        self.enable_readiness("never-ready")
        self.fault_mode = "duplicate-once"
        harness = self.harness()
        with self.assertRaises(ObservationSafetyError):
            harness.run(self.node)
        self.assertEqual(harness.events[-1]["event"], "cluster_readiness_failed")
        self.assertEqual(self.started, ["immutable-id"])
        self.now, self.killed, self.started, self.fault_mode = 100.0, False, [], "interrupt"
        harness = self.harness()
        with self.assertRaises(KeyboardInterrupt):
            harness.run(self.node)
        self.assertEqual(harness.events[-1]["event"], "cluster_readiness_skipped")
        self.assertEqual(self.started, ["immutable-id"])
        self.assertFalse((harness.output / "cluster-readiness.json").exists())

    def test_readiness_does_not_claim_optional_diagnostic_gaps_are_complete(self):
        self.enable_readiness("events-outage")
        harness = self.harness()
        harness.run(self.node)
        evidence = json.loads((harness.output / "cluster-restored.json").read_text())
        self.assertFalse(evidence["inventory_complete"])
        self.assertTrue(json.loads((harness.output / "cluster-readiness.json").read_text())["ready"])

    def test_readiness_artifact_conflict_fails_and_preserves_trial_failure(self):
        self.enable_readiness()
        for failed in (False, True):
            with self.subTest(failed=failed):
                self.now, self.killed, self.started, self.bad_observation_sent = 100.0, False, [], False
                self.fault_mode = "duplicate-once" if failed else None
                harness = self.harness()
                path = harness.output / "cluster-readiness.json"
                path.write_text("preserve")
                with self.assertRaises(ObservationSafetyError if failed else GateError):
                    harness.run(self.node)
                self.assertEqual(path.read_text(), "preserve")
                self.assertEqual(self.started, ["immutable-id"])

    def test_readiness_budget_requires_explicit_finite_bound_and_evidence(self):
        for value in (None, False, True, 0, -1, 1801, float("nan"), float("inf"), "10"):
            with self.subTest(value=value), self.assertRaises(GateError):
                validate_config({**self.config, "capture_cluster_evidence": True, "restoration_readiness_seconds": value})
        with self.assertRaises(GateError):
            validate_config({**self.config, "restoration_readiness_seconds": 10})

    def enable_sampling(self, mode=None):
        self.enable_cluster_evidence()
        self.config["cluster_evidence_interval_seconds"] = 5
        self.sampling_calls = []
        owner = self

        class Sampler:
            def __init__(self, config, namespace, output, kube, **kwargs):
                self.namespace = namespace
                Path(output).mkdir(mode=0o700, exist_ok=False)
                owner.sampling_calls.append(("prepare", owner.killed, bool(owner.started)))

            def start(self):
                owner.sampling_calls.append(("start", owner.killed, bool(owner.started)))
                if mode == "start-failure":
                    raise OSError("SECRET thread start failure")

            def stop(self):
                owner.sampling_calls.append(("stop", owner.killed, bool(owner.started)))

            def finish(self):
                owner.sampling_calls.append(("finish", owner.killed, bool(owner.started)))
                if mode == "finish-failure":
                    raise OSError("SECRET index failure")
                return {"schema_version": 1, "namespace": self.namespace, "worker_stopped": mode != "hung",
                        "stop_reason": "stopped" if mode is None else mode, "attempts": []}
        replacement = patch("node_fault.ClusterSampler", Sampler)
        replacement.start()
        self.addCleanup(replacement.stop)

    def test_sampling_starts_after_injection_and_finishes_after_restoration(self):
        self.enable_sampling()
        harness = self.harness()
        harness.run(self.node)
        self.assertEqual(self.sampling_calls, [("prepare", False, False), ("start", True, False),
                                               ("stop", True, False), ("finish", True, True)])
        event = next(e for e in harness.events if e["event"] == "cluster_sampling_finished")
        path = harness.output / event["file"]
        self.assertEqual(event["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(event["observed_samples"], 0)
        self.assertEqual(harness.events[-1]["event"], "cluster_evidence")

    def test_sampling_failures_fail_trial_only_after_restoration_and_keep_primary_error(self):
        for mode in ("hung", "artifact_limit", "scope_or_inventory_invalid", "finish-failure", "start-failure"):
            for fault in (None, "duplicate-once"):
                with self.subTest(mode=mode, fault=fault):
                    self.now, self.killed, self.started, self.bad_observation_sent = 100.0, False, [], False
                    self.execute = HarnessTest.execute.__get__(self)
                    self.enable_sampling(mode)
                    self.fault_mode = fault
                    harness = self.harness()
                    expected = ObservationSafetyError if fault and mode != "start-failure" else GateError
                    with self.assertRaises(expected) as raised:
                        harness.run(self.node)
                    self.assertEqual(self.started, ["immutable-id"])
                    self.assertEqual(self.sampling_calls[-1], ("finish", True, True))
                    self.assertEqual(harness.events[-1]["event"], "cluster_evidence")
                    self.assertNotIn("SECRET", str(raised.exception) + json.dumps(harness.events))

    def test_sampling_stops_on_interrupt_and_ambiguous_kill_without_masking_it(self):
        self.enable_sampling()
        for fault in ("interrupt", "kill-timeout"):
            self.now, self.killed, self.started, self.sampling_calls = 100.0, False, [], []
            self.fault_mode = fault
            harness = self.harness()
            with self.assertRaises(KeyboardInterrupt if fault == "interrupt" else subprocess.TimeoutExpired):
                harness.run(self.node)
            self.assertEqual(self.started, ["immutable-id"])
            self.assertEqual(self.sampling_calls[-1], ("finish", True, True))
            if fault == "kill-timeout":
                self.assertFalse(any(call[0] == "start" for call in self.sampling_calls))

    def test_restore_failure_still_finishes_sampler_and_has_precedence(self):
        self.enable_sampling("finish-failure")
        original = self.execute
        order = []
        def execute(argv, timeout=20):
            if argv[:2] == ["docker", "start"]:
                order.append("restore_attempted")
                raise GateError("Docker restoration failed")
            return original(argv, timeout)
        self.execute = execute
        harness = self.harness()
        with self.assertRaisesRegex(GateError, "Docker restoration failed"):
            harness.run(self.node)
        self.assertEqual(order, ["restore_attempted"])
        self.assertEqual(self.sampling_calls[-1][0], "finish")

    def test_sampling_artifact_conflicts_prevent_injection_or_fail_finalization(self):
        self.enable_sampling()
        harness = self.harness()
        (harness.output / "cluster-series").mkdir()
        with self.assertRaisesRegex(GateError, "could not be prepared"):
            harness.run(self.node)
        self.assertFalse(self.killed)
        harness = self.harness()
        path = harness.output / "cluster-series.json"
        path.write_text("preserve")
        with self.assertRaisesRegex(GateError, "could not be finalized"):
            harness.run(self.node)
        self.assertEqual(path.read_text(), "preserve")
        self.assertEqual(self.started, ["immutable-id"])

    def test_sampling_budget_requires_evidence_and_bounded_numeric_interval(self):
        for value in (None, True, False, 0, 4, 301, float("inf"), float("nan"), "10"):
            with self.subTest(value=value), self.assertRaises(GateError):
                validate_config({**self.config, "capture_cluster_evidence": True, "cluster_evidence_interval_seconds": value})
        with self.assertRaises(GateError):
            validate_config({**self.config, "cluster_evidence_interval_seconds": 5})

    def test_sampling_failure_still_runs_optional_restoration_readiness(self):
        self.enable_sampling("finish-failure")
        self.enable_readiness()
        harness = self.harness()
        with self.assertRaisesRegex(GateError, "background cluster evidence could not be finalized"):
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertTrue(json.loads((harness.output / "cluster-readiness.json").read_text())["ready"])

    def test_real_background_blocked_read_does_not_postpone_docker_restoration(self):
        self.enable_cluster_evidence()
        self.config["cluster_evidence_interval_seconds"] = 5
        entered, release = threading.Event(), threading.Event()
        original = self.execute
        expected_namespace = {"name": self.config["namespace"], "uid": "namespace-uid"}

        def execute(argv, timeout=20):
            if argv[0] == "observe" and self.killed and not self.started:
                self.assertTrue(entered.wait(2))
            if argv[:2] == ["docker", "start"]:
                self.assertTrue(entered.is_set())
                self.assertFalse(release.is_set(), "sampling was awaited before restoring Docker")
                result = original(argv, timeout)
                release.set()
                return result
            return original(argv, timeout)

        def factory(config, namespace, output, kube, **kwargs):
            self.assertEqual(namespace, expected_namespace)
            def blocked(*args, deadline):
                entered.set()
                release.wait(3)
                return {"metadata": {**namespace, "labels": {"apexload.io/fault-run": config["run_id"]}}}
            return ClusterSampler(config, namespace, output, blocked, clock=time.monotonic)

        self.execute = execute
        self.addCleanup(release.set)
        with patch("node_fault.ClusterSampler", factory):
            harness = self.harness()
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertEqual(self.now - self.fault_at, self.config["fault_seconds"])
        report = json.loads((harness.output / "cluster-series.json").read_text())
        self.assertTrue(report["worker_stopped"])
        self.assertEqual(report["attempts"][0]["status"], "cancelled")

    def test_cluster_inventory_is_outside_the_fault_timing_window(self):
        self.enable_cluster_evidence()
        harness = self.harness()
        harness.run(self.node)
        events = [event for event in harness.events if event["event"] == "cluster_evidence"]
        self.assertEqual([event["phase"] for event in events], ["preflight", "restored"])
        for event in events:
            path = harness.output / event["file"]
            self.assertTrue(event["inventory_complete"])
            self.assertEqual(event["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertTrue(all(not killed or restored for _, killed, restored in self.inventory_reads))
        kinds = [event["event"] for event in harness.events]
        self.assertLess(kinds.index("cluster_evidence"), kinds.index("observation"))
        self.assertLess(kinds.index("node_restored"), len(kinds) - 1)
        self.assertEqual(kinds[-1], "cluster_evidence")
        self.assertEqual(self.started, ["immutable-id"])

    def test_cluster_ownership_change_prevents_injection(self):
        self.enable_cluster_evidence("ownership-change")
        harness = self.harness()
        with self.assertRaises(GateError):
            harness.run(self.node)
        self.assertFalse(self.killed)
        self.assertEqual(harness.events[-1]["event"], "cluster_evidence_unavailable")

    def test_cluster_gaps_do_not_masquerade_as_complete_inventory(self):
        self.enable_cluster_evidence("missing-keda")
        harness = self.harness()
        harness.run(self.node)
        events = [event for event in harness.events if event["event"] == "cluster_evidence"]
        self.assertEqual(len(events), 2)
        for event in events:
            self.assertFalse(event["inventory_complete"])
            self.assertEqual(event["gaps"], ["scaledobjects.keda.sh"])
        self.assertNotIn("SECRET", (harness.output / "events.json").read_text())

    def test_after_restore_inventory_outage_preserves_original_safety_failure(self):
        self.enable_cluster_evidence("after-restore-error")
        self.fault_mode = "duplicate-once"
        harness = self.harness()
        with self.assertRaises(ObservationSafetyError):
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertEqual(harness.events[-1]["event"], "cluster_evidence_unavailable")
        self.assertEqual(harness.events[-1]["phase"], "restored")
        self.assertNotIn("SECRET", (harness.output / "events.json").read_text())

    def test_after_restore_artifact_errors_do_not_replace_trial_failure(self):
        self.enable_cluster_evidence()
        self.fault_mode = "duplicate-once"
        harness = self.harness()
        original = harness.record

        def record(kind, **fields):
            if fields.get("phase") == "restored":
                raise OSError("SECRET evidence disk error")
            return original(kind, **fields)
        harness.record = record
        with self.assertRaises(ObservationSafetyError):
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertNotIn("SECRET", (harness.output / "events.json").read_text())
        # If the trial otherwise succeeds, recording failure must still surface.
        self.killed, self.started, self.fault_mode = False, [], None
        harness = self.harness()
        original = harness.record
        harness.record = record
        with self.assertRaisesRegex(GateError, "restored cluster evidence could not be recorded"):
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])

    def test_checkpoint_index_write_failure_cannot_be_reclassified_as_read_outage(self):
        self.enable_cluster_evidence()
        harness = self.harness()
        original = harness.record

        def record(kind, **fields):
            if kind == "cluster_evidence" and fields.get("phase") == "restored":
                raise OSError("SECRET index error")
            return original(kind, **fields)
        harness.record = record
        with self.assertRaisesRegex(GateError, "restored cluster evidence could not be recorded"):
            harness.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertFalse(any(e["event"] == "cluster_evidence_unavailable" for e in harness.events))

    def test_preflight_artifact_conflict_prevents_injection_without_overwrite(self):
        self.enable_cluster_evidence()
        harness = self.harness()
        path = harness.output / "cluster-preflight.json"
        path.write_text("preserve")
        with self.assertRaises(GateError):
            harness.run(self.node)
        self.assertFalse(self.killed)
        self.assertEqual(path.read_text(), "preserve")

    def test_existing_evidence_directory_is_never_overwritten(self):
        existing = Path(self.temp.name) / "old-evidence"
        existing.mkdir()
        (existing / "events.json").write_text("preserve")
        with self.assertRaises(FileExistsError):
            Harness(self.config, existing, self.execute)
        self.assertEqual((existing / "events.json").read_text(), "preserve")

    def test_wrong_context_and_nonfinite_budget_rejected(self):
        for key, value in [("context", "docker-desktop"), ("cluster", "production"), ("fault_seconds", float("nan")),
                           ("expected_vusers", True), ("capture_cluster_evidence", "yes"), ("capture_cluster_evidence", 1), ("observation_command", "echo dangerous")]:
            with self.subTest(key=key), self.assertRaises(GateError):
                validate_config({**self.config, key: value})

    def test_wrong_cluster_namespace_and_controlplane_never_killed(self):
        for failure in ("cluster", "namespace", "control-plane"):
            with self.subTest(failure=failure):
                self.labels["io.x-k8s.kind.cluster"] = "other" if failure == "cluster" else self.config["cluster"]
                self.namespace_owner = "other" if failure == "namespace" else self.config["run_id"]
                node = self.config["cluster"] + "-control-plane" if failure == "control-plane" else self.node
                with self.assertRaises(GateError):
                    self.harness().run(node)
                self.assertFalse(self.killed)

    def test_clean_traffic_recovery_and_immutable_restore(self):
        h = self.harness()
        h.run(self.node)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertIn("traffic_gate_passed", [e["event"] for e in h.events])

    def test_restore_on_restart_failure_and_ambiguous_kill_timeout(self):
        for mode in ("restart", "kill-timeout", "no-recovery", "interrupt"):
            with self.subTest(mode=mode):
                self.now, self.killed, self.started, self.fault_mode = 100.0, False, [], mode
                with self.assertRaises((GateError, subprocess.TimeoutExpired, KeyboardInterrupt)):
                    self.harness().run(self.node)
                self.assertEqual(self.started, ["immutable-id"])

    def test_no_load_or_no_keda_prevents_fault(self):
        for key in ("expected_vusers", "minimum_target_rps"):
            with self.subTest(key=key):
                self.config[key] = 1000
                with self.assertRaises(GateError):
                    self.harness().run(self.node)
                self.assertFalse(self.killed)
                self.config[key] = 10

    def test_missing_keda_demand_prevents_fault(self):
        self.fault_mode = "no-keda"
        with self.assertRaises(GateError):
            self.harness().run(self.node)
        self.assertFalse(self.killed)

    def test_recovery_reports_completion_of_sustained_window(self):
        h = self.harness()
        h.run(self.node)
        recovered = next(e for e in h.events if e["event"] == "traffic_recovered")
        passed = next(e for e in h.events if e["event"] == "traffic_gate_passed")
        self.assertEqual(recovered["healthy_since_seconds"], 1)
        self.assertEqual(recovered["elapsed"], 11)
        self.assertEqual(passed["recovery_seconds"], 11)

    def test_entire_recovery_window_must_finish_before_deadline(self):
        for healthy_at, passes in ((9, True), (10, False), (12, False)):
            with self.subTest(healthy_at=healthy_at):
                self.now, self.killed, self.started = 100, False, []
                original = HarnessTest.execute.__get__(self)
                def execute(argv, timeout=20):
                    raw = original(argv, timeout)
                    if argv == ["observe"] and self.killed and self.now - self.fault_at < healthy_at:
                        data = json.loads(raw)
                        data["observed_vusers"] = 0
                        return json.dumps(data)
                    return raw
                self.execute = execute
                h = self.harness()
                if passes:
                    h.run(self.node)
                    recovered = next(e for e in h.events if e["event"] == "traffic_recovered")
                    self.assertEqual(recovered["elapsed"], healthy_at + 10)
                else:
                    with self.assertRaisesRegex(GateError, "recovery deadline"):
                        h.run(self.node)
                    self.assertNotIn("traffic_gate_passed", [e["event"] for e in h.events])
                    self.assertEqual(self.now - self.fault_at, 20)
                self.assertEqual(self.started, ["immutable-id"])

    def test_baseline_response_at_deadline_never_injects(self):
        original = self.execute
        observed_timeouts = []
        def execute(argv, timeout=20):
            if argv == ["observe"] and self.now >= 110:
                observed_timeouts.append(timeout)
                self.now += timeout
            return original(argv, timeout)
        self.execute = execute
        with self.assertRaisesRegex(GateError, "baseline observation deadline"):
            self.harness().run(self.node)
        self.assertEqual(observed_timeouts, [10])
        self.assertEqual(self.now, 120)
        self.assertFalse(self.killed)

    def test_slow_recovery_response_cannot_cross_deadline(self):
        original = self.execute
        observed_timeouts = []
        def execute(argv, timeout=20):
            if argv == ["observe"] and self.killed and self.now - self.fault_at >= 10:
                observed_timeouts.append(timeout)
                self.now += timeout
            return original(argv, timeout)
        self.execute = execute
        h = self.harness()
        with self.assertRaisesRegex(GateError, "recovery deadline"):
            h.run(self.node)
        self.assertEqual(observed_timeouts, [10])
        self.assertEqual(self.started, ["immutable-id"])
        self.assertNotIn("traffic_recovered", [e["event"] for e in h.events])

    def test_recovery_time_uses_observation_completion_not_request_start(self):
        original = self.execute
        def execute(argv, timeout=20):
            if argv == ["observe"] and self.killed and self.now - self.fault_at == 11:
                self.now += 0.25
            return original(argv, timeout)
        self.execute = execute
        h = self.harness()
        h.run(self.node)
        recovered = next(e for e in h.events if e["event"] == "traffic_recovered")
        self.assertEqual(recovered["elapsed"], 11.25)
        self.assertEqual(recovered["healthy_since_seconds"], 1)

    def test_observation_cannot_postpone_scheduled_restoration(self):
        for fault_seconds in (15, 30):
            with self.subTest(fault_seconds=fault_seconds):
                self.now, self.killed, self.started = 100, False, []
                self.config["fault_seconds"] = fault_seconds
                original = HarnessTest.execute.__get__(self)
                stopped_reads, restored_at = [], []
                def execute(argv, timeout=20):
                    if argv == ["observe"] and self.killed and not self.started:
                        stopped_reads.append((self.now - self.fault_at, timeout))
                        if self.now - self.fault_at == fault_seconds - 1:
                            self.now += timeout
                            raise subprocess.TimeoutExpired(argv, timeout, stderr="SECRET")
                    if argv[:2] == ["docker", "start"]:
                        restored_at.append(self.now - self.fault_at)
                    return original(argv, timeout)
                self.execute = execute
                h = self.harness()
                h.run(self.node)
                self.assertEqual(restored_at, [fault_seconds])
                self.assertTrue(all(at + timeout <= fault_seconds for at, timeout in stopped_reads))
                self.assertNotIn("SECRET", json.dumps(h.events))

    def test_fault_window_too_short_for_sustained_recovery_fails(self):
        self.config["fault_seconds"] = 10
        h = self.harness()
        with self.assertRaisesRegex(GateError, "recovery deadline"):
            h.run(self.node)
        self.assertEqual(self.now - self.fault_at, 10)
        self.assertEqual(self.started, ["immutable-id"])
        self.assertNotIn("traffic_gate_passed", [e["event"] for e in h.events])

    def test_late_response_cannot_hide_known_controller_restart(self):
        original = self.execute
        def execute(argv, timeout=20):
            if argv == ["observe"] and self.killed and self.now - self.fault_at == 29:
                self.now += timeout
                data = json.loads(original(argv, timeout))
                data["controller_restarts"] = 1
                return json.dumps(data)
            return original(argv, timeout)
        self.execute = execute
        h = self.harness()
        with self.assertRaisesRegex(ObservationSafetyError, "controller restart"):
            h.run(self.node)
        self.assertIn("observation_safety_failed", [e["event"] for e in h.events])
        self.assertNotIn("traffic_gate_passed", [e["event"] for e in h.events])
        self.assertEqual(self.started, ["immutable-id"])

    def test_stale_future_duplicate_and_reset_observations(self):
        for value in (self.now-6, self.now+1, float("nan")):
            with self.assertRaises(GateError):
                validate_observation({**self.observation(), "observed_at_unix": value}, self.config)
        data = self.observation()
        data["assignments"] *= 2
        with self.assertRaises(GateError):
            validate_observation(data, self.config)
        prior = {**self.observation(), "observed_at_unix": self.now-1, "target_requests": 99999}
        with self.assertRaises(GateError):
            validate_observation(self.observation(), self.config, prior)

    def test_safety_failure_cannot_be_hidden_by_later_good_samples(self):
        for mode in ("duplicate-once", "terminal-after-recovery", "reset-after-outage",
                     "target-restart-after-outage", "missing-target-identity", "stale-target-restart-once"):
            with self.subTest(mode=mode):
                self.now, self.killed, self.started, self.fault_mode = 100.0, False, [], mode
                self.bad_observation_sent = False
                h = self.harness()
                with self.assertRaises(GateError):
                    h.run(self.node)
                self.assertEqual(self.started, ["immutable-id"])
                self.assertNotIn("traffic_gate_passed", [e["event"] for e in h.events])
                self.assertIn("observation_safety_failed", [e["event"] for e in h.events])

    def test_target_instance_is_required_and_retained_in_safe_evidence(self):
        for value in (None, "", True, 123, "a" * 31, "a" * 33, "A" * 32, "secret-token", []):
            with self.subTest(value=value), self.assertRaises(ObservationSafetyError):
                validate_observation({**self.observation(), "target_instance_id": value}, self.config)
        data = self.observation()
        del data["target_instance_id"]
        with self.assertRaises(ObservationSafetyError):
            validate_observation(data, self.config)
        self.assertEqual(public_observation(self.observation())["target_instance_id"], "a" * 32)
        previous = {**self.observation(), "observed_at_unix": self.now - 1, "target_requests": 0}
        self.assertTrue(validate_observation(self.observation(), self.config, previous))
        with self.assertRaisesRegex(ObservationSafetyError, "target instance changed"):
            validate_observation({**self.observation(), "target_instance_id": "b" * 32}, self.config, previous)

    def test_target_restart_before_fault_prevents_injection(self):
        self.fault_mode = "target-restart-baseline"
        with self.assertRaisesRegex(ObservationSafetyError, "target instance changed"):
            self.harness().run(self.node)
        self.assertFalse(self.killed)
        self.assertFalse(self.started)

    def test_transport_outage_is_retryable_without_renewing_evidence(self):
        self.fault_mode = "outage-once"
        h = self.harness()
        h.run(self.node)
        self.assertIn("observation_unavailable", [e["event"] for e in h.events])
        self.assertIn("traffic_gate_passed", [e["event"] for e in h.events])
        self.assertEqual(self.started, ["immutable-id"])

    def test_invalid_json_and_wrong_run_are_fatal_evidence(self):
        for payload in ("not-json", "[]", json.dumps({**self.observation(), "run_id": "different"})):
            with self.subTest(payload=payload):
                h = self.harness()
                h.execute = lambda *args, **kwargs: payload
                with self.assertRaises(ObservationSafetyError):
                    h.observe()

    def test_invalid_load_and_assignment_evidence_rejected(self):
        for key, value in (("observed_vusers", float("inf")), ("keda_total_demand", float("nan")),
                           ("keda_total_demand", float("inf")), ("observed_vusers", True),
                           ("controller_restarts", False), ("observed_vusers", "120")):
            with self.subTest(key=key, value=value), self.assertRaises(ObservationSafetyError):
                validate_observation({**self.observation(), key: value}, self.config)
        for shard in (None, [], {**self.observation()["assignments"][0], "generation": True},
                      {**self.observation()["assignments"][0], "node": ["node"]},
                      {**self.observation()["assignments"][0], "shard": ["shard"]}):
            with self.subTest(shard=shard), self.assertRaises(ObservationSafetyError):
                validate_observation({**self.observation(), "assignments": [shard]}, self.config)

    def test_evidence_drops_nonwhitelisted_secret_fields(self):
        data = self.observation()
        data["secret"] = "do-not-publish"
        data["assignments"][0]["supervision_token"] = "do-not-publish"
        self.assertNotIn("do-not-publish", json.dumps(public_observation(data)))


if __name__ == "__main__":
    unittest.main()
