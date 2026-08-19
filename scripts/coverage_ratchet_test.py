import json
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("coverage-ratchet.py")


def report(status: str = "pass", package: str = "pkg/example", coverage: float = 90.0) -> dict:
    return {
        "test_run_status": status,
        "overall_statement_coverage_pct": coverage,
        "packages": [{"package": package, "has_tests": True, "statement_coverage_pct": coverage}],
    }


class CoverageRatchetTest(unittest.TestCase):
    def run_case(self, baseline: dict, current: dict) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            baseline_path = root / "baseline.json"
            current_path = root / "current.json"
            baseline_path.write_text(json.dumps(baseline))
            current_path.write_text(json.dumps(current))
            return subprocess.run(
                [str(SCRIPT), str(current_path), "--baseline", str(baseline_path)],
                text=True,
                capture_output=True,
            )

    def run_update(self, current: dict, with_companion: bool = False) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            current_path = root / "current.json"
            baseline_path = root / "baseline.json"
            current_path.write_text(json.dumps(current))
            if with_companion:
                current_path.with_suffix(".md").write_text("# Current coverage\n")
            return subprocess.run(
                [str(SCRIPT), str(current_path), "--baseline", str(baseline_path), "--update"],
                text=True,
                capture_output=True,
            )

    def test_rejects_regression(self) -> None:
        result = self.run_case(report(), report(coverage=80.0))
        self.assertEqual(result.returncode, 1)
        self.assertIn("COVERAGE RATCHET FAILED", result.stderr)

    def test_rejects_missing_baseline_package(self) -> None:
        result = self.run_case(report(), report(package="pkg/other"))
        self.assertEqual(result.returncode, 1)
        self.assertIn("pkg/example", result.stderr)

    def test_rejects_failed_current_run(self) -> None:
        result = self.run_case(report(), report(status="fail"))
        self.assertEqual(result.returncode, 2)
        self.assertIn("current run's tests did not pass", result.stderr)

    def test_rejects_unknown_current_run(self) -> None:
        result = self.run_case(report(), report(status="unknown"))
        self.assertEqual(result.returncode, 2)
        self.assertIn("test_run_status=pass", result.stderr)

    def test_rejects_failed_run_when_updating_baseline(self) -> None:
        result = self.run_update(report(status="fail"))
        self.assertEqual(result.returncode, 2)
        self.assertIn("test_run_status=pass", result.stderr)

    def test_update_requires_generated_markdown_companion(self) -> None:
        result = self.run_update(report())
        self.assertEqual(result.returncode, 2)
        self.assertIn("Markdown companion", result.stderr)

    def test_update_replaces_json_and_markdown_together(self) -> None:
        result = self.run_update(report(), with_companion=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("JSON and Markdown updated", result.stdout)

    def test_rejects_failed_baseline(self) -> None:
        result = self.run_case(report(status="fail"), report())
        self.assertEqual(result.returncode, 2)
        self.assertIn("baseline was not produced by a passing test run", result.stderr)

    def test_excludes_generated_proto(self) -> None:
        baseline = report(package="api/proto", coverage=100.0)
        current = report(package="api/proto", coverage=0.0)
        current["overall_statement_coverage_pct"] = baseline["overall_statement_coverage_pct"]
        result = self.run_case(baseline, current)
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
