import { useEffect, useRef, useState } from 'react'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import DependencyEvidence from './DependencyEvidence'
import { request } from '../hooks/apiClient'
import type { AnalysisCapabilities, AnalysisPoint } from '../types/analysis'
import type { MetricsHistoryPoint, MonitoringCollectorListResponse } from '../types'

interface Source { credential_ref?: string; collector_public_key?: string; service?: string; id: string; name: string; project_id?: string; revision: number; collector_id: string; pack: string; destination?: string; interval_seconds: number; enabled: boolean }
interface Bindings { revision: number; sources: { source_id: string; required: boolean }[] }
interface Job { id: string; source: Source; state: string; next_sequence: number; coverage_reason?: string }
const sourcesPath = (project: string) => `/projects/${encodeURIComponent(project)}/monitor-sources`

function useMonitoringEnabled(projectId?: string) {
  const [enabled, setEnabled] = useState(false)
  useEffect(() => { const c = new AbortController(); setEnabled(false); if (projectId) void request<AnalysisCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: c.signal }).then(caps => { if (!c.signal.aborted) setEnabled(caps.monitoring_enabled) }).catch(() => {}); return () => c.abort() }, [projectId])
  return enabled
}

export function RunMonitorSources({ projectId, canManage = false }: { projectId?: string; canManage?: boolean }) {
  const enabled = useMonitoringEnabled(projectId)
  const [sources, setSources] = useState<Source[]>([])
  const [collectors, setCollectors] = useState<MonitoringCollectorListResponse['collectors']>([])
  const [draft, setDraft] = useState<Source>({ id: '', name: '', revision: 0, collector_id: '', pack: 'linux-hosts', interval_seconds: 15, enabled: true })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const operation = useRef<AbortController>()
  useEffect(() => {
    const c = new AbortController(); setSources([])
    if (enabled && projectId) void Promise.all([request<Source[]>(sourcesPath(projectId), { signal: c.signal }), request<MonitoringCollectorListResponse>(`/monitoring/collectors?project_id=${encodeURIComponent(projectId)}`, { signal: c.signal })]).then(([s, collectors]) => { if (!c.signal.aborted) { setSources(s); setCollectors(collectors.collectors) } }).catch(e => { if (!c.signal.aborted) setError(String(e)) })
    return () => { c.abort(); operation.current?.abort() }
  }, [enabled, projectId, refresh])
  if (!enabled || !projectId) return <p>Run-bound monitoring is not enabled for this project.</p>
  const save = async (remove = false) => {
    const c = new AbortController(); operation.current = c; setBusy(true); setError('')
    try {
      const path = `${sourcesPath(projectId)}${draft.id ? `/${encodeURIComponent(draft.id)}` : ''}${remove ? `?revision=${draft.revision}` : ''}`
      await request(path, { method: remove ? 'DELETE' : draft.id ? 'PUT' : 'POST', signal: c.signal, ...(remove ? {} : { body: JSON.stringify({ ...draft, id: draft.id || undefined }) }) })
      if (!c.signal.aborted) { setDraft({ ...draft, id: '', revision: 0, name: '' }); setRefresh(x => x + 1) }
    } catch (e) { if (!c.signal.aborted) setError(String(e)) } finally { if (!c.signal.aborted) setBusy(false) }
  }
  return <section className="analysis-workspace" aria-label="Run monitor sources">
    <p>Sources are captured when a run starts. Edits apply to subsequent runs. Collection continues through pauses.</p>
    {error && <p role="alert">{error}</p>}
    <ul>{sources.map(source => <li key={source.id}>{source.name} · {source.pack} · revision {source.revision} · {source.enabled ? 'enabled' : 'disabled'} {canManage && <button type="button" disabled={busy} onClick={() => setDraft(source)}>Edit {source.name}</button>}</li>)}</ul>
    {canManage && <fieldset disabled={busy}>
      <legend>{draft.id ? 'Edit source' : 'New source'}</legend>
      <label>Name<input value={draft.name} maxLength={256} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
      <label>Collector<select value={draft.collector_id} onChange={e => setDraft({ ...draft, collector_id: e.target.value })}><option value="">Select a collector</option>{collectors.map(c => <option key={c.id} value={c.id}>{c.name} · {c.health}</option>)}</select></label>
      <label>Pack<select value={draft.pack} onChange={e => setDraft({ ...draft, pack: e.target.value, interval_seconds: ['cloudwatch', 'azure-monitor'].includes(e.target.value) ? Math.max(60, draft.interval_seconds) : draft.interval_seconds, destination: '', credential_ref: '', collector_public_key: '' })}>{['linux-hosts', 'go-runtime', 'prometheus', 'ssh-linux', 'otel-metrics', 'jmx', 'jdbc', 'cloudwatch', 'azure-monitor'].map(p => <option key={p}>{p}</option>)}</select></label>
      {!['linux-hosts', 'go-runtime'].includes(draft.pack) && <label>Approved destination<input type="url" value={draft.destination ?? ''} onChange={e => setDraft({ ...draft, destination: e.target.value })} /></label>}
      <label>Dependency identity<input value={draft.service ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, service: e.target.value })} /><small>Exact dependency label used to link collected evidence.</small></label>
      {['ssh-linux', 'jmx', 'jdbc'].includes(draft.pack) && <>
        <label>Project credential secret name<input value={draft.credential_ref ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, credential_ref: e.target.value })} /></label>
        <label>Collector X25519 public key<input value={draft.collector_public_key ?? ''} onChange={e => setDraft({ ...draft, collector_public_key: e.target.value })} /><small>Enroll the public key from the approved collector. Jobs seal credentials to this key.</small></label>
      </>}
      <label>Collection interval (seconds)<input type="number" min={1} max={3600} value={draft.interval_seconds} onChange={e => setDraft({ ...draft, interval_seconds: Number(e.target.value) })} /></label>
      <label><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />Enabled</label>
      <button type="button" disabled={!draft.name || !draft.collector_id} onClick={() => void save()}>Save monitor source</button>
      {draft.id && <><button type="button" onClick={() => void save(true)}>Delete source</button><button type="button" onClick={() => setDraft({ ...draft, id: '', name: '', revision: 0 })}>New source</button></>}
    </fieldset>}
  </section>
}

export function ScenarioMonitorBindings({ projectId, scenarioId, canEdit }: { projectId: string; scenarioId: string; canEdit: boolean }) {
  const enabled = useMonitoringEnabled(projectId)
  const [sources, setSources] = useState<Source[]>([])
  const [bindings, setBindings] = useState<Bindings>({ revision: 0, sources: [] })
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const operation = useRef<AbortController>()
  const path = `/scenarios/${encodeURIComponent(scenarioId)}/monitor-bindings`
  useEffect(() => {
    const c = new AbortController()
    if (enabled) void Promise.all([request<Source[]>(sourcesPath(projectId), { signal: c.signal }), request<Bindings>(path, { signal: c.signal })]).then(([s, b]) => { if (!c.signal.aborted) { setSources(s); setBindings(b) } }).catch(e => { if (!c.signal.aborted) setMessage(String(e)) })
    return () => { c.abort(); operation.current?.abort() }
  }, [enabled, projectId, path])
  if (!enabled) return null
  return <fieldset disabled={busy || !canEdit} aria-label="Scenario monitor bindings">
    <legend>Run-bound monitors</legend><p>Save bindings separately. Required sources block Start when their collectors are unavailable.</p>
    {message && <p role="status">{message}</p>}
    {sources.map(source => { const binding = bindings.sources.find(b => b.source_id === source.id); return <div key={source.id}>
      <label><input type="checkbox" checked={!!binding} onChange={e => setBindings({ ...bindings, sources: e.target.checked ? [...bindings.sources, { source_id: source.id, required: false }] : bindings.sources.filter(b => b.source_id !== source.id) })} />{source.name} ({source.pack})</label>
      {binding && <label><input type="checkbox" checked={binding.required} onChange={e => setBindings({ ...bindings, sources: bindings.sources.map(b => b.source_id === source.id ? { ...b, required: e.target.checked } : b) })} />Require {source.name} readiness</label>}
    </div> })}
    {canEdit && <button type="button" onClick={() => { const c = new AbortController(); operation.current = c; setBusy(true); setMessage(''); void request<Bindings>(path, { method: 'PUT', signal: c.signal, body: JSON.stringify(bindings) }).then(b => { if (!c.signal.aborted) { setBindings(b); setMessage('Monitor bindings saved.') } }).catch(e => { if (!c.signal.aborted) setMessage(String(e)) }).finally(() => { if (!c.signal.aborted) setBusy(false) }) }}>Save monitor bindings</button>}
  </fieldset>
}

export function RunMonitorTimeline({ projectId, testId, history }: { projectId?: string; testId: string; history: MetricsHistoryPoint[] }) {
  const enabled = useMonitoringEnabled(projectId)
  const [jobs, setJobs] = useState<Job[]>([])
  const [source, setSource] = useState('')
  const [metric, setMetric] = useState('')
  const [dimensions, setDimensions] = useState('{}')
  const [window, setWindow] = useState<{ from: string; to: string }>()
  const [points, setPoints] = useState<(AnalysisPoint & { relative_time_ms?: number })[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const controller = useRef<AbortController>()
  useEffect(() => {
    const c = new AbortController()
    setJobs([]); setPoints([]); setError(''); setBusy(false); setSource(''); setWindow(undefined)
    if (enabled) void request<Job[]>(`/tests/${encodeURIComponent(testId)}/monitors`, { signal: c.signal }).then(j => { if (!c.signal.aborted) setJobs(j) }).catch(e => { if (!c.signal.aborted) setError(String(e)) })
    return () => { c.abort(); controller.current?.abort() }
  }, [testId, enabled])
  if (!enabled || !jobs.length) return null
  const load = async () => {
    controller.current?.abort(); const c = new AbortController(); controller.current = c; setBusy(true); setError('')
    try {
      const filters = JSON.parse(dimensions) as Record<string, string>
      if (!filters || Array.isArray(filters) || typeof filters !== 'object' || Object.values(filters).some(v => typeof v !== 'string')) throw new Error('Dimensions must be a JSON object of string values.')
      const collected: typeof points = []; let afterJob = ''; let afterSequence = -1
      for (let pageNumber = 0; ; pageNumber++) {
        if (pageNumber >= 100) throw new Error('Monitoring evidence exceeds the scan limit; narrow the requested selection.')
        const query = new URLSearchParams({ metric, source_id: source, dimensions: JSON.stringify(filters), after_job: afterJob, after_sequence: String(afterSequence) })
        if (window) { query.set('from', window.from); query.set('to', window.to) }
        const page = await request<{ points: typeof points; has_more: boolean; after_job: string; after_sequence: number }>(`/tests/${encodeURIComponent(testId)}/monitor-series?${query}`, { signal: c.signal })
        collected.push(...page.points); if (collected.length > 10000) throw new Error('Series exceeds 10,000 points. Use a bounded comparison window in Analysis.')
        if (!page.has_more) break
        afterJob = page.after_job; afterSequence = page.after_sequence
      }
      const identities = new Set(collected.map(p => JSON.stringify([p.unit, p.kind, p.host, Object.entries(p.dimensions ?? {}).sort(([a], [b]) => a.localeCompare(b))])))
      if (identities.size > 1) throw new Error('Select dimensions for a single monitor series before plotting.')
      if (!c.signal.aborted) setPoints(collected)
    } catch (e) { if (!c.signal.aborted) setError(String(e)) } finally { if (!c.signal.aborted) setBusy(false) }
  }
  const rows: { time: number; monitor?: number; latency?: number }[] = []
  for (const point of points) rows.push({ time: Date.parse(point.timestamp_utc), monitor: point.quality === 'valid' ? point.value : undefined })
  for (const point of history) if (point.timestampMs !== undefined && (!window || (point.timestampMs >= Date.parse(window.from) && point.timestampMs <= Date.parse(window.to)))) rows.push({ time: point.timestampMs, latency: point.avgMs })
  rows.sort((a, b) => a.time - b.time)
  return <section className="tx-section" aria-label="Run monitor evidence">
    <DependencyEvidence testId={testId} projectId={projectId} select={(source, metric, from, to) => { controller.current?.abort(); setBusy(false); setSource(source); setMetric(metric); setDimensions('{}'); setWindow({ from, to }); setPoints([]) }} />
    <h3>Run monitor evidence</h3>{window && <p>Selected interval: {window.from} – {window.to} <button type="button" onClick={() => { setWindow(undefined); setPoints([]) }}>Clear time window</button></p>}<p>Aligned by elapsed wall time, including pauses. Associations do not establish causation.</p>
    <ul>{jobs.map(j => <li key={j.id}>{j.source.name} · {j.state} · {j.next_sequence} accepted batches{j.coverage_reason ? ` · ${j.coverage_reason}` : ''}</li>)}</ul>
    {error && <p role="alert">{error}</p>}
    <label>Monitor source<select value={source} onChange={e => { setSource(e.target.value); setPoints([]) }}><option value="">Select a source</option>{jobs.map(j => <option key={j.id} value={j.source.id}>{j.source.name}</option>)}</select></label>
    <label>Metric name<input value={metric} onChange={e => setMetric(e.target.value)} /></label>
    <label>Dimensions (JSON)<input value={dimensions} onChange={e => { setDimensions(e.target.value); setPoints([]) }} /></label>
    <button type="button" disabled={busy || !source || !metric} onClick={() => void load()}>{busy ? 'Loading…' : 'Refresh monitor overlay'}</button>
    {points.length > 0 && <><p>{points.filter(p => p.quality === 'valid').length} valid / {points.length} observed samples. Missing or stale evidence is excluded.</p><ResponsiveContainer width="100%" height={260}><LineChart data={rows}><XAxis dataKey="time" type="number" domain={['dataMin', 'dataMax']} tickFormatter={x => new Date(x).toLocaleTimeString()} /><YAxis yAxisId="monitor" /><YAxis yAxisId="latency" orientation="right" unit=" ms" /><Tooltip labelFormatter={x => new Date(Number(x)).toISOString()} /><Line isAnimationActive={false} data={rows.filter(r => 'monitor' in r)} yAxisId="monitor" dataKey="monitor" stroke="var(--chart-1)" dot={false} connectNulls={false} /><Line isAnimationActive={false} data={rows.filter(r => 'latency' in r)} yAxisId="latency" dataKey="latency" stroke="var(--chart-2)" dot={false} connectNulls={false} /></LineChart></ResponsiveContainer></>}
  </section>
}
