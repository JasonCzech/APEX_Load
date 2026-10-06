import { useCallback, useEffect, useRef, useState } from 'react'
import MonitorTimelinePlot from './MonitorTimelinePlot'
import { monitorSeriesIdentity, providerBucketRange, monitorEntityLabel } from './monitorTimelineModel'
import DependencyEvidence from './DependencyEvidence'
import AppDynamicsRunEvidence from './AppDynamicsRunEvidence'
import AppDynamicsSourceFields from './AppDynamicsSourceFields'
import ElasticSourceFields from './ElasticSourceFields'
import ElasticRunEvidence from './ElasticRunEvidence'
import { elasticSourceReady, emptyElasticSelection, monitorOverlayMetrics } from './elasticMonitoringModel'
import { emptyAppDynamicsSelection } from './appDynamicsConnectionModel'
import type { MonitorSource as Source, RunMonitorJob as Job } from '../types/runMonitoring'
import { request } from '../hooks/apiClient'
import type { AnalysisCapabilities, AnalysisPoint } from '../types/analysis'
import type { MetricsHistoryPoint, MonitoringCollectorListResponse } from '../types'

interface Bindings { revision: number; sources: { source_id: string; required: boolean }[] }
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
  const [sourceValidation, setSourceValidation] = useState('')
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
      <label>Collector<select disabled={['appdynamics', 'elastic'].includes(draft.pack)} value={draft.collector_id} onChange={e => setDraft({ ...draft, collector_id: e.target.value })}><option value="">Select a collector</option>{collectors.map(c => <option key={c.id} value={c.id}>{c.name} · {c.health}</option>)}</select></label>
      <label>Pack<select value={draft.pack} onChange={e => setDraft({ ...draft, pack: e.target.value, destination: '', credential_ref: '', collector_public_key: '', connection_id: undefined, executor_mode: ['appdynamics', 'elastic'].includes(e.target.value) ? 'direct' : undefined, appdynamics: e.target.value === 'appdynamics' ? emptyAppDynamicsSelection() : undefined, elastic: e.target.value === 'elastic' ? emptyElasticSelection(projectId) : undefined, interval_seconds: e.target.value === 'appdynamics' ? 60 : e.target.value === 'elastic' ? 10 : 15 })}>{['linux-hosts', 'go-runtime', 'prometheus', 'ssh-linux', 'otel-metrics', 'jmx', 'jdbc', 'appdynamics', 'elastic'].map(p => <option key={p}>{p}</option>)}</select></label>
      {!['linux-hosts', 'go-runtime', 'appdynamics', 'elastic'].includes(draft.pack) && <label>Approved destination<input type="url" value={draft.destination ?? ''} onChange={e => setDraft({ ...draft, destination: e.target.value })} /></label>}
      <label>Dependency identity<input value={draft.service ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, service: e.target.value })} /><small>Exact dependency label used to link collected evidence.</small></label>
      {['ssh-linux', 'jmx', 'jdbc'].includes(draft.pack) && <>
        <label>Project credential secret name<input value={draft.credential_ref ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, credential_ref: e.target.value })} /></label>
        <label>Collector X25519 public key<input value={draft.collector_public_key ?? ''} onChange={e => setDraft({ ...draft, collector_public_key: e.target.value })} /><small>Enroll the public key from the approved collector. Jobs seal credentials to this key.</small></label>
      </>}
      {draft.pack === 'elastic' && <ElasticSourceFields key={draft.id || 'new'} projectId={projectId} source={draft} onChange={setDraft} onValidationChange={setSourceValidation} />}
      {draft.pack === 'appdynamics' && <AppDynamicsSourceFields projectId={projectId} source={draft} onChange={setDraft} />}
      <label>Collection interval (seconds)<input type="number" min={1} max={3600} value={draft.interval_seconds} onChange={e => setDraft({ ...draft, interval_seconds: Number(e.target.value) })} /></label>
      <label><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />Enabled</label>
      <button type="button" disabled={!draft.name || (draft.pack === 'elastic' && Boolean(sourceValidation)) || (draft.pack === 'elastic' ? !elasticSourceReady(draft) : draft.pack === 'appdynamics' ? !draft.connection_id || !draft.appdynamics?.applications.length || (!draft.appdynamics.metrics.length && !draft.appdynamics.analytics.length && !draft.appdynamics.events && !draft.appdynamics.violations && !draft.appdynamics.snapshots) || (draft.executor_mode === 'collector' && !draft.collector_id) : !draft.collector_id)} onClick={() => void save()}>Save monitor source</button>
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
    <legend>Run-bound monitors</legend><p>Save bindings separately. Required sources block Start when their collection path is unavailable. Missing required evidence makes the final result inconclusive.</p>
    {message && <p role="status">{message}</p>}
    {sources.map(source => { const binding = bindings.sources.find(b => b.source_id === source.id); return <div key={source.id}>
      <label><input type="checkbox" checked={!!binding} disabled={!binding && bindings.sources.length >= 32} onChange={e => setBindings({ ...bindings, sources: e.target.checked ? [...bindings.sources, { source_id: source.id, required: false }] : bindings.sources.filter(b => b.source_id !== source.id) })} />{source.name} ({source.pack})</label>
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
  const [seriesIdentity, setSeriesIdentity] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const controller = useRef<AbortController>()
  const [loadedSelection, setLoadedSelection] = useState('')
  const selectionKey = JSON.stringify([testId, source, metric, dimensions, window])
  const selectedJob = jobs.find(job => job.source.id === source)
  const canonicalProvider = ['appdynamics', 'elastic'].includes(selectedJob?.source.pack || '')
  const collecting = Boolean(selectedJob && ['collecting', 'reconciling', 'draining'].includes(selectedJob.state))
  useEffect(() => {
    const c = new AbortController()
    setLoadedSelection(''); setJobs([]); setPoints([]); setError(''); setBusy(false); setSource(''); setWindow(undefined)
    const refresh = () => { if (enabled) void request<Job[]>(`/tests/${encodeURIComponent(testId)}/monitors`, { signal: c.signal }).then(j => { if (!c.signal.aborted) setJobs(j) }).catch(e => { if (!c.signal.aborted) setError(String(e)) }) }
    refresh()
    const timer = globalThis.setInterval(refresh, 10000)
    return () => { clearInterval(timer); c.abort(); controller.current?.abort() }
  }, [testId, enabled])
  const load = useCallback(async () => {
    controller.current?.abort(); const c = new AbortController(); controller.current = c; setBusy(true); setError('')
    try {
      const filters = JSON.parse(dimensions) as Record<string, string>
      if (!filters || Array.isArray(filters) || typeof filters !== 'object' || Object.values(filters).some(v => typeof v !== 'string')) throw new Error('Dimensions must be a JSON object of string values.')
      const collected: typeof points = []; let afterJob = ''; let afterSequence = -1; let afterKey = ''
      for (let pageNumber = 0; ; pageNumber++) {
        if (pageNumber >= 100) throw new Error('Monitoring evidence exceeds the scan limit; narrow the requested selection.')
        const query = new URLSearchParams({ metric, source_id: source, dimensions: JSON.stringify(filters), ...(canonicalProvider ? { after_key: afterKey } : { after_job: afterJob, after_sequence: String(afterSequence) }) })
        if (window) { query.set('from', window.from); query.set('to', window.to) }
        const page = await request<{ points: typeof points; has_more: boolean; after_job: string; after_sequence: number; after_key?: string }>(`/tests/${encodeURIComponent(testId)}/monitor-series?${query}`, { signal: c.signal })
        collected.push(...page.points); if (collected.length > 10000) throw new Error('Series exceeds 10,000 points. Use a bounded comparison window in Analysis.')
        if (!page.has_more) break
        if (canonicalProvider) { if (!page.after_key || page.after_key === afterKey) throw new Error('Monitoring pagination did not advance.'); afterKey = page.after_key }
        else { afterJob = page.after_job; afterSequence = page.after_sequence }
      }
      const identities = new Set(collected.map(monitorSeriesIdentity))
      if (!c.signal.aborted) { setLoadedSelection(selectionKey); setPoints(collected); setSeriesIdentity(current => identities.has(current) ? current : collected[0] ? monitorSeriesIdentity(collected[0]) : '') }
    } catch (e) { if (!c.signal.aborted) setError(String(e)) } finally { if (!c.signal.aborted) setBusy(false) }
  }, [testId, source, metric, dimensions, window, canonicalProvider, selectionKey])
  useEffect(() => {
    if (!enabled || !collecting || busy || loadedSelection !== selectionKey) return
    const timer = setInterval(() => void load(), 60000)
    return () => clearInterval(timer)
  }, [enabled, collecting, busy, loadedSelection, selectionKey, load])
  if (!enabled || !jobs.length) return null
  const clearLoaded = () => { controller.current?.abort(); setBusy(false); setPoints([]); setLoadedSelection('') }
  const identities = [...new Map(points.map(point => [monitorSeriesIdentity(point), point])).entries()]
  const selectedPoints = points.filter(point => monitorSeriesIdentity(point) === seriesIdentity)
  return <section className="tx-section" aria-label="Run monitor evidence">
    <DependencyEvidence testId={testId} projectId={projectId} select={(source, metric, from, to) => { clearLoaded(); setSource(source); setMetric(metric); setDimensions('{}'); setWindow({ from, to }) }} />
    <ElasticRunEvidence testId={testId} jobs={jobs} sourceId={source || undefined} />
    <AppDynamicsRunEvidence testId={testId} jobs={jobs} sourceId={source || undefined} />
    <h3>Run monitor evidence</h3>{window && <p>Selected interval: {window.from} – {window.to} <button type="button" onClick={() => { clearLoaded(); setWindow(undefined) }}>Clear time window</button></p>}<p>Aligned by elapsed wall time, including pauses. Associations do not establish causation. Loaded selections refresh every 60 seconds during collection and reconciliation.</p>
    <ul>{jobs.map(j => <li key={j.id}>{j.source.name} · {j.state} · {j.next_sequence} accepted batches{j.coverage_reason ? ` · ${j.coverage_reason}` : ''}</li>)}</ul>
    {error && <p role="alert">{error}</p>}
    <label>Monitor source<select value={source} onChange={e => { clearLoaded(); setSource(e.target.value) }}><option value="">Select a source</option>{jobs.map(j => <option key={j.id} value={j.source.id}>{j.source.name}</option>)}</select></label>
    <label>Metric name<input list={`monitor-metrics-${testId}`} value={metric} onChange={e => { clearLoaded(); setMetric(e.target.value) }} /></label>
    <datalist id={`monitor-metrics-${testId}`}>{monitorOverlayMetrics(selectedJob?.source).map(item => <option key={item.name} value={item.name}>{item.unit} · {item.statistic}</option>)}</datalist>
    <label>Dimensions (JSON)<input value={dimensions} onChange={e => { clearLoaded(); setDimensions(e.target.value) }} /></label>
    <button type="button" disabled={busy || !source || !metric} onClick={() => void load()}>{busy ? 'Loading…' : 'Refresh monitor overlay'}</button>
    {points.length > 0 && <><label>Observed monitor series<select value={seriesIdentity} onChange={event => setSeriesIdentity(event.target.value)}>{identities.map(([identity, point]) => <option key={identity} value={identity}>{point.metric_name} ({point.unit}) · {monitorEntityLabel(point, jobs.find(job => job.source.id === source)?.topology)}{point.provider ? ` · ${point.provider.statistic} / ${point.provider.query_window ? 'query window' : `${point.provider.resolution_seconds}s`}` : ''}</option>)}</select></label><p>{selectedPoints.filter(p => p.quality === 'valid').length} valid / {selectedPoints.length} observed samples in the selected series. Missing or stale evidence is excluded.</p>{selectedPoints[0]?.provider && !selectedPoints[0].provider.query_window && selectedPoints[0].provider.window_kind !== 'instant' && <p>First retained bucket: {providerBucketRange(selectedPoints[0], jobs.find(job => job.source.id === source)?.started_at, jobs.find(job => job.source.id === source)?.load_ended_at)}.</p>}{selectedPoints[0]?.provider && !selectedPoints[0].provider.query_window && selectedPoints[0].provider.window_kind !== 'instant' && <p>Source bucket statistic: {selectedPoints[0].provider.statistic} · resolution {selectedPoints[0].provider.resolution_seconds} seconds · {selectedPoints.filter(point => point.provider?.settled).length} settled buckets. Source buckets are not raw transaction samples.</p>}{selectedPoints[0]?.provider?.window_kind === 'instant' && <p>Instantaneous component observations are source API snapshots and carry no fabricated bucket interval.</p>}<MonitorTimelinePlot points={selectedPoints} history={history} window={window} /></>}
  </section>
}
