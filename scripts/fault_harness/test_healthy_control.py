import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from healthy_control import HealthyControl, validate_control_config
from node_fault import GateError
from test_cluster_readiness import cluster_fixture


class HealthyControlTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        kubeconfig = self.root / "kubeconfig"
        kubeconfig.touch()
        self.config = dict(run_id="a" * 16, cluster="apexload-fault-" + "a" * 16,
                           context="kind-apexload-fault-" + "a" * 16, namespace="apexload-fault",
                           kubeconfig=str(kubeconfig), baseline_seconds=10, recovery_seconds=20,
                           fault_seconds=30, control_seconds=10, expected_vusers=10,
                           minimum_target_rps=5, observation_command=["observe"])
        self.data = cluster_fixture(self.config)
        self.now, self.calls, self.serial = 100.0, [], 0
        self.mode = None
        clock = patch("node_fault.time.time", lambda: self.now)
        clock.start()
        self.addCleanup(clock.stop)

    def execute(self, argv, timeout=20):
        self.calls.append((list(argv), timeout, self.now))
        if argv[0] == "kubectl":
            self.assertIn("--kubeconfig", argv)
            self.assertIn(self.config["context"], argv)
            resource = argv[argv.index("get") + 1]
            data = copy.deepcopy(self.data[resource])
            if self.now >= 121:
                if self.mode == "namespace-replaced" and resource == "namespace":
                    data["metadata"]["uid"] = "replacement"
                if self.mode == "node-replaced" and resource == "nodes":
                    data["items"][0]["metadata"]["uid"] = "replacement"
            return json.dumps(data)
        self.assertEqual(argv, ["observe"])
        if self.mode == "slow" and self.now >= 121:
            self.now += timeout
        observation = dict(run_id=self.config["run_id"], target_instance_id="b" * 32,
                           observed_at_unix=self.now, target_requests=int(self.now * 10),
                           observed_vusers=10, keda_total_demand=10, controller_restarts=0, status="RUNNING",
                           assignments=[dict(group="g", shard="0", pod="generator-0",
                                             node=self.config["cluster"] + "-worker", generation=1)], secret="SECRET")
        if self.mode == "never-ready":
            observation["keda_total_demand"] = 0
        if self.mode == "prior-restart":
            observation["controller_restarts"] = 1
        if self.now >= 116:
            if self.mode in ("observed_vusers", "keda_total_demand"):
                observation[self.mode] = 0
            if self.mode == "target-reset":
                observation["target_requests"] = 0
            if self.mode == "low-traffic":
                observation["target_requests"] = 1150 + int(self.now - 115)
            if self.mode == "stale":
                observation["observed_at_unix"] -= 6
            if self.mode == "target-restart":
                observation["target_instance_id"] = "c" * 32
            if self.mode == "terminal":
                observation["status"] = "COMPLETE"
            if self.mode == "duplicate":
                observation["assignments"] *= 2
            if self.mode == "controller-restart":
                observation["controller_restarts"] = 1
            if self.mode == "outage":
                raise GateError("SECRET transport failure")
            if self.mode == "interrupt":
                raise KeyboardInterrupt
        return json.dumps(observation)

    def harness(self):
        self.serial += 1
        return HealthyControl(self.config, self.root / str(self.serial), execute=self.execute,
                              clock=lambda: self.now, sleep=self.sleep)

    def sleep(self, seconds):
        self.now += seconds

    def test_healthy_control_records_private_evidence_without_docker_commands(self):
        h = self.harness()
        h.run_control()
        self.assertEqual(h.events[-1], {"event": "control_gate_passed", "at": h.events[-1]["at"],
                                       "duration_seconds": 10, "elapsed_seconds": 10})
        self.assertFalse(any(argv[0] == "docker" for argv, _, _ in self.calls))
        self.assertFalse(h.restore_required)
        self.assertIsNone(h.victim_id)
        self.assertEqual(self.now, 121)
        for filename in ("events.json", "cluster-control-preflight.json", "cluster-control-completed.json"):
            path = h.output / filename
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn("SECRET", path.read_text())
        self.assertEqual(h.output.stat().st_mode & 0o777, 0o700)

    def test_any_control_violation_is_latched(self):
        for mode in ("observed_vusers", "keda_total_demand", "target-reset", "target-restart", "low-traffic", "stale",
                     "terminal", "duplicate", "controller-restart", "prior-restart", "outage", "namespace-replaced", "node-replaced"):
            with self.subTest(mode=mode):
                self.now, self.mode = 100, mode
                h = self.harness()
                with self.assertRaises(Exception):
                    h.run_control()
                self.assertEqual(h.events[-1]["event"], "control_gate_failed")
                self.assertNotIn("control_gate_passed", [e["event"] for e in h.events])
                self.assertNotIn("SECRET", (h.output / "events.json").read_text())

    def test_acquisition_and_final_observation_are_bounded(self):
        for mode, expected_end in (("never-ready", 120), ("slow", 126)):
            with self.subTest(mode=mode):
                self.now, self.mode, self.calls = 100, mode, []
                h = self.harness()
                with self.assertRaisesRegex(GateError, "deadline"):
                    h.run_control()
                self.assertEqual(self.now, expected_end)
                for argv, timeout, at in self.calls:
                    if argv == ["observe"]:
                        self.assertGreater(timeout, 0)
                        self.assertLessEqual(timeout, 5)
                        self.assertLessEqual(at + timeout, 120 if at <= 111 else 126)

    def test_ownership_failure_prevents_observation(self):
        self.data["namespace"]["metadata"]["labels"] = {}
        h = self.harness()
        with self.assertRaises(Exception):
            h.run_control()
        self.assertTrue(all(argv[0] == "kubectl" for argv, _, _ in self.calls))

    def test_interruption_stops_and_indexes_background_sampler(self):
        self.config.update(capture_cluster_evidence=True, cluster_evidence_interval_seconds=5)
        self.mode = "interrupt"
        h = self.harness()
        with self.assertRaises(KeyboardInterrupt):
            h.run_control()
        report = json.loads((h.output / "cluster-series.json").read_text())
        self.assertTrue(report["worker_stopped"])
        self.assertEqual(report["stop_reason"], "stopped")
        self.assertNotIn("control_gate_passed", [e["event"] for e in h.events])

    def test_background_sampling_success_is_finalized_before_pass(self):
        self.config.update(capture_cluster_evidence=True, cluster_evidence_interval_seconds=5)
        h = self.harness()
        h.run_control()
        kinds = [e["event"] for e in h.events]
        self.assertLess(kinds.index("cluster_sampling_finished"), kinds.index("control_gate_passed"))

    def test_sampler_artifact_conflict_prevents_pass_and_preserves_file(self):
        self.config.update(capture_cluster_evidence=True, cluster_evidence_interval_seconds=5)
        h = self.harness()
        path = h.output / "cluster-series.json"
        path.write_text("prior evidence")
        with self.assertRaises(FileExistsError):
            h.run_control()
        self.assertEqual(path.read_text(), "prior evidence")
        self.assertNotIn("control_gate_passed", [e["event"] for e in h.events])

    def test_final_inventory_failure_cannot_report_pass(self):
        h = self.harness()
        original = self.execute
        def unavailable(argv, timeout=20):
            if argv[0] == "kubectl" and self.now >= 121:
                raise GateError("SECRET final inventory failure")
            return original(argv, timeout)
        h.execute = unavailable
        with self.assertRaises(GateError):
            h.run_control()
        self.assertEqual(h.events[-1]["event"], "control_gate_failed")
        self.assertNotIn("SECRET", (h.output / "events.json").read_text())

    def test_validation_and_overwrite_refusal(self):
        for seconds in (None, True, 0, 9, 1801, "10", float("inf"), float("nan")):
            with self.subTest(seconds=seconds), self.assertRaises(GateError):
                validate_control_config({**self.config, "control_seconds": seconds})
        for seconds in (10, 1800):
            validate_control_config({**self.config, "control_seconds": seconds})
        with self.assertRaises(GateError):
            validate_control_config({**self.config, "restoration_readiness_seconds": 10,
                                     "capture_cluster_evidence": True})
        h = self.harness()
        with self.assertRaises(FileExistsError):
            HealthyControl(self.config, h.output)

    def test_cli_config_error_is_fixed_and_private(self):
        config = self.root / "config.json"
        config.write_text('SECRET bad input')
        result = subprocess.run([sys.executable, str(Path(__file__).with_name("healthy_control.py")),
                                 "--config", str(config), "--output", str(self.root / "out")],
                                capture_output=True, text=True, timeout=5)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, "Healthy control failed; inspect private evidence\n")
        self.assertFalse((self.root / "out").exists())


if __name__ == "__main__":
    unittest.main()
