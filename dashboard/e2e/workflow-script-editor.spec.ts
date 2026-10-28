import { test, expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { writeFile } from 'node:fs/promises'
import type { ScriptDebugRuntimeState } from '../src/types/replay'
import type { ScriptDefinition } from '../src/types/scripts'
import { idOf, json, login, mutation, expectEditorSaved, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('operator edits the failed nested browser assertion and reruns without losing source fields', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const leaf = { name: 'Check greeting', type: 'browser_wait', target: '#result', wait_for: { type: 'selector', value: '#result', timeout_ms: 10000 }, metadata: { audit_marker: 'preserve' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Welcome' }] }
  const script = { name: 'Nested greeting', version: '1.0.0', protocol: 'browser', config: {}, actions: [{ name: 'Repeat journey', type: 'control_loop', control: { count: 1, steps: [leaf] } }] }
  const created = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })
  expect(created.ok(), await created.text()).toBeTruthy()
  const payload = await created.json(); const id = payload.script?.id || payload.id
  let runs = 0
  let replayExpected = 'Welcome'
  await page.route(`**/api/v1/scripts/${id}/debug/sessions`, async route => {
    const body = route.request().postDataJSON(); runs++
    const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
    replayExpected = saved.script.actions[0].control.steps[0].assertions[0].expected
    const now = new Date().toISOString()
    const passed = replayExpected === 'Hello'
    // Go omits the zero action index in JSON. Repair must still find the first root.
    const event = { phase: 'action', step_path: ['loop[0]'], iteration: 0, action_name: 'Check greeting', action_type: 'browser_wait', target: '#result', success: passed, duration_ms: 1, started_at: now, completed_at: now, error_message: passed ? '' : 'Greeting mismatch', protocol_evidence: passed ? {} : { assertion_failure: { type: 'element_text_contains', expected: 'Welcome', actual: 'Hello' } } }
    const report = { replay_session_id: body.replay_session_id, group: 'Nested greeting', protocol: 'browser', started_at: now, completed_at: now, success: passed, events: [event] }
    const sessionIncarnation = randomUUID()
    const runtimeState = { version: 1, kind: 'full', session_id: body.replay_session_id,
      session_incarnation: sessionIncarnation, state_revision: '1', state: 'unavailable',
      reason: 'session_terminal', snapshot: null } satisfies ScriptDebugRuntimeState
    await route.fulfill({ json: { debug_session_id: body.replay_session_id, replay_session_id: body.replay_session_id,
      session: { id: body.replay_session_id, session_incarnation: sessionIncarnation, state_revision: '1',
        runtime_state: runtimeState, status: passed ? 'completed' : 'failed', completed_at: now, report } } })
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.locator('.topbar-debug-action-run').click()
  await expectDebugOutcome(page, 'failed')
  await expect(page.getByRole('button', { name: 'Edit this assertion' })).toBeVisible()
  await expect(page.locator('.script-browser-evidence')).toHaveCount(1)
  await expect(page.getByRole('region', { name: 'Debug detail', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Edit this assertion' }).click()
  await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  const expected = page.getByLabel('Check 1 text', { exact: true })
  await expect(expected).toHaveValue('Welcome')
  await expect(expected).toBeFocused()
  await expected.fill('Hello')
  await expect(page.getByLabel('Script evidence states')).toContainText('stale')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expectEditorSaved(page)
  const saved = await (await request.get(`/api/v1/scripts/${id}`)).json()
  expect(saved.script.actions[0].control.steps[0]).toMatchObject({ metadata: { audit_marker: 'preserve' }, wait_for: leaf.wait_for, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Hello' }] })
  await page.locator('.topbar-debug-action-run').click()
  await expectDebugOutcome(page, 'passed')
  expect(runs).toBe(2)
  expect(replayExpected).toBe('Hello')
  await page.screenshot({ path: testInfo.outputPath('script-debug-evidence.png'), fullPage: true })
})

test('authored script goals survive an unrelated editor save and canonical reopen', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const goals = {
    target_average_tps: 8.75, p95_latency_ms: 217.5, max_error_pct: 0,
    min_vusers: 1, max_vusers: 6, max_error_count: 17,
    stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '11s',
    transaction_rules: [{ population: 'transaction', protocol: 'http', group: 'checkout', transaction: 'Purchase', metric: 'p95_duration_ms', comparator: 'lte', threshold: 215.25, severity: 'fail' }],
  }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Durable script goals', version: '1.0', protocol: 'http', goal_config: goals,
      config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Purchase', type: 'request', method: 'GET', target: '/' }] },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  const baseURL = workbench.getByRole('textbox', { name: 'Base URL' })
  await expect(baseURL).toHaveValue('http://127.0.0.1:1')
  await baseURL.fill('http://127.0.0.1:2')
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await saved)
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  const canonical = await json(await request.get(`/api/v1/scripts/${id}`))
  expect(canonical.script).toMatchObject({ goal_config: goals, config: { base_url: 'http://127.0.0.1:2' } })
  let releaseMetadata!: () => void
  let metadataFetched = false
  let metadataFetchError: unknown
  const metadataRelease = new Promise<void>(resolve => { releaseMetadata = resolve })
  await page.route(`**/api/v1/scripts/${id}/secret-status`, async route => {
    try {
      const response = await route.fetch()
      metadataFetched = true
      await metadataRelease
      await route.fulfill({ response })
    } catch (error) {
      metadataFetchError = error
      await route.abort().catch(() => undefined)
    }
  })
  try {
    await page.reload()
    await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
    await expect(baseURL).toHaveValue('http://127.0.0.1:2')
    await workbench.getByRole('tab', { name: 'DSL' }).click()
    await expect.poll(() => {
      if (metadataFetchError) throw metadataFetchError
      return metadataFetched
    }, { message: 'canonical metadata read completes before the controlled refresh' }).toBe(true)
    const code = page.locator('.script-code-pane')
    // With no action selected, Source already owns the complete canonical JSON.
    // Its virtualized code view must navigate to the authored goals to inspect
    // them; no conditional action-scope toggle is present on this entry path.
    await code.getByRole('button', { name: 'Find', exact: true }).click()
    const find = code.getByPlaceholder('Find', { exact: true })
    await find.pressSequentially('goal_config')
    const refreshedMetadata = mutation(page, 'GET', `/api/v1/scripts/${id}/secret-status`)
    releaseMetadata()
    await json(await refreshedMetadata)
    await expect(find).toHaveValue('goal_config')
    await find.press('Enter')
    await expect(code.locator('.cm-content')).toContainText('goal_config')
    await find.fill('')
    await find.pressSequentially('217.5')
    await find.press('Enter')
    await expect(code.locator('.cm-content')).toContainText('217.5')
    await find.press('Escape')
    await page.screenshot({ path: testInfo.outputPath('canonical-goals-reopen.png'), fullPage: true })
  } finally { releaseMetadata(); await page.unrouteAll({ behavior: 'wait' }) }
})


test('two editors preserve stale drafts and require explicit revision review before reapply', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Revision fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] },
  } })))
  const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  expect(initial.ok()).toBeTruthy()
  const initialTag = initial.headers().etag
  expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
  const other = await page.context().newPage()
  await other.setViewportSize({ width: 1600, height: 1000 })
  try {
    const route = `/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`
    await page.goto(route)
    await other.goto(route)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') { await page.reload(); await other.reload() }
    const firstName = page.getByRole('textbox', { name: 'Script name', exact: true })
    const staleName = other.getByRole('textbox', { name: 'Script name', exact: true })
    await expect(firstName).toHaveValue('Revision fixture')
    await expect(staleName).toHaveValue('Revision fixture')
    await firstName.fill('First editor saved')
    const workbench = page.getByRole('region', { name: 'Author center workbench' })
    await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
    await workbench.getByRole('textbox', { name: 'Base URL' }).fill('http://127.0.0.1:2')
    const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    const response = await saved
    expect(response.request().headers()['if-match']).toBe(initialTag)
    expect(response.headers()['x-apex-script-save-mode']).toBe('conditioned')
    await json(response)
    await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
    await staleName.fill('Retained stale draft')
    let writes = 0
    other.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) writes++ })
    const rejected = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
    await other.getByRole('button', { name: 'Save script', exact: true }).click()
    const conflictResponse = await rejected
    expect(conflictResponse.status()).toBe(412)
    expect(conflictResponse.request().headers()['if-match']).toBe(initialTag)
    const conflict = other.getByRole('region', { name: 'Script save reconciliation' })
    await expect(conflict).toBeVisible()
    await expect(staleName).toHaveValue('Retained stale draft')
    await expect(other.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
    expect(writes).toBe(1)
    await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
    await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeEnabled()
    await expect(staleName).toHaveValue('Retained stale draft')
    await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
    await expect(conflict.getByLabel('Script revision field diff')).toContainText('name')
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('Retained stale draft')
    expect(writes).toBe(1)
    await other.setViewportSize({ width: 760, height: 1000 })
    await conflict.scrollIntoViewIfNeeded()
    const box = await conflict.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(760)
    await expect(conflict.getByRole('button', { name: 'Reapply draft', exact: true })).toBeInViewport()
    const geometry = async () => conflict.evaluate(element => {
      const rect = (node: Element) => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }
      const ancestors = []
      for (let current: HTMLElement | null = element as HTMLElement; current; current = current.parentElement) {
        const style = getComputedStyle(current)
        ancestors.push({ tag: current.tagName, class: current.className, box: rect(current), scrollLeft: current.scrollLeft,
          clientWidth: current.clientWidth, scrollWidth: current.scrollWidth, overflowX: style.overflowX, minWidth: style.minWidth, display: style.display })
      }
      return { box: rect(element), heading: rect(element.querySelector('h3')!), ancestors, viewport: innerWidth }
    })
    const beforeCapture = await geometry()
    await conflict.screenshot({ path: testInfo.outputPath('script-revision-conflict.png'), animations: 'disabled' })
    const afterCapture = await geometry()
    const geometryPath = testInfo.outputPath('reconciliation-geometry.json')
    await writeFile(geometryPath, JSON.stringify({ beforeCapture, afterCapture }, null, 2))
    await testInfo.attach('reconciliation-geometry', { path: geometryPath, contentType: 'application/json' })
    expect(afterCapture.box.x, JSON.stringify(afterCapture)).toBeGreaterThanOrEqual(0)
    expect(afterCapture.box.x + afterCapture.box.width, JSON.stringify(afterCapture)).toBeLessThanOrEqual(760)
    expect(afterCapture.heading.x, JSON.stringify(afterCapture)).toBeGreaterThanOrEqual(afterCapture.box.x)
    expect(afterCapture.heading.x + afterCapture.heading.width, JSON.stringify(afterCapture)).toBeLessThanOrEqual(afterCapture.box.x + afterCapture.box.width)
    expect(await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).evaluate(element => {
      const box = element.getBoundingClientRect()
      return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
    })).toBe(true)
    await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
    await other.setViewportSize({ width: 1600, height: 1000 })
    await expect(conflict).toHaveCount(0)
    await expect(staleName).toHaveValue('Retained stale draft')
    expect(writes).toBe(1)
    const reappliedSave = mutation(other, 'PUT', `/api/v1/scripts/${id}`)
    await other.getByRole('button', { name: 'Save script', exact: true }).click()
    const reappliedResponse = await reappliedSave
    expect(reappliedResponse.request().headers()['if-match']).not.toBe(initialTag)
    expect(reappliedResponse.headers()['x-apex-script-save-mode']).toBe('conditioned')
    await json(reappliedResponse)
    const final = await json(await request.get(`/api/v1/scripts/${id}`))
    expect(final.script).toMatchObject({ name: 'Retained stale draft', config: { base_url: 'http://127.0.0.1:2' } })
    expect(writes).toBe(2)
  } finally { await other.close() }
})

test('placement-only changes invalidate save authority and explicit reapply preserves the current folder', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const folder = idOf(await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, { data: { name: 'Current placement', category: 'scripts' } })))
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Placement fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] } } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const name = page.getByRole('textbox', { name: 'Script name', exact: true })
  await expect(name).toHaveValue('Placement fixture')
  await name.fill('My placement draft')
  await json(await request.patch(`/api/v1/scripts/${id}/move`, { data: { folder_id: folder } }))
  const save = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  expect((await save).status()).toBe(412)
  await expect(name).toHaveValue('My placement draft')
  const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  const retried = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await retried)
  const final = await json(await request.get(`/api/v1/scripts/${id}`))
  expect(final.folder_id).toBe(folder)
  expect(final.script).toMatchObject({ name: 'My placement draft', folder_id: folder })
})

test('uncertain successful writes retain drafts and revoke retry authority until explicit review', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Uncertain fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] } } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const name = page.getByRole('textbox', { name: 'Script name', exact: true })
  await expect(name).toHaveValue('Uncertain fixture')
  let writes = 0
  await page.route(`**/api/v1/scripts/${id}`, async route => {
    if (route.request().method() !== 'PUT') { await route.continue(); return }
    writes++
    const response = await route.fetch()
    expect(response.ok()).toBeTruthy()
    const headers = { ...response.headers() }
    delete headers['x-apex-script-save-mode']
    // Genuine server commit; strip its receipt only on the browser return path.
    await route.fulfill({ response, headers })
  })
  try {
    await name.fill('Potentially committed draft')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
    await expect(conflict).toContainText('We could not confirm whether this save reached the server')
    await expect(name).toHaveValue('Potentially committed draft')
    await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
    expect(writes).toBe(1)
    const final = await json(await request.get(`/api/v1/scripts/${id}`))
    expect(final.script).toMatchObject({ name: 'Potentially committed draft' })
    await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
    await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
    // The server increments its generated RefKey on the committed write. The
    // authored draft is otherwise identical and values remain undisclosed.
    await expect(conflict.getByLabel('Script revision field diff').getByRole('listitem')).toHaveText(['ref_key'])
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('Potentially committed draft')
    expect(writes).toBe(1)
    await conflict.getByRole('button', { name: 'Reload script and discard draft', exact: true }).click()
    await expect(conflict).toHaveCount(0)
    await expect(name).toHaveValue('Potentially committed draft')
    await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
    expect(writes).toBe(1)
  } finally { await page.unrouteAll({ behavior: 'wait' }) }
})


test('library secret rebind retains exact revision authority and requires review after a real conflict', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId,
    script: { name: 'Library rebind fixture', version: '1', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
      actions: [{ name: 'Authorization', type: 'request', method: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.OLD_REFERENCE}}' } }] },
  } })))
  await json(await request.put(`/api/v1/scripts/${id}/library-assignment`, { data: { application_id: workspace.applicationId, release_ids: [], expected_revision: 0 } }))
  const observed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  const initialTag = observed.headers().etag
  expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[0-9a-f]{64}"$/)
  // Only the read-only vault-name catalogue is controlled; transformation and
  // both conflicting/current writes use the actual protected controller APIs.
  await page.route(`**/api/v1/applications/${workspace.applicationId}/secrets`, async route => {
    expect(route.request().method()).toBe('GET')
    await route.fulfill({ json: { application_id: workspace.applicationId, capabilities: { provider: 'database', writable: false },
      secrets: [{ name: 'EXISTING_REFERENCE', provider: 'database', writable: false }, { name: 'SECOND_REFERENCE', provider: 'database', writable: false }], count: 2 } })
  })
  let transformations = 0
  let browserWrites = 0
  page.on('request', item => { if (item.method() === 'PUT' && new URL(item.url()).pathname === `/api/v1/scripts/${id}`) browserWrites++ })
  await page.route('**/api/v1/scripts/secret-references/rebind', async route => {
    transformations++
    expect(route.request().headers()['if-match']).toBe(initialTag)
    const transformed = await route.fetch()
    expect(transformed.ok()).toBeTruthy()
    const external = await request.get(`/api/v1/scripts/${id}?view=authoring`)
    const body = await external.json()
    await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': external.headers().etag },
      data: { script: { ...body.script, name: 'Externally updated library script' } } }))
    await route.fulfill({ response: transformed })
  })
  try {
    await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    const row = page.locator('tbody tr').filter({ hasText: 'Library rebind fixture' })
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'View 1 missing secret detail for Library rebind fixture', exact: true }).click()
    const dialog = page.locator('.script-secret-bindings-dialog')
    await expect(dialog).toHaveAccessibleName('Secret Bindings · Library rebind fixture')
    const reuse = dialog.getByLabel('Use existing application or project secret', { exact: true })
    await reuse.selectOption('EXISTING_REFERENCE')
    const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
    await dialog.getByRole('button', { name: 'Rebind and share', exact: true }).click()
    const failed = await rejected
    expect(failed.status()).toBe(412)
    expect(failed.request().headers()['if-match']).toBe(initialTag)
    const conflict = dialog.getByRole('region', { name: 'Script save reconciliation' })
    await expect(conflict).toContainText('rebound draft is retained')
    await expect(dialog).toContainText('{{secret.EXISTING_REFERENCE}}')
    await expect(dialog.getByRole('button', { name: 'Rebind and share', exact: true })).toBeDisabled()
    expect(browserWrites).toBe(1)
    expect(transformations).toBe(1)
    await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
    await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
    await expect(conflict.getByLabel('Script revision field diff')).toContainText('actions')
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('created_at')
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('updated_at')
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('archived_at')
    await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('EXISTING_REFERENCE')
    await dialog.screenshot({ path: testInfo.outputPath('library-secret-revision-conflict.png') })
    await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
    expect(browserWrites).toBe(1)
    const finalSave = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
    await dialog.getByRole('button', { name: 'Save reviewed rebound draft', exact: true }).click()
    const saved = await finalSave
    expect(saved.request().headers()['if-match']).not.toBe(initialTag)
    expect(saved.headers()['x-apex-script-save-mode']).toBe('conditioned')
    await json(saved)
    await expect(dialog).toHaveCount(0)
    const final = await json(await request.get(`/api/v1/scripts/${id}`))
    expect(final.script).toMatchObject({ name: 'Externally updated library script', actions: [{ headers: { Authorization: 'Bearer {{secret.EXISTING_REFERENCE}}' } }] })
    expect(browserWrites).toBe(2)
    expect(transformations).toBe(1)
    await page.unroute('**/api/v1/scripts/secret-references/rebind')
    const editorBaseline = await request.get(`/api/v1/scripts/${id}?view=authoring`)
    const editorTag = editorBaseline.headers().etag
    await page.route('**/api/v1/scripts/secret-references/rebind', async route => {
      transformations++
      expect(route.request().headers()['if-match']).toBe(editorTag)
      await route.continue()
    })
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    const name = page.getByRole('textbox', { name: 'Script name', exact: true })
    await expect(name).toHaveValue('Externally updated library script')
    await page.getByRole('button', { name: 'Configure secrets', exact: true }).click()
    const editorDialog = page.locator('.script-secret-bindings-dialog')
    await editorDialog.getByLabel('Use existing application or project secret', { exact: true }).selectOption('SECOND_REFERENCE')
    const editorRebind = mutation(page, 'POST', '/api/v1/scripts/secret-references/rebind')
    await editorDialog.getByRole('button', { name: 'Rebind and share', exact: true }).click()
    await json(await editorRebind)
    await expect(editorDialog).toHaveCount(0)
    expect(browserWrites).toBe(2)
    const editorSave = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    const editorSaved = await editorSave
    expect(editorSaved.request().headers()['if-match']).toBe(editorTag)
    expect(editorSaved.headers()['x-apex-script-save-mode']).toBe('conditioned')
    await json(editorSaved)
    const editorFinal = await json(await request.get(`/api/v1/scripts/${id}`))
    expect(editorFinal.script).toMatchObject({ name: 'Externally updated library script', actions: [{ headers: { Authorization: 'Bearer {{secret.SECOND_REFERENCE}}' } }] })
    expect(browserWrites).toBe(3)
    expect(transformations).toBe(2)
  } finally { await page.unrouteAll({ behavior: 'wait' }) }
})


test('reapply preserves own JSON header keys and explicit deletions across a real writer', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId,
    script: { name: 'Own JSON keys fixture', version: '1', protocol: 'http',
      config: { base_url: 'http://127.0.0.1:1', http: { default_headers: { Accept: 'application/json', constructor: 'delete constructor', toString: 'delete toString' } } },
      actions: [{ name: 'No execution', type: 'request', method: 'GET', target: '/' }] },
  } })))
  const initial = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  const original = (await json(initial)).script as ScriptDefinition
  const initialTag = initial.headers().etag
  expect(initialTag).toMatch(/^"script-authoring-v1:scr-v1-[a-f0-9]{64}"$/)
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  await workbench.getByRole('tab', { name: 'DSL', exact: true }).click()
  const code = page.locator('.script-code-pane')
  await code.getByRole('button', { name: 'Edit JSON', exact: true }).click()
  const local = JSON.parse(JSON.stringify(original)) as ScriptDefinition
  local.name = 'Retained own-key draft'
  local.config.http!.default_headers = JSON.parse('{"Accept":"application/json","__proto__":"authored prototype header"}') as Record<string, string>
  await code.locator('.cm-content[contenteditable="true"]').fill(JSON.stringify(local, null, 2))
  await code.getByRole('button', { name: 'Apply changes', exact: true }).click()
  const remote = JSON.parse(JSON.stringify(original)) as ScriptDefinition
  remote.config.base_url = 'http://127.0.0.1:2'
  remote.config.http!.default_headers = { ...remote.config.http!.default_headers, Accept: 'text/plain', 'X-Remote': 'retained remote header' }
  await json(await request.put(`/api/v1/scripts/${id}`, { headers: { 'If-Match': initialTag }, data: { project_id: workspace.projectId, script: remote } }))
  const reviewed = await request.get(`/api/v1/scripts/${id}?view=authoring`)
  await json(reviewed)
  const reviewedTag = reviewed.headers().etag
  expect(reviewedTag).not.toBe(initialTag)
  const rejected = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  const rejectedResponse = await rejected
  expect(rejectedResponse.status()).toBe(412)
  expect(rejectedResponse.request().headers()['if-match']).toBe(initialTag)
  const conflict = page.getByRole('region', { name: 'Script save reconciliation' })
  await conflict.getByRole('button', { name: 'Load current baseline', exact: true }).click()
  await conflict.getByRole('button', { name: 'Diff', exact: true }).click()
  await expect(conflict.getByLabel('Script revision field diff')).toContainText('config.http.default_headers.__proto__')
  await expect(conflict.getByLabel('Script revision field diff')).not.toContainText('authored prototype header')
  await conflict.getByRole('button', { name: 'Reapply draft', exact: true }).click()
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  const savedResponse = await saved
  await json(savedResponse)
  expect(savedResponse.request().headers()['if-match']).toBe(reviewedTag)
  const submitted = savedResponse.request().postDataJSON().script as ScriptDefinition
  const expectedHeaders = JSON.parse('{"Accept":"text/plain","X-Remote":"retained remote header","__proto__":"authored prototype header"}') as Record<string, string>
  expect(Object.prototype.hasOwnProperty.call(submitted.config.http!.default_headers, '__proto__')).toBe(true)
  expect(Object.prototype.hasOwnProperty.call(submitted.config.http!.default_headers, 'constructor')).toBe(false)
  expect(Object.prototype.hasOwnProperty.call(submitted.config.http!.default_headers, 'toString')).toBe(false)
  expect(submitted.config.http!.default_headers).toEqual(expectedHeaders)
  const final = (await json(await request.get(`/api/v1/scripts/${id}`))).script as ScriptDefinition
  expect(final.name).toBe('Retained own-key draft')
  expect(final.config.base_url).toBe('http://127.0.0.1:2')
  expect(final.config.http!.default_headers).toEqual(expectedHeaders)
  expect(Object.getPrototypeOf(final.config.http!.default_headers)).toBe(Object.prototype)
  await page.reload()
  const reopened = (await json(await request.get(`/api/v1/scripts/${id}?view=authoring`))).script as ScriptDefinition
  expect(reopened.config.http!.default_headers).toEqual(expectedHeaders)
  await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(workbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue('http://127.0.0.1:2')
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
})
