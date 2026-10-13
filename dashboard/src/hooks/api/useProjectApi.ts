import { useCallback, useMemo } from 'react'

import { Project, Folder, ProjectSettings } from '../../types'
import {
  type ProjectSecretStatusesResponse,
  type ProjectSecretUsage,
  type ProjectSecretsResponse,
  type ProjectSecretsWriteResponse,
  type ProjectSecretWrite,
} from '../../types/secrets'
import type { ScenarioDraft, ScenarioDraftSave, ScenarioDraftSummary } from '../../types/scenarioDrafts'
import type { SharedAssetUsage, SharedAssetUsageKind } from '../../types/sharedAssets'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

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
  const listProjects = useCallback((filters?: { domain_id?: string; category_id?: string }, options?: ApiCallOptions) => request<{ projects: Project[]; count: number }>(`/projects${query(filters || {})}`, withCallOptions(undefined, options)), [])

  const createProject = useCallback(
    (data: ProjectRequest, options?: ApiCallOptions) => request<Project>('/projects', withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateProject = useCallback(
    (id: string, data: ProjectRequest, options?: ApiCallOptions) => request<Project>(`/projects/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteProject = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/projects/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  const listProjectSecrets = useCallback(
    (projectId: string, options?: ApiCallOptions) => request<ProjectSecretsResponse>(`/projects/${encodeURIComponent(projectId)}/secrets`, withCallOptions(undefined, options)),
    [],
  )

  const getProjectSecretStatuses = useCallback(
    (projectId: string, names: string[], options?: ApiCallOptions) =>
      request<ProjectSecretStatusesResponse>(`/projects/${encodeURIComponent(projectId)}/secrets/status`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ names }),
      }, options)),
    [],
  )

  const getProjectSecretUsage = useCallback(
    (projectId: string, name: string, options?: { limit?: number; offset?: number }, callOptions?: ApiCallOptions) => {
      const params = new URLSearchParams()
      if (options?.limit) params.set('limit', String(options.limit))
      if (options?.offset) params.set('offset', String(options.offset))
      const suffix = params.toString()
      return request<ProjectSecretUsage>(`/projects/${encodeURIComponent(projectId)}/secrets/${encodeURIComponent(name)}/usage${suffix ? `?${suffix}` : ''}`, withCallOptions(undefined, callOptions))
    },
    [],
  )

  const getSharedAssetUsage = useCallback(
    (projectId: string, include: SharedAssetUsageKind[], options?: ApiCallOptions) =>
      request<SharedAssetUsage>(`/projects/${encodeURIComponent(projectId)}/shared-asset-usage${query({ include: include.join(',') })}`, withCallOptions(undefined, options)),
    [],
  )

  // Scenario-builder drafts saved to the project (never launchable).
  const listScenarioDrafts = useCallback(
    (projectId: string, options?: ApiCallOptions) => request<{ drafts: ScenarioDraftSummary[]; count: number }>(`/projects/${encodeURIComponent(projectId)}/scenario-drafts`, withCallOptions(undefined, options)),
    [],
  )
  const getScenarioDraft = useCallback(
    (projectId: string, draftId: string, options?: ApiCallOptions) => request<ScenarioDraft>(`/projects/${encodeURIComponent(projectId)}/scenario-drafts/${encodeURIComponent(draftId)}`, withCallOptions(undefined, options)),
    [],
  )
  const createScenarioDraft = useCallback(
    (projectId: string, draft: ScenarioDraftSave, options?: ApiCallOptions) =>
      request<ScenarioDraft>(`/projects/${encodeURIComponent(projectId)}/scenario-drafts`, withCallOptions({ method: 'POST', body: JSON.stringify(draft) }, options)),
    [],
  )
  const updateScenarioDraft = useCallback(
    (projectId: string, draftId: string, draft: ScenarioDraftSave & { revision: number }, options?: ApiCallOptions) =>
      request<ScenarioDraft>(`/projects/${encodeURIComponent(projectId)}/scenario-drafts/${encodeURIComponent(draftId)}`, withCallOptions({ method: 'PUT', body: JSON.stringify(draft) }, options)),
    [],
  )
  // With a revision, the draft is deleted only if nobody has saved it since (409 otherwise).
  const deleteScenarioDraft = useCallback(
    (projectId: string, draftId: string, revision?: number, options?: ApiCallOptions) =>
      request<{ deleted: string }>(`/projects/${encodeURIComponent(projectId)}/scenario-drafts/${encodeURIComponent(draftId)}${revision ? `?revision=${revision}` : ''}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const createProjectSecrets = useCallback(
    (projectId: string, secrets: ProjectSecretWrite[], options?: ApiCallOptions) =>
      request<ProjectSecretsWriteResponse>(`/projects/${encodeURIComponent(projectId)}/secrets`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ secrets }),
        timeoutMs: PROJECT_SECRET_MUTATION_TIMEOUT_MS,
      }, options)),
    [],
  )

  const rotateProjectSecret = useCallback(
    (projectId: string, name: string, value: string, usage?: { expected_usage_revision?: string; acknowledge_shared_usage?: boolean }, options?: ApiCallOptions) =>
      request<ProjectSecretsWriteResponse>(`/projects/${encodeURIComponent(projectId)}/secrets/${encodeURIComponent(name)}`, withCallOptions({
        method: 'PUT',
        body: JSON.stringify({ value, ...usage }),
        timeoutMs: PROJECT_SECRET_MUTATION_TIMEOUT_MS,
      }, options)),
    [],
  )

  const listFolders = useCallback(
    (projectId: string, category?: string, options?: ApiCallOptions) =>
      request<{ folders: Folder[]; count: number }>(`/projects/${projectId}/folders${query({ category })}`, withCallOptions(undefined, options)),
    [],
  )

  const createFolder = useCallback(
    (projectId: string, data: { name: string; category: string; parent_id?: string | null }, options?: ApiCallOptions) =>
      request<Folder>(`/projects/${projectId}/folders`, withCallOptions({ method: 'POST', body: JSON.stringify(data) }, options)),
    [],
  )

  const updateFolder = useCallback(
    (id: string, data: { name?: string; parent_id?: string | null }, options?: ApiCallOptions) =>
      request<Folder>(`/folders/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(data) }, options)),
    [],
  )

  const deleteFolder = useCallback((id: string, options?: ApiCallOptions) => request<DeleteEntityResponse>(`/folders/${id}`, withCallOptions({ method: 'DELETE' }, options)), [])

  return useMemo(
    () => ({
      listProjects,
      createProject,
      updateProject,
      deleteProject,
      listProjectSecrets,
      getProjectSecretStatuses,
      getProjectSecretUsage,
      getSharedAssetUsage,
      listScenarioDrafts,
      getScenarioDraft,
      createScenarioDraft,
      updateScenarioDraft,
      deleteScenarioDraft,
      createProjectSecrets,
      rotateProjectSecret,
      listFolders,
      createFolder,
      updateFolder,
      deleteFolder,
    }),
    [listProjects, createProject, updateProject, deleteProject, listProjectSecrets, getProjectSecretStatuses, getProjectSecretUsage, getSharedAssetUsage, listScenarioDrafts, getScenarioDraft, createScenarioDraft, updateScenarioDraft, deleteScenarioDraft, createProjectSecrets, rotateProjectSecret, listFolders, createFolder, updateFolder, deleteFolder],
  )
}
