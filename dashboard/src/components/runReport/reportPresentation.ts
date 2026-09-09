import type { RunArchiveReport, RunArchiveOverview } from '../../types'

export type MetricPopulation = 'action' | 'transaction' | 'unknown'
export const populationLabels: Record<MetricPopulation, string> = {
  action: 'Requests / actions', transaction: 'Named transactions', unknown: 'Unclassified legacy records',
}
export function defaultReportPopulation(report: RunArchiveReport | null): MetricPopulation {
  return (['action', 'transaction', 'unknown'] as const).find(key => (report?.metric_populations?.[key]?.transactions ?? 0) > 0) || 'action'
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
export function formatReportNumber(value: number | undefined, digits = 1): string {
  if (value === undefined || !Number.isFinite(value)) return 'Not available'
  return value.toLocaleString(undefined, { maximumFractionDigits: digits })
}
export function formatReportPercent(value: number | undefined, digits = 2): string {
  if (value === undefined || !Number.isFinite(value)) return 'Not available'
  if (value > 0 && value < 10 ** -digits) return `<${(10 ** -digits).toFixed(digits)}%`
  return `${value.toFixed(digits)}%`
}
