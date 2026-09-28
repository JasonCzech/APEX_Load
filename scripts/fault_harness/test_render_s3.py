import contextlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from prepare_inputs import prepare
from render_s3 import render, resources
from render_target import RenderError


@unittest.skipUnless(shutil.which('openssl'), 'openssl required')
class S3ResourcesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.inputs = cls.root / 'inputs'
        cls.run_id = prepare(cls.inputs)
        cls.image = 'docker.io/chrislusf/seaweedfs@sha256:' + 'a' * 64

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    @contextlib.contextmanager
    def changed(self, name, value):
        path = self.inputs / name
        original = path.read_bytes()
        path.write_bytes(value)
        try:
            yield
        finally:
            path.write_bytes(original)

    def test_tls_persistence_bucket_dns_and_credential_separation(self):
        run_id, manifest = resources(self.inputs, self.image)
        self.assertEqual(run_id, self.run_id)
        items = {x['metadata']['name']: x for x in manifest['items'] if x['kind'] == 'Secret'}
        client = items['apexload-s3-client']['stringData']
        server = items['apexload-s3-server']['stringData']
        credentials = json.loads((self.inputs / 'credentials.json').read_text())
        self.assertEqual(client['AWS_ACCESS_KEY_ID'], credentials['s3_access_key'])
        self.assertEqual(client['AWS_SECRET_ACCESS_KEY'], credentials['s3_secret_key'])
        self.assertNotIn('PRIVATE KEY', json.dumps(client))
        identity = json.loads(server['s3.json'])['identities'][0]
        self.assertTrue(all(action.endswith(':apexload-archives') for action in identity['actions']))
        self.assertIn('dir = "/data/filer"', server['filer.toml'])
        self.assertIn('copy_1 = 1', server['master.toml'])
        self.assertIn('max_version = "TLS 1.3"', server['security.toml'])
        self.assertIn('[grpc.s3]', server['security.toml'])
        self.assertIn('ca = "/run/apexfault/ca.crt"', server['security.toml'])
        whole = json.dumps(manifest)
        for key in ('http', 'grpc', 'postgres', 'redis', 'wrapping_key', 'target_load', 'target_observer'):
            self.assertNotIn(credentials[key], whole)
        for item in manifest['items']:
            self.assertEqual(item['metadata']['namespace'], 'apexload-fault')
            self.assertEqual(item['metadata']['labels']['apexload.io/fault-run'], self.run_id)
        deployment = next(x for x in manifest['items'] if x['kind'] == 'Deployment')
        self.assertEqual(deployment['spec']['strategy']['type'], 'Recreate')
        pod = deployment['spec']['template']['spec']
        self.assertEqual(pod['hostname'], client['APEXLOAD_ARCHIVE_BUCKET'])
        self.assertEqual(pod['subdomain'], 'apexload-s3')
        self.assertFalse(pod['automountServiceAccountToken'])
        self.assertEqual(pod['nodeSelector']['kubernetes.io/hostname'], 'apexload-fault-' + self.run_id + '-control-plane')
        self.assertEqual(pod['securityContext']['runAsUser'], 65532)
        container = pod['containers'][0]
        self.assertTrue(container['securityContext']['readOnlyRootFilesystem'])
        for flag in ('-s3.port=8333', '-s3.port.https=0', '-ip.bind=127.0.0.1', '-s3.ip.bind=0.0.0.0'):
            self.assertIn(flag, container['args'])
        service = next(x for x in manifest['items'] if x['kind'] == 'Service')
        self.assertEqual(service['spec']['clusterIP'], 'None')
        claim = next(x for x in manifest['items'] if x['kind'] == 'PersistentVolumeClaim')
        self.assertEqual(claim['spec']['storageClassName'], 'standard')
        policy = next(x for x in manifest['items'] if x['kind'] == 'NetworkPolicy')
        self.assertEqual(policy['spec']['egress'], [])
        self.assertEqual(policy['spec']['ingress'][0]['ports'], [{'protocol': 'TCP', 'port': 8333}])

    def test_bad_credentials_tls_digest_and_storage_class_fail_without_output(self):
        credentials = json.loads((self.inputs / 'credentials.json').read_text())
        for key, value in (('s3_access_key', 'short'), ('s3_secret_key', True),
                           ('s3_secret_key', credentials['s3_access_key'])):
            with self.subTest(key=key, value_type=type(value).__name__):
                with self.changed('credentials.json', json.dumps({**credentials, key: value}).encode()):
                    with self.assertRaises(RenderError):
                        resources(self.inputs, self.image)
        with self.changed('s3.crt', (self.inputs / 'redis.crt').read_bytes()), \
                self.changed('s3.key', (self.inputs / 'redis.key').read_bytes()), self.assertRaises(RenderError):
            resources(self.inputs, self.image)
        for storage in ('', 'a..b', 'Upper', '../other', None, 'a'*254):
            with self.subTest(storage=storage), self.assertRaises(RenderError):
                resources(self.inputs, self.image, storage)
        output = self.root / 'invalid-output'
        with self.assertRaises(RenderError):
            render(self.inputs, 'seaweedfs:latest', output)
        self.assertFalse(output.exists())

    def test_render_is_private_exclusive_and_kustomize_compatible(self):
        output = self.root / 'rendered'
        render(self.inputs, self.image, output)
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        for path in output.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        before = (output / 's3-resources.json').read_bytes()
        with self.assertRaises(RenderError):
            render(self.inputs, self.image, output)
        self.assertEqual((output / 's3-resources.json').read_bytes(), before)
        if shutil.which('kubectl'):
            result = subprocess.run(['kubectl', 'kustomize', str(output)], capture_output=True, timeout=15)
            self.assertEqual(result.returncode, 0, 'Kustomize render failed')


if __name__ == '__main__':
    unittest.main()
