import { useCallback, useMemo } from 'react'

import { Application, LoadComparisonRequestOptions, LoadComparisonResponse, LoadComparisonScope, Release, ReleaseTrend } from '../../types'
import type { ProjectSecretStatusesResponse, ProjectSecretUsage, ProjectSecretsResponse, ProjectSecretsWriteResponse, ProjectSecretWrite } from '../../types/secrets'
import { apiFetch, request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

type DeleteEntityResponse = {
  deleted: string
  external_secret_cleanup_required?: boolean
  external_secret_cleanup_names?: string[]
  external_secret_cleanup_providers?: string[]
  external_secret_cleanup_prefix?: string
}

async function fetchBlob(path: string, options?: ApiCallOptions): Promise<Blob> {
  const res = await apiFetch(path, withCallOptions(undefined, options))
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useApplicationApi() {
  const listApplicationSecrets = useCallback(
    (id: string, options?: ApiCallOptions) => request<ProjectSecretsResponse>(`/applications/${encodeURIComponent(id)}/secrets`, withCallOptions(undefined, options)), [])
  const getApplicationSecretStatuses = useCallback(
    (id: string, names: string[], options?: ApiCallOptions) => request<ProjectSecretStatusesResponse>(`/applications/${encodeURIComponent(id)}/secrets/status`, withCallOptions({
      method: 'POST', body: JSON.stringify({ names }),
    }, options)), [])
  const getApplicationSecretUsage = useCallback(
    (id: string, name: string, pagination?: { limit?: number; offset?: number }, options?: ApiCallOptions) => {
      const params = new URLSearchParams()
      if (pagination?.limit) params.set('limit', String(pagination.limit))
      if (pagination?.offset) params.set('offset', String(pagination.offset))
      return request<ProjectSecretUsage>(`/applications/${encodeURIComponent(id)}/secrets/${encodeURIComponent(name)}/usage?${params}`, withCallOptions(undefined, options))
    }, [])
  const createApplicationSecrets = useCallback(
    (id: string, secrets: ProjectSecretWrite[], options?: ApiCallOptions) => request<ProjectSecretsWriteResponse>(`/applications/${encodeURIComponent(id)}/secrets`, withCallOptions({
      method: 'POST', body: JSON.stringify({ secrets }), timeoutMs: 15000,
    }, options)), [])
  const rotateApplicationSecret = useCallback(
    (id: string, name: string, value: string, usage?: { expected_usage_revision?: string; acknowledge_shared_usage?: boolean }, options?: ApiCallOptions) =>
      request<ProjectSecretsWriteResponse>(`/applications/${encodeURIComponent(id)}/secrets/${encodeURIComponent(name)}`, withCallOptions({
        method: 'PUT', body: JSON.stringify({ value, ...usage }), timeoutMs: 15000,
      }, options)), [])
  const listApplications = useCallback((projectId?: string, options?: ApiCallOptions) => {
    const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    return request<{ applications: Application[]; count: number }>(`/applications${query}`, withCallOptions(undefined, options))
  }, [])

  const createApplication = useCallback(
    (data: { name: string; project_id?: string }, options?: ApiCallOptions) => request<Application>('/applications', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateApplication = useCallback(
    (id: string, data: { name: string; updated_at: string }, options?: ApiCallOptions) => request<Application>(`/applications/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteApplication = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/applications/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  const listReleases = useCallback(
    (applicationId: string, includeArchived = false, options?: ApiCallOptions) =>
      request<{ releases: Release[]; count: number }>(`/applications/${applicationId}/releases${includeArchived ? '?include_archived=true' : ''}`, withCallOptions(undefined, options)),
    [],
  )

  const createRelease = useCallback(
    (applicationId: string, data: { name: string }, options?: ApiCallOptions) =>
      request<Release>(`/applications/${applicationId}/releases`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateRelease = useCallback(
    (id: string, data: { name: string; updated_at: string }, options?: ApiCallOptions) => request<Release>(`/releases/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const archiveRelease = useCallback((id: string, options?: ApiCallOptions) => request<Release>(`/releases/${id}/archive`, withCallOptions({ method: 'POST' }, options)), [])

  const downloadReleaseTrend = useCallback((id: string, options?: ApiCallOptions) => fetchBlob(`/releases/${id}/trend/export`, options), [])

  const getReleaseTrend = useCallback((id: string, options?: ApiCallOptions) => request<ReleaseTrend>(`/releases/${id}/trend`, withCallOptions(undefined, options)), [])

  const getLoadComparison = useCallback((applicationId: string, scope: LoadComparisonScope = 'transaction', key = '', options?: LoadComparisonRequestOptions, callOptions?: ApiCallOptions) => {
    const params = new URLSearchParams({ scope })
    if (key.trim()) {
      params.set('key', key.trim())
    }
    if (options?.population) params.set('population', options.population)
    if (options?.startSeconds !== undefined) params.set('start_seconds', String(options.startSeconds))
    if (options?.endSeconds !== undefined) params.set('end_seconds', String(options.endSeconds))
    options?.testIds?.forEach(testId => {
      const trimmed = testId.trim()
      if (trimmed) params.append('test_id', trimmed)
    })
    return request<LoadComparisonResponse>(`/applications/${applicationId}/load-comparison?${params.toString()}`, withCallOptions(undefined, callOptions))
  }, [])

  const deleteRelease = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/releases/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  return useMemo(
    () => ({
      listApplications,
      listApplicationSecrets,
      getApplicationSecretStatuses,
      getApplicationSecretUsage,
      createApplicationSecrets,
      rotateApplicationSecret,
      createApplication,
      updateApplication,
      deleteApplication,
      listReleases,
      createRelease,
      updateRelease,
      archiveRelease,
      downloadReleaseTrend,
      getReleaseTrend,
      getLoadComparison,
      deleteRelease,
    }),
    [listApplications, listApplicationSecrets, getApplicationSecretStatuses, getApplicationSecretUsage, createApplicationSecrets, rotateApplicationSecret, createApplication, updateApplication, deleteApplication, listReleases, createRelease, updateRelease, archiveRelease, downloadReleaseTrend, getReleaseTrend, getLoadComparison, deleteRelease],
  )
}
