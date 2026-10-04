import { useEffect, useState } from 'react'
import { useAppDynamicsConnectionsApi } from '../hooks/api/useAppDynamicsConnectionsApi'
import type { AppDynamicsEntity, AppDynamicsOperation, AppDynamicsSelection } from '../types/appdynamics'

const presets = {
  response_time: { label: 'Response time', matches: /response time/i, unit: 'ms', branch: 'Overall Application Performance' },
  calls: { label: 'Calls', matches: /calls per minute/i, unit: 'calls/min', branch: 'Overall Application Performance' },
  errors: { label: 'Errors', matches: /errors per minute/i, unit: 'errors/min', branch: 'Overall Application Performance' },
  slow_calls: { label: 'Slow calls', matches: /slow calls|slow transactions/i, unit: 'calls/min', branch: 'Overall Application Performance' },
  stalled_calls: { label: 'Stalled calls', matches: /stalled calls|stall count/i, unit: 'calls/min', branch: 'Overall Application Performance' },
  cpu: { label: 'CPU', matches: /cpu.*(?:usage|busy|used)|%busy/i, unit: '%', branch: 'Application Infrastructure Performance' },
  memory: { label: 'Memory', matches: /memory.*(?:used|usage)|used.*memory/i, unit: 'MB', branch: 'Application Infrastructure Performance' },
  heap: { label: 'Heap', matches: /heap.*used|used.*heap/i, unit: 'MB', branch: 'Application Infrastructure Performance' },
  gc: { label: 'Garbage collection', matches: /gc time spent|garbage collection.*time/i, unit: 'ms', branch: 'Application Infrastructure Performance' },
} as const

// A preset offers only paths returned by connection discovery. Availability and
// units vary by instrumentation; operators review the saved metric selection.
export default function AppDynamicsMetricPresets({ projectId, connectionId, selection, onChange }: { projectId: string; connectionId?: string; selection: AppDynamicsSelection; onChange: (selection: AppDynamicsSelection) => void }) {
  const api = useAppDynamicsConnectionsApi()
  const [preset, setPreset] = useState<keyof typeof presets>('response_time')
  const [path, setPath] = useState<string>(presets.response_time.branch)
  const [application, setApplication] = useState('')
  const [operation, setOperation] = useState<AppDynamicsOperation>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { setOperation(undefined); setError('') }, [projectId, connectionId])
  useEffect(() => {
    if (!operation || !connectionId || !['pending', 'queued', 'running'].includes(operation.status)) return
    const controller = new AbortController()
    const timer = setTimeout(() => { void api.getAppDynamicsOperation(projectId, connectionId, operation.id, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) setOperation(value) }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, connectionId, operation])
  const applicationId = application || selection.applications[0] || ''
  const browse = async (branch = path) => {
    if (!connectionId || !applicationId) return
    setBusy(true); setError(''); setPath(branch)
    try { setOperation(await api.discoverAppDynamicsEntities(projectId, connectionId, 'metrics', { application_id: applicationId, metric_path: branch })) }
    catch (err) { setError(String(err)) }
    finally { setBusy(false) }
  }
  const entities = operation?.result?.entities || []
  const folders = entities.filter(entity => entity.type?.toLowerCase() === 'folder')
  const metrics = entities.filter(entity => entity.path && entity.type?.toLowerCase() !== 'folder' && presets[preset].matches.test(`${entity.path}|${entity.name}`.replace(/\|/g, ' ')))
  const add = (entity: AppDynamicsEntity) => {
    if (!entity.path || selection.metrics.length >= 20) return
    let name: string = preset
    for (let suffix = 2; selection.metrics.some(metric => metric.name === name); suffix++) name = `${preset}_${suffix}`
    const unit = entity.name.includes('%') ? '%' : entity.name.includes('(KB)') ? 'KB' : entity.name.includes('(MB)') ? 'MB' : /count/i.test(entity.name) ? 'count' : presets[preset].unit
    onChange({ ...selection, metrics: [...selection.metrics, { application_id: applicationId, path: entity.path, name, unit, statistic: 'value' }] })
  }
  const pending = busy || Boolean(operation && ['pending', 'queued', 'running'].includes(operation.status))
  return <fieldset aria-label="AppDynamics metric presets"><legend>Available metric presets</legend>
    <label>Metric preset<select value={preset} onChange={event => { const id = event.target.value as typeof preset; setPreset(id); setPath(presets[id].branch); setOperation(undefined) }}>{Object.entries(presets).map(([id, item]) => <option key={id} value={id}>{item.label}</option>)}</select></label>
    <label>Preset application<select value={applicationId} onChange={event => { setApplication(event.target.value); setOperation(undefined) }}><option value="">Select an application</option>{selection.applications.map(id => <option key={id} value={id}>{id}</option>)}</select></label>
    <label>Preset metric branch<input value={path} onChange={event => setPath(event.target.value)} /></label>
    <button type="button" disabled={!connectionId || !applicationId || pending} onClick={() => void browse()}>{pending ? 'Discovering preset metrics…' : 'Discover available preset metrics'}</button>
    <p>Browse the metric hierarchy to find presets exposed by this application. Review units and source statistics before saving.</p>
    {error && <p role="alert">Preset discovery failed: {error}</p>}
    {operation?.error && <p role="alert">{operation.error}</p>}
    {operation?.result?.truncated && <p role="status">Preset results are truncated. Browse a narrower branch.</p>}
    {folders.map(entity => <button type="button" key={entity.path} disabled={pending} onClick={() => void browse(entity.path)}>Browse {entity.name}</button>)}
    {metrics.map(entity => <button type="button" key={entity.path} disabled={selection.metrics.length >= 20 || selection.metrics.some(metric => metric.application_id === applicationId && metric.path === entity.path)} onClick={() => add(entity)}>Add {entity.name}</button>)}
    {operation?.status === 'complete' && !metrics.length && <p>No matching preset metrics were found in this branch.{folders.length ? ' Browse a child branch.' : ''}</p>}
  </fieldset>
}
