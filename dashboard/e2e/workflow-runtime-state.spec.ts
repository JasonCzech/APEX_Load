import { createServer, type Server } from 'node:http'
import type { APIRequestContext, Locator, Page, TestInfo } from '@playwright/test'
import type { ScriptDefinition } from '../src/types/scripts'
import { test, expect, idOf, json, login } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

// These receipts come from real isolated controllers and a loopback target.
// They are execution-boundary evidence, not physical iPad/Salesforce evidence.
interface RuntimeBinding { name: string; value_state: string; value?: string }
interface RuntimeParameter extends RuntimeBinding { state: string; update_policy: string; observed_event_id?: string }
interface RuntimeRow { kind: string; source: string; name: string; row_ordinal?: number; selection: string; state: string; observed_event_id?: string }
interface RuntimeSnapshot {
  state: string; boundary: string
  location: { phase: string; action_index: number; step_path?: string[]; iteration_path?: number[] }
  variables: RuntimeBinding[]; parameters: RuntimeParameter[]; rows: RuntimeRow[]
  frames: { kind: string; phase: string; action_index: number; step_path?: string[]; iteration_path?: number[] }[]
  omissions: { variables: number; parameters: number; rows: number; frames: number; values: number }
}
interface RuntimeSession {
  id: string; status: string; session_incarnation: string; state_revision: string
  runtime_state: { version: number; kind: string; session_id: string; session_incarnation: string; state_revision: string; state: string; reason: string; snapshot: RuntimeSnapshot | null }
  report?: { success: boolean }
}

let target: Server, targetURL = ''
const observed: string[] = []
test.beforeAll(async () => {
  target = createServer((request, response) => {
    observed.push(request.url || '')
    response.setHeader('Content-Type', 'application/json')
    response.end('{"accepted":true}')
  })
  await new Promise<void>((resolve, reject) => { target.once('error', reject); target.listen(0, '127.0.0.1', () => { target.removeListener('error', reject); resolve() }) })
  const address = target.address()
  if (!address || typeof address === 'string') throw new Error('Missing synthetic target address')
  targetURL = `http://127.0.0.1:${address.port}`
})
test.afterAll(async () => { if (target) await new Promise<void>((resolve, reject) => target.close(error => error ? reject(error) : resolve())) })

async function getSession(request: APIRequestContext, id: string) {
  return (await json(await request.get(`/api/v1/script-debug/sessions/${id}`))).session as RuntimeSession
}
function expectEnvelope(session: RuntimeSession) {
  expect(session.session_incarnation).toMatch(/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i)
  expect(session.state_revision).toMatch(/^(0|[1-9][0-9]{0,19})$/)
  expect(session.state_revision.length < 20 || session.state_revision <= '18446744073709551615').toBe(true)
  expect(session.runtime_state).toMatchObject({ version: 1, kind: 'full', session_id: session.id, session_incarnation: session.session_incarnation, state_revision: session.state_revision })
}
async function held(request: APIRequestContext, id: string, location: Partial<RuntimeSnapshot['location']>) {
  let current: RuntimeSession | undefined
  await expect.poll(async () => {
    current = await getSession(request, id)
    return current.runtime_state.state === 'paused' ? current.runtime_state.snapshot?.location : null
  }).toMatchObject(location)
  expectEnvelope(current!)
  expect(current!.runtime_state).toMatchObject({ state: 'paused', reason: 'boundary_held', snapshot: { state: 'complete', boundary: 'before_action' } })
  return current!
}
async function stableInspections(request: APIRequestContext, session: RuntimeSession) {
  for (let i = 0; i < 6; i++) {
    const inspected = await getSession(request, session.id)
    expectEnvelope(inspected)
    expect(inspected.runtime_state).toEqual(session.runtime_state)
    expect(inspected.state_revision).toBe(session.state_revision)
  }
}
function binding(snapshot: RuntimeSnapshot, name: string) { return snapshot.variables.find(value => value.name === name) }
function parameter(snapshot: RuntimeSnapshot, name: string) { return snapshot.parameters.find(value => value.name === name) }
async function openDebugger(page: Page, workspace: Workspace, id: string) {
  await page.setViewportSize({ width: 1800, height: 1100 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('checkbox', { name: 'Step through (start paused)' }).check()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
}

async function alignEvidenceScrollports(focus: Locator) {
  await focus.evaluate(element => {
    const ancestry: HTMLElement[] = []
    for (let parent = element.parentElement; parent; parent = parent.parentElement) ancestry.push(parent)
    const clips = (node: HTMLElement) => /^(auto|scroll|hidden|clip)$/.test(getComputedStyle(node).overflowY)
    const ports = ancestry.filter(parent => /^(auto|scroll)$/.test(getComputedStyle(parent).overflowY) && parent.scrollHeight > parent.clientHeight)
    for (let pass = 0; pass < 3; pass++) for (const port of [...ports].reverse()) {
      const index = ancestry.indexOf(port)
      const target = ports[ports.indexOf(port) - 1] || element
      const header = element.closest('.main-content')?.querySelector(':scope > .main-topbar')
      let safeTop = Math.max(0, header?.getBoundingClientRect().bottom || 0)
      for (const parent of ancestry.slice(index)) {
        if (clips(parent)) safeTop = Math.max(safeTop, parent.getBoundingClientRect().top + parent.clientTop)
      }
      port.scrollTop += target.getBoundingClientRect().top - safeTop - 16
    }
  })
}

function receiptGeometry(element: Element) {
  const box = element.getBoundingClientRect()
  const header = element.closest('.main-content')?.querySelector(':scope > .main-topbar')
  let top = Math.max(0, header?.getBoundingClientRect().bottom || 0) + 8, bottom = innerHeight - 8
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (!/^(auto|scroll|hidden|clip)$/.test(getComputedStyle(parent).overflowY)) continue
    top = Math.max(top, parent.getBoundingClientRect().top + parent.clientTop + 8)
    bottom = Math.min(bottom, parent.getBoundingClientRect().top + parent.clientTop + parent.clientHeight - 8)
  }
  const scrollports = []
  for (let parent = element.parentElement; parent && scrollports.length < 24; parent = parent.parentElement) {
    const port = parent.getBoundingClientRect()
    scrollports.push({ class_name: typeof parent.className === 'string' ? parent.className.slice(0, 120) : '', overflow_y: getComputedStyle(parent).overflowY, client_height: parent.clientHeight, scroll_height: parent.scrollHeight, scroll_top: Math.round(parent.scrollTop), top: Math.round(port.top), bottom: Math.round(port.bottom) })
  }
  const unoccluded = [0.1, 0.5, 0.9].every(fraction => {
    const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height * fraction)
    return hit !== null && element.contains(hit)
  })
  return {
    readable: box.width >= 200 && box.left >= -1 && box.right <= innerWidth + 1 && element.scrollWidth <= element.clientWidth + 1,
    visible: box.top >= top && box.bottom <= bottom && unoccluded,
    focused_width: Math.floor(box.width), focused_height: Math.ceil(box.height),
    focus_top: Math.round(box.top), focus_bottom: Math.round(box.bottom),
    clip_top: Math.round(top), clip_bottom: Math.round(bottom), unoccluded, scrollports,
  }
}

async function narrowReceipts(page: Page, focus: Locator, info: TestInfo, name: string) {
  await expect(page.locator('.sidebar')).toHaveClass(/collapsed/)
  try {
    await expect.poll(() => focus.evaluate(element => element.closest('.debugger-center-body')?.clientHeight ?? 0), {
      message: 'Narrow runtime receipts need enough inspector height to display a complete parameter card',
    }).toBeGreaterThanOrEqual(440)
    for (let pass = 0; pass < 3; pass++) {
      await alignEvidenceScrollports(focus)
      const geometry = await focus.evaluate(receiptGeometry)
      const viewport = page.viewportSize()!
      const available = Math.max(0, geometry.clip_bottom - geometry.clip_top)
      if (geometry.focused_height + 16 <= available || viewport.height >= 1400) break
      await page.setViewportSize({ width: viewport.width, height: Math.min(1400, viewport.height + Math.ceil(geometry.focused_height + 16 - available) + 24) })
    }
    await expect.poll(async () => {
      await alignEvidenceScrollports(focus)
      return focus.evaluate(receiptGeometry)
    }, { message: 'Current-state receipt must remain readable and unoccluded in the actual narrow scrollport' }).toMatchObject({ readable: true, visible: true })
  } catch (error) {
    await info.attach(`${name}-narrow-failure-geometry`, { body: JSON.stringify({ viewport: page.viewportSize(), sidebar_collapsed: true, ...(await focus.evaluate(receiptGeometry)) }), contentType: 'application/json' })
    await page.screenshot({ path: info.outputPath(`${name}-narrow-failure.png`) })
    throw error
  }
  await expect.poll(() => page.evaluate(() => ({ width: Math.max(document.body.scrollWidth, document.documentElement.scrollWidth) <= innerWidth + 1, height: Math.max(document.body.scrollHeight, document.documentElement.scrollHeight) <= 1400 })), { message: 'Narrow receipt evidence must fit the actual desktop viewport without page overflow' }).toEqual({ width: true, height: true })
  await info.attach(`${name}-narrow-geometry`, { body: JSON.stringify({ viewport: page.viewportSize(), sidebar_collapsed: true, ...(await focus.evaluate(receiptGeometry)) }), contentType: 'application/json' })
  await focus.screenshot({ path: info.outputPath(`${name}-narrow-focused.png`) })
  await page.screenshot({ path: info.outputPath(`${name}-narrow.png`), fullPage: true })
}

interface RuntimeCard { name: string; namespace: string; fields: [string, string[]][] }
async function narrowReceiptCards(page: Page, region: Locator, info: TestInfo, name: string, expected: RuntimeCard[]) {
  const cards = region.locator('tbody > tr')
  await expect(cards).toHaveCount(expected.length)
  for (let index = 0; index < expected.length; index++) {
    const card = cards.nth(index), receipt = expected[index]
    await expect(card.locator('th[scope="row"]')).toContainText(receipt.name)
    await expect(card.locator('th[scope="row"]')).toContainText(receipt.namespace)
    for (const [label, values] of receipt.fields) {
      const field = card.locator(`td[data-label="${label}"]`)
      await expect.poll(() => field.evaluate(element => getComputedStyle(element, '::before').content)).toBe(`"${label}"`)
      if (values.length === 1) await expect(field).toHaveText(values[0])
      else for (const value of values) await expect(field).toContainText(value)
    }
    await narrowReceipts(page, card, info, `${name}-${index + 1}`)
  }
}

const data = { name: 'people', type: 'csv' as const, inline_data: 'person\nrow-one\nrow-two\n', strategy: 'sequential' as const, first_row_header: true }
const save = (name: string, value: string, destination: string) => ({ name, type: 'source_parameter', target: '', params: { function: 'lr_save_string', args: [value, destination] } })

test('current paused values and parameter receipts survive inspection without consuming references', async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const startCount = observed.length
  const script: ScriptDefinition = {
    name: 'Runtime_state_non_mutating_inspection', version: '1.0', protocol: 'http', config: { base_url: targetURL },
    data_sources: [data], parameters: [
      { name: 'sequence', type: 'counter', update: 'each_occurrence', start: 42, step: 1 },
      { name: 'stable', type: 'counter', update: 'once', start: 7, step: 1 },
      { name: 'person', type: 'file', data_source: 'people', column: 'person', selection: 'sequential', update: 'each_occurrence' },
    ],
    actions: [save('Assign actual counter', '{{param.sequence}}', 'assigned_number'),
      { name: 'Consume actual references', type: 'request', method: 'GET', target: '/consume?first={{param.sequence}}&second={{param.sequence}}&once={{param.stable}}&again={{param.stable}}&person={{param.person}}&next={{param.person}}&row={{data.people.person}}' },
      { name: 'Finish after inspection', type: 'request', method: 'GET', target: '/finished' }],
  }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
  await openDebugger(page, workspace, id)
  const started = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/debug/sessions'))
  await page.locator('.topbar-debug-action-run').click()
  const initial = (await (await started).json()).session as RuntimeSession
  const before = await held(request, initial.id, { phase: 'action', action_index: 0 })
  expect(parameter(before.runtime_state.snapshot!, 'sequence')).toMatchObject({ state: 'not_evaluated', update_policy: 'each_occurrence' })
  expect(parameter(before.runtime_state.snapshot!, 'stable')).toMatchObject({ state: 'prepared', update_policy: 'once', value: '7' })
  expect(parameter(before.runtime_state.snapshot!, 'person')).toMatchObject({ state: 'not_evaluated' })
  expect(before.runtime_state.snapshot!.rows).toContainEqual(expect.objectContaining({ kind: 'datasource', source: 'people', row_ordinal: 1, state: 'selected' }))
  await stableInspections(request, before)
  expect(observed).toHaveLength(startCount)
  await page.getByRole('tab', { name: 'Live Variables', exact: true }).click()
  const watches = page.getByRole('region', { name: 'Variable watches', exact: true })
  await expect(watches.getByRole('combobox', { name: 'Values at', exact: true })).toHaveValue('current')
  await expect(page.getByRole('region', { name: 'Parameter runtime receipts', exact: true })).toContainText('not evaluated')
  await page.getByRole('button', { name: 'Step', exact: true }).click()
  const afterAssignment = await held(request, initial.id, { phase: 'action', action_index: 1 })
  expect(binding(afterAssignment.runtime_state.snapshot!, 'assigned_number')).toMatchObject({ value_state: 'assigned', value: '42' })
  expect(parameter(afterAssignment.runtime_state.snapshot!, 'sequence')).toMatchObject({ state: 'observed', value: '42' })
  await expect(watches).toContainText('assigned_number')
  await expect(watches).toContainText('42')
  await stableInspections(request, afterAssignment)
  expect(observed).toHaveLength(startCount)
  await page.getByRole('button', { name: 'Step', exact: true }).click()
  const afterRequest = await held(request, initial.id, { phase: 'action', action_index: 2 })
  expect(observed.slice(startCount)).toEqual(['/consume?first=43&second=44&once=7&again=7&person=row-one&next=row-two&row=row-one'])
  expect(parameter(afterRequest.runtime_state.snapshot!, 'sequence')).toMatchObject({ state: 'observed', value: '44' })
  expect(parameter(afterRequest.runtime_state.snapshot!, 'person')).toMatchObject({ state: 'observed', value: 'row-two' })
  expect(afterRequest.runtime_state.snapshot!.rows).toContainEqual(expect.objectContaining({ kind: 'parameter', name: 'person', row_ordinal: 2, state: 'selected' }))
  await stableInspections(request, afterRequest)
  await page.screenshot({ path: testInfo.outputPath('paused-runtime-receipts.png'), fullPage: true })
  await page.getByRole('button', { name: 'Collapse sidebar', exact: true }).click()
  try {
    await page.setViewportSize({ width: 390, height: 1300 })
    await page.getByRole('group', { name: 'Debug panes', exact: true }).getByRole('button', { name: 'Inspector', exact: true }).click()
    const snapshot = afterRequest.runtime_state.snapshot!
    expect(snapshot.variables).toHaveLength(2)
    expect(snapshot.parameters).toHaveLength(3)
    expect(snapshot.rows).toHaveLength(2)
    expect(binding(snapshot, 'people.person')).toMatchObject({ value: 'row-one' })
    for (const label of ['Current variable bindings', 'Parameter runtime receipts', 'Row selection receipts', 'Active runtime frames']) {
      await expect(page.getByRole('region', { name: label, exact: true }).getByRole('heading', { name: label, exact: true })).toBeVisible()
    }
    await narrowReceiptCards(page, page.getByRole('region', { name: 'Current variable bindings', exact: true }), testInfo, 'current-binding', snapshot.variables.map(item => ({ name: item.name, namespace: 'VUser binding', fields: [['Value', [item.value!]], ['State', [item.value_state]]] })))
    await narrowReceiptCards(page, page.getByRole('region', { name: 'Parameter runtime receipts', exact: true }), testInfo, 'parameter-receipt', snapshot.parameters.map(item => ({ name: item.name, namespace: 'param', fields: [['Runtime state', [item.state.replace(/_/g, ' ')]], ['Update policy', [item.update_policy.replace(/_/g, ' ')]], ['Value', [item.value!]], ['Resolution receipt', [item.observed_event_id || 'No actual-use receipt']]] })))
    await narrowReceiptCards(page, page.getByRole('region', { name: 'Row selection receipts', exact: true }), testInfo, 'row-receipt', snapshot.rows.map(item => ({ name: item.name, namespace: item.kind, fields: [['Source', [item.source]], ['Original row', [String(item.row_ordinal)]], ['Selection', [item.selection.replace(/_/g, ' ')]], ['State / receipt', item.observed_event_id ? [item.state, item.observed_event_id] : [item.state]]] })))
    const frames = page.getByRole('region', { name: 'Active runtime frames', exact: true })
    await expect(frames.locator('ol > li')).toHaveCount(snapshot.frames.length)
    await narrowReceipts(page, frames, testInfo, 'active-frames')
  } finally {
    await page.setViewportSize({ width: 1800, height: 1100 })
    await page.getByRole('button', { name: 'Expand sidebar', exact: true }).click()
  }
  await stableInspections(request, afterRequest)
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
  const terminal = await getSession(request, initial.id)
  expectEnvelope(terminal)
  expect(terminal.runtime_state).toMatchObject({ state: 'unavailable', reason: 'session_terminal', snapshot: null })
  await expect(watches).toContainText('Current state unavailable: the session has ended.')
  await expect(watches.getByRole('button', { name: 'Pin assigned_number', exact: true })).toHaveCount(0)
  await watches.getByRole('combobox', { name: 'Values at', exact: true }).selectOption('retained')
  await expect(watches).toContainText('Retained history')
  await expect(watches).toContainText('assigned_number')
  const exported = await request.get(`/api/v1/script-debug/sessions/${initial.id}/export?format=json`)
  expect(exported.ok()).toBe(true)
  const exportBody = await exported.json()
  expect(exportBody.runtime_state ?? exportBody.session?.runtime_state).toMatchObject({ state: 'unavailable', snapshot: null })
})

test('foreach receipts retain original row ordinals and restore enclosing bindings after stepping', async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const startCount = observed.length
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Runtime_state_foreach_restore', version: '1.0', protocol: 'http', config: { base_url: targetURL }, data_sources: [data],
    actions: [{ name: 'Inspect original rows', type: 'control_loop', control: { for_each: { source: 'people', var: 'row' }, steps: [save('Assign row', '{{var.row.person}}', 'current_person'), { name: 'Send row', type: 'request', method: 'GET', target: '/foreach/{{var.row.person}}' }] } }, { name: 'After loop', type: 'request', method: 'GET', target: '/after-loop/{{data.people.person}}' }],
  } } })))
  await openDebugger(page, workspace, id)
  const started = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/debug/sessions'))
  await page.locator('.topbar-debug-action-run').click()
  const initial = (await (await started).json()).session as RuntimeSession
  for (let row = 1; row <= 2; row++) {
    const before = await held(request, initial.id, { action_index: 0, step_path: ['loop[0]'], iteration_path: [row] })
    const snapshot = before.runtime_state.snapshot!
    expect(snapshot.rows).toContainEqual(expect.objectContaining({ kind: 'foreach', source: 'people', row_ordinal: row, state: 'selected' }))
    expect(snapshot.frames).toContainEqual(expect.objectContaining({ kind: 'foreach', iteration_path: [row] }))
    expect(binding(snapshot, 'row.person')).toMatchObject({ value: row === 1 ? 'row-one' : 'row-two' })
    await stableInspections(request, before)
    await page.getByRole('tab', { name: 'Live Variables', exact: true }).click()
    const frames = page.getByRole('region', { name: 'Active runtime frames', exact: true })
    await expect(frames).toContainText(`iteration ${row}`)
    await frames.getByRole('button', { name: 'Inspect runtime frame 2 source', exact: true }).click()
    const source = page.getByRole('region', { name: 'Source', exact: true })
    await expect(source.locator('.script-code-title > span')).toHaveText('actions[0]')
    if (row === 2) await page.screenshot({ path: testInfo.outputPath('foreach-frame-source.png'), fullPage: true })
    await json(await request.post(`/api/v1/script-debug/sessions/${initial.id}/step`, { data: { mode: 'into' } }))
    const assigned = await held(request, initial.id, { action_index: 0, step_path: ['loop[1]'], iteration_path: [row] })
    expect(binding(assigned.runtime_state.snapshot!, 'current_person')).toMatchObject({ value: row === 1 ? 'row-one' : 'row-two' })
    await stableInspections(request, assigned)
    await json(await request.post(`/api/v1/script-debug/sessions/${initial.id}/step`, { data: { mode: 'into' } }))
  }
  const restored = await held(request, initial.id, { action_index: 1 })
  expect(restored.runtime_state.snapshot!.rows.some(row => row.kind === 'foreach')).toBe(false)
  expect(restored.runtime_state.snapshot!.frames.some(frame => frame.kind === 'foreach')).toBe(false)
  expect(binding(restored.runtime_state.snapshot!, 'row.person')).toBeUndefined()
  expect(binding(restored.runtime_state.snapshot!, 'loop_index')).toBeUndefined()
  expect(restored.runtime_state.snapshot!.rows).toContainEqual(expect.objectContaining({ kind: 'datasource', source: 'people', row_ordinal: 1 }))
  expect(observed.slice(startCount)).toEqual(['/foreach/row-one', '/foreach/row-two'])
  const stopped = (await json(await request.post(`/api/v1/script-debug/sessions/${initial.id}/stop`))).session as RuntimeSession
  expectEnvelope(stopped)
  expect(stopped.runtime_state).toMatchObject({ state: 'unavailable', snapshot: null })
  expect(observed.slice(startCount)).toEqual(['/foreach/row-one', '/foreach/row-two'])
})


test('Run To continues after its breakpoint socket precedes the HTTP acknowledgement', async ({ page }) => {
  test.setTimeout(90_000)
  let breakpointSocket: { session?: RuntimeSession } | undefined
  await page.routeWebSocket('**/api/v1/ws**', socket => {
    const server = socket.connectToServer()
    server.onMessage(message => {
      const event = JSON.parse(message.toString()) as { type?: string; session?: RuntimeSession }
      if (event.type === 'script_debug_breakpoints_updated') breakpointSocket = event
      socket.send(message)
    })
  })
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request), startCount = observed.length
  const script: ScriptDefinition = { name: 'Runtime_state_Run_To_socket_acknowledgement', version: '1.0', protocol: 'http', config: { base_url: targetURL }, actions: [
    { name: 'First before target', type: 'request', method: 'GET', target: '/run-to/first' },
    { name: 'Selected target', type: 'request', method: 'GET', target: '/run-to/target' },
    { name: 'Finish after target', type: 'request', method: 'GET', target: '/run-to/finish' },
  ] }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
  await openDebugger(page, workspace, id)
  const started = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/debug/sessions'))
  await page.locator('.topbar-debug-action-run').click()
  const initial = (await (await started).json()).session as RuntimeSession
  await held(request, initial.id, { phase: 'action', action_index: 0 })
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: First before target' })).toBeVisible()
  let releaseAcknowledgement!: () => void
  const acknowledgementGate = new Promise<void>(resolve => { releaseAcknowledgement = resolve })
  let fetchedAcknowledgement: RuntimeSession | undefined, resumeRequests = 0
  page.on('request', outgoing => {
    if (outgoing.method() === 'POST' && new URL(outgoing.url()).pathname === `/api/v1/replay/sessions/${initial.id}/resume`) resumeRequests++
  })
  await page.route(`**/api/v1/replay/sessions/${initial.id}/breakpoints`, async route => {
    if (route.request().method() !== 'PATCH') { await route.continue(); return }
    const response = await route.fetch()
    fetchedAcknowledgement = (await response.json()).session as RuntimeSession
    await acknowledgementGate
    await route.fulfill({ response })
  })
  try {
    await page.getByRole('region', { name: 'Debugger actions' }).getByRole('button', { name: /Action 2.*Selected target/ }).click()
    await page.getByRole('button', { name: 'Run to selected action', exact: true }).click()
    await expect.poll(() => fetchedAcknowledgement?.id).toBe(initial.id)
    await expect.poll(() => breakpointSocket?.session?.state_revision).toBe(fetchedAcknowledgement!.state_revision)
    // The real published session has reached and changed the page before the
    // successful HTTP command acknowledgement is allowed to arrive.
    await expect(page.locator('.debugger-statusbar').getByText('1 breakpoint', { exact: true })).toBeVisible()
    expect(resumeRequests).toBe(0)
    expect(observed.slice(startCount)).toEqual([])
    await held(request, initial.id, { phase: 'action', action_index: 0 })
    releaseAcknowledgement()
    await expect.poll(() => resumeRequests).toBe(1)
    await held(request, initial.id, { phase: 'action', action_index: 1 })
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Selected target' })).toBeVisible()
    expect(observed.slice(startCount)).toEqual(['/run-to/first'])
    await page.getByRole('button', { name: 'Continue', exact: true }).click()
    await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
    expect(observed.slice(startCount)).toEqual(['/run-to/first', '/run-to/target', '/run-to/finish'])
  } finally { releaseAcknowledgement() }
})
