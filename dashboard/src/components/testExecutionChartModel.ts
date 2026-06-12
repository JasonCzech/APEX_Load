import { type ComponentProps } from 'react'
import { LineChart, YAxis } from 'recharts'

import type { MetricsHistoryPoint, PrometheusGraph } from '../types'
import {
  isRouteLatencyTotalSeriesKey,
  routeLatencyDetailSeriesKey,
  routeLatencySeriesLabel,
} from './latencySeries'
import type { LoadImpactMetric, LoadImpactRow } from './loadImpactModel'

export type ChartDomain = ComponentProps<typeof YAxis>['domain']

export interface ChartConfig {
  id: string
  title: string
  type: 'area' | 'line-multi' | 'line-latency' | 'area-multi' | 'line-io' | 'line-network' | 'line-impact' | 'line-prometheus' | 'line-load-impact' | 'line-load-impact-percentiles' | 'load-impact-heatmap'
  dataKey?: string
  color?: string
  totalKey?: string
  keys?: Array<{ k: string; n: string; c: string }>
  domain?: ChartDomain
  defaultMetricKeys?: string[]
  loadImpactMetric?: LoadImpactMetric
  prometheusGraph?: PrometheusGraph
}

export type ChartMetricRowKind = 'transaction' | 'percentile' | 'other'

export interface ChartMetricRow {
  key: string
  label: string
  color: string
  unit?: string
  kind?: ChartMetricRowKind
}

export interface ChartMetricLinePresentation {
  role: 'transaction' | 'percentile' | 'default'
  strokeWidth: number
  strokeDasharray?: string
  strokeOpacity: number
}

export interface ChartMetricSummary {
  latest?: number
  average?: number
  min?: number
  max?: number
  samples: number
}

export type ChartTimeAxisMode = 'clock' | 'relative'
export type ChartDensityMode = 'standard' | 'six_up' | 'command_center'
export type ResponseTimeNetworkMode = 'end_to_end' | 'application'
export type OverlayMetricKey = 'vusers' | 'avgMs' | 'tps'
export type ChartMargin = ComponentProps<typeof LineChart>['margin']
export type RouteLatencyMode = 'total' | 'phases' | 'stability'

const LOAD_IMPACT_CHART_ID_PREFIX = 'load_impact_'
export const LOAD_IMPACT_HEATMAP_CHART_ID = 'load_impact_heatmap'
export const LOAD_IMPACT_PERCENTILES_CHART_ID = 'load_impact_percentiles'
export const LOAD_IMPACT_PERCENTILE_KEYS = ['p50', 'p90', 'p95', 'p99'] as const
export const ART_STD_DEV_BAND_KEY = 'avgMsStdDevBand'
export const ART_STD_DEV_VALUE_KEY = 'avgMsStdDev'
const SMOOTHED_GRAPH_METRIC_SUFFIX = '__smoothed'
export const LOAD_IMPACT_VISIBLE_CHARTS_STORAGE_KEY = 'apexload.execution.visibleLoadImpactCharts'
export const DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS = ['avgMs', 'p95', 'tps', 'errorPct', 'cpuProcess', 'memProcess']
const LOAD_IMPACT_GRAPH_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']
const PROMETHEUS_GRAPH_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)', 'var(--accent)', 'var(--warning)']
const MIN_GRAPH_SMOOTHING_RADIUS = 2
const MAX_GRAPH_SMOOTHING_RADIUS = 6
const DEFAULT_DOWNSAMPLED_GRAPH_POINTS = 420
const MIN_DOWNSAMPLED_GRAPH_POINTS = 180
const MAX_DOWNSAMPLED_GRAPH_POINTS = 900
const MIN_ART_STD_DEV_WINDOW = 5
const MAX_ART_STD_DEV_WINDOW = 13
const DEFAULT_GRAPH_CARD_WIDTH = 520
const DEFAULT_GRAPH_CARD_HEIGHT = 196
const UNSMOOTHED_GRAPH_KEYS = new Set(['targetTps', 'vusers'])

export interface GraphSmoothingDensity {
  chartWidth?: number
  chartHeight?: number
  visibleMetricCount?: number
  expanded?: boolean
}

export const chartConfigs: ChartConfig[] = [
  { id: 'vusers', title: 'Virtual Users', type: 'area', dataKey: 'vusers', color: 'var(--chart-1)' },
  { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' },
  { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' },
  { id: 'errors', title: 'Error Rate (%)', type: 'area', dataKey: 'errorPct', color: 'var(--chart-4)', domain: [0, 'auto'] },
  {
    id: 'cpu',
    title: 'CPU Usage (%)',
    type: 'area-multi',
    keys: [
      { k: 'cpuProcess', n: 'VUsers', c: 'var(--chart-1)' },
      { k: 'cpuFree', n: 'System', c: 'var(--chart-2)' },
    ],
    domain: [0, 100],
  },
  {
    id: 'mem',
    title: 'Memory Usage (MB)',
    type: 'area-multi',
    keys: [
      { k: 'memProcess', n: 'VUsers', c: 'var(--chart-4)' },
      { k: 'memFree', n: 'System', c: 'var(--chart-5)' },
    ],
  },
  { id: 'io', title: 'Network & Disk I/O (KB/s)', type: 'line-io' },
  { id: 'generator_impact', title: 'Generator Impact', type: 'line-impact' },
  { id: 'network_latency', title: 'Route Latency (ms)', type: 'line-network' },
]

export const loadImpactHeatmapChart: ChartConfig = {
  id: LOAD_IMPACT_HEATMAP_CHART_ID,
  title: 'Load Impact Heatmap',
  type: 'load-impact-heatmap',
}

export const loadImpactPercentilesChart: ChartConfig = {
  id: LOAD_IMPACT_PERCENTILES_CHART_ID,
  title: 'P Values Under Load',
  type: 'line-load-impact-percentiles',
  defaultMetricKeys: [...LOAD_IMPACT_PERCENTILE_KEYS],
}

export function loadImpactChartId(metricId: string) {
  return `${LOAD_IMPACT_CHART_ID_PREFIX}${metricId}`
}

export function isLoadImpactChart(chart: ChartConfig) {
  return chart.type === 'line-load-impact' && Boolean(chart.loadImpactMetric)
}

export function isLoadImpactHeatmapChart(chart: ChartConfig) {
  return chart.type === 'load-impact-heatmap' || chart.id === LOAD_IMPACT_HEATMAP_CHART_ID
}

export function isLoadImpactPercentilesChart(chart: ChartConfig) {
  return chart.type === 'line-load-impact-percentiles' || chart.id === LOAD_IMPACT_PERCENTILES_CHART_ID
}

export function isPrometheusChart(chart: ChartConfig) {
  return chart.type === 'line-prometheus' && Boolean(chart.prometheusGraph)
}

export function hasLoadImpactPercentileRows(rows: LoadImpactRow[]) {
  return LOAD_IMPACT_PERCENTILE_KEYS.some(key => rows.some(row => row.metric.id === key))
}

export function buildLoadImpactCharts(rows: LoadImpactRow[]): ChartConfig[] {
  return rows.map((row, index) => ({
    id: loadImpactChartId(row.metric.id),
    title: `${row.metric.label} Under Load`,
    type: 'line-load-impact',
    color: LOAD_IMPACT_GRAPH_COLORS[index % LOAD_IMPACT_GRAPH_COLORS.length],
    defaultMetricKeys: ['average'],
    loadImpactMetric: row.metric,
  }))
}

export function buildPrometheusCharts(graphs: PrometheusGraph[]): ChartConfig[] {
  return graphs.map(graph => ({
    id: graph.id,
    title: graph.title,
    type: 'line-prometheus',
    defaultMetricKeys: graph.series.map(series => series.key),
    prometheusGraph: graph,
  }))
}

function prometheusPointTimestampMs(timestamp: string | undefined, timestampMs: number | undefined) {
  if (typeof timestampMs === 'number' && Number.isFinite(timestampMs)) return timestampMs
  const parsed = timestamp ? Date.parse(timestamp) : Number.NaN
  return Number.isFinite(parsed) ? parsed : undefined
}

function formatPrometheusPointTime(timestampMs: number) {
  return new Date(timestampMs).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })
}

export function prometheusChartData(graph: PrometheusGraph | undefined): MetricsHistoryPoint[] {
  if (!graph) return []
  const rowsByTimestamp = new Map<number, MetricsHistoryPoint>()
  graph.series.forEach(series => {
    series.values.forEach(point => {
      const timestampMs = prometheusPointTimestampMs(point.timestamp, point.timestamp_ms)
      if (timestampMs === undefined || typeof point.value !== 'number' || !Number.isFinite(point.value)) return
      const existing = rowsByTimestamp.get(timestampMs)
      const row = existing ?? {
        time: formatPrometheusPointTime(timestampMs),
        timestampMs,
        vusers: 0,
        tps: 0,
        avgMs: 0,
        errorPct: 0,
        p50: 0,
        p90: 0,
        p99: 0,
      }
      row[series.key] = point.value
      rowsByTimestamp.set(timestampMs, row)
    })
  })
  return Array.from(rowsByTimestamp.entries())
    .sort(([left], [right]) => left - right)
    .map(([, row]) => row)
}

export const routeLatencyModes: Array<{ id: RouteLatencyMode; label: string }> = [
  { id: 'total', label: 'Total' },
  { id: 'phases', label: 'Phases' },
  { id: 'stability', label: 'Stability' },
]

const routeLatencyPhaseRows = [
  { metric: 'total', label: 'Total', color: 'var(--text-muted)' },
  { metric: 'dns', label: 'DNS', color: 'var(--chart-5)' },
  { metric: 'connect', label: 'TCP Connect', color: 'var(--chart-1)' },
  { metric: 'tls', label: 'TLS', color: 'var(--chart-3)' },
  { metric: 'ttfb', label: 'TTFB', color: 'var(--chart-4)' },
] as const

const routeLatencyStabilityRows = [
  { metric: 'jitter', label: 'Jitter', color: 'var(--chart-2)', unit: 'ms' },
  { metric: 'successRate', label: 'Success Rate', color: 'var(--chart-5)', unit: '%' },
] as const

const generatorImpactMetricRows: ChartMetricRow[] = [
  { key: 'containerCPUUsagePct', label: 'Container CPU', color: 'var(--chart-1)', unit: '%' },
  { key: 'containerCPUThrottledSecondsRate', label: 'CPU Throttle', color: 'var(--chart-4)', unit: 's/s' },
  { key: 'containerMemoryUsagePct', label: 'Memory Limit', color: 'var(--chart-5)', unit: '%' },
  { key: 'gcPauseMs', label: 'GC Pause', color: 'var(--chart-2)', unit: 'ms' },
  { key: 'netDropsPerSec', label: 'Net Drops', color: 'var(--chart-3)', unit: 'drops/s' },
  { key: 'netErrorsPerSec', label: 'Net Errors', color: 'var(--danger)', unit: 'errors/s' },
  { key: 'tcpRetransmitsPerSec', label: 'TCP Retransmits', color: 'var(--warning)', unit: 'retrans/s' },
  { key: 'diskIOTimePct', label: 'Disk Busy', color: 'var(--chart-6)', unit: '%' },
]

const loadImpactPercentileMetricRows: ChartMetricRow[] = [
  { key: 'p50', label: 'P50', color: 'var(--chart-5)', unit: 'ms', kind: 'percentile' },
  { key: 'p90', label: 'P90', color: 'var(--chart-3)', unit: 'ms', kind: 'percentile' },
  { key: 'p95', label: 'P95', color: 'var(--chart-2)', unit: 'ms', kind: 'percentile' },
  { key: 'p99', label: 'P99', color: 'var(--chart-4)', unit: 'ms', kind: 'percentile' },
]

export const overlayMetricRows: Record<OverlayMetricKey, ChartMetricRow> = {
  vusers: { key: 'vusers', label: 'Active VUsers', color: 'var(--chart-1)' },
  avgMs: { key: 'avgMs', label: 'ART', color: 'var(--chart-4)', unit: 'ms' },
  tps: { key: 'tps', label: 'TPS', color: 'var(--chart-5)' },
}

export const overlayMetricMenuLabels: Record<OverlayMetricKey, string> = {
  vusers: 'VUsers',
  avgMs: 'ART',
  tps: 'TPS',
}

export function isOverlayMetricAvailable(chart: ChartConfig, key: OverlayMetricKey) {
  if (isPrometheusChart(chart)) {
    return false
  }
  if (isLoadImpactPercentilesChart(chart)) {
    return key !== 'vusers'
  }
  if (isLoadImpactChart(chart)) {
    return key !== 'vusers' && chart.loadImpactMetric?.key !== key
  }
  if (key === 'vusers') return chart.id !== 'vusers'
  if (key === 'avgMs') return chart.id !== 'latency'
  if (key === 'tps') return chart.id !== 'tps'
  return false
}

function normalizeOverlayMetricKeys(chart: ChartConfig, includeVuserOverlay: boolean, overlayMetricKeys: OverlayMetricKey[] = []) {
  const keys = new Set<OverlayMetricKey>(overlayMetricKeys)
  if (includeVuserOverlay) {
    keys.add('vusers')
  }
  return Array.from(keys).filter(key => isOverlayMetricAvailable(chart, key))
}

export function chartPrimaryYAxisLabel(chart: ChartConfig) {
  switch (chart.id) {
    case 'vusers':
      return 'VUsers'
    case 'tps':
      return 'TPS'
    case 'latency':
      return 'Response time (ms)'
    case 'errors':
      return 'Error rate (%)'
    case 'cpu':
      return 'CPU (%)'
    case 'mem':
      return 'Memory (MB)'
    case 'io':
      return 'Throughput (KB/s)'
    case 'generator_impact':
      return 'Impact metrics'
    case 'network_latency':
      return 'Route latency (ms)'
    case LOAD_IMPACT_PERCENTILES_CHART_ID:
      return 'Response time (ms)'
    default:
      if (isPrometheusChart(chart)) {
        const unit = chart.prometheusGraph?.unit?.trim()
        return unit || chart.title
      }
      if (isLoadImpactChart(chart)) {
        const metric = chart.loadImpactMetric!
        return metric.unit ? `${metric.label} (${metric.unit})` : metric.label
      }
      return chart.title
  }
}

export function chartMarginForOverlayKeys(overlayKeys: OverlayMetricKey[], compact = false): ChartMargin {
  const rightAxisCount = Number(overlayKeys.includes('vusers'))
    + Number(overlayKeys.some(key => key === 'avgMs' || key === 'tps'))
  if (compact) {
    return {
      top: 12,
      right: rightAxisCount > 0 ? 4 : 8,
      bottom: 8,
      left: 4,
    }
  }
  return {
    top: 12,
    right: rightAxisCount > 1 ? 106 : rightAxisCount === 1 ? 70 : 12,
    bottom: 8,
    left: 8,
  }
}

export function rateOverlayAxisLabel(overlayKeys: OverlayMetricKey[]) {
  const labels = []
  if (overlayKeys.includes('avgMs')) labels.push('ART (ms)')
  if (overlayKeys.includes('tps')) labels.push('TPS')
  return labels.join(' / ')
}

export function chartTitleWithOverlays(chart: ChartConfig, overlayKeys: OverlayMetricKey[]) {
  const labels = overlayKeys.map(key => overlayMetricMenuLabels[key])
  return labels.length > 0 ? `${chart.title} + ${labels.join(' + ')}` : chart.title
}

export function normalizedChartOrder(order: string[], knownIds = chartConfigs.map(chart => chart.id)) {
  const retained = order.filter(id => knownIds.includes(id))
  const added = knownIds.filter(id => !retained.includes(id))
  return [...retained, ...added]
}

export function defaultHiddenChartIds(charts: ChartConfig[], visibleLoadImpactChartIds: string[] = []) {
  const explicitlyVisible = new Set(visibleLoadImpactChartIds)
  return charts
    .filter(chart => (
      (
        isLoadImpactPercentilesChart(chart)
        || (
          isLoadImpactChart(chart)
          && !DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS.includes(chart.loadImpactMetric!.id)
        )
      )
      && !explicitlyVisible.has(chart.id)
    ))
    .map(chart => chart.id)
}

export const monitorPresets = [
  { id: 'run_operator', label: 'Run Operator', chartIds: ['vusers', 'tps', 'latency', 'errors'] },
  { id: 'performance_engineer', label: 'Performance Engineer', chartIds: ['latency', 'tps', 'errors', 'cpu', 'mem', 'io', 'generator_impact', 'network_latency', 'vusers'] },
  { id: 'platform_admin', label: 'Platform Admin', chartIds: ['cpu', 'mem', 'io', 'generator_impact', 'network_latency', 'vusers', 'tps'] },
  { id: 'release_approver', label: 'Release Approver', chartIds: ['latency', 'errors', 'tps'] },
] as const

export function sameStringArray(left: string[], right: string[]) {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

export function sameStringSet(left: string[], right: string[]) {
  const rightSet = new Set(right)
  return left.length === rightSet.size && left.every(value => rightSet.has(value))
}

export function chartStateForMonitorPreset(presetId: string, knownChartIds = chartConfigs.map(chart => chart.id)) {
  const preset = monitorPresets.find(candidate => candidate.id === presetId)
  const selected = preset?.chartIds.filter(id => knownChartIds.includes(id)) ?? knownChartIds
  const remaining = knownChartIds.filter(id => !selected.includes(id))
  return {
    chartOrder: [...selected, ...remaining],
    hiddenChartIds: remaining,
  }
}

export function formatRelativeRunTime(relativeMs?: number) {
  const totalSeconds = Math.max(0, Math.floor((relativeMs ?? 0) / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60

  if (hours > 0) {
    return `${hours}h${minutes > 0 ? `${minutes}m` : ''}${seconds > 0 ? `${seconds}s` : ''}`
  }
  if (minutes > 0) {
    return `${minutes}m${seconds > 0 ? `${seconds}s` : ''}`
  }
  return `${seconds}s`
}

export function routeLatencySeriesKeys(data: MetricsHistoryPoint[]) {
  const keys = new Set<string>()
  data.forEach(point => {
    Object.keys(point)
      .filter(key => isRouteLatencyTotalSeriesKey(key) && typeof point[key] === 'number')
      .forEach(key => keys.add(key))
  })
  return Array.from(keys)
}

export function latestRouteLatencyValues(data: MetricsHistoryPoint[], keys = routeLatencySeriesKeys(data)) {
  const values: Record<string, number | undefined> = {}
  keys.forEach(key => {
    for (let index = data.length - 1; index >= 0; index -= 1) {
      const value = data[index][key]
      if (typeof value === 'number') {
        values[key] = value
        break
      }
    }
  })
  return values
}

function responseTimeMetricKeys(groupNames: string[]) {
  return [
    'avgMs',
    ...groupNames.map(name => `${name}_avgMs`),
    'p50',
    'p90',
    'p95',
    'p99',
  ]
}

function averageRouteLatencyForPoint(point: MetricsHistoryPoint, latencyKeys: string[]) {
  const values = latencyKeys
    .map(key => point[key])
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (values.length === 0) return undefined
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function roundChartValue(value: number) {
  return Math.round(value * 10) / 10
}

export function smoothedGraphMetricKey(key: string) {
  return `${key}${SMOOTHED_GRAPH_METRIC_SUFFIX}`
}

export function rawGraphMetricKey(key: string) {
  return key.endsWith(SMOOTHED_GRAPH_METRIC_SUFFIX)
    ? key.slice(0, -SMOOTHED_GRAPH_METRIC_SUFFIX.length)
    : key
}

function isSmoothableGraphMetricKey(key: string) {
  return !UNSMOOTHED_GRAPH_KEYS.has(key)
    && !key.startsWith('latency_success_rate_')
    && !key.endsWith('_successRate')
}

export function smoothedMetricKeysForChart(chart: ChartConfig, visibleMetricKeys: string[]) {
  if (isLoadImpactChart(chart) || isLoadImpactHeatmapChart(chart) || isLoadImpactPercentilesChart(chart)) return []
  return visibleMetricKeys.filter(isSmoothableGraphMetricKey)
}

export function rollingArtStdDevWindowForRadius(radius: number) {
  const normalizedRadius = Number.isFinite(radius) ? Math.max(0, Math.floor(radius)) : 0
  return Math.min(MAX_ART_STD_DEV_WINDOW, Math.max(MIN_ART_STD_DEV_WINDOW, (normalizedRadius * 2) + 1))
}

export function shouldRenderArtStdDevBands(chart: ChartConfig, visibleMetricKeys: string[], enabled: boolean) {
  return enabled && chart.id === 'latency' && visibleMetricKeys.includes('avgMs')
}

export function smoothingRadiusForGraphDensity(pointCount: number, density: GraphSmoothingDensity = {}) {
  if (pointCount < (MIN_GRAPH_SMOOTHING_RADIUS * 2) + 1) return 0

  const chartWidth = Math.max(220, density.chartWidth ?? DEFAULT_GRAPH_CARD_WIDTH)
  const chartHeight = Math.max(120, density.chartHeight ?? DEFAULT_GRAPH_CARD_HEIGHT)
  const plotWidth = Math.max(180, chartWidth - 122)
  const plotHeight = Math.max(90, chartHeight - 74)
  const pixelsPerPoint = plotWidth / Math.max(1, pointCount - 1)
  const seriesCrowding = Math.min(1.5, 1 + Math.max(0, (density.visibleMetricCount ?? 1) - 2) * 0.08)
  const verticalCrowding = plotHeight < 110 ? 1.2 : plotHeight > 240 ? 0.85 : 1
  const adjustedPixelsPerPoint = pixelsPerPoint / (seriesCrowding * verticalCrowding)

  let radius = MIN_GRAPH_SMOOTHING_RADIUS
  if (adjustedPixelsPerPoint < 2) {
    radius = 6
  } else if (adjustedPixelsPerPoint < 3.5) {
    radius = 5
  } else if (adjustedPixelsPerPoint < 5.5) {
    radius = 4
  } else if (adjustedPixelsPerPoint < 8) {
    radius = 3
  }
  if (density.expanded) {
    radius = Math.max(MIN_GRAPH_SMOOTHING_RADIUS, radius - 1)
  }

  return Math.min(MAX_GRAPH_SMOOTHING_RADIUS, radius, Math.floor((pointCount - 1) / 2))
}

export function downsampleTargetPointsForGraphDensity(density: GraphSmoothingDensity = {}) {
  if (!density.chartWidth) return DEFAULT_DOWNSAMPLED_GRAPH_POINTS
  const chartWidth = Math.max(220, density.chartWidth)
  const seriesCrowding = Math.min(1.5, 1 + Math.max(0, (density.visibleMetricCount ?? 1) - 2) * 0.08)
  const expandedMultiplier = density.expanded ? 2.1 : 1.6
  const target = Math.round((chartWidth * expandedMultiplier) / seriesCrowding)
  return Math.min(MAX_DOWNSAMPLED_GRAPH_POINTS, Math.max(MIN_DOWNSAMPLED_GRAPH_POINTS, target))
}

function numericGraphValue(point: MetricsHistoryPoint, key: string) {
  const value = point[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (Array.isArray(value)) {
    const finiteValues = value.filter((item): item is number => typeof item === 'number' && Number.isFinite(item))
    if (finiteValues.length > 0) {
      return finiteValues.reduce((sum, item) => sum + item, 0) / finiteValues.length
    }
  }
  return undefined
}

function evenlySampleGraphData(data: MetricsHistoryPoint[], maxPoints: number) {
  if (data.length <= maxPoints) return data
  const lastIndex = data.length - 1
  const indexes = new Set<number>()
  for (let index = 0; index < maxPoints; index += 1) {
    indexes.add(Math.round(index * lastIndex / Math.max(1, maxPoints - 1)))
  }
  return Array.from(indexes).sort((left, right) => left - right).map(index => data[index])
}

function metricRanges(data: MetricsHistoryPoint[], metricKeys: string[]) {
  const ranges = new Map<string, { min: number; max: number }>()
  metricKeys.forEach(key => {
    data.forEach(point => {
      const value = numericGraphValue(point, key)
      if (value === undefined) return
      const current = ranges.get(key)
      if (!current) {
        ranges.set(key, { min: value, max: value })
        return
      }
      current.min = Math.min(current.min, value)
      current.max = Math.max(current.max, value)
    })
  })
  return ranges
}

function compositeGraphScore(point: MetricsHistoryPoint, metricKeys: string[], ranges: Map<string, { min: number; max: number }>) {
  let total = 0
  let samples = 0
  metricKeys.forEach(key => {
    const value = numericGraphValue(point, key)
    const range = ranges.get(key)
    if (value === undefined || !range) return
    const spread = range.max - range.min
    total += spread === 0 ? 0.5 : (value - range.min) / spread
    samples += 1
  })
  return samples > 0 ? total / samples : undefined
}

export function downsampleGraphData(data: MetricsHistoryPoint[], metricKeys: string[], maxPoints = DEFAULT_DOWNSAMPLED_GRAPH_POINTS) {
  const target = Math.max(3, Math.floor(maxPoints))
  if (data.length <= target) return data
  if (target < 8) return evenlySampleGraphData(data, target)

  const uniqueMetricKeys = Array.from(new Set(metricKeys))
  if (uniqueMetricKeys.length === 0) return evenlySampleGraphData(data, target)

  const ranges = metricRanges(data, uniqueMetricKeys)
  if (ranges.size === 0) return evenlySampleGraphData(data, target)

  const retainedIndexes = new Set<number>([0, data.length - 1])
  const bucketCount = Math.max(1, Math.floor((target - 2) / 4))
  const interiorPointCount = data.length - 2

  for (let bucket = 0; bucket < bucketCount; bucket += 1) {
    const start = 1 + Math.floor(bucket * interiorPointCount / bucketCount)
    const endExclusive = 1 + Math.floor((bucket + 1) * interiorPointCount / bucketCount)
    if (start >= endExclusive) continue

    let lowIndex = start
    let highIndex = start
    let lowScore = Number.POSITIVE_INFINITY
    let highScore = Number.NEGATIVE_INFINITY

    for (let index = start; index < endExclusive; index += 1) {
      const score = compositeGraphScore(data[index], uniqueMetricKeys, ranges)
      if (score === undefined) continue
      if (score < lowScore) {
        lowScore = score
        lowIndex = index
      }
      if (score > highScore) {
        highScore = score
        highIndex = index
      }
    }

    retainedIndexes.add(start)
    retainedIndexes.add(lowIndex)
    retainedIndexes.add(highIndex)
    retainedIndexes.add(endExclusive - 1)
  }

  return Array.from(retainedIndexes)
    .sort((left, right) => left - right)
    .map(index => data[index])
}

function smoothingWeightForOffset(offset: number, radius: number) {
  return radius + 1 - Math.abs(offset)
}

function smoothedMetricValue(data: MetricsHistoryPoint[], key: string, index: number, radius: number) {
  const rawValue = data[index][key]
  if (typeof rawValue !== 'number' || !Number.isFinite(rawValue)) return undefined

  let weightedTotal = 0
  let weightTotal = 0
  for (let offset = -radius; offset <= radius; offset += 1) {
    const candidate = data[index + offset]?.[key]
    if (typeof candidate !== 'number' || !Number.isFinite(candidate)) continue
    const weight = smoothingWeightForOffset(offset, radius)
    weightedTotal += candidate * weight
    weightTotal += weight
  }

  if (weightTotal === 0) return undefined
  return roundChartValue(weightedTotal / weightTotal)
}

export function buildSmoothedGraphData(data: MetricsHistoryPoint[], metricKeys: string[], smoothingRadius = MIN_GRAPH_SMOOTHING_RADIUS) {
  const uniqueMetricKeys = Array.from(new Set(metricKeys))
  if (data.length === 0 || uniqueMetricKeys.length === 0) return data
  const radius = Math.max(0, Math.floor(smoothingRadius))

  const next = data.map(point => ({ ...point }))
  uniqueMetricKeys.forEach(key => {
    const finiteIndexes = data
      .map((point, index) => ({ value: point[key], index }))
      .filter((item): item is { value: number; index: number } => typeof item.value === 'number' && Number.isFinite(item.value))
      .map(item => item.index)

    if (radius < 1 || finiteIndexes.length < (radius * 2) + 1) {
      finiteIndexes.forEach(index => {
        next[index][smoothedGraphMetricKey(key)] = data[index][key]
      })
      return
    }

    const firstFiniteIndex = finiteIndexes[0]
    const lastFiniteIndex = finiteIndexes[finiteIndexes.length - 1]
    finiteIndexes.forEach(index => {
      next[index][smoothedGraphMetricKey(key)] = index === firstFiniteIndex || index === lastFiniteIndex
        ? data[index][key]
        : smoothedMetricValue(data, key, index, radius)
    })
  })

  return next
}

export function buildArtStdDevBandData(
  data: MetricsHistoryPoint[],
  smoothingRadius = MIN_GRAPH_SMOOTHING_RADIUS,
  sourceKey = 'avgMs',
  bandKey = ART_STD_DEV_BAND_KEY,
  deviationKey = ART_STD_DEV_VALUE_KEY,
) {
  if (data.length === 0) return data
  const windowSize = rollingArtStdDevWindowForRadius(smoothingRadius)
  const halfWindow = Math.floor(windowSize / 2)

  return data.map((point, index) => {
    const centerValue = point[sourceKey]
    if (typeof centerValue !== 'number' || !Number.isFinite(centerValue)) {
      return point
    }

    const values: number[] = []
    const start = Math.max(0, index - halfWindow)
    const end = Math.min(data.length - 1, index + halfWindow)
    for (let sampleIndex = start; sampleIndex <= end; sampleIndex += 1) {
      const value = data[sampleIndex]?.[sourceKey]
      if (typeof value === 'number' && Number.isFinite(value)) {
        values.push(value)
      }
    }

    if (values.length < 2) return point

    const mean = values.reduce((sum, value) => sum + value, 0) / values.length
    const variance = values.reduce((sum, value) => sum + ((value - mean) ** 2), 0) / values.length
    const stdDev = Math.sqrt(variance)
    const lower = Math.max(0, roundChartValue(mean - stdDev))
    const upper = Math.max(lower, roundChartValue(mean + stdDev))

    return {
      ...point,
      [bandKey]: [lower, upper],
      [deviationKey]: roundChartValue(stdDev),
    }
  })
}

export function responseTimeDataWithoutNetworkLatency(data: MetricsHistoryPoint[], latencyKeys: string[], groupNames: string[]) {
  if (latencyKeys.length === 0) return data
  const responseKeys = responseTimeMetricKeys(groupNames)
  return data.map(point => {
    const routeLatencyMs = averageRouteLatencyForPoint(point, latencyKeys)
    if (routeLatencyMs === undefined) return point
    const next = { ...point }
    responseKeys.forEach(key => {
      const value = point[key]
      if (typeof value === 'number' && Number.isFinite(value)) {
        next[key] = Math.max(0, roundChartValue(value - routeLatencyMs))
      }
    })
    return next
  })
}

function routeLatencyMetricKeysForMode(
  latencyKeys: string[],
  mode: RouteLatencyMode,
  selectedLatencyKey?: string,
) {
  const selectedKey = selectedLatencyKey && latencyKeys.includes(selectedLatencyKey)
    ? selectedLatencyKey
    : latencyKeys[0]
  if (mode === 'total' || !selectedKey) {
    return latencyKeys
  }
  if (mode === 'phases') {
    return routeLatencyPhaseRows.map(row => row.metric === 'total' ? selectedKey : routeLatencyDetailSeriesKey(selectedKey, row.metric))
  }
  return routeLatencyStabilityRows.map(row => routeLatencyDetailSeriesKey(selectedKey, row.metric))
}

function routeLatencyMetricRowsForMode(
  latencyKeys: string[],
  groupColors: string[],
  mode: RouteLatencyMode,
  selectedLatencyKey?: string,
): ChartMetricRow[] {
  const selectedKey = selectedLatencyKey && latencyKeys.includes(selectedLatencyKey)
    ? selectedLatencyKey
    : latencyKeys[0]
  if (mode === 'total' || !selectedKey) {
    return latencyKeys.map((key, index) => ({
      key,
      label: routeLatencySeriesLabel(key),
      color: groupColors[index % groupColors.length],
      unit: 'ms',
    }))
  }
  if (mode === 'phases') {
    return routeLatencyPhaseRows.map(row => ({
      key: row.metric === 'total' ? selectedKey : routeLatencyDetailSeriesKey(selectedKey, row.metric),
      label: row.label,
      color: row.color,
      unit: 'ms',
    }))
  }
  return routeLatencyStabilityRows.map(row => ({
    key: routeLatencyDetailSeriesKey(selectedKey, row.metric),
    label: row.label,
    color: row.color,
    unit: row.unit,
  }))
}

export function chartMetricKeys(
  chart: ChartConfig,
  groupNames: string[],
  latencyKeys: string[],
  includeVuserOverlay = false,
  overlayMetricKeys: OverlayMetricKey[] = [],
  routeLatencyMode: RouteLatencyMode = 'total',
  selectedRouteLatencyKey?: string,
) {
  let keys: string[]
  if (isLoadImpactHeatmapChart(chart)) {
    return []
  }
  if (isPrometheusChart(chart)) {
    return chart.prometheusGraph?.series.map(series => series.key) ?? []
  }
  if (isLoadImpactPercentilesChart(chart)) {
    keys = loadImpactPercentileMetricRows.map(row => row.key)
    const overlays = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !keys.includes(key))
    return overlays.length > 0 ? [...keys, ...overlays] : keys
  }
  if (isLoadImpactChart(chart)) {
    keys = ['average', 'min', 'max']
    const overlays = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !keys.includes(key))
    return overlays.length > 0 ? [...keys, ...overlays] : keys
  }
  switch (chart.id) {
    case 'tps':
      keys = [
        chart.totalKey ?? 'tps',
        ...groupNames.map(name => `${name}_tps`),
      ]
      break
    case 'latency':
      keys = [
        'avgMs',
        ...groupNames.map(name => `${name}_avgMs`),
        'p50',
        'p90',
        'p95',
        'p99',
      ]
      break
    case 'io':
      keys = ['netSent', 'netRecv', 'diskRead', 'diskWrite']
      break
    case 'generator_impact':
      keys = generatorImpactMetricRows.map(row => row.key)
      break
    case 'network_latency':
      keys = routeLatencyMetricKeysForMode(latencyKeys, routeLatencyMode, selectedRouteLatencyKey)
      break
    default:
      if (chart.keys) {
        keys = chart.keys.map(key => key.k)
      } else {
        keys = chart.dataKey ? [chart.dataKey] : []
      }
      break
  }
  const overlays = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
    .filter(key => !keys.includes(key))
  return overlays.length > 0 ? [...keys, ...overlays] : keys
}

export function chartMetricRows(
  chart: ChartConfig,
  groupNames: string[],
  latencyKeys: string[],
  groupColors: string[],
  includeVuserOverlay = false,
  overlayMetricKeys: OverlayMetricKey[] = [],
  routeLatencyMode: RouteLatencyMode = 'total',
  selectedRouteLatencyKey?: string,
): ChartMetricRow[] {
  let rows: ChartMetricRow[]
  if (isLoadImpactHeatmapChart(chart)) {
    return []
  }
  if (isPrometheusChart(chart)) {
    const unit = chart.prometheusGraph?.unit
    return chart.prometheusGraph?.series.map((series, index) => ({
      key: series.key,
      label: series.label || series.key,
      color: PROMETHEUS_GRAPH_COLORS[index % PROMETHEUS_GRAPH_COLORS.length],
      unit,
    })) ?? []
  }
  if (isLoadImpactPercentilesChart(chart)) {
    rows = loadImpactPercentileMetricRows
    const overlayRows = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !rows.some(row => row.key === key))
      .map(key => overlayMetricRows[key])
    return overlayRows.length > 0 ? [...rows, ...overlayRows] : rows
  }
  if (isLoadImpactChart(chart)) {
    const unit = chart.loadImpactMetric?.unit
    rows = [
      { key: 'average', label: 'Average', color: chart.color ?? 'var(--chart-1)', unit },
      { key: 'min', label: 'Min', color: 'var(--chart-3)', unit },
      { key: 'max', label: 'Max', color: 'var(--chart-4)', unit },
    ]
    const overlayRows = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !rows.some(row => row.key === key))
      .map(key => overlayMetricRows[key])
    return overlayRows.length > 0 ? [...rows, ...overlayRows] : rows
  }
  switch (chart.id) {
    case 'vusers':
      rows = [{ key: 'vusers', label: 'Active VUsers', color: chart.color ?? 'var(--chart-1)' }]
      break
    case 'tps':
      rows = [
        { key: chart.totalKey ?? 'tps', label: 'Total', color: 'var(--text-muted)' },
        ...groupNames.map((name, index) => ({ key: `${name}_tps`, label: name, color: groupColors[index % groupColors.length] })),
      ]
      break
    case 'latency':
      rows = [
        { key: 'avgMs', label: 'Total', color: 'var(--text-muted)', unit: 'ms', kind: 'transaction' },
        ...groupNames.map((name, index) => ({ key: `${name}_avgMs`, label: name, color: groupColors[index % groupColors.length], unit: 'ms', kind: 'transaction' as const })),
        { key: 'p50', label: 'P50', color: 'var(--chart-5)', unit: 'ms', kind: 'percentile' },
        { key: 'p90', label: 'P90', color: 'var(--chart-3)', unit: 'ms', kind: 'percentile' },
        { key: 'p95', label: 'P95', color: 'var(--chart-2)', unit: 'ms', kind: 'percentile' },
        { key: 'p99', label: 'P99', color: 'var(--chart-4)', unit: 'ms', kind: 'percentile' },
      ]
      break
    case 'errors':
      rows = [{ key: chart.dataKey ?? 'errorPct', label: 'Error Rate', color: chart.color ?? 'var(--chart-4)', unit: '%' }]
      break
    case 'io':
      rows = [
        { key: 'netSent', label: 'Net Egress', color: 'var(--chart-1)', unit: 'KB/s' },
        { key: 'netRecv', label: 'Net Ingress', color: 'var(--chart-2)', unit: 'KB/s' },
        { key: 'diskRead', label: 'Disk Read', color: 'var(--chart-3)', unit: 'KB/s' },
        { key: 'diskWrite', label: 'Disk Write', color: 'var(--chart-6)', unit: 'KB/s' },
      ]
      break
    case 'generator_impact':
      rows = generatorImpactMetricRows
      break
    case 'network_latency':
      rows = routeLatencyMetricRowsForMode(latencyKeys, groupColors, routeLatencyMode, selectedRouteLatencyKey)
      break
    default:
      rows = chart.keys
        ? chart.keys.map(key => ({ key: key.k, label: key.n, color: key.c, unit: chart.id === 'cpu' ? '%' : 'MB' }))
        : chart.dataKey
          ? [{ key: chart.dataKey, label: 'Total', color: chart.color ?? 'var(--chart-1)' }]
          : []
      break
  }
  const overlayRows = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
    .filter(key => !rows.some(row => row.key === key))
    .map(key => overlayMetricRows[key])
  return overlayRows.length > 0 ? [...rows, ...overlayRows] : rows
}

export function chartMetricLinePresentation(row: ChartMetricRow): ChartMetricLinePresentation {
  if (row.kind === 'transaction') {
    return {
      role: 'transaction',
      strokeWidth: row.key === 'avgMs' ? 2.5 : 2.2,
      strokeOpacity: 1,
    }
  }
  if (row.kind === 'percentile') {
    return {
      role: 'percentile',
      strokeWidth: 1.25,
      strokeDasharray: row.key === 'p95' || row.key === 'p99' ? '4 2' : '3 3',
      strokeOpacity: 0.68,
    }
  }
  return {
    role: 'default',
    strokeWidth: 2,
    strokeOpacity: 1,
  }
}

export function chartMetricSummary(data: MetricsHistoryPoint[], key: string): ChartMetricSummary {
  const values = data
    .map(point => point[key])
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))

  if (values.length === 0) {
    return { samples: 0 }
  }

  return {
    latest: values[values.length - 1],
    average: values.reduce((sum, value) => sum + value, 0) / values.length,
    min: Math.min(...values),
    max: Math.max(...values),
    samples: values.length,
  }
}

function roundDomainValue(value: number) {
  return Math.round(value * 1_000_000) / 1_000_000
}

export function visibleMetricDomain(data: MetricsHistoryPoint[], metricKeys: string[], fallbackDomain?: ChartDomain): ChartDomain | undefined {
  const visibleKeySet = new Set(metricKeys)
  if (visibleKeySet.size === 0) return fallbackDomain

  const values = data.flatMap(point => (
    Object.entries(point)
      .filter(([key]) => visibleKeySet.has(key))
      .flatMap(([, value]) => (
        Array.isArray(value)
          ? value.filter((item): item is number => typeof item === 'number' && Number.isFinite(item))
          : typeof value === 'number' && Number.isFinite(value)
            ? [value]
            : []
      ))
  ))

  if (values.length === 0) return fallbackDomain

  const min = Math.min(...values)
  const max = Math.max(...values)
  if (min >= 0) {
    return [0, max > 0 ? roundDomainValue(max * 1.08) : 1]
  }

  const range = max - min
  const padding = range > 0 ? range * 0.08 : Math.max(Math.abs(max) * 0.08, 1)
  return [roundDomainValue(min - padding), roundDomainValue(max + padding)]
}

function loadImpactMetricValueForBucket(row: LoadImpactRow | undefined, bucketId: string) {
  return row?.cells.find(cell => cell.bucket.id === bucketId)?.average
}

export function loadImpactChartDataForRow(row: LoadImpactRow | undefined, rows: LoadImpactRow[]): MetricsHistoryPoint[] {
  if (!row) return []
  const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
  const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')
  return row.cells.map((cell): MetricsHistoryPoint => {
    const midpointVusers = cell.bucket.minVusers + ((cell.bucket.maxVusers - cell.bucket.minVusers) / 2)
    return {
      time: cell.bucket.label,
      vusers: midpointVusers,
      tps: loadImpactMetricValueForBucket(tpsRow, cell.bucket.id) ?? 0,
      avgMs: loadImpactMetricValueForBucket(avgRow, cell.bucket.id) ?? 0,
      errorPct: 0,
      p50: 0,
      p90: 0,
      p99: 0,
      average: cell.average,
      min: cell.min,
      max: cell.max,
      samples: cell.sampleCount,
      bucketMinVusers: cell.bucket.minVusers,
      bucketMaxVusers: cell.bucket.maxVusers,
      sourceSamples: cell.sampleCount,
    }
  })
}

export function loadImpactPercentileChartData(rows: LoadImpactRow[]): MetricsHistoryPoint[] {
  const percentileRows = new Map(LOAD_IMPACT_PERCENTILE_KEYS.map(key => [
    key,
    rows.find(row => row.metric.id === key),
  ]))
  const referenceRow = LOAD_IMPACT_PERCENTILE_KEYS
    .map(key => percentileRows.get(key))
    .find((row): row is LoadImpactRow => Boolean(row))
  if (!referenceRow) return []

  const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
  const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')
  return referenceRow.cells.map((cell): MetricsHistoryPoint => {
    const midpointVusers = cell.bucket.minVusers + ((cell.bucket.maxVusers - cell.bucket.minVusers) / 2)
    return {
      time: cell.bucket.label,
      vusers: midpointVusers,
      tps: loadImpactMetricValueForBucket(tpsRow, cell.bucket.id) ?? 0,
      avgMs: loadImpactMetricValueForBucket(avgRow, cell.bucket.id) ?? 0,
      errorPct: 0,
      p50: loadImpactMetricValueForBucket(percentileRows.get('p50'), cell.bucket.id) ?? 0,
      p90: loadImpactMetricValueForBucket(percentileRows.get('p90'), cell.bucket.id) ?? 0,
      p95: loadImpactMetricValueForBucket(percentileRows.get('p95'), cell.bucket.id) ?? 0,
      p99: loadImpactMetricValueForBucket(percentileRows.get('p99'), cell.bucket.id) ?? 0,
      bucketMinVusers: cell.bucket.minVusers,
      bucketMaxVusers: cell.bucket.maxVusers,
      sourceSamples: cell.sampleCount,
    }
  })
}

export function graphCsvForChart(
  chart: ChartConfig,
  data: MetricsHistoryPoint[],
  groupNames: string[],
  latencyKeys: string[],
  includeVuserOverlay = false,
  overlayMetricKeys: OverlayMetricKey[] = [],
  routeLatencyMode: RouteLatencyMode = 'total',
  selectedRouteLatencyKey?: string,
) {
  const firstColumn = isLoadImpactChart(chart) || isLoadImpactPercentilesChart(chart) ? 'load' : 'time'
  const keys = [firstColumn, ...chartMetricKeys(chart, groupNames, latencyKeys, includeVuserOverlay, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey)]
  const rows = [
    keys.map(escapeCsvCell).join(','),
    ...data.map(point => keys.map(key => escapeCsvCell(key === firstColumn ? point.time : point[key])).join(',')),
  ]
  return rows.join('\n')
}

function escapeCsvCell(value: unknown) {
  if (value === undefined || value === null) return ''
  const text = String(value)
  if (!/[",\n\r]/.test(text)) return text
  return `"${text.replace(/"/g, '""')}"`
}

export function formatGraphOverlayMetricValue(value: unknown): string | number {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value.toLocaleString(undefined, { maximumFractionDigits: 2 }) : 'n/a'
  }
  if (typeof value === 'string') return value
  if (value === undefined || value === null) return 'n/a'
  return String(value)
}
