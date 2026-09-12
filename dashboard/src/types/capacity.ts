// Capacity reservation transport contracts. Keep these API-owned shapes
// separate from component state so they can migrate to generated OpenAPI
// aliases without changing feature code.

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

export interface PodCapacityVector {
  cpu_cores: number
  memory_mb: number
  network_mbps: number
  vusers: number
  protocols?: Record<string, number>
}

export interface PodCapacitySnapshot {
  state: string
  model_version: string
  incarnation: string
  headroom_pct: number
  schedulable: PodCapacityVector
  reserved: PodCapacityVector
  remaining: PodCapacityVector
  reserved_pct: number
  remaining_pct: number
  bottleneck?: string
  observed_at_unix_ms: number
  confidence: string
  dedicated: boolean
}

export interface PodCapacityPreview {
  target: string
  before: PodCapacitySnapshot
  proposed: PodCapacityVector
  after: PodCapacitySnapshot
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

export interface CapacityReservation {
  project_id: string
  reserved_generators: number
  // Empty scopes the reservation to the global capacity envelope.
  pool_id?: string
  // RFC3339 window; omit both values for an always-active reservation.
  starts_at?: string
  ends_at?: string
}

export interface CapacityTimeslotRequest {
  generators?: number
  vusers?: number
  protocol?: string
  load_zone?: string
  // Empty means the reservation uses the global capacity envelope.
  pool_id?: string
}

export interface CapacityTimeslot {
  id: string
  project_id: string
  scenario_id?: string
  start_at: string
  end_at: string
  timezone?: string
  requested_capacity?: CapacityTimeslotRequest
  approval_state: string
  approval_id?: string
  bound_test_id?: string
  bound_test_status?: string
  conflict_summary?: string
  created_at: string
  updated_at: string
}

export interface CapacityTimeslotListResponse {
  timeslots: CapacityTimeslot[]
  count: number
  limit?: number
  offset?: number
}

export interface CapacityTimeslotAvailabilityResponse {
  available: boolean
  conflicts: CapacityTimeslot[]
  count: number
  summary?: string
}
