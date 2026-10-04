import { createServer, type ServerResponse } from 'node:http'
import { execFileSync } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import type { Locator, Page, TestInfo } from '@playwright/test'
import type { ScriptActionDefinition, ScriptDefinition } from '../src/types/scripts'
import type { ScriptDebugRuntimeState } from '../src/types/replay'
import { test, expect, idOf, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

// A real loopback barrier, independent of engine timing: both siblings must
// arrive before the test releases the first response. The second finishes first.
async function groupTarget(checkParameterOrder = false) {
  const stats = { arrived: 0, active: 0, maxActive: 0, inheritedCookies: 0, completed: [] as number[], consumeCount: 0, acceptedConsumers: 0, parameterOrderCorrect: true }
  let first: ServerResponse | undefined, second: ServerResponse | undefined
  const timers = new Set<ReturnType<typeof setTimeout>>()
  const server = createServer((request, response) => {
    const url = new URL(request.url!, 'http://fixture')
    response.setHeader('Content-Type', 'application/json')
    if (url.pathname === '/login') {
      response.setHeader('Set-Cookie', 'parent_session=synthetic-group-session; Path=/; SameSite=Lax')
      response.end('{"ready":true}')
      return
    }
    if (url.pathname === '/group/1' || url.pathname === '/group/2') {
      const index = url.pathname.endsWith('/1') ? 1 : 2
      stats.arrived++; stats.active++; stats.maxActive = Math.max(stats.maxActive, stats.active)
      if (request.headers.cookie?.includes('parent_session=synthetic-group-session')) stats.inheritedCookies++
      if (checkParameterOrder && url.searchParams.get('case') !== (index === 1 ? 'alpha' : 'beta')) stats.parameterOrderCorrect = false
      let finished = false
      const settled = () => { if (!finished) { finished = true; stats.active-- } }
      response.once('finish', settled); response.once('close', settled)
      if (index === 1) {
        first = response
        const timer = setTimeout(() => { timers.delete(timer); if (!response.writableEnded) { response.statusCode = 504; response.end('{"error":"sibling barrier not released"}') } }, 30_000)
        timer.unref(); timers.add(timer)
      } else second = response
      // Transport scheduling may deliver either sibling first. Keep the second
      // response pending until both arrive, then complete it while the first
      // remains held. This proves overlap without assuming request arrival order.
      if (first && second && !second.writableEnded) {
        stats.completed.push(2)
        second.setHeader('Set-Cookie', 'second_done=yes; Path=/; SameSite=Lax')
        second.end('{"value":"second"}')
      }
      return
    }
    if (url.pathname === '/consume') {
      stats.consumeCount++
      const accepted = request.headers.cookie?.includes('parent_session=synthetic-group-session') && request.headers.cookie.includes('first_done=yes') && request.headers.cookie.includes('second_done=yes') && url.searchParams.get('first') === 'first' && url.searchParams.get('second') === 'second'
      if (accepted) stats.acceptedConsumers++
      response.statusCode = accepted ? 200 : 409
      response.end(JSON.stringify({ accepted: !!accepted }))
      return
    }
    response.statusCode = 404; response.end('{"error":"unknown fixture route"}')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Missing loopback address')
  return {
    url: `http://127.0.0.1:${address.port}`, stats,
    releaseFirst() {
      expect(first, 'Both siblings must arrive on the real target').toBeDefined()
      expect(stats.arrived).toBe(2)
      expect(first!.writableEnded).toBe(false)
      for (const timer of timers) clearTimeout(timer)
      timers.clear()
      stats.completed.push(1)
      first!.setHeader('Set-Cookie', 'first_done=yes; Path=/; SameSite=Lax')
      first!.end('{"value":"first"}')
    },
    async close() {
      for (const timer of timers) clearTimeout(timer)
      timers.clear(); server.closeAllConnections()
      await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
    },
  }
}

async function editor(page: Page, workspace: Workspace, id: string) {
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
}
async function selectGroup(page: Page, name: string) {
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true }).click()
  await page.getByRole('navigation', { name: 'Script outline' }).locator('button[data-outline-select]').filter({ hasText: name }).first().click()
  const panel = page.getByRole('region', { name: 'HTTP sequence execution', exact: true })
  await expect(panel).toBeVisible()
  return panel
}
async function capturePanel(page: Page, panel: Locator, info: TestInfo, name: string) {
  await panel.getByLabel('Sequence execution mode', { exact: true }).evaluate(element => {
    element.scrollIntoView({ block: 'center', inline: 'nearest' })
    for (let parent = element.parentElement; parent; parent = parent.parentElement) parent.scrollLeft = 0
  })
  const bounds = await panel.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1)
  expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  if (page.viewportSize()!.width <= 768 && process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
    const sidebar = page.locator('aside.sidebar')
    const identity = sidebar.locator('.sidebar-identity')
    await expect(identity).toBeVisible()
    const rail = await sidebar.boundingBox()
    expect(rail).not.toBeNull()
    const logout = identity.getByRole('button', { name: 'Sign out', exact: true })
    await expect(logout).toBeVisible()
    for (const element of await identity.locator(':scope, .sidebar-identity-copy, .sidebar-identity-name, .sidebar-identity-meta, .sidebar-identity-action').all()) {
      const box = await element.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(rail!.x - 1)
      expect(box!.x + box!.width).toBeLessThanOrEqual(rail!.x + rail!.width + 1)
    }
    expect(await identity.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    const logoutBounds = await logout.boundingBox()
    expect(logoutBounds!.y + logoutBounds!.height).toBeLessThanOrEqual(page.viewportSize()!.height + 1)
  }
  await page.screenshot({ path: info.outputPath(name) })
}
function timestampNanos(value: string): bigint {
  const milliseconds = Date.parse(value)
  if (!Number.isFinite(milliseconds)) throw new Error('Invalid replay timestamp')
  const fraction = value.match(/\.(\d{1,9})(?:Z|[+-]\d{2}:\d{2})$/)?.[1]
  return BigInt(milliseconds) * 1_000_000n + BigInt(fraction ? fraction.padEnd(9, '0').slice(3) : '0')
}
async function runGroup(page: Page, target: Awaited<ReturnType<typeof groupTarget>>, groupName: string, info: TestInfo, paused: boolean) {
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('checkbox', { name: 'Step through (start paused)' }).setChecked(paused)
  await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).uncheck()
  await options.getByRole('combobox', { name: 'Evidence capture', exact: true }).selectOption('diagnostic')
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  if (paused) {
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Login' })).toBeVisible()
    await page.getByRole('button', { name: 'Step', exact: true }).click()
    await expect(page.getByRole('status').filter({ hasText: `Paused before: ${groupName}` })).toBeVisible()
    expect(target.stats.arrived).toBe(0)
    await page.getByRole('button', { name: 'Step', exact: true }).click()
  }
  await expect.poll(() => target.stats.arrived).toBe(2)
  await expect.poll(() => target.stats.completed).toEqual([2])
  expect(target.stats.maxActive).toBe(2)
  expect(target.stats.active).toBe(1)
  expect(target.stats.inheritedCookies).toBe(2)
  expect(target.stats.parameterOrderCorrect).toBe(true)
  expect(target.stats.consumeCount).toBe(0)
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Consume joined values' })).toHaveCount(0)
  target.releaseFirst()
  if (paused) {
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Consume joined values' })).toBeVisible()
    expect(target.stats.active).toBe(0)
    expect(target.stats.completed).toEqual([2, 1])
    expect(target.stats.consumeCount).toBe(0)
    await page.getByRole('button', { name: 'Continue', exact: true }).click()
  }
  await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
  expect(target.stats.completed).toEqual([2, 1])
  expect(target.stats.consumeCount).toBe(1)
  expect(target.stats.acceptedConsumers).toBe(1)
  const downloaded = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export session', exact: true }).click()
  const path = await (await downloaded).path()
  expect(path).toBeTruthy()
  const exported = JSON.parse(await readFile(path!, 'utf8')) as { report: { success: boolean; events: { parent_action_name?: string; request_index?: number; step_path?: string[]; started_at: string; completed_at: string; success: boolean }[] } }
  expect(exported.report.success).toBe(true)
  const siblings = exported.report.events.filter(event => event.parent_action_name === groupName && event.request_index !== undefined)
  expect(siblings.map(event => event.request_index)).toEqual([0, 1])
  expect(siblings.map(event => event.step_path)).toEqual([['requests[0]'], ['requests[1]']])
  expect(siblings.every(event => event.success)).toBe(true)
  expect(timestampNanos(siblings[0].started_at)).toBeLessThan(timestampNanos(siblings[1].completed_at))
  expect(timestampNanos(siblings[1].started_at)).toBeLessThan(timestampNanos(siblings[0].completed_at))
  // The fixture proves inverted wire response completion. Worker scheduling may
  // legitimately invert their processing end order, which must stay observable.
  for (const sibling of siblings) expect(timestampNanos(sibling.completed_at)).toBeGreaterThanOrEqual(timestampNanos(sibling.started_at))
  await info.attach('concurrent-group-counts', { body: JSON.stringify(target.stats), contentType: 'application/json' })
}

test('HTTP sequence authoring preserves children and steps a concurrent parent-session group through its join barrier', async ({ page }, info) => {
  test.setTimeout(120_000)
  const target = await groupTarget(true)
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request, workspace = await createWorkspace(request)
    const children: ScriptActionDefinition[] = [1, 2].map(index => ({ name: index === 1 ? 'First sibling' : 'Second sibling', type: 'GET', target: `/group/${index}?case={{param.case}}`, metadata: { fixture_tag: `child-${index}` }, extractors: [{ name: index === 1 ? 'first' : 'second', source: 'body', type: 'jsonpath', expression: '$.value' }] }))
    const script: ScriptDefinition = { name: 'Parent session concurrent group', version: '1', protocol: 'http', config: { base_url: target.url }, parameters: [{ name: 'case', type: 'list', update: 'each_occurrence', selection: 'sequential', values: ['alpha', 'beta'] }], actions: [
      { name: 'Login', type: 'GET', target: '/login' },
      { name: 'Concurrent fetches', type: 'http_sequence', target: '', params: { requests: children, future_note: { keep: true } }, metadata: { fixture_tag: 'preserve-parent' } },
      { name: 'Consume joined values', type: 'GET', target: '/consume?first={{var.first}}&second={{var.second}}', assertions: [{ type: 'status_code', expected: 200 }] },
    ] }
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
    const before = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    await page.setViewportSize({ width: 1600, height: 1050 })
    await editor(page, workspace, id)
    const panel = await selectGroup(page, 'Concurrent fetches')
    await expect(panel.getByLabel('Sequence execution mode', { exact: true })).toHaveValue('default')
    await expect(panel.getByLabel('Maximum concurrent requests', { exact: true })).toHaveValue('')
    await panel.getByLabel('Sequence execution mode', { exact: true }).selectOption('concurrent')
    await panel.getByLabel('Maximum concurrent requests', { exact: true }).fill('2')
    await expect(panel).toContainText('Requests share the parent HTTP session.')
    await expect(panel).toContainText('group boundary')
    await capturePanel(page, panel, info, 'concurrent-group-desktop.png')
    await page.setViewportSize({ width: 760, height: 1000 })
    await capturePanel(page, panel, info, 'concurrent-group-narrow.png')
    await page.setViewportSize({ width: 1600, height: 1050 })
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
    const after = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(after.actions[1].params).toEqual({ ...before.actions[1].params, execution_mode: 'concurrent', max_concurrency: 2 })
    expect(after.actions[1].metadata).toEqual(before.actions[1].metadata)
    await editor(page, workspace, id)
    const reopened = await selectGroup(page, 'Concurrent fetches')
    await expect(reopened.getByLabel('Sequence execution mode', { exact: true })).toHaveValue('concurrent')
    await expect(reopened.getByLabel('Maximum concurrent requests', { exact: true })).toHaveValue('2')
    await runGroup(page, target, 'Concurrent fetches', info, true)
  } finally { await target.close() }
})

for (const protocol of ['http/1.1', 'http/2'] as const) {
  test(`HTTP sequence controls review and preserve manual execution settings for ${protocol}`, async ({ page }) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request, workspace = await createWorkspace(request)
    const script: ScriptDefinition = { name: `Alias concurrency ${protocol}`, version: '1', protocol, config: { base_url: 'https://example.test' }, actions: [
      { name: 'Alias concurrent group', type: 'http_sequence', target: '', params: { execution_mode: 'concurrent', max_concurrency: 2, requests: [
        { name: 'Alias first', type: 'GET', target: '/one', metadata: { fixture_tag: 'first' } },
        { name: 'Alias second', type: 'GET', target: '/two', metadata: { fixture_tag: 'second' } },
      ], future_note: { keep: true } }, metadata: { fixture_tag: 'alias-parent' } },
    ] }
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
    const before = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    await page.setViewportSize({ width: 1600, height: 1050 })
    await editor(page, workspace, id)
    const panel = await selectGroup(page, 'Alias concurrent group')
    const mode = panel.getByLabel('Sequence execution mode', { exact: true }), limit = panel.getByLabel('Maximum concurrent requests', { exact: true })
    await expect(mode).toHaveValue('concurrent')
    await expect(limit).toHaveValue('2')
    await limit.fill('99')
    await expect(limit).toHaveValue('99')
    await expect(panel).toContainText('Authored concurrency limit is invalid')
    await expect(panel).toContainText('Execution settings need review')
    await limit.fill('1')
    await expect(panel).not.toContainText('Execution settings need review')
    await mode.selectOption('default')
    await expect(panel).toContainText('Sequential execution (default)')
    await expect(limit).toHaveValue('1')
    await mode.selectOption('concurrent')
    await limit.fill('2')
    await expect(panel).toContainText('up to 2 requests in flight')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
    const after = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(after.protocol).toBe(protocol)
    expect(after.actions[0].params).toEqual(before.actions[0].params)
    expect(after.actions[0].metadata).toEqual(before.actions[0].metadata)
    await editor(page, workspace, id)
    const reopened = await selectGroup(page, 'Alias concurrent group')
    await expect(reopened.getByLabel('Sequence execution mode', { exact: true })).toHaveValue('concurrent')
    await expect(reopened.getByLabel('Maximum concurrent requests', { exact: true })).toHaveValue('2')
  })
}

function sourceArchive(source: string): Buffer {
  // Standard ZIP serialization; no source script is executed. The real importer
  // parses these bytes, including the .usr runtime/action declarations.
  return execFileSync('python3', ['-c', 'import io,json,sys,zipfile\nb=io.BytesIO()\nwith zipfile.ZipFile(b,"w",zipfile.ZIP_DEFLATED) as z:\n for k,v in json.loads(sys.argv[1]).items(): z.writestr(k,v)\nsys.stdout.buffer.write(b.getvalue())', JSON.stringify({ 'Concurrent.usr': '[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n', 'default.cfg': '[HTTP]\nHTTPVersion=HTTP/1.1\n', 'Action.c': source })])
}

test('LoadRunner concurrent markers import exact child mappings and replay on the inherited HTTP session', async ({ page }, info) => {
  test.setTimeout(120_000)
  const target = await groupTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request, workspace = await createWorkspace(request)
    await page.setViewportSize({ width: 1600, height: 1050 })
    await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('button', { name: 'Import', exact: true }).click()
    await page.getByRole('button', { name: /Import Loadrunner Script Archive/ }).click()
    const source = `Action() {\nweb_url("Login", "URL=${target.url}/login", LAST);\nweb_concurrent_start(NULL);\nweb_reg_save_param_json("ParamName=first", "QueryString=$.value", LAST);\nweb_url("First sibling", "URL=${target.url}/group/1", LAST);\nweb_reg_save_param_json("ParamName=second", "QueryString=$.value", LAST);\nweb_url("Second sibling", "URL=${target.url}/group/2", LAST);\nweb_concurrent_end(NULL);\nweb_url("Consume joined values", "URL=${target.url}/consume?first={first}&second={second}", LAST);\nreturn 0;\n}`
    await page.locator('input[type=file][accept=".zip,.usz"]').setInputFiles({ name: 'concurrent-http.zip', mimeType: 'application/zip', buffer: sourceArchive(source) })
    const converted = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
    await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
    const result = await json(await converted)
    const draft = result.script as unknown as ScriptDefinition
    const group = draft.actions.find(action => action.type === 'http_sequence' && action.params?.execution_mode === 'concurrent')
    expect(group).toBeDefined()
    expect(group!.params!.requests).toHaveLength(2)
    const review = result.source_review as { mappings: { id: string; function?: string; target_path?: string; target_name?: string }[] }
    const groupIndex = draft.actions.indexOf(group!)
    const expectedPaths = [0, 1].map(index => `actions[${groupIndex}].params.requests[${index}]`)
    const children = group!.params!.requests as ScriptActionDefinition[]
    const childMappings = review.mappings.filter(mapping => mapping.function === 'web_url' && expectedPaths.includes(mapping.target_path || ''))
    expect(childMappings).toHaveLength(2)
    expect(childMappings.map(mapping => mapping.target_path)).toEqual(expectedPaths)
    expect(childMappings.map(mapping => mapping.target_name)).toEqual(children.map(child => child.name))
    await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
    for (const mapping of childMappings) await expect(page.locator(`tr[data-mapping-id="${mapping.id}"]`)).toContainText(mapping.target_path!)
    await page.getByRole('tab', { name: 'Review', exact: true }).click()
    const save = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
    const setup = page.locator('.lr-completed-setup')
    if (await setup.getAttribute('open') === null) await setup.locator(':scope > summary').click()
    await page.locator('.lr-import-actions-card').getByRole('button', { name: 'Save draft', exact: true }).click()
    const saved = await json(await save)
    const scripts = saved.scripts as { id: string }[]
    expect(scripts).toHaveLength(1)
    await editor(page, workspace, scripts[0].id)
    const panel = await selectGroup(page, group!.name)
    await expect(panel.getByLabel('Sequence execution mode', { exact: true })).toHaveValue('concurrent')
    await expect(panel.getByLabel('Maximum concurrent requests', { exact: true })).toHaveValue('')
    await runGroup(page, target, group!.name, info, false)
  } finally { await target.close() }
})


test('runtime evidence unavailability hides stale DTO detail, history and exported session JSON', async ({ page }, info) => {
  // This case verifies the UI transport admission contract using synthetic DTOs.
  // The two cases above exercise the real importer, engine and HTTP target.
  test.setTimeout(90_000)
  const retained = 'synthetic-runtime-detail-for-unavailable-evidence'
  let sendProgress: ((message: unknown) => void) | undefined
  await page.routeWebSocket('**/api/v1/ws**', socket => {
    sendProgress = message => socket.send(JSON.stringify(message))
  })
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const script: ScriptDefinition = { name: 'Unavailable evidence DTO fixture', version: '1', protocol: 'http', config: { base_url: 'https://fixture.invalid' },
    actions: [{ name: 'Authored request remains available', type: 'GET', target: 'https://fixture.invalid/read' }] }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
  let sessionId = '', sessionIncarnation = '', runsStarted = 0
  const event = { event_id: 'synthetic-event-1', phase: 'action', action_index: 0, action_name: 'Synthetic read', action_type: 'GET',
    target: `/read?echo=${retained}`, success: true, status_code: 200, started_at: '2026-10-03T00:00:00Z',
    completed_at: '2026-10-03T00:00:01Z', duration_ms: 1, leaf_ordinal: 3, body: retained, extractions: { ordinary: retained },
    snapshot: { request_headers: { Echo: retained }, response_body: retained }, protocol_evidence: { detail: retained } }
  const report = () => ({ replay_session_id: sessionId, group: 'Synthetic DTO', protocol: 'http', success: true,
    started_at: '2026-10-03T00:00:00Z', completed_at: '', dropped_event_count: 4, truncated_event_evidence_count: 2, events: [event] })
  const runtimeState = (status: 'running' | 'completed', revision: string): ScriptDebugRuntimeState => ({ version: 1, kind: 'full',
    session_id: sessionId, session_incarnation: sessionIncarnation, state_revision: revision, state: 'unavailable',
    reason: status === 'completed' ? 'session_terminal' : 'not_paused', snapshot: null })
  const session = (status: 'running' | 'completed' = 'running', revision = '1') => ({ id: sessionId,
    session_incarnation: sessionIncarnation, state_revision: revision, runtime_state: runtimeState(status, revision),
    group: 'Synthetic DTO', protocol: 'http', log_level: 'trace', status,
    started_at: '2026-10-03T00:00:00Z', current_event_index: 0, dropped_event_count: 4, current_event: event, events: [event], report: report(),
    logs: [{ id: 'old-log', level: 'info', category: 'action', timestamp: '2026-10-03T00:00:01Z', message: retained }],
    raw_output: [retained], condition_evidence: { 'action#0#': { expr: retained, left: retained, result: true } } })
  const servedSession = () => {
    if (runsStarted === 1) return session()
    const clean = { ...report(), completed_at: '2026-10-03T00:00:03Z', events: [], final_variables: { ordinary: 'new-run-current-value' } }
    return { ...session('completed'), completed_at: clean.completed_at,
      current_event_index: -1, current_event: undefined, events: [], logs: [], raw_output: [], condition_evidence: {}, report: clean }
  }
  await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
    sessionId = route.request().postDataJSON().replay_session_id as string
    runsStarted++
    sessionIncarnation = runsStarted === 1 ? '11111111-1111-4111-8111-111111111111' : '22222222-2222-4222-8222-222222222222'
    await route.fulfill({ json: { script_id: id, replay_session_id: sessionId, debug_session_id: sessionId, session: servedSession() } })
  })
  await page.route('**/api/v1/replay/sessions/*', async route => {
    await route.fulfill({ json: { session: servedSession() } })
  })
  await page.setViewportSize({ width: 1600, height: 1050 })
  await editor(page, workspace, id)
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  await page.getByRole('tab', { name: 'Live Variables', exact: true }).click()
  const watches = page.getByRole('region', { name: 'Variable watches', exact: true })
  await expect(watches.getByRole('combobox', { name: 'Values at', exact: true })).toHaveValue('current')
  await expect(watches).toContainText('Current state unavailable: not paused.')
  await expect(watches).not.toContainText(retained)
  await watches.getByRole('combobox', { name: 'Values at', exact: true }).selectOption('retained')
  await expect(watches).toContainText('Retained history')
  await expect(watches).toContainText(retained)
  await expect.poll(() => Boolean(sendProgress && sessionId)).toBe(true)
  sendProgress!({ type: 'script_debug_evidence_unavailable', debug_session_id: sessionId,
    session_incarnation: sessionIncarnation, state_revision: '2', runtime_evidence_unavailable: true })
  await expect(watches).toContainText('Runtime variable evidence unavailable')
  await expect(page.locator('body')).not.toContainText(retained)
  // An older terminal report and its cached sidecars lack the flag; the
  // already admitted signal must still govern every consumer.
  const terminal = { ...report(), completed_at: '2026-10-03T00:00:02Z', final_variables: { ordinary: retained }, runtime_evidence_unavailable: false }
  const terminalSession = (revision: string) => ({ ...session('completed', revision),
    completed_at: terminal.completed_at, report: terminal, runtime_evidence_unavailable: false })
  sendProgress!({ type: 'script_debug_completed', debug_session_id: sessionId, report: terminal, session: terminalSession('1') })
  await expect(watches).toContainText('Runtime variable evidence unavailable')
  await expect(page.locator('body')).not.toContainText(retained)
  await expect(page.locator('.topbar-debug-action-pause')).toBeEnabled()
  // A newer terminal publication can retire the session, but its stale false
  // privacy flag still cannot restore cached detail or historical values.
  sendProgress!({ type: 'script_debug_completed', debug_session_id: sessionId, report: terminal, session: terminalSession('3') })
  await expect(page.locator('.topbar-debug-action-run')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Export session', exact: true })).toBeEnabled()
  await expect(watches).toContainText('Runtime variable evidence unavailable')
  await expect(page.locator('body')).not.toContainText(retained)
  const downloaded = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export session', exact: true }).click()
  const path = await (await downloaded).path()
  expect(path).toBeTruthy()
  const text = await readFile(path!, 'utf8'), exported = JSON.parse(text) as { report: { runtime_evidence_unavailable: boolean; dropped_event_count: number; truncated_event_evidence_count: number; events: { phase: string; action_index: number; success: boolean; status_code: number; duration_ms: number; leaf_ordinal: number }[] }; session: { runtime_evidence_unavailable: boolean; dropped_event_count: number } }
  expect(text).not.toContain(retained)
  expect(exported.report).toMatchObject({ runtime_evidence_unavailable: true, dropped_event_count: 4, truncated_event_evidence_count: 2 })
  expect(exported.session).toMatchObject({ runtime_evidence_unavailable: true, dropped_event_count: 4 })
  expect(exported.report.events[0]).toMatchObject({ phase: 'action', action_index: 0, success: true, status_code: 200, duration_ms: 1, leaf_ordinal: 3 })
  // Recent runs exclude the current run. A clean second run exposes the
  // first run as history and proves the unavailable flag remains run-scoped.
  await page.locator('.topbar-debug-action-run').click()
  await expect(watches.getByRole('combobox', { name: 'Values at', exact: true })).toHaveValue('current')
  await expect(watches).toContainText('Current state unavailable: the session has ended.')
  await expect(watches).not.toContainText('new-run-current-value')
  await watches.getByRole('combobox', { name: 'Values at', exact: true }).selectOption('retained')
  await expect(watches).toContainText('Retained history')
  await expect(watches).toContainText('Final historical values')
  await expect(watches).toContainText('new-run-current-value')
  await expect(watches).not.toContainText('Runtime variable evidence unavailable')
  const history = page.locator('.debug-run-history')
  await expect(history).toBeVisible()
  await history.locator(':scope > summary').click()
  await expect(history).toContainText('Runtime variable evidence unavailable')
  await expect(history).toContainText('4 executions omitted')
  await expect(history).toContainText('2 execution details truncated')
  await expect(history).not.toContainText(retained)
  await info.attach('runtime-evidence-admission-counts', { body: JSON.stringify({ staleDetailsRetained: false, historicalOutcomesRetained: true, exportedUnavailable: true }), contentType: 'application/json' })
})
