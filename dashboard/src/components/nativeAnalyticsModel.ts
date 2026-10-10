import type { MetricsHistoryPoint } from '../types'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'

export const nativeMetrics: Record<string, { label: string; field: keyof MetricsHistoryPoint; aggregations: string[] }> = {
  tps: { label: 'Transactions per second', field: 'tps', aggregations: ['avg'] },
  response_time: { label: 'Response time (ms)', field: 'avgMs', aggregations: ['avg', 'p50', 'p90', 'p95', 'p99'] },
  error_rate: { label: 'Error rate (%)', field: 'errorPct', aggregations: ['avg'] },
  active_vusers: { label: 'Active VUsers', field: 'vusers', aggregations: ['avg'] },
  resource_cpu: { label: 'Process CPU (%)', field: 'cpuProcess', aggregations: ['avg'] },
  resource_memory: { label: 'Process memory (MB)', field: 'memProcess', aggregations: ['avg'] },
}

export function nativePanelRows(panel: DashboardDefinitionPanel, history: MetricsHistoryPoint[]) {
  const query = panel.query
  const metricName = query?.metric === 'throughput' ? 'tps' : query?.metric === 'transaction_duration' ? 'response_time' : query?.metric
  const metric = metricName && nativeMetrics[metricName]
  // Unsupported saved contracts must never silently show an unfiltered total.
  if (query?.population || !query || !metric || !metric.aggregations.includes(query.aggregation) || query.dimensions?.length || Object.keys(query.filters || {}).length || query.bucket_seconds || query.overlay) return null
  const field = metricName === 'response_time' && query.aggregation !== 'avg' ? query.aggregation : metric.field
  return history.map(point => ({ time: point.time, value: point.isGapBoundary || point.resourceSampleOnly && !query.metric.startsWith('resource_') ? undefined : typeof point[field] === 'number' && Number.isFinite(point[field]) ? point[field] as number : undefined }))
}

// Browser document metrics read final document measurements in saved archive
// panels; they are not offered for action or transaction populations.
export const browserDocumentPanelMetrics: Record<string, { label: string; unit: string }> = {
 document_fcp: { label: 'First contentful paint', unit: 'ms' },
 document_lcp: { label: 'Largest contentful paint', unit: 'ms' },
 document_cls: { label: 'Cumulative layout shift', unit: 'score' },
 document_inp: { label: 'Interaction to next paint', unit: 'ms' },
}

// Browser document metrics (BI-4 browser populations): final qualified
// document measurements of one measurement tuple, as merged distributions.
export const nativeBrowserDocumentMetrics: Record<string, { label: string; aggregations: string[]; unit: string }> = {
 document_lcp: { ...browserDocumentPanelMetrics.document_lcp, aggregations: ['p75', 'p50', 'p90', 'p95', 'p99'] },
 document_fcp: { ...browserDocumentPanelMetrics.document_fcp, aggregations: ['p75', 'p50', 'p90', 'p95', 'p99'] },
 document_inp: { ...browserDocumentPanelMetrics.document_inp, aggregations: ['p75', 'p50', 'p90', 'p95', 'p99'] },
 document_cls: { ...browserDocumentPanelMetrics.document_cls, aggregations: ['p75', 'p50', 'p90', 'p95', 'p99'] },
}
export const nativePopulationMetrics: Record<string, { label: string; aggregations: string[]; unit: string }> = {
 response_time: { ...nativeMetrics.response_time, unit: 'ms' },
 error_rate: { ...nativeMetrics.error_rate, unit: '%' },
 observation_count: { label: 'Observation count', aggregations: ['count'], unit: 'observations' },
 observation_rate: { label: 'Retained observations / second', aggregations: ['rate'], unit: 'observations/s' },
 error_observation_rate: { label: 'Failed retained observations / second', aggregations: ['rate'], unit: 'observations/s' },
 success_observation_rate: { label: 'Successful retained observations / second', aggregations: ['rate'], unit: 'observations/s' },
}
