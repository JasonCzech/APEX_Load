import datetime, hashlib, json, os, pathlib, subprocess, sys, time
root=pathlib.Path('/Users/jasonczech/.codex/worktrees/kafka-owned-seek/Project_Stormrunner')
qa=root/'docs/qa/reviews/feature-completion-browser-resources-2026-10-05/evidence'
qa.mkdir(parents=True,exist_ok=True)
scope=os.getenv('APEXLOAD_RESOURCE_GATE_SCOPE','all')
assert scope in ['all','protocol','storage','backend']
label=sys.argv[1]
assert label and all(c in 'abcdefghijklmnopqrstuvwxyz0123456789-_' for c in label)
log=qa/(label+'.stdout.txt'); receipt=qa/(label+'.json')
assert not log.exists() and not receipt.exists()
def inventory():
 names=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=root).split(b'\0')
 files=[{'path':n.decode(),'sha256':hashlib.sha256((root/n.decode()).read_bytes()).hexdigest()} for n in sorted(set(names)) if n and (n.decode().endswith(('.go','.proto','.js','.mjs','.ts','.tsx','.css')) or n.decode() in ['go.mod','go.sum','dashboard/package.json','dashboard/package-lock.json']) and not n.decode().startswith(('docs/qa/','pkg/api/static/')) and (root/n.decode()).is_file() and (scope=='all' or n.decode() in ['go.mod','go.sum'] or scope=='backend' and n.decode().endswith(('.go','.proto')) or scope=='protocol' and n.decode().startswith('pkg/protocol/') and '/' not in n.decode()[len('pkg/protocol/'):] or scope=='storage' and n.decode().startswith('pkg/storage/') and '/' not in n.decode()[len('pkg/storage/'):])]
 raw=(json.dumps(files,sort_keys=True,separators=(',',':'))+'\n').encode()
 digest=hashlib.sha256(raw).hexdigest(); path=qa/('source-inventory-'+digest+'.json')
 if path.exists(): assert path.read_bytes()==raw
 else: path.write_bytes(raw)
 return {'file':path.name,'sha256':digest,'file_count':len(files)}
cmd=sys.argv[2:];before=inventory();start=time.monotonic();utc=datetime.datetime.now(datetime.timezone.utc).isoformat()
print(json.dumps({'running':label,'command':cmd}),flush=True)
code=130
try:
 with log.open('xb') as stream: code=subprocess.run(cmd,cwd=root,stdout=stream,stderr=subprocess.STDOUT).returncode
except KeyboardInterrupt:
 pass
raw=log.read_bytes();after=inventory()
v={'qualified_source_scope':scope,'receipt_helper_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'command':cmd,'cwd':str(root),'started_at':utc,'finished_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'duration_seconds':round(time.monotonic()-start,3),'exit_code':code,'status':'interrupted-not-acceptance' if code==130 else 'passed' if code==0 else 'failed','raw_log':log.name,'raw_log_bytes':len(raw),'raw_log_sha256':hashlib.sha256(raw).hexdigest(),'source_before':before,'source_after':after,'source_changed':before['sha256']!=after['sha256'],'environment':{k:os.getenv(k) for k in ['GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOFLAGS','NODE_OPTIONS','APEXLOAD_VITEST_MAX_WORKERS','APEXLOAD_BROWSER_TESTS','TZ','PLAYWRIGHT_BROWSERS_PATH']}}
receipt.write_text(json.dumps(v,indent=2)+'\n')
print(json.dumps({k:v[k] for k in ['duration_seconds','exit_code','raw_log_sha256','source_changed']}));print(raw[-8000:].decode(errors='replace'))
raise SystemExit(code)
