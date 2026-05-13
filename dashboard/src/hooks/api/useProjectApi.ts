import { useCallback, useMemo } from 'react'

import { Project, Folder } from '../../types'
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

export function useProjectApi() {
  const listProjects = useCallback(() => request<{ projects: Project[]; count: number }>('/projects'), [])

  const createProject = useCallback(
    (data: { name: string }) => request<Project>('/projects', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const updateProject = useCallback(
    (id: string, data: { name: string }) => request<Project>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteProject = useCallback((id: string) => request<DeleteEntityResponse>(`/projects/${id}`, { method: 'DELETE' }), [])

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
      listFolders,
      createFolder,
      updateFolder,
      deleteFolder,
    }),
    [listProjects, createProject, updateProject, deleteProject, listFolders, createFolder, updateFolder, deleteFolder],
  )
}
