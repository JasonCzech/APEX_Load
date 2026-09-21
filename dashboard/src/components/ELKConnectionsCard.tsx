import { useCallback, useEffect, useState } from 'react'

import { useELKConnectionsApi, type ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { ELKConnection } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

interface Props { projectId?: string; canManage?: boolean }

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

export default function ELKConnectionsCard({ projectId, canManage = false }: Props) {
  const api = useELKConnectionsApi()
  const [connections, setConnections] = useState<ELKConnection[]>([])
  const [editing, setEditing] = useState<ELKConnection | null>(null)
  const [draft, setDraft] = useState<ELKConnectionInput>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    if (!projectId) { setConnections([]); return }
    try {
      const result = await api.listELKConnections(projectId)
      setConnections(Array.isArray(result?.connections) ? result.connections : [])
      setError('')
    }
    catch (err) { setError(getErrorMessage(err, 'Failed to load ELK connections')) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])

  const save = async () => {
    if (!projectId) return
    setBusy(true); setError('')
    try {
      if (editing) await api.updateELKConnection(projectId, editing.id, draft)
      else await api.createELKConnection(projectId, draft)
      setEditing(null); setDraft(emptyDraft()); await load()
    } catch (err) { setError(getErrorMessage(err, 'Failed to save ELK connection')) }
    finally { setBusy(false) }
  }

  if (!projectId) return <div><p>Select a project to configure Elasticsearch capture.</p></div>
  return <div className="monitor-detail-list" aria-label="ELK connections">
    <p>Optional controller-side Elasticsearch capture. No connection is used unless a test explicitly opts in.</p>
    {error && <p className="form-error" role="alert">{error}</p>}
    {connections.length === 0 && !canManage && <p>No ELK connections are configured for this project.</p>}
    {connections.map(connection => <div className="monitor-detail-row" key={connection.id}>
      <div><strong>{connection.name}</strong><p>{connection.endpoint || 'Endpoint not configured'} · {connection.status}</p>
        {connection.last_check_message && <small>{connection.last_check_message}</small>}</div>
      {canManage && <div className="settings-inline-actions">
        <button className="btn btn-secondary btn-sm" type="button" onClick={() => { setEditing(connection); setDraft(draftFrom(connection)) }}>Edit</button>
        <button className="btn btn-secondary btn-sm" type="button" disabled={connection.status !== 'ready' || busy} onClick={async () => {
          setBusy(true); setError('')
          let failure = ''
          try { await api.testELKConnection(projectId, connection.id) }
          catch (err) { failure = getErrorMessage(err, 'Connection test failed') }
          finally { await load(); if (failure) setError(failure); setBusy(false) }
        }}>Test</button>
        <button className="btn btn-danger btn-sm" type="button" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteELKConnection(projectId, connection.id); await load() } catch (err) { setError(getErrorMessage(err, 'Delete failed')) } finally { setBusy(false) } }}>Delete</button>
      </div>}
    </div>)}
    {canManage && <details open={connections.length === 0 || Boolean(editing)}>
      <summary>{editing ? `Edit ${editing.name}` : 'Add ELK connection'}</summary>
      <div className="form-grid">
        <label><span>Name</span><input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
        <label><span>Elasticsearch endpoint</span><input placeholder="https://elastic.example.com:9200" value={draft.endpoint} onChange={e => setDraft({ ...draft, endpoint: e.target.value })} /></label>
        <label><span>Index pattern</span><input placeholder="logs-*" value={draft.index_pattern} onChange={e => setDraft({ ...draft, index_pattern: e.target.value })} /></label>
        <label><span>Authentication</span><select value={draft.auth_type} onChange={e => setDraft({ ...draft, auth_type: e.target.value as ELKConnectionInput['auth_type'] })}><option value="">Not configured</option><option value="api_key">API key secret</option><option value="basic">Basic-auth secrets</option></select></label>
        {draft.auth_type === 'api_key' && <label><span>API key project-secret reference</span><input type="password" autoComplete="off" value={draft.api_key_secret_name || ''} onChange={e => setDraft({ ...draft, api_key_secret_name: e.target.value })} /></label>}
        {draft.auth_type === 'basic' && <><label><span>Username project-secret reference</span><input type="password" autoComplete="off" value={draft.username_secret_name || ''} onChange={e => setDraft({ ...draft, username_secret_name: e.target.value })} /></label><label><span>Password project-secret reference</span><input type="password" autoComplete="off" value={draft.password_secret_name || ''} onChange={e => setDraft({ ...draft, password_secret_name: e.target.value })} /></label></>}
        <label><span>Timestamp field</span><input value={draft.timestamp_field} onChange={e => setDraft({ ...draft, timestamp_field: e.target.value })} /></label>
        <label><span>Level field</span><input value={draft.level_field} onChange={e => setDraft({ ...draft, level_field: e.target.value })} /></label>
        <label><span>Message field</span><input value={draft.message_field} onChange={e => setDraft({ ...draft, message_field: e.target.value })} /></label>
        <label><span>Test ID field</span><input value={draft.test_id_field} onChange={e => setDraft({ ...draft, test_id_field: e.target.value })} /></label>
        <label><span>Trace ID field</span><input value={draft.trace_id_field} onChange={e => setDraft({ ...draft, trace_id_field: e.target.value })} /></label>
        <label><span>Base query filter</span><input value={draft.base_filter} onChange={e => setDraft({ ...draft, base_filter: e.target.value })} /></label>
        <label><span>Error query filter</span><input value={draft.error_filter} onChange={e => setDraft({ ...draft, error_filter: e.target.value })} /></label>
        <fieldset><legend>Numeric aggregations</legend>
          {draft.metric_aggregations.map((metric, index) => <div className="settings-inline-actions" key={`${metric.name}-${index}`}>
            <input aria-label={`Metric ${index + 1} name`} placeholder="latency_avg" value={metric.name} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, name: e.target.value } : item) })} />
            <input aria-label={`Metric ${index + 1} field`} placeholder="duration_ms" value={metric.field} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, field: e.target.value } : item) })} />
            <select aria-label={`Metric ${index + 1} operation`} value={metric.operation} onChange={e => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.map((item, itemIndex) => itemIndex === index ? { ...item, operation: e.target.value as typeof metric.operation } : item) })}><option value="avg">Average</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum">Sum</option></select>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => setDraft({ ...draft, metric_aggregations: draft.metric_aggregations.filter((_, itemIndex) => itemIndex !== index) })}>Remove</button>
          </div>)}
          <button className="btn btn-secondary btn-sm" type="button" disabled={draft.metric_aggregations.length >= 16} onClick={() => setDraft({ ...draft, metric_aggregations: [...draft.metric_aggregations, { name: '', field: '', operation: 'avg' }] })}>Add aggregation</button>
        </fieldset>
        <label className="setting-checkbox-row"><span>Ready for test selection</span><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /></label>
      </div>
      <div className="settings-inline-actions"><button className="btn btn-primary btn-sm" type="button" disabled={busy || !draft.name.trim()} onClick={() => void save()}>{busy ? 'Saving…' : 'Save connection'}</button>{editing && <button className="btn btn-secondary btn-sm" type="button" onClick={() => { setEditing(null); setDraft(emptyDraft()) }}>Cancel</button>}</div>
    </details>}
  </div>
}
