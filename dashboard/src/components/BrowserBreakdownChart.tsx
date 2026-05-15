import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

export type BrowserBreakdown = 'overall' | 'platform' | 'engine' | 'device'

export interface BrowserBreakdownDatum {
  segment: string
  [key: string]: string | number | undefined
}

export interface BrowserBreakdownSeries {
  dataKey: string
  name: string
  stroke: string
  strokeDasharray?: string
  yAxisId?: 'timings' | 'cls'
  valueFormat?: 'ms' | 'score'
}

interface BrowserBreakdownChartProps {
  title: string
  copy?: string
  data: BrowserBreakdownDatum[]
  series: BrowserBreakdownSeries[]
  height?: number
}

const tooltipStyle = {
  background: 'rgba(33, 36, 47, 0.94)',
  border: '1px solid var(--border)',
  borderRadius: 10,
}

export function formatBrowserBreakdownTooltipValue(value: unknown, format: 'ms' | 'score' = 'ms'): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return 'n/a'
  }
  if (format === 'score') {
    return value.toFixed(3)
  }
  return `${value.toFixed(1)} ms`
}

export function getBrowserBreakdownTitle(breakdown: BrowserBreakdown, context: 'Comparison' | 'Breakdown', metricsLabel: string): string {
  if (breakdown === 'overall') return `Browser Overview ${context} (${metricsLabel})`
  if (breakdown === 'platform') return `Browser Platform ${context} (${metricsLabel})`
  if (breakdown === 'engine') return `Browser Engine ${context} (${metricsLabel})`
  return `Browser Device ${context} (${metricsLabel})`
}

export function getBrowserBreakdownChartCopy(series: BrowserBreakdownSeries[]): string {
  if (series.some(item => item.valueFormat === 'score')) {
    return 'Series: LCP and INP on the left axis, CLS on the right axis.'
  }
  if (series.some(item => item.strokeDasharray)) {
    return 'Series: solid LCP and dashed INP for each selected run.'
  }
  return 'Series: browser metric comparison by segment.'
}

export function buildArchiveBrowserBreakdownSeries(): BrowserBreakdownSeries[] {
  return [
    {
      dataKey: 'lcp',
      name: 'LCP',
      stroke: 'var(--accent)',
      yAxisId: 'timings',
      valueFormat: 'ms',
    },
    {
      dataKey: 'inp',
      name: 'INP',
      stroke: 'var(--accent-2, #d97706)',
      yAxisId: 'timings',
      valueFormat: 'ms',
    },
    {
      dataKey: 'cls',
      name: 'CLS',
      stroke: 'var(--chart-4, #10b981)',
      yAxisId: 'cls',
      valueFormat: 'score',
    },
  ]
}

export function buildComparisonBrowserBreakdownSeries(runs: Array<{ id: string; name: string }>): BrowserBreakdownSeries[] {
  return runs.flatMap((run, index) => {
    const color = index === 0 ? 'var(--accent)' : 'var(--accent-2, #d97706)'
    return [
      {
        dataKey: run.id,
        name: `${run.name} LCP`,
        stroke: color,
        valueFormat: 'ms',
      },
      {
        dataKey: `${run.id}_inp`,
        name: `${run.name} INP`,
        stroke: color,
        strokeDasharray: '4 2',
        valueFormat: 'ms',
      },
    ]
  })
}

export default function BrowserBreakdownChart({
  title,
  copy,
  data,
  series,
  height = 260,
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