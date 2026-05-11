import { useCallback, useMemo } from 'react'

import { Application, Release } from '../../types'
import { request } from '../apiClient'

export function useApplicationApi() {
  const listApplications = useCallback(() => request<{ applications: Application[]; count: number }>('/applications'), [])

  const createApplication = useCallback(
    (data: { name: string }) => request<Application>('/applications', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateApplication = useCallback(
    (id: string, data: { name: string }) => request<Application>(`/applications/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteApplication = useCallback((id: string) => request<any>(`/applications/${id}`, { method: 'DELETE' }), [])

  const listReleases = useCallback(
    (applicationId: string) => request<{ releases: Release[]; count: number }>(`/applications/${applicationId}/releases`),
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

  const deleteRelease = useCallback((id: string) => request<any>(`/releases/${id}`, { method: 'DELETE' }), [])

  return useMemo(
    () => ({
      listApplications,
      createApplication,
      updateApplication,
      deleteApplication,
      listReleases,
      createRelease,
      updateRelease,
      deleteRelease,
    }),
    [listApplications, createApplication, updateApplication, deleteApplication, listReleases, createRelease, updateRelease, deleteRelease],
  )
}
