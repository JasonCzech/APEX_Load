"""Unit tests for the multi-project stack driver. They start no containers.

python3 -m unittest discover -s scripts/multiproject -p 'test_*.py'
"""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

import stack


class ActivePathsTest(unittest.TestCase):
    def test_reads_the_aks_ingress_execution_paths(self):
        paths = stack.active_paths(stack.INGRESS_FILE.read_text())
        for path in ('/api/v1/tests', '/api/v1/projects', '/api/v1/scripts', '/api/v1/ws',
                     '/api/v1/system/execution-readiness'):
            self.assertIn(path, paths)
        self.assertNotIn('/', paths)

    def test_ignores_paths_for_the_all_replica_service(self):
        text = '\n'.join([
            'metadata:', '  name: apexload-controller',
            '  paths:',
            '    - path: /api/v1/tests  # execution', '      backend:', '        service:',
            '          name: apexload-controller-active', '          port:', '            name: http',
            '    - path: /', '      backend:', '        service:', '          name: apexload-controller',
        ])
        self.assertEqual(stack.active_paths(text), ['/api/v1/tests'])

    def test_refuses_an_ingress_without_execution_paths(self):
        with self.assertRaises(stack.StackError):
            stack.active_paths('- path: /\n  name: apexload-controller\n')


class HAProxyTest(unittest.TestCase):
    def test_routes_prefixes_like_kubernetes_and_checks_leadership(self):
        config = stack.render_haproxy(['/api/v1/tests', '/api/v1/ci'])
        for path in ('/api/v1/tests', '/api/v1/ci'):
            self.assertIn('acl execution_path path -m str ' + path + '\n', config)
            self.assertIn('acl execution_path path -m beg ' + path + '/\n', config)
        leader = config.split('\nbackend execution_leader\n', 1)[1].split('\nbackend replicas\n', 1)[0]
        self.assertIn('option httpchk GET /api/v1/system/execution-readiness', leader)
        for name in stack.CONTROLLERS:
            self.assertIn('server %s %s:8080' % (name, name), leader)
        self.assertIn('default_backend replicas', config)


class RenderTest(unittest.TestCase):
    def test_credentials_are_distinct_url_safe_tokens(self):
        credentials = stack.new_credentials()
        self.assertEqual(set(credentials), set(stack.CREDENTIAL_KEYS))
        self.assertEqual(len(set(credentials.values())), len(credentials))
        for value in credentials.values():
            self.assertRegex(value, stack.TOKEN_PATTERN)

    def test_target_config_meets_the_target_contract(self):
        credentials = stack.new_credentials()
        config = json.loads(stack.render_target_config('0123456789abcdef', credentials))
        # Mirrors readConfig in cmd/apexfault-target/main.go.
        self.assertEqual(set(config), {'run_id', 'load_token', 'observer_token'})
        self.assertRegex(config['run_id'], r'^[0-9a-f]{16}$')
        self.assertNotEqual(config['load_token'], config['observer_token'])
        with self.assertRaises(stack.StackError):
            stack.render_target_config('not-hex', credentials)

    def test_bootstrap_sql_names_the_proxy_secret_and_never_overwrites(self):
        sql = stack.render_bootstrap_sql()
        self.assertIn('"trusted_proxy_secret_env_var":"' + stack.PROXY_SECRET_ENV + '"', sql)
        self.assertIn('ON CONFLICT(key) DO NOTHING', sql)
        self.assertIn('RAISE EXCEPTION', sql)
        self.assertTrue(sql.startswith('BEGIN;') and sql.rstrip().endswith('COMMIT;'))

    def test_s3_identity_is_scoped_to_the_archive_bucket(self):
        identities = json.loads(stack.render_s3_identities(stack.new_credentials()))['identities']
        self.assertEqual(len(identities), 1)
        self.assertTrue(all(action.endswith(':' + stack.BUCKET) for action in identities[0]['actions']))


class ComposeContractTest(unittest.TestCase):
    def env(self, **images):
        defaults = {'controller': 'c:1', 'generator': 'g:1', 'target': 't:1'}
        defaults.update(images)
        return stack.compose_env('0123456789abcdef', Path('/tmp/mp-work'), stack.new_credentials(), defaults)

    def test_every_compose_variable_is_provided_and_used(self):
        referenced = set(re.findall(r'\$\{(MP_[A-Z0-9_]+)', stack.COMPOSE_FILE.read_text()))
        provided = set(self.env())
        self.assertEqual(referenced - provided, set(), 'compose.yaml needs variables the driver never sets')
        self.assertEqual(provided - referenced, set(), 'the driver sets variables compose.yaml never reads')

    def test_refuses_values_the_env_file_cannot_carry(self):
        with self.assertRaises(stack.StackError):
            self.env(controller='image with space')

    def test_compose_runs_every_declared_generator_and_controller(self):
        text = stack.COMPOSE_FILE.read_text()
        for name in stack.GENERATORS + stack.CONTROLLERS:
            self.assertRegex(text, r'\n  %s:\n' % re.escape(name))


class SuiteEnvTest(unittest.TestCase):
    """The driver and the Go suite must agree on every variable name."""

    @classmethod
    def setUpClass(cls):
        source = (stack.HERE / 'harness_test.go').read_text() + (stack.HERE / 'concurrency_test.go').read_text()
        cls.read_by_suite = set(re.findall(r'"(APEXLOAD_MP_[A-Z0-9_]+)"', source))

    def test_docker_environment_is_read_by_the_suite(self):
        state = {'run_id': '0123456789abcdef', 'work': '/tmp/mp-work',
                 'ports': {'router': 1, 'target': 2, 'controller-1': 3, 'controller-2': 4}}
        env = stack.docker_suite_env(state, stack.new_credentials())
        self.assertEqual(set(env) - self.read_by_suite, set())
        self.assertEqual(env['APEXLOAD_MP_CONTROLLER_URLS'], 'http://127.0.0.1:3,http://127.0.0.1:4')
        self.assertEqual(env['APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS'], '1')

    def test_external_keys_are_read_by_the_suite(self):
        names = set(stack.EXTERNAL_KEYS.values()) | {
            'APEXLOAD_MP_CONTROLLER_URLS', 'APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS',
            'APEXLOAD_MP_DELETE_PROJECTS', 'APEXLOAD_MP_EVIDENCE_DIR'}
        self.assertEqual(names - self.read_by_suite, set())

    def external(self, **overrides):
        config = {'base_url': 'https://apexload.example.com', 'admin_api_key': 'key',
                  'target_url': 'http://apexfault-target.apexload.svc:8090', 'observer_url': 'http://127.0.0.1:18090',
                  'target_run_id': '0123456789abcdef', 'target_load_token': 'l' * 32,
                  'target_observer_token': 'o' * 32}
        config.update(overrides)
        return config

    def test_external_config_maps_and_defaults_to_a_shared_deployment(self):
        env = stack.external_suite_env(self.external(profile='aks', controller_urls=['http://a', 'http://b']), Path('/tmp/e'))
        self.assertEqual(env['APEXLOAD_MP_PROFILE'], 'aks')
        self.assertEqual(env['APEXLOAD_MP_CONTROLLER_URLS'], 'http://a,http://b')
        self.assertNotIn('APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS', env)
        self.assertEqual(env['APEXLOAD_MP_EVIDENCE_DIR'], '/tmp/e')
        env = stack.external_suite_env(self.external(mutate_global_settings=True), Path('/tmp/e'))
        self.assertEqual(env['APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS'], '1')

    def test_suite_process_env_drops_inherited_suite_settings_but_keeps_tuning(self):
        inherited = {'APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS': '1', 'APEXLOAD_MP_ADMIN_API_KEY': 'stale',
                     'APEXLOAD_MP_VUSERS': '20', 'APEXLOAD_MP_RACE_ROUNDS': '5', 'KEEP_ME': 'yes'}
        with mock.patch.dict(os.environ, inherited):
            env = stack.suite_process_env({'APEXLOAD_MP_BASE_URL': 'https://apexload.example.com', 'APEXLOAD_MP_RACE_ROUNDS': '2'})
        self.assertNotIn('APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS', env)
        self.assertNotIn('APEXLOAD_MP_ADMIN_API_KEY', env)
        self.assertEqual(env['APEXLOAD_MP_BASE_URL'], 'https://apexload.example.com')
        self.assertEqual(env['APEXLOAD_MP_VUSERS'], '20')
        self.assertEqual(env['APEXLOAD_MP_RACE_ROUNDS'], '2', "the run's own setting wins over an export")
        self.assertEqual(env['KEEP_ME'], 'yes')

    def test_tuning_knobs_are_read_by_the_suite(self):
        self.assertEqual(set(stack.TUNING_ENV) - self.read_by_suite, set())

    def test_external_config_refuses_typos_and_missing_identity(self):
        with self.assertRaises(stack.StackError):
            stack.external_suite_env(self.external(mutate_global_setting=True), Path('/tmp/e'))
        with self.assertRaises(stack.StackError):
            stack.external_suite_env(self.external(admin_api_key=''), Path('/tmp/e'))
        with self.assertRaises(stack.StackError):
            stack.external_suite_env(self.external(target_url=''), Path('/tmp/e'))


class ConfigureCapacityTest(unittest.TestCase):
    """configure_capacity refreshes and retries when a replica served a stale
    ETag, the way the settings API asks clients to."""

    def test_retries_a_stale_revision_and_keeps_other_settings(self):
        import http.server
        import threading
        state = {'revision': 'ss-1', 'puts': 0, 'stale_reads': 1,
                 'doc': {'general': {'max_concurrent_tests': 10}, 'capacity_planning': {'headroom_pct': 20,
                         'availability': {'source': 'configured', 'configured_available_generators': 1}}}}

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reply(self, status, body=None, etag=None):
                self.send_response(status)
                if etag:
                    self.send_header('ETag', '"%s"' % etag)
                self.end_headers()
                self.wfile.write(json.dumps(body or {}).encode())

            def do_GET(self):
                revision = state['revision']
                if state['stale_reads'] and state['puts']:
                    state['stale_reads'] -= 1
                    revision = 'ss-stale'
                self.reply(200, {**state['doc'], 'revision': revision}, revision)

            def do_PUT(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                state['puts'] += 1
                if self.headers['If-Match'] != '"%s"' % state['revision'] or state['puts'] == 1:
                    # The first PUT loses to a concurrent update elsewhere.
                    state['revision'] = 'ss-%d' % (state['puts'] + 1)
                    return self.reply(412, {'error': 'system settings changed concurrently; refresh and retry'})
                state['doc'] = {key: value for key, value in body.items() if key != 'revision'}
                self.reply(200, state['doc'], state['revision'])

        server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        # A proxy in the environment must not be used: these calls carry the
        # proxy secret and go only to the driver's own loopback ports.
        proxies = {name: 'http://127.0.0.1:9' for name in ('http_proxy', 'HTTP_PROXY', 'https_proxy', 'HTTPS_PROXY')}
        try:
            with mock.patch.dict(os.environ, {**proxies, 'no_proxy': '', 'NO_PROXY': ''}):
                stack.configure_capacity('http://127.0.0.1:%d' % server.server_port, {}, 3)
        finally:
            server.shutdown()
        availability = state['doc']['capacity_planning']['availability']
        self.assertEqual(availability, {'source': 'configured_plus_live', 'configured_available_generators': 3})
        self.assertEqual(state['doc']['capacity_planning']['headroom_pct'], 20)
        self.assertGreaterEqual(state['puts'], 3)


@unittest.skipIf(shutil.which('openssl') is None, 'openssl is required')
class CertificateTest(unittest.TestCase):
    def test_issues_chained_certificates_and_discards_the_ca_key(self):
        with tempfile.TemporaryDirectory() as directory:
            bundle = Path(directory)
            stack.make_certificates(bundle, '0123456789abcdef')
            self.assertEqual(sorted(p.name for p in bundle.iterdir()),
                             ['ca.crt', 'controller.crt', 'controller.key', 'generator.crt', 'generator.key'])
            for role in ('controller', 'generator'):
                verify = subprocess.run(['openssl', 'verify', '-CAfile', 'ca.crt', role + '.crt'], cwd=bundle,
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(verify.returncode, 0, verify.stdout + verify.stderr)
                text = subprocess.run(['openssl', 'x509', '-in', role + '.crt', '-noout', '-text'], cwd=bundle,
                                      capture_output=True, text=True, timeout=30).stdout
                self.assertIn('DNS:apexload-' + role, text)
                self.assertEqual((bundle / (role + '.key')).stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
