# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-new-test.spec.ts >> queued-copy preserves iteration completion and explicitly clears source delay and root runtime
- Location: e2e/workflow-new-test.spec.ts:504:1

# Error details

```
TimeoutError: page.waitForResponse: Timeout 15000ms exceeded while waiting for event "response"
```

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('dialog', { name: 'Create Revised Queued Run', exact: true }).getByRole('button', { name: 'Create Revised Copy', exact: true })

```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. e2e-1791232697227-117646 domain / e2e-1791232697227-117646 project / e2e-1791232697227-117646 application / e2e-1791232697227-117646 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: e2e-1791232697227-117646 domain / e2e-1791232697227-117646 project / e2e-1791232697227-117646 application / e2e-1791232697227-117646 1.0
        - generic [ref=e18]: ⌄
      - navigation "Primary navigation" [ref=e19]:
        - button "New Test" [ref=e21] [cursor=pointer]:
          - img [ref=e23]
          - generic [ref=e26]: New Test
        - generic [ref=e27]:
          - button "Dashboard" [ref=e29] [cursor=pointer]:
            - img [ref=e31]
            - generic [ref=e36]: Dashboard
          - button "Command Center" [ref=e38] [cursor=pointer]:
            - img [ref=e40]
            - generic [ref=e44]: Command Center
          - button "Results" [ref=e46] [cursor=pointer]:
            - img [ref=e48]
            - generic [ref=e49]: Results
          - button "Monitoring" [ref=e51] [cursor=pointer]:
            - img [ref=e53]
            - generic [ref=e55]: Monitoring
          - button "Activity" [ref=e57] [cursor=pointer]:
            - img [ref=e59]
            - generic [ref=e61]: Activity
        - generic [ref=e62]:
          - button "Scripts" [ref=e64] [cursor=pointer]:
            - img [ref=e66]
            - generic [ref=e69]: Scripts
          - button "Scenarios" [ref=e71] [cursor=pointer]:
            - img [ref=e73]
            - generic [ref=e75]: Scenarios
        - generic [ref=e76]:
          - button "Repository" [ref=e78] [cursor=pointer]:
            - img [ref=e80]
            - generic [ref=e83]: Repository
          - button "Access" [ref=e85] [cursor=pointer]:
            - img [ref=e87]
            - generic [ref=e91]: Access
          - button "Settings" [ref=e93] [cursor=pointer]:
            - img [ref=e95]
            - generic [ref=e98]: Settings
        - button "Help & documentation" [ref=e101] [cursor=pointer]:
          - img [ref=e103]
          - generic [ref=e106]: Help & documentation
      - generic [ref=e107]:
        - generic [ref=e108]:
          - 'button "Preview permissions. Current mode: admin" [ref=e110] [cursor=pointer]':
            - generic [ref=e111]: DEV
            - generic [ref=e112]: Preview permissions
          - generic [ref=e113]:
            - generic "Live" [ref=e114]:
              - generic [ref=e116]: Live
            - 'button "Build info: local · dev" [ref=e118] [cursor=pointer]':
              - generic [ref=e120]: Version Info
        - button "Collapse sidebar" [expanded] [ref=e122] [cursor=pointer]:
          - img [ref=e123]
    - main "Run Results" [active] [ref=e125]:
      - status [ref=e126]: Run Results
      - generic "Dashboard controls" [ref=e127]:
        - heading "Run Results" [level=1] [ref=e129]
        - generic [ref=e130]:
          - link "Help for this page" [ref=e131] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e132]:
            - generic [ref=e133]: Browser · Server
          - combobox "Dashboard color theme" [ref=e136]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e137]:
        - generic [ref=e138]:
          - generic [ref=e139]:
            - paragraph [ref=e140]: Run results · LOC-RUN-20261005-014
            - heading "Iteration source" [level=1] [ref=e141]
            - paragraph [ref=e142]:
              - generic [ref=e143]: ABORTED
              - text: · Configured 1 VUsers for Not recorded · Lifecycle elapsed Not recorded
          - generic [ref=e144]:
            - button "Compare" [disabled] [ref=e145]
            - button "Export report" [disabled] [ref=e146]
            - button "Test notes" [ref=e147] [cursor=pointer]
        - region "Execution outcome" [ref=e148]:
          - heading "Execution aborted" [level=2] [ref=e149]
          - paragraph [ref=e150]: test cancelled before start
          - paragraph [ref=e151]: Execution status is separate from sampled transaction success and performance acceptance.
        - region "Evidence availability" [ref=e152]:
          - generic [ref=e153]:
            - heading "Saved archive unavailable" [level=2] [ref=e154]
            - paragraph [ref=e155]: Retained controller records are available below. Archive publication progress is unknown; these records do not establish complete workload or performance evidence.
          - button "Check again" [ref=e156] [cursor=pointer]
        - group [ref=e157]:
          - generic "Run timeline annotations" [ref=e158] [cursor=pointer]
        - tablist "Result sections" [ref=e159]:
          - tab "Summary" [ref=e160] [cursor=pointer]
          - tab "Performance" [ref=e161] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e162] [cursor=pointer]
          - tab "Diagnostics" [ref=e163] [cursor=pointer]
          - tab "Evidence" [ref=e164] [cursor=pointer]
          - tab "Execution" [selected] [ref=e165] [cursor=pointer]
        - tabpanel "Execution" [ref=e166]:
          - paragraph [ref=e167]: Read-only execution record. Configured values come from a mutable retained record; the original plan is unverified. Execution completion and acceptance are assessed separately.
          - generic "Execution facts" [ref=e168]:
            - generic [ref=e169]:
              - term [ref=e170]: Configured duration
              - definition [ref=e171]: Not recorded
            - generic [ref=e172]:
              - term [ref=e173]: Lifecycle elapsed
              - definition [ref=e174]: Not recorded
            - generic [ref=e175]:
              - term [ref=e176]: Configured VUsers
              - definition [ref=e177]: "1"
            - generic [ref=e178]:
              - term [ref=e179]: Observed peak VUsers
              - definition [ref=e180]: Not recorded
          - region "Execution load timeline" [ref=e181]:
            - heading "Configured and observed load" [level=2] [ref=e182]
            - paragraph [ref=e183]: "Configured load: 1 VUsers. This reference is not a ramp schedule. Observed samples cover only the captured intervals."
            - paragraph [ref=e184]: No interval history was retained. Planned workload completion cannot be inferred from the final controller sample.
          - region "Execution groups" [ref=e185]:
            - heading "Groups and configuration" [level=2] [ref=e186]
            - paragraph [ref=e187]: Configuration from the retained test record. It may reflect later changes; the original plan is unverified.
            - table [ref=e189]:
              - rowgroup [ref=e190]:
                - row "Group Script Configured VUsers Configuration details" [ref=e191]:
                  - columnheader "Group" [ref=e192]
                  - columnheader "Script" [ref=e193]
                  - columnheader "Configured VUsers" [ref=e194]
                  - columnheader "Configuration details" [ref=e195]
              - rowgroup [ref=e196]:
                - row "orders Completion_Clear_HTTP 1" [ref=e197]:
                  - rowheader "orders" [ref=e198]
                  - cell "Completion_Clear_HTTP" [ref=e199]
                  - cell "1" [ref=e200]
                  - cell [ref=e201]:
                    - group [ref=e202]:
                      - generic "View configuration" [ref=e203] [cursor=pointer]
          - region "Execution events" [ref=e204]:
            - heading "Execution events" [level=2] [ref=e205]
            - generic [ref=e206]:
              - generic [ref=e207]:
                - term [ref=e208]: Created
                - definition [ref=e209]: 10/5/2026, 4:38:17 PM
              - generic [ref=e210]:
                - term [ref=e211]: Started
                - definition [ref=e212]: Not recorded
              - generic [ref=e213]:
                - term [ref=e214]: Ended
                - definition [ref=e215]: 10/5/2026, 4:38:32 PM
            - list [ref=e216]:
              - listitem [ref=e217]:
                - strong [ref=e218]: Run created
                - generic [ref=e219]: 10/5/2026, 4:38:17 PM · success
              - listitem [ref=e220]:
                - strong [ref=e221]: Abort requested
                - generic [ref=e222]: 10/5/2026, 4:38:32 PM · success
          - region "Telemetry budget policy" [ref=e223]:
            - heading "Telemetry budget policy" [level=3] [ref=e224]
            - paragraph [ref=e225]: No immutable policy receipt was retained for this evidence.
            - paragraph [ref=e226]: Policy availability describes retained settings. It does not establish synchronized clocks, wall-time coverage, exact loss totals, or a telemetry CI verdict. Missing intervals remain unknown.
          - group [ref=e227]:
            - generic "Configuration and infrastructure evidence" [ref=e228] [cursor=pointer]
  - generic [ref=e229]: "0"
```

# Test source

```ts
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
  446 |     await expect(dialog.getByLabel('Elastic log capture', { exact: true })).toHaveValue(idOf(elk))
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
> 525 |   await editor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
      |                                                                                  ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
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
  547 |   await editor.getByLabel('Scenario Name', { exact: true }).fill('Positive iteration delay copy')
  548 |   saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  549 |   await editor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  550 |   response = await saved
  551 |   expect(response.ok(), await response.text()).toBeTruthy()
  552 |   created = await response.json(); copyId = idOf(created); trackRun(copyId)
  553 |   submitted = response.request().postDataJSON()
  554 |   expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '10s', groups: [expect.objectContaining({ iterations: 3 })] })
  555 |   expect(submitted).not.toHaveProperty('goal_config')
  556 |   await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  557 |   await page.reload()
  558 |   const iterationInherited = await json(await request.get(`/api/v1/tests/${copyId}`))
  559 |   expect(iterationInherited).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '10s', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  560 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  561 |   editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  562 |   await editor.getByLabel('Scenario Name', { exact: true }).fill('Iteration delay cleared copy')
  563 |   await editor.getByLabel('Global Start Delay', { exact: true }).fill('')
  564 |   saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  565 |   await editor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  566 |   response = await saved
  567 |   expect(response.ok(), await response.text()).toBeTruthy()
  568 |   created = await response.json(); copyId = idOf(created); trackRun(copyId)
  569 |   submitted = response.request().postDataJSON()
  570 |   expect(submitted).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '0s', revision_source_test_id: idOf(iterationInherited), groups: [expect.objectContaining({ iterations: 3 })] })
  571 |   expect(submitted).not.toHaveProperty('goal_config')
  572 |   await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  573 |   await page.reload()
  574 |   reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  575 |   expect(reopened).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '0s', status: 'QUEUED', groups: [expect.objectContaining({ iterations: 3 })] })
  576 |   expect(await json(await request.get(`/api/v1/tests/${idOf(iterationDelayed)}`))).toEqual(iterationDelayed)
  577 |   expect(await json(await request.get(`/api/v1/tests/${idOf(iterationInherited)}`))).toEqual(iterationInherited)
  578 |   await testInfo.attach('positive-iteration-delay-copy-clear-reopened', { body: JSON.stringify({ source: iterationDelayed, positive_copy: iterationInherited, cleared_submitted: submitted, created, reopened }, null, 2), contentType: 'application/json' })
  579 | 
  580 |   const delaySource = await create({ name: 'Delay and runtime source', duration: '1m', start_delay: '10s', runtime_settings: { log_level: 'standard' }, groups: [{ name: 'orders', script_id: script, vusers: 1 }] })
  581 |   await page.goto(`/#/command-center/${idOf(delaySource)}?${scopeQuery(workspace)}`)
  582 |   await page.reload()
  583 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  584 |   editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  585 |   await editor.getByLabel('Scenario Name', { exact: true }).fill('Delay and root runtime cleared')
  586 |   await expect(editor.getByLabel('Global Start Delay', { exact: true })).toHaveValue('10')
  587 |   await editor.getByLabel('Global Start Delay', { exact: true }).fill('')
  588 |   await editor.getByRole('button', { name: 'Clear Log level override', exact: true }).click()
  589 |   saved = page.waitForResponse(result => result.request().method() === 'POST' && new URL(result.url()).pathname === '/api/v1/tests')
  590 |   await editor.getByRole('button', { name: 'Create Revised Copy', exact: true }).click()
  591 |   response = await saved
  592 |   expect(response.ok(), await response.text()).toBeTruthy()
  593 |   created = await response.json(); copyId = idOf(created); trackRun(copyId)
  594 |   submitted = response.request().postDataJSON()
  595 |   expect(submitted.start_delay).toBe('0s')
  596 |   expect(submitted.runtime_settings).toEqual({})
  597 |   for (const privateField of ['sourceStartDelayPresent', 'sourceRuntimeSettingsPresent', 'revisionContext']) expect(submitted).not.toHaveProperty(privateField)
  598 |   await expect(page).toHaveURL(new RegExp(`/command-center/${copyId}`))
  599 |   await page.reload()
  600 |   reopened = await json(await request.get(`/api/v1/tests/${copyId}`))
  601 |   expect(reopened.start_delay).toBe('0s')
  602 |   expect(reopened.runtime_settings).toEqual({})
  603 |   expect(await json(await request.get(`/api/v1/tests/${idOf(delaySource)}`))).toEqual(delaySource)
  604 |   await testInfo.attach('delay-runtime-clear-transport-reopened', { body: JSON.stringify({ source: delaySource, submitted, created, reopened }, null, 2), contentType: 'application/json' })
  605 |   expect(target.count()).toBe(0)
  606 | })
  607 | 
  608 | test('queued-copy joins a history-guard save to its actual submitted receipt and retains late edits', async ({ page, target, trackRun }, testInfo) => {
  609 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  610 |   const request = page.context().request
  611 |   const workspace = await createWorkspace(request)
  612 |   const script = await seedScript(request, workspace, 'Late_Copy_HTTP', target.url)
  613 |   const sourceId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Late draft source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  614 |   trackRun(sourceId)
  615 |   const source = await json(await request.get(`/api/v1/tests/${sourceId}`))
  616 |   await page.goto(`/#/command-center/${sourceId}?${scopeQuery(workspace)}`)
  617 |   await page.reload()
  618 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  619 |   const editor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  620 |   const name = editor.getByLabel('Scenario Name', { exact: true })
  621 |   await name.fill('Submitted snapshot')
  622 |   let release!: () => void
  623 |   const held = new Promise<void>(resolve => { release = resolve })
  624 |   let publish!: () => void
  625 |   const received = new Promise<void>(resolve => { publish = resolve })
```