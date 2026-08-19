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
test('the six-step scenario builder preserves the draft and reviews the saved values', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scriptId = await createScript(request, workspace)
  const scenarioName = 'Tier-0 Checkout Scenario'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await expect(page.getByRole('heading', { level: 4, name: 'Start With A Model' })).toBeVisible({ timeout: 15_000 })

  await page.getByRole('button', { name: 'Build from Workload Shape' }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Choose Workload Shape' })).toBeVisible()
  await page.getByRole('button', { name: 'Use Manual Shape' }).first().click()

  await expect(page.getByRole('heading', { level: 4, name: 'Scripts And Groups' })).toBeVisible()
  await page.getByPlaceholder('Scenario name').fill(scenarioName)
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  const scriptRow = page.getByRole('row').filter({ hasText: 'Tier_0_Checkout_Script' })
  await scriptRow.getByRole('button', { name: 'Use for Group 1' }).click()

  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Run Settings' })).toBeVisible()
  await page.getByLabel('Duration', { exact: true }).fill('3')
  await page.getByLabel('Duration Unit').selectOption('m')
  const vusers = page.locator('label.scenario-field').filter({ hasText: /^VUsers/ }).locator('input[type="number"]')
  await vusers.fill('17')

  await steps.getByRole('button', { name: /^Data/ }).click()
  await expect(page.getByRole('heading', { level: 4, name: 'Data Pools' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Data Pool' })).toHaveValue('')

  // Walk backward and forward through the footer controls instead of jumping
  // directly to Review. These checks catch a wizard that remounts stale state.
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByLabel('Duration', { exact: true })).toHaveValue('3')
  await expect(page.getByLabel('Duration Unit')).toHaveValue('m')
  await expect(vusers).toHaveValue('17')
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(scenarioName)
  await expect(page.getByRole('button', { name: 'Choose script for Group 1' })).toContainText('Tier_0_Checkout_Script')
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await expect(page.getByRole('heading', { level: 4, name: 'Review And Validate' })).toBeVisible()
  const review = page.locator('.scenario-review-grid')
  await expect(review).toContainText(scenarioName)
  await expect(review).toContainText('3m')
  await expect(review).toContainText('VUser driven')
  await expect(review).toContainText('No data pool')
  const reviewRow = page.getByRole('table', { name: 'Scenario review groups' }).getByRole('row').nth(1)
  await expect(reviewRow).toContainText('Tier_0_Checkout_Script')
  await expect(reviewRow.getByRole('cell', { name: '17', exact: true })).toBeVisible()

  const createResponsePromise = page.waitForResponse(isScenarioCreate)
  await page.getByRole('button', { name: 'Save Scenario' }).click()
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

test('canceling unsaved navigation keeps the scenario draft in the builder', async ({ page, request }) => {
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
