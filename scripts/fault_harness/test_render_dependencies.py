import contextlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

from prepare_inputs import prepare
from render_dependencies import render, resources
from render_target import RenderError


@unittest.skipUnless(shutil.which('openssl'), 'openssl required')
class DependencyResourcesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.inputs = cls.root / 'inputs'
        cls.run_id = prepare(cls.inputs)
        cls.images = ('docker.io/library/postgres@sha256:' + 'a' * 64,
                      'docker.io/library/redis@sha256:' + 'b' * 64)

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

    def test_persistent_tls_dependencies_and_private_client_contract(self):
        run_id, result = resources(self.inputs, *self.images)
        self.assertEqual(run_id, self.run_id)
        self.assertEqual(len(result['items']), 9)
        by_kind = {}
        for item in result['items']:
            by_kind.setdefault(item['kind'], []).append(item)
            self.assertEqual(item['metadata']['namespace'], 'apexload-fault')
            self.assertEqual(item['metadata']['labels']['apexload.io/fault-run'], self.run_id)
        self.assertEqual(set(by_kind), {'Secret', 'StatefulSet', 'Service', 'NetworkPolicy'})
        secrets = {s['metadata']['name']: s for s in by_kind['Secret']}
        credentials = json.loads((self.inputs / 'credentials.json').read_text())
        client = secrets['apexload-fault-data-client']['stringData']
        self.assertEqual(set(client), {'ca.crt', 'APEXLOAD_DATABASE_URL', 'APEXLOAD_REDIS_URL'})
        self.assertIn('sslmode=verify-full', client['APEXLOAD_DATABASE_URL'])
        self.assertTrue(client['APEXLOAD_REDIS_URL'].startswith('rediss://'))
        self.assertNotIn('PRIVATE KEY', json.dumps(client))
        self.assertTrue(all(secret['immutable'] for secret in secrets.values()))
        all_json = json.dumps(result)
        for key in ('http', 'grpc', 'target_observer', 'target_load', 'wrapping_key', 's3_secret_key'):
            self.assertTrue(credentials[key] not in all_json, 'unrelated credential leaked')
        for role, uid, gid in (('postgres', 70, 70), ('redis', 999, 1000)):
            workload = next(w for w in by_kind['StatefulSet'] if w['metadata']['name'] == 'apexload-' + role)
            spec = workload['spec']
            self.assertEqual(spec['replicas'], 1)
            claim = spec['volumeClaimTemplates'][0]['spec']
            self.assertEqual(claim['storageClassName'], 'standard')
            self.assertEqual(claim['accessModes'], ['ReadWriteOnce'])
            pod = spec['template']['spec']
            self.assertEqual(pod['nodeSelector']['kubernetes.io/hostname'], 'apexload-fault-' + run_id + '-control-plane')
            self.assertFalse(pod['automountServiceAccountToken'])
            self.assertEqual(pod['securityContext']['runAsUser'], uid)
            self.assertEqual(pod['securityContext']['fsGroup'], gid)
            container = pod['containers'][0]
            self.assertTrue(container['securityContext']['readOnlyRootFilesystem'])
            self.assertEqual(container['securityContext']['capabilities']['drop'], ['ALL'])
            check = container['readinessProbe']['exec']['command']
            self.assertIn('/run/apexfault/password', check[-1])
            self.assertTrue(credentials[role] not in json.dumps(container), 'password in pod spec')
            self.assertEqual(pod['volumes'][0]['secret']['defaultMode'], 0o440)
        pg = secrets['apexload-postgres-server']['stringData']
        self.assertIn('hostnossl all all all reject', pg['pg_hba.conf'])
        self.assertIn('hostssl all all all scram-sha-256', pg['pg_hba.conf'])
        redis = secrets['apexload-redis-server']['stringData']['redis.conf']
        for setting in ('port 0\n', 'tls-port 6379\n', 'appendonly yes\n', 'maxmemory-policy noeviction\n'):
            self.assertIn(setting, redis)
        for policy in by_kind['NetworkPolicy']:
            self.assertEqual(policy['spec']['egress'], [])
            for peer in policy['spec']['ingress'][0]['from']:
                self.assertEqual(set(peer), {'podSelector'})

    def test_invalid_inputs_fail_before_output_creation(self):
        credentials = json.loads((self.inputs / 'credentials.json').read_text())
        for key, value in (('postgres', 'short'), ('redis', 'bad\nconfig'), ('postgres', True),
                           ('postgres', credentials['redis'])):
            with self.subTest(key=key, value_type=type(value).__name__):
                with self.changed('credentials.json', json.dumps({**credentials, key: value}).encode()):
                    with self.assertRaises(RenderError):
                        resources(self.inputs, *self.images)
        for field in ('postgres.crt', 'redis.key'):
            with self.changed(field, b'bad TLS input'), self.assertRaises(RenderError):
                resources(self.inputs, *self.images)
        with self.changed('redis.crt', (self.inputs / 'postgres.crt').read_bytes()), self.changed('redis.key', (self.inputs / 'postgres.key').read_bytes()):
            with self.assertRaises(RenderError):
                resources(self.inputs, *self.images)
        for image in ('redis:latest', 'sha256:' + 'a'*64):
            with self.assertRaises(RenderError):
                resources(self.inputs, self.images[0], image)
        for storage_class in ('', 'Upper', '../path', 'standard\n', 'a..b', 'a.-b', 'a-.b', 'x' * 254, None):
            with self.assertRaises(RenderError):
                resources(self.inputs, *self.images, storage_class)
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'out'
            with self.assertRaises(RenderError):
                render(self.inputs, self.images[0], 'invalid', output)
            self.assertFalse(output.exists())

    def test_encrypted_key_is_rejected_without_stdin_prompt(self):
        with tempfile.TemporaryDirectory() as tmp:
            encrypted = Path(tmp) / 'encrypted.key'
            subprocess.run(['openssl', 'pkey', '-in', str(self.inputs / 'redis.key'), '-aes-256-cbc',
                            '-passout', 'pass:test-only', '-out', str(encrypted)],
                           capture_output=True, check=True, timeout=5)
            with self.changed('redis.key', encrypted.read_bytes()):
                result = subprocess.run([sys.executable, str(Path(__file__).with_name('render_dependencies.py')),
                                         '--inputs', str(self.inputs), '--postgres-image', self.images[0],
                                         '--redis-image', self.images[1], '--output', str(Path(tmp) / 'out')],
                                        input='', capture_output=True, text=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn('pass phrase', result.stderr.lower())
            self.assertEqual(result.stdout, '')

    def test_private_render_and_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / 'out'
            result = subprocess.run([sys.executable, str(Path(__file__).with_name('render_dependencies.py')),
                                     '--inputs', str(self.inputs), '--postgres-image', self.images[0],
                                     '--redis-image', self.images[1], '--output', str(output)],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, 'dependency render failed')
            self.assertEqual(result.stderr, '')
            self.assertEqual(result.stdout.strip(), 'Rendered private dependency resources for run ' + self.run_id)
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)
            self.assertTrue(all(path.stat().st_mode & 0o777 == 0o600 for path in output.iterdir()))
            before = (output / 'dependency-resources.json').read_bytes()
            with self.assertRaises(RenderError):
                render(self.inputs, *self.images, output)
            self.assertTrue(before == (output / 'dependency-resources.json').read_bytes())
            if shutil.which('kubectl'):
                result = subprocess.run(['kubectl', 'kustomize', str(output)], capture_output=True, text=True, timeout=20)
                self.assertEqual(result.returncode, 0, 'offline render failed')
                self.assertCountEqual(re.findall(r'^kind: (.+)$', result.stdout, re.MULTILINE),
                                      ['Secret']*3 + ['StatefulSet']*2 + ['Service']*2 + ['NetworkPolicy']*2)


if __name__ == '__main__':
    unittest.main()
