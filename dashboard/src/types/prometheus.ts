export interface PrometheusConfig {
  endpoint: string
  backend_profile: 'prometheus' | 'azure_managed'
  auth_type: 'none' | 'bearer' | 'basic' | 'azure'
  approved_cidrs?: string[]; ca_pem?: string; allow_private_http?: boolean
  context_path?: string; mtls?: boolean; client_id?: string; azure_tenant_id?: string; azure_credential_mode?: 'workload_identity' | 'managed_identity' | 'service_principal'
  timeout_seconds?: number; requests_per_minute?: number; max_response_bytes?: number; max_series?: number; max_points?: number
}
export interface PrometheusQuery {
  id: string; name: string; expression: string; unit: string
  semantics: 'gauge' | 'rate' | 'counter' | 'quantile' | 'sum' | 'value' | 'average' | 'minimum' | 'maximum' | 'p95' | 'p99' | 'count'
  allowed_labels: string[]; min_series: number; freshness_expression?: string; max_sample_age_seconds?: number; terminal_expression?: string
  parameters?: Record<string, PrometheusParameter>
}
export interface PrometheusParameter { type: 'label_value' | 'duration' | 'timestamp'; value: string }
export interface PrometheusSelection {
  project_id?: string; queries: PrometheusQuery[]; step_seconds: number; settle_seconds: number; targets: boolean; alerts: boolean; evidence_selector?: string
}
export interface PrometheusCapability {
  status: string; message?: string; from?: string; to?: string; points?: number; evidence?: number; truncated?: boolean; expected_points?: number; missing_points?: number; warnings?: string[]; infos?: string[]; freshness?: string
}
export interface PrometheusConnection {
  id: string; project_id: string; name: string; description?: string; enabled: boolean
  collection_mode: 'direct' | 'collector'; collector_agent_id?: string
  config: PrometheusConfig; revision: number; verified_revision?: number; status?: string
  has_token?: boolean; has_username?: boolean; has_password?: boolean; has_client_secret?: boolean
  has_client_certificate?: boolean; has_client_key?: boolean; has_custom_ca?: boolean
  capabilities?: { metrics?: boolean; discovery?: boolean; targets?: boolean; alerts?: boolean; exemplars?: boolean; version?: string; backend_profile?: string; status?: Record<string, string> }; last_checked_at?: string; last_check_status?: string; last_check_message?: string
  created_at?: string; updated_at?: string
}
export interface PrometheusConnectionInput {
  name: string; description?: string; enabled: boolean
  collection_mode: 'direct' | 'collector'; collector_agent_id?: string; config: PrometheusConfig; revision?: number
  token_secret?: string; username_secret?: string; password_secret?: string
  client_secret_secret?: string; client_certificate_secret?: string; client_key_secret?: string
}
export interface PrometheusDiscoveryEntry { name?: string; id?: string; type?: string; entity_ids?: Record<string, string>; labels?: Record<string, string>; help?: string; unit?: string; value?: string }
export interface PrometheusOperation {
  id: string; connection_id?: string; connection_revision?: number; status: string; kind?: string; created_at?: string; expires_at?: string; message?: string; error?: string
  result?: { capabilities?: { metrics?: boolean; discovery?: boolean; targets?: boolean; alerts?: boolean; exemplars?: boolean; version?: string; backend_profile?: string; status?: Record<string, string> }; entities?: PrometheusDiscoveryEntry[]; truncated?: boolean; message?: string; points?: unknown[]; coverage?: Record<string, PrometheusCapability> }
}
export interface PrometheusInspectionInput {
  kind?: string; selector?: string; label?: string; selection?: PrometheusSelection; from_utc?: string; to_utc?: string
}
export interface PrometheusDashboardDraft {
  selection: PrometheusSelection; dashboard_id: string; dashboard_sha256: string
  connection_id: string; connection_revision: number; range_seconds: number
  resolved_bindings?: Record<string, string>; datasource_uids: string[]; warnings: string[]; truncated: boolean
}
