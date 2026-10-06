# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> library secret rebind retains exact revision authority and requires review after a real conflict
- Location: e2e/workflow-script-editor.spec.ts:267:1

# Error details

```
Error: expect(locator).toContainText(expected) failed

Locator: locator('.script-secret-bindings-dialog').getByRole('region', { name: 'Script save reconciliation' })
Expected substring: "rebound draft is retained"
Timeout: 15000ms
Error: element(s) not found

Call log:
  - Expect "toContainText" with timeout 15000ms
  - waiting for locator('.script-secret-bindings-dialog').getByRole('region', { name: 'Script save reconciliation' })

```

```yaml
- complementary:
  - button "APEX Load dashboard":
    - img "APEX Load"
    - text: APEX Load
  - button "Workspace context. e2e-1791185880372-666281 domain / e2e-1791185880372-666281 project / e2e-1791185880372-666281 application / e2e-1791185880372-666281 1.0": Workspace e2e-1791185880372-666281 domain / e2e-1791185880372-666281 project / e2e-1791185880372-666281 application / e2e-1791185880372-666281 1.0
  - navigation "Primary navigation":
    - button "New Test":
      - img
      - text: New Test
    - paragraph: Operate
    - button "Dashboard"
    - button "Command Center"
    - button "Results":
      - img
      - text: Results
    - button "Monitoring":
      - img
      - text: Monitoring
    - button "Activity"
    - paragraph: Build
    - button "Scripts":
      - img
      - text: Scripts
    - button "Scenarios":
      - img
      - text: Scenarios
    - paragraph: Administer
    - button "Repository":
      - img
      - text: Repository
    - button "Access":
      - img
      - text: Access
    - button "Settings":
      - img
      - text: Settings
    - paragraph: Support
    - button "Help & documentation":
      - img
      - text: Help & documentation
  - 'button "Preview permissions. Current mode: admin"': Preview permissions
  - text: Live
  - 'button "Build info: local · dev"': Version Info
  - button "Collapse sidebar" [expanded]:
    - img
- main "Script Library":
  - status: Script Library
  - heading "Script Library" [level=1]
  - link "Help for this page":
    - /url: "#/docs?guide=user-guide&article=authoring"
  - text: Browser · Server
  - combobox "Dashboard color theme":
    - option "System (Auto)"
    - option "Dark Theme" [selected]
    - option "Light Theme"
    - option "Solarized Dark"
    - option "Solarized Light"
    - option "Monokai Dimmed"
  - alert:
    - strong: This dashboard page could not be loaded.
    - text: The application may have been updated while this page was open.
    - button "Reload and retry"
```

# Test source

```ts
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
  253 |     // The server increments its generated RefKey on the committed write. The
  254 |     // authored draft is otherwise identical and values remain undisclosed.
  255 |     await expect(conflict.getByLabel('Script revision field diff').getByRole('listitem')).toHaveText(['ref_key'])
  256 |     await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('Potentially committed draft')
  257 |     expect(writes).toBe(1)
  258 |     await conflict.getByRole('button', { name: 'Reload script and discard draft', exact: true }).click()
  259 |     await expect(conflict).toHaveCount(0)
  260 |     await expect(name).toHaveValue('Potentially committed draft')
  261 |     await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  262 |     expect(writes).toBe(1)
  263 |   } finally { await page.unrouteAll({ behavior: 'wait' }) }
  264 | })
  265 | 
  266 | 
  267 | test('library secret rebind retains exact revision authority and requires review after a real conflict', async ({ page }, testInfo) => {
  268 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  269 |   await page.setViewportSize({ width: 1600, height: 1000 })
  270 |   const request = page.context().request
  271 |   const workspace = await createWorkspace(request)
  272 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId,
  273 |     script: { name: 'Library rebind fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
  274 |       actions: [{ name: 'Authorization', type: 'request', method: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.OLD_REFERENCE}}' } }] },
  275 |   } })))
  276 |   const observed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  277 |   const initialTag = observed.headers().etag
  278 |   expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[0-9a-f]{64}"$/)
  279 |   // Only the read-only vault-name catalogue is controlled; transformation and
  280 |   // both conflicting/current writes use the actual protected controller APIs.
  281 |   await page.route(`**/api/v1/projects/${workspace.projectId}/secrets`, async route => {
  282 |     expect(route.request().method()).toBe('GET')
  283 |     await route.fulfill({ json: { project_id: workspace.projectId, capabilities: { provider: 'database', writable: false },
  284 |       secrets: [{ name: 'EXISTING_REFERENCE', provider: 'database', writable: false }], count: 1 } })
  285 |   })
  286 |   let transformations = 0
  287 |   let browserWrites = 0
  288 |   page.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) browserWrites++ })
  289 |   await page.route('**/api/v1/scripts/secret-references/rebind', async route => {
  290 |     transformations++
  291 |     expect(route.request().headers()['if-match']).toBe(initialTag)
  292 |     const transformed = await route.fetch()
  293 |     expect(transformed.ok()).toBeTruthy()
  294 |     const external = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  295 |     const body = await external.json()
  296 |     await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': external.headers().etag },
  297 |       data: { script: { ...body.script, name: 'Externally updated library script' } } }))
  298 |     await route.fulfill({ response: transformed })
  299 |   })
  300 |   try {
  301 |     await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
  302 |     if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  303 |     const row = page.locator('tbody tr').filter({ hasText: 'Library rebind fixture' })
  304 |     await expect(row).toHaveCount(1)
  305 |     await row.getByRole('button', { name: 'View 1 missing secret detail for Library rebind fixture', exact: true }).click()
  306 |     const dialog = page.locator('.script-secret-bindings-dialog')
  307 |     await expect(dialog).toHaveAccessibleName('Secret Bindings · Library rebind fixture')
  308 |     const reuse = dialog.getByLabel('Use existing project secret', { exact: true })
  309 |     await reuse.selectOption('EXISTING_REFERENCE')
  310 |     const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  311 |     await dialog.getByRole('button', { name: 'Rebind and share', exact: true }).click()
  312 |     const failed = await rejected
  313 |     expect(failed.status()).toBe(412)
  314 |     expect(failed.request().headers()['if-match']).toBe(initialTag)
  315 |     const conflict = dialog.getByRole('region', { name: 'Script save reconciliation' })
> 316 |     await expect(conflict).toContainText('rebound draft is retained')
      |                            ^ Error: expect(locator).toContainText(expected) failed
  317 |     await expect(dialog).toContainText('{{secret.EXISTING_REFERENCE}}')
  318 |     await expect(dialog.getByRole('button', { name: 'Rebind and share', exact: true })).toBeDisabled()
  319 |     expect(browserWrites).toBe(1)
  320 |     expect(transformations).toBe(1)
  321 |     await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  322 |     await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  323 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('actions')
  324 |     await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('EXISTING_REFERENCE')
  325 |     await dialog.screenshot({ path: testInfo.outputPath('library-secret-revision-conflict.png') })
  326 |     await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  327 |     expect(browserWrites).toBe(1)
  328 |     const finalSave = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  329 |     await dialog.getByRole('button', { name: 'Save reviewed rebound draft', exact: true }).click()
  330 |     const saved = await finalSave
  331 |     expect(saved.request().headers()['if-match']).not.toBe(initialTag)
  332 |     expect(saved.headers()['x-apex-script-save-mode']).toBe('conditioned')
  333 |     await json(saved)
  334 |     await expect(dialog).toHaveCount(0)
  335 |     const final = await json(await request.get(`/api/v1/scripts/${id}`))
  336 |     expect(final.script).toMatchObject({ name: 'Externally updated library script', actions: [{ headers: { Authorization: 'Bearer {{secret.EXISTING_REFERENCE}}' } }] })
  337 |     expect(browserWrites).toBe(2)
  338 |     expect(transformations).toBe(1)
  339 |   } finally { await page.unrouteAll({ behavior: 'wait' }) }
  340 | })
  341 | 
```