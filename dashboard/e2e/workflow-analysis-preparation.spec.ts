import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Project authorization and navigation use the real controller. Bounded
// transport fixtures exercise the complete UI; native normalization and the
// independent zero-traffic counters are covered by Go integration tests.
test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

test('Parquet upload, mapping, query and deletion remain in project scope', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const id = '00000000-0000-0000-0000-000000000001'
  let state = 'uploading'; let revision = 1; let uploaded = false; let deleted = false
  let mapping: unknown
  const record = () => ({ id, project_id: workspace.projectId, name: 'External latency', revision, state, files: [{ id: 'source', relative_path: 'sample.parquet', size: 16, uploaded: uploaded ? 16 : 0 }], accepted: state === 'ready' ? 3 : 0, rejected: 0, mapping })
  const point = (value: number, second: number) => ({ timestamp_utc: `2026-01-01T00:00:0${second}Z`, metric_name: 'latency', unit: 'ms', value, quality: 'valid' })
  await page.route('**/api/v1/analysis/**', async route => {
    const req = route.request(); const url = new URL(req.url()); const method = req.method()
    const send = (value: unknown, status = 200) => route.fulfill({ status, json: value })
    if (url.pathname.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, chunk_bytes: 4 * 1024 * 1024 })
    if (url.pathname.endsWith('/mappings') || url.pathname.endsWith('/comparisons')) return send([])
    if (url.pathname.endsWith('/imports')) {
      expect(url.searchParams.get('project_id')).toBe(workspace.projectId)
      if (method === 'POST') { expect(req.postDataJSON().files[0].relative_path).toBe('sample.parquet'); return send(record(), 201) }
      return send(deleted ? [] : [record()])
    }
    if (url.pathname.endsWith('/files/source') && method === 'PUT') { expect(req.postDataBuffer()?.length).toBe(16); uploaded = true; revision++; return send(record()) }
    if (url.pathname.endsWith('/preview')) return send({ schema: { rows: 3, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64' }, { path: 'latency', path_components: ['latency'], physical_type: 'DOUBLE' }] }, samples: [{ ts: 1767225600000, latency: 20 }] })
    if (url.pathname.endsWith('/mapping')) { mapping = req.postDataJSON().mapping; expect(req.postDataJSON().revision).toBe(revision); state = 'mapped'; revision++; return send(record()) }
    if (url.pathname.endsWith('/commit')) { state = 'ready'; revision++; return send(record(), 202) }
    if (url.pathname.endsWith('/series')) { expect(req.postDataJSON().from).toBe('2026-01-01T00:00:00.000Z'); return send({ points: [point(20, 0), point(40, 1)] }) }
    if (method === 'DELETE') { deleted = true; return route.fulfill({ status: 204 }) }
    return send(record())
  })
  await page.goto(`/#/monitoring?category=analysis&${scopeQuery(workspace)}`); await page.reload()
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
  await area.getByRole('button', { name: 'Validate and import', exact: true }).click()
  await expect(area).toContainText('State: ready')
  const query = area.getByRole('group', { name: 'Query dataset', exact: true })
  await query.getByLabel('Metric', { exact: true }).fill('latency')
  await query.getByLabel('From (UTC)', { exact: true }).fill('2026-01-01T00:00')
  await query.getByLabel('To (UTC)', { exact: true }).fill('2026-01-01T00:01')
  await query.getByRole('button', { name: 'Show series', exact: true }).click()
  await expect(area.getByRole('img', { name: 'latency over UTC time; 2 buckets' })).toBeVisible()
  await page.setViewportSize({ width: 760, height: 1000 })
  await expect(area.getByRole('heading', { name: 'Parquet analysis', exact: true })).toBeVisible()
  await query.screenshot({ path: test.info().outputPath('parquet-query.png') })
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
  await inspector.getByText('Checkout · 30 ms · Success', { exact: true }).click()
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
    if (q.evidence_mode === 'archive') expect(q.overlay_selection).toMatchObject({ dataset_id: 'external-latency', metric: 'latency', aggregation: 'avg' })
    return route.fulfill({ json: { mode: q.evidence_mode, start, warnings: ['Original units retained; gaps excluded from correlation.'], series: [{ name: 'Checkout', unit: 'ms', points: [{ from: start, to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 3, value: 30 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 2, value: 40 }, { from: '2026-10-01T00:00:20Z', to: '2026-10-01T00:00:30Z', offset_seconds: 20, count: 0, value: null }] }], ...(q.evidence_mode === 'archive' ? { overlay: [{ timestamp_utc: start, unit: 'ms', quality: 'valid', value: 22 }, { timestamp_utc: '2026-10-01T00:00:10Z', unit: 'ms', quality: 'valid', value: 28 }], correlation: { Checkout: [{ lag_seconds: 0, pairs: 1, coverage: 0.5 }] } } : {}) } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Evidence workflow', { exact: true }).selectOption('archive')
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
  await page.setViewportSize({ width: 1280, height: 1400 }); await panels.evaluate(node => node.scrollIntoView({ block: 'start' }))
  await panels.screenshot({ path: test.info().outputPath('grouped-overlay-panel.png') })
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Evidence workflow', { exact: true }).selectOption('live')
  await page.getByLabel('Panel title', { exact: true }).fill('Live actions')
  await page.getByLabel('Visualization', { exact: true }).selectOption('table')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  await panels.getByLabel('Project dashboard', { exact: true }).selectOption({ label: 'Live actions' })
  await expect(panels).toContainText('Live source intervals')
  await page.reload(); await panels.getByLabel('Project dashboard', { exact: true }).selectOption({ label: 'Live actions' })
  await expect(panels).toContainText('Checkout (ms)')
})
