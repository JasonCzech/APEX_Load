import { useCallback, useMemo } from 'react'

import { ApplicationRepository, Domain, RepositoryCategory, Release } from '../../types'
import { request } from '../apiClient'
import type { DomainFeaturePolicy } from '../../types/domainFeatures'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

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
  const listDomains = useCallback((options?: ApiCallOptions) => request<{ domains: Domain[]; count: number }>('/domains', withCallOptions(undefined, options)), [])

  const createDomain = useCallback(
    (data: { name: string; description?: string }, options?: ApiCallOptions) =>
      request<{ domain: Domain; application_repository: ApplicationRepository; default_category: RepositoryCategory }>('/domains', withCallOptions({
        method: 'POST',
        body: JSON.stringify(data),
      }, options)),
    [],
  )

  const updateDomain = useCallback(
    (id: string, data: { name: string; description?: string }, options?: ApiCallOptions) =>
      request<Domain>(`/domains/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteDomain = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/domains/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  const getDomainFeatures = useCallback(
    (id: string, options?: ApiCallOptions) => request<DomainFeaturePolicy>(`/domains/${id}/features`, withCallOptions(undefined, options)),
    [],
  )

  const updateDomainFeatures = useCallback(
    (id: string, disabledFeatureIds: string[], revision: string, options?: ApiCallOptions) => request<DomainFeaturePolicy>(`/domains/${id}/features`, withCallOptions({
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'If-Match': `"${revision}"` },
      body: JSON.stringify({ disabled_feature_ids: disabledFeatureIds }),
    }, options)),
    [],
  )

  const getApplicationRepository = useCallback(
    (domainId: string, options?: ApiCallOptions) => request<ApplicationRepository>(`/domains/${domainId}/application-repository`, withCallOptions(undefined, options)),
    [],
  )

  const updateApplicationRepository = useCallback(
    (domainId: string, data: { name: string; description?: string }, options?: ApiCallOptions) =>
      request<ApplicationRepository>(`/domains/${domainId}/application-repository`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const listRepositoryCategories = useCallback(
    (domainId: string, options?: ApiCallOptions) => request<{ categories: RepositoryCategory[]; count: number }>(`/domains/${domainId}/application-repository/categories`, withCallOptions(undefined, options)),
    [],
  )

  const createRepositoryCategory = useCallback(
    (domainId: string, data: { name: string; description?: string; sort_order?: number }, options?: ApiCallOptions) =>
      request<RepositoryCategory>(`/domains/${domainId}/application-repository/categories`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateRepositoryCategory = useCallback(
    (id: string, data: { name: string; description?: string; sort_order?: number }, options?: ApiCallOptions) =>
      request<RepositoryCategory>(`/application-repository/categories/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteRepositoryCategory = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/application-repository/categories/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  const listProjectReleases = useCallback(
    (projectId: string, includeArchived = false, options?: ApiCallOptions) =>
      request<{ releases: Release[]; count: number }>(`/projects/${projectId}/releases${query({ include_archived: includeArchived ? 'true' : undefined })}`, withCallOptions(undefined, options)),
    [],
  )

  const createProjectRelease = useCallback(
    (projectId: string, data: { name: string; application_id?: string }, options?: ApiCallOptions) =>
      request<Release>(`/projects/${projectId}/releases`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const archiveRelease = useCallback((id: string, options?: ApiCallOptions) => request<Release>(`/releases/${id}/archive`, withCallOptions({ method: 'POST' }, options)), [])

  const deleteRelease = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/releases/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  return useMemo(
    () => ({
      listDomains,
      createDomain,
      updateDomain,
      deleteDomain,
      getDomainFeatures,
      updateDomainFeatures,
      getApplicationRepository,
      updateApplicationRepository,
      listRepositoryCategories,
      createRepositoryCategory,
      updateRepositoryCategory,
      deleteRepositoryCategory,
      listProjectReleases,
      createProjectRelease,
      archiveRelease,
      deleteRelease,
    }),
    [
      listDomains,
      createDomain,
      updateDomain,
      deleteDomain,
      getDomainFeatures,
      updateDomainFeatures,
      getApplicationRepository,
      updateApplicationRepository,
      listRepositoryCategories,
      createRepositoryCategory,
      updateRepositoryCategory,
      deleteRepositoryCategory,
      listProjectReleases,
      createProjectRelease,
      archiveRelease,
      deleteRelease,
    ],
  )
}
