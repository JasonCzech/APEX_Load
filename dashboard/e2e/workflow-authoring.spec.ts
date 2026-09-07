import { test, expect, seedScript, mutation, json } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Server-backed tests: response interception is used only to inject a named
// failure, never to manufacture a successful workflow.
test('[U03 U08] empty setup explains prerequisites and invalid saves do not persist', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await page.goto(`/#/new-test?${scopeQuery(workspace)}`)
  await expect(page.getByText(/No scenarios are available yet/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  await page.getByRole('button', { name: 'Save Scenario', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Name the scenario')
  await page.getByRole('textbox', { name: 'Scenario name', exact: true }).fill('Preserve this incomplete draft')
  await page.getByRole('button', { name: 'Save Scenario', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('selected script')
  await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Scripts/ }).click()
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await expect(page.getByText('No existing scripts are available for this project.')).toBeVisible()
  await expect(page.getByRole('searchbox', { name: 'Search scripts', exact: false })).toBeFocused()
  await page.locator('nav.sidebar-nav').getByRole('button', { name: 'Scripts', exact: true }).click()
  await page.getByRole('dialog', { name: 'Unsaved Changes' }).getByRole('button', { name: 'Keep editing' }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Preserve this incomplete draft')
  const list = await json(await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`))
  expect(JSON.stringify(list)).not.toContain('Preserve this incomplete draft')
})

for (const choice of ['Keep editing', 'Discard changes', 'Save changes']) {
  test(`[U07] script navigation: ${choice} has exact persistence semantics`, async ({ page, request }) => {
    const workspace = await createWorkspace(request)
    const scriptId = await seedScript(request, workspace)
    await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
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
  test(`[U07 U18] failed script save (${code}) preserves edits and cannot overwrite stored content`, async ({ page, request }) => {
    const workspace = await createWorkspace(request)
    const id = await seedScript(request, workspace)
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    const workbench = page.getByRole('region', { name: 'Author center workbench' })
    await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
    const input = workbench.getByRole('textbox', { name: 'Base URL' })
    await input.fill('http://127.0.0.1:12345')
    await page.route(`**/api/v1/scripts/${id}`, async route => {
      if (route.request().method() === 'PUT') await route.fulfill({ status: code, json: { error: 'Workflow test: save rejected; retry after recovery.' } })
      else await route.continue()
    })
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expect(page.getByText('Workflow test: save rejected; retry after recovery.', { exact: false }).first()).toBeVisible()
    await expect(input).toHaveValue('http://127.0.0.1:12345')
    expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
    await page.unroute(`**/api/v1/scripts/${id}`)
    const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await json(await saved)
    expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:12345' })
  })
}

test('[U04] project scope and browser history never substitute a similarly named script', async ({ page, request }) => {
  const a = await createWorkspace(request)
  const b = await createWorkspace(request)
  const aId = await seedScript(request, a, 'Shared_name', 'http://127.0.0.1:1111')
  const bId = await seedScript(request, b, 'Shared_name', 'http://127.0.0.1:2222')
  for (const [workspace, id, port] of [[a, aId, 1111], [b, bId, 2222]] as const) {
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    await page.getByRole('tab', { name: 'Runtime Settings' }).click()
    await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue(`http://127.0.0.1:${port}`)
    await expect(page.getByRole('button', { name: new RegExp(`Current project: ${workspace.label}`) })).toBeVisible()
  }
  await page.reload()
  await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:2222')
  await page.goBack()
  await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:1111')
})

test('[U06] malformed OpenAPI can be replaced and validated without creating a library entry', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: /Import From Source/ }).click()
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  const file = controls.locator('input[type=file]')
  await file.setInputFiles({ name: 'broken.yaml', mimeType: 'text/yaml', buffer: Buffer.from('openapi: [broken') })
  await controls.getByRole('textbox', { name: 'Script Name' }).fill('Recovery_Import')
  const failed = mutation(page, 'POST', '/api/v1/scripts/import/openapi')
  await controls.getByRole('button', { name: 'Validate Only' }).click()
  expect((await failed).ok()).toBe(false)
  await expect(page.locator('.sample-import-error')).toBeVisible()
  await file.setInputFiles('e2e/fixtures/tier0-orders.openapi.yaml')
  await controls.getByRole('button', { name: 'Validate Only' }).click()
  await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  expect(JSON.stringify(await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)))).not.toContain('Recovery_Import')
})
