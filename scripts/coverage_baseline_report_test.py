import unittest

from scripts.coverage_baseline_report import RATCHET_EXCLUDED_PACKAGES, ratchet_totals


class CoverageBaselinePolicyTest(unittest.TestCase):
    def test_generated_proto_exclusion_is_explicit(self) -> None:
        self.assertEqual(RATCHET_EXCLUDED_PACKAGES, {"api/proto"})

    def test_generated_proto_does_not_depress_overall_total(self) -> None:
        total, covered = ratchet_totals(
            {"github.com/jasonczech/apexload/api/proto": (100, 0), "github.com/jasonczech/apexload/pkg/api": (50, 40)},
            lambda value: value.removeprefix("github.com/jasonczech/apexload/"),
        )
        self.assertEqual((total, covered), (50, 40))


if __name__ == "__main__":
    unittest.main()
