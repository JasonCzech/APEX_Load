/**
 * Live phase arithmetic shared by the live waterfall, its drawer and the
 * connection health tiles. Every average on the wire divides by every sample
 * (pkg/engine/phase_observations.go), so the observed counts turn them into
 * "when it happened" figures.
 */
import type { PhaseObservations, PhasePercentiles, PhasePercentileSet, TxMetrics } from '../types'

const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined)
const pos = (v: unknown) => Math.max(0, num(v) ?? 0)

/** One transaction's average phase split; the six phases add up to `avgMs`. */
export interface LivePhaseVector {
  dns: number
  connect: number
  tls: number
  /** TTFB minus setup. */
  server: number
  /** Derived: the rest of the average after every other phase, clamped at 0. */
  download: number
  /** Samples without a first byte (timeouts, protocols with no TTFB): unsplit. */
  noResponse: number
  avgMs: number
  /**
   * Where `server` came from: the controller's split, TTFB − setup, nothing
   * (no sample got a first byte), or 'unknown': the record carries no phase
   * figures at all (a window restored from stored history), so every phase
   * is 0 and only `avgMs` is known.
   */
  serverSource: 'split' | 'ttfb' | 'none' | 'unknown'
}

type PhaseFields = Pick<TxMetrics, 'avg_dns_ms' | 'avg_connect_ms' | 'avg_tls_ms' | 'avg_ttfb_ms' | 'avg_server_ms' | 'avg_no_response_ms' | 'phase_observed'>
type PercentileFields = Pick<TxMetrics, 'avg_ms' | 'p50_ms' | 'p90_ms' | 'p95_ms' | 'p99_ms'>

/** Whether the record carries any phase figure or phase observation. */
export function txPhasesReported(tx: Partial<PhaseFields>): boolean {
  return [tx.avg_dns_ms, tx.avg_connect_ms, tx.avg_tls_ms, tx.avg_ttfb_ms, tx.avg_server_ms, tx.avg_no_response_ms].some(v => num(v) !== undefined)
    || Boolean(tx.phase_observed)
}

/**
 * Whether the percentiles are measured: a record with an average but every
 * percentile 0 carries none (Go always sends p50/p90/p99, so a record built
 * without them reads 0).
 */
export function txPercentilesReported(tx: Partial<PercentileFields>): boolean {
  if (!(pos(tx.avg_ms) > 0)) return true
  return [tx.p50_ms, tx.p90_ms, tx.p95_ms, tx.p99_ms].some(v => pos(v) > 0)
}

/**
 * A record with neither phase figures nor percentiles: the controller rebuilt
 * this window from stored interval history (after a restart or failover),
 * which keeps only name, rate, counts and the average. A generator that
 * predates phase observations still sends percentiles, so it is not this.
 */
export function txRestoredWithoutPhases(tx: Partial<PhaseFields & PercentileFields>): boolean {
  return !txPhasesReported(tx) && !txPercentilesReported(tx)
}

/** Shown where a restored window's phases would be. */
export const PHASES_NOT_REPORTED_TEXT = 'Phases not reported for this window'

/**
 * The split for one transaction. TxMetrics carries no download field, so
 * download = avg − dns − connect − tls − server − no_response (clamped), which
 * equals the controller's duration − TTFB split whenever phases fit the
 * duration. Without `avg_server_ms` (an older generator) server falls back to
 * TTFB − setup; with no TTFB at all the time after setup is unsplit.
 */
export function txPhaseVector(tx: Pick<TxMetrics, 'avg_ms' | 'avg_dns_ms' | 'avg_connect_ms' | 'avg_tls_ms' | 'avg_ttfb_ms' | 'avg_server_ms' | 'avg_no_response_ms'> & Partial<Pick<TxMetrics, 'phase_observed' | 'p50_ms' | 'p90_ms' | 'p95_ms' | 'p99_ms'>>): LivePhaseVector {
  const avgMs = pos(tx.avg_ms)
  // Nothing to split: not "no first byte", the split is simply unknown.
  if (txRestoredWithoutPhases(tx)) {
    return { dns: 0, connect: 0, tls: 0, server: 0, download: 0, noResponse: 0, avgMs, serverSource: 'unknown' }
  }
  const dns = pos(tx.avg_dns_ms)
  const connect = pos(tx.avg_connect_ms)
  const tls = pos(tx.avg_tls_ms)
  const ttfb = pos(tx.avg_ttfb_ms)
  const setup = dns + connect + tls
  const splitServer = num(tx.avg_server_ms)
  const splitNoResponse = num(tx.avg_no_response_ms)
  let server: number
  let noResponse: number
  let serverSource: LivePhaseVector['serverSource']
  if (splitServer !== undefined) {
    server = Math.max(0, splitServer)
    noResponse = Math.max(0, splitNoResponse ?? 0)
    serverSource = 'split'
  } else if (ttfb > 0) {
    server = Math.max(0, ttfb - setup)
    noResponse = Math.max(0, splitNoResponse ?? 0)
    serverSource = 'ttfb'
  } else {
    server = 0
    noResponse = Math.max(0, splitNoResponse ?? avgMs - setup)
    serverSource = 'none'
  }
  const download = Math.max(0, avgMs - setup - server - noResponse)
  return { dns, connect, tls, server, download, noResponse, avgMs, serverSource }
}

/** "When it happened" figures from observed counts; undefined when unknown. */
export interface PhaseObservationStats {
  samples: number
  /**
   * 0..100: share of the samples that could time connection setup that opened
   * no new connection (see `reuseScope`). With scope 'bounded' this is the
   * upper end of the range and `reuseMinPct` the lower end.
   */
  reusePct?: number
  /** Lower end of the reuse range when `reuseScope` is 'bounded'. */
  reuseMinPct?: number
  /**
   * Which samples reuse is over. 'all': every sample got a first byte, so
   * every one could time setup. 'first_byte': the samples with no first byte
   * did no setup at all (protocols that time none, such as gRPC, JDBC or
   * messaging, or a timeout on a reused connection), so they are left out.
   * 'bounded': some samples with no first byte did setup, so how many of them
   * could time it is unknown; reuse is a range over every possible count.
   */
  reuseScope?: 'all' | 'first_byte' | 'bounded'
  /** Samples reuse divides by ('all', 'first_byte'), or the fewest possible ('bounded'). */
  reuseSamples?: number
  newConnectionSamples?: number
  dnsSamples?: number
  tlsSamples?: number
  /**
   * Average TLS time per sample that had any handshake. A sample is a whole
   * transaction: one with several handshakes (an authored transaction's child
   * requests) counts their sum, so this is not the cost of one handshake.
   */
  avgHandshakeMs?: number
  /** Average DNS time per sample that looked any name up (several lookups count their sum). */
  avgDnsLookupMs?: number
  /** Average connect time per sample that opened any connection (several connections count their sum). */
  avgConnectMs?: number
  noTtfbSamples?: number
  /**
   * True when observations arrived but no sample timed any setup phase or a
   * first byte: only HTTP and network protocols time DNS, connect and TLS, so
   * gRPC, JDBC, messaging, WebSocket and browser samples carry nothing to
   * count. Reuse and setup figures are then left undefined, not 100% / none.
   */
  setupUnmeasured?: boolean
}

/** Text for a figure that could not be measured for the window's protocols. */
export const SETUP_UNMEASURED_TEXT = 'Not measured for this protocol'
export const SETUP_UNMEASURED_HINT = 'No sample timed DNS, connect, TLS or a first byte (only HTTP and network protocols do)'

export function phaseObservationStats(observed: PhaseObservations | undefined, samples: number, averages: { dns?: number; connect?: number; tls?: number }): PhaseObservationStats {
  const n = Math.max(0, Math.floor(num(samples) ?? 0))
  if (!observed || n <= 0) return { samples: n }
  const connect = Math.min(n, pos(observed.connect))
  const dns = Math.min(n, pos(observed.dns))
  const tls = Math.min(n, pos(observed.tls))
  const noTtfb = num(observed.no_ttfb) === undefined ? undefined : pos(observed.no_ttfb)
  if (noTtfb !== undefined && noTtfb >= n && connect === 0 && dns === 0 && tls === 0) {
    return { samples: n, noTtfbSamples: noTtfb, setupUnmeasured: true }
  }
  const perObserved = (avg: number | undefined, count: number) => (count > 0 ? (pos(avg) * n) / count : undefined)
  // Reuse is over the samples that could time setup: a sample with no first
  // byte that recorded no setup may be a protocol that times none, and
  // counting it as "opened no new connection" would push reuse up in a
  // mixed-protocol window.
  const missing = noTtfb ?? 0
  const withFirstByte = Math.max(0, n - missing)
  const capableAtLeast = Math.max(connect, dns, tls, withFirstByte)
  const noTtfbSetup = pos(observed.no_ttfb_setup_ms)
  let reuse: Pick<PhaseObservationStats, 'reusePct' | 'reuseMinPct' | 'reuseScope' | 'reuseSamples'>
  if (missing <= 0) {
    reuse = { reusePct: (1 - connect / n) * 100, reuseScope: 'all', reuseSamples: n }
  } else if (noTtfbSetup <= 0 && capableAtLeast <= withFirstByte) {
    reuse = { reusePct: (1 - connect / withFirstByte) * 100, reuseScope: 'first_byte', reuseSamples: withFirstByte }
  } else {
    const fewest = Math.max(1, capableAtLeast)
    reuse = { reusePct: (1 - connect / n) * 100, reuseMinPct: (1 - connect / fewest) * 100, reuseScope: 'bounded', reuseSamples: fewest }
  }
  return {
    samples: n,
    ...reuse,
    newConnectionSamples: connect,
    dnsSamples: dns,
    tlsSamples: tls,
    avgHandshakeMs: perObserved(averages.tls, tls),
    avgDnsLookupMs: perObserved(averages.dns, dns),
    avgConnectMs: perObserved(averages.connect, connect),
    noTtfbSamples: noTtfb,
  }
}

export function txObservationStats(tx: TxMetrics): PhaseObservationStats {
  return phaseObservationStats(tx.phase_observed, tx.count, { dns: tx.avg_dns_ms, connect: tx.avg_connect_ms, tls: tx.avg_tls_ms })
}

const pct1 = (v: number) => `${v.toFixed(1)}%`

/** The reuse figure: one share, or a range when it is bounded. */
export function reuseValueText(stats: PhaseObservationStats, format: (v: number) => string = pct1): string | undefined {
  if (stats.reusePct === undefined) return undefined
  if (stats.reuseScope === 'bounded' && stats.reuseMinPct !== undefined && Math.abs(stats.reusePct - stats.reuseMinPct) >= 0.05) {
    return `${format(stats.reuseMinPct)}–${format(stats.reusePct)}`
  }
  return format(stats.reusePct)
}

/**
 * How the reuse figure is counted: a sample is one whole transaction, and one
 * new connection by any of its requests counts the transaction as opening one.
 */
export const REUSE_TRANSACTION_UNIT_TEXT = 'a transaction that opened even one new connection counts as opening one, so request-level reuse can be much higher'

/** What the reuse figure is a share of. */
export function reuseBasisText(stats: PhaseObservationStats): string {
  const n = stats.samples.toLocaleString()
  const missing = (stats.noTtfbSamples ?? 0).toLocaleString()
  switch (stats.reuseScope) {
    case 'first_byte':
      return `of ${(stats.reuseSamples ?? 0).toLocaleString()} transaction samples with a first byte opened no new connection; ${missing} with no first byte and no setup are left out (protocols that time no setup, or timeouts on a reused connection); ${REUSE_TRANSACTION_UNIT_TEXT}`
    case 'bounded':
      return `of the transaction samples that could open a connection opened none: ${missing} of ${n} had no first byte and some of them did setup, so between ${(stats.reuseSamples ?? 0).toLocaleString()} and ${n} samples could time setup; ${REUSE_TRANSACTION_UNIT_TEXT}`
    default:
      return `of ${n} transaction samples opened no new connection; ${REUSE_TRANSACTION_UNIT_TEXT}`
  }
}

/** The statistic the live waterfall's bars show. */
export type LivePhaseStatistic = 'avg' | 'p50' | 'p95' | 'p99'
export type LivePhasePercentile = Exclude<LivePhaseStatistic, 'avg'>

export const LIVE_PHASE_STATISTIC_LABELS: Record<LivePhaseStatistic, string> = { avg: 'Average', p50: 'P50', p95: 'P95', p99: 'P99' }

/** The phases with a percentile on the wire, in waterfall order. */
export const PERCENTILE_PHASES = ['dns', 'connect', 'tls', 'server', 'download'] as const
export type PercentilePhase = (typeof PERCENTILE_PHASES)[number]

export const PERCENTILE_PHASE_LABELS: Record<PercentilePhase, string> = {
  dns: 'DNS', connect: 'Connect', tls: 'TLS', server: 'Server (TTFB − setup)', download: 'Download',
}

/** Shown where per-phase percentiles would be but a generator sent none. */
export const PHASE_PERCENTILES_NOT_REPORTED_TEXT = 'Per-phase percentiles not reported this window: a contributing load generator predates them, or the window had too many transactions and actions to keep them all'

/** Whether the record carries per-phase percentiles (absent means unknown, never zero). */
export function phasePercentilesReported(value: { phase_percentiles?: PhasePercentileSet } | undefined): boolean {
  return Boolean(value?.phase_percentiles && typeof value.phase_percentiles === 'object')
}

/**
 * One phase's percentile in ms, or undefined when the set is unknown or no
 * sample spent time in the phase.
 */
export function phasePercentileMs(set: PhasePercentileSet | undefined, phase: PercentilePhase, statistic: LivePhasePercentile): number | undefined {
  const row: PhasePercentiles | undefined = set?.[phase]
  if (!row || !(pos(row.samples) > 0)) return undefined
  const value = statistic === 'p50' ? row.p50_ms : statistic === 'p95' ? row.p95_ms : row.p99_ms
  return num(value)
}

/**
 * The bars for a percentile statistic: each phase's own percentile over the
 * samples that spent time in it, laid end to end. Percentiles do not add, so
 * the sum is not the transaction's percentile, and there is no no-TTFB part.
 * undefined when the record carries no per-phase percentiles.
 */
export function txPhasePercentileVector(tx: Pick<TxMetrics, 'phase_percentiles'>, statistic: LivePhasePercentile): LivePhaseVector | undefined {
  const set = tx.phase_percentiles
  if (!set) return undefined
  const value = (phase: PercentilePhase) => Math.max(0, phasePercentileMs(set, phase, statistic) ?? 0)
  const dns = value('dns')
  const connect = value('connect')
  const tls = value('tls')
  const server = value('server')
  const download = value('download')
  return { dns, connect, tls, server, download, noResponse: 0, avgMs: dns + connect + tls + server + download, serverSource: set.server ? 'split' : 'none' }
}
