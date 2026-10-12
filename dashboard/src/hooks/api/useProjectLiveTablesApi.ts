import { useMemo } from 'react'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'
import type { ProjectLiveTableCreate, ProjectLiveTableLifecycle, ProjectLiveTableMutation, ProjectLiveTableMutationResponse, ProjectLiveTableRows, ProjectLiveTableView } from '../../types/projectLiveTables'

const revisionQuery = (instance: string, revision: string) => new URLSearchParams({ instance_id: instance, revision })

export function useProjectLiveTablesApi() {
  return useMemo(() => ({
    listProjectLiveTables: (projectId: string, options?: ApiCallOptions) => request<{ tables: ProjectLiveTableView[] }>(`/projects/${encodeURIComponent(projectId)}/live-tables`, withCallOptions(undefined, options)),
    getProjectLiveTable: (projectId: string, tableId: string, options?: ApiCallOptions) => request<ProjectLiveTableView>(`/projects/${encodeURIComponent(projectId)}/live-tables/${encodeURIComponent(tableId)}`, withCallOptions(undefined, options)),
    createProjectLiveTable: (projectId: string, body: ProjectLiveTableCreate) => request<ProjectLiveTableView>(`/projects/${encodeURIComponent(projectId)}/live-tables`, { method: 'POST', body: JSON.stringify(body) }),
    getProjectLiveTableRows: (projectId: string, tableId: string, instance: string, revision: string, offset = 0, options?: ApiCallOptions) => request<ProjectLiveTableRows>(`/projects/${encodeURIComponent(projectId)}/live-tables/${encodeURIComponent(tableId)}/rows?${revisionQuery(instance, revision)}&offset=${offset}&limit=100`, withCallOptions({ cache: 'no-store' }, options)),
    exportProjectLiveTable: (projectId: string, tableId: string, instance: string, revision: string) => request<unknown>(`/projects/${encodeURIComponent(projectId)}/live-tables/${encodeURIComponent(tableId)}/export?${revisionQuery(instance, revision)}`, { cache: 'no-store', maxResponseBytes: 32 * 1024 * 1024 }),
    mutateProjectLiveTable: (projectId: string, tableId: string, body: ProjectLiveTableMutation) => request<ProjectLiveTableMutationResponse>(`/projects/${encodeURIComponent(projectId)}/live-tables/${encodeURIComponent(tableId)}/mutations`, { method: 'POST', body: JSON.stringify(body) }),
    changeProjectLiveTableLifecycle: (projectId: string, tableId: string, body: ProjectLiveTableLifecycle) => request<ProjectLiveTableView>(`/projects/${encodeURIComponent(projectId)}/live-tables/${encodeURIComponent(tableId)}/lifecycle`, { method: 'POST', body: JSON.stringify(body) }),
  }), [])
}
