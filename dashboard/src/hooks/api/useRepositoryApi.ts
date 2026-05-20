import { useCallback, useMemo } from 'react'

import { ApplicationRepository, Domain, RepositoryCategory, Release } from '../../types'
import { request } from '../apiClient'

type DeleteEntityResponse = {
  deleted: string
}

function query(params: Record<string, string | undefined>) {
  const q = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') q.set(key, value)
  })
  const text = q.toString()
  return text ? `?${text}` : ''
}

export function useRepositoryApi() {
  const listDomains = useCallback(() => request<{ domains: Domain[]; count: number }>('/domains'), [])

  const createDomain = useCallback(
    (data: { name: string; description?: string }) =>
      request<{ domain: Domain; application_repository: ApplicationRepository; default_category: RepositoryCategory }>('/domains', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    [],
  )

  const updateDomain = useCallback(
    (id: string, data: { name: string; description?: string }) =>
      request<Domain>(`/domains/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteDomain = useCallback((id: string) => request<DeleteEntityResponse>(`/domains/${id}`, { method: 'DELETE' }), [])

  const getApplicationRepository = useCallback(
    (domainId: string) => request<ApplicationRepository>(`/domains/${domainId}/application-repository`),
    [],
  )

  const updateApplicationRepository = useCallback(
    (domainId: string, data: { name: string; description?: string }) =>
      request<ApplicationRepository>(`/domains/${domainId}/application-repository`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const listRepositoryCategories = useCallback(
    (domainId: string) => request<{ categories: RepositoryCategory[]; count: number }>(`/domains/${domainId}/application-repository/categories`),
    [],
  )

  const createRepositoryCategory = useCallback(
    (domainId: string, data: { name: string; description?: string; sort_order?: number }) =>
      request<RepositoryCategory>(`/domains/${domainId}/application-repository/categories`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateRepositoryCategory = useCallback(
    (id: string, data: { name: string; description?: string; sort_order?: number }) =>
      request<RepositoryCategory>(`/application-repository/categories/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteRepositoryCategory = useCallback((id: string) => request<DeleteEntityResponse>(`/application-repository/categories/${id}`, { method: 'DELETE' }), [])

  const listProjectReleases = useCallback(
    (projectId: string, includeArchived = false) =>
      request<{ releases: Release[]; count: number }>(`/projects/${projectId}/releases${query({ include_archived: includeArchived ? 'true' : undefined })}`),
    [],
  )

  const createProjectRelease = useCallback(
    (projectId: string, data: { name: string; application_id?: string }) =>
      request<Release>(`/projects/${projectId}/releases`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  return useMemo(
    () => ({
      listDomains,
      createDomain,
      updateDomain,
      deleteDomain,
      getApplicationRepository,
      updateApplicationRepository,
      listRepositoryCategories,
      createRepositoryCategory,
      updateRepositoryCategory,
      deleteRepositoryCategory,
      listProjectReleases,
      createProjectRelease,
    }),
    [
      listDomains,
      createDomain,
      updateDomain,
      deleteDomain,
      getApplicationRepository,
      updateApplicationRepository,
      listRepositoryCategories,
      createRepositoryCategory,
      updateRepositoryCategory,
      deleteRepositoryCategory,
      listProjectReleases,
      createProjectRelease,
    ],
  )
}
