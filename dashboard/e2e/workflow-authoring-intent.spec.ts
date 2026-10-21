import type { APIRequestContext } from '@playwright/test'
import { test, expect, login, seedScript, json, idOf } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

// AUTH-1 (docs/ROADMAP_IMPLEMENTATION_SPEC.md#auth-a--durable-user-intent-and-explicit-receipt-semantics):
// a create whose response is lost, followed by a browser reload, must replay
// the object the first attempt created instead of creating another.

test.beforeEach(async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
})

async function createScript(request: APIRequestContext, workspace: Workspace): Promise<void> {
  const response = await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Intent_Checkout_Script', version: '1.0.0', protocol: 'http',
        config: { base_url: 'https://intent.invalid' },
        actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout' }],
      },
    },
  })
  expect(response.ok(), await response.text()).toBeTruthy()
}

async function scenariosNamed(request: APIRequestContext, workspace: Workspace, name: string): Promise<number> {
  const response = await request.get(`/api/v1/scenarios?project_id=${encodeURIComponent(workspace.projectId)}`)
  expect(response.ok(), await response.text()).toBeTruthy()
  const body: unknown = await response.json()
  const items = Array.isArray(body) ? body : (body as { scenarios?: unknown[] }).scenarios ?? []
  return items.filter(item => (item as { name?: string }).name === name).length
}

test('[AUTH-1] a scenario create whose response was lost replays after a reload instead of duplicating', async ({ page }) => {
  test.setTimeout(120_000)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  const name = 'Intent lost-response scenario'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('heading', { level: 4, name: 'Scripts and groups' })).toBeVisible({ timeout: 15_000 })
  await page.getByPlaceholder('Scenario name').fill(name)
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Intent_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Review/ }).click()

  // The controller commits the create; the browser never sees the answer.
  const sentKeys: string[] = []
  await page.route('**/api/v1/scenarios', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    sentKeys.push(route.request().headers()['idempotency-key'] ?? '')
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    await route.abort('failed')
  })
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  // The builder reports the failed request and keeps the draft.
  const banner = page.locator('.page-error-banner')
  await expect(banner).toBeVisible()
  await expect(banner).not.toContainText('Saved')
  await expect(page.locator('.scenario-save-notice')).toHaveCount(0)
  await expect.poll(() => scenariosNamed(request, workspace, name)).toBe(1)
  await page.unroute('**/api/v1/scenarios')

  // A reload keeps the draft and the key its unanswered create used.
  const replayed: string[] = []
  page.on('request', sent => {
    if (sent.method() === 'POST' && new URL(sent.url()).pathname === '/api/v1/scenarios') replayed.push(sent.headers()['idempotency-key'] ?? '')
  })
  await page.reload()
  await page.getByRole('region', { name: 'Unsaved work in this browser' }).getByRole('button', { name: 'Resume draft', exact: true }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue(name)
  await steps.getByRole('button', { name: /^Review/ }).click()
  const answer = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  const response = await answer
  expect(response.status()).toBe(201)
  expect(response.headers()['idempotent-replayed']).toBe('true')
  expect(response.headers()['x-apex-authoring-receipt-version']).toBe('2')
  expect(sentKeys).toHaveLength(1)
  expect(sentKeys[0]).toMatch(/^scenario-create-/)
  expect(replayed).toEqual([sentKeys[0]])
  await expect(page.locator('.scenario-save-notice')).toContainText('had already been saved')
  expect(await scenariosNamed(request, workspace, name)).toBe(1)
})

test('[AUTH-1] a queued revised copy whose response was lost replays instead of queueing another', async ({ page, target, trackRun }) => {
  test.setTimeout(120_000)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Intent_Copy_HTTP', target.url)
  const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: {
    name: 'Intent copy source', project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }],
  } })))
  trackRun(sourceId)
  await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Create revised queued run', exact: true })
  const name = 'Intent revised copy'
  await dialog.getByLabel('Scenario Name', { exact: true }).fill(name)

  // The controller queues the copy; the browser never sees the answer.
  const sentKeys: string[] = []
  await page.route('**/api/v1/tests', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    sentKeys.push(route.request().headers()['idempotency-key'] ?? '')
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    trackRun(idOf(await committed.json()))
    await route.abort('failed')
  })
  const create = dialog.getByRole('button', { name: /^Create revised copy \(\d+ groups?\)$/ })
  await create.click()
  await expect(dialog.locator('.form-error')).toBeVisible()
  await page.unroute('**/api/v1/tests')

  const answer = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await create.click()
  const response = await answer
  expect(response.status()).toBe(201)
  expect(response.headers()['idempotent-replayed']).toBe('true')
  expect(sentKeys).toHaveLength(1)
  expect(sentKeys[0]).toMatch(/^test-create-/)
  expect(response.request().headers()['idempotency-key']).toBe(sentKeys[0])
  await expect(dialog.getByRole('status')).toContainText('no new run was queued')
  const runs: unknown = await json(await request.get(`/api/v1/tests?project_id=${encodeURIComponent(workspace.projectId)}`))
  const items = Array.isArray(runs) ? runs : (runs as { tests?: unknown[] }).tests ?? []
  expect(items.filter(item => (item as { name?: string }).name === name)).toHaveLength(1)
  const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  expect(source.name).toBe('Intent copy source')
  expect(target.count()).toBe(0)
})
