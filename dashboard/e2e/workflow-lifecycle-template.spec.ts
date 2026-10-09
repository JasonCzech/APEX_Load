import { test, expect, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

type ScriptDocument = { name?: string; config?: { base_url?: string }; actions?: { name: string; target?: string }[] }
type StoredScript = ScriptDocument & { id?: string; script?: ScriptDocument }

// AUTH-2 "Template/component/clone": a starter template opens an independent
// draft. Saving it creates the project's own script, which reopens exactly
// as saved, and the template the draft came from is unchanged.
test('a starter template opens an independent draft that saves, reopens and leaves the template unchanged', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  type Catalog = { templates: { id: string; script: ScriptDocument }[] }
  const templateOf = async () => (await json(await request.get('/api/v1/scripts/authoring/templates')) as unknown as Catalog).templates.find(template => template.id === 'http-api-smoke')
  const before = await templateOf()
  expect(before?.script.actions?.map(action => action.name)).toEqual(['Health Check', 'Business Transaction'])

  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Create', exact: true }).click()
  await page.getByRole('tab', { name: 'Starter Templates' }).click()
  await page.getByRole('button', { name: 'Use HTTP API Smoke' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Create Script' })).toBeVisible()

  const name = `Template_${Date.now()}`
  const scriptName = page.locator('.side-panel-script-name input')
  await expect(scriptName).toHaveValue('HTTP API Smoke')
  await scriptName.fill(name)
  const created = mutation(page, 'POST', '/api/v1/scripts')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  const response = await created
  const saved = await json(response) as StoredScript
  const id = saved.id || ''
  expect(id).not.toBe('')

  // The saved script is the project's own copy of the template's content.
  const stored = await json(await request.get(`/api/v1/scripts/${id}`)) as StoredScript
  const document = stored.script || stored
  expect(document.name).toBe(name)
  expect(document.actions?.map(action => [action.name, action.target])).toEqual(before?.script.actions?.map(action => [action.name, action.target]))
  expect(document.config?.base_url).toBe(before?.script.config?.base_url)

  // Reopening shows exactly what was saved.
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(scriptName).toHaveValue(name)
  await expect(page.getByRole('navigation', { name: 'Script outline' })).toContainText('Business Transaction')

  // The template is an independent snapshot: the save never changed it.
  expect(await templateOf()).toEqual(before)
})
