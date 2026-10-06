import SeriesChartExport from "./SeriesChartExport"
import { formatAnalysisTimeTick } from '../utils/analysisTime'
import { useCallback, useEffect, useRef, useState } from 'react'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { apiFetch, request } from '../hooks/apiClient'
import { analysisProtectedIdentityColumn } from '../utils/analysisCredentialColumn'
import type { AnalysisCapabilities, AnalysisImport, AnalysisMapping, AnalysisPoint, AnalysisPreview } from '../types/analysis'
import './AnalysisWorkspace.css'
import { downloadBlob } from '../utils/fileUtils'
import AnalysisSavedMappings from './AnalysisSavedMappings'
import AnalysisComparison from './AnalysisComparison'
import AnalysisIdentityMapping from './AnalysisIdentityMapping'
import AnalysisIdentityFilters from './AnalysisIdentityFilters'
import { analysisIdentityFilterError, analysisMappingDisclosureError, analysisImportedIdentityAvailable, analysisMappingRequiresImportedIdentity, buildAnalysisIdentitySelection, emptyAnalysisIdentityFilters } from '../utils/analysisIdentity'
import AnalysisSummaryMeasureFields from './AnalysisSummaryMeasureFields'
import AnalysisHistogramMeasureFields from './AnalysisHistogramMeasureFields'
import { analysisBucketSeconds, analysisHistogramDeclaration, analysisHistogramMeasureError, analysisMetricAggregations, analysisMetricNames, analysisMissingReasonLabels, analysisSummaryDeclarations, analysisSummaryMeasureError, analysisWindowAlignedRange, analysisWindowAnchor, withAnalysisMeasureKind } from '../utils/analysisSummary'

const importPath = (id: string) => `/analysis/imports/${encodeURIComponent(id)}`
const initialMapping: AnalysisMapping = { version: 1, timestamp: [], timestamp_encoding: 'timestamp', timezone: 'UTC', measures: [] }

export default function AnalysisWorkspace({ projectId, canEdit = false }: { projectId?: string; canEdit?: boolean }) {
  const [capabilities, setCapabilities] = useState<AnalysisCapabilities>()
  const [imports, setImports] = useState<AnalysisImport[]>([])
  const [selected, setSelected] = useState<AnalysisImport>()
  const [files, setFiles] = useState<File[]>([])
  const [name, setName] = useState('')
  const [preview, setPreview] = useState<AnalysisPreview>()
  const [mapping, updateMapping] = useState<AnalysisMapping>(initialMapping)
  const [mappingTemplate, setMappingTemplate] = useState<{ id: string; revision: number }>()
  const setMapping = (next: AnalysisMapping) => { updateMapping(next); setMappingTemplate(undefined) }
  const [acceptRejected, setAcceptRejected] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState('')
  const [points, setPoints] = useState<AnalysisPoint[]>([])
  const [seriesSelection, setSeriesSelection] = useState<Record<string, unknown>>({})
  const [metric, setMetric] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [bucket, setBucket] = useState('')
  const [aggregation, setAggregation] = useState('avg')
  const [identityFilters, setIdentityFilters] = useState(emptyAnalysisIdentityFilters)
  const operation = useRef<AbortController>()

  const reload = useCallback(async (signal?: AbortSignal) => {
    if (!projectId) return
    const next = await request<AnalysisImport[]>(`/analysis/imports?project_id=${encodeURIComponent(projectId)}`, { signal }); if (!signal?.aborted) setImports(next)
  }, [projectId])

  useEffect(() => {
    const controller = new AbortController()
    setSelected(undefined); setPreview(undefined); setPoints([]); setImports([]); setFiles([]); setName(''); setProgress(''); setBusy(false); setAcceptRejected(false); updateMapping(initialMapping); setMappingTemplate(undefined); setCapabilities(undefined); setError(''); setIdentityFilters(emptyAnalysisIdentityFilters())
    if (projectId) request<AnalysisCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal })
      .then(async caps => { if (controller.signal.aborted) return; setCapabilities(caps); if (caps.enabled && caps.native_parquet) await reload(controller.signal) })
      .catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => { controller.abort(); const current = operation.current; operation.current = undefined; current?.abort() }
  }, [projectId, reload])

  useEffect(() => {
    if (selected?.state !== 'normalizing') return
    const controller = new AbortController()
    const timer = window.setInterval(() => {
      void request<AnalysisImport>(importPath(selected.id), { signal: controller.signal }).then(next => { if (!controller.signal.aborted) { setSelected(next); setImports(items => items.map(item => item.id === next.id ? next : item)) } }).catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    }, 1500)
    return () => { window.clearInterval(timer); controller.abort() }
  }, [selected?.id, selected?.state])

  const act = async (work: (signal: AbortSignal) => Promise<void>) => {
    operation.current?.abort()
    const controller = new AbortController(); operation.current = controller
    setBusy(true); setError('')
    try { await work(controller.signal) } catch (e) { if (!controller.signal.aborted) setError(String(e)) }
    finally { if (operation.current === controller) { operation.current = undefined; setBusy(false) } }
  }

  const inspect = async (item: AnalysisImport, signal: AbortSignal) => {
    if (signal.aborted) return
    setSelected(item); setPoints([]); setPreview(undefined); setMapping(initialMapping); setAcceptRejected(false); setIdentityFilters(emptyAnalysisIdentityFilters())
    if (item.mapping) { setMapping(item.mapping); setMetric(item.mapping.measures[0]?.name ?? '') }
    const firstFile = Array.isArray(item.files) ? item.files[0] : undefined
    if (firstFile && typeof firstFile.id === 'string' && firstFile.id && Number.isSafeInteger(firstFile.uploaded) && firstFile.uploaded >= 0 && Number.isSafeInteger(firstFile.size) && firstFile.size >= 0 && firstFile.uploaded === firstFile.size && item.state !== 'cancelled' && item.state !== 'deleted') {
      const result = await request<AnalysisPreview>(`${importPath(item.id)}/files/${encodeURIComponent(firstFile.id)}/preview`, { signal })
      if (signal.aborted) return
      setPreview(result)
      if (!item.mapping) {
        const timestamp = result.schema.columns.find(c => /^(ts|timestamp|time|date)$/i.test(c.path))
        setMapping({ ...initialMapping, timestamp: timestamp?.path_components ?? [] })
      }
    }
  }

  const upload = () => act(async signal => {
    if (!projectId || !files.length || !capabilities) return
    let item = selected?.state === 'uploading' ? selected : await request<AnalysisImport>(`/analysis/imports?project_id=${encodeURIComponent(projectId)}`, {
      method: 'POST', signal, body: JSON.stringify({ name: name || files[0].name, files: files.map(f => ({ relative_path: f.webkitRelativePath || f.name, size: f.size })) }),
    })
    if (signal.aborted) return
    setSelected(item)
    for (const entry of item.files) {
      const file = files.find(f => (f.webkitRelativePath || f.name) === entry.relative_path && f.size === entry.size)
      if (!file) throw new Error(`Select the original file ${entry.relative_path} to resume.`)
      // Replay acknowledged chunks as well: the server verifies their hashes,
      // preventing a different same-size file from supplying a resumed suffix.
      for (let offset = 0; offset < file.size; offset += capabilities.chunk_bytes) {
        setProgress(`${entry.relative_path}: ${Math.round(100 * offset / file.size)}%`)
        await request(`${importPath(item.id)}/files/${entry.id}?offset=${offset}`, { method: 'PUT', signal, headers: { 'Content-Type': 'application/octet-stream' }, body: file.slice(offset, offset + capabilities.chunk_bytes) })
        if (signal.aborted) return
      }
    }
    item = await request<AnalysisImport>(importPath(item.id), { signal })
    if (signal.aborted) return
    setProgress('Upload complete'); await inspect(item, signal)
    if (signal.aborted) return
    await reload(signal)
  })

  const columns = preview?.schema.columns.filter(c => !c.repeated) ?? []
  const identityAvailable = analysisImportedIdentityAvailable(capabilities?.imported_identity_version)
  const mappingUnsupported = !identityAvailable && analysisMappingRequiresImportedIdentity(mapping)
  const selectedMappingDisclosureError = analysisMappingDisclosureError(selected?.mapping)
  const selectedIdentityUnsupported = !identityAvailable && analysisMappingRequiresImportedIdentity(selected?.mapping)
  const summaryStatistics = capabilities?.summary_statistics
  const histogramBoundLimit = capabilities?.histogram_bound_limit
  const summaryMeasureError = mapping.measures.map(m => analysisSummaryMeasureError(m, summaryStatistics) ?? analysisHistogramMeasureError(m, histogramBoundLimit)).find(Boolean)
  // A declared summary answers only its own statistics, over whole windows.
  // A histogram answers counts and interpolated percentiles.
  const summaryDeclarations = analysisSummaryDeclarations(selected?.mapping, metric)
  const histogramDeclaration = analysisHistogramDeclaration(selected?.mapping, metric)
  const metricAggregations = analysisMetricAggregations(selected?.mapping, metric)
  const aggregationOptions = metricAggregations.options
  const queryAggregation = aggregationOptions.includes(aggregation) ? aggregation : aggregationOptions[0]
  const summaryWindowSeconds = metricAggregations.windowSeconds?.(queryAggregation)
  if (!projectId) return <p>Select a project to analyze datasets.</p>
  return <section className="analysis-workspace" aria-label="Analysis workspace">
    <h2>Parquet analysis</h2>
    <p>Import metric observations, inspect their schema, and compare measurements on a UTC timeline.</p>
    {error && <p role="alert">{error}</p>}
    {!capabilities ? <p>Checking analysis availability…</p> : !capabilities.enabled || !capabilities.native_parquet ? <p>Native Parquet analysis is not enabled for this project.</p> : <>
      <div className="analysis-columns">
        <aside aria-label="Datasets">
          <h3>Datasets and imports</h3>
          <button type="button" disabled={busy} onClick={() => { setSelected(undefined); setPreview(undefined); setPoints([]); setMapping(initialMapping); setIdentityFilters(emptyAnalysisIdentityFilters()) }}>New import</button>
          <ul>{imports.map(item => <li key={item.id}><button type="button" disabled={busy} onClick={() => void act(signal => inspect(item, signal))}>{item.name} · {item.state}</button></li>)}</ul>
        </aside>
        <div>
          {canEdit && (!selected || selected.state === 'uploading') && <fieldset disabled={busy}>
            <legend>{selected ? 'Resume upload' : 'Upload Parquet files'}</legend>
            <label>Dataset name<input value={name} onChange={e => setName(e.target.value)} /></label>
            <label>Files<input type="file" multiple accept=".parquet" onChange={e => setFiles(Array.from(e.target.files ?? []))} /></label>
            <label>Directory<input type="file" multiple accept=".parquet" ref={input => input?.setAttribute('webkitdirectory', '')} onChange={e => setFiles(Array.from(e.target.files ?? []).filter(f => f.name.toLowerCase().endsWith('.parquet')))} /></label>
            <button type="button" disabled={!files.length} onClick={() => void upload()}>{selected ? 'Resume and verify upload' : 'Upload files'}</button>
          </fieldset>}
          {progress && <p role="status">{progress}</p>}
          {busy && <button type="button" onClick={() => { const current = operation.current; operation.current = undefined; current?.abort(); setBusy(false) }}>Pause operation</button>}
          {selected && <><h3>{selected.name}</h3><p>State: {selected.state} · Accepted: {selected.accepted} · Rejected: {selected.rejected}</p>{selected.error && <p role="alert">{selected.error}</p>}{selected.state === 'ready' && selected.rejection_artifacts?.map((_, i) => <p key={i}><button type="button" disabled={busy} onClick={() => void act(async signal => {
                const response = await apiFetch(`/analysis/datasets/${encodeURIComponent(selected.id)}/rejections/${i}`, { signal, timeoutMs: 120000 })
                if (signal.aborted) { await response.body?.cancel(); return }
                if (!response.ok) throw new Error('Rejection artifact download failed.')
                const size = Number(response.headers.get('Content-Length'))
                if (!size || size > 64 * 1024 * 1024) { await response.body?.cancel(); throw new Error('This rejection artifact exceeds the 64 MiB dashboard download limit. Retrieve it through the authenticated API.') }
                const blob = await response.blob()
                if (signal.aborted) return
                await downloadBlob(blob, `rejected-rows-${i + 1}.parquet`)
              })}>Download rejected row set {i + 1}</button></p>)}{selected.rejections?.map(r => <p key={`${r.file}-${r.measure}`}>File {selected.files[r.file]?.relative_path ?? r.file}, measure {r.measure + 1}: {r.count} rejected measurements. First row ordinals (zero based): {r.rows.join(', ')}.</p>)}</>}
          {preview && <><p>{preview.schema.rows.toLocaleString()} source rows. Text and nested sample values are redacted.</p><div className="analysis-table"><table><thead><tr>{preview.schema.columns.map(c => <th key={c.path}>{c.path}<small>{c.physical_type}</small></th>)}</tr></thead><tbody>{preview.samples.slice(0, 5).map((row, i) => <tr key={i}>{preview.schema.columns.map(c => <td key={c.path}>{String(row[c.path] ?? 'missing')}</td>)}</tr>)}</tbody></table></div></>}
          {canEdit && preview && selected && ['uploading', 'mapped', 'failed'].includes(selected.state) && <fieldset disabled={busy}>
            <legend>Map measurements</legend>
            <AnalysisSavedMappings projectId={projectId} mapping={mapping} canEdit={canEdit && !mappingUnsupported} onLoad={item => { updateMapping(item.definition); setMappingTemplate(item) }} />
            <label>Timestamp column<select value={JSON.stringify(mapping.timestamp)} onChange={e => setMapping({ ...mapping, timestamp: JSON.parse(e.target.value) })}><option value="[]">Choose a timestamp</option>{columns.map(c => <option key={c.path} disabled={analysisProtectedIdentityColumn(c.path_components)} value={JSON.stringify(c.path_components)}>{c.path}{analysisProtectedIdentityColumn(c.path_components) ? ' (protected)' : ''}</option>)}</select></label>
            <label>Timestamp format<select value={mapping.timestamp_encoding} onChange={e => setMapping({ ...mapping, timestamp_encoding: e.target.value })}>{['timestamp', 'timestamp_offset', 'unix_s', 'unix_ms', 'unix_us', 'unix_ns'].map(value => <option key={value}>{value}</option>)}</select></label>
            <label>Source timezone<input value={mapping.timezone} onChange={e => setMapping({ ...mapping, timezone: e.target.value })} placeholder="America/New_York" /></label>
            {identityAvailable ? <AnalysisIdentityMapping mapping={mapping} columns={columns} onChange={setMapping} /> : <p>Imported identity schema 2 is unavailable on this controller. Version 1 imports remain available.</p>}
            {analysisMappingDisclosureError(mapping) && <p role="alert">{analysisMappingDisclosureError(mapping)} The stored draft is retained for correction.</p>}
            {mappingUnsupported && <p role="alert">This identity mapping is retained, but cannot be committed by this controller. Connect to a controller advertising imported identity schema 2.</p>}
            <p>Select metric columns. Percentiles require raw observations; a summary column is a statistic the source already computed, so declare which statistic and the window it covers (the timestamp is the window start).</p>
            {columns.filter(c => JSON.stringify(c.path_components) !== JSON.stringify(mapping.timestamp)).map(c => {
              const measure = mapping.measures.find(m => JSON.stringify(m.value) === JSON.stringify(c.path_components))
              const update = (values: Partial<typeof mapping.measures[number]>) => setMapping({ ...mapping, measures: mapping.measures.map(m => m === measure ? { ...m, ...values } : m) })
              return <div className="analysis-measure" key={c.path}>
                <label><input type="checkbox" checked={!!measure} disabled={!measure && analysisProtectedIdentityColumn(c.path_components)} onChange={e => setMapping({ ...mapping, measures: e.target.checked ? [...mapping.measures, { value: c.path_components, name: c.path, kind: 'gauge', unit: 'count' }] : mapping.measures.filter(m => m !== measure) })} />{c.path}{analysisProtectedIdentityColumn(c.path_components) ? ' (protected)' : ''}</label>
                {measure && <><label>Metric name<input disabled={!!measure.name_column?.length} value={measure.name} onChange={e => update({ name: e.target.value })} /></label><label>Metric name column (long table)<select value={JSON.stringify(measure.name_column ?? [])} onChange={e => update({ name_column: JSON.parse(e.target.value), name: e.target.value === '[]' ? c.path : '' })}><option value="[]">Use fixed metric name</option>{columns.map(col => <option key={col.path} disabled={analysisProtectedIdentityColumn(col.path_components)} value={JSON.stringify(col.path_components)}>{col.path}{analysisProtectedIdentityColumn(col.path_components) ? ' (protected)' : ''}</option>)}</select></label><label>Semantics<select value={measure.kind} onChange={e => setMapping({ ...mapping, measures: mapping.measures.map(m => m === measure ? withAnalysisMeasureKind(m, e.target.value, { summaryStatistics, histogramBoundLimit }) : m) })}>{['gauge', 'counter', 'observation', 'summary', ...(histogramBoundLimit || measure.kind === 'histogram' ? ['histogram'] : [])].map(v => <option key={v}>{v}</option>)}</select></label><label>Unit<select value={measure.unit} onChange={e => update({ unit: e.target.value })}>{['ms', 's', 'us', 'ns', 'bytes', 'KiB', 'MiB', 'GiB', 'count', 'percent', 'ratio', 'cores'].map(v => <option key={v}>{v}</option>)}</select></label>{measure.kind === 'summary' && summaryStatistics?.length ? <AnalysisSummaryMeasureFields measure={measure} columns={columns} statistics={summaryStatistics} onChange={update} /> : null}{measure.kind === 'histogram' && measure.histogram && histogramBoundLimit ? <AnalysisHistogramMeasureFields measure={measure} columns={columns} onChange={update} /> : null}</>}
              </div>
            })}
            {summaryMeasureError && <p role="alert">{summaryMeasureError}</p>}
            <label><input type="checkbox" checked={acceptRejected} onChange={e => setAcceptRejected(e.target.checked)} />Accept rejected observations and retain the full rejected-row set in provenance</label>
            <button type="button" disabled={!mapping.timestamp.length || !mapping.measures.length || mappingUnsupported || !!analysisMappingDisclosureError(mapping) || !!summaryMeasureError} onClick={() => void act(async signal => {
              if (mappingUnsupported) throw new Error('Imported identity schema 2 is unavailable on this controller.')
              const identityError = analysisMappingDisclosureError(mapping)
              if (identityError) throw new Error(identityError)
              const saved = await request<AnalysisImport>(`${importPath(selected.id)}/mapping`, { method: 'PUT', signal, body: JSON.stringify({ revision: selected.revision, mapping, allow_rejected: acceptRejected, mapping_id: mappingTemplate?.id, mapping_revision: mappingTemplate?.revision }) })
              if (signal.aborted) return
              if (!identityAvailable && analysisMappingRequiresImportedIdentity(saved.mapping)) throw new Error('Imported identity schema 2 is unavailable on this controller.')
              const returnedIdentityError = analysisMappingDisclosureError(saved.mapping)
              if (returnedIdentityError) throw new Error(returnedIdentityError)
              setSelected(saved)
              const committed = await request<AnalysisImport>(`${importPath(selected.id)}/commit`, { method: 'POST', signal })
              if (signal.aborted) return
              setSelected(committed)
              await reload(signal)
            })}>Validate and import</button>
          </fieldset>}
          {selected?.state === 'ready' && <fieldset disabled={busy || selectedIdentityUnsupported || !!selectedMappingDisclosureError}>
            <legend>Query dataset</legend>
            {selectedMappingDisclosureError && <p role="alert">{selectedMappingDisclosureError} This stored mapping is retained; querying its mapped source values is unavailable.</p>}
            {selectedIdentityUnsupported && <p role="alert">This dataset requires imported identity schema 2. Querying is unavailable on this controller; its mapping and labels remain unchanged.</p>}
            <label>Metric<input list="analysis-metric-names" value={metric} onChange={e => setMetric(e.target.value)} /><datalist id="analysis-metric-names">{analysisMetricNames(selected.mapping).map(name => <option key={name} value={name} />)}</datalist></label>
            <label>From (UTC)<input type="datetime-local" step="1" value={from} onChange={e => setFrom(e.target.value)} /></label>
            <label>To (UTC)<input type="datetime-local" step="1" value={to} onChange={e => setTo(e.target.value)} /></label>
            <label>Bucket (seconds, optional)<input type="number" min={1} max={86400} step={1} placeholder="automatic" value={bucket} onChange={e => setBucket(e.target.value)} /></label>
            <label>{histogramDeclaration ? 'Histogram statistic' : summaryDeclarations.length ? 'Summary statistic' : 'Aggregation'}<select value={queryAggregation} onChange={e => setAggregation(e.target.value)}>{aggregationOptions.map(v => <option key={v}>{v}</option>)}</select></label>
            {selected.mapping?.version === 2 && <AnalysisIdentityFilters value={identityFilters} onChange={setIdentityFilters} label="Dataset identity filters" />}
            <button type="button" disabled={!metric || !from || !to || !!selectedMappingDisclosureError || selectedIdentityUnsupported || selected.mapping?.version === 2 && !!analysisIdentityFilterError(identityFilters)} onClick={() => void act(async signal => {
              if (selectedIdentityUnsupported) throw new Error('Imported identity schema 2 is unavailable on this controller.')
              if (selectedMappingDisclosureError) throw new Error(selectedMappingDisclosureError)
              const identitySelection = selected.mapping?.version === 2 ? buildAnalysisIdentitySelection(identityFilters) : {}
              // Buckets start on the metric's own source window grid, so a bucket never cuts a window in two.
              const range = analysisWindowAlignedRange(Date.parse(`${from}Z`), Date.parse(`${to}Z`), summaryWindowSeconds, analysisWindowAnchor(selected, metric, summaryWindowSeconds, identitySelection))
              // An explicit bucket (for example a whole multiple of a cumulative histogram's scrape
              // interval) still widens to whole source windows.
              const requestedBucket = bucket.trim() === '' ? undefined : Number(bucket)
              if (requestedBucket !== undefined && (!Number.isSafeInteger(requestedBucket) || requestedBucket < 1 || requestedBucket > 86400)) throw new Error('The bucket must be 1-86400 whole seconds.')
              const bucketSeconds = requestedBucket === undefined ? analysisBucketSeconds((range.toMs - range.fromMs) / 1000, summaryWindowSeconds) : summaryWindowSeconds ? Math.ceil(requestedBucket / summaryWindowSeconds) * summaryWindowSeconds : requestedBucket
              const result = await request<{ points: AnalysisPoint[] }>(`/analysis/datasets/${encodeURIComponent(selected.id)}/series`, { method: 'POST', signal, body: JSON.stringify({ ...identitySelection, metric, from: new Date(range.fromMs).toISOString(), to: new Date(range.toMs).toISOString(), bucket_seconds: bucketSeconds, aggregation: queryAggregation }) })
              if (!signal.aborted) { setPoints(result.points); setSeriesSelection({ dataset_id: selected.id, ...identitySelection, metric, from, to, aggregation: queryAggregation }) }
            })}>Show series</button>
          </fieldset>}
          {selected?.state === 'ready' && <AnalysisComparison key={selected.id} importedIdentityVersion={capabilities?.imported_identity_version} projectId={projectId} dataset={selected} datasets={imports} canEdit={canEdit} onAssociated={setSelected} />}
          {!selectedMappingDisclosureError && points.length > 0 && <div className="analysis-chart"><SeriesChartExport id="dataset-series" title={`${points[0].metric_name} dataset series`} rows={points.map(point => ({ ...point, value: point.quality === "valid" ? point.value : undefined }))} axes={[{ key: "utc", label: "UTC timestamp", value: point => point.timestamp_utc }, { key: "quality", label: "Quality", value: point => point.quality }, { key: "missing_reason", label: "Missing reason", value: point => point.missing_reason ? analysisMissingReasonLabels[point.missing_reason] ?? point.missing_reason : undefined }, { key: "estimate_bin", label: "Interpolated within bin", value: point => point.estimate_bin ? `${point.estimate_bin.lower}–${point.estimate_bin.upper}` : undefined }, { key: "kind", label: "Evidence kind", value: point => point.input_kind === 'summary' || point.input_kind === 'histogram' ? point.input_kind : point.kind }, { key: "source", label: "Source", value: point => point.source }, { key: "host", label: "Host", value: point => point.host }, { key: "service", label: "Service", value: point => point.service }, { key: "dimensions", label: "Dimensions", value: point => point.dimensions }]} series={[{ key: "value", label: points[0].metric_name, unit: points[0].unit }]} context={seriesSelection}><div role="img" aria-label={`${points[0].metric_name} over UTC time; ${points.length} buckets`}><ResponsiveContainer width="100%" height={260}><LineChart data={points.map(point => ({ ...point, value: point.quality === "valid" ? point.value : undefined }))} margin={{ top: 8, right: 60, bottom: 8, left: 8 }}><XAxis dataKey="timestamp_utc" tickFormatter={formatAnalysisTimeTick} tick={{ fontSize: 11 }} interval="preserveStartEnd" minTickGap={20} /><YAxis unit={points[0].unit ? ` ${points[0].unit}` : undefined} tick={{ fontSize: 11 }} /><Tooltip /><Line isAnimationActive={false} type="linear" dataKey="value" stroke="var(--chart-1)" connectNulls={false} dot={false} /></LineChart></ResponsiveContainer></div></SeriesChartExport></div>}
          {canEdit && selected && !['deleted', 'cancelled'].includes(selected.state) && <button type="button" disabled={busy} onClick={() => void act(async signal => { await request(importPath(selected.id), { method: 'DELETE', signal }); if (signal.aborted) return; setSelected(undefined); setPreview(undefined); setPoints([]); await reload(signal) })}>{selected.state === 'ready' ? 'Delete dataset' : 'Cancel import'}</button>}
        </div>
      </div>
    </>}
  </section>
}
