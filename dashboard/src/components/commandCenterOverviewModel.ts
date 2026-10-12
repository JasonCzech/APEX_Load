import type { TestSummary } from '../types'
import { isPendingTestStatus, normalizeTestStatus } from '../utils/testStatusUtils'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { recordedTime, formatElapsedSeconds, runActiveSeconds } from './runReport/executionPresentation'
import { formatDateTime } from '../utils/dateTime'
import { liveCountsErrorPercent, livePopulation, liveRate, liveRunCounts, liveThroughputPopulation } from '../utils/liveErrorRate'

export function commandRunWaiting(run: TestSummary): boolean {
  return isPendingTestStatus(run.status) || normalizeTestStatus(run.status) === 'SHAKEOUT_REVIEW'
}

export function commandRunHealth(run: TestSummary, now: number) {
  const metrics = run.live_metrics
  const stamp = recordedTime(metrics?.timestamp)
  const waiting = commandRunWaiting(run)
  // Counted as the run's own Command Center counts it: requests once it has
  // sent any (else transactions), run to date.
  const counts = liveRunCounts(metrics)
  const hasSamples = Boolean(counts && counts.attempts > 0)
  const stale = !waiting && Boolean(metrics) && (stamp === undefined || now - stamp > 30_000 || stamp > now + 5_000)
  const errorPct = counts && hasSamples ? liveCountsErrorPercent(counts) : undefined
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

/** The run's throughput in its own unit: requests/s once it has sent requests, TPS for goal runs and runs without requests. */
export function commandRunThroughput(run: TestSummary): { rate: number | undefined; unit: string } {
  const population = liveThroughputPopulation(run, livePopulation(run.live_metrics))
  return { rate: liveRate(run.live_metrics, population), unit: population === 'requests' ? 'req/s' : 'TPS' }
}

export function commandRunTiming(run: TestSummary, now: number) {
  if (commandRunWaiting(run)) return { elapsed: 'Not started', remaining: run.duration || 'Unavailable' }
  // The run's own clock: paused time is left out, since the engine makes it up
  // after a resume. Remaining stays an estimate (a start delay is not counted),
  // and a paused run says so rather than tick down.
  const elapsed = runActiveSeconds(run, now)
  if (run.termination_mode === 'iterations') return { elapsed: formatElapsedSeconds(elapsed), remaining: `${run.live_metrics?.completed_iterations ?? 0} / ${(run.groups || []).reduce((n,g) => n + g.vusers * (g.iterations || 0),0)} iterations` }
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
    return time === undefined ? 'Awaiting scheduled start' : `Scheduled ${formatDateTime(time, { seconds: true })}`
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
