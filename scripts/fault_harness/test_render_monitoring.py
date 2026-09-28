import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from render_monitoring import render, resources
from render_target import RenderError


class MonitoringResourcesTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.inputs = self.root / 'inputs'
        self.inputs.mkdir(mode=0o700)
        self.run = '0123456789abcdef'
        (self.inputs / 'ownership.json').write_text(json.dumps({'run_id': self.run, 'cluster': 'apexload-fault-' + self.run,
            'namespace': 'apexload-fault', 'namespace_label': 'apexload.io/fault-run=' + self.run}))
        self.image = 'docker.io/prom/prometheus@sha256:' + 'a' * 64

    def test_discovery_security_and_scrape_policy(self):
        _, manifest = resources(self.inputs, self.image)
        items = manifest['items']
        self.assertFalse(any(i['kind'] in ('Secret', 'Role', 'RoleBinding', 'Namespace') for i in items))
        config = next(i for i in items if i['kind'] == 'ConfigMap')
        scrape = json.loads(config['data']['prometheus.yml'])['scrape_configs'][0]
        self.assertEqual(scrape['dns_sd_configs'][0]['type'], 'A')
        self.assertEqual(scrape['dns_sd_configs'][0]['port'], 9091)
        service = next(i for i in items if i['kind'] == 'Service' and i['metadata']['name'] == 'apexload-controller-metrics')
        self.assertEqual(service['spec']['clusterIP'], 'None')
        self.assertEqual(service['spec']['selector'], {'app.kubernetes.io/name': 'apexload-controller'})
        deployment = next(i for i in items if i['kind'] == 'Deployment')
        pod = deployment['spec']['template']['spec']
        self.assertFalse(pod['automountServiceAccountToken'])
        self.assertEqual(pod['nodeSelector']['kubernetes.io/hostname'], 'apexload-fault-' + self.run + '-control-plane')
        self.assertTrue(pod['containers'][0]['securityContext']['readOnlyRootFilesystem'])
        policy = next(i for i in items if i['metadata']['name'] == 'apexload-fault-prometheus-scrape')
        self.assertEqual(policy['spec']['ingress'][0]['ports'], [{'protocol': 'TCP', 'port': 9091}])

    def test_production_and_accelerated_queries_private_output(self):
        for accelerated in (False, True):
            with self.subTest(accelerated=accelerated):
                root = self.root / str(accelerated)
                render(self.inputs, self.image, root, 75, 2, 6, accelerated)
                profile = json.loads((root / 'profile.json').read_text())
                self.assertEqual(profile['timing'], 'accelerated' if accelerated else 'production')
                for path in (root, *root.rglob('*')):
                    self.assertEqual(path.stat().st_mode & 0o777, 0o700 if path.is_dir() else 0o600)
                original = (root / 'autoscaling/scaledobject-base.yaml').read_text()
                self.assertEqual(original.count('fleet="' + self.run + '"'), 6)
                self.assertEqual(original.count('apexload_fleet_autoscaling_valid_until > time()'), 6)
                self.assertEqual(original.count('ignoreNullValues: "false"'), 6)
                self.assertIn('signal="reserved_replica_floor"', original)
                self.assertNotIn('${', original)
                with self.assertRaises(RenderError):
                    render(self.inputs, self.image, root)
                self.assertEqual((root / 'autoscaling/scaledobject-base.yaml').read_text(), original)
                patch = json.loads((root / 'controller-autoscaling-merge-patch.json').read_text())
                self.assertEqual(patch, {'data': {'APEXLOAD_GENERATOR_AUTOSCALING_OWNER': 'keda'}})
                if shutil.which('kubectl'):
                    result = subprocess.run(['kubectl', 'kustomize', str(root / 'autoscaling')], capture_output=True, text=True, timeout=15)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn('maxReplicaCount: 6', result.stdout)
                    self.assertEqual(result.stdout.count('threshold: "75"'), 2)
                    self.assertIn('cooldownPeriod: ' + ('30\n' if accelerated else '300\n'), result.stdout)
                    self.assertNotIn('prometheus-operated', result.stdout)
                    self.assertEqual(result.stdout.count('serverAddress: http://apexload-fault-prometheus.apexload-fault.svc:9090'), 6)

    def test_invalid_inputs_do_not_create_output(self):
        for capacity, minimum, maximum, accelerated in ((0, 2, 4, False), (True, 2, 4, False), (1001, 2, 4, False),
                (100, 1, 4, False), (100, 4, 2, False), (100, 2, 13, False), (100, 2, 4, 'yes')):
            with self.subTest(values=(capacity, minimum, maximum, accelerated)), self.assertRaises(RenderError):
                render(self.inputs, self.image, self.root / 'invalid', capacity, minimum, maximum, accelerated)
            self.assertFalse((self.root / 'invalid').exists())
        with self.assertRaises(RenderError):
            render(self.inputs, 'prom/prometheus:latest', self.root / 'invalid')
        (self.inputs / 'ownership.json').write_text('{}')
        with self.assertRaises(RenderError):
            resources(self.inputs, self.image)


if __name__ == '__main__':
    unittest.main()
