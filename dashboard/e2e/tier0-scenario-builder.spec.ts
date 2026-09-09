import { expect, test, type APIRequestContext, type Response } from '@playwright/test'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

function record(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== 'object') {
    throw new Error(`${what} was not an object: ${JSON.stringify(value)}`)
  }
  return value as Record<string, unknown>
}

async function createScript(request: APIRequestContext, workspace: Workspace): Promise<string> {
  const response = await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Tier_0_Checkout_Script',
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

// Tier-0 journey step S5 (docs/qa/TIER0_JOURNEY.md): traverse all six
// Scenario Builder steps, prove state survives backward/forward navigation,
// and require the Review step and stored API resource to agree with the draft.
test('[U09] the six-step scenario builder preserves the draft and reviews the saved values', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scriptId = await createScript(request, workspace)
  const scenarioName = 'Tier-0 Checkout Scenario'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await expect(page.getByRole('heading', { level: 4, name: 'Start With A Model' })).toBeVisible({ timeout: 15_000 })

  await page.getByRole('button', { name: 'Build from Workload Shape' }).click()

  await expect(page.getByRole('heading', { level: 4, name: 'Scripts And Groups' })).toBeVisible()
  await page.getByPlaceholder('Scenario name').fill(scenarioName)
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  const scriptRow = page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' })
  await scriptRow.getByRole('button', { name: 'Use for Group 1' }).click()

  await steps.getByRole('button', { name: /^Shape/ }).click()
  const advancedShapes = page.locator('.scenario-advanced-shapes')
  await expect(advancedShapes).not.toHaveAttribute('open', '')
  await expect(page.getByRole('button', { name: 'Use Stepped Ramp', exact: true })).toBeHidden()
  await page.screenshot({ path: 'test-results/scenario-shapes-progressive.png', fullPage: true })
  await advancedShapes.locator(':scope > summary').click()
  await expect(page.getByRole('button', { name: 'Use Stepped Ramp', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Run Settings' })).toBeVisible()
  await page.getByLabel('Sustained load', { exact: true }).fill('2')
  await page.getByLabel('Sustained load Unit').selectOption('m')
  await page.getByLabel('Ramp-up', { exact: true }).fill('1')
  const vusers = page.getByLabel('Peak VUsers', { exact: true })
  await vusers.fill('17')

  await steps.getByRole('button', { name: /^Data/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Data Pools' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Data Pool' })).toHaveValue('')

  // Walk backward and forward through the footer controls instead of jumping
  // directly to Review. These checks catch a wizard that remounts stale state.
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByLabel('Sustained load', { exact: true })).toHaveValue('2')
  await expect(page.getByLabel('Sustained load Unit')).toHaveValue('m')
  await expect(vusers).toHaveValue('17')
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(scenarioName)
  await expect(page.getByRole('button', { name: 'Choose script for Tier_0_Checkout_Script' })).toContainText('Tier_0_Checkout_Script')
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Review And Validate' })).toBeVisible()
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
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Scripts/ }).click()
  await page.getByPlaceholder('Scenario name').fill(draftName)
  await page.getByRole('button', { name: 'Dashboard', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Unsaved Changes' })
  await expect(dialog).toContainText('Scenario Builder has unsaved changes')
  await dialog.getByRole('button', { name: 'Keep editing' }).click()

  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(/#\/tests\?view=builder/)
  await expect(page.getByRole('heading', { level: 1, name: 'Scenario Builder' })).toBeVisible()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(draftName)
})

test('[U09] source switching, runtime scope and save recovery keep the scenario honest', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: 'Build from Workload Shape' }).click()
  await page.getByPlaceholder('Scenario name').fill('Scenario audit regression')
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await expect(page.getByRole('button', { name: 'Choose script for Tier_0_Checkout_Script' })).toBeFocused()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await steps.getByRole('button', { name: /^Shape/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Shape/ }).click()
  await page.getByLabel('Workload source', { exact: true }).selectOption('csv_driven')
  await expect(page.locator('.scenario-builder-preview')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeDisabled()
  await expect(steps.getByRole('button', { name: /^Shape/ })).not.toHaveClass(/complete/)
  await page.getByLabel('Workload source', { exact: true }).selectOption('shape')
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await page.getByRole('tab', { name: 'Browser/Script Runtime', exact: true }).click()
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
  await page.getByRole('button', { name: 'Save & Create Test Run', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Fixture: scenario save unavailable')
  await expect(page.getByRole('alert')).toBeFocused()
  await expect(page.getByRole('alert')).not.toContainText('Scenario was saved')
  await expect(page.getByRole('textbox', { name: 'Scenario name', exact: true })).toHaveValue('Scenario audit regression')
  await page.unroute('**/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scenario Library', level: 1 })).toBeVisible()
  await expect(page.locator('.scenario-save-notice')).toContainText('Saved Scenario audit regression')
  const row = page.getByRole('row').filter({ hasText: 'Scenario audit regression' })
  await row.getByRole('button', { name: 'Edit', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await expect(page.getByLabel('Peak VUsers', { exact: true })).toHaveValue('5')
  await expect(page.getByLabel('Sustained load', { exact: true })).toHaveValue('3')
  await page.setViewportSize({ width: 600, height: 900 })
  const save = page.getByRole('button', { name: 'Save draft', exact: true })
  await save.scrollIntoViewIfNeeded()
  const bounds = await save.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(600)
  const chartBounds = await page.locator('.scenario-model-chart .recharts-responsive-container').boundingBox()
  const legendBounds = await page.locator('.scenario-model-legend').boundingBox()
  expect(chartBounds).not.toBeNull()
  expect(legendBounds).not.toBeNull()
  expect(chartBounds!.y + chartBounds!.height).toBeLessThanOrEqual(legendBounds!.y)
  await page.screenshot({ path: 'test-results/scenario-builder-review.png', fullPage: true })
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Scenario Library', level: 1 })).toBeVisible()
  await page.goBack()
  await steps.getByRole('button', { name: /^Scripts/ }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Choose script for Group 1' })).toContainText('Add Script')
})
