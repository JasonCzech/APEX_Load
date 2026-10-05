import datetime, hashlib, json, os, pathlib, re, selectors, signal, subprocess, sys, time
root=pathlib.Path('/Users/jasonczech/.codex/worktrees/kafka-owned-seek/Project_Stormrunner')
qa=root/'docs/qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence'
label=sys.argv[1]; command=sys.argv[2:]
if not re.fullmatch(r'[a-z0-9-]{1,80}',label) or not command: raise SystemExit('invalid evidence label/command')
qa.mkdir(parents=True,exist_ok=True)
log=qa/(label+'.log'); receipt_path=qa/(label+'.json')
if log.exists() or log.is_symlink() or receipt_path.exists() or receipt_path.is_symlink(): raise SystemExit('evidence destination already exists')
paths=sorted({root/p for p in ['go.mod','go.sum','scripts/run-optin-suites.sh','scripts/kafka_optin_test.go','scripts/test-kafka-owned-seek.sh','scripts/test-kafka-transactions.sh','scripts/kafka-management-fixture.py','scripts/test_kafka_management_fixture.py','pkg/convert/enterprisearchive/optin_suite_inventory_test.go','docs/qa/TEST_PROGRAM.md']})
source={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
started=datetime.datetime.now(datetime.timezone.utc).isoformat(); begin=time.monotonic()
print(json.dumps({'label':label,'status':'running','command':command}),flush=True)
limit=16<<20; written=0; over_limit=False; timed_out=False
with log.open('xb') as out:
    process=subprocess.Popen(command,cwd=root,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,start_new_session=True)
    selector=selectors.DefaultSelector(); selector.register(process.stdout,selectors.EVENT_READ)
    while selector.get_map():
        if time.monotonic()-begin>900:
            timed_out=True
            try: os.killpg(process.pid,signal.SIGKILL)
            except ProcessLookupError: pass
        for key,_ in selector.select(1):
            body=os.read(key.fileobj.fileno(),65536)
            if not body:
                selector.unregister(key.fileobj); continue
            allowed=max(0,limit-written); out.write(body[:allowed]); written+=min(allowed,len(body))
            if len(body)>allowed:
                over_limit=True
                try: os.killpg(process.pid,signal.SIGKILL)
                except ProcessLookupError: pass
    code=process.wait(timeout=5)
    process.stdout.close(); selector.close()
if over_limit or timed_out: code=1
raw=log.read_bytes()
keys=['GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS']
receipt={'label':label,'command':command,'cwd':str(root),'started_utc':started,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'duration_seconds':round(time.monotonic()-begin,3),'exit_code':code,'output_limit_bytes':limit,'output_limit_exceeded':over_limit,'deadline_exceeded':timed_out,'profile':{key:os.getenv(key) for key in keys},'source_sha256':source,'log_bytes':len(raw),'log_sha256':hashlib.sha256(raw).hexdigest()}
with receipt_path.open('x') as out: out.write(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({k:v for k,v in receipt.items() if k!='source_sha256'}),flush=True)
print('\n'.join(raw.decode(errors='replace').splitlines()[-25:]),flush=True)
sys.exit(code)
