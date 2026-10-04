import { useCallback, useMemo } from 'react'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'
import type { AppDynamicsConnection, AppDynamicsConnectionInput, AppDynamicsOperation } from '../../types/appdynamics'

export function useAppDynamicsConnectionsApi() {
  const listAppDynamicsConnections = useCallback((project: string, options?: ApiCallOptions) => request<{ connections: AppDynamicsConnection[]; count: number }>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections`, withCallOptions(undefined, options)), [])
  const createAppDynamicsConnection = useCallback((project: string, input: AppDynamicsConnectionInput, options?: ApiCallOptions) => request<AppDynamicsConnection>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options)), [])
  const updateAppDynamicsConnection = useCallback((project: string, id: string, input: AppDynamicsConnectionInput, options?: ApiCallOptions) => request<AppDynamicsConnection>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(input) }, options)), [])
  const deleteAppDynamicsConnection = useCallback((project: string, id: string, revision: number, options?: ApiCallOptions) => request<void>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}?revision=${revision}`, withCallOptions({ method: 'DELETE' }, options)), [])
  const testAppDynamicsConnection = useCallback((project: string, id: string, input: { collection_mode: 'direct' | 'collector'; collector_agent_id?: string; collector_public_key?: string }, options?: ApiCallOptions) => request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/test`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options)), [])
  const discoverAppDynamicsEntities = useCallback((project: string, id: string, kind: 'applications' | 'tiers' | 'nodes' | 'business-transactions' | 'backends' | 'metrics', query: Record<string, string> = {}, options?: ApiCallOptions) => {
    const queryString = new URLSearchParams(query)
    const callOptions = withCallOptions(undefined, options)
    switch (kind) {
      case 'applications': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/applications?${queryString}`, callOptions)
      case 'tiers': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/tiers?${queryString}`, callOptions)
      case 'nodes': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/nodes?${queryString}`, callOptions)
      case 'business-transactions': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/business-transactions?${queryString}`, callOptions)
      case 'backends': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/backends?${queryString}`, callOptions)
      case 'metrics': return request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/metrics?${queryString}`, callOptions)
    }
  }, [])
  const getAppDynamicsOperation = useCallback((project: string, id: string, operation: string, options?: ApiCallOptions) => request<AppDynamicsOperation>(`/projects/${encodeURIComponent(project)}/monitoring/appdynamics-connections/${encodeURIComponent(id)}/operations/${encodeURIComponent(operation)}`, withCallOptions(undefined, options)), [])
  return useMemo(() => ({ listAppDynamicsConnections, createAppDynamicsConnection, updateAppDynamicsConnection, deleteAppDynamicsConnection, testAppDynamicsConnection, discoverAppDynamicsEntities, getAppDynamicsOperation }), [listAppDynamicsConnections, createAppDynamicsConnection, updateAppDynamicsConnection, deleteAppDynamicsConnection, testAppDynamicsConnection, discoverAppDynamicsEntities, getAppDynamicsOperation])
}
