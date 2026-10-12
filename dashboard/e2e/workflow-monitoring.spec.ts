import { test, expect, json, login, seedScript, seedScenario, showAccountCard, editScenarioFromRow } from './support/workflow'
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
  await sourceForm.getByLabel('Name (required)', { exact: true }).fill('Checkout Elastic')
  await sourceForm.getByRole('combobox', { name: 'Elastic connection', exact: true }).selectOption('elastic-direct')
  await expect(sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true })).toBeDisabled()
  await sourceForm.getByRole('checkbox', { name: 'Capture logs', exact: true }).uncheck()
  await sourceForm.getByRole('checkbox', { name: 'Capture cluster health', exact: true }).check()
  await sourceForm.getByRole('button', { name: 'Preview Elastic source', exact: true }).click()
  await expect(sourceForm.getByRole('region', { name: 'Elastic inspection result', exact: true })).toContainText('health: available')
  await sourceForm.getByRole('checkbox', { name: 'Capture logs', exact: true }).check()
  await expect(sourceForm.getByRole('region', { name: 'Elastic inspection result', exact: true })).toHaveCount(0)
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
  await expect(sourceForm.getByRole('region', { name: 'Elastic inspection result', exact: true })).toHaveCount(0)
  await expect(sourceForm).toContainText('Assigned collector elastic-agent')
  await expect(sourceForm.getByRole('combobox', { name: /^Collector( \(required\))?$/ })).toHaveValue('elastic-agent')
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
  // Capture options sit under "Monitoring for this run", folded until opened.
  await page.getByText('Monitoring for this run', { exact: true }).click()
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
  const dimensions = rules.getByRole('textbox', { name: 'Criterion 1 entity dimensions', exact: true })
  await dimensions.fill('{"host":')
  await expect(rules.getByRole('alert')).toContainText('before saving or launching')
  await expect(page.getByRole('button', { name: 'Hold for Manual Start', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await expect(dimensions).toBeHidden()
  await page.getByRole('button', { name: 'Run Setup', exact: true }).click()
  await expect(dimensions).toHaveValue('{"host":')
  await expect(rules.getByRole('alert')).toContainText('before saving or launching')
  await expect(page.getByRole('button', { name: 'Hold for Manual Start', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByText('Draft saved in this browser tab. No run or recurring trigger was created.', { exact: true })).toHaveCount(0)
  await dimensions.fill('{"host":"checkout"}')
  await expect(rules.getByRole('alert')).toHaveCount(0)
  const legacy = page.getByRole('combobox', { name: 'Elastic log capture', exact: true })
  await expect(legacy.locator('option')).toHaveText(['Off for this test', 'Legacy capture'])
  await expect(page.getByRole('combobox', { name: 'Dynatrace SaaS capture', exact: true })).toContainText('Ready Dynatrace')
  await page.getByRole('button', { name: 'Schedules', exact: true }).click()
  await page.getByRole('button', { name: 'Run Setup', exact: true }).click()
  await expect(bindings.getByRole('checkbox', { name: 'OpenSearch logs (elastic)', exact: true })).toBeChecked()
  await page.getByRole('navigation').getByRole('button', { name: 'Dashboard', exact: true }).click()
  await page.getByRole('dialog', { name: /^Unsaved changes$/i }).getByRole('button', { name: 'Save changes', exact: true }).click()
  await page.getByRole('navigation').getByRole('button', { name: 'New run', exact: true }).click()
  await page.getByRole('button', { name: 'Restore saved draft', exact: true }).click()
  // The folded section says what the restored draft captures, then opens to it.
  const monitoringSummary = page.locator('details.new-test-monitoring > summary')
  await expect(monitoringSummary).toContainText('2 monitor bindings · 1 monitor rule')
  await monitoringSummary.click()
  await expect(mode).toHaveValue('custom')
  await expect(bindings.getByRole('checkbox', { name: 'Elastic metrics (elastic)', exact: true })).toBeChecked()
  await expect(bindings.getByRole('checkbox', { name: 'OpenSearch logs (elastic)', exact: true })).toBeChecked()
  await expect(bindings.getByRole('checkbox', { name: 'Require Elastic metrics readiness and complete evidence', exact: true })).toBeChecked()
  await expect(rules.getByRole('combobox', { name: 'Criterion 1 statistic', exact: true })).toHaveValue('value')
  expect((await bindings.boundingBox())?.width).toBeGreaterThan(400)
  await bindings.screenshot({ path: testInfo.outputPath('elastic-test-bindings.png') })
})

test('Scenario criteria retain invalid drafts across steps and require correction before saving', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const scenario = await seedScenario(request, workspace, script, 'Criterion workflow')
  const original = await json(await request.get(`/api/v1/scenarios/${scenario}`))
  // Criteria list sources only for a project with run monitoring on.
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true } }))
  await page.route(`**/api/v1/projects/${workspace.projectId}/monitor-sources`, route => route.fulfill({ json: [{ id: 'elastic-metrics', name: 'Elastic criteria', enabled: true, pack: 'elastic', elastic: { metrics: [{ name: 'interval_count', statistic: 'count', unit: 'count', bucket_seconds: 10 }] } }] }))
  const saved: Record<string, unknown>[] = []
  await page.route(`**/api/v1/scenarios/${scenario}`, route => {
    if (route.request().method() !== 'PUT') return route.continue()
    const body = route.request().postDataJSON()
    saved.push(body)
    return route.fulfill({ json: { ...original, ...body, id: scenario } })
  })
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  await page.reload()
  await editScenarioFromRow(page.locator('.scenario-library-table').getByRole('row').filter({ hasText: 'Criterion workflow' }), 'Criterion workflow')
  const steps = page.getByLabel('Scenario builder steps', { exact: true })
  await steps.getByRole('button', { name: /^Data/ }).click()
  const rules = page.getByRole('group', { name: 'Monitoring performance criteria', exact: true })
  await rules.getByRole('button', { name: 'Add monitoring performance criterion', exact: true }).click()
  await rules.getByRole('combobox', { name: 'Criterion 1 source', exact: true }).selectOption('elastic-metrics')
  await rules.getByRole('combobox', { name: 'Criterion 1 metric', exact: true }).selectOption('interval_count')
  const dimensions = rules.getByRole('textbox', { name: 'Criterion 1 entity dimensions', exact: true })
  await dimensions.fill('{')
  await steps.getByRole('button', { name: /^Review/ }).click()
  await expect(dimensions).toBeHidden()
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('before saving or launching')
  expect(saved).toHaveLength(0)
  await steps.getByRole('button', { name: /^Data/ }).click()
  await expect(dimensions).toHaveValue('{')
  await dimensions.fill('{"host":"corrected"}')
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect.poll(() => saved.length).toBe(1)
  expect(saved[0]).toMatchObject({ goal_config: { monitor_rules: [{ dimensions: { host: 'corrected' } }] } })
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
    let artifactReads = 0
    await page.route(`**/api/v1/tests/${run.testId}/archive/files/monitor_evidence.v1.jsonl`, route => ++artifactReads === 1 ? route.fulfill({ status: 503, json: { error: 'temporary outage' } }) : route.fulfill({ contentType: 'application/x-ndjson', body: points.map(point => JSON.stringify(point)).join('\n') + '\n' }))
    await page.goto(`/#/report/${run.testId}?section=evidence`)
    const archive = page.getByRole('region', { name: 'Archived Elastic evidence', exact: true }).first()
    await expect(archive).toContainText('Monitoring: incomplete')
    await expect(archive).toContainText('connection revision 3')
    await expect(archive).toContainText('partial trace context')
    await archive.getByText('Trace trace-1 · 1 retained spans · partial trace context', { exact: true }).click()
    await expect(archive).toContainText('not retained on this page')
    const archivedFrom = archive.getByRole('textbox', { name: 'Archived Elastic from', exact: true })
    await archivedFrom.fill('2026-02-30T10:00:00Z')
    await expect(archive.getByRole('alert')).toContainText('valid UTC RFC3339')
    await archivedFrom.fill('')
    await archive.getByRole('button', { name: 'Load archived Elastic timeline', exact: true }).click()
    await expect(archive.getByRole('alert')).toContainText('503')
    await archive.getByRole('button', { name: 'Load archived Elastic timeline', exact: true }).click()
    await expect(archive.getByRole('combobox', { name: 'Archived Elastic metric series', exact: true })).toContainText('duration (ms)')
    await page.screenshot({ path: testInfo.outputPath('elastic-archive-evidence.png') })
  } finally { await abortLiveRun(request, run) }
})

test('Retired monitoring controls are hidden and inherited bindings can be removed before launch', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  const script = await seedScript(page.context().request, workspace)
  const scenario = await seedScenario(page.context().request, workspace, script)
  await page.context().route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true } }))
  await page.context().route(`**/api/v1/projects/${workspace.projectId}/monitor-sources`, route => route.fulfill({ json: [
    { id: 'retired-source', name: 'Retained source', enabled: true, pack: 'appdynamics', revision: 1 },
  ] }))
  await page.context().route(`**/api/v1/scenarios/${scenario}/monitor-bindings`, route => route.fulfill({ json: { sources: [{ source_id: 'retired-source', required: false }] } }))
  await page.goto(`/#/new-test?scenarioId=${scenario}&${scopeQuery(workspace)}`)
  await page.reload()
  // Capture options sit under "Monitoring for this run", folded until opened.
  await page.getByText('Monitoring for this run', { exact: true }).click()
  const bindings = page.getByRole('group', { name: 'Run monitor selection', exact: true })
  await expect(bindings.getByRole('combobox', { name: 'Monitor binding mode', exact: true })).toHaveValue('inherit')
  await expect(bindings).toContainText('Selected source retired-source retired or unavailable')
  await expect(bindings.getByRole('checkbox', { name: /Retained source/ })).toHaveCount(0)
  await bindings.getByRole('button', { name: 'Remove unavailable source', exact: true }).click()
  await expect(bindings.getByRole('combobox', { name: 'Monitor binding mode', exact: true })).toHaveValue('off')
  await expect(bindings.getByRole('button', { name: 'Remove unavailable source', exact: true })).toHaveCount(0)
  const monitoringPage = await page.context().newPage()
  await monitoringPage.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  await monitoringPage.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sources = monitoringPage.getByRole('region', { name: 'Run monitor sources', exact: true })
  await expect(sources.getByRole('combobox', { name: 'Pack', exact: true }).locator('option[value="appdynamics"]')).toHaveCount(0)
  await expect(monitoringPage.getByRole('link', { name: /AppDynamics/ })).toHaveCount(0)
  const settingsPage = await page.context().newPage()
  await settingsPage.goto(`/#/settings?setting=appdynamics-capture&${scopeQuery(workspace)}`)
  await expect(settingsPage.getByLabel('AppDynamics connections', { exact: true })).toHaveCount(0)
})

test('Retained retired-provider evidence remains readable in generic archive tables and exports', async ({ page }, testInfo) => {
  test.setTimeout(120000)
  const request = page.context().request
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  const run = await startLiveRun(request, process.env.APEXLOAD_E2E_BASE_URL, { label: 'retained-monitor-evidence', duration: '2m' })
  const timestamp = new Date().toISOString()
  const point = { source: 'retired-source', run_id: run.testId, metric_name: 'response_time', unit: 'ms', kind: 'gauge', quality: 'valid', value: 12.5, timestamp_utc: timestamp, provider: { name: 'appdynamics', metric_id: '9', resolution_seconds: 60, statistic: 'value', settled: true, aggregation_eligible: true } }
  try {
    expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok()).toBeTruthy()
    await expect.poll(async () => Boolean((await (await request.get(`/api/v1/tests/${run.testId}`)).json()).archive), { timeout: 60000 }).toBe(true)
    await page.route(`**/api/v1/tests/${run.testId}/archive/report*`, async route => {
      const response = await route.fetch()
      const report = await response.json()
      report.manifest.monitoring_status = 'incomplete'
      report.manifest.monitor_evidence_file = 'monitor_evidence.v1.jsonl'
      report.manifest.monitor_diagnostics_file = 'monitor_diagnostics.v1.json'
      report.manifest.monitor_coverage = [{ job_id: `${run.testId}:retired-source`, source_id: 'retired-source', source_revision: 2, binding_revision: 1, required: false, state: 'incomplete', executor_mode: 'direct', appdynamics: { applications: ['123'] }, provider_coverage: { metrics: { status: 'partial', truncated: true } } }]
      report.monitor_diagnostics = [{ source_id: 'retired-source', diagnostic: { id: 'retained-error', type: 'error', timestamp_utc: timestamp, summary: 'Retained legacy diagnostic', correlation: 'service_time', properties: { request_guid: 'retained-request-guid' }, deep_link: 'https://controller.example.test/evidence/retained-error' } }]
      await route.fulfill({ response, json: report })
    })
    await page.route(`**/api/v1/tests/${run.testId}/archive/files/monitor_evidence.v1.jsonl`, route => route.fulfill({ contentType: 'application/x-ndjson', body: JSON.stringify(point) + '\n' }))
    await page.goto(`/#/report/${run.testId}?section=evidence`)
    const archive = page.getByRole('region', { name: 'Archived AppDynamics evidence', exact: true }).first()
    await expect(archive).toContainText('Retained legacy diagnostic')
    await expect(archive).toContainText('retained-request-guid')
    // A link that opens a new tab says so in its accessible name.
    await expect(archive.getByRole('link', { name: 'Open in AppDynamics (opens in a new tab)', exact: true })).toHaveAttribute('href', 'https://controller.example.test/evidence/retained-error')
    await expect(archive.getByRole('link', { name: 'Download retained monitoring diagnostics', exact: true })).toBeVisible()
    await expect(archive.getByRole('link', { name: 'Download retained monitoring metrics', exact: true })).toBeVisible()
    await archive.getByRole('button', { name: 'Load archived metric timeline', exact: true }).click()
    await expect(archive.getByRole('combobox', { name: 'Archived monitor series', exact: true })).toContainText('response_time (ms)')
    await archive.screenshot({ path: testInfo.outputPath('retained-monitor-evidence.png') })
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
  for (const name of ['Fleet', 'Telemetry', 'Alerts', 'Schedules']) {
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


test('Kafka management authoring retains scope, sealed-secret policy and revision conflicts through reload', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  const path = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  const credentialPin = `v1:${'a'.repeat(64)}`
  let source: Record<string, unknown> | undefined
  let staleUpdate: Record<string, unknown> | undefined
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['kafka-offsets'], messaging_management_version: 1, native_parquet: false } }))
  await page.route('**/api/v1/monitoring/collectors?*', route => route.fulfill({ json: { collectors: [{ id: 'approved-collector', name: 'Approved collector', health: 'healthy' }] } }))
  await page.route(`**${path}**`, route => {
    const request = route.request()
    if (new URL(request.url()).pathname.endsWith('/management-state')) return route.fulfill({ json: { state: 'unavailable' } })
    if (request.method() === 'POST') {
      const draft = request.postDataJSON() as Record<string, unknown>
      expect(draft.credential_ref).toBe('')
      expect(draft.password).toBeUndefined()
      expect(draft.endpoints).toBeUndefined()
      source = { ...draft, id: 'managed-source', revision: 1, management_identity: { ...(draft.management_identity as Record<string, unknown>), credential_version: credentialPin } }
      return route.fulfill({ json: source })
    }
    if (request.method() === 'PUT') {
      staleUpdate = request.postDataJSON() as Record<string, unknown>
      source = { ...source, name: 'Changed by another administrator', revision: 2 }
      return route.fulfill({ status: 409, json: { error: 'monitor source revision changed' } })
    }
    return route.fulfill({ json: source ? [source] : [] })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sources = page.getByRole('region', { name: 'Run monitor sources', exact: true })
  await sources.getByLabel('Name (required)', { exact: true }).fill('Approved Kafka offsets')
  await sources.getByRole('combobox', { name: /^Collector( \(required\))?$/ }).selectOption('approved-collector')
  await sources.getByRole('combobox', { name: 'Pack', exact: true }).selectOption('kafka-offsets')
  await expect(sources.getByRole('button', { name: 'Save monitor source' })).toBeDisabled()
  await sources.getByLabel('Approved destination', { exact: true }).fill('management://orders-cluster')
  await sources.getByLabel('Kafka cluster ID', { exact: true }).fill('exact-broker-cluster-id')
  await sources.getByRole('combobox', { name: 'Consumer isolation', exact: true }).selectOption('read_committed')
  await sources.getByRole('combobox', { name: 'SASL identity', exact: true }).selectOption('sasl_scram')
  await sources.getByLabel('Approved SASL username', { exact: true }).fill('offset-reader')
  await sources.getByLabel('Identity policy revision', { exact: true }).fill('policy-1')
  await sources.getByLabel('Management credential project-secret reference', { exact: true }).fill('offset-reader-secret')
  await sources.getByLabel('Collector X25519 public key', { exact: true }).fill('AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=')
  await sources.getByLabel('Topic 1', { exact: true }).fill('orders')
  await sources.getByLabel('Consumer group 1', { exact: true }).fill('workers')
  await sources.getByRole('button', { name: 'Add Kafka entity' }).click()
  await sources.getByLabel('Topic 2', { exact: true }).fill('orders')
  await sources.getByLabel('Consumer group 2', { exact: true }).fill('workers')
  await expect(sources).toContainText('Remove duplicate')
  await sources.getByLabel('Partition 2', { exact: true }).fill('1')
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await sources.getByRole('button', { name: 'Edit Approved Kafka offsets' }).click()
  await sources.getByText('Saved collector approval · revision 1', { exact: true }).click()
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await expect(sources.getByRole('combobox', { name: 'Consumer isolation', exact: true })).toHaveValue('read_committed')
  await expect(sources.getByLabel('Partition 1', { exact: true })).toHaveValue('0')
  await expect(sources.getByLabel('Partition 2', { exact: true })).toHaveValue('1')
  await sources.getByLabel('Name (required)', { exact: true }).fill('Unsaved stale draft')
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await expect(sources.getByRole('alert')).toContainText('revision changed')
  expect(staleUpdate?.revision).toBe(1)
  await expect(sources.getByLabel('Name (required)', { exact: true })).toHaveValue('Unsaved stale draft')
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await sources.getByRole('button', { name: 'Edit Changed by another administrator' }).click()
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).click()
  await expect(sources.locator('pre')).toContainText('exact-broker-cluster-id')
  await expect(sources.getByLabel('Management credential project-secret reference', { exact: true })).toHaveValue('offset-reader-secret')
  await sources.getByLabel('Kafka cluster ID', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('kafka-management-source.png') })
  await page.setViewportSize({ width: 375, height: 900 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  const approval = sources.getByRole('group', { name: 'Kafka management approval', exact: true })
  await expect.poll(() => approval.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth + 1)).toBe(true)
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await sources.getByLabel('Kafka cluster ID', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('kafka-management-source-narrow.png') })
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('kafka-management-approval-narrow.png') })
})

test('Service Bus authoring preserves explicit queue and subscription approval, revisions and narrow layout', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  const path = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  const credentialPin = `v1:${'a'.repeat(64)}`
  let source: Record<string, unknown> | undefined
  let staleUpdate: Record<string, unknown> | undefined
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['servicebus-runtime'], messaging_management_version: 1, native_parquet: false } }))
  await page.route('**/api/v1/monitoring/collectors?*', route => route.fulfill({ json: { collectors: [{ id: 'approved-collector', name: 'Approved collector', health: 'healthy' }] } }))
  await page.route(`**${path}**`, route => {
    const request = route.request()
    if (new URL(request.url()).pathname.endsWith('/management-state')) return route.fulfill({ json: { state: 'unavailable' } })
    if (request.method() === 'POST') {
      const draft = request.postDataJSON() as Record<string, unknown>
      expect(draft.credential_ref).toBe('')
      expect(draft.password).toBeUndefined()
      expect(draft.endpoints).toBeUndefined()
      expect(draft.connection_string).toBeUndefined()
      expect(draft.interval_seconds).toBe(30)
      expect(draft.management_scope).toEqual({ version: 1, kind: 'servicebus_runtime', provider: 'example.servicebus.windows.net', entities: [{ service_bus: { queue: 'Orders/Nested' } }, { service_bus: { topic: 'Events/Nested', subscription: 'Workers' } }] })
      expect(draft.management_identity).toEqual({ mode: 'shared_key', principal: 'ManageReader', policy_revision: 'policy-1', credential_ref: 'runtime-reader-secret' })
      source = { ...draft, id: 'managed-source', revision: 1, management_identity: { ...(draft.management_identity as Record<string, unknown>), credential_version: credentialPin } }
      return route.fulfill({ json: source })
    }
    if (request.method() === 'PUT') {
      staleUpdate = request.postDataJSON() as Record<string, unknown>
      source = { ...source, name: 'Changed by another administrator', revision: 2 }
      return route.fulfill({ status: 409, json: { error: 'monitor source revision changed' } })
    }
    return route.fulfill({ json: source ? [source] : [] })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sources = page.getByRole('region', { name: 'Run monitor sources', exact: true })
  await expect(sources.getByRole('combobox', { name: 'Pack', exact: true }).locator('option[value="kafka-offsets"]')).toHaveCount(0)
  await sources.getByLabel('Name (required)', { exact: true }).fill('Approved Service Bus counts')
  await sources.getByRole('combobox', { name: /^Collector( \(required\))?$/ }).selectOption('approved-collector')
  await sources.getByRole('combobox', { name: 'Pack', exact: true }).selectOption('servicebus-runtime')
  await expect(sources.getByLabel('Collection interval (seconds)', { exact: true })).toHaveValue('30')
  await expect(sources.getByRole('button', { name: 'Save monitor source' })).toBeDisabled()
  await sources.getByLabel('Approved destination', { exact: true }).fill('management://namespace')
  await sources.getByLabel('Service Bus namespace hostname', { exact: true }).fill('example.servicebus.windows.net')
  await sources.getByLabel('Approved Service Bus Manage policy name', { exact: true }).fill('ManageReader')
  await sources.getByLabel('Identity policy revision', { exact: true }).fill('policy-1')
  await sources.getByLabel('Management credential project-secret reference', { exact: true }).fill('runtime-reader-secret')
  await sources.getByLabel('Collector X25519 public key', { exact: true }).fill('AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=')
  await sources.getByLabel('Queue path 1', { exact: true }).fill('Orders/Nested')
  await sources.getByRole('button', { name: 'Add Service Bus entity', exact: true }).click()
  await sources.getByRole('combobox', { name: 'Entity type 2', exact: true }).selectOption('subscription')
  await expect(sources.getByRole('combobox', { name: 'Entity type 2', exact: true })).toHaveValue('subscription')
  await sources.getByLabel('Topic path 2', { exact: true }).fill('Events/Nested')
  await sources.getByLabel('Subscription name 2', { exact: true }).fill('Workers')
  await sources.getByRole('button', { name: 'Add Service Bus entity', exact: true }).click()
  await sources.getByLabel('Queue path 3', { exact: true }).fill('orders/nested')
  await expect(sources).toContainText('duplicate or case-aliased')
  await expect(sources.getByRole('button', { name: 'Save monitor source' })).toBeDisabled()
  await sources.getByRole('button', { name: 'Remove Service Bus entity 3', exact: true }).click()
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await sources.getByRole('button', { name: 'Edit Approved Service Bus counts' }).click()
  await sources.getByText('Saved collector approval · revision 1', { exact: true }).click()
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await expect(sources.getByLabel('Queue path 1', { exact: true })).toHaveValue('Orders/Nested')
  await expect(sources.getByRole('combobox', { name: 'Entity type 2', exact: true })).toHaveValue('subscription')
  await expect(sources.getByLabel('Topic path 2', { exact: true })).toHaveValue('Events/Nested')
  await expect(sources.getByLabel('Subscription name 2', { exact: true })).toHaveValue('Workers')
  await sources.getByLabel('Management credential project-secret reference', { exact: true }).fill('unsaved-rotation')
  await expect(sources.locator('pre')).not.toContainText('unsaved-rotation')
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await expect(sources.getByRole('alert')).toContainText('revision changed')
  expect(staleUpdate?.revision).toBe(1)
  expect((staleUpdate?.management_identity as Record<string, unknown>).credential_version).toBeUndefined()
  await expect(sources.getByLabel('Management credential project-secret reference', { exact: true })).toHaveValue('unsaved-rotation')
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await sources.getByRole('button', { name: 'Edit Changed by another administrator' }).click()
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).click()
  await expect(sources.getByLabel('Management credential project-secret reference', { exact: true })).toHaveValue('runtime-reader-secret')
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await sources.getByLabel('Service Bus namespace hostname', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('servicebus-management-source.png') })
  await page.setViewportSize({ width: 375, height: 900 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
    await showAccountCard(page)
    const identity = page.locator('.sidebar-identity')
    await expect(identity).toBeVisible()
    await expect.poll(() => identity.evaluate(element => {
      const rail = element.closest('.sidebar')!.getBoundingClientRect()
      const card = element.getBoundingClientRect()
      return card.left >= rail.left && card.right <= rail.right + 1
    })).toBe(true)
    const signOut = identity.getByRole('button', { name: 'Sign out', exact: true })
    await expect(signOut).toBeVisible()
    await expect.poll(() => signOut.evaluate(element => {
      const rail = element.closest('.sidebar')!.getBoundingClientRect()
      const action = element.getBoundingClientRect()
      return action.left >= rail.left && action.right <= rail.right + 1
    })).toBe(true)
    await page.keyboard.press('Escape')
  }
  const approval = sources.getByRole('group', { name: 'Service Bus management approval', exact: true })
  await expect.poll(() => approval.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth + 1)).toBe(true)
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await sources.getByLabel('Service Bus namespace hostname', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('servicebus-management-source-narrow.png') })
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('servicebus-management-approval-narrow.png') })
})

test('Solace authoring preserves explicit VPN queue and topic-endpoint approval, revisions and narrow layout', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  const path = `/api/v1/projects/${workspace.projectId}/monitor-sources`
  const credentialPin = `v1:${'a'.repeat(64)}`
  let source: Record<string, unknown> | undefined
  let staleUpdate: Record<string, unknown> | undefined
  await page.route('**/api/v1/analysis/capabilities?*', route => route.fulfill({ json: { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['solace-semp'], messaging_management_version: 1, native_parquet: false } }))
  await page.route('**/api/v1/monitoring/collectors?*', route => route.fulfill({ json: { collectors: [{ id: 'approved-collector', name: 'Approved collector', health: 'healthy' }] } }))
  await page.route(`**${path}**`, route => {
    const request = route.request()
    if (new URL(request.url()).pathname.endsWith('/management-state')) return route.fulfill({ json: { state: 'unavailable' } })
    if (request.method() === 'POST') {
      const draft = request.postDataJSON() as Record<string, unknown>
      expect(draft.credential_ref).toBe('')
      expect(draft.password).toBeUndefined()
      expect(draft.endpoints).toBeUndefined()
      expect(draft.connection_string).toBeUndefined()
      expect(draft.interval_seconds).toBe(30)
      expect(draft.management_scope).toEqual({ version: 1, kind: 'solace_semp', provider: 'broker.example.com', entities: [{ solace: { vpn: 'primary', queue: 'Orders/Nested' } }, { solace: { vpn: 'primary', topic_endpoint: 'Events/☃' } }] })
      expect(draft.management_identity).toEqual({ mode: 'basic', principal: 'reader', policy_revision: 'policy-1', credential_ref: 'runtime-reader-secret' })
      source = { ...draft, id: 'managed-source', revision: 1, management_identity: { ...(draft.management_identity as Record<string, unknown>), credential_version: credentialPin } }
      return route.fulfill({ json: source })
    }
    if (request.method() === 'PUT') {
      staleUpdate = request.postDataJSON() as Record<string, unknown>
      source = { ...source, name: 'Changed by another administrator', revision: 2 }
      return route.fulfill({ status: 409, json: { error: 'monitor source revision changed' } })
    }
    return route.fulfill({ json: source ? [source] : [] })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  const sources = page.getByRole('region', { name: 'Run monitor sources', exact: true })
  await expect(sources.getByRole('combobox', { name: 'Pack', exact: true }).locator('option[value="kafka-offsets"]')).toHaveCount(0)
  await sources.getByLabel('Name (required)', { exact: true }).fill('Approved Solace depths')
  await sources.getByRole('combobox', { name: /^Collector( \(required\))?$/ }).selectOption('approved-collector')
  await sources.getByRole('combobox', { name: 'Pack', exact: true }).selectOption('solace-semp')
  await expect(sources.getByLabel('Collection interval (seconds)', { exact: true })).toHaveValue('30')
  await expect(sources.getByRole('button', { name: 'Save monitor source' })).toBeDisabled()
  await sources.getByLabel('Approved destination', { exact: true }).fill('management://broker')
  await sources.getByLabel('Solace management hostname', { exact: true }).fill('broker.example.com')
  await sources.getByLabel('Approved Solace management username', { exact: true }).fill('reader')
  await sources.getByLabel('Identity policy revision', { exact: true }).fill('policy-1')
  await sources.getByLabel('Management credential project-secret reference', { exact: true }).fill('runtime-reader-secret')
  await sources.getByLabel('Collector X25519 public key', { exact: true }).fill('AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=')
  await sources.getByLabel('VPN name 1', { exact: true }).fill('primary')
  await sources.getByLabel('Queue name 1', { exact: true }).fill('Orders/Nested')
  await sources.getByRole('button', { name: 'Add Solace entity', exact: true }).click()
  await sources.getByRole('combobox', { name: 'Entity type 2', exact: true }).selectOption('topic_endpoint')
  await expect(sources.getByRole('combobox', { name: 'Entity type 2', exact: true })).toHaveValue('topic_endpoint')
  await sources.getByLabel('VPN name 2', { exact: true }).fill('primary')
  await sources.getByLabel('Topic endpoint name 2', { exact: true }).fill('Events/☃')
  await sources.getByRole('button', { name: 'Add Solace entity', exact: true }).click()
  await sources.getByLabel('VPN name 3', { exact: true }).fill('primary')
  await sources.getByLabel('Queue name 3', { exact: true }).fill('Orders/Nested')
  await expect(sources).toContainText('duplicate Solace')
  await expect(sources.getByRole('button', { name: 'Save monitor source' })).toBeDisabled()
  await sources.getByRole('button', { name: 'Remove Solace entity 3', exact: true }).click()
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await sources.getByRole('button', { name: 'Edit Approved Solace depths' }).click()
  await sources.getByText('Saved collector approval · revision 1', { exact: true }).click()
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await expect(sources.getByLabel('VPN name 1', { exact: true })).toHaveValue('primary')
  await expect(sources.getByLabel('Queue name 1', { exact: true })).toHaveValue('Orders/Nested')
  await expect(sources.getByRole('combobox', { name: 'Entity type 2', exact: true })).toHaveValue('topic_endpoint')
  await expect(sources.getByLabel('VPN name 2', { exact: true })).toHaveValue('primary')
  await expect(sources.getByLabel('Topic endpoint name 2', { exact: true })).toHaveValue('Events/☃')
  await sources.getByLabel('Management credential project-secret reference', { exact: true }).fill('unsaved-rotation')
  await expect(sources.locator('pre')).not.toContainText('unsaved-rotation')
  await sources.getByRole('button', { name: 'Save monitor source' }).click()
  await expect(sources.getByRole('alert')).toContainText('revision changed')
  expect(staleUpdate?.revision).toBe(1)
  expect((staleUpdate?.management_identity as Record<string, unknown>).credential_version).toBeUndefined()
  await expect(sources.getByLabel('Management credential project-secret reference', { exact: true })).toHaveValue('unsaved-rotation')
  await page.reload()
  await page.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await sources.getByRole('button', { name: 'Edit Changed by another administrator' }).click()
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).click()
  await expect(sources.getByLabel('Management credential project-secret reference', { exact: true })).toHaveValue('runtime-reader-secret')
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await sources.getByLabel('Solace management hostname', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('solace-management-source.png') })
  await page.setViewportSize({ width: 375, height: 900 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
    await showAccountCard(page)
    const identity = page.locator('.sidebar-identity')
    await expect(identity).toBeVisible()
    await expect.poll(() => identity.evaluate(element => {
      const rail = element.closest('.sidebar')!.getBoundingClientRect()
      const card = element.getBoundingClientRect()
      return card.left >= rail.left && card.right <= rail.right + 1
    })).toBe(true)
    const signOut = identity.getByRole('button', { name: 'Sign out', exact: true })
    await expect(signOut).toBeVisible()
    await expect.poll(() => signOut.evaluate(element => {
      const rail = element.closest('.sidebar')!.getBoundingClientRect()
      const action = element.getBoundingClientRect()
      return action.left >= rail.left && action.right <= rail.right + 1
    })).toBe(true)
    await page.keyboard.press('Escape')
  }
  const approval = sources.getByRole('group', { name: 'Solace management approval', exact: true })
  await expect.poll(() => approval.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth + 1)).toBe(true)
  await expect(sources.locator('pre')).toContainText(credentialPin)
  await sources.getByLabel('Solace management hostname', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('solace-management-source-narrow.png') })
  await sources.getByText('Saved collector approval · revision 2', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: test.info().outputPath('solace-management-approval-narrow.png') })
})
