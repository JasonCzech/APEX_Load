import copy
import hashlib
import json
from pathlib import Path
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from cluster_evidence import snapshot
from cluster_sampler import ClusterSampler
from test_cluster_readiness import cluster_fixture


class ClusterSamplerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = dict(run_id="a" * 16, cluster="apexload-fault-" + "a" * 16, namespace="apexload-fault",
                           cluster_evidence_interval_seconds=0.01, observation_command=["SECRET"])
        self.data = cluster_fixture(self.config)
        self.baseline = snapshot(self.config, lambda *args: copy.deepcopy(self.data[args[1]]))
        self.appended = threading.Event()
        self.calls = []
        self.output = Path(self.temp.name) / "series"

    def kube(self, *args, deadline):
        self.assertGreater(deadline, time.monotonic())
        self.calls.append(args)
        return copy.deepcopy(self.data[args[1]])

    def sampler(self, kube=None):
        sampler = ClusterSampler(self.config, self.baseline["namespace"], self.output, kube or self.kube)
        original = sampler._append

        def append(entry):
            original(entry)
            self.appended.set()
        sampler._append = append
        self.addCleanup(sampler.finish)
        return sampler

    def run_one(self, sampler):
        sampler.start()
        self.assertTrue(self.appended.wait(2), "background sample did not complete")
        return sampler.finish()

    def test_samples_are_private_hashed_sanitized_and_read_only(self):
        sampler = self.sampler()
        report = self.run_one(sampler)
        self.assertTrue(report["worker_stopped"])
        self.assertEqual(report["stop_reason"], "stopped")
        self.assertEqual(report["namespace"], self.baseline["namespace"])
        self.assertTrue(report["attempts"])
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o700)
        total = 0
        for attempt in report["attempts"]:
            self.assertEqual(attempt["status"], "observed")
            self.assertTrue(attempt["inventory_complete"])
            path = self.output / attempt["file"]
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            data = path.read_bytes()
            total += len(data)
            self.assertEqual(attempt["sha256"], hashlib.sha256(data).hexdigest())
            self.assertEqual(attempt["bytes"], len(data))
            self.assertLessEqual(attempt["started_seconds"], attempt["completed_seconds"])
            self.assertNotIn("SECRET", data.decode())
        self.assertEqual(report["indexed_bytes"], total)
        self.assertTrue(all(args[0] == "get" for args in self.calls))
        self.assertNotIn("SECRET", json.dumps(report))

    def test_partial_resource_inventory_is_retained_as_gap(self):
        def kube(*args, **kwargs):
            if args[1] == "scaledobjects.keda.sh":
                raise RuntimeError("SECRET missing CRD")
            return self.kube(*args, **kwargs)
        report = self.run_one(self.sampler(kube))
        attempt = report["attempts"][0]
        self.assertFalse(attempt["inventory_complete"])
        self.assertEqual(attempt["gaps"], ["scaledobjects.keda.sh"])
        self.assertNotIn("SECRET", json.dumps(report))

    def test_scope_change_stops_before_other_reads_and_cannot_be_retried_away(self):
        self.data["namespace"]["metadata"]["uid"] = "replacement"
        sampler = self.sampler()
        report = self.run_one(sampler)
        self.assertEqual(report["stop_reason"], "scope_or_inventory_invalid")
        self.assertEqual(len(report["attempts"]), 1)
        self.assertEqual(self.calls, [("get", "namespace", "apexload-fault")])
        self.assertEqual(list(self.output.iterdir()), [])

    def test_namespace_transport_outage_is_explicit_and_retryable(self):
        count = 0
        recovered = threading.Event()
        def kube(*args, **kwargs):
            nonlocal count
            count += 1
            if count == 1:
                raise RuntimeError("SECRET unavailable")
            return self.kube(*args, **kwargs)
        sampler = self.sampler(kube)
        original = sampler._append
        def append(entry):
            original(entry)
            if entry["status"] == "observed":
                recovered.set()
        sampler._append = append
        sampler.start()
        self.assertTrue(recovered.wait(2))
        report = sampler.finish()
        self.assertEqual(report["attempts"][0]["status"], "unavailable")
        self.assertEqual(report["attempts"][1]["status"], "observed")
        self.assertNotIn("SECRET", json.dumps(report))

    def test_background_read_does_not_block_caller_and_stop_prevents_more_reads(self):
        entered, release = threading.Event(), threading.Event()
        def kube(*args, **kwargs):
            self.calls.append(args)
            entered.set()
            release.wait(2)
            return copy.deepcopy(self.data[args[1]])
        sampler = self.sampler(kube)
        self.addCleanup(release.set)
        sampler.start()
        self.assertTrue(entered.wait(2))
        # The caller is free to restore the node while Kubernetes is blocked.
        sampler.stop()
        release.set()
        report = sampler.finish()
        self.assertEqual(len(self.calls), 1)
        self.assertTrue(report["worker_stopped"])
        self.assertEqual(report["attempts"][0]["status"], "cancelled")
        self.assertEqual(list(self.output.iterdir()), [])

    def test_join_timeout_is_explicit_and_bounded(self):
        entered, release = threading.Event(), threading.Event()
        def kube(*args, **kwargs):
            entered.set()
            release.wait(2)
            return copy.deepcopy(self.data[args[1]])
        sampler = self.sampler(kube)
        self.addCleanup(release.set)
        sampler.start()
        self.assertTrue(entered.wait(2))
        with patch("cluster_sampler.STOP_SECONDS", 0.01):
            report = sampler.finish()
        self.assertFalse(report["worker_stopped"])
        self.assertEqual(report["stop_reason"], "worker_stop_timeout")
        self.assertIsNone(report["indexed_bytes"])
        release.set()
        self.assertTrue(sampler.finish()["worker_stopped"])

    def test_shared_deadline_rejects_late_success(self):
        now = [100.0]
        deadlines = []
        def kube(*args, deadline):
            deadlines.append(deadline)
            now[0] += 2
            return copy.deepcopy(self.data[args[1]])
        sampler = self.sampler(kube)
        sampler.clock = lambda: now[0]
        report = self.run_one(sampler)
        self.assertEqual(report["attempts"][0]["status"], "unavailable")
        self.assertEqual(len(set(deadlines)), 1)
        self.assertEqual(list(self.output.iterdir()), [])

    def test_artifact_and_attempt_limits_are_explicit_without_extra_reads(self):
        with patch("cluster_sampler.MAX_ATTEMPTS", 1):
            report = self.run_one(self.sampler())
            # finish may stop the interval wait before the loop reaches its cap;
            # the cap itself is checked in the deterministic second run below.
            self.assertEqual(len(report["attempts"]), 1)
        self.output = Path(self.temp.name) / "limited"
        self.appended.clear()
        with patch("cluster_sampler.MAX_SERIES_BYTES", 1):
            report = self.run_one(self.sampler())
        self.assertEqual(report["stop_reason"], "artifact_limit")
        self.assertEqual(list(self.output.iterdir()), [])
        self.output = Path(self.temp.name) / "attempts"
        with patch("cluster_sampler.MAX_ATTEMPTS", 1):
            sampler = self.sampler()
            sampler.start()
            sampler._thread.join(2)
            report = sampler.finish()
        self.assertEqual(report["stop_reason"], "attempt_limit")
        self.assertEqual(len(report["attempts"]), 1)

    def test_existing_directory_and_artifact_are_never_overwritten(self):
        sampler = self.sampler()
        path = self.output / "sample-0001.json"
        path.write_text("preserve")
        report = self.run_one(sampler)
        self.assertEqual(report["stop_reason"], "artifact_write_failed")
        self.assertEqual(path.read_text(), "preserve")
        with self.assertRaises(FileExistsError):
            self.sampler()

    def test_series_byte_limit_counts_prior_samples(self):
        budget = len((json.dumps(self.baseline, indent=2) + "\n").encode()) + 100
        with patch("cluster_sampler.MAX_SERIES_BYTES", budget):
            sampler = self.sampler()
            sampler.start()
            sampler._thread.join(2)
            report = sampler.finish()
        self.assertEqual(report["stop_reason"], "artifact_limit")
        self.assertEqual([a["status"] for a in report["attempts"]], ["observed", "artifact_limit"])
        self.assertEqual(len(list(self.output.iterdir())), 1)
        self.assertLessEqual(report["indexed_bytes"], budget)

    def test_unexpected_worker_exception_is_fixed_failure_not_traceback(self):
        sampler = self.sampler()
        sampler._append = lambda _: (_ for _ in ()).throw(RuntimeError("SECRET"))
        sampler.start()
        sampler._thread.join(2)
        report = sampler.finish()
        self.assertEqual(report["stop_reason"], "worker_failed")
        self.assertNotIn("SECRET", json.dumps(report))


if __name__ == "__main__":
    unittest.main()
