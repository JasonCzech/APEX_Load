import base64
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('redaction', Path(__file__).with_name('redact-recording-evidence.py'))
redaction = importlib.util.module_from_spec(spec)
spec.loader.exec_module(redaction)


class RecordingEvidenceTest(unittest.TestCase):
    def test_redacts_extensionless_download_and_base64_har_content(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            body = json.dumps({'csrf_token': 'fresh-private-token', 'password': 'admin'}).encode()
            har = {'log': {'entries': [{'response': {'content': {
                'encoding': 'base64', 'text': base64.b64encode(body).decode(),
            }}}]}}
            path = root / 'recording-har-content-hash'
            path.write_text(json.dumps(har))
            with zipfile.ZipFile(root / 'trace.zip', 'w') as trace:
                trace.writestr('resources/har-content-hash', json.dumps(har))
            redaction.sanitize(root, ['fresh-private-token'])
            with zipfile.ZipFile(root / 'trace.zip') as trace:
                versions = [path.read_text(), trace.read('resources/har-content-hash')]
            for version in versions:
                content = json.loads(version)['log']['entries'][0]['response']['content']
                clean = json.loads(base64.b64decode(content['text']))
                self.assertEqual(clean, {'csrf_token': '[redacted]', 'password': '[redacted]'})

    def test_redacts_json_lines_password_input_and_embedded_login_body(self):
        events = [
            {'type': 'before', 'params': {'selector': 'internal:label="Password"s', 'value': 'admin'}},
            {'request': {'postData': '{"username":"admin","password":"admin"}'}},
        ]
        cleaned = redaction.redact_text('\n'.join(json.dumps(event) for event in events) + '\n', [])
        first, second = [json.loads(line) for line in cleaned.splitlines()]
        self.assertEqual(first['params']['value'], '[redacted]')
        self.assertEqual(json.loads(second['request']['postData']), {'username': 'admin', 'password': '[redacted]'})

    def test_redacts_synthetic_login_password_without_destroying_usernames(self):
        clean = json.loads(redaction.redact_text('{"username":"admin","password":"admin"}', []))
        self.assertEqual(clean, {'username': 'admin', 'password': '[redacted]'})

    def test_redacts_trace_resources_and_report_attachments(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            raw = b'{"capability":"synthetic-private-capability"}'
            with zipfile.ZipFile(root / 'trace.zip', 'w') as trace:
                trace.writestr('resources/response.json', raw)
                trace.writestr('resources/image.png', b'\xff\x00\xfe')
            (root / 'report.json').write_text(json.dumps({'attachments': [{'body': base64.b64encode(raw).decode()}]}))
            redaction.sanitize(root, ['synthetic-private-capability'])
            with zipfile.ZipFile(root / 'trace.zip') as trace:
                self.assertNotIn(b'synthetic-private-capability', trace.read('resources/response.json'))
                self.assertEqual(trace.read('resources/image.png'), b'\xff\x00\xfe')
            body = json.loads((root / 'report.json').read_text())['attachments'][0]['body']
            self.assertNotIn(b'synthetic-private-capability', base64.b64decode(body))


if __name__ == '__main__':
    unittest.main()
