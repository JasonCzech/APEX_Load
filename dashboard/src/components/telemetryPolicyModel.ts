import type { TelemetryBudgetPolicy, TelemetryPolicySegmentReceipt, TelemetryStage } from '../types/telemetryPolicy'

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

const systemPolicyID = 'system-telemetry-budgets'
// Go's unicode.IsSpace set, which strings.TrimSpace removes. Unlike JavaScript's
// trim(), it keeps U+FEFF and removes U+0085.
const goSpace = '\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000'
const goPadded = new RegExp(`^[${goSpace}]|[${goSpace}]$`)

/** Version 1 names the system settings; version 2 a chosen project policy
 * revision and its definition digest, as the controller's validator does. */
function telemetryPolicyIdentityValid(policy: Record<string, unknown>): boolean {
  if (policy.schema_version === 'apexload.telemetry_policy.v1') {
    return policy.policy_id === systemPolicyID && policy.revision === 1 && policy.definition_sha256 === undefined
  }
  if (policy.schema_version !== 'apexload.telemetry_policy.v2') return false
  const id = policy.policy_id
  return typeof id === 'string' && id !== '' && id !== systemPolicyID && !goPadded.test(id) && new TextEncoder().encode(id).length <= 128 &&
    !/[\p{Cc}\p{Cs}]/u.test(id) && typeof policy.revision === 'number' && Number.isSafeInteger(policy.revision) && policy.revision >= 1 &&
    typeof policy.definition_sha256 === 'string' && /^[a-f0-9]{64}$/.test(policy.definition_sha256)
}

/** Whether a verified receipt is a project budget policy the run chose. */
export function telemetryPolicyChosen(policy: TelemetryBudgetPolicy): boolean {
  return policy.schema_version === 'apexload.telemetry_policy.v2'
}

type TelemetryCaptureBoundary = TelemetryBudgetPolicy['capture_boundary']
/** A receipt captured at admission: an archive's admission evidence. */
export const TELEMETRY_ADMISSION_BOUNDARY: readonly TelemetryCaptureBoundary[] = ['execution-admission-before-staging']
/** Admission or an in-run policy segment (TEL-B): live evidence and segment lists. */
export const TELEMETRY_ANY_BOUNDARY: readonly TelemetryCaptureBoundary[] = ['execution-admission-before-staging', 'in-run-policy-segment']

/** Bound structural admission before any digest work or display projection. */
function telemetryPolicyCandidate(value: unknown, boundaries: readonly TelemetryCaptureBoundary[]): TelemetryPolicyView {
  if (value === undefined) return { reason: 'No immutable policy receipt was retained for this evidence.' }
  const evidence = object(value)
  const invalid = { reason: 'The retained policy receipt is invalid or unsupported.' }
  if (!evidence || !keys(evidence, ['availability', 'reason', 'policy'], ['availability'])) return invalid
  if (evidence.availability === 'unavailable') {
    if (evidence.policy !== undefined && evidence.policy !== null) return invalid
    if (evidence.reason === 'immutable-policy-invalid') return invalid
    // Live evidence only: an admission view has no segments to read.
    if (evidence.reason === 'policy-segments-unavailable' && boundaries.includes('in-run-policy-segment')) return { reason: "The run's telemetry policy segments cannot be read now, so no receipt is shown for its live telemetry." }
    if (evidence.reason !== 'immutable-policy-not-retained') return invalid
    return { reason: 'No immutable policy receipt was retained for this evidence.' }
  }
  if (evidence.availability !== 'available' || evidence.reason !== undefined && evidence.reason !== '') return invalid
  const policy = object(evidence.policy)
  if (!policy || !keys(policy, [...policyFields, 'definition_sha256'], policyFields) || !telemetryPolicyIdentityValid(policy)) return invalid
  const interval = policy.expected_controller_publication_interval_ms
  if (typeof policy.hash !== 'string' || !/^[a-f0-9]{64}$/.test(policy.hash) ||
    typeof policy.captured_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(policy.captured_at) || !Number.isFinite(Date.parse(policy.captured_at)) || /^0001-01-01T00:00:00(?:\.0{1,9})?Z$/.test(policy.captured_at) || new Date(policy.captured_at).toISOString().slice(0, 19) !== policy.captured_at.slice(0, 19) ||
    !boundaries.includes(policy.capture_boundary as TelemetryCaptureBoundary) || policy.classification_algorithm !== 'legacy-mixed-diagnostic.v1' ||
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

// Go's encoding/json escapes <, >, &, U+2028 and U+2029, which JSON.stringify
// leaves as they are.
function goJSONString(value: string): string {
  return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, c => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)
}

// Mirrors the Go struct order (definition_sha256 is omitted when empty).
// encoding/json's float64 encoder uses ES6 shortest round-trip formatting and
// the same exponent cutoffs. Preserve IEEE negative zero explicitly:
// JSON.stringify(-0) would change the digest.
function telemetryPolicyDigestInput(policy: TelemetryBudgetPolicy): string {
  const budgets = policy.budgets.map(budget => `{"stage":${JSON.stringify(budget.stage)},"enabled":${budget.enabled},"limit_ms":${Object.is(budget.limit_ms, -0) ? '-0' : budget.limit_ms.toString()}}`).join(',')
  const definition = policy.definition_sha256 === undefined ? '' : `"definition_sha256":${JSON.stringify(policy.definition_sha256)},`
  return `{"schema_version":${JSON.stringify(policy.schema_version)},"policy_id":${goJSONString(policy.policy_id)},"revision":${policy.revision},${definition}"hash":"","captured_at":"0001-01-01T00:00:00Z","capture_boundary":${JSON.stringify(policy.capture_boundary)},"classification_algorithm":${JSON.stringify(policy.classification_algorithm)},"expected_controller_publication_interval_ms":${policy.expected_controller_publication_interval_ms},"cadence_scope":${JSON.stringify(policy.cadence_scope)},"sustained_breach_window_ms":${policy.sustained_breach_window_ms},"coverage_rule":${JSON.stringify(policy.coverage_rule)},"budgets":[${budgets}]}`
}

/** Verify semantic identity before exposing an available receipt. This detects
 * digest/content mismatch; a digest alone does not attest the receipt's origin.
 */
export async function telemetryPolicyView(value: unknown, boundaries: readonly TelemetryCaptureBoundary[] = TELEMETRY_ADMISSION_BOUNDARY): Promise<TelemetryPolicyView> {
  const candidate = telemetryPolicyCandidate(value, boundaries)
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

export type TelemetryPolicySegmentsView = { segments?: TelemetryPolicySegmentReceipt[]; reason?: string }

const segmentSources = ['admission', 'system_settings', 'run_selection']
const segmentFields = ['sequence', 'effective_at', 'source', 'policy_id', 'policy', 'requested_by']

/** Verify a run's telemetry policy segments (TEL-B): the admission receipt
 * first (sequence 1, captured at admission), then each change in sequence
 * order with an in-run boundary, every receipt verified as a single one is.
 * A change the controller recorded without a receipt (its stored receipt did
 * not validate) is kept without one. Anything else is refused whole rather
 * than shown in part. */
export async function telemetryPolicySegmentsView(value: unknown): Promise<TelemetryPolicySegmentsView> {
  const invalid = { reason: "The run's retained telemetry policy segments are invalid or unsupported." }
  if (!Array.isArray(value) || value.length === 0) return invalid
  const segments: TelemetryPolicySegmentReceipt[] = []
  for (let index = 0; index < value.length; index++) {
    const segment = object(value[index])
    if (!segment || !keys(segment, segmentFields, ['sequence', 'effective_at', 'source', 'policy'])) return invalid
    const admission = index === 0
    if (segment.sequence !== index + 1 || typeof segment.effective_at !== 'string' || !Number.isFinite(Date.parse(segment.effective_at)) ||
      typeof segment.source !== 'string' || !segmentSources.includes(segment.source) || (segment.source === 'admission') !== admission ||
      segment.policy_id !== undefined && typeof segment.policy_id !== 'string' || segment.requested_by !== undefined && typeof segment.requested_by !== 'string') return invalid
    if (!admission && segment.policy === null) {
      segments.push({ sequence: segment.sequence as number, effective_at: segment.effective_at, source: segment.source as TelemetryPolicySegmentReceipt['source'],
        ...(segment.policy_id !== undefined ? { policy_id: segment.policy_id as string } : {}),
        ...(segment.requested_by !== undefined ? { requested_by: segment.requested_by as string } : {}) })
      continue
    }
    const view = await telemetryPolicyView({ availability: 'available', policy: segment.policy }, TELEMETRY_ANY_BOUNDARY)
    if (!view.policy) return view.reason?.includes('unavailable in this browser') ? view : invalid
    if (view.policy.capture_boundary !== (admission ? 'execution-admission-before-staging' : 'in-run-policy-segment')) return invalid
    // The source and policy ID agree with the receipt, so the list and the
    // report name a segment alike.
    if (segment.policy_id !== undefined && segment.policy_id !== view.policy.policy_id) return invalid
    if (!admission && (segment.source === 'system_settings') !== (view.policy.policy_id === systemPolicyID)) return invalid
    segments.push({ sequence: segment.sequence as number, effective_at: segment.effective_at, source: segment.source as TelemetryPolicySegmentReceipt['source'],
      ...(segment.policy_id !== undefined ? { policy_id: segment.policy_id as string } : {}),
      ...(segment.requested_by !== undefined ? { requested_by: segment.requested_by as string } : {}), policy: view.policy })
  }
  return { segments }
}

/** A segment's policy as readers name it. */
export function telemetryPolicySegmentLabel(segment: TelemetryPolicySegmentReceipt): string {
  switch (segment.source) {
    case 'admission':
      return 'Admission receipt'
    case 'system_settings':
      return segment.policy ? 'Threshold settings' : 'Threshold settings (receipt invalid)'
    default:
      return segment.policy ? `Policy ${segment.policy.policy_id} revision ${segment.policy.revision}` : `Policy ${segment.policy_id ?? 'unknown'} (receipt invalid)`
  }
}
