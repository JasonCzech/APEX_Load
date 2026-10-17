import { test, expect, idOf, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('catalog drafts block stale saves and live-table fields preserve explicit empty strings', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: {
      name: 'Catalog field drafts', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
      actions: [
        { name: 'Increment count', type: 'live_table', params: { alias: 'jobs', operation: 'increment', position: 1, columns: ['count'], delta: '1' } },
        { name: 'Update row', type: 'live_table', params: { alias: 'jobs', operation: 'update', position: 1, expected: { count: '1' }, values: { count: '2' } } },
        { name: 'Publish status', type: 'live_table', params: { alias: 'jobs', operation: 'push', values: { status: 'new' } } },
      ],
    },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const save = page.getByRole('button', { name: 'Save script', exact: true })
  const delta = page.locator('.catalog-action-form').getByLabel(/^Add/)
  await delta.fill('1.5')
  await expect(delta).toHaveAttribute('aria-invalid', 'true')
  await expect(save).toBeDisabled()
  await page.getByRole('treeitem', { name: 'Update row Live Table', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Fix action fields before saving' })).toBeVisible()
  await expect(save).toBeDisabled()
  await page.getByRole('treeitem', { name: 'Increment count Live Table', exact: true }).click()
  await expect(delta).toHaveValue('1.5')
  await delta.fill('9007199254740993')

  await page.getByRole('treeitem', { name: 'Update row Live Table', exact: true }).click()
  const expected = page.getByRole('group', { name: 'Only when the row has', exact: true })
  const values = page.locator('.catalog-values').filter({ has: page.getByRole('table', { name: 'Values', exact: true }) })
  await expected.getByRole('button', { name: 'Set status empty, row 1', exact: true }).click()
  await expect(expected.getByRole('textbox', { name: 'status, row 1', exact: true })).toBeFocused()
  await values.getByRole('button', { name: 'Set status empty, row 1', exact: true }).click()
  await expect(values.getByRole('textbox', { name: 'status, row 1', exact: true })).toBeFocused()
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await save.click()
  expect((await saved).ok()).toBe(true)
  const stored = await json(await request.get(`/api/v1/scripts/${id}`)) as { script: { actions: Array<{ params: Record<string, unknown> }> } }
  expect(stored.script.actions[0].params.delta).toBe('9007199254740993')
  expect(stored.script.actions[1].params.expected).toEqual({ count: '1', status: '' })
  expect(stored.script.actions[1].params.values).toEqual({ count: '2', status: '' })
})

test('catalog JSON is validated before blur and keyboard Save includes the focused field draft', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Catalog JSON draft', version: '1.0', protocol: 'kafka', config: { kafka: { mock: true, brokers: ['127.0.0.1:9092'], topic: 'orders' } }, actions: [{ name: 'Publish order', type: 'kafka_produce', target: 'orders', body: 'order', headers: { source: 'old' } }] },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const headers = page.locator('.catalog-action-form').getByLabel(/^Headers/)
  await headers.fill('{"source":')
  await expect(headers).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  await headers.fill('{"source":"keyboard"}')
  await expect(headers).toBeFocused()
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await headers.press('ControlOrMeta+s')
  expect((await saved).ok()).toBe(true)
  const stored = await json(await request.get(`/api/v1/scripts/${id}`)) as { script: { actions: Array<{ headers: Record<string, string> }> } }
  expect(stored.script.actions[0].headers).toEqual({ source: 'keyboard' })
})

// The script session binds a validation result to the exact draft it was
// issued for. A result that arrives after a further edit is dropped; the next
// validation binds to the edited draft, which the controller confirms by
// echoing its fingerprint; and a later edit makes that evidence stale.
test('a validation that returns after an edit is ignored and the next one binds to the edited draft', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const name = `Session_${Date.now()}`
  // An imported script: a validation result the editor wrongly admitted would
  // be committed into the draft (reverting later edits), so the late result's
  // rejection is observable rather than masked by a recomputed summary.
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name, version: '1.0.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: '/orders' }], metadata: { import: { source: 'openapi' } } },
  } })))
  // The stored script must keep its import metadata, or the check below
  // could not fail.
  type StoredImport = { metadata?: { import?: { source?: string } } }
  const stored = await json(await request.get(`/api/v1/scripts/${id}`)) as StoredImport & { script?: StoredImport }
  expect(stored.metadata?.import?.source ?? stored.script?.metadata?.import?.source).toBe('openapi')
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const scriptName = page.getByRole('textbox', { name: 'Script name', exact: true })
  await expect(scriptName).toHaveValue(name)
  const states = page.getByLabel('Script evidence states')
  const recheck = async () => {
    const checks = page.locator('details.script-health-summary')
    if (!await checks.evaluate(element => (element as HTMLDetailsElement).open)) await checks.locator(':scope > summary').click()
    await checks.locator('.debug-readiness-card').getByRole('button', { name: 'Recheck', exact: true }).click()
  }

  // Hold the first draft validation at the controller boundary.
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  let firstFingerprint = ''
  await page.route('**/api/v1/scripts/validate', async route => {
    firstFingerprint = route.request().postDataJSON().fingerprint
    await held
    await route.continue()
  }, { times: 1 })
  await scriptName.fill(`${name}_edited`)
  await recheck()
  await expect.poll(() => firstFingerprint).toMatch(/^sdf1-[0-9a-f]{16}-[0-9a-f]+$/)

  await scriptName.fill(`${name}_edited_twice`)
  const late = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/scripts/validate')
  release()
  const lateResponse = await late
  expect((await lateResponse.json()).validated_fingerprint).toBe(firstFingerprint)
  // Recheck re-enables once the editor has handled the late response.
  const readiness = page.locator('details.script-health-summary .debug-readiness-card')
  await expect(readiness.getByRole('button', { name: 'Recheck', exact: true })).toBeEnabled()
  await expect(scriptName).toHaveValue(`${name}_edited_twice`)
  await expect(readiness).not.toContainText('Script validation passed')
  await expect(states).toContainText('Validation evidence: not yet')
  await expect(states).not.toContainText('Validation evidence: passed')

  const current = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/scripts/validate')
  await recheck()
  const currentResponse = await current
  const sent = currentResponse.request().postDataJSON()
  expect(sent.script.name).toBe(`${name}_edited_twice`)
  expect(sent.fingerprint).not.toBe(firstFingerprint)
  expect((await currentResponse.json()).validated_fingerprint).toBe(sent.fingerprint)
  await expect(states).toContainText('Validation evidence: passed')

  await scriptName.fill(`${name}_edited_three_times`)
  await expect(states).not.toContainText('Validation evidence: passed')
})
