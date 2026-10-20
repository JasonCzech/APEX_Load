import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { javaHTTPFixture } from '../src/utils/javaHttpTestFixtures'

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

// Transport, persistence and raw measurement arithmetic use real Go integration
// fixtures. These controlled responses exercise the real authenticated UI and
// project panel persistence, without running a customer or Java target.
test('Java representations retain their own live action population and archived report evidence', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Java representations', duration: '2m', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  const diagnostics = javaHTTPFixture()
  let archived = false, available = false
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch(), record = await response.json()
    return route.fulfill({ response, json: { ...record, status: archived ? 'COMPLETE' : 'RUNNING', ...(archived ? { archive: { transaction_count: 2 }, started_at: '2026-10-01T00:00:00Z', ended_at: '2026-10-01T00:00:02Z' } : {}), live_metrics: { total_transactions: 999, total_errors: 0, active_vusers: 1, transactions: {}, ...(available ? { java_http: { version: 1, actions: { 'Read object': { population: 2, diagnostics } } } } : {}) } } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: { manifest: { test_id: id, test_name: 'Java representations', status: 'COMPLETE', provenance: { started_at: '2026-10-01T00:00:00Z', duration: '2s', total_vusers: 1 } }, overview: { transactions: 999 }, by_action: { 'Read object': { transactions: 3, actual_action_samples: 2, java_http: diagnostics } } } }))
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`); await page.reload()
  const panel = page.getByRole('region', { name: 'Java HTTP representation diagnostics', exact: true })
  await expect(page.getByText('Create project analytics panel', { exact: true })).toBeVisible()
  await expect(panel).toHaveCount(0)
  available = true
  await expect(panel).toBeVisible()
  await panel.getByText('Read object', { exact: true }).click()
  await expect(panel).toContainText('Observed diagnostics: 1 of 2 action executions')
  await expect(panel).toContainText('Retained response: 0 bytes mean')
  await expect(panel).toContainText('Base64 / retained response: Unavailable')
  await expect(panel).toContainText('Coverage incomplete')
  await expect(panel).toContainText('JVM object footprint is unavailable')
  await page.setViewportSize({ width: 760, height: 900 })
  await panel.screenshot({ path: test.info().outputPath('java-live-representations.png') })
  archived = true
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`); await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await expect(panel).toBeVisible()
  await panel.getByText('Read object', { exact: true }).click()
  await expect(panel).toContainText('Observed diagnostics: 1 of 2 action executions')
  await expect(panel).toContainText('Prepared request compression: none 1; gzip 0; deflate 0')
  await panel.screenshot({ path: test.info().outputPath('java-archive-representations.png') })
})

test('Java native panel saves and reopens paired sums with compression and missing evidence in CSV', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Java native panel', duration: '1s', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  let reads = 0
  await page.route(`**/api/v1/tests/${id}/archive/native-series`, route => {
    const query = route.request().postDataJSON()
    expect(query).toMatchObject({ population: 'java_http_action', metric: 'java_http_prepared_per_decoded', aggregation: 'ratio_of_sums', filters: { action: 'Read object' }, comparison_run_id: 'java-reference', end_seconds: 20, bucket_seconds: 10, max_series: 1 })
    expect(query).not.toHaveProperty('evidence_mode'); reads++
    return route.fulfill({ json: { current: { run_id: id, unit: 'ratio', java_http_compression: 'gzip', warnings: [], identities: ['Read object'], buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 2, value: .5 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 0, value: null, reason: 'measurement_unavailable' }] }, baseline: { run_id: 'java-reference', unit: 'ratio', java_http_compression: 'gzip', warnings: [], identities: ['Read object'], buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 7, value: 0 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 3, value: null, reason: 'incomplete_representation_evidence' }] }, comparable: false, warnings: [], added: [], removed: [] } })
  })
  await page.goto(`/#/test/${id}?${scopeQuery(workspace)}`); await page.reload()
  await page.getByText('Create project analytics panel', { exact: true }).click()
  await page.getByLabel('Panel title', { exact: true }).fill('Java representation ratio')
  await page.getByRole('combobox', { name: 'Evidence population', exact: true }).selectOption('java_http_action')
  await expect(page.getByRole('button', { name: 'Save project panel', exact: true })).toBeDisabled()
  await page.getByLabel('Exact action or transaction name', { exact: true }).fill('Read object')
  await page.getByRole('combobox', { name: 'Metric', exact: true }).selectOption('java_http_prepared_per_decoded')
  await expect(page.getByRole('combobox', { name: 'Measurement', exact: true })).toHaveValue('ratio_of_sums')
  await page.getByLabel('End elapsed seconds', { exact: true }).fill('20')
  await page.getByLabel('Comparison run ID (optional)', { exact: true }).fill('java-reference')
  await page.getByRole('combobox', { name: 'Visualization', exact: true }).selectOption('table')
  await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
  const panels = page.getByRole('region', { name: 'Project analytics panels', exact: true })
  await expect(panels).toContainText('Java representation ratio')
  await page.reload()
  await expect(panels).toContainText('Prepared-body compression: current gzip')
  expect(reads).toBeGreaterThan(1)
  await panels.getByText('Inspect time buckets (2)', { exact: true }).click()
  await expect(panels).toContainText('Missing (measurement unavailable)')
  await expect(panels).toContainText('Missing (incomplete representation evidence)')
  await expect(panels.getByRole('columnheader', { name: 'Baseline measured observations', exact: true })).toBeVisible()
  await expect(panels.getByRole('cell', { name: '7', exact: true })).toBeVisible()
  await expect(panels.getByRole('button', { name: /Inspect current bucket/ })).toHaveCount(0)
  const pending = page.waitForEvent('download')
  await panels.getByRole('button', { name: 'Export Java representation ratio data as CSV', exact: true }).click()
  const download = await pending, stream = await download.createReadStream()
  const chunks: Buffer[] = []; for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
  const csv = Buffer.concat(chunks).toString('utf8')
  expect(csv).toContain('Current (ratio)'); expect(csv).toContain('0.5'); expect(csv).toContain('measurement_unavailable'); expect(csv).toContain('gzip')
  expect(csv).toContain('Baseline measured observations'); expect(csv).toContain('Baseline availability'); expect(csv).toContain('incomplete_representation_evidence'); expect(csv).toContain('"0","0.5","0","2","measured","gzip","7","measured","gzip"')
  await page.setViewportSize({ width: 760, height: 900 })
  await panels.screenshot({ path: test.info().outputPath('java-native-panel.png') })
})

test('Java baseline detail retains per-run populations and versioned evidence in its export', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Java baseline evidence', duration: '2m', groups: [{ name: 'requests', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  const identity = { id, name: 'Current Java run', release_id: 'release', release_name: 'Release', scenario_id: 'scenario', status: 'COMPLETE', vusers: 1, peak_vusers: 1, pinned: true, archive_available: true }
  const baseline = { ...identity, id: 'java-baseline', name: 'Java baseline' }
  const window = { start_seconds: 30, end_seconds: 120, load: 1, total_load: 1, group: 'requests', direction: 'steady', mix: { requests: 1 } }
  const measurement = { samples: 2, successes: 2, errors: 0, mean: 10, min: 10, max: 10, p50: 10, p90: 10, p95: 10, p99: 10, throughput: 1, successful_throughput: 1, error_pct: 0, coverage_pct: 100, instances: 1, points: [], java_http: javaHTTPFixture() }
  const { defaultPolicy } = await import('../src/components/baselineComparison/model')
  const snapshot = { id: 'java-snapshot', project_id: workspace.projectId, current: identity, baselines: [baseline], created_at: '2026-10-01T00:00:00Z', rule_version: 'baseline-comparison-v1', request: { baseline_ids: [baseline.id], population: 'action' }, policy: defaultPolicy, transactions: [{ id: 'tx/0', name: 'Read object', group: 'requests', protocol: 'java_http', source: 'action', unit: 'ms', window, current: measurement, baselines: { [baseline.id]: { measurement: { ...measurement, samples: 1, successes: 1 }, window, qualification: ['Partial diagnostics'], approximate: false } } }], resources: [], setup: [], findings: [], warnings: [] }
  await page.route(`**/api/v1/tests/${id}/baseline-comparisons/java-snapshot`, route => route.fulfill({ json: snapshot }))
  await page.route(`**/api/v1/tests/${id}/baseline-candidates*`, route => route.fulfill({ json: { runs: [baseline], total: 1 } }))
  await page.route('**/api/v1/scenarios/*/comparison-policy', route => route.fulfill({ json: defaultPolicy }))
  await page.route(`**/api/v1/tests/${id}`, async route => { if (route.request().method() !== 'GET') return route.continue(); const response = await route.fetch(), record = await response.json(); return route.fulfill({ response, json: { ...record, scenario_id: 'scenario', status: 'COMPLETE', archive: { transaction_count: 2 }, started_at: '2026-10-01T00:00:00Z', ended_at: '2026-10-01T00:02:00Z' } }) })
  await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: { manifest: { test_id: id, test_name: 'Java baseline evidence', status: 'COMPLETE', provenance: { total_vusers: 1, duration: '2m', started_at: '2026-10-01T00:00:00Z' } }, overview: { transactions: 2 }, by_action: {} } }))
  await page.goto(`/#/report/${id}?section=comparison&comparison_id=java-snapshot&${scopeQuery(workspace)}`); await page.reload()
  await page.getByRole('button', { name: 'Read object', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Read object', exact: true })
  for (const [name, count] of [['Current', 2], ['Java baseline', 1]] as const) {
    const evidence = drawer.getByRole('region', { name: `Java representation evidence for ${name}`, exact: true })
    await evidence.getByText('Read object', { exact: true }).click()
    await expect(evidence).toContainText(`Observed diagnostics: 1 of ${count} action executions`)
    await expect(evidence).toContainText('Retained response: 0 bytes mean')
  }
  await expect(drawer).toContainText('Partial diagnostics')
  const pending = page.waitForEvent('download')
  await drawer.getByRole('button', { name: 'Export Read object comparison data as CSV', exact: true }).click()
  const stream = await (await pending).createReadStream(), chunks: Buffer[] = []
  for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
  const csv = Buffer.concat(chunks).toString('utf8')
  expect(csv).toContain('Java action population'); expect(csv).toContain('unavailable_embedded_opaque_bytes'); expect(csv).toContain('response_retained')
  await page.setViewportSize({ width: 760, height: 900 })
  await drawer.screenshot({ path: test.info().outputPath('java-baseline-detail.png'), animations: 'disabled' })
  await drawer.getByRole('region', { name: 'Java representation evidence for Current', exact: true }).screenshot({ path: test.info().outputPath('java-baseline-current-evidence.png'), animations: 'disabled' })
})


test('Java representation help remains searchable and qualified after reload', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.goto('/#/docs')
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('opaque representations')
  await page.locator('.help-search-results').getByRole('link', { name: 'Web/API execution support', exact: true }).click()
  await expect(page).toHaveURL(/article=web-api-support/)
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('each with its own observed count')
  await expect(article).toContainText('do not measure JVM object footprint')
  await page.reload()
  await expect(article).toContainText('distributed source-window qualification remains open')
  await page.setViewportSize({ width: 760, height: 900 })
  await article.locator('p').filter({ hasText: 'Live and archived Java HTTP diagnostics expose' }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('java-representation-help.png'), animations: 'disabled' })
})
