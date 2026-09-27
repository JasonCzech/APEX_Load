from pathlib import Path
import json
import shutil
import stat
import ssl
import subprocess
import tempfile
import unittest

from prepare_inputs import prepare


@unittest.skipUnless(shutil.which("openssl"), "openssl is required")
class PrepareTest(unittest.TestCase):
    def verify_strict_tls(self, root, role):
        client_context = ssl.create_default_context(cafile=str(root / "ca.crt"))
        client_context.verify_flags |= ssl.VERIFY_X509_STRICT
        server_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        server_context.load_cert_chain(root / (role + ".crt"), root / (role + ".key"))
        client_in, client_out, server_in, server_out = (ssl.MemoryBIO() for _ in range(4))
        client = client_context.wrap_bio(client_in, client_out, server_hostname="localhost")
        server = server_context.wrap_bio(server_in, server_out, server_side=True)
        complete = set()
        for _ in range(20):
            for name, peer in (("client", client), ("server", server)):
                try:
                    peer.do_handshake()
                    complete.add(name)
                except ssl.SSLWantReadError:
                    pass
            if complete == {"client", "server"}:
                return
            server_in.write(client_out.read())
            client_in.write(server_out.read())
        self.fail("strict TLS handshake did not complete")

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
            for role in ("controller", "generator", "fault-target", "postgres", "redis"):
                subprocess.run(["openssl", "verify", "-x509_strict", "-CAfile", str(first / "ca.crt"), str(first / (role + ".crt"))],
                               check=True, capture_output=True)
                self.verify_strict_tls(first, role)
            with self.assertRaises(FileExistsError):
                prepare(first)
            self.assertEqual(json.loads((first / "credentials.json").read_text()), one)


if __name__ == "__main__":
    unittest.main()
