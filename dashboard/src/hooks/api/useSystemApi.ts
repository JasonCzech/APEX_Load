import { useCallback, useMemo } from 'react'

import { DashboardSystemSettings, HealthResponse } from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
  const getHealth = useCallback(() => request<HealthResponse>('/system/health'), [])

  const getSystemSettings = useCallback(() => request<DashboardSystemSettings>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: DashboardSystemSettings) => request<DashboardSystemSettings>('/system/settings', { method: 'PUT', body: JSON.stringify(settings) }),
    [],
  )

  return useMemo(
    () => ({
      getHealth,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getHealth, getSystemSettings, updateSystemSettings],
  )
}