// Run monitor sources shares the analysis workspace's card layout.
import './AnalysisWorkspace.css'
import ElasticSourceFields from './ElasticSourceFields'
import ElasticRunEvidence from './ElasticRunEvidence'
import { emptyElasticSelection } from './elasticMonitoringModel'
import SolaceManagementSourceFields from './SolaceManagementSourceFields'
import { newSolaceIdentity, newSolaceScope, solaceSourceAvailable, solaceSourceError } from './solaceManagementSource'
import ServiceBusManagementSourceFields from './ServiceBusManagementSourceFields'
import { newServiceBusIdentity, newServiceBusScope, serviceBusSourceAvailable, serviceBusSourceError } from './serviceBusManagementSource'
import KafkaManagementSourceFields from './KafkaManagementSourceFields'
import { kafkaSourceError, newKafkaIdentity, newKafkaScope } from './kafkaManagementSource'
import ManagementCollectionState from "./ManagementCollectionState"
import RunManagementEvidence from "./RunManagementEvidence"
import { useCallback, useEffect, useRef, useState } from 'react'
import MonitorTimelinePlot from './MonitorTimelinePlot'
import { monitorSeriesIdentity, providerBucketRange, monitorEntityLabel } from './monitorTimelineModel'
import DependencyEvidence from './DependencyEvidence'
import MonitorRunEvidence from './MonitorRunEvidence'
import PrometheusSourceFields from './PrometheusSourceFields'
import PrometheusRunEvidence from './PrometheusRunEvidence'
import { emptyPrometheusSelection } from './prometheusConnectionModel'
import DynatraceSourceFields from './DynatraceSourceFields'
import { emptyDynatraceSelection } from '../types/dynatrace'
import { monitorMetricCatalog, monitorSourceSelectionComplete, nativeMonitorProvider } from './monitorProviderModel'
import type { MonitorSource, RunMonitorJob } from '../types/runMonitoring'
import { request } from '../hooks/apiClient'
import type { JsonSuccessResponseBody } from '../types/generated/openapi'
import { useMonitoringCapabilities, useMonitoringEnabled } from './useMonitoringCapabilities'
import type { AnalysisPoint } from '../types/analysis'
import type { MetricsHistoryPoint, MonitoringCollectorListResponse } from '../types'

type ManagementSource = JsonSuccessResponseBody<'handleMonitorSources', '200'>[number]
type Source = MonitorSource & Pick<ManagementSource, 'management_scope' | 'management_identity'>
// Provider forms retain their typed selections; management forms edit only their
// separate approval fields and receive no provider-specific execution settings.
const managementSource = (source: Source): ManagementSource => ({ ...source, project_id: source.project_id ?? '', appdynamics: undefined, prometheus: undefined, dynatrace: undefined, elastic: undefined, executor_mode: undefined })
interface Bindings { revision: number; sources: { source_id: string; required: boolean }[] }
type Job = Omit<RunMonitorJob, 'source'> & { source: Source }
const emptySource = (): Source => ({ project_id: '', id: '', name: '', revision: 0, collector_id: '', pack: 'linux-hosts', interval_seconds: 15, enabled: true })
const sourcePacks = ['linux-hosts', 'go-runtime', 'prometheus', 'ssh-linux', 'otel-metrics', 'jmx', 'jdbc', 'cloudwatch', 'azure-monitor', 'dynatrace', 'prometheus-query', 'elastic']
const sourcesPath = (project: string) => `/projects/${encodeURIComponent(project)}/monitor-sources`


export function RunMonitorSources({ projectId, canManage = false, showEnableHint = false }: { projectId?: string; canManage?: boolean; /** Tell an administrator how the project gate is enabled. */ showEnableHint?: boolean }) {
  const capabilities = useMonitoringCapabilities(projectId)
  const enabled = capabilities?.monitoring_enabled === true
  const kafkaManagementAvailable = capabilities?.messaging_management_enabled === true && capabilities.messaging_management_packs?.includes('kafka-offsets') === true
  const serviceBusManagementAvailable = serviceBusSourceAvailable(capabilities)
  const solaceManagementAvailable = solaceSourceAvailable(capabilities)
  const availablePacks = [...sourcePacks, ...(kafkaManagementAvailable ? ['kafka-offsets'] : []), ...(serviceBusManagementAvailable ? ['servicebus-runtime'] : []), ...(solaceManagementAvailable ? ['solace-semp'] : [])]
  const [sources, setSources] = useState<Source[]>([])
  const [collectors, setCollectors] = useState<MonitoringCollectorListResponse['collectors']>([])
  const [draft, setDraft] = useState<Source>(emptySource)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const [sourceValidation, setSourceValidation] = useState('')
  const operation = useRef<AbortController>()
  useEffect(() => {
    operation.current?.abort(); setDraft(emptySource()); setCollectors([]); setError(''); setBusy(false)
  }, [projectId])
  useEffect(() => {
    const c = new AbortController(); setSources([])
    if (enabled && projectId) void Promise.all([request<Source[]>(sourcesPath(projectId), { signal: c.signal }), request<MonitoringCollectorListResponse>(`/monitoring/collectors?project_id=${encodeURIComponent(projectId)}`, { signal: c.signal })]).then(([s, collectors]) => { if (!c.signal.aborted) { setSources(s); setCollectors(collectors.collectors) } }).catch(e => { if (!c.signal.aborted) setError(String(e)) })
    return () => { c.abort(); operation.current?.abort() }
  }, [enabled, projectId, refresh])
  if (!enabled || !projectId) return <div className="monitor-body"><p className="monitor-hint">Run-bound monitoring is not enabled for this project.{showEnableHint && <> It is enabled per project in the controller configuration (<code>APEXLOAD_RUN_MONITOR_PROJECTS</code>).</>}</p></div>
  const savedDraftSource = sources.find(source => source.id === draft.id && source.pack === draft.pack)
  const savedManagementSource = savedDraftSource ? managementSource(savedDraftSource) : undefined
  // Direct packs query their own connection; every other pack runs on a collector.
  const needsCollector = !['dynatrace', 'prometheus-query', 'elastic'].includes(draft.pack)
  const saveBlockedReason = !draft.name.trim()
    ? 'Enter a name for the source.'
    : needsCollector && !draft.collector_id
      ? collectors.length ? 'Choose the collector that will run this pack.' : 'Register a collector for this project first.'
      : ''
  const showCollectors = () => document.querySelector<HTMLElement>('[data-card-id="collectors"]')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  const changeManagementSource = (next: ManagementSource) => setDraft(current => ({ ...current, management_scope: next.management_scope, management_identity: next.management_identity, collector_public_key: next.collector_public_key }))
  const save = async (remove = false) => {
    const c = new AbortController(); operation.current = c; setBusy(true); setError('')
    try {
      if (!remove && draft.pack === 'kafka-offsets') {
        if (!kafkaManagementAvailable) throw new Error('Kafka management configuration is unavailable for this project.')
        const invalid = kafkaSourceError(managementSource(draft))
        if (invalid) throw new Error(invalid)
      }
      if (!remove && draft.pack === 'servicebus-runtime') {
        if (!serviceBusManagementAvailable) throw new Error('Service Bus management configuration is unavailable for this project.')
        const invalid = serviceBusSourceError(managementSource(draft))
        if (invalid) throw new Error(invalid)
      }
      if (!remove && draft.pack === 'solace-semp') {
        if (!solaceManagementAvailable) throw new Error('Solace management configuration is unavailable for this project.')
        const invalid = solaceSourceError(managementSource(draft))
        if (invalid) throw new Error(invalid)
      }
      const path = `${sourcesPath(projectId)}${draft.id ? `/${encodeURIComponent(draft.id)}` : ''}${remove ? `?revision=${draft.revision}` : ''}`
      await request(path, { method: remove ? 'DELETE' : draft.id ? 'PUT' : 'POST', signal: c.signal, ...(remove ? {} : { body: JSON.stringify({ ...draft, id: draft.id || undefined }) }) })
      if (!c.signal.aborted) { setDraft(emptySource()); setRefresh(x => x + 1) }
    } catch (e) { if (!c.signal.aborted) setError(String(e)) } finally { if (!c.signal.aborted) setBusy(false) }
  }
  return <section className="analysis-workspace run-monitor-sources monitor-body" aria-label="Run monitor sources">
    <p className="monitor-status-note">Sources are captured when a run starts. Edits apply to subsequent runs. Collection continues through pauses.</p>
    {error && <p className="form-error" role="alert">{error}</p>}
    {draft.id && draft.management_scope && <ManagementCollectionState key={`${projectId}:${draft.id}`} projectId={projectId} sourceId={draft.id} canManage={canManage} />}
    {sources.length > 0 && <ul className="run-monitor-source-list">{sources.map(source => <li key={source.id}>{source.name} · {source.pack} · revision {source.revision} · {source.enabled ? 'enabled' : 'disabled'} {canManage && <button type="button" className="btn btn-secondary btn-sm" disabled={busy || source.pack === 'appdynamics'} onClick={() => setDraft(source)}>Edit {source.name}</button>}</li>)}</ul>}
    {canManage && <fieldset disabled={busy}>
      <legend>{draft.id ? 'Edit source' : 'New source'}</legend>
      <label>Name (required)<input value={draft.name} maxLength={256} required aria-required="true" onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
      <label>Collector{needsCollector ? ' (required)' : ''}<select disabled={!needsCollector || collectors.length === 0} required={needsCollector} aria-required={needsCollector || undefined} value={draft.collector_id} onChange={e => setDraft({ ...draft, collector_id: e.target.value })}><option value="">{needsCollector && collectors.length === 0 ? 'No collector registered' : 'Select a collector'}</option>{collectors.map(c => <option key={c.id} value={c.id}>{c.name} · {c.health}</option>)}</select></label>
      {needsCollector && collectors.length === 0 && <p className="run-monitor-empty-collector" role="status">No collector is registered for this project, and this pack runs on one. <button type="button" className="btn btn-ghost btn-sm" onClick={showCollectors}>Go to Collectors</button></p>}
      <label>Pack<select value={draft.pack} onChange={e => setDraft({ ...draft, pack: e.target.value, destination: '', credential_ref: '', collector_public_key: '', connection_id: undefined, executor_mode: ['dynatrace', 'prometheus-query', 'elastic'].includes(e.target.value) ? 'direct' : undefined, appdynamics: undefined, elastic: e.target.value === 'elastic' ? emptyElasticSelection(projectId) : undefined, dynatrace: e.target.value === 'dynatrace' ? emptyDynatraceSelection() : undefined, prometheus: e.target.value === 'prometheus-query' ? emptyPrometheusSelection() : undefined, management_scope: e.target.value === 'solace-semp' ? newSolaceScope() : e.target.value === 'servicebus-runtime' ? newServiceBusScope() : e.target.value === 'kafka-offsets' ? newKafkaScope() : undefined, management_identity: e.target.value === 'solace-semp' ? newSolaceIdentity() : e.target.value === 'servicebus-runtime' ? newServiceBusIdentity() : e.target.value === 'kafka-offsets' ? newKafkaIdentity() : undefined, interval_seconds: e.target.value === 'elastic' ? 10 : e.target.value === 'prometheus-query' ? 30 : ['dynatrace'].includes(e.target.value) ? 60 : ['cloudwatch', 'azure-monitor'].includes(e.target.value) ? Math.max(60, draft.interval_seconds) : ['servicebus-runtime', 'solace-semp'].includes(e.target.value) ? Math.max(30, draft.interval_seconds) : e.target.value === 'kafka-offsets' ? Math.max(10, draft.interval_seconds) : draft.interval_seconds })}>{!availablePacks.includes(draft.pack) && <option value={draft.pack} disabled>{draft.pack} (configured management pack)</option>}{availablePacks.map(p => <option key={p}>{p}</option>)}</select></label>
      {!['linux-hosts', 'go-runtime', 'dynatrace', 'prometheus-query', 'elastic'].includes(draft.pack) && <label>Approved destination<input type={['kafka-offsets', 'servicebus-runtime', 'solace-semp'].includes(draft.pack) ? 'text' : 'url'} value={draft.destination ?? ''} onChange={e => setDraft({ ...draft, destination: e.target.value })} /></label>}
      <label>Dependency identity<input value={draft.service ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, service: e.target.value })} /><small>Exact dependency label used to link collected evidence.</small></label>
      {['ssh-linux', 'jmx', 'jdbc'].includes(draft.pack) && <>
        <label>Project credential secret name<input value={draft.credential_ref ?? ''} maxLength={128} onChange={e => setDraft({ ...draft, credential_ref: e.target.value })} /></label>
        <label>Collector X25519 public key<input value={draft.collector_public_key ?? ''} onChange={e => setDraft({ ...draft, collector_public_key: e.target.value })} /><small>Enroll the public key from the approved collector. Jobs seal credentials to this key.</small></label>
      </>}
      {draft.pack === 'kafka-offsets' && <KafkaManagementSourceFields source={managementSource(draft)} savedSource={savedManagementSource} available={kafkaManagementAvailable} onChange={changeManagementSource} />}
      {draft.pack === 'servicebus-runtime' && <ServiceBusManagementSourceFields source={managementSource(draft)} savedSource={savedManagementSource} available={serviceBusManagementAvailable} onChange={changeManagementSource} />}
      {draft.pack === 'solace-semp' && <SolaceManagementSourceFields source={managementSource(draft)} savedSource={savedManagementSource} available={solaceManagementAvailable} onChange={changeManagementSource} />}
      {draft.pack === 'prometheus-query' && <PrometheusSourceFields projectId={projectId} source={draft} onChange={setDraft} />}
      {draft.pack === 'elastic' && <ElasticSourceFields key={draft.id || 'new'} projectId={projectId} source={draft} onChange={setDraft} onValidationChange={setSourceValidation} />}
      {draft.pack === 'dynatrace' && <DynatraceSourceFields projectId={projectId} source={draft} onChange={setDraft} />}
      <label>Collection interval (seconds)<input type="number" min={['dynatrace', 'cloudwatch', 'azure-monitor'].includes(draft.pack) ? 60 : ['servicebus-runtime', 'solace-semp'].includes(draft.pack) ? 30 : draft.pack === 'kafka-offsets' ? 10 : 1} max={3600} value={draft.interval_seconds} onChange={e => setDraft({ ...draft, interval_seconds: Number(e.target.value) })} /></label>
      <label><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />Enabled</label>
      {saveBlockedReason && <p id="run-monitor-save-reason" className="run-monitor-save-reason">{saveBlockedReason}</p>}
      <button type="button" className="btn btn-primary btn-sm" aria-describedby={saveBlockedReason ? 'run-monitor-save-reason' : undefined} disabled={Boolean(saveBlockedReason) || !draft.name || (draft.pack === 'elastic' && Boolean(sourceValidation)) || !monitorSourceSelectionComplete(draft) || (draft.pack === 'kafka-offsets' && (!kafkaManagementAvailable || !!kafkaSourceError(managementSource(draft)))) || (draft.pack === 'servicebus-runtime' && (!serviceBusManagementAvailable || !!serviceBusSourceError(managementSource(draft)))) || (draft.pack === 'solace-semp' && (!solaceManagementAvailable || !!solaceSourceError(managementSource(draft))))} onClick={() => void save()}>Save monitor source</button>
      {draft.id && <><button type="button" className="btn btn-danger btn-sm" onClick={() => void save(true)}>Delete source</button><button type="button" className="btn btn-secondary btn-sm" onClick={() => setDraft(emptySource())}>New source</button></>}
    </fieldset>}
  </section>
}

// The bindings' own revision guards the binding set; scenarioTag is the
// scenario baseline the surrounding editor loaded. A stale tag (412) hands the
// conflict to that editor, and a saved binding set lets it advance its
// baseline.
export function ScenarioMonitorBindings({ projectId, scenarioId, canEdit, scenarioTag, onSaved, onScenarioChanged }: { projectId: string; scenarioId: string; canEdit: boolean; scenarioTag?: string; onSaved?: () => void; onScenarioChanged?: () => void }) {
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
      <label><input type="checkbox" checked={!!binding} disabled={(!binding && bindings.sources.length >= 32) || (source.pack === 'appdynamics' && !binding)} onChange={e => setBindings({ ...bindings, sources: e.target.checked ? [...bindings.sources, { source_id: source.id, required: false }] : bindings.sources.filter(b => b.source_id !== source.id) })} />{source.name} ({source.pack})</label>
      {binding && source.pack !== 'appdynamics' && <label><input type="checkbox" checked={binding.required} onChange={e => setBindings({ ...bindings, sources: bindings.sources.map(b => b.source_id === source.id ? { ...b, required: e.target.checked } : b) })} />Require {source.name} readiness</label>}
    </div> })}
    {canEdit && <button type="button" onClick={() => { const c = new AbortController(); operation.current = c; setBusy(true); setMessage(''); void request<Bindings>(path, { method: 'PUT', signal: c.signal, ...(scenarioTag ? { headers: { 'If-Match': scenarioTag } } : {}), body: JSON.stringify(bindings) }).then(b => { if (!c.signal.aborted) { setBindings(b); setMessage('Monitor bindings saved.'); onSaved?.() } }).catch(e => { if (c.signal.aborted) return; if ((e as { status?: unknown } | null)?.status === 412) { setMessage('The scenario changed after this builder loaded it. Review the current scenario before saving bindings again.'); onScenarioChanged?.() } else setMessage(String(e)) }).finally(() => { if (!c.signal.aborted) setBusy(false) }) }}>Save monitor bindings</button>}
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
  const [evidenceSelection, setEvidenceSelection] = useState<Record<string, unknown>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const controller = useRef<AbortController>()
  const [loadedSelection, setLoadedSelection] = useState('')
  const selectionKey = JSON.stringify([testId, source, metric, dimensions, window])
  const selectedJob = jobs.find(job => job.source.id === source)
  const canonicalProvider = nativeMonitorProvider(selectedJob?.source.pack)
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
      if (!c.signal.aborted) { setLoadedSelection(selectionKey); setPoints(collected); setEvidenceSelection({ source_id: source, metric, dimensions: filters, window }); setSeriesIdentity(current => identities.has(current) ? current : collected[0] ? monitorSeriesIdentity(collected[0]) : '') }
    } catch (e) { if (!c.signal.aborted) setError(String(e)) } finally { if (!c.signal.aborted) setBusy(false) }
  }, [testId, source, metric, dimensions, window, canonicalProvider, selectionKey])
  useEffect(() => {
    if (!enabled || !collecting || busy || loadedSelection !== selectionKey) return
    const timer = setInterval(() => void load(), 60000)
    return () => clearInterval(timer)
  }, [enabled, collecting, busy, loadedSelection, selectionKey, load])
  if (!enabled || !jobs.length) return <RunManagementEvidence projectId={projectId} testId={testId} />
  const clearLoaded = () => { controller.current?.abort(); setBusy(false); setPoints([]); setLoadedSelection('') }
  const identities = [...new Map(points.map(point => [monitorSeriesIdentity(point), point])).entries()]
  const selectedPoints = points.filter(point => monitorSeriesIdentity(point) === seriesIdentity)
  return <section className="tx-section" aria-label="Run monitor evidence">
    <RunManagementEvidence projectId={projectId} testId={testId} />
    <DependencyEvidence testId={testId} projectId={projectId} select={(source, metric, from, to) => { clearLoaded(); setSource(source); setMetric(metric); setDimensions('{}'); setWindow({ from, to }) }} />
    <PrometheusRunEvidence testId={testId} jobs={jobs} sourceId={source || undefined} />
    <MonitorRunEvidence testId={testId} jobs={jobs} sourceId={source || undefined} />
    <ElasticRunEvidence testId={testId} jobs={jobs} sourceId={source || undefined} />
    <h3>Run monitor evidence</h3>{window && <p>Selected interval: {window.from} – {window.to} <button type="button" onClick={() => { clearLoaded(); setWindow(undefined) }}>Clear time window</button></p>}<p>Aligned by elapsed wall time, including pauses. Associations do not establish causation. Loaded selections refresh every 60 seconds during collection and reconciliation.</p>
    <ul>{jobs.map(j => <li key={j.id}>{j.source.name} · {j.state} · {j.next_sequence} accepted batches{j.coverage_reason ? ` · ${j.coverage_reason}` : ''}</li>)}</ul>
    {error && <p role="alert">{error}</p>}
    <label>Monitor source<select value={source} onChange={e => { clearLoaded(); setSource(e.target.value) }}><option value="">Select a source</option>{jobs.map(j => <option key={j.id} value={j.source.id}>{j.source.name}</option>)}</select></label>
    <label>Metric name<input list={`monitor-metrics-${testId}`} value={metric} onChange={e => { clearLoaded(); setMetric(e.target.value) }} /></label>
    <datalist id={`monitor-metrics-${testId}`}>{monitorMetricCatalog(selectedJob?.source).map(item => <option key={item.name} value={item.name}>{item.unit} · {item.path}</option>)}</datalist>
    <label>Dimensions (JSON)<input value={dimensions} onChange={e => { clearLoaded(); setDimensions(e.target.value) }} /></label>
    <button type="button" disabled={busy || !source || !metric} onClick={() => void load()}>{busy ? 'Loading…' : 'Refresh monitor overlay'}</button>
    {points.length > 0 && <><label>Observed monitor series<select value={seriesIdentity} onChange={event => setSeriesIdentity(event.target.value)}>{identities.map(([identity, point]) => <option key={identity} value={identity}>{point.metric_name} ({point.unit}) · {monitorEntityLabel(point, jobs.find(job => job.source.id === source)?.topology)}{point.provider ? ` · ${point.provider.statistic} / ${point.provider.query_window ? 'query window' : point.provider.name === 'prometheus' ? `${point.provider.evaluation_step_seconds}s evaluation grid` : `${point.provider.resolution_seconds}s`}` : ''}</option>)}</select></label><p>{selectedPoints.filter(p => p.quality === 'valid').length} valid / {selectedPoints.length} observed samples in the selected series. Missing or stale evidence is excluded.</p>{['appdynamics', 'dynatrace', 'elasticsearch', 'opensearch'].includes(selectedPoints[0]?.provider?.name || '') && selectedPoints[0]?.provider && !selectedPoints[0].provider.query_window && selectedPoints[0].provider.window_kind !== 'instant' && <p>First retained bucket: {providerBucketRange(selectedPoints[0], jobs.find(job => job.source.id === source)?.started_at, jobs.find(job => job.source.id === source)?.load_ended_at)}.</p>}{['appdynamics', 'dynatrace', 'elasticsearch', 'opensearch'].includes(selectedPoints[0]?.provider?.name || '') && selectedPoints[0]?.provider && !selectedPoints[0].provider.query_window && selectedPoints[0].provider.window_kind !== 'instant' && <p>Source bucket statistic: {selectedPoints[0].provider.statistic} · resolution {selectedPoints[0].provider.resolution_seconds} seconds · {selectedPoints.filter(point => point.provider?.settled).length} settled buckets. Source buckets are not raw transaction samples.</p>}{selectedPoints[0]?.provider?.window_kind === 'instant' && <p>Instantaneous component observations are source API snapshots and carry no fabricated bucket interval.</p>}{selectedPoints[0]?.provider?.name === 'prometheus' && <p>Prometheus evaluation step: {selectedPoints[0].provider.evaluation_step_seconds}s. Source freshness: {selectedPoints.every(point => point.provider?.freshness_known) ? 'known' : 'unknown for some observations'}. Expression evaluation timestamps are not raw scrape timestamps.</p>}<MonitorTimelinePlot runId={testId} selection={evidenceSelection} points={selectedPoints} history={history} window={window} /></>}
  </section>
}
