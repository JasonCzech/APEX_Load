/**
 * L1 live waterfall: one aggregate row per transaction from the latest
 * window's TxMetrics. Rows are independent transactions, so each starts at 0;
 * the phases are window averages, not one request.
 */
import type { LiveMetrics, TxMetrics } from '../types'
import { formatWaterfallMs, type WaterfallPhaseKey } from './waterfall/phases'
import type { WaterfallNode, WaterfallPhaseSpan, WaterfallStatus } from './waterfall/types'
import type { WaterfallTimeSink } from './waterfall/analysis'
import { PHASES_NOT_REPORTED_TEXT, reuseValueText, txObservationStats, txPhaseVector, type LivePhaseVector, type PhaseObservationStats } from './livePhaseModel'
import { STABLE_MAX_TRANSACTIONS, stableMinuteBaselineRow, type StableMinuteBaseline, type StableMinuteBaselineRow, type StableMinuteState } from './liveWindowRetention'
import { formatTimeOfDay } from '../utils/dateTime'

/** Rows beyond this many (slowest first) are left out of the panel. */
export const LIVE_WATERFALL_MAX_ROWS = 200
/** A row reads as failing once this share of its window's samples failed. */
export const LIVE_WATERFALL_FAILING_ERROR_PCT = 50
/** A rise or fall of this many error-rate points against the baseline changes the outcome. */
export const LIVE_WATERFALL_ERROR_SHIFT_POINTS = 5
/**
 * A transaction missing from a window stays listed (as "no samples this
 * window") for this many windows, so a low-rate one does not flicker.
 */
export const LIVE_WATERFALL_HOLD_WINDOWS = 15

/** Shown in place of the rows for a finished run that left no live window with transactions. */
export const LIVE_WATERFALL_FINISHED_EMPTY_TEXT = 'The run has ended, and the controller does not keep per-transaction live data after a run ends, so there is no live window to show. Open the run report’s time anatomy waterfall for this run’s phases.'
/** A restored window: the controller rebuilt it from stored history after a restart or failover. */
export const LIVE_WATERFALL_RESTORED_TEXT = 'This window was restored from stored history (after a controller restart or failover), which keeps only rate, counts and the average: its phases and percentiles were not reported, so each row is its total only. The next live window replaces it.'

/** The only phases a live row can draw; the legend lists just these. */
export const LIVE_WATERFALL_LEGEND_PHASES: readonly WaterfallPhaseKey[] = ['dns', 'connect', 'tls', 'wait', 'receive', 'total']

/** The live vocabulary over the shared keys: no-response time sits in 'total'. */
export const LIVE_WATERFALL_PHASE_LABELS: Partial<Record<WaterfallPhaseKey, string>> = {
  wait: 'Server (TTFB − setup)',
  receive: 'Download',
  total: 'No TTFB (unsplit)',
}

export const LIVE_WATERFALL_PHASE_DEFINITIONS: Partial<Record<WaterfallPhaseKey, string>> = {
  wait: 'Time to first byte minus DNS, connect and TLS: how long the server took to start answering.',
  receive: 'The rest of the average after every other phase (TxMetrics has no download field).',
  total: 'Samples with no first byte (timeouts, or protocols that record no TTFB): their time after setup cannot be split.',
}

const PHASE_ORDER: ReadonlyArray<[keyof Pick<LivePhaseVector, 'dns' | 'connect' | 'tls' | 'server' | 'download' | 'noResponse'>, WaterfallPhaseKey]> = [
  ['dns', 'dns'], ['connect', 'connect'], ['tls', 'tls'], ['server', 'wait'], ['download', 'receive'], ['noResponse', 'total'],
]

export function liveWaterfallSpans(v: Pick<LivePhaseVector, 'dns' | 'connect' | 'tls' | 'server' | 'download' | 'noResponse'>): WaterfallPhaseSpan[] {
  const spans: WaterfallPhaseSpan[] = []
  let cursor = 0
  for (const [field, key] of PHASE_ORDER) {
    const ms = v[field]
    if (ms > 0) {
      spans.push({ key, startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  return spans
}

const spanEnd = (spans: WaterfallPhaseSpan[]) => (spans.length ? spans[spans.length - 1].endMs : 0)

export interface LiveWaterfallPayload {
  name: string
  tx: TxMetrics
  vector: LivePhaseVector
  baseline?: StableMinuteBaselineRow
  /** Set when the latest window had no sample: the figures are from this earlier window. */
  heldFrom?: string
}

const errorPctOf = (tx: TxMetrics) => (tx.count > 0 ? (Math.max(0, tx.errors) / tx.count) * 100 : 0)
const fmtPct = (v: number) => `${v >= 10 || v === 0 ? v.toFixed(0) : v.toFixed(1)}%`

/** Failing once most samples failed; partial errors keep 'passed' styling but name the share. */
function rowStatus(tx: TxMetrics): WaterfallStatus {
  return errorPctOf(tx) >= LIVE_WATERFALL_FAILING_ERROR_PCT ? 'failed' : 'passed'
}

/** The Status column text: the error share whenever any sample failed. */
export function liveWaterfallStatusLabel(tx: TxMetrics): string {
  const pct = errorPctOf(tx)
  if (tx.count <= 0 || tx.errors <= 0) return 'Passed'
  return pct >= LIVE_WATERFALL_FAILING_ERROR_PCT ? `Failing · ${fmtPct(pct)} errors` : `${fmtPct(pct)} errors`
}

/** The change label for a row the baseline has no figures for. */
export const LIVE_WATERFALL_NOT_SEEN_LABEL = 'not seen in the first stable minute'
export const LIVE_WATERFALL_NOT_IN_CAPPED_BASELINE_LABEL = `not in the baseline (it keeps only the first ${STABLE_MAX_TRANSACTIONS} transactions seen in the first stable minute)`

function deltaFor(current: LivePhaseVector, tx: TxMetrics, base: StableMinuteBaselineRow | undefined, baselineTruncated: boolean): WaterfallNode['delta'] {
  // A capped baseline left transactions out: one with no row may well have run during the minute.
  if (!base) return { ms: 0, label: baselineTruncated ? LIVE_WATERFALL_NOT_IN_CAPPED_BASELINE_LABEL : LIVE_WATERFALL_NOT_SEEN_LABEL, outcome: 'added' }
  const ms = current.avgMs - base.avgMs
  // A clear rise in the error share is "now failing", not only a fully failing window.
  const errorShift = errorPctOf(tx) - base.errorPct
  const nowFailing = errorShift >= LIVE_WATERFALL_ERROR_SHIFT_POINTS
  const nowPassing = -errorShift >= LIVE_WATERFALL_ERROR_SHIFT_POINTS
  const errorNote = Math.abs(errorShift) >= 1 ? `; errors ${fmtPct(base.errorPct)} → ${fmtPct(errorPctOf(tx))}` : ''
  const biggest = PHASE_ORDER
    .map(([field]) => ({ field, d: current[field] - base[field] }))
    .sort((a, b) => Math.abs(b.d) - Math.abs(a.d))[0]
  // A row with unknown phases has every phase at 0: no phase moved, only the total is known.
  const phaseNote = current.serverSource !== 'unknown' && biggest && Math.abs(biggest.d) >= 0.5 ? `, mostly ${PHASE_NAMES[biggest.field]} ${biggest.d > 0 ? '+' : '−'}${formatWaterfallMs(Math.abs(biggest.d))}` : ''
  return {
    ms,
    label: `${formatWaterfallMs(base.avgMs)} in the first stable minute → ${formatWaterfallMs(current.avgMs)} now${phaseNote}${errorNote}`,
    outcome: nowFailing ? 'now_failing' : nowPassing ? 'now_passing' : 'same',
  }
}

const PHASE_NAMES: Record<(typeof PHASE_ORDER)[number][0], string> = {
  dns: 'DNS', connect: 'connect', tls: 'TLS', server: 'server', download: 'download', noResponse: 'no TTFB',
}

export interface LiveWaterfallOptions {
  baseline?: StableMinuteBaseline | null
  /** Draw the baseline ghost and change. */
  showBaseline?: boolean
  /** Transactions held from earlier windows (see advanceLiveWaterfallHold). */
  held?: LiveWaterfallHold
}

/** A transaction last seen in an earlier window, kept for LIVE_WATERFALL_HOLD_WINDOWS. */
export interface LiveWaterfallHeldRow {
  tx: TxMetrics
  lastSeen: string
  windowsAgo: number
}

export type LiveWaterfallHold = Record<string, LiveWaterfallHeldRow>

const activeEntries = (transactions: Record<string, TxMetrics> | undefined) => Object.entries(transactions ?? {})
  .filter(([, tx]) => tx && tx.count > 0)
  .map(([key, tx]) => ({ name: tx.name || key, tx }))

/**
 * One window into the hold: transactions with samples refresh their entry,
 * missing ones age by a window and drop out after LIVE_WATERFALL_HOLD_WINDOWS.
 */
export function advanceLiveWaterfallHold(prev: LiveWaterfallHold, metrics: LiveMetrics | undefined): LiveWaterfallHold {
  const next = Object.create(null) as LiveWaterfallHold
  if (!metrics) return next
  const seen = new Set<string>()
  for (const { name, tx } of activeEntries(metrics.transactions)) {
    seen.add(name)
    next[name] = { tx, lastSeen: metrics.timestamp, windowsAgo: 0 }
  }
  for (const [name, row] of Object.entries(prev)) {
    if (seen.has(name) || row.windowsAgo + 1 > LIVE_WATERFALL_HOLD_WINDOWS) continue
    next[name] = { ...row, windowsAgo: row.windowsAgo + 1 }
  }
  return next
}

/** Transactions with samples in the window (rows past the cap included). */
export function liveWaterfallTransactionCount(transactions: Record<string, TxMetrics> | undefined): number {
  return activeEntries(transactions).length
}

/**
 * The row's reuse share, worded by what it divides by: with scope
 * 'first_byte' the samples with no first byte (and no setup) are left out,
 * and with 'bounded' only some of them could time setup, so neither is a
 * share of all the row's transactions.
 */
export function liveWaterfallReuseDetail(stats: PhaseObservationStats): string {
  const value = reuseValueText(stats, fmtPct)
  switch (stats.reuseScope) {
    case 'first_byte':
      return `${value} of transactions with a first byte opened no connection`
    case 'bounded':
      return `${value} of transactions that could time setup opened no connection`
    default:
      return `${value} of transactions opened no connection`
  }
}

type RankedRow = { name: string; tx: TxMetrics; heldFrom?: string; vector: LivePhaseVector }
const slowestFirst = (a: RankedRow, b: RankedRow) => b.vector.avgMs - a.vector.avgMs || a.name.localeCompare(b.name)

/**
 * One row per transaction with samples in the window, slowest average first;
 * with `held`, transactions missing from this window stay listed from the
 * window they were last seen in. A window with no completions (Go omits an
 * empty `transactions`) still lists the held rows. This window's rows take
 * the LIVE_WATERFALL_MAX_ROWS cap first; held rows only fill what is left,
 * so an earlier window can never push a current transaction out.
 */
export function liveWaterfallNodes(transactions: Record<string, TxMetrics> | undefined, options: LiveWaterfallOptions = {}): WaterfallNode<LiveWaterfallPayload>[] {
  const withBaseline = Boolean(options.showBaseline && options.baseline)
  const current: RankedRow[] = activeEntries(transactions)
    .map(entry => ({ ...entry, vector: txPhaseVector(entry.tx) }))
    .sort(slowestFirst)
    .slice(0, LIVE_WATERFALL_MAX_ROWS)
  const present = new Set(activeEntries(transactions).map(r => r.name))
  const held: RankedRow[] = Object.entries(options.held ?? {})
    .filter(([name, row]) => !present.has(name) && row.windowsAgo > 0 && row.tx?.count > 0)
    .map(([name, row]) => ({ name, tx: row.tx, heldFrom: row.lastSeen, vector: txPhaseVector(row.tx) }))
    .sort(slowestFirst)
    .slice(0, Math.max(0, LIVE_WATERFALL_MAX_ROWS - current.length))
  const rows = [...current, ...held].sort(slowestFirst)
  return rows.map(({ name, tx, vector, heldFrom }) => {
    const phases = liveWaterfallSpans(vector)
    const stats = txObservationStats(tx)
    const phasesUnknown = vector.serverSource === 'unknown'
    const detail = [
      heldFrom ? `No samples this window · last seen ${formatTimeOfDay(heldFrom, { seconds: true })}` : undefined,
      `${(tx.tps ?? 0).toFixed(1)} tps`,
      `${tx.count.toLocaleString()} samples`,
      `${fmtPct(errorPctOf(tx))} errors`,
      phasesUnknown ? `${PHASES_NOT_REPORTED_TEXT.toLowerCase()} (restored from history)` : stats.setupUnmeasured ? 'reuse not measured' : stats.reusePct === undefined ? 'reuse not reported' : liveWaterfallReuseDetail(stats),
      vector.serverSource === 'ttfb' && vector.download > 0 ? 'download includes samples with no first byte' : undefined,
    ].filter(Boolean).join(' · ')
    const base = withBaseline ? stableMinuteBaselineRow(options.baseline, name) : undefined
    const node: WaterfallNode<LiveWaterfallPayload> = {
      id: `tx:${name}`,
      kind: 'request',
      label: name,
      detail,
      startMs: 0,
      durationMs: Math.max(vector.avgMs, spanEnd(phases)),
      // A held row was not measured this window: a neutral (hollow, muted)
      // status, so an earlier window's failures do not read as failing now.
      // The error share stays in the detail line beside "last seen".
      status: heldFrom ? 'not_run' : rowStatus(tx),
      statusLabel: heldFrom ? 'No samples' : liveWaterfallStatusLabel(tx),
      phases,
      // Unknown phases draw one outlined total bar, labelled "Total only".
      timingSource: phasesUnknown ? 'duration' : 'aggregate',
      ...(phasesUnknown ? { derived: true } : {}),
      children: [],
      payload: heldFrom ? { name, tx, vector, baseline: base, heldFrom } : { name, tx, vector, baseline: base },
    }
    if (withBaseline) {
      if (base) {
        const ghostPhases = liveWaterfallSpans(base)
        node.ghost = { durationMs: Math.max(base.avgMs, spanEnd(ghostPhases)), phases: ghostPhases }
      }
      // A held row's figures are from an earlier window: no "now" change or
      // now-failing outcome is claimed for a transaction not observed now.
      if (!heldFrom) node.delta = deltaFor(vector, tx, base, Boolean(options.baseline?.truncated))
    }
    return node
  })
}

/** The axis span: the longest bar or ghost. */
export function liveWaterfallSpanMs(nodes: readonly WaterfallNode<LiveWaterfallPayload>[]): number {
  return nodes.reduce((max, n) => Math.max(max, n.durationMs ?? 0, n.ghost?.durationMs ?? 0), 0)
}

/**
 * Headline facts for independent rows. The shared sinks add row durations as
 * if they ran in one session, which is wrong here.
 */
export function liveWaterfallSinks(nodes: readonly WaterfallNode<LiveWaterfallPayload>[], transactions?: Record<string, TxMetrics>): WaterfallTimeSink[] {
  const sinks: WaterfallTimeSink[] = []
  // Held rows are from earlier windows: they are listed, but are not this window's facts.
  const live = nodes.filter(n => !n.payload?.heldFrom)
  if (!live.length) return sinks
  const slowest = live.reduce((a, b) => ((b.durationMs ?? 0) > (a.durationMs ?? 0) ? b : a))
  sinks.push({ id: 'slowest', label: `Slowest: ${slowest.label}`, detail: `${formatWaterfallMs(slowest.durationMs ?? 0)} average this window`, targetId: slowest.id, tone: 'neutral' })
  // Count-weighted, so a busy transaction weighs more than a rare one. Every
  // transaction in the window counts, including rows past the panel's cap.
  const totals = new Map<string, number>()
  let all = 0
  const vectors = transactions
    ? activeEntries(transactions).map(({ tx }) => ({ tx, vector: txPhaseVector(tx) }))
    : live.flatMap(n => (n.payload ? [n.payload] : []))
  for (const p of vectors) {
    for (const [field] of PHASE_ORDER) {
      const ms = p.vector[field] * p.tx.count
      totals.set(field, (totals.get(field) ?? 0) + ms)
      all += ms
    }
  }
  const heavy = [...totals].sort((a, b) => b[1] - a[1])[0]
  if (heavy && all > 0 && heavy[1] > 0) {
    sinks.push({ id: 'dominant-phase', label: `${PHASE_NAMES[heavy[0] as keyof typeof PHASE_NAMES]} = ${Math.round((heavy[1] / all) * 100)}% of sampled time`, detail: 'Weighted by each transaction’s samples this window', tone: 'neutral' })
  }
  const grown = live.filter(n => n.delta && n.delta.outcome !== 'added').sort((a, b) => (b.delta!.ms) - (a.delta!.ms))[0]
  if (grown?.delta && grown.delta.ms >= 0.5) {
    sinks.push({ id: 'growth', label: `Most growth: ${grown.label} +${formatWaterfallMs(grown.delta.ms)}`, detail: grown.delta.label, targetId: grown.id, tone: 'warning' })
  }
  return sinks
}

/**
 * The note naming the baseline minute, or why there is none yet. With
 * `finished` (the run has ended), no further window will arrive, so a minute
 * not yet complete is reported as never captured rather than as coming.
 */
export function stableMinuteNote(state: StableMinuteState | undefined, options: { finished?: boolean } = {}): string {
  if (!state) return 'The first stable minute is not tracked on this view.'
  if (state.phase === 'ready' && state.startMs !== undefined && state.endMs !== undefined) {
    const capped = state.truncated ? ` It keeps only the first ${STABLE_MAX_TRANSACTIONS} transactions seen.` : ''
    return `First stable minute this page received: ${formatTimeOfDay(state.startMs, { seconds: true })}–${formatTimeOfDay(state.endMs, { seconds: true })} at ${state.vusers ?? 0} VUsers (${state.windows} windows, after VUsers stopped rising for 3 windows).${capped}`
  }
  if (options.finished) {
    return 'No first stable minute was captured on this page: the run ended before one was received, so there is no baseline to compare with.'
  }
  if (state.phase === 'collecting' && state.startMs !== undefined) {
    return `Collecting the first stable minute since ${formatTimeOfDay(state.startMs, { seconds: true })} at ${state.vusers ?? 0} VUsers.`
  }
  return 'The first stable minute starts once active VUsers stop rising for 3 windows.'
}
