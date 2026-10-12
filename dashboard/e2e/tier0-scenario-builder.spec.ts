import { expect, test, type APIRequestContext, type Response } from '@playwright/test'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

function record(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== 'object') {
    throw new Error(`${what} was not an object: ${JSON.stringify(value)}`)
  }
  return value as Record<string, unknown>
}

async function createScript(request: APIRequestContext, workspace: Workspace, name = 'Tier_0_Checkout_Script'): Promise<string> {
  const response = await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name,
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://tier0.invalid' },
        actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout' }],
      },
    },
  })
  const text = await response.text()
  expect(response.ok(), text).toBeTruthy()
  const payload = record(JSON.parse(text), 'create script response')
  const stored = payload.script && typeof payload.script === 'object'
    ? record(payload.script, 'created script')
    : payload
  if (typeof stored.id !== 'string' || stored.id === '') {
    throw new Error(`create script response carried no id: ${text.slice(0, 300)}`)
  }
  return stored.id
}

function isScenarioCreate(response: Response): boolean {
  return response.request().method() === 'POST'
    && new URL(response.url()).pathname === '/api/v1/scenarios'
}

// Tier-0 journey step S5 (docs/qa/E2E_SUITES.md): traverse all five
// Scenario builder steps, prove state survives backward/forward navigation,
// and require the Review step and stored API resource to agree with the draft.
test('[U09] the five-step scenario builder preserves the draft and reviews the saved values', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scriptId = await createScript(request, workspace)
  const scenarioName = 'Tier-0 Checkout Scenario'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  // Scripts come first: the load model is chosen once the participating scripts are known.
  await expect(page.getByRole('heading', { level: 4, name: 'Scripts and groups' })).toBeVisible({ timeout: 15_000 })
  await page.getByPlaceholder('Scenario name').fill(scenarioName)
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  const scriptRow = page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' })
  await scriptRow.getByRole('button', { name: 'Use for Group 1' }).click()

  await steps.getByRole('button', { name: /^Model/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Choose a load model' })).toBeVisible()
  // Shapes are revealed per model family rather than as one long catalog.
  await expect(page.getByRole('button', { name: 'Use Closed VUser', exact: true })).toBeHidden()
  await page.getByRole('button', { name: /^Stepped Ramp/ }).click()
  await expect(page.getByRole('button', { name: 'Use Stepped Ramp', exact: true })).toBeVisible()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await expect(page.getByRole('button', { name: 'Use Stepped Ramp', exact: true })).toBeHidden()
  await page.screenshot({ path: 'test-results/scenario-model-families.png', fullPage: true })
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run settings/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Run settings' })).toBeVisible()
  const scenarioTabs = page.getByRole('tablist', { name: 'Run settings sections' })
  await scenarioTabs.getByRole('tab', { name: 'Run phases' }).click()
  await page.getByLabel('Sustained load', { exact: true }).fill('2')
  await page.getByLabel('Sustained load Unit').selectOption('m')
  await page.getByLabel('Ramp-up', { exact: true }).fill('1')
  await scenarioTabs.getByRole('tab', { name: 'Target load' }).click()
  const vusers = page.getByLabel('Peak VUsers', { exact: true })
  await vusers.fill('17')

  await steps.getByRole('button', { name: /^Data/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Data pools' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Data Pool' })).toHaveValue('')

  // Walk backward and forward through the footer controls instead of jumping
  // directly to Review. These checks catch a wizard that remounts stale state.
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await scenarioTabs.getByRole('tab', { name: 'Run phases' }).click()
  await expect(page.getByLabel('Sustained load', { exact: true })).toHaveValue('2')
  await expect(page.getByLabel('Sustained load Unit')).toHaveValue('m')
  await scenarioTabs.getByRole('tab', { name: 'Target load' }).click()
  await expect(vusers).toHaveValue('17')
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(scenarioName)
  await expect(page.getByRole('button', { name: 'Tier_0_Checkout_Script: change the script for Tier_0_Checkout_Script' })).toContainText('Tier_0_Checkout_Script')
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Review and validate' })).toBeVisible()
  const review = page.locator('.scenario-review-grid')
  await expect(page.getByRole('textbox', { name: 'Scenario name', exact: true })).toHaveValue(scenarioName)
  await expect(review).toContainText('3m')
  await expect(review).toContainText('VUser driven')
  await expect(review).toContainText('No data pool')
  const reviewRow = page.getByRole('table', { name: 'Scenario review groups' }).getByRole('row').nth(1)
  await expect(reviewRow).toContainText('Tier_0_Checkout_Script')
  await expect(reviewRow.getByRole('cell', { name: '17', exact: true })).toBeVisible()

  const createResponsePromise = page.waitForResponse(isScenarioCreate)
  await page.getByRole('button', { name: 'Save draft' }).click()
  const createResponse = await createResponsePromise
  expect(createResponse.ok(), await createResponse.text()).toBeTruthy()

  const scenariosResponse = await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`)
  const scenariosText = await scenariosResponse.text()
  expect(scenariosResponse.ok(), scenariosText).toBeTruthy()
  const scenariosPayload = record(JSON.parse(scenariosText), 'list scenarios response')
  const scenarios = Array.isArray(scenariosPayload.scenarios) ? scenariosPayload.scenarios : []
  expect(scenarios).toEqual(expect.arrayContaining([
    expect.objectContaining({
      name: scenarioName,
      duration: '3m',
      scripts: expect.arrayContaining([
        expect.objectContaining({ script_id: scriptId, vusers: 17 }),
      ]),
    }),
  ]))
})

test('[U09] canceling unsaved navigation keeps the scenario draft in the builder', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  const draftName = 'Do Not Lose This Draft'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Start/ }).click()
  await page.getByPlaceholder('Scenario name').fill(draftName)
  await page.getByRole('button', { name: 'Dashboard', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Unsaved Changes' })
  await expect(dialog).toContainText('Scenario builder has unsaved changes')
  await dialog.getByRole('button', { name: 'Keep editing' }).click()

  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(/#\/tests\?view=builder/)
  await expect(page.getByRole('heading', { level: 1, name: 'Scenarios' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Scenario builder', pressed: true })).toBeVisible()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(draftName)
})

test('[U09] source switching, runtime scope and save recovery keep the scenario honest', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.getByPlaceholder('Scenario name').fill('Scenario audit regression')
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await expect(page.getByRole('button', { name: 'Tier_0_Checkout_Script: change the script for Tier_0_Checkout_Script' })).toBeFocused()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Traffic Profile/ }).click()
  await page.getByRole('button', { name: 'Exact CSV schedule', exact: true }).click()
  await expect(page.locator('.scenario-builder-preview')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeEnabled()
  await expect(steps.getByRole('button', { name: /^Model/ })).not.toHaveClass(/complete/)
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run settings/ }).click()
  await page.getByRole('tab', { name: 'Browser/script runtime', exact: true }).click()
  await expect(page.getByLabel('Runtime scope', { exact: true })).toHaveValue('defaults')
  await expect(page.locator('.runtime-override-tabs')).toHaveCount(1)
  await expect(page.locator('.runtime-override-summary')).toHaveAttribute('data-table-pagination', 'off')
  await page.getByLabel('Runtime scope', { exact: true }).selectOption('0')
  await expect(page.getByRole('heading', { name: /Runtime overrides · Tier_0/ })).toBeVisible()
  await expect(page.locator('.runtime-override-tabs')).toHaveCount(1)
  await steps.getByRole('button', { name: /^Review/ }).click()
  await page.route('**/api/v1/scenarios', async route => {
    if (route.request().method() === 'POST') await route.fulfill({ status: 503, json: { error: 'Fixture: scenario save unavailable' } })
    else await route.continue()
  })
  await page.getByRole('button', { name: 'Save and configure run', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Fixture: scenario save unavailable')
  await expect(page.getByRole('alert')).toBeFocused()
  await expect(page.getByRole('alert')).not.toContainText('Scenario was saved')
  await expect(page.getByRole('textbox', { name: 'Scenario name', exact: true })).toHaveValue('Scenario audit regression')
  await page.unroute('**/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scenarios', level: 1 })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Scenario builder', pressed: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Review and validate', level: 4 })).toBeVisible()
  await expect(page.locator('.scenario-save-notice')).toContainText('Saved Scenario audit regression')
  await steps.getByRole('button', { name: /^Run settings/ }).click()
  await expect(page.getByRole('tab', { name: 'Browser/script runtime', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByLabel('Runtime scope', { exact: true })).toHaveValue('0')
  await page.getByRole('tab', { name: 'Target load', exact: true }).click()
  await expect(page.getByLabel('Peak VUsers', { exact: true })).toHaveValue('5')
  await page.getByRole('tablist', { name: 'Run settings sections' }).getByRole('tab', { name: 'Run phases' }).click()
  await expect(page.getByLabel('Sustained load', { exact: true })).toHaveValue('3')
  await page.setViewportSize({ width: 600, height: 900 })
  const save = page.getByRole('button', { name: 'Save draft', exact: true })
  await save.scrollIntoViewIfNeeded()
  const bounds = await save.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(600)
  // ResponsiveContainer settles through ResizeObserver after viewport changes.
  // Measure both rectangles in one browser frame, then wait for that layout.
  await expect.poll(() => page.locator('.scenario-model-preview-panel').evaluate(panel => {
    const chart = panel.querySelector('.scenario-model-chart .recharts-responsive-container')!.getBoundingClientRect()
    const legend = panel.querySelector('.scenario-model-chart .load-plan-legend')!.getBoundingClientRect()
    return legend.y - chart.bottom
  })).toBeGreaterThanOrEqual(0)
  await page.screenshot({ path: 'test-results/scenario-builder-review.png', fullPage: true })
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scenarios', level: 1 })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Scenario library', pressed: true })).toBeVisible()
  await page.goBack()
  await steps.getByRole('button', { name: /^Start/ }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Add script to Group 1' })).toContainText('Add script')
})


test('[U09] script groups are one row each and stack their fields on narrow screens', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  const groups = page.locator('.scenario-script-group-list-spacious .scenario-script-group-card')
  for (let index = 1; index <= 5; index++) {
    if (index > 1) await page.locator('.scenario-add-group-card').click()
    await page.getByRole('button', { name: `Add script to Group ${index}`, exact: true }).click()
    await page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' })
      .getByRole('button', { name: `Use for Group ${index}`, exact: true }).click()
  }
  await expect(groups).toHaveCount(5)
  const table = page.getByRole('table', { name: 'Script groups', exact: true })
  await expect(table.getByRole('columnheader')).toHaveText(['Group', 'Script', 'VUsers', 'Initialization', 'Role', 'Actions'])
  // Wide: one row per group, its cells side by side, every column in view.
  const layout = () => groups.evaluateAll(elements => elements.map(element => {
    const row = element.getBoundingClientRect()
    const cells = Array.from(element.querySelectorAll(':scope > [role="cell"]')).map(cell => cell.getBoundingClientRect())
    return { x: row.x, y: row.y, width: row.width, height: row.height, cellTops: cells.map(cell => Math.round(cell.top)), cellLefts: cells.map(cell => Math.round(cell.left)), cellRight: Math.max(...cells.map(cell => cell.right)) }
  }))
  const wide = await layout()
  for (let index = 0; index < wide.length; index++) {
    expect(new Set(wide[index].cellTops).size).toBe(1)
    expect(wide[index].cellLefts).toEqual([...wide[index].cellLefts].sort((left, right) => left - right))
    expect(wide[index].cellRight).toBeLessThanOrEqual(wide[index].x + wide[index].width)
    if (index > 0) expect(wide[index].y).toBeGreaterThanOrEqual(wide[index - 1].y + wide[index - 1].height - 1)
  }
  await page.screenshot({ path: 'test-results/scenario-builder-group-rows.png', fullPage: true })
  // Just above the stacking width the columns still fit, none cut off.
  await page.setViewportSize({ width: 961, height: 900 })
  for (const row of await layout()) {
    expect(new Set(row.cellTops).size).toBe(1)
    expect(row.cellRight).toBeLessThanOrEqual(row.x + row.width)
  }
  expect(await table.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
  // Narrow: each row stacks its fields, labelled, within the screen.
  await page.setViewportSize({ width: 600, height: 900 })
  await expect(table.getByRole('columnheader').first()).toBeHidden()
  await expect(groups.first().getByText('Group name', { exact: true })).toBeVisible()
  const narrow = await layout()
  for (let index = 0; index < narrow.length; index++) {
    expect(narrow[index].x).toBeGreaterThanOrEqual(0)
    expect(narrow[index].x + narrow[index].width).toBeLessThanOrEqual(600)
    expect(new Set(narrow[index].cellTops).size).toBe(narrow[index].cellTops.length)
    if (index > 0) expect(narrow[index].y).toBeGreaterThanOrEqual(narrow[index - 1].y + narrow[index - 1].height - 1)
  }
})

test('[U09] incomplete browser drafts survive reload without creating scenarios', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await expect(page.getByRole('heading', { name: 'Scripts and groups' })).toBeVisible()
  // Visiting the model step is progress worth checkpointing even before anything is chosen.
  await steps.getByRole('button', { name: /^Model/ }).click()
  await steps.getByRole('button', { name: /^Start/ }).click()
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Saved browser draft' })).toContainText('Untitled draft')
  await page.reload()
  await page.getByRole('button', { name: 'Resume draft', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scripts and groups' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add script to Group 1' })).toBeVisible()
  const response = await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`)
  expect(response.ok()).toBeTruthy()
  expect((await response.json()).scenarios || []).toHaveLength(0)
  await page.getByRole('button', { name: 'Clear saved draft', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Saved browser draft' })).toHaveCount(0)
})

test('[U09] CSV schedule suggestions round-trip through preview and scenario storage', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.getByPlaceholder('Scenario name').fill('CSV roundtrip verification')
  await page.getByRole('button', { name: 'Add script to Group 1', exact: true }).click()
  await page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1', exact: true }).click()
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Traffic Profile/ }).click()
  await page.getByRole('button', { name: 'Exact CSV schedule', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Download sample for these groups' })).toHaveAttribute('download', 'scenario-schedule.csv')
  await page.locator('.csv-driven-panel input[type=file]').setInputFiles({ name: 'schedule.csv', mimeType: 'text/csv', buffer: Buffer.from('elapsed,Tier_0_Checkout_Script\n0s,5\n60s,17\n120s,0\n') })
  await page.getByRole('button', { name: 'Inspect columns', exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Time column', exact: true })).toHaveValue('elapsed')
  await expect(page.getByRole('combobox', { name: 'Time values', exact: true })).toHaveValue('relative')
  await page.getByRole('button', { name: 'Analyze schedule', exact: true }).click()
  await expect(page.getByLabel('Map Tier_0_Checkout_Script', { exact: true })).toHaveValue('Tier_0_Checkout_Script')
  await page.getByRole('button', { name: 'Build exact schedule preview', exact: true }).click()
  await page.getByRole('button', { name: 'Apply CSV schedule', exact: true }).click()
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Review/ }).click()
  const savedResponse = page.waitForResponse(isScenarioCreate)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  const saved = await savedResponse
  expect(saved.ok(), await saved.text()).toBeTruthy()
  const scenario = await saved.json()
  expect(scenario.traffic_profile.mappings[0].points.map((point: { target: number }) => point.target)).toEqual([5, 17, 0])
  expect(scenario.traffic_profile.runtime_target).toBe('vusers')
})

test('[U09] data pools support CSV preview, import and multiple editable bindings', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.getByPlaceholder('Scenario name').fill('Data binding verification')
  await page.getByRole('button', { name: 'Add script to Group 1', exact: true }).click()
  await page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Data/ }).click()
  await page.getByText('Create a data pool without leaving this draft', { exact: true }).click()
  await page.getByLabel('New data pool name', { exact: true }).fill('Customers CSV')
  await page.getByRole('textbox', { name: 'First data row (JSON object)', exact: true }).fill('')
  await page.getByRole('button', { name: 'Create and attach data pool', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Pool Alias', exact: true })).toHaveValue('customers_csv')
  await page.getByText('Preview rows and import CSV', { exact: true }).click()
  await page.locator('.scenario-data-editor input[type=file]').setInputFiles({ name: 'customers.csv', mimeType: 'text/csv', buffer: Buffer.from('email,name\na@example.test,Alice\nb@example.test,Bob\n') })
  await page.getByRole('button', { name: 'Preview CSV import', exact: true }).click()
  await expect(page.getByRole('table', { name: 'CSV data preview', exact: true })).toContainText('a@example.test')
  await page.getByRole('button', { name: 'Import rows into shared pool', exact: true }).click()
  await expect(page.getByRole('table', { name: 'Rows for customers_csv', exact: true })).toContainText('Bob')
  await page.getByRole('button', { name: 'Add data pool binding', exact: true }).click()
  const pools = page.getByRole('combobox', { name: 'Data Pool', exact: true })
  const poolId = await pools.first().inputValue()
  await pools.nth(1).selectOption(poolId)
  await page.getByRole('textbox', { name: 'Pool Alias', exact: true }).nth(1).fill('secondary')
  await expect(page.getByRole('textbox', { name: 'Pool Alias', exact: true })).toHaveCount(2)
  await page.getByRole('button', { name: 'Remove binding 1', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Pool Alias', exact: true })).toHaveValue('secondary')
})

test('[U09] narrow picker keeps actions visible and bulk-adds filtered scripts', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await createScript(request, workspace, 'Tier_0_Second_Script')
  await page.setViewportSize({ width: 600, height: 900 })
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: 'Add script to Group 1', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Choose script', exact: true })
  await dialog.getByRole('combobox', { name: 'Readiness', exact: true }).selectOption('unblocked')
  await dialog.getByRole('combobox', { name: 'Protocol', exact: true }).selectOption('http')
  const action = dialog.getByRole('button', { name: 'Use for Group 1', exact: true }).first()
  await action.scrollIntoViewIfNeeded()
  await expect(action).toBeVisible()
  const box = await action.boundingBox()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(600)
  await dialog.getByRole('checkbox', { name: 'Select Tier_0_Checkout_Script for bulk addition', exact: true }).check()
  await dialog.getByRole('checkbox', { name: 'Select Tier_0_Second_Script for bulk addition', exact: true }).check()
  await dialog.getByRole('button', { name: 'Add selected scripts (2)', exact: true }).click()
  await expect(page.locator('.scenario-script-group-card')).toHaveCount(2)
  await expect(page.getByRole('button', { name: 'Tier_0_Second_Script: change the script for Tier_0_Second_Script', exact: true })).toBeVisible()
})
