import copy
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from deploy_stack import DeploymentError, StackDeployment, command
from prepare_inputs import prepare


@unittest.skipUnless(shutil.which('openssl'), 'openssl required')
class StackDeploymentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.inputs = cls.root / 'inputs'
        cls.run_id = prepare(cls.inputs)
        (cls.inputs / 'kubeconfig').write_text('private-kind-kubeconfig')
        (cls.inputs / 'kubeconfig').chmod(0o600)
        cls.images = {role: 'example.invalid/' + role + '@sha256:' + 'a' * 64
                      for role in ('controller', 'generator', 'target', 'postgres', 'redis', 's3', 'prometheus')}

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def setUp(self):
        self.calls = []
        self.ns = None
        self.fail_phase = None
        self.keda_ready = True
        self.change_namespace_after_create = False
        self.changed_worker = False
        self.replace_container_after_create = False
        self.inspect_count = 0
        self.output = self.root / ('output-' + self._testMethodName)

    def execute(self, argv, data=None, timeout=20):
        self.calls.append((argv, data, timeout))
        cluster = 'apexload-fault-' + self.run_id
        if argv[:2] == ['docker', 'inspect']:
            name = argv[-1]
            self.inspect_count += 1
            return json.dumps([{'Name': '/' + name, 'Id': ('c' if self.ns and self.replace_container_after_create else 'b' if name.endswith('worker2') else 'a') * 64,
                'Config': {'Labels': {'io.x-k8s.kind.cluster': 'unowned' if self.changed_worker else cluster,
                                     'io.x-k8s.kind.role': 'control-plane' if name.endswith('control-plane') else 'worker'}},
                'State': {'Running': True}}])
        if argv[:2] == ['kubectl', 'kustomize']:
            return 'derived manifest: ' + argv[-1]
        self.assertEqual(argv[:7], ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'),
                         '--context', 'kind-' + cluster, '--namespace', 'apexload-fault'])
        if 'get' in argv:
            kind = argv[argv.index('get') + 1]
            if kind == 'nodes':
                return json.dumps({'items': [{'metadata': {'name': cluster + suffix, 'uid': suffix}}
                                            for suffix in ('-control-plane', '-worker', '-worker2')]})
            if kind == 'namespace':
                return json.dumps(self.ns) if self.ns else ''
            return json.dumps({'status': {'conditions': [{'type': 'Established' if kind == 'crd' else 'Available',
                                                          'status': 'True' if self.keda_ready else 'False'}]}})
        if 'create' in argv:
            self.ns = json.loads(data)
            self.ns['metadata']['uid'] = 'created-namespace'
            response = json.dumps(self.ns)
            if self.change_namespace_after_create:
                self.ns['metadata']['uid'] = 'replacement-namespace'
            return response
        if self.fail_phase == 'dependencies' and 'rollout' in argv:
            raise DeploymentError('simulated readiness failure')
        if self.fail_phase == 'auth' and 'exec' in argv:
            raise DeploymentError('simulated incompatible auth')
        if self.fail_phase == 'interrupt' and 'apply' in argv:
            raise KeyboardInterrupt
        return ''

    def deployment(self):
        return StackDeployment(self.inputs, self.images, self.output, execute=self.execute)

    def mutations(self):
        return [argv for argv, _, _ in self.calls if any(x in argv for x in ('create', 'apply', 'patch', 'exec'))]

    def test_source_retention_passes_to_private_profile(self):
        deployment = StackDeployment(self.inputs, self.images, self.output, execute=self.execute,
                                     source_retention_seconds=300)
        deployment.run()
        profile = json.loads((self.output / 'stack/profile.json').read_text())
        self.assertEqual(profile['source_retention_seconds'], 300)
        overlay = json.loads((self.output / 'stack/application/kustomization.yaml').read_text())
        patches = [json.loads(p['patch']) for p in overlay['patches'] if 'target' not in p]
        pod = next(p for p in patches if p['kind'] == 'Deployment')['spec']['template']['spec']
        env = {e['name']: e for e in pod['containers'][0]['env']}
        self.assertEqual(env['APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL']['value'], '300s')

    def test_complete_order_and_readiness_are_not_qualification(self):
        self.deployment().run()
        stages = [e['phase'] for e in json.loads((self.output / 'events.json').read_text())]
        self.assertEqual(stages, ['kindnet-resources', 'dependencies', 'bootstrap-auth', 'application', 'monitoring', 'completed'])
        result = json.loads((self.output / 'result.json').read_text())
        self.assertTrue(result['deployed'])
        self.assertFalse(result['qualified'])
        self.assertEqual(result['namespace_uid'], 'created-namespace')
        calls = [argv for argv, _, _ in self.calls]
        auth_index = next(i for i, argv in enumerate(calls) if 'exec' in argv)
        app_index = next(i for i, (_, data, _) in enumerate(self.calls) if data and 'stack/application' in data)
        self.assertLess(auth_index, app_index)
        auth = self.calls[auth_index]
        self.assertIn('ON_ERROR_STOP=1', auth[0])
        self.assertIn('incompatible fault authentication settings', auth[1])
        self.assertFalse(any('delete' in argv or 'kill' in argv or 'reset' in argv for argv in calls))
        for path in (self.output, *self.output.rglob('*')):
            self.assertEqual(path.stat().st_mode & 0o777, 0o700 if path.is_dir() else 0o600)
        overlay = json.loads((self.output / 'stack/application/kustomization.yaml').read_text())
        self.assertEqual(json.loads(overlay['patches'][-1]['patch'])['data']['APEXLOAD_GENERATOR_AUTOSCALING_OWNER'], 'keda')

    def test_existing_namespace_refuses_all_mutations(self):
        self.ns = {'metadata': {'name': 'apexload-fault', 'uid': 'existing', 'labels': {'apexload.io/fault-run': self.run_id}}}
        with self.assertRaises(DeploymentError):
            self.deployment().run()
        self.assertEqual(self.mutations(), [])

    def test_unowned_worker_refuses_all_mutations(self):
        self.changed_worker = True
        with self.assertRaises(DeploymentError):
            self.deployment().run()
        self.assertEqual(self.mutations(), [])

    def test_missing_keda_refuses_all_mutations(self):
        self.keda_ready = False
        with self.assertRaises(DeploymentError):
            self.deployment().run()
        self.assertEqual(self.mutations(), [])

    def test_created_namespace_identity_is_pinned(self):
        self.change_namespace_after_create = True
        with self.assertRaises(DeploymentError):
            self.deployment().run()
        self.assertEqual(len(self.mutations()), 1)
        self.assertIn('create', self.mutations()[0])

    def test_failure_or_interrupt_preserves_evidence_without_continuing(self):
        for phase in ('dependencies', 'auth', 'interrupt'):
            with self.subTest(phase=phase):
                self.output = self.root / ('failure-' + phase)
                self.fail_phase, self.calls, self.ns = phase, [], None
                with self.assertRaises((DeploymentError, KeyboardInterrupt)):
                    self.deployment().run()
                events = json.loads((self.output / 'events.json').read_text())
                self.assertTrue(events[-1]['resources_preserved'])
                self.assertFalse((self.output / 'result.json').exists())
                self.assertFalse(any(data and 'stack/application' in data for _, data, _ in self.calls))
                self.assertFalse(any('delete' in argv for argv, _, _ in self.calls))

    def test_command_error_cannot_echo_secret_manifests(self):
        from subprocess import CompletedProcess
        with patch('deploy_stack.subprocess.run', return_value=CompletedProcess([], 1, '', 'private Secret content')):
            with self.assertRaises(DeploymentError) as result:
                command(['kubectl'], data='private Secret content')
        self.assertNotIn('private Secret content', str(result.exception))

    def test_output_reuse_is_refused_before_commands(self):
        self.output.mkdir()
        with self.assertRaises(DeploymentError):
            self.deployment()
        self.assertEqual(self.calls, [])

    def test_replaced_container_stops_before_resource_patches(self):
        self.replace_container_after_create = True
        with self.assertRaises(DeploymentError):
            self.deployment().run()
        self.assertEqual(len(self.mutations()), 1)
        self.assertIn('create', self.mutations()[0])

    @unittest.skipUnless(shutil.which('kubectl'), 'kubectl required for offline render')
    def test_real_application_render_sets_keda_before_controllers_start(self):
        self.deployment()
        rendered = subprocess.check_output(['kubectl', 'kustomize', str(self.output / 'stack/application')], text=True, timeout=20)
        self.assertIn('APEXLOAD_GENERATOR_AUTOSCALING_OWNER: keda', rendered)
        self.assertEqual(rendered.count('APEXLOAD_GENERATOR_AUTOSCALING_OWNER:'), 1)
        self.assertIn('namespace: apexload-fault', rendered)
        self.assertNotIn('namespace: apexload\n', rendered)
