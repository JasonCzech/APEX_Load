import datetime,hashlib,json,os,signal,subprocess,sys,time
from pathlib import Path
root=Path('/Users/jasonczech/.codex/worktrees/browser-document-finality/Project_Stormrunner')
name=sys.argv[1];args=sys.argv[2:]
tracked=subprocess.check_output(['git','ls-files','-co','--exclude-standard'],cwd=root,text=True).splitlines()
paths=sorted(set(p for p in tracked if p.endswith('.go') or p in ['go.mod','go.sum']))
def inventory():
 return [{'path':p,'sha256':hashlib.sha256((root/p).read_bytes()).hexdigest(),'bytes':(root/p).stat().st_size} for p in paths if (root/p).is_file()]
before=inventory();start=time.monotonic();log=Path('/private/tmp')/(name+'.log')
with log.open('wb') as stream:
 child=subprocess.Popen(args,cwd=root,stdout=stream,stderr=subprocess.STDOUT,env=os.environ,start_new_session=True)
 try: code=child.wait(timeout=1800)
 except subprocess.TimeoutExpired:
  os.killpg(child.pid,signal.SIGTERM)
  try: child.wait(timeout=15)
  except subprocess.TimeoutExpired: os.killpg(child.pid,signal.SIGKILL);child.wait()
  code=124
elapsed=time.monotonic()-start;after=inventory()
out={'name':name,'argv':args,'cwd':str(root),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'exit_code':code,'elapsed_seconds':round(elapsed,3),'recorded_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'log_path':str(log),'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest(),'source_scope':'Every repository Go source currently listed as tracked/nonignored untracked plus go.mod and go.sum; excludes module cache/native runtime dependencies, so not a complete transitive build inventory.','source_count':len(before),'source_unchanged':before==after,'sources_before':before,'sources_after':after,'environment':{key:os.environ.get(key) for key in ['GOCACHE','GOMAXPROCS','GOMEMLIMIT','GOGC','GOFLAGS']}}
receipt=Path('/private/tmp')/(name+'.json');receipt.write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps({k:out[k] for k in ['name','exit_code','elapsed_seconds','log_path','log_sha256','source_count','source_unchanged']}))
print('receipt_path='+str(receipt))
sys.exit(code)
