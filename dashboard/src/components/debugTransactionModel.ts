import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { DebugCompletionSnapshot } from './debugCompletionModel'
import { actionTransactionMarker, actionTransactionName, containerChildSteps, debugContainerKind, type ScriptActionPhase } from './scriptEditorWorkbench'

interface Location { phase: ScriptActionPhase; actionIndex: number; path: string[] }
/** Only identity and structure are retained, never authored request data. */
export interface DebugTransactionDefinition {
  key: string
  name: string
  kind: 'transaction' | 'subtransaction' | 'page' | 'step' | 'container'
  mode: 'markers' | 'span' | 'sequence' | 'control' | 'action' | 'manual'
  phase: ScriptActionPhase
  loopDepth: number
  location?: Location
  start?: Location
  end?: Location
  children: DebugTransactionDefinition[]
}
export interface DebugTransactionRow {
  key: string; name: string; kind: DebugTransactionDefinition['kind'] | 'execution'; phase: ScriptActionPhase
  status: 'Passed' | 'Failed' | 'Incomplete' | 'Not run' | 'Not observed'
  incomplete: boolean
  startedAt: string | null; completedAt: string | null; durationMs: number | null
  recordedDurationMs?: number | null
  executions: number; eventIndex?: number
  children: DebugTransactionRow[]
}
const last = <T,>(items: readonly T[]): T | undefined => items[items.length - 1]
const keyOf = (l: Location) => JSON.stringify([l.phase, l.actionIndex, l.path])
const phaseNames: Record<ScriptActionPhase, keyof Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>> = { init: 'init', action: 'actions', teardown: 'teardown' }

export function captureDebugTransactionDefinitions(script: ScriptDefinition): DebugTransactionDefinition[] {
  type Entry = { action: ScriptActionDefinition; location: Location }
  const parse = (entries: Entry[]): DebugTransactionDefinition[] => {
    const roots: DebugTransactionDefinition[] = []
    const stack: DebugTransactionDefinition[] = []
    const parents = new Map<DebugTransactionDefinition, DebugTransactionDefinition | undefined>()
    const append = (node: DebugTransactionDefinition, parent: DebugTransactionDefinition | undefined) => {
      ;(parent?.children || roots).push(node)
      parents.set(node, parent)
    }
    const closeTags = () => { while (last(stack)?.mode === 'span') stack.pop() }
    let phase: ScriptActionPhase | undefined
    for (const { action, location } of entries) {
      if (phase !== location.phase) closeTags()
      phase = location.phase
      const marker = actionTransactionMarker(action)
      const fn = String(action.params?.function || '').toLowerCase()
      const base = { phase: location.phase, loopDepth: location.path.filter(p => p.startsWith('loop[')).length }
      // Stop/resume calls are steps inside a span; only an end closes it.
      if (marker && (marker.role === 'start' || fn.includes('end'))) {
        closeTags()
        if (marker.role === 'start') {
          const group: DebugTransactionDefinition = { ...base, children: [], key: `transaction:${keyOf(location)}`, name: marker.name, kind: marker.sub ? 'subtransaction' : 'transaction', mode: 'markers', start: location }
          const args = action.params?.args
          const parentName = marker.sub && Array.isArray(args) && typeof args[1] === 'string' ? args[1].trim().toLowerCase() : ''
          const parent = parentName ? stack.find(open => open.mode === 'markers' && open.name.toLowerCase() === parentName) : undefined
          append(group, parent || last(stack)); stack.push(group)
        } else {
          let at = -1
          for (let i = stack.length - 1; i >= 0; i--) {
            const group = stack[i]
            if (group.mode === 'markers' && group.name.toLowerCase() === marker.name.toLowerCase() && (group.kind === 'subtransaction') === Boolean(marker.sub)) { at = i; break }
          }
          if (at >= 0) {
            const group = stack[at]
            group.end = location
            // Overlapping spans are siblings: a transaction that finishes
            // later cannot be contained by this one's evidence window.
            const overlap = stack[at + 1]
            if (overlap && parents.get(overlap) === group) {
              group.children = group.children.filter(child => child !== overlap)
              append(overlap, parents.get(group))
            }
            stack.splice(at, 1)
          }
        }
        continue
      }
      const tag = actionTransactionName(action)
      if (last(stack)?.mode === 'span' && last(stack)?.name !== tag) stack.pop()
      if (tag && !stack.some(g => g.mode === 'markers') && last(stack)?.mode !== 'span') {
        const group: DebugTransactionDefinition = { ...base, children: [], key: `tag:${keyOf(location)}`, name: tag, kind: 'transaction', mode: 'span' }
        append(group, last(stack)); stack.push(group)
      }
      const container = debugContainerKind(action)
      const manual = action.type === 'source_transaction' && fn === 'lr_set_transaction'
      const args = action.params?.args
      const name = manual && Array.isArray(args) && typeof args[0] === 'string' ? args[0] : action.name || action.type || 'Step'
      // A transparent request group only sends its requests together and
      // records no transaction at run time, so it is a plain container here.
      const sequenceTransaction = container === 'http_sequence' && action.metadata?.transparent_request_group !== true
      const node: DebugTransactionDefinition = { ...base, children: [], key: `step:${keyOf(location)}`, name, kind: manual || sequenceTransaction ? 'transaction' : container ? 'container' : ['request', 'GET', 'POST', 'browser_navigate'].includes(action.type) ? 'page' : 'step', mode: manual ? 'manual' : sequenceTransaction ? 'sequence' : container ? 'control' : 'action', location }
      if (container) {
        // Branch boundaries cannot pair transaction markers or tags together.
        const branches = new Map<string, Entry[]>()
        for (const child of containerChildSteps(action, container)) {
          const branch = child.segment.replace(/\[\d+\]$/, '')
          const list = branches.get(branch) || []
          list.push({ action: child.action, location: { ...location, path: [...location.path, child.segment] } }); branches.set(branch, list)
        }
        node.children = [...branches.values()].flatMap(parse)
      }
      append(node, last(stack))
    }
    return roots
  }
  const hasTransaction = (node: DebugTransactionDefinition): boolean => node.kind === 'transaction' || node.kind === 'subtransaction' || node.children.some(hasTransaction)
  return parse((Object.keys(phaseNames) as ScriptActionPhase[]).flatMap(phase => (script[phaseNames[phase]] || []).map((action, actionIndex) => ({ action, location: { phase, actionIndex, path: [] } })))).filter(hasTransaction)
}

interface Observation { event: BrowserReplayEvent; index: number; order: number; iterations: number[] }
interface Scope { from: number; to: number; iterations?: number[] }
const time = (value: string | undefined) => value && Number.isFinite(Date.parse(value)) ? value : null
// Subtract before converting to Number: epoch milliseconds lose the fractional
// precision that Go's RFC3339Nano timestamps retain for fast transactions.
const timestampNS = (value: string | null | undefined): bigint | null => {
  if (!value || !Number.isFinite(Date.parse(value))) return null
  const fraction = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})\.(\d+)(Z|[+-]\d{2}:?\d{2})$/i.exec(value)
  if (!fraction) return BigInt(Date.parse(value)) * 1000000n
  const whole = Date.parse(fraction[1] + fraction[3])
  if (!Number.isFinite(whole)) return BigInt(Date.parse(value)) * 1000000n
  return BigInt(whole) * 1000000n + BigInt(fraction[2].slice(0, 9).padEnd(9, '0'))
}
const compareTime = (a: string, b: string) => {
  const x = timestampNS(a)!, y = timestampNS(b)!
  return x < y ? -1 : x > y ? 1 : 0
}
const spanDuration = (start: string | null, end: string | null) => {
  const a = timestampNS(start), b = timestampNS(end)
  return a !== null && b !== null && b >= a ? Number(b - a) / 1000000 : null
}

export interface DebugTransactionTimingBreakdown {
  childSpanTotalMs: number
  // Extra counts from overlap; a triple overlap contributes two extra counts.
  overlapMs: number
  outsideChildrenMs: number
  parentElapsedMs: number
}

/** Reconcile immediate children's elapsed spans without counting nested time twice. */
export function debugTransactionTimingBreakdown(row: DebugTransactionRow): DebugTransactionTimingBreakdown | null {
  if (!row.children.length || row.incomplete) return null
  const start = timestampNS(row.startedAt), end = timestampNS(row.completedAt)
  if (start === null || end === null || end < start) return null
  const spans: Array<{ start: bigint; end: bigint }> = []
  for (const child of row.children) {
    if (!child.executions && child.status === 'Not run') continue
    const a = timestampNS(child.startedAt), b = timestampNS(child.completedAt)
    if (child.incomplete || a === null || b === null || b < a || a < start || b > end) return null
    spans.push({ start: a, end: b })
  }
  spans.sort((a, b) => a.start < b.start ? -1 : a.start > b.start ? 1 : 0)
  let sum = 0n, covered = 0n, previousEnd = start
  for (const span of spans) {
    sum += span.end - span.start
    const from = span.start > previousEnd ? span.start : previousEnd
    if (span.end > from) covered += span.end - from
    if (span.end > previousEnd) previousEnd = span.end
  }
  return { childSpanTotalMs: Number(sum) / 1000000, overlapMs: Number(sum - covered) / 1000000, outsideChildrenMs: Number(end - start - covered) / 1000000, parentElapsedMs: Number(end - start) / 1000000 }
}
const observedBoundary = (observed: Observation[], field: 'started_at' | 'completed_at') => {
  const times = observed.map(o => time(o.event[field]))
  if (!times.length || times.some(value => value === null)) return null
  const ordered = (times as string[]).sort(compareTime)
  return field === 'started_at' ? ordered[0] : last(ordered)!
}
const within = (o: Observation, scope: Scope) => o.order >= scope.from && o.order < scope.to && (!scope.iterations || scope.iterations.every((n, i) => o.iterations[i] === n))

export function debugTransactionRows(snapshot: DebugCompletionSnapshot): DebugTransactionRow[] {
  if (snapshot.session.runtime_evidence_unavailable || snapshot.report?.runtime_evidence_unavailable) return []
  const missing = Boolean(snapshot.session.dropped_event_count || snapshot.report?.dropped_event_count)
  const byLocation = new Map<string, Observation[]>()
  const seen = new Set<string>()
  ;(snapshot.report?.events || snapshot.session.events || []).forEach((event, index) => {
    const id = event.event_id || (event.event_index != null ? `index:${event.event_index}` : `position:${index}`)
    if (seen.has(id) || event.action_index == null || !['init', 'action', 'teardown'].includes(event.phase)) return
    seen.add(id)
    const path = event.step_path?.length ? event.step_path : event.request_index != null ? [`requests[${event.request_index}]`] : []
    const key = keyOf({ phase: event.phase as ScriptActionPhase, actionIndex: event.action_index, path })
    const list = byLocation.get(key) || []
    list.push({ event, index, order: event.event_index ?? index, iterations: event.iteration_path || (event.iteration != null ? [event.iteration] : []) }); byLocation.set(key, list)
  })
  byLocation.forEach(list => list.sort((a, b) => a.order - b.order))
  const observations = new Map<string, Observation[]>()
  const at = (l?: Location) => l ? byLocation.get(keyOf(l)) || [] : []
  const collect = (d: DebugTransactionDefinition): Observation[] => {
    const found = observations.get(d.key)
    if (found) return found
    const unique = new Map<number, Observation>()
    for (const o of [...at(d.location), ...at(d.start), ...at(d.end), ...d.children.flatMap(collect)]) unique.set(o.index, o)
    const list = [...unique.values()].sort((a, b) => a.order - b.order)
    observations.set(d.key, list); return list
  }
  const scopedKey = (d: DebugTransactionDefinition, scope: Scope) => `${d.key}@${scope.from}:${scope.to}:${scope.iterations?.join('.') || ''}`
  const empty = (d: DebugTransactionDefinition, scope: Scope): DebugTransactionRow => ({ key: scopedKey(d, scope), name: d.name, kind: d.kind, phase: d.phase, status: missing ? 'Not observed' : 'Not run', incomplete: missing, startedAt: null, completedAt: null, durationMs: null, executions: 0, children: d.children.map(child => empty(child, scope)) })
  const aggregate = (d: DebugTransactionDefinition, rows: DebugTransactionRow[], key: string): DebugTransactionRow => {
    if (rows.length === 1) return { ...rows[0], key, name: d.name, kind: d.kind }
    const starts = rows.map(row => row.startedAt).filter((v): v is string => Boolean(v)).sort(compareTime)
    const ends = rows.map(row => row.completedAt).filter((v): v is string => Boolean(v)).sort(compareTime)
    const startedAt = starts.length === rows.length ? starts[0] : null
    const completedAt = ends.length === rows.length ? last(ends)! : null
    return { key, name: d.name, kind: d.kind, phase: d.phase, status: rows.some(row => row.status === 'Failed') ? 'Failed' : rows.some(row => row.status === 'Incomplete') ? 'Incomplete' : 'Passed', incomplete: rows.some(row => row.incomplete), startedAt, completedAt, durationMs: spanDuration(startedAt, completedAt), executions: rows.length, children: rows.map((row, i) => ({ ...row, key: `${key}:execution:${i}`, name: `Execution ${i + 1}`, kind: 'execution' })) }
  }
  const build = (d: DebugTransactionDefinition, scope: Scope): DebugTransactionRow => {
    const all = collect(d).filter(o => within(o, scope))
    if (!all.length) return empty(d, scope)
    const starts = at(d.start).filter(o => within(o, scope))
    const direct = at(d.location).filter(o => within(o, scope))
    if (d.mode === 'action' || d.mode === 'manual') {
      return aggregate(d, direct.map(o => ({ key: `${d.key}:${o.index}`, name: d.name, kind: d.kind, phase: d.phase, status: o.event.success ? 'Passed' : 'Failed', incomplete: missing || d.mode === 'manual', startedAt: d.mode === 'manual' ? null : time(o.event.started_at), completedAt: d.mode === 'manual' ? null : time(o.event.completed_at), durationMs: d.mode === 'manual' ? null : spanDuration(time(o.event.started_at), time(o.event.completed_at)), recordedDurationMs: d.mode === 'manual' ? null : Number.isFinite(o.event.duration_ms) && o.event.duration_ms >= 0 ? o.event.duration_ms : null, executions: 1, eventIndex: o.index, children: [] })), scopedKey(d, scope))
    }
    let scopes: Array<{ scope: Scope; start?: Observation; end?: Observation }>
    if (d.mode === 'markers') {
      scopes = starts.length ? starts.map((start, i) => {
        const upper = Math.min(scope.to, starts[i + 1]?.order ?? Infinity)
        const end = at(d.end).find(o => o.order >= start.order && o.order < upper && within(o, scope) && JSON.stringify(o.iterations) === JSON.stringify(start.iterations))
        return { start, end, scope: { from: start.order, to: end ? end.order + 1 : upper, iterations: start.iterations } }
      }) : [{ scope }]
    } else {
      const passes = new Map<string, Observation[]>()
      for (const o of all) { const key = JSON.stringify(o.iterations.slice(0, d.loopDepth)); const list = passes.get(key) || []; list.push(o); passes.set(key, list) }
      scopes = [...passes.values()].map(list => ({ scope: { from: list[0].order, to: last(list)!.order + 1, iterations: list[0].iterations.slice(0, d.loopDepth) } }))
    }
    return aggregate(d, scopes.map(({ scope: window, start, end }, i) => {
      const observed = all.filter(o => within(o, window))
      const children = d.children.map(child => build(child, window))
      const localIncomplete = d.mode === 'markers' ? !start || !end : d.mode !== 'control' && children.some(row => row.executions === 0 || row.incomplete)
      const failed = end ? !end.event.success : observed.some(o => !o.event.success)
      const startedAt = d.mode === 'markers' ? time(start?.event.started_at) : observedBoundary(observed, 'started_at')
      const completedAt = localIncomplete ? null : d.mode === 'markers' ? time(end?.event.completed_at) : observedBoundary(observed, 'completed_at')
      return { key: `${d.key}:${i}`, name: d.name, kind: d.kind, phase: d.phase, status: failed ? 'Failed' : localIncomplete || missing && d.mode !== 'markers' ? 'Incomplete' : 'Passed', incomplete: localIncomplete || missing || !startedAt || !completedAt, startedAt, completedAt, durationMs: spanDuration(startedAt, completedAt), executions: 1, eventIndex: end?.index ?? start?.index ?? direct.find(o => within(o, window))?.index, children }
    }), scopedKey(d, scope))
  }
  return (snapshot.transactions || []).map(d => build(d, { from: -Infinity, to: Infinity }))
}
