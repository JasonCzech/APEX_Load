import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { MonitorRule, MonitorSource } from '../types/runMonitoring'
import { monitorMetricCatalog, nativeMonitorProvider } from './monitorProviderModel'
import { useDynatraceConnectionsApi } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceOperation } from '../types/dynatrace'
import { monitorSeriesIdentity } from './monitorTimelineModel'

export default function MonitorRuleEditor({ projectId, value, onChange, canEdit = true }: { projectId?: string; value: MonitorRule[]; canEdit?: boolean; onChange: (rules: MonitorRule[]) => void }) {
  const [sources, setSources] = useState<MonitorSource[]>([])
  const api = useDynatraceConnectionsApi()
  const [preview, setPreview] = useState<{ source: string; connection: string; operation: DynatraceOperation }>()
  const [previewError, setPreviewError] = useState('')
  const [previewLoading, setPreviewLoading] = useState(false)
  const previewVersion = useRef(0)
  useEffect(() => {
    const controller = new AbortController()
    setSources([])
    if (projectId) void request<MonitorSource[]>(`/projects/${encodeURIComponent(projectId)}/monitor-sources`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setSources(result.filter(source => nativeMonitorProvider(source.pack) && source.enabled)) }).catch(() => {})
    return () => controller.abort()
  }, [projectId])
  useEffect(() => { previewVersion.current += 1; setPreview(undefined); setPreviewError(''); setPreviewLoading(false); return () => { previewVersion.current += 1 } }, [projectId])
  useEffect(() => {
    if (!projectId || !preview || !['pending', 'queued', 'running'].includes(preview.operation.status)) return
    const controller = new AbortController()
    const version = previewVersion.current
    const timer = setTimeout(() => { void api.getDynatraceOperation(projectId, preview.connection, preview.operation.id, { signal: controller.signal }).then(operation => { if (!controller.signal.aborted && version === previewVersion.current) setPreview({ ...preview, operation }) }).catch(err => { if (!controller.signal.aborted && version === previewVersion.current) { setPreviewError(String(err)); setPreview(undefined) } }) }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, preview, projectId])
  if (!sources.length && !value.length) return null
  const change = (index: number, updates: Partial<MonitorRule>) => onChange(value.map((rule, i) => i === index ? { ...rule, ...updates } : rule))
  const previewSource = async (source: MonitorSource) => {
    if (!projectId || !source.connection_id || !source.dynatrace) return
    const version = ++previewVersion.current
    setPreviewLoading(true); setPreview(undefined); setPreviewError('')
    try {
      const operation = await api.createDynatraceOperation(projectId, source.connection_id, { kind: 'preview', source_id: source.id })
      if (version === previewVersion.current) setPreview({ source: source.id, connection: source.connection_id, operation })
    } catch (err) { if (version === previewVersion.current) setPreviewError(String(err)) }
    finally { if (version === previewVersion.current) setPreviewLoading(false) }
  }
  return <fieldset disabled={!canEdit} aria-label="Monitoring performance criteria"><legend>Monitoring performance criteria</legend><p>Optional terminal pass/fail criteria use settled source buckets after collection finishes. Missing evidence is not evaluated; required incomplete monitoring makes the result inconclusive. These criteria never stop load execution. Bucket percentiles are overlay evidence. Prometheus grid-quantile criteria describe the selected evaluations and are never combined into a whole-run request percentile.</p>
    {value.map((rule, index) => {
      const source = sources.find(item => item.id === rule.source_id)
      const prometheus = source?.pack === 'prometheus-query'
      const availableMetrics = monitorMetricCatalog(source)
      const metric = availableMetrics.find(item => item.name === rule.metric)
      const terminalQuery = source?.prometheus?.queries.find(query => query.name === rule.metric)
      const overlayOnly = prometheus ? ['counter'] : ['p95', 'p99']
      const wholeWindow = Boolean(source?.dynatrace?.grail?.some(query => query.name === rule.metric && query.kind === 'metric' && !query.bucket_seconds))
      return <fieldset key={index}><legend>Criterion {index + 1}</legend>
        <label>Criterion {index + 1} source<select value={rule.source_id} onChange={event => change(index, { source_id: event.target.value, metric: '', unit: '', provider: sources.find(source => source.id === event.target.value)?.pack === 'prometheus-query' ? undefined : sources.find(source => source.id === event.target.value)?.pack as MonitorRule['provider'], query_id: undefined, evaluation_mode: sources.find(source => source.id === event.target.value)?.pack === 'prometheus-query' ? 'series_points' : undefined, require_freshness: false, backend: undefined, metric_id: undefined, source_statistic: undefined, resolution_seconds: undefined, evaluation_operation: undefined, entity_ids: undefined, dimensions: undefined })}><option value="">Select source</option>{rule.source_id && !source && <option value={rule.source_id}>Unavailable source — review required</option>}{sources.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label>Criterion {index + 1} metric<select value={rule.metric} onChange={event => { const next = availableMetrics.find(item => item.name === event.target.value); const query = source?.prometheus?.queries.find(item => item.name === event.target.value); change(index, { metric: event.target.value, unit: next?.unit || '', query_id: query?.id, evaluation_mode: prometheus ? overlayOnly.includes(next?.statistic || '') && query?.terminal_expression ? 'exact_window' : query?.terminal_expression ? rule.evaluation_mode || 'series_points' : 'series_points' : undefined, backend: undefined, metric_id: undefined, source_statistic: undefined, resolution_seconds: undefined, evaluation_operation: undefined, entity_ids: undefined, dimensions: undefined, statistic: !prometheus && ['p95', 'p99'].includes(next?.statistic || '') && rule.statistic === 'avg' ? 'max' : rule.statistic === 'sum' && next?.statistic !== 'sum' ? 'avg' : rule.statistic }) }}><option value="">Select metric</option>{rule.metric && !metric && <option value={rule.metric}>{rule.metric}</option>}{availableMetrics.map(item => <option key={item.name} value={item.name} disabled={overlayOnly.includes(item.statistic) && !source?.prometheus?.queries.find(query => query.name === item.name)?.terminal_expression}>{item.name} · {item.unit}{overlayOnly.includes(item.statistic) ? source?.prometheus?.queries.find(query => query.name === item.name)?.terminal_expression ? ' · exact window only' : ' · overlay only' : ''}</option>)}</select></label>
        <label>Criterion {index + 1} unit<input value={rule.unit} onChange={event => change(index, { unit: event.target.value })} /></label>
        {prometheus && <><label>Criterion {index + 1} evaluation mode<select value={rule.evaluation_mode || 'series_points'} onChange={event => change(index, { evaluation_mode: event.target.value as 'series_points' | 'exact_window', statistic: event.target.value === 'exact_window' && ['sum', 'count'].includes(terminalQuery?.semantics || '') ? 'sum' : 'avg' })}><option value="series_points" disabled={overlayOnly.includes(metric?.statistic || '')}>Settled expression evaluation points</option><option value="exact_window" disabled={!terminalQuery?.terminal_expression}>Exact terminal query window</option></select></label><label><input type="checkbox" checked={Boolean(rule.require_freshness)} onChange={event => change(index, { require_freshness: event.target.checked })} />Require known target sample freshness</label><p>Coverage uses the expected evaluation grid. Query results do not imply raw scrape observations. Rolling quantile rules describe grid estimates, never a whole-run request percentile. Exact-window evaluation needs an approved terminal expression.</p></>}
        {source?.pack === 'dynatrace' && (!rule.backend || !rule.metric_id || !rule.source_statistic || !wholeWindow && !rule.resolution_seconds) && <p>Preview and select an exact Dynatrace series before saving this criterion. Bucket criteria require its returned resolution; declared whole-window aggregates use the completed load window.</p>}
        {!prometheus && <details><summary>Exact provider series</summary><p>Use the immutable series identity shown by a selection preview or prior run. These filters prevent similarly named series, source statistics or resolutions from being combined. Averages require valid observation weights; descriptor-approved gauges can use time averages.</p>
          {source?.dynatrace && source.connection_id && projectId && <button type="button" disabled={previewLoading} onClick={() => void previewSource(source)}>Preview exact series for criterion {index + 1}</button>}
          {previewError && <p role="alert">Series preview unavailable: {previewError}</p>}
          {preview && source && preview.source === source.id && <><p role="status">Series preview: {preview.operation.status}{preview.operation.message ? ` · ${preview.operation.message}` : ''}. Preview consumes the configured scan allowance.</p>{[...new Map((preview.operation.result?.preview?.points || []).filter(point => point.metric_name === rule.metric && point.provider?.name === 'dynatrace' && (!point.provider.query_window || wholeWindow)).map(point => [monitorSeriesIdentity(point), point])).values()].slice(0, 200).map(point => <button type="button" key={monitorSeriesIdentity(point)} onClick={() => change(index, { provider: 'dynatrace', backend: point.provider?.backend, metric_id: point.provider?.metric_id, source_statistic: point.provider?.statistic, resolution_seconds: point.provider?.query_window ? undefined : point.provider?.resolution_seconds, unit: point.unit, dimensions: point.dimensions, entity_ids: point.provider?.entity_ids })}>Use {point.metric_name} · {point.unit} · {point.provider?.statistic} / {point.provider?.query_window ? 'query window' : `${point.provider?.resolution_seconds}s`} · {Object.values(point.provider?.entity_ids || {}).join(', ')}</button>)}</>}
          <label>Criterion {index + 1} provider<select value={rule.provider || ''} onChange={event => change(index, { provider: event.target.value ? event.target.value as MonitorRule['provider'] : undefined })}><option value="">Selected source provider</option><option value="dynatrace">Dynatrace</option></select></label>
          <label>Criterion {index + 1} backend<input value={rule.backend || ''} onChange={event => change(index, { backend: event.target.value || undefined })} /></label>
          <label>Criterion {index + 1} provider metric ID<input value={rule.metric_id || ''} onChange={event => change(index, { metric_id: event.target.value || undefined })} /></label>
          <label>Criterion {index + 1} source statistic<input value={rule.source_statistic || ''} onChange={event => change(index, { source_statistic: event.target.value || undefined })} /></label>
          <label>Criterion {index + 1} source resolution (seconds)<input type="number" min={1} max={86400} value={rule.resolution_seconds || ''} onChange={event => change(index, { resolution_seconds: event.target.value ? Number(event.target.value) : undefined })} /></label>
          <label>Criterion {index + 1} evaluation operation<select value={rule.evaluation_operation || ''} onChange={event => change(index, { evaluation_operation: event.target.value ? event.target.value as MonitorRule['evaluation_operation'] : undefined })}><option value="">Use selected criterion statistic</option>{['avg', 'time_average', 'weighted_average', 'min', 'max', 'sum'].map(value => <option key={value}>{value}</option>)}</select></label>
        </details>}
        <label>Criterion {index + 1} statistic<select value={rule.statistic} onChange={event => change(index, { statistic: event.target.value as MonitorRule['statistic'] })}><option value="avg" disabled={!prometheus && ['p95', 'p99'].includes(metric?.statistic || '')}>{prometheus && ['quantile', 'p95', 'p99'].includes(metric?.statistic || '') ? 'Mean of grid quantile estimates' : 'Average'}</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum" disabled={prometheus ? rule.evaluation_mode !== 'exact_window' || !['sum', 'count'].includes(terminalQuery?.semantics || '') : metric?.statistic !== 'sum'}>Exact total / sum of interval counts</option></select></label>
        <label>Criterion {index + 1} comparator<select value={rule.comparator} onChange={event => change(index, { comparator: event.target.value as MonitorRule['comparator'] })}>{[['lt', 'Less than'], ['lte', 'At most'], ['gt', 'Greater than'], ['gte', 'At least'], ['eq', 'Equal to'], ['neq', 'Not equal to']].map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
        <label>Criterion {index + 1} threshold<input type="number" step="any" value={rule.threshold} onChange={event => change(index, { threshold: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} minimum coverage (%)<input type="number" min={1} max={100} value={rule.min_coverage_pct} onChange={event => change(index, { min_coverage_pct: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} severity<select value={rule.severity} onChange={event => change(index, { severity: event.target.value as MonitorRule['severity'] })}><option value="fail">Fail criterion</option><option value="warning">Warning</option></select></label>
        <label>Criterion {index + 1} start offset (seconds)<input type="number" min={0} value={rule.start_offset_seconds} onChange={event => change(index, { start_offset_seconds: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} end offset (seconds, optional)<input type="number" min={rule.start_offset_seconds + 1} value={rule.end_offset_seconds ?? ''} onChange={event => change(index, { end_offset_seconds: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} entity dimensions<input key={`${rule.source_id}:${rule.metric}:${JSON.stringify(rule.dimensions || {})}`} defaultValue={JSON.stringify(rule.dimensions || {})} onBlur={event => { try { const dimensions: unknown = JSON.parse(event.target.value); if (dimensions && typeof dimensions === 'object' && !Array.isArray(dimensions) && Object.values(dimensions).every(item => typeof item === 'string')) { change(index, { dimensions: dimensions as Record<string, string> }); event.target.setCustomValidity('') } else event.target.setCustomValidity('Enter a JSON object of string dimensions.') } catch { event.target.setCustomValidity('Enter a valid JSON object.') } event.target.reportValidity() }} /></label>
        <button type="button" onClick={() => onChange(value.filter((_, i) => i !== index))}>Remove criterion {index + 1}</button>
      </fieldset>
    })}
    <button type="button" disabled={value.length >= 32 || !sources.length} onClick={() => onChange([...value, { source_id: '', metric: '', unit: '', statistic: 'avg', comparator: 'lte', threshold: 0, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }])}>Add monitoring performance criterion</button>
  </fieldset>
}
