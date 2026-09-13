import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { captureScript, compareRuns, eventIdentity, executionLabel, readProfiles, safeProfile, variablesAt } from './debuggerExperience'
import { DebugRunHistory, DebugVariableWatches, DebugTimeline } from './DebuggerSessionTools'
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
  it('reconstructs only values known at the selected execution', () => {
    const events = [event({ extractions: { customer: 'first' } }), event({ extractions: { customer: 'second', order: '123' } })]
    expect(variablesAt(events, { input: 'seed' }, 0)).toEqual({ input: 'seed', customer: 'first' })
    expect(variablesAt(events, { input: 'seed' }, -1)).toEqual({ input: 'seed' })
    const html = renderToStaticMarkup(<DebugVariableWatches events={events} seed={{}} latest={{ customer: 'second' }} selected={0} onSelect={() => {}} />)
    expect(html).toContain('Latest session'); expect(html).toContain('Selected execution (after)'); expect(html).toContain('Pin customer')
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
  })
  it('renders protocol-appropriate history and navigable timeline controls', () => {
    const replay = report([event({ status_code: 200 })])
    const html = renderToStaticMarkup(<DebugRunHistory runs={[{ id: 'run1', report: replay, options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'localhost', draft: true, failurePolicy: 'Stop on error' }]} current={replay} currentRecord={{ id: 'current', options: DEFAULT_REPLAY_OPTIONS, breakpoints: [], target: 'current-target.test', draft: true, failurePolicy: 'Continue' }} />)
    expect(html).toContain('current-target.test'); expect(html).toContain('Current configuration'); expect(html).toContain('Historical execution'); expect(html).not.toContain('Browser Debugger'); expect(html).not.toContain('No screenshot')
    expect(renderToStaticMarkup(<DebugTimeline events={replay.events} onSelect={() => {}} />)).toContain('Passed · Get customer')
  })
})
