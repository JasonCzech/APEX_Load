import { describe, expect, it } from 'vitest'

import {
  chartMetricKeys,
  chartMetricRows,
  chartMetricSummary,
  chartStateForMonitorPreset,
  formatGraphOverlayMetricValue,
  formatRelativeRunTime,
  graphCsvForChart,
  latestRouteLatencyValues,
  responseTimeDataWithoutNetworkLatency,
  routeLatencySeriesKeys,
  visibleMetricDomain,
  type ChartConfig,
} from './testExecutionChartModel'
import { routeLatencyDetailSeriesKey, routeLatencySeriesLabel } from './latencySeries'

describe('chartStateForMonitorPreset', () => {
  it('applies role-specific chart order and hides non-preset charts', () => {
    const state = chartStateForMonitorPreset('release_approver', ['vusers', 'tps', 'latency', 'errors', 'cpu'])

    expect(state.chartOrder).toEqual(['latency', 'errors', 'tps', 'vusers', 'cpu'])
    expect(state.hiddenChartIds).toEqual(['vusers', 'cpu'])
  })

  it('falls back to all known charts for an unknown preset', () => {
    const state = chartStateForMonitorPreset('unknown', ['vusers', 'tps'])

    expect(state.chartOrder).toEqual(['vusers', 'tps'])
    expect(state.hiddenChartIds).toEqual([])
  })

  it('hides load-under charts for role presets that do not request them', () => {
    const state = chartStateForMonitorPreset('release_approver', ['vusers', 'tps', 'latency', 'errors', 'load_impact_avgMs'])

    expect(state.chartOrder).toEqual(['latency', 'errors', 'tps', 'vusers', 'load_impact_avgMs'])
    expect(state.hiddenChartIds).toEqual(['vusers', 'load_impact_avgMs'])
  })

  it('keeps route latency series visible when the latest point has no latency sample', () => {
    const endpointKey = 'latency_https%3A%2F%2Fexample.test'
    const dnsKey = routeLatencyDetailSeriesKey(endpointKey, 'dns')
    const history = [
      { time: '10:00:00', vusers: 1, tps: 1, avgMs: 90, errorPct: 0, p50: 80, p90: 100, p99: 110, [endpointKey]: 42, [dnsKey]: 2 },
      { time: '10:00:01', vusers: 1, tps: 1, avgMs: 91, errorPct: 0, p50: 81, p90: 101, p99: 111 },
    ]

    const keys = routeLatencySeriesKeys(history)

    expect(keys).toEqual([endpointKey])
    expect(latestRouteLatencyValues(history, keys)).toEqual({ [endpointKey]: 42 })
    expect(routeLatencySeriesLabel(endpointKey)).toBe('https://example.test')
    expect(routeLatencySeriesLabel(dnsKey)).toBe('DNS · https://example.test')
  })

  it('exports graph CSV with chart-specific metric columns', () => {
    const chart: ChartConfig = { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' }
    const csv = graphCsvForChart(
      chart,
      [
        {
          time: '10:00:00',
          vusers: 2,
          tps: 1,
          avgMs: 100,
          errorPct: 0,
          p50: 80,
          p90: 100,
          p95: 115,
          p99: 130,
          Checkout_avgMs: 92,
        },
      ],
      ['Checkout'],
      [],
    )

    expect(chartMetricKeys(chart, ['Checkout'], [])).toEqual(['avgMs', 'Checkout_avgMs', 'p50', 'p90', 'p95', 'p99'])
    expect(csv).toBe('time,avgMs,Checkout_avgMs,p50,p90,p95,p99\n10:00:00,100,92,80,100,115,130')
  })

  it('can include active VUsers when exporting a graph with the overlay enabled', () => {
    const chart: ChartConfig = { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' }
    const csv = graphCsvForChart(
      chart,
      [
        { time: '10:00:00', vusers: 12, tps: 4, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
      ],
      [],
      [],
      true,
    )

    expect(chartMetricKeys(chart, [], [], true)).toEqual(['tps', 'vusers'])
    expect(csv).toBe('time,tps,vusers\n10:00:00,4,12')
  })

  it('builds metric table rows for grouped charts and overlays', () => {
    const chart: ChartConfig = { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' }

    expect(chartMetricRows(chart, ['Checkout', 'Search'], [], ['#00a3ff', '#ffaa00'], true)).toEqual([
      { key: 'tps', label: 'Total', color: 'var(--text-muted)' },
      { key: 'Checkout_tps', label: 'Checkout', color: '#00a3ff' },
      { key: 'Search_tps', label: 'Search', color: '#ffaa00' },
      { key: 'vusers', label: 'Active VUsers', color: 'var(--chart-1)' },
    ])
  })

  it('builds response time rows with total and per-group series first', () => {
    const chart: ChartConfig = { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' }

    expect(chartMetricRows(chart, ['Checkout', 'Search'], [], ['#00a3ff', '#ffaa00'])).toEqual([
      { key: 'avgMs', label: 'Total', color: 'var(--text-muted)', unit: 'ms' },
      { key: 'Checkout_avgMs', label: 'Checkout', color: '#00a3ff', unit: 'ms' },
      { key: 'Search_avgMs', label: 'Search', color: '#ffaa00', unit: 'ms' },
      { key: 'p50', label: 'P50', color: 'var(--chart-5)', unit: 'ms' },
      { key: 'p90', label: 'P90', color: 'var(--chart-3)', unit: 'ms' },
      { key: 'p95', label: 'P95', color: 'var(--chart-2)', unit: 'ms' },
      { key: 'p99', label: 'P99', color: 'var(--chart-4)', unit: 'ms' },
    ])
  })

  it('builds route latency rows for total, phase, and stability modes', () => {
    const chart: ChartConfig = { id: 'network_latency', title: 'Route Latency (ms)', type: 'line-network' }
    const endpointKey = 'latency_https%3A%2F%2Fexample.test'

    expect(chartMetricRows(chart, [], [endpointKey], ['#00a3ff'])).toEqual([
      { key: endpointKey, label: 'https://example.test', color: '#00a3ff', unit: 'ms' },
    ])
    expect(chartMetricKeys(chart, [], [endpointKey], false, [], 'phases', endpointKey)).toEqual([
      endpointKey,
      routeLatencyDetailSeriesKey(endpointKey, 'dns'),
      routeLatencyDetailSeriesKey(endpointKey, 'connect'),
      routeLatencyDetailSeriesKey(endpointKey, 'tls'),
      routeLatencyDetailSeriesKey(endpointKey, 'ttfb'),
    ])
    expect(chartMetricRows(chart, [], [endpointKey], ['#00a3ff'], false, [], 'stability', endpointKey)).toEqual([
      { key: routeLatencyDetailSeriesKey(endpointKey, 'jitter'), label: 'Jitter', color: 'var(--chart-2)', unit: 'ms' },
      { key: routeLatencyDetailSeriesKey(endpointKey, 'successRate'), label: 'Success Rate', color: 'var(--chart-5)', unit: '%' },
    ])
  })

  it('builds generator impact diagnostic rows', () => {
    const chart: ChartConfig = { id: 'generator_impact', title: 'Generator Impact', type: 'line-impact' }

    expect(chartMetricKeys(chart, [], [])).toEqual([
      'containerCPUUsagePct',
      'containerCPUThrottledSecondsRate',
      'containerMemoryUsagePct',
      'gcPauseMs',
      'netDropsPerSec',
      'netErrorsPerSec',
      'tcpRetransmitsPerSec',
      'diskIOTimePct',
    ])
    expect(chartMetricRows(chart, [], [], ['#00a3ff']).map(row => row.label)).toEqual([
      'Container CPU',
      'CPU Throttle',
      'Memory Limit',
      'GC Pause',
      'Net Drops',
      'Net Errors',
      'TCP Retransmits',
      'Disk Busy',
    ])
  })

  it('builds load-under graph measurement rows and CSV with load buckets', () => {
    const chart: ChartConfig = {
      id: 'load_impact_avgMs',
      title: 'ART Under Load',
      type: 'line-load-impact',
      color: 'var(--chart-1)',
      defaultMetricKeys: ['average'],
      loadImpactMetric: { id: 'avgMs', key: 'avgMs', label: 'ART', unit: 'ms', category: 'performance' },
    }
    const data = [
      {
        time: '5 VUsers',
        vusers: 5,
        tps: 3,
        avgMs: 120,
        errorPct: 0,
        p50: 0,
        p90: 0,
        p99: 0,
        average: 120,
        min: 100,
        max: 140,
      },
    ]

    expect(chartMetricKeys(chart, [], [])).toEqual(['average', 'min', 'max'])
    expect(chartMetricRows(chart, [], [], ['#00a3ff'])).toEqual([
      { key: 'average', label: 'Average', color: 'var(--chart-1)', unit: 'ms' },
      { key: 'min', label: 'Min', color: 'var(--chart-3)', unit: 'ms' },
      { key: 'max', label: 'Max', color: 'var(--chart-4)', unit: 'ms' },
    ])
    expect(graphCsvForChart(chart, data, [], [])).toBe('load,average,min,max\n5 VUsers,120,100,140')
  })

  it('can subtract measured route latency from response time chart data', () => {
    const edgeKey = 'latency_edge'
    const serviceKey = 'latency_service'
    const data = [
      {
        time: '10:00:00',
        vusers: 1,
        tps: 1,
        avgMs: 100,
        errorPct: 0,
        p50: 80,
        p90: 120,
        p95: 140,
        p99: 160,
        Checkout_avgMs: 90,
        [edgeKey]: 10,
        [serviceKey]: 30,
      },
      {
        time: '10:00:01',
        vusers: 1,
        tps: 1,
        avgMs: 8,
        errorPct: 0,
        p50: 7,
        p90: 9,
        p95: 10,
        p99: 11,
        Checkout_avgMs: 12,
        [edgeKey]: 20,
      },
      {
        time: '10:00:02',
        vusers: 1,
        tps: 1,
        avgMs: 105,
        errorPct: 0,
        p50: 85,
        p90: 125,
        p95: 145,
        p99: 165,
        Checkout_avgMs: 95,
      },
    ]

    const adjusted = responseTimeDataWithoutNetworkLatency(data, [edgeKey, serviceKey], ['Checkout'])

    expect(adjusted[0]).toMatchObject({ avgMs: 80, Checkout_avgMs: 70, p50: 60, p90: 100, p95: 120, p99: 140 })
    expect(adjusted[1]).toMatchObject({ avgMs: 0, Checkout_avgMs: 0, p50: 0, p90: 0, p95: 0, p99: 0 })
    expect(adjusted[2]).toBe(data[2])
  })

  it('summarizes metric rows from finite samples only', () => {
    expect(chartMetricSummary([
      { time: '10:00:00', vusers: 1, tps: 2, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120 },
      { time: '10:00:01', vusers: 2, tps: 4, avgMs: 120, errorPct: 0, p50: 100, p90: 120, p99: 140 },
      { time: '10:00:02', vusers: 3, tps: Number.NaN, avgMs: 130, errorPct: 0, p50: 110, p90: 130, p99: 150 },
    ], 'tps')).toEqual({
      latest: 4,
      average: 3,
      min: 2,
      max: 4,
      samples: 2,
    })
  })

  it('builds chart domains from only the currently visible metric keys', () => {
    const data = [
      { time: '10:00:00', vusers: 1, tps: 2, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 2200 },
      { time: '10:00:01', vusers: 2, tps: 3, avgMs: 110, errorPct: 0, p50: 85, p90: 112, p99: 2400 },
    ]

    expect(visibleMetricDomain(data, ['avgMs', 'p90', 'p99'])).toEqual([0, 2592])
    expect(visibleMetricDomain(data, ['avgMs', 'p90'])).toEqual([0, 120.96])
    expect(visibleMetricDomain(data, ['missing'], [0, 'auto'])).toEqual([0, 'auto'])
  })

  it('formats graph overlay metric values with at most two decimals', () => {
    expect(formatGraphOverlayMetricValue(123.4567)).toBe('123.46')
    expect(formatGraphOverlayMetricValue(9.999)).toBe('10')
    expect(formatGraphOverlayMetricValue(2)).toBe('2')
    expect(formatGraphOverlayMetricValue(Number.NaN)).toBe('n/a')
    expect(formatGraphOverlayMetricValue('n/a')).toBe('n/a')
  })

  it('formats relative run time labels for graph axes', () => {
    expect(formatRelativeRunTime(0)).toBe('0s')
    expect(formatRelativeRunTime(65_000)).toBe('1m5s')
    expect(formatRelativeRunTime(3_723_000)).toBe('1h2m3s')
  })
})
