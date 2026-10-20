import { readFile } from "node:fs/promises"
import type { Locator } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Project authorization and navigation use the real controller. Bounded
// transport fixtures exercise the complete UI; native normalization and the
// independent zero-traffic counters are covered by Go integration tests.
test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

// Playwright follows an option's enclosing label to its select when checking
// enabled/disabled state. Check the native option itself. Its programmatic
// selectOption can bypass disabled choices, so exercise malformed-draft
// admission separately from the native disabled contract.
async function assertProtectedColumnsUnavailable(column: Locator, area: Locator) {
  await expect(column).toBeEnabled()
  for (const name of ['credentials.password (protected)', 'api.key (protected)']) {
    await expect(column.getByRole('option', { name, exact: true })).toHaveJSProperty('disabled', true)
  }
  const previous = await column.inputValue()
  const validate = area.getByRole('button', { name: 'Validate and import', exact: true })
  const save = area.getByRole('button', { name: 'Save mapping', exact: true })
  await expect(validate).toBeEnabled(); await expect(save).toBeEnabled()
  for (const protectedValue of ['["credentials","password"]', '["api","key"]']) {
    await column.selectOption(protectedValue)
    await expect(column).toHaveValue(protectedValue)
    const disclosure = area.getByRole('alert').filter({ hasText: 'Protected credential columns cannot become analytical timestamps, measurement values, metric names or identity labels.' })
    await expect(disclosure).toHaveCount(1); await expect(disclosure).toBeVisible()
    await expect(validate).toBeDisabled(); await expect(save).toBeDisabled()
    // Native disabled-button clicks cannot submit the retained invalid draft.
    await validate.evaluate(button => (button as HTMLButtonElement).click())
    await save.evaluate(button => (button as HTMLButtonElement).click())
    await column.selectOption(previous)
    await expect(column).toHaveValue(previous)
    await expect(area.getByRole('alert')).toHaveCount(0)
    await expect(validate).toBeEnabled(); await expect(save).toBeEnabled()
  }
}

// Capture a bounded editor inside its actual scroll owner. A screenshot of a
// whole comparison taller than the viewport contains clipped black areas and
// sticky-toolbar overlap; center hit-testing proves the actual controls remain
// exposed before any visual evidence is saved.
async function captureIdentityFilters(filters: Locator, path: string) {
  await filters.evaluate(element => {
    const container = element.closest('.main-content')
    if (!(container instanceof HTMLElement)) throw new Error('Identity editor has no owned scroll container')
    const toolbar = container.querySelector(':scope > .main-topbar')
    const top = Math.max(container.getBoundingClientRect().top, toolbar?.getBoundingClientRect().bottom ?? 0) + 16
    container.scrollTop += element.getBoundingClientRect().top - top
  })
  try {
    await expect.poll(() => filters.evaluate(element => {
      const box = element.getBoundingClientRect()
      const container = element.closest('.main-content')
      if (!(container instanceof HTMLElement)) return false
      const toolbar = container.querySelector(':scope > .main-topbar')
      const top = Math.max(container.getBoundingClientRect().top, toolbar?.getBoundingClientRect().bottom ?? 0)
      const bottom = Math.min(window.innerHeight, container.getBoundingClientRect().bottom)
      const card = element.closest('.monitor-card')
      if (!(card instanceof HTMLElement) || card.scrollWidth > card.clientWidth + 1) return false
      const descriptions = Array.from(card.querySelectorAll('.exportable-chart > .sr-only'))
      if (descriptions.length === 0 || descriptions.some(description => {
        const style = getComputedStyle(description)
        const bounds = description.getBoundingClientRect()
        return style.position !== 'absolute' || style.clip !== 'rect(0px, 0px, 0px, 0px)' || bounds.width > 1.01 || bounds.height > 1.01 || description.getAttribute('aria-hidden') === 'true'
      })) return false
      return box.top >= top && box.bottom <= bottom && box.left >= 0 && box.right <= window.innerWidth && element.scrollWidth <= element.clientWidth + 1 && Array.from(element.querySelectorAll('input, button')).every(control => {
        const bounds = control.getBoundingClientRect()
        const x = (bounds.left + bounds.right) / 2; const y = (bounds.top + bounds.bottom) / 2
        const hit = document.elementFromPoint(x, y)
        return bounds.width > 0 && bounds.height > 0 && bounds.left >= box.left && bounds.right <= box.right && hit !== null && (control === hit || control.contains(hit))
      })
    })).toBe(true)
  } catch (error) {
    const geometry = await filters.evaluate(element => {
      const bounds = (node: Element) => {
        const box = node.getBoundingClientRect(); const style = getComputedStyle(node)
        return { tag: node.tagName, class: node.className, left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, scrollLeft: node.scrollLeft, minWidth: style.minWidth, gridTemplateColumns: style.gridTemplateColumns, display: style.display }
      }
      const ancestors = []; let parent: Element | null = element
      while (parent) { ancestors.push(bounds(parent)); parent = parent.parentElement }
      return { viewport: { width: innerWidth, height: innerHeight }, ancestors, controls: Array.from(element.querySelectorAll('input, button')).map(bounds) }
    })
    await test.info().attach('identity-filter-geometry', { body: JSON.stringify(geometry, null, 2), contentType: 'application/json' })
    throw error
  }
  const add = filters.getByRole('button', { name: 'Add dimension filter', exact: true })
  const buttonBounds = await add.boundingBox()
  const inputBounds = await filters.getByLabel('Filter 1 value', { exact: true }).boundingBox()
  expect(buttonBounds).not.toBeNull(); expect(inputBounds).not.toBeNull()
  expect(buttonBounds!.height).toBeLessThanOrEqual(inputBounds!.height + 8)
  await filters.screenshot({ path })
}


test('Parquet upload, mapping, query and deletion remain in project scope', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const id = '00000000-0000-0000-0000-000000000001'
  let state = 'uploading'; let revision = 1; let uploaded = false; let deleted = false
  let mappingRequests = 0; let savedMappingRequests = 0; let commitRequests = 0
  let mapping: unknown
  const record = () => ({ id, project_id: workspace.projectId, name: 'External latency', revision, state, files: [{ id: 'source', relative_path: 'sample.parquet', size: 16, uploaded: uploaded ? 16 : 0 }], accepted: state === 'ready' ? 3 : 0, rejected: 0, mapping })
  const point = (value: number, second: number) => ({ timestamp_utc: `2026-01-01T00:00:0${second}Z`, metric_name: 'latency', unit: 'ms', value, quality: 'valid' })
  await page.route('**/api/v1/analysis/**', async route => {
    const req = route.request(); const url = new URL(req.url()); const method = req.method()
    const send = (value: unknown, status = 200) => route.fulfill({ status, json: value })
    if (url.pathname.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 2, chunk_bytes: 4 * 1024 * 1024 })
    if (url.pathname.endsWith('/mappings') || url.pathname.endsWith('/comparisons')) { if (url.pathname.endsWith('/mappings') && method === 'POST') savedMappingRequests++; return send([]) }
    if (url.pathname.endsWith('/imports')) {
      expect(url.searchParams.get('project_id')).toBe(workspace.projectId)
      if (method === 'POST') { expect(req.postDataJSON().files[0].relative_path).toBe('sample.parquet'); return send(record(), 201) }
      return send(deleted ? [] : [record()])
    }
    if (url.pathname.endsWith('/files/source') && method === 'PUT') { expect(req.postDataBuffer()?.length).toBe(16); uploaded = true; revision++; return send(record()) }
    if (url.pathname.endsWith('/preview')) return send({ schema: { rows: 3, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64' }, { path: 'latency', path_components: ['latency'], physical_type: 'DOUBLE' }, { path: 'labels.source', path_components: ['labels', 'source'], physical_type: 'BYTE_ARRAY' }, { path: 'host', path_components: ['host'], physical_type: 'BYTE_ARRAY' }, { path: 'service', path_components: ['service'], physical_type: 'BYTE_ARRAY' }, { path: 'group', path_components: ['group'], physical_type: 'BYTE_ARRAY' }, { path: 'credentials.password', path_components: ['credentials', 'password'], physical_type: 'DOUBLE' }, { path: 'api.key', path_components: ['api', 'key'], physical_type: 'BYTE_ARRAY' }] }, samples: [{ ts: 1767225600000, latency: 20 }] })
    if (url.pathname.endsWith('/mapping')) { mappingRequests++; mapping = req.postDataJSON().mapping; expect(mapping).toMatchObject({ version: 2, identity: { source: ['labels', 'source'], host: ['host'], service: ['service'], dimensions: [{ name: 'a/b~c', value: ['group'] }] } }); expect(req.postDataJSON().revision).toBe(revision); state = 'mapped'; revision++; return send(record()) }
    if (url.pathname.endsWith('/commit')) { commitRequests++; state = 'ready'; revision++; return send(record(), 202) }
    if (url.pathname.endsWith('/series')) { expect(req.postDataJSON()).toMatchObject({ source_id: 'agent', host: 'host-a', service: 'api', dimensions: { 'a/b~c': '' } }); expect(req.postDataJSON().from).toBe('2026-01-01T00:00:00.000Z'); return send({ points: [point(20, 0), point(40, 1)] }) }
    if (method === 'DELETE') { deleted = true; return route.fulfill({ status: 204 }) }
    return send(record())
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Analysis workspace' })
  await expect(area).toBeVisible()
  await area.getByRole('button', { name: 'New import', exact: true }).click()
  await area.getByLabel('Dataset name', { exact: true }).fill('External latency')
  await area.getByLabel('Files', { exact: true }).setInputFiles({ name: 'sample.parquet', mimeType: 'application/octet-stream', buffer: Buffer.alloc(16) })
  await area.getByRole('button', { name: 'Upload files', exact: true }).click()
  await expect(area).toContainText('Upload complete')
  await area.getByLabel('Timestamp format').selectOption('unix_ms')
  await area.getByRole('checkbox', { name: 'latency', exact: true }).check()
  await area.getByLabel('Semantics').selectOption('observation')
  await area.getByRole('combobox', { name: 'Unit', exact: true }).selectOption('ms')
  await area.getByLabel('Mapping name', { exact: true }).fill('Private-selection admission probe')
  for (const column of [area.getByRole('combobox', { name: 'Timestamp column', exact: true }), area.getByRole('combobox', { name: 'Metric name column (long table)', exact: true })]) {
    await assertProtectedColumnsUnavailable(column, area)
  }
  await expect(area.getByRole('checkbox', { name: 'credentials.password (protected)', exact: true })).toBeDisabled()
  await expect(area.getByRole('checkbox', { name: 'api.key (protected)', exact: true })).toBeDisabled()
  const identityMapping = area.getByRole('group', { name: 'Imported measurement identities', exact: true })
  for (const label of ['Source column', 'Host column', 'Service column']) {
    const column = identityMapping.getByRole('combobox', { name: label, exact: true })
    await assertProtectedColumnsUnavailable(column, area)
  }
  expect(mappingRequests).toBe(0); expect(savedMappingRequests).toBe(0); expect(commitRequests).toBe(0)
  await area.getByLabel('Mapping name', { exact: true }).fill('')
  await identityMapping.getByRole('combobox', { name: 'Source column', exact: true }).selectOption('["labels","source"]')
  await identityMapping.getByRole('combobox', { name: 'Host column', exact: true }).selectOption('["host"]')
  await identityMapping.getByRole('combobox', { name: 'Service column', exact: true }).selectOption('["service"]')
  await identityMapping.getByRole('button', { name: 'Add dimension', exact: true }).click()
  await expect(area.getByRole('button', { name: 'Validate and import', exact: true })).toBeDisabled()
  await identityMapping.getByLabel('Dimension 1 name', { exact: true }).fill('a/b~c')
  await identityMapping.getByRole('combobox', { name: 'Dimension 1 column', exact: true }).selectOption('["group"]')
  await area.getByRole('button', { name: 'Validate and import', exact: true }).click()
  await expect(area).toContainText('State: ready')
  expect(mappingRequests).toBe(1); expect(savedMappingRequests).toBe(0); expect(commitRequests).toBe(1)
  const query = area.getByRole('group', { name: 'Query dataset', exact: true })
  await query.getByLabel('Metric', { exact: true }).fill('latency')
  await query.getByLabel('From (UTC)', { exact: true }).fill('2026-01-01T00:00')
  await query.getByLabel('To (UTC)', { exact: true }).fill('2026-01-01T00:01')
  const identityFilters = query.getByRole('group', { name: 'Dataset identity filters', exact: true })
  await identityFilters.getByLabel('Source ID', { exact: true }).fill('agent')
  await identityFilters.getByLabel('Host', { exact: true }).fill('host-a')
  await identityFilters.getByLabel('Service', { exact: true }).fill('api')
  await identityFilters.getByRole('button', { name: 'Add dimension filter', exact: true }).click()
  await expect(query.getByRole('button', { name: 'Show series', exact: true })).toBeDisabled()
  await identityFilters.getByLabel('Filter 1 name', { exact: true }).fill('a/b~c')
  await query.getByRole('button', { name: 'Show series', exact: true }).click()
  await expect(area.getByRole('img', { name: 'latency over UTC time; 2 buckets' })).toBeVisible()
  await page.setViewportSize({ width: 760, height: 1000 })
  await expect(area.getByRole('heading', { name: 'Datasets and imports', exact: true })).toBeVisible()
  await captureIdentityFilters(identityFilters, test.info().outputPath('parquet-query.png'))
  const chart = area.getByRole('img', { name: 'latency over UTC time; 2 buckets' })
  await expect.poll(async () => chart.evaluate(element => {
    const bounds = element.getBoundingClientRect()
    const ticks = Array.from(element.querySelectorAll('.recharts-xAxis-tick-labels .recharts-cartesian-axis-tick-value'))
    return ticks.length > 0 && ticks.every(tick => {
      const box = tick.getBoundingClientRect()
      return box.left >= bounds.left && box.right <= bounds.right
    })
  })).toBe(true)
  await expect(chart).toContainText('01-01 00:00:01')
  await expect(chart).toContainText('ms')
  await query.getByLabel('Metric', { exact: true }).fill('different metric')
  await expect(chart).toBeVisible()
  const csvDownload = page.waitForEvent('download')
  await area.getByRole('button', { name: 'Export latency dataset series data as CSV', exact: true }).click()
  const csv = await readFile((await (await csvDownload).path())!, 'utf8')
  expect(csv).toContain('latency (ms) [value]')
  expect(csv).toContain(id)
  expect(csv).toContain('agent')
  expect(csv).toContain('a/b~c')
  expect(csv).toContain('"20"')
  expect(csv).not.toContain('different metric')
  const svgDownload = page.waitForEvent('download')
  await area.getByRole('button', { name: 'Export latency dataset series image as SVG', exact: true }).click()
  const svg = await readFile((await (await svgDownload).path())!, 'utf8')
  expect(svg).toContain('<svg')
  expect(svg).toContain('latency dataset series')
  expect(svg).not.toContain('var(')
  await chart.screenshot({ path: test.info().outputPath('parquet-series.png') })
  await area.getByRole('button', { name: 'Delete dataset', exact: true }).click()
  await expect(area).not.toContainText('State: ready')
})

test('preparation readiness can renew and Start sends its immutable revision', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request; const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Prepared run controls', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  let state = 'none'; let revision = 1; let started = false
  const preparation = () => ({ id: 'prepared', state, revision, execution_revision: 'immutable-execution-revision', expires_at: '2026-01-01T00:30:00Z' })
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { preparation_enabled: true } }))
  await page.route(`**/api/v1/tests/${id}/preparation`, async route => {
    if (route.request().method() === 'POST') { const body = route.request().postDataJSON(); if (body.preparation_id) expect(body.revision).toBe(revision); else expect(body.capacity).toEqual({ selection: 'ephemeral', spot: 'required', spot_ceiling_percent: 50, on_demand_floor: 1 }); state = 'ready'; revision++ }
    await route.fulfill({ json: preparation() })
  })
  await page.route(`**/api/v1/tests/${id}/start`, async route => {
    expect(route.request().postDataJSON()).toMatchObject({ preparation_id: 'prepared', expected_preparation_revision: 'immutable-execution-revision' }); started = true; state = 'consumed'
    await route.fulfill({ json: { status: 'INITIALIZING' } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Run preparation', exact: true })
  await expect(area).toBeVisible()
  await test.info().attach('preparation-controls', { body: await area.evaluate(node => node.outerHTML), contentType: 'text/html' })
  await area.getByLabel('Capacity', { exact: true }).selectOption('ephemeral')
  await area.getByLabel('Spot policy', { exact: true }).selectOption('required')
  await area.getByLabel('On-demand generator floor', { exact: true }).fill('1')
  await area.getByRole('button', { name: 'Prepare', exact: true }).click()
  await expect(area).toContainText('ready')
  await area.getByRole('button', { name: 'Renew readiness', exact: true }).click()
  await area.getByRole('button', { name: 'Start prepared run', exact: true }).click()
  await expect(area).toContainText('consumed'); expect(started).toBe(true)
})


test('dependency evidence selects the same UTC interval for the monitor overlay', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request; const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Dependency evidence', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, diagnostics_enabled: true } }))
  await page.route(`**/api/v1/tests/${id}/monitors`, route => route.fulfill({ json: [{ id: 'job', source: { id: 'jvm', name: 'JVM' }, state: 'collecting', next_sequence: 1 }] }))
  await page.route(`**/api/v1/tests/${id}/dependency-evidence*`, route => route.fulfill({ json: { timestamp: '2026-01-01T00:00:10Z', interval_seconds: 2, coverage: 'observed', graph: { nodes: [{ id: 'orders', kind: 'dependency', label: 'orders', dependency_type: 'grpc', p95_ms: 25 }] }, monitors: [{ source_id: 'jvm', name: 'JVM', dependency: 'orders', state: 'collecting', coverage: 'observed', metrics: ['jvm.heap.used'] }] } }))
  let inspected = false
  await page.route(`**/api/v1/tests/${id}/monitor-series?*`, route => {
    const query = new URL(route.request().url()).searchParams
    expect(query.get('from')).toBe('2026-01-01T00:00:08.000Z'); expect(query.get('to')).toBe('2026-01-01T00:00:10Z')
    expect(query.get('source_id')).toBe('jvm'); expect(query.get('metric')).toBe('jvm.heap.used'); inspected = true
    return route.fulfill({ json: { points: [{ timestamp_utc: '2026-01-01T00:00:09Z', unit: 'bytes', kind: 'gauge', value: 1024, quality: 'valid' }], has_more: false } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Run monitor evidence', exact: true })
  await expect(area).toContainText('orders (grpc)')
  await area.getByRole('button', { name: 'Inspect jvm.heap.used', exact: true }).click()
  await expect(area).toContainText('Selected interval:')
  await area.getByRole('button', { name: 'Refresh monitor overlay', exact: true }).click()
  await expect(area).toContainText('1 valid / 1 observed samples'); expect(inspected).toBe(true)
})

test('native project panel saves and reopens through the real dashboard API', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const capabilities = await json(await request.get(`/api/v1/analysis/capabilities?project_id=${workspace.projectId}`))
  expect(capabilities.metric_catalog).toMatchObject({
    version: 'apexload.metric_catalog.v1',
    metrics: expect.arrayContaining([expect.objectContaining({ name: 'response_time', unit: 'ms', native_wire_unit: 'ms', aggregations: ['avg', 'min', 'max', 'p50', 'p90', 'p95', 'p99'] })]),
  })
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Native panel run', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Panel title', { exact: true }).fill('Checkout latency')
  await page.getByLabel('Metric', { exact: true }).selectOption('response_time')
  await page.getByLabel('Measurement', { exact: true }).selectOption('p95')
  await page.getByLabel('Visualization', { exact: true }).selectOption('stat')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  const panels = page.getByRole('region', { name: 'Project analytics panels', exact: true })
  await expect(panels.getByRole('heading', { name: 'Checkout latency', exact: true })).toBeVisible()
  await page.reload()
  await expect(panels.getByRole('heading', { name: 'Checkout latency', exact: true })).toBeVisible()
  const saved = await json(await request.get(`/api/v1/monitoring/dashboard-definitions?project_id=${workspace.projectId}`))
  expect((saved.dashboards as { tabs: { panels: { query: unknown }[] }[] }[])[0].tabs[0].panels[0].query).toMatchObject({ metric: 'response_time', aggregation: 'p95', max_series: 1 })
  await expect(panels).toContainText('Response time (ms)')
  await panels.screenshot({ path: test.info().outputPath('native-project-panel.png') })
})

test('filtered archive panels save, reopen, compare, and inspect an exact time bucket', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Filtered archive panel', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  const buckets = [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 3, value: 30 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 0, value: null }]
  await page.route(`**/api/v1/tests/${id}/archive/native-series`, route => {
    expect(route.request().postDataJSON()).toMatchObject({ population: 'action', metric: 'response_time', aggregation: 'p95', filters: { action: 'Checkout' }, end_seconds: 20, bucket_seconds: 10, comparison_run_id: id })
    const series = { run_id: id, buckets, warnings: [], identities: ['Checkout'] }
    return route.fulfill({ json: { current: series, baseline: series, comparable: false, warnings: ['Workload/version compatibility is not established'], added: [], removed: [] } })
  })
  let inspected = false
  await page.route(`**/api/v1/tests/${id}/archive/transactions?*`, route => {
    const params = new URL(route.request().url()).searchParams
    expect(params.get('population')).toBe('action'); expect(params.get('action_exact')).toBe('Checkout')
    expect(params.get('time_to')).toBe(buckets[0].to); expect(params.get('time_to_exclusive')).toBe('true'); inspected = true
    return route.fulfill({ json: { total: 3, transactions: [{ timestamp: buckets[0].from, action_name: 'Checkout', duration_ms: 30, success: true, snapshot: { response_body: '{"kind":"paid"}' } }] } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Panel title', { exact: true }).fill('Filtered checkout')
  await page.getByLabel('Evidence population', { exact: true }).selectOption('action')
  await page.getByLabel('Exact action or transaction name', { exact: true }).fill('Checkout')
  await page.getByLabel('End elapsed seconds', { exact: true }).fill('20')
  await page.getByLabel('Comparison run ID (optional)', { exact: true }).fill(id)
  await page.getByLabel('Measurement', { exact: true }).selectOption('p95')
  await page.getByLabel('Visualization', { exact: true }).selectOption('table')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  const panels = page.getByRole('region', { name: 'Project analytics panels', exact: true })
  await expect(panels).toContainText('Workload/version compatibility is not established')
  await page.reload()
  await expect(panels).toContainText('Filtered checkout')
  await panels.getByText('Inspect time buckets (2)', { exact: true }).click()
  await expect(panels).toContainText('Missing')
  await panels.getByRole('button', { name: 'Inspect current bucket 0s (3)', exact: true }).click()
  const inspector = panels.getByRole('region', { name: 'Filtered transaction inspector' })
  await expect(inspector).toContainText('3 matching observations')
  await inspector.getByText('Checkout · 30.0 ms · Success', { exact: true }).click()
  await expect(inspector).toContainText('paid'); expect(inspected).toBe(true)
  await inspector.getByRole('button', { name: 'Close inspector', exact: true }).click()
  await page.setViewportSize({ width: 1280, height: 1400 })
  await panels.evaluate(node => node.scrollIntoView({ block: 'start' }))
  await panels.screenshot({ path: test.info().outputPath('filtered-native-panel.png') })
})

test('grouped archive overlays and filtered live selections save and reopen', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Grouped evidence', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  const start = '2026-10-01T00:00:00Z'
  await page.route(`**/api/v1/tests/${id}/native-series`, async route => {
    const q = route.request().postDataJSON()
    expect(q.dimensions).toEqual(['action']); expect(q.max_series).toBe(10)
    if (q.evidence_mode === 'live') { expect(q.live_bucket_mode).toBe('merged'); expect(q.filters).toMatchObject({ hierarchy: 'cart', phase: 'actions', network_profile: 'slow' }) }
    if (q.evidence_mode === 'archive') expect(q.filters).toMatchObject({ phase: 'action' })
    if (q.evidence_mode === 'archive') expect(q.overlay_selection).toMatchObject({ dataset_id: 'external-latency', metric: 'latency', aggregation: 'avg' })
    return route.fulfill({ json: { mode: q.evidence_mode, live_bucket_mode: q.live_bucket_mode, start, warnings: ['Original units retained; gaps excluded from correlation.'], series: [{ name: 'Checkout', unit: 'ms', points: [{ from: start, to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 3, value: 30 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 2, value: 40 }, { from: '2026-10-01T00:00:20Z', to: '2026-10-01T00:00:30Z', offset_seconds: 20, count: 0, value: null }] }], ...(q.evidence_mode === 'archive' ? { overlay: [{ timestamp_utc: start, unit: 'ms', quality: 'valid', value: 22 }, { timestamp_utc: '2026-10-01T00:00:10Z', unit: 'ms', quality: 'valid', value: 28 }], correlation: { Checkout: [{ lag_seconds: 0, pairs: 1, coverage: 0.5 }] } } : {}) } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/transactions?*`, route => {
    const url = new URL(route.request().url())
    expect(url.searchParams.get('phase')).toBe('action'); expect(url.searchParams.get('action_exact')).toBe('Checkout'); expect(url.searchParams.get('time_to_exclusive')).toBe('true')
    return route.fulfill({ json: { transactions: [{ timestamp: start, test_id: id, test_name: 'Grouped evidence', action_name: 'Checkout', duration_ms: 30, success: true, dns_ms: 2, ttfb_ms: 20 }], total: 1 } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Evidence workflow', { exact: true }).selectOption('archive')
  await page.getByLabel('phase filter', { exact: true }).fill('action')
  await page.getByLabel('Panel title', { exact: true }).fill('Grouped archive overlay')
  await page.getByLabel('Overlay source', { exact: true }).selectOption('dataset')
  await page.getByLabel('Source or dataset ID', { exact: true }).fill('external-latency')
  await page.getByLabel('Overlay metric', { exact: true }).fill('latency')
  await page.getByLabel('Visualization', { exact: true }).selectOption('line')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  const panels = page.getByRole('region', { name: 'Project analytics panels', exact: true })
  await expect(panels).toContainText('Overlay (ms)'); await panels.getByText('Evidence table (3 timestamps)', { exact: true }).click(); await expect(panels).toContainText('Missing')
  await page.reload(); await expect(panels).toContainText('Grouped archive overlay'); await panels.getByText('Evidence table (3 timestamps)', { exact: true }).click()
  await panels.getByText('Lag correlation and coverage', { exact: true }).click()
  await expect(panels).toContainText('Unscored'); await expect(panels).toContainText('50.0%')
  await panels.getByText('Inspect grouped archive buckets', { exact: true }).click()
  await panels.getByRole('button', { name: 'Inspect Checkout bucket 0s (3)', exact: true }).click()
  const inspector = panels.getByRole('region', { name: 'Filtered transaction inspector' })
  await expect(inspector).toContainText('1 matching observations')
  await inspector.getByText('Checkout · 30.0 ms · Success', { exact: true }).click()
  await expect(inspector).toContainText('DNS: 2.0 ms'); await expect(inspector).toContainText('Connect: —')
  await inspector.screenshot({ path: test.info().outputPath('dimension-waterfall.png') })
  await inspector.getByRole('button', { name: 'Close inspector' }).click()
  await page.setViewportSize({ width: 1280, height: 1400 }); await panels.evaluate(node => node.scrollIntoView({ block: 'start' }))
  await panels.screenshot({ path: test.info().outputPath('grouped-overlay-panel.png') })
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Evidence workflow', { exact: true }).selectOption('live')
  await page.getByLabel('Merge live intervals into buckets', { exact: true }).check()
  await page.getByLabel('hierarchy filter', { exact: true }).fill('cart')
  await page.getByLabel('phase filter', { exact: true }).fill('actions')
  await page.getByLabel('network_profile filter', { exact: true }).fill('slow')
  await page.getByLabel('Panel title', { exact: true }).fill('Live actions')
  await page.getByLabel('Visualization', { exact: true }).selectOption('table')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  await panels.getByLabel('Project dashboard', { exact: true }).selectOption({ label: 'Live actions' })
  await expect(panels).toContainText('Live merged publication buckets')
  await page.reload(); await panels.getByLabel('Project dashboard', { exact: true }).selectOption({ label: 'Live actions' })
  await expect(panels).toContainText('Checkout (ms)')
})

test('timestamped run annotations save, reopen and retain delete history', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Annotated run', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(async () => (await json(await request.get(`/api/v1/tests/${id}`))).status, { timeout: 15000 }).toMatch(/COMPLETE|FAILED/)
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  await page.getByText('Run timeline annotations', { exact: true }).click()
  await page.getByLabel('Annotation label', { exact: true }).fill('Deployment')
  await page.getByLabel('Annotation context', { exact: true }).fill('Version 2 at execution commit')
  await page.getByRole('button', { name: 'Add timeline annotation', exact: true }).click()
  await expect(page.getByText('Deployment', { exact: true })).toBeVisible()
  await page.reload(); await page.getByText('Run timeline annotations', { exact: true }).click()
  await expect(page.getByText('Version 2 at execution commit', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Delete annotation Deployment', exact: true }).click()
  await page.getByRole('alertdialog', { name: 'Delete annotation' }).getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.getByText('Deployment', { exact: true })).toHaveCount(0)
  const stored = await json(await request.get(`/api/v1/tests/${id}/analysis/annotations`))
  const annotations = stored.annotations as Array<{ label: string; text: string; deleted_at?: string }>
  expect(stored.revision).toBe(2); expect(annotations[0]).toMatchObject({ label: 'Deployment', text: 'Version 2 at execution commit' }); expect(annotations[0].deleted_at).toBeTruthy()
})


test('retained observation rates save and reopen with elapsed-time units', async ({ page, trackRun }) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request = page.context().request
 const workspace = await createWorkspace(request)
 const scriptId = await seedScript(request, workspace)
 const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Retained rates', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
 trackRun(id)
 await page.route(`**/api/v1/tests/${id}/archive/native-series`, route => {
 expect(route.request().postDataJSON()).toMatchObject({ population: 'action', metric: 'success_observation_rate', aggregation: 'rate' })
 return route.fulfill({ json: { current: { run_id: id, buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 3, value: .2 }], warnings: [], identities: ['Checkout'] }, comparable: false, warnings: [], added: [], removed: [] } })
 })
 await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
 await page.getByText('Create project analytics panel', { exact: true }).click()
 await page.getByLabel('Panel title', { exact: true }).fill('Successful retained rate')
 await page.getByLabel('Evidence population', { exact: true }).selectOption('action')
 await page.getByLabel('Metric', { exact: true }).selectOption('success_observation_rate')
 await expect(page.getByLabel('Measurement', { exact: true })).toHaveValue('rate')
 await page.getByLabel('Visualization', { exact: true }).selectOption('table')
 await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
 const panels = page.getByRole('region', { name: 'Project analytics panels', exact: true })
 await expect(panels).toContainText('observations/s')
 await page.reload(); await expect(panels).toContainText('Successful retained rate'); await expect(panels).toContainText('observations/s')
})

test('messaging management retains exact counts, denied coverage and reload', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Exact management evidence', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { messaging_management_version: 1, messaging_management_enabled: false, native_parquet: false } }))
  const observed = (value: string) => ({ state: 'observed', value })
  const absent = { state: 'missing', reason: 'not_observed' }
  const record = (denied: boolean) => ({ job_id: 'job', sequence: denied ? '1' : '0', received_at: '2026-10-03T00:00:01Z', evidence: { version: 2, points: [], messaging_management: { version: 1, kind: 'kafka_offsets', source_id: 'source', source_revision: 1, generation: '9007199254740993', poll_sequence: denied ? '2' : '1', poll_id: denied ? 'denied' : 'observed', captured_at: '2026-10-03T00:00:00Z', expected_entities: 1, interval_seconds: 10, missed_ticks: denied ? '2' : '0', inventory_complete: !denied, incomplete: denied, ...(denied ? { unavailable_reason: 'permission_denied' } : {}), rows: denied ? [] : [{ entity: { kafka: { topic: 'orders', group: 'workers', partition: 0 } }, state: 'observed', observed_at: '2026-10-03T00:00:00Z', kafka_offsets: { committed_next: observed('0'), high_watermark_next: observed('9007199254740993'), last_stable_next: absent, log_end_next: { state: 'unsupported', reason: 'unsupported' }, leader_epoch: observed('0'), committed_leader_epoch: { state: 'stale', reason: 'stale' } } }] } } })
  await page.route(`**/api/v1/tests/${id}/monitor-management?*`, route => {
    const cursor = new URL(route.request().url()).searchParams.get('cursor')
    return route.fulfill({ json: { version: 1, records: [record(Boolean(cursor))], has_more: !cursor, ...(!cursor ? { cursor: 'opaque-cursor' } : {}) } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  const evidence = page.getByRole('region', { name: 'Messaging management evidence' })
  await expect(evidence).toBeVisible()
  await evidence.getByRole('button', { name: 'Read management evidence' }).click()
  await expect(evidence.locator('tbody tr').nth(0).locator('td').nth(0)).toHaveText('0')
  await expect(evidence.locator('tbody tr').nth(1).locator('td').nth(0)).toHaveText('9007199254740993')
  await expect(evidence.locator('tbody tr').nth(2).locator('td').nth(0)).toHaveText('—')
  await expect(evidence).toContainText('stale')
  const columnGap = await evidence.locator('tbody tr').nth(1).evaluate(row => {
    const cells = row.querySelectorAll('td')
    const value = document.createRange(); value.selectNodeContents(cells[0])
    const availability = document.createRange(); availability.selectNodeContents(cells[1])
    return availability.getBoundingClientRect().left - value.getBoundingClientRect().right
  })
  expect(columnGap).toBeGreaterThanOrEqual(12)
  await evidence.screenshot({ path: test.info().outputPath('management-exact-values.png') })
  const originalViewport = page.viewportSize()!
  await page.setViewportSize({ width: 375, height: 900 })
  const tableRegion = evidence.getByRole('region', { name: 'Management observation table for orders / 0 / workers', exact: true })
  await expect(tableRegion).toBeVisible()
  expect(await tableRegion.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
  await expect.poll(() => page.getByLabel('Dashboard controls', { exact: true }).evaluate(element => element.getBoundingClientRect().height)).toBeLessThan(300)
  await tableRegion.focus(); await page.keyboard.press('ArrowRight')
  await expect.poll(() => tableRegion.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)
  await expect.poll(() => tableRegion.evaluate(element => {
    const rect = element.getBoundingClientRect()
    return [rect.top + 8, rect.bottom - 8].every(y => {
      const target = document.elementFromPoint(rect.left + Math.min(40, rect.width / 2), y)
      return target !== null && element.contains(target)
    })
  })).toBe(true)
  await tableRegion.screenshot({ path: test.info().outputPath('management-narrow-values.png') })
  await page.screenshot({ path: test.info().outputPath('management-narrow-context.png') })
  await page.setViewportSize(originalViewport)
  await evidence.getByRole('button', { name: 'Next management page' }).click()
  await expect(evidence).toContainText('permission_denied')
  await expect(evidence.locator('table')).toHaveCount(0)
  await page.reload()
  await evidence.getByRole('button', { name: 'Read management evidence' }).click()
  await expect(evidence).toContainText('9007199254740993')
})

test('imported comparison preserves both exact identity selectors across save and reopen', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const leftId = '00000000-0000-0000-0000-000000000011'; const rightId = '00000000-0000-0000-0000-000000000012'
  const mapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['value'], name: 'latency', kind: 'observation', unit: 'ms' }], identity: { source: ['source'], host: ['host'], service: ['service'], dimensions: [{ name: 'a/b~c', value: ['label'] }] } }
  const imports = [leftId, rightId].map((id, i) => ({ id, project_id: workspace.projectId, name: `Identity ${i ? 'B' : 'A'}`, revision: 1, state: 'ready', files: [], accepted: 2, rejected: 0, mapping }))
  const definition = {
    left: { dataset_id: leftId, metric: 'latency', aggregation: 'p95', source_id: 'agent-a', host: 'host-a', service: 'service-a', transaction: 'Lookup', protocol: 'grpc', dimensions: { 'a/b~c': '' } },
    right: { dataset_id: rightId, metric: 'latency', aggregation: 'p95', source_id: 'agent-b', host: 'host-b', service: 'service-b', group: 'two', dimensions: JSON.parse('{"__proto__":"","group":"two"}') },
    from: '2026-01-01T00:00:00Z', to: '2026-01-01T00:01:00Z', bucket_seconds: 10, max_lag_seconds: 0, mode: 'overlay',
  }
  const executedDefinition = {
    ...definition,
    left: { dataset_id: leftId, metric: 'latency', aggregation: 'p95', source_id: 'agent-a', host: 'host-a', service: 'service-a', dimensions: { 'a/b~c': '', transaction: 'Lookup', protocol: 'grpc' } },
    right: { dataset_id: rightId, metric: 'latency', aggregation: 'p95', source_id: 'agent-b', host: 'host-b', service: 'service-b', dimensions: definition.right.dimensions },
    from: '2026-01-01T00:00:00.000Z', to: '2026-01-01T00:01:00.000Z',
  }
  const comparisons: unknown[] = [{ id: 'selectors', name: 'Owned selectors', revision: 1, definition, deleted: false }]
  let copied = false; let analyzed = 0
  await page.route('**/api/v1/analysis/**', async route => {
    const request = route.request(); const path = new URL(request.url()).pathname
    const send = (value: unknown) => route.fulfill({ json: value })
    if (path.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 2, chunk_bytes: 4 * 1024 * 1024 })
    if (path.endsWith('/imports')) return send(imports)
    if (path.endsWith('/comparisons')) {
      if (request.method() === 'POST') {
        expect(request.postDataJSON().definition).toEqual(executedDefinition)
        const saved = { id: 'copy', name: 'Selector copy', revision: 1, deleted: false, definition: request.postDataJSON().definition }
        comparisons.push(saved); copied = true; return send(saved)
      }
      return send(comparisons)
    }
    if (path.endsWith('/compare')) {
      const selected = request.postDataJSON()
      expect(selected).toEqual(executedDefinition)
      analyzed++
      const point = { timestamp_utc: '2026-01-01T00:00:00Z', metric_name: 'latency', unit: 'ms', value: 1, quality: 'valid' }
      return send({ request: selected, left: [point], right: [point] })
    }
    if (path.endsWith('/mappings')) return send([])
    return route.fulfill({ status: 404, json: { error: 'unexpected bounded workflow request' } })
  })
  const openDataset = async () => {
    await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
    const area = page.getByRole('region', { name: 'Analysis workspace', exact: true })
    await area.getByRole('button', { name: 'Identity A · ready', exact: true }).click()
    return area.getByRole('region', { name: 'Dataset comparison', exact: true })
  }
  let comparison = await openDataset()
  await comparison.getByRole('combobox', { name: 'Saved comparison', exact: true }).selectOption('selectors')
  const left = comparison.getByRole('group', { name: 'Dataset identity filters', exact: true })
  const right = comparison.getByRole('group', { name: 'Comparison identity filters', exact: true })
  await expect(left.getByLabel('Service', { exact: true })).toHaveValue('service-a')
  await expect(left.getByLabel('Filter 1 value', { exact: true })).toHaveValue('')
  await expect(left.getByLabel('Filter 2 name', { exact: true })).toHaveValue('transaction')
  await expect(left.getByLabel('Filter 2 value', { exact: true })).toHaveValue('Lookup')
  await expect(left.getByLabel('Filter 3 name', { exact: true })).toHaveValue('protocol')
  await expect(right.getByLabel('Filter 1 name', { exact: true })).toHaveValue('__proto__')
  await comparison.getByRole('button', { name: 'Analyze comparison', exact: true }).click()
  await expect(comparison.getByRole('button', { name: 'Save comparison', exact: true })).toBeDisabled()
  await comparison.getByLabel('Saved comparison name', { exact: true }).fill('Selector copy')
  await left.getByLabel('Source ID', { exact: true }).fill('later unsaved edit')
  await comparison.getByRole('button', { name: 'Save comparison', exact: true }).click()
  await expect.poll(() => copied).toBe(true)
  const downloading = page.waitForEvent('download')
  await comparison.getByRole('button', { name: 'Export Dataset comparison data as CSV', exact: true }).click()
  const download = await downloading
  const csvPath = await download.path()
  expect(csvPath).not.toBeNull()
  const csv = await readFile(csvPath!, 'utf8')
  expect(csv).toContain('Lookup'); expect(csv).toContain('grpc'); expect(csv).toContain('__proto__')
  expect(csv).not.toContain('later unsaved edit')
  comparison = await openDataset()
  await comparison.getByRole('combobox', { name: 'Saved comparison', exact: true }).selectOption('copy')
  await expect(comparison.getByRole('group', { name: 'Dataset identity filters', exact: true }).getByLabel('Source ID', { exact: true })).toHaveValue('agent-a')
  await comparison.getByRole('button', { name: 'Analyze comparison', exact: true }).click()
  await expect.poll(() => analyzed).toBe(2)
  await page.setViewportSize({ width: 760, height: 1000 })
  const reopenedLeft = comparison.getByRole('group', { name: 'Dataset identity filters', exact: true })
  const reopenedRight = comparison.getByRole('group', { name: 'Comparison identity filters', exact: true })
  await expect(reopenedLeft.getByLabel('Source ID', { exact: true })).toHaveValue('agent-a')
  await expect(reopenedRight.getByLabel('Source ID', { exact: true })).toHaveValue('agent-b')
  // Long valid labels must remain editable in this narrow owned container;
  // unsaved draft additions still cannot alter the executed result or its save.
  await reopenedLeft.getByRole('button', { name: 'Add dimension filter', exact: true }).click()
  await reopenedLeft.getByLabel('Filter 4 name', { exact: true }).fill('x'.repeat(128))
  await reopenedLeft.getByLabel('Filter 4 value', { exact: true }).fill('y'.repeat(128))
  await captureIdentityFilters(reopenedLeft, test.info().outputPath('imported-identity-comparison-left.png'))
  await captureIdentityFilters(reopenedRight, test.info().outputPath('imported-identity-comparison-right.png'))
})

test('older analytical peers keep v1 imports usable and refuse v2 identity work without downgrade', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const legacyId = '00000000-0000-0000-0000-000000000021'; const identityId = '00000000-0000-0000-0000-000000000022'
  const mapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['value'], name: 'latency', kind: 'observation', unit: 'ms' }] }
  let legacyState = 'uploading'; let legacyRevision = 1; let queries = 0; let mutations = 0
  const records = () => [
    { id: legacyId, project_id: workspace.projectId, name: 'Legacy measurements', state: legacyState, revision: legacyRevision, files: [{ id: 'file', relative_path: 'legacy.parquet', uploaded: 16, size: 16 }], mapping, accepted: 1, rejected: 0 },
    { id: identityId, project_id: workspace.projectId, name: 'Retained identity measurements', state: 'ready', revision: 1, files: [], mapping: { ...mapping, version: 2, identity: { source: ['source'] } }, accepted: 1, rejected: 0 },
  ]
  await page.route('**/api/v1/analysis/**', async route => {
    const request = route.request(); const path = new URL(request.url()).pathname
    const send = (value: unknown) => route.fulfill({ json: value })
    if (path.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, chunk_bytes: 4 * 1024 * 1024 })
    if (path.endsWith('/imports')) return send(records())
    if (path.endsWith('/mappings') || path.endsWith('/comparisons')) return send([])
    if (path.endsWith('/preview')) return send({ schema: { rows: 1, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64', repeated: false }, { path: 'value', path_components: ['value'], physical_type: 'DOUBLE', repeated: false }] }, samples: [] })
    if (path.endsWith('/mapping')) { expect(request.postDataJSON().mapping).toEqual(mapping); legacyRevision++; mutations++; return send({ ...records()[0], state: 'mapped' }) }
    if (path.endsWith('/commit')) { legacyState = 'ready'; legacyRevision++; mutations++; return send(records()[0]) }
    if (path.endsWith('/series')) { expect(path).toContain(legacyId); expect(request.postDataJSON()).not.toHaveProperty('source_id'); queries++; return send({ points: [] }) }
    return route.fulfill({ status: 404, json: { error: 'unexpected old-peer operation' } })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Analysis workspace', exact: true })
  await area.getByRole('button', { name: 'Legacy measurements · uploading', exact: true }).click()
  await expect(area.getByRole('group', { name: 'Imported measurement identities', exact: true })).toHaveCount(0)
  await expect(area).toContainText('Version 1 imports remain available')
  await area.getByRole('button', { name: 'Validate and import', exact: true }).click()
  await expect(area).toContainText('State: ready')
  const query = area.getByRole('group', { name: 'Query dataset', exact: true })
  await query.getByLabel('From (UTC)', { exact: true }).fill('2026-01-01T00:00')
  await query.getByLabel('To (UTC)', { exact: true }).fill('2026-01-01T00:01')
  await query.getByRole('button', { name: 'Show series', exact: true }).click()
  await expect.poll(() => queries).toBe(1)
  await area.getByRole('button', { name: 'Retained identity measurements · ready', exact: true }).click()
  await expect(query).toContainText('This dataset requires imported identity schema 2')
  await expect(query.getByRole('button', { name: 'Show series', exact: true })).toBeDisabled()
  await expect(area.getByRole('region', { name: 'Dataset comparison', exact: true })).toContainText('stored selectors remain unchanged')
  await expect(area.getByRole('button', { name: 'Analyze comparison', exact: true })).toBeDisabled()
  expect(queries).toBe(1); expect(mutations).toBe(2)
  expect(records()[1].mapping.version).toBe(2)
})


for (const peer of ['missing-capability', 'missing-mapping', 'current', 'legacy-handler', 'bad-echo'] as const) {
  test(`mapping reassociation preserves declared measurements with ${peer}`, async ({ page }) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    const mapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['value'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p95', window_seconds: 60 }] }
    const dataset = { id: '00000000-0000-0000-0000-000000000031', project_id: workspace.projectId, name: 'Declared measurements', state: 'ready', revision: 4, files: [], mapping: peer === 'missing-mapping' ? undefined : mapping, accepted: 2, rejected: 0 }
    let requests = 0
    await page.route('**/api/v1/analysis/**', async route => {
      const request = route.request(); const path = new URL(request.url()).pathname
      const send = (value: unknown) => route.fulfill({ json: value })
      if (path.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 2, mapping_association_version: peer === 'missing-capability' ? undefined : 1, chunk_bytes: 4 * 1024 * 1024, summary_statistics: ['p95'] })
      if (path.endsWith('/imports')) return send([dataset])
      if (path.endsWith('/mappings') || path.endsWith('/comparisons')) return send([])
      if (path.endsWith('/association')) {
        expect(request.method()).toBe('PATCH')
        expect(request.postDataJSON()).toEqual({ contract_version: 1, run_id: 'reviewed-run', revision: 4 })
        requests++
        if (peer === 'legacy-handler') return route.fulfill({ status: 405, json: { error: 'method not allowed' } })
        return send({ contract_version: 1, import: { ...dataset, revision: 5, run_id: 'reviewed-run', mapping: peer === 'bad-echo' ? undefined : mapping } })
      }
      return route.fulfill({ status: 404, json: { error: 'unexpected reassociation workflow operation' } })
    })
    await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
    const area = page.getByRole('region', { name: 'Analysis workspace', exact: true })
    await area.getByRole('button', { name: 'Declared measurements · ready', exact: true }).click()
    const comparison = area.getByRole('region', { name: 'Dataset comparison', exact: true })
    await comparison.getByRole('combobox', { name: 'Compare with', exact: true }).selectOption('run')
    await comparison.getByLabel('Run ID', { exact: true }).fill('reviewed-run')
    const associate = comparison.getByRole('button', { name: 'Associate with run', exact: true })
    if (peer === 'missing-capability' || peer === 'missing-mapping') {
      await expect(associate).toBeDisabled()
      await expect(comparison).toContainText(peer === 'missing-mapping' ? 'Mapping details are unavailable' : 'cannot safely reassociate')
      expect(requests).toBe(0)
    } else {
      await associate.click()
      await expect.poll(() => requests).toBe(1)
      if (peer === 'current') await expect(comparison).toContainText('Associated with run reviewed-run')
      else {
        await expect(comparison).not.toContainText('Associated with run reviewed-run')
        await expect(comparison.getByRole('alert')).toContainText(peer === 'bad-echo' ? 'did not confirm the mapping-preserving association' : 'method not allowed')
      }
    }
    expect(dataset.mapping).toEqual(peer === 'missing-mapping' ? undefined : mapping)
  })
}

test('decimal precision review requires current consent and preserves identity queries', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const id = '00000000-0000-0000-0000-000000000091'
  const column = 'amount_' + 'x'.repeat(200)
  let revision = 7; let state = 'mapped'; let reviews = 0; let commits = 0
  let mapping: { version: number; precision_policy?: string; timestamp: string[]; timestamp_encoding: string; timezone: string; measures: { value: string[]; name: string; kind: string; unit: string }[]; identity?: { host?: string[] } } = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: [column], name: 'amount', kind: 'gauge', unit: 'count' }] }
  const digest = 'a'.repeat(64)
  const record = () => ({ id, project_id: workspace.projectId, name: 'Decimal precision evidence', revision, state, files: [{ id: 'source', relative_path: 'decimal.parquet', size: 16, uploaded: 16 }], mapping, accepted: state === 'ready' ? 1 : 0, rejected: 0 })
  await page.route('**/api/v1/analysis/**', async route => {
    const req = route.request(); const path = new URL(req.url()).pathname
    const send = (value: unknown, status = 200) => route.fulfill({ status, json: value })
    if (path.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 2, precision_consent_version: 1 })
    if (path.endsWith('/imports')) return send([record()])
    if (path.endsWith('/mappings') || path.endsWith('/comparisons')) return send([])
    if (path.endsWith('/preview')) return send({ schema: { rows: 1, columns: ['ts', column].map(path => ({ path, path_components: [path], physical_type: 'INT64', repeated: false })) }, samples: [] })
    if (path.endsWith('/precision-review')) {
      expect(req.postDataJSON().revision).toBe(revision); reviews++
      return send({ version: 1, import_id: id, revision, acknowledgement_sha256: 'b'.repeat(64), review: { version: 1, conversion: 'float64', mapping_sha256: digest, source_set_sha256: digest, source_count: 1, sha256: 'c'.repeat(64), risks: [{ file_sha256: digest, column: [column], precision: 38, scale: 2, reason: 'fractional_decimal_may_round_to_float64' }] } })
    }
    if (path.endsWith('/mapping')) {
      expect(req.postDataJSON()).toMatchObject({ revision, mapping: { version: 3, precision_policy: 'allow_float64_rounding', identity: { host: [column] } }, precision_review_sha256: 'c'.repeat(64), precision_acknowledgement_sha256: 'b'.repeat(64) })
      mapping = req.postDataJSON().mapping; revision++; return send(record())
    }
    if (path.endsWith('/commit')) { commits++; state = 'ready'; revision++; return send(record(), 202) }
    if (path.endsWith('/series')) { expect(req.postDataJSON().host).toBe('exact-host'); return send({ points: [] }) }
    return send(record())
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Analysis workspace' })
  await area.getByRole('button', { name: 'Decimal precision evidence · mapped' }).click()
  const commit = area.getByRole('button', { name: 'Validate and import', exact: true })
  await expect(commit).toBeDisabled()
  await area.getByRole('button', { name: 'Review precision', exact: true }).click()
  await expect(area).toContainText('does not count rows whose values actually change')
  await expect(commit).toBeDisabled()
  await area.getByRole('combobox', { name: 'Decimal conversion', exact: true }).selectOption('allow_float64_rounding')
  await area.getByRole('button', { name: 'Review precision', exact: true }).click()
  const accept = area.getByRole('checkbox', { name: 'I accept potential rounding for these source files and this mapping', exact: true })
  await expect(commit).toBeDisabled(); await accept.check(); await expect(commit).toBeEnabled()
  await area.getByRole('combobox', { name: 'Host column', exact: true }).selectOption(JSON.stringify([column]))
  await expect(accept).toHaveCount(0); await expect(commit).toBeDisabled()
  await area.getByRole('button', { name: 'Review precision', exact: true }).click()
  await accept.check()
  await page.setViewportSize({ width: 760, height: 900 })
  const precisionPanel = area.getByRole('group', { name: 'Numeric precision', exact: true })
  await expect(precisionPanel).toBeVisible()
  expect(await precisionPanel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  expect(await precisionPanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true)
  await precisionPanel.screenshot({ path: test.info().outputPath('decimal-precision-consent.png') })
  await commit.click(); await expect(area).toContainText('State: ready')
  expect(reviews).toBe(3); expect(commits).toBe(1)
  const query = area.getByRole('group', { name: 'Query dataset', exact: true })
  await query.getByLabel('Host', { exact: true }).fill('exact-host')
  await query.getByLabel('From (UTC)', { exact: true }).fill('2026-01-01T00:00')
  await query.getByLabel('To (UTC)', { exact: true }).fill('2026-01-01T00:01')
  await query.getByRole('button', { name: 'Show series', exact: true }).click()
  await expect(area.getByRole('alert')).toHaveCount(0)
})
