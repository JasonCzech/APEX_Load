/** Contracts for reusable browser selectors and replay self-healing. */
export interface RepositoryObjectCandidate {
  kind: string
  value: string
  confidence: number
  reason?: string
}

export interface RepositoryObject {
  logical_name: string
  description?: string
  css?: string
  xpath?: string
  role?: string
  text?: string
  attributes?: Record<string, string>
  ordinal?: string
  regex?: string
  frame_context?: string
  window_context?: string
  candidates?: RepositoryObjectCandidate[]
  best_confidence?: number
  source_script_id?: string
}

export interface ObjectRepository {
  id: string
  project_id?: string
  name: string
  description?: string
  source?: string
  objects: RepositoryObject[]
  auto_promote_on_heal?: boolean
  created_at: string
  updated_at: string
}

export interface ObjectRepositoryListResponse {
  object_repositories: ObjectRepository[]
  count: number
  limit?: number
  offset?: number
}
