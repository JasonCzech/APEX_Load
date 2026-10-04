import { useEffect, useRef, useState } from 'react'
import { usePrometheusConnectionsApi } from '../hooks/api/usePrometheusConnectionsApi'
import type { PrometheusOperation, PrometheusQuery } from '../types/prometheus'
import { buildPrometheusPreset, prometheusPresetAvailable, prometheusPresets } from './prometheusPresets'

export default function PrometheusQueryPresets({ projectId, connectionID, queries, onChange }: { projectId: string; connectionID: string; queries: PrometheusQuery[]; onChange: (queries: PrometheusQuery[]) => void }) {
  const api = usePrometheusConnectionsApi()
  const [operation, setOperation] = useState<PrometheusOperation>()
  const [job, setJob] = useState('')
  const [namespace, setNamespace] = useState('')
  const [presetID, setPresetID] = useState(prometheusPresets[0].id)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const active = useRef<AbortController>()
  useEffect(() => () => active.current?.abort(), [])
  useEffect(() => {
    if (!operation || !['pending', 'queued', 'running'].includes(operation.status)) return
    const controller = new AbortController()
    const timer = setTimeout(() => { void api.getPrometheusOperation(projectId, connectionID, operation.id, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setOperation(result) }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, connectionID, operation, projectId])
  const metrics = (operation?.result?.entities || []).map(item => item.name || item.id || '').filter(Boolean)
  const selected = prometheusPresets.find(item => item.id === presetID)!
  const draft = buildPrometheusPreset(selected, { job, namespace })
  const available = Boolean(operation?.status === 'complete' && prometheusPresetAvailable(selected, metrics))
  return <fieldset aria-label="Prometheus query presets"><legend>Discover target query presets</legend>
    <p>Discover available metric names at the saved collection location. Presets create editable queries scoped to your application; review the expression, labels, unit and freshness before enabling the source.</p>
    {error && <p role="alert">{error}</p>}
    <button type="button" disabled={busy} onClick={async () => {
      const controller = new AbortController(); active.current?.abort(); active.current = controller
      setBusy(true); setError(''); setOperation(undefined)
      try { const result = await api.inspectPrometheusConnection(projectId, connectionID, 'discover', { kind: 'metrics' }, { signal: controller.signal }); if (!controller.signal.aborted) setOperation(result) }
      catch (err) { if (!controller.signal.aborted) setError(String(err)) } finally { if (!controller.signal.aborted) setBusy(false) }
    }}>Discover preset metrics</button>
    {operation && <p role="status">Preset discovery: {operation.status}{operation.message ? ` · ${operation.message}` : ''}{operation.result?.truncated ? ' · metric catalogue truncated; undiscovered presets remain unavailable' : ''}</p>}
    <label>Preset target job<input value={job} maxLength={128} placeholder="checkout" onChange={event => setJob(event.target.value)} /></label>
    <label>Preset target namespace<input value={namespace} maxLength={128} placeholder="Required for Kubernetes presets" onChange={event => setNamespace(event.target.value)} /></label>
    <label>Target metric preset<select value={presetID} onChange={event => setPresetID(event.target.value)}>{prometheusPresets.map(preset => <option key={preset.id} value={preset.id} disabled={!prometheusPresetAvailable(preset, metrics)}>{preset.name}{prometheusPresetAvailable(preset, metrics) ? '' : ' · not discovered'}</option>)}</select></label>
    <p>{selected.help}</p>
    {draft && available && <p>Proposed expression: <code>{draft.expression}</code></p>}
    <button type="button" disabled={!available || !draft || queries.length >= 20 || queries.some(query => query.id === selected.id || query.name === selected.id)} onClick={() => { if (draft) onChange([...queries, draft]) }}>Add discovered preset query</button>
  </fieldset>
}
