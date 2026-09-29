#!/usr/bin/env python3
"""Read one owned local HA run observation for the healthy/node-fault drivers."""
import argparse
import datetime
import ipaddress
import json
import math
from pathlib import Path
import re
import select
import signal
import ssl
import subprocess
import sys
import time
import urllib.parse
import urllib.request

from node_fault import GateError, ObservationSafetyError, validate_observation
from render_target import input_root, read_json, read_ownership, unique_object


def timestamp(value):
    if not isinstance(value, str) or len(value) > 64:
        raise GateError('invalid timestamp')
    parsed = datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.tzinfo is None:
        raise GateError('timestamp needs a timezone')
    return parsed.timestamp()


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,252}', value):
        raise GateError('invalid identity')
    return value


def validate_config(config):
    for key in ('namespace_uid', 'test_id', 'project_id'):
        identifier(config.get(key))
    timestamp(config.get('created_at'))
    if not re.fullmatch(r'[a-f0-9]{32}', config.get('target_instance_id', '')):
        raise GateError('target process identity is required')
    controllers = config.get('surviving_controllers')
    if not isinstance(controllers, list) or not 1 <= len(controllers) <= 8:
        raise GateError('one to eight surviving controller baselines are required')
    seen = set()
    for c in controllers:
        identifier(c.get('uid')); identifier(c.get('node'))
        if c['uid'] in seen or type(c.get('restarts')) is not int or c['restarts'] < 0:
            raise GateError('invalid controller baseline')
        seen.add(c['uid'])


def bind_leader(endpoint, pods, run_id):
    endpoints = endpoint.get('endpoints', [])
    if len(endpoints) != 1 or endpoints[0].get('conditions', {}).get('ready') is not True:
        raise GateError('active endpoint is unavailable')
    ref = endpoints[0].get('targetRef', {})
    if ref.get('kind') != 'Pod' or ref.get('namespace') != 'apexload-fault':
        raise GateError('active endpoint is not a controller pod')
    matches = [p for p in pods if p['metadata'].get('name') == ref.get('name')
               and p['metadata'].get('namespace') == 'apexload-fault'
               and p['metadata'].get('labels', {}).get('apexload.io/fault-run') == run_id
               and p['metadata'].get('labels', {}).get('app.kubernetes.io/name') == 'apexload-controller'
               and not p['metadata'].get('deletionTimestamp')
               and endpoints[0].get('addresses') == [p.get('status', {}).get('podIP')]]
    if len(matches) != 1 or (ref.get('uid') and ref['uid'] != matches[0]['metadata'].get('uid')):
        raise GateError('active endpoint does not identify the observed controller')
    identifier(matches[0]['metadata'].get('uid'))
    return matches[0]


def validate_exclusive_run_inventory(inventory, config, run):
    """Reject traffic samples that another test could have contributed to.

    The per-test counter is the primary attribution check. Inventory remains a
    defense in depth against another test deliberately reusing the selected ID
    or a drifted workload script. A complete page is required; older tests
    qualify only when they ended before the selected test was created.
    """
    if (not isinstance(inventory, dict) or type(inventory.get('total')) is not int
            or not 1 <= inventory['total'] <= 2000 or type(inventory.get('count')) is not int
            or inventory['count'] != inventory['total'] or type(inventory.get('offset')) is not int
            or inventory['offset'] != 0 or type(inventory.get('limit')) is not int
            or inventory['limit'] != 2000 or not isinstance(inventory.get('tests'), list)
            or len(inventory['tests']) != inventory['total']):
        raise ObservationSafetyError('complete test inventory is required for traffic attribution')
    selected_created = timestamp(config['created_at'])
    seen, selected = set(), False
    for candidate in inventory['tests']:
        if not isinstance(candidate, dict):
            raise ObservationSafetyError('invalid test inventory entry')
        try:
            test_id = identifier(candidate.get('id'))
        except (GateError, TypeError):
            raise ObservationSafetyError('invalid test inventory identity') from None
        if test_id in seen:
            raise ObservationSafetyError('duplicate test inventory identity')
        seen.add(test_id)
        if test_id == config['test_id']:
            try:
                created = timestamp(candidate.get('created_at'))
            except (GateError, TypeError, ValueError):
                raise ObservationSafetyError('selected test inventory identity changed') from None
            if (candidate.get('project_id') != config['project_id'] or created != selected_created
                    or candidate.get('status') != run.get('status')):
                raise ObservationSafetyError('selected test inventory identity changed')
            selected = True
            continue
        ended = candidate.get('ended_at')
        if ended is None or candidate.get('status') not in ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED'):
            raise ObservationSafetyError('another test could contribute target traffic')
        try:
            ended_at = timestamp(ended)
        except (GateError, TypeError, ValueError):
            raise ObservationSafetyError('invalid competing test completion time') from None
        if ended_at >= selected_created:
            raise ObservationSafetyError('another test could contribute target traffic')
    if not selected:
        raise ObservationSafetyError('selected test is absent from inventory')


def project_observation(config, run_id, cluster, namespace, pods, endpoint, run, target, demand, inventory, now=None):
    """Bind controller declarations to the current pod/leader/run incarnations.

    This does not independently prove generator epoch ownership or reserve spares.
    The target counter is independent; assignments remain controller declarations.
    """
    validate_config(config)
    now = time.time() if now is None else now
    meta = namespace['metadata']
    if (meta.get('name') != 'apexload-fault' or meta.get('uid') != config['namespace_uid']
            or meta.get('labels', {}).get('apexload.io/fault-run') != run_id):
        raise ObservationSafetyError('namespace ownership changed')
    if not isinstance(pods, list) or len(pods) > 256:
        raise GateError('invalid pod inventory')
    by_uid, by_address = {}, {}
    for pod in pods:
        meta, spec, status = pod['metadata'], pod.get('spec', {}), pod.get('status', {})
        if meta.get('namespace') != 'apexload-fault' or meta.get('labels', {}).get('apexload.io/fault-run') != run_id:
            continue
        uid = identifier(meta['uid'])
        if uid in by_uid:
            raise GateError('duplicate pod identity')
        by_uid[uid] = pod
        if (meta.get('labels', {}).get('app.kubernetes.io/name') == 'apexload-generator'
                and not meta.get('deletionTimestamp') and status.get('podIP')):
            address = ipaddress.ip_address(status['podIP'])
            host = '[' + str(address) + ']' if address.version == 6 else str(address)
            key = host + ':9090'
            if key in by_address:
                raise GateError('ambiguous generator address')
            by_address[key] = pod
    bind_leader(endpoint, pods, run_id)
    generation_text = endpoint['metadata'].get('annotations', {}).get('apexload.io/execution-lease-generation', '')
    if not isinstance(generation_text, str) or not re.fullmatch(r'[1-9][0-9]{0,19}', generation_text):
        raise GateError('missing authoritative generation')
    generation = int(generation_text)
    if (run.get('id') != config['test_id'] or run.get('project_id') != config['project_id']
            or timestamp(run.get('created_at')) != timestamp(config['created_at'])):
        raise ObservationSafetyError('run incarnation changed')
    validate_exclusive_run_inventory(inventory, config, run)
    if (target.get('schema_version') != 2 or target.get('run_id') != run_id
            or target.get('test_id') != config['test_id']
            or target.get('target_instance_id') != config['target_instance_id']):
        raise ObservationSafetyError('target process identity changed')
    if type(target.get('target_requests')) is not int or target['target_requests'] < 0:
        raise ObservationSafetyError('invalid target counter')
    observed = target.get('observed_at_unix')
    if type(observed) not in (int, float) or not math.isfinite(observed) or not now - 5 <= observed <= now:
        raise GateError('target observation is not current')
    restarts = 0
    for baseline in config['surviving_controllers']:
        if baseline['node'] not in (cluster + '-worker', cluster + '-worker2'):
            raise GateError('controller baseline is outside this cluster')
        current = by_uid.get(baseline['uid'])
        if current is None:
            # Replacement/removal of a designated survivor is not hidden by a
            # reset restart counter on its successor. The fault driver latches it.
            restarts += 1
            continue
        if (current['spec'].get('nodeName') != baseline['node']
                or current['metadata']['labels'].get('app.kubernetes.io/name') != 'apexload-controller'):
            raise GateError('controller placement identity changed')
        counts = [c.get('restartCount') for c in current.get('status', {}).get('containerStatuses', [])]
        if not counts or any(type(n) is not int or n < 0 for n in counts) or sum(counts) < baseline['restarts']:
            raise GateError('invalid controller restart evidence')
        restarts += sum(counts) - baseline['restarts']
    if type(demand) not in (int, float) or not math.isfinite(demand) or demand < 0:
        raise GateError('invalid current fleet demand')
    result = {'run_id': run_id, 'target_instance_id': target['target_instance_id'],
              'observed_at_unix': target['observed_at_unix'], 'target_requests': target['target_requests'],
              'status': run.get('status'), 'controller_restarts': restarts, 'keda_total_demand': demand,
              'observed_vusers': 0, 'assignments': []}
    if result['status'] in ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED'):
        # Let the driver's safety validator latch terminality immediately, rather
        # than disguising it as a retryable missing-live-metrics observation.
        return result
    continuity, live = run.get('continuity', {}), run.get('live_metrics', {})
    if (continuity.get('armed') is not True or type(continuity.get('execution_generation')) is not int
            or continuity['execution_generation'] != generation
            or live.get('observation_state', '') != ''
            or not now < timestamp(live.get('observation_valid_until')) <= now + 5
            or not now - 5 <= timestamp(live.get('timestamp')) <= now):
        raise GateError('run ownership or live metrics are not current')
    result['observed_vusers'] = live.get('active_vusers')
    shards = run.get('generator_shards')
    if not isinstance(shards, list) or not 1 <= len(shards) <= 1024:
        raise GateError('current generator assignments are required')
    for shard in shards:
        pod = by_address.get(shard.get('target'))
        if pod is None or pod['spec'].get('nodeName') not in (cluster + '-worker', cluster + '-worker2'):
            raise GateError('assignment cannot be bound to an owned worker pod')
        ordinal, count = shard.get('shard_ordinal', 0), shard.get('shard_count', 1)
        if type(count) is not int or not 1 <= count < 2**32 or type(ordinal) is not int or not 0 <= ordinal < count:
            raise GateError('invalid shard identity')
        group = shard.get('group')
        if not isinstance(group, str) or not group.strip() or len(group) > 256:
            raise GateError('invalid group name')
        result['assignments'].append({'group': group, 'shard': str(ordinal),
            'node': pod['spec']['nodeName'], 'pod': identifier(pod['metadata']['name']), 'generation': generation})
    validate_observation(result, {'run_id': run_id}, now=now)
    return result


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Reader:
    def __init__(self, config):
        validate_config(config)
        self.config = config
        self.inputs = input_root(config['inputs'])
        self.run_id, self.cluster = read_ownership(self.inputs)
        self.credentials = read_json(self.inputs, 'credentials.json')
        self.kube = ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'), '--context', 'kind-' + self.cluster,
                     '--request-timeout=3s', '-n', 'apexload-fault']
        self.processes = []
        self.deadline = time.monotonic() + 4
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
            urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(self.inputs / 'ca.crt'))))

    def remaining(self):
        value = self.deadline - time.monotonic()
        if value <= 0:
            raise GateError('observation deadline exceeded')
        return value

    def get(self, *args):
        result = subprocess.run(self.kube + list(args) + ['-o', 'json'], capture_output=True,
                                timeout=self.remaining())
        if result.returncode or len(result.stdout) > 4 * 1024 * 1024:
            raise GateError('Kubernetes observation unavailable')
        return json.loads(result.stdout, object_pairs_hook=unique_object)

    def forward(self, pod, port):
        identifier(pod)
        process = subprocess.Popen(self.kube + ['port-forward', 'pod/' + pod, '0:' + str(port), '--address', '127.0.0.1'],
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.processes.append(process)
        while process.poll() is None:
            if select.select([process.stdout], [], [], self.remaining())[0]:
                line = process.stdout.readline(2048)
                match = re.match(rb'Forwarding from (127\.0\.0\.1:[0-9]+) ->', line)
                if match:
                    return match[1].decode('ascii')
        raise GateError('pod forward unavailable')

    def request(self, url, headers=None):
        request = urllib.request.Request(url, headers=headers or {})
        with self.opener.open(request, timeout=self.remaining()) as response:
            body = response.read(4 * 1024 * 1024 + 1)
        if len(body) > 4 * 1024 * 1024:
            raise GateError('oversized observation')
        self.remaining()
        return json.loads(body, object_pairs_hook=unique_object)

    def observe(self):
        namespace = self.get('get', 'namespace', 'apexload-fault')
        if (namespace['metadata']['uid'] != self.config['namespace_uid']
                or namespace['metadata'].get('labels', {}).get('apexload.io/fault-run') != self.run_id):
            raise ObservationSafetyError('namespace ownership changed')
        pods = self.get('get', 'pods')['items']
        endpoint = self.get('get', 'endpointslice', 'apexload-controller-active')
        leader_pod = bind_leader(endpoint, pods, self.run_id)
        leader = leader_pod['metadata']
        def component(name):
            matches = [p for p in pods if p['metadata'].get('labels', {}).get('app.kubernetes.io/name') == name
                       and p['metadata'].get('labels', {}).get('apexload.io/fault-run') == self.run_id
                       and not p['metadata'].get('deletionTimestamp')]
            if len(matches) != 1:
                raise GateError('observer dependency pod is unavailable')
            return matches[0]['metadata']['name']
        api = self.forward(leader['name'], 8080)
        prom = self.forward(component('apexload-fault-prometheus'), 9090)
        target_address = self.forward(component('apexload-fault-target'), 8090)
        api_headers = {'X-Auth-Request-User': 'fault-' + self.run_id,
                       'X-APEXLoad-Proxy-Secret': self.credentials['http']}
        run = self.request('http://' + api + '/api/v1/tests/' + self.config['test_id'], api_headers)
        query = ('max(apexload_fleet_autoscaling{fleet="' + self.run_id + '",signal="total_vusers"}'
                 ' and on(fleet,job,instance) (apexload_fleet_autoscaling_valid_until > time()))')
        metrics = self.request('http://' + prom + '/api/v1/query?query=' + urllib.parse.quote(query))
        if metrics.get('status') != 'success' or len(metrics['data']['result']) != 1:
            raise GateError('current fleet demand unavailable')
        demand = float(metrics['data']['result'][0]['value'][1])
        target = self.request('https://' + target_address + '/snapshot',
                              {'Authorization': 'Bearer ' + self.credentials['target_observer'],
                               'X-Apexload-Fault-Test': self.config['test_id']})
        inventory = self.request('http://' + api + '/api/v1/tests?summary=true&limit=2000&offset=0', api_headers)
        current = self.get('get', 'endpointslice', 'apexload-controller-active')
        current_pod = self.get('get', 'pod', leader['name'])
        if current_pod['metadata']['uid'] != leader['uid']:
            raise GateError('leader pod changed during observation')
        bind_leader(current, [current_pod], self.run_id)
        if (current.get('endpoints') != endpoint.get('endpoints')
                or current['metadata'].get('annotations', {}).get('apexload.io/execution-lease-generation')
                != endpoint['metadata'].get('annotations', {}).get('apexload.io/execution-lease-generation')):
            raise GateError('leadership changed during observation')
        return project_observation(self.config, self.run_id, self.cluster, namespace, pods, endpoint, run, target, demand, inventory)

    def close(self):
        for process in self.processes:
            if process.poll() is None:
                process.kill()
            try:
                process.wait(timeout=.2)
            except subprocess.TimeoutExpired:
                pass
            process.stdout.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    reader = None
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 4)
    try:
        path = Path(args.config)
        config = read_json(path.parent, path.name)
        reader = Reader(config)
        result = reader.observe()
    except ObservationSafetyError:
        result = {'safety_violation': True}
    except (Exception, KeyboardInterrupt):
        raise SystemExit('Run observation unavailable; no credentials or raw responses emitted') from None
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        if reader is not None:
            reader.close()
    print(json.dumps(result))


if __name__ == '__main__':
    main()
