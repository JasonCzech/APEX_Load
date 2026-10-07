import { readFile } from "node:fs/promises"
import { startLiveRun, abortLiveRun, type LiveRun } from "./support/liveRun"
import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('Command Center supports empty actions, run triage, persistent switching and catalog recovery', async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  await page.goto(`/#/command-center?${scopeQuery(workspace)}`)
  await page.reload()
  const overview = page.getByRole('region', { name: 'Active run overview', exact: true })
  await expect(overview.getByRole('heading', { name: 'No active runs', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Open analysis', exact: true })).toHaveCount(0)
  await expect(overview.getByRole('button', { name: 'New Test', exact: true })).toBeEnabled()
  await overview.getByRole('button', { name: 'New Test', exact: true }).click()
  await expect(page).toHaveURL(/#\/new-test/)
  await page.getByRole('button', { name: 'Command Center', exact: true }).click()

  const ids: string[] = []
  for (let i = 0; i < 6; i++) {
    ids.push(idOf(await json(await request.post('/api/v1/tests', { data: {
      project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
      name: `Audit checkout ${i}`, duration: '30m', groups: [{ name: 'orders', script_id: scriptId, vusers: 1 }],
    } }))))
  }
  let failCatalog = false
  const project = (run: Record<string, unknown>) => {
    const index = ids.indexOf(String(run.id))
    if (index < 0) return run
    return { ...run, status: index === 4 ? 'PAUSED' : index === 5 ? 'PENDING' : 'RUNNING',
      started_at: new Date(Date.now() - 300_000).toISOString(),
      pending_reason: index === 5 ? 'Project VUser capacity exhausted' : undefined,
      live_metrics: { timestamp: new Date().toISOString(), active_vusers: 1, tps: 10,
        total_transactions: 90, total_errors: index === 0 ? 10 : 0, error_pct: index === 0 ? 10 : 0,
        avg_response_ms: 120, p50_ms: 100, p90_ms: 150, p95_ms: 160, p99_ms: 180 } }
  }
  await page.route('**/api/v1/tests**', async route => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() !== 'GET' || (path !== '/api/v1/tests' && !ids.some(id => path === `/api/v1/tests/${id}`))) return route.continue()
    if (path === '/api/v1/tests' && failCatalog) return route.fulfill({ status: 503, json: { error: 'Catalog unavailable' } })
    const response = await route.fetch()
    const payload = await response.json()
    await route.fulfill({ response, json: path === '/api/v1/tests' ? { ...payload, tests: payload.tests.map(project) } : project(payload) })
  })
  await page.reload()
  await expect(overview).toContainText('6 active runs')
  await expect(overview.getByRole('row').filter({ hasText: 'Audit checkout 0' })).toContainText('Errors reported')
  await expect(overview).toContainText('Project VUser capacity exhausted')
  await overview.getByRole('searchbox', { name: 'Find a run' }).fill('checkout 0')
  await expect(overview.getByRole('button', { name: /^Open Audit checkout/ })).toHaveCount(1)
  await overview.getByRole('button', { name: /^Open Audit checkout 0/ }).press('Enter')
  await expect(page).toHaveURL(new RegExp(`#/command-center/${ids[0]}`))
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true })).toHaveValue(ids[0])
  const target = page.getByRole('spinbutton', { name: 'orders target VUsers', exact: true })
  await target.fill('5')
  await page.getByRole('combobox', { name: 'Current run', exact: true }).selectOption(ids[1])
  await expect(page).toHaveURL(new RegExp(`#/command-center/${ids[1]}`))
  await expect(target).toHaveValue('1')
  await page.getByRole('combobox', { name: 'Current run', exact: true }).selectOption(ids[0])
  await expect(target).toHaveValue('5')
  await page.reload()
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true })).toHaveValue(ids[0])
  await page.getByRole('button', { name: 'Pause', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Pause this test run?' })
  await expect(dialog).toContainText('Audit checkout 0')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await page.getByRole('link', { name: '+4 runs', exact: true }).click()
  await expect(overview).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Scenarios', exact: true })).toHaveCount(0)
  failCatalog = true
  await overview.getByRole('button', { name: 'Refresh runs' }).click()
  await expect(overview.getByRole('alert')).toContainText('Showing the last successful list')
  await expect(overview).toContainText('Audit checkout 0')
  await page.reload()
  await expect(overview.getByRole('alert')).toContainText('Active runs could not be determined')
  await expect(overview.getByRole('heading', { name: 'No active runs', exact: true })).toHaveCount(0)
  failCatalog = false
  await overview.getByRole('button', { name: 'Retry active runs' }).click()
  await expect(overview.getByRole('alert')).toHaveCount(0)
  await expect(overview).toContainText('6 active runs')
  await page.setViewportSize({ width: 900, height: 1000 })
  await overview.getByRole('searchbox', { name: 'Find a run' }).fill('no match')
  await expect(overview).toContainText('No runs match these filters')
  await overview.getByRole('button', { name: 'Clear filters', exact: true }).click()
  await page.screenshot({ path: testInfo.outputPath('command-center-overview.png'), fullPage: true })
})

test('active load observations expire without updates and fresh telemetry clears the warning', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Active observation evidence', duration: '10m', groups: [{ name: 'orders', script_id: scriptId, vusers: 120 }],
  } })))
  let capturedAt = new Date().toISOString()
  await page.clock.install({ time: new Date(capturedAt) })
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    await route.fulfill({ response, json: { ...payload, status: 'RUNNING', started_at: capturedAt,
      live_metrics: { timestamp: capturedAt, observation_valid_until: new Date(Date.parse(capturedAt) + 5000).toISOString(), active_vusers: 120,
        tps: 100, total_transactions: 5000, total_errors: 0, error_pct: 0, avg_response_ms: 10,
        p50_ms: 10, p90_ms: 15, p95_ms: 20, p99_ms: 25 } } })
  })
  await page.goto(`/#/command-center/${id}?${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  const notice = page.getByRole('status', { name: 'Load observation status' })
  await expect(notice).toHaveCount(0)
  await page.clock.fastForward(6000)
  await expect(notice).toContainText('current load and recovery are unconfirmed')
  await expect(page.getByRole('region', { name: 'Command center run controls' })).toContainText('Observed / Target Load')
  await expect(page.getByRole('region', { name: 'Command center run controls' })).not.toContainText('connected to live run data')
  await page.setViewportSize({ width: 1280, height: 950 })
  await page.screenshot({ path: testInfo.outputPath('active-observation-stale.png'), fullPage: true })
  // A real new sample, rather than a fresh receipt time for the old sample.
  capturedAt = await page.evaluate(() => new Date().toISOString())
  await page.reload()
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  await expect(notice).toHaveCount(0)
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`)
  await expect(page.getByRole('region', { name: 'Run state', exact: true })).toBeVisible()
  await page.clock.fastForward(6000)
  const state = page.getByRole('region', { name: 'Run state', exact: true })
  await expect(state).toContainText('Last observed load')
  await expect(state).toContainText('120')
  await expect(state).toContainText('Stale sample')
  await expect(state.getByRole('status')).toContainText('recovery are unconfirmed')
})


// Own the API context so cleanup still runs if the browser test times out.
const exportTest = test.extend<{ exportRun: LiveRun }>({
 exportRun: [async ({ page, playwright, baseURL }, provide) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const context = await playwright.request.newContext({ baseURL, storageState: await page.context().storageState() })
  let run: LiveRun | undefined
  try {
   run = await startLiveRun(context, baseURL, { label: 'command-load-export' })
   await provide(run)
  } finally {
   try { if (run) await abortLiveRun(context, run) } finally { await context.dispose() }
  }
 }, { timeout: 150_000 }],
})

exportTest('analysis load exports retain run identity and distinguish resampled observations', async ({ page, exportRun: run }) => {
 exportTest.setTimeout(150_000)
 await page.goto(`/#/command-center/${run.testId}`)
 await page.getByRole('button', { name: 'Open analysis', exact: true }).click()
 const card = page.locator('[data-chart-definition="command-observed-load"]')
 await expect(card).toBeVisible()
 const csvDownload = page.waitForEvent('download')
 await card.getByRole('button', { name: 'Export Planned and actual load data as CSV', exact: true }).click()
 const csv = await readFile((await (await csvDownload).path())!, 'utf8')
 expect(csv).toContain(run.testId)
 expect(csv).toContain('Resampled observed VUsers (VUsers)')
 expect(csv).toContain('future actual values remain missing')
 const svgDownload = page.waitForEvent('download')
 await card.getByRole('button', { name: 'Export Planned and actual load image as SVG', exact: true }).click()
 const svg = await readFile((await (await svgDownload).path())!, 'utf8')
 expect(svg).toContain(run.testId)
 expect(svg).toContain('<svg')
 expect(svg).not.toContain('var(')
 await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
 await page.getByRole('tab', { name: /^Active (?:Tests|& Queued)/ }).click()
 const openRun = page.getByRole('button', { name: /^Open live test command-load-export/ })
 const activeCard = page.locator('.dash-active-card').filter({ has: openRun })
 await expect(activeCard.locator('[role="button"] button')).toHaveCount(0)
 await expect(activeCard.getByRole('button', { name: /data as CSV$/ })).toBeVisible()
 const dashboardDownload = page.waitForEvent('download')
 await activeCard.getByRole('button', { name: /data as CSV$/ }).click()
 const dashboardCSV = await readFile((await (await dashboardDownload).path())!, 'utf8')
 expect(dashboardCSV).toContain(run.testId)
 await expect(page).toHaveURL(/#\/dashboard/)
 await openRun.focus()
 await expect(openRun).toBeFocused()
 expect(await activeCard.evaluate(element => element.matches(':focus-within'))).toBe(true)
 await page.keyboard.press('Enter')
 await expect(page).toHaveURL(new RegExp(`#/test/${run.testId}`))
})

exportTest('pause waits for controller confirmation and preserves running state when proof is rejected', async ({ page, exportRun: run }, testInfo) => {
 exportTest.setTimeout(150_000)
 const request = page.context().request
 const path = `**/api/v1/tests/${run.testId}/pause`
 await page.goto(`/#/command-center/${run.testId}`)
 await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
 // External boundary failure: the real dashboard must preserve the live state.
 await page.route(path, route => route.fulfill({ status: 409, json: { error: 'Processor pause quiescence is unconfirmed' } }))
 await page.getByRole('button', { name: 'Pause', exact: true }).click()
 await page.getByRole('dialog', { name: 'Pause this test run?' }).getByRole('button', { name: 'Pause Test' }).click()
 await expect(page.getByText('Processor pause quiescence is unconfirmed', { exact: true })).toBeVisible()
 await expect(page.getByRole('button', { name: 'Resume', exact: true })).toHaveCount(0)
 await expect(page.getByRole('combobox', { name: 'Current run', exact: true }).locator('option:checked')).toContainText('RUNNING')
 expect((await json(await request.get(`/api/v1/tests/${run.testId}`))).status).toBe('RUNNING')
 await page.unroute(path)
 let allowPause!: () => void
 let markPending!: () => void
 const pending = new Promise<void>(resolve => { allowPause = resolve })
 const seen = new Promise<void>(resolve => { markPending = resolve })
 await page.route(path, async route => { markPending(); await pending; await route.continue() })
 try {
  await page.getByRole('button', { name: 'Pause', exact: true }).click()
  await page.getByRole('dialog', { name: 'Pause this test run?' }).getByRole('button', { name: 'Pause Test' }).click()
  await seen
  // The UI cannot publish PAUSED ahead of the controller's committed response.
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Resume', exact: true })).toHaveCount(0)
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true }).locator('option:checked')).toContainText('RUNNING')
  expect((await json(await request.get(`/api/v1/tests/${run.testId}`))).status).toBe('RUNNING')
  allowPause()
  await expect.poll(async () => (await json(await request.get(`/api/v1/tests/${run.testId}`))).status).toBe('PAUSED')
  await expect(page.getByRole('button', { name: 'Resume', exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true }).locator('option:checked')).toContainText('PAUSED')
  await page.screenshot({ path: testInfo.outputPath('acknowledged-pause.png'), fullPage: true })
  await page.getByRole('button', { name: 'Resume', exact: true }).click()
  await page.getByRole('dialog', { name: 'Resume this test run?' }).getByRole('button', { name: 'Resume Test' }).click()
  await expect.poll(async () => (await json(await request.get(`/api/v1/tests/${run.testId}`))).status).toBe('RUNNING')
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true }).locator('option:checked')).toContainText('RUNNING')
 } finally { allowPause() }
})
