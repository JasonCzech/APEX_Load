// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestStatus, WSMessage } from '../types'
import { latencySeriesKeyForTarget, routeLatencyDetailSeriesKeyForTarget } from './latencySeries'
import { EXECUTION_STREAM_HISTORY_LIMIT, useExecutionMetricsStream } from './useExecutionMetricsStream'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const useWebSocketMock = vi.fn()
const getTestMock = vi.fn()
const edgeLatencyKey = latencySeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' })
const edgeDnsKey = routeLatencyDetailSeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' }, 'dns')
const edgeSuccessRateKey = routeLatencyDetailSeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' }, 'successRate')

vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: (...args: unknown[]) => useWebSocketMock(...args),
}))

vi.mock('../hooks/api/useTestApi', () => ({
  useTestApi: () => ({ getTest: getTestMock }),
}))

function MetricsStreamHarness({ status = 'RUNNING', testId = 'test-1' }: { status?: TestStatus; testId?: string }) {
  const { localMetricsHistory, latestMetrics } = useExecutionMetricsStream(testId, status)
  const latest = localMetricsHistory[localMetricsHistory.length - 1]

  return (
    <div>
      <span data-testid="count">{String(localMetricsHistory.length)}</span>
      <span data-testid="tps">{String(latest?.tps ?? -1)}</span>
      <span data-testid="group-tps">{String(latest?.Checkout_tps ?? -1)}</span>
      <span data-testid="group-p95">{String(latest?.Checkout_p95 ?? -1)}</span>
      <span data-testid="cpu">{String(latest?.cpuProcess ?? -1)}</span>
      <span data-testid="impact-throttle">{String(latest?.containerCPUThrottledSecondsRate ?? -1)}</span>
      <span data-testid="impact-net-errors">{String(latest?.netErrorsPerSec ?? -1)}</span>
      <span data-testid="route-latency">{String(latest?.[edgeLatencyKey] ?? -1)}</span>
      <span data-testid="route-dns">{String(latest?.[edgeDnsKey] ?? -1)}</span>
      <span data-testid="route-success">{String(latest?.[edgeSuccessRateKey] ?? -1)}</span>
      <span data-testid="latest-route-latency">{String(latestMetrics?.latency?.targets?.[0]?.latency_ms ?? -1)}</span>
      <span data-testid="delivery-lag">{String(latestMetrics?.telemetry?.delivery_lag_ms ?? -1)}</span>
      <span data-testid="end-to-end-age">{String(latestMetrics?.telemetry?.end_to_end_age_ms ?? -1)}</span>
      <span data-testid="transport-lag">{String(latestMetrics?.telemetry?.transport_lag_ms ?? -1)}</span>
      <span data-testid="fanout-lag">{String(latestMetrics?.telemetry?.controller_fanout_lag_ms ?? -1)}</span>
      <span data-testid="received-at">{String(latestMetrics?.telemetry?.client_received_at ?? '')}</span>
    </div>
  )
}

describe('useExecutionMetricsStream', () => {
  let container: HTMLDivElement
  let root: Root
  let onMessage: ((msg: WSMessage) => void) | undefined
  let onOpen: (() => void) | undefined

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-05-12T20:05:01.250Z'))
    getTestMock.mockResolvedValue({ history: [] })
    useWebSocketMock.mockImplementation(({ onMessage: handler, onOpen: openHandler }: { onMessage?: (msg: WSMessage) => void; onOpen?: () => void }) => {
      onMessage = handler
      onOpen = openHandler
      return { connected: true, connectionState: 'connected' }
    })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    onMessage = undefined
    onOpen = undefined
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('maps metrics update websocket messages into execution history points', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })

    expect(useWebSocketMock).toHaveBeenCalled()

    await emit({
      type: 'metrics_update',
      timestamp: '2026-05-12T20:05:00Z',
      data: {
        active_vusers: 15,
        tps: 12.34,
        avg_response_ms: 221.19,
        error_pct: 1.234,
        total_transactions: 120,
        total_errors: 2,
        p50_ms: 150.44,
        p90_ms: 320.22,
        p95_ms: 410.31,
        p99_ms: 501.66,
        groups: {
          Checkout: {
            name: 'Checkout',
            tps: 7.89,
            avg_response_ms: 180.11,
            vusers: 9,
            transactions: 70,
            errors: 3,
            p50_ms: 120,
            p90_ms: 230,
            p95_ms: 260,
            p99_ms: 340,
          },
        },
        resources: {
          cpu_process_pct: 44.43,
          cpu_free_pct: 55.51,
          mem_process_mb: 300.49,
          mem_free_mb: 1200.67,
          net_sent_kbps: 40.21,
          net_recv_kbps: 50.19,
          disk_read_kbps: 10.17,
          disk_write_kbps: 20.16,
          container_cpu_throttled_seconds_rate: 0.26,
          net_error_in_per_sec: 2.14,
          net_error_out_per_sec: 3.12,
        },
        telemetry: {
          collected_at: '2026-05-12T20:05:00.000Z',
          generator_published_at: '2026-05-12T20:05:00.600Z',
          controller_received_at: '2026-05-12T20:05:00.900Z',
          published_at: '2026-05-12T20:05:00.900Z',
          generator_processing_lag_ms: 600,
          transport_lag_ms: 300,
          controller_fanout_lag_ms: 350,
          sample_age_ms: 900,
        },
        latency: {
          targets: [{
            name: 'edge',
            endpoint: 'https://example.test',
            latency_ms: 42.26,
            probe_total_ms: 51.25,
            dns_ms: 2.5,
            success_rate_pct: 91.666,
            last_success: true,
            timestamp: '2026-05-12T20:05:00Z',
          }],
        },
      },
    } as unknown as WSMessage)

    expect(readTestId('count')).toBe('1')
    expect(readTestId('tps')).toBe('12.3')
    expect(readTestId('group-tps')).toBe('7.9')
    expect(readTestId('group-p95')).toBe('260')
    expect(readTestId('cpu')).toBe('44.4')
    expect(readTestId('impact-throttle')).toBe('0.3')
    expect(readTestId('impact-net-errors')).toBe('5.3')
    expect(readTestId('route-latency')).toBe('51.3')
    expect(readTestId('route-dns')).toBe('2.5')
    expect(readTestId('route-success')).toBe('91.7')
    expect(readTestId('delivery-lag')).toBe('350')
    expect(readTestId('end-to-end-age')).toBe('1250')
    expect(readTestId('transport-lag')).toBe('300')
    expect(readTestId('fanout-lag')).toBe('350')
    expect(readTestId('received-at')).toBe('2026-05-12T20:05:01.250Z')
  })

  it('merges latency update websocket messages into the latest execution history point', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })

    await emit({
      type: 'metrics_update',
      timestamp: '2026-05-12T20:05:00Z',
      data: {
        active_vusers: 15,
        tps: 12,
        avg_response_ms: 221,
        error_pct: 1,
        total_transactions: 120,
        total_errors: 2,
        p50_ms: 150,
        p90_ms: 320,
        p95_ms: 410,
        p99_ms: 501,
      },
    } as unknown as WSMessage)
    await emit({
      type: 'latency_update',
      latency: {
        targets: [{
          name: 'edge',
          endpoint: 'https://example.test',
          latency_ms: 27.44,
          probe_total_ms: 31.44,
          last_success: true,
          timestamp: '2026-05-12T20:05:05Z',
        }],
      },
    } as WSMessage)

    expect(readTestId('count')).toBe('1')
    expect(readTestId('route-latency')).toBe('31.4')
    expect(readTestId('latest-route-latency')).toBe('27.44')
  })

  it('disables the test websocket stream for completed runs', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness status="COMPLETE" />)
    })

    expect(useWebSocketMock).toHaveBeenCalledWith(expect.objectContaining({
      testId: 'test-1',
      enabled: false,
    }))
  })

  it('enables the test websocket stream during shakeout', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness status="SHAKEOUT" />)
    })

    expect(useWebSocketMock).toHaveBeenCalledWith(expect.objectContaining({
      testId: 'test-1',
      enabled: true,
    }))
  })

  it('retains full streamed execution history during an active run', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })

    for (let index = 0; index < 301; index += 1) {
      await emit({
        type: 'metrics_update',
        timestamp: '2026-05-12T20:05:00Z',
        data: {
          active_vusers: index,
          tps: index,
          avg_response_ms: index,
          error_pct: 0,
          p50_ms: index,
          p90_ms: index,
          p99_ms: index,
        },
      } as WSMessage)
    }

    expect(readTestId('count')).toBe('301')
    expect(readTestId('tps')).toBe('300')
  })

  // The 300-point cap was deleted outright and this file's guarding test was
  // reworded to bless the removal, leaving localMetricsHistory growing for the
  // whole run. History is bounded again — generously, so long runs keep more
  // than the old 300 points, but never without limit.
  // The 300-point cap was deleted outright and this file's guarding test was
  // reworded to bless the removal, leaving localMetricsHistory growing for the
  // whole run. History is bounded again — generously, so long runs keep far more
  // than the old 300 points, but never without limit.
  it('caps retained execution history at EXECUTION_STREAM_HISTORY_LIMIT', async () => {
    const oversized = Array.from({ length: EXECUTION_STREAM_HISTORY_LIMIT + 25 }, (_, index) => ({
      timestamp: new Date(Date.UTC(2026, 4, 12, 0, 0, index)).toISOString(),
      active_vusers: index,
      tps: index,
      avg_response_ms: index,
      error_pct: 0,
      p50_ms: index,
      p90_ms: index,
      p95_ms: index,
      p99_ms: index,
    }))
    getTestMock.mockResolvedValueOnce({ history: oversized })

    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })
    await triggerOpen()

    expect(Number(readTestId('count'))).toBe(EXECUTION_STREAM_HISTORY_LIMIT)
  })

  // The backfill test above only exercises the merge path (line 97/168). The
  // per-message append (line 149) is a SEPARATE .slice() and must be proven
  // independently, or unbounded live growth regresses undetected. Messages are
  // dispatched inside one act() so the loop stays fast.
  it('caps live streamed history at the limit and drops the oldest point', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })

    const overflow = 5
    await act(async () => {
      for (let index = 0; index < EXECUTION_STREAM_HISTORY_LIMIT + overflow; index += 1) {
        onMessage?.({
          type: 'metrics_update',
          timestamp: '2026-05-12T20:05:00Z',
          data: {
            active_vusers: index,
            tps: index,
            avg_response_ms: index,
            error_pct: 0,
            p50_ms: index,
            p90_ms: index,
            p99_ms: index,
          },
        } as WSMessage)
      }
    })

    expect(Number(readTestId('count'))).toBe(EXECUTION_STREAM_HISTORY_LIMIT)
    // The newest point survives and the oldest was evicted.
    expect(readTestId('tps')).toBe(String(EXECUTION_STREAM_HISTORY_LIMIT + overflow - 1))
  })

  it('resets accumulated history and latest metrics when the testId changes', async () => {
    await act(async () => {
      root.render(<MetricsStreamHarness testId="test-1" />)
    })

    await emit({
      type: 'metrics_update',
      timestamp: '2026-05-12T20:05:00Z',
      data: { active_vusers: 5, tps: 9, avg_response_ms: 100, error_pct: 0, p50_ms: 1, p90_ms: 1, p99_ms: 1 },
    } as WSMessage)
    expect(readTestId('count')).toBe('1')
    expect(readTestId('tps')).toBe('9')

    // Navigating to run B must not show run A's accumulated history/snapshot.
    await act(async () => {
      root.render(<MetricsStreamHarness testId="test-2" />)
    })
    expect(readTestId('count')).toBe('0')
    expect(readTestId('tps')).toBe('-1')
  })

  it('seeds history on first open and backfills only the missed window on reconnect', async () => {
    getTestMock
      .mockResolvedValueOnce({
        history: [
          { timestamp: '2026-05-12T20:04:00Z', tps: 4, active_vusers: 5, avg_response_ms: 100, error_pct: 0, p50_ms: 1, p90_ms: 1, p95_ms: 1, p99_ms: 1 },
        ],
      })
      .mockResolvedValueOnce({
        history: [
          { timestamp: '2026-05-12T20:06:00Z', tps: 99, active_vusers: 5, avg_response_ms: 100, error_pct: 0, p50_ms: 1, p90_ms: 1, p95_ms: 1, p99_ms: 1 },
        ],
      })

    await act(async () => {
      root.render(<MetricsStreamHarness />)
    })

    await triggerOpen()
    expect(getTestMock).toHaveBeenCalledWith('test-1', {
      includeHistory: true,
      limit: EXECUTION_STREAM_HISTORY_LIMIT,
      since: undefined,
    })
    expect(readTestId('count')).toBe('1')
    expect(readTestId('tps')).toBe('4')

    await emit({
      type: 'metrics_update',
      timestamp: '2026-05-12T20:05:00Z',
      data: { active_vusers: 5, tps: 9, avg_response_ms: 100, error_pct: 0, p50_ms: 1, p90_ms: 1, p99_ms: 1 },
    } as WSMessage)
    expect(readTestId('count')).toBe('2')
    expect(readTestId('tps')).toBe('9')

    await triggerOpen()

    expect(getTestMock).toHaveBeenLastCalledWith('test-1', {
      includeHistory: true,
      limit: EXECUTION_STREAM_HISTORY_LIMIT,
      since: '2026-05-12T20:05:00Z',
    })
    expect(readTestId('count')).toBe('3')
    expect(readTestId('tps')).toBe('99')
  })

  async function emit(message: WSMessage) {
    await act(async () => {
      onMessage?.(message)
    })
  }

  async function triggerOpen() {
    await act(async () => {
      onOpen?.()
      // Flush the getTest() promise chain that the backfill kicks off.
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})
