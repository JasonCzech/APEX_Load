import { test, expect } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Exercise unavailable historical evidence without forcing a real controller crash.
for (const status of ['FAILED', 'COMPLETE', 'ABORTED']) {
  test(`ended ${status} run preserves Results navigation without an archive`, async ({ page, request }) => {
    const workspace = await createWorkspace(request)
    const id = `execution-${status.toLowerCase()}`
    await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
    const record = {
      id, name: `Historical ${status}`, status, project_id: workspace.projectId,
      application_id: workspace.applicationId, release_id: workspace.releaseId,
      created_at: '2026-09-10T10:00:00Z', ended_at: '2026-09-10T10:02:00Z',
      duration: '10m', total_vusers: 3, groups: [],
      error: status === 'FAILED' ? 'Controller restarted before the run completed' : undefined,
    }
    await page.route(`**/api/v1/tests/${id}`, route => route.fulfill({ json: record }))
    await page.route(`**/api/v1/tests/${id}/manifest`, route => route.fulfill({ json: {
      schema_version: 'v1', test_id: id, test_name: record.name, status, source: 'live', generated_at: '2026-09-10T12:00:00Z',
      identity: {}, initial_configuration: {}, current_configuration: {}, load_generators: { execution_settings: {} }, runtime_changes: [],
    } }))
    await page.goto(`/#/test/${id}?tab=overview&${scopeQuery(workspace)}`)
    await expect(page).toHaveURL(new RegExp(`/report/${id}\\?section=execution`))
    await expect(page.getByRole('heading', { name: record.name, exact: true })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Execution', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(page.locator('nav.sidebar-nav').getByRole('button', { name: 'Results', exact: true })).toHaveClass(/active/)
    if (status === 'FAILED') await expect(page.getByText(record.error!, { exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Saved archive unavailable' })).toBeVisible()
    await expect(page.getByText('No group configuration was retained.')).toBeVisible()
    await page.getByRole('tab', { name: 'Evidence', exact: true }).click()
    await expect(page.getByText('No lifecycle event history was retained.', { exact: false })).toBeVisible()
    await page.getByRole('tab', { name: 'Performance', exact: true }).click()
    await expect(page.getByText('No controller metric sample was retained.', { exact: false })).toBeVisible()
    await page.getByRole('tab', { name: 'Summary', exact: true }).focus()
    await page.keyboard.press('End')
    await expect(page.getByRole('tab', { name: 'Execution', exact: true })).toBeFocused()
    await page.getByRole('button', { name: 'Check again', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Execution', exact: true })).toHaveAttribute('aria-selected', 'true')
    await page.setViewportSize({ width: 760, height: 900 })
    await expect(page.getByRole('tab', { name: 'Execution', exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: test.info().outputPath('execution-narrow.png'), fullPage: true })
  })
}
