import type { APIRequestContext } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'
import type { ComparisonSnapshot, RunIdentity, TelemetryRunProfile } from '../src/components/baselineComparison/model'
import { defaultPolicy } from '../src/components/baselineComparison/model'
import type { TelemetryReevaluationResult } from '../src/components/baselineComparison/telemetryReevaluationModel'

type Job = { id: string; state: string; input_fingerprint: string; result_sha256?: string; result?: TelemetryReevaluationResult; failure?: string }

async function archivedRun(request: APIRequestContext, workspace: Workspace, targetURL: string, name: string, trackRun: (id: string) => void) {
  const script = await seedScript(request, workspace, `${name.replace(/\W/g, '_')}_${Date.now()}`, targetURL)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name, duration: '3s', groups: [{ name: 'telemetry', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  return id
}

// Real archived runs, a real project telemetry policy and the real
// evaluation worker: the re-evaluation is reproducible from its hashed
// inputs, leaves the archives alone, and is reachable from the comparison
// view (whose saved snapshot alone is a fixture naming the real runs).
test('runs re-evaluate reproducibly under a common telemetry policy, from the API and the comparison view', async ({ page, target, trackRun }) => {
  test.setTimeout(300_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const current = await archivedRun(request, workspace, target.url, 'Telemetry current', trackRun)
  const baseline = await archivedRun(request, workspace, target.url, 'Telemetry baseline', trackRun)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const manifestBefore = await json(await request.get(`/api/v1/tests/${current}/archive/manifest`))

  // A one-millisecond sample-age budget: every retained interval of a local
  // run (sample age about one publication interval) is over it.
  const policy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, { data: {
    kind: 'telemetry_policy', name: 'Strict sample age', definition: { version: 1, sample_age_ms: 1, sustained_window_seconds: 60 },
  } }))
  const policyRef = { id: String(policy.id), revision: Number(policy.revision) }
  const queue = async () => (await json(await request.post(`/api/v1/analysis/evaluations?${scope}`, { data: { kind: 'telemetry_acceptance', request: { runs: [current, baseline], policy: policyRef } } }))) as unknown as Job
  const settle = async (id: string) => {
    await expect.poll(async () => String((await json(await request.get(`/api/v1/analysis/evaluations/${id}?${scope}`))).state), { timeout: 60_000 }).toMatch(/^(ready|failed|cancelled|expired)$/)
    return (await json(await request.get(`/api/v1/analysis/evaluations/${id}?${scope}`))) as unknown as Job
  }
  const first = await settle((await queue()).id)
  expect(first.state, first.failure).toBe('ready')
  const second = await settle((await queue()).id)
  expect(second.id).not.toBe(first.id)
  expect(second.input_fingerprint).toBe(first.input_fingerprint)
  expect(second.result_sha256, 'the same hashed inputs produce the same result').toBe(first.result_sha256)
  const result = first.result!
  expect(result.input_fingerprint).toBe(first.input_fingerprint)
  expect(result.runs.map(run => run.run_id)).toEqual([current, baseline])
  expect(result.all_answerable).toBe(true)
  expect(result.runs[0].reevaluated!.degraded + result.runs[0].reevaluated!.breached).toBeGreaterThan(0)
  expect(result.policy.budgets.find(budget => budget.stage === 'sample_age')).toMatchObject({ enabled: true, limit_ms: 1, source: 'policy' })
  expect(await json(await request.get(`/api/v1/tests/${current}/archive/manifest`)), 'the archive is unchanged').toEqual(manifestBefore)

  // The comparison view: a saved snapshot whose two runs were classified
  // at different cadences, re-evaluated through the real API.
  const identity = (id: string, name: string): RunIdentity => ({ id, name, release_id: workspace.releaseId, release_name: 'Release', scenario_id: '', status: 'COMPLETE', vusers: 1, peak_vusers: 1, completed_at: '2026-10-08T12:00:00Z', pinned: true, archive_available: true })
  const profile = (run_id: string, cadence_ms: number): TelemetryRunProfile => ({ run_id, policy_availability: 'available', policy_hash: 'a'.repeat(64), cadence_ms, cadence_scope: 'local-controller-ticker', sustained_breach_window_ms: 15000, source_capabilities: [] })
  const snapshot: ComparisonSnapshot = {
    id: 'telemetry-snapshot', project_id: workspace.projectId, current: identity(current, 'Telemetry current'), baselines: [identity(baseline, 'Telemetry baseline')],
    created_at: '2026-10-08T12:00:00Z', rule_version: 'baseline-comparison-v1', request: { baseline_ids: [baseline], population: 'transaction', allow_other_scenarios: false }, policy: defaultPolicy,
    transactions: [], resources: [], setup: [], findings: [], warnings: [],
    telemetry_comparisons: [{ baseline_run_id: baseline, scope: 'whole_run', comparable: false, reasons: ['cadence_differs'], current: profile(current, 1000), baseline: profile(baseline, 3000) }],
  }
  await page.route(`**/api/v1/tests/${current}/baseline-comparisons/telemetry-snapshot*`, route => route.fulfill({ json: snapshot }))
  await page.goto(`/#/report/${current}?section=comparison&comparison_id=telemetry-snapshot&${scopeQuery(workspace)}`)
  await page.reload()
  const compatibility = page.getByRole('region', { name: 'Telemetry compatibility', exact: true })
  await expect(compatibility).toContainText('not comparable', { timeout: 30_000 })
  await expect(compatibility).toContainText('different publication cadence')
  await expect(compatibility).toContainText('3000 ms (local-controller-ticker)')
  await compatibility.getByRole('button', { name: 'Re-evaluate 2 runs', exact: true }).click()
  const derived = page.getByRole('region', { name: 'Telemetry re-evaluation result', exact: true })
  await expect(derived).toContainText('Strict sample age revision 1', { timeout: 60_000 })
  await expect(derived.locator(`tr[data-run="${current}"]`)).toContainText('degraded')
  await expect(derived).toContainText('original classification are unchanged')
  await compatibility.screenshot({ path: test.info().outputPath('telemetry-compatibility.png') })
})

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
