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
  ParityStatusResponse,
  SystemInventoryResponse,
} from '../../types'
import { BASE, request } from '../apiClient'

function buildQuery(params: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()

  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })

  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

async function fetchBlob(path: string): Promise<Blob> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

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
    return request<DashboardAuditEventListResponse>(`/auth/audit-events${buildQuery({ limit: 20, ...filter })}`)
  }, [])
  const downloadAuthAuditEvents = useCallback((filter: DashboardAuditEventFilter = {}) => fetchBlob(`/auth/audit-events/export${buildQuery({ limit: 1000, ...filter })}`), [])
  const getHealth = useCallback(() => request<HealthResponse>('/system/health'), [])
  const getSystemInventory = useCallback(() => request<SystemInventoryResponse>('/system/inventory'), [])
  const getParityStatus = useCallback(() => request<ParityStatusResponse>('/system/parity-status'), [])

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
      downloadAuthAuditEvents,
      getHealth,
      getSystemInventory,
      getParityStatus,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthEffectiveAccess, getAuthAccessMatrix, getAuthDiagnostics, getAuthRoleCatalog, listAuthServiceAccounts, createAuthServiceAccount, updateAuthServiceAccount, rotateAuthServiceAccount, deleteAuthServiceAccount, listAuthAuditEvents, downloadAuthAuditEvents, getHealth, getSystemInventory, getParityStatus, getSystemSettings, updateSystemSettings],
  )
}
