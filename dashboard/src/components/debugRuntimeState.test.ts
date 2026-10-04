import { describe, expect, it } from 'vitest'
import type { ReplayRuntimeSnapshot, ScriptDebugSession } from '../types/replay'
import { EMPTY_DEBUG_RUNTIME, admitDebugRuntimeDelta, admitDebugRuntimeSession, beginDebugRuntime, canonicalRuntimeRevision, compareRuntimeRevisions, currentRuntimeLookup, currentRuntimeParameter, invalidateDebugRuntime, runtimeCommandIsAcknowledged, runtimeSnapshotIsValid, type DebugRuntimeAdmission } from './debugRuntimeState'
const incarnation = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
const otherIncarnation = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'
export function runtimeSnapshot(overrides: Partial<ReplayRuntimeSnapshot> = {}): ReplayRuntimeSnapshot {
  return { state: 'complete', boundary: 'before_action', location: { phase: 'action', action_index: 0, event_index: 0, action_name: 'Read', action_type: 'request' }, variables: [{ name: 'customer', value_state: 'assigned', value: 'current' }], parameters: [], rows: [], frames: [], omissions: { variables: 0, parameters: 0, rows: 0, frames: 0, values: 0 }, ...overrides }
}
export function runtimeSession(revision = '1', snapshot: ReplayRuntimeSnapshot | null = runtimeSnapshot(), overrides: Partial<ScriptDebugSession> = {}): ScriptDebugSession {
  return { id: 'run', session_incarnation: incarnation, state_revision: revision, status: snapshot ? 'paused' : 'running', group: 'debug', protocol: 'http', log_level: 'info', current_event_index: -1, runtime_state: { version: 1, kind: 'full', session_id: 'run', session_incarnation: incarnation, state_revision: revision, state: snapshot ? 'paused' : 'unavailable', reason: snapshot ? 'boundary_held' : 'not_paused', snapshot }, ...overrides }
}
function admit(state: DebugRuntimeAdmission, session: ScriptDebugSession, source: 'start' | 'get' | 'mutation' | 'socket' = 'get') { return admitDebugRuntimeSession(state, session, source, { sessionId: state.sessionId, generation: state.generation }) }
const started = () => admit(beginDebugRuntime(EMPTY_DEBUG_RUNTIME, 'run'), runtimeSession(), 'start').state

describe('debug runtime admission', () => {
  it.each(['0', '1', '9007199254740993', '18446744073709551615'])('accepts canonical uint64 revision %s without Number conversion', value => expect(canonicalRuntimeRevision(value)).toBe(true))
  it.each([0, '00', '-1', '1e4', '1.0', ' 1', '+1', '18446744073709551616', '999999999999999999999999999999999'])('rejects noncanonical or overflowing revision %s', value => expect(canonicalRuntimeRevision(value)).toBe(false))
  it('orders distinct values above JS integer precision exactly', () => { expect(compareRuntimeRevisions('9007199254740993', '9007199254740992')).toBe(1); expect(compareRuntimeRevisions('10', '9')).toBe(1) })
  it('binds only generation-guarded trusted start or GET, never socket/mutation', () => {
    const state = beginDebugRuntime(EMPTY_DEBUG_RUNTIME, 'run')
    for (const source of ['socket', 'mutation'] as const) expect(admit(state, runtimeSession(), source).state.incarnation).toBeNull()
    expect(admit(state, runtimeSession(), 'socket').state).toBe(state)
    expect(admitDebugRuntimeDelta(state, { session_id: 'run', session_incarnation: incarnation, state_revision: '1' }).state).toBe(state)
    expect(admitDebugRuntimeSession(state, runtimeSession(), 'get').accepted).toBe(false)
    expect(admit(state, runtimeSession(), 'get').state.incarnation).toBe(incarnation)
  })
  it('ignores foreign session/incarnation and their privacy flags without invalidating current state', () => {
    const state = started()
    for (const foreign of [runtimeSession('2', null, { id: 'foreign', runtime_evidence_unavailable: true }), runtimeSession('2', null, { session_incarnation: otherIncarnation, runtime_evidence_unavailable: true })]) expect(admit(state, foreign).state).toBe(state)
    expect(admitDebugRuntimeDelta(state, { session_id: 'run', session_incarnation: otherIncarnation, state_revision: '2', runtime_evidence_unavailable: true }).state).toBe(state)
  })
  it('drops stale revisions and preserves high-water across invalidation', () => {
    let state = admit(started(), runtimeSession('9007199254740993')).state
    expect(admit(state, runtimeSession('9007199254740992', null)).state).toBe(state)
    state = invalidateDebugRuntime(state, 'expired')
    expect(state.revision).toBe('9007199254740993')
    expect(admit(state, runtimeSession('9007199254740992')).state.runtime).toBeNull()
  })
  it('invalidates equal conflicts, rejects in-flight older-generation GET, permits fresh equal-revision resync', () => {
    const state = started(); const request = { sessionId: 'run', generation: state.generation }
    const conflicting = runtimeSession('1', runtimeSnapshot({ variables: [] }))
    const invalid = admit(state, conflicting, 'socket').state
    expect(invalid.runtime).toBeNull(); expect(invalid.revision).toBe('1')
    expect(admitDebugRuntimeSession(invalid, runtimeSession(), 'get', request).accepted).toBe(false)
    expect(admit(invalid, conflicting).state.runtime?.snapshot?.variables).toEqual([])
  })
  it('a leaf delta clears authority and bars an older GET before allowing bounded full resync', () => {
    const state = started(); const request = { sessionId: 'run', generation: state.generation }
    const invalid = admitDebugRuntimeDelta(state, { session_id: 'run', session_incarnation: incarnation, state_revision: '2', status: 'paused' }).state
    expect(invalid.runtime).toBeNull()
    expect(admitDebugRuntimeSession(invalid, runtimeSession('2'), 'get', request).accepted).toBe(false)
    expect(admit(invalid, runtimeSession('2')).state.runtime?.snapshot?.variables[0].value).toBe('current')
  })
  it.each(['leaf', 'control'] as const)('does not restore invalidated equal-revision state from a delayed identical full socket: %s', cause => {
    const state = started()
    const invalid = cause === 'leaf' ? admitDebugRuntimeDelta(state, { session_id: 'run', session_incarnation: incarnation, state_revision: '1', status: 'paused' }).state : invalidateDebugRuntime(state, 'Control request pending')
    const delayed = admit(invalid, runtimeSession(), 'socket')
    expect(delayed.accepted).toBe(false); expect(delayed.state).toBe(invalid); expect(delayed.state.runtime).toBeNull()
    expect(admit(invalid, runtimeSession(), 'get').state.runtime?.snapshot?.variables[0].value).toBe('current')
    expect(admit(invalid, runtimeSession(), 'mutation').state.runtime?.snapshot?.variables[0].value).toBe('current')
    expect(admit(state, runtimeSession(), 'socket').accepted).toBe(true)
  })
  it('a full newer unavailable publication invalidates older in-flight response generations', () => {
    const state = started(); const request = { sessionId: 'run', generation: state.generation }
    const running = admit(state, runtimeSession('2', null)).state
    expect(running.runtime).toBeNull()
    expect(admitDebugRuntimeSession(running, runtimeSession('2'), 'get', request).accepted).toBe(false)
  })
  it('missing sequence invalidates current authority; paused status alone cannot restore it', () => {
    const state = started()
    const invalid = admit(state, runtimeSession('2', null, { state_revision: undefined, status: 'paused' })).state
    expect(invalid.runtime).toBeNull(); expect(invalid.revision).toBe('1')
    expect(admit(invalid, runtimeSession('2', null, { status: 'paused' })).state.runtime).toBeNull()
  })
  it('terminal state clears current values and cannot return to paused even at a newer revision', () => {
    const state = admit(started(), runtimeSession('2', null, { status: 'completed' })).state
    expect(state.runtime).toBeNull()
    expect(admit(state, runtimeSession('3')).accepted).toBe(false)
  })
  it('privacy is sticky for the same verified run including stale malformed detail, then resets on new run', () => {
    const state = admit(started(), runtimeSession('0', null, { runtime_evidence_unavailable: true })).state
    expect(state.privacyUnavailable).toBe(true)
    expect(admit(state, runtimeSession('3')).state.runtime).toBeNull()
    expect(beginDebugRuntime(state, 'new').privacyUnavailable).toBe(false)
  })
  it.each(['get', 'mutation'] as const)('latches exact-bound stale %s privacy while rejecting metadata and values', source => {
    const state = started(); const oldRequest = { sessionId: 'run', generation: state.generation }
    const newer = admit(state, runtimeSession('2')).state
    const result = admitDebugRuntimeSession(newer, runtimeSession('0', null, { runtime_evidence_unavailable: true }), source, oldRequest)
    expect(result.accepted).toBe(false); expect(result.state.revision).toBe('2'); expect(result.state.privacyUnavailable).toBe(true); expect(result.state.runtime).toBeNull()
    expect(admitDebugRuntimeSession(newer, runtimeSession('0', null, { session_incarnation: otherIncarnation, runtime_evidence_unavailable: true }), source, oldRequest).state).toBe(newer)
  })

  it('full replacements and detached clones remove all prior variables, parameters, rows and frames', () => {
    const state = started(); const empty = runtimeSnapshot({ variables: [], parameters: [], rows: [], frames: [] })
    const next = admit(state, runtimeSession('2', empty)).state
    empty.variables.push({ name: 'late', value_state: 'assigned', value: 'mutated' })
    expect(next.runtime?.snapshot).toMatchObject({ variables: [], parameters: [], rows: [], frames: [] })
    expect(currentRuntimeLookup(next.runtime?.snapshot || null)).toEqual({})
  })
  it('keeps masked names visible in the full arrays without granting current lookup identity', () => {
    const snapshot = runtimeSnapshot({ variables: [{ name: '[REDACTED]', value_state: 'assigned', value: 'visible receipt' }, { name: 'prefix[OMITTED]suffix', value_state: 'assigned', value: 'bounded receipt' }], parameters: [{ name: '[REDACTED]', state: 'prepared', update_policy: 'once', value_state: 'assigned', value: 'cached receipt' }] })
    expect(currentRuntimeLookup(snapshot)).toEqual({})
    expect(currentRuntimeParameter(snapshot, '[REDACTED]')).toBeUndefined()
    expect(snapshot.variables).toHaveLength(2); expect(snapshot.parameters).toHaveLength(1)
    expect(admit(started(), runtimeSession('2', snapshot)).state.runtime?.snapshot?.variables).toEqual(snapshot.variables)
  })
  it('requires one known parameter identity for the authored parameter inspector', () => {
    const parameter: ReplayRuntimeSnapshot['parameters'][number] = { name: 'counter', state: 'observed', update_policy: 'each_occurrence', value_state: 'observed', value: '42' }
    const snapshot = runtimeSnapshot({ parameters: [parameter] })
    expect(currentRuntimeParameter(snapshot, 'counter')).toBe(parameter)
    expect(currentRuntimeParameter(snapshot, 'other')).toBeUndefined()
    expect(currentRuntimeParameter({ ...snapshot, parameters: [parameter, { ...parameter, value: '43' }] }, 'counter')).toBeUndefined()
  })
  it('keeps special scalar names as own properties and suppresses ambiguous aliases', () => {
    const lookup = currentRuntimeLookup(runtimeSnapshot({ variables: [{ name: '__proto__', value_state: 'assigned', value: '' }, { name: 'constructor', value_state: 'assigned', value: 'own' }, { name: '[redacted]', value_state: 'assigned', value: 'one' }, { name: '[redacted]', value_state: 'assigned', value: 'two' }], parameters: [{ name: 'customer', state: 'observed', update_policy: 'each_occurrence', value_state: 'observed', value: '42' }] }))!
    expect(currentRuntimeLookup(runtimeSnapshot({ variables: [{ name: 'people.person', value_state: 'assigned', value: 'Alice' }] }))?.['data.people.person']).toBe('Alice')
    expect(Object.getPrototypeOf(lookup)).toBeNull(); expect(lookup.__proto__).toBe(''); expect(lookup.constructor).toBe('own'); expect(Object.prototype.hasOwnProperty.call(lookup, '[redacted]')).toBe(false); expect(lookup['param.customer']).toBe('42')
  })
})

describe('closed bounded engine DTO admission', () => {
  it('admits complete and bounded held-boundary receipts with original rows and phase/frame sentinels', () => {
    expect(runtimeSnapshotIsValid(runtimeSnapshot())).toBe(true)
    expect(runtimeSnapshotIsValid(runtimeSnapshot({ location: { ...runtimeSnapshot().location, step_path: ['default[0]'] } }))).toBe(true)
    expect(runtimeSnapshotIsValid(runtimeSnapshot({ state: 'bounded', omissions: { variables: 1, parameters: 0, rows: 0, frames: 0, values: 0 }, rows: [{ kind: 'foreach', source: 'orders', name: 'row', row_ordinal: 7, selection: 'foreach', state: 'selected' }], frames: [{ kind: 'phase', phase: 'action', action_index: -1 }, { kind: 'parallel_branch', phase: 'action', action_index: 0, branch_index: 0, iteration_path: [1] }] }))).toBe(true)
  })
  it.each([
    { location: { phase: 'unknown' } }, { location: { action_index: -1 } }, { location: { iteration_path: [0] } }, { location: { step_path: ['arbitrary'] } },
    { variables: [{ name: 'x', value_state: 'omitted', value: 'contradiction' }] }, { variables: [{ name: 'x', value_state: 'assigned' }] }, { variables: [{ name: 'x', value_state: 'assigned', value: 'x'.repeat(4097) }] },
    { parameters: [{ name: 'x', state: 'not_evaluated', update_policy: 'once', value_state: 'assigned', value: 'unresolved' }] },
    { parameters: [{ name: 'x', state: 'prepared', update_policy: 'once', value_state: 'observed', value: 'invented' }] },
    { rows: [{ kind: 'foreach', source: 'rows', name: 'x', selection: 'foreach', state: 'selected' }] },
    { rows: [{ kind: 'foreach', source: 'rows', name: 'x', selection: 'foreach', state: 'exhausted', row_ordinal: 1 }] },
    { frames: [{ kind: 'phase', phase: 'action', action_index: 0 }] }, { frames: [{ kind: 'foreach', phase: 'action', action_index: -1 }] },
    { frames: [{ kind: 'parallel_branch', phase: 'action', action_index: 0 }] }, { frames: [{ kind: 'foreach', phase: 'action', action_index: 0, branch_index: 0 }] },
    { variables: Array.from({ length: 257 }, () => ({ name: 'x', value_state: 'assigned', value: 'x' })) },
    { rows: Array.from({ length: 65 }, () => ({ kind: 'foreach', source: 'rows', name: 'x', selection: 'foreach', state: 'selected', row_ordinal: 1 })) },
    { unknown: { unbounded: 'field' } }, { variables: [{ name: 'x', value_state: 'assigned', value: 'value', unknown: {} }] },
  ])('rejects malformed or unbounded snapshot %# before signature or clone', patch => {
    const snapshot = { ...runtimeSnapshot(), ...patch, ...('location' in patch ? { location: { ...runtimeSnapshot().location, ...patch.location } } : {}) }
    expect(runtimeSnapshotIsValid(snapshot)).toBe(false)
  })
  it.each(['&', '<', '>', '\u2028', '\u2029'])('counts Go JSON escaping against the public serialized byte bound: %s', character => {
    const snapshot = runtimeSnapshot({ variables: Array.from({ length: 8 }, (_, i) => ({ name: String(i), value_state: 'assigned', value: character.repeat(1024) })) })
    expect(runtimeSnapshotIsValid(snapshot)).toBe(true)
    const overflow = runtimeSnapshot({ variables: Array.from({ length: 8 }, (_, i) => ({ name: String(i), value_state: 'assigned', value: character.repeat(1365) })) })
    expect(JSON.stringify(overflow).length).toBeLessThan(64 * 1024)
    expect(runtimeSnapshotIsValid(overflow)).toBe(false)
  })
  it('rejects absent arrays, NUL/unpaired UTF16, byte overflow and total serialized overflow', () => {
    for (const value of [undefined, null]) expect(runtimeSnapshotIsValid({ ...runtimeSnapshot(), variables: value })).toBe(false)
    for (const value of ['\0', '\ud800', '界'.repeat(2000)]) expect(runtimeSnapshotIsValid(runtimeSnapshot({ variables: [{ name: 'x', value_state: 'assigned', value }] }))).toBe(false)
    expect(runtimeSnapshotIsValid(runtimeSnapshot({ variables: Array.from({ length: 20 }, (_, i) => ({ name: String(i), value_state: 'assigned', value: 'x'.repeat(4096) })) }))).toBe(false)
  })
})


describe('debug command acknowledgement independent of runtime evidence admission', () => {
  it('confirms a same-run delayed command without admitting its stale runtime state', () => {
    const initial = started()
    const command = { sessionId: initial.sessionId, incarnation: initial.incarnation, revision: initial.revision }
    const request = { sessionId: initial.sessionId, generation: initial.generation }
    const newer = admit(initial, runtimeSession('3', runtimeSnapshot({ variables: [] })), 'socket').state
    const delayed = runtimeSession('2')
    expect(admitDebugRuntimeSession(newer, delayed, 'mutation', request).accepted).toBe(false)
    expect(runtimeCommandIsAcknowledged(newer, command, delayed)).toBe(true)
    expect(newer.revision).toBe('3')
    expect(newer.runtime?.snapshot?.variables).toEqual([])
  })

  it('retains privacy suppression while confirming only a bound command receipt', () => {
    const initial = started()
    const command = { sessionId: initial.sessionId, incarnation: initial.incarnation, revision: initial.revision }
    const privateState = admit(initial, runtimeSession('3', null, { runtime_evidence_unavailable: true })).state
    expect(runtimeCommandIsAcknowledged(privateState, command, runtimeSession('2'))).toBe(true)
    expect(privateState.privacyUnavailable).toBe(true)
    expect(privateState.runtime).toBeNull()
  })

  it('rejects foreign, replaced, terminal, pre-command and malformed acknowledgements', () => {
    const initial = started()
    const command = { sessionId: initial.sessionId, incarnation: initial.incarnation, revision: initial.revision }
    const current = admit(initial, runtimeSession('3'), 'socket').state
    for (const response of [runtimeSession('0'), runtimeSession('2', null, { id: 'foreign' }), runtimeSession('2', null, { session_incarnation: otherIncarnation }), runtimeSession('2', null, { status: 'completed' }), runtimeSession('2', null, { state_revision: '02' }), runtimeSession('2', null, { runtime_state: undefined })]) {
      expect(runtimeCommandIsAcknowledged(current, command, response)).toBe(false)
    }
    expect(runtimeCommandIsAcknowledged(beginDebugRuntime(current, 'replacement'), command, runtimeSession('2'))).toBe(false)
    expect(runtimeCommandIsAcknowledged({ ...current, incarnation: otherIncarnation }, command, runtimeSession('2'))).toBe(false)
    expect(runtimeCommandIsAcknowledged({ ...current, terminal: true }, command, runtimeSession('2'))).toBe(false)
    expect(runtimeCommandIsAcknowledged(current, { ...command, incarnation: null }, runtimeSession('2'))).toBe(false)
    for (const reason of ['session_terminal', 'session_retired', 'revision_exhausted'] as const) {
      const response = runtimeSession('2', null)
      expect(runtimeCommandIsAcknowledged(current, command, { ...response, runtime_state: { ...response.runtime_state!, reason } })).toBe(false)
    }
    expect(runtimeCommandIsAcknowledged(current, command, runtimeSession('2', null, { report: { replay_session_id: 'foreign' } as ScriptDebugSession['report'] }))).toBe(false)
  })
})
