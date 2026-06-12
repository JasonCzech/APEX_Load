import { useEffect, useMemo, useState } from 'react'

import { MetricsHistoryPoint, PrometheusGraph, TestStatus, TestSummary } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'

interface PrometheusGraphWindow {
  startMs: number
  endMs: number
  stepSeconds: number
}

const LIVE_PROMETHEUS_REFRESH_STATUSES = new Set<TestStatus>(['SHAKEOUT', 'INITIALIZING', 'RUNNING', 'PAUSED', 'STOPPING'])

function parseTimeMs(value?: string) {
  if (!value) return undefined
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function visibleDataWindow(filteredData: MetricsHistoryPoint[]): Pick<PrometheusGraphWindow, 'startMs' | 'endMs'> | null {
  const timestamps = filteredData
    .map(point => point.timestampMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (timestamps.length < 2) return null
  const startMs = timestamps[0]
  const endMs = timestamps[timestamps.length - 1]
  return endMs > startMs ? { startMs, endMs } : null
}

function fallbackRunWindow(test: TestSummary | null): Pick<PrometheusGraphWindow, 'startMs' | 'endMs'> | null {
  const startMs = parseTimeMs(test?.started_at || test?.start_time || test?.created_at)
  if (startMs === undefined) return null
  const endMs = parseTimeMs(test?.ended_at) ?? Date.now()
  if (endMs > startMs) return { startMs, endMs }
  return { startMs, endMs: startMs + 60_000 }
}

function prometheusStepSeconds(startMs: number, endMs: number) {
  const rangeSeconds = Math.max(1, Math.ceil((endMs - startMs) / 1000))
  return Math.min(3600, Math.max(5, Math.ceil(rangeSeconds / 420)))
}

export function usePrometheusGraphs(test: TestSummary | null, filteredData: MetricsHistoryPoint[]) {
  const { getPrometheusGraphs } = useSystemApi()
  const [graphs, setGraphs] = useState<PrometheusGraph[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [available, setAvailable] = useState(false)

  const queryWindow = useMemo<PrometheusGraphWindow | null>(() => {
    const window = visibleDataWindow(filteredData) ?? fallbackRunWindow(test)
    if (!window) return null
    return {
      ...window,
      stepSeconds: prometheusStepSeconds(window.startMs, window.endMs),
    }
  }, [
    filteredData,
    test?.created_at,
    test?.ended_at,
    test?.start_time,
    test?.started_at,
  ])

  useEffect(() => {
    if (!queryWindow) {
      setGraphs([])
      setLoading(false)
      setError('')
      setAvailable(false)
      return
    }

    let cancelled = false
    let timer: ReturnType<typeof window.setInterval> | undefined

    const load = async (showLoading: boolean) => {
      if (showLoading) setLoading(true)
      try {
        const response = await getPrometheusGraphs({
          start: new Date(queryWindow.startMs).toISOString(),
          end: new Date(queryWindow.endMs).toISOString(),
          step_seconds: queryWindow.stepSeconds,
        })
        if (cancelled) return
        const enabledAndConfigured = response.enabled && response.configured
        setAvailable(enabledAndConfigured)
        setGraphs(enabledAndConfigured ? response.graphs : [])
        setError('')
      } catch (err) {
        if (cancelled) return
        setGraphs([])
        setAvailable(true)
        setError(getErrorMessage(err, 'Prometheus metrics unavailable.'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    void load(true)
    if (test?.status && LIVE_PROMETHEUS_REFRESH_STATUSES.has(test.status)) {
      timer = window.setInterval(() => {
        void load(false)
      }, 10_000)
    }

    return () => {
      cancelled = true
      if (timer !== undefined) window.clearInterval(timer)
    }
  }, [getPrometheusGraphs, queryWindow, test?.status])

  return { prometheusGraphs: graphs, prometheusLoading: loading && available, prometheusError: error }
}
