import { elasticSourceReady, monitorOverlayMetrics } from './elasticMonitoringModel'
import { prometheusQueryValid } from './prometheusConnectionModel'
import type { MonitorSource } from '../types/runMonitoring'

export const nativeMonitorProvider = (pack?: string) => pack === 'dynatrace' || pack === 'prometheus-query' || pack === 'elastic'
export function monitorMetricCatalog(source?: MonitorSource): { name: string; unit: string; statistic: string; path?: string; exact_window?: boolean; final_window_supported?: boolean }[] {
  if (source?.pack === 'elastic') return monitorOverlayMetrics(source)
  if (source?.pack === 'prometheus-query') return (source.prometheus?.queries || []).map(query => ({ name: query.name, unit: query.unit, statistic: query.semantics, path: query.semantics }))
  if (source?.pack === 'dynatrace') return [
    ...(source.dynatrace?.metrics || []).map(metric => ({ name: metric.name, unit: metric.unit || '', statistic: metric.aggregation === 'avg' ? metric.semantics === 'gauge' ? 'time_average' : 'average' : metric.aggregation, path: metric.metric_selector })),
    ...(source.dynatrace?.grail || []).filter(query => query.kind === 'metric').map(query => ({ name: query.name, unit: query.unit || '', statistic: query.statistic || 'value' })),
  ]
  return []
}
export function monitorSourceSelectionComplete(source: MonitorSource): boolean {
  if (source.pack === 'elastic') return elasticSourceReady(source)
  if (!nativeMonitorProvider(source.pack)) return Boolean(source.collector_id)
  if (!source.connection_id || source.executor_mode === 'collector' && !source.collector_id) return false
  if (source.pack === 'prometheus-query') return Boolean(source.prometheus?.queries.length && source.prometheus.queries.every(prometheusQueryValid))
  if (source.pack === 'dynatrace') {
    const selection = source.dynatrace
    const metrics = selection?.metrics || []
    const grail = selection?.grail || []
    return Boolean(selection && (metrics.length || grail.length || selection.problems || selection.events || selection.topology_mode) && metrics.every(metric => metric.name.trim() && metric.metric_selector.trim()) && grail.every(query => query.name.trim() && query.query.trim() && (query.kind !== 'metric' || query.unit?.trim() && query.value_field?.trim() && query.statistic && (!query.query.trim().toLowerCase().startsWith('timeseries ') || (query.bucket_seconds || 0) > 0))))
  }
  return false
}
