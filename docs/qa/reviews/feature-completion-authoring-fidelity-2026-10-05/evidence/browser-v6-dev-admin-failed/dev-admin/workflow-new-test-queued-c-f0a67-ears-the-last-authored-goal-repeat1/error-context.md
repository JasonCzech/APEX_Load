# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-new-test.spec.ts >> queued-copy preserves source capture IDs and durably clears the last authored goal
- Location: e2e/workflow-new-test.spec.ts:426:1

# Error details

```
Error: expect(locator).toHaveValue(expected) failed

Locator: getByRole('dialog', { name: 'Create Revised Queued Run', exact: true }).getByLabel('Elastic log capture', { exact: true })
Expected: "4ad98e1c-d52b-4391-b976-4bc6ed29aa19"
Timeout: 15000ms
Error: element(s) not found

Call log:
  - Expect "toHaveValue" with timeout 15000ms
  - waiting for getByRole('dialog', { name: 'Create Revised Queued Run', exact: true }).getByLabel('Elastic log capture', { exact: true })

```

```yaml
- complementary:
  - button "APEX Load dashboard":
    - img "APEX Load"
    - text: APEX Load
  - button "Workspace context. e2e-1791232680704-408907 domain / e2e-1791232680704-408907 project / e2e-1791232680704-408907 application / e2e-1791232680704-408907 1.0": Workspace e2e-1791232680704-408907 domain / e2e-1791232680704-408907 project / e2e-1791232680704-408907 application / e2e-1791232680704-408907 1.0
  - navigation "Primary navigation":
    - button "New Test":
      - img
      - text: New Test
    - button "Dashboard"
    - button "Command Center"
    - button "Results":
      - img
      - text: Results
    - button "Monitoring":
      - img
      - text: Monitoring
    - button "Activity"
    - button "Scripts":
      - img
      - text: Scripts
    - button "Scenarios":
      - img
      - text: Scenarios
    - button "Repository":
      - img
      - text: Repository
    - button "Access":
      - img
      - text: Access
    - button "Settings":
      - img
      - text: Settings
    - button "Help & documentation":
      - img
      - text: Help & documentation
  - 'button "Preview permissions. Current mode: admin"': Preview permissions
  - text: Live
  - 'button "Build info: local · dev"': Version Info
  - button "Collapse sidebar" [expanded]:
    - img
- main "Command Center":
  - status: Command Center
  - heading "Command Center" [level=1]
  - group "Command Center tools":
    - button "Open analysis"
  - navigation "Active test runs":
    - link "P95 capture source Queued":
      - /url: "#/command-center/aa89cf4f-75ba-48cc-bf5d-d0b33c554739?projectId=3994a9d8-3bfd-4d72-bf5c-363b534b3b99&applicationId=8fa0e7e7-b671-4266-8f29-d9046f4badb1&releaseId=e344b512-20a1-4ebd-ab8e-4238ee704d53"
  - link "Help for this page":
    - /url: "#/docs?guide=user-guide&article=execution"
  - text: Browser · Server
  - combobox "Dashboard color theme":
    - option "System (Auto)"
    - option "Dark Theme" [selected]
    - option "Light Theme"
    - option "Solarized Dark"
    - option "Solarized Light"
    - option "Monokai Dimmed"
  - navigation "Switch active run":
    - button "All active runs"
    - text: Current run
    - combobox "Current run":
      - option "P95 capture source · LOC-RUN-20261005-013 · QUEUED" [selected]
  - status "Load observation status": Awaiting telemetry. No current load observation is available.
  - region "Command center run controls":
    - text: QUEUED
    - strong: P95 capture source
    - text: LOC-RUN-20261005-013 / 1m duration / 1 groups Planned load
    - strong: 0 TPS · Not started
    - text: Remaining
    - strong: 1m 00s
    - text: TPS
    - strong: Not started
    - text: Overall Error Rate
    - strong: No samples
    - button "Advanced run details"
    - button "Test Notes"
    - button "Create revised queued copy"
    - button "Start Test"
    - button "Abort"
    - status: Showing run observations from the controller.
  - region "Planned run":
    - heading "Run not started" [level=3]
    - paragraph: Load targets describe the plan. Throughput, errors and response times will appear after execution produces samples.
    - list "Configured group runtime settings":
      - listitem:
        - button "orders runtime settings": orders · Runtime Settings
    - region "Load model profile":
      - heading "Load Model" [level=3]
      - paragraph: Start / 1m 00s remaining
      - text: 0% elapsed
      - group: Load comparison
- dialog "Create Revised Queued Run":
  - heading "Create Revised Queued Run" [level=3]
  - text: 1 total VUsers
  - paragraph: Saving here creates a new queued run. The current queued run remains unchanged.
  - button "VUser-Driven"
  - button "Goal-Oriented"
  - text: Scenario Name
  - textbox "Scenario Name":
    - /placeholder: Optional — auto-generated if empty
    - text: P95 capture source
  - paragraph:
    - strong: "Completion:"
    - text: Duration. The source completion mode is retained.
  - text: Steady State Duration
  - spinbutton "Steady State Duration": "1"
  - combobox "Steady State Duration Unit":
    - option "Seconds"
    - option "Minutes" [selected]
    - option "Hours"
  - button "30s"
  - button "1m"
  - button "5m"
  - button "10m"
  - button "30m"
  - button "1h"
  - text: Global Start Delay
  - spinbutton "Global Start Delay"
  - combobox "Global Start Delay Unit":
    - option "Seconds"
    - option "Minutes" [selected]
    - option "Hours"
  - button "30s"
  - button "1m"
  - button "5m"
  - button "10m"
  - button "30m"
  - button "1h"
  - text: Pre-Run Script Shakeout
  - checkbox "Pre-Run Script Shakeout Run one instance of each script before full scale startup. Any failure halts the scenario and raises an alert."
  - paragraph: Run one instance of each script before full scale startup. Any failure halts the scenario and raises an alert.
  - text: Resource Baseline
  - checkbox "Resource Baseline Add a fixed 5 minute baseline before load starts and another 5 minute baseline after load stops so resource utilization can be observed without user traffic."
  - paragraph: Add a fixed 5 minute baseline before load starts and another 5 minute baseline after load stops so resource utilization can be observed without user traffic.
  - text: Elastic log capture
  - combobox "Elastic log capture Optional. The controller polls the selected ready connection; load generators are unaffected.":
    - option "Off for this test"
    - option "Copy source logs" [selected]
  - text: Optional. The controller polls the selected ready connection; load generators are unaffected. Dynatrace SaaS capture
  - combobox "Dynatrace SaaS capture Optional. Metrics, problems, and Grail logs are polled by the controller.":
    - option "Off for this test"
    - option "Copy source SaaS" [selected]
  - text: Optional. Metrics, problems, and Grail logs are polled by the controller.
  - paragraph: "Capture selected: Copy source logs · Log/error counts and configured numeric aggregations. The saved definition is snapshotted at launch. Correlation uses configured test/trace fields; time-window fallback may include unrelated logs. Evidence is bounded and redacted, not a full log export."
  - paragraph: "Capture selected: Copy source SaaS · Problems. The saved definition is snapshotted at launch. Correlation uses configured test/trace fields; time-window fallback may include unrelated logs. Evidence is bounded and redacted, not a full log export."
  - application: 0s 6s 12s 18s 24s 30s 36s 42s 48s 54s 1m Elapsed time 0 0.25 0.5 0.75 1 VUsers
  - text: Scenario Load Profile
  - heading "Draft Capacity Estimate" [level=4]
  - text: Recommended
  - strong: "1"
  - text: generator Available
  - strong: "1"
  - text: 1 configured Bottleneck
  - strong: cpu
  - text: 20% headroom
  - paragraph: "Availability source: Configured inventory. Snapshot: 1 configured."
  - paragraph: "Estimated total load: 0.00 CPU, 1 MB memory, 0 Mbps network."
  - heading "Controller / Generator Assignment" [level=4]
  - paragraph: Overall size is derived from the scenario footprint, current generator capacity profile, and the configured distributed targets.
  - text: Footprint
  - strong: 1 VUsers
  - text: 1m steady state Complexity
  - strong: "2.00"
  - text: script design score Estimated Load
  - strong: 0.00 CPU
  - text: 1 MB / 0 Mbps
  - paragraph: Auto-assignment recommends 1 host for the current scenario footprint.
  - text: Generator sharing
  - combobox "Generator sharing":
    - option "Shared capacity" [selected]
    - option "Dedicated generators"
  - radiogroup "Scenario generator placement":
    - radio "Automatic placement" [checked]
    - text: Automatic placement
    - radio "Pin to host(s)"
    - text: Pin to host(s)
    - radio "Region-weighted placement" [disabled]
    - text: Region-weighted placement Requires generators in at least two configured regions.
    - radio "Load-zone-weighted placement" [disabled]
    - text: Load-zone-weighted placement Requires generators in at least two configured load zones.
    - radio "Require capability"
    - text: Require capability
  - paragraph: Uses automatic placement for this scenario.
  - checkbox "Tolerate generator loss Continue on surviving assigned generators when policy permits."
  - strong: Tolerate generator loss
  - text: Continue on surviving assigned generators when policy permits.
  - checkbox "Continue through controller failover"
  - strong: Continue through controller failover
  - text: On an HA controller, a standby adopts this run if the leader is lost instead of failing it. Launch checks warn when this deployment cannot honour it.
  - heading "Goal Settings" [level=4]
  - paragraph: Define the target and guardrails. The script groups below still provide the starting seed load and script mix for the scenario.
  - text: Target Avg TPS
  - spinbutton "Target Avg TPS"
  - text: P95 Latency Ceiling (ms)
  - spinbutton "P95 Latency Ceiling (ms)": "1000"
  - text: Max Error %
  - spinbutton "Max Error %"
  - text: Avg Server Processing Ceiling (ms)
  - spinbutton "Avg Server Processing Ceiling (ms)"
  - text: Avg Transfer Ceiling (ms)
  - spinbutton "Avg Transfer Ceiling (ms)"
  - text: Max Error Count
  - spinbutton "Max Error Count"
  - text: Min VUsers
  - spinbutton "Min VUsers"
  - text: Max VUsers
  - spinbutton "Max VUsers"
  - text: Max LCP (ms)
  - spinbutton "Max LCP (ms)"
  - text: Max INP (ms)
  - spinbutton "Max INP (ms)"
  - text: Max CLS
  - spinbutton "Max CLS"
  - text: Max Throttled Requests
  - spinbutton "Max Throttled Requests"
  - text: Max End-to-End Latency (ms)
  - spinbutton "Max End-to-End Latency (ms)"
  - text: Max Partition Skew %
  - spinbutton "Max Partition Skew %"
  - text: Max Redelivery Rate %
  - spinbutton "Max Redelivery Rate %"
  - text: Max Dead-Letter Rate %
  - spinbutton "Max Dead-Letter Rate %"
  - text: SLA Breach Window
  - spinbutton "SLA Breach Window"
  - combobox "SLA Breach Window Unit":
    - option "s"
    - option "m" [selected]
    - option "h"
  - checkbox "Stop on sustained SLA breach"
  - text: Stop on sustained SLA breach
  - checkbox "Stop when total errors exceed max error count"
  - text: Stop when total errors exceed max error count
  - heading "Script Seed Groups" [level=4]
  - paragraph: Seed VUsers define the initial script distribution until goal-based autoscaling is implemented.
  - button "+ Add Group"
  - text: 1 Group Name
  - textbox "Group Name":
    - /placeholder: Group 1
    - text: orders
  - text: Script
  - combobox "Script":
    - option "Capture_Clear_HTTP (http)" [selected]
  - text: Seed VUsers
  - spinbutton "Seed VUsers": "1"
  - text: Ramp-up
  - spinbutton "Ramp-up"
  - combobox "Ramp-up Unit":
    - option "s"
    - option "m" [selected]
    - option "h"
  - text: Ramp-down
  - spinbutton "Ramp-down"
  - combobox "Ramp-down Unit":
    - option "s"
    - option "m" [selected]
    - option "h"
  - text: Iterations
  - spinbutton "Iterations"
  - text: Initialization
  - combobox "Initialization":
    - option "At Runtime" [selected]
    - option "At Once"
  - text: Role
  - combobox "Role":
    - option "Load" [selected]
    - option "Observer Canary (browser scripts only)" [disabled]
  - radiogroup "Group generator placement":
    - radio "Inherit scenario default" [checked]
    - text: Inherit scenario default
    - radio "Automatic placement"
    - text: Automatic placement
    - radio "Pin to host(s)"
    - text: Pin to host(s)
    - radio "Require capability"
    - text: Require capability
  - paragraph: Inherits the scenario Controller / Generator assignment.
  - region "Transaction response-time SLA":
    - heading "Transaction response-time SLA" [level=4]
    - paragraph: Require at least 95% of successful named business transactions to finish within the configured time. Request/action rows and failed samples are excluded.
    - text: 0 of 1 groups covered · 0 overrides Default for all groups (seconds)
    - spinbutton "Default transaction SLA in seconds"
    - text: Leave empty to configure only selected groups.
    - strong: orders
    - text: Capture_Clear_HTTP orders SLA source
    - combobox "orders SLA source":
      - option "Use default" [selected]
      - option "Override"
    - text: No SLA
  - heading "Runtime Settings Overrides" [level=4]
  - paragraph: Test-level runtime overrides applied to every group in this run. Unset fields inherit each group's script defaults.
  - tablist "Runtime Settings Overrides sections":
    - tab "Summary" [selected]
    - tab "Think Time"
    - tab "Iteration Pacing"
    - tab "Run Logic"
    - tab "Error Handling"
    - tab "HTTP Transport"
    - tab "Browser Emulation"
    - tab "Web/HTTP"
  - tabpanel "Summary":
    - table:
      - rowgroup:
        - row "Setting Effective value Source Actions":
          - columnheader "Setting"
          - columnheader "Effective value"
          - columnheader "Source"
          - columnheader "Actions"
      - rowgroup:
        - row "Diagnostics Engine default Engine default":
          - cell "Diagnostics"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "Iterations Duration-driven Engine default":
          - cell "Iterations"
          - cell "Duration-driven"
          - cell "Engine default"
          - cell
        - row "Pacing None Engine default":
          - cell "Pacing"
          - cell "None"
          - cell "Engine default"
          - cell
        - row "Think time None Engine default":
          - cell "Think time"
          - cell "None"
          - cell "Engine default"
          - cell
        - row "Log level Engine default Engine default":
          - cell "Log level"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "Auto-transactions Engine default Engine default":
          - cell "Auto-transactions"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "Error handling Stop on error Engine default":
          - cell "Error handling"
          - cell "Stop on error"
          - cell "Engine default"
          - cell
        - row "Logging Engine default Engine default":
          - cell "Logging"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "Run logic Engine default Engine default":
          - cell "Run logic"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "HTTP transport Engine default Engine default":
          - cell "HTTP transport"
          - cell "Engine default"
          - cell "Engine default"
          - cell
        - row "Web runtime None Engine default":
          - cell "Web runtime"
          - cell "None"
          - cell "Engine default"
          - cell
        - row "Browser emulation None Engine default":
          - cell "Browser emulation"
          - cell "None"
          - cell "Engine default"
          - cell
  - button "Cancel"
  - button "Create Revised Copy (1 group)"
```

# Test source

```ts
  346 |   const runtime = { results: { message_detail: { mode: 'off', include_resources: false } }, websocket: {} }
  347 |   const goal = { transaction_response_time_sla_ms: 0.001, workload_shape_config: { closed_vuser: { scope: 'scenario', scenario: { peak_vusers: 4, ramp_up: '10s', sustain: '50s', ramp_down_enabled: false, ramp_down: '0s' } } } }
  348 |   const groups = [
  349 |     { name: 'orders', script_id: script, vusers: 2, role: 'load', ramp_up: '3s', ramp_down: '4s', start_offset: '1s', initialization: 'at_once', transaction_response_time_sla_ms: 1250, runtime_settings: runtime },
  350 |     { name: 'staged', script_id: script, vusers: 2, role: 'load', start_offset: '2s', initialization: 'at_runtime', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } },
  351 |   ]
  352 |   const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Queued fidelity scenario', duration: '1m', scenario_mode: 'vuser_driven', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: runtime, groups } })))
  353 |   const originalScenario = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  354 |   await page.goto(`/#/new-test?${scopeQuery(workspace)}&scenarioId=${scenario}`)
  355 |   await page.reload()
  356 |   await page.getByRole('button', { name: 'Show selected scenario', exact: true }).click()
  357 |   await page.getByRole('button', { name: 'Edit settings for orders', exact: true }).click()
  358 |   const editor = page.getByRole('dialog', { name: 'Edit orders', exact: true })
  359 |   await editor.getByRole('spinbutton', { name: /^VUsers/ }).fill('4')
  360 |   await editor.getByRole('button', { name: 'Save For This Run', exact: true }).click()
  361 |   await page.getByRole('textbox', { name: 'Run Name', exact: true }).fill('Held fidelity serializer')
  362 |   await page.getByLabel('Release', { exact: true }).selectOption(workspace.releaseId)
  363 |   const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  364 |   // The real free-capacity UI holds a RESERVED run; it does not promise QUEUED.
  365 |   await page.getByRole('button', { name: 'Hold for Manual Start', exact: true }).click()
  366 |   const response = await created
  367 |   expect(response.ok(), await response.text()).toBeTruthy()
  368 |   const heldId = idOf(await response.json()); trackRun(heldId)
  369 |   const heldIntent = response.request().postDataJSON()
  370 |   const held = await json(await request.get(`/api/v1/tests/${heldId}`))
  371 |   await testInfo.attach('held-run-authored-intent', { body: JSON.stringify({ held_intent: heldIntent, held }, null, 2), contentType: 'application/json' })
  372 |   expect(heldIntent.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  373 |   expect(held.status).toBe('RESERVED')
  374 |   expect(held.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  375 |   expect(held.goal_config).toEqual(originalScenario.goal_config)
  376 |   expect(held.runtime_settings).toEqual(originalScenario.runtime_settings)
  377 |   expect(target.count()).toBe(0)
  378 |   // Seed a distinct canonical QUEUED source from that exact owned authored intent.
  379 |   // Only reservation controls change; no status response or admission is mocked.
  380 |   const queuedIntent = structuredClone(heldIntent)
  381 |   delete queuedIntent.reserve_launch_window
  382 |   delete queuedIntent.scheduled_start_at
  383 |   queuedIntent.name = 'Queued fidelity source'
  384 |   const queuedResponse = await request.post('/api/v1/tests', { data: queuedIntent })
  385 |   expect(queuedResponse.ok(), await queuedResponse.text()).toBeTruthy()
  386 |   const sourceId = idOf(await queuedResponse.json()); trackRun(sourceId)
  387 |   const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  388 |   await testInfo.attach('queued-source-authored-intent', { body: JSON.stringify({ queued_seed: queuedIntent, source }, null, 2), contentType: 'application/json' })
  389 |   expect(source.status).toBe('QUEUED')
  390 |   expect(source.goal_config).toEqual(originalScenario.goal_config)
  391 |   expect(source.runtime_settings).toEqual(originalScenario.runtime_settings)
  392 |   await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  393 |   await page.reload()
  394 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  395 |   const copyEditor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  396 |   await expect(copyEditor).toBeVisible()
  397 |   await copyEditor.getByLabel('Scenario Name', { exact: true }).fill('Queued fidelity revised name')
  398 |   await page.screenshot({ path: testInfo.outputPath('queued-copy-fidelity.png') })
  399 |   const copied = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  400 |   await copyEditor.getByRole('button', { name: /^Create Revised Copy/ }).click()
  401 |   const copyResponse = await copied
  402 |   expect(copyResponse.ok(), await copyResponse.text()).toBeTruthy()
  403 |   const createdCopy = await copyResponse.json()
  404 |   const copyId = idOf(createdCopy); trackRun(copyId)
  405 |   const sent = copyResponse.request().postDataJSON()
  406 |   await testInfo.attach('queued-copy-submitted-response', { body: JSON.stringify({ submitted: sent, created: createdCopy }, null, 2), contentType: 'application/json' })
  407 |   expect(copyId).not.toBe(sourceId)
  408 |   expect(sent).toMatchObject({ revision_source_test_id: sourceId, name: 'Queued fidelity revised name', project_id: workspace.projectId, goal_config: source.goal_config, runtime_settings: source.runtime_settings })
  409 |   // The established compact serializer omits the default load role; canonical reads retain it.
  410 |   expect(sent.groups.map((group: Record<string, unknown>) => ({ ...group, role: group.role ?? 'load' }))).toMatchObject([{ ...groups[0], vusers: 4, revision_source_group_index: 0 }, { ...groups[1], revision_source_group_index: 1 }])
  411 |   await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  412 |   await page.reload()
  413 |   const reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  414 |   expect(reopened.status).toBe('QUEUED')
  415 |   expect(reopened.goal_config).toEqual(source.goal_config)
  416 |   expect(reopened.runtime_settings).toEqual(source.runtime_settings)
  417 |   expect(reopened.groups).toMatchObject([{ ...groups[0], vusers: 4 }, groups[1]])
  418 |   const reopenedSource = await json(await request.get(`/api/v1/tests/${sourceId}`))
  419 |   const reopenedScenario = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  420 |   await testInfo.attach('queued-copy-fidelity', { body: JSON.stringify({ scenario: originalScenario, scenario_reopened: reopenedScenario, held_submitted: heldIntent, held, queued_seed_submitted: queuedIntent, source, source_reopened: reopenedSource, submitted: sent, reopened }, null, 2), contentType: 'application/json' })
  421 |   expect(reopenedSource).toEqual(source)
  422 |   expect(scenarioAuthoringProjection(reopenedScenario)).toEqual(scenarioAuthoringProjection(originalScenario))
  423 |   expect(target.count()).toBe(0)
  424 | })
  425 | 
  426 | test('queued-copy preserves source capture IDs and durably clears the last authored goal', async ({ page, target, trackRun }, testInfo) => {
  427 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  428 |   const request = page.context().request
  429 |   const workspace = await createWorkspace(request)
  430 |   const script = await seedScript(request, workspace, 'Capture_Clear_HTTP', target.url)
  431 |   const elk = await json(await request.post(`/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`, { data: { name: 'Copy source logs', enabled: true, endpoint: 'https://capture.invalid', index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message', auth_type: 'api_key', api_key_secret_name: 'e2e-capture-secret' } }))
  432 |   const dt = await json(await request.post(`/api/v1/projects/${workspace.projectId}/monitoring/dynatrace-connections`, { data: { name: 'Copy source SaaS', enabled: true, environment_url: 'https://fidelity.live.dynatrace.com', environment_token_secret: 'e2e-dt-secret', problems_enabled: true, poll_interval_seconds: 60 } }))
  433 |   expect(elk.status).toBe('ready')
  434 |   expect(dt.legacy_capture_status).toBe('ready')
  435 |   const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: { name: 'P95 capture source', project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, duration: '1m', scenario_mode: 'goal_oriented', goal_config: { p95_latency_ms: 1000 }, groups: [{ name: 'orders', script_id: script, vusers: 1 }], elk_capture: { connection_id: idOf(elk) }, dynatrace_capture: { connection_id: idOf(dt) } } })))
  436 |   trackRun(sourceId)
  437 |   const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  438 |   await testInfo.attach('capture-clear-source', { body: JSON.stringify(source, null, 2), contentType: 'application/json' })
  439 |   expect(source.status).toBe('QUEUED')
  440 |   expect(source.goal_config).toEqual({ p95_latency_ms: 1000 })
  441 |   const openCopy = async () => {
  442 |     await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  443 |     await page.reload()
  444 |     await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  445 |     const dialog = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
> 446 |     await expect(dialog.getByLabel('Elastic log capture', { exact: true })).toHaveValue(idOf(elk))
      |                                                                             ^ Error: expect(locator).toHaveValue(expected) failed
  447 |     await expect(dialog.getByLabel('Dynatrace SaaS capture', { exact: true })).toHaveValue(idOf(dt))
  448 |     await expect(dialog.getByRole('button', { name: 'Create Revised Copy', exact: true })).toBeEnabled()
  449 |     return dialog
  450 |   }
  451 |   const preservedEditor = await openCopy()
  452 |   await preservedEditor.getByLabel('Scenario Name', { exact: true }).fill('Capture rename only')
  453 |   const preservedSaved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  454 |   await preservedEditor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  455 |   const preservedResponse = await preservedSaved
  456 |   expect(preservedResponse.ok(), await preservedResponse.text()).toBeTruthy()
  457 |   const preservedCreated = await preservedResponse.json()
  458 |   const preservedId = idOf(preservedCreated); trackRun(preservedId)
  459 |   const preservedWire = preservedResponse.request().postDataJSON()
  460 |   await testInfo.attach('capture-rename-submission', { body: JSON.stringify({ submitted: preservedWire, created: preservedCreated }, null, 2), contentType: 'application/json' })
  461 |   expect(preservedWire).toMatchObject({ elk_capture: { connection_id: idOf(elk) }, dynatrace_capture: { connection_id: idOf(dt) }, goal_config: { p95_latency_ms: 1000 } })
  462 |   expect(preservedWire).not.toHaveProperty('sourceGoalConfigPresent')
  463 |   expect(preservedWire).not.toHaveProperty('revisionContext')
  464 |   await expect(page).toHaveURL(new RegExp(`/command-center/${preservedId}`))
  465 |   await page.reload()
  466 |   const preserved = await json(await request.get(`/api/v1/tests/${preservedId}`))
  467 |   await testInfo.attach('capture-rename-reopened', { body: JSON.stringify(preserved, null, 2), contentType: 'application/json' })
  468 |   expect(preserved.elk_capture).toEqual(source.elk_capture)
  469 |   expect(preserved.dynatrace_capture).toEqual(source.dynatrace_capture)
  470 |   expect(preserved.goal_config).toEqual(source.goal_config)
  471 |   expect(preserved.status).toBe('QUEUED')
  472 | 
  473 |   const clearedEditor = await openCopy()
  474 |   await clearedEditor.getByLabel('Scenario Name', { exact: true }).fill('Explicit goal and capture clear')
  475 |   await clearedEditor.getByLabel('P95 Latency Ceiling (ms)', { exact: true }).fill('')
  476 |   await clearedEditor.getByRole('button', { name: 'VUser-Driven', exact: true }).click()
  477 |   await clearedEditor.getByLabel('Elastic log capture', { exact: true }).selectOption('')
  478 |   await clearedEditor.getByLabel('Dynatrace SaaS capture', { exact: true }).selectOption('')
  479 |   const clearedSaved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  480 |   await clearedEditor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  481 |   const clearedResponse = await clearedSaved
  482 |   expect(clearedResponse.ok(), await clearedResponse.text()).toBeTruthy()
  483 |   const clearedCreated = await clearedResponse.json()
  484 |   const clearedId = idOf(clearedCreated); trackRun(clearedId)
  485 |   const clearedWire = clearedResponse.request().postDataJSON()
  486 |   await testInfo.attach('capture-goal-clear-submission', { body: JSON.stringify({ submitted: clearedWire, created: clearedCreated }, null, 2), contentType: 'application/json' })
  487 |   expect(clearedWire.goal_config).toEqual({})
  488 |   expect(clearedWire).not.toHaveProperty('elk_capture')
  489 |   expect(clearedWire).not.toHaveProperty('dynatrace_capture')
  490 |   await expect(page).toHaveURL(new RegExp(`/command-center/${clearedId}`))
  491 |   await page.reload()
  492 |   const cleared = await json(await request.get(`/api/v1/tests/${clearedId}`))
  493 |   const reopenedSource = await json(await request.get(`/api/v1/tests/${sourceId}`))
  494 |   await testInfo.attach('capture-goal-clear-reopened', { body: JSON.stringify({ cleared, source: reopenedSource }, null, 2), contentType: 'application/json' })
  495 |   expect(cleared.status).toBe('QUEUED')
  496 |   expect(cleared.scenario_mode).toBe('vuser_driven')
  497 |   expect(cleared.goal_config).toEqual({})
  498 |   expect(cleared).not.toHaveProperty('elk_capture')
  499 |   expect(cleared).not.toHaveProperty('dynatrace_capture')
  500 |   expect(reopenedSource).toEqual(source)
  501 |   expect(target.count()).toBe(0)
  502 | })
  503 | 
  504 | test('queued-copy preserves iteration completion and explicitly clears source delay and root runtime', async ({ page, target, trackRun }, testInfo) => {
  505 |   test.setTimeout(90_000)
  506 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  507 |   const request = page.context().request
  508 |   const workspace = await createWorkspace(request)
  509 |   const script = await seedScript(request, workspace, 'Completion_Clear_HTTP', target.url)
  510 |   const create = async (body: Record<string, unknown>) => {
  511 |     const run = await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, ...body } }))
  512 |     trackRun(idOf(run))
  513 |     return json(await request.get(`/api/v1/tests/${idOf(run)}`))
  514 |   }
  515 |   const source = await create({ name: 'Iteration source', termination_mode: 'iterations', duration: '', groups: [{ name: 'orders', script_id: script, vusers: 1, iterations: 3 }] })
  516 |   await page.goto(`/#/command-center/${idOf(source)}?${scopeQuery(workspace)}`)
  517 |   await page.reload()
  518 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  519 |   let editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  520 |   await expect(editor).toContainText('All per-user iterations')
  521 |   await expect(editor.getByLabel('Steady State Duration', { exact: true })).toBeDisabled()
  522 |   await expect(editor.getByLabel('Steady State Duration', { exact: true })).toHaveValue('')
  523 |   await editor.getByLabel('Scenario Name', { exact: true }).fill('Iteration renamed copy')
  524 |   let saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests')
  525 |   await editor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  526 |   let response = await saved
  527 |   expect(response.ok(), await response.text()).toBeTruthy()
  528 |   let created = await response.json()
  529 |   let copyId = idOf(created); trackRun(copyId)
  530 |   let submitted = response.request().postDataJSON()
  531 |   expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', groups: [expect.objectContaining({ iterations: 3 })] })
  532 |   for (const absent of ['start_delay', 'runtime_settings', 'goal_config']) expect(submitted).not.toHaveProperty(absent)
  533 |   await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  534 |   await page.reload()
  535 |   let reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  536 |   expect(reopened).toMatchObject({ termination_mode: 'iterations', duration: '', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  537 |   expect(await json(await request.get(`/api/v1/tests/${idOf(source)}`))).toEqual(source)
  538 |   await testInfo.attach('iteration-copy-transport-reopened', { body: JSON.stringify({ source, submitted, created, reopened }, null, 2), contentType: 'application/json' })
  539 | 
  540 |   const iterationDelayed = await create({ name: 'Delayed iteration source', termination_mode: 'iterations', duration: '', start_delay: '10s', groups: [{ name: 'orders', script_id: script, vusers: 1, iterations: 3 }] })
  541 |   await page.goto(`/#/command-center/${idOf(iterationDelayed)}?${scopeQuery(workspace)}`)
  542 |   await page.reload()
  543 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  544 |   editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  545 |   await expect(editor.getByLabel('Steady State Duration', { exact: true })).toBeDisabled()
  546 |   await expect(editor.getByLabel('Global Start Delay', { exact: true })).toHaveValue('10')
```