import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { captureScript, compareRuns, eventIdentity, executionLabel, readProfiles, safeProfile, variablesAt } from './debuggerExperience'
import { DebugRunHistory, DebugVariableWatches, DebugTimeline } from './DebuggerSessionTools'
import { DebugParameterInspector } from './DebugParameterInspector'
import { RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE } from './runtimeVariableEvidence'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
const event = (extra: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent => ({ phase: 'action', action_index: 0, action_name: 'Get customer', action_type: 'GET', target: '/customer', success: true, duration_ms: 10, started_at: '2026-09-13T00:00:00Z', completed_at: '2026-09-13T00:00:01Z', ...extra })
const report = (events: BrowserReplayEvent[]): BrowserReplayReport => ({ group: 'audit', protocol: 'http', success: events.every(e => e.success), started_at: '2026-09-13T00:00:00Z', completed_at: '2026-09-13T00:00:01Z', events })
describe('debugger operator experience', () => {
  it('captures bounded successful HTTP evidence without changing the authored config', () => {
    const script = { name: 'Capture', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test', results: { message_detail: { mode: 'off' } } }, actions: [] } as ScriptDefinition
    expect(captureScript(script).config.results?.message_detail).toMatchObject({ mode: 'sampled', sample_rate: 1, max_items: 100, max_total_bytes: 4194304 })
    expect(script.config.results?.message_detail?.mode).toBe('off')
    expect(captureScript(script, 'errors').config.results?.message_detail?.mode).toBe('errors')
    expect(captureScript(script, 'script')).toBe(script)
    const kafka = { ...script, protocol: 'kafka' }; expect(captureScript(kafka)).toBe(kafka)
  })
  it('labels paused and stopped state without confusing them with running/success', () => {
    const session = { status: 'paused', current_action: { action_name: 'Get customer' } } as ScriptDebugSession
    expect(executionLabel(true, session, null)).toBe('Paused before: Get customer')
    expect(executionLabel(false, session, { ...report([]), stopped: true })).toBe('Stopped')
    expect(executionLabel(false, null, null)).toBe('Ready')
  })
  it('says a start is only being checked until the controller admits a session', () => {
    expect(executionLabel(true, null, null, true)).toBe('Checking replay readiness…')
    // Without the start pending, an unadmitted run is still starting the debugger.
    expect(executionLabel(true, null, null)).toBe('Starting debugger')
    // Once a session exists it reports where the run is, start pending or not.
    const running = { status: 'running', current_action: { action_name: 'Get customer' } } as ScriptDebugSession
    expect(executionLabel(true, running, null, true)).toBe('Executing: Get customer')
    expect(executionLabel(false, null, null, true)).toBe('Ready')
  })
  it('reconstructs only values known at the selected execution', () => {
    const events = [event({ extractions: { customer: 'first' } }), event({ extractions: { customer: 'second', order: '123' } })]
    expect(variablesAt(events, { input: 'seed' }, 0)).toEqual({ input: 'seed', customer: 'first' })
    expect(variablesAt(events, { input: 'seed' }, -1)).toEqual({ input: 'seed' })
    expect(variablesAt(events, { input: 'seed' }, -2)).toEqual({ input: 'seed' })
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={events} seed={{}} latest={{ customer: 'second' }} selected={0} onSelect={() => {}} />)
    expect(html).toContain('Retained history'); expect(html).toContain('Selected execution (after)'); expect(html).toContain('Pin customer')
  })
  it('falls back to retained history when the selection is cleared', () => {
    const events = [event({ extractions: { customer: 'first' } }), event({ extractions: { customer: 'second' } })]
    const html = renderToStaticMarkup(<DebugVariableWatches mode="selected" events={events} seed={{ customer: 'seed' }} latest={null} selected={null} onSelect={() => {}} />)
    expect(html).toContain('second')
    expect(html).toContain('Reconstructed from session inputs and retained extractions')
  })
  it('shows the last applied streamed-child provenance in a long history for special variable names', () => {
    const many = Array.from({ length: 200 }, (_, i) => event({ action_name: `Step ${i}`, extractions: { regular: `${i}` } }))
    many[20] = event({ action_name: 'Earlier', streamed_children: [{ action_name: 'child', success: true, extractions: JSON.parse('{"__proto__":"first","constructor":"old"}') }] })
    many[180] = event({ action_name: 'Latest', streamed_children: [{ action_name: 'child', success: true, extractions: JSON.parse('{"__proto__":"last","constructor":"new"}'), unapplied_extractions: { ignored: 'no' } }] })
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={many} seed={{}} latest={null} selected={null} onSelect={() => {}} />)
    expect(html).toContain('Pin __proto__')
    expect(html).toContain('Pin constructor')
    expect(html).toContain('Latest · action')
    expect(html).not.toContain('Pin ignored')
  })

  it('attributes a repeated value to the execution that changed it', () => {
    const events = [
      event({ action_name: 'First change', extractions: { token: 'new' } }),
      event({ action_name: 'Repeated value', streamed_children: [{ action_name: 'child', success: true, extractions: { token: 'new' } }] }),
    ]
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={events} seed={{ token: 'old' }} latest={null} selected={null} onSelect={() => {}} />)
    expect(html).toContain('First change · action')
    expect(html).not.toContain('Repeated value · action')
  })

  it('does not attribute final values with no retained origin to session inputs', () => {
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={[]} seed={{ input: 'seed' }} latest={{ input: 'seed', missing: 'final' }} selected={null} onSelect={() => {}} />)
    expect(html).toContain('Session input')
    expect(html).toContain('Unknown / not retained')
    const changed = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={[event({ action_name: 'Old retained event', extractions: { token: 'old' } })]} seed={{}} latest={{ token: 'new' }} selected={null} onSelect={() => {}} />)
    expect(changed).toContain('Unknown / not retained')
    expect(changed).not.toContain('Old retained event · action')
    expect(changed).not.toContain('<small>Changed</small>')
  })
  it('does not infer variable origin or changes from an incomplete history prefix', () => {
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={[event({ action_name: 'Repeated', extractions: { token: 'foo' } })]} seed={{}} latest={{ token: 'foo' }} selected={null} onSelect={() => {}} historyIncomplete completed finalValuesRecorded />)
    expect(html).toContain('Execution evidence may be incomplete')
    expect(html).toContain('Unknown / not retained')
    expect(html).not.toContain('Repeated · action')
    expect(html).not.toContain('<small>Changed</small>')
    const missingValues = renderToStaticMarkup(<DebugVariableWatches mode="selected" events={[event()]} seed={{ token: 'stale-seed' }} latest={null} selected={0} onSelect={() => {}} historyIncomplete />)
    expect(missingValues).toContain('Reconstructed values are unverified and may be stale')
    expect(missingValues).toContain('stale-seed')
    expect(missingValues).toContain('Values as JSON')
    const live = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={[event()]} seed={{ token: 'stale-seed' }} latest={{ token: 'stale-seed' }} selected={null} onSelect={() => {}} historyIncomplete />)
    expect(live).toContain('Reconstructed values are unverified and may be stale')
    expect(live).toContain('stale-seed')
  })

  it('hides retained watch values, namespace rows and JSON when runtime evidence is unavailable', () => {
    const html = renderToStaticMarkup(<DebugVariableWatches evidenceUnavailable events={[event({ extractions: { captured: 'captured-value' } })]} seed={{ seeded: 'seed-value' }} latest={{ final: 'final-value' }} selected={0} onSelect={() => {}} />)
    expect(html).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    for (const value of ['captured-value', 'seed-value', 'final-value', 'Pin captured', 'Values as JSON', 'Variable values and provenance']) expect(html).not.toContain(value)
    expect(html).toContain('role="status"')
  })
  it('hides captured parameter values and their event context when runtime evidence is unavailable', () => {
    const script = { name: 'Parameters', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [], parameters: [{ name: 'account', type: 'counter', update: 'each_iteration', start: 1, step: 1 }] } as ScriptDefinition
    const props = { script, name: 'account', values: { 'param.account': 'captured-parameter' }, event: event({ action_name: 'retained-event-value' }), matchesDraft: true, onEdit: () => {} }
    const unavailable = renderToStaticMarkup(<DebugParameterInspector {...props} evidenceUnavailable />)
    expect(unavailable).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(unavailable).toContain('Edit definition in Author')
    for (const value of ['captured-parameter', 'retained-event-value', 'Reveal captured value', '<output>']) expect(unavailable).not.toContain(value)
    const available = renderToStaticMarkup(<DebugParameterInspector {...props} />)
    expect(available).toContain('retained-event-value')
    expect(available).toContain('captured-parameter')
  })
  it('compares repeated/nested executions without overwriting iterations', () => {
    const a = event({ step_path: ['loop[0]'], iteration: 0 }); const b = event({ step_path: ['loop[0]'], iteration: 1 })
    expect(eventIdentity(a)).not.toBe(eventIdentity(b))
    const diff = compareRuns(report([a, a, b]), report([a, { ...a, success: false, error_message: 'mismatch' }, b]))
    expect(diff).toHaveLength(3); expect(diff[0].fields).toEqual([]); expect(diff[1].fields).toContain('Outcome')
    expect(compareRuns(report([a]), report([]))[0].fields).toContain('Not executed in current run')
    expect(compareRuns(report([a]), report([{ ...a, snapshot: { request_body: 'changed', response_headers: { ETag: 'new' } } }]))[0].fields).toEqual(['Resolved request', 'Response'])
  })
  it('compares the captured reconnect handshake instead of the send payload', () => {
    const reconnect = event({ method: 'ws_send', body: 'first send', headers: { Authorization: 'first' }, snapshot: { request_method: 'ws_reconnect', request_url: 'wss://example.test/a' } })
    expect(compareRuns(report([reconnect]), report([{ ...reconnect, body: 'second send', headers: { Authorization: 'second' } }]))[0].fields).not.toContain('Resolved request')
    expect(compareRuns(report([reconnect]), report([{ ...reconnect, snapshot: { ...reconnect.snapshot, request_url: 'wss://example.test/b' } }]))[0].fields).toContain('Resolved request')
    expect(compareRuns(report([reconnect]), report([{ ...reconnect, snapshot: { ...reconnect.snapshot, request_method: 'ws_send' } }]))[0].fields).toContain('Resolved request')
  })
  it('compares final extracted values independently of child extraction order', () => {
    const child = (extractions: Record<string, string>) => ({ action_name: 'child', success: true, extractions })
    const first = event({ streamed_children: [child({ a: '1' }), child({ b: '2' })] })
    const reordered = event({ streamed_children: [child({ b: '2' }), child({ a: '1' })] })
    expect(compareRuns(report([first]), report([reordered]))[0].fields).not.toContain('Variables')
    expect(compareRuns(report([first]), report([event({ streamed_children: [child({ a: 'changed' }), child({ b: '2' })] })]))[0].fields).toContain('Variables')
  })
  it('compares request and response headers independently of insertion order', () => {
    const first = event({ snapshot: { request_headers: { A: '1', B: '2' }, response_headers: { C: '3', D: '4' } } })
    const reordered = event({ snapshot: { request_headers: { B: '2', A: '1' }, response_headers: { D: '4', C: '3' } } })
    expect(compareRuns(report([first]), report([reordered]))[0].fields).toEqual([])
    expect(compareRuns(report([first]), report([event({ snapshot: {} })]))[0].fields).toEqual(['Resolved request', 'Response'])
  })
  it('never persists seed variables or unrecognized fields in profiles', () => {
    const profile = safeProfile('Default', { ...DEFAULT_REPLAY_OPTIONS, variablesText: '{"password":"private"}', groupName: 'private target' })
    expect(JSON.stringify(profile)).not.toContain('private')
    expect(readProfiles(JSON.stringify([{ ...profile, options: { ...profile.options, variablesText: 'private' } }]))).toEqual([profile])
    for (const input of ['{', '{}', '[null]', '[{"name":"bad","options":{}}]']) expect(readProfiles(input)).toEqual([])
    // A profile keeps the failure choice but never a run identity; older ones default it.
    const stopping = safeProfile('Stop', { ...DEFAULT_REPLAY_OPTIONS, afterFailure: 'stop', runId: 'ticket-42' })
    expect(stopping.options).toMatchObject({ afterFailure: 'stop' })
    expect(stopping.options).not.toHaveProperty('runId')
    const legacy: Record<string, unknown> = { ...profile.options }
    delete legacy.afterFailure
    expect(readProfiles(JSON.stringify([{ name: 'Legacy', options: legacy }]))[0].options.afterFailure).toBe('test-run')
  })
  it('hides unavailable history detail and comparison bodies even when a caller retains an older raw report', () => {
    const detail = 'synthetic-history-runtime-detail'
    const retained = { ...report([event({ body: detail, snapshot: { response_body: detail }, extractions: { ordinary: detail }, protocol_evidence: { http_version_policy: '2', http_version_negotiated: '2', http_version_source: 'wire' } })]),
      runtime_evidence_unavailable: true, dropped_event_count: 4, truncated_event_evidence_count: 2, final_variables: { ordinary: detail }, runtime_profile: { name: detail } }
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: retained, options: DEFAULT_REPLAY_OPTIONS,
      breakpoints: [], target: 'authored-target.test', draft: true, failurePolicy: 'Stop on error' }]} current={retained} />)
    expect(html).toContain(RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE)
    expect(html).toContain('authored-target.test')
    expect(html).toContain('4 executions omitted')
    expect(html).toContain('2 execution details truncated')
    expect(html).not.toContain(detail)
    expect(html).not.toContain('Historical execution')
    expect(html).not.toContain('HTTP version evidence')
  })

  it('shows qualified negotiated HTTP evidence in retained run history', () => {
    const replay = report([event({ protocol_evidence: { http_version_policy: 'auto', http_version_negotiated: '1.1', http_version_source: 'wire' } })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} />)
    expect(html).toContain('HTTP version evidence')
    expect(html).toContain('Replay policy</dt><dd>Auto-negotiate')
    expect(html).toContain('Actual negotiated HTTP version</dt><dd>HTTP/1.1')
  })

  it('shows retained diagnostic extractions and omission receipts for HTTP and browser history', () => {
    const diagnostic = event({ protocol_evidence_omitted_count: 2, unapplied_extractions: { rejected: 'diagnostic-value' } })
    for (const protocol of ['http', 'browser']) {
      const replay = { ...report([diagnostic]), protocol }
      const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} />)
      expect(html).toContain('2 protocol evidence items were omitted or shortened')
      expect(html).toContain('Unapplied extractions')
      expect(html).toContain('diagnostic-value')
    }
  })

  it('explains omitted request headers in retained HTTP history', () => {
    const replay = report([event({ body: 'send-payload-not-handshake', headers: { 'X-Send': 'not-handshake' }, snapshot: { request_method: 'ws_reconnect', request_url: 'wss://example.test/socket', request_headers_omitted: true, request_wire_headers_unavailable: true } })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} />)
    expect(html).toContain('Some request headers were omitted or shortened by retention limits.')
    expect(html).toContain('Actual wire headers could not be verified')
    expect(html).toContain('ws_reconnect wss://example.test/socket')
    expect(html).not.toContain('send-payload-not-handshake')
    expect(html).not.toContain('not-handshake')
  })

  it('retains historical live-table operation results', () => {
    const replay = report([event({ action_type: 'live_table', extractions: { operation_result: 'retained-result' } })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} />)
    expect(html).toContain('operation_result')
    expect(html).toContain('retained-result')
  })

  it('renders protocol-appropriate history and navigable timeline controls', () => {
    const replay = report([event({ status_code: 200 })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} currentRecord={{ id: 'current', options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'current-target.test', draft: true, failurePolicy: 'Continue' }} />)
    expect(html).toContain('current-target.test'); expect(html).toContain('Current configuration'); expect(html).toContain('Historical execution'); expect(html).not.toContain('Browser Debugger'); expect(html).not.toContain('No screenshot')
    expect(renderToStaticMarkup(<DebugTimeline events={replay.events} onSelect={() => {}} />)).toContain('Passed · Get customer')
  })
})

it('keeps bounded HAR controls in profiles and defaults legacy profiles safely', () => {
  const rich = safeProfile('HAR', { ...DEFAULT_REPLAY_OPTIONS, harBodies: 'both', harRedirects: false, harTimings: false, harMaxBodyBytes: 262144, harMaxTotalBodyBytes: 16777216 })
  expect(readProfiles(JSON.stringify([rich]))[0].options).toMatchObject({ harBodies: 'both', harRedirects: false, harTimings: false, harMaxBodyBytes: 262144, harMaxTotalBodyBytes: 16777216 })
  expect(safeProfile('Legacy', { ...DEFAULT_REPLAY_OPTIONS, harBodies: undefined, harMaxBodyBytes: -1 }).options).toMatchObject({ harBodies: 'none', harMaxBodyBytes: 65536 })
})
