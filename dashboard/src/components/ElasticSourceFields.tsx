import { useEffect, useRef, useState } from 'react'
import { useELKConnectionsApi } from '../hooks/api/useELKConnectionsApi'
import type { ELKConnection } from '../types/monitoring'
import type { MonitorSource } from '../types/runMonitoring'
import type { ElasticMetric, ElasticQuery, ElasticSelection } from '../types/elastic'
import { elasticConnectionVerified, elasticMetricDefault, elasticSelectionNeedsIndices, elasticSourceReady, emptyElasticSelection } from './elasticMonitoringModel'
import { useElasticInspection } from './useElasticInspection'
import ElasticInspectionResult from './ElasticInspectionResult'
import { formatDatetimeLocalValue } from './newTestScheduleFormat'

const list = (value: string) => value.split(/[\s,]+/).filter(Boolean)
const units = ['count', 'ms', 'us', 'ns', 's', 'bytes', 'percent', 'ratio', 'requests/s', 'errors/s', 'operations/s', 'connections', 'threads']
function ListInput({ value, onChange, fieldList, placeholder }: { value: string[]; onChange: (value: string[]) => void; fieldList?: string; placeholder?: string }) {
  const [draft, setDraft] = useState(() => value.join(', '))
  useEffect(() => { setDraft(current => JSON.stringify(list(current)) === JSON.stringify(value) ? current : value.join(', ')) }, [value])
  return <input list={fieldList} placeholder={placeholder} value={draft} onChange={event => { setDraft(event.target.value); onChange(list(event.target.value)) }} />
}
const statistics = ['count', 'average', 'sum', 'min', 'max', 'p50', 'p90', 'p95', 'p99', 'cardinality']

function QueryFields({ query, index, esqlEnabled, asyncEnabled, onChange, onError, fieldList }: { query: ElasticQuery; index: number; esqlEnabled: boolean; asyncEnabled: boolean; onChange: (query: ElasticQuery) => void; onError: (error: string) => void; fieldList: string }) {
  const [dsl, setDSL] = useState(JSON.stringify(query.dsl || { match_all: {} }, null, 2))
  const [params, setParams] = useState(JSON.stringify(query.params || []))
  const [error, setError] = useState('')
  const reportValidation = useRef(onError); reportValidation.current = onError
  useEffect(() => { setDSL(current => { try { return JSON.stringify(JSON.parse(current)) === JSON.stringify(query.dsl || { match_all: {} }) ? current : JSON.stringify(query.dsl || { match_all: {} }, null, 2) } catch { return JSON.stringify(query.dsl || { match_all: {} }, null, 2) } }); setError(''); reportValidation.current('') }, [query.dsl])
  useEffect(() => { setParams(current => { try { return JSON.stringify(JSON.parse(current)) === JSON.stringify(query.params || []) ? current : JSON.stringify(query.params || []) } catch { return JSON.stringify(query.params || []) } }); setError(''); reportValidation.current('') }, [query.params])
  const setInvalid = (message: string) => { setError(message); onError(message) }
  return <fieldset><legend>Query {index + 1}</legend>
    <label>Query {index + 1} name<input value={query.name} onChange={event => onChange({ ...query, name: event.target.value })} /></label>
    <label>Query {index + 1} language<select value={query.language} onChange={event => { const language = event.target.value as ElasticQuery['language']; setInvalid(''); onChange({ ...query, language, dsl: language === 'dsl' ? { match_all: {} } : undefined, esql: language === 'esql' ? query.esql || '' : undefined, params: undefined, async: undefined }); setDSL(JSON.stringify({ match_all: {} }, null, 2)) }}><option value="dsl">Search DSL</option><option value="esql" disabled={!esqlEnabled}>ES|QL{!esqlEnabled ? ' · unavailable on this connection' : ''}</option></select></label>
    <label>Query {index + 1} result kind<select value={query.kind} onChange={event => onChange({ ...query, kind: event.target.value })}>{['metric', 'log', 'error', 'span', 'transaction'].map(kind => <option key={kind}>{kind}</option>)}</select></label>
    {query.language === 'dsl' ? <label>Query {index + 1} DSL query clause<textarea value={dsl} onChange={event => {
      setDSL(event.target.value)
      try { const value: unknown = JSON.parse(event.target.value); if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(); setInvalid(''); onChange({ ...query, dsl: value as Record<string, unknown> }) } catch { setInvalid('Enter one valid JSON query clause before saving or previewing.') }
    }} /></label> : <><label>Query {index + 1} ES|QL pipeline<textarea value={query.esql || ''} onChange={event => onChange({ ...query, esql: event.target.value })} /></label><label>Query {index + 1} bound parameters (JSON array)<textarea value={params} onChange={event => { setParams(event.target.value); try { const value: unknown = JSON.parse(event.target.value); if (!Array.isArray(value)) throw new Error(); setInvalid(''); onChange({ ...query, params: value }) } catch { setInvalid('Enter a valid JSON parameter array before saving or previewing.') } }} /></label><label><input type="checkbox" checked={Boolean(query.async)} disabled={!asyncEnabled} onChange={event => onChange({ ...query, async: event.target.checked })} />Use asynchronous ES|QL query{!asyncEnabled ? ' · unavailable on this connection' : ''}</label></>}
    {error && <p role="alert">{error}</p>}
    {query.kind === 'metric' && <>
      <label><input type="checkbox" checked={Boolean(query.exact_window)} onChange={event => onChange({ ...query, exact_window: event.target.checked })} />Query {index + 1} aggregates the exact query window</label>
      <label>Query {index + 1} timestamp output column<input list={fieldList} value={query.timestamp_column || ''} onChange={event => onChange({ ...query, timestamp_column: event.target.value })} /></label>
      <label>Query {index + 1} value field or output column<input list={fieldList} value={query.value_column || ''} onChange={event => onChange({ ...query, value_column: event.target.value })} /></label>
      <label>Query {index + 1} count output column<input list={fieldList} value={query.count_column || ''} onChange={event => onChange({ ...query, count_column: event.target.value || undefined })} /></label>
      <label>Query {index + 1} sum output column<input list={fieldList} value={query.sum_column || ''} onChange={event => onChange({ ...query, sum_column: event.target.value || undefined })} /></label>
      <label>Query {index + 1} source statistic<select value={query.statistic || 'average'} onChange={event => onChange({ ...query, statistic: event.target.value })}>{[...(query.language === 'esql' ? ['value'] : []), ...statistics].map(statistic => <option key={statistic}>{statistic}</option>)}</select></label>
      <label>Query {index + 1} unit<select value={query.unit || 'count'} onChange={event => onChange({ ...query, unit: event.target.value })}>{units.map(unit => <option key={unit}>{unit}</option>)}</select></label>
      <label>Query {index + 1} bucket duration (seconds)<input type="number" min={1} max={86400} disabled={query.exact_window} value={query.bucket_seconds ?? 10} onChange={event => onChange({ ...query, bucket_seconds: Number(event.target.value) })} /></label>
      <label><input type="checkbox" checked={Boolean(query.approximate)} onChange={event => onChange({ ...query, approximate: event.target.checked })} />Query {index + 1} produces approximate values</label>
      <label>Query {index + 1} dimension columns (up to four)<ListInput value={query.dimension_columns || query.group_by || []} onChange={fields => onChange({ ...query, dimension_columns: query.language === 'esql' ? fields : undefined, group_by: query.language === 'dsl' ? fields : undefined })} /></label>
    </>}
    <p>The executor enforces source, time, run scope and limits. ES|QL tables are bounded and cannot establish exhaustive document capture. DSL accepts a query clause. ES|QL permits supported read commands and explicit output mapping. Percentiles are approximate overlays.</p>
  </fieldset>
}

export default function ElasticSourceFields({ projectId, source, onChange, onValidationChange }: { projectId: string; source: MonitorSource; onChange: (source: MonitorSource) => void; onValidationChange?: (error: string) => void }) {
  const api = useELKConnectionsApi()
  const [connections, setConnections] = useState<ELKConnection[]>([])
  const [error, setError] = useState('')
  const [queryErrors, setQueryErrors] = useState<Record<number, string>>({})
  const [from, setFrom] = useState(() => formatDatetimeLocalValue(new Date(Date.now() - 15 * 60000)))
  const [to, setTo] = useState(() => formatDatetimeLocalValue(new Date()))
  const inspection = useElasticInspection(projectId)
  const selection: ElasticSelection = source.elastic || emptyElasticSelection(projectId)
  const queryRowIds = useRef<number[]>([])
  const nextQueryRowId = useRef(0)
  queryRowIds.current.length = Math.min(queryRowIds.current.length, selection.queries?.length || 0)
  while (queryRowIds.current.length < (selection.queries?.length || 0)) queryRowIds.current.push(nextQueryRowId.current++)
  const connection = connections.find(item => item.id === source.connection_id)
  const inspectionScope = useRef<{ kind: 'discover_indices' | 'discover_fields' | 'preview'; key: string } | undefined>(undefined)
  const connectionScope = JSON.stringify([projectId, source.connection_id, connection?.revision])
  const fieldScope = JSON.stringify([connectionScope, selection.indices])
  const previewScope = JSON.stringify([connectionScope, selection, queryErrors, from, to])
  const clearInspection = inspection.clear
  useEffect(() => {
    const previous = inspectionScope.current
    if (!previous) return
    const current = previous.kind === 'discover_indices' ? connectionScope : previous.kind === 'discover_fields' ? fieldScope : previewScope
    if (previous.key !== current) { inspectionScope.current = undefined; clearInspection() }
  }, [connectionScope, fieldScope, previewScope, clearInspection])
  const fieldList = `elastic-fields-${source.id || 'new'}`
  const fields = inspection.operation?.kind === 'discover_fields' ? inspection.operation.result?.fields || [] : []
  const change = (next: ElasticSelection) => onChange({ ...source, elastic: { ...next, project_id: projectId } })
  useEffect(() => {
    const controller = new AbortController(); setConnections([]); setError('')
    void api.listELKConnections(projectId, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setConnections((result.connections || []).filter(item => item.enabled)) }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [api, projectId])
  const needsStableTie = connection?.product === 'opensearch' && connection.capabilities?.shard_doc === false && (selection.logs || selection.errors || selection.traces || selection.transactions || selection.queries?.some(query => query.language === 'dsl' && query.kind !== 'metric'))
  const validation = Object.values(queryErrors).find(Boolean) || (elasticSelectionNeedsIndices(selection) && !selection.indices.length ? 'Select indices or data streams for document and metric collection.' : '') || (selection.queries?.some(query => query.language === 'esql' && query.kind === 'metric' && query.statistic === 'average' && (!query.sum_column || !query.count_column)) ? 'ES|QL averages require sum and count output columns to preserve observation weights.' : '') || ((selection.queries?.length || 0) > 5 ? 'Keep at most five advanced query definitions per source.' : '') || ((selection.metrics?.length || 0) > 20 ? 'Keep at most twenty mapped metrics per source.' : '') || (selection.queries?.some(query => query.language === 'esql') && connection?.capabilities?.esql === false ? 'This connection does not support ES|QL. Select Search DSL or remove the incompatible query.' : '') || (selection.queries?.some(query => query.async) && !connection?.capabilities?.async_esql ? 'Asynchronous ES|QL must be verified on the selected connection.' : '') || (needsStableTie && !selection.unique_sort_field ? 'This OpenSearch connection requires an explicitly mapped unique sortable document field for stable capture pagination.' : '') || (source.enabled && (!connection || !elasticConnectionVerified(connection)) ? 'Select a verified saved connection revision before enabling this source.' : '')
  useEffect(() => { onValidationChange?.(validation); return () => onValidationChange?.('') }, [validation, onValidationChange])
  const inspect = (kind: 'discover_indices' | 'discover_fields' | 'preview') => {
    if (!connection) return
    const fromDate = new Date(from); const toDate = new Date(to)
    if (kind === 'preview' && (!Number.isFinite(fromDate.getTime()) || !Number.isFinite(toDate.getTime()) || toDate <= fromDate)) { setError('Choose a valid preview time window.'); return }
    setError('')
    inspectionScope.current = { kind, key: kind === 'discover_indices' ? connectionScope : kind === 'discover_fields' ? fieldScope : previewScope }
    void inspection.start(connection.id, { kind, expected_revision: connection.revision, indices: selection.indices, ...(kind === 'preview' ? { selection, from_utc: fromDate.toISOString(), to_utc: toDate.toISOString() } : {}), limit: 100 })
  }
  const addTemplate = (kind: 'count' | 'average' | 'p99') => {
    const name = kind === 'count' ? 'document_count' : kind === 'average' ? 'duration_average' : 'duration_p99'
    const duration = selection.duration_field || 'event.duration'
    const fromSource = `FROM ${selection.indices.join(',')}`
    const query: ElasticQuery = kind === 'count' ? { name, language: 'esql', kind: 'metric', esql: `${fromSource} | STATS value = COUNT(*) | LIMIT 1`, value_column: 'value', statistic: 'count', unit: 'count', exact_window: true } : kind === 'average' ? { name, language: 'esql', kind: 'metric', esql: `${fromSource} | STATS duration_sum = SUM(${duration}), observations = COUNT(${duration}) BY bucket = BUCKET(${selection.timestamp_field || '@timestamp'}, 10 seconds) | EVAL value = duration_sum / observations | SORT bucket | LIMIT 1000`, timestamp_column: 'bucket', value_column: 'value', sum_column: 'duration_sum', count_column: 'observations', statistic: 'average', unit: selection.duration_unit || 'ns', bucket_seconds: 10 } : { name, language: 'esql', kind: 'metric', esql: `${fromSource} | STATS value = PERCENTILE(${duration}, 99) | LIMIT 1`, value_column: 'value', statistic: 'p99', unit: selection.duration_unit || 'ns', exact_window: true, approximate: true }
    change({ ...selection, queries: [...(selection.queries || []), query] })
  }
  const changeMetric = (index: number, update: Partial<ElasticMetric>) => change({ ...selection, metrics: (selection.metrics || []).map((metric, i) => i === index ? { ...metric, ...update } : metric) })
  return <fieldset className="elastic-source-fields" aria-label="Elastic source selection"><legend>Elasticsearch / OpenSearch collection</legend>
    {error && <p role="alert">{error}</p>}{inspection.error && <p role="alert">{inspection.error}</p>}{validation && <p role="alert">{validation}</p>}
    <label>Elastic connection<select value={source.connection_id || ''} onChange={event => { const selected = connections.find(item => item.id === event.target.value); onChange({ ...source, connection_id: event.target.value, executor_mode: selected?.collection_mode || 'direct', collector_id: selected?.collector_agent_id || '', elastic: { ...selection, project_id: projectId } }) }}><option value="">Select a saved connection</option>{source.connection_id && !connection && <option value={source.connection_id}>Selected connection unavailable — review required</option>}{connections.map(item => <option value={item.id} key={item.id}>{item.name} · {item.product || item.config?.product || 'elasticsearch'}{!elasticConnectionVerified(item) ? ' · unverified revision' : ''}</option>)}</select></label>
    <p>Collection location: {source.executor_mode === 'collector' ? `Assigned collector ${source.collector_id}` : 'Direct from controller'}. The saved connection controls the collection path and trust.</p>
    <label>Index patterns or data streams (up to 32)<ListInput value={selection.indices} onChange={indices => change({ ...selection, indices })} placeholder="logs-*, metrics-*, traces-*" /></label>
    <button className="btn btn-secondary btn-sm" type="button" disabled={!connection || inspection.busy} onClick={() => inspect('discover_indices')}>Discover Elastic indices</button><button className="btn btn-secondary btn-sm" type="button" disabled={!connection || !selection.indices.length || inspection.busy} onClick={() => inspect('discover_fields')}>Discover Elastic fields</button>
    {inspection.operation?.result?.indices?.map(index => <button className="btn btn-secondary btn-sm" key={index.name} type="button" disabled={selection.indices.includes(index.name)} onClick={() => change({ ...selection, indices: [...selection.indices, index.name] })}>Select index {index.name}</button>)}
    <datalist id={fieldList}>{fields.map(field => <option key={field.name} value={field.name}>{field.types.join(', ')}{field.conflict ? ' · conflicting mappings' : ''}{field.aggregatable ? ' · aggregatable' : ''}</option>)}</datalist>
    <label>Field mapping preset<select value={selection.preset || 'ecs'} onChange={event => { const preset = event.target.value; const defaults = emptyElasticSelection(projectId); change({ ...selection, preset, ...(preset === 'custom' ? {} : { timestamp_field: ['otel', 'opensearch_otel'].includes(preset) ? 'startTime' : preset === 'jaeger' ? 'startTimeMillis' : defaults.timestamp_field, service_field: ['otel', 'opensearch_otel'].includes(preset) ? 'serviceName' : preset === 'jaeger' ? 'process.serviceName' : defaults.service_field, trace_id_field: ['otel', 'opensearch_otel'].includes(preset) ? 'traceId' : preset === 'jaeger' ? 'traceID' : defaults.trace_id_field, span_id_field: ['otel', 'opensearch_otel'].includes(preset) ? 'spanId' : preset === 'jaeger' ? 'spanID' : defaults.span_id_field, duration_field: ['otel', 'opensearch_otel'].includes(preset) ? 'durationInNanos' : preset === 'jaeger' ? 'duration' : preset === 'elastic_apm' ? 'transaction.duration.us' : defaults.duration_field, duration_unit: ['elastic_apm', 'jaeger'].includes(preset) ? 'us' : 'ns', parent_span_id_field: preset === 'opensearch_otel' ? 'parentSpanId' : defaults.parent_span_id_field, status_field: preset === 'opensearch_otel' ? 'status.code' : defaults.status_field }) }) }}>{['ecs', 'elastic_apm', 'opensearch_otel', 'custom'].map(preset => <option key={preset}>{preset}</option>)}</select></label>
    <details><summary>Field mappings, service scope and correlation</summary>{(['timestamp_field', 'ingested_field', 'service_field', 'run_id_field', 'trace_id_field', 'span_id_field', 'transaction_id_field', 'parent_span_id_field', 'message_field', 'severity_field', 'duration_field', 'status_field', 'unique_sort_field'] as const).map(field => <label key={field}>{field.replace(/_/g, ' ')}<input list={fieldList} value={selection[field] || ''} onChange={event => change({ ...selection, [field]: event.target.value || undefined })} /></label>)}
      <label>Trace duration unit<select value={selection.duration_unit || 'ns'} onChange={event => change({ ...selection, duration_unit: event.target.value })}>{['ns', 'us', 'ms', 's'].map(unit => <option key={unit}>{unit}</option>)}</select></label>
      <label>Service values (optional)<ListInput value={selection.service_values || []} onChange={fields => change({ ...selection, service_values: fields })} /></label>
      <label>Projected attribute fields (up to 16)<ListInput fieldList={fieldList} value={selection.projected_fields || selection.attribute_fields || []} onChange={fields => change({ ...selection, projected_fields: fields, attribute_fields: undefined })} /></label>
      <p>Project only required scalar attributes. Older OpenSearch installations need a unique sortable document field; field discovery shows whether a mapping is aggregatable. Run-ID or trace-ID matching provides stronger correlation; service/time matching can include unrelated production traffic.</p>
    </details>
    <fieldset><legend>Signals to collect</legend>{(['logs', 'errors', 'traces', 'transactions', 'cluster_health', 'kibana_health', 'logstash_health'] as const).map(signal => <label key={signal}><input type="checkbox" checked={Boolean(selection[signal])} disabled={(signal === 'kibana_health' && !connection?.config?.kibana_endpoint) || (signal === 'logstash_health' && !connection?.config?.logstash_endpoint)} onChange={event => change({ ...selection, [signal]: event.target.checked })} />Capture {signal.replace(/_/g, ' ')}</label>)}<p>Health APIs have separate permissions and availability; unavailable component health does not imply unavailable search data.</p></fieldset>
    <fieldset><legend>Metric selections</legend>{(selection.metrics || []).map((metric, index) => <fieldset key={index}><legend>Elastic metric {index + 1}</legend>
      <label>Elastic metric {index + 1} name<input value={metric.name} onChange={event => changeMetric(index, { name: event.target.value })} /></label>
      <label>Elastic metric {index + 1} numeric field<input list={fieldList} value={metric.field || ''} onChange={event => changeMetric(index, { field: event.target.value })} /></label>
      <label>Elastic metric {index + 1} statistic<select value={metric.statistic} onChange={event => changeMetric(index, { statistic: event.target.value })}>{statistics.map(statistic => <option key={statistic}>{statistic}</option>)}</select></label>
      <label>Elastic metric {index + 1} unit<select value={metric.unit || 'count'} onChange={event => changeMetric(index, { unit: event.target.value })}>{units.map(unit => <option key={unit}>{unit}</option>)}</select></label>
      <label>Elastic metric {index + 1} kind<select value={metric.kind || 'gauge'} onChange={event => changeMetric(index, { kind: event.target.value })}>{['gauge', 'counter', 'observation', 'histogram', 'summary'].map(kind => <option key={kind}>{kind}</option>)}</select></label>
      <label><input type="checkbox" checked={Boolean(metric.exact_window)} onChange={event => changeMetric(index, { exact_window: event.target.checked })} />Elastic metric {index + 1} aggregates the exact query window</label>
      <label>Elastic metric {index + 1} bucket duration (seconds)<input type="number" min={1} max={86400} disabled={metric.exact_window} value={metric.bucket_seconds || 10} onChange={event => changeMetric(index, { bucket_seconds: Number(event.target.value) })} /></label>
      <label>Elastic metric {index + 1} group by (up to four fields)<ListInput value={metric.group_by || []} onChange={fields => changeMetric(index, { group_by: fields })} /></label>
      <label>Elastic metric {index + 1} pre-aggregated sum field<input list={fieldList} value={metric.sum_field || ''} onChange={event => changeMetric(index, { sum_field: event.target.value || undefined })} /></label>
      <label>Elastic metric {index + 1} pre-aggregated count field<input list={fieldList} value={metric.count_field || ''} onChange={event => changeMetric(index, { count_field: event.target.value || undefined })} /></label>
      <button className="btn btn-secondary btn-sm" type="button" onClick={() => change({ ...selection, metrics: selection.metrics?.filter((_, i) => i !== index) })}>Remove Elastic metric {index + 1}</button>
    </fieldset>)}<button className="btn btn-secondary btn-sm" type="button" disabled={(selection.metrics?.length || 0) >= 20} onClick={() => change({ ...selection, metrics: [...(selection.metrics || []), elasticMetricDefault()] })}>Add Elastic metric</button></fieldset>
    <fieldset><legend>Custom queries and result mapping</legend><p>Generated ES|QL templates use the selected source and mapped fields. Verify field capabilities and preview the result before saving.</p>{(['count', 'average', 'p99'] as const).map(kind => <button className="btn btn-secondary btn-sm" key={kind} type="button" disabled={!connection?.capabilities?.esql || !selection.indices.length || (selection.queries?.length || 0) >= 5 || [...(selection.queries || []), ...(selection.metrics || [])].some(item => item.name === (kind === 'count' ? 'document_count' : kind === 'average' ? 'duration_average' : 'duration_p99'))} onClick={() => addTemplate(kind)}>Add ES|QL {kind === 'count' ? 'exact count' : kind === 'average' ? 'weighted duration average' : 'exact-window p99'} template</button>)}{(selection.queries || []).map((query, index) => <div key={queryRowIds.current[index]}><QueryFields query={query} index={index} esqlEnabled={Boolean(connection?.capabilities?.esql)} asyncEnabled={Boolean(connection?.capabilities?.async_esql)} fieldList={fieldList} onChange={next => change({ ...selection, queries: selection.queries?.map((item, i) => i === index ? next : item) })} onError={message => setQueryErrors(current => ({ ...current, [index]: message }))} /><button className="btn btn-secondary btn-sm" type="button" onClick={() => { queryRowIds.current.splice(index, 1); setQueryErrors(current => Object.fromEntries(Object.entries(current).filter(([key]) => Number(key) !== index).map(([key, value]) => [Number(key) > index ? Number(key) - 1 : Number(key), value]))); change({ ...selection, queries: selection.queries?.filter((_, i) => i !== index) }) }}>Remove Elastic query {index + 1}</button></div>)}<button className="btn btn-secondary btn-sm" type="button" disabled={(selection.queries?.length || 0) >= 5} onClick={() => change({ ...selection, queries: [...(selection.queries || []), { name: '', language: 'dsl', kind: 'log', dsl: { match_all: {} }, statistic: 'average', unit: 'count', bucket_seconds: 10 }] })}>Add Elastic query</button></fieldset>
    <label>Late data settling time (seconds)<input type="number" min={0} max={3600} value={selection.settle_seconds ?? 60} onChange={event => change({ ...selection, settle_seconds: Number(event.target.value) })} /></label>
    <label>Late data overlap (seconds)<input type="number" min={0} max={3600} value={selection.overlap_seconds ?? 120} onChange={event => change({ ...selection, overlap_seconds: Number(event.target.value) })} /></label>
    <label>Provider page size<input type="number" min={1} max={100} value={selection.page_size || 100} onChange={event => change({ ...selection, page_size: Number(event.target.value) })} /></label>
    <fieldset><legend>Retention budgets</legend><label>Maximum retained records per source<input type="number" min={1} max={10000} value={selection.max_retained_records || 10000} onChange={event => change({ ...selection, max_retained_records: Number(event.target.value) })} /></label><label>Maximum canonical bytes per source<input type="number" min={1} max={8388608} value={selection.max_retained_bytes || 8388608} onChange={event => change({ ...selection, max_retained_bytes: Number(event.target.value) })} /></label><p>These values may lower deployment ceilings. Run budgets are shared across sources. Exhaustion stops the affected query and marks coverage as truncated.</p></fieldset>
    <label><input type="checkbox" checked={Boolean(selection.run_header)} onChange={event => change({ ...selection, run_header: event.target.checked })} />Add run correlation header to approved target origins</label>{selection.run_header && <label>Run header target origins (one per line)<textarea value={(selection.header_origins || []).join('\n')} onChange={event => change({ ...selection, header_origins: list(event.target.value) })} /></label>}
    <fieldset><legend>Read-only preview</legend><label>Elastic preview from<input type="datetime-local" value={from} onChange={event => setFrom(event.target.value)} /></label><label>Elastic preview to<input type="datetime-local" value={to} onChange={event => setTo(event.target.value)} /></label><button className="btn btn-secondary btn-sm" type="button" disabled={!connection || !elasticSourceReady(source) || inspection.busy || Boolean(validation)} onClick={() => inspect('preview')}>Preview Elastic source</button><p>Preview uses the selected connection and collection path. Returned records are projected and scrubbed within quotas.</p></fieldset>
    <ElasticInspectionResult operation={inspection.operation} onCancel={() => void inspection.cancel()} />
  </fieldset>
}
