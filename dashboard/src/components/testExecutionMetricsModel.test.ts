import { describe, expect, it } from 'vitest'

import {
  buildExecutionChartData,
  carryForwardPositiveResponseTimes,
  estimateSampleIntervalMs,
  filterExecutionChartData,
  groupTransactionKey,
  mapArchiveResourceSamples,
  mapArchiveSummarySamples,
  mapExecutionHistory,
  mapExecutionMetricToHistoryPoint,
  markDataGaps,
  mergeArchiveResourceHistory,
  mergeMetricsHistory,
  recentGroupJourneyRates,
  recentMetricAverages,
  summarizeExecutionChartData,
} from './testExecutionMetricsModel'
import { latencySeriesKeyForTarget, routeLatencyDetailSeriesKeyForTarget } from './latencySeries'
import { groupTransactionTpsSeriesKey, transactionChartSeriesKey, transactionChartSeriesNames } from './transactionChartSeries'
import type { MetricsHistoryPoint, LiveMetrics } from '../types'

describe('testExecutionMetricsModel', () => {
  it('carries the last positive response time through intervals without samples', () => {
    const transactionAvgKey = transactionChartSeriesKey('Checkout', 'avgMs')
    const point = (time: string, values: Partial<MetricsHistoryPoint>): MetricsHistoryPoint => ({
      time,
      vusers: 1,
      tps: 1,
      avgMs: 0,
      errorPct: 0,
      p50: 0,
      p90: 0,
      p99: 0,
      ...values,
    })
    const points = [
      point('0', { avgMs: 0, Checkout_avgMs: 0, [transactionAvgKey]: 0 }),
      point('1', { avgMs: 120, p95: 180, Checkout_avgMs: 90, [transactionAvgKey]: 80 }),
      point('2', { tps: 0, avgMs: 0, p95: 0, Checkout_avgMs: Number.NaN }),
      point('3', { tps: 0, avgMs: Number.NaN, Checkout_avgMs: 110, [transactionAvgKey]: 0 }),
      point('4', { avgMs: 140, Checkout_avgMs: 0, [transactionAvgKey]: 95 }),
    ]

    const filled = carryForwardPositiveResponseTimes(points)

    expect(filled.map(point => point.avgMs)).toEqual([0, 120, 120, 120, 140])
    expect(filled.map(point => point.p95)).toEqual([undefined, 180, 180, 180, 180])
    expect(filled.map(point => point.Checkout_avgMs)).toEqual([0, 90, 90, 110, 110])
    expect(filled.map(point => point[transactionAvgKey])).toEqual([0, 80, 80, 80, 95])
    expect(filled[0]).toBe(points[0])
    expect(filled[1]).toBe(points[1])
  })

  it('forward-fills zero response-time sentinels even when throughput remains positive', () => {
    const points = [
      { time: '1', vusers: 1, tps: 1, avgMs: 120, errorPct: 0, p50: 100, p90: 140, p99: 180 },
      { time: '2', vusers: 1, tps: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0 },
    ]

    const filled = carryForwardPositiveResponseTimes(points)

    expect(filled[1]).not.toBe(points[1])
    expect(filled[1]).toMatchObject({ avgMs: 120, p50: 100, p90: 140, p99: 180 })
  })

  it('forward-fills empty response-time intervals in final chart data', () => {
    const point = (timestampMs: number, avgMs: number): MetricsHistoryPoint => ({
      time: String(timestampMs),
      timestampMs,
      vusers: 2,
      tps: 1,
      avgMs,
      errorPct: 0,
      p50: avgMs,
      p90: avgMs,
      p99: avgMs,
    })
    const chartData = buildExecutionChartData({
      mappedBackendHistory: [point(1, 100), point(2, 0), point(3, 0), point(4, 130)],
      localMetricsHistory: [],
      fallbackHistory: [],
    })

    expect(chartData.map(sample => sample.avgMs)).toEqual([100, 100, 100, 130])
    // Throughput remains untouched; it is not evidence that a zero latency
    // sentinel represents a measured zero-duration response.
    expect(chartData.map(sample => sample.tps)).toEqual([1, 1, 1, 1])
  })

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
          tps: 2.5,
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
          tps: 1.5,
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
      [transactionChartSeriesKey('PublishOrder', 'tps')]: 2.5,
      [transactionChartSeriesKey('PublishOrder', 'avgMs')]: 90,
      [transactionChartSeriesKey('PublishOrder', 'errorPct')]: 0,
      [transactionChartSeriesKey('RequestReply', 'tps')]: 1.5,
      [transactionChartSeriesKey('RequestReply', 'avgMs')]: 110,
      [transactionChartSeriesKey('RequestReply', 'errorPct')]: 0,
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
      tps: Number.NaN,
      p95: Number.NaN,
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
      transactions: {
        PublishOrder: {
          name: 'PublishOrder',
          tps: 3.5,
          count: 8,
          errors: 2,
          avg_ms: 175,
        },
      },
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
      [transactionChartSeriesKey('PublishOrder', 'tps')]: 3.5,
      [transactionChartSeriesKey('PublishOrder', 'avgMs')]: 175,
      [transactionChartSeriesKey('PublishOrder', 'errorPct')]: 25,
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

  it('preserves an authoritative zero-load interval over a stale resource gauge', () => {
    const timestampMs = Date.parse('2026-05-12T20:00:01.123Z')
    const merged = mergeArchiveResourceHistory(
      [{
        time: '8:00:01 PM',
        timestampMs,
        vusers: 0,
        tps: 0,
        avgMs: 0,
        errorPct: 0,
        p50: 0,
        p90: 0,
        p95: 0,
        p99: 0,
      }],
      [{
        time: '8:00:01 PM',
        timestampMs,
        vusers: 8,
        tps: Number.NaN,
        avgMs: Number.NaN,
        errorPct: Number.NaN,
        p50: Number.NaN,
        p90: Number.NaN,
        p95: Number.NaN,
        p99: Number.NaN,
        cpuProcess: 11,
      }],
    )

    expect(merged).toHaveLength(1)
    expect(merged[0].vusers).toBe(0)
    expect(merged[0].cpuProcess).toBe(11)
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
      { time: '10:00:00', timestampMs: 0, vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
      { time: '10:00:01', timestampMs: 1, vusers: 4, tps: 2, avgMs: 120, errorPct: 1, p50: 100, p90: 140, p99: 180 },
      { time: '10:00:02', timestampMs: 2, vusers: 6, tps: 3, avgMs: 140, errorPct: 2, p50: 110, p90: 150, p99: 190 },
    ], 'custom', { startTimestampMs: 1, endTimestampMs: 2 })

    expect(filtered).toHaveLength(2)
    expect(summarizeExecutionChartData(filtered)).toEqual({
      avgTps: 2.5,
      avgLatency: 130,
      avgError: 1.5,
      maxVusers: 6,
    })
  })

  it('keeps custom zoom mode stable when live retention evicts its anchors', () => {
    const chartData = [
      { time: '10:00:01', timestampMs: 1, vusers: 1, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
      { time: '10:00:02', timestampMs: 2, vusers: 2, tps: 2, avgMs: 110, errorPct: 0, p50: 95, p90: 125, p99: 155 },
      { time: '10:00:03', timestampMs: 3, vusers: 3, tps: 3, avgMs: 120, errorPct: 0, p50: 100, p90: 130, p99: 160 },
    ]

    expect(filterExecutionChartData(chartData, 'custom', { startTimestampMs: 0, endTimestampMs: 2 }))
      .toEqual(chartData.slice(0, 2))
    expect(filterExecutionChartData(chartData, 'custom', { startTimestampMs: 2, endTimestampMs: 4 }))
      .toEqual(chartData.slice(1))
    expect(filterExecutionChartData(chartData, 'custom', { startTimestampMs: -2, endTimestampMs: -1 }))
      .toEqual(chartData.slice(0, 1))
    expect(filterExecutionChartData(chartData, 'custom', { startTimestampMs: 4, endTimestampMs: 5 }))
      .toEqual(chartData.slice(-1))
    expect(filterExecutionChartData(chartData, 'custom', null)).toBe(chartData)
  })

  it('uses timestamp anchors when display labels repeat', () => {
    const chartData = [
      { time: '01:30:00', timestampMs: 1, vusers: 1, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
      { time: '01:30:00', timestampMs: 2, vusers: 2, tps: 2, avgMs: 110, errorPct: 0, p50: 95, p90: 125, p99: 155 },
      { time: '01:30:01', timestampMs: 3, vusers: 3, tps: 3, avgMs: 120, errorPct: 0, p50: 100, p90: 130, p99: 160 },
    ]

    expect(filterExecutionChartData(chartData, 'custom', { startTimestampMs: 2, endTimestampMs: 3 }))
      .toEqual(chartData.slice(1))
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

  it('marks data gaps with a value-less spacer row without tripping on jitter', () => {
    const point = (timestampMs: number): MetricsHistoryPoint => ({
      time: new Date(timestampMs).toLocaleTimeString(),
      timestampMs,
      vusers: 5,
      tps: 5,
      avgMs: 100,
      errorPct: 0,
      p50: 90,
      p90: 120,
      p99: 150,
    })
    // Six 1s samples, then a 10s gap, then two more 1s samples (jitter on the +1050 step is ignored).
    const base = 1_000_000
    const points = [
      point(base),
      point(base + 1000),
      point(base + 2050), // +1050ms jitter — must NOT be flagged
      point(base + 3050),
      point(base + 4050),
      point(base + 5050),
      point(base + 15_050), // +10s — a real gap
      point(base + 16_050),
    ]

    expect(estimateSampleIntervalMs(points)).toBe(1000)
    const gapped = markDataGaps(points)
    const spacers = gapped.filter(p => p.isGapBoundary)
    expect(spacers).toHaveLength(1)
    // The spacer carries no series values, so every line/area breaks across it.
    expect(spacers[0].tps).toBeUndefined()
    expect(spacers[0].avgMs).toBeUndefined()
    expect(gapped).toHaveLength(points.length + 1)
  })

  it('leaves short series untouched', () => {
    const points: MetricsHistoryPoint[] = [
      { time: 'a', timestampMs: 1, vusers: 1, tps: 1, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
      { time: 'b', timestampMs: 2, vusers: 1, tps: 1, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
    ]
    expect(markDataGaps(points)).toBe(points)
  })

  it('merges server history into the live series, filling missed windows in time order', () => {
    const live: MetricsHistoryPoint[] = [
      { time: 'a', timestampMs: 1000, vusers: 1, tps: 10, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
      { time: 'b', timestampMs: 2000, vusers: 1, tps: 11, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
    ]
    // Server history overlaps t=2000 (dup) and adds the missed t=3000/4000.
    const backfill: MetricsHistoryPoint[] = [
      { time: 'b-server', timestampMs: 2000, vusers: 9, tps: 999, avgMs: 9, errorPct: 9, p50: 9, p90: 9, p99: 9 },
      { time: 'c', timestampMs: 3000, vusers: 1, tps: 12, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
      { time: 'd', timestampMs: 4000, vusers: 1, tps: 13, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
    ]

    const merged = mergeMetricsHistory(live, backfill)

    expect(merged.map((p) => p.timestampMs)).toEqual([1000, 2000, 3000, 4000])
    // The existing live point wins on conflict (keeps client-side enrichment).
    expect(merged[1].tps).toBe(11)
    expect(merged[1].time).toBe('b')
  })

  it('returns the original series reference when the backfill adds nothing new', () => {
    const live: MetricsHistoryPoint[] = [
      { time: 'a', timestampMs: 1000, vusers: 1, tps: 10, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1 },
    ]
    expect(mergeMetricsHistory(live, [])).toBe(live)
    expect(mergeMetricsHistory(live, [{ ...live[0], tps: 999 }])).toBe(live)
  })
})

describe('recentMetricAverages', () => {
  const pt = (timestampMs: number, tps: number, avgMs: number): MetricsHistoryPoint => ({
    time: String(timestampMs), timestampMs, vusers: 0, tps, avgMs, errorPct: 0, p50: 0, p90: 0, p99: 0,
  })

  it('averages TPS over the trailing 10s window, anchored on the latest sample', () => {
    const history = [pt(0, 1, 100), pt(5_000, 2, 100), pt(10_000, 3, 100), pt(15_000, 4, 100), pt(20_000, 5, 100)]
    // Anchor 20_000 → cutoff 10_000 → window is the t=10/15/20s points: tps 3,4,5.
    const { tps } = recentMetricAverages(history)
    expect(tps).toBe(4)
  })

  it('weights ART by throughput so busy seconds count more', () => {
    const history = [pt(0, 1, 100), pt(1_000, 9, 200)]
    const { art, tps } = recentMetricAverages(history)
    // (100*1 + 200*9) / (1 + 9) = 190, not the unweighted mean of 150.
    expect(art).toBe(190)
    expect(tps).toBe(5)
  })

  it('falls back to a simple ART mean when the window has no throughput', () => {
    const history = [pt(0, 0, 50), pt(1_000, 0, 150)]
    const { art, tps } = recentMetricAverages(history)
    expect(art).toBe(100)
    expect(tps).toBe(0)
  })

  it('honours a custom window and excludes samples outside it', () => {
    const history = [pt(0, 100, 100), pt(9_000, 2, 200), pt(10_000, 4, 200)]
    // 2s window anchored at 10_000 → cutoff 8_000 → only the 9s and 10s points.
    const { tps, art } = recentMetricAverages(history, 2_000)
    expect(tps).toBe(3)
    expect(art).toBe(200)
  })

  // Every rate on a page covers one window, so the groups' rates add up to
  // the run's; a sample without a group's or transaction's traffic counts
  // as zero for it.
  it('averages request rates and named groups and transactions over the same samples', () => {
    const sample = (timestampMs: number, browse: number, inventory: number | undefined, checkoutTps: number | undefined): MetricsHistoryPoint => ({
      ...pt(timestampMs, browse, 100),
      requestRate: browse + (inventory ?? 0),
      Browse_tps: browse,
      Browse_requestRate: browse,
      ...(inventory === undefined ? {} : { Inventory_tps: 0, Inventory_requestRate: inventory }),
      ...(checkoutTps === undefined ? {} : { [transactionChartSeriesKey('Checkout', 'tps')]: checkoutTps }),
    })
    const history = [sample(0, 100, 100, 50), sample(8_000, 4, 30, 2), sample(9_000, 6, undefined, undefined), sample(10_000, 5, 33, 4)]
    const averages = recentMetricAverages(history, undefined, { groups: ['Browse', 'Inventory'], transactions: ['Checkout'] })
    // Anchor 10_000 → cutoff 0 → all four samples.
    expect(averages.requestRate).toBe((200 + 34 + 6 + 38) / 4)
    expect(averages.groups).toEqual({ Browse: { tps: 115 / 4, requestRate: 115 / 4 }, Inventory: { tps: 0, requestRate: 163 / 4 } })
    expect(averages.transactions).toEqual({ Checkout: 56 / 4 })
    expect(averages.groups!.Browse.requestRate + averages.groups!.Inventory.requestRate).toBeCloseTo(averages.requestRate!, 9)
    expect(recentMetricAverages(history, 1_500).groups).toBeUndefined()
    // A name no sample carries (history recorded without group detail) has no
    // average, so the page shows its latest value rather than 0.
    expect(recentMetricAverages(history, undefined, { groups: ['Checkout'], transactions: ['Login'] })).toMatchObject({ groups: {}, transactions: {} })
  })

  // Each group's rate of a transaction is a series of its own: its share, or
  // the whole when one group ran it, averaged like the transaction's.
  it('keeps and averages each group\'s rate of a transaction', () => {
    const live = (timestamp: string, tps: number, transactions: LiveMetrics['transactions']) => mapExecutionMetricToHistoryPoint({
      timestamp, active_vusers: 2, tps, avg_response_ms: 10, error_pct: 0, total_transactions: 1, total_errors: 0,
      p50_ms: 1, p90_ms: 1, p95_ms: 1, p99_ms: 1, transactions,
    } as LiveMetrics)
    const figures = { count: 1, errors: 0, avg_ms: 1, p50_ms: 1, p90_ms: 1, p99_ms: 1 }
    const history = [
      live('2026-10-13T10:00:00Z', 4, { Login: { name: 'Login', tps: 4, ...figures, groups: ['buyers'] } }),
      live('2026-10-13T10:00:01Z', 6, { Login: { name: 'Login', tps: 6, ...figures, groups: ['buyers', 'sellers'], by_group: { buyers: { tps: 2, ...figures }, sellers: { tps: 4, ...figures } } } }),
    ]
    expect(history[1][groupTransactionTpsSeriesKey('sellers', 'Login')]).toBe(4)
    // The group series are not transaction chart series.
    expect(transactionChartSeriesNames(history)).toEqual(['Login'])
    const averages = recentMetricAverages(history, undefined, { groupTransactions: [['buyers', 'Login'], ['sellers', 'Login'], ['browsers', 'Login']] })
    expect(averages.groupTransactions).toEqual({ [groupTransactionKey('buyers', 'Login')]: 3, [groupTransactionKey('sellers', 'Login')]: 2 })
  })

  it('reads request rates from live samples into history points', () => {
    const point = mapExecutionMetricToHistoryPoint({
      timestamp: '2026-10-11T10:00:00Z', active_vusers: 8, tps: 0, avg_response_ms: 10, error_pct: 100, total_transactions: 0, total_errors: 3,
      p50_ms: 1, p90_ms: 1, p95_ms: 1, p99_ms: 1, request_rate: 34.66,
      groups: { Inventory: { name: 'Inventory', vusers: 4, transactions: 0, errors: 3, tps: 0, request_rate: 25.44, avg_response_ms: 10, p50_ms: 1, p90_ms: 1, p99_ms: 1 } },
    } as LiveMetrics)
    expect(point.requestRate).toBe(34.7)
    expect(point.Inventory_requestRate).toBe(25.4)
    // An older controller sends no request rate: a window with none.
    expect(mapExecutionMetricToHistoryPoint({ timestamp: '2026-10-11T10:00:01Z', tps: 3 } as LiveMetrics).requestRate).toBe(0)
  })

  it('ignores value-less gap-spacer rows and returns empty for empty history', () => {
    const spacer = { time: 'gap', timestampMs: 15_000 } as unknown as MetricsHistoryPoint
    const history = [pt(0, 2, 100), pt(10_000, 4, 100), spacer]
    // Anchor skips the spacer (no tps) → anchors at 10_000, cutoff 0 → both real
    // points included; the spacer contributes nothing.
    const { tps, art } = recentMetricAverages(history)
    expect(tps).toBe(3)
    expect(art).toBe(100)
    expect(recentMetricAverages([])).toEqual({})
  })

  it('excludes gap-boundary spacer rows from chart-summary averages (no NaN)', () => {
    // Two clusters separated by a large time gap so markDataGaps inserts a
    // value-less spacer row — the scenario that produced literal "NaN" metrics.
    // (>= GAP_DETECTION_MIN_POINTS real points are required to trigger detection.)
    const points = [
      pt(0, 10, 100), pt(1_000, 10, 100), pt(2_000, 10, 100), pt(3_000, 10, 100),
      pt(120_000, 10, 100), pt(121_000, 10, 100), pt(122_000, 10, 100),
    ]
    const withGaps = markDataGaps(points)
    expect(withGaps.length).toBeGreaterThan(points.length)
    expect(withGaps.some(point => point.isGapBoundary)).toBe(true)

    const summary = summarizeExecutionChartData(withGaps)
    expect(summary).not.toBeNull()
    // Every real point is 10/100/0, so the averages must equal them exactly —
    // only true if the undefined-valued spacer is excluded from sum AND divisor.
    expect(Number.isNaN(summary!.avgTps)).toBe(false)
    expect(summary!.avgTps).toBe(10)
    expect(summary!.avgLatency).toBe(100)
    expect(summary!.avgError).toBe(0)
  })

  it('excludes unmatched resource-only archive points from chart-summary averages', () => {
    const performancePoint = pt(1_000, 12, 150)
    const resourceOnlyPoint: MetricsHistoryPoint = {
      time: 'resource',
      timestampMs: 1_500,
      vusers: 40,
      tps: Number.NaN,
      avgMs: Number.NaN,
      errorPct: Number.NaN,
      p50: Number.NaN,
      p90: Number.NaN,
      p95: Number.NaN,
      p99: Number.NaN,
      resourceSampleOnly: 1,
      cpuProcess: 72,
    }

    expect(summarizeExecutionChartData([performancePoint, resourceOnlyPoint])).toEqual({
      avgTps: 12,
      avgLatency: 150,
      avgError: 0,
      maxVusers: 0,
    })
    expect(summarizeExecutionChartData([resourceOnlyPoint])).toBeNull()
  })
})

it('keeps measured zero JMS age with its own denominator', () => {
 const point = mapExecutionMetricToHistoryPoint({timestamp:'2026-10-01T00:00:00Z',transactions:{receive:{dependency_type:'jms_queue',server_telemetry:{samples:100,consumer_lag_samples:2,avg_consumer_lag_ms:0,max_consumer_lag_ms:0}}}} as unknown as LiveMetrics)
 expect(point.jmsConsumerLagMs).toBe(0)
 expect(point.jmsPeakConsumerLagMs).toBe(0)
})

describe('recentGroupJourneyRates', () => {
  const point = (timestampMs: number, values: Record<string, number>): MetricsHistoryPoint => ({ time: '', timestampMs, vusers: 1, tps: 1, avgMs: 1, errorPct: 0, p50: 1, p90: 1, p99: 1, ...values })

  it('counts each group\'s completed iterations per minute over the last minute', () => {
    const rates = recentGroupJourneyRates([
      point(0, { Shop_iterations: 1 }),
      point(30_000, { Shop_iterations: 4, Search_iterations: 10 }),
      point(60_000, { Shop_iterations: 9 }),
      point(90_000, { Shop_iterations: 14, Search_iterations: 10 }),
    ], ['Shop', 'Search', 'Absent'])
    // The window starts at 30 s: (14 - 4) over 60 s, and no Search journeys.
    expect(rates).toEqual({ Shop: 10, Search: 0 })
  })

  it('records the running total from a live sample', () => {
    const live = { timestamp: new Date(0).toISOString(), active_vusers: 1, tps: 1, avg_response_ms: 1, error_pct: 0, total_transactions: 1, total_errors: 0, p50_ms: 1, p90_ms: 1, p95_ms: 1, p99_ms: 1,
      groups: { Shop: { name: 'Shop', vusers: 1, transactions: 1, errors: 0, tps: 1, avg_response_ms: 1, p50_ms: 1, p90_ms: 1, p99_ms: 1, completed_iterations: 42 } } } as LiveMetrics
    expect(mapExecutionMetricToHistoryPoint(live).Shop_iterations).toBe(42)
  })

  it('starts the count again after a total falls, and leaves out too short a span', () => {
    expect(recentGroupJourneyRates([
      point(0, { Shop_iterations: 50 }),
      point(10_000, { Shop_iterations: 2 }),
      point(40_000, { Shop_iterations: 8 }),
    ], ['Shop'])).toEqual({ Shop: 12 })
    expect(recentGroupJourneyRates([point(0, { Shop_iterations: 1 }), point(4_000, { Shop_iterations: 3 })], ['Shop'])).toEqual({})
    expect(recentGroupJourneyRates([], ['Shop'])).toEqual({})
  })
})
