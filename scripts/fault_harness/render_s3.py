#!/usr/bin/env python3
"""Render a private, single-pod TLS S3 archive service for disposable kind trials."""
import argparse
import json
import re
import sys

from render_target import (RenderError, input_root, read_input, read_json, read_ownership,
                           validate_image, validate_tls, write_resources)


def resources(inputs, image, storage_class="standard"):
    root = input_root(inputs)
    run_id, cluster = read_ownership(root)
    validate_image(image)
    if (not isinstance(storage_class, str) or len(storage_class) > 253
            or not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*", storage_class)):
        raise RenderError("an explicit valid storage class is required")
    credentials = read_json(root, "credentials.json")
    for key in ("s3_access_key", "s3_secret_key"):
        if not isinstance(credentials.get(key), str) or not re.fullmatch(r"[A-Za-z0-9_-]{32,128}", credentials[key]):
            raise RenderError("invalid S3 credential")
    if credentials['s3_access_key'] == credentials['s3_secret_key']:
        raise RenderError("S3 credentials must differ")
    name, bucket = 'apexload-s3', 'apexload-archives'
    hostname = name + '.apexload-fault.svc'
    cert, key, ca = (read_input(root, filename) for filename in ('s3.crt', 's3.key', 'ca.crt'))
    validate_tls(cert, key, ca, hostname)
    validate_tls(cert, key, ca, bucket + '.' + hostname)
    labels = {'app.kubernetes.io/name': name, 'apexload.io/fault-run': run_id}
    def obj(kind, suffix='', api='v1'):
        return {'apiVersion': api, 'kind': kind, 'metadata': {
            'name': name + suffix, 'namespace': 'apexload-fault', 'labels': dict(labels)}}
    secret = obj('Secret', '-server')
    secret.update(type='Opaque', immutable=True, stringData={
        'tls.crt': cert, 'tls.key': key, 'ca.crt': ca,
        's3.json': json.dumps({'identities': [{'name': 'fault-archive', 'credentials': [{
            'accessKey': credentials['s3_access_key'], 'secretKey': credentials['s3_secret_key']}],
            'actions': [action + ':' + bucket for action in ('Admin', 'Read', 'Write', 'List', 'Tagging')]}]}),
        # Pin metadata to the PVC as well as the object volumes.
        'master.toml': '[master.volume_growth]\ncopy_1 = 1\ncopy_2 = 1\ncopy_3 = 1\ncopy_other = 1\n',
        'filer.toml': '[leveldb2]\nenabled = true\ndir = "/data/filer"\n',
        'security.toml': '[tls]\nmin_version = "TLS 1.2"\nmax_version = "TLS 1.3"\n[grpc]\nca = "/run/apexfault/ca.crt"\n'
                         '[grpc.s3]\ncert = "/run/apexfault/tls.crt"\nkey = "/run/apexfault/tls.key"\n'})
    client = obj('Secret', '-client')
    client.update(type='Opaque', immutable=True, stringData={
        'ca.crt': ca, 'AWS_ACCESS_KEY_ID': credentials['s3_access_key'],
        'AWS_SECRET_ACCESS_KEY': credentials['s3_secret_key'], 'AWS_REGION': 'us-east-1',
        'APEXLOAD_ARCHIVE_BACKEND': 's3', 'APEXLOAD_ARCHIVE_BUCKET': bucket,
        'APEXLOAD_ARCHIVE_ENDPOINT': 'https://' + hostname + ':8333',
        'APEXLOAD_ARCHIVE_REGION': 'us-east-1', 'APEXLOAD_ARCHIVE_PREFIX': 'fault/' + run_id})
    claim = obj('PersistentVolumeClaim', '-data')
    claim['spec'] = {'accessModes': ['ReadWriteOnce'], 'storageClassName': storage_class,
                     'resources': {'requests': {'storage': '2Gi'}}}
    deployment = obj('Deployment', api='apps/v1')
    deployment['spec'] = {'replicas': 1, 'strategy': {'type': 'Recreate'}, 'selector': {'matchLabels': labels},
        'template': {'metadata': {'labels': labels}, 'spec': {
            # A stable pod hostname under the headless service provides the
            # virtual-hosted bucket DNS record used by the application's S3 adapter.
            'hostname': bucket, 'subdomain': name, 'automountServiceAccountToken': False,
            'nodeSelector': {'kubernetes.io/hostname': cluster + '-control-plane'},
            'tolerations': [{'key': 'node-role.kubernetes.io/control-plane', 'operator': 'Exists', 'effect': 'NoSchedule'}],
            'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532,
                                'fsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}},
            'terminationGracePeriodSeconds': 60,
            'containers': [{'name': 's3', 'image': image, 'imagePullPolicy': 'IfNotPresent',
                'command': ['weed'], 'args': ['-logtostderr=true', '-config_dir=/etc/seaweedfs', 'server',
                    '-dir=/data', '-ip=127.0.0.1', '-ip.bind=127.0.0.1', '-volume.max=4', '-master.volumeSizeLimitMB=256',
                    '-s3', '-s3.ip.bind=0.0.0.0', '-s3.port=8333', '-s3.port.https=0', '-s3.port.iceberg=0',
                    '-s3.config=/run/apexfault/s3.json', '-s3.cert.file=/run/apexfault/tls.crt',
                    '-s3.key.file=/run/apexfault/tls.key', '-s3.domainName=' + hostname,
                    '-s3.concurrentUploadLimitMB=32'],
                'env': [{'name': 'HOME', 'value': '/tmp'}],
                'ports': [{'name': 'https', 'containerPort': 8333}],
                'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True,
                                    'capabilities': {'drop': ['ALL']}},
                'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'}, 'limits': {'cpu': '1', 'memory': '512Mi'}},
                'volumeMounts': [{'name': 'data', 'mountPath': '/data'}, {'name': 'tmp', 'mountPath': '/tmp'},
                                 {'name': 'server', 'mountPath': '/run/apexfault', 'readOnly': True},
                                 {'name': 'config', 'mountPath': '/etc/seaweedfs', 'readOnly': True}],
                'startupProbe': {'tcpSocket': {'port': 'https'}, 'periodSeconds': 2, 'failureThreshold': 60},
                # TCP probes only establish listener/process readiness. The signed
                # object put/get and retention check is a separate qualification.
                'readinessProbe': {'tcpSocket': {'port': 'https'}, 'periodSeconds': 5, 'timeoutSeconds': 2},
                'livenessProbe': {'tcpSocket': {'port': 'https'}, 'periodSeconds': 10, 'timeoutSeconds': 2, 'failureThreshold': 6}}],
            'volumes': [{'name': 'data', 'persistentVolumeClaim': {'claimName': name + '-data'}},
                        {'name': 'tmp', 'emptyDir': {'sizeLimit': '64Mi'}},
                        {'name': 'server', 'secret': {'secretName': name + '-server', 'defaultMode': 0o440,
                                                    'items': [{'key': key, 'path': key} for key in ('tls.crt', 'tls.key', 'ca.crt', 's3.json')]}},
                        {'name': 'config', 'secret': {'secretName': name + '-server', 'defaultMode': 0o440,
                                                    'items': [{'key': key, 'path': key} for key in ('filer.toml', 'security.toml', 'master.toml')]}}]}}}
    service = obj('Service')
    service['spec'] = {'clusterIP': 'None', 'selector': labels, 'ports': [{'name': 'https', 'port': 8333, 'targetPort': 'https'}]}
    policy = obj('NetworkPolicy', api='networking.k8s.io/v1')
    policy['spec'] = {'podSelector': {'matchLabels': labels}, 'policyTypes': ['Ingress', 'Egress'], 'egress': [],
                      'ingress': [{'from': [{'podSelector': {'matchLabels': {'app.kubernetes.io/name': peer}}}
                                             for peer in ('apexload-controller', 'apexload-fault-observer')],
                                   'ports': [{'protocol': 'TCP', 'port': 8333}]}]}
    return run_id, {'apiVersion': 'v1', 'kind': 'List', 'items': [secret, client, claim, deployment, service, policy]}


def render(inputs, image, output, storage_class='standard'):
    run_id, manifest = resources(inputs, image, storage_class)
    write_resources(output, manifest, 's3-resources.json')
    return run_id


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True)
    parser.add_argument('--image', required=True, help='SeaweedFS 4.17 repository manifest digest')
    parser.add_argument('--storage-class', default='standard')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    try:
        run_id = render(args.inputs, args.image, args.output, args.storage_class)
    except RenderError as error:
        print(str(error), file=sys.stderr)
        return 1
    print('Rendered private S3 resources for run ' + run_id)
    return 0


if __name__ == '__main__':
    sys.exit(main())
