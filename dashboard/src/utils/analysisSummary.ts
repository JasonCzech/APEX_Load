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
  histogram_counter_reset: 'The histogram counter reset during this window',
  histogram_percentile_in_overflow_bin: 'The percentile falls in the unbounded last bin',
  histogram_no_observations: 'No observations in this bucket',
  partial_histogram_window: 'A histogram window crosses the bucket boundary',
  duplicate_histogram_window: 'The source reported the same histogram window twice',
}

export interface AnalysisMeasureCapabilities { summaryStatistics?: string[]; histogramBoundLimit?: number }

export const analysisHistogramStatistics = ['count', 'p50', 'p75', 'p90', 'p95', 'p99', 'p999']

/** Every aggregation a dataset series or comparison may request. */
export const allAnalysisAggregations = Array.from(new Set([...defaultAnalysisAggregations, ...analysisHistogramStatistics]))

/** Switching kind keeps summary or histogram fields only on that kind. A summary's statistic and
 * window are never guessed: the user declares what the source computed. */
export function withAnalysisMeasureKind(measure: AnalysisMeasure, kind: string, capabilities: AnalysisMeasureCapabilities = {}): AnalysisMeasure {
  const rest: AnalysisMeasure = { ...measure }
  delete rest.statistic; delete rest.window_seconds; delete rest.sample_count_column; delete rest.sample_sum_column; delete rest.histogram
  if (kind === 'summary' && capabilities.summaryStatistics?.length) return { ...rest, kind, ...(measure.statistic ? { statistic: measure.statistic } : {}), ...(measure.window_seconds ? { window_seconds: measure.window_seconds } : {}) }
  if (kind === 'histogram' && capabilities.histogramBoundLimit) return { ...rest, kind, histogram: measure.histogram ?? { bounds: ['+Inf'], bucket_counts: 'per_bin', temporality: 'delta', window_seconds: 60, count_columns: [] } }
  return { ...rest, kind }
}

const histogramBound = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$/

/** Parses comma-separated bounds; the last may be +Inf. */
export function parseAnalysisHistogramBounds(text: string): string[] {
  return text.split(',').map(part => part.trim()).filter(Boolean)
}

export function analysisHistogramMeasureError(measure: AnalysisMeasure, limit?: number): string | undefined {
  if (measure.kind !== 'histogram' || !limit) return
  const label = measure.name || measure.value.join('.')
  const h = measure.histogram
  if (!h) return `${label}: declare the histogram bounds.`
  if (h.bounds.length < 1 || h.bounds.length > limit) return `${label}: declare 1-${limit} upper bounds.`
  let previous = -Infinity
  for (const [i, bound] of h.bounds.entries()) {
    const value = bound === '+Inf' && i === h.bounds.length - 1 ? Infinity : histogramBound.test(bound) ? Number(bound) : NaN
    if (Number.isNaN(value) || value <= previous) return `${label}: bounds must be increasing decimal numbers; only the last may be +Inf.`
    previous = value
  }
  if (h.temporality === 'delta' && (!Number.isSafeInteger(h.window_seconds) || (h.window_seconds ?? 0) < 1 || (h.window_seconds ?? 0) > 86400)) return `${label}: a per-window histogram needs a window of 1-86400 seconds.`
  if (h.bound_column !== undefined) return h.bound_column.length ? undefined : `${label}: choose the bound column.`
  if ((h.count_columns ?? []).length !== h.bounds.length - 1 || h.count_columns?.some(path => !path.length)) return `${label}: choose a count column for every bound but the last (the measure column holds the last).`
  return
}

/** Fixed-name histogram measure called metric, if any. */
export function analysisHistogramDeclaration(mapping: AnalysisMapping | undefined, metric: string) {
  return mapping?.measures.find(m => m.kind === 'histogram' && m.histogram && !m.name_column?.length && m.name === metric)?.histogram
}

/** Histogram measures whose metric names come from a name column; their names are not known
 * until the data is read, so a query for any unmatched metric may be one of them. */
export function analysisNameColumnHistograms(mapping: AnalysisMapping | undefined) {
  return (mapping?.measures ?? []).filter(m => m.kind === 'histogram' && m.histogram && m.name_column?.length).map(m => m.histogram!)
}

/** Distinct fixed metric names of a mapping, for a metric picker. */
export function analysisMetricNames(mapping: AnalysisMapping | undefined): string[] {
  return Array.from(new Set((mapping?.measures ?? []).map(m => m.name).filter((name): name is string => !!name)))
}

/** Aggregations a dataset metric can answer, and the source window its buckets must cover. */
export function analysisMetricAggregations(mapping: AnalysisMapping | undefined, metric: string): { options: string[]; windowSeconds?: (aggregation: string) => number | undefined } {
  const histogram = analysisHistogramDeclaration(mapping, metric)
  if (histogram) return { options: analysisHistogramStatistics, windowSeconds: () => histogram.window_seconds }
  const summaries = analysisSummaryDeclarations(mapping, metric)
  if (summaries.length) return { options: summaries.map(d => d.statistic), windowSeconds: aggregation => summaries.find(d => d.statistic === aggregation)?.window_seconds }
  const named = analysisNameColumnHistograms(mapping)
  if (named.length) {
    const windows = Array.from(new Set(named.map(h => h.window_seconds).filter((w): w is number => !!w)))
    return { options: allAnalysisAggregations, windowSeconds: aggregation => analysisHistogramStatistics.includes(aggregation) && windows.length === 1 ? windows[0] : undefined }
  }
  return { options: defaultAnalysisAggregations }
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

/** Snaps a UTC range outward onto the source window grid (multiples of the window since the
 * epoch), so every bucket starts where a source window starts. Without a window the range is
 * unchanged. */
export function analysisWindowAlignedRange(fromMs: number, toMs: number, windowSeconds?: number): { fromMs: number; toMs: number } {
  if (!windowSeconds || windowSeconds < 1) return { fromMs, toMs }
  const step = windowSeconds * 1000
  return { fromMs: Math.floor(fromMs / step) * step, toMs: Math.ceil(toMs / step) * step }
}
