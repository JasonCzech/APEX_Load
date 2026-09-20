import type {
  ReleaseTrendPoint,
  RunArchiveActionStats,
  RunArchiveReport,
  TestSummary,
} from '../types'
import {
  comparisonEligibility,
  finite,
  type AnalysisMetric,
  type ComparisonEligibility,
} from './dashboardAnalysisModel'

export type TrendPopulation = 'transaction' | 'action' | 'unknown'
export interface TrendMeasurement {
  point: ReleaseTrendPoint
  stats?: RunArchiveActionStats
  latency?: number
  successfulRate?: number
  population: TrendPopulation
}
export interface TrendComparison {
  baseline?: TrendMeasurement
  eligibility: ComparisonEligibility
  latencyDelta?: number
  latencyPct?: number
  errorDelta?: number
  throughputDelta?: number
  regressions: string[]
  reason?: string
}
export function trendStats(
  report: RunArchiveReport | null | undefined,
  population: TrendPopulation,
  transaction: string,
): RunArchiveActionStats | undefined {
  if (!report) return undefined
  if (transaction)
    return (
      population === 'transaction'
        ? report.by_transaction
        : population === 'action'
          ? report.by_request
          : !report.metric_populations
            ? report.by_action
            : undefined
    )?.[transaction]
  return (
    report.metric_populations?.[population] ??
    (population === 'unknown' && !report.metric_populations
      ? report.overview
      : undefined)
  )
}
export function validTrendStats(stats: RunArchiveActionStats | undefined): stats is RunArchiveActionStats {
  return Boolean(stats && finite(stats.transactions) && stats.transactions > 0 && finite(stats.errors) && stats.errors >= 0 && stats.errors <= stats.transactions && finite(stats.error_pct) && stats.error_pct >= 0 && stats.error_pct <= 100)
}
export function trendMeasurement(
  point: ReleaseTrendPoint,
  report: RunArchiveReport | null | undefined,
  population: TrendPopulation,
  transaction: string,
  metric: AnalysisMetric,
): TrendMeasurement {
  const stats =
    trendStats(report, population, transaction) ??
    (!transaction && point.metric_population === population
      ? {
          transactions: point.transactions,
          errors: (point.transactions * point.error_pct) / 100,
          error_pct: point.error_pct,
          avg_response_ms: point.avg_response_ms,
          min_response_ms: 0,
          max_response_ms: 0,
        }
      : undefined)
  const valid = validTrendStats(stats)
  const latency = valid
    ? metric === 'average_ms'
      ? stats.avg_response_ms
      : stats[metric]
    : undefined
  const populationStats = report?.metric_populations?.[population]
  const rate = populationStats?.average_rate_per_second
  const populationCount = populationStats?.transactions
  const successfulRate =
    valid &&
    finite(rate) &&
    finite(populationCount) &&
    populationCount > 0 &&
    stats.transactions <= populationCount
      ? (rate * (stats.transactions - stats.errors)) / populationCount
      : undefined
  return {
    point,
    population,
    stats,
    latency: finite(latency) ? latency : undefined,
    successfulRate,
  }
}

export function compareTrendMeasurements(
  current: TrendMeasurement,
  baseline: TrendMeasurement | undefined,
  runs: Record<string, TestSummary>,
  reports: Record<string, RunArchiveReport | null>,
  thresholds: { latencyPct: number; errorPoints: number },
): TrendComparison {
  const eligibility = comparisonEligibility(
    runs[current.point.run_id],
    baseline && runs[baseline.point.run_id],
    reports[current.point.run_id],
    baseline && reports[baseline.point.run_id],
  )
  const result: TrendComparison = { baseline, eligibility, regressions: [] }
  if (!baseline)
    return { ...result, reason: 'No baseline selected for this run.' }
  if (baseline.point.run_id === current.point.run_id)
    return { ...result, reason: 'This run is the baseline.' }
  if (
    !current.stats?.transactions ||
    !baseline.stats?.transactions ||
    current.population !== baseline.population
  )
    return {
      ...result,
      reason:
        'A baseline exists, but matching population samples are unavailable.',
    }
  if (
    [current.stats, baseline.stats].some(
      (stats) =>
        stats.errors < 0 ||
        stats.errors > stats.transactions ||
        !finite(stats.error_pct) ||
        stats.error_pct < 0 ||
        stats.error_pct > 100,
    )
  )
    return { ...result, reason: 'Matching error denominators are invalid.' }
  if (eligibility.status === 'Not comparable')
    return {
      ...result,
      reason: 'Test conditions differ; no qualified delta is calculated.',
    }
  if (
    current.population === 'unknown' ||
    current.stats.transactions < 100 ||
    baseline.stats.transactions < 100
  ) {
    result.eligibility = {
      status: 'Unverified',
      reasons: [
        ...eligibility.reasons,
        current.population === 'unknown'
          ? 'Legacy population is unverified.'
          : 'Fewer than 100 samples in one or both runs.',
      ],
    }
  }
  if (finite(current.latency) && finite(baseline.latency)) {
    result.latencyDelta = current.latency - baseline.latency
    if (baseline.latency > 0)
      result.latencyPct = (result.latencyDelta / baseline.latency) * 100
  }
  result.errorDelta = current.stats.error_pct - baseline.stats.error_pct
  if (finite(current.successfulRate) && finite(baseline.successfulRate))
    result.throughputDelta = current.successfulRate - baseline.successfulRate
  if (finite(result.latencyPct) && result.latencyPct > thresholds.latencyPct)
    result.regressions.push(
      `Latency +${result.latencyPct.toFixed(1)}% exceeds ${thresholds.latencyPct}% rule`,
    )
  if (result.errorDelta > thresholds.errorPoints)
    result.regressions.push(
      `Error rate +${result.errorDelta.toFixed(2)} pp exceeds ${thresholds.errorPoints} pp rule`,
    )
  return result
}
