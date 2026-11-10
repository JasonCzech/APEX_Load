import type { RunArchiveNetworkPhaseSummary } from '../types'
import type { BrowserReplayEvent } from '../types/replay'
import type { RunArchiveAnatomyRequest, RunArchivePhaseStat, RunArchiveTimeAnatomy } from '../types/timeAnatomy'
import { debugSummaryModel, type DebugCompletionSnapshot } from './debugCompletionModel'
import { debugTransactionRows, type DebugTransactionRow } from './debugTransactionModel'
import { ANATOMY_PHASE_LABELS, STATISTIC_LABELS, requestPhaseSplit, type AggregateStatistic } from './runReport/timeAnatomyModel'
import { AGGREGATE_PHASE_KEYS, formatWaterfallMs } from './waterfall/phases'
import { isThinkTimeEvent } from './waterfall/debugTree'

/**
 * Debug vs load amplification (plan R6): one unloaded debug run compared, per
 * request and phase, with a load run's report. Debug timings are re-split with
 * the archive's sequential_clipped_v1 rule so both sides use one phase model.
 */

export type AmplificationStatistic = Extract<AggregateStatistic, 'p50' | 'p95' | 'p99' | 'avg'>
export const AMPLIFICATION_STATISTICS: readonly AmplificationStatistic[] = ['p50', 'p95', 'p99', 'avg']
export const AMPLIFICATION_STATISTIC_LABELS: Record<AmplificationStatistic, string> = {
  p50: STATISTIC_LABELS.p50, p95: STATISTIC_LABELS.p95, p99: STATISTIC_LABELS.p99, avg: STATISTIC_LABELS.avg,
}

export type AmplificationPhase = 'dns' | 'connect' | 'tls' | 'server' | 'download'
export const AMPLIFICATION_PHASES: readonly AmplificationPhase[] = ['dns', 'connect', 'tls', 'server', 'download']
/** The report's Time Anatomy labels for the same archive phases. */
export const AMPLIFICATION_PHASE_LABELS: Record<AmplificationPhase, string> = {
  dns: ANATOMY_PHASE_LABELS[AGGREGATE_PHASE_KEYS.dns],
  connect: ANATOMY_PHASE_LABELS[AGGREGATE_PHASE_KEYS.connect],
  tls: ANATOMY_PHASE_LABELS[AGGREGATE_PHASE_KEYS.tls],
  server: ANATOMY_PHASE_LABELS[AGGREGATE_PHASE_KEYS.server],
  download: ANATOMY_PHASE_LABELS[AGGREGATE_PHASE_KEYS.download],
}
/** Network = connection setup plus download; server is the server's wait. */
export const NETWORK_PHASES: readonly AmplificationPhase[] = ['dns', 'connect', 'tls', 'download']

/** Below this a debug value is too small to divide by. */
export const MIN_FACTOR_BASE_MS = 0.5
/** The archive join's clock slack around a transaction window. */
const CLAIM_SLACK_MS = 2

/** A phase value; null when the side did not measure it (no TTFB, mean-only download). */
export type PhaseVector = Record<AmplificationPhase, number | null>

export type DebugNotComparableReason = 'har_only' | 'redirect' | 'total_only'
export const NOT_COMPARABLE_LABELS: Record<DebugNotComparableReason, string> = {
  har_only: 'HAR timings only: browser phases use a different model',
  redirect: 'Redirect chain: total only',
  total_only: 'Total only: no phase timings retained',
}

export interface DebugRequestSample {
  eventIndex: number
  actionName: string
  sourceStepIndex: number
  startMs: number | null
  durationMs: number
  /** Re-split phases, or null when not comparable. */
  phases: PhaseVector | null
  notComparable?: DebugNotComparableReason
}

export type AmplificationFactor =
  | { kind: 'ratio'; value: number }
  /** The debug value was about zero but load spent time here. */
  | { kind: 'new' }
  | { kind: 'na' }

export type AmplificationBasis = 'anatomy' | 'mean_only' | 'unmatched' | 'not_comparable'

/**
 * Why a request a debug transaction claimed has no row in the load report's
 * anatomy: the transaction is missing (or was left out by the report's
 * transaction cap), or its request row is (folded into Other requests by the
 * row cap, or no row has this step and occurrence).
 */
export type AnatomyMiss = 'transaction_capped' | 'transaction_missing' | 'row_capped' | 'no_row'
export const ANATOMY_MISS_LABELS: Record<AnatomyMiss, string> = {
  transaction_capped: 'left out of the load report (transaction cap)',
  transaction_missing: 'not in the load report',
  row_capped: 'request folded into Other requests (row cap)',
  no_row: 'request not matched in the load report',
}

/**
 * Above this share of load samples without a TTFB split, server and download
 * under load are understated: that time is unsplit, so the row's server and
 * network factors are not shown. The backend's no-TTFB bucket holds both
 * requests that never got a first byte (timeouts, resets) and samples whose
 * protocol records no TTFB at all (gRPC, JDBC, WebSocket), so it is read as a
 * load signal only when the debug run measured a TTFB for the request.
 */
export const MATERIAL_NO_FIRST_BYTE_PCT = 5

export interface AmplificationRow {
  key: string
  debug: DebugRequestSample
  basis: AmplificationBasis
  /** The transaction whose request row matched (anatomy basis). */
  transactionName?: string
  /** True when the match fell back to action name and occurrence only. */
  fallbackKey?: boolean
  occurrence?: number
  /** The debug transaction that claimed the request (innermost), if any. */
  claimedBy?: string
  /** Why a claimed request did not match an anatomy row. */
  anatomyMiss?: AnatomyMiss
  /** The load values at the chosen statistic, per phase. */
  load: PhaseVector | null
  /** The load means: they add up, so the network sum and the summary use them. */
  loadMean: PhaseVector | null
  /**
   * Share of load samples with no TTFB split, when material and the debug
   * run measured a TTFB for the request (anatomy only).
   */
  loadNoFirstBytePct?: number
  /**
   * Neither side split out a TTFB (a protocol without one, such as gRPC or
   * WebSocket): server and download are not compared, setup still is.
   */
  noTtfbSplit?: boolean
  factors: Record<AmplificationPhase, AmplificationFactor>
  /** Server at the chosen statistic (a single phase). */
  server: AmplificationFactor
  /** DNS + connect + TLS + download, from means: phase percentiles do not add. */
  network: AmplificationFactor
  /** The network factor is DNS + connect + TLS only: download was not measured on one side. */
  networkSetupOnly?: boolean
}

export interface AmplificationSummary {
  basis: 'anatomy' | 'mean_only' | 'none'
  requests: number
  /** Matched rows left out of the totals: load often had no TTFB split. */
  excluded: number
  server: AmplificationFactor
  network: AmplificationFactor
  /** No request in the network total had download on both sides: it is connection setup only. */
  networkSetupOnly: boolean
  headline: string
}

export interface AmplificationModel {
  rows: AmplificationRow[]
  summary: AmplificationSummary
  counts: { requests: number; anatomy: number; meanOnly: number; unmatched: number; notComparable: number }
  hasLoadPhaseData: boolean
}

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const pos = (v: unknown) => (finite(v) && v > 0 ? v : 0)
const CONTAINER_TYPES = new Set(['http_sequence', 'control_if', 'control_switch', 'control_loop', 'control_parallel'])
const HTTP_METHODS = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE)$/i

function isRequestEvent(event: BrowserReplayEvent): boolean {
  if (CONTAINER_TYPES.has(event.action_type) || isThinkTimeEvent(event)) return false
  const timings = event.protocol_evidence?.timings
  return Boolean(timings && typeof timings === 'object')
    || Boolean(event.network_entries?.length)
    || event.status_code != null
    || HTTP_METHODS.test(event.method || event.action_type || '')
}

/** Re-splits one debug event with the archive's sequential_clipped_v1 rule. */
export function debugRequestSample(event: BrowserReplayEvent, eventIndex: number): DebugRequestSample {
  const parsedStart = Date.parse(event.started_at)
  const timings = event.protocol_evidence?.timings
  const t = timings && typeof timings === 'object' ? timings as Record<string, unknown> : null
  const durationMs = pos(event.duration_ms) || pos(t?.duration_ms)
  const base = {
    eventIndex,
    actionName: event.action_name || '',
    sourceStepIndex: finite(event.source_step_index) && event.source_step_index > 0 ? event.source_step_index : 0,
    startMs: Number.isFinite(parsedStart) ? parsedStart : null,
    durationMs,
  }
  if (!t) return { ...base, phases: null, notComparable: event.network_entries?.length ? 'har_only' : 'total_only' }
  // One trace spans the whole chain: TTFB and the dial come from different hops.
  if (pos(t.redirect_count) > 0) return { ...base, phases: null, notComparable: 'redirect' }
  if (!(durationMs > 0)) return { ...base, phases: null, notComparable: 'total_only' }
  const split = requestPhaseSplit({ duration_ms: durationMs, dns_ms: pos(t.dns_ms), connect_ms: pos(t.connect_ms), tls_ms: pos(t.tls_ms), ttfb_ms: pos(t.ttfb_ms) })
  const hasTTFB = pos(t.ttfb_ms) > 0
  return {
    ...base,
    phases: {
      dns: split.dns, connect: split.connect, tls: split.tls,
      server: hasTTFB ? split.server : null,
      download: hasTTFB ? split.download : null,
    },
  }
}

interface TransactionInstance { name: string; startMs: number; endMs: number }

/** Transaction executions of the debug run with real start and end times. */
function transactionInstances(rows: readonly DebugTransactionRow[]): TransactionInstance[] {
  const out: TransactionInstance[] = []
  const add = (name: string, row: DebugTransactionRow) => {
    const startMs = row.startedAt ? Date.parse(row.startedAt) : NaN
    const endMs = row.completedAt ? Date.parse(row.completedAt) : NaN
    if (Number.isFinite(startMs) && Number.isFinite(endMs) && endMs >= startMs) out.push({ name, startMs, endMs })
  }
  const visit = (row: DebugTransactionRow) => {
    if (row.kind === 'transaction' || row.kind === 'subtransaction') {
      const executions = row.children.filter(child => child.kind === 'execution')
      if (executions.length && executions.length === row.children.length) {
        for (const execution of executions) {
          add(row.name, execution)
          execution.children.forEach(visit)
        }
        return
      }
      add(row.name, row)
    }
    row.children.forEach(visit)
  }
  rows.forEach(visit)
  return out
}

/** The archive's claim rule: starts inside the window, or just before it while still running. */
function claims(instance: TransactionInstance, sample: DebugRequestSample): boolean {
  if (sample.startMs == null) return false
  if (sample.startMs > instance.endMs + CLAIM_SLACK_MS) return false
  if (sample.startMs >= instance.startMs) return true
  return sample.startMs >= instance.startMs - CLAIM_SLACK_MS && sample.startMs + sample.durationMs > instance.startMs
}

/**
 * `occurrence` is per `<step>|<action>` inside the instance, the archive's
 * numbering. `nameOccurrenceUnique` says no other claimed request of the
 * instance has the same action name and per-step occurrence, which the
 * name-only fallback needs when steps moved (the baseline diff's rule).
 */
interface Claim { instance: TransactionInstance; occurrence: number; nameOccurrenceUnique: boolean }

/** Per request, the instances that claim it with its occurrence inside each. */
function claimsByRequest(samples: readonly DebugRequestSample[], instances: readonly TransactionInstance[]): Map<number, Claim[]> {
  const out = new Map<number, Claim[]>()
  for (const instance of instances) {
    const claimed = samples.filter(sample => claims(instance, sample))
      .sort((a, b) => (a.startMs! - b.startMs!) || a.eventIndex - b.eventIndex)
    const byKey = new Map<string, number>()
    const numbered = claimed.map(sample => {
      const key = `${sample.sourceStepIndex}|${sample.actionName}`
      const occurrence = (byKey.get(key) ?? 0) + 1
      byKey.set(key, occurrence)
      return { sample, occurrence, nameKey: `${sample.actionName}#${occurrence}` }
    })
    const nameKeyCounts = new Map<string, number>()
    for (const { nameKey } of numbered) nameKeyCounts.set(nameKey, (nameKeyCounts.get(nameKey) ?? 0) + 1)
    for (const { sample, occurrence, nameKey } of numbered) {
      const list = out.get(sample.eventIndex) || []
      list.push({ instance, occurrence, nameOccurrenceUnique: nameKeyCounts.get(nameKey) === 1 })
      out.set(sample.eventIndex, list)
    }
  }
  // Innermost (shortest) transaction first: it names the request most precisely.
  out.forEach(list => list.sort((a, b) => (a.instance.endMs - a.instance.startMs) - (b.instance.endMs - b.instance.startMs)))
  return out
}

function occurrenceMatches(row: RunArchiveAnatomyRequest, occurrence: number): boolean {
  return row.occurrence === occurrence || Boolean(row.occurrence_folded && occurrence >= row.occurrence)
}

type AnatomyLookup =
  | { row: RunArchiveAnatomyRequest; claim: Claim; fallbackKey: boolean }
  | { row: null; miss?: AnatomyMiss }

function findAnatomyRow(anatomy: RunArchiveTimeAnatomy | undefined, sample: DebugRequestSample, claimsFor: readonly Claim[]): AnatomyLookup {
  let miss: AnatomyMiss | undefined
  const transactionsCapped = Boolean(anatomy?.truncation?.truncated && (pos(anatomy.truncation.transactions_not_emitted) > 0 || pos(anatomy.truncation.transactions_omitted) > 0))
  for (const claim of claimsFor) {
    const entry = anatomy?.transactions?.find(candidate => candidate.name === claim.instance.name)
    if (!entry) {
      miss ??= transactionsCapped ? 'transaction_capped' : 'transaction_missing'
      continue
    }
    const rows = (entry.requests || []).filter(row => row.action_name === sample.actionName)
    const exact = rows.find(row => (row.source_step_index ?? 0) === sample.sourceStepIndex && occurrenceMatches(row, claim.occurrence))
    if (exact) return { row: exact, claim, fallbackKey: false }
    // Steps moved (the script was edited since the load run): match on the
    // action name and the archive's per-step occurrence, only where that
    // pair is unique on both sides.
    if (claim.nameOccurrenceUnique) {
      const fallback = rows.filter(row => row.occurrence === claim.occurrence)
      if (fallback.length === 1) return { row: fallback[0], claim, fallbackKey: true }
    }
    miss ??= entry.requests_truncated || entry.other_requests ? 'row_capped' : 'no_row'
  }
  return { row: null, miss }
}

/** Share of the row's load samples with no TTFB split (server observed_pct is the TTFB share). */
function noFirstBytePct(row: RunArchiveAnatomyRequest): number | undefined {
  const observed = row.phases?.server?.observed_pct
  if (!finite(observed) || !(row.phases.server.samples > 0)) return undefined
  const pct = Math.max(100 - observed, 0)
  return pct > MATERIAL_NO_FIRST_BYTE_PCT ? pct : undefined
}

function statValue(stat: RunArchivePhaseStat | undefined, statistic: AmplificationStatistic): number | null {
  if (!stat || !(stat.samples > 0)) return null
  const value = statistic === 'avg' ? stat.avg_ms : stat[`${statistic}_ms`]
  return finite(value) ? Math.max(value, 0) : null
}

function anatomyVector(row: RunArchiveAnatomyRequest, statistic: AmplificationStatistic): PhaseVector {
  return {
    dns: statValue(row.phases?.dns, statistic),
    connect: statValue(row.phases?.connect, statistic),
    tls: statValue(row.phases?.tls, statistic),
    server: statValue(row.phases?.server, statistic),
    download: statValue(row.phases?.download, statistic),
  }
}

/**
 * Run-wide means for one action name, across every group of the run. Server
 * is mean TTFB minus mean setup; there is no download split. The setup means
 * also count requests that got no first byte, so the server value is
 * understated when the run had any (the panel says so).
 */
function meanVector(networkPhases: RunArchiveNetworkPhaseSummary | undefined, actionName: string): PhaseVector | null {
  const means = networkPhases?.by_action?.[actionName]
  if (!means || !(pos(means.count) > 0)) return null
  const dns = pos(means.avg_dns_ms), connect = pos(means.avg_connect_ms), tls = pos(means.avg_tls_ms), ttfb = pos(means.avg_ttfb_ms)
  return { dns, connect, tls, server: ttfb > 0 ? Math.max(ttfb - dns - connect - tls, 0) : null, download: null }
}

export function amplificationFactor(debugMs: number | null, loadMs: number | null): AmplificationFactor {
  if (debugMs == null || loadMs == null) return { kind: 'na' }
  if (debugMs < MIN_FACTOR_BASE_MS) return loadMs >= MIN_FACTOR_BASE_MS ? { kind: 'new' } : { kind: 'na' }
  return { kind: 'ratio', value: loadMs / debugMs }
}

/** Sums the phases both sides measured, and says whether download was one. */
function groupSums(debug: PhaseVector, load: PhaseVector, phases: readonly AmplificationPhase[]): { debug: number; load: number; download: boolean } | null {
  let debugSum = 0, loadSum = 0, any = false, download = false
  for (const phase of phases) {
    const d = debug[phase], l = load[phase]
    if (d == null || l == null) continue
    debugSum += d
    loadSum += l
    any = true
    download ||= phase === 'download'
  }
  return any ? { debug: debugSum, load: loadSum, download } : null
}

function groupFactor(debug: PhaseVector | null, load: PhaseVector | null, phases: readonly AmplificationPhase[]): AmplificationFactor {
  if (!debug || !load) return { kind: 'na' }
  const sums = groupSums(debug, load, phases)
  return sums ? amplificationFactor(sums.debug, sums.load) : { kind: 'na' }
}

/**
 * Whether a network factor left download out: one side did not measure it
 * (run-wide means have no download split; a debug request without TTFB has
 * none), so the factor is connection setup (DNS + connect + TLS) only.
 */
function networkSetupOnly(debug: PhaseVector | null, load: PhaseVector | null): boolean {
  const sums = debug && load ? groupSums(debug, load, NETWORK_PHASES) : null
  return Boolean(sums && !sums.download)
}

const NA_FACTORS = (): Record<AmplificationPhase, AmplificationFactor> => ({ dns: { kind: 'na' }, connect: { kind: 'na' }, tls: { kind: 'na' }, server: { kind: 'na' }, download: { kind: 'na' } })

export function formatFactor(factor: AmplificationFactor): string {
  if (factor.kind === 'new') return 'new'
  if (factor.kind === 'na') return 'n/a'
  const value = factor.value
  return `×${value >= 10 ? value.toFixed(0) : value.toFixed(1)}`
}

export function formatPhaseMs(value: number | null): string {
  return value == null ? '—' : formatWaterfallMs(value)
}

/**
 * Totals over the matched rows, from means: phase percentiles come from
 * different samples and do not add up, across phases or across requests.
 */
function summarize(rows: readonly AmplificationRow[], statistic: AmplificationStatistic, shakeout: boolean): AmplificationSummary {
  const anatomyRows = rows.filter(row => row.basis === 'anatomy')
  const matched = anatomyRows.length ? anatomyRows : rows.filter(row => row.basis === 'mean_only')
  const basis = anatomyRows.length ? 'anatomy' : matched.length ? 'mean_only' : 'none'
  if (basis === 'none') {
    return { basis, requests: 0, excluded: 0, server: { kind: 'na' }, network: { kind: 'na' }, networkSetupOnly: false, headline: 'No debug request could be matched to this load run.' }
  }
  const basisRows = matched.filter(row => row.loadNoFirstBytePct == null)
  const excluded = matched.length - basisRows.length
  let rowsWithDownload = 0, rowsWithoutDownload = 0
  const total = (phases: readonly AmplificationPhase[], countDownload = false) => {
    let debug = 0, load = 0, any = false
    for (const row of basisRows) {
      const sums = row.loadMean && row.debug.phases ? groupSums(row.debug.phases, row.loadMean, phases) : null
      if (!sums) continue
      debug += sums.debug
      load += sums.load
      any = true
      if (countDownload) {
        if (sums.download) rowsWithDownload++
        else rowsWithoutDownload++
      }
    }
    return any ? amplificationFactor(debug, load) : { kind: 'na' } as AmplificationFactor
  }
  const server = total(['server'])
  const network = total(NETWORK_PHASES, true)
  // Run-wide means have no download split: the "network" total is then
  // connection setup only, and is named so.
  const setupOnly = rowsWithoutDownload > 0 && rowsWithDownload === 0
  const networkName = setupOnly ? 'connection setup (DNS, connect, TLS)' : 'network'

  const describe = (name: string, factor: AmplificationFactor) =>
    factor.kind === 'ratio' ? `${name} ${formatFactor(factor)}` : factor.kind === 'new' ? `${name} new` : `${name} n/a`
  const count = basis === 'anatomy'
    ? `${basisRows.length} ${basisRows.length === 1 ? 'request' : 'requests'} matched by transaction`
    : `${basisRows.length} matched ${basisRows.length === 1 ? 'request' : 'requests'}`
  // Mean-only rows have their own factors in the table, but the totals use
  // only the transaction rows once any exists: say what is left out.
  const meanOnlyLeftOut = basis === 'anatomy' ? rows.filter(row => row.basis === 'mean_only').length : 0
  const where = shakeout ? 'in the shakeout run (unloaded)' : 'under load'
  const notes: string[] = []
  if (basis === 'anatomy' && statistic !== 'avg') {
    notes.push(`Phase percentiles do not add up, so these totals use means; the table shows ${AMPLIFICATION_STATISTIC_LABELS[statistic]} per phase.`)
  }
  if (setupOnly) {
    notes.push(basis === 'mean_only' ? 'Run-wide means have no download split, so download is not compared.' : 'Download was not measured on both sides, so it is not compared.')
  } else if (rowsWithoutDownload > 0) {
    notes.push(`The network total leaves out download for ${rowsWithoutDownload} ${rowsWithoutDownload === 1 ? 'request' : 'requests'} not measured on both sides.`)
  }
  if (meanOnlyLeftOut) {
    notes.push(`${meanOnlyLeftOut} ${meanOnlyLeftOut === 1 ? 'request' : 'requests'} compared only with run-wide means ${meanOnlyLeftOut === 1 ? 'is' : 'are'} not in these totals.`)
  }
  if (excluded) {
    notes.push(`${excluded} matched ${excluded === 1 ? 'request is' : 'requests are'} left out: under load ${excluded === 1 ? 'it' : 'they'} often had no TTFB split (timeouts, or a protocol without TTFB), so server and download are understated.`)
  }
  return {
    basis, requests: basisRows.length, excluded, server, network, networkSetupOnly: setupOnly,
    headline: `${describe('Server', server)}, ${describe(networkName, network)} ${where} (mean load vs debug, summed over ${count}${basis === 'mean_only' ? '; run-wide means only' : ''}).${notes.length ? ` ${notes.join(' ')}` : ''}`,
  }
}

export interface AmplificationLoadReport {
  time_anatomy?: RunArchiveTimeAnatomy
  network_phases?: RunArchiveNetworkPhaseSummary
}

/** Only the two report fields the comparison reads, so a cached report stays small. */
export function amplificationLoadReportOf(report: AmplificationLoadReport | null | undefined): AmplificationLoadReport {
  return { time_anatomy: report?.time_anatomy, network_phases: report?.network_phases }
}

/**
 * Fetches a load run's report for the comparison: without the release-wide
 * trend (the controller would collect every run of the release for it), and
 * kept to the fields the comparison reads.
 */
export async function fetchAmplificationLoadReport(
  getReport: (testId: string, options: { includeReleaseTrend: false }) => Promise<AmplificationLoadReport>,
  testId: string,
): Promise<AmplificationLoadReport> {
  return amplificationLoadReportOf(await getReport(testId, { includeReleaseTrend: false }))
}

/** Debug requests of a finished run, in event order. */
export function debugRequestSamples(snapshot: DebugCompletionSnapshot): DebugRequestSample[] {
  return debugSummaryModel(snapshot).events
    .filter(({ event }) => isRequestEvent(event))
    .map(({ event, index }) => debugRequestSample(event, index))
}

export interface AmplificationOptions {
  /** The load run is a single-VUser script shakeout: not a loaded run. */
  shakeout?: boolean
}

export function buildAmplificationModel(snapshot: DebugCompletionSnapshot, report: AmplificationLoadReport | null | undefined, statistic: AmplificationStatistic, options: AmplificationOptions = {}): AmplificationModel {
  const samples = debugRequestSamples(snapshot)
  // Occurrences count every request a transaction claims, comparable or not,
  // exactly as the archive numbers its request rows.
  const claimMap = claimsByRequest(samples, transactionInstances(debugTransactionRows(snapshot)))
  const anatomy = report?.time_anatomy
  const networkPhases = report?.network_phases
  const rows = samples.map((sample): AmplificationRow => {
    const key = `request-${sample.eventIndex}`
    const claimsFor = claimMap.get(sample.eventIndex) || []
    const claimedBy = claimsFor[0]?.instance.name
    if (!sample.phases) {
      return { key, debug: sample, basis: 'not_comparable', ...(claimedBy ? { claimedBy } : {}), load: null, loadMean: null, factors: NA_FACTORS(), server: { kind: 'na' }, network: { kind: 'na' } }
    }
    const match = findAnatomyRow(anatomy, sample, claimsFor)
    let load: PhaseVector | null = null
    let loadMean: PhaseVector | null = null
    let basis: AmplificationBasis = 'unmatched'
    let noFirstByte: number | undefined
    let noTtfbSplit = false
    if (match.row) {
      load = anatomyVector(match.row, statistic)
      loadMean = anatomyVector(match.row, 'avg')
      noFirstByte = noFirstBytePct(match.row)
      if (noFirstByte != null && sample.phases.server == null) {
        // The debug run had no TTFB either: the protocol records none, which
        // says nothing about load. Server and download stay n/a (debug has
        // neither); DNS, connect and TLS are still compared.
        noFirstByte = undefined
        noTtfbSplit = true
      }
      basis = 'anatomy'
    } else {
      load = loadMean = meanVector(networkPhases, sample.actionName)
      if (load) basis = 'mean_only'
    }
    const factors = NA_FACTORS()
    if (load) for (const phase of AMPLIFICATION_PHASES) factors[phase] = amplificationFactor(sample.phases[phase], load[phase])
    if (noFirstByte != null) {
      // That time is unsplit under load: server and download are understated.
      factors.server = { kind: 'na' }
      factors.download = { kind: 'na' }
    }
    return {
      key, debug: sample, basis, load, loadMean, factors,
      ...(claimedBy ? { claimedBy } : {}),
      ...(match.row ? { transactionName: match.claim.instance.name, fallbackKey: match.fallbackKey, occurrence: match.claim.occurrence } : {}),
      ...(!match.row && match.miss ? { anatomyMiss: match.miss } : {}),
      ...(noFirstByte != null ? { loadNoFirstBytePct: noFirstByte } : {}),
      ...(noTtfbSplit ? { noTtfbSplit } : {}),
      server: noFirstByte != null ? { kind: 'na' } : groupFactor(sample.phases, load, ['server']),
      network: noFirstByte != null ? { kind: 'na' } : groupFactor(sample.phases, loadMean, NETWORK_PHASES),
      ...(noFirstByte == null && networkSetupOnly(sample.phases, loadMean) ? { networkSetupOnly: true } : {}),
    }
  })
  return {
    rows,
    summary: summarize(rows, statistic, Boolean(options.shakeout)),
    counts: {
      requests: rows.length,
      anatomy: rows.filter(row => row.basis === 'anatomy').length,
      meanOnly: rows.filter(row => row.basis === 'mean_only').length,
      unmatched: rows.filter(row => row.basis === 'unmatched').length,
      notComparable: rows.filter(row => row.basis === 'not_comparable').length,
    },
    hasLoadPhaseData: Boolean(anatomy?.transactions?.length || Object.keys(networkPhases?.by_action || {}).length),
  }
}
