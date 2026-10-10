import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useELKConnectionsApi, type ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { ELKConnection } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

import { elkCanEnable, elkMissing } from './captureConnectionModel'
import { CaptureSetupHelp, CaptureCorrelationHelp, CaptureConnectionHealth } from './CaptureConnectionShared'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { settingsHref } from './settingsNavigation'
import type { ElasticConfig } from '../types/elastic'
import { elasticCapabilityHealth, elasticConnectionVerified, safeElasticDashboardsLink } from './elasticMonitoringModel'
import { useElasticInspection } from './useElasticInspection'
import ElasticInspectionResult from './ElasticInspectionResult'
import { request } from '../hooks/apiClient'

// canView says the session may list the project's connections (its editors
// and administrators); others are not asked for a list the controller refuses.
interface Props { projectId?: string; canManage?: boolean; canView?: boolean; summaryOnly?: boolean; canApprovePrivateNetworks?: boolean }

const emptyDraft = (): ELKConnectionInput => ({
  name: '', description: '', enabled: false, endpoint: '', index_pattern: '', timestamp_field: '@timestamp',
  level_field: 'log.level', message_field: 'message', test_id_field: '', trace_id_field: 'trace.id',
  base_filter: '', error_filter: 'log.level:(error OR fatal)', auth_type: '', metric_aggregations: [],
  product: 'elasticsearch', collection_mode: 'direct', config: { product: 'elasticsearch', endpoint: '', auth_type: '', timeout_seconds: 10, requests_per_minute: 60 },
})

function draftFrom(connection: ELKConnection): ELKConnectionInput {
  const expanded = Boolean(connection.product || connection.config?.product)
  return {
    name: connection.name, description: connection.description || '', enabled: connection.enabled,
    endpoint: connection.config?.endpoint || connection.endpoint || '', index_pattern: connection.index_pattern || '',
    timestamp_field: connection.timestamp_field || '@timestamp', level_field: connection.level_field || '',
    message_field: connection.message_field || '', test_id_field: connection.test_id_field || '',
    trace_id_field: connection.trace_id_field || '', base_filter: connection.base_filter || '',
    error_filter: connection.error_filter || '', auth_type: connection.config?.auth_type || connection.auth_type || '',
    metric_aggregations: connection.metric_aggregations || [], updated_at: connection.updated_at,
    product: connection.product || connection.config?.product || undefined, collection_mode: connection.collection_mode || 'direct', collector_agent_id: connection.collector_agent_id,
    revision: connection.revision, config: expanded && connection.config ? { ...connection.config, ca_pem: undefined, approved_cidrs: undefined, kibana_ca_pem: undefined, kibana_approved_cidrs: undefined, logstash_ca_pem: undefined, logstash_approved_cidrs: undefined } : undefined,
    kibana_auth_type: connection.kibana_auth_type, logstash_auth_type: connection.logstash_auth_type,
  }
}

export default function ELKConnectionsCard(props: Props) {
  return <ELKConnectionEditor key={props.projectId || 'none'} {...props} />
}

function ELKConnectionEditor({ projectId, canManage = false, canView = true, summaryOnly = false, canApprovePrivateNetworks = false }: Props) {
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
  const [collectors, setCollectors] = useState<{ id: string; name: string; health: string }[]>([])
  const inspection = useElasticInspection(projectId)
  const config: ElasticConfig = draft.config || { product: draft.product || 'elasticsearch', endpoint: draft.endpoint, auth_type: draft.auth_type }
  const changeConfig = (fields: Partial<ElasticConfig>) => setDraft({ ...draft, config: { ...config, ...fields } })
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const missing = elkMissing(draft, editing)
  const canEnable = elkCanEnable(draft, editing)
  const resetDraft = () => { setEditing(null); setDraft(emptyDraft()); setEditorOpen(false) }

  const load = useCallback(async () => {
    if (!projectId || !canView) { setConnections([]); setLoading(false); return }
    setLoading(true)
    try {
      const result = await api.listELKConnections(projectId)
      if (!mounted.current) return
      setConnections(Array.isArray(result?.connections) ? result.connections : [])
      setError('')
    }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Failed to load ELK connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, canView, projectId])
  useEffect(() => { void load() }, [load])
  useEffect(() => {
    const controller = new AbortController()
    if (projectId && canManage && !summaryOnly) void request<{ collectors: typeof collectors }>(`/monitoring/collectors?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setCollectors(result.collectors || []) }).catch(() => {})
    return () => controller.abort()
  }, [projectId, canManage, summaryOnly])
  useEffect(() => { if (inspection.operation && !['pending', 'queued', 'running'].includes(inspection.operation.status)) void load() }, [inspection.operation, load])

  const save = async () => {
    if (!projectId || busy || !draft.name.trim() || (draft.enabled && (missing.length > 0 || !canEnable))) return false
    const payload = { ...draft, api_key_secret_name: draft.api_key_secret_name?.trim() || undefined, username_secret_name: draft.username_secret_name?.trim() || undefined, password_secret_name: draft.password_secret_name?.trim() || undefined, bearer_token_secret_name: draft.bearer_token_secret_name?.trim() || undefined, client_certificate_secret_name: draft.client_certificate_secret_name?.trim() || undefined, client_key_secret_name: draft.client_key_secret_name?.trim() || undefined }
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
  if (!canView) return <div className="capture-connections" aria-label="ELK connections"><p>The project's Elastic connections are shown to its editors and administrators.</p></div>
  return <div className="capture-connections" aria-label="ELK connections">
    <p>Connect Elasticsearch or OpenSearch over HTTPS, directly from the controller or through an assigned collector. Tests select reusable sources for logs, metrics, traces, errors, and component health.</p>
    {notice && <p role="status">{notice}</p>}
    {loading && <p role="status">Loading connections…</p>}
    {error && <button type="button" className="btn btn-secondary" onClick={() => void load()}>Retry loading connections</button>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {connections.length === 0 && !loading && !error && <p>No ELK connections are configured for this project.</p>}
    {connections.map(connection => <div className="capture-connection-row" key={connection.id}>
      <div><strong>{connection.name}</strong><p>{canManage && `${connection.endpoint || 'Endpoint not configured'} · `}{connection.status}</p>
        {canManage && <CaptureConnectionHealth enabled={connection.enabled} missing={elkMissing(draftFrom(connection), connection)} checkedAt={connection.last_checked_at} updatedAt={connection.updated_at} status={connection.last_check_status} message={connection.last_check_message} capabilities={elasticCapabilityHealth(connection.capabilities)} />}
        {canManage && connection.revision && <p>Revision {connection.revision} · {connection.verified_revision === connection.revision ? 'Verified' : 'Current revision needs verification'} · {connection.collection_mode === 'collector' ? 'Assigned collector' : 'Direct collection'}</p>}
        {canManage && connection.capabilities && <p>{connection.capabilities.product} {connection.capabilities.version || ''}{connection.capabilities.serverless ? ' · serverless' : ''}</p>}
        {canManage && elasticConnectionVerified(connection) && safeElasticDashboardsLink(connection.config?.dashboards_url) && <a href={safeElasticDashboardsLink(connection.config?.dashboards_url)} target="_blank" rel="noopener noreferrer">Open approved Kibana / OpenSearch Dashboards</a>}</div>
      {canManage && !summaryOnly && <div className="capture-actions">
        <button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen} onClick={() => { setEditing(connection); setDraft(draftFrom(connection)); setEditorOpen(true); setNotice('') }}>Edit</button>
        <button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen || elkMissing(draftFrom(connection), connection).length > 0} onClick={async () => {
          setBusy(true); setError('')
          let failure = ''
          try { if ((connection.product || connection.config?.product) && connection.revision) await inspection.start(connection.id, { kind: 'verify', expected_revision: connection.revision }); else await api.testELKConnection(projectId, connection.id) }
          catch (err) { failure = getErrorMessage(err, 'Connection test failed') }
          finally { await load(); if (failure) setError(failure); setBusy(false) }
        }}>Test saved connection</button>
        <button className="btn btn-secondary btn-sm" type="button" disabled={busy || editorOpen || inspection.busy || !connection.revision} onClick={() => void inspection.start(connection.id, { kind: 'discover_indices', expected_revision: connection.revision, limit: 100 })}>Discover indices</button>
        <button className="btn btn-danger btn-sm" type="button" disabled={busy || editorOpen} onClick={() => setDeleting(connection.id)}>Delete</button>
        {deleting === connection.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {connection.name}? It will no longer be available for new tests. Existing run snapshots are unchanged.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteELKConnection(projectId, connection.id); if (!mounted.current) return; setDeleting(null); if (editing?.id === connection.id) resetDraft(); await load() } catch (err) { setError(getErrorMessage(err, 'Delete failed')) } finally { setBusy(false) } }}>Confirm delete</button><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setDeleting(null)}>Keep connection</button></div>}
      </div>}
    </div>)}
    {inspection.error && <p role="alert">{inspection.error}</p>}
    <ElasticInspectionResult operation={inspection.operation} onCancel={() => void inspection.cancel()} />
    {summaryOnly && canManage && <a className="btn btn-secondary" href={settingsHref('elk-capture')}>Manage Elastic connections</a>}
    {canManage && !summaryOnly && !editorOpen && <button type="button" className="btn btn-primary" onClick={() => { resetDraft(); setEditorOpen(true); setNotice('') }}>Add ELK connection</button>}
    {canManage && !summaryOnly && editorOpen && <section aria-label="ELK connection editor"><fieldset className="capture-editor" disabled={busy}>
      <h3>{editing ? `Edit ${editing.name}` : 'Add ELK connection'}</h3>
      <CaptureSetupHelp missing={missing} credentials={[editing?.has_api_key && 'API key', editing?.has_basic_username && 'Basic username', editing?.has_basic_password && 'Basic password', editing?.has_bearer_token && 'Bearer token', editing?.has_client_certificate && 'Client certificate', editing?.has_client_key && 'Client key'].filter((value): value is string => Boolean(value))} editing={Boolean(editing)} /><div className="capture-form-grid">
        <h4 className="capture-section-title">Basics</h4><label><span>Name (required)</span><input value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
        <h4 className="capture-section-title">Endpoint and authentication</h4>
        <label><span>Product</span><select value={config.product} onChange={event => setDraft({ ...draft, product: event.target.value as ElasticConfig['product'], config: { ...config, product: event.target.value as ElasticConfig['product'] } })}><option value="elasticsearch">Elasticsearch</option><option value="opensearch">OpenSearch</option></select></label>
        <label><span>Collection location</span><select value={draft.collection_mode || 'direct'} onChange={event => setDraft({ ...draft, collection_mode: event.target.value as 'direct' | 'collector', collector_agent_id: '' })}><option value="direct">Direct from controller</option><option value="collector">Assigned collector</option></select></label>
        {draft.collection_mode === 'collector' && <label><span>Assigned Elastic collector</span><select value={draft.collector_agent_id || ''} onChange={event => setDraft({ ...draft, collector_agent_id: event.target.value })}><option value="">Select a collector</option>{draft.collector_agent_id && !collectors.some(collector => collector.id === draft.collector_agent_id) && <option value={draft.collector_agent_id}>Saved collector · {draft.collector_agent_id}</option>}{collectors.map(collector => <option value={collector.id} key={collector.id}>{collector.name} · {collector.health}</option>)}</select></label>}<label><span>Elasticsearch / OpenSearch endpoint</span><input placeholder="https://elastic.example.com:9200" value={draft.endpoint} onChange={e => setDraft({ ...draft, endpoint: e.target.value, config: { ...config, endpoint: e.target.value } })} /></label>
        <label><span>Index pattern</span><input placeholder="logs-*" value={draft.index_pattern} onChange={e => setDraft({ ...draft, index_pattern: e.target.value })} /></label>
        <label><span>Authentication</span><select value={draft.auth_type} onChange={e => setDraft({ ...draft, auth_type: e.target.value as ELKConnectionInput['auth_type'], config: { ...config, auth_type: e.target.value as ElasticConfig['auth_type'] } })}><option value="">Not configured</option><option value="api_key" disabled={config.product === 'opensearch'}>API key secret</option><option value="basic">Basic-auth secrets</option><option value="bearer">Bearer-token secret</option><option value="mtls">Client-certificate secrets</option></select></label>
        {draft.auth_type === 'api_key' && <label><span>API key project-secret reference</span><input type="password" autoComplete="off" value={draft.api_key_secret_name || ''} onChange={e => setDraft({ ...draft, api_key_secret_name: e.target.value })} /></label>}
        {draft.auth_type === 'basic' && <><label><span>Username project-secret reference</span><input type="password" autoComplete="off" value={draft.username_secret_name || ''} onChange={e => setDraft({ ...draft, username_secret_name: e.target.value })} /></label><label><span>Password project-secret reference</span><input type="password" autoComplete="off" value={draft.password_secret_name || ''} onChange={e => setDraft({ ...draft, password_secret_name: e.target.value })} /></label></>}
        {draft.auth_type === 'bearer' && <label><span>Bearer token project-secret reference</span><input type="password" autoComplete="off" value={draft.bearer_token_secret_name || ''} onChange={event => setDraft({ ...draft, bearer_token_secret_name: event.target.value })} /></label>}
        {draft.auth_type && <><label><span>Client certificate project-secret reference (optional with other authentication)</span><input type="password" autoComplete="off" value={draft.client_certificate_secret_name || ''} onChange={event => setDraft({ ...draft, client_certificate_secret_name: event.target.value })} /></label><label><span>Client key project-secret reference (optional with other authentication)</span><input type="password" autoComplete="off" value={draft.client_key_secret_name || ''} onChange={event => setDraft({ ...draft, client_key_secret_name: event.target.value })} /></label></>}
        <label><span>Approved private CIDRs (one per line)</span><textarea disabled={!canApprovePrivateNetworks} placeholder={editing?.has_approved_private_networks ? 'Saved network approvals are retained unless replaced' : 'System-approved private network ranges'} value={(config.approved_cidrs || []).join('\n')} onChange={event => changeConfig({ approved_cidrs: event.target.value.split(/\s+/).filter(Boolean) })} /></label>
        {!canApprovePrivateNetworks && <p>System administrators approve private network changes.</p>}
        <label><span>Trusted CA PEM</span><textarea placeholder={editing?.has_custom_ca ? 'Saved CA retained unless replaced' : 'Optional custom CA certificate'} value={config.ca_pem || ''} onChange={event => changeConfig({ ca_pem: event.target.value })} /></label>
        <label><span>Kibana HTTPS endpoint (optional)</span><input type="url" value={config.kibana_endpoint || ''} onChange={event => changeConfig({ kibana_endpoint: event.target.value || undefined })} /></label>
        <label><span>Logstash HTTPS endpoint (optional)</span><input type="url" value={config.logstash_endpoint || ''} onChange={event => changeConfig({ logstash_endpoint: event.target.value || undefined })} /></label>
        <label><span>Approved Kibana / OpenSearch Dashboards URL (optional)</span><input type="url" placeholder="https://dashboards.example.com" value={config.dashboards_url || ''} onChange={event => changeConfig({ dashboards_url: event.target.value || undefined })} /><small>Save an explicit HTTPS navigation destination without credentials, query parameters or fragments. This is separate from component health endpoints.</small></label>
        {(['kibana', 'logstash'] as const).filter(component => config[`${component}_endpoint`]).map(component => {
          const auth = draft[`${component}_auth_type`] || ''
          const fields = auth === 'api_key' ? ['api_key'] : auth === 'basic' ? ['username', 'password'] : auth === 'bearer' ? ['bearer_token'] : auth === 'mtls' ? ['client_certificate', 'client_key'] : []
          return <fieldset key={component}><legend>{component === 'kibana' ? 'Kibana' : 'Logstash'} separate credentials</legend><p>{editing?.[`has_${component}_credentials`] ? 'Saved component credentials retained unless replaced.' : 'Select credentials for this component endpoint.'}</p><label>{component} authentication<select value={auth} onChange={event => setDraft({ ...draft, [`${component}_auth_type`]: event.target.value })}><option value="">Not configured</option>{['api_key', 'basic', 'bearer', 'mtls'].map(method => <option value={method} key={method}>{method}</option>)}</select></label>{fields.map(field => {
            const key = `${component}_${field}_secret_name` as keyof ELKConnectionInput
            return <label key={field}>{component} {field.replace(/_/g, ' ')} project-secret reference<input type="password" autoComplete="off" value={typeof draft[key] === 'string' ? draft[key] as string : ''} onChange={event => setDraft({ ...draft, [key]: event.target.value || undefined })} /></label>
          })}<label>{component} approved private CIDRs<textarea disabled={!canApprovePrivateNetworks} value={(config[`${component}_approved_cidrs`] || []).join('\n')} onChange={event => changeConfig({ [`${component}_approved_cidrs`]: event.target.value.split(/\s+/).filter(Boolean) })} /></label><label>{component} trusted CA PEM<textarea value={config[`${component}_ca_pem`] || ''} placeholder="Saved trust is retained for the same endpoint unless replaced" onChange={event => changeConfig({ [`${component}_ca_pem`]: event.target.value })} /></label></fieldset>
        })}
        {(['timeout_seconds', 'requests_per_minute', 'max_response_bytes', 'max_points', 'max_evidence', 'max_series'] as const).map(field => <label key={field}><span>{field.replace(/_/g, ' ')}</span><input type="number" min={1} value={config[field] || ''} onChange={event => changeConfig({ [field]: event.target.value ? Number(event.target.value) : undefined })} /></label>)}
        <h4 className="capture-section-title">Legacy capture defaults and correlation (optional)</h4><label><span>Timestamp field</span><input value={draft.timestamp_field} onChange={e => setDraft({ ...draft, timestamp_field: e.target.value })} /></label>
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
        <label className="capture-checkbox"><span>Enable for new tests</span><input type="checkbox" checked={draft.enabled} disabled={!canEnable && !draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /></label>
      </div>
      <div className="capture-actions"><button className="btn btn-primary btn-sm" type="button" disabled={busy || !draft.name.trim() || (draft.enabled && (missing.length > 0 || !canEnable))} onClick={() => void save()}>{busy ? 'Saving…' : draft.enabled ? 'Save connection' : 'Save draft'}</button>{<button className="btn btn-secondary btn-sm" type="button" onClick={resetDraft}>Discard draft</button>}</div>
    {!canEnable && <p>Save a disabled draft, verify its saved revision, then enable it for future sources and runs. Identity or trust changes require fresh verification and credentials.</p>}</fieldset></section>}
  </div>
}
