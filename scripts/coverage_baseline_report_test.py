import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.coverage_baseline_report import RATCHET_EXCLUDED_PACKAGES, package_inventory, ratchet_totals


class CoverageBaselinePolicyTest(unittest.TestCase):
    def test_generated_proto_exclusion_is_explicit(self) -> None:
        self.assertEqual(RATCHET_EXCLUDED_PACKAGES, {"api/proto"})

    def test_generated_proto_does_not_depress_overall_total(self) -> None:
        total, covered = ratchet_totals(
            {"github.com/jasonczech/apexload/api/proto": (100, 0), "github.com/jasonczech/apexload/pkg/api": (50, 40)},
            lambda value: value.removeprefix("github.com/jasonczech/apexload/"),
        )
        self.assertEqual((total, covered), (50, 40))

    def test_inventory_does_not_infer_tests_from_coverage_presence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "packages.txt"
            path.write_text(
                "github.com/jasonczech/apexload/pkg/covered\ttrue\n"
                "github.com/jasonczech/apexload/pkg/instrumented-only\tfalse\n"
            )
            self.assertEqual(
                package_inventory(path),
                {
                    "github.com/jasonczech/apexload/pkg/covered": True,
                    "github.com/jasonczech/apexload/pkg/instrumented-only": False,
                },
            )

    def test_report_marks_profile_covered_package_without_test_files_untested(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "coverage.out"
            inventory = root / "packages.txt"
            output = root / "report.json"
            package = "github.com/jasonczech/apexload/pkg/instrumented-only"
            profile.write_text(
                "mode: atomic\n"
                f"{package}/source.go:1.1,2.2 1 0\n"
            )
            inventory.write_text(f"{package}\tfalse\n")
            subprocess.run(
                [
                    str(Path(__file__).with_name("coverage_baseline_report.py")),
                    str(profile),
                    str(output),
                    str(inventory),
                    "pass",
                ],
                check=True,
                capture_output=True,
                text=True,
            )
            report = json.loads(output.read_text())
            entry = next(item for item in report["packages"] if item["package"] == "pkg/instrumented-only")
            self.assertFalse(entry["has_tests"])
            self.assertEqual(entry["statements_total"], 1)


if __name__ == "__main__":
    unittest.main()
