import { test, expect, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('local archive failure preserves completed Results and survives checking again', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = 'local-archive-failure'
  const description = 'The controller could not finalize the local archive. Captured files may still be available for recovery.'
  const starts: string[] = []
  page.on('request', request => {
    if (request.method() === 'POST' && request.url().includes(`/tests/${id}/start`)) starts.push(request.url())
  })
  const record = {
    id, name: 'Completed with local archive failure', status: 'COMPLETE', project_id: workspace.projectId,
    application_id: workspace.applicationId, release_id: workspace.releaseId,
    created_at: '2026-09-28T10:00:00Z', ended_at: '2026-09-28T10:02:00Z',
    duration: '2m', total_vusers: 1, groups: [],
    archive_failure: { reason: 'local_archive_failed', recorded_at: '2026-09-28T10:02:01Z' },
  }
  await page.route(`**/api/v1/tests/${id}`, route => route.fulfill({ json: record }))
  await page.route(`**/api/v1/tests/${id}/manifest`, route => route.fulfill({ json: {
    schema_version: 'v1', test_id: id, test_name: record.name, status: record.status, source: 'live', generated_at: record.ended_at,
    identity: {}, initial_configuration: {}, current_configuration: {}, load_generators: { execution_settings: {} }, runtime_changes: [],
  } }))
  await page.goto(`/#/test/${id}?tab=overview&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('heading', { name: record.name, exact: true })).toBeVisible()
  const failureHeading = page.getByRole('heading', { name: 'Local archive finalization failed', exact: true })
  await expect(failureHeading).toBeVisible()
  await expect(page.getByText(description, { exact: true })).toBeVisible()
  await expect(page.getByText('The controller could not collect the generator archive.', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect(failureHeading).toBeVisible()
  await page.reload()
  await expect(failureHeading).toBeVisible()
  await expect(page.getByText(description, { exact: true })).toBeVisible()
  expect(starts).toEqual([])
  await page.screenshot({ path: test.info().outputPath('local-archive-failure.png'), fullPage: true, animations: 'disabled' })
})
