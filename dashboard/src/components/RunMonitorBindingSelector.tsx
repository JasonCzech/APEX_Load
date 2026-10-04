import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AnalysisCapabilities } from '../types/analysis'
import type { MonitorBinding, MonitorSource } from '../types/appdynamics'

export default function RunMonitorBindingSelector({ projectId, value, onChange }: { projectId?: string; value: MonitorBinding[] | undefined; onChange: (value: MonitorBinding[] | undefined) => void }) {
  const [sources, setSources] = useState<MonitorSource[]>([])
  const [enabled, setEnabled] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [retry, setRetry] = useState(0)
  const [custom, setCustom] = useState(Boolean(value?.length))
  useEffect(() => {
    const controller = new AbortController()
    setSources([]); setEnabled(false); setError('')
    if (projectId) {
      setLoading(true)
      void request<AnalysisCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal }).then(async capabilities => {
        if (controller.signal.aborted) return
        setEnabled(capabilities.monitoring_enabled)
        if (capabilities.monitoring_enabled) {
          const result = await request<MonitorSource[]>(`/projects/${encodeURIComponent(projectId)}/monitor-sources`, { signal: controller.signal })
          if (!controller.signal.aborted) setSources(result.filter(source => source.enabled))
        }
      }).catch(err => { if (!controller.signal.aborted) setError(String(err)) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    }
    return () => controller.abort()
  }, [projectId, retry])
  if (!enabled && value === undefined) return null
  const unavailable = value?.filter(binding => !sources.some(source => source.id === binding.source_id)) || []
  return <fieldset aria-label="Run monitor selection"><legend>Run monitoring</legend>
    <label>Monitor binding mode<select value={value === undefined ? 'inherit' : value.length || custom ? 'custom' : 'off'} onChange={event => { setCustom(event.target.value === 'custom'); onChange(event.target.value === 'inherit' ? undefined : []) }}><option value="inherit">Use scenario monitor bindings</option><option value="off">Off for this run</option><option value="custom" disabled={!sources.length}>Choose sources for this run</option></select></label>
    <p>Scenario bindings are inherited unless overridden. Selected source revisions and connections are frozen when preparing or starting the run. Required sources block Start if unavailable; collection errors during load do not stop the test.</p>
    {loading && <p role="status">Loading monitor sources…</p>}{error && <p role="alert">Unable to load monitoring sources. <button type="button" onClick={() => setRetry(number => number + 1)}>Retry monitor sources</button></p>}
    {value !== undefined && (value.length > 0 || custom) && <>{sources.map(source => { const binding = value.find(item => item.source_id === source.id); return <div key={source.id}><label><input type="checkbox" checked={Boolean(binding)} onChange={event => onChange(event.target.checked ? [...value, { source_id: source.id, required: false }] : value.filter(item => item.source_id !== source.id))} />{source.name} ({source.pack})</label>{binding && <label><input type="checkbox" checked={binding.required} onChange={event => onChange(value.map(item => item.source_id === source.id ? { ...item, required: event.target.checked } : item))} />Require {source.name} readiness and complete evidence</label>}</div>})}{!sources.length && !loading && <p>No enabled monitor sources are available.</p>}{unavailable.map(binding => <p role="alert" key={binding.source_id}>Selected source {binding.source_id} unavailable — review required. <button type="button" onClick={() => onChange(value.filter(item => item.source_id !== binding.source_id))}>Remove unavailable source</button></p>)}</>}
  </fieldset>
}
