import type { AnalysisMapping, AnalysisMeasure } from '../types/analysis'

// A summary measure is a statistic the source already computed over a fixed
// window. The server re-buckets it only where that is exact; these helpers
// keep the editor and query panel inside the same rules.

export const defaultAnalysisAggregations = ['avg', 'min', 'max', 'sum', 'count', 'p50', 'p95', 'p99', 'rate']

export const analysisMissingReasonLabels: Record<string, string> = {
  precomputed_percentile_not_mergeable: 'Precomputed percentile spans several source windows and cannot be merged',
  summary_average_without_sample_counts: 'Average spans several source windows without sample counts',
  partial_summary_window: 'A source window crosses the bucket boundary',
  duplicate_summary_window: 'The source reported the same window twice',
}

/** Switching kind keeps summary fields only on summaries, with editable defaults. */
export function withAnalysisMeasureKind(measure: AnalysisMeasure, kind: string, statistics?: string[]): AnalysisMeasure {
  const rest: AnalysisMeasure = { ...measure }
  delete rest.statistic; delete rest.window_seconds; delete rest.sample_count_column; delete rest.sample_sum_column
  if (kind !== 'summary' || !statistics?.length) return { ...rest, kind }
  return { ...rest, kind, statistic: measure.statistic ?? 'avg', window_seconds: measure.window_seconds ?? 60 }
}

export function analysisSummaryMeasureError(measure: AnalysisMeasure, statistics?: string[]): string | undefined {
  if (measure.kind !== 'summary' || !statistics?.length) return
  if (!measure.statistic || !statistics.includes(measure.statistic)) return `${measure.name || measure.value.join('.')}: choose the statistic this summary reports.`
  const window = measure.window_seconds ?? 0
  if (!Number.isSafeInteger(window) || window < 1 || window > 86400) return `${measure.name || measure.value.join('.')}: the summary window must be 1-86400 seconds.`
  if (measure.sample_sum_column?.length && !measure.sample_count_column?.length) return `${measure.name || measure.value.join('.')}: a sample sum needs its sample count column.`
  if (measure.statistic === 'count' && measure.unit !== 'count') return `${measure.name || measure.value.join('.')}: a count summary uses the count unit.`
  return
}

export interface AnalysisSummaryDeclaration { statistic: string; window_seconds: number }

/** Declared statistics of the fixed-name summary measures called metric. */
export function analysisSummaryDeclarations(mapping: AnalysisMapping | undefined, metric: string): AnalysisSummaryDeclaration[] {
  return (mapping?.measures ?? [])
    .filter(m => m.kind === 'summary' && !m.name_column?.length && m.name === metric && m.statistic && m.window_seconds)
    .map(m => ({ statistic: m.statistic as string, window_seconds: m.window_seconds as number }))
}

/** About 500 buckets, widened to whole multiples of a summary source window. */
export function analysisBucketSeconds(rangeSeconds: number, windowSeconds?: number): number {
  const target = Math.max(1, Math.ceil(rangeSeconds / 500))
  if (!windowSeconds) return target
  return Math.ceil(target / windowSeconds) * windowSeconds
}
