export type ComparisonChartMetric = 'tps' | 'art' | 'p50' | 'p90' | 'p95' | 'p99' | 'errors'

export type ComparisonChartDataKey =
  | 'leftTps'
  | 'rightTps'
  | 'leftArt'
  | 'rightArt'
  | 'leftP50'
  | 'rightP50'
  | 'leftP90'
  | 'rightP90'
  | 'leftP95'
  | 'rightP95'
  | 'leftP99'
  | 'rightP99'
  | 'leftErrorPct'
  | 'rightErrorPct'

export type ComparisonSummaryLineConfig = {
  dataKey: ComparisonChartDataKey
  name: string
  stroke: string
  yAxisId: 'value'
}

export type ComparisonChartMetricConfig = {
  id: ComparisonChartMetric
  label: string
  leftKey: ComparisonChartDataKey
  rightKey: ComparisonChartDataKey
  description: string
  valueFormat: 'count' | 'ms' | 'percent'
}

export const COMPARISON_CHART_METRICS: ComparisonChartMetricConfig[] = [
  { id: 'tps', label: 'TPS', leftKey: 'leftTps', rightKey: 'rightTps', description: 'transactions per second', valueFormat: 'count' },
  { id: 'art', label: 'ART', leftKey: 'leftArt', rightKey: 'rightArt', description: 'average response time', valueFormat: 'ms' },
  { id: 'p50', label: 'P50', leftKey: 'leftP50', rightKey: 'rightP50', description: 'median response time', valueFormat: 'ms' },
  { id: 'p90', label: 'P90', leftKey: 'leftP90', rightKey: 'rightP90', description: '90th percentile response time', valueFormat: 'ms' },
  { id: 'p95', label: 'P95', leftKey: 'leftP95', rightKey: 'rightP95', description: '95th percentile response time', valueFormat: 'ms' },
  { id: 'p99', label: 'P99', leftKey: 'leftP99', rightKey: 'rightP99', description: '99th percentile response time', valueFormat: 'ms' },
  { id: 'errors', label: 'Errors', leftKey: 'leftErrorPct', rightKey: 'rightErrorPct', description: 'error percentage', valueFormat: 'percent' },
]

export function getComparisonChartMetricConfig(metric: ComparisonChartMetric): ComparisonChartMetricConfig {
  return COMPARISON_CHART_METRICS.find(item => item.id === metric) || COMPARISON_CHART_METRICS[0]
}

export function formatComparisonChartValue(value: unknown, format: ComparisonChartMetricConfig['valueFormat']) {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return 'n/a'
  }
  if (format === 'ms') return `${value.toFixed(1)} ms`
  if (format === 'percent') return `${value.toFixed(2)}%`
  return value.toFixed(1)
}

export function comparisonChartYAxisLabel(metric: ComparisonChartMetricConfig) {
  if (metric.valueFormat === 'ms') return `${metric.label} (ms)`
  if (metric.valueFormat === 'percent') return 'Error rate (%)'
  return metric.label
}

export function buildComparisonSummarySeries(leftRunLabel: string, rightRunLabel: string, metric: ComparisonChartMetric = 'tps'): ComparisonSummaryLineConfig[] {
  const config = getComparisonChartMetricConfig(metric)
  const leftStroke = 'var(--chart-1)'
  const rightStroke = 'var(--chart-2)'
  return [
    { dataKey: config.leftKey, name: `${leftRunLabel} ${config.label}`, stroke: leftStroke, yAxisId: 'value' },
    { dataKey: config.rightKey, name: `${rightRunLabel} ${config.label}`, stroke: rightStroke, yAxisId: 'value' },
  ]
}
