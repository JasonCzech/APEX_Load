from pathlib import Path
import contextlib
import json
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest

from prepare_inputs import prepare
from render_target import RenderError, render, resources


@unittest.skipUnless(shutil.which("openssl"), "openssl is required")
class RenderTargetTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name)
        cls.inputs = cls.root / "inputs"
        cls.run_id = prepare(cls.inputs)
        cls.other = cls.root / "other"
        prepare(cls.other)
        cls.image = "localhost:5001/apexload-fault-target@sha256:" + "a" * 64

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    @contextlib.contextmanager
    def changed(self, name, data):
        path = self.inputs / name
        original = path.read_bytes()
        path.write_bytes(data if isinstance(data, bytes) else data.encode())
        try:
            yield
        finally:
            path.write_bytes(original)

    def test_placement_security_and_credential_separation(self):
        run_id, result = resources(self.inputs, self.image)
        self.assertEqual(run_id, self.run_id)
        objects = {obj["metadata"]["name"]: obj for obj in result["items"] if obj["kind"] == "Secret"}
        server, load = objects["apexload-fault-target-server"], objects["apexload-fault-target-load"]
        self.assertEqual(set(server["stringData"]), {"target.json", "tls.crt", "tls.key"})
        self.assertEqual(set(load["stringData"]), {"load_token", "ca.crt"})
        config = json.loads((self.inputs / "target.json").read_text())
        self.assertTrue(config["observer_token"] not in json.dumps(load), "load secret exposes observer credential")
        self.assertTrue(load["stringData"]["load_token"] == config["load_token"], "wrong load credential")
        self.assertTrue(server["immutable"] and load["immutable"])
        deployment = next(obj for obj in result["items"] if obj["kind"] == "Deployment")
        self.assertEqual(deployment["spec"]["replicas"], 1)
        self.assertEqual(deployment["spec"]["strategy"], {"type": "Recreate"})
        pod = deployment["spec"]["template"]["spec"]
        self.assertEqual(pod["nodeSelector"], {"kubernetes.io/hostname": "apexload-fault-" + run_id + "-control-plane"})
        self.assertFalse(pod["automountServiceAccountToken"])
        self.assertEqual(pod["securityContext"]["runAsUser"], 65532)
        self.assertEqual(pod["securityContext"]["fsGroup"], 65532)
        self.assertEqual(pod["securityContext"]["seccompProfile"]["type"], "RuntimeDefault")
        self.assertEqual(pod["volumes"][0]["secret"]["defaultMode"], 0o440)
        container = pod["containers"][0]
        self.assertEqual(container["image"], self.image)
        self.assertNotIn("env", container)
        self.assertTrue(container["securityContext"]["readOnlyRootFilesystem"])
        self.assertFalse(container["securityContext"]["allowPrivilegeEscalation"])
        self.assertEqual(container["securityContext"]["capabilities"]["drop"], ["ALL"])
        for probe in ("startupProbe", "readinessProbe", "livenessProbe"):
            self.assertEqual(container[probe]["httpGet"]["scheme"], "HTTPS")
        service = next(obj for obj in result["items"] if obj["kind"] == "Service")
        self.assertEqual(service["spec"]["type"], "ClusterIP")
        self.assertEqual(service["spec"]["selector"], deployment["spec"]["selector"]["matchLabels"])
        policy = next(obj for obj in result["items"] if obj["kind"] == "NetworkPolicy")
        self.assertEqual(policy["spec"]["egress"], [])
        peers = policy["spec"]["ingress"][0]["from"]
        self.assertTrue(all(set(peer) == {"podSelector"} for peer in peers), "ingress must remain namespace-scoped")
        self.assertEqual({peer["podSelector"]["matchLabels"]["app.kubernetes.io/name"] for peer in peers},
                         {"apexload-generator", "apexload-fault-observer"})
        for obj in result["items"]:
            self.assertEqual(obj["metadata"]["namespace"], "apexload-fault")
            self.assertEqual(obj["metadata"]["labels"]["apexload.io/fault-run"], run_id)
        self.assertNotIn("Namespace", {obj["kind"] for obj in result["items"]})

    def test_output_permissions_and_overwrite_refusal(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "rendered"
            render(self.inputs, self.image, out)
            self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o700)
            for path in out.iterdir():
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            original = (out / "target-resources.json").read_bytes()
            with self.assertRaises(RenderError):
                render(self.inputs, self.image, out)
            self.assertTrue((out / "target-resources.json").read_bytes() == original, "existing output changed")
            link = Path(tmp) / "link"
            link.symlink_to(out, target_is_directory=True)
            with self.assertRaises(RenderError):
                render(self.inputs, self.image, link)
        with self.assertRaises(RenderError):
            render(self.inputs, self.image, "relative-output")

    def test_bad_image_and_ownership_inputs_do_not_render(self):
        for image in (None, "image:latest", "sha256:" + "a" * 64, "https://registry/image@sha256:" + "a" * 64,
                      "registry:abc/image@sha256:" + "a" * 64, "registry:65536/image@sha256:" + "a" * 64,
                      self.image.replace("sha256:", "sha512:"), self.image + "\n"):
            with self.subTest(image=image), self.assertRaises(RenderError):
                resources(self.inputs, image)
        ownership = json.loads((self.inputs / "ownership.json").read_text())
        for field, value in (("run_id", True), ("cluster", "another-cluster"), ("namespace", "production"), ("namespace_label", "wrong")):
            with self.changed("ownership.json", json.dumps({**ownership, field: value})), self.assertRaises(RenderError):
                resources(self.inputs, self.image)
        with self.assertRaises(RenderError):
            resources("relative-inputs", self.image)

    def test_bad_credentials_json_and_tls_are_rejected(self):
        config = json.loads((self.inputs / "target.json").read_text())
        for value in ("[]", "not-json", "x" * 8193, '{"run_id":"a","run_id":"b"}',
                      json.dumps({**config, "run_id": "b" * 16}), json.dumps({**config, "load_token": "short"}),
                      json.dumps({**config, "load_token": config["observer_token"]}), json.dumps({**config, "extra": True})):
            with self.changed("target.json", value), self.assertRaises(RenderError):
                resources(self.inputs, self.image)
        for name, data in (("fault-target.key", b"private-key-marker"), ("fault-target.crt", b"x" * 32769),
                           ("ca.crt", (self.other / "ca.crt").read_bytes())):
            with self.changed(name, data), self.assertRaisesRegex(RenderError, "TLS|size"):
                resources(self.inputs, self.image)
        # Valid chain/key with the wrong service identity must still be rejected.
        with self.changed("fault-target.crt", (self.inputs / "controller.crt").read_bytes()), self.changed("fault-target.key", (self.inputs / "controller.key").read_bytes()):
            with self.assertRaisesRegex(RenderError, "TLS"):
                resources(self.inputs, self.image)
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(RenderError):
                resources(Path(tmp), self.image)

    def test_cli_hides_secrets_and_rejects_invalid_inputs_before_output(self):
        script = Path(__file__).with_name("render_target.py")
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "rendered"
            command = [sys.executable, str(script), "--inputs", str(self.inputs), "--image", self.image, "--output", str(out)]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, "renderer CLI failed")
            self.assertEqual(result.stdout.strip(), "Rendered private target resources for run " + self.run_id)
            self.assertEqual(result.stderr, "")
            self.assertTrue(out.is_dir())
            failed = Path(tmp) / "failed"
            with self.changed("target.json", "secret-marker"):
                result = subprocess.run(command[:-1] + [str(failed)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stdout, "")
            self.assertNotIn("secret-marker", result.stderr)
            self.assertFalse(failed.exists())

    @unittest.skipUnless(shutil.which("kubectl"), "kubectl is required for offline Kustomize rendering")
    def test_offline_kustomize_render(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "rendered"
            render(self.inputs, self.image, out)
            result = subprocess.run(["kubectl", "kustomize", str(out)], capture_output=True, text=True, timeout=20)
            self.assertEqual(result.returncode, 0, "offline Kustomize render failed")
            kinds = re.findall(r"^kind: (.+)$", result.stdout, re.MULTILINE)
            self.assertCountEqual(kinds, ["Secret", "Secret", "Deployment", "Service", "NetworkPolicy"])


if __name__ == "__main__":
    unittest.main()
