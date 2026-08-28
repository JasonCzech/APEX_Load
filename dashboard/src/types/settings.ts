import type { DashboardAuthSettings } from './auth'
import type { CapacityReservation } from './capacity'

// Persisted dashboard/system settings transport contracts. Runtime form state
// and secret-entry drafts stay with their owning components.

export interface DashboardThresholdSettings {
  max_error_rate_pct?: number
  max_p99_latency_ms?: number
  // Auto-stop after this many consecutive breached P99 samples; unset/0 keeps
  // the P99 threshold alert-only.
  p99_latency_stop_consecutive_samples?: number
  max_avg_server_processing_ms?: number
  max_avg_transfer_ms?: number
  max_telemetry_sample_age_ms?: number
  max_telemetry_generator_lag_ms?: number
  max_telemetry_transport_lag_ms?: number
  max_telemetry_controller_fanout_ms?: number
  max_telemetry_browser_render_lag_ms?: number
  fleet_constrained_available_slots?: number
  fleet_saturated_available_slots?: number
}

export interface DashboardGeneralSettings {
  api_endpoint: string
  max_concurrent_tests: number
  metrics_interval_seconds: number
  usage_snapshot_interval_seconds?: number
}

export interface DashboardCapacityGeneratorSettings {
  cpu_cores: number
  memory_mb: number
  network_mbps: number
  max_vusers: number
}

export interface DashboardCapacityAvailabilitySettings {
  source: 'configured' | 'configured_plus_live'
  configured_available_generators: number
}

export interface DashboardCapacityPlanningSettings {
  headroom_pct: number
  generator: DashboardCapacityGeneratorSettings
  availability: DashboardCapacityAvailabilitySettings
  reservations?: CapacityReservation[]
}

export interface DashboardArchiveRetentionPolicy {
  name?: string
  project_id?: string
  release_id?: string
  run_status?: string
  run_type?: string
  artifact_type?: string
  compliance_class?: string
  reason?: string
  retention_days: number
}

export interface DashboardArchiveRetentionSettings {
  enabled: boolean
  default_retention_days: number
  har_retention_days?: number
  har_artifact_dir?: string
  policies?: DashboardArchiveRetentionPolicy[]
}

export interface DashboardScheduleDefaultsSettings {
  approval_policy?: 'auto' | 'manual_review' | 'manual' | 'high_cost' | 'off_hours' | 'production' | 'quota_burst' | string
  missed_run_policy?: 'skip' | 'catch_up_once' | 'catch_up_all' | 'require_operator_decision' | string
  blackout_start_time?: string
  blackout_end_time?: string
  availability_start_time?: string
  availability_end_time?: string
}

export interface DashboardNotificationWebhook {
  name?: string
  enabled: boolean
  url: string
  format: 'slack' | 'teams' | 'dynatrace'
  events?: string[]
  api_token?: string
  has_api_token?: boolean
  entity_selector?: string
  timeout_minutes?: number
}

export interface DashboardNotificationsSettings {
  webhooks: DashboardNotificationWebhook[]
}

export interface DashboardGitHubSettings {
  enabled: boolean
  client_id: string
  client_secret_env_var: string
  app_id: number
  app_slug: string
  app_private_key_env_var: string
  app_private_key_file: string
  webhook_secret_env_var: string
  pat_env_var: string
  automation_mode: string
  checks_enabled: boolean
  api_base_url: string
  web_base_url: string
  ca_cert_file: string
  tls_insecure_skip_verify: boolean
  scopes: string[]
  default_repo: string
  default_branch: string
  path_template: string
}

export interface DashboardPrometheusSettings {
  enabled: boolean
  base_url: string
  timeout_seconds: number
  bearer_token_env_var?: string
  allow_label_values: boolean
}

export interface DashboardGrafanaSettings {
  enabled: boolean
  base_url: string
  service_account_token_env_var: string
  timeout_seconds: number
}

export interface DashboardOpenSearchSettings {
  enabled: boolean
  base_url: string
  timeout_seconds: number
  username?: string
  password_env_var?: string
  bearer_token_env_var?: string
}

export interface DashboardPowerBISettings {
  enabled: boolean
  tenant_id?: string
  client_id?: string
  client_secret_env_var?: string
  workspace_id?: string
  dataset_name?: string
  auth_base_url?: string
  api_base_url?: string
  scope?: string
  timeout_seconds?: number
  dashboard_base_url?: string
}

export interface DashboardPowerBIPublish {
  id: string
  test_id: string
  project_id: string
  workspace_id: string
  dataset_id?: string
  dataset_name?: string
  schema_version: number
  status: 'pending' | 'partial' | 'complete' | 'failed' | string
  run_rows_pushed: number
  transaction_rows_pushed: number
  transaction_rows_total: number
  error?: string
  requested_by?: string
  published_at?: string
  created_at: string
  updated_at: string
}

export interface PowerBIPublishResponse {
  publish: DashboardPowerBIPublish
}

export interface PowerBIPublishStatusResponse {
  enabled: boolean
  publishes: DashboardPowerBIPublish[]
}

export interface PowerBITestConnectionResponse {
  ok: boolean
  error?: string
  workspace_id?: string
  dataset_name?: string
  dataset_exists?: boolean
  dataset_id?: string
}

export interface PowerBIDatasetRecreateResponse {
  dataset_id: string
  dataset_name: string
  schema_version: number
}

export interface DashboardClientAppSettings {
  availability: 'enabled' | 'disabled' | string
}

export interface DashboardClientDownloadsSettings {
  enabled: boolean
}

export interface DashboardScriptImportSettings {
  allow_source_secret_migration: boolean
}

export interface DashboardSystemSettings {
  /** Opaque optimistic-concurrency token returned with the settings ETag. */
  revision?: string
  general: DashboardGeneralSettings
  thresholds: DashboardThresholdSettings
  capacity_planning: DashboardCapacityPlanningSettings
  archive_retention: DashboardArchiveRetentionSettings
  schedule_defaults: DashboardScheduleDefaultsSettings
  notifications: DashboardNotificationsSettings
  auth: DashboardAuthSettings
  github: DashboardGitHubSettings
  prometheus: DashboardPrometheusSettings
  opensearch?: DashboardOpenSearchSettings
  grafana?: DashboardGrafanaSettings
  powerbi?: DashboardPowerBISettings
  client_app: DashboardClientAppSettings
  client_downloads: DashboardClientDownloadsSettings
  script_imports: DashboardScriptImportSettings
}
