import { useCallback, useMemo } from 'react'

import {
  DashboardAuthAccessMatrix,
  DashboardAuthDiagnostics,
  DashboardAuthEffectiveAccess,
  DashboardAuthRoleCatalog,
  DashboardAuditEventListResponse,
  DashboardAuditEventFilter,
  DashboardCreateServiceAccountRequest,
  DashboardCreateServiceAccountResponse,
  DashboardServiceAccountListResponse,
  DashboardSystemSettings,
  DashboardUpdateServiceAccountRequest,
  HealthResponse,
  SystemInventoryResponse,
} from '../../types'
import { request } from '../apiClient'

export function useSystemApi() {
  const getAuthEffectiveAccess = useCallback((projectId?: string) => request<DashboardAuthEffectiveAccess>(`/auth/effective-access${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`), [])
	const getAuthAccessMatrix = useCallback(() => request<DashboardAuthAccessMatrix>('/auth/access-matrix'), [])
  const getAuthDiagnostics = useCallback(() => request<DashboardAuthDiagnostics>('/auth/diagnostics'), [])
  const getAuthRoleCatalog = useCallback(() => request<DashboardAuthRoleCatalog>('/auth/roles'), [])
  const listAuthServiceAccounts = useCallback(() => request<DashboardServiceAccountListResponse>('/auth/service-accounts'), [])
  const createAuthServiceAccount = useCallback(
    (payload: DashboardCreateServiceAccountRequest) => request<DashboardCreateServiceAccountResponse>('/auth/service-accounts', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const updateAuthServiceAccount = useCallback(
    (id: string, payload: DashboardUpdateServiceAccountRequest) => request(`/auth/service-accounts/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
    [],
  )
  const rotateAuthServiceAccount = useCallback(
    (id: string) => request<DashboardCreateServiceAccountResponse>(`/auth/service-accounts/${encodeURIComponent(id)}/rotate`, { method: 'POST' }),
    [],
  )
  const deleteAuthServiceAccount = useCallback((id: string) => request<{ deleted: string }>(`/auth/service-accounts/${encodeURIComponent(id)}`, { method: 'DELETE' }), [])
  const listAuthAuditEvents = useCallback((filter: DashboardAuditEventFilter = {}) => {
    const params = new URLSearchParams()
    const entries = Object.entries(filter) as Array<[keyof DashboardAuditEventFilter, string | number | undefined]>
    entries.forEach(([key, value]) => {
      if (value === undefined || value === '') {
        return
      }
      params.set(key, String(value))
    })
    if (!params.has('limit')) {
      params.set('limit', '20')
    }
    return request<DashboardAuditEventListResponse>(`/auth/audit-events?${params.toString()}`)
  }, [])
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
      getAuthDiagnostics,
      getAuthRoleCatalog,
      listAuthServiceAccounts,
      createAuthServiceAccount,
      updateAuthServiceAccount,
      rotateAuthServiceAccount,
      deleteAuthServiceAccount,
      listAuthAuditEvents,
      getHealth,
      getSystemInventory,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthEffectiveAccess, getAuthAccessMatrix, getAuthDiagnostics, getAuthRoleCatalog, listAuthServiceAccounts, createAuthServiceAccount, updateAuthServiceAccount, rotateAuthServiceAccount, deleteAuthServiceAccount, listAuthAuditEvents, getHealth, getSystemInventory, getSystemSettings, updateSystemSettings],
  )
}
