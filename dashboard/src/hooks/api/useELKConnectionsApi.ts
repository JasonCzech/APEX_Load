import { useCallback, useMemo } from 'react'

import type { ELKConnection, ELKConnectionListResponse, ELKMetricAggregation } from '../../types/monitoring'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

export type ELKConnectionInput = {
  name: string
  description: string
  enabled: boolean
  endpoint: string
  index_pattern: string
  timestamp_field: string
  level_field: string
  message_field: string
  test_id_field: string
  trace_id_field: string
  base_filter: string
  error_filter: string
  auth_type: 'api_key' | 'basic' | ''
  api_key_secret_name?: string
  username_secret_name?: string
  password_secret_name?: string
  metric_aggregations: ELKMetricAggregation[]
  updated_at?: string
}

export function useELKConnectionsApi() {
  const listELKConnections = useCallback((projectId: string, options?: ApiCallOptions) =>
    request<ELKConnectionListResponse>(`/projects/${encodeURIComponent(projectId)}/monitoring/elk-connections`, withCallOptions(undefined, options)), [])
  const createELKConnection = useCallback((projectId: string, body: ELKConnectionInput, options?: ApiCallOptions) =>
    request<ELKConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/elk-connections`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)), [])
  const updateELKConnection = useCallback((projectId: string, id: string, body: ELKConnectionInput, options?: ApiCallOptions) =>
    request<ELKConnection>(`/projects/${encodeURIComponent(projectId)}/monitoring/elk-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)), [])
  const deleteELKConnection = useCallback((projectId: string, id: string, options?: ApiCallOptions) =>
    request<void>(`/projects/${encodeURIComponent(projectId)}/monitoring/elk-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'DELETE' }, options)), [])
  const testELKConnection = useCallback((projectId: string, id: string, options?: ApiCallOptions) =>
    request<{ status: string; checked_at: string; message: string; matching_documents: number }>(`/projects/${encodeURIComponent(projectId)}/monitoring/elk-connections/${encodeURIComponent(id)}/test`, withCallOptions({ method: 'POST' }, options)), [])
  return useMemo(() => ({ listELKConnections, createELKConnection, updateELKConnection, deleteELKConnection, testELKConnection }),
    [listELKConnections, createELKConnection, updateELKConnection, deleteELKConnection, testELKConnection])
}
