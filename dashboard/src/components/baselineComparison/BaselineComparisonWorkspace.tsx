import BaselineBrowserDocuments from './BaselineBrowserDocuments'
import BaselineTelemetryEvidence from './BaselineTelemetryEvidence'
import BaselineTelemetryCompatibility from './BaselineTelemetryCompatibility'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { TestSummary } from '../../types'
import { formatDateTime } from '../../utils/dateTime'
import { downloadBlob } from '../../utils/fileUtils'
import { request } from '../../hooks/apiClient'
import type { DashboardDefinition } from '../../hooks/api/useDashboardDefinitionsApi'
import { freezeBrowserPanelDefinition } from '../browserPanelInputs'
import { comparisonColors, comparisonMagnitude, comparisonApi, comparisonVerdictLabel, type SavedComparisonSummary, comparisonRequestSignature, defaultPolicy, difference, measurementValue, metricOptions, metricUnit, number, resourceProviderLabel, verdictBlockers, withUnit, type ComparisonPolicy, type ComparisonRequest, type ComparisonRow, type ComparisonSnapshot, type MeasurementWindow, type Metric, type ResourceMapping, type RunIdentity } from './model'
import { ComparisonDetail } from './ComparisonDetail'
import type { ReportLens } from '../../appRouting'
import { REPORT_LENS_LABELS } from '../runReport/reportLensLabels'
import './BaselineComparisonWorkspace.css'

// onAnalyzeRootCause analyzes the run's root cause against one baseline and
// resolves to what happened.
// lens is the report audience the page shows; the comparison's exports use it.
type Props = { test: TestSummary; initialBaseline?: string; comparisonId?: string; canEditPolicy?: boolean; lens?: ReportLens; onSnapshot: (id: string) => void; onAnalyzeRootCause?: (baselineId: string) => Promise<string> }
const thresholdFields = [['load_tolerance_pct', 'Load tolerance %'], ['latency_pct', 'Latency change %'], ['latency_ms', 'Minimum latency change (ms)'], ['throughput_pct', 'Throughput change %'], ['error_points', 'Error change (percentage points)'], ['resource_pct', 'Resource change %']] as const
const errorText = (e: unknown) => e instanceof Error ? e.message : 'The comparison could not be loaded.'

export default function BaselineComparisonWorkspace({ test, initialBaseline, comparisonId, canEditPolicy = false, lens = 'engineer', onSnapshot, onAnalyzeRootCause }: Props) {
  const [rcaBusy, setRcaBusy] = useState(false)
  const [rcaMessage, setRcaMessage] = useState<{ text: string; error: boolean } | null>(null)
  const workspaceRef = useRef<HTMLDivElement>(null)
  const [contextTop, setContextTop] = useState(84)
  const [candidates, setCandidates] = useState<RunIdentity[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [query, setQuery] = useState('')
  const [other, setOther] = useState(false)
  const [selected, setSelected] = useState<string[]>(initialBaseline ? [initialBaseline] : [])
  const [policyLoading, setPolicyLoading] = useState(Boolean(test.scenario_id && !comparisonId))
  const [policy, setPolicy] = useState<ComparisonPolicy>(defaultPolicy)
  const [windows, setWindows] = useState<Record<string, MeasurementWindow>>({})
  const [minimumLoad, setMinimumLoad] = useState(0)
  const [maximumLoad, setMaximumLoad] = useState(0)
  const [population, setPopulation] = useState<ComparisonRequest['population']>('transaction')
  const [regressionMetrics, setRegressionMetrics] = useState<string[]>([])
  const [regressionProtocols, setRegressionProtocols] = useState('')
  const [regressionGroups, setRegressionGroups] = useState('')
  const [regressionIdentities, setRegressionIdentities] = useState('')
  const [snapshot, setSnapshot] = useState<ComparisonSnapshot | null>(null)
  const [loading, setLoading] = useState(false)
  const [snapshotLoading, setSnapshotLoading] = useState(Boolean(comparisonId))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [view, setView] = useState<'transactions' | 'resources' | 'setup'>('transactions')
  const [search, setSearch] = useState('')
  const [metric, setMetric] = useState<Metric>('p95')
  const [sort, setSort] = useState<'name' | 'change'>('change')
  const [detail, setDetail] = useState<ComparisonRow | null>(null)
  const [allFindings, setAllFindings] = useState(false)
  const [mobileBaseline, setMobileBaseline] = useState('')
  const [resourceDraft, setResourceDraft] = useState<ResourceMapping>({ run_id: test.id, keys: [], identity: '', unit: '', kind: 'gauge', aggregation: 'mean', confirmed: true })
  const [groupRun, setGroupRun] = useState('')
  const [groupFrom, setGroupFrom] = useState('')
  const [groupTo, setGroupTo] = useState('')
  const [panelTitle, setPanelTitle] = useState(`${test.name || test.id} browser comparison`)
  const [savedPanel, setSavedPanel] = useState<DashboardDefinition | null>(null)
  const defaultSelected = useRef(Boolean(initialBaseline || comparisonId))
  const evaluation = useRef<AbortController | null>(null)
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const topbar = workspaceRef.current?.closest('.main-content')?.querySelector<HTMLElement>('.main-topbar')
    if (!topbar) return
    const update = () => setContextTop(topbar.offsetHeight + 8)
    update()
    const observer = new ResizeObserver(update)
    observer.observe(topbar)
    return () => observer.disconnect()
  }, [])
  useEffect(() => () => evaluation.current?.abort(), [])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    comparisonApi.candidates(test.id, other, query, offset, controller.signal).then(result => {
      if (controller.signal.aborted) return
      setCandidates(result.runs); setTotal(result.total)
      if (!defaultSelected.current && !query) {
        const before = new Date(test.started_at || test.created_at).getTime()
        const candidate = result.suggested_baseline === undefined ? result.runs.find(run => run.status === 'COMPLETE' && run.archive_available && run.completed_at && new Date(run.completed_at).getTime() < before) : result.suggested_baseline
        if (candidate) { setSelected([candidate.id]); defaultSelected.current = true }
      }
    }).catch(e => { if (!controller.signal.aborted) setError(errorText(e)) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [test.id, test.started_at, test.created_at, other, query, offset])
  useEffect(() => {
    if (!test.scenario_id || comparisonId) { setPolicyLoading(false); return }
    setPolicyLoading(true)
    const controller = new AbortController()
    comparisonApi.policy(test.scenario_id, controller.signal).then(result => { if (!controller.signal.aborted) setPolicy(result) }).catch(e => { if (!controller.signal.aborted) setNotice(`Using defaults: ${errorText(e)}`) }).finally(() => { if (!controller.signal.aborted) setPolicyLoading(false) })
    return () => controller.abort()
  }, [test.scenario_id, comparisonId])
  useEffect(() => {
    if (!comparisonId || snapshot?.id === comparisonId) { setSnapshotLoading(false); return }
    setSnapshot(null); setDetail(null); setError(''); setSnapshotLoading(true)
    evaluation.current?.abort(); setBusy(false)
    const controller = new AbortController()
    setLoading(true)
    comparisonApi.snapshot(test.id, comparisonId, controller.signal).then(result => {
      if (controller.signal.aborted) return
      setMinimumLoad(result.request.minimum_load || 0); setMaximumLoad(result.request.maximum_load || 0); setSnapshot(result); setSelected(result.request.baseline_ids); setPolicy(result.policy); setWindows(result.request.windows || {}); setOther(result.request.allow_other_scenarios); setPopulation(result.request.population); setRegressionMetrics(result.request.regression_metrics || []); setRegressionProtocols((result.request.regression_protocols || []).join(', ')); setRegressionGroups((result.request.regression_groups || []).join(', ')); setRegressionIdentities((result.request.regression_identities || []).join('\n'))
    }).catch(e => { if (!controller.signal.aborted) setError(errorText(e)) }).finally(() => { if (!controller.signal.aborted) { setLoading(false); setSnapshotLoading(false) } })
    return () => controller.abort()
  }, [test.id, comparisonId, snapshot?.id])

  const [savedComparisons, setSavedComparisons] = useState<SavedComparisonSummary[] | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    comparisonApi.saved(test.id, controller.signal).then(result => { if (!controller.signal.aborted) setSavedComparisons(result.comparisons || []) }).catch(() => { if (!controller.signal.aborted) setSavedComparisons([]) })
    return () => controller.abort()
  }, [test.id, snapshot?.id])

  const runNames = new Map([...candidates, ...(snapshot?.baselines || [])].map(run => [run.id, `${run.release_name || 'No release'} · ${run.name}`]))
  const rows = useMemo(() => {
    const source = view === 'resources' ? snapshot?.resources : snapshot?.transactions
    const value = view === 'resources' ? 'mean' : metric
    const magnitude = (row: ComparisonRow) => comparisonMagnitude(row, value, view === 'resources')
    return [...(source || [])].filter(row => `${row.name} ${row.group} ${row.source} ${resourceProviderLabel(row)}`.toLowerCase().includes(search.toLowerCase())).sort((a, b) => sort === 'name' ? a.name.localeCompare(b.name) : magnitude(b) - magnitude(a))
  }, [snapshot, view, metric, search, sort])
  const virtual = useVirtualizer({ count: rows.length, getScrollElement: () => listRef.current, estimateSize: () => 76, overscan: 8 })
  const virtualItems = virtual.getVirtualItems()
  const visibleRows = virtualItems.length ? virtualItems : rows.slice(0, 12).map((_, index) => ({ index, start: index * 76, end: (index + 1) * 76, size: 76, key: index }))
  const shownBaselines = snapshot?.baselines || []
  // Each compared baseline is "Baseline n" in its column colour wherever it is
  // repeated (findings, setup rows, acceptance evidence); the key names it in
  // full once, and the tag's tooltip does too.
  const baselineTag = (id: string) => {
    const index = shownBaselines.findIndex(run => run.id === id)
    if (index < 0) return <>{runNames.get(id) || id}</>
    return <span className="bc-baseline-tag" title={runNames.get(id) || id}><i aria-hidden="true" style={{ background: comparisonColors[index + 1] }} />Baseline {index + 1}</span>
  }
  const effectiveMobile = shownBaselines.some(run => run.id === mobileBaseline) ? mobileBaseline : shownBaselines[0]?.id
  const requestBody: ComparisonRequest = { regression_identities: regressionIdentities ? regressionIdentities.split('\n').map(v => v.trim()).filter(Boolean) : undefined, regression_metrics: regressionMetrics.length ? regressionMetrics : undefined, regression_protocols: regressionProtocols ? regressionProtocols.split(',').map(v => v.trim()).filter(Boolean) : undefined, regression_groups: regressionGroups ? regressionGroups.split(',').map(v => v.trim()).filter(Boolean) : undefined, baseline_ids: selected, policy, windows: Object.fromEntries(Object.entries(windows).filter(([id]) => id === test.id || selected.includes(id))), population, allow_other_scenarios: other, minimum_load: minimumLoad || undefined, maximum_load: maximumLoad || undefined }
  const dirty = snapshot && comparisonRequestSignature({ ...snapshot.request, policy: snapshot.policy }) !== comparisonRequestSignature(requestBody)

  async function apply() {
    evaluation.current?.abort(); const controller = new AbortController(); evaluation.current = controller
    setBusy(true); setError(''); setNotice('')
    try { const result = await comparisonApi.evaluate(test.id, requestBody, controller.signal); if (controller.signal.aborted) return; setSnapshot(result); setDetail(null); onSnapshot(result.id) }
    catch (e) { if (!controller.signal.aborted) setError(errorText(e)) }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }
  async function savePolicy() {
    if (!test.scenario_id) return
    setBusy(true); setError('')
    try { const saved = await comparisonApi.savePolicy(test.scenario_id, policy); setPolicy(saved); setNotice(`Scenario policy saved as revision ${saved.revision}. Apply comparison to evaluate it.`) }
    catch (e) { setError(errorText(e)) }
    finally { setBusy(false) }
  }
  async function download(format: string) {
    if (!snapshot) return
    setBusy(true); setError('')
    try { await downloadBlob(await comparisonApi.download(test.id, snapshot.id, format, lens), `comparison-${snapshot.id}.${format === 'evidence_csv' ? 'csv' : format}`) }
    catch (e) { setError(errorText(e)) }
    finally { setBusy(false) }
  }
  async function saveToPanel() {
    if (!snapshot || !shownBaselines.length) return
    const baselineRunId = effectiveMobile || shownBaselines[0].id
    const title = panelTitle.trim() || `${test.name || test.id} browser comparison`
    const runWindow = snapshot.request.windows?.[test.id]
    const rawStartSec = Math.floor(Number(runWindow?.start_seconds) || 0)
    const startSec = Math.max(0, Math.min(86399, rawStartSec))
    const rawEndSec = Math.floor(Number(runWindow?.end_seconds) || 0)
    const endSec = rawEndSec > startSec ? Math.min(86400, rawEndSec) : Math.min(86400, startSec + 60)
    const bucketSec = Math.max(10, Math.min(3600, Math.ceil((endSec - startSec) / 1000)))
    const firstDoc = snapshot.browser_documents?.find(doc => doc.baseline_id === baselineRunId) ?? snapshot.browser_documents?.[0]
    const panelMetric = firstDoc?.metric && ['fcp', 'lcp', 'cls', 'inp'].includes(firstDoc.metric) ? `document_${firstDoc.metric}` : 'document_lcp'
    setBusy(true); setError(''); setNotice('')
    try {
      const draft: Pick<DashboardDefinition, 'name' | 'tabs'> = {
        name: title,
        tabs: [{
          id: 'main',
          title: 'Main',
          panels: [{
            id: 'browser-comparison',
            title,
            kind: 'line',
            x: 0,
            y: 0,
            w: 24,
            h: 8,
            query: {
              query_version: 5,
              browser_tuple_mode: 'separate',
              population: 'browser_document',
              metric: panelMetric,
              aggregation: 'p75',
              comparison_run_id: baselineRunId,
              start_seconds: startSec,
              end_seconds: endSec,
              bucket_seconds: bucketSec,
              max_series: 20,
              filters: {
                frame_scope: firstDoc?.frame_scope || 'main_frame',
                lifecycle_phase: firstDoc?.lifecycle_phase || 'navigation',
              },
            },
          }],
        }],
      }
      const frozen = await freezeBrowserPanelDefinition(test.id, draft)
      const created = await request<DashboardDefinition>('/monitoring/dashboard-definitions', {
        method: 'POST',
        body: JSON.stringify({
          project_id: snapshot.project_id || test.project_id || 'default',
          name: frozen.name,
          visibility: 'project',
          tabs: frozen.tabs,
        }),
      })
      setSavedPanel(created)
      setNotice(`Saved panel "${created.name}" (${created.id}, revision ${created.revision}).`)
    } catch (e) {
      setError(errorText(e))
    } finally {
      setBusy(false)
    }
  }
  function toggle(id: string) { defaultSelected.current = true; setSelected(old => old.includes(id) ? old.filter(value => value !== id) : old.length < 5 ? [...old, id] : old) }
  const findings = allFindings ? snapshot?.findings || [] : (snapshot?.findings || []).slice(0, 8)
  return <div ref={workspaceRef} className="bc-workspace">
    {snapshot && <aside style={{ top: contextTop }} className="bc-persistent-context" aria-label="Applied comparison context"><strong>{snapshot.baselines.length} {snapshot.baselines.length === 1 ? 'baseline' : 'baselines'}</strong><span>{snapshot.request.minimum_load || 0}–{snapshot.request.maximum_load || 'all'} observed VUsers · ±{snapshot.policy.load_tolerance_pct}%</span><span>{Object.keys(snapshot.request.windows || {}).length ? 'Custom measurement windows' : 'Stable windows across each run'} · policy {snapshot.policy.revision || 'defaults'}</span><span>{dirty ? 'Draft changes unapplied' : 'Saved assessment'}</span></aside>}
    <section className="bc-context run-report-panel" aria-label="Baseline comparison controls">
      <header><div><p className="bc-eyebrow">Release over release</p><h2>Baseline comparison</h2><p>Explain what changed, at comparable load. Findings are advisory.</p></div><div className="bc-header-actions"><button type="button" className="btn btn-primary" disabled={busy || policyLoading || snapshotLoading || selected.length === 0} onClick={() => void apply()}>{busy ? 'Working…' : snapshot ? 'Apply comparison' : 'Compare selected baselines'}</button>
        {onAnalyzeRootCause && <button type="button" className="btn btn-secondary" disabled={rcaBusy || selected.length !== 1} title={selected.length === 1 ? undefined : 'Select exactly one baseline to analyze the root cause against it.'}
          onClick={() => { setRcaBusy(true); setRcaMessage(null); void onAnalyzeRootCause(selected[0]).then(text => setRcaMessage({ text, error: false }), (err: unknown) => setRcaMessage({ text: err instanceof Error ? err.message : 'The analysis could not be started', error: true })).finally(() => setRcaBusy(false)) }}>
          {rcaBusy ? 'Starting…' : 'Analyze root cause against this baseline'}</button>}
        {rcaMessage && <span className={rcaMessage.error ? 'run-report-error' : 'run-report-muted'} role={rcaMessage.error ? 'alert' : 'status'}>{rcaMessage.text}</span>}</div></header>
      <div className="bc-chips" aria-label="Selected baselines">{selected.map((id, i) => <span key={id} style={{ borderLeft: `4px solid ${comparisonColors[i + 1]}` }}>{runNames.get(id) || id}<button type="button" aria-label={`Remove baseline ${runNames.get(id) || id}`} disabled={busy} onClick={() => toggle(id)}>×</button></span>)}<small>{selected.length}/5 baselines</small></div>
      <details open={!snapshot}><summary>Select pinned baselines</summary><fieldset disabled={busy || policyLoading || snapshotLoading}><div className="bc-controls"><label>Find baseline<input type="search" autoComplete="off" value={query} onChange={e => { setQuery(e.target.value); setOffset(0) }} placeholder="Run, release, or scenario" /></label><label className="bc-check"><input type="checkbox" checked={other} onChange={e => { setOther(e.target.checked); setOffset(0) }} />Include other scenarios in this application</label></div>
        {loading && <p role="status">Loading pinned baselines…</p>}
        {!loading && !candidates.length && <p>No pinned baselines found. Pin a completed run from Results, or include other scenarios.</p>}
        <div className="bc-candidates">{[...new Set(candidates.map(run => run.release_name || 'No release'))].map(release => <section key={release}><h3>{release}</h3>{candidates.filter(run => (run.release_name || 'No release') === release).map(run => <label key={run.id} className="bc-candidate"><input type="checkbox" checked={selected.includes(run.id)} disabled={!run.archive_available || !selected.includes(run.id) && selected.length >= 5} onChange={() => toggle(run.id)} /><span><strong>{run.name}</strong><small>{run.completed_at ? formatDateTime(run.completed_at) : 'Completion time unavailable'} · {run.status} · scenario {run.scenario_id || 'unassigned'}</small><small>{run.vusers} configured VUsers · {run.peak_vusers || 'unverified'} observed peak · {run.archive_available ? 'Archive available' : 'Archive unavailable'}</small></span></label>)}</section>)}</div>
        <div className="bc-controls"><button type="button" className="btn btn-secondary" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>Previous baselines</button><span>{Math.min(offset + 1, total)}–{Math.min(offset + 50, total)} of {total}</span><button type="button" className="btn btn-secondary" disabled={offset + 50 >= total} onClick={() => setOffset(offset + 50)}>Next baselines</button></div>
      </fieldset></details>
      <details><summary>Load alignment, thresholds and mappings · policy revision {policy.revision || 'defaults'}</summary><fieldset disabled={busy || policyLoading || snapshotLoading}><div className="bc-controls"><label>Population<select value={population} onChange={e => setPopulation(e.target.value as ComparisonRequest['population'])}><option value="transaction">Named transactions</option><option value="action">Requests / actions</option><option value="unknown">Legacy / unclassified</option></select></label>{thresholdFields.map(([key, label]) => <label key={key}>{label}<input type="number" min="0" max={key === 'load_tolerance_pct' ? 25 : undefined} step="0.1" value={policy[key]} onChange={e => setPolicy(old => ({ ...old, [key]: Number(e.target.value) }))} /></label>)}</div>
        <h3>Regression acceptance</h3><p>Default: successful P95, successful throughput and error rate. Missing evidence prevents a pass. Advisory findings remain separate.</p><div className="bc-controls"><label>Acceptance metrics<select multiple aria-label="Acceptance metrics" value={regressionMetrics} onChange={e => setRegressionMetrics(Array.from(e.target.selectedOptions, o => o.value))}>{['successful_mean', 'successful_p95', 'successful_p99', 'successful_throughput', 'error_rate_pct', 'consumer_lag_ms', 'consumer_offset_lag', 'acknowledgement_ms', 'grpc_call_p95', 'grpc_first_message_p95', 'websocket_handshake_p95', 'websocket_write_wait_p95', 'websocket_ping_rtt_p95', 'grpc_encode_p95', 'grpc_decode_p95', 'grpc_message_throughput', 'websocket_message_throughput', 'websocket_reconnect_p95', 'websocket_send_p95', 'websocket_receive_p95', 'websocket_text_receive_p95', 'websocket_binary_receive_p95'].map(value => <option key={value} value={value}>{value.replace(/_/g, ' ')}</option>)}</select></label><label>Acceptance identities (one per line)<textarea value={regressionIdentities} onChange={e => setRegressionIdentities(e.target.value)} /></label><label>Acceptance protocols (comma separated)<input value={regressionProtocols} onChange={e => setRegressionProtocols(e.target.value)} /></label><label>Acceptance groups (comma separated)<input value={regressionGroups} onChange={e => setRegressionGroups(e.target.value)} /></label></div>
        <p>0% load tolerance selects exact matches. Stable windows exclude the first 30 seconds and require 60 further seconds. Resource evidence requires 10 samples and 80% coverage; latency requires 100 successful samples, or 1,000 for P99.</p>
        <div className="bc-controls"><label>Minimum observed VUsers<input type="number" min="0" value={minimumLoad} onChange={e => setMinimumLoad(Number(e.target.value))} /></label><label>Maximum observed VUsers (0 = all)<input type="number" min="0" value={maximumLoad} onChange={e => setMaximumLoad(Number(e.target.value))} /></label></div><h3>Measurement windows</h3><div className="bc-window-grid">{[test.id, ...selected].map(id => <div key={id}><strong>{id === test.id ? 'Current run' : runNames.get(id) || id}</strong><label>Start seconds<input type="number" min="0" value={windows[id]?.start_seconds || 0} onChange={e => setWindows(old => ({ ...old, [id]: { start_seconds: Number(e.target.value), end_seconds: old[id]?.end_seconds || 0 } }))} /></label><label>End seconds (0 = whole run)<input type="number" min="0" value={windows[id]?.end_seconds || 0} onChange={e => setWindows(old => ({ ...old, [id]: { start_seconds: old[id]?.start_seconds || 0, end_seconds: Number(e.target.value) } }))} /></label></div>)}</div>
        <details><summary>Map groups from copied scenarios</summary><div className="bc-controls"><label>Baseline<select value={groupRun} onChange={e => setGroupRun(e.target.value)}><option value="">Select a run</option>{selected.map(id => <option key={id} value={id}>{runNames.get(id) || id}</option>)}</select></label><label>Recorded baseline group<input value={groupFrom} onChange={e => setGroupFrom(e.target.value)} /></label><label>Current group<input value={groupTo} onChange={e => setGroupTo(e.target.value)} /></label><button type="button" className="btn btn-secondary" disabled={!groupRun || !groupFrom || !groupTo} onClick={() => { setPolicy(old => ({ ...old, group_mappings: { ...old.group_mappings, [`${groupRun}/${groupFrom}`]: groupTo } })); setGroupFrom(''); setGroupTo('') }}>Add group mapping</button></div>{Object.entries(policy.group_mappings || {}).map(([key, value]) => <p key={key}>{key} → {value} <button type="button" onClick={() => setPolicy(old => ({ ...old, group_mappings: Object.fromEntries(Object.entries(old.group_mappings || {}).filter(([k]) => k !== key)) }))}>Remove mapping</button></p>)}</details>
        <details><summary>Map monitoring series and service aggregations</summary><p>Map each run to the same logical identity. Use exact keys from Resources; this explicitly confirms their units and gauge/counter meaning. Prometheus identities cannot be remapped or combined; define the intended aggregation in approved PromQL before capture.</p><div className="bc-controls"><label>Run<select value={resourceDraft.run_id} onChange={e => setResourceDraft(old => ({ ...old, run_id: e.target.value }))}><option value="*">All runs (reusable mapping)</option>{[test.id, ...selected].map(id => <option key={id} value={id}>{id === test.id ? 'Current run' : runNames.get(id) || id}</option>)}</select></label><label>Logical metric identity<input value={resourceDraft.identity} onChange={e => setResourceDraft(old => ({ ...old, identity: e.target.value }))} placeholder="checkout-java-heap" /></label><label>Recorded keys, one per line<textarea value={resourceDraft.keys.join('\n')} onChange={e => setResourceDraft(old => ({ ...old, keys: e.target.value.split('\n') }))} /></label><label>Unit<input value={resourceDraft.unit} onChange={e => setResourceDraft(old => ({ ...old, unit: e.target.value }))} placeholder="bytes" /></label><label>Metric kind<select value={resourceDraft.kind} onChange={e => setResourceDraft(old => ({ ...old, kind: e.target.value as ResourceMapping['kind'] }))}><option value="gauge">Gauge</option><option value="counter">Counter (compare rates)</option></select></label><label>Aggregate instances<select value={resourceDraft.aggregation} onChange={e => setResourceDraft(old => ({ ...old, aggregation: e.target.value as ResourceMapping['aggregation'] }))}><option value="mean">Mean</option><option value="sum">Sum</option><option value="max">Maximum</option></select></label><label>Optional workload group<input value={resourceDraft.group || ''} onChange={e => setResourceDraft(old => ({ ...old, group: e.target.value }))} /></label><label>Minimum absolute change<input type="number" min="0" value={resourceDraft.absolute_threshold || 0} onChange={e => setResourceDraft(old => ({ ...old, absolute_threshold: Number(e.target.value) }))} /></label><button type="button" className="btn btn-secondary" disabled={!resourceDraft.identity || !resourceDraft.unit || !resourceDraft.keys.some(k => k.trim())} onClick={() => { setPolicy(old => ({ ...old, resource_mappings: [...(old.resource_mappings || []).filter(m => m.run_id !== resourceDraft.run_id || m.identity !== resourceDraft.identity), { ...resourceDraft, keys: resourceDraft.keys.map(k => k.trim()).filter(Boolean) }] })); setNotice('Mapping added to draft. Apply comparison to evaluate it.') }}>Confirm metric mapping</button></div>{policy.resource_mappings?.map((mapping, i) => <p key={`${mapping.run_id}/${mapping.identity}`}>{mapping.run_id} · {mapping.identity} · {mapping.keys.length} series · {mapping.aggregation} <button type="button" onClick={() => setPolicy(old => ({ ...old, resource_mappings: old.resource_mappings?.filter((_, index) => index !== i) }))}>Remove mapping</button></p>)}</details>
        <button type="button" className="btn btn-secondary" disabled={!test.scenario_id || !canEditPolicy} onClick={() => void savePolicy()}>Save scenario policy</button><p>Temporary overrides are included in the saved comparison. Shared policies require scenario-edit access.</p>
      </fieldset></details>
      {dirty && <p className="bc-draft" role="status">Settings changed. Results and exports below still describe the saved comparison; apply to evaluate the draft.</p>}
      {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="run-report-error">{error}</p>}
    </section>
    {!!savedComparisons?.length && <section className="run-report-panel bc-saved" aria-label="Saved assessments">
      <h3>Saved assessments for this run</h3>
      <ul>{savedComparisons.map(item => <li key={item.id}>
        <span><strong>{comparisonVerdictLabel(item.verdict)}</strong> · {formatDateTime(item.created_at)} · against {item.baselines.map(run => run.name || run.id).join(', ') || 'no baselines'}</span>
        {item.id === snapshot?.id ? <span className="bc-saved-current">Showing</span> : <button type="button" className="btn btn-secondary btn-sm" onClick={() => onSnapshot(item.id)}>Open</button>}
      </li>)}</ul>
    </section>}
    {!snapshot && <section className="run-report-panel bc-empty"><h3>Compare the same workload across releases</h3><p>Select up to five pinned runs, then compare. You’ll see transaction changes, monitored resource differences, and setup inconsistencies together.</p></section>}
    {snapshot && <>
      <BaselineTelemetryEvidence evidence={snapshot.telemetry} runNames={new Map([...runNames, [test.id, test.name]])} />
      <BaselineTelemetryCompatibility comparisons={snapshot.telemetry_comparisons} projectId={snapshot.project_id} runNames={new Map([...runNames, [snapshot.current.id, `${snapshot.current.release_name || 'No release'} · ${snapshot.current.name}`]])} />
      <BaselineBrowserDocuments rows={snapshot.browser_documents} baselines={snapshot.baselines} softNavigation={snapshot.browser_soft_navigation} />
      <section className="run-report-panel" aria-label="Regression verdict"><h2>Regression verdict: {snapshot.verdict ? comparisonVerdictLabel(snapshot.verdict.status) : 'Unavailable'}</h2>{snapshot.verdict && <><p>{snapshot.verdict.evaluated.toLocaleString()} acceptance {snapshot.verdict.evaluated === 1 ? 'check' : 'checks'} evaluated · {snapshot.verdict.unavailable.toLocaleString()} unavailable</p>
        {snapshot.verdict.evaluated === 0 && (() => {
          // Nothing evaluated reads as a verdict unless it says why and what to change.
          const blockers = verdictBlockers(snapshot)
          return <div className="bc-verdict-blockers" role="note"><p>No acceptance check could be evaluated, so this comparison gives no regression verdict. The table below still shows the measured changes.</p>
            {blockers.length > 0 && <ul>{blockers.map(text => <li key={text}>{text}</li>)}</ul>}
            <p>Choose a baseline whose run completed with the same setup, or confirm the setup differences, then apply the comparison again. Latency checks also need 100 successful samples (1,000 for P99) in both runs.</p></div>
        })()}<details><summary>Acceptance evidence</summary><ul>{snapshot.verdict.evidence.map((e, i) => <li key={i}>{e.metric}: {comparisonVerdictLabel(e.status)} · {e.baseline_id ? baselineTag(e.baseline_id) : 'All baselines'} / {e.row_id} {e.reason}</li>)}</ul></details></>}</section>
      <section className="run-report-panel" aria-label="Comparison findings"><header><div><p className="bc-eyebrow">Saved {formatDateTime(snapshot.created_at)}</p><h2>Findings to review</h2></div><div className="bc-findings-count"><strong>{snapshot.findings.filter(f => f.status === 'changed').length}</strong> threshold changes <span>· {snapshot.findings.filter(f => f.status !== 'changed').length} qualifications</span></div></header>
        <ul className="bc-baseline-key" aria-label="Baselines compared">{shownBaselines.map((run, index) => <li key={run.id}><i aria-hidden="true" style={{ background: comparisonColors[index + 1] }} /><strong>Baseline {index + 1}</strong> {run.release_name || 'No release'} · {run.name}</li>)}</ul>
        <div className="bc-findings">{findings.map((finding, index) => <button type="button" className={`bc-finding bc-${finding.status}`} key={`${finding.row_id}/${finding.baseline_id}/${index}`} onClick={() => { if (finding.category === 'setup') setView('setup'); else { const row = [...snapshot.transactions, ...snapshot.resources].find(item => item.id === finding.row_id); if (row) setDetail(row) } }}><span>{finding.status === 'changed' ? 'Change detected' : finding.status === 'review' ? 'Setup review' : 'Unverified'} · {finding.category}</span><strong>{finding.message}</strong><small>{baselineTag(finding.baseline_id)} · View evidence</small></button>)}</div>
        {!findings.length && <p>No configured change threshold exceeded. This is not an acceptance verdict.</p>}{snapshot.findings.length > 8 && <button type="button" className="btn btn-ghost" onClick={() => setAllFindings(v => !v)}>{allFindings ? 'Show priority findings' : `Show all ${snapshot.findings.length} findings`}</button>}
        <details><summary>Evidence notes ({snapshot.warnings.length})</summary><ul>{snapshot.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul></details>
      </section>
      <section className="run-report-panel" aria-label="Comparison evidence"><nav className="bc-views" aria-label="Comparison views">{(['transactions', 'resources', 'setup'] as const).map(name => <button type="button" className={`btn ${view === name ? 'btn-primary' : 'btn-secondary'}`} aria-pressed={view === name} key={name} onClick={() => setView(name)}>{name === 'setup' ? 'Setup differences' : name[0].toUpperCase() + name.slice(1)}</button>)}</nav>
        {view === 'setup' ? <div className="bc-table-scroll"><table><thead><tr><th>Setting</th><th>Baseline</th><th>Current</th><th>Reference</th><th>Qualification</th></tr></thead><tbody>{snapshot.setup.map((diff, i) => <tr key={i}><th scope="row">{diff.field}</th><td>{baselineTag(diff.baseline_id)}</td><td>{diff.current}</td><td>{diff.baseline}</td><td>{comparisonVerdictLabel(diff.status)}</td></tr>)}</tbody></table>{!snapshot.setup.length && <p>Recorded setup checks match.</p>}</div> : <>
          <div className="bc-controls"><label>Filter {view}<input type="search" autoComplete="off" value={search} onChange={e => setSearch(e.target.value)} placeholder="Name, group, or source" /></label>{view === 'transactions' && <label>Comparison metric<select value={metric} onChange={e => setMetric(e.target.value as Metric)}>{metricOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>}<label>Sort<select value={sort} onChange={e => setSort(e.target.value as 'change' | 'name')}><option value="change">Largest change</option><option value="name">Name</option></select></label><label className="bc-mobile-picker">Visible baseline<select value={effectiveMobile || ''} onChange={e => setMobileBaseline(e.target.value)}>{shownBaselines.map(run => <option key={run.id} value={run.id}>{run.name}</option>)}</select></label></div>
          <p>{rows.length.toLocaleString()} {rows.length === 1 ? 'row' : 'rows'} · {view === 'resources' ? 'Prometheus means describe settled grid evaluations; other resource means use their recorded intervals. Higher and lower changes both need review.' : 'Successful latency; throughput and error counts include all captured outcomes.'}</p>
          <div ref={listRef} className="bc-matrix" tabIndex={0} role="region" aria-label={`${view} comparison table`}><table><thead><tr><th scope="col">{view === 'resources' ? 'Resource / system' : 'Transaction / group'} · load</th><th scope="col">Current {view === 'resources' ? 'mean' : metricOptions.find(([value]) => value === metric)?.[1]}</th>{view === 'transactions' && <><th scope="col" className="bc-extra">Attempts/s</th><th scope="col" className="bc-extra">Error rate</th><th scope="col" className="bc-extra">Samples</th></>}{shownBaselines.map((run, i) => <th scope="col" key={run.id} className={`bc-baseline ${run.id === effectiveMobile ? 'bc-visible' : ''}`} style={{ borderTop: `3px solid ${comparisonColors[i + 1]}` }}>Baseline {i + 1}<small>{run.release_name || 'No release'}</small><small>{run.name}</small></th>)}</tr></thead><tbody>
            {visibleRows.length > 0 && visibleRows[0].start > 0 && <tr aria-hidden="true"><td colSpan={9} aria-hidden="true" style={{ height: visibleRows[0].start, padding: 0 }} /></tr>}
            {visibleRows.map(item => { const row = rows[item.index]; const selectedMetric = view === 'resources' ? 'mean' : metric; const unit = view === 'resources' ? row.unit : metricUnit(metric); return <tr key={row.id} style={{ height: 76 }}><th scope="row"><button type="button" onClick={() => setDetail(row)}>{row.name}</button><small>{row.group || row.source} · {number(row.window?.load)} VUsers</small>{row.provider && <small className="bc-provider" title={resourceProviderLabel(row)}>{resourceProviderLabel(row)}</small>}</th><td className="bc-value">{withUnit(number(measurementValue(row.current, selectedMetric, view === 'resources')), unit)}</td>{view === 'transactions' && <><td className="bc-extra">{withUnit(number(row.current?.throughput), '/s')}</td><td className="bc-extra">{withUnit(number(row.current?.error_pct), '%')}</td><td className="bc-extra">{number(row.current?.samples)}</td></>}{shownBaselines.map(run => { const cell = row.baselines[run.id]; return <td key={run.id} className={`bc-baseline ${run.id === effectiveMobile ? 'bc-visible' : ''}`}><button type="button" className="bc-cell" onClick={() => setDetail(row)}>{withUnit(number(measurementValue(cell?.measurement, selectedMetric, view === 'resources')), unit)}<small>{view === 'resources' && cell?.delta === undefined ? 'Change unavailable' : difference(measurementValue(row.current, selectedMetric, view === 'resources'), measurementValue(cell?.measurement, selectedMetric, view === 'resources'), unit)}</small><small className={cell?.qualification?.length ? 'bc-caution' : ''}>{cell?.qualification?.length ? 'Review comparability' : cell?.approximate ? 'Approximate load match' : 'Exact load match'}</small></button></td> })}</tr> })}
            {visibleRows.length > 0 && <tr aria-hidden="true"><td colSpan={9} aria-hidden="true" style={{ height: Math.max(0, virtual.getTotalSize() - visibleRows[visibleRows.length - 1].end), padding: 0 }} /></tr>}
          </tbody></table>{!rows.length && <p>No matching evidence. Review the measurement windows and capture notes.</p>}</div>
        </>}
      </section>
      <section className="run-report-panel bc-controls" aria-label="Share comparison"><div><h3>Share this assessment</h3><a href={`#/report/${encodeURIComponent(test.id)}?section=comparison&comparison_id=${encodeURIComponent(snapshot.id)}`}>Permanent comparison link</a></div><p className="run-report-muted">Reports use the {REPORT_LENS_LABELS[lens]} audience chosen at the top of the page.</p>{/* The report's own export names; the evidence and JSON files are the comparison's. */}{[['html', 'Navigable HTML'], ['pdf', 'Print-ready PDF'], ['docx', 'Editable Word'], ['evidence_csv', 'Evidence CSV'], ['json', 'Comparison JSON']].map(([format, label]) => <button key={format} type="button" className="btn btn-secondary" disabled={busy} onClick={() => void download(format)}>{label}</button>)}
        {shownBaselines.length > 0 && <div className="bc-controls"><label>Panel title<input aria-label="Panel title" maxLength={256} value={panelTitle} onChange={e => setPanelTitle(e.target.value)} /></label><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void saveToPanel()}>Save to panel</button></div>}
        {savedPanel && <p role="status">Saved panel: {savedPanel.id} (revision {savedPanel.revision}){savedPanel.tabs[0]?.panels[0]?.query?.browser_inputs?.query_sha256 ? ` · query ${savedPanel.tabs[0].panels[0].query.browser_inputs.query_sha256}` : ''}{savedPanel.tabs[0]?.panels[0]?.query?.browser_inputs?.selection_receipt_sha256 ? ` · selection receipt ${savedPanel.tabs[0].panels[0].query.browser_inputs.selection_receipt_sha256}` : ''}</p>}
      </section>
    </>}
    {detail && snapshot && <ComparisonDetail row={detail} snapshot={snapshot} onClose={() => setDetail(null)} />}
  </div>
}
