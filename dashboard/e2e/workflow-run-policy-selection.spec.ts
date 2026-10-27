import { test, expect, seedScript, seedScenario, login, json, idOf } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('an exact policy revision survives held-run creation, draft restore and queued-copy editing', async ({ page, target, trackRun }, testInfo) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Policy_selection_HTTP', target.url)
  const scenarioId = await seedScenario(request, workspace, scriptId, 'Policy selection scenario')
  const policyScope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const definition = { version: 1, baseline: { mode: 'none' }, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }] }
  const policy = await json(await request.post(`/api/v1/analysis/policies?${policyScope}`, { data: { kind: 'browser_policy', name: 'Chosen policy', definition } }))
  await json(await request.put(`/api/v1/analysis/policies/${policy.id}?${policyScope}`, { data: { kind: 'browser_policy', revision: 1, name: 'Chosen policy revised', definition } }))
  const selection = { browser: { id: policy.id, revision: 1 } }
  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenarioId}`)
  await page.reload()
  await page.getByText('Browser and journey policies', { exact: true }).click()
  await page.getByRole('combobox', { name: 'Browser policy', exact: true }).selectOption(String(policy.id))
  await expect(page.getByRole('spinbutton', { name: 'Browser policy revision', exact: true })).toHaveValue('2')
  await page.getByRole('spinbutton', { name: 'Browser policy revision', exact: true }).fill('1')
  await page.getByRole('textbox', { name: 'Run Name', exact: true }).fill('Selected policy draft')
  await page.getByRole('navigation').getByRole('button', { name: 'Dashboard', exact: true }).click()
  await page.getByRole('dialog', { name: 'Unsaved changes' }).getByRole('button', { name: 'Save changes', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Dashboard', level: 1 })).toBeVisible()
  await page.getByRole('navigation').getByRole('button', { name: 'New run', exact: true }).click()
  await page.getByRole('button', { name: 'Restore saved draft', exact: true }).click()
  await page.getByText('Browser and journey policies', { exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Browser policy', exact: true })).toHaveValue(String(policy.id))
  await expect(page.getByRole('spinbutton', { name: 'Browser policy revision', exact: true })).toHaveValue('1')
  await page.getByLabel('Release', { exact: true }).selectOption(workspace.releaseId)
  await page.setViewportSize({ width: 760, height: 1000 })
  await page.getByRole('combobox', { name: 'Browser policy', exact: true }).scrollIntoViewIfNeeded()
  expect(await page.locator('.run-policy-selector').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('run-policy-selection.png'), animations: 'disabled' })
  const heldResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await page.getByRole('button', { name: 'Hold for manual start', exact: true }).click()
  const response = await heldResponse
  expect(response.ok(), await response.text()).toBeTruthy()
  const heldId = idOf(await response.json()); trackRun(heldId)
  const intent = response.request().postDataJSON()
  expect(intent.analysis_policy_selections).toEqual(selection)
  expect((await json(await request.get(`/api/v1/tests/${heldId}`))).analysis_policy_selections).toEqual(selection)
  delete intent.reserve_launch_window
  delete intent.scheduled_start_at
  intent.name = 'Selected policy queued source'
  const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: intent }))); trackRun(sourceId)
  await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  const editor = page.getByRole('dialog', { name: 'Create revised queued run', exact: true })
  await editor.getByText('Browser and journey policies', { exact: true }).click()
  await expect(editor.getByRole('spinbutton', { name: 'Browser policy revision', exact: true })).toHaveValue('1')
  await editor.getByRole('combobox', { name: 'Browser policy', exact: true }).selectOption('')
  const copyResponse = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  await editor.getByRole('button', { name: /^Create revised copy/ }).click()
  const copied = await copyResponse
  expect(copied.ok(), await copied.text()).toBeTruthy()
  const copyId = idOf(await copied.json()); trackRun(copyId)
  expect(copied.request().postDataJSON().analysis_policy_selections).toEqual({})
  expect((await json(await request.get(`/api/v1/tests/${copyId}`))).analysis_policy_selections).toEqual({})
  expect((await json(await request.get(`/api/v1/tests/${sourceId}`))).analysis_policy_selections).toEqual(selection)
  expect(target.count()).toBe(0)
})

test('admission pins the selected historical policy and refuses deleted or foreign selections', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const other = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Pinned_selection_HTTP', target.url)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const definition = { version: 1, baseline: { mode: 'none' }, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }] }
  const policy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, { data: { kind: 'browser_policy', name: 'Original revision', definition } }))
  const selection = { browser: { id: policy.id, revision: 1 } }
  const intent = { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Pinned selection', duration: '1s', groups: [{ name: 'shop', script_id: scriptId, vusers: 1 }], analysis_policy_selections: selection }
  const minted = await request.post('/api/v1/tests', { data: { ...intent, analysis_policy_selections: { browser: { ...selection.browser, sha256: 'caller-supplied' } } } })
  expect(minted.status()).toBe(400)
  const runId = idOf(await json(await request.post('/api/v1/tests', { data: intent }))); trackRun(runId)
  await json(await request.put(`/api/v1/analysis/policies/${policy.id}?${scope}`, { data: { kind: 'browser_policy', revision: 1, name: 'Later revision', definition } }))
  const start = await request.post(`/api/v1/tests/${runId}/start`)
  expect(start.ok(), await start.text()).toBeTruthy()
  await expect.poll(async () => (await request.get(`/api/v1/tests/${runId}/archive/manifest`)).ok(), { timeout: 90_000 }).toBe(true)
  const manifest = await json(await request.get(`/api/v1/tests/${runId}/archive/manifest`)) as { provenance: { policy_pins: Array<{ id: string; name: string; revision: number; source: string; sha256: string }> } }
  const pin = manifest.provenance.policy_pins.find(item => item.id === policy.id)
  expect(pin).toMatchObject({ name: 'Original revision', revision: 1, source: 'run_selection' })
  expect(pin?.sha256).toMatch(/^[a-f0-9]{64}$/)
  const otherScript = await seedScript(request, other, 'Foreign_selection_HTTP', target.url)
  const foreign = await request.post('/api/v1/tests', { data: { ...intent, project_id: other.projectId, application_id: other.applicationId, release_id: other.releaseId, groups: [{ name: 'shop', script_id: otherScript, vusers: 1 }] } })
  expect(foreign.ok()).toBe(false)
  const refusedId = idOf(await json(await request.post('/api/v1/tests', { data: { ...intent, name: 'Deleted selection' } }))); trackRun(refusedId)
  await json(await request.delete(`/api/v1/analysis/policies/${policy.id}?${scope}&revision=2`))
  const before = target.count()
  const refused = await request.post(`/api/v1/tests/${refusedId}/start`)
  expect(refused.ok()).toBe(false)
  expect((await json(await request.get(`/api/v1/tests/${refusedId}`))).status).toBe('QUEUED')
  expect(target.count()).toBe(before)
  expect((await request.get(`/api/v1/analysis/policies/${policy.id}?${scope}&revision=1`)).ok()).toBe(true)
  const retained = await json(await request.get(`/api/v1/tests/${runId}/archive/manifest`)) as typeof manifest
  expect(retained.provenance.policy_pins).toEqual(manifest.provenance.policy_pins)
})

test('journey policy editing preserves history and the selected run revision', async ({ page, target, trackRun }, testInfo) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  await page.goto(`/#/settings?setting=analysis-policies&${scopeQuery(workspace)}`)
  await page.reload()
  const panel = page.getByRole('region', { name: 'Browser and journey policies', exact: true })
  await panel.getByRole('tab', { name: 'Journey policies', exact: true }).click()
  await panel.getByRole('button', { name: 'New journey policy', exact: true }).click()
  const editor = panel.getByRole('form', { name: 'New journey policy', exact: true })
  await editor.getByLabel('Name', { exact: true }).fill('Checkout journey')
  await editor.getByLabel('Journey 1 name', { exact: true }).fill('Checkout')
  await editor.getByLabel('Journey 1 start', { exact: true }).fill('Browse')
  await editor.getByLabel('Journey 1 success', { exact: true }).fill('Submit')
  await editor.getByRole('button', { name: 'Save policy', exact: true }).click()
  const table = panel.getByRole('table', { name: 'Journey policies', exact: true })
  await expect(table.getByRole('row', { name: /Checkout journey/ })).toContainText('1')
  const [stored] = await json(await request.get(`/api/v1/analysis/policies?${scope}&kind=journey_policy`)) as unknown as Array<{ id: string; definition: Record<string, unknown> }>
  await table.getByRole('button', { name: 'Edit', exact: true }).click()
  const edit = panel.getByRole('form', { name: 'Edit journey policy', exact: true })
  await json(await request.put(`/api/v1/analysis/policies/${stored.id}?${scope}`, { data: { kind: 'journey_policy', name: 'Checkout journey revised', definition: stored.definition, revision: 1 } }))
  await edit.getByLabel('Journey 1 weight', { exact: true }).fill('2')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(edit).toContainText('This policy changed since you opened it')
  await edit.getByRole('button', { name: 'Load the current revision', exact: true }).click()
  await expect(edit).toContainText('(revision 2)')
  await edit.getByLabel('Journey 1 weight', { exact: true }).fill('2')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(table.getByRole('row', { name: /Checkout journey revised/ })).toContainText('3')
  await table.getByRole('button', { name: 'View a revision', exact: true }).click()
  const revision = panel.getByRole('region', { name: 'Policy revision', exact: true })
  await revision.getByLabel('Revision', { exact: true }).fill('1')
  await revision.getByRole('button', { name: 'Show', exact: true }).click()
  await expect(revision).toContainText('"weight": 1')
  const script = await seedScript(request, workspace, 'Journey_policy_selection_HTTP', target.url)
  const scenario = await seedScenario(request, workspace, script, 'Journey policy selection')
  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenario}`)
  await page.reload()
  await page.getByText('Browser and journey policies', { exact: true }).click()
  await page.getByRole('combobox', { name: 'Journey policy', exact: true }).selectOption(stored.id)
  await expect(page.getByRole('spinbutton', { name: 'Journey policy revision', exact: true })).toHaveValue('3')
  await page.getByRole('spinbutton', { name: 'Journey policy revision', exact: true }).fill('1')
  await page.setViewportSize({ width: 760, height: 1000 })
  await page.getByRole('combobox', { name: 'Journey policy', exact: true }).scrollIntoViewIfNeeded()
  expect(await page.locator('.run-policy-selector').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('journey-run-policy-selection.png'), animations: 'disabled' })
  const creation = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  await page.getByRole('button', { name: 'Hold for manual start', exact: true }).click()
  const response = await creation
  expect(response.ok(), await response.text()).toBeTruthy()
  const run = idOf(await response.json()); trackRun(run)
  const chosen = { journey: { id: stored.id, revision: 1 } }
  expect(response.request().postDataJSON().analysis_policy_selections).toEqual(chosen)
  expect((await json(await request.get(`/api/v1/tests/${run}`))).analysis_policy_selections).toEqual(chosen)
  expect(target.count()).toBe(0)
})

test('recurring scenario schedules pin, restore, and clear immutable browser and journey policy revisions', async ({ page, target }, testInfo) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace, 'Schedule_policy_HTTP', target.url)
  const scenarioId = await seedScenario(request, workspace, scriptId, 'Schedule policy scenario')
  const policyScope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const browserDef = { version: 1, baseline: { mode: 'none' }, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }] }
  const browserPolicy = await json(await request.post(`/api/v1/analysis/policies?${policyScope}`, { data: { kind: 'browser_policy', name: 'Schedule Browser Policy', definition: browserDef } }))
  await json(await request.put(`/api/v1/analysis/policies/${browserPolicy.id}?${policyScope}`, { data: { kind: 'browser_policy', revision: 1, name: 'Schedule Browser Policy v2', definition: browserDef } }))
  const journeyDef = {
    version: 1,
    step_risk_enabled: true,
    journeys: [{ id: 'checkout', name: 'Checkout', weight: 1, start: { action: 'GET /', step_path: 'actions[0]' }, success: { action: 'GET /', step_path: 'actions[0]' } }],
  }
  const journeyPolicy = await json(await request.post(`/api/v1/analysis/policies?${policyScope}`, { data: { kind: 'journey_policy', name: 'Schedule Journey Policy', definition: journeyDef } }))
  const selection = {
    browser: { id: browserPolicy.id, revision: 1 },
    journey: { id: journeyPolicy.id, revision: 1 },
  }

  await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenarioId}`)
  await page.reload()
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  const scheduleLayout = page.locator('.new-test-schedules-layout')
  await scheduleLayout.getByLabel('Schedule name', { exact: true }).fill('Nightly pinned policy schedule')
  await scheduleLayout.getByLabel('Release', { exact: true }).selectOption(workspace.releaseId)
  await scheduleLayout.getByText('Browser and journey policies', { exact: true }).click()
  await scheduleLayout.getByRole('combobox', { name: 'Browser policy', exact: true }).selectOption(String(browserPolicy.id))
  await expect(scheduleLayout.getByRole('spinbutton', { name: 'Browser policy revision', exact: true })).toHaveValue('2')
  await scheduleLayout.getByRole('spinbutton', { name: 'Browser policy revision', exact: true }).fill('1')
  await scheduleLayout.getByRole('combobox', { name: 'Journey policy', exact: true }).selectOption(String(journeyPolicy.id))
  await expect(scheduleLayout.getByRole('spinbutton', { name: 'Journey policy revision', exact: true })).toHaveValue('1')

  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await page.reload()
  await page.getByRole('button', { name: 'Restore saved draft', exact: true }).click()
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await scheduleLayout.getByText('Browser and journey policies', { exact: true }).click()
  await expect(scheduleLayout.getByRole('combobox', { name: 'Browser policy', exact: true })).toHaveValue(String(browserPolicy.id))
  await expect(scheduleLayout.getByRole('spinbutton', { name: 'Browser policy revision', exact: true })).toHaveValue('1')
  await expect(scheduleLayout.getByRole('combobox', { name: 'Journey policy', exact: true })).toHaveValue(String(journeyPolicy.id))
  await expect(scheduleLayout.getByRole('spinbutton', { name: 'Journey policy revision', exact: true })).toHaveValue('1')
  await page.screenshot({ path: testInfo.outputPath('schedule-policy-selection.png'), animations: 'disabled' })

  const createResponsePromise = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenarioId}/schedules`)
  await expect(scheduleLayout.getByRole('button', { name: 'Create schedule', exact: true })).toBeEnabled()
  await scheduleLayout.getByRole('button', { name: 'Create schedule', exact: true }).click()
  const createResponse = await createResponsePromise
  expect(createResponse.ok(), await createResponse.text()).toBeTruthy()
  expect(createResponse.request().postDataJSON().analysis_policy_selections).toEqual(selection)
  const createdSchedule = await createResponse.json()
  expect(createdSchedule.analysis_policy_selections).toEqual(selection)

  const schedulesRegion = page.getByRole('region', { name: 'Selected scenario schedules', exact: true })
  await expect(schedulesRegion).toContainText('browser policy r1')
  await expect(schedulesRegion).toContainText('journey policy r1')
  const catalogRegion = page.getByRole('region', { name: 'All scenario schedules', exact: true })
  await expect(catalogRegion).toContainText('Browser policy r1 · Journey policy r1')

  await schedulesRegion.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(scheduleLayout.getByRole('combobox', { name: 'Browser policy', exact: true })).toHaveValue(String(browserPolicy.id))
  await scheduleLayout.getByRole('combobox', { name: 'Browser policy', exact: true }).selectOption('')
  await scheduleLayout.getByRole('combobox', { name: 'Journey policy', exact: true }).selectOption('')

  const updateResponsePromise = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenarioId}/schedules/${createdSchedule.id}`)
  await expect(scheduleLayout.getByRole('button', { name: 'Save schedule', exact: true })).toBeEnabled()
  await scheduleLayout.getByRole('button', { name: 'Save schedule', exact: true }).click()
  const updateResponse = await updateResponsePromise
  expect(updateResponse.ok(), await updateResponse.text()).toBeTruthy()
  expect(updateResponse.request().postDataJSON().analysis_policy_selections).toEqual({})
  const updatedSchedule = await updateResponse.json()
  expect(updatedSchedule.analysis_policy_selections).toBeUndefined()
  await expect(schedulesRegion).not.toContainText('browser policy r1')
  expect(target.count()).toBe(0)
})
