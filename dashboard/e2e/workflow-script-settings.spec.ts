import { test, expect, idOf, mutation, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('runtime evidence uses the imported source treatment and keeps checkbox controls left aligned', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: {
      name: 'Runtime_evidence_layout', version: '1.0', protocol: 'http',
      config: { base_url: 'https://example.invalid', http: { keep_alive: false, cache_enabled: true } },
      actions: [{ name: 'Fixture', type: 'request', method: 'GET', target: '/' }],
      metadata: { import: { source: 'loadrunner', preserved: {
        runtime_files: { 'default.cfg': { WEB: { KeepAlive: '0' }, ModemSpeed: { EnableModemSpeed: '0' } } },
        runtime_setting_dispositions: { 'default.cfg': { WEB: { KeepAlive: { status: 'mapped', fields: ['config.http.keep_alive'] } }, ModemSpeed: { EnableModemSpeed: { status: 'non_applicable' } } } },
      } } },
    },
  } })))
  await page.setViewportSize({ width: 1600, height: 1000 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  const runtime = page.getByRole('region', { name: 'Runtime Settings', exact: true })
  const navigation = runtime.getByRole('navigation', { name: 'Runtime setting categories' })
  await navigation.getByRole('tab', { name: 'Imported Source Runtime Settings', exact: true }).click()
  await expect(runtime.locator('.imported-runtime-table').filter({ hasText: 'KeepAlive' })).toContainText('KeepAlive')
  await expect(navigation.getByRole('tab', { name: 'Preserved run order', exact: true })).toHaveCount(0)
  await expect(runtime.getByRole('region', { name: 'Preserved run order' })).toBeVisible()
  const groups = runtime.getByRole('group', { name: 'Imported runtime setting groups' })
  const webGroup = groups.getByRole('button', { name: /\[WEB\]/ })
  await expect(webGroup).toHaveAttribute('aria-pressed', 'true')
  await expect(runtime.locator('.imported-runtime-table')).toHaveCount(2)
  await expect(runtime.locator('.imported-runtime-table[data-table-pagination="off"]')).toHaveCount(2)
  await expect(runtime.locator('.parameter-sidebar-list > .imported-runtime-file summary')).toHaveCount(0)
  await expect(runtime.locator('.table-pagination-toolbar')).toHaveCount(0)
  await webGroup.click()
  await expect(runtime.locator('.imported-runtime-table')).toHaveCount(1)
  await groups.getByRole('button', { name: 'None', exact: true }).click()
  await expect(runtime.locator('.imported-runtime-table')).toHaveCount(0)
  await groups.getByRole('button', { name: 'All', exact: true }).click()
  await expect(runtime.locator('.imported-runtime-table')).toHaveCount(2)
  await runtime.locator('.imported-runtime-table').first().scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('imported-source-runtime-reference.png') })
  await page.setViewportSize({ width: 900, height: 850 })
  await expect(groups.getByRole('button', { name: /\[ModemSpeed\]/ })).toBeVisible()
  await expect(runtime.locator('.imported-runtime-file-header')).toBeVisible()
  await groups.scrollIntoViewIfNeeded()
  const importedTableFits = await runtime.locator('.imported-runtime-table').first().evaluate(table => {
    const wrapper = table.closest('.imported-runtime-table-wrap')!
    const dispositionHeading = table.querySelector('thead th:last-child')!
    return dispositionHeading.getBoundingClientRect().right <= wrapper.getBoundingClientRect().right + 1
  })
  expect(importedTableFits).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('imported-source-runtime-narrow.png') })
  await page.setViewportSize({ width: 1600, height: 1000 })
  await navigation.getByRole('tab', { name: 'HTTP Transport', exact: true }).click()
  await runtime.getByRole('tab', { name: /^Session & Cache/ }).click()
  const evidence = runtime.locator('.runtime-field-evidence')
  await evidence.locator(':scope > summary').click()
  await expect(evidence.locator('.runtime-evidence-table').first()).toContainText('Keep connections alive')
  await expect(evidence.locator('.runtime-evidence-setting-path').filter({ hasText: 'http.keep_alive' }).first()).toBeVisible()
  await expect(evidence.locator('.runtime-evidence-support.healthy').first()).toBeVisible()
  await expect(evidence.locator('.runtime-evidence-row-details').first()).toBeVisible()
  await expect(evidence.locator('.runtime-evidence-row-details details')).toHaveCount(0)
  await expect(evidence).not.toContainText('Check protocol applicability')
  const cookieToggle = runtime.locator('.scenario-option-card').filter({ hasText: 'Enable cookie jar' })
  const checkAlignment = async () => {
    const metrics = await cookieToggle.evaluate(label => {
      const checkbox = label.querySelector('input[type="checkbox"]')!
      const title = label.querySelector('strong')!
      const description = label.querySelector('span')!
      return { display: getComputedStyle(label).display, inset: checkbox.getBoundingClientRect().left - label.getBoundingClientRect().left, width: checkbox.getBoundingClientRect().width, titleBottom: title.getBoundingClientRect().bottom, descriptionTop: description.getBoundingClientRect().top }
    })
    expect(metrics.display).toBe('grid')
    expect(metrics.inset).toBeLessThan(24)
    expect(metrics.width).toBeLessThan(30)
    expect(metrics.descriptionTop).toBeGreaterThanOrEqual(metrics.titleBottom)
  }
  await checkAlignment()
  await evidence.scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('runtime-evidence-wide.png') })
  await page.setViewportSize({ width: 900, height: 850 })
  await checkAlignment()
  const tableFits = await evidence.locator('.runtime-evidence-table').first().evaluate(table => {
    const wrapper = table.closest('.runtime-evidence-table-wrap')!
    const lastHeading = table.querySelector('thead th:last-child')!
    return lastHeading.getBoundingClientRect().right <= wrapper.getBoundingClientRect().right + 1
  })
  expect(tableFits).toBe(true)
  await cookieToggle.scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('runtime-checkbox-narrow.png') })
  await evidence.locator(':scope > summary').scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('runtime-evidence-narrow.png') })
})

// Exercises the real app shell's pagination observer and persisted controller DSL.
test('parameter workspace preserves 4000 rows, references, masked values and runtime settings', async ({ page }) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Settings_workflow', version: '1.0.0', protocol: 'http',
      config: { base_url: 'http://127.0.0.1:1', think_time: { type: 'fixed', mean_ms: 2000, ignore: true }, run_logic: { iterations: 2, global_iterations: 100, max_transactions: 30, block_scheduling: 'random', action_sequence: ['teardown', 'action', 'init'] } },
      parameters: [{ name: 'customer', type: 'file', data_source: 'users', column: 'id', update: 'each_iteration' }, { name: 'paired', type: 'file', data_source: 'users', column: 'segment', same_line_as: 'customer' }],
      data_sources: [{ name: 'users', type: 'csv', first_row_header: true, strategy: 'sequential', inline_data: 'id,segment\n' + Array.from({ length: 4000 }, (_, i) => `user${i + 1},example${i + 1}`).join('\n') }],
      actions: [{ name: 'orders', type: 'request', method: 'GET', target: '/orders/{{param.customer}}/{{data.users.id}}' }],
    },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  // Login hydrated workspace providers before the API fixture was created.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('region', { name: 'Author center workbench' })).toBeVisible()
  const authorPanes = page.getByRole('group', { name: 'Author panes' })
  if (await authorPanes.isVisible()) await authorPanes.getByRole('button', { name: 'Editor', exact: true }).click()
  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Close script editor', exact: true })).toBeVisible()
  const panel = page.getByRole('region', { name: 'Parameter Data', exact: true })
  await panel.getByRole('textbox', { name: 'Parameter name', exact: true }).fill('account')
  await panel.getByRole('button', { name: 'Rename references' }).click()
  await panel.getByRole('tab', { name: 'Usage', exact: true }).click()
  await expect(panel.getByRole('tabpanel', { name: 'Parameter usage' })).toContainText('parameters[1].same_line_as')
  await panel.getByRole('button', { name: 'Undo structural edit' }).click()
  await expect(panel.getByRole('navigation', { name: 'Parameter selection' })).toContainText('customer')
  await panel.getByRole('tab', { name: 'Settings', exact: true }).click()
  await panel.getByRole('textbox', { name: 'Parameter name', exact: true }).fill('account')
  await panel.getByRole('button', { name: 'Rename references' }).click()
  await panel.getByRole('button', { name: 'Preview value', exact: true }).click()
  await expect(panel.getByRole('button', { name: 'Reveal value', exact: true })).toBeVisible()
  await expect(panel.locator('output')).toHaveText('••••••••')
  await panel.getByRole('button', { name: 'Reveal value', exact: true }).click()
  await expect(panel.locator('output')).toHaveText('user1')
  await panel.getByRole('button', { name: 'Datasets', exact: true }).click()
  await expect(panel).toContainText('4000 rows')
  await expect(panel.locator('.table-pagination-toolbar')).toHaveCount(0)
  for (const row of [2000, 4000, 1]) {
    await panel.getByRole('spinbutton', { name: 'Go to row for users' }).fill(String(row))
    await panel.getByRole('button', { name: 'Go', exact: true }).click()
    await expect(panel.getByLabel(`Row ${row}, id for users`, { exact: true })).toHaveValue(`user${row}`)
  }
  const firstCell = panel.getByLabel('Row 1, id for users', { exact: true })
  await firstCell.fill('edited1')
  await firstCell.press('Tab')
  await panel.getByLabel('Search rows for users').fill('user4000')
  await expect(panel.getByLabel('Row 4000, id for users', { exact: true })).toHaveValue('user4000')
  await panel.getByLabel('Search rows for users').fill('')
  const column = panel.getByLabel('Column 1 name for users')
  await column.fill('account_id')
  await column.press('Enter')
  await panel.getByRole('button', { name: 'Remove column account_id', exact: true }).click()
  const review = page.getByRole('dialog', { name: 'Review dataset change' })
  await expect(review).toContainText('Remap these references')
  await expect(review.getByRole('button', { name: 'Apply dataset change' })).toBeDisabled()
  await review.getByRole('button', { name: 'Cancel', exact: true }).click()

  await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Close script editor', exact: true })).toBeVisible()
  const runtime = page.getByRole('region', { name: 'Runtime Settings', exact: true })
  const navigation = runtime.getByRole('navigation', { name: 'Runtime setting categories' })
  await navigation.getByRole('tab', { name: 'Error Handling', exact: true }).click()
  await runtime.getByRole('combobox', { name: 'Exchange capture', exact: true }).selectOption('sampled')
  await runtime.getByRole('combobox', { name: 'Include fetched resources', exact: true }).selectOption('false')
  await expect(runtime.getByRole('spinbutton', { name: 'Capture sample rate', exact: true })).toHaveValue('1')
  await expect(runtime.getByRole('combobox', { name: 'Include fetched resources', exact: true })).toHaveValue('false')
  await navigation.getByRole('tab', { name: 'Think Time', exact: true }).click()
  await expect(runtime.locator('.runtime-context-details')).not.toHaveAttribute('open', '')
  await expect(runtime.getByText('Editing: Script defaults', { exact: true })).toBeVisible()
  await expect(runtime.getByRole('checkbox', { name: 'Enable think time' })).not.toBeChecked()
  await expect(runtime.locator('fieldset')).toHaveAttribute('disabled', '')
  await expect(runtime.locator('fieldset input').first()).toBeDisabled()
  await runtime.getByRole('checkbox', { name: 'Enable think time' }).check()
  await expect(runtime.locator('fieldset input').first()).toBeEnabled()
  await navigation.getByRole('tab', { name: 'Imported Source Runtime Settings', exact: true }).click()
  await expect(runtime.getByRole('region', { name: 'Preserved run order' })).toContainText('does not affect execution')
  await expect(runtime.getByRole('combobox', { name: 'Block Scheduling' })).toHaveCount(0)
  await navigation.getByRole('tab', { name: 'Run Logic', exact: true }).click()
  await runtime.getByRole('combobox', { name: 'Workload Model' }).selectOption('open')
  await expect(runtime.getByRole('spinbutton', { name: 'Arrival target', exact: true })).toHaveValue('1')
  await runtime.getByRole('spinbutton', { name: 'Maximum in-flight iterations' }).fill('20')
  await runtime.getByRole('combobox', { name: 'Runtime preview context' }).selectOption('debug')
  await runtime.locator('.runtime-context-details > summary').click()
  await expect(runtime.getByText('Editing: Script defaults', { exact: true })).toBeVisible()
  await expect(runtime.getByText(/Replay executes one virtual user/)).toBeVisible()
  await expect(runtime.getByRole('region', { name: 'Effective runtime context' })).toContainText('Replay executes one virtual user')
  await navigation.getByRole('tab', { name: 'HTTP Transport', exact: true }).click()
  await runtime.getByRole('tab', { name: /^Session & Cache(?:\s+\d+)?$/ }).click()
  await runtime.getByRole('spinbutton', { name: 'Response read buffer (bytes)', exact: true }).fill('12288')
  await runtime.getByRole('spinbutton', { name: 'Meta-refresh limit per URL', exact: true }).fill('2')
  await runtime.getByRole('spinbutton', { name: 'Content-check error match limit', exact: true }).fill('10')
  await runtime.getByRole('tab', { name: 'Proxy', exact: true }).click()
  await runtime.getByRole('combobox', { name: 'Proxy Mode', exact: true }).selectOption('pac')
  await runtime.getByRole('textbox', { name: 'PAC URL', exact: true }).fill('https://proxy.example.invalid/config.pac')
  const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await saved)
  const stored = (await json(await request.get(`/api/v1/scripts/${id}`))).script as { actions: { target: string }[]; parameters: { name: string; column: string; same_line_as?: string }[]; data_sources: { inline_data: string }[]; config: Record<string, unknown> }
  expect(stored.config.results).toMatchObject({ message_detail: { mode: 'sampled', sample_rate: 1, include_resources: false } })
  expect(stored.config.http).toMatchObject({receive_buffer_bytes:12288,max_meta_refresh:2,content_check_error_limit:10,proxy_mode:'pac',proxy:{pac_url:'https://proxy.example.invalid/config.pac'}})
  expect(stored.actions[0].target).toBe('/orders/{{param.account}}/{{data.users.account_id}}')
  expect(stored.parameters[1].same_line_as).toBe('account')
  expect(stored.data_sources[0].inline_data).toContain('edited1,example1')
  expect(stored.data_sources[0].inline_data).toContain('user4000,example4000')
  expect(stored.config).toMatchObject({ run_logic: { workload_model: 'open', max_in_flight: 20, block_scheduling: 'random' }, pacing: { type: 'tps', tps: 1 }, think_time: { mean_ms: 2000 } })
  expect((stored.config.think_time as { ignore?: boolean }).ignore ?? false).toBe(false)
  await page.reload()
  await page.getByRole('tab', { name: 'Parameters', exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Parameter selection' })).toContainText('account')
  await page.setViewportSize({ width: 900, height: 1000 })
  await expect(page.getByRole('searchbox', { name: 'Find parameter' })).toBeVisible()
  await page.getByRole('searchbox', { name: 'Find parameter' }).fill('paired')
  await expect(page.getByRole('navigation', { name: 'Parameter selection' }).getByRole('button')).toHaveCount(1)
})

test('older imported draft retains its saved snapshot classification until re-import', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name: 'Legacy_runtime_draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.invalid' },
      actions: [{ name: 'fixture', type: 'request', method: 'GET', target: '/' }],
      metadata: { import: { source: 'loadrunner', preserved: {
        runtime_files: { 'default.cfg': { WEB: { EnableSnapshotsInReplay: '1' } } },
        runtime_setting_dispositions: { 'default.cfg': { WEB: { EnableSnapshotsInReplay: { status: 'non_applicable', note: 'Legacy importer classification; re-import to obtain new mappings.' } } } },
      } } },
    },
  } })))
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  await page.getByRole('tab', { name: 'Imported Source Runtime Settings', exact: true }).click()
  const row = page.locator('.imported-runtime-table tr').filter({ hasText: 'EnableSnapshotsInReplay' })
  await expect(row).toContainText('Not applicable')
  await expect(row.locator('.imported-runtime-disposition')).toHaveAttribute('title', /Legacy importer classification/)
  const stored = (await json(await request.get(`/api/v1/scripts/${id}`))).script as { config: { results?: { message_detail?: unknown } } }
  expect(stored.config.results?.message_detail).toBeUndefined()
  await page.screenshot({ path: testInfo.outputPath('legacy-runtime-classification.png'), fullPage: true })
})

test('property promotion survives capture changes and script save reopen', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: { name: 'Property promotion', version: '1.0', protocol: 'http', config: { base_url: 'https://example.invalid' }, actions: [{ name: 'Read', type: 'request', method: 'GET', target: '/' }] } } })))
  const open = async () => {
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    await page.reload()
    await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
    const runtime = page.getByRole('region', { name: 'Runtime Settings', exact: true })
    await runtime.getByRole('navigation', { name: 'Runtime setting categories' }).getByRole('tab', { name: 'Error Handling', exact: true }).click()
  }
  await open()
  const fields = page.getByRole('group', { name: 'Response property analytics', exact: true })
  await fields.getByRole('button', { name: 'Promote extraction', exact: true }).click()
  await fields.getByLabel('Property name', { exact: true }).fill('operation')
  await fields.getByLabel('Extraction key', { exact: true }).fill('operation_type')
  await fields.getByLabel('Approved categories (comma separated)', { exact: true }).fill('checkout,search')
  await page.getByRole('combobox', { name: 'Exchange capture', exact: true }).selectOption('errors')
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await expect.poll(async () => ((await json(await request.get(`/api/v1/scripts/${id}`))).script as { config: { results?: { property_analytics?: unknown } } }).config.results?.property_analytics).toEqual([{ name: 'operation', extraction: 'operation_type', values: ['checkout', 'search'] }])
  await open()
  await expect(fields.getByLabel('Property name', { exact: true })).toHaveValue('operation')
  await expect(fields.getByLabel('Approved categories (comma separated)', { exact: true })).toHaveValue('checkout,search')
})
