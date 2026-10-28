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

  // A reload keeps the draft and the key its unanswered create used. Only
  // the browser checkpoint may supply that key here: the record of unanswered
  // keys, which would also find it by payload, is cleared first.
  const replayed: string[] = []
  page.on('request', sent => {
    if (sent.method() === 'POST' && new URL(sent.url()).pathname === '/api/v1/scenarios') replayed.push(sent.headers()['idempotency-key'] ?? '')
  })
  await page.evaluate(() => window.localStorage.removeItem('apexload.authoring.create-intents.v1'))
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
  // Creating again replays the same run; only Save as new would queue another.
  const again = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await create.click()
  const repeated = await again
  expect(repeated.headers()['idempotent-replayed']).toBe('true')
  expect(repeated.request().headers()['idempotency-key']).toBe(sentKeys[0])
  const runs: unknown = await json(await request.get(`/api/v1/tests?project_id=${encodeURIComponent(workspace.projectId)}`))
  const items = Array.isArray(runs) ? runs : (runs as { tests?: unknown[] }).tests ?? []
  expect(items.filter(item => (item as { name?: string }).name === name)).toHaveLength(1)
  const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  expect(source.name).toBe('Intent copy source')
  expect(target.count()).toBe(0)
})

test('[AUTH-A1] concurrent create, same-key body conflict, expired receipt, and deleted original resource require explicit Save as new', async ({ page }) => {
  test.setTimeout(120_000)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await createScript(request, workspace)
  const initialName = 'Intent conflict and deleted scenario'

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('heading', { level: 4, name: 'Scripts and groups' })).toBeVisible({ timeout: 15_000 })
  await page.getByPlaceholder('Scenario name').fill(initialName)
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Intent_Checkout_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Review/ }).click()

  // 1. Concurrent create with the same draft intent key (in-progress lease) keeps the key pending,
  // and once the original commit finishes, a changed draft under the same key receives 409 idempotency_key_reused.
  let createdId = ''
  let interceptMode: 'in-progress-then-commit' | 'pass' | 'expired' = 'in-progress-then-commit'
  await page.route('**/api/v1/scenarios', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    if (interceptMode === 'in-progress-then-commit') {
      const committed = await route.fetch()
      expect(committed.status()).toBe(201)
      createdId = idOf(await committed.json())
      interceptMode = 'pass'
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'a request with this Idempotency-Key is already in progress',
          code: 'idempotency_request_in_progress',
        }),
      })
      return
    }
    if (interceptMode === 'expired') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'this Idempotency-Key has expired; start a new create to continue',
          code: 'idempotency_key_expired',
        }),
      })
      return
    }
    await route.continue()
  })

  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  const banner = page.locator('.page-error-banner')
  await expect(banner).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save as new', exact: true })).toHaveCount(0)

  // 2. Same-key/different-body conflict (409 idempotency_key_reused) offers explicit Save as new.
  await page.getByPlaceholder('Scenario name').fill(`${initialName} edited`)
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  await expect(banner).toContainText('choose Save as new')
  await expect(page.getByRole('button', { name: 'Save as new', exact: true })).toBeVisible()

  // 3. Deleted original resource (410 idempotency_resource_deleted) refuses recreation and offers Save as new.
  await page.getByPlaceholder('Scenario name').fill(initialName)
  const deleteRes = await request.delete(`/api/v1/scenarios/${createdId}`)
  expect(deleteRes.ok(), await deleteRes.text()).toBeTruthy()
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  await expect(banner).toContainText('was deleted and will not be recreated')
  await expect(page.getByRole('button', { name: 'Save as new', exact: true })).toBeVisible()

  // 4. Expired receipt handling (409 idempotency_key_expired) offers explicit Save as new,
  // and clicking Save as new rotates the key and creates a fresh scenario.
  interceptMode = 'expired'
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  await expect(banner).toContainText('more than 24 hours old')
  await expect(page.getByRole('button', { name: 'Save as new', exact: true })).toBeVisible()

  await page.unroute('**/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save as new', exact: true }).click()
  await expect(page.locator('.scenario-save-notice')).toContainText(`Saved ${initialName}`)
  expect(await scenariosNamed(request, workspace, initialName)).toBe(1)
})

test('[AUTH-B1] scenario validation marks held results stale with a named reason when constituent scripts change and ignores transient context probe failures', async ({ page }) => {
  test.setTimeout(120_000)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const createRes = await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Context_Probe_Script', version: '1.0.0', protocol: 'http',
        config: { base_url: 'https://intent.invalid' },
        actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout' }],
      },
    },
  })
  expect(createRes.ok(), await createRes.text()).toBeTruthy()
  const scriptId = idOf(await createRes.json())

  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('heading', { level: 4, name: 'Scripts and groups' })).toBeVisible({ timeout: 15_000 })
  await page.getByPlaceholder('Scenario name').fill('Context stale notice scenario')
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Context_Probe_Script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Review/ }).click()

  await page.getByRole('button', { name: 'Validate load model', exact: true }).click()
  await expect(page.locator('.scenario-load-validation')).toBeVisible()
  await expect(page.locator('.scenario-validation-stale-notice')).toHaveCount(0)

  // A transient 503 on context recheck never falsely marks the held validation stale.
  await page.route('**/api/v1/scenarios/validate-load-model?context_only=true', async route => {
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'validation context is temporarily unavailable' }),
    })
  })
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(page.locator('.scenario-load-validation')).toBeVisible()
  await expect(page.locator('.scenario-validation-stale-notice')).toHaveCount(0)
  await page.unroute('**/api/v1/scenarios/validate-load-model?context_only=true')

  // Updating the constituent script changes the server's ScenarioValidationContext scripts digest;
  // on the next focus probe after backoff, the held validation is marked stale with the named reason.
  const updateRes = await request.put(`/api/v1/scripts/${scriptId}`, {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Context_Probe_Script', version: '1.0.1', protocol: 'http',
        config: { base_url: 'https://intent.invalid' },
        actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout/v2' }],
      },
    },
  })
  expect(updateRes.ok(), await updateRes.text()).toBeTruthy()

  await page.route('**/api/v1/scenarios/validate-load-model?context_only=true', async route => {
    await route.continue()
  })
  // Re-validate so the probe spacing starts fresh, then verify an unchanged focus probe keeps the validation valid.
  await page.getByRole('button', { name: 'Validate load model', exact: true }).click()
  await expect(page.locator('.scenario-load-validation')).toBeVisible()
  await page.waitForTimeout(5_200)
  const unchangedProbePromise = page.waitForResponse(
    res => res.url().includes('/api/v1/scenarios/validate-load-model?context_only=true') && res.status() === 200,
  )
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await unchangedProbePromise
  await expect(page.locator('.scenario-load-validation')).toBeVisible()
  await expect(page.locator('.scenario-validation-stale-notice')).toHaveCount(0)

  // Now update the script and trigger focus after spacing; held validation is marked stale with the named reason.
  const secondUpdate = await request.put(`/api/v1/scripts/${scriptId}`, {
    data: {
      project_id: workspace.projectId,
      script: {
        name: 'Context_Probe_Script', version: '1.0.2', protocol: 'http',
        config: { base_url: 'https://intent.invalid' },
        actions: [{ name: 'checkout', type: 'request', method: 'GET', target: '/checkout/v3' }],
      },
    },
  })
  expect(secondUpdate.ok(), await secondUpdate.text()).toBeTruthy()
  await page.waitForTimeout(5_200)
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(page.locator('.scenario-validation-stale-notice')).toContainText(
    'Validation results no longer apply: constituent scripts changed since the check. Validate load model again.',
  )
  await expect(page.locator('.scenario-load-validation')).toHaveCount(0)
})
