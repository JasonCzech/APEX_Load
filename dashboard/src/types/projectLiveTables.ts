export interface ProjectLiveTableRow {
  id: string
  version: string
  values: Record<string, string>
  state?: string
}
export interface ProjectLiveTableView {
  id: string
  project_id: string
  name: string
  profile: 'native_queue' | 'vts_columns'
  columns: string[]
  state: 'active' | 'quiescing' | 'quiesced' | 'archived'
  config_revision: string
  instance_id: string
  stats?: { revision: string; data_bytes?: number; counts?: Record<string, number>; capacity?: { max_rows?: number; max_bytes?: number; max_receipts?: number; max_result_bytes?: number; retry_window_ms?: number } }
  attachments?: { id?: string; run_id: string; group_name: string; alias: string; state: string }[]
  permissions: { can_view: boolean; can_mutate: boolean; can_admin: boolean }
}
export interface ProjectLiveTableCreate {
  name: string
  profile: ProjectLiveTableView['profile']
  columns: string[]
  limits: { max_rows: number; max_bytes: number }
  seed_pool_id?: string
}
export interface ProjectLiveTableMutation {
  idempotency_key: string
  issued_at: string
  expected_instance_id: string
  expected_revision: string
  operation: 'append' | 'update' | 'clear' | 'increment'
  record_id?: string
  position?: number
  expected_version?: string
  values?: Record<string, string>
  columns?: string[]
  delta?: string
}
export interface ProjectLiveTableMutationResponse {
  result: { status: string; revision: string; row?: ProjectLiveTableRow; value?: string; count?: string }
  replayed: boolean
  expires_at: string
}
export interface ProjectLiveTableLifecycle {
  action: 'quiesce' | 'resume' | 'reset' | 'archive'
  expected_config_revision: string
  expected_instance_id: string
  expected_revision: string
  reason: string
}
export interface ProjectLiveTableRows {
  rows: ProjectLiveTableRow[]
  columns: string[]
  revision: string
  total: number
}

/** Run provenance identifies its attachment, without attributing shared contents. */
export interface ProjectLiveTableRunReference {
  table_id: string
  instance_id: string
  attachment_id: string
  group: string
  alias: string
  state: string
  revision: string
}
