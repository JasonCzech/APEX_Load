import { describe, expect, it } from 'vitest'

import type { MetricsHistoryPoint } from '../types'
import {
  ART_STD_DEV_BAND_KEY,
  ART_STD_DEV_VALUE_KEY,
  buildArtStdDevBandData,
  buildSmoothedGraphData,
  chartMetricLinePresentation,
  chartMetricKeys,
  chartMetricRows,
  chartMetricSummary,
  chartStateForMonitorPreset,
  defaultHiddenChartIds,
  downsampleGraphData,
  downsampleTargetPointsForGraphDensity,
  formatGraphOverlayMetricValue,
  formatRelativeRunTime,
  graphCsvForChart,
  hasLoadImpactPercentileRows,
  latestRouteLatencyValues,
  loadImpactHeatmapChart,
  loadImpactPercentileChartData,
  loadImpactPercentilesChart,
  rawGraphMetricKey,
  responseTimeDataWithoutNetworkLatency,
  rollingArtStdDevWindowForRadius,
  routeLatencySeriesKeys,
  shouldRenderArtStdDevBands,
  smoothingRadiusForGraphDensity,
  smoothedGraphMetricKey,
  smoothedMetricKeysForChart,
  visibleMetricDomain,
  type ChartConfig,
} from './testExecutionChartModel'
import { routeLatencyDetailSeriesKey, routeLatencySeriesLabel } from './latencySeries'
import { buildLoadImpactModel } from './loadImpactModel'

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
      { key: 'avgMs', label: 'Total', color: 'var(--text-muted)', unit: 'ms', kind: 'transaction' },
      { key: 'Checkout_avgMs', label: 'Checkout', color: '#00a3ff', unit: 'ms', kind: 'transaction' },
      { key: 'Search_avgMs', label: 'Search', color: '#ffaa00', unit: 'ms', kind: 'transaction' },
      { key: 'p50', label: 'P50', color: 'var(--chart-5)', unit: 'ms', kind: 'percentile' },
      { key: 'p90', label: 'P90', color: 'var(--chart-3)', unit: 'ms', kind: 'percentile' },
      { key: 'p95', label: 'P95', color: 'var(--chart-2)', unit: 'ms', kind: 'percentile' },
      { key: 'p99', label: 'P99', color: 'var(--chart-4)', unit: 'ms', kind: 'percentile' },
    ])
  })

  it('classifies ART transaction and percentile line styles separately', () => {
    const chart: ChartConfig = { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' }
    const rows = chartMetricRows(chart, ['Checkout'], [], ['#00a3ff'])
    const total = rows.find(row => row.key === 'avgMs')!
    const checkout = rows.find(row => row.key === 'Checkout_avgMs')!
    const p95 = rows.find(row => row.key === 'p95')!

    expect(chartMetricLinePresentation(total)).toEqual({ role: 'transaction', strokeWidth: 2.5, strokeOpacity: 1 })
    expect(chartMetricLinePresentation(checkout)).toEqual({ role: 'transaction', strokeWidth: 2.2, strokeOpacity: 1 })
    expect(chartMetricLinePresentation(p95)).toEqual({ role: 'percentile', strokeWidth: 1.25, strokeDasharray: '4 2', strokeOpacity: 0.68 })
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
    const data: MetricsHistoryPoint[] = [
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

  it('keeps the load impact heatmap chart visible by default', () => {
    expect(loadImpactHeatmapChart).toMatchObject({
      id: 'load_impact_heatmap',
      title: 'Load Impact Heatmap',
      type: 'load-impact-heatmap',
    })
    expect(chartMetricRows(loadImpactHeatmapChart, [], [], [])).toEqual([])
    expect(chartMetricKeys(loadImpactHeatmapChart, [], [])).toEqual([])
    expect(defaultHiddenChartIds([loadImpactHeatmapChart])).toEqual([])
  })

  it('builds a combined load-under P-values graph that is hidden until selected', () => {
    const source: MetricsHistoryPoint[] = [
      { time: '10:00:00', vusers: 5, tps: 3, avgMs: 120, errorPct: 0, p50: 92, p90: 118, p95: 130, p99: 160 },
      { time: '10:01:00', vusers: 15, tps: 5, avgMs: 150, errorPct: 0, p50: 122, p90: 168, p95: 190, p99: 230 },
    ]
    const model = buildLoadImpactModel(source)
    const chartData = loadImpactPercentileChartData(model.rows)

    expect(loadImpactPercentilesChart).toMatchObject({
      id: 'load_impact_percentiles',
      title: 'P Values Under Load',
      type: 'line-load-impact-percentiles',
      defaultMetricKeys: ['p50', 'p90', 'p95', 'p99'],
    })
    expect(hasLoadImpactPercentileRows(model.rows)).toBe(true)
    expect(chartMetricKeys(loadImpactPercentilesChart, [], [])).toEqual(['p50', 'p90', 'p95', 'p99'])
    expect(chartMetricRows(loadImpactPercentilesChart, [], [], []).map(row => [row.label, row.kind])).toEqual([
      ['P50', 'percentile'],
      ['P90', 'percentile'],
      ['P95', 'percentile'],
      ['P99', 'percentile'],
    ])
    expect(defaultHiddenChartIds([loadImpactPercentilesChart])).toEqual(['load_impact_percentiles'])
    expect(defaultHiddenChartIds([loadImpactPercentilesChart], ['load_impact_percentiles'])).toEqual([])
    expect(chartData[0]).toMatchObject({ time: '5 VUsers', p50: 92, p90: 118, p95: 130, p99: 160 })
    expect(graphCsvForChart(loadImpactPercentilesChart, chartData, [], [])).toBe(
      'load,p50,p90,p95,p99\n5 VUsers,92,118,130,160\n15 VUsers,122,168,190,230',
    )
  })

  it('can subtract measured route latency from response time chart data', () => {
    const edgeKey = 'latency_edge'
    const serviceKey = 'latency_service'
    const data: MetricsHistoryPoint[] = [
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
    const data: MetricsHistoryPoint[] = [
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

  it('builds display-only smoothed metric keys without changing raw graph data', () => {
    const data = [
      { time: '10:00:00', vusers: 1, tps: 10, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120, Checkout_tps: 8 },
      { time: '10:00:01', vusers: 1, tps: 30, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120, Checkout_tps: 24 },
      { time: '10:00:02', vusers: 1, tps: 10, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120, Checkout_tps: 8 },
      { time: '10:00:03', vusers: 1, tps: 30, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120, Checkout_tps: 24 },
      { time: '10:00:04', vusers: 1, tps: 10, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120, Checkout_tps: 8 },
    ]

    const smoothed = buildSmoothedGraphData(data, ['tps', 'Checkout_tps'])

    expect(smoothed).not.toBe(data)
    expect(smoothed.map(point => point.tps)).toEqual([10, 30, 10, 30, 10])
    expect(smoothed.map(point => point[smoothedGraphMetricKey('tps')])).toEqual([10, 20, 18.9, 20, 10])
    expect(smoothed.map(point => point[smoothedGraphMetricKey('Checkout_tps')])).toEqual([8, 16, 15.1, 16, 8])
    expect(rawGraphMetricKey(smoothedGraphMetricKey('tps'))).toBe('tps')
  })

  it('builds rolling ART standard-deviation bands without mutating raw points', () => {
    const data: MetricsHistoryPoint[] = [
      { time: '10:00:00', vusers: 1, tps: 1, avgMs: 10, errorPct: 0, p50: 8, p90: 10, p99: 12 },
      { time: '10:00:01', vusers: 1, tps: 1, avgMs: 14, errorPct: 0, p50: 9, p90: 14, p99: 18 },
      { time: '10:00:02', vusers: 1, tps: 1, avgMs: 18, errorPct: 0, p50: 12, p90: 18, p99: 22 },
      { time: '10:00:03', vusers: 1, tps: 1, avgMs: 22, errorPct: 0, p50: 16, p90: 22, p99: 28 },
      { time: '10:00:04', vusers: 1, tps: 1, avgMs: 200, errorPct: 0, p50: 25, p90: 200, p99: 240 },
      { time: '10:00:05', vusers: 1, tps: 1, avgMs: 26, errorPct: 0, p50: 20, p90: 26, p99: 32 },
      { time: '10:00:06', vusers: 1, tps: 1, avgMs: 30, errorPct: 0, p50: 24, p90: 30, p99: 36 },
    ]

    const banded = buildArtStdDevBandData(data, 2)

    expect(banded).not.toBe(data)
    expect(data[0][ART_STD_DEV_BAND_KEY]).toBeUndefined()
    expect(banded[0][ART_STD_DEV_BAND_KEY]).toEqual([10.7, 17.3])
    expect(banded[3][ART_STD_DEV_BAND_KEY]).toEqual([0, 128.1])
    expect(banded[3][ART_STD_DEV_VALUE_KEY]).toBe(72.1)
    expect(rollingArtStdDevWindowForRadius(0)).toBe(5)
    expect(rollingArtStdDevWindowForRadius(20)).toBe(13)
  })

  it('handles sparse ART standard-deviation samples and hidden Total ART', () => {
    const chart: ChartConfig = { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' }
    const data: MetricsHistoryPoint[] = [
      { time: '10:00:00', vusers: 1, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 100, p99: 120 },
      { time: '10:00:01', vusers: 1, tps: 1, avgMs: Number.NaN, errorPct: 0, p50: 90, p90: 100, p99: 120 },
      { time: '10:00:02', vusers: 1, tps: 1, avgMs: 140, errorPct: 0, p50: 110, p90: 140, p99: 180 },
    ]

    const banded = buildArtStdDevBandData(data, 2)

    expect(banded[0][ART_STD_DEV_BAND_KEY]).toEqual([100, 140])
    expect(banded[1][ART_STD_DEV_BAND_KEY]).toBeUndefined()
    expect(banded[2][ART_STD_DEV_BAND_KEY]).toEqual([100, 140])
    expect(shouldRenderArtStdDevBands(chart, ['p50', 'p90', 'p95', 'p99'], true)).toBe(false)
    expect(shouldRenderArtStdDevBands(chart, ['avgMs', 'p95'], true)).toBe(true)
    expect(shouldRenderArtStdDevBands(chart, ['avgMs'], false)).toBe(false)
  })

  it('increases display smoothing for dense graph content', () => {
    const sparseRadius = smoothingRadiusForGraphDensity(20, {
      chartWidth: 900,
      chartHeight: 320,
      visibleMetricCount: 2,
      expanded: true,
    })
    const denseRadius = smoothingRadiusForGraphDensity(240, {
      chartWidth: 360,
      chartHeight: 150,
      visibleMetricCount: 6,
    })

    expect(sparseRadius).toBe(2)
    expect(denseRadius).toBeGreaterThan(sparseRadius)
  })

  it('downsamples dense graph data while preserving boundaries and composite extremes', () => {
    const data: MetricsHistoryPoint[] = Array.from({ length: 1000 }, (_, index) => ({
      time: `10:${String(Math.floor(index / 60)).padStart(2, '0')}:${String(index % 60).padStart(2, '0')}`,
      vusers: index % 50,
      tps: index === 500 ? 10_000 : index % 11,
      avgMs: index === 700 ? 9_000 : 100 + (index % 7),
      errorPct: 0,
      p50: 90,
      p90: 110,
      p99: 150,
    }))

    const downsampled = downsampleGraphData(data, ['tps', 'avgMs'], 80)

    expect(downsampled.length).toBeLessThanOrEqual(80)
    expect(downsampled[0]).toBe(data[0])
    expect(downsampled[downsampled.length - 1]).toBe(data[data.length - 1])
    expect(downsampled).toContain(data[500])
    expect(downsampled).toContain(data[700])
    expect(downsampleTargetPointsForGraphDensity()).toBe(420)
    expect(downsampleTargetPointsForGraphDensity({ chartWidth: 240, visibleMetricCount: 8 })).toBeGreaterThanOrEqual(180)
  })

  it('selects graph metrics that are safe for display smoothing', () => {
    const tpsChart: ChartConfig = { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' }
    const vusersChart: ChartConfig = { id: 'vusers', title: 'Virtual Users', type: 'area', dataKey: 'vusers' }
    const routeChart: ChartConfig = { id: 'network_latency', title: 'Route Latency (ms)', type: 'line-network' }
    const latencyChart: ChartConfig = { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' }
    const ioChart: ChartConfig = { id: 'io', title: 'Network & Disk I/O (KB/s)', type: 'line-io' }

    expect(smoothedMetricKeysForChart(tpsChart, ['tps', 'Checkout_tps', 'targetTps', 'avgMs'])).toEqual(['tps', 'Checkout_tps', 'avgMs'])
    expect(smoothedMetricKeysForChart(vusersChart, ['vusers', 'tps', 'avgMs'])).toEqual(['tps', 'avgMs'])
    expect(smoothedMetricKeysForChart(routeChart, ['latency_edge', 'latency_success_rate_edge', 'tps'])).toEqual(['latency_edge', 'tps'])
    expect(smoothedMetricKeysForChart(latencyChart, ['avgMs', 'Checkout_avgMs', 'p95', 'vusers'])).toEqual(['avgMs', 'Checkout_avgMs', 'p95'])
    expect(smoothedMetricKeysForChart(ioChart, ['netSent', 'diskRead', 'avgMs'])).toEqual(['netSent', 'diskRead', 'avgMs'])
  })
})
