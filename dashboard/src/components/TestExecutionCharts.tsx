import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent } from 'react'
import { createPortal } from 'react-dom'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ReferenceLine,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type MouseHandlerDataParam,
  type TooltipPayloadEntry,
  type YAxisTickContentProps,
} from 'recharts'

import { ImportedDashboardResult, LatencyTarget, LiveMetrics, MetricsHistoryPoint, NetworkLatency, PrometheusGraph } from '../types'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { downloadTextFile } from '../utils/fileUtils'
import { useModalDialog } from '../hooks/useModalDialog'
import { buildGeneratorImpactView, type GeneratorImpactVerdict } from './generatorImpact'
import LoadImpactHeatmap from './LoadImpactHeatmap'
import StatTile from './StatTile'
import PrometheusPanelCard from './PrometheusPanelCard'
import {
  buildLoadImpactModel,
  formatLoadImpactValue,
} from './loadImpactModel'
import { chartTooltipContentStyle, preserveChartItemOrder } from './chartTooltipTheme'
import {
  DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS,
  ART_STD_DEV_BAND_KEY,
  LOAD_IMPACT_VISIBLE_CHARTS_STORAGE_KEY,
  buildArtStdDevBandData,
  buildLoadImpactCharts,
  buildPrometheusCharts,
  hasLoadImpactPercentileRows,
  buildSmoothedGraphData,
  boundedChartItems,
  chartMetricLinePresentation,
  chartConfigs,
  chartMarginForOverlayKeys,
  chartMetricKeys,
  chartMetricRows,
  chartMetricSummaries,
  chartPrimaryYAxisLabel,
  chartStateForMonitorPreset,
  chartTitleWithOverlays,
  defaultHiddenChartIds,
  downsampleGraphData,
  downsampleTargetPointsForGraphDensity,
  formatGraphOverlayMetricValue,
  formatRelativeRunTime,
  graphCsvForChart,
  isLoadImpactChart,
  isLoadImpactGroupSeriesKey,
  isLoadImpactHeatmapChart,
  isLoadImpactPercentilesChart,
  isImportedDashboardChart,
  isOverlayMetricAvailable,
  isPrometheusChart,
  isResponsePercentileChart,
  isStatTileChart,
  buildImportedDashboardCharts,
  latestRouteLatencyValues,
  presetExemptChartIds,
  loadImpactHeatmapChart,
  loadImpactPercentilesChart,
  loadImpactChartDataForRow,
  loadImpactChartId,
  aggregateByGranularity,
  autoGranularitySeconds,
  granularityOptionLabel,
  GRANULARITY_OPTIONS,
  loadImpactPercentileChartData,
  MAX_CHART_LEGEND_ITEMS,
  MAX_CHART_TOOLTIP_ITEMS,
  normalizedChartOrder,
  overlayMetricMenuLabels,
  overlayMetricRows,
  prometheusChartData,
  rateOverlayAxisLabel,
  RESPONSE_PERCENTILE_KEYS,
  responseTimeDataWithoutNetworkLatency,
  routeLatencyModes,
  routeLatencySeriesKeys,
  sameStringArray,
  sameStringSet,
  sortMetricTableRows,
  rawGraphMetricKey,
  resolveSharedCursorTime,
  shouldRenderArtStdDevBands,
  smoothingRadiusForGraphDensity,
  smoothedGraphMetricKey,
  smoothedMetricKeysForChart,
  singletonTransactionMetricKeys,
  visibleMetricDomain,
  type ChartConfig,
  type ChartDensityMode,
  type ChartDomain,
  type ChartMetricRow,
  type ChartMetricSummary,
  type ChartTimeAxisMode,
  type MetricTableSort,
  type MetricTableSortKey,
  type OverlayMetricKey,
  type ResponsePercentileKey,
  type ResponseTimeNetworkMode,
  type RouteLatencyMode,
} from './testExecutionChartModel'
import {
  latencySeriesKeyForTarget,
  routeLatencyDetailSeriesKey,
  routeLatencySeriesLabel,
  routeLatencyTargetLabelFromKey,
} from './latencySeries'
import { transactionChartSeriesNames } from './transactionChartSeries'
import {
  executionChartMaxMinuteRange,
  executionChartMinMinuteRange,
  executionChartMinuteRangeValue,
  executionChartTimeRangeForMinutes,
  type ExecutionChartTimeRange,
  type ExecutionChartZoomAnchor,
} from './testExecutionChartZoomModel'
import {
  readStoredString,
  readStoredStringArray,
  readStoredStringArrayRecord,
  writeStoredJSON,
  writeStoredString,
} from '../utils/browserStorage'

interface Props {
  filteredData: MetricsHistoryPoint[]
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  onChartMouseDown: (anchor?: ExecutionChartZoomAnchor) => void
  onChartMouseMove: (anchor?: ExecutionChartZoomAnchor) => void
  onZoom: () => void
  onResetZoom: () => void
  showRefArea: boolean
  refAreaLeft: string | number
  refAreaEnd: string | number
  timeRange: ExecutionChartTimeRange
  onTimeRangeChange: (value: ExecutionChartTimeRange) => void
  isMultiGroup: boolean
  groupNames: string[]
  groupColors: string[]
  groupColorByName?: Readonly<Record<string, string>>
  showLoadImpact?: boolean
  loadImpactData?: MetricsHistoryPoint[]
  loadImpactLoading?: boolean
  loadImpactError?: string
  prometheusGraphs?: PrometheusGraph[]
  prometheusLoading?: boolean
  prometheusError?: string
  importedDashboards?: ImportedDashboardResult[]
  importedDashboardsLoading?: boolean
  importedDashboardsError?: string
  latestLatency?: NetworkLatency
  latestMetrics?: LiveMetrics
  surface?: 'execution' | 'command-center'
  storageScope?: string
  initialPresetId?: string
  defaultDensity?: ChartDensityMode
  activeCursor?: GraphCursorState | null
  onActiveCursorChange?: (cursor: GraphCursorState | null) => void
  enableGraphGroupTabs?: boolean
  activeGraphGroupTab?: GraphGroupTabId
  onActiveGraphGroupTabChange?: (tab: GraphGroupTabId) => void
  underLoadDisplayMode?: UnderLoadDisplayMode
  externalControlsTarget?: HTMLElement | null
  terminalStatus?: string
}

type ChartDropPosition = 'before' | 'after'
export type GraphGroupTabId = 'test_results' | 'under_load' | 'infrastructure' | 'prometheus' | 'imported'
export type UnderLoadDisplayMode = 'graphs' | 'heatmap'
type ChartCardSize = { width: number; height: number }
type ChartCardElement = HTMLElement | null
type AxisLabelPopover = { label: string; x: number; y: number } | null
type AxisInteractionEvent = {
  clientX?: number
  clientY?: number
  currentTarget?: EventTarget | null
  preventDefault?: () => void
  stopPropagation?: () => void
}

const EMPTY_PROMETHEUS_GRAPHS: PrometheusGraph[] = []
// A stable empty default is required (not a `[]` literal), or the useMemo that builds
// the imported charts re-runs on every render.
const EMPTY_IMPORTED_DASHBOARDS: ImportedDashboardResult[] = []

export interface GraphCursorState {
  label?: string | number
  // The hovered sample's timestamp. Charts resolve the crosshair by this (not by
  // a positional index) so it stays aligned across differently-downsampled charts.
  timestampMs?: number
}

function sameGraphCursor(left: GraphCursorState | null | undefined, right: GraphCursorState | null | undefined) {
  return (left?.label ?? undefined) === (right?.label ?? undefined)
    && (left?.timestampMs ?? undefined) === (right?.timestampMs ?? undefined)
}

const graphGroupTabs: Array<{ id: GraphGroupTabId; label: string }> = [
  { id: 'test_results', label: 'Live Metrics' },
  { id: 'under_load', label: 'Under Load' },
  { id: 'infrastructure', label: 'Infrastructure' },
  { id: 'prometheus', label: 'Prometheus' },
  { id: 'imported', label: 'Dashboards' },
]

const infrastructureChartIds = new Set(['cpu', 'mem', 'io', 'tx_network_phases', 'generator_impact', 'network_latency'])
const displayWindowAllSliderValue = executionChartMaxMinuteRange + 1

function displayWindowSliderPercent(value: number) {
  const clamped = Math.min(displayWindowAllSliderValue, Math.max(executionChartMinMinuteRange, value))
  const percent = (clamped - executionChartMinMinuteRange) / (displayWindowAllSliderValue - executionChartMinMinuteRange) * 100
  return `${Number(percent.toFixed(4))}%`
}

function graphGroupForChart(chart: ChartConfig): GraphGroupTabId {
  // Imported dashboard charts are also 'line-prometheus' (so isPrometheusChart is
  // true for them), so this check MUST come first or they'd land in the Prometheus tab.
  if (isImportedDashboardChart(chart)) return 'imported'
  if (isPrometheusChart(chart)) return 'prometheus'
  if (isLoadImpactChart(chart) || isLoadImpactHeatmapChart(chart) || isLoadImpactPercentilesChart(chart)) return 'under_load'
  if (infrastructureChartIds.has(chart.id)) return 'infrastructure'
  return 'test_results'
}

const PRIMARY_Y_AXIS_WIDTH = 60
const OVERLAY_Y_AXIS_WIDTH = 64
const COMMAND_CENTER_PRIMARY_Y_AXIS_WIDTH = 52
const COMMAND_CENTER_OVERLAY_Y_AXIS_WIDTH = 34

function readStoredTimeAxisMode(storageKey: string): ChartTimeAxisMode {
  return readStoredString(storageKey) === 'relative'
    ? 'relative'
    : 'clock'
}

function readStoredChartDensity(storageKey: string, fallback: ChartDensityMode = 'standard'): ChartDensityMode {
  const stored = readStoredString(storageKey)
  if (stored === 'six_up' || stored === 'command_center' || stored === 'standard') {
    return stored
  }
  return fallback
}

function readStoredResponseTimeNetworkMode(storageKey: string): ResponseTimeNetworkMode {
  return readStoredString(storageKey) === 'application'
    ? 'application'
    : 'end_to_end'
}

function readStoredRouteLatencyMode(storageKey: string): RouteLatencyMode {
  const stored = readStoredString(storageKey)
  return stored === 'phases' || stored === 'stability' ? stored : 'total'
}

function safeFileName(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '') || 'graph'
}

function chartGradientId(chart: ChartConfig, key: string, variant = 'fill') {
  return `chart-gradient-${safeFileName(`${chart.id}-${key}-${variant}`)}`
}

function chartGradientFill(chart: ChartConfig, key: string, variant = 'fill') {
  return `url(#${chartGradientId(chart, key, variant)})`
}

function resolveCssVariables(value: string) {
  const rootStyles = getComputedStyle(document.documentElement)
  return value.replace(/var\((--[^),\s]+)(?:,[^)]+)?\)/g, (_, variable: string) => rootStyles.getPropertyValue(variable).trim() || value)
}

function inlineSvgStyles(source: Element, clone: Element) {
  const sourceElements = [source, ...Array.from(source.querySelectorAll('*'))]
  const cloneElements = [clone, ...Array.from(clone.querySelectorAll('*'))]
  sourceElements.forEach((sourceElement, index) => {
    const cloneElement = cloneElements[index] as SVGElement | undefined
    if (!cloneElement) return
    const styles = getComputedStyle(sourceElement)
    ;['fill', 'stroke', 'color', 'font-family', 'font-size', 'font-weight', 'opacity'].forEach(property => {
      const value = styles.getPropertyValue(property)
      if (value && value !== 'none') {
        cloneElement.style.setProperty(property, value)
      }
    })
    ;['fill', 'stroke'].forEach(attribute => {
      const value = cloneElement.getAttribute(attribute)
      if (value?.includes('var(')) {
        cloneElement.setAttribute(attribute, resolveCssVariables(value))
      }
    })
  })
}

function chartSnapshotSvg(card: HTMLElement, chart: ChartConfig) {
  const svg = card.querySelector('svg')
  if (!svg) return null
  const clone = svg.cloneNode(true) as SVGSVGElement
  inlineSvgStyles(svg, clone)
  const rect = svg.getBoundingClientRect()
  const width = rect.width || Number(svg.getAttribute('width')) || 900
  const height = rect.height || Number(svg.getAttribute('height')) || 360
  clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  clone.setAttribute('width', String(width))
  clone.setAttribute('height', String(height))
  if (!clone.getAttribute('viewBox')) {
    clone.setAttribute('viewBox', `0 0 ${width} ${height}`)
  }
  const background = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
  background.setAttribute('x', '0')
  background.setAttribute('y', '0')
  background.setAttribute('width', '100%')
  background.setAttribute('height', '100%')
  background.setAttribute('fill', getComputedStyle(card).backgroundColor || '#ffffff')
  clone.insertBefore(background, clone.firstChild)

  const title = document.createElementNS('http://www.w3.org/2000/svg', 'title')
  title.textContent = chart.title
  clone.insertBefore(title, clone.firstChild)

  return new XMLSerializer().serializeToString(clone)
}

function latestGraphValuesText(
  chart: ChartConfig,
  latest: MetricsHistoryPoint,
  groupNames: string[],
  latencyKeys: string[],
  latencyLatestValues: Record<string, number | undefined>,
  overlayMetricKeys: OverlayMetricKey[] = [],
  routeLatencyMode: RouteLatencyMode = 'total',
  selectedRouteLatencyKey?: string,
  transactionNames: string[] = [],
) {
  const keys = chartMetricKeys(chart, groupNames, latencyKeys, false, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey, transactionNames)
  const labels = new Map(chartMetricRows(chart, groupNames, latencyKeys, [], false, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey, transactionNames).map(row => [row.key, row.label]))
  const lines = [`${chart.title}`]
  keys.forEach(key => {
    const value = chart.id === 'network_latency' ? latencyLatestValues[key] : latest[key]
    const label = chart.id === 'network_latency' ? routeLatencySeriesLabel(key) : labels.get(key) ?? key
    lines.push(`${label}: ${value ?? 'n/a'}`)
  })
  return lines.join('\n')
}

function formatMetricValue(value: number | undefined, unit?: string) {
  if (value === undefined || !Number.isFinite(value)) return 'n/a'
  const abs = Math.abs(value)
  const maximumFractionDigits = abs >= 100 ? 0 : abs >= 10 ? 1 : 2
  const formatted = value.toLocaleString(undefined, { maximumFractionDigits })
  if (!unit) return formatted
  return unit === '%' || unit === 'ms' ? `${formatted}${unit}` : `${formatted} ${unit}`
}

function routeLatencyTargetForKey(latency: NetworkLatency | undefined, key?: string): LatencyTarget | undefined {
  if (!latency?.targets?.length || !key) return undefined
  return latency.targets.find(target => latencySeriesKeyForTarget(target) === key)
}

function routeProbeStatusLabel(target: LatencyTarget) {
  if (target.http_status) return `HTTP ${target.http_status}`
  return target.last_success ? 'Reachable' : 'Failed'
}

function rawTooltipMetricValue(value: unknown, item: unknown) {
  const payload = (item as { payload?: MetricsHistoryPoint } | undefined)?.payload
  const dataKey = String((item as { dataKey?: unknown } | undefined)?.dataKey ?? '')
  const rawKey = rawGraphMetricKey(dataKey)
  const resolved = rawKey !== dataKey && payload
    ? payload[rawKey] ?? value
    : value
  return Array.isArray(resolved)
    ? resolved.map(itemValue => formatGraphOverlayMetricValue(itemValue)).join(' - ')
    : resolved
}

type ChartTooltipEntry = Pick<TooltipPayloadEntry, 'color' | 'dataKey' | 'name' | 'payload' | 'stroke' | 'value'>

export interface BoundedChartTooltipContentProps {
  active?: boolean
  payload?: ReadonlyArray<ChartTooltipEntry>
  label?: string | number
  showLabel?: boolean
}

export function BoundedChartTooltipContent({
  active,
  payload = [],
  label,
  showLabel = true,
}: BoundedChartTooltipContentProps) {
  if (!active) return null
  const presentEntries = payload.filter(entry => entry.value !== undefined && entry.value !== null)
  if (presentEntries.length === 0) return null
  const bounded = boundedChartItems(presentEntries, MAX_CHART_TOOLTIP_ITEMS)
  return (
    <div className="bounded-chart-tooltip" style={chartTooltipContentStyle}>
      {showLabel && label !== undefined && <div className="bounded-chart-tooltip-label">{String(label)}</div>}
      <div className="bounded-chart-tooltip-items">
        {bounded.items.map((entry, index) => {
          const name = String(entry.name ?? entry.dataKey ?? 'Series')
          const color = entry.color || (typeof entry.stroke === 'string' ? entry.stroke : 'var(--text-muted)')
          return (
            <div className="bounded-chart-tooltip-item" key={`${String(entry.dataKey ?? name)}-${index}`}>
              <span className="bounded-chart-tooltip-swatch" style={{ background: color }} />
              <span className="bounded-chart-tooltip-name">{name}</span>
              <strong>{formatGraphOverlayMetricValue(rawTooltipMetricValue(entry.value, entry))}</strong>
            </div>
          )
        })}
      </div>
      {bounded.omitted > 0 && (
        <div className="chart-tooltip-overflow" role="status">+{bounded.omitted} more visible series</div>
      )}
    </div>
  )
}

function generatorImpactVerdictLabel(verdict: GeneratorImpactVerdict) {
  switch (verdict) {
    case 'danger':
      return 'Likely impact'
    case 'warning':
      return 'Watch'
    case 'clear':
      return 'Clear'
    default:
      return 'n/a'
  }
}

function generatorImpactVerdictCopy(verdict: GeneratorImpactVerdict) {
  switch (verdict) {
    case 'danger':
      return 'Saturation overlaps run degradation'
    case 'warning':
      return 'Saturation signal without confirmed overlap'
    case 'clear':
      return 'No generator saturation signals'
    default:
      return 'Waiting for generator resource samples'
  }
}

export default function TestExecutionCharts({
  filteredData,
  expandedChart,
  onExpandedChartChange,
  onChartMouseDown,
  onChartMouseMove,
  onZoom,
  onResetZoom,
  showRefArea,
  refAreaLeft,
  refAreaEnd,
  timeRange,
  onTimeRangeChange,
  isMultiGroup,
  groupNames,
  groupColors,
  groupColorByName,
  showLoadImpact = true,
  loadImpactData = [],
  loadImpactLoading = false,
  loadImpactError = '',
  prometheusGraphs = EMPTY_PROMETHEUS_GRAPHS,
  prometheusLoading = false,
  prometheusError = '',
  importedDashboards = EMPTY_IMPORTED_DASHBOARDS,
  importedDashboardsLoading = false,
  importedDashboardsError = '',
  latestLatency,
  latestMetrics,
  surface = 'execution',
  storageScope = 'apexload.execution',
  initialPresetId,
  defaultDensity,
  activeCursor,
  onActiveCursorChange,
  enableGraphGroupTabs = false,
  activeGraphGroupTab: activeGraphGroupTabProp,
  onActiveGraphGroupTabChange,
  underLoadDisplayMode,
  externalControlsTarget = null,
  terminalStatus,
}: Props) {
  const densityFallback = defaultDensity ?? (surface === 'command-center' ? 'command_center' : 'standard')
  const storageKeyFor = (name: string) => `${storageScope}.${name}`
  const chartOrderStorageKey = storageKeyFor('chartOrder')
  const hiddenChartsStorageKey = storageKeyFor('hiddenCharts')
  const visibleLoadImpactStorageKey = storageScope === 'apexload.execution' ? LOAD_IMPACT_VISIBLE_CHARTS_STORAGE_KEY : storageKeyFor('visibleLoadImpactCharts')
  const vuserOverlayStorageKey = storageKeyFor('vuserOverlayCharts')
  const artOverlayStorageKey = storageKeyFor('artOverlayCharts')
  const tpsOverlayStorageKey = storageKeyFor('tpsOverlayCharts')
  const chartMetricFiltersStorageKey = storageKeyFor('chartMetricFilters')
  const timeAxisModeStorageKey = storageKeyFor('chartTimeAxisMode')
  const chartDensityStorageKey = storageKeyFor('chartDensity')
  const graphTableRowsStorageKey = storageKeyFor('graphTableRows')
  const cursorLabelsStorageKey = storageKeyFor('cursorLabels')
  const gradientUnderlaysStorageKey = storageKeyFor('gradientUnderlays')
  const downsampleChartsStorageKey = storageKeyFor('downsampleCharts')
  const responseTimeNetworkModeStorageKey = storageKeyFor('responseTimeNetworkMode')
  const artStdDevBandsStorageKey = storageKeyFor('artStdDevBands')
  const routeLatencyModeStorageKey = storageKeyFor('routeLatencyMode')
  const chartGranularityStorageKey = storageKeyFor('chartGranularity')
  const [hasStoredChartState] = useState(() => Boolean(readStoredString(chartOrderStorageKey) || readStoredString(hiddenChartsStorageKey)))
  const [initialPresetApplied, setInitialPresetApplied] = useState(false)
  const [localActiveCursor, setLocalActiveCursor] = useState<GraphCursorState | null>(null)
  const [chartOrder, setChartOrder] = useState<string[]>(() => readStoredStringArray(chartOrderStorageKey))
  const [hiddenChartIds, setHiddenChartIds] = useState<string[]>(() => readStoredStringArray(hiddenChartsStorageKey))
  const [visibleLoadImpactChartIds, setVisibleLoadImpactChartIds] = useState<string[]>(() => readStoredStringArray(visibleLoadImpactStorageKey))
  const [vuserOverlayChartIds, setVuserOverlayChartIds] = useState<string[]>(() => readStoredStringArray(vuserOverlayStorageKey))
  const [artOverlayChartIds, setArtOverlayChartIds] = useState<string[]>(() => readStoredStringArray(artOverlayStorageKey))
  const [tpsOverlayChartIds, setTpsOverlayChartIds] = useState<string[]>(() => readStoredStringArray(tpsOverlayStorageKey))
  const [chartMetricFilters, setChartMetricFilters] = useState<Record<string, string[]>>(() => readStoredStringArrayRecord(chartMetricFiltersStorageKey))
  const [timeAxisMode, setTimeAxisMode] = useState<ChartTimeAxisMode>(() => readStoredTimeAxisMode(timeAxisModeStorageKey))
  const [chartDensity, setChartDensity] = useState<ChartDensityMode>(() => {
    const storedDensity = readStoredChartDensity(chartDensityStorageKey, densityFallback)
    return surface === 'command-center' && storedDensity === 'six_up' ? 'command_center' : storedDensity
  })
  const [showGraphTableRows, setShowGraphTableRows] = useState(() => readStoredString(graphTableRowsStorageKey) === 'true')
  const [showCursorLabels, setShowCursorLabels] = useState(() => readStoredString(cursorLabelsStorageKey) !== 'false')
  const [showGradientUnderlays, setShowGradientUnderlays] = useState(() => readStoredString(gradientUnderlaysStorageKey) !== 'false')
  const [downsampleDenseCharts, setDownsampleDenseCharts] = useState(() => readStoredString(downsampleChartsStorageKey) !== 'false')
  const [responseTimeNetworkMode, setResponseTimeNetworkMode] = useState<ResponseTimeNetworkMode>(() => readStoredResponseTimeNetworkMode(responseTimeNetworkModeStorageKey))
  const [artStdDevBandsEnabled, setArtStdDevBandsEnabled] = useState(() => readStoredString(artStdDevBandsStorageKey) === 'true')
  const [routeLatencyMode, setRouteLatencyMode] = useState<RouteLatencyMode>(() => readStoredRouteLatencyMode(routeLatencyModeStorageKey))
  const [granularitySeconds, setGranularitySeconds] = useState<number>(() => {
    const stored = Number(readStoredString(chartGranularityStorageKey))
    return Number.isFinite(stored) && stored >= 0 ? stored : 0
  })
  const [tableSortByChart, setTableSortByChart] = useState<Record<string, MetricTableSort>>({})
  const [selectedMetricByChart, setSelectedMetricByChart] = useState<Record<string, string | null>>({})
  const [localActiveGraphGroupTab, setLocalActiveGraphGroupTab] = useState<GraphGroupTabId>('test_results')
  const [activeImportedDashboardId, setActiveImportedDashboardId] = useState('')
  const [selectedRouteLatencyKey, setSelectedRouteLatencyKey] = useState('')
  const [chartMenu, setChartMenu] = useState<{ chartId: string; x: number; y: number } | null>(null)
  const [chartActionError, setChartActionError] = useState('')
  const [axisLabelPopover, setAxisLabelPopover] = useState<AxisLabelPopover>(null)
  const [chartDrawerOpen, setChartDrawerOpen] = useState(false)
  const [hoveredChartId, setHoveredChartId] = useState<string | null>(null)
  const [draggingChartId, setDraggingChartId] = useState<string | null>(null)
  const [dragOverChartId, setDragOverChartId] = useState<string | null>(null)
  const [chartCardSizes, setChartCardSizes] = useState<Record<string, ChartCardSize>>({})
  const chartCardRefs = useRef<Record<string, ChartCardElement>>({})
  const chartMenuRef = useRef<HTMLDivElement | null>(null)
  const chartMenuFirstItemRef = useRef<HTMLButtonElement | null>(null)
  const chartMenuTriggerRef = useRef<HTMLElement | null>(null)
  const chartDrawerRef = useRef<HTMLElement | null>(null)
  const chartDrawerCloseButtonRef = useRef<HTMLButtonElement | null>(null)
  const axisLabelPopoverRef = useRef<HTMLDivElement | null>(null)
  const sharedCursorRef = useRef<GraphCursorState | null>(activeCursor ?? null)
  const pendingMouseMoveRef = useRef<{ zoomAnchor?: ExecutionChartZoomAnchor; cursor: GraphCursorState | null } | null>(null)
  const mouseMoveFrameRef = useRef<number | null>(null)
  const onChartMouseMoveRef = useRef(onChartMouseMove)
  // Per-chart memo cache for the expensive derived series (downsample + std-dev band + smoothing).
  // These derivations are recomputed inside displayedCharts.map() on every render, but renders also
  // fire on unrelated state changes (hover via setHoveredChartId, metrics ticks that leave a given
  // chart's inputs untouched). Caching keyed on a per-chart input signature keeps the work off the
  // hot path without restructuring this large component.
  const derivedSeriesCacheRef = useRef<
    Map<string, {
      signature: string
      sourceChartData: MetricsHistoryPoint[]
      downsampledChartData: MetricsHistoryPoint[]
      chartDataWithDisplayBands: MetricsHistoryPoint[]
      displayChartData: MetricsHistoryPoint[]
      metricSummaries: Record<string, ChartMetricSummary>
      singletonTransactionMetricKeys: Set<string>
    }>
  >(new Map())
  const commandCenterSurface = surface === 'command-center'
  // Group names arrive as a fresh array on every parent render, so memoize on a
  // signature instead of identity — the load-impact model is rebuilt from the
  // whole history and must not recompute on unrelated renders.
  const graphGroupNamesSignature = (isMultiGroup ? groupNames : []).join('\u001f')
  const graphGroupNames = useMemo(
    () => graphGroupNamesSignature ? graphGroupNamesSignature.split('\u001f') : [],
    [graphGroupNamesSignature],
  )
  const loadImpactSourceData = loadImpactData.length > 0 ? loadImpactData : filteredData
  const loadImpactModel = useMemo(
    () => buildLoadImpactModel(loadImpactSourceData, undefined, undefined, graphGroupNames),
    [graphGroupNames, loadImpactSourceData],
  )
  const loadImpactRows = loadImpactModel.rows
  const loadImpactRowsByChartId = useMemo(() => new Map(loadImpactRows.map(row => [loadImpactChartId(row.metric.id), row])), [loadImpactRows])
  const hasLoadImpactPercentiles = useMemo(() => hasLoadImpactPercentileRows(loadImpactRows), [loadImpactRows])
  const loadImpactPercentileData = useMemo(
    () => loadImpactPercentileChartData(loadImpactRows, graphGroupNames),
    [graphGroupNames, loadImpactRows],
  )
  const loadImpactCharts = useMemo(() => buildLoadImpactCharts(loadImpactRows, graphGroupNames), [graphGroupNames, loadImpactRows])
  const prometheusGraphCharts = useMemo(() => buildPrometheusCharts(prometheusGraphs), [prometheusGraphs])
  const importedDashboardCharts = useMemo(() => buildImportedDashboardCharts(importedDashboards), [importedDashboards])
  const importedDashboardTabs = useMemo(() => importedDashboards.filter(dashboard => dashboard.panels.length > 0), [importedDashboards])
  useEffect(() => {
    if (importedDashboardTabs.length === 0) { setActiveImportedDashboardId(''); return }
    if (!importedDashboardTabs.some(dashboard => dashboard.dashboard_id === activeImportedDashboardId)) setActiveImportedDashboardId(importedDashboardTabs[0].dashboard_id)
  }, [activeImportedDashboardId, importedDashboardTabs])
  const prometheusChartDataById = useMemo(
    () => new Map(
      [...prometheusGraphCharts, ...importedDashboardCharts]
        .filter(chart => chart.type === 'line-prometheus')
        .map(chart => [chart.id, prometheusChartData(chart.prometheusGraph)]),
    ),
    [prometheusGraphCharts, importedDashboardCharts],
  )
  const loadImpactGraphCharts = useMemo(() => (
    loadImpactRows.length > 0
      ? [
          loadImpactHeatmapChart,
          ...(hasLoadImpactPercentiles ? [loadImpactPercentilesChart] : []),
          ...loadImpactCharts,
        ]
      : loadImpactCharts
  ), [hasLoadImpactPercentiles, loadImpactCharts, loadImpactRows.length])
  const loadImpactDefaultVisibleChartIds = useMemo(() => loadImpactGraphCharts
    .filter(chart => (
      isLoadImpactHeatmapChart(chart)
      || (
        isLoadImpactChart(chart)
        && DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS.includes(chart.loadImpactMetric!.id)
      )
    ))
    .map(chart => chart.id), [loadImpactGraphCharts])
  const allChartConfigs = useMemo(() => [...chartConfigs, ...prometheusGraphCharts, ...importedDashboardCharts, ...loadImpactGraphCharts], [loadImpactGraphCharts, prometheusGraphCharts, importedDashboardCharts])
  const allChartIds = useMemo(() => allChartConfigs.map(chart => chart.id), [allChartConfigs])
  const allChartIdSignature = allChartIds.join('|')
  const visibleLoadImpactChartIdSignature = visibleLoadImpactChartIds.join('|')

  useEffect(() => {
    setChartOrder(prev => normalizedChartOrder(prev, allChartIds))
  }, [allChartIdSignature, allChartIds])

  useEffect(() => {
    if (!enableGraphGroupTabs || hasStoredChartState || loadImpactDefaultVisibleChartIds.length === 0) return
    const loadImpactIds = loadImpactDefaultVisibleChartIds
    setVisibleLoadImpactChartIds(prev => sameStringSet(prev, loadImpactIds) ? prev : loadImpactIds)
    setHiddenChartIds(prev => {
      const next = prev.filter(id => !loadImpactIds.includes(id))
      return sameStringArray(next, prev) ? prev : next
    })
  }, [enableGraphGroupTabs, hasStoredChartState, loadImpactDefaultVisibleChartIds])

  useEffect(() => {
    const knownChartIds = new Set(allChartIds)
    setHiddenChartIds(prev => {
      const retained = prev.filter(id => knownChartIds.has(id))
      const next = Array.from(new Set([...retained, ...defaultHiddenChartIds(allChartConfigs, visibleLoadImpactChartIds)]))
      return sameStringSet(next, prev) ? prev : next
    })
  }, [allChartConfigs, allChartIdSignature, allChartIds, visibleLoadImpactChartIdSignature, visibleLoadImpactChartIds])

  useEffect(() => {
    if (!initialPresetId || initialPresetApplied || hasStoredChartState || allChartIds.length === 0) return
    const next = chartStateForMonitorPreset(initialPresetId, allChartIds)
    // Dynamic charts (load-impact + Prometheus/imported pack) can never appear in a
    // static preset's chartIds, so keep them visible rather than letting the preset
    // hide them. Without the Prometheus/imported exemption the Graph Wall's Prometheus
    // and Dashboards tabs render an empty "0" count.
    const exemptIds = enableGraphGroupTabs ? [...loadImpactDefaultVisibleChartIds, ...presetExemptChartIds(allChartConfigs)] : []
    setChartOrder(next.chartOrder)
    setHiddenChartIds(exemptIds.length > 0 ? next.hiddenChartIds.filter(id => !exemptIds.includes(id)) : next.hiddenChartIds)
    setInitialPresetApplied(true)
  }, [allChartConfigs, allChartIds, enableGraphGroupTabs, hasStoredChartState, initialPresetApplied, initialPresetId, loadImpactDefaultVisibleChartIds])

  useEffect(() => {
    writeStoredJSON(chartOrderStorageKey, chartOrder)
  }, [chartOrder, chartOrderStorageKey])

  useEffect(() => {
    writeStoredJSON(hiddenChartsStorageKey, hiddenChartIds)
  }, [hiddenChartIds, hiddenChartsStorageKey])

  useEffect(() => {
    writeStoredJSON(visibleLoadImpactStorageKey, visibleLoadImpactChartIds)
  }, [visibleLoadImpactChartIds, visibleLoadImpactStorageKey])

  useEffect(() => {
    writeStoredJSON(vuserOverlayStorageKey, vuserOverlayChartIds)
  }, [vuserOverlayChartIds, vuserOverlayStorageKey])

  useEffect(() => {
    writeStoredJSON(artOverlayStorageKey, artOverlayChartIds)
  }, [artOverlayChartIds, artOverlayStorageKey])

  useEffect(() => {
    writeStoredJSON(tpsOverlayStorageKey, tpsOverlayChartIds)
  }, [tpsOverlayChartIds, tpsOverlayStorageKey])

  useEffect(() => {
    writeStoredJSON(chartMetricFiltersStorageKey, chartMetricFilters)
  }, [chartMetricFilters, chartMetricFiltersStorageKey])

  useEffect(() => {
    writeStoredString(timeAxisModeStorageKey, timeAxisMode)
  }, [timeAxisMode, timeAxisModeStorageKey])

  useEffect(() => {
    writeStoredString(chartDensityStorageKey, chartDensity)
  }, [chartDensity, chartDensityStorageKey])

  useEffect(() => {
    if (!commandCenterSurface) return
    writeStoredString(graphTableRowsStorageKey, showGraphTableRows ? 'true' : 'false')
  }, [commandCenterSurface, graphTableRowsStorageKey, showGraphTableRows])

  useEffect(() => {
    writeStoredString(cursorLabelsStorageKey, showCursorLabels ? 'true' : 'false')
  }, [cursorLabelsStorageKey, showCursorLabels])

  useEffect(() => {
    writeStoredString(gradientUnderlaysStorageKey, showGradientUnderlays ? 'true' : 'false')
  }, [gradientUnderlaysStorageKey, showGradientUnderlays])

  useEffect(() => {
    writeStoredString(downsampleChartsStorageKey, downsampleDenseCharts ? 'true' : 'false')
  }, [downsampleChartsStorageKey, downsampleDenseCharts])

  useEffect(() => {
    writeStoredString(chartGranularityStorageKey, String(granularitySeconds))
  }, [chartGranularityStorageKey, granularitySeconds])

  useEffect(() => {
    writeStoredString(responseTimeNetworkModeStorageKey, responseTimeNetworkMode)
  }, [responseTimeNetworkMode, responseTimeNetworkModeStorageKey])

  useEffect(() => {
    writeStoredString(artStdDevBandsStorageKey, artStdDevBandsEnabled ? 'true' : 'false')
  }, [artStdDevBandsEnabled, artStdDevBandsStorageKey])

  useEffect(() => {
    writeStoredString(routeLatencyModeStorageKey, routeLatencyMode)
  }, [routeLatencyMode, routeLatencyModeStorageKey])

  useEffect(() => {
    if (!chartMenu) return
    const restoreMenuTrigger = () => {
      const trigger = chartMenuTriggerRef.current
      if (trigger?.isConnected) window.setTimeout(() => trigger.focus(), 0)
    }
    const closeMenu = () => {
      setChartMenu(null)
      restoreMenuTrigger()
    }
    const closeMenuOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && chartMenuRef.current?.contains(target)) {
        return
      }
      closeMenu()
    }
    const closeMenuOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        closeMenu()
      }
    }
    const focusTimer = window.setTimeout(() => chartMenuFirstItemRef.current?.focus(), 0)
    window.addEventListener('pointerdown', closeMenuOnOutsidePointerDown)
    window.addEventListener('resize', closeMenu)
    window.addEventListener('keydown', closeMenuOnEscape)
    return () => {
      window.clearTimeout(focusTimer)
      window.removeEventListener('pointerdown', closeMenuOnOutsidePointerDown)
      window.removeEventListener('resize', closeMenu)
      window.removeEventListener('keydown', closeMenuOnEscape)
    }
  }, [chartMenu])

  useEffect(() => {
    if (!axisLabelPopover) return
    const closeAxisLabelPopover = () => setAxisLabelPopover(null)
    const closeAxisLabelPopoverOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && axisLabelPopoverRef.current?.contains(target)) {
        return
      }
      setAxisLabelPopover(null)
    }
    const closeAxisLabelPopoverOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setAxisLabelPopover(null)
      }
    }
    window.addEventListener('pointerdown', closeAxisLabelPopoverOnOutsidePointerDown)
    window.addEventListener('resize', closeAxisLabelPopover)
    window.addEventListener('keydown', closeAxisLabelPopoverOnEscape)
    return () => {
      window.removeEventListener('pointerdown', closeAxisLabelPopoverOnOutsidePointerDown)
      window.removeEventListener('resize', closeAxisLabelPopover)
      window.removeEventListener('keydown', closeAxisLabelPopoverOnEscape)
    }
  }, [axisLabelPopover])

  useModalDialog({
    dialogRef: chartDrawerRef,
    enabled: chartDrawerOpen,
    initialFocusRef: chartDrawerCloseButtonRef,
    onClose: () => setChartDrawerOpen(false),
  })

  const orderedCharts = useMemo(() => {
    const byId = new Map(allChartConfigs.map(chart => [chart.id, chart]))
    const ordered = chartOrder
      .map(id => byId.get(id))
      .filter((chart): chart is ChartConfig => Boolean(chart))
    return ordered.length ? ordered : allChartConfigs
  }, [allChartConfigs, chartOrder])
  const latencyKeys = useMemo(() => routeLatencySeriesKeys(filteredData), [filteredData])
  const transactionNames = useMemo(() => transactionChartSeriesNames(filteredData), [filteredData])
  // A percentile graph lists only the transactions that actually reported that
  // percentile, so an archived run whose interval summaries carry no
  // per-transaction percentiles never offers an empty line.
  const percentileTransactionNames = useMemo(() => (
    RESPONSE_PERCENTILE_KEYS.reduce((names, percentileKey) => {
      names[percentileKey] = transactionChartSeriesNames(filteredData, percentileKey)
      return names
    }, {} as Record<ResponsePercentileKey, string[]>)
  ), [filteredData])
  const transactionNamesForChart = (chart: ChartConfig) => (
    isResponsePercentileChart(chart) ? percentileTransactionNames[chart.percentileKey] : transactionNames
  )
  const activeRouteLatencyKey = latencyKeys.includes(selectedRouteLatencyKey)
    ? selectedRouteLatencyKey
    : latencyKeys[0]
  const latencyLatestValues = useMemo(() => latestRouteLatencyValues(filteredData, latencyKeys), [filteredData, latencyKeys])
  const responseTimeChartData = useMemo(
    () => responseTimeNetworkMode === 'application'
      ? responseTimeDataWithoutNetworkLatency(filteredData, latencyKeys, groupNames, transactionNames)
      : filteredData,
    [filteredData, groupNames, latencyKeys, responseTimeNetworkMode, transactionNames],
  )
  // Display granularity: averages the live series into time buckets (Auto adapts
  // to the visible span). Applied only to the time-series charts; load-impact and
  // Prometheus charts keep their own cadence.
  const granularityBucketSeconds = useMemo(
    () => (granularitySeconds > 0 ? granularitySeconds : autoGranularitySeconds(filteredData)),
    [granularitySeconds, filteredData],
  )
  const granularBaseData = useMemo(
    () => aggregateByGranularity(filteredData, granularityBucketSeconds),
    [filteredData, granularityBucketSeconds],
  )
  const granularResponseTimeData = useMemo(
    () => aggregateByGranularity(responseTimeChartData, granularityBucketSeconds),
    [responseTimeChartData, granularityBucketSeconds],
  )
  const relativeTimeByClockLabel = useMemo(() => {
    const labels = new Map<string, number>()
    filteredData.forEach(point => {
      if (typeof point.relativeMs === 'number') {
        labels.set(point.time, point.relativeMs)
      }
    })
    return labels
  }, [filteredData])
  const activeRouteLatencyTarget = useMemo(
    () => routeLatencyTargetForKey(latestLatency, activeRouteLatencyKey),
    [activeRouteLatencyKey, latestLatency],
  )
  const generatorImpactView = useMemo(
    () => buildGeneratorImpactView(latestMetrics, filteredData),
    [filteredData, latestMetrics],
  )

  useEffect(() => {
    if (latencyKeys.length === 0) {
      if (selectedRouteLatencyKey) setSelectedRouteLatencyKey('')
      return
    }
    if (!selectedRouteLatencyKey || !latencyKeys.includes(selectedRouteLatencyKey)) {
      setSelectedRouteLatencyKey(latencyKeys[0])
    }
  }, [latencyKeys, selectedRouteLatencyKey])

  const latest = filteredData[filteredData.length - 1] || loadImpactSourceData[loadImpactSourceData.length - 1] || {}
  const showPrometheusTab = prometheusGraphCharts.length > 0 || prometheusLoading || Boolean(prometheusError)
  const showImportedTab = importedDashboardCharts.length > 0 || importedDashboardsLoading || Boolean(importedDashboardsError)
  const visibleGraphGroupTabs = graphGroupTabs.filter(tab => {
    if (tab.id === 'prometheus') return showPrometheusTab
    if (tab.id === 'imported') return showImportedTab
    return true
  })
  const activeGraphGroupTab = activeGraphGroupTabProp ?? localActiveGraphGroupTab
  const updateActiveGraphGroupTab = useCallback((tab: GraphGroupTabId) => {
    if (activeGraphGroupTabProp === undefined) {
      setLocalActiveGraphGroupTab(tab)
    }
    onActiveGraphGroupTabChange?.(tab)
  }, [activeGraphGroupTabProp, onActiveGraphGroupTabChange])
  const onGranularityChange = useCallback((value: string) => {
    const parsed = Number(value)
    if (!Number.isFinite(parsed) || parsed < 0) return
    // Persist immediately so a polling-driven remount cannot restore a stale value.
    writeStoredString(chartGranularityStorageKey, String(parsed))
    setGranularitySeconds(parsed)
  }, [chartGranularityStorageKey])
  useEffect(() => {
    if (!enableGraphGroupTabs) return
    if (visibleGraphGroupTabs.some(tab => tab.id === activeGraphGroupTab)) return
    updateActiveGraphGroupTab('test_results')
  }, [activeGraphGroupTab, enableGraphGroupTabs, updateActiveGraphGroupTab, visibleGraphGroupTabs])
  const visibleCharts = orderedCharts.filter(chart => !hiddenChartIds.includes(chart.id))
  // Configure Graphs lists the cards belonging to the graph page in view. It
  // filters `orderedCharts` (not `visibleCharts`) so a hidden card on this page
  // is still listed and can be switched back on.
  const configurableCharts = enableGraphGroupTabs
    ? orderedCharts.filter(chart => graphGroupForChart(chart) === activeGraphGroupTab)
    : orderedCharts
  const configurableChartsScopeLabel = enableGraphGroupTabs
    ? graphGroupTabs.find(tab => tab.id === activeGraphGroupTab)?.label.toLowerCase() ?? ''
    : ''
  const tabbedCharts = enableGraphGroupTabs
    ? visibleCharts.filter(chart => graphGroupForChart(chart) === activeGraphGroupTab)
    : visibleCharts
  const dedicatedHeatmapMode = enableGraphGroupTabs && activeGraphGroupTab === 'under_load' && underLoadDisplayMode === 'heatmap'
  const graphModeWithoutHeatmap = enableGraphGroupTabs && activeGraphGroupTab === 'under_load' && underLoadDisplayMode === 'graphs'
  const unscopedDisplayModeCharts = dedicatedHeatmapMode
    ? orderedCharts.filter(isLoadImpactHeatmapChart).slice(0, 1)
    : graphModeWithoutHeatmap
      ? tabbedCharts.filter(chart => !isLoadImpactHeatmapChart(chart))
      : tabbedCharts
  const displayModeCharts = activeGraphGroupTab === 'imported' && activeImportedDashboardId
    ? unscopedDisplayModeCharts.filter(chart => chart.importedPanel?.dashboardId === activeImportedDashboardId)
    : unscopedDisplayModeCharts
  const displayedCharts = expandedChart
    ? [
        ...displayModeCharts.filter(chart => chart.id === expandedChart),
        ...displayModeCharts.filter(chart => chart.id !== expandedChart),
      ]
    : displayModeCharts
  const displayedChartIds = displayedCharts.map(chart => chart.id)
  const displayedChartIdSignature = displayedChartIds.join('|')

  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(entries => {
      setChartCardSizes(prev => {
        let changed = false
        const next = { ...prev }
        entries.forEach(entry => {
          const chartId = (entry.target as HTMLElement).dataset.chartId
          if (!chartId) return
          const width = Math.round(entry.contentRect.width)
          const height = Math.round(entry.contentRect.height)
          if (width <= 0 || height <= 0) return
          const current = prev[chartId]
          if (current?.width === width && current.height === height) return
          next[chartId] = { width, height }
          changed = true
        })
        return changed ? next : prev
      })
    })

    displayedChartIdSignature.split('|').filter(Boolean).forEach(chartId => {
      const element = chartCardRefs.current[chartId]
      if (element) observer.observe(element)
    })

    return () => observer.disconnect()
  }, [displayedChartIdSignature])
  const sharedCursor = activeCursor !== undefined ? activeCursor : localActiveCursor
  useEffect(() => {
    sharedCursorRef.current = sharedCursor
  }, [sharedCursor])

  useEffect(() => {
    onChartMouseMoveRef.current = onChartMouseMove
  }, [onChartMouseMove])

  useEffect(() => () => {
    if (mouseMoveFrameRef.current !== null && typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(mouseMoveFrameRef.current)
    }
  }, [])

  if (filteredData.length <= 1 && loadImpactCharts.length === 0 && prometheusGraphCharts.length === 0 && importedDashboardCharts.length === 0 && !loadImpactLoading && !loadImpactError && !prometheusLoading && !prometheusError && !importedDashboardsLoading && !importedDashboardsError) {
    return (
      <section className="charts-section">
        <div className="chart-empty-state" role="status" aria-live="polite">Awaiting metrics</div>
      </section>
    )
  }

  const contextChart = chartMenu ? displayModeCharts.find(chart => chart.id === chartMenu.chartId) : undefined
  const graphGroupCounts: Record<GraphGroupTabId, number> = { test_results: 0, under_load: 0, infrastructure: 0, prometheus: 0, imported: 0 }
  visibleCharts.forEach(chart => {
    graphGroupCounts[graphGroupForChart(chart)] += 1
  })
  const chartDataFor = (chart: ChartConfig) => {
    if (isStatTileChart(chart)) {
      return []
    }
    if (isLoadImpactHeatmapChart(chart)) {
      return loadImpactSourceData
    }
    if (isLoadImpactPercentilesChart(chart)) {
      return loadImpactPercentileData
    }
    if (isLoadImpactChart(chart)) {
      return loadImpactChartDataForRow(loadImpactRowsByChartId.get(chart.id), loadImpactRows, chart.loadImpactGroupNames ?? [])
    }
    if (isPrometheusChart(chart)) {
      return prometheusChartDataById.get(chart.id) ?? []
    }
    // The percentile graphs are response-time graphs too, so they follow the
    // end-to-end / application network-latency mode with the ART graph.
    return chart.id === 'latency' || isResponsePercentileChart(chart) ? granularResponseTimeData : granularBaseData
  }
  const updateSharedCursor = (cursor: GraphCursorState | null) => {
    if (sameGraphCursor(sharedCursorRef.current, cursor)) {
      if (onActiveCursorChange && sharedCursorRef.current !== cursor) {
        onActiveCursorChange(sharedCursorRef.current)
      }
      return
    }
    sharedCursorRef.current = cursor
    if (onActiveCursorChange) {
      onActiveCursorChange(cursor)
    } else {
      setLocalActiveCursor(cursor)
    }
  }

  const flushPendingMouseMove = () => {
    mouseMoveFrameRef.current = null
    const pending = pendingMouseMoveRef.current
    if (!pending) return
    pendingMouseMoveRef.current = null
    if (surface === 'command-center') {
      updateSharedCursor(pending.cursor)
    }
    onChartMouseMoveRef.current(pending.zoomAnchor)
  }

  const scheduleChartMouseMove = (zoomAnchor: ExecutionChartZoomAnchor | undefined, cursor: GraphCursorState | null) => {
    pendingMouseMoveRef.current = { zoomAnchor, cursor }
    if (mouseMoveFrameRef.current !== null) return
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
      flushPendingMouseMove()
      return
    }
    mouseMoveFrameRef.current = window.requestAnimationFrame(flushPendingMouseMove)
  }

  const handleChartMouseUp = () => {
    if (mouseMoveFrameRef.current !== null && typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(mouseMoveFrameRef.current)
    }
    // React queues both reducer dispatches from this event in order, so the
    // latest throttled drag anchor becomes part of the same zoom snapshot
    // before applySelection runs. Without this flush, a quick drag/release can
    // apply a degenerate or one-frame-stale range.
    flushPendingMouseMove()
    onZoom()
  }

  const cursorXForChart = (chartData: MetricsHistoryPoint[]) => resolveSharedCursorTime(chartData, sharedCursor)

  const renderSharedCursor = (chartData: MetricsHistoryPoint[], yAxisId?: string | number) => {
    if (surface !== 'command-center') return null
    const x = cursorXForChart(chartData)
    if (x === undefined) return null
    return (
      <ReferenceLine
        x={x}
        yAxisId={yAxisId}
        stroke="var(--accent)"
        strokeOpacity={0.78}
        strokeDasharray="4 3"
        ifOverflow="extendDomain"
      />
    )
  }

  const formatXAxisLabel = (value: string | number) => {
    if (timeAxisMode === 'clock') return String(value)
    const relativeMs = relativeTimeByClockLabel.get(String(value))
    return formatRelativeRunTime(relativeMs)
  }

  const renderTimeAxis = () => (
    <XAxis
      dataKey="time"
      stroke="var(--text-muted)"
      fontSize={11}
      height={46}
      tickMargin={8}
      tickFormatter={formatXAxisLabel}
    />
  )

  const renderLoadImpactAxis = () => (
    <XAxis
      dataKey="vusers"
      stroke="var(--text-muted)"
      fontSize={11}
      height={46}
      tickMargin={8}
      interval={0}
      minTickGap={8}
    />
  )

  const renderTooltip = (chartId: string) => {
    if (!showCursorLabels) {
      return <Tooltip content={() => null} itemSorter={preserveChartItemOrder} isAnimationActive={false} />
    }
    const showTimeLabel = showCursorLabels && hoveredChartId === chartId
    return (
      <Tooltip
        content={props => (
          <BoundedChartTooltipContent
            active={props.active}
            payload={props.payload}
            label={props.label === undefined ? undefined : formatXAxisLabel(props.label)}
            showLabel={showTimeLabel}
          />
        )}
        labelStyle={{ display: showTimeLabel ? undefined : 'none' }}
        labelFormatter={(value) => showTimeLabel ? formatXAxisLabel(value) : ''}
        formatter={(value, name, item) => [formatGraphOverlayMetricValue(rawTooltipMetricValue(value, item)), name]}
        itemSorter={preserveChartItemOrder}
        isAnimationActive={false}
      />
    )
  }

  const renderLoadImpactTooltip = (chart: ChartConfig) => {
    if (!showCursorLabels) {
      return <Tooltip content={() => null} itemSorter={preserveChartItemOrder} isAnimationActive={false} />
    }
    const showLabel = showCursorLabels && hoveredChartId === chart.id
    const rows = metricRowsForChart(chart)
    const rowUnits = new Map(rows.map(row => [row.key, row.unit]))
    return (
      <Tooltip
        contentStyle={{ ...chartTooltipContentStyle, borderRadius: 8 }}
        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: showLabel ? 4 : 0, display: showLabel ? undefined : 'none' }}
        labelFormatter={(value) => showLabel ? `Load: ${value}` : ''}
        formatter={(value, name, item) => {
          const dataKey = typeof (item as { dataKey?: unknown } | undefined)?.dataKey === 'string'
            ? String((item as { dataKey?: unknown }).dataKey)
            : ''
          return [formatLoadImpactValue(typeof value === 'number' ? value : undefined, rowUnits.get(dataKey)), name]
        }}
        itemSorter={preserveChartItemOrder}
        isAnimationActive={false}
      />
    )
  }

  const handleChartMouseMove = (
    chartId: string,
    event?: MouseHandlerDataParam,
    data?: ReadonlyArray<{ timestampMs?: number }>,
  ) => {
    setHoveredChartId(current => current === chartId ? current : chartId)
    const activeLabel = event?.activeLabel
    const activeIndex = typeof event?.activeTooltipIndex === 'number'
      ? event.activeTooltipIndex
      : typeof event?.activeTooltipIndex === 'string' && event.activeTooltipIndex.trim() !== ''
        ? Number(event.activeTooltipIndex)
        : Number.NaN
    const timestampMs = Number.isInteger(activeIndex) && activeIndex >= 0
      ? data?.[activeIndex]?.timestampMs
      : undefined
    const zoomAnchor = activeLabel !== undefined && typeof timestampMs === 'number'
      ? { label: activeLabel, timestampMs }
      : undefined
    let nextCursor: GraphCursorState | null = null
    if (surface === 'command-center') {
      // Recharts 3 exposes the hovered index but no activePayload on this chart
      // callback. Resolve the timestamp from the exact data array rendered by
      // this chart so differently-downsampled graphs still align by time.
      nextCursor = activeLabel !== undefined || typeof timestampMs === 'number'
        ? { label: activeLabel, timestampMs }
        : null
    }
    scheduleChartMouseMove(zoomAnchor, nextCursor)
  }

  const handleChartMouseDown = (
    event: MouseHandlerDataParam | undefined,
    data: ReadonlyArray<{ timestampMs?: number }>,
  ) => {
    const activeIndex = typeof event?.activeTooltipIndex === 'number'
      ? event.activeTooltipIndex
      : typeof event?.activeTooltipIndex === 'string' && event.activeTooltipIndex.trim() !== ''
        ? Number(event.activeTooltipIndex)
        : Number.NaN
    const timestampMs = Number.isInteger(activeIndex) && activeIndex >= 0
      ? data[activeIndex]?.timestampMs
      : undefined
    onChartMouseDown(event?.activeLabel !== undefined && typeof timestampMs === 'number'
      ? { label: event.activeLabel, timestampMs }
      : undefined)
  }

  const handleChartMouseLeave = (chartId: string) => {
    setHoveredChartId(current => current === chartId ? null : current)
    pendingMouseMoveRef.current = null
    if (mouseMoveFrameRef.current !== null && typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(mouseMoveFrameRef.current)
      mouseMoveFrameRef.current = null
    }
    if (surface === 'command-center') {
      updateSharedCursor(null)
    }
  }

  const toggleChartVisibility = (chartId: string) => {
    const chart = allChartConfigs.find(candidate => candidate.id === chartId)
    const togglingHiddenLoadImpactChart = (
      chart
      && (
        isLoadImpactPercentilesChart(chart)
        || (
          isLoadImpactChart(chart)
          && !DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS.includes(chart.loadImpactMetric!.id)
        )
      )
    )
    if (togglingHiddenLoadImpactChart) {
      const revealingChart = hiddenChartIds.includes(chartId)
      setVisibleLoadImpactChartIds(prev => {
        if (revealingChart) {
          return prev.includes(chartId) ? prev : [...prev, chartId]
        }
        return prev.filter(id => id !== chartId)
      })
    }
    setHiddenChartIds(prev =>
      prev.includes(chartId)
        ? prev.filter(id => id !== chartId)
        : [...prev, chartId],
    )
  }

  const overlayChartIdsForMetric = (key: OverlayMetricKey) => {
    if (key === 'vusers') return vuserOverlayChartIds
    if (key === 'avgMs') return artOverlayChartIds
    return tpsOverlayChartIds
  }

  const setOverlayChartIdsForMetric = (key: OverlayMetricKey, updater: (prev: string[]) => string[]) => {
    if (key === 'vusers') {
      setVuserOverlayChartIds(updater)
    } else if (key === 'avgMs') {
      setArtOverlayChartIds(updater)
    } else {
      setTpsOverlayChartIds(updater)
    }
  }

  const isOverlayMetricEnabled = (chart: ChartConfig, key: OverlayMetricKey) => {
    return isOverlayMetricAvailable(chart, key) && overlayChartIdsForMetric(key).includes(chart.id)
  }

  const overlayMetricKeysForChart = (chart: ChartConfig): OverlayMetricKey[] => {
    return (['vusers', 'avgMs', 'tps'] as OverlayMetricKey[]).filter(key => isOverlayMetricEnabled(chart, key))
  }

  const toggleOverlayMetric = (chart: ChartConfig, key: OverlayMetricKey) => {
    if (!isOverlayMetricAvailable(chart, key)) return
    const enabling = !overlayChartIdsForMetric(key).includes(chart.id)
    setOverlayChartIdsForMetric(key, prev =>
      prev.includes(chart.id)
        ? prev.filter(id => id !== chart.id)
        : [...prev, chart.id],
    )
    setChartMetricFilters(prev => {
      const storedKeys = prev[chart.id]
      if (enabling) {
        return storedKeys && !storedKeys.includes(key)
          ? { ...prev, [chart.id]: [...storedKeys, key] }
          : prev
      }
      if (!storedKeys?.includes(key)) return prev
      const nextKeys = storedKeys.filter(storedKey => storedKey !== key)
      return nextKeys.length > 0 ? { ...prev, [chart.id]: nextKeys } : prev
    })
  }

  const metricRowsForChart = (chart: ChartConfig) => chartMetricRows(
    chart,
    graphGroupNames,
    latencyKeys,
    groupColors,
    false,
    overlayMetricKeysForChart(chart),
    routeLatencyMode,
    activeRouteLatencyKey,
    transactionNamesForChart(chart),
  ).map(row => {
    const groupName = graphGroupNames.find(name => (
      row.label === name
      && (row.key.startsWith(`${name}_`) || isLoadImpactGroupSeriesKey(row.key))
    ))
    const groupColor = groupName ? groupColorByName?.[groupName] : undefined
    return groupColor ? { ...row, color: groupColor } : row
  })

  const visibleMetricKeysForChart = (chart: ChartConfig, rows = metricRowsForChart(chart)) => {
    const availableKeys = rows.map(row => row.key)
    const storedKeys = chartMetricFilters[chart.id]?.filter(key => availableKeys.includes(key)) ?? []
    const defaultKeys = chart.defaultMetricKeys?.filter(key => availableKeys.includes(key)) ?? []
    return storedKeys.length > 0 ? storedKeys : defaultKeys.length > 0 ? defaultKeys : availableKeys
  }

  const visibleOverlayMetricKeys = (chart: ChartConfig, visibleMetricKeys: string[]) => {
    const overlayKeys = overlayMetricKeysForChart(chart)
    return overlayKeys.filter(key => visibleMetricKeys.includes(key))
  }

  const metricYAxisId = (chart: ChartConfig, visibleMetricKeys: string[]) => visibleOverlayMetricKeys(chart, visibleMetricKeys).length > 0 ? 'metric' : undefined

  const showAllMetricRows = (chart: ChartConfig, rows: ChartMetricRow[]) => {
    setChartMetricFilters(prev => ({ ...prev, [chart.id]: rows.map(row => row.key) }))
  }

  const toggleMetricRow = (chart: ChartConfig, rows: ChartMetricRow[], key: string) => {
    const availableKeys = rows.map(row => row.key)
    const defaultKeys = chart.defaultMetricKeys?.filter(candidate => availableKeys.includes(candidate)) ?? []
    setChartMetricFilters(prev => {
      const currentKeys = prev[chart.id]?.filter(candidate => availableKeys.includes(candidate)) ?? (defaultKeys.length > 0 ? defaultKeys : availableKeys)
      const nextKeys = currentKeys.includes(key)
        ? currentKeys.filter(candidate => candidate !== key)
        : [...currentKeys, key]
      if (nextKeys.length === 0) return prev
      return { ...prev, [chart.id]: nextKeys }
    })
  }

  // Click a measurement-table header to sort by that column. First click on a
  // value column sorts descending (largest first); the name column sorts A→Z. A
  // third click on the active column clears the sort (natural order).
  const cycleTableSort = (chartId: string, key: MetricTableSortKey) => {
    const firstDirection = key === 'metric' ? 'asc' : 'desc'
    setTableSortByChart(prev => {
      const current = prev[chartId]
      if (!current || current.key !== key) {
        return { ...prev, [chartId]: { key, direction: firstDirection } }
      }
      if (current.direction === firstDirection) {
        return { ...prev, [chartId]: { key, direction: firstDirection === 'asc' ? 'desc' : 'asc' } }
      }
      const next = { ...prev }
      delete next[chartId]
      return next
    })
  }

  // Click a measurement row to highlight its line on the chart; click again to clear.
  const toggleSelectedMetric = (chartId: string, key: string) => {
    setSelectedMetricByChart(prev => ({ ...prev, [chartId]: prev[chartId] === key ? null : key }))
  }

  const openChartMenu = (event: MouseEvent<HTMLElement>, chartId: string) => {
    event.preventDefault()
    event.stopPropagation()
    chartMenuTriggerRef.current = event.currentTarget instanceof HTMLButtonElement ? event.currentTarget : null
    setAxisLabelPopover(null)
    const menuWidth = 224
    const menuHeight = 340
    const viewportWidth = window.innerWidth || 1024
    const viewportHeight = window.innerHeight || 768
    setChartMenu({
      chartId,
      x: Math.max(12, Math.min(event.clientX, viewportWidth - menuWidth - 12)),
      y: Math.max(12, Math.min(event.clientY, viewportHeight - menuHeight - 12)),
    })
  }

  const openChartMenuFromButton = (event: MouseEvent<HTMLButtonElement>, chartId: string) => {
    event.preventDefault()
    event.stopPropagation()
    chartMenuTriggerRef.current = event.currentTarget
    setAxisLabelPopover(null)
    const rect = event.currentTarget.getBoundingClientRect()
    const menuWidth = 224
    const menuHeight = 340
    const viewportWidth = window.innerWidth || 1024
    const viewportHeight = window.innerHeight || 768
    setChartMenu({
      chartId,
      x: Math.max(12, Math.min(rect.right - menuWidth, viewportWidth - menuWidth - 12)),
      y: Math.max(12, Math.min(rect.bottom + 4, viewportHeight - menuHeight - 12)),
    })
  }

  const openAxisLabelPopover = (event: AxisInteractionEvent | null, label: string) => {
    event?.preventDefault?.()
    event?.stopPropagation?.()
    const targetRect = event?.currentTarget instanceof Element
      ? event.currentTarget.getBoundingClientRect()
      : null
    const fallbackX = targetRect ? targetRect.left + targetRect.width / 2 : (window.innerWidth || 1024) / 2
    const fallbackY = targetRect ? targetRect.top + targetRect.height / 2 : (window.innerHeight || 768) / 2
    const clientX = typeof event?.clientX === 'number' && event.clientX > 0 ? event.clientX : fallbackX
    const clientY = typeof event?.clientY === 'number' && event.clientY > 0 ? event.clientY : fallbackY
    const popoverWidth = 196
    const popoverHeight = 78
    const viewportWidth = window.innerWidth || 1024
    const viewportHeight = window.innerHeight || 768
    setChartMenu(null)
    setAxisLabelPopover({
      label,
      x: Math.max(12, Math.min(clientX + 10, viewportWidth - popoverWidth - 12)),
      y: Math.max(12, Math.min(clientY - 12, viewportHeight - popoverHeight - 12)),
    })
  }

  const axisInteractionProps = (label: string) => ({
    tick: ({ x = 0, y = 0, index = -1, fill = 'var(--text-muted)', textAnchor = 'end', payload }: YAxisTickContentProps) => {
      const keyboardTarget = index === 0
      return (
        <text
          x={x}
          y={y}
          dy="0.32em"
          fill={fill}
          textAnchor={textAnchor}
          className="chart-y-axis-click-target"
          role={keyboardTarget ? 'button' : undefined}
          tabIndex={keyboardTarget ? 0 : undefined}
          aria-label={keyboardTarget ? `Show ${label} axis label` : undefined}
          onClick={event => openAxisLabelPopover(event, label)}
          onKeyDown={keyboardTarget ? (event: ReactKeyboardEvent<SVGTextElement>) => {
            if (event.key === 'Enter' || event.key === ' ') openAxisLabelPopover(event, label)
          } : undefined}
        >
          {payload?.value ?? ''}
        </text>
      )
    },
  })

  const runChartAction = async (action: () => void | Promise<void>, failureMessage = 'Could not complete the chart action') => {
    setChartActionError('')
    setChartMenu(null)
    const trigger = chartMenuTriggerRef.current
    if (trigger?.isConnected) window.setTimeout(() => trigger.focus(), 0)
    try {
      await action()
    } catch (cause: unknown) {
      const detail = cause instanceof Error && cause.message ? `: ${cause.message}` : ''
      setChartActionError(`${failureMessage}${detail}`)
    }
  }

  const displayTitleForChart = (chart: ChartConfig) => {
    const rows = metricRowsForChart(chart)
    const visibleMetricKeys = visibleMetricKeysForChart(chart, rows)
    return chartTitleWithOverlays(chart, visibleOverlayMetricKeys(chart, visibleMetricKeys))
  }

  const downloadChartSvgSnapshot = async (chart: ChartConfig) => {
    const card = chartCardRefs.current[chart.id]
    if (!card) {
      await downloadTextFile(latestGraphValuesText({ ...chart, title: displayTitleForChart(chart) }, latest, graphGroupNames, latencyKeys, latencyLatestValues, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey, transactionNamesForChart(chart)), `${safeFileName(displayTitleForChart(chart))}-values.txt`, 'text/plain;charset=utf-8')
      return
    }
    const displayTitle = displayTitleForChart(chart)
    const snapshot = chartSnapshotSvg(card, { ...chart, title: displayTitle })
    if (!snapshot) {
      await downloadTextFile(latestGraphValuesText({ ...chart, title: displayTitle }, latest, graphGroupNames, latencyKeys, latencyLatestValues, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey, transactionNamesForChart(chart)), `${safeFileName(displayTitle)}-values.txt`, 'text/plain;charset=utf-8')
      return
    }
    await downloadTextFile(snapshot, `${safeFileName(displayTitle)}-snapshot.svg`, 'image/svg+xml;charset=utf-8')
  }

  const exportChartCsv = async (chart: ChartConfig) => {
    await downloadTextFile(graphCsvForChart(chart, chartDataFor(chart), graphGroupNames, latencyKeys, false, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey, transactionNamesForChart(chart)), `${safeFileName(displayTitleForChart(chart))}-data.csv`, 'text/csv;charset=utf-8')
  }

  const copyLatestValues = async (chart: ChartConfig) => {
    const chartData = chartDataFor(chart)
    const chartLatest = chartData[chartData.length - 1] || latest
    const visibleKeys = chartMetricKeys(chart, graphGroupNames, latencyKeys, false, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey, transactionNamesForChart(chart))
    const routeLatestValues = chart.id === 'network_latency'
      ? latestRouteLatencyValues(chartData, visibleKeys)
      : latencyLatestValues
    const text = latestGraphValuesText({ ...chart, title: displayTitleForChart(chart) }, chartLatest, graphGroupNames, latencyKeys, routeLatestValues, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey, transactionNamesForChart(chart))
    if (!await copyTextToClipboard(text)) {
      await downloadTextFile(text, `${safeFileName(displayTitleForChart(chart))}-latest-values.txt`, 'text/plain;charset=utf-8')
    }
  }

  const chartDomIdFor = (chartId: string, suffix: string) => `${storageScope.replace(/[^a-zA-Z0-9_-]/g, '-')}-${chartId}-${suffix}`

  const chartAccessibleLabel = (chartTitle: string, visibleRows: ChartMetricRow[], sampleCount: number) => {
    const series = visibleRows.map(row => row.label).join(', ')
    return `${chartTitle} chart. ${sampleCount} samples.${series ? ` Visible series: ${series}.` : ''}`
  }

  const reorderChart = (sourceChartId: string, targetChartId: string, position: ChartDropPosition) => {
    if (sourceChartId === targetChartId) return
    setChartOrder(prev => {
      const next = normalizedChartOrder(prev, allChartIds)
      const sourceIndex = next.indexOf(sourceChartId)
      const targetIndex = next.indexOf(targetChartId)
      if (sourceIndex === -1 || targetIndex === -1) return prev
      const [movedChartId] = next.splice(sourceIndex, 1)
      const insertionIndex = next.indexOf(targetChartId) + (position === 'after' ? 1 : 0)
      next.splice(insertionIndex, 0, movedChartId)
      return next
    })
  }

  const moveChartByOffset = (chartId: string, offset: number) => {
    setChartOrder(prev => {
      const next = normalizedChartOrder(prev, allChartIds)
      const sourceIndex = next.indexOf(chartId)
      if (sourceIndex === -1) return prev
      const targetIndex = Math.max(0, Math.min(next.length - 1, sourceIndex + offset))
      if (targetIndex === sourceIndex) return prev
      const [movedChartId] = next.splice(sourceIndex, 1)
      next.splice(targetIndex, 0, movedChartId)
      return next
    })
  }

  const moveChartToBoundary = (chartId: string, boundary: 'start' | 'end') => {
    setChartOrder(prev => {
      const next = normalizedChartOrder(prev, allChartIds)
      const sourceIndex = next.indexOf(chartId)
      if (sourceIndex === -1) return prev
      const targetIndex = boundary === 'start' ? 0 : next.length - 1
      if (sourceIndex === targetIndex) return prev
      const [movedChartId] = next.splice(sourceIndex, 1)
      next.splice(targetIndex, 0, movedChartId)
      return next
    })
  }

  const handleChartDragStart = (event: DragEvent<HTMLButtonElement>, chartId: string) => {
    event.stopPropagation()
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', chartId)
    setDraggingChartId(chartId)
    setDragOverChartId(null)
  }

  const handleChartDragOver = (event: DragEvent<HTMLDivElement>, chartId: string) => {
    const sourceChartId = draggingChartId || event.dataTransfer.getData('text/plain')
    if (!sourceChartId || sourceChartId === chartId) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
    setDragOverChartId(current => current === chartId ? current : chartId)
  }

  const handleChartDrop = (event: DragEvent<HTMLDivElement>, chartId: string) => {
    const sourceChartId = draggingChartId || event.dataTransfer.getData('text/plain')
    if (!sourceChartId || sourceChartId === chartId) {
      setDraggingChartId(null)
      setDragOverChartId(null)
      return
    }
    event.preventDefault()
    const rect = chartCardRefs.current[chartId]?.getBoundingClientRect()
    const dropAfterTarget = rect
      ? event.clientY > rect.top + rect.height / 2 || event.clientX > rect.left + rect.width / 2
      : false
    reorderChart(sourceChartId, chartId, dropAfterTarget ? 'after' : 'before')
    setDraggingChartId(null)
    setDragOverChartId(null)
  }

  const handleChartDragEnd = () => {
    setDraggingChartId(null)
    setDragOverChartId(null)
  }

  const handleChartReorderKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, chartId: string) => {
    switch (event.key) {
      case 'ArrowLeft':
      case 'ArrowUp':
        event.preventDefault()
        moveChartByOffset(chartId, -1)
        break
      case 'ArrowRight':
      case 'ArrowDown':
        event.preventDefault()
        moveChartByOffset(chartId, 1)
        break
      case 'Home':
        event.preventDefault()
        moveChartToBoundary(chartId, 'start')
        break
      case 'End':
        event.preventDefault()
        moveChartToBoundary(chartId, 'end')
        break
      default:
        break
    }
  }

  const resetCharts = () => {
    const loadImpactIds = enableGraphGroupTabs ? loadImpactDefaultVisibleChartIds : []
    setVisibleLoadImpactChartIds(loadImpactIds)
    const exemptIds = enableGraphGroupTabs ? [...loadImpactIds, ...presetExemptChartIds(allChartConfigs)] : []
    if (initialPresetId) {
      const next = chartStateForMonitorPreset(initialPresetId, allChartIds)
      setChartOrder(next.chartOrder)
      setHiddenChartIds(exemptIds.length > 0 ? next.hiddenChartIds.filter(id => !exemptIds.includes(id)) : next.hiddenChartIds)
    } else {
      setChartOrder(allChartIds)
      setHiddenChartIds(defaultHiddenChartIds(allChartConfigs, loadImpactIds))
    }
    setChartDensity(densityFallback)
    setShowGradientUnderlays(true)
    setDownsampleDenseCharts(true)
    if (commandCenterSurface) {
      setShowGraphTableRows(false)
    }
  }

  const setResponseTimeNetworkLatencyIncluded = (included: boolean) => {
    setResponseTimeNetworkMode(included ? 'end_to_end' : 'application')
  }

  const renderSectionSubtext = (text: string) => commandCenterSurface ? null : <span>{text}</span>
  const displayWindowMinuteValue = executionChartMinuteRangeValue(timeRange)
  const displayWindowSliderValue = displayWindowMinuteValue ?? displayWindowAllSliderValue
  const displayWindowLabel = timeRange === 'custom'
    ? 'Custom'
    : displayWindowSliderValue >= displayWindowAllSliderValue
      ? 'All'
      : `${displayWindowSliderValue}m`
  const displayWindowSliderPosition = displayWindowSliderPercent(displayWindowSliderValue)
  const setDisplayWindowFromSlider = (value: string) => {
    const nextValue = Number(value)
    onTimeRangeChange(nextValue >= displayWindowAllSliderValue ? 'all' : executionChartTimeRangeForMinutes(nextValue))
  }

  const renderChartDisplayControlSections = () => (
    <>
      <div className="charts-config-panel-section">
        <div>
          <h4>Display Window</h4>
          {renderSectionSubtext('Graph data range')}
        </div>
        <div
          className="chart-window-slider-control"
          style={{ '--chart-window-slider-position': displayWindowSliderPosition } as CSSProperties}
        >
          <input
            type="range"
            min={executionChartMinMinuteRange}
            max={displayWindowAllSliderValue}
            step={1}
            value={displayWindowSliderValue}
            aria-label="Graph display window"
            aria-valuetext={displayWindowLabel}
            onChange={event => setDisplayWindowFromSlider(event.currentTarget.value)}
          />
          <div className="chart-window-slider-value" aria-live="polite">
            <span>{displayWindowLabel}</span>
          </div>
        </div>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>{commandCenterSurface ? 'Layout' : 'Zoom'}</h4>
          {renderSectionSubtext('Graph density')}
        </div>
        <div className="chart-layout-options">
          <div className="time-range-selector chart-segment-toggle" role="group" aria-label={commandCenterSurface ? 'Graph layout' : 'Graph density'}>
            <button
              className={`btn-chip ${chartDensity === 'standard' ? 'active' : ''}`}
              type="button"
              onClick={() => setChartDensity('standard')}
              aria-pressed={chartDensity === 'standard'}
            >
              {commandCenterSurface ? '2x2' : 'Standard'}
            </button>
            {commandCenterSurface ? (
              <button
                className={`btn-chip ${chartDensity === 'command_center' ? 'active' : ''}`}
                type="button"
                onClick={() => setChartDensity('command_center')}
                aria-pressed={chartDensity === 'command_center'}
              >
                3x3
              </button>
            ) : (
              <button
                className={`btn-chip ${chartDensity === 'six_up' ? 'active' : ''}`}
                type="button"
                onClick={() => setChartDensity('six_up')}
                aria-pressed={chartDensity === 'six_up'}
              >
                6 Graphs
              </button>
            )}
          </div>
          {commandCenterSurface && (
            <label className="chart-table-row-toggle">
              <input
                type="checkbox"
                checked={showGraphTableRows}
                aria-label="Show table rows beneath graphs"
                onChange={event => setShowGraphTableRows(event.currentTarget.checked)}
              />
              <span>Show table rows</span>
            </label>
          )}
        </div>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Granularity</h4>
          {renderSectionSubtext('Time-bucket averaging')}
        </div>
        <select
          className="chart-granularity-select"
          aria-label="Graph granularity"
          value={granularitySeconds}
          onChange={event => onGranularityChange(event.currentTarget.value)}
        >
          {GRANULARITY_OPTIONS.map(option => (
            <option key={option.value} value={option.value}>
              {option.value === 0 ? `Auto (${granularityOptionLabel(granularityBucketSeconds)})` : option.label}
            </option>
          ))}
        </select>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Display Time</h4>
          {renderSectionSubtext('Graph x-axis labels')}
        </div>
        <div className="time-range-selector chart-time-axis-toggle" role="group" aria-label="Graph time axis">
          <button
            className={`btn-chip ${timeAxisMode === 'clock' ? 'active' : ''}`}
            type="button"
            onClick={() => setTimeAxisMode('clock')}
            aria-pressed={timeAxisMode === 'clock'}
          >
            Clock Time
          </button>
          <button
            className={`btn-chip ${timeAxisMode === 'relative' ? 'active' : ''}`}
            type="button"
            onClick={() => setTimeAxisMode('relative')}
            aria-pressed={timeAxisMode === 'relative'}
          >
            Relative Time
          </button>
        </div>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Cursor Labels</h4>
          {renderSectionSubtext('Tooltip time/load labels')}
        </div>
        <label className="chart-table-row-toggle">
          <input
            type="checkbox"
            checked={showCursorLabels}
            aria-label="Show cursor labels on graphs"
            onChange={event => setShowCursorLabels(event.currentTarget.checked)}
          />
          <span>Show cursor labels</span>
        </label>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Rendering</h4>
          {renderSectionSubtext('Graph paint optimization')}
        </div>
        <div className="chart-rendering-options">
          <label className="chart-table-row-toggle">
            <input
              type="checkbox"
              checked={showGradientUnderlays}
              aria-label="Show gradient underlay on graphs"
              onChange={event => setShowGradientUnderlays(event.currentTarget.checked)}
            />
            <span>Gradient underlay</span>
          </label>
          <label className="chart-table-row-toggle">
            <input
              type="checkbox"
              checked={downsampleDenseCharts}
              aria-label="Downsample dense charts"
              onChange={event => setDownsampleDenseCharts(event.currentTarget.checked)}
            />
            <span>Downsample dense charts</span>
          </label>
        </div>
      </div>
    </>
  )

  const renderChartConfigurationPanel = () => (
    <div className="charts-config-panel">
      <div className="charts-config-panel-section">
        <div>
          <h4>Graph Cards</h4>
          <span>{configurableChartsScopeLabel ? `Add or remove ${configurableChartsScopeLabel} graph cards` : 'Add or remove graph cards'}</span>
        </div>
        <div className="charts-config-option-list">
          {configurableCharts.length === 0 && (
            <p className="charts-config-empty">No graph cards on this graph page.</p>
          )}
          {configurableCharts.map(chart => {
            const visible = !hiddenChartIds.includes(chart.id)
            return (
              <label key={chart.id} className="charts-config-option">
                <input
                  type="checkbox"
                  checked={visible}
                  aria-label={`Show ${chart.title}`}
                  onChange={() => toggleChartVisibility(chart.id)}
                />
                <span>
                  <strong>{chart.title}</strong>
                  <small>{visible ? 'Visible' : 'Hidden'}</small>
                </span>
              </label>
            )
          })}
        </div>
      </div>
      {!externalControlsTarget && renderChartDisplayControlSections()}
      <div className="charts-config-panel-section">
        <div>
          <h4>Reset</h4>
          <span>Default graph layout</span>
        </div>
        <button className="btn-chip chart-reset-pill" type="button" onClick={resetCharts}>
          Reset Graphs
        </button>
      </div>
    </div>
  )

  const renderOverlayAxes = (chart: ChartConfig, visibleMetricKeys: string[]) => {
    const visibleOverlayKeys = visibleOverlayMetricKeys(chart, visibleMetricKeys)
    const showVuserAxis = visibleOverlayKeys.includes('vusers')
    const rateAxisLabel = rateOverlayAxisLabel(visibleOverlayKeys)
    const overlayAxisWidth = commandCenterSurface ? COMMAND_CENTER_OVERLAY_Y_AXIS_WIDTH : OVERLAY_Y_AXIS_WIDTH
    return (
      <>
        {showVuserAxis && (
          <YAxis
            yAxisId="vusers"
            orientation="right"
            stroke={overlayMetricRows.vusers.color}
            fontSize={11}
            width={overlayAxisWidth}
            tickMargin={2}
            domain={[0, 'auto']}
            {...axisInteractionProps('VUsers')}
          />
        )}
        {rateAxisLabel && (
          <YAxis
            yAxisId="rateOverlay"
            orientation="right"
            stroke="var(--text-muted)"
            fontSize={11}
            width={overlayAxisWidth}
            tickMargin={2}
            domain={[0, 'auto']}
            {...axisInteractionProps(rateAxisLabel)}
          />
        )}
      </>
    )
  }

  const renderMetricAxis = (chart: ChartConfig, visibleMetricKeys: string[], domain?: ChartDomain) => {
    const label = chart.id === 'network_latency' && routeLatencyMode === 'stability' ? 'Jitter (ms)' : chartPrimaryYAxisLabel(chart)
    return (
      <YAxis
        yAxisId={metricYAxisId(chart, visibleMetricKeys)}
        stroke="var(--text-muted)"
        fontSize={11}
        width={commandCenterSurface ? COMMAND_CENTER_PRIMARY_Y_AXIS_WIDTH : PRIMARY_Y_AXIS_WIDTH}
        tickMargin={2}
        domain={domain}
        {...axisInteractionProps(label)}
      />
    )
  }

  const routeLatencySuccessRateKey = activeRouteLatencyKey
    ? routeLatencyDetailSeriesKey(activeRouteLatencyKey, 'successRate')
    : ''

  const renderRouteLatencySuccessAxis = (visibleMetricKeys: string[]) => (
    routeLatencyMode === 'stability' && visibleMetricKeys.includes(routeLatencySuccessRateKey) ? (
      <YAxis
        yAxisId="successRate"
        orientation="right"
        stroke="var(--chart-5)"
        fontSize={11}
        width={commandCenterSurface ? COMMAND_CENTER_OVERLAY_Y_AXIS_WIDTH : OVERLAY_Y_AXIS_WIDTH}
        tickMargin={2}
        domain={[0, 100]}
        {...axisInteractionProps('Success (%)')}
      />
    ) : null
  )

  const isGradientEligibleMetric = (chart: ChartConfig, row: ChartMetricRow, visibleMetricKeys: string[]) => {
    const visibleOverlayKeys = visibleOverlayMetricKeys(chart, visibleMetricKeys)
    if (visibleOverlayKeys.includes(row.key as OverlayMetricKey)) return false
    if (row.kind === 'percentile') return false
    if (row.key === 'targetTps') return false
    if (row.key === routeLatencySuccessRateKey || row.key.startsWith('latency_success_rate_') || row.key.endsWith('_successRate')) return false
    if (isLoadImpactHeatmapChart(chart)) return false
    if (isLoadImpactChart(chart) && row.key !== 'average') return false
    return true
  }

  const renderChartGradientDefs = (chart: ChartConfig, rows: ChartMetricRow[], visibleMetricKeys: string[], includeArtStdDevBand = false) => {
    const gradientRows = rows.filter(row => visibleMetricKeys.includes(row.key) && isGradientEligibleMetric(chart, row, visibleMetricKeys))
    if (gradientRows.length === 0 && !includeArtStdDevBand) return null

    return (
      <defs>
        {gradientRows.map(row => (
          <linearGradient key={`${row.key}-gradient`} id={chartGradientId(chart, row.key)} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={row.color} stopOpacity="0.24" />
            <stop offset="70%" stopColor={row.color} stopOpacity="0.08" />
            <stop offset="100%" stopColor={row.color} stopOpacity="0" />
          </linearGradient>
        ))}
        {includeArtStdDevBand && (
          <linearGradient id={chartGradientId(chart, ART_STD_DEV_BAND_KEY, 'band')} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--chart-4)" stopOpacity="0.18" />
            <stop offset="100%" stopColor="var(--chart-4)" stopOpacity="0.04" />
          </linearGradient>
        )}
      </defs>
    )
  }

  const renderGradientUnderlays = (
    chart: ChartConfig,
    rows: ChartMetricRow[],
    visibleMetricKeys: string[],
    displayMetricKey: (key: string) => string,
    yAxisId?: string | number,
  ) => rows
    .filter(row => visibleMetricKeys.includes(row.key) && isGradientEligibleMetric(chart, row, visibleMetricKeys))
    .map(row => (
      <Area
        key={`gradient-${row.key}`}
        yAxisId={yAxisId}
        type="monotone"
        dataKey={displayMetricKey(row.key)}
        name={row.label}
        stroke="none"
        fill={chartGradientFill(chart, row.key)}
        fillOpacity={1}
        dot={false}
        activeDot={false}
        connectNulls={false}
        legendType="none"
        tooltipType="none"
        isAnimationActive={false}
      />
    ))

  const renderArtStdDevBand = (chart: ChartConfig, visible: boolean, yAxisId?: string | number) => visible ? (
    <Area
      yAxisId={yAxisId}
      type="monotone"
      dataKey={ART_STD_DEV_BAND_KEY}
      name="±1σ"
      stroke="none"
      fill={chartGradientFill(chart, ART_STD_DEV_BAND_KEY, 'band')}
      fillOpacity={1}
      dot={false}
      activeDot={false}
      connectNulls={false}
      legendType="none"
      tooltipType="none"
      isAnimationActive={false}
    />
  ) : null

  // Bold overlay for the row selected in the measurements table. Drawn on top of
  // the normal (thinner) series so the chosen line reads as highlighted; keeps the
  // series' dash so its identity is preserved.
  const renderSelectedSeriesEmphasis = (
    chart: ChartConfig,
    metricRows: ChartMetricRow[],
    visibleMetricKeys: string[],
    displayMetricKey: (key: string) => string,
    yAxisId?: string | number,
  ) => {
    const selectedKey = selectedMetricByChart[chart.id]
    if (!selectedKey || !visibleMetricKeys.includes(selectedKey)) return null
    const row = metricRows.find(candidate => candidate.key === selectedKey)
    if (!row) return null
    const presentation = chartMetricLinePresentation(row)
    return (
      <Line
        yAxisId={yAxisId}
        type="monotone"
        dataKey={displayMetricKey(selectedKey)}
        stroke={row.color}
        strokeWidth={Math.max(3.5, presentation.strokeWidth + 2)}
        strokeDasharray={presentation.strokeDasharray}
        strokeOpacity={1}
        dot={false}
        activeDot={false}
        connectNulls={false}
        legendType="none"
        tooltipType="none"
        isAnimationActive={false}
      />
    )
  }

  const renderChartSeriesLegendItems = (items: ChartMetricRow[]) => items.map(row => {
    const presentation = chartMetricLinePresentation(row)
    return (
      <span key={row.key} className={`chart-series-legend-item ${presentation.role}${presentation.strokeDasharray ? ' dashed' : ''}`}>
        <span
          className="chart-series-swatch"
          style={{
            background: row.kind === 'percentile' ? undefined : row.color,
            borderColor: row.color,
          }}
        />
        <span>{row.label}</span>
      </span>
    )
  })

  const renderLegendOverflow = (omitted: number, label = 'series') => omitted > 0 ? (
    <span className="chart-series-legend-overflow" role="status">+{omitted} more visible {label}</span>
  ) : null

  const renderLineMultiSeriesLegend = (chart: ChartConfig, rows: ChartMetricRow[], visibleMetricKeys: string[]) => {
    const visibleRows = rows.filter(row => (
      visibleMetricKeys.includes(row.key)
      && !overlayMetricKeysForChart(chart).includes(row.key as OverlayMetricKey)
    ))
    if (visibleRows.length <= 1) return null
    const bounded = boundedChartItems(visibleRows, MAX_CHART_LEGEND_ITEMS)
    return (
      <div className="chart-series-legend" aria-label={`${chart.title} series legend`}>
        <div className="chart-series-legend-group">
          <span className="chart-series-legend-heading">Series</span>
          {renderChartSeriesLegendItems(bounded.items)}
          {renderLegendOverflow(bounded.omitted)}
        </div>
      </div>
    )
  }

  const renderLatencySeriesLegend = (rows: ChartMetricRow[], visibleMetricKeys: string[], includeArtStdDevBand: boolean) => {
    const visibleRows = rows.filter(row => visibleMetricKeys.includes(row.key))
    const transactionRows = visibleRows.filter(row => row.kind === 'transaction')
    const percentileRows = visibleRows.filter(row => row.kind === 'percentile')
    if (transactionRows.length === 0 && percentileRows.length === 0 && !includeArtStdDevBand) return null
    const boundedTransactions = boundedChartItems(transactionRows, MAX_CHART_LEGEND_ITEMS)

    return (
      <div className="chart-series-legend" aria-label="Response time series legend">
        {transactionRows.length > 0 && (
          <div className="chart-series-legend-group">
            <span className="chart-series-legend-heading">Transactions</span>
            {renderChartSeriesLegendItems(boundedTransactions.items)}
            {renderLegendOverflow(boundedTransactions.omitted, 'transaction series')}
            {includeArtStdDevBand && (
              <span className="chart-series-legend-item band">
                <span className="chart-series-swatch" />
                <span>±1σ</span>
              </span>
            )}
          </div>
        )}
        {percentileRows.length > 0 && (
          <div className="chart-series-legend-group percentiles">
            <span className="chart-series-legend-heading">Percentiles</span>
            {renderChartSeriesLegendItems(percentileRows)}
          </div>
        )}
      </div>
    )
  }

  const renderOverlayLines = (
    chart: ChartConfig,
    visibleMetricKeys: string[],
    displayMetricKey: (key: string) => string = key => key,
  ) => {
    const visibleOverlayKeys = visibleOverlayMetricKeys(chart, visibleMetricKeys)
    return visibleOverlayKeys.map(key => {
      const row = overlayMetricRows[key]
      return (
        <Line
          key={`overlay-${key}`}
          yAxisId={key === 'vusers' ? 'vusers' : 'rateOverlay'}
          type="monotone"
          dataKey={displayMetricKey(key)}
          name={row.label}
          stroke={row.color}
          dot={false}
          strokeWidth={1.75}
          strokeDasharray="5 3"
          isAnimationActive={false}
        />
      )
    })
  }

  const renderMetricsTable = (
    chart: ChartConfig,
    rows: ChartMetricRow[],
    visibleMetricKeys: string[],
    chartData: MetricsHistoryPoint[],
    summaries: Record<string, ChartMetricSummary>,
  ) => {
    if (rows.length === 0) {
      return (
        <div className="chart-metrics-empty">
          No measurements available
        </div>
      )
    }
    const canFilter = rows.length > 1
    const allVisible = visibleMetricKeys.length === rows.length
    const sort = tableSortByChart[chart.id]
    const sortedRows = sortMetricTableRows(rows, summaries, sort)
    const selectedKey = selectedMetricByChart[chart.id] ?? null
    const sortIndicator = (key: MetricTableSortKey) => (sort?.key === key ? (sort.direction === 'asc' ? ' ▲' : ' ▼') : '')
    const headerCell = (key: MetricTableSortKey, label: string) => (
      <th>
        <button
          type="button"
          className={`metric-sort-header${sort?.key === key ? ' active' : ''}`}
          onClick={() => cycleTableSort(chart.id, key)}
          aria-label={`Sort by ${label}`}
        >
          {label}{sortIndicator(key)}
        </button>
      </th>
    )
    return (
      <div className="chart-metrics-table-wrap">
        <div className="chart-metrics-table-toolbar">
          <span>Measurements</span>
          {canFilter && (
            <button className="btn btn-sm btn-ghost" type="button" disabled={allVisible} onClick={() => showAllMetricRows(chart, rows)}>
              All
            </button>
          )}
        </div>
        <table className="chart-metrics-table">
          <thead>
            <tr>
              {canFilter && <th className="metric-filter-col">Show</th>}
              {headerCell('metric', 'Metric')}
              {headerCell('latest', 'Latest')}
              {headerCell('average', 'Avg')}
              {headerCell('min', 'Min')}
              {headerCell('max', 'Max')}
            </tr>
          </thead>
          <tbody>
            {sortedRows.map(row => {
              const summary = summaries[row.key] ?? { samples: 0 }
              const visible = visibleMetricKeys.includes(row.key)
              const selected = selectedKey === row.key
              return (
                <tr
                  key={row.key}
                  className={`metric-row${visible ? '' : ' muted'}${selected ? ' selected' : ''}`}
                >
                  {canFilter && (
                    <td className="metric-filter-col" onClick={event => event.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={visible}
                        disabled={visible && visibleMetricKeys.length === 1}
                        aria-label={`Show ${row.label}`}
                        onChange={() => toggleMetricRow(chart, rows, row.key)}
                      />
                    </td>
                  )}
                  <td aria-label="Chart metric">
                    <button
                      type="button"
                      className="chart-metric-name"
                      aria-pressed={selected}
                      title="Highlight this line"
                      onClick={() => toggleSelectedMetric(chart.id, row.key)}
                    >
                      <span className="dot" style={{ background: row.color }}></span>
                      <span>{row.label}</span>
                    </button>
                  </td>
                  <td>{formatMetricValue(summary.latest, row.unit)}</td>
                  <td>{formatMetricValue(summary.average, row.unit)}</td>
                  <td>{formatMetricValue(summary.min, row.unit)}</td>
                  <td>{formatMetricValue(summary.max, row.unit)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    )
  }

  const renderAccessibleMetricsTable = (
    rows: ChartMetricRow[],
    visibleMetricKeys: string[],
    chartTitle: string,
    tableId: string,
    summaries: Record<string, ChartMetricSummary>,
  ) => {
    const visibleRows = rows.filter(row => visibleMetricKeys.includes(row.key))
    if (visibleRows.length === 0) return null
    return (
      <table id={tableId} className="chart-a11y-table">
        <caption>{chartTitle} measurements</caption>
        <thead>
          <tr>
            <th>Metric</th>
            <th>Latest</th>
            <th>Average</th>
            <th>Minimum</th>
            <th>Maximum</th>
          </tr>
        </thead>
        <tbody>
          {visibleRows.map(row => {
            const summary = summaries[row.key] ?? { samples: 0 }
            return (
              <tr key={row.key}>
                <th>{row.label}</th>
                <td>{formatMetricValue(summary.latest, row.unit)}</td>
                <td>{formatMetricValue(summary.average, row.unit)}</td>
                <td>{formatMetricValue(summary.min, row.unit)}</td>
                <td>{formatMetricValue(summary.max, row.unit)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    )
  }

  const renderRouteLatencyDetails = () => {
    if (!activeRouteLatencyKey) {
      return null
    }
    if (!activeRouteLatencyTarget) {
      return (
        <div className="route-latency-details route-latency-details-empty">
          Latest route details appear after the next latency probe.
        </div>
      )
    }
    const target = activeRouteLatencyTarget
    const detailRows = [
      { label: 'Target', value: routeLatencyTargetLabelFromKey(activeRouteLatencyKey) },
      { label: 'IP', value: target.ip || 'n/a' },
      { label: 'Probe', value: target.probe_method || 'tcp' },
      { label: 'Status', value: routeProbeStatusLabel(target) },
      { label: 'Route', value: target.route_status || 'unavailable' },
      { label: 'Hops', value: target.hops && target.hops > 0 ? String(target.hops) : 'n/a' },
      { label: 'Samples', value: target.sample_count ? String(target.sample_count) : 'n/a' },
      { label: 'Success', value: formatMetricValue(target.success_rate_pct, '%') },
      { label: 'Jitter', value: formatMetricValue(target.jitter_ms, 'ms') },
      { label: 'Last error', value: target.error || 'None' },
    ]
    return (
      <div className="route-latency-details" aria-label="Latest route latency details">
        <div className="route-latency-detail-chips">
          <span className={`route-latency-chip ${target.last_success ? 'ok' : 'alert'}`}>{routeProbeStatusLabel(target)}</span>
          <span className="route-latency-chip">{target.probe_method || 'tcp'}</span>
          <span className="route-latency-chip">{target.route_status || 'unavailable'}</span>
          <span className="route-latency-chip">{formatMetricValue(target.probe_total_ms || target.latency_ms, 'ms')}</span>
        </div>
        <table className="route-latency-detail-table">
          <tbody>
            {detailRows.map(row => (
              <tr key={row.label}>
                <th>{row.label}</th>
                <td>{row.value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  const renderGeneratorImpactDetails = () => {
    const view = generatorImpactView
    if (view.rows.length === 0) {
      return (
        <div className="generator-impact-details generator-impact-details-empty">
          Generator resource samples unavailable.
        </div>
      )
    }
    return (
      <div className="generator-impact-details" aria-label="Generator impact details">
        <div className="generator-impact-summary">
          <span className={`generator-impact-chip ${view.verdict}`}>{generatorImpactVerdictLabel(view.verdict)}</span>
          <span>{generatorImpactVerdictCopy(view.verdict)}</span>
        </div>
        <div className="generator-impact-table-wrap">
          <table className="generator-impact-table">
            <thead>
              <tr>
                <th>Generator</th>
                <th>VUsers</th>
                <th>TPS</th>
                <th>P95</th>
                <th>Errors</th>
                <th>Throttle</th>
                <th>Mem</th>
                <th>GC</th>
                <th>Net</th>
                <th>Disk</th>
                <th>Reasons</th>
              </tr>
            </thead>
            <tbody>
              {view.rows.map(row => {
                const netLoss = [row.metrics.netDropsPerSec, row.metrics.netErrorsPerSec, row.metrics.tcpRetransmitsPerSec]
                  .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
                  .reduce((sum, value) => sum + value, 0)
                const netLossValue = netLoss > 0 || row.metrics.netDropsPerSec !== undefined || row.metrics.netErrorsPerSec !== undefined || row.metrics.tcpRetransmitsPerSec !== undefined
                  ? netLoss
                  : undefined
                return (
                  <tr key={row.id}>
                    <td aria-label="Generator impact">
                      <span className="generator-impact-generator">
                        <span className={`generator-impact-dot ${row.verdict}`}></span>
                        <span>
                          <strong>{row.id}</strong>
                          <small>{[row.region, row.target].filter(Boolean).join(' · ') || 'n/a'}</small>
                        </span>
                      </span>
                    </td>
                    <td>{formatMetricValue(row.activeVusers)}</td>
                    <td>{formatMetricValue(row.tps)}</td>
                    <td>{formatMetricValue(row.p95Ms, 'ms')}</td>
                    <td>{formatMetricValue(row.errorPct, '%')}</td>
                    <td>{formatMetricValue(row.metrics.containerCPUThrottledSecondsRate, 's/s')}</td>
                    <td>{formatMetricValue(row.metrics.containerMemoryUsagePct, '%')}</td>
                    <td>{formatMetricValue(row.metrics.gcPauseMs, 'ms')}</td>
                    <td>{formatMetricValue(netLossValue, '/s')}</td>
                    <td>{formatMetricValue(row.metrics.diskIOTimePct, '%')}</td>
                    <td>{row.reasons.length > 0 ? row.reasons.join(', ') : 'None'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>
    )
  }

  const densityLabel = commandCenterSurface
    ? chartDensity === 'command_center' ? '3x3' : '2x2'
    : chartDensity === 'six_up'
      ? '6 Graphs'
      : chartDensity === 'command_center'
      ? 'Compact'
      : 'Standard'
  const heatmapGridSpanClassName = commandCenterSurface
    ? chartDensity === 'command_center'
      ? 'load-impact-graph-card-span-3'
      : 'load-impact-graph-card-span-2'
    : chartDensity === 'six_up'
      ? 'load-impact-graph-card-span-3'
      : 'load-impact-graph-card-span-2'
  const gridClassName = [
    'charts-grid',
    chartDensity === 'six_up' ? 'six-up' : '',
    chartDensity === 'command_center' ? 'command-center-compact' : '',
    commandCenterSurface ? 'command-center-graphs' : '',
  ].filter(Boolean).join(' ')

  return (
    <>
      {showLoadImpact && (
        <LoadImpactHeatmap
          points={filteredData}
          title="Load Impact"
          subtitle="Performance and resource behavior grouped by active VUser load."
          emptyMessage="Load impact appears after live metrics include active VUsers and performance or resource samples."
        />
      )}

      {externalControlsTarget && createPortal(
        <section className="command-panel command-sidebar-graph-options" aria-label="Graph display options">
          <div className="command-panel-header">
            <div>
              <h3>Graph Options</h3>
            </div>
          </div>
          <div className="charts-config-panel command-sidebar-graph-controls">
            {renderChartDisplayControlSections()}
          </div>
        </section>,
        externalControlsTarget,
      )}

      <div className={`charts-toolbar${commandCenterSurface ? ' command-center-charts-toolbar' : ''}`} aria-label="Final graph controls">
        <div className="charts-toolbar-heading">
          <div className="charts-toolbar-title-row">
            <h3 className="chart-title">Runtime Graphs</h3>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => setChartDrawerOpen(true)}
              aria-expanded={chartDrawerOpen}
            >
              Configure Graphs
            </button>
          </div>
          <p className="archive-section-copy">{commandCenterSurface ? 'Editable live graph wall for this command-center view.' : 'Live, archived, and load-under graphs for the selected run.'}</p>
        </div>
        <div className="charts-toolbar-actions">
          {enableGraphGroupTabs && (
            <div className="graph-group-tabs" role="tablist" aria-label="Graph groups">
              {visibleGraphGroupTabs.map(tab => (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  className={`graph-group-tab ${activeGraphGroupTab === tab.id ? 'active' : ''}`}
                  aria-selected={activeGraphGroupTab === tab.id}
                  onClick={() => updateActiveGraphGroupTab(tab.id)}
                >
                  <span>{tab.label}</span>
                  <small>{graphGroupCounts[tab.id]}</small>
                </button>
              ))}
            </div>
          )}
          <div className="charts-toolbar-badges">
            <span className="archive-chip">{displayModeCharts.length} graph{displayModeCharts.length === 1 ? '' : 's'}</span>
            <span className="archive-chip">{densityLabel}</span>
            <span className="archive-chip">{timeAxisMode === 'relative' ? 'Relative time' : 'Clock time'}</span>
            {loadImpactLoading && <span className="archive-chip">Loading load metrics</span>}
            {!loadImpactLoading && loadImpactCharts.length > 0 && <span className="archive-chip">{loadImpactCharts.length} load metrics</span>}
            {prometheusLoading && <span className="archive-chip">Loading Prometheus</span>}
            {!prometheusLoading && prometheusGraphCharts.length > 0 && <span className="archive-chip">{prometheusGraphCharts.length} Prometheus graphs</span>}
          </div>
        </div>
      </div>

      {activeGraphGroupTab === 'imported' && importedDashboardTabs.length > 1 && (
        <div className="graph-group-tabs" role="tablist" aria-label="Imported dashboards">
          {importedDashboardTabs.map(dashboard => (
            <button key={dashboard.dashboard_id} type="button" role="tab" className={`graph-group-tab ${activeImportedDashboardId === dashboard.dashboard_id ? 'active' : ''}`} aria-selected={activeImportedDashboardId === dashboard.dashboard_id} onClick={() => setActiveImportedDashboardId(dashboard.dashboard_id)}>
              <span>{dashboard.dashboard_title || 'Dashboard'}</span><small>{dashboard.panels.length}</small>
            </button>
          ))}
        </div>
      )}

      {loadImpactError && <p className="scenario-script-status error">{loadImpactError}</p>}
      {prometheusError && <p className="scenario-script-status error">{prometheusError}</p>}
      {chartActionError && <p className="scenario-script-status error" role="alert">{chartActionError}</p>}
      {terminalStatus && (
        <p className="chart-terminal-status" role="status">
          Run ended with {terminalStatus}. Charts show the final available telemetry.
        </p>
      )}

      {(latestMetrics?.telemetry?.dropped_results ?? 0) > 0 && (
        <p className="scenario-script-status error" role="alert">
          {`Metrics under-counted — ${(latestMetrics?.telemetry?.dropped_results ?? 0).toLocaleString()} result${(latestMetrics?.telemetry?.dropped_results ?? 0) === 1 ? '' : 's'} dropped by the telemetry pipeline; rates and totals shown are a floor.`}
        </p>
      )}

      {(latestMetrics?.telemetry?.shedded_arrivals ?? 0) > 0 && (
        <p className="scenario-script-status error" role="alert">
          {`Open-model load shed — ${(latestMetrics?.telemetry?.shedded_arrivals ?? 0).toLocaleString()} arrival${(latestMetrics?.telemetry?.shedded_arrivals ?? 0) === 1 ? '' : 's'} dropped because the system under test could not keep up with the offered rate. In-flight workers are capped to protect the generator, so effective throughput is below target — lower the TPS or raise run_logic.max_in_flight.`}
        </p>
      )}

      {displayModeCharts.length === 0 ? (
        <div className="charts-empty">
          <span>{
            activeGraphGroupTab === 'prometheus' && prometheusLoading
              ? 'Loading Prometheus graphs'
              : activeGraphGroupTab === 'prometheus' && prometheusError
                ? 'Prometheus metrics unavailable'
								: activeGraphGroupTab === 'imported' && importedDashboardsLoading
									? 'Loading imported dashboards'
									: activeGraphGroupTab === 'imported' && importedDashboardsError
										? 'Imported dashboards unavailable'
                : visibleCharts.length === 0 ? 'All graphs are hidden' : 'No graphs visible in this group'
          }</span>
          <button className="btn btn-secondary btn-sm" type="button" onClick={resetCharts}>Reset Graphs</button>
        </div>
      ) : (
        <div className={gridClassName}>
          {displayedCharts.map((chart) => {
            if (isLoadImpactHeatmapChart(chart)) {
              return (
                <LoadImpactHeatmap
                  key={chart.id}
                  ref={element => {
                    chartCardRefs.current[chart.id] = element
                  }}
                  dataChartId={chart.id}
                  points={loadImpactSourceData}
                  title={chart.title}
                  subtitle="Performance and resource response grouped by active VUser load."
                  loading={loadImpactLoading}
                  error={loadImpactError}
                  emptyMessage="Load impact heatmap appears after live metrics include active VUsers and performance or resource samples."
                  variant="graph-card"
                  className={`chart-card load-impact-graph-card ${heatmapGridSpanClassName} ${dedicatedHeatmapMode ? 'load-impact-map-mode' : ''} ${commandCenterSurface ? 'command-center-chart-card' : ''} ${expandedChart === chart.id ? 'expanded' : ''}`}
                  onContextMenu={event => openChartMenu(event, chart.id)}
                  onMouseEnter={() => setHoveredChartId(chart.id)}
                  onMouseLeave={() => handleChartMouseLeave(chart.id)}
                />
              )
            }
            const chartData = chartDataFor(chart)
            const chartHeight = expandedChart === chart.id
              ? commandCenterSurface ? 360 : 400
              : chartDensity === 'six_up'
                ? 132
                : chartDensity === 'command_center'
                  ? 170
                  : 200
            const metricRows = metricRowsForChart(chart)
            const metricRowKeys = metricRows.map(row => row.key)
            const visibleMetricKeys = visibleMetricKeysForChart(chart, metricRows)
            const visibleOverlayKeysForTitle = visibleOverlayMetricKeys(chart, visibleMetricKeys)
            const smoothedMetricKeys = commandCenterSurface
              ? smoothedMetricKeysForChart(chart, visibleMetricKeys)
              : []
            const smoothedMetricKeySet = new Set(smoothedMetricKeys)
            const chartCardSize = chartCardSizes[chart.id]
            const density = {
              chartWidth: chartCardSize?.width,
              chartHeight,
              visibleMetricCount: visibleMetricKeys.length,
              expanded: expandedChart === chart.id,
            }
            const includeArtStdDevBand = shouldRenderArtStdDevBands(chart, visibleMetricKeys, artStdDevBandsEnabled)
            // The downsample/std-dev-band/smoothing pipeline is the expensive part of this map body.
            // Recompute it only when this chart's actual inputs change; reuse the cached result on
            // unrelated re-renders (hover, ticks that leave this chart's data untouched).
            const derivedSignature = JSON.stringify({
              downsampleDenseCharts,
              targetPoints: downsampleTargetPointsForGraphDensity(density),
              chartWidth: chartCardSize?.width ?? null,
              chartHeight,
              expanded: expandedChart === chart.id,
              metricRowKeys,
              metricKeys: visibleMetricKeys,
              overlayKeys: visibleOverlayKeysForTitle,
              smoothedMetricKeys,
              includeArtStdDevBand,
            })
            const cachedDerived = derivedSeriesCacheRef.current.get(chart.id)
            let downsampledChartData: MetricsHistoryPoint[]
            let chartDataWithDisplayBands: MetricsHistoryPoint[]
            let displayChartData: MetricsHistoryPoint[]
            let metricSummaries: Record<string, ChartMetricSummary>
            let singletonTransactionMetricKeySet: Set<string>
            if (cachedDerived && cachedDerived.signature === derivedSignature && cachedDerived.sourceChartData === chartData) {
              downsampledChartData = cachedDerived.downsampledChartData
              chartDataWithDisplayBands = cachedDerived.chartDataWithDisplayBands
              displayChartData = cachedDerived.displayChartData
              metricSummaries = cachedDerived.metricSummaries
              singletonTransactionMetricKeySet = cachedDerived.singletonTransactionMetricKeys
            } else {
              downsampledChartData = downsampleDenseCharts
                ? downsampleGraphData(
                    chartData,
                    [...visibleMetricKeys, ...visibleOverlayKeysForTitle, 'targetTps'],
                    downsampleTargetPointsForGraphDensity(density),
                  )
                : chartData
              const smoothingRadiusForBuild = smoothingRadiusForGraphDensity(downsampledChartData.length, {
                chartWidth: chartCardSize?.width,
                chartHeight,
                visibleMetricCount: visibleMetricKeys.length,
                expanded: expandedChart === chart.id,
              })
              chartDataWithDisplayBands = includeArtStdDevBand
                ? buildArtStdDevBandData(downsampledChartData, smoothingRadiusForBuild)
                : downsampledChartData
              displayChartData = smoothedMetricKeys.length > 0
                ? buildSmoothedGraphData(chartDataWithDisplayBands, smoothedMetricKeys, smoothingRadiusForBuild)
                : chartDataWithDisplayBands
              metricSummaries = chartMetricSummaries(chartData, metricRowKeys)
              singletonTransactionMetricKeySet = singletonTransactionMetricKeys(displayChartData, metricRowKeys)
              derivedSeriesCacheRef.current.set(chart.id, {
                signature: derivedSignature,
                sourceChartData: chartData,
                downsampledChartData,
                chartDataWithDisplayBands,
                displayChartData,
                metricSummaries,
                singletonTransactionMetricKeys: singletonTransactionMetricKeySet,
              })
            }
            const displayMetricKey = (key: string) => smoothedMetricKeySet.has(key) ? smoothedGraphMetricKey(key) : key
            const metricDot = (key: string) => singletonTransactionMetricKeySet.has(key) ? { r: 3 } : false
            const primaryMetricKeysForDomain = visibleMetricKeys.filter(key => (
              !visibleOverlayKeysForTitle.includes(key as OverlayMetricKey)
              && key !== routeLatencySuccessRateKey
            ))
            // Fit the axis to the series the chart actually draws. With smoothing on,
            // the plotted values are the `__smoothed` keys, whose peaks are lower than
            // the raw samples -- scaling to the raw peak leaves a transient spike
            // stretching the axis while the drawn line stays flattened near the x-axis.
            const displayMetricKeysForDomain = primaryMetricKeysForDomain.map(displayMetricKey)
            const metricAxisDomain = visibleMetricDomain(
              displayChartData,
              includeArtStdDevBand ? [...displayMetricKeysForDomain, ART_STD_DEV_BAND_KEY] : displayMetricKeysForDomain,
              chart.domain,
            )
            const chartTitle = chartTitleWithOverlays(chart, visibleOverlayKeysForTitle)
            const baseChartMargin = chartMarginForOverlayKeys(visibleOverlayKeysForTitle, commandCenterSurface) ?? {}
            const chartMargin = chart.id === 'network_latency' && routeLatencyMode === 'stability' && visibleMetricKeys.includes(routeLatencySuccessRateKey)
              ? { ...baseChartMargin, right: Math.max(baseChartMargin.right ?? 0, commandCenterSurface ? 4 : 70) }
              : baseChartMargin
            const metricAxisIdForChart = metricYAxisId(chart, visibleMetricKeys)
            const showMetric = (key: string) => visibleMetricKeys.includes(key)
            const showDetails = expandedChart === chart.id || (commandCenterSurface ? showGraphTableRows : chartDensity === 'standard')
            const accessibleTableId = chartDomIdFor(chart.id, 'data-table')
            const visibleRowsForA11y = metricRows.filter(row => visibleMetricKeys.includes(row.key))
            const chartAriaLabel = chartAccessibleLabel(chartTitle, visibleRowsForA11y, displayChartData.length)
            return (
              <div
                key={chart.id}
                ref={element => {
                  chartCardRefs.current[chart.id] = element
                }}
                data-chart-id={chart.id}
                className={`chart-card ${commandCenterSurface ? 'command-center-chart-card' : ''} ${expandedChart === chart.id ? 'expanded' : ''} ${draggingChartId === chart.id ? 'dragging' : ''} ${dragOverChartId === chart.id ? 'drop-target' : ''}`}
                onContextMenu={event => openChartMenu(event, chart.id)}
                onMouseEnter={() => setHoveredChartId(chart.id)}
                onMouseLeave={() => handleChartMouseLeave(chart.id)}
                onDragOver={event => handleChartDragOver(event, chart.id)}
                onDragLeave={() => setDragOverChartId(current => current === chart.id ? null : current)}
                onDrop={event => handleChartDrop(event, chart.id)}
              >
                <div className="chart-card-header">
                  <div className="chart-title-wrap">
                    <button
                      className="chart-drag-handle"
                      type="button"
                      draggable
                      aria-label={`Rearrange ${chartTitle}`}
                      title="Drag to rearrange graph. Use arrow keys to move."
                      onDragStart={event => handleChartDragStart(event, chart.id)}
                      onDragEnd={handleChartDragEnd}
                      onKeyDown={event => handleChartReorderKeyDown(event, chart.id)}
                    />
                    <h3 className="chart-title">{chartTitle}</h3>
                  </div>
                  <div className="chart-card-actions">
                    {chart.id === 'latency' && (
                      <>
                        <label
                          className="chart-card-checkbox"
                          title={latencyKeys.length === 0 ? 'No route latency samples available' : undefined}
                        >
                          <input
                            type="checkbox"
                            checked={responseTimeNetworkMode === 'end_to_end'}
                            disabled={latencyKeys.length === 0}
                            onChange={event => setResponseTimeNetworkLatencyIncluded(event.currentTarget.checked)}
                          />
                          <span>Include network latency</span>
                        </label>
                        <label
                          className="chart-card-checkbox"
                          title="Show rolling standard deviation band for Total ART"
                        >
                          <input
                            type="checkbox"
                            checked={artStdDevBandsEnabled}
                            onChange={event => setArtStdDevBandsEnabled(event.currentTarget.checked)}
                          />
                          <span>σ Bands</span>
                        </label>
                      </>
                    )}
                    {chart.id === 'network_latency' && latencyKeys.length > 0 && (
                      <div className="route-latency-card-controls">
                        <div className="route-latency-mode-toggle" role="group" aria-label="Route latency mode">
                          {routeLatencyModes.map(mode => (
                            <button
                              key={mode.id}
                              className={`btn-chip ${routeLatencyMode === mode.id ? 'active' : ''}`}
                              type="button"
                              aria-pressed={routeLatencyMode === mode.id}
                              onClick={() => setRouteLatencyMode(mode.id)}
                            >
                              {mode.label}
                            </button>
                          ))}
                        </div>
                        {latencyKeys.length > 1 && (
                          <select
                            className="route-latency-target-select"
                            aria-label="Route latency target"
                            value={activeRouteLatencyKey}
                            onChange={event => setSelectedRouteLatencyKey(event.currentTarget.value)}
                          >
                            {latencyKeys.map(key => (
                              <option key={key} value={key}>{routeLatencyTargetLabelFromKey(key)}</option>
                            ))}
                          </select>
                        )}
                      </div>
                    )}
                    <button
                      className="btn btn-sm btn-ghost chart-actions-button"
                      type="button"
                      aria-label={`${chartTitle} actions`}
                      aria-haspopup="menu"
                      aria-expanded={chartMenu?.chartId === chart.id}
                      onClick={event => openChartMenuFromButton(event, chart.id)}
                    >
                      Actions
                    </button>
                    <button
                      className="btn-icon btn-expand"
                      type="button"
                      aria-label={expandedChart === chart.id ? `Collapse ${chartTitle}` : `Expand ${chartTitle}`}
                      onClick={() => onExpandedChartChange(expandedChart === chart.id ? null : chart.id)}
                    >
                      {expandedChart === chart.id ? '↙' : '↗'}
                    </button>
                  </div>
                </div>
				<div
					className="chart-visual"
					role={isStatTileChart(chart) || Boolean(chart.importedError) ? undefined : 'img'}
					aria-label={isStatTileChart(chart) || chart.importedError ? undefined : chartAriaLabel}
					aria-describedby={!isStatTileChart(chart) && !chart.importedError && visibleRowsForA11y.length > 0 ? accessibleTableId : undefined}
				>
                  {isStatTileChart(chart) ? (
                    <StatTile
                      title={chart.title}
                      value={chart.statValue?.value}
                      unit={chart.statValue?.unit}
                      error={chart.statValue?.error}
                      compact={commandCenterSurface}
                    />
                  ) : chart.importedError ? (
                    // A resolved imported line panel that errored (bad PromQL, series
                    // cap, unresolved variable) — surface the message instead of a
                    // silently-empty chart so fail-soft does not read as no-data.
                    <div className="chart-empty-state chart-error-state" role="status">{chart.importedError}</div>
                  ) : chart.importedPanel?.kind === 'table' || chart.importedPanel?.kind === 'heatmap' ? (
                    <PrometheusPanelCard title={chart.title} unit={chart.prometheusGraph?.unit} kind={chart.importedPanel.kind} series={chart.prometheusGraph?.series} height={chartHeight} />
                  ) : (
                  <ResponsiveContainer width="100%" height={chartHeight}>
            {chart.type === 'area' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {chart.dataKey && showMetric(chart.dataKey) && (
                  <Area yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey(chart.dataKey)} name="Total" stroke={chart.color!} fill={chartGradientFill(chart, chart.dataKey)} fillOpacity={1} isAnimationActive={false} />
                )}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-load-impact' ? (
              <ComposedChart
                data={displayChartData}
                margin={chartMargin}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderLoadImpactAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderLoadImpactTooltip(chart)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows
                  .filter(row => !overlayMetricKeysForChart(chart).includes(row.key as OverlayMetricKey))
                  .map((row) => {
                    // Group lines read as first-class series (solid, group color);
                    // the min/max envelope stays thin and dashed behind them.
                    const groupSeries = isLoadImpactGroupSeriesKey(row.key)
                    const primary = row.key === 'average' || groupSeries
                    return showMetric(row.key) && (
                      <Line
                        key={row.key}
                        yAxisId={metricAxisIdForChart}
                        type="monotone"
                        dataKey={displayMetricKey(row.key)}
                        name={row.label}
                        stroke={row.color}
                        dot={{ r: primary ? 3 : 2 }}
                        activeDot={{ r: 5 }}
                        strokeWidth={groupSeries ? 2 : row.key === 'average' ? 2.5 : 1.5}
                        strokeDasharray={groupSeries ? row.strokeDasharray : row.key === 'average' ? undefined : '4 2'}
                        connectNulls
                        isAnimationActive={false}
                      />
                    )
                  })}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
              </ComposedChart>
            ) : chart.type === 'line-load-impact-percentiles' ? (
              <ComposedChart
                data={displayChartData}
                margin={chartMargin}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderLoadImpactAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderLoadImpactTooltip(chart)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                <Legend verticalAlign="bottom" itemSorter={preserveChartItemOrder} />
                {metricRows
                  .filter(row => !overlayMetricKeysForChart(chart).includes(row.key as OverlayMetricKey))
                  .map(row => {
                    const presentation = chartMetricLinePresentation(row)
                    return showMetric(row.key) && (
                      <Line
                        key={row.key}
                        yAxisId={metricAxisIdForChart}
                        type="monotone"
                        dataKey={displayMetricKey(row.key)}
                        name={row.label}
                        stroke={row.color}
                        dot={{ r: 2 }}
                        activeDot={{ r: 5 }}
                        strokeWidth={presentation.strokeWidth}
                        strokeDasharray={presentation.strokeDasharray}
                        strokeOpacity={presentation.strokeOpacity}
                        connectNulls
                        isAnimationActive={false}
                      />
                    )
                  })}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
              </ComposedChart>
            ) : chart.type === 'line-prometheus' ? (
              <ComposedChart
                data={displayChartData}
                margin={chartMargin}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {metricRows.length > 1 && <Legend verticalAlign="bottom" itemSorter={preserveChartItemOrder} />}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows.map(row => showMetric(row.key) && (
                  <Line
                    key={row.key}
                    yAxisId={metricAxisIdForChart}
                    type="monotone"
                    dataKey={displayMetricKey(row.key)}
                    name={row.label}
                    stroke={row.color}
                    dot={false}
                    strokeWidth={2}
                    connectNulls
                    isAnimationActive={false}
                  />
                ))}
              </ComposedChart>
            ) : chart.type === 'line-multi' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows
                  .filter(row => !overlayMetricKeysForChart(chart).includes(row.key as OverlayMetricKey))
                  .map(row => {
                    const presentation = chartMetricLinePresentation(row)
                    return showMetric(row.key) && (
                      <Line
                        key={row.key}
                        yAxisId={metricAxisIdForChart}
                        type="monotone"
                        dataKey={displayMetricKey(row.key)}
                        name={row.label}
                        stroke={row.color}
                        dot={metricDot(row.key)}
                        strokeWidth={presentation.strokeWidth}
                        strokeDasharray={presentation.strokeDasharray}
                        strokeOpacity={presentation.strokeOpacity}
                        connectNulls
                        isAnimationActive={false}
                      />
                    )
                  })}
                {surface === 'command-center' && chartData.some(point => typeof point.targetTps === 'number') && (
                  <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="targetTps" name="Target" stroke="var(--warning)" dot={false} strokeWidth={1.75} strokeDasharray="5 3" isAnimationActive={false} />
                )}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-latency' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys, includeArtStdDevBand)}
                {renderArtStdDevBand(chart, includeArtStdDevBand, metricAxisIdForChart)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows
                  .filter(row => row.kind === 'transaction')
                  .map(row => {
                    const presentation = chartMetricLinePresentation(row)
                    return showMetric(row.key) && (
                      <Line
                        key={row.key}
                        yAxisId={metricAxisIdForChart}
                        type="monotone"
                        dataKey={displayMetricKey(row.key)}
                        name={row.label}
                        stroke={row.color}
                        dot={metricDot(row.key)}
                        strokeWidth={presentation.strokeWidth}
                        strokeDasharray={presentation.strokeDasharray}
                        strokeOpacity={presentation.strokeOpacity}
                        connectNulls
                        isAnimationActive={false}
                      />
                    )
                  })}
                {metricRows
                  .filter(row => row.kind === 'percentile')
                  .map(row => {
                    const presentation = chartMetricLinePresentation(row)
                    return showMetric(row.key) && (
                      <Line
                        key={row.key}
                        yAxisId={metricAxisIdForChart}
                        type="monotone"
                        dataKey={displayMetricKey(row.key)}
                        name={row.label}
                        stroke={row.color}
                        dot={false}
                        strokeWidth={presentation.strokeWidth}
                        strokeDasharray={presentation.strokeDasharray}
                        strokeOpacity={presentation.strokeOpacity}
                        isAnimationActive={false}
                      />
                    )
                  })}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'area-multi' || chart.type === 'area-stacked' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                <Legend verticalAlign="bottom" itemSorter={preserveChartItemOrder} />
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {chart.keys!.filter(key => showMetric(key.k)).map((key) => (
                  <Area key={key.k} yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey(key.k)} name={key.n} stroke={key.c} fill={chartGradientFill(chart, key.k)} fillOpacity={1} isAnimationActive={false} connectNulls={false} stackId={chart.type === 'area-stacked' ? (chart.stackId ?? 'stack') : undefined} />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-impact' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows.map((row) => showMetric(row.key) && (
                  <Line
                    key={row.key}
                    yAxisId={metricAxisIdForChart}
                    type="monotone"
                    dataKey={displayMetricKey(row.key)}
                    name={row.label}
                    stroke={row.color}
                    dot={false}
                    strokeWidth={2}
                    isAnimationActive={false}
                  />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-network' ? (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderRouteLatencySuccessAxis(visibleMetricKeys)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                <Legend verticalAlign="bottom" itemSorter={preserveChartItemOrder} />
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {metricRows.map((row) => showMetric(row.key) && (
                    <Line
                      key={row.key}
                      yAxisId={row.key === routeLatencySuccessRateKey ? 'successRate' : metricAxisIdForChart}
                      type="monotone"
                      dataKey={displayMetricKey(row.key)}
                      name={row.label}
                      stroke={row.color}
                      dot={false}
                      strokeWidth={2}
                      isAnimationActive={false}
                    />
                  ))}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : (
              <ComposedChart
                data={displayChartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => handleChartMouseDown(event, displayChartData)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event, displayChartData)}
                onMouseUp={handleChartMouseUp}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(displayChartData, metricAxisIdForChart)}
                <Legend itemSorter={preserveChartItemOrder} />
                {renderChartGradientDefs(chart, metricRows, visibleMetricKeys)}
                {showGradientUnderlays && renderGradientUnderlays(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showMetric('netSent') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey('netSent')} name="Net Egress" stroke="var(--chart-1)" dot={false} strokeWidth={2} isAnimationActive={false} />}
                {showMetric('netRecv') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey('netRecv')} name="Net Ingress" stroke="var(--chart-2)" dot={false} strokeWidth={2} isAnimationActive={false} />}
                {showMetric('diskRead') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey('diskRead')} name="Disk Read" stroke="var(--chart-3)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />}
                {showMetric('diskWrite') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={displayMetricKey('diskWrite')} name="Disk Write" stroke="var(--chart-6)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />}
                {renderOverlayLines(chart, visibleMetricKeys, displayMetricKey)}
                {renderSelectedSeriesEmphasis(chart, metricRows, visibleMetricKeys, displayMetricKey, metricAxisIdForChart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            )}
                  </ResponsiveContainer>
                  )}
                </div>
                {renderAccessibleMetricsTable(metricRows, visibleMetricKeys, chartTitle, accessibleTableId, metricSummaries)}

          {(chart.id === 'tps' || chart.id === 'errors') && renderLineMultiSeriesLegend(chart, metricRows, visibleMetricKeys)}
          {isLoadImpactChart(chart) && (chart.loadImpactGroupNames?.length ?? 0) > 0 && renderLineMultiSeriesLegend(chart, metricRows, visibleMetricKeys)}
          {chart.id === 'latency' && renderLatencySeriesLegend(metricRows, visibleMetricKeys, includeArtStdDevBand)}
          {chart.id === 'network_latency' && showDetails && renderRouteLatencyDetails()}
          {chart.id === 'generator_impact' && showDetails && renderGeneratorImpactDetails()}
            {showDetails && renderMetricsTable(chart, metricRows, visibleMetricKeys, chartData, metricSummaries)}
            </div>
            )
          })}
        </div>
      )}
      {chartDrawerOpen && (
        <div className="charts-config-drawer-layer">
          <button
            type="button"
            className="charts-config-backdrop"
            aria-label="Close graph configuration"
            onClick={() => setChartDrawerOpen(false)}
          />
          <aside ref={chartDrawerRef} className="charts-config-drawer" role="dialog" aria-modal="true" aria-label="Final graph configuration">
            <div className="charts-config-drawer-header">
              <div>
                <h3>Graph Configuration</h3>
                <p>{externalControlsTarget ? 'Choose graph cards for the final graph area.' : 'Choose graph cards, presets, density, and time display for the final graph area.'}</p>
              </div>
              <button ref={chartDrawerCloseButtonRef} type="button" className="btn btn-secondary btn-sm" onClick={() => setChartDrawerOpen(false)}>Close</button>
            </div>
            {renderChartConfigurationPanel()}
          </aside>
        </div>
      )}
      {contextChart && chartMenu && (
        <div
          ref={chartMenuRef}
          className="chart-context-menu"
          style={{ left: chartMenu.x, top: chartMenu.y }}
          role="menu"
          tabIndex={-1}
          aria-label={`${contextChart.title} actions`}
          onKeyDown={event => {
            const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)'))
            const currentIndex = items.indexOf(document.activeElement as HTMLButtonElement)
            const targetIndex = event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? items.length - 1
                : event.key === 'ArrowDown'
                  ? (currentIndex + 1 + items.length) % items.length
                  : event.key === 'ArrowUp'
                    ? (currentIndex - 1 + items.length) % items.length
                    : -1
            if (targetIndex >= 0) {
              event.preventDefault()
              items[targetIndex]?.focus()
            }
          }}
          onContextMenu={event => {
            event.preventDefault()
            event.stopPropagation()
          }}
        >
          <div className="chart-context-menu-title">{displayTitleForChart(contextChart)}</div>
          <button ref={chartMenuFirstItemRef} type="button" role="menuitem" onClick={() => void runChartAction(onResetZoom)}>Reset Zoom</button>
          {(['vusers', 'avgMs', 'tps'] as OverlayMetricKey[])
            .filter(key => isOverlayMetricAvailable(contextChart, key))
            .map(key => (
              <button key={key} type="button" role="menuitem" onClick={() => void runChartAction(() => toggleOverlayMetric(contextChart, key))}>
                {isOverlayMetricEnabled(contextChart, key) ? `Hide ${overlayMetricMenuLabels[key]} Overlay` : `Overlay ${overlayMetricMenuLabels[key]}`}
              </button>
            ))}
          {contextChart.id === 'latency' && (
            <button type="button" role="menuitem" onClick={() => void runChartAction(() => setResponseTimeNetworkLatencyIncluded(responseTimeNetworkMode === 'application'))}>
              {responseTimeNetworkMode === 'application' ? 'Include Network Latency' : 'Remove Network Latency'}
            </button>
          )}
          <button type="button" role="menuitem" onClick={() => void runChartAction(() => onExpandedChartChange(expandedChart === contextChart.id ? null : contextChart.id))}>
            {expandedChart === contextChart.id ? 'Collapse Graph' : 'Expand Graph'}
          </button>
          <button type="button" role="menuitem" onClick={() => void runChartAction(() => downloadChartSvgSnapshot(contextChart), 'Could not save the chart file')}>Download Snapshot</button>
          <button type="button" role="menuitem" onClick={() => void runChartAction(() => exportChartCsv(contextChart), 'Could not save the chart file')}>Export Graph Data</button>
          <button type="button" role="menuitem" onClick={() => void runChartAction(() => copyLatestValues(contextChart), 'Could not copy or save the latest chart values')}>Copy Latest Values</button>
          <button type="button" role="menuitem" onClick={() => void runChartAction(() => toggleChartVisibility(contextChart.id))}>Hide Graph</button>
        </div>
      )}
      {axisLabelPopover && (
        <div
          ref={axisLabelPopoverRef}
          className="chart-axis-label-popover"
          style={{ left: axisLabelPopover.x, top: axisLabelPopover.y }}
          role="status"
          aria-live="polite"
        >
          <div className="chart-axis-label-popover-title">Y-axis</div>
          <div className="chart-axis-label-popover-value">{axisLabelPopover.label}</div>
        </div>
      )}
    </>
  )
}
