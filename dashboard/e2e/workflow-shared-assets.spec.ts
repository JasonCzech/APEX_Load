import { test, expect, json, login, seedScript, idOf } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Shared assets says what uses each asset; a user is a link to where it changes.
test('a pool lists the scenario that binds it, refuses a delete it would break, and opens that scenario at its Data step', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Shared assets HTTP')
  const pool = idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Bound customers', schema: { id: 'string' } } })))
  await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Idle customers', schema: { id: 'string' } } }))
  await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Customer journey', duration: '1m', groups: [{ name: 'Load', script_id: script, vusers: 1 }], data_pools: [{ pool_id: pool, alias: 'customers', access_mode: 'read_only', selection_strategy: 'sequential' }] } }))

  await page.goto(`/#/shared-assets?section=test-data&resourceId=${pool}&${scopeQuery(workspace)}`)
  const bound = page.locator('.settings-release-row').filter({ hasText: 'Bound customers' })
  await expect(bound).toContainText('Used by 1 scenario')
  await expect(page.locator('.settings-release-row').filter({ hasText: 'Idle customers' })).toContainText('Not bound by a scenario')
  const usedBy = page.locator('.settings-manager-summary .shared-asset-used-by')
  await expect(usedBy.getByRole('link', { name: 'Customer journey', exact: true })).toBeVisible()
  await expect(usedBy).toContainText('(as customers)')

  // Deleting it would fail, so the confirmation lists what to change first.
  await bound.getByRole('button', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete data pool' })
  await expect(dialog).toContainText('cannot be deleted while scenarios bind it')
  await expect(dialog.getByRole('link', { name: 'Customer journey', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Delete data pool', exact: true })).toBeDisabled()
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()

  await usedBy.getByRole('link', { name: 'Customer journey', exact: true }).click()
  await expect(page.locator('.scenario-builder-mode')).toHaveText('Editing')
  await expect(page.locator('.scenario-wizard-step').filter({ hasText: 'Data' })).toHaveAttribute('aria-current', 'step')
  await expect(page.getByRole('combobox', { name: 'Data pool', exact: true })).toHaveValue(pool)
  // Back leaves the builder for Shared assets rather than reopening the scenario.
  await page.goBack()
  await expect(page).toHaveURL(/#\/shared-assets\?section=test-data/)
  await expect(usedBy.getByRole('link', { name: 'Customer journey', exact: true })).toBeVisible()
})
