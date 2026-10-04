import { useMemo, useState } from 'react'
import SeriesChartExport from './SeriesChartExport'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import type { MetricsHistoryPoint } from '../types'
import type { AnalysisPoint } from '../types/analysis'
import { buildMonitorTimelineRows, latestMonitorQueryAggregate, monitorLoadOverlays, type MonitorLoadOverlay, type MonitorTimelineRow } from './monitorTimelineModel'

export default function MonitorTimelinePlot({ points, history, window }: { points: AnalysisPoint[]; history: MetricsHistoryPoint[]; window?: { from: string; to: string } }) {
  const [overlay, setOverlay] = useState<MonitorLoadOverlay>('latency')
  const rows = useMemo(() => buildMonitorTimelineRows(points, history, overlay, window), [points, history, overlay, window])
  const load = monitorLoadOverlays[overlay]
  const aggregate = latestMonitorQueryAggregate(points)
  const pointByTime = new Map(points.map(point => [Date.parse(point.timestamp_utc), point]))
  const exportRows: (MonitorTimelineRow & { point?: AnalysisPoint })[] = aggregate
    ? [{ time: Date.parse(aggregate.timestamp_utc), monitor: aggregate.quality === 'valid' && Number.isFinite(aggregate.value) ? aggregate.value : null, point: aggregate }]
    : rows.map(row => ({ ...row, point: 'monitor' in row ? pointByTime.get(row.time) : undefined }))
  const axes = [
    { key: 'utc', label: 'UTC timestamp', value: (row: typeof exportRows[number]) => new Date(row.time).toISOString() },
    { key: 'quality', label: 'Monitor quality', value: (row: typeof exportRows[number]) => row.point?.quality },
    { key: 'host', label: 'Monitor host', value: (row: typeof exportRows[number]) => row.point?.host },
    { key: 'dimensions', label: 'Monitor dimensions', value: (row: typeof exportRows[number]) => row.point?.dimensions },
    { key: 'entity_ids', label: 'Provider entity identities', value: (row: typeof exportRows[number]) => row.point?.provider?.entity_ids },
    { key: 'bucket_end', label: 'Provider bucket end', value: (row: typeof exportRows[number]) => row.point?.provider?.bucket_end },
    { key: 'statistic', label: 'Provider statistic', value: (row: typeof exportRows[number]) => row.point?.provider?.statistic },
  ]
  return <SeriesChartExport id="run-monitors" title={aggregate ? 'Analytics query window summary' : 'Run monitor timeline'} rows={exportRows} axes={axes}
    series={[{ key: 'monitor', label: points[0]?.metric_name || 'Monitor', unit: points[0]?.unit }, ...(!aggregate ? [{ key: 'load', label: load.label, unit: load.unit }] : [])]}
    context={{ source: points[0]?.source, metric: points[0]?.metric_name, window: window || null, overlay: aggregate ? 'query_window' : overlay }}>
    {aggregate ? <section aria-label="Analytics query window summary"><h4>{aggregate.metric_name}: {aggregate.quality === 'valid' && Number.isFinite(aggregate.value) ? `${aggregate.value?.toLocaleString()} ${aggregate.unit}` : 'No valid aggregate'}</h4><p>Most recent query window: {aggregate.provider?.bucket_start} – {aggregate.provider?.bucket_end}. {aggregate.provider?.settled ? 'Settled' : 'Provisional'} · source statistic {aggregate.provider?.statistic}.</p><p>This value summarizes one complete query window. Queries can cover different windows; their aggregate values are not plotted as a bucket timeline. Terminal criteria require a matching complete evaluation window.</p></section> : <div aria-label="Monitoring metric timeline">
      <label>Load metric overlay<select value={overlay} onChange={event => setOverlay(event.target.value as MonitorLoadOverlay)}>{Object.entries(monitorLoadOverlays).map(([id, item]) => <option key={id} value={id}>{item.label} ({item.unit})</option>)}</select></label>
      <p>Absent source buckets appear as gaps. Source timestamps align with load metrics in UTC; cached values are not repeated as observations.</p>
      <ResponsiveContainer width="100%" height={260}><LineChart data={rows}>
        <XAxis dataKey="time" type="number" domain={['dataMin', 'dataMax']} tickFormatter={value => new Date(value).toLocaleTimeString()} />
        <YAxis yAxisId="monitor" unit={points[0]?.unit ? ` ${points[0].unit}` : undefined} />
        <YAxis yAxisId="load" orientation="right" unit={` ${load.unit}`} />
        <Tooltip labelFormatter={value => new Date(Number(value)).toISOString()} />
        <Line name={points[0]?.metric_name || 'Monitor'} isAnimationActive={false} data={rows.filter(row => 'monitor' in row)} yAxisId="monitor" dataKey="monitor" stroke="var(--chart-1)" dot={false} connectNulls={false} />
        <Line name={load.label} isAnimationActive={false} data={rows.filter(row => 'load' in row)} yAxisId="load" dataKey="load" stroke="var(--chart-2)" dot={false} connectNulls={false} />
      </LineChart></ResponsiveContainer>
    </div>}
  </SeriesChartExport>
}
