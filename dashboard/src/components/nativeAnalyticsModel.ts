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
  if (!query || !metric || !metric.aggregations.includes(query.aggregation) || query.dimensions?.length || Object.keys(query.filters || {}).length || query.bucket_seconds || query.overlay) return null
  const field = metricName === 'response_time' && query.aggregation !== 'avg' ? query.aggregation : metric.field
  return history.map(point => ({ time: point.time, value: point.isGapBoundary || point.resourceSampleOnly && !query.metric.startsWith('resource_') ? undefined : typeof point[field] === 'number' && Number.isFinite(point[field]) ? point[field] as number : undefined }))
}
