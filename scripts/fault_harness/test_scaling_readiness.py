import copy
import unittest

from scaling_readiness import generator_fleet_ready


class ScalingReadinessTest(unittest.TestCase):
    def setUp(self):
        self.pods = [{'metadata': {'name': 'gen-' + str(i), 'uid': 'uid-' + str(i), 'namespace': 'apexload-fault'},
                      'status': {'phase': 'Running', 'podIP': '10.0.0.' + str(i + 1),
                                 'conditions': [{'type': 'Ready', 'status': 'True'}],
                                 'containerStatuses': [{'ready': True}]}} for i in range(2)]
        self.generators = [{'target': '10.0.0.' + str(i + 1) + ':9090', 'generator_id': 'gen-' + str(i),
                            'status': 'connected', 'drain_state': 'active', 'compatibility_state': 'compatible',
                            'supported_protocols': ['http'], 'available_capacity': 100, 'max_vusers': 100,
                            'active_vusers': 0} for i in range(2)]

    def ready(self, pods=None, generators=None):
        return generator_fleet_ready(self.pods if pods is None else pods,
                                     self.generators if generators is None else generators,
                                     2, 100, 'apexload-fault')

    def test_matching_ready_fleet_and_ipv6(self):
        self.assertTrue(self.ready())
        self.pods[0]['status']['podIP'] = '2001:db8::1'
        self.generators[0]['target'] = '[2001:db8::1]:9090'
        self.assertTrue(self.ready())

    def test_pending_pod_without_address_is_not_ready(self):
        del self.pods[1]['status']['podIP']
        self.pods[1]['status']['phase'] = 'Pending'
        self.assertFalse(self.ready())

    def test_pod_identity_and_health_fail_closed(self):
        cases = [('metadata', 'uid', None), ('metadata', 'uid', 'uid-0'),
                 ('metadata', 'namespace', 'other'), ('metadata', 'name', 'gen-0'),
                 ('metadata', 'deletionTimestamp', '2026-09-28T00:00:00Z'),
                 ('status', 'podIP', '10.0.0.1'), ('status', 'podIP', 'invalid'),
                 ('status', 'podIP', 167772162),
                 ('status', 'conditions', [{'type': 'Ready', 'status': 'True'}, {'type': 'Ready', 'status': 'False'}]),
                 ('status', 'phase', 'Pending'), ('status', 'conditions', []),
                 ('status', 'containerStatuses', [{'ready': False}])]
        for section, field, value in cases:
            with self.subTest(field=field, value=value):
                pods = copy.deepcopy(self.pods)
                pods[1][section][field] = value
                self.assertFalse(self.ready(pods=pods))

    def test_connected_is_not_sufficient(self):
        cases = [('drain_state', 'draining'), ('drain_state', 'cordoned'),
                 ('status', 'unreachable'), ('available_capacity', 99), ('available_capacity', True),
                 ('available_capacity', -1), ('max_vusers', 99), ('active_vusers', 1),
                 ('active_vusers', False), ('compatibility_state', 'incompatible'),
                 ('supported_protocols', ['grpc']), ('supported_protocols', 'http'), ('generator_id', 'other'),
                 ('target', '10.0.0.9:9090'), ('target', '10.0.0.1:9090')]
        for field, value in cases:
            with self.subTest(field=field, value=value):
                generators = copy.deepcopy(self.generators)
                generators[1][field] = value
                self.assertFalse(self.ready(generators=generators))

    def test_replacement_excludes_terminating_predecessor_but_not_stale_inventory(self):
        old = copy.deepcopy(self.pods[1])
        old['metadata'].update(name='old-pod', uid='old-uid', deletionTimestamp='2026-09-28T00:00:00Z')
        old['status']['podIP'] = '10.0.0.9'
        self.assertTrue(self.ready(pods=self.pods + [old]))
        stale = copy.deepcopy(self.generators[1])
        stale.update(target='10.0.0.9:9090', status='unreachable')
        self.assertFalse(self.ready(generators=self.generators + [stale]))

    def test_malformed_and_incomplete_observations(self):
        for pods, generators in (([], []), (self.pods[:1], self.generators),
                                 ([{}, {}], self.generators), (self.pods, [None, None]),
                                 ('pods', self.generators), (self.pods, 'generators')):
            with self.subTest(pods=pods, generators=generators):
                self.assertFalse(generator_fleet_ready(pods, generators, 2, 100, 'apexload-fault'))
        for replicas, capacity in ((True, 100), (1, 100), (13, 100), (2, 0), (2, True), (2, 1001)):
            self.assertFalse(generator_fleet_ready(self.pods, self.generators, replicas, capacity, 'apexload-fault'))


if __name__ == '__main__':
    unittest.main()
