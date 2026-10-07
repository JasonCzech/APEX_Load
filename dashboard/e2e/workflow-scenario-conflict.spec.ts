import { test, expect, seedScript, seedScenario, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import type { Page } from '@playwright/test'

// Two editors open the same scenario. The first save advances its revision;
// the second editor's save is refused (412), keeps its draft, and saves only
// after loading the current baseline and reapplying the draft onto it.
test('two scenario editors reconcile a stale save without losing either change', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const name = `Conflict fixture ${Date.now()}`
  const scriptId = await seedScript(request, workspace, `${name} script`)
  const id = await seedScenario(request, workspace, scriptId, name)
  const initial = await request.get(`/api/v1/scenarios/${id}?view=authoring`)
  await json(initial)
  const initialTag = initial.headers().etag
  expect(initialTag).toMatch(/^"scenario-authoring-v1:scn-v1-[a-f0-9]{64}"$/)

  const other = await page.context().newPage()
  await other.setViewportSize({ width: 1600, height: 1000 })
  try {
    const openEditor = async (editor: Page) => {
      await editor.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
      if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await editor.reload()
      const read = editor.waitForResponse(response => response.request().method() === 'GET' && new URL(response.url()).pathname === `/api/v1/scenarios/${id}`)
      await editor.locator('.scenario-library-table').getByRole('row').filter({ hasText: name }).getByRole('button', { name: 'Edit', exact: true }).click()
      expect((await read).headers().etag).toBe(initialTag)
      await expect(editor.getByPlaceholder('Scenario name')).toHaveValue(name)
    }
    await openEditor(page)
    await openEditor(other)

    await page.getByPlaceholder('Scenario name').fill(`${name} first`)
    const firstSave = mutation(page, 'PUT', `/api/v1/scenarios/${id}`)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    const firstResponse = await firstSave
    expect(firstResponse.request().headers()['if-match']).toBe(initialTag)
    expect(firstResponse.headers()['x-apex-scenario-save-mode']).toBe('conditioned')
    await json(firstResponse)
    await expect(page.getByText('Editing · Saved', { exact: true })).toBeVisible()

    let writes = 0
    other.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scenarios/${id}`) writes++ })
    const staleName = other.getByPlaceholder('Scenario name')
    await staleName.fill(`${name} retained`)
    const rejected = mutation(other, 'PUT', `/api/v1/scenarios/${id}`)
    await other.getByRole('button', { name: 'Save draft', exact: true }).click()
    const conflictResponse = await rejected
    expect(conflictResponse.status()).toBe(412)
    expect(conflictResponse.request().headers()['if-match']).toBe(initialTag)
    const conflict = other.getByRole('region', { name: 'Scenario save reconciliation' })
    await expect(conflict).toBeVisible()
    await expect(conflict).toContainText('Another change was saved to this scenario')
    await expect(staleName).toHaveValue(`${name} retained`)

    // A new conditioned save requires the reloaded baseline.
    await other.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect(other.locator('.page-error-banner')).toContainText('Resolve the scenario revision review')
    expect(writes).toBe(1)

    await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
    await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
    await expect(staleName).toHaveValue(`${name} retained`)
    await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
    const diff = conflict.getByLabel('Scenario revision field diff')
    await expect(diff).toContainText('scenario.name')
    await expect(diff).toContainText('Changed in your draft')
    await expect(diff).not.toContainText(`${name} retained`)
    expect(writes).toBe(1)

    await other.setViewportSize({ width: 760, height: 1000 })
    await conflict.scrollIntoViewIfNeeded()
    const box = await conflict.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(760)
    await conflict.screenshot({ path: testInfo.outputPath('scenario-revision-conflict.png'), animations: 'disabled' })
    await other.setViewportSize({ width: 1600, height: 1000 })

    await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
    await expect(conflict).toHaveCount(0)
    await expect(staleName).toHaveValue(`${name} retained`)
    const reapplied = mutation(other, 'PUT', `/api/v1/scenarios/${id}`)
    await other.getByRole('button', { name: 'Save draft', exact: true }).click()
    const reappliedResponse = await reapplied
    expect(reappliedResponse.request().headers()['if-match']).toBe(firstResponse.headers().etag)
    expect(reappliedResponse.request().headers()['if-match']).not.toBe(initialTag)
    expect(reappliedResponse.headers()['x-apex-scenario-save-mode']).toBe('conditioned')
    await json(reappliedResponse)
    expect(writes).toBe(2)
    const final = await json(await request.get(`/api/v1/scenarios/${id}`))
    expect(final.name).toBe(`${name} retained`)

    // The first editor's baseline is now stale in turn; reloading discards its
    // (clean) draft and adopts the second editor's save.
    await page.getByPlaceholder('Scenario name').fill(`${name} first again`)
    const stale = mutation(page, 'PUT', `/api/v1/scenarios/${id}`)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    expect((await stale).status()).toBe(412)
    const firstConflict = page.getByRole('region', { name: 'Scenario save reconciliation' })
    await firstConflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
    await firstConflict.getByRole('button', { name: 'Reload scenario and discard draft', exact: true }).click()
    await expect(firstConflict).toHaveCount(0)
    await expect(page.getByPlaceholder('Scenario name')).toHaveValue(`${name} retained`)
  } finally { await other.close() }
})

// Deleting from the library re-reads the scenario: a scenario changed after
// the list loaded is refused locally, and the delete carries the current tag.
test('a library delete is refused for a scenario changed after the list loaded', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const name = `Delete fixture ${Date.now()}`
  const scriptId = await seedScript(request, workspace, `${name} script`)
  const id = await seedScenario(request, workspace, scriptId, name)
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const row = page.locator('.scenario-library-table').getByRole('row').filter({ hasText: name })
  await expect(row).toBeVisible()

  const current = await request.get(`/api/v1/scenarios/${id}?view=authoring`)
  const projection = await json(current) as { scenario: Record<string, unknown> }
  const changed = await request.put(`/api/v1/scenarios/${id}`, { headers: { 'If-Match': current.headers().etag }, data: { ...projection.scenario, duration: '3m' } })
  await json(changed)

  let deletes = 0
  page.on('request', item => { if (item.method() === 'DELETE' && new URL(item.url()).pathname === `/api/v1/scenarios/${id}`) deletes++ })
  const requestDelete = async () => {
    const more = row.locator('details.scenario-more-actions')
    if (!await more.evaluate(element => (element as HTMLDetailsElement).open)) await more.locator('summary').click()
    await row.getByRole('button', { name: 'Delete', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Delete Scenario', exact: true }).click()
  }
  const relisted = page.waitForResponse(response => response.request().method() === 'GET' && new URL(response.url()).pathname === '/api/v1/scenarios')
  await requestDelete()
  await expect(page.locator('.page-error-banner')).toContainText('changed after the list was loaded')
  expect(deletes).toBe(0)
  await relisted
  await expect(row).toBeVisible()

  const deleted = mutation(page, 'DELETE', `/api/v1/scenarios/${id}`)
  await requestDelete()
  const response = await deleted
  expect(response.request().headers()['if-match']).toMatch(/^"scenario-authoring-v1:scn-v1-[a-f0-9]{64}"$/)
  await json(response)
  await expect(row).toHaveCount(0)
})
