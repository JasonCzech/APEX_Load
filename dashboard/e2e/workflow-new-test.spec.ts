import { test, expect, seedScript, seedScenario, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

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
  await expect(page.getByRole('button', { name: 'Save Schedule', exact: true })).toBeDisabled()
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
  await expect(page.getByRole('button', { name: 'Save Schedule', exact: true })).toBeEnabled()
  const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenarioId}/schedules`)
  await page.getByRole('button', { name: 'Save Schedule', exact: true }).click()
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
