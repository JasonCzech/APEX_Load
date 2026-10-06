import SeriesChartExport from "./SeriesChartExport"
import { formatAnalysisTimeTick } from '../utils/analysisTime'
import { useEffect, useRef, useState } from 'react'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { request } from '../hooks/apiClient'
import type { AnalysisImport, AnalysisPoint } from '../types/analysis'
import AnalysisIdentityFilters from './AnalysisIdentityFilters'
import { allAnalysisAggregations, analysisCommonWindow, analysisCommonWindowAnchor, analysisMetricAggregations, analysisWindowAlignedRange, analysisWindowAnchor, defaultAnalysisAggregations } from '../utils/analysisSummary'
import { analysisIdentityFilterError, analysisMappingDisclosureError, analysisIdentityTextError, analysisImportedIdentityAvailable, analysisImportedSelectorsRequested, analysisMappingRequiresImportedIdentity, buildAnalysisIdentitySelection, emptyAnalysisIdentityFilters, restoreAnalysisIdentityFilters, restoreAnalysisImportedIdentityFilters } from '../utils/analysisIdentity'

interface Score { lag_seconds: number; pairs: number; expected_buckets: number; coverage: number; coefficient?: number }
interface Selection { dataset_id?: string; run_id?: string; source_id?: string; transaction?: string; host?: string; service?: string; dimensions?: Record<string, string>; protocol?: string; group?: string; metric: string; aggregation: string }
interface Definition { left: Selection; right: Selection; from: string; to: string; bucket_seconds: number; max_lag_seconds: number; mode: string }
interface SavedComparison { id: string; name: string; revision: number; definition: Definition; deleted: boolean }
interface Comparison { request: Definition; left: AnalysisPoint[]; right: AnalysisPoint[]; correlation?: Score[] }

export default function AnalysisComparison({ projectId, dataset, datasets, canEdit, onAssociated, importedIdentityVersion }: { importedIdentityVersion?: number; projectId: string; dataset: AnalysisImport; datasets: AnalysisImport[]; canEdit: boolean; onAssociated: (in_: AnalysisImport) => void }) {
  const [target, setTarget] = useState('')
  const [runId, setRunId] = useState(dataset.run_id ?? '')
  const [sourceId, setSourceId] = useState('')
  const [transaction, setTransaction] = useState('')
  const [host, setHost] = useState('')
  const [service, setService] = useState('')
  const [leftFilters, setLeftFilters] = useState(emptyAnalysisIdentityFilters)
  const [rightFilters, setRightFilters] = useState(emptyAnalysisIdentityFilters)
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
  const identityAvailable = analysisImportedIdentityAvailable(importedIdentityVersion)
  const retainedImportedFilters = leftFilters.source_id !== '' || leftFilters.host !== '' || leftFilters.service !== '' || leftFilters.dimensions.length > 0 || target !== 'run' && (rightFilters.source_id !== '' || rightFilters.host !== '' || rightFilters.service !== '' || rightFilters.dimensions.length > 0)
  const identityDisclosureError = analysisMappingDisclosureError(dataset.mapping) || target !== 'run' && analysisMappingDisclosureError(datasets.find(item => item.id === target)?.mapping)
  const identityUnsupported = !identityAvailable && (analysisMappingRequiresImportedIdentity(dataset.mapping) || analysisMappingRequiresImportedIdentity(datasets.find(item => item.id === target)?.mapping) || retainedImportedFilters)
  const resultIdentityUnsupported = !identityAvailable && result !== undefined && (analysisImportedSelectorsRequested(result.request.left) || analysisImportedSelectorsRequested(result.request.right))
  useEffect(() => () => operation.current?.abort(), [projectId, dataset.id])
  useEffect(() => {
    const controller = new AbortController()
    void request<SavedComparison[]>(`/analysis/comparisons?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal }).then(items => { if (!controller.signal.aborted) setSaved(items.filter(x => !x.deleted && x.definition.left.dataset_id === dataset.id)) }).catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => controller.abort()
  }, [projectId, dataset.id, savedRevision])
  const applySaved = (d: Definition) => {
    const disclosureError = analysisMappingDisclosureError(dataset.mapping) || analysisMappingDisclosureError(datasets.find(item => item.id === d?.right?.dataset_id)?.mapping)
    if (disclosureError) throw new Error(disclosureError)
    if (!identityAvailable && (analysisMappingRequiresImportedIdentity(dataset.mapping) || analysisMappingRequiresImportedIdentity(datasets.find(item => item.id === d?.right?.dataset_id)?.mapping) || analysisImportedSelectorsRequested(d?.left) || analysisImportedSelectorsRequested(d?.right))) throw new Error('This comparison requires imported identity schema 2, which is unavailable on this controller.')
    // Admit the complete saved view before any setter: a bad later date or
    // selector must not partially replace a user's current comparison.
    if (!d || typeof d !== 'object' || !d.left || !d.right || d.left.dataset_id !== dataset.id || d.left.run_id || (!!d.right.run_id === !!d.right.dataset_id)) throw new Error('Saved comparison has invalid evidence identities.')
    for (const selection of [d.left, d.right]) {
      for (const [text, limit] of [[selection.metric, 256], [selection.transaction ?? '', 256], [selection.protocol ?? '', 64], [selection.group ?? '', 256], [selection.dataset_id ?? '', 128], [selection.run_id ?? '', 128], [selection.source_id ?? '', selection.run_id ? 128 : 256]] as const) {
        const failure = analysisIdentityTextError(text, limit)
        if (failure) throw new Error(failure)
      }
      if (!selection.metric || !allAnalysisAggregations.includes(selection.aggregation)) throw new Error('Saved comparison has invalid measurement semantics.')
    }
    const leftIdentity = restoreAnalysisImportedIdentityFilters(d.left)
    const admittedRightIdentity = d.right.dataset_id ? restoreAnalysisImportedIdentityFilters(d.right) : restoreAnalysisIdentityFilters(d.right)
    const rightIdentity = d.right.dataset_id ? admittedRightIdentity : emptyAnalysisIdentityFilters()
    const rightDimensions = JSON.stringify(buildAnalysisIdentitySelection(admittedRightIdentity).dimensions ?? {})
    if (analysisIdentityTextError(d.from, 64, true) || analysisIdentityTextError(d.to, 64, true) || !/(?:Z|[+-]\d{2}:\d{2})$/.test(d.from) || !/(?:Z|[+-]\d{2}:\d{2})$/.test(d.to)) throw new Error('Saved comparison requires explicit UTC or offset timestamps.')
    for (const bound of [d.from, d.to]) {
      const timestamp = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-](\d{2}):(\d{2}))$/.exec(bound)
      if (!timestamp || /[1-9]/.test(timestamp[7]?.slice(4) ?? '')) throw new Error('Saved comparison timestamps require millisecond precision or an exactly representable fraction.')
      const year = Number(timestamp[1]); const month = Number(timestamp[2]); const day = Number(timestamp[3])
      const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
      if (year < 1 || month < 1 || month > 12 || day < 1 || day > days[month - 1] || Number(timestamp[4]) > 23 || Number(timestamp[5]) > 59 || Number(timestamp[6]) > 59 || Number(timestamp[9] ?? 0) > 23 || Number(timestamp[10] ?? 0) > 59) throw new Error('Saved comparison requires valid calendar dates and offsets.')
    }
    const fromDate = new Date(d.from); const toDate = new Date(d.to)
    const duration = (toDate.valueOf() - fromDate.valueOf()) / 1000
    if (!Number.isFinite(duration) || fromDate.getUTCFullYear() < 1 || toDate.getUTCFullYear() > 9999 || duration <= 0 || duration > 366 * 86400 || !Number.isInteger(d.bucket_seconds) || d.bucket_seconds < 1 || d.bucket_seconds > 86400 || duration / d.bucket_seconds > 10000 || !Number.isInteger(d.max_lag_seconds) || d.max_lag_seconds < 0 || d.max_lag_seconds > 3600 || d.max_lag_seconds % d.bucket_seconds !== 0 || 2 * d.max_lag_seconds / d.bucket_seconds + 1 > 121 || !['overlay', 'correlation'].includes(d.mode)) throw new Error('Saved comparison has an invalid window or lag.')
    const admittedFrom = fromDate.toISOString().slice(0, -1); const admittedTo = toDate.toISOString().slice(0, -1)
    setLeftFilters(leftIdentity); setRightFilters(rightIdentity)
    setTarget(d.right.run_id ? 'run' : d.right.dataset_id ?? ''); setRunId(d.right.run_id ?? ''); setSourceId(d.right.source_id ?? ''); setTransaction(d.right.transaction ?? '')
    setHost(d.right.host ?? ''); setService(d.right.service ?? ''); setDimensions(rightDimensions); setProtocol(d.right.protocol ?? ''); setGroup(d.right.group ?? '')
    setLeftMetric(d.left.metric); setRightMetric(d.right.metric); setAggregation(d.left.aggregation); setRightAggregation(d.right.aggregation)
    setFrom(admittedFrom); setTo(admittedTo); setBucket(d.bucket_seconds); setLag(d.max_lag_seconds); setMode(d.mode); setResult(undefined)
  }
  const act = async (work: (signal: AbortSignal) => Promise<void>) => {
    operation.current?.abort(); const controller = new AbortController(); operation.current = controller
    setBusy(true); setError('')
    try { await work(controller.signal) } catch (e) { if (!controller.signal.aborted) setError(String(e)) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  // A dataset metric declared as a summary or histogram answers only its own
  // statistics, over whole source windows.
  const leftAggregations = analysisMetricAggregations(dataset.mapping, leftMetric)
  const rightAggregations = target && target !== 'run' ? analysisMetricAggregations(datasets.find(item => item.id === target)?.mapping, rightMetric) : { options: defaultAnalysisAggregations }
  const rightDataset = target && target !== 'run' ? datasets.find(item => item.id === target) : undefined
  const windowSides = [
    { windowSeconds: leftAggregations.windowSeconds?.(aggregation), dataset: dataset as AnalysisImport | undefined, metric: leftMetric },
    { windowSeconds: rightAggregations.windowSeconds?.(rightAggregation), dataset: rightDataset, metric: rightMetric },
  ].flatMap(side => side.windowSeconds ? [{ windowSeconds: side.windowSeconds, anchorMs: analysisWindowAnchor(side.dataset, side.metric, side.windowSeconds) }] : [])
  // One bucket must cover whole source windows on both sides, starting where both grids start.
  const sourceWindow = analysisCommonWindow(windowSides.map(side => side.windowSeconds))
  const effectiveBucket = sourceWindow ? Math.ceil(bucket / sourceWindow) * sourceWindow : bucket
  const definition = (): Definition => {
    const range = analysisWindowAlignedRange(Date.parse(`${from}Z`), Date.parse(`${to}Z`), sourceWindow, sourceWindow ? analysisCommonWindowAnchor(windowSides, sourceWindow) : undefined)
    return {
      left: { dataset_id: dataset.id, ...buildAnalysisIdentitySelection(leftFilters), metric: leftMetric, aggregation },
      right: target === 'run' ? { run_id: runId, source_id: sourceId, transaction, host, service, dimensions: JSON.parse(dimensions), ...(!sourceId ? { protocol, group } : {}), metric: sourceId ? rightMetric : 'transaction.duration', aggregation: rightAggregation } : { dataset_id: target, ...buildAnalysisIdentitySelection(rightFilters), metric: rightMetric, aggregation: rightAggregation },
      from: new Date(range.fromMs).toISOString(), to: new Date(range.toMs).toISOString(), bucket_seconds: effectiveBucket, max_lag_seconds: mode === 'correlation' ? Math.ceil(lag / effectiveBucket) * effectiveBucket : 0, mode,
    }
  }
  const chart = new Map<string, { timestamp: string; left?: number; right?: number; left_quality?: string; right_quality?: string }>()
  for (const side of ['left', 'right'] as const) for (const point of result?.[side] ?? []) {
    const row = chart.get(point.timestamp_utc) ?? { timestamp: point.timestamp_utc }
    row[side] = point.quality === 'valid' ? point.value : undefined; row[`${side}_quality`] = point.quality; chart.set(point.timestamp_utc, row)
  }
  return <section aria-label="Dataset comparison">
    <h3>Compare and correlate</h3>
    <p>Correlation measures association. Missing evidence stays missing; it does not establish a cause.</p>
    {error && <p role="alert">{error}</p>}
    {identityDisclosureError && <p role="alert">{identityDisclosureError} Stored mappings remain unchanged; this comparison cannot read their mapped source values.</p>}
    {identityUnsupported && <p role="alert">This comparison requires imported identity schema 2. It is unavailable on this controller; stored selectors remain unchanged.</p>}
    <fieldset disabled={busy}>
      <legend>Evidence selection</legend>
      <label>Saved comparison<select defaultValue="" onChange={e => { const item = saved.find(x => x.id === e.target.value); if (item) { try { applySaved(item.definition); setError('') } catch (failure) { setError(String(failure)) } } }}><option value="">Choose a saved comparison…</option>{saved.map(item => <option key={item.id} value={item.id}>{item.name} · revision {item.revision}</option>)}</select></label>
      <label>Dataset metric<input value={leftMetric} onChange={e => setLeftMetric(e.target.value)} /></label>
      {identityAvailable && dataset.mapping?.version === 2 && <AnalysisIdentityFilters label="Dataset identity filters" value={leftFilters} onChange={setLeftFilters} />}
      <label>Compare with<select value={target} onChange={e => { setTarget(e.target.value); setResult(undefined); setRightFilters(emptyAnalysisIdentityFilters()) }}><option value="">Select evidence…</option>{datasets.filter(d => d.state === 'ready' && d.id !== dataset.id).map(d => <option key={d.id} value={d.id}>{d.name}</option>)}<option value="run">Managed run</option></select></label>
      {identityAvailable && target !== 'run' && datasets.find(item => item.id === target)?.mapping?.version === 2 && <AnalysisIdentityFilters label="Comparison identity filters" value={rightFilters} onChange={setRightFilters} />}
      {target === 'run' && <>
        <label>Run ID<input value={runId} onChange={e => setRunId(e.target.value)} /></label>
        <p>{dataset.run_id ? `Associated with run ${dataset.run_id}` : 'Associate this dataset with a run before comparison.'}</p>
        {canEdit && <button type="button" disabled={!runId || runId === dataset.run_id} onClick={() => void act(async signal => {
          const next = await request<AnalysisImport>(`/analysis/datasets/${encodeURIComponent(dataset.id)}/association`, { method: 'PUT', signal, body: JSON.stringify({ run_id: runId, revision: dataset.revision }) }); if (!signal.aborted) onAssociated(next)
        })}>Associate with run</button>}
        <label>Monitor source ID (leave empty for a transaction)<input value={sourceId} onChange={e => setSourceId(e.target.value)} /></label>
        {!sourceId && <><label>Transaction name<input value={transaction} onChange={e => setTransaction(e.target.value)} /></label><label>Protocol<input value={protocol} onChange={e => setProtocol(e.target.value)} /></label><label>Group<input value={group} onChange={e => setGroup(e.target.value)} /></label></>}
        <label>Host<input value={host} onChange={e => setHost(e.target.value)} /></label><label>Service<input value={service} onChange={e => setService(e.target.value)} /></label><label>Dimensions (JSON)<input value={dimensions} onChange={e => setDimensions(e.target.value)} /></label>
        <p>Filters require matching retained labels. Archived transaction evidence captures protocol, group and population; uncaptured host, service or other labels remain unavailable.</p>
      </>}
      {(target !== 'run' || sourceId) && <label>Comparison metric<input value={rightMetric} onChange={e => setRightMetric(e.target.value)} /></label>}
      <label>From (UTC)<input type="datetime-local" step="0.001" value={from} onChange={e => setFrom(e.target.value)} /></label>
      <label>To (UTC)<input type="datetime-local" step="0.001" value={to} onChange={e => setTo(e.target.value)} /></label>
      <label>Bucket seconds<input type="number" min={1} max={86400} value={bucket} onChange={e => setBucket(Number(e.target.value))} /></label>
      {sourceWindow && effectiveBucket !== bucket && <p>Buckets widen to {effectiveBucket} seconds to cover whole {sourceWindow}-second source windows.</p>}
      <label>Dataset aggregation<select value={aggregation} onChange={e => setAggregation(e.target.value)}>{(leftAggregations.options.includes(aggregation) ? leftAggregations.options : [aggregation, ...leftAggregations.options]).map(x => <option key={x}>{x}</option>)}</select></label>
      <label>Comparison aggregation<select value={rightAggregation} onChange={e => setRightAggregation(e.target.value)}>{(rightAggregations.options.includes(rightAggregation) ? rightAggregations.options : [rightAggregation, ...rightAggregations.options]).map(x => <option key={x}>{x}</option>)}</select></label>
      <label>Analysis<select value={mode} onChange={e => setMode(e.target.value)}><option value="overlay">Overlay compatible measurements</option><option value="correlation">Lagged correlation</option></select></label>
      {mode === 'correlation' && <label>Maximum lag seconds<input type="number" min={0} max={3600} step={bucket} value={lag} onChange={e => setLag(Number(e.target.value))} /></label>}
      <button type="button" disabled={!!identityDisclosureError || identityUnsupported || !target || !leftMetric || !from || !to || !!analysisIdentityFilterError(leftFilters) || target !== 'run' && !!analysisIdentityFilterError(rightFilters)} onClick={() => void act(async signal => { if (identityDisclosureError) throw new Error(identityDisclosureError); if (identityUnsupported) throw new Error('Imported identity schema 2 is unavailable on this controller.'); const next = await request<Comparison>(`/analysis/compare?project_id=${encodeURIComponent(projectId)}`, { method: 'POST', signal, body: JSON.stringify(definition()) }); if (!signal.aborted) setResult(next) })}>Analyze comparison</button>
      {canEdit && <><label>Saved comparison name<input value={saveName} onChange={e => setSaveName(e.target.value)} /></label><button type="button" disabled={!!identityDisclosureError || identityUnsupported || resultIdentityUnsupported || !saveName || !result} onClick={() => void act(async signal => { if (identityDisclosureError) throw new Error(identityDisclosureError); if (identityUnsupported || resultIdentityUnsupported) throw new Error('Imported identity schema 2 is unavailable on this controller.'); await request(`/analysis/comparisons?project_id=${encodeURIComponent(projectId)}`, { method: 'POST', signal, body: JSON.stringify({ name: saveName, definition: result?.request }) }); if (!signal.aborted) setSavedRevision(x => x + 1) })}>Save comparison</button></>}
    </fieldset>
    {!identityDisclosureError && result && (result.left.length === 0 || result.right.length === 0) && <p role="status">No matching retained evidence for {result.left.length === 0 && result.right.length === 0 ? 'either series' : result.left.length === 0 ? 'the dataset series' : 'the comparison series'}. Missing or uncaptured labels remain unavailable.</p>}
    {!identityDisclosureError && result && result.request.mode === 'overlay' && <div className="analysis-chart"><SeriesChartExport id="dataset-comparison" title="Dataset comparison" rows={[...chart.values()].sort((a,b) => a.timestamp.localeCompare(b.timestamp))} axes={[{ key: "utc", label: "UTC timestamp", value: point => point.timestamp }, { key: "left_quality", label: "Dataset quality", value: point => point.left_quality }, { key: "right_quality", label: "Comparison quality", value: point => point.right_quality }]} series={[{ key: "left", label: result.request.left.metric, unit: result.left[0]?.unit }, { key: "right", label: result.request.right.metric, unit: result.right[0]?.unit }]} context={{ selection: result.request, left_coverage: result.left.filter(point => point.quality === "valid").length, left_buckets: result.left.length, right_coverage: result.right.filter(point => point.quality === "valid").length, right_buckets: result.right.length }}><ResponsiveContainer width="100%" height={280}><LineChart data={[...chart.values()].sort((a, b) => a.timestamp.localeCompare(b.timestamp))} margin={{ top: 8, right: 60, bottom: 8, left: 8 }}><XAxis dataKey="timestamp" tickFormatter={formatAnalysisTimeTick} tick={{ fontSize: 11 }} interval="preserveStartEnd" minTickGap={20} /><YAxis unit={result.left[0]?.unit ? ` ${result.left[0].unit}` : undefined} tick={{ fontSize: 11 }} /><Tooltip /><Line isAnimationActive={false} type="linear" dataKey="left" stroke="var(--accent)" dot={false} connectNulls={false} /><Line isAnimationActive={false} type="linear" dataKey="right" stroke="var(--warning, #bd7800)" dot={false} connectNulls={false} /></LineChart></ResponsiveContainer></SeriesChartExport></div>}
    {!identityDisclosureError && result?.correlation && <table><caption>Positive lag means the comparison series follows the dataset.</caption><thead><tr><th>Lag (seconds)</th><th>Association</th><th>Pairs</th><th>Coverage</th></tr></thead><tbody>{result.correlation.map(score => <tr key={score.lag_seconds}><td>{score.lag_seconds}</td><td>{score.coefficient == null ? 'Insufficient variation or evidence' : score.coefficient.toFixed(3)}</td><td>{score.pairs} / {score.expected_buckets}</td><td>{(100 * score.coverage).toFixed(1)}%</td></tr>)}</tbody></table>}
  </section>
}
