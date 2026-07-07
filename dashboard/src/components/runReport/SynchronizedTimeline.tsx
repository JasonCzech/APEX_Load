import { useMemo, useReducer } from 'react'
import {
  Area,
  Brush,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import type { GoalConfig, MetricsHistoryPoint } from '../../types'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from '../chartTooltipTheme'
import { buildBreachWindows } from './runReportModel'
import { initialTimelineZoomState, timelineZoomReducer } from './timelineZoomModel'

interface Props {
  data: MetricsHistoryPoint[]
  goalConfig?: GoalConfig
  loading?: boolean
  error?: string
  showBaseline?: boolean
}

type LaneID = 'vusers' | 'throughput' | 'latency' | 'errors'

interface LaneConfig {
  id: LaneID
  title: string
  yLabel: string
  unit: string
  domain?: [number | string, number | string]
}

const LANES: LaneConfig[] = [
  { id: 'vusers', title: 'VUsers', yLabel: 'VUsers', unit: '' },
  { id: 'throughput', title: 'Throughput', yLabel: 'TPS', unit: ' TPS' },
  { id: 'latency', title: 'Response Time', yLabel: 'ms', unit: ' ms' },
  { id: 'errors', title: 'Error Rate', yLabel: '%', unit: '%', domain: [0, 'auto'] },
]

function formatTooltipValue(value: unknown, _name: unknown, item: { dataKey?: string | number }) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return ['n/a', String(item.dataKey || '')]
  const key = String(item.dataKey || '')
  const suffix = key.toLowerCase().includes('error') ? '%' : key.toLowerCase().includes('ms') || key.toLowerCase().includes('p95') ? ' ms' : ''
  return [`${value.toFixed(suffix === '%' ? 2 : 1)}${suffix}`, key]
}

function renderBreachAreas(windows: ReturnType<typeof buildBreachWindows>) {
  return windows.map(window => (
    <ReferenceArea
      key={`${window.start}-${window.end}`}
      x1={window.start}
      x2={window.end}
      fill="var(--danger)"
      fillOpacity={0.08}
      strokeOpacity={0}
    />
  ))
}

function LaneChart({
  lane,
  data,
  fullCount,
  goalConfig,
  breachWindows,
  showBrush,
  showBaseline,
  onBrush,
}: {
  lane: LaneConfig
  data: MetricsHistoryPoint[]
  fullCount: number
  goalConfig?: GoalConfig
  breachWindows: ReturnType<typeof buildBreachWindows>
  showBrush: boolean
  showBaseline?: boolean
  onBrush: (range: { startIndex?: number; endIndex?: number }) => void
}) {
  const p95Sla = goalConfig?.p95_latency_ms
  const errorSla = goalConfig?.max_error_pct
  return (
    <div className="run-report-timeline-lane" data-lane={lane.id}>
      <div className="run-report-lane-label">
        <strong>{lane.title}</strong>
        <span>{lane.yLabel}</span>
      </div>
      <div className="run-report-lane-chart">
        <ResponsiveContainer width="100%" height={showBrush ? 170 : 138}>
          <ComposedChart data={data} syncId="metrics" margin={{ top: 14, right: 24, bottom: showBrush ? 32 : 8, left: 4 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} minTickGap={28} />
            <YAxis stroke="var(--text-muted)" fontSize={11} width={48} domain={lane.domain} />
            <Tooltip
              formatter={formatTooltipValue}
              contentStyle={chartTooltipContentStyle}
              labelStyle={chartTooltipLabelStyle}
            />
            {renderBreachAreas(breachWindows)}
            {lane.id === 'vusers' && (
              <>
                <Area type="monotone" dataKey="vusers" name="VUsers" stroke="var(--chart-1)" fill="var(--chart-1)" fillOpacity={0.18} isAnimationActive={false} />
                {showBaseline && <Line type="monotone" dataKey="baselineVusers" name="Baseline VUsers" stroke="var(--chart-1)" strokeDasharray="5 4" dot={false} isAnimationActive={false} />}
              </>
            )}
            {lane.id === 'throughput' && (
              <>
                <Line type="monotone" dataKey="tps" name="Actual TPS" stroke="var(--chart-2)" strokeWidth={2} dot={false} isAnimationActive={false} />
                <Line type="monotone" dataKey="targetTps" name="Target TPS" stroke="var(--warning)" strokeDasharray="4 4" dot={false} isAnimationActive={false} />
                {showBaseline && <Line type="monotone" dataKey="baselineTps" name="Baseline TPS" stroke="var(--chart-2)" strokeDasharray="5 4" dot={false} isAnimationActive={false} />}
              </>
            )}
            {lane.id === 'latency' && (
              <>
                {typeof p95Sla === 'number' && <ReferenceLine y={p95Sla} stroke="var(--danger)" strokeDasharray="4 3" label={{ value: 'P95 SLA', fill: 'var(--danger)', fontSize: 11 }} />}
                <Line type="monotone" dataKey="avgMs" name="Avg ms" stroke="var(--chart-5)" strokeWidth={2} dot={false} isAnimationActive={false} />
                <Line type="monotone" dataKey="p95" name="P95 ms" stroke="var(--chart-3)" strokeWidth={2} dot={false} isAnimationActive={false} />
                {showBaseline && <Line type="monotone" dataKey="baselineP95" name="Baseline P95" stroke="var(--chart-3)" strokeDasharray="5 4" dot={false} isAnimationActive={false} />}
              </>
            )}
            {lane.id === 'errors' && (
              <>
                {typeof errorSla === 'number' && <ReferenceLine y={errorSla} stroke="var(--danger)" strokeDasharray="4 3" label={{ value: 'Error SLA', fill: 'var(--danger)', fontSize: 11 }} />}
                <Line type="monotone" dataKey="errorPct" name="Error %" stroke="var(--danger)" strokeWidth={2} dot={false} isAnimationActive={false} />
                {showBaseline && <Line type="monotone" dataKey="baselineErrorPct" name="Baseline Error %" stroke="var(--danger)" strokeDasharray="5 4" dot={false} isAnimationActive={false} />}
              </>
            )}
            {showBrush && fullCount > 1 && (
              <Brush
                dataKey="time"
                height={24}
                travellerWidth={8}
                stroke="var(--accent)"
                fill="var(--bg-card)"
                onChange={onBrush}
              />
            )}
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

export default function SynchronizedTimeline({ data, goalConfig, loading, error, showBaseline }: Props) {
  const [zoomState, dispatchZoom] = useReducer(timelineZoomReducer, initialTimelineZoomState)
  const visibleData = useMemo(() => {
    if (!zoomState.window) return data
    return data.slice(zoomState.window.startIndex, zoomState.window.endIndex + 1)
  }, [data, zoomState.window])
  const breachWindows = useMemo(
    () => buildBreachWindows(visibleData, goalConfig?.p95_latency_ms, goalConfig?.max_error_pct),
    [goalConfig?.max_error_pct, goalConfig?.p95_latency_ms, visibleData],
  )

  if (loading) return <section className="run-report-panel run-report-timeline"><p role="status">Loading timeline...</p></section>
  if (error) return <section className="run-report-panel run-report-timeline"><p className="run-report-error">{error}</p></section>
  if (data.length === 0) return <section className="run-report-panel run-report-timeline"><p>No interval summary history is available for this run.</p></section>

  const slaConfigured = typeof goalConfig?.p95_latency_ms === 'number' || typeof goalConfig?.max_error_pct === 'number'

  return (
    <section className="run-report-panel run-report-timeline" aria-label="Synchronized run timeline">
      <div className="run-report-section-heading">
        <div>
          <h2>Synchronized Timeline</h2>
          <p>{slaConfigured ? 'Hover across lanes to correlate load, throughput, latency, and errors.' : 'SLA not configured for this run.'}</p>
        </div>
        {zoomState.window && (
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => dispatchZoom({ type: 'reset' })}>
            Reset zoom
          </button>
        )}
      </div>
      <div className="run-report-timeline-stack">
        {LANES.map((lane, index) => (
          <LaneChart
            key={lane.id}
            lane={lane}
            data={visibleData}
            fullCount={data.length}
            goalConfig={goalConfig}
            breachWindows={breachWindows}
            showBrush={index === 0}
            showBaseline={showBaseline}
            onBrush={(range) => {
              if (typeof range.startIndex === 'number' && typeof range.endIndex === 'number') {
                dispatchZoom({
                  type: 'set-window',
                  startIndex: range.startIndex,
                  endIndex: range.endIndex,
                  pointCount: data.length,
                  baseStartIndex: zoomState.window?.startIndex ?? 0,
                })
              }
            }}
          />
        ))}
      </div>
    </section>
  )
}
