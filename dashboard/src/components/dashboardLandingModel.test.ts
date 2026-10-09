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
