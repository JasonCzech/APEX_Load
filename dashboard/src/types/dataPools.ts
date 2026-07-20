/** Shared parameter-data pools and their scenario bindings. */
export interface SharedDataPool {
  id: string
  name: string
  description?: string
  schema?: Record<string, string>
  project_id?: string
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface SharedDataPoolRow {
  id: string
  pool_id: string
  data: Record<string, string>
  status?: string
  version: number
  created_at: string
  updated_at: string
}

export interface ScenarioDataPoolBinding {
  id?: string
  scenario_id?: string
  pool_id: string
  pool_name?: string
  alias: string
  access_mode: 'read_only' | 'read_write' | 'consume'
  selection_strategy: 'sequential' | 'random' | 'unique'
  created_at?: string
  updated_at?: string
}
