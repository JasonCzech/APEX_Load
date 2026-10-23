import { freezeBrowserPanelDefinition } from './browserPanelInputs'
import { useEffect, useRef, useState } from 'react'
import ArchivedNativePanel from './ArchivedNativePanel'
import NativeEvidencePanel from './NativeEvidencePanel'
import './NativeAnalyticsPanel.css'
import { Line, LineChart, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import type { MetricsHistoryPoint } from '../types'
import { ANALYTICS_QUERY_VERSION, fetchDashboardDefinition, type DashboardDefinitionPanel, type DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'
import { nativeByteMetrics, nativeJavaHTTPMetrics, nativeBrowserDocumentMetrics, nativeMetrics, nativePopulationMetrics, nativePanelRows } from './nativeAnalyticsModel'
import { formatDateTime } from '../utils/dateTime'

export default function NativeAnalyticsPanel({ panel, history }: { panel: DashboardDefinitionPanel; history: MetricsHistoryPoint[] }) {
  const rows = nativePanelRows(panel, history)
  if (!rows) return <p role="status">This panel requires a query that is unavailable in this view. No unfiltered totals are substituted.</p>
  const metricName = panel.query?.metric || ''
  const metric = nativeMetrics[metricName === 'throughput' ? 'tps' : metricName === 'transaction_duration' ? 'response_time' : metricName]
  const latest = rows[rows.length - 1]?.value
  if (panel.kind === 'stat') return <div><strong>{latest === undefined ? 'No current observation' : latest.toLocaleString(undefined, { maximumFractionDigits: 2 })}</strong><p>{metric.label} · {panel.query?.aggregation} per source interval</p></div>
  if (panel.kind !== 'line' && panel.kind !== 'table') return <p role="status">This panel visualization is unavailable.</p>
  return <ExportableChart allowSVG={panel.kind === 'line'} definitionId={panel.id} title={panel.title} description={`${metric.label}; retained run observations. Gaps remain missing and interval percentiles are not averaged.`} rows={rows} columns={[{ key: 'time', label: 'Time', value: row => row.time }, { key: 'value', label: metric.label, value: row => row.value ?? '' }]}>
    {panel.kind === 'table' ? <table><thead><tr><th>Time</th><th>{metric.label}</th></tr></thead><tbody>{rows.slice(-100).map((row, index) => <tr key={index}><td>{row.time}</td><td>{row.value ?? 'Missing'}</td></tr>)}</tbody></table> : <ResponsiveContainer width="100%" height={240}><LineChart data={rows}><XAxis dataKey="time" /><YAxis /><Tooltip /><Line type="linear" name={metric.label} dataKey="value" stroke="var(--chart-5)" connectNulls={false} dot={false} isAnimationActive={false} /></LineChart></ResponsiveContainer>}
  </ExportableChart>
}

export function NativePanelAuthoring({ onSave, properties = [], runId }: { runId?: string; properties?: string[]; onSave: (definition: Pick<DashboardDefinition, 'name' | 'tabs'>) => Promise<void> }) {
  const [evidenceMode, setEvidenceMode] = useState('')
  const [merged, setMerged] = useState(false)
  const [group, setGroup] = useState('')
  const [maxSeries, setMaxSeries] = useState(10)
  const [separateBrowserTuples, setSeparateBrowserTuples] = useState(false)
  const [freezeInputs, setFreezeInputs] = useState(false)
  const [overlayType, setOverlayType] = useState('')
  const [overlayId, setOverlayId] = useState('')
  const [overlayMetric, setOverlayMetric] = useState('')
  const [overlayAggregation, setOverlayAggregation] = useState('avg')
  const [overlayHost, setOverlayHost] = useState('')
  const [lag, setLag] = useState(0)
  const [population, setPopulation] = useState('')
  const [action, setAction] = useState('')
  const [dimensionFilters, setDimensionFilters] = useState<Record<string, string>>({})
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
  const previewAbort = useRef<AbortController | null>(null)
  useEffect(() => () => { previewAbort.current?.abort(); previewAbort.current = null }, [runId])
  // Browser documents are one measurement tuple of the archive: their
  // filters name the tuple, and they have no action, property or grouping.
  const browserDocuments = population === 'browser_document'
  const javaHTTP = population === 'java_http_action'
  const bytes = population === 'byte'
  const metricTable = javaHTTP ? nativeJavaHTTPMetrics : bytes ? nativeByteMetrics : browserDocuments ? nativeBrowserDocumentMetrics : population ? nativePopulationMetrics : nativeMetrics
  return <details className="native-panel-authoring"><summary>Create project analytics panel</summary><form onSubmit={async event => {
    event.preventDefault(); if (busy) return; setBusy(true); setError('')
    const abort = new AbortController(); previewAbort.current = abort
    try { const definition: Pick<DashboardDefinition, 'name' | 'tabs'> = { name: title.trim(), tabs: [{ id: 'analytics', title: 'Analytics', panels: [{ id: 'metric', title: title.trim(), kind, x: 0, y: 0, w: 24, h: 8, query: { query_version: browserDocuments && separateBrowserTuples && freezeInputs ? 4 : ANALYTICS_QUERY_VERSION, metric, aggregation, max_series: evidenceMode || browserDocuments && separateBrowserTuples ? maxSeries : 1, ...(browserDocuments && separateBrowserTuples ? { browser_tuple_mode: 'separate' as const } : {}), ...(evidenceMode ? { evidence_mode: evidenceMode as 'live' | 'archive', ...(evidenceMode === 'live' && merged ? { live_bucket_mode: 'merged' as const } : {}), dimensions: group ? [group] : [], ...(overlayType ? { overlay_selection: { ...(overlayType === 'dataset' ? { dataset_id: overlayId.trim() } : { source_id: overlayId.trim() }), metric: overlayMetric.trim(), aggregation: overlayAggregation, host: overlayType === 'monitor' ? overlayHost.trim() || undefined : undefined, max_lag_seconds: lag } } : {}) } : {}), ...(population ? { population: population as 'action' | 'transaction' | 'browser_document' | 'java_http_action' | 'byte', filters: { ...dimensionFilters, ...(action && !browserDocuments ? { action } : {}) }, start_seconds: start, end_seconds: end, bucket_seconds: bucket, comparison_run_id: evidenceMode ? undefined : baseline.trim() || undefined, ...(population === 'action' && property ? { property_name: property.split('=')[0], property_value: evidenceMode && group === 'property' ? undefined : property.slice(property.indexOf('=') + 1) } : {}) } : {}) } }] }] }; const prepared = await freezeBrowserPanelDefinition(runId, definition, abort.signal); abort.signal.throwIfAborted(); await onSave(prepared) } catch (reason) { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to save panel') } finally { if (previewAbort.current === abort) previewAbort.current = null; setBusy(false) }
  }}>
    <label>Panel title<input required maxLength={256} value={title} onChange={event => setTitle(event.target.value)} /></label>
    <label>Evidence workflow<select aria-label="Evidence workflow" value={evidenceMode} onChange={event => { const mode = event.target.value; setEvidenceMode(mode); setDimensionFilters({}); if (mode) { setPopulation('action'); setMetric('response_time'); setAggregation('avg'); setGroup('action') } else { setGroup(''); setOverlayType('') } }}><option value="">Run totals / archive comparison</option><option value="live">Filtered live intervals</option><option value="archive">Grouped archive and overlays</option></select></label>
    <label>Evidence population<select aria-label="Evidence population" value={population} onChange={event => { const next = event.target.value; setPopulation(next); setProperty(''); setGroup('action'); if (next === 'java_http_action') { setMetric('java_http_input_bytes'); setAggregation('avg'); setAction(''); setDimensionFilters({}); setGroup(''); setOverlayType('') } else if (next === 'byte') { setMetric('bytes_sent'); setAggregation('sum'); setDimensionFilters({}); setGroup(''); setOverlayType('') } else if (next === 'browser_document') { setMetric('document_lcp'); setAggregation('p75'); setAction(''); setDimensionFilters({ frame_scope: 'main_frame', lifecycle_phase: 'navigation' }) } else { setMetric(next ? 'response_time' : 'tps'); setAggregation('avg'); setDimensionFilters({}) } }}>{!evidenceMode && <option value="">Retained run totals</option>}<option value="action">{evidenceMode === 'live' ? 'Live actions' : 'Archived actions'}</option><option value="transaction">{evidenceMode === 'live' ? 'Live business transactions' : 'Archived business transactions'}</option>{!evidenceMode && <><option value="browser_document">Archived browser documents</option><option value="java_http_action">Archived Java representations</option><option value="byte">Archived HTTP body bytes</option></>}</select></label>
    {population && <fieldset><legend>Evidence selection</legend>
      {browserDocuments ? <>
        <label><input type="checkbox" checked={separateBrowserTuples} onChange={event => setSeparateBrowserTuples(event.target.checked)} />Show separate browser cohorts</label>
        {separateBrowserTuples && <label><input type="checkbox" checked={freezeInputs} disabled={!runId} onChange={event => setFreezeInputs(event.target.checked)} />Freeze archive inputs for this panel</label>}
        {separateBrowserTuples && freezeInputs && <p>Saving evaluates the current and selected comparison archives. The saved panel keeps those exact publications and tuple identities when opened from another run. Changed or unavailable inputs require an explicit new save.</p>}
        {separateBrowserTuples ? <><p>Each complete measurement tuple has its own series. Different engines, profiles, runtime cohorts and document lifecycles are never pooled. Limits fail explicitly.</p><label>Maximum browser series<input type="number" required min={1} max={20} value={maxSeries} onChange={event => setMaxSeries(Number(event.target.value))} /></label></> : <p>A browser panel reads one measurement tuple: name the engine, device profile and group (and region or runtime cohort when several share them).</p>}
        {[['engine', 'Engine'], ['device_profile', 'Device profile'], ['group', 'Group'], ['region', 'Region'], ['cohort', 'Runtime cohort']].map(([key, label]) => <label key={key}>{label}<input aria-label={`${label} filter`} maxLength={key === 'cohort' ? 16 : 256} value={dimensionFilters[key] || ''} onChange={event => setDimensionFilters(current => { const next = { ...current }; if (event.target.value.trim()) next[key] = event.target.value.trim(); else delete next[key]; return next })} /></label>)}
        <label>Frame scope<select aria-label="Frame scope" value={dimensionFilters.frame_scope || 'main_frame'} onChange={event => setDimensionFilters(current => ({ ...current, frame_scope: event.target.value }))}><option value="main_frame">Main frame</option><option value="child_frame">Child frames</option></select></label>
        <label>Lifecycle<select aria-label="Lifecycle" value={dimensionFilters.lifecycle_phase || 'navigation'} onChange={event => setDimensionFilters(current => ({ ...current, lifecycle_phase: event.target.value }))}><option value="navigation">Navigations</option><option value="bfcache_restore">Back-forward cache restores</option></select></label>
      </> : <>
      <label>{bytes ? 'Exact action name' : 'Exact action or transaction name'}<input aria-label="Exact action or transaction name" required={javaHTTP} maxLength={256} value={action} onChange={event => setAction(event.target.value)} /></label>
      {population === 'action' && <label>Property category<select aria-label="Property category" value={property} onChange={event => setProperty(event.target.value)}><option value="">All categories</option>{properties.map(value => <option key={value}>{value}</option>)}</select></label>}
      {!javaHTTP && !bytes && ['network_profile', 'hierarchy', 'phase'].map(key => <label key={key}>{key.replace(/_/g, ' ')} filter<input aria-label={`${key} filter`} maxLength={256} value={dimensionFilters[key] || ''} onChange={event => setDimensionFilters(current => { const next = { ...current }; if (event.target.value.trim()) next[key] = event.target.value.trim(); else delete next[key]; return next })} /></label>)}
      </>}
      <label>Start elapsed seconds<input type="number" min={0} max={86399} required value={start} onChange={event => setStart(Number(event.target.value))} /></label>
      <label>End elapsed seconds<input type="number" min={1} max={86400} required value={end} onChange={event => setEnd(Number(event.target.value))} /></label>
      {evidenceMode === 'live' && <label><input type="checkbox" checked={merged} onChange={event => setMerged(event.target.checked)} />Merge live intervals into buckets</label>}
      <label>Bucket seconds<input type="number" min={1} max={3600} required value={bucket} onChange={event => setBucket(Number(event.target.value))} /></label>
      {!evidenceMode && <label>Comparison run ID (optional)<input maxLength={128} value={baseline} onChange={event => setBaseline(event.target.value)} /></label>}
      {evidenceMode && <><label>Group series by<select aria-label="Group series by" value={group} onChange={event => { setGroup(event.target.value); if (evidenceMode === 'live' && ['hierarchy', 'phase'].includes(event.target.value)) setMerged(true) }}><option value="">No grouping</option><option value="action">Action / transaction identity</option>{population === 'action' && <option value="property">Selected property name</option>}<option value="region">Region</option><option value="dependency">Dependency</option><option value="network_profile">Network profile</option><option value="hierarchy">Hierarchy</option><option value="phase">Lifecycle phase</option></select></label><label>Maximum series<input type="number" min={1} max={20} value={maxSeries} onChange={event => setMaxSeries(Number(event.target.value))} /></label><p>Source mode retains publication intervals. Merged mode combines whole distributions by publication end time, supports action/property intersections and action/network/hierarchy/lifecycle intersections from version 3 runtimes and aligned lag correlation, and shows collection coverage. Boundary attribution is approximate at source-window resolution. Live regions describe business transactions and dependencies describe actions.</p></>}
      <p>{javaHTTP ? 'Java representations use each measurement’s own observations; paired ratios divide sums from the same samples. Missing measurements remain unavailable. Archives allow up to 1,000 buckets and 100,000 selected raw observations.' : bytes ? 'HTTP body bytes on the wire per request, bucketed by request start. Sums and rates (bytes per bucket second) need every request of a bucket measured and every source collected; requests archived without byte evidence remain unavailable. Archives allow up to 1,000 buckets and 100,000 selected raw observations.' : 'Rates describe retained observations per elapsed wall second. Missing buckets remain unavailable; merged rates require full source coverage. Archives: up to 1,000 buckets and 100,000 selected raw observations. Live: newest 1,000 retained intervals.'} Limits fail explicitly.</p>
    </fieldset>}
    {evidenceMode && <fieldset><legend>Monitor or imported evidence</legend><label>Overlay source<select aria-label="Overlay source" value={overlayType} onChange={event => setOverlayType(event.target.value)}><option value="">None</option><option value="monitor">Run monitor</option><option value="dataset">Associated imported dataset</option></select></label>{overlayType && <><label>Source or dataset ID<input required maxLength={128} value={overlayId} onChange={event => setOverlayId(event.target.value)} /></label><label>Overlay metric<input required maxLength={256} value={overlayMetric} onChange={event => setOverlayMetric(event.target.value)} /></label><label>Overlay aggregation<select value={overlayAggregation} onChange={event => setOverlayAggregation(event.target.value)}>{['avg', 'min', 'max', 'sum', 'count', 'rate', 'p50', 'p95', 'p99'].map(value => <option key={value}>{value}</option>)}</select></label>{overlayType === 'monitor' && <label>Monitor host (optional)<input maxLength={256} value={overlayHost} onChange={event => setOverlayHost(event.target.value)} /></label>}<label>Maximum correlation lag seconds<input type="number" min={0} max={3600} step={bucket} value={lag} onChange={event => setLag(Number(event.target.value))} /></label><p>Datasets must be explicitly associated with this run. Each measurement retains its unit axis. Lag correlation uses archive or merged live buckets.</p></>}</fieldset>}
    <label>Metric<select aria-label="Metric" value={metric} onChange={event => { setMetric(event.target.value); setAggregation(metricTable[event.target.value].aggregations[0]) }}>{Object.entries(metricTable).map(([id, item]) => <option key={id} value={id}>{item.label}</option>)}</select></label>
    <label>Measurement<select aria-label="Measurement" value={aggregation} onChange={event => setAggregation(event.target.value)}>{(metricTable[metric] ?? Object.values(metricTable)[0]).aggregations.map(value => <option key={value} value={value}>{value === 'ratio_of_sums' ? 'Ratio of paired sums' : value}</option>)}</select></label>
    <label>Visualization<select aria-label="Visualization" value={kind} onChange={event => setKind(event.target.value)}><option value="line">Line</option><option value="stat">Latest value</option><option value="table">Table</option></select></label>
    <p>Run totals use the selected run time window; archive panels use their saved elapsed window. Saved panels are available to project viewers.</p>
    <button type="submit" disabled={busy || !title.trim() || javaHTTP && !action.trim()}>{busy ? 'Saving…' : 'Save project panel'}</button>
    {error && <p role="alert">{error}</p>}
  </form></details>
}

export function NativeProjectPanels({ definitions, history, testId }: { definitions: DashboardDefinition[]; history: MetricsHistoryPoint[]; testId?: string }) {
  const [selected, setSelected] = useState('')
  const current = definitions.find(item => item.id === selected) || definitions[0]
  // A viewer may reopen exactly a saved revision of the dashboard.
  const [revision, setRevision] = useState(0)
  const [retained, setRetained] = useState<NonNullable<DashboardDefinition['retained_revisions']>>([])
  // The definition as fetched when it was selected: newer than the list when
  // someone saved since the list was loaded.
  const [latest, setLatest] = useState<DashboardDefinition | null>(null)
  const [opened, setOpened] = useState<DashboardDefinition | null>(null)
  const [revisionError, setRevisionError] = useState('')
  const currentID = current?.id || ''
  useEffect(() => {
    setRevision(0)
    setOpened(null)
    setLatest(null)
    setRetained([])
    setRevisionError('')
    if (!currentID) return
    const controller = new AbortController()
    fetchDashboardDefinition(currentID, undefined, controller.signal)
      .then(item => { if (!controller.signal.aborted) { setLatest(item); setRetained(item.retained_revisions || []) } })
      .catch(() => { if (!controller.signal.aborted) setRetained([]) })
    return () => controller.abort()
  }, [currentID])
  useEffect(() => {
    if (!currentID || !revision) return
    const controller = new AbortController()
    fetchDashboardDefinition(currentID, revision, controller.signal)
      .then(item => { if (!controller.signal.aborted) setOpened(item) })
      .catch(reason => { if (!controller.signal.aborted) { setOpened(null); setRevisionError(reason instanceof Error ? reason.message : 'Unable to open the saved revision') } })
    return () => controller.abort()
  }, [currentID, revision])
  if (!current) return null
  const head = latest && latest.id === current.id ? latest : current
  const past = revision && opened && opened.id === current.id && opened.revision === revision && opened.revision !== head.revision ? opened : null
  const definition = past || head
  return <section className="native-project-panels" aria-label="Project analytics panels"><label>Project dashboard<select aria-label="Project dashboard" value={current.id} onChange={event => setSelected(event.target.value)}>{definitions.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    {retained.length > 1 && <label>Saved revision<select aria-label="Saved revision" value={revision || head.revision} onChange={event => { setRevisionError(''); setRevision(Number(event.target.value) === head.revision ? 0 : Number(event.target.value)) }}>{retained.map(item => <option key={item.revision} value={item.revision}>{item.revision === head.revision ? `Current (revision ${item.revision})` : `Revision ${item.revision} · ${formatDateTime(item.saved_at, { seconds: true })}`}</option>)}</select></label>}
    {past && <p role="status">Showing saved revision {past.revision} of “{past.name}”, exactly as it was saved. The current revision is {past.current_revision ?? head.revision}.</p>}
    {revisionError && <p role="alert">{revisionError}</p>}
    {definition.tabs.map(tab => <section key={tab.id} aria-label={tab.title}>{tab.panels.filter(panel => panel.query).map(panel => <article key={panel.id}><h3>{panel.title}</h3>{panel.query?.evidence_mode ? testId ? <NativeEvidencePanel key={`${testId}-${JSON.stringify(panel.query)}`} panel={panel} testId={testId} /> : <p>Run identity unavailable.</p> : panel.query?.population ? testId ? <ArchivedNativePanel key={`${testId}-${JSON.stringify(panel.query)}`} panel={panel} testId={testId} /> : <p>Archive run identity unavailable.</p> : <NativeAnalyticsPanel panel={panel} history={history} />}</article>)}</section>)}
  </section>
}
