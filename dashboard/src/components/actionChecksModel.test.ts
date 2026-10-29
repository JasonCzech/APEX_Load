import { describe, expect, it } from 'vitest'

import type { BrowserReplayEvent } from '../types/replay'
import { bodyIsWhole, checkKindOf, checkKinds, checkResult, lastStepEvent, replayEventStepKey, replayEventStepPath, replayResolver, savedValueResult, statusExpectation, type ChecksReplay } from './actionChecksModel'
import { boundaryExpression } from './extractorMatch'

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_name: 'Login', action_type: 'POST', target: '/login', success: true, started_at: '', completed_at: '', duration_ms: 240,
    status_code: 200,
    snapshot: { response_status: 200, response_body: '{"token":"abc123","user":"Ada"}', response_headers: { 'Content-Type': 'application/json', 'Set-Cookie': 'sid=s-1; Path=/' } },
    ...overrides,
  } as BrowserReplayEvent
}

const replay = (overrides: Partial<BrowserReplayEvent> = {}, variables: Record<string, string> = {}, stale = false): ChecksReplay => ({ event: event(overrides), stale, resolve: replayResolver(variables) })

describe('checks by intent', () => {
  it('offers HTTP checks as questions, with the uncommon ones under More checks', () => {
    const kinds = checkKinds('http')
    expect(kinds.filter(kind => !kind.more).map(kind => kind.question)).toEqual(['Status is …', 'Response contains …', 'Response does not contain …', 'Header contains …', 'Finishes within … ms'])
    expect(kinds.some(kind => kind.type === 'aura_actions_success' && kind.more)).toBe(true)
    expect(kinds.find(kind => kind.type === 'text_contains')!.fields.map(field => field.label)).toEqual(['Text', 'Look in', 'Ignore case'])
  })

  it('offers browser and database checks in their own words', () => {
    expect(checkKinds('browser').filter(kind => !kind.more).map(kind => kind.question)).toEqual(['Element is visible', 'Element text is …', 'Page title is …', 'URL contains …'])
    expect(checkKinds('database').filter(kind => !kind.more).map(kind => kind.question)).toEqual(['Returns … rows', 'Changes … rows', 'First value equals …'])
    expect(checkKinds('jms-solace')[0].type).toBe('message_received')
  })

  it('keeps Aura and ignore case to HTTP itself', () => {
    for (const protocol of ['soap', 'kafka']) {
      const kinds = checkKinds(protocol)
      expect(kinds.some(kind => kind.type === 'aura_actions_success')).toBe(false)
      expect(kinds.some(kind => kind.fields.some(field => field.key === 'case_insensitive'))).toBe(false)
    }
    expect(checkKinds('http/2').some(kind => kind.type === 'aura_actions_success')).toBe(true)
  })

  it('starts a status check and a time limit from the last replay', () => {
    const status = checkKinds('http').find(kind => kind.type === 'status_code')!
    expect(status.create(event({ status_code: 302 }))).toEqual({ type: 'status_code', expected: 302 })
    expect(status.create()).toEqual({ type: 'status_code', expected: 200 })
    const within = checkKinds('http').find(kind => kind.type === 'response_time_ms')!
    expect(within.create(event({ duration_ms: 240 }))).toEqual({ type: 'response_time_ms', max: 500 })
    expect(within.create()).toEqual({ type: 'response_time_ms', max: 5000 })
  })

  it('gives a check type the protocol does not offer generic fields', () => {
    const kind = checkKindOf('http', { type: 'legacy_check', value: 'x', expected: 3 })
    expect(kind.question).toBe('Check: legacy_check')
    expect(kind.fields.map(field => [field.key, field.input])).toEqual([['value', 'text'], ['expected', 'number']])
  })

  it('stores a status as a number, or a reference as written', () => {
    expect(statusExpectation('201')).toBe(201)
    expect(statusExpectation(' 404 ')).toBe(404)
    expect(statusExpectation('{{var.status}}')).toBe('{{var.status}}')
    expect(statusExpectation('')).toBe('')
  })
})

describe('results against the last replay', () => {
  it('says a step that has not replayed is not checked', () => {
    expect(checkResult({ type: 'status_code', expected: 200 }, null)).toEqual({ state: 'unchecked', detail: 'Not replayed yet' })
  })

  it('checks a status, including one from a reference', () => {
    expect(checkResult({ type: 'status_code', expected: 200 }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'status_code', expected: 201 }, replay())).toEqual({ state: 'failed', detail: 'Failed: got 200' })
    expect(checkResult({ type: 'status_code_not', expected: 500 }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'status_code', expected: '{{var.ok}}' }, replay({}, { ok: '200' })).state).toBe('passed')
    expect(checkResult({ type: 'status_code', expected: '{{var.missing}}' }, replay()).state).toBe('unchecked')
    expect(checkResult({ type: 'status_code', expected: '' }, replay())).toEqual({ state: 'unchecked', detail: 'Enter the status' })
    expect(checkResult({ type: 'status_code' }, replay())).toEqual({ state: 'unchecked', detail: 'Enter the status' })
  })

  it('claims nothing is missing from a body the replay kept only part of, or redacted', () => {
    const partial = (state: 'partial' | 'redacted') => ({ snapshot: { response_status: 200, response_body: '{"token":"abc', response_body_state: state, response_headers: { 'Content-Type': 'application/json' } } })
    for (const state of ['partial', 'redacted'] as const) {
      expect(bodyIsWhole(event(partial(state)))).toBe(false)
      expect(checkResult({ type: 'text_contains', value: 'abc' }, replay(partial(state))).state).toBe('passed')
      expect(checkResult({ type: 'text_contains', value: 'user' }, replay(partial(state))).detail).toBe('The replay kept only part of this response; checked when the script runs')
      expect(checkResult({ type: 'text_not_contains', value: 'user' }, replay(partial(state))).state).toBe('unchecked')
      expect(checkResult({ type: 'body_not_contains', value: 'abc' }, replay(partial(state))).state).toBe('failed')
      expect(checkResult({ type: 'text_matches', value: 'user' }, replay(partial(state))).state).toBe('unchecked')
      expect(checkResult({ type: 'body_regex_capture_equals', value: 'user":"(\\w+)', expected: 'x' }, replay(partial(state))).state).toBe('unchecked')
      expect(savedValueResult({ name: 'u', source: 'body', type: 'regex', expression: 'user":"(\\w+)' }, replay(partial(state))).state).toBe('unchecked')
      // The headers are kept whole.
      expect(checkResult({ type: 'text_contains', value: 'xml', source: 'headers' }, replay(partial(state))).state).toBe('failed')
    }
    expect(bodyIsWhole(event({ response_body_state: 'partial' }))).toBe(false)
    expect(bodyIsWhole(event())).toBe(true)
  })

  it('checks text in the body, the headers or both, with case and references', () => {
    expect(checkResult({ type: 'text_contains', value: 'abc123' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'text_contains', value: 'ADA' }, replay())).toMatchObject({ state: 'failed', detail: 'Failed: “ADA” is not in the response' })
    expect(checkResult({ type: 'text_contains', value: 'ADA', case_insensitive: true }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'text_contains', value: 'application/json', source: 'headers' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'text_contains', value: 'application/json' }, replay()).state).toBe('failed')
    expect(checkResult({ type: 'text_not_contains', value: 'error' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'body_contains', value: '{{var.name}}' }, replay({}, { name: 'Ada' })).state).toBe('passed')
    expect(checkResult({ type: 'text_contains', value: '' }, replay())).toEqual({ state: 'unchecked', detail: 'Enter the text to look for' })
  })

  it('checks patterns, headers and captures', () => {
    expect(checkResult({ type: 'text_matches', value: 'abc\\d+' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'text_not_matches', value: 'abc\\d+' }, replay()).state).toBe('failed')
    expect(checkResult({ type: 'text_matches', value: '(' }, replay()).state).toBe('unchecked')
    expect(checkResult({ type: 'header_contains', value: 'content-type', expected: 'json' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'header_contains', value: 'X-Missing', expected: 'x' }, replay())).toEqual({ state: 'failed', detail: 'Failed: no X-Missing header' })
    expect(checkResult({ type: 'body_regex_capture_equals', value: '"user":"(\\w+)"', expected: 'Ada' }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'body_regex_capture_equals', value: '"user":"(\\w+)"', expected: 'Bob' }, replay())).toEqual({ state: 'failed', detail: 'Failed: captured “Ada”' })
  })

  it('checks a time limit against the replayed time', () => {
    expect(checkResult({ type: 'response_time_ms', max: 500 }, replay()).state).toBe('passed')
    expect(checkResult({ type: 'response_time_ms', max: 100 }, replay())).toEqual({ state: 'failed', detail: 'Failed: took 240 ms' })
  })

  it('checks the page title and URL a browser replay captured', () => {
    const page = { page_title: 'Welcome', page_url: 'https://shop.test/home' }
    expect(checkResult({ type: 'page_title', value: 'Welcome' }, replay(page)).state).toBe('passed')
    // The engine reads expected when value is empty.
    expect(checkResult({ type: 'page_title', expected: 'Other' }, replay(page)).state).toBe('failed')
    expect(checkResult({ type: 'url_contains', value: '/home' }, replay(page)).state).toBe('passed')
  })

  it('takes the step’s own outcome for checks the response cannot answer, until the script changes', () => {
    expect(checkResult({ type: 'element_visible', value: '#ok' }, replay()).detail).toBe('Passed in the last replay')
    expect(checkResult({ type: 'element_visible', value: '#ok' }, replay({ success: false, error_message: 'element_visible failed: #ok' })).state).toBe('failed')
    expect(checkResult({ type: 'element_visible', value: '#ok' }, replay({ success: false, error_message: 'timeout' })).state).toBe('unchecked')
    expect(checkResult({ type: 'element_visible', value: '#ok' }, replay({}, {}, true)).state).toBe('unchecked')
  })

  it('shows what a saved value finds in the replayed response', () => {
    expect(savedValueResult({ name: 'token', source: 'body', type: 'json', expression: '$.token' }, replay())).toEqual({ state: 'passed', detail: 'Saves “abc123”' })
    expect(savedValueResult({ name: 'token', source: 'body', type: 'boundary', expression: boundaryExpression('"token":"', '"') }, replay())).toEqual({ state: 'passed', detail: 'Saves “abc123”' })
    expect(savedValueResult({ name: 'token', source: 'body', type: 'boundary', expression: boundaryExpression('<x>', '</x>') }, replay())).toEqual({ state: 'failed', detail: 'No match in the response' })
    expect(savedValueResult({ name: 'ct', source: 'header', type: '', expression: 'content-type' }, replay())).toEqual({ state: 'passed', detail: 'Saves “application/json”' })
    expect(savedValueResult({ name: 'ct', source: 'header', type: '', expression: 'x-none' }, replay()).state).toBe('failed')
    expect(savedValueResult({ name: 'sid', source: 'cookies', type: 'regex', expression: 'sid=([^;]+)' }, replay()).state).toBe('passed')
  })

  it('says what options change about a saved value', () => {
    expect(savedValueResult({ name: 'x', source: 'body', type: 'regex', expression: 'none(\\d)', on_miss: 'empty' }, replay())).toEqual({ state: 'passed', detail: 'No match: saves an empty value' })
    expect(savedValueResult({ name: 'q', source: 'body', type: 'regex', expression: '"(\\w+)":', mode: 'all' }, replay())).toEqual({ state: 'passed', detail: 'Saves 2 values as q_1…' })
    expect(savedValueResult({ name: 't', source: 'body', type: 'json', expression: '$.token', save_offset: 2 }, replay()).detail).toBe('Finds “abc123”; its offset, length or conversion changes what is saved')
    expect(savedValueResult({ name: 't', source: 'body', type: 'json_path', expression: '$.token' }, replay()).detail).toBe('Saves “abc123”')
  })

  it('falls back to what the replay saved for an expression it cannot preview', () => {
    const extractor = { name: 'doc', source: 'body', type: 'xpath_custom', expression: '//x' }
    expect(savedValueResult(extractor, replay({ extractions: { doc: 'v1' } }))).toEqual({ state: 'passed', detail: 'Saved “v1” in the last replay' })
    expect(savedValueResult(extractor, replay({ extractions: { doc: 'v1' } }, {}, true)).state).toBe('unchecked')
    expect(savedValueResult(extractor, null).detail).toBe('Not replayed yet')
  })

  it('uses applied streamed child values, including prototype-like names, without using rejected values', () => {
    const extractor = { name: '__proto__', source: 'body', type: 'xpath_custom', expression: '//x' }
    const children = [{ action_name: 'child', success: true, extractions: JSON.parse('{"__proto__":"value"}') as Record<string, string> }]
    expect(savedValueResult(extractor, replay({ streamed_children: children }))).toEqual({ state: 'passed', detail: 'Saved “value” in the last replay' })
    const rejected = [{ action_name: 'child', success: false, unapplied_extractions: { constructor: 'rejected' } }]
    expect(savedValueResult({ ...extractor, name: 'constructor' }, replay({ streamed_children: rejected })).state).toBe('unchecked')
  })
})

describe('the replay of a step', () => {
  const events = [
    event({ action_index: undefined }),
    event({ action_index: 1, step_path: ['loop[0]'] }),
    event({ action_index: 1, request_index: 0 }),
    event({ action_index: undefined, duration_ms: 9 }),
  ]

  it('finds the step’s last event, reading a missing action index as zero', () => {
    expect(lastStepEvent(events, 'action', 0)).toBe(3)
    expect(lastStepEvent(events, 'action', 1, ['loop[0]'])).toBe(1)
    expect(lastStepEvent(events, 'action', 1, ['requests[0]'])).toBe(2)
    expect(lastStepEvent(events, 'action', 1)).toBe(-1)
    expect(lastStepEvent(events, 'init', 0)).toBe(-1)
  })

  it('keys an event by its step, a missing action index as zero', () => {
    expect(replayEventStepKey(event({ action_index: undefined }))).toBe('action:0')
    expect(replayEventStepKey(event({ phase: 'init', action_index: 2, step_path: ['loop[0]', 'then[1]'] }))).toBe('init:2#loop[0]/then[1]')
    expect(replayEventStepKey(event({ action_index: 3, request_index: 1 }))).toBe('action:3#requests[1]')
    expect(replayEventStepKey(event({ phase: 'session' }))).toBeNull()
  })

  it('reads a sequence request’s place from its index when it has no path', () => {
    expect(replayEventStepPath(event({ request_index: 2 }))).toEqual(['requests[2]'])
    expect(replayEventStepPath(event({ request_index: 2, step_path: ['loop[0]', 'requests[2]'] }))).toEqual(['loop[0]', 'requests[2]'])
    expect(replayEventStepPath(event())).toEqual([])
  })

  it('resolves only the variables the replay had', () => {
    const resolve = replayResolver({ id: '42' })
    expect(resolve('/orders/{{var.id}}')).toBe('/orders/42')
    expect(resolve('{{ var.id }}')).toBe('42')
    expect(resolve('{{var.other}}')).toBeNull()
    expect(resolve('{{data.users.name}}')).toBeNull()
    expect(resolve('plain')).toBe('plain')
  })
})
