import hashlib,json,pathlib,subprocess
root=pathlib.Path('/Users/jasonczech/.codex/worktrees/docs-main-integration/Project_Stormrunner')
names=set(subprocess.check_output(['git','diff','--name-only','HEAD'],cwd=root,text=True).splitlines())|set(subprocess.check_output(['git','ls-files','--others','--exclude-standard'],cwd=root,text=True).splitlines())
names.update(str(p.relative_to(root)) for p in (root/'pkg/api/static').rglob('*') if p.is_file())
files={}
for name in sorted(names):
 if name.startswith('docs/qa/reviews/roadmap-java-representations-2026-10-20/'):continue
 p=root/name
 files[name]=hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else None
out=pathlib.Path('/private/tmp/apex-roadmap-java-acquisition')
(out/'reviewed-source-hashes.json').write_text(json.dumps({'base_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'files':files},indent=2)+'\n')
