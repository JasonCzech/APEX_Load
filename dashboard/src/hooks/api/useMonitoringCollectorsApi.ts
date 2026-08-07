import { useCallback, useMemo } from 'react'

import type {
  CollectorMetricSnapshot,
  MonitoringCollector,
  MonitoringCollectorListResponse,
  MonitoringCollectorTokenResponse,
} from '../../types/monitoring'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

function buildQuery(params: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

export type MonitoringCollectorPatch = {
  name?: string
  description?: string
  health?: string
  allowed_metric_packs?: string[]
}

export function useMonitoringCollectorsApi() {
  const listCollectors = useCallback(
    (params?: { project_id?: string; limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<MonitoringCollectorListResponse>(`/monitoring/collectors${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getCollector = useCallback(
    (id: string, options?: ApiCallOptions) => request<MonitoringCollector>(`/monitoring/collectors/${id}`, withCallOptions(undefined, options)),
    [],
  )

  // createCollector returns the collector plus a one-time plaintext token.
  const createCollector = useCallback(
    (body: { project_id: string; name: string; description?: string; allowed_metric_packs?: string[] }, options?: ApiCallOptions) =>
      request<MonitoringCollectorTokenResponse>('/monitoring/collectors', withCallOptions({
        method: 'POST',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const patchCollector = useCallback(
    (id: string, patch: MonitoringCollectorPatch, options?: ApiCallOptions) =>
      request<MonitoringCollector>(`/monitoring/collectors/${id}`, withCallOptions({
        method: 'PATCH',
        body: JSON.stringify(patch),
      }, options)),
    [],
  )

  const deleteCollector = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/monitoring/collectors/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  // rotateCollectorToken returns the collector plus a new one-time token.
  const rotateCollectorToken = useCallback(
    (id: string, options?: ApiCallOptions) =>
      request<MonitoringCollectorTokenResponse>(`/monitoring/collectors/${id}/rotate-token`, withCallOptions({
        method: 'POST',
      }, options)),
    [],
  )

  // getCollectorMetrics returns the latest ingested heartbeat snapshot.
  const getCollectorMetrics = useCallback(
    (id: string, options?: ApiCallOptions) => request<CollectorMetricSnapshot>(`/monitoring/collectors/${id}/metrics`, withCallOptions(undefined, options)),
    [],
  )

  return useMemo(
    () => ({
      listCollectors,
      getCollector,
      createCollector,
      patchCollector,
      deleteCollector,
      rotateCollectorToken,
      getCollectorMetrics,
    }),
    [listCollectors, getCollector, createCollector, patchCollector, deleteCollector, rotateCollectorToken, getCollectorMetrics],
  )
}
