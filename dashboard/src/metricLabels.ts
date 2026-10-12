/**
 * One name and unit per metric family, shared by Command Center, Dashboard,
 * Results and the report. Headers carry the unit in parentheses ("P95 (ms)");
 * values carry it after the number ("1,006 ms", via formatMs).
 */
export const METRIC_LABELS = {
  tableOpMean: { label: 'Average table operation', unit: 'ms' },
  tableOpMax: { label: 'Maximum table operation', unit: 'ms' },
  avg: { label: 'Avg', unit: 'ms' },
  art: { label: 'ART', unit: 'ms' },
  p50: { label: 'P50', unit: 'ms' },
  p90: { label: 'P90', unit: 'ms' },
  p95: { label: 'P95', unit: 'ms' },
  p99: { label: 'P99', unit: 'ms' },
  tps: { label: 'TPS', unit: '' },
  errorRate: { label: 'Error rate', unit: '%' },
  vusers: { label: 'VUsers', unit: '' },
} as const

export type MetricLabelId = keyof typeof METRIC_LABELS

/** A column or legend header: the metric name with its unit, e.g. "P95 (ms)". */
export function metricHeader(id: MetricLabelId): string {
  const { label, unit } = METRIC_LABELS[id]
  return unit ? `${label} (${unit})` : label
}
