import type { TestSummary } from '../types'
import { isPendingTestStatus, normalizeTestStatus } from '../utils/testStatusUtils'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { recordedTime, formatElapsedSeconds } from './runReport/executionPresentation'

export function commandRunWaiting(run: TestSummary): boolean {
  return isPendingTestStatus(run.status) || normalizeTestStatus(run.status) === 'SHAKEOUT_REVIEW'
}

export function commandRunHealth(run: TestSummary, now: number) {
  const metrics = run.live_metrics
  const stamp = recordedTime(metrics?.timestamp)
  const waiting = commandRunWaiting(run)
  const hasSamples = Boolean(metrics && metrics.total_transactions + metrics.total_errors > 0)
  const stale = !waiting && Boolean(metrics) && (stamp === undefined || now - stamp > 30_000 || stamp > now + 5_000)
  const errorPct = hasSamples ? metrics!.total_errors / (metrics!.total_transactions + metrics!.total_errors) * 100 : undefined
  const issue = Boolean((errorPct ?? 0) > 0 || metrics?.throughput_target_missed)
  return {
    errorPct,
    hasSamples,
    stale,
    label: waiting ? 'Not started' : stale ? 'Metrics stale' : !hasSamples ? 'No samples'
      : issue ? (metrics?.throughput_target_missed ? 'Throughput target missed' : 'Errors reported') : 'No errors reported',
    // Prioritize stale/absent evidence as well as observed errors. This is an
    // attention order, not an invented SLA verdict.
    priority: waiting ? 4 : stale ? 0 : issue ? 1 : !hasSamples ? 2 : 3,
  }
}

export function commandRunTiming(run: TestSummary, now: number) {
  const started = recordedTime(run.started_at || run.start_time)
  if (commandRunWaiting(run)) return { elapsed: 'Not started', remaining: run.duration || 'Unavailable' }
  if (run.termination_mode === 'iterations') return { elapsed: formatElapsedSeconds(started === undefined ? undefined : Math.max(0, (now-started)/1000)), remaining: `${run.live_metrics?.completed_iterations ?? 0} / ${(run.groups || []).reduce((n,g) => n + g.vusers * (g.iterations || 0),0)} iterations` }
  const elapsed = started === undefined ? undefined : Math.max(0, (now - started) / 1000)
  // The summary has no cumulative paused-duration field. Do not present a
  // wall-clock subtraction as an authoritative execution countdown.
  return {
    elapsed: formatElapsedSeconds(elapsed),
    remaining: run.status === 'PAUSED' ? 'Paused' : elapsed === undefined ? 'Unavailable'
      : `~${formatElapsedSeconds(Math.max(0, parseDurationToSeconds(run.duration) - elapsed))}`,
  }
}

export function commandRunWaitReason(run: TestSummary): string {
  if (run.pending_reason) return `${run.queue_position ? `Queue #${run.queue_position} · ` : ''}${run.pending_reason}`
  if (run.status === 'SCHEDULED') {
    const time = recordedTime(run.scheduled_start_at)
    return time === undefined ? 'Awaiting scheduled start' : `Scheduled ${new Date(time).toLocaleString()}`
  }
  if (run.status === 'QUEUED') return 'Ready for manual start'
  if (run.status === 'RESERVED') return 'Capacity reserved; awaiting dispatch'
  if (run.status === 'SHAKEOUT_REVIEW') return 'Review shakeout before continuing'
  return 'Waiting; reason unavailable'
}

export function filterCommandRuns(runs: TestSummary[], search: string, status: string, sort: string, now: number) {
  const term = search.trim().toLocaleLowerCase()
  return runs.filter(run => (!term || `${run.name} ${run.ref_key || ''} ${run.id}`.toLocaleLowerCase().includes(term))
    && (status === 'all' || normalizeTestStatus(run.status) === status))
    .sort((a, b) => {
      if (sort === 'attention') {
        const priority = commandRunHealth(a, now).priority - commandRunHealth(b, now).priority
        if (priority) return priority
      }
      if (sort === 'name') return a.name.localeCompare(b.name) || a.id.localeCompare(b.id)
      return (recordedTime(b.started_at || b.created_at) ?? 0) - (recordedTime(a.started_at || a.created_at) ?? 0) || a.id.localeCompare(b.id)
    })
}
