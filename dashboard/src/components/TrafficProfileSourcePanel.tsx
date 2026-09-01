import { useEffect, useMemo, useRef, useState } from 'react'
import { CartesianGrid, ComposedChart, Legend, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type {
  TrafficProfileAnalyzeResponse,
  TrafficProfile,
  TrafficProfileDeriveRequest,
  TrafficProfileDeriveResponse,
  TrafficProfileGapPolicy,
  TrafficProfileSeries,
  TrafficProfileSource,
  TrafficProfileSourceEntry,
  TrafficProfileSourcesResponse,
  TrafficProfileValueSemantics,
} from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import type { ScenarioUpsertPayload } from './scenarioPayloadUtils'
import { formatTrafficProfileAxisTime, trafficProfileTargetAt, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'

interface Props {
  projectId?: string
  groupNames: string[]
  scenarioDraft: ScenarioUpsertPayload
  existingProfile?: TrafficProfile
  onApply: (result: TrafficProfileDeriveResponse) => void
}

const previewLimit = 1000
const allGroupsMapping = '__all_groups__'
const profileLineColors = ['#2563eb', '#16a34a', '#f59e0b', '#dc2626', '#7c3aed', '#0891b2', '#db2777', '#65a30d']
const fallbackTimezones = ['UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Asia/Tokyo', 'Asia/Kolkata', 'Australia/Sydney']

const browserTimezones = (() => {
  const extendedIntl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  try {
    return extendedIntl.supportedValuesOf?.('timeZone') || fallbackTimezones
  } catch {
    return fallbackTimezones
  }
})()

function timezoneOptions(current: string): string[] {
  return Array.from(new Set(['UTC', current, ...browserTimezones].filter(Boolean)))
}

function TimezoneSelect({ value, onChange, ariaLabel }: { value: string; onChange: (value: string) => void; ariaLabel?: string }) {
  return <select aria-label={ariaLabel} value={value} onChange={event => onChange(event.target.value)}>{timezoneOptions(value).map(zone => <option key={zone} value={zone}>{zone}</option>)}</select>
}

interface GroupUploadSelection {
  analysis: TrafficProfileAnalyzeResponse
  seriesKey: string
  art?: TrafficProfileDeriveRequest['mappings'][number]['art']
}

type ARTUnit = 'milliseconds' | 'seconds'
type GroupARTSelection = { basis: 'none' | 'source_series' | 'operator_peak'; seriesKey?: string; value?: number; unit: ARTUnit }

function savedARTSelection(profile: TrafficProfile | undefined, groupName: string): GroupARTSelection {
  const art = profile?.mappings.find(mapping => mapping.group_name.trim().toLowerCase() === groupName.trim().toLowerCase())?.art
  if (!art) return { basis: 'none', unit: 'milliseconds' }
  const unit: ARTUnit = art.source_unit === 'seconds' ? 'seconds' : 'milliseconds'
  if (art.basis === 'source_series') return { basis: 'source_series', seriesKey: art.series_key, unit }
  return { basis: 'operator_peak', value: unit === 'seconds' ? art.peak_art_ms / 1000 : art.peak_art_ms, unit }
}

function samplePreservingExtrema<T>(values: T[], limit = previewLimit, valueOf: (value: T) => number): T[] {
  if (values.length <= limit) return values
  if (limit <= 1) return values.slice(0, 1)
  if (limit === 2) return [values[0], values[values.length - 1]]

  const interior = values.slice(1, -1)
  const bucketCount = Math.max(1, Math.floor((limit - 2) / 2))
  const sampled: Array<{ index: number; value: T }> = [
    { index: 0, value: values[0] },
  ]
  for (let bucket = 0; bucket < bucketCount; bucket += 1) {
    const start = Math.floor((bucket * interior.length) / bucketCount)
    const end = Math.floor(((bucket + 1) * interior.length) / bucketCount)
    if (start >= end) continue
    let minimumIndex = start
    let maximumIndex = start
    for (let index = start + 1; index < end; index += 1) {
      if (valueOf(interior[index]) < valueOf(interior[minimumIndex])) minimumIndex = index
      if (valueOf(interior[index]) > valueOf(interior[maximumIndex])) maximumIndex = index
    }
    Array.from(new Set([minimumIndex, maximumIndex])).sort((left, right) => left - right).forEach(index => {
      sampled.push({ index: index + 1, value: interior[index] })
    })
  }
  sampled.push({ index: values.length - 1, value: values[values.length - 1] })
  return sampled.sort((left, right) => left.index - right.index).slice(0, limit).map(item => item.value)
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
  const [fileAnalysisDirty, setFileAnalysisDirty] = useState(false)
  const fileConfigRevisionRef = useRef(0)
  const fileRequestRef = useRef(0)
  const [sheet, setSheet] = useState(savedSource?.file?.sheet || '')
  const [timestampColumn, setTimestampColumn] = useState(savedSource?.file?.timestamp_column || '')
  const [timestampMode, setTimestampMode] = useState<'absolute' | 'relative'>(savedSource?.file?.timestamp_mode === 'relative' ? 'relative' : 'absolute')
  const [relativeStartTime, setRelativeStartTime] = useState(savedSource?.file?.relative_start_time || '00:00')
  const [valueColumns, setValueColumns] = useState<string[]>(savedSource?.file?.value_columns || [])
  const [artColumns, setARTColumns] = useState<string[]>(savedSource?.file?.art_columns || [])
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
  const [peakScope, setPeakScope] = useState<'per_group' | 'overall'>(() => existingProfile ? existingProfile.window?.peak_scope || 'per_group' : 'overall')
  const [peakDuration, setPeakDuration] = useState(existingProfile?.window?.duration || '1h')
  const [windowStart, setWindowStart] = useState(localDateTimeValue(existingProfile?.window?.start))
  const [windowEnd, setWindowEnd] = useState(localDateTimeValue(existingProfile?.window?.end))
  const [windowStartOffset, setWindowStartOffset] = useState(existingProfile?.window?.start_offset || '0s')
  const [windowEndOffset, setWindowEndOffset] = useState(existingProfile?.window?.end_offset || existingProfile?.source_duration || '1h')
  const [targetDuration, setTargetDuration] = useState(existingProfile?.target_duration || '')
  const [amplitudeMode, setAmplitudeMode] = useState<'factor' | 'peak'>(existingProfile?.amplitude?.mode || 'factor')
  const [amplitude, setAmplitude] = useState(existingProfile?.amplitude?.value ?? 1)
  const [mappings, setMappings] = useState<Record<string, string>>(() => Object.fromEntries((existingProfile?.mappings || []).map(mapping => [mapping.series_key, mapping.group_name])))
  const [groupART, setGroupART] = useState<Record<string, GroupARTSelection>>(() => Object.fromEntries(groupNames.map(name => [name, savedARTSelection(existingProfile, name)])))
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
  }, [amplitude, amplitudeMode, assignmentMode, gapPolicy, groupART, groupUploads, mappings, peakDuration, semantics, targetDuration, windowEnd, windowEndOffset, windowMode, windowStart, windowStartOffset])

  const selectedSheet = analysis?.sheets?.find(candidate => candidate.name === sheet) || analysis?.sheets?.[0]
  const usesRelativeWindow = assignmentMode === 'per_group'
    ? Object.values(groupUploads).some(upload => upload.analysis.source.file?.timestamp_mode === 'relative')
    : sourceKind === 'file' && timestampMode === 'relative'
  const invalidateFileAnalysis = () => {
    fileConfigRevisionRef.current += 1
    setFileAnalysisDirty(true)
    setDerived(null)
  }

  const analyzeFile = async () => {
    if (!file) return
    const requestID = ++fileRequestRef.current
    const configRevision = fileConfigRevisionRef.current
    const form = new FormData()
    // project_id must precede the file part: the server peeks only the leading
    // scalar fields and stops at the first file, so a trailing project_id is
    // never seen and the request is rejected for project-scoped users.
    if (projectId) form.set('project_id', projectId)
    form.set('file', file)
    if (sheet) form.set('sheet', sheet)
    if (timestampColumn) form.set('timestamp_column', timestampColumn)
    form.set('timestamp_mode', timestampMode)
    if (timestampMode === 'relative') form.set('relative_start_time', relativeStartTime)
    valueColumns.forEach(column => form.append('value_column', column))
    artColumns.forEach(column => form.append('art_column', column))
    if (seriesColumn) form.set('series_column', seriesColumn)
    if (timezone) form.set('timezone', timezone)
    let result: TrafficProfileAnalyzeResponse
    try {
      result = await api.analyzeTrafficProfile(form)
    } catch (cause) {
      if (requestID !== fileRequestRef.current || configRevision !== fileConfigRevisionRef.current) return
      throw cause
    }
    if (requestID !== fileRequestRef.current || configRevision !== fileConfigRevisionRef.current) return
    setAnalysis(result)
    setFileAnalysisDirty(false)
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
    let artSeries: TrafficProfileSeries[]
    let selectedMappings: TrafficProfileDeriveRequest['mappings']
    if (assignmentMode === 'per_group') {
      const selections = groupNames.flatMap((groupName, position) => {
        const upload = groupUploads[groupName]
        const selected = upload?.analysis.series?.find(item => item.series.key === upload.seriesKey)?.series
        if (!upload || !selected) return []
        const keyedSeries = { ...selected, key: `group-${position + 1}:${selected.key}` }
        const selectedART = upload.art?.basis === 'source_series'
          ? upload.analysis.art_series?.find(item => item.series.key === upload.art?.series_key)?.series
          : undefined
        const keyedART = selectedART ? { ...selectedART, key: `group-${position + 1}:art:${selectedART.key}` } : undefined
        const art = upload.art?.basis === 'source_series' && keyedART ? { ...upload.art, series_key: keyedART.key } : upload.art
        return [{ groupName, source: upload.analysis.source, series: keyedSeries, artSeries: keyedART, art }]
      })
      if (selections.length !== groupNames.length) { setError('Analyze and select a workload series for every scenario group.'); return }
      source = { kind: 'multiple', name: 'Per-group production traffic files', sources: selections.map(item => item.source) }
      series = selections.map(item => item.series)
      artSeries = selections.flatMap(item => item.artSeries ? [item.artSeries] : [])
      selectedMappings = selections.map(item => ({ series_key: item.series.key, group_name: item.groupName, source: item.source, ...(item.art ? { art: item.art } : {}) }))
    } else {
      source = analysis?.source || querySource()
      series = (analysis?.series || []).map(item => item.series)
      artSeries = (analysis?.art_series || []).map(item => item.series)
      selectedMappings = series.flatMap(item => {
        const target = mappings[item.key]
        if (!target) return []
        const mapped = target === allGroupsMapping ? groupNames : [target]
        return mapped.map(groupName => {
          const selection = groupART[groupName]
          const art = selection?.basis === 'source_series' && selection.seriesKey
            ? { basis: 'source_series' as const, series_key: selection.seriesKey, unit: selection.unit }
            : selection?.basis === 'operator_peak'
              ? { basis: 'operator_peak' as const, value: selection.value, unit: selection.unit }
              : undefined
          return { series_key: item.key, group_name: groupName, ...(art ? { art } : {}) }
        })
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
        ...(artSeries.length ? { art_series: artSeries } : {}),
        mappings: selectedMappings,
        value_semantics: semantics,
        gap_policy: gapPolicy,
        window: windowMode === 'peak'
          ? { mode: 'peak', peak_scope: peakScope, duration: peakDuration }
          : windowMode === 'explicit'
            ? usesRelativeWindow
              ? { mode: 'explicit', start_offset: windowStartOffset, end_offset: windowEndOffset }
              : { mode: 'explicit', start: requiredISOString(windowStart, 'Window start'), end: requiredISOString(windowEnd, 'Window end') }
            : { mode: 'full' },
        ...(targetDuration ? { target_duration: targetDuration } : {}),
        amplitude: { mode: amplitudeMode, value: amplitude },
        scenario_draft: draft,
      }
      setDerived(await api.deriveTrafficProfile(payload))
    } catch (cause) { setError(getErrorMessage(cause, 'Traffic-profile derivation failed.')) } finally { setBusy(false) }
  }

  const chartModel = useMemo(() => {
    type ChartRow = { elapsed: number } & Record<string, number>
    type ChartLine = { key: string; name: string; color: string; kind: 'source' | 'candidate' | 'saved' | 'total' }
    type ChartSelection = { key: string; name: string; start: number; end: number; color: string }
    const candidateMappings = derived?.preview?.length ? derived.preview : derived?.traffic_profile.mappings || []
    const savedMappings = existingProfile?.mappings || []
    const rawSeries = assignmentMode === 'per_group'
      ? groupNames.flatMap(groupName => {
        const upload = groupUploads[groupName]
        const series = upload?.analysis.series?.find(item => item.series.key === upload.seriesKey)?.series
        return series ? [{ groupName, series }] : []
      })
      : (analysis?.series || []).flatMap(item => {
        const candidateGroups = candidateMappings
          .filter(mapping => mapping.series_key === item.series.key)
          .map(mapping => mapping.group_name)
        const configuredTarget = mappings[item.series.key]
        const groupName = candidateGroups.length > 1
          ? 'All groups'
          : candidateGroups[0] || (configuredTarget === allGroupsMapping ? 'All groups' : configuredTarget)
        return groupName ? [{ groupName, series: item.series }] : []
      })

    if (!rawSeries.length && !candidateMappings.length && !savedMappings.length) return { data: [] as ChartRow[], lines: [] as ChartLine[], selections: [] as ChartSelection[], fullSpan: 1 }

    const candidateDuration = durationMilliseconds(derived?.traffic_profile.target_duration)
    const savedDuration = durationMilliseconds(existingProfile?.target_duration)
    const rawDuration = rawSeries.reduce((maximum, item) => {
      const start = Date.parse(item.series.points[0]?.timestamp || '')
      const end = Date.parse(item.series.points[item.series.points.length - 1]?.timestamp || '')
      return Math.max(maximum, Number.isFinite(start) && Number.isFinite(end) ? Math.max(0, end - start) : 0)
    }, 0)
    const lineCount = rawSeries.length + candidateMappings.length + savedMappings.length + (candidateMappings.length > 1 ? 1 : 0) + (savedMappings.length > 1 ? 1 : 0)
    const pointsPerLine = Math.max(25, Math.floor(previewLimit / Math.max(1, lineCount)))
    const rows = new Map<number, ChartRow>()
    const lines: ChartLine[] = []
    const selections: ChartSelection[] = []
    const mergeValue = (elapsed: number, key: string, value: number) => {
      const normalizedElapsed = Math.round(elapsed)
      rows.set(normalizedElapsed, { ...rows.get(normalizedElapsed), elapsed: normalizedElapsed, [key]: value })
    }
    const groupColor = (groupName: string, fallbackIndex: number) => {
      const groupIndex = groupNames.findIndex(name => name.trim().toLowerCase() === groupName.trim().toLowerCase())
      return profileLineColors[(groupIndex >= 0 ? groupIndex : fallbackIndex) % profileLineColors.length]
    }
    const rawSeriesForMapping = (mapping: TrafficProfile['mappings'][number]) => rawSeries.find(item => item.series.key === mapping.series_key)
      || rawSeries.find(item => item.groupName.trim().toLowerCase() === mapping.group_name.trim().toLowerCase())
    const projectionForMapping = (mapping: TrafficProfile['mappings'][number], runtimeDuration: number) => {
      const raw = rawSeriesForMapping(mapping)
      const rawStart = Date.parse(raw?.series.points[0]?.timestamp || '')
      const rawEnd = Date.parse(raw?.series.points[raw.series.points.length - 1]?.timestamp || '')
      const selectedStart = Date.parse(mapping.source_start || '')
      const selectedEnd = Date.parse(mapping.source_end || '')
      const hasResolvedWindow = Number.isFinite(rawStart) && Number.isFinite(selectedStart) && Number.isFinite(selectedEnd) && selectedEnd > selectedStart
      const start = hasResolvedWindow ? Math.max(0, selectedStart - rawStart) : 0
      const end = hasResolvedWindow
        ? Math.max(start + 1, selectedEnd - rawStart)
        : Number.isFinite(rawStart) && Number.isFinite(rawEnd) ? Math.max(1, rawEnd - rawStart) : Math.max(1, runtimeDuration)
      return { start, end, duration: Math.max(1, runtimeDuration), raw }
    }
    const projectedOffset = (offset: number, projection: ReturnType<typeof projectionForMapping>) => projection.start + (offset / projection.duration) * (projection.end - projection.start)
    const addProfileLines = (profileMappings: TrafficProfile['mappings'], runtimeDuration: number, kind: 'candidate' | 'saved', prefix: string) => {
      const projections = profileMappings.map(mapping => projectionForMapping(mapping, runtimeDuration))
      profileMappings.forEach((mapping, index) => {
        const key = `${prefix}_${index}`
        const projection = projections[index]
        samplePreservingExtrema(mapping.points, pointsPerLine, point => point.target).forEach(point => mergeValue(projectedOffset(point.offset_ms, projection), key, point.target))
        mergeValue(projection.end, key, 0)
        lines.push({ key, name: `${mapping.group_name} ${kind === 'candidate' ? 'candidate' : 'saved profile'}`, color: groupColor(mapping.group_name, index), kind })
      })
      if (profileMappings.length > 1) {
        const key = `${prefix}_total`
        const offsets = Array.from(new Set(profileMappings.flatMap((mapping, index) => [
          ...mapping.points.map(point => projectedOffset(point.offset_ms, projections[index])),
          projections[index].end,
        ]))).sort((left, right) => left - right)
        const totalAt = (sourceOffset: number) => profileMappings.reduce((total, mapping, index) => {
          const projection = projections[index]
          if (sourceOffset < projection.start || sourceOffset >= projection.end) return total
          const runtimeOffset = ((sourceOffset - projection.start) / (projection.end - projection.start)) * projection.duration
          return total + trafficProfileTargetAt(mapping, runtimeOffset / 1000)
        }, 0)
        samplePreservingExtrema(offsets, pointsPerLine, totalAt).forEach(offset => mergeValue(offset, key, totalAt(offset)))
        lines.push({ key, name: `Total ${kind}`, color: kind === 'candidate' ? 'var(--text-primary)' : 'var(--warning)', kind: kind === 'candidate' ? 'total' : 'saved' })
      }
      return projections
    }

    rawSeries.forEach((item, index) => {
      const key = `source_${index}`
      const start = Date.parse(item.series.points[0]?.timestamp || '')
      samplePreservingExtrema(item.series.points, pointsPerLine, point => point.value).forEach(point => {
        const timestamp = Date.parse(point.timestamp)
        if (Number.isFinite(timestamp)) mergeValue(timestamp - start, key, point.value)
      })
      lines.push({ key, name: `${item.groupName} production source`, color: groupColor(item.groupName, index), kind: 'source' })
    })

    const candidateProjections = addProfileLines(candidateMappings, candidateDuration, 'candidate', 'candidate')
    const savedProjections = addProfileLines(savedMappings, savedDuration, 'saved', 'saved')
    if (derived && derived.traffic_profile.window?.mode && derived.traffic_profile.window.mode !== 'full') {
      const selectionByRange = new Map<string, ChartSelection>()
      candidateMappings.forEach((mapping, index) => {
        const projection = candidateProjections[index]
        if (!projection?.raw) return
        const rangeKey = `${Math.round(projection.start)}:${Math.round(projection.end)}`
        const existing = selectionByRange.get(rangeKey)
        if (existing) {
          existing.name = 'Selected window (all mapped groups)'
          return
        }
        selectionByRange.set(rangeKey, { key: `selection_${index}`, name: `${mapping.group_name} selected window`, start: projection.start, end: projection.end, color: groupColor(mapping.group_name, index) })
      })
      selections.push(...selectionByRange.values())
    }

    const projectionEnd = [...candidateProjections, ...savedProjections].reduce((maximum, projection) => Math.max(maximum, projection.end), 0)
    return { data: Array.from(rows.values()).sort((left, right) => left.elapsed - right.elapsed), lines, selections, fullSpan: Math.max(1, rawDuration, projectionEnd) }
  }, [analysis, assignmentMode, derived, existingProfile, groupNames, groupUploads, mappings])
  const derivedTpsStatistics = useMemo(() => {
    if (derived?.traffic_profile.runtime_target !== 'tps') return undefined
    return trafficProfileTpsStatistics(
      derived.traffic_profile.mappings,
      durationMilliseconds(derived.traffic_profile.target_duration) / 1000,
    )
  }, [derived])
  const applyCandidate = () => {
    if (!derived) return
    const sizing = derived.traffic_profile.sizing
    if (sizing?.complete) {
      const currentMin = scenarioDraft.goal_config?.min_vusers
      const currentMax = scenarioDraft.goal_config?.max_vusers
      const message = `Apply this profile and replace goal guardrails${currentMin !== undefined || currentMax !== undefined ? ` (${currentMin ?? 'unset'}-${currentMax ?? 'unset'})` : ''} with the ART-based recommendation ${sizing.suggested_min_vusers}-${sizing.suggested_max_vusers} VUsers?`
      if (!window.confirm(message)) return
    }
    onApply(derived)
  }
  const sourceUsesOwnAxis = chartModel.lines.some(line => line.kind === 'source')
    && chartModel.lines.some(line => line.kind !== 'source')
    && (semantics === 'transactions_per_bucket' || semantics === 'cumulative_counter')
  const primaryAxisLabel = chartModel.lines.some(line => line.kind !== 'source')
    ? (derived?.traffic_profile.runtime_target || existingProfile?.runtime_target) === 'vusers' ? 'VUsers' : 'TPS'
    : semantics === 'cumulative_counter' ? 'Source counter' : semantics === 'transactions_per_bucket' ? 'Transactions / bucket' : semantics === 'vusers' ? 'VUsers' : 'TPS'
  const formatTps = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 2 })

  const perGroupReady = groupNames.length > 1 && groupNames.every(name => {
    const upload = groupUploads[name]
    return Boolean(upload?.analysis.series?.some(item => item.series.key === upload.seriesKey))
  })
  const hasAnalyzedData = assignmentMode === 'per_group'
    ? perGroupReady
    : Boolean(analysis?.series?.length) && !(sourceKind === 'file' && fileAnalysisDirty)

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
          {groupNames.map(groupName => <GroupFileUploadCard key={groupName} groupName={groupName} projectId={projectId} savedFile={existingProfile?.mappings.find(mapping => mapping.group_name.trim().toLowerCase() === groupName.trim().toLowerCase())?.source?.file} savedART={savedARTSelection(existingProfile, groupName)} onReady={selection => setGroupUploads(current => {
            const next = { ...current }
            if (selection) next[groupName] = selection
            else delete next[groupName]
            return next
          })} />)}
        </div>
      ) : <>
        <div className="scenario-source-toggle" aria-label="Traffic profile source">
          {(['file', 'prometheus', 'opensearch'] as const).map(kind => (
            <button key={kind} type="button" className={`mode-chip ${sourceKind === kind ? 'active' : ''}`} disabled={!sourceAvailable(kind)} onClick={() => { fileConfigRevisionRef.current += 1; setSourceKind(kind); setAnalysis(null); setFileAnalysisDirty(false); setDerived(null) }}>{kind === 'file' ? 'CSV / XLSX' : kind === 'prometheus' ? 'Prometheus' : 'OpenSearch'}</button>
          ))}
        </div>

        {sourceKind === 'file' ? (
          <>
            <ImportDropzone accept=".csv,.tsv,.txt,.xlsx" file={file} onFile={next => {
              fileConfigRevisionRef.current += 1
              setFile(next)
              setAnalysis(null)
              setFileAnalysisDirty(false)
              setDerived(null)
              const refreshesSavedFile = Boolean(next && savedSource?.file && next.name === savedSource.file.filename)
              setSheet(refreshesSavedFile ? savedSource?.file?.sheet || '' : '')
              setTimestampColumn(refreshesSavedFile ? savedSource?.file?.timestamp_column || '' : '')
              setValueColumns(refreshesSavedFile ? savedSource?.file?.value_columns || [] : [])
              setARTColumns(refreshesSavedFile ? savedSource?.file?.art_columns || [] : [])
              setSeriesColumn(refreshesSavedFile ? savedSource?.file?.series_column || '' : '')
              setError('')
            }} dropHint="Drop a production traffic CSV or XLSX file" onError={setError} disabled={busy} />
            {analysis?.sheets?.length ? <div className="settings-grid compact-grid">
              <label><span>Worksheet</span><select value={sheet} onChange={event => { setSheet(event.target.value); setTimestampColumn(''); setValueColumns([]); setARTColumns([]); setSeriesColumn(''); invalidateFileAnalysis() }}>{analysis.sheets.map(item => <option key={item.name}>{item.name}</option>)}</select></label>
              <label><span>Timestamp column</span><select value={timestampColumn} onChange={event => { setTimestampColumn(event.target.value); invalidateFileAnalysis() }}><option value="">Select…</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Timestamp values</span><select aria-label="Timestamp values" value={timestampMode} onChange={event => { setTimestampMode(event.target.value as typeof timestampMode); invalidateFileAnalysis() }}><option value="absolute">Absolute date/time</option><option value="relative">Relative elapsed time</option></select></label>
              {timestampMode === 'relative' && <label><span>Relative 0:00 starts at</span><input aria-label="Relative 0:00 starts at" type="time" step={1} value={relativeStartTime} onChange={event => { setRelativeStartTime(event.target.value); invalidateFileAnalysis() }} /></label>}
              <label><span>Value columns (select one or more)</span><select multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={valueColumns} onChange={event => { setValueColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateFileAnalysis() }}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Optional ART columns</span><select aria-label="Optional ART columns" multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={artColumns} onChange={event => { setARTColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateFileAnalysis() }}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Optional series column</span><select value={seriesColumn} onChange={event => { setSeriesColumn(event.target.value); invalidateFileAnalysis() }}><option value="">None</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
              <label><span>Timezone</span><TimezoneSelect ariaLabel="Timezone" value={timezone} onChange={value => { setTimezone(value); invalidateFileAnalysis() }} /></label>
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
          {windowMode === 'peak' && <><label><span>Peak selection</span><select aria-label="Peak selection" value={peakScope} onChange={event => setPeakScope(event.target.value as typeof peakScope)}><option value="overall">Overall across all groups</option><option value="per_group">Each group separately</option></select></label><label><span>Peak window duration</span><input value={peakDuration} onChange={event => setPeakDuration(event.target.value)} /></label></>}
          {windowMode === 'explicit' && (usesRelativeWindow
            ? <><label><span>Window start offset</span><input aria-label="Window start offset" value={windowStartOffset} onChange={event => setWindowStartOffset(event.target.value)} placeholder="0s" /></label><label><span>Window end offset</span><input aria-label="Window end offset" value={windowEndOffset} onChange={event => setWindowEndOffset(event.target.value)} placeholder="1h" /></label></>
            : <><label><span>Window start</span><input type="datetime-local" value={windowStart} onChange={event => setWindowStart(event.target.value)} /></label><label><span>Window end</span><input type="datetime-local" value={windowEnd} onChange={event => setWindowEnd(event.target.value)} /></label></>)}
          <label><span>Test duration (blank = 1:1)</span><input value={targetDuration} onChange={event => setTargetDuration(event.target.value)} placeholder="8h" /></label>
          <label><span>Amplitude</span><select value={amplitudeMode} onChange={event => setAmplitudeMode(event.target.value as typeof amplitudeMode)}><option value="factor">Multiply by</option><option value="peak">Scale peak to</option></select><input type="number" min={0} step="any" value={amplitude} onChange={event => setAmplitude(Number(event.target.value))} /></label>
        </div>
        {assignmentMode === 'shared' && analysis?.series?.length ? <div className="data-table"><table><thead><tr><th>Series</th><th>Points</th><th>Cadence</th><th>Source peak</th><th>Apply to</th></tr></thead><tbody>{analysis.series.map(item => <tr key={item.series.key}><td>{item.series.name}</td><td>{item.stats.count.toLocaleString()}</td><td>{Math.round(item.stats.detected_bucket / 1e9)}s</td><td>{item.stats.max}</td><td><select aria-label={`Apply ${item.series.name} to`} value={mappings[item.series.key] || ''} onChange={event => setMappings(current => ({ ...current, [item.series.key]: event.target.value }))}><option value="">Ignore</option>{groupNames.length > 1 && <option value={allGroupsMapping}>All groups</option>}{groupNames.map(name => <option key={name}>{name}</option>)}</select></td></tr>)}</tbody></table></div> : null}
        {assignmentMode === 'shared' && semantics !== 'vusers' ? <div className="data-table"><table><thead><tr><th>Group</th><th>ART basis</th><th>ART value or series</th><th>Unit</th></tr></thead><tbody>{groupNames.map(groupName => {
          const selection = groupART[groupName] || { basis: 'none', unit: 'milliseconds' as const }
          return <tr key={groupName}><td>{groupName}</td><td><select aria-label={`ART basis for ${groupName}`} value={selection.basis} onChange={event => setGroupART(current => ({ ...current, [groupName]: { basis: event.target.value as GroupARTSelection['basis'], unit: current[groupName]?.unit || 'milliseconds' } }))}><option value="none">Not provided</option>{analysis?.art_series?.length ? <option value="source_series">File ART series</option> : null}<option value="operator_peak">Estimated peak ART</option></select></td><td>{selection.basis === 'source_series' ? <select aria-label={`ART series for ${groupName}`} value={selection.seriesKey || ''} onChange={event => setGroupART(current => ({ ...current, [groupName]: { ...selection, seriesKey: event.target.value } }))}><option value="">Select…</option>{analysis?.art_series?.map(item => <option key={item.series.key} value={item.series.key}>{item.series.name} · peak {item.stats.max}</option>)}</select> : selection.basis === 'operator_peak' ? <input aria-label={`Estimated peak ART for ${groupName}`} type="number" min={0.001} step="any" value={selection.value ?? ''} onChange={event => setGroupART(current => ({ ...current, [groupName]: { ...selection, value: Number(event.target.value) } }))} /> : <span className="secondary">Sizing remains incomplete</span>}</td><td>{selection.basis !== 'none' ? <select aria-label={`ART unit for ${groupName}`} value={selection.unit} onChange={event => setGroupART(current => ({ ...current, [groupName]: { ...selection, unit: event.target.value as ARTUnit } }))}><option value="milliseconds">Milliseconds</option><option value="seconds">Seconds</option></select> : '—'}</td></tr>
        })}</tbody></table></div> : null}
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void derive()}>Build profile preview</button>
      </> : null}

      {chartModel.data.length ? <>{derived && <p className="secondary">{chartModel.selections.length ? 'The graph shows the complete analyzed source range. Shaded areas mark the source window used to build the candidate profile.' : 'The graph shows the complete analyzed source range used to build the candidate profile.'}</p>}<div style={{ height: 280 }}><ResponsiveContainer width="100%" height="100%"><ComposedChart data={chartModel.data} margin={{ top: 8, right: sourceUsesOwnAxis ? 24 : 12, bottom: 4, left: 24 }}><CartesianGrid strokeDasharray="3 3" /><XAxis dataKey="elapsed" type="number" domain={[0, chartModel.fullSpan]} tickFormatter={value => formatTrafficProfileAxisTime(Number(value) / 1000)} /><YAxis yAxisId="target" label={{ value: primaryAxisLabel, angle: -90, position: 'left' }} />{sourceUsesOwnAxis && <YAxis yAxisId="source" orientation="right" label={{ value: semantics === 'cumulative_counter' ? 'Source counter' : 'Transactions / bucket', angle: 90, position: 'right' }} />}<Tooltip labelFormatter={value => `Elapsed: ${formatTrafficProfileAxisTime(Number(value) / 1000)}`} /><Legend />{chartModel.selections.map(selection => <ReferenceArea key={selection.key} yAxisId="target" x1={selection.start} x2={selection.end} fill={selection.color} fillOpacity={0.12} stroke={selection.color} strokeOpacity={0.45} label={{ value: selection.name, position: 'insideTop', fill: selection.color, fontSize: 10 }} />)}{chartModel.lines.map(line => <Line key={line.key} yAxisId={line.kind === 'source' && sourceUsesOwnAxis ? 'source' : 'target'} type={line.kind === 'source' ? 'linear' : 'stepAfter'} dataKey={line.key} name={line.name} stroke={line.color} strokeWidth={line.kind === 'total' ? 3 : line.kind === 'candidate' ? 2 : 1.5} strokeDasharray={line.kind === 'source' ? '2 4' : line.kind === 'saved' ? '8 4' : undefined} strokeOpacity={line.kind === 'source' ? 0.65 : 1} dot={false} connectNulls isAnimationActive={false} />)}</ComposedChart></ResponsiveContainer></div></> : null}
      {derived && <div className="scenario-shape-guidance" role="status">
        <strong>{derived.traffic_profile.mappings.reduce((sum, mapping) => sum + mapping.points.length, 0).toLocaleString()} full-resolution points</strong>
        <p>{derived.traffic_profile.runtime_target.toUpperCase()} profile · {derived.traffic_profile.target_duration}{derivedTpsStatistics ? ` · minimum ${formatTps(derivedTpsStatistics.minimum)} TPS · average ${formatTps(derivedTpsStatistics.average)} TPS · peak ${formatTps(derivedTpsStatistics.maximum)} TPS` : ` · peak ${Math.max(...derived.traffic_profile.mappings.map(mapping => mapping.peak_target))}`}</p>
        {derived.traffic_profile.runtime_target === 'tps' && <div className="data-table"><table><thead><tr><th>Group</th><th>Candidate ART basis</th><th>Peak ART</th><th>Estimated peak VUsers</th>{existingProfile && <th>Saved ART / estimate</th>}</tr></thead><tbody>{derived.traffic_profile.mappings.map(mapping => {
          const savedART = existingProfile?.mappings.find(saved => saved.group_name.trim().toLowerCase() === mapping.group_name.trim().toLowerCase())?.art
          return <tr key={mapping.group_name}><td>{mapping.group_name}</td><td>{mapping.art?.basis === 'source_series' ? mapping.art.fallback_to_peak ? 'File series (peak fallback)' : 'File series' : mapping.art?.basis === 'operator_peak' ? 'Operator estimate' : 'Not provided'}</td><td>{mapping.art ? `${formatTps(mapping.art.peak_art_ms)} ms` : '—'}</td><td>{mapping.art ? mapping.art.estimated_peak_vusers.toLocaleString() : '—'}</td>{existingProfile && <td>{savedART ? `${savedART.basis === 'source_series' ? savedART.fallback_to_peak ? 'File series (peak fallback)' : 'File series' : 'Operator estimate'} · ${formatTps(savedART.peak_art_ms)} ms · ${savedART.estimated_peak_vusers.toLocaleString()} VUsers` : 'Not provided'}</td>}</tr>
        })}</tbody></table></div>}
        {derived.traffic_profile.sizing?.complete ? <>
          <p><strong>Estimated peak VUsers: {(derived.traffic_profile.sizing.estimated_peak_vusers ?? 0).toLocaleString()}</strong> ({(derived.traffic_profile.sizing.profile_peak_vusers ?? 0).toLocaleString()} profile-managed + {(derived.traffic_profile.sizing.static_peak_vusers ?? 0).toLocaleString()} static)</p>
          <p>Current guardrails: {scenarioDraft.goal_config?.min_vusers?.toLocaleString() ?? 'not set'}-{scenarioDraft.goal_config?.max_vusers?.toLocaleString() ?? 'not set'} VUsers · Suggested: {(derived.traffic_profile.sizing.suggested_min_vusers ?? 0).toLocaleString()}-{(derived.traffic_profile.sizing.suggested_max_vusers ?? 0).toLocaleString()} VUsers · {derived.traffic_profile.sizing.recommended_generators ? `${derived.traffic_profile.sizing.recommended_generators} generator(s) recommended` : 'generator recommendation requires load-model validation'}</p>
          {derived.load_model_validation?.capacity_advisory?.warnings?.map(warning => <p key={`capacity-${warning}`} className="secondary">Capacity: {warning}</p>)}
          <p className="secondary">Little&apos;s Law planning estimate only; actual VUser demand is controlled adaptively at runtime. Applying this candidate replaces the scenario guardrails after confirmation.</p>
        </> : derived.traffic_profile.runtime_target === 'tps' ? <p className="secondary">ART sizing is incomplete. Add file-backed or estimated peak ART for every mapped group to estimate VUsers and generator requirements.</p> : null}
        {existingProfile && <p className="secondary">{candidateMatchesExisting ? 'Applied: this candidate is the current scenario profile.' : `Current: ${existingProfile.mappings.reduce((sum, mapping) => sum + mapping.points.length, 0).toLocaleString()} points, ${existingProfile.target_duration}, peak ${Math.max(...existingProfile.mappings.map(mapping => mapping.peak_target))}${existingProfile.sizing?.complete ? `, estimated peak ${(existingProfile.sizing.estimated_peak_vusers ?? 0).toLocaleString()} VUsers, suggested bounds ${existingProfile.sizing.suggested_min_vusers ?? 0}-${existingProfile.sizing.suggested_max_vusers ?? 0}, ${existingProfile.sizing.recommended_generators || 'no'} generator recommendation` : ', ART sizing incomplete'}. Candidate remains unapplied until you confirm below.`}</p>}
        {derived.warnings?.map(warning => <p key={warning} className="secondary">{warning}</p>)}
        <button type="button" className="btn btn-primary" disabled={candidateMatchesExisting} onClick={applyCandidate}>{candidateMatchesExisting ? 'Production profile applied' : derived.traffic_profile.sizing?.complete ? 'Apply profile and ART guardrails' : 'Apply production profile'}</button>
      </div>}
      {error && <div className="form-error" role="alert">{error}</div>}
    </div>
  )
}

interface GroupFileUploadCardProps {
  groupName: string
  projectId?: string
  savedFile?: TrafficProfileSourceEntry['file']
  savedART: GroupARTSelection
  onReady: (selection: GroupUploadSelection | null) => void
}

function GroupFileUploadCard({ groupName, projectId, savedFile, savedART, onReady }: GroupFileUploadCardProps) {
  const api = useApi()
  const [file, setFile] = useState<File | null>(null)
  const [analysis, setAnalysis] = useState<TrafficProfileAnalyzeResponse | null>(null)
  const [analysisDirty, setAnalysisDirty] = useState(false)
  const configRevisionRef = useRef(0)
  const requestRef = useRef(0)
  const [sheet, setSheet] = useState(savedFile?.sheet || '')
  const [timestampColumn, setTimestampColumn] = useState(savedFile?.timestamp_column || '')
  const [timestampMode, setTimestampMode] = useState<'absolute' | 'relative'>(savedFile?.timestamp_mode === 'relative' ? 'relative' : 'absolute')
  const [relativeStartTime, setRelativeStartTime] = useState(savedFile?.relative_start_time || '00:00')
  const [valueColumns, setValueColumns] = useState<string[]>(savedFile?.value_columns || [])
  const [artColumns, setARTColumns] = useState<string[]>(savedFile?.art_columns || [])
  const [seriesColumn, setSeriesColumn] = useState(savedFile?.series_column || '')
  const [timezone, setTimezone] = useState(savedFile?.timezone || 'UTC')
  const [seriesKey, setSeriesKey] = useState('')
  const [artBasis, setARTBasis] = useState<GroupARTSelection['basis']>(savedART.basis)
  const [artSeriesKey, setARTSeriesKey] = useState(savedART.seriesKey || '')
  const [artValue, setARTValue] = useState<number | undefined>(savedART.value)
  const [artUnit, setARTUnit] = useState<ARTUnit>(savedART.unit)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const selectedSheet = analysis?.sheets?.find(candidate => candidate.name === sheet) || analysis?.sheets?.[0]
  const ready = Boolean(!analysisDirty && seriesKey && analysis?.series?.some(item => item.series.key === seriesKey))

  const clearSelection = (resetConfiguration = true) => {
    configRevisionRef.current += 1
    setAnalysis(null)
    setAnalysisDirty(false)
    if (resetConfiguration) {
      setSheet('')
      setTimestampColumn('')
      setValueColumns([])
      setARTColumns([])
      setSeriesColumn('')
    }
    setSeriesKey('')
    setARTSeriesKey('')
    setError('')
    onReady(null)
  }

  const artRequest = (basis = artBasis, selectedSeries = artSeriesKey, value = artValue, unit = artUnit): GroupUploadSelection['art'] => basis === 'source_series' && selectedSeries
    ? { basis: 'source_series', series_key: selectedSeries, unit }
    : basis === 'operator_peak'
      ? { basis: 'operator_peak', value, unit }
      : undefined

  const analyze = async () => {
    if (!file) return
    const requestID = ++requestRef.current
    const configRevision = configRevisionRef.current
    const form = new FormData()
    if (projectId) form.set('project_id', projectId)
    form.set('file', file)
    if (sheet) form.set('sheet', sheet)
    if (timestampColumn) form.set('timestamp_column', timestampColumn)
    form.set('timestamp_mode', timestampMode)
    if (timestampMode === 'relative') form.set('relative_start_time', relativeStartTime)
    valueColumns.forEach(column => form.append('value_column', column))
    artColumns.forEach(column => form.append('art_column', column))
    if (seriesColumn) form.set('series_column', seriesColumn)
    if (timezone) form.set('timezone', timezone)
    setBusy(true)
    setError('')
    try {
      const result = await api.analyzeTrafficProfile(form)
      if (requestID !== requestRef.current || configRevision !== configRevisionRef.current) return
      setAnalysis(result)
      setAnalysisDirty(false)
      const firstSheet = result.sheets?.find(candidate => candidate.name === sheet) || result.sheets?.[0]
      if (firstSheet && !sheet) setSheet(firstSheet.name)
      const firstSeries = result.series?.[0]?.series.key || ''
      setSeriesKey(firstSeries)
      const availableARTKey = result.art_series?.some(item => item.series.key === artSeriesKey) ? artSeriesKey : result.art_series?.[0]?.series.key || ''
      if (artBasis === 'source_series') setARTSeriesKey(availableARTKey)
      if (firstSeries) onReady({ analysis: result, seriesKey: firstSeries, art: artRequest(artBasis, availableARTKey) })
      else onReady(null)
    } catch (cause) {
      if (requestID !== requestRef.current || configRevision !== configRevisionRef.current) return
      setError(getErrorMessage(cause, `Unable to analyze the workload for ${groupName}.`))
    } finally {
      setBusy(false)
    }
  }

  const invalidateColumns = () => {
    configRevisionRef.current += 1
    setAnalysisDirty(true)
    setSeriesKey('')
    onReady(null)
  }

  return <section className="traffic-profile-group-upload" aria-label={`${groupName} workload file`}>
    <div className="traffic-profile-group-upload-header">
      <div><span className="traffic-profile-section-label">Scenario group</span><strong>{groupName}</strong></div>
      {ready ? <span className="status-badge success">Ready</span> : <span className="status-badge">File required</span>}
    </div>
    <ImportDropzone accept=".csv,.tsv,.txt,.xlsx" file={file} onFile={next => { setFile(next); clearSelection(Boolean(!next || !savedFile || next.name !== savedFile.filename)) }} dropHint={`Drop CSV/XLSX for ${groupName}`} onError={setError} disabled={busy} />
    {analysis?.sheets?.length ? <div className="settings-grid compact-grid">
      <label><span>{groupName} worksheet</span><select aria-label={`${groupName} worksheet`} value={sheet} onChange={event => { setSheet(event.target.value); setTimestampColumn(''); setValueColumns([]); setARTColumns([]); setSeriesColumn(''); invalidateColumns() }}>{analysis.sheets.map(item => <option key={item.name}>{item.name}</option>)}</select></label>
      <label><span>{groupName} timestamp column</span><select aria-label={`${groupName} timestamp column`} value={timestampColumn} onChange={event => { setTimestampColumn(event.target.value); invalidateColumns() }}><option value="">Select…</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} timestamp values</span><select aria-label={`${groupName} timestamp values`} value={timestampMode} onChange={event => { setTimestampMode(event.target.value as typeof timestampMode); invalidateColumns() }}><option value="absolute">Absolute date/time</option><option value="relative">Relative elapsed time</option></select></label>
      {timestampMode === 'relative' && <label><span>{groupName} relative 0:00 starts at</span><input aria-label={`${groupName} relative 0:00 starts at`} type="time" step={1} value={relativeStartTime} onChange={event => { setRelativeStartTime(event.target.value); invalidateColumns() }} /></label>}
      <label><span>{groupName} value columns</span><select aria-label={`${groupName} value columns`} multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={valueColumns} onChange={event => { setValueColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateColumns() }}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} optional ART columns</span><select aria-label={`${groupName} optional ART columns`} multiple size={Math.min(5, Math.max(2, selectedSheet?.columns.length || 2))} value={artColumns} onChange={event => { setARTColumns(Array.from(event.currentTarget.selectedOptions, option => option.value)); invalidateColumns() }}>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} optional series column</span><select aria-label={`${groupName} optional series column`} value={seriesColumn} onChange={event => { setSeriesColumn(event.target.value); invalidateColumns() }}><option value="">None</option>{selectedSheet?.columns.map(column => <option key={column}>{column}</option>)}</select></label>
      <label><span>{groupName} timezone</span><TimezoneSelect ariaLabel={`${groupName} timezone`} value={timezone} onChange={value => { setTimezone(value); invalidateColumns() }} /></label>
    </div> : null}
    <button type="button" className="btn btn-secondary" onClick={() => void analyze()} disabled={busy || !file}>{busy ? 'Working…' : analysis?.series?.length ? `Re-analyze ${groupName}` : `Analyze ${groupName}`}</button>
    {analysis?.series?.length && !analysisDirty ? <><label className="traffic-profile-group-series"><span>Workload series for {groupName}</span><select aria-label={`Workload series for ${groupName}`} value={seriesKey} onChange={event => { const next = event.target.value; setSeriesKey(next); onReady(next ? { analysis, seriesKey: next, art: artRequest() } : null) }}><option value="">Select…</option>{analysis.series.map(item => <option key={item.series.key} value={item.series.key}>{item.series.name} · {item.stats.count.toLocaleString()} points · peak {item.stats.max}</option>)}</select></label><div className="settings-grid compact-grid"><label><span>ART basis for {groupName}</span><select aria-label={`ART basis for ${groupName}`} value={artBasis} onChange={event => { const next = event.target.value as GroupARTSelection['basis']; setARTBasis(next); if (seriesKey) onReady({ analysis, seriesKey, art: artRequest(next) }) }}><option value="none">Not provided</option>{analysis.art_series?.length ? <option value="source_series">File ART series</option> : null}<option value="operator_peak">Estimated peak ART</option></select></label>{artBasis === 'source_series' ? <label><span>ART series for {groupName}</span><select aria-label={`ART series for ${groupName}`} value={artSeriesKey} onChange={event => { const next = event.target.value; setARTSeriesKey(next); if (seriesKey) onReady({ analysis, seriesKey, art: artRequest('source_series', next) }) }}><option value="">Select…</option>{analysis.art_series?.map(item => <option key={item.series.key} value={item.series.key}>{item.series.name} · peak {item.stats.max}</option>)}</select></label> : artBasis === 'operator_peak' ? <label><span>Estimated peak ART for {groupName}</span><input aria-label={`Estimated peak ART for ${groupName}`} type="number" min={0.001} step="any" value={artValue ?? ''} onChange={event => { const next = Number(event.target.value); setARTValue(next); if (seriesKey) onReady({ analysis, seriesKey, art: artRequest('operator_peak', '', next) }) }} /></label> : null}{artBasis !== 'none' ? <label><span>ART unit for {groupName}</span><select aria-label={`ART unit for ${groupName}`} value={artUnit} onChange={event => { const next = event.target.value as ARTUnit; setARTUnit(next); if (seriesKey) onReady({ analysis, seriesKey, art: artRequest(artBasis, artSeriesKey, artValue, next) }) }}><option value="milliseconds">Milliseconds</option><option value="seconds">Seconds</option></select></label> : null}</div></> : null}
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
