import { useCallback, useMemo } from 'react'

import { DashboardAuthRoleCatalog, DashboardSystemSettings, HealthResponse, SystemInventoryResponse } from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
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
      getAuthRoleCatalog,
      getHealth,
      getSystemInventory,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthRoleCatalog, getHealth, getSystemInventory, getSystemSettings, updateSystemSettings],
  )
}