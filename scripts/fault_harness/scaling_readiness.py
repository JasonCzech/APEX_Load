"""Fail-closed generator eligibility gate for disposable scaling trials.

The caller must verify cluster/namespace ownership and collect fresh snapshots.
Pod readiness alone is not proof of discoverable, schedulable application capacity.
"""
import ipaddress


def generator_fleet_ready(pods, generators, replicas, pod_capacity, namespace):
    """Require a complete one-to-one match of Ready pods and eligible generators.

    Pending/malformed/stale observations return False so a bounded polling caller
    can wait or fail its trial. This does not prove sustained load or reserve pods.
    """
    if (type(replicas) is not int or not 2 <= replicas <= 12
            or type(pod_capacity) is not int or not 1 <= pod_capacity <= 1000
            or not isinstance(namespace, str) or not namespace
            or not isinstance(pods, list) or not isinstance(generators, list)
            or len(pods) > 24 or len(generators) != replicas):
        return False
    try:
        active = [pod for pod in pods if not pod['metadata'].get('deletionTimestamp')]
        if len(active) != replicas:
            return False
        expected, names, uids = {}, set(), set()
        for pod in active:
            meta, status = pod['metadata'], pod['status']
            name, uid = meta['name'], meta['uid']
            ready_conditions = [c for c in status.get('conditions', []) if c.get('type') == 'Ready']
            if (not isinstance(name, str) or not name or not isinstance(uid, str) or not uid
                    or name in names or uid in uids or meta['namespace'] != namespace
                    or status.get('phase') != 'Running'
                    or len(ready_conditions) != 1 or ready_conditions[0].get('status') != 'True'):
                return False
            containers = status.get('containerStatuses', [])
            if not containers or not all(c.get('ready') is True for c in containers):
                return False
            if not isinstance(status['podIP'], str):
                return False
            address = ipaddress.ip_address(status['podIP'])
            host = '[' + str(address) + ']' if address.version == 6 else str(address)
            target = host + ':9090'
            if target in expected:
                return False
            expected[target] = name
            names.add(name)
            uids.add(uid)
        seen = set()
        for generator in generators:
            target = generator['target']
            available, maximum = generator['available_capacity'], generator['max_vusers']
            active_vusers = generator['active_vusers']
            if (target in seen or target not in expected or generator['generator_id'] != expected[target]
                    or generator.get('status') != 'connected' or generator.get('drain_state') != 'active'
                    or generator.get('compatibility_state') != 'compatible'
                    or not isinstance(generator.get('supported_protocols'), list)
                    or 'http' not in generator['supported_protocols']
                    or type(available) is not int or type(maximum) is not int
                    or not pod_capacity <= available <= maximum
                    or type(active_vusers) is not int or active_vusers != 0):
                return False
            seen.add(target)
        return seen == set(expected)
    except (KeyError, TypeError, ValueError, AttributeError):
        return False
