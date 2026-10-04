import { useCallback, useMemo } from 'react'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'
import type { PrometheusConnection, PrometheusConnectionInput, PrometheusDashboardDraft, PrometheusInspectionInput, PrometheusOperation } from '../../types/prometheus'

export function usePrometheusConnectionsApi() {
  const listPrometheusConnections = useCallback((project: string, options?: ApiCallOptions) => request<{ connections: PrometheusConnection[]; count: number }>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections`, withCallOptions(undefined, options)), [])
  const createPrometheusConnection = useCallback((project: string, input: PrometheusConnectionInput, options?: ApiCallOptions) => request<PrometheusConnection>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options)), [])
  const updatePrometheusConnection = useCallback((project: string, id: string, input: PrometheusConnectionInput, options?: ApiCallOptions) => request<PrometheusConnection>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(input) }, options)), [])
  const deletePrometheusConnection = useCallback((project: string, id: string, revision: number, options?: ApiCallOptions) => request<void>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}?revision=${revision}`, withCallOptions({ method: 'DELETE' }, options)), [])
  const inspectPrometheusConnection = useCallback((project: string, id: string, operation: 'test' | 'discover' | 'preview', input: PrometheusInspectionInput, options?: ApiCallOptions) => {
    switch (operation) {
      case 'test': return request<PrometheusOperation>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}/test`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options))
      case 'discover': return request<PrometheusOperation>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}/discover`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options))
      case 'preview': return request<PrometheusOperation>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}/preview`, withCallOptions({ method: 'POST', body: JSON.stringify(input) }, options))
    }
  }, [])
  const getPrometheusOperation = useCallback((project: string, id: string, operation: string, options?: ApiCallOptions) => request<PrometheusOperation>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}/operations/${encodeURIComponent(operation)}`, withCallOptions(undefined, options)), [])
  const adoptPrometheusDashboard = useCallback((project: string, id: string, dashboard: string, options?: ApiCallOptions) => request<PrometheusDashboardDraft>(`/projects/${encodeURIComponent(project)}/monitoring/prometheus-connections/${encodeURIComponent(id)}/adopt-dashboard`, withCallOptions({ method: 'POST', body: JSON.stringify({ dashboard_id: dashboard, step_seconds: 15, range_seconds: 300 }) }, options)), [])
  return useMemo(() => ({ listPrometheusConnections, createPrometheusConnection, updatePrometheusConnection, deletePrometheusConnection, inspectPrometheusConnection, getPrometheusOperation, adoptPrometheusDashboard }), [listPrometheusConnections, createPrometheusConnection, updatePrometheusConnection, deletePrometheusConnection, inspectPrometheusConnection, getPrometheusOperation, adoptPrometheusDashboard])
}
