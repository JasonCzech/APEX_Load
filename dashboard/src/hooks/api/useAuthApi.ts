import { useCallback, useMemo } from 'react'

import { DashboardAuthStatus } from '../../types'
import { request } from '../apiClient'

type LogoutResponse = {
  redirect_url: string
}

export function useAuthApi() {
  const getAuthStatus = useCallback(() => request<DashboardAuthStatus>('/auth/me'), [])
  const logout = useCallback((csrfToken: string) => request<LogoutResponse>('/auth/logout', {
    method: 'POST',
    headers: { 'X-APEX Load-CSRF-Token': csrfToken },
  }), [])

  return useMemo(
    () => ({
      getAuthStatus,
      logout,
    }),
    [getAuthStatus, logout],
  )
}
