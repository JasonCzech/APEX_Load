import { useCallback, useMemo } from 'react'

import {
  CollectorMetricSnapshot,
  MonitoringCollector,
  MonitoringCollectorListResponse,
  MonitoringCollectorTokenResponse,
} from '../../types'
import { request } from '../apiClient'

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
    (params?: { project_id?: string; limit?: number; offset?: number }) =>
      request<MonitoringCollectorListResponse>(`/monitoring/collectors${buildQuery({ ...params })}`),
    [],
  )

  const getCollector = useCallback(
    (id: string) => request<MonitoringCollector>(`/monitoring/collectors/${id}`),
    [],
  )

  // createCollector returns the collector plus a one-time plaintext token.
  const createCollector = useCallback(
    (body: { project_id: string; name: string; description?: string; allowed_metric_packs?: string[] }) =>
      request<MonitoringCollectorTokenResponse>('/monitoring/collectors', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const patchCollector = useCallback(
    (id: string, patch: MonitoringCollectorPatch) =>
      request<MonitoringCollector>(`/monitoring/collectors/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(patch),
      }),
    [],
  )

  const deleteCollector = useCallback(
    (id: string) => request<{ deleted: string }>(`/monitoring/collectors/${id}`, { method: 'DELETE' }),
    [],
  )

  // rotateCollectorToken returns the collector plus a new one-time token.
  const rotateCollectorToken = useCallback(
    (id: string) =>
      request<MonitoringCollectorTokenResponse>(`/monitoring/collectors/${id}/rotate-token`, {
        method: 'POST',
      }),
    [],
  )

  // getCollectorMetrics returns the latest ingested heartbeat snapshot.
  const getCollectorMetrics = useCallback(
    (id: string) => request<CollectorMetricSnapshot>(`/monitoring/collectors/${id}/metrics`),
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
