import type { BrowserResourceEvidenceV2, BrowserResourcePhase } from '../types/browserResourceEvidence'

const fields = ['version', 'source_generation', 'request_id', 'frame_id', 'document_id', 'navigation_id', 'redirect_chain_id',
  'redirect_from_request_id', 'redirect_hop', 'initiating_action_id', 'observation_action_id', 'origin', 'identity_unavailable_reason',
  'epoch_start_ms', 'document_offset_ms', 'observed_at', 'timing_source', 'phases', 'bytes', 'service_worker',
  'service_worker_unavailable_reason', 'cache', 'cache_unavailable_reason', 'initiator_unavailable_reason', 'state']
const required = ['version', 'source_generation', 'request_id', 'redirect_hop', 'observed_at', 'timing_source', 'phases', 'bytes', 'state']
const ids = ['frame_id', 'document_id', 'navigation_id', 'redirect_chain_id', 'redirect_from_request_id', 'initiating_action_id', 'observation_action_id']
const byteFields = ['request_headers', 'request_body', 'response_headers', 'response_encoded_body']
const phases = new Set(['dns', 'connect', 'tls', 'request_wait', 'response'])
const reasons = new Set(['', 'not_exposed', 'unsupported', 'no_observation', 'no_interaction', 'hidden_before_paint', 'not_loaded',
  'frame_unavailable', 'document_unavailable', 'identity_capacity', 'source_censored', 'source_incomplete', 'measurement_capacity',
  'malformed', 'cohort_unavailable', 'shaping_unavailable', 'new_user_policy_unavailable', 'scope_unqualified'])
function object(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === 'object' && !Array.isArray(value) }
function owns(value: Record<string, unknown>, key: string): boolean { return Object.prototype.hasOwnProperty.call(value, key) }
function keys(value: Record<string, unknown>, allowed: string[], mandatory: string[] = []): boolean {
  return Object.keys(value).every(key => allowed.includes(key)) && mandatory.every(key => owns(value, key))
}
function text(value: unknown, max: number): value is string {
  return typeof value === 'string' && value.length <= max && new TextEncoder().encode(value).length <= max
    && !value.includes('\0') && !/[\r\n\uD800-\uDFFF]/u.test(value)
}
function number(value: unknown, maximum: number, signed = false): value is number {
  return typeof value === 'number' && Number.isFinite(value) && Math.abs(value) <= maximum && (signed || value >= 0)
}
function optionalNumber(value: unknown, maximum: number, signed = false): boolean { return value === undefined || value === null || number(value, maximum, signed) }
function reason(value: unknown): boolean { return value === undefined || typeof value === 'string' && reasons.has(value) }
function id(value: unknown, generation: string, optional = true): boolean {
  return optional && (value === undefined || value === '') || text(value, 128) && value.startsWith(`${generation}-`) && /^[a-z0-9_./-]+$/.test(value)
}
function utc(value: unknown): boolean {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)
    || /^0001-01-01T00:00:00(?:\.0{1,9})?Z$/.test(value)) return false
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) && new Date(parsed).toISOString().slice(0, 19) === value.slice(0, 19)
}
function origin(value: unknown): boolean {
  if (value === undefined || value === '') return true
  if (!text(value, 512) || !/^https?:\/\/[^/?#\s@]+$/.test(value)) return false
  try {
    const parsed = new URL(value)
    return Boolean(parsed.hostname) && !parsed.username && !parsed.password && !parsed.search && !parsed.hash && parsed.pathname === '/'
  } catch { return false }
}

export interface BrowserResourceEvidenceView { evidence: BrowserResourceEvidenceV2 | null; issue: string | null }
/** Refuse unsupported typed evidence while leaving its legacy resource row
 * available. Admission never substitutes action capture time for request time. */
export function browserResourceEvidenceView(value: unknown): BrowserResourceEvidenceView {
  const invalid = { evidence: null, issue: 'Retained request evidence is malformed or unsupported.' }
  if (value === undefined || value === null) return { evidence: null, issue: 'No typed native request evidence was retained.' }
  try {
    if (!object(value) || !keys(value, fields, required) || value.version !== 2 || typeof value.source_generation !== 'string'
      || !/^[a-f0-9]{32}$/.test(value.source_generation) || !id(value.request_id, value.source_generation, false)
      || !number(value.redirect_hop, 64) || !Number.isSafeInteger(value.redirect_hop) || !utc(value.observed_at)
      || value.timing_source !== 'playwright' || typeof value.state !== 'string' || !['finished', 'failed', 'censored'].includes(value.state) || !origin(value.origin)) return invalid
    for (const key of ids) if (!id(value[key], value.source_generation)) return invalid
    for (const key of ['identity_unavailable_reason', 'service_worker_unavailable_reason', 'cache_unavailable_reason', 'initiator_unavailable_reason']) if (!reason(value[key])) return invalid
    for (const key of ['service_worker', 'cache']) if (value[key] !== undefined && value[key] !== null && typeof value[key] !== 'boolean') return invalid
    if (!optionalNumber(value.epoch_start_ms, 253402300799999) || !optionalNumber(value.document_offset_ms, 86400000, true)) return invalid
    if (value.phases !== null && (!Array.isArray(value.phases) || value.phases.length > 5)) return invalid
    const seen = new Set<string>()
    for (const phase of value.phases ?? []) {
      if (!object(phase) || !keys(phase, ['name', 'start_ms', 'end_ms', 'unavailable_reason'], ['name'])
        || typeof phase.name !== 'string' || !phases.has(phase.name) || seen.has(phase.name)
        || !optionalNumber(phase.start_ms, 86400000) || !optionalNumber(phase.end_ms, 86400000) || !reason(phase.unavailable_reason)) return invalid
      seen.add(phase.name)
      const complete = phase.start_ms !== undefined && phase.start_ms !== null && phase.end_ms !== undefined && phase.end_ms !== null
      if (complete && (phase.end_ms as number) < (phase.start_ms as number) || complete === Boolean(phase.unavailable_reason)) return invalid
    }
    if (!object(value.bytes) || !keys(value.bytes, [...byteFields, 'unavailable_reason']) || !reason(value.bytes.unavailable_reason)) return invalid
    for (const key of byteFields) {
      const count = value.bytes[key]
      if (!optionalNumber(count, 2 ** 50) || count !== undefined && count !== null && !Number.isSafeInteger(count)) return invalid
    }
    return { evidence: structuredClone(value) as unknown as BrowserResourceEvidenceV2, issue: null }
  } catch { return invalid }
}

/** Native request-local offsets may overlap; these rows are never summed into
 * server processing or a cross-host waterfall. Missing endpoints remain gaps. */
export function browserResourcePhaseRows(evidence: BrowserResourceEvidenceV2): Array<BrowserResourcePhase & { start_percent?: number; end_percent?: number }> {
  const captured = evidence.phases ?? []
  const maximum = Math.max(1, ...captured.flatMap(phase => [phase.start_ms ?? 0, phase.end_ms ?? 0]))
  return captured.map(phase => ({ ...phase,
    ...(phase.start_ms !== undefined && phase.start_ms !== null ? { start_percent: phase.start_ms / maximum * 100 } : {}),
    ...(phase.end_ms !== undefined && phase.end_ms !== null ? { end_percent: phase.end_ms / maximum * 100 } : {}),
  }))
}
export function browserResourceRowKey(locator: string, evidence?: BrowserResourceEvidenceV2 | null): string {
  return JSON.stringify([locator, evidence?.source_generation ?? null, evidence?.request_id ?? null])
}
