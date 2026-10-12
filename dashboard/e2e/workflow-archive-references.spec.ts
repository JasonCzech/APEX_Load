import { test, expect, seedScript, seedScenario, json, login, editScenarioFromRow } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('scenario drafts retain archived script references through read-only review', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const name = `Retained source ${Date.now()}`
  const scriptId = await seedScript(request, workspace, name)
  await seedScenario(request, workspace, scriptId, name + ' scenario')
  const source = await json(await request.get(`/api/v1/scripts/${scriptId}`))
  await json(await request.post(`/api/v1/scripts/${scriptId}/archive`, { data: { updated_at: (source.script as { updated_at: string }).updated_at } }))
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await editScenarioFromRow(page.getByRole('row').filter({ hasText: name + ' scenario' }), name + ' scenario')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Start/ }).click()
  const selected = page.locator('.scenario-script-select-button').first()
  await expect(selected).toContainText(name)
  await expect(selected).toContainText('Archived')
  await page.getByRole('button', { name: 'Review script', exact: true }).first().click()
  await expect(page.getByRole('region', { name: 'Archived script', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Back to scenario draft', exact: true }).click()
  await expect(selected).toContainText(name)
  await expect(selected).toContainText('Archived')
})
