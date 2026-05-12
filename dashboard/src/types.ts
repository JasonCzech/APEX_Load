export interface TestGroup {
  name: string
  script_id: string
  script_name: string
  protocol: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  initialization?: 'at_runtime' | 'at_once'
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
  log_level?: string
  auto_transactions?: string
  error_handling?: {
    snapshot_on_error?: boolean
  }
}

export type ScenarioMode = 'vuser_driven' | 'goal_oriented'

export interface GoalConfig {
  target_average_tps?: number
  p95_latency_ms?: number
  max_error_pct?: number
  min_vusers?: number
  max_vusers?: number
  stop_on_sla_breach?: boolean
  sla_breach_window?: string
}

export interface GoalStatus {
  mode: 'goal_oriented'
  current_tps: number
  current_total_vusers: number
  target_average_tps?: number
  min_vusers?: number
  max_vusers?: number
  suggested_total_vusers?: number
  last_scale_direction?: 'up' | 'down'
  last_scale_reason?: string
  last_scale_at?: string
  latency_breached?: boolean
  error_breached?: boolean
  breach_active?: boolean
  breach_duration_sec?: number
  stop_triggered?: boolean
  stop_reason?: string
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
  script_shakeout?: boolean
  resource_baseline?: boolean
  total_vusers: number
  duration: string
  created_at: string
  started_at?: string
  ended_at?: string
  live_metrics?: LiveMetrics
  error?: string
  history?: any[]
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
}

export interface RunArchiveReport {
  manifest: RunArchiveManifest
  overview: RunArchiveOverview
  by_action: Record<string, RunArchiveActionStats>
  resource_peaks: RunArchiveResourcePeaks
}

export interface ScenarioDraftGroup {
  name: string
  scriptId: string
  vusers: number
  rampUp: string
  rampDown: string
  initialization: 'at_runtime' | 'at_once'
}

export interface ScenarioDraft {
  name: string
  duration: string
  scenarioMode: ScenarioMode
  scriptShakeout: boolean
  resourceBaseline: boolean
  goalConfig: GoalConfig
  groups: ScenarioDraftGroup[]
  runtimeSettings: {
    iterations?: number
    log_level: string
    auto_transactions: string
    snapshot_on_error: boolean
  }
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
  goal_status?: GoalStatus
  resources?: SystemResources
  latency?: NetworkLatency
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
  script?: any
}

export interface StoredScenario {
  id: string
  name: string
  duration: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
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
  browser?: {
    headless?: boolean
    viewport?: {
      width: number
      height: number
    }
    capture_screenshots?: 'off' | 'on_error' | 'per_action'
    slow_mo_ms?: number
  }
  ignore_think_time?: boolean
  script?: any
}

export interface BrowserReplayStartResponse {
  script_id: string
  replay_session_id: string
  session: BrowserReplaySession
}

export interface BrowserReplaySessionResponse {
  session: BrowserReplaySession
}

export interface MetricsHistoryPoint {
  time: string
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

export interface DashboardThresholdSettings {
  max_error_rate_pct?: number
  max_p99_latency_ms?: number
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
  capacity_advisory?: CapacityAdvisory
}

export interface DashboardSystemSettings {
  general: DashboardGeneralSettings
  thresholds: DashboardThresholdSettings
  capacity_planning: DashboardCapacityPlanningSettings
}
