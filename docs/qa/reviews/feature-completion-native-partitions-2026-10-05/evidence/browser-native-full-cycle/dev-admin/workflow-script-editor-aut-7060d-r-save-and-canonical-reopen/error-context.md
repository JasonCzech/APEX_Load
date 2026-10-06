# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> authored script goals survive an unrelated editor save and canonical reopen
- Location: e2e/workflow-script-editor.spec.ts:60:1

# Error details

```
TimeoutError: locator.pressSequentially: Timeout 15000ms exceeded.
Call log:
  - waiting for locator('.script-code-pane').getByPlaceholder('Find', { exact: true })
    - locator resolved to <input form="" value="" name="search" aria-label="Find" main-field="true" placeholder="Find" class="cm-textfield"/>
  - elementHandle.type("goal_config")
  - element was detached from the DOM, retrying

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791192813481-582681 domain / e2e-1791192813481-582681 project / e2e-1791192813481-582681 application / e2e-1791192813481-582681 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791192813481-582681 domain / e2e-1791192813481-582681 project / e2e-1791192813481-582681 application / e2e-1791192813481-582681 1.0
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
  - main "Durable script goals" [ref=e125]:
    - status [ref=e126]: Durable script goals
    - generic "Dashboard controls" [ref=e127]:
      - heading "Durable script goals" [level=1] [ref=e129]
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
        - generic [ref=e153]: Created by Development Mode · Last modified by Development Mode
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
              - tab "Action editor" [ref=e189] [cursor=pointer]:
                - generic [ref=e191]: Action editor
              - tab "Source" [selected] [ref=e194] [cursor=pointer]:
                - generic [ref=e196]: Source
              - generic [ref=e197]:
                - tab "Runtime Settings" [ref=e199] [cursor=pointer]:
                  - generic [ref=e201]: Runtime Settings
                - button "Close Runtime Settings" [ref=e203] [cursor=pointer]: ×
            - tabpanel "Source" [ref=e205]:
              - region "Source" [ref=e206]:
                - generic [ref=e207]:
                  - generic [ref=e208]:
                    - strong [ref=e209]: "{ } Source"
                    - generic [ref=e210]: 1 executable
                  - generic [ref=e211]:
                    - generic [ref=e212]: Synced
                    - generic "The JSON mirrors the structured editor. Click Edit JSON to change it directly." [ref=e213]: Read-only
                    - button "Find" [ref=e214] [cursor=pointer]
                    - 'button "Word Wrap: On" [pressed] [ref=e215] [cursor=pointer]'
                    - button "Parameterize" [ref=e216] [cursor=pointer]
                    - button "Edit JSON" [ref=e217] [cursor=pointer]
                    - button "Copy" [ref=e218] [cursor=pointer]
                    - button "Download" [ref=e219] [cursor=pointer]
                - generic [ref=e223]:
                  - generic [ref=e224]:
                    - generic [ref=e225]:
                      - generic [ref=e226]: "1"
                      - generic [ref=e227]: "2"
                      - generic [ref=e228]: "3"
                      - generic [ref=e229]: "4"
                      - generic [ref=e230]: "5"
                      - generic [ref=e231]: "6"
                      - generic [ref=e232]: "7"
                      - generic [ref=e233]: "8"
                      - generic [ref=e234]: "9"
                      - generic [ref=e235]: "10"
                      - generic [ref=e236]: "11"
                      - generic [ref=e237]: "12"
                      - generic [ref=e238]: "13"
                      - generic [ref=e239]: "14"
                      - generic [ref=e240]: "15"
                      - generic [ref=e241]: "16"
                      - generic [ref=e242]: "17"
                      - generic [ref=e243]: "18"
                      - generic [ref=e244]: "19"
                      - generic [ref=e245]: "20"
                      - generic [ref=e246]: "21"
                      - generic [ref=e247]: "22"
                      - generic [ref=e248]: "23"
                      - generic [ref=e249]: "24"
                      - generic [ref=e250]: "25"
                      - generic [ref=e251]: "26"
                      - generic [ref=e252]: "27"
                      - generic [ref=e253]: "28"
                      - generic [ref=e254]: "29"
                      - generic [ref=e255]: "30"
                      - generic [ref=e256]: "31"
                      - generic [ref=e257]: "32"
                      - generic [ref=e258]: "33"
                    - generic [ref=e259]:
                      - generic [ref=e260]: ⌄
                      - generic [ref=e261]: ⌄
                      - generic [ref=e262]: ⌄
                      - generic [ref=e263]: ⌄
                      - generic [ref=e264]: ⌄
                      - generic [ref=e265]: ⌄
                      - generic [ref=e266]: ⌄
                      - generic [ref=e267]: ⌄
                      - generic [ref=e268]: ⌄
                  - textbox [ref=e269]:
                    - generic [ref=e271]: "{"
                    - generic [ref=e272]: "\"script\": {"
                    - generic [ref=e273]:
                      - generic [ref=e274]: "\"actions\""
                      - text: ": ["
                    - generic [ref=e275]: "{"
                    - generic [ref=e276]:
                      - generic [ref=e277]: "\"method\""
                      - text: ":"
                      - generic [ref=e278]: "\"GET\""
                      - text: ","
                    - generic [ref=e279]:
                      - generic [ref=e280]: "\"name\""
                      - text: ":"
                      - generic [ref=e281]: "\"Purchase\""
                      - text: ","
                    - generic [ref=e282]:
                      - generic [ref=e283]: "\"target\""
                      - text: ":"
                      - generic [ref=e284]: "\"/\""
                      - text: ","
                    - generic [ref=e285]: "\"think_time\": {"
                    - generic [ref=e286]: "\"mean_ms\": 0,"
                    - generic [ref=e287]: "\"type\": \"\""
                    - generic [ref=e288]: "},"
                    - generic [ref=e289]:
                      - generic [ref=e290]: "\"type\""
                      - text: ":"
                      - generic [ref=e291]: "\"request\""
                    - generic [ref=e292]: "}"
                    - generic [ref=e293]: "],"
                    - generic [ref=e294]:
                      - generic [ref=e295]: "\"config\""
                      - text: ": {"
                    - generic [ref=e296]:
                      - generic [ref=e297]: "\"base_url\""
                      - text: ": \"http://127.0.0.1:2\","
                    - generic [ref=e298]:
                      - generic [ref=e299]: "\"browser\""
                      - text: ": {"
                    - generic [ref=e300]:
                      - generic [ref=e301]: "\"geolocation\""
                      - text: ": {},"
                    - generic [ref=e302]:
                      - generic [ref=e303]: "\"http_credentials\""
                      - text: ": {},"
                    - generic [ref=e304]:
                      - generic [ref=e305]: "\"proxy\""
                      - text: ": {},"
                    - generic [ref=e306]:
                      - generic [ref=e307]: "\"review_policy\""
                      - text: ": {},"
                    - generic [ref=e308]:
                      - generic [ref=e309]: "\"viewport\""
                      - text: ": {}"
                    - generic [ref=e310]: "},"
                    - generic [ref=e311]:
                      - generic [ref=e312]: "\"bruno\""
                      - text: ": {"
                    - generic [ref=e313]:
                      - generic [ref=e314]: "\"limits\""
                      - text: ": {}"
                    - generic [ref=e315]: "},"
                    - generic [ref=e316]:
                      - generic [ref=e317]: "\"database\""
                      - text: ": {},"
                    - generic [ref=e318]:
                      - generic [ref=e319]: "\"error_handling\""
                      - text: ": {},"
                    - generic [ref=e320]:
                      - generic [ref=e321]: "\"eventhubs\""
                      - text: ": {},"
                    - generic [ref=e322]:
                      - generic [ref=e323]: "\"grpc\""
                      - text: ": {},"
                    - generic [ref=e324]:
                      - generic [ref=e325]: "\"http\""
                      - text: ": {"
                    - generic [ref=e326]:
                      - generic [ref=e327]: "\"auth\""
                      - text: ": {},"
                    - generic [ref=e328]:
                      - generic [ref=e329]: "\"proxy\""
                      - text: ": {},"
```

# Test source

```ts
  1   | import { test, expect } from '@playwright/test'
  2   | import { randomUUID } from 'node:crypto'
  3   | import type { ScriptDebugRuntimeState } from '../src/types/replay'
  4   | import { idOf, json, login, mutation } from './support/workflow'
  5   | import { createWorkspace, scopeQuery } from './support/workspace'
  6   | 
  7   | test('operator edits the failed nested browser assertion and reruns without losing source fields', async ({ page }, testInfo) => {
  8   |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  9   |   const request = page.context().request
  10  |   const workspace = await createWorkspace(request)
  11  |   const leaf = { name: 'Check greeting', type: 'browser_wait', target: '#result', wait_for: { type: 'selector', value: '#result', timeout_ms: 10000 }, metadata: { audit_marker: 'preserve' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Welcome' }] }
  12  |   const script = { name: 'Nested greeting', version: '1.0.0', protocol: 'browser', config: {}, actions: [{ name: 'Repeat journey', type: 'control_loop', control: { count: 1, steps: [leaf] } }] }
  13  |   const created = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })
  14  |   expect(created.ok(), await created.text()).toBeTruthy()
  15  |   const payload = await created.json(); const id = payload.script?.id || payload.id
  16  |   let runs = 0
  17  |   let replayExpected = 'Welcome'
  18  |   await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
  19  |     const body = route.request().postDataJSON(); runs++
  20  |     const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  21  |     replayExpected = saved.script.actions[0].control.steps[0].assertions[0].expected
  22  |     const now = new Date().toISOString()
  23  |     const passed = replayExpected === 'Hello'
  24  |     // Go omits the zero action index in JSON. Repair must still find the first root.
  25  |     const event = { phase: 'action', step_path: ['loop[0]'], iteration: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: passed, duration_ms: 1, started_at: now, completed_at: now, error_message: passed ? '' : 'Greeting mismatch', protocol_evidence: passed ? {} : { assertion_failure: { type: 'element_text_contains', expected: 'Welcome', actual: 'Hello' } } }
  26  |     const report = { replay_session_id: body.replay_session_id, group: 'Nested greeting', protocol: 'browser', started_at: now, completed_at: now, success: passed, events: [event] }
  27  |     const sessionIncarnation = randomUUID()
  28  |     const runtimeState = { version: 1, kind: 'full', session_id: body.replay_session_id,
  29  |       session_incarnation: sessionIncarnation, state_revision: '1', state: 'unavailable',
  30  |       reason: 'session_terminal', snapshot: null } satisfies ScriptDebugRuntimeState
  31  |     await route.fulfill({ json: { debug_session_id: body.replay_session_id, replay_session_id: body.replay_session_id,
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
> 98  |   await find.pressSequentially('goal_config')
      |              ^ TimeoutError: locator.pressSequentially: Timeout 15000ms exceeded.
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
```