export type TelemetryStage = 'sample_age' | 'generator_processing' | 'transport' | 'controller_fanout' | 'browser_render'

/** A retained policy receipt; availability does not certify collected metrics.
 * Version 1 is the system threshold settings; version 2 is a project
 * telemetry budget policy revision the run chose, with its definition digest. */
export interface TelemetryBudgetPolicy {
  schema_version: 'apexload.telemetry_policy.v1' | 'apexload.telemetry_policy.v2'
  policy_id: string
  revision: number
  definition_sha256?: string
  hash: string
  captured_at: string
  /** Captured at admission, or for a policy segment the run changed to while it ran (TEL-B). */
  capture_boundary: 'execution-admission-before-staging' | 'in-run-policy-segment'
  classification_algorithm: 'legacy-mixed-diagnostic.v1'
  expected_controller_publication_interval_ms: number
  cadence_scope: 'local-controller-ticker' | 'distributed-controller-coalescing-gate'
  sustained_breach_window_ms: number
  coverage_rule: 'retained-classifications-only;gaps-unknown'
  budgets: Array<{ stage: TelemetryStage; enabled: boolean; limit_ms: number }>
}

export interface TelemetryPolicyEvidence {
  availability: 'available' | 'unavailable'
  /** policy-segments-unavailable: live evidence of a run whose telemetry
   * policy segments cannot be read now, so no receipt is claimed (TEL-B). */
  reason?: 'immutable-policy-not-retained' | 'immutable-policy-invalid' | 'policy-segments-unavailable'
  policy?: TelemetryBudgetPolicy | null
}

/** One segment of a run's telemetry budget policy (TEL-B): from effective_at,
 * the run's live telemetry is classified by policy. Sequence 1 is the
 * admission receipt; each later one is an append-only change made while the
 * run ran. An interval a change fell inside is classified by none. */
export interface TelemetryPolicySegmentReceipt {
  sequence: number
  effective_at: string
  source: 'admission' | 'system_settings' | 'run_selection'
  policy_id?: string
  /** Absent for a stored change whose receipt the controller could not
   * validate (an archive records it without one). */
  policy?: TelemetryBudgetPolicy
  requested_by?: string
}

/** POST /api/v1/tests/{id}/telemetry-policy-segments */
export interface TelemetryPolicySegmentRequest {
  request_id: string
  /** A project telemetry budget policy ID, or project_default for the threshold settings. */
  telemetry_budget_policy_id: string
  /** A local run's publication cadence in whole seconds; omitted keeps it. */
  controller_publication_interval_seconds?: number
  /** The run's latest segment as the requester saw it (1 before any change). */
  expected_sequence: number
}
