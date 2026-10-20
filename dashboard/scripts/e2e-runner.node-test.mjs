import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { copyFileSync, existsSync, mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import test from 'node:test'

const controller = `#!/usr/bin/env node
const fs = require('node:fs'), http = require('node:http');
const file = process.env.APEXLOAD_DB;
const assetRoot=process.env.APEXLOAD_IMPORT_ASSET_DIR, protectedRoot=process.env.APEXLOAD_BRUNO_ARTIFACT_ROOT;
for(const dir of [assetRoot,protectedRoot]) fs.mkdirSync(dir,{recursive:true});
let state = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file)) : {mode:'dev'};
const audit = event => fs.appendFileSync(process.env.RUNNER_AUDIT, JSON.stringify(event)+'\\n');
audit({kind:'start', pid:process.pid, db:file, mode:state.mode});
console.log('retained controller evidence');
const server = http.createServer((req,res) => {
  if (req.url.endsWith('/system/settings')) {
    if (req.method === 'PUT') {state.mode='local';fs.writeFileSync(file,JSON.stringify(state));}
    res.setHeader('ETag','"settings-1"');res.end('{}');return;
  }
  if (req.url.endsWith('/auth/me')) {res.end(JSON.stringify({mode:state.mode,enabled:state.mode==='local'}));return;}
  if (req.url === '/fixture') {const previous=state.marker;state.marker=true;fs.writeFileSync(file,JSON.stringify(state));const previousAssets=[assetRoot,protectedRoot].map(dir=>{const marker=dir+'/marker';const prior=fs.existsSync(marker);fs.writeFileSync(marker,'visited');return prior});res.end(JSON.stringify({previous:Boolean(previous),db:file,mode:state.mode,assetRoot,protectedRoot,previousAssets}));return;}
  res.end('{}');
});
server.listen(Number(process.env.APEXLOAD_ADDR.split(':').pop()), '127.0.0.1');
process.on('SIGTERM',()=>server.close(()=>{audit({kind:'stop',pid:process.pid});process.exit(0)}));
`
const playwright = `#!/usr/bin/env node
const fs = require('node:fs'), path = require('node:path');
const args = process.argv.slice(2);
const audit = event => fs.appendFileSync(process.env.RUNNER_AUDIT, JSON.stringify(event)+'\\n');
if(args.includes('--list')) {
  const files=['a[1].spec.ts','b.spec.ts'];
  console.log(JSON.stringify({config:{configFile:path.join(process.cwd(),'playwright.config.ts')},errors:process.env.RUNNER_BAD_SELECTION ? [{message:'bad collection'}] : [],suites:[{specs:files.map(file=>({file,tests:[{}]}))}]}));process.exit(0);
}
(async()=>{
  const output=path.join(process.env.APEXLOAD_E2E_OUTPUT_ROOT,process.env.APEXLOAD_E2E_PROFILE);
  fs.rmSync(output,{recursive:true,force:true});fs.mkdirSync(output,{recursive:true});fs.writeFileSync(path.join(output,'evidence.txt'),'test evidence');
  const state=await (await fetch(process.env.APEXLOAD_E2E_BASE_URL+'/fixture')).json();
  audit({kind:'test',pid:process.pid,args,output,...state});
  if(process.env.RUNNER_WAIT){setInterval(()=>{},1000);return;}
  const isFirst=args.some(arg=>{try{return new RegExp(arg).test(path.join(process.cwd(),'e2e','a[1].spec.ts'))}catch{return false}});
  process.exitCode=process.env.RUNNER_FAIL_FIRST && isFirst ? 1 : 0;
})().catch(error=>{console.error(error);process.exitCode=1});
`

async function fixture(t, profile, args = [], extra = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'apex-e2e-runner-'))
  let child, done
  t.after(async () => {
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM')
      await done
    }
    rmSync(dir, { recursive: true, force: true })
  })
  const put = (file, content, mode = 0o644) => { const target = join(dir, file); mkdirSync(dirname(target), { recursive: true }); writeFileSync(target, content, { mode }); return target }
  const runner = put('scripts/run-e2e.sh', readFileSync(new URL('../../scripts/run-e2e.sh', import.meta.url)))
  put('dashboard/scripts/check-e2e-browser.mjs', '') // Browser preflight has its own real-browser test.
  const helper = put('dashboard/scripts/e2e-file-selection.mjs', '')
  copyFileSync(new URL('./e2e-file-selection.mjs', import.meta.url), helper)
  put('dashboard/playwright.config.ts', '')
  put('dashboard/e2e/a[1].spec.ts', ''); put('dashboard/e2e/b.spec.ts', '')
  put('pkg/api/static/index.html', 'fixture')
  const sharedAssets=join(dir,'shared-assets'), sharedProtected=join(dir,'shared-protected')
  put('shared-assets/marker','parent sentinel'); put('shared-protected/marker','parent sentinel')
  const binary = put('fixture-controller.cjs', controller, 0o755)
  put('dashboard/node_modules/.bin/playwright', playwright, 0o755)
  put('bin/go', '#!/bin/bash\nset -eu\nprintf \'{"kind":"build"}\\n\' >> "$RUNNER_AUDIT"\nwhile (( $# )); do if [[ "$1" = -o ]]; then cp "$RUNNER_CONTROLLER" "$2"; chmod +x "$2"; exit 0; fi; shift; done\nexit 2\n', 0o755)
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening')
  const port = server.address().port; await new Promise(resolve => server.close(resolve))
  const audit = join(dir, 'audit.jsonl'), output = join(dir, 'artifacts')
  child = spawn('bash', [runner, ...args], { cwd: dir, env: { ...process.env, PATH: `${join(dir,'bin')}:${dirname(process.execPath)}:${process.env.PATH}`, RUNNER_AUDIT:audit, RUNNER_CONTROLLER:binary, APEXLOAD_IMPORT_ASSET_DIR:sharedAssets, APEXLOAD_BRUNO_ARTIFACT_ROOT:sharedProtected, APEXLOAD_E2E_PROFILE:profile, APEXLOAD_E2E_PORT:String(port), APEXLOAD_E2E_OUTPUT_ROOT:output, ...extra }, stdio:['ignore','pipe','pipe'] })
  let text='';child.stdout.on('data',d=>{text+=d});child.stderr.on('data',d=>{text+=d})
  done = once(child,'exit')
  return { child, done, audit, output, sharedAssets, sharedProtected, text:()=>text, events:()=>existsSync(audit) ? readFileSync(audit,'utf8').trim().split('\n').filter(Boolean).map(line=>JSON.parse(line)) : [] }
}

for (const profile of ['dev-admin','local-auth']) {
  test(`whole ${profile} suite builds once, isolates stores, retains failed evidence and continues`, {timeout:30000, skip:process.platform === 'win32'}, async t => {
    const run = await fixture(t,profile,[],{RUNNER_FAIL_FIRST:'1'})
    const [code] = await run.done;assert.equal(code,1,run.text())
    const events=run.events(), builds=events.filter(e=>e.kind==='build'), tests=events.filter(e=>e.kind==='test')
    assert.equal(builds.length,1);assert.equal(tests.length,2,run.text())
    assert.notEqual(tests[0].db,tests[1].db)
    assert.notEqual(tests[0].assetRoot,tests[1].assetRoot)
    assert.notEqual(tests[0].protectedRoot,tests[1].protectedRoot)
    for(const event of tests){
      assert.deepEqual(event.previousAssets,[false,false])
      assert.equal(event.assetRoot,join(dirname(event.db),'import-assets'))
      assert.equal(event.protectedRoot,join(dirname(event.db),'protected-imports'))
      assert.equal(existsSync(event.assetRoot),false);assert.equal(existsSync(event.protectedRoot),false)
    }
    for(const dir of [run.sharedAssets,run.sharedProtected]) assert.equal(readFileSync(join(dir,'marker'),'utf8'),'parent sentinel')
    for(const event of tests){assert.equal(event.previous,false);assert.equal(event.mode,profile==='local-auth'?'local':'dev');assert.equal(existsSync(event.db),false);assert.equal(readFileSync(join(event.output,'evidence.txt'),'utf8'),'test evidence')}
    const starts=events.filter(e=>e.kind==='start'), stops=events.filter(e=>e.kind==='stop')
    assert.equal(starts.length,profile==='local-auth'?4:2);assert.equal(stops.length,starts.length)
    const logs=join(run.output,'controller-logs',profile);const session=join(logs,readdirSync(logs)[0]);const retained=readdirSync(session).filter(f=>f.endsWith('.log'))
    assert.equal(retained.length,2)
    for(const file of retained)assert.match(readFileSync(join(session,file),'utf8'),/retained controller evidence/)
  })
}

test('filtered invocation preserves all CLI arguments and its controller log', {timeout:30000, skip:process.platform === 'win32'}, async t => {
  const args=['--grep','a thing','one.spec.ts','two.spec.ts','--shard=1/2']
  const run=await fixture(t,'dev-admin',args);const [code]=await run.done;assert.equal(code,0,run.text())
  const tests=run.events().filter(e=>e.kind==='test');assert.equal(tests.length,1);assert.deepEqual(tests[0].args,['test',...args]);assert.deepEqual(tests[0].previousAssets,[false,false])
  for(const dir of [run.sharedAssets,run.sharedProtected]) assert.equal(readFileSync(join(dir,'marker'),'utf8'),'parent sentinel')
  const logs=join(run.output,'controller-logs','dev-admin');assert.match(readFileSync(join(logs,readdirSync(logs)[0],'selected.log'),'utf8'),/retained controller evidence/)
})

test('collection failure runs no tests or controllers and cannot pass an empty suite', {timeout:30000, skip:process.platform === 'win32'}, async t => {
  const run=await fixture(t,'dev-admin',[],{RUNNER_BAD_SELECTION:'1'});const [code]=await run.done;assert.equal(code,1,run.text())
  assert.equal(run.events().filter(e=>['start','test'].includes(e.kind)).length,0)
})

test('interruption stops the owned controller and retains its log', {timeout:30000, skip:process.platform === 'win32'}, async t => {
  const run=await fixture(t,'dev-admin',['a.spec.ts'],{RUNNER_WAIT:'1'})
  for(let i=0;i<200 && !run.events().some(e=>e.kind==='test');i++)await new Promise(resolve=>setTimeout(resolve,25))
  assert.ok(run.events().some(e=>e.kind==='test'),run.text());run.child.kill('SIGTERM')
  const [code]=await run.done;assert.equal(code,143,run.text())
  const events=run.events();assert.equal(events.filter(e=>e.kind==='stop').length,1)
  assert.equal(existsSync(events.find(e=>e.kind==='start').db),false)
})


test('fixture teardown joins running processes before removing their files', {timeout:30000, skip:process.platform === 'win32'}, async t => {
  const run = await fixture(t, 'dev-admin', ['a.spec.ts'], {RUNNER_WAIT:'1'})
  for (let i=0; i<200 && !run.events().some(e=>e.kind==='test'); i++) await new Promise(resolve=>setTimeout(resolve,25))
  const events=run.events(), browser=events.find(e=>e.kind==='test'), controller=events.find(e=>e.kind==='start')
  assert.ok(browser,run.text())
  t.after(() => {
    for (const pid of [run.child.pid, browser.pid, controller.pid]) assert.throws(() => process.kill(pid, 0), {code:'ESRCH'})
    assert.equal(existsSync(controller.db),false)
  })
  // Leave the wrapper running to exercise the same hook used on assertion failure.
})

for (const failDev of [false, true]) {
  test(`workflow wrapper runs both profiles and returns ${failDev ? 'failure' : 'success'}`, {timeout:30000, skip:process.platform === 'win32'}, async t => {
    const dir = mkdtempSync(join(tmpdir(), 'apex-e2e-wrapper-'))
    let child, done
    t.after(async () => {
      if (child && child.exitCode === null && child.signalCode === null) {
        child.kill('SIGTERM')
        await done
      }
      rmSync(dir, {recursive:true, force:true})
    })
    mkdirSync(join(dir, 'scripts'))
    const wrapper = join(dir, 'scripts/run-dashboard-workflows.sh')
    copyFileSync(new URL('../../scripts/run-dashboard-workflows.sh', import.meta.url), wrapper)
    const audit = join(dir, 'profiles.jsonl')
    writeFileSync(join(dir, 'scripts/run-e2e.sh'), `#!/bin/bash
set -eu
printf '{"profile":"%s","port":"%s"}\\n' "$APEXLOAD_E2E_PROFILE" "$APEXLOAD_E2E_PORT" >> "$RUNNER_AUDIT"
if [[ "$APEXLOAD_E2E_PROFILE" == dev-admin && "$RUNNER_FAIL_DEV" == 1 ]]; then exit 1; fi
`, {mode:0o755})
    child = spawn('bash', [wrapper], {cwd:dir, env:{...process.env, APEXLOAD_E2E_PROFILES:'', APEXLOAD_E2E_PORT:'18111', APEXLOAD_E2E_AUTH_PORT:'18112', RUNNER_AUDIT:audit, RUNNER_FAIL_DEV:failDev ? '1' : ''}, stdio:['ignore','pipe','pipe']})
    let output = ''
    child.stdout.on('data', data => {output += data})
    child.stderr.on('data', data => {output += data})
    done = once(child, 'exit')
    const [code] = await done
    assert.equal(code, failDev ? 1 : 0, output)
    assert.deepEqual(readFileSync(audit, 'utf8').trim().split('\n').map(line => JSON.parse(line)), [
      {profile:'dev-admin', port:'18111'},
      {profile:'local-auth', port:'18112'},
    ])
  })
}
