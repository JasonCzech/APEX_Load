/**
 * Debug-run diff (plan D7): align two retained debug runs action by action and
 * explain each change in time by phase, e.g. "login: +310 ms, all in server".
 */
import type { BrowserReplayEvent } from '../../types/replay'
import { debugTransactionRows, type DebugTransactionRow } from '../debugTransactionModel'
import type { BrowserReplayReport } from '../../types/replay'
import type { DebugRunRecord } from '../debuggerExperience'
import type { DebugCompletionSnapshot } from '../debugCompletionModel'
import { formatDateTime } from '../../utils/dateTime'
import { eventIdentity } from '../debuggerExperience'
import { isTimed, walk, type WaterfallTimeSink } from './analysis'
import { buildDebugTimelineModel, type DebugTimelineModel, type DebugTimelineRowModel } from './debugEventModel'
import { buildDebugWaterfallTree, isContainerEvent, isTransactionMarkerEvent, scriptIterationKeys, type DebugWaterfallNode } from './debugTree'
import { formatWaterfallMs, type WaterfallPhaseKey } from './phases'
import type { WaterfallPhaseSpan } from './types'

/** Plain words for verdicts, in the load-run vocabulary (wait = server, receive = download). */
const PHASE_WORDS: Record<WaterfallPhaseKey, string> = {
  queueing: 'queueing',
  dns: 'DNS',
  connect: 'connect',
  tls: 'TLS',
  send: 'send',
  wait: 'server',
  receive: 'download',
  // In a diff, 'total' carries the time no measured phase explains.
  total: 'time outside measured phases',
}

const NO_CHANGE_MS = 0.5

export type RunDiffOutcome = 'same' | 'now_failing' | 'now_passing' | 'added' | 'removed'

export interface RunDiffEntry {
  /** Action identity plus occurrence, the same alignment compareRuns uses. */
  key: string
  basePosition?: number
  currentPosition?: number
  base?: DebugTimelineRowModel
  current?: DebugTimelineRowModel
  /** current − baseline; a missing side counts as zero. */
  deltaMs: number
  /** Per-phase current − baseline, only when both runs measured phases. */
  phaseDeltas: Partial<Record<WaterfallPhaseKey, number>>
  phasesComparable: boolean
  /** Why phases cannot be compared, when they cannot. */
  phasesReason?: 'total only' | 'timing sources differ'
  dominant?: { key: WaterfallPhaseKey; ms: number; share: number }
  outcome: RunDiffOutcome
  summary: string
}

export interface RunDiff {
  entries: RunDiffEntry[]
  byCurrentPosition: Map<number, RunDiffEntry>
  onlyInBaseline: RunDiffEntry[]
  base: DebugTimelineModel
  current: DebugTimelineModel
}

/** Events that are bookkeeping or wrap other events carry no time of their own to compare. */
const comparable = (ev: BrowserReplayEvent) => !isTransactionMarkerEvent(ev) && !isContainerEvent(ev)

export function formatDeltaMs(ms: number): string {
  if (Math.abs(ms) < NO_CHANGE_MS) return '±0 ms'
  return `${ms > 0 ? '+' : '−'}${formatWaterfallMs(Math.abs(ms))}`
}

function phaseTotalsOf(row: DebugTimelineRowModel): Map<WaterfallPhaseKey, number> {
  const totals = new Map<WaterfallPhaseKey, number>()
  for (const s of row.segments) totals.set(s.key, (totals.get(s.key) ?? 0) + s.durationMs)
  return totals
}

const hasPhases = (t: Map<WaterfallPhaseKey, number>) => [...t].some(([k, v]) => k !== 'total' && v > 0)

/** Phase whose change explains most of the total change, in the same direction. */
export function dominantPhaseDelta(deltaMs: number, phaseDeltas: Partial<Record<WaterfallPhaseKey, number>>): RunDiffEntry['dominant'] {
  if (Math.abs(deltaMs) < NO_CHANGE_MS) return undefined
  let best: RunDiffEntry['dominant']
  for (const [key, ms] of Object.entries(phaseDeltas) as Array<[WaterfallPhaseKey, number]>) {
    if (Math.sign(ms) !== Math.sign(deltaMs)) continue
    if (!best || Math.abs(ms) > Math.abs(best.ms)) best = { key, ms, share: ms / deltaMs }
  }
  return best
}

/** "+310 ms, all in server", "−42 ms, mostly TLS (64%)", "+5 ms, spread across phases". */
export function deltaVerdict(deltaMs: number, phaseDeltas: Partial<Record<WaterfallPhaseKey, number>>, phasesComparable: boolean, reason: string = 'total only'): string {
  if (Math.abs(deltaMs) < NO_CHANGE_MS) return 'No change'
  const head = formatDeltaMs(deltaMs)
  if (!phasesComparable) return `${head} (${reason})`
  const dom = dominantPhaseDelta(deltaMs, phaseDeltas)
  if (!dom) return `${head}, spread across phases`
  if (dom.share >= 0.9) return `${head}, all in ${PHASE_WORDS[dom.key]}`
  if (dom.share >= 0.5) return `${head}, mostly ${PHASE_WORDS[dom.key]} (${Math.round(dom.share * 100)}%)`
  return `${head}, spread across phases (largest: ${PHASE_WORDS[dom.key]})`
}

function compare(key: string, base: DebugTimelineRowModel | undefined, current: DebugTimelineRowModel | undefined, basePosition?: number, currentPosition?: number): RunDiffEntry {
  const bt = base ? phaseTotalsOf(base) : new Map<WaterfallPhaseKey, number>()
  const ct = current ? phaseTotalsOf(current) : new Map<WaterfallPhaseKey, number>()
  const deltaMs = (current?.durationMs ?? 0) - (base?.durationMs ?? 0)
  // Phases from different sources (HAR vs trace timings) are different
  // measurements: comparing them would invent per-phase changes.
  const bothPhased = Boolean(base && current && hasPhases(bt) && hasPhases(ct))
  const sameSource = Boolean(base && current && base.timingSource === current.timingSource)
  const phasesComparable = bothPhased && sameSource
  const phasesReason: RunDiffEntry['phasesReason'] = !base || !current || phasesComparable ? undefined : bothPhased ? 'timing sources differ' : 'total only'
  const phaseDeltas: Partial<Record<WaterfallPhaseKey, number>> = {}
  if (phasesComparable) {
    let measuredDelta = 0
    for (const k of new Set([...bt.keys(), ...ct.keys()])) {
      if (k === 'total') continue
      phaseDeltas[k] = (ct.get(k) ?? 0) - (bt.get(k) ?? 0)
      measuredDelta += phaseDeltas[k]!
    }
    // HAR phases often cover only part of an action (client work, redirects,
    // pauses): keep the unexplained remainder as its own bucket so the verdict
    // never credits a small phase change with a large total change.
    const unmeasured = deltaMs - measuredDelta
    if (Math.abs(unmeasured) >= NO_CHANGE_MS) phaseDeltas.total = unmeasured
  }
  const outcome: RunDiffOutcome = !base ? 'added' : !current ? 'removed' : base.success && !current.success ? 'now_failing' : !base.success && current.success ? 'now_passing' : 'same'
  let summary: string
  if (outcome === 'added') summary = `New in this run (${formatWaterfallMs(current!.durationMs)})`
  else if (outcome === 'removed') summary = `Only in baseline (${formatWaterfallMs(base!.durationMs)})`
  else {
    summary = deltaVerdict(deltaMs, phaseDeltas, phasesComparable, phasesReason)
    if (outcome === 'now_failing') summary += ' · now failing'
    if (outcome === 'now_passing') summary += ' · now passing'
  }
  return { key, basePosition, currentPosition, base, current, deltaMs, phaseDeltas, phasesComparable, phasesReason, dominant: dominantPhaseDelta(deltaMs, phaseDeltas), outcome, summary }
}

/** Align two runs by action identity and occurrence (the n-th execution of the same step). */
export function diffDebugRuns(baseEvents: readonly BrowserReplayEvent[], currentEvents: readonly BrowserReplayEvent[]): RunDiff {
  const base = buildDebugTimelineModel(baseEvents)
  const current = buildDebugTimelineModel(currentEvents)
  // Key = script iteration + action identity + occurrence within that
  // iteration, so one iteration that ran differently does not shift the ones
  // after it. A re-delivered event (same identity) is counted once.
  const index = (model: DebugTimelineModel, events: readonly BrowserReplayEvent[]) => {
    const iterations = scriptIterationKeys(events)
    const counts = new Map<string, number>()
    const seen = new Set<string>()
    const out = new Map<string, number>()
    model.rows.forEach((row, p) => {
      if (!comparable(row.event)) return
      const ev = row.event
      const once = ev.event_id || `${ev.event_index}:${ev.phase}:${ev.action_index}:${ev.started_at}`
      if (seen.has(once)) return
      seen.add(once)
      const id = `${iterations[p]}|${eventIdentity(ev)}`
      const n = counts.get(id) ?? 0
      counts.set(id, n + 1)
      out.set(`${id}#${n}`, p)
    })
    return out
  }
  const left = index(base, baseEvents)
  const right = index(current, currentEvents)
  const entries: RunDiffEntry[] = []
  const byCurrentPosition = new Map<number, RunDiffEntry>()
  const onlyInBaseline: RunDiffEntry[] = []
  for (const [key, p] of right) {
    const bp = left.get(key)
    const entry = compare(key, bp != null ? base.rows[bp] : undefined, current.rows[p], bp, p)
    entries.push(entry)
    byCurrentPosition.set(p, entry)
  }
  for (const [key, bp] of left) {
    if (right.has(key)) continue
    const entry = compare(key, base.rows[bp], undefined, bp, undefined)
    entries.push(entry)
    onlyInBaseline.push(entry)
  }
  return { entries, byCurrentPosition, onlyInBaseline, base, current }
}

export interface DebugRunDiffInput {
  events: readonly BrowserReplayEvent[]
  transactions?: readonly DebugTransactionRow[] | null
  /** The run was stopped before it finished. */
  stopped?: boolean
}

export interface DebugRunDiffTree {
  roots: DebugWaterfallNode[]
  spanMs: number
  diff: RunDiff
  /** Node id for each comparable current-run position, for chips and selection. */
  nodeIdByCurrentPosition: Map<number, string>
  sinks: WaterfallTimeSink[]
}

const spansOf = (row: DebugTimelineRowModel): WaterfallPhaseSpan[] => row.segments.map(s => ({ key: s.key, startMs: s.startMs, endMs: s.endMs }))

/**
 * The current run's waterfall, each execution annotated with the baseline
 * execution it aligns to (a ghost bar) and the change; parents sum their
 * children's changes. Executions only the baseline had get their own group.
 */
export function buildDebugRunDiffTree(baseline: DebugRunDiffInput, current: DebugRunDiffInput): DebugRunDiffTree {
  const diff = diffDebugRuns(baseline.events, current.events)
  const tree = buildDebugWaterfallTree({ events: current.events, transactions: current.transactions })
  const nodeIdByCurrentPosition = new Map<number, string>()
  const leafEntry = new Map<DebugWaterfallNode, RunDiffEntry>()

  walk(tree.roots, n => {
    const p = n.payload?.position
    if (p == null || n.payload?.transaction || n.kind === 'hop' || n.kind === 'marker' || n.kind === 'container' || n.kind === 'iteration') return
    const entry = diff.byCurrentPosition.get(p)
    if (!entry) return
    nodeIdByCurrentPosition.set(p, n.id)
    leafEntry.set(n, entry)
    n.delta = { ms: entry.deltaMs, label: entry.summary, outcome: entry.outcome }
    if (entry.base) n.ghost = { durationMs: entry.base.durationMs, phases: spansOf(entry.base) }
  })

  // Parents (iterations, transactions, containers) align with the baseline's
  // by their path of labels and occurrence, so their headline change is their
  // own measured duration, not just the sum of their requests.
  const baseTree = buildDebugWaterfallTree({ events: baseline.events, transactions: baseline.transactions })
  const isParent = (n: DebugWaterfallNode) => n.kind === 'iteration' || n.kind === 'transaction' || n.kind === 'container'
  const keyParents = (nodes: readonly DebugWaterfallNode[], prefix: string, out: Map<string, DebugWaterfallNode>, keys?: Map<DebugWaterfallNode, string>) => {
    const counts = new Map<string, number>()
    for (const n of nodes) {
      if (!isParent(n)) continue
      const id = `${n.kind}:${n.label}`
      const occ = counts.get(id) ?? 0
      counts.set(id, occ + 1)
      const key = `${prefix}/${id}#${occ}`
      out.set(key, n)
      keys?.set(n, key)
      keyParents(n.children, key, out, keys)
    }
  }
  const baseParents = new Map<string, DebugWaterfallNode>()
  keyParents(baseTree.roots, '', baseParents)
  const currentParentKeys = new Map<DebugWaterfallNode, string>()
  keyParents(tree.roots, '', new Map(), currentParentKeys)

  // Roll-ups count matched executions only: a new or missing execution is a
  // difference in what ran, not time gained or lost, and is reported apart.
  type Roll = { ms: number; phases: Partial<Record<WaterfallPhaseKey, number>>; comparable: boolean; any: boolean }
  const rollUp = (n: DebugWaterfallNode): Roll => {
    const own = leafEntry.get(n)
    if (own) {
      const matched = own.outcome !== 'added' && own.outcome !== 'removed'
      return matched ? { ms: own.deltaMs, phases: own.phaseDeltas, comparable: own.phasesComparable, any: true } : { ms: 0, phases: {}, comparable: true, any: false }
    }
    let ms = 0
    let any = false
    let comparableAll = true
    const phases: Partial<Record<WaterfallPhaseKey, number>> = {}
    for (const c of n.children) {
      const r = rollUp(c)
      if (!r.any) continue
      any = true
      ms += r.ms
      comparableAll &&= r.comparable
      for (const [k, v] of Object.entries(r.phases) as Array<[WaterfallPhaseKey, number]>) phases[k] = (phases[k] ?? 0) + v
    }
    const key = currentParentKeys.get(n)
    const counterpart = key ? baseParents.get(key) : undefined
    if (isParent(n) && counterpart && isTimed(n) && isTimed(counterpart)) {
      // Own change; time its requests do not explain goes to the unmeasured bucket.
      const own = n.durationMs - counterpart.durationMs
      const explained = { ...phases, total: (phases.total ?? 0) + (own - ms) }
      n.delta = { ms: own, label: deltaVerdict(own, explained, comparableAll && any, any ? 'total only' : 'no matching requests') }
      n.ghost = { durationMs: counterpart.durationMs, phases: [] }
    } else if (any && n.kind !== 'hop') {
      n.delta = { ms, label: deltaVerdict(ms, phases, comparableAll) }
    }
    return { ms, phases, comparable: comparableAll, any }
  }
  tree.roots.forEach(rollUp)

  const roots = [...tree.roots]
  if (diff.onlyInBaseline.length) {
    const children: DebugWaterfallNode[] = diff.onlyInBaseline.map(e => ({
      id: `baseline-only:${e.key}`,
      kind: e.base!.statusCode != null || e.base!.timingSource !== 'duration' ? 'request' : 'step',
      label: e.base!.actionName || e.base!.location,
      detail: `${e.base!.location} · not executed in this run`,
      startMs: null,
      durationMs: null,
      status: 'not_run',
      phases: [],
      children: [],
      delta: { ms: e.deltaMs, label: e.summary, outcome: 'removed' },
      payload: {},
    }))
    roots.push({
      id: 'iteration:baseline-only',
      kind: 'iteration',
      label: 'Only in baseline',
      detail: 'Executions the baseline run had and this run did not',
      startMs: null,
      durationMs: null,
      status: 'not_run',
      phases: [],
      children,
      delta: { ms: children.reduce((s, c) => s + (c.delta?.ms ?? 0), 0), label: `${children.length} not executed in this run` },
      payload: {},
    })
  }

  // Ghosts can run past the current run's end (it got faster): keep them reachable.
  let spanMs = tree.spanMs
  walk(roots, n => {
    if (n.ghost && isTimed(n)) spanMs = Math.max(spanMs, n.startMs + n.ghost.durationMs)
  })
  return { roots, spanMs, diff, nodeIdByCurrentPosition, sinks: runDiffSinks(diff, nodeIdByCurrentPosition, { baseStopped: baseline.stopped, currentStopped: current.stopped }) }
}

/** Headline facts for the comparison: elapsed change, biggest regressions and improvement, outcome changes. */
export function runDiffSinks(diff: RunDiff, nodeIdByCurrentPosition: ReadonlyMap<number, string>, runs: { baseStopped?: boolean; currentStopped?: boolean } = {}): WaterfallTimeSink[] {
  const sinks: WaterfallTimeSink[] = []
  const baseMs = diff.base.sessionSpanMs
  const curMs = diff.current.sessionSpanMs
  const added = diff.entries.filter(e => e.outcome === 'added').length
  const removed = diff.onlyInBaseline.length
  // A run that stopped early or ran different steps is not "faster": its
  // elapsed time measures a different amount of work.
  const unlike = runs.baseStopped || runs.currentStopped || added > 0 || removed > 0
  if (unlike && (baseMs > 0 || curMs > 0)) {
    sinks.push({
      id: 'elapsed',
      label: `Elapsed ${formatWaterfallMs(baseMs)} → ${formatWaterfallMs(curMs)} (not like for like)`,
      detail: runs.baseStopped || runs.currentStopped ? 'A run was stopped before it finished, so total time is not comparable.' : `The runs executed different steps (${added} new, ${removed} only in baseline).`,
      tone: 'neutral',
    })
  } else if (baseMs > 0 || curMs > 0) {
    const d = curMs - baseMs
    const pct = baseMs > 0 ? Math.round((d / baseMs) * 100) : 0
    sinks.push({
      id: 'elapsed',
      label: Math.abs(d) < NO_CHANGE_MS ? 'Elapsed time unchanged' : `Run ${d > 0 ? 'slower' : 'faster'} by ${formatWaterfallMs(Math.abs(d))}${baseMs > 0 ? ` (${d > 0 ? '+' : '−'}${Math.abs(pct)}%)` : ''}`,
      detail: `${formatWaterfallMs(baseMs)} baseline → ${formatWaterfallMs(curMs)} this run`,
      tone: d > 0 && pct >= 10 ? 'warning' : 'neutral',
    })
  }
  const matched = diff.entries.filter(e => e.base && e.current)
  // Outcome changes have their own chips: a request that now fails fast is not an improvement.
  const sameOutcome = matched.filter(e => e.outcome === 'same')
  const label = (e: RunDiffEntry) => e.current?.actionName || e.base?.actionName || 'Execution'
  const target = (e: RunDiffEntry) => (e.currentPosition != null ? nodeIdByCurrentPosition.get(e.currentPosition) : undefined)
  const regressions = sameOutcome.filter(e => e.deltaMs >= 1).sort((a, b) => b.deltaMs - a.deltaMs).slice(0, 3)
  for (const e of regressions) sinks.push({ id: `slower:${e.key}`, label: `${label(e)}: ${e.summary}`, detail: `${formatWaterfallMs(e.base!.durationMs)} → ${formatWaterfallMs(e.current!.durationMs)}`, targetId: target(e), tone: 'warning' })
  const improvement = sameOutcome.filter(e => e.deltaMs <= -1).sort((a, b) => a.deltaMs - b.deltaMs)[0]
  if (improvement) sinks.push({ id: `faster:${improvement.key}`, label: `${label(improvement)}: ${improvement.summary}`, detail: `${formatWaterfallMs(improvement.base!.durationMs)} → ${formatWaterfallMs(improvement.current!.durationMs)}`, targetId: target(improvement), tone: 'neutral' })
  const nowFailing = matched.filter(e => e.outcome === 'now_failing')
  if (nowFailing.length) sinks.push({ id: 'now-failing', label: `${nowFailing.length} now failing`, detail: nowFailing.slice(0, 3).map(label).join(' · '), targetId: target(nowFailing[0]), tone: 'danger' })
  const nowPassing = matched.filter(e => e.outcome === 'now_passing')
  if (nowPassing.length) sinks.push({ id: 'now-passing', label: `${nowPassing.length} now passing`, detail: nowPassing.slice(0, 3).map(label).join(' · '), targetId: target(nowPassing[0]), tone: 'neutral' })
  if (added || removed) sinks.push({ id: 'membership', label: `${added} new · ${removed} only in baseline`, detail: 'Executions without a counterpart in the other run (different path, iteration count or failure point).', tone: 'neutral' })
  return sinks
}

/** True when any node carries a comparison. */
export function hasRunDiff(roots: readonly DebugWaterfallNode[]): boolean {
  let found = false
  walk(roots, n => {
    if (!found && n.delta) found = true
  })
  return found
}

export interface RunDiffOption {
  id: string
  label: string
  report: BrowserReplayReport
  record?: Pick<DebugRunRecord, 'transactions' | 'draft'>
}

export const CURRENT_RUN_ID = '__current__'

const runLabel = (report: BrowserReplayReport, prefix: string) =>
  `${prefix}${report.started_at ? formatDateTime(report.started_at, { milliseconds: false }) : 'Unknown start'} · ${report.stopped ? 'Stopped' : report.success ? 'Passed' : 'Failed'} · ${report.events.length} executions`

const sameRun = (a: BrowserReplayReport, b: BrowserReplayReport) =>
  a === b || (Boolean(a.replay_session_id) && a.replay_session_id === b.replay_session_id) || (a.started_at === b.started_at && a.events.length === b.events.length)

/** Retained runs plus the current run when it is not retained yet; one rule for the mount check and the panel. */
export function buildRunDiffOptions(runs: readonly DebugRunRecord[], current?: BrowserReplayReport | null, currentRecord?: Pick<DebugRunRecord, 'transactions' | 'draft'> | null): RunDiffOption[] {
  const list: RunDiffOption[] = runs.map((run, i) => ({ id: run.id, label: runLabel(run.report, i === 0 ? 'Latest · ' : ''), report: run.report, record: run }))
  if (current && !runs.some(r => sameRun(r.report, current))) {
    list.unshift({ id: CURRENT_RUN_ID, label: runLabel(current, 'This run · '), report: current, record: currentRecord ?? undefined })
  }
  return list
}

/** Transaction rows for a run, when its definitions were retained. */
export function runTransactionRows(report: BrowserReplayReport, record?: Pick<DebugRunRecord, 'transactions' | 'draft'>, id = 'run'): DebugTransactionRow[] | null {
  if (!record?.transactions) return null
  const snapshot = {
    key: id,
    scriptName: '',
    draft: record.draft,
    transactions: record.transactions,
    report,
    session: { id, events: report.events, dropped_event_count: report.dropped_event_count },
  } as unknown as DebugCompletionSnapshot
  return debugTransactionRows(snapshot)
}
