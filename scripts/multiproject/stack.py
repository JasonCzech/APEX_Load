#!/usr/bin/env python3
"""Run the multi-project concurrency suite against a disposable Docker stack.

  run    build images, start a fresh stack, run the suite, keep evidence, remove the stack
  up     start a fresh stack and leave it running for iterating on the suite
  test   run the suite against a stack from `up` (--stack) or an existing
         deployment such as AKS (--external)
  down   remove a stack started by `up`

Every credential and certificate is generated fresh into a private work
directory. Nothing is reused from earlier runs, and no git command is run.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
COMPOSE_FILE = HERE / 'compose.yaml'
INGRESS_FILE = ROOT / 'deployments/k8s/aks/controller-ingress.yaml'
GO_PACKAGE = './scripts/multiproject'
GO_TEST = '^TestMultiProjectConcurrency$'

ACTIVE_SERVICE = 'apexload-controller-active'
PROXY_SECRET_ENV = 'APEXLOAD_MP_PROXY_SECRET'
ADMIN_SUBJECT = 'mp-admin'
BUCKET = 'apexload-archives'
CONTROLLERS = ('controller-1', 'controller-2')
GENERATORS = ('generator-1', 'generator-2', 'generator-3')

# Dependency images. The init image matches the repository compose.yaml pin;
# SeaweedFS matches the image the node-fault harness qualified.
DEPENDENCY_IMAGES = {
    'MP_INIT_IMAGE': 'alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6',
    'MP_POSTGRES_IMAGE': 'postgres:16-alpine',
    'MP_REDIS_IMAGE': 'redis:7-alpine',
    'MP_ROUTER_IMAGE': 'haproxy:3.2-alpine',
    'MP_S3_IMAGE': 'docker.io/chrislusf/seaweedfs@sha256:186de7ef977a20343ee9a5544073f081976a29e2d29ecf8379891e7bf177fbe9',
}

CREDENTIAL_KEYS = ('proxy', 'grpc', 'wrapping_key', 'auth_session', 'postgres', 'redis',
                   's3_access_key', 's3_secret_key', 'target_load', 'target_observer')
TOKEN_PATTERN = re.compile(r'[A-Za-z0-9_-]{32,128}')

# Private files that hold credentials. They are deleted when the stack is
# removed; evidence and logs are kept.
SECRET_FILES = ('credentials.json', 'compose.env', 'suite.env', 'bootstrap-auth.sql',
                'bundle/controller.key', 'bundle/generator.key', 'bundle/target.json', 'bundle/s3.json')


class StackError(RuntimeError):
    pass


# --- Pure renderers (unit-tested in test_stack.py) ---------------------------

def new_credentials():
    values = {key: secrets.token_urlsafe(32) for key in CREDENTIAL_KEYS}
    if len(set(values.values())) != len(values):
        raise StackError('generated credentials collided; retry')
    return values


def active_paths(ingress_text):
    """Return the ingress paths routed to the admitted execution leader."""
    paths, current = [], None
    for line in ingress_text.splitlines():
        stripped = line.split('#', 1)[0].strip()
        match = re.fullmatch(r'- path:\s*(\S+)', stripped)
        if match:
            current = match.group(1)
            continue
        match = re.fullmatch(r'name:\s*(\S+)', stripped)
        if match and current is not None and match.group(1).startswith('apexload-controller'):
            if match.group(1) == ACTIVE_SERVICE:
                paths.append(current)
            current = None
    if not paths:
        raise StackError('no execution-leader paths found in ' + str(INGRESS_FILE))
    for path in paths:
        if not re.fullmatch(r'/[A-Za-z0-9/_.-]*', path) or path == '/':
            raise StackError('unexpected execution-leader ingress path: ' + path)
    return paths


def render_haproxy(paths, controllers=CONTROLLERS):
    """Mirror the AKS ingress: leader paths go to whichever controller passes
    the execution-readiness check, everything else balances across replicas."""
    acls = []
    for path in paths:
        prefix = path.rstrip('/')
        # Kubernetes Prefix matching is element-wise: /x matches /x and /x/...
        acls.append('    acl execution_path path -m str ' + prefix)
        acls.append('    acl execution_path path -m beg ' + prefix + '/')
    servers = '\n'.join('    server {0} {0}:8080'.format(name) for name in controllers)
    return '\n'.join([
        'global',
        '    log stdout format raw local0 notice',
        '    maxconn 4096',
        '',
        'resolvers docker',
        '    nameserver dns 127.0.0.11:53',
        '    hold valid 5s',
        '    hold obsolete 5s',
        '',
        'defaults',
        '    mode http',
        '    log global',
        '    option httplog',
        '    timeout connect 5s',
        '    timeout client 3600s',
        '    timeout server 3600s',
        '    timeout tunnel 3600s',
        '    default-server init-addr last,libc,none resolvers docker check inter 1s fall 2 rise 1',
        '',
        'frontend api',
        '    bind :8080',
        *acls,
        '    use_backend execution_leader if execution_path',
        '    default_backend replicas',
        '',
        '# The role of the apexload-controller-active EndpointSlice: only the',
        '# admitted execution leader passes this check.',
        'backend execution_leader',
        '    option httpchk GET /api/v1/system/execution-readiness',
        '    http-check expect status 200',
        servers,
        '',
        'backend replicas',
        '    balance roundrobin',
        '    option httpchk GET /api/v1/system/health',
        '    http-check expect status 200',
        servers,
        '',
    ])


def auth_settings():
    return {'mode': 'trusted_headers', 'user_header': 'X-Auth-Request-User',
            'roles_header': 'X-APEXLoad-Roles', 'trusted_proxy_secret_env_var': PROXY_SECRET_ENV}


def render_bootstrap_sql():
    """Seed trusted-header auth before the controllers start. The SQL never
    overwrites an existing, different auth setting; it fails instead."""
    auth = json.dumps(auth_settings(), separators=(',', ':'), sort_keys=True)
    settings = json.dumps({'auth': auth_settings()}, separators=(',', ':'), sort_keys=True)
    if "'" in settings:
        raise StackError('auth settings must not contain a quote')
    return ("BEGIN;\n"
            "CREATE TABLE IF NOT EXISTS app_settings(key TEXT PRIMARY KEY,value TEXT,updated_at TIMESTAMPTZ);\n"
            "INSERT INTO app_settings(key,value,updated_at) VALUES ('system_settings','" + settings + "',NOW()) "
            "ON CONFLICT(key) DO NOTHING;\n"
            "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM app_settings WHERE key='system_settings' "
            "AND value::jsonb->'auth' = '" + auth + "'::jsonb) "
            "THEN RAISE EXCEPTION 'incompatible authentication settings'; END IF; END $$;\n"
            "COMMIT;\n")


def render_target_config(run_id, credentials):
    if not re.fullmatch(r'[0-9a-f]{16}', run_id):
        raise StackError('target run ID must be 16 lowercase hexadecimal characters')
    return json.dumps({'run_id': run_id, 'load_token': credentials['target_load'],
                       'observer_token': credentials['target_observer']}, indent=2) + '\n'


def render_s3_identities(credentials):
    actions = [action + ':' + BUCKET for action in ('Admin', 'Read', 'Write', 'List', 'Tagging')]
    return json.dumps({'identities': [{'name': 'mp-archive', 'credentials': [{
        'accessKey': credentials['s3_access_key'], 'secretKey': credentials['s3_secret_key']}],
        'actions': actions}]}, indent=2) + '\n'


SEAWEEDFS_CONFIG = {
    'master.toml': '[master.volume_growth]\ncopy_1 = 1\ncopy_2 = 1\ncopy_3 = 1\ncopy_other = 1\n',
    'filer.toml': '[leveldb2]\nenabled = true\ndir = "/data/filer"\n',
}


def compose_env(run_id, work, credentials, images, generator_max_vusers=200):
    """Variables compose.yaml interpolates. Values never contain whitespace or
    '#', so the env file needs no quoting."""
    env = {
        'MP_RUN_ID': run_id,
        'MP_PRIVATE_DIR': str(work),
        'MP_ADMIN_SUBJECT': ADMIN_SUBJECT,
        'MP_PROXY_SECRET': credentials['proxy'],
        'MP_GRPC_TOKEN': credentials['grpc'],
        'MP_WRAPPING_KEY': credentials['wrapping_key'],
        'MP_AUTH_SESSION_SECRET': credentials['auth_session'],
        'MP_POSTGRES_PASSWORD': credentials['postgres'],
        'MP_REDIS_PASSWORD': credentials['redis'],
        'MP_S3_ACCESS_KEY': credentials['s3_access_key'],
        'MP_S3_SECRET_KEY': credentials['s3_secret_key'],
        'MP_GENERATOR_MAX_VUSERS': str(generator_max_vusers),
        'MP_CONTROLLER_IMAGE': images['controller'],
        'MP_GENERATOR_IMAGE': images['generator'],
        'MP_TARGET_IMAGE': images['target'],
        **DEPENDENCY_IMAGES,
    }
    for key, value in env.items():
        if not value or re.search(r'[\s#"\'$]', value):
            raise StackError('compose variable %s has an unsupported value' % key)
    return env


def docker_suite_env(state, credentials):
    """The Go suite's environment for a stack this driver started."""
    ports = state['ports']
    return {
        'APEXLOAD_MP_PROFILE': 'docker',
        'APEXLOAD_MP_BASE_URL': 'http://127.0.0.1:%d' % ports['router'],
        'APEXLOAD_MP_CONTROLLER_URLS': ','.join('http://127.0.0.1:%d' % ports[name] for name in CONTROLLERS),
        'APEXLOAD_MP_PROXY_SECRET': credentials['proxy'],
        'APEXLOAD_MP_ADMIN_SUBJECT': ADMIN_SUBJECT,
        'APEXLOAD_MP_TARGET_URL': 'http://target:8090',
        'APEXLOAD_MP_OBSERVER_URL': 'http://127.0.0.1:%d' % ports['target'],
        'APEXLOAD_MP_TARGET_RUN_ID': state['run_id'],
        'APEXLOAD_MP_TARGET_LOAD_TOKEN': credentials['target_load'],
        'APEXLOAD_MP_TARGET_OBSERVER_TOKEN': credentials['target_observer'],
        'APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS': '1',
        'APEXLOAD_MP_MIN_GENERATORS': str(len(GENERATORS)),
        # Unique per suite run, so repeated runs against one stack never reuse names.
        'APEXLOAD_MP_PREFIX': 'mp-%s-%s' % (state['run_id'][:6], format(int(time.time()) % (1 << 24), '06x')),
        'APEXLOAD_MP_EVIDENCE_DIR': str(Path(state['work']) / 'evidence'),
    }


EXTERNAL_KEYS = {
    'base_url': 'APEXLOAD_MP_BASE_URL',
    'admin_api_key': 'APEXLOAD_MP_ADMIN_API_KEY',
    'proxy_secret': 'APEXLOAD_MP_PROXY_SECRET',
    'admin_subject': 'APEXLOAD_MP_ADMIN_SUBJECT',
    'target_url': 'APEXLOAD_MP_TARGET_URL',
    'observer_url': 'APEXLOAD_MP_OBSERVER_URL',
    'target_run_id': 'APEXLOAD_MP_TARGET_RUN_ID',
    'target_load_token': 'APEXLOAD_MP_TARGET_LOAD_TOKEN',
    'target_observer_token': 'APEXLOAD_MP_TARGET_OBSERVER_TOKEN',
    'prefix': 'APEXLOAD_MP_PREFIX',
    'profile': 'APEXLOAD_MP_PROFILE',
    'vusers': 'APEXLOAD_MP_VUSERS',
    'pacing_ms': 'APEXLOAD_MP_PACING_MS',
    'min_generators': 'APEXLOAD_MP_MIN_GENERATORS',
    'race_rounds': 'APEXLOAD_MP_RACE_ROUNDS',
}


def external_suite_env(config, evidence_dir):
    """Map an external deployment description (see README.md) to the suite's
    environment. Unknown keys are refused so a typo cannot silently drop a
    setting such as mutate_global_settings."""
    allowed = set(EXTERNAL_KEYS) | {'controller_urls', 'mutate_global_settings', 'delete_projects'}
    unknown = sorted(set(config) - allowed)
    if unknown:
        raise StackError('unknown external config keys: ' + ', '.join(unknown))
    for key in ('base_url', 'target_url', 'observer_url', 'target_run_id', 'target_load_token', 'target_observer_token'):
        if not isinstance(config.get(key), str) or not config[key].strip():
            raise StackError('external config requires ' + key)
    if not config.get('admin_api_key') and not (config.get('proxy_secret') and config.get('admin_subject')):
        raise StackError('external config requires admin_api_key, or proxy_secret and admin_subject')
    env = {EXTERNAL_KEYS[key]: str(value) for key, value in config.items() if key in EXTERNAL_KEYS}
    env.setdefault('APEXLOAD_MP_PROFILE', 'external')
    urls = config.get('controller_urls', [])
    if not isinstance(urls, list) or not all(isinstance(url, str) for url in urls):
        raise StackError('controller_urls must be a list of URLs')
    if urls:
        env['APEXLOAD_MP_CONTROLLER_URLS'] = ','.join(urls)
    if config.get('mutate_global_settings') is True:
        env['APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS'] = '1'
    if config.get('delete_projects') is True:
        env['APEXLOAD_MP_DELETE_PROJECTS'] = '1'
    env['APEXLOAD_MP_EVIDENCE_DIR'] = str(evidence_dir)
    return env


# --- Side effects ------------------------------------------------------------

def write_private(path, text, replace=False):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    flags = os.O_WRONLY | os.O_CREAT | (os.O_TRUNC if replace else os.O_EXCL)
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(text)


def make_certificates(bundle, run_id):
    """A two-day private CA plus controller (client) and generator (server)
    certificates for the mutual-TLS gRPC channel. The CA key is deleted as
    soon as both certificates are signed."""
    def openssl(*args):
        result = subprocess.run(['openssl', *args], cwd=bundle, stdout=subprocess.DEVNULL,
                                stderr=subprocess.PIPE, text=True, timeout=60)
        if result.returncode:
            raise StackError('openssl failed: ' + result.stderr.strip()[-400:])
    openssl('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'ca.key', '-out', 'ca.crt',
            '-days', '2', '-subj', '/CN=apexload-multiproject-' + run_id,
            '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign',
            '-addext', 'subjectKeyIdentifier=hash', '-addext', 'authorityKeyIdentifier=keyid:always')
    for role in ('controller', 'generator'):
        write_private(bundle / (role + '.ext'),
                      'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n'
                      'subjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid:always\n'
                      'extendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:apexload-' + role + '\n')
        openssl('req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', role + '.key', '-out', role + '.csr',
                '-subj', '/CN=apexload-' + role)
        openssl('x509', '-req', '-in', role + '.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key', '-set_serial',
                str(int.from_bytes(secrets.token_bytes(16), 'big')), '-out', role + '.crt', '-days', '2',
                '-extfile', role + '.ext')
        (bundle / (role + '.csr')).unlink()
        (bundle / (role + '.ext')).unlink()
    (bundle / 'ca.key').unlink()
    for path in bundle.iterdir():
        if path.is_file():
            path.chmod(0o600)


def run(argv, *, timeout, check=True, capture=False, log=None, cwd=ROOT, env=None):
    stdout = subprocess.PIPE if capture else (log if log is not None else subprocess.DEVNULL)
    result = subprocess.run(argv, cwd=cwd, env=env, stdout=stdout, stderr=subprocess.STDOUT,
                            text=True, timeout=timeout)
    if check and result.returncode:
        detail = (result.stdout or '').strip()[-1500:] if capture else 'see ' + (log.name if log else 'output')
        raise StackError('%s failed (exit %d): %s' % (' '.join(argv[:4]), result.returncode, detail))
    return result


def image_present(reference):
    return subprocess.run(['docker', 'image', 'inspect', reference], stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL, timeout=30).returncode == 0


# Every call this driver makes goes to its own loopback ports and may carry the
# proxy secret, so it never uses an environment or system HTTP proxy.
DIRECT = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def wait_http(url, accept, timeout, headers=None):
    deadline = time.monotonic() + timeout
    last = 'no response'
    while time.monotonic() < deadline:
        request = urllib.request.Request(url, headers=headers or {})
        try:
            with DIRECT.open(request, timeout=5) as response:
                body = response.read(1 << 20)
                if accept(response.status, body):
                    return body
                last = 'HTTP %d' % response.status
        except urllib.error.HTTPError as error:
            last = 'HTTP %d' % error.code
        except (urllib.error.URLError, OSError) as error:
            last = str(error)
        time.sleep(2)
    raise StackError('%s not ready after %ds (%s)' % (url, timeout, last))


def configure_capacity(base, headers, generators):
    """Describe the fleet the way an operator would: this many generators,
    capped by live discovery. A fresh install assumes one generator."""
    def call(method, body=None, etag=None):
        extra = {'Content-Type': 'application/json'} if body is not None else {}
        if etag:
            extra['If-Match'] = etag
        request = urllib.request.Request(base + '/api/v1/system/settings', method=method,
                                         data=None if body is None else json.dumps(body).encode(),
                                         headers={**headers, **extra})
        with DIRECT.open(request, timeout=15) as response:
            return response.headers.get('ETag'), json.loads(response.read())
    try:
        for attempt in range(1, 11):
            etag, settings = call('GET')
            availability = settings.setdefault('capacity_planning', {}).setdefault('availability', {})
            if (availability.get('source') == 'configured_plus_live'
                    and availability.get('configured_available_generators') == generators):
                return
            availability['source'] = 'configured_plus_live'
            availability['configured_available_generators'] = generators
            try:
                call('PUT', settings, etag)
                return
            except urllib.error.HTTPError as error:
                # Reads balance across replicas; one that has not yet seen the
                # previous update serves a stale ETag. Refresh and retry.
                if error.code != 412:
                    raise StackError('configure capacity: HTTP %d: %s' % (error.code, error.read()[:300]))
                time.sleep(0.3 * attempt)
    except (urllib.error.URLError, OSError, ValueError) as error:
        raise StackError('configure capacity: %s' % error) from None
    raise StackError('configure capacity: settings kept changing concurrently')


class Stack:
    """One disposable compose project and its private work directory."""

    def __init__(self, work, state):
        self.work = Path(work)
        self.state = state
        self.project = state['project']
        self.logs = self.work / 'logs'

    @classmethod
    def create(cls, work_root, images, generator_max_vusers):
        run_id = secrets.token_hex(8)
        work = Path(work_root) if work_root else Path(tempfile.mkdtemp(prefix='apexload-multiproject-'))
        if work_root:
            try:
                work.mkdir(mode=0o700, parents=False, exist_ok=False)
            except OSError as error:
                raise StackError('--work-dir must be a new directory whose parent exists: %s' % error) from None
        try:
            return cls._populate(work, run_id, images, generator_max_vusers)
        except BaseException:
            # Nothing has started yet, so the generated secrets have no use.
            shutil.rmtree(work, ignore_errors=True)
            raise

    @classmethod
    def _populate(cls, work, run_id, images, generator_max_vusers):
        if re.search(r'[\s#"\'$]', str(work)):
            raise StackError('work directory path must not contain whitespace, quotes, # or $')
        credentials = new_credentials()
        write_private(work / 'credentials.json', json.dumps({'run_id': run_id, **credentials}, indent=2) + '\n')
        bundle = work / 'bundle'
        bundle.mkdir(mode=0o700)
        make_certificates(bundle, run_id)
        write_private(bundle / 'target.json', render_target_config(run_id, credentials))
        write_private(bundle / 's3.json', render_s3_identities(credentials))
        write_private(bundle / 'haproxy.cfg', render_haproxy(active_paths(INGRESS_FILE.read_text())))
        for name, text in SEAWEEDFS_CONFIG.items():
            write_private(bundle / 'seaweedfs' / name, text)
        write_private(work / 'bootstrap-auth.sql', render_bootstrap_sql())
        (work / 'logs').mkdir(mode=0o700)
        (work / 'evidence').mkdir(mode=0o700)
        state = {'run_id': run_id, 'project': 'apexload-mp-' + run_id[:12], 'work': str(work),
                 'images': images, 'generator_max_vusers': generator_max_vusers,
                 'built_images': [], 'pulled_images': [], 'ports': {}}
        stack = cls(work, state)
        stack.save()
        stack.write_compose_env()
        return stack

    def write_compose_env(self):
        env = compose_env(self.state['run_id'], self.work, self.credentials(), self.state['images'],
                          self.state['generator_max_vusers'])
        write_private(self.work / 'compose.env', ''.join('%s=%s\n' % item for item in sorted(env.items())),
                      replace=True)

    def use_images(self, images):
        """Build the application images unless the caller supplied them."""
        if images is None:
            images = self.build_images(self.state['run_id'][:12])
        self.state['images'] = images
        self.save()
        self.write_compose_env()

    @classmethod
    def load(cls, work):
        work = Path(work)
        state = json.loads((work / 'state.json').read_text())
        return cls(work, state)

    def save(self):
        path = self.work / 'state.json'
        tmp = path.with_suffix('.tmp')
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(self.state, stream, indent=2)
        tmp.replace(path)

    def credentials(self):
        return json.loads((self.work / 'credentials.json').read_text())

    def compose(self, *args, timeout=600, check=True, capture=False, log_name=None):
        argv = ['docker', 'compose', '--project-name', self.project, '--file', str(COMPOSE_FILE),
                '--env-file', str(self.work / 'compose.env'), *args]
        if log_name and not capture:
            with open(self.logs / log_name, 'a') as log:
                return run(argv, timeout=timeout, check=check, log=log)
        return run(argv, timeout=timeout, check=check, capture=capture)

    def build_images(self, tag):
        """Build the controller, generator and target images from this checkout."""
        images = {
            'controller': 'apexload-controller:mp-' + tag,
            'generator': 'apexload-generator:mp-' + tag,
            'target': 'apexload-fault-target:mp-' + tag,
        }
        builds = [
            (images['controller'], ['--file', 'Dockerfile', '--build-arg', 'BINARY=controller']),
            (images['generator'], ['--file', 'Dockerfile', '--build-arg', 'BINARY=generator']),
            (images['target'], ['--file', 'cmd/apexfault-target/Dockerfile']),
        ]
        with open(self.logs / 'build.log', 'a') as log:
            for image, args in builds:
                print('building %s (log: %s)' % (image, self.logs / 'build.log'), flush=True)
                run(['docker', 'build', '--progress=plain', *args,
                     '--build-arg', 'VERSION=multiproject-' + tag, '--tag', image, str(ROOT)],
                    timeout=3600, log=log)
                self.state['built_images'].append(image)
                self.save()
        return images

    def record_missing_dependencies(self):
        """Remember dependency images this run will pull, so cleanup removes
        only what the run added."""
        self.state['pulled_images'] = [ref for ref in DEPENDENCY_IMAGES.values() if not image_present(ref)]
        self.save()

    def start(self):
        print('starting stack %s (work: %s)' % (self.project, self.work), flush=True)
        self.record_missing_dependencies()
        self.compose('run', '--rm', '--no-deps', 'bundle-init', timeout=300, log_name='compose-up.log')
        self.compose('up', '--detach', '--wait', '--wait-timeout', '240', 'postgres', 'redis', 's3',
                     timeout=600, log_name='compose-up.log')
        self.compose('run', '--rm', '--no-deps', 'db-bootstrap', timeout=120, log_name='compose-up.log')
        self.create_bucket()
        self.compose('up', '--detach', '--wait', '--wait-timeout', '180', 'target', *GENERATORS,
                     timeout=300, log_name='compose-up.log')
        self.compose('up', '--detach', '--wait', '--wait-timeout', '300', *CONTROLLERS, 'router',
                     timeout=420, log_name='compose-up.log')
        ports = {'router': self.port('router', 8080), 'target': self.port('target', 8090)}
        for name in CONTROLLERS:
            ports[name] = self.port(name, 8080)
        self.state['ports'] = ports
        self.save()
        base = 'http://127.0.0.1:%d' % ports['router']
        wait_http(base + '/api/v1/system/execution-readiness', lambda status, _: status == 200, 180)

        def enough_generators(status, body):
            return status == 200 and json.loads(body).get('total_generators', 0) >= len(GENERATORS)
        # Fleet capacity counts discovered generators. It needs an identity;
        # the first admin request is also the one that triggers the bootstrap grant.
        credentials = self.credentials()
        headers = {'X-Auth-Request-User': ADMIN_SUBJECT, 'X-APEXLoad-Proxy-Secret': credentials['proxy']}
        wait_http(base + '/api/v1/fleet/capacity', enough_generators, 180, headers=headers)
        configure_capacity(base, headers, len(GENERATORS))
        wait_http('http://127.0.0.1:%d/healthz' % ports['target'], lambda status, _: status == 204, 60)
        print('stack ready: router %s' % base, flush=True)

    def create_bucket(self):
        script = ("printf 's3.bucket.create -name %s\\nexit\\n' | weed shell -master=127.0.0.1:9333 >/dev/null 2>&1; "
                  "printf 's3.bucket.list\\nexit\\n' | weed shell -master=127.0.0.1:9333 2>&1") % BUCKET
        deadline = time.monotonic() + 120
        while True:
            result = self.compose('exec', '-T', 's3', 'sh', '-c', script, timeout=60, check=False, capture=True)
            if result.returncode == 0 and BUCKET in (result.stdout or ''):
                return
            if time.monotonic() > deadline:
                raise StackError('could not create the archive bucket: ' + (result.stdout or '').strip()[-400:])
            time.sleep(3)

    def port(self, service, container_port):
        result = self.compose('port', service, str(container_port), timeout=30, capture=True)
        match = re.search(r':(\d+)\s*$', (result.stdout or '').strip())
        if not match:
            raise StackError('no published port for %s:%d' % (service, container_port))
        return int(match.group(1))

    def write_suite_env(self):
        env = docker_suite_env(self.state, self.credentials())
        write_private(self.work / 'suite.env', ''.join("export %s='%s'\n" % item for item in sorted(env.items())))
        return env

    def collect_logs(self):
        with open(self.logs / 'compose-services.log', 'w') as log:
            run(['docker', 'compose', '--project-name', self.project, '--file', str(COMPOSE_FILE),
                 '--env-file', str(self.work / 'compose.env'), 'logs', '--no-color', '--timestamps'],
                timeout=120, check=False, log=log)
        for path in self.logs.iterdir():
            path.chmod(0o600)

    def down(self, keep_images=False):
        print('removing stack %s' % self.project, flush=True)
        problems = []
        if (self.work / 'compose.env').exists():
            result = self.compose('--profile', 'setup', 'down', '--volumes', '--remove-orphans', '--timeout', '20',
                                  timeout=300, check=False, log_name='compose-down.log')
            if result.returncode:
                problems.append('docker compose down exited %d' % result.returncode)
        label = 'label=com.docker.compose.project=' + self.project
        for kind in ('container', 'volume', 'network'):
            argv = ['docker', kind, 'ls', '--quiet', '--filter', label]
            if kind == 'container':
                argv.insert(3, '--all')
            leftover = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=60)
            if leftover.stdout.strip():
                problems.append('%s resources remain for %s: %s' % (kind, self.project, ' '.join(leftover.stdout.split())))
        if not keep_images:
            for image in self.state.get('built_images', []) + self.state.get('pulled_images', []):
                if image_present(image):
                    removed = subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL,
                                             stderr=subprocess.PIPE, text=True, timeout=120)
                    if removed.returncode:
                        problems.append('could not remove image %s: %s' % (image, removed.stderr.strip()[-200:]))
        for name in SECRET_FILES:
            path = self.work / name
            if path.exists():
                path.unlink()
        for path in (self.work / 'bundle').glob('**/*'):
            if path.is_file():
                path.unlink()
        self.state['removed'] = True
        self.save()
        for problem in problems:
            print('cleanup: ' + problem, file=sys.stderr)
        return not problems


# Load-shape knobs a caller may export for any run. They change how much load
# each run sends, never which deployment, identity or settings the suite uses.
TUNING_ENV = frozenset({
    'APEXLOAD_MP_VUSERS', 'APEXLOAD_MP_PACING_MS', 'APEXLOAD_MP_RACE_ROUNDS',
    'APEXLOAD_MP_COUNT_TOLERANCE_REQUESTS', 'APEXLOAD_MP_COUNT_TOLERANCE_RATIO',
})


def suite_process_env(env):
    """The suite's environment: the caller's, minus any APEXLOAD_MP_* it
    exported (a sourced suite.env, say) other than the tuning knobs, plus
    exactly this run's settings. A stale APEXLOAD_MP_MUTATE_GLOBAL_SETTINGS or
    admin key must never reach a run whose own configuration leaves it unset."""
    full_env = {key: value for key, value in os.environ.items()
                if not key.startswith('APEXLOAD_MP_') or key in TUNING_ENV}
    full_env.update(env)
    return full_env


def run_suite(env, log_path, timeout_minutes):
    argv = ['go', 'test', '-tags', 'multiproject_integration', '-count=1', '-timeout', '%dm' % timeout_minutes,
            '-v', '-run', GO_TEST, GO_PACKAGE]
    print('running: ' + ' '.join(argv), flush=True)
    log_path = Path(log_path)
    log_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(log_path, 'w') as log:
        # Its own session, so an interrupt stops the test binary go starts too,
        # not only go itself.
        process = subprocess.Popen(argv, cwd=ROOT, env=suite_process_env(env), stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, text=True, start_new_session=True)
        finished = False
        try:
            for line in process.stdout:
                sys.stdout.write(line)
                log.write(line)
            finished = True
        finally:
            # Only an interrupted read stops the group: after EOF go may still
            # be exiting, and signalling it then would replace its exit code.
            if not finished and process.poll() is None:
                stop_process_group(process)
        code = process.wait()
    log_path.chmod(0o600)
    return code


def stop_process_group(process):
    for sig, grace in ((signal.SIGTERM, 30), (signal.SIGKILL, 10)):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            return
        try:
            process.wait(timeout=grace)
            return
        except subprocess.TimeoutExpired:
            continue


def preflight(min_free_gib):
    for tool in ('docker', 'go', 'openssl'):
        if shutil.which(tool) is None:
            raise StackError(tool + ' is required')
    run(['docker', 'compose', 'version'], timeout=30, capture=True)
    free = shutil.disk_usage(ROOT).free / (1 << 30)
    if free < min_free_gib:
        raise StackError('%.1f GiB free; at least %d GiB is needed for image builds and the stack '
                         '(override with --min-free-gib)' % (free, min_free_gib))


def provided_images(args):
    given = {role: getattr(args, role + '_image') for role in ('controller', 'generator', 'target')}
    if any(given.values()) and not all(given.values()):
        raise StackError('pass all of --controller-image, --generator-image and --target-image, or none')
    return given if all(given.values()) else None


PENDING_IMAGES = {'controller': 'pending', 'generator': 'pending', 'target': 'pending'}


def new_stack(args):
    preflight(args.min_free_gib)
    images = provided_images(args)
    return Stack.create(args.work_dir, images or PENDING_IMAGES, args.generator_max_vusers), images


def command_run(args):
    stack, images = new_stack(args)
    code = 1
    try:
        stack.use_images(images)
        stack.start()
        env = stack.write_suite_env()
        code = run_suite(env, stack.logs / 'go-test.log', args.timeout_minutes)
    finally:
        try:
            stack.collect_logs()
        finally:
            clean = stack.down(keep_images=args.keep_images)
    print('evidence: %s' % (stack.work / 'evidence'))
    print('logs:     %s' % stack.logs)
    if not clean:
        print('cleanup was incomplete; see the messages above', file=sys.stderr)
        return code or 1
    return code


def command_up(args):
    stack, images = new_stack(args)
    try:
        stack.use_images(images)
        stack.start()
        stack.write_suite_env()
    except BaseException:
        try:
            stack.collect_logs()
        finally:
            stack.down(keep_images=args.keep_images)
        raise
    script = Path(__file__).resolve()
    print('stack %s is running.' % stack.project)
    print('  run the suite:  python3 %s test --stack %s' % (script, stack.work))
    print('  remove it:      python3 %s down --stack %s' % (script, stack.work))
    return 0


def command_test(args):
    if bool(args.stack) == bool(args.external):
        raise StackError('pass exactly one of --stack or --external')
    if args.stack:
        stack = Stack.load(args.stack)
        if stack.state.get('removed'):
            raise StackError('that stack was removed')
        env = docker_suite_env(stack.state, stack.credentials())
        log = stack.logs / ('go-test-%d.log' % int(time.time()))
    else:
        config = json.loads(Path(args.external).read_text())
        evidence = Path(args.evidence_dir or tempfile.mkdtemp(prefix='apexload-multiproject-evidence-'))
        env = external_suite_env(config, evidence)
        log = evidence / 'go-test.log'
    code = run_suite(env, log, args.timeout_minutes)
    print('evidence: %s' % env['APEXLOAD_MP_EVIDENCE_DIR'])
    return code


def command_down(args):
    stack = Stack.load(args.stack)
    return 0 if stack.down(keep_images=args.keep_images) else 1


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)

    def stack_options(command):
        command.add_argument('--work-dir', help='new private directory for credentials, logs and evidence '
                                                '(default: a fresh temporary directory)')
        command.add_argument('--controller-image', help='use this controller image instead of building one')
        command.add_argument('--generator-image', help='use this generator image instead of building one')
        command.add_argument('--target-image', help='use this apexfault-target image instead of building one')
        command.add_argument('--generator-max-vusers', type=int, default=200)
        command.add_argument('--min-free-gib', type=int, default=15)
        command.add_argument('--keep-images', action='store_true',
                             help='keep built and newly pulled images after the stack is removed')

    run_parser = sub.add_parser('run', help='build, start, test and remove a stack')
    stack_options(run_parser)
    run_parser.add_argument('--timeout-minutes', type=int, default=45)
    up_parser = sub.add_parser('up', help='start a stack and leave it running')
    stack_options(up_parser)
    test_parser = sub.add_parser('test', help='run the suite against a running stack or an external deployment')
    test_parser.add_argument('--stack', help='work directory printed by `up`')
    test_parser.add_argument('--external', help='JSON description of an existing deployment (see README.md)')
    test_parser.add_argument('--evidence-dir', help='with --external: where to write evidence')
    test_parser.add_argument('--timeout-minutes', type=int, default=45)
    down_parser = sub.add_parser('down', help='remove a stack started by `up`')
    down_parser.add_argument('--stack', required=True)
    down_parser.add_argument('--keep-images', action='store_true')
    args = parser.parse_args(argv)

    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupted)
    try:
        return {'run': command_run, 'up': command_up, 'test': command_test, 'down': command_down}[args.command](args)
    except StackError as error:
        print('error: %s' % error, file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print('interrupted', file=sys.stderr)
        return 130


if __name__ == '__main__':
    sys.exit(main())
