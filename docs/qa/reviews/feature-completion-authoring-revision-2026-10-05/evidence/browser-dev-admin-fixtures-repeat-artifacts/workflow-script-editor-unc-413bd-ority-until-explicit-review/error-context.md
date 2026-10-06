# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-editor.spec.ts >> uncertain successful writes retain drafts and revoke retry authority until explicit review
- Location: e2e/workflow-script-editor.spec.ts:220:1

# Error details

```
Error: expect(locator).toContainText(expected) failed

Locator: getByRole('region', { name: 'Script save reconciliation' }).getByLabel('Script revision field diff')
Expected substring: "No document-field differences"
Received string:    "Changed field paths only; values are withheld.ref_key"
Timeout: 15000ms

Call log:
  - Expect "toContainText" with timeout 15000ms
  - waiting for getByRole('region', { name: 'Script save reconciliation' }).getByLabel('Script revision field diff')
    34 × locator resolved to <div aria-label="Script revision field diff">…</div>
       - unexpected value "Changed field paths only; values are withheld.ref_key"

```

```yaml
- paragraph: Changed field paths only; values are withheld.
- list:
  - listitem: ref_key
```

# Test source

```ts
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
  201 |   const name = page.locator('.side-panel-script-name input')
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
> 253 |     await expect(conflict.getByLabel('Script revision field diff')).toContainText('No document-field differences')
      |                                                                     ^ Error: expect(locator).toContainText(expected) failed
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
  302 |     await row.getByRole('button', { name: 'Configure secrets', exact: true }).first().click()
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