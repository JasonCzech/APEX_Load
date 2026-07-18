import { useMemo } from 'react'
import { CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { PrometheusSeries } from '../types'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import { prometheusChartData } from './testExecutionChartModel'
import StatTile from './StatTile'

const SERIES_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

interface Props {
  title: string
  unit?: string
  kind: 'line' | 'single_value'
  series?: PrometheusSeries[]
  value?: number
  error?: string
  height?: number
}

// PrometheusPanelCard renders a single resolved imported-dashboard panel — a line
// chart for timeseries panels, a StatTile for single-value panels. It is deliberately
// standalone: it reuses the pure prometheusChartData pivot and the shared tooltip theme
// but does NOT import TestExecutionCharts, so the static results surfaces stay clear of
// that component's live zoom/cursor state and its bundle weight.
export default function PrometheusPanelCard({ title, unit, kind, series = [], value, error, height = 200 }: Props) {
  const rows = useMemo(
    () => (kind === 'line' ? prometheusChartData({ id: title, title, unit, series }) : []),
    [kind, series, title, unit],
  )

  if (kind === 'single_value') {
    return (
      <div className="imported-panel-card imported-panel-card-tile">
        <StatTile title={title} value={value} unit={unit} error={error} />
      </div>
    )
  }

  return (
    <div className="imported-panel-card">
      <div className="imported-panel-card-header">
        <span className="imported-panel-card-title">{title}</span>
        {unit ? <span className="imported-panel-card-unit">{unit}</span> : null}
      </div>
      {error ? (
        <div className="chart-empty-state chart-error-state" role="status">{error}</div>
      ) : rows.length === 0 ? (
        <div className="chart-empty-state" role="status">No data in this window</div>
      ) : (
        <ResponsiveContainer width="100%" height={height}>
          <ComposedChart data={rows} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="time" tick={{ fontSize: 11, fill: 'var(--text-muted)' }} minTickGap={40} />
            <YAxis tick={{ fontSize: 11, fill: 'var(--text-muted)' }} width={52} />
            <Tooltip contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} />
            {series.map((s, index) => (
              <Line
                key={s.key}
                type="monotone"
                dataKey={s.key}
                name={s.label || s.key}
                stroke={SERIES_COLORS[index % SERIES_COLORS.length]}
                dot={false}
                strokeWidth={1.5}
                isAnimationActive={false}
                connectNulls={false}
              />
            ))}
          </ComposedChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
