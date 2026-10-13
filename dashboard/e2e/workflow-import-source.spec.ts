import { mkdtemp, writeFile, truncate, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test, expect, login, mutation, json } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('source catalog search and importer guidance stay consistent across formats', async ({ page }) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  // Refresh hierarchy cached before the authenticated API setup.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  const search = page.getByRole('searchbox', { name: 'Find a source format' })
  await search.fill('no-such-format')
  await expect(page.getByText(/No source formats match your search/)).toBeVisible()
  await page.getByRole('button', { name: 'Clear search' }).click()
  await expect(page.getByText('17 of 17 sources')).toBeVisible()

  for (const [query, title, sourceHeading] of [
    // The page is titled "Import <source>"; the importer's own heading names the source.
    ['OpenAPI', 'Import OpenAPI / Swagger', 'OpenAPI / Swagger'], ['Bruno', 'Import Bruno Collection', 'Bruno Collection'],
    ['Postman', 'Import Postman Collection', 'Postman Collection'], ['WSDL', 'Import WSDL / SOAP', 'WSDL / SOAP'],
    ['AsyncAPI', 'Import AsyncAPI / Kafka', 'AsyncAPI / Kafka'], ['RAML', 'Import RAML / Anypoint', 'RAML / Anypoint'],
    ['HAR Traffic', 'Import HAR Traffic', 'HAR Traffic'], ['Playwright', 'Import Recorder Export / Playwright Codegen', 'Recorder Export / Playwright Codegen'],
    ['Request / Response', 'Import Request / Response Sample', 'Request / Response Sample'], ['JMeter', 'Import JMeter Test Plan', 'JMeter Test Plan'],
    ['DevWeb', 'Import DevWeb Source', 'DevWeb Source'], ['k6', 'Import k6 Script', 'k6 Script'],
    ['CloudTest', 'Import Akamai CloudTest Export', 'Akamai CloudTest Export'], ['Event Hubs', 'Import Event Hubs Capture', 'Event Hubs Capture'],
    ['Broker Config', 'Import Broker Config', 'Broker Config'],
  ]) {
    await search.fill(query)
    await page.locator('.script-create-option').filter({ has: page.getByRole('heading', { name: sourceHeading, exact: true }) }).click()
    await expect(page.getByRole('heading', { level: 1, name: title, exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { level: 2, name: sourceHeading, exact: true })).toBeVisible()
    const guide = page.getByRole('list', { name: 'Import workflow', exact: true })
    await expect(guide).toBeVisible()
    await expect(guide.locator('[aria-current="step"]')).toContainText('Provide source')
    if (query === 'JMeter') {
      const action = page.getByRole('button', { name: 'Inspect & review' })
      const coverageButton = page.getByRole('button', { name: 'Import Coverage', exact: true })
      expect(await action.evaluate((button, trigger) => Boolean(trigger && (button.compareDocumentPosition(trigger) & Node.DOCUMENT_POSITION_FOLLOWING)), await coverageButton.elementHandle())).toBe(true)
      await coverageButton.click()
      await expect(page.getByRole('heading', { name: 'Import Coverage', exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Close', exact: true }).click()
    }
    if (query === 'Request / Response') await expect(page.getByRole('textbox', { name: 'Primary Sample', exact: true })).toBeVisible()
    if (query === 'HAR Traffic') await expect(page.getByRole('button', { name: 'Drop an optional comparison HAR file here' })).toBeVisible()
    await page.getByRole('button', { name: 'Back', exact: true }).click()
  }
})

test('import addresses name their importer, survive a reload and keep the old address', async ({ page }) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const scope = scopeQuery(workspace)
  const title = (name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  // The address written before import routes named their launcher still
  // opens the LoadRunner archive importer.
  await page.goto(`/#/scripts?mode=import&${scope}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(title('Import LoadRunner Script Archive')).toBeVisible()

  // An importer opened from the catalog is named in the address, so a reload
  // reopens it (it used to fall back to the LoadRunner archive importer).
  await page.goto(`/#/scripts?mode=create&${scope}`)
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.locator('.script-create-option').filter({ has: page.getByRole('heading', { name: 'HAR Traffic', exact: true }) }).click()
  await expect(title('Import HAR Traffic')).toBeVisible()
  await expect(page).toHaveURL(/mode=import&launcher=traffic_har&importMode=auto_detect/)
  await page.reload()
  await expect(title('Import HAR Traffic')).toBeVisible()

  // A named launcher opens directly; an unknown one opens the create options.
  await page.goto(`/#/scripts?mode=import&launcher=jmeter&${scope}`)
  await expect(title('Import JMeter Test Plan')).toBeVisible()
  await page.goto(`/#/scripts?mode=import&launcher=har-import&${scope}`)
  await expect(title('Scripts')).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Import', exact: true })).toBeVisible()
})

test('Oracle review clears obsolete findings and never offers a script destination', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  // Refresh hierarchy cached before the authenticated API setup.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('Oracle')
  await page.locator('.script-create-option').click()
  await expect(page.getByText('Review only: this page produces findings and next actions.', { exact: false })).toBeVisible()
  await expect(page.getByText('Save destination', { exact: false })).toHaveCount(0)
  const run = page.getByRole('button', { name: 'Run Review', exact: true })
  await expect(run).toBeDisabled()
  const source = page.getByRole('textbox', { name: 'Source Text', exact: true })
  await source.fill('GET /OA_HTML/AppsLogin Oracle E-Business web login')
  await run.click()
  await expect(page.getByRole('heading', { name: 'Review Pack', exact: true })).toBeVisible()
  const sourceDetails = page.locator('.import-conversion-details')
  await expect(sourceDetails).not.toHaveAttribute('open')
  await sourceDetails.locator('summary').click()
  await expect(sourceDetails.locator('dt', { hasText: 'bytes' }).locator('..').locator('dd')).toHaveText(String('GET /OA_HTML/AppsLogin Oracle E-Business web login'.length))
  await expect(page.locator('.sample-stat')).toHaveCount(0)
  await page.locator('.import-source-setup > summary').click()
  await page.getByRole('textbox', { name: 'Source Text', exact: true }).fill('Different source')
  await expect(page.getByRole('heading', { name: 'Review Pack', exact: true })).toHaveCount(0)
  await expect(run).toBeEnabled()
  await page.screenshot({ path: testInfo.outputPath('oracle-current-source.png'), fullPage: true })
})

test('Postman inspection sends project scope before collection bytes', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('Postman')
  await page.locator('.script-create-option').click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({
    name: 'orders.postman_collection.json', mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify({ info: { name: 'Orders' }, item: [{ name: 'Get orders', request: { method: 'GET', url: 'https://example.invalid/orders' } }] })),
  })
  const response = mutation(page, 'POST', '/api/v1/scripts/import/postman')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const imported = await response
  expect(imported.ok(), await imported.text()).toBeTruthy()
  expect(new URL(imported.url()).searchParams.get('project_id')).toBe(workspace.projectId)
  await expect(controls.getByRole('heading', { name: 'Review Generated Script', exact: true })).toBeVisible()
  await expect(controls.getByRole('tablist', { name: 'Import review sections' }).getByRole('tab')).toHaveCount(6)
})

test('HAR comparison can be removed and upload errors recover accessibly', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  // Refresh hierarchy cached before the authenticated API setup.
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const inputs = page.getByRole('group', { name: 'Script import controls' }).locator('input[type=file]')
  await inputs.nth(0).setInputFiles({ name: 'invalid.txt', mimeType: 'text/plain', buffer: Buffer.from('invalid') })
  await expect(page.locator('.sample-import-error[role=alert]')).toBeVisible()
  const har = { name: 'synthetic.har', mimeType: 'application/json', buffer: Buffer.from('{"log":{"version":"1.2","entries":[]}}') }
  await inputs.nth(0).setInputFiles(har)
  await expect(page.locator('.sample-import-error')).toHaveCount(0)
  await inputs.nth(1).setInputFiles({ ...har, name: 'comparison.har' })
  await page.getByRole('group', { name: 'Selected file comparison.har', exact: true }).getByRole('button', { name: 'Remove', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Drop an optional comparison HAR file here' })).toBeVisible()
  // Real validation must leave the library untouched, even when no entries can convert.
  const validation = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await page.getByText('Advanced validation only', { exact: true }).click()
  await page.getByRole('button', { name: 'Validate Only' }).click()
  await validation
  expect((await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))).scripts).toHaveLength(0)
})

test('JMeter review uses the shared six tabs and retains project scope during upload', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('JMeter')
  await page.locator('.script-create-option').click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').setInputFiles({
    name: 'review.jmx', mimeType: 'application/xml', buffer: Buffer.from(`<?xml version="1.0"?><jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree><TestPlan testname="Review" enabled="true"/><hashTree><ThreadGroup testname="Orders" enabled="true"><stringProp name="ThreadGroup.num_threads">2</stringProp></ThreadGroup><hashTree><HTTPSamplerProxy testname="Get orders" enabled="true"><stringProp name="HTTPSampler.domain">example.invalid</stringProp><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.path">/orders</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`),
  })
  const response = mutation(page, 'POST', '/api/v1/scripts/import/jmeter')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const imported = await response
  expect(imported.ok(), await imported.text()).toBeTruthy()
  const tabs = controls.getByRole('tablist', { name: 'Import review sections' })
  const labels = ['Review', 'Credentials', 'Data & parameters', 'Runtime settings', 'DSL Preview', 'Source Mapping']
  await expect(tabs.getByRole('tab')).toHaveCount(6)
  for (const label of labels) {
    const tab = tabs.getByRole('tab', { name: label, exact: true })
    await tab.click()
    await expect(tab).toHaveAttribute('aria-selected', 'true')
    await expect(controls.getByRole('tabpanel', { name: label, exact: true })).toBeVisible()
  }
  await tabs.getByRole('tab', { name: 'Runtime settings', exact: true }).click()
  await controls.getByRole('checkbox', { name: /Show \d+ unconfigured APEX settings/ }).check()
  const runtimeCategories = controls.getByRole('navigation', { name: 'Runtime categories' })
  await expect(runtimeCategories).toBeVisible()
  expect(await runtimeCategories.evaluate(nav => getComputedStyle(nav).flexDirection)).toBe('column')
  const runtime = controls.getByRole('region', { name: 'Additional APEX settings', exact: true })
  const timeoutRow = runtime.getByRole('row', { name: /Request timeout \(ms\)/ })
  await expect(timeoutRow).toContainText('Limits how long a request can wait before timing out.')
  await expect(timeoutRow.locator('.import-runtime-setting-path')).toContainText('http.request_timeout_ms')
  await expect(timeoutRow.locator('details')).toHaveCount(0)
  const keepAliveRow = runtime.getByRole('row', { name: /Keep connections alive/ })
  await expect(keepAliveRow).toContainText('Controls connection reuse')
  await timeoutRow.scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('jmeter-runtime-review-wide.png') })
  await page.setViewportSize({ width: 760, height: 900 })
  await runtimeCategories.scrollIntoViewIfNeeded()
  const runtimeTableFits = await timeoutRow.locator('xpath=ancestor::table').evaluate(table => {
    const wrapper = table.closest('.lr-table-wrap')!
    const lastHeading = table.querySelector('thead th:last-child')!
    return lastHeading.getBoundingClientRect().right <= wrapper.getBoundingClientRect().right + 1
  })
  expect(runtimeTableFits).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('jmeter-runtime-review-narrow.png') })
  await page.setViewportSize({ width: 1280, height: 900 })
  await controls.getByRole('button', { name: 'Edit HTTP', exact: true }).click()
  await controls.getByRole('spinbutton', { name: 'HTTP: Request timeout (ms)', exact: true }).fill('6500')
  const settingsApply = mutation(page, 'POST', '/api/v1/scripts/import/settings')
  await controls.getByRole('button', { name: 'Apply HTTP', exact: true }).click()
  expect((await settingsApply).ok()).toBeTruthy()
  await expect(controls.getByRole('region', { name: 'Additional APEX settings', exact: true })).toContainText('6500')
  await expect(controls.getByRole('tabpanel')).toContainText('Orders')
  await tabs.getByRole('tab', { name: /^Data & parameters(,|$)/ }).click()
  const dataViews = controls.getByRole('tablist', { name: 'Data views' })
  await expect(dataViews).toBeVisible()
  expect(await dataViews.evaluate(nav => getComputedStyle(nav).flexDirection)).toBe('column')
  await page.setViewportSize({ width: 760, height: 900 })
  await dataViews.scrollIntoViewIfNeeded()
  expect(await dataViews.evaluate(nav => getComputedStyle(nav).flexDirection)).toBe('column')
  await page.screenshot({ path: testInfo.outputPath('jmeter-data-review-narrow.png') })
  await page.setViewportSize({ width: 1280, height: 900 })
  await tabs.getByRole('tab', { name: 'DSL Preview', exact: true }).click()
  await expect(controls.getByRole('tabpanel')).toContainText('Get orders')
  await tabs.getByRole('tab', { name: /^Review(,|$)/ }).click()
  await expect(controls.locator('.import-source-setup')).not.toHaveAttribute('open')
  const reference = controls.locator('.import-conversion-details')
  await expect(reference).not.toHaveAttribute('open')
  await reference.locator('summary').click()
  await expect(reference.locator('dt', { hasText: 'Converted Samplers' }).locator('..').locator('dd')).toHaveText('1')
  await expect(controls.locator('.lr-stat')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('jmeter-six-tab-review.png'), fullPage: true })
  await page.setViewportSize({ width: 760, height: 900 })
  await expect(tabs.getByRole('tab', { name: 'Source Mapping', exact: true })).toBeVisible()
  expect(await reference.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('jmeter-narrow-review.png'), fullPage: true })
})


test('HAR picker accepts captures above 50 MiB and rejects files above 100 MiB', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  const inputs = controls.locator('input[type=file]')
  const directory = await mkdtemp(join(tmpdir(), 'apex-har-picker-'))
  try {
    const capture = join(directory, 'large-synthetic.har')
    const oversized = join(directory, 'oversized-synthetic.har')
    const bytes = Buffer.alloc(51 * 1024 * 1024, 0x20)
    bytes.write('{"log":{"entries":[]}}')
    await writeFile(capture, bytes)
    await writeFile(oversized, '{}')
    await truncate(oversized, 100 * 1024 * 1024 + 1)
    await inputs.nth(0).setInputFiles(capture)
    await expect(page.getByRole('group', { name: 'Selected file large-synthetic.har', exact: true })).toBeVisible()
    await expect(controls.getByRole('button', { name: 'Inspect & review', exact: true })).toBeEnabled()
    await expect(page.locator('.sample-import-error')).toHaveCount(0)
    await inputs.nth(1).setInputFiles(capture)
    await expect(page.getByRole('group', { name: 'Selected file large-synthetic.har', exact: true })).toHaveCount(2)
    await inputs.nth(1).setInputFiles(oversized)
    await expect(page.locator('.sample-import-error[role=alert]')).toContainText('imports are limited to 100.0 MB')
    await inputs.nth(0).setInputFiles(oversized)
    await expect(page.locator('.sample-import-error[role=alert]')).toContainText('imports are limited to 100.0 MB')
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})
