import { useCallback, useMemo } from 'react'

import type { DynatraceConnection, DynatraceConnectionListResponse, DynatraceMetricQuery } from '../../types/monitoring'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

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
}

export function useDynatraceConnectionsApi() {
  const listDynatraceConnections = useCallback((projectId: string, options?: ApiCallOptions) => request<DynatraceConnectionListResponse>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections`, withCallOptions(undefined, options)), [])
  const createDynatraceConnection = useCallback((projectId: string, body: DynatraceConnectionInput, options?: ApiCallOptions) => request<DynatraceConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)), [])
  const updateDynatraceConnection = useCallback((projectId: string, id: string, body: DynatraceConnectionInput, options?: ApiCallOptions) => request<DynatraceConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)), [])
  const deleteDynatraceConnection = useCallback((projectId: string, id: string, options?: ApiCallOptions) => request<void>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'DELETE' }, options)), [])
  const testDynatraceConnection = useCallback((projectId: string, id: string, options?: ApiCallOptions) => request<{ status: string; checked_at: string; message: string; capabilities: Record<string, { status: string; message?: string }> }>(`/projects/${encodeURIComponent(projectId)}/monitoring/dynatrace-connections/${encodeURIComponent(id)}/test`, withCallOptions({ method: 'POST' }, options)), [])
  return useMemo(() => ({ listDynatraceConnections, createDynatraceConnection, updateDynatraceConnection, deleteDynatraceConnection, testDynatraceConnection }), [listDynatraceConnections, createDynatraceConnection, updateDynatraceConnection, deleteDynatraceConnection, testDynatraceConnection])
}
