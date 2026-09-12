import { test, expect, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test.beforeEach(async ({ page }) => {
  test.setTimeout(60000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
})

test('Monitoring preserves repeated keyboard navigation and calendar context', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  // Refresh the workspace inventory after API fixture creation in an already signed-in page.
  await page.reload()
  const tabs = page.getByRole('tablist', { name: 'Monitor categories' })
  await tabs.getByRole('tab', { name: 'Overview', exact: true }).focus()
  for (const name of ['Fleet', 'Telemetry', 'Alerts', 'Schedules']) {
    await page.keyboard.press('ArrowRight')
    await expect(tabs.getByRole('tab', { name, exact: true })).toBeFocused()
  }
  await page.getByRole('button', { name: 'Day', exact: true }).click()
  await page.getByRole('button', { name: 'Cron', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Show queued runs', exact: true }).uncheck()
  await tabs.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Datasource evidence' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Cron', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('checkbox', { name: 'Show queued runs', exact: true })).not.toBeChecked()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Schedule run', exact: true })).toBeVisible()
})

test('Collectors receive a heartbeat while Monitoring remains open', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const created = await json(await request.post('/api/v1/monitoring/collectors', { data: { project_id: workspace.projectId, name: 'Monitoring refresh acceptance', allowed_metric_packs: ['linux-hosts'] } }))
  const collector = created.collector as { id: string }
  await page.route('**/api/v1/integrations/apm/profiles', async route => {
    const response = await route.fetch()
    const profiles = await response.json()
    profiles.profiles.find((profile: { id: string }) => profile.id === 'opentelemetry').status = 'unavailable'
    await route.fulfill({ response, json: profiles })
  })
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.locator('[data-card-id="apm-integrations"] .monitor-header-badge.danger')).toContainText('1 unavailable')
  const row = page.locator('.pp-row').filter({ hasText: 'Monitoring refresh acceptance' })
  await expect(row).toBeVisible()
  await json(await request.post(`/api/v1/monitoring/collectors/${collector.id}/heartbeat`, { headers: { Authorization: `Bearer ${created.token}` }, data: { packs: [{ pack: 'linux-hosts', samples: { cpu_pct: 17 } }] } }))
  await expect(row).toContainText('healthy', { timeout: 15000 })
  await row.getByRole('button', { name: 'Metrics', exact: true }).click()
  const metrics = page.getByRole('dialog', { name: /Latest captured metrics/ })
  await expect(metrics).toContainText('cpu_pct')
  await expect(metrics.getByRole('button', { name: 'Refresh metrics', exact: true })).toBeVisible()
})
