#!/usr/bin/env python3
"""Render private local Prometheus and KEDA scaling inputs; never install them."""
import argparse
import json
import os
from pathlib import Path
import re
import sys

from render_target import RenderError, input_root, read_ownership, validate_image, write_resources


def resources(inputs, image):
    run_id, cluster = read_ownership(input_root(inputs))
    validate_image(image)
    labels = {'app.kubernetes.io/name': 'apexload-fault-prometheus', 'apexload.io/fault-run': run_id}
    def obj(kind, name, api='v1'):
        return {'apiVersion': api, 'kind': kind, 'metadata': {'name': name, 'namespace': 'apexload-fault', 'labels': dict(labels)}}
    config = obj('ConfigMap', 'apexload-fault-prometheus')
    config['data'] = {'prometheus.yml': json.dumps({'global': {'scrape_interval': '2s', 'scrape_timeout': '1s'},
        'scrape_configs': [{'job_name': 'apexload-fault', 'metrics_path': '/metrics',
                            'dns_sd_configs': [{'names': ['apexload-controller-metrics.apexload-fault.svc'],
                                                'type': 'A', 'port': 9091, 'refresh_interval': '5s'}]}]})}
    workload = obj('Deployment', 'apexload-fault-prometheus', 'apps/v1')
    probe = {'httpGet': {'path': '/-/ready', 'port': 'http'}, 'timeoutSeconds': 2, 'periodSeconds': 5}
    workload['spec'] = {'replicas': 1, 'strategy': {'type': 'Recreate'}, 'selector': {'matchLabels': labels},
        'template': {'metadata': {'labels': labels}, 'spec': {'automountServiceAccountToken': False,
            'nodeSelector': {'kubernetes.io/hostname': cluster + '-control-plane'},
            'tolerations': [{'key': 'node-role.kubernetes.io/control-plane', 'operator': 'Exists', 'effect': 'NoSchedule'}],
            'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532, 'fsGroup': 65532,
                                'seccompProfile': {'type': 'RuntimeDefault'}},
            'containers': [{'name': 'prometheus', 'image': image, 'imagePullPolicy': 'IfNotPresent',
                'args': ['--config.file=/etc/prometheus/prometheus.yml', '--storage.tsdb.path=/prometheus',
                         '--storage.tsdb.retention.time=4h', '--storage.tsdb.retention.size=128MB',
                         '--storage.tsdb.wal-compression', '--web.listen-address=:9090'],
                'ports': [{'name': 'http', 'containerPort': 9090}],
                'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'}, 'limits': {'cpu': '1', 'memory': '512Mi'}},
                'volumeMounts': [{'name': 'config', 'mountPath': '/etc/prometheus', 'readOnly': True},
                                 {'name': 'data', 'mountPath': '/prometheus'}, {'name': 'tmp', 'mountPath': '/tmp'}],
                'startupProbe': {**probe, 'failureThreshold': 30}, 'readinessProbe': probe,
                'livenessProbe': {'httpGet': {'path': '/-/healthy', 'port': 'http'}, 'periodSeconds': 10, 'timeoutSeconds': 2}}],
            'volumes': [{'name': 'config', 'configMap': {'name': 'apexload-fault-prometheus'}},
                        {'name': 'data', 'emptyDir': {'sizeLimit': '512Mi'}}, {'name': 'tmp', 'emptyDir': {'sizeLimit': '32Mi'}}]}}}
    service = obj('Service', 'apexload-fault-prometheus')
    service['spec'] = {'selector': labels, 'ports': [{'name': 'http', 'port': 9090, 'targetPort': 'http'}]}
    metrics = obj('Service', 'apexload-controller-metrics')
    metrics['spec'] = {'clusterIP': 'None', 'selector': {'app.kubernetes.io/name': 'apexload-controller'},
                       'ports': [{'name': 'metrics', 'port': 9091, 'targetPort': 'metrics'}]}
    scrape = obj('NetworkPolicy', 'apexload-fault-prometheus-scrape', 'networking.k8s.io/v1')
    scrape['spec'] = {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-controller'}}, 'policyTypes': ['Ingress'],
        'ingress': [{'from': [{'podSelector': {'matchLabels': labels}}], 'ports': [{'protocol': 'TCP', 'port': 9091}]}]}
    ingress = obj('NetworkPolicy', 'apexload-fault-prometheus', 'networking.k8s.io/v1')
    ingress['spec'] = {'podSelector': {'matchLabels': labels}, 'policyTypes': ['Ingress'], 'ingress': [
        {'from': [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'keda'}}},
                  {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-fault-observer'}}}],
         'ports': [{'protocol': 'TCP', 'port': 9090}]}]}
    return run_id, {'apiVersion': 'v1', 'kind': 'List', 'items': [config, workload, service, metrics, scrape, ingress]}


def render(inputs, image, output, pod_capacity=100, minimum=2, maximum=4, accelerated=False):
    run_id, manifest = resources(inputs, image)
    if (type(pod_capacity) is not int or not 1 <= pod_capacity <= 1000
            or type(minimum) is not int or type(maximum) is not int or not 2 <= minimum <= maximum <= 12
            or type(accelerated) is not bool):
        raise RenderError('invalid local autoscaling bounds')
    # Preserve the shipped PromQL and all triggers. Only its installation token
    # and explicit local sizing/timing are substituted, with structural checks.
    base = Path(__file__).resolve().parents[2] / 'deployments/k8s/onprem/generator-keda-scaledobject.yaml'
    text = base.read_text()
    signals = re.findall(r'signal="([a-z_]+)"', text)
    expected = ['total_vusers', 'active_vusers', 'total_browser_vusers', 'active_browser_vusers',
                'saturated_generators', 'reserved_replica_floor']
    if signals != expected or text.count('${APEXLOAD_FLEET_ID}') != len(expected):
        raise RenderError('production autoscaling template changed; review local patches')
    text = text.replace('${APEXLOAD_FLEET_ID}', run_id)
    patches = [{'op': 'replace', 'path': '/spec/minReplicaCount', 'value': minimum},
               {'op': 'replace', 'path': '/spec/maxReplicaCount', 'value': maximum}]
    for i in range(len(expected)):
        patches.append({'op': 'replace', 'path': '/spec/triggers/' + str(i) + '/metadata/serverAddress',
                        'value': 'http://apexload-fault-prometheus.apexload-fault.svc:9090'})
    for i in (0, 1):
        patches.append({'op': 'replace', 'path': '/spec/triggers/' + str(i) + '/metadata/threshold', 'value': str(pod_capacity)})
    if accelerated:
        patches.extend([{'op': 'replace', 'path': '/spec/pollingInterval', 'value': 5},
                        {'op': 'replace', 'path': '/spec/cooldownPeriod', 'value': 30},
                        {'op': 'replace', 'path': '/spec/advanced/horizontalPodAutoscalerConfig/behavior', 'value': {
                            'scaleUp': {'stabilizationWindowSeconds': 0, 'policies': [{'type': 'Pods', 'value': 2, 'periodSeconds': 15}]},
                            'scaleDown': {'stabilizationWindowSeconds': 30, 'policies': [{'type': 'Pods', 'value': 1, 'periodSeconds': 15}]}}}])
    overlay = {'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'namespace': 'apexload-fault',
        'resources': ['scaledobject-base.yaml'], 'labels': [{'pairs': {'apexload.io/fault-run': run_id}}],
        'patches': [{'target': {'kind': 'ScaledObject', 'name': 'apexload-generator'}, 'patch': json.dumps(patches)}]}
    root = Path(output)
    if not root.is_absolute():
        raise RenderError('output must be a new absolute directory')
    try:
        root.mkdir(mode=0o700, exist_ok=False)
        write_resources(root / 'prometheus', manifest, 'prometheus-resources.json')
        (root / 'autoscaling').mkdir(mode=0o700)
        # This is a merge patch, deliberately NOT an apply resource: applying a
        # partial ConfigMap could remove the production data-platform settings.
        for path, content in ((root / 'autoscaling/scaledobject-base.yaml', text),
                              (root / 'autoscaling/kustomization.yaml', json.dumps(overlay, indent=2)),
                              (root / 'controller-autoscaling-merge-patch.json', json.dumps({'data': {'APEXLOAD_GENERATOR_AUTOSCALING_OWNER': 'keda'}})),
                              (root / 'profile.json', json.dumps({'run_id': run_id, 'prometheus_image': image,
                                  'minimum': minimum, 'maximum': maximum, 'pod_capacity': pod_capacity,
                                  'timing': 'accelerated' if accelerated else 'production'}, indent=2))):
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'w') as stream:
                stream.write(content + '\n')
    except OSError:
        raise RenderError('cannot create new private monitoring resources') from None
    return run_id


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True)
    parser.add_argument('--prometheus-image', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--pod-capacity', type=int, default=100)
    parser.add_argument('--minimum', type=int, default=2)
    parser.add_argument('--maximum', type=int, default=4)
    parser.add_argument('--accelerated', action='store_true')
    args = parser.parse_args()
    try:
        run_id = render(args.inputs, args.prometheus_image, args.output, args.pod_capacity, args.minimum, args.maximum, args.accelerated)
    except RenderError as error:
        print(str(error), file=sys.stderr)
        return 1
    print('Rendered private monitoring inputs for run ' + run_id)
    return 0


if __name__ == '__main__':
    sys.exit(main())
