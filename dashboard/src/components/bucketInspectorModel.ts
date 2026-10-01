import type { DashboardAnalyticsQuery } from '../hooks/api/useDashboardDefinitionsApi'
import type { TransactionEvent } from '../types'

export interface InspectorSelection { run: string; from: string; to: string; group?: string; name?: string }
export function bucketInspectorParams(query: DashboardAnalyticsQuery, selection: InspectorSelection, page: number) {
  const params = new URLSearchParams({ population: query.population || 'action', action_exact: query.filters?.action || '', property_name: query.property_name || '', property_value: query.property_value || '', time_from: selection.from, time_to: selection.to, time_to_exclusive: 'true', limit: '25', offset: String(page * 25) })
  for (const key of ['network_profile', 'hierarchy', 'phase']) if (query.filters?.[key]) params.set(key, query.filters[key])
  if (selection.name && selection.group) {
    if (selection.group === 'action') params.set('action_exact', selection.name)
    else if (selection.group === 'property') params.set('property_value', selection.name.slice((query.property_name || '').length + 1))
    else { params.set(selection.group === 'dependency' ? 'dependency_target' : selection.group, selection.name); params.set('group_dimension', selection.group); params.set('group_value', selection.name) }
  }
  return params
}
export function transactionWaterfall(event: TransactionEvent) {
  // TTFB is an elapsed milestone; DNS/connect/TLS are retained phase durations.
  // Unknown phases are not reconstructed from total RPC/transaction duration.
  return [['DNS', event.dns_ms], ['Connect', event.connect_ms], ['TLS', event.tls_ms], ['Time to first byte (milestone)', event.ttfb_ms], ['Total observation', event.duration_ms]] as const
}
