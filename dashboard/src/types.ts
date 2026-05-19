import { type BrowserConfig, type PacingConfig, type ScriptDefinition, type ThinkTimeConfig } from './types/scripts'

export interface TestGroup {
  name: string
  script_id: string
  script_name: string
  protocol: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  iterations?: number
  initialization?: 'at_runtime' | 'at_once'
}

export interface GeneratorAssignmentSettings {
  mode?: 'auto' | 'specific_hosts' | 'region_weighted'
  selected_targets?: string[]
  region_weights?: Record<string, number>
}

export interface ScenarioFootprint {
  total_vusers: number
  duration: string
  complexity_score: number
  estimated_cpu_cores: number
  estimated_memory_mb: number
  estimated_network_mbps: number
}

export interface AssignmentRecommendation {
  mode: 'auto' | 'specific_hosts' | 'region_weighted'
  available_targets?: string[]
  recommended_targets?: string[]
  available_regions?: string[]
  region_weights?: Record<string, number>
  summary?: string
  warnings?: string[]
}

export interface Project {
  id: string
  name: string
  settings?: ProjectSettings
  created_at: string
  updated_at: string
}

export interface RedactionRule {
  name?: string
  pattern: string
  replacement?: string
}

export interface ProjectSettings {
  redaction_rules?: RedactionRule[]
  max_concurrent_tests?: number
  max_vusers?: number
}

export interface Application {
  id: string
  project_id?: string
  name: string
  created_at: string
  updated_at: string
}

export interface Release {
  id: string
  application_id: string
  name: string
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface ReleaseRef {
  id: string
  name: string
  application_id: string
}

export interface Folder {
  id: string
  project_id: string
  parent_id?: string | null
  name: string
  category: 'scripts' | 'scenarios' | 'results' | 'recordings'
  created_at: string
  updated_at: string
}

export type RuntimeBrowserSettings = Pick<BrowserConfig, 'headless' | 'device_profile' | 'touch_mode' | 'orientation'>

export interface RuntimeSettingsOverride {
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
  browser?: RuntimeBrowserSettings
  log_level?: string
  auto_transactions?: string
  error_handling?: {
    continue_on_error?: boolean
    snapshot_on_error?: boolean
  }
}

export interface ScenarioRuntimeSettingsDraft {
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
  browser?: RuntimeBrowserSettings
  log_level: string
  auto_transactions: string
  continue_on_error: boolean
  snapshot_on_error: boolean
}

export type ScenarioMode = 'vuser_driven' | 'goal_oriented'

export interface GoalConfig {
  target_average_tps?: number
  p95_latency_ms?: number
  max_error_pct?: number
  max_avg_server_processing_ms?: number
  max_avg_transfer_ms?: number
  max_error_count?: number
  min_vusers?: number
  max_vusers?: number
  stop_on_sla_breach?: boolean
  stop_on_max_errors?: boolean
  sla_breach_window?: string
}

export interface GoalStatus {
  mode: 'goal_oriented'
  current_tps: number
  current_total_vusers: number
  target_average_tps?: number
  max_error_count?: number
  min_vusers?: number
  max_vusers?: number
  suggested_total_vusers?: number
  last_scale_direction?: 'up' | 'down'
  last_scale_reason?: string
  last_scale_at?: string
  latency_breached?: boolean
  error_breached?: boolean
  server_processing_breached?: boolean
  transfer_breached?: boolean
  error_count_breached?: boolean
  breach_active?: boolean
  breach_duration_sec?: number
  stop_triggered?: boolean
  stop_reason?: string
}

export interface RunComparisonBaseline {
  source_test_id: string
  source_test_name: string
  policy?: 'pinned' | 'latest_successful' | 'latest_finished' | string
  completed_at?: string
  transactions?: Record<string, TxMetrics>
  dependency_health?: Record<string, TxMetrics>
}

export interface RunArchiveExecutionSettingsSnapshot {
  general: {
    api_endpoint: string
    max_concurrent_tests: number
    metrics_interval_seconds: number
  }
  thresholds: {
    max_error_rate_pct?: number
    max_p99_latency_ms?: number
    max_avg_server_processing_ms?: number
    max_avg_transfer_ms?: number
    max_telemetry_sample_age_ms?: number
    max_telemetry_generator_lag_ms?: number
    max_telemetry_transport_lag_ms?: number
    max_telemetry_controller_fanout_ms?: number
    max_telemetry_browser_render_lag_ms?: number
  }
  capacity_planning: {
    headroom_pct: number
    generator: {
      cpu_cores: number
      memory_mb: number
      network_mbps: number
      max_vusers: number
    }
    availability: {
      source: string
      configured_available_generators: number
    }
  }
}

export interface RunArchiveProvenance {
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  requested_at?: string
  started_at?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  start_delay?: string
  total_vusers?: number
  duration?: string
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  runtime_settings?: RuntimeSettingsOverride
  groups?: TestGroup[]
  baseline_pinned?: boolean
  baseline_pinned_at?: string
  execution_settings: RunArchiveExecutionSettingsSnapshot
  controller_env?: Record<string, string>
  generator_inventory?: Array<{
    target: string
    generator_id?: string
    status: string
    available_capacity: number
    active_vusers?: number
    cpu_percent?: number
    memory_bytes?: number
    goroutine_count?: number
    cpu_limit_cores?: number
    memory_limit_bytes?: number
    max_vusers?: number
  }>
}

export interface RunArchiveErrorPattern {
  signature: string
  sample: string
  count: number
  first_seen: string
  latest_seen: string
  actions?: string[]
  snapshot?: {
    request_url?: string
  }
}

export interface TestSummary {
  id: string
  name: string
  status: TestStatus
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  folder_id?: string | null
  groups: TestGroup[]
  capacity_advisory?: CapacityAdvisory
  assignment?: GeneratorAssignmentSettings
  script_shakeout?: boolean
  resource_baseline?: boolean
  total_vusers: number
  duration: string
  start_delay?: string
  created_at: string
  pending_since?: string
  pending_reason?: string
  queue_position?: number
  baseline_pinned?: boolean
  baseline_pinned_at?: string
  started_at?: string
  ended_at?: string
  live_metrics?: LiveMetrics
  telemetry_health?: TelemetryRunHealth
  error?: string
  history?: LiveMetrics[]
  comparison_baseline?: RunComparisonBaseline
  start_time?: string
  runtime_settings?: RuntimeSettingsOverride
  scenario_mode?: ScenarioMode
  goal_config?: GoalConfig
  archive?: RunArchiveRecord
  // backwards compat
  script_id?: string
  script_name?: string
}

export interface RunArchiveRecord {
  test_id: string
  schema_version: string
  archive_path: string
  manifest_path: string
  transaction_count: number
  resource_sample_count: number
  interval_summary_count: number
  created_at: string
  completed_at: string
}

export interface RunArchiveManifest {
  schema_version: string
  test_id: string
  test_name: string
  created_at: string
  completed_at?: string
  status?: string
  error?: string
  provenance?: RunArchiveProvenance
  transactions_file: string
  transactions_parquet_file?: string
  resource_samples_file: string
  resource_samples_parquet_file?: string
  interval_summaries_file: string
  interval_summaries_parquet_file?: string
  transaction_count: number
  resource_sample_count: number
  interval_summary_count: number
  sla_breached?: boolean
  sla_details?: string[]
}

export interface TransactionEvent {
  timestamp: string
  test_id: string
  test_name: string
  group?: string
  action_name?: string
  correlation_id?: string
  trace_id?: string
  span_id?: string
  network_profile?: string
  dependency_type?: string
  dependency_target?: string
  duration_ms: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
  bytes_sent?: number
  bytes_received?: number
  status_code?: number
  success: boolean
  error_message?: string
  extractions?: Record<string, string>
  page_url?: string
  page_title?: string
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
  client_metrics?: ClientMetrics
  server_telemetry?: ServerTelemetry
  snapshot?: {
    request_method: string
    request_url: string
    request_headers?: Record<string, string>
    request_body?: string
    response_status: number
    response_headers?: Record<string, string>
    response_body?: string
  }
}

export interface IntervalSummaryRecord {
  timestamp: string
  test_id: string
  test_name: string
  active_vusers: number
  tps: number
  avg_response_ms: number
  error_pct: number
  total_transactions: number
  total_errors: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
}

export interface ClientMetrics {
  device_profile?: string
  device_platform?: string
  device_tier?: string
  browser_engine?: string
  action_duration_ms?: number
  fcp_ms?: number
  lcp_ms?: number
  cls_score?: number
  inp_ms?: number
  dom_content_loaded_ms?: number
  dom_complete_ms?: number
  load_event_ms?: number
  blocking_resource_count?: number
  total_resource_count?: number
  resource_transfer_kb?: number
  js_heap_used_mb?: number
  js_heap_total_mb?: number
  browser_process_cpu_pct?: number
  browser_process_mem_mb?: number
  layout_count?: number
  recalc_style_count?: number
  script_duration_ms?: number
  layout_duration_ms?: number
  task_duration_ms?: number
  long_task_count?: number
}

export interface ServerTelemetry {
  server_processing_ms?: number
  transfer_ms?: number
  serialization_ms?: number
  deserialization_ms?: number
  request_payload_kb?: number
  request_size_class?: string
  response_payload_kb?: number
  response_size_class?: string
  envelope_overhead_kb?: number
  soap_fault_code?: string
  soap_fault_category?: string
  soap_namespace_conflict?: string
  redirect_count?: number
  redirect_ms?: number
  message_latency_ms?: number
  messages_sent?: number
  messages_received?: number
  messages_sent_per_sec?: number
  messages_received_per_sec?: number
  consumer_lag_ms?: number
  connection_reused?: boolean
}

export interface ServerTelemetrySummary {
  samples: number
  avg_server_processing_ms?: number
  avg_transfer_ms?: number
  avg_request_payload_kb?: number
  avg_response_payload_kb?: number
  request_payload_histogram?: Record<string, number>
  response_payload_histogram?: Record<string, number>
  avg_message_latency_ms?: number
  max_message_latency_ms?: number
  messages_sent?: number
  messages_received?: number
  avg_messages_sent_per_sec?: number
  avg_messages_received_per_sec?: number
  avg_consumer_lag_ms?: number
  max_consumer_lag_ms?: number
  request_size_classes?: Record<string, number>
  response_size_classes?: Record<string, number>
  connection_reused_count?: number
  connection_reused_pct?: number
}

export interface ArchivePage<T> {
  count: number
  total: number
  limit: number
  offset: number
  filters?: Record<string, unknown>
  transactions?: T[]
  resources?: T[]
  summaries?: T[]
}

export interface RunArchiveActionStats {
  transactions: number
  errors: number
  error_pct: number
  avg_response_ms: number
  min_response_ms: number
  max_response_ms: number
}

export interface RunArchiveResourcePeaks {
  cpu_process_pct: number
  mem_process_mb: number
  net_sent_kbps: number
  net_recv_kbps: number
  disk_read_kbps: number
  disk_write_kbps: number
}

export interface RunArchiveOverview {
  transactions: number
  errors: number
  error_pct: number
  avg_response_ms: number
  min_response_ms: number
  max_response_ms: number
  resource_samples: number
  interval_summaries: number
  peak_tps: number
  peak_target_tps?: number
  max_throughput_gap_pct?: number
  throughput_missed_intervals?: number
  peak_active_vusers: number
  browser?: BrowserLiveMetrics
  telemetry?: RunArchiveTelemetrySummary
  load_model_goals?: RunArchiveLoadModelGoals
  goal_completion?: RunArchiveGoalCompletion
}

export interface RunArchiveLoadModelGoals {
  configured_global_iterations?: number
  configured_max_transactions?: number
  target_tps?: number
  staged_groups?: number
  stage_count?: number
  once_only_groups?: number
}

export interface RunArchiveGoalCompletion {
  achieved_transactions: number
  configured_max_transactions?: number
  transaction_completion_pct?: number
  configured_global_iterations?: number
  iteration_evidence?: string
  throughput_target_tps?: number
  throughput_missed_intervals?: number
}

export interface RunArchiveTelemetrySummary {
  status: 'healthy' | 'degraded' | 'breached'
  samples: number
  healthy_intervals?: number
  degraded_intervals?: number
  breached_intervals?: number
  sustained_breach_intervals?: number
  dropped_results?: number
  websocket_dropped_messages?: number
  websocket_dropped_events?: number
  max_sample_age_ms?: number
  max_generator_processing_lag_ms?: number
  max_transport_lag_ms?: number
  max_controller_fanout_lag_ms?: number
  max_browser_render_lag_ms?: number
  first_breach_at?: string
  last_breach_at?: string
  condition_counts?: Record<string, number>
}

export interface RunArchiveSOAPFaultTimelineBucket {
  timestamp: string
  total: number
  by_code?: Record<string, number>
  by_category?: Record<string, number>
}

export interface RunArchiveSOAPPayloadTimelineBucket {
  timestamp: string
  samples: number
  avg_request_payload_kb?: number
  avg_response_payload_kb?: number
  avg_envelope_overhead_kb?: number
  max_envelope_overhead_kb?: number
}

export interface RunArchiveSummaryPoint {
  timestamp: string
  active_vusers: number
  tps: number
  error_pct: number
  p95_ms: number
  total_transactions: number
  total_errors: number
}

export interface RunArchiveReport {
  schema_version?: string
  compatibility?: RunArchiveReportCompatibility
  completeness?: RunArchiveCompletenessScore
  change_attribution?: RunArchiveChangeAttribution
  report_profiles?: RunArchiveReportProfile[]
  manifest: RunArchiveManifest
  overview: RunArchiveOverview
  evidence_tiers?: RunArchiveEvidenceTier[]
  error_patterns?: RunArchiveErrorPattern[]
  summary_timeline?: RunArchiveSummaryPoint[]
  by_action: Record<string, RunArchiveActionStats>
  by_soap_operation?: Record<string, RunArchiveActionStats>
  by_network_profile?: Record<string, RunArchiveActionStats>
  by_soap_fault_code?: Record<string, number>
  by_soap_fault_category?: Record<string, number>
  soap_fault_timeline?: RunArchiveSOAPFaultTimelineBucket[]
  soap_payload_timeline?: RunArchiveSOAPPayloadTimelineBucket[]
  browser_by_engine?: Record<string, BrowserEngineLiveMetrics>
  browser_by_platform?: Record<string, BrowserPlatformLiveMetrics>
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  browser_resources?: BrowserResourceTiming[]
  resource_peaks: RunArchiveResourcePeaks
}

export interface RunArchiveReportProfile {
  id: string
  label: string
  description: string
  export_formats: string[]
  included_sections: string[]
}

export interface ReportProfilesResponse {
  generated_at: string
  default_profile: string
  profiles: RunArchiveReportProfile[]
  download_formats: string[]
}

export interface RootCauseSignal {
  name: string
  status: string
  value?: string
  severity?: string
  score?: number
}

export interface RootCauseFinding {
  category: string
  severity: 'critical' | 'warning' | 'healthy' | string
  title: string
  detail: string
  evidence?: string[]
  score: number
}

export interface RunRootCauseSummary {
  schema_version: string
  test_id: string
  generated_at: string
  status: 'healthy' | 'degraded' | 'breached' | 'unknown' | string
  confidence: 'low' | 'medium' | 'high' | string
  summary: string
  signals?: RootCauseSignal[]
  top_findings?: RootCauseFinding[]
  next_actions?: string[]
  evidence?: string[]
}

export interface RunArchiveChangeAttribution {
  script_revisions?: string[]
  scenario_revision?: string
  environment?: string
  generator_versions?: string[]
  data_revision?: string
  runtime_overrides: boolean
  runtime_override_fields?: string[]
}

export interface RunArchiveCompletenessScore {
  score_pct: number
  status: 'complete' | 'needs_review' | 'incomplete' | string
  findings?: RunArchiveCompletenessFinding[]
}

export interface RunArchiveCompletenessFinding {
  dimension: string
  severity: 'info' | 'warning' | 'critical' | string
  penalty: number
  message: string
}

export interface RunArchiveReportCompatibility {
  status: 'compatible' | 'incompatible' | string
  report_schema_version: string
  manifest_schema_version?: string
  supported_manifest_schemas: string[]
  outputs: RunArchiveOutputCompatibility[]
  issues?: RunArchiveCompatibilityIssue[]
}

export interface RunArchiveOutputCompatibility {
  format: string
  artifact?: string
  schema_version?: string
  status: 'compatible' | 'missing' | 'incompatible' | 'unsupported' | string
  reason?: string
}

export interface RunArchiveCompatibilityIssue {
  severity: 'info' | 'warning' | 'error' | string
  code: string
  message: string
}

export interface RunArchiveEvidenceTier {
  tier: string
  label: string
  status: 'present' | 'missing' | string
  count?: number
  description?: string
  reason?: string
}

export interface BrowserResourceTiming {
  timestamp: string
  action_name?: string
  page_url?: string
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  duration_ms?: number
  bytes_received?: number
  device_profile?: string
  device_platform?: string
  browser_engine?: string
}

export interface BrowserEngineLiveMetrics extends BrowserLiveMetrics {
  browser_engine?: string
}

export interface BrowserPlatformLiveMetrics extends BrowserLiveMetrics {
  device_platform?: string
  browser_engine?: string
}

export interface ScenarioDraftGroup {
  name: string
  scriptId: string
  vusers: number
  rampUp: string
  rampDown: string
  stages?: LoadStageDraft[]
  iterations?: number
  initialization: 'at_runtime' | 'at_once'
}

export interface ScenarioDraft {
  name: string
  duration: string
  startDelay: string
  scenarioMode: ScenarioMode
  scriptShakeout: boolean
  resourceBaseline: boolean
  assignment: GeneratorAssignmentSettings
  goalConfig: GoalConfig
  groups: ScenarioDraftGroup[]
  runtimeSettings: ScenarioRuntimeSettingsDraft
}

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

export interface ScenarioSchedule {
  id: string
  scenario_id: string
  project_id?: string
  name: string
  cron_expr: string
  timezone?: string
  release_id?: string
  run_name_template?: string
  enabled: boolean
  revision?: number
  approval_policy?: 'auto' | 'manual_review' | 'high_cost' | 'off_hours' | 'production' | 'quota_burst' | string
  missed_run_policy?: 'skip' | 'catch_up_once' | 'catch_up_all' | 'require_operator_decision' | string
  conflict_summary?: string
  last_occurrence?: ScenarioScheduleOccurrence
  next_required_action?: string
  pending_approval_id?: string
  owner_subject?: string
  owner_display_name?: string
  governance?: ScenarioScheduleGovernance
  blackout_windows?: ScenarioScheduleWindow[]
  maintenance_windows?: ScenarioScheduleWindow[]
  availability_windows?: ScenarioScheduleWindow[]
  last_run_test_id?: string
  last_run_at?: string
  next_run_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleGovernance {
  rbac_review?: {
    decision?: string
    reviewed_at?: string
    reviewed_by?: string
    required_roles?: string[]
    review_source?: string
  }
  quota_review?: {
    decision?: string
    project_max_concurrent_tests?: number
    project_max_vusers?: number
    estimated_peak_vusers?: number
    findings?: string[]
  }
  policy_version?: string
}

export interface ScenarioScheduleWindow {
  name?: string
  days_of_week?: number[]
  start_time: string
  end_time: string
  timezone?: string
}

export interface ScenarioScheduleOccurrence {
  id: string
  schedule_id: string
  scenario_id: string
  project_id?: string
  schedule_revision: number
  planned_at: string
  materialized_at?: string
  status: 'planned' | 'materialized' | 'queued' | 'blocked' | 'skipped' | 'failed' | 'approved' | 'completed' | string
  reason_code?: string
  test_id?: string
  approval_id?: string
  conflict_summary?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleApproval {
  id: string
  schedule_id: string
  scenario_id: string
  project_id?: string
  schedule_revision: number
  policy: string
  status: 'pending' | 'approved' | 'rejected' | string
  requested_by?: string
  requested_at?: string
  resolved_by?: string
  resolved_at?: string
  decision_reason?: string
  required_role?: string
  created_at: string
  updated_at: string
}

export interface ScenarioScheduleHealth {
  total: number
  enabled: number
  due: number
  blocked: number
  failed: number
  pending_approval: number
  queued?: number
  materialized?: number
  skipped?: number
  completed?: number
  status_counts?: Record<string, number>
}

export type TestStatus =
  | 'QUEUED'
  | 'PENDING'
  | 'INITIALIZING'
  | 'RUNNING'
  | 'STOPPING'
  | 'COMPLETE'
  | 'FAILED'
  | 'ABORTED'
  | 'PAUSED'

export interface LiveMetrics {
  timestamp: string
  active_vusers: number
  tps: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  avg_response_ms: number
  error_pct: number
  total_transactions: number
  total_errors: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  transactions?: Record<string, TxMetrics>
  dependency_health?: Record<string, TxMetrics>
  dependency_map?: LiveDependencyMap
  error_patterns?: LiveErrorPattern[]
  soap_operations?: Record<string, TxMetrics>
  network_profiles?: Record<string, TxMetrics>
  groups?: Record<string, GroupLiveMetrics>
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  capacity_status?: LiveCapacityStatus
  goal_status?: GoalStatus
  telemetry_status?: TelemetryRunHealth
  telemetry?: TelemetryHealth
  resources?: SystemResources
  latency?: NetworkLatency
}

export interface BrowserLiveMetrics {
  samples: number
  avg_fcp_ms?: number
  avg_lcp_ms?: number
  avg_cls_score?: number
  avg_inp_ms?: number
  avg_blocking_resources?: number
  avg_long_task_count?: number
  avg_js_heap_used_mb?: number
  avg_browser_process_cpu_pct?: number
  avg_browser_process_mem_mb?: number
}

export interface LiveCapacityStatus {
  allocated_vusers: number
  available_generators: number
  recommended_generators: number
  saturated: boolean
  queued_vusers: number
  rejected_vusers: number
  by_protocol?: Record<string, LiveCapacityBreakdown>
  by_region?: Record<string, LiveCapacityBreakdown>
}

export interface LiveCapacityBreakdown {
  allocated_vusers?: number
  queued_vusers?: number
  rejected_vusers?: number
}

export interface BrowserDeviceLiveMetrics extends BrowserLiveMetrics {
  device_profile?: string
  device_platform?: string
  device_tier?: string
  browser_engine?: string
}

export interface TelemetryHealth {
  dropped_results?: number
  history_points?: number
  websocket_clients?: number
  websocket_dropped_messages?: number
  websocket_dropped_events?: number
  generator_published_at?: string
  controller_received_at?: string
  collected_at?: string
  published_at?: string
  client_received_at?: string
  generator_processing_lag_ms?: number
  transport_lag_ms?: number
  controller_fanout_lag_ms?: number
  browser_render_lag_ms?: number
  delivery_lag_ms?: number
  end_to_end_age_ms?: number
  sample_age_ms?: number
}

export interface TelemetryRunHealth {
  status: 'healthy' | 'degraded' | 'breached'
  severity: 'healthy' | 'warning' | 'danger'
  summary?: string
  current_signature?: string
  conditions?: string[]
  breach_active?: boolean
  sustained_breach?: boolean
  breach_duration_sec?: number
  breach_started_at?: string
  last_observed_at: string
  last_transition_at?: string
}

export interface TelemetryDiagnostic {
  severity: 'healthy' | 'warning' | 'danger'
  summary: string
  conditions?: string[]
  samples?: {
    issue: 'slow' | 'error' | 'broker' | 'dead_letter'
    transaction: string
    dependency_type?: string
    dependency_target?: string
    correlation_ids?: string[]
    count?: number
    errors?: number
    error_pct?: number
    avg_ms?: number
    p99_ms?: number
    avg_message_latency_ms?: number
    avg_consumer_lag_ms?: number
    avg_messages_sent_per_sec?: number
    avg_messages_received_per_sec?: number
  }[]
  telemetry?: TelemetryHealth
}

export interface TelemetryDiagnosticEntry {
  test_id: string
  test_name?: string
  timestamp: string
  diagnostic: TelemetryDiagnostic
}

export interface NetworkLatency {
  targets: LatencyTarget[]
}

export interface LatencyTarget {
  name: string
  endpoint: string
  latency_ms: number
  ip?: string
  hops?: number
  last_success: boolean
  timestamp: string
}

export interface SystemResources {
  cpu_process_pct: number
  cpu_free_pct: number
  mem_process_mb: number
  mem_free_mb: number
  net_sent_kbps: number
  net_recv_kbps: number
  disk_read_kbps: number
  disk_write_kbps: number
  external_metrics?: Record<string, number>
}

export interface TxMetrics {
  name: string
  dependency_type?: string
  dependency_target?: string
  last_error_message?: string
  last_status_code?: number
  last_error_at?: string
  server_telemetry?: ServerTelemetrySummary
  avg_dns_ms?: number
  avg_connect_ms?: number
  avg_tls_ms?: number
  avg_ttfb_ms?: number
  correlation_ids?: string[]
  correlation_count?: number
  count: number
  errors: number
  avg_ms: number
  p50_ms: number
  p90_ms: number
  p95_ms?: number
  p99_ms: number
}

export interface LiveErrorPattern {
  signature: string
  sample: string
  count: number
  first_seen: string
  latest_seen: string
  actions?: string[]
  affected_groups?: string[]
  status_code?: number
  error_pct?: number
  snapshot_url?: string
}

export interface LiveDependencyMap {
  nodes: LiveDependencyNode[]
  edges: LiveDependencyEdge[]
}

export interface LiveDependencyNode {
  id: string
  label: string
  kind: 'source' | 'dependency'
  dependency_type?: string
  status?: 'healthy' | 'warning' | 'danger' | 'unknown'
  transactions?: number
  errors?: number
  error_pct?: number
  p95_ms?: number
}

export interface LiveDependencyEdge {
  source: string
  target: string
  dependency_type?: string
  transactions?: number
  errors?: number
  error_pct?: number
  p95_ms?: number
}

export interface GroupLiveMetrics {
  name: string
  vusers: number
  transactions: number
  errors: number
  tps: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  avg_response_ms: number
  p50_ms: number
  p90_ms: number
  p99_ms: number
}

export interface StoredScript {
  id: string
  name: string
  protocol: string
  version: string
  project_id?: string
  folder_id?: string | null
  created_at: string
  scenario_count?: number
  associated_releases?: ReleaseRef[]
  scenario_run_releases?: ReleaseRef[]
  last_run_at?: string
  authoring_health?: AuthoringHealth
  script?: ScriptDefinition
}

export interface AuthoringTemplate {
  id: string
  name: string
  protocol: string
  use_case: string
  description: string
  tags?: string[]
  script: ScriptDefinition
  recommended_scenario?: {
    duration?: string
    vusers?: number
    ramp_up?: string
    iterations?: number
  }
}

export interface AuthoringSession {
  id: string
  project_id?: string
  source: string
  name: string
  status: string
  draft?: ScriptDefinition
  review_state?: Record<string, unknown>
  next_actions?: string[]
  blocking_health_inputs?: string[]
  approval_ready?: boolean
  created_at: string
  updated_at: string
  last_accessed_at: string
}

export interface AuthoringCorrelationPreviewRequest {
  name: string
  source?: string
  type?: string
  expression: string
  sample_value?: string
  found_action_index?: number
  used_action_indexes?: number[]
  replace_sample_value?: boolean
  apply?: boolean
}

export interface AuthoringCorrelationDiff {
  kind: string
  path: string
  before?: string
  after?: string
}

export interface AuthoringCorrelationPreviewResponse {
  session_id: string
  applied: boolean
  extractor: {
    name: string
    source: string
    type: string
    expression: string
  }
  draft?: ScriptDefinition
  changes: AuthoringCorrelationDiff[]
  authoring_health: AuthoringHealth
}

export interface AuthoringHealth {
  score: number
  status: 'healthy' | 'needs_review' | 'blocked' | string
  inputs: AuthoringHealthInput[]
}

export interface AuthoringHealthInput {
  key: string
  status: 'pass' | 'info' | 'warning' | 'blocker' | string
  count?: number
  summary: string
}

export interface StoredScenario {
  id: string
  name: string
  duration: string
  start_delay?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  runtime_settings?: RuntimeSettingsOverride
  scripts: Array<{
    order: number
    name?: string
    script_id: string
    script_name?: string
    protocol?: string
    vusers: number
    ramp_up?: string
    ramp_down?: string
    stages?: LoadStageDraft[]
    initialization?: 'at_runtime' | 'at_once'
  }>
  schedules?: ScenarioSchedule[]
  data_pools?: ScenarioDataPoolBinding[]
  created_at: string
  updated_at: string
  script_count: number
  result_count?: number
  associated_releases?: ReleaseRef[]
  last_run_at?: string
}

export interface WSMessage {
  type: string
  test_id?: string
  project_id?: string
  script_id?: string
  replay_session_id?: string
  test_name?: string
  status?: string
  timestamp?: string
  event_index?: number
  data?: LiveMetrics
  event?: unknown
  report?: unknown
  session?: unknown
}

export interface BrowserReplayConsoleEntry {
  level?: string
  text?: string
  source?: string
  timestamp?: string
}

export interface BrowserReplayNetworkEntry {
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  duration_ms?: number
  bytes_received?: number
}

export interface BrowserReplayDomSummary {
  title?: string
  ready_state?: string
  visible_text?: string[]
}

export interface BrowserReplaySnapshot {
  request_method?: string
  request_url?: string
  request_headers?: Record<string, string>
  request_body?: string
  response_status?: number
  response_headers?: Record<string, string>
  response_body?: string
  page_url?: string
  page_title?: string
  html?: string
  screenshot_base64?: string
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
}

export interface BrowserReplayEvent {
  phase: string
  action_name: string
  action_type: string
  method?: string
  target: string
  selector?: string
  value?: string
  headers?: Record<string, string>
  body?: string
  status_code?: number
  success: boolean
  error_message?: string
  started_at: string
  completed_at: string
  duration_ms: number
  extractions?: Record<string, string>
  page_url?: string
  page_title?: string
  html?: string
  screenshot_base64?: string
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
  snapshot?: BrowserReplaySnapshot
}

export interface BrowserReplayReport {
  group: string
  protocol: string
  started_at: string
  completed_at: string
  success: boolean
  error?: string
  events: BrowserReplayEvent[]
  final_variables?: Record<string, string>
}

export interface BrowserReplaySession {
  id: string
  script_id?: string
  project_id?: string
  status: 'queued' | 'running' | 'paused' | 'completed' | 'failed' | 'stopped'
  group: string
  protocol: string
  started_at?: string
  completed_at?: string
  current_event_index: number
  current_event?: BrowserReplayEvent
  events?: BrowserReplayEvent[]
  report?: BrowserReplayReport
  error?: string
}

export interface BrowserReplayStartRequest {
  group_name?: string
  replay_session_id?: string
  variables?: Record<string, string>
  start_paused?: boolean
  breakpoints?: number[]
  browser?: BrowserConfig
  ignore_think_time?: boolean
  script?: ScriptDefinition
}

export interface BrowserReplayStartResponse {
  script_id: string
  replay_session_id: string
  session: BrowserReplaySession
  warnings?: string[]
}

export interface BrowserReplaySessionResponse {
  session: BrowserReplaySession
  warnings?: string[]
}

export interface MetricsHistoryPoint {
  time: string
  timestampMs?: number
  vusers: number
  tps: number
  avgMs: number
  errorPct: number
  p50: number
  p90: number
  p99: number
  // System Resources
  cpuProcess?: number
  cpuFree?: number
  memProcess?: number
  memFree?: number
  netSent?: number
  netRecv?: number
  diskRead?: number
  diskWrite?: number
  jmsConsumerLagMs?: number
  jmsPeakConsumerLagMs?: number
  [key: string]: string | number | undefined
}

export interface HealthResponse {
  status: string
  version: string
  ws_clients: number
  test_count: number
  capacity_availability?: {
    source: 'configured' | 'configured_plus_live'
    configured_available_generators: number
    effective_available_generators: number
    live_available_generators?: number
    warnings?: string[]
  }
}

export interface SystemInventoryController {
  id: string
  status: string
  address: string
  version: string
  execution_mode: 'local' | 'distributed'
}

export interface SystemInventoryGenerator {
  target: string
  generator_id: string
  region?: string
  status: 'connected' | 'healthy' | 'ready' | 'unreachable' | string
  version?: string
  supported_protocols?: string[]
  compatibility_state?: string
  compatibility_issues?: string[]
  drain_state?: 'active' | 'cordoned' | 'draining' | string
  capacity_state?: 'available' | 'constrained' | 'saturated' | 'unavailable' | 'unknown' | string
  health_score?: number
  available_capacity: number
  active_vusers: number
  cpu_percent: number
  memory_bytes: number
  goroutine_count: number
  cpu_limit_cores: number
  memory_limit_bytes: number
  max_vusers: number
}

export interface SystemInventoryResponse {
  execution_mode: 'local' | 'distributed'
  controller: SystemInventoryController
  generators: SystemInventoryGenerator[]
}

export interface FleetAlert {
  type: string
  target: string
  region?: string
  severity: 'warning' | 'danger' | string
  summary: string
}

export interface FleetAlertsResponse {
  alerts: FleetAlert[]
  count: number
}

export interface FleetCapacityResponse {
  project_id?: string
  region?: string
  protocol?: string
  total_generators: number
  eligible_generators: number
  active_slots: number
  available_slots: number
  project_active_slots?: number
  project_pending_slots?: number
  reserved_generators?: number
  reserved_slots?: number
  max_slots: number
  compatible_features?: string[]
  rejection_reasons?: string[]
  project_slots?: FleetProjectSlotAccounting[]
}

export interface FleetProjectSlotAccounting {
  project_id: string
  active_slots: number
  pending_slots: number
  running_tests: number
  pending_tests: number
  reserved_generators?: number
  reserved_slots?: number
  fair_share_tier: string
}

export interface ParityStatusArea {
  id: string
  name: string
  status: 'complete' | 'partial' | 'blocked' | string
  summary: string
  evidence?: string[]
  remaining?: string[]
  release_gate: boolean
  blocking_gate?: boolean
}

export interface CompetitiveParityArea {
  id: string
  name: string
  status: 'supported' | 'partial' | 'gap' | 'blocked' | string
  competitors?: string[]
  summary: string
  evidence?: string[]
  remaining?: string[]
  decision?: string
  evidence_level?: string
}

export interface ParityStatusResponse {
  generated_at: string
  overall: 'complete' | 'partial' | 'blocked' | string
  areas: ParityStatusArea[]
  competitive_parity?: CompetitiveParityArea[]
}

export interface APMIntegrationProfile {
  id: string
  name: string
  status: 'supported' | 'configured' | 'available' | string
  capabilities: string[]
  supported_signals: string[]
  correlation_fields: string[]
  required_settings?: string[]
  evidence?: string[]
  setup_actions?: string[]
}

export interface APMCorrelationContract {
  status: string
  propagation: string
  correlation_fields: string[]
  evidence?: string[]
}

export interface APMIntegrationProfilesResponse {
  generated_at: string
  configured_count: number
  profiles: APMIntegrationProfile[]
  generic_correlation: APMCorrelationContract
}

export interface InfrastructureMonitorPack {
  id: string
  name: string
  status: 'supported' | 'integration_contract' | string
  source: string
  resource_type: string
  signals: string[]
  evidence?: string[]
  setup_actions?: string[]
}

export interface InfrastructureMonitorPacksResponse {
  generated_at: string
  packs: InfrastructureMonitorPack[]
  evidence?: string[]
}

export interface ReleaseSLAWaiver {
  id: string
  release_id: string
  project_id?: string
  scope: string
  reason: string
  approved_by?: string
  status: 'active' | 'expired' | string
  expires_at?: string
  created_at: string
  updated_at: string
}

export interface ReleaseSLAGovernanceRun {
  test_id: string
  test_name?: string
  status: string
  completed_at?: string
  sla_breached: boolean
  sla_details?: string[]
  telemetry_status?: string
  completeness?: string
}

export interface ReleaseSLAGovernanceResponse {
  generated_at: string
  release_id: string
  release_name?: string
  application_id?: string
  project_id?: string
  owner: {
    type: string
    id?: string
    display_name: string
  }
  status: 'passed' | 'failed' | 'waived' | 'no_runs' | string
  summary: {
    total_runs: number
    archived_runs: number
    passed_runs: number
    failed_runs: number
    waived_breaches: number
    unwaived_breaches: number
  }
  breach_history?: ReleaseSLAGovernanceRun[]
  waivers?: ReleaseSLAWaiver[]
  audit_trail?: DashboardAuditEvent[]
  next_actions?: string[]
}

export interface ExternalResultImport {
  id: string
  provider: 'k6' | 'jmeter' | 'gatling' | string
  name: string
  project_id?: string
  release_id?: string
  scenario_id?: string
  source?: string
  summary?: Record<string, unknown>
  created_at: string
}

export interface ExternalResultImportsResponse {
  imports: ExternalResultImport[]
  count: number
  limit: number
  offset: number
}

export interface DashboardThresholdSettings {
  max_error_rate_pct?: number
  max_p99_latency_ms?: number
  max_avg_server_processing_ms?: number
  max_avg_transfer_ms?: number
  max_telemetry_sample_age_ms?: number
  max_telemetry_generator_lag_ms?: number
  max_telemetry_transport_lag_ms?: number
  max_telemetry_controller_fanout_ms?: number
  max_telemetry_browser_render_lag_ms?: number
}

export interface DashboardGeneralSettings {
  api_endpoint: string
  max_concurrent_tests: number
  metrics_interval_seconds: number
  usage_snapshot_interval_seconds?: number
}

export interface SystemReadinessCheck {
  name: string
  status: 'ready' | 'degraded' | 'not_ready' | string
  message: string
  details?: Record<string, unknown>
}

export interface SystemReadinessResponse {
  status: 'ready' | 'degraded' | 'not_ready' | string
  generated_at: string
  checks: SystemReadinessCheck[]
}

export interface UsageAccountingBucket {
  key?: string
  run_count: number
  group_count?: number
  total_vusers: number
  duration_seconds: number
  vuser_seconds: number
  archive_bytes?: number
  status_counts?: Record<string, number>
}

export interface UsageAccountingResponse {
  generated_at: string
  project_id?: string
  totals: UsageAccountingBucket
  by_project?: UsageAccountingBucket[]
  by_protocol?: UsageAccountingBucket[]
  by_region?: UsageAccountingBucket[]
  notes?: string[]
}

export interface UsageAccountingSnapshot {
  id: string
  project_id?: string
  generated_at: string
  created_at: string
  usage: UsageAccountingResponse
}

export interface UsageAccountingSnapshotsResponse {
  snapshots: UsageAccountingSnapshot[]
  count: number
  limit: number
  offset: number
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
  policies?: DashboardArchiveRetentionPolicy[]
}

export interface DashboardAuthSettings {
  mode: 'dev' | 'disabled' | 'trusted_headers' | 'oidc'
  provider_name?: string
  login_url?: string
  logout_url?: string
  issuer_url?: string
  client_id?: string
  client_secret_env_var?: string
  redirect_url?: string
  scopes?: string[]
  roles_claim?: string
  project_roles_claim?: string
  session_secret_env_var?: string
  user_header: string
  name_header: string
  email_header: string
  roles_header: string
  project_roles_header: string
}

export interface DashboardNotificationWebhook {
  name?: string
  enabled: boolean
  url: string
  format: 'slack' | 'teams' | 'dynatrace'
  events?: string[]
  api_token?: string
  entity_selector?: string
  timeout_minutes?: number
}

export interface DashboardNotificationsSettings {
  webhooks: DashboardNotificationWebhook[]
}

export interface DashboardAuthenticatedUser {
  subject: string
  display_name?: string
  email?: string
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  scopes?: string[]
}

export interface DashboardAuthStatus {
  enabled: boolean
  mode: DashboardAuthSettings['mode']
  provider_name?: string
  login_url?: string
  logout_url?: string
  logout_csrf_token?: string
  authenticated: boolean
  session_status?: 'active' | 'expired' | 'invalid' | 'missing' | 'unavailable' | string
  session_expires_at?: string
  session_ttl_seconds?: number
  user?: DashboardAuthenticatedUser
}

export interface DashboardAuthDiagnosticsCheck {
  id: string
  label: string
  status: 'pass' | 'warn' | 'fail' | 'info' | string
  summary: string
  detail?: string
}

export interface DashboardAuthDiagnostics {
  enabled: boolean
  mode: DashboardAuthSettings['mode']
  provider_name?: string
  provider_model: string
  ready: boolean
  summary: string
  checks: DashboardAuthDiagnosticsCheck[]
}

export interface DashboardAuthRoleDefinition {
  id: string
  name: string
  scope: string
  description: string
  capabilities?: string[]
}

export interface DashboardAuthRoleCatalog {
  provider_model: string
  roles: DashboardAuthRoleDefinition[]
}

export interface DashboardAuthAccessOperation {
  id: string
  label: string
  global_roles?: string[]
  project_roles?: string[]
}

export interface DashboardAuthAccessResource {
  id: string
  name: string
  description: string
  operations: DashboardAuthAccessOperation[]
}

export interface DashboardAuthAccessMatrix {
  version: string
  resources: DashboardAuthAccessResource[]
}

export interface DashboardAuthEffectiveAccessOperation {
  id: string
  label: string
  allowed: boolean
  grant_source?: string
}

export interface DashboardAuthEffectiveAccessResource {
  id: string
  name: string
  description: string
  operations: DashboardAuthEffectiveAccessOperation[]
}

export interface DashboardAuthEffectiveAccess {
  enabled: boolean
  authenticated: boolean
  project_id?: string
  resources: DashboardAuthEffectiveAccessResource[]
}

export interface DashboardServiceAccount {
  id: string
  name: string
  description?: string
  active: boolean
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  scopes?: string[]
  last_used_at?: string
  expires_at?: string
  key_rotated_at?: string
  rotation_interval_days?: number
  rotation_due_at?: string
  created_at: string
  updated_at: string
}

export interface DashboardServiceAccountListResponse {
  service_accounts: DashboardServiceAccount[]
  count: number
}

export interface DashboardCreateServiceAccountRequest {
  name: string
  description?: string
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  scopes?: string[]
  expires_at?: string
  rotation_interval_days?: number
}

export interface DashboardCreateServiceAccountResponse {
  service_account: DashboardServiceAccount
  api_key: string
}

export interface DashboardUpdateServiceAccountRequest {
  active: boolean
}

export interface DashboardAuditEvent {
  id: string
  action: string
  actor_type: string
  actor_subject: string
  actor_display_name?: string
  project_id?: string
  resource_type: string
  resource_id?: string
  status: string
  details?: Record<string, unknown>
  created_at: string
}

export interface DashboardAuditEventListResponse {
  events: DashboardAuditEvent[]
  count: number
  limit?: number
  offset?: number
}

export interface DashboardAuditEventFilter {
  limit?: number
  offset?: number
  action?: string
  actor_type?: string
  project_id?: string
  resource_type?: string
  resource_id?: string
  status?: string
}

export interface ArchivePruneCandidate {
  test_id: string
  project_id?: string
  release_id?: string
  run_status?: string
  archive_path: string
  policy_name?: string
  artifact_type: string
  compliance_class?: string
  reason?: string
  retention_days: number
  completed_at: string
  expired_at: string
}

export interface ArchiveRetentionResult {
  test_id: string
  archive_path: string
  status: string
  compliance_class?: string
  reason?: string
  error?: string
}

export interface ArchiveRetentionPlanResponse {
  generated_at: string
  dry_run: boolean
  candidates: ArchivePruneCandidate[]
  results?: ArchiveRetentionResult[]
  summary: {
    candidate_count: number
    pruned_count: number
    failed_count: number
  }
}

export interface CapacityResourceEstimate {
  cpu_cores: number
  memory_mb: number
  network_mbps: number
}

export interface CapacityDimensionRecommendations {
  cpu: number
  memory: number
  network: number
  vusers: number
}

export interface CapacityAdvisory {
  total_vusers: number
  duration_seconds: number
  baseline_window_enabled: boolean
  workload_class?: 'http' | 'browser' | 'mixed' | string
  browser_vusers?: number
  max_browser_vusers_per_generator?: number
  protocol_densities?: ProtocolDensityEnvelope[]
  headroom_pct: number
  complexity_score: number
  bottleneck: 'cpu' | 'memory' | 'network' | 'vusers'
  availability_source?: 'configured' | 'configured_plus_live'
  configured_available_generators?: number
  live_available_generators?: number
  recommended_generators: number
  available_generators: number
  shortfall_generators?: number
  requires_override?: boolean
  estimated_per_vuser: CapacityResourceEstimate
  estimated_total: CapacityResourceEstimate
  usable_per_generator: CapacityResourceEstimate
  dimension_recommendations: CapacityDimensionRecommendations
  warnings?: string[]
}

export interface ProtocolDensityEnvelope {
  protocol: string
  vusers: number
  max_vusers_per_generator: number
  cpu_cores_per_vuser: number
  memory_mb_per_vuser: number
  network_mbps_per_vuser: number
  compatibility_confidence?: string
  benchmark_profile?: string
  benchmark_evidence?: string
  calibration_source?: string
  calibrated_at?: string
}

export interface LoadModelTemplateGroup {
  name: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  iterations?: number
  start_delay?: string
}

export interface LoadStageDraft {
  name?: string
  type: 'ramp_up' | 'hold' | 'ramp_down' | string
  duration: string
  target_vusers?: number
  target_tps?: number
}

export interface LoadModelTemplate {
  id: string
  name: string
  shape_id: string
  description: string
  scenario: {
    scenario_mode?: ScenarioMode
    duration: string
    goal_config?: GoalConfig
    groups: LoadModelTemplateGroup[]
  }
  notes?: string[]
}

export interface LoadModelSupportEntry {
  id: string
  name: string
  category: string
  status: 'executed' | 'partial' | 'planned' | string
  stormrunner_shape: string
  fields: string[]
  notes: string
}

export interface LoadModelSupportMatrixResponse {
  support_matrix: LoadModelSupportEntry[]
  status_counts: Record<string, number>
  notes: string[]
}

export interface LoadModelTemplatesResponse {
  templates: LoadModelTemplate[]
  count: number
}

export interface LoadModelValidationItem {
  dimension: string
  group?: string
  status: 'ready' | 'needs_review' | 'blocked' | string
  summary: string
  required_rows?: number
  available_rows?: number
  target_vusers?: number
  target_tps?: number
}

export interface LoadModelValidationReport {
  status: 'ready' | 'needs_review' | 'blocked' | string
  status_counts: Record<string, number>
  items: LoadModelValidationItem[]
  total_vusers: number
  peak_target_tps?: number
  capacity_advisory?: CapacityAdvisory
  assignment_recommendation?: AssignmentRecommendation
}

export interface DraftCapacityEstimate {
  scenario_mode?: ScenarioMode
  total_vusers: number
  duration: string
  resource_baseline?: boolean
  groups: TestGroup[]
  assignment?: GeneratorAssignmentSettings
  footprint?: ScenarioFootprint
  assignment_recommendation?: AssignmentRecommendation
  capacity_advisory?: CapacityAdvisory
}

export interface DashboardSystemSettings {
  general: DashboardGeneralSettings
  thresholds: DashboardThresholdSettings
  capacity_planning: DashboardCapacityPlanningSettings
  archive_retention: DashboardArchiveRetentionSettings
  notifications: DashboardNotificationsSettings
  auth: DashboardAuthSettings
}
