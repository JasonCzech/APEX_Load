import { test, expect } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

test('operator edits the failed nested browser assertion and reruns without losing source fields', async ({ page, request }, testInfo) => {
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
    await route.fulfill({ json: { debug_session_id: body.replay_session_id, replay_session_id: body.replay_session_id, session: { id: body.replay_session_id, status: passed ? 'completed' : 'failed', completed_at: now, report } } })
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
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
