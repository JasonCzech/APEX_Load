#!/usr/bin/env python3
"""Prepare private phased resources for the disposable HTTP HA fault profile."""
import argparse
import json
import os
from pathlib import Path
import re
import sys

import render_dependencies
import render_s3
import render_target
from render_target import (RenderError, input_root, read_input, read_json, read_ownership,
                           validate_image, validate_tls, write_resources)


def application(inputs, controller_image, generator_image, replicas=4, pod_capacity=100, source_retention_seconds=0):
    root = input_root(inputs)
    run_id, cluster = read_ownership(root)
    for image in (controller_image, generator_image):
        validate_image(image)
    for value, low, high in ((replicas, 2, 12), (pod_capacity, 1, 1000)):
        if type(value) is not int or not low <= value <= high:
            raise RenderError('invalid local replica count or pod capacity')
    if (type(source_retention_seconds) is not int or
            (source_retention_seconds != 0 and not 60 <= source_retention_seconds <= 720 * 3600)):
        raise RenderError('source retention must be disabled (0) or between 60 and 2592000 seconds')
    credentials = read_json(root, 'credentials.json')
    for key in ('grpc', 'http', 'wrapping_key', 'auth_session'):
        if not isinstance(credentials.get(key), str) or not re.fullmatch(r'[A-Za-z0-9_-]{32,128}', credentials[key]):
            raise RenderError('invalid application credential')
    if len({credentials[k] for k in ('grpc', 'http', 'wrapping_key', 'auth_session')}) != 4:
        raise RenderError('application credentials must differ')
    ca = read_input(root, 'ca.crt')
    labels = {'apexload.io/fault-run': run_id}
    def secret(name, data):
        return {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': name, 'namespace': 'apexload-fault', 'labels': labels},
                'immutable': True, 'type': 'Opaque', 'stringData': data}
    items = [secret('apexload-data-platform', {
        'APEXLOAD_GRPC_AUTH_TOKEN': credentials['grpc'], 'APEXLOAD_WRAPPING_MASTER_KEY': credentials['wrapping_key'],
        'APEXLOAD_FAULT_PROXY_SECRET': credentials['http'], 'APEXLOAD_AUTH_SESSION_SECRET': credentials['auth_session']})]
    for role in ('controller', 'generator'):
        cert, key = (read_input(root, role + suffix) for suffix in ('.crt', '.key'))
        validate_tls(cert, key, ca, 'apexload-' + role + '.apexload-fault.svc')
        items.append(secret('apexload-' + role + '-grpc-tls', {'tls.crt': cert, 'tls.key': key, 'ca.crt': ca}))
    patches = []
    def patch(kind, name, spec=None, **extra):
        obj = {'apiVersion': 'apps/v1' if kind in ('Deployment', 'StatefulSet') else 'v1',
               'kind': kind, 'metadata': {'name': name}, **extra}
        if spec is not None:
            obj['spec'] = spec
        patches.append({'patch': json.dumps(obj)})
    # There is no external ingress in this local profile; use explicit loopback forwards.
    patches.append({'patch': json.dumps({'apiVersion': 'networking.k8s.io/v1', 'kind': 'Ingress',
                                         'metadata': {'name': 'apexload-controller'}, '$patch': 'delete'})})
    patch('PersistentVolumeClaim', 'apexload-controller-import-assets', **{'$patch': 'delete'})
    patch('Namespace', 'apexload', **{'$patch': 'delete'})
    patches.append({'target': {'kind': 'RoleBinding'}, 'patch': json.dumps([
        {'op': 'add', 'path': '/subjects/0/namespace', 'value': 'apexload-fault'}])})
    patch('ConfigMap', 'apexload-data-platform', data={
        'APEXLOAD_FLEET_ID': run_id, 'APEXLOAD_GENERATORS': 'dns://apexload-generator.apexload-fault.svc:9090',
        'APEXLOAD_GRPC_TLS_SERVER_NAME': 'apexload-generator.apexload-fault.svc',
        'APEXLOAD_REDIS_KEY_PREFIX': 'apexload:fault:' + run_id,
        'APEXLOAD_ARCHIVE_PREFIX': 'fault/' + run_id,
        'APEXLOAD_ARCHIVE_ENDPOINT': 'https://apexload-s3.apexload-fault.svc:8333',
        'SSL_CERT_FILE': '/run/apexfault-data/ca.crt', 'APEXLOAD_GENERATOR_DISCOVERY_INTERVAL': '5s'})
    for role, image, kind in (('controller', controller_image, 'Deployment'), ('generator', generator_image, 'StatefulSet')):
        container = {'name': role, 'image': image, 'resources': {'$patch': 'replace',
                     'requests': {'cpu': '100m', 'memory': '128Mi', 'ephemeral-storage': '128Mi'},
                     'limits': {'cpu': '1', 'memory': '1Gi' if role == 'controller' else '512Mi', 'ephemeral-storage': '2Gi'}}}
        pod = {'affinity': {'nodeAffinity': {'requiredDuringSchedulingIgnoredDuringExecution': {'nodeSelectorTerms': [
            {'matchExpressions': [{'key': 'kubernetes.io/hostname', 'operator': 'In', 'values': [cluster + '-worker', cluster + '-worker2']}]}]}}},
            'containers': [container], 'volumes': [{'name': 'grpc-tls', 'secret': {'defaultMode': 0o440}},
                                                {'name': 'tmp', 'emptyDir': {'sizeLimit': '128Mi'}}]}
        spec = {'replicas': 2 if role == 'controller' else replicas,
                'template': {'metadata': {'labels': labels}, 'spec': pod}}
        if role == 'controller':
            # A 320-VUser/45s archive is about 190 MiB in the local trial.
            # Transfer spools must fit independently of materialization cache.
            container['resources']['requests']['ephemeral-storage'] = '512Mi'
            # Keep the data-volume limit distinct from the whole-pod budget:
            # cache downloads, unconfirmed sources and transfer spools coexist.
            container['resources']['limits']['ephemeral-storage'] = '5Gi'
            next(v for v in pod['volumes'] if v['name'] == 'tmp')['emptyDir']['sizeLimit'] = '1Gi'
            pod['affinity']['podAntiAffinity'] = {'$patch': 'replace', 'requiredDuringSchedulingIgnoredDuringExecution': [
                {'labelSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-controller'}}, 'topologyKey': 'kubernetes.io/hostname'}]}
            pod['volumes'].extend([{'name': 'import-assets', 'persistentVolumeClaim': None, 'emptyDir': {'sizeLimit': '128Mi'}},
                {'name': 'data', 'emptyDir': {'sizeLimit': '3Gi'}},
                {'name': 'data-ca', 'secret': {'secretName': 'apexload-fault-data-client', 'defaultMode': 0o440,
                                             'items': [{'key': 'ca.crt', 'path': 'ca.crt'}]}}])
            container['volumeMounts'] = [{'name': 'data-ca', 'mountPath': '/run/apexfault-data', 'readOnly': True}]
            container['env'] = [{'name': 'APEXLOAD_PUBLIC_URL', 'value': 'https://localhost'},
                                {'name': 'APEXLOAD_ALLOW_BOOTSTRAP_ADMIN', 'value': 'true'},
                                {'name': 'APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES', 'value': str(256 * 1024**2)},
                                {'name': 'APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_MAX_BYTES', 'value': str(256 * 1024**2)},
                                {'name': 'APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_TTL', 'value': '15m'},
                                {'name': 'APEXLOAD_BOOTSTRAP_SUBJECT', 'value': 'fault-' + run_id}]
            if source_retention_seconds:
                container['env'].append({'name': 'APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL',
                                         'value': str(source_retention_seconds) + 's'})
            for source, keys in (('apexload-fault-data-client', ('APEXLOAD_DATABASE_URL', 'APEXLOAD_REDIS_URL')),
                                 ('apexload-s3-client', ('AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_REGION'))):
                container['env'].extend({'name': key, 'valueFrom': {'secretKeyRef': {'name': source, 'key': key}}} for key in keys)
            container['startupProbe'] = {'httpGet': {'path': '/api/v1/system/health', 'port': 'http'},
                                         'timeoutSeconds': 3, 'periodSeconds': 2, 'failureThreshold': 60}
        else:
            container['env'] = [{'name': 'APEXLOAD_GENERATOR_MAX_VUSERS', 'value': str(pod_capacity)},
                                {'name': 'SSL_CERT_FILE', 'value': '/etc/apexload/grpc-tls/ca.crt'}]
            pod['topologySpreadConstraints'] = [{'maxSkew': 1, 'topologyKey': 'kubernetes.io/hostname',
                'whenUnsatisfiable': 'DoNotSchedule', 'labelSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-generator'}}}]
            spec['volumeClaimTemplates'] = [{'metadata': {'name': 'generator-state'}, 'spec': {
                'accessModes': ['ReadWriteOnce'], 'storageClassName': 'standard', 'resources': {'requests': {'storage': '1Gi'}}}}]
        patch(kind, 'apexload-' + role, spec)
    # Explicit same-namespace observer API access. The trusted-header secret is
    # still required; a pod label alone never grants application authentication.
    items.append({'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy',
        'metadata': {'name': 'apexload-fault-observer-api', 'namespace': 'apexload-fault', 'labels': labels},
        'spec': {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-controller'}}, 'policyTypes': ['Ingress'],
                 'ingress': [{'from': [{'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'apexload-fault-observer'}}}],
                              'ports': [{'protocol': 'TCP', 'port': 8080}]}]}})
    kustomization = {'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'namespace': 'apexload-fault',
        'resources': [str(Path(__file__).resolve().parents[2] / 'deployments/k8s/onprem-ha'), 'application-resources.json'],
        'labels': [{'pairs': labels}], 'patches': patches}
    return run_id, {'apiVersion': 'v1', 'kind': 'List', 'items': items}, kustomization


def render(inputs, images, output, replicas=4, pod_capacity=100, source_retention_seconds=0):
    # Finish validation before creating any output. Components share the exact
    # private inputs and installation identity, not earlier deployment snapshots.
    run_id, app, overlay = application(inputs, images['controller'], images['generator'], replicas, pod_capacity,
                                      source_retention_seconds)
    _, target = render_target.resources(inputs, images['target'])
    _, dependencies = render_dependencies.resources(inputs, images['postgres'], images['redis'])
    _, s3 = render_s3.resources(inputs, images['s3'])
    namespace = {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'apexload-fault', 'labels': {
        'apexload.io/fault-run': run_id, 'pod-security.kubernetes.io/enforce': 'restricted',
        'pod-security.kubernetes.io/enforce-version': 'v1.34'}}}
    base_items = [namespace, *target['items'], *dependencies['items'], *s3['items']]
    root = Path(output)
    if not root.is_absolute():
        raise RenderError('output must be a new absolute directory')
    root = root.resolve()
    overlay['resources'][0] = os.path.relpath(overlay['resources'][0], root / 'application')
    try:
        root.mkdir(mode=0o700, exist_ok=False)
        write_resources(root / 'dependencies', {'apiVersion': 'v1', 'kind': 'List', 'items': base_items}, 'dependencies.json')
        app_dir = root / 'application'
        app_dir.mkdir(mode=0o700)
        # Authentication is seeded explicitly before controllers start. The SQL
        # refuses an incompatible existing setting, never resets it on retry.
        settings = json.dumps({'auth': {'mode': 'trusted_headers', 'user_header': 'X-Auth-Request-User',
            'roles_header': 'X-APEXLoad-Roles', 'trusted_proxy_secret_env_var': 'APEXLOAD_FAULT_PROXY_SECRET'}})
        sql = ("BEGIN;\nCREATE TABLE IF NOT EXISTS app_settings(key TEXT PRIMARY KEY,value TEXT,updated_at TIMESTAMPTZ);\n"
               "INSERT INTO app_settings(key,value,updated_at) VALUES ('system_settings','" + settings + "',NOW()) ON CONFLICT(key) DO NOTHING;\n"
               "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM app_settings WHERE key='system_settings' AND value::jsonb->'auth' = '" +
               json.dumps(json.loads(settings)['auth']) + "'::jsonb) THEN RAISE EXCEPTION 'incompatible fault authentication settings'; END IF; END $$;\nCOMMIT;\n")
        for path, content in ((app_dir / 'application-resources.json', json.dumps(app, indent=2)),
                              (app_dir / 'kustomization.yaml', json.dumps(overlay, indent=2)),
                              (root / 'bootstrap-auth.sql', sql),
                              (root / 'kindnet-resources-patch.json', json.dumps({'spec': {'template': {'spec': {
                                  'containers': [{'name': 'kindnet-cni', 'resources': {
                                      'requests': {'cpu': '100m', 'memory': '50Mi'},
                                      'limits': {'cpu': '1', 'memory': '128Mi'}}}]}}}}, indent=2)),
                              (root / 'profile.json', json.dumps({'run_id': run_id, 'images': images, 'replicas': replicas,
                                   'pod_capacity': pod_capacity, 'profile': 'disposable-http-ha-s3',
                                   'source_retention_seconds': source_retention_seconds,
                                   'limitations': ['single-host', 'single-instance-data-services', 'local-path-generator-pvcs',
                                                    'ephemeral-import-assets', 'external-keda-prometheus-install-required']}, indent=2))):
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'w') as stream:
                stream.write(content + '\n')
    except OSError:
        raise RenderError('cannot create new private stack resources') from None
    return run_id


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True)
    parser.add_argument('--output', required=True)
    for role in ('controller', 'generator', 'target', 'postgres', 'redis', 's3'):
        parser.add_argument('--' + role + '-image', required=True)
    parser.add_argument('--replicas', type=int, default=4)
    parser.add_argument('--pod-capacity', type=int, default=100)
    parser.add_argument('--source-retention-seconds', type=int, default=0,
                        help='opt in only after all staging writers support publication-source fencing')
    args = parser.parse_args()
    try:
        run_id = render(args.inputs, {role: getattr(args, role + '_image') for role in
                        ('controller', 'generator', 'target', 'postgres', 'redis', 's3')},
                        args.output, args.replicas, args.pod_capacity, args.source_retention_seconds)
    except RenderError as error:
        print(str(error), file=sys.stderr)
        return 1
    print('Rendered private phased stack for run ' + run_id)
    return 0


if __name__ == '__main__':
    sys.exit(main())
