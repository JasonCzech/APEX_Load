import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { MonitorRule, MonitorSource } from '../types/appdynamics'

export default function MonitorRuleEditor({ projectId, value, onChange, canEdit = true }: { projectId?: string; value: MonitorRule[]; canEdit?: boolean; onChange: (rules: MonitorRule[]) => void }) {
  const [sources, setSources] = useState<MonitorSource[]>([])
  useEffect(() => {
    const controller = new AbortController()
    if (projectId) void request<MonitorSource[]>(`/projects/${encodeURIComponent(projectId)}/monitor-sources`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setSources(result.filter(source => source.pack === 'appdynamics' && source.enabled)) }).catch(() => {})
    return () => controller.abort()
  }, [projectId])
  if (!sources.length && !value.length) return null
  const change = (index: number, updates: Partial<MonitorRule>) => onChange(value.map((rule, i) => i === index ? { ...rule, ...updates } : rule))
  return <fieldset disabled={!canEdit} aria-label="AppDynamics performance criteria"><legend>AppDynamics performance criteria</legend><p>Optional terminal pass/fail criteria use settled source buckets after collection finishes. Missing evidence is not evaluated; required incomplete monitoring makes the result inconclusive. These criteria never stop load execution. Source bucket percentiles are available for overlays and are not eligible for terminal aggregation; they are never combined into a whole-run percentile.</p>
    {value.map((rule, index) => {
      const source = sources.find(item => item.id === rule.source_id)
      const availableMetrics = [...(source?.appdynamics?.metrics || []), ...(source?.appdynamics?.analytics || []).map(query => ({ name: query.name, unit: query.unit || 'count', statistic: query.statistic || 'value' }))]
      const metric = availableMetrics.find(item => item.name === rule.metric)
      return <fieldset key={index}><legend>Criterion {index + 1}</legend>
        <label>Criterion {index + 1} source<select value={rule.source_id} onChange={event => change(index, { source_id: event.target.value, metric: '', unit: '' })}><option value="">Select source</option>{rule.source_id && !source && <option value={rule.source_id}>Unavailable source — review required</option>}{sources.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label>Criterion {index + 1} metric<select value={rule.metric} onChange={event => { const next = availableMetrics.find(item => item.name === event.target.value); change(index, { metric: event.target.value, unit: next?.unit || '', statistic: ['p95', 'p99'].includes(next?.statistic || '') && rule.statistic === 'avg' ? 'max' : rule.statistic === 'sum' && next?.statistic !== 'sum' ? 'avg' : rule.statistic }) }}><option value="">Select metric</option>{rule.metric && !metric && <option value={rule.metric}>{rule.metric}</option>}{availableMetrics.map(item => <option key={item.name} value={item.name} disabled={['p95', 'p99'].includes(item.statistic)}>{item.name} · {item.unit}{['p95', 'p99'].includes(item.statistic) ? ' · overlay only' : ''}</option>)}</select></label>
        <label>Criterion {index + 1} unit<input value={rule.unit} onChange={event => change(index, { unit: event.target.value })} /></label>
        <label>Criterion {index + 1} statistic<select value={rule.statistic} onChange={event => change(index, { statistic: event.target.value as MonitorRule['statistic'] })}><option value="avg" disabled={['p95', 'p99'].includes(metric?.statistic || '')}>Average</option><option value="min">Minimum</option><option value="max">Maximum</option><option value="sum" disabled={metric?.statistic !== 'sum'}>Sum of interval counts</option></select></label>
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
    <button type="button" disabled={value.length >= 32 || !sources.length} onClick={() => onChange([...value, { source_id: '', metric: '', unit: '', statistic: 'avg', comparator: 'lte', threshold: 0, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }])}>Add AppDynamics performance criterion</button>
  </fieldset>
}
