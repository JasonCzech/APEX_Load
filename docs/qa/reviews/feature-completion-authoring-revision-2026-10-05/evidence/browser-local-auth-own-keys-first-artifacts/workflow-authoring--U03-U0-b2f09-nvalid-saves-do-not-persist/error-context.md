# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-authoring.spec.ts >> [U03 U08] empty setup explains prerequisites and invalid saves do not persist
- Location: e2e/workflow-authoring.spec.ts:10:1

# Error details

```
Error: expect(locator).toBeVisible() failed

Locator: getByText(/No scenarios are available yet/)
Expected: visible
Timeout: 15000ms
Error: element(s) not found

Call log:
  - Expect "toBeVisible" with timeout 15000ms
  - waiting for getByText(/No scenarios are available yet/)

```

```yaml
- complementary:
  - button "APEX Load dashboard":
    - img "APEX Load"
    - text: APEX Load
  - button "Workspace context. Default Domain / Unassigned / Legacy / Imported Legacy": Workspace Default Domain / Unassigned / Legacy / Imported Legacy
  - navigation "Primary navigation":
    - button "New Test":
      - img
      - text: New Test
    - button "Dashboard"
    - button "Command Center"
    - button "Results":
      - img
      - text: Results
    - button "Monitoring":
      - img
      - text: Monitoring
    - button "Activity"
    - button "Scripts":
      - img
      - text: Scripts
    - button "Scenarios":
      - img
      - text: Scenarios
    - button "Repository":
      - img
      - text: Repository
    - button "Access":
      - img
      - text: Access
    - button "Settings":
      - img
      - text: Settings
    - button "Help & documentation":
      - img
      - text: Help & documentation
  - text: Local System Administrator System Admin
  - button "Sign out"
  - text: Live
  - 'button "Build info: local · dev"': Version Info
  - button "Collapse sidebar" [expanded]:
    - img
- main "New Test":
  - status: New Test
  - heading "New Test" [level=1]
  - link "Help for this page":
    - /url: "#/docs?guide=user-guide&article=scenarios"
  - text: Browser · Server
  - combobox "Dashboard color theme":
    - option "System (Auto)"
    - option "Dark Theme" [selected]
    - option "Light Theme"
    - option "Solarized Dark"
    - option "Solarized Light"
    - option "Monokai Dimmed"
  - alert: Project d11d93cb-d59a-41b4-8c9c-f403e622f437 is unavailable. Select an accessible project to continue.
```

# Test source

```ts
  1   | import { test, expect, seedScript, mutation, json, login } from './support/workflow'
  2   | import { createWorkspace, scopeQuery } from './support/workspace'
  3   | 
  4   | // Server-backed tests: response interception is used only to inject a named
  5   | // failure, never to manufacture a successful workflow.
  6   | test.beforeEach(async ({ page }) => {
  7   |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  8   | })
  9   | 
  10  | test('[U03 U08] empty setup explains prerequisites and invalid saves do not persist', async ({ page }) => {
  11  |   const request = page.context().request
  12  |   const workspace = await createWorkspace(request)
  13  |   await page.goto(`/#/new-test?${scopeQuery(workspace)}`)
> 14  |   await expect(page.getByText(/No scenarios are available yet/)).toBeVisible()
      |                                                                  ^ Error: expect(locator).toBeVisible() failed
  15  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  16  |   await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  17  |   await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeDisabled()
  18  |   await expect(page.getByRole('heading', { level: 4, name: 'Scripts And Groups' })).toBeVisible()
  19  |   await page.getByPlaceholder('Scenario name', { exact: true }).fill('Preserve this incomplete draft')
  20  |   await expect(page.getByRole('button', { name: 'Save draft', exact: true })).toBeEnabled()
  21  |   await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  22  |   await expect(page.getByText('No existing scripts are available for this project.')).toBeVisible()
  23  |   await expect(page.getByRole('searchbox', { name: 'Search scripts', exact: false })).toBeFocused()
  24  |   await page.getByRole('dialog', { name: 'Choose Script' }).getByRole('button', { name: 'Cancel' }).click()
  25  |   await page.locator('nav.sidebar-nav').getByRole('button', { name: 'Scripts', exact: true }).click()
  26  |   await page.getByRole('dialog', { name: 'Unsaved Changes' }).getByRole('button', { name: 'Keep editing' }).click()
  27  |   await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Preserve this incomplete draft')
  28  |   await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  29  |   await expect(page.getByRole('region', { name: 'Saved browser draft' })).toContainText('Preserve this incomplete draft')
  30  |   const list = await json(await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`))
  31  |   expect(JSON.stringify(list)).not.toContain('Preserve this incomplete draft')
  32  | })
  33  | 
  34  | for (const choice of ['Keep editing', 'Discard changes', 'Save changes']) {
  35  |   test(`[U07] script navigation: ${choice} has exact persistence semantics`, async ({ page }) => {
  36  |     const request = page.context().request
  37  |     const workspace = await createWorkspace(request)
  38  |     const scriptId = await seedScript(request, workspace)
  39  |     await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
  40  |     const workbench = page.getByRole('region', { name: 'Author center workbench' })
  41  |     await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  42  |     await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:12345')
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
```