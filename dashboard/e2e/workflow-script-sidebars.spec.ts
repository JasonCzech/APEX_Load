import { test, expect, idOf, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('script sidebars preserve selection, source scope, body editing and Debug options across narrow layouts', async ({ page }) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Sidebar_workflow', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
    parameters: [{ name: 'customer', type: 'list', values: ['example'], update: 'each_iteration' }],
    init: [{ name: 'Initialize', type: 'request', method: 'GET', target: '/initialize' }],
    actions: [{ name: 'Create order', type: 'request', method: 'POST', target: '/orders/{{param.customer}}', body: '{"customer":"{{param.customer}}"}' }, { name: 'Read order', type: 'request', method: 'GET', target: '/orders' }],
  } } })))
  await page.setViewportSize({ width: 1800, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const outline = page.getByRole('navigation', { name: 'Script outline', exact: true })
  await outline.getByRole('button', { name: /Create order/ }).click()
  await expect(page.getByRole('group', { name: 'Action property sections' })).toBeVisible()
  await page.getByRole('button', { name: 'Edit request body in center' }).click()
  const body = page.getByRole('region', { name: 'Request body editor' })
  await body.getByRole('textbox', { name: 'Request body' }).fill('{"customer":"{{param.customer}}","edited":true}')
  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  const parameters = page.getByRole('region', { name: 'Parameter Data', exact: true })
  await parameters.getByRole('navigation', { name: 'Parameter selection' }).getByRole('button', { name: /customer/ }).click()
  await parameters.getByRole('button', { name: 'Show uses in Source', exact: true }).click()
  const parameter = page.getByRole('region', { name: 'Parameter customer', exact: true })
  await expect(parameter.locator('input')).toHaveCount(0)
  await parameter.getByRole('button', { name: /Open customer usage in actions\[0\].target/ }).click()
  await expect(page.getByRole('button', { name: 'Full Script JSON', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('.script-code-pane')).toContainText('customer')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  const stored = await json(await request.get(`/api/v1/scripts/${id}`))
  expect((stored.script as { actions: { body: string }[] }).actions[0].body).toContain('"edited":true')
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  await options.getByRole('combobox', { name: 'Log level' }).selectOption('debug')
  await options.getByRole('checkbox', { name: 'Step through (start paused)' }).check()
  await expect(page.getByRole('button', { name: 'Debug options (2)', exact: true })).toBeVisible()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Debug options (2)', exact: true })).toBeFocused()
  for (const width of [900, 700]) {
    await page.setViewportSize({ width, height: 1000 })
    const panes = page.getByRole('group', { name: 'Debug panes' })
    await panes.getByRole('button', { name: 'Outline', exact: true }).click()
    const actions = page.getByRole('region', { name: 'Debugger actions' })
    await expect(actions.getByRole('button', { name: /^Action 1 Create order/ })).toBeVisible()
    expect((await actions.boundingBox())!.height).toBeGreaterThan(100)
    await expect(page.getByRole('checkbox', { name: 'Failures', exact: true })).toBeDisabled()
    await page.locator('.debugger-parameter-item').filter({ hasText: 'customer' }).click()
    await expect(page.getByRole('region', { name: 'Debug parameter customer' })).toContainText('Not captured')
    await page.getByRole('button', { name: 'Expand logs and problems' }).click()
    await panes.getByRole('button', { name: 'Outline', exact: true }).click()
    await expect(actions.getByRole('button', { name: /^Action 2 Read order/ })).toBeVisible()
    await page.getByRole('button', { name: 'Collapse logs and problems' }).click()
    await actions.getByRole('button', { name: /^Action 2 Read order/ }).click()
    await expect(page.locator('.script-code-title')).toContainText('actions[1]')
    await expect(panes.getByRole('button', { name: 'Editor', exact: true })).toHaveAttribute('aria-pressed', 'true')
  }
  await page.getByRole('button', { name: 'Debug options (2)', exact: true }).click()
  await options.getByRole('textbox', { name: 'Session Variables JSON' }).fill('[]')
  await expect(options.getByRole('textbox', { name: 'Session Variables JSON' })).toHaveAttribute('aria-invalid', 'true')
  await options.getByRole('button', { name: 'Reset session options' }).click()
  await expect(page.getByRole('button', { name: 'Debug options (0)', exact: true })).toBeVisible()
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true }).click()
  await page.getByRole('group', { name: 'Author panes' }).getByRole('button', { name: 'Outline', exact: true }).click()
  await expect(outline).toBeVisible()
  await page.getByRole('group', { name: 'Author panes' }).getByRole('button', { name: 'Editor', exact: true }).click()
  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Parameter Data', exact: true })).toBeVisible()
  await expect(outline).toBeHidden()
})

for (const width of [1800, 1100, 900, 700]) {
  test(`Source Mapping keeps equal source and destination widths through selection, wrap, resize and restore at ${width}px`, async ({ page }) => {
    test.setTimeout(120_000)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
      name: 'Source_mapping_split', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
      parameters: [{ name: 'customer', type: 'list', values: ['example'] }],
      actions: [{ name: 'Short request', type: 'request', method: 'GET', target: '/short' }, { name: 'Long request', type: 'request', method: 'POST', target: '/long', body: 'a'.repeat(2000) }],
      metadata: { import: { source: 'loadrunner', preserved: { source_review: {
        source_files: [{ path: 'Action.c', language: 'c', content: 'web_url("Short", LAST);\nweb_custom_request("' + 'long'.repeat(500) + '", LAST);' }],
        mappings: [
          { id: 'short', source_file: 'Action.c', start_line: 1, end_line: 1, function: 'short_request', target_path: 'actions[0]', status: 'converted' },
          { id: 'long', source_file: 'Action.c', start_line: 2, end_line: 2, function: 'long_request', target_path: 'actions[1].body', status: 'converted' },
        ],
      } } } },
    } } })))
    await page.setViewportSize({ width: 1800, height: 1000 })
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
    const mapping = page.getByRole('region', { name: 'Source Mapping', exact: true })
    await mapping.getByRole('button', { name: /^Show mappings/ }).click()
    await expect(mapping.locator('.source-map-review')).toHaveCSS('overflow-y', 'auto')
    const panels = mapping.locator('.source-map-panel')
    const equalSplit = async () => {
      await expect(panels.nth(0)).toBeVisible()
      await expect(panels.nth(1)).toBeVisible()
      // Read both panes in the same layout frame while the responsive sidebar animates.
      const [source, destination] = await panels.evaluateAll(elements => elements.map(element => {
        const { x, y, width, height } = element.getBoundingClientRect()
        return { x, y, width, height }
      }))
      expect(Math.abs(source.width - destination.width)).toBeLessThanOrEqual(1)
      const stacked = await mapping.locator('.source-map-panels').evaluate(element => getComputedStyle(element).flexDirection === 'column')
      if (stacked) {
        expect(Math.abs(source.x - destination.x)).toBeLessThanOrEqual(1)
        expect(destination.y).toBeGreaterThanOrEqual(source.y + source.height)
      } else {
        expect(Math.abs(source.y - destination.y)).toBeLessThanOrEqual(1)
        expect(destination.x).toBeGreaterThan(source.x + source.width)
      }
      const headers = panels.locator('.source-map-panel-header')
      expect(Math.abs((await headers.nth(0).boundingBox())!.height - (await headers.nth(1).boundingBox())!.height)).toBeLessThanOrEqual(1)
      expect(source.height).toBeGreaterThan(100)
      expect(source.height).toBeLessThanOrEqual(1000)
      expect(destination.height).toBeLessThanOrEqual(1000)
      await expect(panels.nth(0)).toHaveCSS('resize', 'none')
    }
    const longMapping = mapping.getByRole('row').filter({ hasText: 'long_request' })
    await longMapping.click()
    await expect(longMapping.locator('[data-mapping-select]')).toHaveAttribute('aria-current', 'true')
    await page.setViewportSize({ width, height: 1000 })
    await expect(longMapping.locator('[data-mapping-select]')).toHaveAttribute('aria-current', 'true')
    await equalSplit()
    for (const functionName of ['short_request', 'long_request']) {
      await mapping.getByRole('row').filter({ hasText: functionName }).click()
      await equalSplit()
    }
    await mapping.locator('.source-map-view-options > summary').click()
    await mapping.getByRole('button', { name: 'Word Wrap: On', exact: true }).click()
    await equalSplit()
    await mapping.getByRole('button', { name: 'Word Wrap: Off', exact: true }).click()
    await mapping.locator('.source-map-view-options > summary').click()
    await panels.nth(0).getByRole('button', { name: 'Expand source', exact: true }).click()
    await expect(panels.nth(1)).toBeHidden()
    await panels.nth(0).getByRole('button', { name: 'Return to comparison', exact: true }).click()
    await equalSplit()
    await panels.nth(1).getByRole('button', { name: 'Expand destination', exact: true }).click()
    await expect(panels.nth(0)).toBeHidden()
    await panels.nth(1).getByRole('button', { name: 'Return to comparison', exact: true }).click()
    await mapping.getByRole('button', { name: 'Hide mappings', exact: true }).click()
    await equalSplit()
    await mapping.getByRole('button', { name: /^Show mappings/ }).click()
    if (width === 700) {
      // A document tab selected from the full-width parameter workspace must reveal
      // the document even if the previous narrow pane was the inspector.
      const panes = page.getByRole('group', { name: 'Author panes' })
      await panes.getByRole('button', { name: 'Editor', exact: true }).click()
      await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
      const parameters = page.getByRole('region', { name: 'Parameter Data', exact: true })
      await parameters.getByRole('navigation', { name: 'Parameter selection' }).getByRole('button', { name: /customer/ }).click()
      await parameters.getByRole('button', { name: 'Show uses in Source', exact: true }).click()
      await page.getByRole('region', { name: 'Parameter customer', exact: true }).getByRole('button', { name: /^Edit in Parameter Data/ }).click()
      await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
      await expect(mapping).toBeVisible()
      await expect(panes.getByRole('button', { name: 'Editor', exact: true })).toHaveAttribute('aria-pressed', 'true')
      await equalSplit()
    }
  })
}

test('source mapping review decisions survive saving and moved or edited destinations require verified identity', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const source = {
    name: 'Mapping_review_workflow', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
    actions: [{ type: 'request', name: 'First', method: 'GET', target: '/first' }, { type: 'request', name: 'Second', method: 'GET', target: '/second' }],
    metadata: { import: { source: 'loadrunner', preserved: { source_review: {
      source_files: [{ path: 'Action.c', content: 'first();\nunsupported();' }, { path: 'globals.h', content: '#define COUNT 2', truncated: true }],
      omitted_source_files: ['missing.dat'], total_source_files: 3,
      mappings: [{ id: 'first', source_file: 'Action.c', start_line: 1, function: 'first', target_path: 'actions[0]', status: 'converted', support_mode: 'executable', notes: 'Check the first request.' },
        { id: 'source-only', source_file: 'Action.c', start_line: 2, function: 'unsupported', status: 'unsupported' }],
    } } } },
  }
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: source } })))
  const url = `/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`
  await page.setViewportSize({ width: 1800, height: 1100 })
  await page.goto(url)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
  const mapping = page.getByRole('region', { name: 'Source Mapping', exact: true })
  await expect(mapping.getByRole('table')).toBeHidden()
  await expect(mapping).toContainText('Destination identity: matched')
  await expect(mapping).toContainText('Check the first request.')
  const previewNotice = mapping.getByRole('note', { name: 'Source preview limits' })
  await expect(previewNotice).toContainText('Preview limits only — not an import issue')
  await previewNotice.locator('summary').click()
  await expect(previewNotice.getByRole('listitem')).toHaveCount(2)
  await expect(previewNotice.getByRole('listitem').filter({ hasText: 'missing.dat' })).toContainText('Not shown in preview')
  await previewNotice.getByRole('button', { name: 'View preview of globals.h' }).click()
  await expect(mapping.getByRole('combobox', { name: 'Captured source file (2)', exact: true })).toHaveValue('globals.h')
  await mapping.getByRole('combobox', { name: 'Captured source file (2)', exact: true }).selectOption('Action.c')
  await previewNotice.locator('summary').click()
  await mapping.getByText('Review & mapping details · Not reviewed', { exact: true }).click()
  await mapping.getByLabel('Review note', { exact: true }).fill('Checked against captured source')
  await mapping.getByRole('button', { name: 'Record review decision', exact: true }).click()
  await expect(mapping).toContainText('Acknowledged')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  const saved = await json(await request.get(`/api/v1/scripts/${id}`))
  const savedScript = (saved.script || saved) as typeof source
  const persisted = savedScript.metadata.import.preserved as typeof source.metadata.import.preserved & { source_mapping_review: { decisions: Record<string, { reviewer: string; note: string }> } }
  expect(persisted.source_mapping_review.decisions.first.reviewer).toBeTruthy()
  expect(persisted.source_mapping_review.decisions.first.note).toBe('Checked against captured source')
  // Exercise the saved contract as an external draft edit, then reopen through the real UI.
  await json(await request.put(`/api/v1/scripts/${id}`, { data: { script: { ...savedScript, actions: [...savedScript.actions].reverse() } } }))
  await page.reload()
  await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
  await expect(mapping).toContainText('Destination identity: moved')
  await expect(mapping.locator('.source-map-panel').nth(1)).toContainText('actions[1]')
  await expect(mapping).toContainText('Acknowledged')
  const moved = { ...savedScript, actions: [savedScript.actions[1], { ...savedScript.actions[0], target: '/edited' }] }
  await json(await request.put(`/api/v1/scripts/${id}`, { data: { script: moved } }))
  await page.reload()
  await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
  await expect(mapping).toContainText('Destination identity: changed')
  await expect(mapping).toContainText('Decision needs renewed review')
  await mapping.getByText('Review & mapping details · Decision needs renewed review', { exact: true }).click()
  await mapping.getByLabel('Destination path', { exact: true }).fill('actions[1]')
  await mapping.getByRole('button', { name: 'Re-link to this destination', exact: true }).click()
  await expect(mapping).toContainText('Destination identity: matched')
  await expect(mapping).toContainText('Not reviewed')
  await mapping.getByRole('button', { name: /^Show mappings/ }).click()
  await mapping.getByRole('row').filter({ hasText: 'unsupported' }).click()
  await mapping.getByText('Review & mapping details · Not reviewed', { exact: true }).click()
  await mapping.getByRole('combobox', { name: 'Review decision', exact: true }).selectOption('needs_changes')
  await mapping.getByRole('button', { name: 'Record review decision', exact: true }).click()
  await expect(mapping).toContainText('Review & mapping details · Needs changes')
})
