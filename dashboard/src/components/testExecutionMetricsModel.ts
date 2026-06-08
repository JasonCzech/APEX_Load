import { IntervalSummaryRecord, LiveMetrics, MetricsHistoryPoint, ResourceSample, TestStatus } from '../types'
import { resourceImpactHistoryValues } from './generatorImpact'
import { routeLatencySeriesValuesForTarget } from './latencySeries'

export interface ExecutionChartSummary {
  avgTps: number
  avgLatency: number
  avgError: number
  maxVusers: number
}

export function mapExecutionMetricToHistoryPoint(metric: LiveMetrics): MetricsHistoryPoint {
  const point: MetricsHistoryPoint = {
    time: new Date(metric.timestamp).toLocaleTimeString(),
    timestampMs: Date.parse(metric.timestamp),
    vusers: metric.active_vusers,
    tps: metric.tps,
    targetTps: metric.target_tps,
    avgMs: metric.avg_response_ms,
    errorPct: metric.error_pct || 0,
    p50: metric.p50_ms,
    p90: metric.p90_ms,
    p95: metric.p95_ms,
    p99: metric.p99_ms,
    cpuProcess: metric.resources?.cpu_process_pct,
    cpuFree: metric.resources?.cpu_free_pct,
    memProcess: metric.resources?.mem_process_mb,
    memFree: metric.resources?.mem_free_mb,
    netSent: metric.resources?.net_sent_kbps,
    netRecv: metric.resources?.net_recv_kbps,
    diskRead: metric.resources?.disk_read_kbps,
    diskWrite: metric.resources?.disk_write_kbps,
    ...resourceImpactHistoryValues(metric.resources),
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
      point[`${name}_tps`] = groupMetric.tps
      point[`${name}_avgMs`] = groupMetric.avg_response_ms
      point[`${name}_vusers`] = groupMetric.vusers
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
        tps: 0,
        avgMs: 0,
        errorPct: 0,
        p50: 0,
        p90: 0,
        p95: 0,
        p99: 0,
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
      vusers: point.vusers || resourcePoint.vusers,
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
        return pointsWithRelativeRunTime.slice(0, lastNonZeroIndex + 6)
      }
    }
  }

  return pointsWithRelativeRunTime
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
  timeRange: '3m' | '10m' | 'all' | 'custom',
  zoomRange: { left: string | number; right: string | number } | null,
): MetricsHistoryPoint[] {
  if (timeRange === 'custom' && zoomRange) {
    const left = chartData.findIndex((point) => point.time === zoomRange.left)
    const right = chartData.findIndex((point) => point.time === zoomRange.right)
    if (left !== -1 && right !== -1) {
      return chartData.slice(Math.min(left, right), Math.max(left, right) + 1)
    }
  }

  if (timeRange === 'all') {
    return chartData
  }

  const windowMs = timeRange === '3m' ? 180_000 : 600_000
  const newestTimestamp = [...chartData].reverse().find(point => typeof point.timestampMs === 'number')?.timestampMs
  if (newestTimestamp === undefined) {
    const approximatePoints = timeRange === '3m' ? 180 : 600
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

  const sum = filteredData.reduce((acc, point) => ({
    tps: acc.tps + point.tps,
    avgMs: acc.avgMs + point.avgMs,
    errorPct: acc.errorPct + point.errorPct,
    vusers: Math.max(acc.vusers, point.vusers),
  }), { tps: 0, avgMs: 0, errorPct: 0, vusers: 0 })

  return {
    avgTps: sum.tps / filteredData.length,
    avgLatency: sum.avgMs / filteredData.length,
    avgError: sum.errorPct / filteredData.length,
    maxVusers: sum.vusers,
  }
}
