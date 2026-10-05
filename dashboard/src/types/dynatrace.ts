import type { AppDynamicsCapability, AppDynamicsEntity, AppDynamicsEvidence } from './appdynamics'
import type { AnalysisPoint } from './analysis'

export interface DynatraceConfig {
  environment_url?: string; platform_url?: string; platform_auth_type?: 'platform_token' | 'oauth_client'
  oauth_resource?: string; oauth_scopes?: string[]
  timeout_seconds?: number; requests_per_minute?: number; platform_requests_per_minute?: number
  max_response_bytes?: number; max_series?: number; max_points?: number; max_requests_per_poll?: number; max_poll_seconds?: number
  max_scan_bytes?: number; max_run_scan_bytes?: number; drain_reserve_bytes?: number
}
export interface DynatraceMetricSelection {
  name: string; metric_selector: string; entity_selector?: string; unit?: string
  aggregation: 'avg' | 'min' | 'max' | 'sum' | 'count' | 'value' | 'p95' | 'p99'
  semantics?: 'gauge' | 'request_average'
}
export interface DynatraceGrailQuery {
  name: string; query: string; kind: 'metric' | 'log' | 'span' | 'event' | 'trace'; unit?: string; statistic?: string
  value_field?: string; fields?: string[]; bucket_seconds?: number; run_id_field?: string; entity_id_field?: string; entity_ids?: string[]
}
export interface DynatraceSelection {
  project_id?: string; metrics: DynatraceMetricSelection[]; entity_selector?: string
  problems: boolean; events: boolean; problem_selector?: string; event_selector?: string; topology_mode?: '' | 'classic' | 'smartscape'; grail: DynatraceGrailQuery[]
  smartscape_node_type?: string; smartscape_entity_ids?: string[]
  settle_seconds: number; run_header?: boolean; header_origins?: string[]
}
export interface DynatraceDiscoveryOptions {
  kind: 'metrics' | 'entities' | 'entity_types' | 'smartscape'; search?: string; selector?: string; entity_selector?: string
  metric_id?: string; cursor?: string; limit?: number; topology_mode?: string
}
export interface DynatraceOperationRequest { kind: 'verify' | 'discover' | 'preview'; source_id?: string; options?: DynatraceDiscoveryOptions; selection?: DynatraceSelection }
export interface DynatraceOperation {
  id: string; connection_id?: string; connection_revision?: number; kind: string; status: string; created_at: string; expires_at: string; message?: string
  result?: { capabilities?: Record<string, AppDynamicsCapability | string | boolean | Record<string, string>>; entities?: AppDynamicsEntity[]; items?: AppDynamicsEntity[]; metrics?: AppDynamicsEntity[]; next_cursor?: string; truncated?: boolean; message?: string; preview?: { points?: AnalysisPoint[]; evidence?: AppDynamicsEvidence[]; coverage?: Record<string, AppDynamicsCapability>; progress?: { scanned_bytes?: number; query_cost_known?: boolean } } }
}

export const emptyDynatraceSelection = (): DynatraceSelection => ({ metrics: [], problems: true, events: false, grail: [], settle_seconds: 120 })
export const defaultDynatraceBudgets = (): DynatraceConfig => ({ timeout_seconds: 10, requests_per_minute: 60, platform_requests_per_minute: 60, max_response_bytes: 4194304, max_series: 200, max_points: 10000, max_requests_per_poll: 30, max_poll_seconds: 30, max_scan_bytes: 1000000000, max_run_scan_bytes: 10000000000, drain_reserve_bytes: 2000000000 })
