import { test as base, expect, type APIRequestContext } from '@playwright/test'
import { appendFile, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { randomBytes } from 'node:crypto'
import { resolve } from 'node:path'
import { type ChildProcess } from 'node:child_process'
import { freePort, healthy, startProcess, stopProcess, portIsListening } from './process'
import { createTarget } from './target'
import { createWorkspace, type Workspace } from '../../e2e/support/workspace'

export interface Harness {
  url: string; cdp: number; agentCDP: number; dir: string; env: NodeJS.ProcessEnv
  target: Awaited<ReturnType<typeof createTarget>>; workspace: Workspace
  request: APIRequestContext; processes: ChildProcess[]; ownedPorts:number[]
}
export const test = base.extend<{ harness: Harness; agentMode: boolean }>({
  agentMode: [false, { option: true }],
  harness: async ({ page, playwright, agentMode }, provide, info) => {
    const bins = process.env.APEXLOAD_RECORDING_E2E_BIN_DIR
    if (!bins) throw new Error('Run scripts/run-recording-e2e.sh (test binaries required)')
    // The parent runner also owns this tree, so forced worker termination
    // cannot strand a case database, CA or browser asset in the OS temp dir.
    const dir = await mkdtemp(`${bins}/case-`)
    const processes: ChildProcess[] = []
    const browserErrors: string[] = []
    page.on('pageerror',error=>browserErrors.push(error.stack || error.message))
    const target = await createTarget(dir).catch(async error => {
      await rm(dir, { recursive: true, force: true })
      throw error
    })
    const port = await freePort(), cdp = await freePort(), agentCDP = await freePort()
    const url = `http://127.0.0.1:${port}`
    const ownedPorts=[port,cdp,agentCDP,Number(new URL(target.url).port),Number(new URL(target.httpsURL).port)]
    const env = { ...process.env, APEXLOAD_ADDR: `127.0.0.1:${port}`, APEXLOAD_DB: `${dir}/db.sqlite`, APEXLOAD_ARCHIVE_DIR: `${dir}/archives`, APEXLOAD_RECORDER_CA_DIR: `${dir}/ca`, APEXLOAD_RECORDER_PROXY_ADDR: '127.0.0.1:0', APEXLOAD_RECORDER_ALLOW_PRIVATE_TARGETS: 'true', APEXLOAD_RECORDER_TLS_INSECURE_SKIP_VERIFY: 'true', APEXLOAD_RECORDING_E2E_CDP_PORT: String(cdp), APEXLOAD_BROWSER_RECORDER: agentMode ? 'off' : 'on', APEXLOAD_ENV: 'development', APEXLOAD_RECORDING_E2E_STOP_GATE: `${dir}/stop-gate`, APEXLOAD_WRAPPING_MASTER_KEY: randomBytes(32).toString('base64') }
    let request: APIRequestContext | undefined
    try {
      let controller = startProcess(resolve(bins, 'controller'), [], dir, env, info.outputPath('controller.log')); processes.push(controller)
      await healthy(`${url}/api/v1/system/health`, controller)
      if (info.project.name === 'local-auth') {
        const settings = await fetch(`${url}/api/v1/system/settings`)
        const update = await fetch(`${url}/api/v1/system/settings`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'If-Match': settings.headers.get('etag')! }, body: JSON.stringify({ auth: { mode: 'local' } }) })
        expect(update.ok, await update.text()).toBeTruthy()
        await stopProcess(controller)
        controller = startProcess(resolve(bins, 'controller'), [], dir, env, info.outputPath('controller.log')); processes.push(controller)
        await healthy(`${url}/api/v1/system/health`, controller)
        await page.goto(`${url}/#/dashboard`)
        await page.getByLabel('Username', { exact: true }).fill('admin')
        await page.getByLabel('Password', { exact: true }).fill('admin')
        await page.getByRole('button', { name: 'Sign in', exact: true }).click()
        await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible()
      }
      request = await playwright.request.newContext({ baseURL: url, storageState: await page.context().storageState() })
      const workspace = await createWorkspace(request)
      await provide({ url, cdp, agentCDP, dir, env, target, workspace, request, processes, ownedPorts })
    } finally {
      const secrets=[...target.sensitiveValues,...(await page.context().cookies().catch(()=>[])).map(c=>c.value)]
      if(process.env.APEXLOAD_RECORDING_E2E_REDACTIONS) await appendFile(process.env.APEXLOAD_RECORDING_E2E_REDACTIONS,secrets.filter(Boolean).map(v=>JSON.stringify(v)+'\n').join(''),{mode:0o600})
      await writeFile(info.outputPath('dashboard-errors.json'),JSON.stringify(browserErrors,null,2))
      await writeFile(info.outputPath('fixture-events.json'), JSON.stringify(target.events, null, 2))
      const checkouts = target.events.filter(event => event.path === '/checkout')
      const checkoutBodies = checkouts.map(event => JSON.parse(event.body) as {csrf_token?: string; order_id?: string})
      // Keep freshness evidence useful after individual credentials and IDs
      // have been removed from the retained ledger and traces.
      await writeFile(info.outputPath('fixture-summary.json'), JSON.stringify({
        events: target.events.length,
        checkouts: checkouts.length,
        acceptedCheckouts: checkouts.filter(event => event.accepted).length,
        rejectedCheckouts: checkouts.filter(event => !event.accepted).length,
        distinctCSRFValues: new Set(checkoutBodies.map(body => body.csrf_token).filter(Boolean)).size,
        distinctOrderIDs: new Set(checkoutBodies.map(body => body.order_id).filter(Boolean)).size,
      }, null, 2))
      for (const child of processes.reverse()) await stopProcess(child)
      await request?.dispose(); await target.close(); await rm(dir, { recursive: true, force: true })
      let openPorts: {port:number;open:boolean}[] = []
      try {
        // The driver's child browser can take a moment to exit after its
        // controller has terminated. Bound that shutdown instead of checking
        // the port during the intervening process-exit notification.
        await expect.poll(async () => {
          openPorts=(await Promise.all(ownedPorts.map(async port=>({port,open:await portIsListening(port)})))).filter(p=>p.open)
          return openPorts
        }, {timeout:5000,message:'All owned listeners must close after teardown'}).toEqual([])
      } finally {
        await writeFile(info.outputPath('cleanup.json'),JSON.stringify({processesExited:processes.every(p=>p.exitCode!==null||p.signalCode!==null),openPorts}))
      }
      expect(browserErrors,'Dashboard must not raise uncaught JavaScript errors').toEqual([])
    }
  },
})
export { expect }
