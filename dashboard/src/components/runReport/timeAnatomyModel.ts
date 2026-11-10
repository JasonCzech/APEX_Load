/**
 * Pure model for the report's time anatomy (R1 aggregate waterfall, R2 tail
 * attribution, R3 representative drill-downs).
 *
 * The controller splits every archive row into phases (sequential_clipped_v1)
 * and the browser never re-splits them: drill-down rows draw the backend
 * `phases` as they come, so no trace-timing helper is used here.
 *
 * The node shape mirrors `WaterfallNode` from the shared waterfall module
 * (../waterfall/types) field for field, so `<Waterfall>` can replace
 * `ReportPhaseBars` once that module lands on main. It is declared locally
 * until then because the shared module is still being edited.
 */
import type { ReportLens } from '../../appRouting'
import { getErrorMessage } from '../../utils/errorUtils'
import type {
  ArchiveTransactionWaterfall,
  ArchiveTransactionWaterfallParams,
  ArchiveWaterfallRow,
  RunArchiveAnatomyPhaseStats,
  RunArchiveAnatomyRequest,
  RunArchivePhaseMeans,
  RunArchivePhaseStat,
  RunArchiveRepresentativeSample,
  RunArchiveTailAttribution,
  RunArchiveTimeAnatomy,
  RunArchiveTimeDecomposition,
  RunArchiveTransactionAnatomy,
  TimeAnatomyTailComponentName,
  TimeAnatomyTruncationReason,
} from '../../types/timeAnatomy'

// ---------------------------------------------------------------------------
// Node shape (structurally compatible with ../waterfall/types WaterfallNode)

/** The shared phase keys: server draws as `wait`, download as `receive`, unsplit as `total`. */
export type AnatomyPhaseKey = 'dns' | 'connect' | 'tls' | 'wait' | 'receive' | 'total'

export interface AnatomyPhaseSpan {
  key: AnatomyPhaseKey
  startMs: number
  endMs: number
}

export interface AnatomyTimeSpan {
  startMs: number
  endMs: number
}

export type AnatomyNodeKind = 'transaction' | 'request' | 'think'
export type AnatomyNodeStatus = 'passed' | 'failed' | 'incomplete' | 'not_run'

export interface AnatomyNode<T = unknown> {
  id: string
  kind: AnatomyNodeKind
  label: string
  detail?: string
  startMs: number | null
  durationMs: number | null
  status: AnatomyNodeStatus
  phases: AnatomyPhaseSpan[]
  timingSource?: 'aggregate' | 'protocol_evidence' | 'duration'
  statusCode?: number
  connectionReused?: boolean
  derived?: boolean
  errorMessage?: string
  /**
   * Stretches of the row's own window in which none of its children ran,
   * drawn hatched on its track. Report-only: the shared waterfall has no
   * such field, and it is optional so the shape stays assignable.
   */
  outside?: AnatomyTimeSpan[]
  children: AnatomyNode<T>[]
  payload?: T
}

export type AggregateStatistic = 'avg' | 'p50' | 'p90' | 'p95' | 'p99'
export const AGGREGATE_STATISTICS: readonly AggregateStatistic[] = ['avg', 'p50', 'p90', 'p95', 'p99']

export interface AggregatePhaseStat {
  avg?: number
  p50?: number
  p90?: number
  p95?: number
  p99?: number
  observedPct?: number
}

export type AggregatePhaseName = 'dns' | 'connect' | 'tls' | 'server' | 'download'
export type AggregatePhaseVector = Partial<Record<AggregatePhaseName, AggregatePhaseStat>>

const AGGREGATE_PHASE_KEYS: Record<AggregatePhaseName, AnatomyPhaseKey> = {
  dns: 'dns',
  connect: 'connect',
  tls: 'tls',
  server: 'wait',
  download: 'receive',
}

/**
 * Legend labels. `wait` is the server's share of TTFB: the controller counts
 * TTFB from request start, so server = TTFB minus connection setup. `total`
 * is relabelled: on a report it is the untimed (no TTFB) remainder.
 */
export const ANATOMY_PHASE_LABELS: Record<AnatomyPhaseKey, string> = {
  dns: 'DNS',
  connect: 'Connect',
  tls: 'TLS',
  wait: 'Server wait',
  receive: 'Download',
  total: 'No TTFB split',
}

/** Definitions shown beside the legend and as each phase's tooltip. */
export const ANATOMY_PHASE_DEFINITIONS: Partial<Record<AnatomyPhaseKey, string>> = {
  wait: 'Time to first byte minus connection setup (DNS, connect and TLS): how long the server took to start answering.',
  total: 'The request recorded no time to first byte, so its time cannot be split into phases.',
}

export const OUTSIDE_REQUESTS_LABEL = 'Outside requests'
export const OUTSIDE_REQUESTS_DEFINITION = 'Time inside the transaction when none of its requests was running (think time, client work).'

export const ANATOMY_PHASE_ORDER: readonly AnatomyPhaseKey[] = ['dns', 'connect', 'tls', 'wait', 'receive', 'total']

export const STATISTIC_LABELS: Record<AggregateStatistic, string> = { avg: 'Avg', p50: 'P50', p90: 'P90', p95: 'P95', p99: 'P99' }

const pos = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : 0)
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

// ---------------------------------------------------------------------------
// Phase split parity (mirrors the Go archiveRequestPhaseSplit test table)

export interface PhaseSplitInput {
  duration_ms?: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
}

export interface PhaseSplit {
  dns: number
  connect: number
  tls: number
  server: number
  download: number
  unsplit: number
  clamped: boolean
}

/**
 * The controller's sequential_clipped_v1 split. The browser does not use it on
 * archive rows (the backend sends `phases`); it exists so the parity table
 * below is checked on both sides of the wire.
 */
export function requestPhaseSplit(input: PhaseSplitInput): PhaseSplit {
  const D = pos(input.duration_ms)
  let clamped = false
  const clip = (value: number, limit: number) => {
    const out = Math.min(value, Math.max(0, limit))
    if (out !== value) clamped = true
    return out
  }
  const dns = clip(pos(input.dns_ms), D)
  const connect = clip(pos(input.connect_ms), D - dns)
  const tls = clip(pos(input.tls_ms), D - dns - connect)
  const setup = dns + connect + tls
  const ttfb = pos(input.ttfb_ms)
  if (ttfb > 0) {
    const raw = ttfb - setup
    const server = Math.min(Math.max(raw, 0), D - setup)
    if (server !== raw) clamped = true
    return { dns, connect, tls, server, download: D - setup - server, unsplit: 0, clamped }
  }
  return { dns, connect, tls, server: 0, download: 0, unsplit: D - setup, clamped }
}

export interface PhaseSplitParityCase {
  name: string
  input: PhaseSplitInput
  expected: PhaseSplit
}

/** Mirrored by TestArchiveRequestPhaseSplit in pkg/api. Change both together. */
export const PHASE_SPLIT_PARITY_CASES: readonly PhaseSplitParityCase[] = [
  { name: 'full split', input: { dns_ms: 5, connect_ms: 10, tls_ms: 15, ttfb_ms: 100, duration_ms: 150 }, expected: { dns: 5, connect: 10, tls: 15, server: 70, download: 50, unsplit: 0, clamped: false } },
  { name: 'reused connection', input: { ttfb_ms: 40, duration_ms: 60 }, expected: { dns: 0, connect: 0, tls: 0, server: 40, download: 20, unsplit: 0, clamped: false } },
  { name: 'no ttfb', input: { dns_ms: 5, ttfb_ms: 0, duration_ms: 80 }, expected: { dns: 5, connect: 0, tls: 0, server: 0, download: 0, unsplit: 75, clamped: false } },
  { name: 'ttfb below setup', input: { dns_ms: 10, connect_ms: 10, tls_ms: 10, ttfb_ms: 20, duration_ms: 100 }, expected: { dns: 10, connect: 10, tls: 10, server: 0, download: 70, unsplit: 0, clamped: true } },
  { name: 'setup above duration', input: { dns_ms: 100, connect_ms: 60, tls_ms: 40, duration_ms: 150 }, expected: { dns: 100, connect: 50, tls: 0, server: 0, download: 0, unsplit: 0, clamped: true } },
  { name: 'ttfb above duration', input: { ttfb_ms: 200, duration_ms: 150 }, expected: { dns: 0, connect: 0, tls: 0, server: 150, download: 0, unsplit: 0, clamped: true } },
  { name: 'non-finite and negative', input: { dns_ms: Number.NaN, connect_ms: Number.POSITIVE_INFINITY, tls_ms: -5, ttfb_ms: -1, duration_ms: 50 }, expected: { dns: 0, connect: 0, tls: 0, server: 0, download: 0, unsplit: 50, clamped: false } },
]

// ---------------------------------------------------------------------------
// Selection

export function pickAnatomyTransaction(anatomy: RunArchiveTimeAnatomy | undefined | null, selectedName: string | undefined): { entry: RunArchiveTransactionAnatomy | undefined; matchedSelection: boolean } {
  const transactions = anatomy?.transactions || []
  const matched = selectedName ? transactions.find(entry => entry.name === selectedName) : undefined
  if (matched) return { entry: matched, matchedSelection: true }
  return { entry: transactions[0], matchedSelection: false }
}

export function defaultStatistic(lens: ReportLens): AggregateStatistic {
  return lens === 'executive' ? 'p50' : 'p95'
}

// ---------------------------------------------------------------------------
// R1 aggregate waterfall

function statValue(stat: RunArchivePhaseStat | undefined, statistic: AggregateStatistic): number {
  if (!stat) return 0
  return pos(statistic === 'avg' ? stat.avg_ms : stat[`${statistic}_ms`])
}

function aggregateStat(stat: RunArchivePhaseStat | undefined): AggregatePhaseStat {
  return {
    avg: stat?.avg_ms,
    p50: stat?.p50_ms,
    p90: stat?.p90_ms,
    p95: stat?.p95_ms,
    p99: stat?.p99_ms,
    ...(stat?.observed_pct !== undefined ? { observedPct: stat.observed_pct } : {}),
  }
}

export function toAggregateVector(phases: RunArchiveAnatomyPhaseStats | undefined): AggregatePhaseVector {
  return {
    dns: aggregateStat(phases?.dns),
    connect: aggregateStat(phases?.connect),
    tls: aggregateStat(phases?.tls),
    server: aggregateStat(phases?.server),
    download: aggregateStat(phases?.download),
  }
}

export function formatAnatomyMs(value: number | undefined): string {
  if (!finite(value)) return '—'
  const abs = Math.abs(value)
  return `${value.toLocaleString(undefined, { maximumFractionDigits: abs >= 100 ? 0 : 1, minimumFractionDigits: 0 })} ms`
}

function rowDetail(row: RunArchiveAnatomyRequest, statistic: AggregateStatistic): string {
  let detail = statistic === 'avg' ? 'Average of each phase' : `${STATISTIC_LABELS[statistic]} of each phase`
  if (finite(row.presence_pct) && row.presence_pct < 95) detail += ` · in ${Math.round(row.presence_pct)}% of instances`
  if (finite(row.connections?.reuse_pct)) detail += ` · reuse ${Math.round(row.connections.reuse_pct)}%`
  return detail
}

function rowLabel(row: RunArchiveAnatomyRequest): string {
  if (row.occurrence > 1 || row.occurrence_folded) return `${row.action_name} #${row.occurrence}${row.occurrence_folded ? '+' : ''}`
  return row.action_name
}

function aggregateNode(row: RunArchiveAnatomyRequest, statistic: AggregateStatistic, id: string, startMs: number | null, derived: boolean): AnatomyNode<RunArchiveAnatomyRequest> {
  const vector = toAggregateVector(row.phases)
  const spans: AnatomyPhaseSpan[] = []
  let cursor = 0
  for (const name of Object.keys(AGGREGATE_PHASE_KEYS) as AggregatePhaseName[]) {
    const ms = pos(vector[name]?.[statistic])
    if (ms > 0) {
      spans.push({ key: AGGREGATE_PHASE_KEYS[name], startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  const unsplit = statValue(row.phases?.unsplit, statistic)
  if (unsplit > 0) {
    spans.push({ key: 'total', startMs: cursor, endMs: cursor + unsplit })
    cursor += unsplit
  }
  return {
    id,
    kind: 'request',
    label: rowLabel(row),
    detail: rowDetail(row, statistic),
    startMs,
    durationMs: cursor,
    status: row.failed > 0 ? 'incomplete' : 'passed',
    phases: spans,
    timingSource: 'aggregate',
    derived,
    children: [],
    payload: row,
  }
}

/** One aggregate row per request key, in backend order. Offsets are the median offset whatever the statistic. */
export function aggregateRoots(entry: RunArchiveTransactionAnatomy, statistic: AggregateStatistic): AnatomyNode<RunArchiveAnatomyRequest>[] {
  return (entry.requests || []).map(row => aggregateNode(row, statistic, row.key, pos(row.offset_ms?.p50_ms), false))
}

/** The muted `Other requests` row: offsets are not meaningful, so it starts at 0. */
export function othersRoot(entry: RunArchiveTransactionAnatomy, statistic: AggregateStatistic = 'avg'): AnatomyNode<RunArchiveAnatomyRequest> | null {
  if (!entry.other_requests) return null
  const node = aggregateNode(entry.other_requests, statistic, 'other-requests', 0, true)
  return { ...node, label: 'Other requests', detail: `${formatCount(entry.other_requests.samples)} samples beyond the row limit; start not meaningful` }
}

export function aggregateSpanMs(roots: AnatomyNode[]): number {
  let span = 0
  const visit = (node: AnatomyNode) => {
    span = Math.max(span, pos(node.startMs) + pos(node.durationMs))
    node.children.forEach(visit)
  }
  roots.forEach(visit)
  return span
}

export function rowsHaveOnlyUnsplit(entry: RunArchiveTransactionAnatomy): boolean {
  const rows = entry.requests || []
  if (!rows.length) return false
  return rows.every(row => (['dns', 'connect', 'tls', 'server', 'download'] as const).every(phase => pos(row.phases?.[phase]?.max_ms) === 0 && pos(row.phases?.[phase]?.avg_ms) === 0))
}

// ---------------------------------------------------------------------------
// Composition strip

export interface CompositionSegment {
  key: 'server' | 'setup' | 'download' | 'unsplit' | 'non_request' | 'overlap'
  label: string
  ms: number
  pct: number
}

export function compositionSegments(decomposition: RunArchiveTimeDecomposition | undefined): { segments: CompositionSegment[]; identityResidual: number } {
  if (!decomposition) return { segments: [], identityResidual: 0 }
  const p: RunArchivePhaseMeans = decomposition.phases
  const total = decomposition.avg_duration_ms
  const setup = (p?.dns_ms || 0) + (p?.connect_ms || 0) + (p?.tls_ms || 0)
  const parts: [CompositionSegment['key'], string, number][] = [
    ['server', 'server', p?.server_ms || 0],
    ['setup', 'setup', setup],
    ['download', 'download', p?.download_ms || 0],
    ['unsplit', 'untimed request time', p?.unsplit_ms || 0],
    ['non_request', 'outside requests', decomposition.non_request_ms || 0],
    ['overlap', 'overlap', -(decomposition.overlap_ms || 0)],
  ]
  const phaseSum = setup + (p?.server_ms || 0) + (p?.download_ms || 0) + (p?.unsplit_ms || 0)
  const identityResidual = total - (phaseSum + (decomposition.non_request_ms || 0) - (decomposition.overlap_ms || 0))
  const segments = parts
    .filter(([, , ms]) => Math.abs(ms) > 1e-9)
    .map(([key, label, ms]) => ({ key, label, ms, pct: total > 0 ? (ms / total) * 100 : 0 }))
  return { segments, identityResidual }
}

export function compositionSentence(name: string, decomposition: RunArchiveTimeDecomposition | undefined, prefix = 'Average '): string {
  const { segments } = compositionSegments(decomposition)
  if (!segments.length) return ''
  const parts = segments.map(segment => `${segment.label} ${formatSignedPct(segment.pct)}`)
  return `${prefix}${name}: ${parts.join(' · ')}`
}

function formatSignedPct(pct: number): string {
  const rounded = Math.round(pct)
  return rounded < 0 ? `−${Math.abs(rounded)}%` : `${rounded}%`
}

// ---------------------------------------------------------------------------
// R2 tail

export const TAIL_COMPONENT_LABELS: Record<TimeAnatomyTailComponentName, string> = {
  dns: 'DNS',
  connect: 'connect',
  tls: 'TLS',
  server: 'server',
  download: 'download',
  unsplit: 'untimed request time',
  non_request: 'time outside requests',
  overlap: 'overlap',
}

export interface TailBar {
  component: TimeAnatomyTailComponentName
  label: string
  deltaMs: number
  sharePct: number | null
  sign: 'positive' | 'negative' | 'zero'
}

export function tailBars(tail: RunArchiveTailAttribution | undefined): TailBar[] {
  return (tail?.delta?.components || []).map(component => ({
    component: component.component,
    label: TAIL_COMPONENT_LABELS[component.component] ?? component.component,
    deltaMs: component.delta_ms,
    sharePct: finite(component.share_pct) ? component.share_pct : null,
    sign: component.delta_ms > 0 ? 'positive' : component.delta_ms < 0 ? 'negative' : 'zero',
  }))
}

export function formatSignedMs(value: number): string {
  const rounded = Math.round(value)
  if (rounded === 0) return '0 ms'
  return `${rounded > 0 ? '+' : '−'}${Math.abs(rounded).toLocaleString()} ms`
}

/** The controller's R2 gates (archive_time_anatomy.go). */
const TAIL_MIN_INSTANCES = 40
const TAIL_MIN_TAIL_JOINED = 3
const TAIL_MIN_MEDIAN_JOINED = 10

/** Copy for a tail that has no headline. Null when the status is ok. */
export function tailStatusCopy(tail: RunArchiveTailAttribution | undefined): string | null {
  if (!tail) return null
  switch (tail.status) {
    case 'insufficient_samples':
      // The controller also says this for a large run whose bands could not be matched to their requests.
      if (!finite(tail.instances) || tail.instances < TAIL_MIN_INSTANCES) {
        return `Tail attribution needs at least ${TAIL_MIN_INSTANCES} completed instances with ${TAIL_MIN_TAIL_JOINED} in the tail; this run has ${formatCount(tail.instances)}.`
      }
      return `Too few instances could be matched to their requests to explain the tail: ${formatCount(tail.tail?.joined)} of ${formatCount(tail.tail?.count)} above P95 and ${formatCount(tail.median?.joined)} of ${formatCount(tail.median?.count)} at or below P50 (needs ${TAIL_MIN_TAIL_JOINED} and ${TAIL_MIN_MEDIAN_JOINED}).`
    case 'no_tail':
      if (finite(tail.threshold_p95_ms) && finite(tail.threshold_p50_ms) && tail.threshold_p95_ms > tail.threshold_p50_ms) {
        return `No tail to explain: no${tail.band_estimate === 'sampled' ? ' sampled' : ''} instance ran slower than P95 (${formatAnatomyMs(tail.threshold_p95_ms)}).`
      }
      return 'No tail to explain: P95 equals the median.'
    case 'low_join_coverage': {
      // Either band below 80% sets the status: name the one with the lower coverage.
      const coverage = (band: RunArchiveTailAttribution['tail'] | undefined) => (band && band.count > 0 ? band.joined / band.count : 1)
      const tailCoverage = coverage(tail.tail)
      const medianCoverage = coverage(tail.median)
      const [name, ratio] = medianCoverage < tailCoverage ? ['median', medianCoverage] : ['tail', tailCoverage]
      return `Only ${Math.round(ratio * 100)}% of ${name} samples could be matched to their requests; attribution shown without a headline.`
    }
    default:
      return null
  }
}

export function bandEstimateCopy(tail: RunArchiveTailAttribution): string {
  if (tail.band_estimate === 'sampled') return `Estimated from a uniform sample of ${formatCount(tail.sampled_instances)} of ${formatCount(tail.instances)}`
  return 'Exact: every completed instance'
}

// ---------------------------------------------------------------------------
// Honesty, empty, coverage and truncation copy

export function honestyNote(entry: RunArchiveTransactionAnatomy, statistic: AggregateStatistic): string {
  if (statistic === 'avg') return 'Each bar shows the average of each phase. Averages add up; rows start at their median offset.'
  const label = STATISTIC_LABELS[statistic]
  const value = formatAnatomyMs(entry.duration?.[`${statistic}_ms`])
  return `Each bar shows the ${label} of each phase on its own. Phase ${label}s don't add up to the transaction's ${label} (${value}). The tail card above shows what slow transactions actually spend extra time on. Rows start at their median offset; concurrent requests overlap.`
}

export const EMPTY_TRANSACTIONS_COPY = 'This run recorded no business transactions. Wrap requests in transactions to see per-transaction anatomy; per-request phase averages are under Diagnostics → Network phases.'
export const UNSPLIT_ONLY_COPY = 'This protocol records no phase timings; bars show request duration only.'
export const LEGACY_IDENTITY_COPY = 'Legacy archive: VUser IDs may be reused, so requests from another VUser may appear.'

export function unjoinedCopy(name: string): string {
  return `Child requests could not be matched to ${name} (no VUser identity or no requests inside its time window).`
}

/** Every matched instance of `name` may have lost requests to the report's processing limits. */
export function partialOnlyCopy(name: string): string {
  return `Every matched instance of ${name} may have lost requests to the report's processing limits, so its breakdown is left out. The Audit view lists which limits were reached.`
}

/** The callout for a transaction with no joined instance: the report's limits when they caused it, else no match. */
export function notJoinedCopy(entry: Pick<RunArchiveTransactionAnatomy, 'name' | 'partial_instances'>): string {
  return (entry.partial_instances ?? 0) > 0 ? partialOnlyCopy(entry.name) : unjoinedCopy(entry.name)
}

/** Plain-language causes for the drill-down's partial and join-may-differ reasons. Unknown codes are never shown. */
const DRILLDOWN_REASON_CAUSES: Record<string, string> = {
  pending_key_cap: 'this VUser had too many requests waiting for their transaction',
  pending_global_cap: 'too many requests across the run were waiting for their transactions',
  identity_cap: 'too many VUsers were being tracked at once',
  work_budget: 'the report reached its processing budget',
}

function drilldownCauses(reasons: readonly string[] | undefined): string {
  const causes = [...new Set((reasons || []).map(reason => DRILLDOWN_REASON_CAUSES[reason]).filter(Boolean))]
  if (causes.length <= 1) return causes[0] || ''
  return `${causes.slice(0, -1).join(', ')} and ${causes[causes.length - 1]}`
}

/** Why a partial drill-down shows no breakdown, from its partial_reasons. */
export function partialDrilldownCopy(reasons: readonly string[] | undefined): string {
  // Once the work budget is spent the report matched no requests to this transaction at all.
  if (reasons?.includes('work_budget')) return 'The report reached its processing budget before this transaction, so it matched no requests to it and left it out of its averages; outside-request time is unknown.'
  const causes = drilldownCauses(reasons)
  return causes
    ? `Some requests of this transaction were not retained by the report because ${causes}; outside-request time is unknown.`
    : 'Some requests of this transaction were not retained by the report; outside-request time is unknown.'
}

/**
 * The caveat for a drill-down whose report join hit run-wide limits. The
 * drill-down replays this VUser alone, so its requests and breakdown are real
 * for the instance, but the report may have counted it differently.
 */
export function joinMayDifferCopy(reasons: readonly string[] | undefined): string {
  const causes = drilldownCauses(reasons)
  if (!causes) return 'The report could not be checked against this view, so it may count this instance differently: fewer matched requests, or left out of its averages.'
  return `During this run ${causes}, so the report may have matched fewer requests to this instance than shown here, or left it out of its averages. This view rebuilds the instance from its own VUser's requests.`
}

/** The drill-down's requests may be incomplete, so no stretch of its window can be called "outside requests". */
export function drilldownIsPartial(resp: Pick<ArchiveTransactionWaterfall, 'partial'>): boolean {
  return resp.partial === true
}

/** The report's view of this instance may differ from the drill-down (and the drill-down is not itself partial). */
export function drilldownJoinMayDiffer(resp: Pick<ArchiveTransactionWaterfall, 'partial' | 'join_may_differ'>): boolean {
  return resp.join_may_differ === true && !drilldownIsPartial(resp)
}

export function selectionNote(name: string): string {
  return `Showing ${name}; time anatomy covers business transactions`
}

export function truncationNotices(anatomy: RunArchiveTimeAnatomy, entry: RunArchiveTransactionAnatomy | undefined): string[] {
  const notices: string[] = []
  const shown = anatomy.transactions?.length || 0
  const notEmitted = anatomy.truncation?.transactions_not_emitted || 0
  if (notEmitted > 0) notices.push(`Showing ${formatCount(shown)} of ${formatCount(shown + notEmitted)} transactions (SLA-failed first).`)
  if (entry?.requests_truncated) {
    const omitted = entry.other_requests?.samples ?? anatomy.truncation?.request_rows_omitted ?? 0
    notices.push(`Showing ${formatCount(entry.requests?.length || 0)} request rows; ${formatCount(omitted)} samples grouped as Other requests.`)
  }
  return notices
}

/**
 * The three coverage callouts: in the audit lens always, otherwise only when
 * non-zero. The outside-requests callout also shows in every lens whenever
 * requests could not be placed (evicted or dropped), even with none outside.
 */
export function coverageNotices(anatomy: RunArchiveTimeAnatomy, lens: ReportLens): string[] {
  const c = anatomy.coverage
  if (!c) return []
  const always = lens === 'audit'
  const notices: string[] = []
  if (always || c.late_request_rows > 0) notices.push(`${formatCount(c.late_request_rows)} requests were written after their transaction and are excluded.`)
  if (always || c.unjoinable_transaction_rows > 0) notices.push(`${formatCount(c.unjoinable_transaction_rows)} transaction rows had no VUser identity.`)
  if ((c.partial_transaction_rows ?? 0) > 0) notices.push(`${formatCount(c.partial_transaction_rows)} transaction rows may have lost requests to the report's processing limits and are left out of the averages.`)
  // Requests evicted from the report's buffer, and requests dropped by its
  // processing limits (work budget, global or per-VUser cap, or released
  // unclaimed once the budget was spent), were neither matched nor counted
  // as outside, so the outside count is then only a lower bound.
  const evicted = Math.max(0, anatomy.truncation?.pending_requests_evicted ?? 0)
  const dropped = Math.max(0, anatomy.truncation?.pending_requests_dropped ?? 0)
  if (always || c.requests_outside_transactions > 0 || evicted + dropped > 0) {
    const outside = formatCount(c.requests_outside_transactions)
    if (evicted + dropped <= 0) {
      notices.push(`${outside} requests ran outside any transaction.`)
    } else {
      const why = dropped > 0 && evicted > 0
        ? "were dropped by the report's processing limits or not kept long enough to tell whether they ran inside one"
        : dropped > 0
          ? "were dropped by the report's processing limits before they could be checked"
          : 'were not kept long enough to tell whether they ran inside one'
      if (c.requests_outside_transactions > 0) notices.push(`At least ${outside} requests ran outside any transaction; ${formatCount(evicted + dropped)} more ${why}.`)
      else notices.push(`${formatCount(evicted + dropped)} requests could not be placed inside or outside a transaction; they ${why}.`)
    }
  }
  return notices
}

/** The Q4 caveat: phase timings that did not fit the request were clamped. */
export function clampedNotice(anatomy: RunArchiveTimeAnatomy): string | null {
  const n = anatomy.coverage?.clamped_request_samples || 0
  if (n <= 0) return null
  return `${formatCount(n)} request samples reported phase timings that did not fit their duration; those phases were clamped, so server and download time may be understated.`
}

export const TRUNCATION_REASON_COPY: Record<TimeAnatomyTruncationReason, string> = {
  transaction_cap: 'The run had more transaction names than the report tracks; later names were not analysed.',
  report_transaction_cap: 'The report shows a limited number of transactions; SLA-failed ones come first.',
  request_row_cap: 'Some transactions had more distinct requests than the report keeps; the rest are grouped as Other requests.',
  occurrence_fold: 'Requests repeated nine or more times in one instance are folded into occurrence 9.',
  pending_key_cap: 'A VUser had too many requests waiting for their transaction; the oldest were not matched.',
  pending_global_cap: 'Too many requests were waiting for their transactions at once; some were not matched.',
  instance_sample: 'Tail bands and representative samples come from a uniform sample of instances.',
  sample_budget: 'The instance sample was reduced to stay within the report memory budget.',
  identity_cap: 'Too many VUsers were being tracked at once; requests held for VUsers dropped to make room were not matched.',
  work_budget: 'The report reached its processing budget; later transactions were not matched to requests and are left out of the averages.',
}

export function truncationReasonCopy(reason: string): string {
  return (TRUNCATION_REASON_COPY as Record<string, string>)[reason] ?? reason
}

export const COVERAGE_LABELS: Record<keyof RunArchiveTimeAnatomy['coverage'], string> = {
  transaction_rows: 'Transaction rows',
  joined_transaction_rows: 'Transaction rows matched to requests',
  transaction_rows_without_requests: 'Transaction rows with no requests inside',
  partial_transaction_rows: 'Transaction rows that may have lost requests (left out of averages)',
  unjoinable_transaction_rows: 'Transaction rows without VUser identity',
  interrupted_transaction_rows: 'Interrupted transaction rows',
  request_rows: 'Request rows',
  requests_in_transactions: 'Requests inside a transaction',
  requests_outside_transactions: 'Requests outside any transaction',
  requests_claimed_across_iteration: 'Requests matched across an iteration boundary',
  late_request_rows: 'Requests written after their transaction',
  requests_ending_after_transaction: 'Requests ending after their transaction',
  clamped_request_samples: 'Request samples with clamped phases',
  retried_request_samples: 'Retried request samples',
  legacy_identity_rows: 'Rows with legacy VUser identity',
  unknown_population_rows: 'Rows of unknown population',
}

export function formatCount(value: number | undefined): string {
  return finite(value) ? value.toLocaleString() : '0'
}

// ---------------------------------------------------------------------------
// R3 drill-down

const DRILLDOWN_PHASES: readonly [keyof RunArchivePhaseMeans, AnatomyPhaseKey][] = [
  ['dns_ms', 'dns'],
  ['connect_ms', 'connect'],
  ['tls_ms', 'tls'],
  ['server_ms', 'wait'],
  ['download_ms', 'receive'],
  ['unsplit_ms', 'total'],
]

function spansFromBackendPhases(phases: RunArchivePhaseMeans | undefined): AnatomyPhaseSpan[] {
  const spans: AnatomyPhaseSpan[] = []
  let cursor = 0
  for (const [field, key] of DRILLDOWN_PHASES) {
    const ms = pos(phases?.[field])
    if (ms > 0) {
      spans.push({ key, startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  return spans
}

function drilldownRequestNode(row: ArchiveWaterfallRow): AnatomyNode<ArchiveWaterfallRow> {
  const spans = spansFromBackendPhases(row.phases)
  const split = spans.some(span => span.key !== 'total')
  const detail = [row.dependency_target, row.http_version ? `HTTP/${row.http_version}` : '', row.connection ? `${row.connection} connection` : ''].filter(Boolean).join(' · ')
  return {
    id: row.event_id || `ordinal-${row.ordinal}`,
    kind: row.population === 'transaction' ? 'transaction' : 'request',
    label: row.action_name || 'Request',
    detail: detail || undefined,
    startMs: pos(row.offset_ms),
    durationMs: pos(row.duration_ms),
    status: row.interrupted ? 'incomplete' : row.success ? 'passed' : 'failed',
    phases: spans,
    timingSource: split ? 'protocol_evidence' : 'duration',
    statusCode: row.status_code,
    connectionReused: row.connection === 'reused' ? true : row.connection === 'new' ? false : undefined,
    errorMessage: row.error_message,
    children: [],
    payload: row,
  }
}

/** A gap gets its own row only when it is material; smaller ones are drawn hatched on the root's track. */
export const MATERIAL_GAP_SHARE = 0.05
export const MATERIAL_GAP_MS = 50

export function isMaterialGap(gapMs: number, spanMs: number): boolean {
  return gapMs >= MATERIAL_GAP_MS || (spanMs > 0 && gapMs >= spanMs * MATERIAL_GAP_SHARE)
}

/** Appends a span, merging it into the previous one when they touch and share a key. */
function pushPhaseSpan(spans: AnatomyPhaseSpan[], key: AnatomyPhaseKey, startMs: number, endMs: number) {
  if (endMs - startMs <= 0) return
  const last = spans[spans.length - 1]
  if (last && last.key === key && Math.abs(last.endMs - startMs) < 1e-9) {
    last.endMs = endMs
    return
  }
  spans.push({ key, startMs, endMs })
}

/**
 * The drill-down's tree: the transaction root, its requests from the backend
 * `phases`, and the time no request was running.
 *
 * The root's own track reads as the decomposition: each stretch of its window
 * takes the phase of the request that was running then (the earliest-starting
 * one where requests overlap), and the stretches with no request are hatched
 * `outside` segments. Only material gaps (see isMaterialGap) also get a row of
 * their own, so a run of sub-millisecond gaps does not flood the list. When the
 * backend truncated the requests, the time after the last one is unknown and
 * is left blank rather than counted as outside. When the drill-down is
 * partial (drilldownIsPartial), requests may be missing anywhere in the
 * window, so no gap is drawn or counted as outside at all.
 */
export function waterfallRootsFromDrilldown(resp: ArchiveTransactionWaterfall): AnatomyNode<ArchiveWaterfallRow>[] {
  const root = resp.root
  const span = Math.max(pos(root.duration_ms), pos(root.elapsed_ms))
  const requests = [...(resp.requests || [])].sort((a, b) => pos(a.offset_ms) - pos(b.offset_ms) || a.ordinal - b.ordinal)
  const children: AnatomyNode<ArchiveWaterfallRow>[] = []
  const projected: AnatomyPhaseSpan[] = []
  const outside: AnatomyTimeSpan[] = []
  let coveredEnd = 0
  let gapIndex = 0
  const partial = drilldownIsPartial(resp)
  const pushGap = (from: number, to: number) => {
    if (partial || to - from <= 0) return
    outside.push({ startMs: from, endMs: to })
    if (!isMaterialGap(to - from, span)) return
    gapIndex += 1
    children.push({
      id: `${root.event_id}-gap-${gapIndex}`,
      kind: 'think',
      label: OUTSIDE_REQUESTS_LABEL,
      detail: 'No request was running',
      startMs: from,
      durationMs: to - from,
      status: 'passed',
      phases: [],
      derived: true,
      children: [],
    })
  }
  for (const request of requests) {
    const start = Math.min(pos(request.offset_ms), span)
    if (start > coveredEnd) pushGap(coveredEnd, start)
    const node = drilldownRequestNode(request)
    children.push(node)
    const end = Math.min(span, start + pos(request.duration_ms))
    // Project the part of this request that no earlier request already covers.
    const segments = node.phases.length ? node.phases : [{ key: 'total' as const, startMs: 0, endMs: pos(request.duration_ms) }]
    for (const segment of segments) {
      const from = Math.max(start + segment.startMs, coveredEnd)
      const to = Math.min(start + segment.endMs, end)
      pushPhaseSpan(projected, segment.key, from, to)
    }
    coveredEnd = Math.max(coveredEnd, end)
  }
  if (requests.length && span > coveredEnd && !resp.truncated) pushGap(coveredEnd, span)

  const rootIsTransaction = root.population !== 'action'
  const rootNode: AnatomyNode<ArchiveWaterfallRow> = rootIsTransaction
    ? {
      id: root.event_id,
      kind: 'transaction',
      label: root.action_name || 'Transaction',
      detail: root.source_transaction,
      startMs: 0,
      durationMs: span,
      status: root.interrupted ? 'incomplete' : root.success ? 'passed' : 'failed',
      phases: projected,
      ...(outside.length ? { outside } : {}),
      statusCode: root.status_code,
      errorMessage: root.error_message,
      children,
      payload: root,
    }
    : { ...drilldownRequestNode(root), startMs: 0, children }
  return [rootNode]
}

/** Depth-first rows for the non-virtualized renderer. */
export function flattenNodes<T>(roots: AnatomyNode<T>[]): { node: AnatomyNode<T>; depth: number }[] {
  const out: { node: AnatomyNode<T>; depth: number }[] = []
  const visit = (node: AnatomyNode<T>, depth: number) => {
    out.push({ node, depth })
    node.children.forEach(child => visit(child, depth + 1))
  }
  roots.forEach(root => visit(root, 0))
  return out
}

export const REPRESENTATIVE_LABELS = { p50: 'Median', p95: 'P95', slowest: 'Slowest', failed: 'Failed' } as const

/** The transaction the Summary line explains: an ok tail with a headline, SLA-failed first. */
export function headlineTransaction(anatomy: RunArchiveTimeAnatomy | null | undefined): RunArchiveTransactionAnatomy | undefined {
  const explained = (anatomy?.transactions || []).filter(entry => entry.tail?.status === 'ok' && entry.tail.headline)
  return explained.find(entry => entry.sla_status === 'failed') || explained[0]
}

function errorStatus(error: unknown): unknown {
  return error && typeof error === 'object' ? (error as { status?: unknown }).status : undefined
}

/** The drill-down endpoint answers 429 (too many in flight for this caller) or 503 (archive busy): worth retrying. */
export function drilldownErrorIsBusy(error: unknown): boolean {
  const status = errorStatus(error)
  return status === 429 || status === 503
}

export const DRILLDOWN_BUSY_COPY = 'The archive is busy rebuilding other transactions. Try again in a moment.'

export function drilldownErrorCopy(error: unknown): string {
  const status = errorStatus(error)
  if (drilldownErrorIsBusy(error)) return DRILLDOWN_BUSY_COPY
  if (status === 400) return 'This transaction could not be located in the archive.'
  if (status === 404) return 'Event no longer in archive.'
  if (status === 413 || status === 422) return 'Archive too large to rebuild this transaction.'
  return getErrorMessage(error, 'Unable to rebuild this transaction.')
}

/**
 * The ordinal hints for a representative's drill-down. The endpoint accepts
 * any span (a far-off hint only costs it a bounded scan), so `ordinal` is sent
 * whenever it is a valid line index, and `first_ordinal` with it when it is
 * valid and not past the ordinal (the one pair the endpoint answers 400). With
 * no valid ordinal nothing is sent: `first_ordinal` alone is also a 400, and
 * the endpoint then finds the event by id.
 */
export function drilldownHints(sample: Pick<RunArchiveRepresentativeSample, 'ordinal' | 'first_ordinal'>): ArchiveTransactionWaterfallParams {
  const validOrdinal = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0
  if (!validOrdinal(sample.ordinal)) return {}
  if (!validOrdinal(sample.first_ordinal) || sample.first_ordinal > sample.ordinal) return { ordinal: sample.ordinal }
  return { ordinal: sample.ordinal, first_ordinal: sample.first_ordinal }
}

/** Plain words for the controller's error classes; `other` (or none) says nothing and is left out. */
const ERROR_CLASS_LABELS: Record<string, string> = {
  timeout: 'timeout',
  connection: 'connection error',
  assertion: 'assertion failed',
  http_4xx: 'HTTP 4xx',
  http_5xx: 'HTTP 5xx',
  resource_limit: 'resource limit',
  session_lost: 'session lost',
}
const UNINFORMATIVE_ERROR_CLASSES = new Set(['', 'other', 'error', 'unknown'])

/** A representative chip: "P95 130 ms", "Failed · HTTP 503", "Failed · timeout" or plain "Failed". */
export function representativeChipLabel(sample: Pick<RunArchiveRepresentativeSample, 'reason' | 'duration_ms' | 'status_code' | 'error_class'>): string {
  const name = REPRESENTATIVE_LABELS[sample.reason] ?? sample.reason
  if (sample.reason !== 'failed') return `${name} ${formatAnatomyMs(sample.duration_ms)}`
  if (sample.status_code && sample.status_code > 0) return `${name} · HTTP ${sample.status_code}`
  const errorClass = (sample.error_class || '').trim().toLowerCase()
  if (UNINFORMATIVE_ERROR_CLASSES.has(errorClass)) return name
  return `${name} · ${ERROR_CLASS_LABELS[errorClass] ?? errorClass.replace(/_/g, ' ')}`
}

/** What the panel's live region reads after the reader picks another transaction. */
export function anatomyAnnouncement(entry: RunArchiveTransactionAnatomy | undefined): string {
  if (!entry) return ''
  if (!(entry.joined_instances > 0)) return notJoinedCopy(entry)
  const text = entry.tail ? (entry.tail.status === 'ok' && entry.tail.headline) || tailStatusCopy(entry.tail) : ''
  return text ? `${entry.name}: ${text}` : entry.name
}

/**
 * A DOM id for a waterfall row, so a caller can scroll it into view. The
 * encoding is lossless (every character outside [A-Za-z0-9-] becomes `_` plus
 * its four-digit UTF-16 code), so distinct keys never share an id.
 */
export function reportPhaseRowId(prefix: string, id: string): string {
  return `${prefix}-${id.replace(/[^A-Za-z0-9-]/g, char => `_${char.charCodeAt(0).toString(16).padStart(4, '0')}`)}`
}
