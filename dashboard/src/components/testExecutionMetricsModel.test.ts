import { describe, expect, it } from 'vitest'

import {
  buildExecutionChartData,
  filterExecutionChartData,
  mapExecutionHistory,
  summarizeExecutionChartData,
} from './testExecutionMetricsModel'

describe('testExecutionMetricsModel', () => {
  it('maps backend execution history into chart points with group and latency metrics', () => {
    const mapped = mapExecutionHistory([{
      timestamp: '2026-05-12T20:00:00Z',
      active_vusers: 12,
      tps: 4.5,
      avg_response_ms: 123,
      error_pct: 1.5,
      total_transactions: 10,
      total_errors: 1,
      p50_ms: 100,
      p90_ms: 150,
      p95_ms: 160,
      p99_ms: 220,
      groups: {
        Checkout: {
          name: 'Checkout',
          vusers: 12,
          transactions: 10,
          errors: 1,
          tps: 4.5,
          avg_response_ms: 123,
          p50_ms: 100,
          p90_ms: 150,
          p99_ms: 220,
        },
      },
      latency: {
        targets: [{
          name: 'edge',
          endpoint: 'https://example.test',
          latency_ms: 42,
          last_success: true,
          timestamp: '2026-05-12T20:00:00Z',
        }],
      },
      resources: {
        cpu_process_pct: 10,
        cpu_free_pct: 70,
        mem_process_mb: 256,
        mem_free_mb: 4096,
        net_sent_kbps: 12,
        net_recv_kbps: 18,
        disk_read_kbps: 2,
        disk_write_kbps: 4,
      },
    }])

    expect(mapped).toHaveLength(1)
    expect(mapped[0]).toMatchObject({
      vusers: 12,
      tps: 4.5,
      avgMs: 123,
      Checkout_tps: 4.5,
      Checkout_avgMs: 123,
      Checkout_vusers: 12,
      latency_edge: 42,
      cpuProcess: 10,
      memProcess: 256,
    })
  })

  it('keeps the paused chart frozen shortly after TPS reaches zero', () => {
    const chartData = buildExecutionChartData({
      mappedBackendHistory: [],
      localMetricsHistory: [],
      fallbackHistory: [
        { time: '1', vusers: 10, tps: 5, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '2', vusers: 10, tps: 4, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '3', vusers: 8, tps: 2, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '4', vusers: 4, tps: 0.05, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '5', vusers: 2, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '6', vusers: 0, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '7', vusers: 0, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '8', vusers: 0, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '9', vusers: 0, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
        { time: '10', vusers: 0, tps: 0, avgMs: 10, errorPct: 0, p50: 10, p90: 20, p99: 30 },
      ],
      status: 'PAUSED',
    })

    expect(chartData.map((point) => point.time)).toEqual(['1', '2', '3', '4', '5', '6', '7', '8'])
  })

  it('filters custom zoom ranges and summarizes the resulting slice', () => {
    const filtered = filterExecutionChartData([
      { time: '10:00:00', vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
      { time: '10:00:01', vusers: 4, tps: 2, avgMs: 120, errorPct: 1, p50: 100, p90: 140, p99: 180 },
      { time: '10:00:02', vusers: 6, tps: 3, avgMs: 140, errorPct: 2, p50: 110, p90: 150, p99: 190 },
    ], 'custom', { left: '10:00:01', right: '10:00:02' })

    expect(filtered).toHaveLength(2)
    expect(summarizeExecutionChartData(filtered)).toEqual({
      avgTps: 2.5,
      avgLatency: 130,
      avgError: 1.5,
      maxVusers: 6,
    })
  })
})