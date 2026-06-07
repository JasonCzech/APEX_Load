import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { Area, AreaChart, CartesianGrid, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { Application, DashboardAuthEffectiveAccessResource, DraftCapacityEstimate, LaunchConflict, LaunchPreflightResponse, Release, RuntimeSettingsOverride, ScenarioSchedule, ScenarioScheduleHealth, ScenarioScheduleOccurrence, StoredScenario, SystemInventoryGenerator } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatTimeCompact, parseDurationToSeconds } from '../utils/timeUtils'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import {
  buildEffectiveScenarioGroups,
  buildEmptyRunDefaultOverrides,
  buildGroupEditDraft,
  buildGroupsForRunPayload,
  buildGroupRuntimeTableValues,
  buildRunDefaultCards,
  buildRunDefaultEditDraft,
  buildRunLoadProfilePreview,
  buildRuntimeSettingsForRun,
  emptyGroupEditDraft,
  formatGroupPlacement,
  generatorCanRunGroup,
  generatorDisabledReason,
  getEffectiveResourceBaseline,
  getEffectiveRunDuration,
  GLOBAL_RUN_DEFAULT_KEYS,
  groupOverrideCount,
  groupOverrideKey,
  groupPlacementSummary,
  hasGroupOverrideValues,
  hasOwnKey,
  hasRuntimeSettingsValues,
  selectedGeneratorCapacity,
  type GroupEditDraft,
  type GroupRunOverride,
  type RunDefaultEditDraft,
  type RunDefaultKey,
  type RunDefaultOverrides,
  type RunRuntimeOverrideDraft,
  type ScenarioScriptGroup,
} from './newTestRunModel'
import './CreateTestModal.css'

interface Props {
  activeApplication: Application | null
  activeRelease: Release | null
  releases: Release[]
  initialScenarioId?: string
  onSelectTest: (id: string) => void
  onCreateRelease: (name: string) => Promise<Release | null>
  onOpenScenarioBuilder?: () => void
}

type ScheduleDraft = {
  name: string
  cron_expr: string
  timezone: string
  release_id: string
  run_name_template: string
  enabled: boolean
  approval_policy: string
  missed_run_policy: string
  blackout_start_time: string
  blackout_end_time: string
  availability_start_time: string
  availability_end_time: string
}

type SortDirection = 'asc' | 'desc'
type ScenarioSortField = 'name' | 'releases' | 'last_run_at' | 'vusers' | 'duration' | 'groups'
const NO_RELEASE_FILTER = '__no_release__'
type GroupEditChanges = {
  vusers: boolean
  initialization: boolean
  ramp_up: boolean
  ramp_down: boolean
  iterations: boolean
  log_level: boolean
  auto_transactions: boolean
  pacing: boolean
  think_time: boolean
  continue_on_error: boolean
  snapshot_on_error: boolean
  error_handling: boolean
  browser_device_profile: boolean
  browser_headless: boolean
  browser_touch_mode: boolean
  browser_orientation: boolean
  browser: boolean
  assignment: boolean
}

const LOAD_PROFILE_COLORS = ['#2563eb', '#16a34a', '#f59e0b', '#dc2626', '#7c3aed', '#0891b2']

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatCount(value: number, singular: string, plural = `${singular}s`) {
  return `${value} ${value === 1 ? singular : plural}`
}

function formatScheduleHealthCount(value?: number) {
  return typeof value === 'number' ? value.toLocaleString() : '-'
}

function formatScheduleTimestamp(value?: string) {
  return value ? new Date(value).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : 'Not scheduled'
}

function formatLaunchTimestamp(value?: string) {
  return value ? new Date(value).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : 'Pending'
}

function formatLaunchWindow(preflight?: LaunchPreflightResponse | null) {
  const window = preflight?.candidate_window
  if (!window) return 'Pending preflight'
  return `${formatLaunchTimestamp(window.start_at)} - ${formatLaunchTimestamp(window.end_at)}`
}

function formatLaunchDecision(preflight?: LaunchPreflightResponse | null, loading = false) {
  if (loading) return 'Checking'
  if (!preflight) return 'Pending preflight'
  if (preflight.start_now_allowed) return 'Start now'
  if (preflight.queue_allowed) return 'Queue'
  if (preflight.schedule_allowed) return 'Schedule'
  return 'Blocked'
}

function firstLaunchConflict(preflight?: LaunchPreflightResponse | null): LaunchConflict | undefined {
  return preflight?.blocking_conflicts?.[0] || preflight?.warnings?.[0]
}

function formatLaunchConflict(preflight?: LaunchPreflightResponse | null) {
  const conflict = firstLaunchConflict(preflight)
  if (!conflict) return 'None'
  return conflict.message
}

function formatNextBlockingEvent(preflight?: LaunchPreflightResponse | null) {
  const conflicts = preflight?.blocking_conflicts || []
  const dated = conflicts
    .filter(conflict => conflict.starts_at)
    .sort((left, right) => String(left.starts_at).localeCompare(String(right.starts_at)))
  const first = dated[0]
  return first ? `${formatLaunchTimestamp(first.starts_at)} - ${first.message}` : 'None'
}

function formatDatetimeLocalValue(date: Date) {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function datetimeLocalToISO(value: string) {
  if (!value) return ''
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toISOString()
}

function formatUtcScheduleClock(date: Date) {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(date)
}

function formatTimeZoneClock(date: Date, timeZone: string) {
  try {
    return new Intl.DateTimeFormat('en-US', {
      timeZone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(date)
  } catch {
    return formatUtcScheduleClock(date)
  }
}

function formatTimeZoneOffset(date: Date, timeZone: string) {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(date)
    const partValue = (type: string) => Number(parts.find(part => part.type === type)?.value || 0)
    const zonedTime = Date.UTC(
      partValue('year'),
      partValue('month') - 1,
      partValue('day'),
      partValue('hour'),
      partValue('minute'),
      partValue('second'),
    )
    const offsetMinutes = Math.round((zonedTime - date.getTime()) / 60000)
    const sign = offsetMinutes >= 0 ? '+' : '-'
    const absoluteMinutes = Math.abs(offsetMinutes)
    const hours = String(Math.floor(absoluteMinutes / 60)).padStart(2, '0')
    const minutes = String(absoluteMinutes % 60).padStart(2, '0')
    return `UTC${sign}${hours}:${minutes}`
  } catch {
    return 'UTC+00:00'
  }
}

function formatTimeZoneOptionLabel(timeZone: string, date: Date) {
  return `${timeZone.replace(/_/g, ' ')} - ${formatTimeZoneClock(date, timeZone)} ${formatTimeZoneOffset(date, timeZone)}`
}

function formatScheduleToken(value?: string) {
  return value ? value.replace(/_/g, ' ') : ''
}

function formatScheduleWindow(window: { name?: string, start_time: string, end_time: string, timezone?: string }) {
  const label = window.name ? `${window.name}: ` : ''
  return `${label}${window.start_time}-${window.end_time}${window.timezone ? ` ${window.timezone}` : ''}`
}

function scheduleStatusTone(value?: string) {
  if (!value) return ''
  if (['blocked', 'failed', 'rejected'].includes(value)) return 'danger'
  if (['approval_required', 'operator_decision_required', 'pending', 'skipped'].includes(value)) return 'warning'
  if (['materialized', 'queued', 'approved', 'completed', 'enabled'].includes(value)) return 'healthy'
  return ''
}

const COMMON_TIMEZONES = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Phoenix',
  'America/Anchorage',
  'Pacific/Honolulu',
  'America/Toronto',
  'America/Vancouver',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Amsterdam',
  'Europe/Stockholm',
  'Europe/Moscow',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Australia/Sydney',
  'Australia/Melbourne',
  'Pacific/Auckland',
]

function describeCron(expr: string): string {
  const parts = expr.trim().split(/\s+/)
  if (parts.length !== 5) return ''
  const [minute, hour, dom, month, dow] = parts

  const dowNames: Record<string, string> = { '0': 'Sun', '1': 'Mon', '2': 'Tue', '3': 'Wed', '4': 'Thu', '5': 'Fri', '6': 'Sat', '7': 'Sun' }
  const monthNames: Record<string, string> = { '1': 'Jan', '2': 'Feb', '3': 'Mar', '4': 'Apr', '5': 'May', '6': 'Jun', '7': 'Jul', '8': 'Aug', '9': 'Sep', '10': 'Oct', '11': 'Nov', '12': 'Dec' }

  const formatTime = (h: string, m: string) => {
    const hNum = parseInt(h, 10)
    const mNum = parseInt(m, 10)
    if (isNaN(hNum) || isNaN(mNum)) return null
    const suffix = hNum >= 12 ? 'PM' : 'AM'
    const displayH = hNum === 0 ? 12 : hNum > 12 ? hNum - 12 : hNum
    return `${displayH}:${String(mNum).padStart(2, '0')} ${suffix}`
  }

  const describeDow = (field: string) => {
    if (field === '*') return ''
    return field.split(',').map(part => {
      if (part.includes('-')) {
        const [start, end] = part.split('-')
        return `${dowNames[start] || start}–${dowNames[end] || end}`
      }
      return dowNames[part] || part
    }).join(', ')
  }

  const describeMonth = (field: string) => {
    if (field === '*') return ''
    return field.split(',').map(part => monthNames[part] || part).join(', ')
  }

  const time = formatTime(hour, minute)
  const dowDesc = describeDow(dow)
  const monthDesc = describeMonth(month)
  const domDesc = dom !== '*' ? `on day ${dom}` : ''

  const segments: string[] = []
  if (time) segments.push(`At ${time}`)
  else if (hour === '*' && minute === '*') segments.push('Every minute')
  else if (hour === '*') segments.push(`At minute ${minute} of every hour`)
  else segments.push(`At ${hour}:${String(minute).padStart(2, '0')}`)

  if (dowDesc) segments.push(dowDesc)
  if (domDesc) segments.push(domDesc)
  if (monthDesc) segments.push(`in ${monthDesc}`)

  return segments.join(', ')
}

function totalScenarioVUsers(scenario?: StoredScenario | null) {
  return scenario?.scripts.reduce((sum, script) => sum + (script.vusers || 0), 0) || 0
}

function totalGroupVUsers(groups: ScenarioScriptGroup[]) {
  return groups.reduce((sum, script) => sum + (script.vusers || 0), 0)
}

function scenarioProtocolList(scenario?: StoredScenario | null) {
  const values = new Set((scenario?.scripts || []).map(script => script.protocol || 'http'))
  return Array.from(values)
}

function groupProtocolList(groups: ScenarioScriptGroup[]) {
  const values = new Set(groups.map(script => script.protocol || 'http'))
  return Array.from(values)
}

function formatScenarioModeShort(value?: StoredScenario['scenario_mode']) {
  return value === 'goal_oriented' ? 'Goal' : 'VUser'
}

function formatScenarioWorkloadSummary(scenario: StoredScenario) {
  const groupCount = scenario.scripts.length
  const vusers = totalScenarioVUsers(scenario)
  return `${groupCount} group${groupCount === 1 ? '' : 's'} · ${vusers} VUsers`
}

function formatScenarioReleaseSummary(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  return releases.length ? releases.map(release => release.name).join(', ') : 'No releases'
}

function scenarioSearchText(scenario: StoredScenario) {
  return [
    scenario.name,
    scenario.duration,
    scenario.scenario_mode,
    ...scenarioProtocolList(scenario),
    ...(scenario.scripts || []).map(script => `${script.name || ''} ${script.script_name || ''} ${script.script_id || ''}`),
    ...(scenario.data_pools || []).map(pool => `${pool.alias} ${pool.pool_name || pool.pool_id}`),
    ...(scenario.associated_releases || []).map(release => release.name),
  ].filter(Boolean).join(' ').toLowerCase()
}

function scenarioMatchesReleaseFilter(scenario: StoredScenario, releaseFilter: string) {
  if (!releaseFilter) return true
  const associatedReleases = scenario.associated_releases || []
  if (releaseFilter === NO_RELEASE_FILTER) return associatedReleases.length === 0
  return associatedReleases.some(release => release.id === releaseFilter || release.name === releaseFilter)
}

function buildDefaultScheduleDraft(scenario?: StoredScenario | null, releaseId?: string) {
  return {
    name: scenario ? `${scenario.name} Schedule` : '',
    cron_expr: '0 9 * * 1-5',
    timezone: 'UTC',
    release_id: releaseId || '',
    run_name_template: scenario?.name || '',
    enabled: true,
    approval_policy: 'auto',
    missed_run_policy: 'skip',
    blackout_start_time: '',
    blackout_end_time: '',
    availability_start_time: '',
    availability_end_time: '',
  }
}

function SortIndicator({ active, direction }: { active: boolean, direction: SortDirection }) {
  return <span className={`sort-icon ${active ? 'active' : ''}`}>{active ? (direction === 'asc' ? '↑' : '↓') : '↕'}</span>
}

function changedFieldClass(changed: boolean, base = '') {
  return [base, changed ? 'new-test-field-changed' : ''].filter(Boolean).join(' ') || undefined
}

function ChangedBadge({ changed }: { changed: boolean }) {
  return changed ? <em className="new-test-changed-pill">Changed</em> : null
}

function normalizeDraftText(value?: string) {
  return String(value || '').trim()
}

function selectedTargetsChanged(left: string[], right: string[]) {
  const normalize = (values: string[]) => values.filter(Boolean).slice().sort().join('\u0000')
  return normalize(left) !== normalize(right)
}

function buildGroupEditChanges(draft: GroupEditDraft, baseline: GroupEditDraft): GroupEditChanges {
  const continueOnErrorChanged = normalizeDraftText(draft.continue_on_error) !== normalizeDraftText(baseline.continue_on_error)
  const snapshotOnErrorChanged = normalizeDraftText(draft.snapshot_on_error) !== normalizeDraftText(baseline.snapshot_on_error)
  const browserDeviceProfileChanged = normalizeDraftText(draft.browser_device_profile) !== normalizeDraftText(baseline.browser_device_profile)
  const browserHeadlessChanged = normalizeDraftText(draft.browser_headless) !== normalizeDraftText(baseline.browser_headless)
  const browserTouchModeChanged = normalizeDraftText(draft.browser_touch_mode) !== normalizeDraftText(baseline.browser_touch_mode)
  const browserOrientationChanged = normalizeDraftText(draft.browser_orientation) !== normalizeDraftText(baseline.browser_orientation)
  return {
    vusers: normalizeDraftText(draft.vusers) !== normalizeDraftText(baseline.vusers),
    initialization: draft.initialization !== baseline.initialization,
    ramp_up: normalizeDraftText(draft.ramp_up) !== normalizeDraftText(baseline.ramp_up),
    ramp_down: normalizeDraftText(draft.ramp_down) !== normalizeDraftText(baseline.ramp_down),
    iterations: normalizeDraftText(draft.iterations) !== normalizeDraftText(baseline.iterations),
    log_level: normalizeDraftText(draft.log_level) !== normalizeDraftText(baseline.log_level),
    auto_transactions: normalizeDraftText(draft.auto_transactions) !== normalizeDraftText(baseline.auto_transactions),
    pacing: normalizeDraftText(draft.pacingType) !== normalizeDraftText(baseline.pacingType)
      || normalizeDraftText(draft.pacingAmount) !== normalizeDraftText(baseline.pacingAmount),
    think_time: normalizeDraftText(draft.thinkTimeMs) !== normalizeDraftText(baseline.thinkTimeMs)
      || draft.thinkIgnore !== baseline.thinkIgnore,
    continue_on_error: continueOnErrorChanged,
    snapshot_on_error: snapshotOnErrorChanged,
    error_handling: continueOnErrorChanged || snapshotOnErrorChanged,
    browser_device_profile: browserDeviceProfileChanged,
    browser_headless: browserHeadlessChanged,
    browser_touch_mode: browserTouchModeChanged,
    browser_orientation: browserOrientationChanged,
    browser: browserDeviceProfileChanged || browserHeadlessChanged || browserTouchModeChanged || browserOrientationChanged,
    assignment: draft.assignmentMode !== baseline.assignmentMode
      || selectedTargetsChanged(draft.selectedTargets, baseline.selectedTargets),
  }
}

export default function NewTestPage({ activeApplication, activeRelease, releases, initialScenarioId, onSelectTest, onCreateRelease, onOpenScenarioBuilder }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [pageMode, setPageMode] = useState<'launch' | 'schedules'>('launch')
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scenarioId, setScenarioId] = useState(initialScenarioId || '')
  const [releaseId, setReleaseId] = useState(activeRelease?.id || '')
  const [schedules, setSchedules] = useState<ScenarioSchedule[]>([])
  const [scheduleHealth, setScheduleHealth] = useState<ScenarioScheduleHealth | null>(null)
  const [scheduleLoading, setScheduleLoading] = useState(false)
  const [scheduleSubmitting, setScheduleSubmitting] = useState(false)
  const [scheduleClock, setScheduleClock] = useState(() => new Date())
  const [editingScheduleId, setEditingScheduleId] = useState<string | null>(null)
  const [expandedScheduleId, setExpandedScheduleId] = useState<string | null>(null)
  const [scheduleOccurrenceLoading, setScheduleOccurrenceLoading] = useState<string | null>(null)
  const [scheduleOccurrences, setScheduleOccurrences] = useState<Record<string, ScenarioScheduleOccurrence[]>>({})
  const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [runName, setRunName] = useState('')
  const [newReleaseName, setNewReleaseName] = useState('')
  const [scenarioQuery, setScenarioQuery] = useState('')
  const [scenarioReleaseFilter, setScenarioReleaseFilter] = useState('')
  const [scenarioSortField, setScenarioSortField] = useState<ScenarioSortField>('name')
  const [scenarioSortDirection, setScenarioSortDirection] = useState<SortDirection>('asc')
  const [expandedScenarioId, setExpandedScenarioId] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [confirmDeleteScheduleId, setConfirmDeleteScheduleId] = useState<string | null>(null)
  const [showAllRuntimeDefaults, setShowAllRuntimeDefaults] = useState(false)
  const [runDefaultOverrides, setRunDefaultOverrides] = useState<RunDefaultOverrides>(() => buildEmptyRunDefaultOverrides())
  const [editingDefaultKey, setEditingDefaultKey] = useState<RunDefaultKey | null>(null)
  const [defaultEditDraft, setDefaultEditDraft] = useState<RunDefaultEditDraft>({ value: '', pacingType: '', pacingAmount: '', thinkIgnore: false })
  const [defaultSaveNotice, setDefaultSaveNotice] = useState('')
  const [groupRunOverrides, setGroupRunOverrides] = useState<Record<string, GroupRunOverride>>({})
  const [editingGroupKey, setEditingGroupKey] = useState<string | null>(null)
  const [groupEditDraft, setGroupEditDraft] = useState<GroupEditDraft>(() => emptyGroupEditDraft())
  const [groupSaveNotice, setGroupSaveNotice] = useState('')
  const [showNewRelease, setShowNewRelease] = useState(false)
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [immediatePreflight, setImmediatePreflight] = useState<LaunchPreflightResponse | null>(null)
  const [immediatePreflightLoading, setImmediatePreflightLoading] = useState(false)
  const [scheduledPreflight, setScheduledPreflight] = useState<LaunchPreflightResponse | null>(null)
  const [scheduledPreflightLoading, setScheduledPreflightLoading] = useState(false)
  const [scheduledStartLocal, setScheduledStartLocal] = useState('')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const [generatorInventoryLoading, setGeneratorInventoryLoading] = useState(false)
  const [generatorInventoryError, setGeneratorInventoryError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScenarios(activeProject?.id)
      const next = res.scenarios || []
      setScenarios(next)
      setScenarioId(prev => {
        if (next.some(item => item.id === prev)) return prev
        if (initialScenarioId && next.some(item => item.id === initialScenarioId)) return initialScenarioId
        return next[0]?.id || ''
      })
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [activeProject?.id, api, initialScenarioId])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => {
    const timer = window.setInterval(() => setScheduleClock(new Date()), 60_000)
    return () => window.clearInterval(timer)
  }, [])
  useEffect(() => {
    if (!initialScenarioId) return
    setScenarioId(prev => prev === initialScenarioId ? prev : initialScenarioId)
    setRunName('')
  }, [initialScenarioId])
  useEffect(() => {
    setRunDefaultOverrides(buildEmptyRunDefaultOverrides())
    setGroupRunOverrides({})
    setEditingDefaultKey(null)
    setEditingGroupKey(null)
    setDefaultSaveNotice('')
    setGroupSaveNotice('')
    setShowAllRuntimeDefaults(false)
  }, [scenarioId])
  useEffect(() => { setReleaseId(activeRelease?.id || releases[0]?.id || '') }, [activeRelease?.id, releases])
  useEffect(() => {
    let cancelled = false
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(activeProject?.id)
        if (!cancelled) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api])
  useEffect(() => {
    let cancelled = false
    const loadInventory = async () => {
      setGeneratorInventoryLoading(true)
      setGeneratorInventoryError('')
      try {
        const response = await api.getSystemInventory()
        if (!cancelled) {
          setGeneratorInventory(response.generators || [])
        }
      } catch (err: unknown) {
        if (!cancelled) {
          setGeneratorInventory([])
          setGeneratorInventoryError(getErrorMessage(err, 'Unable to load generator inventory'))
        }
      } finally {
        if (!cancelled) setGeneratorInventoryLoading(false)
      }
    }
    loadInventory()
    return () => {
      cancelled = true
    }
  }, [api])

  const scenario = useMemo(() => scenarios.find(item => item.id === scenarioId), [scenarios, scenarioId])
  const release = useMemo(() => releases.find(item => item.id === releaseId), [releases, releaseId])
  const effectiveRunDuration = useMemo(() => getEffectiveRunDuration(scenario, runDefaultOverrides), [scenario, runDefaultOverrides])
  const effectiveResourceBaseline = useMemo(() => getEffectiveResourceBaseline(scenario, runDefaultOverrides), [scenario, runDefaultOverrides])
  const effectiveScenarioGroups = useMemo(() => buildEffectiveScenarioGroups(scenario, groupRunOverrides), [groupRunOverrides, scenario])
  const runLoadProfilePreview = useMemo(
    () => buildRunLoadProfilePreview(effectiveScenarioGroups, effectiveRunDuration, scenario?.start_delay, effectiveResourceBaseline),
    [effectiveResourceBaseline, effectiveRunDuration, effectiveScenarioGroups, scenario?.start_delay],
  )
  const runDefaultCards = useMemo(() => buildRunDefaultCards(scenario, runDefaultOverrides), [scenario, runDefaultOverrides])
  const globalRunDefaultCards = useMemo(
    () => runDefaultCards.filter(card => GLOBAL_RUN_DEFAULT_KEYS.has(card.key)),
    [runDefaultCards],
  )
  const runtimeRunDefaultCards = useMemo(
    () => runDefaultCards.filter(card => !GLOBAL_RUN_DEFAULT_KEYS.has(card.key)),
    [runDefaultCards],
  )
  const runtimeRunDefaultOverrideCount = useMemo(
    () => runtimeRunDefaultCards.filter(card => card.isOverridden).length,
    [runtimeRunDefaultCards],
  )
  const editingDefaultCard = useMemo(
    () => runDefaultCards.find(card => card.key === editingDefaultKey),
    [editingDefaultKey, runDefaultCards],
  )
  const editingGroup = useMemo(
    () => scenario?.scripts.find(group => groupOverrideKey(group) === editingGroupKey),
    [editingGroupKey, scenario],
  )
  const groupEditBaseline = useMemo(
    () => editingGroup && scenario ? buildGroupEditDraft(editingGroup, scenario, runDefaultOverrides) : emptyGroupEditDraft(),
    [editingGroup, runDefaultOverrides, scenario],
  )
  const groupEditChanges = useMemo(
    () => buildGroupEditChanges(groupEditDraft, groupEditBaseline),
    [groupEditBaseline, groupEditDraft],
  )
  const hasGroupEditChanges = useMemo(
    () => Object.values(groupEditChanges).some(Boolean),
    [groupEditChanges],
  )
  const generatorByTarget = useMemo(() => new Map(generatorInventory.map(generator => [generator.target, generator])), [generatorInventory])
  const scenarioReleaseFilterOptions = useMemo(() => {
    const optionMap = new Map<string, string>()
    releases.forEach(item => {
      optionMap.set(item.name, item.name)
    })
    scenarios.forEach(item => {
      (item.associated_releases || []).forEach(releaseItem => {
        optionMap.set(releaseItem.name, releaseItem.name)
      })
    })
    return Array.from(optionMap.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((left, right) => left.label.localeCompare(right.label))
  }, [releases, scenarios])
  const scenariosWithoutRelease = useMemo(
    () => scenarios.some(item => !item.associated_releases || item.associated_releases.length === 0),
    [scenarios],
  )
  const filteredScenarios = useMemo(() => {
    const query = scenarioQuery.trim().toLowerCase()
    return scenarios.filter(item => {
      if (!scenarioMatchesReleaseFilter(item, scenarioReleaseFilter)) return false
      return !query || scenarioSearchText(item).includes(query)
    })
  }, [scenarioQuery, scenarioReleaseFilter, scenarios])
  const scenarioFiltersActive = Boolean(scenarioQuery.trim() || scenarioReleaseFilter)
  const sortedScenarios = useMemo(() => {
    const valueFor = (item: StoredScenario): string | number => {
      if (scenarioSortField === 'last_run_at') return new Date(item.last_run_at || 0).getTime()
      if (scenarioSortField === 'vusers') return totalScenarioVUsers(item)
      if (scenarioSortField === 'duration') return parseDurationToSeconds(item.duration)
      if (scenarioSortField === 'groups') return item.scripts.length
      if (scenarioSortField === 'releases') return formatScenarioReleaseSummary(item).toLowerCase()
      return item.name.toLowerCase()
    }

    return [...filteredScenarios].sort((left, right) => {
      const leftValue = valueFor(left)
      const rightValue = valueFor(right)
      const result = leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : left.name.localeCompare(right.name)
      return scenarioSortDirection === 'asc' ? result : -result
    })
  }, [filteredScenarios, scenarioSortDirection, scenarioSortField])
  const refreshScheduleHealth = useCallback(async () => {
    try {
      const res = await api.getScheduleHealth()
      setScheduleHealth(res)
    } catch {
      setScheduleHealth(null)
    }
  }, [api])
  const refreshSchedules = useCallback(async (currentScenarioId: string) => {
    if (!currentScenarioId) {
      setSchedules([])
      return
    }
    setScheduleLoading(true)
    try {
      const res = await api.listScenarioSchedules(currentScenarioId)
      setSchedules(res.schedules || [])
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load schedules'))
    } finally {
      setScheduleLoading(false)
    }
  }, [api])
  const totalVUsers = useMemo(() => totalGroupVUsers(effectiveScenarioGroups), [effectiveScenarioGroups])
  const protocols = useMemo(() => groupProtocolList(effectiveScenarioGroups), [effectiveScenarioGroups])
  const groupOverridesCount = useMemo(() => groupOverrideCount(groupRunOverrides), [groupRunOverrides])
  const runOverridesCount = useMemo(() => runDefaultCards.filter(card => card.isOverridden).length + groupOverridesCount, [groupOverridesCount, runDefaultCards])
  const launchSummaryItems = useMemo(() => {
    const capacityAdvisory = immediatePreflight?.capacity_advisory || draftEstimate?.capacity_advisory
    const dataPools = scenario?.data_pools || []
    const dataPoolValue = !scenario
      ? 'None'
      : dataPools.length
        ? dataPools.map(pool => pool.alias || pool.pool_name || pool.pool_id).join(', ')
        : 'None'
    const launchConflict = firstLaunchConflict(immediatePreflight)
    const launchBlocked = Boolean(immediatePreflight?.blocking_conflicts?.length)
    const capacityValue = !scenario
      ? 'Select a scenario'
      : immediatePreflightLoading || estimateLoading
        ? 'Refreshing estimate...'
        : capacityAdvisory
          ? `${formatCount(capacityAdvisory.recommended_generators, 'generator')} needed; ${capacityAdvisory.available_generators} available`
          : 'Pending estimate'
    const bottleneckValue = !scenario
      ? 'Not available'
      : immediatePreflightLoading || estimateLoading
        ? 'Refreshing...'
        : capacityAdvisory
          ? `${capacityAdvisory.bottleneck}, ${capacityAdvisory.headroom_pct}% headroom`
          : 'Pending estimate'

    return [
      { label: 'Run Name', value: runName.trim() || scenario?.name || 'Unnamed' },
      { label: 'Scenario', value: scenario?.name || 'None selected' },
      { label: 'Release', value: release?.name || 'None selected' },
      { label: 'Duration', value: effectiveRunDuration || 'Not set' },
      { label: 'Total VUsers', value: String(totalVUsers) },
      { label: 'Script Groups', value: scenario ? formatCount(effectiveScenarioGroups.length, 'group') : '0 groups' },
      { label: 'Protocols', value: protocols.length ? protocols.join(', ') : 'None' },
      { label: 'Run Overrides', value: formatCount(runOverridesCount, 'override') },
      { label: 'Host Placement', value: groupPlacementSummary(groupRunOverrides) },
      { label: 'Shared Data Pools', value: dataPoolValue },
      { label: 'Capacity', value: capacityValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Bottleneck', value: bottleneckValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Launch Decision', value: formatLaunchDecision(immediatePreflight, immediatePreflightLoading), tone: launchBlocked ? 'warning' : 'normal' },
      { label: 'Projected Window', value: formatLaunchWindow(immediatePreflight) },
      { label: 'Conflicts', value: launchConflict ? launchConflict.message : 'None', tone: launchConflict ? 'warning' : 'normal' },
      { label: 'Next Blocking Event', value: formatNextBlockingEvent(immediatePreflight), tone: launchBlocked ? 'warning' : 'normal' },
    ]
  }, [draftEstimate?.capacity_advisory, effectiveRunDuration, effectiveScenarioGroups.length, estimateLoading, groupRunOverrides, immediatePreflight, immediatePreflightLoading, protocols, release?.name, runName, runOverridesCount, scenario, totalVUsers])
  const readiness = useMemo(() => {
    const checks = [
      { label: activeApplication ? `Application: ${activeApplication.name}` : 'Select an application', ready: !!activeApplication },
      { label: release ? `Release: ${release.name}` : 'Select or create a release', ready: !!release },
      { label: scenario ? `Scenario: ${scenario.name}` : 'Select a scenario', ready: !!scenario },
      { label: scenario?.scripts.length ? `${scenario.scripts.length} script group${scenario.scripts.length === 1 ? '' : 's'} ready` : 'Scenario needs at least one script group', ready: !!scenario?.scripts.length },
    ]
    return checks
  }, [activeApplication, release, scenario])
  const canManageTests = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'), [effectiveAuthAccessResources])
  const canLaunch = readiness.every(item => item.ready) && !creating && canManageTests
  const nextScheduledRun = useMemo(() => schedules.filter(item => item.enabled && item.next_run_at).sort((left, right) => (left.next_run_at || '').localeCompare(right.next_run_at || ''))[0], [schedules])
  const schedulesNeedingAction = useMemo(() => schedules.filter(item => item.next_required_action || item.pending_approval_id || item.conflict_summary || item.last_error).length, [schedules])
  const scheduleConflictCount = useMemo(() => schedules.filter(item => item.conflict_summary).length, [schedules])
  const scheduleStatusCounts = scheduleHealth?.status_counts ?? {}
  const scheduleQueuedCount = scheduleHealth?.queued ?? scheduleStatusCounts.queued ?? 0
  const scheduleMaterializedCount = scheduleHealth?.materialized ?? scheduleStatusCounts.materialized ?? 0
  const scheduleSkippedCount = scheduleHealth?.skipped ?? scheduleStatusCounts.skipped ?? 0
  const scheduleCompletedCount = scheduleHealth?.completed ?? scheduleStatusCounts.completed ?? 0
  const scheduleHealthSeverity = !scheduleHealth
    ? 'unknown'
    : scheduleHealth.failed > 0 || scheduleHealth.blocked > 0
      ? 'danger'
      : scheduleHealth.pending_approval > 0 || scheduleHealth.due > 0
        ? 'warning'
        : scheduleHealth.total > 0
          ? 'healthy'
          : 'unknown'
  const scheduleHealthLabel = !scheduleHealth
    ? 'checking'
    : scheduleHealth.failed > 0
      ? `${scheduleHealth.failed} failed`
      : scheduleHealth.blocked > 0
        ? `${scheduleHealth.blocked} blocked`
        : scheduleHealth.pending_approval > 0
          ? `${scheduleHealth.pending_approval} approval`
          : scheduleHealth.due > 0
            ? `${scheduleHealth.due} due`
            : scheduleHealth.total > 0
              ? `${scheduleHealth.enabled}/${scheduleHealth.total} enabled`
              : 'no schedules'
  const scheduleHealthSummary = !scheduleHealth
    ? 'Checking governed schedule state across the project.'
    : scheduleHealth.total === 0
      ? 'No recurring schedules are configured yet.'
      : `${formatCount(scheduleHealth.total, 'schedule')} tracked across the project; ${formatCount(schedules.length, 'schedule')} ${schedules.length === 1 ? 'matches' : 'match'} the selected scenario.`
  const scheduleHealthMetrics = [
    { label: 'Due', value: scheduleHealth?.due, tone: scheduleHealth?.due ? 'warning' : undefined },
    { label: 'Blocked', value: scheduleHealth?.blocked, tone: scheduleHealth?.blocked ? 'danger' : undefined },
    { label: 'Failed', value: scheduleHealth?.failed, tone: scheduleHealth?.failed ? 'danger' : undefined },
    { label: 'Approval', value: scheduleHealth?.pending_approval, tone: scheduleHealth?.pending_approval ? 'warning' : undefined },
    { label: 'Queued', value: scheduleHealth ? scheduleQueuedCount : undefined },
    { label: 'Materialized', value: scheduleHealth ? scheduleMaterializedCount : undefined },
    { label: 'Skipped', value: scheduleHealth ? scheduleSkippedCount : undefined },
    { label: 'Completed', value: scheduleHealth ? scheduleCompletedCount : undefined },
  ]
  const scheduleHealthContext = [
    { label: 'Scenario', value: scenario?.name || 'None selected' },
    { label: 'Scenario Schedules', value: String(schedules.length) },
    { label: 'Scenario Enabled', value: String(schedules.filter(item => item.enabled).length) },
    { label: 'Needs Action', value: String(schedulesNeedingAction), tone: schedulesNeedingAction ? 'warning' : undefined },
    { label: 'Conflicts', value: String(scheduleConflictCount), tone: scheduleConflictCount ? 'danger' : undefined },
    { label: 'Next Scenario Run', value: nextScheduledRun ? formatScheduleTimestamp(nextScheduledRun.next_run_at) : 'Not scheduled' },
  ]
  const runCreatePayload = useMemo(() => {
    if (!scenario || !activeApplication) return null
    const runtimeSettings = buildRuntimeSettingsForRun(scenario, runDefaultOverrides)
    const groupPayloads = buildGroupsForRunPayload(scenario, groupRunOverrides)
    return {
      name: runName.trim() || scenario.name,
      duration: effectiveRunDuration,
      application_id: activeApplication.id,
      release_id: releaseId || undefined,
      scenario_id: scenario.id,
      project_id: activeProject?.id,
      ...(runtimeSettings !== undefined ? { runtime_settings: runtimeSettings } : {}),
      ...(groupPayloads ? { groups: groupPayloads } : {}),
      ...(hasOwnKey(runDefaultOverrides, 'script_shakeout') ? { script_shakeout: Boolean(runDefaultOverrides.script_shakeout) } : {}),
      ...(hasOwnKey(runDefaultOverrides, 'resource_baseline') ? { resource_baseline: Boolean(runDefaultOverrides.resource_baseline) } : {}),
    }
  }, [activeApplication, activeProject?.id, effectiveRunDuration, groupRunOverrides, releaseId, runDefaultOverrides, runName, scenario])
  const scheduledStartISO = useMemo(() => datetimeLocalToISO(scheduledStartLocal), [scheduledStartLocal])

  useEffect(() => {
    if (scenario && !runName) setRunName(scenario.name)
  }, [scenario, runName])
  useEffect(() => {
    if (!scenarioId) {
      setExpandedScenarioId('')
    }
  }, [scenarioId])
  useEffect(() => {
    void refreshSchedules(scenarioId)
  }, [refreshSchedules, scenarioId])
  useEffect(() => {
    if (pageMode === 'schedules') {
      void refreshScheduleHealth()
    }
  }, [pageMode, refreshScheduleHealth])
  useEffect(() => {
    if (!editingScheduleId) {
      setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
    }
  }, [editingScheduleId, releaseId, scenario])

  useEffect(() => {
    if (!runCreatePayload) {
      setDraftEstimate(null)
      return
    }
    let cancelled = false
    setEstimateLoading(true)
    const timer = window.setTimeout(async () => {
      try {
        const estimate = await api.estimateTest(runCreatePayload)
        if (!cancelled) setDraftEstimate(estimate)
      } catch {
        if (!cancelled) setDraftEstimate(null)
      } finally {
        if (!cancelled) setEstimateLoading(false)
      }
    }, 400)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [api, runCreatePayload])

  useEffect(() => {
    if (!runCreatePayload) {
      setImmediatePreflight(null)
      setImmediatePreflightLoading(false)
      return
    }
    let cancelled = false
    setImmediatePreflightLoading(true)
    const timer = window.setTimeout(async () => {
      try {
        const preflight = await api.preflightTest({ ...runCreatePayload, intent: 'immediate' })
        if (!cancelled) setImmediatePreflight(preflight)
      } catch {
        if (!cancelled) setImmediatePreflight(null)
      } finally {
        if (!cancelled) setImmediatePreflightLoading(false)
      }
    }, 400)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [api, runCreatePayload])

  useEffect(() => {
    if (!runCreatePayload || !scheduledStartISO) {
      setScheduledPreflight(null)
      setScheduledPreflightLoading(false)
      return
    }
    let cancelled = false
    setScheduledPreflightLoading(true)
    const timer = window.setTimeout(async () => {
      try {
        const preflight = await api.preflightTest({ ...runCreatePayload, intent: 'scheduled', scheduled_start_at: scheduledStartISO })
        if (!cancelled) setScheduledPreflight(preflight)
      } catch {
        if (!cancelled) setScheduledPreflight(null)
      } finally {
        if (!cancelled) setScheduledPreflightLoading(false)
      }
    }, 400)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [api, runCreatePayload, scheduledStartISO])

  const step1Complete = Boolean(scenario && scenario.scripts.length > 0)
  const step2Complete = Boolean(activeApplication && release)
  const step3Complete = step1Complete

  const createRelease = async () => {
    const name = newReleaseName.trim()
    if (!name) return
    const created = await onCreateRelease(name)
    if (created) setReleaseId(created.id)
    setNewReleaseName('')
  }

  const selectScenario = (nextScenarioId: string) => {
    setScenarioId(nextScenarioId)
    setExpandedScenarioId(prev => prev === nextScenarioId ? '' : nextScenarioId)
    setRunName('')
  }

  const sortScenarios = (field: ScenarioSortField) => {
    if (scenarioSortField === field) {
      setScenarioSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
      return
    }
    setScenarioSortField(field)
    setScenarioSortDirection(field === 'last_run_at' || field === 'vusers' ? 'desc' : 'asc')
  }

  const createRun = async (intent: 'start_now' | 'queue' | 'schedule') => {
    if (!canManageTests) {
      setError('Current session cannot create or start test runs for this project.')
      return
    }
    if (!scenario || !releaseId || !activeApplication || !runCreatePayload) {
      setError('Choose a release and scenario before creating a test.')
      return
    }
    if (intent === 'schedule' && !scheduledStartISO) {
      setError('Choose a scheduled start time before scheduling a run.')
      return
    }
    setCreating(true)
    setError('')
    try {
      const payload = intent === 'schedule'
        ? { ...runCreatePayload, release_id: releaseId, scheduled_start_at: scheduledStartISO }
        : { ...runCreatePayload, release_id: releaseId }
      const test = await api.createTest(payload)
      if (intent === 'start_now' || intent === 'queue') {
        await api.startTest(test.id)
      }
      onSelectTest(test.id)
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to create test'))
    } finally {
      setCreating(false)
    }
  }

  const openRunDefaultEditor = (key: RunDefaultKey) => {
    if (!scenario) return
    setDefaultEditDraft(buildRunDefaultEditDraft(key, scenario, runDefaultOverrides))
    setEditingDefaultKey(key)
  }

  const setRuntimeOverride = <K extends keyof RunRuntimeOverrideDraft>(field: K, value: RunRuntimeOverrideDraft[K]) => {
    setRunDefaultOverrides(prev => ({
      ...prev,
      runtime_settings: {
        ...prev.runtime_settings,
        [field]: value,
      },
    }))
  }

  const clearRunDefaultOverride = () => {
    if (!editingDefaultKey || !editingDefaultCard) return
    const key = editingDefaultKey
    setRunDefaultOverrides(prev => {
      const next: RunDefaultOverrides = {
        ...prev,
        runtime_settings: { ...prev.runtime_settings },
      }
      if (key === 'duration') {
        delete next.duration
      } else if (key === 'script_shakeout') {
        delete next.script_shakeout
      } else if (key === 'resource_baseline') {
        delete next.resource_baseline
      } else {
        delete next.runtime_settings[key]
      }
      return next
    })
    setDefaultSaveNotice(`${editingDefaultCard.label} now uses the scenario default. Scenario defaults were not changed.`)
    setEditingDefaultKey(null)
  }

  const saveRunDefaultOverride = () => {
    if (!editingDefaultKey || !editingDefaultCard) return
    const key = editingDefaultKey
    if (key === 'duration') {
      const duration = defaultEditDraft.value.trim()
      if (!duration) {
        setError('Duration is required for a run override.')
        return
      }
      setRunDefaultOverrides(prev => ({ ...prev, duration }))
    } else if (key === 'iterations') {
      setRuntimeOverride('iterations', defaultEditDraft.value ? Number(defaultEditDraft.value) : null)
    } else if (key === 'log_level') {
      setRuntimeOverride('log_level', defaultEditDraft.value || null)
    } else if (key === 'auto_transactions') {
      setRuntimeOverride('auto_transactions', defaultEditDraft.value || null)
    } else if (key === 'pacing') {
      if (!defaultEditDraft.pacingType) {
        setRuntimeOverride('pacing', null)
      } else if (defaultEditDraft.pacingType === 'none') {
        setRuntimeOverride('pacing', { type: 'none' })
      } else if (defaultEditDraft.pacingType === 'fixed_interval') {
        setRuntimeOverride('pacing', { type: 'fixed_interval', interval_ms: defaultEditDraft.pacingAmount ? Number(defaultEditDraft.pacingAmount) : 0 })
      } else if (defaultEditDraft.pacingType === 'tps') {
        setRuntimeOverride('pacing', { type: 'tps', tps: defaultEditDraft.pacingAmount ? Number(defaultEditDraft.pacingAmount) : 0 })
      } else {
        setRuntimeOverride('pacing', { type: 'fixed_delay', delay_ms: defaultEditDraft.pacingAmount ? Number(defaultEditDraft.pacingAmount) : 0 })
      }
    } else if (key === 'think_time') {
      if (defaultEditDraft.thinkIgnore) {
        setRuntimeOverride('think_time', { type: 'fixed', mean_ms: defaultEditDraft.value ? Number(defaultEditDraft.value) : 0, ignore: true })
      } else if (defaultEditDraft.value) {
        setRuntimeOverride('think_time', { type: 'fixed', mean_ms: Number(defaultEditDraft.value), ignore: false })
      } else {
        setRuntimeOverride('think_time', null)
      }
    } else if (key === 'continue_on_error') {
      setRuntimeOverride('continue_on_error', defaultEditDraft.value === '' ? null : defaultEditDraft.value === 'true')
    } else if (key === 'snapshot_on_error') {
      setRuntimeOverride('snapshot_on_error', defaultEditDraft.value === '' ? null : defaultEditDraft.value === 'true')
    } else if (key === 'script_shakeout') {
      setRunDefaultOverrides(prev => ({ ...prev, script_shakeout: defaultEditDraft.value === 'true' }))
    } else if (key === 'resource_baseline') {
      setRunDefaultOverrides(prev => ({ ...prev, resource_baseline: defaultEditDraft.value === 'true' }))
    }
    if (!GLOBAL_RUN_DEFAULT_KEYS.has(key)) {
      setShowAllRuntimeDefaults(true)
    }
    setError('')
    setDefaultSaveNotice(`Saved ${editingDefaultCard.label} for this run only. Scenario defaults were not changed.`)
    setEditingDefaultKey(null)
  }

  const renderRunDefaultEditorControl = () => {
    if (!editingDefaultKey) return null
    if (editingDefaultKey === 'duration') {
      return (
        <label>
          <span>Run duration</span>
          <input className="settings-input" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))} placeholder="30m0s" />
        </label>
      )
    }
    if (editingDefaultKey === 'iterations') {
      return (
        <label>
          <span>Iterations</span>
          <input className="settings-input" type="number" min="1" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))} placeholder="Script default" />
        </label>
      )
    }
    if (editingDefaultKey === 'log_level') {
      return (
        <label>
          <span>Log level</span>
          <select className="settings-input" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))}>
            <option value="">Script default</option>
            <option value="disabled">Disabled</option>
            <option value="standard">Standard</option>
            <option value="extended">Extended</option>
          </select>
        </label>
      )
    }
    if (editingDefaultKey === 'auto_transactions') {
      return (
        <label>
          <span>Auto transactions</span>
          <select className="settings-input" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))}>
            <option value="">Script default</option>
            <option value="none">None</option>
            <option value="per_action">Per Action</option>
            <option value="per_iteration">Per Iteration</option>
          </select>
        </label>
      )
    }
    if (editingDefaultKey === 'pacing') {
      return (
        <div className="new-test-default-editor-grid">
          <label>
            <span>Pacing</span>
            <select className="settings-input" value={defaultEditDraft.pacingType} onChange={event => setDefaultEditDraft(prev => ({ ...prev, pacingType: event.target.value, pacingAmount: event.target.value === 'none' ? '' : prev.pacingAmount }))}>
              <option value="">Script default</option>
              <option value="none">None</option>
              <option value="fixed_delay">Fixed delay</option>
              <option value="fixed_interval">Fixed interval</option>
              <option value="tps">Target TPS</option>
            </select>
          </label>
          {defaultEditDraft.pacingType && defaultEditDraft.pacingType !== 'none' && (
            <label>
              <span>{defaultEditDraft.pacingType === 'tps' ? 'Target TPS' : 'Milliseconds'}</span>
              <input className="settings-input" type="number" min="0" value={defaultEditDraft.pacingAmount} onChange={event => setDefaultEditDraft(prev => ({ ...prev, pacingAmount: event.target.value }))} placeholder="0" />
            </label>
          )}
        </div>
      )
    }
    if (editingDefaultKey === 'think_time') {
      return (
        <div className="new-test-default-editor-grid">
          <label>
            <span>Think time mean (ms)</span>
            <input className="settings-input" type="number" min="0" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))} placeholder="Script default" disabled={defaultEditDraft.thinkIgnore} />
          </label>
          <label className="checkbox-label new-test-default-checkbox">
            <input type="checkbox" checked={defaultEditDraft.thinkIgnore} onChange={event => setDefaultEditDraft(prev => ({ ...prev, thinkIgnore: event.target.checked }))} />
            <span>Ignore think time</span>
          </label>
        </div>
      )
    }
    if (editingDefaultKey === 'continue_on_error' || editingDefaultKey === 'snapshot_on_error') {
      return (
        <label>
          <span>{editingDefaultKey === 'continue_on_error' ? 'Continue on error' : 'Snapshot on error'}</span>
          <select className="settings-input" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))}>
            <option value="">Script default</option>
            <option value="true">Enabled</option>
            <option value="false">Disabled</option>
          </select>
        </label>
      )
    }
    return (
      <label>
        <span>{editingDefaultKey === 'script_shakeout' ? 'Script shakeout' : 'Resource baseline'}</span>
        <select className="settings-input" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({ ...prev, value: event.target.value }))}>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>
      </label>
    )
  }

  const openGroupEditor = (group: ScenarioScriptGroup) => {
    if (!scenario) return
    const key = groupOverrideKey(group)
    setGroupEditDraft(buildGroupEditDraft(group, scenario, runDefaultOverrides, groupRunOverrides[key]))
    setEditingGroupKey(key)
  }

  const clearGroupOverride = () => {
    if (!editingGroupKey || !editingGroup) return
    setGroupRunOverrides(prev => {
      const next = { ...prev }
      delete next[editingGroupKey]
      return next
    })
    setGroupSaveNotice(`${editingGroup.name || editingGroup.script_name || 'Script group'} now uses scenario and run defaults. Scenario defaults were not changed.`)
    setEditingGroupKey(null)
  }

  const saveGroupOverride = () => {
    if (!editingGroupKey || !editingGroup || !scenario) return
    if (!hasGroupEditChanges) return
    const nextOverride: GroupRunOverride = {}
    const vusers = Number(groupEditDraft.vusers)
    if (!Number.isFinite(vusers) || vusers < 1) {
      setError('VUsers must be greater than 0 for a script group override.')
      return
    }
    if (groupEditChanges.vusers) nextOverride.vusers = vusers
    const rampUp = groupEditDraft.ramp_up.trim()
    const rampDown = groupEditDraft.ramp_down.trim()
    if (groupEditChanges.ramp_up) nextOverride.ramp_up = rampUp
    if (groupEditChanges.ramp_down) nextOverride.ramp_down = rampDown
    if (groupEditChanges.initialization) {
      nextOverride.initialization = groupEditDraft.initialization
    }

    const runtimeSettings: RuntimeSettingsOverride = {}
    if (groupEditChanges.iterations) {
      const iterations = Number(groupEditDraft.iterations)
      if (!Number.isFinite(iterations) || iterations < 1) {
        setError('Iterations must be greater than 0 for a script group override.')
        return
      }
      runtimeSettings.iterations = iterations
    }
    if (groupEditChanges.log_level) runtimeSettings.log_level = groupEditDraft.log_level
    if (groupEditChanges.auto_transactions) runtimeSettings.auto_transactions = groupEditDraft.auto_transactions
    if (groupEditChanges.pacing) {
      if (groupEditDraft.pacingType === 'none') {
        runtimeSettings.pacing = { type: 'none' }
      } else if (groupEditDraft.pacingType === 'fixed_interval') {
        runtimeSettings.pacing = { type: 'fixed_interval', interval_ms: groupEditDraft.pacingAmount ? Number(groupEditDraft.pacingAmount) : 0 }
      } else if (groupEditDraft.pacingType === 'tps') {
        runtimeSettings.pacing = { type: 'tps', tps: groupEditDraft.pacingAmount ? Number(groupEditDraft.pacingAmount) : 0 }
      } else if (groupEditDraft.pacingType === 'fixed_delay') {
        runtimeSettings.pacing = { type: 'fixed_delay', delay_ms: groupEditDraft.pacingAmount ? Number(groupEditDraft.pacingAmount) : 0 }
      }
    }
    if (groupEditChanges.think_time && groupEditDraft.thinkIgnore) {
      runtimeSettings.think_time = { type: 'fixed', mean_ms: groupEditDraft.thinkTimeMs ? Number(groupEditDraft.thinkTimeMs) : 0, ignore: true }
    } else if (groupEditChanges.think_time && groupEditDraft.thinkTimeMs.trim()) {
      runtimeSettings.think_time = { type: 'fixed', mean_ms: Number(groupEditDraft.thinkTimeMs), ignore: false }
    }
    if (groupEditChanges.error_handling) {
      runtimeSettings.error_handling = {
        continue_on_error: groupEditDraft.continue_on_error === 'true',
        snapshot_on_error: groupEditDraft.snapshot_on_error === 'true',
      }
    }
    if (groupEditChanges.browser) {
      runtimeSettings.browser = {
        ...(groupEditDraft.browser_device_profile ? { device_profile: groupEditDraft.browser_device_profile } : {}),
        ...(groupEditDraft.browser_headless ? { headless: groupEditDraft.browser_headless === 'true' } : {}),
        ...(groupEditDraft.browser_touch_mode ? { touch_mode: groupEditDraft.browser_touch_mode === 'true' } : {}),
        ...(groupEditDraft.browser_orientation ? { orientation: groupEditDraft.browser_orientation } : {}),
      }
    }
    if (hasRuntimeSettingsValues(runtimeSettings)) {
      nextOverride.runtime_settings = runtimeSettings
    }
    if (groupEditChanges.assignment && groupEditDraft.assignmentMode === 'specific_hosts') {
      const selectedTargets = groupEditDraft.selectedTargets.filter(Boolean)
      if (selectedTargets.length === 0) {
        setError('Select at least one load generator host for group placement.')
        return
      }
      const unavailableTargets = selectedTargets.filter(target => {
        const generator = generatorByTarget.get(target)
        return generator ? !generatorCanRunGroup(generator, editingGroup) : false
      })
      if (unavailableTargets.length > 0) {
        setError(`Selected generator is not eligible for this group: ${unavailableTargets.join(', ')}`)
        return
      }
      nextOverride.assignment = {
        mode: 'specific_hosts',
        selected_targets: selectedTargets,
        ...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
      }
    }

    setGroupRunOverrides(prev => {
      const next = { ...prev }
      if (hasGroupOverrideValues(nextOverride)) {
        next[editingGroupKey] = nextOverride
      } else {
        delete next[editingGroupKey]
      }
      return next
    })
    setError('')
    setGroupSaveNotice('Saved script settings for this run only. Scenario defaults were not changed.')
    setEditingGroupKey(null)
  }

  const submitSchedule = async () => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) {
	  setError('Choose a scenario before managing schedules.')
	  return
	}
	setScheduleSubmitting(true)
	setError('')
	const payload = {
	  name: scheduleDraft.name.trim(),
	  cron_expr: scheduleDraft.cron_expr.trim(),
	  timezone: scheduleDraft.timezone.trim() || 'UTC',
	  release_id: scheduleDraft.release_id || undefined,
	  run_name_template: scheduleDraft.run_name_template.trim() || undefined,
	  enabled: scheduleDraft.enabled,
	  approval_policy: scheduleDraft.approval_policy,
	  missed_run_policy: scheduleDraft.missed_run_policy,
	  blackout_windows: scheduleDraft.blackout_start_time && scheduleDraft.blackout_end_time ? [{
	    name: 'dashboard-blackout',
	    start_time: scheduleDraft.blackout_start_time,
	    end_time: scheduleDraft.blackout_end_time,
	    timezone: scheduleDraft.timezone.trim() || 'UTC',
	  }] : undefined,
	  availability_windows: scheduleDraft.availability_start_time && scheduleDraft.availability_end_time ? [{
	    name: 'dashboard-availability',
	    days_of_week: [1, 2, 3, 4, 5],
	    start_time: scheduleDraft.availability_start_time,
	    end_time: scheduleDraft.availability_end_time,
	    timezone: scheduleDraft.timezone.trim() || 'UTC',
	  }] : undefined,
	}
	try {
	  if (editingScheduleId) {
	    await api.updateScenarioSchedule(scenario.id, editingScheduleId, payload)
	  } else {
	    await api.createScenarioSchedule(scenario.id, payload)
	  }
	  setEditingScheduleId(null)
	  setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
	  await refreshSchedules(scenario.id)
	  await refreshScheduleHealth()
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to save schedule'))
	} finally {
	  setScheduleSubmitting(false)
	}
  }

  const editSchedule = (schedule: ScenarioSchedule) => {
	setEditingScheduleId(schedule.id)
	setScheduleDraft({
	  name: schedule.name,
	  cron_expr: schedule.cron_expr,
	  timezone: schedule.timezone || 'UTC',
	  release_id: schedule.release_id || '',
	  run_name_template: schedule.run_name_template || '',
	  enabled: schedule.enabled,
	  approval_policy: schedule.approval_policy || 'auto',
	  missed_run_policy: schedule.missed_run_policy || 'skip',
	  blackout_start_time: schedule.blackout_windows?.[0]?.start_time || '',
	  blackout_end_time: schedule.blackout_windows?.[0]?.end_time || '',
	  availability_start_time: schedule.availability_windows?.[0]?.start_time || '',
	  availability_end_time: schedule.availability_windows?.[0]?.end_time || '',
	})
  }

  const toggleSchedule = async (schedule: ScenarioSchedule) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	setError('')
	try {
	  await api.updateScenarioSchedule(scenario.id, schedule.id, {
	    name: schedule.name,
	    cron_expr: schedule.cron_expr,
	    timezone: schedule.timezone || 'UTC',
	    release_id: schedule.release_id || undefined,
	    run_name_template: schedule.run_name_template || undefined,
	    enabled: !schedule.enabled,
	    approval_policy: schedule.approval_policy || 'auto',
	    missed_run_policy: schedule.missed_run_policy || 'skip',
	    blackout_windows: schedule.blackout_windows,
	    availability_windows: schedule.availability_windows,
	  })
	  await refreshSchedules(scenario.id)
	  await refreshScheduleHealth()
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to update schedule'))
	}
  }

  const deleteSchedule = async (scheduleId: string) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	setConfirmDeleteScheduleId(null)
	setError('')
	try {
	  await api.deleteScenarioSchedule(scenario.id, scheduleId)
	  if (editingScheduleId === scheduleId) {
	    setEditingScheduleId(null)
	    setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
	  }
	  await refreshSchedules(scenario.id)
	  await refreshScheduleHealth()
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to delete schedule'))
	}
  }

  const resolveScheduleApproval = async (schedule: ScenarioSchedule, approved: boolean) => {
    if (!canManageTests) {
      setError('Current session cannot manage scenario schedules for this project.')
      return
    }
    if (!scenario || !schedule.pending_approval_id) return
    setError('')
    try {
      if (approved) {
        await api.approveScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Approved from schedule dashboard.')
      } else {
        await api.rejectScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Rejected from schedule dashboard.')
      }
      await refreshSchedules(scenario.id)
      await refreshScheduleHealth()
    } catch (err: unknown) {
      setError(getErrorMessage(err, approved ? 'Unable to approve schedule' : 'Unable to reject schedule'))
    }
  }

  const toggleScheduleOccurrences = async (schedule: ScenarioSchedule) => {
    if (!scenario) return
    if (expandedScheduleId === schedule.id) {
      setExpandedScheduleId(null)
      return
    }
    setExpandedScheduleId(schedule.id)
    if (scheduleOccurrences[schedule.id]) {
      return
    }
    setScheduleOccurrenceLoading(schedule.id)
    setError('')
    try {
      const response = await api.listScenarioScheduleOccurrences(scenario.id, schedule.id)
      setScheduleOccurrences(prev => ({ ...prev, [schedule.id]: response.occurrences || [] }))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load schedule occurrences'))
    } finally {
      setScheduleOccurrenceLoading(null)
    }
  }

  const showStartNow = Boolean(immediatePreflight?.start_now_allowed)
  const showQueueRun = Boolean(immediatePreflight?.queue_allowed)
  const immediateLaunchBlocked = Boolean(immediatePreflight && !showStartNow && !showQueueRun)
  const scheduleAllowed = Boolean(scheduledStartISO && scheduledPreflight?.schedule_allowed)
  const scheduleBlocked = Boolean(scheduledStartISO && scheduledPreflight && !scheduledPreflight.schedule_allowed)
  const scheduleMin = formatDatetimeLocalValue(scheduleClock)

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Run Test</h2>
          <p className="page-subtitle">Choose the release context, scenario, and run setup.</p>
        </div>
        <div className="inline-create-row">
          <button className="btn btn-accent-action" aria-pressed={pageMode === 'launch'} onClick={() => setPageMode('launch')}>Launch Runs</button>
          <button className="btn btn-accent-action" aria-pressed={pageMode === 'schedules'} onClick={() => setPageMode('schedules')}>Schedules</button>
        </div>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}
      {!canManageTests && <div className="settings-error-inline">Current session can view scenarios but cannot create runs or schedules for this project.</div>}

      {pageMode === 'schedules' ? (
        <div className="new-test-layout new-test-schedules-layout">
          <section className={`new-test-schedule-health-card ${scheduleHealthSeverity}`} aria-label="Schedule Health">
            <div className="new-test-schedule-health-header">
              <div>
                <h3>Schedule Health</h3>
                <p>{scheduleHealthSummary}</p>
              </div>
              <span className={`monitor-header-badge ${scheduleHealthSeverity}`}>{scheduleHealthLabel}</span>
            </div>
            <div className="new-test-schedule-health-body">
              <div className="new-test-schedule-health-hero">
                <span>Enabled Schedules</span>
                <strong>{scheduleHealth ? `${formatScheduleHealthCount(scheduleHealth.enabled)}/${formatScheduleHealthCount(scheduleHealth.total)}` : '-'}</strong>
                <p>{scenario ? `${scenario.name} is selected for editing.` : 'Select a scenario to manage its recurring triggers.'}</p>
              </div>
              <div className="new-test-schedule-health-metrics">
                {scheduleHealthMetrics.map(metric => (
                  <div key={metric.label} className={metric.tone ? `is-${metric.tone}` : undefined}>
                    <strong>{formatScheduleHealthCount(metric.value)}</strong>
                    <span>{metric.label}</span>
                  </div>
                ))}
              </div>
              <div className="new-test-schedule-health-context">
                {scheduleHealthContext.map(item => (
                  <div key={item.label} className={item.tone ? `is-${item.tone}` : undefined}>
                    <span>{item.label}</span>
                    <strong>{item.value}</strong>
                  </div>
                ))}
              </div>
              <div className="new-test-schedule-health-readiness">
                <div className={`readiness-item ${scenario ? 'ready' : 'blocked'}`}>
                  <span>{scenario ? 'Ready' : 'Needed'}</span>
                  <p>{scenario ? `${scenario.name} selected` : 'Select a scenario to manage schedules'}</p>
                </div>
                <div className={`readiness-item ${scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'ready' : 'blocked'}`}>
                  <span>{scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'Ready' : 'Needed'}</span>
                  <p>{scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'Schedule form is complete' : 'Enter a name and cron expression'}</p>
                </div>
              </div>
            </div>
          </section>
          <div className="new-test-main">
            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">1</span>
                <div>
                  <h3>Scenario Schedule</h3>
                  <p>Bind a recurring cron expression to a reusable scenario.</p>
                </div>
              </div>
              <div className="settings-group">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Scenario</span>
                    <span className="setting-desc">Recurring runs always execute one stored scenario</span>
                  </div>
                  <select className="settings-input" value={scenarioId} onChange={(event) => { setScenarioId(event.target.value); setEditingScheduleId(null) }}>
                    <option value="">Select scenario</option>
                    {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Schedule Name</span>
                    <span className="setting-desc">Human-friendly label for the recurring trigger</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.name} onChange={(event) => setScheduleDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Nightly smoke" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Cron Expression</span>
                    <span className="setting-desc">Five-field cron format, for example `0 9 * * 1-5`</span>
                    {scheduleDraft.cron_expr.trim() && describeCron(scheduleDraft.cron_expr) && (
                      <span className="setting-desc cron-preview">{describeCron(scheduleDraft.cron_expr)}{scheduleDraft.timezone ? ` (${scheduleDraft.timezone})` : ''}</span>
                    )}
                  </div>
                  <input className="settings-input" value={scheduleDraft.cron_expr} onChange={(event) => setScheduleDraft(prev => ({ ...prev, cron_expr: event.target.value }))} placeholder="0 9 * * 1-5" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Timezone</span>
                    <span className="setting-desc">IANA timezone used for cron and calendar windows</span>
                    <span className="setting-desc schedule-current-utc">Current UTC: {formatUtcScheduleClock(scheduleClock)} UTC</span>
                  </div>
                  <select className="settings-input" value={scheduleDraft.timezone} onChange={(event) => setScheduleDraft(prev => ({ ...prev, timezone: event.target.value }))}>
                    {COMMON_TIMEZONES.map(tz => <option key={tz} value={tz}>{formatTimeZoneOptionLabel(tz, scheduleClock)}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Release Context</span>
                    <span className="setting-desc">Optional release to attach to scheduled runs</span>
                  </div>
                  <select className="settings-input" value={scheduleDraft.release_id} onChange={(event) => setScheduleDraft(prev => ({ ...prev, release_id: event.target.value }))}>
                    <option value="">No release</option>
                    {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Run Name Template</span>
                    <span className="setting-desc">Default run name used when the schedule fires</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.run_name_template} onChange={(event) => setScheduleDraft(prev => ({ ...prev, run_name_template: event.target.value }))} placeholder={scenario?.name || 'Scheduled run'} />
                </div>
                <details className="new-test-disclosure" open>
                  <summary>Governance</summary>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Approval Policy</span>
                      <span className="setting-desc">Require approval before materialization for governed schedules.</span>
                    </div>
                    <select className="settings-input" value={scheduleDraft.approval_policy} onChange={(event) => setScheduleDraft(prev => ({ ...prev, approval_policy: event.target.value }))}>
                      <option value="auto">Auto approve</option>
                      <option value="manual_review">Manual review</option>
                      <option value="high_cost">High cost</option>
                      <option value="off_hours">Off hours</option>
                      <option value="production">Production</option>
                      <option value="quota_burst">Quota burst</option>
                    </select>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Missed Run Policy</span>
                      <span className="setting-desc">Choose how restart reconciliation handles missed cron windows.</span>
                    </div>
                    <select className="settings-input" value={scheduleDraft.missed_run_policy} onChange={(event) => setScheduleDraft(prev => ({ ...prev, missed_run_policy: event.target.value }))}>
                      <option value="skip">Skip missed runs</option>
                      <option value="catch_up_once">Catch up once</option>
                      <option value="catch_up_all">Catch up all</option>
                      <option value="require_operator_decision">Require operator decision</option>
                    </select>
                  </div>
                </details>
                <details className="new-test-disclosure">
                  <summary>Calendar Windows</summary>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Blackout Window</span>
                      <span className="setting-desc">Optional daily no-run range in the schedule timezone</span>
                    </div>
                    <div className="inline-create-row">
                      <input className="settings-input" value={scheduleDraft.blackout_start_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, blackout_start_time: event.target.value }))} placeholder="00:00" />
                      <input className="settings-input" value={scheduleDraft.blackout_end_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, blackout_end_time: event.target.value }))} placeholder="06:00" />
                    </div>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Availability Window</span>
                      <span className="setting-desc">Optional weekday allowed-run range in the schedule timezone</span>
                    </div>
                    <div className="inline-create-row">
                      <input className="settings-input" value={scheduleDraft.availability_start_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, availability_start_time: event.target.value }))} placeholder="09:00" />
                      <input className="settings-input" value={scheduleDraft.availability_end_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, availability_end_time: event.target.value }))} placeholder="17:00" />
                    </div>
                  </div>
                </details>
                <label className="setting-row" style={{ alignItems: 'center', gap: 12 }}>
                  <input type="checkbox" checked={scheduleDraft.enabled} onChange={(event) => setScheduleDraft(prev => ({ ...prev, enabled: event.target.checked }))} />
                  <div className="setting-info">
                    <span className="setting-label">Enabled</span>
                    <span className="setting-desc">Disabled schedules stay saved but do not trigger</span>
                  </div>
                </label>
                <div className="new-test-form-actions">
                  <button className="btn btn-primary" onClick={submitSchedule} disabled={!scenario || scheduleSubmitting || !canManageTests}>{editingScheduleId ? 'Update Schedule' : 'Save Schedule'}</button>
                  {editingScheduleId && <button className="btn btn-secondary" onClick={() => { setEditingScheduleId(null); setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId)) }}>Cancel Edit</button>}
                </div>
              </div>
            </section>

            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">2</span>
                <div>
                  <h3>Recurring Triggers</h3>
                  <p>Review next run times, recent executions, and disable or remove stale schedules.</p>
                </div>
              </div>
              {scheduleLoading ? (
                <div className="new-test-empty">Loading schedules…</div>
              ) : schedules.length > 0 ? (
                <div className="data-table">
                  <table>
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Cron</th>
                        <th>Next Run</th>
                        <th>Last Run</th>
                        <th>Release</th>
                        <th>Policy</th>
                        <th>Status</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {schedules.map(item => {
                        const occurrences = scheduleOccurrences[item.id] || []
                        const lastOccurrence = item.last_occurrence
                        return (
                          <Fragment key={item.id}>
                            <tr>
                              <td className="cell-name">
                                {item.name}
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">rev {item.revision || 1}</span>
                                  {item.owner_display_name && <span className="script-health-token">owner {item.owner_display_name}</span>}
                                  {item.pending_approval_id && <span className="script-health-token warning">approval pending</span>}
                                  {item.conflict_summary && <span className="script-health-token danger">conflict</span>}
                                </div>
                              </td>
                              <td className="cell-mono">
                                {item.cron_expr}
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">{item.timezone || 'UTC'}</span>
                                  {!!item.blackout_windows?.length && <span className="script-health-token warning">{item.blackout_windows.length} blackout</span>}
                                  {!!item.maintenance_windows?.length && <span className="script-health-token warning">{item.maintenance_windows.length} maintenance</span>}
                                  {!!item.availability_windows?.length && <span className="script-health-token healthy">{item.availability_windows.length} availability</span>}
                                </div>
                              </td>
                              <td>{formatScheduleTimestamp(item.next_run_at)}</td>
                              <td>{formatScheduleTimestamp(item.last_run_at)}</td>
                              <td>{releases.find(releaseItem => releaseItem.id === item.release_id)?.name || 'None'}</td>
                              <td>
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">{formatScheduleToken(item.approval_policy || 'auto')}</span>
                                  <span className="script-health-token">{formatScheduleToken(item.missed_run_policy || 'skip')}</span>
                                </div>
                              </td>
                              <td>
                                <div className="schedule-status-stack">
                                  <span className={`script-health-token ${item.enabled ? 'healthy' : 'warning'}`}>{item.enabled ? 'Enabled' : 'Paused'}</span>
                                  {item.next_required_action && <span className={`script-health-token ${scheduleStatusTone(item.next_required_action)}`}>{formatScheduleToken(item.next_required_action)}</span>}
                                  {lastOccurrence?.status && <span className={`script-health-token ${scheduleStatusTone(lastOccurrence.status)}`}>last {formatScheduleToken(lastOccurrence.status)}</span>}
                                  {lastOccurrence?.reason_code && <span className="script-health-token">{formatScheduleToken(lastOccurrence.reason_code)}</span>}
                                </div>
                                {item.conflict_summary && <div className="schedule-governance-detail text-danger">{item.conflict_summary}</div>}
                                {item.last_error && <div className="schedule-governance-detail text-danger">{item.last_error}</div>}
                                {item.governance?.rbac_review?.decision && (
                                  <div className="schedule-governance-detail">
                                    RBAC {formatScheduleToken(item.governance.rbac_review.decision)}
                                    {item.governance.rbac_review.review_source ? ` via ${formatScheduleToken(item.governance.rbac_review.review_source)}` : ''}
                                  </div>
                                )}
                                {item.governance?.quota_review?.decision && (
                                  <div className="schedule-governance-detail">
                                    Quota {formatScheduleToken(item.governance.quota_review.decision)}
                                    {item.governance.quota_review.estimated_peak_vusers ? ` · peak ${item.governance.quota_review.estimated_peak_vusers} VUsers` : ''}
                                  </div>
                                )}
                                {item.governance?.quota_review?.findings?.slice(0, 1).map(finding => (
                                  <div key={finding} className="schedule-governance-detail text-danger">{finding}</div>
                                ))}
                              </td>
                              <td>
                                <div className="inline-create-row">
                                  {item.next_required_action === 'approval_required' && item.pending_approval_id && (
                                    <>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, true)} disabled={!canManageTests}>Approve</button>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, false)} disabled={!canManageTests}>Reject</button>
                                    </>
                                  )}
                                  <button className="btn btn-secondary" onClick={() => toggleScheduleOccurrences(item)}>{expandedScheduleId === item.id ? 'Hide Audit' : 'Audit Trail'}</button>
                                  <button className="btn btn-secondary" onClick={() => editSchedule(item)} disabled={!canManageTests}>Edit</button>
                                  <button className="btn btn-secondary" onClick={() => toggleSchedule(item)} disabled={!canManageTests}>{item.enabled ? 'Pause' : 'Enable'}</button>
                                  <button className="btn btn-secondary" onClick={() => setConfirmDeleteScheduleId(item.id)} disabled={!canManageTests}>Delete</button>
                                </div>
                              </td>
                            </tr>
                            {expandedScheduleId === item.id && (
                              <tr className="no-hover" key={`${item.id}-occurrences`}>
                                <td colSpan={8} style={{ background: 'var(--bg-primary)' }}>
                                  <div className="schedule-occurrence-panel">
                                    <div className="schedule-occurrence-header">
                                      <strong>Occurrence Audit Trail</strong>
                                      <span>{scheduleOccurrenceLoading === item.id ? 'Loading...' : `${occurrences.length} records`}</span>
                                    </div>
                                    {scheduleOccurrenceLoading === item.id ? (
                                      <div className="cell-muted">Loading schedule occurrence history...</div>
                                    ) : occurrences.length === 0 ? (
                                      <div className="cell-muted">No materialized occurrences recorded yet.</div>
                                    ) : (
                                      <div className="data-table">
                                        <table>
                                          <thead>
                                            <tr>
                                              <th>Planned</th>
                                              <th>Materialized</th>
                                              <th>Status</th>
                                              <th>Reason</th>
                                              <th>Test</th>
                                              <th>Approval</th>
                                              <th>Conflict</th>
                                            </tr>
                                          </thead>
                                          <tbody>
                                            {occurrences.map(occurrence => (
                                              <tr key={occurrence.id}>
                                                <td>{formatScheduleTimestamp(occurrence.planned_at)}</td>
                                                <td>{formatScheduleTimestamp(occurrence.materialized_at)}</td>
                                                <td><span className={`script-health-token ${scheduleStatusTone(occurrence.status)}`}>{formatScheduleToken(occurrence.status)}</span></td>
                                                <td>{formatScheduleToken(occurrence.reason_code) || 'None'}</td>
                                                <td className="cell-mono">{occurrence.test_id || 'None'}</td>
                                                <td className="cell-mono">{occurrence.approval_id || 'None'}</td>
                                                <td>{occurrence.conflict_summary || 'None'}</td>
                                              </tr>
                                            ))}
                                          </tbody>
                                        </table>
                                      </div>
                                    )}
                                    {(item.blackout_windows?.length || item.maintenance_windows?.length || item.availability_windows?.length) ? (
                                      <div className="schedule-window-summary">
                                        {item.blackout_windows?.map(window => (
                                          <span key={`blackout-${window.name || window.start_time}`} className="script-health-token warning">Blackout {formatScheduleWindow(window)}</span>
                                        ))}
                                        {item.maintenance_windows?.map(window => (
                                          <span key={`maintenance-${window.name || window.start_time}`} className="script-health-token warning">Maintenance {formatScheduleWindow(window)}</span>
                                        ))}
                                        {item.availability_windows?.map(window => (
                                          <span key={`availability-${window.name || window.start_time}`} className="script-health-token healthy">Availability {formatScheduleWindow(window)}</span>
                                        ))}
                                      </div>
                                    ) : null}
                                  </div>
                                </td>
                              </tr>
                            )}
                          </Fragment>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="new-test-empty">No schedules exist for this scenario yet.</div>
              )}
            </section>
          </div>

        </div>
      ) : (
      <div className="new-test-layout">
        <div className="new-test-main">
          <section className="new-test-step">
            <div className="new-test-step-header">
              <span className={`new-test-step-index${step1Complete ? ' complete' : ''}`}>{step1Complete ? '✓' : '1'}</span>
              <div className="new-test-step-header-content">
                <h3>Select Scenario</h3>
                <p>Choose the reusable workload definition to execute.</p>
              </div>
              {onOpenScenarioBuilder && (
                <button
                  type="button"
                  className="new-test-builder-button new-test-step-header-link"
                  onClick={onOpenScenarioBuilder}
                >
                  Open Scenario Builder
                </button>
              )}
            </div>
            <div className="settings-group">
              <div className="new-test-scenario-browser">
                <div className="new-test-scenario-picker">
                  <div className="new-test-scenario-toolbar">
                    <label className="library-search new-test-scenario-filter">
                      <span>Filter</span>
                      <select
                        aria-label="Filter by release"
                        value={scenarioReleaseFilter}
                        onChange={(event) => setScenarioReleaseFilter(event.target.value)}
                      >
                        <option value="">All releases</option>
                        {scenariosWithoutRelease && <option value={NO_RELEASE_FILTER}>No release association</option>}
                        {scenarioReleaseFilterOptions.map(option => (
                          <option key={option.value} value={option.value}>{option.label}</option>
                        ))}
                      </select>
                    </label>
                    <label className="library-search new-test-scenario-search">
                      <span>Search scenarios</span>
                      <input
                        type="search"
                        value={scenarioQuery}
                        onChange={(event) => setScenarioQuery(event.target.value)}
                        placeholder="Find by scenario, script, protocol, release, or data pool..."
                      />
                    </label>
                    <div className="new-test-scenario-count">
                      {scenarioFiltersActive ? `${filteredScenarios.length} matching scenario${filteredScenarios.length === 1 ? '' : 's'}` : `${scenarios.length} available scenario${scenarios.length === 1 ? '' : 's'}`}
                    </div>
                  </div>
                  {groupSaveNotice && (
                    <div className="new-test-default-notice" role="status">{groupSaveNotice}</div>
                  )}
                  {filteredScenarios.length > 0 ? (
                    <div className="new-test-table-wrap new-test-scenario-table-wrap">
                      <div className="data-table">
                        <table className="new-test-scenario-table">
                          <thead>
                            <tr>
                              <th onClick={() => sortScenarios('name')} className="sortable">Scenario <SortIndicator active={scenarioSortField === 'name'} direction={scenarioSortDirection} /></th>
                              <th onClick={() => sortScenarios('releases')} className="sortable">Releases <SortIndicator active={scenarioSortField === 'releases'} direction={scenarioSortDirection} /></th>
                              <th onClick={() => sortScenarios('last_run_at')} className="sortable">Most Recent Run <SortIndicator active={scenarioSortField === 'last_run_at'} direction={scenarioSortDirection} /></th>
                              <th onClick={() => sortScenarios('vusers')} className="sortable">VUsers <SortIndicator active={scenarioSortField === 'vusers'} direction={scenarioSortDirection} /></th>
                              <th onClick={() => sortScenarios('duration')} className="sortable">Duration <SortIndicator active={scenarioSortField === 'duration'} direction={scenarioSortDirection} /></th>
                              <th onClick={() => sortScenarios('groups')} className="sortable">Groups <SortIndicator active={scenarioSortField === 'groups'} direction={scenarioSortDirection} /></th>
                              <th>Protocols</th>
                            </tr>
                          </thead>
                          <tbody>
                            {sortedScenarios.map(item => {
                              const itemProtocols = scenarioProtocolList(item)
                              const selected = item.id === scenarioId
                              const expanded = item.id === expandedScenarioId
                              const groupsForDisplay = selected ? effectiveScenarioGroups : item.scripts
                              return (
                                <Fragment key={item.id}>
                                  <tr
                                    className={`new-test-scenario-table-row clickable-row ${selected ? 'selected' : ''}`}
                                    onClick={() => selectScenario(item.id)}
                                  >
                                    <td className="cell-name">
                                      <div className="new-test-scenario-name-cell">
                                        <span className="new-test-row-expander" aria-hidden="true">{expanded ? 'v' : '>'}</span>
                                        <span>{item.name}</span>
                                        <span className={`new-test-scenario-card-mode ${item.scenario_mode === 'goal_oriented' ? 'goal' : ''}`}>{formatScenarioModeShort(item.scenario_mode)}</span>
                                      </div>
                                      <div className="new-test-scenario-card-meta">{formatScenarioWorkloadSummary(item)}</div>
                                    </td>
                                    <td>
                                      <div className="new-test-release-token-list">
                                        {(item.associated_releases || []).map((releaseItem, index) => (
                                          <span key={`${releaseItem.id || releaseItem.name}-${index}`} className="script-health-token">{releaseItem.name}</span>
                                        ))}
                                        {(!item.associated_releases || item.associated_releases.length === 0) && <span className="cell-muted">No releases</span>}
                                      </div>
                                    </td>
                                    <td className="cell-muted">{formatDate(item.last_run_at)}</td>
                                    <td>{totalScenarioVUsers(item)}</td>
                                    <td>{item.duration || 'Not set'}</td>
                                    <td>{item.scripts.length}</td>
                                    <td>
                                      <div className="new-test-release-token-list">
                                        {itemProtocols.length ? itemProtocols.map(protocol => <span key={protocol} className="protocol-badge">{protocol}</span>) : <span className="cell-muted">None</span>}
                                      </div>
                                    </td>
                                  </tr>
                                  {expanded && (
                                    <tr className="no-hover new-test-scenario-expanded-row">
                                      <td colSpan={7}>
                                        <div className="new-test-scenario-groups-panel">
                                          <div className="new-test-scenario-groups-header">
                                            <strong>Script Groups</strong>
                                            <span>{formatCount(groupsForDisplay.length, 'group')}</span>
                                          </div>
                                          <div className="data-table">
                                            <table className="new-test-script-table">
                                              <colgroup>
                                                <col className="new-test-script-col-group" />
                                                <col className="new-test-script-col-script" />
                                                <col className="new-test-script-col-protocol" />
                                                <col className="new-test-script-col-workload" />
                                                <col className="new-test-script-col-iterations" />
                                                <col className="new-test-script-col-pacing" />
                                                <col className="new-test-script-col-think" />
                                                <col className="new-test-script-col-log" />
                                                <col className="new-test-script-col-auto" />
                                                <col className="new-test-script-col-error" />
                                                <col className="new-test-script-col-placement" />
                                              </colgroup>
                                              <thead>
                                                <tr>
                                                  <th>Group</th>
                                                  <th>Script</th>
                                                  <th>Protocol</th>
                                                  <th>Workload</th>
                                                  <th>Iterations</th>
                                                  <th>Pacing</th>
                                                  <th>Think Time</th>
                                                  <th>Log Level</th>
                                                  <th>Auto Tx</th>
                                                  <th>Error Handling</th>
                                                  <th>Placement</th>
                                                </tr>
                                              </thead>
                                              <tbody>
                                                {groupsForDisplay.length > 0 ? groupsForDisplay.map((group, index) => {
                                                  const sourceGroup = item.scripts[index] || group
                                                  const key = groupOverrideKey(sourceGroup)
                                                  const override = selected ? groupRunOverrides[key] : undefined
                                                  const runtimeValues = buildGroupRuntimeTableValues(sourceGroup, item, selected ? runDefaultOverrides : buildEmptyRunDefaultOverrides(), override)
                                                  const rowOverridden = selected && hasGroupOverrideValues(override)
                                                  return (
                                                    <tr key={key} className={rowOverridden ? 'new-test-script-row overridden' : 'new-test-script-row'} onClick={() => { if (selected) openGroupEditor(sourceGroup) }}>
                                                      <td className="cell-name">
                                                        <span>{group.name || group.script_name}</span>
                                                        {rowOverridden && <em className="new-test-override-pill">Run override</em>}
                                                      </td>
                                                      <td>{group.script_name || group.script_id}</td>
                                                      <td><span className="protocol-badge">{group.protocol || 'http'}</span></td>
                                                      <td>
                                                        <div className="new-test-workload-stack">
                                                          <strong>{group.vusers} VUsers</strong>
                                                          <span>{group.ramp_up || 'No ramp up'} / {group.ramp_down || 'No ramp down'}</span>
                                                          <span>{group.initialization === 'at_once' ? 'Init at once' : 'Init at runtime'}</span>
                                                        </div>
                                                      </td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.iterations}</td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.pacing}</td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.thinkTime}</td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.logLevel}</td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.autoTransactions}</td>
                                                      <td className="new-test-runtime-cell">{runtimeValues.errorHandling}</td>
                                                      <td className="new-test-runtime-cell">{formatGroupPlacement(override)}</td>
                                                    </tr>
                                                  )
                                                }) : (
                                                  <tr>
                                                    <td colSpan={11} className="cell-muted">No script groups included in this scenario.</td>
                                                  </tr>
                                                )}
                                              </tbody>
                                            </table>
                                          </div>
                                        </div>
                                      </td>
                                    </tr>
                                  )}
                                </Fragment>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  ) : (
                    <div className="new-test-empty compact">No scenarios match this search.</div>
                  )}
                </div>
              </div>
            </div>
          </section>

          <div className="new-test-run-only-callout" role="note">
            Note: Changes on this page only override the default settings for the current run. To change scenario defaults, edit the scenario through the Scenario Editor.
          </div>

          <div className="new-test-run-config-row">
            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className={`new-test-step-index${step2Complete ? ' complete' : ''}`}>{step2Complete ? '✓' : '2'}</span>
                <div>
                  <h3>Run Setup</h3>
                  <p>Name the run and attach it to a release.</p>
                </div>
              </div>
              <div className="settings-group new-test-setup-grid">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Run Name</span>
                    <span className="setting-desc">Shown in results and monitoring</span>
                  </div>
                  <input className="settings-input" value={runName} onChange={event => setRunName(event.target.value)} placeholder={scenario?.name || 'Run name'} />
                </div>
                <div className="setting-row new-test-release-row">
                  <div className="setting-info">
                    <span className="setting-label">Release</span>
                    <span className="setting-desc">Required target for results</span>
                  </div>
                  <select className="settings-input" value={releaseId} onChange={(event) => setReleaseId(event.target.value)}>
                    <option value="">Select release</option>
                    {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                  {showNewRelease ? (
                    <div className="new-test-release-create-row">
                      <input className="settings-input" value={newReleaseName} onChange={event => setNewReleaseName(event.target.value)} placeholder="Release name, e.g. PI30" autoFocus />
                      <button className="btn btn-secondary" onClick={createRelease} disabled={!newReleaseName.trim()}>Add</button>
                      <button className="btn btn-ghost btn-sm" onClick={() => { setShowNewRelease(false); setNewReleaseName('') }}>Cancel</button>
                    </div>
                  ) : (
                    <button className="btn btn-ghost btn-sm btn-accent-action new-test-add-release-link" onClick={() => setShowNewRelease(true)}>+ New Release</button>
                  )}
                </div>
              </div>
            </section>

            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className={`new-test-step-index${step3Complete ? ' complete' : ''}`}>{step3Complete ? '✓' : '3'}</span>
                <div>
                  <h3>Global Run Settings</h3>
                  <p>Run-wide defaults for duration, shakeout, baseline, and runtime behavior.</p>
                </div>
              </div>
              {defaultSaveNotice && (
                <div className="new-test-default-notice" role="status">{defaultSaveNotice}</div>
              )}
              {scenario ? (
                <>
                  <div className="new-test-detail-grid review new-test-default-grid">
                    {globalRunDefaultCards.map(card => (
                      <button
                        key={card.key}
                        type="button"
                        className={`new-test-default-card ${card.isOverridden ? 'overridden' : ''}`}
                        onClick={() => openRunDefaultEditor(card.key)}
                      >
                        <span>{card.label}</span>
                        <strong>{card.value}</strong>
                        {card.isOverridden && <em>Run override</em>}
                      </button>
                    ))}
                  </div>
                  <details
                    className="new-test-runtime-defaults"
                    open={showAllRuntimeDefaults}
                    onToggle={event => setShowAllRuntimeDefaults((event.currentTarget as HTMLDetailsElement).open)}
                  >
                    <summary>
                      <span>Apply runtime defaults to all script groups</span>
                      {runtimeRunDefaultOverrideCount > 0 && <em>{runtimeRunDefaultOverrideCount} override{runtimeRunDefaultOverrideCount === 1 ? '' : 's'}</em>}
                    </summary>
                    <div className="new-test-detail-grid review new-test-default-grid">
                      {runtimeRunDefaultCards.map(card => (
                        <button
                          key={card.key}
                          type="button"
                          className={`new-test-default-card ${card.isOverridden ? 'overridden' : ''}`}
                          onClick={() => openRunDefaultEditor(card.key)}
                        >
                          <span>{card.label}</span>
                          <strong>{card.value}</strong>
                          {card.isOverridden && <em>Run override</em>}
                        </button>
                      ))}
                    </div>
                  </details>
                </>
              ) : (
                <div className="new-test-empty">Select a scenario to review global settings for this run.</div>
              )}
            </section>
          </div>

        </div>

        <aside className="new-test-sidebar-column">
          <section className="new-test-summary">
            <h3>Launch Summary</h3>
            <dl className="new-test-summary-list" aria-label="Run settings summary">
              {launchSummaryItems.map(item => (
                <div key={item.label} className={item.tone === 'warning' ? 'warning' : undefined}>
                  <dt>{item.label}</dt>
                  <dd>{item.value}</dd>
                </div>
              ))}
            </dl>
            <div className="new-test-launch-actions">
              {immediatePreflightLoading && !immediatePreflight && (
                <button className="btn btn-secondary" disabled>Checking Launch</button>
              )}
              {showStartNow && (
                <button className="btn btn-primary" onClick={() => createRun('start_now')} disabled={!canLaunch || immediatePreflightLoading}>Start Now</button>
              )}
              {showQueueRun && (
                <button className="btn btn-secondary" onClick={() => createRun('queue')} disabled={!canLaunch || immediatePreflightLoading}>Create Queued Run</button>
              )}
              {immediateLaunchBlocked && (
                <div className="new-test-launch-feedback warning" role="status">{formatLaunchConflict(immediatePreflight)}</div>
              )}
              <div className="new-test-schedule-launch">
                <label>
                  <span>Scheduled Start</span>
                  <input
                    className="settings-input"
                    type="datetime-local"
                    min={scheduleMin}
                    value={scheduledStartLocal}
                    onChange={event => setScheduledStartLocal(event.target.value)}
                  />
                </label>
                <button className="btn btn-secondary" onClick={() => createRun('schedule')} disabled={!canLaunch || scheduledPreflightLoading || !scheduleAllowed}>Schedule Run</button>
                {scheduledStartLocal && scheduledPreflightLoading && (
                  <div className="new-test-launch-feedback" role="status">Checking scheduled window</div>
                )}
                {scheduledStartLocal && scheduledPreflight?.schedule_allowed && !scheduledPreflightLoading && (
                  <div className="new-test-launch-feedback success" role="status">Scheduled window clear: {formatLaunchWindow(scheduledPreflight)}</div>
                )}
                {scheduleBlocked && !scheduledPreflightLoading && (
                  <div className="new-test-launch-feedback warning" role="status">{formatLaunchConflict(scheduledPreflight)}</div>
                )}
              </div>
            </div>
          </section>
          {scenario && (
            <section className="new-test-profile-card" aria-label="Modeled Load Profile">
              <div className="new-test-profile-header">
                <div>
                  <h3>Modeled Load Profile</h3>
                  <p>Planned VUsers for this run.</p>
                </div>
                <div className="new-test-profile-stat">
                  <strong>{runLoadProfilePreview.peakVUsers}</strong>
                  <span>peak</span>
                </div>
              </div>
              {runLoadProfilePreview.points.length > 0 ? (
                <>
                  <div className="new-test-profile-chart">
                    <ResponsiveContainer width="100%" height={210}>
                      <AreaChart data={runLoadProfilePreview.points} margin={{ top: 10, right: 12, left: 20, bottom: 14 }}>
                        <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
                        <XAxis
                          dataKey="second"
                          tickFormatter={(value) => formatTimeCompact(Number(value))}
                          stroke="var(--text-muted)"
                          fontSize={10}
                          height={38}
                          tickLine={false}
                          label={{ value: 'Elapsed time', position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 10 }}
                        />
                        <YAxis
                          stroke="var(--text-muted)"
                          fontSize={10}
                          width={66}
                          tickLine={false}
                          allowDecimals={false}
                          label={{ value: 'VUsers', angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 10 }}
                        />
                        <Tooltip
                          labelFormatter={(value) => `Time: ${formatTimeCompact(Number(value))}`}
                          contentStyle={{ ...chartTooltipContentStyle, borderRadius: 8, fontSize: '12px' }}
                          labelStyle={chartTooltipLabelStyle}
                          formatter={(value: number, name: string) => [value, name === 'total' ? 'Total VUsers' : name]}
                        />
                        {effectiveScenarioGroups.map((group, index) => (
                          <Area
                            key={`${group.order}-${group.script_id}`}
                            type="monotone"
                            dataKey={`group${index}`}
                            name={group.name || group.script_name || `Group ${index + 1}`}
                            stackId="run-profile"
                            stroke={LOAD_PROFILE_COLORS[index % LOAD_PROFILE_COLORS.length]}
                            fill={LOAD_PROFILE_COLORS[index % LOAD_PROFILE_COLORS.length]}
                            fillOpacity={0.28}
                            isAnimationActive={false}
                          />
                        ))}
                        <Line
                          type="monotone"
                          dataKey="total"
                          name="Total"
                          stroke="var(--text-primary)"
                          strokeWidth={2}
                          dot={false}
                          isAnimationActive={false}
                        />
                        <ReferenceArea
                          x1={runLoadProfilePreview.preBaselineSeconds}
                          x2={runLoadProfilePreview.preBaselineSeconds + runLoadProfilePreview.steadySeconds}
                          fill="var(--accent)"
                          fillOpacity={0.05}
                          strokeOpacity={0}
                        />
                        {runLoadProfilePreview.preBaselineSeconds > 0 && (
                          <ReferenceArea
                            x1={0}
                            x2={runLoadProfilePreview.preBaselineSeconds}
                            fill="var(--warning)"
                            fillOpacity={0.05}
                            strokeOpacity={0}
                          />
                        )}
                        {runLoadProfilePreview.postBaselineSeconds > 0 && (
                          <ReferenceArea
                            x1={runLoadProfilePreview.totalSeconds - runLoadProfilePreview.postBaselineSeconds}
                            x2={runLoadProfilePreview.totalSeconds}
                            fill="var(--warning)"
                            fillOpacity={0.05}
                            strokeOpacity={0}
                          />
                        )}
                      </AreaChart>
                    </ResponsiveContainer>
                  </div>
                  <div className="new-test-profile-legend">
                    {effectiveScenarioGroups.map((group, index) => (
                      <span key={`${group.order}-${group.script_id}`}>
                        <i style={{ background: LOAD_PROFILE_COLORS[index % LOAD_PROFILE_COLORS.length] }} />
                        {group.name || group.script_name || `Group ${index + 1}`}
                      </span>
                    ))}
                  </div>
                  <div className="new-test-profile-meta">
                    <span>{formatTimeCompact(runLoadProfilePreview.totalSeconds)} total</span>
                    <span>{formatTimeCompact(runLoadProfilePreview.steadySeconds)} steady</span>
                  </div>
                </>
              ) : (
                <div className="new-test-empty compact">Select a runnable scenario to preview the load profile.</div>
              )}
            </section>
          )}
        </aside>
      </div>
      )}

      {editingDefaultKey && editingDefaultCard && (
        <div className="modal-overlay" onClick={() => setEditingDefaultKey(null)}>
          <div className="modal run-default-modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Edit {editingDefaultCard.label}</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditingDefaultKey(null)}>Close</button>
            </div>
            <div className="new-test-default-editor">
              <p className="new-test-default-editor-note">Saved changes apply only to the run created from this page. The scenario default remains unchanged.</p>
              <div className="new-test-default-current">
                <span>Scenario default</span>
                <strong>{editingDefaultCard.scenarioValue}</strong>
              </div>
              {renderRunDefaultEditorControl()}
              <div className="modal-actions">
                <button type="button" className="btn btn-secondary" onClick={() => setEditingDefaultKey(null)}>Cancel</button>
                <button type="button" className="btn btn-secondary" onClick={clearRunDefaultOverride}>Use Scenario Default</button>
                <button type="button" className="btn btn-primary" onClick={saveRunDefaultOverride}>Save Run Override</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {editingGroupKey && editingGroup && scenario && (
        <div className="modal-overlay" onClick={() => setEditingGroupKey(null)}>
          <div className="modal run-default-modal run-group-modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Edit {editingGroup.name || editingGroup.script_name || 'Script Group'}</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditingGroupKey(null)}>Close</button>
            </div>
            <div className="new-test-default-editor">
              <p className="new-test-default-editor-note">Saved changes apply only to the run created from this page. The scenario default remains unchanged.</p>
              <div className="new-test-default-current">
                <span>Scenario defaults</span>
                <strong>{editingGroup.vusers} VUsers · {editingGroup.ramp_up || 'No ramp up'} / {editingGroup.ramp_down || 'No ramp down'}</strong>
              </div>
              <div className="new-test-group-editor-sections">
                <section>
                  <h4>Workload</h4>
                  <div className="new-test-default-editor-grid">
                    <label className={changedFieldClass(groupEditChanges.vusers)}>
                      <span>VUsers <ChangedBadge changed={groupEditChanges.vusers} /></span>
                      <input className="settings-input" type="number" min="1" value={groupEditDraft.vusers} onChange={event => setGroupEditDraft(prev => ({ ...prev, vusers: event.target.value }))} />
                    </label>
                    <label className={changedFieldClass(groupEditChanges.initialization)}>
                      <span>Initialization <ChangedBadge changed={groupEditChanges.initialization} /></span>
                      <select className="settings-input" value={groupEditDraft.initialization} onChange={event => setGroupEditDraft(prev => ({ ...prev, initialization: event.target.value as 'at_runtime' | 'at_once' }))}>
                        <option value="at_runtime">At runtime</option>
                        <option value="at_once">At once</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.ramp_up)}>
                      <span>Ramp up <ChangedBadge changed={groupEditChanges.ramp_up} /></span>
                      <input className="settings-input" value={groupEditDraft.ramp_up} onChange={event => setGroupEditDraft(prev => ({ ...prev, ramp_up: event.target.value }))} placeholder="Scenario default" />
                    </label>
                    <label className={changedFieldClass(groupEditChanges.ramp_down)}>
                      <span>Ramp down <ChangedBadge changed={groupEditChanges.ramp_down} /></span>
                      <input className="settings-input" value={groupEditDraft.ramp_down} onChange={event => setGroupEditDraft(prev => ({ ...prev, ramp_down: event.target.value }))} placeholder="Scenario default" />
                    </label>
                  </div>
                </section>
                <section>
                  <h4>Load Generator Sharding <ChangedBadge changed={groupEditChanges.assignment} /></h4>
                  <p className="new-test-inline-note">Saved host placement applies only to this run. Multiple hosts split this group by available capacity.</p>
                  <div className="new-test-placement-toggle" role="radiogroup" aria-label="Load generator placement">
                    <label className={groupEditDraft.assignmentMode === 'auto' ? 'selected' : ''}>
                      <input
                        type="radio"
                        name="group-assignment-mode"
                        checked={groupEditDraft.assignmentMode === 'auto'}
                        onChange={() => setGroupEditDraft(prev => ({ ...prev, assignmentMode: 'auto', selectedTargets: [] }))}
                      />
                      <span>Automatic placement</span>
                    </label>
                    <label className={groupEditDraft.assignmentMode === 'specific_hosts' ? 'selected' : ''}>
                      <input
                        type="radio"
                        name="group-assignment-mode"
                        checked={groupEditDraft.assignmentMode === 'specific_hosts'}
                        onChange={() => setGroupEditDraft(prev => ({ ...prev, assignmentMode: 'specific_hosts' }))}
                      />
                      <span>Specific host sharding</span>
                    </label>
                  </div>
                  {groupEditDraft.assignmentMode === 'specific_hosts' && (
                    <div className="new-test-generator-picker">
                      {generatorInventoryLoading ? (
                        <div className="new-test-empty compact">Loading generator inventory...</div>
                      ) : generatorInventory.length === 0 ? (
                        <div className="new-test-empty compact">{generatorInventoryError || 'No load generators are registered.'}</div>
                      ) : (
                        generatorInventory.map(generator => {
                          const selected = groupEditDraft.selectedTargets.includes(generator.target)
                          const eligible = generatorCanRunGroup(generator, editingGroup)
                          const disabledReason = generatorDisabledReason(generator, editingGroup)
                          return (
                            <label key={generator.target} className={`new-test-generator-option ${selected ? 'selected' : ''} ${!eligible ? 'disabled' : ''}`}>
                              <input
                                type="checkbox"
                                checked={selected}
                                disabled={!eligible && !selected}
                                onChange={event => {
                                  setGroupEditDraft(prev => {
                                    const selectedTargets = event.target.checked
                                      ? Array.from(new Set([...prev.selectedTargets, generator.target]))
                                      : prev.selectedTargets.filter(target => target !== generator.target)
                                    return { ...prev, selectedTargets }
                                  })
                                }}
                              />
                              <span>
                                <strong>{generator.target}</strong>
                                <em>{generator.region || 'default'} · {generator.available_capacity} capacity · {generator.active_vusers} active</em>
                                {disabledReason && <em className="warning">{disabledReason}</em>}
                              </span>
                            </label>
                          )
                        })
                      )}
                      <div className="new-test-placement-summary">
                        <strong>{formatGroupPlacement({ assignment: { mode: groupEditDraft.assignmentMode, selected_targets: groupEditDraft.selectedTargets, ...(groupEditDraft.selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}) } })}</strong>
                        <span>{selectedGeneratorCapacity(generatorInventory, groupEditDraft.selectedTargets)} selected VUser capacity</span>
                      </div>
                    </div>
                  )}
                </section>
                <section>
                  <h4>Runtime Settings</h4>
                  <div className="new-test-default-editor-grid">
                    <label className={changedFieldClass(groupEditChanges.iterations)}>
                      <span>Iterations <ChangedBadge changed={groupEditChanges.iterations} /></span>
                      <input className="settings-input" type="number" min="1" value={groupEditDraft.iterations} onChange={event => setGroupEditDraft(prev => ({ ...prev, iterations: event.target.value }))} placeholder="Duration driven" />
                    </label>
                    <label className={changedFieldClass(groupEditChanges.log_level)}>
                      <span>Log level <ChangedBadge changed={groupEditChanges.log_level} /></span>
                      <select className="settings-input" value={groupEditDraft.log_level} onChange={event => setGroupEditDraft(prev => ({ ...prev, log_level: event.target.value }))}>
                        <option value="disabled">Disabled</option>
                        <option value="standard">Standard</option>
                        <option value="extended">Extended</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.auto_transactions)}>
                      <span>Auto transactions <ChangedBadge changed={groupEditChanges.auto_transactions} /></span>
                      <select className="settings-input" value={groupEditDraft.auto_transactions} onChange={event => setGroupEditDraft(prev => ({ ...prev, auto_transactions: event.target.value }))}>
                        <option value="none">None</option>
                        <option value="per_action">Per Action</option>
                        <option value="per_iteration">Per Iteration</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.pacing)}>
                      <span>Pacing <ChangedBadge changed={groupEditChanges.pacing} /></span>
                      <select className="settings-input" value={groupEditDraft.pacingType} onChange={event => setGroupEditDraft(prev => ({ ...prev, pacingType: event.target.value, pacingAmount: event.target.value === 'none' ? '' : prev.pacingAmount }))}>
                        <option value="">Script timing</option>
                        <option value="none">None</option>
                        <option value="fixed_delay">Fixed delay</option>
                        <option value="fixed_interval">Fixed interval</option>
                        <option value="tps">Target TPS</option>
                      </select>
                    </label>
                    {groupEditDraft.pacingType && groupEditDraft.pacingType !== 'none' && (
                      <label className={changedFieldClass(groupEditChanges.pacing)}>
                        <span>{groupEditDraft.pacingType === 'tps' ? 'Target TPS' : 'Milliseconds'} <ChangedBadge changed={groupEditChanges.pacing} /></span>
                        <input className="settings-input" type="number" min="0" value={groupEditDraft.pacingAmount} onChange={event => setGroupEditDraft(prev => ({ ...prev, pacingAmount: event.target.value }))} />
                      </label>
                    )}
                    <label className={changedFieldClass(groupEditChanges.think_time)}>
                      <span>Think time mean (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                      <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeMs} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkTimeMs: event.target.value }))} placeholder="Script think time" disabled={groupEditDraft.thinkIgnore} />
                    </label>
                    <label className={changedFieldClass(groupEditChanges.think_time, 'checkbox-label new-test-default-checkbox')}>
                      <input type="checkbox" checked={groupEditDraft.thinkIgnore} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkIgnore: event.target.checked }))} />
                      <span>Ignore think time <ChangedBadge changed={groupEditChanges.think_time} /></span>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.continue_on_error)}>
                      <span>Continue on error <ChangedBadge changed={groupEditChanges.continue_on_error} /></span>
                      <select className="settings-input" value={groupEditDraft.continue_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, continue_on_error: event.target.value }))}>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.snapshot_on_error)}>
                      <span>Snapshot on error <ChangedBadge changed={groupEditChanges.snapshot_on_error} /></span>
                      <select className="settings-input" value={groupEditDraft.snapshot_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, snapshot_on_error: event.target.value }))}>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                  </div>
                </section>
                <section>
                  <h4>Browser Runtime</h4>
                  <div className="new-test-default-editor-grid">
                    <label className={changedFieldClass(groupEditChanges.browser_device_profile)}>
                      <span>Device profile <ChangedBadge changed={groupEditChanges.browser_device_profile} /></span>
                      <input className="settings-input" value={groupEditDraft.browser_device_profile} onChange={event => setGroupEditDraft(prev => ({ ...prev, browser_device_profile: event.target.value }))} placeholder="Standard browser" />
                    </label>
                    <label className={changedFieldClass(groupEditChanges.browser_orientation)}>
                      <span>Orientation <ChangedBadge changed={groupEditChanges.browser_orientation} /></span>
                      <select className="settings-input" value={groupEditDraft.browser_orientation} onChange={event => setGroupEditDraft(prev => ({ ...prev, browser_orientation: event.target.value }))}>
                        <option value="">Standard</option>
                        <option value="portrait">Portrait</option>
                        <option value="landscape">Landscape</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.browser_headless)}>
                      <span>Headless <ChangedBadge changed={groupEditChanges.browser_headless} /></span>
                      <select className="settings-input" value={groupEditDraft.browser_headless} onChange={event => setGroupEditDraft(prev => ({ ...prev, browser_headless: event.target.value }))}>
                        <option value="">Standard</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.browser_touch_mode)}>
                      <span>Touch mode <ChangedBadge changed={groupEditChanges.browser_touch_mode} /></span>
                      <select className="settings-input" value={groupEditDraft.browser_touch_mode} onChange={event => setGroupEditDraft(prev => ({ ...prev, browser_touch_mode: event.target.value }))}>
                        <option value="">Standard</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                  </div>
                </section>
              </div>
              <div className="modal-actions">
                <button type="button" className="btn btn-secondary" onClick={() => setEditingGroupKey(null)}>Cancel</button>
                <button type="button" className="btn btn-secondary" onClick={clearGroupOverride}>Use Scenario Defaults</button>
                <button type="button" className="btn btn-primary" onClick={saveGroupOverride} disabled={!hasGroupEditChanges}>Save For This Run</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {confirmDeleteScheduleId && (
        <div className="modal-overlay" onClick={() => setConfirmDeleteScheduleId(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Schedule</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeleteScheduleId(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Permanently delete schedule <strong>{schedules.find(s => s.id === confirmDeleteScheduleId)?.name || 'this schedule'}</strong>? Future runs managed by this schedule will no longer fire.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteScheduleId(null)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={() => deleteSchedule(confirmDeleteScheduleId)}>Delete Schedule</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
