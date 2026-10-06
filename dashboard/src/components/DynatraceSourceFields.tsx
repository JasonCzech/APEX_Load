import { useEffect, useRef, useState } from 'react'
import { useDynatraceConnectionsApi } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceConnection } from '../types/monitoring'
import type { MonitorSource } from '../types/runMonitoring'
import { emptyDynatraceSelection, type DynatraceOperation, type DynatraceSelection } from '../types/dynatrace'
import { settingsHref } from './settingsNavigation'

const list = (value: string) => value.split(/[\s,]+/).filter(Boolean)
export default function DynatraceSourceFields({ projectId, source, onChange }: { projectId: string; source: MonitorSource; onChange: (source: MonitorSource) => void }) {
  const api = useDynatraceConnectionsApi()
  const [connections, setConnections] = useState<DynatraceConnection[]>([])
  const [error, setError] = useState('')
  const [operation, setOperation] = useState<DynatraceOperation>()
  const [busy, setBusy] = useState(false)
  const inspectionVersion = useRef(0)
  useEffect(() => {
    const controller = new AbortController()
    void api.listDynatraceConnections(projectId, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) { setConnections((result.connections || []).filter(connection => connection.enabled)); setError('') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [api, projectId])
  useEffect(() => { inspectionVersion.current += 1; setOperation(undefined); setError(''); setBusy(false); return () => { inspectionVersion.current += 1 } }, [projectId, source.connection_id])
  useEffect(() => {
    if (!source.connection_id || !operation || !['pending', 'queued', 'running'].includes(operation.status)) return
    const controller = new AbortController()
    const timer = setTimeout(() => { void api.getDynatraceOperation(projectId, source.connection_id || '', operation.id, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setOperation(result) }).catch(err => { if (!controller.signal.aborted) { setError(String(err)); setOperation(undefined) } }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, source.connection_id, operation])
  const selection: DynatraceSelection = source.dynatrace ? { ...source.dynatrace, metrics: source.dynatrace.metrics || [], grail: source.dynatrace.grail || [], problems: Boolean(source.dynatrace.problems), events: Boolean(source.dynatrace.events), settle_seconds: source.dynatrace.settle_seconds || 120 } : emptyDynatraceSelection()
  const change = (next: DynatraceSelection) => onChange({ ...source, dynatrace: { ...next, project_id: projectId } })
  const inspect = async (kind: 'preview' | 'discover') => {
    if (!source.connection_id) return
    const version = ++inspectionVersion.current
    setBusy(true); setError('')
    try { const result = await api.createDynatraceOperation(projectId, source.connection_id, kind === 'preview' ? { kind, selection: { ...selection, project_id: projectId } } : { kind, options: { kind: 'metrics', limit: 100 } }); if (version === inspectionVersion.current) setOperation(result) }
    catch (err) { if (version === inspectionVersion.current) setError(String(err)) } finally { if (version === inspectionVersion.current) setBusy(false) }
  }
  return <fieldset aria-label="Dynatrace source selection"><legend>Dynatrace SaaS collection</legend>
    {error && <p role="alert">Dynatrace selection unavailable: {error}</p>}
    <label>Dynatrace connection<select disabled={busy} value={source.connection_id || ''} onChange={event => {
      const connection = connections.find(item => item.id === event.target.value)
      onChange({ ...source, connection_id: event.target.value, executor_mode: connection?.collection_mode || 'direct', collector_id: connection?.collector_agent_id || '', interval_seconds: 60, dynatrace: { ...selection, project_id: projectId } })
    }}><option value="">Select a saved connection</option>{source.connection_id && !connections.some(item => item.id === source.connection_id) && <option value={source.connection_id}>Selected connection unavailable — review required</option>}{connections.map(connection => <option key={connection.id} value={connection.id} disabled={!connection.revision || connection.verified_revision !== connection.revision}>{connection.name}{!connection.revision || connection.verified_revision !== connection.revision ? ' · unverified revision' : ''}</option>)}</select></label>
    <p>Collection location: {source.executor_mode === 'collector' ? `Assigned collector ${source.collector_id}` : 'Direct from controller'}. The saved connection controls execution, credentials and Grail scan budgets. <a href={settingsHref('dynatrace-capture')}>Manage and discover Dynatrace connections</a></p>
    <label>Problems/events/topology entity selector<input disabled={selection.topology_mode === 'smartscape'} value={selection.entity_selector || ''} placeholder="type(SERVICE)" onChange={event => change({ ...selection, entity_selector: event.target.value })} /></label>
    <p>This selector scopes Classic problems, events and topology. Set each metric's entity selector independently; Grail queries use their own approved entity fields and IDs. Entity and time selection includes all matching activity. Metrics retain buckets, dimensions and descriptor units.</p>
    <fieldset><legend>Classic metric selections</legend>{selection.metrics.map((metric, index) => {
      const update = (fields: Partial<typeof metric>) => change({ ...selection, metrics: selection.metrics.map((item, i) => i === index ? { ...item, ...fields } : item) })
      return <div key={index} className="capture-metric-row">
        <label>Dynatrace metric {index + 1} name<input value={metric.name} onChange={event => update({ name: event.target.value })} /></label>
        <label>Dynatrace metric {index + 1} selector<input value={metric.metric_selector} placeholder="builtin:service.response.time" onChange={event => update({ metric_selector: event.target.value })} /></label>
        <label>Dynatrace metric {index + 1} entity selector<input value={metric.entity_selector || ''} onChange={event => update({ entity_selector: event.target.value })} /></label>
        <label>Dynatrace metric {index + 1} unit<input value={metric.unit || ''} placeholder="Descriptor unit" onChange={event => update({ unit: event.target.value })} /></label>
        <label>Dynatrace metric {index + 1} aggregation<select value={metric.aggregation} onChange={event => update({ aggregation: event.target.value as typeof metric.aggregation })}>{['avg', 'min', 'max', 'sum', 'count', 'value', 'p95', 'p99'].map(value => <option key={value}>{value}</option>)}</select></label>
        <label>Dynatrace metric {index + 1} semantics<select value={metric.semantics || ''} onChange={event => update({ semantics: event.target.value ? event.target.value as typeof metric.semantics : undefined })}><option value="">Descriptor semantics</option><option value="gauge">Gauge time average (descriptor must agree)</option><option value="request_average">Request average (requires valid observation weights)</option></select></label>
        <button type="button" onClick={() => change({ ...selection, metrics: selection.metrics.filter((_, i) => i !== index) })}>Remove Dynatrace metric {index + 1}</button>
      </div>
    })}<button type="button" disabled={selection.metrics.length >= 20} onClick={() => change({ ...selection, metrics: [...selection.metrics, { name: '', metric_selector: '', aggregation: 'avg' }] })}>Add Dynatrace metric</button><p>Up to 20 selections per source. Whole-window folding, fabricated missing values and time shifts are rejected. Bucket percentiles remain overlays and never become whole-run percentiles.</p></fieldset>
    <label><input type="checkbox" checked={selection.problems} onChange={event => change({ ...selection, problems: event.target.checked })} />Capture problems and their latest resolution</label>
    {selection.problems && <label>Source problem selector<input value={selection.problem_selector || ''} onChange={event => change({ ...selection, problem_selector: event.target.value })} /></label>}
    <label><input type="checkbox" checked={selection.events} onChange={event => change({ ...selection, events: event.target.checked })} />Capture events</label>
    {selection.events && <label>Source event selector<input value={selection.event_selector || ''} onChange={event => change({ ...selection, event_selector: event.target.value })} /></label>}
    <label>Topology discovery<select value={selection.topology_mode || ''} onChange={event => change({ ...selection, topology_mode: event.target.value as DynatraceSelection['topology_mode'], entity_selector: event.target.value === 'smartscape' ? '' : selection.entity_selector, ...(event.target.value === 'smartscape' ? {} : { smartscape_node_type: undefined, smartscape_entity_ids: undefined }) })}><option value="">Off</option><option value="classic">Classic entities and relationships</option><option value="smartscape">Smartscape</option></select></label>
    {selection.topology_mode === 'smartscape' && <><label>Smartscape node type<input value={selection.smartscape_node_type || ''} placeholder="*" onChange={event => change({ ...selection, smartscape_node_type: event.target.value })} /></label><label>Smartscape entity IDs<input value={(selection.smartscape_entity_ids || []).join(', ')} onChange={event => change({ ...selection, smartscape_entity_ids: list(event.target.value) })} /></label><p>Smartscape uses its own opaque IDs and node types. Selecting Smartscape clears the Classic diagnostic/topology entity selector; per-metric Classic selectors are retained.</p></>}
    <fieldset><legend>Optional Grail queries</legend>{selection.grail.map((query, index) => {
      const update = (fields: Partial<typeof query>) => change({ ...selection, grail: selection.grail.map((item, i) => i === index ? { ...item, ...fields } : item) })
      return <div key={index}>
        <label>Grail {index + 1} name<input value={query.name} onChange={event => update({ name: event.target.value })} /></label>
        <label>Grail {index + 1} result kind<select value={query.kind} onChange={event => update({ kind: event.target.value as typeof query.kind, ...(event.target.value === 'metric' ? { value_field: query.value_field || 'value', statistic: query.statistic || 'average', bucket_seconds: query.bucket_seconds ?? 60 } : {}) })}>{['metric', 'log', 'span', 'event', 'trace'].map(kind => <option key={kind}>{kind}</option>)}</select></label>
        <label>Grail {index + 1} read-only DQL<textarea value={query.query} onChange={event => update({ query: event.target.value })} /></label>
        <label>Grail {index + 1} projected fields<input value={(query.fields || []).join(', ')} onChange={event => update({ fields: list(event.target.value) })} /></label>
        <label>Grail {index + 1} entity ID field<input value={query.entity_id_field || ''} placeholder="dt.entity.service" onChange={event => update({ entity_id_field: event.target.value })} /></label>
        <label>Grail {index + 1} entity IDs<input value={(query.entity_ids || []).join(', ')} onChange={event => update({ entity_ids: list(event.target.value) })} /></label>
        <label>Grail {index + 1} run ID field<input value={query.run_id_field || ''} placeholder="apex.test_id" onChange={event => update({ run_id_field: event.target.value })} /></label>
        {query.kind === 'metric' && <><label>Grail {index + 1} unit<input value={query.unit || ''} onChange={event => update({ unit: event.target.value })} /></label><label>Grail {index + 1} value field<input value={query.value_field ?? 'value'} onChange={event => update({ value_field: event.target.value })} /></label><label>Grail {index + 1} source statistic<select value={query.statistic || 'average'} onChange={event => update({ statistic: event.target.value })}>{['average', 'time_average', 'min', 'max', 'sum', 'value', 'p95', 'p99'].map(value => <option key={value}>{value}</option>)}</select></label><label>Grail {index + 1} bucket seconds (0 for whole-window aggregate)<input type="number" min={0} max={86400} value={query.bucket_seconds ?? 60} onChange={event => update({ bucket_seconds: Number(event.target.value) })} /></label></>}
        <button type="button" onClick={() => change({ ...selection, grail: selection.grail.filter((_, i) => i !== index) })}>Remove Grail query {index + 1}</button>
      </div>
    })}<button type="button" disabled={selection.grail.length >= 5} onClick={() => change({ ...selection, grail: [...selection.grail, { name: '', kind: 'log', query: '', fields: ['timestamp', 'content', 'status', 'trace.id'], run_id_field: '' }] })}>Add Grail query</button><p>Requires separate Platform read permissions and available Grail data. Administrator-owned DQL must use explicit projections; samples exclude request bodies, arbitrary properties and unrestricted traces. Trace summaries represent sampled spans and do not imply a complete trace. Query previews consume the configured scan budget.</p></fieldset>
    <label>Late data settling time (seconds)<input type="number" min={0} max={3600} value={selection.settle_seconds} onChange={event => change({ ...selection, settle_seconds: Number(event.target.value) })} /></label>
    <label><input type="checkbox" checked={Boolean(selection.run_header)} onChange={event => change({ ...selection, run_header: event.target.checked })} />Add run correlation header to approved target origins</label>
    {selection.run_header && <><label>Header target origins (one per line)<textarea value={(selection.header_origins || []).join('\n')} onChange={event => change({ ...selection, header_origins: list(event.target.value) })} /></label><p>Configure operator-owned Dynatrace capture of X-Apex-Test-Run and the corresponding Grail run ID field. W3C trace IDs remain separate. Redirects outside approved origins do not receive the header.</p></>}
    <button type="button" disabled={busy || !source.connection_id} onClick={() => void inspect('preview')}>Preview Dynatrace selection</button>
    <button type="button" disabled={busy || !source.connection_id} onClick={() => void inspect('discover')}>Discover metric catalog</button>
    {operation && <section aria-label="Dynatrace source inspection"><p role="status">{operation.kind}: {operation.status}{operation.message ? ` · ${operation.message}` : ''}</p>{operation.result?.truncated && <p>Results are truncated. Narrow the selection.</p>}{operation.result?.preview && <><p>{operation.result.preview.progress?.query_cost_known ? `${(operation.result.preview.progress.scanned_bytes || 0).toLocaleString()} scanned bytes reported.` : 'Upstream scan cost is unknown; the reserved query budget remains charged.'}</p><p>{operation.result.preview.points?.length || 0} preview observations; {operation.result.preview.evidence?.length || 0} retained summaries. Preview does not create test traffic.</p><ul>{Object.entries(operation.result.preview.coverage || {}).map(([kind, coverage]) => <li key={kind}>{kind}: {coverage.status}{coverage.truncated ? ' · truncated' : ''}{coverage.message ? ` · ${coverage.message}` : ''}</li>)}</ul></>}{(operation.result?.items || operation.result?.metrics || []).map(item => <p key={item.id}>{item.name} · {item.id}<button type="button" disabled={selection.metrics.length >= 20} onClick={() => change({ ...selection, metrics: [...selection.metrics, { name: '', metric_selector: item.id || '', aggregation: 'avg' }] })}>Select {item.name}</button></p>)}</section>}
  </fieldset>
}
