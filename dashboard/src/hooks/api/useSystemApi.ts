import { useCallback, useMemo } from 'react'

import { DashboardSystemSettings, HealthResponse, SystemInventoryResponse } from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
  const getHealth = useCallback(() => request<HealthResponse>('/system/health'), [])
  const getSystemInventory = useCallback(() => request<SystemInventoryResponse>('/system/inventory'), [])

  const getSystemSettings = useCallback(() => request<DashboardSystemSettings>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: DashboardSystemSettings) => request<DashboardSystemSettings>('/system/settings', { method: 'PUT', body: JSON.stringify(settings) }),
    [],
  )

  return useMemo(
    () => ({
      getHealth,
      getSystemInventory,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getHealth, getSystemInventory, getSystemSettings, updateSystemSettings],
  )
}