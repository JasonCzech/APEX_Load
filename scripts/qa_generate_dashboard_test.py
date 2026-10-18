import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("qa-generate-dashboard.py")


class QADashboardTest(unittest.TestCase):
    def test_generates_and_detects_staleness(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            status = root / "STATUS.md"
            matrix = root / "E2E_SUITES.md"
            metrics = root / "metrics-table.md"
            api_contract = root / "api-contract-matrix.json"
            output = root / "DASHBOARD.md"
            executions = root / "executions.json"
            executions.write_text('{"as_of":"2026-10-18","lanes":[{"name":"Live broker","status":"not configured"}]}')
            status.write_text(
                "## Milestones\n\n| ID | State |\n|---|---|\n| M0 | IN_PROGRESS |\n"
            )
            matrix.write_text(
                "### Tier-0 assertion matrix\n\n| Step | Requirement | State |\n|---|---|---|\n"
                "| S1 | Login | GREEN |\n| S2 | Workspace | PARTIAL |\n| S3 | Import | MISSING |\n"
            )
            metrics.write_text("# QA Headline Metrics\n\n| Metric | Current |\n|---|---:|\n| Specs | 1 |\n")
            api_contract.write_text(
                '{"totals":{"routes":4,"green":2,"partial":1,"missing":1}}\n'
            )
            command = [str(SCRIPT), "--status", str(status), "--matrix", str(matrix), "--metrics", str(metrics), "--api-contract", str(api_contract), "--executions", str(executions), "--output", str(output)]
            subprocess.run(command, check=True)
            generated = output.read_text()
            self.assertIn("| 1 | 1 | 1 | 3 |", generated)
            self.assertIn("Live broker | not configured | No verified success recorded", generated)
            self.assertIn("| 2 | 1 | 1 | 4 |", generated)
            subprocess.run([*command, "--check"], check=True)
            output.write_text(generated + "stale\n")
            self.assertNotEqual(subprocess.run([*command, "--check"], capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
