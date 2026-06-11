import { useCallback, useMemo } from 'react'

import { Application, LoadComparisonRequestOptions, LoadComparisonResponse, LoadComparisonScope, Release, ReleaseTrend } from '../../types'
import { apiFetch, request } from '../apiClient'

type DeleteEntityResponse = {
  deleted: string
}

async function fetchBlob(path: string): Promise<Blob> {
  const res = await apiFetch(path)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useApplicationApi() {
  const listApplications = useCallback((projectId?: string) => {
    const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    return request<{ applications: Application[]; count: number }>(`/applications${query}`)
  }, [])

  const createApplication = useCallback(
    (data: { name: string; project_id?: string }) => request<Application>('/applications', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateApplication = useCallback(
    (id: string, data: { name: string }) => request<Application>(`/applications/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteApplication = useCallback((id: string) => request<DeleteEntityResponse>(`/applications/${id}`, { method: 'DELETE' }), [])

  const listReleases = useCallback(
    (applicationId: string, includeArchived = false) =>
      request<{ releases: Release[]; count: number }>(`/applications/${applicationId}/releases${includeArchived ? '?include_archived=true' : ''}`),
    [],
  )

  const createRelease = useCallback(
    (applicationId: string, data: { name: string }) =>
      request<Release>(`/applications/${applicationId}/releases`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateRelease = useCallback(
    (id: string, data: { name: string }) => request<Release>(`/releases/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const archiveRelease = useCallback((id: string) => request<Release>(`/releases/${id}/archive`, { method: 'POST' }), [])

  const downloadReleaseTrend = useCallback((id: string) => fetchBlob(`/releases/${id}/trend/export`), [])

  const getReleaseTrend = useCallback((id: string) => request<ReleaseTrend>(`/releases/${id}/trend`), [])

  const getLoadComparison = useCallback((applicationId: string, scope: LoadComparisonScope = 'transaction', key = '', options?: LoadComparisonRequestOptions) => {
    const params = new URLSearchParams({ scope })
    if (key.trim()) {
      params.set('key', key.trim())
    }
    options?.testIds?.forEach(testId => {
      const trimmed = testId.trim()
      if (trimmed) params.append('test_id', trimmed)
    })
    return request<LoadComparisonResponse>(`/applications/${applicationId}/load-comparison?${params.toString()}`)
  }, [])

  const deleteRelease = useCallback((id: string) => request<DeleteEntityResponse>(`/releases/${id}`, { method: 'DELETE' }), [])

  return useMemo(
    () => ({
      listApplications,
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
    [listApplications, createApplication, updateApplication, deleteApplication, listReleases, createRelease, updateRelease, archiveRelease, downloadReleaseTrend, getReleaseTrend, getLoadComparison, deleteRelease],
  )
}
