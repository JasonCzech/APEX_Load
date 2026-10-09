import { test, expect, seedScript, seedScenario, login, json, idOf, signOut } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Queued-run creation updates these read-context fields without editing the scenario.
// Keep every other field, including authored clocks, in the source-fidelity oracle.
function scenarioAuthoringProjection(snapshot: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(snapshot).filter(([key]) =>
    key !== 'associated_releases' && key !== 'last_run_test_id' && key !== 'last_run_at'))
}

for (const topology of ['missing', 'single-region', 'multi-zone', 'multi-region'] as const) {
  test(`Scenario builder gates weighted placement for ${topology} infrastructure`, async ({ page }, testInfo) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    await seedScript(page.context().request, workspace, 'Placement_HTTP')
    await page.route('**/api/v1/system/inventory*', async route => {
      const response = await route.fetch()
      const body = await response.json()
      body.generators = topology === 'missing' ? [] : ['a', 'b'].map(suffix => ({
        target: `gen-${suffix}:9090`, generator_id: `gen-${suffix}`, status: 'connected',
        region: topology === 'multi-region' ? `region-${suffix}` : 'east',
        labels: { load_zone: topology === 'single-region' ? 'zone-a' : `zone-${suffix}` },
        available_capacity: 100, active_vusers: 0, cpu_percent: 0, memory_bytes: 0,
        goroutine_count: 0, cpu_limit_cores: 2, memory_limit_bytes: 1073741824, max_vusers: 100,
      }))
      await route.fulfill({ response, json: body })
    })
    await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
    await page.reload()
    await page.getByPlaceholder('Scenario name').fill('Topology placement draft')
    await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
    await page.getByRole('row').filter({ hasText: 'Placement_HTTP' }).getByRole('button', { name: 'Use for Group 1' }).click()
    await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run Settings/ }).click()
    await page.getByRole('tab', { name: 'Controller/Generator', exact: true }).click()
    const placement = page.locator('.scenario-root-placement-editor')
    await expect(placement.getByRole('combobox', { name: 'Generator sharing' })).toHaveValue('shared')
    const region = placement.getByRole('radio', { name: 'Region-weighted placement', exact: true })
    const zone = placement.getByRole('radio', { name: 'Load-zone-weighted placement', exact: true })
    if (topology === 'multi-region') {
      await expect(region).toBeEnabled()
      await region.check()
      await expect(placement.getByLabel('Region region-a weight')).toHaveValue('1')
      await expect(placement.getByLabel('Region region-b weight')).toHaveValue('1')
    } else {
      await expect(region).toBeDisabled()
      await expect(region).toHaveAccessibleDescription('Requires generators in at least two configured regions.')
    }
    if (topology === 'multi-region') {
      await expect(zone).toBeEnabled()
      await zone.check()
      await expect(placement.getByLabel('Load zone region-a weight')).toHaveValue('1')
      await expect(placement.getByLabel('Load zone region-b weight')).toHaveValue('1')
    } else {
      await expect(zone).toBeDisabled()
      await expect(zone).toHaveAccessibleDescription('Requires generators in at least two configured load zones.')
      // Bypass Playwright's enabled check to verify native disabled controls
      // remain unchanged even when their labels receive a pointer click.
      await region.locator('..').click({ force: true })
      await zone.locator('..').click({ force: true })
      await expect(placement.getByRole('radio', { name: 'Automatic placement', exact: true })).toBeChecked()
    }
    await page.setViewportSize({ width: 850, height: 1000 })
    await placement.screenshot({ path: testInfo.outputPath(`weighted-placement-${topology}.png`) })
  })
}

test('Scenario builder preserves controller continuity and the recovery spare budget', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await seedScript(request, workspace, 'Continuity_HTTP')
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.reload()
  const steps = page.getByLabel('Scenario builder steps')
  await page.getByPlaceholder('Scenario name').fill('Continuity scenario')
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Continuity_HTTP' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await page.getByRole('tab', { name: 'Controller/Generator', exact: true }).click()
  const continuity = page.getByRole('checkbox', { name: 'Continue through controller failover' })
  const lossTolerance = page.getByRole('checkbox', { name: 'Tolerate generator loss' })
  await continuity.check()
  await lossTolerance.check()
  const spareBudget = page.getByRole('spinbutton', { name: 'Recovery spare VUser budget' })
  await spareBudget.fill('200')
  await expect(spareBudget).toHaveValue('200')
  await expect(continuity).toBeChecked()
  await expect(lossTolerance).toBeChecked()
  await page.setViewportSize({ width: 850, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath('controller-continuity.png'), fullPage: true })
  await steps.getByRole('button', { name: /^Data/ }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await expect(continuity).toBeChecked()
  await expect(spareBudget).toHaveValue('200')
  await steps.getByRole('button', { name: /^Review/ }).click()
  const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  const response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  expect(response.request().postDataJSON().assignment).toMatchObject({ failover_policy: 'continue', tolerate_generator_loss: true, recovery_spare_vusers: 200 })
})

test('New Test saves drafts without execution, retains both tabs and validates recurring triggers', async ({ page }) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const scenarioId = await seedScenario(request, workspace, scriptId, 'Duplicate workload')
  await seedScenario(request, workspace, scriptId, 'Duplicate workload')
  const mutations: string[] = []
  page.on('request', request => {
    const path = new URL(request.url()).pathname
    const testMutation = path === '/api/v1/tests' || /^\/api\/v1\/tests\/[^/]+\//.test(path)
    const scheduleMutation = /^\/api\/v1\/scenarios\/[^/]+\/schedules$/.test(path)
    if (request.method() !== 'GET' && (testMutation || scheduleMutation)) mutations.push(request.url())
  })
  const route = `/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenarioId}`
  await page.goto(route)
  await page.reload()
  await expect(page.getByRole('region', { name: 'Selected scenario', exact: true })).toContainText('Duplicate workload')
  await page.getByRole('textbox', { name: 'Run Name', exact: true }).fill('Saved draft only')
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Unsaved changes' })).not.toBeVisible()
  const scenarioSelect = page.getByRole('combobox', { name: 'Scenario', exact: true })
  const options = await scenarioSelect.locator('option').allTextContents()
  const duplicates = options.filter(option => option.includes('Duplicate workload'))
  expect(duplicates).toHaveLength(2)
  expect(new Set(duplicates).size).toBe(2)
  await page.getByRole('textbox', { name: 'Schedule Name', exact: true }).fill('Draft trigger')
  await page.getByText('Advanced cron expression', { exact: true }).click()
  await page.getByRole('textbox', { name: 'Raw cron expression', exact: true }).fill('invalid cron')
  await expect(page.getByRole('button', { name: 'Create schedule', exact: true })).toBeDisabled()
  await expect(page.locator('#new-test-cron-error')).toBeVisible()
  await page.getByRole('button', { name: 'Run Setup', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Run Name', exact: true })).toHaveValue('Saved draft only')
  await page.getByRole('navigation').getByRole('button', { name: 'Dashboard', exact: true }).click()
  const guard = page.getByRole('dialog', { name: 'Unsaved changes' })
  await expect(guard).toBeVisible()
  await guard.getByRole('button', { name: 'Save changes', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible()
  expect(mutations).toEqual([])
  await page.getByRole('navigation').getByRole('button', { name: 'New Test', exact: true }).click()
  await page.getByRole('button', { name: 'Restore saved draft', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Run Name', exact: true })).toHaveValue('Saved draft only')
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Schedule Name', exact: true })).toHaveValue('Draft trigger')
  await page.getByText('Advanced cron expression', { exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Raw cron expression', exact: true })).toHaveValue('invalid cron')
  await page.getByRole('navigation').getByRole('button', { name: 'Dashboard', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible()
  await expect(guard).not.toBeVisible()
  expect(mutations).toEqual([])
})

test('New Test restores the default name after clearing and reselecting the same scenario', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const scenarioId = await seedScenario(request, workspace, scriptId, 'Reselected workload')
  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenarioId}`)
  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Run Name', exact: true })).toHaveValue('Reselected workload')
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await page.getByRole('combobox', { name: 'Scenario', exact: true }).selectOption('')
  await page.getByRole('button', { name: 'Run Setup', exact: true }).click()
  await page.locator('.new-test-scenario-table-row').filter({ hasText: 'Reselected workload' }).click()
  await expect(page.getByRole('textbox', { name: 'Run Name', exact: true })).toHaveValue('Reselected workload')
})

test('New Test exposes editable groups and saves a paused recurring schedule with visible policies', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const scenarioId = await seedScenario(request, workspace, scriptId, 'Review workload')
  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenarioId}`)
  await page.reload()
  await page.getByRole('button', { name: 'Show selected scenario', exact: true }).click()
  await page.getByRole('button', { name: 'Edit settings for orders', exact: true }).click()
  const editor = page.getByRole('dialog', { name: 'Edit orders', exact: true })
  await expect(editor).toBeVisible()
  await expect(editor.getByText('Browser Runtime', { exact: true })).not.toBeVisible()
  await expect(editor.getByRole('button', { name: 'Refresh host inventory' })).toBeVisible()
  await editor.getByRole('button', { name: 'Close', exact: true }).click()
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Effective schedule policies' })).toContainText('system scheduling defaults')
  await page.getByRole('textbox', { name: 'Schedule Name', exact: true }).fill('Paused review trigger')
  await page.getByRole('checkbox', { name: /^Enabled/ }).uncheck()
  await page.getByRole('combobox', { name: 'Release Context', exact: true }).selectOption({ index: 1 })
  await expect(page.getByRole('button', { name: 'Create schedule', exact: true })).toBeEnabled()
  const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenarioId}/schedules`)
  await page.getByRole('button', { name: 'Create schedule', exact: true }).click()
  expect((await saved).ok()).toBeTruthy()
  const catalog = page.getByRole('region', { name: 'All scenario schedules' })
  await expect(catalog.getByRole('row').filter({ hasText: 'Paused review trigger' })).toContainText('Paused')
  const stored = await request.get(`/api/v1/scenarios/${scenarioId}/schedules`)
  expect((await stored.json()).schedules.find((schedule: { name: string }) => schedule.name === 'Paused review trigger').enabled).toBe(false)
  await page.setViewportSize({ width: 850, height: 1000 })
  await expect(page.getByRole('textbox', { name: 'Schedule Name', exact: true })).toBeVisible()
  const widths = await page.locator('.new-test-schedule-fields > .setting-row').evaluateAll(rows => rows.map(row => row.getBoundingClientRect().width))
  expect(Math.max(...widths) - Math.min(...widths)).toBeLessThan(2)
  await page.screenshot({ path: testInfo.outputPath('new-test-schedules-narrow.png'), fullPage: true })
})

// The fixture advertises a remote backend; this is UI contract verification,
// not an AKS runtime or workload-qualification test. No workload is launched.
for (const facadeLedger of [false, true]) {
test(`Java group profile survives placement edits and scenario persistence (facade ledger: ${facadeLedger})`, async ({ page }, testInfo) => {
  test.setTimeout(90000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Java_Profile_UI')
  await page.route('**/api/v1/scripts**', async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    const projectFixture = (value: unknown): unknown => {
      if (Array.isArray(value)) return value.map(projectFixture)
      if (!value || typeof value !== 'object') return value
      const item = value as Record<string, unknown>
      return Object.fromEntries(Object.entries(item).map(([key, child]) => [key, key === 'protocol' && item.id === scriptId ? 'java_vuser' : projectFixture(child)]))
    }
    await route.fulfill({ response, json: projectFixture(payload) })
  })
  await page.route('**/api/v1/system/inventory*', async route => {
    const response = await route.fetch()
    const body = await response.json()
    body.generators = [{ target: 'java-generator:9090', generator_id: 'java-generator', status: 'connected', drain_state: 'active', compatibility_state: 'compatible', available_capacity: 100, active_vusers: 0, cpu_percent: 0, memory_bytes: 0, goroutine_count: 0, cpu_limit_cores: 2, memory_limit_bytes: 1073741824, max_vusers: 100, supported_protocols: [], compatible_features: ['java_host_broker_v1'], java_host: { contract: 'apexload.java-placement/v1', mode: 'dedicated', containment: 'execution_pod', max_hosts: 25, max_vusers: 25, max_memory_bytes: 1073741824, profiles: [{ script_sha256: 'a'.repeat(64), profile_id: 'approved-java', configuration_sha256: 'b'.repeat(64), policy_sha256: 'c'.repeat(64), memory_bytes: 268435456, preparation_ms: 120000, max_run_ms: 3600000, invocation_ms: 30000 }] } }]
    if (facadeLedger) {
      Object.assign(body.generators[0].java_host.profiles[0], { facade_resources_sha256: 'd'.repeat(64), facade_cpu_millicores: 100, facade_memory_bytes: 8388608, facade_network_kbps: 100 })
      const budget = { cpu_cores: 1, memory_mb: 16, network_mbps: 10, vusers: 100, protocols: { java_host_facade: 100 } }
      body.generators[0].pod_capacity = { state: 'ready', model_version: 'peak_capacity_v1', incarnation: 'pod', headroom_pct: 0, schedulable: budget, reserved: { cpu_cores: 0, memory_mb: 0, network_mbps: 0, vusers: 0 }, remaining: budget, reserved_pct: 0, remaining_pct: 100, observed_at_unix_ms: Date.now(), confidence: 'configured', dedicated: false }
    }
    await route.fulfill({ response, json: body })
  })
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  await page.reload()
  const steps = page.getByLabel('Scenario builder steps')
  await page.getByPlaceholder('Scenario name').fill('Java profile draft')
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Java_Profile_UI' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await page.getByRole('tab', { name: 'Controller/Generator', exact: true }).click()
  const profile = page.getByRole('combobox', { name: 'Java execution profile' })
  await profile.selectOption('approved-java')
  await expect(page.getByLabel('Java preparation deadline (seconds)')).toHaveValue('120')
  const group = page.locator('.scenario-group-placement-item').filter({ hasText: 'Java_Profile_UI' })
  await expect(group.getByRole('radio', { name: 'Inherit scenario default' })).toBeChecked()
  await group.getByText('Pin to host(s)', { exact: true }).click()
  await group.getByRole('checkbox').check()
  await expect(group).toContainText(`${facadeLedger ? 2 : 4} host ceiling`)
  await expect(profile).toHaveValue('approved-java')
  await group.getByText('Automatic placement', { exact: true }).click()
  await page.setViewportSize({ width: 850, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath('java-profile-placement.png'), fullPage: true })
  await steps.getByRole('button', { name: /^Data/ }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  await expect(profile).toHaveValue('approved-java')
  await group.getByText('Inherit scenario default', { exact: true }).click()
  await steps.getByRole('button', { name: /^Review/ }).click()
  const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  const response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  expect(response.request().postDataJSON().groups[0].assignment.mode).toBe('inherit')
  expect(response.request().postDataJSON().groups[0].assignment.java_host).toEqual({ profile_id: 'approved-java', preparation_ms: 120000, run_lifetime_ms: 3600000 })
  const stored = await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`)
  expect(stored.ok()).toBeTruthy()
  expect((await stored.json()).scenarios).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'Java profile draft', scripts: expect.arrayContaining([expect.objectContaining({ assignment: expect.objectContaining({ java_host: { profile_id: 'approved-java', preparation_ms: 120000, run_lifetime_ms: 3600000 } }) })]) })]))
})

}

test('Live configuration reports prepared Java reservations without opening raw JSON', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const id = 'java-placement-reporting'
  const profile = { profile_id: 'approved-java', script_sha256: 'a'.repeat(64), configuration_sha256: 'b'.repeat(64), policy_sha256: 'c'.repeat(64), memory_bytes: 268435456, preparation_ms: 120000, max_run_ms: 3600000, invocation_ms: 30000 }
  const record = { id, name: 'Prepared Java run', status: 'RUNNING', project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, created_at: new Date().toISOString(), started_at: new Date().toISOString(), duration: '10m', total_vusers: 2, groups: [] }
  await page.route(`**/api/v1/tests/${id}`, route => route.fulfill({ json: record }))
  await page.route(`**/api/v1/tests/${id}/manifest`, route => route.fulfill({ json: {
    schema_version: 'v1', test_id: id, test_name: record.name, status: 'RUNNING', source: 'live', generated_at: new Date().toISOString(), identity: {}, initial_configuration: {}, current_configuration: {}, scripts: [], runtime_changes: [],
    load_generators: { execution_settings: {}, generator_shards: [{ group: 'buyers', target: 'java-generator:9090', vusers: 2, java_host: { contract: 'apexload.java-placement/v1', mode: 'dedicated', containment: 'execution_pod', profile, preparation_ms: 120000, run_lifetime_ms: 600000, max_hosts: 25, max_vusers: 25, reservation_id: 'reserved-java-hosts', prepared_vusers: 4, admitted_vusers: 2 } }] },
  } }))
  await page.goto(`/#/test/${id}?tab=configuration&${scopeQuery(workspace)}`)
  // Login loads the project catalog before the API fixture creates this scope.
  await page.reload()
  const reservations = page.getByRole('region', { name: 'Java host reservations' })
  await expect(reservations).toBeVisible()
  await expect(reservations).toContainText('approved-java')
  await expect(reservations).toContainText('reserved-java-hosts')
  await expect(reservations).toContainText('4 / 2')
  await page.setViewportSize({ width: 850, height: 1000 })
  await expect(reservations).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('java-reservations.png'), fullPage: true })
})


test('prepared Event Hubs run displays processor admission and starts with the pinned revision', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = 'prepared-eventhubs-resources'
  const claim = { contract: 'eventhubs_processor_reservations_v1', protocol: 'eventhubs', peak_vusers: 2, requested: { processors: 2, workers: 8, max_partitions_per_processor: 4 } }
  const admission = { claim, admitted: claim.requested, current: true }
  const allocation = [{ group: 'processor', target: 'generator-a', vusers: 1, runtime_resource_claim: claim, runtime_resource_admission: admission }]
  await page.route(`**/api/v1/analysis/capabilities?project_id=${workspace.projectId}`, route => route.fulfill({ json: { preparation_enabled: true, monitoring_enabled: false, imports_enabled: false } }))
  await page.route(`**/api/v1/tests/${id}`, route => route.fulfill({ json: { id, name: 'Prepared Event Hubs', status: 'INITIALIZING', project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, duration: '10m', total_vusers: 1, created_at: '2026-10-03T10:00:00Z', groups: [], generator_shards: allocation } }))
  await page.route(`**/api/v1/tests/${id}/manifest`, route => route.fulfill({ json: { schema_version: 'v1', test_id: id, test_name: 'Prepared Event Hubs', status: 'INITIALIZING', source: 'live', identity: {}, initial_configuration: {}, current_configuration: {}, load_generators: { execution_settings: {}, generator_shards: allocation }, runtime_changes: [] } }))
  await page.route(`**/api/v1/tests/${id}/preparation`, route => route.fulfill({ json: { id: 'prep-eventhubs', state: 'ready', revision: 2, execution_revision: 'immutable-execution', expires_at: '2099-10-03T10:30:00Z', allocation } }))
  let started: unknown
  await page.route(`**/api/v1/tests/${id}/start`, route => { started = route.request().postDataJSON(); return route.fulfill({ json: { status: 'starting' } }) })
  await page.goto(`/#/test/${id}?tab=configuration&${scopeQuery(workspace)}`)
  await page.reload()
  const preparation = page.getByRole('region', { name: 'Run preparation' })
  await expect(preparation.getByRole('region', { name: 'Event Hubs processor reservations' })).toBeVisible()
  await expect(preparation).toContainText('Requested processors / workers')
  await expect(preparation).toContainText('Admitted processors / workers')
  await expect(preparation.getByText('2 / 8', { exact: true })).toHaveCount(2)
  await expect(preparation).toContainText('Partition ceiling per processor')
  await expect(preparation.getByText('4', { exact: true })).toBeVisible()
  await page.setViewportSize({ width: 850, height: 1000 })
  await page.screenshot({ path: test.info().outputPath('eventhubs-preparation-resources.png'), fullPage: true })
  await preparation.getByRole('button', { name: 'Start prepared run', exact: true }).click()
  await expect.poll(() => started).toEqual({ preparation_id: 'prep-eventhubs', expected_preparation_revision: 'immutable-execution' })
  await expect(preparation).toContainText('consumed')
})


test('actual held-run serialization and an API-seeded queued copy preserve complete authored intent', async ({ page, target, trackRun }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Queued_Fidelity_HTTP', target.url)
  const runtime = { results: { message_detail: { mode: 'off', include_resources: false } }, websocket: {} }
  const goal = { transaction_response_time_sla_ms: 0.001, workload_shape_config: { closed_vuser: { scope: 'scenario', scenario: { peak_vusers: 4, ramp_up: '10s', sustain: '50s', ramp_down_enabled: false, ramp_down: '0s' } } } }
  const groups = [
    { name: 'orders', script_id: script, vusers: 2, role: 'load', ramp_up: '3s', ramp_down: '4s', start_offset: '1s', initialization: 'at_once', transaction_response_time_sla_ms: 1250, runtime_settings: runtime },
    { name: 'staged', script_id: script, vusers: 2, role: 'load', start_offset: '2s', initialization: 'at_runtime', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } },
  ]
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Queued fidelity scenario', duration: '1m', scenario_mode: 'vuser_driven', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: runtime, groups } })))
  const originalScenario = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenario}`)
  await page.reload()
  await page.getByRole('button', { name: 'Show selected scenario', exact: true }).click()
  await page.getByRole('button', { name: 'Edit settings for orders', exact: true }).click()
  const editor = page.getByRole('dialog', { name: 'Edit orders', exact: true })
  await editor.getByRole('spinbutton', { name: /^VUsers/ }).fill('4')
  await editor.getByRole('button', { name: 'Save For This Run', exact: true }).click()
  await page.getByRole('textbox', { name: 'Run Name', exact: true }).fill('Held fidelity serializer')
  await page.getByLabel('Release', { exact: true }).selectOption(workspace.releaseId)
  const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  // The real free-capacity UI holds a RESERVED run; it does not promise QUEUED.
  await page.getByRole('button', { name: 'Hold for Manual Start', exact: true }).click()
  const response = await created
  expect(response.ok(), await response.text()).toBeTruthy()
  const heldId = idOf(await response.json()); trackRun(heldId)
  const heldIntent = response.request().postDataJSON()
  const held = await json(await request.get(`/api/v1/tests/${heldId}`))
  await testInfo.attach('held-run-authored-intent', { body: JSON.stringify({ held_intent: heldIntent, held }, null, 2), contentType: 'application/json' })
  expect(heldIntent.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  expect(held.status).toBe('RESERVED')
  expect(held.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  expect(held.goal_config).toEqual(originalScenario.goal_config)
  expect(held.runtime_settings).toEqual(originalScenario.runtime_settings)
  expect(target.count()).toBe(0)
  // Seed a distinct canonical QUEUED source from that exact owned authored intent.
  // Only reservation controls change; no status response or admission is mocked.
  const queuedIntent = structuredClone(heldIntent)
  delete queuedIntent.reserve_launch_window
  delete queuedIntent.scheduled_start_at
  queuedIntent.name = 'Queued fidelity source'
  const queuedResponse = await request.post('/api/v1/tests', { data: queuedIntent })
  expect(queuedResponse.ok(), await queuedResponse.text()).toBeTruthy()
  const sourceId = idOf(await queuedResponse.json()); trackRun(sourceId)
  const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  await testInfo.attach('queued-source-authored-intent', { body: JSON.stringify({ queued_seed: queuedIntent, source }, null, 2), contentType: 'application/json' })
  expect(source.status).toBe('QUEUED')
  expect(source.goal_config).toEqual(originalScenario.goal_config)
  expect(source.runtime_settings).toEqual(originalScenario.runtime_settings)
  await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  const copyEditor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await expect(copyEditor).toBeVisible()
  await copyEditor.getByLabel('Scenario Name', { exact: true }).fill('Queued fidelity revised name')
  await page.screenshot({ path: testInfo.outputPath('queued-copy-fidelity.png'), animations: 'disabled' })
  const copied = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  await copyEditor.getByRole('button', { name: /^Create Revised Copy/ }).click()
  const copyResponse = await copied
  expect(copyResponse.ok(), await copyResponse.text()).toBeTruthy()
  const createdCopy = await copyResponse.json()
  const copyId = idOf(createdCopy); trackRun(copyId)
  const sent = copyResponse.request().postDataJSON()
  await testInfo.attach('queued-copy-submitted-response', { body: JSON.stringify({ submitted: sent, created: createdCopy }, null, 2), contentType: 'application/json' })
  expect(copyId).not.toBe(sourceId)
  expect(sent).toMatchObject({ revision_source_test_id: sourceId, name: 'Queued fidelity revised name', project_id: workspace.projectId, goal_config: source.goal_config, runtime_settings: source.runtime_settings })
  // The established compact serializer omits the default load role; canonical reads retain it.
  expect(sent.groups.map((group: Record<string, unknown>) => ({ ...group, role: group.role ?? 'load' }))).toMatchObject([{ ...groups[0], vusers: 4, revision_source_group_index: 0 }, { ...groups[1], revision_source_group_index: 1 }])
  await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  await page.reload()
  const reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  expect(reopened.status).toBe('QUEUED')
  expect(reopened.goal_config).toEqual(source.goal_config)
  expect(reopened.runtime_settings).toEqual(source.runtime_settings)
  expect(reopened.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  const reopenedSource = await json(await request.get(`/api/v1/tests/${sourceId}`))
  const reopenedScenario = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  await testInfo.attach('queued-copy-fidelity', { body: JSON.stringify({ scenario: originalScenario, scenario_reopened: reopenedScenario, held_submitted: heldIntent, held, queued_seed_submitted: queuedIntent, source, source_reopened: reopenedSource, submitted: sent, reopened }, null, 2), contentType: 'application/json' })
  expect(reopenedSource).toEqual(source)
  expect(scenarioAuthoringProjection(reopenedScenario)).toEqual(scenarioAuthoringProjection(originalScenario))
  expect(target.count()).toBe(0)
})

test('queued-copy preserves source capture IDs and durably clears the last authored goal', async ({ page, target, trackRun }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Capture_Clear_HTTP', target.url)
  const elk = await json(await request.post(`/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`, { data: { name: 'Copy source logs', enabled: true, endpoint: 'https://capture.invalid', index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message', auth_type: 'api_key', api_key_secret_name: 'e2e-capture-secret' } }))
  const dt = await json(await request.post(`/api/v1/projects/${workspace.projectId}/monitoring/dynatrace-connections`, { data: { name: 'Copy source SaaS', enabled: true, environment_url: 'https://fidelity.live.dynatrace.com', environment_token_secret: 'e2e-dt-secret', problems_enabled: true, poll_interval_seconds: 60 } }))
  expect(elk.status).toBe('ready')
  expect(dt.legacy_capture_status).toBe('ready')
  const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: { name: 'P95 capture source', project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, duration: '1m', scenario_mode: 'goal_oriented', goal_config: { p95_latency_ms: 1000 }, groups: [{ name: 'orders', script_id: script, vusers: 1 }], elk_capture: { connection_id: idOf(elk) }, dynatrace_capture: { connection_id: idOf(dt) } } })))
  trackRun(sourceId)
  const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  await testInfo.attach('capture-clear-source', { body: JSON.stringify(source, null, 2), contentType: 'application/json' })
  expect(source.status).toBe('QUEUED')
  expect(source.goal_config).toEqual({ p95_latency_ms: 1000 })
  const openCopy = async () => {
    await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
    await page.reload()
    await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
    await expect(dialog.getByRole('combobox', { name: /^Elastic log capture/ })).toHaveValue(idOf(elk))
    await expect(dialog.getByRole('combobox', { name: /^Dynatrace SaaS capture/ })).toHaveValue(idOf(dt))
    await expect(dialog.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ })).toBeEnabled()
    return dialog
  }
  const preservedEditor = await openCopy()
  await preservedEditor.getByLabel('Scenario Name', { exact: true }).fill('Capture rename only')
  const preservedSaved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await preservedEditor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  const preservedResponse = await preservedSaved
  expect(preservedResponse.ok(), await preservedResponse.text()).toBeTruthy()
  const preservedCreated = await preservedResponse.json()
  const preservedId = idOf(preservedCreated); trackRun(preservedId)
  const preservedWire = preservedResponse.request().postDataJSON()
  await testInfo.attach('capture-rename-submission', { body: JSON.stringify({ submitted: preservedWire, created: preservedCreated }, null, 2), contentType: 'application/json' })
  expect(preservedWire).toMatchObject({ elk_capture: { connection_id: idOf(elk) }, dynatrace_capture: { connection_id: idOf(dt) }, goal_config: { p95_latency_ms: 1000 } })
  expect(preservedWire).not.toHaveProperty('sourceGoalConfigPresent')
  expect(preservedWire).not.toHaveProperty('revisionContext')
  await expect(page).toHaveURL(new RegExp(`/command-center/${preservedId}`))
  await page.reload()
  const preserved = await json(await request.get(`/api/v1/tests/${preservedId}`))
  await testInfo.attach('capture-rename-reopened', { body: JSON.stringify(preserved, null, 2), contentType: 'application/json' })
  expect(preserved.elk_capture).toEqual(source.elk_capture)
  expect(preserved.dynatrace_capture).toEqual(source.dynatrace_capture)
  expect(preserved.goal_config).toEqual(source.goal_config)
  expect(preserved.status).toBe('QUEUED')

  const clearedEditor = await openCopy()
  await clearedEditor.getByLabel('Scenario Name', { exact: true }).fill('Explicit goal and capture clear')
  await clearedEditor.getByLabel('P95 Latency Ceiling (ms)', { exact: true }).fill('')
  await clearedEditor.getByRole('button', { name: 'VUser-Driven', exact: true }).click()
  await clearedEditor.getByRole('combobox', { name: /^Elastic log capture/ }).selectOption('')
  await clearedEditor.getByRole('combobox', { name: /^Dynatrace SaaS capture/ }).selectOption('')
  const clearedSaved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await clearedEditor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  const clearedResponse = await clearedSaved
  expect(clearedResponse.ok(), await clearedResponse.text()).toBeTruthy()
  const clearedCreated = await clearedResponse.json()
  const clearedId = idOf(clearedCreated); trackRun(clearedId)
  const clearedWire = clearedResponse.request().postDataJSON()
  await testInfo.attach('capture-goal-clear-submission', { body: JSON.stringify({ submitted: clearedWire, created: clearedCreated }, null, 2), contentType: 'application/json' })
  expect(clearedWire.goal_config).toEqual({})
  expect(clearedWire).not.toHaveProperty('elk_capture')
  expect(clearedWire).not.toHaveProperty('dynatrace_capture')
  await expect(page).toHaveURL(new RegExp(`/command-center/${clearedId}`))
  await page.reload()
  const cleared = await json(await request.get(`/api/v1/tests/${clearedId}`))
  const reopenedSource = await json(await request.get(`/api/v1/tests/${sourceId}`))
  await testInfo.attach('capture-goal-clear-reopened', { body: JSON.stringify({ cleared, source: reopenedSource }, null, 2), contentType: 'application/json' })
  expect(cleared.status).toBe('QUEUED')
  expect(cleared.scenario_mode).toBe('vuser_driven')
  expect(cleared.goal_config).toEqual({})
  expect(cleared).not.toHaveProperty('elk_capture')
  expect(cleared).not.toHaveProperty('dynatrace_capture')
  expect(reopenedSource).toEqual(source)
  expect(target.count()).toBe(0)
})

test('queued-copy preserves iteration completion and explicitly clears source delay and root runtime', async ({ page, target, trackRun }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Completion_Clear_HTTP', target.url)
  const create = async (body: Record<string, unknown>) => {
    const run = await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, ...body } }))
    trackRun(idOf(run))
    return json(await request.get(`/api/v1/tests/${idOf(run)}`))
  }
  const source = await create({ name: 'Iteration source', termination_mode: 'iterations', duration: '', groups: [{ name: 'orders', script_id: script, vusers: 1, iterations: 3 }] })
  await page.goto(`/#/command-center/${idOf(source)}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  let editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await expect(editor).toContainText('All per-user iterations')
  await expect(editor.getByLabel('Steady State Duration', { exact: true })).toBeDisabled()
  await expect(editor.getByLabel('Steady State Duration', { exact: true })).toHaveValue('')
  await editor.getByLabel('Scenario Name', { exact: true }).fill('Iteration renamed copy')
  let saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await editor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  let response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  let created = await response.json()
  let copyId = idOf(created); trackRun(copyId)
  let submitted = response.request().postDataJSON()
  expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', groups: [expect.objectContaining({ iterations: 3 })] })
  for (const absent of ['start_delay', 'runtime_settings', 'goal_config']) expect(submitted).not.toHaveProperty(absent)
  await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  await page.reload()
  let reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  expect(reopened).toMatchObject({ termination_mode: 'iterations', duration: '', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  expect(await json(await request.get(`/api/v1/tests/${idOf(source)}`))).toEqual(source)
  await testInfo.attach('iteration-copy-transport-reopened', { body: JSON.stringify({ source, submitted, created, reopened }, null, 2), contentType: 'application/json' })

  const iterationDelayed = await create({ name: 'Delayed iteration source', termination_mode: 'iterations', duration: '', start_delay: '10s', groups: [{ name: 'orders', script_id: script, vusers: 1, iterations: 3 }] })
  await page.goto(`/#/command-center/${idOf(iterationDelayed)}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await expect(editor.getByLabel('Steady State Duration', { exact: true })).toBeDisabled()
  await expect(editor.getByLabel('Global Start Delay', { exact: true })).toHaveValue('10')
  await editor.getByLabel('Scenario Name', { exact: true }).fill('Positive iteration delay copy')
  saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  await editor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  created = await response.json(); copyId = idOf(created); trackRun(copyId)
  submitted = response.request().postDataJSON()
  expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '10s', groups: [expect.objectContaining({ iterations: 3 })] })
  expect(submitted).not.toHaveProperty('goal_config')
  await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  await page.reload()
  const iterationInherited = await json(await request.get(`/api/v1/tests/${copyId}`))
  expect(iterationInherited).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '10s', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await editor.getByLabel('Scenario Name', { exact: true }).fill('Iteration delay cleared copy')
  await editor.getByLabel('Global Start Delay', { exact: true }).fill('')
  saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  await editor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  created = await response.json(); copyId = idOf(created); trackRun(copyId)
  submitted = response.request().postDataJSON()
  expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '0s', revision_source_test_id: idOf(iterationInherited), groups: [expect.objectContaining({ iterations: 3 })] })
  expect(submitted).not.toHaveProperty('goal_config')
  await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  await page.reload()
  reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  expect(reopened).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '0s', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  expect(await json(await request.get(`/api/v1/tests/${idOf(iterationDelayed)}`))).toEqual(iterationDelayed)
  expect(await json(await request.get(`/api/v1/tests/${idOf(iterationInherited)}`))).toEqual(iterationInherited)
  await testInfo.attach('positive-iteration-delay-copy-clear-reopened', { body: JSON.stringify({ source: iterationDelayed, positive_copy: iterationInherited, cleared_submitted: submitted, created, reopened }, null, 2), contentType: 'application/json' })

  const delaySource = await create({ name: 'Delay and runtime source', duration: '1m', start_delay: '10s', runtime_settings: { log_level: 'standard' }, groups: [{ name: 'orders', script_id: script, vusers: 1 }] })
  await page.goto(`/#/command-center/${idOf(delaySource)}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await editor.getByLabel('Scenario Name', { exact: true }).fill('Delay and root runtime cleared')
  await expect(editor.getByLabel('Global Start Delay', { exact: true })).toHaveValue('10')
  await editor.getByLabel('Global Start Delay', { exact: true }).fill('')
  await editor.getByRole('button', { name: 'Clear Log level override', exact: true }).click()
  saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  await editor.getByRole('button', { name: /^Create Revised Copy \(\d+ groups?\)$/ }).click()
  response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  created = await response.json(); copyId = idOf(created); trackRun(copyId)
  submitted = response.request().postDataJSON()
  expect(submitted.start_delay).toBe('0s')
  expect(submitted.runtime_settings).toEqual({})
  for (const privateField of ['sourceStartDelayPresent', 'sourceRuntimeSettingsPresent', 'revisionContext']) expect(submitted).not.toHaveProperty(privateField)
  await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  await page.reload()
  reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  expect(reopened.start_delay).toBe('0s')
  expect(reopened.runtime_settings).toEqual({})
  expect(await json(await request.get(`/api/v1/tests/${idOf(delaySource)}`))).toEqual(delaySource)
  await testInfo.attach('delay-runtime-clear-transport-reopened', { body: JSON.stringify({ source: delaySource, submitted, created, reopened }, null, 2), contentType: 'application/json' })
  expect(target.count()).toBe(0)
})

test('queued-copy joins a history-guard save to its actual submitted receipt and retains late edits', async ({ page, target, trackRun }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Late_Copy_HTTP', target.url)
  const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Late draft source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  trackRun(sourceId)
  const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  const editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  const name = editor.getByLabel('Scenario Name', { exact: true })
  await name.fill('Submitted snapshot')
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  let publish!: () => void
  const received = new Promise<void>(resolve => { publish = resolve })
  let handlerStarted = false
  let handlerJoined = false
  let handlerError: unknown
  let postCount = 0
  let joinedCount = 0
  let receipt: Record<string, unknown> | undefined
  const pattern = '**/api/v1/tests'
  await page.route(pattern, async route => {
    if (route.request().method() !== 'POST' || route.request().postDataJSON().revision_source_test_id !== sourceId) return route.continue()
    handlerStarted = true
    postCount++
    try {
      const response = await route.fetch()
      expect(response.ok(), await response.text()).toBeTruthy()
      receipt = await response.json()
      trackRun(idOf(receipt!))
      await testInfo.attach('actual-submitted-late-draft-receipt', { body: JSON.stringify({ submitted: route.request().postDataJSON(), receipt }, null, 2), contentType: 'application/json' })
      publish()
      await held
      await route.fulfill({ response })
    } catch (error) { handlerError = error; publish(); await route.abort().catch(() => undefined) }
    finally { joinedCount++; handlerJoined = joinedCount === postCount }
  })
  try {
    await editor.getByRole('button', { name: /^Create Revised Copy/ }).click()
    await expect.poll(() => Boolean(receipt) || Boolean(handlerError)).toBe(true)
    await received
    if (handlerError) throw handlerError
    await name.fill('Later unsaved snapshot')
    // A real hash/history route change reaches the app guard even while the
    // underlying page is inert. Save must join the already accepted POST.
    await page.evaluate(() => { window.location.hash = '#/dashboard' })
    const pendingGuard = page.getByRole('dialog', { name: 'Unsaved Changes', exact: true })
    await expect(pendingGuard).toBeVisible()
    await pendingGuard.getByRole('button', { name: 'Save changes', exact: true }).click()
    await expect(pendingGuard.getByRole('button', { name: 'Saving…', exact: true })).toBeDisabled()
    const response = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
    release()
    await response
    await expect(editor).toContainText('Edits made while saving remain unsaved')
    await expect(name).toHaveValue('Later unsaved snapshot')
    await expect(page).toHaveURL(new RegExp(`/command-center/${sourceId}`))
    await expect(pendingGuard).toContainText('Save did not complete')
    await pendingGuard.getByRole('button', { name: 'Keep editing', exact: true }).click()
    expect(postCount).toBe(1)
    await expect.poll(() => joinedCount).toBe(1)
    const actualCreated = await json(await request.get(`/api/v1/tests/${idOf(receipt!)}`))
    expect(actualCreated.name).toBe('Submitted snapshot')
    await testInfo.attach('history-joined-single-flight-reopened', { body: JSON.stringify({ post_count: postCount, joined_count: joinedCount, receipt, reopened: actualCreated }, null, 2), contentType: 'application/json' })
    await editor.getByRole('button', { name: 'Cancel', exact: true }).click()
    const guard = page.getByRole('dialog', { name: 'Unsaved Changes', exact: true })
    await expect(guard).toBeVisible()
    await guard.getByRole('button', { name: 'Keep editing', exact: true }).click()
    await expect(name).toHaveValue('Later unsaved snapshot')
    expect(await json(await request.get(`/api/v1/tests/${sourceId}`))).toEqual(source)
    expect(target.count()).toBe(0)
  } finally {
    release()
    await page.unroute(pattern)
    if (handlerStarted) await expect.poll(() => handlerJoined, { timeout: 5000, message: 'Owned deferred response must join' }).toBe(true)
  }
})

test('queued-copy submit refuses a source started concurrently and viewers cannot open the action', async ({ page, target, trackRun }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Queued_Ownership_HTTP', target.url)
  const source = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Queued ownership source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  trackRun(source)
  await page.goto(`/#/command-center/${source}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  const copyEditor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  await expect(copyEditor).toBeVisible()
  await copyEditor.getByLabel('Scenario Name', { exact: true }).fill('Must refuse started source')
  const pattern = '**/api/v1/tests'
  let started = false
  await page.route(pattern, async route => {
    if (route.request().method() !== 'POST' || route.request().postDataJSON().revision_source_test_id !== source) return route.continue()
    const start = await request.post(`/api/v1/tests/${source}/start`)
    expect(start.ok(), await start.text()).toBeTruthy()
    started = true
    await route.continue()
  })
  try {
    const submitted = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests' && response.request().postDataJSON().revision_source_test_id === source)
    await copyEditor.getByRole('button', { name: /^Create Revised Copy/ }).click()
    const response = await submitted
    expect(started).toBe(true)
    expect(response.ok()).toBe(false)
    expect(await response.text()).toMatch(/queued|started/i)
    await expect.poll(() => target.count()).toBeGreaterThan(0)
    await page.reload()
    await expect(page.getByRole('button', { name: 'Create revised queued copy', exact: true })).toHaveCount(0)
    await testInfo.attach('started-source-refusal', { body: JSON.stringify({ source, status: response.status(), submitted: response.request().postDataJSON() }, null, 2), contentType: 'application/json' })
  } finally { await page.unroute(pattern) }
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
    const viewerSource = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Viewer queued source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
    trackRun(viewerSource)
    const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
    const viewer = users.find(user => user.subject === 'local:user')!
    await json(await request.post(`/api/v1/auth/users/${viewer.id}/bindings`, { data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' } }))
    await signOut(page)
    await login(page, 'user')
    try {
      await page.goto(`/#/command-center/${viewerSource}?${scopeQuery(workspace)}`)
      await page.reload()
      await expect(page.getByRole('region', { name: 'Command center run controls' })).toContainText('Viewer queued source')
      await expect(page.getByRole('button', { name: 'Create revised queued copy', exact: true })).toHaveCount(0)
      await expect(page.getByRole('list', { name: 'Configured group runtime settings', exact: true })).toHaveCount(0)
      expect((await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, revision_source_test_id: viewerSource, name: 'Forbidden viewer copy', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1, revision_source_group_index: 0 }] } })).status()).toBe(403)
    } finally {
      await signOut(page)
      await login(page)
    }
  }
})


test('queued group dialogs retain inherited Results siblings and WebSocket zero and explicit clear', async ({ page, target, trackRun }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const results = { csv_exports: [{ id: 'calls', file_name: 'calls.csv', action_name: 'orders', columns: [{ name: 'latency', metric: 'latency_ms' }] }], property_analytics: [{ name: 'category', extraction: 'category', values: ['one'] }] }
  const http = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Inherited_Fidelity_HTTP', version: '1', protocol: 'http', config: { base_url: target.url, results }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: '/orders' }] } } })))
  const websocket = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Inherited_Fidelity_WS', version: '1', protocol: 'websocket', config: { base_url: 'ws://127.0.0.1:1', websocket: { callback_interval_ms: 250, read_timeout_ms: 200, compression: true } }, actions: [{ name: 'Connect', type: 'ws_connect', target: 'ws://127.0.0.1:1' }] } } })))
  const originalHTTP = await json(await request.get(`/api/v1/scripts/${http}`))
  const originalWS = await json(await request.get(`/api/v1/scripts/${websocket}`))
  const run = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Queued dialog fidelity', duration: '1m', groups: [{ name: 'orders', script_id: http, vusers: 1 }, { name: 'WS', script_id: websocket, vusers: 1, runtime_settings: { websocket: { callback_interval_ms: 500, read_timeout_ms: 100, compression: true } } }] } })))
  trackRun(run)
  await page.goto(`/#/command-center/${run}?${scopeQuery(workspace)}`)
  await page.reload()
  const open = async (group: string) => {
    await page.getByRole('button', { name: `${group} runtime settings`, exact: true }).click()
    const dialog = page.getByRole('dialog', { name: `Runtime Settings — ${group}`, exact: true })
    await expect(dialog.getByRole('button', { name: 'Save to run', exact: true })).toBeVisible()
    return dialog
  }
  const httpDialog = await open('orders')
  await httpDialog.getByRole('tab', { name: 'Error Handling', exact: true }).click()
  await httpDialog.getByRole('combobox', { name: /^Exchange capture/ }).selectOption('off')
  await httpDialog.getByRole('combobox', { name: /^Include fetched resources/ }).selectOption('false')
  await httpDialog.getByLabel('Retained exchange limit', { exact: true }).fill('0')
  const httpSaved = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/orders/runtime-settings`)
  await httpDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  const httpResponse = await httpSaved
  expect(httpResponse.ok(), await httpResponse.text()).toBeTruthy()
  expect(httpResponse.request().postDataJSON().runtime_settings.results).toEqual({ ...results, message_detail: { mode: 'off', include_resources: false, max_items: 0 } })
  await expect(httpDialog).toBeHidden()
  const httpView = await json(await request.get(`/api/v1/tests/${run}/groups/orders/runtime-settings`))
  expect(httpView.runtime_settings).toMatchObject({ results: { ...results, message_detail: { mode: 'off', include_resources: false } } })
  const wsDialog = await open('WS')
  const callback = wsDialog.getByLabel('WebSocket callback interval (ms)', { exact: true })
  await expect(callback).toHaveValue('500')
  await callback.fill('0')
  const wsSaved = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/WS/runtime-settings`)
  await wsDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  const wsResponse = await wsSaved
  expect(wsResponse.ok(), await wsResponse.text()).toBeTruthy()
  expect(wsResponse.request().postDataJSON().runtime_settings.websocket).toEqual({ callback_interval_ms: 0, read_timeout_ms: 100, compression: true })
  await expect(wsDialog).toBeHidden()
  const clearDialog = await open('WS')
  await clearDialog.getByRole('button', { name: 'Clear WebSocket override', exact: true }).click()
  const cleared = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/WS/runtime-settings`)
  await clearDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  const clearResponse = await cleared
  expect(clearResponse.ok(), await clearResponse.text()).toBeTruthy()
  expect(clearResponse.request().postDataJSON().fields).toContain('websocket')
  expect(clearResponse.request().postDataJSON().runtime_settings).not.toHaveProperty('websocket')
  await expect(clearDialog).toBeHidden()
  await page.reload()
  const inherited = await open('WS')
  await expect(inherited.getByLabel('WebSocket callback interval (ms)', { exact: true })).toHaveAttribute('placeholder', '250')
  await expect(inherited.getByLabel('WebSocket callback interval (ms)', { exact: true })).toHaveValue('')
  expect(await json(await request.get(`/api/v1/scripts/${http}`))).toEqual(originalHTTP)
  expect(await json(await request.get(`/api/v1/scripts/${websocket}`))).toEqual(originalWS)
  expect(target.count()).toBe(0)
  await testInfo.attach('group-dialog-fidelity', { body: JSON.stringify({ http: httpView, submittedWS: wsResponse.request().postDataJSON(), clear: clearResponse.request().postDataJSON() }, null, 2), contentType: 'application/json' })
})
