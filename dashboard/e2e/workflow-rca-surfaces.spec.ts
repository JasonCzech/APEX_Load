import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { rcaTestReportView, rcaTestResponse, rcaTestSummary } from '../src/components/rca/rcaTestFixture'

// A project administrator turns root-cause analysis on against the real
// controller. The run is real and authorized; its archive and analyses are
// served from fixtures, as in the investigation spec, so the Summary can be
// watched while an automatic analysis completes.
test('root cause is turned on in Settings and follows a run from Summary to Diagnostics and Performance', async ({ page }, testInfo) => {
  test.setTimeout(120000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const runId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Checkout soak', duration: '10m', groups: [{ name: 'checkout', script_id: scriptId, vusers: 20 }] } })))

  // Settings › Project › Analysis saves against the revision it read.
  await page.goto(`/#/settings?setting=analysis&${scopeQuery(workspace)}`)
  // A session that signed in before the workspace existed reloads its
  // project list.
  await page.reload()
  const settings = page.locator('.project-analysis-settings')
  await expect(settings).toContainText('Not changed yet')
  await expect(settings.getByLabel('Analyze every archived run automatically')).toBeDisabled()
  await settings.getByLabel('Compute root-cause analyses').check()
  await settings.getByLabel('Show root cause in summaries and reports').check()
  await settings.getByLabel('Analyze every archived run automatically').check()
  await settings.getByRole('button', { name: 'Save analysis settings', exact: true }).click()
  await expect(settings.getByRole('status')).toHaveText('Saved as revision 1.')
  const saved = await json(await request.get(`/api/v1/projects/${workspace.projectId}/analysis-settings`)) as { effective: { root_cause: Record<string, boolean> }; settings: { revision: number } }
  expect(saved.settings.revision).toBe(1)
  expect(saved.effective.root_cause).toEqual({ enabled: true, present: true, auto_analyze: true, auto_enrich: false })
  await settings.screenshot({ path: testInfo.outputPath('project-analysis-settings.png') })

  const start = Date.parse('2026-10-05T12:00:00Z')
  const summaries = Array.from({ length: 60 }, (_, i) => {
    const at = start + (i + 1) * 10_000
    const slow = at >= Date.parse('2026-10-05T12:03:20Z')
    return { timestamp: new Date(at).toISOString(), test_id: runId, test_name: 'Checkout soak', active_vusers: at < Date.parse('2026-10-05T12:02:00Z') ? 10 : 20, tps: 20, avg_response_ms: slow ? 900 : 300, error_pct: slow ? 1 : 0, total_transactions: 200, total_errors: slow ? 2 : 0, p50_ms: slow ? 800 : 250, p90_ms: slow ? 2500 : 400, p95_ms: slow ? 3100 : 450, p99_ms: slow ? 3900 : 600 }
  })
  // The automatic analysis is running when the report loads and completes
  // on the summary's second poll.
  let completed = false
  let polls = 0
  const running = { analysis_id: 'rca-1', revision: 2, state: 'running', origin: 'automatic', created_at: '2026-10-05T12:10:02Z', presented: true, analysis_enabled: true, auto_analyze: true }
  await page.route('**/api/v1/tests**', async route => {
    const url = new URL(route.request().url()); const path = url.pathname; const method = route.request().method()
    const base = `/api/v1/tests/${runId}`
    if (path === base && method === 'GET') {
      const response = await route.fetch(); const payload = await response.json()
      return route.fulfill({ response, json: { ...payload, status: 'COMPLETE', created_at: '2026-10-05T11:59:00Z', started_at: '2026-10-05T12:00:00Z', ended_at: '2026-10-05T12:10:00Z',
        archive: { test_id: runId, schema_version: '1', archive_path: '/archive', manifest_path: '/archive/manifest.json', transaction_count: 12000, resource_sample_count: 0, interval_summary_count: summaries.length, created_at: '2026-10-05T12:10:00Z', completed_at: '2026-10-05T12:10:01Z' } } })
    }
    if (path === `${base}/archive/report`) return route.fulfill({ json: {
      manifest: { test_id: runId, test_name: 'Checkout soak', status: 'COMPLETE', termination_reason: 'duration_reached', completed_at: '2026-10-05T12:10:00Z', provenance: { total_vusers: 20, duration: '10m', started_at: '2026-10-05T12:00:00Z' } },
      overview: { transactions: 12000, peak_active_vusers: 20 }, metric_populations: { transaction: { transactions: 12000, errors: 70, avg_response_ms: 600, p95_ms: 3100 } },
      by_action: {}, by_transaction: { Checkout: { transactions: 12000, errors: 70, error_pct: 0.58, avg_response_ms: 600, min_response_ms: 90, max_response_ms: 5200, p95_ms: 3100 } },
      error_patterns: [{ signature: 'checkout-503', sample: 'HTTP 503 from /cart/checkout', count: 70, first_seen: '2026-10-05T12:03:40Z', latest_seen: '2026-10-05T12:05:50Z', actions: ['Checkout'] }],
      completeness: { score_pct: 100 }, verdict: { status: 'fail' },
      validity: { rules_version: 'validity.v1', verdict: 'suspect', reasons: ['gen-1 held CPU at or above 85% for 3 intervals'], windows: [], limitations: [] },
      root_cause_view: rcaTestReportView(),
      root_cause_summary: rcaTestSummary({ state: 'running', analysis_id: undefined, revision: undefined, outcome: undefined, top_hypothesis: undefined, origin: undefined, auto_analyze: true,
        latest: { analysis_id: 'rca-1', revision: 2, state: 'running', origin: 'automatic', created_at: '2026-10-05T12:10:02Z' } }),
    } })
    if (path === `${base}/archive/summaries`) return route.fulfill({ json: { summaries, total: summaries.length } })
    if (path === `${base}/archive/resources`) return route.fulfill({ json: { samples: [], total: 0 } })
    if (path === `${base}/archive/transactions`) return route.fulfill({ json: { transactions: [], total: 0 } })
    if (path === `${base}/archive/analysis`) return route.fulfill({ status: 404, json: { error: 'no analysis' } })
    if (path === `${base}/archive/root-cause/v2`) return route.fulfill({ json: completed ? rcaTestResponse({ origin: 'automatic', analysis_id: 'rca-1' }) : { state: 'none', presented: true, analysis_enabled: true, auto_analyze: true } })
    if (path === `${base}/archive/root-cause/analyses/rca-1`) {
      polls++
      if (polls < 2) return route.fulfill({ json: running })
      completed = true
      return route.fulfill({ json: rcaTestResponse({ origin: 'automatic' }) })
    }
    if (path === `${base}/archive/root-cause/links`) return route.fulfill({ json: { links: [] } })
    if (path === `${base}/archive/exemplars`) return route.fulfill({ json: { exemplars: [], total: 0 } })
    return route.continue()
  })

  // Summary: the run's validity, the running analysis, then its verdict
  // without a reload.
  await page.goto(`/#/report/${runId}?${scopeQuery(workspace)}`)
  const outcome = page.getByRole('region', { name: 'Run outcome', exact: true })
  await expect(outcome).toContainText('Suspect run')
  await expect(outcome).toContainText('Analysis running')
  await expect(page.getByRole('note', { name: 'Evidence limitations' })).toContainText('Run validity is suspect: gen-1 held CPU at or above 85% for 3 intervals')
  await expect(outcome).toContainText('#1 Resource pressure (medium confidence)', { timeout: 30000 })
  await outcome.screenshot({ path: testInfo.outputPath('run-outcome-root-cause.png') })

  // Diagnostics without a hypothesis: the analysis's facts and the run's
  // largest changes on its timeline.
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  const panel = page.getByRole('region', { name: 'Root cause', exact: true })
  await expect(panel.locator('.rca-facts')).toContainText('Automatic · revision 2')
  const changes = panel.getByRole('region', { name: 'Largest changes', exact: true })
  await expect(changes).toContainText('Change: Checkout · p95 latency rose +300 ms')
  await expect(changes.locator('.recharts-surface').first()).toBeVisible()
  // The error pattern names the hypothesis it overlaps.
  await expect(page.getByRole('button', { name: 'Explained by #1 Resource pressure', exact: true })).toBeVisible()

  // Performance marks the analysis on the timeline; a mark opens its
  // hypothesis.
  await page.getByRole('tab', { name: 'Performance', exact: true }).click()
  const markers = page.getByRole('list', { name: 'Root-cause markers', exact: true })
  await expect(markers).toContainText('Symptom: Checkout · p95 latency +300 ms (#1 Resource pressure)')
  await markers.screenshot({ path: testInfo.outputPath('performance-root-cause-markers.png') })
  await markers.getByRole('button', { name: /^Symptom: Checkout/ }).click()
  await expect(page).toHaveURL(/section=diagnostics/)
  await expect(page).toHaveURL(/hypothesis=hypothesis-cpu/)
  await expect(panel.getByRole('region', { name: 'Investigation: Resource pressure' })).toBeVisible()
})
