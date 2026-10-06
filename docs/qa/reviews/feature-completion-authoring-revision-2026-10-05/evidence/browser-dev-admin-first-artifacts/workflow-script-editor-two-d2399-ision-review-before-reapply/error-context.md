# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> two editors preserve stale drafts and require explicit revision review before reapply
- Location: e2e/workflow-script-editor.spec.ts:110:1

# Error details

```
TimeoutError: locator.fill: Timeout 15000ms exceeded.
Call log:
  - waiting for locator('.side-panel-script-name input')
    - locator resolved to <input value="Revision fixture" placeholder="My_Test_Script"/>
    - fill("First editor saved")
  - attempting fill action
    2 × waiting for element to be visible, enabled and editable
      - element is not visible
    - retrying fill action
    - waiting 20ms
    2 × waiting for element to be visible, enabled and editable
      - element is not visible
    - retrying fill action
      - waiting 100ms
    30 × waiting for element to be visible, enabled and editable
       - element is not visible
     - retrying fill action
       - waiting 500ms

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791185466508-980009 domain / e2e-1791185466508-980009 project / e2e-1791185466508-980009 application / e2e-1791185466508-980009 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791185466508-980009 domain / e2e-1791185466508-980009 project / e2e-1791185466508-980009 application / e2e-1791185466508-980009 1.0
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
  - main "Revision fixture" [active] [ref=e125]:
    - status [ref=e126]: Revision fixture
    - generic "Dashboard controls" [ref=e127]:
      - heading "Revision fixture" [level=1] [ref=e129]
      - group "Script workspace" [ref=e131]:
        - button "Author" [pressed] [ref=e132] [cursor=pointer]
        - button "Debug" [ref=e133] [cursor=pointer]
      - generic [ref=e134]:
        - link "Help for this page" [ref=e135] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=authoring"
        - generic "Contextual secondary actions" [ref=e136]:
          - group "No unsaved changes to save" [ref=e137]:
            - button "Save script" [disabled] [ref=e138]:
              - generic [ref=e139]: Save
          - button "Debug" [ref=e141] [cursor=pointer]:
            - img [ref=e143]
            - generic [ref=e145]: Debug
        - generic "Deployment identity" [ref=e146]:
          - generic [ref=e147]: Browser · Server
        - combobox "Dashboard color theme" [ref=e150]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - generic [ref=e151]:
      - region "Object history" [ref=e152]:
        - generic [ref=e153]: Created by Development Mode · Last modified by Unknown
        - group [ref=e154]:
          - generic "History" [ref=e155] [cursor=pointer]
      - generic [ref=e156]:
        - generic "Script evidence states" [ref=e157]:
          - button "Close script editor" [ref=e158] [cursor=pointer]: Close editor
          - button "Reset layout" [ref=e159] [cursor=pointer]
          - group "Undo document change" [ref=e160]:
            - button "Undo document change" [disabled] [ref=e161]:
              - img [ref=e162]
          - group "Redo document change" [ref=e165]:
            - button "Redo document change" [disabled] [ref=e166]:
              - img [ref=e167]
          - button "Review changes" [ref=e170] [cursor=pointer]
          - generic [ref=e171]: "Saved: up to date"
          - generic [ref=e172]: "Validation evidence: not yet"
          - generic [ref=e173]: "Debugged: missing"
          - generic [ref=e174]: "Approval readiness: needs evidence"
          - button "Author Problems (0)" [ref=e175] [cursor=pointer]
          - group [ref=e176]:
            - generic "Checks & readiness" [ref=e177] [cursor=pointer]
        - group "Author panes" [ref=e178]:
          - button "Outline" [ref=e179] [cursor=pointer]
          - button "Editor" [pressed] [ref=e180] [cursor=pointer]
          - button "Context" [ref=e181] [cursor=pointer]
        - tabpanel "Authoring workspace" [ref=e183]:
          - region "Author center workbench" [ref=e185]:
            - tablist "Author center workbench tabs" [ref=e186]:
              - tab "Action editor" [selected] [ref=e189] [cursor=pointer]:
                - generic [ref=e191]: Action editor
              - tab "Source" [ref=e194] [cursor=pointer]:
                - generic [ref=e196]: Source
              - generic [ref=e197]:
                - tab "Runtime Settings" [ref=e199] [cursor=pointer]:
                  - generic [ref=e201]: Runtime Settings
                - button "Close Runtime Settings" [ref=e203] [cursor=pointer]: ×
            - tabpanel "Action editor" [ref=e205]:
              - region "Action editor" [ref=e206]:
                - generic [ref=e207]:
                  - button "Edit selected action" [pressed] [ref=e208] [cursor=pointer]
                  - button "Overview & bulk" [ref=e209] [cursor=pointer]
                  - button "+ Add Action" [ref=e210] [cursor=pointer]
                - generic [ref=e212]:
                  - heading "Select an action to start editing" [level=3] [ref=e213]
                  - paragraph [ref=e214]: Use the outline to navigate init, actions, teardown, and nested blocks, or add an action.
```

# Test source

```ts
  32  |       session: { id: body.replay_session_id, session_incarnation: sessionIncarnation, state_revision: '1',
  33  |         runtime_state: runtimeState, status: passed ? 'completed' : 'failed', completed_at: now, report } } })
  34  |   })
  35  |   await page.setViewportSize({ width: 1440, height: 1000 })
  36  |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  37  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  38  |   await page.locator('.topbar-debug-action-run').click()
  39  |   await expect(page.getByRole('button', { name: 'Edit this assertion' })).toBeVisible()
  40  |   await expect(page.locator('.script-browser-evidence')).toHaveCount(1)
  41  |   await expect(page.getByRole('region', { name: 'Debug detail', exact: true })).toHaveCount(0)
  42  |   await page.getByRole('button', { name: 'Edit this assertion' }).click()
  43  |   await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  44  |   const expected = page.getByLabel('Assertion 1 expected value', { exact: true })
  45  |   await expect(expected).toHaveValue('Welcome')
  46  |   await expect(expected).toBeFocused()
  47  |   await expected.fill('Hello')
  48  |   await expect(page.getByLabel('Script evidence states')).toContainText('stale')
  49  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  50  |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  51  |   const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  52  |   expect(saved.script.actions[0].control.steps[0]).toMatchObject({ metadata: { audit_marker: 'preserve' }, wait_for: leaf.wait_for, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Hello' }] })
  53  |   await page.locator('.topbar-debug-action-run').click()
  54  |   await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
  55  |   expect(runs).toBe(2)
  56  |   expect(replayExpected).toBe('Hello')
  57  |   await page.screenshot({ path: testInfo.outputPath('script-debug-evidence.png'), fullPage: true })
  58  | })
  59  | 
  60  | test('authored script goals survive an unrelated editor save and canonical reopen', async ({ page }, testInfo) => {
  61  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  62  |   const request = page.context().request
  63  |   const workspace = await createWorkspace(request)
  64  |   const goals = {
  65  |     target_average_tps: 8.75, p95_latency_ms: 217.5, max_error_pct: 0,
  66  |     min_vusers: 1, max_vusers: 6, max_error_count: 17,
  67  |     stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '11s',
  68  |     transaction_rules: [{ population: 'transaction', protocol: 'http', group: 'checkout', transaction: 'Purchase', metric: 'p95_duration_ms', comparator: 'lte', threshold: 215.25, severity: 'fail' }],
  69  |   }
  70  |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
  71  |     project_id: workspace.projectId,
  72  |     script: { name: 'Durable script goals', version: '1.0', protocol: 'http', goal_config: goals,
  73  |       config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Purchase', type: 'request', method: 'GET', target: '/' }] },
  74  |   } })))
  75  |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  76  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  77  |   const workbench = page.getByRole('region', { name: 'Author center workbench' })
  78  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  79  |   const baseURL = workbench.getByRole('textbox', { name: 'Base URL' })
  80  |   await expect(baseURL).toHaveValue('http://127.0.0.1:1')
  81  |   await baseURL.fill('http://127.0.0.1:2')
  82  |   const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  83  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  84  |   await json(await saved)
  85  |   await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  86  |   const canonical = await json(await request.get(`/api/v1/scripts/${id}`))
  87  |   expect(canonical.script).toMatchObject({ goal_config: goals, config: { base_url: 'http://127.0.0.1:2' } })
  88  |   await page.reload()
  89  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  90  |   await expect(baseURL).toHaveValue('http://127.0.0.1:2')
  91  |   await workbench.getByRole('tab', { name: 'Source' }).click()
  92  |   const code = page.locator('.script-code-pane')
  93  |   // With no action selected, Source already owns the complete canonical JSON.
  94  |   // Its virtualized code view must navigate to the authored goals to inspect
  95  |   // them; no conditional action-scope toggle is present on this entry path.
  96  |   await code.getByRole('button', { name: 'Find', exact: true }).click()
  97  |   const find = code.getByPlaceholder('Find', { exact: true })
  98  |   await find.pressSequentially('goal_config')
  99  |   await find.press('Enter')
  100 |   await expect(code.locator('.cm-content')).toContainText('goal_config')
  101 |   await find.fill('')
  102 |   await find.pressSequentially('217.5')
  103 |   await find.press('Enter')
  104 |   await expect(code.locator('.cm-content')).toContainText('217.5')
  105 |   await find.press('Escape')
  106 |   await page.screenshot({ path: testInfo.outputPath('canonical-goals-reopen.png'), fullPage: true })
  107 | })
  108 | 
  109 | 
  110 | test('two editors preserve stale drafts and require explicit revision review before reapply', async ({ page }, testInfo) => {
  111 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  112 |   const request = page.context().request
  113 |   const workspace = await createWorkspace(request)
  114 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
  115 |     project_id: workspace.projectId,
  116 |     script: { name: 'Revision fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] },
  117 |   } })))
  118 |   const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  119 |   expect(initial.ok()).toBeTruthy()
  120 |   const initialTag = initial.headers().etag
  121 |   expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
  122 |   const other = await page.context().newPage()
  123 |   try {
  124 |     const route = `/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`
  125 |     await page.goto(route)
  126 |     await other.goto(route)
  127 |     if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') { await page.reload(); await other.reload() }
  128 |     const firstName = page.locator('.side-panel-script-name input')
  129 |     const staleName = other.locator('.side-panel-script-name input')
  130 |     await expect(firstName).toHaveValue('Revision fixture')
  131 |     await expect(staleName).toHaveValue('Revision fixture')
> 132 |     await firstName.fill('First editor saved')
      |                     ^ TimeoutError: locator.fill: Timeout 15000ms exceeded.
  133 |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  134 |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  135 |     await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:2')
  136 |     const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  137 |     await page.getByRole('button', { name: 'Save script', exact: true }).click()
  138 |     const response = await saved
  139 |     expect(response.request().headers()['if-match']).toBe(initialTag)
  140 |     expect(response.headers()['x-apex-script-save-mode']).toBe('conditioned')
  141 |     await json(response)
  142 |     await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  143 |     await staleName.fill('Retained stale draft')
  144 |     let writes = 0
  145 |     other.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) writes++ })
  146 |     const rejected = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
  147 |     await other.getByRole('button', { name: 'Save script', exact: true }).click()
  148 |     const conflictResponse = await rejected
  149 |     expect(conflictResponse.status()).toBe(412)
  150 |     expect(conflictResponse.request().headers()['if-match']).toBe(initialTag)
  151 |     const conflict = other.getByRole('region', { name: 'Script save reconciliation' })
  152 |     await expect(conflict).toBeVisible()
  153 |     await expect(staleName).toHaveValue('Retained stale draft')
  154 |     await expect(other.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  155 |     expect(writes).toBe(1)
  156 |     await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  157 |     await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
  158 |     await expect(staleName).toHaveValue('Retained stale draft')
  159 |     await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  160 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('name')
  161 |     await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('Retained stale draft')
  162 |     expect(writes).toBe(1)
  163 |     await other.setViewportSize({ width: 760, height: 1000 })
  164 |     await conflict.scrollIntoViewIfNeeded()
  165 |     const box = await conflict.boundingBox()
  166 |     expect(box).not.toBeNull()
  167 |     expect(box!.x).toBeGreaterThanOrEqual(0)
  168 |     expect(box!.x + box!.width).toBeLessThanOrEqual(760)
  169 |     await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeInViewport()
  170 |     await conflict.screenshot({ path: testInfo.outputPath('script-revision-conflict.png') })
  171 |     await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  172 |     await expect(conflict).toHaveCount(0)
  173 |     await expect(staleName).toHaveValue('Retained stale draft')
  174 |     expect(writes).toBe(1)
  175 |     const reappliedSave = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
  176 |     await other.getByRole('button', { name: 'Save script', exact: true }).click()
  177 |     const reappliedResponse = await reappliedSave
  178 |     expect(reappliedResponse.request().headers()['if-match']).not.toBe(initialTag)
  179 |     expect(reappliedResponse.headers()['x-apex-script-save-mode']).toBe('conditioned')
  180 |     await json(reappliedResponse)
  181 |     const final = await json(await request.get(`/api/v1/scripts/${id}`))
  182 |     expect(final.script).toMatchObject({ name: 'Retained stale draft', config: { base_url: 'http://127.0.0.1:2' } })
  183 |     expect(writes).toBe(2)
  184 |   } finally { await other.close() }
  185 | })
  186 | 
  187 | test('placement-only changes invalidate save authority and explicit reapply preserves the current folder', async ({ page }) => {
  188 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  189 |   const request = page.context().request
  190 |   const workspace = await createWorkspace(request)
  191 |   const folder = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, { data: { name: 'Current placement', category: 'scripts' } })))
  192 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Placement fixture', version: '1', protocol: 'http', config: {}, actions: [] } } })))
  193 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  194 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  195 |   const name = page.locator('.side-panel-script-name input')
  196 |   await expect(name).toHaveValue('Placement fixture')
  197 |   await name.fill('My placement draft')
  198 |   await json(await request.patch(`/api/v1/scripts/${id}/move`, { data: { folder_id: folder } }))
  199 |   const save = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  200 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  201 |   expect((await save).status()).toBe(412)
  202 |   await expect(name).toHaveValue('My placement draft')
  203 |   const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  204 |   await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  205 |   await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  206 |   const retried = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  207 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  208 |   await json(await retried)
  209 |   const final = await json(await request.get(`/api/v1/scripts/${id}`))
  210 |   expect(final.folder_id).toBe(folder)
  211 |   expect(final.script).toMatchObject({ name: 'My placement draft', folder_id: folder })
  212 | })
  213 | 
  214 | test('uncertain successful writes retain drafts and revoke retry authority until explicit review', async ({ page }) => {
  215 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  216 |   const request = page.context().request
  217 |   const workspace = await createWorkspace(request)
  218 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Uncertain fixture', version: '1', protocol: 'http', config: {}, actions: [] } } })))
  219 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  220 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  221 |   const name = page.locator('.side-panel-script-name input')
  222 |   await expect(name).toHaveValue('Uncertain fixture')
  223 |   let writes = 0
  224 |   await page.route(`**/api/v1/scripts/${id}`, async route => {
  225 |     if (route.request().method() !== 'PUT') { await route.continue(); return }
  226 |     writes++
  227 |     const response = await route.fetch()
  228 |     expect(response.ok()).toBeTruthy()
  229 |     const headers = { ...response.headers() }
  230 |     delete headers['x-apex-script-save-mode']
  231 |     // Genuine server commit; strip its receipt only on the browser return path.
  232 |     await route.fulfill({ response, headers })
```