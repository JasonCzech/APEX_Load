import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useELKConnectionsApi, type ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { ELKConnection } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

import { elkMissing } from './captureConnectionModel'
import { CaptureSetupHelp, CaptureCorrelationHelp, CaptureConnectionHealth } from './CaptureConnectionShared'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { settingsHref } from './settingsNavigation'

interface Props { projectId?: string; canManage?: boolean; summaryOnly?: boolean }

const emptyDraft = (): ELKConnectionInput => ({
  name: '', description: '', enabled: false, endpoint: '', index_pattern: '', timestamp_field: '@timestamp',
  level_field: 'log.level', message_field: 'message', test_id_field: '', trace_id_field: 'trace.id',
  base_filter: '', error_filter: 'log.level:(error OR fatal)', auth_type: '', metric_aggregations: [],
})

function draftFrom(connection: ELKConnection): ELKConnectionInput {
  return {
    name: connection.name, description: connection.description || '', enabled: connection.enabled,
    endpoint: connection.endpoint || '', index_pattern: connection.index_pattern || '',
    timestamp_field: connection.timestamp_field || '@timestamp', level_field: connection.level_field || '',
    message_field: connection.message_field || '', test_id_field: connection.test_id_field || '',
    trace_id_field: connection.trace_id_field || '', base_filter: connection.base_filter || '',
    error_filter: connection.error_filter || '', auth_type: connection.auth_type || '',
    metric_aggregations: connection.metric_aggregations || [], updated_at: connection.updated_at,
  }
}

export default function ELKConnectionsCard(props: Props) {
  return <ELKConnectionEditor key={props.projectId || 'none'} {...props} />
}

function ELKConnectionEditor({ projectId, canManage = false, summaryOnly = false }: Props) {
  const api = useELKConnectionsApi()
  const [connections, setConnections] = useState<ELKConnection[]>([])
  const [editing, setEditing] = useState<ELKConnection | null>(null)
  const [draft, setDraft] = useState<ELKConnectionInput>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [editorOpen, setEditorOpen] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const missing = elkMissing(draft, editing)
  const resetDraft = () => { setEditing(null); setDraft(emptyDraft()); setEditorOpen(false) }

  const load = useCallback(async () => {
    if (!projectId) { setConnections([]); setLoading(false); return }
    setLoading(true)
    try {
      const result = await api.listELKConnections(projectId)
      if (!mounted.current) return
      setConnections(Array.isArray(result?.connections) ? result.connections : [])
      setError('')
    }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Failed to load ELK connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])

  const save = async () => {
    if (!projectId || busy || !draft.name.trim() || (draft.enabled && missing.length > 0)) return false
    const payload = { ...draft, api_key_secret_name: draft.api_key_secret_name?.trim() || undefined, username_secret_name: draft.username_secret_name?.trim() || undefined, password_secret_name: draft.password_secret_name?.trim() || undefined }
    setBusy(true); setError('')
    try {
      if (editing) await api.updateELKConnection(projectId, editing.id, payload)
      else await api.createELKConnection(projectId, payload)
      if (!mounted.current) return false; resetDraft(); setNotice('Connection saved. Test saved configuration before enabling capture for a run.'); await load(); return true
    } catch (err) { setError(getErrorMessage(err, 'Failed to save ELK connection')); return false }
    finally { setBusy(false) }
  }

  const dirty = editorOpen && JSON.stringify(draft) !== JSON.stringify(editing ? draftFrom(editing) : emptyDraft())
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const saveRef = useRef(save)
  saveRef.current = save
  const resetRef = useRef(resetDraft)
  resetRef.current = resetDraft
  const guard = useMemo(() => ({
    id: 'elk-connection-draft',
    label: 'ELK connection draft',
    isDirty: () => dirtyRef.current,
    save: () => saveRef.current(),
    discard: () => resetRef.current(),
  }), [])
  useNavigationGuard(guard, dirty)

  if (!projectId) return <div><p>Select a project to configure Elasticsearch capture.</p></div>
  return <div className="capture-connections" aria-label="ELK connections">
    <p>Optional controller-side Elasticsearch capture. No connection is used unless a test explicitly opts in.</p>
    {notice && <p role="status">{notice}</p>}
    {loading && <p role="status">Loading connections…</p>}
    {error && <button type="button" className="btn btn-secondary" onClick={() => void load()}>Retry loading connections</button>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {connections.length === 0 && !loading && !error && <p>No ELK connections are configured for this project.</p>}
    {connections.map(connection => <div className="capture-connection-row" key={connection.id}>
      <div><strong>{connection.name}</strong><p>{canManage && `${connection.endpoint || 'Endpoint not configured'} · `}{connection.status}</p>
        {canManage && <CaptureConnectionHealth enabled={connection.enabled} missing={elkMissing(draftFrom(connection), connection)} checkedAt={connection.last_checked_at} updatedAt={connection.updated_at} status={connection.last_check_status} message={connection.last_check_message} />}</div>
      {canManage && !summaryOnly && <div className="capture-actions">
        <button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen} onClick={() => { setEditing(connection); setDraft(draftFrom(connection)); setEditorOpen(true); setNotice('') }}>Edit</button>
        <button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen || elkMissing(draftFrom(connection), connection).length > 0} onClick={async () => {
          setBusy(true); setError('')
          let failure = ''
          try { await api.testELKConnection(projectId, connection.id) }
          catch (err) { failure = getErrorMessage(err, 'Connection test failed') }
          finally { await load(); if (failure) setError(failure); setBusy(false) }
        }}>Test saved connection</button>
        <button className="btn btn-danger btn-sm" type="button" disabled={busy || editorOpen} onClick={() => setDeleting(connection.id)}>Delete</button>
        {deleting === connection.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {connection.name}? It will no longer be available for new tests. Existing run snapshots are unchanged.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteELKConnection(projectId, connection.id); if (!mounted.current) return; setDeleting(null); if (editing?.id === connection.id) resetDraft(); await load() } catch (err) { setError(getErrorMessage(err, 'Delete failed')) } finally { setBusy(false) } }}>Confirm delete</button><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setDeleting(null)}>Keep connection</button></div>}
      </div>}
    </div>)}
    {summaryOnly && canManage && <a className="btn btn-secondary" href={settingsHref('elk-capture')}>Manage Elastic connections</a>}
    {canManage && !summaryOnly && !editorOpen && <button type="button" className="btn btn-primary" onClick={() => { resetDraft(); setEditorOpen(true); setNotice('') }}>Add ELK connection</button>}
    {canManage && !summaryOnly && editorOpen && <section aria-label="ELK connection editor"><fieldset className="capture-editor" disabled={busy}>
      <h3>{editing ? `Edit ${editing.name}` : 'Add ELK connection'}</h3>
      <CaptureSetupHelp missing={missing} credentials={[editing?.has_api_key && 'API key', editing?.has_basic_username && 'Basic username', editing?.has_basic_password && 'Basic password'].filter((value): value is string => Boolean(value))} editing={Boolean(editing)} /><div className="capture-form-grid">
        <h4 className="capture-section-title">Basics</h4><label><span>Name (required)</span><input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
        <h4 className="capture-section-title">Endpoint and authentication</h4><label><span>Elasticsearch endpoint</span><input placeholder="https://elastic.example.com:9200" value={draft.endpoint} onChange={e => setDraft({ ...draft, endpoint: e.target.value })} /></label>
        <label><span>Index pattern</span><input placeholder="logs-*" value={draft.index_pattern} onChange={e => setDraft({ ...draft, index_pattern: e.target.value })} /></label>
        <label><span>Authentication</span><select value={draft.auth_type} onChange={e => setDraft({ ...draft, auth_type: e.target.value as ELKConnectionInput['auth_type'] })}><option value="">Not configured</option><option value="api_key">API key secret</option><option value="basic">Basic-auth secrets</option></select></label>
        {draft.auth_type === 'api_key' && <label><span>API key project-secret reference</span><input type="password" autoComplete="off" value={draft.api_key_secret_name || ''} onChange={e => setDraft({ ...draft, api_key_secret_name: e.target.value })} /></label>}
        {draft.auth_type === 'basic' && <><label><span>Username project-secret reference</span><input type="password" autoComplete="off" value={draft.username_secret_name || ''} onChange={e => setDraft({ ...draft, username_secret_name: e.target.value })} /></label><label><span>Password project-secret reference</span><input type="password" autoComplete="off" value={draft.password_secret_name || ''} onChange={e => setDraft({ ...draft, password_secret_name: e.target.value })} /></label></>}
        <h4 className="capture-section-title">Field mappings and correlation</h4><label><span>Timestamp field</span><input value={draft.timestamp_field} onChange={e => setDraft({ ...draft, timestamp_field: e.target.value })} /></label>
        <label><span>Level field</span><input value={draft.level_field} onChange={e => setDraft({ ...draft, level_field: e.target.value })} /></label>
        <label><span>Message field</span><input value={draft.message_field} onChange={e => setDraft({ ...draft, message_field: e.target.value })} /></label>
        <CaptureCorrelationHelp /><label><span>Test ID field (optional)</span><input value={draft.test_id_field} onChange={e => setDraft({ ...draft, test_id_field: e.target.value })} /></label>
        <label><span>Trace ID field</span><input value={draft.trace_id_field} onChange={e => setDraft({ ...draft, trace_id_field: e.target.value })} /></label>
        <h4 className="capture-section-title">Advanced filters (optional)</h4><label><span>Base query filter</span><input value={draft.base_filter} onChange={e => setDraft({ ...draft, base_filter: e.target.value })} /></label>
        <label><span>Error query filter</span><input value={draft.error_filter} onChange={e => setDraft({ ...draft, error_filter: e.target.value })} /></label>
        <fieldset><legend>Numeric aggregations</legend>
          {draft.metric_aggregations.map((metric, index) => <div className="capture-metric-row" key={index}>
            <input aria-label={`Metric ${index + 1} name`} placeholder="latency_avg" value={metric.name} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, name: e.target.value } : item) })} />
            <input aria-label={`Metric ${index + 1} field`} placeholder="duration_ms" value={metric.field} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, field: e.target.value } : item) })} />
            <select aria-label={`Metric ${index + 1} operation`} value={metric.operation} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, operation: e.target.value as typeof metric.operation } : item) })}><option value="avg">Average</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum">Sum</option></select>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.filter((_, itemIndex) => itemIndex !== index) })}>Remove</button>
          </div>)}
          <button className="btn btn-secondary btn-sm" type="button" disabled={draft.metric_aggregations.length >= 16} onClick={() => setDraft({ ...draft, metric_aggregations: [...draft.metric_aggregations, { name: '', field: '', operation: 'avg' }] })}>Add aggregation</button>
        </fieldset>
        <label className="capture-checkbox"><span>Enable for new tests</span><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /></label>
      </div>
      <div className="capture-actions"><button className="btn btn-primary btn-sm" type="button" disabled={busy || !draft.name.trim() || (draft.enabled && missing.length > 0)} onClick={() => void save()}>{busy ? 'Saving…' : draft.enabled ? 'Save connection' : 'Save draft'}</button>{<button className="btn btn-secondary btn-sm" type="button" onClick={resetDraft}>Discard draft</button>}</div>
    </fieldset></section>}
  </div>
}
