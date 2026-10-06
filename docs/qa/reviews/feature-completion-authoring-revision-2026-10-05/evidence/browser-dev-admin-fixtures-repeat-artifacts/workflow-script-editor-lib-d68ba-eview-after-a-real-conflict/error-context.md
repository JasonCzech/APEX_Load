# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> library secret rebind retains exact revision authority and requires review after a real conflict
- Location: e2e/workflow-script-editor.spec.ts:264:1

# Error details

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for locator('tbody tr').filter({ hasText: 'Library rebind fixture' }).getByRole('button', { name: 'Configure secrets', exact: true }).first()

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791185785516-342713 domain / e2e-1791185785516-342713 project / e2e-1791185785516-342713 application / e2e-1791185785516-342713 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791185785516-342713 domain / e2e-1791185785516-342713 project / e2e-1791185785516-342713 application / e2e-1791185785516-342713 1.0
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
  - main "Script Library" [active] [ref=e129]:
    - status [ref=e130]: Script Library
    - generic "Dashboard controls" [ref=e131]:
      - heading "Script Library" [level=1] [ref=e133]
      - group "Script page view" [ref=e135]:
        - button "Script Library" [pressed] [ref=e136] [cursor=pointer]
        - button "Script Builder" [ref=e137] [cursor=pointer]
      - generic [ref=e138]:
        - link "Help for this page" [ref=e139] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=authoring"
        - generic "Deployment identity" [ref=e140]:
          - generic [ref=e141]: Browser · Server
        - combobox "Dashboard color theme" [ref=e144]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - generic [ref=e145]:
      - generic [ref=e146]:
        - generic [ref=e147]:
          - generic [ref=e149]:
            - heading "Script assets" [level=3] [ref=e150]
            - paragraph [ref=e151]: 1 matching script · Project library across applications and releases.
          - region "Library controls" [ref=e152]:
            - generic [ref=e153]:
              - generic [ref=e154]:
                - generic [ref=e155]: Search scripts
                - searchbox "Search scripts" [ref=e156]
              - generic [ref=e157]:
                - generic [ref=e158]: Source
                - combobox "Source" [ref=e159] [cursor=pointer]:
                  - option "All" [selected]
                  - option "Local"
                  - option "Remote Workspace"
              - generic [ref=e160]:
                - button "Create script" [ref=e161] [cursor=pointer]
                - button "Refresh" [ref=e162] [cursor=pointer]
            - generic [ref=e163]:
              - generic [ref=e164]:
                - text: Script archive state
                - combobox "Script archive state" [ref=e165]:
                  - option "Active" [selected]
                  - option "Archived"
                  - option "All"
              - generic [ref=e166]:
                - generic [ref=e167]: Assigned application
                - combobox "Assigned application" [ref=e168]:
                  - option "All assigned applications" [selected]
                  - option "Unassigned"
              - generic [ref=e169]:
                - generic [ref=e170]: Assigned release
                - combobox "Assigned release" [ref=e171]:
                  - option "All assigned releases" [selected]
                  - option "No assigned releases"
              - generic [ref=e172]:
                - generic [ref=e173]: Run release
                - combobox "Run release" [ref=e174] [cursor=pointer]:
                  - option "All releases" [selected]
              - generic [ref=e175]:
                - generic [ref=e176]: Folder
                - combobox "Folder" [ref=e177] [cursor=pointer]:
                  - option "All folders" [selected]
                  - option "Unfiled"
        - generic "Local authoring health filters" [ref=e178]:
          - generic [ref=e179]: Local authoring health
          - button "All 1" [pressed] [ref=e180] [cursor=pointer]:
            - generic [ref=e181]: All
            - strong [ref=e182]: "1"
          - button "Blocked 0" [ref=e183] [cursor=pointer]:
            - generic [ref=e184]: Blocked
            - strong [ref=e185]: "0"
          - button "Needs Replay 0" [ref=e186] [cursor=pointer]:
            - generic [ref=e187]: Needs Replay
            - strong [ref=e188]: "0"
          - button "Missing Data 0" [ref=e189] [cursor=pointer]:
            - generic [ref=e190]: Missing Data
            - strong [ref=e191]: "0"
          - button "Runtime Warning 0" [ref=e192] [cursor=pointer]:
            - generic [ref=e193]: Runtime Warning
            - strong [ref=e194]: "0"
          - button "AI Guardrail 0" [ref=e195] [cursor=pointer]:
            - generic [ref=e196]: AI Guardrail
            - strong [ref=e197]: "0"
        - generic [ref=e198]:
          - navigation "Script library pages" [ref=e199]:
            - status [ref=e200]: Showing 1–1 of 1
            - generic [ref=e201]:
              - text: Rows
              - combobox "Script library rows per page" [ref=e202]:
                - option "10" [selected]
                - option "25"
                - option "All"
            - button "Previous" [disabled] [ref=e203]
            - generic [ref=e204]: Page 1 of 1
            - button "Next" [disabled] [ref=e205]
          - table "Show usage & dates" [ref=e206]:
            - rowgroup [ref=e207]:
              - row "Select all scripts on this page Source Script sorted ascending Folder not sorted Authoring health not sorted Usage not sorted Assigned application/releases Run releases not sorted Create Date not sorted Last Run not sorted Actions" [ref=e208]:
                - columnheader "Select all scripts on this page" [ref=e209]:
                  - checkbox "Select all scripts on this page" [ref=e210] [cursor=pointer]
                - columnheader "Source" [ref=e211]
                - columnheader "Script sorted ascending" [ref=e212] [cursor=pointer]:
                  - button "Script sorted ascending" [ref=e213]:
                    - text: Script
                    - generic "sorted ascending" [ref=e214]: ↑
                - columnheader "Folder not sorted" [ref=e215] [cursor=pointer]:
                  - button "Folder not sorted" [ref=e216]:
                    - text: Folder
                    - generic "not sorted" [ref=e217]: ↕
                - columnheader "Authoring health not sorted" [ref=e218] [cursor=pointer]:
                  - button "Authoring health not sorted" [ref=e219]:
                    - text: Authoring health
                    - generic "not sorted" [ref=e220]: ↕
                - columnheader "Usage not sorted" [ref=e221] [cursor=pointer]:
                  - button "Usage not sorted" [ref=e222]:
                    - text: Usage
                    - generic "not sorted" [ref=e223]: ↕
                - columnheader "Assigned application/releases" [ref=e224]
                - columnheader "Run releases not sorted" [ref=e225] [cursor=pointer]:
                  - button "Run releases not sorted" [ref=e226]:
                    - text: Run releases
                    - generic "not sorted" [ref=e227]: ↕
                - columnheader "Create Date not sorted" [ref=e228] [cursor=pointer]:
                  - button "Create Date not sorted" [ref=e229]:
                    - text: Create Date
                    - generic "not sorted" [ref=e230]: ↕
                - columnheader "Last Run not sorted" [ref=e231] [cursor=pointer]:
                  - button "Last Run not sorted" [ref=e232]:
                    - text: Last Run
                    - generic "not sorted" [ref=e233]: ↕
                - columnheader "Actions" [ref=e234]
            - rowgroup [ref=e235]:
              - row "Select Library rebind fixture Local Library rebind fixture Created by Development Mode · Last modified by Unknown http v1 LOC-SCR-0006-v1 Copy LOC-SCR-0006-v1 Unfiled Scenario usage Scenario usage Assigned application/releases Run releases Oct 5, 2026 Never Edit Archive" [ref=e236]:
                - cell "Select Library rebind fixture" [ref=e237]:
                  - checkbox "Select Library rebind fixture" [ref=e238] [cursor=pointer]
                - cell "Local" [ref=e239]:
                  - generic [ref=e240]: Local
                - cell "Library rebind fixture Created by Development Mode · Last modified by Unknown http v1 LOC-SCR-0006-v1 Copy LOC-SCR-0006-v1" [ref=e241]:
                  - button "Library rebind fixture" [ref=e242] [cursor=pointer]
                  - generic [ref=e243]: Created by Development Mode · Last modified by Unknown
                  - generic [ref=e244]:
                    - generic [ref=e245]: http
                    - generic [ref=e246]: v1
                    - generic "LOC-SCR-0006-v1" [ref=e247]:
                      - generic [ref=e248]: LOC-SCR-0006-v1
                      - button "Copy LOC-SCR-0006-v1" [ref=e249] [cursor=pointer]: Copy
                - cell "Unfiled" [ref=e250]
                - cell "Scenario usage" [ref=e251]:
                  - text: Static checks passed · 100
                  - generic [ref=e252]:
                    - generic [ref=e253]: static checks passed
                    - button "View 1 missing secret detail for Library rebind fixture" [ref=e254] [cursor=pointer]: 1 secret missing
                - cell "Scenario usage" [ref=e255]:
                  - generic [ref=e256]:
                    - strong [ref=e257]: 0 scenarios
                    - generic [ref=e258]: No scenario-run releases yet
                - cell "Assigned application/releases" [ref=e259]: Unassigned
                - cell "Run releases" [ref=e260]:
                  - generic [ref=e262]: No releases yet
                - cell "Oct 5, 2026" [ref=e263]
                - cell "Never" [ref=e264]
                - cell "Edit Archive" [ref=e265]:
                  - button "Edit" [ref=e266] [cursor=pointer]
                  - button "Archive" [ref=e268] [cursor=pointer]
                  - group [ref=e269]:
                    - generic "More" [ref=e270] [cursor=pointer]
      - group [ref=e271]:
        - 'generic "Advanced: object repositories" [ref=e272]'
      - group [ref=e273]:
        - 'generic "Advanced: correlation rulesets" [ref=e274]'
```

# Test source

```ts
  202 |   await expect(name).toHaveValue('Placement fixture')
  203 |   await name.fill('My placement draft')
  204 |   await json(await request.patch(`/api/v1/scripts/${id}/move`, { data: { folder_id: folder } }))
  205 |   const save = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  206 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  207 |   expect((await save).status()).toBe(412)
  208 |   await expect(name).toHaveValue('My placement draft')
  209 |   const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  210 |   await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  211 |   await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  212 |   const retried = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  213 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  214 |   await json(await retried)
  215 |   const final = await json(await request.get(`/api/v1/scripts/${id}`))
  216 |   expect(final.folder_id).toBe(folder)
  217 |   expect(final.script).toMatchObject({ name: 'My placement draft', folder_id: folder })
  218 | })
  219 | 
  220 | test('uncertain successful writes retain drafts and revoke retry authority until explicit review', async ({ page }) => {
  221 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  222 |   await page.setViewportSize({ width: 1600, height: 1000 })
  223 |   const request = page.context().request
  224 |   const workspace = await createWorkspace(request)
  225 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Uncertain fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] } } })))
  226 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  227 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  228 |   const name = page.locator('.side-panel-script-name input')
  229 |   await expect(name).toHaveValue('Uncertain fixture')
  230 |   let writes = 0
  231 |   await page.route(`**/api/v1/scripts/${id}`, async route => {
  232 |     if (route.request().method() !== 'PUT') { await route.continue(); return }
  233 |     writes++
  234 |     const response = await route.fetch()
  235 |     expect(response.ok()).toBeTruthy()
  236 |     const headers = { ...response.headers() }
  237 |     delete headers['x-apex-script-save-mode']
  238 |     // Genuine server commit; strip its receipt only on the browser return path.
  239 |     await route.fulfill({ response, headers })
  240 |   })
  241 |   try {
  242 |     await name.fill('Potentially committed draft')
  243 |     await page.getByRole('button', { name: 'Save script', exact: true }).click()
  244 |     const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  245 |     await expect(conflict).toContainText('outcome or revision protection could not be verified')
  246 |     await expect(name).toHaveValue('Potentially committed draft')
  247 |     await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  248 |     expect(writes).toBe(1)
  249 |     const final = await json(await request.get(`/api/v1/scripts/${id}`))
  250 |     expect(final.script).toMatchObject({ name: 'Potentially committed draft' })
  251 |     await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  252 |     await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  253 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('No document-field differences')
  254 |     expect(writes).toBe(1)
  255 |     await conflict.getByRole('button', { name: 'Reload script and discard draft', exact: true }).click()
  256 |     await expect(conflict).toHaveCount(0)
  257 |     await expect(name).toHaveValue('Potentially committed draft')
  258 |     await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  259 |     expect(writes).toBe(1)
  260 |   } finally { await page.unrouteAll({ behavior: 'wait' }) }
  261 | })
  262 | 
  263 | 
  264 | test('library secret rebind retains exact revision authority and requires review after a real conflict', async ({ page }, testInfo) => {
  265 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  266 |   await page.setViewportSize({ width: 1600, height: 1000 })
  267 |   const request = page.context().request
  268 |   const workspace = await createWorkspace(request)
  269 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId,
  270 |     script: { name: 'Library rebind fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
  271 |       actions: [{ name: 'Authorization', type: 'request', method: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.OLD_REFERENCE}}' } }] },
  272 |   } })))
  273 |   const observed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  274 |   const initialTag = observed.headers().etag
  275 |   expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[0-9a-f]{64}"$/)
  276 |   // Only the read-only vault-name catalogue is controlled; transformation and
  277 |   // both conflicting/current writes use the actual protected controller APIs.
  278 |   await page.route(`**/api/v1/projects/${workspace.projectId}/secrets`, async route => {
  279 |     expect(route.request().method()).toBe('GET')
  280 |     await route.fulfill({ json: { project_id: workspace.projectId, capabilities: { provider: 'database', writable: false },
  281 |       secrets: [{ name: 'EXISTING_REFERENCE', provider: 'database', writable: false }], count: 1 } })
  282 |   })
  283 |   let transformations = 0
  284 |   let browserWrites = 0
  285 |   page.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) browserWrites++ })
  286 |   await page.route('**/api/v1/scripts/secret-references/rebind', async route => {
  287 |     transformations++
  288 |     expect(route.request().headers()['if-match']).toBe(initialTag)
  289 |     const transformed = await route.fetch()
  290 |     expect(transformed.ok()).toBeTruthy()
  291 |     const external = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  292 |     const body = await external.json()
  293 |     await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': external.headers().etag },
  294 |       data: { script: { ...body.script, name: 'Externally updated library script' } } }))
  295 |     await route.fulfill({ response: transformed })
  296 |   })
  297 |   try {
  298 |     await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
  299 |     if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  300 |     const row = page.locator('tbody tr').filter({ hasText: 'Library rebind fixture' })
  301 |     await expect(row).toHaveCount(1)
> 302 |     await row.getByRole('button', { name: 'Configure secrets', exact: true }).first().click()
      |                                                                                       ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
  303 |     const dialog = page.locator('.script-secret-bindings-dialog')
  304 |     await expect(dialog).toHaveAccessibleName('Secret Bindings · Library rebind fixture')
  305 |     const reuse = dialog.getByLabel('Use existing project secret', { exact: true })
  306 |     await reuse.selectOption('EXISTING_REFERENCE')
  307 |     const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  308 |     await dialog.getByRole('button', { name: 'Rebind and share', exact: true }).click()
  309 |     const failed = await rejected
  310 |     expect(failed.status()).toBe(412)
  311 |     expect(failed.request().headers()['if-match']).toBe(initialTag)
  312 |     const conflict = dialog.getByRole('region', { name: 'Script save reconciliation' })
  313 |     await expect(conflict).toContainText('rebound draft is retained')
  314 |     await expect(dialog).toContainText('{{secret.EXISTING_REFERENCE}}')
  315 |     await expect(dialog.getByRole('button', { name: 'Rebind and share', exact: true })).toBeDisabled()
  316 |     expect(browserWrites).toBe(1)
  317 |     expect(transformations).toBe(1)
  318 |     await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  319 |     await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  320 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('actions')
  321 |     await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('EXISTING_REFERENCE')
  322 |     await dialog.screenshot({ path: testInfo.outputPath('library-secret-revision-conflict.png') })
  323 |     await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  324 |     expect(browserWrites).toBe(1)
  325 |     const finalSave = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  326 |     await dialog.getByRole('button', { name: 'Save reviewed rebound draft', exact: true }).click()
  327 |     const saved = await finalSave
  328 |     expect(saved.request().headers()['if-match']).not.toBe(initialTag)
  329 |     expect(saved.headers()['x-apex-script-save-mode']).toBe('conditioned')
  330 |     await json(saved)
  331 |     await expect(dialog).toHaveCount(0)
  332 |     const final = await json(await request.get(`/api/v1/scripts/${id}`))
  333 |     expect(final.script).toMatchObject({ name: 'Externally updated library script', actions: [{ headers: { Authorization: 'Bearer {{secret.EXISTING_REFERENCE}}' } }] })
  334 |     expect(browserWrites).toBe(2)
  335 |     expect(transformations).toBe(1)
  336 |   } finally { await page.unrouteAll({ behavior: 'wait' }) }
  337 | })
  338 | 
```