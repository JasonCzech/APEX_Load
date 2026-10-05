# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-authoring.spec.ts >> [U04] project scope and browser history never substitute a similarly named script
- Location: e2e/workflow-authoring.spec.ts:135:1

# Error details

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('tab', { name: 'Runtime Settings' })

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. Default Domain / Unassigned / Legacy / Imported Legacy" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: Default Domain / Unassigned / Legacy / Imported Legacy
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
        - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e109]:
          - generic [ref=e110]: LS
          - generic [ref=e111]:
            - generic [ref=e112]: Local System Administrator
            - generic [ref=e113]: System Admin
            - button "Sign out" [ref=e114] [cursor=pointer]
        - generic [ref=e115]:
          - generic "Live" [ref=e116]:
            - generic [ref=e118]: Live
          - 'button "Build info: local · dev" [ref=e120] [cursor=pointer]':
            - generic [ref=e122]: Version Info
      - button "Collapse sidebar" [expanded] [ref=e124] [cursor=pointer]:
        - img [ref=e125]
  - main "Edit Script" [active] [ref=e127]:
    - status [ref=e128]: Edit Script
    - generic "Dashboard controls" [ref=e129]:
      - heading "Edit Script" [level=1] [ref=e131]
      - generic [ref=e132]:
        - link "Help for this page" [ref=e133] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=authoring"
        - generic "Deployment identity" [ref=e134]:
          - generic [ref=e135]: Browser · Server
        - combobox "Dashboard color theme" [ref=e138]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - alert [ref=e139]: Project 3fc68c9e-98fb-4838-9f71-84ddbbfe380c is unavailable. Select an accessible project to continue.
```

# Test source

```ts
  43  |     await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
  44  |     const guard = page.getByRole('dialog', { name: 'Unsaved Changes' })
  45  |     await expect(guard).toBeVisible()
  46  |     await guard.getByRole('button', { name: choice, exact: true }).click()
  47  |     if (choice === 'Keep editing') {
  48  |       await expect(workbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:12345')
  49  |       await expect(page).toHaveURL(/mode=edit/)
  50  |     } else await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  51  |     const stored = await json(await request.get(`/api/v1/scripts/${scriptId}`))
  52  |     expect(((stored.script as Record<string, unknown> || stored).config as { base_url: string }).base_url).toBe(choice === 'Save changes' ? 'http://127.0.0.1:12345' : 'http://127.0.0.1:1')
  53  |   })
  54  | }
  55  | 
  56  | for (const code of [500, 409, 401]) {
  57  |   test(`[U07 U18] failed script save (${code}) preserves edits and cannot overwrite stored content`, async ({ page }) => {
  58  |     const request = page.context().request
  59  |     const workspace = await createWorkspace(request)
  60  |     const id = await seedScript(request, workspace)
  61  |     const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  62  |     const initialDocument = await json(initial)
  63  |     const initialTag = initial.headers().etag
  64  |     expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
  65  |     const writes: string[] = []
  66  |     page.on('request', outgoing => {
  67  |       if (outgoing.method() === 'PUT' && new URL(outgoing.url()).pathname === `/api/v1/scripts/${id}`) writes.push(outgoing.headers()['if-match'])
  68  |     })
  69  |     await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  70  |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  71  |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  72  |     const input = workbench.getByRole('textbox', { name: 'Base URL' })
  73  |     await input.fill('http://127.0.0.1:12345')
  74  |     await page.route(`**/api/v1/scripts/${id}`, async route => {
  75  |       if (route.request().method() === 'PUT') await route.fulfill({ status: code, json: { error: 'Workflow test: save rejected; retry after recovery.' } })
  76  |       else await route.continue()
  77  |     })
  78  |     try {
  79  |       const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  80  |       const saveButton = page.getByRole('button', { name: 'Save script', exact: true })
  81  |       await saveButton.click()
  82  |       const rejectedResponse = await rejected
  83  |       expect(rejectedResponse.status()).toBe(code)
  84  |       expect(rejectedResponse.request().headers()['if-match']).toBe(initialTag)
  85  |       await expect(page.getByText('Workflow test: save rejected; retry after recovery.', { exact: false }).first()).toBeVisible()
  86  |       await expect(input).toHaveValue('http://127.0.0.1:12345')
  87  |       expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
  88  |       expect(writes).toEqual([initialTag])
  89  |       await page.unroute(`**/api/v1/scripts/${id}`)
  90  |       let reviewedTag = initialTag
  91  |       let expectedName = (initialDocument.script as Record<string, unknown>).name
  92  |       if (code === 500) {
  93  |         // A nondefinitive outcome revokes retry authority, even though this
  94  |         // injected response did not reach storage. A real other writer proves
  95  |         // that explicit review is needed before another conditional save.
  96  |         await expect(saveButton).toBeDisabled()
  97  |         const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  98  |         await expect(conflict).toBeVisible()
  99  |         expectedName = 'Changed by another author during recovery'
  100 |         await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': initialTag }, data: {
  101 |           project_id: workspace.projectId, script: { ...(initialDocument.script as Record<string, unknown>), name: expectedName },
  102 |         } }))
  103 |         const reviewed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  104 |         const reviewedDocument = await json(reviewed)
  105 |         reviewedTag = reviewed.headers().etag
  106 |         expect(reviewedTag).not.toBe(initialTag)
  107 |         expect(reviewedDocument.script).toMatchObject({ name: expectedName, config: { base_url: 'http://127.0.0.1:1' } })
  108 |         await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  109 |         await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
  110 |         await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  111 |         await expect(conflict.getByLabel('Script revision field diff')).toContainText('config.base_url')
  112 |         await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('http://127.0.0.1:12345')
  113 |         await expect(input).toHaveValue('http://127.0.0.1:12345')
  114 |         await expect(saveButton).toBeDisabled()
  115 |         expect(writes).toEqual([initialTag])
  116 |         await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  117 |         await expect(conflict).toHaveCount(0)
  118 |         await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  119 |         await expect(input).toHaveValue('http://127.0.0.1:12345')
  120 |         await expect(saveButton).toBeEnabled()
  121 |         expect(writes).toEqual([initialTag])
  122 |         expect(((await json(await request.get(`/api/v1/scripts/${id}`))).script as Record<string, unknown>).config).toMatchObject({ base_url: 'http://127.0.0.1:1' })
  123 |       }
  124 |       const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  125 |       await saveButton.click()
  126 |       const savedResponse = await saved
  127 |       await json(savedResponse)
  128 |       expect(savedResponse.request().headers()['if-match']).toBe(reviewedTag)
  129 |       expect(writes).toEqual([initialTag, reviewedTag])
  130 |       expect((await json(await request.get(`/api/v1/scripts/${id}`))).script).toMatchObject({ name: expectedName, config: { base_url: 'http://127.0.0.1:12345' } })
  131 |     } finally { await page.unrouteAll({ behavior: 'wait' }) }
  132 |   })
  133 | }
  134 | 
  135 | test('[U04] project scope and browser history never substitute a similarly named script', async ({ page }) => {
  136 |   const request = page.context().request
  137 |   const a = await createWorkspace(request)
  138 |   const b = await createWorkspace(request)
  139 |   const aId = await seedScript(request, a, 'Shared_name', 'http://127.0.0.1:1111')
  140 |   const bId = await seedScript(request, b, 'Shared_name', 'http://127.0.0.1:2222')
  141 |   for (const [workspace, id, port] of [[a, aId, 1111], [b, bId, 2222]] as const) {
  142 |     await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
> 143 |     await page.getByRole('tab', { name: 'Runtime Settings' }).click()
      |                                                               ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
  144 |     await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue(`http://127.0.0.1:${port}`)
  145 |     await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${workspace.label} project`) })).toBeVisible()
  146 |   }
  147 |   await page.reload()
  148 |   await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  149 |   await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:2222')
  150 |   await page.goBack()
  151 |   await page.getByRole('tab', { name: 'Runtime Settings' }).click()
  152 |   await expect(page.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:1111')
  153 | })
  154 | 
  155 | test('[U06] malformed OpenAPI can be replaced and validated without creating a library entry', async ({ page }) => {
  156 |   const request = page.context().request
  157 |   const workspace = await createWorkspace(request)
  158 |   await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  159 |   await page.getByRole('button', { name: 'Import', exact: true }).click()
  160 |   await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  161 |   const controls = page.getByRole('group', { name: 'Script import controls' })
  162 |   const file = controls.locator('input[type=file]')
  163 |   await file.setInputFiles({ name: 'broken.yaml', mimeType: 'text/yaml', buffer: Buffer.from('openapi: [broken') })
  164 |   await controls.getByRole('textbox', { name: 'Script Name' }).fill('Recovery_Import')
  165 |   const failed = mutation(page, 'POST', '/api/v1/scripts/import/openapi')
  166 |   await controls.getByText('Advanced validation only', { exact: true }).click()
  167 |   await controls.getByRole('button', { name: 'Validate Only' }).click()
  168 |   expect((await failed).ok()).toBe(false)
  169 |   await expect(page.locator('.sample-import-error')).toBeVisible()
  170 |   await file.setInputFiles('e2e/fixtures/tier0-orders.openapi.yaml')
  171 |   await controls.getByRole('button', { name: 'Validate Only' }).click()
  172 |   await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  173 |   expect(JSON.stringify(await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)))).not.toContain('Recovery_Import')
  174 | })
  175 | 
```