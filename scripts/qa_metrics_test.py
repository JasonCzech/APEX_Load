from pathlib import Path
import sys
import unittest
from unittest.mock import patch
from types import SimpleNamespace


sys.path.insert(0, str(Path(__file__).parent))

from qa_metrics import open_inherited_defects, packages_without_tests, tier0_states


class QAMetricsTest(unittest.TestCase):
    def test_counts_assertion_states(self) -> None:
        matrix = "| S1 | login | GREEN | x |\n| S2 | workspace | PARTIAL | x |\n| S3 | import | MISSING | x |\n"
        self.assertEqual(tier0_states(matrix), {"GREEN": 1, "PARTIAL": 1, "MISSING": 1})

    def test_counts_only_open_inherited_defects(self) -> None:
        defects = (
            "| DEF-1 | High | x | M0 | OPEN | — |\n"
            "| DEF-2 | High | x | M0 | FIXED | abcdef0 |\n"
            "## Found by this program\n| QA-1 | High | x | M0 | OPEN | — |\n"
        )
        self.assertEqual(open_inherited_defects(defects), 1)

    def test_ignored_evidence_and_dependencies_do_not_change_source_inventory(self) -> None:
        lines = "\n".join([
            "example/pkg/runtime\tfalse", "example/pkg/tested\ttrue",
            "example/artifacts/private-runtime\tfalse", "example/node_modules/flatted/golang\tfalse",
        ])
        with patch("qa_metrics.subprocess.run", return_value=SimpleNamespace(stdout=lines)):
            self.assertEqual(packages_without_tests(Path(".")), 1)

    def test_repository_package_inventory_is_nonempty(self) -> None:
        root = Path(__file__).resolve().parent.parent
        self.assertGreater(packages_without_tests(root), 0)


if __name__ == "__main__":
    unittest.main()
