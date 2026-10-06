import { test, expect, seedScript, mutation, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Server-backed tests: response interception is used only to inject a named
// failure, never to manufacture a successful workflow.
test.beforeEach(async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
})

test('[U03 U08] empty setup explains prerequisites and invalid saves do not persist', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/new-test?${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByText(/No scenarios are available yet/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeDisabled()
  await expect(page.getByRole('heading', { level: 4, name: 'Scripts And Groups' })).toBeVisible()
  await page.getByPlaceholder('Scenario name', { exact: true }).fill('Preserve this incomplete draft')
  await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await expect(page.getByText('No existing scripts are available for this project.')).toBeVisible()
  await expect(page.getByRole('searchbox', { name: 'Search scripts', exact: false })).toBeFocused()
  await page.getByRole('dialog', { name: 'Choose Script' }).getByRole('button', { name: 'Cancel' }).click()
  await page.locator('nav.sidebar-nav').getByRole('button', { name: 'Scripts', exact: true }).click()
  await page.getByRole('dialog', { name: 'Unsaved Changes' }).getByRole('button', { name: 'Keep editing' }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Preserve this incomplete draft')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Saved browser draft' })).toContainText('Preserve this incomplete draft')
  const list = await json(await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`))
  expect(JSON.stringify(list)).not.toContain('Preserve this incomplete draft')
})

for (const choice of ['Keep editing', 'Discard changes', 'Save changes']) {
  test(`[U07] script navigation: ${choice} has exact persistence semantics`, async ({ page }) => {
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const scriptId = await seedScript(request, workspace)
    await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    const workbench = page.getByRole('region', { name: 'Author center workbench' })
    await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
    await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:12345')
    await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
    const guard = page.getByRole('dialog', { name: 'Unsaved Changes' })
    await expect(guard).toBeVisible()
    await guard.getByRole('button', { name: choice, exact: true }).click()
    if (choice === 'Keep editing') {
      await expect(workbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:12345')
      await expect(page).toHaveURL(/mode=edit/)
    } else await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
    const stored = await json(await request.get(`/api/v1/scripts/${scriptId}`))
    expect(((stored.script as Record<string, unknown> || stored).config as { base_url: string }).base_url).toBe(choice === 'Save changes' ? 'http://127.0.0.1:12345' : 'http://127.0.0.1:1')
  })
}

for (const code of [500, 409, 401]) {
  test(`[U07 U18] failed script save (${code}) preserves edits and cannot overwrite stored content`, async ({ page }) => {
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const id = await seedScript(request, workspace)
    const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
    const initialDocument = await json(initial)
    const initialTag = initial.headers().etag
    expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
    const writes: string[] = []
    page.on('request', outgoing => {
      if (outgoing.method() === 'PUT' && new URL(outgoing.url()).pathname === `/api/v1/scripts/${id}`) writes.push(outgoing.headers()['if-match'])
    })
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    const workbench = page.getByRole('region', { name: 'Author center workbench' })
    await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
    const input = workbench.getByRole('textbox', { name: 'Base URL' })
    await input.fill('http://127.0.0.1:12345')
    await page.route(`**/api/v1/scripts/${id}`, async route => {
      if (route.request().method() === 'PUT') await route.fulfill({ status: code, json: { error: 'Workflow test: save rejected; retry after recovery.' } })
      else await route.continue()
    })
    try {
      const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
      const saveButton = page.getByRole('button', { name: 'Save script', exact: true })
      await saveButton.click()
      const rejectedResponse = await rejected
      expect(rejectedResponse.status()).toBe(code)
      expect(rejectedResponse.request().headers()['if-match']).toBe(initialTag)
      await expect(page.getByText('Workflow test: save rejected; retry after recovery.', { exact: false }).first()).toBeVisible()
      await expect(input).toHaveValue('http://127.0.0.1:12345')
      expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
      expect(writes).toEqual([initialTag])
      await page.unroute(`**/api/v1/scripts/${id}`)
      let reviewedTag = initialTag
      let expectedName = (initialDocument.script as Record<string, unknown>).name
      if (code === 500) {
        // A nondefinitive outcome revokes retry authority, even though this
        // injected response did not reach storage. A real other writer proves
        // that explicit review is needed before another conditional save.
        await expect(saveButton).toBeDisabled()
        const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
        await expect(conflict).toBeVisible()
        expectedName = 'Changed by another author during recovery'
        await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': initialTag }, data: {
          project_id: workspace.projectId, script: { ...(initialDocument.script as Record<string, unknown>), name: expectedName },
        } }))
        const reviewed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
        const reviewedDocument = await json(reviewed)
        reviewedTag = reviewed.headers().etag
        expect(reviewedTag).not.toBe(initialTag)
        expect(reviewedDocument.script).toMatchObject({ name: expectedName, config: { base_url: 'http://127.0.0.1:1' } })
        await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
        await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
        await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
        await expect(conflict.getByLabel('Script revision field diff')).toContainText('config.base_url')
        await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('http://127.0.0.1:12345')
        await expect(input).toHaveValue('http://127.0.0.1:12345')
        await expect(saveButton).toBeDisabled()
        expect(writes).toEqual([initialTag])
        await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
        await expect(conflict).toHaveCount(0)
        await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
        await expect(input).toHaveValue('http://127.0.0.1:12345')
        await expect(saveButton).toBeEnabled()
        expect(writes).toEqual([initialTag])
        expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
      }
      const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
      await saveButton.click()
      const savedResponse = await saved
      await json(savedResponse)
      expect(savedResponse.request().headers()['if-match']).toBe(reviewedTag)
      expect(writes).toEqual([initialTag, reviewedTag])
      expect((await json(await request.get(`/api/v1/scripts/${id}`))).script).toMatchObject({ name: expectedName, config: { base_url: 'http://127.0.0.1:12345' } })
    } finally { await page.unrouteAll({ behavior: 'wait' }) }
  })
}

test('[U04] project scope and browser history never substitute a similarly named script', async ({ page }) => {
  const request = page.context().request
  const a = await createWorkspace(request)
  const b = await createWorkspace(request)
  const aId = await seedScript(request, a, 'Shared_name', 'http://127.0.0.1:1111')
  const bId = await seedScript(request, b, 'Shared_name', 'http://127.0.0.1:2222')
  for (const [workspace, id, port] of [[a, aId, 1111], [b, bId, 2222]] as const) {
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('tab', { name: 'Runtime Settings' }).click()
    await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue(`http://127.0.0.1:${port}`)
    await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${workspace.label} project`) })).toBeVisible()
  }
  await page.reload()
  await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:2222')
  await page.goBack()
  await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:1111')
})

test('[U06] malformed OpenAPI can be replaced and validated without creating a library entry', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  const file = controls.locator('input[type=file]')
  await file.setInputFiles({ name: 'broken.yaml', mimeType: 'text/yaml', buffer: Buffer.from('openapi: [broken') })
  await controls.getByRole('textbox', { name: 'Script Name' }).fill('Recovery_Import')
  const failed = mutation(page, 'POST', '/api/v1/scripts/import/openapi')
  await controls.getByText('Advanced validation only', { exact: true }).click()
  await controls.getByRole('button', { name: 'Validate Only' }).click()
  expect((await failed).ok()).toBe(false)
  await expect(page.locator('.sample-import-error')).toBeVisible()
  await file.setInputFiles('e2e/fixtures/tier0-orders.openapi.yaml')
  await controls.getByRole('button', { name: 'Validate Only' }).click()
  await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  expect(JSON.stringify(await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)))).not.toContain('Recovery_Import')
})
