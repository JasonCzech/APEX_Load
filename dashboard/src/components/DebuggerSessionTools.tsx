import { useEffect, useMemo, useState } from 'react'
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugBreakpoint, ReplayRuntimeSnapshot } from '../types/replay'
import type { ReplayOptionsState } from './scriptEditorState'
import { compareRuns, eventLocation, readProfiles, safeProfile, variablesAt, type DebugRunRecord } from './debuggerExperience'
import { omitUnavailableReplayReport, RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE } from './runtimeVariableEvidence'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import { HTTPVersionReplayEvidence } from './HTTPVersionReplayEvidence'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { formatReplayHeaders } from './scriptEditorUtils'
import type { RuntimeInspectionMode } from './debugRuntimeState'
import { DebugRuntimeStateEvidence } from './DebugRuntimeStateEvidence'

export function DebugProfiles({ options, onChange, disabled }: { options: ReplayOptionsState; onChange: (options: ReplayOptionsState) => void; disabled: boolean }) {
  const [profiles, setProfiles] = useState(() => { try { return readProfiles(localStorage.getItem('apexload.debug.profiles.v1')) } catch { return [] } })
  const [name, setName] = useState('')
  const [notice, setNotice] = useState('')
  return <div className="debug-session-tools">
    <label>Run profile<select disabled={disabled} value="" onChange={e => { const profile = profiles.find(p => p.name === e.target.value); if (profile) onChange({ ...options, ...profile.options }) }}><option value="">Custom session settings</option>{profiles.map(p => <option key={p.name}>{p.name}</option>)}</select></label>
    <label>Profile name<input disabled={disabled} value={name} maxLength={60} onChange={e => setName(e.target.value)} /></label>
    <button type="button" disabled={disabled || !name.trim()} onClick={() => { const next = [...profiles.filter(p => p.name !== name.trim()), safeProfile(name, options)].slice(-10); setProfiles(next); try { localStorage.setItem('apexload.debug.profiles.v1', JSON.stringify(next)); setNotice('Saved execution controls only. Session variables and target are not stored.') } catch { setNotice('Profile available until this editor closes; browser storage is unavailable.') } }}>Save profile</button>
    <small role="status">{notice || 'Profiles store execution controls only; variables and target remain specific to this script.'}</small>
  </div>
}

export function DebugVariableWatches({ events, seed, latest, selected, onSelect, evidenceUnavailable = false, snapshot = null, currentReason = 'Current state unavailable. Pause at an engine boundary to inspect values.', mode, onModeChange, completed = false, canInspectFrame, onInspectFrame }: { events: BrowserReplayEvent[]; seed: Record<string, string>; latest: Record<string, string> | null; selected: number | null; onSelect: (index: number) => void; evidenceUnavailable?: boolean; snapshot?: ReplayRuntimeSnapshot | null; currentReason?: string; mode?: RuntimeInspectionMode; onModeChange?: (mode: RuntimeInspectionMode) => void; completed?: boolean; canInspectFrame?: (frame: ReplayRuntimeSnapshot['frames'][number]) => boolean; onInspectFrame?: (frame: ReplayRuntimeSnapshot['frames'][number]) => void }) {
  const [localMode, setLocalMode] = useState<RuntimeInspectionMode>('current')
  const scope = mode || localMode
  const changeMode = (value: RuntimeInspectionMode) => { setLocalMode(value); onModeChange?.(value) }
  const [search, setSearch] = useState('')
  const [changedOnly, setChangedOnly] = useState(false)
  const [pinned, setPinned] = useState<string[]>([])
  const index = scope === 'selected' ? selected ?? -1 : events.length - 1
  const values = useMemo(() => evidenceUnavailable || scope === 'current' ? {} : scope === 'retained' ? latest || variablesAt(events, seed, index) : variablesAt(events, seed, index), [scope, latest, events, seed, index, evidenceUnavailable])
  const before = useMemo(() => evidenceUnavailable || scope === 'current' ? {} : variablesAt(events, seed, index - 1), [scope, events, seed, index, evidenceUnavailable])
  const names = [...new Set([...Object.keys(values), ...pinned])].filter(name => `${name} ${Object.prototype.hasOwnProperty.call(values, name) ? values[name] : ''}`.toLowerCase().includes(search.toLowerCase()) && (!changedOnly || before[name] !== values[name])).sort((a, b) => Number(pinned.includes(b)) - Number(pinned.includes(a)) || a.localeCompare(b))
  if (evidenceUnavailable) return <section aria-label="Variable watches" className="debug-watch-panel"><p role="status">{RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE}</p></section>
  return <section aria-label="Variable watches" className="debug-watch-panel">
    <div className="debug-session-tools"><label>Values at<select value={scope} onChange={e => changeMode(e.target.value as RuntimeInspectionMode)}><option value="current">Current paused state</option><option value="retained">Retained history</option><option value="selected" disabled={selected == null}>Selected execution (after)</option></select></label>{scope !== 'current' && <><label>Search variables<input value={search} onChange={e => setSearch(e.target.value)} /></label><label><input type="checkbox" checked={changedOnly} onChange={e => setChangedOnly(e.target.checked)} />Changed at this execution</label></>}</div>
    {scope === 'current' ? snapshot ? <DebugRuntimeStateEvidence snapshot={snapshot} canInspectFrame={canInspectFrame} onInspectFrame={onInspectFrame} /> : <p role="status">{currentReason}</p> : <>
    <p role="status">Retained history · {scope === 'selected' && selected != null && events[selected] ? `After ${eventLocation(events[selected])}` : completed ? 'Final historical values' : 'Reconstructed from session inputs and retained extractions'} · This is historical evidence, not current engine state. Values omitted from retained evidence cannot be reconstructed.</p>
    <table><caption className="sr-only">Variable values and provenance</caption><thead><tr><th>Watch</th><th>Name / namespace</th><th>Value</th><th>Last changed</th></tr></thead><tbody>{names.map(name => {
      let changed = -1
      for (let i = 0; i <= index; i++) if (events[i]?.extractions && Object.prototype.hasOwnProperty.call(events[i].extractions!, name)) changed = i
      const value = Object.prototype.hasOwnProperty.call(values, name) ? values[name] : undefined
      return <tr key={name}><td><button type="button" aria-label={`Pin ${name}`} aria-pressed={pinned.includes(name)} onClick={() => setPinned(p => p.includes(name) ? p.filter(n => n !== name) : [...p, name])}>{pinned.includes(name) ? 'Pinned' : 'Pin'}</button></td><th scope="row">{name}<small>{name.includes('.') ? name.split('.')[0] : 'var'}</small></th><td><code>{value === '' ? '(empty)' : value ?? 'Not available'}</code>{before[name] !== value && <small>Changed</small>}</td><td>{changed >= 0 ? <button type="button" onClick={() => onSelect(changed)}>{events[changed].action_name} · {eventLocation(events[changed])}</button> : 'Session input'}</td></tr>
    })}</tbody></table>{!names.length && <p>No variables match this view.</p>}
    <details><summary>Values as JSON</summary><SnapshotBodyPreview body={JSON.stringify(values, null, 2)} headers={{ 'Content-Type': 'application/json' }} /></details>
    </>}
  </section>
}

export function DebugTimeline({ events, onSelect }: { events: BrowserReplayEvent[]; onSelect: (index: number) => void }) {
  const [search, setSearch] = useState(''), [failures, setFailures] = useState(false)
  const rows = events.map((event, index) => ({ event, index })).filter(({ event }) => (!failures || !event.success) && `${event.action_name} ${event.target} ${eventLocation(event)}`.toLowerCase().includes(search.toLowerCase()))
  return <div className="debug-execution-timeline"><div className="debug-session-tools"><label>Find execution<input value={search} onChange={e => setSearch(e.target.value)} /></label><label><input type="checkbox" checked={failures} onChange={e => setFailures(e.target.checked)} />Failures only</label></div>{rows.map(({ event, index }) => <button type="button" key={event.event_id || index} className="debug-timeline-row" onClick={() => onSelect(index)}><strong>{event.success ? 'Passed' : 'Failed'} · {event.action_name}</strong><span>{eventLocation(event)} · {event.duration_ms.toFixed(1)} ms</span><small>{event.error_message || event.target}</small>{Object.keys(event.extractions || {}).length > 0 && <small>Variables: {Object.keys(event.extractions!).join(', ')}</small>}</button>)}{!rows.length && <p>{events.length ? 'No executions match these filters.' : 'No completed executions captured yet.'}</p>}</div>
}

function HistoricalEvidence({ report }: { report: BrowserReplayReport }) {
  const [selected, setSelected] = useState(0)
  const event = report.events[selected]
  if (report.runtime_evidence_unavailable) return <p role="status">{RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE}</p>
  if (report.protocol === 'browser') return <BrowserReplayDebugger report={report} running={false} />
  return <div className="debug-history-evidence"><label>Historical execution<select value={selected} onChange={e => setSelected(Number(e.target.value))}>{report.events.map((e, index) => <option value={index} key={index}>{eventLocation(e)} · {e.action_name} · {e.success ? 'Passed' : 'Failed'}</option>)}</select></label>{event ? <>
    <p>{event.error_message || `${event.method || event.action_type} ${event.target}`}</p><p>Response status: {event.status_code ?? 'Not captured'}</p>
    <HTTPVersionReplayEvidence evidence={event.protocol_evidence} />
    <HTTPResponseContentEvidence evidence={event.protocol_evidence} bodyState={event.snapshot?.response_body_state || event.response_body_state} />
    <div className="debug-history-columns"><section><h4>Request</h4><SnapshotTextPreview text={`${event.method || event.action_type} ${event.target}\n${formatReplayHeaders(event.snapshot?.request_headers || event.headers)}`} /><SnapshotBodyPreview body={event.snapshot?.request_body || event.body} emptyMessage="No request body retained" /></section><section><h4>Response</h4><SnapshotTextPreview text={formatReplayHeaders(event.snapshot?.response_headers)} /><SnapshotBodyPreview body={event.snapshot?.response_body} headers={event.snapshot?.response_headers} emptyMessage={`Response body: ${event.snapshot?.response_body_state || event.response_body_state || 'not retained'}`} /></section></div>
    {event.protocol_evidence && <details><summary>Protocol evidence</summary><SnapshotBodyPreview body={JSON.stringify(event.protocol_evidence, null, 2)} /></details>}<details><summary>Extracted variables</summary><SnapshotBodyPreview body={JSON.stringify(event.extractions || {}, null, 2)} /></details>
  </> : <p>No retained executions.</p>}</div>
}
export function DebugRunHistory({ runs, current, currentRecord }: { runs: DebugRunRecord[]; current: BrowserReplayReport | null; currentRecord?: Omit<DebugRunRecord, 'report'> | null }) {
  const [selectedId, setSelectedId] = useState(''), [pinnedBaselineId, setPinnedBaselineId] = useState<string | null>(null)
  const admittedRuns = useMemo(() => runs.map(run => run.report.runtime_evidence_unavailable
    ? { ...run, report: omitUnavailableReplayReport(run.report) } : run), [runs])
  const admittedCurrent = useMemo(() => current?.runtime_evidence_unavailable ? omitUnavailableReplayReport(current) : current, [current])
  const selected = admittedRuns.find(r => r.id === selectedId) || admittedRuns[0]
  const baseline = admittedRuns.find(run => run.id === pinnedBaselineId) || selected
  const diff = useMemo(() => baseline && admittedCurrent ? compareRuns(baseline.report, admittedCurrent) : [], [baseline, admittedCurrent])
  if (!selected) return null
  const configuration = (record: Omit<DebugRunRecord, 'report'> | null | undefined, report: BrowserReplayReport) => ({ target: record?.target, failurePolicy: record?.failurePolicy, capture: record?.options.capture, ignoreThinkTime: record?.options.ignoreThinkTime, inputs: record?.inputs, runtime: report.runtime_profile })
  return <details className="debug-run-history"><summary>Recent runs ({runs.length}) · Compare evidence</summary><p>Evidence stays in this editor session (up to 5 runs). Export to retain it before closing.</p><label>Inspect run<select value={selected.id} onChange={e => setSelectedId(e.target.value)}>{admittedRuns.map(r => <option key={r.id} value={r.id}>{r.report.started_at} · {r.report.protocol} · {r.report.stopped ? 'Stopped' : r.report.success ? 'Passed' : 'Failed'}</option>)}</select></label><button type="button" aria-pressed={pinnedBaselineId === selected.id} onClick={() => setPinnedBaselineId(selected.id)}>Pin comparison baseline</button>
    <p>{selected.draft ? 'Unsaved draft' : 'Saved script'} · {selected.report.script_revision || selected.report.script_hash || 'Revision identity unavailable'} · {selected.target} · {selected.failurePolicy}</p>
    {Boolean(selected.report.dropped_event_count || selected.report.truncated_event_evidence_count) && <p className="debug-history-retention">{selected.report.dropped_event_count || 0} executions omitted · {selected.report.truncated_event_evidence_count || 0} execution details truncated in retained evidence.</p>}
    <HistoricalEvidence key={selected.id} report={selected.report} />
    {admittedCurrent && <details><summary>Compare baseline with current run</summary><p>Baseline: {baseline.report.started_at} · Current: {admittedCurrent.started_at}</p><div className="debug-history-columns"><section><h4>Baseline configuration</h4><SnapshotBodyPreview body={JSON.stringify(configuration(baseline, baseline.report), null, 2)} /></section><section><h4>Current configuration</h4><SnapshotBodyPreview body={JSON.stringify(configuration(currentRecord, admittedCurrent), null, 2)} /></section></div>{diff.filter(row => row.fields.length).map(row => <details key={row.key}><summary>{(row.after || row.before)!.action_name} · {eventLocation((row.after || row.before)!)} · {row.fields.join(', ')}</summary><div className="debug-history-columns"><SnapshotBodyPreview body={JSON.stringify(row.before || null, null, 2)} /><SnapshotBodyPreview body={JSON.stringify(row.after || null, null, 2)} /></div></details>)}{diff.every(row => !row.fields.length) && <p>No outcome, request, response, assertion or variable differences in retained evidence.</p>}</details>}
  </details>
}

function BreakpointNumber({ label, value, minimum, placeholder, disabled, onCommit }: { label: string; value: number | undefined; minimum: number; placeholder: string; disabled: boolean; onCommit: (value: number | undefined) => void }) {
  const [draft, setDraft] = useState(value == null ? '' : String(value))
  useEffect(() => setDraft(value == null ? '' : String(value)), [value, disabled])
  const commit = () => {
    const parsed = draft.trim() === '' ? undefined : Number(draft)
    if (parsed != null && !Number.isFinite(parsed)) { setDraft(value == null ? '' : String(value)); return }
    const next = parsed == null ? undefined : Math.max(minimum, Math.min(1000000, Math.trunc(parsed)))
    setDraft(next == null ? '' : String(next))
    if (next !== value) onCommit(next)
  }
  return <label>{label}<input type="number" min={minimum} max={1000000} disabled={disabled} value={draft} placeholder={placeholder} onChange={e => setDraft(e.target.value)} onBlur={commit} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur() } }} /></label>
}

export function DebugBreakpointControls({ breakpoints, disabled, onChange }: { breakpoints: ScriptDebugBreakpoint[]; disabled: boolean; onChange: (next: ScriptDebugBreakpoint[]) => void }) {
  const persistent = breakpoints.filter(b => !b.once)
  return <details className="debug-breakpoint-controls"><summary>Breakpoint controls ({persistent.length})</summary><button type="button" disabled={disabled || !persistent.length} onClick={() => onChange(breakpoints.map(b => b.once ? b : { ...b, disabled: persistent.some(p => !p.disabled) }))}>{persistent.some(b => !b.disabled) ? 'Disable all breakpoints' : 'Enable all breakpoints'}</button><p>Hit count counts eligible visits during this run. Iteration is zero-based and available on nested action breakpoints; it targets the innermost loop. Editing breakpoints resets hit counts.</p>{persistent.map((bp, index) => <div className="debug-session-tools" key={index}><span>{bp.phase} {bp.action_index + 1}{bp.step_path?.length ? ` › ${bp.step_path.join(' › ')}` : bp.request_index != null ? ` › request ${bp.request_index + 1}` : ''}</span><label><input type="checkbox" disabled={disabled} checked={!bp.disabled} onChange={e => onChange(breakpoints.map(b => b === bp ? { ...b, disabled: !e.target.checked } : b))} />Enabled</label><BreakpointNumber label="Break on hit" minimum={1} disabled={disabled} value={bp.hit_count || undefined} placeholder="Every hit" onCommit={hit_count => onChange(breakpoints.map(b => b === bp ? { ...b, hit_count } : b))} /><BreakpointNumber label="Loop iteration" minimum={0} disabled={disabled || (!bp.step_path?.length && bp.request_index == null)} value={bp.iteration} placeholder="Any" onCommit={iteration => onChange(breakpoints.map(b => b === bp ? { ...b, iteration } : b))} /></div>)}</details>
}
