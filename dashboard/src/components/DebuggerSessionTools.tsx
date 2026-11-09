import { useEffect, useMemo, useState } from 'react'
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugBreakpoint, ReplayRuntimeSnapshot } from '../types/replay'
import type { ScriptDefinition, ScriptParameterDefinition } from '../types/scripts'
import type { ReplayOptionsState } from './scriptEditorState'
import { compareRuns, eventLocation, readProfiles, safeProfile, variablesAt, type DebugRunRecord } from './debuggerExperience'
import { omitUnavailableReplayReport, RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE } from './runtimeVariableEvidence'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import { HTTPVersionReplayEvidence } from './HTTPVersionReplayEvidence'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import { HTTPNetworkTraceEvidence } from './HTTPNetworkTraceEvidence'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { formatReplayHeaders, inferDelimiter } from './scriptEditorUtils'
import type { RuntimeInspectionMode } from './debugRuntimeState'
import { DebugRuntimeStateEvidence } from './DebugRuntimeStateEvidence'
import { appliedReplayExtractions } from './replayExtractionValues'
import { ReplayExtractions } from './ReplayExtractions'
import { placeSteps, stepOutputs } from './referenceScopeModel'
import { isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'
import {
  buildDebugTimelineModel,
  DEBUG_TIMELINE_LEGEND_PHASES,
  type DebugTimelineScaleMode,
} from './debugTimelineWaterfallModel'

function collectScriptAuthoredVariables(script?: ScriptDefinition): Map<string, string> {
  const result = new Map<string, string>()
  if (!script) return result
  for (const { action } of placeSteps(script)) {
    const label = action.name ? `Authored in ${action.name}` : 'Authored in script'
    for (const output of stepOutputs(action)) {
      const key = output.expression.startsWith('var.') ? output.expression.slice(4) : output.expression
      if (key && !result.has(key)) result.set(key, label)
    }
    const forEach = (action.control as { for_each?: { var?: string } } | undefined)?.for_each
    if (forEach?.var && !result.has(forEach.var)) result.set(forEach.var, label)
  }
  return result
}

function parseFirstDataSourceValue(script: ScriptDefinition, parameter: ScriptParameterDefinition): string | undefined {
  const source = script.data_sources?.find(item => item.name === parameter.data_source)
  if (!source?.inline_data || !['csv', 'dat'].includes(source.type) || source.encoding === 'base64' || source.inline_data.length > 1024 * 1024) return undefined
  const delimiter = inferDelimiter(source)
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false
  let started = false
  for (let i = 0; i < source.inline_data.length; i++) {
    const ch = source.inline_data[i]
    if (ch === '"') {
      started = true
      if (quoted && source.inline_data[i + 1] === '"') { cell += '"'; i++ } else quoted = !quoted
    } else if (ch === delimiter && !quoted) {
      row.push(cell); cell = ''
    } else if ((ch === '\n' || ch === '\r') && !quoted) {
      if (ch === '\r' && source.inline_data[i + 1] === '\n') i++
      if (started || cell || row.length) { row.push(cell); rows.push(row) }
      row = []; cell = ''; started = false
      if (rows.length >= 4) break
    } else {
      cell += ch
    }
  }
  if (started || cell || row.length) rows.push([...row, cell])
  if (!rows.length) return undefined
  const first = rows[0]
  const columns = (source.first_row_header ? first || source.columns || [] : source.columns?.length ? source.columns : first?.map((_, idx) => `col_${idx + 1}`) || []).map(col => col.trim())
  const colIndex = parameter.column ? columns.indexOf(parameter.column.trim()) : 0
  if (colIndex < 0) return undefined
  const dataRows = rows.slice(source.first_row_header ? 1 : 0)
  const targetRow = dataRows[Math.max(0, (parameter.start_row ?? 1) - 1)] ?? dataRows[0]
  const raw = targetRow?.[colIndex]
  if (raw === undefined) return undefined
  return /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(raw) ? '[REDACTED]' : raw.trim()
}

function collectScriptParameters(script?: ScriptDefinition): Map<string, { configuredValue: string | undefined; provenance: string; type: string }> {
  const result = new Map<string, { configuredValue: string | undefined; provenance: string; type: string }>()
  if (!script?.parameters?.length) return result
  for (const parameter of script.parameters) {
    const rawName = parameter.name?.trim()
    if (!rawName) continue
    const key = `param.${rawName}`
    const paramType = (parameter.type || 'list').trim() || 'list'
    const protectedParam = isSensitiveCredentialPreviewName(rawName) || isSensitiveCredentialPreviewName(parameter.column || '') || /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(JSON.stringify(parameter))
    const provenance = parameter.values?.length
      ? 'Inline values'
      : parameter.data_source
        ? `Data source (${parameter.data_source}${parameter.column ? `.${parameter.column}` : ''})`
        : `${paramType} generator`
    let configuredValue: string | undefined
    if (protectedParam) {
      configuredValue = '[REDACTED]'
    } else if (parameter.values?.length) {
      const idx = Math.max(0, (parameter.start_row ?? 1) - 1)
      configuredValue = parameter.values[idx] ?? parameter.values[0]
    } else if (parameter.type === 'file' && parameter.data_source) {
      configuredValue = parseFirstDataSourceValue(script, parameter)
    } else if (parameter.type === 'counter') {
      configuredValue = String(parameter.start ?? 1)
    } else if (parameter.type === 'current_iteration' || parameter.type === 'vuser_id') {
      configuredValue = '1'
    } else if (parameter.type) {
      configuredValue = `Generated (${parameter.type})`
    }
    result.set(key, { configuredValue, provenance, type: paramType })
  }
  return result
}

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

export function DebugVariableWatches({ events, seed, latest, selected, onSelect, evidenceUnavailable = false, historyIncomplete = false, snapshot = null, currentReason = 'Current state unavailable. Pause at an engine boundary to inspect values.', mode, onModeChange, completed = false, finalValuesRecorded = false, canInspectFrame, onInspectFrame, script, replaying = false }: { events: BrowserReplayEvent[]; seed: Record<string, string>; latest: Record<string, string> | null; selected: number | null; onSelect: (index: number) => void; evidenceUnavailable?: boolean; historyIncomplete?: boolean; snapshot?: ReplayRuntimeSnapshot | null; currentReason?: string; mode?: RuntimeInspectionMode; onModeChange?: (mode: RuntimeInspectionMode) => void; completed?: boolean; finalValuesRecorded?: boolean; canInspectFrame?: (frame: ReplayRuntimeSnapshot['frames'][number]) => boolean; onInspectFrame?: (frame: ReplayRuntimeSnapshot['frames'][number]) => void; script?: ScriptDefinition; replaying?: boolean }) {
  const [localMode, setLocalMode] = useState<RuntimeInspectionMode>('current')
  const [explicitCurrent, setExplicitCurrent] = useState(false)
  const requested = mode || localMode
  const authoredVariables = useMemo(() => collectScriptAuthoredVariables(script), [script])
  const scriptParameters = useMemo(() => collectScriptParameters(script), [script])
  const hasScriptDefinitions = scriptParameters.size > 0 || authoredVariables.size > 0
  // Once the session has ended "Current paused state" can only say it is
  // unavailable, so show the recorded values by default until the engineer
  // asks for the current state.
  // The choice is published to the shared mode, so every debugger panel shows the same point in time.
  // Final values are only supplied once a recorded scope is chosen, so the caller says they exist.
  const hasRecordedValues = events.length > 0 || finalValuesRecorded || Object.keys(latest || {}).length > 0 || (completed && hasScriptDefinitions)
  const fallbackScope: RuntimeInspectionMode | null = requested === 'selected' && selected == null ? 'retained' : requested === 'current' && completed && !snapshot && !explicitCurrent && hasRecordedValues ? selected != null ? 'selected' : 'retained' : null
  const scope: RuntimeInspectionMode = fallbackScope || requested
  useEffect(() => {
    if (!fallbackScope) return
    setLocalMode(fallbackScope)
    onModeChange?.(fallbackScope)
  }, [fallbackScope, onModeChange])
  const changeMode = (value: RuntimeInspectionMode) => { setExplicitCurrent(value === 'current'); setLocalMode(value); onModeChange?.(value) }
  const [search, setSearch] = useState('')
  const [changedOnly, setChangedOnly] = useState(false)
  const [selectedTypes, setSelectedTypes] = useState<string[]>([])
  const [pinned, setPinned] = useState<string[]>([])
  const index = scope === 'selected' ? selected ?? -1 : events.length - 1
  const reconstructed = useMemo(() => evidenceUnavailable || scope === 'current' ? {} : variablesAt(events, seed, index), [scope, events, seed, index, evidenceUnavailable])
  const incompleteValuesUnverified = historyIncomplete && !(scope === 'retained' && completed && finalValuesRecorded && latest)
  const values = scope === 'retained' && !evidenceUnavailable ? latest || reconstructed : reconstructed
  const before = useMemo(() => evidenceUnavailable || scope === 'current' ? {} : variablesAt(events, seed, index - 1), [scope, events, seed, index, evidenceUnavailable])
  const lastChanged = useMemo(() => {
    const positions = new Map<string, number>()
    const current = new Map(Object.entries(seed))
    for (let i = 0; i <= index && i < events.length; i++) {
      for (const [name, value] of Object.entries(appliedReplayExtractions(events[i]))) {
        if (!current.has(name) || current.get(name) !== value) positions.set(name, i)
        current.set(name, value)
      }
    }
    return positions
  }, [events, index, seed])
  const showPreRunTable = Boolean(script) && !replaying && !completed && !snapshot && !explicitCurrent && events.length === 0
  const showWatchTable = scope !== 'current' || showPreRunTable
  const valueForName = (source: Record<string, string>, name: string) => Object.prototype.hasOwnProperty.call(source, name) ? source[name] : undefined
  const displayValueForName = (name: string) => {
    const runtimeVal = valueForName(values, name) ?? ((!finalValuesRecorded || showPreRunTable) ? valueForName(seed, name) : undefined)
    if (runtimeVal !== undefined) return runtimeVal
    return scriptParameters.get(name)?.configuredValue
  }
  const variableTypeForName = (name: string) => {
    const paramInfo = scriptParameters.get(name)
    if (paramInfo) return paramInfo.type
    if (name.startsWith('param.')) return 'param'
    return name.includes('.') ? name.split('.')[0] : 'var'
  }
  const changedAtExecution = (name: string) => !historyIncomplete && scope !== 'current' && valueForName(values, name) !== undefined && valueForName(values, name) === valueForName(reconstructed, name) && valueForName(before, name) !== valueForName(reconstructed, name)
  const candidateNames = [
    ...Object.keys(values),
    ...(script ? Object.keys(seed) : []),
    ...authoredVariables.keys(),
    ...scriptParameters.keys(),
    ...pinned,
  ]
  const allCandidateNames = [...new Set(candidateNames)]
  const availableTypes = (() => {
    const ordered: string[] = ['var']
    for (const info of scriptParameters.values()) {
      if (!ordered.includes(info.type)) ordered.push(info.type)
    }
    for (const name of allCandidateNames) {
      const paramInfo = scriptParameters.get(name)
      const type = paramInfo ? paramInfo.type : name.startsWith('param.') ? 'param' : name.includes('.') ? name.split('.')[0] : 'var'
      if (!ordered.includes(type)) ordered.push(type)
    }
    if (ordered.length < 2) ordered.push('param')
    return ordered
  })()
  const activeSelectedTypes = selectedTypes.filter(type => availableTypes.includes(type))
  const names = allCandidateNames
    .filter(name => (activeSelectedTypes.length === 0 || activeSelectedTypes.includes(variableTypeForName(name))) && `${name} ${variableTypeForName(name)} ${displayValueForName(name) ?? ''} ${scriptParameters.get(name)?.provenance ?? ''} ${authoredVariables.get(name) ?? ''}`.toLowerCase().includes(search.toLowerCase()) && (!changedOnly || changedAtExecution(name)))
    .sort((a, b) => Number(pinned.includes(b)) - Number(pinned.includes(a)) || a.localeCompare(b))
  if (evidenceUnavailable) return <section aria-label="Variable watches" className="debug-watch-panel" data-table-pagination="off"><p role="status">{RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE}</p></section>
  return <section aria-label="Variable watches" className="debug-watch-panel" data-table-pagination="off">
    <div className="debug-session-tools"><label>Values at<select value={scope} onChange={e => changeMode(e.target.value as RuntimeInspectionMode)}><option value="current">Current paused state</option><option value="retained">Retained history</option><option value="selected" disabled={selected == null}>Selected execution (after)</option></select></label>{showWatchTable && <><label>Search variables<input value={search} onChange={e => setSearch(e.target.value)} /></label><div className="debug-watch-type-filter" role="group" aria-label="Filter by parameter or variable type"><span className="debug-watch-type-filter-label">Type</span><button type="button" className={`debug-watch-type-chip${activeSelectedTypes.length === 0 ? ' active' : ''}`} aria-pressed={activeSelectedTypes.length === 0} onClick={() => setSelectedTypes([])}>All types</button>{availableTypes.map(type => { const isSelected = activeSelectedTypes.includes(type); return <button key={type} type="button" className={`debug-watch-type-chip${isSelected ? ' active' : ''}`} aria-pressed={isSelected} onClick={() => setSelectedTypes(prev => { const base = prev.filter(item => availableTypes.includes(item)); return base.includes(type) ? base.filter(item => item !== type) : [...base, type] })}>{type}</button> })}</div>{scope !== 'current' && <label><input type="checkbox" checked={changedOnly} onChange={e => setChangedOnly(e.target.checked)} />Changed at this execution</label>}</>}</div>
    {scope === 'current' && snapshot ? <DebugRuntimeStateEvidence snapshot={snapshot} canInspectFrame={canInspectFrame} onInspectFrame={onInspectFrame} /> : null}
    {scope === 'current' && !snapshot ? <p role="status">{currentReason}</p> : null}
    {showWatchTable && <>
    {scope !== 'current' && <p role="status">Retained history · {scope === 'selected' && selected != null && events[selected] ? `After ${eventLocation(events[selected])}` : completed ? 'Final historical values' : 'Reconstructed from session inputs and retained extractions'} · This is historical evidence, not current engine state. Values omitted from retained evidence cannot be reconstructed.</p>}
    {scope !== 'current' && historyIncomplete && <p role="status">Execution evidence may be incomplete; variable origins and changes are unavailable.{incompleteValuesUnverified ? ' Reconstructed values are unverified and may be stale.' : ''}</p>}
    <div className="debug-watch-table-scroll" role="region" aria-label="Variable values and provenance" tabIndex={0}>
    <table className="debug-watch-table" data-table-pagination="off"><caption className="sr-only">Variable values and provenance</caption><colgroup><col className="debug-watch-column-action" /><col className="debug-watch-column-name" /><col className="debug-watch-column-value" /><col className="debug-watch-column-changed" /></colgroup><thead><tr><th scope="col">Watch</th><th scope="col">Name / namespace</th><th scope="col">Value</th><th scope="col">Last changed</th></tr></thead><tbody>{names.map(name => {
      const changed = lastChanged.get(name) ?? -1
      const runtimeVal = valueForName(values, name) ?? ((!finalValuesRecorded || showPreRunTable) ? valueForName(seed, name) : undefined)
      const paramInfo = scriptParameters.get(name)
      const value = runtimeVal !== undefined ? runtimeVal : paramInfo?.configuredValue
      const originKnown = !historyIncomplete && scope !== 'current' && valueForName(reconstructed, name) !== undefined && reconstructed[name] === runtimeVal
      return <tr key={name}><td><button type="button" aria-label={`Pin ${name}`} aria-pressed={pinned.includes(name)} onClick={() => setPinned(p => p.includes(name) ? p.filter(n => n !== name) : [...p, name])}>{pinned.includes(name) ? 'Pinned' : 'Pin'}</button></td><th scope="row">{name}<small>{paramInfo ? `param · ${paramInfo.type}` : name.includes('.') ? name.split('.')[0] : 'var'}</small></th><td><code>{value === '' ? '(empty)' : value ?? 'Not available'}</code>{changedAtExecution(name) && <small>Changed</small>}</td><td>{originKnown && changed >= 0 ? <button type="button" onClick={() => onSelect(changed)}>{events[changed].action_name} · {eventLocation(events[changed])}</button> : (originKnown || showPreRunTable) && valueForName(seed, name) !== undefined && valueForName(seed, name) === value ? 'Session input' : paramInfo ? paramInfo.provenance : authoredVariables.has(name) && runtimeVal === undefined ? authoredVariables.get(name)! : 'Unknown / not retained'}</td></tr>
    })}</tbody></table></div>{!names.length && <p>No variables match this view.</p>}
    {scope !== 'current' && <details><summary>Values as JSON</summary><SnapshotBodyPreview body={JSON.stringify(values, null, 2)} headers={{ 'Content-Type': 'application/json' }} /></details>}
    </>}
  </section>
}

export function DebugTimeline({
  events,
  selectedIndex,
  onSelect,
  onInspect,
  variant = 'dock',
}: {
  events: BrowserReplayEvent[]
  selectedIndex?: number | null
  onSelect: (index: number) => void
  onInspect?: (index: number) => void
  variant?: 'dock' | 'summary'
}) {
  const [search, setSearch] = useState('')
  const [failures, setFailures] = useState(false)
  const [scaleMode, setScaleMode] = useState<DebugTimelineScaleMode>('waterfall')
  const [localSelectedIndex, setLocalSelectedIndex] = useState<number | null>(null)

  const model = useMemo(() => buildDebugTimelineModel(events, scaleMode), [events, scaleMode])
  const q = search.toLowerCase()
  const filteredRows = useMemo(
    () => model.rows.filter(r => (!failures || !r.success) && `${r.actionName} ${r.target} ${r.location}`.toLowerCase().includes(q)),
    [model.rows, failures, q],
  )

  useEffect(() => {
    setLocalSelectedIndex(null)
  }, [selectedIndex, events])

  const activeIndex = localSelectedIndex ?? selectedIndex
  // Only the real selection is highlighted and announced as current; the
  // inspector previews the first visible row when nothing visible is selected.
  const selectedRow = filteredRows.find(r => r.index === activeIndex) ?? null
  const activeRow = selectedRow ?? filteredRows[0] ?? null
  const selectionHidden = selectedRow === null && activeIndex != null && model.rows.some(r => r.index === activeIndex)

  const renderPhaseBar = (t: { trackOffsetPct: number; trackWidthPct: number; segments: typeof model.rows[0]['segments'] }) => (
    <div className="debug-timeline-bar-track" aria-hidden="true">
      <div className="debug-timeline-bar-fill" style={{ left: `${t.trackOffsetPct}%`, width: `${t.trackWidthPct}%` }}>
        {t.segments.map((seg, i) => (
          <span
            key={`${seg.key}-${i}`}
            className={`debug-timeline-bar-segment phase-${seg.key}`}
            style={{ width: `${seg.widthPct}%` }}
            title={`${seg.label}: ${seg.durationMs.toFixed(1)} ms`}
          />
        ))}
      </div>
    </div>
  )

  return (
    <div className={variant === 'summary' ? 'debug-summary-waterfall' : 'debug-execution-timeline'}>
      <div className="debug-session-tools debug-timeline-toolbar">
        <div>
          <label>{variant === 'summary' ? 'Filter waterfall' : 'Find execution'}<input value={search} onChange={e => setSearch(e.target.value)} /></label>
          <label><input type="checkbox" checked={failures} onChange={e => setFailures(e.target.checked)} />Failures only</label>
        </div>
        <div>
          <div className="debug-timeline-scale-switch" role="group" aria-label="Timeline scale">
            {([['waterfall', 'Waterfall (session)'], ['stacked', 'Stacked (100%)']] as const).map(([mode, label]) => (
              <button key={mode} type="button" className={scaleMode === mode ? 'active' : ''} aria-pressed={scaleMode === mode} onClick={() => setScaleMode(mode)}>
                {label}
              </button>
            ))}
          </div>
          <ul className="debug-timeline-legend" aria-label="Traffic phases">
            {DEBUG_TIMELINE_LEGEND_PHASES.map(p => (
              <li key={p.key}>
                <i className={`debug-timeline-swatch phase-${p.key}`} aria-hidden="true" />
                <span>{p.shortLabel}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      {!filteredRows.length ? (
        <p>{events.length ? 'No executions match these filters.' : 'No completed executions captured yet.'}</p>
      ) : (
        <div className="debug-timeline-workspace">
          <div className="debug-timeline-waterfall-table" role="region" aria-label="Execution timeline waterfall">
            <div className="debug-timeline-header" aria-hidden="true">
              <span>Action / target</span>
              <span>Status / protocol</span>
              <span className="debug-timeline-col-time">Duration</span>
              <div className="debug-timeline-ruler">
                {model.rulerTicks.map((tick, idx) => <span key={idx}>{tick}</span>)}
              </div>
            </div>
            {filteredRows.map(row => {
              const isSelected = selectedRow?.index === row.index
              const stateCls = row.success ? 'passed' : 'failed'
              return (
                <div key={row.id} className="debug-timeline-row-group">
                  <button
                    type="button"
                    className={`debug-timeline-row debug-timeline-waterfall-row ${isSelected ? 'selected' : ''} ${stateCls}`}
                    aria-current={isSelected ? 'true' : undefined}
                    onClick={() => { setLocalSelectedIndex(row.index); onSelect(row.index) }}
                  >
                    <div className="debug-timeline-cell-main">
                      <div>
                        <span className={`debug-timeline-status-dot ${stateCls}`} aria-hidden="true" />
                        <strong>{row.success ? 'Passed' : 'Failed'} · {row.actionName}</strong>
                      </div>
                      <small>{row.location} · {row.errorMessage || row.target}</small>
                      {row.extractedVariables.length > 0 && <small>Variables: {row.extractedVariables.join(', ')}</small>}
                    </div>
                    <div className="debug-timeline-cell-proto">
                      {row.statusCode != null && <span className={`debug-timeline-status-code ${stateCls}`}>HTTP {row.statusCode}</span>}
                      <span>{row.protocolLabel}{row.connectionReused ? ' · reused' : ''}</span>
                    </div>
                    <span className="debug-timeline-cell-duration">{row.durationMs.toFixed(1)} ms</span>
                    {renderPhaseBar(row)}
                  </button>
                  {isSelected && row.hops.length > 1 && (
                    <div className="debug-timeline-hop-list" aria-label={`Network hops for ${row.actionName}`}>
                      {row.hops.map(hop => (
                        <div key={hop.hopIndex} className="debug-timeline-hop-row">
                          <span>{hop.label}</span>
                          <span>{hop.protocolLabel}{hop.connectionReused ? ' · reused' : ''}</span>
                          <span className="debug-timeline-hop-duration">{hop.durationMs.toFixed(1)} ms</span>
                          {renderPhaseBar(hop)}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>

          {activeRow && (
            <aside className="debug-timeline-inspector" aria-label="Timing breakdown">
              <header>
                <div>
                  <strong>{activeRow.actionName}</strong>
                  <span>
                    {activeRow.location} · Queued at +{activeRow.startOffsetMs.toFixed(1)} ms · Started at +{activeRow.startedOffsetMs.toFixed(1)} ms · {activeRow.protocolLabel}
                    {activeRow.connectionReused ? ' · Reused connection' : ''}
                  </span>
                  {!selectedRow && (
                    <small className="debug-timeline-inspector-preview">
                      {selectionHidden ? 'Selected execution is hidden by the current filters · ' : ''}Preview · first visible execution
                    </small>
                  )}
                </div>
                <code>{activeRow.durationMs.toFixed(1)} ms</code>
              </header>
              <div className="debug-timeline-breakdown-list">
                {activeRow.breakdown.map((item, idx) => {
                  const showCat = item.category !== 'execution' && item.category !== (idx > 0 ? activeRow.breakdown[idx - 1].category : null)
                  return (
                    <div key={item.key}>
                      {showCat && <div className="debug-timeline-breakdown-category">{item.category === 'connection' ? 'Connection preparation' : 'Request and response'}</div>}
                      <div className="debug-timeline-breakdown-row">
                        <span>
                          <i className={`debug-timeline-swatch phase-${item.key}`} aria-hidden="true" />
                          <span>{item.label}</span>
                        </span>
                        <div aria-hidden="true">
                          {item.durationMs > 0 && <span className={`phase-${item.key}`} style={{ left: `${item.startPct}%`, width: `${item.widthPct}%` }} />}
                        </div>
                        <span>{item.durationMs > 0 ? `${item.durationMs.toFixed(1)} ms` : item.unavailableReason || '—'}</span>
                      </div>
                    </div>
                  )
                })}
              </div>
              <footer>
                <small>{activeRow.extractedVariables.length > 0 ? `Extracted: ${activeRow.extractedVariables.join(', ')}` : activeRow.errorMessage || `${activeRow.methodOrType} ${activeRow.target}`}</small>
                <div className="debug-timeline-inspector-actions">
                  <strong>Total: {activeRow.durationMs.toFixed(1)} ms</strong>
                  {onInspect && (
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => onInspect(activeRow.index)}>
                      Inspect {activeRow.actionName || 'action'}
                    </button>
                  )}
                </div>
              </footer>
            </aside>
          )}
        </div>
      )}
    </div>
  )
}


function HistoricalEvidence({ report }: { report: BrowserReplayReport }) {
  const [selected, setSelected] = useState(0)
  const event = report.events[selected]
  if (report.runtime_evidence_unavailable) return <p role="status">{RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE}</p>
  if (report.protocol === 'browser') return <BrowserReplayDebugger report={report} running={false} selectedEventIndex={selected} onSelectEvent={setSelected} />
  return <div className="debug-history-evidence"><label>Historical execution<select value={selected} onChange={e => setSelected(Number(e.target.value))}>{report.events.map((e, index) => <option value={index} key={index}>{eventLocation(e)} · {e.action_name} · {e.success ? 'Passed' : 'Failed'}</option>)}</select></label>{event ? <>
    <p>{event.error_message || `${event.method || event.action_type} ${event.target}`}</p><p>Response status: {event.status_code ?? 'Not captured'}</p>
    <HTTPVersionReplayEvidence evidence={event.protocol_evidence} />
    <HTTPResponseContentEvidence evidence={event.protocol_evidence} bodyState={event.snapshot?.response_body_state || event.response_body_state} />
    <HTTPNetworkTraceEvidence evidence={event.protocol_evidence} networkEntries={event.network_entries || event.snapshot?.network_entries} />
    <div className="debug-history-columns"><section><h4>Request</h4>{event.snapshot?.request_wire_headers_unavailable && <p role="status">Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.</p>}{event.snapshot?.request_headers_omitted && <p role="status">Some request headers were omitted or shortened by retention limits.</p>}<SnapshotTextPreview text={`${event.snapshot?.request_method || event.method || event.action_type} ${event.snapshot?.request_url || event.target}\n${formatReplayHeaders(event.snapshot ? event.snapshot.request_headers : event.headers)}`} /><SnapshotBodyPreview body={event.snapshot ? event.snapshot.request_body : event.body} emptyMessage="No request body retained" /></section><section><h4>Response</h4><SnapshotTextPreview text={formatReplayHeaders(event.snapshot?.response_headers)} /><SnapshotBodyPreview body={event.snapshot?.response_body} headers={event.snapshot?.response_headers} emptyMessage={`Response body: ${event.snapshot?.response_body_state || event.response_body_state || 'not retained'}`} /></section></div>
    {Boolean(event.protocol_evidence_omitted_count) && <p role="status">{event.protocol_evidence_omitted_count} protocol evidence {event.protocol_evidence_omitted_count === 1 ? 'item was' : 'items were'} omitted or shortened by retention limits.</p>}
    {event.protocol_evidence && <details><summary>Protocol evidence</summary><SnapshotBodyPreview body={JSON.stringify(event.protocol_evidence, null, 2)} /></details>}<details><summary>Extracted variables</summary>{event.action_type === 'live_table' ? <SnapshotBodyPreview body={JSON.stringify(event.extractions || {}, null, 2)} /> : <ReplayExtractions event={event} />}</details>
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
  const configuration = (record: Omit<DebugRunRecord, 'report'> | null | undefined, report: BrowserReplayReport) => ({ target: record?.target, failurePolicy: record?.failurePolicy, capture: record?.options.capture, ignoreThinkTime: record?.options.ignoreThinkTime, networkTrace: record ? record.options.networkTrace !== false : undefined, iterations: record ? record.iterations ?? 1 : undefined, inputs: record?.inputs, runtime: report.runtime_profile })
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
  return <details className="debug-breakpoint-controls"><summary>Breakpoints ({persistent.length})</summary><button type="button" disabled={disabled || !persistent.length} onClick={() => onChange(breakpoints.map(b => b.once ? b : { ...b, disabled: persistent.some(p => !p.disabled) }))}>{persistent.some(b => !b.disabled) ? 'Disable all breakpoints' : 'Enable all breakpoints'}</button><p>Hit count counts eligible visits during this run. Iteration is zero-based and available on nested action breakpoints; it targets the innermost loop. Editing breakpoints resets hit counts.</p>{persistent.map((bp, index) => <div className="debug-session-tools" key={index}><span>{bp.phase} {bp.action_index + 1}{bp.step_path?.length ? ` › ${bp.step_path.join(' › ')}` : bp.request_index != null ? ` › request ${bp.request_index + 1}` : ''}</span><label><input type="checkbox" disabled={disabled} checked={!bp.disabled} onChange={e => onChange(breakpoints.map(b => b === bp ? { ...b, disabled: !e.target.checked } : b))} />Enabled</label><BreakpointNumber label="Break on hit" minimum={1} disabled={disabled} value={bp.hit_count || undefined} placeholder="Every hit" onCommit={hit_count => onChange(breakpoints.map(b => b === bp ? { ...b, hit_count } : b))} /><BreakpointNumber label="Loop iteration" minimum={0} disabled={disabled || (!bp.step_path?.length && bp.request_index == null)} value={bp.iteration} placeholder="Any" onCommit={iteration => onChange(breakpoints.map(b => b === bp ? { ...b, iteration } : b))} /></div>)}</details>
}
