import SeriesChartExport from "./SeriesChartExport"
import { useCallback, useEffect, useRef, useState } from 'react'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { request } from '../hooks/apiClient'
import type { PrometheusGraphsResponse } from '../types'
import { settingsHref } from './settingsNavigation'
import { formatDateTime } from '../utils/dateTime'

export default function MonitoringEvidenceCard({ canManage }: { canManage: boolean }) {
  const [minutes, setMinutes] = useState(60)
  const [data, setData] = useState<PrometheusGraphsResponse | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const generation = useRef(0)
  const invalidate = useCallback(() => { generation.current++ }, [])
  const refresh = useCallback(async () => {
    const current = ++generation.current
    setBusy(true)
    const end = new Date()
    const params = new URLSearchParams({ start: new Date(end.getTime() - minutes * 60000).toISOString(), end: end.toISOString(), step_seconds: String(Math.max(15, Math.ceil(minutes * 60 / 240))) })
    try {
      const result = await request<PrometheusGraphsResponse>(`/monitoring/prometheus/graphs?${params}`, { timeoutMs: 15000 })
      if (current === generation.current) { setData(result); setError('') }
    } catch (err) { if (current === generation.current) setError(err instanceof Error ? err.message : 'Unable to query datasource.') }
    finally { if (current === generation.current) setBusy(false) }
  }, [minutes])
  useEffect(() => { setData(null); void refresh(); return invalidate }, [refresh, invalidate])
  const points = data?.graphs.flatMap(graph => graph.series.flatMap(series => series.values)).filter(point => Number.isFinite(point.value)) || []
  const latest = points.reduce((value, point) => Math.max(value, point.timestamp_ms || Date.parse(point.timestamp) || 0), 0)
  return <section className="monitor-card monitor-evidence" aria-labelledby="monitor-evidence-title">
    <div className="monitor-header"><h3 id="monitor-evidence-title">Datasource evidence</h3></div>
    <div className="monitor-body">
      <p>Deployment-wide Prometheus metrics. APM traces and collector snapshots have separate delivery checks below.</p>
      <div className="monitor-workflow-actions"><label>Metric timeframe<select value={minutes} onChange={event => setMinutes(Number(event.target.value))}><option value={15}>Last 15 minutes</option><option value={60}>Last hour</option><option value={1440}>Last 24 hours</option></select></label><button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void refresh()}>{busy ? 'Querying…' : 'Refresh evidence'}</button>{canManage && <a href={settingsHref('prometheus')}>Configure datasource</a>}</div>
      {error && <p role="alert">{error} Previously loaded samples may be stale.</p>}
      <p role="status">{busy ? 'Checking datasource…' : !data ? 'Datasource unavailable' : !data.enabled || !data.configured ? 'Datasource not enabled or configured' : !points.length ? 'Configured; no samples returned in this timeframe' : `${points.length.toLocaleString()} samples returned · Latest sample ${formatDateTime(latest, { seconds: true })}`}</p>
      {data && <p className="monitor-status-note">Query completed {formatDateTime(data.generated_at, { seconds: true })}. Refresh to check for newer data.</p>}
      {data?.graphs.map(graph => <details key={graph.id}><summary>{graph.title} · {graph.series.length} series{graph.unit ? ` · ${graph.unit}` : ''}</summary>{graph.series.map(series => <figure key={series.key} style={{ margin: '16px 0' }}><figcaption>{series.label}</figcaption><SeriesChartExport id={`monitor-${graph.id}-${series.key}`} title={series.label} rows={series.values} axes={[{ key: "utc", label: "Source timestamp (UTC)", value: point => point.timestamp }, { key: "epoch", label: "Timestamp (ms)", value: point => point.timestamp_ms }]} series={[{ key: "value", label: graph.title, unit: graph.unit }]} context={{ graph_id: graph.id, series_key: series.key, labels: series.labels, query_start: data.start, query_end: data.end, step_seconds: data.step_seconds, generated_at: data.generated_at }}><ResponsiveContainer width="100%" height={180}><LineChart data={series.values.map(point => ({ ...point, time: point.timestamp_ms || Date.parse(point.timestamp) }))}><CartesianGrid strokeDasharray="3 3" /><XAxis dataKey="time" type="number" domain={['dataMin', 'dataMax']} tickFormatter={value => new Date(value).toLocaleTimeString()} /><YAxis width={64} /><Tooltip labelFormatter={value => formatDateTime(Number(value), { seconds: true })} /><Line dataKey="value" name={graph.unit || graph.title} stroke="var(--accent, #4f86ff)" dot={false} isAnimationActive={false} /></LineChart></ResponsiveContainer></SeriesChartExport><p className="monitor-status-note">{series.values.length} samples · Latest value {series.values[series.values.length - 1]?.value ?? 'None'} {graph.unit}</p></figure>)}</details>)}
    </div>
  </section>
}
