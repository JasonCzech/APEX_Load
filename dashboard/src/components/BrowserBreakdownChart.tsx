import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import {
  type BrowserBreakdownDatum,
  type BrowserBreakdownSeries,
  formatBrowserBreakdownTooltipLabel,
  formatBrowserBreakdownTooltipValue,
} from './browserBreakdownModel'

interface BrowserBreakdownChartProps {
  title: string
  copy?: string
  data: BrowserBreakdownDatum[]
  series: BrowserBreakdownSeries[]
  height?: number
  segmentLabel?: string
}

const tooltipStyle = {
  background: 'rgba(33, 36, 47, 0.94)',
  border: '1px solid var(--border)',
  borderRadius: 10,
}

export default function BrowserBreakdownChart({
  title,
  copy,
  data,
  series,
  height = 260,
  segmentLabel = 'Segment',
}: BrowserBreakdownChartProps) {
  if (data.length === 0 || series.length === 0) {
    return null
  }

  const hasScoreAxis = series.some(item => item.yAxisId === 'cls')
  const seriesByKey = new Map(series.map(item => [item.dataKey, item]))

  return (
    <div className="results-compare-chart-card">
      <h4 className="results-compare-chart-title">{title}</h4>
      {copy && <p className="results-compare-chart-copy">{copy}</p>}
      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={data}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey="segment" stroke="var(--text-muted)" fontSize={11} />
          <YAxis yAxisId="timings" stroke="var(--text-muted)" fontSize={11} />
          {hasScoreAxis && <YAxis yAxisId="cls" orientation="right" stroke="var(--text-muted)" fontSize={11} domain={[0, 'auto']} />}
          <Tooltip
            contentStyle={tooltipStyle}
            labelFormatter={value => formatBrowserBreakdownTooltipLabel(value, segmentLabel)}
            formatter={(value, name, item) => {
              const metadata = seriesByKey.get(String((item as { dataKey?: string } | undefined)?.dataKey ?? ''))
              return [formatBrowserBreakdownTooltipValue(value, metadata?.valueFormat), metadata?.name || String(name)]
            }}
            isAnimationActive={false}
          />
          <Legend />
          {series.map(item => (
            <Line
              key={item.dataKey}
              yAxisId={item.yAxisId || 'timings'}
              type="monotone"
              dataKey={item.dataKey}
              name={item.name}
              stroke={item.stroke}
              strokeWidth={2}
              strokeDasharray={item.strokeDasharray}
              dot={{ r: 3 }}
              connectNulls
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}