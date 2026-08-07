import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Area, AreaChart, CartesianGrid, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { Application, ApplicationRepository, CapacityTimeslot, DashboardAuthEffectiveAccessResource, DashboardScheduleDefaultsSettings, DraftCapacityEstimate, GeneratorAssignmentSettings, LaunchPreflightResponse, Release, RuntimeSettingsOverride, ScenarioSchedule, ScenarioScheduleOccurrence, ScenarioScheduleWindow, StoredScenario, SystemInventoryGenerator, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatTimeCompact, isValidDurationInput, isValidNonNegativeDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import { chartTooltipContentStyle, chartTooltipLabelStyle, preserveChartItemOrder } from './chartTooltipTheme'
import { listAllCapacityTimeslots } from './schedulingCatalogModel'
import { SortIndicator } from './SortIndicator'
import {
  COMMON_TIMEZONES,
  datetimeLocalToISO,
  describeCron,
  firstLaunchConflict,
  formatDatetimeLocalValue,
  formatLaunchConflict,
  formatLaunchDecision,
  formatLaunchWindow,
  formatNextBlockingEvent,
  formatReservationWindow,
  formatScheduleTimestamp,
  formatScheduleTimestampInZone,
  formatScheduleToken,
  formatScheduleWindow,
  formatTimeslotLabel,
  formatTimeZoneOptionLabel,
  formatUtcScheduleClock,
  reservationStartISO,
  scheduleStatusTone,
} from './newTestScheduleFormat'
import ErrorDiagnostic from './ErrorDiagnostic'
import PageViewToggle from './PageViewToggle'
import GroupPlacementEditor from './GroupPlacementEditor'
import { CronBuilder } from './ScheduleCronBuilder'
import { normalizeRequiredLabels } from './assignmentLabels'
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
  cloneRuntimeSettingsForRun,
  emptyGroupEditDraft,
  errorHandlingSectionIsPinned,
  formatGroupPlacement,
  generatorCanRunGroup,
  getEffectiveResourceBaseline,
  getEffectiveRunDuration,
  GLOBAL_RUN_DEFAULT_KEYS,
  groupOverrideCount,
  groupOverrideKey,
  groupPlacementSummary,
  hasGroupOverrideValues,
  hasOwnKey,
  hasRuntimeSettingsValues,
  mergeRuntimeSettingsForRun,
  setRuntimeAutoTransactions,
  setRuntimeBrowser,
  setRuntimeIterations,
  setRuntimeLogLevel,
  stagedGroupPeakVUsers,
  type GroupEditDraft,
  type GroupRunOverride,
  type RunDefaultEditDraft,
  type RunDefaultKey,
  type RunDefaultOverrides,
  type RunRuntimeOverrideDraft,
  type ScenarioScriptGroup,
} from './newTestRunModel'
import './CreateTestModal.css'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { effectiveGroupVUsers, maxVUsersForRole, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import {
  canonicalThinkTimeType,
  thinkTimeConfigForType,
  thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'
import {
  SECONDS_INPUT_STEP,
} from './runtimeTiming'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import { pacingConfigForSelection, pacingFromEdit, randomPacingDraft } from './pacingEditModel'
import {
  clearNewTestQueueAttempt,
  fingerprintNewTestQueuePayload,
  persistNewTestQueueAttempt,
  readNewTestQueueAttempt,
  type NewTestQueueAttempt,
} from './newTestQueueAttempt'

interface Props {
  activeApplication: Application | null
  applicationRepository?: ApplicationRepository | null
  activeRelease: Release | null
  releases: Release[]
  initialScenarioId?: string
  initialPrefillStartLocal?: string
  onSelectTest: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onCreateRelease: (name: string) => Promise<Release | null>
  onOpenScenarioBuilder?: () => void
}

function createRequestedTestID(): string {
  const cryptoSource = globalThis.crypto
  if (typeof cryptoSource?.randomUUID === 'function') return cryptoSource.randomUUID()

  const bytes = new Uint8Array(16)
  if (typeof cryptoSource?.getRandomValues === 'function') {
    cryptoSource.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const value = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  return `${value.slice(0, 8)}-${value.slice(8, 12)}-${value.slice(12, 16)}-${value.slice(16, 20)}-${value.slice(20)}`
}

function queuedRunStillNeedsStart(test: TestSummary): boolean {
  const status = String(test.status || '').toUpperCase()
  return !status || status === 'QUEUED' || status === 'RESERVED'
}

type QueueRecoveryState = {
  attempt: NewTestQueueAttempt
  status: 'checking' | 'ready' | 'unavailable'
  test?: TestSummary
  message?: string
}

function errorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

type ScheduleWindowDraft = {
  name: string
  days_of_week: number[]
  start_time: string
  end_time: string
  timezone?: string
}

type ScheduleDraft = {
	revision?: number
  name: string
  cron_expr: string
  timezone: string
  release_id: string
  timeslot_id: string
  run_name_template: string
  enabled: boolean
  approval_policy: string
  missed_run_policy: string
  blackout_windows: ScheduleWindowDraft[]
  maintenance_windows: ScheduleWindowDraft[]
  availability_windows: ScheduleWindowDraft[]
  // Optional date-range pause, held as <input type="datetime-local"> values
  // (local wall-clock) and converted to RFC3339 UTC when submitted.
  disabled_from: string
  disabled_until: string
}

const DEFAULT_SCHEDULE_DEFAULTS: Required<DashboardScheduleDefaultsSettings> = {
  approval_policy: 'auto',
  missed_run_policy: 'skip',
  blackout_start_time: '',
  blackout_end_time: '',
  availability_start_time: '',
  availability_end_time: '',
}
const EMPTY_SCENARIOS: StoredScenario[] = []
const EMPTY_BINDABLE_TIMESLOTS: CapacityTimeslot[] = []

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


function totalScenarioVUsers(scenario?: StoredScenario | null) {
  return scenario?.scripts.reduce((sum, script) => sum + effectiveGroupVUsers(script), 0) || 0
}

function totalGroupVUsers(groups: ScenarioScriptGroup[]) {
  return groups.reduce((sum, script) => sum + (script.vusers || 0), 0)
}

function validateRunSetup(scenario: StoredScenario | undefined, effectiveGroups: ScenarioScriptGroup[]): string {
  const rootAssignmentError = validateGeneratorAssignment(scenario?.assignment, 'scenario', 'Scenario assignment')
  if (rootAssignmentError) return rootAssignmentError
  const storedScenarioError = validateAggregateVUserDemand(scenario?.scripts || [], 'Scenario')
  if (storedScenarioError) return storedScenarioError
  const aggregateError = validateAggregateVUserDemand(effectiveGroups)
  if (aggregateError) return aggregateError
  for (let index = 0; index < effectiveGroups.length; index += 1) {
    const group = effectiveGroups[index]
    const label = group.name || group.script_name || `Group ${index + 1}`
    const envelopeError = validateGroupExecutionEnvelope({ role: group.role, protocol: group.protocol, vusers: effectiveGroupVUsers(group), label })
    if (envelopeError) return envelopeError
    const assignmentError = validateGeneratorAssignment(group.assignment, 'group', label)
    if (assignmentError) return assignmentError
  }
  return ''
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

function normalizeScheduleDefaults(defaults?: DashboardScheduleDefaultsSettings | null): Required<DashboardScheduleDefaultsSettings> {
  return {
    ...DEFAULT_SCHEDULE_DEFAULTS,
    ...(defaults || {}),
  }
}

function buildDefaultScheduleDraft(
  scenario?: StoredScenario | null,
  releaseId?: string,
  defaults?: DashboardScheduleDefaultsSettings | null,
): ScheduleDraft {
  const scheduleDefaults = normalizeScheduleDefaults(defaults)
  const blackoutWindows: ScheduleWindowDraft[] = scheduleDefaults.blackout_start_time && scheduleDefaults.blackout_end_time
    ? [{ name: 'Default blackout', days_of_week: [], start_time: scheduleDefaults.blackout_start_time, end_time: scheduleDefaults.blackout_end_time }]
    : []
  const availabilityWindows: ScheduleWindowDraft[] = scheduleDefaults.availability_start_time && scheduleDefaults.availability_end_time
    ? [{ name: 'Business hours', days_of_week: [1, 2, 3, 4, 5], start_time: scheduleDefaults.availability_start_time, end_time: scheduleDefaults.availability_end_time }]
    : []
  return {
    name: scenario ? `${scenario.name} Schedule` : '',
    cron_expr: '0 9 * * 1-5',
    timezone: 'UTC',
    release_id: releaseId || '',
    timeslot_id: '',
    run_name_template: scenario?.name || '',
    enabled: true,
    approval_policy: scheduleDefaults.approval_policy || 'auto',
    missed_run_policy: scheduleDefaults.missed_run_policy || 'skip',
    blackout_windows: blackoutWindows,
    maintenance_windows: [],
    availability_windows: availabilityWindows,
    disabled_from: '',
    disabled_until: '',
  }
}

// scheduleWindowsToPayload drops incomplete windows (no start/end), preserves
// an existing window timezone, and falls back to the schedule timezone for new
// windows. It returns undefined when empty so the field is omitted.
function scheduleWindowsToPayload(windows: ScheduleWindowDraft[], timezone: string) {
  const tz = timezone.trim() || 'UTC'
  const out = windows
    .filter(w => w.start_time && w.end_time)
    .map(w => ({
      name: w.name.trim() || undefined,
      days_of_week: w.days_of_week.length ? w.days_of_week : undefined,
      start_time: w.start_time,
      end_time: w.end_time,
      timezone: w.timezone?.trim() || tz,
    }))
  return out.length ? out : undefined
}

function scheduleWindowsToDraft(windows?: ScenarioScheduleWindow[]): ScheduleWindowDraft[] {
  return (windows || []).map(w => ({
    name: w.name || '',
    days_of_week: w.days_of_week || [],
    start_time: w.start_time || '',
    end_time: w.end_time || '',
    timezone: w.timezone,
  }))
}

const SCHEDULE_WEEKDAYS: { value: number; label: string }[] = [
  { value: 0, label: 'Sun' }, { value: 1, label: 'Mon' }, { value: 2, label: 'Tue' },
  { value: 3, label: 'Wed' }, { value: 4, label: 'Thu' }, { value: 5, label: 'Fri' }, { value: 6, label: 'Sat' },
]

function ScheduleWindowListEditor({ title, description, windows, fallbackTimezone, onChange, disabled }: {
  title: string
  description: string
  windows: ScheduleWindowDraft[]
  fallbackTimezone: string
  onChange: (next: ScheduleWindowDraft[]) => void
  disabled?: boolean
}) {
  const update = (i: number, patch: Partial<ScheduleWindowDraft>) => onChange(windows.map((w, idx) => (idx === i ? { ...w, ...patch } : w)))
  const remove = (i: number) => onChange(windows.filter((_, idx) => idx !== i))
  const add = () => onChange([...windows, { name: '', days_of_week: [], start_time: '09:00', end_time: '17:00', timezone: fallbackTimezone.trim() || 'UTC' }])
  const toggleDay = (i: number, day: number) => {
    const w = windows[i]
    const has = w.days_of_week.includes(day)
    update(i, { days_of_week: has ? w.days_of_week.filter(d => d !== day) : [...w.days_of_week, day].sort((a, b) => a - b) })
  }
  return (
    <div className="setting-row schedule-window-editor">
      <div className="setting-info">
        <span className="setting-label">{title}</span>
        <span className="setting-desc">{description}</span>
      </div>
      <div className="schedule-window-list">
        {windows.length === 0 && <span className="setting-desc schedule-window-empty">None configured.</span>}
        {windows.map((w, i) => (
          <div key={i} className="schedule-window-row">
            <input aria-label={`${title} window ${i + 1} name`} className="settings-input schedule-window-name" placeholder="Name (optional)" value={w.name} disabled={disabled} onChange={e => update(i, { name: e.target.value })} />
            <div className="schedule-window-days" role="group" aria-label={`${title} window ${i + 1} days of week`}>
              {SCHEDULE_WEEKDAYS.map(d => (
                <button type="button" key={d.value} className={`schedule-day-toggle ${w.days_of_week.includes(d.value) ? 'active' : ''}`} disabled={disabled} aria-pressed={w.days_of_week.includes(d.value)} onClick={() => toggleDay(i, d.value)}>{d.label}</button>
              ))}
            </div>
            <input aria-label={`${title} window ${i + 1} start time`} type="time" className="settings-input schedule-window-time" value={w.start_time} disabled={disabled} onChange={e => update(i, { start_time: e.target.value })} />
            <span className="schedule-window-sep">to</span>
            <input aria-label={`${title} window ${i + 1} end time`} type="time" className="settings-input schedule-window-time" value={w.end_time} disabled={disabled} onChange={e => update(i, { end_time: e.target.value })} />
            <select
              aria-label={`${title} window ${i + 1} timezone`}
              className="settings-input schedule-window-timezone"
              value={w.timezone?.trim() || fallbackTimezone.trim() || 'UTC'}
              disabled={disabled}
              onChange={e => update(i, { timezone: e.target.value })}
            >
              {COMMON_TIMEZONES.map(tz => <option key={tz} value={tz}>{tz}</option>)}
            </select>
            <button type="button" className="btn btn-ghost btn-sm" disabled={disabled} onClick={() => remove(i)} aria-label={`Remove ${title.toLowerCase()} window ${i + 1}`}>Remove</button>
          </div>
        ))}
        <button type="button" className="btn btn-secondary btn-sm" disabled={disabled} onClick={add}>+ Add window</button>
      </div>
    </div>
  )
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

function thinkTimeFromEdit(mean: string, ignore: boolean, baseline?: RuntimeSettingsOverride['think_time']): RuntimeSettingsOverride['think_time'] | undefined {
  if (!mean.trim() && !ignore && !baseline?.type) return undefined
  const type = baseline?.type || 'fixed'
  return thinkTimeConfigForType({
    ...(baseline || { type: 'fixed', mean_ms: 0 }),
    ...(thinkTimeUsesMean(type)
      ? { mean_ms: mean.trim() ? Number(mean) : (baseline?.mean_ms ?? 0) }
      : {}),
    ignore,
  }, type)
}

function selectedTargetsChanged(left: string[], right: string[]) {
  const normalize = (values: string[]) => values.filter(Boolean).slice().sort().join('\u0000')
  return normalize(left) !== normalize(right)
}


function requiredLabelsChanged(left: Record<string, string>, right: Record<string, string>) {
  const normalize = (values: Record<string, string>) => Object.entries(normalizeRequiredLabels(values) || {})
    .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey))
    .map(([key, value]) => `${key}=${value}`)
    .join('\u0000')
  return normalize(left) !== normalize(right)
}

function assignmentFromGroupEditDraft(draft: GroupEditDraft): GeneratorAssignmentSettings {
  const requiredLabels = normalizeRequiredLabels(draft.requiredLabels)
  const common = {
    ...(requiredLabels ? { required_labels: requiredLabels } : {}),
    ...(draft.tolerateGeneratorLoss !== undefined ? { tolerate_generator_loss: draft.tolerateGeneratorLoss } : {}),
  }
  if (draft.assignmentMode === 'specific_hosts') {
    const selectedTargets = draft.selectedTargets.filter(Boolean)
    return {
      mode: 'specific_hosts',
      selected_targets: selectedTargets,
      ...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
      ...common,
    }
  }
  if (draft.assignmentMode === 'region_weighted') {
    return { mode: 'region_weighted', region_weights: { ...draft.regionWeights }, ...common }
  }
  if (draft.assignmentMode === 'load_zone_weighted') {
    return { mode: 'load_zone_weighted', load_zone_weights: { ...draft.loadZoneWeights }, ...common }
  }
  return {
    mode: 'auto',
    ...common,
  }
}

function groupEditDraftPatchFromAssignment(assignment?: GeneratorAssignmentSettings): Pick<GroupEditDraft, 'assignmentMode' | 'selectedTargets' | 'requiredLabels' | 'regionWeights' | 'loadZoneWeights' | 'tolerateGeneratorLoss'> {
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels) || {}
  const common = {
    selectedTargets: assignment?.selected_targets || [],
    requiredLabels,
    regionWeights: { ...(assignment?.region_weights || {}) },
    loadZoneWeights: { ...(assignment?.load_zone_weights || {}) },
    tolerateGeneratorLoss: assignment?.tolerate_generator_loss,
  }
  if (assignment?.mode === 'specific_hosts') return { ...common, assignmentMode: 'specific_hosts' }
  if (assignment?.mode === 'region_weighted') return { ...common, assignmentMode: 'region_weighted' }
  if (assignment?.mode === 'load_zone_weighted') return { ...common, assignmentMode: 'load_zone_weighted' }
  if (Object.keys(requiredLabels).length > 0) return { ...common, assignmentMode: 'required_labels', selectedTargets: [] }
  return {
    ...common,
    assignmentMode: 'auto',
    selectedTargets: [],
    requiredLabels: {},
  }
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
      || normalizeDraftText(draft.pacingAmount) !== normalizeDraftText(baseline.pacingAmount)
      || JSON.stringify(draft.pacingConfig || null) !== JSON.stringify(baseline.pacingConfig || null),
    think_time: normalizeDraftText(draft.thinkTimeMs) !== normalizeDraftText(baseline.thinkTimeMs)
      || draft.thinkIgnore !== baseline.thinkIgnore
      || JSON.stringify(draft.thinkTimeConfig || null) !== JSON.stringify(baseline.thinkTimeConfig || null),
    continue_on_error: continueOnErrorChanged,
    snapshot_on_error: snapshotOnErrorChanged,
    error_handling: continueOnErrorChanged || snapshotOnErrorChanged,
    browser_device_profile: browserDeviceProfileChanged,
    browser_headless: browserHeadlessChanged,
    browser_touch_mode: browserTouchModeChanged,
    browser_orientation: browserOrientationChanged,
    browser: browserDeviceProfileChanged || browserHeadlessChanged || browserTouchModeChanged || browserOrientationChanged,
    assignment: draft.assignmentMode !== baseline.assignmentMode
      || selectedTargetsChanged(draft.selectedTargets, baseline.selectedTargets)
      || requiredLabelsChanged(draft.requiredLabels, baseline.requiredLabels)
      || JSON.stringify(draft.regionWeights) !== JSON.stringify(baseline.regionWeights)
      || JSON.stringify(draft.loadZoneWeights) !== JSON.stringify(baseline.loadZoneWeights)
      || draft.tolerateGeneratorLoss !== baseline.tolerateGeneratorLoss,
  }
}

export default function NewTestPage({ activeApplication, applicationRepository, activeRelease, releases, initialScenarioId, initialPrefillStartLocal, onSelectTest, onOpenCommandCenter, onCreateRelease, onOpenScenarioBuilder }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectID = activeProject?.id || ''
  const [pageMode, setPageMode] = useState<'launch' | 'schedules'>('launch')
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const { runGuarded } = useNavigationGuardRunner()
  const [scenarioState, setScenarioState] = useState<{ projectID: string; scenarios: StoredScenario[] }>({ projectID: '', scenarios: [] })
  const scenarios = scenarioState.projectID === activeProjectID ? scenarioState.scenarios : EMPTY_SCENARIOS
  const [scenarioId, setScenarioId] = useState(initialScenarioId || '')
  const [releaseId, setReleaseId] = useState(activeRelease?.id || '')
  const [schedules, setSchedules] = useState<ScenarioSchedule[]>([])
  const [scheduleLoading, setScheduleLoading] = useState(false)
  const [scheduleSubmitting, setScheduleSubmitting] = useState(false)
  const [scheduleClock, setScheduleClock] = useState(() => new Date())
  const [editingScheduleId, setEditingScheduleId] = useState<string | null>(null)
  const [expandedScheduleId, setExpandedScheduleId] = useState<string | null>(null)
  const [scheduleOccurrenceLoading, setScheduleOccurrenceLoading] = useState<string | null>(null)
  const [scheduleOccurrences, setScheduleOccurrences] = useState<Record<string, ScenarioScheduleOccurrence[]>>({})
  const [scheduleDefaults, setScheduleDefaults] = useState<DashboardScheduleDefaultsSettings>(DEFAULT_SCHEDULE_DEFAULTS)
  const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [scheduleDraftBaseline, setScheduleDraftBaseline] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [schedulePreview, setSchedulePreview] = useState<{ occurrences: string[]; error: string; conflictSummary?: string } | null>(null)
  const [runName, setRunName] = useState('')
  const runNameScenarioRef = useRef('')
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
  const [scheduledStartLocal, setScheduledStartLocal] = useState(initialPrefillStartLocal || '')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const [generatorInventoryLoading, setGeneratorInventoryLoading] = useState(false)
  const [generatorInventoryError, setGeneratorInventoryError] = useState('')
  const [timeslotState, setTimeslotState] = useState<{ projectID: string; timeslots: CapacityTimeslot[] }>({ projectID: '', timeslots: [] })
  const bindableTimeslots = timeslotState.projectID === activeProjectID ? timeslotState.timeslots : EMPTY_BINDABLE_TIMESLOTS
  const [selectedTimeslotId, setSelectedTimeslotId] = useState('')
  const newTestDirtyRef = useRef(false)
  const runDefaultOverridesRef = useRef(runDefaultOverrides)
  const groupRunOverridesRef = useRef(groupRunOverrides)
  const createQueuedRunRef = useRef<() => Promise<boolean>>(async () => false)
  const savePendingRunEditorsRef = useRef<() => boolean>(() => true)
  const resetRunSetupRef = useRef<() => void>(() => undefined)
  const saveScheduleRef = useRef<() => Promise<boolean>>(async () => false)
  const resetScheduleSetupRef = useRef<() => void>(() => undefined)
  const scheduleDirtyRef = useRef(false)
  // Session persistence keeps an unresolved Queue Now attempt recoverable after
  // route changes and reloads. Recovery is keyed by stable workspace scope so a
  // changed or reset form cannot accidentally create a second run.
  const queueAttemptRef = useRef<NewTestQueueAttempt | null>(null)
  const [queueRecovery, setQueueRecovery] = useState<QueueRecoveryState | null>(null)
  const [queueRecoveryCheckGeneration, setQueueRecoveryCheckGeneration] = useState(0)
  const scenarioRequestGenerationRef = useRef(0)
  const timeslotRequestGenerationRef = useRef(0)
  const scheduleRequestGenerationRef = useRef(0)
  const scheduleOccurrenceRequestGenerationRef = useRef(0)
  const scheduleScopeGenerationRef = useRef(0)
  const activeProjectIDRef = useRef(activeProjectID)
  activeProjectIDRef.current = activeProjectID
  const activeScenarioIDRef = useRef(scenarioId)
  activeScenarioIDRef.current = scenarioId
  runDefaultOverridesRef.current = runDefaultOverrides
  groupRunOverridesRef.current = groupRunOverrides
  const runDefaultDialogRef = useRef<HTMLDivElement>(null)
  const runGroupDialogRef = useRef<HTMLDivElement>(null)
  const deleteScheduleDialogRef = useRef<HTMLDivElement>(null)

  useModalDialog({
    dialogRef: runDefaultDialogRef,
    enabled: Boolean(editingDefaultKey),
    onClose: () => setEditingDefaultKey(null),
  })
  useModalDialog({
    dialogRef: runGroupDialogRef,
    enabled: Boolean(editingGroupKey),
    onClose: () => setEditingGroupKey(null),
  })
  useModalDialog({
    dialogRef: deleteScheduleDialogRef,
    enabled: Boolean(confirmDeleteScheduleId),
    onClose: () => setConfirmDeleteScheduleId(null),
  })

  useLayoutEffect(() => {
    setScenarioState({ projectID: activeProjectID, scenarios: [] })
    setTimeslotState({ projectID: activeProjectID, timeslots: [] })
    setScenarioId('')
    setSelectedTimeslotId('')
    setEffectiveAuthAccessResources([])
    setError('')
    scheduleRequestGenerationRef.current += 1
    scheduleOccurrenceRequestGenerationRef.current += 1
    scheduleScopeGenerationRef.current += 1
    setSchedules([])
    setScheduleLoading(false)
    setScheduleSubmitting(false)
    setEditingScheduleId(null)
    setExpandedScheduleId(null)
    setScheduleOccurrenceLoading(null)
    setScheduleOccurrences({})
    setConfirmDeleteScheduleId(null)
    const nextScheduleDraft = buildDefaultScheduleDraft()
    setScheduleDraft(nextScheduleDraft)
    setScheduleDraftBaseline(nextScheduleDraft)
  }, [activeProjectID])

  useEffect(() => {
    const applicationId = activeApplication?.id || ''
    if (!activeProjectID || !applicationId) {
      queueAttemptRef.current = null
      setQueueRecovery(null)
      return
    }
    const attempt = readNewTestQueueAttempt({ projectId: activeProjectID, applicationId })
    queueAttemptRef.current = attempt
    if (!attempt) {
      setQueueRecovery(null)
      return
    }

    let cancelled = false
    setQueueRecovery({ attempt, status: 'checking' })
    void api.getTest(attempt.requestedTestId).then(test => {
      if (cancelled) return
      if (test.project_id !== attempt.projectId || test.application_id !== attempt.applicationId) {
        setQueueRecovery({
          attempt,
          status: 'unavailable',
          message: 'The preserved run no longer matches this project and application. Verify it before creating another run.',
        })
        return
      }
      if (!queuedRunStillNeedsStart(test)) {
        clearNewTestQueueAttempt(attempt)
        queueAttemptRef.current = null
        setQueueRecovery(null)
        return
      }
      setQueueRecovery({ attempt, status: 'ready', test })
    }).catch((cause: unknown) => {
      if (cancelled) return
      if (errorStatus(cause) === 404) {
        clearNewTestQueueAttempt(attempt)
        queueAttemptRef.current = null
        setQueueRecovery(null)
        return
      }
      setQueueRecovery({
        attempt,
        status: 'unavailable',
        message: getErrorMessage(cause, 'Unable to verify the preserved queued run'),
      })
    })
    return () => { cancelled = true }
  }, [activeApplication?.id, activeProjectID, api, queueRecoveryCheckGeneration])

  const refresh = useCallback(async () => {
    const projectID = activeProjectID
    const requestGeneration = ++scenarioRequestGenerationRef.current
    if (!projectID) {
      setScenarioState({ projectID: '', scenarios: [] })
      setScenarioId('')
      return
    }
    try {
      const res = await api.listScenarios(projectID)
      if (
        scenarioRequestGenerationRef.current !== requestGeneration
        || activeProjectIDRef.current !== projectID
      ) return
      const next = res.scenarios || []
      setScenarioState({ projectID, scenarios: next })
      setScenarioId(prev => {
        if (next.some(item => item.id === prev)) return prev
        if (initialScenarioId && next.some(item => item.id === initialScenarioId)) return initialScenarioId
        return next[0]?.id || ''
      })
    } catch (err: unknown) {
      if (
        scenarioRequestGenerationRef.current === requestGeneration
        && activeProjectIDRef.current === projectID
      ) setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [activeProjectID, api, initialScenarioId])

  useEffect(() => { refresh() }, [refresh])

  // Approved, not-yet-bound capacity reservations the operator can attach this run to.
  const refreshBindableTimeslots = useCallback(async () => {
    const projectID = activeProjectID
    const requestGeneration = ++timeslotRequestGenerationRef.current
    if (!projectID) {
      setTimeslotState({ projectID: '', timeslots: [] })
      return
    }
    try {
      const nextTimeslots = await listAllCapacityTimeslots(api.listTimeslots, projectID)
      if (
        timeslotRequestGenerationRef.current !== requestGeneration
        || activeProjectIDRef.current !== projectID
      ) return
      setTimeslotState({
        projectID,
        timeslots: nextTimeslots.filter(slot => slot.approval_state === 'approved' && !slot.bound_test_id),
      })
    } catch {
      if (
        timeslotRequestGenerationRef.current === requestGeneration
        && activeProjectIDRef.current === projectID
      ) setTimeslotState({ projectID, timeslots: [] })
    }
  }, [activeProjectID, api])

  useEffect(() => { void refreshBindableTimeslots() }, [refreshBindableTimeslots])
  // Drop the selection if the chosen slot is no longer bindable (approved+unbound) for this project.
  useEffect(() => {
    setSelectedTimeslotId(prev => (prev && bindableTimeslots.some(slot => slot.id === prev) ? prev : ''))
  }, [bindableTimeslots])

  useEffect(() => {
    const timer = window.setInterval(() => setScheduleClock(new Date()), 60_000)
    return () => window.clearInterval(timer)
  }, [])
  useEffect(() => {
    if (!initialScenarioId || !scenarios.some(item => item.id === initialScenarioId)) return
    setScenarioId(prev => prev === initialScenarioId ? prev : initialScenarioId)
  }, [initialScenarioId, scenarios])
  useEffect(() => {
    const emptyDefaults = buildEmptyRunDefaultOverrides()
    runDefaultOverridesRef.current = emptyDefaults
    groupRunOverridesRef.current = {}
    setRunDefaultOverrides(emptyDefaults)
    setGroupRunOverrides({})
    setEditingDefaultKey(null)
    setEditingGroupKey(null)
    setDefaultSaveNotice('')
    setGroupSaveNotice('')
    setShowAllRuntimeDefaults(false)
  }, [scenarioId])
  const defaultReleaseId = activeRelease?.id || releases[0]?.id || ''
  useEffect(() => { setReleaseId(defaultReleaseId) }, [defaultReleaseId])
  useEffect(() => {
    let cancelled = false
    const loadEffectiveAccess = async () => {
      if (!activeProject?.id) {
        setEffectiveAuthAccessResources([])
        return
      }
      try {
        const response = await api.getAuthEffectiveAccess(activeProject.id, {
          domain_id: activeProject?.domain_id,
          application_repository_id: applicationRepository?.id,
        })
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
  }, [activeProject?.id, activeProject?.domain_id, api, applicationRepository?.id])
  useEffect(() => {
    let cancelled = false
    const loadScheduleDefaults = async () => {
      try {
        const response = await api.getSystemSettings()
        if (!cancelled) {
          setScheduleDefaults(normalizeScheduleDefaults(response.schedule_defaults))
        }
      } catch {
        if (!cancelled) {
          setScheduleDefaults(DEFAULT_SCHEDULE_DEFAULTS)
        }
      }
    }
    loadScheduleDefaults()
    return () => {
      cancelled = true
    }
  }, [api])
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
  const replaceScheduleDraft = useCallback((next: ScheduleDraft) => {
    setScheduleDraft(next)
    setScheduleDraftBaseline(next)
  }, [])
  const isCurrentScheduleScope = useCallback((scope: { projectID: string; scenarioID: string; generation: number }) => (
    activeProjectIDRef.current === scope.projectID
    && activeScenarioIDRef.current === scope.scenarioID
    && scheduleScopeGenerationRef.current === scope.generation
  ), [])

  useLayoutEffect(() => {
    scheduleRequestGenerationRef.current += 1
    scheduleOccurrenceRequestGenerationRef.current += 1
    scheduleScopeGenerationRef.current += 1
    setSchedules([])
    setScheduleLoading(false)
    setScheduleSubmitting(false)
    setEditingScheduleId(null)
    setExpandedScheduleId(null)
    setScheduleOccurrenceLoading(null)
    setScheduleOccurrences({})
    setConfirmDeleteScheduleId(null)
    replaceScheduleDraft(buildDefaultScheduleDraft())
  }, [activeProjectID, replaceScheduleDraft, scenarioId])

  // Live "next runs" preview: re-fetch (debounced) from the backend so the
  // shown fire times use the exact production cron evaluator (tz/DST/windows/
  // pause/Quartz specifiers) and cannot diverge from what the scheduler will do.
  useEffect(() => {
    const cron = scheduleDraft.cron_expr.trim()
    if (!scenarioId || !cron || typeof api.previewScenarioSchedule !== 'function') {
      setSchedulePreview(null)
      return
    }
    let cancelled = false
    const handle = window.setTimeout(() => {
      // `name` is required by the request type but ignored by the preview, so a
      // constant keeps the form name out of the dependency/refetch loop.
      api.previewScenarioSchedule(scenarioId, {
        name: 'schedule-preview',
        cron_expr: cron,
        timezone: scheduleDraft.timezone.trim() || 'UTC',
        blackout_windows: scheduleWindowsToPayload(scheduleDraft.blackout_windows, scheduleDraft.timezone),
        maintenance_windows: scheduleWindowsToPayload(scheduleDraft.maintenance_windows, scheduleDraft.timezone),
        availability_windows: scheduleWindowsToPayload(scheduleDraft.availability_windows, scheduleDraft.timezone),
        disabled_from: datetimeLocalToISO(scheduleDraft.disabled_from) || undefined,
        disabled_until: datetimeLocalToISO(scheduleDraft.disabled_until) || undefined,
      }, 5).then(result => {
        if (!cancelled) setSchedulePreview({ occurrences: result.occurrences || [], error: '', conflictSummary: result.conflict_summary || '' })
      }).catch(err => {
        if (!cancelled) setSchedulePreview({ occurrences: [], error: getErrorMessage(err, 'Invalid schedule') })
      })
    }, 400)
    return () => { cancelled = true; window.clearTimeout(handle) }
  }, [api, scenarioId, scheduleDraft.cron_expr, scheduleDraft.timezone, scheduleDraft.blackout_windows, scheduleDraft.maintenance_windows, scheduleDraft.availability_windows, scheduleDraft.disabled_from, scheduleDraft.disabled_until])

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
  const defaultEditBaseline = useMemo(
    () => editingDefaultKey && scenario
      ? buildRunDefaultEditDraft(editingDefaultKey, scenario, runDefaultOverrides)
      : null,
    [editingDefaultKey, runDefaultOverrides, scenario],
  )
  const hasDefaultEditChanges = useMemo(
    () => Boolean(defaultEditBaseline && JSON.stringify(defaultEditDraft) !== JSON.stringify(defaultEditBaseline)),
    [defaultEditBaseline, defaultEditDraft],
  )
  const editingGroup = useMemo(
    () => scenario?.scripts.find(group => groupOverrideKey(group) === editingGroupKey),
    [editingGroupKey, scenario],
  )
  const editingGroupStagePeak = useMemo(
    () => editingGroup ? stagedGroupPeakVUsers(editingGroup) : undefined,
    [editingGroup],
  )
  const groupEditBaseline = useMemo(
    () => editingGroup && scenario
      ? buildGroupEditDraft(editingGroup, scenario, runDefaultOverrides, editingGroupKey ? groupRunOverrides[editingGroupKey] : undefined)
      : emptyGroupEditDraft(),
    [editingGroup, editingGroupKey, groupRunOverrides, runDefaultOverrides, scenario],
  )
  const groupEditChanges = useMemo(
    () => buildGroupEditChanges(groupEditDraft, groupEditBaseline),
    [groupEditBaseline, groupEditDraft],
  )
  const hasGroupEditChanges = useMemo(
    () => Object.values(groupEditChanges).some(Boolean),
    [groupEditChanges],
  )
  const groupEditEnvelopeError = useMemo(() => {
    if (!editingGroup) return ''
    return validateGroupExecutionEnvelope({
      role: editingGroup.role,
      protocol: editingGroup.protocol,
      vusers: editingGroupStagePeak ?? Number(groupEditDraft.vusers),
      label: editingGroup.name || editingGroup.script_name || 'Script group',
    }) || ''
  }, [editingGroup, editingGroupStagePeak, groupEditDraft.vusers])
  const defaultDurationInvalid = editingDefaultKey === 'duration'
    && defaultEditDraft.value.trim().length > 0
    && !isValidDurationInput(defaultEditDraft.value.trim())
  const groupRampUpInvalid = groupEditDraft.ramp_up.trim().length > 0
    && !isValidNonNegativeDurationInput(groupEditDraft.ramp_up.trim())
  const groupRampDownInvalid = groupEditDraft.ramp_down.trim().length > 0
    && !isValidNonNegativeDurationInput(groupEditDraft.ramp_down.trim())
  const generatorByTarget = useMemo(() => new Map(generatorInventory.map(generator => [generator.target, generator])), [generatorInventory])
  const availableLabels = useMemo(() => {
    const labels = new Map<string, Set<string>>()
    generatorInventory.forEach(generator => {
      Object.entries(generator.labels || {}).forEach(([key, value]) => {
        const labelKey = key.trim()
        const labelValue = value.trim()
        if (!labelKey || !labelValue) return
        const values = labels.get(labelKey) || new Set<string>()
        values.add(labelValue)
        labels.set(labelKey, values)
      })
    })
    return Object.fromEntries(Array.from(labels.entries())
      .map(([key, values]) => [key, Array.from(values).sort()]))
  }, [generatorInventory])
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
  const refreshSchedules = useCallback(async (currentScenarioId: string) => {
    const projectID = activeProjectID
    const scope = {
      projectID,
      scenarioID: currentScenarioId,
      generation: scheduleScopeGenerationRef.current,
    }
    const requestGeneration = ++scheduleRequestGenerationRef.current
    if (!currentScenarioId) {
      if (isCurrentScheduleScope(scope)) setSchedules([])
      return
    }
    setScheduleLoading(true)
    try {
      const res = await api.listScenarioSchedules(currentScenarioId)
      if (!isCurrentScheduleScope(scope) || requestGeneration !== scheduleRequestGenerationRef.current) return
      setSchedules(res.schedules || [])
    } catch (err: unknown) {
      if (isCurrentScheduleScope(scope) && requestGeneration === scheduleRequestGenerationRef.current) {
        setError(getErrorMessage(err, 'Unable to load schedules'))
      }
    } finally {
      if (isCurrentScheduleScope(scope) && requestGeneration === scheduleRequestGenerationRef.current) {
        setScheduleLoading(false)
      }
    }
  }, [activeProjectID, api, isCurrentScheduleScope])
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
      { label: 'Host Placement', value: groupPlacementSummary(groupRunOverrides, scenario?.scripts) },
      { label: 'Data Pools', value: dataPoolValue },
      { label: 'Capacity', value: capacityValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Bottleneck', value: bottleneckValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Reservation Decision', value: formatLaunchDecision(immediatePreflight, immediatePreflightLoading), tone: launchBlocked ? 'warning' : 'normal' },
      { label: 'Reserved Window', value: formatReservationWindow(immediatePreflight) },
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
  const canManageProjects = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects'), [effectiveAuthAccessResources])
  const runSetupValidationError = useMemo(
    () => validateRunSetup(scenario, effectiveScenarioGroups),
    [effectiveScenarioGroups, scenario],
  )
  const canLaunch = readiness.every(item => item.ready) && !creating && canManageTests && !runSetupValidationError
  const buildCurrentRunCreatePayload = useCallback((
    currentRunDefaults: RunDefaultOverrides,
    currentGroupOverrides: Record<string, GroupRunOverride>,
  ) => {
    if (!scenario || !activeApplication) return null
    const runtimeSettings = buildRuntimeSettingsForRun(scenario, currentRunDefaults)
    const groupPayloads = buildGroupsForRunPayload(scenario, currentGroupOverrides)
    const duration = getEffectiveRunDuration(scenario, currentRunDefaults)
    return {
      name: runName.trim() || scenario.name,
      duration,
      application_id: activeApplication.id,
      release_id: releaseId || undefined,
      scenario_id: scenario.id,
      project_id: activeProject?.id,
      ...(runtimeSettings !== undefined ? { runtime_settings: runtimeSettings } : {}),
      ...(groupPayloads ? { groups: groupPayloads } : {}),
      ...(hasOwnKey(currentRunDefaults, 'script_shakeout') ? { script_shakeout: Boolean(currentRunDefaults.script_shakeout) } : {}),
      ...(hasOwnKey(currentRunDefaults, 'resource_baseline') ? { resource_baseline: Boolean(currentRunDefaults.resource_baseline) } : {}),
    }
  }, [activeApplication, activeProject?.id, releaseId, runName, scenario])
  const runCreatePayload = useMemo(
    () => buildCurrentRunCreatePayload(runDefaultOverrides, groupRunOverrides),
    [buildCurrentRunCreatePayload, groupRunOverrides, runDefaultOverrides],
  )
  const scheduledStartISO = useMemo(() => datetimeLocalToISO(scheduledStartLocal), [scheduledStartLocal])
  const runNameScenarioIdentity = scenario ? `${activeProjectID}:${scenario.id}` : ''
  const runNameScenarioDefault = scenario?.name || ''

  useEffect(() => {
    if (runNameScenarioRef.current === runNameScenarioIdentity) return
    runNameScenarioRef.current = runNameScenarioIdentity
    setRunName(runNameScenarioDefault)
  }, [runNameScenarioDefault, runNameScenarioIdentity])
  useEffect(() => {
    if (!scenarioId) {
      setExpandedScenarioId('')
    }
  }, [scenarioId])
  useEffect(() => {
    void refreshSchedules(scenarioId)
  }, [refreshSchedules, scenarioId])
  useEffect(() => {
    if (!editingScheduleId) {
      replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
    }
  }, [editingScheduleId, releaseId, replaceScheduleDraft, scenario, scheduleDefaults])

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
  const step2Complete = Boolean(activeApplication && release && runName.trim())
  const step3Complete = step1Complete

  const createRelease = async () => {
    const name = newReleaseName.trim()
    if (!name) return
    const created = await onCreateRelease(name)
    if (created) setReleaseId(created.id)
    setNewReleaseName('')
  }

  const selectScenario = (nextScenarioId: string) => {
    if (nextScenarioId === scenarioId) {
      setExpandedScenarioId(prev => prev === nextScenarioId ? '' : nextScenarioId)
      return
    }
    runGuarded(() => {
      setScenarioId(nextScenarioId)
      setExpandedScenarioId(nextScenarioId)
      setRunName('')
    })
  }

  const sortScenarios = (field: ScenarioSortField) => {
    if (scenarioSortField === field) {
      setScenarioSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
      return
    }
    setScenarioSortField(field)
    setScenarioSortDirection(field === 'last_run_at' || field === 'vusers' ? 'desc' : 'asc')
  }

  const scenarioAriaSort = (field: ScenarioSortField): 'ascending' | 'descending' | 'none' => (
    scenarioSortField === field ? (scenarioSortDirection === 'asc' ? 'ascending' : 'descending') : 'none'
  )

  const createRun = async (intent: 'reserve' | 'queue' | 'schedule', options: { navigate?: boolean } = {}): Promise<boolean> => {
    const navigate = options.navigate ?? true
    const currentRunDefaults = runDefaultOverridesRef.current
    const currentGroupOverrides = groupRunOverridesRef.current
    const currentRunCreatePayload = buildCurrentRunCreatePayload(currentRunDefaults, currentGroupOverrides)
    const currentEffectiveGroups = buildEffectiveScenarioGroups(scenario, currentGroupOverrides)
    const currentRunSetupValidationError = validateRunSetup(scenario, currentEffectiveGroups)
    if (!canManageTests) {
      setError('Current session cannot create, reserve, or start test runs for this project.')
      return false
    }
    if (!scenario || !releaseId || !activeApplication || !currentRunCreatePayload) {
      setError('Choose a release and scenario before creating a test.')
      return false
    }
    if (currentRunSetupValidationError) {
      setError(currentRunSetupValidationError)
      return false
    }
    if (intent === 'schedule' && !scheduledStartISO) {
      setError('Choose a scheduled start time before scheduling a run.')
      return false
    }
    setCreating(true)
    setError('')
    try {
      const payload = intent === 'schedule'
        ? { ...currentRunCreatePayload, release_id: releaseId, scheduled_start_at: scheduledStartISO }
        : intent === 'reserve'
          ? { ...currentRunCreatePayload, release_id: releaseId, reserve_launch_window: true, scheduled_start_at: reservationStartISO(immediatePreflight) }
          : { ...currentRunCreatePayload, release_id: releaseId }
      // Bind the reserved timeslot atomically as part of creation (strict): if the
      // timeslot can't be bound the create fails, so a run is never left unprotected.
      const createPayload = selectedTimeslotId ? { ...payload, timeslot_id: selectedTimeslotId } : payload
      let test: TestSummary | null = null
      let queueAttempt: NewTestQueueAttempt | null = null
      let recoverQueuedTest: (() => Promise<TestSummary | null>) | null = null
      if (intent === 'queue') {
        const payloadFingerprint = await fingerprintNewTestQueuePayload(createPayload)
        const queueScope = { projectId: activeProjectID, applicationId: activeApplication.id }
        const existingAttempt = queueAttemptRef.current || readNewTestQueueAttempt(queueScope)
        if (existingAttempt) {
          try {
            const existingRun = await api.getTest(existingAttempt.requestedTestId)
            const sameScope = existingRun.id === existingAttempt.requestedTestId
              && existingRun.project_id === existingAttempt.projectId
              && existingRun.application_id === existingAttempt.applicationId
            if (!sameScope) {
              setQueueRecovery({
                attempt: existingAttempt,
                status: 'unavailable',
                message: 'The preserved run no longer matches this project and application. Verify it before creating another run.',
              })
              setError('A previous queued-run attempt must be resolved before another queued run can be created.')
              return false
            }
            if (queuedRunStillNeedsStart(existingRun)) {
              queueAttemptRef.current = existingAttempt
              setQueueRecovery({ attempt: existingAttempt, status: 'ready', test: existingRun })
              setError('A previous queued run is still waiting to start. Resume it or explicitly choose Create another run.')
              return false
            }
            clearNewTestQueueAttempt(existingAttempt)
            queueAttemptRef.current = null
          } catch (recoveryError: unknown) {
            if (errorStatus(recoveryError) !== 404) {
              queueAttemptRef.current = existingAttempt
              setQueueRecovery({
                attempt: existingAttempt,
                status: 'unavailable',
                message: getErrorMessage(recoveryError, 'Unable to verify the preserved queued run'),
              })
              setError('The previous queued-run attempt could not be verified. Retry verification or explicitly choose Create another run.')
              return false
            }
            clearNewTestQueueAttempt(existingAttempt)
            queueAttemptRef.current = null
          }
        }
        queueAttempt = queueAttemptRef.current
        if (!queueAttempt) {
          queueAttempt = persistNewTestQueueAttempt(
            payloadFingerprint,
            createRequestedTestID(),
            queueScope,
          )
        }
        queueAttemptRef.current = queueAttempt
        const requestedTestID = queueAttempt.requestedTestId

        const recoverCreatedTest = async () => {
          const recovered = await api.getTest(requestedTestID)
          const sameScope = recovered.id === requestedTestID
            && (!createPayload.project_id || recovered.project_id === createPayload.project_id)
            && (!createPayload.application_id || recovered.application_id === createPayload.application_id)
            && (!createPayload.scenario_id || recovered.scenario_id === createPayload.scenario_id)
            && (!createPayload.release_id || recovered.release_id === createPayload.release_id)
          return sameScope ? recovered : null
        }
        recoverQueuedTest = recoverCreatedTest

        try {
          test = await api.createTest({ ...createPayload, requested_test_id: requestedTestID })
        } catch (createError: unknown) {
          // The create response may have been lost after the durable row was
          // committed. Recover only the exact client-generated ID and verify its
          // immutable scope before treating it as this attempt.
          try {
            test = await recoverCreatedTest()
          } catch {
            test = null
          }
          if (!test) throw createError
        }
      } else {
        test = await api.createTest(createPayload)
      }
      if (!test) throw new Error('Unable to recover the created queued run')
      const finishTimeslotBinding = () => {
        if (!selectedTimeslotId) return
        setSelectedTimeslotId('')
        void refreshBindableTimeslots()
      }
      if (intent !== 'queue') finishTimeslotBinding()
      if (intent === 'queue') {
        if (!queueAttempt || !recoverQueuedTest) throw new Error('Unable to recover the queued-run attempt')
        const resolvedQueueAttempt = queueAttempt
        const resolveQueueAttempt = () => {
          clearNewTestQueueAttempt(resolvedQueueAttempt)
          if (queueAttemptRef.current?.requestedTestId === resolvedQueueAttempt.requestedTestId) {
            queueAttemptRef.current = null
          }
          setQueueRecovery(null)
          finishTimeslotBinding()
        }
        if (!queuedRunStillNeedsStart(test)) {
          resolveQueueAttempt()
          if (navigate) (onOpenCommandCenter || onSelectTest)(test.id)
          return true
        }
        try {
          await api.startTest(test.id)
        } catch (startError: unknown) {
          let recoveredAfterStart: TestSummary | null = null
          try {
            recoveredAfterStart = await recoverQueuedTest()
          } catch {
            // The known created run remains the recovery anchor while its latest
            // state cannot be read.
          }
          if (recoveredAfterStart && !queuedRunStillNeedsStart(recoveredAfterStart)) {
            resolveQueueAttempt()
            if (navigate) (onOpenCommandCenter || onSelectTest)(recoveredAfterStart.id)
            return true
          }
          // Creation is already durable. Hand the known run to the execution
          // workspace even if start was rejected or its acknowledgement was
          // lost; retrying Queue Now must never create a second run.
          setQueueRecovery({ attempt: resolvedQueueAttempt, status: 'ready', test: recoveredAfterStart || test })
          setError(`${getErrorMessage(startError, 'Unable to start the created run')} The queued run was preserved for recovery.`)
          if (navigate) (onOpenCommandCenter || onSelectTest)(test.id)
          return true
        }
        resolveQueueAttempt()
        if (navigate) (onOpenCommandCenter || onSelectTest)(test.id)
        return true
      }
      if (navigate && intent === 'reserve') {
        ;(onOpenCommandCenter || onSelectTest)(test.id)
        return true
      }
      if (navigate) onSelectTest(test.id)
      return true
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to create test'))
      return false
    } finally {
      setCreating(false)
    }
  }

  const resolveRecoveredQueueAttempt = useCallback((attempt: NewTestQueueAttempt) => {
    clearNewTestQueueAttempt(attempt)
    if (queueAttemptRef.current?.requestedTestId === attempt.requestedTestId) {
      queueAttemptRef.current = null
    }
    setQueueRecovery(null)
    setSelectedTimeslotId('')
    void refreshBindableTimeslots()
  }, [refreshBindableTimeslots])

  const resumeRecoveredQueuedRun = async () => {
    const recovery = queueRecovery
    if (!recovery?.test || recovery.status !== 'ready') return
    setCreating(true)
    setError('')
    try {
      await api.startTest(recovery.test.id)
      resolveRecoveredQueueAttempt(recovery.attempt)
      ;(onOpenCommandCenter || onSelectTest)(recovery.test.id)
    } catch (startError: unknown) {
      let latest: TestSummary | null = null
      try {
        latest = await api.getTest(recovery.test.id)
      } catch {
        // Preserve the known queued row and its recovery token when verification
        // is temporarily unavailable.
      }
      if (latest && !queuedRunStillNeedsStart(latest)) {
        resolveRecoveredQueueAttempt(recovery.attempt)
        ;(onOpenCommandCenter || onSelectTest)(latest.id)
        return
      }
      setQueueRecovery({ ...recovery, test: latest || recovery.test })
      setError(`${getErrorMessage(startError, 'Unable to start the preserved queued run')} The same run remains available for recovery.`)
    } finally {
      setCreating(false)
    }
  }

  const createAnotherQueuedRun = () => {
    const attempt = queueRecovery?.attempt || queueAttemptRef.current
    if (attempt) clearNewTestQueueAttempt(attempt)
    queueAttemptRef.current = null
    setQueueRecovery(null)
    setError('The recovery marker was cleared. The existing queued run was not deleted; review it in Results before submitting another run.')
  }

  const resetRunSetup = useCallback(() => {
    const emptyDefaults = buildEmptyRunDefaultOverrides()
    runDefaultOverridesRef.current = emptyDefaults
    groupRunOverridesRef.current = {}
    setRunName(scenario?.name || '')
    setReleaseId(defaultReleaseId)
    setNewReleaseName('')
    setShowNewRelease(false)
    setRunDefaultOverrides(emptyDefaults)
    setGroupRunOverrides({})
    setEditingDefaultKey(null)
    setEditingGroupKey(null)
    setDefaultSaveNotice('')
    setGroupSaveNotice('')
    setShowAllRuntimeDefaults(false)
    setSelectedTimeslotId('')
    setScheduledStartLocal('')
  }, [defaultReleaseId, scenario?.name])

  const resetScheduleSetup = useCallback(() => {
    setEditingScheduleId(null)
    setExpandedScheduleId(null)
    setScheduleOccurrenceLoading(null)
    replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
  }, [releaseId, replaceScheduleDraft, scenario, scheduleDefaults])

  const runNameDirty = Boolean(runName.trim() && runName.trim() !== (scenario?.name || '').trim())
  const releaseDirty = Boolean(releaseId && releaseId !== defaultReleaseId)
  const pendingDefaultDirty = Boolean(editingDefaultKey && hasDefaultEditChanges)
  const pendingGroupDirty = Boolean(editingGroupKey && hasGroupEditChanges)
  const newTestDirty = pageMode === 'launch' && Boolean(
    runNameDirty
    || releaseDirty
    || runOverridesCount > 0
    || selectedTimeslotId
    || scheduledStartLocal
    || newReleaseName.trim()
    || pendingDefaultDirty
    || pendingGroupDirty
  )
  const scheduleDraftDirty = pageMode === 'schedules'
    && JSON.stringify(scheduleDraft) !== JSON.stringify(scheduleDraftBaseline)
  createQueuedRunRef.current = async () => {
    if (!savePendingRunEditorsRef.current()) return false
    const created = await createRun('queue', { navigate: false })
    if (created) resetRunSetup()
    return created
  }
  resetRunSetupRef.current = resetRunSetup
  resetScheduleSetupRef.current = resetScheduleSetup
  newTestDirtyRef.current = newTestDirty
  scheduleDirtyRef.current = scheduleDraftDirty
  const newTestGuard = useMemo<NavigationGuard>(() => ({
    id: 'new-test-run-setup',
    label: 'New Test setup or schedule',
    isDirty: () => newTestDirtyRef.current || scheduleDirtyRef.current,
    save: () => scheduleDirtyRef.current ? saveScheduleRef.current() : createQueuedRunRef.current(),
    discard: () => {
      if (scheduleDirtyRef.current) resetScheduleSetupRef.current()
      if (newTestDirtyRef.current) resetRunSetupRef.current()
    },
  }), [])
  useNavigationGuard(newTestGuard, newTestDirty || scheduleDraftDirty)

  const openRunDefaultEditor = (key: RunDefaultKey) => {
    if (!scenario) return
    setDefaultEditDraft(buildRunDefaultEditDraft(key, scenario, runDefaultOverrides))
    setEditingDefaultKey(key)
  }

  const commitRunDefaultOverrides = (next: RunDefaultOverrides) => {
    runDefaultOverridesRef.current = next
    setRunDefaultOverrides(next)
  }

  const clearRunDefaultOverride = () => {
    if (!editingDefaultKey || !editingDefaultCard) return
    const key = editingDefaultKey
    const next: RunDefaultOverrides = {
      ...runDefaultOverridesRef.current,
      runtime_settings: { ...runDefaultOverridesRef.current.runtime_settings },
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
    commitRunDefaultOverrides(next)
    setDefaultSaveNotice(`${editingDefaultCard.label} now uses the scenario default. Scenario defaults were not changed.`)
    setEditingDefaultKey(null)
  }

  const saveRunDefaultOverride = (): boolean => {
    if (!editingDefaultKey || !editingDefaultCard) return false
    const key = editingDefaultKey
    const next: RunDefaultOverrides = {
      ...runDefaultOverridesRef.current,
      runtime_settings: { ...runDefaultOverridesRef.current.runtime_settings },
    }
    const setRuntimeOverride = <K extends keyof RunRuntimeOverrideDraft>(field: K, value: RunRuntimeOverrideDraft[K]) => {
      next.runtime_settings[field] = value
    }
    if (key === 'duration') {
      const duration = defaultEditDraft.value.trim()
      if (!duration) {
        setError('Duration is required for a run override.')
        return false
      }
      if (!isValidDurationInput(duration)) {
        setError('Run duration must be a positive Go duration such as 30s, 5m, or 1h30m.')
        return false
      }
      next.duration = duration
    } else if (key === 'iterations') {
      setRuntimeOverride('iterations', defaultEditDraft.value ? Number(defaultEditDraft.value) : null)
    } else if (key === 'log_level') {
      setRuntimeOverride('log_level', defaultEditDraft.value || null)
    } else if (key === 'auto_transactions') {
      setRuntimeOverride('auto_transactions', defaultEditDraft.value || null)
    } else if (key === 'pacing') {
      setRuntimeOverride('pacing', pacingFromEdit(defaultEditDraft.pacingType, defaultEditDraft.pacingAmount, defaultEditDraft.pacingConfig) || null)
    } else if (key === 'think_time') {
      setRuntimeOverride('think_time', thinkTimeFromEdit(defaultEditDraft.value, defaultEditDraft.thinkIgnore, defaultEditDraft.thinkTimeConfig) || null)
    } else if (key === 'continue_on_error') {
      setRuntimeOverride('continue_on_error', defaultEditDraft.value === '' ? null : defaultEditDraft.value === 'true')
    } else if (key === 'snapshot_on_error') {
      setRuntimeOverride('snapshot_on_error', defaultEditDraft.value === '' ? null : defaultEditDraft.value === 'true')
    } else if (key === 'script_shakeout') {
      next.script_shakeout = defaultEditDraft.value === 'true'
    } else if (key === 'resource_baseline') {
      next.resource_baseline = defaultEditDraft.value === 'true'
    }
    commitRunDefaultOverrides(next)
    if (!GLOBAL_RUN_DEFAULT_KEYS.has(key)) {
      setShowAllRuntimeDefaults(true)
    }
    setError('')
    setDefaultSaveNotice(`Saved ${editingDefaultCard.label} for this run only. Scenario defaults were not changed.`)
    setEditingDefaultKey(null)
    return true
  }

  const renderRunDefaultEditorControl = () => {
    if (!editingDefaultKey) return null
    if (editingDefaultKey === 'duration') {
      return (
        <label>
          <span>Run duration</span>
          <input
            className="settings-input"
            value={defaultEditDraft.value}
            aria-invalid={defaultDurationInvalid}
            aria-describedby={defaultDurationInvalid ? 'new-test-run-duration-error' : undefined}
            onChange={event => { setError(''); setDefaultEditDraft(prev => ({ ...prev, value: event.target.value })) }}
            placeholder="30m0s"
          />
          {defaultDurationInvalid && <small id="new-test-run-duration-error" className="new-test-group-field-error">Use a duration such as 30s, 5m, or 1h30m.</small>}
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
            <select className="settings-input" value={defaultEditDraft.pacingType} onChange={event => {
              const type = event.target.value
              setDefaultEditDraft(prev => ({
                ...prev,
                pacingType: type,
                pacingAmount: type === 'none' || type.startsWith('random_') ? '' : prev.pacingAmount,
                pacingConfig: pacingConfigForSelection(type, prev.pacingConfig, prev.pacingAmount),
              }))
            }}>
              <option value="">Script default</option>
              <option value="none">None</option>
              <option value="fixed_delay">Fixed delay</option>
              <option value="random_delay">Random delay</option>
              <option value="fixed_interval">Fixed interval</option>
              <option value="random_interval">Random interval</option>
              <option value="tps">Target TPS</option>
            </select>
          </label>
          {['fixed_delay', 'fixed_interval', 'tps'].includes(defaultEditDraft.pacingType) && (
            <label>
              <span>{defaultEditDraft.pacingType === 'tps' ? 'Target TPS' : 'Seconds'}</span>
              <input className="settings-input" type="number" min="0" step={defaultEditDraft.pacingType === 'tps' ? undefined : SECONDS_INPUT_STEP} value={defaultEditDraft.pacingAmount} onChange={event => setDefaultEditDraft(prev => ({ ...prev, pacingAmount: event.target.value }))} placeholder="0" />
            </label>
          )}
          {defaultEditDraft.pacingType === 'random_delay' && (
            <>
              <label>
                <span>Minimum delay (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input"
                  valueMs={defaultEditDraft.pacingConfig?.min_delay_ms}
                  emptyWhenUndefined
                  onChangeMs={minDelayMs => setDefaultEditDraft(prev => ({
                    ...prev,
                    pacingConfig: {
                      ...randomPacingDraft('random_delay', prev.pacingConfig, prev.pacingAmount),
                      min_delay_ms: minDelayMs,
                    },
                  }))} />
              </label>
              <label>
                <span>Maximum delay (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input"
                  valueMs={defaultEditDraft.pacingConfig?.max_delay_ms}
                  emptyWhenUndefined
                  onChangeMs={maxDelayMs => setDefaultEditDraft(prev => ({
                    ...prev,
                    pacingConfig: {
                      ...randomPacingDraft('random_delay', prev.pacingConfig, prev.pacingAmount),
                      max_delay_ms: maxDelayMs,
                    },
                  }))} />
              </label>
            </>
          )}
          {defaultEditDraft.pacingType === 'random_interval' && (
            <>
              <label>
                <span>Minimum interval (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input"
                  valueMs={defaultEditDraft.pacingConfig?.min_interval_ms}
                  emptyWhenUndefined
                  onChangeMs={minIntervalMs => setDefaultEditDraft(prev => ({
                    ...prev,
                    pacingConfig: {
                      ...randomPacingDraft('random_interval', prev.pacingConfig, prev.pacingAmount),
                      min_interval_ms: minIntervalMs,
                    },
                  }))} />
              </label>
              <label>
                <span>Maximum interval (seconds)</span>
                <SecondsFromMillisecondsInput className="settings-input"
                  valueMs={defaultEditDraft.pacingConfig?.max_interval_ms}
                  emptyWhenUndefined
                  onChangeMs={maxIntervalMs => setDefaultEditDraft(prev => ({
                    ...prev,
                    pacingConfig: {
                      ...randomPacingDraft('random_interval', prev.pacingConfig, prev.pacingAmount),
                      max_interval_ms: maxIntervalMs,
                    },
                  }))} />
              </label>
            </>
          )}
        </div>
      )
    }
    if (editingDefaultKey === 'think_time') {
      const thinkType = canonicalThinkTimeType(defaultEditDraft.thinkTimeConfig?.type)
      const updateThinkConfig = (patch: Partial<NonNullable<RuntimeSettingsOverride['think_time']>>) => {
        setDefaultEditDraft(prev => ({
          ...prev,
          thinkTimeConfig: thinkTimeConfigForType({
            ...prev.thinkTimeConfig,
            ...patch,
          }, prev.thinkTimeConfig?.type || 'fixed'),
        }))
      }
      return (
        <div className="new-test-default-editor-grid">
          <label>
            <span>Think time mode</span>
            <select className="settings-input" value={thinkType} onChange={event => {
              const type = event.target.value
              setDefaultEditDraft(prev => ({
                ...prev,
                value: type === 'recorded' ? '' : prev.value,
                thinkIgnore: type ? prev.thinkIgnore : false,
                thinkTimeConfig: type
                  ? thinkTimeConfigForType({
                      ...prev.thinkTimeConfig,
                      ...(prev.value.trim() !== '' ? { mean_ms: Number(prev.value) } : {}),
                    }, type)
                  : undefined,
              }))
            }}>
              <option value="">Script default</option>
              <option value="none">None</option>
              <option value="recorded">Replay think time as recorded</option>
              <option value="fixed">Fixed</option>
              <option value="gaussian">Gaussian</option>
              <option value="uniform">Uniform</option>
            </select>
          </label>
          {thinkTimeUsesMean(thinkType) && (
            <label>
              <span>Think time mean (ms)</span>
              <input className="settings-input" type="number" min="0" value={defaultEditDraft.value} onChange={event => setDefaultEditDraft(prev => ({
                ...prev,
                value: event.target.value,
                thinkTimeConfig: prev.thinkTimeConfig
                  ? { ...prev.thinkTimeConfig, mean_ms: event.target.value === '' ? undefined : Number(event.target.value) }
                  : prev.thinkTimeConfig,
              }))} placeholder="Script default" disabled={defaultEditDraft.thinkIgnore} />
            </label>
          )}
          {thinkTimeUsesStdDeviation(thinkType) && (
            <label>
              <span>Think time std dev (ms)</span>
              <input className="settings-input" type="number" min="0" value={defaultEditDraft.thinkTimeConfig?.std_ms ?? ''} onChange={event => updateThinkConfig({ std_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="0" disabled={defaultEditDraft.thinkIgnore} />
            </label>
          )}
          {thinkTimeUsesBounds(thinkType) && (
            <>
              <label>
                <span>Minimum think time (ms)</span>
                <input className="settings-input" type="number" min="0" value={defaultEditDraft.thinkTimeConfig?.min_ms ?? ''} onChange={event => updateThinkConfig({ min_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="0" disabled={defaultEditDraft.thinkIgnore} />
              </label>
              <label>
                <span>Maximum think time (ms)</span>
                <input className="settings-input" type="number" min="0" value={defaultEditDraft.thinkTimeConfig?.max_ms ?? ''} onChange={event => updateThinkConfig({ max_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="0" disabled={defaultEditDraft.thinkIgnore} />
              </label>
            </>
          )}
          {thinkType && (
            <>
              <ThinkTimeMultiplierInput
                draftScope={`${activeProjectID}:${scenarioId}:run-default`}
                label="Multiplier"
                className="settings-input"
                value={defaultEditDraft.thinkTimeConfig?.multiplier}
                onValueChange={multiplier => updateThinkConfig({ multiplier })}
              />
              <label>
                <span>Cap (ms, 0 = none)</span>
                <input className="settings-input" type="number" min="0" value={defaultEditDraft.thinkTimeConfig?.limit_ms ?? 0} onChange={event => updateThinkConfig({ limit_ms: Number(event.target.value) })} />
              </label>
            </>
          )}
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
    const next = { ...groupRunOverridesRef.current }
    delete next[editingGroupKey]
    groupRunOverridesRef.current = next
    setGroupRunOverrides(next)
    setGroupSaveNotice(`${editingGroup.name || editingGroup.script_name || 'Script group'} now uses scenario and run defaults. Scenario defaults were not changed.`)
    setEditingGroupKey(null)
  }

  const saveGroupOverride = (): boolean => {
    if (!editingGroupKey || !editingGroup || !scenario) return false
    if (!hasGroupEditChanges) return true
    const existingOverride = groupRunOverridesRef.current[editingGroupKey]
    const nextOverride: GroupRunOverride = existingOverride
      ? {
          ...existingOverride,
          runtime_settings: cloneRuntimeSettingsForRun(existingOverride.runtime_settings),
          assignment: existingOverride.assignment ? { ...existingOverride.assignment } : undefined,
        }
      : {}
    const stagedPeak = stagedGroupPeakVUsers(editingGroup)
    const vusers = stagedPeak ?? Number(groupEditDraft.vusers)
    if (groupEditEnvelopeError) return false
    if (stagedPeak !== undefined) delete nextOverride.vusers
    else if (groupEditChanges.vusers) nextOverride.vusers = vusers
    const rampUp = groupEditDraft.ramp_up.trim()
    const rampDown = groupEditDraft.ramp_down.trim()
    if (rampUp && !isValidNonNegativeDurationInput(rampUp)) {
      setError('Ramp up must be a non-negative Go duration such as 0s, 30s, or 5m.')
      return false
    }
    if (rampDown && !isValidNonNegativeDurationInput(rampDown)) {
      setError('Ramp down must be a non-negative Go duration such as 0s, 30s, or 5m.')
      return false
    }
    if (groupEditChanges.ramp_up) nextOverride.ramp_up = rampUp
    if (groupEditChanges.ramp_down) nextOverride.ramp_down = rampDown
    if (groupEditChanges.initialization) {
      nextOverride.initialization = groupEditDraft.initialization
    }

    const runtimeSettings = mergeRuntimeSettingsForRun(editingGroup.runtime_settings, existingOverride?.runtime_settings) || {}
    const effectiveRootRuntime = buildRuntimeSettingsForRun(scenario, runDefaultOverridesRef.current)
      || cloneRuntimeSettingsForRun(scenario.runtime_settings)
      || {}
    let runtimeChanged = false
    if (groupEditChanges.iterations) {
      const iterations = Number(groupEditDraft.iterations)
      if (!Number.isFinite(iterations) || iterations < 1) {
        setError('Iterations must be greater than 0 for a script group override.')
        return false
      }
      if (!runtimeSettings.run_logic && effectiveRootRuntime.run_logic) runtimeSettings.run_logic = { ...effectiveRootRuntime.run_logic }
      setRuntimeIterations(runtimeSettings, iterations)
      runtimeChanged = true
    }
    if (groupEditChanges.log_level) {
      if (!runtimeSettings.log && effectiveRootRuntime.log) runtimeSettings.log = { ...effectiveRootRuntime.log }
      setRuntimeLogLevel(runtimeSettings, groupEditDraft.log_level)
      runtimeChanged = true
    }
    if (groupEditChanges.auto_transactions) {
      if (!runtimeSettings.run_logic && effectiveRootRuntime.run_logic) runtimeSettings.run_logic = { ...effectiveRootRuntime.run_logic }
      setRuntimeAutoTransactions(runtimeSettings, groupEditDraft.auto_transactions)
      runtimeChanged = true
    }
    if (groupEditChanges.pacing) {
      const pacing = pacingFromEdit(groupEditDraft.pacingType, groupEditDraft.pacingAmount, groupEditDraft.pacingConfig)
      if (pacing) runtimeSettings.pacing = pacing
      else delete runtimeSettings.pacing
      runtimeChanged = true
    }
    if (groupEditChanges.think_time) {
      const thinkTime = thinkTimeFromEdit(groupEditDraft.thinkTimeMs, groupEditDraft.thinkIgnore, groupEditDraft.thinkTimeConfig)
      if (thinkTime) runtimeSettings.think_time = thinkTime
      else delete runtimeSettings.think_time
      runtimeChanged = true
    }
    if (groupEditChanges.error_handling) {
      // The API applies error_handling as a whole SECTION (mergeRuntimeSettings copies the
      // group's section over the run's, applyRuntimeOverrides then assigns it whole), so the
      // winning section contributes every field and the levels underneath contribute nothing —
      // not even a half the winner omits. Seed the untouched halves the same section-wise way
      // the dialog resolved them for display (resolveGroupErrorHandling), otherwise the value
      // saved is not the value shown: a group section that omits continue_on_error renders
      // Disabled but would be written back as the root's Enabled.
      const groupErrorHandling = runtimeSettings.error_handling
      const errorHandling = errorHandlingSectionIsPinned(groupErrorHandling)
        ? { ...groupErrorHandling }
        : { ...(effectiveRootRuntime.error_handling || {}) }
      if (groupEditChanges.continue_on_error) {
        if (groupEditDraft.continue_on_error === '') delete errorHandling.continue_on_error
        else errorHandling.continue_on_error = groupEditDraft.continue_on_error === 'true'
      }
      if (groupEditChanges.snapshot_on_error) {
        if (groupEditDraft.snapshot_on_error === '') delete errorHandling.snapshot_on_error
        else errorHandling.snapshot_on_error = groupEditDraft.snapshot_on_error === 'true'
      }
      if (Object.keys(errorHandling).length > 0) runtimeSettings.error_handling = errorHandling
      else delete runtimeSettings.error_handling
      runtimeChanged = true
    }
    if (groupEditChanges.browser) {
      if (!runtimeSettings.browser_config && effectiveRootRuntime.browser_config) {
        runtimeSettings.browser_config = cloneRuntimeSettingsForRun({ browser_config: effectiveRootRuntime.browser_config })?.browser_config
      }
      setRuntimeBrowser(runtimeSettings, {
        device_profile: groupEditDraft.browser_device_profile || undefined,
        headless: groupEditDraft.browser_headless ? groupEditDraft.browser_headless === 'true' : undefined,
        touch_mode: groupEditDraft.browser_touch_mode ? groupEditDraft.browser_touch_mode === 'true' : undefined,
        orientation: groupEditDraft.browser_orientation || undefined,
      })
      runtimeChanged = true
    }
    if ((runtimeChanged || existingOverride?.runtime_settings) && hasRuntimeSettingsValues(runtimeSettings)) {
      nextOverride.runtime_settings = runtimeSettings
    } else if (runtimeChanged) {
      delete nextOverride.runtime_settings
    }
    const nextAssignment = groupEditChanges.assignment
      ? assignmentFromGroupEditDraft(groupEditDraft)
      : existingOverride?.assignment || editingGroup.assignment
    const assignmentError = validateGeneratorAssignment(nextAssignment, 'group', editingGroup.name || editingGroup.script_name || 'Script group')
    if (assignmentError) {
      setError(assignmentError)
      return false
    }
    if (groupEditChanges.assignment) {
      const changedAssignment = nextAssignment as GeneratorAssignmentSettings
      if (groupEditDraft.assignmentMode === 'specific_hosts') {
        const selectedTargets = changedAssignment.selected_targets || []
        if (selectedTargets.length === 0) {
          setError('Select at least one load generator host for group placement.')
          return false
        }
        const unavailableTargets = selectedTargets.filter(target => {
          const generator = generatorByTarget.get(target)
          return generator ? !generatorCanRunGroup(generator, editingGroup) : false
        })
        if (unavailableTargets.length > 0) {
          setError(`Selected generator is not eligible for this group: ${unavailableTargets.join(', ')}`)
          return false
        }
      }
      if (groupEditDraft.assignmentMode === 'required_labels' && !normalizeRequiredLabels(groupEditDraft.requiredLabels)) {
        setError('Select at least one required generator capability for this group.')
        return false
      }
      nextOverride.assignment = changedAssignment
    }

    const next = { ...groupRunOverridesRef.current }
    if (hasGroupOverrideValues(nextOverride)) {
      next[editingGroupKey] = nextOverride
    } else {
      delete next[editingGroupKey]
    }
    groupRunOverridesRef.current = next
    setGroupRunOverrides(next)
    setError('')
    setGroupSaveNotice('Saved script settings for this run only. Scenario defaults were not changed.')
    setEditingGroupKey(null)
    return true
  }

  savePendingRunEditorsRef.current = () => {
    if (editingDefaultKey && hasDefaultEditChanges && !saveRunDefaultOverride()) return false
    if (editingGroupKey && hasGroupEditChanges && !saveGroupOverride()) return false
    return true
  }

  const submitSchedule = async (): Promise<boolean> => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return false
	}
	if (!scenario) {
	  setError('Choose a scenario before managing schedules.')
	  return false
	}
	const scope = {
	  projectID: activeProjectID,
	  scenarioID: scenario.id,
	  generation: scheduleScopeGenerationRef.current,
	}
	setScheduleSubmitting(true)
	setError('')
	const payload = {
	  name: scheduleDraft.name.trim(),
	  cron_expr: scheduleDraft.cron_expr.trim(),
	  timezone: scheduleDraft.timezone.trim() || 'UTC',
	  release_id: scheduleDraft.release_id || undefined,
	  timeslot_id: scheduleDraft.timeslot_id || undefined,
	  run_name_template: scheduleDraft.run_name_template.trim() || undefined,
	  enabled: scheduleDraft.enabled,
	  approval_policy: scheduleDraft.approval_policy,
	  missed_run_policy: scheduleDraft.missed_run_policy,
	  blackout_windows: scheduleWindowsToPayload(scheduleDraft.blackout_windows, scheduleDraft.timezone),
	  maintenance_windows: scheduleWindowsToPayload(scheduleDraft.maintenance_windows, scheduleDraft.timezone),
	  availability_windows: scheduleWindowsToPayload(scheduleDraft.availability_windows, scheduleDraft.timezone),
	  disabled_from: datetimeLocalToISO(scheduleDraft.disabled_from) || undefined,
	  disabled_until: datetimeLocalToISO(scheduleDraft.disabled_until) || undefined,
	  expected_revision: editingScheduleId ? scheduleDraft.revision : undefined,
	}
	try {
	  if (editingScheduleId) {
	    await api.updateScenarioSchedule(scenario.id, editingScheduleId, payload)
	  } else {
	    await api.createScenarioSchedule(scenario.id, payload)
	  }
	  if (!isCurrentScheduleScope(scope)) return false
	  setEditingScheduleId(null)
	  replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
	  await refreshSchedules(scenario.id)
	  return true
	} catch (err: unknown) {
	  if (isCurrentScheduleScope(scope)) setError(getErrorMessage(err, 'Unable to save schedule'))
	  return false
	} finally {
	  if (isCurrentScheduleScope(scope)) setScheduleSubmitting(false)
	}
  }

  const editSchedule = (schedule: ScenarioSchedule) => {
    runGuarded(() => {
      setEditingScheduleId(schedule.id)
      const nextDraft: ScheduleDraft = {
        revision: schedule.revision,
        name: schedule.name,
        cron_expr: schedule.cron_expr,
        timezone: schedule.timezone || 'UTC',
        release_id: schedule.release_id || '',
        timeslot_id: schedule.timeslot_id || '',
        run_name_template: schedule.run_name_template || '',
        enabled: schedule.enabled,
        approval_policy: schedule.approval_policy || 'auto',
        missed_run_policy: schedule.missed_run_policy || 'skip',
        blackout_windows: scheduleWindowsToDraft(schedule.blackout_windows),
        maintenance_windows: scheduleWindowsToDraft(schedule.maintenance_windows),
        availability_windows: scheduleWindowsToDraft(schedule.availability_windows),
        disabled_from: schedule.disabled_from ? formatDatetimeLocalValue(new Date(schedule.disabled_from)) : '',
        disabled_until: schedule.disabled_until ? formatDatetimeLocalValue(new Date(schedule.disabled_until)) : '',
      }
      replaceScheduleDraft(nextDraft)
    })
  }

  const toggleSchedule = async (schedule: ScenarioSchedule) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
	setError('')
	try {
	  await api.updateScenarioSchedule(scenario.id, schedule.id, {
	    name: schedule.name,
	    cron_expr: schedule.cron_expr,
	    timezone: schedule.timezone || 'UTC',
	    release_id: schedule.release_id || undefined,
	    timeslot_id: schedule.timeslot_id || undefined,
	    run_name_template: schedule.run_name_template || undefined,
	    enabled: !schedule.enabled,
	    approval_policy: schedule.approval_policy || 'auto',
	    missed_run_policy: schedule.missed_run_policy || 'skip',
	    blackout_windows: schedule.blackout_windows,
	    maintenance_windows: schedule.maintenance_windows,
	    availability_windows: schedule.availability_windows,
	    disabled_from: schedule.disabled_from || undefined,
	    disabled_until: schedule.disabled_until || undefined,
	    expected_revision: schedule.revision,
	  })
	  if (!isCurrentScheduleScope(scope)) return
	  await refreshSchedules(scenario.id)
	} catch (err: unknown) {
	  if (isCurrentScheduleScope(scope)) setError(getErrorMessage(err, 'Unable to update schedule'))
	}
  }

  const deleteSchedule = async (scheduleId: string) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
	setConfirmDeleteScheduleId(null)
	setError('')
	try {
	  await api.deleteScenarioSchedule(scenario.id, scheduleId)
	  if (!isCurrentScheduleScope(scope)) return
	  if (editingScheduleId === scheduleId) {
	    setEditingScheduleId(null)
	    replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
	  }
	  await refreshSchedules(scenario.id)
	} catch (err: unknown) {
	  if (isCurrentScheduleScope(scope)) setError(getErrorMessage(err, 'Unable to delete schedule'))
	}
  }

  const resolveScheduleApproval = async (schedule: ScenarioSchedule, approved: boolean) => {
    if (!canManageProjects) {
      setError('Project-admin access is required to resolve schedule approvals.')
      return
    }
    if (!scenario || !schedule.pending_approval_id) return
    const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
    setError('')
    try {
      if (approved) {
        await api.approveScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Approved from schedule dashboard.')
      } else {
        await api.rejectScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Rejected from schedule dashboard.')
      }
      if (!isCurrentScheduleScope(scope)) return
      await refreshSchedules(scenario.id)
    } catch (err: unknown) {
      if (isCurrentScheduleScope(scope)) setError(getErrorMessage(err, approved ? 'Unable to approve schedule' : 'Unable to reject schedule'))
    }
  }

  const toggleScheduleOccurrences = async (schedule: ScenarioSchedule) => {
    if (!scenario) return
    if (expandedScheduleId === schedule.id) {
      setExpandedScheduleId(null)
      return
    }
    const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
    const requestGeneration = ++scheduleOccurrenceRequestGenerationRef.current
    setExpandedScheduleId(schedule.id)
    if (scheduleOccurrences[schedule.id]) {
      return
    }
    setScheduleOccurrenceLoading(schedule.id)
    setError('')
    try {
      const response = await api.listScenarioScheduleOccurrences(scenario.id, schedule.id)
      if (!isCurrentScheduleScope(scope) || requestGeneration !== scheduleOccurrenceRequestGenerationRef.current) return
      setScheduleOccurrences(prev => ({ ...prev, [schedule.id]: response.occurrences || [] }))
    } catch (err: unknown) {
      if (isCurrentScheduleScope(scope) && requestGeneration === scheduleOccurrenceRequestGenerationRef.current) {
        setError(getErrorMessage(err, 'Unable to load schedule occurrences'))
      }
    } finally {
      if (isCurrentScheduleScope(scope) && requestGeneration === scheduleOccurrenceRequestGenerationRef.current) {
        setScheduleOccurrenceLoading(null)
      }
    }
  }

		const resolveScheduleOccurrence = async (schedule: ScenarioSchedule, occurrence: ScenarioScheduleOccurrence, action: 'retry' | 'catch_up' | 'skip') => {
		  if (!canManageProjects || !scenario || occurrence.reason_code === 'approval_required') return
		  const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
		  setScheduleSubmitting(true)
	  setError('')
	  try {
		await api.resolveScenarioScheduleOccurrence(
		  scenario.id,
		  schedule.id,
		  occurrence.id,
		  action,
		  occurrence.revision,
			  `Operator selected ${action.replace('_', ' ')} from the schedule dashboard.`,
			)
			if (!isCurrentScheduleScope(scope)) return
			const response = await api.listScenarioScheduleOccurrences(scenario.id, schedule.id)
			if (!isCurrentScheduleScope(scope)) return
			setScheduleOccurrences(prev => ({ ...prev, [schedule.id]: response.occurrences || [] }))
		await refreshSchedules(scenario.id)
		  } catch (err: unknown) {
			if (isCurrentScheduleScope(scope)) setError(getErrorMessage(err, 'Unable to resolve schedule occurrence'))
		  } finally {
			if (isCurrentScheduleScope(scope)) setScheduleSubmitting(false)
		  }
		}

  saveScheduleRef.current = submitSchedule

  const showStartNow = true
  const startNowAllowed = immediatePreflight ? immediatePreflight.start_now_allowed : true
  const showQueueRun = Boolean(immediatePreflight?.queue_allowed)
  const immediateLaunchBlocked = Boolean(immediatePreflight && !startNowAllowed && !showQueueRun)
  const scheduleAllowed = Boolean(scheduledStartISO && scheduledPreflight?.schedule_allowed)
  const scheduleBlocked = Boolean(scheduledStartISO && scheduledPreflight && !scheduledPreflight.schedule_allowed)
  const scheduleMin = formatDatetimeLocalValue(scheduleClock)
  const newTestViewControls = useMemo(() => (
    <PageViewToggle
      value={pageMode}
      ariaLabel="New test page view"
      className="new-test-view-toggle topbar-page-section-toggle"
      onChange={nextMode => runGuarded(() => setPageMode(nextMode))}
      options={[
        { value: 'launch', label: 'Run Setup' },
        { value: 'schedules', label: 'Schedules' },
      ]}
    />
  ), [pageMode, runGuarded])
  const newTestTopbarContribution = useMemo(() => ({
    id: 'new-test-page',
    title: 'New Test',
    controls: newTestViewControls,
    priority: 10,
  }), [newTestViewControls])

  useTopbarContribution(newTestTopbarContribution)

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>New Test</h2>
          <p className="page-subtitle">Choose the release context, scenario, and run setup.</p>
        </div>
        {!hasTopbarContributionProvider && (
          <div className="inline-create-row new-test-view-switcher">
            {newTestViewControls}
          </div>
        )}
      </div>

      {error && <ErrorDiagnostic error={error} fallback="Unable to create or load test setup" compact className="new-test-error-diagnostic" />}
      {!canManageTests && <div className="settings-error-inline page-error-banner" role="alert">Current session can view scenarios but cannot create runs or schedules for this project.</div>}
      {queueRecovery && (
        <div className="new-test-launch-feedback warning" role="status">
          {queueRecovery.status === 'checking' ? (
            <span>Checking the preserved queued-run attempt…</span>
          ) : queueRecovery.status === 'ready' && queueRecovery.test ? (
            <>
              <span>
                Preserved queued run <strong>{queueRecovery.test.name || queueRecovery.test.id}</strong> is still waiting to start.
              </span>
              <div className="inline-create-row">
                <button className="btn btn-sm btn-primary" type="button" onClick={() => void resumeRecoveredQueuedRun()} disabled={creating}>
                  Resume queued run
                </button>
                <button className="btn btn-sm btn-secondary" type="button" onClick={createAnotherQueuedRun} disabled={creating}>
                  Create another run
                </button>
              </div>
              <small>Create another run only clears this recovery marker; it does not delete the existing queued run.</small>
            </>
          ) : (
            <>
              <span>{queueRecovery.message || 'The preserved queued run could not be verified.'}</span>
              <div className="inline-create-row">
                <button className="btn btn-sm btn-secondary" type="button" onClick={() => setQueueRecoveryCheckGeneration(current => current + 1)} disabled={creating}>
                  Retry verification
                </button>
                <button className="btn btn-sm btn-secondary" type="button" onClick={createAnotherQueuedRun} disabled={creating}>
                  Create another run
                </button>
              </div>
              <small>Create another run only clears this recovery marker; it does not delete any run that may already exist.</small>
            </>
          )}
        </div>
      )}

      {pageMode === 'schedules' ? (
        <div className="new-test-layout new-test-schedules-layout">
          <div className="new-test-main">
            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">1</span>
                <div className="new-test-step-header-content">
                  <h3>Scenario Schedule</h3>
                  <p>Bind a recurring cron expression to a reusable scenario.</p>
                </div>
                <div className="new-test-step-header-actions">
                  {editingScheduleId && (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      onClick={() => {
                        setEditingScheduleId(null)
                        replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
                      }}
                    >
                      Cancel Edit
                    </button>
                  )}
                  <button type="button" className="btn btn-primary" onClick={submitSchedule} disabled={!scenario || scheduleSubmitting || !canManageTests}>
                    {editingScheduleId ? 'Update Schedule' : 'Save Schedule'}
                  </button>
                </div>
              </div>
              <div className="settings-group">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-scenario-label">Scenario</span>
                    <span className="setting-desc">Recurring runs always execute one stored scenario</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-scenario-label" value={scenarioId} onChange={(event) => {
                    const nextScenarioID = event.target.value
                    runGuarded(() => {
                      setScenarioId(nextScenarioID)
                      setEditingScheduleId(null)
                    })
                  }}>
                    <option value="">Select scenario</option>
                    {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-name-label">Schedule Name</span>
                    <span className="setting-desc">Human-friendly label for the recurring trigger</span>
                  </div>
                  <input className="settings-input" aria-labelledby="schedule-name-label" value={scheduleDraft.name} onChange={(event) => setScheduleDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Nightly smoke" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Cron Expression</span>
                    <span className="setting-desc">Build a schedule below, or type a five-field cron (supports `MON-FRI`, `@daily`, `L`, `15W`, `5#3`).</span>
                    {scheduleDraft.cron_expr.trim() && describeCron(scheduleDraft.cron_expr) && (
                      <span className="setting-desc cron-preview">{describeCron(scheduleDraft.cron_expr)}{scheduleDraft.timezone ? ` (${scheduleDraft.timezone})` : ''}</span>
                    )}
                  </div>
                  <div className="cron-field-stack">
                    <CronBuilder value={scheduleDraft.cron_expr} disabled={!canManageTests} onChange={(expr) => setScheduleDraft(prev => ({ ...prev, cron_expr: expr }))} />
                    <input className="settings-input cron-raw-input" value={scheduleDraft.cron_expr} disabled={!canManageTests} onChange={(event) => setScheduleDraft(prev => ({ ...prev, cron_expr: event.target.value }))} placeholder="0 9 * * 1-5" aria-label="Raw cron expression" />
                    {schedulePreview && (
                      schedulePreview.error ? (
                        <span className="setting-desc cron-preview-error" role="alert">{schedulePreview.error}</span>
                      ) : schedulePreview.occurrences.length > 0 ? (
                        <div className="cron-next-runs">
                          <span className="setting-desc">Next runs ({scheduleDraft.timezone || 'UTC'}):</span>
                          <ul>
                            {schedulePreview.occurrences.map((iso, idx) => (
                              <li key={idx}>{formatScheduleTimestampInZone(iso, scheduleDraft.timezone)}</li>
                            ))}
                          </ul>
                        </div>
                      ) : (
                        <span className="setting-desc">No upcoming runs match this schedule within the next year.</span>
                      )
                    )}
                    {schedulePreview && !schedulePreview.error && schedulePreview.conflictSummary && (
                      <span className="setting-desc cron-preview-error" role="alert">
                        ⚠ This schedule will be blocked until resolved: {schedulePreview.conflictSummary}
                      </span>
                    )}
                  </div>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-timezone-label">Timezone</span>
                    <span className="setting-desc">IANA timezone used for cron evaluation</span>
                    <span className="setting-desc schedule-current-utc">Current UTC: {formatUtcScheduleClock(scheduleClock)} UTC</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-timezone-label" value={scheduleDraft.timezone} onChange={(event) => setScheduleDraft(prev => ({ ...prev, timezone: event.target.value }))}>
                    {COMMON_TIMEZONES.map(tz => <option key={tz} value={tz}>{formatTimeZoneOptionLabel(tz, scheduleClock)}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-release-label">Release Context</span>
                    <span className="setting-desc">Optional release to attach to scheduled runs</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-release-label" value={scheduleDraft.release_id} onChange={(event) => setScheduleDraft(prev => ({ ...prev, release_id: event.target.value }))}>
                    <option value="">No release</option>
                    {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-timeslot-label">Default Timeslot</span>
                    <span className="setting-desc">Optional approved capacity timeslot each scheduled run binds at materialization. If the slot is unavailable, that run is recorded as blocked instead of launching unprotected.</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-timeslot-label" value={scheduleDraft.timeslot_id} onChange={(event) => setScheduleDraft(prev => ({ ...prev, timeslot_id: event.target.value }))}>
                    <option value="">No timeslot</option>
                    {bindableTimeslots.map(slot => <option key={slot.id} value={slot.id}>{formatTimeslotLabel(slot)}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-run-name-label">Run Name Template</span>
                    <span className="setting-desc">Default run name used when the schedule fires</span>
                  </div>
                  <input className="settings-input" aria-labelledby="schedule-run-name-label" value={scheduleDraft.run_name_template} onChange={(event) => setScheduleDraft(prev => ({ ...prev, run_name_template: event.target.value }))} placeholder={scenario?.name || 'Scheduled run'} />
                </div>
                <ScheduleWindowListEditor
                  title="Blackout windows"
                  description="Runs never fire during these windows. Leave days unselected to apply every day."
                  windows={scheduleDraft.blackout_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageTests}
                  onChange={next => setScheduleDraft(prev => ({ ...prev, blackout_windows: next }))}
                />
                <ScheduleWindowListEditor
                  title="Maintenance windows"
                  description="Shared maintenance windows that also block runs. Leave days unselected to apply every day."
                  windows={scheduleDraft.maintenance_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageTests}
                  onChange={next => setScheduleDraft(prev => ({ ...prev, maintenance_windows: next }))}
                />
                <ScheduleWindowListEditor
                  title="Availability windows"
                  description="When set, runs fire only inside these windows. Leave days unselected to apply every day."
                  windows={scheduleDraft.availability_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageTests}
                  onChange={next => setScheduleDraft(prev => ({ ...prev, availability_windows: next }))}
                />
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Pause window (optional)</span>
                    <span className="setting-desc">Suppress runs over an absolute date range without disabling the schedule. Leave a bound empty for an open-ended pause; clear both to resume normally.</span>
                  </div>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                    <input
                      type="datetime-local"
                      className="settings-input"
                      aria-label="Pause from"
                      value={scheduleDraft.disabled_from}
                      disabled={!canManageTests}
                      onChange={(event) => setScheduleDraft(prev => ({ ...prev, disabled_from: event.target.value }))}
                    />
                    <input
                      type="datetime-local"
                      className="settings-input"
                      aria-label="Pause until"
                      value={scheduleDraft.disabled_until}
                      disabled={!canManageTests}
                      onChange={(event) => setScheduleDraft(prev => ({ ...prev, disabled_until: event.target.value }))}
                    />
                  </div>
                </div>
                <label className="setting-row" style={{ alignItems: 'center', gap: 12 }}>
                  <input type="checkbox" checked={scheduleDraft.enabled} onChange={(event) => setScheduleDraft(prev => ({ ...prev, enabled: event.target.checked }))} />
                  <div className="setting-info">
                    <span className="setting-label">Enabled</span>
                    <span className="setting-desc">Disabled schedules stay saved but do not trigger</span>
                  </div>
                </label>
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
                          <td aria-label="Schedule governance">
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
                                  {canManageProjects && item.next_required_action === 'approval_required' && item.pending_approval_id && (
                                    <>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, true)}>Approve</button>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, false)}>Reject</button>
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
										  <th>Resolution</th>
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
											<td>
											  {(occurrence.status === 'blocked' || occurrence.status === 'failed') && occurrence.reason_code !== 'approval_required' && canManageProjects ? (
												<div className="inline-create-row" aria-label="Resolve occurrence">
												  <button className="btn btn-secondary" disabled={scheduleSubmitting} onClick={() => resolveScheduleOccurrence(item, occurrence, 'retry')}>Retry</button>
												  <button className="btn btn-secondary" disabled={scheduleSubmitting} onClick={() => resolveScheduleOccurrence(item, occurrence, 'catch_up')}>Catch up</button>
												  <button className="btn btn-secondary" disabled={scheduleSubmitting} onClick={() => resolveScheduleOccurrence(item, occurrence, 'skip')}>Skip</button>
												</div>
											  ) : 'None'}
											</td>
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
                <p>Choose the reusable workload definition below, or choose Open Scenario Builder to create a new Scenario.</p>
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
                        <table className="new-test-scenario-table" aria-label="Available scenarios">
                          <thead>
                            <tr>
                              <th scope="col" aria-sort={scenarioAriaSort('name')} className="sortable"><button type="button" onClick={() => sortScenarios('name')}>Scenario <SortIndicator active={scenarioSortField === 'name'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col" aria-sort={scenarioAriaSort('releases')} className="sortable"><button type="button" onClick={() => sortScenarios('releases')}>Releases <SortIndicator active={scenarioSortField === 'releases'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col" aria-sort={scenarioAriaSort('last_run_at')} className="sortable"><button type="button" onClick={() => sortScenarios('last_run_at')}>Most Recent Run <SortIndicator active={scenarioSortField === 'last_run_at'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col" aria-sort={scenarioAriaSort('vusers')} className="sortable"><button type="button" onClick={() => sortScenarios('vusers')}>VUsers <SortIndicator active={scenarioSortField === 'vusers'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col" aria-sort={scenarioAriaSort('duration')} className="sortable"><button type="button" onClick={() => sortScenarios('duration')}>Duration <SortIndicator active={scenarioSortField === 'duration'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col" aria-sort={scenarioAriaSort('groups')} className="sortable"><button type="button" onClick={() => sortScenarios('groups')}>Groups <SortIndicator active={scenarioSortField === 'groups'} direction={scenarioSortDirection} /></button></th>
                              <th scope="col">Protocols</th>
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
                                    tabIndex={0}
                                    aria-selected={selected}
                                    aria-expanded={expanded}
                                    aria-controls={`new-test-scenario-groups-${item.id}`}
                                    onClick={() => selectScenario(item.id)}
                                    onKeyDown={event => {
                                      if (event.currentTarget !== event.target) return
                                      if (event.key === 'Enter' || event.key === ' ') {
                                        event.preventDefault()
                                        selectScenario(item.id)
                                      }
                                    }}
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
                                    <tr className="no-hover new-test-scenario-expanded-row" id={`new-test-scenario-groups-${item.id}`}>
                              <td colSpan={7} aria-label="Scenario script groups">
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
                                                    <tr
                                                      key={key}
                                                      className={rowOverridden ? 'new-test-script-row overridden' : 'new-test-script-row'}
                                                      tabIndex={selected ? 0 : undefined}
                                                      aria-label={selected ? `Edit run settings for ${group.name || group.script_name || 'script group'}` : undefined}
                                                      onClick={() => { if (selected) openGroupEditor(sourceGroup) }}
                                                      onKeyDown={event => {
                                                        if (!selected || event.currentTarget !== event.target) return
                                                        if (event.key === 'Enter' || event.key === ' ') {
                                                          event.preventDefault()
                                                          openGroupEditor(sourceGroup)
                                                        }
                                                      }}
                                                    >
                                                      <td className="cell-name">
                                                        <span>{group.name || group.script_name}</span>
                                                        {rowOverridden && <em className="new-test-override-pill">Run override</em>}
                                                      </td>
                                                      <td>{group.script_name || group.script_id}</td>
                                                      <td><span className="protocol-badge">{group.protocol || 'http'}</span></td>
                                    <td aria-label="Group workload">
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
                                                      <td className="new-test-runtime-cell">{formatGroupPlacement(override, sourceGroup)}</td>
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
                    <div className="new-test-empty compact">
                      {scenarioFiltersActive
                        ? 'No scenarios match this search.'
                        : 'No scenarios are available yet. Open Scenario Builder to create the first reusable workload.'}
                    </div>
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
                    <span className="setting-label" id="new-test-run-name-label">Run Name</span>
                    <span className="setting-desc">Shown in results and monitoring</span>
                  </div>
                  <input className="settings-input" aria-labelledby="new-test-run-name-label" value={runName} onChange={event => setRunName(event.target.value)} placeholder={scenario?.name || 'Run name'} />
                </div>
                <div className="setting-row new-test-release-row">
                  <div className="setting-info">
                    <span className="setting-label" id="new-test-release-label">Release</span>
                    <span className="setting-desc">Required target for results</span>
                  </div>
                  <select className="settings-input" aria-labelledby="new-test-release-label" value={releaseId} onChange={(event) => setReleaseId(event.target.value)}>
                    <option value="">Select release</option>
                    {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                  {showNewRelease ? (
                    <div className="new-test-release-create-row">
                      <input className="settings-input" aria-label="New release name" value={newReleaseName} onChange={event => setNewReleaseName(event.target.value)} placeholder="Release name, e.g. PI30" />
                      <button className="btn btn-secondary" onClick={createRelease} disabled={!newReleaseName.trim()}>Add</button>
                      <button className="btn btn-ghost btn-sm" onClick={() => { setShowNewRelease(false); setNewReleaseName('') }}>Cancel</button>
                    </div>
                  ) : (
                    <button className="btn btn-ghost btn-sm btn-accent-action new-test-add-release-link" onClick={() => setShowNewRelease(true)}>+ New Release</button>
                  )}
                </div>
                {bindableTimeslots.length > 0 && (
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label" id="new-test-timeslot-label">Reserved Timeslot</span>
                      <span className="setting-desc">Attach this run to an approved capacity reservation (optional)</span>
                    </div>
                    <select className="settings-input" aria-labelledby="new-test-timeslot-label" value={selectedTimeslotId} onChange={event => setSelectedTimeslotId(event.target.value)}>
                      <option value="">No reservation</option>
                      {bindableTimeslots.map(slot => (
                        <option key={slot.id} value={slot.id}>
                          {formatTimeslotLabel(slot)}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
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
                        type="number"
                        domain={[0, 'dataMax']}
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
                        itemSorter={preserveChartItemOrder}
                        labelFormatter={(value) => `Time: ${formatTimeCompact(Number(value))}`}
                        contentStyle={{ ...chartTooltipContentStyle, borderRadius: 8, fontSize: '12px' }}
                        labelStyle={chartTooltipLabelStyle}
                        formatter={(value, name) => [value ?? 0, name === 'total' ? 'Total VUsers' : name ?? '']}
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

          <section className="new-test-launch-card" aria-label="Hold for Manual Start or Schedule Run">
            <h3>Hold for Manual Start / Schedule Run</h3>
              <div className="new-test-launch-actions">
                {runSetupValidationError && <div className="new-test-launch-feedback warning" role="alert">{runSetupValidationError}</div>}
              {immediatePreflightLoading && !immediatePreflight && (
                <button className="btn btn-secondary" disabled>Checking Window</button>
              )}
              {showStartNow && (
                <button className="btn btn-primary" onClick={() => createRun('reserve')} disabled={!canLaunch || immediatePreflightLoading || !startNowAllowed} title="Holds a launch window for this run to be started manually later. This is not a capacity timeslot reservation.">Hold for Manual Start</button>
              )}
              {showQueueRun && (
                <button className="btn btn-secondary" onClick={() => createRun('queue')} disabled={!canLaunch || immediatePreflightLoading || Boolean(queueRecovery)}>Create Queued Run</button>
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

          <section className="new-test-summary">
            <h3>Run Summary</h3>
            <dl className="new-test-summary-list" aria-label="Run settings summary">
              {launchSummaryItems.map(item => (
                <div key={item.label} className={item.tone === 'warning' ? 'warning' : undefined}>
                  <dt>{item.label}</dt>
                  <dd>{item.value}</dd>
                </div>
              ))}
            </dl>
          </section>
        </aside>
      </div>
      )}

      {editingDefaultKey && editingDefaultCard && (
        <div className="modal-overlay" {...dialogBackdropProps(() => setEditingDefaultKey(null), 'Close run default editor')}>
          <div
            ref={runDefaultDialogRef}
            className="modal run-default-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="new-test-run-default-dialog-title"
          >
            <div className="modal-header">
              <h3 id="new-test-run-default-dialog-title">Edit {editingDefaultCard.label}</h3>
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

      {editingGroupKey && editingGroup && scenario && createPortal(
        <div className="modal-overlay" {...dialogBackdropProps(() => setEditingGroupKey(null), 'Close run group editor')}>
          <div
            ref={runGroupDialogRef}
            className="modal run-default-modal run-group-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="new-test-run-group-dialog-title"
          >
            <div className="modal-header">
              <h3 id="new-test-run-group-dialog-title">Edit {editingGroup.name || editingGroup.script_name || 'Script Group'}</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditingGroupKey(null)}>Close</button>
            </div>
            <div className="new-test-default-editor run-group-modal-body">
              <p className="new-test-default-editor-note">Saved changes apply only to the run created from this page. The scenario default remains unchanged.</p>
              <div className="new-test-default-current">
                <span>Scenario defaults</span>
                <strong>{editingGroupStagePeak ?? editingGroup.vusers} VUsers · {editingGroup.ramp_up || 'No ramp up'} / {editingGroup.ramp_down || 'No ramp down'}</strong>
              </div>
              <div className="new-test-group-editor-sections">
                <section>
                  <h4>Workload</h4>
                  <div className="new-test-default-editor-grid">
                    <label className={changedFieldClass(groupEditChanges.vusers)}>
                      <span>{editingGroupStagePeak === undefined ? 'VUsers' : 'Peak VUsers (stage-derived)'} <ChangedBadge changed={groupEditChanges.vusers} /></span>
                      <input
                        className="settings-input"
                        type="number"
                        min="1"
                        max={maxVUsersForRole(editingGroup.role)}
                        value={groupEditDraft.vusers}
                        disabled={editingGroupStagePeak !== undefined}
                        aria-invalid={Boolean(groupEditEnvelopeError)}
                        aria-describedby={groupEditEnvelopeError ? 'new-test-group-vusers-error' : undefined}
                        onChange={event => setGroupEditDraft(prev => ({ ...prev, vusers: event.target.value }))}
                      />
                    </label>
                    {groupEditEnvelopeError && (
                      <p id="new-test-group-vusers-error" className="new-test-group-field-error" role="alert">{groupEditEnvelopeError}</p>
                    )}
                    {editingGroupStagePeak !== undefined && (
                      <p className="new-test-inline-note">This staged group is fixed at its peak stage target of {editingGroupStagePeak} VUsers. Edit the scenario stages to change it.</p>
                    )}
                    <label className={changedFieldClass(groupEditChanges.initialization)}>
                      <span>Initialization <ChangedBadge changed={groupEditChanges.initialization} /></span>
                      <select className="settings-input" value={groupEditDraft.initialization} onChange={event => setGroupEditDraft(prev => ({ ...prev, initialization: event.target.value as 'at_runtime' | 'at_once' }))}>
                        <option value="at_runtime">At runtime</option>
                        <option value="at_once">At once</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.ramp_up)}>
                      <span>Ramp up <ChangedBadge changed={groupEditChanges.ramp_up} /></span>
                      <input className="settings-input" value={groupEditDraft.ramp_up} aria-invalid={groupRampUpInvalid} aria-describedby={groupRampUpInvalid ? 'new-test-group-ramp-up-error' : undefined} onChange={event => { setError(''); setGroupEditDraft(prev => ({ ...prev, ramp_up: event.target.value })) }} placeholder="Scenario default" />
                      {groupRampUpInvalid && <small id="new-test-group-ramp-up-error" className="new-test-group-field-error">Use a duration such as 30s, 5m, or 1h30m.</small>}
                    </label>
                    <label className={changedFieldClass(groupEditChanges.ramp_down)}>
                      <span>Ramp down <ChangedBadge changed={groupEditChanges.ramp_down} /></span>
                      <input className="settings-input" value={groupEditDraft.ramp_down} aria-invalid={groupRampDownInvalid} aria-describedby={groupRampDownInvalid ? 'new-test-group-ramp-down-error' : undefined} onChange={event => { setError(''); setGroupEditDraft(prev => ({ ...prev, ramp_down: event.target.value })) }} placeholder="Scenario default" />
                      {groupRampDownInvalid && <small id="new-test-group-ramp-down-error" className="new-test-group-field-error">Use a duration such as 30s, 5m, or 1h30m.</small>}
                    </label>
                  </div>
                </section>
                <section>
                  <h4>Load Generator Sharding <ChangedBadge changed={groupEditChanges.assignment} /></h4>
                  <p className="new-test-inline-note">Saved host placement applies only to this run. Multiple hosts split this group by available capacity.</p>
                  {generatorInventoryLoading ? (
                    <div className="new-test-empty compact">Loading generator inventory...</div>
                  ) : (
                    <>
                      {generatorInventory.length === 0 && (
                        <div className="new-test-empty compact">{generatorInventoryError || 'No load generators are registered. Automatic placement and capability rules remain available.'}</div>
                      )}
                    <GroupPlacementEditor
                      assignment={assignmentFromGroupEditDraft(groupEditDraft)}
                      onChange={assignment => setGroupEditDraft(prev => ({ ...prev, ...groupEditDraftPatchFromAssignment(assignment) }))}
                      generatorInventory={generatorInventory}
                      availableLabels={availableLabels}
                      protocol={editingGroup?.protocol}
                      compact
                      allowInherit={false}
                      scope="group"
                    />
                    </>
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
                      <select className="settings-input" value={groupEditDraft.pacingType} onChange={event => {
                        const type = event.target.value
                        setGroupEditDraft(prev => ({
                          ...prev,
                          pacingType: type,
                          pacingAmount: type === 'none' || type.startsWith('random_') ? '' : prev.pacingAmount,
                          pacingConfig: pacingConfigForSelection(type, prev.pacingConfig, prev.pacingAmount),
                        }))
                      }}>
                        <option value="">Script timing</option>
                        <option value="none">None</option>
                        <option value="fixed_delay">Fixed delay</option>
                        <option value="random_delay">Random delay</option>
                        <option value="fixed_interval">Fixed interval</option>
                        <option value="random_interval">Random interval</option>
                        <option value="tps">Target TPS</option>
                      </select>
                    </label>
                    {['fixed_delay', 'fixed_interval', 'tps'].includes(groupEditDraft.pacingType) && (
                      <label className={changedFieldClass(groupEditChanges.pacing)}>
                        <span>{groupEditDraft.pacingType === 'tps' ? 'Target TPS' : 'Seconds'} <ChangedBadge changed={groupEditChanges.pacing} /></span>
                        <input className="settings-input" type="number" min="0" step={groupEditDraft.pacingType === 'tps' ? undefined : SECONDS_INPUT_STEP} value={groupEditDraft.pacingAmount} onChange={event => setGroupEditDraft(prev => ({ ...prev, pacingAmount: event.target.value }))} />
                      </label>
                    )}
                    {groupEditDraft.pacingType === 'random_delay' && (
                      <>
                        <label className={changedFieldClass(groupEditChanges.pacing)}>
                          <span>Minimum delay (seconds) <ChangedBadge changed={groupEditChanges.pacing} /></span>
                          <SecondsFromMillisecondsInput className="settings-input"
                            valueMs={groupEditDraft.pacingConfig?.min_delay_ms}
                            emptyWhenUndefined
                            onChangeMs={minDelayMs => setGroupEditDraft(prev => ({
                              ...prev,
                              pacingConfig: {
                                ...randomPacingDraft('random_delay', prev.pacingConfig, prev.pacingAmount),
                                min_delay_ms: minDelayMs,
                              },
                            }))} />
                        </label>
                        <label className={changedFieldClass(groupEditChanges.pacing)}>
                          <span>Maximum delay (seconds) <ChangedBadge changed={groupEditChanges.pacing} /></span>
                          <SecondsFromMillisecondsInput className="settings-input"
                            valueMs={groupEditDraft.pacingConfig?.max_delay_ms}
                            emptyWhenUndefined
                            onChangeMs={maxDelayMs => setGroupEditDraft(prev => ({
                              ...prev,
                              pacingConfig: {
                                ...randomPacingDraft('random_delay', prev.pacingConfig, prev.pacingAmount),
                                max_delay_ms: maxDelayMs,
                              },
                            }))} />
                        </label>
                      </>
                    )}
                    {groupEditDraft.pacingType === 'random_interval' && (
                      <>
                        <label className={changedFieldClass(groupEditChanges.pacing)}>
                          <span>Minimum interval (seconds) <ChangedBadge changed={groupEditChanges.pacing} /></span>
                          <SecondsFromMillisecondsInput className="settings-input"
                            valueMs={groupEditDraft.pacingConfig?.min_interval_ms}
                            emptyWhenUndefined
                            onChangeMs={minIntervalMs => setGroupEditDraft(prev => ({
                              ...prev,
                              pacingConfig: {
                                ...randomPacingDraft('random_interval', prev.pacingConfig, prev.pacingAmount),
                                min_interval_ms: minIntervalMs,
                              },
                            }))} />
                        </label>
                        <label className={changedFieldClass(groupEditChanges.pacing)}>
                          <span>Maximum interval (seconds) <ChangedBadge changed={groupEditChanges.pacing} /></span>
                          <SecondsFromMillisecondsInput className="settings-input"
                            valueMs={groupEditDraft.pacingConfig?.max_interval_ms}
                            emptyWhenUndefined
                            onChangeMs={maxIntervalMs => setGroupEditDraft(prev => ({
                              ...prev,
                              pacingConfig: {
                                ...randomPacingDraft('random_interval', prev.pacingConfig, prev.pacingAmount),
                                max_interval_ms: maxIntervalMs,
                              },
                            }))} />
                        </label>
                      </>
                    )}
                    <label className={changedFieldClass(groupEditChanges.think_time)}>
                      <span>Think time mode <ChangedBadge changed={groupEditChanges.think_time} /></span>
                      <select className="settings-input" value={canonicalThinkTimeType(groupEditDraft.thinkTimeConfig?.type)} onChange={event => {
                        const type = event.target.value
                        setGroupEditDraft(prev => ({
                          ...prev,
                          thinkTimeMs: type === 'recorded' ? '' : prev.thinkTimeMs,
                          thinkIgnore: type ? prev.thinkIgnore : false,
                          thinkTimeConfig: type
                            ? thinkTimeConfigForType({
                                ...prev.thinkTimeConfig,
                                ...(prev.thinkTimeMs.trim() !== '' ? { mean_ms: Number(prev.thinkTimeMs) } : {}),
                              }, type)
                            : undefined,
                        }))
                      }}>
                        <option value="">Script think time</option>
                        <option value="none">None</option>
                        <option value="recorded">Replay think time as recorded</option>
                        <option value="fixed">Fixed</option>
                        <option value="gaussian">Gaussian</option>
                        <option value="uniform">Uniform</option>
                      </select>
                    </label>
                    {thinkTimeUsesMean(groupEditDraft.thinkTimeConfig?.type) && (
                      <label className={changedFieldClass(groupEditChanges.think_time)}>
                        <span>Think time mean (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                        <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeMs} onChange={event => setGroupEditDraft(prev => ({
                          ...prev,
                          thinkTimeMs: event.target.value,
                          thinkTimeConfig: prev.thinkTimeConfig
                            ? { ...prev.thinkTimeConfig, mean_ms: event.target.value === '' ? undefined : Number(event.target.value) }
                            : prev.thinkTimeConfig,
                        }))} placeholder="Script think time" disabled={groupEditDraft.thinkIgnore} />
                      </label>
                    )}
                    {thinkTimeUsesStdDeviation(groupEditDraft.thinkTimeConfig?.type) && (
                      <label className={changedFieldClass(groupEditChanges.think_time)}>
                        <span>Think time std dev (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                        <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeConfig?.std_ms ?? ''} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkTimeConfig: { ...prev.thinkTimeConfig!, std_ms: event.target.value === '' ? undefined : Number(event.target.value) } }))} placeholder="0" disabled={groupEditDraft.thinkIgnore} />
                      </label>
                    )}
                    {thinkTimeUsesBounds(groupEditDraft.thinkTimeConfig?.type) && (
                      <>
                        <label className={changedFieldClass(groupEditChanges.think_time)}>
                          <span>Minimum think time (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                          <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeConfig?.min_ms ?? ''} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkTimeConfig: { ...prev.thinkTimeConfig!, min_ms: event.target.value === '' ? undefined : Number(event.target.value) } }))} placeholder="0" disabled={groupEditDraft.thinkIgnore} />
                        </label>
                        <label className={changedFieldClass(groupEditChanges.think_time)}>
                          <span>Maximum think time (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                          <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeConfig?.max_ms ?? ''} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkTimeConfig: { ...prev.thinkTimeConfig!, max_ms: event.target.value === '' ? undefined : Number(event.target.value) } }))} placeholder="0" disabled={groupEditDraft.thinkIgnore} />
                        </label>
                      </>
                    )}
                    {groupEditDraft.thinkTimeConfig?.type && (
                      <>
                        <ThinkTimeMultiplierInput
                          draftScope={`${activeProjectID}:${scenarioId}:${editingGroupKey || 'group'}`}
                          label={<>Think time multiplier <ChangedBadge changed={groupEditChanges.think_time} /></>}
                          wrapperClassName={changedFieldClass(groupEditChanges.think_time)}
                          className="settings-input"
                          value={groupEditDraft.thinkTimeConfig.multiplier}
                          onValueChange={multiplier => setGroupEditDraft(prev => ({
                            ...prev,
                            thinkTimeConfig: thinkTimeConfigForType({
                              ...prev.thinkTimeConfig!,
                              multiplier,
                            }, prev.thinkTimeConfig?.type || 'fixed'),
                          }))}
                        />
                        <label className={changedFieldClass(groupEditChanges.think_time)}>
                          <span>Think time cap (ms) <ChangedBadge changed={groupEditChanges.think_time} /></span>
                          <input className="settings-input" type="number" min="0" value={groupEditDraft.thinkTimeConfig.limit_ms ?? 0} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkTimeConfig: { ...prev.thinkTimeConfig!, limit_ms: Number(event.target.value) } }))} />
                        </label>
                      </>
                    )}
                    <label className={changedFieldClass(groupEditChanges.think_time, 'checkbox-label new-test-default-checkbox')}>
                      <input type="checkbox" checked={groupEditDraft.thinkIgnore} onChange={event => setGroupEditDraft(prev => ({ ...prev, thinkIgnore: event.target.checked }))} />
                      <span>Ignore think time <ChangedBadge changed={groupEditChanges.think_time} /></span>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.continue_on_error)}>
                      <span>Continue on error <ChangedBadge changed={groupEditChanges.continue_on_error} /></span>
                      <select className="settings-input" value={groupEditDraft.continue_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, continue_on_error: event.target.value }))}>
                        <option value="">Script default</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.snapshot_on_error)}>
                      <span>Snapshot on error <ChangedBadge changed={groupEditChanges.snapshot_on_error} /></span>
                      <select className="settings-input" value={groupEditDraft.snapshot_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, snapshot_on_error: event.target.value }))}>
                        <option value="">Script default</option>
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
                <button type="button" className="btn btn-primary" onClick={saveGroupOverride} disabled={!hasGroupEditChanges || Boolean(groupEditEnvelopeError)}>Save For This Run</button>
              </div>
            </div>
          </div>
        </div>,
        document.body,
      )}

      {confirmDeleteScheduleId && (
        <div className="modal-overlay" {...dialogBackdropProps(() => setConfirmDeleteScheduleId(null), 'Close schedule deletion dialog')}>
          <div
            ref={deleteScheduleDialogRef}
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="new-test-delete-schedule-dialog-title"
          >
            <div className="modal-header">
              <h3 id="new-test-delete-schedule-dialog-title">Delete Schedule</h3>
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
