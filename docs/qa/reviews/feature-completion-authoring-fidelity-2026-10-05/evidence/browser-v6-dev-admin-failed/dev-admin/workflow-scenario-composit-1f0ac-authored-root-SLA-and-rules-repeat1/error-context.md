# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-scenario-composition.spec.ts >> changing a saved workload shape retains the authored root SLA and rules
- Location: e2e/workflow-scenario-composition.spec.ts:143:1

# Error details

```
TimeoutError: locator.fill: Timeout 15000ms exceeded.
Call log:
  - waiting for getByLabel('Ending load level', { exact: true })

```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. e2e-1791232741955-411834 domain / e2e-1791232741955-411834 project / e2e-1791232741955-411834 application / e2e-1791232741955-411834 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: e2e-1791232741955-411834 domain / e2e-1791232741955-411834 project / e2e-1791232741955-411834 application / e2e-1791232741955-411834 1.0
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
    - main "Scenario Builder" [ref=e125]:
      - status [ref=e126]: Scenario Builder
      - generic "Dashboard controls" [ref=e127]:
        - heading "Scenario Builder" [level=1] [ref=e129]
        - group "Scenario page view" [ref=e131]:
          - button "Scenario Library" [ref=e132] [cursor=pointer]
          - button "Scenario Builder" [pressed] [ref=e133] [cursor=pointer]
          - button "Execution Plans" [ref=e134] [cursor=pointer]
        - generic [ref=e135]:
          - link "Help for this page" [ref=e136] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=scenarios"
          - generic "Deployment identity" [ref=e137]:
            - generic [ref=e138]: Browser · Server
          - combobox "Dashboard color theme" [ref=e141]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - group [ref=e143]:
        - generic [ref=e144]:
          - paragraph [ref=e146]: Shape guardrail source
          - generic [ref=e147]:
            - generic [ref=e148]: Editing · Unsaved changes
            - group [ref=e149]:
              - generic "Readiness" [ref=e150] [cursor=pointer]
        - paragraph [ref=e151]: Checkmarks indicate valid fields, including defaults; they do not indicate an operator review.
        - generic "Scenario builder steps" [ref=e152]:
          - button "Start valid" [ref=e153] [cursor=pointer]:
            - generic [ref=e154]: ✓
            - generic [ref=e155]: Start
          - button "Model & Shape valid" [ref=e156] [cursor=pointer]:
            - generic [ref=e157]: ✓
            - generic [ref=e158]: Model & Shape
          - button "Run Settings valid" [active] [ref=e159] [cursor=pointer]:
            - generic [ref=e160]: ✓
            - generic [ref=e161]: Run Settings
          - button "Data" [ref=e162] [cursor=pointer]:
            - generic [ref=e163]: "4"
            - generic [ref=e164]: Data
          - button "Review" [ref=e165] [cursor=pointer]:
            - generic [ref=e166]: "5"
            - generic [ref=e167]: Review
        - generic [ref=e168]:
          - 'region "Scenario step: run" [ref=e169]':
            - generic [ref=e170]:
              - generic [ref=e171]:
                - heading "Run Settings" [level=4] [ref=e172]
                - paragraph [ref=e173]: Configure duration, group load, stage modeling, protection, SLAs, browser defaults, and generator assignment.
              - note [ref=e174]:
                - generic [ref=e175]:
                  - generic [ref=e176]: Stepped Ramp
                  - generic [ref=e177]: ramp
                - paragraph [ref=e178]: Measure performance at equal-duration load levels. Configure the range, increment, scope, and protection settings on Run Settings.
                - paragraph [ref=e179]: Stage lists round-trip through API/storage, derive compatible runtime ramps, and appear in archive load-model goal evidence.
              - tablist "Run settings sections" [ref=e180]:
                - tab "Scenario Settings" [selected] [ref=e181] [cursor=pointer]
                - tab "Browser/Script Runtime" [ref=e182] [cursor=pointer]
                - tab "Controller/Generator" [ref=e183] [cursor=pointer]
              - tabpanel "Scenario Settings" [ref=e184]:
                - tablist "Scenario settings sections" [ref=e185]:
                  - tab "Target Load" [selected] [ref=e186] [cursor=pointer]
                  - tab "Ramp Pattern" [ref=e187] [cursor=pointer]
                  - tab "Run Phases" [ref=e188] [cursor=pointer]
                  - tab "Advanced Settings" [ref=e189] [cursor=pointer]
                - tabpanel "Target Load" [ref=e190]:
                  - generic [ref=e191]:
                    - generic [ref=e192]: Guided worksheet
                    - heading "Stepped Ramp Setup" [level=3] [ref=e193]
                    - paragraph [ref=e194]: Define equal-duration load levels to compare throughput, latency, and errors as demand increases.
                  - region "Load range" [ref=e195]:
                    - generic [ref=e196]: "1"
                    - generic [ref=e197]:
                      - heading "Load range" [level=4] [ref=e198]
                      - paragraph [ref=e199]: Set the initial and final load levels. Run Phases controls total duration.
                      - generic [ref=e200]:
                        - generic [ref=e201]:
                          - generic [ref=e202]: Starting load level
                          - generic [ref=e203]:
                            - spinbutton "Starting load level VUsers" [ref=e204]: "0"
                            - generic [ref=e205]: VUsers
                        - generic [ref=e206]:
                          - generic [ref=e207]: Ending load level
                          - generic [ref=e208]:
                            - spinbutton "Ending load level VUsers" [ref=e209]: "60"
                            - generic [ref=e210]: VUsers
                  - paragraph [ref=e211]: Changes apply immediately. Review the total demand and duration in the preview, then continue.
          - complementary [ref=e212]:
            - generic [ref=e213]:
              - generic [ref=e214]:
                - generic [ref=e215]:
                  - heading "Model Preview" [level=4] [ref=e216]
                  - paragraph [ref=e217]: Planned VUsers over time by script group, calculated from the current wizard draft.
                - generic [ref=e218]:
                  - generic [ref=e219]:
                    - strong [ref=e220]: "60"
                    - text: peak VUsers
                  - generic [ref=e221]:
                    - strong [ref=e222]: 1h0m
                    - text: duration
                  - generic [ref=e223]:
                    - strong [ref=e224]: "1"
                    - text: groups
                  - generic [ref=e225]: Stepped Ramp
                  - generic [ref=e226]: No data pool
                  - generic [ref=e227]: Not validated
              - application [ref=e231]:
                - generic [ref=e245]:
                  - generic [ref=e246]:
                    - generic [ref=e248]: 2.5m
                    - generic [ref=e250]: 13m
                    - generic [ref=e252]: 23m
                    - generic [ref=e254]: 33m
                    - generic [ref=e256]: 43m
                    - generic [ref=e258]: 53m
                    - generic [ref=e260]: 1h
                  - generic [ref=e261]: Elapsed time
                  - generic [ref=e262]:
                    - generic [ref=e264]: "0"
                    - generic [ref=e266]: "15"
                    - generic [ref=e268]: "30"
                    - generic [ref=e270]: "45"
                    - generic [ref=e272]: "60"
                  - generic [ref=e273]: VUsers
              - generic [ref=e274]:
                - generic [ref=e275]: orders
                - generic [ref=e277]: Total VUsers
            - generic [ref=e279]:
              - generic [ref=e280]:
                - strong [ref=e281]: Load levels
                - generic [ref=e282]: Live preview
              - table [ref=e283]:
                - rowgroup [ref=e284]:
                  - row "Step Time range VUsers Duration" [ref=e285]:
                    - columnheader "Step" [ref=e286]
                    - columnheader "Time range" [ref=e287]
                    - columnheader "VUsers" [ref=e288]
                    - columnheader "Duration" [ref=e289]
                - rowgroup [ref=e290]:
                  - row "1 0s–10m 10 10m" [ref=e291]:
                    - cell "1" [ref=e292]
                    - cell "0s–10m" [ref=e293]
                    - cell "10" [ref=e294]
                    - cell "10m" [ref=e295]
                  - row "2 10m–20m 20 10m" [ref=e296]:
                    - cell "2" [ref=e297]
                    - cell "10m–20m" [ref=e298]
                    - cell "20" [ref=e299]
                    - cell "10m" [ref=e300]
                  - row "3 20m–30m 30 10m" [ref=e301]:
                    - cell "3" [ref=e302]
                    - cell "20m–30m" [ref=e303]
                    - cell "30" [ref=e304]
                    - cell "10m" [ref=e305]
                  - row "4 30m–40m 40 10m" [ref=e306]:
                    - cell "4" [ref=e307]
                    - cell "30m–40m" [ref=e308]
                    - cell "40" [ref=e309]
                    - cell "10m" [ref=e310]
                  - row "5 40m–50m 50 10m" [ref=e311]:
                    - cell "5" [ref=e312]
                    - cell "40m–50m" [ref=e313]
                    - cell "50" [ref=e314]
                    - cell "10m" [ref=e315]
                  - row "6 50m–1h0m 60 10m" [ref=e316]:
                    - cell "6" [ref=e317]
                    - cell "50m–1h0m" [ref=e318]
                    - cell "60" [ref=e319]
                    - cell "10m" [ref=e320]
              - paragraph [ref=e321]: Metrics are tagged with each active load level for live and archived comparison.
        - generic [ref=e322]:
          - generic [ref=e323]: Incomplete or blocked drafts are saved in this browser. Configured scenarios are saved to the project; neither launches a run.
          - button "Cancel" [ref=e324] [cursor=pointer]
          - button "Back" [ref=e325] [cursor=pointer]
          - button "Next" [ref=e326] [cursor=pointer]
          - button "Save draft" [ref=e327] [cursor=pointer]
  - generic [ref=e328]: "15"
```

# Test source

```ts
  59  |   await expect(preview.getByLabel('Live group summary').locator('.scenario-composition-summary-row').nth(1)).toContainText('1s start')
  60  | 
  61  |   await page.setViewportSize({ width: 1440, height: 1000 })
  62  |   await page.screenshot({ path: testInfo.outputPath('scenario-composition-desktop.png'), fullPage: true })
  63  |   await page.setViewportSize({ width: 600, height: 900 })
  64  |   await expect(control).toBeVisible()
  65  |   await expect(preview).toBeVisible()
  66  |   const narrowTabTops = await sections.getByRole('tab').evaluateAll(tabs => tabs.map(tab => Math.round(tab.getBoundingClientRect().top)))
  67  |   expect(new Set(narrowTabTops).size).toBe(1)
  68  |   expect(await control.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
  69  |   await page.screenshot({ path: testInfo.outputPath('scenario-composition-narrow.png'), fullPage: true })
  70  | })
  71  | 
  72  | test('guided stepped ramp keeps its pattern editor within the four scenario tabs', async ({ page }) => {
  73  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  74  |   const workspace = await createWorkspace(page.context().request)
  75  |   await seedScript(page.context().request, workspace, 'Stepped_HTTP')
  76  |   await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
  77  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  78  |   await page.getByPlaceholder('Scenario name').fill('Stepped tab layout')
  79  |   await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  80  |   await page.getByRole('row').filter({ hasText: 'Stepped_HTTP' }).getByRole('button', { name: 'Use for Group 1' }).click()
  81  |   const steps = page.getByLabel('Scenario builder steps')
  82  |   await steps.getByRole('button', { name: /^Model & Shape/ }).click()
  83  |   await page.getByRole('button', { name: /^Stepped Ramp/ }).click()
  84  |   await page.getByRole('button', { name: 'Use Stepped Ramp', exact: true }).click()
  85  |   await steps.getByRole('button', { name: /^Run Settings/ }).click()
  86  | 
  87  |   const tabs = page.getByRole('tablist', { name: 'Scenario settings sections' })
  88  |   await expect(tabs.getByRole('tab')).toHaveText(['Target Load', 'Ramp Pattern', 'Run Phases', 'Advanced Settings'])
  89  |   await expect(page.getByLabel('Ending load level')).toBeVisible()
  90  |   await tabs.getByRole('tab', { name: 'Ramp Pattern' }).click()
  91  |   await expect(page.getByRole('heading', { name: 'Create equal steps' })).toBeVisible()
  92  |   await expect(page.getByRole('heading', { name: 'Apply the model' })).toBeVisible()
  93  |   await tabs.getByRole('tab', { name: 'Run Phases' }).click()
  94  |   await expect(page.getByLabel('Duration', { exact: true })).toBeVisible()
  95  |   await tabs.getByRole('tab', { name: 'Advanced Settings' }).click()
  96  |   await expect(page.getByRole('heading', { name: 'Protect the Run' })).toBeVisible()
  97  |   await expect(page.getByRole('heading', { name: 'Transaction response-time SLA' })).toBeVisible()
  98  |   await expect(page.getByText('Advanced run protection and assurance')).toHaveCount(0)
  99  | })
  100 | 
  101 | 
  102 | test('scenario rename and reopen retain Results, WebSocket, named shape and group timing intent', async ({ page }, testInfo) => {
  103 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  104 |   const request = page.context().request
  105 |   const workspace = await createWorkspace(request)
  106 |   const script = await seedScript(request, workspace, 'Fidelity_HTTP')
  107 |   const results = { csv_exports: [{ id: 'calls', file_name: 'calls.csv', action_name: 'orders', columns: [{ name: 'latency', metric: 'latency_ms' }] }], property_analytics: [{ name: 'category', extraction: 'category', values: ['one', 'two'] }] }
  108 |   const rootRuntime = { results: { message_detail: { mode: 'off', include_resources: false, sample_rate: 0, max_items: 0 } }, websocket: { callback_interval_ms: 0, compression: false, reconnect: false } }
  109 |   const goal = { transaction_response_time_sla_ms: 0.001, workload_shape_config: { closed_vuser: { scope: 'scenario', scenario: { peak_vusers: 4, ramp_up: '10s', sustain: '50s', ramp_down_enabled: false, ramp_down: '0s' } } } }
  110 |   const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: {
  111 |     project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Fidelity original', duration: '1m', scenario_mode: 'vuser_driven', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: rootRuntime,
  112 |     groups: [
  113 |       { name: 'orders', script_id: script, vusers: 2, role: 'load', ramp_up: '3s', ramp_down: '4s', start_offset: '1s', initialization: 'at_once', transaction_response_time_sla_ms: 1250, runtime_settings: { results, websocket: {} } },
  114 |       { name: 'staged', script_id: script, vusers: 2, role: 'load', start_offset: '2s', initialization: 'at_runtime', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } },
  115 |     ],
  116 |   } })))
  117 |   const original = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  118 |   await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  119 |   await page.reload()
  120 |   await page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Fidelity original' }).getByRole('button', { name: 'Edit', exact: true }).click()
  121 |   await page.getByPlaceholder('Scenario name').fill('Fidelity renamed only')
  122 |   await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  123 |   const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
  124 |   await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  125 |   const response = await saved
  126 |   expect(response.ok(), await response.text()).toBeTruthy()
  127 |   const sent = response.request().postDataJSON()
  128 |   expect(sent).toMatchObject({ name: 'Fidelity renamed only', workload_shape_id: 'closed-vuser', goal_config: goal, runtime_settings: { results: { message_detail: { mode: 'off', include_resources: false } }, websocket: {} } })
  129 |   expect(sent.groups).toMatchObject([{ start_offset: '1s', ramp_up: '3s', ramp_down: '4s', transaction_response_time_sla_ms: 1250, runtime_settings: { results, websocket: {} } }, { start_offset: '2s', stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], transaction_response_time_sla_ms: 1500, runtime_settings: { results: {}, websocket: {} } }])
  130 |   const stored = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  131 |   expect(stored.runtime_settings).toEqual(original.runtime_settings)
  132 |   expect(stored.scripts).toEqual(original.scripts)
  133 |   expect(stored.goal_config).toEqual(original.goal_config)
  134 |   expect(stored.workload_shape_id).toBe('closed-vuser')
  135 |   await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  136 |   await page.reload()
  137 |   await page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Fidelity renamed only' }).getByRole('button', { name: 'Edit', exact: true }).click()
  138 |   await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Fidelity renamed only')
  139 |   await testInfo.attach('canonical-scenario-fidelity', { body: JSON.stringify({ original, submitted: sent, reopened: stored }, null, 2), contentType: 'application/json' })
  140 | })
  141 | 
  142 | 
  143 | test('changing a saved workload shape retains the authored root SLA and rules', async ({ page, target }, testInfo) => {
  144 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  145 |   const request = page.context().request
  146 |   const workspace = await createWorkspace(request)
  147 |   const script = await seedScript(request, workspace, 'Shape_Guardrails_HTTP', target.url)
  148 |   const goal = { target_average_tps: 25, min_vusers: 1, max_vusers: 30, p95_latency_ms: 2300, max_error_pct: 0, max_error_count: 3, max_avg_server_processing_ms: 400, max_avg_transfer_ms: 125, max_lcp_ms: 2500, max_inp_ms: 200, max_cls: 0.1, max_throttled_requests: 0, max_end_to_end_latency_ms: 500, max_partition_skew_pct: 0, max_redelivery_rate_pct: 0, max_dead_letter_rate_pct: 0, stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '7s', transaction_response_time_sla_ms: 0.001, transaction_rules: [{ transaction: 'orders', metric: 'p95_duration_ms', comparator: 'lte', threshold: 1250 }], monitor_rules: [], workload_shape_config: { goal_driven: { groups: { group_0: { target_tps: 25, art: { basis: 'manual', art_ms: 400 } } } } } }
  149 |   const scenario = idOf(await json(await request.post('/api/v1/scenarios', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, name: 'Shape guardrail source', duration: '1m', scenario_mode: 'goal_oriented', workload_shape_id: 'open-arrival-rate', goal_config: goal, groups: [{ name: 'orders', script_id: script, vusers: 2, initialization: 'at_once', transaction_response_time_sla_ms: 1250 }] } })))
  150 |   const original = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  151 |   await testInfo.attach('original-shape-guardrails', { body: JSON.stringify(original, null, 2), contentType: 'application/json' })
  152 |   await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  153 |   await page.reload()
  154 |   await page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Shape guardrail source' }).getByRole('button', { name: 'Edit', exact: true }).click()
  155 |   await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Model & Shape/ }).click()
  156 |   await page.getByRole('button', { name: /^Stepped Ramp/ }).click()
  157 |   await page.getByRole('button', { name: 'Use Stepped Ramp', exact: true }).click()
  158 |   await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run Settings/ }).click()
> 159 |   await page.getByLabel('Ending load level', { exact: true }).fill('14')
      |                                                               ^ TimeoutError: locator.fill: Timeout 15000ms exceeded.
  160 |   await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Review/ }).click()
  161 |   const saved = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname === `/api/v1/scenarios/${scenario}`)
  162 |   await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  163 |   const response = await saved
  164 |   const submitted = response.request().postDataJSON()
  165 |   await testInfo.attach('shape-guardrail-save', { body: JSON.stringify({ submitted, status: response.status(), response: await response.text() }, null, 2), contentType: 'application/json' })
  166 |   expect(response.ok()).toBeTruthy()
  167 |   const reopened = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  168 |   await testInfo.attach('reopened-shape-guardrails', { body: JSON.stringify(reopened, null, 2), contentType: 'application/json' })
  169 |   expect(submitted.workload_shape_id).toBe('stepped-ramp')
  170 |   const replacedOrCanonicallyEmpty = new Set(['target_average_tps', 'min_vusers', 'max_vusers', 'workload_shape_config', 'monitor_rules'])
  171 |   const guardrails = Object.fromEntries(Object.entries(goal).filter(([key]) => !replacedOrCanonicallyEmpty.has(key)))
  172 |   expect(submitted.goal_config).toMatchObject(guardrails)
  173 |   expect(reopened.goal_config).toMatchObject(guardrails)
  174 |   // Go's canonical DTO omits zero-length rule arrays; their empty semantics
  175 |   // are retained, while explicit nonempty-to-empty template submission is unit-tested.
  176 |   expect(submitted.goal_config.monitor_rules ?? []).toEqual([])
  177 |   expect(reopened.goal_config).not.toHaveProperty('monitor_rules')
  178 |   for (const field of ['target_average_tps', 'min_vusers', 'max_vusers']) expect(reopened.goal_config).not.toHaveProperty(field)
  179 |   expect(reopened.goal_config).not.toHaveProperty('early_stop')
  180 |   expect(reopened.goal_config).not.toHaveProperty('workload_shape_config.goal_driven')
  181 |   expect(reopened.goal_config).toHaveProperty('workload_shape_config.stepped_ramp')
  182 |   expect(reopened.scripts).toMatchObject([{ script_id: script, vusers: 14, initialization: 'at_once', transaction_response_time_sla_ms: 1250 }])
  183 |   await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  184 |   await page.reload()
  185 |   await page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Shape guardrail source' }).getByRole('button', { name: 'Edit', exact: true }).click()
  186 |   await page.getByLabel('Scenario builder steps').getByRole('button', { name: /^Run Settings/ }).click()
  187 |   await expect(page.getByLabel('Ending load level', { exact: true })).toHaveValue('14')
  188 |   expect(target.count()).toBe(0)
  189 | })
  190 | 
```