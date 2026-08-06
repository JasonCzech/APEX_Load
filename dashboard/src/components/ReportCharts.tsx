import { useMemo } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { RunArchiveReport } from '../types'

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

interface TimelinePoint {
  label: string
  tps: number
  target: number
  p95: number
  errorPct: number
  vusers: number
}

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

  const actionBars = useMemo(() => {
    const entries = Object.entries(report.by_action || {})
    entries.sort((a, b) => b[1].transactions - a[1].transactions)
    return entries
      .slice(0, 8)
      .map(([name, stats]) => ({ name, avg: round(stats.avg_response_ms) }))
      .sort((a, b) => b.avg - a.avg)
  }, [report.by_action])

  const hasTimeline = timeline.length >= 2

  if (!hasTimeline && actionBars.length === 0) {
    return <p className="report-empty">No interval timeline or per-action data is available to chart for this run.</p>
  }

  return (
    <div className="report-charts">
      {hasTimeline && (
        <>
          <ReportChartCard title="Throughput (TPS)">
            <ResponsiveContainer width="100%" height={220}>
              <LineChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Line type="monotone" dataKey="tps" name="Actual TPS" stroke={ACCENT} strokeWidth={2} dot={false} />
                {hasTarget && (
                  <Line type="monotone" dataKey="target" name="Target TPS" stroke={ACCENT_2} strokeWidth={2} strokeDasharray="5 4" dot={false} />
                )}
              </LineChart>
            </ResponsiveContainer>
          </ReportChartCard>

          <ReportChartCard title="Response Time (p95, ms)">
            <ResponsiveContainer width="100%" height={220}>
              <AreaChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="p95" name="p95 latency" stroke={ACCENT} fill={ACCENT} fillOpacity={0.15} strokeWidth={2} />
              </AreaChart>
            </ResponsiveContainer>
          </ReportChartCard>

          <ReportChartCard title="Error Rate (%)">
            <ResponsiveContainer width="100%" height={220}>
              <LineChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Line type="monotone" dataKey="errorPct" name="Error %" stroke={ACCENT_2} strokeWidth={2} dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </ReportChartCard>

          <ReportChartCard title="Active VUsers">
            <ResponsiveContainer width="100%" height={220}>
              <AreaChart data={timeline} margin={{ top: 8, right: 16, bottom: 4, left: 0 }}>
                <CartesianGrid stroke={GRID} strokeDasharray="4 4" />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: INK }} stroke={GRID} minTickGap={32} />
                <YAxis tick={{ fontSize: 11, fill: INK }} stroke={GRID} width={48} />
                <Tooltip contentStyle={tooltipStyle} />
                <Area type="monotone" dataKey="vusers" name="Active VUsers" stroke={ACCENT} fill={ACCENT} fillOpacity={0.15} strokeWidth={2} />
              </AreaChart>
            </ResponsiveContainer>
          </ReportChartCard>
        </>
      )}

      {actionBars.length > 0 && (
        <ReportChartCard title="Average Response Time by Action (ms)">
          <ResponsiveContainer width="100%" height={Math.max(160, actionBars.length * 34 + 24)}>
            <BarChart data={actionBars} layout="vertical" margin={{ top: 8, right: 32, bottom: 4, left: 8 }}>
              <CartesianGrid stroke={GRID} strokeDasharray="4 4" horizontal={false} />
              <XAxis type="number" tick={{ fontSize: 11, fill: INK }} stroke={GRID} />
              <YAxis type="category" dataKey="name" width={150} tick={{ fontSize: 11, fill: INK }} stroke={GRID} />
              <Tooltip contentStyle={tooltipStyle} />
              <Bar dataKey="avg" name="Avg ms" radius={[0, 3, 3, 0]} fill={ACCENT} />
            </BarChart>
          </ResponsiveContainer>
        </ReportChartCard>
      )}
    </div>
  )
}

function ReportChartCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <figure className="report-chart-card">
      <figcaption>{title}</figcaption>
      {children}
    </figure>
  )
}

function round(value: number): number {
  return Math.round((value + Number.EPSILON) * 10) / 10
}
