import datetime,hashlib,json,os,pathlib,subprocess,sys,time
root=pathlib.Path('/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner');qa=root/'docs/qa/reviews/feature-completion-browser-frame-isolation-2026-10-05/evidence';qa.mkdir(parents=True,exist_ok=True)
label=sys.argv[1];assert label and all(c in 'abcdefghijklmnopqrstuvwxyz0123456789-_' for c in label)
log=qa/(label+'.stdout.txt');receipt=qa/(label+'.json');assert not log.exists() and not receipt.exists()
def snapshot():
 names=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=root).split(b'\0');paths=[n.decode() for n in sorted(set(names)) if n and (n.decode().endswith('.go') or n.decode() in ['go.mod','go.sum']) and (root/n.decode()).is_file()]
 rows=[{'path':n,'sha256':hashlib.sha256((root/n).read_bytes()).hexdigest()} for n in paths];raw=(json.dumps(rows,sort_keys=True,separators=(',',':'))+'\n').encode();digest=hashlib.sha256(raw).hexdigest();dest=qa/('frame-isolation-go-inventory-'+digest+'.json')
 if dest.exists():assert dest.read_bytes()==raw
 else:dest.write_bytes(raw)
 return {'file':dest.name,'sha256':digest,'file_count':len(rows)}
cmd=sys.argv[2:];before=snapshot();start=time.monotonic();utc=datetime.datetime.now(datetime.timezone.utc).isoformat();print(json.dumps({'running':label,'scope':'Neutral frame isolation and actual no-interaction oracle, no extended HAR owner/ledger'}),flush=True)
with log.open('xb') as stream:code=subprocess.run(cmd,cwd=root,stdout=stream,stderr=subprocess.STDOUT).returncode
raw=log.read_bytes();after=snapshot();d={'scope':'Neutral stale-frame/live-sibling finalization isolation only; no full browser/HAR or program qualification','command':cmd,'cwd':str(root),'started_at_utc':utc,'duration_seconds':round(time.monotonic()-start,3),'exit_code':code,'raw_log':log.name,'raw_log_sha256':hashlib.sha256(raw).hexdigest(),'raw_log_bytes':len(raw),'source_before':before,'source_after':after,'source_changed':before!=after,'native_test_mode':os.getenv('APEXLOAD_BROWSER_TESTS'),'helper_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'environment':{k:os.getenv(k) for k in ['GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS']}}
receipt.write_text(json.dumps(d,indent=2)+'\n');print(json.dumps({k:d[k] for k in ['duration_seconds','exit_code','raw_log_sha256','source_changed']}),flush=True);print(raw[-1400:].decode(errors='replace'));raise SystemExit(code)
