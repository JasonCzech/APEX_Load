import { useCallback, useMemo } from 'react'

import { DashboardAuthStatus } from '../../types'
import { request } from '../apiClient'

export function useAuthApi() {
  const getAuthStatus = useCallback(() => request<DashboardAuthStatus>('/auth/me'), [])

  return useMemo(
    () => ({
      getAuthStatus,
    }),
    [getAuthStatus],
  )
}