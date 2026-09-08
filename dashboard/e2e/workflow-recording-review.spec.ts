import { test, expect } from '@playwright/test'

// Deterministic recording/replay fixtures exercise the review UI without
// requiring a headed workstation browser in CI. Go integration tests cover capture.
test('recording review scopes verification to outputs and invalidates changed conversions', async ({ page }, testInfo) => {
  const now = new Date().toISOString()
  const session = { id: 'review-recording', name: 'Checkout', status: 'stopped', start_url: 'https://shop.example', started_at: now, stopped_at: now, capture_har: true, har_available: true, step_count: 2, navigation_count: 1, network_request_count: 2, websocket_count: 0, warning_count: 0, steps: [
    { seq: 1, kind: 'click', title: 'Shop', timestamp: now, element: { text: 'Continue' } },
    { seq: 2, kind: 'click', title: 'Shop', timestamp: now, element: { text: 'Finish journey' } },
  ] }
  const scripts = ['browser', 'http'].map(protocol => ({ name: `Checkout ${protocol}`, protocol, version: '1.0.0', config: {}, init: [{ type: 'think', name: 'Prepare' }], actions: [{ type: protocol === 'browser' ? 'browser_click' : 'request', name: 'Continue' }], teardown: [] }))
  let verification: Record<string, unknown> | undefined
  await page.route('**/api/v1/browser-recorder/sessions/review-recording**', async route => {
    const path = new URL(route.request().url()).pathname
    const response = path.endsWith('/convert/preview') ? { script: scripts[0], scripts, warnings: [], stats: {} }
      : path.endsWith('/verify') ? (() => { verification = route.request().postDataJSON(); return { replay_session_id: verification!.replay_session_id, warnings: [] } })()
      : { session }
    await route.fulfill({ json: response })
  })
  await page.route('**/api/v1/browser-replay/sessions/*', async route => {
    const id = new URL(route.request().url()).pathname.split('/').pop()
    await route.fulfill({ json: { session: { id, status: 'completed', completed_at: now, report: { replay_session_id: id, group: 'Checkout http', protocol: 'http', script_hash: 'fixture-output-hash', started_at: now, completed_at: now, success: true, events: [] } } } })
  })
  await page.goto('/#/recorder-panel?session=review-recording')
  await expect(page.getByText('Finish journey', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Preview Conversion', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Output 2: Checkout http' })).toBeVisible()
  await page.getByRole('button', { name: 'Verify output 2', exact: true }).click()
  await expect(page.getByText('Verification passed for this conversion', { exact: true })).toBeVisible()
  expect(verification?.script_index).toBe(1)
  expect(verification?.conversion).toMatchObject({ expected_script_count: 2 })
  await expect(page.getByRole('button', { name: 'Save unverified draft', exact: true })).toBeVisible()
  await page.getByLabel('Parameterize inputs', { exact: true }).check()
  await expect(page.getByText('Verification passed for this conversion', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Verify output 2', exact: true })).toBeDisabled()
  await page.screenshot({ path: testInfo.outputPath('recording-output-review.png'), fullPage: true })
  await page.reload()
  await expect(page.getByText('Previous recording verification', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save unverified draft', exact: true })).toBeVisible()
})

test('a narrow browser cockpit keeps assertion evidence and the captured viewport readable', async ({ page, request }, testInfo) => {
  const { createWorkspace, scopeQuery } = await import('./support/workspace')
  const workspace = await createWorkspace(request)
  const script = { name: 'Browser evidence layout', version: '1.0.0', protocol: 'browser', config: {}, actions: [{ name: 'Check greeting', type: 'browser_wait', selector: '#result' }] }
  const created = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })
  expect(created.ok(), await created.text()).toBeTruthy()
  const payload = await created.json()
  const id = payload.script?.id || payload.id
  const now = new Date().toISOString()
  const event = { action_index: 0, phase: 'action', action_name: 'Check greeting', action_type: 'browser_wait', selector: '#result', target: '#result', success: false, started_at: now, completed_at: now, error_message: 'Greeting did not match', protocol_evidence: { assertion_failure: { type: 'element_text_contains', expected: 'Welcome operator', actual: 'Hello operator' } }, screenshot_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0nQAAAAASUVORK5CYII=' }
  await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
    const sessionId = route.request().postDataJSON().replay_session_id
    const report = { replay_session_id: sessionId, group: 'Browser evidence layout', protocol: 'browser', started_at: now, completed_at: now, success: false, events: [event] }
    await route.fulfill({ json: { debug_session_id: sessionId, replay_session_id: sessionId, session: { id: sessionId, status: 'failed', completed_at: now, report } } })
  })
  await page.setViewportSize({ width: 700, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  const cockpit = page.locator('.script-browser-evidence')
  await expect(cockpit.locator('[aria-label="Assertion result"]')).toContainText('Hello operator')
  await expect(page.locator('.topbar-debug-action-step')).toContainText('Restart step')
  const bounds = await cockpit.boundingBox()
  const viewport = await cockpit.locator('.selected-evidence-screenshot').boundingBox()
  expect(viewport!.width).toBeGreaterThan(bounds!.width * 0.8)
  await page.screenshot({ path: testInfo.outputPath('browser-evidence-narrow.png'), fullPage: true })
})
