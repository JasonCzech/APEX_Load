import { useMemo } from 'react'

import { MetricsHistoryPoint, TestStatus, TestSummary } from '../types'

// Shared query-window derivation for the Prometheus-backed graph hooks. Both the
// curated Prometheus pack and the imported-dashboard panels must poll on the SAME
// quantized window, or the two graph sets tear against each other on the shared
// cursor; extracting this guarantees that by construction and keeps the quantization
// correctness comment in exactly one place.

export const LIVE_PROMETHEUS_REFRESH_STATUSES = new Set<TestStatus>(['SHAKEOUT', 'INITIALIZING', 'RUNNING', 'PAUSED', 'STOPPING'])

export interface PrometheusQueryWindow {
  windowStartMs: number | null
  windowEndMs: number | null
  windowStepSeconds: number | null
}

function parseTimeMs(value?: string) {
  if (!value) return undefined
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function visibleDataWindow(filteredData: MetricsHistoryPoint[]): { startMs: number; endMs: number } | null {
  const timestamps = filteredData
    .map(point => point.timestampMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (timestamps.length < 2) return null
  const startMs = timestamps[0]
  const endMs = timestamps[timestamps.length - 1]
  return endMs > startMs ? { startMs, endMs } : null
}

function fallbackRunWindow(test: TestSummary | null): { startMs: number; endMs: number } | null {
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

// quantizeMs snaps a timestamp to a bucket boundary so that the query window only changes when it
// crosses a genuine boundary, not on every WebSocket data tick (~1-2s). Without this the window
// object identity changes each tick, tearing down the 10s poll and refetching all graphs.
export function quantizeMs(valueMs: number, bucketMs: number, mode: 'floor' | 'ceil') {
  if (bucketMs <= 0) return valueMs
  const ratio = valueMs / bucketMs
  return (mode === 'floor' ? Math.floor(ratio) : Math.ceil(ratio)) * bucketMs
}

// usePrometheusQueryWindow derives the quantized scalar window from the visible data
// (falling back to the run window). Callers depend on the three scalar values rather
// than a memoized object, so the fetch effect only re-runs when the window actually
// crosses a step boundary.
export function usePrometheusQueryWindow(test: TestSummary | null, filteredData: MetricsHistoryPoint[]): PrometheusQueryWindow {
  const queryWindow = useMemo(() => {
    const window = visibleDataWindow(filteredData) ?? fallbackRunWindow(test)
    if (!window) return null
    const stepSeconds = prometheusStepSeconds(window.startMs, window.endMs)
    // Quantize the raw endpoints to the step bucket so the window stays referentially stable
    // between genuine boundaries; new data ticks within the same bucket do not move it.
    const bucketMs = Math.max(stepSeconds, 10) * 1000
    return {
      startMs: quantizeMs(window.startMs, bucketMs, 'floor'),
      endMs: quantizeMs(window.endMs, bucketMs, 'ceil'),
      stepSeconds,
    }
  }, [filteredData, test])

  return {
    windowStartMs: queryWindow?.startMs ?? null,
    windowEndMs: queryWindow?.endMs ?? null,
    windowStepSeconds: queryWindow?.stepSeconds ?? null,
  }
}
