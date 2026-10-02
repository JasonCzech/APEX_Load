import SeriesChartExport from "./SeriesChartExport"
import { formatAnalysisTimeTick } from '../utils/analysisTime'
import { useEffect, useRef, useState } from 'react'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { request } from '../hooks/apiClient'
import type { AnalysisImport, AnalysisPoint } from '../types/analysis'

interface Score { lag_seconds: number; pairs: number; expected_buckets: number; coverage: number; coefficient?: number }
interface Selection { dataset_id?: string; run_id?: string; source_id?: string; transaction?: string; host?: string; dimensions?: Record<string, string>; protocol?: string; group?: string; metric: string; aggregation: string }
interface Definition { left: Selection; right: Selection; from: string; to: string; bucket_seconds: number; max_lag_seconds: number; mode: string }
interface SavedComparison { id: string; name: string; revision: number; definition: Definition; deleted: boolean }
interface Comparison { request: Definition; left: AnalysisPoint[]; right: AnalysisPoint[]; correlation?: Score[] }

export default function AnalysisComparison({ projectId, dataset, datasets, canEdit, onAssociated }: { projectId: string; dataset: AnalysisImport; datasets: AnalysisImport[]; canEdit: boolean; onAssociated: (in_: AnalysisImport) => void }) {
  const [target, setTarget] = useState('')
  const [runId, setRunId] = useState(dataset.run_id ?? '')
  const [sourceId, setSourceId] = useState('')
  const [transaction, setTransaction] = useState('')
  const [host, setHost] = useState('')
  const [dimensions, setDimensions] = useState('{}')
  const [protocol, setProtocol] = useState('')
  const [group, setGroup] = useState('')
  const [leftMetric, setLeftMetric] = useState(dataset.mapping?.measures[0]?.name ?? '')
  const [rightMetric, setRightMetric] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [bucket, setBucket] = useState(10)
  const [lag, setLag] = useState(0)
  const [mode, setMode] = useState('overlay')
  const [aggregation, setAggregation] = useState('avg')
  const [rightAggregation, setRightAggregation] = useState('avg')
  const [saved, setSaved] = useState<SavedComparison[]>([])
  const [savedRevision, setSavedRevision] = useState(0)
  const [result, setResult] = useState<Comparison>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [saveName, setSaveName] = useState('')
  const operation = useRef<AbortController>()
  useEffect(() => () => operation.current?.abort(), [projectId, dataset.id])
  useEffect(() => {
    const controller = new AbortController()
    void request<SavedComparison[]>(`/analysis/comparisons?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal }).then(items => { if (!controller.signal.aborted) setSaved(items.filter(x => !x.deleted && x.definition.left.dataset_id === dataset.id)) }).catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => controller.abort()
  }, [projectId, dataset.id, savedRevision])
  const applySaved = (d: Definition) => {
    setTarget(d.right.run_id ? 'run' : d.right.dataset_id ?? ''); setRunId(d.right.run_id ?? ''); setSourceId(d.right.source_id ?? ''); setTransaction(d.right.transaction ?? '')
    setHost(d.right.host ?? ''); setDimensions(JSON.stringify(d.right.dimensions ?? {})); setProtocol(d.right.protocol ?? ''); setGroup(d.right.group ?? '')
    setLeftMetric(d.left.metric); setRightMetric(d.right.metric); setAggregation(d.left.aggregation); setRightAggregation(d.right.aggregation)
    setFrom(new Date(d.from).toISOString().slice(0, 19)); setTo(new Date(d.to).toISOString().slice(0, 19)); setBucket(d.bucket_seconds); setLag(d.max_lag_seconds); setMode(d.mode); setResult(undefined)
  }
  const act = async (work: (signal: AbortSignal) => Promise<void>) => {
    operation.current?.abort(); const controller = new AbortController(); operation.current = controller
    setBusy(true); setError('')
    try { await work(controller.signal) } catch (e) { if (!controller.signal.aborted) setError(String(e)) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  const definition = (): Definition => ({
    left: { dataset_id: dataset.id, metric: leftMetric, aggregation },
    right: target === 'run' ? { run_id: runId, source_id: sourceId, transaction, ...(sourceId ? { host, dimensions: JSON.parse(dimensions) } : { protocol, group }), metric: sourceId ? rightMetric : 'transaction.duration', aggregation: rightAggregation } : { dataset_id: target, metric: rightMetric, aggregation: rightAggregation },
    from: new Date(`${from}Z`).toISOString(), to: new Date(`${to}Z`).toISOString(), bucket_seconds: bucket, max_lag_seconds: mode === 'correlation' ? lag : 0, mode,
  })
  const chart = new Map<string, { timestamp: string; left?: number; right?: number; left_quality?: string; right_quality?: string }>()
  for (const side of ['left', 'right'] as const) for (const point of result?.[side] ?? []) {
    const row = chart.get(point.timestamp_utc) ?? { timestamp: point.timestamp_utc }
    row[side] = point.quality === 'valid' ? point.value : undefined; row[`${side}_quality`] = point.quality; chart.set(point.timestamp_utc, row)
  }
  return <section aria-label="Dataset comparison">
    <h3>Compare and correlate</h3>
    <p>Correlation measures association. Missing evidence stays missing; it does not establish a cause.</p>
    {error && <p role="alert">{error}</p>}
    <fieldset disabled={busy}>
      <legend>Evidence selection</legend>
      <label>Saved comparison<select defaultValue="" onChange={e => { const item = saved.find(x => x.id === e.target.value); if (item) applySaved(item.definition) }}><option value="">Choose a saved comparison…</option>{saved.map(item => <option key={item.id} value={item.id}>{item.name} · revision {item.revision}</option>)}</select></label>
      <label>Dataset metric<input value={leftMetric} onChange={e => setLeftMetric(e.target.value)} /></label>
      <label>Compare with<select value={target} onChange={e => { setTarget(e.target.value); setResult(undefined) }}><option value="">Select evidence…</option>{datasets.filter(d => d.state === 'ready' && d.id !== dataset.id).map(d => <option key={d.id} value={d.id}>{d.name}</option>)}<option value="run">Managed run</option></select></label>
      {target === 'run' && <>
        <label>Run ID<input value={runId} onChange={e => setRunId(e.target.value)} /></label>
        <p>{dataset.run_id ? `Associated with run ${dataset.run_id}` : 'Associate this dataset with a run before comparison.'}</p>
        {canEdit && <button type="button" disabled={!runId || runId === dataset.run_id} onClick={() => void act(async signal => {
          const next = await request<AnalysisImport>(`/analysis/datasets/${encodeURIComponent(dataset.id)}/association`, { method: 'PUT', signal, body: JSON.stringify({ run_id: runId, revision: dataset.revision }) }); if (!signal.aborted) onAssociated(next)
        })}>Associate with run</button>}
        <label>Monitor source ID (leave empty for a transaction)<input value={sourceId} onChange={e => setSourceId(e.target.value)} /></label>
        {sourceId ? <><label>Host<input value={host} onChange={e => setHost(e.target.value)} /></label><label>Dimensions (JSON)<input value={dimensions} onChange={e => setDimensions(e.target.value)} /></label></> : <><label>Transaction name<input value={transaction} onChange={e => setTransaction(e.target.value)} /></label><label>Protocol<input value={protocol} onChange={e => setProtocol(e.target.value)} /></label><label>Group<input value={group} onChange={e => setGroup(e.target.value)} /></label></>}
      </>}
      {(target !== 'run' || sourceId) && <label>Comparison metric<input value={rightMetric} onChange={e => setRightMetric(e.target.value)} /></label>}
      <label>From (UTC)<input type="datetime-local" value={from} onChange={e => setFrom(e.target.value)} /></label>
      <label>To (UTC)<input type="datetime-local" value={to} onChange={e => setTo(e.target.value)} /></label>
      <label>Bucket seconds<input type="number" min={1} max={86400} value={bucket} onChange={e => setBucket(Number(e.target.value))} /></label>
      <label>Dataset aggregation<select value={aggregation} onChange={e => setAggregation(e.target.value)}>{['avg', 'min', 'max', 'count', 'rate', 'p50', 'p95', 'p99'].map(x => <option key={x}>{x}</option>)}</select></label>
      <label>Comparison aggregation<select value={rightAggregation} onChange={e => setRightAggregation(e.target.value)}>{['avg', 'min', 'max', 'sum', 'count', 'rate', 'p50', 'p95', 'p99'].map(x => <option key={x}>{x}</option>)}</select></label>
      <label>Analysis<select value={mode} onChange={e => setMode(e.target.value)}><option value="overlay">Overlay compatible measurements</option><option value="correlation">Lagged correlation</option></select></label>
      {mode === 'correlation' && <label>Maximum lag seconds<input type="number" min={0} max={3600} step={bucket} value={lag} onChange={e => setLag(Number(e.target.value))} /></label>}
      <button type="button" disabled={!target || !leftMetric || !from || !to} onClick={() => void act(async signal => { const next = await request<Comparison>(`/analysis/compare?project_id=${encodeURIComponent(projectId)}`, { method: 'POST', signal, body: JSON.stringify(definition()) }); if (!signal.aborted) setResult(next) })}>Analyze comparison</button>
      {canEdit && <><label>Saved comparison name<input value={saveName} onChange={e => setSaveName(e.target.value)} /></label><button type="button" disabled={!saveName || !result} onClick={() => void act(async signal => { await request(`/analysis/comparisons?project_id=${encodeURIComponent(projectId)}`, { method: 'POST', signal, body: JSON.stringify({ name: saveName, definition: result?.request }) }); if (!signal.aborted) setSavedRevision(x => x + 1) })}>Save comparison</button></>}
    </fieldset>
    {result && result.request.mode === 'overlay' && <div className="analysis-chart"><SeriesChartExport id="dataset-comparison" title="Dataset comparison" rows={[...chart.values()].sort((a,b) => a.timestamp.localeCompare(b.timestamp))} axes={[{ key: "utc", label: "UTC timestamp", value: point => point.timestamp }, { key: "left_quality", label: "Dataset quality", value: point => point.left_quality }, { key: "right_quality", label: "Comparison quality", value: point => point.right_quality }]} series={[{ key: "left", label: result.request.left.metric, unit: result.left[0]?.unit }, { key: "right", label: result.request.right.metric, unit: result.right[0]?.unit }]} context={{ selection: result.request, left_coverage: result.left.filter(point => point.quality === "valid").length, left_buckets: result.left.length, right_coverage: result.right.filter(point => point.quality === "valid").length, right_buckets: result.right.length }}><ResponsiveContainer width="100%" height={280}><LineChart data={[...chart.values()].sort((a, b) => a.timestamp.localeCompare(b.timestamp))} margin={{ top: 8, right: 60, bottom: 8, left: 8 }}><XAxis dataKey="timestamp" tickFormatter={formatAnalysisTimeTick} tick={{ fontSize: 11 }} interval="preserveStartEnd" minTickGap={20} /><YAxis unit={result.left[0]?.unit ? ` ${result.left[0].unit}` : undefined} tick={{ fontSize: 11 }} /><Tooltip /><Line isAnimationActive={false} type="linear" dataKey="left" stroke="var(--accent)" dot={false} connectNulls={false} /><Line isAnimationActive={false} type="linear" dataKey="right" stroke="var(--warning, #bd7800)" dot={false} connectNulls={false} /></LineChart></ResponsiveContainer></SeriesChartExport></div>}
    {result?.correlation && <table><caption>Positive lag means the comparison series follows the dataset.</caption><thead><tr><th>Lag (seconds)</th><th>Association</th><th>Pairs</th><th>Coverage</th></tr></thead><tbody>{result.correlation.map(score => <tr key={score.lag_seconds}><td>{score.lag_seconds}</td><td>{score.coefficient == null ? 'Insufficient variation or evidence' : score.coefficient.toFixed(3)}</td><td>{score.pairs} / {score.expected_buckets}</td><td>{(100 * score.coverage).toFixed(1)}%</td></tr>)}</tbody></table>}
  </section>
}
