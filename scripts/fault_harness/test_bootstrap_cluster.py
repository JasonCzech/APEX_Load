import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from bootstrap_cluster import Bootstrap, BootstrapError, command
from unittest.mock import Mock


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.inputs = self.root / 'inputs'; self.inputs.mkdir(mode=0o700)
        self.run_id = 'a' * 16; self.cluster = 'apexload-fault-' + self.run_id
        owner = {'run_id': self.run_id, 'cluster': self.cluster, 'namespace': 'apexload-fault',
                 'namespace_label': 'apexload.io/fault-run=' + self.run_id}
        (self.inputs / 'ownership.json').write_text(json.dumps(owner)); (self.inputs / 'ownership.json').chmod(0o600)
        self.kind = self.root / 'kind'; self.kind.write_text('#!/bin/sh\nexit 1\n'); self.kind.chmod(0o700)
        self.chart = self.root / 'chart.tgz'; self.chart.write_bytes(b'approved-chart')
        self.chart_hash = hashlib.sha256(self.chart.read_bytes()).hexdigest()
        self.image = 'kindest/node@sha256:' + 'b' * 64
        self.images = {role: 'ghcr.io/kedacore/' + repo + '@sha256:' + 'c' * 64 for role, repo in
                       (('keda', 'keda'), ('metricsApiServer', 'keda-metrics-apiserver'), ('webhooks', 'keda-admission-webhooks'))}
        self.output = self.root / 'output'
        self.calls = []; self.namespace = None; self.existing = ''
        self.fail_stage = None; self.replace_namespace = False; self.replace_node = False
        self.bad_chart = False; self.engine_calls = 0; self.replace_engine = False
        patcher = patch('bootstrap_cluster.shutil.disk_usage', return_value=type('Space', (), {'free': 16 * 1024**3})())
        patcher.start(); self.addCleanup(patcher.stop)

    def execute(self, argv, timeout=20, data=None):
        self.calls.append((argv, timeout, data))
        if argv[:4] == ['docker', '--context', 'desktop-linux', 'info']:
            self.engine_calls += 1
            return json.dumps({'ID': 'other' if self.replace_engine and self.engine_calls > 1 else 'engine',
                               'OperatingSystem': 'Docker Desktop', 'OSType': 'linux'})
        if argv[:4] == ['docker', '--context', 'desktop-linux', 'ps']:
            return self.existing
        if argv[:4] == ['docker', '--context', 'desktop-linux', 'image']:
            return '[]'
        if argv[:4] == ['docker', '--context', 'desktop-linux', 'inspect']:
            return json.dumps([{'Name': '/' + name, 'Id': ('d' if self.replace_node else 'b') * 64,
                'State': {'Running': True}, 'Config': {'Labels': {'io.x-k8s.kind.cluster': self.cluster,
                    'io.x-k8s.kind.role': 'control-plane' if name.endswith('-control-plane') else 'worker'}}} for name in argv[4:]])
        if argv[:3] == ['helm', 'show', 'chart']:
            return 'name: keda\nversion: ' + ('2.20.0' if self.bad_chart else '2.21.0') + '\nappVersion: 2.21.0\n'
        if argv[0] == str(self.kind):
            self.assertIn('--retain', argv)
            self.assertEqual(argv[argv.index('--kubeconfig') + 1], str(self.inputs / 'kubeconfig'))
            (self.inputs / 'kubeconfig').write_text('owned-kubeconfig')
            if self.fail_stage == 'create':
                raise BootstrapError('simulated create timeout')
            return ''
        if argv[:2] == ['helm', 'install']:
            if self.fail_stage == 'install':
                raise KeyboardInterrupt
            if self.fail_stage == 'replace-node':
                self.replace_node = True
            return ''
        self.assertEqual(argv[:5], ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'), '--context', 'kind-' + self.cluster])
        if 'version' in argv:
            return json.dumps({'serverVersion': {'gitVersion': 'v1.29.0' if self.fail_stage == 'version' else 'v1.34.3'}})
        if 'get' in argv:
            if 'deployments' in argv:
                return json.dumps({'items': [{'spec': {'template': {'spec': {
                    'nodeSelector': {'kubernetes.io/hostname': self.cluster + '-control-plane'},
                    'containers': [{'image': 'wrong' if self.fail_stage == 'image' else image.replace('@', ':2.21.0@')}]}}}}
                    for image in self.images.values()]})
            if 'nodes' in argv:
                return json.dumps({'items': [{'metadata': {'name': self.cluster + s, 'uid': 'uid' + s}}
                                            for s in ('-control-plane', '-worker', '-worker2')]})
            ns = json.loads(json.dumps(self.namespace))
            if self.replace_namespace:
                ns['metadata']['uid'] = 'replacement'
            return json.dumps(ns)
        if 'create' in argv:
            self.namespace = json.loads(data); self.namespace['metadata']['uid'] = 'keda-uid'
            return json.dumps(self.namespace)
        if 'wait' in argv and self.fail_stage == 'readiness':
            raise BootstrapError('not ready')
        return ''

    def build(self):
        return Bootstrap(self.inputs, self.output, self.kind, self.image, self.chart, self.chart_hash, self.images, execute=self.execute)

    def mutations(self):
        return [argv for argv, _, _ in self.calls if 'create' in argv or 'install' in argv]

    def test_fresh_cluster_and_keda_are_pinned_and_bounded(self):
        self.build().run()
        result = json.loads((self.output / 'result.json').read_text())
        self.assertTrue(result['ready']); self.assertFalse(result['qualified'])
        self.assertEqual(result['node_image'], self.image)
        self.assertEqual(result['keda_namespace_uid'], 'keda-uid')
        self.assertEqual(len(self.mutations()), 3)
        self.assertFalse(any('delete' in a or 'uninstall' in a or '--atomic' in a for a, _, _ in self.calls))
        values = json.loads((self.output / 'keda-values.json').read_text())
        self.assertEqual(values['image']['keda']['tag'], '2.21.0@sha256:' + 'c' * 64)
        self.assertEqual(values['nodeSelector']['kubernetes.io/hostname'], self.cluster + '-control-plane')
        self.assertEqual((self.inputs / 'kubeconfig').stat().st_mode & 0o777, 0o600)
        for path in self.output.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_existing_cluster_refuses_mutation(self):
        self.existing = self.cluster + '-worker\n'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(self.mutations(), [])
        self.assertFalse((self.inputs / 'kubeconfig').exists())

    def test_existing_kubeconfig_refuses_even_rendering(self):
        (self.inputs / 'kubeconfig').write_text('existing')
        with self.assertRaises(BootstrapError): self.build()
        self.assertFalse(self.output.exists())
        self.assertEqual(self.calls, [])

    def test_checksum_and_image_pins_are_required(self):
        self.chart_hash = '0' * 64
        with self.assertRaises(BootstrapError): self.build()
        self.assertEqual(self.calls, [])
        self.chart_hash = hashlib.sha256(self.chart.read_bytes()).hexdigest()
        self.image = 'kindest/node:latest'
        with self.assertRaises(BootstrapError): self.build()
        self.assertEqual(self.calls, [])

    def test_low_disk_prevents_mutations(self):
        with patch('bootstrap_cluster.shutil.disk_usage', return_value=type('Space', (), {'free': 1024})()):
            with self.assertRaises(BootstrapError): self.build()
        self.assertEqual(self.calls, [])

    def test_changed_artifacts_refuse_creation(self):
        b = self.build(); (self.output / 'kind.json').write_text('changed')
        with self.assertRaises(BootstrapError): b.run()
        self.assertEqual(self.mutations(), [])

    def test_wrong_chart_version_refuses_creation(self):
        self.bad_chart = True
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(self.mutations(), [])

    def test_engine_replacement_refuses_creation(self):
        self.replace_engine = True
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(self.mutations(), [])

    def test_namespace_replacement_refuses_helm_install(self):
        self.replace_namespace = True
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(len(self.mutations()), 2)
        self.assertFalse((self.output / 'result.json').exists())

    def test_container_replacement_cannot_pass(self):
        self.fail_stage = 'replace-node'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertFalse((self.output / 'result.json').exists())

    def test_creation_failure_preserves_new_kubeconfig_and_receipts(self):
        self.fail_stage = 'create'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertTrue((self.inputs / 'kubeconfig').exists())
        self.assertTrue(json.loads((self.output / 'events.json').read_text())[-1]['resources_preserved'])
        self.assertEqual(len(self.mutations()), 1)

    def test_interrupted_install_preserves_cluster(self):
        self.fail_stage = 'install'
        with self.assertRaises(KeyboardInterrupt): self.build().run()
        self.assertTrue(json.loads((self.output / 'events.json').read_text())[-1]['resources_preserved'])
        self.assertFalse(any('delete' in a for a, _, _ in self.calls))

    def test_unsupported_server_preserves_cluster_without_installing_keda(self):
        self.fail_stage = 'version'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(len(self.mutations()), 1)

    def test_deployed_image_change_cannot_pass(self):
        self.fail_stage = 'image'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertFalse((self.output / 'result.json').exists())

    def test_command_overrides_remote_docker_and_alternate_provider(self):
        with patch.dict('os.environ', {'DOCKER_HOST': 'tcp://remote', 'DOCKER_CONTEXT': 'remote', 'KIND_EXPERIMENTAL_PROVIDER': 'podman'}), \
             patch('bootstrap_cluster.subprocess.run', return_value=Mock(returncode=0, stdout='ready')) as run:
            self.assertEqual(command(['kind', 'version']), 'ready')
            env = run.call_args.kwargs['env']
            self.assertEqual(env['DOCKER_CONTEXT'], 'desktop-linux')
            self.assertEqual(env['KIND_EXPERIMENTAL_PROVIDER'], 'docker')
            self.assertNotIn('DOCKER_HOST', env)

    def test_readiness_failure_stops_before_namespace_creation(self):
        self.fail_stage = 'readiness'
        with self.assertRaises(BootstrapError): self.build().run()
        self.assertEqual(len(self.mutations()), 1)


if __name__ == '__main__':
    unittest.main()
