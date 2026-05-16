import { useCallback, useMemo } from 'react'

import { DashboardAuthAccessMatrix, DashboardAuthEffectiveAccess, DashboardAuthRoleCatalog, DashboardSystemSettings, HealthResponse, SystemInventoryResponse } from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
  const getAuthEffectiveAccess = useCallback((projectId?: string) => request<DashboardAuthEffectiveAccess>(`/auth/effective-access${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`), [])
	const getAuthAccessMatrix = useCallback(() => request<DashboardAuthAccessMatrix>('/auth/access-matrix'), [])
  const getAuthRoleCatalog = useCallback(() => request<DashboardAuthRoleCatalog>('/auth/roles'), [])
  const getHealth = useCallback(() => request<HealthResponse>('/system/health'), [])
  const getSystemInventory = useCallback(() => request<SystemInventoryResponse>('/system/inventory'), [])

  const getSystemSettings = useCallback(() => request<DashboardSystemSettings>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: DashboardSystemSettings) => request<DashboardSystemSettings>('/system/settings', { method: 'PUT', body: JSON.stringify(settings) }),
    [],
  )

  return useMemo(
    () => ({
		getAuthEffectiveAccess,
		getAuthAccessMatrix,
      getAuthRoleCatalog,
      getHealth,
      getSystemInventory,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthEffectiveAccess, getAuthAccessMatrix, getAuthRoleCatalog, getHealth, getSystemInventory, getSystemSettings, updateSystemSettings],
  )
}