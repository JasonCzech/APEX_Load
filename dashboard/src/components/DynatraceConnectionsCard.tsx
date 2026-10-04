import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useDynatraceConnectionsApi, type DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceConnection } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

import { dynatraceMissing } from './captureConnectionModel'
import { CaptureSetupHelp, CaptureCorrelationHelp, CaptureConnectionHealth } from './CaptureConnectionShared'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { settingsHref } from './settingsNavigation'
import { dynatraceCanEnable, dynatraceScopeHelp } from './dynatraceConnectionModel'
import type { DynatraceDiscoveryOptions, DynatraceOperation } from '../types/dynatrace'
import { appDynamicsCapabilityHealth, type AppDynamicsCapabilities } from '../types/appdynamics'

interface Props { projectId?: string; canManage?: boolean; summaryOnly?: boolean }

const emptyDraft = (): DynatraceConnectionInput => ({
  name: '', description: '', enabled: false, environment_url: '', platform_url: '', metrics_enabled: false, problems_enabled: false, logs_enabled: false,
  platform_auth_type: '', oauth_resource: '', oauth_scopes: [], metric_queries: [], problem_selector: '', problem_entity_selector: '',
  log_base_filter: '', log_test_id_field: 'apex.test_id', log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: 'trace_id', poll_interval_seconds: 15,
  collection_mode: 'direct', collector_agent_id: '', events_enabled: false, spans_enabled: false, grail_metrics_enabled: false, grail_events_enabled: false, topology_mode: '', requests_per_minute: 60, platform_requests_per_minute: 60, max_scan_bytes: 1000000000, max_run_scan_bytes: 10000000000, drain_reserve_bytes: 2000000000,
})

function draftFrom(c: DynatraceConnection): DynatraceConnectionInput {
  return { name: c.name, description: c.description || '', enabled: c.enabled, environment_url: c.environment_url || '', platform_url: c.platform_url || '', metrics_enabled: c.metrics_enabled, problems_enabled: c.problems_enabled, logs_enabled: c.logs_enabled,
    platform_auth_type: c.platform_auth_type || '', oauth_resource: c.oauth_resource || '', oauth_scopes: c.oauth_scopes || [], metric_queries: c.metric_queries || [], problem_selector: c.problem_selector || '', problem_entity_selector: c.problem_entity_selector || '',
    log_base_filter: c.log_base_filter || '', log_test_id_field: c.log_test_id_field || '', log_timestamp_field: c.log_timestamp_field || 'timestamp', log_status_field: c.log_status_field || 'status', log_message_field: c.log_message_field || 'content', log_trace_id_field: c.log_trace_id_field || 'trace_id', poll_interval_seconds: c.poll_interval_seconds || 15, updated_at: c.updated_at,
    expected_revision: c.revision, collection_mode: c.collection_mode || 'direct', collector_agent_id: c.collector_agent_id || '', events_enabled: Boolean(c.events_enabled), spans_enabled: Boolean(c.spans_enabled), grail_metrics_enabled: Boolean(c.grail_metrics_enabled), grail_events_enabled: Boolean(c.grail_events_enabled), topology_mode: c.topology_mode || '', requests_per_minute: c.requests_per_minute || 60, platform_requests_per_minute: c.platform_requests_per_minute || 60, max_scan_bytes: c.max_scan_bytes || 1000000000, max_run_scan_bytes: c.max_run_scan_bytes || 10000000000, drain_reserve_bytes: c.drain_reserve_bytes ?? 2000000000 }
}

export default function DynatraceConnectionsCard(props: Props) {
  return <DynatraceConnectionEditor key={props.projectId || 'none'} {...props} />
}

function DynatraceConnectionEditor({ projectId, canManage = false, summaryOnly = false }: Props) {
  const api = useDynatraceConnectionsApi()
  const [connections, setConnections] = useState<DynatraceConnection[]>([])
  const [editing, setEditing] = useState<DynatraceConnection | null>(null)
  const [draft, setDraft] = useState<DynatraceConnectionInput>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [editorOpen, setEditorOpen] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [operation, setOperation] = useState<DynatraceOperation>()
  const [operationConnection, setOperationConnection] = useState('')
  const [discovery, setDiscovery] = useState<DynatraceDiscoveryOptions>({ kind: 'metrics', search: '', selector: '', limit: 100 })
  const mounted = useRef(true)
  const inspectionVersion = useRef(0)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; inspectionVersion.current += 1 } }, [])
  const missing = dynatraceMissing(draft, editing)
  const canEnable = dynatraceCanEnable(draft, editing)
  const platformRequired = draft.logs_enabled || draft.spans_enabled || draft.grail_metrics_enabled || draft.grail_events_enabled || draft.topology_mode === 'smartscape'
  const environmentRequired = draft.metrics_enabled || draft.problems_enabled || draft.events_enabled || draft.topology_mode === 'classic'
  const resetDraft = () => { setEditing(null); setDraft(emptyDraft()); setEditorOpen(false) }
  const load = useCallback(async () => {
    if (!projectId) { setConnections([]); setLoading(false); return }
    setLoading(true)
    try { const result = await api.listDynatraceConnections(projectId); if (!mounted.current) return; setConnections(result.connections || []); setError('') }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Failed to load Dynatrace connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])
  useEffect(() => {
    if (!projectId || !operation?.connection_id || !['pending', 'queued', 'running'].includes(operation.status)) return
    const controller = new AbortController()
    const connection = operation.connection_id
    const version = inspectionVersion.current
    const timer = setTimeout(() => { void api.getDynatraceOperation(projectId, connection, operation.id, { signal: controller.signal }).then(result => { if (!controller.signal.aborted && version === inspectionVersion.current) { setOperation({ ...result, connection_id: connection }); if (!['pending', 'queued', 'running'].includes(result.status)) void load() } }).catch(err => { if (!controller.signal.aborted && version === inspectionVersion.current) { setError(getErrorMessage(err, 'Operation failed')); setOperation(undefined) } }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, operation, operationConnection, load])
  const inspect = async (connection: string, kind: 'verify' | 'discover', cursor?: string) => {
    if (!projectId) return
    const version = ++inspectionVersion.current
    setBusy(true); setError(''); setOperation(undefined); setOperationConnection(connection)
    try { const result = await api.createDynatraceOperation(projectId, connection, { kind, ...(kind === 'discover' ? { options: { ...discovery, cursor } } : {}) }); if (mounted.current && version === inspectionVersion.current) setOperation({ ...result, connection_id: connection }) }
    catch (err) { if (mounted.current && version === inspectionVersion.current) setError(getErrorMessage(err, 'Unable to start Dynatrace inspection')) } finally { if (mounted.current && version === inspectionVersion.current) setBusy(false) }
  }
  const chooseDiscoveryConnection = (connection: string) => { inspectionVersion.current += 1; setOperation(undefined); setOperationConnection(connection); setError('') }
  const changeDiscovery = (next: DynatraceDiscoveryOptions) => { inspectionVersion.current += 1; setOperation(undefined); setDiscovery(next); setError('') }
  const save = async () => {
    if (!projectId || busy || !draft.name.trim() || (draft.enabled && missing.length > 0)) return false
    const payload = { ...draft, environment_token_secret: draft.environment_token_secret?.trim() || undefined, platform_token_secret: draft.platform_token_secret?.trim() || undefined, oauth_client_id_secret: draft.oauth_client_id_secret?.trim() || undefined, oauth_client_secret_secret: draft.oauth_client_secret_secret?.trim() || undefined }
    setBusy(true); setError('')
    try { if (editing) await api.updateDynatraceConnection(projectId, editing.id, payload); else await api.createDynatraceConnection(projectId, payload); if (!mounted.current) return false; resetDraft(); setNotice('Connection saved. Test saved configuration before enabling capture for a run.'); await load(); return true }
    catch (err) { setError(getErrorMessage(err, 'Failed to save Dynatrace connection')); return false } finally { setBusy(false) }
  }
  const dirty = editorOpen && JSON.stringify(draft) !== JSON.stringify(editing ? draftFrom(editing) : emptyDraft())
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const saveRef = useRef(save)
  saveRef.current = save
  const resetRef = useRef(resetDraft)
  resetRef.current = resetDraft
  const guard = useMemo(() => ({
    id: 'dynatrace-connection-draft',
    label: 'Dynatrace connection draft',
    isDirty: () => dirtyRef.current,
    save: () => saveRef.current(),
    discard: () => resetRef.current(),
  }), [])
  useNavigationGuard(guard, dirty)

  if (!projectId) return <p>Select a project to configure Dynatrace capture.</p>
  return <div className="capture-connections" aria-label="Dynatrace connections">
    <p>Dynatrace SaaS monitoring runs directly from the controller or through an assigned collector. Saved sources choose metrics and evidence; tests opt in explicitly and retain immutable revisions.</p>
    {notice && <p role="status">{notice}</p>}
    {loading && <p role="status">Loading connections…</p>}
    {error && <button type="button" className="btn btn-secondary" onClick={() => void load()}>Retry loading connections</button>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {connections.length === 0 && !loading && !error && <p>No Dynatrace connections are configured for this project.</p>}
    {connections.map(c => <div className="capture-connection-row" key={c.id}><div><strong>{c.name}</strong><p>{canManage && `${c.environment_url || c.platform_url || 'Endpoints not configured'} · `}Test capture {c.legacy_capture_status ?? c.status} · Native monitoring {c.status} · {c.collection_mode === 'collector' ? 'Assigned collector' : 'Direct collection'}</p>{canManage && <CaptureConnectionHealth enabled={c.enabled} missing={dynatraceMissing(draftFrom(c), c)} checkedAt={c.last_checked_at} updatedAt={c.updated_at} status={c.last_check_status} message={c.last_check_message} capabilities={c.last_capability_health} />}{canManage && (!c.revision || c.verified_revision !== c.revision) && <p>Current revision has not been verified.</p>}</div>
      {canManage && !summaryOnly && <div className="capture-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen} onClick={() => { setEditing(c); setDraft(draftFrom(c)); setEditorOpen(true); setNotice('') }}>Edit</button><button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen || dynatraceMissing(draftFrom(c), c).length > 0} onClick={() => void inspect(c.id, 'verify')}>Test saved connection</button><button className="btn btn-danger btn-sm" type="button" disabled={busy || editorOpen} onClick={() => setDeleting(c.id)}>Delete</button>
        <button type="button" className="btn btn-secondary btn-sm" disabled={busy || editorOpen} onClick={() => chooseDiscoveryConnection(c.id)}>Discover metrics and entities</button>
        {deleting === c.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {c.name}? It will no longer be available for new tests. Existing run snapshots are unchanged.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteDynatraceConnection(projectId, c.id, c.revision); if (!mounted.current) return; setDeleting(null); if (editing?.id === c.id) resetDraft(); await load() } catch (err) { setError(getErrorMessage(err, 'Delete failed')) } finally { setBusy(false) } }}>Confirm delete</button><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setDeleting(null)}>Keep connection</button></div>}</div>}
    </div>)}
    {canManage && !summaryOnly && operationConnection && <fieldset disabled={busy || editorOpen} aria-label="Dynatrace discovery"><legend>Discover Dynatrace</legend><label>Discovery type<select value={discovery.kind} onChange={event => changeDiscovery({ ...discovery, kind: event.target.value as DynatraceDiscoveryOptions['kind'] })}>{['metrics', 'entities', 'entity_types', 'smartscape'].map(kind => <option key={kind}>{kind}</option>)}</select></label><label>Discovery search<input value={discovery.search || ''} onChange={event => changeDiscovery({ ...discovery, search: event.target.value })} /></label><label>Discovery selector<input value={discovery.selector || ''} onChange={event => changeDiscovery({ ...discovery, selector: event.target.value })} /></label><button type="button" onClick={() => void inspect(operationConnection, 'discover')}>Discover</button></fieldset>}
    {operation && <section aria-label="Dynatrace operation"><p role="status">{operation.kind}: {operation.status}{operation.message ? ` · ${operation.message}` : ''}</p>{operation.result?.capabilities && <ul aria-label="Dynatrace capability diagnostics">{Object.entries(appDynamicsCapabilityHealth(operation.result.capabilities as AppDynamicsCapabilities) || {}).map(([name, capability]) => <li key={name}>{name}: {capability.status}{capability.message ? ` · ${capability.message}` : ''}</li>)}</ul>}{operation.result?.truncated && <p>Discovery results are truncated. Narrow the selector or continue to the next page.</p>}<table className="advanced-group-table"><thead><tr><th>Name</th><th>Provider ID</th><th>Type</th></tr></thead><tbody>{(operation.result?.items || operation.result?.entities || operation.result?.metrics || []).map(item => <tr key={item.id}><td>{item.name}</td><td>{item.id}</td><td>{item.type}</td></tr>)}</tbody></table>{operation.result?.next_cursor && operation.connection_id && <button type="button" disabled={busy} onClick={() => void inspect(operation.connection_id || '', 'discover', operation.result?.next_cursor)}>Next discovery page</button>}</section>}
    {summaryOnly && canManage && <a className="btn btn-secondary" href={settingsHref('dynatrace-capture')}>Manage Dynatrace connections</a>}
    {canManage && !summaryOnly && !editorOpen && <button type="button" className="btn btn-primary" onClick={() => { resetDraft(); setEditorOpen(true); setNotice('') }}>Add Dynatrace connection</button>}
    {canManage && !summaryOnly && editorOpen && <section aria-label="Dynatrace connection editor"><fieldset className="capture-editor" disabled={busy}><h3>{editing ? `Edit ${editing.name}` : 'Add Dynatrace connection'}</h3><CaptureSetupHelp missing={missing} credentials={[editing?.has_environment_token && 'Environment token', editing?.has_platform_token && 'Platform token', editing?.has_oauth_client_id && 'OAuth client ID', editing?.has_oauth_client_secret && 'OAuth client secret'].filter((value): value is string => Boolean(value))} editing={Boolean(editing)} /><p>Choose what to capture first. Metrics and Problems use the Environment API; Grail queries use the Platform API and separate authentication.</p><div className="capture-form-grid">
      <h4 className="capture-section-title">Basics</h4><label><span>Name (required)</span><input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
      <label><span>Description</span><input value={draft.description} onChange={e => setDraft({ ...draft, description: e.target.value })} /></label>
      <h4 className="capture-section-title">Capture capabilities</h4>
      <label className="capture-checkbox"><span>Metrics API</span><input type="checkbox" checked={draft.metrics_enabled} onChange={e => setDraft({ ...draft, metrics_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Problems API</span><input type="checkbox" checked={draft.problems_enabled} onChange={e => setDraft({ ...draft, problems_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Grail logs</span><input type="checkbox" checked={draft.logs_enabled} onChange={e => setDraft({ ...draft, logs_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Events API</span><input type="checkbox" checked={Boolean(draft.events_enabled)} onChange={e => setDraft({ ...draft, events_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Grail events</span><input type="checkbox" checked={Boolean(draft.grail_events_enabled)} onChange={e => setDraft({ ...draft, grail_events_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Grail spans and trace summaries</span><input type="checkbox" checked={Boolean(draft.spans_enabled)} onChange={e => setDraft({ ...draft, spans_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Grail numeric queries</span><input type="checkbox" checked={Boolean(draft.grail_metrics_enabled)} onChange={e => setDraft({ ...draft, grail_metrics_enabled: e.target.checked })} /></label>
      <label><span>Topology capability</span><select value={draft.topology_mode || ''} onChange={e => setDraft({ ...draft, topology_mode: e.target.value as DynatraceConnectionInput['topology_mode'] })}><option value="">Off</option><option value="classic">Classic entities</option><option value="smartscape">Smartscape</option></select></label>
      <p>{dynatraceScopeHelp()}</p>
      <h4 className="capture-section-title">Capability-specific connection details</h4>
      {environmentRequired && <><label><span>Environment URL</span><input placeholder="https://abc.live.dynatrace.com" value={draft.environment_url} onChange={e => setDraft({ ...draft, environment_url: e.target.value })} /></label></>}
      {platformRequired && <><label><span>Platform URL</span><input placeholder="https://abc.apps.dynatrace.com" value={draft.platform_url} onChange={e => setDraft({ ...draft, platform_url: e.target.value })} /></label></>}
      {environmentRequired && <><label><span>Environment token project-secret reference</span><input type="password" autoComplete="off" value={draft.environment_token_secret || ''} onChange={e => setDraft({ ...draft, environment_token_secret: e.target.value })} /></label></>}
      {platformRequired && <><label><span>Platform authentication</span><select value={draft.platform_auth_type} onChange={e => setDraft({ ...draft, platform_auth_type: e.target.value as DynatraceConnectionInput['platform_auth_type'] })}><option value="">Not configured</option><option value="platform_token">Platform token</option><option value="oauth_client">OAuth client</option></select></label>
      {draft.platform_auth_type === 'platform_token' && <label><span>Platform token project-secret reference</span><input type="password" autoComplete="off" value={draft.platform_token_secret || ''} onChange={e => setDraft({ ...draft, platform_token_secret: e.target.value })} /></label>}
      {draft.platform_auth_type === 'oauth_client' && <><label><span>OAuth client ID secret reference</span><input type="password" value={draft.oauth_client_id_secret || ''} onChange={e => setDraft({ ...draft, oauth_client_id_secret: e.target.value })} /></label><label><span>OAuth client secret reference</span><input type="password" value={draft.oauth_client_secret_secret || ''} onChange={e => setDraft({ ...draft, oauth_client_secret_secret: e.target.value })} /></label><label><span>OAuth resource</span><input value={draft.oauth_resource} onChange={e => setDraft({ ...draft, oauth_resource: e.target.value })} /></label><label><span>OAuth scopes</span><input value={draft.oauth_scopes.join(' ')} onChange={e => setDraft({ ...draft, oauth_scopes: e.target.value.split(/\s+/).filter(Boolean) })} /></label></>}</>}
      {draft.problems_enabled && <><label><span>Problem selector</span><input value={draft.problem_selector} onChange={e => setDraft({ ...draft, problem_selector: e.target.value })} /></label><label><span>Problem entity selector</span><input value={draft.problem_entity_selector} onChange={e => setDraft({ ...draft, problem_entity_selector: e.target.value })} /></label></>}
      {draft.logs_enabled && <><CaptureCorrelationHelp /><label><span>Log base DQL filter (optional)</span><input value={draft.log_base_filter} onChange={e => setDraft({ ...draft, log_base_filter: e.target.value })} /></label>
      <label><span>Log test ID field</span><input value={draft.log_test_id_field} onChange={e => setDraft({ ...draft, log_test_id_field: e.target.value })} /></label>
      <label><span>Log timestamp field</span><input value={draft.log_timestamp_field} onChange={e => setDraft({ ...draft, log_timestamp_field: e.target.value })} /></label>
      <label><span>Log status field</span><input value={draft.log_status_field} onChange={e => setDraft({ ...draft, log_status_field: e.target.value })} /></label>
      <label><span>Log message field</span><input value={draft.log_message_field} onChange={e => setDraft({ ...draft, log_message_field: e.target.value })} /></label>
      <label><span>Log trace ID field</span><input value={draft.log_trace_id_field} onChange={e => setDraft({ ...draft, log_trace_id_field: e.target.value })} /></label>
      </>}
      <label><span>Collection mode</span><select value={draft.collection_mode || 'direct'} onChange={e => setDraft({ ...draft, collection_mode: e.target.value as 'direct' | 'collector', collector_agent_id: e.target.value === 'direct' ? '' : draft.collector_agent_id })}><option value="direct">Direct from controller</option><option value="collector">Assigned collector</option></select></label>
      {draft.collection_mode === 'collector' && <label><span>Assigned collector ID</span><input value={draft.collector_agent_id || ''} onChange={e => setDraft({ ...draft, collector_agent_id: e.target.value })} /></label>}
      <label><span>Environment requests per minute</span><input type="number" min={1} max={60} value={draft.requests_per_minute || 60} onChange={e => setDraft({ ...draft, requests_per_minute: Number(e.target.value) })} /></label>
      <label><span>Platform requests per minute</span><input type="number" min={1} max={60} value={draft.platform_requests_per_minute || 60} onChange={e => setDraft({ ...draft, platform_requests_per_minute: Number(e.target.value) })} /></label>
      <label><span>Grail maximum scanned bytes per query</span><input type="number" min={1} max={100000000000} value={draft.max_scan_bytes || 1000000000} onChange={e => setDraft({ ...draft, max_scan_bytes: Number(e.target.value) })} /></label>
      <label><span>Grail maximum scanned bytes per source/run</span><input type="number" min={1} max={1000000000000} value={draft.max_run_scan_bytes || 10000000000} onChange={e => setDraft({ ...draft, max_run_scan_bytes: Number(e.target.value) })} /></label>
      <label><span>Grail scanned bytes reserved for final collection</span><input type="number" min={0} max={draft.max_run_scan_bytes || 10000000000} value={draft.drain_reserve_bytes ?? 2000000000} onChange={e => setDraft({ ...draft, drain_reserve_bytes: Number(e.target.value) })} /></label>
      <p>Defaults allow 1 GB scanned per query and 10 GB per source/run, including a 2 GB reserve for delayed final data. Scan limits can cause partial evidence; preview queries also consume their bounded allowance.</p>
      <label><span>Legacy capture poll interval (seconds)</span><input type="number" min={10} max={300} value={draft.poll_interval_seconds} onChange={e => setDraft({ ...draft, poll_interval_seconds: Number(e.target.value) })} /></label>
      {draft.metrics_enabled && <fieldset><legend>Legacy capture defaults</legend><p>Native monitor sources own their metric selections. These optional defaults preserve existing connection-based capture.</p>{draft.metric_queries.map((metric, index) => <div className="capture-metric-row" key={index}><input aria-label={`Dynatrace metric ${index + 1} name`} placeholder="service_latency" value={metric.name} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, name: e.target.value } : m) })} /><input aria-label={`Dynatrace metric ${index + 1} selector`} placeholder="builtin:service.response.time" value={metric.metric_selector} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, metric_selector: e.target.value } : m) })} /><input aria-label={`Dynatrace metric ${index + 1} entity selector`} placeholder="type(SERVICE)" value={metric.entity_selector || ''} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, entity_selector: e.target.value } : m) })} /><select aria-label={`Dynatrace metric ${index + 1} aggregation`} value={metric.aggregation} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, aggregation: e.target.value as typeof metric.aggregation } : m) })}><option value="avg">Average</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum">Sum</option></select><button className="btn btn-secondary btn-sm" type="button" onClick={() => setDraft({ ...draft, metric_queries: draft.metric_queries.filter((_, i) => i !== index) })}>Remove</button></div>)}<button className="btn btn-secondary btn-sm" type="button" disabled={draft.metric_queries.length >= 10} onClick={() => setDraft({ ...draft, metric_queries: [...draft.metric_queries, { name: '', metric_selector: '', aggregation: 'avg' }] })}>Add metric</button></fieldset>}
      <label className="capture-checkbox"><span>Enable for new tests</span><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /></label>
    </div>{!canEnable && <p>Enabled connections are available for per-test capture. Native monitor sources also need the saved revision verified with Test saved connection; identity, capability, execution or budget changes require verification again.</p>}<div className="capture-actions"><button className="btn btn-primary btn-sm" type="button" disabled={busy || !draft.name.trim() || (draft.enabled && missing.length > 0)} onClick={() => void save()}>{busy ? 'Saving…' : draft.enabled ? 'Save connection' : 'Save draft'}</button>{<button className="btn btn-secondary btn-sm" type="button" onClick={resetDraft}>Discard draft</button>}</div></fieldset></section>}
  </div>
}
