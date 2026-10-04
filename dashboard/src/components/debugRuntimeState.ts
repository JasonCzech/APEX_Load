import type { ReplayRuntimeSnapshot, ScriptDebugSession, ScriptDebugRuntimeState } from '../types/replay'

export type RuntimeInspectionMode = 'current' | 'retained' | 'selected'
export interface DebugRuntimeAdmission {
  sessionId: string | null
  incarnation: string | null
  revision: string | null
  signature: string | null
  generation: number
  privacyUnavailable: boolean
  terminal: boolean
  runtime: ScriptDebugRuntimeState | null
  reason: string
}
export type DebugRuntimeRequest = Pick<DebugRuntimeAdmission, 'sessionId' | 'generation'>
export const EMPTY_DEBUG_RUNTIME: DebugRuntimeAdmission = { sessionId: null, incarnation: null, revision: null, signature: null, generation: 0, privacyUnavailable: false, terminal: false, runtime: null, reason: 'Start a debug run to inspect current paused state.' }
const MAX_REVISION = '18446744073709551615'
const terminal = new Set(['completed', 'failed', 'stopped'])
const reasons = new Set(['boundary_held', 'not_paused', 'pause_pending', 'snapshot_unavailable', 'context_canceled', 'integrity_fence_lost', 'runtime_evidence_unavailable', 'session_terminal', 'session_retired', 'revision_exhausted'])
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value) && (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null)
const oneOf = (value: unknown, choices: string[]) => typeof value === 'string' && choices.includes(value)
const integer = (value: unknown, min = 0) => typeof value === 'number' && Number.isSafeInteger(value) && value >= min

export function canonicalRuntimeRevision(value: unknown): value is string {
  return typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value) && (value.length < MAX_REVISION.length || value.length === MAX_REVISION.length && value <= MAX_REVISION)
}
export function compareRuntimeRevisions(left: string, right: string): number {
  return left.length === right.length ? left === right ? 0 : left < right ? -1 : 1 : left.length < right.length ? -1 : 1
}
export function beginDebugRuntime(state: DebugRuntimeAdmission, sessionId: string): DebugRuntimeAdmission {
  return { ...EMPTY_DEBUG_RUNTIME, sessionId, generation: state.generation + 1, reason: 'Waiting for authoritative paused state.' }
}
export function invalidateDebugRuntime(state: DebugRuntimeAdmission, reason: string): DebugRuntimeAdmission {
  return { ...state, generation: state.generation + 1, runtime: null, reason }
}
export function runtimeRequestIsCurrent(state: DebugRuntimeAdmission, request: DebugRuntimeRequest): boolean {
  return request.sessionId === state.sessionId && request.generation === state.generation
}
const byteLength = (value: string) => new TextEncoder().encode(value).length
// Match encoding/json's escaped wire size, including its default HTML escapes.
const engineJSONByteLength = (value: unknown) => byteLength(JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, character => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`))
export const runtimeIdentityNameIsKnown = (name: string) => !/\[(?:REDACTED|OMITTED)\]/.test(name)
function boundedText(value: unknown, max = 4096): value is string {
  if (typeof value !== 'string' || value.includes('\0') || value.length > max) return false
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i)
    if (unit >= 0xd800 && unit <= 0xdbff) { const next = value.charCodeAt(++i); if (!(next >= 0xdc00 && next <= 0xdfff)) return false }
    else if (unit >= 0xdc00 && unit <= 0xdfff) return false
  }
  return byteLength(value) <= max
}
const closed = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).every(key => keys.includes(key))
const optionalText = (value: unknown) => value === undefined || boundedText(value)
const paths = (value: unknown) => value === undefined || Array.isArray(value) && value.length <= 64 && value.every(segment => boundedText(segment, 128) && /^(?:(?:requests|then|else|loop|default)\[[0-9]+\]|case\[[0-9]+\]\[[0-9]+\]|branches\[[0-9]+\]\.steps\[[0-9]+\])$/.test(segment))
const iterations = (value: unknown) => value === undefined || Array.isArray(value) && value.length <= 64 && value.every(v => integer(v, 1))
function validValue(item: Record<string, unknown>, observed: boolean) {
  return item.value_state === 'omitted' ? item.value === undefined : oneOf(item.value_state, observed ? ['assigned', 'observed', 'redacted'] : ['assigned', 'redacted']) && boundedText(item.value)
}
export function runtimeSnapshotIsValid(value: unknown): value is ReplayRuntimeSnapshot {
  if (!record(value) || !closed(value, ['state', 'boundary', 'location', 'variables', 'parameters', 'rows', 'frames', 'omissions', 'runtime_evidence_unavailable', 'unavailable_reason']) || !oneOf(value.state, ['complete', 'bounded']) || !oneOf(value.boundary, ['before_action', 'after_action']) || value.runtime_evidence_unavailable !== undefined && value.runtime_evidence_unavailable !== false || value.unavailable_reason !== undefined && value.unavailable_reason !== '') return false
  const location = value.location
  if (!record(location) || !closed(location, ['event_id', 'phase', 'action_index', 'event_index', 'action_name', 'action_type', 'method', 'target', 'selector', 'source_step_id', 'parent_action_name', 'request_index', 'request_count', 'step_path', 'iteration', 'iteration_path', 'leaf_ordinal']) || !oneOf(location.phase, ['init', 'action', 'teardown']) || !integer(location.action_index) || !integer(location.event_index) || !boundedText(location.action_name) || !boundedText(location.action_type)) return false
  if (!['event_id', 'method', 'target', 'selector', 'source_step_id', 'parent_action_name'].every(key => optionalText(location[key])) || !paths(location.step_path) || !iterations(location.iteration_path) || location.iteration !== undefined && !integer(location.iteration, 1) || location.request_count !== undefined && !integer(location.request_count) || location.leaf_ordinal !== undefined && !integer(location.leaf_ordinal)) return false
  if (location.request_index !== undefined && (!integer(location.request_index) || !integer(location.request_count, 1) || (location.request_index as number) >= (location.request_count as number))) return false
  const omissions = value.omissions
  if (!record(omissions) || !closed(omissions, ['variables', 'parameters', 'rows', 'frames', 'values']) || !['variables', 'parameters', 'rows', 'frames', 'values'].every(key => integer(omissions[key])) || value.state === 'complete' && Object.values(omissions).some(count => count !== 0)) return false
  const arrays = ['variables', 'parameters', 'rows', 'frames']
  if (!arrays.every(key => Array.isArray(value[key])) || arrays.reduce((count, key) => count + (value[key] as unknown[]).length, 0) > 256 || (value.rows as unknown[]).length > 64 || (value.frames as unknown[]).length > 64) return false
  const array = (key: string, check: (item: Record<string, unknown>) => boolean) => (value[key] as unknown[]).every(item => record(item) && check(item))
  const valid = array('variables', item => closed(item, ['name', 'value_state', 'value']) && boundedText(item.name) && validValue(item, false))
    && array('parameters', item => {
      if (!closed(item, ['name', 'state', 'update_policy', 'value_state', 'value', 'observed_event_id']) || !boundedText(item.name) || !optionalText(item.observed_event_id) || !oneOf(item.state, ['not_evaluated', 'prepared', 'observed', 'exhausted', 'unavailable']) || !oneOf(item.update_policy, ['once', 'each_iteration', 'each_occurrence', 'unavailable']) || !validValue(item, true)) return false
      if (oneOf(item.state, ['not_evaluated', 'exhausted', 'unavailable'])) return item.value === undefined && item.value_state === 'omitted' && !item.observed_event_id
      if (item.state === 'prepared') return !item.observed_event_id && item.value_state !== 'observed'
      return item.value_state !== 'assigned'
    })
    && array('rows', item => closed(item, ['kind', 'source', 'name', 'row_ordinal', 'selection', 'state', 'observed_event_id']) && oneOf(item.kind, ['datasource', 'parameter', 'foreach']) && boundedText(item.source) && boundedText(item.name) && optionalText(item.observed_event_id) && oneOf(item.selection, ['sequential', 'random', 'unique', 'pop', 'same_line', 'foreach', 'fixed', 'unavailable']) && oneOf(item.state, ['selected', 'exhausted', 'unavailable']) && (item.state === 'selected' ? integer(item.row_ordinal, 1) : item.row_ordinal === undefined))
    && array('frames', item => closed(item, ['kind', 'phase', 'action_index', 'step_path', 'iteration_path', 'branch_index']) && oneOf(item.kind, ['phase', 'http_sequence', 'control_if', 'control_switch', 'control_loop', 'foreach', 'parallel', 'parallel_branch']) && oneOf(item.phase, ['init', 'action', 'teardown']) && (item.kind === 'phase' ? item.action_index === -1 : integer(item.action_index)) && paths(item.step_path) && iterations(item.iteration_path) && (item.kind === 'parallel_branch' ? integer(item.branch_index) : item.branch_index === undefined))
  return valid && engineJSONByteLength(value) <= 64 * 1024
}

function fullState(session: ScriptDebugSession): ScriptDebugRuntimeState | null {
  const value = session.runtime_state
  if (!record(value) || !closed(value, ['version', 'kind', 'session_id', 'session_incarnation', 'state_revision', 'state', 'reason', 'snapshot']) || value.version !== 1 || value.kind !== 'full' || value.session_id !== session.id || value.session_incarnation !== session.session_incarnation || value.state_revision !== session.state_revision || !reasons.has(value.reason as string)) return null
  if (value.state === 'unavailable') return value.snapshot === null && value.reason !== 'boundary_held' ? value as unknown as ScriptDebugRuntimeState : null
  return value.state === 'paused' && value.reason === 'boundary_held' && session.status === 'paused' && runtimeSnapshotIsValid(value.snapshot) && value.snapshot.state !== 'unavailable' ? value as unknown as ScriptDebugRuntimeState : null
}
// Object-key order does not create a false equal-revision conflict.
function stableSignature(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableSignature).join(',')}]`
  if (record(value)) return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stableSignature(value[key])}`).join(',')}}`
  return JSON.stringify(value) ?? 'undefined'
}
export interface RuntimeAdmissionResult { state: DebugRuntimeAdmission; accepted: boolean }
export function admitDebugRuntimeSession(state: DebugRuntimeAdmission, session: ScriptDebugSession, source: 'start' | 'get' | 'mutation' | 'socket', request?: DebugRuntimeRequest): RuntimeAdmissionResult {
  // Foreign signals cannot invalidate the current run or latch its privacy flag.
  if (session.id !== state.sessionId || state.incarnation && session.session_incarnation !== undefined && session.session_incarnation !== state.incarnation) return { state, accepted: false }
  if (session.runtime_state?.session_id !== undefined && session.runtime_state.session_id !== state.sessionId || state.incarnation && session.runtime_state?.session_incarnation !== undefined && session.runtime_state.session_incarnation !== state.incarnation || session.report?.replay_session_id !== undefined && session.report.replay_session_id !== state.sessionId) return { state, accepted: false }
  if (!state.incarnation && source === 'socket') return { state, accepted: false }
  const identityValid = typeof session.session_incarnation === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(session.session_incarnation)
  const privacySignal = session.runtime_evidence_unavailable === true || session.report?.runtime_evidence_unavailable === true || session.runtime_state?.snapshot?.runtime_evidence_unavailable === true
  // A stale trusted request cannot publish detail, but a verified same-run
  // privacy failure still suppresses every retained/current consumer.
  const boundPrivacy = state.incarnation !== null && state.incarnation === session.session_incarnation && privacySignal
  let current = boundPrivacy ? { ...state, privacyUnavailable: true, runtime: null } : state
  if (source !== 'socket' && (!request || !runtimeRequestIsCurrent(state, request))) return { state: current, accepted: false }
  const canBind = source === 'start' || source === 'get'
  if (!state.incarnation && canBind && identityValid && privacySignal) current = { ...current, privacyUnavailable: true, runtime: null }
  const reject = (reason: string): RuntimeAdmissionResult => ({ state: invalidateDebugRuntime(current, reason), accepted: false })
  if (!identityValid || !canonicalRuntimeRevision(session.state_revision) || !oneOf(session.status, ['queued', 'running', 'paused', 'completed', 'failed', 'stopped'])) return reject('Current state unavailable: session identity or revision could not be verified.')
  if (!current.incarnation && !canBind) return reject('Waiting for a trusted session response.')
  if (current.revision && compareRuntimeRevisions(session.state_revision, current.revision) < 0) return { state: current, accepted: false }
  const runtime = fullState(session)
  if (!runtime) return reject('Current state unavailable: the complete runtime state was not supplied.')
  const signature = stableSignature({ status: session.status, runtime })
  const equal = current.revision === session.state_revision
  // A delayed identical full socket must not undo a leaf or operator invalidation.
  if (equal && source === 'socket' && current.runtime === null) return { state: current, accepted: false }
  if (equal && current.signature !== signature && !(source === 'get' && current.runtime === null)) return reject('Current state unavailable: conflicting state at the same revision. Resynchronizing.')
  if (current.terminal && !terminal.has(session.status)) return { state: current, accepted: false }
  const isTerminal = current.terminal || terminal.has(session.status)
  const admitted = { ...current, generation: current.generation + (current.revision !== session.state_revision || current.signature !== signature ? 1 : 0), incarnation: session.session_incarnation!, revision: session.state_revision, signature, terminal: isTerminal,
    runtime: !current.privacyUnavailable && !isTerminal && runtime.state === 'paused' ? structuredClone(runtime) : null,
    reason: current.privacyUnavailable ? 'Runtime variable evidence unavailable.' : isTerminal ? 'Current state unavailable: the session has ended.' : runtime.state === 'paused' ? '' : `Current state unavailable: ${runtime.reason.replace(/_/g, ' ')}.` }
  return { state: admitted, accepted: true }
}
export function admitDebugRuntimeDelta(state: DebugRuntimeAdmission, delta: { session_id?: string; session_incarnation?: string; state_revision?: string; status?: string; runtime_evidence_unavailable?: boolean }): RuntimeAdmissionResult {
  if (delta.session_id !== state.sessionId || state.incarnation && delta.session_incarnation !== undefined && delta.session_incarnation !== state.incarnation) return { state, accepted: false }
  if (!state.incarnation) return { state, accepted: false }
  const privacy = state.incarnation !== null && delta.session_incarnation === state.incarnation && delta.runtime_evidence_unavailable
  const current = privacy ? { ...state, privacyUnavailable: true, runtime: null } : state
  const reject = (reason: string): RuntimeAdmissionResult => ({ state: invalidateDebugRuntime(current, reason), accepted: false })
  if (!current.incarnation || delta.session_incarnation !== current.incarnation || !canonicalRuntimeRevision(delta.state_revision) || delta.status !== undefined && !oneOf(delta.status, ['queued', 'running', 'paused', 'completed', 'failed', 'stopped'])) return reject('Current state unavailable: waiting for a verified complete session response.')
  if (current.revision && compareRuntimeRevisions(delta.state_revision, current.revision) < 0) return { state: current, accepted: false }
  const next = invalidateDebugRuntime(current, 'Current state unavailable: resynchronizing after an execution update.')
  return { state: { ...next, revision: delta.state_revision, terminal: current.terminal || terminal.has(delta.status || '') }, accepted: true }
}
export function currentRuntimeLookup(snapshot: ReplayRuntimeSnapshot | null): Record<string, string> | null {
  if (!snapshot || snapshot.runtime_evidence_unavailable) return null
  const candidates = new Map<string, Array<string | undefined>>()
  const add = (name: string, value: string | undefined) => candidates.set(name, [...(candidates.get(name) || []), value])
  snapshot.variables.forEach(item => { if (!runtimeIdentityNameIsKnown(item.name)) return; const value = item.value_state === 'assigned' ? item.value : undefined; add(item.name, value); add(`var.${item.name}`, value); add(`data.${item.name}`, value) })
  snapshot.parameters.forEach(item => { if (runtimeIdentityNameIsKnown(item.name)) add(`param.${item.name}`, ['assigned', 'observed'].includes(item.value_state) && ['prepared', 'observed'].includes(item.state) ? item.value : undefined) })
  const result: Record<string, string> = Object.create(null)
  candidates.forEach((values, name) => { if (values.length === 1 && values[0] !== undefined) result[name] = values[0] })
  return result
}

export function runtimeValueLabel(item: { value_state: string; value?: string }): string {
  return item.value_state === 'redacted' ? 'Redacted' : item.value_state === 'omitted' || item.value === undefined ? 'Unavailable' : item.value === '' ? '(empty)' : item.value
}

export function currentRuntimeParameter(snapshot: ReplayRuntimeSnapshot | null | undefined, name: string | null): ReplayRuntimeSnapshot['parameters'][number] | undefined {
  if (!snapshot || !name || !runtimeIdentityNameIsKnown(name)) return undefined
  const matches = snapshot.parameters.filter(parameter => parameter.name === name)
  return matches.length === 1 ? matches[0] : undefined
}
