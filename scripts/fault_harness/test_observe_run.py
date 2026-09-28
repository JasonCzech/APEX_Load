import copy
import datetime
import unittest

from observe_run import project_observation, validate_config
from node_fault import GateError, ObservationSafetyError, validate_observation


class RunObservationTest(unittest.TestCase):
    def setUp(self):
        self.now = 1800000000
        self.run_id = 'a' * 16
        self.cluster = 'apexload-fault-' + self.run_id
        def ts(offset):
            return datetime.datetime.fromtimestamp(self.now + offset, datetime.timezone.utc).isoformat()
        self.config = {'namespace_uid': 'namespace-uid', 'test_id': 'test-id', 'project_id': 'project-id',
                       'created_at': ts(-60), 'target_instance_id': 'b' * 32,
                       'surviving_controllers': [{'uid': 'controller-uid', 'node': self.cluster + '-worker', 'restarts': 2}]}
        self.namespace = {'metadata': {'name': 'apexload-fault', 'uid': 'namespace-uid',
                                       'labels': {'apexload.io/fault-run': self.run_id}}}
        self.pods = []
        for name, uid, address in [('controller', 'controller-uid', '10.0.0.1'), ('generator', 'generator-uid', '10.0.0.2')]:
            self.pods.append({'metadata': {'name': 'apexload-' + name + '-0', 'uid': uid, 'namespace': 'apexload-fault',
                'labels': {'apexload.io/fault-run': self.run_id, 'app.kubernetes.io/name': 'apexload-' + name}},
                'spec': {'nodeName': self.cluster + '-worker'},
                'status': {'podIP': address, 'containerStatuses': [{'restartCount': 2}]}})
        self.endpoint = {'metadata': {'annotations': {'apexload.io/execution-lease-generation': '7'}},
                         'endpoints': [{'conditions': {'ready': True}, 'addresses': ['10.0.0.1'],
                         'targetRef': {'kind': 'Pod', 'namespace': 'apexload-fault', 'name': 'apexload-controller-0', 'uid': 'controller-uid'}}]}
        self.run = {'id': 'test-id', 'project_id': 'project-id', 'created_at': ts(-60), 'status': 'RUNNING',
                    'continuity': {'armed': True, 'execution_generation': 7},
                    'live_metrics': {'timestamp': ts(-1), 'observation_valid_until': ts(3), 'active_vusers': 80},
                    'generator_shards': [{'group': 'load', 'target': '10.0.0.2:9090'}]}
        self.target = {'run_id': self.run_id, 'target_instance_id': 'b' * 32, 'observed_at_unix': self.now - .1, 'target_requests': 1000}

    def observe(self):
        return project_observation(self.config, self.run_id, self.cluster, self.namespace, self.pods,
                                   self.endpoint, self.run, self.target, 80., now=self.now)

    def test_binds_run_leader_pod_and_independent_counter(self):
        result = self.observe()
        self.assertEqual(result['observed_vusers'], 80)
        self.assertEqual(result['target_requests'], 1000)
        self.assertEqual(result['controller_restarts'], 0)
        self.assertEqual(result['assignments'], [{'group': 'load', 'shard': '0', 'pod': 'apexload-generator-0',
                                                 'node': self.cluster + '-worker', 'generation': 7}])
        self.assertNotIn('live_metrics', result)
        self.assertNotIn('created_at', result)

    def test_namespace_run_and_target_changes_are_safety_failures(self):
        for obj, key, value in [(self.namespace['metadata'], 'uid', 'other'), (self.run, 'id', 'other'),
                                (self.run, 'project_id', 'other'), (self.target, 'run_id', 'other'),
                                (self.target, 'target_instance_id', 'c' * 32), (self.run, 'created_at', '2026-01-01T00:00:00Z')]:
            old = obj[key]
            with self.subTest(key=key):
                obj[key] = value
                with self.assertRaises(ObservationSafetyError):
                    self.observe()
                obj[key] = old

    def test_stale_metrics_old_leadership_and_missing_assignments_refuse_observation(self):
        for obj, key, value in [(self.run['continuity'], 'armed', False),
                                (self.run['continuity'], 'execution_generation', 6),
                                (self.run['live_metrics'], 'observation_state', 'stale'),
                                (self.run['live_metrics'], 'observation_valid_until', '2026-01-01T00:00:00Z'),
                                (self.run, 'generator_shards', []), (self.endpoint, 'endpoints', [])]:
            old = obj.get(key)
            with self.subTest(key=key):
                obj[key] = value
                with self.assertRaises(GateError):
                    self.observe()
                if old is None:
                    obj.pop(key)
                else:
                    obj[key] = old

    def test_ambiguous_or_replaced_pods_do_not_satisfy_assignment_binding(self):
        self.pods.append(copy.deepcopy(self.pods[1]))
        with self.assertRaises(GateError):
            self.observe()
        self.pods.pop()
        self.pods[1]['metadata']['deletionTimestamp'] = '2027-01-15T08:00:00Z'
        with self.assertRaises(GateError):
            self.observe()
        self.pods[1]['metadata'].pop('deletionTimestamp')
        self.pods[1]['status']['podIP'] = '10.0.0.3'
        with self.assertRaises(GateError):
            self.observe()

    def test_duplicate_shards_are_latched_safety_failure(self):
        self.run['generator_shards'] *= 2
        with self.assertRaises(ObservationSafetyError):
            self.observe()

    def test_survivor_restarts_and_replacement_are_reported(self):
        self.pods[0]['status']['containerStatuses'][0]['restartCount'] = 3
        self.assertEqual(self.observe()['controller_restarts'], 1)
        # A new leader may be the replacement, but a designated survivor's
        # disappearance is still a failure rather than a reset restart counter.
        self.pods[0]['metadata']['uid'] = 'replacement'
        self.endpoint['endpoints'][0]['targetRef']['uid'] = 'replacement'
        self.assertEqual(self.observe()['controller_restarts'], 1)

    def test_terminal_run_is_returned_for_immediate_driver_rejection(self):
        self.run['status'] = 'FAILED'
        self.run.pop('live_metrics')
        self.run.pop('generator_shards')
        result = self.observe()
        self.assertEqual(result['status'], 'FAILED')
        with self.assertRaises(ObservationSafetyError):
            validate_observation(result, {'run_id': self.run_id}, now=self.now)

    def test_explicit_adapter_safety_signal_cannot_be_retried_as_outage(self):
        for value in (True, False, None, 'untrusted diagnostic'):
            with self.assertRaisesRegex(ObservationSafetyError, '^observation adapter reported a safety violation$'):
                validate_observation({'safety_violation': value}, {'run_id': self.run_id}, now=self.now)

    def test_configuration_rejects_unbound_or_duplicate_survivors(self):
        self.config['surviving_controllers'] *= 2
        with self.assertRaises(GateError):
            validate_config(self.config)

    def test_group_names_need_not_be_kubernetes_identifiers(self):
        self.run['generator_shards'][0]['group'] = 'Checkout flow: region 1'
        self.assertEqual(self.observe()['assignments'][0]['group'], 'Checkout flow: region 1')

    def test_shard_ordinal_must_fit_declared_count(self):
        for count, ordinal in ((True, 0), (0, 0), (1, 1), (2, True), (2, -1)):
            self.run['generator_shards'][0].update(shard_count=count, shard_ordinal=ordinal)
            with self.subTest(count=count, ordinal=ordinal), self.assertRaises(GateError):
                self.observe()

    def test_reader_uses_pod_ports_and_rechecks_leader_uid(self):
        from observe_run import Reader
        from unittest.mock import patch
        for replaced in (False, True):
            reader = Reader.__new__(Reader)
            reader.config, reader.run_id, reader.cluster = self.config, self.run_id, self.cluster
            reader.credentials = {'http': 'private-http', 'target_observer': 'private-observer'}
            pods = copy.deepcopy(self.pods)
            for name in ('prometheus', 'target'):
                p = copy.deepcopy(self.pods[1])
                p['metadata'].update(name=name, uid=name + '-uid')
                p['metadata']['labels']['app.kubernetes.io/name'] = 'apexload-fault-' + name
                pods.append(p)
            endpoint = copy.deepcopy(self.endpoint)
            # The shipped publisher omits targetRef.uid. Name+IP are bound to
            # the read pod UID, which must be identical in the final read.
            endpoint['endpoints'][0]['targetRef'].pop('uid')
            def get(*args):
                if args[1] == 'namespace': return self.namespace
                if args[1] == 'pods': return {'items': pods}
                if args[1] == 'endpointslice': return endpoint
                if args[1] == 'pod':
                    result = copy.deepcopy(self.pods[0])
                    if replaced: result['metadata']['uid'] = 'replacement'
                    return result
                self.fail('unexpected Kubernetes read')
            reader.get = get
            forwards = []
            def forward(pod, port):
                forwards.append((pod, port))
                return '127.0.0.1:' + str(port)
            reader.forward = forward
            def request(url, headers=None):
                if '/tests/' in url:
                    self.assertEqual(headers['X-APEXLoad-Proxy-Secret'], 'private-http')
                    return self.run
                if '/query?' in url: return {'status': 'success', 'data': {'result': [{'value': [self.now, '80']}]}}
                if '/snapshot' in url:
                    self.assertEqual(headers['Authorization'], 'Bearer private-observer')
                    return self.target
                self.fail('unexpected HTTP read')
            reader.request = request
            with self.subTest(replaced=replaced), patch('observe_run.time.time', return_value=self.now):
                if replaced:
                    with self.assertRaisesRegex(GateError, 'leader pod changed'):
                        reader.observe()
                else:
                    self.assertEqual(reader.observe()['observed_vusers'], 80)
                self.assertEqual(forwards, [('apexload-controller-0', 8080), ('prometheus', 9090), ('target', 8090)])

    def test_terminal_observations_still_require_valid_independent_evidence(self):
        self.run['status'] = 'COMPLETE'
        self.target['target_requests'] = float('nan')
        with self.assertRaises(ObservationSafetyError):
            self.observe()
        self.target['target_requests'] = 1000
        self.target['observed_at_unix'] = self.now - 6
        with self.assertRaises(GateError):
            self.observe()
