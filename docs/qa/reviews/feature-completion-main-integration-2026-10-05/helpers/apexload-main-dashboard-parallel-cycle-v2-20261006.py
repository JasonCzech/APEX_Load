import datetime, hashlib, json, os, pathlib, shutil, signal, subprocess, tempfile, time

root = pathlib.Path('/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner')
qa = root / 'docs/qa/reviews/feature-completion-main-integration-2026-10-05'
evidence = qa / 'evidence'
receipt = evidence / 'dashboard-cycle-2-parallel-profile-outcomes.json'
assert not receipt.exists()
profiles = [('dev-admin', '18471'), ('local-auth', '18472')]
output = qa / 'playwright/full-cycle-2'
for profile, _ in profiles:
    assert not (evidence / ('dashboard-cycle-2-' + profile + '.log')).exists()
    assert not (output / profile).exists(), 'refusing to replace retained profile artifacts'
jobs = []
stages = []
resources = []
errors = []
cancelled_signal = None
wrapper_failed = False

def now(): return datetime.datetime.now(datetime.timezone.utc).isoformat()
def digest(path): return hashlib.sha256(path.read_bytes()).hexdigest()
class WrapperInterrupted(BaseException): pass

def interrupt(signum, _frame):
    global cancelled_signal
    cancelled_signal = signum
    # Do not raise between Popen returning and ownership registration.

signal.signal(signal.SIGINT, interrupt)
signal.signal(signal.SIGTERM, interrupt)

def raise_if_cancelled():
    if cancelled_signal is not None:
        raise WrapperInterrupted('received signal ' + str(cancelled_signal))

def spawn(job, **kwargs):
    raise_if_cancelled()
    job['process'] = subprocess.Popen(job['command'], cwd=root, start_new_session=True, **kwargs)
    job['owned_process_group_id'] = job['process'].pid
    jobs.append(job)
    raise_if_cancelled()
    return job

def wait_owned_child(process):
    while True:
        raise_if_cancelled()
        try:
            code = process.wait(timeout=0.25)
            raise_if_cancelled()
            return code
        except subprocess.TimeoutExpired:
            pass

def mark_joined(job):
    job['exit_code'] = job['process'].returncode
    job.setdefault('finished_utc', now())
    job['direct_child_physically_joined'] = job['process'].returncode is not None

def group_exists(job):
    try:
        os.killpg(job['owned_process_group_id'], 0)
        return True
    except ProcessLookupError:
        return False

def settle_joined_group(job):
    deadline = time.monotonic() + 5
    while group_exists(job) and time.monotonic() < deadline:
        raise_if_cancelled()
        time.sleep(0.05)
    job['owned_process_group_empty'] = not group_exists(job)
    if not job['owned_process_group_empty']:
        raise RuntimeError('owned group survived normal child completion: ' + str(job['owned_process_group_id']))

def signal_owned_group(job, signum):
    try:
        os.killpg(job['owned_process_group_id'], signum)
        job.setdefault('cleanup_signals', []).append(signum)
    except ProcessLookupError:
        pass

def join_and_clean_owned_jobs():
    # Repeated signals must not interrupt the physical join or receipt write.
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    active = [job for job in jobs if job['process'].poll() is None]
    for job in active:
        signal_owned_group(job, signal.SIGTERM)
    deadline = time.monotonic() + 25
    for job in active:
        try:
            job['process'].wait(timeout=max(0.01, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            job['cleanup_escalated'] = True
            signal_owned_group(job, signal.SIGKILL)
    kill_deadline = time.monotonic() + 5
    for job in jobs:
        if job['process'].poll() is None:
            try:
                job['process'].wait(timeout=max(0.01, kill_deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                errors.append('owned child did not join: ' + str(job['process'].pid))
        if job['process'].returncode is not None:
            mark_joined(job)
        else:
            job['direct_child_physically_joined'] = False
            job['exit_code'] = None
    # Only inspect/terminate the process groups established by this wrapper.
    survivors = [job for job in jobs if not job.get('owned_process_group_empty', False) and group_exists(job)]
    for job in survivors:
        signal_owned_group(job, signal.SIGKILL)
    group_deadline = time.monotonic() + 5
    while survivors and time.monotonic() < group_deadline:
        survivors = [job for job in survivors if group_exists(job)]
        if survivors:
            time.sleep(0.05)  # Bounded cleanup polling; never a test synchronization delay.
    for job in jobs:
        # Once observed empty, never probe/signal an old PGID that could be reused.
        if not job.get('owned_process_group_empty', False):
            job['owned_process_group_empty'] = not group_exists(job)
        if not job['owned_process_group_empty']:
            errors.append('owned process group remains: ' + str(job['owned_process_group_id']))
        stream = job.get('stream')
        if stream is not None:
            stream.close()
            job['log_bytes'] = job['log'].stat().st_size
            job['log_sha256'] = digest(job['log'])
    for resource in resources:
        owner = resource.get('owner')
        safe = owner is None or (owner['direct_child_physically_joined'] and owner['owned_process_group_empty'])
        resource['temporary_directory_removed_after_join'] = safe
        if safe:
            try:
                shutil.rmtree(resource['temp'])
            except OSError as error:
                resource['temporary_directory_removed_after_join'] = False
                errors.append('scratch cleanup failed: ' + str(error))
        else:
            errors.append('retained unjoined scratch directory: ' + str(resource['temp']))
        if owner is not None:
            owner['temporary_directory_removed_after_join'] = resource['temporary_directory_removed_after_join']

print(json.dumps({'phase': 'wrapper_started', 'wrapper_pid': os.getpid(), 'at_utc': now()}), flush=True)
try:
    for name in ['typecheck', 'lint', 'test', 'build']:
        command = ['npm', '--prefix', 'dashboard', 'run', name]
        job = {'kind': 'stage', 'name': name, 'started_utc': now(), 'command': command}
        stages.append(job)
        print(json.dumps({'phase': name, 'started_utc': job['started_utc'], 'command': command}), flush=True)
        spawn(job)
        wait_owned_child(job['process'])
        mark_joined(job)
        settle_joined_group(job)
        if job['exit_code']:
            raise RuntimeError('dashboard stage failed: ' + name)
    print(json.dumps({'phase': 'embedded_build_complete', 'at_utc': now()}), flush=True)
    for profile, port in profiles:
        temp = pathlib.Path(tempfile.mkdtemp(prefix='apexload-main-ui2-' + profile + '-', dir='/private/tmp'))
        resource = {'temp': temp}
        resources.append(resource)
        temp.chmod(0o700)
        imports = temp / 'imports'
        imports.mkdir(mode=0o700)
        log = evidence / ('dashboard-cycle-2-' + profile + '.log')
        stream = log.open('xb')
        env = dict(os.environ, APEXLOAD_E2E_PROFILE=profile, APEXLOAD_E2E_PORT=port, TMPDIR=str(temp), APEXLOAD_IMPORT_ASSET_DIR=str(imports), APEXLOAD_E2E_OUTPUT_ROOT=str(output))
        job = {'kind': 'profile', 'profile': profile, 'port': port, 'temporary_directory': str(temp), 'import_asset_directory': str(imports), 'output_root': str(output), 'log_path': str(log), 'started_utc': now(), 'command': ['scripts/run-e2e.sh'], 'stream': stream, 'log': log}
        resource['owner'] = job
        try:
            spawn(job, env=env, stdout=stream, stderr=subprocess.STDOUT)
        except BaseException:
            if 'process' not in job:
                stream.close()
                resource.pop('owner')
            raise
        print(json.dumps({'phase': 'profile_started', 'profile': profile, 'port': port, 'temporary_directory': str(temp), 'owned_process_group_id': job['owned_process_group_id']}), flush=True)
    for job in [x for x in jobs if x['kind'] == 'profile']:
        wait_owned_child(job['process'])
        mark_joined(job)
        settle_joined_group(job)
        print(json.dumps({'phase': 'profile_joined', 'profile': job['profile'], 'exit_code': job['exit_code'], 'finished_utc': job['finished_utc']}), flush=True)
except BaseException as error:
    wrapper_failed = True
    errors.append(type(error).__name__ + ': ' + str(error))
    print(json.dumps({'phase': 'wrapper_failure', 'error_type': type(error).__name__, 'message': str(error)}), flush=True)
finally:
    join_and_clean_owned_jobs()
    for job in jobs:
        for key in ['process', 'stream', 'log']:
            job.pop(key, None)
    profile_jobs = [x for x in jobs if x['kind'] == 'profile']
    data = {'schema': 'apexload.complete-dashboard-cycle-parallel-profiles.v2', 'finished_utc': now(), 'scope': 'Same typecheck, lint, full unit, embed build and unfiltered two-profile run-e2e commands as test:cycle; only physically isolated profile scheduling is concurrent. No retries, test filters or acceptance relaxations. Join claims concern only owned children/process groups and existing script-owned cleanup, never unrelated processes.', 'wrapper_sha256': digest(pathlib.Path(__file__)), 'wrapper_pid': os.getpid(), 'cancelled_signal': cancelled_signal, 'stages': stages, 'profiles': profile_jobs, 'all_owned_direct_children_physically_joined': all(x['direct_child_physically_joined'] for x in jobs), 'all_owned_process_groups_empty': all(x['owned_process_group_empty'] for x in jobs), 'errors': errors, 'failed': wrapper_failed or cancelled_signal is not None or bool(errors) or any(x.get('exit_code') != 0 for x in jobs) or len(stages) != 4 or len(profile_jobs) != 2, 'build_test_cache_cleanup_deferred_until_all_session_jobs_complete': True}
    with receipt.open('x') as output_file:
        json.dump(data, output_file, indent=2)
        output_file.write('\n')
    print(json.dumps({'phase': 'cycle_joined', 'failed': data['failed'], 'receipt': str(receipt)}), flush=True)
raise SystemExit((128 + cancelled_signal) if cancelled_signal else (1 if data['failed'] else 0))
