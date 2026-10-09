import { useEffect, useMemo, useRef, useState } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type {
  ScenarioMode,
  TrafficProfile,
  TrafficProfileAnalyzeResponse,
  TrafficProfileDeriveRequest,
  TrafficProfileDeriveResponse,
} from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import type { ScenarioUpsertPayload } from './scenarioPayloadUtils'
import { formatTrafficProfileAxisTime, inferTimestampMode, trafficProfileTargetAt } from './trafficProfilePreviewModel'

interface Props {
  projectId?: string
  groupNames: string[]
  scenarioDraft: ScenarioUpsertPayload
  existingProfile?: TrafficProfile
  onApply: (result: TrafficProfileDeriveResponse) => void
}

type LayoutMode = 'wide' | 'long'
type ARTUnit = 'milliseconds' | 'seconds'
type ARTSelection = { basis: 'source_series' | 'operator_peak'; seriesKey?: string; value?: number; unit: ARTUnit }

const colors = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

function durationMilliseconds(value?: string): number {
  if (!value) return 0
  const units: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000 }
  const pattern = /(\d+(?:\.\d+)?)(ms|s|m|h)/g
  let total = 0
  let consumed = ''
  for (const match of value.matchAll(pattern)) {
    total += Number(match[1]) * units[match[2]]
    consumed += match[0]
  }
  return consumed === value ? total : 0
}

export default function CSVDrivenSourcePanel({ projectId, groupNames, scenarioDraft, existingProfile, onApply }: Props) {
  const api = useApi()
  const [mode, setMode] = useState<ScenarioMode>(existingProfile?.runtime_target === 'tps' ? 'goal_oriented' : scenarioDraft.scenario_mode || 'vuser_driven')
  const [layout, setLayout] = useState<LayoutMode>(existingProfile?.source.file?.series_column ? 'long' : 'wide')
  const [file, setFile] = useState<File | null>(null)
  const [analysis, setAnalysis] = useState<TrafficProfileAnalyzeResponse | null>(null)
  const [timestampColumn, setTimestampColumn] = useState(existingProfile?.source.file?.timestamp_column || '')
  const [timestampMode, setTimestampMode] = useState<'absolute' | 'relative'>(existingProfile?.source.file?.timestamp_mode === 'relative' ? 'relative' : 'absolute')
  const [relativeStartTime, setRelativeStartTime] = useState(existingProfile?.source.file?.relative_start_time || '00:00')
  const [valueColumns, setValueColumns] = useState<string[]>(existingProfile?.source.file?.value_columns || [])
  const [seriesColumn, setSeriesColumn] = useState(existingProfile?.source.file?.series_column || '')
  const [artColumns, setARTColumns] = useState<string[]>(existingProfile?.source.file?.art_columns || [])
  const [timezone, setTimezone] = useState(existingProfile?.source.file?.timezone || 'UTC')
  const [mappings, setMappings] = useState<Record<string, string>>(() => Object.fromEntries(existingProfile?.mappings.map(mapping => [mapping.series_key, mapping.group_name]) || []))
  const [art, setART] = useState<Record<string, ARTSelection>>(() => Object.fromEntries(existingProfile?.mappings.flatMap(mapping => mapping.art ? [[mapping.group_name, {
    basis: mapping.art.basis as ARTSelection['basis'],
    seriesKey: mapping.art.series_key,
    value: mapping.art.basis === 'operator_peak' ? mapping.art.peak_art_ms : undefined,
    unit: 'milliseconds' as const,
  }]] : []) || []))
  const [minVusers, setMinVusers] = useState(scenarioDraft.goal_config?.min_vusers || 1)
  const [maxVusers, setMaxVusers] = useState(scenarioDraft.goal_config?.max_vusers || 100)
  const [derived, setDerived] = useState<TrafficProfileDeriveResponse | null>(null)
  const [derivedFingerprint, setDerivedFingerprint] = useState('')
  const inputFingerprint = JSON.stringify([projectId, scenarioDraft, groupNames, mode, layout, timestampColumn, timestampMode, relativeStartTime, valueColumns, seriesColumn, artColumns, timezone, mappings, art, minVusers, maxVusers])
  const currentFingerprint = useRef(inputFingerprint)
  currentFingerprint.current = inputFingerprint
  const requestGeneration = useRef(0)
  useEffect(() => () => { requestGeneration.current++ }, [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const selectedSheet = analysis?.sheets?.[0]
  const analyzed = Boolean(analysis?.series?.length)

  const resetCandidate = () => {
    setAnalysis(null)
    setMappings({})
    setDerived(null)
    setError('')
  }

  const invalidateColumnMapping = () => {
    setAnalysis(current => current ? { ...current, series: [], art_series: [] } : current)
    setMappings({})
    setDerived(null)
    setError('')
  }

  const analyze = async () => {
    if (!file) return
    const form = new FormData()
    if (projectId) form.set('project_id', projectId)
    form.set('file', file)
    if (timestampColumn) form.set('timestamp_column', timestampColumn)
    form.set('timestamp_mode', timestampMode)
    if (timestampMode === 'relative') form.set('relative_start_time', relativeStartTime)
    valueColumns.forEach(column => form.append('value_column', column))
    if (layout === 'long' && seriesColumn) form.set('series_column', seriesColumn)
    if (mode === 'goal_oriented') artColumns.forEach(column => form.append('art_column', column))
    form.set('timezone', timezone)
    const generation = ++requestGeneration.current
    const submittedFingerprint = inputFingerprint
    setBusy(true)
    setError('')
    setDerived(null)
    try {
      const result = await api.analyzeCSVDriven(form)
      if (generation !== requestGeneration.current || submittedFingerprint !== currentFingerprint.current) return
      setAnalysis(result)
      if (!result.series?.length) {
        setMappings({})
        const columns = result.sheets?.[0]?.columns || []
        const time = columns.find(column => /^(elapsed|time|timestamp|datetime)$/i.test(column))
        if (!timestampColumn && time) {
          setTimestampColumn(time)
          setTimestampMode(inferTimestampMode(result.sheets?.[0]?.samples, columns, time) || (/elapsed/i.test(time) ? 'relative' : timestampMode))
        }
        if (!valueColumns.length && layout === 'wide') setValueColumns(columns.filter(column => groupNames.some(group => group.toLowerCase() === column.toLowerCase())))
      } else {
        setMappings(Object.fromEntries(result.series.map(item => [item.series.key, groupNames.find(group => group.toLowerCase() === item.series.name.toLowerCase() || group.toLowerCase() === item.series.key.toLowerCase()) || ''])))
      }
    } catch (cause) {
      if (generation === requestGeneration.current && submittedFingerprint === currentFingerprint.current) setError(getErrorMessage(cause, 'Unable to analyze the CSV schedule.'))
    } finally {
      if (generation === requestGeneration.current) setBusy(false)
    }
  }

  // Analyze needs these columns; say which one is missing instead of only disabling it.
  const analyzeBlocker = !timestampColumn ? 'Choose the time column to analyze the schedule.'
    : layout === 'long' && !seriesColumn ? 'Choose the group / series column to analyze the schedule.'
      : valueColumns.length === 0 ? `Choose ${layout === 'wide' ? 'at least one target column' : 'the target column'} to analyze the schedule.`
        : ''
  const mappingRows = analysis?.series || []
  const mappedGroups = Object.values(mappings).filter(Boolean)
  const mappingsReady = groupNames.length > 0
    && groupNames.every(group => mappedGroups.filter(mapped => mapped === group).length === 1)
    && mappedGroups.length === groupNames.length
  const artReady = mode === 'vuser_driven' || groupNames.every(group => {
    const selection = art[group]
    return selection?.basis === 'source_series' ? Boolean(selection.seriesKey) : selection?.basis === 'operator_peak' && Number(selection.value) > 0
  })
  const guardrailsReady = mode === 'vuser_driven' || (minVusers >= 1 && maxVusers >= minVusers)
  const scriptsReady = scenarioDraft.groups.length > 0 && scenarioDraft.groups.every(group => Boolean(group.script_id?.trim()))

  const derive = async () => {
    if (!analysis || !mappingsReady || !artReady || !guardrailsReady || !scriptsReady) return
    const selectedMappings: TrafficProfileDeriveRequest['mappings'] = mappingRows.flatMap(item => {
      const groupName = mappings[item.series.key]
      if (!groupName) return []
      const selection = art[groupName]
      return [{
        series_key: item.series.key,
        group_name: groupName,
        ...(mode === 'goal_oriented' && selection ? {
          art: selection.basis === 'source_series'
            ? { basis: 'source_series' as const, series_key: selection.seriesKey, unit: selection.unit }
            : { basis: 'operator_peak' as const, value: selection.value, unit: selection.unit },
        } : {}),
      }]
    })
    const draft: ScenarioUpsertPayload = {
      ...scenarioDraft,
      scenario_mode: mode,
      ...(mode === 'goal_oriented' ? {
        goal_config: { ...(scenarioDraft.goal_config || {}), min_vusers: minVusers, max_vusers: maxVusers },
      } : {}),
    }
    const generation = ++requestGeneration.current
    const submittedFingerprint = inputFingerprint
    setBusy(true)
    setError('')
    try {
      const source = mode === 'vuser_driven' && analysis.source.file
        ? { ...analysis.source, file: { ...analysis.source.file, art_columns: undefined } }
        : analysis.source
      const result = await api.deriveCSVDriven({
        project_id: projectId,
        source,
        series: (analysis.series || []).map(item => item.series),
        ...(mode === 'goal_oriented' && analysis.art_series?.length ? { art_series: analysis.art_series.map(item => item.series) } : {}),
        mappings: selectedMappings,
        value_semantics: mode === 'goal_oriented' ? 'tps' : 'vusers',
        gap_policy: 'drop',
        window: { mode: 'full' },
        scenario_draft: draft,
      })
      if (generation !== requestGeneration.current || submittedFingerprint !== currentFingerprint.current) return
      setDerived(result)
      setDerivedFingerprint(submittedFingerprint)
    } catch (cause) {
      if (generation === requestGeneration.current && submittedFingerprint === currentFingerprint.current) setError(getErrorMessage(cause, 'Unable to build the CSV schedule preview.'))
    } finally {
      if (generation === requestGeneration.current) setBusy(false)
    }
  }

  const chart = useMemo(() => {
    if (!derived || derivedFingerprint !== inputFingerprint) return { rows: [] as Array<Record<string, number>>, keys: [] as string[], duration: 1 }
    const duration = durationMilliseconds(derived.traffic_profile.target_duration)
    const offsets = Array.from(new Set([
      ...derived.traffic_profile.mappings.flatMap(mapping => mapping.points.map(point => point.offset_ms)),
      duration,
    ])).sort((a, b) => a - b)
    const rows = offsets.map(offset => {
      const row: Record<string, number> = { offset }
      derived.traffic_profile.mappings.forEach((mapping, index) => { row[`group_${index}`] = trafficProfileTargetAt(mapping, offset / 1000) })
      return row
    })
    return { rows, keys: derived.traffic_profile.mappings.map((_, index) => `group_${index}`), duration: Math.max(1, duration) }
  }, [derived, derivedFingerprint, inputFingerprint])

  return (
    <fieldset disabled={busy} className="traffic-profile-panel csv-driven-panel">
      <div className="stepped-ramp-heading">
        <span>Guided worksheet</span>
        <h3>CSV Driven Setup</h3>
        <p>Upload an authored schedule and replay every row as an exact target boundary.</p>
      </div>

      <section className="stepped-ramp-section" aria-labelledby="csv-mode-title">
        <div className="stepped-ramp-number">1</div>
        <div className="stepped-ramp-section-body">
          <h4 id="csv-mode-title">Choose the runtime target</h4>
          <p>This choice controls how CSV values are validated and which capacity inputs are required.</p>
          <div className="stepped-ramp-scope" role="radiogroup" aria-label="CSV runtime target">
            <button type="button" role="radio" aria-checked={mode === 'vuser_driven'} className={mode === 'vuser_driven' ? 'active' : ''} onClick={() => { setMode('vuser_driven'); setDerived(null) }}><strong>VUser Driven</strong><span>Rows contain whole VUser targets</span></button>
            <button type="button" role="radio" aria-checked={mode === 'goal_oriented'} className={mode === 'goal_oriented' ? 'active' : ''} onClick={() => { setMode('goal_oriented'); setDerived(null) }}><strong>Goal Driven</strong><span>Rows contain TPS targets</span></button>
          </div>
        </div>
      </section>

      <section className="stepped-ramp-section" aria-labelledby="csv-upload-title">
        <div className="stepped-ramp-number">2</div>
        <div className="stepped-ramp-section-body">
          <h4 id="csv-upload-title">Upload and describe the CSV</h4>
          <p>Wide files use one target column per group. Long files use a series column plus one target column.</p>
          <p>Relative time requires a unit, such as <code>0s</code>, <code>60s</code> or <code>2m</code>; bare numbers are not elapsed seconds. Absolute time uses timestamps such as <code>2026-01-01T00:00:00Z</code>. Use increasing times and non-negative targets; VUser targets must be whole numbers.</p>
          <a className="btn btn-secondary" download="scenario-schedule.csv" href={`data:text/csv;charset=utf-8,${encodeURIComponent([
            ['elapsed', ...(groupNames.length ? groupNames : ['Group 1'])].map(value => '"' + value.replace(/"/g, '""') + '"').join(','),
            ...['0s', '60s', '120s'].map((time, index) => [time, ...(groupNames.length ? groupNames : ['Group 1']).map(() => index === 1 ? '10' : '0')].join(',')),
          ].join('\r\n'))}`}>Download sample for these groups</a>
          <ImportDropzone accept=".csv" file={file} onFile={next => { setFile(next); resetCandidate(); setTimestampColumn(''); setValueColumns([]); setSeriesColumn(''); setARTColumns([]) }} dropHint="Drop an exact workload schedule CSV" onError={setError} disabled={busy} />
          {file && !selectedSheet && <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void analyze()}>{busy ? 'Inspecting…' : 'Inspect columns'}</button>}
          {selectedSheet && <>
            <div className="stepped-ramp-choice" role="radiogroup" aria-label="CSV layout">
              <button type="button" role="radio" aria-checked={layout === 'wide'} className={layout === 'wide' ? 'active' : ''} onClick={() => { setLayout('wide'); setSeriesColumn(''); invalidateColumnMapping() }}>Wide columns</button>
              <button type="button" role="radio" aria-checked={layout === 'long'} className={layout === 'long' ? 'active' : ''} onClick={() => { setLayout('long'); setValueColumns(current => current.slice(0, 1)); invalidateColumnMapping() }}>Long rows</button>
            </div>
            {!!selectedSheet.samples?.length && <details><summary>Preview uploaded rows</summary><div className="data-table"><table><thead><tr>{selectedSheet.columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{selectedSheet.samples.slice(0, 5).map((row, index) => <tr key={index}>{row.map((value, col) => <td key={col}>{value}</td>)}</tr>)}</tbody></table></div></details>}
            <p>Suggested columns are editable. Confirm time values and target columns, then analyze the schedule.</p>
            <div className="settings-grid compact-grid">
              <label><span>Time column</span><select value={timestampColumn} onChange={event => { const next = event.target.value; setTimestampColumn(next); const inferred = inferTimestampMode(selectedSheet.samples, selectedSheet.columns, next); if (inferred) setTimestampMode(inferred); invalidateColumnMapping() }}><option value="">Select…</option>{selectedSheet.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Time values</span><select value={timestampMode} onChange={event => { setTimestampMode(event.target.value as typeof timestampMode); invalidateColumnMapping() }}><option value="absolute">Absolute timestamps</option><option value="relative">Relative elapsed time</option></select></label>
              {timestampMode === 'relative' && <label><span>Elapsed zero</span><input type="time" step={1} value={relativeStartTime} onChange={event => { setRelativeStartTime(event.target.value); invalidateColumnMapping() }} /></label>}
              {layout === 'long' && <label><span>Group / series column</span><select value={seriesColumn} onChange={event => { setSeriesColumn(event.target.value); invalidateColumnMapping() }}><option value="">Select…</option>{selectedSheet.columns.map(column => <option key={column}>{column}</option>)}</select></label>}
              <label><span>{layout === 'wide' ? 'Target columns' : 'Target column'}</span><select multiple={layout === 'wide'} size={layout === 'wide' ? Math.min(6, Math.max(2, selectedSheet.columns.length)) : undefined} value={valueColumns} onChange={event => { setValueColumns(Array.from(event.currentTarget.selectedOptions, option => option.value).slice(0, layout === 'wide' ? undefined : 1)); invalidateColumnMapping() }}>{selectedSheet.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              {mode === 'goal_oriented' && <label><span>Optional ART columns</span><select multiple size={Math.min(6, Math.max(2, selectedSheet.columns.length))} value={artColumns} onChange={event => { setARTColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateColumnMapping() }}>{selectedSheet.columns.map(column => <option key={column}>{column}</option>)}</select></label>}
              {timestampMode === 'absolute' && <label><span>Timezone</span><input value={timezone} onChange={event => { setTimezone(event.target.value); invalidateColumnMapping() }} /></label>}
            </div>
            <button type="button" className="btn btn-secondary" disabled={busy || Boolean(analyzeBlocker)} aria-describedby={analyzeBlocker ? 'csv-analyze-blocker' : undefined} onClick={() => void analyze()}>{busy ? 'Analyzing…' : analyzed ? 'Re-analyze schedule' : 'Analyze schedule'}</button>
            {analyzeBlocker && <p id="csv-analyze-blocker" className="setting-desc">{analyzeBlocker}</p>}
          </>}
        </div>
      </section>

      {analyzed && <section className="stepped-ramp-section" aria-labelledby="csv-map-title">
        <div className="stepped-ramp-number">3</div>
        <div className="stepped-ramp-section-body">
          <h4 id="csv-map-title">Map groups and verify capacity</h4>
          <p>Every scenario group must receive exactly one CSV series.</p>
          <div className="data-table"><table><thead><tr><th>CSV series</th><th>Rows</th><th>Cadence</th><th>Peak</th><th>Scenario group</th></tr></thead><tbody>{mappingRows.map(item => <tr key={item.series.key}><td>{item.series.name}</td><td>{item.stats.count}</td><td>{formatTrafficProfileAxisTime(item.stats.detected_bucket / 1e9)}</td><td>{item.stats.max}</td><td><select aria-label={`Map ${item.series.name}`} value={mappings[item.series.key] || ''} onChange={event => { setMappings(current => ({ ...current, [item.series.key]: event.target.value })); setDerived(null) }}><option value="">Ignore</option>{groupNames.map(group => <option key={group}>{group}</option>)}</select></td></tr>)}</tbody></table></div>
          {mode === 'goal_oriented' && <>
            <div className="data-table"><table><thead><tr><th>Group</th><th>ART basis</th><th>ART value or series</th><th>Unit</th></tr></thead><tbody>{groupNames.map(group => {
              const selection = art[group] || { basis: 'operator_peak', unit: 'milliseconds' as const }
              return <tr key={group}><td>{group}</td><td><select aria-label={`ART basis for ${group}`} value={selection.basis} onChange={event => setART(current => ({ ...current, [group]: { basis: event.target.value as ARTSelection['basis'], unit: selection.unit } }))}><option value="operator_peak">Estimated peak ART</option>{analysis?.art_series?.length ? <option value="source_series">CSV ART series</option> : null}</select></td><td>{selection.basis === 'source_series' ? <select aria-label={`ART series for ${group}`} value={selection.seriesKey || ''} onChange={event => setART(current => ({ ...current, [group]: { ...selection, seriesKey: event.target.value } }))}><option value="">Select…</option>{analysis?.art_series?.map(item => <option key={item.series.key} value={item.series.key}>{item.series.name}</option>)}</select> : <input aria-label={`Estimated peak ART for ${group}`} type="number" min={0.001} step="any" value={selection.value ?? ''} onChange={event => setART(current => ({ ...current, [group]: { ...selection, value: Number(event.target.value) } }))} />}</td><td><select aria-label={`ART unit for ${group}`} value={selection.unit} onChange={event => setART(current => ({ ...current, [group]: { ...selection, unit: event.target.value as ARTUnit } }))}><option value="milliseconds">Milliseconds</option><option value="seconds">Seconds</option></select></td></tr>
            })}</tbody></table></div>
            <div className="stepped-ramp-fields two"><label className="scenario-field"><span>Minimum VUsers</span><input className="settings-input" type="number" min={1} value={minVusers} onChange={event => setMinVusers(Number(event.target.value))} /></label><label className="scenario-field"><span>Maximum VUsers</span><input className="settings-input" type="number" min={minVusers} value={maxVusers} onChange={event => setMaxVusers(Number(event.target.value))} /></label></div>
          </>}
          {!mappingsReady && <p className="settings-error-inline">Map one CSV series to every scenario group.</p>}
          {mode === 'goal_oriented' && !artReady && <p className="settings-error-inline">Provide CSV-backed or estimated peak ART for every group.</p>}
          {mode === 'goal_oriented' && !guardrailsReady && <p className="settings-error-inline">Maximum VUsers must be at least the minimum.</p>}
          {!scriptsReady && <p className="settings-error-inline">Assign a script to every scenario group before building the runtime schedule.</p>}
          <button type="button" className="btn btn-primary" disabled={busy || !mappingsReady || !artReady || !guardrailsReady || !scriptsReady} onClick={() => void derive()}>{busy ? 'Building…' : 'Build exact schedule preview'}</button>
        </div>
      </section>}

      {derived && derivedFingerprint === inputFingerprint && <section className="scenario-shape-guidance" role="status">
        <strong>{derived.traffic_profile.runtime_target === 'tps' ? 'Goal-driven TPS schedule' : 'VUser schedule'} · {derived.traffic_profile.target_duration}</strong>
        <p>{derived.traffic_profile.mappings.reduce((sum, mapping) => sum + mapping.points.length, 0).toLocaleString()} exact step boundaries across {derived.traffic_profile.mappings.length} group{derived.traffic_profile.mappings.length === 1 ? '' : 's'}.</p>
        <div style={{ height: 280 }}><ResponsiveContainer width="100%" height="100%"><LineChart data={chart.rows}><CartesianGrid strokeDasharray="3 3" /><XAxis dataKey="offset" type="number" domain={[0, chart.duration]} tickFormatter={value => formatTrafficProfileAxisTime(Number(value) / 1000)} /><YAxis /><Tooltip labelFormatter={value => `Elapsed ${formatTrafficProfileAxisTime(Number(value) / 1000)}`} /><Legend />{chart.keys.map((key, index) => <Line key={key} type="stepAfter" dataKey={key} name={derived.traffic_profile.mappings[index].group_name} stroke={colors[index % colors.length]} dot={false} isAnimationActive={false} />)}</LineChart></ResponsiveContainer></div>
        {derived.traffic_profile.sizing?.complete && <p><strong>Estimated peak capacity: {(derived.traffic_profile.sizing.estimated_peak_vusers ?? 0).toLocaleString()} VUsers.</strong> Suggested guardrails: {derived.traffic_profile.sizing.suggested_min_vusers}-{derived.traffic_profile.sizing.suggested_max_vusers} VUsers.</p>}
        {derived.warnings?.map(warning => <p key={warning} className="secondary">{warning}</p>)}
        <button type="button" className="btn btn-primary" disabled={busy || !scriptsReady || !mappingsReady || !artReady || !guardrailsReady} onClick={() => { if (derivedFingerprint === currentFingerprint.current) onApply(derived) }}>Apply CSV schedule</button>
      </section>}
      {error && <div className="form-error" role="alert">{error}</div>}
    </fieldset>
  )
}
