import { test, expect, json, login, seedScript, seedScenario } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { abortLiveRun, startLiveRun } from './support/liveRun'

test.beforeEach(async ({ page }) => {
  test.setTimeout(60000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
})

test('Elastic saved connection drafts and revision inspections preserve credential privacy', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/settings?setting=elk-capture&${scopeQuery(workspace)}`)
  await page.reload()
  const card = page.getByLabel('ELK connections', { exact: true })
  await card.getByRole('button', { name: 'Add ELK connection', exact: true }).click()
  const editor = card.getByLabel('ELK connection editor', { exact: true })
  await editor.getByLabel('Name (required)', { exact: true }).fill('Workflow Elastic')
  await editor.getByLabel('Elasticsearch / OpenSearch endpoint', { exact: true }).fill('https://workflow.elastic.example.com')
  await editor.getByRole('combobox', { name: 'Authentication', exact: true }).selectOption('api_key')
  await editor.getByLabel('API key project-secret reference', { exact: true }).fill('workflow-elastic-key')
  await expect(editor.getByRole('checkbox', { name: 'Enable for new tests', exact: true })).toBeDisabled()
  const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`)
  await editor.getByRole('button', { name: 'Save draft', exact: true }).click()
  const response = await created
  expect(response.ok()).toBeTruthy()
  const text = await response.text()
  expect(text).not.toContain('workflow-elastic-key')
  const connection = JSON.parse(text) as { id: string; revision: number }
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(editor.getByLabel('API key project-secret reference', { exact: true })).toHaveValue('')
  await editor.getByRole('button', { name: 'Discard draft', exact: true }).click()
  const operation = { id: 'elastic-verify', connection_id: connection.id, connection_revision: connection.revision, kind: 'verify', status: 'pending', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 60000).toISOString() }
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/elk-connections/${connection.id}/inspections`, route => {
    expect(route.request().postDataJSON()).toMatchObject({ kind: 'verify', expected_revision: connection.revision })
    return route.fulfill({ status: 202, json: operation })
  })
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/elk-connections/${connection.id}/inspections/elastic-verify`, route => route.fulfill({ json: { ...operation, status: 'complete', result: { capabilities: { product: 'elasticsearch', version: '9.0', dsl: true, esql: false, pit: true, serverless: false } } } }))
  await card.getByRole('button', { name: 'Test saved connection', exact: true }).click()
  await expect(card.getByLabel('Elastic inspection result', { exact: true })).toContainText('Detected elasticsearch 9.0')
  await expect(card.getByLabel('Elastic inspection result', { exact: true })).toContainText('ES|QL: unavailable')
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  await expect(card).toContainText('No ELK connections are configured')
})

test('Elastic source workflow supports direct preview and assigned OpenSearch collectors', async ({ page }, testInfo) => {
  const workspace = await createWorkspace(page.context().request)
  const sourcePath = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  let sources: Record<string, unknown>[] = []
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true } }))
  await page.route(`**${sourcePath}**`, route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: sources })
    sources = [{ ...route.request().postDataJSON(), id: 'elastic-source', revision: 1 }]
    return route.fulfill({ json: sources[0] })
  })
  await page.route('**/api/v1/monitoring/collectors?*', route => route.fulfill({ json: { collectors: [{ id: 'elastic-agent', name: 'Private collector', health: 'healthy' }] } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`, route => route.fulfill({ json: { connections: [
    { id: 'elastic-direct', name: 'Verified Elastic', product: 'elasticsearch', enabled: true, revision: 2, verified_revision: 2, collection_mode: 'direct', config: { endpoint: 'https://elastic.example.com', product: 'elasticsearch', auth_type: 'api_key' }, capabilities: { esql: true, async_esql: false } },
    { id: 'opensearch-private', name: 'Verified private OpenSearch', product: 'opensearch', enabled: true, revision: 3, verified_revision: 3, collection_mode: 'collector', collector_agent_id: 'elastic-agent', config: { endpoint: 'https://opensearch.example.com', product: 'opensearch', auth_type: 'basic' }, capabilities: { esql: false } },
  ] } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/elk-connections/elastic-direct/inspections`, route => {
    const input = route.request().postDataJSON()
    if (input.selection.cluster_health && !input.selection.logs) {
      expect(input).toMatchObject({ kind: 'preview', expected_revision: 2, selection: { indices: [], cluster_health: true, logs: false } })
      return route.fulfill({ status: 202, json: { id: 'health-preview', connection_id: 'elastic-direct', connection_revision: 2, kind: 'preview', status: 'complete', result: { preview: { points: [], coverage: { health: { status: 'available' } } } } } })
    }
    expect(input).toMatchObject({ kind: 'preview', expected_revision: 2, selection: { indices: ['logs-*'], page_size: 100, settle_seconds: 60, overlap_seconds: 120 } })
    return route.fulfill({ status: 202, json: { id: 'preview', connection_id: 'elastic-direct', connection_revision: 2, kind: 'preview', status: 'complete', result: { preview: { points: [], evidence: [{ id: 'doc', kind: 'log', log: { message: 'Scrubbed preview' }, correlation: 'service_time', service: 'checkout' }], coverage: { logs: { status: 'partial', truncated: true, reason: 'preview_limit' } } } } } })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sourceForm = page.getByRole('region', { name: 'Run monitor sources', exact: true })
  await sourceForm.getByRole('combobox', { name: 'Pack', exact: true }).selectOption('elastic')
  await sourceForm.getByLabel('Name', { exact: true }).fill('Checkout Elastic')
  await sourceForm.getByRole('combobox', { name: 'Elastic connection', exact: true }).selectOption('elastic-direct')
  await expect(sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true })).toBeDisabled()
  await sourceForm.getByRole('checkbox', { name: 'Capture logs', exact: true }).uncheck()
  await sourceForm.getByRole('checkbox', { name: 'Capture cluster health', exact: true }).check()
  await sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true }).click()
  await expect(sourceForm.getByRole('region', { name: 'Elastic inspection result', exact: true })).toContainText('health: available')
  await sourceForm.getByRole('checkbox', { name: 'Capture logs', exact: true }).check()
  await expect(sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true })).toBeDisabled()
  await sourceForm.getByRole('checkbox', { name: 'Capture cluster health', exact: true }).uncheck()
  await sourceForm.getByLabel('Index patterns or data streams (up to 32)', { exact: true }).fill('logs-*')
  await sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true }).click()
  await expect(sourceForm).toContainText('Scrubbed preview')
  await expect(sourceForm).toContainText('preview_limit')
  await sourceForm.getByRole('region', { name: 'Elastic inspection result', exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('elastic-source-preview.png') })
  const directSaved = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === sourcePath)
  await sourceForm.getByRole('button', { name: 'Save monitor source', exact: true }).click()
  expect((await directSaved).postDataJSON()).toMatchObject({ executor_mode: 'direct', connection_id: 'elastic-direct', interval_seconds: 10, elastic: { logs: true, indices: ['logs-*'] } })
  await sourceForm.getByRole('button', { name: 'Edit Checkout Elastic', exact: true }).click()
  await sourceForm.getByRole('combobox', { name: 'Elastic connection', exact: true }).selectOption('opensearch-private')
  await expect(sourceForm).toContainText('Assigned collector elastic-agent')
  await expect(sourceForm.getByRole('combobox', { name: 'Collector', exact: true })).toHaveValue('elastic-agent')
  await sourceForm.getByRole('button', { name: 'Add Elastic query', exact: true }).click()
  await expect(sourceForm.getByRole('combobox', { name: 'Query 1 language', exact: true }).locator('option[value="esql"]')).toHaveJSProperty('disabled', true)
  await sourceForm.getByRole('button', { name: 'Remove Elastic query 1', exact: true }).click()
  const collectorSaved = page.waitForRequest(request => request.method() === 'PUT' && new URL(request.url()).pathname === `${sourcePath}/elastic-source`)
  await sourceForm.getByRole('button', { name: 'Save monitor source', exact: true }).click()
  expect((await collectorSaved).postDataJSON()).toMatchObject({ executor_mode: 'collector', collector_id: 'elastic-agent', connection_id: 'opensearch-private' })
})

test('New Test preserves multiple Elastic bindings, exact-window criteria and draft selections', async ({ page }, testInfo) => {
  const workspace = await createWorkspace(page.context().request)
  const script = await seedScript(page.context().request, workspace)
  const scenario = await seedScenario(page.context().request, workspace, script)
  const sourcePath = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  const sources = [
    { id: 'elastic-metrics', name: 'Elastic metrics', enabled: true, pack: 'elastic', connection_id: 'expanded-elastic', elastic: { metrics: [{ name: 'interval_count', statistic: 'count', unit: 'count', bucket_seconds: 10 }, { name: 'p99', statistic: 'p99', unit: 'us', bucket_seconds: 10 }] } },
    { id: 'opensearch-logs', name: 'OpenSearch logs', enabled: true, pack: 'elastic', connection_id: 'private-opensearch', elastic: { logs: true } },
  ]
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true } }))
  await page.route(`**${sourcePath}`, route => route.fulfill({ json: sources }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`, route => route.fulfill({ json: { connections: [
    { id: 'legacy-elk', name: 'Legacy capture', enabled: true, status: 'ready', legacy_capture_ready: true },
    { id: 'expanded-elastic', name: 'Expanded profile', enabled: true, status: 'ready', product: 'elasticsearch', legacy_capture_ready: false },
  ] } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/dynatrace-connections`, route => route.fulfill({ json: { connections: [{ id: 'ready-dynatrace', name: 'Ready Dynatrace', enabled: true, status: 'ready' }] } }))
  await page.goto(`/#/new-test?scenarioId=${scenario}&${scopeQuery(workspace)}`)
  await page.reload()
  const bindings = page.getByRole('group', { name: 'Run monitor selection', exact: true })
  const mode = bindings.getByRole('combobox', { name: 'Monitor binding mode', exact: true })
  await expect(mode).toHaveValue('inherit')
  await mode.selectOption('off')
  await expect(mode).toHaveValue('off')
  await mode.selectOption('custom')
  await bindings.getByRole('checkbox', { name: 'Elastic metrics (elastic)', exact: true }).check()
  await bindings.getByRole('checkbox', { name: 'OpenSearch logs (elastic)', exact: true }).check()
  await bindings.getByRole('checkbox', { name: 'Require Elastic metrics readiness and complete evidence', exact: true }).check()
  const rules = page.getByRole('group', { name: 'Monitoring performance criteria', exact: true })
  await rules.getByRole('button', { name: 'Add monitoring performance criterion', exact: true }).click()
  await rules.getByRole('combobox', { name: 'Criterion 1 source', exact: true }).selectOption('elastic-metrics')
  await rules.getByRole('combobox', { name: 'Criterion 1 metric', exact: true }).selectOption('p99')
  await expect(rules.getByRole('combobox', { name: 'Criterion 1 statistic', exact: true })).toHaveValue('value')
  await expect(rules.getByRole('textbox', { name: 'Criterion 1 unit', exact: true })).toHaveValue('ms')
  const legacy = page.getByRole('combobox', { name: 'Elastic log capture', exact: true })
  await expect(legacy.locator('option')).toHaveText(['Off for this test', 'Legacy capture'])
  await expect(page.getByRole('combobox', { name: 'Dynatrace SaaS capture', exact: true })).toContainText('Ready Dynatrace')
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await page.getByRole('button', { name: 'Run Setup', exact: true }).click()
  await expect(bindings.getByRole('checkbox', { name: 'OpenSearch logs (elastic)', exact: true })).toBeChecked()
  await page.getByRole('navigation').getByRole('button', { name: 'Dashboard', exact: true }).click()
  await page.getByRole('dialog', { name: /^Unsaved changes$/i }).getByRole('button', { name: 'Save changes', exact: true }).click()
  await page.getByRole('navigation').getByRole('button', { name: 'New Test', exact: true }).click()
  await page.getByRole('button', { name: 'Restore saved draft', exact: true }).click()
  await expect(mode).toHaveValue('custom')
  await expect(bindings.getByRole('checkbox', { name: 'Elastic metrics (elastic)', exact: true })).toBeChecked()
  await expect(bindings.getByRole('checkbox', { name: 'OpenSearch logs (elastic)', exact: true })).toBeChecked()
  await expect(bindings.getByRole('checkbox', { name: 'Require Elastic metrics readiness and complete evidence', exact: true })).toBeChecked()
  await expect(rules.getByRole('combobox', { name: 'Criterion 1 statistic', exact: true })).toHaveValue('value')
  expect((await bindings.boundingBox())?.width).toBeGreaterThan(400)
  await bindings.screenshot({ path: testInfo.outputPath('elastic-test-bindings.png') })
})

test('Elastic live and archived evidence preserves canonical paging, truncation and partial traces', async ({ page }, testInfo) => {
  test.setTimeout(120000)
  const request = page.context().request
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  const run = await startLiveRun(request, process.env.APEXLOAD_E2E_BASE_URL, { label: 'elastic-evidence', duration: '2m' })
  const start = new Date().toISOString()
  const selection = { indices: ['logs-*', 'traces-*'], logs: true, traces: true, metrics: [{ name: 'duration', field: 'event.duration', statistic: 'average', unit: 'ns', bucket_seconds: 10 }] }
  const span = { id: 'span-document', provider: 'elasticsearch', kind: 'span', query_id: 'spans', timestamp_utc: start, service: 'checkout', correlation: 'run_id', span: { trace_id: 'trace-1', span_id: 'span-1', parent_span_id: 'missing-parent', operation: 'Checkout retained span', duration_ms: 12.5 } }
  const log = { id: 'log-document', provider: 'elasticsearch', kind: 'log', query_id: 'logs', timestamp_utc: start, service: 'checkout', correlation: 'service_time', log: { message: 'Retained checkout log' } }
  const points = [{ source: 'elastic-source', timestamp_utc: start, metric_name: 'duration', unit: 'ms', value: 12.5, quality: 'valid', provider: { name: 'elasticsearch', metric_id: 'duration', window_kind: 'bucket', statistic: 'average', bucket_start: start, bucket_end: new Date(Date.parse(start) + 10000).toISOString(), resolution_seconds: 10, settled: false, aggregation_eligible: false } }]
  try {
    await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, diagnostics_enabled: false } }))
    await page.route(`**/api/v1/tests/${run.testId}/monitors`, route => route.fulfill({ json: [{ id: `${run.testId}:elastic-source`, source: { id: 'elastic-source', name: 'Checkout Elastic', revision: 2, pack: 'elastic', executor_mode: 'direct', elastic: selection }, state: 'reconciling', next_sequence: 2, coverage: { logs: { status: 'truncated', truncated: true, reason: 'record_limit' }, spans: { status: 'complete' } } }] }))
    await page.route(`**/api/v1/tests/${run.testId}/monitor-evidence?*`, route => {
      const query = new URL(route.request().url()).searchParams
      return route.fulfill({ json: query.get('after_key') ? { evidence: [{ ...log, source_id: 'elastic-source' }], has_more: false, after_key: 'log-document' } : { evidence: [{ ...span, source_id: 'elastic-source' }], has_more: true, after_key: 'span-document' } })
    })
    await page.route(`**/api/v1/tests/${run.testId}/monitor-series?*`, route => { expect(new URL(route.request().url()).searchParams.has('after_key')).toBe(true); return route.fulfill({ json: { points, has_more: false, after_key: 'duration' } }) })
    await page.goto(`/#/test/${run.testId}?tab=overview`)
    const monitor = page.getByRole('region', { name: 'Run monitor evidence', exact: true })
    const evidence = monitor.getByRole('region', { name: 'Elastic run evidence', exact: true })
    await expect(evidence).toContainText('record_limit')
    await expect(evidence).toContainText('partial trace context')
    await expect(evidence).toContainText('Checkout retained span')
    await evidence.getByText('Trace trace-1 · 1 retained spans · partial trace context', { exact: true }).click()
    await expect(evidence).toContainText('not retained on this page')
    await page.screenshot({ path: testInfo.outputPath('elastic-live-trace.png') })
    await evidence.getByRole('button', { name: 'Next Elastic evidence page', exact: true }).click()
    await expect(evidence).toContainText('Retained checkout log')
    await monitor.getByRole('combobox', { name: 'Monitor source', exact: true }).selectOption('elastic-source')
    await monitor.getByLabel('Metric name', { exact: true }).fill('duration')
    await monitor.getByRole('button', { name: 'Refresh monitor overlay', exact: true }).click()
    await expect(monitor).toContainText('1 valid / 1 observed samples')
    expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok()).toBeTruthy()
    await expect.poll(async () => Boolean((await (await request.get(`/api/v1/tests/${run.testId}`)).json()).archive), { timeout: 60000 }).toBe(true)
    await page.route(`**/api/v1/tests/${run.testId}/archive/report*`, async route => {
      const response = await route.fetch(); const report = await response.json()
      report.manifest.monitoring_status = 'incomplete'
      report.manifest.monitor_evidence_file = 'monitor_evidence.v1.jsonl'
      report.manifest.monitor_coverage = [{ job_id: `${run.testId}:elastic-source`, source_id: 'elastic-source', source_revision: 2, connection_revision: 3, binding_revision: 1, required: true, state: 'incomplete', executor_mode: 'direct', elastic: { product: 'elasticsearch', preset: 'ecs', mapping_revision: 'a'.repeat(64), metrics: selection.metrics, queries: [{ id: 'logs', language: 'dsl', signal: 'log', revision: 'b'.repeat(64) }], signals: ['logs', 'spans'] }, provider_coverage: { logs: { status: 'truncated', truncated: true }, spans: { status: 'complete' } } }]
      report.monitor_diagnostics = [{ source_id: 'elastic-source', diagnostic: span }, { source_id: 'elastic-source', diagnostic: log }]
      await route.fulfill({ response, json: report })
    })
    await page.route(`**/api/v1/tests/${run.testId}/archive/files/monitor_evidence.v1.jsonl`, route => route.fulfill({ contentType: 'application/x-ndjson', body: points.map(point => JSON.stringify(point)).join('\n') + '\n' }))
    await page.goto(`/#/report/${run.testId}?section=evidence`)
    const archive = page.getByRole('region', { name: 'Archived Elastic evidence', exact: true }).first()
    await expect(archive).toContainText('Monitoring: incomplete')
    await expect(archive).toContainText('connection revision 3')
    await expect(archive).toContainText('partial trace context')
    await archive.getByText('Trace trace-1 · 1 retained spans · partial trace context', { exact: true }).click()
    await expect(archive).toContainText('not retained on this page')
    await archive.getByRole('button', { name: 'Load archived Elastic timeline', exact: true }).click()
    await expect(archive.getByRole('combobox', { name: 'Archived Elastic metric series', exact: true })).toContainText('duration (ms)')
    await page.screenshot({ path: testInfo.outputPath('elastic-archive-evidence.png') })
  } finally { await abortLiveRun(request, run) }
})

test('AppDynamics connection drafts, saved checks, and discovery preserve credential privacy', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/settings?setting=appdynamics-capture&${scopeQuery(workspace)}`)
  await page.reload()
  const card = page.getByLabel('AppDynamics connections', { exact: true })
  await card.getByRole('button', { name: 'Add AppDynamics connection' }).click()
  const form = card.getByLabel('AppDynamics connection editor', { exact: true })
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow AppDynamics')
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  await expect(card).toContainText('Workflow AppDynamics')
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await form.getByLabel('Controller HTTPS URL', { exact: true }).fill('https://workflow.saas.appdynamics.com')
  await form.getByLabel('Account name', { exact: true }).fill('workflow')
  await form.getByLabel('OAuth client ID secret reference', { exact: true }).fill('workflow-appd-client-id')
  await form.getByLabel('OAuth client secret reference', { exact: true }).fill('workflow-appd-client-secret')
  await expect(form.getByRole('checkbox', { name: 'Enable connection', exact: true })).toBeDisabled()
  const updated = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname.includes('/monitoring/appdynamics-connections/'))
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  const updateResponse = await updated
  expect(updateResponse.ok()).toBeTruthy()
  const body = await updateResponse.text()
  expect(body).not.toContain('workflow-appd-client-id')
  expect(body).not.toContain('workflow-appd-client-secret')
  const connection = JSON.parse(body) as { id: string }
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(form.getByLabel('OAuth client ID secret reference', { exact: true })).toHaveValue('')
  await form.getByRole('button', { name: 'Discard draft', exact: true }).click()
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/${connection.id}/test`, route => route.fulfill({ status: 202, json: { id: 'fixture-check', kind: 'test', status: 'pending', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 60000).toISOString() } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/${connection.id}/operations/fixture-check`, route => route.fulfill({ json: { id: 'fixture-check', kind: 'test', status: 'complete', result: { capabilities: { metrics: true, analytics: false } } } }))
  await card.getByRole('button', { name: 'Test saved connection', exact: true }).click()
  await expect(card.getByLabel('AppDynamics operation')).toContainText('metrics: supported')
  await expect(card.getByLabel('AppDynamics operation')).toContainText('analytics: unavailable')
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/${connection.id}/applications?*`, route => route.fulfill({ status: 202, json: { id: 'fixture-discovery', kind: 'discover', status: 'pending' } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/${connection.id}/operations/fixture-discovery`, route => route.fulfill({ json: { id: 'fixture-discovery', kind: 'discover', status: 'complete', result: { entities: [{ id: '123', name: 'Checkout', type: 'application' }] } } }))
  await card.getByRole('button', { name: 'Discover entities', exact: true }).click()
  await card.getByLabel('AppDynamics discovery').getByRole('button', { name: 'Discover', exact: true }).click()
  await expect(card.getByLabel('AppDynamics operation')).toContainText('Checkout')
  await expect(card.getByLabel('AppDynamics operation')).toContainText('123')
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await card.getByRole('button', { name: 'Keep connection', exact: true }).click()
  await expect(card).toContainText('Workflow AppDynamics')
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  await expect(card).toContainText('No AppDynamics connections are configured')
})

test('AppDynamics source editing preserves typed selections for direct and collector collection', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  const sourcePath = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  let sources: Record<string, unknown>[] = []
  await page.route('**/api/v1/analysis/capabilities?*', async route => {
    const response = await route.fetch()
    await route.fulfill({ json: { ...await response.json(), monitoring_enabled: true } })
  })
  await page.route(`**${sourcePath}**`, async route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: sources })
    const source = route.request().postDataJSON() as Record<string, unknown>
    sources = [{ ...source, id: 'workflow-appd-source', revision: 1 }]
    return route.fulfill({ json: sources[0] })
  })
  await page.route(`**/api/v1/monitoring/collectors?*`, route => route.fulfill({ json: { collectors: [{ id: 'workflow-collector', name: 'Private AppDynamics collector', health: 'healthy' }] } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections`, route => route.fulfill({ json: { connections: [{ id: 'workflow-appd', name: 'Verified AppDynamics', enabled: true, revision: 1, verified_revision: 1, collection_mode: 'direct', config: { endpoint: 'https://workflow.saas.appdynamics.com', account_name: 'workflow', auth_type: 'oauth' } }, { id: 'workflow-appd-private', name: 'Verified private AppDynamics', enabled: true, revision: 1, verified_revision: 1, collection_mode: 'collector', collector_agent_id: 'workflow-collector', config: { endpoint: 'https://workflow.private.appdynamics.com', account_name: 'workflow', auth_type: 'oauth' } }] } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/workflow-appd/metrics?*`, route => route.fulfill({ status: 202, json: { id: 'fixture-presets', status: 'pending' } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitoring/appdynamics-connections/workflow-appd/operations/fixture-presets`, route => route.fulfill({ json: { id: 'fixture-presets', status: 'complete', result: { entities: [{ id: '9', name: 'Average Response Time (ms)', type: 'leaf', path: 'Overall Application Performance|Average Response Time (ms)' }] } } }))
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sourceForm = page.getByRole('region', { name: 'Run monitor sources', exact: true })
  await sourceForm.getByRole('combobox', { name: 'Pack', exact: true }).selectOption('appdynamics')
  await sourceForm.getByLabel('Name', { exact: true }).fill('Checkout APM')
  await sourceForm.getByRole('combobox', { name: 'AppDynamics connection', exact: true }).selectOption('workflow-appd')
  await expect(sourceForm.getByRole('combobox', { name: 'Collector', exact: true })).toBeDisabled()
  await sourceForm.getByLabel('Application IDs (comma separated)', { exact: true }).fill('123')
  await sourceForm.getByRole('button', { name: 'Discover available preset metrics', exact: true }).click()
  await sourceForm.getByRole('button', { name: 'Add Average Response Time (ms)', exact: true }).click()
  await expect(sourceForm.getByLabel('Metric 1 name', { exact: true })).toHaveValue('response_time')
  await expect(sourceForm.getByLabel('Metric 1 path', { exact: true })).toHaveValue('Overall Application Performance|Average Response Time (ms)')
  await expect(sourceForm.getByLabel('Metric 1 unit', { exact: true })).toHaveValue('ms')
  await sourceForm.getByRole('combobox', { name: 'Metric 1 source statistic', exact: true }).selectOption('average')
  await sourceForm.getByRole('button', { name: 'Add Analytics query', exact: true }).click()
  await sourceForm.getByLabel('Analytics 1 name', { exact: true }).fill('checkout_count')
  await sourceForm.getByLabel('Analytics 1 application ID field (alternative)', { exact: true }).fill('applicationId')
  await sourceForm.getByLabel('Analytics 1 read query', { exact: true }).fill('SELECT COUNT(*) AS value FROM transactions')
  const saved = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === sourcePath)
  await sourceForm.getByRole('button', { name: 'Save monitor source', exact: true }).click()
  const directPayload = (await saved).postDataJSON()
  expect(directPayload).toMatchObject({ executor_mode: 'direct', connection_id: 'workflow-appd', interval_seconds: 60, appdynamics: { applications: ['123'], metrics: [{ application_id: '123', name: 'response_time', unit: 'ms', statistic: 'average' }], analytics: [{ application_id: '123', name: 'checkout_count', kind: 'transaction', statistic: 'value' }] } })
  await sourceForm.getByRole('button', { name: 'Edit Checkout APM', exact: true }).click()
  await sourceForm.getByRole('combobox', { name: 'AppDynamics connection', exact: true }).selectOption('workflow-appd-private')
  await expect(sourceForm.getByRole('combobox', { name: 'Collection location', exact: true })).toHaveValue('collector')
  await expect(sourceForm.getByRole('combobox', { name: 'Collector', exact: true })).toHaveValue('workflow-collector')
  const updated = page.waitForRequest(request => request.method() === 'PUT' && new URL(request.url()).pathname === `${sourcePath}/workflow-appd-source`)
  await sourceForm.getByRole('button', { name: 'Save monitor source', exact: true }).click()
  expect((await updated).postDataJSON()).toMatchObject({ executor_mode: 'collector', collector_id: 'workflow-collector', appdynamics: { metrics: [{ name: 'response_time' }], analytics: [{ application_id: '123' }] } })
})

test('AppDynamics live and archived evidence preserve delayed state, units and correlation', async ({ page }) => {
  test.setTimeout(120000)
  const request = page.context().request
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  const run = await startLiveRun(request, process.env.APEXLOAD_E2E_BASE_URL, { label: 'appd-evidence', duration: '2m' })
  const start = new Date(Math.floor(Date.now() / 60000) * 60000).toISOString()
  const bucket = (offset: number, value: number) => {
    const timestamp = new Date(Date.parse(start) + offset * 60000).toISOString()
    return { source: 'apm', run_id: run.testId, metric_name: 'response_time', unit: 'ms', kind: 'gauge', quality: 'valid', value, dimensions: { node: '5' }, timestamp_utc: timestamp, provider: { name: 'appdynamics', metric_id: '9', entity_ids: { application: '123', node: '5' }, bucket_start: timestamp, bucket_end: new Date(Date.parse(timestamp) + 60000).toISOString(), resolution_seconds: 60, statistic: 'value', settled: true, aggregation_eligible: true } }
  }
  const secondNode = bucket(0, 10)
  const points = [bucket(0, 0), bucket(3, 25), { ...secondNode, dimensions: { node: '6' }, provider: { ...secondNode.provider, metric_id: '10', entity_ids: { application: '123', node: '6' } } }]
  const selection = { applications: ['123'], metrics: [{ application_id: '123', name: 'response_time', path: 'Overall Application Performance|Average Response Time (ms)', unit: 'ms', statistic: 'value' }], events: true, violations: true, snapshots: true, analytics: [], settle_seconds: 120 }
  const evidence = { id: 'snapshot-7', kind: 'snapshot', timestamp_utc: start, summary: 'Checkout snapshot', severity: 'ERROR', application_id: '123', entity_ids: { tier: '5' }, deep_link: 'https://controller.example.com/controller/#/snapshot/7', properties: { correlation: 'run_header', requestguid: 'appd-request-guid' } }
  try {
    await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, diagnostics_enabled: false } }))
    await page.route(`**/api/v1/tests/${run.testId}/monitors`, route => route.fulfill({ json: [{ id: `${run.testId}:apm`, source: { id: 'apm', name: 'Checkout APM', pack: 'appdynamics', appdynamics: selection }, state: 'reconciling', next_sequence: 2, drain_deadline: new Date(Date.now() + 300000).toISOString(), coverage: { metrics: { status: 'partial', message: 'Waiting for delayed buckets' } }, topology: [{ id: '5', type: 'node', name: 'Checkout node', entity_ids: { application: '123' } }] }] }))
    await page.route(`**/api/v1/tests/${run.testId}/monitor-evidence?*`, route => route.fulfill({ json: { evidence: [evidence], has_more: false, after_key: 'snapshot-7' } }))
    await page.route(`**/api/v1/tests/${run.testId}/monitor-series?*`, route => route.fulfill({ json: { points, has_more: false, after_key: 'bucket-3' } }))
    await page.goto(`/#/test/${run.testId}?tab=overview`)
    const monitor = page.getByRole('region', { name: 'Run monitor evidence', exact: true })
    await expect(monitor).toContainText('Load finished; collecting delayed AppDynamics data')
    await expect(monitor).toContainText('Final collection deadline:')
    await expect(monitor.getByRole('link', { name: 'Open in AppDynamics' })).toHaveAttribute('href', evidence.deep_link)
    await expect(monitor).toContainText('Request GUID: appd-request-guid')
    await monitor.getByRole('combobox', { name: 'Monitor source', exact: true }).selectOption('apm')
    await monitor.getByLabel('Metric name', { exact: true }).fill('response_time')
    await monitor.getByRole('button', { name: 'Refresh monitor overlay', exact: true }).click()
    await expect(monitor).toContainText('2 valid / 2 observed samples')
    await expect(monitor.getByRole('combobox', { name: 'Observed monitor series' })).toContainText('Checkout node (5)')
    await monitor.getByRole('combobox', { name: 'Observed monitor series' }).selectOption({ index: 1 })
    await expect(monitor).toContainText('1 valid / 1 observed samples')
    await monitor.getByRole('combobox', { name: 'Load metric overlay', exact: true }).selectOption('tps')
    await expect(monitor.getByRole('combobox', { name: 'Load metric overlay', exact: true })).toHaveValue('tps')
    expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok()).toBeTruthy()
    await expect.poll(async () => Boolean((await (await request.get(`/api/v1/tests/${run.testId}`)).json()).archive), { timeout: 60000 }).toBe(true)
    await page.route(`**/api/v1/tests/${run.testId}/archive/report*`, async route => {
      const response = await route.fetch(); const report = await response.json()
      report.manifest.monitoring_status = 'incomplete'
      report.manifest.monitor_evidence_file = 'monitor_evidence.v1.jsonl'
      report.manifest.monitor_coverage = [{ job_id: `${run.testId}:apm`, source_id: 'apm', source_revision: 2, binding_revision: 1, required: true, state: 'incomplete', executor_mode: 'direct', appdynamics: selection, provider_coverage: { metrics: { status: 'partial', truncated: true } } }]
      report.monitor_diagnostics = [{ source_id: 'apm', diagnostic: evidence }]
      await route.fulfill({ response, json: report })
    })
    await page.route(`**/api/v1/tests/${run.testId}/archive/files/monitor_evidence.v1.jsonl`, route => route.fulfill({ contentType: 'application/x-ndjson', body: points.map(point => JSON.stringify(point)).join('\n') + '\n' }))
    await page.goto(`/#/report/${run.testId}?section=evidence`)
    const archive = page.getByRole('region', { name: 'Archived AppDynamics evidence', exact: true }).first()
    await expect(archive).toContainText('Monitoring: incomplete')
    await expect(archive).toContainText('metrics: partial · truncated')
    await archive.getByRole('button', { name: 'Load archived metric timeline' }).click()
    await expect(archive.getByRole('combobox', { name: 'Archived monitor series' })).toContainText('response_time (ms)')
    await expect(archive.getByRole('combobox', { name: 'Load metric overlay' })).toBeVisible()
  } finally { await abortLiveRun(request, run) }
})

test('Confluence connection setup follows saved project connection workflow', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/settings?setting=confluence&${scopeQuery(workspace)}`)
  await page.reload()
  const card = page.getByLabel('Confluence connections')
  await expect(card).toBeVisible()
  await card.getByRole('button', { name: 'Add Confluence connection' }).click()
  const editor = card.getByLabel('Confluence connection editor')
  await editor.getByLabel('Name', { exact: true }).fill('Workflow wiki')
  await editor.getByLabel('Confluence Cloud site URL').fill('https://example.atlassian.net')
  await editor.getByLabel('Atlassian account email').fill('writer@example.com')
  await editor.getByLabel('Space ID').fill('42')
  await editor.getByLabel('Parent page ID').fill('100')
  await editor.getByLabel(/API token project-secret reference/).fill('workflow-wiki-token')
  await editor.getByRole('checkbox', { name: 'Enable connection' }).check()
  const created = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/integrations/confluence-connections`)
  await editor.getByRole('button', { name: 'Save connection' }).click()
  const response = await created
  expect(response.ok()).toBeTruthy()
  expect(await response.text()).not.toContain('workflow-wiki-token')
  await expect(card).toContainText('Workflow wiki')
  await card.getByRole('button', { name: 'Edit' }).click()
  await expect(editor.getByLabel(/API token project-secret reference/)).toHaveValue('')
  await editor.getByRole('button', { name: 'Discard draft' }).click()
  await card.getByRole('button', { name: 'Delete' }).click()
  await card.getByRole('button', { name: 'Confirm delete' }).click()
  await expect(card).toContainText('No Confluence connections are configured')
})

test('archived run report publishes to a selected Confluence connection', async ({ page }) => {
  test.setTimeout(120000)
  const request=page.context().request
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport','true'))
  const run=await startLiveRun(request,process.env.APEXLOAD_E2E_BASE_URL,{label:'confluence-report',duration:'2m'})
  try {
    expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok()).toBeTruthy()
    await expect.poll(async () => {
      const response=await request.get(`/api/v1/tests/${run.testId}`)
      const payload=await response.json() as {archive?:unknown}
      return Boolean(payload.archive)
    },{timeout:60000}).toBe(true)
    const publication={test_id:run.testId,project_id:'default',connection_id:'wiki',status:'complete',page_id:'200',page_url:'https://example.atlassian.net/wiki/spaces/42/pages/200',attempts:1,created_at:'2026-09-25T00:00:00Z',updated_at:'2026-09-25T00:00:00Z'}
    let published=false
    await page.route(`**/api/v1/tests/${run.testId}/publish/confluence`,route => {
      if (route.request().method()==='POST') {published=true;return route.fulfill({json:{publication}})}
      return route.fulfill({json:{connections:[{id:'wiki',name:'Workflow wiki',enabled:true}],publications:published?[publication]:[]}})
    })
    await page.goto(`/#/report/${run.testId}`)
    await page.getByRole('button',{name:'Export report'}).click()
    await page.getByText('Publish to Confluence',{exact:true}).first().click()
    await expect(page.getByRole('combobox',{name:'Confluence connection'})).toHaveValue('wiki')
    await page.getByRole('button',{name:'Publish to Confluence'}).click()
    await expect(page.getByRole('link',{name:'Open page'})).toHaveAttribute('href',publication.page_url)
    await expect(page.getByRole('button',{name:'Update Confluence page'})).toBeVisible()
  } finally {await abortLiveRun(request,run)}
})

test('Monitoring preserves repeated keyboard navigation and calendar context', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  // Refresh the workspace inventory after API fixture creation in an already signed-in page.
  await page.reload()
  const controlPlane = page.locator('[data-card-id="system-health"]')
  await expect(controlPlane.locator('.monitor-build-heading')).toBeVisible()
  await expect(controlPlane.locator('.monitor-build-row')).toHaveCount(3)
  await expect(controlPlane.locator('.monitor-build-date')).toHaveCount(3)
  await expect(controlPlane).not.toContainText('0.0.0.0:8080')
  const tabs = page.getByRole('tablist', { name: 'Monitor categories' })
  await tabs.getByRole('tab', { name: 'Overview', exact: true }).focus()
  for (const name of ['Fleet', 'Telemetry', 'Alerts', 'Analysis', 'Schedules']) {
    await page.keyboard.press('ArrowRight')
    await expect(tabs.getByRole('tab', { name, exact: true })).toBeFocused()
  }
  await page.getByRole('button', { name: 'Day', exact: true }).click()
  await page.getByRole('button', { name: 'Cron', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Show queued runs', exact: true }).uncheck()
  await tabs.getByRole('tab', { name: 'Alerts', exact: true }).click()
  const incidentHistory = page.locator('.monitor-incident-history')
  await expect(incidentHistory.getByRole('heading', { name: 'Shared SLA incident history' })).toBeVisible()
  await expect(incidentHistory.getByRole('searchbox', { name: 'Search incidents' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Datasource evidence' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Cron', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('checkbox', { name: 'Show queued runs', exact: true })).not.toBeChecked()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Schedule run', exact: true })).toBeVisible()
})

test('Collectors receive a heartbeat while Monitoring remains open', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const created = await json(await request.post('/api/v1/monitoring/collectors', { data: { project_id: workspace.projectId, name: 'Monitoring refresh acceptance', allowed_metric_packs: ['linux-hosts'] } }))
  const collector = created.collector as { id: string }
  await page.route('**/api/v1/integrations/apm/profiles', async route => {
    const response = await route.fetch()
    const profiles = await response.json()
    profiles.profiles.find((profile: { id: string }) => profile.id === 'opentelemetry').status = 'unavailable'
    await route.fulfill({ response, json: profiles })
  })
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.locator('[data-card-id="apm-integrations"] .monitor-header-badge.danger')).toContainText('1 unavailable')
  const row = page.locator('.pp-row').filter({ hasText: 'Monitoring refresh acceptance' })
  await expect(row).toBeVisible()
  await json(await request.post(`/api/v1/monitoring/collectors/${collector.id}/heartbeat`, { headers: { Authorization: `Bearer ${created.token}` }, data: { packs: [{ pack: 'linux-hosts', samples: { cpu_pct: 17 } }] } }))
  await expect(row).toContainText('healthy', { timeout: 15000 })
  await row.getByRole('button', { name: 'Metrics', exact: true }).click()
  const metrics = page.getByRole('dialog', { name: /Latest captured metrics/ })
  await expect(metrics).toContainText('cpu_pct')
  await expect(metrics.getByRole('button', { name: 'Refresh metrics', exact: true })).toBeVisible()
})

test('legacy ELK connection edits preserve capture defaults and secret privacy', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const created = await request.post(`/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`, { data: { name: 'Workflow legacy Elastic', endpoint: 'https://127.0.0.1:1', enabled: true, auth_type: 'api_key', api_key_secret_name: 'workflow-elastic-api-key', index_pattern: 'workflow-logs-*', timestamp_field: '@timestamp', level_field: 'log.level', message_field: 'message', trace_id_field: 'trace.id', metric_aggregations: [] } })
  expect(created.ok()).toBeTruthy()
  const safeResponse = await created.text()
  expect(safeResponse).not.toContain('workflow-elastic-api-key')
  const connection = JSON.parse(safeResponse) as { id: string; legacy_capture_ready: boolean }
  expect(connection.legacy_capture_ready).toBe(true)
  await page.goto(`/#/settings?setting=elk-capture&${scopeQuery(workspace)}`)
  await page.reload()
  const card = page.getByLabel('ELK connections', { exact: true })
  const form = card.getByLabel('ELK connection editor', { exact: true })
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(form.getByLabel('Elasticsearch / OpenSearch endpoint', { exact: true })).toHaveValue('https://127.0.0.1:1')
  await expect(form.getByLabel('API key project-secret reference', { exact: true })).toHaveValue('')
  await expect(form.getByLabel('Name (required)', { exact: true })).toHaveCSS('min-height', '40px')
  await form.getByRole('button', { name: 'Add aggregation', exact: true }).click()
  const metricName = form.getByRole('textbox', { name: 'Metric 1 name', exact: true })
  await metricName.pressSequentially('duration_avg')
  await expect(metricName).toBeFocused()
  await expect(metricName).toHaveValue('duration_avg')
  await form.getByRole('textbox', { name: 'Metric 1 field', exact: true }).fill('duration_ms')
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow legacy Elastic updated')
  const updated = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname.endsWith(`/elk-connections/${connection.id}`))
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  const update = await updated
  expect(update.ok()).toBeTruthy()
  expect(await update.json()).toMatchObject({ legacy_capture_ready: true, metric_aggregations: [{ name: 'duration_avg', field: 'duration_ms', operation: 'avg' }] })
  await expect(card).toContainText('Workflow legacy Elastic updated')
  const tested = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/test'))
  await card.getByRole('button', { name: 'Test saved connection', exact: true }).click()
  expect((await tested).status()).toBe(502)
  await expect(card).toContainText('ELK connection test failed')
  const deleted = page.waitForResponse(response => response.request().method() === 'DELETE' && new URL(response.url()).pathname.endsWith(`/elk-connections/${connection.id}`))
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  expect((await deleted).status()).toBe(204)
  await expect(card).not.toContainText('Workflow legacy Elastic updated')
})

test('Dynatrace SaaS connection plumbing is manageable without exposing secret references', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('link', { name: 'Dynatrace capture settings', exact: true }).click()
  const card = page.locator('[aria-label="Dynatrace connections"]')
  const form = card.locator('[aria-label="Dynatrace connection editor"]')
  await card.getByRole('button', { name: 'Add Dynatrace connection', exact: true }).click()
  await expect(form).toBeVisible()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Dynatrace')
  await expect(form.getByLabel('Environment URL', { exact: true })).toHaveCount(0)
  await expect(form.getByLabel('Log timestamp field', { exact: true })).toHaveCount(0)
  await form.getByRole('checkbox', { name: 'Problems API', exact: true }).check()
  await form.getByLabel('Environment URL', { exact: true }).fill('https://workflow.live.dynatrace.com')
  await form.getByLabel('Environment token project-secret reference', { exact: true }).fill('workflow-dynatrace-token')
  await form.getByRole('checkbox', { name: 'Problems API', exact: true }).check()
  await form.getByRole('checkbox', { name: 'Enable for new tests', exact: true }).check()
  const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/monitoring/dynatrace-connections`)
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  const createdResponse = await created
  expect(createdResponse.ok()).toBeTruthy()
  expect(await createdResponse.text()).not.toContain('workflow-dynatrace-token')
  await expect(card).toContainText('Workflow Dynatrace')
  await expect(card).toContainText('ready')
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Dynatrace updated')
  const updated = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname.includes('/monitoring/dynatrace-connections/'))
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  expect((await updated).ok()).toBeTruthy()
  await expect(card).toContainText('Workflow Dynatrace updated')
  const deleted = page.waitForResponse(response => response.request().method() === 'DELETE' && new URL(response.url()).pathname.includes('/monitoring/dynatrace-connections/'))
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(card.getByRole('group', { name: 'Confirm connection deletion' })).toBeVisible()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  expect((await deleted).status()).toBe(204)
  await expect(card).not.toContainText('Workflow Dynatrace updated')
})
