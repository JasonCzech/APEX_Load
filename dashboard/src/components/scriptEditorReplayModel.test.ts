import { replayEvidenceIndex } from './scriptEditorReplayModel'
import { describe, expect, it } from 'vitest'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugLogEntry,
  ScriptDebugSession,
} from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import {
  buildBrowserHTTPConversionReview,
  debugBreakpointKey,
  debugLogLevelWeight,
  formatConditionEvidence,
  formatDebugLeafSuffix,
  formatDebugLogEntry,
  highlightDebugLogRaw,
  tokenizeDebugLogRawLine,
  formatReplayDiagnosisCategory,
  hasStructuredReplaySnapshot,
  humanizeStepSegment,
  normalizeDebugPhase,
  normalizeStoredBreakpoints,
  replayNetworkEntriesForConversion,
  safeParseReplayVariables,
  upsertReplayActivity,
} from './scriptEditorReplayModel'

function replayEvent(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action',
    action_name: 'Open home',
    action_type: 'browser_navigate',
    target: 'https://example.test',
    success: true,
    started_at: '2026-07-20T12:00:00Z',
    completed_at: '2026-07-20T12:00:01Z',
    duration_ms: 1000,
    ...overrides,
  }
}

function replayReport(events: BrowserReplayEvent[], completedAt = ''): BrowserReplayReport {
  return {
    group: 'default',
    protocol: 'browser',
    started_at: '2026-07-20T12:00:00Z',
    completed_at: completedAt,
    success: true,
    events,
  }
}

function browserScript(): ScriptDefinition {
  return {
    ...EMPTY_SCRIPT,
    protocol: 'browser',
    actions: [
      { name: 'Open', type: 'browser_navigate', target: 'https://example.test', headers: {}, body: '' },
      { name: 'API', type: 'GET', target: '/health', headers: {}, body: '' },
    ],
  }
}

describe('script editor replay model', () => {
  it('prefers live session events and falls back to report events when the session is empty', () => {
    const reportEvent = replayEvent({ action_name: 'Report event' })
    const sessionEvent = replayEvent({ action_name: 'Session event' })
    const session = { events: [sessionEvent] } as ScriptDebugSession

    expect(replayNetworkEntriesForConversion(
      replayReport([{ ...reportEvent, network_entries: [{ url: '/report' }] }]),
      { ...session, events: [{ ...sessionEvent, network_entries: [{ url: '/session' }] }] } as ScriptDebugSession,
    )).toEqual([{ method: 'GET', url: '/session' }])
    expect(replayNetworkEntriesForConversion(
      replayReport([{ ...reportEvent, network_entries: [{ url: '/report' }] }]),
      { events: [] } as unknown as ScriptDebugSession,
    )).toEqual([{ method: 'GET', url: '/report' }])
  })

  it('normalizes and deduplicates captured network requests by method and URL', () => {
    const event = replayEvent({
      method: 'post',
      network_entries: [
        { method: 'get', url: ' https://example.test/api ' },
        { method: 'GET', url: 'https://example.test/api', bytes_received: 99 },
        { method: 'GET', url: '   ' },
      ],
      snapshot: {
        request_method: 'post',
        request_url: 'https://example.test/submit',
        response_status: 201,
        network_entries: [{ method: 'POST', url: 'https://example.test/submit' }],
      },
    })

    expect(replayNetworkEntriesForConversion(replayReport([event]))).toEqual([
      { method: 'GET', url: 'https://example.test/api' },
      { method: 'POST', url: 'https://example.test/submit' },
    ])
  })

  it.each([
    ['needs_replay', replayReport([]), 0],
    ['blocked', replayReport([replayEvent()]), 0],
    ['ready_for_review', replayReport([replayEvent({ network_entries: [{ url: '/orders' }] })]), 1],
  ] as const)('classifies HTTP conversion as %s', (status, report, requestCount) => {
    const review = buildBrowserHTTPConversionReview(browserScript(), report)
    expect(review.status).toBe(status)
    expect(review.browserActionCount).toBe(1)
    expect(review.candidateRequestCount).toBe(requestCount)
  })

  it('recognizes protocol request/response evidence without treating empty snapshots as structured', () => {
    expect(hasStructuredReplaySnapshot(null)).toBe(false)
    expect(hasStructuredReplaySnapshot({})).toBe(false)
    expect(hasStructuredReplaySnapshot({ response_status: 200 })).toBe(true)
    expect(hasStructuredReplaySnapshot({ request_headers: {} })).toBe(true)
  })

  it('parses replay variables safely', () => {
    expect(safeParseReplayVariables('{"user":"alice","attempt":2}')).toEqual({ user: 'alice', attempt: '2' })
    expect(safeParseReplayVariables('{not json')).toBeUndefined()
  })

  it('normalizes persisted breakpoints and drops malformed fields', () => {
    expect(normalizeStoredBreakpoints([
      { phase: 'init', action_index: 1, request_index: 2, step_path: ['requests[2]'] },
      { phase: 'unexpected', action_index: '3', request_index: -1, step_path: ['then[0]', 2] },
      { action_index: -1 },
      null,
    ])).toEqual([
      { phase: 'init', action_index: 1, request_index: 2, step_path: ['requests[2]'] },
      { phase: 'action', action_index: 3 },
    ])
    expect(normalizeDebugPhase('teardown')).toBe('teardown')
    expect(debugBreakpointKey('action', 3, 2)).toBe('action:3:2')
  })

  it('formats nested debugger locations and condition evidence', () => {
    expect(humanizeStepSegment('requests[2]')).toBe('Request 3')
    expect(humanizeStepSegment('custom[0]')).toBe('custom 1')
    expect(humanizeStepSegment('plain')).toBe('plain')
    expect(formatDebugLeafSuffix({ request_index: 1, request_count: 4, iteration_path: [2, 3] }))
      .toBe(' · request 2/4 · loop iter 2·3')
    expect(formatConditionEvidence({ expr: 'int(var.i) < 3', result: true })).toBe('int(var.i) < 3 → true')
    expect(formatConditionEvidence({ left: 'token', op: 'exists', result: false })).toBe('token exists → false')
    expect(formatConditionEvidence({ left: 'admin', op: 'eq', right: 'user', result: false }))
      .toBe('admin eq user → false')
  })

  it('formats and orders raw debug log levels', () => {
    const entry: ScriptDebugLogEntry = {
      id: 'log-1',
      timestamp: '2026-07-20T12:00:00Z',
      level: 'info',
      category: 'request',
      phase: 'action',
      action_index: 2,
      message: 'sent',
      raw: 'GET /orders',
    }
    expect(debugLogLevelWeight('error')).toBeLessThan(debugLogLevelWeight('debug'))
    expect(debugLogLevelWeight('all')).toBe(5)
    expect(formatDebugLogEntry(entry)).toContain('INFO request action 3 sent\nGET /orders')
    expect(formatReplayDiagnosisCategory('selector_not_found')).toBe('Selector Not Found')
    expect(formatReplayDiagnosisCategory()).toBe('Diagnosis')
  })

  it('tokenizes debugger protocol output without changing its text', () => {
    const cases = [
      ['GET /orders?customer=42 HTTP/1.1', ['method', 'plain', 'target', 'plain', 'number']],
      ['HTTP/1.1 200 OK', ['plain', 'status-success', 'plain']],
      ['HTTP/1.1 404 Not Found', ['plain', 'status-warning', 'plain']],
      ['HTTP/2 503 Unavailable', ['plain', 'status-error', 'plain']],
      ['Content-Type: application/json', ['header', 'punctuation', 'plain']],
      ['{"order_id":42,"ready":true,"state":"created"}', ['punctuation', 'key', 'punctuation', 'number', 'punctuation', 'key', 'punctuation', 'literal', 'punctuation', 'key', 'punctuation', 'string', 'punctuation']],
    ] as const

    cases.forEach(([line, kinds]) => {
      const tokens = tokenizeDebugLogRawLine(line)
      expect(tokens.map(token => token.kind)).toEqual(kinds)
      expect(tokens.map(token => token.text).join('')).toBe(line)
    })
  })

  it('bounds semantic highlighting and reports content omitted from display', () => {
    const raw = 'GET /orders HTTP/1.1\n{"order_id":42,"ready":true}\ntrailing-payload'
    const result = highlightDebugLogRaw(raw, {
      highlightChars: 36,
      highlightTokens: 6,
      renderChars: 54,
    })

    expect(result.tokens.length).toBeLessThanOrEqual(6)
    expect(result.tokens.map(token => token.text).join('') + result.plainRemainder).toBe(raw.slice(0, 54))
    expect(result.omittedCharCount).toBe(raw.length - 54)
  })

  it('appends new activity and replaces matching activity without reordering', () => {
    const first = { id: 'one', kind: 'start' as const, title: 'Started', timestamp: '1' }
    const second = { id: 'two', kind: 'event' as const, title: 'Action', timestamp: '2' }
    const appended = upsertReplayActivity([first], second)
    const replaced = upsertReplayActivity(appended, { ...first, title: 'Restarted' })

    expect(appended).toEqual([first, second])
    expect(replaced.map(item => item.id)).toEqual(['one', 'two'])
    expect(replaced[0].title).toBe('Restarted')
  })
})

describe('replayEvidenceIndex', () => {
  it('keeps a failed action selected after successful teardown', () => {
    expect(replayEvidenceIndex({ events: [{ success: true }, { success: false }, { success: true }] } as BrowserReplayReport)).toBe(1)
    expect(replayEvidenceIndex({ events: [] } as unknown as BrowserReplayReport)).toBeNull()
  })
})
