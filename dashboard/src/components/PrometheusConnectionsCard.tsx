import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { usePrometheusConnectionsApi } from '../hooks/api/usePrometheusConnectionsApi'
import type { PrometheusConnection, PrometheusConnectionInput, PrometheusOperation } from '../types/prometheus'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { CaptureSetupHelp } from './CaptureConnectionShared'
import { settingsHref } from './settingsNavigation'
import { emptyPrometheusQuery, emptyPrometheusSelection, prometheusCanEnable, prometheusDraft, prometheusMissing, prometheusSecretFields } from './prometheusConnectionModel'
import { appDynamicsCapabilityHealth } from '../types/appdynamics'
import { getErrorMessage } from '../utils/errorUtils'

interface Props { projectId?: string; canManage?: boolean; summaryOnly?: boolean; canApprovePrivateNetworks?: boolean }
export default function PrometheusConnectionsCard(props: Props) { return <PrometheusEditor key={props.projectId || 'none'} {...props} /> }
function PrometheusEditor({ projectId, canManage = false, summaryOnly = false, canApprovePrivateNetworks = false }: Props) {
  const api = usePrometheusConnectionsApi()
  const [connections, setConnections] = useState<PrometheusConnection[]>([])
  const [editing, setEditing] = useState<PrometheusConnection | null>(null)
  const [draft, setDraft] = useState(prometheusDraft)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [deleting, setDeleting] = useState('')
  const [inspect, setInspect] = useState({ connection: '', kind: 'metrics', selector: '', label: '', expression: 'up', unit: 'boolean', allowedLabels: 'job,instance' })
  const [operation, setOperation] = useState<{ connection: string; value: PrometheusOperation }>()
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const load = useCallback(async () => {
    if (!projectId) return
    setLoading(true)
    try { const value = await api.listPrometheusConnections(projectId); if (mounted.current) { setConnections(value.connections || []); setError('') } }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to load Prometheus connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])
  useEffect(() => {
    if (!projectId || !operation || !['pending', 'queued', 'running'].includes(operation.value.status)) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => { void api.getPrometheusOperation(projectId, operation.connection, operation.value.id, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) { setOperation({ connection: operation.connection, value }); if (!['pending', 'queued', 'running'].includes(value.status)) void load() } }).catch(err => { if (!controller.signal.aborted) { setError(getErrorMessage(err, 'Prometheus inspection failed')); setOperation(undefined) } }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, operation, load])
  const reset = () => { setEditing(null); setDraft(prometheusDraft()); setOpen(false) }
  const missing = prometheusMissing(draft, editing)
  const verified = prometheusCanEnable(draft, editing)
  const save = async () => {
    if (!projectId || busy || !draft.name.trim() || (draft.enabled && (missing.length || !verified))) return false
    const payload = { ...draft }
    for (const key of prometheusSecretFields) payload[key] = draft[key]?.trim() || undefined
    setBusy(true); setError('')
    try {
      if (editing) await api.updatePrometheusConnection(projectId, editing.id, payload)
      else await api.createPrometheusConnection(projectId, payload)
      if (!mounted.current) return false
      reset(); setNotice('Connection saved. Test the saved revision before enabling it.'); await load(); return true
    } catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to save Prometheus connection')); return false }
    finally { if (mounted.current) setBusy(false) }
  }
  const dirty = open && JSON.stringify(draft) !== JSON.stringify(prometheusDraft(editing))
  const guardState = useRef({ dirty, save, reset }); guardState.current = { dirty, save, reset }
  const guard = useMemo(() => ({ id: 'prometheus-connection-draft', label: 'Prometheus connection draft', isDirty: () => guardState.current.dirty, save: () => guardState.current.save(), discard: () => guardState.current.reset() }), [])
  useNavigationGuard(guard, dirty)
  const start = async (connection: string, kind: 'test' | 'discover' | 'preview') => {
    if (!projectId || busy) return
    setBusy(true); setError('')
    const query = { ...emptyPrometheusQuery(), name: 'preview', expression: inspect.expression, unit: inspect.unit, allowed_labels: inspect.allowedLabels.split(/[\s,]+/).filter(Boolean), min_series: 1 }
    try {
      const value = await api.inspectPrometheusConnection(projectId, connection, kind, kind === 'discover' ? { kind: inspect.kind, selector: inspect.selector, label: inspect.label } : kind === 'preview' ? { selection: { ...emptyPrometheusSelection(), project_id: projectId, queries: [query] } } : {})
      if (mounted.current) setOperation({ connection, value })
    } catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to start Prometheus inspection')) }
    finally { if (mounted.current) setBusy(false) }
  }
  const config = (updates: Partial<PrometheusConnectionInput['config']>) => setDraft({ ...draft, enabled: false, config: { ...draft.config, ...updates } })
  if (!projectId) return <p>Select a project to configure Prometheus test monitoring.</p>
  return <div className="capture-connections" aria-label="Prometheus test monitoring connections">
    <p>Read target-system metrics from an approved Prometheus HTTP API. Collection runs directly from the controller or through the assigned collector. Tests opt in through monitor sources.</p>
    {loading && <p role="status">Loading Prometheus connections…</p>}{notice && <p role="status">{notice}</p>}
    {error && <p role="alert">{error} <button type="button" onClick={() => void load()}>Retry loading connections</button></p>}
    {!loading && !connections.length && !error && <p>No Prometheus test monitoring connections are configured for this project.</p>}
    {connections.map(connection => <div className="capture-connection-row" key={connection.id}><div><strong>{connection.name}</strong><p>{connection.enabled ? 'Enabled' : 'Disabled'} · {connection.collection_mode === 'collector' ? 'Assigned collector' : 'Direct collection'}</p>{canManage && <><p>{connection.config.endpoint}</p><p>Revision {connection.revision} · {connection.status || connection.last_check_status || 'Unverified'}{connection.last_check_message ? ` · ${connection.last_check_message}` : ''}</p>{connection.verified_revision !== connection.revision && <p>Current revision has not been verified.</p>}{connection.capabilities && <ul>{Object.entries(appDynamicsCapabilityHealth(connection.capabilities) || {}).map(([name, health]) => <li key={name}>{name}: {health.status}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}</>}</div>
      {canManage && !summaryOnly && <div className="capture-actions"><button type="button" disabled={busy || open} onClick={() => { setEditing(connection); setDraft(prometheusDraft(connection)); setOpen(true) }}>Edit</button><button type="button" disabled={busy || open || prometheusMissing(prometheusDraft(connection), connection).length > 0} onClick={() => void start(connection.id, 'test')}>Test saved connection</button><button type="button" disabled={busy || open} onClick={() => setInspect({ ...inspect, connection: connection.id })}>Discover and preview queries</button><button type="button" disabled={busy || open} onClick={() => setDeleting(connection.id)}>Delete</button>{deleting === connection.id && <div role="group" aria-label="Confirm Prometheus connection deletion"><p>Delete {connection.name}? Retained run snapshots remain available.</p><button type="button" disabled={busy} onClick={async () => { setBusy(true); try { await api.deletePrometheusConnection(projectId, connection.id, connection.revision); if (mounted.current) { setDeleting(''); await load() } } catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to delete connection')) } finally { if (mounted.current) setBusy(false) } }}>Confirm delete</button><button type="button" onClick={() => setDeleting('')}>Keep connection</button></div>}</div>}
    </div>)}
    {operation && <section aria-label="Prometheus inspection result"><p role="status">{operation.value.kind || 'Inspection'}: {operation.value.status}{operation.value.message ? ` · ${operation.value.message}` : ''}</p>{operation.value.error && <p role="alert">{operation.value.error}</p>}{operation.value.result?.message && <p>{operation.value.result.message}</p>}{operation.value.result?.truncated && <p role="status">Discovery or preview reached its result limit. Narrow the selector or query.</p>}{operation.value.result?.capabilities && <ul>{Object.entries(appDynamicsCapabilityHealth(operation.value.result.capabilities) || {}).map(([name, health]) => <li key={name}>{name}: {health.status}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}{operation.value.result?.coverage && <ul>{Object.entries(operation.value.result.coverage).map(([name, health]) => <li key={name}>{name}: {health.status}{health.truncated ? ' · incomplete coverage' : ''}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}{operation.value.result?.entities && <table><thead><tr><th>Name</th><th>Type</th><th>Labels / metadata</th></tr></thead><tbody>{operation.value.result.entities.map((entity, index) => <tr key={entity.id || `${entity.name}:${index}`}><td>{entity.name || entity.value || entity.id}</td><td>{entity.type || '—'}</td><td>{JSON.stringify(entity.entity_ids || entity.labels || {})}</td></tr>)}</tbody></table>}{operation.value.result?.points && <p>{operation.value.result.points.length} preview observations returned. Preview does not create a test or archive.</p>}</section>}
    {canManage && !summaryOnly && inspect.connection && <fieldset disabled={busy} aria-label="Prometheus discovery and preview"><legend>Discover target metrics and preview a query</legend><label>Discovery kind<select value={inspect.kind} onChange={event => setInspect({ ...inspect, kind: event.target.value })}>{['metrics', 'labels', 'label_values', 'series', 'targets', 'metadata'].map(kind => <option key={kind}>{kind}</option>)}</select></label><label>Discovery selector<input value={inspect.selector} placeholder={'{job="checkout"}'} onChange={event => setInspect({ ...inspect, selector: event.target.value })} /></label>{inspect.kind === 'label_values' && <label>Discovery label<input value={inspect.label} onChange={event => setInspect({ ...inspect, label: event.target.value })} /></label>}<button type="button" disabled={inspect.kind === 'label_values' && !inspect.label} onClick={() => void start(inspect.connection, 'discover')}>Discover</button><label>Preview PromQL<textarea value={inspect.expression} onChange={event => setInspect({ ...inspect, expression: event.target.value })} /></label><label>Preview unit<input value={inspect.unit} onChange={event => setInspect({ ...inspect, unit: event.target.value })} /></label><label>Preview allowed labels<input value={inspect.allowedLabels} onChange={event => setInspect({ ...inspect, allowedLabels: event.target.value })} /><small>Approve every label returned by the expression. Unexpected labels reject the preview.</small></label><button type="button" disabled={!inspect.expression.trim() || !inspect.unit.trim()} onClick={() => void start(inspect.connection, 'preview')}>Preview query</button><p>Verification and queries run at the saved execution location. Successful API access does not establish target freshness or complete metric coverage.</p></fieldset>}
    {summaryOnly && canManage && <a href={settingsHref('prometheus-capture')}>Manage Prometheus test monitoring connections</a>}
    {canManage && !summaryOnly && !open && <button type="button" onClick={() => { reset(); setOpen(true) }}>Add Prometheus connection</button>}
    {canManage && !summaryOnly && open && <section aria-label="Prometheus connection editor"><fieldset disabled={busy}><h3>{editing ? `Edit ${editing.name}` : 'Add Prometheus connection'}</h3><CaptureSetupHelp missing={missing} editing={Boolean(editing)} credentials={[editing?.has_token && 'Bearer token', editing?.has_username && 'Username', editing?.has_password && 'Password', editing?.has_client_secret && 'Azure client secret', editing?.has_client_certificate && 'Client certificate', editing?.has_client_key && 'Client key'].filter((value): value is string => Boolean(value))} />
      <label>Name (required)<input maxLength={256} value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label><label>Description<input value={draft.description || ''} onChange={event => setDraft({ ...draft, description: event.target.value })} /></label><label>Prometheus endpoint<input type="url" placeholder="https://metrics.example.com" value={draft.config.endpoint} onChange={event => config({ endpoint: event.target.value })} /></label><label>Backend profile<select value={draft.config.backend_profile} onChange={event => config({ backend_profile: event.target.value as typeof draft.config.backend_profile })}><option value="prometheus">Prometheus HTTP API</option><option value="azure_managed">Azure Monitor managed Prometheus</option></select></label><label>Authentication<select value={draft.config.auth_type} onChange={event => config({ auth_type: event.target.value as typeof draft.config.auth_type })}>{[['none', 'No authentication'], ['bearer', 'Bearer token'], ['basic', 'Basic authentication'], ['azure', 'Azure identity']].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      {draft.config.auth_type === 'azure' && <><label>Azure credential mode<select value={draft.config.azure_credential_mode || ''} onChange={event => config({ azure_credential_mode: event.target.value as 'workload_identity' | 'managed_identity' | 'service_principal' })}><option value="">Select an explicit identity mode</option><option value="workload_identity">Workload identity</option><option value="managed_identity">Managed identity</option><option value="service_principal">Service principal</option></select></label><label>Azure tenant ID<input value={draft.config.azure_tenant_id || ''} onChange={event => config({ azure_tenant_id: event.target.value })} /></label><label>Azure client ID<input value={draft.config.client_id || ''} onChange={event => config({ client_id: event.target.value })} /></label></>}
      <label>Prometheus context path (optional)<input value={draft.config.context_path || ''} placeholder="/prometheus" onChange={event => config({ context_path: event.target.value })} /></label><label><input type="checkbox" checked={Boolean(draft.config.mtls)} onChange={event => config({ mtls: event.target.checked })} />Use client certificate authentication</label>
      {prometheusSecretFields.filter(key => (draft.config.mtls && (key === 'client_certificate_secret' || key === 'client_key_secret')) || (draft.config.auth_type === 'bearer' && key === 'token_secret') || (draft.config.auth_type === 'basic' && ['username_secret', 'password_secret'].includes(key)) || (draft.config.auth_type === 'azure' && draft.config.azure_credential_mode === 'service_principal' && key === 'client_secret_secret')).map(key => <label key={key}>{({ token_secret: 'Bearer token', username_secret: 'Username', password_secret: 'Password', client_secret_secret: 'Azure client secret', client_certificate_secret: 'Client certificate', client_key_secret: 'Client key' })[key]} secret reference<input type="password" autoComplete="off" value={draft[key] || ''} onChange={event => setDraft({ ...draft, enabled: false, [key]: event.target.value })} /></label>)}
      <label>Collection location<select value={draft.collection_mode} onChange={event => setDraft({ ...draft, enabled: false, collection_mode: event.target.value as 'direct' | 'collector', collector_agent_id: event.target.value === 'direct' ? '' : draft.collector_agent_id })}><option value="direct">Direct from controller</option><option value="collector">Assigned collector</option></select></label>{draft.collection_mode === 'collector' && <label>Assigned collector ID<input value={draft.collector_agent_id || ''} onChange={event => setDraft({ ...draft, enabled: false, collector_agent_id: event.target.value })} /></label>}
      <label>Custom CA bundle (PEM)<textarea value={draft.config.ca_pem || ''} onChange={event => config({ ca_pem: event.target.value })} /><small>{editing?.has_custom_ca ? 'A saved CA bundle is present. Blank preserves it for the same identity.' : 'Hostname verification remains enabled.'}</small></label><label>Approved private CIDRs<textarea disabled={!canApprovePrivateNetworks} value={(draft.config.approved_cidrs || []).join('\n')} onChange={event => config({ approved_cidrs: event.target.value.split(/[\s,]+/).filter(Boolean) })} /><small>System administrators approve private-network trust. Saved trust details are hidden from public projections.</small></label><label><input type="checkbox" disabled={!canApprovePrivateNetworks} checked={Boolean(draft.config.allow_private_http)} onChange={event => config({ allow_private_http: event.target.checked })} />Allow unauthenticated HTTP on approved private networks</label>
      <label><input type="checkbox" disabled={!verified || missing.length > 0} checked={draft.enabled} onChange={event => setDraft({ ...draft, enabled: event.target.checked })} />Enabled after saved revision verification</label><p>Credential fields accept project secret references. Blank fields retain credentials only for the same endpoint and authentication identity. Changing collection or credentials requires verification again.</p><button type="button" disabled={!draft.name.trim() || (draft.enabled && (!verified || missing.length > 0))} onClick={() => void save()}>Save connection</button><button type="button" onClick={reset}>Cancel</button>
    </fieldset></section>}
  </div>
}
