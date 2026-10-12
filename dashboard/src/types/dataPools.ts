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
  scope?: 'run' | 'project'
  project_table_id?: string
  id?: string
  scenario_id?: string
  pool_id: string
  pool_name?: string
  alias: string
  access_mode: 'read_only' | 'read_write' | 'consume' | 'live'
  selection_strategy: 'sequential' | 'random' | 'unique' | 'manual' | 'next' | 'pop' | 'lease'
  live_options?: LiveTableOptions
  created_at?: string
  updated_at?: string
}

export interface LiveTableOptions {
  columns?: string[]
  profile?: 'native_queue' | 'vts_columns'
  seed_policy?: 'current_at_start' | 'empty' | 'original_seed'
  seed_run_id?: string
  durability?: 'memory' | 'durable'
  end_mode?: 'discard' | 'save_as_new' | 'write_back'
  empty_policy?: 'error' | 'skip' | 'wait'
  wait_ms?: number
  operation_timeout_ms?: number
  lease_ttl_ms?: number
  max_lease_lifetime_ms?: number
  max_delivery_attempts?: number
  max_rows?: number
  max_bytes?: number
  max_result_bytes?: number
  retry_window_ms?: number
  max_receipts?: number
  output_pool_name?: string
}

export interface LiveTableOperationStats {
  observations?: {
    version: number
    captured_at?: string
    wait_count: number
    wait_total_ns: number
    empty_outcomes: number
    throttled: number
    unknown_outcomes: number
    unknown_pop_attempts: number
    recovery_gaps: number
    incomplete: boolean
    dropped_events: number
    events?: { kind: string; first: string; last: string; count: number }[]
  }
  op_count?: number
  op_errors?: number
  op_unknown?: number
  op_latency_total_ns?: number
  op_latency_max_ns?: number
  op_latency_buckets?: number[]
}
export interface RunArchiveLiveTableSummary extends LiveTableOperationStats {
  table_id: string
  alias: string
  profile: string
  durability: string
  end_mode: string
  state: string
  revision: number
  data_bytes: number
  counts: Partial<Record<'seeded' | 'pushed' | 'available' | 'leased' | 'acknowledged' | 'popped' | 'quarantined' | 'discarded' | 'delivery_attempts' | 'redeliveries', number>>
  finalization?: { state?: string; mode?: string; output_pool_id?: string; reason?: string }
}
