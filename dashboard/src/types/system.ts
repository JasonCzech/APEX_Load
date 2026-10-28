// System-health and controller-fleet transport contracts. These are API
// payloads, not component state, and remain re-exported by the compatibility
// barrel in ../types.ts while consumers migrate domain by domain.

export interface VersionResponse {
  version: string
  display_version?: string
  commit?: string
  revision?: string
  build_time?: string
  go_version?: string
  environment: string
  deployment?: DeploymentIdentity
}

export interface DeploymentIdentity {
  kind: 'desktop' | 'kubernetes' | 'docker' | 'server' | string
  label: string
  bind_scope: 'loopback' | 'wide' | string
  auth_enforced: boolean
  desktop_available: boolean
  client_downloads_enabled: boolean
}

export interface HealthResponse {
  status: string
  version: string
  display_version?: string
  commit?: string
  revision?: string
  environment?: string
  build_time?: string
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

export interface SystemInventoryRuntimeCapacity {
  resource_contract?: string
  resources?: import('./runtimeResources').RuntimeResourceCapacity
  protocol: string
  available: boolean
  max_vusers: number
  available_vusers: number
  features?: string[]
  occupancy_mode?: 'idle' | 'persistent' | 'legacy' | string
}

export interface DataPlatformInventory {
  metadata_store: string
  archive_backend: string
  archive_uri_prefix?: string
  cache_backend: string
  cache_key_prefix?: string
  cache_fanout?: string
  metrics_retention: string
}

export interface JavaHostProfileSummary {
  script_sha256: string
  profile_id: string
  configuration_sha256: string
  policy_sha256: string
  memory_bytes: number
  preparation_ms: number
  max_run_ms: number
  invocation_ms: number
  facade_cpu_millicores?: number
  facade_memory_bytes?: number
  facade_network_kbps?: number
  facade_resources_sha256?: string
}

export interface JavaHostCapabilitySummary {
  prepared_population?: boolean
  contract: string
  mode: string
  containment: string
  max_hosts: number
  max_vusers: number
  max_memory_bytes: number
  profiles: JavaHostProfileSummary[]
}

export interface SystemInventoryGenerator {
  java_host?: JavaHostCapabilitySummary
  pod_capacity?: import('./capacity').PodCapacitySnapshot
  target: string
  generator_id: string
  pod_name?: string
  restartable?: boolean
  restart_blocked_reason?: string
  region?: string
  fleet_pool_id?: string
  pool_name?: string
  status: 'connected' | 'healthy' | 'ready' | 'unreachable' | string
  version?: string
  supported_protocols?: string[]
  compatible_features?: string[]
  runtime_capacities?: SystemInventoryRuntimeCapacity[]
  labels?: Record<string, string>
  compatibility_state?: string
  compatibility_issues?: string[]
  drain_state?: 'active' | 'cordoned' | 'draining' | string
  cordoned?: boolean
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
  cpu_user_pct?: number
  cpu_system_pct?: number
  cpu_iowait_pct?: number
  cpu_steal_pct?: number
  thread_count?: number
  open_fd_count?: number
  heap_alloc_mb?: number
  heap_in_use_mb?: number
  gc_count_delta?: number
  gc_pause_ms?: number
  container_cpu_usage_pct?: number
  container_cpu_throttled_periods_delta?: number
  container_cpu_throttled_seconds_rate?: number
  container_memory_working_set_mb?: number
  container_memory_usage_pct?: number
  container_memory_fail_count_delta?: number
  container_oom_kill_count_delta?: number
  net_drop_in_per_sec?: number
  net_drop_out_per_sec?: number
  net_error_in_per_sec?: number
  net_error_out_per_sec?: number
  tcp_retransmits_per_sec?: number
  tcp_established?: number
  tcp_time_wait?: number
  disk_io_time_pct?: number
}

export interface SystemInventoryResponse {
  execution_mode: 'local' | 'distributed'
  controller: SystemInventoryController
  generators: SystemInventoryGenerator[]
  data_platform: DataPlatformInventory
}

export interface SystemRestartCapabilities {
  enabled: boolean
  disabled_reason?: string
  execution_mode: 'local' | 'distributed' | string
  namespace?: string
  controller_deployment?: string
  generator_stateful_set?: string
  allow_force: boolean
  supports: {
    controller_workload: boolean
    generator_workload: boolean
    generator_pod: boolean
  }
}

export interface SystemRestartRequest {
  target_type: 'controller' | 'generator'
  scope: 'pod' | 'workload'
  target: string
  reason: string
  confirmation: string
  force?: boolean
}

export interface SystemRestartResult {
  status: 'success' | 'blocked' | 'failed' | 'disabled' | 'not_found' | string
  message: string
  target_type?: 'controller' | 'generator' | string
  scope?: 'pod' | 'workload' | string
  target?: string
  namespace?: string
  workload?: string
  pod_name?: string
  active_vusers?: number
  active_run_count?: number
  reachable?: boolean
  forced?: boolean
  warnings?: string[]
  disabled_reason?: string
}

export interface SystemServiceEntry {
  id: string
  name: string
  kind: 'backend' | 'frontend' | string
  status: string
  address?: string
  url?: string
  version?: string
  source: string
  metadata?: Record<string, unknown>
}

export interface SystemServicesResponse {
  generated_at: string
  services: SystemServiceEntry[]
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

export interface CloudTestCapability {
  id: string
  category: string
  name: string
  cloudtest_evidence_url: string
  evidence_confidence: 'current_official' | 'current_api' | 'legacy_official' | 'inferred'
  apex_status: 'supported' | 'partial' | 'planned' | 'demand_gated' | 'non_goal'
  workstream: string
  acceptance_test: string
  implementation_status: 'implemented' | 'in_progress' | 'not_started' | 'demand_gated' | 'non_goal'
}

export interface BrowserBenchmarkAcceptance {
  measurement_version?: string
  rss_per_vuser_target_bytes?: number
  launch_p50_target_ms: number
  action_overhead_target_ms: number
  rss_attribution: string
  required_raw_run_count: number
  required_benchmark_command: string
}

export interface BrowserBenchmarkDensityEnvelope {
  recommended_canary_vusers_per_generator: number
  maximum_claimed_vusers_per_generator: number
  pairing_model: string
  claim_rule: string
}

export interface BrowserBenchmarkProfile {
  id: string
  name: string
  browser_engine: string
  device_profile: string
  device_class: string
  platform: string
  status: string
  evidence_level: string
  artifact_path: string
  acceptance: BrowserBenchmarkAcceptance
  density_envelope: BrowserBenchmarkDensityEnvelope
  benchmark_commands: string[]
  evidence: string[]
  release_gate: boolean
  high_density_claim_allowed: boolean
  notes?: string[]
}

export interface BrowserBenchmarkEvidenceResponse {
  generated_at: string
  claim_policy: string
  hardware_profile: {
    id: string
    name: string
    cpu_cores: number
    memory_gb: number
    runtime: string
    description: string
  }
  profiles: BrowserBenchmarkProfile[]
  environment_matrix: Array<{
    id: string
    browser_engine: string
    device_profiles: string[]
    validation_command: string
    ci_expectation: string
    evidence: string[]
  }>
  commands: string[]
  evidence: string[]
}

export interface ParityStatusResponse {
  generated_at: string
  overall: 'complete' | 'partial' | 'blocked' | string
  areas: ParityStatusArea[]
  competitive_parity?: CompetitiveParityArea[]
  cloudtest_capabilities?: CloudTestCapability[]
  browser_benchmarks?: BrowserBenchmarkProfile[]
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
  by_user?: UsageAccountingBucket[]
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
