import { chromium, expect, test as playwrightTest, type Page, type Browser, type TestInfo, type CDPSession } from '@playwright/test'
import { appendFileSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import type { Harness } from './fixtures'
import { freePort, healthy, startProcess } from './process'
import { scopeQuery } from '../../e2e/support/workspace'

export type Script = Record<string, any> // DSL changes are checked by the real parser, not a test copy.
export async function studio(page: Page, h: Harness) {
  await page.goto(`${h.url}/#/scripts?${scopeQuery(h.workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Script builder', exact: true }).click()
  await page.getByRole('button', { name: 'Record', exact: true }).click()
  await expect(page.getByRole('group', { name: 'Recording method' })).toBeVisible()
}
export async function pairAgent(page: Page, h: Harness, info: TestInfo) {
  await expect(page.getByTestId('browser-recorder-bridge-panel')).toBeVisible()
  const token = (await page.getByTestId('browser-recorder-bridge-token').textContent())!.trim()
  recordSensitive(token)
  await page.addStyleTag({content:'[data-testid=browser-recorder-bridge-token]{-webkit-text-security:disc}'})
  const port = await freePort(), origin = `http://127.0.0.1:${port}`
  h.ownedPorts.push(port)
  const agent = startProcess(resolve(process.env.APEXLOAD_RECORDING_E2E_BIN_DIR!, 'agent'), ['--addr', `127.0.0.1:${port}`, '--token-stdin', '--allow-origin', h.url, '--json'], h.dir, { ...h.env, APEXLOAD_BROWSER_RECORDER: 'on', APEXLOAD_RECORDING_E2E_CDP_PORT: String(h.agentCDP) }, info.outputPath('agent.log'))
  h.processes.push(agent); agent.stdin!.end(token+'\n')
  await healthy(`${origin}/api/v1/system/health`, agent)
  await page.getByLabel('Agent address', { exact: true }).fill(origin)
  // Connect checks that the agent answers, then pairs with it.
  const pairing=page.waitForResponse(r=>r.url().endsWith('/bridge/pair')&&r.request().method()==='POST')
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  const paired=await (await pairing).json()
  if(paired.bridge?.session_id) recordSensitive(paired.bridge.session_id)
  await expect(page.getByTestId('browser-recorder-bridge-connected')).toBeVisible()
  return agent
}
export async function startRecording(page: Page, h: Harness, agent = false, options: { har?: boolean; mobile?: string; screenshots?: boolean; minimal?: boolean; ws?: boolean; excludeHosts?: string } = {}) {
  await page.getByLabel('Start URL scheme', { exact: true }).selectOption('http://')
  await page.getByLabel('Start URL address', { exact: true }).fill(h.target.url.replace('http://',''))
  await page.getByLabel('Capture side-car HAR', { exact: false }).setChecked(options.har ?? true)
  if (options.mobile) await page.getByLabel('Emulated device preset', { exact: true }).selectOption({ label: options.mobile })
  if (options.screenshots || options.minimal || options.ws !== undefined || options.excludeHosts) {
    await page.getByRole('button', { name: 'Advanced options', exact: true }).click()
    if (options.excludeHosts) await page.getByLabel('Exclude hosts',{exact:true}).fill(options.excludeHosts)
    if (options.screenshots) await page.getByLabel('Capture per-step screenshot thumbnails').check()
    if (options.minimal) await page.getByLabel('Allow the in-page advanced sidebar', { exact: false }).uncheck()
    if (options.ws !== undefined) await page.getByLabel('Capture WebSocket frame payloads (scrubbed)', { exact: true }).setChecked(options.ws)
  }
  const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/browser-recorder/sessions' && r.request().method() === 'POST')
  await page.getByRole('button', { name: 'Start browser recording', exact: true }).click()
  const result = await response; expect(result.ok(), await result.text()).toBeTruthy()
  const session = (await result.json()).session
  expect(session.project_id).toBe(h.workspace.projectId)
  expect(session.status).toBe('recording')
  expect(session.id).toBeTruthy()
  await expect(page.getByTestId('browser-recorder-panel')).toBeVisible()
  let attached: Browser | undefined
  await expect.poll(async () => {
    try { attached = await chromium.connectOverCDP(`http://127.0.0.1:${agent ? h.agentCDP : h.cdp}`); return true } catch { return false }
  }).toBe(true)
  const browser = attached!
  const target = browser.contexts().flatMap(c => c.pages()).find(p => p.url().startsWith(h.target.url))
  expect(target, 'Recorder-owned target page').toBeTruthy()
  await expect(target!.locator('body')).toHaveAttribute('data-token', /.+/)
  return { browser, target: target!, id: session.id as string, session }
}
export async function snapshot(h: Harness, id: string, raw = false) {
  const response = await h.request.get(`/api/v1/browser-recorder/sessions/${id}${raw?'?raw=true':''}`)
  expect(response.ok(), await response.text()).toBeTruthy()
  const session = (await response.json()).session
  session.steps ??= []
  return session
}
export async function stopRecording(page: Page) {
  await page.getByTestId('browser-recorder-panel').getByRole('button', { name: 'Stop recording', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Preview conversion', exact: true })).toBeVisible({ timeout: 30_000 })
}
export async function preview(page: Page, info: TestInfo) {
  const response = page.waitForResponse(r => r.url().endsWith('/convert/preview') && r.request().method() === 'POST')
  await page.getByRole('button', { name: 'Preview conversion', exact: true }).click()
  const result = await response; expect(result.ok(), await result.text()).toBeTruthy()
  const payload = await result.json()
  await info.attach('converted-dsl', { body: JSON.stringify(payload, null, 2), contentType: 'application/json' })
  return payload as { script: Script; scripts?: Script[]; warnings?: string[] }
}
export async function verifyOutputs(page: Page, count: number) {
  for (let i=1; i<=count; i++) {
    await page.getByRole('button', { name: `Verify output ${i}`, exact: true }).click()
    await expect(page.getByText('Verification passed for this conversion', { exact: true })).toHaveCount(i, { timeout: 60_000 })
  }
}
export async function saveRecording(page: Page, verified = true): Promise<string[]> {
  const response = page.waitForResponse(r => /\/convert$|\/commit-batch$/.test(new URL(r.url()).pathname) && r.request().method() === 'POST')
  await page.getByRole('button', { name: verified ? 'Save verified draft' : 'Save unverified draft', exact: true }).click()
  const result = await response; expect(result.ok(), await result.text()).toBeTruthy()
  const payload = await result.json()
  const stored = payload.stored_scripts || payload.scripts || (payload.stored_script ? [payload.stored_script] : payload.script ? [payload.script] : [])
  expect(stored.length, 'Saved conversion outputs').toBeGreaterThan(0)
  return stored.map((s: { id: string }) => s.id)
}
export async function editor(page: Page, h: Harness, id: string) {
  await page.goto(`${h.url}/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(h.workspace)}`)
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
}
export async function editSource(page: Page, script: Script) {
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true }).click()
  await page.getByRole('tab', { name: 'Source', exact: true }).click()
  const fullSource = page.getByRole('button', { name: 'Edit in Full Script JSON', exact: true })
  if (await fullSource.isVisible()) await fullSource.click()
  await page.getByRole('button', { name: 'Edit JSON', exact: true }).click()
  const source = page.locator('.script-code-pane .cm-content[contenteditable="true"]')
  await source.click()
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A')
  await page.keyboard.insertText(JSON.stringify(script))
  await page.getByRole('button', { name: 'Apply changes', exact: true }).click()
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
}
export async function debug(page: Page, info: TestInfo, success = true) {
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByLabel('Script evidence states')).toContainText(success ? 'Debugged: passed' : 'Debugged: failed', { timeout: 60_000 })
  const downloading = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export session', exact: true }).click()
  const download = await downloading
  const content = await readFile((await download.path())!, 'utf8')
  await info.attach('replay-session', { body: content, contentType: 'application/json' })
  const exported = JSON.parse(content)
  expect(exported.report.success).toBe(success)
  return exported.report
}

// CDP can inspect closed shadow roots without changing their encapsulation.
// Coordinates are resolved afresh; mutations still use Chromium's input system.
export class Toolbar {
  constructor(private page: Page, private cdp: CDPSession) {}
  static async for(page: Page) { return new Toolbar(page, await page.context().newCDPSession(page)) }
  async node(selector: string): Promise<number> {
    const { root } = await this.cdp.send('DOM.getDocument', { depth: -1, pierce: true })
    const find = (node: any): any => {
      if (node.attributes?.includes('data-apexload-recorder')) return node
      for (const child of [...(node.children || []), ...(node.shadowRoots || [])]) { const found = find(child); if (found) return found }
    }
    const host = find(root)
    if (!host?.shadowRoots?.length) throw new Error('Recorder toolbar missing')
    const { nodeId } = await this.cdp.send('DOM.querySelector', { nodeId: host.shadowRoots[0].nodeId, selector })
    if (!nodeId) throw new Error(`Toolbar control missing: ${selector}`)
    return nodeId
  }
  async point(selector: string) {
    let previous = '', point = { x: 0, y: 0 }
    // Live sidebar snapshots replace step buttons and can resize the dock.
    // Give closed-root controls the same discovery/stability wait as a normal
    // Playwright locator. Only reads/scrolling are polled; input is sent once.
    await expect.poll(async () => {
      try {
        const nodeId = await this.node(selector)
        await this.cdp.send('DOM.scrollIntoViewIfNeeded', { nodeId })
        const { model } = await this.cdp.send('DOM.getBoxModel', { nodeId })
        const q = model.content
        const position = JSON.stringify(q)
        point = { x: (q[0]+q[2]+q[4]+q[6])/4, y: (q[1]+q[3]+q[5]+q[7])/4 }
        const stable = position === previous
        previous = position
        return stable
      } catch {
        previous = ''
        return false
      }
    }, { timeout: 5000, intervals: [50, 100, 250], message: `Toolbar control must be attached and stable: ${selector}` }).toBe(true)
    return point
  }
  // Whether a bar control is rendered and visible (not display:none, not
  // hidden while the bar waits for its layout).
  async shown(selector: string) {
    try {
      const { object } = await this.cdp.send('DOM.resolveNode', { nodeId: await this.node(selector) })
      const result = await this.cdp.send('Runtime.callFunctionOn', { objectId: object.objectId, functionDeclaration: 'function () { const s = getComputedStyle(this); const r = this.getBoundingClientRect(); return s.display !== "none" && s.visibility !== "hidden" && r.width > 0 && r.height > 0 }', returnByValue: true })
      return result.result.value === true
    } catch {
      return false
    }
  }
  async box(selector: string): Promise<{ x: number; y: number; width: number; height: number }> {
    const { object } = await this.cdp.send('DOM.resolveNode', { nodeId: await this.node(selector) })
    const result = await this.cdp.send('Runtime.callFunctionOn', { objectId: object.objectId, functionDeclaration: 'function () { return this.getBoundingClientRect().toJSON() }', returnByValue: true })
    return result.result.value
  }
  async count(selector: string) {
    const nodeId = await this.node('.panel')
    return (await this.cdp.send('DOM.querySelectorAll', { nodeId, selector })).nodeIds.length
  }
  async value(selector: string) {
    const { object } = await this.cdp.send('DOM.resolveNode', { nodeId: await this.node(selector) })
    const result = await this.cdp.send('Runtime.callFunctionOn', { objectId: object.objectId, functionDeclaration: 'function () { return this.value }', returnByValue: true })
    return result.result.value
  }
  async click(selector: string) {
    await this.page.bringToFront()
    // A new document keeps the bar invisible until its layout arrives; a click
    // then would land on the page beneath it.
    await expect.poll(() => this.shown(selector), { message: `Toolbar control must be visible: ${selector}` }).toBe(true)
    const p = await this.point(selector)
    await this.page.mouse.click(p.x,p.y)
  }
  async fill(selector: string, value: string) {
    await this.click(selector)
    await this.page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A')
    await this.page.keyboard.insertText(value)
    try {
      await expect.poll(()=>this.value(selector),{message:`Toolbar input ${selector} must retain the typed value`}).toBe(value)
    } catch(error) {
      const {object}=await this.cdp.send('DOM.resolveNode',{nodeId:await this.node(selector)})
      const state=await this.cdp.send('Runtime.callFunctionOn',{objectId:object.objectId,functionDeclaration:'function(){const a=this.getRootNode().activeElement;return {value:this.value,rect:this.getBoundingClientRect().toJSON(),active:a?{tag:a.tagName,cls:a.className,value:a.value}:null,viewport:{width:innerWidth,height:innerHeight,scrollX,scrollY}}}',returnByValue:true})
      await playwrightTest.info().attach('toolbar-input-state',{body:JSON.stringify(state.result.value),contentType:'application/json'})
      await playwrightTest.info().attach('toolbar-input-screen',{body:await this.page.screenshot(),contentType:'image/png'})
      throw error
    }
  }
  async close() { await this.cdp.detach().catch(() => {}) }
}

export async function executeSaved(page:Page,h:Harness,id:string,info:TestInfo) {
  const {buildScenarioUI}=await import('../../e2e/support/workflow')
  const response=await h.request.get(`/api/v1/scripts/${id}`)
  expect(response.ok()).toBeTruthy()
  const script=(await response.json()).script
  script.name=`Recorded ${script.protocol} ${id.slice(0,8)}`
  script.config.run_logic={...script.config.run_logic,iterations:1,auto_transactions:'per_iteration'}
  script.teardown=[{name:'Teardown ledger',type:script.protocol==='browser'?'browser_navigate':'request',...(script.protocol==='http'?{method:'GET'}:{}),target:'/event?name=teardown'}]
  await editor(page,h,id);await editSource(page,script)
  await buildScenarioUI(page,script.name,`Workload ${id.slice(0,8)}`)
  await page.getByText('Apply runtime defaults to all script groups',{exact:true}).click()
  await page.getByRole('button',{name:/^Iterations /}).click()
  const override=page.getByRole('dialog',{name:'Edit Iterations',exact:true})
  await override.getByLabel('Iterations',{exact:true}).fill('1')
  await override.getByRole('button',{name:'Save Run Override',exact:true}).click()
  await page.getByRole('combobox',{name:'Release',exact:true}).selectOption(h.workspace.releaseId)
  await page.getByLabel('Run Name',{exact:true}).fill(`One VUser ${script.protocol}`)
  const start=page.getByRole('button',{name:'Start Now',exact:true})
  await expect(start).toBeEnabled({timeout:30_000})
  const created=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/v1/tests')
  const before=h.target.events.length
  await start.click()
  const result=await created;expect(result.ok(),await result.text()).toBeTruthy()
  const run=await result.json();expect(run.total_vusers).toBe(1)
  await expect.poll(async()=>{const r=await h.request.get(`/api/v1/tests/${run.id}`);return(await r.json()).status},{timeout:90_000}).toBe('COMPLETE')
  const events=h.target.events.slice(before)
  expect(events.filter(e=>e.query.name==='click'||e.path==='/checkout')).toHaveLength(1)
  expect(events.every(e=>e.accepted)).toBe(true)
  expect(events.filter(e=>e.query.name==='teardown')).toHaveLength(1)
  expect(events.at(-1)?.query.name).toBe('teardown')
  const manifest=await h.request.get(`/api/v1/tests/${run.id}/manifest`)
  expect(manifest.ok()).toBeTruthy()
  const execution=await manifest.json()
  expect(execution.current_configuration.runtime_settings.iterations).toBe(1)
  let transactions:Script[]=[]
  await expect.poll(async()=>{
    const response=await h.request.get(`/api/v1/tests/${run.id}/archive/transactions?limit=100`)
    if(!response.ok()) return 0
    transactions=(await response.json()).transactions || []
    return transactions.length
  },{timeout:30_000}).toBeGreaterThan(0)
  expect(transactions.every(t=>t.success)).toBe(true)
  expect(transactions.some(t=>t.action_name==='Teardown ledger'&&t.phase==='teardown')).toBe(true)
  await info.attach(`${script.protocol}-execution`,{body:JSON.stringify({manifest:execution,transactions}),contentType:'application/json'})
}

export function recordSensitive(value:string) {
  if(process.env.APEXLOAD_RECORDING_E2E_REDACTIONS) appendFileSync(process.env.APEXLOAD_RECORDING_E2E_REDACTIONS,JSON.stringify(value)+'\n',{mode:0o600})
}
