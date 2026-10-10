import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { rcaTestReportView, rcaTestResponse, rcaTestSummary } from '../src/components/rca/rcaTestFixture'

// The run is real and authorized; its archive and root-cause analyses are
// served from fixtures. The engine and analysis service are exercised against
// real archives in the Go tests.
test('root-cause investigation opens from Summary, drills into a hypothesis and reopens from a shared link', async ({ page, browser }, testInfo) => {
  test.setTimeout(90000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const runId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Checkout soak', duration: '10m', groups: [{ name: 'checkout', script_id: scriptId, vusers: 20 }] } })))

  const start = Date.parse('2026-10-05T12:00:00Z')
  const summaries = Array.from({ length: 60 }, (_, i) => {
    const at = start + (i + 1) * 10_000
    const slow = at >= Date.parse('2026-10-05T12:03:20Z')
    return { timestamp: new Date(at).toISOString(), test_id: runId, test_name: 'Checkout soak', active_vusers: at < Date.parse('2026-10-05T12:02:00Z') ? 10 : 20, tps: 20, avg_response_ms: slow ? 900 : 300, error_pct: slow ? 1 : 0, total_transactions: 200, total_errors: slow ? 2 : 0, p50_ms: slow ? 800 : 250, p90_ms: slow ? 2500 : 400, p95_ms: slow ? 3100 : 450, p99_ms: slow ? 3900 : 600 }
  })
  const exemplar = (timestamp: string, trace: string) => ({ population: 'transaction', name: 'Checkout', interval_start: timestamp, interval_end: timestamp, reason: 'slowest', trace_id: trace, duration_ms: 3100, success: false, status_code: 503, timestamp, links: [{ provider: 'dynatrace', connection_id: 'dt-1', url: `https://dt.example.test/trace/${trace}` }] })
  const exemplarQueries: string[] = []
  let lookupBody: { mapping_id: string; values: string[] } | undefined
  let approved: unknown
  const mapping = { id: 'map-1', project_id: workspace.projectId, kind: 'business_key_mapping', name: 'Order ID', revision: 1, deleted: false, definition: { extraction: 'order_id', source_id: 'elastic-1', field: 'order.id', label: 'Order ID' } }
  await page.route('**/api/v1/analysis/business-key-mappings**', async route => {
    if (route.request().method() === 'POST') {
      approved = route.request().postDataJSON()
      return route.fulfill({ json: { ...mapping, id: 'map-2' } })
    }
    return route.fulfill({ json: [mapping] })
  })
  await page.route('**/api/v1/projects/*/monitor-sources', route => route.fulfill({ json: [{ id: 'elastic-1', name: 'Order logs', pack: 'elastic-query', revision: 1, collector_id: '', interval_seconds: 30, enabled: true }] }))
  let requested: unknown
  let rca2Polls = 0
  await page.route('**/api/v1/tests**', async route => {
    const url = new URL(route.request().url()); const path = url.pathname; const method = route.request().method()
    const base = `/api/v1/tests/${runId}`
    if (path === base && method === 'GET') {
      const response = await route.fetch(); const payload = await response.json()
      return route.fulfill({ response, json: { ...payload, status: 'COMPLETE', created_at: '2026-10-05T11:59:00Z', started_at: '2026-10-05T12:00:00Z', ended_at: '2026-10-05T12:10:00Z',
        archive: { test_id: runId, schema_version: '1', archive_path: '/archive', manifest_path: '/archive/manifest.json', transaction_count: 12000, resource_sample_count: 0, interval_summary_count: summaries.length, created_at: '2026-10-05T12:10:00Z', completed_at: '2026-10-05T12:10:01Z' } } })
    }
    if (path === `${base}/archive/report`) return route.fulfill({ json: {
      manifest: { test_id: runId, test_name: 'Checkout soak', status: 'COMPLETE', phase_events_file: 'phase_events.jsonl', provenance: { total_vusers: 20, duration: '10m', started_at: '2026-10-05T12:00:00Z' } },
      overview: { transactions: 12000, peak_active_vusers: 20 }, metric_populations: { transaction: { transactions: 12000, errors: 70, avg_response_ms: 600, p95_ms: 3100 } },
      by_action: {}, by_transaction: { Checkout: { transactions: 12000, errors: 70, error_pct: 0.58, avg_response_ms: 600, min_response_ms: 90, max_response_ms: 5200, p95_ms: 3100 } },
      error_patterns: [
        { signature: 'checkout-503', sample: 'HTTP 503 from /cart/checkout', count: 70, first_seen: '2026-10-05T12:03:40Z', latest_seen: '2026-10-05T12:09:50Z', actions: ['Checkout'] },
        { signature: 'login-401', sample: 'HTTP 401 from /login', count: 3, first_seen: '2026-10-05T12:03:40Z', latest_seen: '2026-10-05T12:04:10Z', actions: ['Login'] },
      ],
      completeness: { score_pct: 100 }, verdict: { status: 'fail' },
      root_cause_view: rcaTestReportView(),
      root_cause_summary: rcaTestSummary({ origin: 'requested' }),
    } })
    if (path === `${base}/archive/summaries`) return route.fulfill({ json: { summaries, total: summaries.length } })
    if (path === `${base}/archive/resources`) return route.fulfill({ json: { samples: [], total: 0 } })
    if (path === `${base}/archive/transactions`) return route.fulfill({ json: { transactions: [], total: 0 } })
    if (path === `${base}/archive/analysis`) return route.fulfill({ status: 404, json: { error: 'no analysis' } })
    // The composer's server-rendered preview; its sections are checked in Go.
    if (path === `${base}/report`) return route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>Report</title><p>Rendered report</p>' })
    if (path === `${base}/archive/files/phase_events.jsonl`) return route.fulfill({ contentType: 'application/x-ndjson', body: [
      { schema: 'apexload.phase_event.v1', kind: 'ramp_start', level: 20, target: 'checkout', at: '2026-10-05T12:01:40Z' },
      { schema: 'apexload.phase_event.v1', kind: 'level_reached', level: 20, target: 'checkout', at: '2026-10-05T12:02:00Z', deliveries: [{ sink: 'grafana', format: 'grafana', status: 'delivered' }] },
    ].map(event => JSON.stringify(event)).join('\n') + '\n' })
    if (path === `${base}/archive/root-cause/v2` || path === `${base}/archive/root-cause/analyses/rca-1`) return route.fulfill({ json: rcaTestResponse() })
    if (path === `${base}/archive/root-cause/analyses` && method === 'POST') {
      requested = route.request().postDataJSON()
      return route.fulfill({ status: 202, json: { analysis_id: 'rca-2', revision: 3, state: 'queued', created_at: '2026-10-05T12:20:00Z', presented: true } })
    }
    if (path === `${base}/archive/root-cause/analyses/rca-2`) {
      rca2Polls++
      return route.fulfill({ json: rca2Polls < 2 ? { analysis_id: 'rca-2', revision: 3, state: 'running', presented: true } : rcaTestResponse({ analysis_id: 'rca-2', revision: 3, completed_at: '2026-10-05T12:20:04Z' }) })
    }
    if (path === `${base}/archive/business-keys/lookup` && method === 'POST') {
      lookupBody = route.request().postDataJSON()
      return route.fulfill({ json: {
        mapping: { id: 'map-1', revision: 1, label: 'Order ID', extraction: 'order_id', source_id: 'elastic-1', field: 'order.id' },
        results: [
          { index: 0, requests: { total: 2, matches: [{ timestamp: '2026-10-05T12:04:00Z', group: 'checkout', name: 'POST /pay', duration_ms: 3100, success: false, status_code: 503, trace_id: 'trace-in-window', matched_by: 'digest' }] },
            logs: { status: 'found', total: 1, entries: [{ timestamp: '2026-10-05T12:04:01Z', trace_id: 'trace-in-window', message: 'payment gateway timeout' }] } },
          { index: 1, requests: { total: 0, matches: [] }, logs: { status: 'not_found', total: 0, entries: [] } },
        ],
        limitations: [],
      } })
    }
    if (path === `${base}/archive/exemplars`) {
      // The controller filters by name, population and window.
      exemplarQueries.push(url.search)
      const from = Date.parse(url.searchParams.get('from') || '') || -Infinity
      const to = Date.parse(url.searchParams.get('to') || '') || Infinity
      const matching = [exemplar('2026-10-05T12:04:00Z', 'trace-in-window'), exemplar('2026-10-05T12:09:30Z', 'trace-late')]
        .filter(e => e.name === url.searchParams.get('name') && e.population === url.searchParams.get('population') && Date.parse(e.timestamp) >= from && Date.parse(e.timestamp) <= to)
      return route.fulfill({ json: { exemplars: matching, total: matching.length } })
    }
    return route.continue()
  })

  // Summary's Run outcome names the leading hypothesis and opens its
  // investigation.
  await page.goto(`/#/report/${runId}?${scopeQuery(workspace)}`)
  await page.reload()
  const outcome = page.getByRole('region', { name: 'Run outcome', exact: true })
  await expect(outcome).toContainText('#1 Resource pressure (medium confidence)')
  await outcome.getByRole('button', { name: 'Investigate root cause', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Diagnostics', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(page).toHaveURL(/section=diagnostics/)
  await expect(page).toHaveURL(/analysis=rca-1/)
  await expect(page).toHaveURL(/rev=2/)
  await expect(page).toHaveURL(/hypothesis=hypothesis-cpu/)
  await expect.poll(() => decodeURIComponent(page.url())).toContain('window=2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z')

  // Diagnostics shows acceptance, validity, the ranked hypotheses, the
  // contradictions and what would disprove each.
  const panel = page.getByRole('region', { name: 'Root cause', exact: true })
  await expect(panel.getByText('Supported hypotheses', { exact: true })).toBeVisible()
  await expect(panel.getByText('Validity unknown', { exact: true })).toBeVisible()
  const first = panel.getByRole('listitem', { name: 'Hypothesis 1: Resource pressure' })
  await expect(first).toContainText('Would disprove it: CPU stays below saturation while latency rises')
  await expect(first).toContainText('Contradicting evidence: None found.')
  await expect(panel.getByRole('listitem', { name: 'Hypothesis 2: Garbage collection' })).toContainText('jvm_gc_seconds_rate')

  // The drill-down: one timeline with onsets, uncertainty and the load step,
  // the transaction population, exemplars in the window, cause evidence and
  // matching errors.
  const investigation = panel.getByRole('region', { name: 'Investigation: Resource pressure' })
  await expect(investigation.locator('.recharts-surface').first()).toBeVisible()
  await expect(investigation).toContainText('Symptom: Checkout · p95 latency rose +300 ms from 2026-10-05 12:03:20 UTC ± 10 s')
  await expect(investigation).toContainText('Cause: builtin:service.cpu.time · Dynatrace rose +60 pts from 2026-10-05 12:02:30 UTC ± 10 s')
  await expect(investigation).toContainText('checkout began ramping to 20 VUsers at 2026-10-05 12:01:40 UTC')
  await expect(investigation).toContainText('checkout reached 20 VUsers at 2026-10-05 12:02:00 UTC')
  await expect(investigation).not.toContainText('inferred from its steady load levels')
  await expect(investigation.getByRole('region', { name: 'Transaction populations' })).toContainText('12,000')
  const exemplars = investigation.getByRole('region', { name: 'Exemplars' })
  await expect(exemplars.getByRole('link', { name: 'Open in Dynatrace' })).toHaveAttribute('href', 'https://dt.example.test/trace/trace-in-window')
  await expect(exemplars).not.toContainText('trace-late')
  expect(new URLSearchParams(exemplarQueries[0]).get('from')).toBe('2026-10-05T12:01:25.000Z')
  expect(new URLSearchParams(exemplarQueries[0]).get('population')).toBe('transaction')
  await expect(investigation).toContainText('Suspect run from 2026-10-05 12:04:30 UTC to 2026-10-05 12:04:40 UTC')
  await expect(investigation.getByRole('region', { name: 'Cause evidence' })).toContainText('Components: cart-service')
  const errors = investigation.getByRole('region', { name: 'Errors' })
  await expect(errors).toContainText('HTTP 503 from /cart/checkout')
  await expect(errors).not.toContainText('HTTP 401 from /login')
  const viewport = page.viewportSize()
  await page.setViewportSize({ width: 1280, height: 2600 })
  await investigation.scrollIntoViewIfNeeded()
  await investigation.screenshot({ path: testInfo.outputPath('rca-investigation.png') })
  if (viewport) await page.setViewportSize(viewport)

  // Widening to the whole run brings the later exemplar in; the window lives
  // in the URL.
  await investigation.getByRole('button', { name: 'Whole run', exact: true }).click()
  await expect(page).not.toHaveURL(/window=/)
  await expect(exemplars).toContainText('trace-late')
  await investigation.getByRole('button', { name: 'Focus cause: builtin:service.cpu.time · Dynatrace', exact: true }).click()
  await expect.poll(() => decodeURIComponent(page.url())).toContain('window=2026-10-05T12:02:20.000Z~2026-10-05T12:06:00.000Z')
  const shared = page.url()

  // A shared link reopens the same analysis, hypothesis and window, without
  // reading whichever analysis is now the latest.
  const other = await browser.newContext({ storageState: await page.context().storageState() })
  const reopened = await other.newPage()
  let latestRead = false
  await reopened.route('**/api/v1/tests**', async route => {
    const url = new URL(route.request().url()); const path = url.pathname
    const base = `/api/v1/tests/${runId}`
    if (path === `${base}/archive/root-cause/v2`) { latestRead = true; return route.fulfill({ json: { state: 'none', presented: true } }) }
    if (path === `${base}/archive/root-cause/analyses/rca-1`) return route.fulfill({ json: rcaTestResponse() })
    if (path === `${base}/archive/exemplars`) return route.fulfill({ json: { exemplars: [exemplar('2026-10-05T12:04:00Z', 'trace-in-window')], total: 1 } })
    if (path === `${base}/archive/summaries`) return route.fulfill({ json: { summaries, total: summaries.length } })
    if (path === `${base}/archive/resources`) return route.fulfill({ json: { samples: [], total: 0 } })
    if (path === `${base}/archive/transactions`) return route.fulfill({ json: { transactions: [], total: 0 } })
    if (path === `${base}/archive/analysis`) return route.fulfill({ status: 404, json: { error: 'no analysis' } })
    if (path === `${base}/archive/report`) return route.fulfill({ json: { manifest: { test_id: runId, status: 'COMPLETE' }, overview: { transactions: 12000 }, by_action: {}, error_patterns: [] } })
    if (path === base && route.request().method() === 'GET') {
      const response = await route.fetch(); const payload = await response.json()
      return route.fulfill({ response, json: { ...payload, status: 'COMPLETE', started_at: '2026-10-05T12:00:00Z', ended_at: '2026-10-05T12:10:00Z',
        archive: { test_id: runId, schema_version: '1', archive_path: '/archive', manifest_path: '/archive/manifest.json', transaction_count: 12000, resource_sample_count: 0, interval_summary_count: summaries.length, created_at: '2026-10-05T12:10:00Z', completed_at: '2026-10-05T12:10:01Z' } } })
    }
    return route.continue()
  })
  await reopened.goto(shared)
  const reopenedInvestigation = reopened.getByRole('region', { name: 'Investigation: Resource pressure' })
  await expect(reopenedInvestigation).toBeVisible()
  await expect(reopenedInvestigation.getByRole('button', { name: 'Focus cause: builtin:service.cpu.time · Dynatrace', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(reopenedInvestigation).toContainText('Window: 2026-10-05 12:02:20 UTC to 2026-10-05 12:06:00 UTC')
  expect(latestRead).toBe(false)
  await other.close()
  // The first page runs the analysis poll; make it the active page again so
  // its timers are not throttled as a background page's.
  await page.bringToFront()

  // Business keys: values typed for a lookup go out once and stay off the
  // URL; project admins approve mappings in the same panel.
  const keys = page.getByRole('region', { name: 'Business key lookup', exact: true })
  await keys.getByLabel('Values, one per line (up to 20)').fill('ORD-1\nORD-404')
  await keys.getByRole('button', { name: 'Look up', exact: true }).click()
  await expect(keys.getByRole('region', { name: 'Result for ORD-1' })).toContainText('2 archived requests · Found in the logs (1)')
  await expect(keys.getByRole('region', { name: 'Result for ORD-1' })).toContainText('payment gateway timeout')
  await expect(keys.getByRole('region', { name: 'Result for ORD-404' })).toContainText('0 archived requests · Not found in the logs')
  expect(lookupBody).toEqual({ mapping_id: 'map-1', values: ['ORD-1', 'ORD-404'] })
  expect(page.url()).not.toContain('ORD-1')
  await page.setViewportSize({ width: 1280, height: 2600 })
  await keys.scrollIntoViewIfNeeded()
  await keys.screenshot({ path: testInfo.outputPath('business-key-lookup.png') })
  await page.setViewportSize({ width: 1280, height: 720 })
  await keys.getByText('Approved business keys (1)').click()
  await keys.getByLabel('Extraction').fill('account_id')
  await keys.getByLabel('Log source').selectOption('elastic-1')
  await keys.getByLabel('Log field').fill('account.ban')
  await keys.getByRole('button', { name: 'Approve mapping', exact: true }).click()
  await expect.poll(() => approved).toEqual({ name: 'account_id', definition: { extraction: 'account_id', source_id: 'elastic-1', field: 'account.ban' } })

  // Re-analyzing queues a new revision, routes to it and follows it to
  // completion.
  await panel.getByRole('button', { name: 'Re-analyze', exact: true }).click()
  await expect(page).toHaveURL(/analysis=rca-2/)
  await expect(page).not.toHaveURL(/hypothesis=/)
  await expect(panel.getByText('Analysis revision 3 is running…', { exact: true })).toBeVisible()
  await expect(panel.getByText('Analysis revision 3 is running…', { exact: true })).toBeHidden({ timeout: 15000 })
  await expect(panel.getByRole('listitem', { name: 'Hypothesis 1: Resource pressure' })).toBeVisible()
  await expect(panel).toContainText('requested analysis, revision 3')
  expect(requested).toEqual({ enrich: false })

  // The report composer previews the same generated root cause the exports
  // render, apart from the analyst's findings.
  await page.getByRole('button', { name: 'Export report', exact: true }).click()
  await page.getByRole('button', { name: 'Preview / edit commentary', exact: true }).click()
  const composer = page.getByRole('dialog', { name: 'Report composer' })
  await composer.getByRole('button', { name: 'Edit commentary', exact: true }).click()
  const generated = composer.getByRole('region', { name: 'Root-cause analysis (generated)' })
  await expect(generated).toContainText('Root-cause v2, analysis revision 2')
  await expect(generated).toContainText('Resource pressure (medium confidence)')
  await expect(generated).toContainText('Would disprove it: CPU stays below saturation while latency rises')
  await expect(generated).not.toContainText('F-0n')
})
