// Capacity reservation transport contracts. Keep these API-owned shapes
// separate from component state so they can migrate to generated OpenAPI
// aliases without changing feature code.

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
