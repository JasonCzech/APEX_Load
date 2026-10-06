import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { MonitorRule, MonitorSource } from '../types/runMonitoring'
import { monitorSourceMetrics } from './elasticMonitoringModel'
import './RunMonitoringControls.css'

function RuleDimensions({ value, onChange, onError }: { value?: Record<string, string>; onChange: (dimensions: Record<string, string>) => void; onError: (error: string) => void }) {
  const serialized = JSON.stringify(value || {})
  const [draft, setDraft] = useState(serialized)
  const [error, setError] = useState('')
  const applied = useRef(serialized)
  const reportError = useRef(onError); reportError.current = onError
  useEffect(() => { if (serialized !== applied.current) { applied.current = serialized; setDraft(serialized); setError(''); reportError.current('') } }, [serialized])
  return <><input value={draft} aria-invalid={Boolean(error)} onChange={event => {
    setDraft(event.target.value)
    try {
      const dimensions: unknown = JSON.parse(event.target.value)
      if (!dimensions || typeof dimensions !== 'object' || Array.isArray(dimensions) || !Object.values(dimensions).every(item => typeof item === 'string')) throw new Error()
      applied.current = JSON.stringify(dimensions)
      setError(''); onError(''); onChange(dimensions as Record<string, string>)
    } catch { const message = 'Enter a valid JSON object of string dimensions before saving or launching.'; setError(message); onError(message) }
  }} />{error && <span role="alert">{error}</span>}</>
}

export default function MonitorRuleEditor({ projectId, value, onChange, onValidationChange, canEdit = true }: { projectId?: string; value: MonitorRule[]; canEdit?: boolean; onChange: (rules: MonitorRule[]) => void; onValidationChange?: (error: string) => void }) {
  const [sources, setSources] = useState<MonitorSource[]>([])
  const [dimensionErrors, setDimensionErrors] = useState<Record<number, string>>({})
  const validation = Object.values(dimensionErrors).find(Boolean) || ''
  useEffect(() => { onValidationChange?.(validation); return () => onValidationChange?.('') }, [onValidationChange, validation])
  useEffect(() => setDimensionErrors({}), [projectId])
  useEffect(() => {
    const controller = new AbortController()
    if (projectId) void request<MonitorSource[]>(`/projects/${encodeURIComponent(projectId)}/monitor-sources`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) setSources(result.filter(source => ['appdynamics', 'elastic'].includes(source.pack) && source.enabled)) }).catch(() => {})
    return () => controller.abort()
  }, [projectId])
  if (!sources.length && !value.length) return null
  const change = (index: number, updates: Partial<MonitorRule>) => {
    if (updates.source_id !== undefined || updates.metric !== undefined) setDimensionErrors(current => ({ ...current, [index]: '' }))
    onChange(value.map((rule, i) => i === index ? { ...rule, ...updates } : rule))
  }
  return <fieldset className="run-monitor-controls run-monitor-criteria" disabled={!canEdit} aria-label="Monitoring performance criteria"><legend>Monitoring performance criteria</legend><p>Optional terminal pass/fail criteria use settled source buckets after collection finishes. Missing evidence is not evaluated; required incomplete monitoring makes the result inconclusive. These criteria never stop load execution. Source bucket percentiles are overlays. Elastic exact-window metrics can use a single final value, including an approximate percentile, re-read over the rule interval. Bucket percentiles are never combined into a whole-run percentile.</p>
    {value.map((rule, index) => {
      const source = sources.find(item => item.id === rule.source_id)
      const availableMetrics = monitorSourceMetrics(source)
      const percentile = (item?: typeof availableMetrics[number]) => ['p50', 'p90', 'p95', 'p99', 'cardinality'].includes(item?.statistic || '')
      const exactWindow = (item?: typeof availableMetrics[number]) => source?.pack === 'elastic' && Boolean(item?.exact_window || (item?.final_window_supported && percentile(item)))
      const metric = availableMetrics.find(item => item.name === rule.metric)
      return <fieldset key={index}><legend>Criterion {index + 1}</legend>
        <label>Criterion {index + 1} source<select value={rule.source_id} onChange={event => change(index, { source_id: event.target.value, metric: '', unit: '' })}><option value="">Select source</option>{rule.source_id && !source && <option value={rule.source_id}>Unavailable source — review required</option>}{sources.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label>Criterion {index + 1} metric<select value={rule.metric} onChange={event => { const next = availableMetrics.find(item => item.name === event.target.value); const canSum = next?.statistic === 'sum' || source?.pack === 'elastic' && next?.statistic === 'count' && next.final_window_supported; change(index, { metric: event.target.value, unit: next?.unit || '', statistic: exactWindow(next) ? 'value' : rule.statistic === 'value' || rule.statistic === 'sum' && !canSum ? 'avg' : rule.statistic }) }}><option value="">Select metric</option>{rule.metric && !metric && <option value={rule.metric}>{rule.metric}</option>}{availableMetrics.map(item => <option key={item.name} value={item.name} disabled={percentile(item) && !exactWindow(item)}>{item.name} · {item.unit}{exactWindow(item) ? ' · exact rule window' : percentile(item) ? ' · overlay only' : ''}</option>)}</select></label>
        <label>Criterion {index + 1} unit<input value={rule.unit} onChange={event => change(index, { unit: event.target.value })} /></label>
        <label>Criterion {index + 1} statistic<select value={rule.statistic} onChange={event => change(index, { statistic: event.target.value as MonitorRule['statistic'] })}><option value="avg" disabled={percentile(metric) || exactWindow(metric)}>Average</option><option value="min" disabled={percentile(metric) || exactWindow(metric)}>Minimum</option><option value="max" disabled={percentile(metric) || exactWindow(metric)}>Maximum</option><option value="value" disabled={!exactWindow(metric)}>Single final exact-window value</option><option value="sum" disabled={!(metric?.statistic === 'sum' || source?.pack === 'elastic' && metric?.statistic === 'count' && metric.final_window_supported) || exactWindow(metric)}>Sum of interval counts</option></select></label>
        <label>Criterion {index + 1} comparator<select value={rule.comparator} onChange={event => change(index, { comparator: event.target.value as MonitorRule['comparator'] })}>{[['lt', 'Less than'], ['lte', 'At most'], ['gt', 'Greater than'], ['gte', 'At least'], ['eq', 'Equal to'], ['neq', 'Not equal to']].map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
        <label>Criterion {index + 1} threshold<input type="number" step="any" value={rule.threshold} onChange={event => change(index, { threshold: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} minimum coverage (%)<input type="number" min={1} max={100} value={rule.min_coverage_pct} onChange={event => change(index, { min_coverage_pct: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} severity<select value={rule.severity} onChange={event => change(index, { severity: event.target.value as MonitorRule['severity'] })}><option value="fail">Fail criterion</option><option value="warning">Warning</option></select></label>
        <label>Criterion {index + 1} start offset (seconds)<input type="number" min={0} value={rule.start_offset_seconds} onChange={event => change(index, { start_offset_seconds: Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} end offset (seconds, optional)<input type="number" min={rule.start_offset_seconds + 1} value={rule.end_offset_seconds ?? ''} onChange={event => change(index, { end_offset_seconds: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>
        <label>Criterion {index + 1} entity dimensions<RuleDimensions key={`${rule.source_id}:${rule.metric}`} value={rule.dimensions} onChange={dimensions => change(index, { dimensions })} onError={error => setDimensionErrors(current => ({ ...current, [index]: error }))} /></label>
        <button type="button" onClick={() => { setDimensionErrors(current => Object.fromEntries(Object.entries(current).filter(([key]) => Number(key) !== index).map(([key, error]) => [Number(key) > index ? Number(key) - 1 : Number(key), error]))); onChange(value.filter((_, i) => i !== index)) }}>Remove criterion {index + 1}</button>
      </fieldset>
    })}
    <button type="button" disabled={value.length >= 32 || !sources.length} onClick={() => onChange([...value, { source_id: '', metric: '', unit: '', statistic: 'avg', comparator: 'lte', threshold: 0, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }])}>Add monitoring performance criterion</button>
  </fieldset>
}
