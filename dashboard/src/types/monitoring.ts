/** Contracts for project-scoped infrastructure monitoring collectors. */
export interface MonitoringCollector {
  id: string
  project_id: string
  name: string
  description?: string
  health: string
  last_seen_at?: string
  allowed_metric_packs: string[]
  created_at: string
  updated_at: string
}

export interface MonitoringCollectorListResponse {
  collectors: MonitoringCollector[]
  count: number
  limit?: number
  offset?: number
}

export interface MonitoringCollectorTokenResponse {
  collector: MonitoringCollector
  token: string
}

export interface CollectorMetricPack {
  pack: string
  timestamp: string
  samples: Record<string, number>
}

export interface CollectorMetricSnapshot {
  collector_id: string
  project_id?: string
  health: string
  received_at?: string
  age_seconds?: number
  stale: boolean
  pending?: boolean
  packs: CollectorMetricPack[]
}

export type ELKMetricAggregation = {
  name: string
  field: string
  operation: 'avg' | 'min' | 'max' | 'sum'
}

export interface ELKConnection {
  id: string
  project_id: string
  name: string
  description?: string
  enabled: boolean
  status: 'unconfigured' | 'ready' | 'disabled'
  endpoint?: string
  index_pattern?: string
  timestamp_field?: string
  level_field?: string
  message_field?: string
  test_id_field?: string
  trace_id_field?: string
  base_filter?: string
  error_filter?: string
  auth_type?: 'api_key' | 'basic'
  has_api_key: boolean
  has_basic_username: boolean
  has_basic_password: boolean
  metric_aggregations: ELKMetricAggregation[]
  last_checked_at?: string
  last_check_status?: string
  last_check_message?: string
  created_at: string
  updated_at: string
}

export interface ELKConnectionListResponse {
  connections: ELKConnection[]
  count: number
}

export interface ELKLogSample {
  id?: string
  timestamp?: string
  level?: string
  message?: string
  test_id?: string
  trace_id?: string
}

export interface ELKCaptureStatus {
  connection_id: string
  connection_name?: string
  status: string
  last_polled_at?: string
  last_successful_at?: string
  message?: string
  metrics?: Record<string, number>
  samples?: ELKLogSample[]
}

export type DynatraceMetricQuery = {
  name: string
  metric_selector: string
  entity_selector?: string
  aggregation: 'avg' | 'min' | 'max' | 'sum'
}

export interface DynatraceConnection {
  revision?: number; verified_revision?: number; collection_mode?: 'direct' | 'collector'; collector_agent_id?: string
  events_enabled?: boolean; spans_enabled?: boolean; grail_metrics_enabled?: boolean; grail_events_enabled?: boolean; topology_mode?: '' | 'classic' | 'smartscape'
  requests_per_minute?: number; platform_requests_per_minute?: number; max_scan_bytes?: number; max_run_scan_bytes?: number; drain_reserve_bytes?: number
  capabilities?: Record<string, unknown>
  id: string
  project_id: string
  name: string
  description?: string
  enabled: boolean
  status: 'unconfigured' | 'ready' | 'disabled'
  environment_url?: string
  platform_url?: string
  metrics_enabled: boolean
  problems_enabled: boolean
  logs_enabled: boolean
  has_environment_token: boolean
  platform_auth_type?: 'platform_token' | 'oauth_client'
  has_platform_token: boolean
  has_oauth_client_id: boolean
  has_oauth_client_secret: boolean
  oauth_resource?: string
  oauth_scopes: string[]
  metric_queries: DynatraceMetricQuery[]
  problem_selector?: string
  problem_entity_selector?: string
  log_base_filter?: string
  log_test_id_field?: string
  log_timestamp_field?: string
  log_status_field?: string
  log_message_field?: string
  log_trace_id_field?: string
  poll_interval_seconds: number
  last_checked_at?: string
  last_check_status?: string
  last_check_message?: string
  last_capability_health?: Record<string, { status: string; message?: string }>
  created_at: string
  updated_at: string
}

export interface DynatraceConnectionListResponse { connections: DynatraceConnection[]; count: number }
export interface DynatraceLogSample { timestamp?: string; status?: string; message?: string; test_id?: string; trace_id?: string }
export interface DynatraceProblemSummary { id?: string; display_id?: string; title?: string; severity?: string; status?: string; start_time?: string; end_time?: string; affected_entity?: string }
export interface DynatraceCaptureStatus {
  connection_id: string
  connection_name?: string
  status: string
  last_polled_at?: string
  last_successful_at?: string
  message?: string
  capabilities?: Record<string, { status: string; message?: string }>
  metrics?: Record<string, number>
  logs?: DynatraceLogSample[]
  problems?: DynatraceProblemSummary[]
}
export interface DynatraceEvidenceRecord { type: 'log_sample' | 'problem_summary' | 'log' | 'problem'; log?: DynatraceLogSample; problem?: DynatraceProblemSummary }
