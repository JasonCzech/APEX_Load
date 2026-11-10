/**
 * Client-side retention of per-window live data the history points drop:
 * each window's trace exemplars (L3) and the first stable minute's
 * per-transaction phase averages (the L1 "under load" baseline). Pure and
 * bounded; the stream hook keeps one instance per run.
 */
import type { LiveMetrics, TraceExemplar, TxMetrics } from '../types'
import { txPhaseVector, type LivePhaseVector } from './livePhaseModel'

/** How long exemplar windows are kept. */
export const EXEMPLAR_RETENTION_MS = 15 * 60_000
/** Hard caps so a forward-version or runaway payload cannot grow memory. */
export const EXEMPLAR_MAX_WINDOWS = 1800
export const EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW = 200
const EXEMPLARS_PER_TRANSACTION = 3
/** A click resolves to the nearest retained window within this distance. */
export const EXEMPLAR_WINDOW_TOLERANCE_MS = 5_000

export const STABLE_MINUTE_MS = 60_000
/** Windows in a row without a VUser increase before the ramp counts as done. */
export const STABLE_WINDOWS_REQUIRED = 3
export const STABLE_MAX_TRANSACTIONS = 200
/** While collecting, VUsers may drift this share above the starting level. */
export const STABLE_RISE_TOLERANCE = 0.05

export interface ExemplarWindowTransaction {
  name: string
  exemplars: TraceExemplar[]
  /** The groups whose VUsers ran it in the window, when the source attributes them. */
  groups?: string[]
}

export interface ExemplarWindow {
  tsMs: number
  timestamp: string
  transactions: ExemplarWindowTransaction[]
  /**
   * Set when the window carried exemplars for more than
   * EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW transactions: `seen` had exemplars,
   * only the first `kept` (in wire order) are retained, so a transaction
   * missing here may well have had some.
   */
  truncated?: { seen: number; kept: number }
}

interface PhaseSums {
  weight: number
  count: number
  errors: number
  dns: number
  connect: number
  tls: number
  server: number
  download: number
  noResponse: number
  avgMs: number
}

export interface StableMinuteState {
  phase: 'waiting' | 'collecting' | 'ready'
  lastVusers?: number
  /** Windows in a row whose active VUsers did not rise. */
  steadyWindows: number
  startMs?: number
  endMs?: number
  vusers?: number
  windows: number
  sums: Record<string, PhaseSums>
  /**
   * Set once a transaction was left out because STABLE_MAX_TRANSACTIONS
   * names were already kept, so a row with no baseline may have run during
   * the minute.
   */
  truncated?: boolean
}

export interface LiveWindowRetention {
  exemplars: ExemplarWindow[]
  /** Earliest window this page received, so "not retained" can be told apart from "none". */
  firstWindowMs?: number
  stable: StableMinuteState
}

/**
 * Maps keyed by transaction name have no prototype, so a transaction named
 * "constructor" or "toString" cannot resolve to an inherited member.
 */
const nameMap = <V>(from?: Record<string, V>): Record<string, V> => Object.assign(Object.create(null) as Record<string, V>, from)
const hasName = (map: Record<string, unknown>, name: string) => Object.prototype.hasOwnProperty.call(map, name)

export const emptyStableMinute = (): StableMinuteState => ({ phase: 'waiting', steadyWindows: 0, windows: 0, sums: nameMap() })
export const emptyLiveWindowRetention = (): LiveWindowRetention => ({ exemplars: [], stable: emptyStableMinute() })

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

function windowExemplars(transactions: Record<string, TxMetrics> | undefined): { kept: ExemplarWindowTransaction[]; seen: number } {
  if (!transactions) return { kept: [], seen: 0 }
  const out: ExemplarWindowTransaction[] = []
  let seen = 0
  for (const [key, tx] of Object.entries(transactions)) {
    const exemplars = (tx?.exemplars ?? []).filter(e => e && typeof e.trace_id === 'string' && e.trace_id !== '').slice(0, EXEMPLARS_PER_TRANSACTION)
    if (!exemplars.length) continue
    seen++
    // Past the cap, keep counting: the drawer says a missing transaction was dropped, not that it had none.
    if (out.length >= EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW) continue
    const groups = Array.isArray(tx.groups) ? tx.groups.filter((g): g is string => typeof g === 'string' && g !== '') : []
    out.push(groups.length ? { name: tx.name || key, exemplars, groups } : { name: tx.name || key, exemplars })
  }
  return { kept: out, seen }
}

/** Keeps windows that carried exemplars, newest last, within the retention span. */
export function appendExemplarWindow(ring: ExemplarWindow[], metrics: LiveMetrics, tsMs: number, timestamp: string): ExemplarWindow[] {
  const { kept: transactions, seen } = windowExemplars(metrics.transactions)
  const cutoff = tsMs - EXEMPLAR_RETENTION_MS
  let start = 0
  while (start < ring.length && ring[start].tsMs < cutoff) start++
  const kept = start > 0 ? ring.slice(start) : ring
  if (!transactions.length) return kept
  // A replayed or reordered window replaces the one with the same time.
  const next = kept.filter(w => w.tsMs !== tsMs)
  next.push(seen > transactions.length ? { tsMs, timestamp, transactions, truncated: { seen, kept: transactions.length } } : { tsMs, timestamp, transactions })
  if (next.length > 1 && next[next.length - 2].tsMs > tsMs) next.sort((a, b) => a.tsMs - b.tsMs)
  return next.length > EXEMPLAR_MAX_WINDOWS ? next.slice(next.length - EXEMPLAR_MAX_WINDOWS) : next
}

/** The retained window nearest `tsMs`, if one lies within the tolerance. */
export function nearestExemplarWindow(ring: readonly ExemplarWindow[], tsMs: number, toleranceMs = EXEMPLAR_WINDOW_TOLERANCE_MS): ExemplarWindow | undefined {
  let best: ExemplarWindow | undefined
  let bestGap = Infinity
  for (const w of ring) {
    const gap = Math.abs(w.tsMs - tsMs)
    if (gap < bestGap) {
      best = w
      bestGap = gap
    }
  }
  return best && bestGap <= toleranceMs ? best : undefined
}

/** A clicked chart point's time span: [startMs, endMs). */
export interface ExemplarTimeRange {
  startMs: number
  endMs: number
}

/**
 * The span a time-bucketed chart point averages. aggregateByGranularity
 * groups points by floor(timestampMs / bucket) and keeps the first point's
 * timestamp, so the clicked time is only the bucket's first window.
 */
export function exemplarBucketRange(tsMs: number, bucketSeconds: number): ExemplarTimeRange | undefined {
  if (!Number.isFinite(tsMs) || !(bucketSeconds > 0)) return undefined
  const bucketMs = bucketSeconds * 1000
  const startMs = Math.floor(tsMs / bucketMs) * bucketMs
  return { startMs, endMs: startMs + bucketMs }
}

/** The retained windows inside the range, oldest first. */
export function exemplarWindowsInRange(ring: readonly ExemplarWindow[], range: ExemplarTimeRange): ExemplarWindow[] {
  return ring.filter(w => w.tsMs >= range.startMs && w.tsMs < range.endMs)
}

/**
 * The retained window in the range whose exemplars (for the chart's
 * transactions) include the slowest request: the one most likely behind a
 * spike in the averaged point. Falls back to the first window in the range
 * when none has an exemplar for those transactions.
 */
export function slowestExemplarWindowInRange(
  ring: readonly ExemplarWindow[],
  range: ExemplarTimeRange,
  names: readonly string[],
  groups: readonly string[] = [],
): ExemplarWindow | undefined {
  const inRange = exemplarWindowsInRange(ring, range)
  let best: ExemplarWindow | undefined
  let bestMs = -Infinity
  for (const w of inRange) {
    for (const t of exemplarsForTransactions(w, names, groups)) {
      for (const e of t.exemplars) {
        const ms = finite(e.duration_ms) ? e.duration_ms : -Infinity
        if (!best || ms > bestMs) {
          best = w
          bestMs = ms
        }
      }
    }
  }
  return best ?? inRange[0]
}

/**
 * The window's transactions limited to `names` or to transactions run by
 * `groups` (all of them when both are empty).
 */
export function exemplarsForTransactions(window: ExemplarWindow | undefined, names: readonly string[], groups: readonly string[] = []): ExemplarWindowTransaction[] {
  if (!window) return []
  if (!names.length && !groups.length) return window.transactions
  const wanted = new Set(names)
  const wantedGroups = new Set(groups)
  return window.transactions.filter(t => wanted.has(t.name) || (t.groups ?? []).some(g => wantedGroups.has(g)))
}

/** What a chart's visible series ask the exemplar drawer for. */
export interface ExemplarChartSelection {
  /** Empty with `groups` empty: every transaction. */
  transactionNames: string[]
  groups: string[]
}

/**
 * The exemplar filter for a chart's visible series: a visible run total means
 * every transaction; otherwise the visible transactions plus every
 * transaction the visible groups ran. Keys are the chart's series keys
 * (`totalKey`, `<group>_<metric>`, transaction keys); `transactionName`
 * decodes a transaction key.
 */
export function exemplarSelectionForSeries(
  visibleKeys: readonly string[],
  options: { totalKey: string; metric: string; groupNames: readonly string[]; transactionName: (key: string) => string | undefined },
): ExemplarChartSelection {
  if (visibleKeys.includes(options.totalKey)) return { transactionNames: [], groups: [] }
  const names = new Set<string>()
  const groups = new Set<string>()
  const groupKeys = new Map(options.groupNames.map(g => [`${g}_${options.metric}`, g]))
  for (const key of visibleKeys) {
    const name = options.transactionName(key)
    if (name) names.add(name)
    const group = groupKeys.get(key)
    if (group) groups.add(group)
  }
  return { transactionNames: [...names], groups: [...groups] }
}

/** Whether this page received live windows, so a chart click can open exemplars. */
export const liveExemplarsReceived = (retention: LiveWindowRetention | undefined): boolean => retention?.firstWindowMs !== undefined
/** Whether any retained window carries exemplars. */
export const liveExemplarsRetained = (retention: LiveWindowRetention | undefined): boolean => (retention?.exemplars.length ?? 0) > 0

function addWindowSums(sums: Record<string, PhaseSums>, transactions: Record<string, TxMetrics> | undefined): { sums: Record<string, PhaseSums>; truncated: boolean } {
  if (!transactions) return { sums, truncated: false }
  const next = nameMap(sums)
  let truncated = false
  for (const [key, tx] of Object.entries(transactions)) {
    const name = tx?.name || key
    const weight = finite(tx?.count) && tx.count > 0 ? tx.count : 0
    if (!weight) continue
    const known = hasName(next, name)
    if (!known && Object.keys(next).length >= STABLE_MAX_TRANSACTIONS) {
      truncated = true
      continue
    }
    const v = txPhaseVector(tx)
    // A record with no phase figures would pull every baseline phase toward 0.
    if (v.serverSource === 'unknown') continue
    const prev = known ? next[name] : { weight: 0, count: 0, errors: 0, dns: 0, connect: 0, tls: 0, server: 0, download: 0, noResponse: 0, avgMs: 0 }
    next[name] = {
      weight: prev.weight + weight,
      count: prev.count + weight,
      errors: prev.errors + (finite(tx.errors) ? Math.max(0, tx.errors) : 0),
      dns: prev.dns + v.dns * weight,
      connect: prev.connect + v.connect * weight,
      tls: prev.tls + v.tls * weight,
      server: prev.server + v.server * weight,
      download: prev.download + v.download * weight,
      noResponse: prev.noResponse + v.noResponse * weight,
      avgMs: prev.avgMs + v.avgMs * weight,
    }
  }
  return { sums: next, truncated }
}

/**
 * The first stable minute: the 60 s that begin once active VUsers have not
 * risen for STABLE_WINDOWS_REQUIRED windows in a row (the stream carries no
 * ramp state). A clear rise above the starting level while collecting means
 * the ramp went on, so it starts over; once a minute is complete it stays
 * fixed for the run.
 */
export function advanceStableMinute(state: StableMinuteState, metrics: LiveMetrics, tsMs: number): StableMinuteState {
  if (state.phase === 'ready') return state
  const vusers = finite(metrics.active_vusers) ? metrics.active_vusers : 0
  if (vusers <= 0) return { ...emptyStableMinute() }
  const rose = state.lastVusers !== undefined && vusers > state.lastVusers
  if (state.phase === 'collecting') {
    // Noise around the level is not a ramp: only a clear rise above it restarts.
    const level = state.vusers ?? vusers
    if (vusers > level + Math.max(1, level * STABLE_RISE_TOLERANCE)) return { ...emptyStableMinute(), lastVusers: vusers }
    if (state.startMs !== undefined && tsMs >= state.startMs + STABLE_MINUTE_MS) {
      return Object.keys(state.sums).length ? { ...state, phase: 'ready', endMs: state.startMs + STABLE_MINUTE_MS, lastVusers: vusers } : { ...emptyStableMinute(), lastVusers: vusers }
    }
    const added = addWindowSums(state.sums, metrics.transactions)
    return { ...state, lastVusers: vusers, windows: state.windows + 1, sums: added.sums, ...(state.truncated || added.truncated ? { truncated: true } : {}) }
  }
  const steadyWindows = state.lastVusers === undefined || rose ? 0 : state.steadyWindows + 1
  if (steadyWindows < STABLE_WINDOWS_REQUIRED) return { ...state, lastVusers: vusers, steadyWindows }
  const added = addWindowSums(nameMap(), metrics.transactions)
  return { phase: 'collecting', lastVusers: vusers, steadyWindows, startMs: tsMs, vusers, windows: 1, sums: added.sums, ...(added.truncated ? { truncated: true } : {}) }
}

export interface StableMinuteBaselineRow extends LivePhaseVector {
  count: number
  errorPct: number
}

/** The baseline row for `name`, never an inherited member. */
export const stableMinuteBaselineRow = (baseline: StableMinuteBaseline | null | undefined, name: string): StableMinuteBaselineRow | undefined => (
  baseline && hasName(baseline.rows, name) ? baseline.rows[name] : undefined
)

export interface StableMinuteBaseline {
  startMs: number
  endMs: number
  vusers: number
  windows: number
  rows: Record<string, StableMinuteBaselineRow>
  /** The minute ran more than STABLE_MAX_TRANSACTIONS transactions; only the first ones seen are kept. */
  truncated?: boolean
}

/** The completed minute's count-weighted averages, or null until one is complete. */
export function stableMinuteBaseline(state: StableMinuteState): StableMinuteBaseline | null {
  if (state.phase !== 'ready' || state.startMs === undefined || state.endMs === undefined) return null
  const rows = nameMap<StableMinuteBaselineRow>()
  for (const [name, s] of Object.entries(state.sums)) {
    if (s.weight <= 0) continue
    const avg = (v: number) => v / s.weight
    rows[name] = {
      dns: avg(s.dns), connect: avg(s.connect), tls: avg(s.tls), server: avg(s.server), download: avg(s.download), noResponse: avg(s.noResponse),
      avgMs: avg(s.avgMs), serverSource: 'split', count: s.count, errorPct: s.count > 0 ? (s.errors / s.count) * 100 : 0,
    }
  }
  const baseline: StableMinuteBaseline = { startMs: state.startMs, endMs: state.endMs, vusers: state.vusers ?? 0, windows: state.windows, rows }
  return state.truncated ? { ...baseline, truncated: true } : baseline
}

/** One live window into the retention. */
export function advanceLiveWindowRetention(prev: LiveWindowRetention, metrics: LiveMetrics, timestamp: string): LiveWindowRetention {
  const tsMs = Date.parse(timestamp)
  if (!Number.isFinite(tsMs)) return prev
  return {
    exemplars: appendExemplarWindow(prev.exemplars, metrics, tsMs, timestamp),
    firstWindowMs: prev.firstWindowMs === undefined ? tsMs : Math.min(prev.firstWindowMs, tsMs),
    stable: advanceStableMinute(prev.stable, metrics, tsMs),
  }
}
