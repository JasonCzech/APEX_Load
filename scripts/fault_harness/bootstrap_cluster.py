#!/usr/bin/env python3
"""Create a fresh three-node Docker Desktop kind cluster and install pinned KEDA 2.21.0."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess

from render_target import input_root, read_input, read_json, read_ownership


class BootstrapError(RuntimeError):
    pass


def command(argv, timeout=20, data=None):
    env = dict(os.environ, DOCKER_CONTEXT='desktop-linux', KIND_EXPERIMENTAL_PROVIDER='docker')
    for key in ('DOCKER_HOST', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH'):
        env.pop(key, None)
    result = subprocess.run(argv, input=data, text=True, capture_output=True, timeout=timeout, env=env)
    if result.returncode or len(result.stdout) > 8 * 1024 * 1024:
        raise BootstrapError('bootstrap command failed; resources were retained')
    return result.stdout


class Bootstrap:
    def __init__(self, inputs, output, kind_binary, node_image, chart, chart_sha256, images, execute=command):
        self.inputs = input_root(inputs)
        self.run_id, self.cluster = read_ownership(self.inputs)
        self.execute = execute
        self.kind = str(Path(kind_binary).resolve(strict=True))
        if not Path(self.kind).is_file() or not os.access(self.kind, os.X_OK):
            raise BootstrapError('kind must be an executable file')
        if not re.fullmatch(r'kindest/node@sha256:[a-f0-9]{64}', node_image):
            raise BootstrapError('a digest-pinned kind node image is required')
        if not re.fullmatch(r'[a-f0-9]{64}', chart_sha256):
            raise BootstrapError('a trusted chart checksum is required')
        chart = Path(chart)
        if chart.is_symlink() or not chart.is_file() or chart.stat().st_size > 8 * 1024 * 1024:
            raise BootstrapError('chart must be a bounded local archive')
        chart_bytes = chart.read_bytes()
        if hashlib.sha256(chart_bytes).hexdigest() != chart_sha256:
            raise BootstrapError('chart checksum differs from the approved artifact')
        repos = {'keda': 'keda', 'metricsApiServer': 'keda-metrics-apiserver', 'webhooks': 'keda-admission-webhooks'}
        if set(images) != set(repos):
            raise BootstrapError('all three KEDA image pins are required')
        values = {'image': {'pullPolicy': 'IfNotPresent'}, 'clusterName': self.cluster,
                  'nodeSelector': {'kubernetes.io/hostname': self.cluster + '-control-plane'},
                  'tolerations': [{'key': 'node-role.kubernetes.io/control-plane', 'operator': 'Exists', 'effect': 'NoSchedule'}]}
        for role, repo in repos.items():
            image = images[role]
            if not isinstance(image, str) or not re.fullmatch('ghcr.io/kedacore/' + repo + r'@sha256:[a-f0-9]{64}', image):
                raise BootstrapError('KEDA images must pin the official component repositories')
            values['image'][role] = {'registry': 'ghcr.io', 'repository': 'kedacore/' + repo,
                                      'tag': '2.21.0@' + image.split('@')[1]}
        self.images, self.node_image, self.chart_hash = images, node_image, chart_sha256
        self.output = Path(output)
        if not self.output.is_absolute() or self.output.exists() or self.output.is_symlink():
            raise BootstrapError('output must be a new absolute directory')
        if (self.inputs / 'kubeconfig').exists() or (self.inputs / 'kubeconfig').is_symlink():
            raise BootstrapError('existing kubeconfig refused; prepare fresh inputs')
        if shutil.disk_usage(self.inputs).free < 8 * 1024**3:
            raise BootstrapError('at least 8 GiB of free host storage is required')
        self.output.mkdir(mode=0o700)
        self.write_bytes('keda.tgz', chart_bytes)
        self.write('keda-values.json', values)
        self.write('kind.json', {'kind': 'Cluster', 'apiVersion': 'kind.x-k8s.io/v1alpha4',
                                'nodes': [{'role': 'control-plane'}, {'role': 'worker'}, {'role': 'worker'}]})
        self.artifact_hashes = {name: hashlib.sha256((self.output / name).read_bytes()).hexdigest()
                                for name in ('keda.tgz', 'keda-values.json', 'kind.json')}
        self.k = ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'), '--context', 'kind-' + self.cluster]
        self.node_ids = None
        self.kubeconfig = None
        self.keda_uid = None
        self.engine_id = None
        self.events = []
        self.phase = 'preflight'

    def write_bytes(self, name, value):
        fd = os.open(self.output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as f:
            f.write(value)

    def write(self, name, value):
        self.write_bytes(name, (json.dumps(value, indent=2) + '\n').encode())

    def docker(self, *args):
        return self.execute(['docker', '--context', 'desktop-linux', *args])

    def engine(self):
        value = json.loads(self.docker('info', '--format', '{{json .}}'))
        if (value.get('OperatingSystem') != 'Docker Desktop' or value.get('OSType') != 'linux'
                or not value.get('ID') or (self.engine_id is not None and value['ID'] != self.engine_id)):
            raise BootstrapError('the Docker Desktop engine changed or is unavailable')
        self.engine_id = value['ID']

    def assets(self):
        if read_ownership(self.inputs) != (self.run_id, self.cluster):
            raise BootstrapError('input ownership changed')
        for name, expected in self.artifact_hashes.items():
            path = self.output / name
            if (path.is_symlink() or not path.is_file() or path.stat().st_size > 8 * 1024 * 1024
                    or hashlib.sha256(path.read_bytes()).hexdigest() != expected):
                raise BootstrapError('bootstrap artifact changed')

    def owned(self):
        self.assets()
        self.engine()
        if read_ownership(self.inputs) != (self.run_id, self.cluster):
            raise BootstrapError('input ownership changed')
        config = read_input(self.inputs, 'kubeconfig')
        if self.kubeconfig is not None and config != self.kubeconfig:
            raise BootstrapError('kubeconfig changed')
        self.kubeconfig = config
        names = {self.cluster + suffix: role for suffix, role in
                 (('-control-plane', 'control-plane'), ('-worker', 'worker'), ('-worker2', 'worker'))}
        containers = json.loads(self.docker('inspect', *sorted(names)))
        ids = {}
        for c in containers:
            name = c.get('Name', '').removeprefix('/')
            labels = c.get('Config', {}).get('Labels', {})
            if (name not in names or name in ids or labels.get('io.x-k8s.kind.cluster') != self.cluster
                    or labels.get('io.x-k8s.kind.role') != names[name] or c.get('State', {}).get('Running') is not True
                    or not re.fullmatch(r'[a-f0-9]{64}', c.get('Id', ''))):
                raise BootstrapError('unexpected disposable node container')
            ids[name] = c['Id']
        nodes = json.loads(self.execute(self.k + ['get', 'nodes', '-o', 'json']))['items']
        kube_ids = {n['metadata']['name']: n['metadata']['uid'] for n in nodes}
        identity = {'containers': ids, 'nodes': kube_ids}
        if (set(ids) != set(names) or len(nodes) != 3 or set(kube_ids) != set(names)
                or not all(isinstance(uid, str) and uid for uid in kube_ids.values())
                or (self.node_ids is not None and self.node_ids != identity)):
            raise BootstrapError('cluster node identity changed')
        self.node_ids = identity
        if self.keda_uid is not None:
            ns = json.loads(self.execute(self.k + ['get', 'namespace', 'keda', '-o', 'json']))['metadata']
            if ns.get('uid') != self.keda_uid or ns.get('labels', {}).get('apexload.io/fault-run') != self.run_id:
                raise BootstrapError('KEDA namespace identity changed')

    def run(self):
        try:
            self.assets()
            self.engine()
            existing = self.docker('ps', '--all', '--format', '{{.Names}}').splitlines()
            if any(name == self.cluster or name.startswith(self.cluster + '-') for name in existing):
                raise BootstrapError('cluster containers already exist; fresh inputs are required')
            self.docker('image', 'inspect', self.node_image)
            metadata = self.execute(['helm', 'show', 'chart', str(self.output / 'keda.tgz')])
            if any(not re.search(r'^' + key + ': ' + re.escape(value) + r'\s*$', metadata, re.MULTILINE)
                   for key, value in (('name', 'keda'), ('version', '2.21.0'), ('appVersion', '2.21.0'))):
                raise BootstrapError('this profile requires the trusted KEDA 2.21.0 chart')
            self.assets()
            self.engine()
            # This is a new private file, never the user's default kubeconfig.
            fd = os.open(self.inputs / 'kubeconfig', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            os.close(fd)
            self.phase = 'cluster-create'
            self.events.append({'phase': self.phase, 'engine_id': self.engine_id})
            self.execute([self.kind, 'create', 'cluster', '--name', self.cluster,
                          '--config', str(self.output / 'kind.json'), '--image', self.node_image,
                          '--kubeconfig', str(self.inputs / 'kubeconfig'), '--retain', '--wait', '180s'], timeout=300)
            self.owned()
            self.execute(self.k + ['wait', '--for=condition=Ready', 'nodes', '--all', '--timeout=120s'], timeout=125)
            self.owned()
            version = json.loads(self.execute(self.k + ['version', '-o', 'json']))
            if not re.fullmatch(r'v1\.(34|35|36)\.[0-9]+', version.get('serverVersion', {}).get('gitVersion', '')):
                raise BootstrapError('KEDA 2.21 profile requires Kubernetes 1.34 through 1.36')
            self.phase = 'keda-namespace'
            # create refuses a concurrent creator; existing namespaces are never adopted.
            namespace = {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'keda',
                         'labels': {'apexload.io/fault-run': self.run_id}}}
            ns = json.loads(self.execute(self.k + ['create', '-f', '-', '-o', 'json'], data=json.dumps(namespace)))['metadata']
            if ns.get('name') != 'keda' or not isinstance(ns.get('uid'), str) or not ns['uid']:
                raise BootstrapError('KEDA namespace creation returned no identity')
            self.keda_uid = ns['uid']
            self.owned()
            self.phase = 'keda-install'
            self.events.append({'phase': self.phase, 'identity': self.node_ids, 'keda_namespace_uid': self.keda_uid})
            self.execute(['helm', 'install', 'keda', str(self.output / 'keda.tgz'),
                          '--kubeconfig', str(self.inputs / 'kubeconfig'), '--kube-context', 'kind-' + self.cluster,
                          '--namespace', 'keda', '--values', str(self.output / 'keda-values.json'),
                          '--wait', '--timeout', '240s'], timeout=260)
            self.owned()
            self.execute(self.k + ['wait', '--for=condition=Established', 'crd/scaledobjects.keda.sh', '--timeout=60s'], timeout=65)
            self.execute(self.k + ['wait', '--for=condition=Available', 'apiservice/v1beta1.external.metrics.k8s.io', '--timeout=90s'], timeout=95)
            self.owned()
            deployments = json.loads(self.execute(self.k + ['get', 'deployments', '--namespace', 'keda', '-o', 'json']))['items']
            observed = []
            for deployment in deployments:
                spec = deployment['spec']['template']['spec']
                if spec.get('nodeSelector', {}).get('kubernetes.io/hostname') != self.cluster + '-control-plane':
                    raise BootstrapError('KEDA placement differs from the pinned profile')
                observed.extend(c['image'] for c in spec['containers'])
            expected = [image.replace('@', ':2.21.0@') for image in self.images.values()]
            if len(deployments) != 3 or sorted(observed) != sorted(expected):
                raise BootstrapError('deployed KEDA images differ from the pins')
            self.write('result.json', {'ready': True, 'qualified': False, 'run_id': self.run_id, 'identity': self.node_ids,
                'keda_namespace_uid': self.keda_uid, 'node_image': self.node_image, 'keda_chart_sha256': self.chart_hash,
                'keda_images': self.images, 'scope': 'Fresh cluster and KEDA readiness; application deployment and fault qualification remain'})
        except BaseException:
            self.events.append({'phase': self.phase, 'failed': True, 'resources_preserved': True})
            raise
        finally:
            self.write('events.json', self.events)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('inputs', 'output', 'kind-binary', 'node-image', 'keda-chart', 'keda-chart-sha256', 'keda-images'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 900)
    try:
        images_file = Path(args.keda_images)
        images = read_json(images_file.parent, images_file.name)
        Bootstrap(args.inputs, args.output, args.kind_binary, args.node_image, args.keda_chart,
                  args.keda_chart_sha256, images).run()
    except (Exception, KeyboardInterrupt):
        raise SystemExit('Cluster bootstrap failed; retain private inputs/evidence and inspect the owned resources. No deletion was attempted.') from None
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
    print('Created the disposable cluster and ready KEDA installation; application qualification remains separate.')


if __name__ == '__main__':
    main()
