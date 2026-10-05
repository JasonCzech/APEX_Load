# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> authored script goals survive an unrelated editor save and canonical reopen
- Location: e2e/workflow-script-editor.spec.ts:61:1

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
    - button "Workspace context. e2e-1791186344450-542208 domain / e2e-1791186344450-542208 project / e2e-1791186344450-542208 application / e2e-1791186344450-542208 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791186344450-542208 domain / e2e-1791186344450-542208 project / e2e-1791186344450-542208 application / e2e-1791186344450-542208 1.0
      - generic [ref=e18]: ⌄
    - navigation "Primary navigation" [ref=e19]:
      - button "New Test" [ref=e21] [cursor=pointer]:
        - img [ref=e23]
        - generic [ref=e26]: New Test
      - generic [ref=e27]:
        - paragraph [ref=e28]: Operate
        - button "Dashboard" [ref=e30] [cursor=pointer]:
          - img [ref=e32]
          - generic [ref=e37]: Dashboard
        - button "Command Center" [ref=e39] [cursor=pointer]:
          - img [ref=e41]
          - generic [ref=e45]: Command Center
        - button "Results" [ref=e47] [cursor=pointer]:
          - img [ref=e49]
          - generic [ref=e50]: Results
        - button "Monitoring" [ref=e52] [cursor=pointer]:
          - img [ref=e54]
          - generic [ref=e56]: Monitoring
        - button "Activity" [ref=e58] [cursor=pointer]:
          - img [ref=e60]
          - generic [ref=e62]: Activity
      - generic [ref=e63]:
        - paragraph [ref=e64]: Build
        - button "Scripts" [ref=e66] [cursor=pointer]:
          - img [ref=e68]
          - generic [ref=e71]: Scripts
        - button "Scenarios" [ref=e73] [cursor=pointer]:
          - img [ref=e75]
          - generic [ref=e77]: Scenarios
      - generic [ref=e78]:
        - paragraph [ref=e79]: Administer
        - button "Repository" [ref=e81] [cursor=pointer]:
          - img [ref=e83]
          - generic [ref=e86]: Repository
        - button "Access" [ref=e88] [cursor=pointer]:
          - img [ref=e90]
          - generic [ref=e94]: Access
        - button "Settings" [ref=e96] [cursor=pointer]:
          - img [ref=e98]
          - generic [ref=e101]: Settings
      - generic [ref=e102]:
        - paragraph [ref=e103]: Support
        - button "Help & documentation" [ref=e105] [cursor=pointer]:
          - img [ref=e107]
          - generic [ref=e110]: Help & documentation
    - generic [ref=e111]:
      - generic [ref=e112]:
        - 'button "Preview permissions. Current mode: admin" [ref=e114] [cursor=pointer]':
          - generic [ref=e115]: DEV
          - generic [ref=e116]: Preview permissions
        - generic [ref=e117]:
          - generic "Live" [ref=e118]:
            - generic [ref=e120]: Live
          - 'button "Build info: local · dev" [ref=e122] [cursor=pointer]':
            - generic [ref=e124]: Version Info
      - button "Collapse sidebar" [expanded] [ref=e126] [cursor=pointer]:
        - img [ref=e127]
  - main "Durable script goals" [ref=e129]:
    - status [ref=e130]: Durable script goals
    - generic "Dashboard controls" [ref=e131]:
      - heading "Durable script goals" [level=1] [ref=e133]
      - group "Script workspace" [ref=e135]:
        - button "Author" [pressed] [ref=e136] [cursor=pointer]
        - button "Debug" [ref=e137] [cursor=pointer]
      - generic [ref=e138]:
        - link "Help for this page" [ref=e139] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=authoring"
        - generic "Contextual secondary actions" [ref=e140]:
          - group "No unsaved changes to save" [ref=e141]:
            - button "Save script" [disabled] [ref=e142]:
              - generic [ref=e143]: Save
          - button "Debug one iteration" [ref=e145] [cursor=pointer]:
            - img [ref=e147]
            - generic [ref=e149]: Debug one iteration
        - generic "Deployment identity" [ref=e150]:
          - generic [ref=e151]: Browser · Server
        - combobox "Dashboard color theme" [ref=e154]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - generic [ref=e155]:
      - region "Object history" [ref=e156]:
        - generic [ref=e157]: Created by Development Mode · Last modified by Development Mode
        - group [ref=e158]:
          - generic "History" [ref=e159] [cursor=pointer]
      - generic [ref=e160]:
        - generic "Script evidence states" [ref=e161]:
          - button "Outline" [pressed] [ref=e162] [cursor=pointer]
          - button "Close script editor" [ref=e163] [cursor=pointer]: Close editor
          - button "Reset layout" [ref=e164] [cursor=pointer]
          - group "Undo document change" [ref=e165]:
            - button "Undo document change" [disabled] [ref=e166]:
              - img [ref=e167]
          - group "Redo document change" [ref=e170]:
            - button "Redo document change" [disabled] [ref=e171]:
              - img [ref=e172]
          - button "Review changes" [ref=e175] [cursor=pointer]
          - generic [ref=e176]: "Saved: up to date"
          - generic [ref=e177]: "Validation evidence: not yet"
          - generic [ref=e178]: "Debugged: missing"
          - generic [ref=e179]: "Approval readiness: needs evidence"
          - button "Author Problems (0)" [ref=e180] [cursor=pointer]
          - group [ref=e181]:
            - generic "Checks & readiness" [ref=e182] [cursor=pointer]
        - tabpanel "Authoring workspace" [ref=e184]:
          - generic [ref=e185]:
            - separator "Resize outline panel (double-click to fit content)" [ref=e186]
            - generic [ref=e187]:
              - button "Collapse outline side panel" [expanded] [ref=e191] [cursor=pointer]:
                - img [ref=e192]
                - generic [ref=e194]: Collapse
              - generic "Script identity" [ref=e195]:
                - generic [ref=e196]:
                  - generic [ref=e197]: Script Name
                  - textbox "Script Name" [ref=e198]:
                    - /placeholder: My_Test_Script
                    - text: Durable script goals
              - navigation "Script outline" [ref=e199]:
                - generic [ref=e200]:
                  - text: Find action
                  - searchbox "Find action" [ref=e201]
                - generic [ref=e202]:
                  - heading "Init" [level=4] [ref=e203]
                  - text: No actions
                - generic [ref=e204]:
                  - heading "Actions" [level=4] [ref=e205]
                  - button "Purchase GET · Action 1" [ref=e208] [cursor=pointer]:
                    - strong [ref=e209]: Purchase
                    - generic [ref=e210]: GET · Action 1
                - generic [ref=e211]:
                  - heading "Teardown" [level=4] [ref=e212]
                  - text: No actions
              - group [ref=e213]:
                - generic "Parameters & data (0)" [ref=e214] [cursor=pointer]
              - generic [ref=e216]:
                - button "Script Configuration" [ref=e217] [cursor=pointer]
                - button "Runtime Settings" [ref=e218] [cursor=pointer]
                - button "Parameter Data" [ref=e219] [cursor=pointer]
            - region "Author center workbench" [ref=e220]:
              - tablist "Author center workbench tabs" [ref=e221]:
                - tab "Action editor" [ref=e224] [cursor=pointer]:
                  - generic [ref=e226]: Action editor
                - tab "Source" [selected] [ref=e229] [cursor=pointer]:
                  - generic [ref=e231]: Source
                - generic [ref=e232]:
                  - tab "Runtime Settings" [ref=e234] [cursor=pointer]:
                    - generic [ref=e236]: Runtime Settings
                  - button "Close Runtime Settings" [ref=e238] [cursor=pointer]: ×
              - tabpanel "Source" [ref=e240]:
                - region "Source" [ref=e241]:
                  - generic [ref=e242]:
                    - generic [ref=e243]:
                      - strong [ref=e244]: "{ } Source"
                      - generic [ref=e245]: 1 executable
                    - generic [ref=e246]:
                      - generic [ref=e247]: Synced
                      - generic "The JSON mirrors the structured editor. Click Edit JSON to change it directly." [ref=e248]: Read-only
                      - button "Find" [ref=e249] [cursor=pointer]
                      - 'button "Word Wrap: On" [pressed] [ref=e250] [cursor=pointer]'
                      - button "Parameterize" [ref=e251] [cursor=pointer]
                      - button "Edit JSON" [ref=e252] [cursor=pointer]
                      - button "Copy" [ref=e253] [cursor=pointer]
                      - button "Download" [ref=e254] [cursor=pointer]
                  - generic [ref=e258]:
                    - generic [ref=e259]:
                      - generic [ref=e260]:
                        - generic [ref=e261]: "1"
                        - generic [ref=e262]: "2"
                        - generic [ref=e263]: "3"
                        - generic [ref=e264]: "4"
                        - generic [ref=e265]: "5"
                        - generic [ref=e266]: "6"
                        - generic [ref=e267]: "7"
                        - generic [ref=e268]: "8"
                        - generic [ref=e269]: "9"
                        - generic [ref=e270]: "10"
                        - generic [ref=e271]: "11"
                        - generic [ref=e272]: "12"
                        - generic [ref=e273]: "13"
                        - generic [ref=e274]: "14"
                        - generic [ref=e275]: "15"
                        - generic [ref=e276]: "16"
                        - generic [ref=e277]: "17"
                        - generic [ref=e278]: "18"
                        - generic [ref=e279]: "19"
                        - generic [ref=e280]: "20"
                        - generic [ref=e281]: "21"
                        - generic [ref=e282]: "22"
                        - generic [ref=e283]: "23"
                        - generic [ref=e284]: "24"
                        - generic [ref=e285]: "25"
                        - generic [ref=e286]: "26"
                        - generic [ref=e287]: "27"
                        - generic [ref=e288]: "28"
                        - generic [ref=e289]: "29"
                        - generic [ref=e290]: "30"
                        - generic [ref=e291]: "31"
                        - generic [ref=e292]: "32"
                        - generic [ref=e293]: "33"
                        - generic [ref=e294]: "34"
                        - generic [ref=e295]: "35"
                        - generic [ref=e296]: "36"
                        - generic [ref=e297]: "37"
                        - generic [ref=e298]: "38"
                        - generic [ref=e299]: "39"
                        - generic [ref=e300]: "40"
                        - generic [ref=e301]: "41"
                        - generic [ref=e302]: "42"
                        - generic [ref=e303]: "43"
                        - generic [ref=e304]: "44"
                        - generic [ref=e305]: "45"
                        - generic [ref=e306]: "46"
                        - generic [ref=e307]: "47"
                        - generic [ref=e308]: "48"
                        - generic [ref=e309]: "49"
                        - generic [ref=e310]: "50"
                        - generic [ref=e311]: "51"
                        - generic [ref=e312]: "52"
                        - generic [ref=e313]: "53"
                        - generic [ref=e314]: "54"
                        - generic [ref=e315]: "55"
                        - generic [ref=e316]: "56"
                        - generic [ref=e317]: "57"
                        - generic [ref=e318]: "58"
                        - generic [ref=e319]: "59"
                        - generic [ref=e320]: "60"
                        - generic [ref=e321]: "61"
                        - generic [ref=e322]: "62"
                        - generic [ref=e323]: "63"
                        - generic [ref=e324]: "64"
                        - generic [ref=e325]: "65"
                        - generic [ref=e326]: "66"
                        - generic [ref=e327]: "67"
                        - generic [ref=e328]: "68"
                      - generic [ref=e329]:
                        - generic [ref=e330]: ⌄
                        - generic [ref=e331]: ⌄
                        - generic [ref=e332]: ⌄
                        - generic [ref=e333]: ⌄
                        - generic [ref=e334]: ⌄
                        - generic [ref=e335]: ⌄
                        - generic [ref=e336]: ⌄
                        - generic [ref=e337]: ⌄
                        - generic [ref=e338]: ⌄
                        - generic [ref=e339]: ⌄
                        - generic [ref=e340]: ⌄
                        - generic [ref=e341]: ⌄
                        - generic [ref=e342]: ⌄
                        - generic [ref=e343]: ⌄
                    - textbox [ref=e344]:
                      - generic [ref=e346]: "{"
                      - generic [ref=e347]: "\"script\": {"
                      - generic [ref=e348]:
                        - generic [ref=e349]: "\"actions\""
                        - text: ": ["
                      - generic [ref=e350]: "{"
                      - generic [ref=e351]:
                        - generic [ref=e352]: "\"method\""
                        - text: ":"
                        - generic [ref=e353]: "\"GET\""
                        - text: ","
                      - generic [ref=e354]:
                        - generic [ref=e355]: "\"name\""
                        - text: ":"
                        - generic [ref=e356]: "\"Purchase\""
                        - text: ","
                      - generic [ref=e357]:
                        - generic [ref=e358]: "\"target\""
                        - text: ":"
                        - generic [ref=e359]: "\"/\""
                        - text: ","
                      - generic [ref=e360]: "\"think_time\": {"
                      - generic [ref=e361]: "\"mean_ms\": 0,"
                      - generic [ref=e362]: "\"type\": \"\""
                      - generic [ref=e363]: "},"
                      - generic [ref=e364]:
                        - generic [ref=e365]: "\"type\""
                        - text: ":"
                        - generic [ref=e366]: "\"request\""
                      - generic [ref=e367]: "}"
                      - generic [ref=e368]: "],"
                      - generic [ref=e369]:
                        - generic [ref=e370]: "\"config\""
                        - text: ": {"
                      - generic [ref=e371]:
                        - generic [ref=e372]: "\"base_url\""
                        - text: ": \"http://127.0.0.1:2\","
                      - generic [ref=e373]:
                        - generic [ref=e374]: "\"browser\""
                        - text: ": {"
                      - generic [ref=e375]:
                        - generic [ref=e376]: "\"geolocation\""
                        - text: ": {},"
                      - generic [ref=e377]:
                        - generic [ref=e378]: "\"http_credentials\""
                        - text: ": {},"
                      - generic [ref=e379]:
                        - generic [ref=e380]: "\"proxy\""
                        - text: ": {},"
                      - generic [ref=e381]:
                        - generic [ref=e382]: "\"review_policy\""
                        - text: ": {},"
                      - generic [ref=e383]:
                        - generic [ref=e384]: "\"viewport\""
                        - text: ": {}"
                      - generic [ref=e385]: "},"
                      - generic [ref=e386]:
                        - generic [ref=e387]: "\"bruno\""
                        - text: ": {"
                      - generic [ref=e388]:
                        - generic [ref=e389]: "\"limits\""
                        - text: ": {}"
                      - generic [ref=e390]: "},"
                      - generic [ref=e391]:
                        - generic [ref=e392]: "\"database\""
                        - text: ": {},"
                      - generic [ref=e393]:
                        - generic [ref=e394]: "\"error_handling\""
                        - text: ": {},"
                      - generic [ref=e395]:
                        - generic [ref=e396]: "\"eventhubs\""
                        - text: ": {},"
                      - generic [ref=e397]:
                        - generic [ref=e398]: "\"grpc\""
                        - text: ": {},"
                      - generic [ref=e399]:
                        - generic [ref=e400]: "\"http\""
                        - text: ": {"
                      - generic [ref=e401]:
                        - generic [ref=e402]: "\"auth\""
                        - text: ": {},"
                      - generic [ref=e403]:
                        - generic [ref=e404]: "\"proxy\""
                        - text: ": {},"
                      - generic [ref=e405]:
                        - generic [ref=e406]: "\"tls\""
                        - text: ": {}"
                      - generic [ref=e407]: "},"
                      - generic [ref=e408]:
                        - generic [ref=e409]: "\"java_http\""
                        - text: ": {},"
                      - generic [ref=e410]:
                        - generic [ref=e411]: "\"java_vuser\""
                        - text: ": {},"
                      - generic [ref=e412]:
                        - generic [ref=e413]: "\"jms\""
                        - text: ": {},"
                      - generic [ref=e414]:
                        - generic [ref=e415]: "\"kafka\""
                        - text: ": {},"
                      - generic [ref=e416]:
                        - generic [ref=e417]: "\"log\""
                        - text: ": {},"
                      - generic [ref=e418]:
                        - generic [ref=e419]: "\"network\""
                        - text: ": {},"
                      - generic [ref=e420]:
                        - generic [ref=e421]: "\"observability\""
                        - text: ": {},"
                      - generic [ref=e422]:
                        - generic [ref=e423]: "\"pacing\""
                        - text: ": {"
                      - generic [ref=e424]:
                        - generic [ref=e425]: "\"type\""
                        - text: ": \"\""
                      - generic [ref=e426]: "},"
                      - generic [ref=e427]:
                        - generic [ref=e428]: "\"results\""
                        - text: ": {},"
                      - generic [ref=e429]:
                        - generic [ref=e430]: "\"run_logic\""
                        - text: ": {},"
                      - generic [ref=e431]:
                        - generic [ref=e432]: "\"sapgui\""
                        - text: ": {},"
                      - generic [ref=e433]:
                        - generic [ref=e434]: "\"soap\""
                        - text: ": {"
                      - generic [ref=e435]:
                        - generic [ref=e436]: "\"fault_handling\""
                        - text: ": {}"
                      - generic [ref=e437]: "},"
                      - generic [ref=e438]:
                        - generic [ref=e439]: "\"think_time\""
                        - text: ": {"
                      - generic [ref=e440]:
                        - generic [ref=e441]: "\"mean_ms\""
                        - text: ": 0,"
                      - generic [ref=e442]:
                        - generic [ref=e443]: "\"type\""
                        - text: ": \"\""
                      - generic [ref=e444]: "},"
                      - generic [ref=e445]:
                        - generic [ref=e446]: "\"web_runtime\""
                        - text: ": {"
                      - generic [ref=e447]:
                        - generic [ref=e448]: "\"data_formatting\""
                        - text: ": {},"
                      - generic [ref=e449]:
                        - generic [ref=e450]: "\"download_filters\""
                        - text: ": {},"
                      - generic [ref=e451]:
                        - generic [ref=e452]: "\"network_simulation\""
                        - text: ": {},"
                      - generic [ref=e453]:
                        - generic [ref=e454]: "\"streaming\""
                        - text: ": {}"
                      - generic [ref=e455]: "},"
                      - generic [ref=e456]:
                        - generic [ref=e457]: "\"websocket\""
                        - text: ": {}"
                      - generic [ref=e458]: "},"
                      - generic [ref=e459]: "\"goal_config\": {"
                      - generic [ref=e460]: "\"max_error_count\": 17,"
                      - generic [ref=e461]: "\"max_error_pct\": 0,"
                      - generic [ref=e462]: "\"max_vusers\": 6,"
                      - generic [ref=e463]: "\"min_vusers\": 1,"
            - generic [ref=e465]:
              - generic [ref=e466]:
                - generic [ref=e467]:
                  - generic: Insights
                  - tablist "Script JSON companion panel" [ref=e468]:
                    - tab "Insights" [selected] [ref=e469] [cursor=pointer]
                    - tab "Parameters" [ref=e470] [cursor=pointer]
                - button "Collapse right sidebar" [expanded] [ref=e471] [cursor=pointer]:
                  - img [ref=e472]
                  - generic [ref=e474]: Collapse
              - generic [ref=e475]:
                - generic [ref=e476]:
                  - strong [ref=e477]: Problems
                  - generic [ref=e478]:
                    - generic [ref=e479]: 0 errors
                    - generic [ref=e480]: •
                    - generic [ref=e481]: 0 warnings
                - generic [ref=e482]: No script-level issues detected.
              - paragraph [ref=e483]: Use Checks & readiness above to review validation and runtime evidence.
              - group [ref=e484]:
                - generic "Repair and import review" [ref=e485] [cursor=pointer]
              - group [ref=e486]:
                - generic "Data & variable details" [ref=e487] [cursor=pointer]
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
  9   |   await page.setViewportSize({ width: 1600, height: 1000 })
  10  |   const request = page.context().request
  11  |   const workspace = await createWorkspace(request)
  12  |   const leaf = { name: 'Check greeting', type: 'browser_wait', target: '#result', wait_for: { type: 'selector', value: '#result', timeout_ms: 10000 }, metadata: { audit_marker: 'preserve' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Welcome' }] }
  13  |   const script = { name: 'Nested greeting', version: '1.0.0', protocol: 'browser', config: {}, actions: [{ name: 'Repeat journey', type: 'control_loop', control: { count: 1, steps: [leaf] } }] }
  14  |   const created = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })
  15  |   expect(created.ok(), await created.text()).toBeTruthy()
  16  |   const payload = await created.json(); const id = payload.script?.id || payload.id
  17  |   let runs = 0
  18  |   let replayExpected = 'Welcome'
  19  |   await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
  20  |     const body = route.request().postDataJSON(); runs++
  21  |     const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  22  |     replayExpected = saved.script.actions[0].control.steps[0].assertions[0].expected
  23  |     const now = new Date().toISOString()
  24  |     const passed = replayExpected === 'Hello'
  25  |     // Go omits the zero action index in JSON. Repair must still find the first root.
  26  |     const event = { phase: 'action', step_path: ['loop[0]'], iteration: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: passed, duration_ms: 1, started_at: now, completed_at: now, error_message: passed ? '' : 'Greeting mismatch', protocol_evidence: passed ? {} : { assertion_failure: { type: 'element_text_contains', expected: 'Welcome', actual: 'Hello' } } }
  27  |     const report = { replay_session_id: body.replay_session_id, group: 'Nested greeting', protocol: 'browser', started_at: now, completed_at: now, success: passed, events: [event] }
  28  |     const sessionIncarnation = randomUUID()
  29  |     const runtimeState = { version: 1, kind: 'full', session_id: body.replay_session_id,
  30  |       session_incarnation: sessionIncarnation, state_revision: '1', state: 'unavailable',
  31  |       reason: 'session_terminal', snapshot: null } satisfies ScriptDebugRuntimeState
  32  |     await route.fulfill({ json: { debug_session_id: body.replay_session_id, replay_session_id: body.replay_session_id,
  33  |       session: { id: body.replay_session_id, session_incarnation: sessionIncarnation, state_revision: '1',
  34  |         runtime_state: runtimeState, status: passed ? 'completed' : 'failed', completed_at: now, report } } })
  35  |   })
  36  |   await page.setViewportSize({ width: 1440, height: 1000 })
  37  |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  38  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  39  |   await page.locator('.topbar-debug-action-run').click()
  40  |   await expect(page.getByRole('button', { name: 'Edit this assertion' })).toBeVisible()
  41  |   await expect(page.locator('.script-browser-evidence')).toHaveCount(1)
  42  |   await expect(page.getByRole('region', { name: 'Debug detail', exact: true })).toHaveCount(0)
  43  |   await page.getByRole('button', { name: 'Edit this assertion' }).click()
  44  |   await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  45  |   const expected = page.getByLabel('Assertion 1 expected value', { exact: true })
  46  |   await expect(expected).toHaveValue('Welcome')
  47  |   await expect(expected).toBeFocused()
  48  |   await expected.fill('Hello')
  49  |   await expect(page.getByLabel('Script evidence states')).toContainText('stale')
  50  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  51  |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  52  |   const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  53  |   expect(saved.script.actions[0].control.steps[0]).toMatchObject({ metadata: { audit_marker: 'preserve' }, wait_for: leaf.wait_for, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Hello' }] })
  54  |   await page.locator('.topbar-debug-action-run').click()
  55  |   await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
  56  |   expect(runs).toBe(2)
  57  |   expect(replayExpected).toBe('Hello')
  58  |   await page.screenshot({ path: testInfo.outputPath('script-debug-evidence.png'), fullPage: true })
  59  | })
  60  | 
  61  | test('authored script goals survive an unrelated editor save and canonical reopen', async ({ page }, testInfo) => {
  62  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  63  |   await page.setViewportSize({ width: 1600, height: 1000 })
  64  |   const request = page.context().request
  65  |   const workspace = await createWorkspace(request)
  66  |   const goals = {
  67  |     target_average_tps: 8.75, p95_latency_ms: 217.5, max_error_pct: 0,
  68  |     min_vusers: 1, max_vusers: 6, max_error_count: 17,
  69  |     stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '11s',
  70  |     transaction_rules: [{ population: 'transaction', protocol: 'http', group: 'checkout', transaction: 'Purchase', metric: 'p95_duration_ms', comparator: 'lte', threshold: 215.25, severity: 'fail' }],
  71  |   }
  72  |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
  73  |     project_id: workspace.projectId,
  74  |     script: { name: 'Durable script goals', version: '1.0', protocol: 'http', goal_config: goals,
  75  |       config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Purchase', type: 'request', method: 'GET', target: '/' }] },
  76  |   } })))
  77  |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  78  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  79  |   const workbench = page.getByRole('region', { name: 'Author center workbench' })
  80  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  81  |   const baseURL = workbench.getByRole('textbox', { name: 'Base URL' })
  82  |   await expect(baseURL).toHaveValue('http://127.0.0.1:1')
  83  |   await baseURL.fill('http://127.0.0.1:2')
  84  |   const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  85  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  86  |   await json(await saved)
  87  |   await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  88  |   const canonical = await json(await request.get(`/api/v1/scripts/${id}`))
  89  |   expect(canonical.script).toMatchObject({ goal_config: goals, config: { base_url: 'http://127.0.0.1:2' } })
  90  |   await page.reload()
  91  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  92  |   await expect(baseURL).toHaveValue('http://127.0.0.1:2')
  93  |   await workbench.getByRole('tab', { name: 'Source' }).click()
  94  |   const code = page.locator('.script-code-pane')
  95  |   // With no action selected, Source already owns the complete canonical JSON.
  96  |   // Its virtualized code view must navigate to the authored goals to inspect
  97  |   // them; no conditional action-scope toggle is present on this entry path.
  98  |   await code.getByRole('button', { name: 'Find', exact: true }).click()
  99  |   const find = code.getByPlaceholder('Find', { exact: true })
> 100 |   await find.pressSequentially('goal_config')
      |              ^ TimeoutError: locator.pressSequentially: Timeout 15000ms exceeded.
  101 |   await code.getByRole('button', { name: 'next', exact: true }).click()
  102 |   await expect(code.locator('.cm-content')).toContainText('goal_config')
  103 |   await find.fill('')
  104 |   await find.pressSequentially('217.5')
  105 |   await code.getByRole('button', { name: 'next', exact: true }).click()
  106 |   await expect(code.locator('.cm-content')).toContainText('217.5')
  107 |   await find.press('Escape')
  108 |   await page.screenshot({ path: testInfo.outputPath('canonical-goals-reopen.png'), fullPage: true })
  109 | })
  110 | 
  111 | 
  112 | test('two editors preserve stale drafts and require explicit revision review before reapply', async ({ page }, testInfo) => {
  113 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  114 |   await page.setViewportSize({ width: 1600, height: 1000 })
  115 |   const request = page.context().request
  116 |   const workspace = await createWorkspace(request)
  117 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
  118 |     project_id: workspace.projectId,
  119 |     script: { name: 'Revision fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] },
  120 |   } })))
  121 |   const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  122 |   expect(initial.ok()).toBeTruthy()
  123 |   const initialTag = initial.headers().etag
  124 |   expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
  125 |   const other = await page.context().newPage()
  126 |   await other.setViewportSize({ width: 1600, height: 1000 })
  127 |   try {
  128 |     const route = `/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`
  129 |     await page.goto(route)
  130 |     await other.goto(route)
  131 |     if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') { await page.reload(); await other.reload() }
  132 |     const firstName = page.locator('.side-panel-script-name input')
  133 |     const staleName = other.locator('.side-panel-script-name input')
  134 |     await expect(firstName).toHaveValue('Revision fixture')
  135 |     await expect(staleName).toHaveValue('Revision fixture')
  136 |     await firstName.fill('First editor saved')
  137 |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  138 |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  139 |     await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:2')
  140 |     const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  141 |     await page.getByRole('button', { name: 'Save script', exact: true }).click()
  142 |     const response = await saved
  143 |     expect(response.request().headers()['if-match']).toBe(initialTag)
  144 |     expect(response.headers()['x-apex-script-save-mode']).toBe('conditioned')
  145 |     await json(response)
  146 |     await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  147 |     await staleName.fill('Retained stale draft')
  148 |     let writes = 0
  149 |     other.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) writes++ })
  150 |     const rejected = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
  151 |     await other.getByRole('button', { name: 'Save script', exact: true }).click()
  152 |     const conflictResponse = await rejected
  153 |     expect(conflictResponse.status()).toBe(412)
  154 |     expect(conflictResponse.request().headers()['if-match']).toBe(initialTag)
  155 |     const conflict = other.getByRole('region', { name: 'Script save reconciliation' })
  156 |     await expect(conflict).toBeVisible()
  157 |     await expect(staleName).toHaveValue('Retained stale draft')
  158 |     await expect(other.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  159 |     expect(writes).toBe(1)
  160 |     await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  161 |     await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
  162 |     await expect(staleName).toHaveValue('Retained stale draft')
  163 |     await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  164 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('name')
  165 |     await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('Retained stale draft')
  166 |     expect(writes).toBe(1)
  167 |     await other.setViewportSize({ width: 760, height: 1000 })
  168 |     await conflict.scrollIntoViewIfNeeded()
  169 |     const box = await conflict.boundingBox()
  170 |     expect(box).not.toBeNull()
  171 |     expect(box!.x).toBeGreaterThanOrEqual(0)
  172 |     expect(box!.x + box!.width).toBeLessThanOrEqual(760)
  173 |     await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeInViewport()
  174 |     await conflict.screenshot({ path: testInfo.outputPath('script-revision-conflict.png') })
  175 |     await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  176 |     await other.setViewportSize({ width: 1600, height: 1000 })
  177 |     await expect(conflict).toHaveCount(0)
  178 |     await expect(staleName).toHaveValue('Retained stale draft')
  179 |     expect(writes).toBe(1)
  180 |     const reappliedSave = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
  181 |     await other.getByRole('button', { name: 'Save script', exact: true }).click()
  182 |     const reappliedResponse = await reappliedSave
  183 |     expect(reappliedResponse.request().headers()['if-match']).not.toBe(initialTag)
  184 |     expect(reappliedResponse.headers()['x-apex-script-save-mode']).toBe('conditioned')
  185 |     await json(reappliedResponse)
  186 |     const final = await json(await request.get(`/api/v1/scripts/${id}`))
  187 |     expect(final.script).toMatchObject({ name: 'Retained stale draft', config: { base_url: 'http://127.0.0.1:2' } })
  188 |     expect(writes).toBe(2)
  189 |   } finally { await other.close() }
  190 | })
  191 | 
  192 | test('placement-only changes invalidate save authority and explicit reapply preserves the current folder', async ({ page }) => {
  193 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  194 |   await page.setViewportSize({ width: 1600, height: 1000 })
  195 |   const request = page.context().request
  196 |   const workspace = await createWorkspace(request)
  197 |   const folder = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, { data: { name: 'Current placement', category: 'scripts' } })))
  198 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Placement fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] } } })))
  199 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  200 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
```