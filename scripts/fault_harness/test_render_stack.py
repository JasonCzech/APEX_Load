import contextlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from prepare_inputs import prepare
from render_stack import application, render
from render_target import RenderError


@unittest.skipUnless(shutil.which('openssl'), 'openssl required')
class StackResourcesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.inputs = cls.root / 'inputs'
        cls.run_id = prepare(cls.inputs)
        cls.images = {role: 'example.invalid/' + role + '@sha256:' + 'a' * 64
                      for role in ('controller', 'generator', 'target', 'postgres', 'redis', 's3')}

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    @contextlib.contextmanager
    def changed(self, name, content):
        p = self.inputs / name
        original = p.read_bytes()
        p.write_bytes(content)
        try:
            yield
        finally:
            p.write_bytes(original)

    def test_archive_admission_fits_volume_allocations(self):
        _, _, overlay = application(self.inputs, self.images['controller'], self.images['generator'])
        patches = [json.loads(p['patch']) for p in overlay['patches'] if 'target' not in p]
        controller = next(p for p in patches if p['kind'] == 'Deployment')['spec']['template']['spec']
        generator = next(p for p in patches if p['kind'] == 'StatefulSet')['spec']
        def numeric_env(pod):
            return {e['name']: int(e['value']) for e in pod['containers'][0]['env']
                    if e.get('value', '').isdigit()}
        def quantity(value):
            self.assertTrue(value.endswith(('Mi', 'Gi')))
            return int(value[:-2]) * 1024 ** (2 if value.endswith('Mi') else 3)
        mib = 1024 ** 2
        env = numeric_env(controller)
        paths = {e['name']: e.get('value') for e in controller['containers'][0]['env']}
        self.assertEqual(paths['APEXLOAD_ARCHIVE_READER_CACHE_DIR'], '/var/lib/apexload/archive-read-cache')
        self.assertEqual(paths['APEXLOAD_ARCHIVE_TRANSFER_DIR'], '/tmp/apexload-archive-transfers')
        self.assertEqual(paths['APEXLOAD_ARCHIVE_DOWNLOAD_STAGING_DIR'], '/tmp/apexload-archive-downloads')
        self.assertGreaterEqual(env['APEXLOAD_ARCHIVE_DOWNLOAD_MAX_BYTES'], env['APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES'])
        volumes = {v['name']: quantity(v['emptyDir']['sizeLimit']) for v in controller['volumes'] if 'emptyDir' in v}
        backlog = env['APEXLOAD_ARCHIVE_BACKLOG_MAX_BYTES']
        self.assertGreater(backlog, 0)
        # Pinned readers may outlive cache eviction; count both allowances.
        data = (backlog + env['APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES'] +
                env['APEXLOAD_ARCHIVE_ACTIVE_READER_MAX_BYTES'] +
                env['APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_MAX_BYTES'])
        self.assertLessEqual(data + 512 * mib, volumes['data'])
        self.assertLessEqual(sum(volumes.values()) + 512 * mib,
                             quantity(controller['containers'][0]['resources']['limits']['ephemeral-storage']))
        self.assertGreater(env['APEXLOAD_ARCHIVE_DOWNLOAD_MAX_CONCURRENT'], 0)
        transfers = env['APEXLOAD_ARCHIVE_TRANSFER_MAX_BYTES']
        downloads = env['APEXLOAD_ARCHIVE_DOWNLOAD_MAX_BYTES'] * env['APEXLOAD_ARCHIVE_DOWNLOAD_MAX_CONCURRENT']
        self.assertLessEqual(transfers + downloads + 128 * mib, volumes['tmp'])
        self.assertGreater(env['APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES'], 0)
        self.assertLessEqual(env['APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES'], transfers)
        self.assertLessEqual(env['APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES'], env['APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES'])
        for phase in ('CAPTURE', 'MERGE', 'CONVERSION', 'PUBLICATION', 'ADJUNCT'):
            self.assertGreater(env['APEXLOAD_ARCHIVE_' + phase + '_MAX_BYTES'], 0)
            self.assertLessEqual(env['APEXLOAD_ARCHIVE_' + phase + '_MAX_BYTES'], backlog)
            self.assertGreaterEqual(env['APEXLOAD_ARCHIVE_' + phase + '_MIN_FREE_BYTES'], 64 * mib)
        self.assertGreaterEqual(env['APEXLOAD_ARCHIVE_TRANSFER_MIN_FREE_BYTES'], 64 * mib)
        self.assertGreaterEqual(env['APEXLOAD_ARCHIVE_DOWNLOAD_MIN_FREE_BYTES'], 64 * mib)
        gen = numeric_env(generator['template']['spec'])
        storage = quantity(generator['volumeClaimTemplates'][0]['spec']['resources']['requests']['storage'])
        self.assertLessEqual(gen['APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES'] + 128 * mib, storage)
        self.assertGreater(gen['APEXLOAD_GENERATOR_ARCHIVE_MAX_BYTES'], 0)
        self.assertLessEqual(gen['APEXLOAD_GENERATOR_ARCHIVE_MAX_BYTES'], gen['APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES'])
        self.assertGreaterEqual(gen['APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES'], 64 * mib)

    def test_phased_private_output_and_base_overlay_render(self):
        output = self.root / 'stack'
        render(self.inputs, self.images, output)
        for path in (output, *output.rglob('*')):
            self.assertEqual(path.stat().st_mode & 0o777, 0o700 if path.is_dir() else 0o600)
        deps = json.loads((output / 'dependencies/dependencies.json').read_text())['items']
        names = {item['metadata']['name'] for item in deps}
        for name in ('apexload-fault', 'apexload-postgres', 'apexload-redis', 'apexload-s3', 'apexload-fault-target'):
            self.assertIn(name, names)
        ns = next(item for item in deps if item['kind'] == 'Namespace')
        self.assertEqual(ns['metadata']['labels']['pod-security.kubernetes.io/enforce'], 'restricted')
        app = json.loads((output / 'application/application-resources.json').read_text())
        creds = json.loads((self.inputs / 'credentials.json').read_text())
        for role in ('postgres', 'redis', 's3_access_key', 's3_secret_key', 'target_observer', 'target_load'):
            self.assertNotIn(creds[role], json.dumps(app))
        config = json.loads((output / 'application/kustomization.yaml').read_text())
        self.assertEqual(config['namespace'], 'apexload-fault')
        patches = [json.loads(p['patch']) for p in config['patches'] if 'target' not in p]
        deleted = {(p['kind'], p['metadata']['name']) for p in patches if p.get('$patch') == 'delete'}
        self.assertEqual(deleted, {('Namespace', 'apexload'), ('Ingress', 'apexload-controller'),
                                   ('PersistentVolumeClaim', 'apexload-controller-import-assets')})
        controller = next(p for p in patches if p['kind'] == 'Deployment')['spec']
        self.assertEqual(controller['replicas'], 2)
        pod = controller['template']['spec']
        self.assertIn('requiredDuringSchedulingIgnoredDuringExecution', pod['affinity']['podAntiAffinity'])
        self.assertEqual(pod['containers'][0]['image'], self.images['controller'])
        env = {e['name']: e for e in pod['containers'][0]['env']}
        self.assertEqual(env['APEXLOAD_BOOTSTRAP_SUBJECT']['value'], 'fault-' + self.run_id)
        self.assertEqual(env['APEXLOAD_ALLOW_BOOTSTRAP_ADMIN']['value'], 'true')
        volumes = {v['name']: v for v in pod['volumes']}
        self.assertEqual(volumes['tmp']['emptyDir']['sizeLimit'], '1Gi')
        self.assertEqual(volumes['data']['emptyDir']['sizeLimit'], '3Gi')
        self.assertEqual(pod['containers'][0]['resources']['limits']['ephemeral-storage'], '5Gi')
        self.assertEqual(pod['containers'][0]['resources']['requests']['ephemeral-storage'], '512Mi')
        self.assertEqual(env['APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES']['value'], str(256 * 1024**2))
        self.assertEqual(env['APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_MAX_BYTES']['value'], str(256 * 1024**2))
        self.assertEqual(env['APEXLOAD_ARCHIVE_ACTIVE_READER_MAX_BYTES']['value'], str(512 * 1024**2))
        self.assertEqual(env['APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_TTL']['value'], '15m')
        self.assertNotIn('APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL', env)
        self.assertEqual(env['APEXLOAD_DATABASE_URL']['valueFrom']['secretKeyRef']['name'], 'apexload-fault-data-client')
        self.assertEqual(env['AWS_SECRET_ACCESS_KEY']['valueFrom']['secretKeyRef']['name'], 'apexload-s3-client')
        generator = next(p for p in patches if p['kind'] == 'StatefulSet')['spec']
        self.assertEqual(generator['replicas'], 4)
        self.assertEqual(generator['template']['spec']['topologySpreadConstraints'][0]['whenUnsatisfiable'], 'DoNotSchedule')
        sql = (output / 'bootstrap-auth.sql').read_text()
        self.assertIn('ON CONFLICT(key) DO NOTHING', sql)
        self.assertIn('RAISE EXCEPTION', sql)
        self.assertNotIn(creds['http'], sql)
        cni = json.loads((output / 'kindnet-resources-patch.json').read_text())
        self.assertEqual(cni['spec']['template']['spec']['containers'][0]['resources']['limits']['cpu'], '1')
        with self.assertRaises(RenderError):
            render(self.inputs, self.images, output)
        if shutil.which('kubectl'):
            result = subprocess.run(['kubectl', 'kustomize', str(output / 'application')], capture_output=True, timeout=20)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            text = result.stdout.decode()
            self.assertTrue('registry.example.internal' not in text, 'unexpected base value in rendered profile')
            self.assertTrue('CHANGE-ME' not in text, 'unexpected base value in rendered profile')
            self.assertTrue('kind: Namespace' not in text, 'unexpected base value in rendered profile')
            self.assertTrue('kind: Ingress' not in text, 'unexpected base value in rendered profile')
            self.assertTrue('persistentVolumeClaim:' not in text, 'ephemeral import volume still references deleted PVC')
            self.assertTrue('namespace: apexload\n' not in text, 'stale service-account namespace')
            self.assertTrue('$patch' not in text, 'unexpected base value in rendered profile')
            self.assertTrue('APEXLOAD_CONTROLLER_HA' in text, 'missing HA configuration')
            self.assertTrue('apexload-controller-active' in text, 'missing active endpoint')
            self.assertTrue(self.images['generator'] in text, 'missing pinned image')
            self.assertTrue('apexload.apexload.svc' not in text, 'unexpected base value in rendered profile')

    def test_source_retention_is_explicit_and_bounded(self):
        for seconds in (60, 300, 720 * 3600):
            output = self.root / ('retention-' + str(seconds))
            render(self.inputs, self.images, output, source_retention_seconds=seconds)
            config = json.loads((output / 'application/kustomization.yaml').read_text())
            patches = [json.loads(p['patch']) for p in config['patches'] if 'target' not in p]
            pod = next(p for p in patches if p['kind'] == 'Deployment')['spec']['template']['spec']
            env = {e['name']: e for e in pod['containers'][0]['env']}
            self.assertEqual(env['APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL']['value'], str(seconds) + 's')
            self.assertEqual(json.loads((output / 'profile.json').read_text())['source_retention_seconds'], seconds)
        for index, seconds in enumerate((-1, 1, 59, 720 * 3600 + 1, True, 60.0, '60', None)):
            output = self.root / ('bad-retention-' + str(index))
            with self.subTest(seconds=seconds), self.assertRaises(RenderError):
                render(self.inputs, self.images, output, source_retention_seconds=seconds)
            self.assertFalse(output.exists())

    def test_validation_before_any_output(self):
        for replicas, capacity in ((1, 100), (13, 100), (True, 100), (4, 0), (4, 1001), (4, 1.5)):
            with self.subTest(replicas=replicas, capacity=capacity), self.assertRaises(RenderError):
                application(self.inputs, self.images['controller'], self.images['generator'], replicas, capacity)
        for role in self.images:
            output = self.root / ('bad-' + role)
            with self.subTest(role=role), self.assertRaises(RenderError):
                render(self.inputs, {**self.images, role: 'example.invalid/image:mutable'}, output)
            self.assertFalse(output.exists())
        with self.changed('controller.crt', (self.inputs / 'redis.crt').read_bytes()), \
                self.changed('controller.key', (self.inputs / 'redis.key').read_bytes()), self.assertRaises(RenderError):
            render(self.inputs, self.images, self.root / 'bad-tls')
        creds = json.loads((self.inputs / 'credentials.json').read_text())
        for value in ('short', False, creds['grpc']):
            with self.changed('credentials.json', json.dumps({**creds, 'auth_session': value}).encode()), self.assertRaises(RenderError):
                render(self.inputs, self.images, self.root / 'bad-auth')


if __name__ == '__main__':
    unittest.main()
