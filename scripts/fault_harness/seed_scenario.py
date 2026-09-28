#!/usr/bin/env python3
"""Create a private HTTP scenario in an owned disposable HA installation, without running it."""
import argparse
import json
import os
from pathlib import Path
import re
import select
import signal
import subprocess
import threading
import time
import urllib.request

from observe_run import NoRedirect, bind_leader, identifier
from render_target import input_root, read_input, read_json, read_ownership, unique_object


class SeedError(RuntimeError):
    pass


class OwnedAPI:
    def __init__(self, inputs):
        self.inputs = input_root(inputs)
        self.run_id, self.cluster = read_ownership(self.inputs)
        self.kubeconfig = read_input(self.inputs, 'kubeconfig')
        self.credentials = read_json(self.inputs, 'credentials.json')
        if any(not isinstance(self.credentials.get(key), str)
               or not re.fullmatch(r'[A-Za-z0-9_-]{32,128}', self.credentials[key])
               for key in ('http', 'target_load')):
            raise SeedError('fresh private HTTP and target credentials are required')
        self.k = ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'),
                  '--context', 'kind-' + self.cluster, '--namespace', 'apexload-fault']
        self.deadline = time.monotonic() + 100
        self.namespace_uid = None
        self.leader_uid = None
        self.epoch = None
        self.node_ids = None
        self.process = None
        self.drain = None
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def remaining(self):
        left = self.deadline - time.monotonic()
        if left <= 0:
            raise SeedError('scenario setup deadline exceeded')
        return min(10, left)

    def get(self, *args):
        result = subprocess.run(self.k + ['get', *args, '-o', 'json'], capture_output=True,
                                timeout=self.remaining())
        if result.returncode or len(result.stdout) > 4 * 1024 * 1024:
            raise SeedError('owned installation is unavailable')
        return json.loads(result.stdout, object_pairs_hook=unique_object)

    def verify(self):
        if read_input(self.inputs, 'kubeconfig') != self.kubeconfig:
            raise SeedError('kubeconfig changed during setup')
        ns = self.get('namespace', 'apexload-fault')['metadata']
        if (ns.get('name') != 'apexload-fault' or ns.get('labels', {}).get('apexload.io/fault-run') != self.run_id
                or (self.namespace_uid is not None and ns.get('uid') != self.namespace_uid)):
            raise SeedError('disposable namespace ownership changed')
        self.namespace_uid = identifier(ns.get('uid'))
        nodes = self.get('nodes')['items']
        node_ids = {n['metadata']['name']: identifier(n['metadata'].get('uid')) for n in nodes}
        expected = {self.cluster + suffix for suffix in ('-control-plane', '-worker', '-worker2')}
        if (len(nodes) != 3 or set(node_ids) != expected
                or (self.node_ids is not None and node_ids != self.node_ids)):
            raise SeedError('disposable cluster nodes changed')
        self.node_ids = node_ids
        pods = self.get('pods', '-l', 'app.kubernetes.io/name=apexload-controller')['items']
        endpoint = self.get('endpointslice', 'apexload-controller-active')
        pod = bind_leader(endpoint, pods, self.run_id)
        uid = pod['metadata']['uid']
        epoch = endpoint['metadata'].get('annotations', {}).get('apexload.io/execution-lease-generation')
        if (not isinstance(epoch, str) or not re.fullmatch(r'[1-9][0-9]{0,19}', epoch)
                or (self.leader_uid is not None and (uid != self.leader_uid or epoch != self.epoch))):
            raise SeedError('leadership changed during setup')
        self.leader_uid, self.epoch = uid, epoch
        return pod['metadata']['name']

    def connect(self):
        name = self.verify()
        self.process = subprocess.Popen(self.k + ['port-forward', 'pod/' + name, '0:8080', '--address', '127.0.0.1'],
                                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        while self.process.poll() is None:
            if select.select([self.process.stdout], [], [], self.remaining())[0]:
                line = self.process.stdout.readline(2048)
                match = re.fullmatch(rb'Forwarding from (127\.0\.0\.1:[0-9]+) -> 8080\r?\n', line)
                if match:
                    self.address = 'http://' + match[1].decode('ascii') + '/api/v1/'
                    # A long-lived forward otherwise blocks when its log pipe
                    # fills. Drain into no retained buffer, including on errors.
                    stream = self.process.stdout
                    self.drain = threading.Thread(target=lambda: self.drain_output(stream), daemon=True)
                    self.drain.start()
                    self.verify()
                    return
        raise SeedError('controller forward unavailable')

    @staticmethod
    def drain_output(stream):
        try:
            while stream.read(4096):
                pass
        except (OSError, ValueError):
            pass

    def request(self, method, path, body=None):
        if not re.fullmatch(r'[A-Za-z0-9_/-]+', path) or '..' in path:
            raise SeedError('invalid API resource path')
        self.verify()
        headers = {'Content-Type': 'application/json', 'X-Auth-Request-User': 'fault-' + self.run_id,
                   'X-APEXLoad-Proxy-Secret': self.credentials['http']}
        req = urllib.request.Request(self.address + path, headers=headers, method=method,
                                     data=None if body is None else json.dumps(body).encode())
        with self.opener.open(req, timeout=self.remaining()) as response:
            raw = response.read(4 * 1024 * 1024 + 1)
        if len(raw) > 4 * 1024 * 1024:
            raise SeedError('oversized API response')
        self.remaining()
        return json.loads(raw, object_pairs_hook=unique_object)

    def close(self):
        if self.process is not None:
            if self.process.poll() is None:
                self.process.kill()
            self.process.wait(timeout=5)
            if self.drain is not None:
                self.drain.join(timeout=5)
            self.process.stdout.close()


def seed(api, output):
    output = Path(output)
    if not output.is_absolute() or output.exists() or output.is_symlink():
        raise SeedError('output must be a new absolute directory')
    output.mkdir(mode=0o700)
    events = []
    phase = 'preflight'

    def save(name, value):
        fd = os.open(output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as f:
            json.dump(value, f, indent=2)
            f.write('\n')

    try:
        api.connect()
        inventory = api.request('GET', 'system/inventory')
        platform = inventory.get('data_platform', {})
        if any(platform.get(key) != value for key, value in
               (('metadata_store', 'postgres'), ('cache_backend', 'redis'), ('archive_backend', 's3'))):
            raise SeedError('PostgreSQL, Redis and S3 are required for this scenario')
        phase = 'project'
        project = api.request('POST', 'projects', {'name': 'fault-' + api.run_id + '-scenario'})
        project_id = identifier(project.get('id'))
        events.append({'resource': 'project', 'id': project_id})
        save('project.json', {'project_id': project_id})
        phase = 'project_secret'
        api.request('POST', 'projects/' + project_id + '/secrets', {
            'secrets': [{'name': 'fault_load', 'value': api.credentials['target_load']}]})
        events.append({'resource': 'project_secret', 'name': 'fault_load'})
        phase = 'script'
        script = api.request('POST', 'scripts', {'project_id': project_id, 'script': {
            'name': 'Private fault traffic', 'version': '1.0.0', 'protocol': 'http',
            'config': {'base_url': 'https://apexload-fault-target.apexload-fault.svc',
                       'pacing': {'type': 'fixed_interval', 'interval_ms': 100},
                       'http': {'connect_timeout_ms': 2000, 'request_timeout_ms': 5000}},
            'actions': [{'type': 'http', 'name': 'counter', 'method': 'GET', 'target': '/load',
                         'headers': {'Authorization': 'Bearer {{secret.fault_load}}', 'X-Apexload-Fault-Run': api.run_id}}]}})
        script_id = identifier(script.get('id'))
        if script.get('project_id') != project_id:
            raise SeedError('created script belongs to an unexpected project')
        events.append({'resource': 'script', 'id': script_id})
        save('script.json', {'project_id': project_id, 'script_id': script_id})
        phase = 'scenario'
        scenario = api.request('POST', 'scenarios', {'name': 'Private HTTP healthy control', 'project_id': project_id,
            'duration': '5m', 'assignment': {'failover_policy': 'continue'},
            'groups': [{'name': 'load', 'script_id': script_id, 'vusers': 40}]})
        scenario_id = identifier(scenario.get('id'))
        if scenario.get('project_id') != project_id:
            raise SeedError('created scenario belongs to an unexpected project')
        events.append({'resource': 'scenario', 'id': scenario_id})
        api.verify()
        save('result.json', {'seeded': True, 'qualified': False, 'run_id': api.run_id,
            'namespace_uid': api.namespace_uid, 'controller_uid': api.leader_uid, 'execution_generation': api.epoch,
            'project_id': project_id, 'script_id': script_id, 'scenario_id': scenario_id,
            'scope': 'Saved scenario only; no test was created or started'})
    except BaseException:
        events.append({'failed': True, 'resources_preserved': True, 'last_attempted_phase': phase,
                       'last_mutation_may_have_committed': phase != 'preflight'})
        raise
    finally:
        try:
            save('events.json', events)
        finally:
            api.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 110)
    try:
        seed(OwnedAPI(args.inputs), args.output)
    except (Exception, KeyboardInterrupt):
        raise SystemExit('Scenario setup failed; inspect private receipts. Resources were preserved; no raw responses emitted.') from None
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
    print('Saved the private HTTP scenario; execution and qualification remain separate.')


if __name__ == '__main__':
    main()
