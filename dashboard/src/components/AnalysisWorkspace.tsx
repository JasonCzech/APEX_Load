import { formatAnalysisTimeTick } from '../utils/analysisTime'
import { useCallback, useEffect, useRef, useState } from 'react'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { apiFetch, request } from '../hooks/apiClient'
import type { AnalysisCapabilities, AnalysisImport, AnalysisMapping, AnalysisPoint, AnalysisPreview } from '../types/analysis'
import './AnalysisWorkspace.css'
import { downloadBlob } from '../utils/fileUtils'
import AnalysisSavedMappings from './AnalysisSavedMappings'
import AnalysisComparison from './AnalysisComparison'

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
  const [metric, setMetric] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [aggregation, setAggregation] = useState('avg')
  const operation = useRef<AbortController>()

  const reload = useCallback(async (signal?: AbortSignal) => {
    if (!projectId) return
    const next = await request<AnalysisImport[]>(`/analysis/imports?project_id=${encodeURIComponent(projectId)}`, { signal }); if (!signal?.aborted) setImports(next)
  }, [projectId])

  useEffect(() => {
    const controller = new AbortController()
    setSelected(undefined); setPreview(undefined); setPoints([]); setImports([]); setFiles([]); setName(''); setProgress(''); setBusy(false); setAcceptRejected(false); updateMapping(initialMapping); setMappingTemplate(undefined); setCapabilities(undefined); setError('')
    if (projectId) request<AnalysisCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal })
      .then(async caps => { if (controller.signal.aborted) return; setCapabilities(caps); if (caps.enabled && caps.native_parquet) await reload(controller.signal) })
      .catch(e => { if (!controller.signal.aborted) setError(String(e)) })
    return () => { controller.abort(); operation.current?.abort() }
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
    finally { if (operation.current === controller) setBusy(false) }
  }

  const inspect = async (item: AnalysisImport, signal: AbortSignal) => {
    setSelected(item); setPoints([]); setPreview(undefined); setMapping(initialMapping); setAcceptRejected(false)
    if (item.mapping) { setMapping(item.mapping); setMetric(item.mapping.measures[0]?.name ?? '') }
    if (item.files[0]?.uploaded === item.files[0]?.size && item.state !== 'cancelled' && item.state !== 'deleted') {
      const result = await request<AnalysisPreview>(`${importPath(item.id)}/files/${item.files[0].id}/preview`, { signal })
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
    setSelected(item)
    for (const entry of item.files) {
      const file = files.find(f => (f.webkitRelativePath || f.name) === entry.relative_path && f.size === entry.size)
      if (!file) throw new Error(`Select the original file ${entry.relative_path} to resume.`)
      // Replay acknowledged chunks as well: the server verifies their hashes,
      // preventing a different same-size file from supplying a resumed suffix.
      for (let offset = 0; offset < file.size; offset += capabilities.chunk_bytes) {
        setProgress(`${entry.relative_path}: ${Math.round(100 * offset / file.size)}%`)
        await request(`${importPath(item.id)}/files/${entry.id}?offset=${offset}`, { method: 'PUT', signal, headers: { 'Content-Type': 'application/octet-stream' }, body: file.slice(offset, offset + capabilities.chunk_bytes) })
      }
    }
    item = await request<AnalysisImport>(importPath(item.id), { signal })
    setProgress('Upload complete'); await inspect(item, signal); await reload(signal)
  })

  const columns = preview?.schema.columns.filter(c => !c.repeated) ?? []
  if (!projectId) return <p>Select a project to analyze datasets.</p>
  return <section className="analysis-workspace" aria-label="Analysis workspace">
    <h2>Parquet analysis</h2>
    <p>Import metric observations, inspect their schema, and compare measurements on a UTC timeline.</p>
    {error && <p role="alert">{error}</p>}
    {!capabilities ? <p>Checking analysis availability…</p> : !capabilities.enabled || !capabilities.native_parquet ? <p>Native Parquet analysis is not enabled for this project.</p> : <>
      <div className="analysis-columns">
        <aside aria-label="Datasets">
          <h3>Datasets and imports</h3>
          <button type="button" disabled={busy} onClick={() => { setSelected(undefined); setPreview(undefined); setPoints([]); setMapping(initialMapping) }}>New import</button>
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
          {busy && <button type="button" onClick={() => operation.current?.abort()}>Pause operation</button>}
          {selected && <><h3>{selected.name}</h3><p>State: {selected.state} · Accepted: {selected.accepted} · Rejected: {selected.rejected}</p>{selected.error && <p role="alert">{selected.error}</p>}{selected.state === 'ready' && selected.rejection_artifacts?.map((_, i) => <p key={i}><button type="button" disabled={busy} onClick={() => void act(async signal => {
                const response = await apiFetch(`/analysis/datasets/${encodeURIComponent(selected.id)}/rejections/${i}`, { signal, timeoutMs: 120000 })
                if (!response.ok) throw new Error('Rejection artifact download failed.')
                const size = Number(response.headers.get('Content-Length'))
                if (!size || size > 64 * 1024 * 1024) { await response.body?.cancel(); throw new Error('This rejection artifact exceeds the 64 MiB dashboard download limit. Retrieve it through the authenticated API.') }
                await downloadBlob(await response.blob(), `rejected-rows-${i + 1}.parquet`)
              })}>Download rejected row set {i + 1}</button></p>)}{selected.rejections?.map(r => <p key={`${r.file}-${r.measure}`}>File {selected.files[r.file]?.relative_path ?? r.file}, measure {r.measure + 1}: {r.count} rejected measurements. First row ordinals (zero based): {r.rows.join(', ')}.</p>)}</>}
          {preview && <><p>{preview.schema.rows.toLocaleString()} source rows. Text and nested sample values are redacted.</p><div className="analysis-table"><table><thead><tr>{preview.schema.columns.map(c => <th key={c.path}>{c.path}<small>{c.physical_type}</small></th>)}</tr></thead><tbody>{preview.samples.slice(0, 5).map((row, i) => <tr key={i}>{preview.schema.columns.map(c => <td key={c.path}>{String(row[c.path] ?? 'missing')}</td>)}</tr>)}</tbody></table></div></>}
          {canEdit && preview && selected && ['uploading', 'mapped', 'failed'].includes(selected.state) && <fieldset disabled={busy}>
            <legend>Map measurements</legend>
            <AnalysisSavedMappings projectId={projectId} mapping={mapping} canEdit={canEdit} onLoad={item => { updateMapping(item.definition); setMappingTemplate(item) }} />
            <label>Timestamp column<select value={JSON.stringify(mapping.timestamp)} onChange={e => setMapping({ ...mapping, timestamp: JSON.parse(e.target.value) })}><option value="[]">Choose a timestamp</option>{columns.map(c => <option key={c.path} value={JSON.stringify(c.path_components)}>{c.path}</option>)}</select></label>
            <label>Timestamp format<select value={mapping.timestamp_encoding} onChange={e => setMapping({ ...mapping, timestamp_encoding: e.target.value })}>{['timestamp', 'timestamp_offset', 'unix_s', 'unix_ms', 'unix_us', 'unix_ns'].map(value => <option key={value}>{value}</option>)}</select></label>
            <label>Source timezone<input value={mapping.timezone} onChange={e => setMapping({ ...mapping, timezone: e.target.value })} placeholder="America/New_York" /></label>
            <p>Select metric columns. Percentiles require raw observations.</p>
            {columns.filter(c => JSON.stringify(c.path_components) !== JSON.stringify(mapping.timestamp)).map(c => {
              const measure = mapping.measures.find(m => JSON.stringify(m.value) === JSON.stringify(c.path_components))
              const update = (values: Partial<typeof mapping.measures[number]>) => setMapping({ ...mapping, measures: mapping.measures.map(m => m === measure ? { ...m, ...values } : m) })
              return <div className="analysis-measure" key={c.path}>
                <label><input type="checkbox" checked={!!measure} onChange={e => setMapping({ ...mapping, measures: e.target.checked ? [...mapping.measures, { value: c.path_components, name: c.path, kind: 'gauge', unit: 'count' }] : mapping.measures.filter(m => m !== measure) })} />{c.path}</label>
                {measure && <><label>Metric name<input disabled={!!measure.name_column?.length} value={measure.name} onChange={e => update({ name: e.target.value })} /></label><label>Metric name column (long table)<select value={JSON.stringify(measure.name_column ?? [])} onChange={e => update({ name_column: JSON.parse(e.target.value), name: e.target.value === '[]' ? c.path : '' })}><option value="[]">Use fixed metric name</option>{columns.map(col => <option key={col.path} value={JSON.stringify(col.path_components)}>{col.path}</option>)}</select></label><label>Semantics<select value={measure.kind} onChange={e => update({ kind: e.target.value })}>{['gauge', 'counter', 'observation', 'summary'].map(v => <option key={v}>{v}</option>)}</select></label><label>Unit<select value={measure.unit} onChange={e => update({ unit: e.target.value })}>{['ms', 's', 'us', 'ns', 'bytes', 'KiB', 'MiB', 'GiB', 'count', 'percent', 'ratio', 'cores'].map(v => <option key={v}>{v}</option>)}</select></label></>}
              </div>
            })}
            <label><input type="checkbox" checked={acceptRejected} onChange={e => setAcceptRejected(e.target.checked)} />Accept rejected observations and retain the full rejected-row set in provenance</label>
            <button type="button" disabled={!mapping.timestamp.length || !mapping.measures.length} onClick={() => void act(async signal => {
              const saved = await request<AnalysisImport>(`${importPath(selected.id)}/mapping`, { method: 'PUT', signal, body: JSON.stringify({ revision: selected.revision, mapping, allow_rejected: acceptRejected, mapping_id: mappingTemplate?.id, mapping_revision: mappingTemplate?.revision }) })
              setSelected(saved)
              setSelected(await request<AnalysisImport>(`${importPath(selected.id)}/commit`, { method: 'POST', signal }))
              await reload(signal)
            })}>Validate and import</button>
          </fieldset>}
          {selected?.state === 'ready' && <fieldset disabled={busy}>
            <legend>Query dataset</legend>
            <label>Metric<input list="analysis-metric-names" value={metric} onChange={e => setMetric(e.target.value)} /><datalist id="analysis-metric-names">{selected.mapping?.measures.filter(m => m.name).map(m => <option key={m.name} value={m.name} />)}</datalist></label>
            <label>From (UTC)<input type="datetime-local" value={from} onChange={e => setFrom(e.target.value)} /></label>
            <label>To (UTC)<input type="datetime-local" value={to} onChange={e => setTo(e.target.value)} /></label>
            <label>Aggregation<select value={aggregation} onChange={e => setAggregation(e.target.value)}>{['avg', 'min', 'max', 'sum', 'count', 'p50', 'p95', 'p99', 'rate'].map(v => <option key={v}>{v}</option>)}</select></label>
            <button type="button" disabled={!metric || !from || !to} onClick={() => void act(async signal => {
              const result = await request<{ points: AnalysisPoint[] }>(`/analysis/datasets/${selected.id}/series`, { method: 'POST', signal, body: JSON.stringify({ metric, from: new Date(`${from}Z`).toISOString(), to: new Date(`${to}Z`).toISOString(), bucket_seconds: Math.max(1, Math.ceil((Date.parse(`${to}Z`) - Date.parse(`${from}Z`)) / 1000 / 500)), aggregation }) })
              setPoints(result.points)
            })}>Show series</button>
          </fieldset>}
          {selected?.state === 'ready' && <AnalysisComparison key={selected.id} projectId={projectId} dataset={selected} datasets={imports} canEdit={canEdit} onAssociated={setSelected} />}
          {points.length > 0 && <div className="analysis-chart" role="img" aria-label={`${points[0].metric_name} over UTC time; ${points.length} buckets`}><ResponsiveContainer width="100%" height={260}><LineChart data={points} margin={{ top: 8, right: 60, bottom: 8, left: 8 }}><XAxis dataKey="timestamp_utc" tickFormatter={formatAnalysisTimeTick} tick={{ fontSize: 11 }} interval="preserveStartEnd" minTickGap={20} /><YAxis unit={points[0].unit ? ` ${points[0].unit}` : undefined} tick={{ fontSize: 11 }} /><Tooltip /><Line isAnimationActive={false} type="linear" dataKey="value" stroke="var(--chart-1)" connectNulls={false} dot={false} /></LineChart></ResponsiveContainer></div>}
          {canEdit && selected && !['deleted', 'cancelled'].includes(selected.state) && <button type="button" disabled={busy} onClick={() => void act(async signal => { await request(importPath(selected.id), { method: 'DELETE', signal }); setSelected(undefined); setPreview(undefined); setPoints([]); await reload(signal) })}>{selected.state === 'ready' ? 'Delete dataset' : 'Cancel import'}</button>}
        </div>
      </div>
    </>}
  </section>
}
