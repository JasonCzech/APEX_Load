import { useMemo } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  Rectangle,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { BarShapeProps } from 'recharts'

import { RunArchiveReport } from '../types'
import ExportableChart from './ExportableChart'
import { type ChartExportColumn } from './exportableChartUtils'

const ACCENT = '#0f766e'
const ACCENT_2 = '#b45309'
const GRID = '#d8cfc2'
const INK = '#1f2933'
const SURFACE = '#fffdf8'

const tooltipStyle = {
  background: SURFACE,
  border: `1px solid ${GRID}`,
  borderRadius: 8,
  color: INK,
  fontSize: 12,
}

interface ActionBar {
  name: string
  avg: number
  // True when this action's average is at or above the run-wide average, i.e.
  // it is one of the actions dragging the run's response time up.
  aboveRunAverage: boolean
}

const ACTION_BAR_RADIUS: [number, number, number, number] = [0, 3, 3, 0]

// Per-bar paint. recharts 3 still ships <Cell>, but its typings mark it for
// removal in v4; `shape` is the supported way to vary a bar's appearance per
// datum, and unlike <Cell> it is honoured for every rendered rectangle.
function actionBarFill(bar: ActionBar | undefined): string {
  return bar?.aboveRunAverage ? ACCENT_2 : ACCENT
}

function ActionBarShape({ payload, x, y, width, height }: BarShapeProps) {
  return (
    <Rectangle
      x={x}
      y={y}
      width={width}
      height={height}
      radius={ACTION_BAR_RADIUS}
      fill={actionBarFill(payload as ActionBar | undefined)}
    />
  )
}

// Top 8 actions by transaction count, drawn slowest-first, each flagged against
// the run-wide average response time so the chart can single out the offenders.
function buildActionBars(report: RunArchiveReport): ActionBar[] {
  const runAverage = (report.metric_populations ? report.metric_populations.action?.avg_response_ms : report.overview?.avg_response_ms) ?? 0
  const entries = Object.entries((report.metric_populations ? report.by_request : report.by_action) || {})
  entries.sort((a, b) => b[1].transactions - a[1].transactions)
  return entries
    .slice(0, 8)
    .map(([name, stats]) => {
      const avg = round(stats.avg_response_ms)
      return { name, avg, aboveRunAverage: runAverage > 0 && avg >= round(runAverage) }
    })
    .sort((a, b) => b.avg - a.avg)
}

interface TimelinePoint {
  label: string
  tps: number
  target: number
  p95: number
  errorPct: number
  vusers: number
}

const timelineColumns: readonly ChartExportColumn<TimelinePoint>[] = [
  { key: 'time', label: 'Time', value: row => row.label },
  { key: 'tps', label: 'Actual TPS', value: row => row.tps },
  { key: 'target', label: 'Target TPS', value: row => row.target },
  { key: 'p95', label: 'P95 response time (ms)', value: row => row.p95 },
  { key: 'error_pct', label: 'Error rate (%)', value: row => row.errorPct },
  { key: 'vusers', label: 'Active VUsers', value: row => row.vusers },
]

const actionColumns: readonly ChartExportColumn<ActionBar>[] = [
  { key: 'action', label: 'Action', value: row => row.name },
  { key: 'avg_ms', label: 'Average response time (ms)', value: row => row.avg },
  { key: 'above_run_average', label: 'At or above run average', value: row => row.aboveRunAverage },
]

function clock(timestamp: string): string {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

interface Props {
  report: RunArchiveReport
}

export default function ReportCharts({ report }: Props) {
  const timeline = useMemo<TimelinePoint[]>(
    () =>
      (report.summary_timeline || []).map((point) => ({
        label: clock(point.timestamp),
        tps: round(point.tps),
        target: round(point.target_tps ?? 0),
        p95: round(point.p95_ms),
        errorPct: round(point.error_pct),
        vusers: point.active_vusers,
      })),
    [report.summary_timeline],
  )

  const hasTarget = useMemo(() => timeline.some((point) => point.target > 0), [timeline])

  const actionBars = useMemo(() => buildActionBars(report), [report])
  const runAverageMs = round((report.metric_populations ? report.metric_populations.action?.avg_response_ms : report.overview?.avg_response_ms) ?? 0)
  const highlightsSlowActions = actionBars.some((bar) => bar.aboveRunAverage)

  const hasTimeline = timeline.length >= 2

  if (!hasTimeline && actionBars.length === 0) {
    return <p className="report-empty">No interval timeline or per-action data is available to chart for this run.</p>
  }

  return (
    <div className="report-charts">
      {hasTimeline && <p className="report-chart-note">{(report.metric_populations?.transaction?.transactions || 0) > 0 ? 'Interval charts show named transaction metrics.' : 'Legacy interval metrics have no identified named transaction population. They are separate from request / action totals.'}</p>}
      {hasTimeline && (
        <>
          <ExportableChart definitionId="report-throughput" title="Throughput (TPS)" description="Actual and configured target transactions per second over the archived run timeline." rows={timeline} columns={timelineColumns}>
            <ResponsiveContainer width="100%" height={220}>
              <LineChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Line type="monotone" dataKey="tps" name="Actual TPS" stroke={ACCENT} strokeWidth={2} dot={false} isAnimationActive={false} />
                {hasTarget && (
                  <Line type="monotone" dataKey="target" name="Target TPS" stroke={ACCENT_2} strokeWidth={2} strokeDasharray="5 4" dot={false} isAnimationActive={false} />
                )}
              </LineChart>
            </ResponsiveContainer>
          </ExportableChart>

          <ExportableChart definitionId="report-p95-response-time" title="Response Time (p95, ms)" description="Ninety-fifth percentile response time in milliseconds over the archived run timeline." rows={timeline} columns={timelineColumns}>
            <ResponsiveContainer width="100%" height={220}>
              <AreaChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="p95" name="p95 latency" stroke={ACCENT} fill={ACCENT} fillOpacity={0.15} strokeWidth={2} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          </ExportableChart>

          <ExportableChart definitionId="report-error-rate" title="Error Rate (%)" description="Error percentage reported in archived intervals; population availability is stated above." rows={timeline} columns={timelineColumns}>
            <ResponsiveContainer width="100%" height={220}>
              <LineChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Line type="monotone" dataKey="errorPct" name="Error rate (%)" stroke={ACCENT_2} strokeWidth={2} dot={false} isAnimationActive={false} />
              </LineChart>
            </ResponsiveContainer>
          </ExportableChart>

          <ExportableChart definitionId="report-active-vusers" title="Active VUsers" description="Active virtual users over the archived run timeline." rows={timeline} columns={timelineColumns}>
            <ResponsiveContainer width="100%" height={220}>
              <AreaChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="vusers" name="Active VUsers" stroke={ACCENT} fill={ACCENT} fillOpacity={0.15} strokeWidth={2} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          </ExportableChart>
        </>
      )}

      {actionBars.length > 0 && (
        <ExportableChart
          definitionId="report-action-response-time"
          title="Average Response Time by Action (ms)"
          description="Average response time by action, with actions at or above the run average highlighted."
          rows={actionBars}
          columns={actionColumns}
          note={highlightsSlowActions ? `Highlighted bars are at or above the run average of ${runAverageMs} ms.` : undefined}
        >
          <ResponsiveContainer width="100%" height={Math.max(160, actionBars.length * 34 + 24)}>
            <BarChart data={actionBars} layout="vertical" margin={{ top: 8, right: 32, bottom: 4, left: 8 }}>
              <CartesianGrid stroke={GRID} strokeDasharray="4 4" horizontal={false} />
              <XAxis type="number" tick={{ fontSize: 11, fill: INK }} stroke={GRID} />
              <YAxis type="category" dataKey="name" width={150} tick={{ fontSize: 11, fill: INK }} stroke={GRID} />
              <Tooltip contentStyle={tooltipStyle} />
              <Bar dataKey="avg" name="Avg ms" radius={ACTION_BAR_RADIUS} fill={ACCENT} shape={ActionBarShape} isAnimationActive={false} />
            </BarChart>
          </ResponsiveContainer>
        </ExportableChart>
      )}
    </div>
  )
}

function round(value: number): number {
  return Math.round((value + Number.EPSILON) * 10) / 10
}
