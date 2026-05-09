export interface TestGroup {
  name: string
  script_id: string
  script_name: string
  protocol: string
  vusers: number
  ramp_up?: string
}

export interface TestSummary {
  id: string
  name: string
  status: TestStatus
  groups: TestGroup[]
  total_vusers: number
  duration: string
  created_at: string
  started_at?: string
  ended_at?: string
  live_metrics?: LiveMetrics
  error?: string
  history?: any[]
  start_time?: string
  // backwards compat
  script_id?: string
  script_name?: string
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
