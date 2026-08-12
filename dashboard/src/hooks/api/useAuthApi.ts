import { useCallback, useMemo } from 'react'

import { DashboardAuthStatus } from '../../types'
import { request } from '../apiClient'

type LogoutResponse = {
  redirect_url: string
}

type LocalLoginResponse = {
  authenticated: boolean
}

export function useAuthApi() {
  const getAuthStatus = useCallback(() => request<DashboardAuthStatus>('/auth/me'), [])
  const login = useCallback((username: string, password: string) => request<LocalLoginResponse>('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  }), [])
  const logout = useCallback((csrfToken: string) => request<LogoutResponse>('/auth/logout', {
    method: 'POST',
    headers: { 'X-APEXLoad-CSRF-Token': csrfToken },
  }), [])

  return useMemo(
    () => ({
      getAuthStatus,
      login,
      logout,
    }),
    [getAuthStatus, login, logout],
  )
}
