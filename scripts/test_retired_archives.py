"""Cold source snapshots must remain byte-for-byte restorable."""
import hashlib
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RetiredArchiveTests(unittest.TestCase):
    def test_manifests_match_retained_source_bytes(self):
        manifests = [ROOT / "_retired/appdynamics/manifest.json",
                     ROOT / "_retired/bruno-runner/manifest.json",
                     ROOT / "_retired/main-integration-manifest.json"]
        for manifest in manifests:
            records = json.loads(manifest.read_text())["files"]
            self.assertTrue(records, manifest)
            paths = set()
            for record in records:
                with self.subTest(manifest=manifest.name, path=record["snapshot_path"]):
                    self.assertNotIn(record["snapshot_path"], paths)
                    paths.add(record["snapshot_path"])
                    snapshot = ROOT / record["snapshot_path"]
                    self.assertTrue(snapshot.is_relative_to(ROOT / "_retired"))
                    self.assertEqual(hashlib.sha256(snapshot.read_bytes()).hexdigest(), record["sha256"])


if __name__ == "__main__":
    unittest.main()
