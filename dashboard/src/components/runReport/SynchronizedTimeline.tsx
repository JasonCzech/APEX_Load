import { type ReactNode, useMemo, useReducer } from 'react'
import {
  Area,
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

function formatTooltipValue(value: unknown, _name: unknown, item: { dataKey?: unknown }): [ReactNode, string] {
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
  goalConfig,
  breachWindows,
  showBaseline,
}: {
  lane: LaneConfig
  data: MetricsHistoryPoint[]
  goalConfig?: GoalConfig
  breachWindows: ReturnType<typeof buildBreachWindows>
  showBaseline?: boolean
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
        <ResponsiveContainer width="100%" height={138}>
          <ComposedChart data={data} syncId="metrics" margin={{ top: 14, right: 24, bottom: 8, left: 4 }}>
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

          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

export default function SynchronizedTimeline({ data, goalConfig, loading, error, showBaseline }: Props) {
  const [zoomState, dispatchZoom] = useReducer(timelineZoomReducer, initialTimelineZoomState)
  const startIndex = Math.min(zoomState.window?.startIndex ?? 0, Math.max(0, data.length - 1))
  const endIndex = Math.min(zoomState.window?.endIndex ?? Math.max(0, data.length - 1), Math.max(0, data.length - 1))
  const visibleData = useMemo(() => data.slice(startIndex, endIndex + 1), [data, startIndex, endIndex])
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
      {data.length > 1 && <div className="run-report-timeline-window" aria-label="Timeline window">
        <label>Window start: {data[startIndex]?.time}
          <input type="range" aria-label="Timeline window start" min={0} max={endIndex} value={startIndex}
            aria-valuetext={data[startIndex]?.time}
            onChange={event => dispatchZoom({ type: 'set-window', startIndex: Number(event.target.value), endIndex, pointCount: data.length })} />
        </label>
        <label>Window end: {data[endIndex]?.time}
          <input type="range" aria-label="Timeline window end" min={startIndex} max={data.length - 1} value={endIndex}
            aria-valuetext={data[endIndex]?.time}
            onChange={event => dispatchZoom({ type: 'set-window', startIndex, endIndex: Number(event.target.value), pointCount: data.length })} />
        </label>
      </div>}
      <div className="run-report-timeline-stack">
        {LANES.map((lane) => (
          <LaneChart
            key={lane.id}
            lane={lane}
            data={visibleData}
            goalConfig={goalConfig}
            breachWindows={breachWindows}
            showBaseline={showBaseline}

          />
        ))}
      </div>
    </section>
  )
}
