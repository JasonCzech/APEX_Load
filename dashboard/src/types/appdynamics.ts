import type { AnalysisPoint } from './analysis'

export interface AppDynamicsConfig {
  endpoint: string
  context_path: string
  account_name: string
  auth_type: 'oauth' | 'basic'
  events_endpoint?: string
  events_account_name?: string
  approved_cidrs?: string[]
  ca_pem?: string
  timeout_seconds?: number
  requests_per_minute?: number
  analytics_requests_per_minute?: number
  max_response_bytes?: number
  max_series?: number
  max_points?: number
  max_requests_per_poll?: number
  max_poll_seconds?: number
}

export interface AppDynamicsCapability { status: string; message?: string; from?: string; to?: string; points?: number; evidence?: number; truncated?: boolean }
export type AppDynamicsCapabilities = Record<string, AppDynamicsCapability | Record<string, string> | boolean | string>
export function appDynamicsCapabilityHealth(capabilities?: AppDynamicsCapabilities): Record<string, AppDynamicsCapability> | undefined {
  if (!capabilities) return undefined
  const health: Record<string, AppDynamicsCapability> = {}
  const statuses = capabilities.status
  if (statuses && typeof statuses === 'object') {
    for (const [name, status] of Object.entries(statuses)) if (typeof status === 'string') health[name] = { status }
  }
  for (const [name, value] of Object.entries(capabilities)) {
    if (name === 'status' || name === 'metric_api' || health[name]) continue
    if (typeof value === 'boolean') health[name] = { status: value ? 'supported' : 'unavailable' }
    else if (typeof value === 'string') health[name] = { status: value || 'unknown' }
    else if (typeof value.status === 'string') health[name] = value as AppDynamicsCapability
  }
  return health
}
export interface AppDynamicsConnection {
  id: string; project_id: string; name: string; description?: string; enabled: boolean
  collection_mode: 'direct' | 'collector'; collector_agent_id?: string
  config: AppDynamicsConfig; revision: number; verified_revision?: number
  has_client_id: boolean; has_client_secret: boolean; has_username: boolean; has_password: boolean; has_events_api_key: boolean
  has_custom_ca?: boolean
  capabilities?: AppDynamicsCapabilities
  last_checked_at?: string; last_check_status?: string; last_check_message?: string
  created_at: string; updated_at: string
}

export interface AppDynamicsConnectionInput {
  name: string; description?: string; enabled: boolean
  collection_mode: 'direct' | 'collector'; collector_agent_id?: string
  config: AppDynamicsConfig; revision?: number
  client_id_secret?: string; client_secret_secret?: string; username_secret?: string; password_secret?: string; events_api_key_secret?: string
}

export interface AppDynamicsEntity { id: string; name: string; type?: string; path?: string; parent_id?: string; entity_ids?: Record<string, string> }
export interface AppDynamicsOperation {
  id: string; status: string; kind?: string; operation?: string; created_at: string; expires_at: string
  message?: string; error?: string
  result?: { entities?: AppDynamicsEntity[]; capabilities?: AppDynamicsCapabilities; message?: string; truncated?: boolean }
}
export interface AppDynamicsMetricSelection {
  application_id: string; path: string; name: string; unit: string
  statistic: 'value' | 'average' | 'sum' | 'current' | 'p95' | 'p99'
  entity_ids?: Record<string, string>
}
export interface AppDynamicsAnalyticsQuery {
  name: string; query: string; application_id: string
  kind?: 'log' | 'transaction' | 'metric'; unit?: string
  statistic?: 'value' | 'sum' | 'average'; bucket_seconds?: number; run_id_field?: string
  application_id_field?: string; application_name?: string
}
export interface AppDynamicsSelection {
  project_id?: string; applications: string[]; metrics: AppDynamicsMetricSelection[]
  scope_ids?: Record<string, string[]>
  events: boolean; violations: boolean; snapshots: boolean
  analytics: AppDynamicsAnalyticsQuery[]
  settle_seconds: number
  run_header?: boolean; header_origins?: string[]
}
export interface MonitorSource {
  id: string; name: string; project_id?: string; revision: number; collector_id: string
  pack: string; destination?: string; interval_seconds: number; enabled: boolean
  credential_ref?: string; collector_public_key?: string; service?: string
  connection_id?: string; executor_mode?: 'direct' | 'collector'; appdynamics?: AppDynamicsSelection
}
export interface MonitorBinding { source_id: string; required: boolean }
export interface MonitorRule {
  source_id: string; metric: string; dimensions?: Record<string, string>; unit: string
  statistic: 'avg' | 'min' | 'max' | 'sum'; comparator: 'lt' | 'lte' | 'gt' | 'gte' | 'eq' | 'neq'
  threshold: number; severity: 'fail' | 'warning'; min_coverage_pct: number
  start_offset_seconds: number; end_offset_seconds?: number
}
export interface MonitorRuleResult { rule: MonitorRule; status: string; observed?: number; coverage_pct: number; eligible_buckets: number; message: string }
export interface AppDynamicsEvidence {
  id?: string; source_id?: string; type?: string; kind?: string; timestamp_utc?: string; timestamp?: string
  severity?: string; title?: string; summary?: string; message?: string; application_id?: string; entity_id?: string
  business_transaction_id?: string; correlation?: string; correlation_strength?: string; status?: string
  entity_ids?: Record<string, string>; deep_link?: string; properties?: Record<string, string>
}
export interface RunMonitorJob {
  id: string; source: MonitorSource; state: string; next_sequence: number; coverage_reason?: string
  started_at?: string; load_ended_at?: string; drain_deadline?: string; last_success_at?: string; query_watermark?: string
  coverage?: Record<string, AppDynamicsCapability>; points?: AnalysisPoint[]; topology?: AppDynamicsEntity[]
}
export interface RunMonitorCoverage {
  job_id: string; source_id: string; source_revision: number; binding_revision: number
  connection_id?: string; connection_revision?: number; executor_mode?: string
  appdynamics?: AppDynamicsSelection; required: boolean; state: string; reason?: string; batches: number
  provider_coverage?: Record<string, AppDynamicsCapability>; topology?: AppDynamicsEntity[]
}
export interface RunMonitorDiagnostic { source_id: string; diagnostic: AppDynamicsEvidence }
