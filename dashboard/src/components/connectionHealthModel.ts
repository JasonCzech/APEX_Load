/**
 * L4: fleet connection health for the latest live window. Phase counts cover
 * business-transaction samples (total_transactions + total_errors); a sample
 * that opened several connections counts once.
 */
import type { LiveMetrics, PhasePercentileSet, SystemResources } from '../types'
import { phaseObservationStats, txRestoredWithoutPhases, type PhaseObservationStats } from './livePhaseModel'

/** Ephemeral port use at or above this share is flagged as an exhaustion risk. */
export const EPHEMERAL_PORT_WARNING_PCT = 80

export interface PortPressure {
  value: number
  /** The generator with the highest value, or undefined for fleet resources. */
  generatorId?: string
  /**
   * The JSON omits a zero count (omitempty); when the same snapshot reported
   * other TCP diagnostics, an absent socket-table count (TIME_WAIT) is a
   * measured 0. Never set for the port share (see worst).
   */
  inferredZero?: boolean
}

export interface ConnectionHealth {
  samples: number
  stats: PhaseObservationStats
  /** False when the window has samples but no phase observations (an older generator). */
  observed: boolean
  /** Seconds the window covers: total_transactions / tps, else undefined (assume 1 s). */
  windowSeconds?: number
  newConnectionsPerSec?: number
  timeWait?: PortPressure
  ephemeralPortPct?: PortPressure
  /**
   * The window was rebuilt from stored interval history (after a controller
   * restart or failover, or for an ended run reopened after one). History
   * keeps no resources (so no socket diagnostics) and phases only for rows
   * written with the network-phase column, so a missing figure is the stored
   * row's, not a generator's.
   */
  fromHistory: boolean
  /**
   * The fleet's per-phase percentiles; undefined when unknown (a load
   * generator predates them, or the window was restored from history).
   */
  phasePercentiles?: PhasePercentileSet
}

/**
 * Whether the window came from stored history, by the signal the live
 * waterfall uses: every transaction record carries neither phases nor
 * percentiles (a live generator always sends percentiles). An ended run's
 * compacted tail has no transactions; it is from history when it also has
 * no resources at all, which every live window carries.
 */
export function liveWindowFromHistory(metrics: LiveMetrics, options: { runFinished?: boolean } = {}): boolean {
  const records = Object.values(metrics.transactions ?? {}).filter(tx => tx && finite(tx.count) && tx.count > 0)
  if (records.length) return records.every(tx => txRestoredWithoutPhases(tx))
  return Boolean(options.runFinished) && !metrics.resources && Object.keys(metrics.generator_resources ?? {}).length === 0
}

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/**
 * Whether a snapshot carried the TCP diagnostics block. Go drops zero counts
 * (omitempty), so an established count is the one field present whenever the
 * generator read its socket table with any connection open.
 */
const reportsTcpDiagnostics = (r: SystemResources | undefined) => Boolean(r && finite(r.tcp_established) && r.tcp_established > 0)

/**
 * The highest value across generators, else the fleet value. With
 * `inferZero`, an absent value in a snapshot that read its socket table is a
 * measured 0: only for counts taken from that same table (TIME_WAIT). The
 * port share also needs the local port range, which a generator can fail to
 * read on its own (collectTCPDiagnostics then leaves it out), so its absence
 * is never read as 0.
 */
function worst(metrics: LiveMetrics, pick: (r: SystemResources) => number | undefined, inferZero: boolean): PortPressure | undefined {
  let best: PortPressure | undefined
  let zero: PortPressure | undefined
  for (const [key, snapshot] of Object.entries(metrics.generator_resources ?? {})) {
    const value = snapshot?.resources ? pick(snapshot.resources) : undefined
    if (finite(value) && (!best || value > best.value)) best = { value, generatorId: snapshot.generator_id || key }
    else if (inferZero && !finite(value) && !zero && reportsTcpDiagnostics(snapshot?.resources)) zero = { value: 0, inferredZero: true }
  }
  if (best) return best
  const fleet = metrics.resources ? pick(metrics.resources) : undefined
  if (finite(fleet)) return { value: fleet }
  if (!inferZero) return undefined
  return zero ?? (reportsTcpDiagnostics(metrics.resources) ? { value: 0, inferredZero: true } : undefined)
}

export function connectionHealth(metrics: LiveMetrics | undefined, options: { runFinished?: boolean } = {}): ConnectionHealth | null {
  if (!metrics) return null
  const success = finite(metrics.total_transactions) ? Math.max(0, metrics.total_transactions) : 0
  const failed = finite(metrics.total_errors) ? Math.max(0, metrics.total_errors) : 0
  const samples = success + failed
  const stats = phaseObservationStats(metrics.phase_observed, samples, { dns: metrics.avg_dns_ms, connect: metrics.avg_connect_ms, tls: metrics.avg_tls_ms })
  // tps = successes / window, so the window length follows from both.
  const windowSeconds = success > 0 && finite(metrics.tps) && metrics.tps > 0 ? success / metrics.tps : undefined
  const newConnectionsPerSec = stats.newConnectionSamples === undefined ? undefined : stats.newConnectionSamples / (windowSeconds ?? 1)
  return {
    samples,
    stats,
    observed: Boolean(metrics.phase_observed) && samples > 0,
    windowSeconds,
    newConnectionsPerSec,
    timeWait: worst(metrics, r => r.tcp_time_wait, true),
    ephemeralPortPct: worst(metrics, r => r.ephemeral_port_utilization_pct, false),
    fromHistory: liveWindowFromHistory(metrics, options),
    phasePercentiles: metrics.phase_percentiles && typeof metrics.phase_percentiles === 'object' ? metrics.phase_percentiles : undefined,
  }
}
