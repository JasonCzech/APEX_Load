import { test, expect, json, login, seedScript, idOf, editScenarioFromRow } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

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
  await page.getByRole('button', { name: 'Add script to Group 1', exact: true }).click()
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
  await page.getByRole('heading', { name: 'Data pools', exact: true }).scrollIntoViewIfNeeded()
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
 await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({hasText:'Pool parity'}),'Pool parity')
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
 await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({hasText:'Pool parity'}),'Pool parity')
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
 await page.getByRole('group', { name: 'Scenario page view' }).getByRole('button', { name: 'Scenario library', exact: true }).click()
 const row = page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Legacy pool migration' })
 await expect(row).toBeVisible()
 await page.reload()
 await expect(row).toBeVisible()
 await editScenarioFromRow(row, 'Legacy pool migration')
 await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Data/ }).click()
 await expect(page.getByLabel('Pool Alias')).toHaveCount(2)
 expect(await page.getByLabel('Pool Alias').evaluateAll(inputs => inputs.map(input => (input as HTMLInputElement).value))).toEqual(['orders', 'replacement'])
})
