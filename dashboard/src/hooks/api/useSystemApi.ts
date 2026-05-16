import { useCallback, useMemo } from 'react'

import { DashboardAuthAccessMatrix, DashboardAuthRoleCatalog, DashboardSystemSettings, HealthResponse, SystemInventoryResponse } from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
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
		getAuthAccessMatrix,
      getAuthRoleCatalog,
      getHealth,
      getSystemInventory,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthAccessMatrix, getAuthRoleCatalog, getHealth, getSystemInventory, getSystemSettings, updateSystemSettings],
  )
}