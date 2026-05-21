import { useEffect, useMemo, useRef, useState, type ComponentProps, type DragEvent, type MouseEvent } from 'react'
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
import { routeLatencySeriesLabel } from './latencySeries'

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
  isMultiGroup: boolean
  groupNames: string[]
  groupColors: string[]
}

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

function moveChartId(ids: string[], draggedId: string, targetId: string) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
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

export function chartMetricKeys(chart: ChartConfig, groupNames: string[], latencyKeys: string[], includeVuserOverlay = false) {
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
        'p50',
        'p90',
        'p99',
        ...groupNames.map(name => `${name}_avgMs`),
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
  if (includeVuserOverlay && chart.id !== 'vusers' && !keys.includes('vusers')) {
    return [...keys, 'vusers']
  }
  return keys
}

export function graphCsvForChart(chart: ChartConfig, data: MetricsHistoryPoint[], groupNames: string[], latencyKeys: string[], includeVuserOverlay = false) {
  const keys = ['time', ...chartMetricKeys(chart, groupNames, latencyKeys, includeVuserOverlay)]
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

function latestGraphValuesText(chart: ChartConfig, latest: MetricsHistoryPoint, groupNames: string[], latencyKeys: string[], latencyLatestValues: Record<string, number | undefined>) {
  const keys = chartMetricKeys(chart, groupNames, latencyKeys)
  const lines = [`${chart.title}`]
  keys.forEach(key => {
    const value = chart.id === 'network_latency' ? latencyLatestValues[key] : latest[key]
    lines.push(`${chart.id === 'network_latency' ? routeLatencySeriesLabel(key) : key}: ${value ?? 'n/a'}`)
  })
  return lines.join('\n')
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
  isMultiGroup,
  groupNames,
  groupColors,
}: Props) {
  const [customizingCharts, setCustomizingCharts] = useState(false)
  const [draggedChartId, setDraggedChartId] = useState<string | null>(null)
  const [chartOrder, setChartOrder] = useState<string[]>(() => readStoredArray('apexload.execution.chartOrder'))
  const [hiddenChartIds, setHiddenChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.hiddenCharts'))
  const [vuserOverlayChartIds, setVuserOverlayChartIds] = useState<string[]>(() => readStoredArray('apexload.execution.vuserOverlayCharts'))
  const [chartMenu, setChartMenu] = useState<{ chartId: string; x: number; y: number } | null>(null)
  const chartCardRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const chartMenuRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const knownIds = chartConfigs.map(chart => chart.id)
    setChartOrder(prev => {
      const retained = prev.filter(id => knownIds.includes(id))
      const added = knownIds.filter(id => !retained.includes(id))
      return [...retained, ...added]
    })
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
  const latencyKeys = useMemo(() => routeLatencySeriesKeys(filteredData), [filteredData])
  const latencyLatestValues = useMemo(() => latestRouteLatencyValues(filteredData, latencyKeys), [filteredData, latencyKeys])

  if (filteredData.length <= 1) {
    return null
  }

  const latest = filteredData[filteredData.length - 1] || {}
  const visibleCharts = orderedCharts.filter(chart => !hiddenChartIds.includes(chart.id))
  const hiddenCharts = orderedCharts.filter(chart => hiddenChartIds.includes(chart.id))
  const contextChart = chartMenu ? visibleCharts.find(chart => chart.id === chartMenu.chartId) : undefined

  const handleChartDragStart = (event: DragEvent<HTMLDivElement>, chartId: string) => {
    if (!customizingCharts) return
    setDraggedChartId(chartId)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', chartId)
  }

  const handleChartDragOver = (event: DragEvent<HTMLDivElement>, targetId: string) => {
    if (!customizingCharts || !draggedChartId || draggedChartId === targetId) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  const handleChartDrop = (event: DragEvent<HTMLDivElement>, targetId: string) => {
    if (!customizingCharts) return
    event.preventDefault()
    const sourceId = event.dataTransfer.getData('text/plain') || draggedChartId
    if (!sourceId) return
    setChartOrder(prev => moveChartId(prev, sourceId, targetId))
    setDraggedChartId(null)
  }

  const toggleChartVisibility = (chartId: string) => {
    setHiddenChartIds(prev =>
      prev.includes(chartId)
        ? prev.filter(id => id !== chartId)
        : [...prev, chartId],
    )
  }

  const isVuserOverlayAvailable = (chart: ChartConfig) => chart.id !== 'vusers'
  const isVuserOverlayEnabled = (chartId: string) => chartId !== 'vusers' && vuserOverlayChartIds.includes(chartId)
  const toggleVuserOverlay = (chartId: string) => {
    if (chartId === 'vusers') return
    setVuserOverlayChartIds(prev =>
      prev.includes(chartId)
        ? prev.filter(id => id !== chartId)
        : [...prev, chartId],
    )
  }

  const openChartMenu = (event: MouseEvent<HTMLDivElement>, chartId: string) => {
    event.preventDefault()
    event.stopPropagation()
    const menuWidth = 224
    const menuHeight = 260
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
    downloadTextFile(`${safeFileName(chart.title)}-data.csv`, graphCsvForChart(chart, filteredData, groupNames, latencyKeys, isVuserOverlayEnabled(chart.id)), 'text/csv;charset=utf-8')
  }

  const copyLatestValues = (chart: ChartConfig) => {
    const text = latestGraphValuesText(chart, latest, groupNames, latencyKeys, latencyLatestValues)
    if (navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(text)
    }
  }

  const resetCharts = () => {
    setChartOrder(chartConfigs.map(chart => chart.id))
    setHiddenChartIds([])
  }

  const applyMonitorPreset = (presetId: string) => {
    const next = chartStateForMonitorPreset(presetId)
    setChartOrder(next.chartOrder)
    setHiddenChartIds(next.hiddenChartIds)
  }

  const renderVuserOverlayAxis = (chart: ChartConfig) => (
    isVuserOverlayEnabled(chart.id) ? (
      <YAxis yAxisId="vusers" orientation="right" stroke="var(--chart-1)" fontSize={11} width={42} domain={[0, 'auto']} />
    ) : null
  )

  const renderVuserOverlayLine = (chart: ChartConfig) => (
    isVuserOverlayEnabled(chart.id) ? (
      <Line
        yAxisId="vusers"
        type="monotone"
        dataKey="vusers"
        name="Active VUsers"
        stroke="var(--chart-1)"
        dot={false}
        strokeWidth={1.75}
        strokeDasharray="5 3"
        isAnimationActive={false}
      />
    ) : null
  )

  return (
    <>
      <div className="charts-customize-bar">
        <div>
          <h3>Graphs</h3>
          <span>{visibleCharts.length} visible / {hiddenCharts.length} hidden</span>
        </div>
        <div className="charts-customize-actions">
          {customizingCharts && (
            <>
              <div className="monitor-preset-buttons">
                {monitorPresets.map(preset => (
                  <button key={preset.id} className="btn btn-secondary btn-sm" type="button" onClick={() => applyMonitorPreset(preset.id)}>
                    {preset.label}
                  </button>
                ))}
              </div>
              <button className="btn btn-secondary btn-sm" type="button" onClick={resetCharts}>
                Reset Graphs
              </button>
            </>
          )}
          <button
            className={`btn btn-sm ${customizingCharts ? 'btn-primary' : 'btn-secondary'}`}
            type="button"
            onClick={() => setCustomizingCharts(value => !value)}
          >
            {customizingCharts ? 'Done' : 'Customize Graphs'}
          </button>
        </div>
      </div>

      {customizingCharts && hiddenCharts.length > 0 && (
        <div className="hidden-chart-list">
          {hiddenCharts.map(chart => (
            <button key={chart.id} className="hidden-chart-pill" type="button" onClick={() => toggleChartVisibility(chart.id)}>
              Show {chart.title}
            </button>
          ))}
        </div>
      )}

      {visibleCharts.length === 0 ? (
        <div className="charts-empty">
          <span>All graphs are hidden</span>
          <button className="btn btn-secondary btn-sm" type="button" onClick={resetCharts}>Reset Graphs</button>
        </div>
      ) : (
        <div className={`charts-grid ${customizingCharts ? 'is-customizing' : ''}`}>
          {visibleCharts.map((chart) => (
            <div
              key={chart.id}
              ref={element => {
                chartCardRefs.current[chart.id] = element
              }}
              className={`chart-card ${expandedChart === chart.id ? 'expanded' : ''} ${customizingCharts ? 'editable' : ''} ${draggedChartId === chart.id ? 'dragging' : ''}`}
              draggable={customizingCharts}
              onContextMenu={event => openChartMenu(event, chart.id)}
              onDragStart={event => handleChartDragStart(event, chart.id)}
              onDragOver={event => handleChartDragOver(event, chart.id)}
              onDrop={event => handleChartDrop(event, chart.id)}
              onDragEnd={() => setDraggedChartId(null)}
            >
              <div className="chart-card-header">
                <div className="chart-title-wrap">
                  {customizingCharts && <span className="chart-drag-handle" title="Drag to reorder">::</span>}
                  <h3 className="chart-title">{chart.title}</h3>
                </div>
                <div className="chart-card-actions">
                  {customizingCharts && (
                    <button className="btn-icon btn-hide-chart" type="button" onClick={() => toggleChartVisibility(chart.id)} title="Hide graph">
                      Hide
                    </button>
                  )}
                  <button
                    className="btn-icon btn-expand"
                    type="button"
                    disabled={customizingCharts}
                    onClick={() => onExpandedChartChange(expandedChart === chart.id ? null : chart.id)}
                  >
                    {expandedChart === chart.id ? '↙' : '↗'}
                  </button>
                </div>
          </div>
          <ResponsiveContainer width="100%" height={expandedChart === chart.id ? 400 : 200}>
            {chart.type === 'area' ? (
              <ComposedChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} domain={chart.domain} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Area yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey={chart.dataKey!} name="Total" stroke={chart.color!} fill={chart.color!} fillOpacity={0.15} isAnimationActive={false} />
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-multi' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                {isMultiGroup && <Legend verticalAlign="bottom" height={36} />}
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey={chart.totalKey!} name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
                {isMultiGroup && groupNames.map((name, index) => (
                  <Line key={name} yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey={`${name}_tps`} name={name} stroke={groupColors[index % groupColors.length]} dot={false} isAnimationActive={false} />
                ))}
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'line-latency' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="p50" name="P50" stroke="var(--chart-5)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="p90" name="P90" stroke="var(--chart-3)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="p99" name="P99" stroke="var(--chart-4)" dot={false} strokeWidth={1.5} strokeDasharray="4 2" isAnimationActive={false} />
                {isMultiGroup && groupNames.map((name, index) => (
                  <Line key={name} yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey={`${name}_avgMs`} name={`${name} avg`} stroke={groupColors[index % groupColors.length]} dot={false} strokeWidth={1.5} strokeDasharray="2 2" isAnimationActive={false} />
                ))}
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'area-multi' ? (
              <ComposedChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} domain={chart.domain} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                {chart.keys!.map((key) => (
                  <Area key={key.k} yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey={key.k} name={key.n} stroke={key.c} fill={key.c} fillOpacity={0.2} isAnimationActive={false} />
                ))}
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </ComposedChart>
            ) : chart.type === 'line-network' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                {latencyKeys.map((key, index) => (
                    <Line
                      key={key}
                      yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined}
                      type="monotone"
                      dataKey={key}
                      name={routeLatencySeriesLabel(key)}
                      stroke={groupColors[index % groupColors.length]}
                      dot={false}
                      strokeWidth={2}
                      isAnimationActive={false}
	                    />
	                  ))}
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} stroke="var(--text-muted)" fontSize={11} />
                {renderVuserOverlayAxis(chart)}
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="netSent" name="Net Egress" stroke="var(--chart-1)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="netRecv" name="Net Ingress" stroke="var(--chart-2)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="diskRead" name="Disk Read" stroke="var(--chart-3)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                <Line yAxisId={isVuserOverlayEnabled(chart.id) ? 'metric' : undefined} type="monotone" dataKey="diskWrite" name="Disk Write" stroke="var(--chart-6)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                {renderVuserOverlayLine(chart)}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            )}
          </ResponsiveContainer>

          <div className="chart-metrics-footer">
            {chart.id === 'vusers' && (
              <div className="footer-metric">
                <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                <span className="label">Active:</span>
                <span className="value">{latest.vusers}</span>
              </div>
            )}
            {chart.id === 'tps' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--text-muted)' }}></span>
                  <span className="label">Total:</span>
                  <span className="value">{latest.tps}</span>
                </div>
                {isMultiGroup && groupNames.map((name, index) => (
                  <div key={name} className="footer-metric">
                    <span className="dot" style={{ background: groupColors[index % groupColors.length] }}></span>
                    <span className="label">{name}:</span>
                    <span className="value">{latest[`${name}_tps`]}</span>
                  </div>
                ))}
              </>
            )}
            {chart.id === 'latency' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-5)' }}></span>
                  <span className="label">P50:</span>
                  <span className="value">{latest.p50}ms</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-3)' }}></span>
                  <span className="label">P90:</span>
                  <span className="value">{latest.p90}ms</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                  <span className="label">P99:</span>
                  <span className="value">{latest.p99}ms</span>
                </div>
              </>
            )}
            {chart.id === 'errors' && (
              <div className="footer-metric">
                <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                <span className="label">Current:</span>
                <span className="value">{latest.errorPct}%</span>
              </div>
            )}
            {chart.type === 'area-multi' && chart.keys?.map((key) => (
              <div key={key.k} className="footer-metric">
                <span className="dot" style={{ background: key.c }}></span>
                <span className="label">{key.n}:</span>
                <span className="value">{latest[key.k]}{chart.id === 'cpu' ? '%' : ' MB'}</span>
              </div>
            ))}
            {chart.id === 'io' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                  <span className="label">Net Out:</span>
                  <span className="value">{latest.netSent} KB/s</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-2)' }}></span>
                  <span className="label">Net In:</span>
                  <span className="value">{latest.netRecv} KB/s</span>
                </div>
              </>
            )}
            {chart.id === 'network_latency' && (
              <>
                {latencyKeys.map((key, index) => (
                    <div key={key} className="footer-metric">
                      <span className="dot" style={{ background: groupColors[index % groupColors.length] }}></span>
                      <span className="label">{routeLatencySeriesLabel(key)}:</span>
                      <span className="value">{latencyLatestValues[key]}ms</span>
                    </div>
                  ))}
              </>
            )}
            {isVuserOverlayEnabled(chart.id) && (
              <div className="footer-metric">
                <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                <span className="label">VUsers:</span>
                <span className="value">{latest.vusers}</span>
              </div>
            )}
          </div>
            </div>
          ))}
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
          {isVuserOverlayAvailable(contextChart) && (
            <button type="button" role="menuitem" onClick={() => runChartAction(() => toggleVuserOverlay(contextChart.id))}>
              {isVuserOverlayEnabled(contextChart.id) ? 'Hide VUsers Overlay' : 'Overlay VUsers'}
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
