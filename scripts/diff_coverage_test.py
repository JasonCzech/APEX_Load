import json
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("diff-coverage.py")


class DiffCoverageTest(unittest.TestCase):
    def run_case(self, profile: str, diff: str, floor: int = 80, untested: tuple[str, ...] = ()) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile_path = root / "coverage.out"
            diff_path = root / "change.diff"
            report_path = root / "coverage.json"
            profile_path.write_text(profile)
            diff_path.write_text(diff)
            report_path.write_text(
                json.dumps({
                    "packages": [
                        {"package": package, "has_tests": False}
                        for package in untested
                    ]
                })
            )
            return subprocess.run(
                [str(SCRIPT), str(profile_path), "--report", str(report_path), "--diff-file", str(diff_path), "--floor", str(floor)],
                text=True,
                capture_output=True,
            )

    def test_passes_when_changed_statements_meet_floor(self) -> None:
        result = self.run_case(
            "mode: atomic\ngithub.com/jasonczech/apexload/pkg/example.go:10.1,12.2 3 1\n",
            "+++ b/pkg/example.go\n@@ -9,0 +10,3 @@\n",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("100.00%", result.stdout)

    def test_fails_when_changed_statements_are_uncovered(self) -> None:
        result = self.run_case(
            "mode: atomic\ngithub.com/jasonczech/apexload/pkg/example.go:10.1,12.2 3 0\n",
            "+++ b/pkg/example.go\n@@ -9,0 +10,3 @@\n",
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn("DIFF COVERAGE FAILED", result.stderr)

    def test_ignores_generated_protobuf(self) -> None:
        result = self.run_case(
            "mode: atomic\ngithub.com/jasonczech/apexload/api/proto/example.pb.go:10.1,12.2 3 0\n",
            "+++ b/api/proto/example.pb.go\n@@ -9,0 +10,3 @@\n",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no changed executable", result.stdout)

    def test_fails_when_changed_package_has_no_tests_or_profile_blocks(self) -> None:
        result = self.run_case(
            "mode: atomic\n",
            "+++ b/pkg/untested/new.go\n@@ -0,0 +1,4 @@\n",
            untested=("pkg/untested",),
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn("pkg/untested", result.stderr)


if __name__ == "__main__":
    unittest.main()
