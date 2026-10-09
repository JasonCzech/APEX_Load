import { test, expect, seedScript, seedScenario, mutation, json, idOf, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

async function statusIsStable(sample: () => Promise<string>, expected: string) {
  let count = 0
  await expect.poll(async () => { expect(await sample()).toBe(expected); return ++count }, { intervals: [250, 500, 1000] }).toBeGreaterThanOrEqual(3)
}

test('[U10 U11] unavailable preflight blocks launch; recovery creates only one run', async ({ page, request, target, trackRun }) => {
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Preflight_HTTP', target.url)
  const scenarioId = await seedScenario(request, workspace, scriptId)
  const pattern = '**/api/v1/tests/preflight'
  await page.route(pattern, route => route.fulfill({ status: 503, json: { error: 'Capacity service unavailable; retry preflight' } }))
  await page.goto(`/#/new-test?scenarioId=${scenarioId}&${scopeQuery(workspace)}`)
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  const start = page.getByRole('button', { name: 'Start Now', exact: true })
  await expect(page.getByText('Capacity service unavailable; retry preflight', { exact: false }).first()).toBeVisible()
  await expect(start).toBeDisabled()
  expect(target.count()).toBe(0)
  await page.unroute(pattern)
  await page.reload()
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  const runName = `once-${Date.now()}`
  await page.getByLabel('Run Name', { exact: true }).fill(runName)
  await expect(start).toBeEnabled({ timeout: 30_000 })
  let creates = 0
  page.on('request', r => { if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/tests') creates++ })
  const created = mutation(page, 'POST', '/api/v1/tests')
  // Two DOM clicks in one turn reproduce a rapid repeated activation without
  // waiting for a second locator after the app navigates away.
  await start.evaluate(button => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click() })
  const id = idOf(await json(await created)); trackRun(id)
  await expect(page).toHaveURL(new RegExp(`(?:test|command-center)/${id}`))
  await expect.poll(() => runStatus(request, id), { timeout: 60_000 }).toBe('RUNNING')
  expect(creates).toBe(1)
  expect((await json(await request.get(`/api/v1/scenarios/${scenarioId}`))).duration).toBe('2m')
})

test('[U15] held run stays reserved across reload and starts only on explicit action', async ({ page, request, target, trackRun }) => {
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Held_HTTP', target.url)
  const scenarioId = await seedScenario(request, workspace, scriptId)
  await page.goto(`/#/new-test?scenarioId=${scenarioId}&${scopeQuery(workspace)}`)
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  const hold = page.getByRole('button', { name: 'Hold for Manual Start', exact: true })
  await expect(hold).toBeEnabled({ timeout: 30_000 })
  const created = mutation(page, 'POST', '/api/v1/tests')
  await hold.click()
  const id = idOf(await json(await created)); trackRun(id)
  await page.reload()
  await statusIsStable(() => runStatus(request, id), 'RESERVED')
  expect(target.count()).toBe(0)
  await page.getByRole('button', { name: /^Start(?: Test| Run| Now)?$/ }).first().click()
  const confirm = page.getByRole('dialog')
  if (await confirm.isVisible()) await confirm.getByRole('button', { name: /^Start/ }).click()
  await expect.poll(() => target.count(), { timeout: 60_000 }).toBeGreaterThan(0)
  await expect.poll(() => runStatus(request, id)).toBe('RUNNING')
})

test('[U12] disconnect is visible and reconnect resumes the same run', async ({ page, request, target, trackRun }) => {
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Reconnect_HTTP', target.url)
  const run = await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Reconnect run', duration: '2m', groups: [{ name: 'orders', script_id: scriptId, vusers: 1 }] } }))
  const id = idOf(run); trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  let connection: import('@playwright/test').WebSocketRoute | undefined
  let interrupted = false
  await page.routeWebSocket('**/api/v1/ws**', async ws => {
    if (interrupted) { await ws.close(); return }
    connection = ws
    ws.connectToServer()
  })
  await page.goto(`/#/test/${id}?tab=overview&${scopeQuery(workspace)}`)
  await expect(page.locator('.sidebar-status')).toHaveText('Live connection: Live')
  interrupted = true
  expect(connection).toBeDefined()
  await connection!.close({ code: 1012, reason: 'Workflow reconnect test' })
  await expect(page.locator('.sidebar-status')).not.toHaveText('Live connection: Live', { timeout: 20_000 })
  interrupted = false
  await expect(page.locator('.sidebar-status')).toHaveText('Live connection: Live', { timeout: 30_000 })
  await expect(page).toHaveURL(new RegExp(`#/test/${id}`))
  const count = target.count()
  await expect.poll(() => target.count()).toBeGreaterThan(count)
  await expect(page.getByText('Reconnect run', { exact: false }).first()).toBeVisible()
})

test('[U17 U16] archive read failure is visible and retry restores the same run evidence', async ({ page, request, target, trackRun }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Archive_HTTP', target.url)
  const run = await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Archive recovery', duration: '2m', groups: [{ name: 'orders', script_id: scriptId, vusers: 1 }] } }))
  const id = idOf(run); trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => target.count(), { timeout: 30_000 }).toBeGreaterThan(0)
  await json(await request.post(`/api/v1/tests/${id}/stop`))
  await expect.poll(() => runStatus(request, id), { timeout: 60_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok()).toBe(true)
  const pattern = `**/api/v1/tests/${id}/archive/report**`
  await page.route(pattern, route => route.fulfill({ status: 503, json: { error: 'Archive temporarily unavailable' } }))
  await page.goto(`/#/test/${id}?tab=archive&${scopeQuery(workspace)}`)
  await expect(page.getByText('Archive temporarily unavailable', { exact: false }).first()).toBeVisible()
  await page.unroute(pattern)
  await page.reload()
  await expect(page.getByText('Archive temporarily unavailable')).toBeHidden()
  await expect(page.getByRole('tab', { name: 'Evidence', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByText('Raw evidence downloads', { exact: true }).click()
  await expect(page.getByRole('button', { name: 'Download Transactions JSONL' }).first()).toBeVisible()
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download Transactions JSONL' }).first().click()
  expect(await (await downloadPromise).failure()).toBeNull()
})


test('[U15] scheduled run preserves its future start across reload without traffic', async ({ page, request, target, trackRun }) => {
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Scheduled_HTTP', target.url)
  const scenarioId = await seedScenario(request, workspace, scriptId)
  await page.goto(`/#/new-test?scenarioId=${scenarioId}&${scopeQuery(workspace)}`)
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  const future = new Date(Date.now() + 24 * 60 * 60 * 1000)
  future.setSeconds(0, 0)
  const local = await page.evaluate(timestamp => {
    const date = new Date(timestamp)
    return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
  }, future.getTime())
  await page.getByLabel(/^Scheduled start \(/).fill(local)
  const schedule = page.getByRole('button', { name: 'Schedule run', exact: true })
  await expect(schedule).toBeEnabled({ timeout: 30_000 })
  const created = mutation(page, 'POST', '/api/v1/tests')
  await schedule.click()
  const run = await json(await created)
  const id = idOf(run); trackRun(id)
  await expect(page).toHaveURL(new RegExp(`(?:test|command-center)/${id}`))
  await page.reload()
  await statusIsStable(() => runStatus(request, id), 'SCHEDULED')
  const stored = await json(await request.get(`/api/v1/tests/${id}`))
  expect(new Date(String(stored.scheduled_start_at)).getTime()).toBe(future.getTime())
  expect(target.count()).toBe(0)
  await expect(page.getByText('SCHEDULED', { exact: true }).first()).toBeVisible()
})
