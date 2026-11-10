/**
 * Debug-event adapter: builds the iteration → transaction → page/step →
 * request → redirect-hop tree that `<Waterfall>` renders for a debug run.
 */
import type { BrowserReplayEvent } from '../../types/replay'
import type { DebugTransactionRow } from '../debugTransactionModel'
import { isTimed, walk } from './analysis'
import { buildDebugTimelineModel, parseTimestampMs, type DebugTimelineModel, type DebugTimelineRowModel } from './debugEventModel'
import type { WaterfallNode, WaterfallNodeKind, WaterfallStatus } from './types'

export interface DebugWaterfallPayload {
  /** Position in the events array given to the builder. */
  position?: number
  /** The caller's event index (for example the index in the session's events). */
  eventIndex?: number
  hopIndex?: number
  transaction?: DebugTransactionRow
  /** Groups repeated executions of one transaction inside a script iteration. */
  aggregate?: boolean
}

export type DebugWaterfallNode = WaterfallNode<DebugWaterfallPayload>

export interface DebugWaterfallTree {
  roots: DebugWaterfallNode[]
  spanMs: number
  timeline: DebugTimelineModel
}

const pos = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : 0)
const HTTP_METHODS = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE)$/
const CONTAINMENT_TOLERANCE_MS = 0.5

export function isThinkTimeEvent(ev: BrowserReplayEvent): boolean {
  return /think/i.test(ev.action_type || '') || /think_time/i.test(ev.source_action || '') || /^lr_think_time\b/i.test(ev.action_name || '')
}

/**
 * Debug events carry no script-iteration number, so detect a new iteration
 * when an action-phase event moves back to an earlier action, or revisits a
 * location already seen this iteration at the iteration's first action.
 */
export function scriptIterationKeys(events: readonly BrowserReplayEvent[]): string[] {
  let iteration = 1
  let lastAction = -Infinity
  let firstAction: number | null = null
  let seen = new Set<string>()
  // A re-delivered event (same identity) takes its first occurrence's key and
  // does not move iteration state, matching the summary's own de-duplication.
  const firstKey = new Map<string, string>()
  return events.map(ev => {
    const identity = ev.event_id || `${ev.event_index}:${ev.phase}:${ev.action_index}:${ev.started_at}`
    const prior = firstKey.get(identity)
    if (prior) return prior
    const key = keyFor(ev)
    firstKey.set(identity, key)
    return key
  })
  function keyFor(ev: BrowserReplayEvent): string {
    if (ev.phase === 'init') return 'init'
    if (ev.phase === 'teardown') return 'teardown'
    if (ev.phase !== 'action') return `phase:${ev.phase || 'other'}`
    // The replay API omits a zero action_index, like every other event mapper assumes.
    const actionIndex = typeof ev.action_index === 'number' ? ev.action_index : 0
    // Same loop identity debugTransactionModel uses: iteration_path, else the legacy iteration.
    const loopPath = ev.iteration_path?.length ? ev.iteration_path : ev.iteration != null ? [ev.iteration] : []
    const loc = `${actionIndex}|${(ev.step_path || []).join('/')}|${ev.request_index ?? ''}|${loopPath.join('.')}`
    const restarted = firstAction !== null && (actionIndex < lastAction || (seen.has(loc) && actionIndex === firstAction))
    if (restarted) {
      iteration++
      seen = new Set()
      firstAction = null
    }
    if (firstAction === null) firstAction = actionIndex
    seen.add(loc)
    lastAction = actionIndex
    return `it:${iteration}`
  }
}

function iterationOrder(key: string): number {
  if (key === 'init') return -1
  if (key.startsWith('it:')) return Number(key.slice(3))
  if (key === 'teardown') return 1e6
  if (key === 'not_observed') return 1e6 + 2
  if (key === 'not_run') return 1e6 + 3
  return 1e6 + 1
}

function iterationLabel(key: string, actionIterations: number): string {
  if (key === 'init') return 'Init'
  if (key === 'teardown') return 'Teardown'
  if (key === 'not_run') return 'Not run'
  if (key === 'not_observed') return 'Not observed'
  if (key.startsWith('it:')) return actionIterations > 1 ? `Iteration ${key.slice(3)}` : 'Actions'
  return key.replace(/^phase:/, '').replace(/^./, c => c.toUpperCase())
}

const shortUrl = (url: string | undefined) => {
  if (!url) return ''
  try {
    const u = new URL(url)
    return `${u.host}${u.pathname}${u.search}`
  } catch {
    return url
  }
}

function statusOfRow(row: DebugTransactionRow): WaterfallStatus {
  if (row.status === 'Failed') return 'failed'
  if (row.status === 'Incomplete') return 'incomplete'
  // 'Not observed' means evidence was dropped, not that it never ran.
  if (row.status === 'Not observed') return 'incomplete'
  if (row.status === 'Not run') return 'not_run'
  return 'passed'
}

function kindOfRow(row: DebugTransactionRow): WaterfallNodeKind {
  if (row.kind === 'transaction' || row.kind === 'subtransaction') return 'transaction'
  if (row.kind === 'container') return 'container'
  return 'step'
}

export const isTransactionMarkerEvent = (ev: BrowserReplayEvent) => ev.action_type === 'source_transaction'

/** Parent events that span their child requests (the summary drops these). */
export const CONTAINER_EVENT_TYPES = new Set(['http_sequence', 'control_if', 'control_switch', 'control_loop', 'control_parallel'])
export const isContainerEvent = (ev: BrowserReplayEvent) => CONTAINER_EVENT_TYPES.has(ev.action_type)

function leafKind(row: DebugTimelineRowModel): WaterfallNodeKind {
  if (isThinkTimeEvent(row.event)) return 'think'
  if (isTransactionMarkerEvent(row.event)) return 'marker'
  // Anything with phase timings, a status, a method or network hops was a request.
  return row.timingSource !== 'duration' || row.statusCode != null || HTTP_METHODS.test(row.methodOrType) || row.hops.length > 0 ? 'request' : 'step'
}

function hopChildren(row: DebugTimelineRowModel, position: number, eventIndex: number, leafId: string): DebugWaterfallNode[] {
  if (row.hops.length > 1) {
    return row.hops.map(hop => ({
      id: `${leafId}#hop${hop.hopIndex}`,
      kind: 'hop',
      label: `${hop.statusCode ?? '—'} ${hop.method} ${shortUrl(hop.url)}`,
      detail: hop.url,
      startMs: hop.startOffsetMs,
      durationMs: hop.durationMs,
      status: (hop.statusCode ?? 0) >= 400 ? 'failed' : 'passed',
      phases: hop.segments.map(s => ({ key: s.key, startMs: s.startMs, endMs: s.endMs })),
      timingSource: hop.segments.some(s => s.key !== 'total') ? row.timingSource : 'duration',
      statusCode: hop.statusCode,
      method: hop.method,
      protocolLabel: hop.protocolLabel,
      connectionReused: hop.connectionReused,
      children: [],
      payload: { position, eventIndex, hopIndex: hop.hopIndex },
    }))
  }
  // Without per-hop evidence, show the redirect chain from the trace counts.
  const t = row.event.protocol_evidence?.timings as Record<string, unknown> | undefined
  const count = pos(t?.redirect_count)
  const redirectMs = Math.min(pos(t?.redirect_ms), row.durationMs)
  if (!count || !redirectMs) return []
  return [
    {
      id: `${leafId}#redirects`,
      kind: 'hop',
      label: `${count} ${count === 1 ? 'redirect' : 'redirects'} followed`,
      detail: 'From the trace redirect count; per-hop phases were not retained.',
      startMs: row.startOffsetMs,
      durationMs: redirectMs,
      status: 'passed',
      phases: [{ key: 'total', startMs: 0, endMs: redirectMs }],
      timingSource: 'duration',
      derived: true,
      children: [],
      payload: { position, eventIndex },
    },
    {
      id: `${leafId}#final`,
      kind: 'hop',
      label: `${row.statusCode ?? '—'} ${row.methodOrType} ${shortUrl(row.target)}`,
      detail: 'Final response after redirects.',
      startMs: row.startOffsetMs + redirectMs,
      durationMs: Math.max(0, row.durationMs - redirectMs),
      status: row.success ? 'passed' : 'failed',
      phases: [{ key: 'total', startMs: 0, endMs: Math.max(0, row.durationMs - redirectMs) }],
      timingSource: 'duration',
      derived: true,
      statusCode: row.statusCode,
      children: [],
      payload: { position, eventIndex },
    },
  ]
}

function leafNode(row: DebugTimelineRowModel, position: number, eventIndex: number, label?: string, id?: string): DebugWaterfallNode {
  const nodeId = id ?? `ev:${row.id}:${position}`
  const t = row.event.protocol_evidence?.timings as Record<string, unknown> | undefined
  const count = pos(t?.redirect_count)
  const hops = row.hops.length > 1 ? row.hops : null
  // Only the document's own chain redirects; typed sub-resources (images,
  // scripts, XHR) a navigation loaded alongside it are not redirect hops.
  const entries = row.event.network_entries?.length ? row.event.network_entries : row.event.snapshot?.network_entries ?? []
  const onPrimaryChain = (i: number) => {
    const type = entries[i]?.resource_type?.toLowerCase()
    return !type || type === 'document' || type === 'navigation'
  }
  const redirectHops = hops ? hops.filter(h => onPrimaryChain(h.hopIndex) && [301, 302, 303, 307, 308].includes(h.statusCode ?? 0)) : []
  return {
    id: nodeId,
    kind: leafKind(row),
    label: label || row.actionName || row.location,
    detail: row.errorMessage || `${row.location} · ${row.methodOrType} ${row.target}`,
    startMs: row.startOffsetMs,
    durationMs: row.durationMs,
    status: row.success ? 'passed' : 'failed',
    phases: row.segments.map(s => ({ key: s.key, startMs: s.startMs, endMs: s.endMs })),
    timingSource: row.timingSource,
    statusCode: row.statusCode,
    method: row.methodOrType,
    protocolLabel: row.protocolLabel,
    connectionReused: row.connectionReused,
    redirect: count > 0 ? { count, ms: pos(t?.redirect_ms) } : redirectHops.length ? { count: redirectHops.length, ms: redirectHops.reduce((s, h) => s + h.durationMs, 0) } : undefined,
    errorMessage: row.errorMessage,
    children: hopChildren(row, position, eventIndex, nodeId),
    payload: { position, eventIndex },
  }
}

const STATUS_RANK: Record<WaterfallStatus, number> = { passed: 0, not_run: 1, incomplete: 2, failed: 3 }
/**
 * Children decide pass/fail; a row spanning several instances only adds its
 * 'incomplete' (dropped evidence), since its failure may belong to another instance.
 */
function worseStatus(a: WaterfallStatus, row: WaterfallStatus, evidenceDropped: boolean): WaterfallStatus {
  // Only dropped evidence applies to every instance; a missing end marker
  // belongs to one execution, which reports it itself.
  return evidenceDropped && row === 'incomplete' && STATUS_RANK.incomplete > STATUS_RANK[a] ? 'incomplete' : a
}

function aggregateStatus(children: readonly DebugWaterfallNode[]): WaterfallStatus {
  if (children.some(c => c.status === 'failed')) return 'failed'
  if (children.some(c => c.status === 'incomplete')) return 'incomplete'
  if (children.length && children.every(c => c.status === 'not_run')) return 'not_run'
  return 'passed'
}

/** Parents without their own timing span their children. */
function finalizeTiming(node: DebugWaterfallNode) {
  node.children.forEach(finalizeTiming)
  if (isTimed(node) || !node.children.length) return
  // A transaction whose retained timing is incomplete (no end marker, or a
  // manual lr_set_transaction) stays untimed rather than borrowing a marker's span.
  const tx = node.payload?.transaction
  if (tx && tx.durationMs == null && !node.payload?.aggregate && !hasMeasuredWork(node)) return
  const timed = node.children.filter(isTimed)
  if (!timed.length) return
  const start = Math.min(...timed.map(c => c.startMs))
  const end = Math.max(...timed.map(c => c.startMs + c.durationMs))
  node.startMs = start
  node.durationMs = Math.max(0, end - start)
}

/** Some descendant actually took time (markers at 0 ms do not count). */
const hasMeasuredWork = (node: DebugWaterfallNode): boolean => node.children.some(c => (isTimed(c) && c.durationMs > 0 && !c.children.length) || hasMeasuredWork(c))

/**
 * A transaction whose only child is a same-named transaction (an HTTP sequence
 * and the tag span inside it) is one level, not two.
 */
function mergeSameNamedChains(node: DebugWaterfallNode) {
  node.children.forEach(mergeSameNamedChains)
  const only = node.children.length === 1 ? node.children[0] : null
  if (!only || only.label !== node.label || (node.kind !== 'transaction' && node.kind !== 'container') || (only.kind !== 'transaction' && only.kind !== 'container')) return
  if (!isTimed(node) && isTimed(only)) {
    node.startMs = only.startMs
    node.durationMs = only.durationMs
  }
  if (only.status === 'failed' || (only.status === 'incomplete' && node.status === 'passed')) node.status = only.status
  node.children = only.children
}

const firstPosition = (n: DebugWaterfallNode): number => {
  let best = n.payload?.position ?? Infinity
  for (const c of n.children) best = Math.min(best, firstPosition(c))
  return best
}

/** Order children by start (ties keep script order) and give synthetic parents their children's status. */
function sortAndSettle(node: DebugWaterfallNode, evidenceDropped = false) {
  node.children.forEach(c => sortAndSettle(c, evidenceDropped))
  node.children.sort((a, b) => {
    if (a.startMs == null || b.startMs == null) return a.startMs == null ? (b.startMs == null ? firstPosition(a) - firstPosition(b) : 1) : -1
    return a.startMs - b.startMs || firstPosition(a) - firstPosition(b)
  })
  if (node.kind === 'iteration' && node.id.startsWith('iteration:') && node.status !== 'not_run') node.status = aggregateStatus(node.children)
  else if (node.id.startsWith('src:')) node.status = aggregateStatus(node.children)
  // Aggregates keep their own row's incomplete evidence (dropped events).
  else if (node.payload?.aggregate) node.status = worseStatus(aggregateStatus(node.children), node.payload.transaction ? statusOfRow(node.payload.transaction) : 'passed', evidenceDropped)
}

function deepestContainer(nodes: readonly DebugWaterfallNode[], startMs: number, endMs: number): DebugWaterfallNode | null {
  for (const n of nodes) {
    if (n.kind !== 'transaction' && n.kind !== 'container' && n.kind !== 'iteration') continue
    if (!isTimed(n)) continue
    const end = n.startMs + n.durationMs
    // Starting exactly at a parent's end belongs to whatever follows it.
    const startsInside = startMs >= n.startMs - CONTAINMENT_TOLERANCE_MS && (startMs < end || n.durationMs === 0)
    if (startsInside && endMs <= end + CONTAINMENT_TOLERANCE_MS) {
      return deepestContainer(n.children, startMs, endMs) ?? n
    }
  }
  return null
}

export function buildDebugWaterfallTree(input: {
  events: readonly BrowserReplayEvent[]
  /** Caller's index for each event (defaults to its position). */
  eventIndices?: readonly number[]
  /** Transaction rows from `debugTransactionRows`, when definitions were retained. */
  transactions?: readonly DebugTransactionRow[] | null
  /**
   * The full, unfiltered event list indexed by eventIndices. The summary drops
   * container events before building the tree; iteration boundaries are found
   * on the full list so a container that starts each iteration still counts.
   */
  allEvents?: readonly BrowserReplayEvent[]
  /** The run dropped events, so every instance of a transaction may be incomplete. */
  evidenceDropped?: boolean
}): DebugWaterfallTree {
  const evidenceDropped = Boolean(input.evidenceDropped)
  const events = Array.isArray(input.events) ? input.events : []
  const timeline = buildDebugTimelineModel(events, 'waterfall')
  const indices = input.eventIndices && input.eventIndices.length === events.length ? input.eventIndices : events.map((_, i) => i)
  const positionByIndex = new Map(indices.map((idx, p) => [idx, p]))
  const allKeys = input.allEvents?.length ? scriptIterationKeys(input.allEvents) : null
  const ownKeys = scriptIterationKeys(events)
  const iterKeys = allKeys ? indices.map((idx, p) => allKeys[idx] ?? ownKeys[p]) : ownKeys
  const used = new Set<number>()
  const origin = timeline.originMs

  type Built = { node: DebugWaterfallNode; iter: string | null }

  // Executed rows whose own event is not in this list (the summary drops
  // container events) still belong to an iteration: find it by phase and time.
  const iterationSpans = new Map<string, { start: number; end: number }>()
  timeline.rows.forEach((tr, p) => {
    const k = iterKeys[p]
    const span = iterationSpans.get(k)
    const end = tr.startOffsetMs + tr.durationMs
    if (!span) iterationSpans.set(k, { start: tr.startOffsetMs, end })
    else {
      span.start = Math.min(span.start, tr.startOffsetMs)
      span.end = Math.max(span.end, end)
    }
  })
  // An executed row with no iteration evidence still ran: file it under its
  // phase (or the first action iteration), never under "Not run".
  const executedFallbackKey = (row: DebugTransactionRow): string => {
    if (row.phase === 'init' || row.phase === 'teardown') return row.phase
    const first = [...new Set(iterKeys.filter(k => k.startsWith('it:')))].sort((a, b) => iterationOrder(a) - iterationOrder(b))[0]
    return first ?? 'it:1'
  }
  const iterationForRow = (row: DebugTransactionRow): string | null => {
    if (row.phase === 'init' || row.phase === 'teardown') return iterationSpans.has(row.phase) ? row.phase : null
    const actionKeys = [...iterationSpans.keys()].filter(k => k.startsWith('it:')).sort((a, b) => iterationOrder(a) - iterationOrder(b))
    if (!actionKeys.length) return null
    const startedAt = parseTimestampMs(row.startedAt ?? undefined)
    if (startedAt == null || origin == null) return actionKeys[0]
    const at = startedAt - origin
    // The last iteration that had started by then.
    let hit = actionKeys[0]
    for (const k of actionKeys) if (iterationSpans.get(k)!.start <= at + CONTAINMENT_TOLERANCE_MS) hit = k
    return hit
  }

  const rowTiming = (row: DebugTransactionRow) => {
    const start = parseTimestampMs(row.startedAt ?? undefined)
    if (origin == null || start == null || row.durationMs == null) return { startMs: null, durationMs: null }
    // The origin comes from the retained events; a row that began before them
    // (a dropped container event) is clamped to the start of the axis.
    const offset = start - origin
    return offset >= 0 ? { startMs: offset, durationMs: row.durationMs } : { startMs: 0, durationMs: Math.max(0, row.durationMs + offset) }
  }

  const fromRow = (row: DebugTransactionRow): Built[] => {
    const aggregate = row.children.length > 0 && row.children.every(c => c.kind === 'execution')
    if (aggregate) {
      const execs = row.children.flatMap(c => fromRow({ ...c, name: row.name, kind: row.kind }))
      const groups = new Map<string, Built[]>()
      for (const b of execs) {
        const k = b.iter ?? 'not_run'
        groups.set(k, [...(groups.get(k) || []), b])
      }
      return [...groups].map(([iter, list]): Built => {
        if (list.length === 1) return list[0]
        // Repeated executions inside one script iteration are loop iterations.
        const children = list.map((b, k) =>
          b.node.children.length && b.node.kind !== 'request'
            ? { ...b.node, kind: 'iteration' as const, label: `Iteration ${k + 1}` }
            : { ...b.node, label: `${b.node.label} · #${k + 1}` },
        )
        return {
          iter: iter === 'not_run' ? null : iter,
          node: {
            id: `${row.key}@${iter}`,
            kind: kindOfRow(row),
            label: row.name,
            detail: `${list.length} executions`,
            startMs: null,
            durationMs: null,
            status: aggregateStatus(children),
            phases: [],
            children,
            payload: { transaction: row, aggregate: true },
          },
        }
      })
    }
    const isLeaf = row.children.length === 0 && row.eventIndex != null && row.kind !== 'transaction' && row.kind !== 'subtransaction' && row.kind !== 'container'
    if (isLeaf) {
      const p = positionByIndex.get(row.eventIndex!)
      const tr = p != null ? timeline.rows[p] : undefined
      if (p != null && tr && !used.has(p)) {
        used.add(p)
        return [{ node: leafNode(tr, p, indices[p], row.name, `${row.key}:ev${p}`), iter: iterKeys[p] }]
      }
    }
    const children = row.children.flatMap(fromRow)
    const ownPos = row.eventIndex != null ? positionByIndex.get(row.eventIndex) : undefined
    const iters = [...new Set(children.map(b => b.iter).filter((k): k is string => Boolean(k)))]
    if (row.executions > 0 && iters.length > 1) {
      // One retained span covered several script iterations (an untagged
      // loop-free span): show one instance per iteration, timed by its children.
      return iters.map((iter, i): Built => {
        const mine = children.filter(b => b.iter === iter || (i === 0 && !b.iter)).map(b => b.node)
        return {
          iter,
          node: {
            id: `${row.key}@${iter}`,
            kind: kindOfRow(row),
            label: row.name,
            detail: row.kind === 'subtransaction' ? 'Subtransaction' : row.kind === 'container' ? 'Control flow' : row.kind === 'transaction' ? 'Transaction' : 'Page / step',
            startMs: null,
            durationMs: null,
            status: worseStatus(aggregateStatus(mine), statusOfRow(row), evidenceDropped),
            phases: [],
            children: mine,
            payload: { transaction: row, aggregate: true },
          },
        }
      })
    }
    const iter = row.executions === 0 ? null : (iters[0] ?? (ownPos != null ? iterKeys[ownPos] : iterationForRow(row)))
    return [{
      iter,
      node: {
        id: row.key,
        kind: kindOfRow(row),
        label: row.name,
        detail: row.kind === 'subtransaction' ? 'Subtransaction' : row.kind === 'container' ? 'Control flow' : row.kind === 'transaction' ? 'Transaction' : 'Page / step',
        ...rowTiming(row),
        status: statusOfRow(row),
        phases: [],
        children: children.map(b => b.node),
        payload: { transaction: row, position: ownPos, eventIndex: row.eventIndex },
      },
    }]
  }

  const groups = new Map<string, DebugWaterfallNode[]>()
  const push = (key: string, node: DebugWaterfallNode) => groups.set(key, [...(groups.get(key) || []), node])
  for (const row of input.transactions || []) for (const b of fromRow(row)) push(b.iter ?? (row.executions > 0 ? executedFallbackKey(row) : b.node.status === 'incomplete' ? 'not_observed' : 'not_run'), b.node)
  for (const list of groups.values()) list.forEach(finalizeTiming)
  // A transaction row's own start/end marker events nest under that transaction.
  // The engine closes transactions left open at the end with a "finalize"
  // marker named after the transaction; it belongs to the latest one.
  const closedTransaction = (name: string): DebugWaterfallNode | undefined => {
    // Only observed executions can be closed. Loop executions are displayed as
    // "Iteration k", so match on the transaction row's own name, not the label.
    const isInstance = (n: DebugWaterfallNode) => {
      const tx = n.payload?.transaction
      return Boolean(tx && !n.payload?.aggregate && tx.name === name && tx.executions > 0 && (tx.kind === 'transaction' || tx.kind === 'subtransaction'))
    }
    // Outermost match wins: an HTTP sequence often shares its marker transaction's name.
    const outermost = (nodes: readonly DebugWaterfallNode[]): DebugWaterfallNode[] => nodes.flatMap(n => (isInstance(n) ? [n] : outermost(n.children)))
    const hits = [...groups]
      .filter(([k]) => k !== 'not_run' && k !== 'not_observed')
      .sort((a, b) => iterationOrder(a[0]) - iterationOrder(b[0]))
      .flatMap(([, list]) => outermost(list))
    // The engine closes a transaction left open: prefer the latest one with no end.
    const open = hits.filter(n => n.payload?.transaction?.durationMs == null)
    return open[open.length - 1] ?? hits[hits.length - 1]
  }
  const ownerByPosition = new Map<number, DebugWaterfallNode>()
  for (const list of groups.values()) walk(list, n => {
    const p = n.payload?.position
    if (n.payload?.transaction && p != null && !used.has(p) && !ownerByPosition.has(p)) ownerByPosition.set(p, n)
  })
  // A completed row records only its end marker, but its startedAt is exactly
  // its start marker's started_at, so back-to-back starts (Outer, then Inner a
  // few microseconds later) each go to their own transaction. Outer rows come
  // first in this preorder list, matching the order the starts ran in.
  const startOwners = new Map<string, DebugWaterfallNode[]>()
  for (const list of groups.values()) walk(list, n => {
    const tx = n.payload?.transaction
    // Any observed execution, including loop executions shown as "Iteration k".
    if (tx?.startedAt && !n.payload?.aggregate && (tx.kind === 'transaction' || tx.kind === 'subtransaction')) startOwners.set(tx.startedAt, [...(startOwners.get(tx.startedAt) || []), n])
  })
  // An open transaction already owns its start marker by position; it must not
  // also wait in this queue for a different start marker with the same time.
  for (const [at, list] of startOwners) {
    startOwners.set(at, list.filter(n => {
      const p = n.payload?.position
      return p == null || ownerByPosition.get(p) !== n || timeline.rows[p]?.event.started_at !== at
    }))
  }
  const startOwnerFor = (ev: BrowserReplayEvent) => {
    const list = ev.started_at ? startOwners.get(ev.started_at) : undefined
    if (!list?.length) return undefined
    // Prefer the transaction the marker names ("Start_Checkout_transaction"), else the outermost.
    const named = list.findIndex(n => {
      const name = n.payload?.transaction?.name
      return Boolean(name && ev.action_name?.includes(name))
    })
    return list.splice(named >= 0 ? named : 0, 1)[0]
  }

  const widened: DebugWaterfallNode[] = []
  // Container events (the dock passes them; the summary drops them) become
  // containers that adopt the events inside their span, never leaf work that
  // would double-count their children's time. Outer (longer) ones first.
  const containerPositions = timeline.rows
    .map((tr, p) => ({ tr, p }))
    .filter(({ tr, p }) => !used.has(p) && !ownerByPosition.has(p) && isContainerEvent(tr.event))
    .sort((a, b) => b.tr.durationMs - a.tr.durationMs || a.p - b.p)
  for (const { tr, p } of containerPositions) {
    used.add(p)
    const key = iterKeys[p]
    const node: DebugWaterfallNode = {
      id: `ctr:${tr.id}:${p}`,
      kind: 'container',
      label: tr.actionName || tr.location,
      detail: `${tr.location} · ${tr.event.action_type.replace(/_/g, ' ')}`,
      startMs: tr.startOffsetMs,
      durationMs: tr.durationMs,
      status: tr.success ? 'passed' : 'failed',
      phases: [],
      errorMessage: tr.errorMessage,
      children: [],
      payload: { position: p, eventIndex: indices[p] },
    }
    const parent = deepestContainer(groups.get(key) || [], tr.startOffsetMs, tr.startOffsetMs + tr.durationMs)
    if (parent) parent.children.push(node)
    else push(key, node)
  }
  // Events no transaction row claimed: markers, think time, untagged requests.
  const groupBySource = !input.transactions?.length
  let lastSynthetic: { key: string; name: string; node: DebugWaterfallNode; container: DebugWaterfallNode | null } | null = null
  timeline.rows.forEach((tr, p) => {
    if (used.has(p)) return
    const key = iterKeys[p]
    const node = leafNode(tr, p, indices[p])
    const source = groupBySource ? tr.event.source_transaction?.trim() : ''
    if (source) {
      // Tagged requests stay inside the container event they ran in; a
      // container named like the tag (an HTTP sequence) is the transaction.
      const container = isTimed(node) ? deepestContainer(groups.get(key) || [], node.startMs, node.startMs + node.durationMs) : null
      if (container && container.label === source) {
        container.children.push(node)
        lastSynthetic = null
        return
      }
      if (lastSynthetic && lastSynthetic.key === key && lastSynthetic.name === source && lastSynthetic.container === container) {
        lastSynthetic.node.children.push(node)
        return
      }
      const parent: DebugWaterfallNode = { id: `src:${key}:${p}:${source}`, kind: 'transaction', label: source, detail: 'Transaction', startMs: null, durationMs: null, status: 'passed', phases: [], children: [node], payload: {} }
      lastSynthetic = { key, name: source, node: parent, container }
      if (container) container.children.push(parent)
      else push(key, parent)
      return
    }
    lastSynthetic = null
    const owner =
      ownerByPosition.get(p) ??
      (node.kind === 'marker' ? (key.startsWith('phase:') ? closedTransaction(tr.event.action_name) : startOwnerFor(tr.event)) : undefined)
    if (owner) {
      owner.children.push(node)
      // A closing marker carries the engine's measured transaction time.
      if (node.kind === 'marker' && isTimed(node) && node.durationMs > 0 && owner.payload?.transaction?.durationMs == null) {
        const start = isTimed(owner) ? Math.min(owner.startMs, node.startMs) : node.startMs
        const end = Math.max(isTimed(owner) ? owner.startMs + owner.durationMs : 0, node.startMs + node.durationMs)
        owner.startMs = start
        owner.durationMs = end - start
        widened.push(owner)
      }
      return
    }
    const container = isTimed(node) ? deepestContainer(groups.get(key) || [], node.startMs, node.startMs + node.durationMs) : null
    if (container) container.children.push(node)
    else push(key, node)
  })

  const actionIterations = new Set(iterKeys.filter(k => k.startsWith('it:'))).size
  const roots: DebugWaterfallNode[] = [...groups]
    .sort((a, b) => iterationOrder(a[0]) - iterationOrder(b[0]))
    .map(([key, children]) => ({
      id: `iteration:${key}`,
      kind: 'iteration',
      label: iterationLabel(key, actionIterations),
      detail: key === 'not_run' ? 'Defined transactions that did not run' : key === 'not_observed' ? 'Evidence for these transactions was dropped' : undefined,
      startMs: null,
      durationMs: null,
      status: key === 'not_run' ? 'not_run' : aggregateStatus(children),
      phases: [],
      children,
      payload: {},
    }))
  // A closing marker widened its transaction after parents were timed: widen
  // the derived ancestors (aggregates, iterations) that no longer cover it.
  if (widened.length) {
    const parentOf = new Map<DebugWaterfallNode, DebugWaterfallNode>()
    walk(roots, (n, parent) => {
      if (parent) parentOf.set(n, parent)
    })
    for (const child of widened) {
      for (let a = parentOf.get(child), c = child; a; c = a, a = parentOf.get(a)) {
        if (!isTimed(a) || !isTimed(c) || (a.payload?.transaction?.durationMs != null && !a.payload?.aggregate)) break
        const start = Math.min(a.startMs, c.startMs)
        const end = Math.max(a.startMs + a.durationMs, c.startMs + c.durationMs)
        a.startMs = start
        a.durationMs = end - start
      }
    }
  }
  roots.forEach(mergeSameNamedChains)
  roots.forEach(finalizeTiming)
  roots.forEach(r => sortAndSettle(r, evidenceDropped))

  let spanMs = timeline.sessionSpanMs
  walk(roots, n => {
    if (isTimed(n)) spanMs = Math.max(spanMs, n.startMs + n.durationMs)
  })
  return { roots, spanMs, timeline }
}
