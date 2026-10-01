import { useState } from 'react'
import ArchivedNativePanel from './ArchivedNativePanel'
import './NativeAnalyticsPanel.css'
import { Line, LineChart, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import type { MetricsHistoryPoint } from '../types'
import type { DashboardDefinitionPanel, DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'
import { nativeMetrics, nativePanelRows } from './nativeAnalyticsModel'

export default function NativeAnalyticsPanel({ panel, history }: { panel: DashboardDefinitionPanel; history: MetricsHistoryPoint[] }) {
  const rows = nativePanelRows(panel, history)
  if (!rows) return <p role="status">This panel requires a query that is unavailable in this view. No unfiltered totals are substituted.</p>
  const metricName = panel.query?.metric || ''
  const metric = nativeMetrics[metricName === 'throughput' ? 'tps' : metricName === 'transaction_duration' ? 'response_time' : metricName]
  const latest = rows[rows.length - 1]?.value
  if (panel.kind === 'stat') return <div><strong>{latest === undefined ? 'No current observation' : latest.toLocaleString(undefined, { maximumFractionDigits: 2 })}</strong><p>{metric.label} · {panel.query?.aggregation} per source interval</p></div>
  if (panel.kind !== 'line' && panel.kind !== 'table') return <p role="status">This panel visualization is unavailable.</p>
  return <ExportableChart allowSVG={panel.kind === 'line'} definitionId={panel.id} title={panel.title} description={`${metric.label}; retained run observations. Gaps remain missing and interval percentiles are not averaged.`} rows={rows} columns={[{ key: 'time', label: 'Time', value: row => row.time }, { key: 'value', label: metric.label, value: row => row.value ?? '' }]}>
    {panel.kind === 'table' ? <table><thead><tr><th>Time</th><th>{metric.label}</th></tr></thead><tbody>{rows.slice(-100).map((row, index) => <tr key={index}><td>{row.time}</td><td>{row.value ?? 'Missing'}</td></tr>)}</tbody></table> : <ResponsiveContainer width="100%" height={240}><LineChart data={rows}><XAxis dataKey="time" /><YAxis /><Tooltip /><Line type="linear" name={metric.label} dataKey="value" stroke="#38bdf8" connectNulls={false} dot={false} isAnimationActive={false} /></LineChart></ResponsiveContainer>}
  </ExportableChart>
}

export function NativePanelAuthoring({ onSave, properties = [] }: { properties?: string[]; onSave: (definition: Pick<DashboardDefinition, 'name' | 'tabs'>) => Promise<void> }) {
  const [population, setPopulation] = useState('')
  const [action, setAction] = useState('')
  const [property, setProperty] = useState('')
  const [baseline, setBaseline] = useState('')
  const [start, setStart] = useState(0)
  const [end, setEnd] = useState(60)
  const [bucket, setBucket] = useState(10)
  const [title, setTitle] = useState('Run analytics')
  const [metric, setMetric] = useState('tps')
  const [aggregation, setAggregation] = useState('avg')
  const [kind, setKind] = useState('line')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  return <details className="native-panel-authoring"><summary>Create project analytics panel</summary><form onSubmit={async event => {
    event.preventDefault(); setBusy(true); setError('')
    try { await onSave({ name: title.trim(), tabs: [{ id: 'analytics', title: 'Analytics', panels: [{ id: 'metric', title: title.trim(), kind, x: 0, y: 0, w: 24, h: 8, query: { metric, aggregation, max_series: 1, ...(population ? { population: population as 'action' | 'transaction', filters: action ? { action } : {}, start_seconds: start, end_seconds: end, bucket_seconds: bucket, comparison_run_id: baseline.trim() || undefined, ...(population === 'action' && property ? { property_name: property.split('=')[0], property_value: property.slice(property.indexOf('=') + 1) } : {}) } : {}) } }] }] }) } catch (reason) { setError(reason instanceof Error ? reason.message : 'Unable to save panel') } finally { setBusy(false) }
  }}>
    <label>Panel title<input required maxLength={256} value={title} onChange={event => setTitle(event.target.value)} /></label>
    <label>Evidence population<select aria-label="Evidence population" value={population} onChange={event => { setPopulation(event.target.value); setMetric(event.target.value ? 'response_time' : 'tps'); setAggregation('avg'); setProperty('') }}><option value="">Retained run totals</option><option value="action">Archived actions</option><option value="transaction">Archived business transactions</option></select></label>
    {population && <fieldset><legend>Archive selection</legend>
      <label>Exact action or transaction name<input maxLength={256} value={action} onChange={event => setAction(event.target.value)} /></label>
      {population === 'action' && <label>Property category<select aria-label="Property category" value={property} onChange={event => setProperty(event.target.value)}><option value="">All categories</option>{properties.map(value => <option key={value}>{value}</option>)}</select></label>}
      <label>Start elapsed seconds<input type="number" min={0} max={86399} required value={start} onChange={event => setStart(Number(event.target.value))} /></label>
      <label>End elapsed seconds<input type="number" min={1} max={86400} required value={end} onChange={event => setEnd(Number(event.target.value))} /></label>
      <label>Bucket seconds<input type="number" min={1} max={3600} required value={bucket} onChange={event => setBucket(Number(event.target.value))} /></label>
      <label>Comparison run ID (optional)<input maxLength={128} value={baseline} onChange={event => setBaseline(event.target.value)} /></label>
      <p>Completed archives only. Up to 1,000 buckets and 100,000 selected raw observations. Comparison uses the same elapsed window and filters in both runs.</p>
    </fieldset>}
    <label>Metric<select aria-label="Metric" value={metric} onChange={event => { setMetric(event.target.value); setAggregation(event.target.value === 'observation_count' ? 'count' : 'avg') }}>{Object.entries(population ? { response_time: nativeMetrics.response_time, error_rate: nativeMetrics.error_rate, observation_count: { label: 'Observation count', aggregations: ['count'] } } : nativeMetrics).map(([id, item]) => <option key={id} value={id}>{item.label}</option>)}</select></label>
    <label>Measurement<select aria-label="Measurement" value={aggregation} onChange={event => setAggregation(event.target.value)}>{(metric === 'observation_count' ? ['count'] : nativeMetrics[metric].aggregations).map(value => <option key={value}>{value}</option>)}</select></label>
    <label>Visualization<select aria-label="Visualization" value={kind} onChange={event => setKind(event.target.value)}><option value="line">Line</option><option value="stat">Latest value</option><option value="table">Table</option></select></label>
    <p>Run totals use the selected run time window; archive panels use their saved elapsed window. Saved panels are available to project viewers.</p>
    <button type="submit" disabled={busy || !title.trim()}>{busy ? 'Saving…' : 'Save project panel'}</button>
    {error && <p role="alert">{error}</p>}
  </form></details>
}

export function NativeProjectPanels({ definitions, history, testId }: { definitions: DashboardDefinition[]; history: MetricsHistoryPoint[]; testId?: string }) {
  const [selected, setSelected] = useState('')
  const definition = definitions.find(item => item.id === selected) || definitions[0]
  if (!definition) return null
  return <section className="native-project-panels" aria-label="Project analytics panels"><label>Project dashboard<select aria-label="Project dashboard" value={definition.id} onChange={event => setSelected(event.target.value)}>{definitions.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    {definition.tabs.map(tab => <section key={tab.id} aria-label={tab.title}>{tab.panels.filter(panel => panel.query).map(panel => <article key={panel.id}><h3>{panel.title}</h3>{panel.query?.population ? testId ? <ArchivedNativePanel key={`${testId}-${JSON.stringify(panel.query)}`} panel={panel} testId={testId} /> : <p>Archive run identity unavailable.</p> : <NativeAnalyticsPanel panel={panel} history={history} />}</article>)}</section>)}
  </section>
}
