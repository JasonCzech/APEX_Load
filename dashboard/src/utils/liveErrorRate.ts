import type { GroupLiveMetrics, LiveMetrics } from '../types'

// Live samples carry two kinds of counters: total_transactions/total_errors
// describe the last sample window (normally one second), and the run_* fields
// accumulate from the start of the run. A run-level error rate must use the
// run counters; a one-second window of a run failing 1% usually contains no
// error at all and reads 0.00%. Older runs predate the run counters, so every
// helper falls back to the window values and reports which basis it used.
// The controller omits zero run counters, so a run count with no error count
// means no errors so far.

export type ErrorRateBasis = 'run' | 'window'

/** Failed attempts over all attempts; successes and errors are separate counters. */
export function attemptErrorPercent(successes: number, errors: number): number {
  const attempts = successes + errors
  return attempts > 0 ? errors / attempts * 100 : 0
}

export function liveRunCounts(metrics: LiveMetrics | undefined): { transactions: number; errors: number; basis: ErrorRateBasis } | undefined {
  if (!metrics) return undefined
  if (typeof metrics.run_total_transactions === 'number' || typeof metrics.run_total_errors === 'number') {
    return { transactions: metrics.run_total_transactions ?? 0, errors: metrics.run_total_errors ?? 0, basis: 'run' }
  }
  return { transactions: metrics.total_transactions ?? 0, errors: metrics.total_errors ?? 0, basis: 'window' }
}

export function liveRunErrorPercent(metrics: LiveMetrics | undefined): number | undefined {
  const counts = liveRunCounts(metrics)
  return counts ? attemptErrorPercent(counts.transactions, counts.errors) : undefined
}

export function groupRunCounts(group: GroupLiveMetrics | undefined): { transactions: number; errors: number; basis: ErrorRateBasis } | undefined {
  if (!group) return undefined
  if (typeof group.run_transactions === 'number' || typeof group.run_errors === 'number') {
    return { transactions: group.run_transactions ?? 0, errors: group.run_errors ?? 0, basis: 'run' }
  }
  return { transactions: group.transactions ?? 0, errors: group.errors ?? 0, basis: 'window' }
}

export function groupRunErrorPercent(group: GroupLiveMetrics | undefined): number {
  const counts = groupRunCounts(group)
  return counts ? attemptErrorPercent(counts.transactions, counts.errors) : 0
}

/** Short label suffix that names the window an error rate covers. */
export function errorRateBasisLabel(basis: ErrorRateBasis | undefined): string {
  return basis === 'window' ? 'last sample' : 'run to date'
}
