import { test, expect, idOf, json, login } from './support/workflow'
import type { Page, TestInfo } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

interface PendingInspectorFrames {
  request: typeof window.requestAnimationFrame
  cancel: typeof window.cancelAnimationFrame
  frames: Map<number, FrameRequestCallback>
}
type FocusFixtureWindow = Window & { __apexloadPendingInspectorFrames?: PendingInspectorFrames }

async function preserveRapidInspectorEdit(page: Page, testInfo: TestInfo) {
  const editor = page.locator('.author-main-action')
  const target = editor.getByLabel('Target (path)', { exact: true })
  await page.evaluate(() => {
    const fixture = window as FocusFixtureWindow
    if (fixture.__apexloadPendingInspectorFrames) throw new Error('Inspector frame fixture already installed')
    const state: PendingInspectorFrames = { request: window.requestAnimationFrame, cancel: window.cancelAnimationFrame, frames: new Map() }
    fixture.__apexloadPendingInspectorFrames = state
    let id = 0
    window.requestAnimationFrame = callback => {
      if (state.frames.size >= 64) throw new Error('Inspector frame fixture exceeded its bound')
      const frame = --id; state.frames.set(frame, callback); return frame
    }
    window.cancelAnimationFrame = frame => { if (!state.frames.delete(frame)) state.cancel.call(window, frame) }
  })
  try {
    const selected = page.getByRole('navigation', { name: 'Script outline' }).getByRole('button', { name: /Create order.*POST/ })
    await selected.click()
    await expect(selected).toHaveAttribute('aria-current', 'true')
    await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order')
    await expect(editor.locator('.inspector-selection-path')).toContainText('script.actions[0]')
    await expect(target).toHaveValue('/orders')
    await target.focus()
    await page.keyboard.press('ControlOrMeta+A')
    const capture = () => page.evaluate(() => {
      const active = document.activeElement as HTMLInputElement | null
      return { owner: document.querySelector('.author-main-action .inspector-selection-path')?.textContent,
        active: { tag: active?.tagName, field: active?.getAttribute('data-author-field'), value: active?.value, start: active?.selectionStart, end: active?.selectionEnd },
        target: (document.querySelector('.author-main-action [data-author-field="target"]') as HTMLInputElement | null)?.value,
        queued_frames: (window as FocusFixtureWindow).__apexloadPendingInspectorFrames?.frames.size ?? 0 }
    })
    const before = await capture()
    await testInfo.attach('rapid-target-edit-before-frame', { body: JSON.stringify(before, null, 2), contentType: 'application/json' })
    expect(before.queued_frames).toBeGreaterThan(0)
    expect(before.active.field).toBe('target')
    expect([before.active.start, before.active.end]).toEqual([0, '/orders'.length])
    await page.evaluate(() => {
      const fixture = window as FocusFixtureWindow
      const state = fixture.__apexloadPendingInspectorFrames
      if (!state) throw new Error('Inspector frame fixture missing')
      const callbacks = [...state.frames.values()]; state.frames.clear()
      window.requestAnimationFrame = state.request; window.cancelAnimationFrame = state.cancel
      delete fixture.__apexloadPendingInspectorFrames
      callbacks.forEach(callback => callback.call(window, performance.now()))
    })
    const after = await capture()
    await testInfo.attach('rapid-target-edit-after-frame', { body: JSON.stringify(after, null, 2), contentType: 'application/json' })
    await expect(target).toBeFocused()
    expect(after.active.field).toBe('target')
    expect([after.active.start, after.active.end]).toEqual([0, '/orders'.length])
    // A locator key press would refocus and conceal an intercepted user keystroke.
    await page.keyboard.press('Delete')
    await expect(target).toHaveValue('')
    await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order')
    await expect(selected).toHaveAttribute('aria-current', 'true')
  } finally {
    await page.evaluate(() => {
      const fixture = window as FocusFixtureWindow
      const state = fixture.__apexloadPendingInspectorFrames
      if (state) { window.requestAnimationFrame = state.request; window.cancelAnimationFrame = state.cancel; state.frames.clear(); delete fixture.__apexloadPendingInspectorFrames }
    })
  }
}

test('Author creates, reverses, validates, edits source and saves without losing context', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Author_operator_journey', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:18787' },
    parameters: [{ name: 'customer', type: 'list', values: ['sample'] }],
    actions: [{ name: 'Create order', type: 'request', method: 'POST', target: '/orders', headers: { 'Content-Type': 'application/json' }, body: '{"customer":"{{param.customer}}"}' },
      { name: 'Poll order', type: 'control_loop', control: { count: 2, steps: [{ name: 'Read order', type: 'request', method: 'GET', target: '/orders/1' }] } }],
  } } })))
  await page.setViewportSize({ width: 1600, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  const outline = page.getByRole('navigation', { name: 'Script outline' })
  await outline.getByRole('button', { name: /Poll order.*Loop/ }).click()
  const editor = page.locator('.author-main-action')
  await expect(editor.getByRole('button', { name: 'Control flow', exact: true })).toBeVisible()
  await expect(editor.getByLabel('Target (path)', { exact: true })).toHaveCount(0)
  await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  let insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  await expect(insertion.getByLabel('Action name', { exact: true })).toBeFocused()
  await insertion.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  await insertion.getByLabel('Placement', { exact: true }).selectOption('inside')
  await insertion.getByLabel('Action name', { exact: true }).fill('Health check')
  await insertion.getByLabel('Target (required)', { exact: true }).fill('/health')
  await insertion.getByRole('button', { name: 'Insert action', exact: true }).click()
  await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  await expect(editor.locator('.inspector-selection-path')).toContainText('Actions › Poll order › Health check')
  await expect(outline.getByRole('button', { name: /Health check.*GET/ })).toHaveAttribute('aria-current', 'true')
  await page.getByRole('button', { name: 'Undo document change', exact: true }).click()
  await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Poll order')
  await page.getByRole('button', { name: 'Redo document change', exact: true }).click()
  await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  await editor.getByLabel('Target (path)', { exact: true }).fill('')
  await page.getByRole('button', { name: /^Author Problems \(/ }).click()
  const problems = page.getByRole('dialog', { name: 'Author Problems', exact: true })
  await expect(problems.getByRole('button', { name: /Action target is required/ })).toHaveCount(1)
  await problems.getByRole('button', { name: /Action target is required/ }).click()
  await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  await editor.getByLabel('Target (path)', { exact: true }).fill('/health')
  await preserveRapidInspectorEdit(page, testInfo)
  await page.getByRole('button', { name: /^Author Problems \(/ }).click()
  await page.getByRole('dialog', { name: 'Author Problems', exact: true }).getByRole('button', { name: /Action target is required/ }).click()
  await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  await editor.getByLabel('Target (path)', { exact: true }).fill('/orders')
  await editor.getByRole('button', { name: 'Edit request body in center' }).click()
  const body = page.getByRole('region', { name: 'Request body editor' })
  await expect(body.getByLabel('Editor language')).toHaveValue('json')
  await expect(body.locator('.cm-editor')).not.toHaveCSS('background-color', 'rgb(255, 255, 255)')
  await body.getByRole('button', { name: 'Check syntax', exact: true }).click()
  await expect(body).toContainText('Syntax is valid')
  await page.getByRole('tab', { name: 'Source', exact: true }).click()
  await page.getByRole('button', { name: 'Edit in Full Script JSON' }).click()
  await page.getByRole('button', { name: 'Edit JSON', exact: true }).click()
  const source = page.locator('.script-code-pane .cm-content[contenteditable="true"]')
  await source.fill('{')
  await expect(page.getByRole('button', { name: 'Apply changes', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Discard JSON edits', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Discard JSON edits', exact: true }).click()
  await page.getByRole('tab', { name: 'Action editor', exact: true }).click()
  await editor.getByLabel('Name', { exact: true }).fill('Create order revised')
  await page.getByRole('button', { name: 'Review changes', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Changes since last save' })).toContainText('Create order revised')
  await page.getByRole('dialog', { name: 'Changes since last save' }).getByRole('button', { name: 'Close', exact: true }).click()
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order revised')
  await page.setViewportSize({ width: 900, height: 850 })
  await expect(editor.getByLabel('Name', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Undo document change', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Undo document change', exact: true }).locator('svg')).toHaveCSS('fill', 'none')
  await page.screenshot({ path: testInfo.outputPath('author-operator-experience.png'), fullPage: true })
})
