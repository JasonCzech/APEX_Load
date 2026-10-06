import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import unittest

spec = importlib.util.spec_from_file_location("fixture_matrix", Path(__file__).with_name("run-import-fixture-matrix.py"))
matrix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(matrix)


class MatrixEvidenceTests(unittest.TestCase):
    def test_command_preserves_output_and_exit_code(self):
        result = matrix.run_command([sys.executable, "-c", "print('evidence'); raise SystemExit(3)"], matrix.ROOT, 5)
        self.assertEqual(result.returncode, 3)
        self.assertEqual(result.stdout.strip(), "evidence")

    def test_timeout_is_bounded_and_retains_output(self):
        with self.assertRaises(subprocess.TimeoutExpired) as captured:
            matrix.run_command([sys.executable, "-c", "import time; print('started', flush=True); time.sleep(60)"], matrix.ROOT, 0.2)
        self.assertIn("started", captured.exception.stdout)

    def test_skipped_or_missing_tests_are_not_passes(self):
        cases = [([], 0, "failed"), ([{"Test": "TestA", "Action": "skip"}], 0, "blocked"),
                 ([{"Test": "TestA", "Action": "pass"}], 1, "failed"),
                 ([{"Test": "TestA/child", "Action": "skip"}, {"Test": "TestA", "Action": "pass"}], 0, "partial"),
                 ([{"Test": "TestA/child", "Action": "fail"}, {"Test": "TestA", "Action": "pass"}], 0, "failed"),
                 ([{"Test": "TestA", "Action": "pass"}], 0, "passed")]
        for events, code, status in cases:
            with self.subTest(status=status, events=events):
                result = matrix.summarize("\n".join(map(json.dumps, events)), code, "go-json")
                self.assertEqual(result["status"], status)

    def test_node_tap_counts_and_failure(self):
        self.assertEqual(matrix.summarize("# pass 4\n# fail 0\n# skipped 0\n", 0, "tap")["status"], "passed")
        self.assertEqual(matrix.summarize("# pass 4\n# fail 1\n# skipped 0\n", 0, "tap")["status"], "failed")

    def test_live_profiles_require_selection_and_configuration(self):
        row = {"live": True, "requires_env": ["BROKER"]}
        self.assertIsNotNone(matrix.preflight(row, {"BROKER": "value"}, False))
        self.assertIn("BROKER", matrix.preflight(row, {}, True))
        self.assertIsNone(matrix.preflight(row, {"BROKER": "value"}, True))

    def test_manual_coverage_gap_is_not_executable(self):
        self.assertEqual(matrix.preflight({"manual_gap": "No provider fixture"}, {}, True), "No provider fixture")

    def test_manifest_has_unique_ids_and_inspectable_sources(self):
        rows = json.loads(matrix.MANIFEST.read_text())["cases"]
        self.assertEqual(len({row["id"] for row in rows}), len(rows))
        for row in rows:
            self.assertRegex(row["id"], r"^[a-z0-9-]+$")
            self.assertTrue(row["scope"])
            self.assertTrue(row["expectations"])
            for source in row["sources"]:
                self.assertTrue((matrix.ROOT / source).exists(), source)
            self.assertTrue(row.get("command") or row.get("manual_gap"))


if __name__ == "__main__":
    unittest.main()
