import type { RunArchiveReport, TestSummary } from '../types'

export type AcceptanceState = 'Pass' | 'Fail' | 'Inconclusive' | 'Not assessed' | 'Unavailable' | 'Loading'
export type DashboardReports = Record<string, RunArchiveReport | null>
export interface RunFilters {
  search: string
  days: string
  status: string
  evidence: string
  acceptance: string
}
export const emptyRunFilters: RunFilters = { search: '', days: 'all', status: 'all', evidence: 'all', acceptance: 'all' }

export function runReference(test: TestSummary): string {
  return test.ref_key || test.id
}

export function dashboardAcceptance(test: TestSummary, reports: DashboardReports): AcceptanceState {
  if (!test.archive) return 'Unavailable'
  if (!(test.id in reports)) return 'Loading'
  const verdict = reports[test.id]?.verdict
  if (!verdict) return 'Unavailable'
  if (verdict.failed || verdict.status === 'Fail') return 'Fail'
  if (verdict.status === 'Not assessed') return 'Not assessed'
  if (verdict.status === 'Pass' && !verdict.conditional) return 'Pass'
  return 'Inconclusive'
}

export function summarizeAcceptance(tests: TestSummary[], reports: DashboardReports): Record<AcceptanceState, number> {
  const counts: Record<AcceptanceState, number> = { Pass: 0, Fail: 0, Inconclusive: 0, 'Not assessed': 0, Unavailable: 0, Loading: 0 }
  for (const test of tests) counts[dashboardAcceptance(test, reports)]++
  return counts
}

export function filterDashboardRuns(tests: TestSummary[], filters: RunFilters, reports: DashboardReports, now: number): TestSummary[] {
  const search = filters.search.trim().toLocaleLowerCase()
  const cutoff = filters.days === 'all' ? null : now - Number(filters.days) * 86_400_000
  return tests.filter(test => {
    if (search && !`${test.name} ${runReference(test)} ${test.script_name || ''}`.toLocaleLowerCase().includes(search)) return false
    if (filters.status !== 'all' && test.status !== filters.status) return false
    if (filters.evidence !== 'all' && Boolean(test.archive) !== (filters.evidence === 'available')) return false
    if (filters.acceptance !== 'all' && dashboardAcceptance(test, reports) !== filters.acceptance) return false
    const completed = Date.parse(test.ended_at || test.start_time || test.created_at)
    return cutoff === null || (Number.isFinite(completed) && completed >= cutoff && completed <= now)
  })
}

export interface DashboardAttentionItem {
  tone: 'danger' | 'warning' | 'info'
  title: string
  detail: string
  testId?: string
  destination?: 'monitoring' | 'evidence'
}

export function pendingRunDetail(test: TestSummary): string {
  if (test.status === 'SCHEDULED') {
    return test.scheduled_start_at ? `Scheduled for ${new Date(test.scheduled_start_at).toLocaleString()}` : 'Awaiting scheduled start'
  }
  const position = test.queue_position ? `Queue #${test.queue_position}` : test.status === 'RESERVED' ? 'Reserved' : 'Queued'
  const reason = test.pending_reason || (test.status === 'RESERVED' ? 'Capacity reserved; awaiting dispatch' : 'Waiting for capacity')
  return `${position} · ${reason}${test.pending_since ? ` · since ${new Date(test.pending_since).toLocaleString()}` : ''}`
}

export function buildDashboardAttention(
  active: TestSummary[],
  pending: TestSummary[],
  completed: TestSummary[],
  reports: DashboardReports,
  capacityWarnings: string[] | null = [],
  healthStale = false,
): DashboardAttentionItem[] {
  const items: DashboardAttentionItem[] = []
  if (healthStale) {
    items.push({ tone: 'warning', title: 'System health is stale', detail: 'The latest health check failed. Generator readiness and capacity may have changed.', destination: 'monitoring' })
  }
  for (const test of active) {
    const metrics = test.live_metrics
    if (metrics && metrics.error_pct >= 1) {
      items.push({ tone: 'danger', title: `${test.name} errors`, detail: `${metrics.error_pct.toFixed(2)}% current error rate`, testId: test.id })
    }
    if (metrics && metrics.avg_response_ms >= 1000) {
      items.push({ tone: 'warning', title: `${test.name} latency`, detail: `${metrics.avg_response_ms.toFixed(0)} ms average response`, testId: test.id })
    }
  }
  for (const test of pending) {
    items.push({ tone: 'warning', title: `${test.name} waiting`, detail: pendingRunDetail(test), testId: test.id })
  }
  for (const warning of capacityWarnings ?? []) {
    items.push({ tone: 'warning', title: 'Capacity warning', detail: warning, destination: 'monitoring' })
  }
  for (const test of completed) {
    if (dashboardAcceptance(test, reports) === 'Fail') {
      items.push({ tone: 'danger', title: `${test.name} acceptance failed`, detail: reports[test.id]?.verdict?.explanation || 'Review the recorded acceptance assessment.', testId: test.id })
    } else if (test.status === 'FAILED' || test.status === 'ABORTED') {
      items.push({ tone: test.status === 'FAILED' ? 'danger' : 'warning', title: `${test.name} ${test.status.toLowerCase()}`, detail: `Execution ended ${new Date(test.ended_at || test.created_at).toLocaleString()}`, testId: test.id })
    }
  }
  const missing = completed.filter(test => !test.archive).length
  if (missing) {
    items.push({ tone: 'info', title: `${missing} run${missing === 1 ? '' : 's'} missing archives`, detail: 'Saved archives unavailable; publication progress is unknown. Review retained evidence.', destination: 'evidence' })
  }
  return items
}
