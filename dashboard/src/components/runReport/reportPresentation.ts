import type { RunArchiveReport, RunArchiveOverview, RunArchiveActionStats } from '../../types'
import { formatPercentValue } from '../../utils/numberFormat'

export type MetricPopulation = 'action' | 'transaction' | 'unknown'
export const populationLabels: Record<MetricPopulation, string> = {
  action: 'Requests / actions', transaction: 'Named transactions', unknown: 'Unclassified legacy records',
}
export function defaultReportPopulation(report: RunArchiveReport | null): MetricPopulation {
  if (report && !report.metric_populations) return 'unknown'
  return (['action', 'transaction', 'unknown'] as const).find(key => (report?.metric_populations?.[key]?.transactions ?? 0) > 0) || 'action'
}
/** Old reports contain only mixed records; never relabel them as measured requests. */
export function reportPopulationActions(report: RunArchiveReport | null, population?: MetricPopulation): Record<string, RunArchiveActionStats> {
  if (!report) return {}
  if (population === 'transaction') return report.by_transaction || {}
  if (population === 'action') return report.by_request || {}
  if (population === 'unknown') return report.by_unknown || (!report.metric_populations ? report.by_action : undefined) || {}
  return (report.metric_populations ? report.by_request : report.by_action) || {}
}
export function reportPopulationOverview(report: RunArchiveReport | null, population: MetricPopulation): RunArchiveOverview | undefined {
  if (!report) return undefined
  const metrics = report.metric_populations?.[population]
  if (!metrics || metrics.transactions === 0) return undefined
  return { ...report.overview, ...metrics, p95_ms: metrics.p95_ms, peak_tps: metrics.peak_tps ?? Number.NaN }
}
export function reportAcceptance(report: RunArchiveReport | null) {
  return report?.verdict || { status: 'Not available', failed: false, conditional: true, explanation: 'No acceptance assessment is available.' }
}
/**
 * The one placeholder for a metric with no value. Use it instead of "n/a",
 * "--" or "Not available" so an empty cell reads the same on every surface;
 * while a run is live, say "Awaiting data" instead.
 */
export const MISSING_VALUE = '—'

export function formatReportNumber(value: number | undefined, digits = 1): string {
  if (value === undefined || !Number.isFinite(value)) return MISSING_VALUE
  return value.toLocaleString(undefined, { maximumFractionDigits: digits })
}
export function formatReportPercent(value: number | undefined, digits = 2): string {
  if (value === undefined || !Number.isFinite(value)) return MISSING_VALUE
  return formatPercentValue(value, digits)
}
