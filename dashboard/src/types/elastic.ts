import type { AnalysisPoint } from './analysis'

export type ElasticProduct = 'elasticsearch' | 'opensearch'
export interface ElasticConfig {
  endpoint: string; product: ElasticProduct; auth_type: 'api_key' | 'basic' | 'bearer' | 'mtls' | ''
  approved_cidrs?: string[]; ca_pem?: string; timeout_seconds?: number; requests_per_minute?: number
  max_response_bytes?: number; max_series?: number; max_points?: number; max_evidence?: number
  max_requests_per_poll?: number; max_poll_seconds?: number; max_retries?: number
  kibana_endpoint?: string; logstash_endpoint?: string; dashboards_url?: string
  kibana_ca_pem?: string; kibana_approved_cidrs?: string[]; logstash_ca_pem?: string; logstash_approved_cidrs?: string[]
}
export interface ElasticCapability {
  status: string; message?: string; truncated?: boolean; points?: number; evidence?: number
  from?: string; to?: string; query_id?: string; reason?: string; as_of?: string; late_arrival_seconds?: number
}
export interface ElasticCapabilities {
  product: string; version?: string; serverless: boolean; dsl: boolean; esql: boolean; pit: boolean
  async_esql?: boolean; shard_doc: boolean; metrics: boolean; logs: boolean; traces: boolean; health: boolean; status?: Record<string, string>
}
export interface ElasticField { name: string; types: string[]; searchable: boolean; aggregatable: boolean; conflict: boolean }
export interface ElasticIndex { name: string; type: string }
export interface ElasticScalar { kind: 'string' | 'number' | 'boolean' | 'null'; string_value?: string; number_value?: number; bool_value?: boolean }
export interface ElasticRecord {
  id?: string; source_id?: string; kind?: string; timestamp_utc?: string; application_id?: string
  received_at?: string; ingested_at?: string; severity?: string; summary?: string; service?: string; correlation?: string; status?: string; deep_link?: string
  provider?: string; query_id?: string; index?: string; document_id?: string; entity_ids?: Record<string, string>; properties?: Record<string, string>
  log?: { message: string; level?: string; attributes?: Record<string, ElasticScalar> }
  span?: { trace_id: string; span_id: string; parent_span_id?: string; operation?: string; duration_ms: number; attributes?: Record<string, ElasticScalar> }
  transaction?: { name: string; duration_ms: number; attributes?: Record<string, ElasticScalar> }
}
export interface ElasticMetric {
  name: string; field?: string; statistic: string; kind?: string; unit?: string
  bucket_seconds?: number; exact_window?: boolean; group_by?: string[]; sum_field?: string; count_field?: string
}
export interface ElasticQuery {
  name: string; language: 'dsl' | 'esql'; kind: string; dsl?: Record<string, unknown>; esql?: string; params?: unknown[]
  timestamp_column?: string; value_column?: string; count_column?: string; sum_column?: string
  statistic?: string; unit?: string; bucket_seconds?: number; group_by?: string[]; dimension_columns?: string[]; exact_window?: boolean; approximate?: boolean; async?: boolean
}
export interface ElasticSelection {
  project_id?: string; indices: string[]; preset?: string
  timestamp_field?: string; ingested_field?: string; service_field?: string; service_values?: string[]
  run_id_field?: string; trace_id_field?: string; span_id_field?: string; transaction_id_field?: string
  parent_span_id_field?: string; message_field?: string; severity_field?: string; duration_field?: string
  duration_unit?: string; status_field?: string; unique_sort_field?: string; attribute_fields?: string[]; projected_fields?: string[]
  logs?: boolean; traces?: boolean; transactions?: boolean; errors?: boolean; cluster_health?: boolean; kibana_health?: boolean; logstash_health?: boolean
  metrics?: ElasticMetric[]; queries?: ElasticQuery[]; settle_seconds?: number; overlap_seconds?: number
  max_window_seconds?: number; page_size?: number; run_header?: boolean; header_origins?: string[]
  max_retained_records?: number; max_retained_bytes?: number
}
export interface ElasticInspection {
  id: string; connection_id: string; connection_revision: number
  kind: 'verify' | 'discover_indices' | 'discover_fields' | 'preview'
  status: string; message?: string; created_at: string; expires_at: string
  result?: { preview?: { points: AnalysisPoint[]; evidence: ElasticRecord[]; coverage: Record<string, ElasticCapability>; capabilities?: ElasticCapabilities }; capabilities?: ElasticCapabilities; indices?: ElasticIndex[]; fields?: ElasticField[]; points?: AnalysisPoint[]; evidence?: ElasticRecord[]; coverage?: Record<string, ElasticCapability>; columns?: { name: string; type: string }[]; rows?: unknown[][]; truncated?: boolean; message?: string }
}
export interface ElasticInspectionInput {
  kind: ElasticInspection['kind']; expected_revision?: number; indices?: string[]
  selection?: ElasticSelection; from_utc?: string; to_utc?: string; limit?: number
}

export interface ElasticMonitoringMetadata {
  product: ElasticProduct; preset?: string; mapping_revision: string
  metrics?: ElasticMetric[]; queries?: { id: string; language: string; signal: string; statistic?: string; unit?: string; revision: string }[]
  signals?: string[]
}
