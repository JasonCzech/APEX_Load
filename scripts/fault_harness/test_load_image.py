import copy
import unittest
from unittest.mock import patch

from load_image import ImageError, digest_alias_needed, validate_source, validate_worker, stream_import


class ImageLoadTest(unittest.TestCase):
    def setUp(self):
        self.digest = 'sha256:' + 'a' * 64
        self.tag = 'docker.io/library/apexload-controller:fault-abc'
        self.reference = 'docker.io/library/apexload-controller@' + self.digest
        self.image = [{'Os': 'linux', 'Architecture': 'arm64', 'RepoDigests': ['apexload-controller@' + self.digest]}]
        self.cluster = 'apexload-fault-' + 'a' * 16
        self.node = self.cluster + '-worker'
        self.worker = [{'Id': 'b' * 64, 'Name': '/' + self.node, 'State': {'Running': True},
                        'Config': {'Labels': {'io.x-k8s.kind.cluster': self.cluster, 'io.x-k8s.kind.role': 'worker'}}}]

    def test_source_is_pinned_in_expected_repository_and_platform(self):
        self.assertEqual(validate_source(self.tag, self.reference, self.image), 'arm64')
        for field, value in [('Os', 'windows'), ('Architecture', 'unknown'), ('RepoDigests', None),
                             ('RepoDigests', ['apexload-controller@sha256:' + 'c' * 64])]:
            image = copy.deepcopy(self.image)
            image[0][field] = value
            with self.subTest(field=field), self.assertRaises(ImageError):
                validate_source(self.tag, self.reference, image)
        for tag in ('apexload-controller:fault-abc', self.tag + '@sha256:bad', 'docker.io/other/image:tag'):
            with self.subTest(tag=tag), self.assertRaises(ImageError):
                validate_source(tag, self.reference, self.image)

    def test_workers_are_owned_running_and_immutable(self):
        self.assertEqual(validate_worker(self.worker, self.cluster, self.node), 'b' * 64)
        for field, value in [('Id', ''), ('Name', '/other'), ('State', {'Running': False}), ('Config', {'Labels': {}})]:
            worker = copy.deepcopy(self.worker)
            worker[0][field] = value
            with self.subTest(field=field), self.assertRaises(ImageError):
                validate_worker(worker, self.cluster, self.node)
        with self.assertRaises(ImageError):
            validate_worker(self.worker, self.cluster, self.cluster + '-control-plane')

    def test_tag_presence_does_not_imply_digest_reference_presence(self):
        row = self.tag + ' application/vnd.oci.image.manifest.v1+json ' + self.digest + ' 369MiB linux/arm64\n'
        self.assertTrue(digest_alias_needed(row, self.tag, self.reference))
        alias = self.reference + ' application/vnd.oci.image.manifest.v1+json ' + self.digest + '\n'
        self.assertFalse(digest_alias_needed(row + alias, self.tag, self.reference))
        for listing in ('', row.replace(self.digest, 'sha256:' + 'c' * 64), row + row,
                        row + alias.replace('json ' + self.digest, 'json sha256:' + 'd' * 64)):
            with self.subTest(listing=listing), self.assertRaises(ImageError):
                digest_alias_needed(listing, self.tag, self.reference)

    def test_interrupted_stream_reaps_both_processes(self):
        from unittest.mock import MagicMock
        source, sink = MagicMock(), MagicMock()
        source.poll.return_value = None
        sink.poll.return_value = None
        sink.wait.side_effect = [KeyboardInterrupt(), 0]
        with patch('load_image.subprocess.Popen', side_effect=[source, sink]), self.assertRaises(KeyboardInterrupt):
            stream_import(self.tag, 'arm64', 'b' * 64, MagicMock())
        source.kill.assert_called_once()
        sink.kill.assert_called_once()
        source.stdout.close.assert_called()
        source.wait.assert_called_once_with(timeout=5)

    def test_failed_consumer_launch_reaps_exporter(self):
        from unittest.mock import MagicMock
        source = MagicMock()
        source.poll.return_value = None
        with patch('load_image.subprocess.Popen', side_effect=[source, OSError('cannot start importer')]), self.assertRaises(OSError):
            stream_import(self.tag, 'arm64', 'b' * 64, MagicMock())
        source.kill.assert_called_once()
        source.wait.assert_called_once_with(timeout=5)

    def test_source_and_sink_exit_codes_are_required(self):
        from unittest.mock import MagicMock
        for source_code, sink_code in ((1, 0), (0, 1)):
            source, sink = MagicMock(), MagicMock()
            source.wait.return_value, sink.wait.return_value = source_code, sink_code
            source.poll.return_value, sink.poll.return_value = source_code, sink_code
            with self.subTest(source=source_code, sink=sink_code), patch(
                    'load_image.subprocess.Popen', side_effect=[source, sink]), self.assertRaises(ImageError):
                stream_import(self.tag, 'arm64', 'b' * 64, MagicMock())

    def test_second_worker_validation_failure_prevents_all_imports(self):
        import json
        import tempfile
        from pathlib import Path
        from load_image import load
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            run = 'a' * 16
            (root / 'ownership.json').write_text(json.dumps({'run_id': run, 'cluster': self.cluster,
                'namespace': 'apexload-fault', 'namespace_label': 'apexload.io/fault-run=' + run}))
            other = copy.deepcopy(self.worker)
            other[0]['Name'] = '/' + self.cluster + '-worker2'
            other[0]['Config']['Labels']['io.x-k8s.kind.cluster'] = 'unrelated'
            responses = [json.dumps(self.image), json.dumps(self.worker), 'aarch64\n', json.dumps(other)]
            with patch('load_image.command', side_effect=responses), patch('load_image.stream_import') as importing:
                with self.assertRaises(ImageError):
                    load(root, self.tag, self.reference, root / 'evidence')
                importing.assert_not_called()
                self.assertFalse((root / 'evidence').exists())

    def test_complete_load_creates_and_verifies_missing_digest_aliases(self):
        import json
        import tempfile
        from pathlib import Path
        from load_image import load
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            run = 'a' * 16
            (root / 'ownership.json').write_text(json.dumps({'run_id': run, 'cluster': self.cluster,
                'namespace': 'apexload-fault', 'namespace_label': 'apexload.io/fault-run=' + run}))
            aliases = set()
            def execute(argv, timeout=15):
                if argv[:3] == ['docker', 'image', 'inspect']:
                    return json.dumps(self.image)
                if argv[:2] == ['docker', 'inspect']:
                    worker = copy.deepcopy(self.worker)
                    worker[0]['Name'] = '/' + argv[-1]
                    worker[0]['Id'] = ('c' if argv[-1].endswith('worker2') else 'b') * 64
                    return json.dumps(worker)
                container = argv[2]
                if argv[-2:] == ['uname', '-m']:
                    return 'aarch64'
                if argv[-1] == 'list':
                    listing = self.tag + ' manifest ' + self.digest + '\n'
                    if container in aliases:
                        listing += self.reference + ' manifest ' + self.digest + '\n'
                    return listing
                if argv[-3:] == ['tag', self.tag, self.reference]:
                    aliases.add(container)
                    return self.reference
                self.fail('unexpected command')
            with patch('load_image.command', side_effect=execute), patch('load_image.stream_import') as importing:
                report = load(root, self.tag, self.reference, root / 'evidence')
            self.assertEqual(importing.call_count, 2)
            self.assertEqual(len(aliases), 2)
            self.assertTrue(all(w['digest_alias_created'] for w in report['workers']))
            self.assertEqual(json.loads((root / 'evidence/result.json').read_text()), report)
            self.assertEqual((root / 'evidence/result.json').stat().st_mode & 0o777, 0o600)

    def test_control_plane_requires_explicit_owned_role(self):
        from load_image import validate_node
        control = copy.deepcopy(self.worker)
        node = self.cluster + '-control-plane'
        control[0]['Name'] = '/' + node
        control[0]['Config']['Labels']['io.x-k8s.kind.role'] = 'control-plane'
        self.assertEqual(validate_node(control, self.cluster, node, 'control-plane'), 'b' * 64)
        with self.assertRaises(ImageError):
            validate_worker(control, self.cluster, node)
        for role, name, cluster in [('worker', node, self.cluster), ('control-plane', self.node, self.cluster),
                                    ('control-plane', node, 'other'), ('all', node, self.cluster)]:
            with self.subTest(role=role, name=name, cluster=cluster), self.assertRaises(ImageError):
                validate_node(control, cluster, name, role)
        for field, value in [('Id', ''), ('Name', '/docker-desktop'), ('State', {'Running': False}), ('Config', {'Labels': {}})]:
            invalid = copy.deepcopy(control)
            invalid[0][field] = value
            with self.subTest(field=field), self.assertRaises(ImageError):
                validate_node(invalid, self.cluster, node, 'control-plane')

    def test_invalid_placement_does_not_inspect_or_import(self):
        from load_image import load
        with patch('load_image.command') as command, patch('load_image.stream_import') as importing:
            with self.assertRaises(ImageError):
                load('/unused', self.tag, self.reference, '/unused/output', 'all')
            command.assert_not_called()
            importing.assert_not_called()

    def test_explicit_control_plane_import_only_touches_owned_container(self):
        import json
        import tempfile
        from pathlib import Path
        from load_image import load
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            run = 'a' * 16
            (root / 'ownership.json').write_text(json.dumps({'run_id': run, 'cluster': self.cluster,
                'namespace': 'apexload-fault', 'namespace_label': 'apexload.io/fault-run=' + run}))
            control = copy.deepcopy(self.worker)
            node = self.cluster + '-control-plane'
            control[0]['Name'] = '/' + node
            control[0]['Config']['Labels']['io.x-k8s.kind.role'] = 'control-plane'
            row = self.tag + ' manifest ' + self.digest + '\n'
            alias = self.reference + ' manifest ' + self.digest + '\n'
            responses = [json.dumps(self.image), json.dumps(control), 'aarch64', row, self.reference, row + alias]
            with patch('load_image.command', side_effect=responses) as command, patch('load_image.stream_import') as importing:
                report = load(root, self.tag, self.reference, root / 'evidence', 'control-plane')
            importing.assert_called_once()
            self.assertEqual(importing.call_args.args[:3], (self.tag, 'arm64', 'b' * 64))
            self.assertEqual(report['placement'], 'control-plane')
            self.assertEqual(report['workers'], [])
            self.assertEqual(report['nodes'][0]['node'], node)
            self.assertEqual(json.loads((root / 'evidence/result.json').read_text()), report)
            self.assertEqual(command.call_args_list[1].args[0], ['docker', 'inspect', node])
            for call in command.call_args_list[2:]:
                self.assertEqual(call.args[0][:3], ['docker', 'exec', 'b' * 64])
