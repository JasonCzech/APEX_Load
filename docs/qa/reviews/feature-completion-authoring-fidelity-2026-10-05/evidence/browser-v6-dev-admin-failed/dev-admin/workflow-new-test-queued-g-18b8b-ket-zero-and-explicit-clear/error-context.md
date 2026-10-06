# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-new-test.spec.ts >> queued group dialogs retain inherited Results siblings and WebSocket zero and explicit clear
- Location: e2e/workflow-new-test.spec.ts:747:1

# Error details

```
TimeoutError: locator.selectOption: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('dialog', { name: 'Runtime Settings — orders', exact: true }).getByLabel('Exchange capture', { exact: true })

```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. e2e-1791232620197-874910 domain / e2e-1791232620197-874910 project / e2e-1791232620197-874910 application / e2e-1791232620197-874910 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: e2e-1791232620197-874910 domain / e2e-1791232620197-874910 project / e2e-1791232620197-874910 application / e2e-1791232620197-874910 1.0
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
            - paragraph [ref=e140]: Run results · LOC-RUN-20261005-009
            - heading "Queued dialog fidelity" [level=1] [ref=e141]
            - paragraph [ref=e142]:
              - generic [ref=e143]: ABORTED
              - text: · Configured 2 VUsers for 1m · Lifecycle elapsed Not recorded
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
              - definition [ref=e171]: 1m
            - generic [ref=e172]:
              - term [ref=e173]: Lifecycle elapsed
              - definition [ref=e174]: Not recorded
            - generic [ref=e175]:
              - term [ref=e176]: Configured VUsers
              - definition [ref=e177]: "2"
            - generic [ref=e178]:
              - term [ref=e179]: Observed peak VUsers
              - definition [ref=e180]: Not recorded
          - region "Execution load timeline" [ref=e181]:
            - heading "Configured and observed load" [level=2] [ref=e182]
            - paragraph [ref=e183]: "Configured load: 2 VUsers. This reference is not a ramp schedule. Observed samples cover only the captured intervals."
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
                - row "orders Inherited_Fidelity_HTTP 1" [ref=e197]:
                  - rowheader "orders" [ref=e198]
                  - cell "Inherited_Fidelity_HTTP" [ref=e199]
                  - cell "1" [ref=e200]
                  - cell [ref=e201]:
                    - group [ref=e202]:
                      - generic "View configuration" [ref=e203] [cursor=pointer]
                - row "WS Inherited_Fidelity_WS 1" [ref=e204]:
                  - rowheader "WS" [ref=e205]
                  - cell "Inherited_Fidelity_WS" [ref=e206]
                  - cell "1" [ref=e207]
                  - cell [ref=e208]:
                    - group [ref=e209]:
                      - generic "View configuration" [ref=e210] [cursor=pointer]
          - region "Execution events" [ref=e211]:
            - heading "Execution events" [level=2] [ref=e212]
            - generic [ref=e213]:
              - generic [ref=e214]:
                - term [ref=e215]: Created
                - definition [ref=e216]: 10/5/2026, 4:37:00 PM
              - generic [ref=e217]:
                - term [ref=e218]: Started
                - definition [ref=e219]: Not recorded
              - generic [ref=e220]:
                - term [ref=e221]: Ended
                - definition [ref=e222]: 10/5/2026, 4:37:15 PM
            - list [ref=e223]:
              - listitem [ref=e224]:
                - strong [ref=e225]: Run created
                - generic [ref=e226]: 10/5/2026, 4:37:00 PM · success
              - listitem [ref=e227]:
                - strong [ref=e228]: Abort requested
                - generic [ref=e229]: 10/5/2026, 4:37:15 PM · success
          - region "Telemetry budget policy" [ref=e230]:
            - heading "Telemetry budget policy" [level=3] [ref=e231]
            - paragraph [ref=e232]: No immutable policy receipt was retained for this evidence.
            - paragraph [ref=e233]: Policy availability describes retained settings. It does not establish synchronized clocks, wall-time coverage, exact loss totals, or a telemetry CI verdict. Missing intervals remain unknown.
          - group [ref=e234]:
            - generic "Configuration and infrastructure evidence" [ref=e235] [cursor=pointer]
  - generic [ref=e236]: 2 VU
```

# Test source

```ts
  668 |     await expect(pendingGuard).toContainText('Save did not complete')
  669 |     await pendingGuard.getByRole('button', { name: 'Keep editing', exact: true }).click()
  670 |     expect(postCount).toBe(1)
  671 |     await expect.poll(() => joinedCount).toBe(1)
  672 |     const actualCreated = await json(await request.get(`/api/v1/tests/${idOf(receipt!)}`))
  673 |     expect(actualCreated.name).toBe('Submitted snapshot')
  674 |     await testInfo.attach('history-joined-single-flight-reopened', { body: JSON.stringify({ post_count: postCount, joined_count: joinedCount, receipt, reopened: actualCreated }, null, 2), contentType: 'application/json' })
  675 |     await editor.getByRole('button', { name: 'Cancel', exact: true }).click()
  676 |     const guard = page.getByRole('dialog', { name: 'Unsaved Changes', exact: true })
  677 |     await expect(guard).toBeVisible()
  678 |     await guard.getByRole('button', { name: 'Keep editing', exact: true }).click()
  679 |     await expect(name).toHaveValue('Later unsaved snapshot')
  680 |     expect(await json(await request.get(`/api/v1/tests/${sourceId}`))).toEqual(source)
  681 |     expect(target.count()).toBe(0)
  682 |   } finally {
  683 |     release()
  684 |     await page.unroute(pattern)
  685 |     if (handlerStarted) await expect.poll(() => handlerJoined, { timeout: 5000, message: 'Owned deferred response must join' }).toBe(true)
  686 |   }
  687 | })
  688 | 
  689 | test('queued-copy submit refuses a source started concurrently and viewers cannot open the action', async ({ page, target, trackRun }, testInfo) => {
  690 |   test.setTimeout(90_000)
  691 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  692 |   const request = page.context().request
  693 |   const workspace = await createWorkspace(request)
  694 |   const script = await seedScript(request, workspace, 'Queued_Ownership_HTTP', target.url)
  695 |   const source = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Queued ownership source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  696 |   trackRun(source)
  697 |   await page.goto(`/#/command-center/${source}?${scopeQuery(workspace)}`)
  698 |   await page.reload()
  699 |   await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
  700 |   const copyEditor = page.getByRole('dialog', { name: 'Create Revised Queued Run', exact: true })
  701 |   await expect(copyEditor).toBeVisible()
  702 |   await copyEditor.getByLabel('Scenario Name', { exact: true }).fill('Must refuse started source')
  703 |   const pattern = '**/api/v1/tests'
  704 |   let started = false
  705 |   await page.route(pattern, async route => {
  706 |     if (route.request().method() !== 'POST' || route.request().postDataJSON().revision_source_test_id !== source) return route.continue()
  707 |     const start = await request.post(`/api/v1/tests/${source}/start`)
  708 |     expect(start.ok(), await start.text()).toBeTruthy()
  709 |     started = true
  710 |     await route.continue()
  711 |   })
  712 |   try {
  713 |     const submitted = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/tests' && response.request().postDataJSON().revision_source_test_id === source)
  714 |     await copyEditor.getByRole('button', { name: /^Create Revised Copy/ }).click()
  715 |     const response = await submitted
  716 |     expect(started).toBe(true)
  717 |     expect(response.ok()).toBe(false)
  718 |     expect(await response.text()).toMatch(/queued|started/i)
  719 |     await expect.poll(() => target.count()).toBeGreaterThan(0)
  720 |     await page.reload()
  721 |     await expect(page.getByRole('button', { name: 'Create revised queued copy', exact: true })).toHaveCount(0)
  722 |     await testInfo.attach('started-source-refusal', { body: JSON.stringify({ source, status: response.status(), submitted: response.request().postDataJSON() }, null, 2), contentType: 'application/json' })
  723 |   } finally { await page.unroute(pattern) }
  724 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
  725 |     const viewerSource = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Viewer queued source', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  726 |     trackRun(viewerSource)
  727 |     const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
  728 |     const viewer = users.find(user => user.subject === 'local:user')!
  729 |     await json(await request.post(`/api/v1/auth/users/${viewer.id}/bindings`, { data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' } }))
  730 |     await page.getByRole('button', { name: 'Sign out' }).click()
  731 |     await login(page, 'user')
  732 |     try {
  733 |       await page.goto(`/#/command-center/${viewerSource}?${scopeQuery(workspace)}`)
  734 |       await page.reload()
  735 |       await expect(page.getByRole('region', { name: 'Command center run controls' })).toContainText('Viewer queued source')
  736 |       await expect(page.getByRole('button', { name: 'Create revised queued copy', exact: true })).toHaveCount(0)
  737 |       await expect(page.getByRole('list', { name: 'Configured group runtime settings', exact: true })).toHaveCount(0)
  738 |       expect((await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, revision_source_test_id: viewerSource, name: 'Forbidden viewer copy', duration: '1m', groups: [{ name: 'orders', script_id: script, vusers: 1, revision_source_group_index: 0 }] } })).status()).toBe(403)
  739 |     } finally {
  740 |       await page.getByRole('button', { name: 'Sign out' }).click()
  741 |       await login(page)
  742 |     }
  743 |   }
  744 | })
  745 | 
  746 | 
  747 | test('queued group dialogs retain inherited Results siblings and WebSocket zero and explicit clear', async ({ page, target, trackRun }, testInfo) => {
  748 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  749 |   const request = page.context().request
  750 |   const workspace = await createWorkspace(request)
  751 |   const results = { csv_exports: [{ id: 'calls', file_name: 'calls.csv', action_name: 'orders', columns: [{ name: 'latency', metric: 'latency_ms' }] }], property_analytics: [{ name: 'category', extraction: 'category', values: ['one'] }] }
  752 |   const http = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Inherited_Fidelity_HTTP', version: '1', protocol: 'http', config: { base_url: target.url, results }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: '/orders' }] } } })))
  753 |   const websocket = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Inherited_Fidelity_WS', version: '1', protocol: 'websocket', config: { base_url: 'ws://127.0.0.1:1', websocket: { callback_interval_ms: 250, read_timeout_ms: 200, compression: true } }, actions: [{ name: 'Connect', type: 'ws_connect', target: 'ws://127.0.0.1:1' }] } } })))
  754 |   const originalHTTP = await json(await request.get(`/api/v1/scripts/${http}`))
  755 |   const originalWS = await json(await request.get(`/api/v1/scripts/${websocket}`))
  756 |   const run = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Queued dialog fidelity', duration: '1m', groups: [{ name: 'orders', script_id: http, vusers: 1 }, { name: 'WS', script_id: websocket, vusers: 1, runtime_settings: { websocket: { callback_interval_ms: 500, read_timeout_ms: 100, compression: true } } }] } })))
  757 |   trackRun(run)
  758 |   await page.goto(`/#/command-center/${run}?${scopeQuery(workspace)}`)
  759 |   await page.reload()
  760 |   const open = async (group: string) => {
  761 |     await page.getByRole('button', { name: `${group} runtime settings`, exact: true }).click()
  762 |     const dialog = page.getByRole('dialog', { name: `Runtime Settings — ${group}`, exact: true })
  763 |     await expect(dialog.getByRole('button', { name: 'Save to run', exact: true })).toBeVisible()
  764 |     return dialog
  765 |   }
  766 |   const httpDialog = await open('orders')
  767 |   await httpDialog.getByRole('tab', { name: 'Error Handling', exact: true }).click()
> 768 |   await httpDialog.getByLabel('Exchange capture', { exact: true }).selectOption('off')
      |                                                                    ^ TimeoutError: locator.selectOption: Timeout 15000ms exceeded.
  769 |   await httpDialog.getByLabel('Include fetched resources', { exact: true }).selectOption('false')
  770 |   await httpDialog.getByLabel('Retained exchange limit', { exact: true }).fill('0')
  771 |   const httpSaved = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/orders/runtime-settings`)
  772 |   await httpDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  773 |   const httpResponse = await httpSaved
  774 |   expect(httpResponse.ok(), await httpResponse.text()).toBeTruthy()
  775 |   expect(httpResponse.request().postDataJSON().runtime_settings.results).toEqual({ ...results, message_detail: { mode: 'off', include_resources: false, max_items: 0 } })
  776 |   await expect(httpDialog).toBeHidden()
  777 |   const httpView = await json(await request.get(`/api/v1/tests/${run}/groups/orders/runtime-settings`))
  778 |   expect(httpView.runtime_settings).toMatchObject({ results: { ...results, message_detail: { mode: 'off', include_resources: false } } })
  779 |   const wsDialog = await open('WS')
  780 |   const callback = wsDialog.getByLabel('WebSocket callback interval (ms)', { exact: true })
  781 |   await expect(callback).toHaveValue('500')
  782 |   await callback.fill('0')
  783 |   const wsSaved = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/WS/runtime-settings`)
  784 |   await wsDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  785 |   const wsResponse = await wsSaved
  786 |   expect(wsResponse.ok(), await wsResponse.text()).toBeTruthy()
  787 |   expect(wsResponse.request().postDataJSON().runtime_settings.websocket).toEqual({ callback_interval_ms: 0, read_timeout_ms: 100, compression: true })
  788 |   await expect(wsDialog).toBeHidden()
  789 |   const clearDialog = await open('WS')
  790 |   await clearDialog.getByRole('button', { name: 'Clear WebSocket override', exact: true }).click()
  791 |   const cleared = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/tests/${run}/groups/WS/runtime-settings`)
  792 |   await clearDialog.getByRole('button', { name: 'Save to run', exact: true }).click()
  793 |   const clearResponse = await cleared
  794 |   expect(clearResponse.ok(), await clearResponse.text()).toBeTruthy()
  795 |   expect(clearResponse.request().postDataJSON().fields).toContain('websocket')
  796 |   expect(clearResponse.request().postDataJSON().runtime_settings).not.toHaveProperty('websocket')
  797 |   await expect(clearDialog).toBeHidden()
  798 |   await page.reload()
  799 |   const inherited = await open('WS')
  800 |   await expect(inherited.getByLabel('WebSocket callback interval (ms)', { exact: true })).toHaveAttribute('placeholder', '250')
  801 |   await expect(inherited.getByLabel('WebSocket callback interval (ms)', { exact: true })).toHaveValue('')
  802 |   expect(await json(await request.get(`/api/v1/scripts/${http}`))).toEqual(originalHTTP)
  803 |   expect(await json(await request.get(`/api/v1/scripts/${websocket}`))).toEqual(originalWS)
  804 |   expect(target.count()).toBe(0)
  805 |   await testInfo.attach('group-dialog-fidelity', { body: JSON.stringify({ http: httpView, submittedWS: wsResponse.request().postDataJSON(), clear: clearResponse.request().postDataJSON() }, null, 2), contentType: 'application/json' })
  806 | })
  807 | 
```