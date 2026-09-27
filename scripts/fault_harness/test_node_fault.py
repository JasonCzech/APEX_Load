import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from node_fault import GateError, ObservationSafetyError, Harness, public_observation, validate_config, validate_observation


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
        return dict(run_id=self.config["run_id"], observed_at_unix=self.now, target_requests=int(self.now * 10),
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

    def test_existing_evidence_directory_is_never_overwritten(self):
        existing = Path(self.temp.name) / "old-evidence"
        existing.mkdir()
        (existing / "events.json").write_text("preserve")
        with self.assertRaises(FileExistsError):
            Harness(self.config, existing, self.execute)
        self.assertEqual((existing / "events.json").read_text(), "preserve")

    def test_wrong_context_and_nonfinite_budget_rejected(self):
        for key, value in [("context", "docker-desktop"), ("cluster", "production"), ("fault_seconds", float("nan")),
                           ("expected_vusers", True), ("observation_command", "echo dangerous")]:
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
        for mode in ("duplicate-once", "terminal-after-recovery", "reset-after-outage"):
            with self.subTest(mode=mode):
                self.now, self.killed, self.started, self.fault_mode = 100.0, False, [], mode
                self.bad_observation_sent = False
                h = self.harness()
                with self.assertRaises(GateError):
                    h.run(self.node)
                self.assertEqual(self.started, ["immutable-id"])
                self.assertNotIn("traffic_gate_passed", [e["event"] for e in h.events])
                self.assertIn("observation_safety_failed", [e["event"] for e in h.events])

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
