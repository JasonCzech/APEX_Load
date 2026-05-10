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
  groups: TestGroup[]
  script_shakeout?: boolean
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
  // backwards compat
  script_id?: string
  script_name?: string
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
  created_at: string
}

export interface WSMessage {
  type: string
  test_id: string
  test_name?: string
  status?: string
  timestamp: string
  data?: LiveMetrics
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
}
