import type { MonitorSource } from '../types/appdynamics'

export const nativeMonitorProvider = (pack?: string) => pack === 'appdynamics' || pack === 'dynatrace'
export function monitorMetricCatalog(source?: MonitorSource): { name: string; unit: string; statistic: string; path?: string }[] {
  if (source?.pack === 'dynatrace') return [
    ...(source.dynatrace?.metrics || []).map(metric => ({ name: metric.name, unit: metric.unit || '', statistic: metric.aggregation === 'avg' ? metric.semantics === 'gauge' ? 'time_average' : 'average' : metric.aggregation, path: metric.metric_selector })),
    ...(source.dynatrace?.grail || []).filter(query => query.kind === 'metric').map(query => ({ name: query.name, unit: query.unit || '', statistic: query.statistic || 'value' })),
  ]
  return [...(source?.appdynamics?.metrics || []), ...(source?.appdynamics?.analytics || []).filter(query => query.kind === 'metric').map(query => ({ name: query.name, unit: query.unit || 'count', statistic: query.statistic || 'value' }))]
}
export function monitorSourceSelectionComplete(source: MonitorSource): boolean {
  if (!nativeMonitorProvider(source.pack)) return Boolean(source.collector_id)
  if (!source.connection_id || source.executor_mode === 'collector' && !source.collector_id) return false
  if (source.pack === 'dynatrace') {
    const selection = source.dynatrace
    const metrics = selection?.metrics || []
    const grail = selection?.grail || []
    return Boolean(selection && (metrics.length || grail.length || selection.problems || selection.events || selection.topology_mode) && metrics.every(metric => metric.name.trim() && metric.metric_selector.trim()) && grail.every(query => query.name.trim() && query.query.trim() && (query.kind !== 'metric' || query.unit?.trim() && query.value_field?.trim() && query.statistic && (!query.query.trim().toLowerCase().startsWith('timeseries ') || (query.bucket_seconds || 0) > 0))))
  }
  const selection = source.appdynamics
  return Boolean(selection?.applications.length && (selection.metrics.length || selection.analytics.length || selection.events || selection.violations || selection.snapshots))
}
