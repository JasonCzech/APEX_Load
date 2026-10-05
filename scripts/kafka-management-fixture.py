#!/usr/bin/env python3
"""Bounded disposable Kafka TLS/SASL qualification; no production credentials."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import shutil
import subprocess
import tempfile
import tarfile
import stat
import time

INDEX = 'sha256:05b4616e0702ef2729327705d54ad6b50ea70b271c4b730fabd2320789fb7b02'
PLATFORMS = {'arm64': 'sha256:ddc235acd57d0fec5b21eedd46dcaf4e89a7569ff1c9890c5597c6d9561c59b0', 'amd64': 'sha256:f5f8605794dce59d1d566f63667682771c2fec9e96088b0d6a5ef0f8476dd4b2'}
IMAGE = 'apache/kafka@' + INDEX
GIB = 1024 ** 3


def protected_container_bundle(scratch):
    """Copy only required private inputs with explicit container ownership."""
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w') as archive:
        for name in ('password', 'ca.pem', 'broker.pem', 'server.properties', 'start.sh', 'admin.properties'):
            descriptor = os.open(scratch / name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(descriptor, 'rb') as source:
                info = os.fstat(source.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 1 << 20:
                    raise RuntimeError('fixture input is not a bounded protected regular file')
                data = source.read((1 << 20) + 1)
                if len(data) != info.st_size:
                    raise RuntimeError('fixture input changed during protected copy')
            member = tarfile.TarInfo(name)
            member.uid = member.gid = 0
            member.uname = member.gname = 'root'
            member.mode = 0o600
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
    return output.getvalue()


def broker_error_summary(output, password, private_key):
    """Bounded diagnostic lines for this fixture, excluding private inputs."""
    key_lines = [line for line in private_key.splitlines() if line and not line.startswith('-----')]
    result = []
    for line in output.splitlines():
        if not any(word in line for word in ('ERROR', 'Exception', 'Caused by', 'Invalid', 'Error', 'failed', 'not found', 'denied')):
            continue
        if any(secret in line for secret in key_lines):
            result.append('[fixture private key material omitted]')
        else:
            result.append(line.replace(password, '[REDACTED]')[:512])
        if len(result) == 16:
            break
    return result


def wait_for_broker_readiness(call, label, setup_port):
    """Retry ordinary not-ready results only after each owned probe exits.

    A numeric TCP probe avoids competing cold admin JVMs during broker boot.
    The admin profile limits provider waiting; a host command timeout remains
    terminal so no unproven remote exec is followed by another probe.
    """
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        state = call(['docker', 'inspect', '--format', '{{.State.Running}}', label], timeout=min(5, remaining))
        if state.stdout.strip() != 'true':
            raise RuntimeError('broker exited during bounded readiness')
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        connected = False
        try:
            with socket.create_connection(('127.0.0.1', setup_port), timeout=min(1, remaining)):
                connected = True
        except OSError:
            pass
        if connected:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            response = call(['docker', 'exec', label, '/opt/kafka/bin/kafka-topics.sh',
                             '--bootstrap-server', 'localhost:9094',
                             '--command-config', '/fixture/admin.properties', '--list'],
                            check=False, timeout=min(20, remaining))
            if response.returncode == 0:
                return
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(0.5, remaining))
    raise RuntimeError('bounded broker readiness failed')


def reap_fixture_test_group(child, leader_exited):
    # Darwin rejects signals to a group containing only its unreaped zombie.
    # Accept that error only after WNOWAIT proved our leader exited, then reap
    # and require group absence. Never retry a mutating signal after reaping.
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    except PermissionError:
        if not leader_exited:
            raise RuntimeError('owned active test group could not be stopped') from None
    child.wait(timeout=30)
    group_deadline = time.monotonic()+30
    while True:
        try:
            os.killpg(child.pid, 0)
        except ProcessLookupError:
            return True
        except PermissionError:
            raise RuntimeError('owned test group absence could not be proven') from None
        if time.monotonic() >= group_deadline:
            raise RuntimeError('test group shutdown could not be proven')
        time.sleep(0.1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--platform', choices=tuple(PLATFORMS), default='arm64' if os.uname().machine == 'arm64' else 'amd64')
    parser.add_argument('--port', type=int, default=19392)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    if not 1024 <= args.port <= 65533:
        parser.error('port must leave room for the two adjacent loopback fixture ports')
    if os.path.lexists(args.report) or os.path.lexists(args.report.with_suffix('.tests.txt')):
        parser.error('report and test log destinations must be unused')
    args.report.parent.mkdir(parents=True, exist_ok=True)
    scratch = Path(tempfile.mkdtemp(prefix='apexload-kafka-management-'))
    scratch.chmod(0o700)
    label = 'apexload-kafka-management-' + secrets.token_hex(6)
    volumes = [label + '-config', label + '-data']
    started = time.monotonic()
    deadline = started + 900
    report = {'version': 1, 'kafka_version': '3.9.2', 'image_index': INDEX, 'platform': 'linux/' + args.platform, 'platform_manifest': PLATFORMS[args.platform], 'profile': 'single-kraft-broker', 'memory_bytes': 1536 * 1024**2, 'cpus': 1, 'data_guard_bytes': 2 * GIB, 'created_resources': [], 'status': 'failed'}

    def headroom():
        free = shutil.disk_usage(scratch).free
        if free < 3 * GIB:
            raise RuntimeError('fixture disk admission below 3 GiB free reserve')
        if time.monotonic() >= deadline:
            raise RuntimeError('fixture exceeded 15-minute wall deadline')
        return free

    def call(argv, *, data=None, check=True, timeout=60):
        headroom()
        remaining = max(1, deadline - time.monotonic())
        try:
            result = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=min(timeout, remaining), text=not isinstance(data, bytes))
        except subprocess.TimeoutExpired:
            report['failed_operation'] = next((Path(value).name for value in argv if value.startswith('/opt/kafka/bin/')), ' '.join(argv[:2]))
            raise RuntimeError('fixture command deadline exceeded') from None
        if check and result.returncode:
            report['failed_operation'] = next((Path(value).name for value in argv if value.startswith('/opt/kafka/bin/')), ' '.join(argv[:2]))
            if argv[:2] == ['docker', 'exec'] and (scratch/'broker.key').is_file():
                report['command_diagnostics'] = broker_error_summary(result.stdout + result.stderr, password, (scratch/'broker.key').read_text())
            # Classify local container admission without retaining raw daemon or
            # provider output, which may include private configuration.
            if argv[:2] == ['docker', 'start']:
                detail = result.stderr.lower()
                reasons = [('no such file or directory', 'executable_or_path_missing'),
                           ('address already in use', 'loopback_port_in_use'),
                           ('port is already allocated', 'loopback_port_in_use'),
                           ('cannot allocate memory', 'memory_admission'),
                           ('no space left', 'disk_admission'),
                           ('exec format error', 'platform_executable'),
                           ('permission denied', 'execution_permission'),
                           ('failed to initialize logging driver', 'logging_driver_admission')]
                report['start_failure_code'] = next((code for text, code in reasons if text in detail), 'unclassified_container_admission')
            raise RuntimeError('fixture command failed: ' + argv[0] + ' status ' + str(result.returncode))
        return result

    def write(name, text):
        path = scratch / name
        path.write_text(text)
        path.chmod(0o600)
        return path

    try:
        report['free_before_bytes'] = headroom()
        raw = call(['docker', 'buildx', 'imagetools', 'inspect', IMAGE, '--raw']).stdout.encode()
        if 'sha256:' + hashlib.sha256(raw).hexdigest() != INDEX:
            raise RuntimeError('registry index does not match pinned digest')
        index = json.loads(raw)
        actual = [m['digest'] for m in index['manifests'] if m.get('platform', {}).get('os') == 'linux' and m.get('platform', {}).get('architecture') == args.platform]
        if actual != [PLATFORMS[args.platform]]:
            raise RuntimeError('registry platform differs from approved manifest')
        call(['docker', 'pull', '--platform', 'linux/' + args.platform, IMAGE], timeout=180)
        password = secrets.token_urlsafe(24)
        write('password', password + '\n')
        write('extensions.cnf', 'subjectAltName=DNS:localhost,IP:127.0.0.1\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
        write('ca.cnf', '[req]\ndistinguished_name=dn\nx509_extensions=ca\nprompt=no\n[dn]\nCN=APEX disposable Kafka CA\n[ca]\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\n')
        call(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(scratch/'ca.key'), '-out', str(scratch/'ca.pem'), '-days', '2', '-config', str(scratch/'ca.cnf'), '-extensions', 'ca', '-subj', '/CN=APEX disposable Kafka CA'])
        call(['openssl', 'req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(scratch/'broker.key'), '-out', str(scratch/'broker.csr'), '-subj', '/CN=localhost'])
        call(['openssl', 'x509', '-req', '-in', str(scratch/'broker.csr'), '-CA', str(scratch/'ca.pem'), '-CAkey', str(scratch/'ca.key'), '-CAcreateserial', '-out', str(scratch/'broker.crt'), '-days', '2', '-extfile', str(scratch/'extensions.cnf')])
        write('broker.pem', (scratch/'broker.key').read_text() + (scratch/'broker.crt').read_text())
        for path in scratch.iterdir():
            path.chmod(0o600)
        cluster_id = secrets.token_urlsafe(16)
        properties = f"""process.roles=broker,controller
node.id=1
controller.quorum.voters=1@localhost:9093
listeners=SASL_SSL://0.0.0.0:9092,SETUP://0.0.0.0:9094,CONTROLLER://0.0.0.0:9093
advertised.listeners=SASL_SSL://localhost:{args.port},SETUP://localhost:9094
listener.security.protocol.map=SASL_SSL:SASL_SSL,SETUP:PLAINTEXT,CONTROLLER:PLAINTEXT
controller.listener.names=CONTROLLER
inter.broker.listener.name=SETUP
log.dirs=/data/kafka
num.partitions=1
auto.create.topics.enable=false
offsets.topic.replication.factor=1
offsets.topic.num.partitions=1
transaction.state.log.num.partitions=1
transaction.state.log.replication.factor=1
transaction.state.log.min.isr=1
group.initial.rebalance.delay.ms=0
log.retention.bytes=536870912
log.segment.bytes=16777216
log.retention.hours=1
ssl.keystore.type=PEM
ssl.keystore.location=/fixture/broker.pem
ssl.truststore.type=PEM
ssl.truststore.location=/fixture/ca.pem
ssl.client.auth=none
sasl.enabled.mechanisms=PLAIN,SCRAM-SHA-256,SCRAM-SHA-512
listener.name.sasl_ssl.plain.sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required user_reader="{password}";
listener.name.sasl_ssl.scram-sha-256.sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required;
listener.name.sasl_ssl.scram-sha-512.sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required;
authorizer.class.name=org.apache.kafka.metadata.authorizer.StandardAuthorizer
super.users=User:ANONYMOUS
allow.everyone.if.no.acl.found=false
"""
        write('server.properties', properties)
        write('admin.properties', 'default.api.timeout.ms=3000\nrequest.timeout.ms=2000\n')
        write('start.sh', f'#!/usr/bin/env bash\nset -euo pipefail\n/opt/kafka/bin/kafka-storage.sh format --ignore-formatted --cluster-id {cluster_id} --config /fixture/server.properties >/dev/null\nexec /opt/kafka/bin/kafka-server-start.sh /fixture/server.properties\n')
        call(['docker', 'network', 'create', label])
        report['created_resources'].append(label)
        for volume in volumes:
            call(['docker', 'volume', 'create', volume])
            report['created_resources'].append(volume)
        call(['docker', 'create', '--name', label, '--network', label, '--platform', 'linux/' + args.platform, '--user', '0:0', '--memory', '1536m', '--cpus', '1', '--pids-limit', '256', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--log-driver', 'local', '--log-opt', 'max-size=8m', '--log-opt', 'max-file=2', '--mount', 'type=volume,source='+volumes[0]+',target=/fixture', '--mount', 'type=volume,source='+volumes[1]+',target=/data', '--publish', f'127.0.0.1:{args.port+1}:9092', '--publish', f'127.0.0.1:{args.port+2}:9094', '--env', 'KAFKA_HEAP_OPTS=-Xms256m -Xmx512m', '--env', 'LOG_DIR=/data/logs', '--env', 'KAFKA_GC_LOG_OPTS=-Xlog:gc*:file=/data/kafka-gc.log:time,tags:filecount=2,filesize=8M', '--entrypoint', '/bin/bash', IMAGE, '/fixture/start.sh'])
        report['created_resources'].append(label + ':container')
        call(['docker', 'cp', '-', label + ':/fixture'], data=protected_container_bundle(scratch))
        call(['docker', 'start', label])
        wait_for_broker_readiness(call, label, args.port + 2)
        # Setup runs on a distinct listener and never uses the collector identity.
        setup = ['docker', 'exec', label]
        call(setup + ['/opt/kafka/bin/kafka-topics.sh', '--bootstrap-server', 'localhost:9094', '--create', '--topic', 'orders', '--partitions', '1', '--replication-factor', '1'])
        # Independent fixture mutation waits for each acknowledgement. Fixed
        # per-record limits shorten provider failures; the unchanged host command
        # bound remains the aggregate guard, including producer close.
        call(['docker', 'exec', '-i', label, '/opt/kafka/bin/kafka-console-producer.sh', '--bootstrap-server', 'localhost:9094', '--topic', 'orders', '--sync', '--producer-property', 'max.block.ms=10000', '--producer-property', 'request.timeout.ms=3000', '--producer-property', 'delivery.timeout.ms=10000', '--producer-property', 'linger.ms=0', '--producer-property', 'retries=3', '--producer-property', 'acks=all'], data='one\ntwo\nthree\nfour\n')
        call(setup + ['/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9094', '--group', 'workers', '--topic', 'orders:0', '--reset-offsets', '--to-offset', '1', '--execute'])
        for resource in ('--topic', '--group'):
            call(setup + ['/opt/kafka/bin/kafka-acls.sh', '--bootstrap-server', 'localhost:9094', '--add', '--allow-principal', 'User:reader', '--operation', 'Describe', resource, 'orders' if resource == '--topic' else 'workers'])
        # Secrets are read inside the private container; never rendered in output.
        for mechanism in ('SCRAM-SHA-256', 'SCRAM-SHA-512'):
            scram = 'read -r fixture_password </fixture/password; exec /opt/kafka/bin/kafka-configs.sh --bootstrap-server localhost:9094 --alter --entity-type users --entity-name reader --add-config "' + mechanism + '=[iterations=8192,password=${fixture_password}]"'
            call(setup + ['/bin/bash', '-c', scram])
        def data_usage(timeout=5):
            usage = call(setup + ['du', '-sk', '/data'], timeout=timeout).stdout.split()[0]
            total = int(usage) * 1024
            report['peak_data_bytes'] = max(report.get('peak_data_bytes', 0), total)
            if total > 2 * GIB:
                raise RuntimeError('fixture data exceeded monitored 2 GiB guard')
            return total
        data_usage()
        fixture = {'endpoint': f'tls://localhost:{args.port}', 'dial_address': f'127.0.0.1:{args.port}', 'broker_address': f'127.0.0.1:{args.port+1}', 'setup_address': f'127.0.0.1:{args.port+2}', 'ca_file': str(scratch/'ca.pem'), 'proxy_cert_file': str(scratch/'broker.crt'), 'proxy_key_file': str(scratch/'broker.key'), 'cluster_id': cluster_id, 'username': 'reader', 'password': password, 'mechanisms': ['plain', 'scram_sha_256', 'scram_sha_512'], 'topic': 'orders', 'group': 'workers', 'denied_group': 'denied-workers', 'expected_committed_next': '1', 'expected_high_watermark_next': '4', 'expected_last_stable_next': '4'}
        fixture_path = write('fixture.json', json.dumps(fixture))
        env = dict(os.environ, APEXLOAD_KAFKA_MANAGEMENT_FIXTURE=str(fixture_path))
        # The fixture owns the complete test process group, including the Go
        # test child. Deadline/resource failures kill and reap that group before
        # provider teardown; killing only the go driver would leak its child.
        with args.report.with_suffix('.tests.txt').open('w') as test_log:
            child = subprocess.Popen(['go', 'test', '-v', '-p', '1', './pkg/collectoragent', '-run', '^TestKafkaManagementActualBroker', '-count=1', '-timeout=8m'], env=env, stdout=test_log, stderr=subprocess.STDOUT, start_new_session=True)
            test_deadline = min(deadline, time.monotonic()+480)
            leader_exited = False
            try:
                while True:
                    # WNOWAIT retains the exact owned session leader until
                    # its group has been signaled; its ID cannot be reused.
                    status = os.waitid(os.P_PID, child.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                    if status is not None and status.si_pid == child.pid:
                        leader_exited = True
                        report['test_exit'] = status.si_status if status.si_code == os.CLD_EXITED else -status.si_status
                        break
                    remaining_test = test_deadline - time.monotonic()
                    if remaining_test <= 0:
                        raise RuntimeError('genuine Kafka test exceeded wall deadline')
                    headroom()
                    data_usage(timeout=min(5, remaining_test))
                    time.sleep(min(5, max(0, test_deadline-time.monotonic())))
            finally:
                # The unreaped owned leader prevents PGID reuse during this
                # signal. Reap after it, then prove group absence without issuing
                # any later signal to a potentially reused numerical identity.
                report['test_group_reaped'] = reap_fixture_test_group(child, leader_exited)
        if report['test_exit']:
            raise RuntimeError('genuine Kafka test failed; see sanitized test log')
        test_output = args.report.with_suffix('.tests.txt').read_text()
        required = ('--- PASS: TestKafkaManagementActualBroker ',
                    '--- PASS: TestKafkaManagementActualBroker/plain ',
                    '--- PASS: TestKafkaManagementActualBroker/scram_sha_256 ',
                    '--- PASS: TestKafkaManagementActualBroker/scram_sha_512 ',
                    '--- PASS: TestKafkaManagementActualBroker/open_transaction_read_boundaries ')
        if any(case not in test_output for case in required):
            raise RuntimeError('genuine Kafka test did not execute every required case')
        report['status'] = 'passed'
    except Exception as error:
        report['failure'] = str(error)[:512]
        if label + ':container' in report['created_resources'] and (scratch/'broker.key').is_file() and (scratch/'password').is_file():
            try:
                logs = subprocess.run(['docker', 'logs', '--tail', '60', label], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=5)
                report['broker_diagnostics'] = broker_error_summary(logs.stdout, (scratch/'password').read_text().strip(), (scratch/'broker.key').read_text())
            except (OSError, subprocess.TimeoutExpired):
                report['broker_diagnostic_status'] = 'unavailable'
        raise
    finally:
        report['duration_seconds'] = round(time.monotonic()-started, 3)
        report['cleanup'] = []
        # Cleanup attempts are independent: one unavailable Docker operation
        # cannot skip remaining owned resources, protected scratch or the report.
        for argv in (['docker', 'rm', '--force', label], *[['docker', 'volume', 'rm', volume] for volume in volumes], ['docker', 'network', 'rm', label]):
            entry = {'resource': argv[-1]}
            try:
                result = subprocess.run(argv, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
                entry['exit'] = result.returncode
            except subprocess.TimeoutExpired:
                entry['error'] = 'cleanup timeout'
            except OSError:
                entry['error'] = 'cleanup unavailable'
            report['cleanup'].append(entry)
        report['cleanup_verified'] = True
        inventories = [(['docker', 'ps', '-a', '--format', '{{.Names}}'], {label}), (['docker', 'volume', 'ls', '--format', '{{.Name}}'], set(volumes)), (['docker', 'network', 'ls', '--format', '{{.Name}}'], {label})]
        for argv, owned in inventories:
            try:
                result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=30, text=True)
                if result.returncode or owned.intersection(result.stdout.splitlines()):
                    report['cleanup_verified'] = False
            except (OSError, subprocess.TimeoutExpired):
                report['cleanup_verified'] = False
        try:
            shutil.rmtree(scratch)
        except OSError:
            report['scratch_removed'] = False
        else:
            report['scratch_removed'] = not scratch.exists()
        if not report['cleanup_verified'] or not report['scratch_removed']:
            report['status'] = 'failed'
            report['cleanup_failure'] = 'owned fixture resource or protected scratch removal could not be proven'
        report['free_after_bytes'] = shutil.disk_usage(args.report.parent).free
        args.report.write_text(json.dumps(report, indent=2)+'\n')
        print('Kafka management fixture:', report['status'], '; report:', args.report)
        if report.get('cleanup_failure'):
            raise RuntimeError(report['cleanup_failure'])


if __name__ == '__main__':
    main()
