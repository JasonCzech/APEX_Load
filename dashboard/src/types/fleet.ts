// Fleet-management transport contracts. UI-only state belongs with the
// consuming feature rather than in this transport module.

import type { PoolAllocation } from './cluster'

export interface ProvisionerInfo {
  name: string
  can_provision: boolean
  supported_regions?: string[]
  description: string
}

export interface FleetAlert {
  id: string
  type: string
  target: string
  region?: string
  severity: 'warning' | 'danger' | string
  summary: string
  acknowledged?: boolean
  acknowledged_at?: string
  acknowledged_by_subject?: string
  acknowledged_by_display_name?: string
  acknowledgement_note?: string
}

export interface FleetAlertsResponse {
  alerts: FleetAlert[]
  count: number
  unacknowledged_count: number
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

export interface FleetPoolMember {
  id: string
  pool_id: string
  generator_id: string
  target: string
  status: string
  joined_at: string
}

export interface FleetPool {
  id: string
  name: string
  description?: string
  cloud_provider: string
  region?: string
  min_size?: number
  max_size?: number
  desired_size?: number
  status: string
  tags?: Record<string, string>
  /** Registered cluster connection this pool scales in; empty means the
   *  controller's env-configured provisioner. */
  cluster_id?: string
  /** Provider-neutral placement. PaaS pools do not need pre-associated runtime members. */
  execution_target_id?: string
  /** The domain this pool is scoped to. Empty/absent means UNSCOPED: the
   *  domain deployment-target constraint does not apply to it. */
  domain_id?: string
  lifecycle_mode?: 'standing' | 'on_demand' | 'warm_burst'
  warm_floor?: number
  readiness_timeout_seconds?: number
  idle_grace_seconds?: number
  runtime_profile_id?: string
  capacity_admission?: 'strict' | 'best_effort'
  /** Host-resource budget and node placement for this pool's generator pods. */
  allocation?: PoolAllocation
  created_at: string
  updated_at: string
  members?: FleetPoolMember[]
}

export interface FleetPoolListResponse {
  fleet_pools: FleetPool[]
  count: number
  limit?: number
  offset?: number
}

export interface FleetGeneratorMetadata {
  generator_id: string
  target?: string
  fleet_pool_id?: string | null
  region?: string
  provider_id?: string
  provider_status?: string
  cordoned?: boolean
  tags?: Record<string, string>
  created_at: string
  updated_at: string
}

export interface FleetGeneratorMetadataListResponse {
  generators: FleetGeneratorMetadata[]
  count: number
  limit?: number
  offset?: number
}

export interface FleetProvisioningEvent {
  id: string
  request_id: string
  event_type: string
  message?: string
  timestamp: string
}

export interface FleetProvisioningRequest {
  id: string
  fleet_pool_id: string
  project_id?: string
  status: string
  desired_count: number
  execution_target_id?: string
  provider_operation_id?: string
  idempotency_key?: string
  observed_count?: number
  ready_count?: number
  pending_count?: number
  draining_count?: number
  failed_count?: number
  deadline_at?: string
  last_error?: string
  reason?: string
  requested_by_subject?: string
  events?: FleetProvisioningEvent[]
  created_at: string
  updated_at: string
  completed_at?: string
}

export interface FleetProvisioningRequestListResponse {
  provisioning_requests: FleetProvisioningRequest[]
  count: number
  limit?: number
  offset?: number
}

export interface FleetProvisioningInstance {
  id: string
  execution_target_id: string
  fleet_pool_id: string
  test_id?: string
  provisioning_request_id?: string
  generator_id: string
  provider_id?: string
  state: string
  archive_state: string
  cleanup_deadline?: string
  created_at: string
  updated_at: string
}

export interface FleetProvisioningInstanceListResponse {
  instances: FleetProvisioningInstance[]
  total: number
}
