// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MetricsHistoryPoint, PrometheusGraphsResponse, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const getPrometheusGraphs = vi.fn<
  (params: { start: string; end: string; step_seconds: number }) => Promise<PrometheusGraphsResponse>
>()

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getPrometheusGraphs }),
}))

// Imported after the mock is registered.
import { usePrometheusGraphs } from './usePrometheusGraphs'

function okResponse(): PrometheusGraphsResponse {
  return {
    enabled: true,
    configured: true,
    generated_at: '2026-06-12T12:00:00Z',
    graphs: [{ id: 'cpu', title: 'CPU', series: [] }],
  }
}

function Harness({ test, filteredData }: { test: TestSummary | null; filteredData: MetricsHistoryPoint[] }) {
  usePrometheusGraphs(test, filteredData)
  return null
}

function point(timestampMs: number): MetricsHistoryPoint {
  return {
    time: new Date(timestampMs).toISOString(),
    timestampMs,
    vusers: 10,
    tps: 5,
    avgMs: 100,
    errorPct: 0,
    p50: 90,
    p90: 150,
    p95: 180,
    p99: 220,
  }
}

function runningTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Prom Run',
    status: 'RUNNING',
    total_vusers: 10,
    duration: '10m',
    created_at: '2026-06-12T12:00:00Z',
    started_at: '2026-06-12T12:00:00Z',
    groups: [{ name: 'g1', protocol: 'http', script_id: 's1', script_name: 'S1', vusers: 10 }],
  }
}

describe('usePrometheusGraphs', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    getPrometheusGraphs.mockReset()
    getPrometheusGraphs.mockResolvedValue(okResponse())
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.useRealTimers()
  })

  const render = async (test: TestSummary | null, filteredData: MetricsHistoryPoint[]) => {
    await act(async () => {
      root.render(<Harness test={test} filteredData={filteredData} />)
      await Promise.resolve()
    })
  }

  it('does not refetch when data ticks within the same step bucket', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    // A short range yields the minimum 5s step, quantized to a 10s bucket.
    const initial = [point(base), point(base + 2000)]
    await render(runningTest(), initial)

    expect(getPrometheusGraphs).toHaveBeenCalledTimes(1)
    const callCountAfterMount = getPrometheusGraphs.mock.calls.length

    // Simulate several WebSocket ticks that append points within the same 10s bucket. Each tick
    // passes a brand-new array (new identity), the way the real consumer does.
    for (let i = 1; i <= 4; i++) {
      await render(runningTest(), [...initial, point(base + 2000 + i * 1000)])
    }

    // The window is referentially stable across same-bucket ticks, so the effect must not re-run.
    expect(getPrometheusGraphs.mock.calls.length).toBe(callCountAfterMount)
  })

  it('refetches once when the window crosses a step boundary', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    const before = getPrometheusGraphs.mock.calls.length

    // Advance the latest data point well past the 10s bucket so endMs crosses a boundary.
    await render(runningTest(), [point(base), point(base + 30000)])

    expect(getPrometheusGraphs.mock.calls.length).toBe(before + 1)
  })

  it('polls on the 10s interval for a live run without per-tick teardown', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    expect(getPrometheusGraphs).toHaveBeenCalledTimes(1)

    await act(async () => {
      vi.advanceTimersByTime(10_000)
      await Promise.resolve()
    })

    // The interval poll fires exactly one additional fetch.
    expect(getPrometheusGraphs).toHaveBeenCalledTimes(2)
  })
})
