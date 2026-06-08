import { useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent } from 'react'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  LineChart,
  ReferenceLine,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { LatencyTarget, LiveMetrics, MetricsHistoryPoint, NetworkLatency } from '../types'
import { buildGeneratorImpactView, type GeneratorImpactVerdict } from './generatorImpact'
import LoadImpactHeatmap from './LoadImpactHeatmap'
import {
  buildLoadImpactModel,
  formatLoadImpactValue,
} from './loadImpactModel'
import { chartTooltipContentStyle } from './chartTooltipTheme'
import {
  DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS,
  LOAD_IMPACT_VISIBLE_CHARTS_STORAGE_KEY,
  buildLoadImpactCharts,
  chartConfigs,
  chartMarginForOverlayKeys,
  chartMetricKeys,
  chartMetricRows,
  chartMetricSummary,
  chartPrimaryYAxisLabel,
  chartStateForMonitorPreset,
  chartTitleWithOverlays,
  defaultHiddenChartIds,
  formatGraphOverlayMetricValue,
  formatRelativeRunTime,
  graphCsvForChart,
  isLoadImpactChart,
  isOverlayMetricAvailable,
  latestRouteLatencyValues,
  loadImpactChartDataForRow,
  loadImpactChartId,
  monitorPresets,
  normalizedChartOrder,
  overlayMetricMenuLabels,
  overlayMetricRows,
  rateOverlayAxisLabel,
  responseTimeDataWithoutNetworkLatency,
  routeLatencyModes,
  routeLatencySeriesKeys,
  sameStringArray,
  sameStringSet,
  visibleMetricDomain,
  type ChartConfig,
  type ChartDensityMode,
  type ChartDomain,
  type ChartMetricRow,
  type ChartTimeAxisMode,
  type OverlayMetricKey,
  type ResponseTimeNetworkMode,
  type RouteLatencyMode,
} from './testExecutionChartModel'
import {
  latencySeriesKeyForTarget,
  routeLatencyDetailSeriesKey,
  routeLatencySeriesLabel,
  routeLatencyTargetLabelFromKey,
} from './latencySeries'
import { ExecutionChartTimeRange } from './testExecutionChartZoomModel'
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
  onChartMouseDown: (activeLabel?: string | number) => void
  onChartMouseMove: (activeLabel?: string | number) => void
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
  showLoadImpact?: boolean
  loadImpactData?: MetricsHistoryPoint[]
  loadImpactLoading?: boolean
  loadImpactError?: string
  latestLatency?: NetworkLatency
  latestMetrics?: LiveMetrics
  surface?: 'execution' | 'command-center'
  storageScope?: string
  initialPresetId?: string
  defaultDensity?: ChartDensityMode
  activeCursor?: GraphCursorState | null
  onActiveCursorChange?: (cursor: GraphCursorState | null) => void
}

type ChartDropPosition = 'before' | 'after'

export interface GraphCursorState {
  label?: string | number
  index?: number
}

interface RechartsMouseEvent {
  activeLabel?: string | number
  activeTooltipIndex?: number
}

const AXIS_LABEL_STYLE = {
  fill: 'var(--text-muted)',
  fontSize: 11,
  fontWeight: 700,
}

function bottomAxisLabel(value: string) {
  return {
    value,
    position: 'insideBottom',
    offset: -18,
    style: AXIS_LABEL_STYLE,
  }
}

function leftAxisLabel(value: string) {
  return {
    value,
    angle: -90,
    position: 'left',
    offset: 0,
    style: { ...AXIS_LABEL_STYLE, textAnchor: 'middle' },
  }
}

function rightAxisLabel(value: string) {
  return {
    value,
    angle: 90,
    position: 'right',
    offset: 0,
    style: { ...AXIS_LABEL_STYLE, textAnchor: 'middle' },
  }
}

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

function downloadBlob(filename: string, blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function downloadTextFile(filename: string, content: string, type = 'text/plain;charset=utf-8') {
  downloadBlob(filename, new Blob([content], { type }))
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
) {
  const keys = chartMetricKeys(chart, groupNames, latencyKeys, false, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey)
  const labels = new Map(chartMetricRows(chart, groupNames, latencyKeys, [], false, overlayMetricKeys, routeLatencyMode, selectedRouteLatencyKey).map(row => [row.key, row.label]))
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
  showLoadImpact = true,
  loadImpactData = [],
  loadImpactLoading = false,
  loadImpactError = '',
  latestLatency,
  latestMetrics,
  surface = 'execution',
  storageScope = 'apexload.execution',
  initialPresetId,
  defaultDensity,
  activeCursor,
  onActiveCursorChange,
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
  const responseTimeNetworkModeStorageKey = storageKeyFor('responseTimeNetworkMode')
  const routeLatencyModeStorageKey = storageKeyFor('routeLatencyMode')
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
  const [chartDensity, setChartDensity] = useState<ChartDensityMode>(() => readStoredChartDensity(chartDensityStorageKey, densityFallback))
  const [responseTimeNetworkMode, setResponseTimeNetworkMode] = useState<ResponseTimeNetworkMode>(() => readStoredResponseTimeNetworkMode(responseTimeNetworkModeStorageKey))
  const [routeLatencyMode, setRouteLatencyMode] = useState<RouteLatencyMode>(() => readStoredRouteLatencyMode(routeLatencyModeStorageKey))
  const [selectedRouteLatencyKey, setSelectedRouteLatencyKey] = useState('')
  const [chartMenu, setChartMenu] = useState<{ chartId: string; x: number; y: number } | null>(null)
  const [chartDrawerOpen, setChartDrawerOpen] = useState(false)
  const [hoveredChartId, setHoveredChartId] = useState<string | null>(null)
  const [draggingChartId, setDraggingChartId] = useState<string | null>(null)
  const [dragOverChartId, setDragOverChartId] = useState<string | null>(null)
  const chartCardRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const chartMenuRef = useRef<HTMLDivElement | null>(null)
  const commandCenterSurface = surface === 'command-center'
  const loadImpactSourceData = loadImpactData.length > 0 ? loadImpactData : filteredData
  const loadImpactModel = useMemo(() => buildLoadImpactModel(loadImpactSourceData), [loadImpactSourceData])
  const loadImpactRows = loadImpactModel.rows
  const loadImpactRowsByChartId = useMemo(() => new Map(loadImpactRows.map(row => [loadImpactChartId(row.metric.id), row])), [loadImpactRows])
  const loadImpactCharts = useMemo(() => buildLoadImpactCharts(loadImpactRows), [loadImpactRows])
  const allChartConfigs = useMemo(() => [...chartConfigs, ...loadImpactCharts], [loadImpactCharts])
  const allChartIds = useMemo(() => allChartConfigs.map(chart => chart.id), [allChartConfigs])
  const allChartIdSignature = allChartIds.join('|')
  const visibleLoadImpactChartIdSignature = visibleLoadImpactChartIds.join('|')

  useEffect(() => {
    setChartOrder(prev => normalizedChartOrder(prev, allChartIds))
  }, [allChartIdSignature, allChartIds])

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
    setChartOrder(next.chartOrder)
    setHiddenChartIds(next.hiddenChartIds)
    setInitialPresetApplied(true)
  }, [allChartIds, hasStoredChartState, initialPresetApplied, initialPresetId])

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
    writeStoredString(responseTimeNetworkModeStorageKey, responseTimeNetworkMode)
  }, [responseTimeNetworkMode, responseTimeNetworkModeStorageKey])

  useEffect(() => {
    writeStoredString(routeLatencyModeStorageKey, routeLatencyMode)
  }, [routeLatencyMode, routeLatencyModeStorageKey])

  useEffect(() => {
    if (!chartMenu) return
    const closeMenu = () => setChartMenu(null)
    const closeMenuOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && chartMenuRef.current?.contains(target)) {
        return
      }
      setChartMenu(null)
    }
    const closeMenuOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setChartMenu(null)
      }
    }
    window.addEventListener('pointerdown', closeMenuOnOutsidePointerDown)
    window.addEventListener('resize', closeMenu)
    window.addEventListener('keydown', closeMenuOnEscape)
    return () => {
      window.removeEventListener('pointerdown', closeMenuOnOutsidePointerDown)
      window.removeEventListener('resize', closeMenu)
      window.removeEventListener('keydown', closeMenuOnEscape)
    }
  }, [chartMenu])

  useEffect(() => {
    if (!chartDrawerOpen) return

    const closeDrawerOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setChartDrawerOpen(false)
      }
    }
    window.addEventListener('keydown', closeDrawerOnEscape)
    return () => window.removeEventListener('keydown', closeDrawerOnEscape)
  }, [chartDrawerOpen])

  const orderedCharts = useMemo(() => {
    const byId = new Map(allChartConfigs.map(chart => [chart.id, chart]))
    const ordered = chartOrder
      .map(id => byId.get(id))
      .filter((chart): chart is ChartConfig => Boolean(chart))
    return ordered.length ? ordered : allChartConfigs
  }, [allChartConfigs, chartOrder])
  const activeMonitorPresetId = useMemo(() => {
    const currentOrder = orderedCharts.map(chart => chart.id)
    return monitorPresets.find(preset => {
      const presetState = chartStateForMonitorPreset(preset.id, allChartIds)
      return sameStringArray(presetState.chartOrder, currentOrder)
        && sameStringSet(presetState.hiddenChartIds, hiddenChartIds.filter(id => allChartIds.includes(id)))
    })?.id ?? ''
  }, [allChartIds, hiddenChartIds, orderedCharts])
  const latencyKeys = useMemo(() => routeLatencySeriesKeys(filteredData), [filteredData])
  const activeRouteLatencyKey = latencyKeys.includes(selectedRouteLatencyKey)
    ? selectedRouteLatencyKey
    : latencyKeys[0]
  const latencyLatestValues = useMemo(() => latestRouteLatencyValues(filteredData, latencyKeys), [filteredData, latencyKeys])
  const responseTimeChartData = useMemo(
    () => responseTimeNetworkMode === 'application'
      ? responseTimeDataWithoutNetworkLatency(filteredData, latencyKeys, groupNames)
      : filteredData,
    [filteredData, groupNames, latencyKeys, responseTimeNetworkMode],
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

  if (filteredData.length <= 1 && loadImpactCharts.length === 0 && !loadImpactLoading && !loadImpactError) {
    return null
  }

  const latest = filteredData[filteredData.length - 1] || loadImpactSourceData[loadImpactSourceData.length - 1] || {}
  const visibleCharts = orderedCharts.filter(chart => !hiddenChartIds.includes(chart.id))
  const displayedCharts = expandedChart
    ? [
        ...visibleCharts.filter(chart => chart.id === expandedChart),
        ...visibleCharts.filter(chart => chart.id !== expandedChart),
      ]
    : visibleCharts
  const contextChart = chartMenu ? visibleCharts.find(chart => chart.id === chartMenu.chartId) : undefined
  const chartDataFor = (chart: ChartConfig) => {
    if (isLoadImpactChart(chart)) {
      return loadImpactChartDataForRow(loadImpactRowsByChartId.get(chart.id), loadImpactRows)
    }
    return chart.id === 'latency' ? responseTimeChartData : filteredData
  }
  const sharedCursor = activeCursor !== undefined ? activeCursor : localActiveCursor
  const updateSharedCursor = (cursor: GraphCursorState | null) => {
    if (onActiveCursorChange) {
      onActiveCursorChange(cursor)
    } else {
      setLocalActiveCursor(cursor)
    }
  }

  const cursorXForChart = (chartData: MetricsHistoryPoint[]) => {
    if (!sharedCursor || chartData.length === 0) return undefined
    if (sharedCursor.label !== undefined && chartData.some(point => point.time === sharedCursor.label)) {
      return sharedCursor.label
    }
    if (typeof sharedCursor.index === 'number' && Number.isFinite(sharedCursor.index)) {
      const index = Math.max(0, Math.min(chartData.length - 1, Math.round(sharedCursor.index)))
      return chartData[index]?.time
    }
    return undefined
  }

  const renderSharedCursor = (chartData: MetricsHistoryPoint[]) => {
    if (surface !== 'command-center') return null
    const x = cursorXForChart(chartData)
    if (x === undefined) return null
    return (
      <ReferenceLine
        x={x}
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
      label={bottomAxisLabel(timeAxisMode === 'relative' ? 'Run time' : 'Clock time')}
    />
  )

  const renderLoadImpactAxis = () => (
    <XAxis
      dataKey="time"
      stroke="var(--text-muted)"
      fontSize={11}
      height={46}
      tickMargin={8}
      interval={0}
      minTickGap={8}
      label={bottomAxisLabel('VUser load')}
    />
  )

  const renderTooltip = (chartId: string) => {
    const showTimeLabel = hoveredChartId === chartId
    return (
      <Tooltip
        contentStyle={{ ...chartTooltipContentStyle, borderRadius: 8 }}
        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: showTimeLabel ? 4 : 0, display: showTimeLabel ? undefined : 'none' }}
        labelFormatter={(value) => showTimeLabel ? formatXAxisLabel(value) : ''}
        formatter={(value, name) => [formatGraphOverlayMetricValue(value), name]}
        isAnimationActive={false}
      />
    )
  }

  const renderLoadImpactTooltip = (chart: ChartConfig) => {
    const showLabel = hoveredChartId === chart.id
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
        isAnimationActive={false}
      />
    )
  }

  const handleChartMouseMove = (chartId: string, event?: RechartsMouseEvent) => {
    setHoveredChartId(current => current === chartId ? current : chartId)
    const activeLabel = event?.activeLabel
    if (surface === 'command-center') {
      const nextCursor = activeLabel !== undefined || typeof event?.activeTooltipIndex === 'number'
        ? { label: activeLabel, index: event?.activeTooltipIndex }
        : null
      updateSharedCursor(nextCursor)
    }
    onChartMouseMove(activeLabel)
  }

  const handleChartMouseLeave = (chartId: string) => {
    setHoveredChartId(current => current === chartId ? null : current)
    if (surface === 'command-center') {
      updateSharedCursor(null)
    }
  }

  const toggleChartVisibility = (chartId: string) => {
    const chart = allChartConfigs.find(candidate => candidate.id === chartId)
    const togglingHiddenLoadImpactChart = (
      chart
      && isLoadImpactChart(chart)
      && !DEFAULT_LOAD_IMPACT_GRAPH_METRIC_IDS.includes(chart.loadImpactMetric!.id)
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

  const metricRowsForChart = (chart: ChartConfig) => chartMetricRows(chart, groupNames, latencyKeys, groupColors, false, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey)

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

  const openChartMenu = (event: MouseEvent<HTMLDivElement>, chartId: string) => {
    event.preventDefault()
    event.stopPropagation()
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

  const runChartAction = (action: () => void) => {
    action()
    setChartMenu(null)
  }

  const displayTitleForChart = (chart: ChartConfig) => {
    const rows = metricRowsForChart(chart)
    const visibleMetricKeys = visibleMetricKeysForChart(chart, rows)
    return chartTitleWithOverlays(chart, visibleOverlayMetricKeys(chart, visibleMetricKeys))
  }

  const downloadChartSvgSnapshot = (chart: ChartConfig) => {
    const card = chartCardRefs.current[chart.id]
    if (!card) return
    const displayTitle = displayTitleForChart(chart)
    const snapshot = chartSnapshotSvg(card, { ...chart, title: displayTitle })
    if (!snapshot) return
    downloadTextFile(`${safeFileName(displayTitle)}-snapshot.svg`, snapshot, 'image/svg+xml;charset=utf-8')
  }

  const exportChartCsv = (chart: ChartConfig) => {
    downloadTextFile(`${safeFileName(displayTitleForChart(chart))}-data.csv`, graphCsvForChart(chart, chartDataFor(chart), groupNames, latencyKeys, false, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey), 'text/csv;charset=utf-8')
  }

  const copyLatestValues = (chart: ChartConfig) => {
    const chartData = chartDataFor(chart)
    const chartLatest = chartData[chartData.length - 1] || latest
    const visibleKeys = chartMetricKeys(chart, groupNames, latencyKeys, false, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey)
    const routeLatestValues = chart.id === 'network_latency'
      ? latestRouteLatencyValues(chartData, visibleKeys)
      : latencyLatestValues
    const text = latestGraphValuesText({ ...chart, title: displayTitleForChart(chart) }, chartLatest, groupNames, latencyKeys, routeLatestValues, overlayMetricKeysForChart(chart), routeLatencyMode, activeRouteLatencyKey)
    if (navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(text)
    }
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
    setVisibleLoadImpactChartIds([])
    if (initialPresetId) {
      const next = chartStateForMonitorPreset(initialPresetId, allChartIds)
      setChartOrder(next.chartOrder)
      setHiddenChartIds(next.hiddenChartIds)
    } else {
      setChartOrder(allChartIds)
      setHiddenChartIds(defaultHiddenChartIds(allChartConfigs, []))
    }
    setChartDensity(densityFallback)
  }

  const applyMonitorPreset = (presetId: string) => {
    if (!presetId) {
      resetCharts()
      return
    }
    setVisibleLoadImpactChartIds([])
    const next = chartStateForMonitorPreset(presetId, allChartIds)
    setChartOrder(next.chartOrder)
    setHiddenChartIds(next.hiddenChartIds)
  }

  const setResponseTimeNetworkLatencyIncluded = (included: boolean) => {
    setResponseTimeNetworkMode(included ? 'end_to_end' : 'application')
  }

  const renderChartConfigurationPanel = () => (
    <div className="charts-config-panel">
      <div className="charts-config-panel-section">
        <div>
          <h4>Graph Cards</h4>
          <span>Add or remove graph cards</span>
        </div>
        <div className="charts-config-option-list">
          {orderedCharts.map(chart => {
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
      <div className="charts-config-panel-section">
        <div>
          <h4>Display Window</h4>
          <span>Graph data range</span>
        </div>
        <div className="time-range-selector" role="group" aria-label="Graph display window">
          <button className={`btn-chip ${timeRange === '3m' ? 'active' : ''}`} type="button" onClick={() => onTimeRangeChange('3m')}>3m</button>
          <button className={`btn-chip ${timeRange === '10m' ? 'active' : ''}`} type="button" onClick={() => onTimeRangeChange('10m')}>10m</button>
          <button className={`btn-chip ${timeRange === 'all' ? 'active' : ''}`} type="button" onClick={() => onTimeRangeChange('all')}>Max</button>
        </div>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Zoom</h4>
          <span>Graph density</span>
        </div>
        <div className="time-range-selector chart-segment-toggle" role="group" aria-label="Graph density">
          <button
            className={`btn-chip ${chartDensity === 'standard' ? 'active' : ''}`}
            type="button"
            onClick={() => setChartDensity('standard')}
            aria-pressed={chartDensity === 'standard'}
          >
            Standard
          </button>
          {commandCenterSurface && (
            <button
              className={`btn-chip ${chartDensity === 'command_center' ? 'active' : ''}`}
              type="button"
              onClick={() => setChartDensity('command_center')}
              aria-pressed={chartDensity === 'command_center'}
            >
              Compact
            </button>
          )}
          <button
            className={`btn-chip ${chartDensity === 'six_up' ? 'active' : ''}`}
            type="button"
            onClick={() => setChartDensity('six_up')}
            aria-pressed={chartDensity === 'six_up'}
          >
            6 Graphs
          </button>
        </div>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Role View</h4>
          <span>Graph preset</span>
        </div>
        <select
          className="chart-role-select"
          aria-label="Graph role view"
          value={activeMonitorPresetId}
          onChange={event => applyMonitorPreset(event.currentTarget.value)}
        >
          <option value="">Manual view</option>
          {monitorPresets.map(preset => (
            <option key={preset.id} value={preset.id}>{preset.label}</option>
          ))}
        </select>
      </div>
      <div className="charts-config-panel-section">
        <div>
          <h4>Display Time</h4>
          <span>Graph x-axis labels</span>
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
    return (
      <>
        {showVuserAxis && (
          <YAxis
            yAxisId="vusers"
            orientation="right"
            stroke={overlayMetricRows.vusers.color}
            fontSize={11}
            width={72}
            domain={[0, 'auto']}
            label={rightAxisLabel('VUsers')}
          />
        )}
        {rateAxisLabel && (
          <YAxis
            yAxisId="rateOverlay"
            orientation="right"
            stroke="var(--text-muted)"
            fontSize={11}
            width={86}
            domain={[0, 'auto']}
            label={rightAxisLabel(rateAxisLabel)}
          />
        )}
      </>
    )
  }

  const renderMetricAxis = (chart: ChartConfig, visibleMetricKeys: string[], domain?: ChartDomain) => (
    <YAxis
      yAxisId={metricYAxisId(chart, visibleMetricKeys)}
      stroke="var(--text-muted)"
      fontSize={11}
      width={82}
      domain={domain}
      label={leftAxisLabel(chart.id === 'network_latency' && routeLatencyMode === 'stability' ? 'Jitter (ms)' : chartPrimaryYAxisLabel(chart))}
    />
  )

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
        width={86}
        domain={[0, 100]}
        label={rightAxisLabel('Success (%)')}
      />
    ) : null
  )

  const renderOverlayLines = (chart: ChartConfig, visibleMetricKeys: string[]) => {
    const visibleOverlayKeys = visibleOverlayMetricKeys(chart, visibleMetricKeys)
    return visibleOverlayKeys.map(key => {
      const row = overlayMetricRows[key]
      return (
        <Line
          key={`overlay-${key}`}
          yAxisId={key === 'vusers' ? 'vusers' : 'rateOverlay'}
          type="monotone"
          dataKey={key}
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

  const renderMetricsTable = (chart: ChartConfig, rows: ChartMetricRow[], visibleMetricKeys: string[], chartData: MetricsHistoryPoint[]) => {
    if (rows.length === 0) {
      return (
        <div className="chart-metrics-empty">
          No measurements available
        </div>
      )
    }
    const canFilter = rows.length > 1
    const allVisible = visibleMetricKeys.length === rows.length
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
              <th>Metric</th>
              <th>Latest</th>
              <th>Avg</th>
              <th>Min</th>
              <th>Max</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(row => {
              const summary = chartMetricSummary(chartData, row.key)
              const visible = visibleMetricKeys.includes(row.key)
              return (
                <tr key={row.key} className={visible ? '' : 'muted'}>
                  {canFilter && (
                    <td className="metric-filter-col">
                      <input
                        type="checkbox"
                        checked={visible}
                        disabled={visible && visibleMetricKeys.length === 1}
                        aria-label={`Show ${row.label}`}
                        onChange={() => toggleMetricRow(chart, rows, row.key)}
                      />
                    </td>
                  )}
                  <td>
                    <span className="chart-metric-name">
                      <span className="dot" style={{ background: row.color }}></span>
                      <span>{row.label}</span>
                    </span>
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
                    <td>
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

  const densityLabel = chartDensity === 'six_up'
    ? '6 Graphs'
    : chartDensity === 'command_center'
      ? 'Compact'
      : 'Standard'
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

      <div className={`charts-toolbar${commandCenterSurface ? ' command-center-charts-toolbar' : ''}`} aria-label="Final graph controls">
        <div>
          <h3 className="chart-title">Runtime Graphs</h3>
          <p className="archive-section-copy">{commandCenterSurface ? 'Editable live graph wall for this command-center view.' : 'Live, archived, and load-under graphs for the selected run.'}</p>
        </div>
        <div className="charts-toolbar-actions">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => setChartDrawerOpen(true)}
            aria-expanded={chartDrawerOpen}
          >
            Configure Graphs
          </button>
          <div className="charts-toolbar-badges">
            <span className="archive-chip">{visibleCharts.length} graph{visibleCharts.length === 1 ? '' : 's'}</span>
            <span className="archive-chip">{densityLabel}</span>
            <span className="archive-chip">{timeAxisMode === 'relative' ? 'Relative time' : 'Clock time'}</span>
            {loadImpactLoading && <span className="archive-chip">Loading load metrics</span>}
            {!loadImpactLoading && loadImpactCharts.length > 0 && <span className="archive-chip">{loadImpactCharts.length} load metrics</span>}
          </div>
        </div>
      </div>

      {loadImpactError && <p className="scenario-script-status error">{loadImpactError}</p>}

      {visibleCharts.length === 0 ? (
        <div className="charts-empty">
          <span>All graphs are hidden</span>
          <button className="btn btn-secondary btn-sm" type="button" onClick={resetCharts}>Reset Graphs</button>
        </div>
      ) : (
        <div className={gridClassName}>
          {displayedCharts.map((chart) => {
            const chartData = chartDataFor(chart)
            const chartHeight = expandedChart === chart.id
              ? commandCenterSurface ? 360 : 400
              : chartDensity === 'six_up'
                ? 132
                : chartDensity === 'command_center'
                  ? 170
                  : 200
            const metricRows = metricRowsForChart(chart)
            const visibleMetricKeys = visibleMetricKeysForChart(chart, metricRows)
            const visibleOverlayKeysForTitle = visibleOverlayMetricKeys(chart, visibleMetricKeys)
            const primaryMetricKeysForDomain = visibleMetricKeys.filter(key => (
              !visibleOverlayKeysForTitle.includes(key as OverlayMetricKey)
              && key !== routeLatencySuccessRateKey
            ))
            const metricAxisDomain = visibleMetricDomain(chartData, primaryMetricKeysForDomain, chart.domain)
            const chartTitle = chartTitleWithOverlays(chart, visibleOverlayKeysForTitle)
            const baseChartMargin = chartMarginForOverlayKeys(visibleOverlayKeysForTitle) ?? {}
            const chartMargin = chart.id === 'network_latency' && routeLatencyMode === 'stability' && visibleMetricKeys.includes(routeLatencySuccessRateKey)
              ? { ...baseChartMargin, right: Math.max(baseChartMargin.right ?? 0, 88) }
              : baseChartMargin
            const metricAxisIdForChart = metricYAxisId(chart, visibleMetricKeys)
            const showMetric = (key: string) => visibleMetricKeys.includes(key)
            const showDetails = expandedChart === chart.id || chartDensity === 'standard'
            return (
              <div
                key={chart.id}
                ref={element => {
                  chartCardRefs.current[chart.id] = element
                }}
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
                      className="btn-icon btn-expand"
                      type="button"
                      onClick={() => onExpandedChartChange(expandedChart === chart.id ? null : chart.id)}
                    >
                      {expandedChart === chart.id ? '↙' : '↗'}
                    </button>
                  </div>
                </div>
                <ResponsiveContainer width="100%" height={chartHeight}>
            {chart.type === 'area' ? (
              <ComposedChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                {chart.dataKey && showMetric(chart.dataKey) && (
                  <Area yAxisId={metricAxisIdForChart} type="monotone" dataKey={chart.dataKey} name="Total" stroke={chart.color!} fill={chart.color!} fillOpacity={0.15} isAnimationActive={false} />
                )}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-load-impact' ? (
              <LineChart
                data={chartData}
                margin={chartMargin}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderLoadImpactAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderLoadImpactTooltip(chart)}
                {renderSharedCursor(chartData)}
                {metricRows
                  .filter(row => !overlayMetricKeysForChart(chart).includes(row.key as OverlayMetricKey))
                  .map((row) => showMetric(row.key) && (
                    <Line
                      key={row.key}
                      yAxisId={metricAxisIdForChart}
                      type="monotone"
                      dataKey={row.key}
                      name={row.label}
                      stroke={row.color}
                      dot={{ r: row.key === 'average' ? 3 : 2 }}
                      activeDot={{ r: 5 }}
                      strokeWidth={row.key === 'average' ? 2.5 : 1.5}
                      strokeDasharray={row.key === 'average' ? undefined : '4 2'}
                      connectNulls
                      isAnimationActive={false}
                    />
                  ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
              </LineChart>
            ) : chart.type === 'line-multi' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                {isMultiGroup && <Legend verticalAlign="bottom" height={36} />}
                {chart.totalKey && showMetric(chart.totalKey) && (
                  <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={chart.totalKey} name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
                )}
                {surface === 'command-center' && chartData.some(point => typeof point.targetTps === 'number') && (
                  <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="targetTps" name="Target" stroke="var(--warning)" dot={false} strokeWidth={1.75} strokeDasharray="5 3" isAnimationActive={false} />
                )}
                {isMultiGroup && groupNames.map((name, index) => showMetric(`${name}_tps`) && (
                  <Line key={name} yAxisId={metricAxisIdForChart} type="monotone" dataKey={`${name}_tps`} name={name} stroke={groupColors[index % groupColors.length]} dot={false} isAnimationActive={false} />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'line-latency' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                <Legend verticalAlign="bottom" height={36} />
                {showMetric('avgMs') && (
                  <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="avgMs" name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
                )}
                {isMultiGroup && groupNames.map((name, index) => showMetric(`${name}_avgMs`) && (
                  <Line key={name} yAxisId={metricAxisIdForChart} type="monotone" dataKey={`${name}_avgMs`} name={name} stroke={groupColors[index % groupColors.length]} dot={false} strokeWidth={2} isAnimationActive={false} />
                ))}
                {showMetric('p50') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="p50" name="P50" stroke="var(--chart-5)" dot={false} strokeWidth={1.25} strokeDasharray="3 3" isAnimationActive={false} />}
                {showMetric('p90') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="p90" name="P90" stroke="var(--chart-3)" dot={false} strokeWidth={1.25} strokeDasharray="3 3" isAnimationActive={false} />}
                {showMetric('p95') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="p95" name="P95" stroke="var(--chart-2)" dot={false} strokeWidth={1.25} strokeDasharray="4 2" isAnimationActive={false} />}
                {showMetric('p99') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="p99" name="P99" stroke="var(--chart-4)" dot={false} strokeWidth={1.25} strokeDasharray="4 2" isAnimationActive={false} />}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'area-multi' ? (
              <ComposedChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                <Legend verticalAlign="bottom" height={36} />
                {chart.keys!.filter(key => showMetric(key.k)).map((key) => (
                  <Area key={key.k} yAxisId={metricAxisIdForChart} type="monotone" dataKey={key.k} name={key.n} stroke={key.c} fill={key.c} fillOpacity={0.2} isAnimationActive={false} />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-impact' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                {metricRows.map((row) => showMetric(row.key) && (
                  <Line
                    key={row.key}
                    yAxisId={metricAxisIdForChart}
                    type="monotone"
                    dataKey={row.key}
                    name={row.label}
                    stroke={row.color}
                    dot={false}
                    strokeWidth={2}
                    isAnimationActive={false}
                  />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'line-network' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderRouteLatencySuccessAxis(visibleMetricKeys)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                <Legend verticalAlign="bottom" height={36} />
                {metricRows.map((row) => showMetric(row.key) && (
                    <Line
                      key={row.key}
                      yAxisId={row.key === routeLatencySuccessRateKey ? 'successRate' : metricAxisIdForChart}
                      type="monotone"
                      dataKey={row.key}
                      name={row.label}
                      stroke={row.color}
                      dot={false}
                      strokeWidth={2}
                      isAnimationActive={false}
                    />
                  ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : (
              <LineChart
                data={chartData}
                syncId="metrics"
                margin={chartMargin}
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                {renderMetricAxis(chart, visibleMetricKeys, metricAxisDomain)}
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {renderSharedCursor(chartData)}
                <Legend />
                {showMetric('netSent') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="netSent" name="Net Egress" stroke="var(--chart-1)" dot={false} strokeWidth={2} isAnimationActive={false} />}
                {showMetric('netRecv') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="netRecv" name="Net Ingress" stroke="var(--chart-2)" dot={false} strokeWidth={2} isAnimationActive={false} />}
                {showMetric('diskRead') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="diskRead" name="Disk Read" stroke="var(--chart-3)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />}
                {showMetric('diskWrite') && <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey="diskWrite" name="Disk Write" stroke="var(--chart-6)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            )}
          </ResponsiveContainer>

          {chart.id === 'network_latency' && showDetails && renderRouteLatencyDetails()}
          {chart.id === 'generator_impact' && showDetails && renderGeneratorImpactDetails()}
          {showDetails && renderMetricsTable(chart, metricRows, visibleMetricKeys, chartData)}
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
          <aside className="charts-config-drawer" role="dialog" aria-modal="true" aria-label="Final graph configuration">
            <div className="charts-config-drawer-header">
              <div>
                <h3>Graph Configuration</h3>
                <p>Choose graph cards, presets, density, and time display for the final graph area.</p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setChartDrawerOpen(false)}>Close</button>
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
          aria-label={`${contextChart.title} actions`}
          onClick={event => event.stopPropagation()}
          onContextMenu={event => {
            event.preventDefault()
            event.stopPropagation()
          }}
        >
          <div className="chart-context-menu-title">{displayTitleForChart(contextChart)}</div>
          <button type="button" role="menuitem" onClick={() => runChartAction(onResetZoom)}>Reset Zoom</button>
          {(['vusers', 'avgMs', 'tps'] as OverlayMetricKey[])
            .filter(key => isOverlayMetricAvailable(contextChart, key))
            .map(key => (
              <button key={key} type="button" role="menuitem" onClick={() => runChartAction(() => toggleOverlayMetric(contextChart, key))}>
                {isOverlayMetricEnabled(contextChart, key) ? `Hide ${overlayMetricMenuLabels[key]} Overlay` : `Overlay ${overlayMetricMenuLabels[key]}`}
              </button>
            ))}
          {contextChart.id === 'latency' && (
            <button type="button" role="menuitem" onClick={() => runChartAction(() => setResponseTimeNetworkLatencyIncluded(responseTimeNetworkMode === 'application'))}>
              {responseTimeNetworkMode === 'application' ? 'Include Network Latency' : 'Remove Network Latency'}
            </button>
          )}
          <button type="button" role="menuitem" onClick={() => runChartAction(() => onExpandedChartChange(expandedChart === contextChart.id ? null : contextChart.id))}>
            {expandedChart === contextChart.id ? 'Collapse Graph' : 'Expand Graph'}
          </button>
          <button type="button" role="menuitem" onClick={() => runChartAction(() => downloadChartSvgSnapshot(contextChart))}>Download Snapshot</button>
          <button type="button" role="menuitem" onClick={() => runChartAction(() => exportChartCsv(contextChart))}>Export Graph Data</button>
          <button type="button" role="menuitem" onClick={() => runChartAction(() => copyLatestValues(contextChart))}>Copy Latest Values</button>
          <button type="button" role="menuitem" onClick={() => runChartAction(() => toggleChartVisibility(contextChart.id))}>Hide Graph</button>
        </div>
      )}
    </>
  )
}
