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
  mode?: 'auto' | 'specific_hosts'
  selected_targets?: string[]
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
  mode: 'auto' | 'specific_hosts'
  available_targets?: string[]
  recommended_targets?: string[]
  summary?: string
  warnings?: string[]
}

export interface Project {
  id: string
  name: string
  created_at: string
  updated_at: string
}

export interface Application {
  id: string
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

export interface RuntimeSettingsOverride {
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
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
  completed_at?: string
  transactions?: Record<string, TxMetrics>
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
  started_at?: string
  ended_at?: string
  live_metrics?: LiveMetrics
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
  transactions_file: string
  transactions_parquet_file?: string
  resource_samples_file: string
  resource_samples_parquet_file?: string
  interval_summaries_file: string
  interval_summaries_parquet_file?: string
  transaction_count: number
  resource_sample_count: number
  interval_summary_count: number
}

export interface TransactionEvent {
  timestamp: string
  test_id: string
  test_name: string
  group?: string
  action_name?: string
  correlation_id?: string
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
  request_payload_kb?: number
  response_payload_kb?: number
  connection_reused?: boolean
}

export interface ServerTelemetrySummary {
  samples: number
  avg_server_processing_ms?: number
  avg_transfer_ms?: number
  avg_request_payload_kb?: number
  avg_response_payload_kb?: number
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
  peak_active_vusers: number
  browser?: BrowserLiveMetrics
}

export interface RunArchiveReport {
  manifest: RunArchiveManifest
  overview: RunArchiveOverview
  by_action: Record<string, RunArchiveActionStats>
  browser_by_engine?: Record<string, BrowserEngineLiveMetrics>
  browser_by_platform?: Record<string, BrowserPlatformLiveMetrics>
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  resource_peaks: RunArchiveResourcePeaks
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

export type TestStatus =
  | 'QUEUED'
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
  avg_response_ms: number
  error_pct: number
  total_transactions: number
  total_errors: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  transactions?: Record<string, TxMetrics>
  groups?: Record<string, GroupLiveMetrics>
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  goal_status?: GoalStatus
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
  delivery_lag_ms?: number
  end_to_end_age_ms?: number
  sample_age_ms?: number
}

export interface TelemetryDiagnostic {
  severity: 'healthy' | 'warning' | 'danger'
  summary: string
  conditions?: string[]
  samples?: {
    issue: 'slow' | 'error'
    transaction: string
    dependency_type?: string
    dependency_target?: string
    correlation_ids?: string[]
    count?: number
    errors?: number
    error_pct?: number
    avg_ms?: number
    p99_ms?: number
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
}

export interface TxMetrics {
  name: string
  dependency_type?: string
  dependency_target?: string
  server_telemetry?: ServerTelemetrySummary
  correlation_ids?: string[]
  correlation_count?: number
  count: number
  errors: number
  avg_ms: number
  p50_ms: number
  p90_ms: number
  p99_ms: number
}

export interface GroupLiveMetrics {
  name: string
  vusers: number
  transactions: number
  errors: number
  tps: number
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
  script?: ScriptDefinition
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
    initialization?: 'at_runtime' | 'at_once'
  }>
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
  status: 'connected' | 'unreachable'
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

export interface DashboardThresholdSettings {
  max_error_rate_pct?: number
  max_p99_latency_ms?: number
  max_avg_server_processing_ms?: number
  max_avg_transfer_ms?: number
}

export interface DashboardGeneralSettings {
  api_endpoint: string
  max_concurrent_tests: number
  metrics_interval_seconds: number
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
}
