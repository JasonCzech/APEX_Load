import type { GroupLiveMetrics, LiveMetrics } from '../types'

// Live samples carry two kinds of counters: the window fields describe the
// last sample window (normally one second), and the run_* fields accumulate
// from the start of the run. A run-level error rate must use the run
// counters; a one-second window of a run failing 1% usually contains no error
// at all and reads 0.00%. Older runs predate the run counters, so every
// helper falls back to the window values and reports which basis it used.
// The controller omits zero counters, so a run count with no error count
// means no errors so far.
//
// They also count two populations. Requests are every request the run
// attempted, failed ones included: the report's request population.
// Transactions are business transactions, counted as passed and failed. A
// script without transactions still sends requests, and the transaction that
// wraps each of its iterations fails whenever any request in it fails, so its
// transaction error rate overstates what the target saw. Live throughput and
// error rates therefore count requests whenever the run has sent any, as the
// report does, and transactions otherwise. Labels name the population shown.

export type ErrorRateBasis = 'run' | 'window'
export type LivePopulation = 'requests' | 'transactions'

export interface LiveCounts {
  /** Requests attempted, or transactions passed plus failed. */
  attempts: number
  errors: number
  basis: ErrorRateBasis
  population: LivePopulation
}

/** Failed attempts over all attempts; successes and errors are separate counters. */
export function attemptErrorPercent(successes: number, errors: number): number {
  const attempts = successes + errors
  return attempts > 0 ? errors / attempts * 100 : 0
}

/** Failed over attempted, where the attempts already include the failures. */
export function liveCountsErrorPercent(counts: Pick<LiveCounts, 'attempts' | 'errors'>): number {
  return counts.attempts > 0 ? Math.min(100, counts.errors / counts.attempts * 100) : 0
}

/** Requests once the run has sent any, else transactions (the report's rule). */
export function livePopulation(metrics: LiveMetrics | undefined): LivePopulation {
  const requests = typeof metrics?.run_total_requests === 'number' ? metrics.run_total_requests : metrics?.requests ?? 0
  return requests > 0 ? 'requests' : 'transactions'
}

export function liveRunCounts(metrics: LiveMetrics | undefined, population: LivePopulation = livePopulation(metrics)): LiveCounts | undefined {
  if (!metrics) return undefined
  if (population === 'requests') {
    return typeof metrics.run_total_requests === 'number'
      ? { attempts: metrics.run_total_requests, errors: metrics.run_request_errors ?? 0, basis: 'run', population }
      : { attempts: metrics.requests ?? 0, errors: metrics.request_errors ?? 0, basis: 'window', population }
  }
  if (typeof metrics.run_total_transactions === 'number' || typeof metrics.run_total_errors === 'number') {
    const errors = metrics.run_total_errors ?? 0
    return { attempts: (metrics.run_total_transactions ?? 0) + errors, errors, basis: 'run', population }
  }
  const errors = metrics.total_errors ?? 0
  return { attempts: (metrics.total_transactions ?? 0) + errors, errors, basis: 'window', population }
}

export function liveRunErrorPercent(metrics: LiveMetrics | undefined, population?: LivePopulation): number | undefined {
  const counts = liveRunCounts(metrics, population)
  return counts ? liveCountsErrorPercent(counts) : undefined
}

export function groupRunCounts(group: GroupLiveMetrics | undefined, population: LivePopulation): LiveCounts | undefined {
  if (!group) return undefined
  if (population === 'requests') {
    return typeof group.run_requests === 'number'
      ? { attempts: group.run_requests, errors: group.run_request_errors ?? 0, basis: 'run', population }
      : { attempts: group.requests ?? 0, errors: group.request_errors ?? 0, basis: 'window', population }
  }
  if (typeof group.run_transactions === 'number' || typeof group.run_errors === 'number') {
    const errors = group.run_errors ?? 0
    return { attempts: (group.run_transactions ?? 0) + errors, errors, basis: 'run', population }
  }
  const errors = group.errors ?? 0
  return { attempts: (group.transactions ?? 0) + errors, errors, basis: 'window', population }
}

/** A group's error rate, or undefined when it has no attempts in the population yet. */
export function groupRunErrorPercent(group: GroupLiveMetrics | undefined, population: LivePopulation): number | undefined {
  const counts = groupRunCounts(group, population)
  return counts && counts.attempts > 0 ? liveCountsErrorPercent(counts) : undefined
}

/** The population a run's throughput counts: a goal-oriented run's stays TPS, the unit of its targets. */
export function liveThroughputPopulation(test: { scenario_mode?: string } | undefined, population: LivePopulation): LivePopulation {
  return test?.scenario_mode === 'goal_oriented' ? 'transactions' : population
}

/** The window rate of a sample or a group in the population: requests/s or TPS. */
export function liveRate(metrics: { tps?: number; request_rate?: number } | undefined, population: LivePopulation): number | undefined {
  if (!metrics) return undefined
  return population === 'requests' ? metrics.request_rate ?? 0 : metrics.tps
}

/** Short label suffix that names the window an error rate covers. */
export function errorRateBasisLabel(basis: ErrorRateBasis | undefined): string {
  return basis === 'window' ? 'last sample' : 'run to date'
}

/** "Request error rate" or "Transaction error rate". */
export function errorRateLabel(population: LivePopulation): string {
  return population === 'requests' ? 'Request error rate' : 'Transaction error rate'
}

/** The unit a population's throughput is counted in. */
export function throughputLabel(population: LivePopulation): string {
  return population === 'requests' ? 'Requests/s' : 'TPS'
}
