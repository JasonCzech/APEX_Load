#!/usr/bin/env python3
"""Deploy the private HTTP HA profile into a fresh namespace of its owned kind cluster."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time

import render_monitoring
import render_stack
from render_target import input_root, read_input, read_ownership


class DeploymentError(RuntimeError):
    pass


def command(argv, data=None, timeout=20):
    result = subprocess.run(argv, input=data, capture_output=True, text=True, timeout=timeout)
    if result.returncode or len(result.stdout) > 2 * 1024 * 1024:
        # Kubernetes errors can echo Secret manifests. Do not expose them.
        raise DeploymentError('deployment command failed; resources and private inputs were preserved')
    return result.stdout


class StackDeployment:
    def __init__(self, inputs, images, output, minimum=2, maximum=4, pod_capacity=100,
                 accelerated=False, execute=command):
        self.inputs = input_root(inputs)
        self.run_id, self.cluster = read_ownership(self.inputs)
        self.kubeconfig = read_input(self.inputs, 'kubeconfig')
        self.execute = execute
        self.k = ['kubectl', '--kubeconfig', str(self.inputs / 'kubeconfig'),
                  '--context', 'kind-' + self.cluster, '--namespace', 'apexload-fault']
        self.output = Path(output)
        if not self.output.is_absolute() or self.output.exists() or self.output.is_symlink():
            raise DeploymentError('output must be a new absolute directory')
        self.output.mkdir(mode=0o700)
        # Renderers validate TLS, pins, capacity and private inputs. Only these
        # derived manifests may be applied; no arbitrary manifest path is accepted.
        render_stack.render(self.inputs, images, self.output / 'stack', maximum, pod_capacity)
        render_monitoring.render(self.inputs, images['prometheus'], self.output / 'monitoring',
                                 pod_capacity, minimum, maximum, accelerated)
        overlay = self.output / 'stack/application/kustomization.yaml'
        content = json.loads(overlay.read_text())
        content['patches'].append({'patch': json.dumps({'apiVersion': 'v1', 'kind': 'ConfigMap',
            'metadata': {'name': 'apexload-data-platform'},
            'data': {'APEXLOAD_GENERATOR_AUTOSCALING_OWNER': 'keda'}})})
        overlay.write_text(json.dumps(content, indent=2) + '\n')
        self.node_ids = None
        self.namespace_uid = None
        self.events = []
        self.stage = 'preflight'

    def get(self, *args):
        return json.loads(self.execute(self.k + ['get', *args, '-o', 'json']))

    def owned(self, allow_absent=False):
        if read_input(self.inputs, 'kubeconfig') != self.kubeconfig:
            raise DeploymentError('kubeconfig changed during deployment')
        ids = {}
        for suffix, role in (('-control-plane', 'control-plane'), ('-worker', 'worker'), ('-worker2', 'worker')):
            name = self.cluster + suffix
            inspected = json.loads(self.execute(['docker', 'inspect', name]))
            if not isinstance(inspected, list) or len(inspected) != 1:
                raise DeploymentError('ambiguous kind node')
            node = inspected[0]
            labels = node.get('Config', {}).get('Labels', {})
            if (node.get('Name') != '/' + name or node.get('State', {}).get('Running') is not True
                    or labels.get('io.x-k8s.kind.cluster') != self.cluster
                    or labels.get('io.x-k8s.kind.role') != role
                    or not re.fullmatch(r'[a-f0-9]{64}', node.get('Id', ''))):
                raise DeploymentError('node is not the expected running disposable container')
            ids[name] = node['Id']
        nodes = self.get('nodes')['items']
        kubernetes_ids = {n['metadata']['name']: n['metadata']['uid'] for n in nodes}
        if (len(nodes) != 3 or set(kubernetes_ids) != set(ids)
                or not all(isinstance(uid, str) and uid for uid in kubernetes_ids.values())):
            raise DeploymentError('unexpected Kubernetes node inventory')
        identity = {'containers': ids, 'nodes': kubernetes_ids}
        if self.node_ids is not None and identity != self.node_ids:
            raise DeploymentError('cluster nodes changed during deployment')
        self.node_ids = identity
        raw = self.execute(self.k + ['get', 'namespace', 'apexload-fault', '--ignore-not-found', '-o', 'json'])
        if not raw.strip():
            if allow_absent and self.namespace_uid is None:
                return
            raise DeploymentError('owned namespace disappeared')
        ns = json.loads(raw)['metadata']
        if (ns.get('labels', {}).get('apexload.io/fault-run') != self.run_id
                or not isinstance(ns.get('uid'), str) or not ns['uid']
                or (self.namespace_uid is not None and ns['uid'] != self.namespace_uid)):
            raise DeploymentError('namespace is not the same disposable installation')
        self.namespace_uid = ns['uid']

    def phase(self, name):
        self.stage = name
        self.owned()
        self.events.append({'phase': name, 'at_unix': time.time(), 'namespace_uid': self.namespace_uid,
                            'identity': self.node_ids})

    def apply(self, manifest):
        self.owned()
        self.execute(self.k + ['apply', '-f', '-'], data=manifest, timeout=45)

    def ready(self, resource):
        self.execute(self.k + ['rollout', 'status', resource, '--timeout=180s'], timeout=185)

    def run(self):
        try:
            self.owned(allow_absent=True)
            # This command never upgrades an installation or interrupts live work.
            if self.namespace_uid is not None:
                raise DeploymentError('namespace already exists; use a fresh owned cluster for deployment')
            crd = self.get('crd', 'scaledobjects.keda.sh')
            metrics = self.get('apiservice', 'v1beta1.external.metrics.k8s.io')
            if (not any(c.get('type') == 'Established' and c.get('status') == 'True' for c in crd.get('status', {}).get('conditions', []))
                    or not any(c.get('type') == 'Available' and c.get('status') == 'True' for c in metrics.get('status', {}).get('conditions', []))):
                raise DeploymentError('KEDA and its external metrics API must be ready before deployment')
            rendered = {}
            for role, path in (('dependencies', 'stack/dependencies'), ('application', 'stack/application'),
                               ('prometheus', 'monitoring/prometheus'), ('autoscaling', 'monitoring/autoscaling')):
                rendered[role] = self.execute(['kubectl', 'kustomize', str(self.output / path)])
                if not rendered[role].strip():
                    raise DeploymentError('empty rendered resources')
            # Recheck immediately before the first mutation. Namespace creation
            # uses create (not apply), so a concurrent creator cannot be adopted.
            self.owned(allow_absent=True)
            if self.namespace_uid is not None:
                raise DeploymentError('namespace appeared during preflight')
            namespace = {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'apexload-fault',
                'labels': {'apexload.io/fault-run': self.run_id, 'pod-security.kubernetes.io/enforce': 'restricted',
                           'pod-security.kubernetes.io/enforce-version': 'v1.34'}}}
            created = json.loads(self.execute(self.k + ['create', '-f', '-', '-o', 'json'], data=json.dumps(namespace)))
            meta = created['metadata']
            if meta.get('name') != 'apexload-fault' or not isinstance(meta.get('uid'), str) or not meta['uid']:
                raise DeploymentError('namespace creation did not return an identity')
            self.namespace_uid = meta['uid']
            self.phase('kindnet-resources')
            self.execute(self.k + ['--namespace', 'kube-system', 'patch', 'daemonset', 'kindnet',
                                  '--type=strategic', '--patch-file', str(self.output / 'stack/kindnet-resources-patch.json')])
            self.execute(self.k + ['--namespace', 'kube-system', 'rollout', 'status',
                                  'daemonset/kindnet', '--timeout=180s'], timeout=185)
            self.phase('dependencies')
            self.apply(rendered['dependencies'])
            for resource in ('statefulset/apexload-postgres', 'statefulset/apexload-redis',
                             'deployment/apexload-s3', 'deployment/apexload-fault-target'):
                self.ready(resource)
            self.phase('bootstrap-auth')
            self.execute(self.k + ['exec', '-i', 'apexload-postgres-0', '--', 'psql', '-U', 'apexload',
                                  '-d', 'apexload', '-v', 'ON_ERROR_STOP=1'],
                         data=(self.output / 'stack/bootstrap-auth.sql').read_text(), timeout=30)
            self.phase('application')
            self.apply(rendered['application'])
            self.ready('deployment/apexload-controller')
            self.ready('statefulset/apexload-generator')
            self.phase('monitoring')
            self.apply(rendered['prometheus'])
            self.ready('deployment/apexload-fault-prometheus')
            self.apply(rendered['autoscaling'])
            self.execute(self.k + ['wait', '--for=condition=Ready', 'scaledobject/apexload-generator', '--timeout=90s'], timeout=95)
            self.phase('completed')
            self.write('result.json', {'deployed': True, 'qualified': False, 'run_id': self.run_id,
                'namespace_uid': self.namespace_uid, 'identity': self.node_ids,
                'scope': 'Kubernetes-ready disposable stack; load and failure qualification must run separately'})
        except BaseException:
            self.events.append({'phase': self.stage, 'failed': True, 'resources_preserved': True})
            raise
        finally:
            self.write('events.json', self.events)

    def write(self, name, value):
        fd = os.open(self.output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream, indent=2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('inputs', 'output', 'controller-image', 'generator-image', 'target-image',
                 'postgres-image', 'redis-image', 's3-image', 'prometheus-image'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--minimum', type=int, default=2)
    parser.add_argument('--maximum', type=int, default=4)
    parser.add_argument('--pod-capacity', type=int, default=100)
    parser.add_argument('--accelerated', action='store_true')
    args = parser.parse_args()
    images = {role: getattr(args, role + '_image') for role in
              ('controller', 'generator', 'target', 'postgres', 'redis', 's3', 'prometheus')}
    previous_term = signal.getsignal(signal.SIGTERM)
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        StackDeployment(args.inputs, images, args.output, args.minimum, args.maximum,
                        args.pod_capacity, args.accelerated).run()
    except (ValueError, OSError, RuntimeError, subprocess.SubprocessError, KeyboardInterrupt):
        parser.exit(1, 'Deployment failed; private evidence and cluster resources were preserved.\n')
    finally:
        signal.signal(signal.SIGTERM, previous_term)
    print('Disposable stack deployed. Load and failure qualification remain separate.')


if __name__ == '__main__':
    main()
