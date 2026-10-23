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
  capture_boundary: 'execution-admission-before-staging'
  classification_algorithm: 'legacy-mixed-diagnostic.v1'
  expected_controller_publication_interval_ms: number
  cadence_scope: 'local-controller-ticker' | 'distributed-controller-coalescing-gate'
  sustained_breach_window_ms: number
  coverage_rule: 'retained-classifications-only;gaps-unknown'
  budgets: Array<{ stage: TelemetryStage; enabled: boolean; limit_ms: number }>
}

export interface TelemetryPolicyEvidence {
  availability: 'available' | 'unavailable'
  reason?: 'immutable-policy-not-retained' | 'immutable-policy-invalid'
  policy?: TelemetryBudgetPolicy | null
}
