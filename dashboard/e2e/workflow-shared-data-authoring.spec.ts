import { test, expect, expectDebugOutcome, editScenarioFromRow, idOf, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('a linked lease workflow is inserted, replayed against sandbox rows and bound in a scenario without JSON', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  // The script starts with one producer step; everything after it is authored in the editor.
  const scriptId = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Shared data authoring', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
    actions: [{ name: 'Publish a job', type: 'live_table', params: { alias: 'jobs', operation: 'push', values: { id: 'job-{{fn.uuid}}' }, result_prefix: 'published' } }],
  } } })))
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
  await page.reload()

  await page.getByRole('button', { name: '+ Add Action', exact: true }).click()
  const insert = page.getByRole('dialog', { name: 'Insert action or block' })
  await insert.getByLabel('Action type', { exact: true }).selectOption('template:lease_ack')
  await expect(insert.locator('input[name="alias"]')).toHaveValue('jobs')
  await expect(insert).toContainText('Your own steps go before “Acknowledge the leased row”.')
  await insert.getByRole('button', { name: 'Insert 3 steps', exact: true }).click()
  await expect(page.getByRole('treeitem', { name: 'Acknowledge the leased row' })).toHaveAttribute('aria-selected', 'true')
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${scriptId}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  expect((await saved).ok()).toBe(true)
  const stored = (await json(await request.get(`/api/v1/scripts/${scriptId}`))) as { script: { actions: Array<{ name: string }> } }
  expect(stored.script.actions.map(action => action.name)).toEqual(['Publish a job', 'Lease a row from jobs', 'When a row was leased'])
  expect(stored.script.actions[2]).toMatchObject({ control: { condition: { left: '{{var.leased.status}}', op: 'eq', right: 'ok' }, steps: [{ params: { operation: 'ack', lease_ref: 'leased' } }] } })

  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  const data = page.getByRole('region', { name: 'Data this script uses' })
  await expect(data).toContainText('Bind in a scenario before running')

  // Debug against sandbox rows made from the script, with no JSON.
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: /^Debug options/ }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('button', { name: 'Create tables from this script', exact: true }).click()
  await expect(options.getByRole('region', { name: 'Sandbox table jobs' })).toBeVisible()
  const newRow = options.getByLabel('id, new row', { exact: true })
  await newRow.fill('seeded-job')
  await newRow.press('Enter')
  await expect(options.getByLabel('id, row 1', { exact: true })).toHaveValue('seeded-job')
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  const started = page.waitForResponse(res => res.request().method() === 'POST' && new URL(res.url()).pathname === `/api/v1/scripts/${scriptId}/debug/sessions`)
  await page.locator('.topbar-debug-action-run').click()
  const session = await started
  expect(session.ok(), await session.text()).toBe(true)
  expect(session.request().postDataJSON().live_table_seeds).toEqual([{ alias: 'jobs', profile: 'native_queue', columns: ['id'], rows: [{ values: { id: 'seeded-job' } }] }])
  await expectDebugOutcome(page, 'passed')
  const rail = page.getByRole('complementary', { name: 'Actions', exact: true })
  await rail.locator('.debugger-action-row-main').filter({ hasText: 'Lease a row from jobs' }).click()
  const result = page.locator('.live-table-step-result').first()
  await expect(result).toContainText('saved as leased')
  await expect(result.getByRole('table', { name: 'Row for Lease a row from jobs' })).toContainText('seeded-job')

  // Bind the alias in a scenario: a live table for this run, starting from a pool.
  const pool = idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Job seed', schema: { id: 'string' }, columns: ['id'] } })))
  await json(await request.post(`/api/v1/data-pools/${pool}/rows`, { data: { data: { id: 'pool-job' } } }))
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Shared data authoring run', duration: '1m', groups: [{ name: 'Workers', script_id: scriptId, vusers: 1 }] } })))
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Shared data authoring run' }), 'Shared data authoring run')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Data/ }).click()
  const needs = page.getByRole('region', { name: 'Data the scripts need' })
  await expect(needs).toContainText('jobsNot bound')
  await needs.getByRole('button', { name: 'Bind jobs', exact: true }).click()
  await expect(needs).toContainText('Bound: live table for this run')
  await expect(page.getByRole('radio', { name: /^Live table for this run/ })).toBeChecked()
  await page.getByRole('combobox', { name: 'Data pool', exact: true }).selectOption(pool)
  await expect(page.locator('.scenario-data-mismatch')).toHaveCount(0)
  // A snapshot cannot run the lease, and says so before launch.
  await page.getByRole('radio', { name: /^Read-only snapshot/ }).check()
  await expect(page.locator('.scenario-data-mismatch')).toContainText('a read-only snapshot cannot run')
  await page.getByRole('radio', { name: /^Live table for this run/ }).check()
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  const savedScenario = mutation(page, 'PUT', `/api/v1/scenarios/${scenario}`)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  expect((await savedScenario).request().postDataJSON().data_pools[0]).toMatchObject({ alias: 'jobs', access_mode: 'live', scope: 'run', pool_id: pool, live_options: { seed_policy: 'current_at_start', profile: 'native_queue' } })
})
