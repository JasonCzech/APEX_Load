# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-author-experience.spec.ts >> Author creates, reverses, validates, edits source and saves without losing context
- Location: e2e/workflow-author-experience.spec.ts:76:1

# Error details

```
Error: expect(locator).toBeFocused() failed

Locator:  locator('.author-main-action').getByLabel('Target (path)', { exact: true })
Expected: focused
Received: inactive
Timeout:  15000ms

Call log:
  - Expect "toBeFocused" with timeout 15000ms
  - waiting for locator('.author-main-action').getByLabel('Target (path)', { exact: true })
    34 × locator resolved to <input value="/orders" data-author-field="target" placeholder="/api/resource"/>
       - unexpected value "inactive"

```

```yaml
- textbox "Target (path)":
  - /placeholder: /api/resource
  - text: /orders
```

# Test source

```ts
  1   | import { test, expect, idOf, json, login } from './support/workflow'
  2   | import type { Page, TestInfo } from '@playwright/test'
  3   | import { createWorkspace, scopeQuery } from './support/workspace'
  4   | 
  5   | interface PendingInspectorFrames {
  6   |   request: typeof window.requestAnimationFrame
  7   |   cancel: typeof window.cancelAnimationFrame
  8   |   frames: Map<number, FrameRequestCallback>
  9   | }
  10  | type FocusFixtureWindow = Window & { __apexloadPendingInspectorFrames?: PendingInspectorFrames }
  11  | 
  12  | async function preserveRapidInspectorEdit(page: Page, testInfo: TestInfo) {
  13  |   const editor = page.locator('.author-main-action')
  14  |   const target = editor.getByLabel('Target (path)', { exact: true })
  15  |   await page.evaluate(() => {
  16  |     const fixture = window as FocusFixtureWindow
  17  |     if (fixture.__apexloadPendingInspectorFrames) throw new Error('Inspector frame fixture already installed')
  18  |     const state: PendingInspectorFrames = { request: window.requestAnimationFrame, cancel: window.cancelAnimationFrame, frames: new Map() }
  19  |     fixture.__apexloadPendingInspectorFrames = state
  20  |     let id = 0
  21  |     window.requestAnimationFrame = callback => {
  22  |       if (state.frames.size >= 64) throw new Error('Inspector frame fixture exceeded its bound')
  23  |       const frame = --id; state.frames.set(frame, callback); return frame
  24  |     }
  25  |     window.cancelAnimationFrame = frame => { if (!state.frames.delete(frame)) state.cancel.call(window, frame) }
  26  |   })
  27  |   try {
  28  |     const selected = page.getByRole('navigation', { name: 'Script outline' }).getByRole('button', { name: /Create order.*POST/ })
  29  |     await selected.click()
  30  |     await expect(selected).toHaveAttribute('aria-current', 'true')
  31  |     await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order')
  32  |     await expect(editor.locator('.inspector-selection-path')).toContainText('script.actions[0]')
  33  |     await expect(target).toHaveValue('/orders')
  34  |     await target.focus()
  35  |     await page.keyboard.press('ControlOrMeta+A')
  36  |     const capture = () => page.evaluate(() => {
  37  |       const active = document.activeElement as HTMLInputElement | null
  38  |       return { owner: document.querySelector('.author-main-action .inspector-selection-path')?.textContent,
  39  |         active: { tag: active?.tagName, field: active?.getAttribute('data-author-field'), value: active?.value, start: active?.selectionStart, end: active?.selectionEnd },
  40  |         target: (document.querySelector('.author-main-action [data-author-field="target"]') as HTMLInputElement | null)?.value,
  41  |         queued_frames: (window as FocusFixtureWindow).__apexloadPendingInspectorFrames?.frames.size ?? 0 }
  42  |     })
  43  |     const before = await capture()
  44  |     await testInfo.attach('rapid-target-edit-before-frame', { body: JSON.stringify(before, null, 2), contentType: 'application/json' })
  45  |     expect(before.queued_frames).toBeGreaterThan(0)
  46  |     expect(before.active.field).toBe('target')
  47  |     expect([before.active.start, before.active.end]).toEqual([0, '/orders'.length])
  48  |     await page.evaluate(() => {
  49  |       const fixture = window as FocusFixtureWindow
  50  |       const state = fixture.__apexloadPendingInspectorFrames
  51  |       if (!state) throw new Error('Inspector frame fixture missing')
  52  |       const callbacks = [...state.frames.values()]; state.frames.clear()
  53  |       window.requestAnimationFrame = state.request; window.cancelAnimationFrame = state.cancel
  54  |       delete fixture.__apexloadPendingInspectorFrames
  55  |       callbacks.forEach(callback => callback.call(window, performance.now()))
  56  |     })
  57  |     const after = await capture()
  58  |     await testInfo.attach('rapid-target-edit-after-frame', { body: JSON.stringify(after, null, 2), contentType: 'application/json' })
> 59  |     await expect(target).toBeFocused()
      |                          ^ Error: expect(locator).toBeFocused() failed
  60  |     expect(after.active.field).toBe('target')
  61  |     expect([after.active.start, after.active.end]).toEqual([0, '/orders'.length])
  62  |     // A locator key press would refocus and conceal an intercepted user keystroke.
  63  |     await page.keyboard.press('Delete')
  64  |     await expect(target).toHaveValue('')
  65  |     await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order')
  66  |     await expect(selected).toHaveAttribute('aria-current', 'true')
  67  |   } finally {
  68  |     await page.evaluate(() => {
  69  |       const fixture = window as FocusFixtureWindow
  70  |       const state = fixture.__apexloadPendingInspectorFrames
  71  |       if (state) { window.requestAnimationFrame = state.request; window.cancelAnimationFrame = state.cancel; state.frames.clear(); delete fixture.__apexloadPendingInspectorFrames }
  72  |     })
  73  |   }
  74  | }
  75  | 
  76  | test('Author creates, reverses, validates, edits source and saves without losing context', async ({ page }, testInfo) => {
  77  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  78  |   const request = page.context().request
  79  |   const workspace = await createWorkspace(request)
  80  |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
  81  |     name: 'Author_operator_journey', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:18787' },
  82  |     parameters: [{ name: 'customer', type: 'list', values: ['sample'] }],
  83  |     actions: [{ name: 'Create order', type: 'request', method: 'POST', target: '/orders', headers: { 'Content-Type': 'application/json' }, body: '{"customer":"{{param.customer}}"}' },
  84  |       { name: 'Poll order', type: 'control_loop', control: { count: 2, steps: [{ name: 'Read order', type: 'request', method: 'GET', target: '/orders/1' }] } }],
  85  |   } } })))
  86  |   await page.setViewportSize({ width: 1600, height: 1000 })
  87  |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  88  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  89  |   await expect(page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true })).toHaveAttribute('aria-pressed', 'true')
  90  |   const outline = page.getByRole('navigation', { name: 'Script outline' })
  91  |   await outline.getByRole('button', { name: /Poll order.*Loop/ }).click()
  92  |   const editor = page.locator('.author-main-action')
  93  |   await expect(editor.getByRole('button', { name: 'Control flow', exact: true })).toBeVisible()
  94  |   await expect(editor.getByLabel('Target (path)', { exact: true })).toHaveCount(0)
  95  |   await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  96  |   let insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  97  |   await expect(insertion.getByLabel('Action name', { exact: true })).toBeFocused()
  98  |   await insertion.getByRole('button', { name: 'Cancel', exact: true }).click()
  99  |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  100 |   await editor.getByRole('button', { name: '+ Add Action', exact: true }).click()
  101 |   insertion = page.getByRole('dialog', { name: 'Insert action or block' })
  102 |   await insertion.getByLabel('Placement', { exact: true }).selectOption('inside')
  103 |   await insertion.getByLabel('Action name', { exact: true }).fill('Health check')
  104 |   await insertion.getByLabel('Target (required)', { exact: true }).fill('/health')
  105 |   await insertion.getByRole('button', { name: 'Insert action', exact: true }).click()
  106 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  107 |   await expect(editor.locator('.inspector-selection-path')).toContainText('Actions › Poll order › Health check')
  108 |   await expect(outline.getByRole('button', { name: /Health check.*GET/ })).toHaveAttribute('aria-current', 'true')
  109 |   await page.getByRole('button', { name: 'Undo document change', exact: true }).click()
  110 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Poll order')
  111 |   await page.getByRole('button', { name: 'Redo document change', exact: true }).click()
  112 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Health check')
  113 |   await editor.getByLabel('Target (path)', { exact: true }).fill('')
  114 |   await page.getByRole('button', { name: /^Author Problems \(/ }).click()
  115 |   const problems = page.getByRole('dialog', { name: 'Author Problems', exact: true })
  116 |   await expect(problems.getByRole('button', { name: /Action target is required/ })).toHaveCount(1)
  117 |   await problems.getByRole('button', { name: /Action target is required/ }).click()
  118 |   await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  119 |   await editor.getByLabel('Target (path)', { exact: true }).fill('/health')
  120 |   await preserveRapidInspectorEdit(page, testInfo)
  121 |   await page.getByRole('button', { name: /^Author Problems \(/ }).click()
  122 |   await page.getByRole('dialog', { name: 'Author Problems', exact: true }).getByRole('button', { name: /Action target is required/ }).click()
  123 |   await expect(editor.getByLabel('Target (path)', { exact: true })).toBeFocused()
  124 |   await editor.getByLabel('Target (path)', { exact: true }).fill('/orders')
  125 |   await editor.getByRole('button', { name: 'Edit request body in center' }).click()
  126 |   const body = page.getByRole('region', { name: 'Request body editor' })
  127 |   await expect(body.getByLabel('Editor language')).toHaveValue('json')
  128 |   await expect(body.locator('.cm-editor')).not.toHaveCSS('background-color', 'rgb(255, 255, 255)')
  129 |   await body.getByRole('button', { name: 'Check syntax', exact: true }).click()
  130 |   await expect(body).toContainText('Syntax is valid')
  131 |   await page.getByRole('tab', { name: 'Source', exact: true }).click()
  132 |   await page.getByRole('button', { name: 'Edit in Full Script JSON' }).click()
  133 |   await page.getByRole('button', { name: 'Edit JSON', exact: true }).click()
  134 |   const source = page.locator('.script-code-pane .cm-content[contenteditable="true"]')
  135 |   await source.fill('{')
  136 |   await expect(page.getByRole('button', { name: 'Apply changes', exact: true })).toBeDisabled()
  137 |   await expect(page.getByRole('button', { name: 'Discard JSON edits', exact: true })).toBeVisible()
  138 |   await page.getByRole('button', { name: 'Discard JSON edits', exact: true }).click()
  139 |   await page.getByRole('tab', { name: 'Action editor', exact: true }).click()
  140 |   await editor.getByLabel('Name', { exact: true }).fill('Create order revised')
  141 |   await page.getByRole('button', { name: 'Review changes', exact: true }).click()
  142 |   await expect(page.getByRole('dialog', { name: 'Changes since last save' })).toContainText('Create order revised')
  143 |   await page.getByRole('dialog', { name: 'Changes since last save' }).getByRole('button', { name: 'Close', exact: true }).click()
  144 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  145 |   await expect(page.getByLabel('Script evidence states')).toContainText('Saved: up to date')
  146 |   await expect(editor.getByLabel('Name', { exact: true })).toHaveValue('Create order revised')
  147 |   await page.setViewportSize({ width: 900, height: 850 })
  148 |   await expect(editor.getByLabel('Name', { exact: true })).toBeVisible()
  149 |   await expect(page.getByRole('button', { name: 'Undo document change', exact: true })).toBeVisible()
  150 |   await expect(page.getByRole('button', { name: 'Undo document change', exact: true }).locator('svg')).toHaveCSS('fill', 'none')
  151 |   await page.screenshot({ path: testInfo.outputPath('author-operator-experience.png'), fullPage: true })
  152 | })
  153 | 
```