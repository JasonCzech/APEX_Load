import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import {
  type BrowserBreakdownDatum,
  type BrowserBreakdownSeries,
  formatBrowserBreakdownTooltipLabel,
  formatBrowserBreakdownTooltipValue,
} from './browserBreakdownModel'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import { comparisonRunColor } from './resultsComparisonChartModel'

interface BrowserBreakdownChartProps {
  title: string
  copy?: string
  data: BrowserBreakdownDatum[]
  series: BrowserBreakdownSeries[]
  height?: number
  segmentLabel?: string
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
  const resolvedSeries = (() => {
    const usedStrokes = new Set<string>()
    return series.map((item, index) => {
      const duplicateOffset = index % 10
      let stroke = item.stroke
      if (usedStrokes.has(stroke)) {
        for (let offset = 0; offset <= series.length; offset += 1) {
          const candidate = comparisonRunColor((duplicateOffset + offset) % 10)
          if (!usedStrokes.has(candidate)) {
            stroke = candidate
            break
          }
        }
      }
      usedStrokes.add(stroke)
      return {
        ...item,
        stroke,
      }
    })
  })()

  return (
    <div className="results-compare-chart-card">
      <h4 className="results-compare-chart-title">{title}</h4>
      {copy && <p className="results-compare-chart-copy">{copy}</p>}
      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={data} margin={{ top: 10, right: hasScoreAxis ? 92 : 18, bottom: 12, left: 24 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis
            dataKey="segment"
            stroke="var(--text-muted)"
            fontSize={11}
            height={42}
            label={{ value: segmentLabel, position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 11 }}
          />
          <YAxis
            yAxisId="timings"
            stroke="var(--text-muted)"
            fontSize={11}
            width={88}
            label={{ value: 'Timing (ms)', angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 11 }}
          />
          {hasScoreAxis && (
            <YAxis
              yAxisId="cls"
              orientation="right"
              stroke="var(--text-muted)"
              fontSize={11}
              width={70}
              domain={[0, 'auto']}
              label={{ value: 'CLS score', angle: 90, position: 'right', fill: 'var(--text-muted)', fontSize: 11 }}
            />
          )}
          <Tooltip
            contentStyle={chartTooltipContentStyle}
            labelStyle={chartTooltipLabelStyle}
            labelFormatter={value => formatBrowserBreakdownTooltipLabel(value, segmentLabel)}
            formatter={(value, name, item) => {
              const metadata = seriesByKey.get(String((item as { dataKey?: string } | undefined)?.dataKey ?? ''))
              return [formatBrowserBreakdownTooltipValue(value, metadata?.valueFormat), metadata?.name || String(name)]
            }}
            isAnimationActive={false}
          />
          <Legend />
          {resolvedSeries.map(item => (
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
