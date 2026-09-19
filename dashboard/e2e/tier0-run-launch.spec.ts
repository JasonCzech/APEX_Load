import { expect, test, type APIRequestContext, type APIResponse, type Response } from '@playwright/test'

import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

async function requireJSON(response: APIResponse | Response, what: string): Promise<Record<string, unknown>> {
  const text = await response.text()
  expect(response.ok(), `${what}: ${text}`).toBeTruthy()
  const value = JSON.parse(text) as unknown
  if (!value || typeof value !== 'object') throw new Error(`${what} returned no object: ${text.slice(0, 300)}`)
  return value as Record<string, unknown>
}

function idFrom(payload: Record<string, unknown>, what: string): string {
  const nested = payload.script && typeof payload.script === 'object'
    ? payload.script as Record<string, unknown>
    : payload
  if (typeof nested.id !== 'string' || !nested.id) throw new Error(`${what} returned no id`)
  return nested.id
}

async function seedRunnableScenario(request: APIRequestContext, workspace: Workspace, baseURL: string): Promise<string> {
  const script = await requireJSON(await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Tier_0_Launch_Script',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: baseURL, pacing: { type: 'fixed_interval', interval_ms: 500 } },
        actions: [{ name: 'health', type: 'http', method: 'GET', target: '/api/v1/system/health' }],
      },
    },
  }), 'create launch script')
  const scriptId = idFrom(script, 'create launch script')

  const scenario = await requireJSON(await request.post('/api/v1/scenarios', {
    data: {
      name: 'Tier-0 Immediate Launch',
      duration: '2m',
      scenario_mode: 'vuser_driven',
      groups: [{ name: 'smoke', script_id: scriptId, vusers: 2, ramp_up: '1s' }],
    },
  }), 'create launch scenario')
  return idFrom(scenario, 'create launch scenario')
}

test('[U10] preflight and estimate lead to an immediate launch that reaches RUNNING', async ({ page, request, baseURL }) => {
  test.setTimeout(150_000)
  if (!baseURL) throw new Error('Tier-0 launch needs Playwright baseURL')

  const workspace = await createWorkspace(request, { label: 'tier0-launch' })
  const scenarioId = await seedRunnableScenario(request, workspace, baseURL)
  const estimateResponses: Response[] = []
  const preflightResponses: Response[] = []
  page.on('response', response => {
    const path = new URL(response.url()).pathname
    if (response.request().method() === 'POST' && path === '/api/v1/tests/estimate') estimateResponses.push(response)
    if (response.request().method() === 'POST' && path === '/api/v1/tests/preflight') preflightResponses.push(response)
  })

  await page.goto(`/#/new-test?scenarioId=${scenarioId}&${scopeQuery(workspace)}`)
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  const startNow = page.getByRole('button', { name: 'Start Now' })
  await expect(startNow, 'launch stays disabled until preflight has made a start-now decision').toBeEnabled({ timeout: 30_000 })
  expect(estimateResponses.some(response => response.ok()), 'the UI must obtain a successful capacity estimate').toBeTruthy()
  expect(preflightResponses.some(response => response.ok()), 'the UI must obtain a successful launch preflight').toBeTruthy()

  const created = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname === '/api/v1/tests')
  await startNow.click()
  const createdPayload = await requireJSON(await created, 'create immediate run')
  const testId = idFrom(createdPayload, 'create immediate run')

  await expect(page).toHaveURL(new RegExp(`(?:command-center|test)/${testId}`), { timeout: 30_000 })
  await expect.poll(async () => {
    const response = await request.get(`/api/v1/tests/${testId}`)
    if (!response.ok()) return ''
    return String(((await response.json()) as Record<string, unknown>).status ?? '')
  }, { timeout: 60_000, message: 'the launched run must reach RUNNING' }).toBe('RUNNING')

  await request.post(`/api/v1/tests/${testId}/abort`)
})
