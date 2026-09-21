import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useDynatraceConnectionsApi, type DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceConnection } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

import { dynatraceMissing } from './captureConnectionModel'
import { CaptureSetupHelp, CaptureCorrelationHelp, CaptureConnectionHealth } from './CaptureConnectionShared'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { settingsHref } from './settingsNavigation'

interface Props { projectId?: string; canManage?: boolean; summaryOnly?: boolean }

const emptyDraft = (): DynatraceConnectionInput => ({
  name: '', description: '', enabled: false, environment_url: '', platform_url: '', metrics_enabled: false, problems_enabled: false, logs_enabled: false,
  platform_auth_type: '', oauth_resource: '', oauth_scopes: [], metric_queries: [], problem_selector: '', problem_entity_selector: '',
  log_base_filter: '', log_test_id_field: 'apex.test_id', log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: 'trace_id', poll_interval_seconds: 15,
})

function draftFrom(c: DynatraceConnection): DynatraceConnectionInput {
  return { name: c.name, description: c.description || '', enabled: c.enabled, environment_url: c.environment_url || '', platform_url: c.platform_url || '', metrics_enabled: c.metrics_enabled, problems_enabled: c.problems_enabled, logs_enabled: c.logs_enabled,
    platform_auth_type: c.platform_auth_type || '', oauth_resource: c.oauth_resource || '', oauth_scopes: c.oauth_scopes || [], metric_queries: c.metric_queries || [], problem_selector: c.problem_selector || '', problem_entity_selector: c.problem_entity_selector || '',
    log_base_filter: c.log_base_filter || '', log_test_id_field: c.log_test_id_field || '', log_timestamp_field: c.log_timestamp_field || 'timestamp', log_status_field: c.log_status_field || 'status', log_message_field: c.log_message_field || 'content', log_trace_id_field: c.log_trace_id_field || 'trace_id', poll_interval_seconds: c.poll_interval_seconds || 15, updated_at: c.updated_at }
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
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const missing = dynatraceMissing(draft, editing)
  const resetDraft = () => { setEditing(null); setDraft(emptyDraft()); setEditorOpen(false) }
  const load = useCallback(async () => {
    if (!projectId) { setConnections([]); setLoading(false); return }
    setLoading(true)
    try { const result = await api.listDynatraceConnections(projectId); if (!mounted.current) return; setConnections(result.connections || []); setError('') }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Failed to load Dynatrace connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])
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
    <p>Optional controller-side Dynatrace SaaS capture. Tests opt in explicitly; configuration changes never alter an active run.</p>
    {notice && <p role="status">{notice}</p>}
    {loading && <p role="status">Loading connections…</p>}
    {error && <button type="button" className="btn btn-secondary" onClick={() => void load()}>Retry loading connections</button>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {connections.length === 0 && !loading && !error && <p>No Dynatrace connections are configured for this project.</p>}
    {connections.map(c => <div className="capture-connection-row" key={c.id}><div><strong>{c.name}</strong><p>{canManage && `${c.environment_url || c.platform_url || 'Endpoints not configured'} · `}{c.status}</p>{canManage && <CaptureConnectionHealth enabled={c.enabled} missing={dynatraceMissing(draftFrom(c), c)} checkedAt={c.last_checked_at} updatedAt={c.updated_at} status={c.last_check_status} message={c.last_check_message} capabilities={c.last_capability_health} />}</div>
      {canManage && !summaryOnly && <div className="capture-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen} onClick={() => { setEditing(c); setDraft(draftFrom(c)); setEditorOpen(true); setNotice('') }}>Edit</button><button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen || dynatraceMissing(draftFrom(c), c).length > 0} onClick={async () => { setBusy(true); let failure = ''; try { await api.testDynatraceConnection(projectId, c.id) } catch (err) { failure = getErrorMessage(err, 'Connection test failed') } finally { await load(); if (failure) setError(failure); setBusy(false) } }}>Test saved connection</button><button className="btn btn-danger btn-sm" type="button" disabled={busy || editorOpen} onClick={() => setDeleting(c.id)}>Delete</button>
        {deleting === c.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {c.name}? It will no longer be available for new tests. Existing run snapshots are unchanged.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteDynatraceConnection(projectId, c.id); if (!mounted.current) return; setDeleting(null); if (editing?.id === c.id) resetDraft(); await load() } catch (err) { setError(getErrorMessage(err, 'Delete failed')) } finally { setBusy(false) } }}>Confirm delete</button><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setDeleting(null)}>Keep connection</button></div>}</div>}
    </div>)}
    {summaryOnly && canManage && <a className="btn btn-secondary" href={settingsHref('dynatrace-capture')}>Manage Dynatrace connections</a>}
    {canManage && !summaryOnly && !editorOpen && <button type="button" className="btn btn-primary" onClick={() => { resetDraft(); setEditorOpen(true); setNotice('') }}>Add Dynatrace connection</button>}
    {canManage && !summaryOnly && editorOpen && <section aria-label="Dynatrace connection editor"><fieldset className="capture-editor" disabled={busy}><h3>{editing ? `Edit ${editing.name}` : 'Add Dynatrace connection'}</h3><CaptureSetupHelp missing={missing} credentials={[editing?.has_environment_token && 'Environment token', editing?.has_platform_token && 'Platform token', editing?.has_oauth_client_id && 'OAuth client ID', editing?.has_oauth_client_secret && 'OAuth client secret'].filter((value): value is string => Boolean(value))} editing={Boolean(editing)} /><p>Choose what to capture first. Metrics and Problems use the Environment API; Grail logs use the Platform API and separate authentication.</p><div className="capture-form-grid">
      <h4 className="capture-section-title">Basics</h4><label><span>Name (required)</span><input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
      <label><span>Description</span><input value={draft.description} onChange={e => setDraft({ ...draft, description: e.target.value })} /></label>
      <h4 className="capture-section-title">Capture capabilities</h4>
      <label className="capture-checkbox"><span>Metrics API</span><input type="checkbox" checked={draft.metrics_enabled} onChange={e => setDraft({ ...draft, metrics_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Problems API</span><input type="checkbox" checked={draft.problems_enabled} onChange={e => setDraft({ ...draft, problems_enabled: e.target.checked })} /></label>
      <label className="capture-checkbox"><span>Grail logs</span><input type="checkbox" checked={draft.logs_enabled} onChange={e => setDraft({ ...draft, logs_enabled: e.target.checked })} /></label>
      <h4 className="capture-section-title">Capability-specific connection details</h4>
      {(draft.metrics_enabled || draft.problems_enabled) && <><label><span>Environment URL</span><input placeholder="https://abc.live.dynatrace.com" value={draft.environment_url} onChange={e => setDraft({ ...draft, environment_url: e.target.value })} /></label></>}
      {draft.logs_enabled && <><label><span>Platform URL</span><input placeholder="https://abc.apps.dynatrace.com" value={draft.platform_url} onChange={e => setDraft({ ...draft, platform_url: e.target.value })} /></label></>}
      {(draft.metrics_enabled || draft.problems_enabled) && <><label><span>Environment token project-secret reference</span><input type="password" autoComplete="off" value={draft.environment_token_secret || ''} onChange={e => setDraft({ ...draft, environment_token_secret: e.target.value })} /></label></>}
      {draft.logs_enabled && <><label><span>Platform authentication</span><select value={draft.platform_auth_type} onChange={e => setDraft({ ...draft, platform_auth_type: e.target.value as DynatraceConnectionInput['platform_auth_type'] })}><option value="">Not configured</option><option value="platform_token">Platform token</option><option value="oauth_client">OAuth client</option></select></label>
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
      <label><span>Poll interval (seconds)</span><input type="number" min={10} max={300} value={draft.poll_interval_seconds} onChange={e => setDraft({ ...draft, poll_interval_seconds: Number(e.target.value) })} /></label>
      {draft.metrics_enabled && <fieldset><legend>Metric selectors</legend>{draft.metric_queries.map((metric, index) => <div className="capture-metric-row" key={index}><input aria-label={`Dynatrace metric ${index + 1} name`} placeholder="service_latency" value={metric.name} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, name: e.target.value } : m) })} /><input aria-label={`Dynatrace metric ${index + 1} selector`} placeholder="builtin:service.response.time" value={metric.metric_selector} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, metric_selector: e.target.value } : m) })} /><input aria-label={`Dynatrace metric ${index + 1} entity selector`} placeholder="type(SERVICE)" value={metric.entity_selector || ''} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, entity_selector: e.target.value } : m) })} /><select aria-label={`Dynatrace metric ${index + 1} aggregation`} value={metric.aggregation} onChange={e => setDraft({ ...draft, metric_queries: draft.metric_queries.map((m, i) => i === index ? { ...m, aggregation: e.target.value as typeof metric.aggregation } : m) })}><option value="avg">Average</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum">Sum</option></select><button className="btn btn-secondary btn-sm" type="button" onClick={() => setDraft({ ...draft, metric_queries: draft.metric_queries.filter((_, i) => i !== index) })}>Remove</button></div>)}<button className="btn btn-secondary btn-sm" type="button" disabled={draft.metric_queries.length >= 10} onClick={() => setDraft({ ...draft, metric_queries: [...draft.metric_queries, { name: '', metric_selector: '', aggregation: 'avg' }] })}>Add metric</button></fieldset>}
      <label className="capture-checkbox"><span>Enable for new tests</span><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /></label>
    </div><div className="capture-actions"><button className="btn btn-primary btn-sm" type="button" disabled={busy || !draft.name.trim() || (draft.enabled && missing.length > 0)} onClick={() => void save()}>{busy ? 'Saving…' : draft.enabled ? 'Save connection' : 'Save draft'}</button>{<button className="btn btn-secondary btn-sm" type="button" onClick={resetDraft}>Discard draft</button>}</div></fieldset></section>}
  </div>
}
