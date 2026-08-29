import { useEffect, useMemo, useState } from 'react'
import { CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type {
  TrafficProfileAnalyzeResponse,
  TrafficProfile,
  TrafficProfileDeriveRequest,
  TrafficProfileDeriveResponse,
  TrafficProfileGapPolicy,
  TrafficProfileSeries,
  TrafficProfileSource,
  TrafficProfileSourcesResponse,
  TrafficProfileValueSemantics,
} from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import type { ScenarioUpsertPayload } from './scenarioPayloadUtils'

interface Props {
  projectId?: string
  groupNames: string[]
  scenarioDraft: ScenarioUpsertPayload
  existingProfile?: TrafficProfile
  onApply: (result: TrafficProfileDeriveResponse) => void
}

const previewLimit = 1000
const allGroupsMapping = '__all_groups__'

interface GroupUploadSelection {
  analysis: TrafficProfileAnalyzeResponse
  seriesKey: string
}

function sample<T>(values: T[], limit = previewLimit): T[] {
  if (values.length <= limit) return values
  const stride = Math.ceil(values.length / limit)
  return values.filter((_, index) => index % stride === 0 || index === values.length - 1)
}

function localDateTimeValue(value?: string): string {
  if (!value) return ''
  const parsed = new Date(value)
  if (!Number.isFinite(parsed.getTime())) return ''
  return new Date(parsed.getTime() - parsed.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

function requiredISOString(value: string, label: string): string {
  const parsed = new Date(value)
  if (!value || !Number.isFinite(parsed.getTime())) throw new Error(`${label} must be a valid date and time.`)
  return parsed.toISOString()
}

function durationMilliseconds(value?: string): number {
  if (!value) return 0
  const units: Record<string, number> = { ns: 1e-6, us: 1e-3, 'µs': 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 }
  const pattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g
  let total = 0
  let consumed = ''
  for (const match of value.matchAll(pattern)) {
    total += Number(match[1]) * units[match[2]]
    consumed += match[0]
  }
  return consumed === value ? total : 0
}

export default function TrafficProfileSourcePanel({ projectId, groupNames, scenarioDraft, existingProfile, onApply }: Props) {
  const api = useApi()
  const savedSource = existingProfile?.source
  const savedQuery = savedSource?.prometheus
  const savedOpenSearch = savedSource?.opensearch
  const savedRange = savedQuery?.range || savedOpenSearch?.range
  const [capabilities, setCapabilities] = useState<TrafficProfileSourcesResponse | null>(null)
  const [assignmentMode, setAssignmentMode] = useState<'shared' | 'per_group'>(savedSource?.kind === 'multiple' ? 'per_group' : 'shared')
  const [sourceKind, setSourceKind] = useState<'file' | 'prometheus' | 'opensearch'>(savedSource?.kind === 'prometheus' || savedSource?.kind === 'opensearch' ? savedSource.kind : 'file')
  const [file, setFile] = useState<File | null>(null)
  const [groupUploads, setGroupUploads] = useState<Record<string, GroupUploadSelection>>({})
  const [analysis, setAnalysis] = useState<TrafficProfileAnalyzeResponse | null>(null)
  const [sheet, setSheet] = useState(savedSource?.file?.sheet || '')
  const [timestampColumn, setTimestampColumn] = useState(savedSource?.file?.timestamp_column || '')
  const [valueColumns, setValueColumns] = useState<string[]>(savedSource?.file?.value_columns || [])
  const [seriesColumn, setSeriesColumn] = useState(savedSource?.file?.series_column || '')
  const [timezone, setTimezone] = useState(savedSource?.file?.timezone || 'UTC')
  const [query, setQuery] = useState(savedQuery?.query || '')
  const [index, setIndex] = useState(savedOpenSearch?.index || '')
  const [timestampField, setTimestampField] = useState(savedOpenSearch?.timestamp_field || '@timestamp')
  const [metric, setMetric] = useState<'count' | 'sum' | 'avg'>(savedOpenSearch?.metric || 'count')
  const [metricField, setMetricField] = useState(savedOpenSearch?.metric_field || '')
  const [openSearchFilter, setOpenSearchFilter] = useState(savedOpenSearch?.filter ? JSON.stringify(savedOpenSearch.filter, null, 2) : '')
  const [rangeMode, setRangeMode] = useState<'relative' | 'absolute'>(savedRange?.mode || 'relative')
  const [lookback, setLookback] = useState(savedRange?.lookback || '24h')
  const [rangeStart, setRangeStart] = useState(localDateTimeValue(savedRange?.start))
  const [rangeEnd, setRangeEnd] = useState(localDateTimeValue(savedRange?.end))
  const [stepSeconds, setStepSeconds] = useState(savedRange?.step_seconds || 15)
  const [semantics, setSemantics] = useState<TrafficProfileValueSemantics>(existingProfile?.value_semantics || 'transactions_per_bucket')
  const [gapPolicy, setGapPolicy] = useState<TrafficProfileGapPolicy>(existingProfile?.gap_policy || 'hold')
  const [windowMode, setWindowMode] = useState<'full' | 'explicit' | 'peak'>(existingProfile?.window?.mode || 'full')
  const [peakDuration, setPeakDuration] = useState(existingProfile?.window?.duration || '1h')
  const [windowStart, setWindowStart] = useState(localDateTimeValue(existingProfile?.window?.start))
  const [windowEnd, setWindowEnd] = useState(localDateTimeValue(existingProfile?.window?.end))
  const [targetDuration, setTargetDuration] = useState(existingProfile?.target_duration || '')
  const [amplitudeMode, setAmplitudeMode] = useState<'factor' | 'peak'>(existingProfile?.amplitude?.mode || 'factor')
  const [amplitude, setAmplitude] = useState(existingProfile?.amplitude?.value ?? 1)
  const [mappings, setMappings] = useState<Record<string, string>>(() => Object.fromEntries((existingProfile?.mappings || []).map(mapping => [mapping.series_key, mapping.group_name])))
  const [derived, setDerived] = useState<TrafficProfileDeriveResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const candidateMatchesExisting = useMemo(() => Boolean(
    derived && existingProfile && JSON.stringify(derived.traffic_profile) === JSON.stringify(existingProfile),
  ), [derived, existingProfile])

  useEffect(() => {
    void api.getTrafficProfileSources().then(setCapabilities).catch(cause => setError(getErrorMessage(cause, 'Unable to load traffic-profile sources.')))
  }, [api])

  useEffect(() => {
    setDerived(null)
  }, [amplitude, amplitudeMode, assignmentMode, gapPolicy, groupUploads, mappings, peakDuration, semantics, targetDuration, windowEnd, windowMode, windowStart])

  const selectedSheet = analysis?.sheets?.find(candidate => candidate.name === sheet) || analysis?.sheets?.[0]

  const analyzeFile = async () => {
    if (!file) return
    const form = new FormData()
    // project_id must precede the file part: the server peeks only the leading
    // scalar fields and stops at the first file, so a trailing project_id is
    // never seen and the request is rejected for project-scoped users.
    if (projectId) form.set('project_id', projectId)
    form.set('file', file)
    if (sheet) form.set('sheet', sheet)
    if (timestampColumn) form.set('timestamp_column', timestampColumn)
    valueColumns.forEach(column => form.append('value_column', column))
    if (seriesColumn) form.set('series_column', seriesColumn)
    if (timezone) form.set('timezone', timezone)
    const result = await api.analyzeTrafficProfile(form)
    setAnalysis(result)
    const firstSheet = result.sheets?.find(candidate => candidate.name === sheet) || result.sheets?.[0]
    if (firstSheet && !sheet) setSheet(firstSheet.name)
    if (result.series?.length) seedMappings(result.series.map(item => item.series))
  }

  const querySource = (): TrafficProfileSource => {
    if (!Number.isFinite(stepSeconds) || stepSeconds <= 0) throw new Error('Step seconds must be greater than zero.')
    const range = rangeMode === 'relative'
      ? { mode: 'relative' as const, lookback, step_seconds: stepSeconds }
      : { mode: 'absolute' as const, start: requiredISOString(rangeStart, 'Range start'), end: requiredISOString(rangeEnd, 'Range end'), step_seconds: stepSeconds }
    if (sourceKind === 'prometheus') return { kind: 'prometheus', prometheus: { query, range } }
    let filter: unknown
    if (openSearchFilter.trim()) filter = JSON.parse(openSearchFilter)
    return { kind: 'opensearch', opensearch: { index, timestamp_field: timestampField, interval: `${stepSeconds}s`, metric, ...(metric !== 'count' ? { metric_field: metricField } : {}), ...(filter ? { filter } : {}), range } }
  }

  const seedMappings = (series: TrafficProfileSeries[]) => {
    setMappings(current => {
      if (assignmentMode === 'shared' && groupNames.length > 1) {
        return series[0] ? { [series[0].key]: allGroupsMapping } : {}
      }
      const next = { ...current }
      series.forEach((item, position) => { if (!next[item.key] && groupNames[position]) next[item.key] = groupNames[position] })
      return next
    })
  }

  const analyze = async () => {
    setBusy(true); setError(''); setDerived(null)
    try {
      if (sourceKind === 'file') await analyzeFile()
      else {
        const result = await api.analyzeTrafficProfile({ project_id: projectId, source: querySource() })
        setAnalysis(result)
        seedMappings((result.series || []).map(item => item.series))
      }
    } catch (cause) { setError(getErrorMessage(cause, 'Traffic-profile analysis failed.')) } finally { setBusy(false) }
  }

  const derive = async () => {
    let source: TrafficProfileSource
    let series: TrafficProfileSeries[]
    let selectedMappings: TrafficProfileDeriveRequest['mappings']
    if (assignmentMode === 'per_group') {
      const selections = groupNames.flatMap((groupName, position) => {
        const upload = groupUploads[groupName]
        const selected = upload?.analysis.series?.find(item => item.series.key === upload.seriesKey)?.series
        if (!upload || !selected) return []
        const keyedSeries = { ...selected, key: `group-${position + 1}:${selected.key}` }
        return [{ groupName, source: upload.analysis.source, series: keyedSeries }]
      })
      if (selections.length !== groupNames.length) { setError('Analyze and select a workload series for every scenario group.'); return }
      source = { kind: 'multiple', name: 'Per-group production traffic files', sources: selections.map(item => item.source) }
      series = selections.map(item => item.series)
      selectedMappings = selections.map(item => ({ series_key: item.series.key, group_name: item.groupName, source: item.source }))
    } else {
      source = analysis?.source || querySource()
      series = (analysis?.series || []).map(item => item.series)
      selectedMappings = series.flatMap(item => {
        const target = mappings[item.key]
        if (!target) return []
        if (target === allGroupsMapping) return groupNames.map(groupName => ({ series_key: item.key, group_name: groupName }))
        return [{ series_key: item.key, group_name: target }]
      })
    }
    if (!selectedMappings.length) { setError('Map at least one series to a scenario group.'); return }
    if (new Set(selectedMappings.map(item => item.group_name)).size !== selectedMappings.length) { setError('Each scenario group can be mapped from only one series.'); return }
    const mappedGroups = new Set(selectedMappings.map(item => item.group_name))
    const conflicts = scenarioDraft.groups.filter((group, position) => mappedGroups.has(group.name || groupNames[position] || '') && ((group.stages?.length || 0) > 0 || Boolean(group.start_offset) || Boolean(group.ramp_up) || Boolean(group.ramp_down)))
    if (conflicts.length && !window.confirm('Applying this profile will clear manual stages, simple ramps, and start offsets for mapped groups. Continue?')) return
    const draft: ScenarioUpsertPayload = structuredClone(scenarioDraft)
    draft.groups.forEach((group, position) => {
      if (!group.name) group.name = groupNames[position]
      if (mappedGroups.has(group.name || '')) { group.stages = []; delete group.start_offset; delete group.ramp_up; delete group.ramp_down }
    })
    setBusy(true); setError('')
    try {
      if (!Number.isFinite(amplitude) || amplitude < 0) throw new Error('Amplitude must be zero or greater.')
      const payload: TrafficProfileDeriveRequest = {
        project_id: projectId,
        source,
        series,
        mappings: selectedMappings,
        value_semantics: semantics,
        gap_policy: gapPolicy,
        window: windowMode === 'peak' ? { mode: 'peak', duration: peakDuration } : windowMode === 'explicit' ? { mode: 'explicit', start: requiredISOString(windowStart, 'Window start'), end: requiredISOString(windowEnd, 'Window end') } : { mode: 'full' },
        ...(targetDuration ? { target_duration: targetDuration } : {}),
        amplitude: { mode: amplitudeMode, value: amplitude },
        scenario_draft: draft,
      }
      setDerived(await api.deriveTrafficProfile(payload))
    } catch (cause) { setError(getErrorMessage(cause, 'Traffic-profile derivation failed.')) } finally { setBusy(false) }
  }

  const chartData = useMemo(() => {
    const firstGroupUpload = groupNames.map(name => groupUploads[name]).find(Boolean)
    const selectedGroupSeries = firstGroupUpload?.analysis.series?.find(item => item.series.key === firstGroupUpload.seriesKey)?.series
    const raw = (assignmentMode === 'per_group' ? selectedGroupSeries : analysis?.series?.[0]?.series)?.points || []
    const points = derived?.preview?.[0]?.points || []
    const oldPoints = existingProfile?.mappings?.[0]?.points || []
    if (!raw.length && !points.length && !oldPoints.length) return []
    const rawStart = raw.length ? Date.parse(raw[0].timestamp) : 0
    const rawSpan = raw.length > 1 ? Math.max(1, Date.parse(raw[raw.length - 1].timestamp) - rawStart) : 1
    const targetSpan = derived?.traffic_profile ? Math.max(1, durationMilliseconds(derived.traffic_profile.target_duration), points[points.length - 1]?.offset_ms || rawSpan) : rawSpan
    const rows: Array<{ elapsed: number; raw?: number; target?: number; oldTarget?: number }> = sample(raw).map(point => ({ elapsed: ((Date.parse(point.timestamp) - rawStart) / rawSpan) * targetSpan, raw: point.value }))
    sample(points).forEach(point => rows.push({ elapsed: point.offset_ms, target: point.target }))
    sample(oldPoints).forEach(point => rows.push({ elapsed: point.offset_ms, oldTarget: point.target }))
    return rows.sort((left, right) => left.elapsed - right.elapsed)
  }, [analysis, assignmentMode, derived, existingProfile, groupNames, groupUploads])

  const perGroupReady = groupNames.length > 1 && groupNames.every(name => {
    const upload = groupUploads[name]
    return Boolean(upload?.analysis.series?.some(item => item.series.key === upload.seriesKey))
  })
  const hasAnalyzedData = assignmentMode === 'per_group' ? perGroupReady : Boolean(analysis?.series?.length)

  const sourceAvailable = (kind: string) => {
    if (kind === 'file') return true
    const capability = capabilities?.sources.find(source => source.kind === kind)
    return Boolean(capability?.enabled && capability.configured)
  }

  return (
    <div className="traffic-profile-panel">
      {groupNames.length > 1 && <div className="traffic-profile-assignment-mode">
        <span className="traffic-profile-section-label">Workload assignment</span>
        <div className="scenario-source-toggle" aria-label="Workload assignment mode">
          <button type="button" className={`mode-chip ${assignmentMode === 'shared' ? 'active' : ''}`} onClick={() => setAssignmentMode('shared')}>One profile for all groups</button>
          <button type="button" className={`mode-chip ${assignmentMode === 'per_group' ? 'active' : ''}`} onClick={() => setAssignmentMode('per_group')}>Separate file per group</button>
        </div>
        <p className="secondary">{assignmentMode === 'shared' ? `The selected series will be copied to all ${groupNames.length} groups.` : 'Upload and select an independent production series for each group.'}</p>
      </div>}

      {assignmentMode === 'per_group' ? (
        <div className="traffic-profile-group-uploads">
          {groupNames.map(groupName => <GroupFileUploadCard key={groupName} groupName={groupName} projectId={projectId} onReady={selection => setGroupUploads(current => {
            const next = { ...current }
            if (selection) next[groupName] = selection
            else delete next[groupName]
            return next
          })} />)}
        </div>
      ) : <>
        <div className="scenario-source-toggle" aria-label="Traffic profile source">
          {(['file', 'prometheus', 'opensearch'] as const).map(kind => (
            <button key={kind} type="button" className={`mode-chip ${sourceKind === kind ? 'active' : ''}`} disabled={!sourceAvailable(kind)} onClick={() => { setSourceKind(kind); setAnalysis(null); setDerived(null) }}>{kind === 'file' ? 'CSV / XLSX' : kind === 'prometheus' ? 'Prometheus' : 'OpenSearch'}</button>
          ))}
        </div>

        {sourceKind === 'file' ? (
          <>
            <ImportDropzone accept=".csv,.tsv,.txt,.xlsx" file={file} onFile={next => {
              setFile(next)
              setAnalysis(null)
              setDerived(null)
              setSheet('')
              setTimestampColumn('')
              setValueColumns([])
              setSeriesColumn('')
              setError('')
            }} dropHint="Drop a production traffic CSV or XLSX file" onError={setError} disabled={busy} />
            {analysis?.sheets?.length ? <div className="settings-grid compact-grid">
              <label><span>Worksheet</span><select value={sheet} onChange={event => { setSheet(event.target.value); setTimestampColumn(''); setValueColumns([]); setSeriesColumn('') }}>{analysis.sheets.map(item => <option key={item.name}>{item.name}</option>)}</select></label>
              <label><span>Timestamp column</span><select value={timestampColumn} onChange={event => setTimestampColumn(event.target.value)}><option value="">Select…</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Value columns (select one or more)</span><select multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={valueColumns} onChange={event => setValueColumns(Array.from(event.currentTarget.selectedOptions, option => option.value))}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Optional series column</span><select value={seriesColumn} onChange={event => setSeriesColumn(event.target.value)}><option value="">None</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Timezone for naive timestamps</span><input value={timezone} onChange={event => setTimezone(event.target.value)} placeholder="America/New_York" /></label>
            </div> : null}
          </>
        ) : sourceKind === 'prometheus' ? (
          <div className="settings-grid compact-grid"><label className="span-2"><span>PromQL</span><textarea value={query} onChange={event => setQuery(event.target.value)} /></label><QueryRangeFields mode={rangeMode} setMode={setRangeMode} lookback={lookback} setLookback={setLookback} start={rangeStart} setStart={setRangeStart} end={rangeEnd} setEnd={setRangeEnd} stepSeconds={stepSeconds} setStepSeconds={setStepSeconds} /></div>
        ) : (
          <div className="settings-grid compact-grid"><label><span>Index</span><input value={index} onChange={event => setIndex(event.target.value)} /></label><label><span>Timestamp field</span><input value={timestampField} onChange={event => setTimestampField(event.target.value)} /></label><label><span>Metric</span><select value={metric} onChange={event => setMetric(event.target.value as typeof metric)}><option value="count">Count</option><option value="sum">Sum</option><option value="avg">Average</option></select></label>{metric !== 'count' && <label><span>Metric field</span><input value={metricField} onChange={event => setMetricField(event.target.value)} /></label>}<label className="span-2"><span>Optional filter Query DSL</span><textarea value={openSearchFilter} onChange={event => setOpenSearchFilter(event.target.value)} placeholder={'{"term":{"environment":"production"}}'} /></label><QueryRangeFields mode={rangeMode} setMode={setRangeMode} lookback={lookback} setLookback={setLookback} start={rangeStart} setStart={setRangeStart} end={rangeEnd} setEnd={setRangeEnd} stepSeconds={stepSeconds} setStepSeconds={setStepSeconds} /></div>
        )}
        <button type="button" className="btn btn-secondary" onClick={() => void analyze()} disabled={busy || !sourceAvailable(sourceKind) || (sourceKind === 'file' && !file)}>{busy ? 'Working…' : analysis?.series?.length ? 'Re-analyze' : 'Analyze source'}</button>
      </>}

      {hasAnalyzedData ? <>
        <div className="settings-grid compact-grid">
          <label><span>Values represent</span><select value={semantics} onChange={event => setSemantics(event.target.value as TrafficProfileValueSemantics)}><option value="transactions_per_bucket">Transactions per bucket</option><option value="cumulative_counter">Cumulative counter</option><option value="tps">TPS</option><option value="vusers">VUsers</option></select></label>
          <label><span>Gap policy</span><select value={gapPolicy} onChange={event => setGapPolicy(event.target.value as TrafficProfileGapPolicy)}><option value="hold">Hold previous</option><option value="zero">Insert zero</option><option value="drop">Drop missing</option></select></label>
          <label><span>Window</span><select value={windowMode} onChange={event => setWindowMode(event.target.value as typeof windowMode)}><option value="full">Full range</option><option value="explicit">Explicit range</option><option value="peak">Peak window</option></select></label>
          {windowMode === 'peak' && <label><span>Peak window duration</span><input value={peakDuration} onChange={event => setPeakDuration(event.target.value)} /></label>}
          {windowMode === 'explicit' && <><label><span>Window start</span><input type="datetime-local" value={windowStart} onChange={event => setWindowStart(event.target.value)} /></label><label><span>Window end</span><input type="datetime-local" value={windowEnd} onChange={event => setWindowEnd(event.target.value)} /></label></>}
          <label><span>Test duration (blank = 1:1)</span><input value={targetDuration} onChange={event => setTargetDuration(event.target.value)} placeholder="8h" /></label>
          <label><span>Amplitude</span><select value={amplitudeMode} onChange={event => setAmplitudeMode(event.target.value as typeof amplitudeMode)}><option value="factor">Multiply by</option><option value="peak">Scale peak to</option></select><input type="number" min={0} step="any" value={amplitude} onChange={event => setAmplitude(Number(event.target.value))} /></label>
        </div>
        {assignmentMode === 'shared' && analysis?.series?.length ? <div className="data-table"><table><thead><tr><th>Series</th><th>Points</th><th>Cadence</th><th>Source peak</th><th>Apply to</th></tr></thead><tbody>{analysis.series.map(item => <tr key={item.series.key}><td>{item.series.name}</td><td>{item.stats.count.toLocaleString()}</td><td>{Math.round(item.stats.detected_bucket / 1e9)}s</td><td>{item.stats.max}</td><td><select aria-label={`Apply ${item.series.name} to`} value={mappings[item.series.key] || ''} onChange={event => setMappings(current => ({ ...current, [item.series.key]: event.target.value }))}><option value="">Ignore</option>{groupNames.length > 1 && <option value={allGroupsMapping}>All groups</option>}{groupNames.map(name => <option key={name}>{name}</option>)}</select></td></tr>)}</tbody></table></div> : null}
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void derive()}>Build profile preview</button>
      </> : null}

      {chartData.length ? <div style={{ height: 280 }}><ResponsiveContainer width="100%" height="100%"><ComposedChart data={chartData}><CartesianGrid strokeDasharray="3 3" /><XAxis dataKey="elapsed" tickFormatter={value => `${Math.round(Number(value) / 1000)}s`} /><YAxis /><Tooltip /><Line type="linear" dataKey="raw" name="Production source" stroke="var(--text-muted)" dot={false} connectNulls isAnimationActive={false} />{existingProfile && <Line type="stepAfter" dataKey="oldTarget" name="Current saved profile" stroke="var(--warning)" dot={false} connectNulls isAnimationActive={false} />}<Line type="stepAfter" dataKey="target" name="Candidate runtime target" stroke="var(--accent)" dot={false} connectNulls isAnimationActive={false} /></ComposedChart></ResponsiveContainer></div> : null}
      {derived && <div className="scenario-shape-guidance" role="status"><strong>{derived.traffic_profile.mappings.reduce((sum, mapping) => sum + mapping.points.length, 0).toLocaleString()} full-resolution points</strong><p>{derived.traffic_profile.runtime_target.toUpperCase()} profile · {derived.traffic_profile.target_duration} · peak {Math.max(...derived.traffic_profile.mappings.map(mapping => mapping.peak_target))}</p>{existingProfile && <p className="secondary">{candidateMatchesExisting ? 'Applied: this candidate is the current scenario profile.' : `Current: ${existingProfile.mappings.reduce((sum, mapping) => sum + mapping.points.length, 0).toLocaleString()} points, ${existingProfile.target_duration}, peak ${Math.max(...existingProfile.mappings.map(mapping => mapping.peak_target))}. Candidate remains unapplied until you confirm below.`}</p>}{derived.warnings?.map(warning => <p key={warning} className="secondary">{warning}</p>)}<button type="button" className="btn btn-primary" disabled={candidateMatchesExisting} onClick={() => onApply(derived)}>{candidateMatchesExisting ? 'Production profile applied' : 'Apply production profile'}</button></div>}
      {error && <div className="form-error" role="alert">{error}</div>}
    </div>
  )
}

interface GroupFileUploadCardProps {
  groupName: string
  projectId?: string
  onReady: (selection: GroupUploadSelection | null) => void
}

function GroupFileUploadCard({ groupName, projectId, onReady }: GroupFileUploadCardProps) {
  const api = useApi()
  const [file, setFile] = useState<File | null>(null)
  const [analysis, setAnalysis] = useState<TrafficProfileAnalyzeResponse | null>(null)
  const [sheet, setSheet] = useState('')
  const [timestampColumn, setTimestampColumn] = useState('')
  const [valueColumns, setValueColumns] = useState<string[]>([])
  const [seriesColumn, setSeriesColumn] = useState('')
  const [timezone, setTimezone] = useState('UTC')
  const [seriesKey, setSeriesKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const selectedSheet = analysis?.sheets?.find(candidate => candidate.name === sheet) || analysis?.sheets?.[0]
  const ready = Boolean(seriesKey && analysis?.series?.some(item => item.series.key === seriesKey))

  const clearSelection = () => {
    setAnalysis(null)
    setSheet('')
    setTimestampColumn('')
    setValueColumns([])
    setSeriesColumn('')
    setSeriesKey('')
    setError('')
    onReady(null)
  }

  const analyze = async () => {
    if (!file) return
    const form = new FormData()
    if (projectId) form.set('project_id', projectId)
    form.set('file', file)
    if (sheet) form.set('sheet', sheet)
    if (timestampColumn) form.set('timestamp_column', timestampColumn)
    valueColumns.forEach(column => form.append('value_column', column))
    if (seriesColumn) form.set('series_column', seriesColumn)
    if (timezone) form.set('timezone', timezone)
    setBusy(true)
    setError('')
    onReady(null)
    try {
      const result = await api.analyzeTrafficProfile(form)
      setAnalysis(result)
      const firstSheet = result.sheets?.find(candidate => candidate.name === sheet) || result.sheets?.[0]
      if (firstSheet && !sheet) setSheet(firstSheet.name)
      const firstSeries = result.series?.[0]?.series.key || ''
      setSeriesKey(firstSeries)
      if (firstSeries) onReady({ analysis: result, seriesKey: firstSeries })
    } catch (cause) {
      setError(getErrorMessage(cause, `Unable to analyze the workload for ${groupName}.`))
    } finally {
      setBusy(false)
    }
  }

  const invalidateColumns = () => {
    setSeriesKey('')
    onReady(null)
  }

  return <section className="traffic-profile-group-upload" aria-label={`${groupName} workload file`}>
    <div className="traffic-profile-group-upload-header">
      <div><span className="traffic-profile-section-label">Scenario group</span><strong>{groupName}</strong></div>
      {ready ? <span className="status-badge success">Ready</span> : <span className="status-badge">File required</span>}
    </div>
    <ImportDropzone accept=".csv,.tsv,.txt,.xlsx" file={file} onFile={next => { setFile(next); clearSelection() }} dropHint={`Drop CSV/XLSX for ${groupName}`} onError={setError} disabled={busy} />
    {analysis?.sheets?.length ? <div className="settings-grid compact-grid">
      <label><span>{groupName} worksheet</span><select aria-label={`${groupName} worksheet`} value={sheet} onChange={event => { setSheet(event.target.value); setTimestampColumn(''); setValueColumns([]); setSeriesColumn(''); invalidateColumns() }}>{analysis.sheets.map(item => <option key={item.name}>{item.name}</option>)}</select></label>
      <label><span>{groupName} timestamp column</span><select aria-label={`${groupName} timestamp column`} value={timestampColumn} onChange={event => { setTimestampColumn(event.target.value); invalidateColumns() }}><option value="">Select…</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} value columns</span><select aria-label={`${groupName} value columns`} multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={valueColumns} onChange={event => { setValueColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateColumns() }}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} optional series column</span><select aria-label={`${groupName} optional series column`} value={seriesColumn} onChange={event => { setSeriesColumn(event.target.value); invalidateColumns() }}><option value="">None</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} timezone</span><input aria-label={`${groupName} timezone`} value={timezone} onChange={event => { setTimezone(event.target.value); invalidateColumns() }} placeholder="America/New_York" /></label>
    </div> : null}
    <button type="button" className="btn btn-secondary" onClick={() => void analyze()} disabled={busy || !file}>{busy ? 'Working…' : analysis?.series?.length ? `Re-analyze ${groupName}` : `Analyze ${groupName}`}</button>
    {analysis?.series?.length ? <label className="traffic-profile-group-series"><span>Workload series for {groupName}</span><select aria-label={`Workload series for ${groupName}`} value={seriesKey} onChange={event => { const next = event.target.value; setSeriesKey(next); onReady(next ? { analysis, seriesKey: next } : null) }}><option value="">Select…</option>{analysis.series.map(item => <option key={item.series.key} value={item.series.key}>{item.series.name} · {item.stats.count.toLocaleString()} points · peak {item.stats.max}</option>)}</select></label> : null}
    {error && <div className="form-error" role="alert">{error}</div>}
  </section>
}

interface QueryRangeFieldsProps {
  mode: 'relative' | 'absolute'
  setMode: (value: 'relative' | 'absolute') => void
  lookback: string
  setLookback: (value: string) => void
  start: string
  setStart: (value: string) => void
  end: string
  setEnd: (value: string) => void
  stepSeconds: number
  setStepSeconds: (value: number) => void
}

function QueryRangeFields({ mode, setMode, lookback, setLookback, start, setStart, end, setEnd, stepSeconds, setStepSeconds }: QueryRangeFieldsProps) {
  return <>
    <label><span>Range</span><select value={mode} onChange={event => setMode(event.target.value as typeof mode)}><option value="relative">Relative</option><option value="absolute">Absolute</option></select></label>
    {mode === 'relative' ? <label><span>Lookback</span><input value={lookback} onChange={event => setLookback(event.target.value)} /></label> : <><label><span>Start</span><input type="datetime-local" value={start} onChange={event => setStart(event.target.value)} /></label><label><span>End</span><input type="datetime-local" value={end} onChange={event => setEnd(event.target.value)} /></label></>}
    <label><span>Step seconds</span><input type="number" min={1} value={stepSeconds} onChange={event => setStepSeconds(Number(event.target.value))} /></label>
  </>
}
