import { test, expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import type { ScriptDebugRuntimeState } from '../src/types/replay'
import { idOf, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('operator edits the failed nested browser assertion and reruns without losing source fields', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const leaf = { name: 'Check greeting', type: 'browser_wait', target: '#result', wait_for: { type: 'selector', value: '#result', timeout_ms: 10000 }, metadata: { audit_marker: 'preserve' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Welcome' }] }
  const script = { name: 'Nested greeting', version: '1.0.0', protocol: 'browser', config: {}, actions: [{ name: 'Repeat journey', type: 'control_loop', control: { count: 1, steps: [leaf] } }] }
  const created = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })
  expect(created.ok(), await created.text()).toBeTruthy()
  const payload = await created.json(); const id = payload.script?.id || payload.id
  let runs = 0
  let replayExpected = 'Welcome'
  await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
    const body = route.request().postDataJSON(); runs++
    const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
    replayExpected = saved.script.actions[0].control.steps[0].assertions[0].expected
    const now = new Date().toISOString()
    const passed = replayExpected === 'Hello'
    // Go omits the zero action index in JSON. Repair must still find the first root.
    const event = { phase: 'action', step_path: ['loop[0]'], iteration: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: passed, duration_ms: 1, started_at: now, completed_at: now, error_message: passed ? '' : 'Greeting mismatch', protocol_evidence: passed ? {} : { assertion_failure: { type: 'element_text_contains', expected: 'Welcome', actual: 'Hello' } } }
    const report = { replay_session_id: body.replay_session_id, group: 'Nested greeting', protocol: 'browser', started_at: now, completed_at: now, success: passed, events: [event] }
    const sessionIncarnation = randomUUID()
    const runtimeState = { version: 1, kind: 'full', session_id: body.replay_session_id,
      session_incarnation: sessionIncarnation, state_revision: '1', state: 'unavailable',
      reason: 'session_terminal', snapshot: null } satisfies ScriptDebugRuntimeState
    await route.fulfill({ json: { debug_session_id: body.replay_session_id, replay_session_id: body.replay_session_id,
      session: { id: body.replay_session_id, session_incarnation: sessionIncarnation, state_revision: '1',
        runtime_state: runtimeState, status: passed ? 'completed' : 'failed', completed_at: now, report } } })
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByRole('button', { name: 'Edit this assertion' })).toBeVisible()
  await expect(page.locator('.script-browser-evidence')).toHaveCount(1)
  await expect(page.getByRole('region', { name: 'Debug detail', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Edit this assertion' }).click()
  await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  const expected = page.getByLabel('Assertion 1 expected value', { exact: true })
  await expect(expected).toHaveValue('Welcome')
  await expect(expected).toBeFocused()
  await expected.fill('Hello')
  await expect(page.getByLabel('Script evidence states')).toContainText('stale')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  expect(saved.script.actions[0].control.steps[0]).toMatchObject({ metadata: { audit_marker: 'preserve' }, wait_for: leaf.wait_for, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Hello' }] })
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
  expect(runs).toBe(2)
  expect(replayExpected).toBe('Hello')
  await page.screenshot({ path: testInfo.outputPath('script-debug-evidence.png'), fullPage: true })
})

test('authored script goals survive an unrelated editor save and canonical reopen', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const goals = {
    target_average_tps: 8.75, p95_latency_ms: 217.5, max_error_pct: 0,
    min_vusers: 1, max_vusers: 6, max_error_count: 17,
    stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '11s',
    transaction_rules: [{ population: 'transaction', protocol: 'http', group: 'checkout', transaction: 'Purchase', metric: 'p95_duration_ms', comparator: 'lte', threshold: 215.25, severity: 'fail' }],
  }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Durable script goals', version: '1.0', protocol: 'http', goal_config: goals,
      config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Purchase', type: 'request', method: 'GET', target: '/' }] },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  const baseURL = workbench.getByRole('textbox', { name: 'Base URL' })
  await expect(baseURL).toHaveValue('http://127.0.0.1:1')
  await baseURL.fill('http://127.0.0.1:2')
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await saved)
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  const canonical = await json(await request.get(`/api/v1/scripts/${id}`))
  expect(canonical.script).toMatchObject({ goal_config: goals, config: { base_url: 'http://127.0.0.1:2' } })
  await page.reload()
  await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(baseURL).toHaveValue('http://127.0.0.1:2')
  await workbench.getByRole('tab', { name: 'Source' }).click()
  const code = page.locator('.script-code-pane')
  // With no action selected, Source already owns the complete canonical JSON.
  // Its virtualized code view must navigate to the authored goals to inspect
  // them; no conditional action-scope toggle is present on this entry path.
  await code.getByRole('button', { name: 'Find', exact: true }).click()
  const find = code.getByPlaceholder('Find', { exact: true })
  await find.pressSequentially('goal_config')
  await find.press('Enter')
  await expect(code.locator('.cm-content')).toContainText('goal_config')
  await find.fill('')
  await find.pressSequentially('217.5')
  await find.press('Enter')
  await expect(code.locator('.cm-content')).toContainText('217.5')
  await find.press('Escape')
  await page.screenshot({ path: testInfo.outputPath('canonical-goals-reopen.png'), fullPage: true })
})
