import { useMemo } from 'react'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'
import type { ProjectLiveTableCreate, ProjectLiveTableLifecycle, ProjectLiveTableMutation, ProjectLiveTableMutationResponse, ProjectLiveTableRows, ProjectLiveTableView } from '../../types/projectLiveTables'

const collection = (projectId: string) => `/projects/${encodeURIComponent(projectId)}/live-tables`
const tablePath = (projectId: string, tableId: string) => `${collection(projectId)}/${encodeURIComponent(tableId)}`
const revisionQuery = (instance: string, revision: string) => new URLSearchParams({ instance_id: instance, revision })

export function useProjectLiveTablesApi() {
  return useMemo(() => ({
    listProjectLiveTables: (projectId: string, options?: ApiCallOptions) => request<{ tables: ProjectLiveTableView[] }>(collection(projectId), withCallOptions(undefined, options)),
    getProjectLiveTable: (projectId: string, tableId: string, options?: ApiCallOptions) => request<ProjectLiveTableView>(tablePath(projectId, tableId), withCallOptions(undefined, options)),
    createProjectLiveTable: (projectId: string, body: ProjectLiveTableCreate) => request<ProjectLiveTableView>(collection(projectId), { method: 'POST', body: JSON.stringify(body) }),
    getProjectLiveTableRows: (projectId: string, tableId: string, instance: string, revision: string, offset = 0, options?: ApiCallOptions) => request<ProjectLiveTableRows>(`${tablePath(projectId, tableId)}/rows?${revisionQuery(instance, revision)}&offset=${offset}&limit=100`, withCallOptions({ cache: 'no-store' }, options)),
    exportProjectLiveTable: (projectId: string, tableId: string, instance: string, revision: string) => request<unknown>(`${tablePath(projectId, tableId)}/export?${revisionQuery(instance, revision)}`, { cache: 'no-store', maxResponseBytes: 32 * 1024 * 1024 }),
    mutateProjectLiveTable: (projectId: string, tableId: string, body: ProjectLiveTableMutation) => request<ProjectLiveTableMutationResponse>(`${tablePath(projectId, tableId)}/mutations`, { method: 'POST', body: JSON.stringify(body) }),
    changeProjectLiveTableLifecycle: (projectId: string, tableId: string, body: ProjectLiveTableLifecycle) => request<ProjectLiveTableView>(`${tablePath(projectId, tableId)}/lifecycle`, { method: 'POST', body: JSON.stringify(body) }),
  }), [])
}
