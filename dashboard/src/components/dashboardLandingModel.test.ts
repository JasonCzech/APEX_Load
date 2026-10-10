import { describe, expect, it } from 'vitest'
import type { RunArchiveReport, TestSummary } from '../types'
import { buildDashboardAttention, dashboardAcceptance, emptyRunFilters, filterDashboardRuns, pendingRunDetail, summarizeAcceptance } from './dashboardLandingModel'

const run = (id: string, changes: Partial<TestSummary> = {}): TestSummary => ({ id, name: 'Same name', status: 'COMPLETE', groups: [], total_vusers: 10, duration: '1m', created_at: '2026-09-12T12:00:00Z', ...changes })
const archived = run('archive', { archive: { transaction_count: 50 } as TestSummary['archive'] })
const report = (status: string, conditional = false, failed = false) => ({ verdict: { status, conditional, failed } } as RunArchiveReport)

describe('dashboard evidence and acceptance', () => {
  it.each([
    ['Pass', false, false, 'Pass'], ['Fail', false, true, 'Fail'], ['Not assessed', true, false, 'Not assessed'],
    ['Inconclusive', true, false, 'Inconclusive'], ['Pass', true, false, 'Inconclusive'], ['Unexpected status', false, false, 'Inconclusive'],
  ] as const)('preserves the recorded %s assessment', (status, conditional, failed, expected) => {
    expect(dashboardAcceptance(archived, { archive: report(status, conditional, failed) })).toBe(expected)
  })
  it('does not infer pass or publication progress from COMPLETE or archive metadata', () => {
    expect(dashboardAcceptance(run('missing'), {})).toBe('Unavailable')
    expect(dashboardAcceptance(archived, {})).toBe('Loading')
    expect(dashboardAcceptance(archived, { archive: null })).toBe('Unavailable')
    expect(dashboardAcceptance(archived, { archive: {} as RunArchiveReport })).toBe('Unavailable')
    expect(summarizeAcceptance([run('missing'), archived], { archive: report('Not assessed', true) })).toEqual({ Pass: 0, Fail: 0, Inconclusive: 0, 'Not assessed': 1, Unavailable: 1, Loading: 0 })
  })
})

describe('dashboard retrieval', () => {
  const runs = [archived, run('older', { ref_key: 'LOC-RUN-OLD', created_at: '2026-05-01T12:00:00Z', status: 'FAILED' }), run('invalid', { created_at: 'invalid' }), run('future', { created_at: '2026-09-14T12:00:00Z' })]
  const now = Date.parse('2026-09-13T12:00:00Z')
  it('matches a run reference without losing duplicate-name identity', () => {
    expect(filterDashboardRuns(runs, { ...emptyRunFilters, search: ' loc-run-old ' }, {}, now).map(test => test.id)).toEqual(['older'])
  })
  it('combines period, execution, evidence, and acceptance filters', () => {
    expect(filterDashboardRuns(runs, { ...emptyRunFilters, days: '1', status: 'COMPLETE', evidence: 'available', acceptance: 'Pass' }, { archive: report('Pass') }, now)).toEqual([archived])
    expect(filterDashboardRuns(runs, { ...emptyRunFilters, status: 'FAILED', evidence: 'unavailable' }, {}, now).map(test => test.id)).toEqual(['older'])
    expect(filterDashboardRuns(runs, { ...emptyRunFilters, search: 'does not exist' }, {}, now)).toEqual([])
  })
  it('separates operator-stopped runs from completed ones, as Results does', () => {
    const stopped = run('stopped', { stopped_by_subject: 'local:dev' })
    const all = [archived, stopped]
    expect(filterDashboardRuns(all, { ...emptyRunFilters, status: 'STOPPED' }, {}, now).map(test => test.id)).toEqual(['stopped'])
    expect(filterDashboardRuns(all, { ...emptyRunFilters, status: 'COMPLETE' }, {}, now).map(test => test.id)).toEqual([archived.id])
  })
  it('includes the date boundary and excludes invalid and future timestamps from bounded periods', () => {
    expect(filterDashboardRuns(runs, { ...emptyRunFilters, days: '1' }, {}, now)).toEqual([archived])
    expect(filterDashboardRuns(runs, emptyRunFilters, {}, now)).toHaveLength(4)
  })
})

describe('dashboard attention priorities', () => {
  it('places live blockers and queue work before grouped historical evidence gaps', () => {
    const missing = Array.from({ length: 8 }, (_, i) => run(`missing-${i}`))
    const pending = run('pending', { status: 'PENDING', queue_position: 2, pending_reason: 'Project limit reached' })
    const items = buildDashboardAttention([], [pending], missing, {})
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ id: 'run-pending:pending', testId: 'pending', detail: 'Queue #2 · Project limit reached' })
    expect(items[1]).toMatchObject({ id: 'missing-archives:missing-0,missing-1,missing-2,missing-3,missing-4,missing-5,missing-6,missing-7', title: '8 runs missing archives', destination: 'evidence' })
    expect(items[1].detail).toContain('publication progress is unknown')
  })
  it('does not describe scheduled or reserved work as capacity starvation', () => {
    expect(pendingRunDetail(run('scheduled', { status: 'SCHEDULED' }))).toBe('Awaiting scheduled start')
    expect(pendingRunDetail(run('reserved', { status: 'RESERVED' }))).toBe('Reserved · Capacity reserved; awaiting dispatch')
  })
  it('retains every alert, warns about stale health, and preserves acceptance failure separately from execution', () => {
    const failedAssessment = { ...archived, status: 'COMPLETE' as const }
    const items = buildDashboardAttention([], [], [failedAssessment], { archive: report('Fail', false, true) }, ['No project capacity'], true)
    expect(items.map(item => item.title)).toEqual(['System health is stale', 'Capacity warning', 'Same name acceptance failed'])
    expect(items.map(item => item.id)).toEqual(['system-health-stale', 'capacity-warning:0:No project capacity', 'acceptance-failed:archive'])
    expect(buildDashboardAttention([], [], [], {})).toEqual([])
    expect(buildDashboardAttention([], [], [], {}, null)).toEqual([])
  })
})

describe('dashboard root-cause attention', () => {
  const summary = (changes: Record<string, unknown> = {}) => ({ state: 'completed', presented: true, analysis_enabled: true, analysis_id: 'a-1', outcome: 'supported',
    top_hypothesis: { id: 'h-1', rank: 1, title: 'Pool exhaustion', confidence: 'high', explains: ['Checkout · p95 latency'] }, hypothesis_count: 1, anomaly_count: 2, ...changes })
  const withRootCause = (changes: Record<string, unknown>) => ({ ...report('Pass'), ...changes } as unknown as RunArchiveReport)

  it('flags invalid and suspect runs and found causes, opening the root cause', () => {
    const reports = {
      broken: withRootCause({ validity: { verdict: 'invalid', reasons: ['generator-1 was OOM-killed'] }, root_cause_summary: summary({ outcome: 'insufficient_evidence', top_hypothesis: undefined }) }),
      shaky: withRootCause({ validity: { verdict: 'suspect', reasons: [] }, root_cause_summary: summary({ state: 'not_enabled', presented: false }) }),
      found: withRootCause({ validity: { verdict: 'valid' }, root_cause_summary: summary() }),
    }
    const items = buildDashboardAttention([], [], [run('broken', { name: 'Broken' }), run('shaky', { name: 'Shaky' }), run('found', { name: 'Found' })], reports)
    const byId = Object.fromEntries(items.map(item => [item.id, item]))
    expect(byId['rca-invalid:broken']).toMatchObject({ tone: 'danger', title: 'Broken run invalid', detail: 'generator-1 was OOM-killed', destination: 'root-cause', rootCause: { analysis: 'a-1' } })
    // Without root cause presented, a suspect run still opens the run.
    expect(byId['rca-suspect:shaky']).toMatchObject({ tone: 'warning', testId: 'shaky' })
    expect(byId['rca-suspect:shaky'].destination).toBeUndefined()
    // The same words as the report and Results: rank, title and confidence.
    expect(byId['rca-cause:found:a-1']).toMatchObject({ tone: 'info', title: 'Found root cause: #1 Pool exhaustion (high confidence)', destination: 'root-cause', rootCause: { analysis: 'a-1', hypothesis: 'h-1' } })
    expect(byId['rca-cause:found:a-1'].detail).toBe('Leading root-cause hypothesis for Checkout · p95 latency')
    expect(items.some(item => item.id.startsWith('rca-cause:broken'))).toBe(false)
  })

  it("flags the release's latest archived run when it regressed, unless its acceptance already failed", () => {
    const trend = { release_id: 'r-1', run_count: 2, has_baseline: true, regressed: true, regressions: ['error rate regressed'], baseline: { run_name: 'Baseline run' } }
    const archive = { archive: { transaction_count: 10 } as TestSummary['archive'] }
    const regressed = { ...report('Not assessed'), release_trend: trend } as unknown as RunArchiveReport
    // completed is newest first; an older regression is history.
    const items = buildDashboardAttention([], [], [run('worse', { name: 'Worse', ...archive }), run('older', { name: 'Older', ...archive })], { worse: regressed, older: regressed })
    expect(items.find(item => item.id === 'release-regression:worse')).toMatchObject({ tone: 'warning', title: 'Worse regressed', detail: 'Against Baseline run: error rate regressed', testId: 'worse' })
    expect(items.some(item => item.id === 'release-regression:older')).toBe(false)
    const failed = { ...report('Fail', false, true), release_trend: trend } as unknown as RunArchiveReport
    expect(buildDashboardAttention([], [], [run('failed', archive)], { failed }).some(item => item.id.startsWith('release-regression:'))).toBe(false)
    const steady = { ...report('Not assessed'), release_trend: { ...trend, regressed: false, regressions: [] } } as unknown as RunArchiveReport
    expect(buildDashboardAttention([], [], [run('steady', archive), run('older', archive)], { steady, older: regressed }).some(item => item.id.startsWith('release-regression:'))).toBe(false)
  })

  it('says nothing about valid runs or projects that do not present root cause', () => {
    const quiet = withRootCause({ validity: { verdict: 'unknown' }, root_cause_summary: summary({ state: 'not_enabled', presented: false }) })
    expect(buildDashboardAttention([], [], [run('quiet')], { quiet }).filter(item => item.id.startsWith('rca-'))).toEqual([])
  })
})
