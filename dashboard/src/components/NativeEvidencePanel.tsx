import { useEffect, useState } from 'react'
import { Legend, ReferenceLine, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { request } from '../hooks/apiClient'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'
import TransactionBucketInspector from './TransactionBucketInspector'
import type { InspectorSelection } from './bucketInspectorModel'

import { nativeEvidencePlotPoints, type NativeEvidencePoint as Point } from './nativeEvidenceModel'
interface Series { name: string; unit: string; points: Point[] }
interface OverlayPoint { timestamp_utc: string; value: number | null; unit: string; quality: string; metric_name: string }
interface Correlation { lag_seconds: number; pairs: number; coverage: number; coefficient?: number }
interface Result { annotations?: { id: string; timestamp: string; label: string }[]; live_bucket_mode?: string; mode: string; start: string; from: string; to: string; series: Series[]; warnings: string[]; overlay?: OverlayPoint[]; correlation?: Record<string, Correlation[]> }
const colors = ['#38bdf8', '#a78bfa', '#34d399', '#fb7185', '#fbbf24']

export default function NativeEvidencePanel({ panel, testId }: { panel: DashboardDefinitionPanel; testId: string }) {
  const [selection, setSelection] = useState<InspectorSelection | null>(null)
  const encoded = JSON.stringify(panel.query)
  const live = panel.query?.evidence_mode === 'live'
  const [revision, setRevision] = useState(0)
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    setResult(null); setError(''); setSelection(null)
    const load = async () => {
      try {
        const value = await request<Result>(`/tests/${encodeURIComponent(testId)}/native-series`, { method: 'POST', body: encoded, signal: abort.signal, timeoutMs: 35000 })
        if (!abort.signal.aborted) { setResult(value); setError('') }
      } catch (reason) {
        if (!abort.signal.aborted) { setResult(null); setError(reason instanceof Error ? reason.message : 'Evidence unavailable') }
      } finally {
        if (live && !abort.signal.aborted) timer = setTimeout(() => { void load() }, 10000)
      }
    }
    void load()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [testId, encoded, revision, live])
  if (error) return <div><p role="alert">Evidence unavailable: {error}</p><button onClick={() => setRevision(value => value + 1)}>Retry evidence query</button></div>
  if (!result) return <p role="status">Loading selected evidence…</p>
  const rowsByTime = new Map<number, Record<string, number | null>>()
  result.series.forEach((series, index) => nativeEvidencePlotPoints(series.points, result.start, live && result.live_bucket_mode !== 'merged').forEach(point => {
    const row = rowsByTime.get(point.offset_seconds) || { elapsed: point.offset_seconds }
    row[`series${index}`] = point.value
    if (point.coverage !== undefined) row.coverage = point.coverage * 100
    rowsByTime.set(point.offset_seconds, row)
  }))
  result.overlay?.forEach(point => {
    const elapsed = (Date.parse(point.timestamp_utc) - Date.parse(result.start)) / 1000
    const row = rowsByTime.get(elapsed) || { elapsed }
    row.overlay = point.quality === 'valid' ? point.value : null
    rowsByTime.set(elapsed, row)
  })
  const rows = [...rowsByTime.values()].sort((a, b) => Number(a.elapsed) - Number(b.elapsed))
  const originalIntervals = live && result.live_bucket_mode !== 'merged'
  const unit = result.series[0]?.unit || ''
  const overlayUnit = result.overlay?.find(point => point.unit)?.unit || 'unavailable'
  const hasCoverage = result.series.some(series => series.points.some(point => point.coverage !== undefined))
  const columns = [{ key: 'elapsed', label: 'Elapsed seconds', value: (row: Record<string, number | null>) => row.elapsed }, ...result.series.map((series, index) => ({ key: `series${index}`, label: `${series.name} (${series.unit})`, value: (row: Record<string, number | null>) => row[`series${index}`] ?? '' })), ...(hasCoverage ? [{ key: 'coverage', label: 'Collection coverage (%)', value: (row: Record<string, number | null>) => row.coverage ?? '' }] : []), ...(result.overlay ? [{ key: 'overlay', label: `Overlay (${overlayUnit})`, value: (row: Record<string, number | null>) => row.overlay ?? '' }] : [])]
  return <section aria-label="Grouped evidence panel">
    <p>{live ? originalIntervals ? 'Live source intervals · refreshes every 10 seconds' : 'Live merged publication buckets · refreshes every 10 seconds' : 'Archived raw observations'} · {panel.query?.population} · {panel.query?.metric} / {panel.query?.aggregation} · {unit}</p>
    <p>Selection: {panel.query?.filters?.action || 'All identities'}{panel.query?.property_name ? ` · ${panel.query.property_name}=${panel.query.property_value || 'all categories'}` : ''} · Group: {panel.query?.dimensions?.[0] || 'none'} · {panel.query?.start_seconds || 0}–{panel.query?.end_seconds}s</p>
    {panel.query?.overlay_selection && <p>Overlay: {panel.query.overlay_selection.metric} / {panel.query.overlay_selection.aggregation} · {panel.query.overlay_selection.dataset_id ? `Dataset ${panel.query.overlay_selection.dataset_id}` : `Monitor ${panel.query.overlay_selection.source_id}`} · {overlayUnit}, separate axis.</p>}
    {result.warnings.map((warning, index) => <p role="status" key={index}>{warning}</p>)}
    {!result.series.length && <p>No matching evidence.</p>}
    <ExportableChart definitionId={panel.id} allowSVG={panel.kind === 'line'} title={panel.title} description="Selected evidence on elapsed wall time; original units and missing values retained" rows={rows} columns={columns}>
      {panel.kind === 'line' && <ResponsiveContainer width="100%" height={300}><LineChart data={rows}><XAxis type="number" dataKey="elapsed" domain={['dataMin', 'dataMax']} /><YAxis yAxisId="native" label={{ value: unit, angle: -90, position: 'insideLeft' }} />{result.overlay && <YAxis yAxisId="overlay" orientation="right" label={{ value: overlayUnit, angle: 90, position: 'insideRight' }} />}<Tooltip /><Legend />{result.annotations?.map(annotation => <ReferenceLine key={annotation.id} yAxisId="native" x={(Date.parse(annotation.timestamp) - Date.parse(result.start)) / 1000} label={annotation.label} stroke="#94a3b8" strokeDasharray="4 4" />)}{result.series.map((series, index) => <Line key={series.name} yAxisId="native" name={`${series.name} (${series.unit})`} data={nativeEvidencePlotPoints(series.points, result.start, live && result.live_bucket_mode !== 'merged').map(point => ({ elapsed: point.offset_seconds, [`series${index}`]: point.value }))} dataKey={`series${index}`} stroke={colors[index % colors.length]} dot={false} connectNulls={false} isAnimationActive={false} />)}{result.overlay && <Line yAxisId="overlay" name={`Overlay (${overlayUnit})`} data={result.overlay.map(point => ({ elapsed: (Date.parse(point.timestamp_utc) - Date.parse(result.start)) / 1000, overlay: point.quality === 'valid' ? point.value : null }))} dataKey="overlay" stroke="#f97316" dot={false} connectNulls={false} isAnimationActive={false} />}</LineChart></ResponsiveContainer>}
      {panel.kind === 'stat' && result.series.map(series => <p key={series.name}>{series.name}: {series.points[series.points.length - 1]?.value ?? 'Missing'} {series.unit}</p>)}
      <details open={panel.kind === 'table'}><summary>Evidence table ({rows.length} timestamps)</summary><table><thead><tr>{columns.map(column => <th key={column.key}>{column.label}</th>)}</tr></thead><tbody>{rows.map(row => <tr key={row.elapsed}>{columns.map(column => <td key={column.key}>{column.value(row) === '' ? 'Missing' : column.value(row)}</td>)}</tr>)}</tbody></table></details>
    </ExportableChart>
    {!live && panel.query && <details><summary>Inspect grouped archive buckets</summary>{result.series.map(series => <section key={series.name}><h4>{series.name}</h4>{series.points.map(point => <button key={point.from} disabled={!point.count} onClick={() => setSelection({ run: testId, from: point.from, to: point.to, group: panel.query?.dimensions?.[0], name: series.name })}>Inspect {series.name} bucket {point.offset_seconds}s ({point.count})</button>)}</section>)}</details>}
    {selection && panel.query && <TransactionBucketInspector key={`${selection.run}-${selection.from}-${selection.name}`} query={panel.query} selection={selection} onClose={() => setSelection(null)} />}
    {result.correlation && <details><summary>Lag correlation and coverage</summary><p>Positive lag means overlay evidence follows the selected series. Association does not prove causation.</p>{Object.entries(result.correlation).map(([name, scores]) => <section key={name}><h4>{name}</h4><table><thead><tr><th>Lag seconds</th><th>Coefficient</th><th>Pairs</th><th>Coverage</th></tr></thead><tbody>{scores.map(score => <tr key={score.lag_seconds}><td>{score.lag_seconds}</td><td>{score.coefficient?.toFixed(3) ?? 'Unscored'}</td><td>{score.pairs}</td><td>{(score.coverage * 100).toFixed(1)}%</td></tr>)}</tbody></table></section>)}</details>}
  </section>
}
