import { useEffect, useMemo, useRef, useState, type ComponentProps, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent } from 'react'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  LineChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { MetricsHistoryPoint } from '../types'
import LoadImpactHeatmap from './LoadImpactHeatmap'
import { routeLatencySeriesLabel } from './latencySeries'
import { ExecutionChartTimeRange } from './testExecutionChartZoomModel'

type ChartDomain = ComponentProps<typeof YAxis>['domain']

export interface ChartConfig {
  id: string
  title: string
  type: 'area' | 'line-multi' | 'line-latency' | 'area-multi' | 'line-io' | 'line-network'
  dataKey?: string
  color?: string
  totalKey?: string
  keys?: Array<{ k: string; n: string; c: string }>
  domain?: ChartDomain
}

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
}

export interface ChartMetricRow {
  key: string
  label: string
  color: string
  unit?: string
}

export interface ChartMetricSummary {
  latest?: number
  average?: number
  min?: number
  max?: number
  samples: number
}

type ChartTimeAxisMode = 'clock' | 'relative'
type ChartDensityMode = 'standard' | 'six_up'
type ResponseTimeNetworkMode = 'end_to_end' | 'application'
type ChartDropPosition = 'before' | 'after'
type OverlayMetricKey = 'vusers' | 'avgMs' | 'tps'

const chartConfigs: ChartConfig[] = [
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
  { id: 'network_latency', title: 'Route Latency (ms)', type: 'line-network' },
]

const overlayMetricRows: Record<OverlayMetricKey, ChartMetricRow> = {
  vusers: { key: 'vusers', label: 'Active VUsers', color: 'var(--chart-1)' },
  avgMs: { key: 'avgMs', label: 'ART', color: 'var(--chart-4)', unit: 'ms' },
  tps: { key: 'tps', label: 'TPS', color: 'var(--chart-5)' },
}

const overlayMetricMenuLabels: Record<OverlayMetricKey, string> = {
  vusers: 'VUsers',
  avgMs: 'ART',
  tps: 'TPS',
}

function isOverlayMetricAvailable(chart: ChartConfig, key: OverlayMetricKey) {
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

function normalizedChartOrder(order: string[]) {
  const knownIds = chartConfigs.map(chart => chart.id)
  const retained = order.filter(id => knownIds.includes(id))
  const added = knownIds.filter(id => !retained.includes(id))
  return [...retained, ...added]
}

export const monitorPresets = [
  { id: 'run_operator', label: 'Run Operator', chartIds: ['vusers', 'tps', 'latency', 'errors'] },
  { id: 'performance_engineer', label: 'Performance Engineer', chartIds: ['latency', 'tps', 'errors', 'cpu', 'mem', 'io', 'network_latency', 'vusers'] },
  { id: 'platform_admin', label: 'Platform Admin', chartIds: ['cpu', 'mem', 'io', 'network_latency', 'vusers', 'tps'] },
  { id: 'release_approver', label: 'Release Approver', chartIds: ['latency', 'errors', 'tps'] },
] as const

function readStoredArray(key: string) {
  if (typeof window === 'undefined') return []
  try {
    const stored = window.localStorage.getItem(key)
    if (!stored) return []
    const parsed = JSON.parse(stored)
    return Array.isArray(parsed) ? parsed.filter(value => typeof value === 'string') : []
  } catch {
    return []
  }
}

function readStoredRecordArray(key: string) {
  if (typeof window === 'undefined') return {}
  try {
    const stored = window.localStorage.getItem(key)
    if (!stored) return {}
    const parsed = JSON.parse(stored)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(
      Object.entries(parsed)
        .filter((entry): entry is [string, unknown[]] => typeof entry[0] === 'string' && Array.isArray(entry[1]))
        .map(([recordKey, value]) => [recordKey, value.filter(item => typeof item === 'string')]),
    ) as Record<string, string[]>
  } catch {
    return {}
  }
}

function readStoredTimeAxisMode(): ChartTimeAxisMode {
  if (typeof window === 'undefined') return 'clock'
  return window.localStorage.getItem('apexload.execution.chartTimeAxisMode') === 'relative'
    ? 'relative'
    : 'clock'
}

function readStoredChartDensity(): ChartDensityMode {
  if (typeof window === 'undefined') return 'standard'
  return window.localStorage.getItem('apexload.execution.chartDensity') === 'six_up'
    ? 'six_up'
    : 'standard'
}

function readStoredResponseTimeNetworkMode(): ResponseTimeNetworkMode {
  if (typeof window === 'undefined') return 'end_to_end'
  return window.localStorage.getItem('apexload.execution.responseTimeNetworkMode') === 'application'
    ? 'application'
    : 'end_to_end'
}

function sameStringArray(left: string[], right: string[]) {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

function sameStringSet(left: string[], right: string[]) {
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
      .filter(key => key.startsWith('latency_') && typeof point[key] === 'number')
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

export function chartMetricKeys(
  chart: ChartConfig,
  groupNames: string[],
  latencyKeys: string[],
  includeVuserOverlay = false,
  overlayMetricKeys: OverlayMetricKey[] = [],
) {
  let keys: string[]
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
    case 'network_latency':
      keys = latencyKeys
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
): ChartMetricRow[] {
  let rows: ChartMetricRow[]
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
        { key: 'avgMs', label: 'Total', color: 'var(--text-muted)', unit: 'ms' },
        ...groupNames.map((name, index) => ({ key: `${name}_avgMs`, label: name, color: groupColors[index % groupColors.length], unit: 'ms' })),
        { key: 'p50', label: 'P50', color: 'var(--chart-5)', unit: 'ms' },
        { key: 'p90', label: 'P90', color: 'var(--chart-3)', unit: 'ms' },
        { key: 'p95', label: 'P95', color: 'var(--chart-2)', unit: 'ms' },
        { key: 'p99', label: 'P99', color: 'var(--chart-4)', unit: 'ms' },
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
    case 'network_latency':
      rows = latencyKeys.map((key, index) => ({
        key,
        label: routeLatencySeriesLabel(key),
        color: groupColors[index % groupColors.length],
        unit: 'ms',
      }))
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

export function graphCsvForChart(
  chart: ChartConfig,
  data: MetricsHistoryPoint[],
  groupNames: string[],
  latencyKeys: string[],
  includeVuserOverlay = false,
  overlayMetricKeys: OverlayMetricKey[] = [],
) {
  const keys = ['time', ...chartMetricKeys(chart, groupNames, latencyKeys, includeVuserOverlay, overlayMetricKeys)]
  const rows = [
    keys.map(escapeCsvCell).join(','),
    ...data.map(point => keys.map(key => escapeCsvCell(point[key])).join(',')),
  ]
  return rows.join('\n')
}

function escapeCsvCell(value: unknown) {
  if (value === undefined || value === null) return ''
  const text = String(value)
  if (!/[",\n\r]/.test(text)) return text
  return `"${text.replace(/"/g, '""')}"`
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
) {
  const keys = chartMetricKeys(chart, groupNames, latencyKeys, false, overlayMetricKeys)
  const lines = [`${chart.title}`]
  keys.forEach(key => {
    const value = chart.id === 'network_latency' ? latencyLatestValues[key] : latest[key]
    lines.push(`${chart.id === 'network_latency' ? routeLatencySeriesLabel(key) : key}: ${value ?? 'n/a'}`)
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
}: Props) {
  const [chartOrder, setChartOrder] = useState<string[]>(() => readStoredArray('apexload.execution.chartOrder'))
  const [hiddenChartIds, setHiddenChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.hiddenCharts'))
  const [vuserOverlayChartIds, setVuserOverlayChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.vuserOverlayCharts'))
  const [artOverlayChartIds, setArtOverlayChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.artOverlayCharts'))
  const [tpsOverlayChartIds, setTpsOverlayChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.tpsOverlayCharts'))
  const [chartMetricFilters, setChartMetricFilters] = useState<Record<string, string[]>>(() => readStoredRecordArray('apexload.execution.chartMetricFilters'))
  const [timeAxisMode, setTimeAxisMode] = useState<ChartTimeAxisMode>(() => readStoredTimeAxisMode())
  const [chartDensity, setChartDensity] = useState<ChartDensityMode>(() => readStoredChartDensity())
  const [responseTimeNetworkMode, setResponseTimeNetworkMode] = useState<ResponseTimeNetworkMode>(() => readStoredResponseTimeNetworkMode())
  const [chartMenu, setChartMenu] = useState<{ chartId: string; x: number; y: number } | null>(null)
  const [hoveredChartId, setHoveredChartId] = useState<string | null>(null)
  const [draggingChartId, setDraggingChartId] = useState<string | null>(null)
  const [dragOverChartId, setDragOverChartId] = useState<string | null>(null)
  const chartCardRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const chartMenuRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    setChartOrder(prev => normalizedChartOrder(prev))
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.chartOrder', JSON.stringify(chartOrder))
  }, [chartOrder])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.hiddenCharts', JSON.stringify(hiddenChartIds))
  }, [hiddenChartIds])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.vuserOverlayCharts', JSON.stringify(vuserOverlayChartIds))
  }, [vuserOverlayChartIds])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.artOverlayCharts', JSON.stringify(artOverlayChartIds))
  }, [artOverlayChartIds])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.tpsOverlayCharts', JSON.stringify(tpsOverlayChartIds))
  }, [tpsOverlayChartIds])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.chartMetricFilters', JSON.stringify(chartMetricFilters))
  }, [chartMetricFilters])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.chartTimeAxisMode', timeAxisMode)
  }, [timeAxisMode])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.chartDensity', chartDensity)
  }, [chartDensity])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.execution.responseTimeNetworkMode', responseTimeNetworkMode)
  }, [responseTimeNetworkMode])

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

  const orderedCharts = useMemo(() => {
    const byId = new Map(chartConfigs.map(chart => [chart.id, chart]))
    const ordered = chartOrder
      .map(id => byId.get(id))
      .filter((chart): chart is ChartConfig => Boolean(chart))
    return ordered.length ? ordered : chartConfigs
  }, [chartOrder])
  const activeMonitorPresetId = useMemo(() => {
    const currentOrder = orderedCharts.map(chart => chart.id)
    return monitorPresets.find(preset => {
      const presetState = chartStateForMonitorPreset(preset.id)
      return sameStringArray(presetState.chartOrder, currentOrder)
        && sameStringSet(presetState.hiddenChartIds, hiddenChartIds.filter(id => chartConfigs.some(chart => chart.id === id)))
    })?.id ?? ''
  }, [hiddenChartIds, orderedCharts])
  const latencyKeys = useMemo(() => routeLatencySeriesKeys(filteredData), [filteredData])
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

  if (filteredData.length <= 1) {
    return null
  }

  const latest = filteredData[filteredData.length - 1] || {}
  const visibleCharts = orderedCharts.filter(chart => !hiddenChartIds.includes(chart.id))
  const hiddenCharts = orderedCharts.filter(chart => hiddenChartIds.includes(chart.id))
  const displayedCharts = expandedChart
    ? [
        ...visibleCharts.filter(chart => chart.id === expandedChart),
        ...visibleCharts.filter(chart => chart.id !== expandedChart),
      ]
    : visibleCharts
  const contextChart = chartMenu ? visibleCharts.find(chart => chart.id === chartMenu.chartId) : undefined
  const chartDataFor = (chart: ChartConfig) => chart.id === 'latency' ? responseTimeChartData : filteredData

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
      tickFormatter={formatXAxisLabel}
    />
  )

  const renderTooltip = (chartId: string) => {
    const showTimeLabel = hoveredChartId === chartId
    return (
      <Tooltip
        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: showTimeLabel ? 4 : 0, display: showTimeLabel ? undefined : 'none' }}
        labelFormatter={(value) => showTimeLabel ? formatXAxisLabel(value) : ''}
        isAnimationActive={false}
      />
    )
  }

  const handleChartMouseMove = (chartId: string, activeLabel?: string | number) => {
    setHoveredChartId(current => current === chartId ? current : chartId)
    onChartMouseMove(activeLabel)
  }

  const handleChartMouseLeave = (chartId: string) => {
    setHoveredChartId(current => current === chartId ? null : current)
  }

  const toggleChartVisibility = (chartId: string) => {
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

  const metricRowsForChart = (chart: ChartConfig) => chartMetricRows(chart, groupNames, latencyKeys, groupColors, false, overlayMetricKeysForChart(chart))

  const visibleMetricKeysForChart = (chart: ChartConfig, rows = metricRowsForChart(chart)) => {
    const availableKeys = rows.map(row => row.key)
    const storedKeys = chartMetricFilters[chart.id]?.filter(key => availableKeys.includes(key)) ?? []
    return storedKeys.length > 0 ? storedKeys : availableKeys
  }

  const visibleOverlayMetricKeys = (chart: ChartConfig, visibleMetricKeys: string[]) => {
    const overlayKeys = overlayMetricKeysForChart(chart)
    return overlayKeys.filter(key => visibleMetricKeys.includes(key))
  }

  const isVuserOverlayVisible = (chart: ChartConfig, visibleMetricKeys: string[]) => visibleOverlayMetricKeys(chart, visibleMetricKeys).includes('vusers')

  const metricYAxisId = (chart: ChartConfig, visibleMetricKeys: string[]) => visibleOverlayMetricKeys(chart, visibleMetricKeys).length > 0 ? 'metric' : undefined

  const showAllMetricRows = (chart: ChartConfig, rows: ChartMetricRow[]) => {
    setChartMetricFilters(prev => ({ ...prev, [chart.id]: rows.map(row => row.key) }))
  }

  const toggleMetricRow = (chart: ChartConfig, rows: ChartMetricRow[], key: string) => {
    const availableKeys = rows.map(row => row.key)
    setChartMetricFilters(prev => {
      const currentKeys = prev[chart.id]?.filter(candidate => availableKeys.includes(candidate)) ?? availableKeys
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

  const downloadChartSvgSnapshot = (chart: ChartConfig) => {
    const card = chartCardRefs.current[chart.id]
    if (!card) return
    const snapshot = chartSnapshotSvg(card, chart)
    if (!snapshot) return
    downloadTextFile(`${safeFileName(chart.title)}-snapshot.svg`, snapshot, 'image/svg+xml;charset=utf-8')
  }

  const exportChartCsv = (chart: ChartConfig) => {
    downloadTextFile(`${safeFileName(chart.title)}-data.csv`, graphCsvForChart(chart, chartDataFor(chart), groupNames, latencyKeys, false, overlayMetricKeysForChart(chart)), 'text/csv;charset=utf-8')
  }

  const copyLatestValues = (chart: ChartConfig) => {
    const chartData = chartDataFor(chart)
    const chartLatest = chartData[chartData.length - 1] || latest
    const text = latestGraphValuesText(chart, chartLatest, groupNames, latencyKeys, latencyLatestValues, overlayMetricKeysForChart(chart))
    if (navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(text)
    }
  }

  const reorderChart = (sourceChartId: string, targetChartId: string, position: ChartDropPosition) => {
    if (sourceChartId === targetChartId) return
    setChartOrder(prev => {
      const next = normalizedChartOrder(prev)
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
      const next = normalizedChartOrder(prev)
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
      const next = normalizedChartOrder(prev)
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
    setChartOrder(chartConfigs.map(chart => chart.id))
    setHiddenChartIds([])
  }

  const applyMonitorPreset = (presetId: string) => {
    if (!presetId) {
      resetCharts()
      return
    }
    const next = chartStateForMonitorPreset(presetId)
    setChartOrder(next.chartOrder)
    setHiddenChartIds(next.hiddenChartIds)
  }

  const setResponseTimeNetworkLatencyIncluded = (included: boolean) => {
    setResponseTimeNetworkMode(included ? 'end_to_end' : 'application')
  }

  const renderOverlayAxes = (chart: ChartConfig, visibleMetricKeys: string[]) => {
    const visibleOverlayKeys = visibleOverlayMetricKeys(chart, visibleMetricKeys)
    const showVuserAxis = visibleOverlayKeys.includes('vusers')
    const showRateAxis = visibleOverlayKeys.some(key => key === 'avgMs' || key === 'tps')
    return (
      <>
        {showVuserAxis && (
          <YAxis yAxisId="vusers" orientation="right" stroke={overlayMetricRows.vusers.color} fontSize={11} width={42} domain={[0, 'auto']} />
        )}
        {showRateAxis && (
          <YAxis yAxisId="rateOverlay" orientation="right" stroke="var(--text-muted)" fontSize={11} width={42} domain={[0, 'auto']} />
        )}
      </>
    )
  }

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

  return (
    <>
      <div className="charts-customize-panel">
        <div className="charts-customize-panel-section">
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
        <div className="charts-customize-panel-section">
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
        <div className="charts-customize-panel-section">
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
        <div className="charts-customize-panel-section">
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
        <div className="charts-customize-panel-section">
          <div>
            <h4>Reset</h4>
            <span>Default graph layout</span>
          </div>
          <button className="btn-chip chart-reset-pill" type="button" onClick={resetCharts}>
            Reset Graphs
          </button>
        </div>
        {hiddenCharts.length > 0 && (
          <div className="charts-customize-panel-section">
            <div>
              <h4>Hidden Graphs</h4>
              <span>Restore hidden graph cards</span>
            </div>
            <div className="hidden-chart-list">
              {hiddenCharts.map(chart => (
                <button key={chart.id} className="hidden-chart-pill" type="button" onClick={() => toggleChartVisibility(chart.id)}>
                  Show {chart.title}
                </button>
              ))}
            </div>
          </div>
        )}
      </div>

      <LoadImpactHeatmap
        points={filteredData}
        title="Load Impact"
        subtitle="Performance and resource behavior grouped by active VUser load."
        emptyMessage="Load impact appears after live metrics include active VUsers and performance or resource samples."
      />

      {visibleCharts.length === 0 ? (
        <div className="charts-empty">
          <span>All graphs are hidden</span>
          <button className="btn btn-secondary btn-sm" type="button" onClick={resetCharts}>Reset Graphs</button>
        </div>
      ) : (
        <div className={`charts-grid ${chartDensity === 'six_up' ? 'six-up' : ''}`}>
          {displayedCharts.map((chart) => {
            const chartData = chartDataFor(chart)
            const chartHeight = chartDensity === 'six_up' && expandedChart !== chart.id ? 132 : expandedChart === chart.id ? 400 : 200
            const metricRows = metricRowsForChart(chart)
            const visibleMetricKeys = visibleMetricKeysForChart(chart, metricRows)
            const metricAxisIdForChart = metricYAxisId(chart, visibleMetricKeys)
            const showMetric = (key: string) => visibleMetricKeys.includes(key)
            return (
              <div
                key={chart.id}
                ref={element => {
                  chartCardRefs.current[chart.id] = element
                }}
                className={`chart-card ${expandedChart === chart.id ? 'expanded' : ''} ${draggingChartId === chart.id ? 'dragging' : ''} ${dragOverChartId === chart.id ? 'drop-target' : ''}`}
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
                      aria-label={`Rearrange ${chart.title}`}
                      title="Drag to rearrange graph. Use arrow keys to move."
                      onDragStart={event => handleChartDragStart(event, chart.id)}
                      onDragEnd={handleChartDragEnd}
                      onKeyDown={event => handleChartReorderKeyDown(event, chart.id)}
                    />
                    <h3 className="chart-title">{chart.title}</h3>
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
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} domain={chart.domain} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {chart.dataKey && showMetric(chart.dataKey) && (
                  <Area yAxisId={metricAxisIdForChart} type="monotone" dataKey={chart.dataKey} name="Total" stroke={chart.color!} fill={chart.color!} fillOpacity={0.15} isAnimationActive={false} />
                )}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-multi' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                {isMultiGroup && <Legend verticalAlign="bottom" height={36} />}
                {chart.totalKey && showMetric(chart.totalKey) && (
                  <Line yAxisId={metricAxisIdForChart} type="monotone" dataKey={chart.totalKey} name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
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
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
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
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} domain={chart.domain} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                <Legend verticalAlign="bottom" height={36} />
                {chart.keys!.filter(key => showMetric(key.k)).map((key) => (
                  <Area key={key.k} yAxisId={metricAxisIdForChart} type="monotone" dataKey={key.k} name={key.n} stroke={key.c} fill={key.c} fillOpacity={0.2} isAnimationActive={false} />
                ))}
                {renderOverlayLines(chart, visibleMetricKeys)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-network' ? (
              <LineChart
                data={chartData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
                <Legend verticalAlign="bottom" height={36} />
                {latencyKeys.map((key, index) => showMetric(key) && (
                    <Line
                      key={key}
                      yAxisId={metricAxisIdForChart}
                      type="monotone"
                      dataKey={key}
                      name={routeLatencySeriesLabel(key)}
                      stroke={groupColors[index % groupColors.length]}
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
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => handleChartMouseMove(chart.id, event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                {renderTimeAxis()}
                <YAxis yAxisId={metricAxisIdForChart} stroke="var(--text-muted)" fontSize={11} />
                {renderOverlayAxes(chart, visibleMetricKeys)}
                {renderTooltip(chart.id)}
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

          {(chartDensity === 'standard' || expandedChart === chart.id) && renderMetricsTable(chart, metricRows, visibleMetricKeys, chartData)}
            </div>
            )
          })}
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
          <div className="chart-context-menu-title">{contextChart.title}</div>
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
