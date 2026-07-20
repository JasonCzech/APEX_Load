// Fleet-management transport contracts. UI-only state belongs with the
// consuming feature rather than in this transport module.

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
