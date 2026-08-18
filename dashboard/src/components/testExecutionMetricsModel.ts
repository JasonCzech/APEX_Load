import { IntervalSummaryRecord, LiveMetrics, MetricsHistoryPoint, ResourceSample, TestStatus } from '../types'
import { resourceImpactHistoryValues } from './generatorImpact'
import { routeLatencySeriesValuesForTarget } from './latencySeries'
import { transactionChartSeriesKey } from './transactionChartSeries'
import {
  executionChartMinuteRangeValue,
  type ExecutionChartTimeRange,
  type ExecutionChartZoomRange,
} from './testExecutionChartZoomModel'

export interface ExecutionChartSummary {
  avgTps: number
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
    errOther: rounded(metric.error_breakdown?.other),
    ...roundedHistoryValues(resourceImpactHistoryValues(metric.resources)),
  }

  const jmsTransactions = Object.values(metric.transactions ?? {}).filter((tx) => tx.dependency_type?.startsWith('jms_') && tx.server_telemetry)
  if (jmsTransactions.length > 0) {
    let lagSamples = 0
    let lagSum = 0
    let lagPeak = 0

    jmsTransactions.forEach((tx) => {
      const samples = tx.server_telemetry?.samples ?? 0
      const avgLag = tx.server_telemetry?.avg_consumer_lag_ms ?? 0
      if (samples > 0 && avgLag > 0) {
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
    if (lagPeak > 0) {
      point.jmsPeakConsumerLagMs = lagPeak
    }
  }

  if (metric.groups) {
    Object.entries(metric.groups).forEach(([name, groupMetric]) => {
      point[`${name}_tps`] = rounded(groupMetric.tps)
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

export interface RecentMetricAverages {
  tps?: number
  art?: number
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
export function recentMetricAverages(
  history: ReadonlyArray<MetricsHistoryPoint>,
  windowMs: number = HERO_METRIC_AVERAGE_WINDOW_MS,
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
  let artWeightedSum = 0
  let artWeight = 0
  let artSimpleSum = 0
  let artSimpleCount = 0
  for (const point of history) {
    if (typeof point.timestampMs !== 'number' || point.timestampMs < cutoff) continue
    if (typeof point.tps === 'number') {
      tpsSum += point.tps
      tpsCount += 1
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
  if (tpsCount > 0) result.tps = tpsSum / tpsCount
  if (artWeight > 0) result.art = artWeightedSum / artWeight
  else if (artSimpleCount > 0) result.art = artSimpleSum / artSimpleCount
  return result
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
  const pointsWithRelativeRunTime = addRelativeRunTime(resourceEnrichedPoints)

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
// is detected, so every series breaks (rather than interpolating a misleading
// straight line) across missing intervals. The spacer carries the real midpoint
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

  return {
    avgTps: sum.tps / realPoints.length,
    avgLatency: sum.avgMs / realPoints.length,
    avgError: sum.errorPct / realPoints.length,
    maxVusers: sum.vusers,
  }
}
