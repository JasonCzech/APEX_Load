import { useCallback, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { LiveMetrics, MetricsHistoryPoint, NetworkLatency, TelemetryHealth, TestStatus, WSMessage } from '../types'
import { latencySeriesKeyForTarget } from './latencySeries'

export const EXECUTION_STREAM_HISTORY_LIMIT = 300

function shouldStreamExecutionMetrics(status?: TestStatus): boolean {
  return status === 'RUNNING' || status === 'INITIALIZING' || status === 'PAUSED' || status === 'STOPPING'
}

function parseTimestamp(value?: string): number | undefined {
  if (!value) return undefined
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? undefined : timestamp
}

function computeElapsedMs(start?: number, end?: number): number | undefined {
  if (start === undefined || end === undefined) return undefined
  return Math.max(0, end - start)
}

function buildClientTelemetry(telemetry: TelemetryHealth | undefined, receivedAt: Date): TelemetryHealth | undefined {
  if (!telemetry) return undefined

  const receivedAtMs = receivedAt.getTime()
  const publishedAtMs = parseTimestamp(telemetry.published_at)
  const collectedAtMs = parseTimestamp(telemetry.collected_at)

  return {
    ...telemetry,
    client_received_at: receivedAt.toISOString(),
    delivery_lag_ms: computeElapsedMs(publishedAtMs, receivedAtMs),
    end_to_end_age_ms: computeElapsedMs(collectedAtMs, receivedAtMs),
  }
}

function rounded(value: number | undefined, precision = 10) {
  return Math.round((value ?? 0) * precision) / precision
}

function applyLatencyTargets(point: MetricsHistoryPoint, latency?: NetworkLatency) {
  latency?.targets?.forEach((target) => {
    point[latencySeriesKeyForTarget(target)] = rounded(target.latency_ms)
  })
  return point
}

export function useExecutionMetricsStream(testId: string, status?: TestStatus) {
  const [localMetricsHistory, setLocalMetricsHistory] = useState<MetricsHistoryPoint[]>([])
  const [latestMetrics, setLatestMetrics] = useState<LiveMetrics>()

  const handleTestWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'latency_update' && msg.latency) {
      setLatestMetrics(prev => prev ? { ...prev, latency: msg.latency } : prev)
      setLocalMetricsHistory(prev => {
        if (prev.length === 0) return prev
        const next = [...prev]
        next[next.length - 1] = applyLatencyTargets({ ...next[next.length - 1] }, msg.latency)
        return next
      })
      return
    }

    if (msg.type !== 'metrics_update' || !msg.data || !msg.timestamp) {
      return
    }

    const receivedAt = new Date()
    const telemetry = buildClientTelemetry(msg.data.telemetry, receivedAt)

    setLatestMetrics({
      ...msg.data,
      telemetry,
    })

    const point: MetricsHistoryPoint = {
      time: new Date(msg.timestamp).toLocaleTimeString(),
      timestampMs: Date.parse(msg.timestamp),
      vusers: msg.data.active_vusers,
      tps: rounded(msg.data.tps),
      avgMs: rounded(msg.data.avg_response_ms),
      errorPct: rounded(msg.data.error_pct, 100),
      p50: rounded(msg.data.p50_ms),
      p90: rounded(msg.data.p90_ms),
      p99: rounded(msg.data.p99_ms),
    }

    if (msg.data.groups) {
      for (const [name, groupMetrics] of Object.entries(msg.data.groups)) {
        point[`${name}_tps`] = rounded(groupMetrics.tps)
        point[`${name}_avgMs`] = rounded(groupMetrics.avg_response_ms)
        point[`${name}_vusers`] = groupMetrics.vusers
      }
    }

    if (msg.data.resources) {
      const resources = msg.data.resources
      point.cpuProcess = rounded(resources.cpu_process_pct)
      point.cpuFree = rounded(resources.cpu_free_pct)
      point.memProcess = rounded(resources.mem_process_mb)
      point.memFree = rounded(resources.mem_free_mb)
      point.netSent = rounded(resources.net_sent_kbps)
      point.netRecv = rounded(resources.net_recv_kbps)
      point.diskRead = rounded(resources.disk_read_kbps)
      point.diskWrite = rounded(resources.disk_write_kbps)
    }

    setLocalMetricsHistory((prev) => [...prev, applyLatencyTargets(point, msg.data?.latency)].slice(-EXECUTION_STREAM_HISTORY_LIMIT))
  }, [])

  useWebSocket({
    testId,
    enabled: shouldStreamExecutionMetrics(status),
    onMessage: handleTestWSMessage,
  })

  return { localMetricsHistory, latestMetrics }
}
