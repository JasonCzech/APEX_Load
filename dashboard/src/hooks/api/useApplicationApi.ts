import { useCallback, useMemo } from 'react'

import { Application, Release } from '../../types'
import { request } from '../apiClient'

type DeleteEntityResponse = {
  deleted: string
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
      deleteRelease,
    }),
    [listApplications, createApplication, updateApplication, deleteApplication, listReleases, createRelease, updateRelease, archiveRelease, deleteRelease],
  )
}
