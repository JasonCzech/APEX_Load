import ExportableChart from "./ExportableChart"
import { useMemo, type ReactNode } from 'react'
import { CartesianGrid, ComposedChart, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { ImportedDashboardPanelKind, ImportedDashboardPanelThreshold, PrometheusSeries } from '../types'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import { thresholdAwareYAxisMax } from './prometheusPanelCardModel'
import { prometheusChartData } from './testExecutionChartModel'
import StatTile from './StatTile'
import { formatDateTime } from '../utils/dateTime'

const SERIES_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

interface Props {
  title: string
  unit?: string
  kind: ImportedDashboardPanelKind
  series?: PrometheusSeries[]
  value?: number
  error?: string
  height?: number
  thresholds?: ImportedDashboardPanelThreshold[]
  baselineSeries?: PrometheusSeries[]
  baselineValue?: number
}

// PrometheusPanelCard renders a single resolved imported-dashboard panel — a line
// chart for timeseries panels, a StatTile for single-value panels. It is deliberately
// standalone: it reuses the pure prometheusChartData pivot and the shared tooltip theme
// but does NOT import TestExecutionCharts, so the static results surfaces stay clear of
// that component's live zoom/cursor state and its bundle weight.
export default function PrometheusPanelCard({ title, unit, kind, series = [], value, error, height = 200, thresholds = [], baselineSeries = [], baselineValue }: Props) {
  const combinedSeries = useMemo(() => {
    if (baselineSeries.length === 0 || series.length === 0) return series
    const earliest = (items: PrometheusSeries[]) => items.reduce((minimum, item) => item.values.reduce((valueMinimum, point) => Math.min(valueMinimum, point.timestamp_ms), minimum), Number.POSITIVE_INFINITY)
    const currentStart = earliest(series)
    const baselineStart = earliest(baselineSeries)
    const delta = Number.isFinite(currentStart) && Number.isFinite(baselineStart) ? currentStart - baselineStart : 0
    return [...series, ...baselineSeries.map(item => ({ ...item, key: `${item.key}__baseline`, label: `Baseline ${item.label}`, values: item.values.map(point => ({ ...point, original_timestamp: point.timestamp, timestamp_ms: point.timestamp_ms + delta, timestamp: new Date(point.timestamp_ms + delta).toISOString() })) }))]
  }, [baselineSeries, series])
  const rows = useMemo(
    () => (kind === 'line' ? prometheusChartData({ id: title, title, unit, series: combinedSeries }) : []),
    [combinedSeries, kind, title, unit],
  )
  const yAxisDomain = useMemo<[number, (dataMax: number) => number]>(
    () => [0, (dataMax: number) => thresholdAwareYAxisMax(dataMax, thresholds)],
    [thresholds],
  )

  const exportRows = combinedSeries.flatMap(item => item.values.filter(point => Number.isFinite(point.value)).map(point => {
    return { series: item.key, label: item.label, labels: item.labels, value: point.value, timestamp: point.timestamp, original_timestamp: (point as typeof point & { original_timestamp?: string }).original_timestamp ?? point.timestamp }
  }))
  const exportColumns = [
    { key: "utc", label: "Aligned UTC timestamp", value: (point: typeof exportRows[number]) => point.timestamp },
    { key: "original", label: "Source UTC timestamp", value: (point: typeof exportRows[number]) => point.original_timestamp },
    { key: "series", label: "Series key", value: (point: typeof exportRows[number]) => point.series },
    { key: "label", label: "Series label", value: (point: typeof exportRows[number]) => point.label },
    { key: "labels", label: "Source labels", value: (point: typeof exportRows[number]) => point.labels },
    { key: "value", label: `Value${unit ? ` (${unit})` : ""}`, value: (point: typeof exportRows[number]) => point.value },
    { key: "thresholds", label: "Thresholds", value: () => thresholds },
  ]
  if (kind === 'single_value') {
    const active = typeof value === 'number'
      ? thresholds.filter(step => step.value === undefined || value >= step.value).slice(-1)[0]
      : undefined
    const sparkline = series.flatMap(item => item.values.map(point => point.value)).slice(-120)
    return (
      <div className="imported-panel-card imported-panel-card-tile">
        <StatTile title={title} value={value} unit={unit} error={error} sparkline={sparkline} tone={active?.severity} baselineValue={baselineValue} />
      </div>
    )
  }

  if (kind === 'table') {
    return (
      <PanelShell title={title} unit={unit} error={error} empty={series.length === 0}>
        <div className="imported-panel-table-wrap">
          <table className="imported-panel-table">
            <thead><tr><th>Series</th><th>Latest</th><th>Time</th></tr></thead>
            <tbody>{series.map(item => {
              const latest = item.values[item.values.length - 1]
              return <tr key={item.key}><td>{item.label || item.key}</td><td>{latest ? `${latest.value}${unit ? ` ${unit}` : ''}` : '—'}</td><td>{latest ? formatDateTime(latest.timestamp_ms, { seconds: true }) : '—'}</td></tr>
            })}</tbody>
          </table>
        </div>
      </PanelShell>
    )
  }

  if (kind === 'heatmap') {
    const heatSeries = series.slice(0, 20).map(item => ({ ...item, values: item.values.slice(-60).filter(point => Number.isFinite(point.value)) }))
    const values = heatSeries.flatMap(item => item.values.slice(-60).map(point => point.value))
    const minValue = values.length ? Math.min(...values) : 0
    const maxValue = values.length ? Math.max(...values) : 0
    return (
      <PanelShell title={title} unit={unit} error={error} empty={values.length === 0}>
        <ExportableChart definitionId="imported-heatmap" title={title} description={`${title}. Last 60 points from the first 20 series, unit ${unit || "unverified"}.`} rows={heatSeries.flatMap(item => item.values.slice(-60).map(point => ({ series: item.key, label: item.label, labels: item.labels, value: point.value, timestamp: point.timestamp, original_timestamp: point.timestamp })))} columns={exportColumns}>
          <svg className="imported-panel-heatmap" role="img" aria-label={`${title} heatmap`} viewBox={`0 0 600 ${Math.max(14, heatSeries.length * 14)}`} width="100%">
            {heatSeries.map((item, rowIndex) => <g className="imported-panel-heatmap-row" key={item.key}>
              {item.values.slice(-60).map((point, index) => {
                const intensity = maxValue === minValue ? 0.5 : (point.value - minValue) / (maxValue - minValue)
                return <rect key={`${point.timestamp_ms}-${index}`} x={index * 10} y={rowIndex * 14} width={9} height={12} fill="var(--accent)" opacity={0.18 + intensity * 0.82}><title>{`${item.label || item.key}: ${point.value}${unit ? ` ${unit}` : ''}`}</title></rect>
              })}
            </g>)}
          </svg>
        </ExportableChart>
      </PanelShell>
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
        <ExportableChart definitionId="imported-prometheus" title={title} description={`${title}. Current and baseline source timestamps are retained; baseline is aligned to current start. Unit: ${unit || "unverified"}.`} rows={exportRows} columns={exportColumns}><ResponsiveContainer width="100%" height={height}>
          <ComposedChart data={rows} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="time" tick={{ fontSize: 11, fill: 'var(--text-muted)' }} minTickGap={40} />
            <YAxis domain={yAxisDomain} tick={{ fontSize: 11, fill: 'var(--text-muted)' }} width={52} />
            <Tooltip contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} />
            {thresholds.filter(step => step.value !== undefined).map((step, index) => (
              <ReferenceLine key={`${step.value}-${index}`} y={step.value} stroke={step.severity === 'critical' ? 'var(--danger)' : step.severity === 'warning' ? 'var(--warning)' : step.severity === 'normal' ? 'var(--success)' : 'var(--text-muted)'} strokeDasharray="5 4" label={{ value: `${step.value}${unit ? ` ${unit}` : ''}`, fill: 'var(--text-muted)', fontSize: 10 }} />
            ))}
            {combinedSeries.map((s, index) => (
              <Line
                key={s.key}
                type="monotone"
                dataKey={s.key}
                name={s.label || s.key}
                stroke={SERIES_COLORS[(s.key.endsWith('__baseline') ? Math.max(0, index - series.length) : index) % SERIES_COLORS.length]}
                dot={false}
                strokeWidth={1.5}
                isAnimationActive={false}
                connectNulls={false}
                strokeDasharray={s.key.endsWith('__baseline') ? '5 4' : undefined}
              />
            ))}
          </ComposedChart>
        </ResponsiveContainer></ExportableChart>
      )}
    </div>
  )
}

function PanelShell({ title, unit, error, empty, children }: { title: string; unit?: string; error?: string; empty: boolean; children: ReactNode }) {
  return <div className="imported-panel-card">
    <div className="imported-panel-card-header"><span className="imported-panel-card-title">{title}</span>{unit ? <span className="imported-panel-card-unit">{unit}</span> : null}</div>
    {error ? <div className="chart-empty-state chart-error-state" role="status">{error}</div> : empty ? <div className="chart-empty-state" role="status">No data in this window</div> : children}
  </div>
}
