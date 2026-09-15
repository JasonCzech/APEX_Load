import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('Dashboard preserves queue routing, evidence truth, filters and narrow run details', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const ids: string[] = []
  for (const name of ['Legacy checkout', 'Queued checkout', 'Reserved checkout']) {
    ids.push(idOf(await json(await request.post('/api/v1/tests', { data: {
      project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
      name, duration: '1m', groups: [{ name: 'orders', script_id: scriptId, vusers: 1 }],
    } }))))
  }
  // Reproduce legacy missing evidence and waiting capacity without starting load
  // or depending on the scheduler's timing. Run IDs and routing remain real.
  const project = (run: Record<string, unknown>) => {
    const index = ids.indexOf(String(run.id))
    if (index < 0) return run
    return { ...run, status: ['COMPLETE', 'PENDING', 'RESERVED'][index], archive: undefined,
      ended_at: index === 0 ? '2026-05-20T10:00:00Z' : undefined,
      pending_reason: index > 0 ? 'Project VUser capacity exhausted' : undefined,
      queue_position: index > 0 ? index : undefined }
  }
  await page.route('**/api/v1/tests**', async route => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() !== 'GET' || (path !== '/api/v1/tests' && !ids.some(id => path === `/api/v1/tests/${id}`))) return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    const body = path === '/api/v1/tests' ? { ...payload, tests: payload.tests.map(project) } : payload.test ? { ...payload, test: project(payload.test) } : project(payload)
    await route.fulfill({ response, json: body })
  })
  await page.goto(`/#/dashboard?${scopeQuery(workspace)}`)
  // Login mounted the workspace catalog before API seeding. A hash-only goto
  // does not reload that catalog, so load the newly created workspace afresh.
  await page.reload()
  const summary = page.getByRole('region', { name: 'Release readiness and attention' })
  await expect(summary).toContainText('0 of 1')
  await expect(summary).toContainText('Idle — no active runs')
  await expect(summary).toContainText('Queued checkout waiting')
  const attention = summary.locator('[aria-label="Needs attention"]')
  await expect(attention.getByRole('heading', { name: 'Needs attention (3)' })).toBeVisible()
  await attention.getByRole('button', { name: 'Dismiss Queued checkout waiting', exact: true }).click()
  await expect(attention).not.toContainText('Queued checkout waiting')
  await expect(attention.getByRole('heading', { name: 'Needs attention (2)' })).toBeVisible()
  await page.reload()
  await expect(attention).not.toContainText('Queued checkout waiting')
  await expect(attention.getByRole('heading', { name: 'Needs attention (2)' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Active & Queued 2' })).toBeVisible()
  await summary.getByRole('button', { name: 'View queued tests (2)' }).click()
  const queue = page.getByRole('region', { name: 'Queued runs' })
  await expect(queue).toContainText('Reserved checkout')
  await expect(queue).toContainText('Project VUser capacity exhausted')
  await queue.getByRole('button', { name: 'View queued tests', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`#/command-center/${ids[1]}`))
  await expect(page.locator('#app-page-title')).toHaveText('Command Center')
  await expect(page.locator('main')).toContainText('Queued checkout')

  await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
  await page.getByRole('tab', { name: 'Recent Runs 1' }).focus()
  await page.keyboard.press('ArrowRight')
  await expect(page.getByRole('tab', { name: 'Active & Queued 2' })).toBeFocused()
  await page.getByRole('tab', { name: 'Recent Runs 1' }).click()
  await page.getByRole('searchbox', { name: 'Find a run' }).fill('no matching run')
  await expect(page.getByRole('status').filter({ hasText: 'No runs match these filters' })).toBeVisible()
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click()
  await page.getByRole('combobox', { name: 'Evidence', exact: true }).selectOption('unavailable')
  const row = page.getByRole('row').filter({ hasText: 'Legacy checkout' })
  await expect(row).toContainText('Archive unavailable')
  await expect(row).not.toContainText('archive pending')
  await page.setViewportSize({ width: 850, height: 1000 })
  await row.locator('summary').click()
  await expect(row).toContainText('Configured VUsers')
  await expect(row.getByText('Unavailable — publication progress unknown', { exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('dashboard-narrow-details.png'), fullPage: true })
  await row.getByRole('button', { name: /^View results for Legacy checkout/ }).click()
  await expect(page.getByRole('heading', { name: 'Saved archive unavailable' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Evidence availability' })).toContainText('publication progress is unknown')
})

test('Dashboard labels failed health refreshes stale and recovers independently of run data', async ({ page }) => {
  test.setTimeout(60_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  let failHealth = false
  await page.route('**/api/v1/system/health', route => failHealth ? route.fulfill({ status: 503, json: { error: 'health probe unavailable' } }) : route.continue())
  await page.goto(`/#/dashboard?${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('button', { name: 'Healthy API Health', exact: true })).toBeVisible()
  failHealth = true
  await expect(page.getByRole('button', { name: 'Stale API Health', exact: true })).toBeVisible({ timeout: 25_000 })
  await expect(page.getByRole('button', { name: '— Generators Ready', exact: true })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Release readiness and attention' })).toContainText('Run data: last updated')
  failHealth = false
  await expect(page.getByRole('button', { name: 'Healthy API Health', exact: true })).toBeVisible({ timeout: 25_000 })
})
