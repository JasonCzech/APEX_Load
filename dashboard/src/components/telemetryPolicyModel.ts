import type { TelemetryBudgetPolicy, TelemetryStage } from '../types/telemetryPolicy'

const stages: TelemetryStage[] = ['sample_age', 'generator_processing', 'transport', 'controller_fanout', 'browser_render']
export const telemetryStageLabels: Record<TelemetryStage, string> = { sample_age: 'Sample age', generator_processing: 'Generator processing', transport: 'Transport', controller_fanout: 'Controller fan-out', browser_render: 'Browser render' }
const policyFields = ['schema_version', 'policy_id', 'revision', 'hash', 'captured_at', 'capture_boundary', 'classification_algorithm', 'expected_controller_publication_interval_ms', 'cadence_scope', 'sustained_breach_window_ms', 'coverage_rule', 'budgets']

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}
function keys(value: Record<string, unknown>, allowed: string[], required: string[] = allowed): boolean {
  return Object.keys(value).every(key => allowed.includes(key)) && required.every(key => Object.prototype.hasOwnProperty.call(value, key))
}

export type TelemetryPolicyView = { policy?: TelemetryBudgetPolicy; reason?: string }

/** Bound structural admission before any digest work or display projection. */
function telemetryPolicyCandidate(value: unknown): TelemetryPolicyView {
  if (value === undefined) return { reason: 'No immutable policy receipt was retained for this evidence.' }
  const evidence = object(value)
  const invalid = { reason: 'The retained policy receipt is invalid or unsupported.' }
  if (!evidence || !keys(evidence, ['availability', 'reason', 'policy'], ['availability'])) return invalid
  if (evidence.availability === 'unavailable') {
    if (evidence.policy !== undefined && evidence.policy !== null) return invalid
    if (evidence.reason === 'immutable-policy-invalid') return invalid
    if (evidence.reason !== 'immutable-policy-not-retained') return invalid
    return { reason: 'No immutable policy receipt was retained for this evidence.' }
  }
  if (evidence.availability !== 'available' || evidence.reason !== undefined && evidence.reason !== '') return invalid
  const policy = object(evidence.policy)
  if (!policy || !keys(policy, policyFields)) return invalid
  const interval = policy.expected_controller_publication_interval_ms
  if (policy.schema_version !== 'apexload.telemetry_policy.v1' || policy.policy_id !== 'system-telemetry-budgets' || policy.revision !== 1 ||
    typeof policy.hash !== 'string' || !/^[a-f0-9]{64}$/.test(policy.hash) ||
    typeof policy.captured_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(policy.captured_at) || !Number.isFinite(Date.parse(policy.captured_at)) || /^0001-01-01T00:00:00(?:\.0{1,9})?Z$/.test(policy.captured_at) || new Date(policy.captured_at).toISOString().slice(0, 19) !== policy.captured_at.slice(0, 19) ||
    policy.capture_boundary !== 'execution-admission-before-staging' || policy.classification_algorithm !== 'legacy-mixed-diagnostic.v1' ||
    typeof interval !== 'number' || !Number.isSafeInteger(interval) || interval < 1000 || interval > 9223372036854 || interval % 1000 !== 0 ||
    policy.cadence_scope !== 'local-controller-ticker' && policy.cadence_scope !== 'distributed-controller-coalescing-gate' ||
    policy.cadence_scope === 'distributed-controller-coalescing-gate' && interval !== 1000 ||
    policy.sustained_breach_window_ms !== 15000 || policy.coverage_rule !== 'retained-classifications-only;gaps-unknown' ||
    !Array.isArray(policy.budgets) || policy.budgets.length !== stages.length) return invalid
  for (let i = 0; i < stages.length; i++) {
    const budget = object(policy.budgets[i])
    if (!budget || !keys(budget, ['stage', 'enabled', 'limit_ms']) || budget.stage !== stages[i] || typeof budget.limit_ms !== 'number' ||
      !Number.isFinite(budget.limit_ms) || budget.limit_ms < 0 || typeof budget.enabled !== 'boolean' || budget.enabled !== (budget.limit_ms > 0)) return invalid
  }
  // Own the display projection, so later response mutations cannot rewrite it.
  return { policy: { ...policy, budgets: policy.budgets.map(budget => ({ ...budget })) } as TelemetryBudgetPolicy }
}

// Mirrors the fixed v1 Go struct order. encoding/json's float64 encoder uses
// ES6 shortest round-trip formatting and the same exponent cutoffs. Preserve
// IEEE negative zero explicitly: JSON.stringify(-0) would change the digest.
function telemetryPolicyDigestInput(policy: TelemetryBudgetPolicy): string {
  const budgets = policy.budgets.map(budget => `{"stage":${JSON.stringify(budget.stage)},"enabled":${budget.enabled},"limit_ms":${Object.is(budget.limit_ms, -0) ? '-0' : budget.limit_ms.toString()}}`).join(',')
  return `{"schema_version":${JSON.stringify(policy.schema_version)},"policy_id":${JSON.stringify(policy.policy_id)},"revision":${policy.revision},"hash":"","captured_at":"0001-01-01T00:00:00Z","capture_boundary":${JSON.stringify(policy.capture_boundary)},"classification_algorithm":${JSON.stringify(policy.classification_algorithm)},"expected_controller_publication_interval_ms":${policy.expected_controller_publication_interval_ms},"cadence_scope":${JSON.stringify(policy.cadence_scope)},"sustained_breach_window_ms":${policy.sustained_breach_window_ms},"coverage_rule":${JSON.stringify(policy.coverage_rule)},"budgets":[${budgets}]}`
}

/** Verify semantic identity before exposing an available receipt. This detects
 * digest/content mismatch; a digest alone does not attest the receipt's origin.
 */
export async function telemetryPolicyView(value: unknown): Promise<TelemetryPolicyView> {
  const candidate = telemetryPolicyCandidate(value)
  if (!candidate.policy) return candidate
  if (!globalThis.crypto?.subtle) return { reason: 'Policy digest verification is unavailable in this browser context.' }
  try {
    const digest = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(telemetryPolicyDigestInput(candidate.policy)))
    const hash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
    if (hash !== candidate.policy.hash) return { reason: 'The retained policy receipt is invalid or unsupported.' }
    return candidate
  } catch {
    return { reason: 'Policy digest verification is unavailable in this browser context.' }
  }
}
