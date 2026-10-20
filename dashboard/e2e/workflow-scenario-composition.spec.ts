import { test, expect, seedScript, login, json, idOf, editScenarioFromRow, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { storedZip } from './support/zip'
import { canonicalImportDraft } from './support/importResponse'

test('scenario composition combines group editing with a live preview', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await seedScript(page.context().request, workspace, 'Composition_HTTP')
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByPlaceholder('Scenario name').fill('Composition preview')
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Composition_HTTP' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await page.getByRole('button', { name: /Add group/ }).click()
  await page.getByRole('button', { name: 'Add script to Group 2' }).click()
  await page.getByRole('row').filter({ hasText: 'Composition_HTTP' }).getByRole('button', { name: 'Use for Group 2' }).click()
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run settings/ }).click()

  const control = page.getByRole('region', { name: 'Scenario composition timeline' })
  const preview = page.locator('.scenario-builder-preview .scenario-model-preview-panel')
  const sections = page.getByRole('tablist', { name: 'Run settings sections' })
  await expect(page.getByRole('combobox', { name: 'Scenario Mode' })).toHaveCount(0)
  await expect(sections.getByRole('tab')).toHaveText(['Target load', 'Ramp pattern', 'Run phases', 'Advanced settings', 'Browser/script runtime', 'Controller/generator'])
  const tabTops = await sections.getByRole('tab').evaluateAll(tabs => tabs.map(tab => Math.round(tab.getBoundingClientRect().top)))
  expect(new Set(tabTops).size).toBe(1)
  await expect(control.getByText('Group load & timeline')).toBeVisible()
  await expect(control.locator('.composition-track')).toHaveCount(2)
  await expect(control.locator('.composition-group-editor')).toBeVisible()
  await expect(preview.getByRole('heading', { name: 'Live scenario preview' })).toBeVisible()
  await expect(preview.getByLabel('Live group summary').locator('.scenario-composition-summary-row')).toHaveCount(2)

  await sections.getByRole('tab', { name: 'Ramp pattern' }).click()
  await expect(page.getByRole('button', { name: 'Enable stages' })).toBeVisible()
  const stageGroup = page.getByRole('combobox', { name: 'Load group for advanced stages' })
  await expect(stageGroup).toBeVisible()
  await stageGroup.selectOption('0')
  await expect(stageGroup).toHaveValue('0')
  await stageGroup.selectOption('1')
  await expect(control).toHaveCount(0)
  await sections.getByRole('tab', { name: 'Run phases' }).click()
  await expect(page.getByLabel('Duration', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Start delay', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Ramp up', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Ramp down', { exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Load group for run phases' })).toHaveValue('1')
  await sections.getByRole('tab', { name: 'Advanced settings' }).click()
  await expect(page.getByRole('heading', { name: 'Protect the run' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Pre-run assurance' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Transaction response-time SLA' })).toBeVisible()
  await expect(page.getByText('Advanced run protection and assurance')).toHaveCount(0)
  await sections.getByRole('tab', { name: 'Target load' }).click()

  await control.locator('.composition-track-label').nth(1).click()
  await expect(control.locator('.composition-track-label').nth(1)).toHaveAttribute('aria-pressed', 'true')
  await control.locator('.scenario-vuser-field input[type="number"]').fill('25')
  await expect(preview.getByLabel('Live group summary').locator('.scenario-composition-summary-row').nth(1)).toContainText('25 VUsers')
  const offsetSlider = control.locator('.composition-track-slider').nth(1)
  await offsetSlider.focus()
  await offsetSlider.press('ArrowRight')
  await expect(preview.getByLabel('Live group summary').locator('.scenario-composition-summary-row').nth(1)).toContainText('1s start')

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath('scenario-composition-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 600, height: 900 })
  await expect(control).toBeVisible()
  await expect(preview).toBeVisible()
  const narrowTabTops = await sections.getByRole('tab').evaluateAll(tabs => tabs.map(tab => Math.round(tab.getBoundingClientRect().top)))
  expect(new Set(narrowTabTops).size).toBe(1)
  expect(await control.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
  await page.screenshot({ path: testInfo.outputPath('scenario-composition-narrow.png'), fullPage: true })
})

test('guided stepped ramp keeps its pattern editor within the four scenario tabs', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await seedScript(page.context().request, workspace, 'Stepped_HTTP')
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByPlaceholder('Scenario name').fill('Stepped tab layout')
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Stepped_HTTP' }).getByRole('button', { name: 'Use for Group 1' }).click()
  const steps = page.getByLabel('Scenario builder steps')
  await steps.getByRole('button', { name: /^Model & shape/ }).click()
  await page.getByRole('button', { name: /^Stepped Ramp/ }).click()
  await page.getByRole('button', { name: 'Use Stepped Ramp', exact: true }).click()
  await steps.getByRole('button', { name: /^Run settings/ }).click()

  const tabs = page.getByRole('tablist', { name: 'Run settings sections' })
  await expect(tabs.getByRole('tab')).toHaveText(['Target load', 'Ramp pattern', 'Run phases', 'Advanced settings', 'Browser/script runtime', 'Controller/generator'])
  await expect(page.getByLabel('Ending load level')).toBeVisible()
  await tabs.getByRole('tab', { name: 'Ramp pattern' }).click()
  await expect(page.getByRole('heading', { name: 'Create equal steps' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Apply the model' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Run phases' }).click()
  await expect(page.getByLabel('Duration', { exact: true })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Advanced settings' }).click()
  await expect(page.getByRole('heading', { name: 'Protect the run' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Transaction response-time SLA' })).toBeVisible()
  await expect(page.getByText('Advanced run protection and assurance')).toHaveCount(0)
})


test('scenario rename and reopen retain Results, WebSocket, named shape and group timing intent', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Fidelity_HTTP')
  const results = { csv_exports: [{ id: 'calls', file_name: 'calls.csv', action_name: 'orders', columns: [{ name: 'latency', metric: 'latency_ms' }] }], property_analytics: [{ name: 'category', extraction: 'category', values: ['one', 'two'] }] }
  const rootRuntime = { results: { message_detail: { mode: 'off', include_resources: false, sample_rate: 0, max_items: 0 } }, websocket: { callback_interval_ms: 0, compression: false, reconnect: false } }
  const goal = { transaction_response_time_sla_ms: 0.001, workload_shape_config: { closed_vuser: { scope: 'scenario', scenario: { peak_vusers: 4, ramp_up: '10s', sustain: '50s', ramp_down_enabled: false, ramp_down: '0s' } } } }
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Fidelity original', duration: '1m', scenario_mode: 'vuser_driven', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: rootRuntime,
    groups: [
      { name: 'orders', script_id: script, vusers: 2, role: 'load', ramp_up: '3s', ramp_down: '4s', start_offset: '1s', initialization: 'at_once', transaction_response_time_sla_ms: 1250, runtime_settings: { results, websocket: {} } },
      { name: 'staged', script_id: script, vusers: 2, role: 'load', start_offset: '2s', initialization: 'at_runtime', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } },
    ],
  } })))
  const original = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Fidelity original' }), 'Fidelity original')
  await page.getByPlaceholder('Scenario name').fill('Fidelity renamed only')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  const response = await saved
  expect(response.ok(), await response.text()).toBeTruthy()
  const sent = response.request().postDataJSON()
  expect(sent).toMatchObject({ name: 'Fidelity renamed only', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: { results: { message_detail: { mode: 'off', include_resources: false } }, websocket: {} } })
  expect(sent.groups).toMatchObject([{ start_offset: '1s', ramp_up: '3s', ramp_down: '4s', transaction_response_time_sla_ms: 1250, runtime_settings: { results, websocket: {} } }, { start_offset: '2s', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } }])
  const stored = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  expect(stored.runtime_settings).toEqual(original.runtime_settings)
  expect(stored.scripts).toEqual(original.scripts)
  expect(stored.goal_config).toEqual(original.goal_config)
  expect(stored.workload_shape_id).toBe('closed-vuser')
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Fidelity renamed only' }), 'Fidelity renamed only')
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Fidelity renamed only')
  await testInfo.attach('canonical-scenario-fidelity', { body: JSON.stringify({ original, submitted: sent, reopened: stored }, null, 2), contentType: 'application/json' })
})


test('changing a saved workload shape retains the authored root SLA and rules', async ({ page, target }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, 'Shape_Guardrails_HTTP', target.url)
  const goal = { target_average_tps: 25, min_vusers: 1, max_vusers: 30, p95_latency_ms: 2300, max_error_pct: 0, max_error_count: 3, max_avg_server_processing_ms: 400, max_avg_transfer_ms: 125, max_lcp_ms: 2500, max_inp_ms: 200, max_cls: 0.1, max_throttled_requests: 0, max_end_to_end_latency_ms: 500, max_partition_skew_pct: 0, max_redelivery_rate_pct: 0, max_dead_letter_rate_pct: 0, stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '7s', transaction_response_time_sla_ms: 0.001, transaction_rules: [{ transaction: 'orders', metric: 'p95_duration_ms', comparator: 'lte', threshold: 1250 }], monitor_rules: [], workload_shape_config: { goal_driven: { groups: { group_0: { target_tps: 25, art: { basis: 'manual', art_ms: 400 } } } } } }
  const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Shape guardrail source', duration: '1m', scenario_mode: 'goal_oriented', workload_shape_id: 'open-arrival-rate', goal_config: goal, groups: [{ name: 'orders', script_id: script, vusers: 2, initialization: 'at_once', transaction_response_time_sla_ms: 1250 }] } })))
  const original = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  await testInfo.attach('original-shape-guardrails', { body: JSON.stringify(original, null, 2), contentType: 'application/json' })
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Shape guardrail source' }), 'Shape guardrail source')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Model & shape/ }).click()
  await page.getByRole('button', { name: /^Stepped Ramp/ }).click()
  await page.getByRole('button', { name: 'Use Stepped Ramp', exact: true }).click()
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run settings/ }).click()
  await page.getByRole('spinbutton', { name: 'Ending load level VUsers', exact: true }).fill('14')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
  await page.getByRole('button', { name: 'Save scenario', exact: true }).click()
  const response = await saved
  const submitted = response.request().postDataJSON()
  await testInfo.attach('shape-guardrail-save', { body: JSON.stringify({ submitted, status: response.status(), response: await response.text() }, null, 2), contentType: 'application/json' })
  expect(response.ok()).toBeTruthy()
  const reopened = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  await testInfo.attach('reopened-shape-guardrails', { body: JSON.stringify(reopened, null, 2), contentType: 'application/json' })
  expect(submitted.workload_shape_id).toBe('stepped-ramp')
  const replacedOrCanonicallyEmpty = new Set(['target_average_tps', 'min_vusers', 'max_vusers', 'workload_shape_config', 'monitor_rules'])
  const guardrails = Object.fromEntries(Object.entries(goal).filter(([key]) => !replacedOrCanonicallyEmpty.has(key)))
  expect(submitted.goal_config).toMatchObject(guardrails)
  expect(reopened.goal_config).toMatchObject(guardrails)
  // Go's canonical DTO omits zero-length rule arrays; their empty semantics
  // are retained, while explicit nonempty-to-empty template submission is unit-tested.
  expect(submitted.goal_config.monitor_rules ?? []).toEqual([])
  expect(reopened.goal_config).not.toHaveProperty('monitor_rules')
  for (const field of ['target_average_tps', 'min_vusers', 'max_vusers']) expect(reopened.goal_config).not.toHaveProperty(field)
  expect(reopened.goal_config).not.toHaveProperty('early_stop')
  expect(reopened.goal_config).not.toHaveProperty('workload_shape_config.goal_driven')
  expect(reopened.goal_config).toHaveProperty('workload_shape_config.stepped_ramp')
  expect(reopened.scripts).toMatchObject([{ script_id: script, vusers: 14, initialization: 'at_once', transaction_response_time_sla_ms: 1250 }])
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Shape guardrail source' }), 'Shape guardrail source')
  await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run settings/ }).click()
  await expect(page.getByRole('spinbutton', { name: 'Ending load level VUsers', exact: true })).toHaveValue('14')
  expect(target.count()).toBe(0)
})


test('scenario prerequisite saves retain archive siblings and later editor changes', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await seedScript(request, workspace, 'Original group script')
  // This legacy plain response is the direct-editor cohort. Protected imports
  // retain save-before-edit; all saves and reopen requests below use real HTTP.
  await page.route('**/api/v1/scripts/import/enterprise-script-archive/validate', async route => {
    const response = await route.fetch()
    expect(response.ok()).toBe(true)
    const payload = await response.json()
    expect(payload.secret_binding_requirements || []).toHaveLength(0)
    const draft = canonicalImportDraft(payload)
    delete payload.import_draft_expires_at
    if (draft.metadata?.import?.preserved) delete draft.metadata.import.preserved.server_import_attestation_v1
    await route.fulfill({ response, json: payload })
  })
  await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByPlaceholder('Scenario name').fill('Retained prerequisite scenario')
  await page.getByRole('button', { name: 'Add script to Group 1' }).click()
  await page.getByRole('row').filter({ hasText: 'Original group script' }).getByRole('button', { name: 'Use for Group 1' }).click()
  await page.getByRole('button', { name: /Add group/ }).click()
  await page.getByRole('button', { name: 'Add script to Group 2' }).click()
  await page.getByRole('button', { name: 'Import script', exact: true }).click()
  await page.getByRole('button', { name: /LoadRunner Script Archive/ }).click()
  const archive = storedZip([
    { name: 'Plain.usr', content: '[General]\nType=Web\n[Actions]\nAction1=Action.c\n' },
    { name: 'Action.c', content: 'Action()\n{\nweb_url("Home", "URL=https://example.invalid/home", LAST);\nreturn 0;\n}' },
  ])
  const siblingArchive = storedZip([
    { name: 'Sibling.usr', content: '[General]\nType=Web\n[Actions]\nAction1=Action.c\n' },
    { name: 'Action.c', content: 'Action()\n{\nweb_url("Sibling", "URL=https://example.invalid/sibling", LAST);\nreturn 0;\n}' },
  ])
  await page.locator('input[type=file][accept=".zip,.usz"]').setInputFiles([
    { name: 'first.zip', mimeType: 'application/zip', buffer: archive },
    { name: 'sibling.zip', mimeType: 'application/zip', buffer: siblingArchive },
  ])
  await page.getByRole('button', { name: 'Validate 2 Scripts', exact: true }).click()
  await page.getByRole('button', { name: 'Open draft in Visual Editor', exact: true }).click()
  const name = page.getByRole('textbox', { name: 'Script name', exact: true })
  await expect(name).toBeVisible()
  await name.fill('Saved prerequisite draft')
  let releaseResponse = () => {}
  let responseCreated = () => {}
  const created = new Promise<void>(resolve => { responseCreated = resolve })
  const release = new Promise<void>(resolve => { releaseResponse = resolve })
  await page.route('**/api/v1/scripts', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    const response = await route.fetch()
    responseCreated()
    await release
    await route.fulfill({ response })
  }, { times: 1 })
  const save = mutation(page, 'POST', '/api/v1/scripts')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  try {
    await created
    await name.fill('Later prerequisite changes')
  } finally { releaseResponse() }
  const saved = await json(await save)
  await expect(page.getByRole('region', { name: 'Create prerequisite script' })).toBeVisible()
  await expect(name).toHaveValue('Later prerequisite changes')
  // Saving the later edit updates the same library script and keeps its source.
  const update = mutation(page, 'PUT', `/api/v1/scripts/${saved.id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await update)
  await page.getByRole('button', { name: 'Back to converted script', exact: true }).click()
  await expect(page.getByText('sibling.zip', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('region', { name: 'Create prerequisite script' })).toBeVisible()
  await page.getByRole('button', { name: 'Back to scenario draft', exact: true }).click()
  const leave = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: 'Unsaved Changes', exact: true }) })
  await expect(leave).toContainText('LoadRunner import review')
  await leave.getByRole('button', { name: 'Keep editing', exact: true }).click()
  await page.getByRole('button', { name: /^sibling\.zip / }).click()
  const siblingSave = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
  await page.getByRole('button', { name: 'Save reviewed drafts', exact: true }).click()
  await json(await siblingSave)
  await page.getByRole('button', { name: 'Back to scenario draft', exact: true }).click()
  await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Retained prerequisite scenario')
  const groups = page.locator('.scenario-script-group-card')
  await expect(groups).toHaveCount(2)
  await expect(groups.nth(0)).toContainText('Original group script')
  await expect(groups.nth(1)).toContainText('Later prerequisite changes')
  expect((await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))).scripts).toHaveLength(3)
})
