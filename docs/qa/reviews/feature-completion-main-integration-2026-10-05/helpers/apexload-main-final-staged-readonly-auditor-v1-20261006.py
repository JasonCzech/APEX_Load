#!/usr/bin/env python3
"""Read only repository metadata/bytes; write only create-only private JSON receipts."""
import argparse, datetime, hashlib, json, os, pathlib, re, stat, subprocess
ROOT = pathlib.Path('/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner')
PLAN = pathlib.Path('/private/tmp/apexload-main-final-staging-allowset-audit-plan-v2-20261006.json')
PRIOR = pathlib.Path('/private/tmp/apexload-main-resume-readonly-stage-preparation-20261006.json')
PLAN_SHA = '3e050fff6aab29fb41a5209d908dbe49ab71887216f23cc4d0199908568c2a66'
PRIOR_SHA = 'e2ab5c4f1f3593878068122e6521a5fecf57faa7c0d443d29730add6a51c3a3d'
MAX_BLOB = 100 * 1024 * 1024

def digest(data): return hashlib.sha256(data).hexdigest()
def read_json(path, expected=None):
    p=pathlib.Path(path); raw=p.read_bytes()
    if expected is not None: assert digest(raw)==expected, str(p)
    return json.loads(raw)
def git(*args):
    return subprocess.check_output(['git','--no-optional-locks',*args],cwd=ROOT,env={**os.environ,'GIT_OPTIONAL_LOCKS':'0','LC_ALL':'C'})
def regular(path):
    relative=pathlib.Path(path)
    assert not relative.is_absolute() and ".." not in relative.parts and relative.parts, path
    p=ROOT/relative
    for parent in p.parents:
        if parent==ROOT: break
        assert not parent.is_symlink(), str(parent)
    st=p.lstat(); assert stat.S_ISREG(st.st_mode) and not p.is_symlink(), path
    return p,st

def snapshot(path, blob=False):
    p,st=regular(path); a=hashlib.sha256(); b=hashlib.sha1()
    b.update(('blob '+str(st.st_size)+'\0').encode())
    with p.open('rb') as f:
        while True:
            data=f.read(1024*1024)
            if not data:break
            a.update(data); b.update(data)
    assert p.stat().st_size==st.st_size, path
    d={'path':path,'bytes':st.st_size,'sha256':a.hexdigest(),'working_mode':f'{stat.S_IMODE(st.st_mode):04o}'}
    if blob: d.update(git_blob=b.hexdigest(),git_mode='100755' if st.st_mode&0o111 else '100644')
    return d

def inventory(prefix):
    p=ROOT/prefix; assert p.is_dir() and not p.is_symlink(),prefix
    result=[]
    for folder,dirs,files in os.walk(p,followlinks=False):
        for name in dirs:assert not (pathlib.Path(folder)/name).is_symlink(),str(pathlib.Path(folder)/name)
        for name in files: result.append(snapshot(str((pathlib.Path(folder)/name).relative_to(ROOT))))
    return sorted(result,key=lambda x:x['path'])

def create_private(path,value):
    p=pathlib.Path(path);assert p.parent==pathlib.Path('/private/tmp') and p.parent.is_dir(),str(p)
    with p.open('x') as f:json.dump(value,f,indent=2);f.write('\n')
    print(json.dumps({'path':str(p),'bytes':p.stat().st_size,'sha256':digest(p.read_bytes())},indent=2))

def check_gate(path,role,plan):
    p=pathlib.Path(path);d=read_json(p);assert d['exit_code']==0, str(p)
    assert d['cwd']==str(ROOT) and d['head']==plan['expected_head'] and d['integrating_remote_main']==plan['expected_merge_head']
    assert d['source_changed_paths']==[] and d['source_before']==d['source_after'],str(p)
    life=d['execution_lifecycle'];assert life['owned_child_physically_joined'] and life['owned_child_group_empty'] and life['cancelled_signal'] is None and not life['errors'],str(p)
    cmd=d['command']
    if role=='go':
        assert cmd[:2]==['go','test'] and './...' in cmd and '-json' in cmd and '-count=1' in cmd and '-timeout=45m' in cmd,cmd
        assert d['environment']['APEXLOAD_BROWSER_TESTS']=='require' and d['embed_before']==d['embed_after'],str(p)
    else:
        assert cmd==['npm','--prefix','dashboard','run','test:cycle'],cmd
    log=p.with_suffix('.log');st=log.stat();assert st.st_size==d['log_bytes'],str(log)
    a=hashlib.sha256()
    with log.open('rb') as f:
        while True:
            data=f.read(1024*1024)
            if not data:break
            a.update(data)
    assert a.hexdigest()==d['log_sha256'],str(log)
    for item in d['source_after']:
        actual=snapshot(item['path']);assert actual['bytes']==item['bytes'] and actual['sha256']==item['sha256'],item['path']
    return d,{'role':role,'path':str(p),'receipt_sha256':digest(p.read_bytes()),'label':d['label'],'command':cmd,'log_bytes':st.st_size,'log_sha256':a.hexdigest(),'physical_join_verified':True}

def main():
    a=argparse.ArgumentParser();a.add_argument('--freeze-qa',metavar='PRIVATE_JSON');a.add_argument('--qa-manifest');a.add_argument('--go-gate');a.add_argument('--ui-gate');a.add_argument('--output');args=a.parse_args()
    plan=read_json(PLAN,PLAN_SHA);prior=read_json(PRIOR,PRIOR_SHA)
    head=git('rev-parse','HEAD').decode().strip(); merge=git('rev-parse','MERGE_HEAD').decode().strip()
    assert head==plan['expected_head'] and merge==plan['expected_merge_head'];assert not git('ls-files','-u')
    if args.freeze_qa:
        assert not any([args.qa_manifest,args.go_gate,args.ui_gate,args.output])
        create_private(args.freeze_qa,{'schema':'apexload.main-integration.final-owned-QA-inventory.v1','at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'root':str(ROOT),'HEAD':head,'MERGE_HEAD':merge,'prefix':plan['QA_prefix'],'files':inventory(plan['QA_prefix']),'scope':'Metadata freeze only. No tests, staged qualification or Git writes claimed.'});return
    assert all([args.qa_manifest,args.go_gate,args.ui_gate,args.output])
    assert git('rev-parse','--show-object-format').decode().strip()=='sha1'
    index_before=git('ls-files','--stage','-z');status_before=git('status','--porcelain=v1','-z','--untracked-files=all');raw_before=git('diff','--cached','--raw','-z','--no-renames')
    assert not git('diff','--name-only','-z'), 'Unstaged tracked changes remain'
    q=read_json(args.qa_manifest);assert q['root']==str(ROOT) and q['prefix']==plan['QA_prefix'] and q['HEAD']==head and q['MERGE_HEAD']==merge
    currentqa=inventory(plan['QA_prefix']);assert currentqa==q['files'],'Final QA bytes/modes changed since freeze'
    go,goev=check_gate(args.go_gate,'go',plan);ui,uiev=check_gate(args.ui_gate,'ui',plan)
    assert go['source_after']==ui['source_after'], 'Full gates qualified different repository source'
    assert go['embed_after']==ui['embed_after'],'Full gates qualified different embedded output'
    embed=inventory('pkg/api/static')
    assert [{k:x[k] for k in ['path','bytes','sha256']} for x in embed]==ui['embed_after'],'Final embedded output differs from qualified UI build'
    runtime_before=[snapshot(x['path']) for x in plan['reviewed_current_runtime_UI_hashes']]
    assert runtime_before==plan['reviewed_current_runtime_UI_hashes']
    for x in prior['generated_exact_generation_output']+prior['prior_overlap_review_exact_current']:
        assert snapshot(x['path'])==x,x['path']
    index={}
    for row in index_before.rstrip(b'\0').split(b'\0'):
        info,path=row.split(b'\t',1);mode,oid,stage=info.decode().split();assert stage=='0';index[path.decode()]=(mode,oid)
    qpaths={x['path'] for x in currentqa};epaths={x['path'] for x in embed};allowed=set(plan['base_allowed_non_QA_paths'])|qpaths|epaths|set(plan['head_remote_index_embedded_paths_for_exact_deletion_checks'])
    staged=git('diff','--cached','--name-status','-z','--no-renames').rstrip(b'\0').split(b'\0');changed=[]
    assert len(staged)%2==0
    for i in range(0,len(staged),2):
        code=staged[i].decode();path=staged[i+1].decode();assert path in allowed,path
        if code=='D':assert path not in index and not os.path.lexists(ROOT/path),path;changed.append({'path':path,'change':'deleted'});continue
        assert code in ['A','M','T'],(code,path);x=snapshot(path,blob=True);assert x['bytes']<MAX_BLOB,path
        assert index[path]==(x['git_mode'],x['git_blob']),path;changed.append({**x,'change':code})
    for x in prior['incoming_only_git_object_matches']:
        assert index[x['path']]==(x['git_mode'],x['git_blob']),x['path']
    for p in qpaths|epaths:
        x=snapshot(p,blob=True);assert p in index and index[p]==(x['git_mode'],x['git_blob']),p
    assert {p for p in index if p.startswith(plan['QA_prefix'])}==qpaths
    assert {p for p in index if p.startswith('pkg/api/static/')}==epaths
    supplement=plan['unrelated_supplement'];s=snapshot(supplement['path']);assert s['sha256']==supplement['sha256'] and s['bytes']==supplement['bytes'] and s['path'] not in index
    untracked=git('ls-files','--others','--exclude-standard','-z').rstrip(b'\0').split(b'\0');assert untracked==[s['path'].encode()],untracked
    markers=[]
    pattern=re.compile(rb'(?m)^(?:<{7}(?: |$)|={7}$|>{7}(?: |$))')
    for x in go['source_after']:
        if pattern.search((ROOT/x['path']).read_bytes()):markers.append(x['path'])
    assert not markers,markers
    assert git('ls-files','--stage','-z')==index_before and git('status','--porcelain=v1','-z','--untracked-files=all')==status_before and git('diff','--cached','--raw','-z','--no-renames')==raw_before
    assert [snapshot(x['path']) for x in plan['reviewed_current_runtime_UI_hashes']]==runtime_before
    assert inventory(plan['QA_prefix'])==currentqa
    assert git('rev-parse','HEAD').decode().strip()==head and git('rev-parse','MERGE_HEAD').decode().strip()==merge
    create_private(args.output,{'schema':'apexload.main-integration.final-staged-readonly-audit.v1','at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'reviewer':'/root/resume_integration_audit','root':str(ROOT),'HEAD':head,'MERGE_HEAD':merge,'result':'Exact staged source/embedded/owned-QA scope clean, qualified inputs unchanged; ordinary merge commit/push remains separate authorized operation.','plan_sha256':PLAN_SHA,'prior_resume_audit_sha256':PRIOR_SHA,'helper_sha256':digest(pathlib.Path(__file__).read_bytes()),'qualified_gates':[goev,uiev],'runtime_UI_hashes':runtime_before,'incoming_only_index_exact_count':len(prior['incoming_only_git_object_matches']),'owned_QA_file_count':len(currentqa),'embedded_file_count':len(embed),'staged_changed_path_count':len(changed),'staged_paths':changed,'index_bookends_sha256':digest(index_before),'status_bookends_sha256':digest(status_before),'staged_raw_bookends_sha256':digest(raw_before),'excluded_untracked_supplement':s,'conflict_marker_files':markers,'findings':[],'execution':'Read-only Git metadata/bytes/hashes, no tests/builds/source/Git/cache mutations. Create-only private output; no private HAR content copied.'})
if __name__=='__main__':main()
