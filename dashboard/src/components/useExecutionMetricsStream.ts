import { useCallback, useEffect, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { useWebSocket } from '../hooks/useWebSocket'
import { LiveMetrics, MetricsHistoryPoint, NetworkLatency, TelemetryHealth, TestStatus, WSMessage } from '../types'
import { routeLatencySeriesValuesForTarget } from './latencySeries'
import { mapExecutionHistory, mapExecutionMetricToHistoryPoint, mergeMetricsHistory } from './testExecutionMetricsModel'

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
  const { getTest } = useApi()
  const [localMetricsHistory, setLocalMetricsHistory] = useState<MetricsHistoryPoint[]>([])
  const [latestMetrics, setLatestMetrics] = useState<LiveMetrics>()

  // Distinguish the first socket open (nothing has been missed yet) from a
  // reconnect (a window may have been missed while the socket was down, and
  // needs backfilling). Reset on test switch so run B's first open isn't treated
  // as a reconnect.
  const hasConnectedRef = useRef(false)
  // Guard a late-resolving backfill against a test switch or unmount.
  const activeTestIdRef = useRef(testId)
  activeTestIdRef.current = testId
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // Clear accumulated stream state when switching tests so run B never shows run
  // A's history/last-metrics snapshot (the parent reuses this hook instance
  // without a key, and the next run may not stream, so nothing else clears it).
  useEffect(() => {
    setLocalMetricsHistory([])
    setLatestMetrics(undefined)
    hasConnectedRef.current = false
  }, [testId])

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

  // On reconnect, pull the controller's recorded history for this run and merge
  // it into the live series, filling any window missed while the socket was down
  // (e.g. a backgrounded tab the browser froze, or the machine sleeping). The
  // first open is skipped — nothing has been missed, and the parent already
  // seeds history from the test snapshot.
  const handleStreamOpen = useCallback(() => {
    if (!hasConnectedRef.current) {
      hasConnectedRef.current = true
      return
    }
    if (!testId) return
    void getTest(testId)
      .then((test) => {
        if (!mountedRef.current || activeTestIdRef.current !== testId) return
        const backfill = mapExecutionHistory(test?.history)
        if (backfill.length === 0) return
        setLocalMetricsHistory((prev) =>
          mergeMetricsHistory(prev, backfill).slice(-EXECUTION_STREAM_HISTORY_LIMIT),
        )
      })
      .catch(() => {
        // Best-effort backfill; the live stream resumes regardless.
      })
  }, [getTest, testId])

  useWebSocket({
    testId,
    enabled: shouldStreamExecutionMetrics(status),
    onMessage: handleTestWSMessage,
    onOpen: handleStreamOpen,
  })

  return { localMetricsHistory, latestMetrics }
}
