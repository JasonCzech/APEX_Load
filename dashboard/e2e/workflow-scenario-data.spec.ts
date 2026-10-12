import { test, expect, json, login, seedScript, idOf } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { readFile } from 'node:fs/promises'

test('scenario Data step previews script parameters and restores them after removing a shared pool', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Checkout_data_preview', version: '1.0.0', protocol: 'http',
      config: { base_url: 'https://example.invalid' },
      data_sources: [{ name: 'customers', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'email,segment\ndemo@example.test,retail\nsecond@example.test,business' }],
      parameters: [
        { name: 'customer_email', type: 'file', data_source: 'customers', column: 'email', update: 'each_iteration', selection: 'unique', on_exhaustion: 'error' },
        { name: 'customer_segment', type: 'file', data_source: 'customers', column: 'segment', same_line_as: 'customer_email' },
        { name: 'order_number', type: 'counter', start: 1000, end: 9999, step: 1 },
      ],
      actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout/{{param.customer_email}}/{{data.customers.segment}}' }],
    },
  } }))
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  const panel = page.getByRole('region', { name: 'Script data & parameters', exact: true })
  await page.getByPlaceholder('Scenario name').fill('Script data preview scenario')
  await page.getByRole('button', { name: 'Choose script for Group 1', exact: true }).click()
  await page.getByRole('row').filter({ hasText: 'Checkout_data_preview' }).getByRole('button', { name: 'Use for Group 1', exact: true }).click()
  await steps.getByRole('button', { name: /^Data/ }).click()
  await expect(panel.getByRole('definition')).toHaveCount(4)
  await expect(panel).toContainText('1 sources · 3 parameters')
  await expect(panel).toContainText('Same row as customer_email')
  await expect(panel).toContainText('Unique selection · On exhaustion: Error')
  await panel.locator('.scenario-script-source > summary').click()
  await expect(panel.getByLabel('Data preview for customers')).toContainText('demo@example.test,retail')
  await expect(page.getByText(/no script source or scenario pool supplies it/)).toHaveCount(0)
  await page.setViewportSize({ width: 1440, height: 1600 })
  await page.getByRole('heading', { name: 'Data Pools', exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('scenario-script-data-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 600, height: 900 })
  await expect(panel.getByRole('heading', { name: 'Script data & parameters' })).toBeVisible()
  expect(await panel.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
  await page.screenshot({ path: testInfo.outputPath('scenario-script-data-narrow.png'), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByText('Create a data pool without leaving this draft', { exact: true }).click()
  await page.getByLabel('New data pool name', { exact: true }).fill('Shared customers')
  await page.getByRole('textbox', { name: 'First data row (JSON object)', exact: true }).fill('{"email":"shared@example.test","segment":"retail"}')
  await page.getByRole('button', { name: 'Create and attach data pool', exact: true }).click()
  await expect(panel).toBeHidden()
  await page.getByRole('button', { name: 'Remove binding 1', exact: true }).click()
  await expect(panel).toBeVisible()
  await expect(panel).toContainText('1 sources · 3 parameters')
})

test('scenario save and reopen preserves both bindings and attaching another pool appends it', async ({page}) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request=page.context().request
 const workspace=await createWorkspace(request)
 const script=await seedScript(request,workspace,'Pool parity HTTP')
 const pools=[]
 for (const name of ['Customers','Orders']) pools.push(idOf(await json(await request.post('/api/v1/data-pools',{data:{project_id:workspace.projectId,name}}))))
 const bindings=pools.map((pool_id,index)=>({pool_id,alias:index===0?'customers':'orders',access_mode:'read_only',selection_strategy:index===0?'unique':'random'}))
 const scenario=idOf(await json(await request.post('/api/v1/scenarios',{data:{project_id:workspace.projectId,name:'Pool parity',duration:'1m',groups:[{name:'Load',script_id:script,vusers:1,initialization:'at_runtime'}],data_pools:bindings}})))
 await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
 await page.reload()
 await page.locator('.scenario-library-table').getByRole('row').filter({hasText:'Pool parity'}).getByRole('button',{name:/^Edit\b/}).click()
 const steps=page.getByLabel('Scenario builder steps')
 await steps.getByRole('button',{name:/^Data/}).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(2)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs=>inputs.map(input=>(input as HTMLInputElement).value))).toEqual(['customers','orders'])
 await page.getByText('Create a data pool without leaving this draft',{exact:true}).click()
 await page.getByLabel('New data pool name',{exact:true}).fill('Addresses')
 await page.getByRole('button',{name:'Create and attach data pool',exact:true}).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(3)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs=>inputs.map(input=>(input as HTMLInputElement).value))).toEqual(['customers','orders','addresses'])
 await steps.getByRole('button',{name:/^Review/}).click()
 const saved=page.waitForResponse(response=>response.request().method()==='PUT'&&new URL(response.url()).pathname===`/api/v1/scenarios/${scenario}`)
 await page.getByRole('button',{name:'Save draft',exact:true}).click()
 const response=await saved
 expect(response.ok(),await response.text()).toBeTruthy()
 expect(response.request().postDataJSON().data_pools.slice(0,2)).toMatchObject(bindings)
 const stored=await json(await request.get(`/api/v1/scenarios/${scenario}`))
 if (!Array.isArray(stored.data_pools)) throw new Error('Scenario did not retain canonical bindings')
 expect(stored.data_pools).toHaveLength(3)
 expect(stored.data_pools).toEqual(expect.arrayContaining(bindings.map(binding=>expect.objectContaining(binding))))
 await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
 await page.reload()
 await page.locator('.scenario-library-table').getByRole('row').filter({hasText:'Pool parity'}).getByRole('button',{name:/^Edit\b/}).click()
 await page.getByLabel('Scenario builder steps').getByRole('button',{name:/^Data/}).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(3)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs=>inputs.map(input=>(input as HTMLInputElement).value))).toEqual(['addresses','customers','orders'])
})

test('legacy browser checkpoint migrates a first-pool replacement through save and reopen', async ({ page }) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request = page.context().request
 const workspace = await createWorkspace(request)
 const script = await seedScript(request, workspace, 'Legacy pool migration HTTP')
 const pools = []
 for (const name of ['Original customers', 'Replacement customers', 'Orders']) pools.push(idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name } }))))
 const bindings = [{ pool_id: pools[0], alias: 'customers', access_mode: 'read_only', selection_strategy: 'sequential' }, { pool_id: pools[2], alias: 'orders', access_mode: 'read_only', selection_strategy: 'random' }]
 const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Legacy pool migration', duration: '1m', groups: [{ name: 'Load', script_id: script, vusers: 1, initialization: 'at_runtime' }], data_pools: bindings } })))
 const stored = await json(await request.get(`/api/v1/scenarios/${scenario}`))
 await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
 await page.reload()
 await expect(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Legacy pool migration' })).toBeVisible()
 // The pre-migration create action changed the scalar first-slot fields while
 // retaining the old canonical array. Preserve this real legacy shape.
 await page.evaluate(({ project, scenarioId, updatedAt, scriptId, originalBindings, replacement }) => {
  localStorage.setItem(`apexload.scenario.authoring.v1.${encodeURIComponent(project)}`, JSON.stringify({
   version: 2, projectId: project, savedAt: new Date().toISOString(), editingId: scenarioId, editingUpdatedAt: updatedAt,
   source: 'existing', startChoice: 'existing', shapeId: '', templateId: '', cloneId: scenarioId, step: 'data', visited: 4,
   form: { name: 'Legacy pool migration', duration: '1m', start_delay: '', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false,
    runtime_pacing_type: '', runtime_ignore_think_time: false, runtime_log_level: '', runtime_auto_transactions: '', assignment_mode: 'auto', assignment_targets: [],
    groups: [{ name: 'Load', script_id: scriptId, vusers: 1, ramp_up: '', ramp_down: '', start_offset: '', initialization: 'at_runtime', stages: [] }],
    data_pools: originalBindings, data_pool_id: replacement, data_pool_alias: 'replacement', data_pool_selection_strategy: 'unique' },
  }))
 }, { project: workspace.projectId, scenarioId: scenario, updatedAt: String(stored.updated_at || ''), scriptId: script, originalBindings: bindings, replacement: pools[1] })
 await page.reload()
 await expect(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Legacy pool migration' })).toBeVisible()
 await page.getByRole('region', { name: 'Saved browser draft' }).getByRole('button', { name: 'Resume draft', exact: true }).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(2)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs => inputs.map(input => (input as HTMLInputElement).value))).toEqual(['replacement', 'orders'])
 await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
 const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
 await page.getByRole('button', { name: 'Save draft', exact: true }).click()
 const response = await saved
 expect(response.ok(), await response.text()).toBeTruthy()
 expect(response.request().postDataJSON().data_pools).toMatchObject([{ pool_id: pools[1], alias: 'replacement', selection_strategy: 'unique' }, bindings[1]])
 await expect(page.locator('.scenario-save-notice')).toHaveText(/^Saved Legacy pool migration/)
 await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeEnabled()
 await expect(page.getByRole('region', { name: 'Saved browser draft' })).toHaveCount(0)
 await expect(page.getByText('Editing · Saved', { exact: true })).toBeVisible()
 await page.getByRole('group', { name: 'Scenario page view' }).getByRole('button', { name: 'Scenario Library', exact: true }).click()
 const row = page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Legacy pool migration' })
 await expect(row).toBeVisible()
 await page.reload()
 await expect(row).toBeVisible()
 await row.getByRole('button', { name: /^Edit\b/ }).click()
 await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Data/ }).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(2)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs => inputs.map(input => (input as HTMLInputElement).value))).toEqual(['orders', 'replacement'])
})

test('live-table opt-in survives save and reopen while read-only snapshots stay the default', async ({ page }, testInfo) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request = page.context().request
 const workspace = await createWorkspace(request)
 const script = await seedScript(request, workspace, 'Live table configuration HTTP')
 const pool = idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Live work seed', schema: { id: 'string' } } })))
 const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Live table opt-in', duration: '1m', groups: [{ name: 'Load', script_id: script, vusers: 1 }], data_pools: [{ pool_id: pool, alias: 'work', access_mode: 'read_only', selection_strategy: 'sequential' }] } })))
 const openData = async () => {
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`); await page.reload()
  await page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Live table opt-in' }).getByRole('button', { name: /^Edit\b/ }).click()
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Data/ }).click()
 }
 await openData()
 await expect(page.getByRole('combobox', { name: 'Access mode', exact: true })).toHaveValue('read_only')
 await page.getByRole('combobox', { name: 'Access mode', exact: true }).selectOption('live')
 const options = page.getByRole('region', { name: 'Live table options for work' })
 await expect(options.getByRole('combobox', { name: 'When the run ends', exact: true })).toHaveValue('discard')
 await options.getByRole('combobox', { name: 'Starting data', exact: true }).selectOption('original_seed')
 await options.getByLabel('Source run ID', { exact: true }).fill('prior-run-id')
 await expect(options).toContainText('retained encrypted seed')
 await options.getByRole('combobox', { name: 'Starting data', exact: true }).selectOption('empty')
 await expect(options.getByLabel('Source run ID', { exact: true })).toHaveCount(0)
 await options.getByLabel('Columns', { exact: true }).fill('id,email')
 await options.getByRole('combobox', { name: 'At each iteration', exact: true }).selectOption('lease')
 await expect(options).toContainText('Expired leases can be delivered again')
 await options.getByRole('combobox', { name: 'Table behavior', exact: true }).selectOption('vts_columns')
 await expect(options.getByRole('combobox', { name: 'At each iteration', exact: true })).toHaveValue('manual')
 await expect(options.getByRole('combobox', { name: 'At each iteration', exact: true }).locator('option[value=lease]')).toHaveCount(0)
 await options.getByRole('combobox', { name: 'Table behavior', exact: true }).selectOption('native_queue')
 await options.getByRole('combobox', { name: 'When no row is available', exact: true }).selectOption('wait')
 await expect(options.getByLabel('Maximum wait (ms)', { exact: true })).toHaveValue('10000')
 await page.setViewportSize({ width: 1280, height: 1000 })
 await page.screenshot({ path: testInfo.outputPath('live-table-binding-desktop.png'), fullPage: true })
 await page.setViewportSize({ width: 600, height: 900 })
 expect(await options.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
 await options.getByLabel('Columns', { exact: true }).scrollIntoViewIfNeeded()
 await expect(options.getByLabel('Columns', { exact: true })).toBeInViewport()
 await page.screenshot({ path: testInfo.outputPath('live-table-binding-narrow.png'), fullPage: true })
 await page.setViewportSize({ width: 1280, height: 900 })
 await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
 const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
 await page.getByRole('button', { name: 'Save draft', exact: true }).click()
 const response = await saved
 expect(response.ok(), await response.text()).toBeTruthy()
 expect(response.request().postDataJSON().data_pools[0]).toMatchObject({ access_mode: 'live', selection_strategy: 'manual', live_options: { seed_policy: 'empty', profile: 'native_queue', columns: ['id', 'email'], durability: 'memory', end_mode: 'discard', empty_policy: 'wait', wait_ms: 10000 } })
 expect(await response.json()).toMatchObject({ authoring_projection: { scenario: { data_pools: [{ access_mode: 'live', live_options: { seed_policy: 'empty', columns: ['id', 'email'], end_mode: 'discard', empty_policy: 'wait', wait_ms: 10000 } }] } } })
 await openData()
 await expect(page.getByRole('combobox', { name: 'Access mode', exact: true })).toHaveValue('live')
 await expect(options.getByLabel('Columns', { exact: true })).toHaveValue('id,email')
})

test('live run counts keep values private until preview and clear them after disconnect', async ({ page, trackRun }, testInfo) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request = page.context().request
 const workspace = await createWorkspace(request)
 const script = idOf(await json(await request.post('/api/v1/scripts', { data: {
  project_id: workspace.projectId,
  script: { name: 'Live table runtime', version: '1.0.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1', pacing: { type: 'fixed_interval', interval_ms: 500 } },
   init: [{ name: 'Publish work', type: 'live_table', params: { alias: 'work', operation: 'push', values: { id: 'runtime-row', email: 'preview-only@example.test' } } }],
   actions: [{ name: 'Inspect work', type: 'live_table', params: { alias: 'work', operation: 'read', position: 1, result_prefix: 'work_result' } }],
  },
 } })))
 const id = idOf(await json(await request.post('/api/v1/tests', { data: {
  project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
  name: 'Live table privacy run', duration: '2m', groups: [{ name: 'Worker', script_id: script, vusers: 1 }],
  data_pools: [{ alias: 'work', access_mode: 'live', selection_strategy: 'manual', live_options: { seed_policy: 'empty', columns: ['id', 'email'], durability: 'memory', end_mode: 'discard' } }],
 } }))); trackRun(id)
 await json(await request.post(`/api/v1/tests/${id}/start`))
 const notifications: Record<string, unknown>[] = []
 page.on('websocket', socket => {
  if (new URL(socket.url()).pathname !== `/api/v1/tests/${id}/stream`) return
  socket.on('framereceived', frame => {
   try { const message = JSON.parse(String(frame.payload)); if (message.type === 'live_tables_changed') notifications.push(message) } catch { /* Other transport frames are not table hints. */ }
  })
 })
 await page.addInitScript(() => {
  const sockets: WebSocket[] = []
  Object.defineProperty(window, '__apexLiveTableTestSockets', { value: sockets })
  const BrowserWebSocket = window.WebSocket
  window.WebSocket = class extends BrowserWebSocket {
   constructor(url: string | URL, protocols?: string | string[]) { super(url, protocols); sockets.push(this) }
  }
 })
 await page.goto(`/#/test/${id}?tab=overview&${scopeQuery(workspace)}`)
 // Local-auth login loads the project list before this API-seeded workspace exists.
 await page.reload()
 const panel = page.getByRole('region', { name: 'Live data tables', exact: true })
 await expect(panel.getByRole('heading', { name: 'work', exact: true })).toBeVisible()
 await expect(panel).toContainText('Revision 1', { timeout: 30_000 })
 await expect(panel).toContainText('Live updates connected.')
 await expect(panel.locator('time')).toHaveAttribute('datetime', /^\d{4}-\d{2}-\d{2}T/)
 await expect.poll(() => notifications.length).toBeGreaterThan(0)
 for (const message of notifications) {
  expect(Object.keys(message).sort()).toEqual(['event_index', 'test_id', 'timestamp', 'type'])
  expect(message.test_id).toBe(id)
 }
 await expect(panel).not.toContainText('preview-only@example.test')
 await expect(panel.getByRole('table')).toHaveCount(0)
 await panel.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).click()
 await expect(panel.getByRole('table', { name: 'Live rows for work' })).toContainText('preview-only@example.test')
 await page.setViewportSize({ width: 1280, height: 1000 })
 await panel.getByRole('table', { name: 'Live rows for work' }).scrollIntoViewIfNeeded()
 await expect(panel.getByRole('table', { name: 'Live rows for work' })).toBeInViewport()
 await page.screenshot({ path: testInfo.outputPath('live-table-run-desktop.png'), fullPage: true })
 const download = page.waitForEvent('download')
 await panel.getByRole('button', { name: 'Export table', exact: true }).click()
 const exported = await download
 expect(await exported.failure()).toBeNull()
 const exportPath = testInfo.outputPath('live-table-export.json')
 await exported.saveAs(exportPath)
 expect(JSON.parse(await readFile(exportPath, 'utf8'))).toMatchObject({ format: 'apexload-live-table-v1', revision: 1, columns: ['id', 'email'], rows: [{ values: { id: 'runtime-row', email: 'preview-only@example.test' } }] })
 const closed = await page.evaluate(runId => {
  const sockets = (window as Window & { __apexLiveTableTestSockets?: WebSocket[] }).__apexLiveTableTestSockets || []
  let count = 0
  for (const socket of sockets) {
   if (socket.readyState === WebSocket.OPEN && new URL(socket.url).pathname === `/api/v1/tests/${runId}/stream`) { socket.close(); count++ }
  }
  return count
 }, id)
 expect(closed).toBeGreaterThan(0)
 await expect(panel).toContainText('Reconnecting live updates.')
 await expect(panel).not.toContainText('preview-only@example.test')
 await expect(panel).toContainText('Live updates connected.', { timeout: 15_000 })
 await expect(panel.getByRole('table')).toHaveCount(0)
 await page.evaluate(() => window.dispatchEvent(new Event('offline')))
 await expect(panel).not.toContainText('preview-only@example.test')
 await expect(panel).toContainText('Offline.')
 await page.evaluate(() => window.dispatchEvent(new Event('online')))
 await expect(panel.getByRole('heading', { name: 'work', exact: true })).toBeVisible()
 await expect(panel.getByRole('table')).toHaveCount(0)
 await page.setViewportSize({ width: 600, height: 900 })
 expect(await panel.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
 await panel.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).scrollIntoViewIfNeeded()
 await expect(panel.getByRole('button', { name: 'Preview up to 100 rows', exact: true })).toBeInViewport()
 await page.screenshot({ path: testInfo.outputPath('live-table-run-narrow.png'), fullPage: true })
 await json(await request.post(`/api/v1/tests/${id}/abort`))
 await expect.poll(async () => (await json(await request.get(`/api/v1/tests/${id}`))).status).toBe('ABORTED')
 await page.goto(`/#/report/${id}?section=execution&${scopeQuery(workspace)}`)
 const retained = page.getByRole('region', { name: 'Live data tables', exact: true })
 await expect(retained.getByRole('heading', { name: 'work', exact: true })).toBeVisible()
 await expect(retained).toContainText('Closed')
 await expect(retained).toContainText('Finalization: complete.')
 await expect(retained).not.toContainText('preview-only@example.test')
})
