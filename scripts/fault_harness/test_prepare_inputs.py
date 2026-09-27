from pathlib import Path
import json
import shutil
import stat
import subprocess
import tempfile
import unittest

from prepare_inputs import prepare


@unittest.skipUnless(shutil.which("openssl"), "openssl is required")
class PrepareTest(unittest.TestCase):
    def test_fresh_inputs_permissions_certificates_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            first, second = Path(tmp) / "first", Path(tmp) / "second"
            self.assertNotEqual(prepare(first), prepare(second))
            one, two = json.loads((first / "credentials.json").read_text()), json.loads((second / "credentials.json").read_text())
            self.assertTrue(all(one[key] != two[key] for key in one))
            target = json.loads((first / "target.json").read_text())
            self.assertEqual(target["run_id"], json.loads((first / "ownership.json").read_text())["run_id"])
            self.assertEqual(target["load_token"], one["target_load"])
            self.assertEqual(target["observer_token"], one["target_observer"])
            self.assertNotEqual(target["load_token"], target["observer_token"])
            self.assertEqual(stat.S_IMODE(first.stat().st_mode), 0o700)
            for path in first.iterdir():
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            for role in ("controller", "generator", "fault-target"):
                subprocess.run(["openssl", "verify", "-CAfile", str(first / "ca.crt"), str(first / (role + ".crt"))],
                               check=True, capture_output=True)
            with self.assertRaises(FileExistsError):
                prepare(first)
            self.assertEqual(json.loads((first / "credentials.json").read_text()), one)


if __name__ == "__main__":
    unittest.main()
