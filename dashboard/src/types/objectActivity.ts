export type ActivityResourceType = 'script' | 'scenario' | 'test' | 'external_result_import'
export type ArchiveState = 'active' | 'archived' | 'all'
export interface ObjectActivityEvent {
  id: string
  action: string
  actor_type: string
  actor_subject: string
  actor_display_name?: string
  initiated_by?: string
  project_id: string
  resource_type: ActivityResourceType
  resource_id: string
  resource_name: string
  status: string
  changed_fields?: string[]
  created_at: string
  deleted?: boolean
}
export interface ObjectActivitySummary {
  creator?: ObjectActivityEvent
  last_modifier?: ObjectActivityEvent
  last_archive?: ObjectActivityEvent
}
export interface ObjectActivityResponse {
  events: ObjectActivityEvent[]
  has_more: boolean; activity?: ObjectActivitySummary
  count: number
  limit: number
  offset: number
}
