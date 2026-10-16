import { countLabel as formatCount } from '../utils/numberFormat'
import { validateGoalConfig } from './goalConfigValidation'
import RunMonitorBindingSelector from './RunMonitorBindingSelector'
import type { MonitorBinding, MonitorRule } from '../types/runMonitoring'
import MonitorRuleEditor from './MonitorRuleEditor'
import ObjectHistoryDialog from './ObjectHistoryDialog'
import RowActions from './ux/RowActions'
import ObjectArchiveControls from './ObjectArchiveControls'
import ThinkTimePercentageInputs from './ThinkTimePercentageInputs'
import { suggestedApplication, suggestedReleases } from './libraryLaunchAssignments'
import CaptureSelectionHelp from './CaptureSelectionHelp'
import { legacyELKCaptureReady, captureSelectionNeedsReview, dynatraceCapabilities } from './captureConnectionModel'
import type { StoredScript } from '../types/storedContent'
import FormErrorSummary from './ux/FormErrorSummary'
import FormField from './ux/FormField'
import DialogLayer from './ux/DialogLayer'
import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal, flushSync } from 'react-dom'
import LoadPlanChart from './LoadPlanChart'
import { LOAD_PLAN_COLORS } from './loadPlanSteps'
import { Application, ApplicationRepository, CapacityTimeslot, DashboardAuthEffectiveAccessResource, DashboardScheduleDefaultsSettings, DraftCapacityEstimate, DynatraceConnection, ELKConnection, GeneratorAssignmentSettings, LaunchPreflightResponse, Release, RuntimeSettingsOverride, ScenarioSchedule, ScenarioScheduleOccurrence, ScenarioScheduleWindow, StoredScenario, SystemInventoryGenerator, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatUpTo } from '../utils/numberFormat'
import { formatTimeCompact, isValidDurationInput, isValidNonNegativeDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import { listAllCapacityTimeslots } from './schedulingCatalogModel'
import { SortIndicator } from './SortIndicator'
import {
  datetimeLocalToISO,
  describeCron,
  firstLaunchConflict,
  formatDatetimeLocalValue,
  formatLaunchConflict,
  formatLaunchConflicts,
  startNowBlocker,
  formatLaunchDecision,
  formatLaunchWindow,
  formatNextBlockingEvent,
  formatReservationWindow,
  formatScheduleInstant,
  formatScheduleInstantForViewer,
  formatScheduleToken,
  formatScheduleWindow,
  formatTimeslotLabel,
  reservationStartISO,
  scheduleStatusTone,
} from './newTestScheduleFormat'
import ErrorDiagnostic from './ErrorDiagnostic'
import PageViewToggle from './PageViewToggle'
import GroupPlacementEditor from './GroupPlacementEditor'
import { CronBuilder } from './ScheduleCronBuilder'
import { describeDataBinding } from './scenarioDataBindingModel'
import { ScheduleClockChanges, ScheduleCurrentUtc, ScheduleNextRuns, type SchedulePreviewState } from './ScheduleCadenceFields'
import TimezoneSelect from './ux/TimezoneSelect'
import type { ScheduleClockChange } from '../types/executionPlans'
import { normalizeRequiredLabels } from './assignmentLabels'
import {
  applyGroupErrorHandlingDraftEdit,
  buildEffectiveScenarioGroups,
  buildEmptyRunDefaultOverrides,
  buildGroupEditDraft,
  buildGroupErrorHandlingSectionForRun,
  buildGroupsForRunPayload,
  buildGroupRuntimeTableValues,
  buildRunDefaultCards,
  buildRunDefaultEditDraft,
  buildRunLoadProfilePreview,
  buildRuntimeSettingsForRun,
  cloneRuntimeSettingsForRun,
  emptyGroupEditDraft,
  errorHandlingSectionIsPinned,
  errorHandlingSurvivesBooleanClear,
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
  hasRuntimeSettingsInstructions,
  mergeRuntimeSettingsForRun,
  setRuntimeAutoTransactions,
  setRuntimeBrowser,
  setRuntimeIterations,
  setRuntimeLogLevel,
  stagedGroupPeakVUsers,
  stampClearedRuntimeSections,
  type GroupEditDraft,
  type GroupRunOverride,
  type RunDefaultEditDraft,
  type RunDefaultKey,
  type RunDefaultOverrides,
  type RunRuntimeOverrideDraft,
  type ScenarioScriptGroup,
} from './newTestRunModel'
import { scenarioDraftFromStoredScenario, scenarioLoadPreview } from './scenarioAssembly'
import './CreateTestModal.css'
import './NewTestWorkspace.css'
import NewTestReadiness from './NewTestReadiness'
import NewTestScheduleCatalog from './NewTestScheduleCatalog'
import { scenarioIdentity } from './newTestPresentation'
import { readNewTestDraft, saveNewTestDraft, clearNewTestDraft, type NewTestDraft, type ScheduleDraft, type ScheduleWindowDraft } from './newTestDraft'
import RunTraceContextSelector from './RunTraceContextSelector'
import { traceContextErrors, traceContextPayload, traceContextSummary, type RunTraceContextSettings } from './runTraceContext'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { effectiveGroupVUsers, maxVUsersForRole, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import { trafficProfileMappingForGroup, trafficProfilePeak } from './trafficProfilePreviewModel'
import {
  canonicalThinkTimeType,
  thinkTimeConfigForType,
  thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import DurationField from './DurationField'
import { confirmAction } from './ux/confirmAction'
import { settingsHref, workspaceHref } from './settingsNavigation'
import { ApiError } from '../hooks/apiClient'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'
import {
  SECONDS_INPUT_STEP,
} from './runtimeTiming'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import { isLegacyApplicationId, isLegacyReleaseId } from '../utils/legacyPlacement'
import { pacingConfigForSelection, pacingFromEdit, randomPacingDraft } from './pacingEditModel'
import {
  clearNewTestQueueAttempt,
  fingerprintNewTestQueuePayload,
  persistNewTestQueueAttempt,
  readNewTestQueueAttempt,
  type NewTestQueueAttempt,
} from './newTestQueueAttempt'
import { insideRowDisclosure } from './rowDisclosure'

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

// The fields the launch blockers link to.
type LaunchField = 'application' | 'release' | 'scenario' | 'monitoring'

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

const formatProfileTps = (value: number | undefined) => formatUpTo(value ?? 0)

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}



function totalScenarioVUsers(scenario?: StoredScenario | null) {
  return scenario?.scripts.reduce((sum, script) => sum + effectiveGroupVUsers(script), 0) || 0
}

/** A production traffic profile drives the load; its groups' VUsers are only the seed. */
function scenarioTrafficProfile(scenario?: StoredScenario | null) {
  return scenario?.traffic_profile?.mappings.length ? scenario.traffic_profile : undefined
}

/**
 * The VUsers a scenario reaches: a VUser profile's peak plus the groups it
 * leaves static, or a TPS profile's planning estimate (undefined until its
 * ART sizing is complete, since the runtime sets the VUsers).
 */
function scenarioPeakVUsers(scenario?: StoredScenario | null): number | undefined {
  const profile = scenarioTrafficProfile(scenario)
  if (!profile) return totalScenarioVUsers(scenario)
  if (profile.runtime_target === 'tps') return profile.sizing?.complete ? profile.sizing.estimated_peak_vusers : undefined
  const staticVUsers = (scenario?.scripts || []).filter(script => !trafficProfileMappingForGroup(profile, script.name)).reduce((sum, script) => sum + effectiveGroupVUsers(script), 0)
  return Math.round(trafficProfilePeak(profile)) + staticVUsers
}

/** "20 VUsers", or a profile's peak in its own unit. */
function formatScenarioLoad(scenario: StoredScenario) {
  const profile = scenarioTrafficProfile(scenario)
  if (profile?.runtime_target === 'tps') return `peak ${formatProfileTps(trafficProfilePeak(profile))} TPS`
  const vusers = scenarioPeakVUsers(scenario) ?? 0
  return `${profile ? 'peak ' : ''}${vusers.toLocaleString()} VUser${vusers === 1 ? '' : 's'}`
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

function formatScenarioModeShort(scenario: StoredScenario) {
  if (scenarioTrafficProfile(scenario)) return 'Traffic Profile'
  return scenario.scenario_mode === 'goal_oriented' ? 'Goal' : 'VUser'
}

/** Goal and traffic-profile scenarios (TPS or VUser profiles alike) share the accented chip. */
function scenarioModeChipClass(scenario: StoredScenario) {
  return `new-test-scenario-card-mode ${scenario.scenario_mode === 'goal_oriented' || scenarioTrafficProfile(scenario) ? 'goal' : ''}`
}

function formatScenarioWorkloadSummary(scenario: StoredScenario) {
  const groupCount = scenario.scripts.length
  return `${groupCount} group${groupCount === 1 ? '' : 's'} · ${formatScenarioLoad(scenario)}`
}

function formatScenarioReleaseSummary(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  return releases.length ? releases.map(release => release.name).join(', ') : 'No releases'
}

function scenarioSearchText(scenario: StoredScenario) {
  return [
    scenario.name, scenario.id, scenario.ref_key,
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
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
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
  // "Blackout windows" names the list; each entry is a "blackout window".
  const kind = title.replace(/\s+windows$/i, '')
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
            <input aria-label={`${kind} window ${i + 1} name`} className="settings-input schedule-window-name" placeholder="Name (optional)" value={w.name} disabled={disabled} onChange={e => update(i, { name: e.target.value })} />
            <div className="schedule-window-days" role="group" aria-label={`${kind} window ${i + 1} days of week`}>
              {SCHEDULE_WEEKDAYS.map(d => (
                <button type="button" key={d.value} className={`schedule-day-toggle ${w.days_of_week.includes(d.value) ? 'active' : ''}`} disabled={disabled} aria-pressed={w.days_of_week.includes(d.value)} onClick={() => toggleDay(i, d.value)}>{d.label}</button>
              ))}
            </div>
            <input aria-label={`${kind} window ${i + 1} start time`} type="time" className="settings-input schedule-window-time" value={w.start_time} disabled={disabled} onChange={e => update(i, { start_time: e.target.value })} />
            <span className="schedule-window-sep">to</span>
            <input aria-label={`${kind} window ${i + 1} end time`} type="time" className="settings-input schedule-window-time" value={w.end_time} disabled={disabled} onChange={e => update(i, { end_time: e.target.value })} />
            <TimezoneSelect
              ariaLabel={`${kind} window ${i + 1} timezone`}
              className="settings-input schedule-window-timezone"
              value={w.timezone?.trim() || fallbackTimezone.trim() || 'UTC'}
              disabled={disabled}
              onChange={timezone => update(i, { timezone })}
            />
            <button type="button" className="btn btn-ghost btn-sm" disabled={disabled} onClick={() => remove(i)} aria-label={`Remove ${kind.toLowerCase()} window ${i + 1}`}>Remove</button>
          </div>
        ))}
        <button type="button" className="btn btn-secondary btn-sm" disabled={disabled} aria-label={`Add ${kind.toLowerCase()} window`} onClick={add}>+ Add window</button>
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
  // No mode selected means the editor sits on its inherit option ('Script think time' /
  // 'Script default'), so there is no think-time section to build. A mean left behind in the
  // draft must not reconstitute one — that is how the inherit option used to save a FIXED pin
  // instead of clearing the section. 'Ignore think time' is its own explicit pin, so it still
  // produces a section.
  if (!ignore && !canonicalThinkTimeType(baseline?.type)) return undefined
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

export default function NewTestPage({ activeApplication: workspaceApplication, applicationRepository, activeRelease: workspaceRelease, releases: workspaceReleases, initialScenarioId, initialPrefillStartLocal, onSelectTest, onOpenCommandCenter, onCreateRelease, onOpenScenarioBuilder }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectID = activeProject?.id || ''
  const [pageMode, setPageMode] = useState<'launch' | 'schedules'>('launch')
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const { runGuarded } = useNavigationGuardRunner()
  const [scenarioState, setScenarioState] = useState<{ projectID: string; scenarios: StoredScenario[]; loaded: boolean }>({ projectID: '', scenarios: [], loaded: false })
  const scenarioCatalogReady = scenarioState.projectID === activeProjectID && scenarioState.loaded
  const scenarios = scenarioState.projectID === activeProjectID ? scenarioState.scenarios : EMPTY_SCENARIOS
  const [scenarioId, setScenarioId] = useState(initialScenarioId || '')
  const [captureRetry, setCaptureRetry] = useState(0)
  const [elkLoading, setELKLoading] = useState(false)
  const [elkError, setELKError] = useState(false)
  const [elkForbidden, setELKForbidden] = useState(false)
  const [dynatraceLoading, setDynatraceLoading] = useState(false)
  const [dynatraceError, setDynatraceError] = useState(false)
  const [dynatraceForbidden, setDynatraceForbidden] = useState(false)
  const [elkConnections, setELKConnections] = useState<ELKConnection[]>([])
  const [elkConnectionId, setELKConnectionId] = useState('')
  const [dynatraceConnections, setDynatraceConnections] = useState<DynatraceConnection[]>([])
  const [dynatraceConnectionId, setDynatraceConnectionId] = useState('')
  const [monitorBindings, setMonitorBindings] = useState<MonitorBinding[] | undefined>()
  const [traceContext, setTraceContext] = useState<RunTraceContextSettings | undefined>()
  const traceContextNeedsReview = traceContextErrors(traceContext).length > 0
  const [monitorRules, setMonitorRules] = useState<MonitorRule[] | undefined>()
  const [monitorEditorError, setMonitorEditorError] = useState('')
  const [monitorEditorRevision, setMonitorEditorRevision] = useState(0)
  const [elasticMonitorConflict, setElasticMonitorConflict] = useState(false)
  const [inheritedMonitorBindingCount, setInheritedMonitorBindingCount] = useState(0)
  useEffect(() => { setELKConnectionId(''); setDynatraceConnectionId(''); setMonitorBindings(undefined); setMonitorRules(undefined); setELKConnections([]); setDynatraceConnections([]) }, [activeProjectID])
  const captureNeedsReview = captureSelectionNeedsReview(elkConnectionId, elkConnections, elkLoading, elkError) || captureSelectionNeedsReview(dynatraceConnectionId, dynatraceConnections, dynatraceLoading, dynatraceError)
  const [activeApplication, setLaunchApplication] = useState(workspaceApplication)
  const [applications, setApplications] = useState<Application[]>([])
  const [assignmentScripts, setAssignmentScripts] = useState<StoredScript[]>([])
  // The project whose script assignments have loaded (or failed to), so the
  // release default waits for them instead of changing under the user.
  const [assignmentsLoadedFor, setAssignmentsLoadedFor] = useState('')
  const [releaseCatalogRefresh, setReleaseCatalogRefresh] = useState(0)
  const [launchReleaseState, setLaunchReleaseState] = useState<{ applicationId: string; releases: Release[] }>({ applicationId: '', releases: [] })
  const releases = (launchReleaseState.applicationId === activeApplication?.id ? launchReleaseState.releases : activeApplication?.id === workspaceApplication?.id ? workspaceReleases : []).filter(release => !release.archived_at)
  // The legacy bucket files runs that name no release: a select does not offer
  // it as a choice, though a selection already on it stays (and names resolve).
  const releaseChoices = (selected: string) => releases.filter(release => !isLegacyReleaseId(release.id) || release.id === selected)
  const [releaseId, setReleaseId] = useState('')
  const launchProjectRef = useRef(activeProjectID)
  const launchPlacementPinnedRef = useRef(false)
  const explicitPlacementRef = useRef(false)
  const releaseCreationScopeRef = useRef<object>({})
  const releaseCreationPendingRef = useRef(false)
  const [releaseCreating, setReleaseCreating] = useState(false)
  useLayoutEffect(() => {
    releaseCreationScopeRef.current = {}
    releaseCreationPendingRef.current = false
    setReleaseCreating(false)
    return () => { releaseCreationScopeRef.current = {} }
  }, [activeProjectID, activeApplication?.id])
  useEffect(() => {
    const projectChanged = launchProjectRef.current !== activeProjectID
    launchProjectRef.current = activeProjectID
    if (projectChanged) { launchPlacementPinnedRef.current = false; explicitPlacementRef.current = false }
    const workspaceMatches = !workspaceApplication?.project_id || workspaceApplication.project_id === activeProjectID
    // Workspace hydration can briefly publish another application. Once a launch
    // has an explicit choice or assignment suggestion, keep that placement.
    setLaunchApplication(current => !projectChanged && launchPlacementPinnedRef.current && current && (!current.project_id || current.project_id === activeProjectID)
      ? current : workspaceMatches ? workspaceApplication : null)
  }, [workspaceApplication, activeProjectID])
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [effectiveAccessError, setEffectiveAccessError] = useState('')
  // A role that cannot create runs or schedules is not asked for estimates,
  // readiness, schedule previews or capture connections the controller
  // refuses; the page says why.
  const canManageTests = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'), [effectiveAuthAccessResources])
  useEffect(() => {
    let cancelled = false
    setApplications([]); setAssignmentScripts([]); setAssignmentsLoadedFor('')
    void Promise.all([api.listApplications(activeProjectID), api.listScripts(activeProjectID, undefined, { archiveState: 'all' })]).then(([apps, scripts]) => {
      if (!cancelled) { setApplications(apps.applications || []); setAssignmentScripts(scripts.scripts || []) }
    }).catch(() => { /* Workspace selection remains usable if suggestions cannot load. */ })
      .finally(() => { if (!cancelled) setAssignmentsLoadedFor(activeProjectID) })
    return () => { cancelled = true }
  }, [api, activeProjectID])
  useEffect(() => {
    let cancelled = false
    const list = api.listDynatraceConnections
    if (!activeProjectID || typeof list !== 'function') { setDynatraceConnections([]); setDynatraceConnectionId(''); return () => { cancelled = true } }
    setDynatraceLoading(true); setDynatraceError(false); setDynatraceForbidden(false)
    void list(activeProjectID).then(response => { if (cancelled) return; const ready = (response.connections || []).filter(connection => (connection.legacy_capture_status ?? connection.status) === 'ready'); setDynatraceConnections(ready);  }).catch((cause: unknown) => { if (!cancelled) { setDynatraceConnections([]); if (cause instanceof ApiError && cause.status === 403) setDynatraceForbidden(true); else setDynatraceError(true) } }).finally(() => { if (!cancelled) setDynatraceLoading(false) })
    return () => { cancelled = true }
  }, [api, activeProjectID, captureRetry])
  useEffect(() => {
    let cancelled = false
    const listELKConnections = api.listELKConnections
    if (!activeProjectID || typeof listELKConnections !== 'function') {
      setELKConnections([])
      setELKConnectionId('')
      return () => { cancelled = true }
    }
    // The list is the project's editors' (as a run is); a viewer's page says
    // so instead of asking. Until access is known, nothing is asked yet.
    if (!effectiveAccessLoaded) { setELKLoading(true); return () => { cancelled = true } }
    if (!effectiveAccessError && !canManageTests) {
      setELKConnections([]); setELKLoading(false); setELKError(false); setELKForbidden(true)
      return () => { cancelled = true }
    }
    setELKLoading(true); setELKError(false); setELKForbidden(false)
    void listELKConnections(activeProjectID).then(response => {
      if (cancelled) return
      const ready = (response.connections || []).filter(legacyELKCaptureReady)
      setELKConnections(ready)
      // Retain an unavailable selection until the user explicitly reviews it.
    }).catch((cause: unknown) => {
      if (!cancelled) { setELKConnections([]); if (cause instanceof ApiError && cause.status === 403) setELKForbidden(true); else setELKError(true) }
    }).finally(() => { if (!cancelled) setELKLoading(false) })
    return () => { cancelled = true }
  }, [api, activeProjectID, canManageTests, captureRetry, effectiveAccessError, effectiveAccessLoaded])
  useEffect(() => {
    let cancelled = false
    const applicationId = activeApplication?.id
    if (applicationId) void api.listReleases(applicationId).then(response => {
      if (!cancelled) setLaunchReleaseState({ applicationId, releases: response.releases || [] })
    }).catch(() => { if (!cancelled) setLaunchReleaseState({ applicationId, releases: [] }) })
    return () => { cancelled = true }
  }, [api, activeApplication?.id, releaseCatalogRefresh])
  const [schedules, setSchedules] = useState<ScenarioSchedule[]>([])
  // Each saved schedule's daylight-saving changes, by schedule ID.
  const [scheduleClockChanges, setScheduleClockChanges] = useState<Record<string, ScheduleClockChange[]>>({})
  const [scheduleLoading, setScheduleLoading] = useState(false)
  const [scheduleSubmitting, setScheduleSubmitting] = useState(false)
  const [scheduleClock, setScheduleClock] = useState(() => new Date())
  const [editingScheduleId, setEditingScheduleId] = useState<string | null>(null)
  const editingScheduleIdRef = useRef(editingScheduleId)
  editingScheduleIdRef.current = editingScheduleId
  const [expandedScheduleId, setExpandedScheduleId] = useState<string | null>(null)
  const [scheduleOccurrenceLoading, setScheduleOccurrenceLoading] = useState<string | null>(null)
  const [scheduleOccurrences, setScheduleOccurrences] = useState<Record<string, ScenarioScheduleOccurrence[]>>({})
  const [scheduleDefaultsStatus, setScheduleDefaultsStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [scheduleDefaultsRetry, setScheduleDefaultsRetry] = useState(0)
  const [scheduleDefaults, setScheduleDefaults] = useState<DashboardScheduleDefaultsSettings>(DEFAULT_SCHEDULE_DEFAULTS)
  const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [scheduleDraftBaseline, setScheduleDraftBaseline] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [schedulePreview, setSchedulePreview] = useState<(SchedulePreviewState & { key: string }) | null>(null)
  const [runName, setRunName] = useState('')
  const runNameScenarioRef = useRef('')
  const runNameEditedRef = useRef(false)
  const scheduleDraftRef = useRef(scheduleDraft)
  const scheduleBaselineRef = useRef(scheduleDraftBaseline)
  scheduleDraftRef.current = scheduleDraft
  scheduleBaselineRef.current = scheduleDraftBaseline
  const [newReleaseName, setNewReleaseName] = useState('')
  const [scenarioQuery, setScenarioQuery] = useState('')
  const [scenarioReleaseFilter, setScenarioReleaseFilter] = useState('')
  const [scenarioSortField, setScenarioSortField] = useState<ScenarioSortField>('name')
  const [scenarioSortDirection, setScenarioSortDirection] = useState<SortDirection>('asc')
  // Once a scenario is chosen the list folds away and the chosen one's
  // groups show in its card; "Change scenario" opens the list again. A
  // scenario the page picks itself (the first of a fresh entry) keeps the
  // list open until the user chooses or keeps one; one the user arrived with
  // (from the scenario builder or the library) counts as chosen.
  const [chosenScenarioId, setChosenScenarioId] = useState(initialScenarioId || '')
  const [changingScenario, setChangingScenario] = useState(false)
  // The scenario whose history is open, so it never reopens for another.
  const [historyScenarioId, setHistoryScenarioId] = useState('')
  const selectedScenarioHeadingRef = useRef<HTMLHeadingElement | null>(null)
  const scenarioSearchRef = useRef<HTMLInputElement | null>(null)
  const focusAfterScenarioChoiceRef = useRef<'scenario' | 'search' | ''>('')
  const applicationSelectRef = useRef<HTMLSelectElement | null>(null)
  const releaseSelectRef = useRef<HTMLSelectElement | null>(null)
  const newReleaseInputRef = useRef<HTMLInputElement | null>(null)
  const monitoringRef = useRef<HTMLDetailsElement | null>(null)
  const [monitoringOpen, setMonitoringOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const creatingRef = useRef(false)
  const [error, setError] = useState('')
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
  const [estimateCheckedAt, setEstimateCheckedAt] = useState('')
  const [preflightCheckedAt, setPreflightCheckedAt] = useState('')
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateFailed, setEstimateFailed] = useState(false)
  // Duration fields report text they cannot turn into a duration; saving waits for a fix.
  const [invalidDurationFields, setInvalidDurationFields] = useState<Record<string, boolean>>({})
  const reportDurationValidity = useCallback((field: string) => (invalid: boolean) => {
    setInvalidDurationFields(current => current[field] === invalid ? current : { ...current, [field]: invalid })
  }, [])
  const [immediatePreflight, setImmediatePreflight] = useState<LaunchPreflightResponse | null>(null)
  const [immediatePreflightLoading, setImmediatePreflightLoading] = useState(false)
  const [immediatePreflightError, setImmediatePreflightError] = useState('')
  const [scheduledPreflight, setScheduledPreflight] = useState<LaunchPreflightResponse | null>(null)
  const [scheduledPreflightLoading, setScheduledPreflightLoading] = useState(false)
  const [scheduledStartLocal, setScheduledStartLocal] = useState(initialPrefillStartLocal || '')
  const scheduledStartBaselineRef = useRef(initialPrefillStartLocal || '')
  const [effectiveAccessRetryNonce, setEffectiveAccessRetryNonce] = useState(0)
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const [generatorInventoryLoading, setGeneratorInventoryLoading] = useState(false)
  const [generatorInventoryError, setGeneratorInventoryError] = useState('')
  const [inventoryRefresh, setInventoryRefresh] = useState(0)
  const [inventoryCheckedAt, setInventoryCheckedAt] = useState('')
  const [scheduleQuery, setScheduleQuery] = useState('')
  const [catalogRefresh, setCatalogRefresh] = useState(0)
  const [timeslotState, setTimeslotState] = useState<{ projectID: string; timeslots: CapacityTimeslot[] }>({ projectID: '', timeslots: [] })
  const bindableTimeslots = timeslotState.projectID === activeProjectID ? timeslotState.timeslots : EMPTY_BINDABLE_TIMESLOTS
  const [selectedTimeslotId, setSelectedTimeslotId] = useState('')
  const newTestDirtyRef = useRef(false)
  const runDefaultOverridesRef = useRef(runDefaultOverrides)
  const groupRunOverridesRef = useRef(groupRunOverrides)
  const saveDraftRef = useRef<() => boolean>(() => false)
  const [savedDraft, setSavedDraft] = useState<NewTestDraft | null>(null)
  const [restoreDraft, setRestoreDraft] = useState<NewTestDraft | null>(null)
  const [draftNotice, setDraftNotice] = useState('')
  const savePendingRunEditorsRef = useRef<() => boolean>(() => true)
  const resetRunSetupRef = useRef<() => void>(() => undefined)
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

  useLayoutEffect(() => {
    runNameScenarioRef.current = ''
    runNameEditedRef.current = false
    setRunName('')
    setScenarioState({ projectID: activeProjectID, scenarios: [], loaded: false })
    setTimeslotState({ projectID: activeProjectID, timeslots: [] })
    setScenarioId('')
    setSelectedTimeslotId('')
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    setEffectiveAccessError('')
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
      setScenarioState({ projectID: '', scenarios: [], loaded: false })
      setScenarioId('')
      return
    }
    try {
      const res = await api.listScenarios(projectID, 'all')
      if (
        scenarioRequestGenerationRef.current !== requestGeneration
        || activeProjectIDRef.current !== projectID
      ) return
      const next = (res.scenarios || []).filter(item => !item.archived_at || item.id === initialScenarioId || item.id === activeScenarioIDRef.current)
      setScenarioState({ projectID, scenarios: next, loaded: true })
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
    setChosenScenarioId(initialScenarioId)
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
  useEffect(() => {
    let cancelled = false
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    setEffectiveAccessError('')
    const loadEffectiveAccess = async () => {
      if (!activeProject?.id) {
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
      } catch (cause: unknown) {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
          setEffectiveAccessError(getErrorMessage(cause, 'Unable to verify project permissions'))
        }
      } finally {
        if (!cancelled) {
          setEffectiveAccessLoaded(true)
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, activeProject?.domain_id, api, applicationRepository?.id, effectiveAccessRetryNonce])
  useEffect(() => {
    let cancelled = false
    const loadScheduleDefaults = async () => {
      setScheduleDefaultsStatus('loading')
      try {
        const response = await api.getSystemSettings()
        if (!cancelled) {
          setScheduleDefaults(normalizeScheduleDefaults(response.schedule_defaults))
          setScheduleDefaultsStatus('ready')
        }
      } catch {
        if (!cancelled) {
          setScheduleDefaults(DEFAULT_SCHEDULE_DEFAULTS)
          setScheduleDefaultsStatus('error')
        }
      }
    }
    loadScheduleDefaults()
    return () => {
      cancelled = true
    }
  }, [api, scheduleDefaultsRetry])
  useEffect(() => {
    let cancelled = false
    const loadInventory = async () => {
      setGeneratorInventoryLoading(true)
      setGeneratorInventoryError('')
      setInventoryCheckedAt('')
      try {
        const response = await api.getSystemInventory()
        if (!cancelled) {
          setGeneratorInventory(response.generators || [])
          setInventoryCheckedAt(new Date().toLocaleTimeString())
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
  }, [api, inventoryRefresh])

  const scenario = useMemo(() => scenarios.find(item => item.id === scenarioId), [scenarios, scenarioId])
  const scenarioListOpen = !scenario || scenario.id !== chosenScenarioId || changingScenario
  // A scenario without its own assignment takes its application and releases
  // from its scripts' assignments, unknown until they load (or fail to).
  const assignmentsPending = Boolean(scenario && !scenario.library_assignment?.application && scenario.scripts.length && assignmentsLoadedFor !== activeProjectID)
  const archivedDependencies = assignmentScripts.filter(script => script.archived_at && scenario?.scripts.some(group => group.script_id === script.id))
  const suggestedForScenario = useRef('')
  useEffect(() => { suggestedForScenario.current = '' }, [activeProjectID])
  useEffect(() => {
    if (!scenario || restoreDraft || !applications.length) return
    const key = `${activeProjectID}:${scenario.id}`
    if (suggestedForScenario.current === key) return
    if (explicitPlacementRef.current) { suggestedForScenario.current = key; return }
    // Wait for script metadata before deciding whether a scenario has a common application.
    if (assignmentsPending) return
    const target = suggestedApplication(scenario, assignmentScripts)
    suggestedForScenario.current = key
    const application = applications.find(item => item.id === target && item.project_id === activeProjectID)
    if (application) { launchPlacementPinnedRef.current = true; setLaunchApplication(application) }
  }, [scenario, assignmentScripts, assignmentsPending, applications, activeProjectID, restoreDraft, releaseId])
  const highlightedReleases = suggestedReleases(scenario, assignmentScripts, activeApplication?.id || '')
  // The workspace's release is the user's own choice of context, so a run in
  // the workspace's application starts there, unless the scenario is assigned
  // to other releases of that application: then the user picks, with the
  // assigned ones marked. Any other application starts with no release. A
  // release the user picks, creates or restores stays. Until the scenario's
  // script assignments are known there is no default, so a release offered
  // first is never withdrawn once they arrive.
  const defaultReleaseId = !assignmentsPending && activeApplication?.id && activeApplication.id === workspaceApplication?.id && workspaceRelease
    && releases.some(item => item.id === workspaceRelease.id)
    && (highlightedReleases.size === 0 || highlightedReleases.has(workspaceRelease.id))
    ? workspaceRelease.id
    : ''
  const releaseChosenRef = useRef(false)
  useEffect(() => {
    releaseChosenRef.current = false
    // A schedule's release belongs to the application it was chosen in. An
    // untouched new-schedule draft is cleared with its baseline, so it stays
    // untouched and follows the run's release; an edited draft, or a stored
    // schedule being edited, only loses the release, which shows as unsaved.
    const untouched = !editingScheduleIdRef.current && JSON.stringify(scheduleDraftRef.current) === JSON.stringify(scheduleBaselineRef.current)
    setScheduleDraft(current => ({ ...current, release_id: '' }))
    if (untouched) setScheduleDraftBaseline(current => ({ ...current, release_id: '' }))
  }, [activeApplication?.id, activeProjectID])
  // Only the run's release follows the default: an untouched schedule draft
  // is rebuilt from it, and an edited one, or a schedule being edited, keeps
  // its own.
  useEffect(() => {
    if (releaseChosenRef.current) return
    setReleaseId(defaultReleaseId)
  }, [activeApplication?.id, activeProjectID, defaultReleaseId])
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
    replaceScheduleDraft(buildDefaultScheduleDraft())
  }, [activeProjectID, replaceScheduleDraft, scenarioId])

  useEffect(() => {
    if (editingScheduleId || scheduleDefaultsStatus !== 'ready') return
    setScheduleDraft(previous => ({ ...previous, approval_policy: scheduleDefaults.approval_policy || 'auto', missed_run_policy: scheduleDefaults.missed_run_policy || 'skip' }))
    setScheduleDraftBaseline(previous => ({ ...previous, approval_policy: scheduleDefaults.approval_policy || 'auto', missed_run_policy: scheduleDefaults.missed_run_policy || 'skip' }))
  }, [editingScheduleId, scheduleDefaults, scheduleDefaultsStatus])

  const schedulePreviewKey = JSON.stringify([activeProjectID, scenarioId, scheduleDraft.cron_expr, scheduleDraft.timezone, scheduleDraft.blackout_windows, scheduleDraft.maintenance_windows, scheduleDraft.availability_windows, scheduleDraft.disabled_from, scheduleDraft.disabled_until])
  const currentSchedulePreview = schedulePreview?.key === schedulePreviewKey ? schedulePreview : null
  const schedulePreviewPending = Boolean(scenarioId && scheduleDraft.cron_expr.trim() && !currentSchedulePreview)
  const scheduleFormError = !editingScheduleId && scheduleDefaultsStatus !== 'ready' ? 'System scheduling defaults are not verified.'
    : !scheduleDraft.name.trim() ? 'Enter a schedule name.'
    : !scheduleDraft.cron_expr.trim() ? 'Enter a schedule frequency or cron expression.'
      : currentSchedulePreview?.error || ''

  // Live "next runs" preview: re-fetch (debounced) from the backend so the
  // shown fire times use the exact production cron evaluator (tz/DST/windows/
  // pause/Quartz specifiers) and cannot diverge from what the scheduler will do.
  useEffect(() => {
    const cron = scheduleDraft.cron_expr.trim()
    if (!scenarioId || !cron || !canManageTests || typeof api.previewScenarioSchedule !== 'function') {
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
        if (!cancelled) setSchedulePreview({ key: schedulePreviewKey, occurrences: result.occurrences || [], error: '', conflictSummary: result.conflict_summary || '', clockChanges: result.clock_changes || [] })
      }).catch(err => {
        if (!cancelled) setSchedulePreview({ key: schedulePreviewKey, occurrences: [], error: getErrorMessage(err, 'Invalid schedule') })
      })
    }, 400)
    return () => { cancelled = true; window.clearTimeout(handle) }
  }, [api, canManageTests, schedulePreviewKey, scenarioId, scheduleDraft.cron_expr, scheduleDraft.timezone, scheduleDraft.blackout_windows, scheduleDraft.maintenance_windows, scheduleDraft.availability_windows, scheduleDraft.disabled_from, scheduleDraft.disabled_until])

  const release = useMemo(() => releases.find(item => item.id === releaseId), [releases, releaseId])
  const effectiveRunDuration = useMemo(() => getEffectiveRunDuration(scenario, runDefaultOverrides), [scenario, runDefaultOverrides])
  const effectiveResourceBaseline = useMemo(() => getEffectiveResourceBaseline(scenario, runDefaultOverrides), [scenario, runDefaultOverrides])
  const effectiveScenarioGroups = useMemo(() => buildEffectiveScenarioGroups(scenario, groupRunOverrides), [groupRunOverrides, scenario])
  // The effective run (saved scenario with this run's overrides) is only
  // displayed; the launch request still sends just the overrides.
  const runLoadProfilePreview = useMemo(
    () => scenario
      ? scenarioLoadPreview(scenarioDraftFromStoredScenario({ ...scenario, scripts: effectiveScenarioGroups, duration: effectiveRunDuration, resource_baseline: effectiveResourceBaseline }))
      : buildRunLoadProfilePreview([], '', undefined, false),
    [effectiveResourceBaseline, effectiveRunDuration, effectiveScenarioGroups, scenario],
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
  // Clearing a GROUP's error_handling stops the group pinning a section; it does not reach the
  // script's own config while the run level still pins one, because mergeRuntimeSettings then
  // hands the run-level section down instead. Name the level the run would actually inherit from
  // rather than promising a script default the group cannot reach — and when the group's own
  // section carries retry fields this dialog cannot edit, the section survives the clear and
  // keeps executing both booleans as false, so the inherit option cannot be honoured at all.
  // Offering it there is the silent discard this control exists to avoid, so it is disabled and
  // says why instead.
  const groupErrorHandlingInherit = useMemo(() => {
    if (!scenario) return { label: 'Script default', disabled: false }
    const groupRuntime = mergeRuntimeSettingsForRun(
      editingGroup?.runtime_settings,
      editingGroupKey ? groupRunOverrides[editingGroupKey]?.runtime_settings : undefined,
    )
    if (errorHandlingSurvivesBooleanClear(groupRuntime?.error_handling)) {
      return { label: 'Pinned by group retry policy', disabled: true }
    }
    const rootRuntime = buildRuntimeSettingsForRun(scenario, runDefaultOverrides) || scenario.runtime_settings
    return {
      label: errorHandlingSectionIsPinned(rootRuntime?.error_handling) ? 'Scenario default' : 'Script default',
      disabled: false,
    }
  }, [editingGroup, editingGroupKey, groupRunOverrides, runDefaultOverrides, scenario])
  // The readiness review judges the scenario as this run will launch it: the
  // run's defaults and each group's run overrides laid over the stored ones.
  const readinessScenario = useMemo(() => scenario && {
    ...scenario,
    runtime_settings: buildRuntimeSettingsForRun(scenario, runDefaultOverrides) || scenario.runtime_settings,
    scripts: scenario.scripts.map(group => ({ ...group, runtime_settings: mergeRuntimeSettingsForRun(group.runtime_settings, groupRunOverrides[groupOverrideKey(group)]?.runtime_settings) })),
  }, [groupRunOverrides, runDefaultOverrides, scenario])
  // continue_on_error has a scenario-level policy the other error-handling
  // booleans do not: it is enabled unless a layer explicitly disables it. Naming
  // the inherit option "Script default" there would promise a fallback the run
  // never takes, so it says what the group would actually run with instead.
  const continueOnErrorInherit = useMemo(() => {
    if (groupErrorHandlingInherit.disabled) return groupErrorHandlingInherit
    if (!scenario) return { label: 'Scenario default (enabled)', disabled: false }
    const rootRuntime = buildRuntimeSettingsForRun(scenario, runDefaultOverrides) || scenario.runtime_settings
    const scenarioValue = rootRuntime?.error_handling?.continue_on_error
    if (scenarioValue !== undefined) {
      return { label: `Scenario default (${scenarioValue ? 'enabled' : 'disabled'})`, disabled: false }
    }
    return { label: 'Scenario default (enabled)', disabled: false }
  }, [groupErrorHandlingInherit, runDefaultOverrides, scenario])
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
      if (scenarioSortField === 'vusers') return scenarioPeakVUsers(item) ?? -1
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
      setScheduleClockChanges(res.clock_changes || {})
      setCatalogRefresh(value => value + 1)
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
  // Pool names for the launch review's description of each data binding.
  const [dataPoolNames, setDataPoolNames] = useState<Record<string, string>>({})
  const scenarioPoolIds = useMemo(() => [...new Set((scenario?.data_pools || []).map(binding => binding.pool_id).filter(Boolean))].join(','), [scenario?.data_pools])
  useEffect(() => {
    if (!activeProjectID || !scenarioPoolIds) return undefined
    let cancelled = false
    void api.listDataPools(activeProjectID, true).then(response => {
      if (!cancelled) setDataPoolNames(Object.fromEntries((response.data_pools || []).map(pool => [pool.id, pool.name])))
    }).catch(() => undefined)
    return () => { cancelled = true }
  }, [activeProjectID, api, scenarioPoolIds])
  const launchSummaryItems = useMemo(() => {
    const capacityAdvisory = immediatePreflight?.capacity_advisory || draftEstimate?.capacity_advisory
    const bindingSummaries = (scenario?.data_pools || []).map(binding => describeDataBinding(binding, id => dataPoolNames[id]))
    const dataPoolValue = !scenario
      ? 'None'
      : bindingSummaries.length
        ? bindingSummaries.map(item => item.text).join('; ')
        : 'None'
    // Both sources of an estimate failed: say so instead of "pending" forever.
    const estimateUnavailable = estimateFailed && !immediatePreflight && Boolean(immediatePreflightError)
    const launchConflict = firstLaunchConflict(immediatePreflight)
    const launchBlocked = Boolean(immediatePreflight?.blocking_conflicts?.length)
    const capacityValue = !scenario
      ? 'Select a scenario'
      : !effectiveAccessLoaded
        ? 'Checking your access…'
      : effectiveAccessError
        ? 'Not checked: permissions could not be verified'
      : !canManageTests
        ? 'Not checked: your role cannot start runs in this project'
      : immediatePreflightLoading || estimateLoading
        ? 'Refreshing estimate…'
        : capacityAdvisory
          ? `${formatCount(capacityAdvisory.recommended_generators, 'generator')} estimated; ${capacityAdvisory.available_generators} targets in the capacity estimate. Check live host inventory below. Updated ${immediatePreflight?.capacity_advisory ? preflightCheckedAt : estimateCheckedAt}.`
          : estimateUnavailable
            ? 'Estimate unavailable. Use Refresh host inventory to retry.'
            : 'Pending estimate'
    const bottleneckValue = !scenario || !canManageTests
      ? 'Not available'
      : immediatePreflightLoading || estimateLoading
        ? 'Refreshing…'
        : capacityAdvisory
          ? `${capacityAdvisory.bottleneck}, ${capacityAdvisory.headroom_pct}% headroom`
          : estimateUnavailable ? 'Estimate unavailable' : 'Pending estimate'

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
      { label: 'Data pools', value: dataPoolValue, tone: bindingSummaries.some(item => item.writesBack) ? 'warning' : 'normal' },
      { label: 'Capacity estimate', value: capacityValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Estimated bottleneck', value: bottleneckValue, tone: capacityAdvisory?.requires_override ? 'warning' : 'normal' },
      { label: 'Preflight Decision', value: formatLaunchDecision(immediatePreflight, immediatePreflightLoading), tone: launchBlocked ? 'warning' : 'normal' },
      { label: `Proposed launch window (${Intl.DateTimeFormat().resolvedOptions().timeZone})`, value: formatReservationWindow(immediatePreflight) },
      { label: 'Conflicts', value: formatLaunchConflicts(immediatePreflight), tone: launchConflict ? 'warning' : 'normal' },
      { label: 'Next Blocking Event', value: formatNextBlockingEvent(immediatePreflight), tone: launchBlocked ? 'warning' : 'normal' },
    ]
  }, [canManageTests, dataPoolNames, effectiveAccessError, effectiveAccessLoaded, estimateCheckedAt, preflightCheckedAt, draftEstimate?.capacity_advisory, effectiveRunDuration, effectiveScenarioGroups.length, estimateFailed, estimateLoading, groupRunOverrides, immediatePreflight, immediatePreflightError, immediatePreflightLoading, protocols, release?.name, runName, runOverridesCount, scenario, totalVUsers])
  const readiness = useMemo(() => {
    const checks: Array<{ label: string; ready: boolean; field: LaunchField }> = [
      { label: activeApplication ? `Application: ${activeApplication.name}` : 'Select an application', ready: !!activeApplication, field: 'application' },
      { label: release ? `Release: ${release.name}` : 'Select or create a release', ready: !!release, field: 'release' },
      { label: scenario ? `Scenario: ${scenario.name}` : 'Select a scenario', ready: !!scenario, field: 'scenario' },
      { label: scenario?.scripts.length ? `${scenario.scripts.length} script group${scenario.scripts.length === 1 ? '' : 's'} ready` : 'Scenario needs at least one script group', ready: !!scenario?.scripts.length, field: 'scenario' },
    ]
    return checks
  }, [activeApplication, release, scenario])
  const canManageSchedules = canManageTests && !scenario?.archived_at
  const canManageProjects = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects'), [effectiveAuthAccessResources])
  const timeslotValidationError = selectedTimeslotId && !bindableTimeslots.some(slot => slot.id === selectedTimeslotId) ? 'The saved capacity reservation is unavailable. Refresh capacity or explicitly choose another reservation before launching.' : ''
  const runSetupValidationError = useMemo(
    () => timeslotValidationError || validateRunSetup(scenario, effectiveScenarioGroups),
    [effectiveScenarioGroups, scenario, timeslotValidationError],
  )
  const monitorRuleValidationError = monitorEditorError || validateGoalConfig({ monitor_rules: monitorRules ?? scenario?.goal_config?.monitor_rules })
  // The first reasons a launch is unavailable, shown beside the disabled
  // launch buttons instead of leaving them silently greyed out. One the user
  // can fix on this page links to its field.
  const launchBlockers: Array<{ label: string; field?: LaunchField }> = [
    ...readiness.filter(item => !item.ready).map(item => ({ label: item.label, field: item.field })),
    ...(scenario?.archived_at ? [{ label: 'This scenario is archived' }] : []),
    ...(archivedDependencies.length ? [{ label: 'A script in this scenario is archived' }] : []),
    ...(!canManageTests ? [{ label: 'Your role cannot start runs in this project' }] : []),
    ...(runSetupValidationError ? [{ label: runSetupValidationError }] : []),
    ...(monitorRuleValidationError ? [{ label: 'Fix the monitor rules', field: 'monitoring' as const }] : []),
    ...(elasticMonitorConflict ? [{ label: 'Resolve the Elastic capture conflict', field: 'monitoring' as const }] : []),
    ...(captureNeedsReview ? [{ label: 'Review the telemetry capture selection', field: 'monitoring' as const }] : []),
    ...(traceContextNeedsReview ? [{ label: 'Fix the trace context settings', field: 'monitoring' as const }] : []),
  ]
  const monitoringNeedsReview = elasticMonitorConflict || Boolean(monitorRuleValidationError) || captureNeedsReview || traceContextNeedsReview
  const monitorRuleCount = (monitorRules ?? scenario?.goal_config?.monitor_rules ?? []).length
  // Bindings left to the scenario still apply, so they count as the run's.
  const monitorBindingCount = monitorBindings === undefined ? inheritedMonitorBindingCount : monitorBindings.length
  const monitoringSummary = monitoringNeedsReview ? 'Needs review' : [
    monitorBindingCount ? `${monitorBindingCount} monitor binding${monitorBindingCount === 1 ? '' : 's'}${monitorBindings === undefined ? ' from the scenario' : ''}` : '',
    monitorRuleCount ? `${monitorRuleCount} monitor rule${monitorRuleCount === 1 ? '' : 's'}` : '',
    elkConnectionId ? `Elastic: ${elkConnections.find(connection => connection.id === elkConnectionId)?.name || 'selected'}` : '',
    dynatraceConnectionId ? `Dynatrace: ${dynatraceConnections.find(connection => connection.id === dynatraceConnectionId)?.name || 'selected'}` : '',
    traceContextPayload(traceContext) ? 'Trace context' : '',
  ].filter(Boolean).join(' · ') || 'None for this run'
  const focusLaunchField = (field: LaunchField) => {
    if (field === 'scenario') {
      if (scenarioListOpen) scenarioSearchRef.current?.focus()
      else changeScenario()
      return
    }
    if (field === 'monitoring') {
      // Opened on the element too: a section the user folded while it needed
      // review keeps an open prop of true, which React would not re-apply.
      if (monitoringRef.current) monitoringRef.current.open = true
      setMonitoringOpen(true)
      monitoringRef.current?.querySelector('summary')?.focus()
      return
    }
    const target = field === 'application' ? applicationSelectRef.current : showNewRelease ? newReleaseInputRef.current : releaseSelectRef.current
    target?.focus()
  }
  const canLaunch = !elasticMonitorConflict && !monitorRuleValidationError && !scenario?.archived_at && archivedDependencies.length === 0 && readiness.every(item => item.ready) && !creating && canManageTests && !runSetupValidationError && !captureNeedsReview && !traceContextNeedsReview
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
      termination_mode: scenario.termination_mode,
      application_id: activeApplication.id,
      release_id: releaseId || undefined,
      scenario_id: scenario.id,
      project_id: activeProject?.id,
      ...(runtimeSettings !== undefined ? { runtime_settings: runtimeSettings } : {}),
      ...(groupPayloads ? { groups: groupPayloads } : {}),
      ...(hasOwnKey(currentRunDefaults, 'script_shakeout') ? { script_shakeout: Boolean(currentRunDefaults.script_shakeout) } : {}),
      ...(hasOwnKey(currentRunDefaults, 'resource_baseline') ? { resource_baseline: Boolean(currentRunDefaults.resource_baseline) } : {}),
      ...(elkConnectionId ? { elk_capture: { connection_id: elkConnectionId } } : {}),
	  ...(dynatraceConnectionId ? { dynatrace_capture: { connection_id: dynatraceConnectionId } } : {}),
      ...(monitorBindings !== undefined ? { monitor_bindings: monitorBindings } : {}),
      ...(traceContextPayload(traceContext) ? { trace_context: traceContextPayload(traceContext) } : {}),
      ...(monitorRules !== undefined ? { goal_config: { ...scenario.goal_config, monitor_rules: monitorRules } } : {}),
    }
  }, [activeApplication, activeProject?.id, dynatraceConnectionId, elkConnectionId, monitorBindings, monitorRules, releaseId, runName, scenario, traceContext])
  const runCreatePayload = useMemo(
    () => buildCurrentRunCreatePayload(runDefaultOverrides, groupRunOverrides),
    [buildCurrentRunCreatePayload, groupRunOverrides, runDefaultOverrides],
  )
  const enteredScheduledStartISO = useMemo(() => datetimeLocalToISO(scheduledStartLocal), [scheduledStartLocal])
  // A start in the past is reported beside the field; it is never sent to
  // preflight, which would otherwise call the (already elapsed) window clear.
  const scheduledStartInPast = Boolean(enteredScheduledStartISO && Date.parse(enteredScheduledStartISO) <= Date.now())
  const scheduledStartISO = scheduledStartInPast ? '' : enteredScheduledStartISO
  const runNameScenarioIdentity = scenario ? `${activeProjectID}:${scenario.id}` : ''
  const runNameScenarioDefault = scenario?.name || ''

  useEffect(() => {
    // A name can be entered while the selected scenario is still loading.
    // Loading (or refreshing) its metadata must not overwrite that edit.
    if (!runNameScenarioIdentity || runNameScenarioRef.current === runNameScenarioIdentity) return
    if (runNameScenarioRef.current) runNameEditedRef.current = false
    runNameScenarioRef.current = runNameScenarioIdentity
    if (!runNameEditedRef.current) setRunName(runNameScenarioDefault)
  }, [runNameScenarioDefault, runNameScenarioIdentity])
  // Focus follows the scenario choice: to the chosen scenario's card once the
  // list folds away, or to the search when the list opens to change it.
  useEffect(() => {
    const target = focusAfterScenarioChoiceRef.current
    if (!target) return
    if (target === 'scenario' && scenario && !scenarioListOpen) {
      focusAfterScenarioChoiceRef.current = ''
      selectedScenarioHeadingRef.current?.focus()
    } else if (target === 'search' && scenarioListOpen) {
      focusAfterScenarioChoiceRef.current = ''
      scenarioSearchRef.current?.focus()
    }
  }, [scenario, scenarioListOpen])
  useEffect(() => {
    void refreshSchedules(scenarioId)
  }, [refreshSchedules, scenarioId])
  useEffect(() => {
    if (!editingScheduleId && JSON.stringify(scheduleDraftRef.current) === JSON.stringify(scheduleBaselineRef.current)) {
      replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
    }
  }, [editingScheduleId, releaseId, replaceScheduleDraft, scenario, scheduleDefaults])

  useEffect(() => {
    if (!runCreatePayload || !canManageTests) {
      setDraftEstimate(null)
      return
    }
    let cancelled = false
    setEstimateLoading(true)
    setEstimateFailed(false)
    const timer = window.setTimeout(async () => {
      try {
        const estimate = await api.estimateTest(runCreatePayload)
        if (!cancelled) { setDraftEstimate(estimate); setEstimateCheckedAt(new Date().toLocaleTimeString()) }
      } catch {
        if (!cancelled) { setDraftEstimate(null); setEstimateFailed(true) }
      } finally {
        if (!cancelled) setEstimateLoading(false)
      }
    }, 400)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [api, canManageTests, runCreatePayload, inventoryRefresh])

  useEffect(() => {
    if (!runCreatePayload || !canManageTests) {
      setImmediatePreflightError('')
      setImmediatePreflight(null)
      setImmediatePreflightLoading(false)
      return
    }
    let cancelled = false
    setImmediatePreflight(null)
    setImmediatePreflightError('')
    setImmediatePreflightLoading(true)
    const timer = window.setTimeout(async () => {
      try {
        const preflight = await api.preflightTest({ ...runCreatePayload, intent: 'immediate' })
        if (!cancelled) { setImmediatePreflight(preflight); setPreflightCheckedAt(new Date().toLocaleTimeString()) }
      } catch (cause: unknown) {
        if (!cancelled) {
          setImmediatePreflight(null)
          setImmediatePreflightError(`Launch readiness could not be checked (${getErrorMessage(cause, 'no response')}). Use Refresh host inventory to retry.`)
        }
      } finally {
        if (!cancelled) setImmediatePreflightLoading(false)
      }
    }, 400)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [api, canManageTests, runCreatePayload, inventoryRefresh])

  useEffect(() => {
    if (!runCreatePayload || !scheduledStartISO || !canManageTests) {
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
  }, [api, canManageTests, runCreatePayload, scheduledStartISO])

  const step1Complete = Boolean(scenario && scenario.scripts.length > 0)
  const step2Complete = Boolean(activeApplication && release && runName.trim())
  const step3Complete = step1Complete

  const createRelease = async () => {
    const name = newReleaseName.trim()
    if (!name || !activeApplication || releaseCreationPendingRef.current) return
    const scope = releaseCreationScopeRef.current
    releaseCreationPendingRef.current = true
    setReleaseCreating(true)
    setError('')
    launchPlacementPinnedRef.current = true
    explicitPlacementRef.current = true
    try {
      const created = activeApplication.id !== workspaceApplication?.id
        ? await api.createRelease(activeApplication.id, { name }) : await onCreateRelease(name)
      if (scope !== releaseCreationScopeRef.current) return
      if (created && created.application_id === activeApplication.id) {
        setLaunchReleaseState(current => ({ applicationId: created.application_id, releases: [...(current.applicationId === created.application_id ? current.releases : releases).filter(item => item.id !== created.id), created] }))
        releaseChosenRef.current = true
        setReleaseId(created.id)
        // Supersede any list read started before creation, then load the full
        // current catalog so a late response cannot remove the new selection.
        setReleaseCatalogRefresh(value => value + 1)
        setNewReleaseName('')
      }
    } catch (error) {
      if (scope === releaseCreationScopeRef.current) setError(getErrorMessage(error, 'Unable to create release'))
    } finally {
      if (scope === releaseCreationScopeRef.current) {
        releaseCreationPendingRef.current = false
        setReleaseCreating(false)
      }
    }
  }

  // Keeping the current scenario (choosing it again) folds the list away.
  const keepScenario = () => {
    if (!scenario) return
    focusAfterScenarioChoiceRef.current = 'scenario'
    setChosenScenarioId(scenario.id)
    setChangingScenario(false)
  }
  const changeScenario = () => {
    focusAfterScenarioChoiceRef.current = 'search'
    setChangingScenario(true)
  }

  const selectScenario = (nextScenarioId: string) => {
    if (nextScenarioId === scenarioId) {
      keepScenario()
      return
    }
    runGuarded(() => {
      focusAfterScenarioChoiceRef.current = 'scenario'
      setScenarioId(nextScenarioId)
      setChosenScenarioId(nextScenarioId)
      setChangingScenario(false)
      runNameScenarioRef.current = ''
      runNameEditedRef.current = false
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

  const createRun = async (intent: 'start' | 'reserve' | 'queue' | 'schedule', options: { navigate?: boolean } = {}): Promise<boolean> => {
    if (elasticMonitorConflict) { setError('Turn legacy Elastic log capture off or remove the conflicting run-bound source before launching.'); return false }
    if (monitorRuleValidationError) { setError(monitorRuleValidationError); return false }
    if (captureNeedsReview) { setError('Review the unavailable capture connection, retry, or explicitly turn capture off before launching.'); return false }
    if (traceContextNeedsReview) { setError('Fix the trace context origins, or turn trace context off, before launching.'); return false }
    if (creatingRef.current) return false
    const navigate = options.navigate ?? true
    const currentRunDefaults = runDefaultOverridesRef.current
    const currentGroupOverrides = groupRunOverridesRef.current
    const currentRunCreatePayload = buildCurrentRunCreatePayload(currentRunDefaults, currentGroupOverrides)
    const currentEffectiveGroups = buildEffectiveScenarioGroups(scenario, currentGroupOverrides)
    const currentRunSetupValidationError = timeslotValidationError || validateRunSetup(scenario, currentEffectiveGroups)
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
    creatingRef.current = true
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
      const openCreatedRun = (id: string, commandCenter = true) => {
        clearNewTestDraft(activeProjectID, activeApplication.id)
        setSavedDraft(null)
        // The submitted form is durable; clear its guard before navigating.
        flushSync(resetRunSetup)
        ;(commandCenter ? (onOpenCommandCenter || onSelectTest) : onSelectTest)(id)
      }
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
          if (navigate) openCreatedRun(test.id)
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
            if (navigate) openCreatedRun(recoveredAfterStart.id)
            return true
          }
          // Creation is already durable. Hand the known run to the execution
          // workspace even if start was rejected or its acknowledgement was
          // lost; retrying Queue Now must never create a second run.
          setQueueRecovery({ attempt: resolvedQueueAttempt, status: 'ready', test: recoveredAfterStart || test })
          setError(`${getErrorMessage(startError, 'Unable to start the created run')} The queued run was preserved for recovery.`)
          if (navigate) openCreatedRun(test.id)
          return true
        }
        resolveQueueAttempt()
        if (navigate) openCreatedRun(test.id)
        return true
      }
      if (intent === 'start') {
        await api.startTest(test.id)
        if (navigate) openCreatedRun(test.id)
        return true
      }
      if (navigate && intent === 'reserve') {
        ;openCreatedRun(test.id)
        return true
      }
      if (navigate) openCreatedRun(test.id, false)
      return true
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to create run'))
      return false
    } finally {
      creatingRef.current = false
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
    setELKConnectionId('')
	setDynatraceConnectionId('')
    setMonitorBindings(undefined)
    setMonitorRules(undefined)
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
    runNameEditedRef.current = false
    setRunName(scenario?.name || '')
    releaseChosenRef.current = false
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
    setELKConnectionId('')
	setDynatraceConnectionId('')
    setMonitorBindings(undefined)
    setMonitorRules(undefined)
    setMonitorEditorError('')
    setMonitorEditorRevision(value => value + 1)
    scheduledStartBaselineRef.current = ''
    setScheduledStartLocal('')
  }, [defaultReleaseId, scenario?.name])

  const resetScheduleSetup = useCallback(() => {
    setEditingScheduleId(null)
    setExpandedScheduleId(null)
    setScheduleOccurrenceLoading(null)
    replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
  }, [releaseId, replaceScheduleDraft, scenario, scheduleDefaults])

  const runNameDirty = Boolean(scenario && runName !== scenario.name)
  // Only a release someone chose (picked, created or restored) is an edit:
  // until the default-following effect runs, an untouched page's release
  // briefly differs from a default that just resolved, and leaving then must
  // not ask about unsaved changes.
  const releaseDirty = releaseChosenRef.current && releaseId !== defaultReleaseId
  const pendingDefaultDirty = Boolean(editingDefaultKey && hasDefaultEditChanges)
  const pendingGroupDirty = Boolean(editingGroupKey && hasGroupEditChanges)
  const newTestDirty = Boolean(
    runNameDirty
    || releaseDirty
    || runOverridesCount > 0
    || selectedTimeslotId
    || elkConnectionId
	|| dynatraceConnectionId
    || monitorBindings !== undefined
    || traceContext !== undefined
    || monitorRules !== undefined
    || Boolean(monitorEditorError)
    || scheduledStartLocal !== scheduledStartBaselineRef.current
    || newReleaseName.trim()
    || pendingDefaultDirty
    || pendingGroupDirty
  )
  const scheduleDraftDirty = JSON.stringify(scheduleDraft) !== JSON.stringify(scheduleDraftBaseline)
  const buildCurrentDraft = (): NewTestDraft => ({
      version: 1, projectId: activeProjectID, applicationId: activeApplication?.id || '',
      scenarioId: scenario?.id || '', scenarioUpdatedAt: scenario?.updated_at || '', pageMode,
      runName, releaseId, runDefaultOverrides: runDefaultOverridesRef.current,
      groupRunOverrides: groupRunOverridesRef.current, scheduledStartLocal, selectedTimeslotId, elkConnectionId, dynatraceConnectionId, monitorBindings, traceContext, monitorRules, newReleaseName, showNewRelease,
      scheduleDraft, editingScheduleId,
    })
  const savedDraftMatches = savedDraft && JSON.stringify({ ...savedDraft, pageMode: null }) === JSON.stringify({ ...buildCurrentDraft(), pageMode: null })
  const hasUnsavedChanges = Boolean(monitorEditorError) || (newTestDirty || scheduleDraftDirty) && (!savedDraftMatches || pendingDefaultDirty || pendingGroupDirty)
  saveDraftRef.current = () => {
    if (monitorEditorError) { setError(monitorEditorError); return false }
    if (!savePendingRunEditorsRef.current() || !activeProjectID || !activeApplication || !scenario) return false
    const draft = buildCurrentDraft()
    try {
      saveNewTestDraft(draft)
      setSavedDraft(draft)
      setError('')
      setDraftNotice('Draft saved in this browser tab. No run or recurring trigger was created.')
      return true
    } catch {
      setError('Draft could not be saved in this browser. Keep this page open and try again.')
      return false
    }
  }
  resetRunSetupRef.current = resetRunSetup
  resetScheduleSetupRef.current = resetScheduleSetup
  newTestDirtyRef.current = hasUnsavedChanges && newTestDirty
  scheduleDirtyRef.current = hasUnsavedChanges && scheduleDraftDirty
  const newTestGuard = useMemo<NavigationGuard>(() => ({
    id: 'new-test-run-setup',
    label: 'New run draft (saving does not create or start a run)',
    isDirty: () => newTestDirtyRef.current || scheduleDirtyRef.current,
    save: () => saveDraftRef.current(),
    discard: () => {
      if (scheduleDirtyRef.current) resetScheduleSetupRef.current()
      if (newTestDirtyRef.current) resetRunSetupRef.current()
    },
  }), [])
  useNavigationGuard(newTestGuard, hasUnsavedChanges)

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
      if (invalidDurationFields.duration) {
        setError('Run duration must be a positive Go duration such as 30s, 5m, or 1h30m.')
        return false
      }
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
    setDefaultSaveNotice(`Saved ${editingDefaultCard.label.charAt(0).toLowerCase()}${editingDefaultCard.label.slice(1)} for this run only. Scenario defaults were not changed.`)
    setEditingDefaultKey(null)
    return true
  }

  const renderRunDefaultEditorControl = () => {
    if (!editingDefaultKey) return null
    if (editingDefaultKey === 'duration') {
      // The same number + unit control the scenario builder uses for durations.
      return (
        <DurationField
          label="Run duration"
          required
          onInvalidChange={reportDurationValidity('duration')}
          value={defaultEditDraft.value}
          onChange={value => { setError(''); setDefaultEditDraft(prev => ({ ...prev, value })) }}
        />
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
                // Same reconstitution hazard as the group dialog: 'Script default' has to drop
                // the mean, or thinkTimeFromEdit rebuilds a fixed think time from it and the
                // run-default override pins instead of clearing.
                value: type && type !== 'recorded' ? prev.value : '',
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
              <option value="random_percentage">Random percentage of recorded think time</option>
              <option value="fixed">Fixed</option>
              <option value="gaussian">Gaussian</option>
              <option value="uniform">Uniform</option>
            </select>
          </label>
          {thinkType === 'random_percentage' && <ThinkTimePercentageInputs value={defaultEditDraft.thinkTimeConfig ?? undefined} disabled={defaultEditDraft.thinkIgnore} onChange={updateThinkConfig} />}
          {Boolean(thinkType) && thinkTimeUsesMean(thinkType) && (
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
            <option value="">{editingDefaultKey === 'continue_on_error' ? 'Scenario default (enabled)' : 'Script default'}</option>
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
    // Text a duration field could not read leaves its draft empty, so check
    // the fields themselves before "no changes" can close the dialog.
    if (invalidDurationFields.ramp_up) {
      setError('Ramp up must be a non-negative Go duration such as 0s, 30s, or 5m.')
      return false
    }
    if (invalidDurationFields.ramp_down) {
      setError('Ramp down must be a non-negative Go duration such as 0s, 30s, or 5m.')
      return false
    }
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
      // not even a half the winner omits. buildGroupErrorHandlingSectionForRun owns that rule for
      // every combination of the two halves; the section it declines to build is dropped here and
      // tombstoned below, which is how the group stops pinning error handling at all.
      const errorHandling = buildGroupErrorHandlingSectionForRun({
        groupSection: runtimeSettings.error_handling,
        inheritedSection: effectiveRootRuntime.error_handling,
        draft: {
          continue_on_error: groupEditDraft.continue_on_error,
          snapshot_on_error: groupEditDraft.snapshot_on_error,
        },
        changed: {
          continue_on_error: groupEditChanges.continue_on_error,
          snapshot_on_error: groupEditChanges.snapshot_on_error,
        },
      })
      if (errorHandling) runtimeSettings.error_handling = errorHandling
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
      }, { ancestorPinsBrowserConfig: Boolean(effectiveRootRuntime.browser_config) })
      runtimeChanged = true
    }
    // buildGroupsForRunPayload overlays this override back onto the group's STORED
    // runtime_settings, and an overlay can only assign — an absent key means "unedited", so it
    // can never remove the stored section. Every section this dialog can clear therefore has to
    // be written as an explicit null tombstone that mergeRuntimeSettingsForRun deletes on, and
    // the list of them lives in CLEARABLE_RUNTIME_SECTIONS so a newly clearable section cannot
    // be missed here. This must run AFTER every section edit above (the browser block included,
    // which is where the four browser fields delete their section). Re-stating it on every save
    // also keeps the clear across a second save: the merge above already applied the earlier
    // tombstone, so the key is missing again and would otherwise be silently forgotten,
    // restoring the stored section.
    stampClearedRuntimeSections(runtimeSettings, editingGroup.runtime_settings)
    if ((runtimeChanged || existingOverride?.runtime_settings) && hasRuntimeSettingsInstructions(runtimeSettings)) {
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
	if (!canManageSchedules) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return false
	}
	if (!scenario) {
	  setError('Choose a scenario before managing schedules.')
	  return false
	}
	if (!editingScheduleId && !scheduleDraft.release_id) { setError('Choose a release for the new schedule.'); return false }
	if (schedulePreviewPending || scheduleFormError) {
      setError(scheduleFormError || 'Wait for the schedule preview before saving.')
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
	if (!canManageSchedules) {
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

  // Uses the same destructive confirmation as every other library delete.
  const requestDeleteSchedule = async (schedule: { id: string; name?: string }) => {
    if (!(await confirmAction(`Permanently delete schedule “${schedule.name || 'this schedule'}”? Future runs managed by this schedule will no longer fire. This cannot be undone.`, { title: 'Delete schedule', confirmLabel: 'Delete schedule', destructive: true }))) return
    await deleteSchedule(schedule.id)
  }

  const deleteSchedule = async (scheduleId: string) => {
	if (!canManageSchedules) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	const scope = { projectID: activeProjectID, scenarioID: scenario.id, generation: scheduleScopeGenerationRef.current }
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


  useEffect(() => {
    setSavedDraft(readNewTestDraft(activeProjectID, activeApplication?.id || ''))
    setRestoreDraft(null)
    setDraftNotice('')
  }, [activeProjectID, activeApplication?.id])
  useEffect(() => {
    if (!restoreDraft || !scenario || restoreDraft.scenarioId !== scenario.id) return
    if (restoreDraft.projectId !== activeProjectID || restoreDraft.applicationId !== activeApplication?.id) return
    runDefaultOverridesRef.current = restoreDraft.runDefaultOverrides
    groupRunOverridesRef.current = restoreDraft.groupRunOverrides
    setRunDefaultOverrides(restoreDraft.runDefaultOverrides)
    setGroupRunOverrides(restoreDraft.groupRunOverrides)
    setNewReleaseName(restoreDraft.newReleaseName)
    setShowNewRelease(restoreDraft.showNewRelease)
    runNameEditedRef.current = true
    setRunName(restoreDraft.runName)
    launchPlacementPinnedRef.current = true
    explicitPlacementRef.current = true
    releaseChosenRef.current = true
    setReleaseId(restoreDraft.releaseId)
    setScheduledStartLocal(restoreDraft.scheduledStartLocal)
    setSelectedTimeslotId(restoreDraft.selectedTimeslotId)
    setELKConnectionId(restoreDraft.elkConnectionId || '')
	setDynatraceConnectionId(restoreDraft.dynatraceConnectionId || '')
    setMonitorBindings(restoreDraft.monitorBindings)
    setTraceContext(restoreDraft.traceContext)
    setMonitorRules(restoreDraft.monitorRules)
    setScheduleDraft(restoreDraft.editingScheduleId ? restoreDraft.scheduleDraft : { ...restoreDraft.scheduleDraft, approval_policy: scheduleDefaults.approval_policy || 'auto', missed_run_policy: scheduleDefaults.missed_run_policy || 'skip' })
    setEditingScheduleId(restoreDraft.editingScheduleId)
    setPageMode(restoreDraft.pageMode)
    setChosenScenarioId(scenario.id)
    setChangingScenario(false)
    suggestedForScenario.current = `${activeProjectID}:${scenario.id}`
    setRestoreDraft(null)
    setDraftNotice('Saved draft restored. Review current capacity and settings before starting or scheduling.')
  }, [restoreDraft, scenario, activeProjectID, activeApplication?.id, scheduleDefaults])

  const showStartNow = true
  const startNowAllowed = Boolean(immediatePreflight?.start_now_allowed)
  const showQueueRun = Boolean(immediatePreflight?.queue_allowed)
  // Said beside Start now, so a disabled button always carries its reason.
  // While the same scenario is re-checked, its last reason stays (and stays
  // quiet): nothing flashes or is re-announced for a run that could start
  // before, and another scenario's reason is never shown for this one.
  const lastStartBlockerRef = useRef<{ scenarioId: string; blocker: ReturnType<typeof startNowBlocker> }>({ scenarioId: '', blocker: null })
  if (!immediatePreflightLoading) lastStartBlockerRef.current = { scenarioId, blocker: startNowBlocker(immediatePreflight, { queueAvailable: canLaunch && !queueRecovery }) }
  const startBlocker = lastStartBlockerRef.current.scenarioId === scenarioId ? lastStartBlockerRef.current.blocker : null
  const launchDescribedBy = [launchBlockers.length && !creating ? 'new-test-launch-blockers' : '', startBlocker ? 'new-test-start-blocker' : ''].filter(Boolean).join(' ') || undefined
  const scheduleAllowed = Boolean(scheduledStartISO && scheduledPreflight?.schedule_allowed)
  const scheduleBlocked = Boolean(scheduledStartISO && scheduledPreflight && !scheduledPreflight.schedule_allowed)
  const scheduleMin = formatDatetimeLocalValue(scheduleClock)
  const newTestViewControls = useMemo(() => (
    <PageViewToggle
      value={pageMode}
      ariaLabel="New run page view"
      className="new-test-view-toggle topbar-page-section-toggle"
      onChange={nextMode => runGuarded(() => setPageMode(nextMode), ['new-test-run-setup'])}
      options={[
        { value: 'launch', label: 'Run setup' },
        { value: 'schedules', label: 'Schedules' },
      ]}
    />
  ), [pageMode, runGuarded])
  const newTestTopbarContribution = useMemo(() => ({
    id: 'new-test-page',
    title: 'New run',
    controls: newTestViewControls,
    priority: 10,
  }), [newTestViewControls])

  useTopbarContribution(newTestTopbarContribution)

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>New run</h2>
          <p className="page-subtitle">Choose the release context, scenario, and run setup.</p>
        </div>
        {!hasTopbarContributionProvider && (
          <div className="inline-create-row new-test-view-switcher">
            {newTestViewControls}
          </div>
        )}
      </div>

      <div className="new-test-draft-toolbar">
        <span>Run setup and Schedules keep your edits when switching tabs.</span>
        <button type="button" className="btn btn-secondary" onClick={() => saveDraftRef.current()} disabled={!scenario || !canManageTests} title={canManageTests ? undefined : 'Your role cannot create runs in this project.'}>Save draft</button>
        {savedDraft && <button type="button" className="btn btn-secondary" disabled={!scenarioCatalogReady} aria-describedby={!scenarioCatalogReady ? 'new-test-restore-help' : undefined} onClick={() => runGuarded(() => {
          if (!scenarios.some(item => item.id === savedDraft.scenarioId && (item.updated_at || '') === savedDraft.scenarioUpdatedAt)) {
            setError('The saved scenario has changed or is no longer available. Select it again and review its current settings.')
            return
          }
          setScenarioId(savedDraft.scenarioId)
          setRestoreDraft(savedDraft)
        })}>Restore saved draft</button>}
        {savedDraft && !scenarioCatalogReady && <span id="new-test-restore-help">The scenario list must load before this draft can be restored.</span>}
      </div>
      {draftNotice && <p role="status">{draftNotice}</p>}
      {error && <ErrorDiagnostic error={error} fallback="Unable to create or load run setup" compact className="new-test-error-diagnostic" />}
      {effectiveAccessLoaded && effectiveAccessError && (
        <div className="settings-error-inline page-error-banner" role="alert">
          {effectiveAccessError}. Permissions could not be verified, so run and schedule changes remain disabled.
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setEffectiveAccessRetryNonce(value => value + 1)}>Retry permissions</button>
        </div>
      )}
      {effectiveAccessLoaded && !effectiveAccessError && !canManageTests && <div className="settings-error-inline page-error-banner" role="alert">Current session can view scenarios but cannot create runs or schedules for this project.</div>}
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

      {pageMode === 'schedules' && (
        <div className="new-test-layout new-test-schedules-layout">
          <div className="new-test-main">
            <NewTestScheduleCatalog scenarios={scenarios} projectId={activeProjectID} revision={catalogRefresh} onSelectScenario={id => runGuarded(() => { setScenarioId(id); setChosenScenarioId(id); setScheduleQuery('') })} />

            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">1</span>
                <div className="new-test-step-header-content">
                  <h3>Scenario schedule</h3>
                  <p>Choose when the stored scenario runs. Run setup overrides are not used by recurring schedules.</p>
                </div>
              </div>
              <div className="settings-group new-test-schedule-fields">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-scenario-label">Scenario</span>
                    <span className="setting-desc">Recurring runs always execute one stored scenario</span>
                  </div>
                  <input type="search" autoComplete="off" className="settings-input" aria-label="Search schedule scenarios" placeholder="Name or reference key" value={scheduleQuery} onChange={event => setScheduleQuery(event.target.value)} />
                  <select className="settings-input" aria-labelledby="schedule-scenario-label" value={scenarioId} onChange={(event) => {
                    const nextScenarioID = event.target.value
                    runGuarded(() => {
                      if (nextScenarioID !== scenarioId) {
                        runNameScenarioRef.current = ''
                        runNameEditedRef.current = false
                        setRunName('')
                      }
                      setScenarioId(nextScenarioID)
                      setChosenScenarioId(nextScenarioID)
                      setEditingScheduleId(null)
                    })
                  }}>
                    <option value="">Select scenario</option>
                    {[...scenarios].sort((a, b) => scenarioIdentity(a).localeCompare(scenarioIdentity(b))).filter(item => item.id === scenarioId || scenarioSearchText(item).includes(scheduleQuery.toLowerCase())).map(item => <option key={item.id} value={item.id}>{scenarioIdentity(item)} · {item.duration} · {formatScenarioLoad(item)}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-name-label">Schedule name</span>
                    <span className="setting-desc">A name to tell this schedule apart</span>
                  </div>
                  <input className="settings-input" required aria-invalid={!scheduleDraft.name.trim()} aria-describedby={!scheduleDraft.name.trim() ? "new-test-schedule-name-error" : undefined} aria-labelledby="schedule-name-label" value={scheduleDraft.name} onChange={(event) => setScheduleDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Nightly smoke" />
                  {!scheduleDraft.name.trim() && <span id="new-test-schedule-name-error" role="alert">Enter a schedule name.</span>}
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Frequency and time</span>
                    <span className="setting-desc">Build a schedule below, or type a five-field cron expression; it also takes <code>MON-FRI</code>, <code>@daily</code>, <code>L</code>, <code>15W</code> and <code>5#3</code>.</span>
                    {scheduleDraft.cron_expr.trim() && describeCron(scheduleDraft.cron_expr) && (
                      <span className="setting-desc cron-preview">{describeCron(scheduleDraft.cron_expr)}{scheduleDraft.timezone ? ` (${scheduleDraft.timezone})` : ''}</span>
                    )}
                  </div>
                  <div className="cron-field-stack">
                    <CronBuilder value={scheduleDraft.cron_expr} disabled={!canManageSchedules} onChange={(expr) => setScheduleDraft(prev => ({ ...prev, cron_expr: expr }))} />
<details><summary>Advanced cron expression</summary>                    <input className="settings-input cron-raw-input" value={scheduleDraft.cron_expr} disabled={!canManageSchedules} onChange={(event) => setScheduleDraft(prev => ({ ...prev, cron_expr: event.target.value }))} placeholder="0 9 * * 1-5" aria-label="Raw cron expression" aria-invalid={Boolean(currentSchedulePreview?.error) || !scheduleDraft.cron_expr.trim()} aria-describedby={currentSchedulePreview?.error || !scheduleDraft.cron_expr.trim() ? "new-test-cron-error" : undefined} /></details>
                    {!scheduleDraft.cron_expr.trim() && <p id="new-test-cron-error" role="alert">Enter a schedule frequency or cron expression.</p>}
                    <ScheduleNextRuns preview={currentSchedulePreview} pending={schedulePreviewPending} timezone={scheduleDraft.timezone} errorId="new-test-cron-error" />
                  </div>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-timezone-label">Timezone</span>
                    <span className="setting-desc">IANA timezone used for cron evaluation</span>
                    <ScheduleCurrentUtc now={scheduleClock} />
                  </div>
                  <TimezoneSelect className="settings-input" labelledBy="schedule-timezone-label" value={scheduleDraft.timezone} now={scheduleClock} onChange={timezone => setScheduleDraft(prev => ({ ...prev, timezone }))} />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-release-label">Release</span>
                    <span className="setting-desc">Where the schedule's runs are filed</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-release-label" value={scheduleDraft.release_id} onChange={(event) => { launchPlacementPinnedRef.current = true; explicitPlacementRef.current = true; setScheduleDraft(prev => ({ ...prev, release_id: event.target.value })) }}>
                    <option value="">Select release</option>
                    {releaseChoices(scheduleDraft.release_id).map(item => <option key={item.id} value={item.id}>{item.name}{highlightedReleases.has(item.id) ? ' — assigned' : ''}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-timeslot-label">Default timeslot</span>
                    <span className="setting-desc">Optional approved capacity timeslot each scheduled run binds at materialization. If the slot is unavailable, that run is recorded as blocked instead of launching unprotected.</span>
                  </div>
                  <select className="settings-input" aria-labelledby="schedule-timeslot-label" value={scheduleDraft.timeslot_id} onChange={(event) => setScheduleDraft(prev => ({ ...prev, timeslot_id: event.target.value }))}>
                    <option value="">No timeslot</option>
                    {bindableTimeslots.map(slot => <option key={slot.id} value={slot.id}>{formatTimeslotLabel(slot)}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="schedule-run-name-label">Run name template</span>
                    <span className="setting-desc">Default run name used when the schedule fires</span>
                  </div>
                  <input className="settings-input" aria-labelledby="schedule-run-name-label" value={scheduleDraft.run_name_template} onChange={(event) => setScheduleDraft(prev => ({ ...prev, run_name_template: event.target.value }))} placeholder={scenario?.name || 'Scheduled run'} />
                </div>
                <section className="new-test-schedule-policy" aria-label="Effective schedule policies">
                  <h4>Effective schedule policies</h4>
                  {!editingScheduleId && scheduleDefaultsStatus !== 'ready' && <p role="alert">Scheduling defaults {scheduleDefaultsStatus === 'loading' ? 'are loading' : 'could not be loaded'}. Save is unavailable until they are verified.<button type="button" className="btn btn-secondary btn-sm" onClick={() => setScheduleDefaultsRetry(value => value + 1)}>Retry scheduling defaults</button></p>}
                  <p>Source: {editingScheduleId ? 'saved schedule' : 'system scheduling defaults'}. These policies are submitted with this schedule.</p>
                  <dl><dt>Approval</dt><dd>{formatScheduleToken(scheduleDraft.approval_policy)}</dd><dt>Missed runs</dt><dd>{formatScheduleToken(scheduleDraft.missed_run_policy)}</dd></dl>
                  <p>To change defaults, review scheduling settings with your administrator.</p>
                  {scenario && <p>Stored workload: <strong>{scenarioIdentity(scenario)}</strong> · {formatScenarioLoad(scenario)} · {scenario.duration} · {scenario.scripts.length} groups. Run-only overrides do not apply.</p>}
                </section>
                <details className="schedule-policy-details"><summary>Optional schedule windows and pauses</summary>
                <ScheduleWindowListEditor
                  title="Blackout windows"
                  description="Runs never fire during these windows. Leave days unselected to apply every day."
                  windows={scheduleDraft.blackout_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageSchedules}
                  onChange={next => setScheduleDraft(prev => ({ ...prev, blackout_windows: next }))}
                />
                <ScheduleWindowListEditor
                  title="Maintenance windows"
                  description="Shared maintenance windows that also block runs. Leave days unselected to apply every day."
                  windows={scheduleDraft.maintenance_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageSchedules}
                  onChange={next => setScheduleDraft(prev => ({ ...prev, maintenance_windows: next }))}
                />
                <ScheduleWindowListEditor
                  title="Availability windows"
                  description="When set, runs fire only inside these windows. Leave days unselected to apply every day."
                  windows={scheduleDraft.availability_windows}
                  fallbackTimezone={scheduleDraft.timezone}
                  disabled={!canManageSchedules}
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
                      disabled={!canManageSchedules}
                      onChange={(event) => setScheduleDraft(prev => ({ ...prev, disabled_from: event.target.value }))}
                    />
                    <input
                      type="datetime-local"
                      className="settings-input"
                      aria-label="Pause until"
                      value={scheduleDraft.disabled_until}
                      disabled={!canManageSchedules}
                      onChange={(event) => setScheduleDraft(prev => ({ ...prev, disabled_until: event.target.value }))}
                    />
                  </div>
                </div>
                </details>
                <label className="setting-row" style={{ alignItems: 'center', gap: 12 }}>
                  <input type="checkbox" checked={scheduleDraft.enabled} onChange={(event) => setScheduleDraft(prev => ({ ...prev, enabled: event.target.checked }))} />
                  <div className="setting-info">
                    <span className="setting-label">Enabled</span>
                    <span className="setting-desc">Turned off, the schedule stays saved but starts no runs</span>
                  </div>
                </label>
                {/* The form's submit sits at its end, after everything it saves. */}
                <div className="new-test-schedule-actions">
                  {editingScheduleId && (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      onClick={() => {
                        setEditingScheduleId(null)
                        replaceScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId, scheduleDefaults))
                      }}
                    >
                      Cancel edit
                    </button>
                  )}
                  <button type="button" className="btn btn-primary" onClick={submitSchedule} disabled={!scenario || scheduleSubmitting || !canManageSchedules || schedulePreviewPending || Boolean(scheduleFormError) || (!editingScheduleId && !scheduleDraft.release_id)}>
                    {editingScheduleId ? 'Save schedule' : 'Create schedule'}
                  </button>
                </div>
              </div>
            </section>

            <section className="new-test-step" aria-label="Selected scenario schedules">
              <div className="new-test-step-header">
                <span className="new-test-step-index">2</span>
                <div>
                  <h3>Saved schedules</h3>
                  <p>Schedules for {scenario ? scenarioIdentity(scenario) : 'the selected scenario'}</p>
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
                        <th>Next run (schedule time zone)</th>
                        <th>Last run (schedule time zone)</th>
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
                              <td>
                                {formatScheduleInstantForViewer(item.next_run_at, item.timezone)}
                                <ScheduleClockChanges changes={scheduleClockChanges[item.id]} timezone={item.timezone || 'UTC'} />
                              </td>
                              <td>{formatScheduleInstant(item.last_run_at, item.timezone)}</td>
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
                                  <button className="btn btn-secondary" onClick={() => editSchedule(item)} disabled={!canManageSchedules}>Edit</button>
                                  <button className="btn btn-secondary" onClick={() => toggleSchedule(item)} disabled={!canManageSchedules}>{item.enabled ? 'Pause' : 'Enable'}</button>
                                  <button className="btn btn-secondary" onClick={() => { void requestDeleteSchedule(item) }} disabled={!canManageSchedules}>Delete</button>
                                </div>
                              </td>
                            </tr>
                            {expandedScheduleId === item.id && (
                              <tr className="no-hover" key={`${item.id}-occurrences`}>
                                <td colSpan={8} style={{ background: 'var(--bg-primary)' }}>
                                  <div className="schedule-occurrence-panel">
                                    <div className="schedule-occurrence-header">
                                      <strong>Occurrence Audit Trail</strong>
                                      <span>{scheduleOccurrenceLoading === item.id ? 'Loading…' : `${occurrences.length} records`}</span>
                                    </div>
                                    {scheduleOccurrenceLoading === item.id ? (
                                      <div className="cell-muted">Loading schedule occurrence history…</div>
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
                                              <th>Run</th>
                                              <th>Approval</th>
                                              <th>Conflict</th>
										  <th>Resolution</th>
                                            </tr>
                                          </thead>
                                          <tbody>
                                            {occurrences.map(occurrence => (
                                              <tr key={occurrence.id}>
                                                <td>{formatScheduleInstant(occurrence.planned_at, item.timezone)}</td>
                                                <td>{formatScheduleInstant(occurrence.materialized_at, item.timezone)}</td>
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
      )}
      <div className="new-test-layout new-test-workspace" hidden={pageMode === 'schedules'} style={pageMode === 'schedules' ? { display: 'none' } : undefined}>
        <div className="new-test-main">
          <section className="new-test-step">
            <div className="new-test-step-header">
              <span className={`new-test-step-index${step1Complete ? ' complete' : ''}`}>{step1Complete ? '✓' : '1'}</span>
              <div className="new-test-step-header-content">
                <h3>Scenario</h3>
                <p>{!scenarioListOpen ? 'The reusable workload this run starts from. Settings you change here apply to this run only.' : 'Choose the reusable workload definition below, or create a new scenario in the scenario builder.'}</p>
              </div>
              {onOpenScenarioBuilder && (
                <button
                  type="button"
                  className="new-test-builder-button new-test-step-header-link"
                  onClick={onOpenScenarioBuilder}
                >
                  Open scenario builder
                </button>
              )}
            </div>
            {scenario && <div className="new-test-selected" role="group" aria-label="Selected scenario">
              <div className="new-test-selected-header">
                <div>
                  <h4 ref={selectedScenarioHeadingRef} tabIndex={-1}>{scenarioIdentity(scenario)} <span className={scenarioModeChipClass(scenario)}>{formatScenarioModeShort(scenario)}</span></h4>
                  <p className="new-test-selected-meta">{formatScenarioWorkloadSummary(scenario)} · {scenario.termination_mode === 'iterations' ? 'All iterations' : scenario.duration || 'Duration not set'} · Results release: {release?.name || 'Select a release'}</p>
                  <p className="new-test-selected-meta">{scenario.associated_releases?.length ? `Previously run in ${formatScenarioReleaseSummary(scenario)}` : 'Not run in a release yet'} · Most recent run: {formatDate(scenario.last_run_at)}</p>
                </div>
                <div className="new-test-selected-actions">
                  {scenario.archived_at && <span className="status-badge">Archived</span>}
                  <button className="btn btn-secondary btn-sm" type="button" aria-expanded={scenarioListOpen} aria-controls={scenarioListOpen ? 'new-test-scenario-browser' : undefined} onClick={scenarioListOpen ? keepScenario : changeScenario}>{scenarioListOpen ? 'Keep this scenario' : 'Change scenario'}</button>
                  <RowActions className="new-test-selected-more" label={scenario.name}>
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => setHistoryScenarioId(scenario.id)}>History</button>
                    <ObjectArchiveControls inMenu resourceType="scenario" resourceId={scenario.id} resourceName={scenario.name} archivedAt={scenario.archived_at} updatedAt={scenario.updated_at} canManage={canManageTests} onChanged={refresh} />
                  </RowActions>
                </div>
              </div>
              {scenario.archived_at && <p role="alert">This scenario is archived. Restore it to create a run.</p>}
              {archivedDependencies.map(script => <p role="alert" key={script.id}>Archived script <a href={`#/scripts?mode=edit&scriptId=${encodeURIComponent(script.id)}&projectId=${encodeURIComponent(activeProjectID)}`}>{script.name}</a> blocks execution. Restore it before launching.</p>)}
              {groupSaveNotice && <div className="new-test-default-notice" role="status">{groupSaveNotice}</div>}
              {!scenarioListOpen && <div className="new-test-scenario-groups-panel">
                <div className="new-test-scenario-groups-header">
                  <strong>Script Groups</strong>
                  <span>{formatCount(effectiveScenarioGroups.length, 'group')}</span>
                </div>
                <div className="data-table">
                  <table className="new-test-script-table">
                    <thead><tr><th>Group / script</th><th>Workload</th><th>Runtime / placement</th></tr></thead>
                    <tbody>
                      {effectiveScenarioGroups.length > 0 ? effectiveScenarioGroups.map((group, index) => {
                        const sourceGroup = scenario.scripts[index] || group
                        const key = groupOverrideKey(sourceGroup)
                        const override = groupRunOverrides[key]
                        const runtimeValues = buildGroupRuntimeTableValues(sourceGroup, scenario, runDefaultOverrides, override)
                        const rowOverridden = hasGroupOverrideValues(override)
                        return (
                          <tr
                            key={key}
                            className={rowOverridden ? 'new-test-script-row overridden' : 'new-test-script-row'}
                            tabIndex={0}
                            aria-label={`Edit run settings for ${group.name || group.script_name || 'script group'}`}
                            onClick={event => { if (!insideRowDisclosure(event)) openGroupEditor(sourceGroup) }}
                            onKeyDown={event => {
                              if (event.currentTarget !== event.target) return
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault()
                                openGroupEditor(sourceGroup)
                              }
                            }}
                          >
                            <td className="cell-name">
                              <span>{group.name || group.script_name}</span><small>{group.script_name || group.script_id} · {group.protocol || 'http'}</small>
                              <button type="button" className="btn btn-secondary btn-sm" aria-label={`Edit settings for ${group.name || group.script_name || 'script group'}`} onClick={event => { event.stopPropagation(); openGroupEditor(sourceGroup) }}>Edit settings</button>
                              {rowOverridden && <em className="new-test-override-pill">Run override</em>}
                            </td>
          <td aria-label="Group workload">
                              <div className="new-test-workload-stack">
                                <strong>{group.vusers} VUsers</strong>
                                <span>{group.ramp_up || 'No ramp up'} / {group.ramp_down || 'No ramp down'}</span>
                                <span>{group.initialization === 'at_once' ? 'Init at once' : 'Init at runtime'}</span>
                              </div>
                            </td>
                            <td className="new-test-runtime-cell">
                              <p>{formatGroupPlacement(override, sourceGroup)}</p>
                              <details><summary>Runtime details</summary>
                                <dl><dt>Iterations</dt><dd>{runtimeValues.iterations}</dd><dt>Pacing</dt><dd>{runtimeValues.pacing}</dd><dt>Think time</dt><dd>{runtimeValues.thinkTime}</dd><dt>Log level</dt><dd>{runtimeValues.logLevel}</dd><dt>Auto transactions</dt><dd>{runtimeValues.autoTransactions}</dd><dt>Error handling</dt><dd>{runtimeValues.errorHandling}</dd></dl>
                              </details>
                            </td>
                          </tr>
                        )
                      }) : (
                        <tr>
                          <td colSpan={3} className="cell-muted">No script groups included in this scenario.</td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </div>}
            </div>}
            {scenarioListOpen && (
            <div className="settings-group" id="new-test-scenario-browser">
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
                        ref={scenarioSearchRef}
                        type="search" autoComplete="off"
                        value={scenarioQuery}
                        onChange={(event) => setScenarioQuery(event.target.value)}
                        placeholder="Find by scenario, script, protocol, release, or data pool…"
                      />
                    </label>
                    <label className="library-filter"><span>Sort scenarios</span><select aria-label="Sort scenarios" value={scenarioSortField} onChange={event => setScenarioSortField(event.target.value as ScenarioSortField)}>
                      <option value="name">Name</option><option value="releases">Releases</option><option value="last_run_at">Most recent run</option><option value="vusers">VUsers</option><option value="duration">Duration</option><option value="groups">Groups</option>
                    </select></label>
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => setScenarioSortDirection(value => value === 'asc' ? 'desc' : 'asc')}>{scenarioSortDirection === 'asc' ? 'Ascending' : 'Descending'}</button>
                    <div className="new-test-scenario-count">
                      {scenarioFiltersActive ? `${filteredScenarios.length} matching scenario${filteredScenarios.length === 1 ? '' : 's'}` : `${scenarios.length} available scenario${scenarios.length === 1 ? '' : 's'}`}
                    </div>
                  </div>
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
                              return (
                                <tr
                                  key={item.id}
                                  className={`new-test-scenario-table-row clickable-row ${selected ? 'selected' : ''}`}
                                  onClick={() => selectScenario(item.id)}
                                >
                                  <td className="cell-name">
                                    <div className="new-test-scenario-name-cell">
                                      {/* The row's keyboard control carries its state; table rows cannot. */}
                                      <button
                                        type="button"
                                        className="new-test-row-toggle"
                                        aria-current={selected ? 'true' : undefined}
                                        onClick={event => { event.stopPropagation(); selectScenario(item.id) }}
                                      >
                                        <span>{item.name}<small className="new-test-reference">{item.ref_key || item.id}</small></span>
                                      </button>
                                      <span className={scenarioModeChipClass(item)}>{formatScenarioModeShort(item)}</span>
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
                                  <td>{scenarioPeakVUsers(item)?.toLocaleString() ?? <span className="cell-muted" title="Set at runtime by the traffic profile's TPS targets">Adaptive</span>}</td>
                                  <td>{item.termination_mode === 'iterations' ? 'All iterations' : item.duration || 'Not set'}</td>
                                  <td>{item.scripts.length}</td>
                                  <td>
                                    <div className="new-test-release-token-list">
                                      {itemProtocols.length ? itemProtocols.map(protocol => <span key={protocol} className="protocol-badge">{protocol}</span>) : <span className="cell-muted">None</span>}
                                    </div>
                                  </td>
                                </tr>
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
                        : 'No scenarios are available yet. Create the first reusable workload in the scenario builder.'}
                    </div>
                  )}
                </div>
              </div>
            </div>
            )}
          </section>
          {scenario && historyScenarioId === scenario.id && <ObjectHistoryDialog title={`History · ${scenario.name}`} projectId={scenario.project_id} resourceType="scenario" resourceId={scenario.id} activity={scenario.activity} onClose={() => setHistoryScenarioId('')} />}

          <div className="new-test-run-only-callout" role="note" hidden={!scenario}>
            Note: Changes on this page only override the default settings for the current run. To change scenario defaults, edit the scenario in the scenario builder.
          </div>

          <div className="new-test-run-config-row" hidden={!scenario}>
            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className={`new-test-step-index${step2Complete ? ' complete' : ''}`}>{step2Complete ? '✓' : '2'}</span>
                <div>
                  <h3>Run setup</h3>
                  <p>Name the run and attach it to a release.</p>
                </div>
              </div>
              <div className="settings-group new-test-setup-grid">
                <label className="settings-filter-field">Launch application<select ref={applicationSelectRef} aria-label="Launch application" className="settings-input" value={activeApplication?.id || ''} onChange={event => { const next = applications.find(item => item.id === event.target.value); if (next) { launchPlacementPinnedRef.current = true; explicitPlacementRef.current = true; setLaunchApplication(next) } }}><option value="" disabled>Select application</option>{applications.filter(item => !isLegacyApplicationId(item.id) || item.id === activeApplication?.id).map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
                <div className="setting-row new-test-release-row">
                  <div className="setting-info">
                    <span className="setting-label" id="new-test-release-label">Release</span>
                    <span className="setting-desc">Required target for results</span>
                  </div>
                  <select ref={releaseSelectRef} className="settings-input" aria-labelledby="new-test-release-label" value={releaseId} onChange={(event) => { launchPlacementPinnedRef.current = true; explicitPlacementRef.current = true; releaseChosenRef.current = true; setReleaseId(event.target.value) }}>
                    <option value="">Select release</option>
                    {releaseChoices(releaseId).map(item => <option key={item.id} value={item.id}>{item.name}{highlightedReleases.has(item.id) ? ' — assigned' : ''}</option>)}
                  </select>
                  {showNewRelease ? (
                    <div className="new-test-release-create-row">
                      <FormField label="Release name" htmlFor="NewTestPage-newReleaseName"><input ref={newReleaseInputRef} className="settings-input" aria-label="New release name" value={newReleaseName} onChange={event => setNewReleaseName(event.target.value)} placeholder="Release name, e.g. PI30" id="NewTestPage-newReleaseName" /></FormField>
                      <button className="btn btn-secondary" onClick={() => { void createRelease() }} disabled={releaseCreating || !newReleaseName.trim()}>Add</button>
                      <button className="btn btn-ghost btn-sm" onClick={() => { setShowNewRelease(false); setNewReleaseName('') }}>Cancel</button>
                    </div>
                  ) : (
                    <button className="btn btn-ghost btn-sm btn-primary new-test-add-release-link" onClick={() => setShowNewRelease(true)}>+ New Release</button>
                  )}
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label" id="new-test-run-name-label">Run Name</span>
                    <span className="setting-desc">Shown in results and monitoring</span>
                  </div>
                  <input className="settings-input" aria-labelledby="new-test-run-name-label" value={runName} onChange={event => { runNameEditedRef.current = true; setRunName(event.target.value) }} placeholder={scenario?.name || 'Run name'} />
                </div>
                {(bindableTimeslots.length > 0 || selectedTimeslotId) && (
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label" id="new-test-timeslot-label">Reserved Timeslot</span>
                      <span className="setting-desc">Attach this run to an approved capacity reservation (optional)</span>
                    </div>
                    <select className="settings-input" aria-labelledby="new-test-timeslot-label" value={selectedTimeslotId} onChange={event => setSelectedTimeslotId(event.target.value)}>
                      <option value="">No reservation</option>
                      {timeslotValidationError && <option value={selectedTimeslotId}>Unavailable saved reservation — review required</option>}
                      {bindableTimeslots.map(slot => (
                        <option key={slot.id} value={slot.id}>
                          {formatTimeslotLabel(slot)}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
                {/* Optional capture for this run, folded away unless it needs attention. */}
                <details ref={monitoringRef} className="new-test-monitoring" open={monitoringOpen || monitoringNeedsReview} onToggle={event => setMonitoringOpen(event.currentTarget.open)}>
                  <summary><span>Monitoring for this run</span><small className={monitoringNeedsReview ? 'warning' : undefined}>{monitoringSummary}</small></summary>
                  <div className="new-test-monitoring-body">
                  <RunMonitorBindingSelector projectId={activeProjectID} scenarioId={scenario?.id} legacyELKConnectionId={elkConnectionId} onConflictChange={setElasticMonitorConflict} onInheritedCountChange={setInheritedMonitorBindingCount} value={monitorBindings} onChange={setMonitorBindings} />
                  <MonitorRuleEditor key={`${activeProjectID}:${scenarioId}:${monitorEditorRevision}`} projectId={activeProjectID} value={monitorRules ?? scenario?.goal_config?.monitor_rules ?? []} onChange={setMonitorRules} onValidationChange={setMonitorEditorError} />
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label" id="new-test-elk-label">Elastic log capture</span>
                      <span className="setting-desc">Optional controller-side polling; load generators are unaffected</span>
                    </div>
                    <select className="settings-input" aria-labelledby="new-test-elk-label" value={elkConnectionId} onChange={event => setELKConnectionId(event.target.value)}>
                      <option value="">Off for this run</option>
                      {elkConnectionId && !elkConnections.some(c => c.id === elkConnectionId) && <option value={elkConnectionId}>Selected connection unavailable — review required</option>}
                      {elkConnections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}
                    </select>
                    <CaptureSelectionHelp provider="elk" count={elkConnections.length} error={elkError} forbidden={elkForbidden} loading={elkLoading} retry={() => setCaptureRetry(value => value + 1)} selected={elkConnections.find(c => c.id === elkConnectionId)?.name} canManage={canManageProjects} />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label" id="new-test-dynatrace-label">Dynatrace SaaS capture</span>
                      <span className="setting-desc">Optional Metrics, Problems, and Grail log polling from the controller</span>
                    </div>
                    <select className="settings-input" aria-labelledby="new-test-dynatrace-label" value={dynatraceConnectionId} onChange={event => setDynatraceConnectionId(event.target.value)}>
                      <option value="">Off for this run</option>
                      {dynatraceConnectionId && !dynatraceConnections.some(c => c.id === dynatraceConnectionId) && <option value={dynatraceConnectionId}>Selected connection unavailable — review required</option>}
                      {dynatraceConnections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}
                    </select>
                    <CaptureSelectionHelp provider="dynatrace" capabilities={dynatraceCapabilities(dynatraceConnections.find(c => c.id === dynatraceConnectionId))} count={dynatraceConnections.length} error={dynatraceError} forbidden={dynatraceForbidden} loading={dynatraceLoading} retry={() => setCaptureRetry(value => value + 1)} selected={dynatraceConnections.find(c => c.id === dynatraceConnectionId)?.name} canManage={canManageProjects} />
                  </div>
                  <RunTraceContextSelector value={traceContext} onChange={setTraceContext} suggest={Boolean(elkConnectionId || dynatraceConnectionId || (monitorBindings?.length ?? 0) > 0)} />
                  </div>
                </details>
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
                        disabled={scenario.termination_mode === 'iterations' && card.key === 'duration'}
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
                          disabled={scenario.termination_mode === 'iterations' && card.key === 'duration'}
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

          {readinessScenario && <NewTestReadiness scenario={readinessScenario} projectId={activeProjectID} summary />}
        </div>

        <aside className="new-test-sidebar-column" hidden={!scenario}>
          <section className="new-test-profile-card" aria-label="Modeled load profile">
            <div className="new-test-profile-header">
              <div>
                <h3>Modeled load profile</h3>
                <p>{runLoadProfilePreview.runtimeTarget === 'tps' ? `${scenario?.traffic_profile?.runtime_target === 'tps' ? 'Production-derived adaptive' : 'Configured adaptive'} TPS targets. VUser demand is controlled at runtime${runLoadProfilePreview.estimatedPeakVUsers !== undefined ? '; the ART-backed VUser figure is a Little’s Law planning estimate' : ''}.` : runLoadProfilePreview.showVUsers ? 'Planned VUsers for this run.' : 'VUser demand is determined by the goal controller at runtime.'}</p>
              </div>
              {runLoadProfilePreview.showVUsers && runLoadProfilePreview.peakTps === undefined && <div className="new-test-profile-stat">
                <strong>{runLoadProfilePreview.peakVUsers}</strong>
                <span>peak VUsers</span>
              </div>}
            </div>
            {runLoadProfilePreview.peakTps !== undefined && <div className="new-test-profile-stats" aria-label="TPS profile statistics">
              <div className="new-test-profile-stat"><strong>{formatProfileTps(runLoadProfilePreview.minTps)}</strong><span>minimum TPS</span></div>
              <div className="new-test-profile-stat"><strong>{formatProfileTps(runLoadProfilePreview.averageTps)}</strong><span>average TPS</span></div>
              <div className="new-test-profile-stat"><strong>{formatProfileTps(runLoadProfilePreview.peakTps)}</strong><span>peak TPS</span></div>
              {runLoadProfilePreview.estimatedPeakVUsers !== undefined && <div className="new-test-profile-stat"><strong>{runLoadProfilePreview.estimatedPeakVUsers.toLocaleString()}</strong><span>estimated peak VUsers</span></div>}
              {runLoadProfilePreview.recommendedGenerators !== undefined && runLoadProfilePreview.recommendedGenerators > 0 && <div className="new-test-profile-stat"><strong>{runLoadProfilePreview.recommendedGenerators}</strong><span>recommended generators</span></div>}
            </div>}
            {runLoadProfilePreview.points.length > 0 && (runLoadProfilePreview.showVUsers || runLoadProfilePreview.peakTps !== undefined) ? (
              <>
                <div className="new-test-profile-chart">
                  <LoadPlanChart
                    label="Modeled load profile"
                    points={runLoadProfilePreview.points}
                    totalSeconds={runLoadProfilePreview.totalSeconds}
                    showVUsers={runLoadProfilePreview.showVUsers}
                    groups={effectiveScenarioGroups.map((group, index) => ({ key: `group${index}`, label: group.name || group.script_name || `Group ${index + 1}`, color: LOAD_PLAN_COLORS[index % LOAD_PLAN_COLORS.length] }))}
                    tps={runLoadProfilePreview.peakTps === undefined ? [] : [
                      ...((runLoadProfilePreview.tpsGroupIndexes || []).length > 1 ? (runLoadProfilePreview.tpsGroupIndexes || []).map(index => ({ key: `group${index}Tps`, label: `${effectiveScenarioGroups[index]?.name || effectiveScenarioGroups[index]?.script_name || `Group ${index + 1}`} TPS`, color: LOAD_PLAN_COLORS[index % LOAD_PLAN_COLORS.length] })) : []),
                      { key: 'totalTps', label: scenario?.traffic_profile?.runtime_target === 'tps' ? 'Production target TPS' : 'Target TPS', color: 'var(--warning)' },
                    ]}
                    phases={runLoadProfilePreview}
                    height={210}
                  />
                </div>
                <div className="new-test-profile-meta">
                  <span>{formatTimeCompact(runLoadProfilePreview.totalSeconds)} total</span>
                  <span>{formatTimeCompact(runLoadProfilePreview.steadySeconds)} load window (includes ramp-up)</span>
                </div>
              </>
            ) : (
              <div className="new-test-empty compact">{scenario?.scenario_mode === 'goal_oriented' ? 'No deterministic VUser curve is available. The goal controller will determine demand during execution.' : scenario?.termination_mode === 'iterations' ? 'This scenario runs until every VUser finishes its iterations, so its length is not known before the run.' : 'Select a runnable scenario to preview the load profile.'}</div>
            )}
          </section>

          <section className="new-test-launch-card" aria-label="Run timing">
            <FormErrorSummary issues={[{ message: immediatePreflightError || '' }, { message: runSetupValidationError || '' }]} />
            <h3>Run timing</h3>
            <p>Start now, hold for a manual start, or schedule once. For repetition, use Schedules.</p>
            {scenario && <p><strong>{scenarioIdentity(scenario)}</strong> · {totalVUsers} VUsers · {effectiveRunDuration} · {release?.name || 'No release selected'}</p>}
              <div className="new-test-launch-actions">

              {immediatePreflightLoading && !immediatePreflight && (
                <button className="btn btn-secondary" disabled>Checking Window</button>
              )}
              {showStartNow && (
                <>
                  <button className="btn btn-primary" onClick={() => createRun('start')} disabled={!canLaunch || immediatePreflightLoading || !startNowAllowed} aria-describedby={launchDescribedBy}>Start now</button>
                  <button className="btn btn-secondary" onClick={() => createRun('reserve')} disabled={!canLaunch || immediatePreflightLoading || !startNowAllowed} aria-describedby={launchDescribedBy} title="Holds a launch window for this run to be started manually later. This is not a capacity timeslot reservation.">Hold for manual start</button>
                </>
              )}
              {showQueueRun && (
                <button className="btn btn-secondary" onClick={() => createRun('queue')} disabled={!canLaunch || immediatePreflightLoading || Boolean(queueRecovery)} aria-describedby={launchDescribedBy}>Create queued run</button>
              )}
              {launchBlockers.length > 0 && !creating && (
                <div id="new-test-launch-blockers" className="new-test-launch-feedback warning" role="status">
                  To launch: {launchBlockers.slice(0, 3).map((blocker, index) => (
                    <Fragment key={blocker.label}>
                      {index > 0 && ' · '}
                      {blocker.field ? <button type="button" className="new-test-blocker-link" onClick={() => focusLaunchField(blocker.field!)}>{blocker.label}</button> : blocker.label}
                    </Fragment>
                  ))}{launchBlockers.length > 3 ? ` · and ${launchBlockers.length - 3} more` : ''}.
                </div>
              )}
              {startBlocker && (
                <div id="new-test-start-blocker" className="new-test-launch-feedback warning" role="status">
                  {startBlocker.message}
                  {startBlocker.overCap && <> <a href={settingsHref('limits')}>Project limits &amp; quotas</a> · <a href={workspaceHref('tests', undefined, undefined, { view: 'plans' })}>Execution plans</a></>}
                </div>
              )}
              <div className="new-test-schedule-launch">
                <label>
                  <span>Scheduled start ({Intl.DateTimeFormat().resolvedOptions().timeZone})</span>
                  <input
                    className="settings-input"
                    type="datetime-local"
                    min={scheduleMin}
                    value={scheduledStartLocal}
                    onChange={event => setScheduledStartLocal(event.target.value)}
                    aria-invalid={scheduledStartInPast || undefined}
                    aria-describedby={scheduledStartInPast ? 'new-test-scheduled-start-error' : undefined}
                  />
                </label>
                <button className="btn btn-secondary" onClick={() => createRun('schedule')} disabled={!canLaunch || scheduledPreflightLoading || !scheduleAllowed} aria-describedby={launchBlockers.length && !creating ? 'new-test-launch-blockers' : undefined}>Schedule run</button>
                {scheduledStartInPast && (
                  <div id="new-test-scheduled-start-error" className="new-test-launch-feedback warning" role="alert">That time has already passed. Choose a start time in the future.</div>
                )}
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
            <p>Capacity is an estimate. The proposed window is not reserved until you hold or schedule the run.</p>
            <p>{generatorInventoryError || (inventoryCheckedAt ? (generatorInventory.length > 0 ? `${generatorInventory.length} live inventory hosts · checked ${inventoryCheckedAt}` : `No live generator inventory (local or static mode); capacity uses the configured generator count · checked ${inventoryCheckedAt}`) : 'Live inventory not yet verified')}</p>
            <button type="button" className="btn btn-secondary btn-sm" disabled={generatorInventoryLoading} onClick={() => { setInventoryRefresh(value => value + 1); void refreshBindableTimeslots() }}>Refresh capacity and inventory</button>
            <dl className="new-test-summary-list" aria-label="Run settings summary">
              <div><dt>Elastic capture</dt><dd>{elkConnections.find(c => c.id === elkConnectionId)?.name || (elkConnectionId ? 'Selected connection unavailable — review required' : 'Off for this run')}</dd></div>
              <div><dt>Dynatrace capture</dt><dd>{dynatraceConnections.find(c => c.id === dynatraceConnectionId)?.name || (dynatraceConnectionId ? 'Selected connection unavailable — review required' : 'Off for this run')}</dd></div>
              <div className={traceContextNeedsReview ? 'warning' : undefined}><dt>Trace context</dt><dd>{traceContextNeedsReview ? 'Origins need review' : traceContextSummary(traceContext)}</dd></div>
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

      {editingDefaultKey && editingDefaultCard && (
        <DialogLayer><div className="modal-overlay" {...dialogBackdropProps(() => setEditingDefaultKey(null))}>
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
        </div></DialogLayer>
      )}

      {editingGroupKey && editingGroup && scenario && createPortal(
        <DialogLayer><div className="modal-overlay" {...dialogBackdropProps(() => setEditingGroupKey(null))}>
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
                    <div className={changedFieldClass(groupEditChanges.ramp_up)}>
                      <DurationField
                        label="Ramp up"
                        allowEmpty
                        allowZero
                        onInvalidChange={reportDurationValidity('ramp_up')}
                        placeholder="Scenario default"
                        value={groupEditDraft.ramp_up}
                        onChange={value => { setError(''); setGroupEditDraft(prev => ({ ...prev, ramp_up: value })) }}
                      />
                      <ChangedBadge changed={groupEditChanges.ramp_up} />
                    </div>
                    <div className={changedFieldClass(groupEditChanges.ramp_down)}>
                      <DurationField
                        label="Ramp down"
                        allowEmpty
                        allowZero
                        onInvalidChange={reportDurationValidity('ramp_down')}
                        placeholder="Scenario default"
                        value={groupEditDraft.ramp_down}
                        onChange={value => { setError(''); setGroupEditDraft(prev => ({ ...prev, ramp_down: value })) }}
                      />
                      <ChangedBadge changed={groupEditChanges.ramp_down} />
                    </div>
                  </div>
                </section>
                <section>
                  <h4>Load Generator Sharding <ChangedBadge changed={groupEditChanges.assignment} /></h4>
                  <button type="button" className="btn btn-secondary btn-sm" onClick={() => setInventoryRefresh(value => value + 1)}>Refresh host inventory</button>
                  <p className="new-test-inline-note">Saved host placement applies only to this run. Multiple hosts split this group by available capacity.</p>
                  {generatorInventoryLoading ? (
                    <div className="new-test-empty compact">Loading generator inventory…</div>
                  ) : (
                    <>
                      {generatorInventory.length === 0 && (
                        <div className="new-test-empty compact">{generatorInventoryError || 'No live registered hosts are listed. Capacity estimates may include configured or local execution capacity; automatic placement remains available.'}</div>
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
                          // 'Script think time' means the group transmits no think_time section.
                          // A mean left behind in the draft would be reconstituted by
                          // thinkTimeFromEdit as a fixed override (its bail-out needs an EMPTY
                          // mean), pinning a fixed think time instead of clearing the section —
                          // so the inherit option drops the mean the same way 'recorded' does.
                          thinkTimeMs: type && type !== 'recorded' ? prev.thinkTimeMs : '',
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
                        <option value="random_percentage">Random percentage of recorded think time</option>
                        <option value="fixed">Fixed</option>
                        <option value="gaussian">Gaussian</option>
                        <option value="uniform">Uniform</option>
                      </select>
                    </label>
                    {/* No mode selected is the inherit option, which carries no mean: showing the
                        field there invites a value thinkTimeFromEdit now deliberately ignores. */}
                    {groupEditDraft.thinkTimeConfig?.type === 'random_percentage' && <ThinkTimePercentageInputs value={groupEditDraft.thinkTimeConfig} disabled={groupEditDraft.thinkIgnore} onChange={change => setGroupEditDraft(prev => ({ ...prev, thinkTimeConfig: { ...prev.thinkTimeConfig!, ...change } }))} />}
                    {Boolean(canonicalThinkTimeType(groupEditDraft.thinkTimeConfig?.type)) && thinkTimeUsesMean(groupEditDraft.thinkTimeConfig?.type) && (
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
                      <select className="settings-input" value={groupEditDraft.continue_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, ...applyGroupErrorHandlingDraftEdit(prev, 'continue_on_error', event.target.value) }))}>
                        <option value="" disabled={continueOnErrorInherit.disabled}>{continueOnErrorInherit.label}</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label className={changedFieldClass(groupEditChanges.snapshot_on_error)}>
                      <span>Snapshot on error <ChangedBadge changed={groupEditChanges.snapshot_on_error} /></span>
                      <select className="settings-input" value={groupEditDraft.snapshot_on_error} onChange={event => setGroupEditDraft(prev => ({ ...prev, ...applyGroupErrorHandlingDraftEdit(prev, 'snapshot_on_error', event.target.value) }))}>
                        <option value="" disabled={groupErrorHandlingInherit.disabled}>{groupErrorHandlingInherit.label}</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                  </div>
                </section>
                {editingGroup?.protocol === 'browser' && <section>
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
                </section>}
              </div>
              <div className="modal-actions">
                <button type="button" className="btn btn-secondary" onClick={() => setEditingGroupKey(null)}>Cancel</button>
                <button type="button" className="btn btn-secondary" onClick={clearGroupOverride}>Use Scenario Defaults</button>
                <button type="button" className="btn btn-primary" onClick={saveGroupOverride} disabled={(!hasGroupEditChanges && !invalidDurationFields.ramp_up && !invalidDurationFields.ramp_down) || Boolean(groupEditEnvelopeError)}>Save For This Run</button>
              </div>
            </div>
          </div>
        </div></DialogLayer>,
        document.body,
      )}

    </div>
  )
}
