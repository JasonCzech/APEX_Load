import { test, expect, idOf, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

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
    script: { name, version: '1.0.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: '/orders' }], metadata: { import: { source_format: 'openapi' } } },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const scriptName = page.locator('.side-panel-script-name input')
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
