import { useEffect, useState } from 'react'
import { useAppDynamicsConnectionsApi } from '../hooks/api/useAppDynamicsConnectionsApi'
import type { AppDynamicsConnection, AppDynamicsSelection, MonitorSource } from '../types/appdynamics'
import { emptyAppDynamicsSelection } from './appDynamicsConnectionModel'
import AppDynamicsMetricPresets from './AppDynamicsMetricPresets'


const ids = (value: string) => value.split(/[\s,]+/).filter(Boolean)
export default function AppDynamicsSourceFields({ projectId, source, onChange }: { projectId: string; source: MonitorSource; onChange: (source: MonitorSource) => void }) {
  const api = useAppDynamicsConnectionsApi()
  const [connections, setConnections] = useState<AppDynamicsConnection[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    void api.listAppDynamicsConnections(projectId, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setConnections((result.connections || []).filter(connection => connection.enabled)) }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [api, projectId])
  const selection: AppDynamicsSelection = { ...emptyAppDynamicsSelection(), ...source.appdynamics, metrics: source.appdynamics?.metrics || [], analytics: source.appdynamics?.analytics || [] }
  const change = (next: AppDynamicsSelection) => onChange({ ...source, appdynamics: { ...next, project_id: projectId } })
  return <fieldset aria-label="AppDynamics source selection"><legend>AppDynamics collection</legend><p>Select at least one metric, diagnostic capability, or Analytics definition for this source.</p>
    {error && <p role="alert">Unable to load AppDynamics connections: {error}</p>}
    <label>AppDynamics connection<select value={source.connection_id || ''} onChange={event => {
      const connection = connections.find(item => item.id === event.target.value)
      onChange({ ...source, connection_id: event.target.value, executor_mode: connection?.collection_mode || 'direct', collector_id: connection?.collector_agent_id || '', interval_seconds: 60, appdynamics: { ...selection, project_id: projectId } })
    }}><option value="">Select a saved connection</option>{source.connection_id && !connections.some(item => item.id === source.connection_id) && <option value={source.connection_id}>Selected connection unavailable — review required</option>}{connections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}{connection.verified_revision !== connection.revision ? ' · unverified revision' : ''}</option>)}</select></label>
    <label>Collection location<select disabled value={source.executor_mode || 'direct'} onChange={event => onChange({ ...source, executor_mode: event.target.value as 'direct' | 'collector' })}><option value="direct">Direct from controller</option><option value="collector">Assigned collector</option></select></label>
    <p>The saved connection assigns the collection location and collector. Change these in connection settings, then verify the revised connection.</p>
    <label>Application IDs (comma separated)<input value={selection.applications.join(', ')} onChange={event => change({ ...selection, applications: ids(event.target.value) })} /></label>
    <p>Use connection discovery to find application IDs and exact metric paths. Each source accepts up to 20 metric selections and 5 Analytics queries. Metrics retain their source timestamps, units, and entity dimensions.</p>
    {(['tier', 'node', 'business_transaction', 'backend', 'machine'] as const).map(scope => <label key={scope}>{scope.replace(/_/g, ' ')} IDs (optional)<input value={selection.scope_ids?.[scope]?.join(', ') || ''} onChange={event => change({ ...selection, scope_ids: { ...selection.scope_ids, [scope]: ids(event.target.value) } })} /></label>)}
    <AppDynamicsMetricPresets projectId={projectId} connectionId={source.connection_id} selection={selection} onChange={change} />
    <fieldset><legend>Metric selections</legend>{selection.metrics.map((metric, index) => <div key={index} className="capture-metric-row">
      <label>Metric {index + 1} name<input value={metric.name} onChange={event => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, name: event.target.value } : item) })} /></label>
      <label>Metric {index + 1} application ID<input value={metric.application_id} onChange={event => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, application_id: event.target.value } : item) })} /></label>
      <label>Metric {index + 1} path<input value={metric.path} placeholder="Overall Application Performance|Average Response Time (ms)" onChange={event => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, path: event.target.value } : item) })} /></label>
      <label>Metric {index + 1} unit<input value={metric.unit} placeholder="ms" onChange={event => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, unit: event.target.value } : item) })} /></label>
      <label>Metric {index + 1} source statistic<select value={metric.statistic} onChange={event => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, statistic: event.target.value as typeof metric.statistic } : item) })}>{['value', 'average', 'sum', 'current', 'p95', 'p99'].map(statistic => <option key={statistic}>{statistic}</option>)}</select></label>
      <button type="button" onClick={() => change({ ...selection, metrics: selection.metrics.filter((_, i) => i !== index) })}>Remove metric {index + 1}</button>
    </div>)}<button type="button" disabled={selection.metrics.length >= 20} onClick={() => change({ ...selection, metrics: [...selection.metrics, { application_id: selection.applications[0] || '', name: '', path: '', unit: '', statistic: 'value' }] })}>Add AppDynamics metric</button></fieldset>
    {(['events', 'violations', 'snapshots'] as const).map(capability => <label key={capability}><input type="checkbox" checked={selection[capability]} onChange={event => change({ ...selection, [capability]: event.target.checked })} />Capture {capability === 'violations' ? 'health rule violations' : capability}</label>)}
    <p>Snapshots are bounded summaries. Events, violations, and snapshots use the selected run window and entity scope; they do not prove that load caused an issue.</p>
    <fieldset><legend>Optional Analytics</legend>{selection.analytics.map((query, index) => {
      const updateQuery = (fields: Partial<typeof query>) => change({ ...selection, analytics: selection.analytics.map((item, i) => i === index ? { ...item, ...fields } : item) })
      return <div key={index}>
        <label>Analytics {index + 1} name<input value={query.name} onChange={event => updateQuery({ name: event.target.value })} /></label>
        <label>Analytics {index + 1} application ID<input value={query.application_id || ''} onChange={event => updateQuery({ application_id: event.target.value })} /></label>
        <label>Analytics {index + 1} Controller application name<input value={query.application_name || ''} onChange={event => updateQuery({ application_name: event.target.value || undefined })} /></label>
        <label>Analytics {index + 1} application ID field (alternative)<input value={query.application_id_field || ''} placeholder="applicationId" onChange={event => updateQuery({ application_id_field: event.target.value || undefined })} /></label>
        <label>Analytics {index + 1} result kind<select value={query.kind || 'transaction'} onChange={event => updateQuery({ kind: event.target.value as typeof query.kind })}>{['transaction', 'log', 'metric'].map(kind => <option key={kind}>{kind}</option>)}</select></label>
        <label>Analytics {index + 1} unit<input value={query.unit || 'count'} onChange={event => updateQuery({ unit: event.target.value })} /></label>
        <label>Analytics {index + 1} source statistic<select value={query.statistic || 'value'} onChange={event => updateQuery({ statistic: event.target.value as typeof query.statistic })}>{['value', 'sum', 'average'].map(statistic => <option key={statistic}>{statistic}</option>)}</select></label>
        <label>Analytics {index + 1} bucket duration (seconds)<input type="number" min={1} max={86400} value={query.bucket_seconds || ''} onChange={event => updateQuery({ bucket_seconds: event.target.value ? Number(event.target.value) : undefined })} /></label>
        <label>Analytics {index + 1} run ID field (optional)<input value={query.run_id_field || ''} placeholder="apexRunID" onChange={event => updateQuery({ run_id_field: event.target.value || undefined })} /></label>
        <label>Analytics {index + 1} read query<textarea value={query.query} onChange={event => updateQuery({ query: event.target.value })} /></label>
        <button type="button" onClick={() => change({ ...selection, analytics: selection.analytics.filter((_, i) => i !== index) })}>Remove Analytics query {index + 1}</button>
      </div>
    })}<button type="button" disabled={selection.analytics.length >= 5} onClick={() => change({ ...selection, analytics: [...selection.analytics, { name: '', query: '', application_id: selection.applications[0] || '', kind: 'transaction', unit: 'count', statistic: 'value' }] })}>Add Analytics query</button><p>Requires a licensed Events Service and a separate Analytics API key. Each definition requires the exact Controller application name or an approved application ID field, together with its selected application ID. Project only the fields needed; SELECT * is rejected. Numeric series require an alias named value. Timestamped series require an explicit bucket duration.</p></fieldset>
    <label>Late data settling time (seconds)<input type="number" min={0} max={300} value={selection.settle_seconds} onChange={event => change({ ...selection, settle_seconds: Number(event.target.value) })} /></label>
    <label><input type="checkbox" checked={Boolean(selection.run_header)} onChange={event => change({ ...selection, run_header: event.target.checked })} />Add run correlation header to approved target origins</label>
    {selection.run_header && <><label>Header target origins (one per line)<textarea value={(selection.header_origins || []).join('\n')} onChange={event => change({ ...selection, header_origins: ids(event.target.value) })} /></label><p>Configure the corresponding AppDynamics HTTP data collector. Analytics definitions can name its run ID field to filter records for this run. Without that field, Analytics uses entity and time correlation.</p></>}
  </fieldset>
}
