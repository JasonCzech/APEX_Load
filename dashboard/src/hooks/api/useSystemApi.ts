import { useCallback, useMemo } from 'react'

import { request } from '../apiClient'

export function useSystemApi() {
  const getHealth = useCallback(() => request<any>('/system/health'), [])

  const getSystemSettings = useCallback(() => request<any>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: any) => request<any>('/system/settings', { method: 'PUT', body: JSON.stringify(settings) }),
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