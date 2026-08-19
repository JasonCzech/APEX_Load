import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location(
    "qa_generate_evidence_summary",
    Path(__file__).with_name("qa-generate-evidence-summary.py"),
)
module = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(module)


class QAEvidenceSummaryTest(unittest.TestCase):
    def test_renders_manifest_deterministically(self) -> None:
        manifest = {
            "milestone": "M2",
            "result": "pass",
            "repository_commit": "a" * 40,
            "started_at": "2026-08-18T10:00:00Z",
            "finished_at": "2026-08-18T11:00:00Z",
            "environment": {"os": "linux", "runner": "kind"},
            "commands": ["go test ./..."],
            "limitations": [],
            "artifacts": [{"path": "report.json", "sha256": "b" * 64}],
        }
        first = module.render(manifest)
        self.assertEqual(first, module.render(manifest))
        self.assertIn("# M2 Evidence Summary", first)
        self.assertIn("_None recorded._", first)
        self.assertIn("`report.json`", first)


if __name__ == "__main__":
    unittest.main()
