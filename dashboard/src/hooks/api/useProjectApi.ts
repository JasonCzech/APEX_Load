import { useCallback, useMemo } from 'react'

import { Project, Folder, ProjectSettings } from '../../types'
import {
  type ProjectSecretStatusesResponse,
  type ProjectSecretsResponse,
  type ProjectSecretsWriteResponse,
  type ProjectSecretWrite,
} from '../../types/secrets'
import { request } from '../apiClient'

const PROJECT_SECRET_MUTATION_TIMEOUT_MS = 15000

export type DeleteEntityResponse = {
  deleted: string
  external_secret_cleanup_required?: boolean
  external_secret_cleanup_names?: string[]
  external_secret_cleanup_providers?: string[]
  external_secret_cleanup_prefix?: string
  secret_provider?: string
}

type ProjectRequest = {
  name: string
  domain_id?: string
  category_id?: string
  settings?: ProjectSettings
}

function query(params: Record<string, string | undefined>) {
  const q = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') q.set(key, value)
  })
  const text = q.toString()
  return text ? `?${text}` : ''
}

export function useProjectApi() {
  const listProjects = useCallback((filters?: { domain_id?: string; category_id?: string }) => request<{ projects: Project[]; count: number }>(`/projects${query(filters || {})}`), [])

  const createProject = useCallback(
    (data: ProjectRequest) => request<Project>('/projects', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateProject = useCallback(
    (id: string, data: ProjectRequest) => request<Project>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteProject = useCallback((id: string) => request<DeleteEntityResponse>(`/projects/${id}`, { method: 'DELETE' }), [])

  const listProjectSecrets = useCallback(
    (projectId: string) => request<ProjectSecretsResponse>(`/projects/${encodeURIComponent(projectId)}/secrets`),
    [],
  )

  const getProjectSecretStatuses = useCallback(
    (projectId: string, names: string[]) =>
      request<ProjectSecretStatusesResponse>(`/projects/${encodeURIComponent(projectId)}/secrets/status`, {
        method: 'POST',
        body: JSON.stringify({ names }),
      }),
    [],
  )

  const createProjectSecrets = useCallback(
    (projectId: string, secrets: ProjectSecretWrite[]) =>
      request<ProjectSecretsWriteResponse>(`/projects/${encodeURIComponent(projectId)}/secrets`, {
        method: 'POST',
        body: JSON.stringify({ secrets }),
        timeoutMs: PROJECT_SECRET_MUTATION_TIMEOUT_MS,
      }),
    [],
  )

  const rotateProjectSecret = useCallback(
    (projectId: string, name: string, value: string) =>
      request<ProjectSecretsWriteResponse>(`/projects/${encodeURIComponent(projectId)}/secrets/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ value }),
        timeoutMs: PROJECT_SECRET_MUTATION_TIMEOUT_MS,
      }),
    [],
  )

  const listFolders = useCallback(
    (projectId: string, category?: string) =>
      request<{ folders: Folder[]; count: number }>(`/projects/${projectId}/folders${query({ category })}`),
    [],
  )

  const createFolder = useCallback(
    (projectId: string, data: { name: string; category: string; parent_id?: string | null }) =>
      request<Folder>(`/projects/${projectId}/folders`, { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateFolder = useCallback(
    (id: string, data: { name?: string; parent_id?: string | null }) =>
      request<Folder>(`/folders/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteFolder = useCallback((id: string) => request<DeleteEntityResponse>(`/folders/${id}`, { method: 'DELETE' }), [])

  return useMemo(
    () => ({
      listProjects,
      createProject,
      updateProject,
      deleteProject,
      listProjectSecrets,
      getProjectSecretStatuses,
      createProjectSecrets,
      rotateProjectSecret,
      listFolders,
      createFolder,
      updateFolder,
      deleteFolder,
    }),
    [listProjects, createProject, updateProject, deleteProject, listProjectSecrets, getProjectSecretStatuses, createProjectSecrets, rotateProjectSecret, listFolders, createFolder, updateFolder, deleteFolder],
  )
}
