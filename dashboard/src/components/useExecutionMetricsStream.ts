import { useCallback, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { LiveMetrics, MetricsHistoryPoint, NetworkLatency, TelemetryHealth, TestStatus, WSMessage } from '../types'
import { routeLatencySeriesValuesForTarget } from './latencySeries'
import { mapExecutionMetricToHistoryPoint } from './testExecutionMetricsModel'

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
    Object.entries(routeLatencySeriesValuesForTarget(target)).forEach(([key, value]) => {
      point[key] = rounded(value)
    })
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

    const point = mapExecutionMetricToHistoryPoint({ ...msg.data, timestamp: msg.timestamp })

    setLocalMetricsHistory((prev) => [...prev, applyLatencyTargets(point, msg.data?.latency)].slice(-EXECUTION_STREAM_HISTORY_LIMIT))
  }, [])

  useWebSocket({
    testId,
    enabled: shouldStreamExecutionMetrics(status),
    onMessage: handleTestWSMessage,
  })

  return { localMetricsHistory, latestMetrics }
}
