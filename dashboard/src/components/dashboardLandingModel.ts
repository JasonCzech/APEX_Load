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
    // "Stopped by operator" is a COMPLETE run with a stop actor, as on Results.
    const stoppedByOperator = Boolean(test.stopped_by_subject)
    if (filters.status === 'STOPPED') {
      if (!stoppedByOperator) return false
    } else if (filters.status !== 'all' && (test.status !== filters.status || (filters.status === 'COMPLETE' && stoppedByOperator))) return false
    if (filters.evidence !== 'all' && Boolean(test.archive) !== (filters.evidence === 'available')) return false
    if (filters.acceptance !== 'all' && dashboardAcceptance(test, reports) !== filters.acceptance) return false
    const completed = Date.parse(test.ended_at || test.start_time || test.created_at)
    return cutoff === null || (Number.isFinite(completed) && completed >= cutoff && completed <= now)
  })
}

export interface DashboardAttentionItem {
  id: string
  tone: 'danger' | 'warning' | 'info'
  title: string
  detail: string
  testId?: string
  destination?: 'monitoring' | 'evidence' | 'root-cause'
  // rootCause names the analysis and hypothesis a root-cause item opens.
  rootCause?: { analysis?: string; hypothesis?: string }
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
    items.push({ id: 'system-health-stale', tone: 'warning', title: 'System health is stale', detail: 'The latest health check failed. Generator readiness and capacity may have changed.', destination: 'monitoring' })
  }
  for (const test of active) {
    const metrics = test.live_metrics
    if (metrics && metrics.error_pct >= 1) {
      items.push({ id: `run-errors:${test.id}`, tone: 'danger', title: `${test.name} errors`, detail: `${metrics.error_pct.toFixed(2)}% current error rate`, testId: test.id })
    }
    if (metrics && metrics.avg_response_ms >= 1000) {
      items.push({ id: `run-latency:${test.id}`, tone: 'warning', title: `${test.name} latency`, detail: `${metrics.avg_response_ms.toFixed(0)} ms average response`, testId: test.id })
    }
  }
  for (const test of pending) {
    items.push({ id: `run-pending:${test.id}`, tone: 'warning', title: `${test.name} waiting`, detail: pendingRunDetail(test), testId: test.id })
  }
  for (const [index, warning] of (capacityWarnings ?? []).entries()) {
    items.push({ id: `capacity-warning:${index}:${warning}`, tone: 'warning', title: 'Capacity warning', detail: warning, destination: 'monitoring' })
  }
  for (const test of completed) {
    if (dashboardAcceptance(test, reports) === 'Fail') {
      items.push({ id: `acceptance-failed:${test.id}`, tone: 'danger', title: `${test.name} acceptance failed`, detail: reports[test.id]?.verdict?.explanation || 'Review the recorded acceptance assessment.', testId: test.id })
    } else if (test.status === 'FAILED' || test.status === 'ABORTED') {
      items.push({ id: `execution-${test.status.toLowerCase()}:${test.id}`, tone: test.status === 'FAILED' ? 'danger' : 'warning', title: `${test.name} ${test.status.toLowerCase()}`, detail: `Execution ended ${new Date(test.ended_at || test.created_at).toLocaleString()}`, testId: test.id })
    }
  }
  // A run its generators or telemetry could not carry, and a run whose
  // cause was found, are worth a look; each opens the run's root cause.
  for (const test of completed) {
    const report = reports[test.id]
    const validity = report?.validity?.verdict
    const summary = report?.root_cause_summary
    const presented = summary?.presented === true
    if (validity === 'invalid' || validity === 'suspect') {
      items.push({ id: `rca-${validity}:${test.id}`, tone: validity === 'invalid' ? 'danger' : 'warning', title: `${test.name} run ${validity}`,
        detail: report?.validity?.reasons?.[0] || 'The load generators or telemetry could not carry the reported load.', testId: test.id,
        ...(presented ? { destination: 'root-cause' as const, rootCause: { analysis: summary?.analysis_id } } : {}) })
    }
    if (presented && summary?.outcome === 'supported' && summary.top_hypothesis) {
      const top = summary.top_hypothesis
      items.push({ id: `rca-cause:${test.id}:${summary.analysis_id || ''}`, tone: 'info', title: `${test.name}: ${top.title}`,
        detail: `Leading root-cause hypothesis${top.confidence ? ` (${top.confidence} confidence)` : ''}${top.explains?.length ? ` for ${top.explains[0]}` : ''}`,
        testId: test.id, destination: 'root-cause', rootCause: { analysis: summary.analysis_id, hypothesis: top.id } })
    }
  }
  const missingArchiveIds = completed.filter(test => !test.archive).map(test => test.id).sort()
  if (missingArchiveIds.length) {
    items.push({ id: `missing-archives:${missingArchiveIds.join(',')}`, tone: 'info', title: `${missingArchiveIds.length} run${missingArchiveIds.length === 1 ? '' : 's'} missing archives`, detail: 'Saved archives unavailable; publication progress is unknown. Review retained evidence.', destination: 'evidence' })
  }
  return items
}
