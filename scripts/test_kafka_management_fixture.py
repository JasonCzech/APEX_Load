"""Regression checks for bounded fixture cleanup; no Docker resources are used."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tarfile
import unittest
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('kafka_management_fixture', Path(__file__).with_name('kafka-management-fixture.py'))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class FixtureCleanupTest(unittest.TestCase):
    def test_final_output_burst_is_bounded_before_pass_marker_decoding(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-final-log-test-') as directory:
            path = Path(directory)/'test.log'
            path.write_text('--- PASS: TestKafkaOwnedSeekActualBroker (0.01s)\n')
            self.assertEqual(fixture.bounded_fixture_test_output(path), path.read_text())
            with path.open('wb') as output:
                output.write(b'x' * (16 * 1024**2 + 1))
            with self.assertRaisesRegex(RuntimeError, '16 MiB'):
                fixture.bounded_fixture_test_output(path)
            path.write_bytes(b'\xff')
            with self.assertRaises(UnicodeDecodeError):
                fixture.bounded_fixture_test_output(path)

    def test_named_wrappers_refuse_switching_qualification_suites(self):
        for name, other in (('test-kafka-management.sh', 'owned-seek'), ('test-kafka-owned-seek.sh', 'management'), ('test-kafka-transactions.sh', 'owned-seek')):
            with self.subTest(wrapper=name):
                command = ['bash', str(Path(__file__).with_name(name)), '--suite', other]
                result = subprocess.run(command, capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 2)
                self.assertIn('only qualifies', result.stderr)

    def test_abbreviated_suite_flags_are_rejected_before_provider_work(self):
        for override in (['--su', 'management'], ['--sui=management']):
            with self.subTest(override=override), tempfile.TemporaryDirectory(prefix='apexload-kafka-suite-test-') as directory:
                with patch.object(sys, 'argv', ['fixture', '--suite', 'owned-seek', '--report', str(Path(directory)/'report.json'), *override]), patch.object(fixture.tempfile, 'mkdtemp') as scratch, patch.object(fixture.subprocess, 'run') as provider, contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit) as raised:
                        fixture.main()
                self.assertEqual(raised.exception.code, 2)
                scratch.assert_not_called()
                provider.assert_not_called()

    def test_unreadable_key_withholds_owned_private_log_without_raising(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-key-test-') as directory:
            scratch = Path(directory)
            (scratch/'broker.key').write_text('private-key')
            log = scratch/'report.tests.txt'
            log.write_text('FAIL private-key executor-private oracle-private')
            report = {'status': 'passed', 'test_log_created': True}
            original = Path.read_text
            def read(path, *args, **kwargs):
                if path.name == 'broker.key': raise PermissionError('test key read refused')
                return original(path, *args, **kwargs)
            with patch.object(Path, 'read_text', read):
                fixture.finalize_fixture_evidence(report, scratch, log, ['executor-private', 'oracle-private'])
            self.assertEqual(report['status'], 'failed')
            self.assertIn('could not be checked', report['evidence_failure'])
            self.assertTrue(report['test_log_withheld_or_redacted'])
            self.assertFalse(log.exists())
            # Never remove an unowned log if failure precedes our log creation.
            log.write_text('original unowned content')
            report = {'status': 'failed'}
            with patch.object(Path, 'read_text', read):
                fixture.finalize_fixture_evidence(report, scratch, log, ['private'])
            self.assertEqual(log.read_text(), 'original unowned content')

    def test_failed_test_logs_are_redacted_and_oversized_logs_are_withheld(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-log-test-') as directory:
            path = Path(directory)/'test.log'
            self.assertFalse(fixture.sanitize_fixture_test_log(path, ['executor-secret', 'oracle-secret']))
            original = b'FAIL request executor-secret oracle-secret key-material\n'
            path.write_bytes(original)
            self.assertTrue(fixture.sanitize_fixture_test_log(path, ['executor-secret', 'oracle-secret', 'key-material']))
            self.assertEqual(path.read_bytes(), b'FAIL request [REDACTED] [REDACTED] [REDACTED]\n')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertFalse(fixture.sanitize_fixture_test_log(path, ['executor-secret', 'oracle-secret']))
            self.assertEqual(path.read_bytes(), b'FAIL request [REDACTED] [REDACTED] [REDACTED]\n')
            path.write_bytes(b'x' * (16 * 1024**2 + 1))
            self.assertTrue(fixture.sanitize_fixture_test_log(path, []))
            self.assertEqual(path.read_text(), '[fixture test log exceeded its 16 MiB evidence limit]\n')

    def test_qualification_profiles_cannot_substitute_management_for_execution(self):
        management = fixture.suite_definition('management')
        execution = fixture.suite_definition('owned-seek')
        self.assertNotEqual(management['environment'], execution['environment'])
        self.assertIn('./pkg/collectoragent', management['command'])
        self.assertNotIn('-tags=kafka_real,kafka_seek_integration', management['command'])
        self.assertIn('./pkg/protocol/kafka', execution['command'])
        self.assertIn('-tags=kafka_real,kafka_seek_integration', execution['command'])
        self.assertIn('^TestKafkaOwnedSeekActualBroker$', execution['command'])
        required = execution['required']
        # Skipped or management-only output cannot certify execution.
        for output in ('--- SKIP: TestKafkaOwnedSeekActualBroker (0.00s)',
                       '\n'.join(management['required']),
                       '\n'.join(required[:-1])):
            self.assertTrue(any(case not in output for case in required))
        with self.assertRaisesRegex(ValueError, 'unsupported'):
            fixture.suite_definition('unsupported-eos-profile')

    def test_execution_failure_diagnostics_redact_both_independent_identities(self):
        lines = fixture.broker_error_summary('Error executor=execution-private oracle=oracle-private\nException harmless',
                                             'execution-private', '', 'oracle-private')
        self.assertNotIn('execution-private', json.dumps(lines))
        self.assertNotIn('oracle-private', json.dumps(lines))
        self.assertEqual(lines[0], 'Error executor=[REDACTED] oracle=[REDACTED]')
        # An unset optional identity must not replace every character.
        self.assertEqual(fixture.broker_error_summary('Error harmless', 'private', '', ''), ['Error harmless'])

    def test_existing_evidence_is_rejected_before_scratch_or_provider_work(self):
        for existing in ('report', 'tests', 'dangling-report', 'dangling-tests'):
            with self.subTest(existing=existing), tempfile.TemporaryDirectory(prefix='apexload-kafka-report-test-') as directory:
                report = Path(directory)/'qualification.json'
                destination = report.with_suffix('.tests.txt') if existing.endswith('tests') else report
                if existing.startswith('dangling-'):
                    destination.symlink_to(Path(directory)/'absent')
                else:
                    destination.write_bytes(b'original immutable evidence')
                with patch.object(sys, 'argv', ['fixture', '--report', str(report)]), patch.object(fixture.tempfile, 'mkdtemp') as scratch, patch.object(fixture.subprocess, 'run') as provider, contextlib.redirect_stderr(io.StringIO()) as errors:
                    with self.assertRaises(SystemExit) as raised:
                        fixture.main()
                self.assertEqual(raised.exception.code, 2)
                self.assertIn('destinations must be unused', errors.getvalue())
                scratch.assert_not_called()
                provider.assert_not_called()
                if existing.startswith('dangling-'):
                    self.assertTrue(destination.is_symlink())
                else:
                    self.assertEqual(destination.read_bytes(), b'original immutable evidence')

    def test_readiness_waits_for_listener_and_retries_only_exited_admin_probes(self):
        calls = []
        connection = Mock()
        connection.__enter__ = Mock(return_value=connection)
        connection.__exit__ = Mock(return_value=False)
        def command(argv, **kwargs):
            calls.append((argv, kwargs))
            if argv[:2] == ['docker', 'inspect']:
                return subprocess.CompletedProcess(argv, 0, 'true\n', '')
            return subprocess.CompletedProcess(argv, 0 if len(calls) >= 5 else 1, '', '')
        with patch.object(fixture.socket, 'create_connection', side_effect=[ConnectionRefusedError(), connection, connection]) as connect, patch.object(fixture.time, 'sleep'):
            fixture.wait_for_broker_readiness(command, 'owned-broker', 19494)
        self.assertEqual(len(calls), 5)
        self.assertEqual(connect.call_count, 3)
        for invocation in connect.call_args_list:
            self.assertEqual(invocation.args, (('127.0.0.1', 19494),))
            self.assertLessEqual(invocation.kwargs['timeout'], 1)
        self.assertEqual(connection.__exit__.call_count, 2)
        for argv, kwargs in calls:
            if argv[:2] == ['docker', 'exec']:
                self.assertIn('/fixture/admin.properties', argv)
                self.assertFalse(kwargs['check'])
                self.assertLessEqual(kwargs['timeout'], 20)

    def test_readiness_inspect_cannot_admit_connection_after_deadline(self):
        command = Mock(return_value=subprocess.CompletedProcess([], 0, 'true', ''))
        with patch.object(fixture.time, 'monotonic', side_effect=[0, 0, 0, 91]), patch.object(fixture.socket, 'create_connection') as connect:
            with self.assertRaisesRegex(RuntimeError, 'bounded broker readiness failed'):
                fixture.wait_for_broker_readiness(command, 'owned-broker', 19494)
        command.assert_called_once()
        connect.assert_not_called()

    def test_readiness_command_timeout_is_terminal_without_another_remote_probe(self):
        connection = Mock()
        connection.__enter__ = Mock(return_value=connection)
        connection.__exit__ = Mock(return_value=False)
        command = Mock(side_effect=[subprocess.CompletedProcess([], 0, 'true', ''), RuntimeError('fixture command deadline exceeded')])
        with patch.object(fixture.socket, 'create_connection', return_value=connection), patch.object(fixture.time, 'sleep') as sleep:
            with self.assertRaisesRegex(RuntimeError, 'command deadline exceeded'):
                fixture.wait_for_broker_readiness(command, 'owned-broker', 19494)
        self.assertEqual(command.call_count, 2)
        connection.__exit__.assert_called_once()
        sleep.assert_not_called()

    def test_readiness_has_overall_deadline_and_rejects_exited_broker(self):
        command = Mock()
        with patch.object(fixture.time, 'monotonic', side_effect=[0, 91]):
            with self.assertRaisesRegex(RuntimeError, 'bounded broker readiness failed'):
                fixture.wait_for_broker_readiness(command, 'owned-broker', 19494)
        command.assert_not_called()
        command.return_value = subprocess.CompletedProcess([], 0, 'false', '')
        with self.assertRaisesRegex(RuntimeError, 'broker exited'):
            fixture.wait_for_broker_readiness(command, 'owned-broker', 19494)

    def test_exited_zombie_permission_error_requires_reap_and_group_absence(self):
        child = Mock(pid=12345)
        with patch.object(fixture.os, 'killpg', side_effect=[PermissionError(), ProcessLookupError()]) as signal:
            self.assertTrue(fixture.reap_fixture_test_group(child, True))
        child.wait.assert_called_once_with(timeout=30)
        self.assertEqual([call.args for call in signal.call_args_list], [(12345, fixture.signal.SIGKILL), (12345, 0)])
        child = Mock(pid=12345)
        with patch.object(fixture.os, 'killpg', side_effect=[PermissionError(), PermissionError()]):
            with self.assertRaisesRegex(RuntimeError, 'absence could not be proven'):
                fixture.reap_fixture_test_group(child, True)

    def test_active_group_signal_permission_error_cannot_claim_reap(self):
        child = Mock(pid=12345)
        with patch.object(fixture.os, 'killpg', side_effect=PermissionError()):
            with self.assertRaisesRegex(RuntimeError, 'active test group could not be stopped'):
                fixture.reap_fixture_test_group(child, False)
        child.wait.assert_not_called()
    def test_broker_failure_diagnostics_are_bounded_and_redacted(self):
        output = 'Error password=private-password\nCaused by private-key-material\n' + ('Exception bounded detail\n' * 100)
        lines = fixture.broker_error_summary(output, 'private-password', '-----BEGIN PRIVATE KEY-----\nprivate-key-material\n-----END PRIVATE KEY-----')
        self.assertEqual(len(lines), 16)
        self.assertNotIn('private-password', json.dumps(lines))
        self.assertNotIn('private-key-material', json.dumps(lines))
        self.assertIn('[REDACTED]', json.dumps(lines))
        self.assertIn('[fixture private key material omitted]', lines)
        self.assertLessEqual(len(fixture.broker_error_summary('Exception ' + 'x'*2000, 'secret', '')[0]), 512)
    def test_container_copy_owns_only_required_protected_inputs(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-copy-test-') as directory:
            scratch = Path(directory)
            required = {'password', 'ca.pem', 'broker.pem', 'server.properties', 'start.sh', 'admin.properties'}
            for name in required | {'ca.key', 'broker.key', 'fixture.json'}:
                path = scratch/name
                path.write_bytes(b'private fixture canary')
                path.chmod(0o600)
            with tarfile.open(fileobj=io.BytesIO(fixture.protected_container_bundle(scratch))) as archive:
                self.assertEqual(set(archive.getnames()), required)
                for member in archive.getmembers():
                    self.assertEqual((member.uid, member.gid, member.mode), (0, 0, 0o600))
                    self.assertTrue(member.isfile())
            (scratch/'password').chmod(0o644)
            with self.assertRaisesRegex(RuntimeError, 'protected regular file'):
                fixture.protected_container_bundle(scratch)
            (scratch/'password').unlink()
            (scratch/'password').symlink_to(scratch/'ca.key')
            with self.assertRaises(OSError):
                fixture.protected_container_bundle(scratch)
            (scratch/'password').unlink()
            os.mkfifo(scratch/'password', 0o600)
            with self.assertRaisesRegex(RuntimeError, 'protected regular file'):
                fixture.protected_container_bundle(scratch)

    def test_cleanup_timeout_does_not_skip_remaining_resources_scratch_or_report(self):
        for failure in ('timeout', 'unavailable'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory(prefix='apexload-kafka-cleanup-test-') as directory:
                report = Path(directory)/'report.json'
                calls = []
                def command(argv, **_):
                    calls.append(argv)
                    if argv[:3] == ['docker', 'buildx', 'imagetools']:
                        return subprocess.CompletedProcess(argv, 1, '', 'private provider detail')
                    if argv[:3] == ['docker', 'rm', '--force']:
                        if failure == 'timeout': raise subprocess.TimeoutExpired(argv, 60)
                        raise OSError('private host detail')
                    return subprocess.CompletedProcess(argv, 0, '', '')
                with patch.object(sys, 'argv', ['fixture', '--report', str(report)]), patch.object(fixture.subprocess, 'run', side_effect=command), patch.object(fixture.shutil, 'disk_usage', return_value=shutil._ntuple_diskusage(40*fixture.GIB, 10*fixture.GIB, 30*fixture.GIB)), contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaisesRegex(RuntimeError, 'fixture command failed'):
                        fixture.main()
                evidence = json.loads(report.read_text())
                self.assertEqual(evidence['status'], 'failed')
                self.assertTrue(evidence['scratch_removed'])
                self.assertTrue(evidence['cleanup_verified'])
                self.assertEqual(len(evidence['cleanup']), 4)
                self.assertIn('error', evidence['cleanup'][0])
                self.assertEqual(sum(argv[:3] == ['docker', 'volume', 'rm'] for argv in calls), 2)
                self.assertEqual(sum(argv[:3] == ['docker', 'network', 'rm'] for argv in calls), 1)
                self.assertNotIn('private provider detail', report.read_text())
                self.assertNotIn('private host detail', report.read_text())

    def test_unavailable_inventory_cannot_claim_cleanup_success(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-cleanup-test-') as directory:
            report = Path(directory)/'report.json'
            def command(argv, **_):
                if argv[:3] == ['docker', 'buildx', 'imagetools']:
                    return subprocess.CompletedProcess(argv, 1, '', '')
                if argv[:2] == ['docker', 'ps']:
                    return subprocess.CompletedProcess(argv, 1, '', '')
                return subprocess.CompletedProcess(argv, 0, '', '')
            with patch.object(sys, 'argv', ['fixture', '--report', str(report)]), patch.object(fixture.subprocess, 'run', side_effect=command), patch.object(fixture.shutil, 'disk_usage', return_value=shutil._ntuple_diskusage(40*fixture.GIB, 10*fixture.GIB, 30*fixture.GIB)), contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, 'removal could not be proven'):
                    fixture.main()
            evidence = json.loads(report.read_text())
            self.assertFalse(evidence['cleanup_verified'])
            self.assertTrue(evidence['scratch_removed'])
            self.assertEqual(evidence['status'], 'failed')




class TransactionProfileTest(unittest.TestCase):
    def test_transaction_profile_requires_three_distinct_actual_cases(self):
        profile = fixture.suite_definition('transactions')
        self.assertEqual(profile['environment'], 'APEXLOAD_KAFKA_TRANSACTION_FIXTURE')
        self.assertIn('-tags=kafka_real,kafka_transaction_integration', profile['command'])
        self.assertIn('^TestKafkaTransactionsActualBroker$', profile['command'])
        self.assertEqual(profile['required'], (
            '--- PASS: TestKafkaTransactionsActualBroker ',
            '--- PASS: TestKafkaTransactionsActualBroker/read_committed_latest_lso ',
            '--- PASS: TestKafkaTransactionsActualBroker/consume_transform_commit_abort ',
            '--- PASS: TestKafkaTransactionsActualBroker/stale_producer_fencing '))
        previous = fixture.suite_definition('owned-seek')
        self.assertEqual(previous['environment'], 'APEXLOAD_KAFKA_SEEK_FIXTURE')
        self.assertEqual(len(previous['required']), 5)
        self.assertNotIn(profile['environment'], previous['command'])

    def test_scope_uses_distinct_exact_topics_groups_and_short_uuid_safe_prefix(self):
        inventory = fixture.transaction_inventory('123456abcdef')
        self.assertEqual(len(set(inventory['topics'] + inventory['groups'])), 6)
        self.assertEqual(inventory['transactional_prefix'], 'apex-txn-123456abcdef-')
        self.assertLess(len(inventory['transactional_prefix']) + len('visibility') + 37, 249)
        for bad in ('*', '123456abcdeg', '123456abcdef0', '../123456abc'):
            with self.assertRaises(ValueError): fixture.transaction_inventory(bad)

    def test_oracle_acl_is_describe_only_and_output_reader_never_joins_group(self):
        inventory = fixture.transaction_inventory('123456abcdef')
        plan = fixture.transaction_acl_plan(inventory)
        for grant in plan:
            principal = grant[grant.index('--allow-principal') + 1]
            operations = [grant[i+1] for i, value in enumerate(grant) if value == '--operation']
            self.assertNotIn('All', operations)
            self.assertNotIn('*', grant)
            if principal == 'User:oracle':
                self.assertEqual(operations, ['Describe'])
                self.assertTrue('--topic' in grant or '--group' in grant)
            if principal == 'User:output':
                self.assertEqual(operations, ['Read', 'Describe'])
                self.assertEqual(grant[-2:], ['--topic', inventory['output_topic']])
            if principal == 'User:writer':
                self.assertNotIn('--group', grant)
                self.assertNotIn('Read', operations)
            if '--transactional-id' in grant:
                self.assertIn(principal, ('User:executor', 'User:writer'))
                self.assertEqual(operations, ['Write', 'Describe'])
                self.assertEqual(grant[grant.index('--transactional-id') + 1], inventory['transactional_prefix'])
                self.assertEqual(grant[-2:], ['--resource-pattern-type', 'prefixed'])
        self.assertEqual(sum(grant[1] == 'User:oracle' for grant in plan), 6)
        self.assertEqual(sum(grant[1] == 'User:output' for grant in plan), 1)

    def test_all_four_transaction_credentials_are_redacted_on_failure(self):
        with tempfile.TemporaryDirectory(prefix='apexload-kafka-txn-log-test-') as directory:
            scratch = Path(directory)
            (scratch/'broker.key').write_text('-----BEGIN PRIVATE KEY-----\nkeymaterial\n-----END PRIVATE KEY-----')
            log = scratch/'report.tests.txt'
            log.write_text('FAIL executor-private oracle-private writer-private output-private keymaterial')
            report = {'status': 'passed', 'test_log_created': True}
            fixture.finalize_fixture_evidence(report, scratch, log,
                ['executor-private', 'oracle-private', 'writer-private', 'output-private'])
            self.assertEqual(report['status'], 'failed')
            self.assertEqual(log.read_text(), 'FAIL [REDACTED] [REDACTED] [REDACTED] [REDACTED] [REDACTED]')
            self.assertTrue(report['test_log_withheld_or_redacted'])


if __name__ == '__main__':
    unittest.main()
