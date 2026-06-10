import { describe, expect, it } from 'vitest'

import {
  buildExecutionChartData,
  filterExecutionChartData,
  mapArchiveResourceSamples,
  mapArchiveSummarySamples,
  mapExecutionHistory,
  mergeArchiveResourceHistory,
  summarizeExecutionChartData,
} from './testExecutionMetricsModel'
import { latencySeriesKeyForTarget, routeLatencyDetailSeriesKeyForTarget } from './latencySeries'

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
      transactions: {
        PublishOrder: {
          name: 'PublishOrder',
          dependency_type: 'jms_queue',
          count: 3,
          errors: 0,
          avg_ms: 90,
          p50_ms: 80,
          p90_ms: 100,
          p99_ms: 120,
          server_telemetry: {
            samples: 3,
            avg_consumer_lag_ms: 20,
            max_consumer_lag_ms: 28,
          },
        },
        RequestReply: {
          name: 'RequestReply',
          dependency_type: 'jms_queue',
          count: 2,
          errors: 0,
          avg_ms: 110,
          p50_ms: 100,
          p90_ms: 130,
          p99_ms: 150,
          server_telemetry: {
            samples: 2,
            avg_consumer_lag_ms: 50,
            max_consumer_lag_ms: 65,
          },
        },
      },
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
          p95_ms: 160,
          p99_ms: 220,
        },
      },
      latency: {
        targets: [{
          name: 'edge',
          endpoint: 'https://example.test',
          latency_ms: 42,
          probe_total_ms: 51,
          dns_ms: 2,
          connect_ms: 10,
          ttfb_ms: 45,
          jitter_ms: 1.5,
          success_rate_pct: 92,
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
        cpu_iowait_pct: 7,
        gc_pause_ms: 12,
        container_cpu_usage_pct: 88,
        container_cpu_throttled_seconds_rate: 0.25,
        container_memory_usage_pct: 72,
        net_drop_in_per_sec: 1,
        net_drop_out_per_sec: 2,
        tcp_retransmits_per_sec: 3,
        disk_io_time_pct: 44,
      },
    }])

    expect(mapped).toHaveLength(1)
    const endpointKey = latencySeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' })
    const dnsKey = routeLatencyDetailSeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' }, 'dns')
    const successRateKey = routeLatencyDetailSeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' }, 'successRate')

    expect(mapped[0]).toMatchObject({
      vusers: 12,
      tps: 4.5,
      avgMs: 123,
      p95: 160,
      Checkout_tps: 4.5,
      Checkout_avgMs: 123,
      Checkout_vusers: 12,
      Checkout_transactions: 10,
      Checkout_errors: 1,
      Checkout_p50: 100,
      Checkout_p90: 150,
      Checkout_p95: 160,
      Checkout_p99: 220,
      Checkout_errorPct: 9.09,
      [endpointKey]: 51,
      [dnsKey]: 2,
      [successRateKey]: 92,
      cpuProcess: 10,
      memProcess: 256,
      cpuIOWait: 7,
      gcPauseMs: 12,
      containerCPUUsagePct: 88,
      containerCPUThrottledSecondsRate: 0.3,
      containerMemoryUsagePct: 72,
      netDropsPerSec: 3,
      tcpRetransmitsPerSec: 3,
      diskIOTimePct: 44,
      jmsConsumerLagMs: 32,
      jmsPeakConsumerLagMs: 65,
    })
  })

  it('leaves an omitted group percentile undefined instead of coercing it to 0', () => {
    const [point] = mapExecutionHistory([{
      timestamp: '2026-05-12T20:00:00Z',
      active_vusers: 5,
      tps: 2,
      avg_response_ms: 100,
      error_pct: 0,
      total_transactions: 5,
      total_errors: 0,
      p50_ms: 100,
      p90_ms: 150,
      p95_ms: 200,
      p99_ms: 220,
      groups: {
        Checkout: {
          name: 'Checkout',
          vusers: 5,
          transactions: 5,
          errors: 0,
          tps: 2,
          avg_response_ms: 100,
          p50_ms: 100,
          p90_ms: 150,
          // p95_ms omitted by the server (omitempty); must stay a gap, not 0.
          p99_ms: 220,
        },
      },
    }])

    expect(point.Checkout_p95).toBeUndefined()
    expect(point.Checkout_p50).toBe(100)
    expect(point.Checkout_p99).toBe(220)
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

  it('adds relative run time offsets from chart timestamps', () => {
    const chartData = buildExecutionChartData({
      mappedBackendHistory: [],
      localMetricsHistory: [],
      fallbackHistory: [
        { time: '10:00:00', timestampMs: 1_000, vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
        { time: '10:00:05', timestampMs: 6_000, vusers: 4, tps: 2, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135 },
      ],
    })

    expect(chartData.map((point) => point.relativeMs)).toEqual([0, 5_000])
  })

  it('maps archived resource samples into resource chart points', () => {
    const mapped = mapArchiveResourceSamples([{
      timestamp: '2026-05-12T20:00:01.123Z',
      active_vusers: 8,
      cpu_process_pct: 11,
      cpu_free_pct: 70,
      mem_process_mb: 256,
      mem_free_mb: 4096,
      net_sent_kbps: 12,
      net_recv_kbps: 18,
      disk_read_kbps: 2,
      disk_write_kbps: 4,
      cpu_steal_pct: 2,
      gc_pause_ms: 9,
      container_cpu_usage_pct: 66,
      container_memory_usage_pct: 81,
      net_error_in_per_sec: 4,
      net_error_out_per_sec: 5,
      disk_io_time_pct: 37,
    }])

    expect(mapped[0]).toMatchObject({
      timestampMs: Date.parse('2026-05-12T20:00:01.123Z'),
      vusers: 8,
      tps: 0,
      p95: 0,
      resourceSampleOnly: 1,
      cpuProcess: 11,
      memProcess: 256,
      netSent: 12,
      diskWrite: 4,
      cpuSteal: 2,
      gcPauseMs: 9,
      containerCPUUsagePct: 66,
      containerMemoryUsagePct: 81,
      netErrorsPerSec: 9,
      diskIOTimePct: 37,
    })
  })

  it('maps archived interval summaries into performance chart points', () => {
    const mapped = mapArchiveSummarySamples([{
      timestamp: '2026-05-12T20:00:01.123Z',
      test_id: 'test-1',
      test_name: 'Checkout',
      active_vusers: 12,
      tps: 8.5,
      avg_response_ms: 140,
      error_pct: 1.25,
      total_transactions: 20,
      total_errors: 1,
      p50_ms: 90,
      p90_ms: 130,
      p95_ms: 155,
      p99_ms: 220,
      latency: {
        targets: [{
          name: 'edge',
          endpoint: 'https://example.test',
          latency_ms: 42,
          probe_total_ms: 52,
          ttfb_ms: 44,
          last_success: true,
          timestamp: '2026-05-12T20:00:01.123Z',
        }],
      },
    }])

    const endpointKey = latencySeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' })
    const ttfbKey = routeLatencyDetailSeriesKeyForTarget({ name: 'edge', endpoint: 'https://example.test' }, 'ttfb')

    expect(mapped[0]).toMatchObject({
      timestampMs: Date.parse('2026-05-12T20:00:01.123Z'),
      vusers: 12,
      tps: 8.5,
      avgMs: 140,
      errorPct: 1.25,
      p50: 90,
      p90: 130,
      p95: 155,
      p99: 220,
      [endpointKey]: 52,
      [ttfbKey]: 44,
    })
  })

  it('merges archived resource samples into matching interval chart points', () => {
    const baseHistory = [{
      time: '8:00:01 PM',
      timestampMs: Date.parse('2026-05-12T20:00:01.123Z'),
      vusers: 8,
      tps: 6,
      avgMs: 120,
      errorPct: 1,
      p50: 90,
      p90: 120,
      p99: 180,
    }]
    const resourceHistory = mapArchiveResourceSamples([{
      timestamp: '2026-05-12T20:00:01.123Z',
      active_vusers: 8,
      cpu_process_pct: 11,
      cpu_free_pct: 70,
      mem_process_mb: 256,
      mem_free_mb: 4096,
      net_sent_kbps: 12,
      net_recv_kbps: 18,
      disk_read_kbps: 2,
      disk_write_kbps: 4,
      container_cpu_throttled_seconds_rate: 0.5,
      tcp_retransmits_per_sec: 6,
    }])

    const merged = mergeArchiveResourceHistory(baseHistory, resourceHistory)

    expect(merged).toHaveLength(1)
    expect(merged[0]).toMatchObject({
      tps: 6,
      avgMs: 120,
      cpuProcess: 11,
      cpuFree: 70,
      memProcess: 256,
      netSent: 12,
      diskRead: 2,
      containerCPUThrottledSecondsRate: 0.5,
      tcpRetransmitsPerSec: 6,
    })
  })

  it('enriches completed chart data with archived resources before adding relative run time', () => {
    const archiveResourceHistory = mapArchiveResourceSamples([{
      timestamp: '2026-05-12T20:00:00Z',
      active_vusers: 10,
      cpu_process_pct: 20,
      cpu_free_pct: 65,
      mem_process_mb: 512,
      mem_free_mb: 2048,
      net_sent_kbps: 80,
      net_recv_kbps: 96,
      disk_read_kbps: 7,
      disk_write_kbps: 9,
    }])

    const chartData = buildExecutionChartData({
      mappedBackendHistory: [{
        time: '8:00:00 PM',
        timestampMs: Date.parse('2026-05-12T20:00:00Z'),
        vusers: 10,
        tps: 5,
        avgMs: 100,
        errorPct: 0,
        p50: 80,
        p90: 100,
        p99: 130,
      }],
      localMetricsHistory: [],
      fallbackHistory: [],
      archiveResourceHistory,
      status: 'COMPLETE',
    })

    expect(chartData[0]).toMatchObject({
      relativeMs: 0,
      tps: 5,
      cpuProcess: 20,
      memProcess: 512,
      netRecv: 96,
      diskWrite: 9,
    })
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

  it('filters relative display windows across the full minute slider range', () => {
    const chartData = [
      { time: '10:00:00', timestampMs: 0, vusers: 1, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
      { time: '10:15:00', timestampMs: 15 * 60_000, vusers: 2, tps: 2, avgMs: 110, errorPct: 0, p50: 95, p90: 125, p99: 155 },
      { time: '10:30:00', timestampMs: 30 * 60_000, vusers: 3, tps: 3, avgMs: 120, errorPct: 0, p50: 100, p90: 130, p99: 160 },
      { time: '10:31:00', timestampMs: 31 * 60_000, vusers: 4, tps: 4, avgMs: 130, errorPct: 0, p50: 105, p90: 135, p99: 165 },
    ]

    expect(filterExecutionChartData(chartData, '1m', null).map(point => point.time)).toEqual(['10:30:00', '10:31:00'])
    expect(filterExecutionChartData(chartData, '30m', null).map(point => point.time)).toEqual(['10:15:00', '10:30:00', '10:31:00'])
    expect(filterExecutionChartData(chartData, 'all', null)).toBe(chartData)
  })
})
