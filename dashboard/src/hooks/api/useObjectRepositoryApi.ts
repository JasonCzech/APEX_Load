import { useCallback, useMemo } from 'react'

import type {
  ObjectRepository,
  ObjectRepositoryListResponse,
} from '../../types/objectRepository'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

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
    (params?: { project_id?: string; limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<ObjectRepositoryListResponse>(`/object-repositories${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getObjectRepository = useCallback(
    (id: string, options?: ApiCallOptions) => request<ObjectRepository>(`/object-repositories/${id}`, withCallOptions(undefined, options)),
    [],
  )

  const createObjectRepository = useCallback(
    (body: Partial<ObjectRepository>, options?: ApiCallOptions) =>
      request<ObjectRepository>('/object-repositories', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const updateObjectRepository = useCallback(
    (id: string, body: Partial<ObjectRepository>, options?: ApiCallOptions) =>
      request<ObjectRepository>(`/object-repositories/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)),
    [],
  )

  const deleteObjectRepository = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/object-repositories/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const createObjectRepositoryFromScript = useCallback(
    (body: { project_id: string; script_id: string; name?: string; bind_script?: boolean }, options?: ApiCallOptions) =>
      request<ObjectRepository>('/object-repositories/from-script', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
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
