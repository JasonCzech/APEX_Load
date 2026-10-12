import { test, expect, json, idOf, login, seedScript, seedScenario, signOut, showLibraryFilters } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('Operators move scripts individually and in bulk, recover failures, and retain assignments after reload', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const first = await seedScript(request, workspace, 'Folder_First')
  const second = await seedScript(request, workspace, 'Folder_Second')
  const scenarioId = await seedScenario(request, workspace, first)
  const original = await json(await request.get(`/api/v1/scripts/${first}`))
  const parent = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, { data: { name: 'APIs', category: 'scripts' } })))
  const folder = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, { data: { name: 'Checkout', category: 'scripts', parent_id: parent } })))
  await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
  // API-seeded projects must be loaded into the signed-in workspace provider.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const row = page.locator('.scripts-library-table tbody tr').filter({ hasText: 'Folder_First' })
  await row.getByText('More', { exact: true }).click()
  await row.getByRole('button', { name: 'Move to folder…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Move to folder', exact: true })
  await expect(dialog.getByRole('button', { name: 'Move', exact: true })).toBeDisabled()
  await dialog.getByLabel('Destination folder').selectOption({ label: 'APIs / Checkout' })
  await page.screenshot({ path: testInfo.outputPath('move-scripts-dialog.png'), animations: 'disabled' })
  await dialog.getByRole('button', { name: 'Move', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(row).toContainText('Checkout')
  await page.reload()
  await expect(row).toContainText('Checkout')
  await showLibraryFilters(page)
  await page.locator('.library-folder-filter').selectOption(folder)
  await row.getByText('More', { exact: true }).click()
  await row.getByRole('button', { name: 'Move to folder…' }).click()
  await dialog.getByLabel('Destination folder').selectOption('')
  await dialog.getByRole('button', { name: 'Move', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(row).toHaveCount(0)
  await expect(page.locator('.library-folder-filter')).toHaveValue(folder)
  await page.locator('.library-folder-filter').selectOption('all')
  await page.getByLabel('Select all scripts on this page', { exact: true }).check()
  await page.getByRole('button', { name: 'Move Selected…' }).click()
  await dialog.getByLabel('Destination folder').selectOption(folder)
  let firstMoves = 0
  page.on('request', req => { if (req.method() === 'PATCH' && req.url().endsWith(`/scripts/${first}/move`)) firstMoves++ })
  await page.route(`**/scripts/${second}/move`, route => route.fulfill({ status: 409, json: { error: 'Folder test: concurrent change' } }))
  await dialog.getByRole('button', { name: 'Move', exact: true }).click()
  await expect(dialog).toContainText('Moved 1; unchanged 0; failed 1.')
  await expect(dialog).toContainText('Folder_Second: Folder test: concurrent change')
  await page.unroute(`**/scripts/${second}/move`)
  await dialog.getByRole('button', { name: 'Retry failed moves' }).click()
  await expect(dialog).toBeHidden()
  expect(firstMoves).toBe(1)
  await page.reload()
  for (const name of ['Folder_First', 'Folder_Second']) {
    await expect(page.locator('.scripts-library-table tbody tr').filter({ hasText: name })).toContainText('Checkout')
  }
  for (const id of [first, second]) {
    const stored = await json(await request.get(`/api/v1/scripts/${id}`))
    expect(stored.folder_id).toBe(folder)
  }
  const moved = await json(await request.get(`/api/v1/scripts/${first}`))
  expect((moved.script as Record<string, unknown>).actions).toEqual((original.script as Record<string, unknown>).actions)
  expect((moved.script as Record<string, unknown>).config).toEqual((original.script as Record<string, unknown>).config)
  const scenario = await json(await request.get(`/api/v1/scenarios/${scenarioId}`))
  expect(JSON.stringify(scenario)).toContain(first)
})

test('Viewers cannot move scripts through the library or API', async ({ page }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'requires local viewer persona')
  await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = await seedScript(request, workspace, 'Read_only_folder_script')
  const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
  const viewer = users.find(user => user.subject === 'local:user')!
  await json(await request.post(`/api/v1/auth/users/${viewer.id}/bindings`, {
    data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' },
  }))
  await signOut(page)
  await login(page, 'user')
  await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
  // API-seeded projects must be loaded into the signed-in workspace provider.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.locator('.scripts-library-table')).toContainText('Read_only_folder_script')
  await expect(page.getByRole('button', { name: 'Move to folder…' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Move Selected…' })).toHaveCount(0)
  expect((await request.patch(`/api/v1/scripts/${id}/move`, { data: { folder_id: null } })).status()).toBe(403)
})
