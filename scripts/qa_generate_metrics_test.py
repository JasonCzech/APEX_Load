import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location("qa_generate_metrics", Path(__file__).with_name("qa-generate-metrics.py"))
module = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(module)
open_inherited_defects = module.open_inherited_defects
packages_without_tests = module.packages_without_tests
tier0_states = module.tier0_states


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

    def test_repository_package_inventory_is_nonempty(self) -> None:
        root = Path(__file__).resolve().parent.parent
        self.assertGreater(packages_without_tests(root), 0)


if __name__ == "__main__":
    unittest.main()
