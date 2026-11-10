/**
 * Baseline diff waterfall (plan R5): one transaction's time anatomy in the
 * current run against a baseline, both from the archive report's
 * `time_anatomy` (whole run, merged by transaction name).
 *
 * Request rows align by row key ("<step>|<action>#<occurrence>"); when a
 * script edit renumbered its steps, rows fall back to `action#occurrence`.
 * Phase percentiles are not additive, so the attribution headline uses the
 * decomposition means (they sum to the mean duration change) and the P95
 * change is stated on its own.
 */
import type { RunArchiveAnatomyRequest, RunArchivePhaseMeans, RunArchivePhaseStat, RunArchiveTimeAnatomy, RunArchiveTransactionAnatomy } from '../../types/timeAnatomy'
import { anatomyRequestNode, STATISTIC_LABELS, type AggregateStatistic } from '../runReport/timeAnatomyModel'
import { formatWaterfallMs } from '../waterfall/phases'
import { formatDeltaMs } from '../waterfall/debugRunDiff'
import type { WaterfallNode, WaterfallTimeSink } from '../waterfall'

export type AnatomyRowMatch = 'key' | 'action_occurrence'

export interface AnatomyRowPair {
  /** Waterfall node id. */
  id: string
  current?: RunArchiveAnatomyRequest
  baseline?: RunArchiveAnatomyRequest
  /** How the two sides were matched; absent for rows only one run has. */
  match?: AnatomyRowMatch
  /** The `Other requests` overflow row. */
  other?: boolean
}

export interface AnatomyRowAlignment {
  pairs: AnatomyRowPair[]
  /** Pairs matched on action and occurrence because their step numbers differ. */
  fallbackMatches: number
}

const OTHER_ID = 'other-requests'
const fallbackKey = (row: RunArchiveAnatomyRequest) => `${row.action_name}#${row.occurrence}${row.occurrence_folded ? '+' : ''}`

/** Current rows in backend order, then rows only the baseline has, then `Other requests`. */
export function alignAnatomyRows(current: RunArchiveTransactionAnatomy | undefined, baseline: RunArchiveTransactionAnatomy | undefined): AnatomyRowAlignment {
  const cur = current?.requests || []
  const base = baseline?.requests || []
  const baseByKey = new Map(base.map(row => [row.key, row]))
  const used = new Set<RunArchiveAnatomyRequest>()
  const matched = new Map<RunArchiveAnatomyRequest, { row: RunArchiveAnatomyRequest; match: AnatomyRowMatch }>()
  for (const row of cur) {
    const other = baseByKey.get(row.key)
    if (other) {
      matched.set(row, { row: other, match: 'key' })
      used.add(other)
    }
  }
  // Fallback only where action#occurrence is unambiguous on both sides.
  const unique = (rows: RunArchiveAnatomyRequest[]) => {
    const groups = new Map<string, RunArchiveAnatomyRequest[]>()
    for (const row of rows) groups.set(fallbackKey(row), [...(groups.get(fallbackKey(row)) || []), row])
    return new Map([...groups].filter(([, list]) => list.length === 1).map(([key, list]) => [key, list[0]]))
  }
  const curLeft = unique(cur.filter(row => !matched.has(row)))
  const baseLeft = unique(base.filter(row => !used.has(row)))
  let fallbackMatches = 0
  for (const [key, row] of curLeft) {
    const other = baseLeft.get(key)
    if (!other) continue
    matched.set(row, { row: other, match: 'action_occurrence' })
    used.add(other)
    fallbackMatches++
  }
  const pairs: AnatomyRowPair[] = cur.map(row => {
    const m = matched.get(row)
    return m ? { id: row.key, current: row, baseline: m.row, match: m.match } : { id: row.key, current: row }
  })
  for (const row of base) if (!used.has(row)) pairs.push({ id: `baseline:${row.key}`, baseline: row })
  if (current?.other_requests || baseline?.other_requests) {
    pairs.push({ id: OTHER_ID, current: current?.other_requests, baseline: baseline?.other_requests, other: true, ...(current?.other_requests && baseline?.other_requests ? { match: 'key' as const } : {}) })
  }
  return { pairs, fallbackMatches }
}

// ---------------------------------------------------------------------------
// Per-phase deltas

export type AnatomyPhaseName = 'dns' | 'connect' | 'tls' | 'server' | 'download' | 'unsplit'
export const ANATOMY_DIFF_PHASES: readonly AnatomyPhaseName[] = ['dns', 'connect', 'tls', 'server', 'download', 'unsplit']
export const ANATOMY_DIFF_PHASE_LABELS: Record<AnatomyPhaseName, string> = { dns: 'DNS', connect: 'Connect', tls: 'TLS', server: 'Server wait', download: 'Download', unsplit: 'No TTFB split' }

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const pos = (v: unknown) => (finite(v) && v > 0 ? v : 0)

export function statOf(stat: RunArchivePhaseStat | undefined, statistic: AggregateStatistic): number {
  if (!stat) return 0
  return pos(statistic === 'avg' ? stat.avg_ms : stat[`${statistic}_ms`])
}

const hasSplitPhases = (row: RunArchiveAnatomyRequest | undefined) => Boolean(row && (['dns', 'connect', 'tls', 'server', 'download'] as const).some(p => pos(row.phases?.[p]?.avg_ms) > 0 || pos(row.phases?.[p]?.max_ms) > 0))

export interface AnatomyRowDiff {
  pair: AnatomyRowPair
  outcome: 'same' | 'added' | 'removed'
  /** Per-phase current − baseline for the statistic; only when both sides exist. */
  phaseDeltas: Partial<Record<AnatomyPhaseName, number>>
  /** Change in the drawn bar: the sum of the per-phase statistic. */
  barDeltaMs: number
  /** Change in the request's own total for the statistic (not the bar sum for percentiles). */
  totalDeltaMs?: number
  /** Change in the median start offset within the transaction. */
  offsetDeltaMs?: number
  phasesComparable: boolean
}

function barMs(row: RunArchiveAnatomyRequest | undefined, statistic: AggregateStatistic): number {
  if (!row) return 0
  return ANATOMY_DIFF_PHASES.reduce((sum, name) => sum + statOf(row.phases?.[name], statistic), 0)
}

export function diffAnatomyRow(pair: AnatomyRowPair, statistic: AggregateStatistic): AnatomyRowDiff {
  const { current, baseline } = pair
  const outcome = !baseline ? 'added' : !current ? 'removed' : 'same'
  const phaseDeltas: AnatomyRowDiff['phaseDeltas'] = {}
  if (current && baseline) for (const name of ANATOMY_DIFF_PHASES) phaseDeltas[name] = statOf(current.phases?.[name], statistic) - statOf(baseline.phases?.[name], statistic)
  return {
    pair,
    outcome,
    phaseDeltas,
    barDeltaMs: barMs(current, statistic) - barMs(baseline, statistic),
    ...(current && baseline ? { totalDeltaMs: statOf(current.phases?.total, statistic) - statOf(baseline.phases?.total, statistic) } : {}),
    ...(current && baseline && !pair.other ? { offsetDeltaMs: pos(current.offset_ms?.p50_ms) - pos(baseline.offset_ms?.p50_ms) } : {}),
    phasesComparable: hasSplitPhases(current) && hasSplitPhases(baseline),
  }
}

/** Verdict words for the anatomy's phases: unsplit is measured time of samples without a first byte, not unexplained time. */
const ANATOMY_PHASE_WORDS: Record<AnatomyPhaseName, string> = { dns: 'DNS', connect: 'connect', tls: 'TLS', server: 'server', download: 'download', unsplit: 'no TTFB split' }

/** "+310 ms, all in server", "−42 ms, mostly no TTFB split (64%)": named as the row drawer's phase table names them. */
export function anatomyDeltaVerdict(deltaMs: number, phaseDeltas: Partial<Record<AnatomyPhaseName, number>>, phasesComparable: boolean): string {
  if (Math.abs(deltaMs) < 0.5) return 'No change'
  const head = formatDeltaMs(deltaMs)
  if (!phasesComparable) return `${head} (total only)`
  let dom: { name: AnatomyPhaseName; share: number; ms: number } | undefined
  for (const [name, ms] of Object.entries(phaseDeltas) as Array<[AnatomyPhaseName, number]>) {
    if (!finite(ms) || Math.sign(ms) !== Math.sign(deltaMs)) continue
    if (!dom || Math.abs(ms) > Math.abs(dom.ms)) dom = { name, ms, share: ms / deltaMs }
  }
  if (!dom) return `${head}, spread across phases`
  const word = ANATOMY_PHASE_WORDS[dom.name]
  if (dom.share >= 0.9) return `${head}, all in ${word}`
  if (dom.share >= 0.5) return `${head}, mostly ${word} (${Math.round(dom.share * 100)}%)`
  return `${head}, spread across phases (largest: ${word})`
}

function rowLabel(row: RunArchiveAnatomyRequest): string {
  if (row.occurrence > 1 || row.occurrence_folded) return `${row.action_name} #${row.occurrence}${row.occurrence_folded ? '+' : ''}`
  return row.action_name
}

export function rowDiffLabel(diff: AnatomyRowDiff, statistic: AggregateStatistic): string {
  if (diff.outcome === 'added') return `New in this run (${formatWaterfallMs(barMs(diff.pair.current, statistic))})`
  if (diff.outcome === 'removed') return `Only in baseline (${formatWaterfallMs(barMs(diff.pair.baseline, statistic))})`
  let label = anatomyDeltaVerdict(diff.barDeltaMs, diff.phaseDeltas, diff.phasesComparable)
  if (statistic !== 'avg' && finite(diff.totalDeltaMs)) label += ` · request ${STATISTIC_LABELS[statistic]} ${formatDeltaMs(diff.totalDeltaMs)}`
  if (diff.pair.match === 'action_occurrence') label += ' · matched by action (step numbers differ)'
  return label
}

export interface AnatomyDiffTree {
  roots: WaterfallNode<RunArchiveAnatomyRequest>[]
  spanMs: number
  rows: AnatomyRowDiff[]
  fallbackMatches: number
  hasDeltas: boolean
}

/** The current run's aggregate rows, each with the baseline row as a ghost and the change as its delta. */
export function buildAnatomyDiffTree(current: RunArchiveTransactionAnatomy, baseline: RunArchiveTransactionAnatomy, statistic: AggregateStatistic): AnatomyDiffTree {
  const { pairs, fallbackMatches } = alignAnatomyRows(current, baseline)
  const rows = pairs.map(pair => diffAnatomyRow(pair, statistic))
  const roots: WaterfallNode<RunArchiveAnatomyRequest>[] = rows.map(diff => {
    const { pair } = diff
    const delta = { ms: diff.barDeltaMs, label: rowDiffLabel(diff, statistic), outcome: diff.outcome }
    if (!pair.current) {
      const row = pair.baseline!
      return {
        id: pair.id,
        kind: 'request',
        label: pair.other ? 'Other requests' : rowLabel(row),
        detail: 'Only in baseline: not in this run’s anatomy',
        startMs: null,
        durationMs: null,
        status: 'not_run',
        phases: [],
        timingSource: 'aggregate',
        children: [],
        delta,
        payload: row,
      }
    }
    // Other requests have no meaningful offset, so they start at 0 as in the report.
    const node = anatomyRequestNode(pair.current, statistic, pair.id, pair.other ? 0 : pos(pair.current.offset_ms?.p50_ms), Boolean(pair.other))
    if (pair.other) node.label = 'Other requests'
    if (pair.baseline) {
      const ghost = anatomyRequestNode(pair.baseline, statistic, `${pair.id}:baseline`, 0)
      node.ghost = { durationMs: ghost.durationMs ?? 0, phases: ghost.phases }
    }
    node.delta = delta
    return node
  })
  // Ghosts can run past the current rows' end (the run got faster): keep them in view.
  let spanMs = 0
  for (const n of roots) if (n.startMs != null) spanMs = Math.max(spanMs, n.startMs + pos(n.durationMs), n.startMs + pos(n.ghost?.durationMs))
  return { roots, spanMs, rows, fallbackMatches, hasDeltas: rows.some(r => r.outcome !== 'same' || Math.abs(r.barDeltaMs) >= 0.5) }
}

// ---------------------------------------------------------------------------
// Transaction-level attribution (decomposition means)

export type MeanComponent = AnatomyPhaseName | 'non_request' | 'overlap'
export const MEAN_COMPONENT_LABELS: Record<MeanComponent, string> = { ...ANATOMY_DIFF_PHASE_LABELS, non_request: 'Outside requests', overlap: 'Overlap (subtracted)' }
const MEAN_COMPONENT_WORDS: Record<MeanComponent, string> = { ...ANATOMY_PHASE_WORDS, non_request: 'outside requests', overlap: 'overlap' }
const MEAN_KEYS: Record<AnatomyPhaseName, keyof RunArchivePhaseMeans> = { dns: 'dns_ms', connect: 'connect_ms', tls: 'tls_ms', server: 'server_ms', download: 'download_ms', unsplit: 'unsplit_ms' }

export interface MeanComponentDelta {
  component: MeanComponent
  baselineMs: number
  currentMs: number
  /** Contribution to the mean duration change: overlap counts negatively. */
  deltaMs: number
  /** deltaMs / mean change, when the change is not zero. */
  share?: number
}

export interface MeanAttribution {
  baselineMs: number
  currentMs: number
  deltaMs: number
  components: MeanComponentDelta[]
  headline: string
  /** Matched instances on each side. */
  instances: { current: number; baseline: number }
  /** How much of each run the means cover: matched over eligible (not interrupted) instances. */
  coverage: { current: MeanCoverage; baseline: MeanCoverage }
  /**
   * A run's means cover less than MEAN_MIN_JOIN_COVERAGE of its instances
   * (requests lost to buffer drops or identity eviction): the matched subset
   * may be biased, so shares are withheld and the headline says so.
   */
  lowCoverage: boolean
}

export interface MeanCoverage {
  /** Instances in the decomposition (joined, not interrupted). */
  matched: number
  /** Instances that could have been matched: all but the interrupted ones. */
  eligible: number
  /** matched / eligible, at most 1. */
  ratio: number
}

/** The report's tail attribution withholds its headline below this join coverage; the mean diff does too. */
export const MEAN_MIN_JOIN_COVERAGE = 0.8

const NO_CHANGE_MS = 0.5

function meanCoverage(entry: RunArchiveTransactionAnatomy): MeanCoverage {
  const matched = pos(entry.decomposition?.instances)
  const eligible = Math.max(pos(entry.instances) - pos(entry.interrupted_instances), matched)
  return { matched, eligible, ratio: eligible > 0 ? Math.min(1, matched / eligible) : 0 }
}

/** "2,500 of 10,000 instances (25%)". */
export function formatMeanCoverage(coverage: MeanCoverage): string {
  return `${coverage.matched.toLocaleString('en-US')} of ${coverage.eligible.toLocaleString('en-US')} instances (${Math.floor(coverage.ratio * 100)}%)`
}

export function meanAttribution(current: RunArchiveTransactionAnatomy, baseline: RunArchiveTransactionAnatomy): MeanAttribution | null {
  const c = current.decomposition
  const b = baseline.decomposition
  if (!c || !b || !(c.instances > 0) || !(b.instances > 0)) return null
  const deltaMs = (c.avg_duration_ms || 0) - (b.avg_duration_ms || 0)
  const components: MeanComponentDelta[] = []
  for (const name of ANATOMY_DIFF_PHASES) {
    const cur = c.phases?.[MEAN_KEYS[name]] || 0
    const base = b.phases?.[MEAN_KEYS[name]] || 0
    components.push({ component: name, baselineMs: base, currentMs: cur, deltaMs: cur - base })
  }
  components.push({ component: 'non_request', baselineMs: b.non_request_ms || 0, currentMs: c.non_request_ms || 0, deltaMs: (c.non_request_ms || 0) - (b.non_request_ms || 0) })
  components.push({ component: 'overlap', baselineMs: b.overlap_ms || 0, currentMs: c.overlap_ms || 0, deltaMs: -((c.overlap_ms || 0) - (b.overlap_ms || 0)) })
  const coverage = { current: meanCoverage(current), baseline: meanCoverage(baseline) }
  const lowCoverage = coverage.current.ratio < MEAN_MIN_JOIN_COVERAGE || coverage.baseline.ratio < MEAN_MIN_JOIN_COVERAGE
  // A share of a change measured on a biased subset reads as the transaction's own split: withhold it.
  if (Math.abs(deltaMs) >= NO_CHANGE_MS && !lowCoverage) for (const comp of components) comp.share = comp.deltaMs / deltaMs
  return {
    baselineMs: b.avg_duration_ms || 0,
    currentMs: c.avg_duration_ms || 0,
    deltaMs,
    components,
    headline: meanHeadline(deltaMs, components, { lowCoverage }),
    instances: { current: c.instances, baseline: b.instances },
    coverage,
    lowCoverage,
  }
}

/**
 * "Mean +180 ms: 85% server, 15% TLS"; amounts instead of shares when other
 * phases offset the change, or when a run's means cover too few instances
 * ("Mean +180 ms (low match coverage): server +200 ms").
 */
export function meanHeadline(deltaMs: number, components: readonly MeanComponentDelta[], options: { lowCoverage?: boolean } = {}): string {
  const qualifier = options.lowCoverage ? ' (low match coverage)' : ''
  if (Math.abs(deltaMs) < NO_CHANGE_MS) return `Mean: no change${qualifier}`
  const head = `Mean ${formatDeltaMs(deltaMs)}${qualifier}`
  const moving = components.filter(c => Math.abs(c.deltaMs) >= NO_CHANGE_MS).sort((x, y) => Math.abs(y.deltaMs) - Math.abs(x.deltaMs))
  const sameDirection = moving.filter(c => Math.sign(c.deltaMs) === Math.sign(deltaMs))
  if (!sameDirection.length) return `${head}, spread across phases`
  // Shares above 100% would read oddly: when other components offset the
  // change, or the shares are withheld for low coverage, state amounts.
  const offset = moving.some(c => Math.sign(c.deltaMs) !== Math.sign(deltaMs))
  if (offset || options.lowCoverage) {
    return `${head}: ${moving.slice(0, 3).map(c => `${MEAN_COMPONENT_WORDS[c.component]} ${formatDeltaMs(c.deltaMs)}`).join(', ')}`
  }
  const shown = sameDirection.filter(c => (c.share ?? 0) >= 0.05).slice(0, 3)
  return `${head}: ${(shown.length ? shown : sameDirection.slice(0, 1)).map(c => `${Math.round((c.share ?? 0) * 100)}% ${MEAN_COMPONENT_WORDS[c.component]}`).join(', ')}`
}

// ---------------------------------------------------------------------------
// Percentile and connection facts

export interface PercentileChange {
  statistic: AggregateStatistic
  baselineMs: number
  currentMs: number
  deltaMs: number
  approximate: boolean
}

/**
 * The time anatomy's transaction duration covers every instance of the whole
 * run, failures included, unlike the comparison's stable-window successful
 * P95 shown beside it: its label always says so.
 */
export const WHOLE_RUN_P95_LABEL = 'Whole-run P95'
export const WHOLE_RUN_P95_SCOPE = 'all instances, failures included'

export function transactionPercentileChange(current: RunArchiveTransactionAnatomy, baseline: RunArchiveTransactionAnatomy, statistic: Exclude<AggregateStatistic, 'avg'> = 'p95'): PercentileChange | null {
  const c = current.duration
  const b = baseline.duration
  if (!c || !b || !(c.samples > 0) || !(b.samples > 0)) return null
  const cur = c[`${statistic}_ms`]
  const base = b[`${statistic}_ms`]
  if (!finite(cur) || !finite(base)) return null
  return { statistic, baselineMs: base, currentMs: cur, deltaMs: cur - base, approximate: c.percentile_quality === 'approximate' || b.percentile_quality === 'approximate' }
}

export interface ConnectionReuse {
  newConnections: number
  reused: number
  /** reused / (new + reused) × 100; undefined when no request was classified. */
  reusePct?: number
}

export function transactionConnectionReuse(entry: RunArchiveTransactionAnatomy | undefined): ConnectionReuse {
  let newConnections = 0
  let reused = 0
  for (const row of [...(entry?.requests || []), ...(entry?.other_requests ? [entry.other_requests] : [])]) {
    newConnections += pos(row.connections?.new)
    reused += pos(row.connections?.reused)
  }
  const classified = newConnections + reused
  return { newConnections, reused, ...(classified > 0 ? { reusePct: (reused / classified) * 100 } : {}) }
}

/** "Connection reuse 92% → 40%", or null unless both runs classified connections. */
export function reuseSentence(current: ConnectionReuse, baseline: ConnectionReuse): string | null {
  if (!finite(current.reusePct) || !finite(baseline.reusePct)) return null
  return `Connection reuse ${Math.round(baseline.reusePct)}% → ${Math.round(current.reusePct)}%`
}

// ---------------------------------------------------------------------------
// Availability and honesty

export type AnatomyDiffAvailability =
  | { status: 'ready'; current: RunArchiveTransactionAnatomy; baseline: RunArchiveTransactionAnatomy }
  | { status: 'unavailable'; message: string }

function missingCopy(anatomy: RunArchiveTimeAnatomy, name: string, run: string): string {
  const notEmitted = anatomy.truncation?.transactions_not_emitted || 0
  return notEmitted > 0
    ? `${run} has no time anatomy for ${name}. Its report lists the first ${anatomy.transactions?.length || 0} transactions and left ${notEmitted} out, so ${name} may be one of them.`
    : `${run} recorded no time anatomy for ${name}.`
}

export function anatomyDiffAvailability(name: string, current: RunArchiveTimeAnatomy | undefined, baseline: RunArchiveTimeAnatomy | undefined): AnatomyDiffAvailability {
  if (!current) return { status: 'unavailable', message: 'The current run’s report has no time anatomy (archived before it was recorded, or no business transactions).' }
  if (!baseline) return { status: 'unavailable', message: 'The baseline’s report has no time anatomy (archived before it was recorded, or no business transactions).' }
  const cur = current.transactions?.find(t => t.name === name)
  const base = baseline.transactions?.find(t => t.name === name)
  if (!cur) return { status: 'unavailable', message: missingCopy(current, name, 'The current run') }
  if (!base) return { status: 'unavailable', message: missingCopy(baseline, name, 'The baseline') }
  return { status: 'ready', current: cur, baseline: base }
}

export function anatomyDiffNotes(current: RunArchiveTransactionAnatomy, baseline: RunArchiveTransactionAnatomy, statistic: AggregateStatistic, fallbackMatches: number): string[] {
  const notes = [
    'Whole run: time anatomy covers every instance in each run, not the comparison’s stable load windows.',
    'Groups are merged: rows come from every group that ran a transaction with this name.',
  ]
  if (statistic !== 'avg') notes.push(`Each bar sums the ${STATISTIC_LABELS[statistic]} of each phase. Phase ${STATISTIC_LABELS[statistic]}s do not add up to the request’s or the transaction’s ${STATISTIC_LABELS[statistic]}.`)
  notes.push('The mean headline uses matched instances only; the P95 change uses every instance.')
  const coverage = { current: meanCoverage(current), baseline: meanCoverage(baseline) }
  if (coverage.current.matched > 0 && coverage.baseline.matched > 0 && (coverage.current.ratio < MEAN_MIN_JOIN_COVERAGE || coverage.baseline.ratio < MEAN_MIN_JOIN_COVERAGE)) {
    notes.push(`Low match coverage: the means cover ${formatMeanCoverage(coverage.baseline)} in the baseline and ${formatMeanCoverage(coverage.current)} in this run (the rest may have lost requests to a buffer drop or eviction). The matched instances may not be typical, so the mean change is not split into shares; check it against the P95 change.`)
  }
  if (fallbackMatches > 0) notes.push(`${fallbackMatches} ${fallbackMatches === 1 ? 'row was' : 'rows were'} matched by action and occurrence because the step numbers changed.`)
  const approximate = [current, baseline].some(entry => entry.duration?.percentile_quality === 'approximate' || [...(entry.requests || [])].some(row => row.phases?.total?.percentile_quality === 'approximate'))
  if (approximate) notes.push('Percentiles are approximate (estimated from a bounded sketch).')
  if (current.requests_truncated || baseline.requests_truncated) notes.push('A run hit the per-transaction row limit; its extra requests are grouped as Other requests.')
  return notes
}

/** Headline chips for the waterfall's facts strip. */
export function anatomyDiffSinks(mean: MeanAttribution | null, p95: PercentileChange | null, reuse: string | null, tree: AnatomyDiffTree): WaterfallTimeSink[] {
  const sinks: WaterfallTimeSink[] = []
  if (mean) sinks.push({ id: 'mean', label: mean.headline, detail: `Mean of matched instances: ${formatWaterfallMs(mean.baselineMs)} → ${formatWaterfallMs(mean.currentMs)}; matched ${formatMeanCoverage(mean.coverage.baseline)} → ${formatMeanCoverage(mean.coverage.current)}`, tone: mean.deltaMs >= NO_CHANGE_MS ? 'warning' : 'neutral' })
  if (p95) sinks.push({ id: 'p95', label: `${WHOLE_RUN_P95_LABEL} ${formatDeltaMs(p95.deltaMs)}`, detail: `${formatWaterfallMs(p95.baselineMs)} → ${formatWaterfallMs(p95.currentMs)}${p95.approximate ? ' (approximate)' : ''}; ${WHOLE_RUN_P95_SCOPE}; not split by phase`, tone: p95.deltaMs >= NO_CHANGE_MS ? 'warning' : 'neutral' })
  if (reuse) sinks.push({ id: 'reuse', label: reuse, detail: 'New and reused connections summed over the transaction’s request rows', tone: 'neutral' })
  const added = tree.rows.filter(r => r.outcome === 'added').length
  const removed = tree.rows.filter(r => r.outcome === 'removed').length
  if (added || removed) sinks.push({ id: 'rows', label: `${added} new, ${removed} missing request ${added + removed === 1 ? 'row' : 'rows'}`, detail: 'Rows only one run recorded are not counted as time gained or lost', tone: 'neutral' })
  return sinks
}
