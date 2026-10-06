import datetime, hashlib, json, os, pathlib, subprocess, sys, time
root=pathlib.Path('/Users/jasonczech/.codex/worktrees/authoring-content-revision/Project_Stormrunner')
qa=root/'docs/qa/reviews/feature-completion-import-publication-2026-10-05/evidence'
label=sys.argv[1]; assert label and all(c in 'abcdefghijklmnopqrstuvwxyz0123456789-_' for c in label)
log=qa/(label+'.stdout.txt'); receipt=qa/(label+'.json'); assert not log.exists() and not receipt.exists()
def snapshot():
 names=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=root).split(b'\0')
 paths=[n.decode() for n in sorted(set(names)) if n and (n.decode().endswith('.go') or n.decode() in ['go.mod','go.sum']) and (root/n.decode()).is_file()]
 rows=[{'path':p,'sha256':hashlib.sha256((root/p).read_bytes()).hexdigest()} for p in paths]
 raw=(json.dumps(rows,sort_keys=True,separators=(',',':'))+'\n').encode(); digest=hashlib.sha256(raw).hexdigest();p=qa/('publication-go-inventory-'+digest+'.json')
 if p.exists(): assert p.read_bytes()==raw
 else: p.write_bytes(raw)
 return {'file':p.name,'sha256':digest,'file_count':len(rows)}
def artifacts():
 paths=['docs/swagger.yaml','docs/api-endpoint-catalog.json','pkg/api/apidocs/swagger.yaml','pkg/api/apidocs/api-endpoint-catalog.json','dashboard/src/types/generated/openapi.ts']
 return [{'path':p,'sha256':hashlib.sha256((root/p).read_bytes()).hexdigest()} for p in paths if (root/p).is_file()]
cmd=sys.argv[2:];before=snapshot();artifact_before=artifacts();start=time.monotonic();utc=datetime.datetime.now(datetime.timezone.utc).isoformat();print(json.dumps({'running':label,'command':cmd}),flush=True);code=130
try:
 with log.open('xb') as stream: code=subprocess.run(cmd,cwd=root,stdout=stream,stderr=subprocess.STDOUT).returncode
except KeyboardInterrupt: pass
raw=log.read_bytes();after=snapshot();v={'scope':'dedicated durable import publication primitive and existing claims/backfill paths; source-aware scenario admission, public API/UI support and composed merge acceptance remain separate','command':cmd,'cwd':str(root),'started_at':utc,'finished_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'duration_seconds':round(time.monotonic()-start,3),'exit_code':code,'status':'interrupted-not-acceptance' if code==130 else 'passed' if code==0 else 'failed','raw_log':log.name,'raw_log_bytes':len(raw),'raw_log_sha256':hashlib.sha256(raw).hexdigest(),'source_before':before,'source_after':after,'source_changed':before['sha256']!=after['sha256'],'generated_before':artifact_before,'generated_after':artifacts(),'helper_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'environment':{k:os.getenv(k) for k in ['GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS','NODE_OPTIONS']}}
receipt.write_text(json.dumps(v,indent=2)+'\n');print(json.dumps({k:v[k] for k in ['duration_seconds','exit_code','raw_log_sha256','source_changed']}));print(raw[-2200:].decode(errors='replace'));raise SystemExit(code)
