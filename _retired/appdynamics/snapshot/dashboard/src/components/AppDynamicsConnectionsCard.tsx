import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useAppDynamicsConnectionsApi } from '../hooks/api/useAppDynamicsConnectionsApi'
import { appDynamicsCapabilityHealth, type AppDynamicsConnection, type AppDynamicsConnectionInput, type AppDynamicsOperation } from '../types/appdynamics'
import { getErrorMessage } from '../utils/errorUtils'
import { useNavigationGuard } from '../hooks/NavigationGuardProvider'
import { CaptureSetupHelp, CaptureConnectionHealth } from './CaptureConnectionShared'
import { settingsHref } from './settingsNavigation'
import { appDynamicsCanEnable, appDynamicsDraft, appDynamicsMissing } from './appDynamicsConnectionModel'


interface Props { projectId?: string; canManage?: boolean; summaryOnly?: boolean; canApprovePrivateNetworks?: boolean }
export default function AppDynamicsConnectionsCard(props: Props) { return <AppDynamicsEditor key={props.projectId || 'none'} {...props} /> }
function AppDynamicsEditor({ projectId, canManage = false, summaryOnly = false, canApprovePrivateNetworks = false }: Props) {
  const api = useAppDynamicsConnectionsApi()
  const [connections, setConnections] = useState<AppDynamicsConnection[]>([])
  const [editing, setEditing] = useState<AppDynamicsConnection | null>(null)
  const [draft, setDraft] = useState(appDynamicsDraft)
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [deleting, setDeleting] = useState('')
  const [operation, setOperation] = useState<{ connection: string; value: AppDynamicsOperation }>()
  const [discovery, setDiscovery] = useState({ connection: '', kind: 'applications' as 'applications' | 'tiers' | 'nodes' | 'business-transactions' | 'backends' | 'metrics', application: '', path: '', search: '' })
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const load = useCallback(async () => {
    if (!projectId) return
    setLoading(true)
    try { const result = await api.listAppDynamicsConnections(projectId); if (mounted.current) { setConnections(result.connections || []); setError('') } }
    catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to load AppDynamics connections')) }
    finally { if (mounted.current) setLoading(false) }
  }, [api, projectId])
  useEffect(() => { void load() }, [load])
  useEffect(() => {
    if (!projectId || !operation || !['pending', 'queued', 'running'].includes(operation.value.status)) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      void api.getAppDynamicsOperation(projectId, operation.connection, operation.value.id, { signal: controller.signal }).then(value => {
        if (controller.signal.aborted) return
        setOperation({ connection: operation.connection, value })
        if (!['pending', 'queued', 'running'].includes(value.status)) void load()
      }).catch(err => { if (!controller.signal.aborted) { setError(getErrorMessage(err, 'AppDynamics operation failed')); setOperation(undefined) } })
    }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, operation, load])
  const discoveredEntities = (operation?.value.result?.entities || []).filter(entity => !discovery.search.trim() || `${entity.name} ${entity.id} ${entity.path || ''}`.toLowerCase().includes(discovery.search.trim().toLowerCase()))
  const missing = appDynamicsMissing(draft, editing)
  const canEnable = appDynamicsCanEnable(draft, editing)
  const reset = () => { setEditing(null); setDraft(appDynamicsDraft()); setOpen(false) }
  const save = async () => {
    if (!projectId || busy || !draft.name.trim() || (draft.enabled && (missing.length || !canEnable))) return false
    const payload = { ...draft }
    for (const key of ['client_id_secret', 'client_secret_secret', 'username_secret', 'password_secret', 'events_api_key_secret'] as const) payload[key] = draft[key]?.trim() || undefined
    setBusy(true); setError('')
    try {
      if (editing) await api.updateAppDynamicsConnection(projectId, editing.id, payload)
      else await api.createAppDynamicsConnection(projectId, payload)
      if (!mounted.current) return false
      reset(); setNotice('Connection saved. Test the saved connection before using it in a monitor source.'); await load(); return true
    } catch (err) { setError(getErrorMessage(err, 'Unable to save AppDynamics connection')); return false }
    finally { if (mounted.current) setBusy(false) }
  }
  const dirty = open && JSON.stringify(draft) !== JSON.stringify(appDynamicsDraft(editing || undefined))
  const guardState = useRef({ dirty, save, reset }); guardState.current = { dirty, save, reset }
  const guard = useMemo(() => ({ id: 'appdynamics-connection-draft', label: 'AppDynamics connection draft', isDirty: () => guardState.current.dirty, save: () => guardState.current.save(), discard: () => guardState.current.reset() }), [])
  useNavigationGuard(guard, dirty)
  const start = async (connection: AppDynamicsConnection, kind?: typeof discovery.kind) => {
    if (!projectId) return
    setBusy(true); setError('')
    try {
      const options = { collection_mode: connection.collection_mode, collector_agent_id: connection.collector_agent_id || '' }
      const value = kind ? await api.discoverAppDynamicsEntities(projectId, connection.id, kind, { ...options, application_id: discovery.application, metric_path: discovery.path }) : await api.testAppDynamicsConnection(projectId, connection.id, options)
      if (mounted.current) setOperation({ connection: connection.id, value })
    } catch (err) { if (mounted.current) setError(getErrorMessage(err, 'Unable to start AppDynamics operation')) }
    finally { if (mounted.current) setBusy(false) }
  }
  if (!projectId) return <p>Select a project to configure AppDynamics capture.</p>
  return <div className="capture-connections" aria-label="AppDynamics connections">
    <p>Connect a classic AppDynamics SaaS or on-premises Controller. Monitor sources choose direct collection or an assigned collector, and tests select which sources to capture.</p>
    {notice && <p role="status">{notice}</p>}{loading && <p role="status">Loading connections…</p>}
    {error && <p role="alert" className="form-error">{error} <button type="button" onClick={() => void load()}>Retry loading connections</button></p>}
    {!loading && !connections.length && !error && <p>No AppDynamics connections are configured for this project.</p>}
    {connections.map(connection => <div className="capture-connection-row" key={connection.id}><div><strong>{connection.name}</strong><p>{connection.enabled ? 'Enabled' : 'Disabled'} · {connection.collection_mode === 'collector' ? 'Assigned collector' : 'Direct collection'}</p>{canManage && <><p>{connection.config.endpoint}</p><CaptureConnectionHealth enabled={connection.enabled} missing={appDynamicsMissing(appDynamicsDraft(connection), connection)} checkedAt={connection.last_checked_at} updatedAt={connection.updated_at} status={connection.last_check_status} message={connection.last_check_message} capabilities={appDynamicsCapabilityHealth(connection.capabilities)} />{typeof connection.capabilities?.metric_api === 'string' && <p>Metric API: {connection.capabilities.metric_api}</p>}{connection.verified_revision !== connection.revision && <p>Current revision has not been verified.</p>}</>}</div>
      {canManage && !summaryOnly && <div className="capture-actions"><button type="button" className="btn btn-secondary btn-sm" disabled={busy || open} onClick={() => { setEditing(connection); setDraft(appDynamicsDraft(connection)); setOpen(true) }}>Edit</button><button type="button" className="btn btn-secondary btn-sm" disabled={busy || open || appDynamicsMissing(appDynamicsDraft(connection), connection).length > 0} onClick={() => void start(connection)}>Test saved connection</button><button type="button" className="btn btn-secondary btn-sm" disabled={busy || open} onClick={() => setDiscovery({ ...discovery, connection: connection.id })}>Discover entities</button><button type="button" className="btn btn-danger btn-sm" disabled={busy || open} onClick={() => setDeleting(connection.id)}>Delete</button>
        {deleting === connection.id && <div role="group" aria-label="Confirm connection deletion"><p>Delete {connection.name}? Existing run snapshots remain available.</p><button type="button" className="btn btn-danger" disabled={busy} onClick={async () => { setBusy(true); try { await api.deleteAppDynamicsConnection(projectId, connection.id, connection.revision); if (mounted.current) { setDeleting(''); await load() } } catch (err) { setError(getErrorMessage(err, 'Unable to delete connection')) } finally { if (mounted.current) setBusy(false) } }}>Confirm delete</button><button type="button" onClick={() => setDeleting('')}>Keep connection</button></div>}
      </div>}
    </div>)}

    {operation && <section aria-label="AppDynamics operation"><p role="status">{operation.value.kind || operation.value.operation || 'Operation'}: {operation.value.status}{operation.value.message ? ` · ${operation.value.message}` : ''}</p>{operation.value.error && <p role="alert">{operation.value.error}</p>}{typeof operation.value.result?.capabilities?.metric_api === 'string' && <p>Metric API: {operation.value.result.capabilities.metric_api}</p>}{operation.value.result?.capabilities && <ul>{Object.entries(appDynamicsCapabilityHealth(operation.value.result.capabilities) || {}).map(([name, capability]) => <li key={name}>{name}: {capability.status}{capability.message ? ` · ${capability.message}` : ''}</li>)}</ul>}{operation.value.result?.truncated && <p role="status">Discovery results are truncated at 200 entities. Browse a narrower metric branch or entity scope.</p>}{operation.value.result?.entities && <label>Search discovery results<input type="search" value={discovery.search} onChange={event => setDiscovery({ ...discovery, search: event.target.value })} /></label>}{operation.value.result?.entities && <table className="advanced-group-table"><thead><tr><th>Name</th><th>ID or path</th><th>Type</th></tr></thead><tbody>{discoveredEntities.map(entity => <tr key={`${entity.id}:${entity.path}`}><td>{entity.name}</td><td>{entity.path || entity.id}</td><td>{entity.type || '—'}</td></tr>)}</tbody></table>}</section>}
    {canManage && !summaryOnly && discovery.connection && <fieldset disabled={busy} aria-label="AppDynamics discovery"><legend>Discover AppDynamics entities</legend><label>Entity type<select value={discovery.kind} onChange={event => setDiscovery({ ...discovery, kind: event.target.value as typeof discovery.kind })}>{['applications', 'tiers', 'nodes', 'business-transactions', 'backends', 'metrics'].map(kind => <option key={kind}>{kind}</option>)}</select></label><label>Application ID<input value={discovery.application} onChange={event => setDiscovery({ ...discovery, application: event.target.value })} /></label>{discovery.kind === 'metrics' && <label>Metric path<input value={discovery.path} onChange={event => setDiscovery({ ...discovery, path: event.target.value })} /></label>}<button type="button" disabled={discovery.kind !== 'applications' && !discovery.application} onClick={() => { const connection = connections.find(connection => connection.id === discovery.connection); if (connection) void start(connection, discovery.kind) }}>Discover</button></fieldset>}
    {summaryOnly && canManage && <a href={settingsHref('appdynamics-capture')} className="btn btn-secondary">Manage AppDynamics connections</a>}
    {canManage && !summaryOnly && !open && <button type="button" className="btn btn-primary" onClick={() => { reset(); setOpen(true) }}>Add AppDynamics connection</button>}
    {canManage && !summaryOnly && open && <section aria-label="AppDynamics connection editor"><fieldset className="capture-editor" disabled={busy}><h3>{editing ? `Edit ${editing.name}` : 'Add AppDynamics connection'}</h3><CaptureSetupHelp missing={missing} credentials={[editing?.has_client_id && 'OAuth client ID', editing?.has_client_secret && 'OAuth client secret', editing?.has_username && 'Username', editing?.has_password && 'Password', editing?.has_events_api_key && 'Analytics API key'].filter((value): value is string => Boolean(value))} editing={Boolean(editing)} />
      <div className="capture-form-grid"><label>Name (required)<input value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label><label>Description<input value={draft.description} onChange={event => setDraft({ ...draft, description: event.target.value })} /></label><label>Controller HTTPS URL<input type="url" placeholder="https://account.saas.appdynamics.com" value={draft.config.endpoint} onChange={event => setDraft({ ...draft, config: { ...draft.config, endpoint: event.target.value } })} /></label><label>Controller context path<input value={draft.config.context_path} onChange={event => setDraft({ ...draft, config: { ...draft.config, context_path: event.target.value } })} /></label><label>Account name<input value={draft.config.account_name} onChange={event => setDraft({ ...draft, config: { ...draft.config, account_name: event.target.value } })} /></label><label>Authentication<select value={draft.config.auth_type} onChange={event => setDraft({ ...draft, config: { ...draft.config, auth_type: event.target.value as 'oauth' | 'basic' } })}><option value="oauth">OAuth client</option><option value="basic">Basic authentication</option></select></label>
      {(draft.config.auth_type === 'oauth' ? [['client_id_secret', 'OAuth client ID secret reference'], ['client_secret_secret', 'OAuth client secret reference']] : [['username_secret', 'Username secret reference'], ['password_secret', 'Password secret reference']]).map(([key, label]) => <label key={key}>{label}<input type="password" autoComplete="off" value={draft[key as keyof Pick<AppDynamicsConnectionInput, 'client_id_secret' | 'client_secret_secret' | 'username_secret' | 'password_secret'>] || ''} onChange={event => setDraft({ ...draft, [key]: event.target.value })} /></label>)}
      <label>Default collection mode<select value={draft.collection_mode} onChange={event => setDraft({ ...draft, collection_mode: event.target.value as 'direct' | 'collector' })}><option value="direct">Direct from controller</option><option value="collector">Assigned collector</option></select></label>{draft.collection_mode === 'collector' && <label>Assigned collector ID<input value={draft.collector_agent_id || ''} onChange={event => setDraft({ ...draft, collector_agent_id: event.target.value })} /></label>}
      <label>Analytics Events Service HTTPS URL (optional)<input type="url" value={draft.config.events_endpoint || ''} onChange={event => setDraft({ ...draft, config: { ...draft.config, events_endpoint: event.target.value } })} /></label><label>Analytics global account name<input value={draft.config.events_account_name || ''} onChange={event => setDraft({ ...draft, config: { ...draft.config, events_account_name: event.target.value } })} /></label><label>Analytics API key secret reference<input type="password" autoComplete="off" value={draft.events_api_key_secret || ''} onChange={event => setDraft({ ...draft, events_api_key_secret: event.target.value })} /></label>
      <label>Approved private CIDRs (one per line)<textarea disabled={!canApprovePrivateNetworks} value={(draft.config.approved_cidrs || []).join('\n')} onChange={event => setDraft({ ...draft, config: { ...draft.config, approved_cidrs: event.target.value.split(/\s+/).filter(Boolean) } })} /></label>{!canApprovePrivateNetworks && <p>System administrators approve changes to private network access.</p>}<label>Trusted CA PEM (optional)<textarea placeholder={editing?.has_custom_ca ? 'Saved custom CA retained unless replaced' : 'Optional trusted CA'} value={draft.config.ca_pem || ''} onChange={event => setDraft({ ...draft, config: { ...draft.config, ca_pem: event.target.value } })} /></label><label>Requests per minute<input type="number" min={1} max={60} value={draft.config.requests_per_minute || 60} onChange={event => setDraft({ ...draft, config: { ...draft.config, requests_per_minute: Number(event.target.value) } })} /></label><label>Analytics requests per minute<input type="number" min={1} max={30} value={draft.config.analytics_requests_per_minute || 30} onChange={event => setDraft({ ...draft, config: { ...draft.config, analytics_requests_per_minute: Number(event.target.value) } })} /></label>
      <label className="capture-checkbox">Enable connection<input type="checkbox" disabled={!draft.enabled && !canEnable} checked={draft.enabled} onChange={event => setDraft({ ...draft, enabled: event.target.checked })} /></label></div>
      {!canEnable && <p>Save this configuration with the connection disabled, test the saved connection, then edit to enable the verified revision. Changing Controller identity requires new credential references.</p>}
      <div className="capture-actions"><button type="button" className="btn btn-primary" disabled={!draft.name.trim() || (draft.enabled && (missing.length > 0 || !canEnable))} onClick={() => void save()}>Save connection</button><button type="button" className="btn btn-secondary" onClick={reset}>Discard draft</button></div>
    </fieldset></section>}
  </div>
}
