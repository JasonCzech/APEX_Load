import unittest
from qa_lane_status import render_lane_status


class LaneStatusTest(unittest.TestCase):
    def test_never_passed_failed_and_stale_are_distinct(self):
        data = {"as_of": "2026-10-18", "lanes": [
            {"name": "cloud", "status": "not configured"},
            {"name": "broker", "status": "failed", "last_success": {"at": "2026-08-01T01:00:00Z", "evidence": "receipt.json"}},
            {"name": "browser", "status": "passed", "last_success": {"at": "2026-10-18T01:00:00Z", "evidence": "browser.json"}},
        ]}
        text = "\n".join(render_lane_status(data))
        self.assertIn("not configured | No verified success recorded", text)
        self.assertIn("failed | [2026-08-01](receipt.json) (stale)", text)
        self.assertIn("passed | [2026-10-18](browser.json)", text)

    def test_cannot_claim_pass_without_evidence(self):
        for success in [None, {"at": "2026-10-18T00:00:00Z", "evidence": ""}, {"at": "2026-10-19T00:00:00Z", "evidence": "future.json"}]:
            with self.subTest(success=success), self.assertRaises(ValueError):
                render_lane_status({"as_of": "2026-10-18", "lanes": [{"name": "broker", "status": "passed", "last_success": success}]})


if __name__ == "__main__":
    unittest.main()
