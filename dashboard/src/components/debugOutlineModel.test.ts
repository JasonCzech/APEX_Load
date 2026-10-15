import { describe, expect, it } from 'vitest'

import type { BrowserReplayEvent, ScriptDebugBreakpoint } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { buildDebugOutline, type DebugOutlineState } from './debugOutlineModel'
import { buildOutlineModel, visibleOutlineRows, type OutlineRow } from './scriptOutlineModel'
import { buildScriptPhaseActionRows } from './scriptEditorWorkbench'
import { buildEventIndexByLocation, buildEventsByStepPath } from './useScriptReplayDebuggerController'

type Raw = Record<string, unknown>
const marker = (role: 'start' | 'end', name: string) => ({
  name: `${role === 'start' ? 'Start' : 'End'}_${name}_transaction`,
  type: 'source_transaction',
  params: { function: `lr_${role}_transaction`, args: [name, role === 'end' ? 'LR_AUTO' : name] },
})

// Checkout (a transaction around Cart) · Pay (a sequence of two requests) · Retry (a loop around Poll).
const actions: Raw[] = [
  marker('start', 'Checkout'),
  { name: 'Cart', type: 'GET', method: 'GET', target: '/cart' },
  marker('end', 'Checkout'),
  { name: 'Pay', type: 'http_sequence', method: 'SEQUENCE', target: '/pay', params: { requests: [
    { name: 'Authorize', type: 'POST', method: 'POST', target: '/authorize' },
    { name: 'Capture', type: 'POST', method: 'POST', target: '/capture' },
  ] } },
  { name: 'Retry', type: 'control_loop', control: { count: 2, steps: [{ name: 'Poll', type: 'GET', method: 'GET', target: '/poll' }] } },
]
const script = { name: 'debug', protocol: 'http', actions: actions as unknown as ScriptActionDefinition[] } as unknown as ScriptDefinition
const model = buildOutlineModel(buildScriptPhaseActionRows(script))
const rows = visibleOutlineRows(model, { isExpanded: () => true, expandedContainers: new Set(['action:3', 'action:4']), query: '', allRequestsRows: true })
const row = (title: string): OutlineRow => {
  const found = rows.find(candidate => candidate.title === title)
  if (!found) throw new Error(`no row ${title}`)
  return found
}

const event = (actionIndex: number, success: boolean, extra: Partial<BrowserReplayEvent> = {}) => ({
  phase: 'action', action_index: actionIndex, action_name: '', action_type: 'GET', target: '', success,
  started_at: '', completed_at: '', duration_ms: 1, ...extra,
}) as BrowserReplayEvent

const outline = (events: BrowserReplayEvent[], overrides: Partial<DebugOutlineState> = {}) => buildDebugOutline({
  events,
  eventIndexByLocation: buildEventIndexByLocation(events),
  eventsByStepPath: buildEventsByStepPath(events),
  breakpoints: [],
  breakpointKeys: new Set(),
  currentActionKey: null,
  currentStepKey: null,
  currentAncestorKeys: new Set(),
  terminal: false,
  ...overrides,
})

describe('buildDebugOutline', () => {
  it('rolls each row up from its events: a failure inside wins, then the step being run', () => {
    const events = [
      event(1, true),
      event(3, true, { request_index: 0 }),
      event(3, false, { request_index: 1 }),
      event(4, true, { step_path: ['loop[0]'], iteration: 1 }),
      event(4, true, { step_path: ['loop[0]'], iteration: 2 }),
    ]
    const running = outline(events)
    expect(running.status(row('Checkout'))).toBe('pass')
    expect(running.status(row('Cart'))).toBe('pass')
    expect(running.status(row('Pay'))).toBe('fail')
    expect(running.status(row('Authorize'))).toBe('pass')
    expect(running.status(row('Capture'))).toBe('fail')
    expect(running.status(row('Retry'))).toBe('pass')
    expect(running.status(row('Poll'))).toBe('pass')
    expect(running.status(row('Actions'))).toBeNull()
    expect(running.status(row('All requests'))).toBeNull()

    // The loop is paused on Poll: Poll and Retry are current; nothing ran yet elsewhere.
    const paused = outline([], { currentActionKey: 'action:4', currentStepKey: 'action:4#loop[0]', currentAncestorKeys: new Set(['action:4', 'action:4#loop[0]']) })
    expect(paused.status(row('Poll'))).toBe('current')
    expect(paused.status(row('Retry'))).toBe('current')
    expect(paused.status(row('Cart'))).toBeNull()

    // Once the run is over, what has no evidence did not run.
    expect(outline([event(1, true)], { terminal: true }).status(row('Pay'))).toBe('not-run')
  })

  it('opens a looped step on its last pass and a request on its own event', () => {
    const events = [event(4, true, { step_path: ['loop[0]'] }), event(3, true, { request_index: 1 }), event(4, false, { step_path: ['loop[0]'] })]
    const debug = outline(events)
    expect(debug.eventIndex(row('Poll').select!)).toBe(2)
    expect(debug.eventIndex(row('Capture').select!)).toBe(1)
    expect(debug.eventIndex(row('Authorize').select!)).toBeUndefined()
  })

  it('gives breakable rows their breakpoint, and transactions none', () => {
    const breakpoints: ScriptDebugBreakpoint[] = [
      { phase: 'action', action_index: 1, disabled: true },
      { phase: 'action', action_index: 3, request_index: 0 },
      { phase: 'action', action_index: 4, step_path: ['loop[0]'] },
    ]
    const debug = outline([], { breakpoints, breakpointKeys: new Set(['action:1', 'action:3:0', 'action:4#loop[0]']) })
    expect(debug.breakpoint(row('Checkout'))).toBeNull()
    expect(debug.breakpoint(row('Actions'))).toBeNull()
    expect(debug.breakpoint(row('Cart'))).toEqual({ state: 'disabled', title: 'Remove breakpoint on Cart' })
    expect(debug.breakpoint(row('Pay'))).toEqual({ state: 'partial', title: 'Add breakpoint on Pay (1 inside it)' })
    expect(debug.breakpoint(row('Authorize'))?.state).toBe('active')
    expect(debug.breakpoint(row('Capture'))).toEqual({ state: 'none', title: 'Add breakpoint on Capture' })
    expect(debug.breakpoint(row('Retry'))?.state).toBe('partial')
    expect(debug.breakpoint(row('Poll'))?.state).toBe('active')

    // All requests sets the missing ones, and clears them once every request has one.
    expect(debug.breakpoint(row('All requests'))).toEqual({ state: 'partial', title: 'Add a breakpoint on all 2 requests (1 set)' })
    expect(debug.breakpointTarget(row('All requests'))).toEqual({ phase: 'action', actionIndex: 3, requests: 2, set: true })
    const full = outline([], { breakpoints: [...breakpoints, { phase: 'action', action_index: 3, request_index: 1 }] })
    expect(full.breakpoint(row('All requests'))?.state).toBe('active')
    expect(full.breakpointTarget(row('All requests'))).toEqual({ phase: 'action', actionIndex: 3, requests: 2, set: false })

    // A Run-to target on the other request is the run's: All requests still adds one there.
    const runTo = outline([], { breakpoints: [...breakpoints, { phase: 'action', action_index: 3, request_index: 1, once: true }] })
    expect(runTo.breakpointTarget(row('All requests'))).toEqual({ phase: 'action', actionIndex: 3, requests: 2, set: true })

    expect(debug.breakpointTarget(row('Capture'))).toEqual({ phase: 'action', actionIndex: 3, requestIndex: 1 })
    expect(debug.breakpointTarget(row('Poll'))).toEqual({ phase: 'action', actionIndex: 4, stepPath: ['loop[0]'] })
  })
})
