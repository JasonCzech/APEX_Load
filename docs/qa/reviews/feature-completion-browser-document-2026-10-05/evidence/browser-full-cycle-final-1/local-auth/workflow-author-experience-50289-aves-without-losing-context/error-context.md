# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-author-experience.spec.ts >> Author creates, reverses, validates, edits source and saves without losing context
- Location: e2e/workflow-author-experience.spec.ts:4:1

# Error details

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('dialog', { name: 'Author Problems', exact: true }).getByRole('button', { name: /Action target is required/ })

```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. e2e-1791197708219-580264 domain / e2e-1791197708219-580264 project / e2e-1791197708219-580264 application / e2e-1791197708219-580264 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: e2e-1791197708219-580264 domain / e2e-1791197708219-580264 project / e2e-1791197708219-580264 application / e2e-1791197708219-580264 1.0
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
          - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e113]:
            - generic [ref=e114]: LS
            - generic [ref=e115]:
              - generic [ref=e116]: Local System Administrator
              - generic [ref=e117]: System Admin
              - button "Sign out" [ref=e118] [cursor=pointer]
          - generic [ref=e119]:
            - generic "Live" [ref=e120]:
              - generic [ref=e122]: Live
            - 'button "Build info: local · dev" [ref=e124] [cursor=pointer]':
              - generic [ref=e126]: Version Info
        - button "Collapse sidebar" [expanded] [ref=e128] [cursor=pointer]:
          - img [ref=e129]
    - main "Author_operator_journey" [ref=e131]:
      - status [ref=e132]: Author_operator_journey
      - generic "Dashboard controls" [ref=e133]:
        - heading "Author_operator_journey" [level=1] [ref=e135]
        - group "Script workspace" [ref=e137]:
          - button "Author" [pressed] [ref=e138] [cursor=pointer]
          - button "Debug" [ref=e139] [cursor=pointer]
        - generic [ref=e140]:
          - link "Help for this page" [ref=e141] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=authoring"
          - generic "Contextual secondary actions" [ref=e142]:
            - button "Save script" [ref=e144] [cursor=pointer]:
              - generic [ref=e145]: Save
            - button "Debug one iteration" [ref=e147] [cursor=pointer]:
              - img [ref=e149]
              - generic [ref=e151]: Debug one iteration
          - generic "Deployment identity" [ref=e152]:
            - generic [ref=e153]: Browser · Server
          - combobox "Dashboard color theme" [ref=e156]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e157]:
        - region "Object history" [ref=e158]:
          - generic [ref=e159]: Created by Local System Administrator · Last modified by Unknown
          - group [ref=e160]:
            - generic "History" [ref=e161] [cursor=pointer]
        - generic [ref=e162]:
          - generic "Script evidence states" [ref=e163]:
            - button "Outline" [pressed] [ref=e164] [cursor=pointer]
            - button "Close script editor" [ref=e165] [cursor=pointer]: Close editor
            - button "Reset layout" [ref=e166] [cursor=pointer]
            - button "Undo document change" [ref=e168] [cursor=pointer]:
              - img [ref=e169]
            - group "Redo document change" [ref=e172]:
              - button "Redo document change" [disabled] [ref=e173]:
                - img [ref=e174]
            - button "Review changes" [ref=e177] [cursor=pointer]
            - generic [ref=e178]: "Saved: unsaved changes"
            - generic [ref=e179]: "Validation evidence: not yet"
            - generic [ref=e180]: "Debugged: missing"
            - generic [ref=e181]: "Approval readiness: needs evidence"
            - button "Author Problems (0)" [ref=e182] [cursor=pointer]
            - group [ref=e183]:
              - generic "Checks & readiness" [ref=e184] [cursor=pointer]
          - generic [ref=e186]: Redid the document change.
          - tabpanel "Authoring workspace" [ref=e188]:
            - generic [ref=e189]:
              - separator "Resize outline panel (double-click to fit content)" [ref=e190]
              - generic [ref=e191]:
                - button "Collapse outline side panel" [expanded] [ref=e195] [cursor=pointer]:
                  - img [ref=e196]
                  - generic [ref=e198]: Collapse
                - generic "Script identity" [ref=e199]:
                  - generic [ref=e200]:
                    - generic [ref=e201]: Script Name
                    - textbox "Script Name" [ref=e202]:
                      - /placeholder: My_Test_Script
                      - text: Author_operator_journey
                - navigation "Script outline" [ref=e203]:
                  - generic [ref=e204]:
                    - text: Find action
                    - searchbox "Find action" [ref=e205]
                  - generic [ref=e206]:
                    - heading "Init" [level=4] [ref=e207]
                    - text: No actions
                  - generic [ref=e208]:
                    - heading "Actions" [level=4] [ref=e209]
                    - button "Create order POST · Action 1" [ref=e212] [cursor=pointer]:
                      - strong [ref=e213]: Create order
                      - generic [ref=e214]: POST · Action 1
                    - generic [ref=e215]:
                      - button "Collapse Poll order" [expanded] [ref=e216] [cursor=pointer]: ▾
                      - button "Poll order Loop · Action 2" [ref=e218] [cursor=pointer]:
                        - strong [ref=e219]: Poll order
                        - generic [ref=e220]: Loop · Action 2
                    - button "Read order GET · Nested step loop[0]" [ref=e223] [cursor=pointer]:
                      - strong [ref=e224]: Read order
                      - generic [ref=e225]: GET · Nested step loop[0]
                    - button "Health check GET · Nested step loop[1]" [ref=e228] [cursor=pointer]:
                      - strong [ref=e229]: Health check
                      - generic [ref=e230]: GET · Nested step loop[1]
                  - generic [ref=e231]:
                    - heading "Teardown" [level=4] [ref=e232]
                    - text: No actions
                - group [ref=e233]:
                  - generic "Parameters & data (1)" [ref=e234] [cursor=pointer]
                - generic [ref=e236]:
                  - button "Script Configuration" [ref=e237] [cursor=pointer]
                  - button "Runtime Settings" [ref=e238] [cursor=pointer]
                  - button "Parameter Data" [ref=e239] [cursor=pointer]
              - region "Author center workbench" [ref=e240]:
                - tablist "Author center workbench tabs" [ref=e241]:
                  - tab "Action editor" [selected] [ref=e244] [cursor=pointer]:
                    - generic [ref=e246]: Action editor
                  - tab "Source" [ref=e249] [cursor=pointer]:
                    - generic [ref=e251]: Source
                  - generic [ref=e252]:
                    - tab "Runtime Settings" [ref=e254] [cursor=pointer]:
                      - generic [ref=e256]: Runtime Settings
                    - button "Close Runtime Settings" [ref=e258] [cursor=pointer]: ×
                - tabpanel "Action editor" [ref=e260]:
                  - region "Action editor" [ref=e261]:
                    - generic [ref=e262]:
                      - button "Edit selected action" [pressed] [ref=e263] [cursor=pointer]
                      - button "Overview & bulk" [ref=e264] [cursor=pointer]
                      - button "+ Add Action" [ref=e265] [cursor=pointer]
                    - generic [ref=e266]:
                      - paragraph [ref=e267]:
                        - text: Actions › Create order
                        - generic [ref=e268]: script.actions[0] · Current draft
                      - generic [ref=e269]:
                        - generic [ref=e270]:
                          - generic [ref=e271]: request
                          - heading "Create order" [level=3] [ref=e272]
                        - group "Action property sections" [ref=e273]:
                          - button "Request" [pressed] [ref=e274] [cursor=pointer]
                          - button "Correlation & checks (0)" [ref=e275] [cursor=pointer]
                          - button "Timing" [ref=e276] [cursor=pointer]
                          - button "Metadata" [ref=e277] [cursor=pointer]
                        - generic [ref=e278]:
                          - generic [ref=e279]:
                            - generic [ref=e280]:
                              - generic [ref=e281]: Name
                              - textbox "Name" [ref=e282]: Create order
                            - generic [ref=e283]:
                              - generic [ref=e284]: HTTP method
                              - combobox "HTTP method" [ref=e285]:
                                - option "GET"
                                - option "POST" [selected]
                                - option "PUT"
                                - option "PATCH"
                                - option "DELETE"
                                - option "HEAD"
                                - option "OPTIONS"
                            - generic [ref=e286]:
                              - generic [ref=e287]: Transaction
                              - textbox "Transaction" [ref=e288]:
                                - /placeholder: (none)
                            - generic [ref=e289]:
                              - generic [ref=e290]: Target (path)
                              - textbox "Target (path)" [ref=e291]:
                                - /placeholder: /api/resource
                                - text: /orders
                          - region "Request HTTP version policy" [ref=e292]:
                            - generic [ref=e293]: Request HTTP version
                            - combobox "Request HTTP version" [ref=e294]:
                              - option "Inherit script setting (Auto-negotiate (HTTP/2 or HTTP/1.1))" [selected]
                              - option "Auto-negotiate (HTTP/2 or HTTP/1.1)"
                              - option "Require HTTP/1.1"
                              - option "Require HTTP/2 (no fallback)"
                            - paragraph [ref=e295]: "Authored policy: Inherit script setting (Auto-negotiate (HTTP/2 or HTTP/1.1))."
                            - generic [ref=e296]:
                              - term [ref=e297]: Captured request HTTP version
                              - definition [ref=e298]: Not observed
                              - term [ref=e299]: Captured response HTTP version
                              - definition [ref=e300]: Not observed
                            - generic [ref=e301]: Captured versions are observations from the source. Replay shows its actual negotiated version separately. Requests keep the current virtual user’s cookies and authentication.
                          - generic [ref=e302]:
                            - generic [ref=e303]:
                              - heading "Headers" [level=4] [ref=e304]
                              - button "+ Add" [ref=e305] [cursor=pointer]
                            - generic [ref=e306]:
                              - textbox "Header Content-Type name" [ref=e307]:
                                - /placeholder: Key
                                - text: Content-Type
                              - textbox "Header Content-Type value" [ref=e308]:
                                - /placeholder: Value
                                - text: application/json
                              - button "Remove header Content-Type" [ref=e309] [cursor=pointer]: ✕
                          - generic [ref=e310]:
                            - heading "Request body" [level=4] [ref=e311]
                            - paragraph [ref=e312]: 33 characters · JSON
                            - button "Edit request body in center" [ref=e313] [cursor=pointer]
              - generic [ref=e314]:
                - generic [ref=e315]:
                  - generic [ref=e316]: Action context
                  - button "Collapse right sidebar" [expanded] [ref=e317] [cursor=pointer]:
                    - img [ref=e318]
                    - generic [ref=e320]: Collapse
                - generic [ref=e321]:
                  - heading "Selected action" [level=3] [ref=e322]
                  - paragraph [ref=e323]: Create order
                  - paragraph [ref=e324]: Edit properties in the main workbench. Inspect Source for the serialized action.
                  - button "Inspect action source" [ref=e325] [cursor=pointer]
                  - button "Edit action" [ref=e326] [cursor=pointer]
  - button "Close Author Problems" [ref=e327]:
    - dialog "Author Problems" [ref=e328]:
      - generic [ref=e329]:
        - generic [ref=e330]:
          - heading "Author Problems" [level=3] [ref=e331]
          - paragraph [ref=e332]: 0 specific issues require attention.
        - button "Close" [active] [ref=e333] [cursor=pointer]
      - region "Author Problems, items 0 through 0 of 0" [ref=e334]
      - navigation "Author Problems pages" [ref=e335]:
        - generic [ref=e336]: No issues to display.
```

# Test source

```ts
  1  | import { test, expect, idOf, json, login } from './support/workflow'
  2  | import { createWorkspace, scopeQuery } from './support/workspace'
  3  | 
  4  | test('Author creates, reverses, validates, edits source and saves without losing context', async ({ page }, testInfo) => {
  5  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  6  |   const request = page.context().request
  7  |   const workspace = await createWorkspace(request)
  8  |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
  9  |     name: 'Author_operator_journey', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:18787' },
  10 |     parameters: [{ name: 'customer', type: 'list', values: ['sample'] }],
  11 |     actions: [{ name: 'Create order', type: 'request', method: 'POST', target: '/orders', headers: { 'Content-Type': 'application/json' }, body: '{"customer":"{{param.customer}}"}' },
  12 |       { name: 'Poll order', type: 'control_loop', control: { count: 2, steps: [{ name: 'Read order', type: 'request', method: 'GET', target: '/orders/1' }] } }],
  13 |   } } })))
  14 |   await page.setViewportSize({ width: 1600, height: 1000 })
  15 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  16 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  17 |   await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  18 |   const outline = page.getByRole('navigation', { name: 'Script outline' })
  19 |   await outline.getByRole('button', { name: /Poll order.*Loop/ }).click()
  20 |   const editor = page.locator('.author-main-action')
  21 |   await expect(editor.getByRole('button', { name: 'Control flow', exact: true })).toBeVisible()
  22 |   await expect(editor.getByLabel('Target (path)', { exact: true })).toHaveCount(0)
  23 |   await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  24 |   let insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  25 |   await expect(insertion.getByLabel('Action name', { exact: true })).toBeFocused()
  26 |   await insertion.getByRole('button', { name: 'Cancel', exact: true }).click()
  27 |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  28 |   await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  29 |   insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  30 |   await insertion.getByLabel('Placement', { exact: true }).selectOption('inside')
  31 |   await insertion.getByLabel('Action name', { exact: true }).fill('Health check')
  32 |   await insertion.getByLabel('Target (required)', { exact: true }).fill('/health')
  33 |   await insertion.getByRole('button', { name: 'Insert action', exact: true }).click()
  34 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  35 |   await expect(editor.locator('.inspector-selection-path')).toContainText('Actions › Poll order › Health check')
  36 |   await expect(outline.getByRole('button', { name: /Health check.*GET/ })).toHaveAttribute('aria-current', 'true')
  37 |   await page.getByRole('button', { name: 'Undo document change', exact: true }).click()
  38 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Poll order')
  39 |   await page.getByRole('button', { name: 'Redo document change', exact: true }).click()
  40 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  41 |   await editor.getByLabel('Target (path)', { exact: true }).fill('')
  42 |   await page.getByRole('button', { name: /^Author Problems \(/ }).click()
  43 |   const problems = page.getByRole('dialog', { name: 'Author Problems', exact: true })
  44 |   await expect(problems.getByRole('button', { name: /Action target is required/ })).toHaveCount(1)
  45 |   await problems.getByRole('button', { name: /Action target is required/ }).click()
  46 |   await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  47 |   await editor.getByLabel('Target (path)', { exact: true }).fill('/health')
  48 |   await outline.getByRole('button', { name: /Create order.*POST/ }).click()
  49 |   await editor.getByLabel('Target (path)', { exact: true }).fill('')
  50 |   await page.getByRole('button', { name: /^Author Problems \(/ }).click()
> 51 |   await page.getByRole('dialog', { name: 'Author Problems', exact: true }).getByRole('button', { name: /Action target is required/ }).click()
     |                                                                                                                                       ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
  52 |   await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  53 |   await editor.getByLabel('Target (path)', { exact: true }).fill('/orders')
  54 |   await editor.getByRole('button', { name: 'Edit request body in center' }).click()
  55 |   const body = page.getByRole('region', { name: 'Request body editor' })
  56 |   await expect(body.getByLabel('Editor language')).toHaveValue('json')
  57 |   await expect(body.locator('.cm-editor')).not.toHaveCSS('background-color', 'rgb(255, 255, 255)')
  58 |   await body.getByRole('button', { name: 'Check syntax', exact: true }).click()
  59 |   await expect(body).toContainText('Syntax is valid')
  60 |   await page.getByRole('tab', { name: 'Source', exact: true }).click()
  61 |   await page.getByRole('button', { name: 'Edit in Full Script JSON' }).click()
  62 |   await page.getByRole('button', { name: 'Edit JSON', exact: true }).click()
  63 |   const source = page.locator('.script-code-pane .cm-content[contenteditable="true"]')
  64 |   await source.fill('{')
  65 |   await expect(page.getByRole('button', { name: 'Apply changes', exact: true })).toBeDisabled()
  66 |   await expect(page.getByRole('button', { name: 'Discard JSON edits', exact: true })).toBeVisible()
  67 |   await page.getByRole('button', { name: 'Discard JSON edits', exact: true }).click()
  68 |   await page.getByRole('tab', { name: 'Action editor', exact: true }).click()
  69 |   await editor.getByLabel('Name', { exact: true }).fill('Create order revised')
  70 |   await page.getByRole('button', { name: 'Review changes', exact: true }).click()
  71 |   await expect(page.getByRole('dialog', { name: 'Changes since last save' })).toContainText('Create order revised')
  72 |   await page.getByRole('dialog', { name: 'Changes since last save' }).getByRole('button', { name: 'Close', exact: true }).click()
  73 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  74 |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  75 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order revised')
  76 |   await page.setViewportSize({ width: 900, height: 850 })
  77 |   await expect(editor.getByLabel('Name', { exact: true })).toBeVisible()
  78 |   await expect(page.getByRole('button', { name: 'Undo document change', exact: true })).toBeVisible()
  79 |   await expect(page.getByRole('button', { name: 'Undo document change', exact: true }).locator('svg')).toHaveCSS('fill', 'none')
  80 |   await page.screenshot({ path: testInfo.outputPath('author-operator-experience.png'), fullPage: true })
  81 | })
  82 | 
```