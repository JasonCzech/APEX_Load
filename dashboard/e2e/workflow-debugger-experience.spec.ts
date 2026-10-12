import { createServer, type Server } from 'node:http'
import { readFile } from 'node:fs/promises'
import { test, expect, idOf, json, login, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

let target: Server
let targetURL = ''
let capturePageGate: Promise<void> | undefined
test.beforeAll(async () => {
  target = createServer(async (req, res) => {
    if (req.url === '/capture-page') {
      await capturePageGate
      res.setHeader('Content-Type', 'text/html'); res.end('<img src="/capture-resource">'); return
    }
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ customer: 'debug-customer', path: req.url, message: 'diagnostic evidence' }))
  })
  await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
  const address = target.address(); if (!address || typeof address === 'string') throw new Error('Missing fixture address')
  targetURL = `http://127.0.0.1:${address.port}`
})
test.afterAll(async () => { if (target) await new Promise<void>((resolve, reject) => target.close(error => error ? reject(error) : resolve())) })

test('debugger captures successful evidence, diagnoses runtime failure, repairs a draft and retains protocol-correct history', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Debugger_operator_journey', version: '1.0', protocol: 'http', config: { base_url: targetURL },
    actions: [{ name: 'Get customer', type: 'request', method: 'GET', target: '/customer', extractors: [{ name: 'customer', source: 'body', type: 'jsonpath', expression: '$.customer' }] }, { name: 'Check order', type: 'request', method: 'GET', target: '/order/{{var.customer}}', assertions: [{ type: 'status_code', expected: 201 }] }, { name: 'Finish', type: 'request', method: 'GET', target: '/finish' }],
  } } })))
  await page.setViewportSize({ width: 1800, height: 1100 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const mode = page.getByRole('group', { name: 'Script workspace' })
  await mode.getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  const checkOptionAlignment = async () => {
    const alignments = await options.locator('label.replay-checkbox').evaluateAll(labels => labels.map(label => {
      const checkbox = label.querySelector('input[type="checkbox"]')!
      return { direction: getComputedStyle(label).flexDirection, inset: checkbox.getBoundingClientRect().left - label.getBoundingClientRect().left, width: checkbox.getBoundingClientRect().width }
    }))
    expect(alignments).toHaveLength(3)
    expect(alignments.every(item => item.direction === 'row' && item.inset < 24 && item.width < 30)).toBe(true)
  }
  await checkOptionAlignment()
  await page.setViewportSize({ width: 900, height: 850 })
  await checkOptionAlignment()
  await page.setViewportSize({ width: 1800, height: 1100 })
  await options.getByRole('checkbox', { name: 'Step through (start paused)' }).check()
  await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).check()
  await options.getByLabel('Profile name', { exact: true }).fill('Step and inspect')
  await options.getByRole('button', { name: 'Save profile', exact: true }).click()
  await expect(options).toContainText('Saved execution controls only')
  await options.getByRole('checkbox', { name: 'Step through (start paused)' }).uncheck()
  await options.getByRole('combobox', { name: /^Run profile/ }).selectOption('Step and inspect')
  await expect(options.getByRole('checkbox', { name: 'Step through (start paused)' })).toBeChecked()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Get customer' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Running…', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Step', exact: true }).click()
  // Paused again, the debugger shows the request about to run, with the value
  // the step just extracted filled in; the step that ran is one click away.
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Check order' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Next request' })).toContainText('/order/debug-customer')
  await page.getByRole('complementary', { name: 'Actions' }).locator('.debugger-action-row-main').filter({ hasText: 'Get customer' }).click()
  await expect(page.getByRole('region', { name: 'Response evidence' })).toContainText('Body captured')
  // Inspecting a step stops following; follow again before continuing.
  await page.getByLabel('Follow execution', { exact: true }).check()
  await expect(page.getByRole('region', { name: 'Next request' })).toContainText('/order/debug-customer')
  await expect(page.getByRole('button', { name: 'Continue', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Response evidence' })).toContainText('expected status 201, got 200')
  await page.getByRole('tab', { name: /^Problems/ }).click()
  const problems = page.getByRole('tabpanel', { name: 'Problems' })
  await expect(problems).toContainText('1 runtime failures')
  await expect(problems.getByRole('button', { name: /Check order/ })).toBeVisible()
  await expect(problems).not.toContainText('No problems detected')
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
  await expectDebugOutcome(page, 'failed')
  await page.getByRole('button', { name: 'Edit this action', exact: true }).click()
  const expected = page.getByLabel('Assertion 1 expected number', { exact: true })
  await expect(expected).toBeFocused(); await expected.fill('200')
  await page.locator('.topbar-debug-action-run').click()
  await expect(page.getByRole('button', { name: 'Continue', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
  await expectDebugOutcome(page, 'passed')
  await expect(page.getByLabel('Script evidence states')).toContainText('unsaved changes')
  await page.getByText('Recent runs (1) · Compare evidence', { exact: true }).click()
  await expect(page.getByLabel('Historical execution')).toBeVisible()
  await expect(page.getByText('Waiting for the first browser event')).toHaveCount(0)
  await page.getByRole('button', { name: 'Pin comparison baseline' }).click()
  await page.getByText('Compare baseline with current run', { exact: true }).click()
  await expect(page.locator('.debug-run-history')).toContainText('Outcome')
  await page.getByText('Recent runs (1) · Compare evidence', { exact: true }).click()
  await page.getByRole('tab', { name: 'Variables', exact: true }).click()
  // An ended session opens on recorded values rather than an empty current state.
  await expect(page.getByRole('combobox', { name: 'Values at', exact: true })).not.toHaveValue('current')
  await expect(page.getByRole('region', { name: 'Variable watches' })).toContainText('This is historical evidence, not current engine state.')
  await page.getByRole('combobox', { name: 'Values at', exact: true }).selectOption('current')
  await expect(page.getByRole('region', { name: 'Variable watches' })).toContainText('Current state unavailable: the session has ended.')
  await page.getByRole('combobox', { name: 'Values at', exact: true }).selectOption('retained')
  await page.getByRole('button', { name: 'Pin customer', exact: true }).click()
  await page.getByLabel('Search variables', { exact: true }).fill('customer')
  await expect(page.getByRole('region', { name: 'Variable watches' })).toContainText('debug-customer')
  await page.getByRole('tab', { name: 'Timeline', exact: true }).click()
  await page.getByLabel('Find execution').fill('Check order')
  await page.locator('.debug-execution-timeline').getByRole('button', { name: /Passed · Check order/ }).click()
  await expect(page.locator('.debug-selection-status')).toContainText('Inspecting action 2')
  await expect(page.getByRole('tab', { name: 'Output', exact: true })).toHaveCount(0)
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export session', exact: true }).click()
  const download = await downloadPromise
  const path = await download.path(); expect(path).toBeTruthy()
  const exported = JSON.parse(await readFile(path!, 'utf8'))
  expect(exported.report.success).toBe(true)
  expect(exported.replay_options.capture).toBe('diagnostic')
  expect(exported.replay_options.variables).toBeUndefined()
  await page.setViewportSize({ width: 900, height: 850 })
  await expect(page.getByRole('button', { name: 'Export session', exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('debugger-operator-experience.png'), fullPage: true })
})

test('run-to starts at the beginning and consumes its temporary breakpoint without deleting a permanent breakpoint', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Debugger_run_to', version: '1.0', protocol: 'http', config: { base_url: targetURL }, actions: [{ name: 'First', type: 'request', method: 'GET', target: '/first' }, { name: 'Second', type: 'request', method: 'GET', target: '/second' }] } } })))
  await page.setViewportSize({ width: 1800, height: 1100 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Add breakpoint on Second', exact: true }).click()
  await page.getByText('Breakpoints (1)', { exact: true }).click()
  const hitCount = page.getByLabel('Break on hit', { exact: true })
  await hitCount.fill('12')
  await expect(hitCount).toHaveValue('12')
  await hitCount.press('Enter')
  await expect(hitCount).toHaveValue('12')
  await page.getByRole('button', { name: 'Disable all breakpoints', exact: true }).click()
  await page.getByText('Breakpoints (1)', { exact: true }).click()
  await page.getByRole('region', { name: 'Debugger actions' }).getByRole('button', { name: /Action 2.*Second/ }).click()
  await page.getByRole('button', { name: 'Run to selected action', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Second' })).toBeVisible()
  await expect(page.locator('.debugger-live-strip')).toContainText('1 recorded executions')
  await expect(page.getByText('Breakpoints (1)', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
  await expectDebugOutcome(page, 'passed')
})


test('failures-only debugger capture preserves fetched resource evidence', async ({ page }, testInfo) => {
  let suppressedTerminalMessages = 0
  await page.routeWebSocket('**/api/v1/ws**', socket => {
    const server = socket.connectToServer()
    server.onMessage(message => {
      const type = (JSON.parse(message.toString()) as { type?: string }).type
      if (type?.startsWith('script_debug_')) {
        if (['script_debug_completed', 'script_debug_failed', 'script_debug_stopped'].includes(type)) suppressedTerminalMessages++
        return
      }
      socket.send(message)
    })
  })
  let releaseCapturePage!: () => void
  capturePageGate = new Promise(resolve => { releaseCapturePage = resolve })
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
      name: 'Resource_failure_evidence', version: '1.0', protocol: 'http',
      config: { base_url: targetURL, browser_emulation: { cache: { download_non_html_resources: true } }, results: { message_detail: { mode: 'sampled', sample_rate: 1, include_resources: true, max_items: 7, max_item_bytes: 65536, max_total_bytes: 131072 } } },
      actions: [{ name: 'Failing page check', type: 'request', method: 'GET', target: '/capture-page', assertions: [{ type: 'status_code', expected: 201 }] }],
    } } })))
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
    const options = page.getByRole('region', { name: 'Session Overrides' })
    await options.getByRole('combobox', { name: 'Evidence capture', exact: true }).selectOption('errors')
    await options.getByRole('button', { name: 'Done', exact: true }).click()
    const started = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/debug/sessions'))
    const recovered = page.waitForResponse(async response => {
      if (response.request().method() !== 'GET' || !new URL(response.url()).pathname.startsWith('/api/v1/replay/sessions/')) return false
      const body = await response.json()
      return body.session?.status === 'failed' && Boolean(body.session.report?.completed_at)
    })
    await page.locator('.topbar-debug-action-run').click()
    const initial = (await (await started).json()).session
    expect(initial.status).toBe('running')
    expect(initial.events ?? []).toHaveLength(0)
    releaseCapturePage()
    const terminal = (await (await recovered).json()).session
    expect(terminal.id).toBe(initial.id)
    expect(terminal.report.events).toHaveLength(1)
    expect(terminal.report.events[0]).toMatchObject({ success: false, status_code: 200 })
    expect(terminal.report.events[0].snapshot.resources).toHaveLength(1)
    expect(terminal.report.events[0].snapshot.resources[0]).toMatchObject({ request_url: `${targetURL}/capture-resource`, response_status: 200, response_body_state: 'captured' })
    await expect.poll(() => suppressedTerminalMessages).toBeGreaterThan(0)
    await expectDebugOutcome(page, 'failed')
    const resources = page.getByRole('region', { name: 'Captured resources', exact: true })
    await expect(resources).toContainText('/capture-resource')
    await resources.locator('summary').click()
    await expect(resources).toContainText('diagnostic evidence')
    await testInfo.attach('terminal-get-recovery-counts', { body: JSON.stringify({ suppressedTerminalMessages, recoveredEvents: terminal.report.events.length, capturedResources: terminal.report.events[0].snapshot.resources.length }), contentType: 'application/json' })
    await page.screenshot({ path: testInfo.outputPath('failure-resource-evidence.png'), fullPage: true })
  } finally {
    releaseCapturePage()
    capturePageGate = undefined
  }
})
