import { test, expect, json, login, seedScript, idOf } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { storedZip } from './support/zip'

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

test('saved blocks are listed, previewed and deleted without opening a script', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await json(await request.post('/api/v1/scripts/authoring/components', { data: { project_id: workspace.projectId, name: 'Sign-in steps', kind: 'request_group', protocols: ['http'], summary: 'Opens a session and signs in', actions: [{ name: 'Open session', type: 'GET', method: 'GET', target: '/session' }, { name: 'Sign in', type: 'POST', method: 'POST', target: '/login' }] } }))

  await page.goto(`/#/shared-assets?section=saved-blocks&${scopeQuery(workspace)}`)
  const row = page.locator('.pp-row').filter({ hasText: 'Sign-in steps' })
  await expect(row).toContainText('2 steps')
  // Only blocks saved in the project; the built-in catalog is not listed here.
  await expect(page.locator('.pp-row')).toHaveCount(1)
  await row.getByRole('button', { name: /^Sign-in steps/ }).click()
  await expect(row.getByRole('group', { name: 'Steps of Sign-in steps' })).toContainText('Open session')

  await row.getByRole('button', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete saved block' })
  await expect(dialog).toContainText('Scripts that inserted it keep their copies of its steps.')
  await dialog.getByRole('button', { name: 'Delete saved block', exact: true }).click()
  await expect(page.getByText('No saved blocks yet.', { exact: true })).toBeVisible()
})

// WP6: each place that uses a shared asset opens it in Shared assets over
// itself, and is back where it was, showing the asset as it is now.
async function boundPoolWorkspace(page: import('@playwright/test').Page) {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Round trip HTTP', 'http://127.0.0.1:1', '/orders/{{data.customers.id}}')
  const pool = idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Round trip customers', schema: { id: 'string' } } })))
  await json(await request.post(`/api/v1/data-pools/${pool}/rows`, { data: { data: { id: 'first' } } }))
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Round trip journey', duration: '1m', groups: [{ name: 'Load', script_id: script, vusers: 1 }], data_pools: [{ pool_id: pool, alias: 'customers', access_mode: 'read_only', selection_strategy: 'sequential' }] } })))
  return { request, workspace, script, pool, scenario }
}

test('the scenario Data step edits its pool in Shared assets and comes back to the same step', async ({ page }) => {
  const { workspace, scenario } = await boundPoolWorkspace(page)
  await page.goto(`/#/tests?view=builder&scenarioId=${scenario}&step=data&${scopeQuery(workspace)}`)
  await expect(page.locator('.scenario-builder-mode')).toHaveText('Editing')
  await expect(page.locator('.scenario-wizard-step').filter({ hasText: 'Data' })).toHaveAttribute('aria-current', 'step')
  await page.getByText('Preview rows and import CSV', { exact: true }).click()
  await expect(page.getByText(/1 rows in shared pool/)).toBeVisible()

  await page.getByRole('button', { name: "Edit this pool's rows", exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Shared assets · Test data' })
  await dialog.getByRole('textbox', { name: 'id, new row', exact: true }).fill('second')
  await dialog.getByRole('button', { name: 'Add row', exact: true }).click()
  await expect(dialog.getByRole('textbox', { name: 'id, new row', exact: true })).toHaveValue('')
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()

  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(/#\/tests\?/)
  await expect(page.locator('.scenario-wizard-step').filter({ hasText: 'Data' })).toHaveAttribute('aria-current', 'step')
  await expect(page.getByText(/2 rows in shared pool/)).toBeVisible()
})

test('the script editor opens the pool a scenario binds in Shared assets and stays open', async ({ page }) => {
  const { workspace, script, pool } = await boundPoolWorkspace(page)
  await page.goto(`/#/scripts?mode=edit&scriptId=${script}&${scopeQuery(workspace)}`)
  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  const data = page.getByRole('region', { name: 'Data this script uses' })
  await expect(data).toContainText('Round trip journey: read-only snapshot')
  await data.getByRole('button', { name: 'Open the pool Round trip journey binds as customers', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Shared assets · Test data' })
  await expect(dialog.getByRole('link', { name: 'Open in Shared assets', exact: true })).toHaveAttribute('href', new RegExp(`resourceId=${pool}`))
  await expect(dialog.getByRole('textbox', { name: 'id, new row', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`scriptId=${script}`))
  await expect(data).toContainText('Round trip journey: read-only snapshot')
})

test('the Insert dialog manages saved blocks in Shared assets and lists what is left', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Blocks HTTP')
  for (const name of ['Keep this block', 'Retire this block']) {
    await json(await request.post('/api/v1/scripts/authoring/components', { data: { project_id: workspace.projectId, name, kind: 'request_group', protocols: ['http'], summary: name, actions: [{ name: `${name} step`, type: 'GET', method: 'GET', target: '/session' }] } }))
  }
  await page.goto(`/#/scripts?mode=edit&scriptId=${script}&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: '+ Add Action', exact: true }).click()
  const insert = page.getByRole('dialog', { name: 'Insert action or block' })
  await insert.getByText('Add action from a block', { exact: true }).click()
  const reusable = insert.getByRole('button', { name: /^Reusable Blocks/ })
  if (await reusable.getAttribute('aria-expanded') === 'false') await reusable.click()
  const blockCards = insert.locator('.user-block-card')
  await expect(blockCards.filter({ hasText: 'Retire this block' })).toHaveCount(1)
  await insert.getByRole('button', { name: 'Manage saved blocks', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Shared assets · Saved blocks' })
  await dialog.locator('.pp-row').filter({ hasText: 'Retire this block' }).getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('dialog', { name: 'Delete saved block' }).getByRole('button', { name: 'Delete saved block', exact: true }).click()
  await expect(dialog.locator('.pp-row')).toHaveCount(1)
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()

  await expect(dialog).toBeHidden()
  await expect(blockCards.filter({ hasText: 'Keep this block' })).toHaveCount(1)
  await expect(blockCards.filter({ hasText: 'Retire this block' })).toHaveCount(0)
})

test('an import review creates the secret it needs in Shared assets and sees it configured', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const archive = storedZip([
    { name: 'Secrets_Collection/opencollection.yml', content: 'opencollection: 1.0.0\ninfo: {name: Secrets round trip, uid: secrets-round-trip}\n' },
    { name: 'Secrets_Collection/Account/SignIn.yml', content: `info: {name: SignIn, uid: sign-in, type: http, seq: 1}
http:
  method: GET
  url: https://example.test/account
  auth:
    type: basic
    username: "{{username}}"
    password: "{{password}}"
` },
  ])
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /Bruno Collection/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type="file"]').first().setInputFiles({ name: 'Secrets_Collection.zip', mimeType: 'application/zip', buffer: archive })
  await controls.getByRole('button', { name: 'Inspect & review' }).click()
  await controls.getByRole('tab', { name: /^Credentials(,|$)/ }).click()
  await expect(controls.getByRole('button', { name: 'Credentials: 2 missing', exact: true })).toBeVisible()
  const names = await controls.getByRole('textbox', { name: /Secret reference name/ }).evaluateAll(inputs => inputs.map(input => (input as HTMLInputElement).value))
  const password = names.find(name => name.startsWith('PASSWORD_'))!
  expect(password).toBeTruthy()
  await controls.getByRole('button', { name: 'Open Secrets', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Shared assets · Secrets' })
  const vault = dialog.locator('.project-secrets-panel')
  await expect(vault.getByText('Loading secret metadata…')).toBeHidden()
  await vault.getByLabel('Secret name').fill(password)
  await vault.getByLabel('Secret value').fill('test-only-password')
  await vault.getByRole('button', { name: 'Create secret', exact: true }).click()
  await expect(vault.locator('.project-secret-row').filter({ hasText: password })).toBeVisible()
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()

  // Back in the review, which has checked its secrets again.
  await expect(dialog).toBeHidden()
  await expect(controls.getByRole('button', { name: 'Credentials: 1 missing', exact: true })).toBeVisible()
})
