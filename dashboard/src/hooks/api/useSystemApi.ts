import { useCallback, useMemo } from 'react'

import {
  DashboardAuthAccessMatrix,
  DashboardAuthDiagnostics,
  DashboardAuthEffectiveAccess,
  DashboardAuthRoleCatalog,
  ArchiveRetentionPlanResponse,
  DashboardAuditEventListResponse,
  DashboardAuditEventFilter,
  DashboardCreateServiceAccountRequest,
  DashboardCreateServiceAccountResponse,
  DashboardServiceAccountListResponse,
  DashboardSystemSettings,
  DashboardUpdateServiceAccountRequest,
  APMIntegrationProfilesResponse,
  BrowserBenchmarkEvidenceResponse,
  ExternalResultImportsResponse,
  FleetAlertsResponse,
  FleetCapacityResponse,
  HealthResponse,
  InfrastructureMonitorPacksResponse,
  ParityStatusResponse,
  ReleaseSLAGovernanceResponse,
  ReportProfilesResponse,
  SystemInventoryResponse,
  SystemReadinessResponse,
  UsageAccountingResponse,
  UsageAccountingSnapshot,
  UsageAccountingSnapshotsResponse,
} from '../../types'
import { apiFetch, request } from '../apiClient'

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
  const res = await apiFetch(path)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useSystemApi() {
  const getAuthEffectiveAccess = useCallback((projectId?: string, scope?: { domain_id?: string; application_repository_id?: string }) => {
    const q = new URLSearchParams()
    if (projectId) q.set('project_id', projectId)
    if (scope?.domain_id) q.set('domain_id', scope.domain_id)
    if (scope?.application_repository_id) q.set('application_repository_id', scope.application_repository_id)
    const query = q.toString()
    return request<DashboardAuthEffectiveAccess>(`/auth/effective-access${query ? `?${query}` : ''}`)
  }, [])
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
  const getSystemReadiness = useCallback(() => request<SystemReadinessResponse>('/system/readiness'), [])
  const getParityStatus = useCallback(() => request<ParityStatusResponse>('/system/parity-status'), [])
  const getBrowserBenchmarks = useCallback(() => request<BrowserBenchmarkEvidenceResponse>('/system/browser-benchmarks'), [])
  const getAPMIntegrationProfiles = useCallback(() => request<APMIntegrationProfilesResponse>('/integrations/apm/profiles'), [])
  const getInfrastructureMonitorPacks = useCallback(() => request<InfrastructureMonitorPacksResponse>('/monitoring/packs'), [])
  const getReportProfiles = useCallback(() => request<ReportProfilesResponse>('/reports/profiles'), [])
  const getReleaseSLAGovernance = useCallback((releaseId: string) => request<ReleaseSLAGovernanceResponse>(`/releases/${encodeURIComponent(releaseId)}/sla-governance`), [])
  const createReleaseSLAWaiver = useCallback(
    (releaseId: string, payload: { scope?: string; reason: string; approved_by?: string; expires_at?: string }) =>
      request(`/releases/${encodeURIComponent(releaseId)}/sla-governance/waivers`, { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const listExternalResultImports = useCallback((params: { project_id?: string; release_id?: string; provider?: string } = {}) =>
    request<ExternalResultImportsResponse>(`/results/imports${buildQuery(params)}`),
  [])
  const importExternalResults = useCallback(
    (payload: unknown) => request('/results/import', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const getFleetAlerts = useCallback(() => request<FleetAlertsResponse>('/fleet/alerts'), [])
  const getFleetCapacity = useCallback((params: { project_id?: string; region?: string; protocol?: string } = {}) =>
    request<FleetCapacityResponse>(`/fleet/capacity${buildQuery(params)}`),
  [])
  const getUsageAccounting = useCallback((projectId?: string) => request<UsageAccountingResponse>(`/system/usage${buildQuery({ project_id: projectId })}`), [])
  const listUsageAccountingSnapshots = useCallback((projectId?: string) => request<UsageAccountingSnapshotsResponse>(`/system/usage/snapshots${buildQuery({ project_id: projectId, limit: 5 })}`), [])
  const captureUsageAccountingSnapshot = useCallback((projectId?: string) => request<UsageAccountingSnapshot>(`/system/usage/snapshots${buildQuery({ project_id: projectId })}`, { method: 'POST' }), [])
  const downloadUsageAccounting = useCallback((projectId?: string) => fetchBlob(`/system/usage/export${buildQuery({ project_id: projectId })}`), [])
  const planArchiveRetention = useCallback(() => request<ArchiveRetentionPlanResponse>('/archives/retention/plan', { method: 'POST' }), [])
  const applyArchiveRetention = useCallback(() => request<ArchiveRetentionPlanResponse>('/archives/retention/apply', { method: 'POST' }), [])

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
      getSystemReadiness,
      getParityStatus,
      getBrowserBenchmarks,
      getAPMIntegrationProfiles,
      getInfrastructureMonitorPacks,
      getReportProfiles,
      getReleaseSLAGovernance,
      createReleaseSLAWaiver,
      listExternalResultImports,
      importExternalResults,
      getFleetAlerts,
      getFleetCapacity,
      getUsageAccounting,
      listUsageAccountingSnapshots,
      captureUsageAccountingSnapshot,
      downloadUsageAccounting,
      planArchiveRetention,
      applyArchiveRetention,
      getSystemSettings,
      updateSystemSettings,
    }),
    [getAuthEffectiveAccess, getAuthAccessMatrix, getAuthDiagnostics, getAuthRoleCatalog, listAuthServiceAccounts, createAuthServiceAccount, updateAuthServiceAccount, rotateAuthServiceAccount, deleteAuthServiceAccount, listAuthAuditEvents, downloadAuthAuditEvents, getHealth, getSystemInventory, getSystemReadiness, getParityStatus, getBrowserBenchmarks, getAPMIntegrationProfiles, getInfrastructureMonitorPacks, getReportProfiles, getReleaseSLAGovernance, createReleaseSLAWaiver, listExternalResultImports, importExternalResults, getFleetAlerts, getFleetCapacity, getUsageAccounting, listUsageAccountingSnapshots, captureUsageAccountingSnapshot, downloadUsageAccounting, planArchiveRetention, applyArchiveRetention, getSystemSettings, updateSystemSettings],
  )
}
