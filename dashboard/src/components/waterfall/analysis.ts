import { WATERFALL_PHASE_BY_KEY, formatWaterfallMs, type WaterfallPhaseKey } from './phases'
import type { WaterfallFlatRow, WaterfallNode, WaterfallSortMode } from './types'

const EPSILON_MS = 0.05

/** Children for timing analysis: the unfiltered list when a filter trimmed the node. */
export const timingChildren = <T,>(n: WaterfallNode<T>): WaterfallNode<T>[] => n.sourceChildren ?? n.children

export const isTimed = <T,>(n: WaterfallNode<T>): n is WaterfallNode<T> & { startMs: number; durationMs: number } =>
  n.startMs != null && n.durationMs != null && Number.isFinite(n.startMs) && Number.isFinite(n.durationMs)

/** Timed children that represent work: transaction markers are bookkeeping. */
const isTimedWork = <T,>(n: WaterfallNode<T>): n is WaterfallNode<T> & { startMs: number; durationMs: number } => isTimed(n) && n.kind !== 'marker'

export const nodeEndMs = (n: WaterfallNode<unknown>) => (isTimed(n) ? n.startMs + Math.max(0, n.durationMs) : null)

// A page/step row with child requests is a group, not work of its own (requests keep their hops).
const isLeafWork = (n: WaterfallNode<unknown>) => n.kind === 'request' || (n.kind === 'step' && n.children.length === 0)

/** One execution that can fail: hops belong to their request and are not counted again. */
export const isFailureCandidate = (n: WaterfallNode<unknown>) => n.status === 'failed' && (isLeafWork(n) || n.kind === 'think' || n.kind === 'marker')

export function walk<T>(nodes: readonly WaterfallNode<T>[], visit: (node: WaterfallNode<T>, parent: WaterfallNode<T> | null, depth: number) => void, parent: WaterfallNode<T> | null = null, depth = 0) {
  for (const n of nodes) {
    visit(n, parent, depth)
    walk(n.children, visit, n, depth + 1)
  }
}

export function findNode<T>(nodes: readonly WaterfallNode<T>[], id: string): WaterfallNode<T> | null {
  for (const n of nodes) {
    if (n.id === id) return n
    const hit = findNode(n.children, id)
    if (hit) return hit
  }
  return null
}

/** Ancestor ids from the root down to (not including) `id`; null when absent. */
export function ancestorIds(nodes: readonly WaterfallNode<unknown>[], id: string, trail: string[] = []): string[] | null {
  for (const n of nodes) {
    if (n.id === id) return trail
    const hit = ancestorIds(n.children, id, [...trail, n.id])
    if (hit) return hit
  }
  return null
}

/** Per-phase totals over a leaf's spans or, for parents, all leaf descendants. */
export function phaseTotals(node: WaterfallNode<unknown>): Map<WaterfallPhaseKey, number> {
  const totals = new Map<WaterfallPhaseKey, number>()
  const add = (n: WaterfallNode<unknown>) => {
    // Think time and transaction markers are not request phases.
    if (n.kind === 'think' || n.kind === 'marker') return
    if (timingChildren(n).length === 0 || (n.kind === 'request' && n.phases.length > 0)) {
      for (const p of n.phases) totals.set(p.key, (totals.get(p.key) ?? 0) + Math.max(0, p.endMs - p.startMs))
      return
    }
    timingChildren(n).forEach(add)
  }
  add(node)
  return totals
}

export interface DominantPhase {
  key: WaterfallPhaseKey
  ms: number
  /** Share of the node's measured phase time, 0..1. */
  share: number
}

export function dominantPhase(node: WaterfallNode<unknown>): DominantPhase | null {
  const totals = phaseTotals(node)
  let best: DominantPhase | null = null
  let sum = 0
  for (const v of totals.values()) sum += v
  for (const [key, ms] of totals) {
    if (ms > 0 && (!best || ms > best.ms)) best = { key, ms, share: 0 }
  }
  if (!best) return null
  best.share = sum > 0 ? best.ms / sum : 0
  return best
}

export interface SelfChildTime {
  /** Elapsed time covered by at least one child. */
  childMs: number
  /** Elapsed time with no child request running (think time, pacing, client work). */
  selfMs: number
  /** Extra counts from children that overlap each other. */
  overlapMs: number
  childSumMs: number
}

/** Merge timed child spans clipped to the parent and split elapsed into self vs child time. */
export function selfChildTime(node: WaterfallNode<unknown>): SelfChildTime | null {
  const kids = timingChildren(node)
  if (!isTimed(node) || kids.length === 0) return null
  const start = node.startMs
  const end = start + node.durationMs
  // Think time is time outside child work, so it counts as self time.
  const spans = kids
    .filter(isTimedWork)
    .filter(c => c.kind !== 'think')
    .map(c => ({ s: Math.max(start, c.startMs), e: Math.min(end, c.startMs + c.durationMs) }))
    .sort((a, b) => a.s - b.s)
  let covered = 0
  let sum = 0
  let cursor = start
  for (const sp of spans) {
    // Clipped to the parent, so a child running past its end is not "overlap".
    sum += Math.max(0, sp.e - sp.s)
    const from = Math.max(sp.s, cursor)
    if (sp.e > from) covered += sp.e - from
    cursor = Math.max(cursor, sp.e)
  }
  return { childMs: covered, selfMs: Math.max(0, node.durationMs - covered), overlapMs: Math.max(0, sum - covered), childSumMs: sum }
}

export interface WaterfallGap {
  startMs: number
  durationMs: number
}

/** Intervals inside a parent's span with no child running. */
export function childGaps(node: WaterfallNode<unknown>, minMs = EPSILON_MS): WaterfallGap[] {
  const kids = timingChildren(node)
  if (!isTimed(node) || kids.length === 0) return []
  const end = node.startMs + node.durationMs
  const spans = kids.filter(isTimedWork).sort((a, b) => a.startMs - b.startMs)
  const gaps: WaterfallGap[] = []
  let cursor = node.startMs
  for (const c of spans) {
    if (c.startMs - cursor > minMs) gaps.push({ startMs: cursor, durationMs: c.startMs - cursor })
    cursor = Math.max(cursor, c.startMs + c.durationMs)
  }
  if (end - cursor > minMs && spans.length > 0) gaps.push({ startMs: cursor, durationMs: end - cursor })
  return gaps
}

/**
 * Critical path: the chain of nodes that sets each parent's end time. Starting
 * from the child that ends last, repeatedly step to the child that finished
 * latest before the current one started. Recurses into every node on the path.
 */
export function criticalPathIds(nodes: readonly WaterfallNode<unknown>[]): Set<string> {
  const out = new Set<string>()
  const visit = (siblings: readonly WaterfallNode<unknown>[]) => {
    // Markers are bookkeeping (a closing marker spans its whole transaction), not work.
    const timed = siblings.filter(isTimedWork)
    if (!timed.length) return
    const end = (n: (typeof timed)[number]) => n.startMs + n.durationMs
    // Sorted by end time so each predecessor is a binary search (O(n log n) per level).
    const byEnd = timed.map((n, i) => ({ n, i, e: end(n) })).sort((x, y) => x.e - y.e || x.i - y.i)
    let cur = timed.reduce((best, n) => (end(n) > end(best) + EPSILON_MS || (Math.abs(end(n) - end(best)) <= EPSILON_MS && n.startMs > best.startMs) ? n : best))
    const chain = [cur]
    const inChain = new Set([cur])
    while (chain.length < timed.length) {
      const limit = cur.startMs + EPSILON_MS
      let lo = 0
      let hi = byEnd.length - 1
      let at = -1
      while (lo <= hi) {
        const mid = (lo + hi) >> 1
        if (byEnd[mid].e <= limit) {
          at = mid
          lo = mid + 1
        } else hi = mid - 1
      }
      while (at >= 0 && inChain.has(byEnd[at].n)) at--
      if (at < 0) break
      cur = byEnd[at].n
      chain.push(cur)
      inChain.add(cur)
    }
    for (const n of chain) {
      out.add(n.id)
      // The unfiltered children: a hidden sibling can still set the end time.
      visit(timingChildren(n))
    }
  }
  visit(nodes)
  return out
}

function compareChronological(a: WaterfallNode<unknown>, b: WaterfallNode<unknown>) {
  if (a.startMs == null && b.startMs == null) return 0
  if (a.startMs == null) return 1
  if (b.startMs == null) return -1
  return a.startMs - b.startMs
}

export function sortSiblings<T>(nodes: readonly WaterfallNode<T>[], mode: WaterfallSortMode): WaterfallNode<T>[] {
  const indexed = nodes.map((n, i) => ({ n, i }))
  const dur = (n: WaterfallNode<T>) => n.durationMs ?? -1
  indexed.sort((x, y) => {
    let c = 0
    if (mode === 'slowest') c = dur(y.n) - dur(x.n)
    else if (mode === 'dominant') {
      const dx = dominantPhase(x.n)
      const dy = dominantPhase(y.n)
      const kx = dx ? WATERFALL_PHASE_BY_KEY[dx.key].label : '￿'
      const ky = dy ? WATERFALL_PHASE_BY_KEY[dy.key].label : '￿'
      // Group by dominant phase, heaviest phase time first within a group.
      c = kx === ky ? (dy?.ms ?? 0) - (dx?.ms ?? 0) : kx.localeCompare(ky)
    } else c = compareChronological(x.n, y.n)
    return c || x.i - y.i
  })
  return indexed.map(x => x.n)
}

/** Iterations keep script order under every sort; sorting applies below them. */
function sortLevel<T>(nodes: readonly WaterfallNode<T>[], mode: WaterfallSortMode, depth: number): WaterfallNode<T>[] {
  if (depth === 0 && nodes.every(n => n.kind === 'iteration')) return nodes.slice()
  return sortSiblings(nodes, mode)
}

export function flattenVisible<T>(roots: readonly WaterfallNode<T>[], expanded: ReadonlySet<string>, mode: WaterfallSortMode = 'chronological'): WaterfallFlatRow<T>[] {
  const out: WaterfallFlatRow<T>[] = []
  const visit = (nodes: readonly WaterfallNode<T>[], depth: number, parentId: string | null) => {
    const sorted = sortLevel(nodes, mode, depth)
    sorted.forEach((node, i) => {
      const hasChildren = node.children.length > 0
      const isOpen = hasChildren && expanded.has(node.id)
      out.push({ node, depth, parentId, hasChildren, expanded: isOpen, posInSet: i + 1, setSize: sorted.length })
      if (isOpen) visit(node.children, depth + 1, node.id)
    })
  }
  visit(roots, 0, null)
  return out
}

/** All nodes in display order regardless of expansion (for failure stepping). */
export function flattenAll<T>(roots: readonly WaterfallNode<T>[], mode: WaterfallSortMode = 'chronological'): WaterfallNode<T>[] {
  const out: WaterfallNode<T>[] = []
  const visit = (nodes: readonly WaterfallNode<T>[], depth: number) => {
    for (const n of sortLevel(nodes, mode, depth)) {
      out.push(n)
      visit(n.children, depth + 1)
    }
  }
  visit(roots, 0)
  return out
}

/** Keep nodes that match, plus ancestors of matches. Children of a match are kept. */
export function filterTree<T>(nodes: readonly WaterfallNode<T>[], match: (n: WaterfallNode<T>) => boolean): WaterfallNode<T>[] {
  const out: WaterfallNode<T>[] = []
  for (const n of nodes) {
    if (match(n)) {
      out.push(n)
      continue
    }
    const kids = filterTree(n.children, match)
    if (kids.length) out.push({ ...n, children: kids, sourceChildren: n.sourceChildren ?? n.children })
  }
  return out
}

export interface WaterfallTimeSink {
  id: string
  label: string
  detail: string
  /** Node to select when the chip is activated. */
  targetId?: string
  tone: 'neutral' | 'warning' | 'danger'
}

/**
 * Headline facts for the "Top time sinks" strip: the few slowest requests and
 * their share of elapsed time, the heaviest phase overall, TLS cost on new
 * connections, think time, and failures.
 */
export function topTimeSinks(roots: readonly WaterfallNode<unknown>[], elapsedMs: number, topN = 3): WaterfallTimeSink[] {
  const leaves: WaterfallNode<unknown>[] = []
  let thinkMs = 0
  let failed = 0
  let firstFailure: string | undefined
  walk(roots, n => {
    if (n.kind === 'think' && isTimed(n)) thinkMs += n.durationMs
    if (isLeafWork(n) && isTimed(n)) leaves.push(n)
    if (isFailureCandidate(n)) {
      failed++
      firstFailure ??= n.id
    }
  })
  const sinks: WaterfallTimeSink[] = []
  const elapsed = elapsedMs > 0 ? elapsedMs : leaves.reduce((s, n) => s + (n.durationMs ?? 0), 0)
  const slow = [...leaves].sort((a, b) => (b.durationMs ?? 0) - (a.durationMs ?? 0)).slice(0, topN).filter(n => (n.durationMs ?? 0) > 0)
  if (slow.length && elapsed > 0) {
    const sum = slow.reduce((s, n) => s + (n.durationMs ?? 0), 0)
    const pct = Math.round((Math.min(sum, elapsed) / elapsed) * 100)
    sinks.push({
      id: 'slowest',
      label: `${slow.length} ${slow.length === 1 ? 'request' : 'requests'} = ${pct}% of elapsed`,
      detail: slow.map(n => `${n.label} ${formatWaterfallMs(n.durationMs ?? 0)}`).join(' · '),
      targetId: slow[0].id,
      tone: pct >= 50 ? 'warning' : 'neutral',
    })
  }
  const totals = new Map<WaterfallPhaseKey, number>()
  let phasedLeaves = 0
  let tlsMs = 0
  let tlsCount = 0
  let tlsTarget: string | undefined
  let tlsTargetMs = 0
  for (const leaf of leaves) {
    const t = phaseTotals(leaf)
    for (const [k, v] of t) totals.set(k, (totals.get(k) ?? 0) + v)
    if ([...t].some(([k, v]) => v > 0 && k !== 'total' && k !== 'queueing')) phasedLeaves++
    const tls = t.get('tls') ?? 0
    if (tls > 0) {
      tlsMs += tls
      tlsCount++
      if (tls > tlsTargetMs) {
        tlsTargetMs = tls
        tlsTarget = leaf.id
      }
    }
  }
  const measured = [...totals].filter(([k]) => k !== 'total' && k !== 'queueing')
  const measuredSum = measured.reduce((s, [, v]) => s + v, 0)
  const heavy = measured.sort((a, b) => b[1] - a[1])[0]
  if (heavy && measuredSum > 0 && heavy[1] > 0) {
    sinks.push({
      id: 'dominant-phase',
      label: `${WATERFALL_PHASE_BY_KEY[heavy[0]].shortLabel} = ${Math.round((heavy[1] / measuredSum) * 100)}% of measured phase time`,
      detail: `${formatWaterfallMs(heavy[1])} across ${phasedLeaves} ${phasedLeaves === 1 ? 'request' : 'requests'} with phase timings`,
      tone: 'neutral',
    })
  }
  if (tlsCount > 0) {
    sinks.push({
      id: 'tls',
      label: `TLS on ${tlsCount} new ${tlsCount === 1 ? 'connection' : 'connections'} = ${formatWaterfallMs(tlsMs)}`,
      detail: 'Handshakes on fresh connections; reused connections skip this cost.',
      targetId: tlsTarget,
      tone: 'neutral',
    })
  }
  if (thinkMs > 0 && elapsed > 0) {
    sinks.push({ id: 'think', label: `Think time = ${formatWaterfallMs(thinkMs)} (${Math.round((thinkMs / elapsed) * 100)}%)`, detail: 'Scripted pauses between requests.', tone: 'neutral' })
  }
  if (failed > 0) {
    sinks.push({ id: 'failures', label: `${failed} ${failed === 1 ? 'failure' : 'failures'}`, detail: 'Press ] to step to the next failure, [ for the previous one.', targetId: firstFailure, tone: 'danger' })
  }
  return sinks
}

/** Earliest start and latest end across all timed nodes. */
export function treeExtent(roots: readonly WaterfallNode<unknown>[]): { startMs: number; endMs: number } | null {
  let s = Infinity
  let e = -Infinity
  walk(roots, n => {
    if (!isTimed(n)) return
    s = Math.min(s, n.startMs)
    e = Math.max(e, n.startMs + n.durationMs)
  })
  return Number.isFinite(s) && Number.isFinite(e) ? { startMs: s, endMs: e } : null
}

// Open what ran; subtrees that never ran stay collapsed so they do not bury the run.
export const defaultWaterfallExpanded = (node: WaterfallNode<unknown>): boolean => (node.kind === 'iteration' || node.kind === 'transaction' || node.kind === 'container') && node.status !== 'not_run'
