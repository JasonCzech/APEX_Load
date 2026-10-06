# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-authoring.spec.ts >> [U07 U18] failed script save (500) preserves edits and cannot overwrite stored content
- Location: e2e/workflow-authoring.spec.ts:51:3

# Error details

```
TimeoutError: page.waitForResponse: Timeout 15000ms exceeded while waiting for event "response"
```

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('button', { name: 'Save script', exact: true })
    - locator resolved to <button disabled type="button" aria-label="Save script" class="btn btn-sm topbar-action-button btn-primary" data-tooltip="Load and review a protected authoring baseline before saving">…</button>
  - attempting click action
    2 × waiting for element to be visible, enabled and stable
      - element is not enabled
    - retrying click action
    - waiting 20ms
    2 × waiting for element to be visible, enabled and stable
      - element is not enabled
    - retrying click action
      - waiting 100ms
    28 × waiting for element to be visible, enabled and stable
       - element is not enabled
     - retrying click action
       - waiting 500ms

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791188900880-674880 domain / e2e-1791188900880-674880 project / e2e-1791188900880-674880 application / e2e-1791188900880-674880 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791188900880-674880 domain / e2e-1791188900880-674880 project / e2e-1791188900880-674880 application / e2e-1791188900880-674880 1.0
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
  - main "Workflow_HTTP" [ref=e125]:
    - status [ref=e126]: Workflow_HTTP
    - generic "Dashboard controls" [ref=e127]:
      - heading "Workflow_HTTP" [level=1] [ref=e129]
      - generic "Current context" [ref=e130]:
        - button "View 1 script issue detail" [ref=e132] [cursor=pointer]: 1 issue
      - group "Script workspace" [ref=e134]:
        - button "Author" [pressed] [ref=e135] [cursor=pointer]
        - button "Debug" [ref=e136] [cursor=pointer]
      - generic [ref=e137]:
        - link "Help for this page" [ref=e138] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=authoring"
        - generic "Contextual secondary actions" [ref=e139]:
          - group "Load and review a protected authoring baseline before saving" [ref=e140]:
            - button "Save script" [disabled] [ref=e141]:
              - generic [ref=e142]: Save
          - button "Debug" [ref=e144] [cursor=pointer]:
            - img [ref=e146]
            - generic [ref=e148]: Debug
        - generic "Deployment identity" [ref=e149]:
          - generic [ref=e150]: Browser · Server
        - combobox "Dashboard color theme" [ref=e153]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - generic [ref=e154]:
      - region "Object history" [ref=e155]:
        - generic [ref=e156]: Created by Development Mode · Last modified by Unknown
        - group [ref=e157]:
          - generic "History" [ref=e158] [cursor=pointer]
      - generic [ref=e159]:
        - generic "Script evidence states" [ref=e160]:
          - button "Close script editor" [ref=e161] [cursor=pointer]: Close editor
          - button "Reset layout" [ref=e162] [cursor=pointer]
          - button "Undo document change" [ref=e164] [cursor=pointer]:
            - img [ref=e165]
          - group "Redo document change" [ref=e168]:
            - button "Redo document change" [disabled] [ref=e169]:
              - img [ref=e170]
          - button "Review changes" [ref=e173] [cursor=pointer]
          - generic [ref=e174]: "Saved: unsaved changes"
          - generic [ref=e175]: "Validation evidence: not yet"
          - generic [ref=e176]: "Debugged: missing"
          - generic [ref=e177]: "Approval readiness: needs evidence"
          - button "Author Problems (1)" [ref=e178] [cursor=pointer]
          - group [ref=e179]:
            - generic "Checks & readiness" [ref=e180] [cursor=pointer]
        - region "Script save reconciliation" [ref=e181]:
          - heading "Review the current script before saving" [level=3] [ref=e182]
          - alert [ref=e183]: The save outcome or revision protection could not be verified. Your draft is retained. Load the current baseline before reviewing and reapplying it; do not retry automatically.
          - paragraph [ref=e184]: Your editable draft is retained. Loading the current baseline does not retry a save or replace the draft.
          - generic [ref=e185]:
            - button "Load current baseline" [ref=e186] [cursor=pointer]
            - button "Diff" [disabled] [ref=e187]
            - button "Reapply draft" [disabled] [ref=e188]
            - button "Reload script and discard draft" [disabled] [ref=e189]
        - generic [ref=e191]: "Workflow test: save rejected; retry after recovery."
        - tabpanel "Authoring workspace" [ref=e193]:
          - region "Author center workbench" [ref=e195]:
            - tablist "Author center workbench tabs" [ref=e196]:
              - tab "Action editor" [ref=e199] [cursor=pointer]:
                - generic [ref=e201]: Action editor
              - tab "Source" [ref=e204] [cursor=pointer]:
                - generic [ref=e206]: Source
              - generic [ref=e207]:
                - tab "Runtime Settings" [selected] [ref=e209] [cursor=pointer]:
                  - generic [ref=e211]: Runtime Settings
                - button "Close Runtime Settings" [ref=e213] [cursor=pointer]: ×
            - tabpanel "Runtime Settings" [ref=e215]:
              - region "Runtime Settings" [ref=e216]:
                - region "Effective runtime context" [ref=e217]:
                  - generic [ref=e218]:
                    - strong [ref=e219]: "Editing: Script defaults"
                    - generic [ref=e220]:
                      - text: Preview
                      - combobox "Runtime preview context" [ref=e221]:
                        - option "Script defaults" [selected]
                        - option "Current Debug options"
                    - generic [ref=e222]:
                      - text: Find setting
                      - searchbox "Find setting" [ref=e223]
                    - generic [ref=e224] [cursor=pointer]:
                      - checkbox "Modified only" [ref=e225]
                      - text: Modified only
                  - group [ref=e226]:
                    - generic "Context details · 1 action timing overrides" [ref=e227] [cursor=pointer]
                - generic [ref=e228]:
                  - navigation "Runtime setting categories" [ref=e229]:
                    - tablist "Runtime settings sections" [ref=e230]:
                      - tab "Script Configuration" [selected] [ref=e231] [cursor=pointer]:
                        - img [ref=e233]
                        - generic [ref=e235]: Script Configuration
                      - tab "Run Logic" [ref=e236] [cursor=pointer]:
                        - img [ref=e238]
                        - generic [ref=e243]: Run Logic
                      - tab "Iteration Pacing" [ref=e244] [cursor=pointer]:
                        - img [ref=e246]
                        - generic [ref=e249]: Iteration Pacing
                      - tab "Think Time" [ref=e250] [cursor=pointer]:
                        - img [ref=e252]
                        - generic [ref=e255]: Think Time
                      - tab "HTTP Transport" [ref=e256] [cursor=pointer]:
                        - img [ref=e258]
                        - generic [ref=e262]: HTTP Transport
                      - tab "Browser Emulation" [ref=e263] [cursor=pointer]:
                        - img [ref=e265]
                        - generic [ref=e269]: Browser Emulation
                      - tab "Error Handling" [ref=e270] [cursor=pointer]:
                        - img [ref=e272]
                        - generic [ref=e274]: Error Handling
                      - tab "Logging" [ref=e275] [cursor=pointer]:
                        - img [ref=e277]
                        - generic [ref=e279]: Logging
                      - tab "Web HTTP/HTML Compatibility" [ref=e280] [cursor=pointer]:
                        - img [ref=e282]
                        - generic [ref=e284]: Web HTTP/HTML Compatibility
                  - tabpanel "Script Configuration" [ref=e285]:
                    - heading "Script Configuration" [level=3] [ref=e286]
                    - generic [ref=e289]:
                      - generic [ref=e290]:
                        - generic [ref=e291]: Protocol
                        - generic "Protocol" [ref=e292]:
                          - strong [ref=e293]: HTTP API
                          - generic [ref=e294]: http
                      - generic [ref=e295]:
                        - generic [ref=e296]: Version
                        - textbox "Version" [ref=e297]:
                          - /placeholder: 1.0.0
                          - text: 1.0.0
                      - generic [ref=e298]:
                        - generic [ref=e299]: Base URL
                        - textbox "Base URL" [ref=e300]:
                          - /placeholder: https://api.example.com
                          - text: http://127.0.0.1:12345
                    - group [ref=e301]:
                      - generic "Value origin & execution support Script Configuration" [ref=e302] [cursor=pointer]:
                        - generic [ref=e303]:
                          - img [ref=e305]
                          - strong [ref=e307]: Value origin & execution support
                          - generic [ref=e308]: Script Configuration
```

# Test source

```ts
  1   | import { test, expect, seedScript, mutation, json } from './support/workflow'
  2   | import { createWorkspace, scopeQuery } from './support/workspace'
  3   | 
  4   | // Server-backed tests: response interception is used only to inject a named
  5   | // failure, never to manufacture a successful workflow.
  6   | test('[U03 U08] empty setup explains prerequisites and invalid saves do not persist', async ({ page, request }) => {
  7   |   const workspace = await createWorkspace(request)
  8   |   await page.goto(`/#/new-test?${scopeQuery(workspace)}`)
  9   |   await expect(page.getByText(/No scenarios are available yet/)).toBeVisible()
  10  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  11  |   await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  12  |   await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeDisabled()
  13  |   await expect(page.getByRole('heading', { level: 4, name: 'Scripts And Groups' })).toBeVisible()
  14  |   await page.getByPlaceholder('Scenario name', { exact: true }).fill('Preserve this incomplete draft')
  15  |   await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeEnabled()
  16  |   await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  17  |   await expect(page.getByText('No existing scripts are available for this project.')).toBeVisible()
  18  |   await expect(page.getByRole('searchbox', { name: 'Search scripts', exact: false })).toBeFocused()
  19  |   await page.getByRole('dialog', { name: 'Choose Script' }).getByRole('button', { name: 'Cancel' }).click()
  20  |   await page.locator('nav.sidebar-nav').getByRole('button', { name: 'Scripts', exact: true }).click()
  21  |   await page.getByRole('dialog', { name: 'Unsaved Changes' }).getByRole('button', { name: 'Keep editing' }).click()
  22  |   await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Preserve this incomplete draft')
  23  |   await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  24  |   await expect(page.getByRole('region', { name: 'Saved browser draft' })).toContainText('Preserve this incomplete draft')
  25  |   const list = await json(await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`))
  26  |   expect(JSON.stringify(list)).not.toContain('Preserve this incomplete draft')
  27  | })
  28  | 
  29  | for (const choice of ['Keep editing', 'Discard changes', 'Save changes']) {
  30  |   test(`[U07] script navigation: ${choice} has exact persistence semantics`, async ({ page, request }) => {
  31  |     const workspace = await createWorkspace(request)
  32  |     const scriptId = await seedScript(request, workspace)
  33  |     await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
  34  |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  35  |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  36  |     await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:12345')
  37  |     await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
  38  |     const guard = page.getByRole('dialog', { name: 'Unsaved Changes' })
  39  |     await expect(guard).toBeVisible()
  40  |     await guard.getByRole('button', { name: choice, exact: true }).click()
  41  |     if (choice === 'Keep editing') {
  42  |       await expect(workbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:12345')
  43  |       await expect(page).toHaveURL(/mode=edit/)
  44  |     } else await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  45  |     const stored = await json(await request.get(`/api/v1/scripts/${scriptId}`))
  46  |     expect(((stored.script as Record<string, unknown> || stored).config as { base_url: string }).base_url).toBe(choice === 'Save changes' ? 'http://127.0.0.1:12345' : 'http://127.0.0.1:1')
  47  |   })
  48  | }
  49  | 
  50  | for (const code of [500, 409, 401]) {
  51  |   test(`[U07 U18] failed script save (${code}) preserves edits and cannot overwrite stored content`, async ({ page, request }) => {
  52  |     const workspace = await createWorkspace(request)
  53  |     const id = await seedScript(request, workspace)
  54  |     await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  55  |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  56  |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  57  |     const input = workbench.getByRole('textbox', { name: 'Base URL' })
  58  |     await input.fill('http://127.0.0.1:12345')
  59  |     await page.route(`**/api/v1/scripts/${id}`, async route => {
  60  |       if (route.request().method() === 'PUT') await route.fulfill({ status: code, json: { error: 'Workflow test: save rejected; retry after recovery.' } })
  61  |       else await route.continue()
  62  |     })
  63  |     await page.getByRole('button', { name: 'Save script', exact: true }).click()
  64  |     await expect(page.getByText('Workflow test: save rejected; retry after recovery.', { exact: false }).first()).toBeVisible()
  65  |     await expect(input).toHaveValue('http://127.0.0.1:12345')
  66  |     expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
  67  |     await page.unroute(`**/api/v1/scripts/${id}`)
  68  |     const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
> 69  |     await page.getByRole('button', { name: 'Save script', exact: true }).click()
      |                                                                          ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
  70  |     await json(await saved)
  71  |     expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:12345' })
  72  |   })
  73  | }
  74  | 
  75  | test('[U04] project scope and browser history never substitute a similarly named script', async ({ page, request }) => {
  76  |   const a = await createWorkspace(request)
  77  |   const b = await createWorkspace(request)
  78  |   const aId = await seedScript(request, a, 'Shared_name', 'http://127.0.0.1:1111')
  79  |   const bId = await seedScript(request, b, 'Shared_name', 'http://127.0.0.1:2222')
  80  |   for (const [workspace, id, port] of [[a, aId, 1111], [b, bId, 2222]] as const) {
  81  |     await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  82  |     await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  83  |     await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue(`http://127.0.0.1:${port}`)
  84  |     await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${workspace.label} project`) })).toBeVisible()
  85  |   }
  86  |   await page.reload()
  87  |   await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  88  |   await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:2222')
  89  |   await page.goBack()
  90  |   await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  91  |   await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:1111')
  92  | })
  93  | 
  94  | test('[U06] malformed OpenAPI can be replaced and validated without creating a library entry', async ({ page, request }) => {
  95  |   const workspace = await createWorkspace(request)
  96  |   await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  97  |   await page.getByRole('button', { name: 'Import', exact: true }).click()
  98  |   await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  99  |   const controls = page.getByRole('group', { name: 'Script import controls' })
  100 |   const file = controls.locator('input[type=file]')
  101 |   await file.setInputFiles({ name: 'broken.yaml', mimeType: 'text/yaml', buffer: Buffer.from('openapi: [broken') })
  102 |   await controls.getByRole('textbox', { name: 'Script Name' }).fill('Recovery_Import')
  103 |   const failed = mutation(page, 'POST', '/api/v1/scripts/import/openapi')
  104 |   await controls.getByText('Advanced validation only', { exact: true }).click()
  105 |   await controls.getByRole('button', { name: 'Validate Only' }).click()
  106 |   expect((await failed).ok()).toBe(false)
  107 |   await expect(page.locator('.sample-import-error')).toBeVisible()
  108 |   await file.setInputFiles('e2e/fixtures/tier0-orders.openapi.yaml')
  109 |   await controls.getByRole('button', { name: 'Validate Only' }).click()
  110 |   await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  111 |   expect(JSON.stringify(await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)))).not.toContain('Recovery_Import')
  112 | })
  113 | 
```