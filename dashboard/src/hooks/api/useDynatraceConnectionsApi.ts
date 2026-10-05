import { useCallback, useMemo } from 'react'

import type { DynatraceConnection, DynatraceConnectionListResponse, DynatraceMetricQuery } from '../../types/monitoring'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'
import type { DynatraceOperation, DynatraceOperationRequest } from '../../types/dynatrace'

export type DynatraceConnectionInput = {
  name: string; description: string; enabled: boolean
  environment_url: string; platform_url: string
  metrics_enabled: boolean; problems_enabled: boolean; logs_enabled: boolean
  environment_token_secret?: string
  platform_auth_type: 'platform_token' | 'oauth_client' | ''
  platform_token_secret?: string; oauth_client_id_secret?: string; oauth_client_secret_secret?: string
  oauth_resource: string; oauth_scopes: string[]; metric_queries: DynatraceMetricQuery[]
  problem_selector: string; problem_entity_selector: string
  log_base_filter: string; log_test_id_field: string; log_timestamp_field: string; log_status_field: string; log_message_field: string; log_trace_id_field: string
  poll_interval_seconds: number; updated_at?: string
  expected_revision?: number; collection_mode?: 'direct' | 'collector'; collector_agent_id?: string
  events_enabled?: boolean; spans_enabled?: boolean; grail_metrics_enabled?: boolean; grail_events_enabled?: boolean; topology_mode?: '' | 'classic' | 'smartscape'
  requests_per_minute?: number; platform_requests_per_minute?: number; max_scan_bytes?: number; max_run_scan_bytes?: number; drain_reserve_bytes?: number
}

export function useDynatraceConnectionsApi() {
  const listDynatraceConnections = useCallback((projectId: string, options?: ApiCallOptions) => request<DynatraceConnectionListResponse>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections`, withCallOptions(undefined, options)), [])
  const createDynatraceConnection = useCallback((projectId: string, body: DynatraceConnectionInput, options?: ApiCallOptions) => request<DynatraceConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)), [])
  const updateDynatraceConnection = useCallback((projectId: string, id: string, body: DynatraceConnectionInput, options?: ApiCallOptions) => request<DynatraceConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)), [])
  const deleteDynatraceConnection = useCallback((projectId: string, id: string, revision?: number, options?: ApiCallOptions) => request<void>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}${revision ? `?expected_revision=${revision}` : ''}`, withCallOptions({ method: 'DELETE' }, options)), [])
  const testDynatraceConnection = useCallback((projectId: string, id: string, options?: ApiCallOptions) => request<{ status: string; checked_at: string; message: string; capabilities: Record<string, { status: string; message?: string }> }>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}/test`, withCallOptions({ method: 'POST' }, options)), [])
  const createDynatraceOperation = useCallback((projectId: string, id: string, body: DynatraceOperationRequest, options?: ApiCallOptions) => request<DynatraceOperation>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}/operations`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)), [])
  const getDynatraceOperation = useCallback((projectId: string, id: string, operation: string, options?: ApiCallOptions) => request<DynatraceOperation>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}/operations/${encodeURIComponent(operation)}`, withCallOptions(undefined, options)), [])
  return useMemo(() => ({ listDynatraceConnections, createDynatraceConnection, updateDynatraceConnection, deleteDynatraceConnection, testDynatraceConnection, createDynatraceOperation, getDynatraceOperation }), [listDynatraceConnections, createDynatraceConnection, updateDynatraceConnection, deleteDynatraceConnection, testDynatraceConnection, createDynatraceOperation, getDynatraceOperation])
}
