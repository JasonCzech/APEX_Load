import { useCallback, useMemo } from 'react'

import type {
  ObjectRepository,
  ObjectRepositoryListResponse,
} from '../../types/objectRepository'
import { request } from '../apiClient'

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

export function useObjectRepositoryApi() {
  const listObjectRepositories = useCallback(
    (params?: { project_id?: string; limit?: number; offset?: number }) =>
      request<ObjectRepositoryListResponse>(`/object-repositories${buildQuery({ ...params })}`),
    [],
  )

  const getObjectRepository = useCallback(
    (id: string) => request<ObjectRepository>(`/object-repositories/${id}`),
    [],
  )

  const createObjectRepository = useCallback(
    (body: Partial<ObjectRepository>) =>
      request<ObjectRepository>('/object-repositories', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateObjectRepository = useCallback(
    (id: string, body: Partial<ObjectRepository>) =>
      request<ObjectRepository>(`/object-repositories/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteObjectRepository = useCallback(
    (id: string) => request<{ deleted: string }>(`/object-repositories/${id}`, { method: 'DELETE' }),
    [],
  )

  const createObjectRepositoryFromScript = useCallback(
    (body: { project_id: string; script_id: string; name?: string; bind_script?: boolean }) =>
      request<ObjectRepository>('/object-repositories/from-script', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  return useMemo(
    () => ({
      listObjectRepositories,
      getObjectRepository,
      createObjectRepository,
      updateObjectRepository,
      deleteObjectRepository,
      createObjectRepositoryFromScript,
    }),
    [listObjectRepositories, getObjectRepository, createObjectRepository, updateObjectRepository, deleteObjectRepository, createObjectRepositoryFromScript],
  )
}
