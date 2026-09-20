import { validateRecordingReport, validateRecordingSelection } from './recording-report.mjs'
import { spawn, execFileSync } from 'node:child_process'
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const scratch = await mkdtemp(`${tmpdir()}/apex-recording-build-`)
const output = resolve(process.env.APEXLOAD_RECORDING_E2E_OUTPUT || `${root}/output/playwright/recording/${new Date().toISOString().replaceAll(':', '-')}`)
await mkdir(output, { recursive: true })
let child
let interrupted = false
let killTimer
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  interrupted = true
  if (child?.pid) {
    const pid = child.pid
    try { process.kill(-pid, signal) } catch { /* exited */ }
    killTimer ??= setTimeout(() => { try { process.kill(-pid, 'SIGKILL') } catch { /* exited */ } }, 10_000)
    killTimer.unref()
  }
})
async function run(command, args, cwd = root) {
  if (interrupted) throw new Error('Interrupted')
  child = spawn(command, args, { cwd, detached: true, stdio: 'inherit', env: { ...process.env, APEXLOAD_RECORDING_E2E_BIN_DIR: scratch, APEXLOAD_RECORDING_E2E_OUTPUT: output, APEXLOAD_RECORDING_E2E_REDACTIONS: `${scratch}/redactions.jsonl` } })
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', code => resolve(code)) })
  const group = child.pid
  child = undefined
  clearTimeout(killTimer)
  // Reap orphan service processes even if a worker exited before teardown.
  if (group) { try { process.kill(-group, 'SIGKILL') } catch { /* group already empty */ } }
  if (code !== 0) throw new Error(`${command} exited with ${code}`)
}
try {
  validateRecordingSelection(process.argv.slice(2))
  await run('npm', ['run', 'build'], `${root}/dashboard`)
  await run('go', ['build', '-tags', 'recording_e2e', '-o', `${scratch}/controller`, './cmd/controller'])
  await run('go', ['build', '-tags', 'recording_e2e', '-o', `${scratch}/agent`, './cmd/apexload-record-agent'])
  await run(`${root}/dashboard/node_modules/.bin/playwright`, ['test', '--config=playwright.recording.config.ts', ...process.argv.slice(2)], `${root}/dashboard`)
  const report = JSON.parse(await readFile(`${output}/report.json`, 'utf8'))
  const inventory=JSON.parse(await readFile(`${root}/scripts/recording-scenarios.json`,'utf8'))
  validateRecordingReport(report,inventory,process.env.APEXLOAD_E2E_PROFILE?[process.env.APEXLOAD_E2E_PROFILE]:['dev-admin','local-auth'],process.argv.length>2)
} catch (error) { console.error(error.message); process.exitCode = interrupted ? 130 : 1 }
finally {
  try { execFileSync('python3',[`${root}/scripts/redact-recording-evidence.py`,output,`${scratch}/redactions.jsonl`],{stdio:'inherit'}) } catch { process.exitCode=1 }
  await rm(scratch, { recursive: true, force: true }); console.log(`Recording evidence: ${output}`) }
