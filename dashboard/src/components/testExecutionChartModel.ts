import { type ComponentProps } from 'react'
import { LineChart, YAxis } from 'recharts'

import type { ImportedDashboardPanelKind, ImportedDashboardResult, MetricsHistoryPoint, PrometheusGraph } from '../types'
import {
  isRouteLatencyTotalSeriesKey,
  routeLatencyDetailSeriesKey,
  routeLatencySeriesLabel,
} from './latencySeries'
import {
  createLoadImpactBuckets,
  loadImpactBucketForVusers,
  type LoadImpactBucket,
  type LoadImpactMetric,
  type LoadImpactPoint,
  type LoadImpactRow,
} from './loadImpactModel'
import {
  MAX_TRANSACTION_CHART_SERIES,
  isTransactionChartSeriesKey,
  transactionChartSeriesKey,
  transactionChartSeriesPresentation,
} from './transactionChartSeries'

// The percentiles the aggregate, per-group, and per-transaction series all
// report, each with its own Live Metrics graph.
export type ResponsePercentileKey = 'p50' | 'p90' | 'p95' | 'p99'

export type ChartDomain = ComponentProps<typeof YAxis>['domain']

export interface ImportedPanelRef {
  dashboardId: string
  dashboardTitle: string
  panelId: number
  kind: ImportedDashboardPanelKind
}

export interface ImportedStatValue {
  value?: number
  unit?: string
  error?: string
}

export interface ChartConfig {
  id: string
  title: string
  type: 'area' | 'line-multi' | 'line-latency' | 'area-multi' | 'area-stacked' | 'line-io' | 'line-network' | 'line-impact' | 'line-prometheus' | 'line-load-impact' | 'line-load-impact-percentiles' | 'load-impact-heatmap' | 'stat-tile'
  dataKey?: string
  color?: string
  totalKey?: string
  keys?: Array<{ k: string; n: string; c: string }>
  stackId?: string
  domain?: ChartDomain
  defaultMetricKeys?: string[]
  loadImpactMetric?: LoadImpactMetric
  // Set on the standalone P50/P90/P95/P99 response-time graphs: the single
  // percentile every series on the graph reports.
  percentileKey?: ResponsePercentileKey
  // Groups that have their own line on this Under Load graph.
  loadImpactGroupNames?: string[]
  prometheusGraph?: PrometheusGraph
  // Imported Grafana dashboard panels.
  importedPanel?: ImportedPanelRef
  statValue?: ImportedStatValue
  importedError?: string
}

export type ChartMetricRowKind = 'transaction' | 'percentile' | 'other'

export interface ChartMetricRow {
  key: string
  label: string
  color: string
  unit?: string
  kind?: ChartMetricRowKind
  strokeDasharray?: string
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

export interface BoundedChartItems<T> {
  items: T[]
  omitted: number
}

export function boundedChartItems<T>(items: ReadonlyArray<T>, limit: number): BoundedChartItems<T> {
  const boundedLimit = Number.isFinite(limit) ? Math.max(0, Math.floor(limit)) : 0
  return {
    items: items.slice(0, boundedLimit),
    omitted: Math.max(0, items.length - boundedLimit),
  }
}

const GROUP_LINE_DASH_PATTERNS = [undefined, '5 3', '2 3', '8 3 2 3', '1 3', '6 2 1 2'] as const

export function groupSeriesDasharray(index: number, paletteSize: number): string | undefined {
  if (paletteSize <= 0 || index < paletteSize) return undefined
  const repeatedPaletteCycle = Math.floor(index / paletteSize)
  return GROUP_LINE_DASH_PATTERNS[repeatedPaletteCycle % GROUP_LINE_DASH_PATTERNS.length]
}

export type MetricTableSortKey = 'metric' | 'latest' | 'average' | 'min' | 'max'

export interface MetricTableSort {
  key: MetricTableSortKey
  direction: 'asc' | 'desc'
}

// sortMetricTableRows orders the measurement-table rows by a column (the metric
// name, or one of the latest/avg/min/max summary values). Rows whose summary
// value is missing always sort to the bottom regardless of direction. Returns the
// input array unchanged when no sort is active.
export function sortMetricTableRows(
  rows: ChartMetricRow[],
  summaries: Record<string, ChartMetricSummary>,
  sort: MetricTableSort | undefined,
): ChartMetricRow[] {
  if (!sort) return rows
  const factor = sort.direction === 'asc' ? 1 : -1
  const valueFor = (row: ChartMetricRow): number | string | undefined => {
    if (sort.key === 'metric') return row.label.toLowerCase()
    return summaries[row.key]?.[sort.key]
  }
  return [...rows].sort((left, right) => {
    const a = valueFor(left)
    const b = valueFor(right)
    if (a === undefined && b === undefined) return 0
    if (a === undefined) return 1
    if (b === undefined) return -1
    if (typeof a === 'string' || typeof b === 'string') {
      return factor * String(a).localeCompare(String(b))
    }
    return factor * (a - b)
  })
}

// SharedCursorRef identifies the point the user is hovering, independent of any
// single chart's array layout: a clock-time label plus the underlying sample
// timestamp. The timestamp is what makes the shared crosshair line up across
// charts that were downsampled to different lengths.
export interface SharedCursorRef {
  label?: string | number
  timestampMs?: number
}

interface CursorResolvablePoint {
  time?: string | number
  timestampMs?: number
}

// resolveSharedCursorTime maps a shared hover cursor onto a specific chart's
// data, returning the x-axis category value (the point's `time`) where the
// crosshair should sit — or undefined to draw nothing.
//
// It resolves by IDENTITY (exact label, else nearest timestamp), never by a
// positional index. Indexing one chart's cursor position into another chart's
// differently-sized array is what made the crosshair drift progressively away
// from the pointer as it moved right: when the rendered chart held more points
// than the hovered one, index i landed at a smaller fraction of the width.
// Matching on timestamp keeps every chart aligned regardless of downsampling.
export function resolveSharedCursorTime(
  chartData: ReadonlyArray<CursorResolvablePoint>,
  cursor: SharedCursorRef | null | undefined,
): string | number | undefined {
  if (!cursor || chartData.length === 0) return undefined

  // Fast path: the hovered clock label is present verbatim — the common case
  // when charts share the same time domain.
  if (cursor.label !== undefined && chartData.some(point => point.time === cursor.label)) {
    return cursor.label
  }

  // Otherwise land on the sample closest in time. This covers charts that were
  // downsampled differently (so the exact hovered label isn't a member) without
  // borrowing a meaningless positional index.
  if (typeof cursor.timestampMs === 'number' && Number.isFinite(cursor.timestampMs)) {
    let bestTime: string | number | undefined
    let bestDelta = Infinity
    for (const point of chartData) {
      if (typeof point.timestampMs !== 'number') continue
      const delta = Math.abs(point.timestampMs - cursor.timestampMs)
      if (delta < bestDelta) {
        bestDelta = delta
        bestTime = point.time
      }
    }
    if (bestTime !== undefined) return bestTime
  }

  return undefined
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
const PROMETHEUS_GRAPH_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)', 'var(--chart-7)', 'var(--chart-8)']
const MIN_GRAPH_SMOOTHING_RADIUS = 2
const MAX_GRAPH_SMOOTHING_RADIUS = 6
const DEFAULT_DOWNSAMPLED_GRAPH_POINTS = 420
const MIN_DOWNSAMPLED_GRAPH_POINTS = 180
const MAX_DOWNSAMPLED_GRAPH_POINTS = 900
export const MAX_CHART_LEGEND_ITEMS = 24
export const MAX_CHART_TOOLTIP_ITEMS = 12
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

export const RESPONSE_PERCENTILE_KEYS: ResponsePercentileKey[] = ['p50', 'p90', 'p95', 'p99']

const RESPONSE_PERCENTILE_COLORS: Record<ResponsePercentileKey, string> = {
  p50: 'var(--chart-5)',
  p90: 'var(--chart-3)',
  p95: 'var(--chart-2)',
  p99: 'var(--chart-4)',
}

export function responsePercentileChartId(percentileKey: ResponsePercentileKey) {
  return `latency_${percentileKey}`
}

export function responsePercentileLabel(percentileKey: ResponsePercentileKey) {
  return percentileKey.toUpperCase()
}

const responsePercentileCharts: ChartConfig[] = RESPONSE_PERCENTILE_KEYS.map(percentileKey => ({
  id: responsePercentileChartId(percentileKey),
  title: `${responsePercentileLabel(percentileKey)} Response Time (ms)`,
  type: 'line-multi',
  totalKey: percentileKey,
  color: RESPONSE_PERCENTILE_COLORS[percentileKey],
  percentileKey,
}))

export function isResponsePercentileChart(chart: ChartConfig): chart is ChartConfig & { percentileKey: ResponsePercentileKey } {
  return chart.percentileKey !== undefined
}

export const chartConfigs: ChartConfig[] = [
  { id: 'vusers', title: 'VUsers', type: 'area', dataKey: 'vusers', color: 'var(--chart-1)' },
  { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' },
  { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' },
  ...responsePercentileCharts,
  { id: 'errors', title: 'Error Rate (%)', type: 'line-multi', totalKey: 'errorPct', color: 'var(--chart-4)', domain: [0, 'auto'] },
  {
    id: 'cpu',
    title: 'CPU Usage — Fleet (%)',
    type: 'area-multi',
    keys: [
      { k: 'cpuProcess', n: 'VUser avg %', c: 'var(--chart-1)' },
      { k: 'cpuFree', n: 'System avg %', c: 'var(--chart-2)' },
      { k: 'cpuProcessMax', n: 'Worst pod %', c: 'var(--chart-3)' },
    ],
    domain: [0, 100],
  },
  {
    id: 'mem',
    title: 'Memory — Fleet total (MB)',
    type: 'area-multi',
    keys: [
      { k: 'memProcess', n: 'VUser total MB', c: 'var(--chart-4)' },
      { k: 'memFree', n: 'System free MB', c: 'var(--chart-5)' },
    ],
  },
  { id: 'io', title: 'Network & Disk I/O (KB/s)', type: 'line-io' },
  {
    id: 'tx_network_phases',
    title: 'Network Phase Breakdown — Transaction Traffic (ms)',
    type: 'area-stacked',
    stackId: 'phases',
    keys: [
      { k: 'txDnsMs', n: 'DNS', c: 'var(--chart-5)' },
      { k: 'txConnectMs', n: 'Connect', c: 'var(--chart-1)' },
      { k: 'txTlsMs', n: 'TLS', c: 'var(--chart-3)' },
      { k: 'txTtfbMs', n: 'TTFB', c: 'var(--chart-4)' },
      { k: 'txWaitMs', n: 'Wait', c: 'var(--chart-2)' },
    ],
    domain: [0, 'auto'],
  },
  {
    id: 'error_taxonomy',
    title: 'Error Taxonomy (stacked)',
    type: 'area-stacked',
    stackId: 'errors',
    keys: [
      // One chart slot per category: status tokens alias chart slots in most
      // themes (--danger == --chart-4), which made two categories identical.
      { k: 'errTimeout', n: 'Timeout', c: 'var(--chart-3)' },
      { k: 'errConnection', n: 'Connection', c: 'var(--chart-5)' },
      { k: 'errHttp4xx', n: 'HTTP 4xx', c: 'var(--chart-1)' },
      { k: 'errHttp5xx', n: 'HTTP 5xx', c: 'var(--chart-4)' },
      { k: 'errAssertion', n: 'Assertion', c: 'var(--chart-6)' },
      { k: 'errResourceLimit', n: 'Generator resource limit', c: 'var(--chart-7)' },
      { k: 'errSessionLost', n: 'Observer session lost', c: 'var(--chart-2)' },
      { k: 'errOther', n: 'Other', c: 'var(--chart-8)' },
    ],
    domain: [0, 'auto'],
  },
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

// Under-Load graphs draw one line per selected group alongside the aggregate, so
// a group series needs a data key that cannot collide with the aggregate keys
// (average/min/max) or with the time-series group keys (`Checkout_avgMs`).
const LOAD_IMPACT_GROUP_SERIES_SUFFIX = '__loadImpactAverage'

export function loadImpactGroupSeriesKey(groupName: string) {
  return `${groupName}${LOAD_IMPACT_GROUP_SERIES_SUFFIX}`
}

export function isLoadImpactGroupSeriesKey(key: string) {
  return key.endsWith(LOAD_IMPACT_GROUP_SERIES_SUFFIX)
}

// loadImpactGroupSeriesNames returns the groups that actually reported the metric
// somewhere in the model, so a chart never advertises an empty group line.
export function loadImpactGroupSeriesNames(row: LoadImpactRow | undefined, groupNames: string[]) {
  if (!row) return []
  return groupNames.filter(name => (row.groupPoints?.[name]?.length ?? 0) > 0)
}

export function buildLoadImpactCharts(rows: LoadImpactRow[], groupNames: string[] = []): ChartConfig[] {
  return rows.map((row, index) => {
    const seriesGroups = loadImpactGroupSeriesNames(row, groupNames)
    return {
      id: loadImpactChartId(row.metric.id),
      title: `${row.metric.label} Under Load`,
      type: 'line-load-impact',
      color: LOAD_IMPACT_GRAPH_COLORS[index % LOAD_IMPACT_GRAPH_COLORS.length],
      defaultMetricKeys: seriesGroups.length > 0
        ? seriesGroups.map(loadImpactGroupSeriesKey)
        : ['average'],
      loadImpactMetric: row.metric,
      loadImpactGroupNames: seriesGroups.length > 0 ? seriesGroups : undefined,
    }
  })
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

export function isImportedDashboardChart(chart: ChartConfig): boolean {
  return Boolean(chart.importedPanel)
}

export function isStatTileChart(chart: ChartConfig): boolean {
  return chart.type === 'stat-tile'
}

// buildImportedDashboardCharts turns resolved imported-dashboard panels into chart
// cards: line panels reuse the existing 'line-prometheus' renderer, single-value
// panels become 'stat-tile' cards. Chart ids are the backend's globally-namespaced
// panel keys; a defensive de-dup guarantees they can never collide with the static
// chartConfigs ids (which would silently corrupt the id-keyed order/hidden state).
export function buildImportedDashboardCharts(dashboards: ImportedDashboardResult[]): ChartConfig[] {
  const seen = new Set<string>()
  const charts: ChartConfig[] = []
  dashboards.forEach(dashboard => {
    dashboard.panels.forEach(panel => {
      const id = `gdash_${dashboard.dashboard_id}_${panel.panel_id}`
      if (seen.has(id)) return
      seen.add(id)
		const titleParts = [dashboard.dashboard_title, panel.row_title, panel.title]
			.map(part => part?.trim())
			.filter((part, index, parts): part is string => Boolean(part) && parts.indexOf(part) === index)
		const chartTitle = titleParts.join(' · ') || panel.title
      const importedPanel: ImportedPanelRef = {
        dashboardId: dashboard.dashboard_id,
        dashboardTitle: dashboard.dashboard_title ?? '',
        panelId: panel.panel_id,
        kind: panel.kind,
      }
      if (panel.kind === 'single_value') {
        charts.push({
          id,
			title: chartTitle,
          type: 'stat-tile',
          importedPanel,
          statValue: { value: panel.value, unit: panel.unit, error: panel.error },
        })
        return
      }
		const graph: PrometheusGraph = { id, title: chartTitle, unit: panel.unit, series: panel.series ?? [] }
      charts.push({
        id,
		title: chartTitle,
        type: 'line-prometheus',
        defaultMetricKeys: graph.series.map(series => series.key),
        prometheusGraph: graph,
        importedPanel,
        importedError: panel.error,
      })
    })
  })
  return charts
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
  { key: 'netErrorsPerSec', label: 'Net Errors', color: 'var(--chart-7)', unit: 'errors/s' },
  { key: 'tcpRetransmitsPerSec', label: 'TCP Retransmits', color: 'var(--chart-8)', unit: 'retrans/s' },
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
  if (isResponsePercentileChart(chart)) {
    return `${responsePercentileLabel(chart.percentileKey)} response time (ms)`
  }
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
      return 'Fleet CPU (%)'
    case 'mem':
      return 'Fleet memory (MB)'
    case 'io':
      return 'Throughput (KB/s)'
    case 'tx_network_phases':
      return 'Phase time (ms)'
    case 'error_taxonomy':
      return 'Errors / interval'
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

// presetExemptChartIds returns the ids of charts built dynamically at runtime
// (the Prometheus pack; imported dashboard panels once they are wired in). Monitor
// presets curate only the *static* chartConfigs list, so chartStateForMonitorPreset
// would classify every dynamic chart as "remaining" and hide it. These charts
// default visible instead — the same exemption loadImpactDefaultVisibleChartIds
// already applies to load-impact charts.
export function presetExemptChartIds(charts: ChartConfig[]): string[] {
  return charts.filter(chart => isPrometheusChart(chart) || isImportedDashboardChart(chart)).map(chart => chart.id)
}

export const monitorPresets = [
  { id: 'run_operator', label: 'Run Operator', chartIds: ['vusers', 'tps', 'latency', 'errors', 'error_taxonomy'] },
  { id: 'performance_engineer', label: 'Performance Engineer', chartIds: ['latency', 'latency_p95', 'latency_p99', 'tps', 'errors', 'error_taxonomy', 'tx_network_phases', 'cpu', 'mem', 'io', 'generator_impact', 'network_latency', 'vusers'] },
  { id: 'platform_admin', label: 'Platform Admin', chartIds: ['cpu', 'mem', 'io', 'tx_network_phases', 'generator_impact', 'network_latency', 'vusers', 'tps'] },
  { id: 'release_approver', label: 'Release Approver', chartIds: ['latency', 'latency_p95', 'errors', 'error_taxonomy', 'tps'] },
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

// responsePercentileMetricKeys mirrors the aggregate/group/transaction shape the
// TPS and Error Rate graphs use, for a single percentile.
export function responsePercentileMetricKeys(
  percentileKey: ResponsePercentileKey,
  groupNames: string[],
  transactionNames: string[] = [],
) {
  return [
    percentileKey,
    ...groupNames.map(name => `${name}_${percentileKey}`),
    ...transactionNames.map(name => transactionChartSeriesKey(name, percentileKey)),
  ]
}

function responsePercentileMetricRows(
  chart: ChartConfig & { percentileKey: ResponsePercentileKey },
  groupNames: string[],
  groupColors: string[],
  transactionNames: string[],
): ChartMetricRow[] {
  const percentileKey = chart.percentileKey
  return [
    { key: percentileKey, label: 'Total', color: 'var(--text-muted)', unit: 'ms' },
    ...groupNames.map((name, index) => ({
      key: `${name}_${percentileKey}`,
      label: name,
      color: groupColors[index % groupColors.length],
      unit: 'ms',
      strokeDasharray: groupSeriesDasharray(index, groupColors.length),
    })),
    ...transactionNames.map(name => {
      const presentation = transactionChartSeriesPresentation(name)
      return {
        key: transactionChartSeriesKey(name, percentileKey),
        label: `Transaction · ${name}`,
        color: presentation.color,
        unit: 'ms',
        kind: 'transaction' as const,
        strokeDasharray: presentation.strokeDasharray,
      }
    }),
  ]
}

// responseTimeMetricKeys lists every series the "remove network latency" mode
// adjusts: average response time plus each percentile, for the aggregate, the
// groups, and the transactions.
function responseTimeMetricKeys(groupNames: string[], transactionNames: string[] = []) {
  return [
    'avgMs',
    ...groupNames.map(name => `${name}_avgMs`),
    ...transactionNames.map(name => transactionChartSeriesKey(name, 'avgMs')),
    ...RESPONSE_PERCENTILE_KEYS.flatMap(percentileKey => responsePercentileMetricKeys(percentileKey, groupNames, transactionNames)),
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

function addEvenlySpacedIndexes(retainedIndexes: Set<number>, dataLength: number, target: number) {
  const lastIndex = dataLength - 1
  for (let index = 0; index < target && retainedIndexes.size < target; index += 1) {
    retainedIndexes.add(Math.round(index * lastIndex / Math.max(1, target - 1)))
  }
}

// Reserve bounded first/last samples for each visible transaction series before
// composite downsampling. A low-volume transaction may not be a composite
// extrema in its time bucket, but dropping every one of its samples would leave
// a legend entry with no corresponding graph data.
function transactionSampleIndexes(data: MetricsHistoryPoint[], metricKeys: string[], budget: number) {
  const transactionKeys = Array.from(new Set(metricKeys.filter(isTransactionChartSeriesKey)))
    .slice(0, MAX_TRANSACTION_CHART_SERIES)
  if (transactionKeys.length === 0) return new Set<number>()

  const samplesByKey = new Map<string, { first: number; last: number }>()
  data.forEach((point, index) => {
    transactionKeys.forEach(key => {
      if (numericGraphValue(point, key) === undefined) return
      const samples = samplesByKey.get(key)
      if (samples) {
        samples.last = index
      } else {
        samplesByKey.set(key, { first: index, last: index })
      }
    })
  })

  const indexes = new Set<number>()
  // Every supported visible series gets one finite occurrence even when that
  // makes the result exceed the nominal point target. The transaction series
  // count is independently capped, so this remains bounded while avoiding a
  // legend entry whose entire line was removed by downsampling.
  transactionKeys.forEach(key => {
    const samples = samplesByKey.get(key)
    if (samples) indexes.add(samples.first)
  })
  const desiredSize = Math.max(indexes.size, Math.max(0, budget))
  // Spend any remaining nominal budget on a second occurrence per series.
  transactionKeys.forEach(key => {
    const samples = samplesByKey.get(key)
    if (indexes.size < desiredSize && samples && samples.last !== samples.first) indexes.add(samples.last)
  })
  return indexes
}

export function downsampleGraphData(data: MetricsHistoryPoint[], metricKeys: string[], maxPoints = DEFAULT_DOWNSAMPLED_GRAPH_POINTS) {
  const target = Math.max(3, Math.floor(maxPoints))
  if (data.length <= target) return data

  const uniqueMetricKeys = Array.from(new Set(metricKeys))
  const retainedIndexes = new Set<number>([0, data.length - 1])
  if (target < 8 || uniqueMetricKeys.length === 0) {
    transactionSampleIndexes(data, uniqueMetricKeys, Math.max(0, target - retainedIndexes.size))
      .forEach(index => retainedIndexes.add(index))
    addEvenlySpacedIndexes(retainedIndexes, data.length, target)
    return Array.from(retainedIndexes).sort((left, right) => left - right).map(index => data[index])
  }

  const ranges = metricRanges(data, uniqueMetricKeys)
  if (ranges.size === 0) {
    transactionSampleIndexes(data, uniqueMetricKeys, Math.max(0, target - retainedIndexes.size))
      .forEach(index => retainedIndexes.add(index))
    addEvenlySpacedIndexes(retainedIndexes, data.length, target)
    return Array.from(retainedIndexes).sort((left, right) => left - right).map(index => data[index])
  }

  // Keep gap-boundary spacer rows so missing observations survive downsampling.
  for (let index = 0; index < data.length; index += 1) {
    if (data[index].isGapBoundary) retainedIndexes.add(index)
  }
  transactionSampleIndexes(data, uniqueMetricKeys, Math.max(0, target - retainedIndexes.size))
    .forEach(index => retainedIndexes.add(index))
  const remainingCapacity = Math.max(0, target - retainedIndexes.size)
  if (remainingCapacity === 0) {
    return Array.from(retainedIndexes).sort((left, right) => left - right).map(index => data[index])
  }
  const bucketCount = Math.max(1, Math.floor(remainingCapacity / 4))
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

    for (const index of [start, lowIndex, highIndex, endExclusive - 1]) {
      if (retainedIndexes.size >= target) break
      retainedIndexes.add(index)
    }
  }

  addEvenlySpacedIndexes(retainedIndexes, data.length, target)

  return Array.from(retainedIndexes)
    .sort((left, right) => left - right)
    .map(index => data[index])
}

// --- Display granularity (time-bucket averaging) -------------------------------
// Resamples the live series into fixed-width time buckets, averaging each metric
// within a bucket (a moving "X-second/minute average"). Distinct from the
// extrema-preserving downsampler: granularity is a user-facing smoothing control.

export interface GranularityOption {
  value: number // seconds; 0 = Auto
  label: string
}

export const GRANULARITY_OPTIONS: GranularityOption[] = [
  { value: 0, label: 'Auto' },
  { value: 1, label: '1s' },
  { value: 5, label: '5s' },
  { value: 15, label: '15s' },
  { value: 30, label: '30s' },
  { value: 60, label: '1m' },
  { value: 300, label: '5m' },
  { value: 900, label: '15m' },
]

const GRANULARITY_NICE_SECONDS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600]
const AUTO_GRANULARITY_TARGET_BUCKETS = 600
// Fields that describe a point's position or are flags, not averageable metrics.
const NON_AVERAGED_POINT_KEYS = new Set(['timestampMs', 'relativeMs', 'isGapBoundary', 'resourceSampleOnly'])

export function granularityOptionLabel(seconds: number): string {
  const known = GRANULARITY_OPTIONS.find(option => option.value === seconds)
  if (known) return known.label
  if (seconds > 0 && seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds > 0 && seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

// autoGranularitySeconds picks the smallest "nice" interval so the visible span
// fits within ~AUTO_GRANULARITY_TARGET_BUCKETS buckets — near-native for short
// windows, lightly coarser for long ones. Adapts to the current zoom because it
// reads the span of the (already time-filtered) points.
export function autoGranularitySeconds(points: MetricsHistoryPoint[]): number {
  const times = points
    .map(point => point.timestampMs)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (times.length < 2) return 1
  const spanSeconds = (Math.max(...times) - Math.min(...times)) / 1000
  if (spanSeconds <= 0) return 1
  const target = spanSeconds / AUTO_GRANULARITY_TARGET_BUCKETS
  return GRANULARITY_NICE_SECONDS.find(nice => nice >= target) ?? GRANULARITY_NICE_SECONDS[GRANULARITY_NICE_SECONDS.length - 1]
}

// aggregateByGranularity averages consecutive points that fall in the same
// [t0, t0+bucket) window. Gap-boundary spacer rows are preserved (and flush the
// current bucket) so missing observations survive. Returns the input unchanged for bucket≤0.
export function aggregateByGranularity(points: MetricsHistoryPoint[], bucketSeconds: number): MetricsHistoryPoint[] {
  if (bucketSeconds <= 0 || points.length === 0) return points
  const bucketMs = bucketSeconds * 1000
  const result: MetricsHistoryPoint[] = []
  let bucketKey: number | null = null
  let group: MetricsHistoryPoint[] = []

  const flush = () => {
    if (group.length === 0) return
    if (group.length === 1) {
      result.push(group[0])
      group = []
      return
    }
    const sums = new Map<string, { total: number; count: number }>()
    for (const point of group) {
      for (const [key, value] of Object.entries(point)) {
        if (NON_AVERAGED_POINT_KEYS.has(key)) continue
        if (typeof value === 'number' && Number.isFinite(value)) {
          const agg = sums.get(key) ?? { total: 0, count: 0 }
          agg.total += value
          agg.count += 1
          sums.set(key, agg)
        }
      }
    }
    // Anchor on the first point so position fields (time/timestampMs/relativeMs)
    // and any string fields are retained; overwrite only the averaged metrics.
    const merged: MetricsHistoryPoint = { ...group[0] }
    sums.forEach((agg, key) => {
      merged[key] = agg.total / agg.count
    })
    result.push(merged)
    group = []
  }

  for (const point of points) {
    if (point.isGapBoundary) {
      flush()
      result.push(point)
      bucketKey = null
      continue
    }
    const ts = point.timestampMs
    const key: number | null = typeof ts === 'number' ? Math.floor(ts / bucketMs) : bucketKey
    if (bucketKey === null || key !== bucketKey) {
      flush()
      bucketKey = key
    }
    group.push(point)
  }
  flush()
  return result
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

export function responseTimeDataWithoutNetworkLatency(
  data: MetricsHistoryPoint[],
  latencyKeys: string[],
  groupNames: string[],
  transactionNames: string[] = [],
) {
  if (latencyKeys.length === 0) return data
  const responseKeys = responseTimeMetricKeys(groupNames, transactionNames)
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
  transactionNames: string[] = [],
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
    const loadImpactGroupNames = chart.loadImpactGroupNames ?? []
    keys = [
      ...(loadImpactGroupNames.length > 0 ? [] : ['average']),
      ...loadImpactGroupNames.map(loadImpactGroupSeriesKey),
      'min',
      'max',
    ]
    const overlays = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !keys.includes(key))
    return overlays.length > 0 ? [...keys, ...overlays] : keys
  }
  if (isResponsePercentileChart(chart)) {
    keys = responsePercentileMetricKeys(chart.percentileKey, groupNames, transactionNames)
    const overlays = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !keys.includes(key))
    return overlays.length > 0 ? [...keys, ...overlays] : keys
  }
  switch (chart.id) {
    case 'tps':
      keys = [
        chart.totalKey ?? 'tps',
        ...groupNames.map(name => `${name}_tps`),
        ...transactionNames.map(name => transactionChartSeriesKey(name, 'tps')),
      ]
      break
    case 'latency':
      keys = [
        'avgMs',
        ...groupNames.map(name => `${name}_avgMs`),
        ...transactionNames.map(name => transactionChartSeriesKey(name, 'avgMs')),
      ]
      break
    case 'errors':
      keys = [
        chart.totalKey ?? 'errorPct',
        ...groupNames.map(name => `${name}_errorPct`),
        ...transactionNames.map(name => transactionChartSeriesKey(name, 'errorPct')),
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
  transactionNames: string[] = [],
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
    const loadImpactGroupNames = chart.loadImpactGroupNames ?? []
    rows = [
      ...(loadImpactGroupNames.length > 0 ? [] : [{
        key: 'average',
        label: 'Average',
        color: chart.color ?? 'var(--chart-1)',
        unit,
      }]),
      // One line per selected group. Colors follow the group's palette slot from
      // the group list so a group reads the same on every graph.
      ...loadImpactGroupNames.map((name, fallbackIndex) => {
        const paletteIndex = groupNames.indexOf(name)
        const index = paletteIndex >= 0 ? paletteIndex : fallbackIndex
        return {
          key: loadImpactGroupSeriesKey(name),
          label: name,
          color: groupColors[index % groupColors.length] ?? 'var(--chart-1)',
          unit,
          strokeDasharray: groupSeriesDasharray(index, groupColors.length),
        }
      }),
      { key: 'min', label: 'Min', color: 'var(--chart-3)', unit },
      { key: 'max', label: 'Max', color: 'var(--chart-4)', unit },
    ]
    const overlayRows = normalizeOverlayMetricKeys(chart, includeVuserOverlay, overlayMetricKeys)
      .filter(key => !rows.some(row => row.key === key))
      .map(key => overlayMetricRows[key])
    return overlayRows.length > 0 ? [...rows, ...overlayRows] : rows
  }
  if (isResponsePercentileChart(chart)) {
    rows = responsePercentileMetricRows(chart, groupNames, groupColors, transactionNames)
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
        ...groupNames.map((name, index) => ({
          key: `${name}_tps`,
          label: name,
          color: groupColors[index % groupColors.length],
          strokeDasharray: groupSeriesDasharray(index, groupColors.length),
        })),
        ...transactionNames.map(name => {
          const presentation = transactionChartSeriesPresentation(name)
          return {
            key: transactionChartSeriesKey(name, 'tps'),
            label: `Transaction · ${name}`,
            color: presentation.color,
            kind: 'transaction' as const,
            strokeDasharray: presentation.strokeDasharray,
          }
        }),
      ]
      break
    case 'latency':
      rows = [
        { key: 'avgMs', label: 'Total', color: 'var(--text-muted)', unit: 'ms', kind: 'transaction' },
        ...groupNames.map((name, index) => ({
          key: `${name}_avgMs`,
          label: name,
          color: groupColors[index % groupColors.length],
          unit: 'ms',
          kind: 'transaction' as const,
          strokeDasharray: groupSeriesDasharray(index, groupColors.length),
        })),
        ...transactionNames.map(name => {
          const presentation = transactionChartSeriesPresentation(name)
          return {
            key: transactionChartSeriesKey(name, 'avgMs'),
            label: `Transaction · ${name}`,
            color: presentation.color,
            unit: 'ms',
            kind: 'transaction' as const,
            strokeDasharray: presentation.strokeDasharray,
          }
        }),
      ]
      break
    case 'errors':
      rows = [
        { key: chart.totalKey ?? 'errorPct', label: 'Total', color: 'var(--text-muted)', unit: '%' },
        ...groupNames.map((name, index) => ({
          key: `${name}_errorPct`,
          label: name,
          color: groupColors[index % groupColors.length],
          unit: '%',
          strokeDasharray: groupSeriesDasharray(index, groupColors.length),
        })),
        ...transactionNames.map(name => {
          const presentation = transactionChartSeriesPresentation(name)
          return {
            key: transactionChartSeriesKey(name, 'errorPct'),
            label: `Transaction · ${name}`,
            color: presentation.color,
            unit: '%',
            kind: 'transaction' as const,
            strokeDasharray: presentation.strokeDasharray,
          }
        }),
      ]
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
    default: {
      const keyedUnit = chart.id === 'cpu'
        ? '%'
        : chart.id === 'tx_network_phases'
          ? 'ms'
          : chart.id === 'error_taxonomy'
            ? 'errors'
            : 'MB'
      rows = chart.keys
        ? chart.keys.map(key => ({ key: key.k, label: key.n, color: key.c, unit: keyedUnit }))
        : chart.dataKey
          ? [{ key: chart.dataKey, label: 'Total', color: chart.color ?? 'var(--chart-1)' }]
          : []
      break
    }
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
      strokeDasharray: row.strokeDasharray,
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
    strokeDasharray: row.strokeDasharray,
    strokeOpacity: 1,
  }
}

export function chartMetricSummary(data: MetricsHistoryPoint[], key: string): ChartMetricSummary {
  return chartMetricSummaries(data, [key])[key] ?? { samples: 0 }
}

// Builds every measurement-table summary in one history traversal. This avoids
// rescanning a 5k-point archive once per transaction series during each render.
export function chartMetricSummaries(data: MetricsHistoryPoint[], keys: ReadonlyArray<string>): Record<string, ChartMetricSummary> {
  const uniqueKeys = Array.from(new Set(keys))
  const accumulators = new Map<string, { latest: number; total: number; min: number; max: number; samples: number }>()

  data.forEach(point => {
    uniqueKeys.forEach(key => {
      const value = point[key]
      if (typeof value !== 'number' || !Number.isFinite(value)) return
      const current = accumulators.get(key)
      if (current) {
        current.latest = value
        current.total += value
        current.min = Math.min(current.min, value)
        current.max = Math.max(current.max, value)
        current.samples += 1
      } else {
        accumulators.set(key, { latest: value, total: value, min: value, max: value, samples: 1 })
      }
    })
  })

  return uniqueKeys.reduce<Record<string, ChartMetricSummary>>((summaries, key) => {
    const accumulator = accumulators.get(key)
    summaries[key] = accumulator
      ? {
          latest: accumulator.latest,
          average: accumulator.total / accumulator.samples,
          min: accumulator.min,
          max: accumulator.max,
          samples: accumulator.samples,
        }
      : { samples: 0 }
    return summaries
  }, {})
}

// Finds singleton transaction series in one pass and stops checking a series
// once its second finite sample is seen.
export function singletonTransactionMetricKeys(data: MetricsHistoryPoint[], metricKeys: ReadonlyArray<string>) {
  const unresolved = new Set(Array.from(new Set(metricKeys.filter(isTransactionChartSeriesKey))).slice(0, MAX_TRANSACTION_CHART_SERIES))
  const singletons = new Set<string>()
  for (const point of data) {
    if (unresolved.size === 0) break
    for (const key of unresolved) {
      const value = point[key]
      if (typeof value !== 'number' || !Number.isFinite(value)) continue
      if (singletons.has(key)) {
        singletons.delete(key)
        unresolved.delete(key)
      } else {
        singletons.add(key)
      }
    }
  }
  return singletons
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

function groupLoadPointsInBucket(row: LoadImpactRow | undefined, groupName: string, bucket: LoadImpactBucket) {
  return (row?.groupPoints?.[groupName] ?? []).filter(point => loadImpactBucketForVusers(point.vusers, [bucket]))
}

function averageLoadImpactPoints(points: LoadImpactPoint[]) {
  return points.length > 0 ? points.reduce((sum, point) => sum + point.value, 0) / points.length : undefined
}

function groupMetricAverageForBucket(
  row: LoadImpactRow | undefined,
  groupNames: string[],
  bucket: LoadImpactBucket,
) {
  return averageLoadImpactPoints(groupNames.flatMap(name => groupLoadPointsInBucket(row, name, bucket)))
}

function groupedLoadImpactChartData(
  row: LoadImpactRow,
  rows: LoadImpactRow[],
  groupNames: string[],
): MetricsHistoryPoint[] {
  const allGroupPoints = groupNames.flatMap(name => row.groupPoints?.[name] ?? [])
  const buckets = createLoadImpactBuckets(allGroupPoints.map(point => point.vusers))
  const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
  const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')

  return buckets.map((bucket): MetricsHistoryPoint => {
    const groupedValues = groupNames.reduce<Record<string, number | undefined>>((series, name) => {
      series[loadImpactGroupSeriesKey(name)] = averageLoadImpactPoints(groupLoadPointsInBucket(row, name, bucket))
      return series
    }, {})
    const metricPoints = groupNames.flatMap(name => groupLoadPointsInBucket(row, name, bucket))
    const values = metricPoints.map(point => point.value)
    const midpointVusers = bucket.minVusers + ((bucket.maxVusers - bucket.minVusers) / 2)
    return {
      ...groupedValues,
      time: String(midpointVusers),
      timestampMs: metricPoints[0]?.timestampMs,
      vusers: midpointVusers,
      tps: groupMetricAverageForBucket(tpsRow, groupNames, bucket) ?? 0,
      avgMs: groupMetricAverageForBucket(avgRow, groupNames, bucket) ?? 0,
      errorPct: 0,
      p50: 0,
      p90: 0,
      p99: 0,
      average: averageLoadImpactPoints(metricPoints),
      min: values.length > 0 ? Math.min(...values) : undefined,
      max: values.length > 0 ? Math.max(...values) : undefined,
      samples: values.length,
      bucketMinVusers: bucket.minVusers,
      bucketMaxVusers: bucket.maxVusers,
      sourceSamples: values.length,
    }
  })
}

export function loadImpactChartDataForRow(
  row: LoadImpactRow | undefined,
  rows: LoadImpactRow[],
  groupNames: string[] = [],
): MetricsHistoryPoint[] {
  if (!row) return []
  if (groupNames.some(name => (row.groupPoints?.[name]?.length ?? 0) > 0)) {
    return groupedLoadImpactChartData(row, rows, groupNames)
  }
  const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
  const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')
  return row.cells.map((cell): MetricsHistoryPoint => {
    const midpointVusers = cell.bucket.minVusers + ((cell.bucket.maxVusers - cell.bucket.minVusers) / 2)
    const bucketTime = String(midpointVusers)
    const groupSeries = groupNames.reduce<Record<string, number | undefined>>((series, name) => {
      series[loadImpactGroupSeriesKey(name)] = cell.groupAverages?.[name]
      return series
    }, {})
    return {
      ...groupSeries,
      time: bucketTime,
      timestampMs: cell.points[0]?.timestampMs,
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

export function loadImpactPercentileChartData(rows: LoadImpactRow[], groupNames: string[] = []): MetricsHistoryPoint[] {
  const percentileRows = new Map(LOAD_IMPACT_PERCENTILE_KEYS.map(key => [
    key,
    rows.find(row => row.metric.id === key),
  ]))
  const referenceRow = LOAD_IMPACT_PERCENTILE_KEYS
    .map(key => percentileRows.get(key))
    .find((row): row is LoadImpactRow => Boolean(row))
  if (!referenceRow) return []

  const percentileGroupNames = groupNames.filter(name => (referenceRow.groupPoints?.[name]?.length ?? 0) > 0)
  if (percentileGroupNames.length > 0) {
    const allGroupPoints = percentileGroupNames.flatMap(name => referenceRow.groupPoints?.[name] ?? [])
    const buckets = createLoadImpactBuckets(allGroupPoints.map(point => point.vusers))
    const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
    const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')
    return buckets.map((bucket): MetricsHistoryPoint => {
      const referencePoints = percentileGroupNames.flatMap(name => groupLoadPointsInBucket(referenceRow, name, bucket))
      const midpointVusers = bucket.minVusers + ((bucket.maxVusers - bucket.minVusers) / 2)
      return {
        time: bucket.label,
        timestampMs: referencePoints[0]?.timestampMs,
        vusers: midpointVusers,
        tps: groupMetricAverageForBucket(tpsRow, percentileGroupNames, bucket) ?? 0,
        avgMs: groupMetricAverageForBucket(avgRow, percentileGroupNames, bucket) ?? 0,
        errorPct: 0,
        p50: groupMetricAverageForBucket(percentileRows.get('p50'), percentileGroupNames, bucket) ?? 0,
        p90: groupMetricAverageForBucket(percentileRows.get('p90'), percentileGroupNames, bucket) ?? 0,
        p95: groupMetricAverageForBucket(percentileRows.get('p95'), percentileGroupNames, bucket) ?? 0,
        p99: groupMetricAverageForBucket(percentileRows.get('p99'), percentileGroupNames, bucket) ?? 0,
        bucketMinVusers: bucket.minVusers,
        bucketMaxVusers: bucket.maxVusers,
        sourceSamples: referencePoints.length,
      }
    })
  }

  const avgRow = rows.find(candidate => candidate.metric.id === 'avgMs')
  const tpsRow = rows.find(candidate => candidate.metric.id === 'tps')
  return referenceRow.cells.map((cell): MetricsHistoryPoint => {
    const midpointVusers = cell.bucket.minVusers + ((cell.bucket.maxVusers - cell.bucket.minVusers) / 2)
    return {
      time: cell.bucket.label,
      timestampMs: cell.points[0]?.timestampMs,
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
  transactionNames: string[] = [],
  selection?: { visibleKeys: readonly string[]; labels?: Record<string, string>; context?: Record<string, unknown> },
) {
  if (data.length > 100_000) throw new Error("Chart CSV export exceeds the row limit.")
  const firstColumn = isLoadImpactChart(chart) || isLoadImpactPercentilesChart(chart) ? 'load' : 'time'
  const keys = [firstColumn, ...chartMetricKeys(chart, groupNames, latencyKeys, includeVuserOverlay, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey, transactionNames).filter(key => !selection || selection.visibleKeys.includes(key))]
  if (keys.length + (selection?.context ? 1 : 0) > 256) throw new Error('Chart CSV export exceeds the column limit.')
  const encoder = new TextEncoder()
  let bytes = 0
  const rows: string[] = []
  const append = (line: string) => {
    bytes += encoder.encode(line).byteLength + 1
    if (bytes > 16 * 1024 * 1024) throw new Error('Chart CSV export exceeds the byte limit.')
    rows.push(line)
  }
  append([...keys.map(key => escapeCsvCell(selection?.labels?.[key] ?? key)), ...(selection?.context ? ['Selection'] : [])].join(','))
  for (const point of data) append([...keys.map(key => escapeCsvCell(key === firstColumn ? point.time : point[key])), ...(selection?.context ? [escapeCsvCell(JSON.stringify(selection.context))] : [])].join(','))
  return rows.join('\n')
}

function escapeCsvCell(value: unknown) {
  if (value === undefined || value === null || typeof value === 'number' && !Number.isFinite(value)) return ''
  let text = String(value)
  if (typeof value === 'string' && /^[\s\uFEFF]*[=+\-@]/.test(text)) text = `'${text}`
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
