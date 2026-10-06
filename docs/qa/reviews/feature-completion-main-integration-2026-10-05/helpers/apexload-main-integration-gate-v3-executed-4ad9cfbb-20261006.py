import datetime, hashlib, json, os, pathlib, shutil, signal, subprocess, sys, time
root = pathlib.Path('/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner')
qa = root / 'docs/qa/reviews/feature-completion-main-integration-2026-10-05/evidence'
qa.mkdir(parents=True, exist_ok=True)
label, command = sys.argv[1], sys.argv[2:]
log, receipt = qa / (label+'.log'), qa / (label+'.json')
assert not log.exists() and not receipt.exists(), 'refusing to replace retained evidence'

def digest(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def inventory():
    paths=set()
    for folder in ['pkg','cmd','api/proto','scripts','.github/workflows','third_party','dashboard/src','dashboard/e2e','dashboard/scripts','docs/guides']:
        for path in (root/folder).rglob('*'):
            if path.is_file() and not path.is_symlink() and '/static/' not in str(path) and path.suffix in ['.go','.ts','.tsx','.js','.mjs','.css','.md','.mod','.sum','.proto','.py','.sh','.ps1','.yml','.yaml']:
                paths.add(path.relative_to(root).as_posix())
    for rel in ['go.mod','go.sum','pkg/api/apidocs/api-endpoint-catalog.json','third_party/playwright-go/UPSTREAM_MANIFEST.json','third_party/playwright-go/LICENSE','third_party/playwright-go/README.upstream.txt','dashboard/index.html','dashboard/eslint.config.js','.dockerignore','Makefile','Dockerfile','Dockerfile.controller','runners/java_vuser/Dockerfile.supervisor','runners/java_vuser/Dockerfile.broker','runners/java_vuser/Dockerfile.facade','runners/java_vuser/Dockerfile','.github/workflows/ci.yml','.github/workflows/recording-e2e.yml','dashboard/package.json','dashboard/package-lock.json','dashboard/vite.config.ts','dashboard/playwright.config.ts','dashboard/tsconfig.json','dashboard/tsconfig.node.json','docs/swagger.yaml','docs/api-endpoint-catalog.json','docs/help-content-catalog.json','docs/dsl.schema.json','docs/qa/evidence/M1/api-contract-matrix.json','docs/qa/DASHBOARD.md']:
        if (root/rel).is_file(): paths.add(rel)
    return [{'path':rel,'bytes':(root/rel).stat().st_size,'sha256':digest(root/rel)} for rel in sorted(paths)]
def embedded():
    return [{'path':p.relative_to(root).as_posix(),'bytes':p.stat().st_size,'sha256':digest(p)} for p in sorted((root/'pkg/api/static').rglob('*')) if p.is_file()]
before,embed_before=inventory(),embedded();started=datetime.datetime.now(datetime.timezone.utc).isoformat();begin=time.monotonic();disk_before=shutil.disk_usage(root).free
display_command=[arg if len(arg)<=220 else '[bounded argument: '+str(len(arg))+' bytes; sha256 '+hashlib.sha256(arg.encode()).hexdigest()+']' for arg in command]
print(json.dumps({'label':label,'running':True,'command':display_command,'free_bytes_before':disk_before}),flush=True)
class GateInterrupted(BaseException): pass
cancelled_signal = None
process = None
lifecycle_errors = []
child_group_empty = False
child_physically_joined = False
cleanup_escalated = False

def interrupt(signum, _frame):
    global cancelled_signal
    cancelled_signal = signum
    # Record cancellation without interrupting Popen ownership registration.

def owned_group_exists():
    try:
        os.killpg(process.pid, 0)
        return True
    except ProcessLookupError:
        return False

def signal_owned_group(signum):
    try:
        os.killpg(process.pid, signum)
    except ProcessLookupError:
        pass

def raise_if_cancelled():
    if cancelled_signal is not None:
        raise GateInterrupted('received signal ' + str(cancelled_signal))

def wait_owned_child():
    while True:
        raise_if_cancelled()
        try:
            result = process.wait(timeout=0.25)
            raise_if_cancelled()
            return result
        except subprocess.TimeoutExpired:
            pass

signal.signal(signal.SIGINT, interrupt)
signal.signal(signal.SIGTERM, interrupt)
with log.open('wb') as output:
    try:
        raise_if_cancelled()
        process = subprocess.Popen(command, cwd=root, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        print(json.dumps({'label': label, 'owned_child_pid': process.pid, 'owned_child_process_group': process.pid}), flush=True)
        code = wait_owned_child()
        child_physically_joined = True
    except GateInterrupted:
        # The inner UI wrapper forwards to and joins its separately owned profiles.
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if process is not None:
            signal_owned_group(cancelled_signal)
            try:
                process.wait(timeout=45)
                child_physically_joined = True
            except subprocess.TimeoutExpired:
                cleanup_escalated = True
                signal_owned_group(signal.SIGKILL)
                try:
                    process.wait(timeout=5)
                    child_physically_joined = True
                except subprocess.TimeoutExpired:
                    lifecycle_errors.append('owned direct child failed to join after escalation')
        code = 128 + cancelled_signal
    except BaseException as error:
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        lifecycle_errors.append(type(error).__name__ + ': ' + str(error))
        if process is not None and process.poll() is None:
            signal_owned_group(signal.SIGTERM)
            try:
                process.wait(timeout=45)
            except subprocess.TimeoutExpired:
                cleanup_escalated = True
                signal_owned_group(signal.SIGKILL)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    lifecycle_errors.append('owned direct child failed to join after escalation')
        child_physically_joined = process is not None and process.poll() is not None
        code = 1
    finally:
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if process is not None:
            deadline = time.monotonic() + 5
            while owned_group_exists() and time.monotonic() < deadline:
                time.sleep(0.05)  # Bounded cleanup polling only; no test timing change.
            child_group_empty = not owned_group_exists()
            if not child_group_empty:
                lifecycle_errors.append('owned child process group still present; no unrelated group was signalled')
after,embed_after=inventory(),embedded()
before_hashes={x['path']:x['sha256'] for x in before};after_hashes={x['path']:x['sha256'] for x in after}
changed=[p for p in sorted(set(before_hashes)|set(after_hashes)) if before_hashes.get(p)!=after_hashes.get(p)]
allowed_source=os.environ.get('APEX_GATE_ALLOW_SOURCE_CHANGES')=='1';allowed_embed=os.environ.get('APEX_GATE_ALLOW_EMBED_CHANGES')=='1'
keys=['TMPDIR','APEXLOAD_IMPORT_ASSET_DIR','GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS','NODE_OPTIONS','APEXLOAD_VITEST_MAX_WORKERS','APEXLOAD_E2E_PORT','APEXLOAD_E2E_AUTH_PORT','APEXLOAD_E2E_PROFILE','APEXLOAD_E2E_OUTPUT_ROOT','APEXLOAD_BROWSER_TESTS','APEXLOAD_ANALYSIS_MEMORY_LIMIT','APEXLOAD_ANALYSIS_SPILL_LIMIT','TZ']
data={'schema':'apexload.composed-review-gate.v1','scope':'Gate against the current composed owned integration worktree; source/embedded inventories are repository inputs, not all transitive/native runtime files. Process completion recorded only after subprocess join. Qualification is limited to the named command.','label':label,'command':command,'helper_path':str(pathlib.Path(__file__).resolve()),'helper_sha256':digest(pathlib.Path(__file__)),'cwd':str(root),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'integrating_remote_main':'da1d6dd00ffdd250c630de7a73328a08040571a1','started_utc':started,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'duration_seconds':round(time.monotonic()-begin,3),'exit_code':code,'execution_lifecycle':{'owned_child_pid':process.pid if process is not None else None,'owned_child_process_group':process.pid if process is not None else None,'owned_child_physically_joined':child_physically_joined,'owned_child_group_empty':child_group_empty,'cancelled_signal':cancelled_signal,'cleanup_escalated':cleanup_escalated,'errors':lifecycle_errors,'scope':'Only this gate-owned direct child/process group; the inner UI wrapper separately owns and joins its profile groups. No claim about unrelated processes.'},'environment':{k:os.getenv(k) for k in keys},'postgres_fixture_configured':bool(os.getenv('APEXLOAD_TEST_POSTGRES_URL')),'free_bytes_before':disk_before,'free_bytes_after':shutil.disk_usage(root).free,'source_before':before,'source_after':after,'source_changed_paths':changed,'source_changes_expected':allowed_source,'embed_before':embed_before,'embed_after':embed_after,'embed_changes_expected':allowed_embed,'log_bytes':log.stat().st_size,'log_sha256':digest(log)}
receipt.write_text(json.dumps(data,indent=2)+'\n')
print(json.dumps({k:(display_command if k=='command' else v) for k,v in data.items() if k not in ['source_before','source_after','embed_before','embed_after']}),flush=True)
print('\n'.join(log.read_text(errors='replace').splitlines()[-35:]),flush=True)
sys.exit(code or (97 if lifecycle_errors else 95 if changed and not allowed_source else 96 if embed_before!=embed_after and not allowed_embed else 0))
