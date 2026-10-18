import { IntervalSummaryRecord, LiveMetrics, MetricsHistoryPoint, ResourceSample, TestStatus } from '../types'
import { resourceImpactHistoryValues } from './generatorImpact'
import { routeLatencySeriesValuesForTarget } from './latencySeries'
import { groupTransactionTpsSeriesKey, transactionChartSeriesKey } from './transactionChartSeries'
import {
  executionChartMinuteRangeValue,
  type ExecutionChartTimeRange,
  type ExecutionChartZoomRange,
} from './testExecutionChartZoomModel'

export interface ExecutionChartSummary {
  avgTps: number
  /** Requests per second over the same points, when they carry a request rate. */
  avgRequestRate?: number
  avgLatency: number
  avgError: number
  maxVusers: number
}

function rounded(value: number | undefined, precision = 10) {
  return Math.round((value ?? 0) * precision) / precision
}

// roundedOptional rounds a present value but preserves `undefined`, so a metric
// the server omitted (e.g. a group's p95) stays a gap rather than becoming a
// misleading 0. This keeps the live mapper consistent with the archive mapper,
// which passes raw values through.
function roundedOptional(value: number | undefined, precision = 10) {
  return typeof value === 'number' ? Math.round(value * precision) / precision : undefined
}

function roundedHistoryValues(values: Partial<MetricsHistoryPoint>) {
  return Object.entries(values).reduce((acc, [key, value]) => {
    if (typeof value === 'number') {
      acc[key] = rounded(value)
    }
    return acc
  }, {} as Record<string, number>)
}

export function mapExecutionMetricToHistoryPoint(metric: LiveMetrics): MetricsHistoryPoint {
  const point: MetricsHistoryPoint = {
    time: new Date(metric.timestamp).toLocaleTimeString(),
    timestampMs: Date.parse(metric.timestamp),
    vusers: metric.active_vusers,
    tps: rounded(metric.tps),
    requestRate: rounded(metric.request_rate),
    targetTps: metric.target_tps,
    avgMs: rounded(metric.avg_response_ms),
    errorPct: rounded(metric.error_pct || 0, 100),
    p50: rounded(metric.p50_ms),
    p90: rounded(metric.p90_ms),
    p95: rounded(metric.p95_ms),
    p99: rounded(metric.p99_ms),
    cpuProcess: rounded(metric.resources?.cpu_process_pct),
    cpuFree: rounded(metric.resources?.cpu_free_pct),
    memProcess: rounded(metric.resources?.mem_process_mb),
    memFree: rounded(metric.resources?.mem_free_mb),
    netSent: rounded(metric.resources?.net_sent_kbps),
    netRecv: rounded(metric.resources?.net_recv_kbps),
    diskRead: rounded(metric.resources?.disk_read_kbps),
    diskWrite: rounded(metric.resources?.disk_write_kbps),
    cpuProcessMax: roundedOptional(metric.resources?.cpu_process_pct_max),
    // Network-phase breakdown from real VU traffic (kept distinct from the
    // synthetic route-latency probe). Optional so an absent phase stays a gap.
    txDnsMs: roundedOptional(metric.avg_dns_ms),
    txConnectMs: roundedOptional(metric.avg_connect_ms),
    txTlsMs: roundedOptional(metric.avg_tls_ms),
    txTtfbMs: roundedOptional(metric.avg_ttfb_ms),
    txWaitMs: roundedOptional(metric.avg_wait_ms),
    // Error taxonomy counts (0 is meaningful for a class, so use rounded()).
    errTimeout: rounded(metric.error_breakdown?.timeout),
    errConnection: rounded(metric.error_breakdown?.connection),
    errHttp4xx: rounded(metric.error_breakdown?.http_4xx),
    errHttp5xx: rounded(metric.error_breakdown?.http_5xx),
    errAssertion: rounded(metric.error_breakdown?.assertion),
    errResourceLimit: rounded(metric.error_breakdown?.resource_limit),
    errSessionLost: rounded(metric.error_breakdown?.session_lost),
    errOther: rounded(metric.error_breakdown?.other),
    ...roundedHistoryValues(resourceImpactHistoryValues(metric.resources)),
  }

  const jmsTransactions = Object.values(metric.transactions ?? {}).filter((tx) => tx.dependency_type?.startsWith('jms_') && tx.server_telemetry)
  if (jmsTransactions.length > 0) {
    let lagSamples = 0
    let lagSum = 0
    let lagPeak = 0

    jmsTransactions.forEach((tx) => {
      const samples = tx.server_telemetry?.consumer_lag_samples ?? ((tx.server_telemetry?.avg_consumer_lag_ms ?? 0) > 0 ? tx.server_telemetry?.samples ?? 0 : 0)
      const avgLag = tx.server_telemetry?.avg_consumer_lag_ms ?? 0
      if (samples > 0 && Number.isFinite(avgLag)) {
        lagSamples += samples
        lagSum += avgLag * samples
      }
      const maxLag = tx.server_telemetry?.max_consumer_lag_ms ?? 0
      if (maxLag > lagPeak) {
        lagPeak = maxLag
      }
    })

    if (lagSamples > 0) {
      point.jmsConsumerLagMs = lagSum / lagSamples
    }
    if (lagSamples > 0) {
      point.jmsPeakConsumerLagMs = lagPeak
    }
  }

  if (metric.groups) {
    Object.entries(metric.groups).forEach(([name, groupMetric]) => {
      point[`${name}_tps`] = rounded(groupMetric.tps)
      point[`${name}_requestRate`] = rounded(groupMetric.request_rate)
      // A running total, kept exact: journeys per minute come from its change.
      if (typeof groupMetric.completed_iterations === 'number') point[`${name}_iterations`] = groupMetric.completed_iterations
      point[`${name}_avgMs`] = rounded(groupMetric.avg_response_ms)
      point[`${name}_vusers`] = groupMetric.vusers
      point[`${name}_transactions`] = groupMetric.transactions
      point[`${name}_errors`] = groupMetric.errors
      point[`${name}_targetTps`] = groupMetric.target_tps
      point[`${name}_p50`] = roundedOptional(groupMetric.p50_ms)
      point[`${name}_p90`] = roundedOptional(groupMetric.p90_ms)
      point[`${name}_p95`] = roundedOptional(groupMetric.p95_ms)
      point[`${name}_p99`] = roundedOptional(groupMetric.p99_ms)
      const attempts = (groupMetric.transactions ?? 0) + (groupMetric.errors ?? 0)
      point[`${name}_errorPct`] = attempts > 0 ? rounded((groupMetric.errors ?? 0) / attempts * 100, 100) : 0
    })
  }

  if (metric.transactions) {
    Object.entries(metric.transactions).forEach(([fallbackName, transactionMetric]) => {
      const name = transactionMetric.name?.trim() || fallbackName
      if (!name) return
      point[transactionChartSeriesKey(name, 'tps')] = roundedOptional(transactionMetric.tps)
      point[transactionChartSeriesKey(name, 'avgMs')] = rounded(transactionMetric.avg_ms)
      point[transactionChartSeriesKey(name, 'errorPct')] = transactionMetric.count > 0
        ? rounded(transactionMetric.errors / transactionMetric.count * 100, 100)
        : 0
      // Percentiles stay optional: a transaction that reported none must leave a
      // gap on the P-value graphs rather than a misleading 0 ms line.
      point[transactionChartSeriesKey(name, 'p50')] = roundedOptional(transactionMetric.p50_ms)
      point[transactionChartSeriesKey(name, 'p90')] = roundedOptional(transactionMetric.p90_ms)
      point[transactionChartSeriesKey(name, 'p95')] = roundedOptional(transactionMetric.p95_ms)
      point[transactionChartSeriesKey(name, 'p99')] = roundedOptional(transactionMetric.p99_ms)
      // Each group's own rate: its share, or the whole when one group ran it.
      const shares = Object.entries(transactionMetric.by_group ?? {})
      if (shares.length > 1) shares.forEach(([group, share]) => { point[groupTransactionTpsSeriesKey(group, name)] = roundedOptional(share?.tps) })
      else if (transactionMetric.groups?.length === 1) point[groupTransactionTpsSeriesKey(transactionMetric.groups[0], name)] = roundedOptional(transactionMetric.tps)
    })
  }

  if (metric.latency?.targets) {
    metric.latency.targets.forEach((target) => {
      Object.assign(point, routeLatencySeriesValuesForTarget(target))
    })
  }

  return point
}

export function mapExecutionHistory(history: LiveMetrics[] | undefined): MetricsHistoryPoint[] {
  if (!history?.length) {
    return []
  }

  return history.map(mapExecutionMetricToHistoryPoint)
}

// Trailing window for the Ops View top-line TPS/ART averages.
export const HERO_METRIC_AVERAGE_WINDOW_MS = 10_000
/** The trailing average window, e.g. "10 s". */
export const RECENT_AVERAGE_WINDOW = `${HERO_METRIC_AVERAGE_WINDOW_MS / 1000} s`
/** Names the trailing average in labels, e.g. "TPS (10 s avg)". */
export const RECENT_AVERAGE_LABEL = `${RECENT_AVERAGE_WINDOW} avg`

export interface RecentMetricAverages {
  tps?: number
  art?: number
  /** Requests per second, over the same samples as tps. */
  requestRate?: number
  /** Each named group's TPS and requests per second, over the same samples; a group no sample carries is left out. */
  groups?: Record<string, { tps: number; requestRate: number }>
  /** Each named transaction's TPS, over the same samples; a transaction no sample carries is left out. */
  transactions?: Record<string, number>
  /** Each named group's rate of a transaction, keyed by groupTransactionKey, over the same samples; one no sample carries is left out. */
  groupTransactions?: Record<string, number>
}

/** The groups and transactions to average beside the run. */
export interface RecentMetricAverageNames {
  groups?: readonly string[]
  transactions?: readonly string[]
  /** Group and transaction pairs. */
  groupTransactions?: ReadonlyArray<readonly [group: string, transaction: string]>
}

/** Names one group's rate of a transaction in RecentMetricAverages.groupTransactions. */
export function groupTransactionKey(group: string, transaction: string) {
  return `${group}\u0000${transaction}`
}

function seriesValue(point: MetricsHistoryPoint, key: string): number | undefined {
  const value = point[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

// recentMetricAverages computes trailing-window (default 10s) averages of TPS and
// ART from the live metrics history, so the Ops View top-line cards read as a
// smoothed 10-second average instead of a single jittery 1-second sample.
//
// The window is anchored on the most recent real sample's timestamp (data-
// relative, not wall-clock, so it works for replayed/paused streams too). TPS is
// the mean of per-second throughput across the window. ART is throughput-weighted
// — each window's average response time is weighted by its TPS, a proxy for the
// number of completed transactions — so busy seconds count more than quiet ones;
// it falls back to a plain mean when the window had no throughput. Gap-spacer and
// value-less rows are ignored. Returns undefined fields when the window has no
// usable samples so callers can fall back to the instantaneous value.
//
// Requests per second and each named group's and transaction's rate are
// averaged over the same samples as TPS, so every rate on a page covers one
// window and the groups' rates add up to the run's. A sample without a group
// or transaction counts as zero for it: the sample had none of its traffic.
// One that no sample in the window carries (history recorded without group
// detail) is left out, so callers fall back to the latest value instead of 0.
export function recentMetricAverages(
  history: ReadonlyArray<MetricsHistoryPoint>,
  windowMs: number = HERO_METRIC_AVERAGE_WINDOW_MS,
  names: RecentMetricAverageNames = {},
): RecentMetricAverages {
  let anchorTs: number | undefined
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const point = history[index]
    if (typeof point.timestampMs === 'number' && typeof point.tps === 'number') {
      anchorTs = point.timestampMs
      break
    }
  }
  if (anchorTs === undefined) return {}
  const cutoff = anchorTs - windowMs

  let tpsSum = 0
  let tpsCount = 0
  let requestRateSum = 0
  let artWeightedSum = 0
  let artWeight = 0
  let artSimpleSum = 0
  let artSimpleCount = 0
  const groupNames = names.groups ?? []
  const transactionNames = names.transactions ?? []
  const groupSums = groupNames.map(() => ({ tps: 0, requestRate: 0, seen: false }))
  const transactionKeys = transactionNames.map(name => transactionChartSeriesKey(name, 'tps'))
  const transactionSums = transactionNames.map(() => ({ tps: 0, seen: false }))
  const groupTransactionPairs = names.groupTransactions ?? []
  const groupTransactionKeys = groupTransactionPairs.map(([group, transaction]) => groupTransactionTpsSeriesKey(group, transaction))
  const groupTransactionSums = groupTransactionPairs.map(() => ({ tps: 0, seen: false }))
  for (const point of history) {
    if (typeof point.timestampMs !== 'number' || point.timestampMs < cutoff) continue
    if (typeof point.tps === 'number') {
      tpsSum += point.tps
      tpsCount += 1
      requestRateSum += point.requestRate ?? 0
      groupNames.forEach((name, index) => {
        const tps = seriesValue(point, `${name}_tps`)
        const requestRate = seriesValue(point, `${name}_requestRate`)
        groupSums[index].tps += tps ?? 0
        groupSums[index].requestRate += requestRate ?? 0
        groupSums[index].seen ||= tps !== undefined || requestRate !== undefined
      })
      transactionKeys.forEach((key, index) => {
        const tps = seriesValue(point, key)
        transactionSums[index].tps += tps ?? 0
        transactionSums[index].seen ||= tps !== undefined
      })
      groupTransactionKeys.forEach((key, index) => {
        const tps = seriesValue(point, key)
        groupTransactionSums[index].tps += tps ?? 0
        groupTransactionSums[index].seen ||= tps !== undefined
      })
    }
    if (typeof point.avgMs === 'number') {
      artSimpleSum += point.avgMs
      artSimpleCount += 1
      if (typeof point.tps === 'number' && point.tps > 0) {
        artWeightedSum += point.avgMs * point.tps
        artWeight += point.tps
      }
    }
  }

  const result: RecentMetricAverages = {}
  if (tpsCount > 0) {
    result.tps = tpsSum / tpsCount
    result.requestRate = requestRateSum / tpsCount
    if (groupNames.length > 0) {
      result.groups = Object.fromEntries(groupNames.flatMap((name, index) => groupSums[index].seen ? [[name, { tps: groupSums[index].tps / tpsCount, requestRate: groupSums[index].requestRate / tpsCount }]] : []))
    }
    if (transactionNames.length > 0) {
      result.transactions = Object.fromEntries(transactionNames.flatMap((name, index) => transactionSums[index].seen ? [[name, transactionSums[index].tps / tpsCount]] : []))
    }
    if (groupTransactionPairs.length > 0) {
      result.groupTransactions = Object.fromEntries(groupTransactionPairs.flatMap(([group, transaction], index) => groupTransactionSums[index].seen ? [[groupTransactionKey(group, transaction), groupTransactionSums[index].tps / tpsCount]] : []))
    }
  }
  if (artWeight > 0) result.art = artWeightedSum / artWeight
  else if (artSimpleCount > 0) result.art = artSimpleSum / artSimpleCount
  return result
}

/** The window journeys per minute are counted over, and its label. */
const JOURNEY_RATE_WINDOW_MS = 60_000
export const JOURNEY_RATE_WINDOW = '1 min avg'
const MIN_JOURNEY_RATE_SPAN_MS = 15_000

/**
 * Each named group's iterations (journeys) per minute over the trailing
 * window, anchored on the latest sample as recentMetricAverages is: the
 * change in the group's running total between the oldest and newest samples
 * in the window that carry it. A total that falls (a restarted execution, a
 * generator gone) starts the count again after the fall. A group whose
 * samples span under 15 s, or carry no total, is left out. The history is
 * read back from its newest sample and stops at the window's start.
 */
export function recentGroupJourneyRates(
  history: ReadonlyArray<MetricsHistoryPoint>,
  groups: readonly string[],
  windowMs: number = JOURNEY_RATE_WINDOW_MS,
): Record<string, number> {
  let anchorTs: number | undefined
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const timestamp = history[index].timestampMs
    if (typeof timestamp === 'number') {
      anchorTs = timestamp
      break
    }
  }
  if (anchorTs === undefined || groups.length === 0) return {}
  const cutoff = anchorTs - windowMs
  const rates: Record<string, number> = {}
  for (const name of groups) {
    const key = `${name}_iterations`
    let newest: { t: number; v: number } | undefined
    let oldest: { t: number; v: number } | undefined
    for (let index = history.length - 1; index >= 0; index -= 1) {
      const point = history[index]
      const t = point.timestampMs
      if (typeof t !== 'number') continue
      if (t < cutoff) break
      const v = seriesValue(point, key)
      if (v === undefined) continue
      // An older total above the oldest counted one means the total fell
      // after it: the count restarted there.
      if (oldest && v > oldest.v) break
      newest ??= { t, v }
      oldest = { t, v }
    }
    if (newest && oldest && newest.t - oldest.t >= MIN_JOURNEY_RATE_SPAN_MS) rates[name] = (newest.v - oldest.v) / (newest.t - oldest.t) * 60_000
  }
  return rates
}

// historyPointKey identifies a sample by timestamp (falling back to its display
// time), matching the de-duplication identity used when histories are combined
// for charting.
function historyPointKey(point: MetricsHistoryPoint): string {
  return typeof point.timestampMs === 'number' ? `ts:${point.timestampMs}` : `time:${point.time}`
}

// mergeMetricsHistory folds server-supplied history points into an existing
// (live-streamed) series, filling any window the client missed while the
// WebSocket was disconnected — e.g. while the dashboard tab was backgrounded or
// the machine slept. Existing points win on conflict (they carry client-side
// enrichments such as latency targets and telemetry timing), so a reconnect
// backfill only adds the genuinely-missing samples. The result is returned in
// ascending timestamp order.
export function mergeMetricsHistory(
  existing: MetricsHistoryPoint[],
  incoming: MetricsHistoryPoint[],
): MetricsHistoryPoint[] {
  if (incoming.length === 0) return existing

  const byKey = new Map<string, MetricsHistoryPoint>()
  for (const point of existing) {
    byKey.set(historyPointKey(point), point)
  }
  let added = false
  for (const point of incoming) {
    const key = historyPointKey(point)
    if (!byKey.has(key)) {
      byKey.set(key, point)
      added = true
    }
  }
  if (!added) return existing

  return Array.from(byKey.values()).sort((left, right) => {
    const leftTs = typeof left.timestampMs === 'number' ? left.timestampMs : 0
    const rightTs = typeof right.timestampMs === 'number' ? right.timestampMs : 0
    return leftTs - rightTs
  })
}

export function mapArchiveResourceSamples(samples: ResourceSample[] | undefined): MetricsHistoryPoint[] {
  if (!samples?.length) {
    return []
  }

  return samples
    .map((sample) => {
      const timestampMs = Date.parse(sample.timestamp)
      return {
        time: new Date(sample.timestamp).toLocaleTimeString(),
        timestampMs: Number.isNaN(timestampMs) ? undefined : timestampMs,
        vusers: sample.active_vusers ?? 0,
        // Resource samples are independently downsampled from performance
        // summaries. NaN keeps unmatched resource-only points out of Recharts
        // performance lines and KPI/breach calculations without inventing a
        // zero-throughput/zero-latency interval.
        tps: Number.NaN,
        avgMs: Number.NaN,
        errorPct: Number.NaN,
        p50: Number.NaN,
        p90: Number.NaN,
        p95: Number.NaN,
        p99: Number.NaN,
        resourceSampleOnly: 1,
        cpuProcess: sample.cpu_process_pct,
        cpuFree: sample.cpu_free_pct,
        memProcess: sample.mem_process_mb,
        memFree: sample.mem_free_mb,
        netSent: sample.net_sent_kbps,
        netRecv: sample.net_recv_kbps,
        diskRead: sample.disk_read_kbps,
        diskWrite: sample.disk_write_kbps,
        ...resourceImpactHistoryValues(sample),
      }
    })
    .filter(point => typeof point.timestampMs === 'number')
}

export function mapArchiveSummarySamples(summaries: IntervalSummaryRecord[] | undefined): MetricsHistoryPoint[] {
  if (!summaries?.length) {
    return []
  }

  return summaries
    .map((summary) => {
      const timestampMs = Date.parse(summary.timestamp)
      const point: MetricsHistoryPoint = {
        time: new Date(summary.timestamp).toLocaleTimeString(),
        timestampMs: Number.isNaN(timestampMs) ? undefined : timestampMs,
        vusers: summary.active_vusers,
        tps: summary.tps,
        avgMs: summary.avg_response_ms,
        errorPct: summary.error_pct || 0,
        p50: summary.p50_ms,
        p90: summary.p90_ms,
        p95: summary.p95_ms,
        p99: summary.p99_ms,
      }
      if (summary.groups) {
        Object.entries(summary.groups).forEach(([name, groupMetric]) => {
          point[`${name}_tps`] = groupMetric.tps
          point[`${name}_avgMs`] = groupMetric.avg_response_ms
          point[`${name}_vusers`] = groupMetric.vusers
          point[`${name}_transactions`] = groupMetric.transactions
          point[`${name}_errors`] = groupMetric.errors
          point[`${name}_targetTps`] = groupMetric.target_tps
          point[`${name}_p50`] = groupMetric.p50_ms
          point[`${name}_p90`] = groupMetric.p90_ms
          point[`${name}_p95`] = groupMetric.p95_ms
          point[`${name}_p99`] = groupMetric.p99_ms
          const attempts = (groupMetric.transactions ?? 0) + (groupMetric.errors ?? 0)
          point[`${name}_errorPct`] = attempts > 0 ? (groupMetric.errors ?? 0) / attempts * 100 : 0
        })
      }
      if (summary.transactions) {
        Object.entries(summary.transactions).forEach(([fallbackName, transactionMetric]) => {
          const name = transactionMetric.name?.trim() || fallbackName
          if (!name) return
          point[transactionChartSeriesKey(name, 'tps')] = roundedOptional(transactionMetric.tps)
          point[transactionChartSeriesKey(name, 'avgMs')] = rounded(transactionMetric.avg_ms)
          point[transactionChartSeriesKey(name, 'errorPct')] = transactionMetric.count > 0
            ? rounded(transactionMetric.errors / transactionMetric.count * 100, 100)
            : 0
        })
      }
      summary.latency?.targets?.forEach((target) => {
        Object.assign(point, routeLatencySeriesValuesForTarget(target))
      })
      return point
    })
    .filter(point => typeof point.timestampMs === 'number')
}

export function mergeArchiveResourceHistory(
  chartHistory: MetricsHistoryPoint[],
  archiveResourceHistory: MetricsHistoryPoint[] | undefined,
): MetricsHistoryPoint[] {
  if (!archiveResourceHistory?.length) {
    return chartHistory
  }

  if (chartHistory.length === 0) {
    return [...archiveResourceHistory]
  }

  const resourcesByTimestamp = new Map<number, MetricsHistoryPoint>()
  archiveResourceHistory.forEach((point) => {
    if (typeof point.timestampMs === 'number') {
      resourcesByTimestamp.set(point.timestampMs, point)
    }
  })

  const matchedResourceTimestamps = new Set<number>()
  const merged = chartHistory.map((point) => {
    if (typeof point.timestampMs !== 'number') {
      return point
    }
    const resourcePoint = resourcesByTimestamp.get(point.timestampMs)
    if (!resourcePoint) {
      return point
    }
    matchedResourceTimestamps.add(point.timestampMs)
    return {
      ...point,
      // Interval summaries own the load timeline. In particular, zero is an
      // authoritative stair-step level and must not be replaced by a stale
      // positive resource sample from the same timestamp.
      vusers: point.vusers ?? resourcePoint.vusers,
      cpuProcess: resourcePoint.cpuProcess,
      cpuFree: resourcePoint.cpuFree,
      memProcess: resourcePoint.memProcess,
      memFree: resourcePoint.memFree,
      netSent: resourcePoint.netSent,
      netRecv: resourcePoint.netRecv,
      diskRead: resourcePoint.diskRead,
      diskWrite: resourcePoint.diskWrite,
      cpuIOWait: resourcePoint.cpuIOWait,
      cpuSteal: resourcePoint.cpuSteal,
      gcPauseMs: resourcePoint.gcPauseMs,
      containerCPUUsagePct: resourcePoint.containerCPUUsagePct,
      containerCPUThrottledSecondsRate: resourcePoint.containerCPUThrottledSecondsRate,
      containerMemoryUsagePct: resourcePoint.containerMemoryUsagePct,
      containerOOMKillCountDelta: resourcePoint.containerOOMKillCountDelta,
      netDropsPerSec: resourcePoint.netDropsPerSec,
      netErrorsPerSec: resourcePoint.netErrorsPerSec,
      tcpRetransmitsPerSec: resourcePoint.tcpRetransmitsPerSec,
      diskIOTimePct: resourcePoint.diskIOTimePct,
    }
  })

  const resourceOnlyPoints = archiveResourceHistory.filter(point => (
    typeof point.timestampMs === 'number' && !matchedResourceTimestamps.has(point.timestampMs)
  ))

  if (resourceOnlyPoints.length === 0) {
    return merged
  }

  return [...merged, ...resourceOnlyPoints].sort((left, right) => {
    if (typeof left.timestampMs === 'number' && typeof right.timestampMs === 'number') {
      return left.timestampMs - right.timestampMs
    }
    return 0
  })
}

interface BuildExecutionChartDataOptions {
  mappedBackendHistory: MetricsHistoryPoint[]
  localMetricsHistory: MetricsHistoryPoint[]
  fallbackHistory: MetricsHistoryPoint[]
  archiveResourceHistory?: MetricsHistoryPoint[]
  status?: TestStatus
}

export function buildExecutionChartData({
  mappedBackendHistory,
  localMetricsHistory,
  fallbackHistory,
  archiveResourceHistory,
  status,
}: BuildExecutionChartDataOptions): MetricsHistoryPoint[] {
  let combined: MetricsHistoryPoint[] = []

  if (localMetricsHistory.length > 0) {
    combined = mappedBackendHistory.length > 0
      ? [...mappedBackendHistory, ...localMetricsHistory]
      : [...localMetricsHistory]
  } else if (fallbackHistory.length > 0) {
    combined = mappedBackendHistory.length > 0
      ? [...mappedBackendHistory, ...fallbackHistory]
      : [...fallbackHistory]
  } else {
    combined = mappedBackendHistory
  }

  const seen = new Set<string>()
  const deduped: MetricsHistoryPoint[] = []
  for (let index = combined.length - 1; index >= 0; index -= 1) {
    const identity = typeof combined[index].timestampMs === 'number'
      ? `ts:${combined[index].timestampMs}`
      : `time:${combined[index].time}`
    if (!seen.has(identity)) {
      seen.add(identity)
      deduped.unshift(combined[index])
    }
  }

  const resourceEnrichedPoints = mergeArchiveResourceHistory(deduped, archiveResourceHistory)
  const responseTimeFilledPoints = carryForwardPositiveResponseTimes(resourceEnrichedPoints)
  const pointsWithRelativeRunTime = addRelativeRunTime(responseTimeFilledPoints)

  if (status === 'PAUSED' && pointsWithRelativeRunTime.length > 0) {
    const lastPoint = pointsWithRelativeRunTime[pointsWithRelativeRunTime.length - 1]
    if (lastPoint.tps < 0.1) {
      let lastNonZeroIndex = -1
      for (let index = pointsWithRelativeRunTime.length - 1; index >= 0; index -= 1) {
        if (pointsWithRelativeRunTime[index].tps > 0.1) {
          lastNonZeroIndex = index
          break
        }
      }

      if (lastNonZeroIndex !== -1) {
        return markDataGaps(pointsWithRelativeRunTime.slice(0, lastNonZeroIndex + 6))
      }
    }
  }

  return markDataGaps(pointsWithRelativeRunTime)
}

const RESPONSE_TIME_METRICS = ['avgMs', 'p50', 'p90', 'p95', 'p99'] as const

function responseTimeMetricSuffix(key: string) {
  return RESPONSE_TIME_METRICS.find(metric => (
    key === metric
    || key.endsWith(`_${metric}`)
    || (key.startsWith('transaction:') && key.endsWith(`:${metric}`))
  ))
}

// Empty engine intervals carry 0 ms (and resource-only archive rows carry NaN)
// because no response-time samples were observed. Those are absence sentinels,
// not measured zero-duration responses. Keep each response-time line at its last
// positive observation until a new positive sample arrives. This runs before
// markDataGaps, so an actual missing sampling window still receives a value-less
// gap-boundary row. Runtime graphs may connect across it without adding samples.
export function carryForwardPositiveResponseTimes(points: MetricsHistoryPoint[]): MetricsHistoryPoint[] {
  if (points.length === 0) return points

  const responseTimeKeys = Array.from(new Set(points.flatMap(point => Object.keys(point).filter(key => responseTimeMetricSuffix(key) !== undefined))))
  if (responseTimeKeys.length === 0) return points

  const previousPositive = new Map<string, number>()
  let changed = false
  const filled = points.map(point => {
    let next = point
    responseTimeKeys.forEach(key => {
      const value = point[key]
      if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
        previousPositive.set(key, value)
        return
      }
      const metric = responseTimeMetricSuffix(key)
      if (!metric) return
      const previous = previousPositive.get(key)
      if (previous === undefined) return
      if (next === point) {
        next = { ...point }
        changed = true
      }
      next[key] = previous
    })
    return next
  })
  return changed ? filled : points
}

const GAP_DETECTION_MIN_POINTS = 6
// A gap is only flagged when the inter-sample delta exceeds this multiple of the
// measured median interval — high enough that normal jitter never trips it.
const GAP_DETECTION_FACTOR = 3

// estimateSampleIntervalMs returns the median delta between consecutive
// timestamps, a jitter-robust estimate of the sampling cadence.
export function estimateSampleIntervalMs(points: MetricsHistoryPoint[]): number {
  const deltas: number[] = []
  for (let index = 1; index < points.length; index += 1) {
    const prev = points[index - 1].timestampMs
    const curr = points[index].timestampMs
    if (typeof prev === 'number' && typeof curr === 'number') {
      const delta = curr - prev
      if (delta > 0) deltas.push(delta)
    }
  }
  if (deltas.length === 0) return 1000
  deltas.sort((left, right) => left - right)
  const mid = Math.floor(deltas.length / 2)
  return deltas.length % 2 === 0 ? (deltas[mid - 1] + deltas[mid]) / 2 : deltas[mid]
}

// markDataGaps inserts a synthetic value-less spacer row wherever a sampling gap
// is detected, preserving the unobserved interval in the data even when a runtime
// graph connects its surrounding samples. The spacer carries the real midpoint
// timestamp, so its axis tick reads as a normal — but empty — time slot.
export function markDataGaps(points: MetricsHistoryPoint[], factor = GAP_DETECTION_FACTOR): MetricsHistoryPoint[] {
  if (points.length < GAP_DETECTION_MIN_POINTS) return points
  const intervalMs = estimateSampleIntervalMs(points)
  if (!Number.isFinite(intervalMs) || intervalMs <= 0) return points
  const threshold = intervalMs * factor

  const result: MetricsHistoryPoint[] = []
  for (let index = 0; index < points.length; index += 1) {
    const point = points[index]
    if (index > 0) {
      const prev = points[index - 1]
      if (typeof prev.timestampMs === 'number' && typeof point.timestampMs === 'number') {
        const delta = point.timestampMs - prev.timestampMs
        if (delta > threshold) {
          const midTs = prev.timestampMs + delta / 2
          const spacer = {
            time: new Date(midTs).toLocaleTimeString(),
            timestampMs: midTs,
            relativeMs: typeof prev.relativeMs === 'number' && typeof point.relativeMs === 'number'
              ? prev.relativeMs + (point.relativeMs - prev.relativeMs) / 2
              : undefined,
            isGapBoundary: 1,
          } as unknown as MetricsHistoryPoint
          result.push(spacer)
        }
      }
    }
    result.push(point)
  }
  return result
}

function addRelativeRunTime(points: MetricsHistoryPoint[]): MetricsHistoryPoint[] {
  if (points.length === 0) {
    return points
  }
  const firstTimestamp = points.find(point => typeof point.timestampMs === 'number')?.timestampMs
  return points.map((point, index) => ({
    ...point,
    relativeMs: typeof firstTimestamp === 'number' && typeof point.timestampMs === 'number'
      ? Math.max(0, point.timestampMs - firstTimestamp)
      : index * 1000,
  }))
}

export function filterExecutionChartData(
  chartData: MetricsHistoryPoint[],
  timeRange: ExecutionChartTimeRange,
  zoomRange: ExecutionChartZoomRange | null,
): MetricsHistoryPoint[] {
  if (timeRange === 'custom') {
    if (!zoomRange || chartData.length === 0) return chartData

    // Timestamp bounds are unique across repeated display labels (multi-day
    // runs and DST folds) and are already normalized chronologically by the
    // zoom reducer. Retention naturally clamps an evicted edge to the oldest
    // or newest surviving point.
    const firstTimestampIndex = chartData.findIndex(point => typeof point.timestampMs === 'number')
    let lastTimestampIndex = -1
    for (let index = chartData.length - 1; index >= 0; index -= 1) {
      if (typeof chartData[index].timestampMs === 'number') {
        lastTimestampIndex = index
        break
      }
    }
    if (firstTimestampIndex === -1 || lastTimestampIndex === -1) return chartData
    const oldestTimestampMs = chartData[firstTimestampIndex].timestampMs!
    const newestTimestampMs = chartData[lastTimestampIndex].timestampMs!
    if (zoomRange.endTimestampMs < oldestTimestampMs) {
      return chartData.slice(firstTimestampIndex, firstTimestampIndex + 1)
    }
    if (zoomRange.startTimestampMs > newestTimestampMs) {
      return chartData.slice(lastTimestampIndex, lastTimestampIndex + 1)
    }

    const start = chartData.findIndex(point => (
      typeof point.timestampMs === 'number' && point.timestampMs >= zoomRange.startTimestampMs
    ))
    let end = -1
    for (let index = chartData.length - 1; index >= 0; index -= 1) {
      const timestampMs = chartData[index].timestampMs
      if (typeof timestampMs === 'number' && timestampMs <= zoomRange.endTimestampMs) {
        end = index
        break
      }
    }
    if (start === -1 || end === -1) return chartData
    if (start > end) {
      const midpoint = zoomRange.startTimestampMs + (zoomRange.endTimestampMs - zoomRange.startTimestampMs) / 2
      let nearest = firstTimestampIndex
      for (let index = firstTimestampIndex + 1; index <= lastTimestampIndex; index += 1) {
        const timestampMs = chartData[index].timestampMs
        if (typeof timestampMs === 'number'
          && Math.abs(timestampMs - midpoint) < Math.abs(chartData[nearest].timestampMs! - midpoint)) {
          nearest = index
        }
      }
      return chartData.slice(nearest, nearest + 1)
    }
    return chartData.slice(start, end + 1)
  }

  if (timeRange === 'all') {
    return chartData
  }

  const minutes = executionChartMinuteRangeValue(timeRange) ?? 10
  const windowMs = minutes * 60_000
  const newestTimestamp = [...chartData].reverse().find(point => typeof point.timestampMs === 'number')?.timestampMs
  if (newestTimestamp === undefined) {
    const approximatePoints = minutes * 60
    return chartData.slice(-approximatePoints)
  }

  const cutoff = newestTimestamp - windowMs
  return chartData.filter(point => {
    if (typeof point.timestampMs !== 'number') {
      return true
    }
    return point.timestampMs >= cutoff
  })
}

export function summarizeExecutionChartData(filteredData: MetricsHistoryPoint[]): ExecutionChartSummary | null {
  if (filteredData.length === 0) {
    return null
  }

  // Exclude gap-boundary spacers and independently sampled resource-only rows.
  // Resource rows intentionally use NaN for performance values so chart lines
  // break instead of inventing zero-throughput intervals; NaN still has the
  // JavaScript type "number", so every averaged field must be finite.
  const realPoints = filteredData.filter(
    point => !point.isGapBoundary
      && point.resourceSampleOnly !== 1
      && Number.isFinite(point.tps)
      && Number.isFinite(point.avgMs)
      && Number.isFinite(point.errorPct),
  )
  if (realPoints.length === 0) {
    return null
  }

  const sum = realPoints.reduce((acc, point) => ({
    tps: acc.tps + point.tps,
    avgMs: acc.avgMs + point.avgMs,
    errorPct: acc.errorPct + point.errorPct,
    vusers: Math.max(acc.vusers, point.vusers),
  }), { tps: 0, avgMs: 0, errorPct: 0, vusers: 0 })

  const requestRates = realPoints.flatMap(point => Number.isFinite(point.requestRate) ? [point.requestRate as number] : [])
  return {
    avgTps: sum.tps / realPoints.length,
    ...(requestRates.length > 0 ? { avgRequestRate: requestRates.reduce((total, rate) => total + rate, 0) / requestRates.length } : {}),
    avgLatency: sum.avgMs / realPoints.length,
    avgError: sum.errorPct / realPoints.length,
    maxVusers: sum.vusers,
  }
}
