import { randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { test, expect, json, login, seedScript, idOf, mutation, editScenarioFromRow } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import type { ProjectLiveTableView } from '../src/types/projectLiveTables'

// These workflows use a real durable controller. The local harness and CI must
// provide an isolated wrapping key; no project data or production table is used.
test('project table operator reconciles conflicts and lost responses before governing its lifecycle', async ({ page }, testInfo) => {
  test.setTimeout(120000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const collection = `/api/v1/projects/${workspace.projectId}/live-tables`
  await page.goto(`/#/repository?setting=live-tables&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByText('Create project live table', { exact: true }).click()
  const create = page.getByRole('group', { name: 'Starting definition' })
  await create.getByLabel('Name', { exact: true }).fill('Project work')
  await create.getByLabel('Columns', { exact: true }).fill('id,note')
  const created = mutation(page, 'POST', collection)
  await create.getByRole('button', { name: 'Create table', exact: true }).click()
  const table = await json(await created) as unknown as ProjectLiveTableView
  const path = `${collection}/${table.id}`
  const detail = page.getByRole('region', { name: 'Project live table details' })
  await expect(detail.getByRole('heading', { name: 'Project work', exact: true })).toBeVisible()
  await detail.getByLabel('Include id', { exact: true }).check()
  await detail.getByLabel('Value for id', { exact: true }).fill('original')
  const appended = mutation(page, 'POST', `${path}/mutations`)
  await detail.getByRole('button', { name: 'Append row', exact: true }).click()
  const appendResult = await json(await appended)
  await expect(detail).toContainText(`Data revision ${(appendResult.result as { revision: string }).revision}`)
  await expect(detail).toContainText('Command completed')
  await expect(detail.getByRole('table', { name: 'Project live table rows' })).toHaveCount(0)
  await detail.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).click()
  const rows = detail.getByRole('table', { name: 'Project live table rows' })
  await expect(rows).toContainText('original')
  await expect(rows).toContainText('absent')
  await rows.getByRole('button', { name: 'Edit row 1', exact: true }).click()
  await detail.getByLabel('Value for id', { exact: true }).fill('my reviewed edit')
  const current = await json(await request.get(path)) as unknown as ProjectLiveTableView
  // A second operator commits after our deliberate preview. The draft must
  // keep its reviewed revision even when the metadata poll sees the new one.
  const concurrentResult = await json(await request.post(`${path}/mutations`, { data: { idempotency_key: randomUUID(), issued_at: new Date().toISOString(), expected_instance_id: current.instance_id, expected_revision: current.stats!.revision, operation: 'append', values: { id: 'concurrent' } } }))
  const conflict = mutation(page, 'POST', `${path}/mutations`)
  await detail.getByRole('button', { name: 'Apply row change', exact: true }).click()
  const conflictResponse = await conflict
  expect(conflictResponse.status()).toBe(409)
  await expect(detail.getByLabel('Value for id', { exact: true })).toHaveValue('my reviewed edit')
  await expect(detail.getByRole('alert')).toContainText('draft is preserved')
  await expect(detail).toContainText(`Data revision ${(concurrentResult.result as { revision: string }).revision}`)
  await detail.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).click()
  await detail.getByRole('button', { name: 'Use reviewed row version and keep my draft', exact: true }).click()
  const edited = mutation(page, 'POST', `${path}/mutations`)
  await detail.getByRole('button', { name: 'Apply row change', exact: true }).click()
  const editResult = await json(await edited)
  await expect(detail).toContainText(`Data revision ${(editResult.result as { revision: string }).revision}`)
  await expect(detail.getByRole('button', { name: 'Append row', exact: true })).toBeVisible()

  // Drop only the response after the server commits. A retry must use the exact
  // original key, timestamp and payload and recover its outcome without adding.
  const commands: unknown[] = []
  let lostResponse = true
  await page.route(`**${path}/mutations`, async route => {
    commands.push(route.request().postDataJSON())
    if (lostResponse) {
      lostResponse = false
      const response = await route.fetch()
      expect(response.ok(), await response.text()).toBeTruthy()
      await route.abort('failed')
    } else await route.continue()
  })
  await detail.getByLabel('Include id', { exact: true }).check()
  await detail.getByLabel('Value for id', { exact: true }).fill('once only')
  await detail.getByRole('button', { name: 'Append row', exact: true }).click()
  await expect(detail.getByRole('button', { name: 'Retry same command', exact: true })).toBeVisible()
  await expect(detail.getByRole('button', { name: 'Append row', exact: true })).toBeDisabled()
  const recovered = mutation(page, 'POST', `${path}/mutations`)
  await detail.getByRole('button', { name: 'Retry same command', exact: true }).click()
  const recoveredResult = await json(await recovered)
  await expect(detail).toContainText(`Data revision ${(recoveredResult.result as { revision: string }).revision}`)
  await expect(detail).toContainText('Original outcome recovered')
  expect(commands).toHaveLength(2)
  expect(commands[1]).toEqual(commands[0])
  await page.unroute(`**${path}/mutations`)
  await detail.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).click()
  await expect(rows.getByRole('row')).toHaveCount(4)
  await expect(rows).toContainText('my reviewed edit')
  await expect(rows.getByRole('cell', { name: 'once only', exact: true })).toHaveCount(1)
  const download = page.waitForEvent('download')
  await detail.getByRole('button', { name: 'Export table', exact: true }).click()
  const exportPath = await (await download).path()
  expect(exportPath).toBeTruthy()
  expect(await readFile(exportPath!, 'utf8')).toContain('once only')
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath('project-live-table-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 600, height: 900 })
  expect(await detail.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
  await page.screenshot({ path: testInfo.outputPath('project-live-table-narrow.png'), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })

  await detail.getByText('Table lifecycle', { exact: true }).click()
  const lifecycle = async (action: string) => {
    await detail.getByRole('combobox', { name: 'Action', exact: true }).selectOption(action)
    await detail.getByLabel('Reason', { exact: true }).fill(`Browser verification: ${action}`)
    await detail.getByLabel('I confirm this lifecycle change for Project work.').check()
    const changed = mutation(page, 'POST', `${path}/lifecycle`)
    await detail.getByRole('button', { name: 'Apply lifecycle change', exact: true }).click()
    const changedTable = await json(await changed)
    await expect(detail).toContainText(`Configuration revision ${changedTable.config_revision}`)
  }
  await lifecycle('quiesce')
  await expect(detail).toContainText('quiesced')
  await lifecycle('reset')
  await expect.poll(async () => (await json(await request.get(path))).instance_id).not.toBe(table.instance_id)
  await lifecycle('resume')
  await expect(detail).toContainText('· active ·')
  await lifecycle('quiesce')
  await expect(detail).toContainText('quiesced')
  await lifecycle('archive')
  await expect(detail).toContainText('archived')
  await detail.getByRole('button', { name: 'Preview up to 100 rows', exact: true }).click()
  await expect(detail).toContainText('of 0 rows')
})

test('scenario explicitly references a project table while existing pool bindings stay snapshots', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Project binding HTTP')
  const pool = idOf(await json(await request.post('/api/v1/data-pools', { data: { project_id: workspace.projectId, name: 'Existing customers', schema: { id: 'string' } } })))
  const table = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/live-tables`, { data: { name: 'Project customers', profile: 'native_queue', columns: ['id'], limits: { max_rows: 10, max_bytes: 8192 } } })))
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, name: 'Project binding', duration: '1m', groups: [{ name: 'Load', script_id: script, vusers: 1 }], data_pools: [{ pool_id: pool, alias: 'customers', access_mode: 'read_only', selection_strategy: 'sequential' }] } })))
  const open = async () => {
    await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
    await page.reload()
    await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Project binding' }), 'Project binding')
    await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Data/ }).click()
  }
  await open()
  await expect(page.getByRole('combobox', { name: 'Access mode', exact: true })).toHaveValue('read_only')
  await page.getByRole('combobox', { name: 'Access mode', exact: true }).selectOption('live')
  await page.getByRole('combobox', { name: 'Table scope', exact: true }).selectOption('project')
  const binding = page.getByRole('region', { name: 'Project live-table binding' })
  await binding.getByRole('combobox', { name: 'Project live table', exact: true }).selectOption(table)
  await expect(binding.getByLabel('When the run ends', { exact: true })).toHaveCount(0)
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  const saved = mutation(page, 'PUT', `/api/v1/scenarios/${scenario}`)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  const response = await json(await saved)
  expect(response).toMatchObject({ authoring_projection: { scenario: { data_pools: [{ scope: 'project', project_table_id: table, access_mode: 'live' }] } } })
  await open()
  await expect(page.getByRole('combobox', { name: 'Table scope', exact: true })).toHaveValue('project')
  await expect(binding.getByRole('combobox', { name: 'Project live table', exact: true })).toHaveValue(table)
  const unchangedPool = await json(await request.get(`/api/v1/data-pools/${pool}`))
  expect(unchangedPool).toMatchObject({ data_pool: { id: pool, name: 'Existing customers' } })
})

test('native service declarations, literal JavaScript and distributed rendezvous survive canonical save and reopen', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Native authoring', version: '1.0.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Native producer', type: 'native_javascript', params: { source: 'return {status: "ready"};', inputs: {}, result_prefix: 'produced', timeout_ms: 5000 } }, { name: 'Coordinate checkout', type: 'rendezvous', params: { scope: 'local' } }] } } })))
  const open = async () => { await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`); await page.reload() }
  await open()
  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  await workbench.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  const services = page.getByRole('region', { name: 'Native script services' })
  await services.getByRole('button', { name: 'Add live-table declaration', exact: true }).click()
  await services.getByLabel('Alias', { exact: true }).fill('work')
  await services.getByLabel('push', { exact: true }).check()
  await services.getByText('Native Java examples', { exact: true }).click()
  await services.getByText('Conditional update', { exact: true }).click()
  await expect(services.getByLabel('Java conditional update example', { exact: true })).toHaveValue(/table\.update\(row\.id\(\), row\.version\(\),/)
  await workbench.getByRole('tab', { name: 'Action editor', exact: true }).click()

  await page.getByRole('navigation', { name: 'Script outline' }).getByRole('treeitem', { name: /Native producer/ }).click()
  const native = page.getByRole('region', { name: 'Native JavaScript settings' })
  const literal = "const result = apex.data.table('work').push({id: input.id});\nreturn {status: result.status, literal: '{{var.literal}}'};"
  await native.getByRole('textbox', { name: 'JavaScript source', exact: true }).fill(literal)
  await native.getByRole('textbox', { name: 'Inputs (JSON object)', exact: true }).fill('{"id":"{{var.id}}"}')
  await page.getByRole('navigation', { name: 'Script outline' }).getByRole('treeitem', { name: /Coordinate checkout/ }).click()
  const rendezvous = page.getByRole('region', { name: 'Rendezvous settings' })
  await rendezvous.getByRole('combobox', { name: 'Scope', exact: true }).selectOption('distributed_group')
  await rendezvous.getByLabel('Barrier key', { exact: true }).fill('checkout-ready')
  await rendezvous.getByRole('combobox', { name: 'Participants', exact: true }).selectOption('percent')
  await rendezvous.getByLabel('Participant percentage', { exact: true }).fill('80')
  await rendezvous.getByLabel('Maximum wait (ms)', { exact: true }).fill('25000')
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${scriptId}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await saved)
  const canonical = await json(await request.get(`/api/v1/scripts/${scriptId}`))
  expect(canonical).toMatchObject({ script: { config: { native_services: { live_tables: [{ alias: 'work', operations: ['push'] }] } }, actions: [{ params: { source: literal, inputs: { id: '{{var.id}}' } } }, { params: { scope: 'distributed_group', key: 'checkout-ready', percent: 80, timeout_ms: 25000, on_timeout: 'fail' } }] } })
  await open()
  await workbench.getByRole('tab', { name: 'Action editor', exact: true }).click()
  await page.getByRole('navigation', { name: 'Script outline' }).getByRole('treeitem', { name: /Native producer/ }).click()
  await expect(native.getByRole('textbox', { name: 'JavaScript source', exact: true })).toHaveValue(literal)
  await page.screenshot({ path: testInfo.outputPath('native-script-authoring.png'), fullPage: true })
  await workbench.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  await expect(services.getByLabel('Alias', { exact: true })).toHaveValue('work')
  await expect(services.getByLabel('push', { exact: true })).toBeChecked()
})

test('a project-only run keeps attachment provenance and links to its table after completion', async ({ page, trackRun }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const table = await json(await request.post(`/api/v1/projects/${workspace.projectId}/live-tables`, { data: { name: 'Shared across runs', profile: 'native_queue', columns: ['id'], limits: { max_rows: 10, max_bytes: 8192 } } })) as unknown as ProjectLiveTableView
  const script = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Project table reader', version: '1.0.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1', pacing: { type: 'fixed_interval', interval_ms: 500 } }, init: [{ name: 'Publish work', type: 'live_table', params: { alias: 'work', operation: 'push', values: { id: 'project-private-row' } } }], actions: [{ name: 'Read work', type: 'live_table', params: { alias: 'work', operation: 'read', position: 1, result_prefix: 'read_work' } }] } } })))
  const run = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Project table provenance', duration: '2m', groups: [{ name: 'Worker', script_id: script, vusers: 1 }], data_pools: [{ alias: 'work', access_mode: 'live', scope: 'project', project_table_id: table.id, selection_strategy: 'manual' }] } })))
  trackRun(run)
  await json(await request.post(`/api/v1/tests/${run}/start`))
  await page.goto(`/#/test/${run}?tab=overview&${scopeQuery(workspace)}`)
  await page.reload()
  const panel = page.getByRole('region', { name: 'Live data tables', exact: true })
  const refs = panel.getByRole('region', { name: 'Project table references' })
  await expect(refs).toContainText(table.instance_id)
  await expect(refs).toContainText('global row counts and operations are not attributed to this run')
  await expect(panel).not.toContainText('No live tables yet')
  await expect(panel).not.toContainText('project-private-row')
  await expect(panel.getByRole('button', { name: 'Preview up to 100 rows' })).toHaveCount(0)
  await expect.poll(async () => {
    const current = await json(await request.get(`/api/v1/projects/${workspace.projectId}/live-tables/${table.id}`)) as unknown as ProjectLiveTableView
    return current.stats?.counts?.available
  }).toBe(1)
  await page.screenshot({ path: testInfo.outputPath('project-table-run-reference.png'), fullPage: true })
  await refs.getByRole('link', { name: 'work · Worker', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Project live table details' }).getByRole('heading', { name: 'Shared across runs' })).toBeVisible()
  await json(await request.post(`/api/v1/tests/${run}/abort`))
  await expect.poll(async () => (await json(await request.get(`/api/v1/tests/${run}`))).status).toBe('ABORTED')
  await page.goto(`/#/report/${run}?section=execution&${scopeQuery(workspace)}`)
  await expect(page.getByRole('region', { name: 'Project table references' })).toContainText('detached')
  await expect(page.getByRole('region', { name: 'Project table references' })).toContainText(table.instance_id)
  const current = await json(await request.get(`/api/v1/projects/${workspace.projectId}/live-tables/${table.id}`)) as unknown as ProjectLiveTableView
  expect(current.state).toBe('active')
  expect(current.stats?.counts?.available).toBe(1)
})
