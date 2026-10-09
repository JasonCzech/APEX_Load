import type { IntervalSummaryRecord } from '../types'
import { formatFixed } from '../utils/numberFormat'

export type ComparisonChartMetric = 'tps' | 'art' | 'p50' | 'p90' | 'p95' | 'p99' | 'errors'

export type ComparisonChartDataKey = string

export type ComparisonSummaryLineConfig = {
  dataKey: ComparisonChartDataKey
  name: string
  stroke: string
  yAxisId: 'value'
}

export type ComparisonChartMetricConfig = {
  id: ComparisonChartMetric
  label: string
  description: string
  valueFormat: 'count' | 'ms' | 'percent'
}

export const COMPARISON_CHART_METRICS: ComparisonChartMetricConfig[] = [
  { id: 'tps', label: 'TPS', description: 'transactions per second', valueFormat: 'count' },
  { id: 'art', label: 'ART', description: 'average response time', valueFormat: 'ms' },
  { id: 'p50', label: 'P50', description: 'median response time', valueFormat: 'ms' },
  { id: 'p90', label: 'P90', description: '90th percentile response time', valueFormat: 'ms' },
  { id: 'p95', label: 'P95', description: '95th percentile response time', valueFormat: 'ms' },
  { id: 'p99', label: 'P99', description: '99th percentile response time', valueFormat: 'ms' },
  { id: 'errors', label: 'Errors', description: 'error percentage', valueFormat: 'percent' },
]

const COMPARISON_RUN_STROKES = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--chart-6)',
  '#14b8a6',
  '#f97316',
  '#6366f1',
  '#64748b',
]

export function getComparisonChartMetricConfig(metric: ComparisonChartMetric): ComparisonChartMetricConfig {
  return COMPARISON_CHART_METRICS.find(item => item.id === metric) || COMPARISON_CHART_METRICS[0]
}

export function formatComparisonChartValue(value: unknown, format: ComparisonChartMetricConfig['valueFormat']) {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return 'n/a'
  }
  if (format === 'ms') return `${value.toFixed(1)} ms`
  if (format === 'percent') return `${formatFixed(value, 2)}%`
  return value.toFixed(1)
}

export function comparisonChartYAxisLabel(metric: ComparisonChartMetricConfig) {
  if (metric.valueFormat === 'ms') return `${metric.label} (ms)`
  if (metric.valueFormat === 'percent') return 'Error rate (%)'
  return metric.label
}

export function comparisonRunColor(index: number) {
  return COMPARISON_RUN_STROKES[index % COMPARISON_RUN_STROKES.length]
}

export function comparisonSummaryDataKey(runId: string, metric: ComparisonChartMetric, index: number) {
  const safeId = runId.replace(/[^A-Za-z0-9_]/g, '_') || 'run'
  return `run_${index}_${safeId}_${metric}`
}

export function comparisonChartMetricValue(summary: IntervalSummaryRecord | undefined, metric: ComparisonChartMetric) {
  if (!summary) return undefined
  switch (metric) {
    case 'art':
      return summary.avg_response_ms
    case 'p50':
      return summary.p50_ms
    case 'p90':
      return summary.p90_ms
    case 'p95':
      return summary.p95_ms
    case 'p99':
      return summary.p99_ms
    case 'errors':
      return summary.error_pct
    case 'tps':
    default:
      return summary.tps
  }
}

export function buildComparisonSummarySeries(runs: Array<{ id: string; label: string }>, metric: ComparisonChartMetric = 'tps'): ComparisonSummaryLineConfig[] {
  const config = getComparisonChartMetricConfig(metric)
  return runs.map((run, index) => ({
    dataKey: comparisonSummaryDataKey(run.id, metric, index),
    name: `${run.label} ${config.label}`,
    stroke: comparisonRunColor(index),
    yAxisId: 'value',
  }))
}
