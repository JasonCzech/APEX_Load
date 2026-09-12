import { useCallback, useMemo } from 'react'

import { type BrowserEmulationCatalog, type ScriptConfig } from '../../types/scripts'

import {
  ArchiveRetentionPlanResponse,
  APMIntegrationProfilesResponse,
  ExternalResultImportsResponse,
  ImportedDashboard,
  ImportedDashboardGraphsResponse,
  ImportedDashboardImportReview,
  InfrastructureMonitorPacksResponse,
  PrometheusGraphsResponse,
  ReleaseSLAGovernanceResponse,
  ReportProfilesResponse,
} from '../../types'
import type {
  DashboardAccessReviewResponse,
  DashboardAuditEventFilter,
  DashboardAuditEventListResponse,
  DashboardAuthAccessMatrix,
  DashboardAuthDiagnostics,
  DashboardAuthEffectiveAccess,
  DashboardAuthPolicy,
  DashboardAuthRoleCatalog,
  DashboardCreateServiceAccountRequest,
  DashboardCreateServiceAccountResponse,
  DashboardCreateUserBindingRequest,
  DashboardCreateUserRequest,
  DashboardOnboardUserRequest,
  DashboardServiceAccountListResponse,
  DashboardUpdateServiceAccountRequest,
  DashboardUpdateUserRequest,
  DashboardUser,
  DashboardUserActivityResponse,
  DashboardUserListResponse,
} from '../../types/auth'
import type {
  FleetAlert,
  FleetAlertsResponse,
  FleetCapacityResponse,
  ProvisionerInfo,
} from '../../types/fleet'
import type {
  BrowserBenchmarkEvidenceResponse,
  HealthResponse,
  ParityStatusResponse,
  SystemInventoryResponse,
  SystemReadinessResponse,
  SystemRestartCapabilities,
  SystemRestartRequest,
  SystemRestartResult,
  SystemServicesResponse,
  UsageAccountingResponse,
  UsageAccountingSnapshot,
  UsageAccountingSnapshotsResponse,
  VersionResponse,
} from '../../types/system'
import type { DashboardSystemSettings, PowerBIDatasetRecreateResponse, PowerBITestConnectionResponse } from '../../types/settings'
import { apiFetch, ApiError, request } from '../apiClient'

// The controller completes its readiness probe in 2.5 seconds and Monitoring
// polls every five seconds. Keep the browser request inside that polling window
// so a stalled proxy or connection cannot pin the shared dashboard refresh.
export const SYSTEM_READINESS_REQUEST_TIMEOUT_MS = 4_000

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isSystemReadinessResponse(value: unknown): value is SystemReadinessResponse {
  if (!isRecord(value) || value.status !== 'not_ready' || typeof value.generated_at !== 'string' || !Array.isArray(value.checks)) {
    return false
  }
  return value.checks.every(check => (
    isRecord(check)
    && typeof check.name === 'string'
    && typeof check.status === 'string'
    && typeof check.message === 'string'
    && (check.details === undefined || isRecord(check.details))
  ))
}

async function fetchSystemReadiness(): Promise<SystemReadinessResponse> {
  try {
    return await request<SystemReadinessResponse>('/system/readiness', {
      timeoutMs: SYSTEM_READINESS_REQUEST_TIMEOUT_MS,
    })
  } catch (error) {
    // Readiness deliberately uses HTTP 503 for a valid not_ready document so
    // Kubernetes can consume the same route. Treat only that validated payload
    // as data; malformed or unrelated 503 responses remain transport failures.
    if (error instanceof ApiError && error.status === 503 && isSystemReadinessResponse(error.payload)) {
      return error.payload
    }
    throw error
  }
}

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
  const getAuthEffectiveAccessForPrincipal = useCallback(
    (params: { principal_type: 'user' | 'service_account'; principal_id: string; project_id?: string; domain_id?: string; application_repository_id?: string }) =>
      request<DashboardAuthEffectiveAccess>(`/auth/effective-access/principal${buildQuery(params)}`),
    [],
  )
  const getAuthAccessMatrix = useCallback(() => request<DashboardAuthAccessMatrix>('/auth/access-matrix'), [])
  // getAuthPolicy folds /auth/roles + /auth/access-matrix into one fetch (the split hooks
  // remain for any caller that needs just one half).
  const getAuthPolicy = useCallback(() => request<DashboardAuthPolicy>('/auth/policy'), [])
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
  const getAuthUser = useCallback((id: string) => request<DashboardUser>(`/auth/users/${encodeURIComponent(id)}`), [])
  const listAuthUsers = useCallback((filter: { q?: string; active?: string; role?: string; domain_id?: string; project_id?: string; limit?: number; offset?: number } = {}) => request<DashboardUserListResponse>(`/auth/users${buildQuery({ limit: 100, ...filter })}`), [])
  const createAuthUser = useCallback(
    (payload: DashboardCreateUserRequest) => request<DashboardUser>('/auth/users', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const onboardAuthUser = useCallback(
    (payload: DashboardOnboardUserRequest) => request<DashboardUser>('/auth/users/onboard', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const updateAuthUser = useCallback(
    (id: string, payload: DashboardUpdateUserRequest) => request<DashboardUser>(`/auth/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
    [],
  )
  const enableAuthUser = useCallback((id: string) => request<DashboardUser>(`/auth/users/${encodeURIComponent(id)}/enable`, { method: 'POST' }), [])
  const disableAuthUser = useCallback((id: string) => request<DashboardUser>(`/auth/users/${encodeURIComponent(id)}/disable`, { method: 'POST' }), [])
  const revokeAuthUserSessions = useCallback((id: string) => request<DashboardUser>(`/auth/users/${encodeURIComponent(id)}/revoke-sessions`, { method: 'POST' }), [])
  const deleteAuthUser = useCallback((id: string) => request<{ deleted: string }>(`/auth/users/${encodeURIComponent(id)}`, { method: 'DELETE' }), [])
  const createAuthUserBinding = useCallback(
    (id: string, payload: DashboardCreateUserBindingRequest) => request(`/auth/users/${encodeURIComponent(id)}/bindings`, { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const deleteAuthUserBinding = useCallback((id: string, bindingId: string) => request<{ deleted: string }>(`/auth/users/${encodeURIComponent(id)}/bindings/${encodeURIComponent(bindingId)}`, { method: 'DELETE' }), [])
  const getAuthUserActivity = useCallback((filter: DashboardAuditEventFilter = {}) => {
    return request<DashboardUserActivityResponse>(`/auth/user-activity${buildQuery({ limit: 50, ...filter })}`)
  }, [])
  const downloadAuthUserActivity = useCallback((filter: DashboardAuditEventFilter = {}) => fetchBlob(`/auth/user-activity/export${buildQuery({ limit: 1000, ...filter })}`), [])
  const getAccessReview = useCallback(() => request<DashboardAccessReviewResponse>('/auth/access-review'), [])
  const listAuthAuditEvents = useCallback((filter: DashboardAuditEventFilter = {}) => {
    return request<DashboardAuditEventListResponse>(`/auth/audit-events${buildQuery({ limit: 20, ...filter })}`)
  }, [])
  const downloadAuthAuditEvents = useCallback((filter: DashboardAuditEventFilter = {}) => fetchBlob(`/auth/audit-events/export${buildQuery({ limit: 1000, ...filter })}`), [])
  const getHealth = useCallback(() => request<HealthResponse>('/system/health'), [])
  const getVersion = useCallback(() => request<VersionResponse>('/system/version'), [])
  // Engine config defaults — the values the runtime applies when a script leaves a
  // field unset; the runtime editor uses these as the lowest "inherited" layer.
  const getConfigDefaults = useCallback(() => request<{ config: ScriptConfig }>('/dsl/config-defaults'), [])
  // Browser-emulation catalog: the curated browser identities the runtime
  // editor's Browser Emulation tab offers, kept in lockstep with replay-time
  // User-Agent resolution by serving it from the controller.
  const getBrowserEmulationCatalog = useCallback(() => request<BrowserEmulationCatalog>('/dsl/browser-emulation-catalog'), [])
  const getSystemInventory = useCallback(() => request<SystemInventoryResponse>('/system/inventory'), [])
  const getRestartCapabilities = useCallback(() => request<SystemRestartCapabilities>('/system/restarts/capabilities'), [])
  const executeRestart = useCallback(
    (payload: SystemRestartRequest) => request<SystemRestartResult>('/system/restarts', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const getSystemReadiness = useCallback(() => fetchSystemReadiness(), [])
  const getSystemServices = useCallback(() => request<SystemServicesResponse>('/system/services'), [])
  const getParityStatus = useCallback(() => request<ParityStatusResponse>('/system/parity-status'), [])
  const getBrowserBenchmarks = useCallback(() => request<BrowserBenchmarkEvidenceResponse>('/system/browser-benchmarks'), [])
  const getAPMIntegrationProfiles = useCallback(() => request<APMIntegrationProfilesResponse>('/integrations/apm/profiles'), [])
  const getInfrastructureMonitorPacks = useCallback(() => request<InfrastructureMonitorPacksResponse>('/monitoring/packs'), [])
  const getPrometheusGraphs = useCallback(
    (params: { start: string; end: string; step_seconds: number }) =>
      request<PrometheusGraphsResponse>(`/monitoring/prometheus/graphs${buildQuery(params)}`),
    [],
  )
  const getImportedDashboardGraphs = useCallback(
		(params: { project_id: string; test_id?: string; start: string; end: string; step_seconds: number }) =>
      request<ImportedDashboardGraphsResponse>(`/monitoring/dashboards/graphs${buildQuery(params)}`),
    [],
  )
  const listImportedDashboards = useCallback(
		(params: { project_id?: string; limit?: number; offset?: number } = {}) =>
			request<{ dashboards: ImportedDashboard[]; count: number; limit?: number; offset?: number }>(`/monitoring/dashboards${buildQuery(params)}`),
    [],
  )
  const importGrafanaDashboard = useCallback(
    (payload: { project_id: string; name?: string; content: string }) =>
      request<ImportedDashboardImportReview>('/monitoring/dashboards/import/grafana', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const importGrafanaDashboardByUID = useCallback(
    (payload: { project_id: string; uid: string; name?: string }) =>
      request<ImportedDashboardImportReview>('/monitoring/dashboards/import/grafana/live', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const commitGrafanaDashboardImport = useCallback(
    (payload: { project_id: string; import_session_id: string; name?: string; bindings?: Record<string, string> }) =>
      request<ImportedDashboard>('/monitoring/dashboards/import/grafana/commit', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const previewGrafanaDashboardImport = useCallback(
    (payload: { project_id: string; import_session_id: string; bindings?: Record<string, string>; test_id?: string; start?: string; end?: string; step_seconds?: number }) =>
      request<ImportedDashboardGraphsResponse>('/monitoring/dashboards/import/grafana/preview', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const resolveGrafanaImportLabelValues = useCallback(
    (payload: { project_id: string; import_session_id: string; variable: string; bindings?: Record<string, string>; test_id?: string }) =>
      request<{ variable: string; options: string[] }>('/monitoring/dashboards/import/grafana/label-values', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const getImportedDashboardPanels = useCallback(
    (id: string, params: { test_id?: string; start: string; end: string; step_seconds: number }) =>
      request<ImportedDashboardGraphsResponse>(`/monitoring/dashboards/${encodeURIComponent(id)}/panels${buildQuery(params)}`),
    [],
  )
  const patchImportedDashboard = useCallback(
    (id: string, payload: { name?: string; description?: string; enabled?: boolean; bindings?: Record<string, string> }) =>
      request<ImportedDashboard>(`/monitoring/dashboards/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
    [],
  )
  const deleteImportedDashboard = useCallback(
    (id: string) => request<{ deleted: string }>(`/monitoring/dashboards/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    [],
  )
  const getReportProfiles = useCallback(() => request<ReportProfilesResponse>('/reports/profiles'), [])
  const getReleaseSLAGovernance = useCallback((releaseId: string) => request<ReleaseSLAGovernanceResponse>(`/releases/${encodeURIComponent(releaseId)}/sla-governance`), [])
  const createReleaseSLAWaiver = useCallback(
    (releaseId: string, payload: { scope?: string; reason: string; approved_by?: string; expires_at?: string }) =>
      request(`/releases/${encodeURIComponent(releaseId)}/sla-governance/waivers`, { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const listExternalResultImports = useCallback((params: { project_id?: string; application_id?: string; release_id?: string; provider?: string; limit?: number; offset?: number } = {}) =>
    request<ExternalResultImportsResponse>(`/results/imports${buildQuery(params)}`),
  [])
  const importExternalResults = useCallback(
    (payload: unknown) => request('/results/import', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )
  const getFleetAlerts = useCallback(() => request<FleetAlertsResponse>('/fleet/alerts'), [])
  const ackFleetAlert = useCallback(
    (id: string, payload: { acknowledged?: boolean; note?: string } = {}) =>
      request<FleetAlert>(`/fleet/alerts/${encodeURIComponent(id)}/ack`, {
        method: 'PATCH',
        body: JSON.stringify(payload),
      }),
    [],
  )
  const getFleetCapacity = useCallback((params: { project_id?: string; region?: string; protocol?: string } = {}) =>
    request<FleetCapacityResponse>(`/fleet/capacity${buildQuery(params)}`),
  [])
  const getFleetProvisioner = useCallback(() => request<ProvisionerInfo>('/fleet/provisioner'), [])
  const getUsageAccounting = useCallback((projectId?: string) => request<UsageAccountingResponse>(`/system/usage${buildQuery({ project_id: projectId })}`), [])
  const listUsageAccountingSnapshots = useCallback((projectId?: string) => request<UsageAccountingSnapshotsResponse>(`/system/usage/snapshots${buildQuery({ project_id: projectId, limit: 5 })}`), [])
  const captureUsageAccountingSnapshot = useCallback((projectId?: string) => request<UsageAccountingSnapshot>(`/system/usage/snapshots${buildQuery({ project_id: projectId })}`, { method: 'POST' }), [])
  const downloadUsageAccounting = useCallback((projectId?: string) => fetchBlob(`/system/usage/export${buildQuery({ project_id: projectId })}`), [])
  const planArchiveRetention = useCallback(() => request<ArchiveRetentionPlanResponse>('/archives/retention/plan', { method: 'POST' }), [])
  const applyArchiveRetention = useCallback(() => request<ArchiveRetentionPlanResponse>('/archives/retention/apply', { method: 'POST' }), [])

  const getSystemSettings = useCallback(() => request<DashboardSystemSettings>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: DashboardSystemSettings) => {
      const { revision, ...payload } = settings
      return request<DashboardSystemSettings>('/system/settings', {
        method: 'PUT',
        headers: revision ? { 'If-Match': JSON.stringify(revision) } : undefined,
        body: JSON.stringify(payload),
      })
    },
    [],
  )

  const testPowerBIConnection = useCallback(
    () => request<PowerBITestConnectionResponse>('/integrations/powerbi/test-connection', { method: 'POST' }),
    [],
  )

  const recreatePowerBIDataset = useCallback(
    () => request<PowerBIDatasetRecreateResponse>('/integrations/powerbi/dataset/recreate', { method: 'POST' }),
    [],
  )

  return useMemo(
    () => ({
      getAuthEffectiveAccess,
      getAuthEffectiveAccessForPrincipal,
      getAuthAccessMatrix,
      getAuthPolicy,
      getAuthDiagnostics,
      getAuthRoleCatalog,
      listAuthServiceAccounts,
      createAuthServiceAccount,
      updateAuthServiceAccount,
      rotateAuthServiceAccount,
      deleteAuthServiceAccount,
      getAuthUser,
      listAuthUsers,
      createAuthUser,
      onboardAuthUser,
      updateAuthUser,
      enableAuthUser,
      disableAuthUser,
      revokeAuthUserSessions,
      deleteAuthUser,
      createAuthUserBinding,
      deleteAuthUserBinding,
      getAuthUserActivity,
      downloadAuthUserActivity,
      getAccessReview,
      listAuthAuditEvents,
      downloadAuthAuditEvents,
      getHealth,
      getVersion,
      getConfigDefaults,
      getBrowserEmulationCatalog,
      getSystemInventory,
      getRestartCapabilities,
      executeRestart,
      getSystemReadiness,
      getSystemServices,
      getParityStatus,
      getBrowserBenchmarks,
      getAPMIntegrationProfiles,
      getInfrastructureMonitorPacks,
      getPrometheusGraphs,
      getImportedDashboardGraphs,
      listImportedDashboards,
      importGrafanaDashboard,
      importGrafanaDashboardByUID,
      commitGrafanaDashboardImport,
      previewGrafanaDashboardImport,
      resolveGrafanaImportLabelValues,
      getImportedDashboardPanels,
      patchImportedDashboard,
      deleteImportedDashboard,
      getReportProfiles,
      getReleaseSLAGovernance,
      createReleaseSLAWaiver,
      listExternalResultImports,
      importExternalResults,
      getFleetAlerts,
      ackFleetAlert,
      getFleetCapacity,
      getFleetProvisioner,
      getUsageAccounting,
      listUsageAccountingSnapshots,
      captureUsageAccountingSnapshot,
      downloadUsageAccounting,
      planArchiveRetention,
      applyArchiveRetention,
      getSystemSettings,
      updateSystemSettings,
      testPowerBIConnection,
      recreatePowerBIDataset,
    }),
    [getAuthEffectiveAccess, getAuthEffectiveAccessForPrincipal, getAuthAccessMatrix, getAuthPolicy, getAuthDiagnostics, getAuthRoleCatalog, listAuthServiceAccounts, createAuthServiceAccount, updateAuthServiceAccount, rotateAuthServiceAccount, deleteAuthServiceAccount, getAuthUser, listAuthUsers, createAuthUser, onboardAuthUser, updateAuthUser, enableAuthUser, disableAuthUser, revokeAuthUserSessions, deleteAuthUser, createAuthUserBinding, deleteAuthUserBinding, getAuthUserActivity, downloadAuthUserActivity, getAccessReview, listAuthAuditEvents, downloadAuthAuditEvents, getHealth, getVersion, getConfigDefaults, getBrowserEmulationCatalog, getSystemInventory, getRestartCapabilities, executeRestart, getSystemReadiness, getSystemServices, getParityStatus, getBrowserBenchmarks, getAPMIntegrationProfiles, getInfrastructureMonitorPacks, getPrometheusGraphs, getImportedDashboardGraphs, listImportedDashboards, importGrafanaDashboard, importGrafanaDashboardByUID, commitGrafanaDashboardImport, previewGrafanaDashboardImport, resolveGrafanaImportLabelValues, getImportedDashboardPanels, patchImportedDashboard, deleteImportedDashboard, getReportProfiles, getReleaseSLAGovernance, createReleaseSLAWaiver, listExternalResultImports, importExternalResults, getFleetAlerts, ackFleetAlert, getFleetCapacity, getFleetProvisioner, getUsageAccounting, listUsageAccountingSnapshots, captureUsageAccountingSnapshot, downloadUsageAccounting, planArchiveRetention, applyArchiveRetention, getSystemSettings, updateSystemSettings, testPowerBIConnection, recreatePowerBIDataset],
  )
}
