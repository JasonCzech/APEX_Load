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
    const html = renderToStaticMarkup(<DebugVariableWatches mode="retained" events={events} seed={{}} latest={{ customer: 'second' }} selected={0} onSelect={() => {}} />)
    expect(html).toContain('Retained history'); expect(html).toContain('Selected execution (after)'); expect(html).toContain('Pin customer')
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
    expect(available).toContain('Reveal captured value')
    expect(available).toContain('retained-event-value')
    expect(available).not.toContain('captured-parameter')
  })
  it('compares repeated/nested executions without overwriting iterations', () => {
    const a = event({ step_path: ['loop[0]'], iteration: 0 }); const b = event({ step_path: ['loop[0]'], iteration: 1 })
    expect(eventIdentity(a)).not.toBe(eventIdentity(b))
    const diff = compareRuns(report([a, a, b]), report([a, { ...a, success: false, error_message: 'mismatch' }, b]))
    expect(diff).toHaveLength(3); expect(diff[0].fields).toEqual([]); expect(diff[1].fields).toContain('Outcome')
    expect(compareRuns(report([a]), report([]))[0].fields).toContain('Not executed in current run')
    expect(compareRuns(report([a]), report([{ ...a, snapshot: { request_body: 'changed', response_headers: { ETag: 'new' } } }]))[0].fields).toEqual(['Resolved request', 'Response'])
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

  it('renders protocol-appropriate history and navigable timeline controls', () => {
    const replay = report([event({ status_code: 200 })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} currentRecord={{ id: 'current', options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'current-target.test', draft: true, failurePolicy: 'Continue' }} />)
    expect(html).toContain('current-target.test'); expect(html).toContain('Current configuration'); expect(html).toContain('Historical execution'); expect(html).not.toContain('Browser Debugger'); expect(html).not.toContain('No screenshot')
    expect(renderToStaticMarkup(<DebugTimeline events={replay.events} onSelect={() => {}} />)).toContain('Passed · Get customer')
  })
})
