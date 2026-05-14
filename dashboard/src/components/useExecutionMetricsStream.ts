import { useCallback, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { LiveMetrics, MetricsHistoryPoint, TelemetryHealth, TestStatus, WSMessage } from '../types'

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

export function useExecutionMetricsStream(testId: string, status?: TestStatus) {
  const [localMetricsHistory, setLocalMetricsHistory] = useState<MetricsHistoryPoint[]>([])
  const [latestMetrics, setLatestMetrics] = useState<LiveMetrics>()

  const handleTestWSMessage = useCallback((msg: WSMessage) => {
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
      tps: Math.round(msg.data.tps * 10) / 10,
      avgMs: Math.round(msg.data.avg_response_ms * 10) / 10,
      errorPct: Math.round(msg.data.error_pct * 100) / 100,
      p50: Math.round(msg.data.p50_ms * 10) / 10,
      p90: Math.round(msg.data.p90_ms * 10) / 10,
      p99: Math.round(msg.data.p99_ms * 10) / 10,
    }

    if (msg.data.groups) {
      for (const [name, groupMetrics] of Object.entries(msg.data.groups)) {
        point[`${name}_tps`] = Math.round(groupMetrics.tps * 10) / 10
        point[`${name}_avgMs`] = Math.round(groupMetrics.avg_response_ms * 10) / 10
        point[`${name}_vusers`] = groupMetrics.vusers
      }
    }

    if (msg.data.resources) {
      const resources = msg.data.resources
      point.cpuProcess = Math.round(resources.cpu_process_pct * 10) / 10
      point.cpuFree = Math.round(resources.cpu_free_pct * 10) / 10
      point.memProcess = Math.round(resources.mem_process_mb * 10) / 10
      point.memFree = Math.round(resources.mem_free_mb * 10) / 10
      point.netSent = Math.round(resources.net_sent_kbps * 10) / 10
      point.netRecv = Math.round(resources.net_recv_kbps * 10) / 10
      point.diskRead = Math.round(resources.disk_read_kbps * 10) / 10
      point.diskWrite = Math.round(resources.disk_write_kbps * 10) / 10
    }

    setLocalMetricsHistory((prev) => [...prev, point].slice(-EXECUTION_STREAM_HISTORY_LIMIT))
  }, [])

  useWebSocket({
    testId,
    enabled: shouldStreamExecutionMetrics(status),
    onMessage: handleTestWSMessage,
  })

  return { localMetricsHistory, latestMetrics }
}