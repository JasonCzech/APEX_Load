import type { BrowserReplayEvent, ScriptDebugBreakpoint } from '../types/replay'
import type { OutlineBreakpoint, OutlineRunStatus } from './ScriptOutlineTree'
import { debugBreakpointKey } from './scriptEditorReplayModel'
import type { OutlineRow } from './scriptOutlineModel'
import { debugSequenceKey, debugStepPathKey, type DebugRailRenderRow } from './scriptEditorWorkbench'

export interface DebugOutlineState {
  events: BrowserReplayEvent[]
  /** The last event at each action or sequence request, and each step's events (loop passes), as the debugger indexes them. */
  eventIndexByLocation: Map<string, number>
  eventsByStepPath: Map<string, number[]>
  breakpoints: ScriptDebugBreakpoint[]
  breakpointKeys: Set<string>
  /** The step the run is on (and the blocks around it), while it runs. */
  currentActionKey: string | null
  currentStepKey: string | null
  currentAncestorKeys: Set<string>
  /** The run finished: a step without evidence did not run. */
  terminal: boolean
}

/**
 * Where a row's breakpoint goes: the action, one request of a sequence, a step
 * inside a block, or every request of a sequence (set, or cleared when all are).
 */
export type DebugBreakpointTarget =
  | { phase: string; actionIndex: number; requestIndex?: number }
  | { phase: string; actionIndex: number; stepPath: string[] }
  | { phase: string; actionIndex: number; requests: number; set: boolean }

export interface DebugOutline {
  breakpoint: (row: OutlineRow) => OutlineBreakpoint | null
  breakpointTarget: (row: OutlineRow) => DebugBreakpointTarget | null
  status: (row: OutlineRow) => OutlineRunStatus | null
  /** The evidence a row opens: its event, or a looped step's last pass. */
  eventIndex: (item: DebugRailRenderRow) => number | undefined
}

interface Ran { count: number; failed: boolean }

const pathKey = (phase: string, actionIndex: number, stepPath: readonly string[]) => `${phase}#${actionIndex}#${stepPath.join('/')}`

/**
 * The Debug workspace's view of the outline: each row's breakpoint and run
 * status, from the session's events and breakpoints. A block's status rolls
 * up the steps inside it, as a transaction's rolls up its steps.
 */
export function buildDebugOutline({ events, eventIndexByLocation, eventsByStepPath, breakpoints, breakpointKeys, currentActionKey, currentStepKey, currentAncestorKeys, terminal }: DebugOutlineState): DebugOutline {
  const ran = new Map<string, Ran>()
  events.forEach(event => {
    if (typeof event.action_index !== 'number') return
    const path = event.step_path || []
    for (let depth = 0; depth <= path.length; depth += 1) {
      const key = pathKey(event.phase, event.action_index, path.slice(0, depth))
      const summary = ran.get(key) || { count: 0, failed: false }
      summary.count += 1
      summary.failed ||= !event.success
      ran.set(key, summary)
    }
  })

  const disabled = new Set(breakpoints.filter(bp => bp.disabled).map(bp => bp.step_path?.length
    ? debugStepPathKey(bp.phase, bp.action_index, bp.step_path)
    : debugBreakpointKey(bp.phase, bp.action_index, bp.request_index)))
  // Breakpoints on a block's requests or steps, by the block's action; and on
  // a sequence's requests alone.
  const inside = new Map<string, number>()
  const onRequests = new Map<string, number>()
  for (const bp of breakpoints) {
    if (bp.request_index == null && !bp.step_path?.length) continue
    const key = debugSequenceKey(bp.phase, bp.action_index)
    inside.set(key, (inside.get(key) || 0) + 1)
    // A one-shot Run-to target is the run's, not one of the requests' own.
    if (bp.request_index != null && !bp.once) onRequests.set(key, (onRequests.get(key) || 0) + 1)
  }

  const runStatus = (summary: Ran | undefined, current: boolean): OutlineRunStatus | null =>
    summary?.failed ? 'fail' : current ? 'current' : summary?.count ? 'pass' : terminal ? 'not-run' : null

  const actionCurrent = (key: string) => currentActionKey === key || currentAncestorKeys.has(key)

  const itemStatus = (item: DebugRailRenderRow): OutlineRunStatus | null => {
    const { phase, actionIndex } = item.row
    if (item.kind === 'action') return runStatus(ran.get(pathKey(phase, actionIndex, [])), actionCurrent(item.key))
    if (item.kind === 'container') return runStatus(ran.get(pathKey(phase, actionIndex, item.stepPath)), currentAncestorKeys.has(item.key))
    if (item.requestIndex != null) {
      const index = eventIndexByLocation.get(item.key)
      return runStatus(index == null ? undefined : { count: 1, failed: !events[index].success }, currentActionKey === item.key)
    }
    return runStatus(ran.get(pathKey(phase, actionIndex, item.stepPath)), currentStepKey === debugStepPathKey(phase, actionIndex, item.stepPath))
  }

  const breakpointTarget = (row: OutlineRow): DebugBreakpointTarget | null => {
    const item = row.select
    if (!item || row.kind === 'phase' || row.kind === 'transaction' || row.kind === 'subtransaction' || item.kind === 'container') return null
    const { phase, actionIndex } = item.row
    if (row.allRequests) return { phase, actionIndex, requests: row.allRequests, set: (onRequests.get(debugSequenceKey(phase, actionIndex)) || 0) < row.allRequests }
    if (item.kind === 'action') return { phase, actionIndex }
    return item.requestIndex != null ? { phase, actionIndex, requestIndex: item.requestIndex } : { phase, actionIndex, stepPath: item.stepPath }
  }

  return {
    breakpointTarget,
    breakpoint(row) {
      const target = breakpointTarget(row)
      const item = row.select
      if (!target || !item) return null
      if ('requests' in target) {
        const set = onRequests.get(debugSequenceKey(target.phase, target.actionIndex)) || 0
        if (!target.set) return { state: 'active', title: `Remove the breakpoints on all ${target.requests} requests` }
        return { state: set ? 'partial' : 'none', title: `Add a breakpoint on all ${target.requests} requests${set ? ` (${set} set)` : ''}` }
      }
      const key = 'stepPath' in target
        ? debugStepPathKey(target.phase, target.actionIndex, target.stepPath)
        : debugBreakpointKey(target.phase, target.actionIndex, target.requestIndex)
      const name = item.kind === 'leaf' ? item.name : item.row.action.name || `action ${item.row.actionIndex + 1}`
      if (breakpointKeys.has(key)) return { state: disabled.has(key) ? 'disabled' : 'active', title: `Remove breakpoint on ${name}` }
      const within = item.kind === 'action' ? inside.get(key) || 0 : 0
      if (within) return { state: 'partial', title: `Add breakpoint on ${name} (${within} inside it)` }
      return { state: 'none', title: `Add breakpoint on ${name}` }
    },
    status(row) {
      if (row.kind === 'phase' || row.allRequests) return null
      if (row.kind === 'transaction' || row.kind === 'subtransaction') {
        let current = false
        let count = 0
        let failed = false
        for (const step of row.rows) {
          const key = debugSequenceKey(step.phase, step.actionIndex)
          const summary = ran.get(pathKey(step.phase, step.actionIndex, []))
          count += summary?.count || 0
          failed ||= Boolean(summary?.failed)
          current ||= actionCurrent(key)
        }
        return runStatus({ count, failed }, current)
      }
      return row.select ? itemStatus(row.select) : null
    },
    eventIndex(item) {
      if (item.kind === 'container') return undefined
      if (item.kind === 'action' || item.requestIndex != null) return eventIndexByLocation.get(item.key)
      const passes = eventsByStepPath.get(debugStepPathKey(item.row.phase, item.row.actionIndex, item.stepPath))
      return passes?.[passes.length - 1]
    },
  }
}
