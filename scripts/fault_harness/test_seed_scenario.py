import copy
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from seed_scenario import OwnedAPI, SeedError, seed


class FakeAPI:
    run_id = 'a' * 16
    namespace_uid = 'namespace-id'
    leader_uid = 'leader-id'
    epoch = '3'
    credentials = {'http': 'private-proxy', 'target_load': 'private-load-token'}

    def __init__(self):
        self.calls = []
        self.closed = False
        self.fail = None
        self.wrong_project = None
        self.platform = {'metadata_store': 'postgres', 'cache_backend': 'redis', 'archive_backend': 's3'}

    def connect(self):
        self.verify()

    def verify(self):
        pass

    def request(self, method, path, body=None):
        self.calls.append((method, path, body))
        if path == self.fail:
            raise SeedError('failed mutation; response may contain a private token')
        if method == 'GET':
            return {'data_platform': self.platform}
        project = 'wrong' if path == self.wrong_project else 'project-id'
        return {'id': {'projects': 'project-id', 'scripts': 'script-id', 'scenarios': 'scenario-id'}.get(path),
                'project_id': project}

    def close(self):
        self.closed = True


class SeedScenarioTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.output = Path(self.temp.name) / 'evidence'
        self.api = FakeAPI()

    def test_creates_secret_reference_and_scenario_without_a_run(self):
        seed(self.api, self.output)
        self.assertTrue(self.api.closed)
        self.assertEqual([(m, p) for m, p, _ in self.api.calls], [
            ('GET', 'system/inventory'), ('POST', 'projects'), ('POST', 'projects/project-id/secrets'),
            ('POST', 'scripts'), ('POST', 'scenarios')])
        script = self.api.calls[3][2]['script']
        self.assertEqual(script['config']['base_url'], 'https://apexload-fault-target.apexload-fault.svc')
        self.assertEqual(script['actions'][0]['headers']['Authorization'], 'Bearer {{secret.fault_load}}')
        self.assertNotIn('private-load-token', json.dumps(script))
        self.assertEqual(script['config']['pacing']['interval_ms'], 100)
        result = json.loads((self.output / 'result.json').read_text())
        self.assertTrue(result['seeded'])
        self.assertFalse(result['qualified'])
        self.assertEqual(result['scenario_id'], 'scenario-id')
        for path in self.output.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn('private-', path.read_text())
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o700)

    def test_existing_output_refuses_requests(self):
        self.output.mkdir()
        with self.assertRaises(SeedError):
            seed(self.api, self.output)
        self.assertEqual(self.api.calls, [])

    def test_unexpected_backend_refuses_mutations(self):
        self.api.platform['metadata_store'] = 'sqlite'
        with self.assertRaises(SeedError):
            seed(self.api, self.output)
        self.assertEqual(len(self.api.calls), 1)
        self.assertTrue(self.api.closed)

    def test_partial_failure_preserves_only_known_resource_receipts(self):
        self.api.fail = 'scripts'
        with self.assertRaises(SeedError):
            seed(self.api, self.output)
        self.assertTrue(self.api.closed)
        self.assertFalse((self.output / 'result.json').exists())
        events = json.loads((self.output / 'events.json').read_text())
        self.assertEqual(events[-1], {'failed': True, 'resources_preserved': True,
                                     'last_attempted_phase': 'script', 'last_mutation_may_have_committed': True})
        self.assertEqual(events[0], {'resource': 'project', 'id': 'project-id'})
        self.assertFalse(any(m == 'DELETE' for m, _, _ in self.api.calls))
        self.assertFalse(any('private-' in p.read_text() for p in self.output.iterdir()))

    def test_wrong_project_cannot_pass(self):
        for resource in ('scripts', 'scenarios'):
            with self.subTest(resource=resource):
                self.api = FakeAPI(); self.api.wrong_project = resource
                with self.assertRaises(SeedError):
                    seed(self.api, Path(self.temp.name) / resource)
                self.assertTrue(self.api.closed)

    def test_interruption_closes_transport(self):
        self.api.request = Mock(side_effect=KeyboardInterrupt)
        with self.assertRaises(KeyboardInterrupt):
            seed(self.api, self.output)
        self.assertTrue(self.api.closed)
        self.assertTrue(json.loads((self.output / 'events.json').read_text())[-1]['failed'])

    def test_receipt_failure_still_closes_transport(self):
        with patch('seed_scenario.os.open', side_effect=OSError('disk unavailable')):
            with self.assertRaises(OSError):
                seed(self.api, self.output)
        self.assertTrue(self.api.closed)


class OwnedAPITests(unittest.TestCase):
    def setUp(self):
        self.api = object.__new__(OwnedAPI)
        self.api.inputs = Path('/private/inputs')
        self.api.kubeconfig = 'pinned-config'
        self.api.run_id = 'a' * 16
        self.api.cluster = 'apexload-fault-' + self.api.run_id
        self.api.namespace_uid = self.api.leader_uid = self.api.epoch = self.api.node_ids = None
        self.ns = {'metadata': {'name': 'apexload-fault', 'uid': 'namespace',
            'labels': {'apexload.io/fault-run': self.api.run_id}}}
        self.nodes = {'items': [{'metadata': {'name': self.api.cluster + s, 'uid': 'uid' + s}}
                               for s in ('-control-plane', '-worker', '-worker2')]}
        self.pod = {'metadata': {'name': 'controller', 'namespace': 'apexload-fault', 'uid': 'controller-uid',
            'labels': {'app.kubernetes.io/name': 'apexload-controller', 'apexload.io/fault-run': self.api.run_id}},
            'status': {'podIP': '172.18.0.2'}}
        self.endpoint = {'metadata': {'annotations': {'apexload.io/execution-lease-generation': '3'}},
            'endpoints': [{'conditions': {'ready': True}, 'addresses': ['172.18.0.2'],
                'targetRef': {'kind': 'Pod', 'namespace': 'apexload-fault', 'name': 'controller', 'uid': 'controller-uid'}}]}
        self.api.get = lambda *args: {'namespace': self.ns, 'nodes': self.nodes,
            'pods': {'items': [self.pod]}, 'endpointslice': self.endpoint}[args[0]]
        patcher = patch('seed_scenario.read_input', return_value='pinned-config')
        self.read_input = patcher.start(); self.addCleanup(patcher.stop)

    def test_missing_credentials_refuse_setup_before_network_access(self):
        for values in ({}, {'http': 'x' * 32, 'target_load': 'short'}, {'http': '\n' * 32, 'target_load': 'x' * 32}):
            with patch('seed_scenario.input_root', return_value=Path('/private/inputs')), \
                 patch('seed_scenario.read_ownership', return_value=('a' * 16, 'apexload-fault-' + 'a' * 16)), \
                 patch('seed_scenario.read_json', return_value=values):
                with self.assertRaises(SeedError):
                    OwnedAPI('/private/inputs')

    def test_stable_namespace_nodes_and_leader(self):
        self.assertEqual(self.api.verify(), 'controller')
        self.assertEqual(self.api.verify(), 'controller')
        self.assertEqual(self.api.namespace_uid, 'namespace')

    def test_replacements_fail_closed(self):
        self.api.verify()
        for mutate, obj in ((lambda x: x['metadata'].update(uid='replaced'), self.ns),
                            (lambda x: x['items'][0]['metadata'].update(uid='replaced'), self.nodes),
                            (lambda x: x['metadata']['annotations'].update({'apexload.io/execution-lease-generation': '4'}), self.endpoint)):
            saved = copy.deepcopy(obj)
            mutate(obj)
            with self.assertRaises(SeedError):
                self.api.verify()
            obj.clear(); obj.update(saved)
        self.read_input.return_value = 'changed-config'
        with self.assertRaises(SeedError):
            self.api.verify()

    def test_foreign_namespace_refuses_leader_lookup(self):
        self.ns['metadata']['labels']['apexload.io/fault-run'] = 'foreign'
        with self.assertRaises(SeedError):
            self.api.verify()

    def test_drain_retains_no_output_and_handles_closed_pipe(self):
        OwnedAPI.drain_output(io.BytesIO(b'x' * 100_000))
        stream = io.BytesIO(); stream.close()
        OwnedAPI.drain_output(stream)


if __name__ == '__main__':
    unittest.main()
