import { request } from '../apiClient'

// Analysis policies (BI-4 editors): revisioned project definitions. Saving an
// existing policy names the revision it was read at; the server refuses a
// changed one (409), so concurrent edits never overwrite each other. Runs pin
// the revision they started with, and archived findings keep the revision
// they were evaluated under: editing a policy never rewrites them.

export type EditableAnalysisPolicyKind = 'browser_policy' | 'journey_policy'

export interface AnalysisPolicyRecord {
  id: string
  project_id: string
  kind: string
  name: string
  revision: number
  definition: unknown
  deleted: boolean
  created_at: string
}

export interface AnalysisPolicySave {
  kind: EditableAnalysisPolicyKind
  name: string
  definition: unknown
}

const scoped = (projectId: string, extra: Record<string, string> = {}) => new URLSearchParams({ project_id: projectId, ...extra }).toString()

export const analysisPoliciesApi = {
  list(projectId: string, kind: EditableAnalysisPolicyKind, signal?: AbortSignal) {
    return request<AnalysisPolicyRecord[]>(`/analysis/policies?${scoped(projectId, { kind, limit: '100' })}`, { signal })
  },
  get(projectId: string, id: string, revision?: number, signal?: AbortSignal) {
    return request<AnalysisPolicyRecord>(`/analysis/policies/${encodeURIComponent(id)}?${scoped(projectId, revision ? { revision: String(revision) } : {})}`, { signal })
  },
  create(projectId: string, policy: AnalysisPolicySave) {
    return request<AnalysisPolicyRecord>(`/analysis/policies?${scoped(projectId)}`, { method: 'POST', body: JSON.stringify(policy) })
  },
  update(projectId: string, id: string, revision: number, policy: AnalysisPolicySave) {
    return request<AnalysisPolicyRecord>(`/analysis/policies/${encodeURIComponent(id)}?${scoped(projectId)}`, { method: 'PUT', body: JSON.stringify({ ...policy, revision }) })
  },
  remove(projectId: string, id: string, revision: number) {
    return request<AnalysisPolicyRecord>(`/analysis/policies/${encodeURIComponent(id)}?${scoped(projectId, { revision: String(revision) })}`, { method: 'DELETE' })
  },
}
