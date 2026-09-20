import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { access, chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { validateRecordingReport } from './recording-report.mjs'
const report = () => ({stats:{expected:1,skipped:0,unexpected:0,flaky:0},suites:[{specs:[{title:'capture',tests:[{projectName:'dev-admin',results:[{status:'passed'}]}]}]}]})
test('complete inventory matches every profile and scenario',()=>{
  validateRecordingReport(report(),['capture'],['dev-admin'])
  assert.throws(()=>validateRecordingReport(report(),['capture','proxy'],['dev-admin']),/Missing/)
  assert.throws(()=>validateRecordingReport(report(),['capture'],['dev-admin','local-auth']),/Missing/)
  assert.throws(()=>validateRecordingReport(report(),[],['dev-admin']),/coverage inventory/)
})
test('targeting cannot hide skips, retries or an empty selection',()=>{
  validateRecordingReport(report(),['capture','proxy'],['dev-admin'],true)
  for(const key of ['skipped','unexpected','flaky']){const r=report();r.stats[key]=1;assert.throws(()=>validateRecordingReport(r,[],[],true))}
  const r=report();r.suites[0].specs[0].tests[0].results.push({status:'passed'});assert.throws(()=>validateRecordingReport(r,[],[],true),/exactly once/)
  assert.throws(()=>validateRecordingReport({...report(),suites:[]},[],[],true),/No recording/)
})

test('selection cannot override retry, discovery, or evidence policy',async()=>{
  const {validateRecordingSelection}=await import('./recording-report.mjs')
  validateRecordingSelection(['browser.spec.ts','--grep','agent:'])
  validateRecordingSelection(['--grep-invert=mobile'])
  for(const args of [['--retries=2'],['--workers','4'],['--reporter=line'],['--list'],['--grep']]) assert.throws(()=>validateRecordingSelection(args))
})

for (const mode of ['failure', 'interruption']) test(`entry point cleans process groups and scratch space after ${mode}`, { timeout: 20_000 }, async () => {
  const directory = await mkdtemp(`${tmpdir()}/recording-runner-test-`)
  const marker = `${directory}/started.json`
  const output = `${directory}/evidence`
  // Replace only the external build command. The real entry point must own
  // and reap this process tree when a build fails or the user interrupts it.
  await writeFile(`${directory}/npm`, `#!/usr/bin/env node
const fs = require('node:fs');
const child = require('node:child_process').spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {stdio: 'ignore'});
fs.writeFileSync(process.env.RECORDING_HARNESS_TEST_MARKER, JSON.stringify({scratch: process.env.APEXLOAD_RECORDING_E2E_BIN_DIR, pid: child.pid}));
fs.writeFileSync(process.env.APEXLOAD_RECORDING_E2E_OUTPUT + '/build.json', JSON.stringify({pairing_token: 'synthetic-capability'}));
${mode === 'failure' ? 'process.exit(17)' : 'setInterval(() => {}, 1000)'}
`)
  await chmod(`${directory}/npm`, 0o755)
  const runner = spawn(process.execPath, [resolve(import.meta.dirname, 'run-recording-e2e.mjs')], {
    env: { ...process.env, PATH: `${directory}:${process.env.PATH}`, RECORDING_HARNESS_TEST_MARKER: marker, APEXLOAD_RECORDING_E2E_OUTPUT: output },
    stdio: 'ignore',
  })
  const exited = new Promise((resolve, reject) => { runner.once('exit', resolve); runner.once('error', reject) })
  let childPID
  try {
    let state
    const deadline = Date.now() + 10_000
    while (!state && Date.now() < deadline) {
      try { state = JSON.parse(await readFile(marker, 'utf8')) } catch { await delay(25) }
    }
    assert.ok(state, 'build command must start')
    childPID = state.pid
    if (mode === 'interruption') runner.kill('SIGTERM')
    assert.equal(await exited, mode === 'failure' ? 1 : 130)
    await assert.rejects(access(state.scratch), { code: 'ENOENT' })
    const alive = () => { try { process.kill(childPID, 0); return true } catch { return false } }
    for (let attempt = 0; alive() && attempt < 40; attempt++) await delay(25)
    assert.equal(alive(), false, 'orphan descendant must exit')
    assert.deepEqual(JSON.parse(await readFile(`${output}/build.json`, 'utf8')), { pairing_token: '[redacted]' })
  } finally {
    runner.kill('SIGKILL')
    if (childPID) { try { process.kill(childPID, 'SIGKILL') } catch { /* already reaped */ } }
    await rm(directory, { recursive: true, force: true })
  }
})
