import type { ELKConnection } from '../types/monitoring'
import type { ElasticCapabilities, ElasticMetric, ElasticRecord, ElasticScalar, ElasticSelection } from '../types/elastic'
import type { MonitorSource } from '../types/runMonitoring'

export function elasticScalarValue(value: ElasticScalar): string {
  if (value.kind === 'null') return 'null'
  if (value.kind === 'string') return value.string_value || ''
  if (value.kind === 'number') return value.number_value === undefined ? 'Unavailable' : String(value.number_value)
  return value.bool_value === undefined ? 'Unavailable' : String(value.bool_value)
}
export function elasticCapabilityHealth(capabilities?: ElasticCapabilities): Record<string, { status: string }> | undefined {
  if (!capabilities) return undefined
  return Object.fromEntries(['dsl', 'esql', 'pit', 'metrics', 'logs', 'traces', 'health'].map(name => [name, { status: capabilities.status?.[name] || (capabilities[name as keyof ElasticCapabilities] ? 'supported' : 'unavailable') }]))
}
export function emptyElasticSelection(projectId?: string): ElasticSelection {
  return { project_id: projectId, indices: [], preset: 'ecs', timestamp_field: '@timestamp', ingested_field: 'event.ingested', service_field: 'service.name', run_id_field: '', trace_id_field: 'trace.id', span_id_field: 'span.id', transaction_id_field: 'transaction.id', parent_span_id_field: 'parent.id', message_field: 'message', severity_field: 'log.level', duration_field: 'event.duration', duration_unit: 'ns', status_field: 'event.outcome', logs: true, traces: false, errors: false, cluster_health: false, metrics: [], queries: [], settle_seconds: 60, overlap_seconds: 120, page_size: 100 }
}
export function elasticSelectionHasData(selection?: ElasticSelection): boolean {
  return Boolean(selection && (selection.logs || selection.traces || selection.transactions || selection.errors || selection.cluster_health || selection.kibana_health || selection.logstash_health || selection.metrics?.length || selection.queries?.length))
}
export function elasticSelectionNeedsIndices(selection?: ElasticSelection): boolean {
  return Boolean(selection && (selection.logs || selection.traces || selection.transactions || selection.errors || selection.metrics?.length || selection.queries?.length))
}
export function elasticSourceReady(source: MonitorSource): boolean {
  const selection = source.elastic
  return Boolean(source.connection_id && elasticSelectionHasData(selection) && (!elasticSelectionNeedsIndices(selection) || selection?.indices.length) && (source.executor_mode !== 'collector' || source.collector_id))
}
export function elasticConnectionVerified(connection: ELKConnection): boolean {
  return Boolean(connection.enabled && connection.revision && connection.verified_revision === connection.revision)
}
const normalizedElasticUnit = (unit: string) => ['ns', 'us', 's'].includes(unit) ? 'ms' : unit
export function monitorSourceMetrics(source?: MonitorSource): { name: string; unit: string; statistic: string; bucket_seconds?: number; exact_window?: boolean; final_window_supported?: boolean }[] {
  if (!source) return []
  if (source.pack === 'elastic') return [
    ...(source.elastic?.metrics || []).map(metric => ({ name: metric.name, unit: normalizedElasticUnit(['count', 'cardinality'].includes(metric.statistic) ? 'count' : metric.unit || ''), statistic: metric.statistic, bucket_seconds: metric.bucket_seconds, exact_window: metric.exact_window, final_window_supported: true })),
    ...(source.elastic?.queries || []).filter(query => query.kind === 'metric').map(query => ({ name: query.name, unit: normalizedElasticUnit(['count', 'cardinality'].includes(query.statistic || '') ? 'count' : query.unit || 'count'), statistic: query.statistic || 'value', bucket_seconds: query.bucket_seconds, exact_window: query.exact_window, final_window_supported: query.language === 'dsl' || query.exact_window })),
  ]
  return [...(source.appdynamics?.metrics || []), ...(source.appdynamics?.analytics || []).filter(query => query.kind === 'metric' || query.kind === 'transaction' || !query.kind).map(query => ({ name: query.name, unit: query.unit || 'count', statistic: query.statistic || 'value' }))]
}
export function elasticMetricDefault(): ElasticMetric { return { name: '', statistic: 'average', field: '', kind: 'gauge', unit: 'count', bucket_seconds: 10, group_by: [] } }
export function elasticAttributes(record: ElasticRecord): Record<string, ElasticScalar> { return record.log?.attributes || record.span?.attributes || record.transaction?.attributes || {} }
export function safeElasticDashboardsLink(value?: string): string | undefined {
  if (!value) return undefined
  try { const url = new URL(value); return url.protocol === 'https:' && !url.username && !url.password && !url.search && !url.hash ? url.href : undefined } catch { return undefined }
}

export function monitorOverlayMetrics(source?: MonitorSource): { name: string; unit: string; statistic: string }[] {
  const metrics = monitorSourceMetrics(source)
  const selection = source?.elastic
  if (source?.pack !== 'elastic' || !selection) return metrics
  const health: { name: string; unit: string; statistic: string }[] = []
  const append = (prefix: string, names: [string, string][]) => health.push(...names.map(([name, unit]) => ({ name: prefix + name, unit, statistic: 'instantaneous value' })))
  if (selection.cluster_health) for (const product of ['elasticsearch', 'opensearch']) {
    append(`${product}.cluster.`, [['status', 'ratio'], ['nodes', 'count'], ['active_shards', 'count'], ['unassigned_shards', 'count'], ['pending_tasks', 'count'], ['active_shards_percent', 'percent']])
    append(`${product}.node.`, [['heap_percent', 'percent'], ['heap_bytes', 'bytes'], ['cpu_percent', 'percent'], ['index_total', 'count'], ['query_total', 'count']])
    append(`${product}.index.`, [['documents', 'count'], ['store_bytes', 'bytes'], ['index_total', 'count'], ['query_total', 'count']])
  }
  if (selection.kibana_health) append('kibana.', [['status', 'ratio']])
  if (selection.logstash_health) {
    append('logstash.', [['heap_percent', 'percent'], ['heap_bytes', 'bytes'], ['threads', 'threads'], ['cpu_percent', 'percent'], ['events_in', 'count'], ['events_out', 'count']])
    append('logstash.pipeline.', [['events_in', 'count'], ['events_out', 'count'], ['event_duration_ms', 'ms'], ['queue_events', 'count'], ['queue_bytes', 'bytes']])
  }
  return [...metrics, ...health]
}
