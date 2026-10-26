import { createServer, type Server } from 'node:http'
import { readFile } from 'node:fs/promises'
import { test, expect, idOf, json, login, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

let target: Server
let targetURL = ''
let capturePageGate: Promise<void> | undefined
test.beforeAll(async () => {
  target = createServer(async (req, res) => {
    if (req.url === '/har-start') {
      res.writeHead(307, { Location: '/har-end', 'Content-Type': 'application/json' }); res.end('{"hop":true}'); return
    }
    if (req.url === '/har-body-page') {
      res.setHeader('Content-Type', 'text/html'); res.end(`<script>fetch('/har-start',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({value:'request-visible',password:'private-password'})}).then(r=>r.json()).then(()=>document.body.textContent='ready')</script>`); return
    }
    if (req.url === '/har-end') {
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ value: 'response-visible', access_token: 'private-token' })); return
    }
    if (req.url === '/capture-page') {
      await capturePageGate
      res.setHeader('Content-Type', 'text/html'); res.end('<img src="/capture-resource">'); return
    }
    if (req.url === '/layout-short') {
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ status: 'ready' })); return
    }
    if (req.url === '/layout-long') {
      res.setHeader('Content-Type', 'application/json')
      res.end(JSON.stringify({ lines: Array.from({ length: 40 }, (_, index) => `Retained response line ${index + 1}`), tail: 'last-retained-response-value' }))
      return
    }
    if (req.url === '/timing-total') await new Promise(resolve => setTimeout(resolve, 30))
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
    expect(alignments).toHaveLength(5)
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
  await page.getByRole('complementary', { name: 'Actions', exact: true }).getByRole('treeitem', { name: 'Get customer' }).click()
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
  const expected = page.getByLabel('Check 1 status', { exact: true })
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

test('request and response code fills the debug pane and follows dock resizing', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Debugger_payload_height', version: '1.0', protocol: 'http', config: { base_url: targetURL },
    actions: [
      { name: 'Short payload', type: 'request', method: 'POST', target: '/layout-short', body: 'debug-customer' },
      { name: 'Long payload', type: 'request', method: 'GET', target: '/layout-long' },
    ],
  } } })))
  // Leave enough height for the evidence metadata and both payloads when the dock is open.
  await page.setViewportSize({ width: 1800, height: 1800 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  await expectDebugOutcome(page, 'passed')
  const actions = page.getByRole('complementary', { name: 'Actions', exact: true })
  await actions.getByRole('treeitem', { name: /^Short payload/ }).click()
  const detail = page.getByRole('region', { name: 'Debug detail', exact: true })
  const requestCard = detail.locator('.replay-code-block').filter({ has: page.locator('.replay-code-block-header .replay-detail-label').filter({ hasText: /^Request$/ }) })
  const responseCard = detail.locator('.replay-code-block').filter({ has: page.locator('.replay-code-block-header .replay-detail-label').filter({ hasText: /^Response$/ }) })
  await expect(requestCard.locator('.snapshot-code')).toContainText('debug-customer')
  await expect(responseCard.locator('.snapshot-code')).toContainText('ready')
  if (await page.getByRole('button', { name: 'Collapse logs and problems', exact: true }).isVisible()) {
    await page.getByRole('button', { name: 'Collapse logs and problems', exact: true }).click()
  }

  const geometry = async () => Promise.all([requestCard, responseCard].map(card => card.evaluate(element => {
    const code = element.querySelector<HTMLElement>('.snapshot-code')!
    const body = code.closest('.debugger-snapshot-body') || code.parentElement!
    const buttons = Array.from(body.querySelectorAll<HTMLElement>('.snapshot-body-actions')).at(-1)!
    const grid = element.closest<HTMLElement>('.replay-event-detail-grid')!
    const scrollport = element.closest<HTMLElement>('.debugger-center-body')!
    const codeBounds = code.getBoundingClientRect()
    const gridBounds = grid.getBoundingClientRect()
    const scrollBounds = scrollport.getBoundingClientRect()
    return {
      codeHeight: codeBounds.height,
      codeToActionsGap: buttons.getBoundingClientRect().top - codeBounds.bottom,
      gridBottomGap: scrollBounds.top + scrollport.clientTop + scrollport.clientHeight - parseFloat(getComputedStyle(scrollport).paddingBottom) - gridBounds.bottom,
      bodyBottomGap: body.getBoundingClientRect().bottom - buttons.getBoundingClientRect().bottom,
    }
  })))
  const expectFilledBodies = async () => {
    await expect.poll(async () => (await geometry()).map(bounds => ({
      ...bounds,
      fillsPane: bounds.codeHeight > 40 && Math.abs(bounds.gridBottomGap) <= 30
        && bounds.codeToActionsGap >= 0 && bounds.codeToActionsGap <= 12
        && Math.abs(bounds.bodyBottomGap) <= 2,
    })), { message: 'Both payload code areas must stretch to their actions and the remaining debug pane height' }).toMatchObject([{ fillsPane: true }, { fillsPane: true }])
    return geometry()
  }
  const collapsed = await expectFilledBodies()
  await page.getByRole('button', { name: 'Expand logs and problems', exact: true }).click()
  const expanded = await expectFilledBodies()
  for (let index = 0; index < expanded.length; index++) expect(collapsed[index].codeHeight - expanded[index].codeHeight).toBeGreaterThan(80)
  const separator = page.getByRole('separator', { name: 'Resize bottom panel', exact: true })
  await separator.focus()
  await separator.press('Home')
  const resized = await expectFilledBodies()
  for (let index = 0; index < resized.length; index++) expect(resized[index].codeHeight - expanded[index].codeHeight).toBeGreaterThan(40)
  await page.getByRole('button', { name: 'Collapse logs and problems', exact: true }).click()
  await page.setViewportSize({ width: 1800, height: 1600 })
  const shorterViewport = await expectFilledBodies()
  for (let index = 0; index < shorterViewport.length; index++) expect(collapsed[index].codeHeight - shorterViewport[index].codeHeight).toBeGreaterThan(80)
  await page.screenshot({ path: testInfo.outputPath('debugger-payload-height.png') })

  // Full retained payloads remain reachable through the existing preview control.
  await actions.getByRole('treeitem', { name: /^Long payload/ }).click()
  await page.setViewportSize({ width: 900, height: 1000 })
  const paneSwitch = page.getByRole('group', { name: 'Debug panes', exact: true })
  if (await paneSwitch.isVisible()) await paneSwitch.getByRole('button', { name: 'Editor', exact: true }).click()
  await responseCard.getByRole('button', { name: 'Open full body', exact: true }).click()
  const fullBody = page.getByRole('dialog', { name: 'Full body', exact: true })
  const lastValue = fullBody.getByText('"last-retained-response-value"', { exact: true })
  await lastValue.scrollIntoViewIfNeeded()
  await expect(lastValue).toBeVisible()
  await fullBody.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(responseCard.getByRole('button', { name: 'Open full body', exact: true })).toBeVisible()
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
  await page.getByRole('complementary', { name: 'Actions', exact: true }).getByRole('treeitem', { name: /^Second/ }).click()
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

test('debug completion summary opens once, links failures, downloads retained HAR and reruns', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Debug_summary_checkout', version: '1.0', protocol: 'http', config: { base_url: targetURL },
    actions: [
      { name: 'Get customer', type: 'request', method: 'GET', target: '/customer' },
      { name: 'Check order', type: 'request', method: 'GET', target: '/order', assertions: [{ type: 'status_code', expected: 201 }] },
    ],
  } } })))
  await page.setViewportSize({ width: 1500, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).uncheck()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  const summary = page.getByRole('region', { name: 'Debug run summary', exact: true })
  await expect(summary).toBeVisible()
  await expect(summary.getByRole('status')).toContainText('Failed')
  await expect(summary).toContainText('Debug_summary_checkout')
  await expect(summary).toContainText('Check order')
  await expect(summary).toContainText('HTTP 200')
  await expect(summary.getByRole('heading', { name: 'Debug summary' })).toBeFocused()
  const downloadPromise = page.waitForEvent('download')
  await summary.getByRole('button', { name: 'Download HAR', exact: true }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toMatch(/\.har$/)
  const path = await download.path()
  expect(path).toBeTruthy()
  const raw = await readFile(path!, 'utf8')
  const har = JSON.parse(raw)
  expect(har.log.version).toBe('1.2')
  expect(har.log.entries).toHaveLength(2)
  await summary.getByRole('button', { name: 'Inspect Check order' }).click()
  await expect(page.getByRole('region', { name: 'Response evidence' })).toContainText('expected status 201, got 200')
  // Periodic session refreshes must not take the selected evidence tab away.
  await page.waitForTimeout(1800)
  await expect(summary).toBeHidden()
  await page.getByRole('tab', { name: 'Summary', exact: true }).click()
  await summary.getByRole('button', { name: 'Debug again', exact: true }).click()
  await expect(summary).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('debug-summary-desktop.png') })
  await page.setViewportSize({ width: 900, height: 850 })
  await expect(summary.getByRole('button', { name: 'Download HAR', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Restart in step mode', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Get customer' })).toBeVisible()
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(summary).toBeVisible()
  await expect(summary.getByRole('status')).toContainText('Stopped')
  await expect(summary).toContainText('No network traffic was retained')
  await expect(summary.getByRole('button', { name: 'Download HAR', exact: true })).toHaveCount(0)
})


test('browser debug completion summary exports observed page and resource requests', async ({ page }) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Browser_debug_summary', version: '1.0', protocol: 'browser',
    config: { browser: { engine: 'chromium', device_profile: 'desktop_1080p', headless: true } },
    actions: [{ name: 'Open page', type: 'browser_navigate', target: `${targetURL}/capture-page` }],
  } } })))
  await page.setViewportSize({ width: 1500, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  const summary = page.getByRole('region', { name: 'Debug run summary', exact: true })
  await expect(summary).toBeVisible({ timeout: 60_000 })
  await expect(summary.getByRole('status')).toContainText('Passed')
  const downloadPromise = page.waitForEvent('download')
  await summary.getByRole('button', { name: 'Download HAR', exact: true }).click()
  const download = await downloadPromise
  const file = await download.path(); expect(file).toBeTruthy()
  const har = JSON.parse(await readFile(file!, 'utf8'))
  const urls = har.log.entries.map((entry: { request: { url: string } }) => entry.request.url)
  expect(urls).toContain(`${targetURL}/capture-page`)
  expect(urls).toContain(`${targetURL}/capture-resource`)
  await summary.getByRole('button', { name: 'Return to debugger', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Browser Replay', exact: true })).toHaveAttribute('aria-selected', 'true')
})

for (const protocol of ['http', 'browser'] as const) test(`${protocol} HAR capture options retain redacted bodies, redirects and timings`, async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: `HAR_options_${protocol}`, version: '1.0', protocol,
    config: protocol === 'http' ? { base_url: targetURL } : { browser: { engine: 'chromium', headless: true } },
    actions: protocol === 'http'
      ? [{ name: 'Send data', type: 'request', method: 'POST', target: '/har-start', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ value: 'request-visible', password: '{{var.debug_password}}' }) }]
      : [{ name: 'Open body page', type: 'browser_navigate', target: `${targetURL}/har-body-page` }, { name: 'Wait for response', type: 'browser_wait', wait_for: { type: 'text', value: 'ready' } }],
  } } })))
  await page.setViewportSize({ width: 1500, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  const openOptions = () => page.getByRole('button', { name: /^Debug options \(/ }).click()
  await openOptions()
  if (protocol === 'http') await options.getByRole('textbox', { name: 'Session Variables JSON', exact: true }).fill(JSON.stringify({ debug_password: 'private-password' }))
  const bodies = options.getByRole('combobox', { name: 'Retain bodies', exact: true })
  await bodies.focus(); await expect(bodies).toBeFocused()
  await bodies.press('Tab')
  const redirects = options.getByRole('checkbox', { name: 'Retain intermediate redirects', exact: true })
  await expect(redirects).toBeFocused()
  await redirects.press('Space'); await expect(redirects).not.toBeChecked()
  await redirects.press('Space'); await expect(redirects).toBeChecked()
  // Native select key changes are unavailable in this headless macOS runtime.
  await bodies.selectOption('both')
  await expect(bodies).toHaveValue('both')
  await expect(options.getByRole('combobox', { name: 'Per-body limit', exact: true })).toBeEnabled()
  await options.getByRole('combobox', { name: 'Per-body limit', exact: true }).selectOption('262144')
  await options.getByRole('checkbox', { name: 'Retain intermediate redirects', exact: true }).check()
  await options.getByRole('checkbox', { name: 'Retain detailed timings', exact: true }).check()
  await page.screenshot({ path: testInfo.outputPath('har-capture-options.png') })
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  const summary = page.getByRole('region', { name: 'Debug run summary', exact: true })
  const downloadHAR = async () => {
    const ready = page.waitForEvent('download')
    await summary.getByRole('button', { name: 'Download HAR', exact: true }).click()
    const download = await ready, file = await download.path()
    const raw = await readFile(file!, 'utf8')
    expect(raw).not.toContain('private-password'); expect(raw).not.toContain('private-token')
    return JSON.parse(raw)
  }
  await page.locator('.topbar-debug-action-run').click()
  await expect(summary).toBeVisible({ timeout: 60_000 })
  await expect(summary.getByRole('status')).toContainText('Passed')
  await expect(summary).toContainText('256 KiB per body')
  const rich = await downloadHAR()
  expect(rich.log.entries.some((e: { request: { url: string } }) => e.request.url.endsWith('/har-start'))).toBe(true)
  const last = rich.log.entries.find((e: { request: { url: string } }) => e.request.url.endsWith('/har-end'))
  expect(last.request.postData.text).toContain('request-visible')
  expect(last.response.content.text).toContain('response-visible')
  expect(last._apexload.time_source).toBe('timings')
  await openOptions()
  await options.getByRole('combobox', { name: 'Retain bodies', exact: true }).selectOption('none')
  await options.getByRole('checkbox', { name: 'Retain intermediate redirects', exact: true }).uncheck()
  await options.getByRole('checkbox', { name: 'Retain detailed timings', exact: true }).uncheck()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await expect(summary).toContainText('Bodies: request and response')
  await summary.getByRole('button', { name: 'Debug again', exact: true }).click()
  await expect(summary).toBeVisible({ timeout: 60_000 })
  await expect(summary).toContainText('Bodies: none')
  const limited = await downloadHAR()
  expect(limited.log.entries.some((e: { request: { url: string } }) => e.request.url.endsWith('/har-start'))).toBe(false)
  for (const entry of limited.log.entries) { expect(entry.request.postData).toBeUndefined(); expect(entry.response.content.text).toBeUndefined(); expect(entry.timings.dns).toBe(-1); expect(entry.timings.connect).toBe(-1) }
})


test('summary expands transaction timings, nested pages and debugger evidence', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const marker = (name: string, end = false, sub = false) => ({ name: `${end ? 'End' : 'Start'} ${name}`, type: 'source_transaction', target: '', params: { function: `lr_${end ? 'end' : 'start'}_${sub ? 'sub_' : ''}transaction`, args: end ? [name, 'LR_AUTO'] : sub ? [name, 'Checkout'] : [name] } })
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Transaction_breakdown', version: '1.0', protocol: 'http', config: { base_url: targetURL },
    init: [marker('Setup'), { name: 'Login', type: 'request', method: 'GET', target: '/login' }, marker('Setup', true)],
    actions: [marker('Checkout'), marker('Cart', false, true), {
      name: 'Cart page', type: 'http_sequence', target: '', params: { requests: [
        { name: 'Get customer', method: 'GET', target: '/customer' },
        { name: 'Check order', method: 'GET', target: '/order', assertions: [{ type: 'status_code', expected: 201 }] },
      ] },
    }, marker('Totals', false, true), { name: 'Calculate totals', type: 'request', method: 'GET', target: '/timing-total' }, marker('Totals', true, true), { ...marker('Cart', true, true), think_time: { type: 'fixed', mean_ms: 120 }, think_time_after: true }, marker('Checkout', true), marker('Receipt'), { name: 'Finish', type: 'request', method: 'GET', target: '/finish' }, marker('Receipt', true)],
  } } })))
  await page.setViewportSize({ width: 1800, height: 1200 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: /^Debug options \(/ }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).uncheck()
  await options.getByRole('checkbox', { name: 'Ignore think time', exact: true }).uncheck()
  await options.getByRole('combobox', { name: 'After a failure', exact: true }).selectOption('continue')
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await page.locator('.topbar-debug-action-run').click()
  const summary = page.getByRole('region', { name: 'Debug run summary', exact: true })
  await expect(summary).toBeVisible()
  await expect(summary.getByRole('status')).toContainText('Failed')
  const table = summary.getByRole('table', { name: 'Transactions from the completed debug run' })
  await expect(table.getByRole('columnheader', { name: 'Elapsed time (ms)', exact: true })).toBeVisible()
  const row = (name: string) => table.getByRole('row').filter({ has: page.getByRole('rowheader').filter({ hasText: name }) })
  await expect(row('Setup')).toContainText('Passed')
  await expect(row('Checkout')).toContainText('Failed')
  await expect(row('Receipt')).toContainText('Passed')
  await expect(table.getByText('Cart page', { exact: true })).toHaveCount(0)
  const expandCheckout = table.getByRole('button', { name: 'Expand Checkout', exact: true })
  await expandCheckout.focus(); await expandCheckout.press('Enter')
  await expect(table.getByRole('button', { name: 'Collapse Checkout', exact: true })).toHaveAttribute('aria-expanded', 'true')
  // Both subtransactions declare Checkout as their parent, even while overlapping.
  await expect(row('Totals')).toContainText('Passed')
  await table.getByRole('button', { name: 'Expand Cart', exact: true }).press('Space')
  await table.getByRole('button', { name: 'Expand Cart page', exact: true }).click()
  await expect(row('Cart page')).toContainText('Failed')
  await expect(row('Get customer')).toContainText('Passed')
  await expect(row('Check order')).toContainText('Failed')
  await expect(row('Check order')).toContainText('Action time:')
  await expect(row('Totals')).toContainText('Passed')
  await expect(row('Receipt')).toContainText('Passed')
  await expect(summary.locator('.table-pagination-toolbar')).toHaveCount(0)
  const breakdown = table.getByRole('note', { name: 'Timing breakdown for Checkout', exact: true })
  await expect(breakdown).toContainText('outside child spans')
  const outside = (await breakdown.innerText()).match(/([\d,.]+) ms outside child spans/)
  expect(outside).not.toBeNull()
  expect(Number(outside![1].replaceAll(',', ''))).toBeGreaterThanOrEqual(100)
  const overlap = (await breakdown.innerText()).match(/([\d,.]+) ms counted more than once/)
  expect(overlap).not.toBeNull()
  expect(Number(overlap![1].replaceAll(',', ''))).toBeGreaterThanOrEqual(20)
  await expect(row('Checkout').locator('time')).toHaveCount(2)
  await expect(row('Check order').locator('time')).toHaveCount(2)
  await expect(row('Check order').getByRole('cell').nth(4)).toContainText('ms')
  await page.screenshot({ path: testInfo.outputPath('transaction-summary-desktop.png'), fullPage: true })
  await table.getByRole('button', { name: 'Inspect Check order', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Response evidence' })).toContainText('expected status 201, got 200')
  await page.getByRole('tab', { name: 'Summary', exact: true }).click()
  // Returning to Summary restores the transaction table.
  if (await table.getByRole('button', { name: 'Expand Checkout', exact: true }).isVisible()) {
    await table.getByRole('button', { name: 'Expand Checkout', exact: true }).click()
    await table.getByRole('button', { name: 'Expand Cart', exact: true }).click()
    await table.getByRole('button', { name: 'Expand Cart page', exact: true }).click()
  }
  await expect(table.getByText('Check order', { exact: true })).toBeVisible()
  await page.setViewportSize({ width: 900, height: 1000 })
  const scrollRegion = summary.getByRole('region', { name: 'Transaction timings', exact: true })
  await scrollRegion.focus(); await expect(scrollRegion).toBeFocused()
  expect(await scrollRegion.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
  await scrollRegion.press('ArrowRight')
  await expect.poll(() => scrollRegion.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)
  await page.screenshot({ path: testInfo.outputPath('transaction-summary-narrow.png'), fullPage: true })
  await page.getByRole('button', { name: 'Restart in step mode', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Paused before: Start Setup' })).toBeVisible()
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(summary).toBeVisible()
  await expect(summary.getByRole('status')).toContainText('Stopped')
  await expect(row('Checkout')).toContainText('Not run')
  await expect(row('Checkout').locator('time')).toHaveCount(0)
  await expect(table.getByText('Check order', { exact: true })).toHaveCount(0)
})
