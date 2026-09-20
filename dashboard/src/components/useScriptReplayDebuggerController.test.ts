import { describe, expect, it } from 'vitest'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugLogEntry,
  ScriptDebugSession,
} from '../types/replay'
import { debugBreakpointKey } from './scriptEditorReplayModel'
import { debugStepPathKey } from './scriptEditorWorkbench'
import {
  buildDebugRowPositionByKey,
  buildEventIndexByLocation,
  buildEventsByStepPath,
  deriveDebuggerStatus,
  filterDebugLogs,
  selectDebugEvents,
  toggleExpandedSequence,
} from './useScriptReplayDebuggerController'

function replayEvent(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action',
    action_name: 'Fetch orders',
    action_type: 'GET',
    target: '/orders',
    success: true,
    started_at: '2026-07-20T12:00:00Z',
    completed_at: '2026-07-20T12:00:01Z',
    duration_ms: 1000,
    ...overrides,
  }
}

function replayReport(events: BrowserReplayEvent[], success = true): BrowserReplayReport {
  return {
    group: 'default',
    protocol: 'http',
    started_at: '2026-07-20T12:00:00Z',
    completed_at: '2026-07-20T12:00:01Z',
    success,
    events,
  }
}

function debugLog(overrides: Partial<ScriptDebugLogEntry> = {}): ScriptDebugLogEntry {
  return {
    id: 'log-1',
    timestamp: '2026-07-20T12:00:00Z',
    level: 'info',
    category: 'request',
    message: 'sent request',
    ...overrides,
  }
}

describe('script replay debugger controller selectors', () => {
  it('selects the most complete event stream and prefers the live stream on ties', () => {
    const reportEvents = [replayEvent({ action_name: 'Report one' }), replayEvent({ action_name: 'Report two' })]
    const sessionEvents = [replayEvent({ action_name: 'Session one' })]

    expect(selectDebugEvents({ events: sessionEvents } as ScriptDebugSession, replayReport(reportEvents)))
      .toBe(reportEvents)
    expect(selectDebugEvents({ events: reportEvents } as ScriptDebugSession, replayReport(sessionEvents)))
      .toBe(reportEvents)
    expect(selectDebugEvents(null, null)).toEqual([])
  })

  it('renders out-of-order live deltas while a full snapshot catches up', () => {
    const first = replayEvent({ action_index: 0 })
    const last = replayEvent({ action_index: 2 })
    const sparse: BrowserReplayEvent[] = []
    sparse[2] = last
    const available = selectDebugEvents(null, replayReport([...sparse]))
    expect(available).toEqual([last])
    expect(() => buildEventIndexByLocation(available)).not.toThrow()
    expect(selectDebugEvents({ events: [first, last] } as ScriptDebugSession, replayReport([...sparse]))).toEqual([first, last])
  })

  it.each([
    [true, false, null, 'running'],
    [true, true, null, 'paused'],
    [false, false, replayReport([], true), 'success'],
    [false, false, replayReport([], false), 'error'],
    [false, false, null, 'idle'],
  ] as const)('derives %s/%s replay state as %s', (replaying, paused, report, expected) => {
    expect(deriveDebuggerStatus(replaying, paused, report)).toBe(expected)
  })

  it('filters logs by clear position, severity, and formatted text', () => {
    const logs = [
      debugLog({ id: 'old', level: 'error', message: 'old failure' }),
      debugLog({ id: 'warn', level: 'warn', message: 'retrying order' }),
      debugLog({ id: 'info', level: 'info', message: 'order complete' }),
      debugLog({ id: 'trace', level: 'trace', message: 'headers', raw: 'x-request-id: match-me' }),
    ]

    expect(filterDebugLogs(logs, 1, 'warn', '').map(entry => entry.id)).toEqual(['warn'])
    expect(filterDebugLogs(logs, 1, 'all', 'MATCH-ME').map(entry => entry.id)).toEqual(['trace'])
    expect(filterDebugLogs(logs, logs.length, 'all', '')).toEqual([])
  })

  it('indexes request events separately while retaining the first row position', () => {
    const events = [
      replayEvent({ phase: 'init', action_index: 0 }),
      replayEvent({ action_index: 2, request_index: 0 }),
      replayEvent({ action_index: 2, request_index: 1 }),
      replayEvent({ action_index: undefined }),
    ]

    const byLocation = buildEventIndexByLocation(events)
    expect(byLocation.get(debugBreakpointKey('init', 0))).toBe(0)
    expect(byLocation.get(debugBreakpointKey('action', 2, 0))).toBe(1)
    expect(byLocation.get(debugBreakpointKey('action', 2, 1))).toBe(2)
    expect(byLocation.size).toBe(3)

    const byRow = buildDebugRowPositionByKey(events)
    expect(byRow.get(debugBreakpointKey('action', 2))).toBe(1)
    expect(byRow.size).toBe(2)
  })

  it('groups repeated loop events by their stable step path', () => {
    const stepPath = ['then[0]', 'requests[1]']
    const events = [
      replayEvent({ action_index: 3, step_path: stepPath, iteration_path: [1] }),
      replayEvent({ action_index: 3, step_path: stepPath, iteration_path: [2] }),
      replayEvent({ action_index: 3, step_path: [] }),
    ]

    const groups = buildEventsByStepPath(events)
    expect(groups.get(debugStepPathKey('action', 3, stepPath))).toEqual([0, 1])
    expect(groups.size).toBe(1)
  })

  it('toggles sequence expansion without mutating the current set', () => {
    const current = new Set(['action:1'])
    const added = toggleExpandedSequence(current, 'action:2')
    const removed = toggleExpandedSequence(added, 'action:1')

    expect([...current]).toEqual(['action:1'])
    expect([...added]).toEqual(['action:1', 'action:2'])
    expect([...removed]).toEqual(['action:2'])
  })
})
