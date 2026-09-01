import { Fragment, memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { ApplicationRepository, DashboardAuthEffectiveAccessResource, DraftCapacityEstimate, GeneratorAssignmentSettings, GoalConfig, GoalSizingReport, LoadModelSupportEntry, LoadModelTemplate, LoadModelValidationReport, LoadStageDraft, RunArchiveActionStats, RunArchiveReport, RuntimeSettingsOverride, ScenarioDataPoolBinding, ScenarioMode, ScenarioRuntimeSettingsDraft, SharedDataPool, StoredScenario, StoredScript, SystemInventoryGenerator, TestSummary, TrafficProfile, TrafficProfileDeriveResponse } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatTimeCompact, isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import './TestHub.css'
import './CreateTestModal.css'
import { buildCreateTestPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { SortHeader } from './SortHeader'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import RuntimeSettingsGroupEditor from './RuntimeSettingsGroupEditor'
import { readImportedJavaVMParameters } from './scriptEditorImportModel'
import GroupPlacementEditor from './GroupPlacementEditor'
import { overrideFromRuntimeDraft } from './runtimeSettingsModel'
import { applyEffectiveRuntimeSettingsToGroups } from './runtimeSettingsApply'
import { adaptScenarioShapeDefaults, buildScenarioModelPreview, buildScenarioShapeCatalog, buildShapeStages, formatModelTimeLabel, getScenarioShapePreset, ScenarioModelPreview, ScenarioSeed, ScenarioShapePreset, ScenarioSourceMode, ScenarioWizardStep, shapeStageStrategyFor, validateStageTimeline } from './scenarioWizardUtils'
import { BaselineGroupOption, baselineGroupOptions, formatGoalSizingSummary, goalSizedGoalFields, goalSizedGroups, littlesLawVUsers } from './goalSizingUtils'
import DurationField from './DurationField'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import { hasGoalModeConfiguration, validateGoalConfig } from './goalConfigValidation'
import PageViewToggle from './PageViewToggle'
import ExecutionPlansPanel from './ExecutionPlansPanel'
import { type NavPage } from './Sidebar'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation } from './useLibraryFederation'
import { shortLibraryHash } from './libraryFederationUtils'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import SelectAllCheckbox from './SelectAllCheckbox'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { clearScenarioBuilderDraft, hasScenarioBuilderProgress } from './scenarioBuilderDraft'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { clampVUsersForRole, maxVUsersForRole, OBSERVER_MAX_VUSERS, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'
import {
  assignmentFromForm,
  cloneAssignment,
  ensureScenarioGroups,
  replaceScenarioRuntimeDefaults,
  reseedScenarioGroups,
  runtimeSettingsFromForm,
  scenarioFormStateFromStoredScenario,
} from './scenarioFormModel'
import {
  canonicalThinkTimeType,
} from './thinkTimeConfig'
import { requestSettingsFocus } from './settingsSectionLayout'
import { loadAllTestSummaries } from './testCatalogPagination'
import { scenarioSeedFromTemplate } from './scenarioTemplateSeed'
import { duplicateScenarioGroupNameIndexes, scenarioGroupNameAfterScriptSelection } from './scenarioGroupUtils'
import { boundedAllSettled } from '../utils/boundedAllSettled'
import { ScenarioCompositionTimeline } from './ScenarioCompositionTimeline'
import TrafficProfileSourcePanel from './TrafficProfileSourcePanel'
import { formatTrafficProfileAxisTime } from './trafficProfilePreviewModel'

interface Props {
  applicationRepository?: ApplicationRepository | null
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onCreateTestFromScenario?: (scenarioId: string) => void
  onNavigate?: (page: NavPage) => void
  onScenarioViewChange?: (view: 'library' | 'builder' | 'plans') => void
  initialView?: 'library' | 'builder' | 'plans'
  featurePolicySignal?: number
}

export interface ScenarioFormState {
  name: string
  duration: string
  /** Preserved even though start delay is not currently exposed by this editor. */
  start_delay: string
  groups: ScenarioFormGroup[]
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  goal_target_average_tps?: number
  goal_min_vusers?: number
  goal_max_vusers?: number
  goal_p95_latency_ms?: number
  goal_max_error_pct?: number
  goal_config?: GoalConfig
  traffic_profile?: TrafficProfile
  continue_on_error?: boolean
  snapshot_on_error?: boolean
  runtime_iterations?: number
  runtime_pacing_type: '' | 'none' | 'fixed_delay' | 'random_delay' | 'fixed_interval' | 'random_interval' | 'tps'
  runtime_pacing_delay_ms?: number
  runtime_pacing_min_delay_ms?: number
  runtime_pacing_max_delay_ms?: number
  runtime_pacing_interval_ms?: number
  runtime_pacing_min_interval_ms?: number
  runtime_pacing_max_interval_ms?: number
  runtime_pacing_tps?: number
  runtime_think_time_type?: '' | 'none' | 'fixed' | 'recorded' | 'gaussian' | 'uniform'
  runtime_think_time_ms?: number
  runtime_think_time_std_ms?: number
  runtime_think_time_min_ms?: number
  runtime_think_time_max_ms?: number
  runtime_think_time_multiplier?: number
  runtime_think_time_limit_ms?: number
  runtime_ignore_think_time: boolean
  runtime_log_level: string
  runtime_auto_transactions: string
  /** Canonical stored override used to retain advanced fields the compact editor does not expose. */
  runtime_settings?: RuntimeSettingsOverride
  browser_device_profile?: string
  browser_orientation?: string
  browser_touch_mode?: '' | 'true' | 'false'
  browser_headless?: '' | 'true' | 'false'
  assignment_mode: NonNullable<GeneratorAssignmentSettings['mode']>
  assignment_targets: string[]
  assignment?: GeneratorAssignmentSettings
  data_pool_id?: string
  data_pool_alias?: string
  data_pool_access_mode?: ScenarioDataPoolBinding['access_mode']
  data_pool_selection_strategy?: ScenarioDataPoolBinding['selection_strategy']
  /** Complete aggregate retained while the compact data-pool editor changes the first binding. */
  data_pools?: ScenarioDataPoolBinding[]
}

export interface ScenarioFormGroup {
  name: string
  script_id: string
  role?: 'load' | 'observer'
  vusers: number
  ramp_up: string
	ramp_down: string
	start_offset?: string
  stages: LoadStageDraft[]
  iterations?: number
  initialization: 'at_runtime' | 'at_once'
  /** Per-group runtime overrides layered over the scenario-wide defaults. */
  runtimeSettings?: ScenarioRuntimeSettingsDraft
  assignment?: GeneratorAssignmentSettings
}

type ScenarioSortField = 'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_test_id' | 'last_run_at' | 'last_run_ended_at' | 'result_count'
type ScriptLibrarySortField = 'name' | 'created_at' | 'last_run_at' | 'associated_scenarios' | 'associated_releases' | 'protocol'
type GoalBaselineSortField = 'name' | 'id' | 'started_at' | 'duration' | 'vusers' | 'peak_tps' | 'art' | 'error_rate'
export type ScenarioStartChoice = ScenarioSourceMode | 'goal' | ''

// Workload shape backing the production traffic-profile workflow. Its domain
// feature gates the workflow's entry points and its analyze/derive endpoints.
const PRODUCTION_REPLAY_SHAPE_ID = 'production-replay'
type RunSettingsTab = 'scenario' | 'runtime' | 'infrastructure'

const scenarioWizardSteps: Array<{ id: ScenarioWizardStep, label: string }> = [
  { id: 'start', label: 'Start' },
  { id: 'scripts', label: 'Scripts' },
  { id: 'source', label: 'Shape / Source' },
  { id: 'run', label: 'Run Settings' },
  { id: 'data', label: 'Data' },
  { id: 'review', label: 'Review' },
]

const runSettingsTabs: Array<{ id: RunSettingsTab, label: string }> = [
  { id: 'scenario', label: 'Scenario Settings' },
  { id: 'runtime', label: 'Browser/Script Runtime' },
  { id: 'infrastructure', label: 'Controller/Generator' },
]


function emptyScenarioGroup(scriptId = '', index = 0): ScenarioFormGroup {
  return {
    name: index === 0 ? '' : `Group ${index + 1}`,
    script_id: scriptId,
    vusers: 10,
    ramp_up: '',
		ramp_down: '',
		start_offset: '',
    stages: [],
    initialization: 'at_runtime',
  }
}

function emptyScenarioForm(scriptId = ''): ScenarioFormState {
  return {
    name: '',
    duration: '1m',
    start_delay: '',
    groups: [emptyScenarioGroup(scriptId)],
    scenario_mode: 'vuser_driven',
    script_shakeout: false,
    resource_baseline: false,
    runtime_pacing_type: '',
    runtime_think_time_type: '',
    runtime_ignore_think_time: false,
    runtime_log_level: '',
    runtime_auto_transactions: '',
    browser_device_profile: '',
    browser_orientation: '',
    browser_touch_mode: '',
    browser_headless: '',
    assignment_mode: 'auto',
    assignment_targets: [],
    data_pool_id: '',
    data_pool_alias: '',
    data_pool_access_mode: 'read_only',
    data_pool_selection_strategy: 'sequential',
  }
}

function mergeCloneWithScriptSelections(current: ScenarioFormState, cloned: ScenarioFormState): ScenarioFormState {
  const groups = cloned.groups.map((clonedGroup, index) => {
    const authoredGroup = current.groups[index]
    if (!authoredGroup?.script_id) return clonedGroup
    return {
      ...clonedGroup,
      name: authoredGroup.name,
      script_id: authoredGroup.script_id,
      initialization: authoredGroup.initialization,
      role: authoredGroup.role,
    }
  })
  for (const authoredGroup of current.groups.slice(cloned.groups.length)) {
    if (authoredGroup.script_id) groups.push(authoredGroup)
  }
  return {
    ...cloned,
    name: current.name.trim() ? current.name : cloned.name,
    groups: ensureScenarioGroups(groups),
  }
}

type LoadStageType = 'ramp_up' | 'hold' | 'ramp_down'

function goalConfigFromForm(form: ScenarioFormState): GoalConfig {
  const goal: GoalConfig = { ...(form.goal_config || {}) }
  const setNumber = (field: keyof GoalConfig, value: number | undefined) => {
    if (value === undefined || value === null) {
      delete goal[field]
      return
    }
    goal[field] = Number(value) as never
  }
  setNumber('target_average_tps', form.goal_target_average_tps)
  setNumber('min_vusers', form.goal_min_vusers)
  setNumber('max_vusers', form.goal_max_vusers)
  setNumber('p95_latency_ms', form.goal_p95_latency_ms)
  setNumber('max_error_pct', form.goal_max_error_pct)
  return goal
}

function scenarioFormWithGoalField<K extends keyof GoalConfig>(
  form: ScenarioFormState,
  field: K,
  value: GoalConfig[K],
): ScenarioFormState {
  const goal: GoalConfig = { ...(form.goal_config || {}) }
  if (value === undefined || value === null || value === '') {
    delete goal[field]
  } else {
    goal[field] = value as never
  }
  const next: ScenarioFormState = {
    ...form,
    goal_config: Object.keys(goal).length > 0 ? goal : undefined,
  }
  const numberValue = value as number | undefined
  switch (field) {
    case 'target_average_tps': next.goal_target_average_tps = numberValue; break
    case 'min_vusers': next.goal_min_vusers = numberValue; break
    case 'max_vusers': next.goal_max_vusers = numberValue; break
    case 'p95_latency_ms': next.goal_p95_latency_ms = numberValue; break
    case 'max_error_pct': next.goal_max_error_pct = numberValue; break
  }
  return next
}

function maxStageVUsers(stages: LoadStageDraft[]) {
  return stages.reduce((max, stage) => Math.max(max, Number(stage.target_vusers) || 0), 0)
}

function scenarioSeedFromPreset(preset: ScenarioShapePreset): ScenarioSeed {
  return {
    duration: preset.duration,
    scenario_mode: preset.scenarioMode || 'vuser_driven',
    script_shakeout: preset.scriptShakeout,
    resource_baseline: preset.resourceBaseline,
    groups: preset.groups.map(group => ({
      name: group.name,
      vusers: maxStageVUsers(group.stages || []) || group.vusers,
      ramp_up: group.ramp_up,
      ramp_down: group.ramp_down,
      stages: group.stages,
    })),
    goal: preset.goal,
    pacing: preset.pacing,
    continue_on_error: preset.continueOnError,
    snapshot_on_error: preset.snapshotOnError,
  }
}

function scenarioSeedFromForm(form: ScenarioFormState): ScenarioSeed {
  return {
    duration: form.duration,
    scenario_mode: form.scenario_mode,
    script_shakeout: form.script_shakeout,
    resource_baseline: form.resource_baseline,
    groups: form.groups.map(group => ({
      name: group.name,
      vusers: group.vusers,
      ramp_up: group.ramp_up,
      ramp_down: group.ramp_down,
      stages: group.stages,
      iterations: group.iterations,
    })),
    goal: goalConfigFromForm(form),
    pacing: {
      type: form.runtime_pacing_type,
      delay_ms: form.runtime_pacing_delay_ms,
      min_delay_ms: form.runtime_pacing_min_delay_ms,
      max_delay_ms: form.runtime_pacing_max_delay_ms,
      interval_ms: form.runtime_pacing_interval_ms,
      min_interval_ms: form.runtime_pacing_min_interval_ms,
      max_interval_ms: form.runtime_pacing_max_interval_ms,
      tps: form.runtime_pacing_tps,
    },
    think_time: {
      type: form.runtime_think_time_type,
      mean_ms: form.runtime_think_time_ms,
      std_ms: form.runtime_think_time_std_ms,
      min_ms: form.runtime_think_time_min_ms,
      max_ms: form.runtime_think_time_max_ms,
      multiplier: form.runtime_think_time_multiplier,
      limit_ms: form.runtime_think_time_limit_ms,
      ignore: form.runtime_ignore_think_time,
    },
    runtime_settings: form.runtime_settings ? structuredClone(form.runtime_settings) : undefined,
    iterations: form.runtime_iterations,
    log_level: form.runtime_log_level,
    auto_transactions: form.runtime_auto_transactions,
    continue_on_error: form.continue_on_error,
    snapshot_on_error: form.snapshot_on_error,
  }
}

function normalizeStageForPayload(stage: LoadStageDraft) {
  return {
    ...(stage.name ? { name: stage.name } : {}),
    type: stage.type,
    duration: stage.duration,
    ...(stage.target_vusers !== undefined ? { target_vusers: Number(stage.target_vusers) || 0 } : {}),
    ...(stage.target_tps !== undefined ? { target_tps: Number(stage.target_tps) || 0 } : {}),
  }
}

function normalizeGroupForPayload(group: ScenarioFormGroup) {
  const runtimeOwnsIterations = group.runtimeSettings?.run_logic !== undefined
    || group.runtimeSettings?.iterations !== undefined
  return {
    name: group.name,
    scriptId: group.script_id,
    role: group.role,
    vusers: maxStageVUsers(group.stages) || Number(group.vusers) || 10,
    rampUp: group.ramp_up,
		rampDown: group.ramp_down,
		startOffset: group.start_offset,
    stages: group.stages.map(normalizeStageForPayload),
    // A runtime iteration override and the compact legacy field are alternate
    // representations of the same setting. Never serialize both: the backend
    // rejects mismatched values and the runtime editor promotes edits into
    // run_logic.
    iterations: runtimeOwnsIterations ? undefined : group.iterations,
    initialization: group.initialization,
    runtimeSettings: group.runtimeSettings,
    assignment: group.assignment,
  }
}

function findScriptById(scripts: StoredScript[], scriptId?: string) {
  if (!scriptId) return undefined
  return scripts.find(script => script.id === scriptId)
}

function createDefaultStages(vusers: number, rampUp = '2m', totalDuration = '5m', rampDown = '1m'): LoadStageDraft[] {
  const target = Number(vusers) || 10
  const totalSeconds = parseDurationToSeconds(totalDuration) || 300
  let rampUpSeconds = parseDurationToSeconds(rampUp) || Math.max(1, Math.round(totalSeconds * 0.3))
  let rampDownSeconds = parseDurationToSeconds(rampDown) || Math.max(1, Math.round(totalSeconds * 0.15))
  if (rampUpSeconds + rampDownSeconds >= totalSeconds) {
    rampUpSeconds = Math.max(1, Math.round(totalSeconds * 0.4))
    rampDownSeconds = Math.max(1, Math.round(totalSeconds * 0.2))
  }
  const holdSeconds = Math.max(1, totalSeconds - rampUpSeconds - rampDownSeconds)
  return [
    { name: 'Warmup', type: 'ramp_up', duration: formatTimeCompact(rampUpSeconds), target_vusers: target },
    { name: 'Steady', type: 'hold', duration: formatTimeCompact(holdSeconds), target_vusers: target },
    { name: 'Cooldown', type: 'ramp_down', duration: formatTimeCompact(rampDownSeconds), target_vusers: 1 },
  ]
}

function simpleRampFromStages(group: ScenarioFormGroup): Pick<ScenarioFormGroup, 'ramp_up' | 'ramp_down' | 'vusers'> {
  const firstRampUp = group.stages.find(stage => stage.type === 'ramp_up')?.duration || group.ramp_up
  const lastRampDown = [...group.stages].reverse().find(stage => stage.type === 'ramp_down')?.duration || group.ramp_down
  return {
    ramp_up: firstRampUp,
    ramp_down: lastRampDown,
    vusers: maxStageVUsers(group.stages) || group.vusers,
  }
}

function getLoadModelStatusClass(status?: string) {
  if (status === 'ready' || status === 'executed') return 'ready'
  if (status === 'blocked' || status === 'planned') return 'blocked'
  return 'needs-review'
}

function formatLoadModelDimension(value: string) {
  return value.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function WorkloadShapeVisual({ entry }: { entry: LoadModelSupportEntry }) {
  const title = `${entry.name} workload shape`

  if (entry.id === 'open-arrival-rate') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 46L12 37L24 37L24 46L36 46L36 34L48 34L48 46L60 46L60 31L72 31L72 46L84 46L84 28L96 28L96 46Z" />
        <path className="scenario-shape-line" d="M12 40L108 28" />
        {[18, 34, 50, 66, 82, 98].map(x => <circle key={x} className="scenario-shape-marker" cx={x} cy={34 - (x - 18) * 0.12} r="2.8" />)}
      </svg>
    )
  }

  if (entry.id === 'stepped-ramp') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50H30V40H48V30H66V20H84V38H108V50Z" />
        <path className="scenario-shape-line" d="M12 50H30V40H48V30H66V20H84V38H108" />
      </svg>
    )
  }

  if (entry.id === 'spike-rendezvous') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-event" d="M60 14V51" />
        <path className="scenario-shape-fill" d="M12 50H45L60 16L75 50H108V52H12Z" />
        <path className="scenario-shape-line" d="M12 50H45L60 16L75 50H108" />
      </svg>
    )
  }

  if (entry.id === 'soak') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L28 30H94L108 50V52H12Z" />
        <path className="scenario-shape-line" d="M12 50L28 30H94L108 50" />
      </svg>
    )
  }

  if (entry.id === 'stress') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 52L28 46L43 39L58 31L72 23L86 18L100 15L108 18V52Z" />
        <path className="scenario-shape-line" d="M12 52L28 46L43 39L58 31L72 23L86 18L100 15L108 18" />
      </svg>
    )
  }

  if (entry.id === PRODUCTION_REPLAY_SHAPE_ID) {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50C22 49 29 45 36 38C44 29 51 20 62 17C74 14 84 20 91 29C99 38 103 45 108 49V52H12Z" />
        <path className="scenario-shape-line" d="M12 50C22 49 29 45 36 38C44 29 51 20 62 17C74 14 84 20 91 29C99 38 103 45 108 49" />
      </svg>
    )
  }

  if (entry.id === 'goal-based') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-event" d="M12 24H108M12 43H108" />
        <path className="scenario-shape-line" d="M12 42C24 20 34 21 45 34S68 48 78 31S96 19 108 27" />
      </svg>
    )
  }

  if (entry.id === 'chaos-resilience') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L26 34H48L56 44L66 24L78 39L88 30L108 46V52H12Z" />
        <path className="scenario-shape-event" d="M55 15V51M78 15V51" />
        <path className="scenario-shape-line" d="M12 50L26 34H48L56 44L66 24L78 39L88 30L108 46" />
        <path className="scenario-shape-line-secondary" d="M16 40L34 40L42 30L50 45L62 31L72 45L84 33L104 33" />
      </svg>
    )
  }

  if (entry.id === 'mixed-protocol') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L24 38H96L108 50V52H12Z" />
        <path className="scenario-shape-line" d="M12 50L24 38H96L108 50" />
        <path className="scenario-shape-line-secondary" d="M12 52L32 44H86L108 52" />
      </svg>
    )
  }

  return (
    <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
      <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
      <path className="scenario-shape-fill" d="M12 50L30 28H92L108 46V52H12Z" />
      <path className="scenario-shape-line" d="M12 50L30 28H92L108 46" />
    </svg>
  )
}

function getScenarioSortValue(scenario: StoredScenario, field: ScenarioSortField): string | number {
  switch (field) {
    case 'scripts':
      return (scenario.scripts || []).length
    case 'data_pools':
      return (scenario.data_pools || []).length
    case 'created_at':
      return new Date(scenario.created_at || 0).getTime()
    case 'last_run_test_id':
      return (scenario.last_run_test_id || '').toLowerCase()
    case 'last_run_at':
      return new Date(scenario.last_run_at || 0).getTime()
    case 'last_run_ended_at':
      return new Date(scenario.last_run_ended_at || 0).getTime()
    case 'associated_releases':
      return (scenario.associated_releases || []).length
    case 'result_count':
      return scenario.result_count || 0
    case 'name':
    default:
      return (scenario.name || '').toLowerCase()
  }
}

function getRunSortValue(run: TestSummary, field: 'name' | 'status' | 'release_id' | 'start_time' | 'duration'): string | number {
  if (field === 'start_time') {
    return new Date(run.start_time || run.created_at || 0).getTime()
  }
  return String(run[field] || '').toLowerCase()
}

function latestRunTimestamp(run: TestSummary): string {
  return run.start_time || run.started_at || run.created_at || ''
}

function maxFinite(values: Array<number | undefined>): number | undefined {
  const finite = values.filter((value): value is number => Number.isFinite(value))
  if (finite.length === 0) return undefined
  return Math.max(...finite)
}

function firstFinite(values: Array<number | undefined>): number | undefined {
  return values.find((value): value is number => Number.isFinite(value))
}

function formatOptionalNumber(value: number | undefined, digits = 0): string {
  if (!Number.isFinite(value)) return '—'
  return Number(value).toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
}

function formatOptionalMs(value: number | undefined): string {
  const formatted = formatOptionalNumber(value, 0)
  return formatted === '—' ? formatted : `${formatted} ms`
}

function formatOptionalTps(value: number | undefined): string {
  const formatted = formatOptionalNumber(value, 1)
  return formatted === '—' ? formatted : formatted
}

function formatOptionalPercent(value: number | undefined): string {
  const formatted = formatOptionalNumber(value, 2)
  return formatted === '—' ? formatted : `${formatted}%`
}

function formatGoalSizingBasis(value: string): string {
  if (value === 'group') return 'Group metrics'
  if (value === 'overview') return 'Run overview'
  return value || '—'
}

function goalBaselinePeakTps(run: TestSummary, report?: RunArchiveReport): number | undefined {
  return maxFinite([
    report?.overview?.peak_tps,
    run.live_metrics?.tps,
    ...(run.sparkline || []).map(point => point.tps),
  ])
}

function goalBaselineArt(run: TestSummary, report?: RunArchiveReport): number | undefined {
  return firstFinite([
    report?.overview?.avg_response_ms,
    run.live_metrics?.avg_response_ms,
  ])
}

function goalBaselineErrorRate(run: TestSummary, report?: RunArchiveReport): number | undefined {
  return firstFinite([
    report?.overview?.error_pct,
    run.live_metrics?.error_pct,
  ])
}

function goalBaselineVusers(run: TestSummary, report?: RunArchiveReport): number | undefined {
  return firstFinite([
    run.total_vusers,
    report?.overview?.peak_active_vusers,
    run.live_metrics?.active_vusers,
  ])
}

function getGoalBaselineSortValue(run: TestSummary, report: RunArchiveReport | undefined, field: GoalBaselineSortField): string | number {
  switch (field) {
    case 'id':
      return run.id.toLowerCase()
    case 'started_at':
      return new Date(latestRunTimestamp(run) || 0).getTime()
    case 'duration':
      return parseDurationToSeconds(run.duration || '') || 0
    case 'vusers':
      return goalBaselineVusers(run, report) || 0
    case 'peak_tps':
      return goalBaselinePeakTps(run, report) || 0
    case 'art':
      return goalBaselineArt(run, report) || 0
    case 'error_rate':
      return goalBaselineErrorRate(run, report) || 0
    case 'name':
    default:
      return `${run.name || ''} ${run.id || ''}`.toLowerCase()
  }
}

interface GoalBaselineScriptRow {
  key: string
  group: string
  scriptId?: string
  scriptName: string
  protocol?: string
  vusers?: number
  metrics?: RunArchiveActionStats
}

function goalBaselineScriptRows(run: TestSummary, report?: RunArchiveReport): GoalBaselineScriptRow[] {
  const archiveGroups = report?.manifest?.provenance?.groups || []
  const sourceGroups = archiveGroups.length > 0 ? archiveGroups : (run.groups || [])
  if (sourceGroups.length === 0 && (run.script_id || run.script_name)) {
    return [{
      key: run.script_id || run.id,
      group: run.script_name || run.name,
      scriptId: run.script_id,
      scriptName: run.script_name || run.name,
      protocol: undefined,
      vusers: run.total_vusers,
      metrics: report?.by_group?.[run.script_name || run.name],
    }]
  }
  return sourceGroups.map((group, index) => {
    const groupName = group.name || group.script_name || group.script_id || `Group ${index + 1}`
    return {
      key: `${groupName}-${group.script_id || index}`,
      group: groupName,
      scriptId: group.script_id,
      scriptName: group.script_name || group.name || group.script_id || `Script ${index + 1}`,
      protocol: group.protocol,
      vusers: group.vusers,
      metrics: report?.by_group?.[groupName],
    }
  })
}

function goalBaselineSearchText(run: TestSummary, report?: RunArchiveReport): string {
  return [
    run.id,
    run.ref_key,
    run.name,
    run.status,
    run.release_id,
    run.duration,
    formatDateTime(latestRunTimestamp(run)),
    ...goalBaselineScriptRows(run, report).flatMap(row => [row.group, row.scriptId, row.scriptName, row.protocol]),
  ].filter(Boolean).join(' ').toLowerCase()
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return Array.from(new Set(releases.map(release => release.name))).join(', ')
}

function formatScenarioScripts(scenario: StoredScenario) {
  const scripts = scenario.scripts || []
  if (scripts.length === 0) return 'No scripts assigned'
  return scripts.map(script => script.script_name || script.name || script.script_id).join(', ')
}

function formatScenarioDataPools(scenario: StoredScenario) {
  const pools = scenario.data_pools || []
  if (pools.length === 0) return 'No data pools'
  return pools.map(pool => `${pool.alias} (${pool.pool_name || pool.pool_id})`).join(', ')
}

function formatScenarioResultCount(scenario: StoredScenario) {
  const count = scenario.result_count || 0
  return `${count} result${count === 1 ? '' : 's'}`
}

function formatScenarioRunTestID(scenario: StoredScenario) {
  return scenario.last_run_test_id || '—'
}

function scenarioSearchText(scenario: StoredScenario) {
  return [
    scenario.name,
    scenario.duration,
    scenario.last_run_test_id,
    scenario.assignment?.mode,
    ...(scenario.assignment?.selected_targets || []),
    ...(scenario.scripts || []).map(script => script.script_name || script.name || script.script_id),
    ...(scenario.data_pools || []).map(pool => `${pool.alias} ${pool.pool_name || pool.pool_id}`),
    ...(scenario.associated_releases || []).map(release => release.name),
    formatBrowserRuntimeSummary(scenario.runtime_settings?.browser),
  ].filter(Boolean).join(' ').toLowerCase()
}

function uniqueValues(values: Array<string | undefined>) {
  return Array.from(new Set(values.map(value => value?.trim()).filter(Boolean) as string[]))
}

function scriptProtocols(script: StoredScript) {
  return uniqueValues([script.protocol || 'http'])
}

function associatedScenarioList(script: StoredScript) {
  const scenarios = script.associated_scenarios || []
  if (scenarios.length === 0) return 'No scenarios'
  return uniqueValues(scenarios.map(scenario => scenario.name || scenario.id)).join(', ')
}

function scriptLibrarySearchText(script: StoredScript) {
  return [
    script.id,
    script.name,
    ...scriptProtocols(script),
    script.version,
    script.created_at,
    script.last_run_at,
    formatDate(script.created_at),
    formatDate(script.last_run_at),
    formatDateTime(script.last_run_at),
    script.authoring_health?.status,
    ...(script.associated_scenarios || []).map(scenario => `${scenario.id} ${scenario.name}`),
    ...(script.associated_releases || []).map(release => release.name),
    ...(script.associated_releases || []).map(release => release.id),
    ...(script.scenario_run_releases || []).map(release => release.name),
  ].filter(Boolean).join(' ').toLowerCase()
}

function scriptReleaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return uniqueValues(releases.map(release => release.name || release.id)).join(', ')
}

function formatScriptScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
}

function getScriptLibrarySortValue(script: StoredScript, field: ScriptLibrarySortField): string | number {
  switch (field) {
    case 'created_at':
      return new Date(script.created_at || 0).getTime()
    case 'last_run_at':
      return new Date(script.last_run_at || 0).getTime()
    case 'associated_scenarios':
      return associatedScenarioList(script).toLowerCase()
    case 'associated_releases':
      return scriptReleaseList(script).toLowerCase()
    case 'protocol':
      return scriptProtocols(script).join(', ').toLowerCase()
    case 'name':
    default:
      return `${script.name || ''} ${script.id || ''}`.toLowerCase()
  }
}

function scenarioAssignmentSummary(scenario: StoredScenario) {
  if (scenario.assignment?.mode === 'specific_hosts') {
    const targets = scenario.assignment.selected_targets || []
    if (targets.length === 0) return { badge: 'Specific Hosts', detail: 'No hosts selected' }
    return {
      badge: 'Specific Hosts',
      detail: targets.join(', '),
    }
  }
  return null
}

function ScenarioModelTooltip({ active, payload, label }: {
  active?: boolean
  payload?: Array<{ color?: string, name?: string, value?: number | string }>
  label?: string | number
}) {
  if (!active || !payload || payload.length === 0) return null
  return (
    <div className="scenario-model-tooltip">
      <strong>{formatModelTimeLabel(Number(label) || 0)}</strong>
      {payload
        .filter(item => item.value !== undefined && item.value !== null)
        .map(item => (
          <span key={`${item.name}-${item.color}`}>
            <i style={{ background: item.color || 'var(--accent)' }} />
            {item.name}: {typeof item.value === 'number' ? item.value.toFixed(item.value % 1 === 0 ? 0 : 1) : item.value}
          </span>
        ))}
    </div>
  )
}

function ScenarioModelPreviewPanel({
  model,
  shapeLabel,
  dataPoolLabel,
  validationStatus,
}: {
  model: ScenarioModelPreview
  shapeLabel: string
  dataPoolLabel: string
  validationStatus: string
}) {
  const groupSeries = model.series.filter(series => series.kind === 'group')
  const totalSeries = model.series.find(series => series.kind === 'total')
  const tpsSeries = model.series.filter(series => series.kind === 'tps')
  const includesTps = tpsSeries.length > 0
  const visibleSeries = model.series.filter(series => model.showVUsers || series.kind === 'tps')
  const formatTps = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 2 })

  return (
    <section className="scenario-model-preview-panel">
      <div className="scenario-model-preview-header">
        <div>
          <h4>Model Preview</h4>
          <p>{includesTps ? (model.showVUsers ? 'Planned TPS targets and VUsers over time, calculated from the current wizard draft.' : 'Planned adaptive TPS targets. VUser demand is determined by the goal controller at runtime.') : (model.showVUsers ? 'Planned VUsers over time by script group, calculated from the current wizard draft.' : 'VUser demand is determined by the goal controller at runtime and cannot be modeled ahead of the run.')}</p>
        </div>
        <div className="scenario-model-summary">
          {model.showVUsers && <span><strong>{model.peakVusers}</strong> peak VUsers</span>}
          {model.peakTps !== undefined && <span><strong>{formatTps(model.peakTps)}</strong> peak TPS</span>}
          {model.minTps !== undefined && <span><strong>{formatTps(model.minTps)}</strong> minimum TPS</span>}
          {model.averageTps !== undefined && <span><strong>{formatTps(model.averageTps)}</strong> average TPS</span>}
          <span><strong>{formatModelTimeLabel(model.durationSeconds)}</strong> duration</span>
          <span><strong>{groupSeries.length}</strong> groups</span>
          <span>{shapeLabel}</span>
          <span>{dataPoolLabel}</span>
          <span>{validationStatus}</span>
        </div>
      </div>
      {(model.showVUsers || includesTps) ? <div className="scenario-model-chart">
        <ResponsiveContainer width="100%" height={340}>
          <ComposedChart data={model.points} margin={{ top: 16, right: includesTps && model.showVUsers ? 92 : 24, bottom: 18, left: 24 }}>
            <CartesianGrid stroke="var(--border-subtle, var(--border))" strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="elapsedSeconds"
              tickFormatter={(value) => formatTrafficProfileAxisTime(Number(value))}
              stroke="var(--text-muted)"
              fontSize={11}
              height={42}
              tickLine={false}
              axisLine={false}
              label={{ value: 'Elapsed time', position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 11 }}
            />
            {model.showVUsers && <YAxis
              yAxisId="vusers"
              stroke="var(--text-muted)"
              fontSize={11}
              width={80}
              tickLine={false}
              axisLine={false}
              label={{ value: 'VUsers', angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 11 }}
            />}
            {includesTps && (
              <YAxis
                yAxisId="tps"
                orientation={model.showVUsers ? 'right' : 'left'}
                stroke="var(--warning)"
                fontSize={11}
                width={68}
                tickLine={false}
                axisLine={false}
                label={{ value: 'TPS', angle: model.showVUsers ? 90 : -90, position: model.showVUsers ? 'right' : 'left', fill: 'var(--warning)', fontSize: 11 }}
              />
            )}
            <Tooltip content={<ScenarioModelTooltip />} />
            {model.showVUsers && groupSeries.map(series => (
              <Area
                key={series.key}
                yAxisId="vusers"
                type={series.interpolation || 'linear'}
                dataKey={series.key}
                name={series.label}
                stackId="vusers"
                stroke={series.color}
                fill={series.color}
                fillOpacity={0.16}
                strokeWidth={2}
                dot={false}
                isAnimationActive={false}
              />
            ))}
            {model.showVUsers && totalSeries && (
              <Line
                yAxisId="vusers"
                type={totalSeries.interpolation || 'linear'}
                dataKey={totalSeries.key}
                name={totalSeries.label}
                stroke={totalSeries.color}
                strokeWidth={3}
                dot={false}
                isAnimationActive={false}
              />
            )}
            {tpsSeries.map(series => (
              <Line
                key={series.key}
                yAxisId="tps"
                type={series.interpolation || 'linear'}
                dataKey={series.key}
                name={series.label}
                stroke={series.color}
                strokeWidth={series.key === 'totalTps' ? 3 : 2}
                strokeDasharray={series.key === 'totalTps' ? '6 5' : undefined}
                dot={false}
                isAnimationActive={false}
              />
            ))}
          </ComposedChart>
        </ResponsiveContainer>
      </div> : <div className="scenario-model-chart"><p className="scenario-shape-guidance">No deterministic load curve is available. The goal controller will determine VUser demand during execution.</p></div>}
      <div className="scenario-model-legend">
        {visibleSeries.map(series => (
          <span key={series.key}>
            <i style={{ background: series.color }} />
            {series.label}
          </span>
        ))}
      </div>
    </section>
  )
}

function TestHub({ applicationRepository, onSelectTest, onOpenCommandCenter, onCreateTestFromScenario, onNavigate, onScenarioViewChange, initialView = 'library', featurePolicySignal = 0 }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const [engineDefaults, setEngineDefaults] = useState<ScriptConfig | undefined>(undefined)
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [loadModelTemplates, setLoadModelTemplates] = useState<LoadModelTemplate[]>([])
  const [loadModelSupportMatrix, setLoadModelSupportMatrix] = useState<LoadModelSupportEntry[]>([])
  const [selectedLoadModelTemplateId, setSelectedLoadModelTemplateId] = useState('')
  const [loadModelValidation, setLoadModelValidation] = useState<LoadModelValidationReport | null>(null)
  const [loadModelValidationLoading, setLoadModelValidationLoading] = useState(false)
  const [loadModelValidationError, setLoadModelValidationError] = useState('')
  const [showForm, setShowForm] = useState(initialView === 'builder')
  const [showPlans, setShowPlans] = useState(initialView === 'plans')
  const [planSeedScenarioId, setPlanSeedScenarioId] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [wizardStep, setWizardStep] = useState<ScenarioWizardStep>('start')
  const [maxVisitedWizardIndex, setMaxVisitedWizardIndex] = useState(0)
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const { runGuarded } = useNavigationGuardRunner()
  const [scenarioSourceMode, setScenarioSourceMode] = useState<ScenarioSourceMode>('shape')
  const [scenarioStartChoice, setScenarioStartChoice] = useState<ScenarioStartChoice>('')
  const [scenarioCloneQuery, setScenarioCloneQuery] = useState('')
  const [selectedCloneScenarioId, setSelectedCloneScenarioId] = useState('')
  const [selectedShapeId, setSelectedShapeId] = useState('')
  const [form, setForm] = useState<ScenarioFormState>(() => emptyScenarioForm())
  const markWizardStepVisited = useCallback((stepId: ScenarioWizardStep) => {
    const stepIndex = scenarioWizardSteps.findIndex(step => step.id === stepId)
    if (stepIndex >= 0) {
      setMaxVisitedWizardIndex(current => Math.max(current, stepIndex))
    }
  }, [])
  const [error, setError] = useState('')
  const [scenarioQuery, setScenarioQuery] = useState('')
  const [scenarioReleaseFilter, setScenarioReleaseFilter] = useState('all')
  const [scriptLibraryQuery, setScriptLibraryQuery] = useState('')
  const [scriptLibrarySortField, setScriptLibrarySortField] = useState<ScriptLibrarySortField>('name')
  const [scriptLibrarySortDirection, setScriptLibrarySortDirection] = useState<'asc' | 'desc'>('asc')
  const [scriptSelectionTargetIndex, setScriptSelectionTargetIndex] = useState<number | null>(null)
  const [sortField, setSortField] = useState<ScenarioSortField>('last_run_at')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [expandedScenarioId, setExpandedScenarioId] = useState<string | null>(null)
  const [scenarioRuns, setScenarioRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<'name' | 'status' | 'release_id' | 'start_time' | 'duration'>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const [confirmDeleteScenarioId, setConfirmDeleteScenarioId] = useState<string | null>(null)
  const [selectedScenarioIds, setSelectedScenarioIds] = useState<Set<string>>(new Set())
  const [deletingScenarioIds, setDeletingScenarioIds] = useState<Set<string>>(new Set())
  // Build-from-goal (Little's Law) sizing panel state.
  const [goalSizingOpen, setGoalSizingOpen] = useState(false)
  const [goalBaselineTests, setGoalBaselineTests] = useState<TestSummary[]>([])
  const [goalBaselineId, setGoalBaselineId] = useState('')
  const [goalBaselineQuery, setGoalBaselineQuery] = useState('')
  const [goalBaselineSortField, setGoalBaselineSortField] = useState<GoalBaselineSortField>('started_at')
  const [goalBaselineSortDirection, setGoalBaselineSortDirection] = useState<'asc' | 'desc'>('desc')
  const [expandedGoalBaselineId, setExpandedGoalBaselineId] = useState<string | null>(null)
  const [goalBaselineReports, setGoalBaselineReports] = useState<Record<string, RunArchiveReport>>({})
  const [goalBaselineReportErrors, setGoalBaselineReportErrors] = useState<Record<string, string>>({})
  const [goalBaselineLoadingId, setGoalBaselineLoadingId] = useState('')
  const [goalBaselineGroups, setGoalBaselineGroups] = useState<BaselineGroupOption[]>([])
  const [goalTargetTps, setGoalTargetTps] = useState<Record<string, string>>({})
  const [goalSizingResult, setGoalSizingResult] = useState<GoalSizingReport | null>(null)
  const [goalSizingLoading, setGoalSizingLoading] = useState(false)
  const [goalSizingError, setGoalSizingError] = useState('')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [stageGroupIndex, setStageGroupIndex] = useState(0)
  const [runtimeGroupIndex, setRuntimeGroupIndex] = useState(0)
  const [runSettingsTab, setRunSettingsTab] = useState<RunSettingsTab>('scenario')
  const scriptLibraryRef = useRef<HTMLDivElement | null>(null)
  const scriptLibrarySearchRef = useRef<HTMLInputElement | null>(null)
  const scriptGroupRefs = useRef<Array<HTMLDivElement | null>>([])
  const goalBaselineTableRef = useRef<HTMLDivElement | null>(null)
  const goalSizingResultRef = useRef<HTMLDivElement | null>(null)
  const deleteScenarioDialogRef = useRef<HTMLDivElement | null>(null)
  const deleteScenarioInitialFocusRef = useRef<HTMLButtonElement | null>(null)
  const editingBaselineFormRef = useRef('')
  const persistScenarioRef = useRef<() => Promise<StoredScenario | null>>(async () => null)
  const resetScenarioBuilderRef = useRef<() => void>(() => undefined)
  const refreshRef = useRef<() => Promise<void>>(async () => undefined)
  const isScenarioBuilderDirtyRef = useRef<() => boolean>(() => false)
  const currentProjectIdRef = useRef(activeProjectId)
  const refreshGenerationRef = useRef(0)
  const goalSizingScopeGenerationRef = useRef(0)
  const goalBaselineSelectionGenerationRef = useRef(0)
  const effectiveAccessScopeKey = `${activeProjectId || ''}\u0000${activeProject?.domain_id || ''}\u0000${applicationRepository?.id || ''}`
  const effectiveAccessScopeKeyRef = useRef(effectiveAccessScopeKey)
  effectiveAccessScopeKeyRef.current = effectiveAccessScopeKey
  if (currentProjectIdRef.current !== activeProjectId) {
    currentProjectIdRef.current = activeProjectId
    refreshGenerationRef.current += 1
    goalSizingScopeGenerationRef.current += 1
    goalBaselineSelectionGenerationRef.current += 1
  }
  const closeDeleteScenarioDialog = useCallback(() => setConfirmDeleteScenarioId(null), [])
  useModalDialog({
    dialogRef: deleteScenarioDialogRef,
    enabled: Boolean(confirmDeleteScenarioId),
    initialFocusRef: deleteScenarioInitialFocusRef,
    onClose: closeDeleteScenarioDialog,
  })

  useLayoutEffect(() => {
    setScenarios([])
    setScripts([])
    setDataPools([])
    setSelectedCloneScenarioId('')
    setExpandedScenarioId(null)
    setScenarioRuns({})
    setLoadingRunsFor(null)
    setConfirmDeleteScenarioId(null)
    setSelectedScenarioIds(new Set())
    setDeletingScenarioIds(new Set())
    setGoalBaselineTests([])
    setGoalBaselineId('')
    setGoalBaselineQuery('')
    setExpandedGoalBaselineId(null)
    setGoalBaselineReports({})
    setGoalBaselineReportErrors({})
    setGoalBaselineLoadingId('')
    setGoalBaselineGroups([])
    setGoalTargetTps({})
    setGoalSizingResult(null)
    setGoalSizingError('')
    setGoalSizingLoading(false)
    setGoalSizingOpen(false)
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    setError('')
    // The builder's identity and every authored value belong to the project in
    // which the draft was opened. An unchanged edit is intentionally not dirty,
    // so the global navigation guard may allow a project switch without asking;
    // reset the edit binding here so a subsequent save cannot update the prior
    // project's scenario from the new project workspace.
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(emptyScenarioForm())
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setScenarioCloneQuery('')
    setWizardStep('start')
    setMaxVisitedWizardIndex(0)
    setStageGroupIndex(0)
    setRuntimeGroupIndex(0)
    setRunSettingsTab('scenario')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setPlanSeedScenarioId(null)
  }, [activeProjectId])
  const resetGoalBaselineTableScroll = () => {
    if (goalBaselineTableRef.current) goalBaselineTableRef.current.scrollLeft = 0
  }
  const formGroups = useMemo(
    () => form.groups.length > 0 ? form.groups : [emptyScenarioGroup()],
    [form.groups],
  )
  const allScenarioGroupsHaveScripts = formGroups.every(group => Boolean(group.script_id.trim()))
  const duplicateGroupNameIndexes = useMemo(
    () => duplicateScenarioGroupNameIndexes(formGroups.map(group => group.name)),
    [formGroups],
  )
  const scenarioGroupNameError = duplicateGroupNameIndexes.size > 0
    ? 'Group names must be unique. Rename duplicate groups before continuing.'
    : ''
  const canAddScenarioGroup = allScenarioGroupsHaveScripts && !scenarioGroupNameError
  const addScenarioGroupBlockedReason = scenarioGroupNameError || 'Add a script to every existing group before adding another group.'
  const activeGroupIndex = Math.min(stageGroupIndex, formGroups.length - 1)
  const activeStageGroup = formGroups[activeGroupIndex] || formGroups[0]
  const activeProfileMapping = form.traffic_profile?.mappings.find(mapping => mapping.group_name === (activeStageGroup?.name || `Group ${activeGroupIndex + 1}`))
  const activeStageTimeline = useMemo(
    () => validateStageTimeline(form.duration, activeStageGroup?.stages || []),
    [activeStageGroup?.stages, form.duration],
  )
  const blockingStageTimelineMessage = useMemo(() => {
    for (let index = 0; index < formGroups.length; index += 1) {
      const group = formGroups[index]
      if (!group.stages.length) continue
      const validation = validateStageTimeline(form.duration, group.stages)
      if (validation.status === 'error') {
        return `${group.name || `Group ${index + 1}`}: ${validation.message}`
      }
    }
    return ''
  }, [form.duration, formGroups])
  const observerEnvelopeError = useMemo(() => {
    for (let index = 0; index < formGroups.length; index += 1) {
      const group = formGroups[index]
      const script = findScriptById(scripts, group.script_id)
      const peakVUsers = Math.max(Number(group.vusers) || 0, maxStageVUsers(group.stages))
      const groupError = validateGroupExecutionEnvelope({
        role: group.role,
        protocol: script?.protocol,
        vusers: peakVUsers,
        label: group.name || `Group ${index + 1}`,
      })
      if (groupError) return groupError
    }
    return ''
  }, [formGroups, scripts])
  const aggregateEnvelopeError = useMemo(
    () => validateAggregateVUserDemand(formGroups, 'Scenario') || '',
    [formGroups],
  )
  const assignmentValidationError = useMemo(() => {
    const rootError = validateGeneratorAssignment(assignmentFromForm(form), 'scenario', 'Scenario assignment')
    if (rootError) return rootError
    for (let index = 0; index < formGroups.length; index += 1) {
      const group = formGroups[index]
      const groupError = validateGeneratorAssignment(group.assignment, 'group', group.name || `Group ${index + 1}`)
      if (groupError) return groupError
    }
    return ''
  }, [form, formGroups])
  const showBrowserRuntimeSettings = formGroups.some(group => findScriptById(scripts, group.script_id)?.protocol === 'browser')
  const canManageTests = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'),
    [effectiveAuthAccessResources],
  )
  const canManageProjects = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects'),
    [effectiveAuthAccessResources],
  )
  const shapeCatalog = useMemo(
    () => buildScenarioShapeCatalog(loadModelSupportMatrix, loadModelTemplates),
    [loadModelSupportMatrix, loadModelTemplates],
  )
  const selectedShape = shapeCatalog.find(option => option.id === selectedShapeId)
  const selectedShapePreset: ScenarioShapePreset | undefined = selectedShape ? getScenarioShapePreset(selectedShape.id) : undefined
  const selectedTemplate = loadModelTemplates.find(template => template.id === selectedLoadModelTemplateId)
  const selectedCloneScenario = useMemo(
    () => scenarios.find(scenario => scenario.id === selectedCloneScenarioId) || null,
    [scenarios, selectedCloneScenarioId],
  )
  const scenarioModel = useMemo(
    () => buildScenarioModelPreview({
      duration: form.duration,
      scenarioMode: form.scenario_mode,
      groups: formGroups.map(group => ({
        name: group.name || findScriptById(scripts, group.script_id)?.name || undefined,
        vusers: Number(group.vusers) || 0,
        ramp_up: group.ramp_up,
        ramp_down: group.ramp_down,
        stages: group.stages,
      })),
      goalTargetTps: form.goal_target_average_tps,
      trafficProfile: form.traffic_profile,
    }),
    [form.duration, form.goal_target_average_tps, form.scenario_mode, form.traffic_profile, formGroups, scripts],
  )
  const filteredCloneScenarios = useMemo(() => {
    const query = scenarioCloneQuery.trim().toLowerCase()
    if (!query) return scenarios
    return scenarios.filter(scenario => scenarioSearchText(scenario).includes(query))
  }, [scenarioCloneQuery, scenarios])
  const visibleScriptLibrary = useMemo(() => {
    const query = scriptLibraryQuery.trim().toLowerCase()
    const filtered = query
      ? scripts.filter(script => scriptLibrarySearchText(script).includes(query))
      : scripts
    return [...filtered].sort((left, right) => {
      const leftValue = getScriptLibrarySortValue(left, scriptLibrarySortField)
      const rightValue = getScriptLibrarySortValue(right, scriptLibrarySortField)
      const direction = scriptLibrarySortDirection === 'asc' ? 1 : -1
      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * direction
      }
      return String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true, sensitivity: 'base' }) * direction
    })
  }, [scriptLibraryQuery, scriptLibrarySortDirection, scriptLibrarySortField, scripts])
  const visibleGoalBaselineTests = useMemo(() => {
    const query = goalBaselineQuery.trim().toLowerCase()
    const filtered = query
      ? goalBaselineTests.filter(test => goalBaselineSearchText(test, goalBaselineReports[test.id]).includes(query))
      : goalBaselineTests
    return [...filtered].sort((left, right) => {
      const leftValue = getGoalBaselineSortValue(left, goalBaselineReports[left.id], goalBaselineSortField)
      const rightValue = getGoalBaselineSortValue(right, goalBaselineReports[right.id], goalBaselineSortField)
      const direction = goalBaselineSortDirection === 'asc' ? 1 : -1
      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * direction
      }
      return String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true, sensitivity: 'base' }) * direction
    })
  }, [goalBaselineQuery, goalBaselineReports, goalBaselineSortDirection, goalBaselineSortField, goalBaselineTests])
  const showScenarioLibrary = !showForm && !showPlans
  const showScriptLibrary = showForm && wizardStep === 'scripts'
  const showModelPreview = showForm && wizardStep !== 'start' && wizardStep !== 'scripts'

  const buildScenarioDraftPayload = useCallback((draftForm: ScenarioFormState = form) => {
    const retainedPools = (draftForm.data_pools || []).map(binding => ({ ...binding, access_mode: 'read_only' as const }))
    // The compact editor surfaces only the first binding. Clearing it must not
    // wipe bindings 2..N, which the wizard never displays.
    const dataPoolsPayload = draftForm.data_pool_id ? [{
      ...(retainedPools[0] || {}),
      pool_id: draftForm.data_pool_id,
      alias: (draftForm.data_pool_alias || dataPools.find(pool => pool.id === draftForm.data_pool_id)?.name || 'data').trim().toLowerCase().replace(/\s+/g, '_'),
      access_mode: 'read_only' as const,
      selection_strategy: draftForm.data_pool_selection_strategy || 'sequential',
    }, ...retainedPools.slice(1)] : retainedPools.slice(1)

    return buildScenarioUpsertPayload({
      name: draftForm.name || 'Draft scenario',
      projectId: activeProjectId,
      workloadShapeId: selectedShapeId,
      duration: draftForm.duration || '1m',
      startDelay: draftForm.start_delay,
      scenarioMode: draftForm.scenario_mode,
      scriptShakeout: draftForm.script_shakeout,
      resourceBaseline: draftForm.resource_baseline,
      assignment: assignmentFromForm(draftForm),
      groups: draftForm.groups.map(normalizeGroupForPayload),
      goalConfig: goalConfigFromForm(draftForm),
      runtimeSettings: runtimeSettingsFromForm(draftForm, showBrowserRuntimeSettings),
      trafficProfile: draftForm.traffic_profile,
      dataPools: dataPoolsPayload,
    })
  }, [activeProjectId, dataPools, form, selectedShapeId, showBrowserRuntimeSettings])

  const refresh = useCallback(async () => {
    const projectId = activeProjectId
    const requestGeneration = ++refreshGenerationRef.current
    if (!projectId) return
    try {
      const [scenarioRes, scriptRes, dataPoolRes, templateRes, supportRes] = await Promise.all([
        api.listScenarios(projectId),
        api.listScripts(projectId),
        api.listDataPools(projectId),
        api.listLoadModelTemplates({ project_id: projectId }),
        api.getLoadModelSupportMatrix(projectId),
      ])
      if (requestGeneration !== refreshGenerationRef.current || currentProjectIdRef.current !== projectId) return
      const nextScenarios = scenarioRes.scenarios || []
      const nextScripts = scriptRes.scripts || []
      setScenarios(nextScenarios)
      setScripts(nextScripts)
      setDataPools(dataPoolRes.data_pools || [])
      setLoadModelTemplates(templateRes.templates || [])
      setLoadModelSupportMatrix(supportRes.support_matrix || [])
      setForm(prev => ({
        ...prev,
        groups: ensureScenarioGroups(prev.groups.map(group => ({
          ...group,
          script_id: group.script_id && nextScripts.some(script => script.id === group.script_id) ? group.script_id : '',
        }))),
      }))
    } catch (err: unknown) {
      if (requestGeneration === refreshGenerationRef.current && currentProjectIdRef.current === projectId) {
        setError(getErrorMessage(err, 'Unable to load scenarios'))
      }
    }
  }, [activeProjectId, api])

  useEffect(() => { void refresh() }, [featurePolicySignal, refresh])

  useEffect(() => {
    if (!featurePolicySignal) return
    const grandfatheredShapeId = editingId
      ? scenarios.find(scenario => scenario.id === editingId)?.workload_shape_id || ''
      : ''
    if (!selectedShapeId || loadModelSupportMatrix.some(shape => shape.id === selectedShapeId)) return
    if (selectedShapeId === grandfatheredShapeId) return
    setSelectedShapeId('')
    setSelectedLoadModelTemplateId('')
    setError('The selected workload shape was disabled by an administrator. Compatible draft values were retained as a Custom Workload.')
  }, [editingId, featurePolicySignal, loadModelSupportMatrix, scenarios, selectedShapeId])

  // The production-replay workflow has its own entry points and its own
  // analyze/derive endpoints, so it has to honour the shape's feature policy
  // itself rather than relying on the shape catalog it never reads. An empty
  // matrix means "not loaded yet", not "disabled", so the card does not flicker.
  const trafficProfileShapeEnabled = useMemo(
    () => loadModelSupportMatrix.length === 0 || loadModelSupportMatrix.some(shape => shape.id === PRODUCTION_REPLAY_SHAPE_ID),
    [loadModelSupportMatrix],
  )

  useEffect(() => {
    // A scenario saved before the shape was disabled keeps its profile and stays
    // editable; only an in-progress draft is moved off the hidden workflow.
    if (trafficProfileShapeEnabled || form.traffic_profile) return
    setScenarioSourceMode(current => (current === 'traffic_profile' ? 'shape' : current))
    setScenarioStartChoice(current => (current === 'traffic_profile' ? 'shape' : current))
  }, [form.traffic_profile, trafficProfileShapeEnabled])
  // Engine config defaults are static app-wide; fetch once so the runtime editor can
  // show the actual inherited value (never a blank placeholder) for unset fields.
  useEffect(() => {
    let cancelled = false
    api.getConfigDefaults().then(r => { if (!cancelled) setEngineDefaults(r.config) }).catch(() => {})
    return () => { cancelled = true }
  }, [api])
  useEffect(() => {
    let cancelled = false
    const projectId = activeProjectId
    const requestScopeKey = effectiveAccessScopeKey
    const isCurrentRequest = () => !cancelled && effectiveAccessScopeKeyRef.current === requestScopeKey
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    if (!projectId) return () => { cancelled = true }
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(projectId, {
          domain_id: activeProject?.domain_id,
          application_repository_id: applicationRepository?.id,
        })
        if (isCurrentRequest()) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (isCurrentRequest()) {
          setEffectiveAuthAccessResources([])
        }
      } finally {
        if (isCurrentRequest()) {
          setEffectiveAccessLoaded(true)
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProjectId, activeProject?.domain_id, api, applicationRepository?.id, effectiveAccessScopeKey])
  useEffect(() => {
    // Only tear down manage-only views once access has loaded; the brief
    // default-DENY window before the access fetch resolves must not reset a
    // deep-linked builder/edit view for a user who can in fact manage tests.
    if (effectiveAccessLoaded && !canManageTests) {
      setShowForm(false)
      setEditingId(null)
      setConfirmDeleteScenarioId(null)
    }
  }, [effectiveAccessLoaded, canManageTests])

  useEffect(() => {
    setStageGroupIndex(current => Math.min(current, Math.max(0, form.groups.length - 1)))
  }, [form.groups.length])

  useEffect(() => {
    scriptGroupRefs.current = scriptGroupRefs.current.slice(0, form.groups.length)
    setScriptSelectionTargetIndex(current => (
      current !== null && current >= form.groups.length ? null : current
    ))
  }, [form.groups.length])

  useEffect(() => {
    if (!showForm || form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setDraftEstimate(null)
      setEstimateLoading(false)
      setEstimateError('')
      return
    }

    let cancelled = false
    setEstimateError('')
    const timer = window.setTimeout(() => {
      if (cancelled) return
      setEstimateLoading(true)
      const draftPayload = buildScenarioDraftPayload()
      api.estimateTest(buildCreateTestPayload({
        name: form.name,
        duration: draftPayload.duration,
        startDelay: '',
        scenarioMode: draftPayload.scenario_mode,
        scriptShakeout: false,
        resourceBaseline: false,
        assignment: draftPayload.assignment,
        groups: form.groups.map(normalizeGroupForPayload),
        goalConfig: goalConfigFromForm(form),
        runtimeSettings: runtimeSettingsFromForm(form, showBrowserRuntimeSettings),
      }))
      .then((estimate) => {
        if (cancelled) return
        setDraftEstimate(estimate)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setDraftEstimate(null)
        setEstimateError(getErrorMessage(err, 'Unable to estimate assignment guidance'))
      })
      .finally(() => {
        if (cancelled) return
        setEstimateLoading(false)
      })
    }, 300)

    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [api, buildScenarioDraftPayload, form, showBrowserRuntimeSettings, showForm])

  const scenarioReleaseOptions = useMemo(() => {
    const names = new Set<string>()
    scenarios.forEach(scenario => (scenario.associated_releases || []).forEach(release => release.name && names.add(release.name)))
    return Array.from(names).sort((a, b) => a.localeCompare(b))
  }, [scenarios])

  const openRemoteSettings = useCallback(() => {
    requestSettingsFocus('integrations')
    onNavigate?.('settings')
  }, [onNavigate])

  const sorted = useMemo(() => {
    const query = scenarioQuery.trim().toLowerCase()
    const filteredScenarios = scenarios.filter(scenario => {
      if (scenarioReleaseFilter !== 'all' && !(scenario.associated_releases || []).some(release => release.name === scenarioReleaseFilter)) return false
      if (!query) return true
      return scenarioSearchText(scenario).includes(query)
    })
    return [...filteredScenarios].sort((a, b) => {
      const aValue = getScenarioSortValue(a, sortField)
      const bValue = getScenarioSortValue(b, sortField)
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scenarios, scenarioQuery, scenarioReleaseFilter, sortField, sortDirection])

  const scenarioFederation = useLibraryFederation({
    kind: 'scenario',
    projectId: activeProjectId,
    onRefreshLocal: refresh,
    requireProjectScope: true,
  })

  const knownScenarioIds = useMemo(() => new Set(scenarios.map(scenario => scenario.id)), [scenarios])

  const federationRemoteScenarioRows = useMemo(() => {
    if (scenarioFederation.sourceFilter === 'local') return []
    if (scenarioReleaseFilter !== 'all') return []
    const query = scenarioQuery.trim().toLowerCase()
    return scenarioFederation.remoteItems.filter(item => {
      if (scenarioFederation.sourceFilter === 'all' && item.local_item_id && knownScenarioIds.has(item.local_item_id)) return false
      if (!query) return true
      return [item.name, item.protocol, item.project_id].filter(Boolean).join(' ').toLowerCase().includes(query)
    })
  }, [scenarioFederation.remoteItems, scenarioFederation.sourceFilter, knownScenarioIds, scenarioQuery, scenarioReleaseFilter])

  const showLocalScenarios = scenarioFederation.sourceFilter !== 'host'
  const visibleScenarioIds = useMemo(
    () => showLocalScenarios ? sorted.map(scenario => scenario.id) : [],
    [showLocalScenarios, sorted],
  )
  const selectedVisibleScenarioIds = useMemo(
    () => visibleScenarioIds.filter(id => selectedScenarioIds.has(id)),
    [selectedScenarioIds, visibleScenarioIds],
  )

  useEffect(() => {
    const visible = new Set(visibleScenarioIds)
    setSelectedScenarioIds(current => {
      const next = new Set(Array.from(current).filter(id => visible.has(id)))
      return next.size === current.size ? current : next
    })
  }, [visibleScenarioIds])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else {
      setSortField(field)
      setSortDirection(field === 'last_run_at' || field === 'last_run_ended_at' || field === 'result_count' ? 'desc' : 'asc')
    }
  }

  const handleScriptLibrarySort = (field: ScriptLibrarySortField) => {
    if (scriptLibrarySortField === field) {
      setScriptLibrarySortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setScriptLibrarySortField(field)
      setScriptLibrarySortDirection(field === 'created_at' || field === 'last_run_at' ? 'desc' : 'asc')
    }
  }

  const handleGoalBaselineSort = (field: GoalBaselineSortField) => {
    resetGoalBaselineTableScroll()
    if (goalBaselineSortField === field) {
      setGoalBaselineSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setGoalBaselineSortField(field)
      setGoalBaselineSortDirection(['started_at', 'vusers', 'peak_tps', 'art', 'error_rate'].includes(field) ? 'desc' : 'asc')
    }
  }

  const handleInnerSort = (field: typeof innerSortField) => {
    if (innerSortField === field) setInnerSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setInnerSortField(field); setInnerSortDirection('asc') }
  }

  const getSortedInnerRuns = (runs: TestSummary[]) => {
    return [...runs].sort((a, b) => {
      const aValue = getRunSortValue(a, innerSortField)
      const bValue = getRunSortValue(b, innerSortField)
      if (aValue < bValue) return innerSortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return innerSortDirection === 'asc' ? 1 : -1
      return 0
    })
  }

  const loadScenarioRuns = async (scenario: StoredScenario) => {
    if (scenarioRuns[scenario.id]) return
    setLoadingRunsFor(scenario.id)
    try {
      const res = await api.listScenarioResults(scenario.id)
      const runs = (res.tests || []).sort((a: TestSummary, b: TestSummary) => {
        const aTime = new Date(a.start_time || a.created_at || 0).getTime()
        const bTime = new Date(b.start_time || b.created_at || 0).getTime()
        return bTime - aTime
      })
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: runs }))
    } catch {
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: [] }))
    } finally {
      setLoadingRunsFor(null)
    }
  }

  const toggleScenarioRuns = async (scenario: StoredScenario) => {
    if (expandedScenarioId === scenario.id) {
      setExpandedScenarioId(null)
      return
    }
    setExpandedScenarioId(scenario.id)
    await loadScenarioRuns(scenario)
  }

  // Reset every builder field to a pristine "new scenario" state. Does not touch
  // showForm/view or the persisted draft — callers decide those.
  const resetBuilderToBlank = useCallback(() => {
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(emptyScenarioForm())
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setGoalSizingResult(null)
    setWizardStep('start')
    setMaxVisitedWizardIndex(0)
    setStageGroupIndex(0)
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setRunSettingsTab('scenario')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
  }, [])

  const openCreate = useCallback(() => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    clearScenarioBuilderDraft()
    resetBuilderToBlank()
    setShowForm(true)
    setShowPlans(false)
    onScenarioViewChange?.('builder')
  }, [canManageTests, onScenarioViewChange, resetBuilderToBlank])

  // Discard the in-progress draft and start a fresh blank scenario (stays in the builder).
  const startBlankScenario = useCallback(() => {
    if (!window.confirm('Discard this in-progress scenario draft and start over with a blank builder?')) return
    clearScenarioBuilderDraft()
    resetBuilderToBlank()
  }, [resetBuilderToBlank])

  const openScenarioLibrary = useCallback(() => {
    setShowForm(false)
    setShowPlans(false)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setConfirmDeleteScenarioId(null)
    setScriptSelectionTargetIndex(null)
    setScenarioStartChoice('')
    setMaxVisitedWizardIndex(0)
    onScenarioViewChange?.('library')
  }, [onScenarioViewChange])

  const openExecutionPlans = useCallback(() => {
    setShowForm(false)
    setShowPlans(true)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    onScenarioViewChange?.('plans')
  }, [onScenarioViewChange])

  const guardedOpenScenarioLibrary = useCallback(() => {
    runGuarded(openScenarioLibrary)
  }, [openScenarioLibrary, runGuarded])

  const guardedOpenExecutionPlans = useCallback(() => {
    runGuarded(openExecutionPlans)
  }, [openExecutionPlans, runGuarded])

  const addScenarioToPlan = useCallback((scenarioId: string) => {
    setPlanSeedScenarioId(scenarioId)
    openExecutionPlans()
  }, [openExecutionPlans])

  const scenarioPageControls = useMemo(() => (
    <PageViewToggle
      value={showPlans ? 'plans' : showForm ? 'builder' : 'library'}
      ariaLabel="Scenario page view"
      className="scenario-page-toggle topbar-page-section-toggle"
      onChange={view => {
        if (view === 'library') guardedOpenScenarioLibrary()
        else if (view === 'plans') guardedOpenExecutionPlans()
        else openCreate()
      }}
      options={[
        { value: 'library', label: 'Scenario Library' },
        { value: 'builder', label: 'Scenario Builder', disabled: !canManageTests },
        { value: 'plans', label: 'Execution Plans' },
      ]}
    />
  ), [canManageTests, guardedOpenExecutionPlans, guardedOpenScenarioLibrary, openCreate, showForm, showPlans])

  const scenarioTopbarContribution = useMemo(() => ({
    id: 'scenario-page',
    title: showPlans ? 'Execution Plans' : showForm ? 'Scenario Builder' : 'Scenario Library',
    controls: scenarioPageControls,
    priority: 10,
  }), [scenarioPageControls, showForm, showPlans])

  useTopbarContribution(scenarioTopbarContribution)

  const openEdit = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    const fallbackScriptId = scripts[0]?.id || ''
    const nextForm = scenarioFormStateFromStoredScenario(scenario, fallbackScriptId)
    setEditingId(scenario.id)
    editingBaselineFormRef.current = JSON.stringify(nextForm)
    setForm(nextForm)
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId(scenario.workload_shape_id || '')
    setSelectedCloneScenarioId(scenario.id)
    setScenarioSourceMode(scenario.traffic_profile ? 'traffic_profile' : 'existing')
    setScenarioStartChoice(scenario.traffic_profile ? 'traffic_profile' : 'existing')
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setRunSettingsTab('scenario')
    setWizardStep('scripts')
    setMaxVisitedWizardIndex(scenarioWizardSteps.findIndex(step => step.id === 'source'))
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(true)
  }

  const duplicateScenario = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    openEdit(scenario)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(prev => ({
      ...prev,
      name: `${scenario.name} (Copy)`,
      data_pools: prev.data_pools?.map(binding => ({
        pool_id: binding.pool_id,
        pool_name: binding.pool_name,
        alias: binding.alias,
        access_mode: 'read_only',
        selection_strategy: binding.selection_strategy,
      })),
    }))
  }

  const selectScenarioSourceMode = (mode: ScenarioSourceMode) => {
    setScenarioSourceMode(mode)
    setScenarioStartChoice(mode)
    if (mode === 'shape') setSelectedCloneScenarioId('')
    setWizardStep('scripts')
  }

  const cloneScenarioDraft = (scenario: StoredScenario) => {
    const fallbackScriptId = scripts[0]?.id || ''
    const clonedForm = scenarioFormStateFromStoredScenario(scenario, fallbackScriptId, true)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(current => mergeCloneWithScriptSelections(current, clonedForm))
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId(scenario.workload_shape_id || '')
    setSelectedCloneScenarioId(scenario.id)
    setScenarioSourceMode(scenario.traffic_profile ? 'traffic_profile' : 'existing')
    setScenarioStartChoice(scenario.traffic_profile ? 'traffic_profile' : 'existing')
    setScriptSelectionTargetIndex(null)
    setRunSettingsTab('scenario')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    markWizardStepVisited('source')
    void loadScenarioRuns(scenario)
  }

  // Single form-seeding path shared by the starter-template button and the workload-shape
  // catalog. Shape-controlled fields are refreshed while authored per-group script,
  // role, initialization, iteration, runtime, and placement settings are retained.
  const seedFormFromScenario = (seed: ScenarioSeed) => {
    setForm(prev => {
      const replacesRuntime = seed.runtime_settings !== undefined
      return {
      ...prev,
      duration: seed.duration || prev.duration,
      scenario_mode: seed.scenario_mode || 'vuser_driven',
      script_shakeout: seed.script_shakeout ?? prev.script_shakeout,
      resource_baseline: seed.resource_baseline ?? prev.resource_baseline,
      groups: reseedScenarioGroups(seed.groups, prev.groups),
      goal_target_average_tps: seed.goal?.target_average_tps,
      goal_min_vusers: seed.goal?.min_vusers,
      goal_max_vusers: seed.goal?.max_vusers,
      goal_p95_latency_ms: seed.goal?.p95_latency_ms,
      goal_max_error_pct: seed.goal?.max_error_pct,
      goal_config: seed.goal ? { ...seed.goal } : undefined,
      runtime_iterations: seed.iterations ?? prev.runtime_iterations,
      runtime_pacing_type: (seed.pacing?.type || (replacesRuntime ? '' : prev.runtime_pacing_type) || '') as ScenarioFormState['runtime_pacing_type'],
      runtime_pacing_delay_ms: seed.pacing ? seed.pacing.delay_ms : (replacesRuntime ? undefined : prev.runtime_pacing_delay_ms),
      runtime_pacing_min_delay_ms: seed.pacing ? seed.pacing.min_delay_ms : (replacesRuntime ? undefined : prev.runtime_pacing_min_delay_ms),
      runtime_pacing_max_delay_ms: seed.pacing ? seed.pacing.max_delay_ms : (replacesRuntime ? undefined : prev.runtime_pacing_max_delay_ms),
      runtime_pacing_interval_ms: seed.pacing ? seed.pacing.interval_ms : (replacesRuntime ? undefined : prev.runtime_pacing_interval_ms),
      runtime_pacing_min_interval_ms: seed.pacing ? seed.pacing.min_interval_ms : (replacesRuntime ? undefined : prev.runtime_pacing_min_interval_ms),
      runtime_pacing_max_interval_ms: seed.pacing ? seed.pacing.max_interval_ms : (replacesRuntime ? undefined : prev.runtime_pacing_max_interval_ms),
      runtime_pacing_tps: seed.pacing ? seed.pacing.tps : (replacesRuntime ? undefined : prev.runtime_pacing_tps),
      runtime_think_time_type: canonicalThinkTimeType(
        seed.think_time?.type || (replacesRuntime ? '' : prev.runtime_think_time_type),
      ) as ScenarioFormState['runtime_think_time_type'],
      runtime_think_time_ms: seed.think_time ? seed.think_time.mean_ms : (replacesRuntime ? undefined : prev.runtime_think_time_ms),
      runtime_think_time_std_ms: seed.think_time ? seed.think_time.std_ms : (replacesRuntime ? undefined : prev.runtime_think_time_std_ms),
      runtime_think_time_min_ms: seed.think_time ? seed.think_time.min_ms : (replacesRuntime ? undefined : prev.runtime_think_time_min_ms),
      runtime_think_time_max_ms: seed.think_time ? seed.think_time.max_ms : (replacesRuntime ? undefined : prev.runtime_think_time_max_ms),
      runtime_think_time_multiplier: seed.think_time ? seed.think_time.multiplier : (replacesRuntime ? undefined : prev.runtime_think_time_multiplier),
      runtime_think_time_limit_ms: seed.think_time ? seed.think_time.limit_ms : (replacesRuntime ? undefined : prev.runtime_think_time_limit_ms),
      runtime_ignore_think_time: seed.think_time ? Boolean(seed.think_time.ignore) : (replacesRuntime ? false : prev.runtime_ignore_think_time),
      runtime_settings: seed.runtime_settings
        ? structuredClone(seed.runtime_settings)
        : prev.runtime_settings,
      runtime_log_level: seed.log_level ?? prev.runtime_log_level,
      runtime_auto_transactions: seed.auto_transactions ?? prev.runtime_auto_transactions,
      continue_on_error: seed.continue_on_error ?? prev.continue_on_error,
      snapshot_on_error: seed.snapshot_on_error ?? prev.snapshot_on_error,
      }
    })
  }

  const selectShapeOption = (shapeId: string) => {
    const preset = getScenarioShapePreset(shapeId)
    const shapeChanged = shapeId !== selectedShapeId
    setSelectedShapeId(shapeId)
    setSelectedCloneScenarioId('')
    setSelectedLoadModelTemplateId('')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('shape')
    setScriptSelectionTargetIndex(null)
    if (preset && shapeChanged) {
      const seed = adaptScenarioShapeDefaults(shapeId, scenarioSeedFromPreset(preset), {
        duration: form.duration,
        groups: form.groups,
        goalTps: form.goal_target_average_tps,
      })
      seedFormFromScenario(seed)
    }
    setStageGroupIndex(0)
    setRunSettingsTab(preset?.recommendedRunTab ?? 'scenario')
    markWizardStepVisited('source')
    setWizardStep('run')
  }

  const applyLoadModelTemplate = (templateId: string) => {
    setSelectedLoadModelTemplateId(templateId)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    const template = loadModelTemplates.find(item => item.id === templateId)
    if (!template) return
    setSelectedShapeId(template.shape_id)
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('shape')
    setScriptSelectionTargetIndex(null)
    setStageGroupIndex(0)
    markWizardStepVisited('source')
    setWizardStep('run')
    const seed = adaptScenarioShapeDefaults(template.shape_id, scenarioSeedFromTemplate(template), {
      duration: form.duration,
      groups: form.groups,
      goalTps: form.goal_target_average_tps,
    })
    seedFormFromScenario(seed)
  }

  const openGoalSizing = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    setGoalSizingOpen(true)
    setScenarioStartChoice('goal')
    setGoalBaselineId('')
    setGoalBaselineQuery('')
    setExpandedGoalBaselineId(null)
    setGoalBaselineReports({})
    setGoalBaselineReportErrors({})
    setGoalBaselineLoadingId('')
    setGoalBaselineGroups([])
    setGoalTargetTps({})
    setGoalSizingResult(null)
    setGoalSizingError('')
    setWizardStep('start')
    setShowForm(true)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    const projectId = activeProjectId
    const scopeGeneration = goalSizingScopeGenerationRef.current
    const requestGeneration = ++goalBaselineSelectionGenerationRef.current
    try {
      const response = await loadAllTestSummaries(api.listTests, { project_id: projectId, summary: true })
      if (
        currentProjectIdRef.current !== projectId
        || goalSizingScopeGenerationRef.current !== scopeGeneration
        || goalBaselineSelectionGenerationRef.current !== requestGeneration
      ) return
      setGoalBaselineTests((response.tests || []).filter(test => test.status === 'COMPLETE'))
    } catch (err: unknown) {
      if (
        currentProjectIdRef.current !== projectId
        || goalSizingScopeGenerationRef.current !== scopeGeneration
        || goalBaselineSelectionGenerationRef.current !== requestGeneration
      ) return
      setGoalSizingError(getErrorMessage(err, 'Unable to load completed runs'))
    }
  }

  const loadGoalBaselineReport = async (testId: string): Promise<RunArchiveReport> => {
    const cached = goalBaselineReports[testId]
    if (cached) return cached
    const projectId = activeProjectId
    const scopeGeneration = goalSizingScopeGenerationRef.current
    const isCurrentScope = () => (
      currentProjectIdRef.current === projectId
      && goalSizingScopeGenerationRef.current === scopeGeneration
    )
    if (isCurrentScope()) {
      setGoalBaselineLoadingId(testId)
      setGoalBaselineReportErrors(prev => ({ ...prev, [testId]: '' }))
    }
    try {
      const report = await api.getTestArchiveReport(testId)
      if (isCurrentScope()) {
        setGoalBaselineReports(prev => ({ ...prev, [testId]: report }))
      }
      return report
    } catch (err: unknown) {
      const message = getErrorMessage(err, 'Unable to load the baseline run report')
      if (isCurrentScope()) {
        setGoalBaselineReportErrors(prev => ({ ...prev, [testId]: message }))
      }
      throw new Error(message)
    } finally {
      if (isCurrentScope()) {
        setGoalBaselineLoadingId(current => current === testId ? '' : current)
      }
    }
  }

  const selectGoalBaseline = async (testId: string) => {
    const projectId = activeProjectId
    const scopeGeneration = goalSizingScopeGenerationRef.current
    const selectionGeneration = ++goalBaselineSelectionGenerationRef.current
    const isCurrentSelection = () => (
      currentProjectIdRef.current === projectId
      && goalSizingScopeGenerationRef.current === scopeGeneration
      && goalBaselineSelectionGenerationRef.current === selectionGeneration
    )
    resetGoalBaselineTableScroll()
    setGoalBaselineId(testId)
    setExpandedGoalBaselineId(testId || null)
    setGoalBaselineGroups([])
    setGoalTargetTps({})
    setGoalSizingResult(null)
    setGoalSizingError('')
    if (!testId) return
    try {
      const report = await loadGoalBaselineReport(testId)
      if (!isCurrentSelection()) return
      const options = baselineGroupOptions(report)
      if (options.length === 0) {
        setGoalSizingError('This run has no response-time data to size from.')
        return
      }
      setGoalBaselineGroups(options)
      setGoalTargetTps(Object.fromEntries(options.map(option => [option.group, ''])))
    } catch (err: unknown) {
      if (!isCurrentSelection()) return
      setGoalSizingError(getErrorMessage(err, 'Unable to load the baseline run report'))
    }
  }

  const toggleGoalBaselineExpanded = async (testId: string) => {
    const projectId = activeProjectId
    const scopeGeneration = goalSizingScopeGenerationRef.current
    const isCurrentScope = () => (
      currentProjectIdRef.current === projectId
      && goalSizingScopeGenerationRef.current === scopeGeneration
    )
    resetGoalBaselineTableScroll()
    if (expandedGoalBaselineId === testId) {
      setExpandedGoalBaselineId(null)
      return
    }
    setExpandedGoalBaselineId(testId)
    setGoalSizingError('')
    try {
      await loadGoalBaselineReport(testId)
    } catch (err: unknown) {
      if (!isCurrentScope()) return
      setGoalSizingError(getErrorMessage(err, 'Unable to load the baseline run report'))
    }
  }

  const runGoalSizing = async () => {
    setGoalSizingError('')
    const groups = goalBaselineGroups
      .map(option => ({ option, tps: Number(goalTargetTps[option.group]) }))
      .filter(entry => Number.isFinite(entry.tps) && entry.tps > 0)
    if (groups.length === 0) {
      setGoalSizingError('Enter a target TPS greater than zero for at least one group.')
      return
    }
    setGoalSizingLoading(true)
    try {
      const result = await api.sizeScenarioFromGoal({
        baseline_test_id: goalBaselineId,
        groups: groups.map(({ option, tps }) => ({
          script_id: option.script_id,
          group: option.basis === 'group' ? option.group : undefined,
          target_tps: tps,
        })),
      })
      setGoalSizingResult(result)
    } catch (err: unknown) {
      setGoalSizingResult(null)
      setGoalSizingError(getErrorMessage(err, 'Unable to size scenario from goal'))
    } finally {
      setGoalSizingLoading(false)
    }
  }

  const applyGoalSizing = () => {
    if (!goalSizingResult) return
    const draft = goalSizingResult.scenario_draft
    const sizedGroups = goalSizedGroups(draft)
    const goalFields = goalSizedGoalFields(draft)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(prev => ({
      ...prev,
      name: draft.name || prev.name,
      duration: draft.duration || prev.duration,
      scenario_mode: goalFields.scenario_mode,
      goal_target_average_tps: goalFields.goal_target_average_tps,
      goal_min_vusers: goalFields.goal_min_vusers,
      goal_max_vusers: goalFields.goal_max_vusers,
      groups: ensureScenarioGroups(
        sizedGroups.map((group, index) => ({
          name: group.name,
          script_id: group.script_id || prev.groups[index]?.script_id || '',
          vusers: group.vusers,
          ramp_up: group.ramp_up,
          ramp_down: '',
          stages: [],
          initialization: 'at_runtime' as const,
          assignment: prev.groups[index]?.assignment,
        })),
      ),
    }))
    setSelectedShapeId('')
    setSelectedLoadModelTemplateId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('goal')
    setStageGroupIndex(0)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setGoalSizingOpen(false)
    setRunSettingsTab('scenario')
    markWizardStepVisited('source')
    setWizardStep('scripts')
  }

  const closeGoalSizing = () => {
    setGoalSizingOpen(false)
    setWizardStep('start')
  }

  const updateScenarioGroup = (index: number, patch: Partial<ScenarioFormGroup>) => {
    setForm(prev => ({
      ...prev,
      groups: ensureScenarioGroups(
        prev.groups.map((group, groupIndex) => groupIndex === index ? { ...group, ...patch } : group),
      ),
    }))
    setLoadModelValidation(null)
  }

  const updateGroupRuntime = (index: number, next: ScenarioRuntimeSettingsDraft | undefined) => {
    setForm(prev => ({
      ...prev,
      groups: prev.groups.map((group, groupIndex) => {
        if (groupIndex !== index) return group
        const runtimeOwnsIterations = next?.run_logic !== undefined || next?.iterations !== undefined
        return {
          ...group,
          ...(runtimeOwnsIterations ? { iterations: undefined } : {}),
          runtimeSettings: next,
        }
      }),
    }))
    setLoadModelValidation(null)
  }

  // Protocols represented in the scenario, so the scenario-defaults editor keeps
  // every section editable that at least one group can use. Before any group is
  // added, every section stays available.
  const scenarioDefaultProtocols = (() => {
    const protocols = formGroups
      .map(group => findScriptById(scripts, group.script_id)?.protocol)
      .filter((protocol): protocol is string => Boolean(protocol && protocol.trim()))
    return protocols.length > 0 ? protocols : ['http', 'browser', 'java_vuser']
  })()

  // The scenario-defaults editor owns the canonical runtime_settings object.
  // The legacy flat mirrors are kept in sync (Scenario Settings tab) or cleared
  // (pacing/think-time, which no longer have flat editors) so
  // runtimeSettingsFromForm composes deterministically from the canonical value.
  const applyScenarioDefaultRuntime = (next: ScenarioRuntimeSettingsDraft | undefined) => {
    setForm(prev => replaceScenarioRuntimeDefaults(prev, next))
    setLoadModelValidation(null)
  }

  // Apply the source group's effective runtime (engine → source script → scenario →
  // source group) to each target. Materializing before protocol filtering prevents
  // an inheriting source from accidentally turning into the target script's defaults.
  const applyRuntimeToGroups = (sourceIndex: number, targetIndexes: number[]) => {
    setForm(prev => ({
      ...prev,
      groups: applyEffectiveRuntimeSettingsToGroups(
        prev.groups,
        scripts,
        engineDefaults,
        runtimeSettingsFromForm(prev, true),
        sourceIndex,
        targetIndexes,
      ),
    }))
    setLoadModelValidation(null)
  }

  const addScenarioGroup = () => {
    if (!canAddScenarioGroup) return
    setForm(prev => {
      if (prev.groups.some(group => !group.script_id.trim()) || duplicateScenarioGroupNameIndexes(prev.groups.map(group => group.name)).size > 0) return prev
      const nextGroups = [...prev.groups, emptyScenarioGroup('', prev.groups.length)]
      return { ...prev, groups: nextGroups }
    })
    setStageGroupIndex(form.groups.length)
    setLoadModelValidation(null)
  }

  const addScenarioGroupFromScript = (script: StoredScript) => {
    if (!canAddScenarioGroup) return
    setForm(prev => {
      if (prev.groups.some(group => !group.script_id.trim()) || duplicateScenarioGroupNameIndexes(prev.groups.map(group => group.name)).size > 0) return prev
      const nextGroups = [
        ...prev.groups,
        {
          ...emptyScenarioGroup(script.id, prev.groups.length),
          name: scenarioGroupNameAfterScriptSelection(
            '',
            prev.groups.length,
            script.name,
            '',
            prev.groups.map(group => group.name),
          ),
          script_id: script.id,
        },
      ]
      return { ...prev, groups: ensureScenarioGroups(nextGroups) }
    })
    setStageGroupIndex(form.groups.length)
    setLoadModelValidation(null)
  }

  const chooseScriptForGroup = (index: number) => {
    setScriptSelectionTargetIndex(index)
    window.setTimeout(() => {
      scriptLibraryRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      scriptLibrarySearchRef.current?.focus()
    }, 0)
  }

  const selectScriptFromLibrary = (script: StoredScript) => {
    if (scriptSelectionTargetIndex !== null && formGroups[scriptSelectionTargetIndex]) {
      const targetIndex = scriptSelectionTargetIndex
      const targetGroup = formGroups[targetIndex]
      const previousScriptName = findScriptById(scripts, targetGroup.script_id)?.name || ''
      updateScenarioGroup(targetIndex, {
        script_id: script.id,
        name: scenarioGroupNameAfterScriptSelection(
          targetGroup.name,
          targetIndex,
          script.name,
          previousScriptName,
          formGroups.map(group => group.name),
        ),
        ...(targetGroup.role === 'observer' && script.protocol !== 'browser'
          ? { role: 'load' as const }
          : {}),
      })
      setScriptSelectionTargetIndex(null)
      window.setTimeout(() => {
        scriptGroupRefs.current[targetIndex]?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
      }, 0)
      return
    }
    addScenarioGroupFromScript(script)
  }

  const removeScenarioGroup = (index: number) => {
    setForm(prev => {
      const nextGroups = prev.groups
        .filter((_, groupIndex) => groupIndex !== index)
        .map((group, nextIndex) => {
          const previousIndex = nextIndex >= index ? nextIndex + 1 : nextIndex
          return group.name.trim() === `Group ${previousIndex + 1}`
            ? { ...group, name: `Group ${nextIndex + 1}` }
            : group
        })
      return { ...prev, groups: ensureScenarioGroups(nextGroups) }
    })
    setScriptSelectionTargetIndex(current => {
      if (current === null) return null
      if (current === index) return null
      return current > index ? current - 1 : current
    })
    setStageGroupIndex(current => Math.max(0, Math.min(current, form.groups.length - 2)))
    setLoadModelValidation(null)
  }

  const enableDefaultStages = () => {
    const strategy = shapeStageStrategyFor(selectedShapeId)
    const durationSeconds = parseDurationToSeconds(form.duration) || 300
    const adapted = selectedShapeId
      ? adaptScenarioShapeDefaults(selectedShapeId, scenarioSeedFromForm(form), {
        duration: form.duration,
        groups: form.groups,
        goalTps: form.goal_target_average_tps,
      })
      : undefined
    const adaptedGroup = adapted?.groups?.[stageGroupIndex]
    const shapeStages = strategy !== 'none'
      ? buildShapeStages(strategy, {
        peakVusers: activeStageGroup.vusers,
        durationSeconds,
        turbulenceTps: adapted?.goal?.target_average_tps || form.goal_target_average_tps,
      })
      : []
    const nextStages = shapeStages.length > 0
      ? shapeStages
      : createDefaultStages(activeStageGroup.vusers, activeStageGroup.ramp_up || '2m', form.duration || '5m', activeStageGroup.ramp_down || '1m')
    const nextPeak = maxStageVUsers(nextStages) || activeStageGroup.vusers
    const nextRampUp = strategy === 'none' ? '' : (adaptedGroup?.ramp_up || '')
    const nextRampDown = strategy === 'none' ? '' : (adaptedGroup?.ramp_down || '')
    setForm(prev => ({
      ...prev,
      ...(adapted?.scenario_mode ? { scenario_mode: adapted.scenario_mode } : {}),
      goal_target_average_tps: adapted?.goal?.target_average_tps ?? prev.goal_target_average_tps,
      goal_min_vusers: adapted?.goal?.min_vusers ?? prev.goal_min_vusers,
      goal_max_vusers: adapted?.goal?.max_vusers ?? prev.goal_max_vusers,
      goal_p95_latency_ms: adapted?.goal?.p95_latency_ms ?? prev.goal_p95_latency_ms,
      goal_max_error_pct: adapted?.goal?.max_error_pct ?? prev.goal_max_error_pct,
      runtime_pacing_type: (adapted?.pacing?.type || prev.runtime_pacing_type || '') as ScenarioFormState['runtime_pacing_type'],
      runtime_pacing_delay_ms: adapted?.pacing ? adapted.pacing.delay_ms : prev.runtime_pacing_delay_ms,
      runtime_pacing_min_delay_ms: adapted?.pacing ? adapted.pacing.min_delay_ms : prev.runtime_pacing_min_delay_ms,
      runtime_pacing_max_delay_ms: adapted?.pacing ? adapted.pacing.max_delay_ms : prev.runtime_pacing_max_delay_ms,
      runtime_pacing_interval_ms: adapted?.pacing ? adapted.pacing.interval_ms : prev.runtime_pacing_interval_ms,
      runtime_pacing_min_interval_ms: adapted?.pacing ? adapted.pacing.min_interval_ms : prev.runtime_pacing_min_interval_ms,
      runtime_pacing_max_interval_ms: adapted?.pacing ? adapted.pacing.max_interval_ms : prev.runtime_pacing_max_interval_ms,
      runtime_pacing_tps: adapted?.pacing ? adapted.pacing.tps : prev.runtime_pacing_tps,
      groups: ensureScenarioGroups(prev.groups.map((group, groupIndex) => groupIndex === stageGroupIndex ? {
        ...group,
        stages: nextStages,
        vusers: nextPeak,
        ramp_up: nextRampUp,
        ramp_down: nextRampDown,
      } : group)),
    }))
    setLoadModelValidation(null)
  }

  const disableStages = () => {
    const simpleRamp = simpleRampFromStages(activeStageGroup)
    updateScenarioGroup(stageGroupIndex, { ...simpleRamp, stages: [] })
    setLoadModelValidation(null)
    setLoadModelValidationError('')
  }

  const updateStage = (index: number, patch: Partial<LoadStageDraft>) => {
    const stages = activeStageGroup.stages.map((stage, stageIndex) => stageIndex === index ? { ...stage, ...patch } : stage)
    const peak = maxStageVUsers(stages)
    updateScenarioGroup(stageGroupIndex, { stages, vusers: peak || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const addStage = () => {
    const stages = [...activeStageGroup.stages, { name: `Stage ${activeStageGroup.stages.length + 1}`, type: 'hold' as LoadStageType, duration: '1m', target_vusers: Number(activeStageGroup.vusers) || 10 }]
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const duplicateStage = (index: number) => {
    const source = activeStageGroup.stages[index]
    if (!source) return
    const stages = [
      ...activeStageGroup.stages.slice(0, index + 1),
      { ...source, name: source.name ? `${source.name} Copy` : `Stage ${index + 2}` },
      ...activeStageGroup.stages.slice(index + 1),
    ]
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const removeStage = (index: number) => {
    const stages = activeStageGroup.stages.filter((_, stageIndex) => stageIndex !== index)
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const validateLoadModel = async () => {
    if (form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setLoadModelValidationError('Select a script for every group before validating the load model.')
      return
    }
    if (!isValidDurationInput(form.duration)) {
      setLoadModelValidationError('Set a positive scenario duration before validating the load model.')
      return
    }
    if (scenarioGroupNameError) {
      setLoadModelValidationError(scenarioGroupNameError)
      setWizardStep('scripts')
      return
    }
    if (blockingStageTimelineMessage) {
      setLoadModelValidationError(blockingStageTimelineMessage)
      return
    }
    if (observerEnvelopeError) {
      setLoadModelValidationError(observerEnvelopeError)
      return
    }
    if (aggregateEnvelopeError) {
      setLoadModelValidationError(aggregateEnvelopeError)
      return
    }
    if (assignmentValidationError) {
      setLoadModelValidationError(assignmentValidationError)
      return
    }
    setLoadModelValidationLoading(true)
    setLoadModelValidationError('')
    try {
      const report = await api.validateScenarioLoadModel(buildScenarioDraftPayload())
      setLoadModelValidation(report)
      setWizardStep('review')
    } catch (err: unknown) {
      setLoadModelValidation(null)
      setLoadModelValidationError(getErrorMessage(err, 'Unable to validate load model'))
    } finally {
      setLoadModelValidationLoading(false)
    }
  }

  const persistScenario = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return null
    }
    if (!form.name.trim()) {
      setError('Name the scenario before saving.')
      setWizardStep('review')
      return null
    }
    if (form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setError('At least one script group with a selected script is required.')
      return null
    }
    if (scenarioGroupNameError) {
      setError(scenarioGroupNameError)
      setWizardStep('scripts')
      return null
    }
    if (!isValidDurationInput(form.duration)) {
      setError('Scenario duration is required.')
      return null
    }
    if (blockingStageTimelineMessage) {
      setError(blockingStageTimelineMessage)
      return null
    }
    if (observerEnvelopeError) {
      setError(observerEnvelopeError)
      return null
    }
    if (aggregateEnvelopeError) {
      setError(aggregateEnvelopeError)
      return null
    }
    if (assignmentValidationError) {
      setError(assignmentValidationError)
      return null
    }
    if (form.scenario_mode === 'goal_oriented') {
      const goalConfig = goalConfigFromForm(form)
      if (!hasGoalModeConfiguration(goalConfig)) {
        setError('Goal-oriented scenarios require at least one goal or guardrail setting')
        return null
      }
      const goalConfigError = validateGoalConfig(goalConfig)
      if (goalConfigError) {
        setError(goalConfigError)
        return null
      }
    }
    const payload = buildScenarioDraftPayload()
    try {
      const saved = editingId ? await api.updateScenario(editingId, payload) : await api.createScenario(payload)
      return saved
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to save scenario'))
      return null
    }
  }

  const resetScenarioBuilder = () => {
    clearScenarioBuilderDraft()
    setEditingId(null)
    editingBaselineFormRef.current = ''
    setForm(emptyScenarioForm())
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setScenarioCloneQuery('')
    setRunSettingsTab('scenario')
    setWizardStep('start')
    setMaxVisitedWizardIndex(0)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(false)
  }

  const saveScenario = async () => {
    const saved = await persistScenario()
    if (saved) {
      resetScenarioBuilder()
      refresh()
    }
  }

  const saveScenarioAndCreateTest = async () => {
    const saved = await persistScenario()
    if (!saved?.id) {
      setError('Scenario was saved, but the response did not include a scenario id.')
      return
    }
    resetScenarioBuilder()
    onCreateTestFromScenario?.(saved.id)
    void refresh()
  }

  const deleteScenario = async (id: string) => {
    setConfirmDeleteScenarioId(null)
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot delete them.')
      return
    }
    const scopeProjectId = activeProjectId
    setDeletingScenarioIds(current => new Set(current).add(id))
    try {
      await api.deleteScenario(id)
      if (currentProjectIdRef.current !== scopeProjectId) return
      setScenarios(current => current.filter(scenario => scenario.id !== id))
      setSelectedScenarioIds(current => {
        const next = new Set(current)
        next.delete(id)
        return next
      })
      await refresh()
    } catch (err: unknown) {
      if (currentProjectIdRef.current === scopeProjectId) setError(getErrorMessage(err, 'Unable to delete scenario'))
    } finally {
      if (currentProjectIdRef.current === scopeProjectId) {
        setDeletingScenarioIds(current => {
          const next = new Set(current)
          next.delete(id)
          return next
        })
      }
    }
  }

  const toggleScenarioSelection = (id: string) => {
    setSelectedScenarioIds(current => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const toggleAllVisibleScenarios = (checked: boolean) => {
    setSelectedScenarioIds(current => {
      const next = new Set(current)
      visibleScenarioIds.forEach(id => checked ? next.add(id) : next.delete(id))
      return next
    })
  }

  const deleteSelectedScenarios = async () => {
    const ids = selectedVisibleScenarioIds.filter(id => !deletingScenarioIds.has(id))
    if (!canManageTests || ids.length === 0 || !confirm(`Delete ${ids.length} selected scenario${ids.length === 1 ? '' : 's'}? Scenarios with linked results cannot be deleted.`)) return
    const scopeProjectId = activeProjectId
    setDeletingScenarioIds(current => new Set([...current, ...ids]))
    setError('')
    const outcomes = await boundedAllSettled(ids, id => api.deleteScenario(id))
    if (currentProjectIdRef.current !== scopeProjectId) return
    const deletedIds = ids.filter((_, index) => outcomes[index]?.status === 'fulfilled')
    const failed = outcomes.flatMap((outcome, index) => outcome.status === 'rejected'
      ? [{ id: ids[index], reason: getErrorMessage(outcome.reason, 'delete failed') }]
      : [])
    const deleted = new Set(deletedIds)
    setScenarios(current => current.filter(scenario => !deleted.has(scenario.id)))
    setSelectedScenarioIds(current => {
      const next = new Set(current)
      deletedIds.forEach(id => next.delete(id))
      return next
    })
    setDeletingScenarioIds(current => {
      const next = new Set(current)
      ids.forEach(id => next.delete(id))
      return next
    })
    if (failed.length > 0) {
      const details = failed.slice(0, 3).map(item => `${item.id}: ${item.reason}`).join('; ')
      const suffix = failed.length > 3 ? `; ${failed.length - 3} more failed` : ''
      setError(`Deleted ${deletedIds.length} of ${ids.length} selected scenarios. ${details}${suffix}`)
    }
    await refresh()
  }

  const assignmentRecommendation = draftEstimate?.assignment_recommendation
  const availableTargets = assignmentRecommendation?.available_targets || []
  const recommendedTargets = assignmentRecommendation?.recommended_targets || []
  const availableLabels = assignmentRecommendation?.available_labels || {}
  const rootAssignment = assignmentFromForm(form)

  const updateRootAssignment = (next?: GeneratorAssignmentSettings) => {
    const assignment = cloneAssignment(next) || { mode: 'auto' as const }
    setForm(previous => ({
      ...previous,
      assignment,
      assignment_mode: assignment.mode || 'auto',
      assignment_targets: [...(assignment.selected_targets || [])],
    }))
  }

  const currentWizardIndex = scenarioWizardSteps.findIndex(step => step.id === wizardStep)
  const scenarioBuilderDirty = showForm && (
    editingId
      ? Boolean(editingBaselineFormRef.current) && JSON.stringify(form) !== editingBaselineFormRef.current
      : hasScenarioBuilderProgress({ form, scenarioStartChoice, maxVisitedWizardIndex })
  )
  persistScenarioRef.current = persistScenario
  resetScenarioBuilderRef.current = resetScenarioBuilder
  refreshRef.current = refresh
  isScenarioBuilderDirtyRef.current = () => scenarioBuilderDirty
  const scenarioBuilderGuard = useMemo<NavigationGuard>(() => ({
    id: 'scenario-builder',
    label: 'Scenario Builder',
    isDirty: () => isScenarioBuilderDirtyRef.current(),
    save: async () => {
      const saved = await persistScenarioRef.current()
      if (!saved) return false
      resetScenarioBuilderRef.current()
      void refreshRef.current()
      return true
    },
    discard: () => {
      clearScenarioBuilderDraft()
      resetScenarioBuilderRef.current()
    },
  }), [])
  useNavigationGuard(showForm ? scenarioBuilderGuard : null, scenarioBuilderDirty)

  useEffect(() => {
    if (currentWizardIndex >= 0) {
      setMaxVisitedWizardIndex(current => Math.max(current, currentWizardIndex))
    }
  }, [currentWizardIndex])

  useEffect(() => {
    const node = goalSizingResultRef.current
    if (!goalSizingOpen || !goalSizingResult || !node || typeof node.scrollIntoView !== 'function') return
    window.requestAnimationFrame(() => {
      node.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }, [goalSizingOpen, goalSizingResult])

  useEffect(() => {
    if (!showForm) return
    let cancelled = false
    api.getSystemInventory()
      .then(response => {
        if (!cancelled) setGeneratorInventory(response.generators || [])
      })
      .catch(() => {
        if (!cancelled) setGeneratorInventory([])
      })
    return () => {
      cancelled = true
    }
  }, [api, showForm])

  const dataPoolLabel = form.data_pool_id
    ? dataPools.find(pool => pool.id === form.data_pool_id)?.name || 'Data pool configured'
    : 'No data pool'
  const shapeLabel = selectedTemplate?.name || selectedShape?.support.name || (scenarioSourceMode === 'existing' ? 'Modeled from scenario' : 'Manual shape')
  const validationLabel = loadModelValidation ? formatLoadModelDimension(loadModelValidation.status) : 'Not validated'
  const requiredDraftReady = Boolean(form.name.trim() && isValidDurationInput(form.duration) && formGroups.every(group => group.script_id) && !scenarioGroupNameError)
  const sourceStepComplete = scenarioStartChoice === 'goal'
    ? Boolean(goalSizingResult)
    : scenarioSourceMode === 'existing'
      ? Boolean(selectedCloneScenarioId || editingId)
      : Boolean(selectedShapeId || selectedLoadModelTemplateId)
  const wizardStepCompletion: Record<ScenarioWizardStep, boolean> = {
    start: Boolean(scenarioStartChoice),
    source: sourceStepComplete,
    scripts: Boolean(form.name.trim() && formGroups.length > 0 && formGroups.every(group => group.script_id) && !scenarioGroupNameError),
    run: Boolean(isValidDurationInput(form.duration) && !blockingStageTimelineMessage),
    data: true,
    review: Boolean(requiredDraftReady && !blockingStageTimelineMessage),
  }
  // Only offer "Start Over" for a new build (not an edit) once there's progress to discard.
  const builderHasProgress = !editingId && hasScenarioBuilderProgress({ form, scenarioStartChoice, maxVisitedWizardIndex })

  const renderScenarioRunsContent = (scenario: StoredScenario) => {
    const runs = scenarioRuns[scenario.id] || []
    const loading = loadingRunsFor === scenario.id
    return (
      <div className="scenario-linked-runs-content">
        <div className="scenario-linked-runs-header">
          <div>
            <strong>Past Scenario Runs</strong>
            <span>{loading ? 'Loading runs...' : `${runs.length} linked run${runs.length === 1 ? '' : 's'}`}</span>
          </div>
          <span className="scenario-table-assignment-detail">{scenario.name}</span>
        </div>
        {runs.length === 0 ? (
          <div className="cell-muted">{loading ? 'Loading runs...' : 'No runs for this scenario yet.'}</div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <SortHeader label="Run Name" field="name" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Status" field="status" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Release ID" field="release_id" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Started" field="start_time" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Duration" field="duration" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {getSortedInnerRuns(runs).map(run => (
                  <tr key={run.id} className="clickable-row" onClick={(event) => { event.stopPropagation(); onSelectTest(run.id) }}>
                    <td className="cell-name">
                      <div>{run.name}</div>
                      <div className="script-table-meta">
                        <ReferenceKeyBadge value={run.ref_key} />
                      </div>
                    </td>
                    <td>{run.status}</td>
                    <td className="cell-mono">{run.release_id || '—'}</td>
                    <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                    <td className="cell-muted">{run.duration}</td>
                    <td style={{ textAlign: 'right' }}>
                      <div className="scenario-linked-run-actions">
                      <button className="btn btn-sm btn-secondary" onClick={(event) => { event.stopPropagation(); onSelectTest(run.id) }}>Open</button>
                      {onOpenCommandCenter && (
                        <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onOpenCommandCenter(run.id) }}>Run Dashboard</button>
                      )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    )
  }

  const renderSourceScenarioRunsPanel = () => {
    if (wizardStep !== 'source' || scenarioSourceMode !== 'existing' || !selectedCloneScenario) return null
    return (
      <section className="scenario-source-runs-panel" aria-live="polite">
        {renderScenarioRunsContent(selectedCloneScenario)}
      </section>
    )
  }

  const renderGoalSizingPanel = () => {
    const previewTotal = goalBaselineGroups.reduce((sum, option) => {
      const tps = Number(goalTargetTps[option.group])
      return sum + (Number.isFinite(tps) && tps > 0 ? littlesLawVUsers(tps, option.baseline_art_ms) : 0)
    }, 0)
    const baselineSummary = goalBaselineQuery.trim()
      ? `${visibleGoalBaselineTests.length} matching completed run${visibleGoalBaselineTests.length === 1 ? '' : 's'}`
      : `${goalBaselineTests.length} completed run${goalBaselineTests.length === 1 ? '' : 's'} available`
    const selectedBaseline = goalBaselineTests.find(test => test.id === goalBaselineId)
    return (
      <section className="scenario-wizard-panel" data-testid="goal-sizing-panel">
        <div className="scenario-wizard-panel-header">
          <div>
            <h4>Build From Goal</h4>
            <p>Pick a completed baseline run and a target throughput per group. APEX Load sizes VUsers with Little&apos;s Law (VUsers = target TPS × response time), then the goal controller converges at run time.</p>
          </div>
          <button type="button" className="mode-chip" onClick={closeGoalSizing}>Back</button>
        </div>

        <div className="scenario-subpanel goal-sizing-setup" data-testid="goal-sizing-modeling-options">
          <div className="scenario-assignment-header">
            <div>
              <h4>Modeling Options</h4>
              <p>Set the target throughput envelope for the selected baseline run before sizing the scenario draft.</p>
            </div>
            <span className="scenario-table-assignment-detail">
              {selectedBaseline ? `Baseline ${selectedBaseline.id}` : 'Select a baseline run below'}
            </span>
          </div>

          {goalBaselineGroups.length > 0 ? (
            <div className="goal-sizing-groups">
              <table className="settings-table goal-sizing-table">
                <colgroup>
                  <col className="goal-sizing-col-group" />
                  <col className="goal-sizing-col-art" />
                  <col className="goal-sizing-col-target" />
                  <col className="goal-sizing-col-estimate" />
                </colgroup>
                <thead>
                  <tr><th>Group</th><th>Baseline ART</th><th>Target TPS</th><th>Est. VUsers</th></tr>
                </thead>
                <tbody>
                  {goalBaselineGroups.map(option => {
                    const tps = Number(goalTargetTps[option.group])
                    const estimate = Number.isFinite(tps) && tps > 0 ? littlesLawVUsers(tps, option.baseline_art_ms) : 0
                    return (
                      <tr key={option.group}>
                        <td>{option.group}</td>
                        <td>{Math.round(option.baseline_art_ms)} ms</td>
                        <td>
                          <input
                            className="settings-input"
                            type="number"
                            min="0"
                            step="1"
                            aria-label={`Target TPS for ${option.group}`}
                            value={goalTargetTps[option.group] ?? ''}
                            onChange={event => setGoalTargetTps(prev => ({ ...prev, [option.group]: event.target.value }))}
                          />
                        </td>
                        <td>{estimate > 0 ? estimate : '—'}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
              {previewTotal > 0 && <p className="goal-sizing-preview">Estimated total: <strong>{previewTotal} VUsers</strong></p>}
              <button type="button" className="btn btn-primary" onClick={() => void runGoalSizing()} disabled={goalSizingLoading}>
                {goalSizingLoading ? 'Sizing…' : 'Size scenario'}
              </button>
            </div>
          ) : (
            <p className="cell-muted goal-sizing-empty">Select a completed run from the table below to populate baseline ART and target TPS inputs.</p>
          )}

          {goalSizingError && <p className="form-error" role="alert">{goalSizingError}</p>}

          {goalSizingResult && (
            <div className="goal-sizing-result" data-testid="goal-sizing-result" ref={goalSizingResultRef}>
              <div className="goal-sizing-result-header">
                <div>
                  <h4>Recommended Size</h4>
                  <p>{formatGoalSizingSummary(goalSizingResult)}</p>
                </div>
                <div className="goal-sizing-result-header-meta">
                  <span className="scenario-table-assignment-detail">Baseline {goalSizingResult.baseline_test_id}</span>
                  <button type="button" className="btn btn-primary" onClick={applyGoalSizing}>Apply to scenario</button>
                </div>
              </div>
              <div className="goal-sizing-result-summary" aria-label="Sizing result summary">
                <div>
                  <span>Total VUsers</span>
                  <strong>{formatOptionalNumber(goalSizingResult.total_vusers, 0)}</strong>
                </div>
                <div>
                  <span>Target TPS</span>
                  <strong>{formatOptionalTps(goalSizingResult.total_target_tps)}</strong>
                </div>
                <div>
                  <span>Draft Duration</span>
                  <strong>{goalSizingResult.scenario_draft.duration || '—'}</strong>
                </div>
                <div>
                  <span>Groups</span>
                  <strong>{goalSizingResult.groups.length}</strong>
                </div>
              </div>
              {goalSizingResult.load_model_validation?.capacity_advisory && (
                <div className="goal-sizing-capacity">
                  <strong>Capacity</strong>
                  <span>{goalSizingResult.load_model_validation.capacity_advisory.recommended_generators} generator(s) recommended.</span>
                </div>
              )}
              <div className="goal-sizing-result-table-wrap">
                <table className="goal-sizing-result-table">
                  <thead>
                    <tr>
                      <th>Group</th>
                      <th>Target TPS</th>
                      <th>Baseline ART</th>
                      <th>Computed VUsers</th>
                      <th>Basis</th>
                    </tr>
                  </thead>
                  <tbody>
                    {goalSizingResult.groups.map(group => (
                      <tr key={group.group}>
                        <td>
                          <strong>{group.group}</strong>
                          {group.script_id && <span>{group.script_id}</span>}
                        </td>
                        <td>{formatOptionalTps(group.target_tps)}</td>
                        <td>{formatOptionalMs(group.baseline_art_ms)}</td>
                        <td>{formatOptionalNumber(group.computed_vusers, 0)}</td>
                        <td>{formatGoalSizingBasis(group.basis)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>

        <div className="goal-baseline-picker" aria-label="Completed baseline runs">
          <div className="goal-baseline-toolbar">
            <label className="library-search goal-baseline-search">
              <span>Filter completed runs</span>
              <input
                type="search"
                value={goalBaselineQuery}
                onChange={event => {
                  resetGoalBaselineTableScroll()
                  setGoalBaselineQuery(event.target.value)
                }}
                placeholder="Run name, run ID, release, script..."
              />
            </label>
            <span className="scenario-table-assignment-detail">{baselineSummary}</span>
          </div>
          <div className="data-table goal-baseline-table" ref={goalBaselineTableRef}>
            <table>
              <thead>
                <tr>
                  <th>Scripts</th>
                  <SortHeader label="Run" field="name" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} />
                  <SortHeader label="Run ID" field="id" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} />
                  <SortHeader label="Run Date/Time" field="started_at" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} />
                  <SortHeader label="Overall Duration" field="duration" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} />
                  <SortHeader label="VUsers" field="vusers" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} numeric />
                  <SortHeader label="Peak TPS" field="peak_tps" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} numeric />
                  <SortHeader label="ART" field="art" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} numeric />
                  <SortHeader label="Error Rate" field="error_rate" sort={{ field: goalBaselineSortField, direction: goalBaselineSortDirection }} onSort={handleGoalBaselineSort} numeric />
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {visibleGoalBaselineTests.map(test => {
                  const report = goalBaselineReports[test.id]
                  const selected = goalBaselineId === test.id
                  const expanded = expandedGoalBaselineId === test.id
                  const loadingReport = goalBaselineLoadingId === test.id
                  const reportError = goalBaselineReportErrors[test.id]
                  const scriptRows = goalBaselineScriptRows(test, report)
                  return (
                    <Fragment key={test.id}>
                      <tr
                        className={`clickable-row ${selected ? 'selected' : ''}`}
                        aria-selected={selected}
                        onClick={() => void selectGoalBaseline(test.id)}
                      >
                        <td>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            aria-expanded={expanded}
                            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${test.name} scripts`}
                            onClick={(event) => {
                              event.stopPropagation()
                              void toggleGoalBaselineExpanded(test.id)
                            }}
                          >
                            {expanded ? 'Hide' : 'Scripts'}
                          </button>
                        </td>
                        <td className="cell-name">
                          <div>{test.name}</div>
                          <div className="script-table-meta">
                            <ReferenceKeyBadge value={test.ref_key} />
                            <span>{test.status}</span>
                            {test.release_id && <span>Release {test.release_id}</span>}
                          </div>
                        </td>
                        <td className="cell-mono">{test.id}</td>
                        <td className="cell-muted scenario-run-time-cell">{formatDateTime(latestRunTimestamp(test))}</td>
                        <td className="cell-muted">{test.duration || '—'}</td>
                        <td>{formatOptionalNumber(goalBaselineVusers(test, report), 0)}</td>
                        <td>{formatOptionalTps(goalBaselinePeakTps(test, report))}</td>
                        <td>{formatOptionalMs(goalBaselineArt(test, report))}</td>
                        <td>{formatOptionalPercent(goalBaselineErrorRate(test, report))}</td>
                        <td style={{ textAlign: 'right' }}>
                          <button
                            type="button"
                            className={`btn btn-sm ${selected ? 'btn-primary' : 'btn-secondary'}`}
                            aria-label={`${selected ? 'Selected' : 'Select'} ${test.name} as goal baseline`}
                            disabled={loadingReport && selected}
                            onClick={(event) => {
                              event.stopPropagation()
                              void selectGoalBaseline(test.id)
                            }}
                          >
                            {loadingReport && selected ? 'Loading...' : selected ? 'Selected' : 'Select'}
                          </button>
                        </td>
                      </tr>
                      {expanded && (
                        <tr className="no-hover goal-baseline-expanded-row">
                          <td colSpan={10}>
                            {loadingReport && !report ? (
                              <div className="cell-muted">Loading script details...</div>
                            ) : reportError ? (
                              <div className="form-error" role="alert">{reportError}</div>
                            ) : scriptRows.length === 0 ? (
                              <div className="cell-muted">No script groups were recorded for this run.</div>
                            ) : (
                              <div className="goal-baseline-script-panel">
                                <div className="scenario-linked-runs-header">
                                  <div>
                                    <strong>Scripts in {test.name}</strong>
                                    <span>{scriptRows.length} script group{scriptRows.length === 1 ? '' : 's'} · Run ID {test.id}</span>
                                  </div>
                                  <span className="scenario-table-assignment-detail">Overall {test.duration || '—'} · Started {formatDateTime(latestRunTimestamp(test))}</span>
                                </div>
                                <table className="goal-baseline-script-table">
                                  <thead>
                                    <tr>
                                      <th>Script</th>
                                      <th>VUsers</th>
                                      <th>Peak TPS</th>
                                      <th>ART</th>
                                      <th>Error Rate</th>
                                      <th>Duration</th>
                                    </tr>
                                  </thead>
                                  <tbody>
                                    {scriptRows.map(row => (
                                      <tr key={row.key}>
                                        <td className="cell-name">
                                          <div>{row.scriptName}</div>
                                          <div className="script-table-meta">
                                            <span>{row.group}</span>
                                            {row.scriptId && <span className="cell-mono">{row.scriptId}</span>}
                                            {row.protocol && <span>{row.protocol}</span>}
                                          </div>
                                        </td>
                                        <td>{formatOptionalNumber(row.vusers, 0)}</td>
                                        <td>{formatOptionalTps(row.metrics?.peak_tps)}</td>
                                        <td>{formatOptionalMs(row.metrics?.avg_response_ms)}</td>
                                        <td>{formatOptionalPercent(row.metrics?.error_pct)}</td>
                                        <td>{test.duration || '—'}</td>
                                      </tr>
                                    ))}
                                  </tbody>
                                </table>
                              </div>
                            )}
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  )
                })}
                {visibleGoalBaselineTests.length === 0 && (
                  <tr>
                    <td colSpan={10} className="cell-muted">
                      {goalBaselineTests.length === 0 ? 'No completed runs are available for goal sizing.' : 'No completed runs match these filters.'}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </section>
    )
  }

  const renderShapeGuidance = () => {
    if (!selectedShape || !selectedShapePreset) return null
    const showMultiGroupHint = selectedShapePreset.suggestMultipleGroups && formGroups.length > 1
    return (
      <div className="scenario-shape-guidance" role="note">
        <div className="scenario-shape-guidance-header">
          <span className="scenario-shape-guidance-name">{selectedShape.support.name}</span>
          <span className="scenario-shape-guidance-category">{selectedShape.support.category}</span>
        </div>
        <p className="scenario-shape-guidance-copy">{selectedShapePreset.guidance}</p>
        {showMultiGroupHint && (
          <p className="scenario-shape-guidance-copy secondary">This shape seeds {formGroups.length} groups — assign a script to each.</p>
        )}
        {selectedShape.support.notes && (
          <p className="scenario-shape-guidance-copy secondary">{selectedShape.support.notes}</p>
        )}
      </div>
    )
  }

  const renderWizardStep = () => {
    if (goalSizingOpen) {
      return renderGoalSizingPanel()
    }
    if (wizardStep === 'start') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Start With A Model</h4>
            <p>Choose whether this scenario should inherit a known workload or start from a supported load shape.</p>
          </div>
          <div className="scenario-source-grid">
            <button type="button" className={`scenario-source-card ${scenarioSourceMode === 'existing' ? 'selected' : ''}`} onClick={() => selectScenarioSourceMode('existing')}>
              <span>Model from Existing Scenario</span>
              <strong>Clone Draft</strong>
              <small>Search the scenario library, copy its scripts, run settings, data pool, and generator defaults, then save as a new scenario.</small>
            </button>
            <button type="button" className={`scenario-source-card ${scenarioSourceMode === 'shape' ? 'selected' : ''}`} onClick={() => selectScenarioSourceMode('shape')}>
              <span>Build from Workload Shape</span>
              <strong>Unified Catalog</strong>
              <small>Select a supported workload model and optionally apply one of its starter templates.</small>
            </button>
            {trafficProfileShapeEnabled && <button type="button" className={`scenario-source-card ${scenarioSourceMode === 'traffic_profile' ? 'selected' : ''}`} onClick={() => selectScenarioSourceMode('traffic_profile')}>
              <span>Production Traffic Profile</span>
              <strong>CSV, XLSX, Prometheus, OpenSearch</strong>
              <small>Preserve the production series at full resolution and replay it as adaptive TPS or direct VUser targets.</small>
            </button>}
            <button type="button" className="scenario-source-card" onClick={() => void openGoalSizing()}>
              <span>Build from Goal</span>
              <strong>Little&apos;s Law Sizing</strong>
              <small>Pick a completed baseline run and a target TPS per group; VUsers are sized automatically and tuned by the goal controller.</small>
            </button>
          </div>
        </section>
      )
    }

    if (wizardStep === 'source') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>{scenarioSourceMode === 'existing' ? 'Choose Scenario To Model' : scenarioSourceMode === 'traffic_profile' ? 'Build From Production Traffic' : 'Choose Workload Shape'}</h4>
              <p>{scenarioSourceMode === 'existing' ? 'The selected scenario becomes an editable copy; the original remains unchanged.' : scenarioSourceMode === 'traffic_profile' ? 'Analyze a file or production query, map series to groups, and preview the exact runtime targets before applying.' : 'Supported workload models and starter templates are combined into one actionable catalog.'}</p>
            </div>
            <div className="scenario-source-toggle">
              <button type="button" className={`mode-chip ${scenarioSourceMode === 'shape' ? 'active' : ''}`} onClick={() => selectScenarioSourceMode('shape')}>Shapes</button>
              <button type="button" className={`mode-chip ${scenarioSourceMode === 'existing' ? 'active' : ''}`} onClick={() => selectScenarioSourceMode('existing')}>Existing</button>
              {trafficProfileShapeEnabled && <button type="button" className={`mode-chip ${scenarioSourceMode === 'traffic_profile' ? 'active' : ''}`} onClick={() => selectScenarioSourceMode('traffic_profile')}>Production profile</button>}
            </div>
          </div>
          {scenarioSourceMode === 'traffic_profile' ? (
            <TrafficProfileSourcePanel
              projectId={activeProjectId}
              groupNames={form.groups.map((group, index) => group.name.trim() || findScriptById(scripts, group.script_id)?.name || `Group ${index + 1}`)}
              scenarioDraft={buildScenarioDraftPayload()}
              existingProfile={form.traffic_profile}
              onApply={(result: TrafficProfileDeriveResponse) => {
                const managed = new Map(result.traffic_profile.mappings.map(mapping => [mapping.group_name, mapping]))
                const goal = result.scenario_draft.goal_config
                setForm(previous => ({
                  ...previous,
                  duration: result.traffic_profile.target_duration,
                  scenario_mode: result.traffic_profile.runtime_target === 'vusers' ? 'vuser_driven' : 'goal_oriented',
                  traffic_profile: result.traffic_profile,
                  goal_config: goal ? structuredClone(goal) : undefined,
                  goal_target_average_tps: goal?.target_average_tps,
                  goal_min_vusers: goal?.min_vusers,
                  goal_max_vusers: goal?.max_vusers,
                  goal_p95_latency_ms: goal?.p95_latency_ms,
                  goal_max_error_pct: goal?.max_error_pct,
                  groups: previous.groups.map((group, index) => {
                    const name = group.name.trim() || findScriptById(scripts, group.script_id)?.name || `Group ${index + 1}`
                    const mapping = managed.get(name)
                    return mapping ? { ...group, name, stages: [], ramp_up: '', ramp_down: '', start_offset: '', vusers: result.traffic_profile.runtime_target === 'vusers' ? mapping.peak_target : group.vusers } : group
                  }),
                }))
                setSelectedShapeId(PRODUCTION_REPLAY_SHAPE_ID)
                setLoadModelValidation(result.load_model_validation || null)
              }}
            />
          ) : scenarioSourceMode === 'existing' ? (
            <>
              <label className="library-search scenario-clone-search">
                <span>Search existing scenarios</span>
                <input type="search" value={scenarioCloneQuery} onChange={event => setScenarioCloneQuery(event.target.value)} placeholder="Scenario, script, data pool, release..." />
              </label>
              <div className="data-table scenario-clone-table">
                <table>
                  <thead>
                    <tr>
                      <th>Source</th>
                      <th>Scenario</th>
                      <th>Test ID</th>
                      <th>Workload</th>
                      <th>Associated Releases</th>
                      <th>Create Date</th>
                      <th>Start</th>
                      <th>Finish</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredCloneScenarios.map(scenario => {
                      const selected = selectedCloneScenarioId === scenario.id
                      const assignment = scenarioAssignmentSummary(scenario)
                      const showScenarioMeta = Boolean(scenario.ref_key || assignment)
                      const browserRuntimeSummary = formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)
                      return (
                        <tr
                          key={scenario.id}
                          className={`clickable-row ${selected ? 'selected' : ''}`}
                          aria-selected={selected}
                          tabIndex={0}
                          onClick={() => cloneScenarioDraft(scenario)}
                          onKeyDown={(event) => {
                            if (event.key === 'Enter' || event.key === ' ') {
                              event.preventDefault()
                              cloneScenarioDraft(scenario)
                            }
                          }}
                        >
                          <td>
                            <span className="script-health-token">Local</span>
                          </td>
                          <td className="cell-name">
                            <div>{scenario.name}</div>
                            {showScenarioMeta && (
                              <div className="scenario-table-assignment">
                                <ReferenceKeyBadge value={scenario.ref_key} />
                                {assignment && (
                                  <>
                                    <span className="scenario-table-assignment-badge manual">{assignment.badge}</span>
                                    <span className="scenario-table-assignment-detail">{assignment.detail}</span>
                                  </>
                                )}
                              </div>
                            )}
                            {browserRuntimeSummary && (
                              <div className="scenario-table-browser">
                                <span className="scenario-table-browser-badge">Browser</span>
                                <span className="scenario-table-assignment-detail">{browserRuntimeSummary}</span>
                              </div>
                            )}
                          </td>
                          <td className="cell-mono">{formatScenarioRunTestID(scenario)}</td>
                          <td aria-label="Scenario workload">
                            <div className="scenario-table-workload">
                              <strong>{formatScenarioScripts(scenario)}</strong>
                              <span>{formatScenarioDataPools(scenario)}</span>
                              <span>{formatScenarioResultCount(scenario)}</span>
                            </div>
                          </td>
                          <td aria-label="Scenario releases">
                            <div className="scenario-table-workload">
                              <strong>{releaseList(scenario)}</strong>
                            </div>
                          </td>
                          <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                          <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_at)}</td>
                          <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_ended_at)}</td>
                          <td style={{ textAlign: 'right' }}>
                            <button
                              className={`btn btn-sm ${selected ? 'btn-primary' : 'btn-secondary'}`}
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation()
                                cloneScenarioDraft(scenario)
                              }}
                            >
                              {selected ? 'Selected' : 'Model'}
                            </button>
                          </td>
                        </tr>
                      )
                    })}
                    {filteredCloneScenarios.length === 0 && (
                      <tr>
                        <td colSpan={9} className="cell-muted">No scenarios match this source search.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </>
          ) : (
            <div className="scenario-shape-catalog-grid">
              {shapeCatalog.map(option => (
                <div key={option.id} className={`scenario-shape-catalog-card ${selectedShapeId === option.id ? 'selected' : ''}`}>
                  <div className="scenario-support-card-header">
                    <div>
                      <strong>{option.support.name}</strong>
                      <span>{option.support.category}</span>
                    </div>
                    <span className={`scenario-load-status ${getLoadModelStatusClass(option.support.status)}`}>{formatLoadModelDimension(option.support.status)}</span>
                  </div>
                  <p>{option.support.apexload_shape}</p>
                  <div className="scenario-support-fields">
                    {option.support.fields.slice(0, 3).map(field => <code key={field}>{field}</code>)}
                    {option.support.fields.length > 3 && <code>+{option.support.fields.length - 3} fields</code>}
                  </div>
                  <WorkloadShapeVisual entry={option.support} />
                  <div className="scenario-shape-actions">
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => selectShapeOption(option.id)}>Use Manual Shape</button>
                    {option.templates.map(template => (
                      <button key={template.id} type="button" className={`btn btn-sm ${selectedLoadModelTemplateId === template.id ? 'btn-primary' : 'btn-ghost'}`} onClick={() => applyLoadModelTemplate(template.id)}>
                        {template.name}
                      </button>
                    ))}
                  </div>
                </div>
              ))}
              {shapeCatalog.length === 0 && <p className="scenario-assignment-copy secondary">No workload shape catalog is available from this controller.</p>}
            </div>
          )}
        </section>
      )
    }

    if (wizardStep === 'scripts') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>Scripts And Groups</h4>
              <p>Add each script participating in the scenario and set its initialization scope. Tune VUsers and ramp on the Run Settings step.</p>
            </div>
          </div>
          {renderShapeGuidance()}
          <div className="settings-grid scenario-core-grid">
            <label className="scenario-field">
              <span>Scenario Name</span>
              <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
            </label>
          </div>
          <div className="scenario-script-group-list scenario-script-group-list-spacious">
            {formGroups.map((group, index) => {
              const selectedScript = findScriptById(scripts, group.script_id)
              const isSelectionTarget = scriptSelectionTargetIndex === index
              return (
                <div
                  key={`${group.script_id || 'script'}-${index}`}
                  className={`scenario-script-group-card ${isSelectionTarget ? 'selecting-script' : ''}`}
                  ref={node => { scriptGroupRefs.current[index] = node }}
                >
                  <div className="scenario-script-group-title">
                    <strong>{group.name || `Group ${index + 1}`}</strong>
		  {formGroups.length > 1 && (
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => removeScenarioGroup(index)}>Remove</button>
                    )}
                  </div>
                  <div className="settings-grid scenario-group-grid">
                    <label className="scenario-field">
                      <span>Group Name</span>
                      <input
                        className="settings-input"
                        value={group.name}
                        onChange={event => updateScenarioGroup(index, { name: event.target.value })}
                        placeholder={`Group ${index + 1}`}
                        aria-invalid={duplicateGroupNameIndexes.has(index)}
                      />
                      {duplicateGroupNameIndexes.has(index) && <small className="scenario-field-error">Group names must be unique.</small>}
                    </label>
                    <div className="scenario-field scenario-script-picker-field">
                      <span>Script</span>
                      <button
                        type="button"
                        className={`scenario-script-select-button ${selectedScript ? 'has-script' : ''}`}
                        onClick={() => chooseScriptForGroup(index)}
                        aria-label={`Choose script for ${group.name || `Group ${index + 1}`}`}
                      >
                        <span className="scenario-script-select-copy">
                          <strong>{selectedScript?.name || 'Add Script'}</strong>
                          {selectedScript && <small>v{selectedScript.version || '—'}</small>}
                        </span>
                        {selectedScript && <span className="protocol-badge">{selectedScript.protocol || 'http'}</span>}
                      </button>
                    </div>
                    <label className="scenario-field">
                      <span>Initialization Scope</span>
                      <select className="settings-input" value={group.initialization} onChange={event => updateScenarioGroup(index, { initialization: event.target.value as ScenarioFormGroup['initialization'] })}>
                        <option value="at_runtime">Init/teardown per VUser</option>
                        <option value="at_once">Init/teardown once per group</option>
                      </select>
                    </label>
                    <label className="scenario-field">
                      <span>Role</span>
                      <select
                        className="settings-input"
                        value={group.role || 'load'}
                        onChange={event => {
                          const role = event.target.value as ScenarioFormGroup['role']
                          updateScenarioGroup(index, {
                            role,
                            ...(role === 'observer' ? {
                              vusers: clampVUsersForRole(group.vusers, role, 1),
                              stages: group.stages.map(stage => ({
                                ...stage,
                                ...(stage.target_vusers !== undefined
                                  ? { target_vusers: clampVUsersForRole(stage.target_vusers, role) }
                                  : {}),
                              })),
                            } : {}),
                          })
                        }}
                        title="Observer canaries are small browser groups that continuously report Web Vitals and stay pinned during goal-based scaling."
                      >
                        <option value="load">Load</option>
                        <option value="observer" disabled={selectedScript?.protocol !== 'browser'}>
                          Observer Canary{selectedScript?.protocol !== 'browser' ? ' (browser scripts only)' : ''}
                        </option>
                      </select>
                      {group.role === 'observer' && <small>Observer canaries are capped at {OBSERVER_MAX_VUSERS} VUsers.</small>}
                    </label>
                  </div>
                </div>
              )
            })}
            <button
              type="button"
              className="scenario-add-group-card"
              onClick={addScenarioGroup}
              disabled={!canAddScenarioGroup}
              title={!canAddScenarioGroup ? addScenarioGroupBlockedReason : undefined}
            >
              <span>+</span>
              <strong>Add Group</strong>
              {!canAddScenarioGroup && <small>{scenarioGroupNameError || 'Add a script to every existing group first'}</small>}
            </button>
          </div>
        </section>
      )
    }

    if (wizardStep === 'run') {
      const activeGroupScript = findScriptById(scripts, activeStageGroup.script_id)
      const activeGroupVUserMax = maxVUsersForRole(activeStageGroup.role)
      const activeGroupLabel = activeStageGroup.name || `Group ${activeGroupIndex + 1}`

      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Run Settings</h4>
            <p>Configure VUsers, ramp, duration, mode, goals, stage modeling, browser defaults, and generator assignment.</p>
          </div>
          {renderShapeGuidance()}
          <div className="scenario-run-settings-tabs" role="tablist" aria-label="Run settings sections">
            {runSettingsTabs.map(tab => (
              <button
                key={tab.id}
                type="button"
                id={`scenario-run-settings-${tab.id}-tab`}
                className={`scenario-run-settings-tab ${runSettingsTab === tab.id ? 'active' : ''}`}
                role="tab"
                aria-selected={runSettingsTab === tab.id}
                aria-controls={`scenario-run-settings-${tab.id}-panel`}
                onClick={() => setRunSettingsTab(tab.id)}
              >
                {tab.label}
              </button>
            ))}
          </div>
          <div
            className="scenario-run-settings-tab-panel"
            id={`scenario-run-settings-${runSettingsTab}-panel`}
            role="tabpanel"
            aria-labelledby={`scenario-run-settings-${runSettingsTab}-tab`}
          >
            {runSettingsTab === 'scenario' && (
              <>
          <div className="settings-grid scenario-core-grid">
            <DurationField
              className="scenario-field"
              label="Duration"
              value={form.duration}
              required
              showPresets
              onChange={value => {
                setForm({ ...form, duration: value })
                setLoadModelValidation(null)
                setLoadModelValidationError('')
              }}
            />
            <label className="scenario-field">
              <span>Scenario Mode</span>
              <select className="settings-input" value={form.scenario_mode} onChange={event => setForm({ ...form, scenario_mode: event.target.value as ScenarioMode })}>
                <option value="vuser_driven">VUser driven</option>
                <option value="goal_oriented">Goal oriented</option>
              </select>
            </label>
          </div>
          {formGroups.length > 1 && (
            <div className="scenario-group-tabs" role="tablist" aria-label="Scenario load groups">
              {formGroups.map((group, index) => {
                const script = findScriptById(scripts, group.script_id)
                const groupLabel = group.name || `Group ${index + 1}`
                return (
                  <button
                    key={`${group.script_id || 'group'}-scenario-tab-${index}`}
                    type="button"
                    id={`scenario-group-tab-${index}`}
                    className={`scenario-group-tab ${activeGroupIndex === index ? 'active' : ''}`}
                    role="tab"
                    aria-selected={activeGroupIndex === index}
                    aria-controls="scenario-active-group-panel"
                    onClick={() => setStageGroupIndex(index)}
                  >
                    <strong>{groupLabel}</strong>
                    <span>{script?.name || 'Unassigned script'} · {group.vusers} VUsers</span>
                  </button>
                )
              })}
            </div>
		  )}
		  <ScenarioCompositionTimeline
			duration={form.duration}
			groups={formGroups}
			onOffsetChange={(index, value) => updateScenarioGroup(index, { start_offset: value })}
		  />
		  <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Load Per Group</h4>
                <p>Set the VUser count and simple ramp for each script group participating in this scenario.</p>
              </div>
            </div>
            <div
              id="scenario-active-group-panel"
              className="scenario-script-group-list scenario-load-group-panel"
              role={formGroups.length > 1 ? 'tabpanel' : undefined}
              aria-labelledby={formGroups.length > 1 ? `scenario-group-tab-${activeGroupIndex}` : undefined}
            >
                <div key={`${activeStageGroup.script_id || 'group'}-load-${activeGroupIndex}`} className="scenario-script-group-card">
                  <div className="scenario-script-group-title">
                    <strong>{activeGroupLabel}</strong>
                    <span className="scenario-assignment-copy secondary">{activeGroupScript?.name || 'Unassigned script'}</span>
                  </div>
                  <div className="settings-grid scenario-group-grid">
                    <label className="scenario-field">
                      <span>VUsers</span>
                      <div className="scenario-vuser-field">
                        <input
                          className="scenario-vuser-slider"
                          type="range"
                          min={1}
                          max={activeStageGroup.role === 'observer'
                            ? activeGroupVUserMax
                            : Math.min(activeGroupVUserMax, Math.max(100, Number(activeStageGroup.vusers) * 2 || 100))}
                          value={activeStageGroup.vusers}
                          onChange={event => updateScenarioGroup(activeGroupIndex, {
                            vusers: clampVUsersForRole(Number(event.target.value), activeStageGroup.role, 1),
                          })}
                        />
                        <input
                          className="settings-input"
                          type="number"
                          min={1}
                          max={activeGroupVUserMax}
                          value={activeStageGroup.vusers}
                          onChange={event => updateScenarioGroup(activeGroupIndex, {
                            vusers: clampVUsersForRole(Number(event.target.value), activeStageGroup.role, 1),
                          })}
                        />
                      </div>
                    </label>
					<DurationField
						className="scenario-field"
						label="Start Offset"
						value={activeStageGroup.start_offset || ''}
						allowEmpty
						compact
						onChange={value => updateScenarioGroup(activeGroupIndex, { start_offset: value })}
					/>
					<DurationField
						className="scenario-field"
						label="Ramp Up"
                      value={activeStageGroup.ramp_up}
                      allowEmpty
                      compact
                      onChange={value => updateScenarioGroup(activeGroupIndex, { ramp_up: value })}
                    />
                    <DurationField
                      className="scenario-field"
                      label="Ramp Down"
                      value={activeStageGroup.ramp_down}
                      allowEmpty
                      compact
                      onChange={value => updateScenarioGroup(activeGroupIndex, { ramp_down: value })}
                    />
                  </div>
                </div>
            </div>
          </div>
          <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Pre-Run Assurance</h4>
                <p>Run a one-pass canary/shakeout immediately before full load. Passing starts the run automatically; failures pause with evidence for continue/cancel.</p>
              </div>
            </div>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${form.script_shakeout ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.script_shakeout} onChange={event => setForm({ ...form, script_shakeout: event.target.checked })} />
                <div><strong>Script shakeout gate</strong><span>Execute init/actions once for each script group before full load.</span></div>
              </label>
              <label className={`scenario-option-card ${form.resource_baseline ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.resource_baseline} onChange={event => setForm({ ...form, resource_baseline: event.target.checked })} />
                <div><strong>Resource baseline windows</strong><span>Capture 5m pre/post resource evidence around the run.</span></div>
              </label>
              <label className={`scenario-option-card ${form.continue_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.continue_on_error ?? false} onChange={event => setForm({ ...form, continue_on_error: event.target.checked })} />
                <div><strong>Continue on action errors</strong><span>Keep collecting resilience evidence after request-level failures.</span></div>
              </label>
              <label className={`scenario-option-card ${form.snapshot_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.snapshot_on_error ?? false} onChange={event => setForm({ ...form, snapshot_on_error: event.target.checked })} />
                <div><strong>Snapshot on error</strong><span>Preserve request/response context for failed actions.</span></div>
              </label>
            </div>
          </div>
          {form.scenario_mode === 'goal_oriented' && (
            <div className="scenario-subpanel">
              <div className="scenario-assignment-header">
                <div>
                  <h4>Goal Controller</h4>
                  <p>Persist target envelope settings with this scenario draft.</p>
                </div>
              </div>
              <CreateTestGoalSettings
                goalConfig={goalConfigFromForm(form)}
                onGoalConfigChange={(field, value) => setForm(previous => scenarioFormWithGoalField(previous, field, value))}
              />
            </div>
          )}
          <div className="scenario-subpanel">
            {activeProfileMapping && (
              <div className="scenario-shape-guidance">
                <strong>Managed by production traffic profile</strong>
                <p>{activeProfileMapping.points.length.toLocaleString()} {form.traffic_profile?.runtime_target.toUpperCase()} target changes · peak {activeProfileMapping.peak_target}</p>
                <button type="button" className="btn btn-secondary" onClick={() => { setForm(previous => ({ ...previous, traffic_profile: undefined })); setSelectedShapeId('') }}>Remove profile</button>
              </div>
            )}
            <div hidden={Boolean(activeProfileMapping)}>
            <div className="scenario-assignment-header">
              <div>
                <h4>Advanced Stair-Step Ramp</h4>
                <p>Keep simple ramp fields for common runs, or enable explicit stages when each interval needs its own VUser/TPS target.</p>
              </div>
              <div className="scenario-stage-actions">
                {activeStageGroup.stages.length > 0 && (
                  <button type="button" className="btn btn-secondary" onClick={disableStages}>
                    Use Simple Ramp
                  </button>
                )}
                <button type="button" className="btn btn-secondary" onClick={activeStageGroup.stages.length > 0 ? addStage : enableDefaultStages}>
                  {activeStageGroup.stages.length > 0 ? '+ Add Stage' : 'Enable Stages'}
                </button>
              </div>
            </div>
            {selectedShape && (
              <p className="scenario-assignment-copy secondary">
                Regeneration uses the selected {selectedShape.support.name} shape plus {activeGroupLabel}'s current peak and scenario duration.
              </p>
            )}
            {activeStageGroup.stages.length > 0 ? (
              <div className="scenario-stage-list">
                <div className="scenario-stage-toolbar">
                  <span className={`scenario-stage-status ${activeStageTimeline.status}`}>{formatLoadModelDimension(activeStageTimeline.status)}</span>
                  {activeStageTimeline.message && <span>{activeStageTimeline.message}</span>}
                  <button type="button" className="btn btn-sm btn-ghost" onClick={enableDefaultStages}>
                    {selectedShape ? `Regenerate for ${selectedShape.support.name}` : 'Regenerate From Simple Ramp'}
                  </button>
                </div>
                {activeStageGroup.stages.map((stage, index) => (
                  <div key={`${stage.name || 'stage'}-${index}`} className="scenario-stage-row">
                    <input className="settings-input" value={stage.name || ''} onChange={event => updateStage(index, { name: event.target.value })} placeholder="Stage name" />
                    <select className="settings-input" value={stage.type} onChange={event => updateStage(index, { type: event.target.value as LoadStageType })}>
                      <option value="ramp_up">Ramp up</option>
                      <option value="hold">Hold</option>
                      <option value="ramp_down">Ramp down</option>
                    </select>
                    <DurationField
                      value={stage.duration}
                      compact
                      required
                      showValidation={false}
                      onChange={value => updateStage(index, { duration: value })}
                    />
                    <input
                      className="settings-input"
                      type="number"
                      min={0}
                      max={activeGroupVUserMax}
                      value={stage.target_vusers ?? ''}
                      onChange={event => updateStage(index, {
                        target_vusers: event.target.value === ''
                          ? undefined
                          : clampVUsersForRole(Number(event.target.value), activeStageGroup.role),
                      })}
                      placeholder="Target VUsers"
                    />
                    <input className="settings-input" type="number" min={0} value={stage.target_tps ?? ''} onChange={event => updateStage(index, { target_tps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Target TPS" />
                    <div className="scenario-stage-row-actions">
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => duplicateStage(index)}>Duplicate</button>
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => removeStage(index)}>Remove</button>
                    </div>
                  </div>
                ))}
                <p className="scenario-assignment-copy secondary">Peak staged VUsers: {maxStageVUsers(activeStageGroup.stages) || 0}. Validation will reject stages missing duration plus either VUsers or TPS.</p>
              </div>
            ) : (
              <p className="scenario-assignment-copy secondary">Simple ramp is active for this group. Enable advanced stages to define stair-step intervals and per-step VUser/TPS targets.</p>
            )}
            </div>
          </div>
              </>
            )}
            {runSettingsTab === 'runtime' && (
              <>
              <RuntimeSettingsGroupEditor
                draftScope={`${activeProjectId}:${editingId || 'new-scenario'}`}
                groups={formGroups.map(group => {
                  const script = findScriptById(scripts, group.script_id)
                  return {
                    name: group.name,
                    scriptId: group.script_id,
                    protocol: script?.protocol,
                    scriptConfig: script?.script?.config,
                    scriptActions: script?.script
                      ? [
                          ...(script.script.init || []),
                          ...(script.script.actions || []),
                          ...(script.script.teardown || []),
                        ]
                      : undefined,
                    importedJavaVMParameters: readImportedJavaVMParameters(script?.script?.metadata?.import),
                  }
                })}
                focusedIndex={runtimeGroupIndex}
                onFocusChange={setRuntimeGroupIndex}
                scenarioDefault={overrideFromRuntimeDraft(runtimeSettingsFromForm(form, showBrowserRuntimeSettings))}
                engineDefaults={engineDefaults}
                overrides={formGroups.map(group => group.runtimeSettings)}
                onChange={updateGroupRuntime}
                onApplyToGroups={applyRuntimeToGroups}
              />
              <RuntimeSettingsGroupEditor
                draftScope={`${activeProjectId}:${editingId || 'new-scenario'}:defaults`}
                title="Scenario runtime defaults"
                description="These defaults apply to every script group unless that group overrides them above. Unset fields inherit the script and engine defaults."
                hideApplyPanel
                groups={[{ name: 'Scenario defaults', scriptId: editingId || 'scenario-defaults' }]}
                focusedIndex={0}
                onFocusChange={() => {}}
                engineDefaults={engineDefaults}
                applicableProtocols={scenarioDefaultProtocols}
                overrides={[runtimeSettingsFromForm(form, showBrowserRuntimeSettings)]}
                onChange={(_, next) => applyScenarioDefaultRuntime(next)}
                onApplyToGroups={() => {}}
              />
              </>
            )}
            {runSettingsTab === 'infrastructure' && (
              <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Controller / Generator Assignment</h4>
                <p>Use automatic placement, host pinning, capability labels, or weighted regions and load zones for the scenario default.</p>
              </div>
              {estimateLoading && <span className="scenario-assignment-status">Estimating...</span>}
            </div>
            {draftEstimate?.footprint && (
              <div className="scenario-assignment-footprint">
                <div className="scenario-assignment-metric"><span>Footprint</span><strong>{draftEstimate.footprint.total_vusers} VUsers</strong><small>{draftEstimate.footprint.duration}</small></div>
                <div className="scenario-assignment-metric"><span>Complexity</span><strong>{draftEstimate.footprint.complexity_score.toFixed(2)}</strong><small>derived from current script mix</small></div>
                <div className="scenario-assignment-metric"><span>Estimated Load</span><strong>{draftEstimate.footprint.estimated_cpu_cores.toFixed(2)} CPU</strong><small>{Math.round(draftEstimate.footprint.estimated_memory_mb)} MB / {Math.round(draftEstimate.footprint.estimated_network_mbps)} Mbps</small></div>
              </div>
            )}
            {assignmentRecommendation?.summary && <p className="scenario-assignment-copy">{assignmentRecommendation.summary}</p>}
            <div className="scenario-root-placement-editor">
              <GroupPlacementEditor
                assignment={rootAssignment}
                onChange={updateRootAssignment}
                availableTargets={availableTargets}
                recommendedTargets={recommendedTargets}
                generatorInventory={generatorInventory}
                availableLabels={availableLabels}
                allowInherit={false}
                scope="scenario"
              />
            </div>
            {rootAssignment.mode === 'auto' && recommendedTargets.length > 0 ? (
              <div className="scenario-assignment-chip-row">{recommendedTargets.map(target => <span key={target} className="scenario-assignment-chip">{target}</span>)}</div>
            ) : null}
            {estimateError && <div className="settings-error-inline">{estimateError}</div>}
            {assignmentRecommendation?.warnings && assignmentRecommendation.warnings.length > 0 && (
              <div className="scenario-assignment-warnings">{assignmentRecommendation.warnings.map(warning => <p key={warning}>{warning}</p>)}</div>
            )}
            <div className="scenario-group-placement-panel">
              <div className="scenario-assignment-header compact">
                <div>
                  <h4>Per-Group Generator Placement</h4>
                  <p>Groups inherit the scenario default unless they explicitly use automatic placement, host pinning, or a required capability.</p>
                </div>
              </div>
              <div className="scenario-group-placement-list">
                {formGroups.map((group, index) => {
                  const script = findScriptById(scripts, group.script_id)
                  return (
                    <div key={`${group.script_id}-${index}`} className="scenario-group-placement-item">
                      <div className="scenario-group-placement-title">
                        <strong>{group.name || script?.name || `Group ${index + 1}`}</strong>
                        <span>{script?.protocol || 'script'} · {group.vusers} VUsers</span>
                      </div>
                      <GroupPlacementEditor
                        assignment={group.assignment}
                        onChange={assignment => updateScenarioGroup(index, { assignment })}
                        availableTargets={availableTargets}
                        recommendedTargets={recommendedTargets}
                        generatorInventory={generatorInventory}
                        availableLabels={availableLabels}
                        protocol={script?.protocol}
                        compact
                        scope="group"
                      />
                    </div>
                  )
                })}
              </div>
            </div>
              </div>
            )}
          </div>
        </section>
      )
    }

    if (wizardStep === 'data') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Data Pools</h4>
            <p>Attach one data pool for scripts that need coordinated parameter data.</p>
          </div>
          <div className="settings-grid scenario-core-grid">
            <label className="scenario-field">
              <span>Data Pool</span>
              <select className="settings-input" value={form.data_pool_id || ''} onChange={event => setForm({ ...form, data_pool_id: event.target.value })}>
                <option value="">No data pool</option>
                {dataPools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
              </select>
            </label>
            <label className="scenario-field">
              <span>Pool Alias</span>
              <input className="settings-input" value={form.data_pool_alias || ''} onChange={event => setForm({ ...form, data_pool_alias: event.target.value })} placeholder="Pool alias, e.g. customers" disabled={!form.data_pool_id} />
            </label>
            <label className="scenario-field">
              <span>Access Mode</span>
              <select className="settings-input" value="read_only" disabled>
                <option value="read_only">Read-only run snapshot</option>
              </select>
              <small>Rows are snapshotted when the run is created; the shared pool is never mutated by execution.</small>
            </label>
            <label className="scenario-field">
              <span>Selection Strategy</span>
              <select className="settings-input" value={form.data_pool_selection_strategy || 'sequential'} onChange={event => setForm({ ...form, data_pool_selection_strategy: event.target.value as 'sequential' | 'random' | 'unique' })} disabled={!form.data_pool_id}>
                <option value="sequential">Sequential rows</option>
                <option value="random">Random rows</option>
                <option value="unique">Unique rows</option>
              </select>
            </label>
          </div>
        </section>
      )
    }

    return (
      <section className="scenario-wizard-panel">
        <div className="scenario-wizard-panel-header">
          <div>
            <h4>Review And Validate</h4>
            <p>Validate modeled demand, capacity, data fit, and generator placement before saving.</p>
          </div>
          <button type="button" className="btn btn-secondary" onClick={validateLoadModel} disabled={loadModelValidationLoading || Boolean(blockingStageTimelineMessage || observerEnvelopeError || aggregateEnvelopeError || assignmentValidationError)}>
            {loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}
          </button>
        </div>
        <label className="scenario-field scenario-name-prompt">
          <span>{form.name.trim() ? 'Scenario Name' : 'Name this scenario to save it'}</span>
          <input
            className="settings-input"
            value={form.name}
            placeholder="Scenario name"
            aria-label="Scenario name"
            onChange={event => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <div className="scenario-review-grid">
          <div className="scenario-review-card"><span>Name</span><strong>{form.name.trim() || 'Unnamed scenario'}</strong></div>
          <div className="scenario-review-card"><span>Shape</span><strong>{shapeLabel}</strong></div>
          <div className="scenario-review-card"><span>Scripts</span><strong>{formGroups.length} group{formGroups.length === 1 ? '' : 's'}</strong></div>
          <div className="scenario-review-card"><span>Duration</span><strong>{form.duration}</strong></div>
          <div className="scenario-review-card"><span>Mode</span><strong>{form.scenario_mode === 'goal_oriented' ? 'Goal oriented' : 'VUser driven'}</strong></div>
          <div className="scenario-review-card"><span>Data</span><strong>{dataPoolLabel}</strong></div>
          <div className="scenario-review-card"><span>Shakeout</span><strong>{form.script_shakeout ? 'Enabled' : 'Disabled'}</strong></div>
        </div>
        <div className="data-table scenario-review-groups">
          <table aria-label="Scenario review groups">
            <thead>
              <tr>
                <th>Group</th>
                <th>Script</th>
                <th>Role</th>
                <th>VUsers</th>
                <th>Ramp Up</th>
                <th>Ramp Down</th>
                <th>Initialization</th>
              </tr>
            </thead>
            <tbody>
              {formGroups.map((group, index) => {
                const script = findScriptById(scripts, group.script_id)
                return (
                  <tr key={`${group.script_id || 'group'}-review-${index}`}>
                    <td>{group.name || `Group ${index + 1}`}</td>
                    <td>{script?.name || 'Unassigned script'}</td>
                    <td>{group.role === 'observer' ? 'Observer Canary' : 'Load'}</td>
                    <td>{group.vusers}</td>
                    <td>{group.ramp_up || 'None'}</td>
                    <td>{group.ramp_down || 'None'}</td>
                    <td>{group.initialization === 'at_once' ? 'Once per group' : 'Per VUser'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        {loadModelValidationError && <div className="settings-error-inline">{loadModelValidationError}</div>}
        {loadModelValidation ? (
          <div className="scenario-load-validation">
            <div className={`scenario-load-validation-summary ${getLoadModelStatusClass(loadModelValidation.status)}`}>
              <strong>{formatLoadModelDimension(loadModelValidation.status)}</strong>
              <span>{loadModelValidation.total_vusers} VUsers{loadModelValidation.peak_target_tps ? ` · ${loadModelValidation.peak_target_tps} TPS peak` : ''}</span>
            </div>
            <div className="scenario-load-validation-items">
              {loadModelValidation.items.map((item, index) => (
                <div key={`${item.dimension}-${item.group || 'all'}-${index}`} className="scenario-load-validation-item">
                  <span className={`scenario-load-status ${getLoadModelStatusClass(item.status)}`}>{formatLoadModelDimension(item.status)}</span>
                  <div><strong>{formatLoadModelDimension(item.dimension)}{item.group ? ` · ${item.group}` : ''}</strong><p>{item.summary}</p></div>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <p className="scenario-assignment-copy secondary">Run validation to check demand, capacity, data fit, and generator placement.</p>
        )}
      </section>
    )
  }

  return (
    <div className="page">
      {(!showForm || !hasTopbarContributionProvider) && (
        <div className="page-header">
          <div>
            <h2>{showPlans ? 'Execution Plans' : showForm ? 'Scenario Builder' : 'Scenario Library'}</h2>
            <p className="page-subtitle">
              {showPlans
                ? 'Ordered, cross-run orchestration of scenarios — parallel or sequential.'
                : 'Reusable scenario definitions linked to scripts, releases, and results.'}
            </p>
          </div>
          {!hasTopbarContributionProvider && scenarioPageControls}
        </div>
      )}

      {error && <div className="settings-error-inline page-error-banner" role="alert">{error}</div>}

      {showPlans && (
        <div className="page-section">
          <ExecutionPlansPanel
            projectId={activeProject?.id}
            canManage={canManageTests}
            canAdmin={canManageProjects}
            scenarioOptions={scenarios.map(scenario => ({ id: scenario.id, name: scenario.name }))}
            onOpenTest={onSelectTest}
            seedScenarioId={planSeedScenarioId ?? undefined}
            onSeedConsumed={() => setPlanSeedScenarioId(null)}
          />
        </div>
      )}

      {showForm && (
        <div className="page-section scenario-builder">
          <div className="scenario-builder-header">
            <div>
              {form.name.trim()
                ? <p className="scenario-builder-name" aria-label="Scenario name">{form.name.trim()}</p>
                : <p className="page-subtitle">Compose scripts, choose a workload source, tune run settings, attach data, and validate the planned model.</p>}
            </div>
            <div className="scenario-builder-progress">
              <span className={requiredDraftReady ? 'ready' : ''}>Draft</span>
              <span className={selectedShapeId || scenarioSourceMode === 'existing' ? 'ready' : ''}>Model</span>
              <span className={loadModelValidation?.status === 'blocked' ? 'blocked' : loadModelValidation ? 'ready' : ''}>Validation</span>
            </div>
          </div>

          <div className="scenario-wizard-stepper" aria-label="Scenario builder steps">
            {scenarioWizardSteps.map((step, index) => {
              const stepComplete = index <= maxVisitedWizardIndex && wizardStepCompletion[step.id]
              return (
                <button
                  key={step.id}
                  type="button"
                  className={`scenario-wizard-step ${wizardStep === step.id ? 'active' : ''} ${stepComplete ? 'complete' : ''}`}
                  onClick={() => setWizardStep(step.id)}
                  aria-label={`${step.label}${stepComplete ? ' complete' : ''}`}
                >
                  <span className="scenario-wizard-step-marker" aria-hidden="true">{stepComplete ? '✓' : index + 1}</span>
                  <span className="scenario-wizard-step-label">{step.label}</span>
                </button>
              )
            })}
          </div>

          <div className={`scenario-builder-workspace ${showModelPreview ? 'with-preview' : ''}`}>
            <div className="scenario-builder-main">
              {renderWizardStep()}
            </div>

            {showModelPreview && (
              <aside className="scenario-builder-preview">
                <ScenarioModelPreviewPanel
                  model={scenarioModel}
                  shapeLabel={shapeLabel}
                  dataPoolLabel={dataPoolLabel}
                  validationStatus={validationLabel}
                />
              </aside>
            )}
          </div>

          {renderSourceScenarioRunsPanel()}

          {blockingStageTimelineMessage && <div className="settings-error-inline">{blockingStageTimelineMessage}</div>}
          {observerEnvelopeError && <div className="settings-error-inline">{observerEnvelopeError}</div>}
          {aggregateEnvelopeError && <div className="settings-error-inline">{aggregateEnvelopeError}</div>}
          {assignmentValidationError && <div className="settings-error-inline">{assignmentValidationError}</div>}
          {scenarioGroupNameError && <div className="settings-error-inline">{scenarioGroupNameError}</div>}

          <div className="scenario-builder-actions">
            <button className="btn btn-secondary" onClick={guardedOpenScenarioLibrary}>Cancel</button>
            {builderHasProgress && (
              <button
                type="button"
                className="btn btn-ghost"
                style={{ color: 'var(--danger)' }}
                onClick={startBlankScenario}
                title="Discard this in-progress draft and start a new blank scenario"
              >
                Start Over
              </button>
            )}
            {currentWizardIndex > 0 && (
              <button
                className="btn btn-secondary"
                onClick={() => {
                  const nextIndex = Math.max(0, currentWizardIndex - 1)
                  setWizardStep(scenarioWizardSteps[nextIndex].id)
                }}
              >
                Back
              </button>
            )}
            <button
              className="btn btn-secondary"
              onClick={() => {
                const nextIndex = Math.min(scenarioWizardSteps.length - 1, currentWizardIndex + 1)
                setWizardStep(scenarioWizardSteps[nextIndex].id)
              }}
              disabled={currentWizardIndex === scenarioWizardSteps.length - 1 || (wizardStep === 'scripts' && Boolean(scenarioGroupNameError))}
            >
              Next
            </button>
            {wizardStep === 'review' && onCreateTestFromScenario && (
              <button className="btn btn-secondary" onClick={saveScenarioAndCreateTest} disabled={Boolean(blockingStageTimelineMessage || observerEnvelopeError || aggregateEnvelopeError || assignmentValidationError || scenarioGroupNameError)}>Save &amp; Create Test Run</button>
            )}
            <button className="btn btn-primary" onClick={saveScenario} disabled={Boolean(blockingStageTimelineMessage || observerEnvelopeError || aggregateEnvelopeError || assignmentValidationError || scenarioGroupNameError)}>Save Scenario</button>
          </div>

        </div>
      )}

      {showScriptLibrary && (
        <div className="page-section scenario-existing-script-library" ref={scriptLibraryRef}>
          <div className="library-toolbar">
            <div>
              <h3 className="section-title">Existing Scripts Library ({scripts.length})</h3>
              <p className="library-toolbar-subtitle">
                {scriptLibraryQuery.trim() ? `${visibleScriptLibrary.length} matching script${visibleScriptLibrary.length === 1 ? '' : 's'}` : 'Pick from existing scripts while assembling scenario groups.'}
              </p>
              {scriptSelectionTargetIndex !== null && formGroups[scriptSelectionTargetIndex] && (
                <p className="scenario-script-selection-status">
                  Choosing for {formGroups[scriptSelectionTargetIndex].name || `Group ${scriptSelectionTargetIndex + 1}`}
                </p>
              )}
            </div>
            <label className="library-search">
              <span>Search scripts</span>
              <input
                ref={scriptLibrarySearchRef}
                type="search"
                value={scriptLibraryQuery}
                onChange={(event) => setScriptLibraryQuery(event.target.value)}
                placeholder="Find scripts..."
              />
            </label>
          </div>
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <SortHeader label="Script" field="name" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                  <SortHeader label="Created" field="created_at" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                  <SortHeader label="Last Run" field="last_run_at" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                  <SortHeader label="Associated Scenarios" field="associated_scenarios" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} numeric />
                  <SortHeader label="Associated Releases" field="associated_releases" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} numeric />
                  <SortHeader label="Protocol(s)" field="protocol" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {visibleScriptLibrary.map(script => {
                  const targetScriptId = scriptSelectionTargetIndex !== null ? formGroups[scriptSelectionTargetIndex]?.script_id : ''
                  const selectedForTarget = Boolean(targetScriptId) && targetScriptId === script.id
                  return (
                    <tr key={script.id} className={selectedForTarget ? 'selected' : ''}>
                      <td className="cell-name">
                        <div>{script.name}</div>
                        <div className="scenario-table-assignment-detail">v{script.version || '—'} · {script.id}</div>
                      </td>
                      <td className="cell-muted">{formatDate(script.created_at)}</td>
                      <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                      <td aria-label="Script scenario usage">
                        <div className="scenario-table-workload">
                          <strong>{formatScriptScenarioCount(script)}</strong>
                          <span>{associatedScenarioList(script)}</span>
                        </div>
                      </td>
                      <td>
                        <div className="scenario-table-workload">
                          <strong>{scriptReleaseList(script)}</strong>
                          {!!script.scenario_run_releases?.length && <span>Run releases: {uniqueValues(script.scenario_run_releases.map(release => release.name || release.id)).join(', ')}</span>}
                        </div>
                      </td>
                      <td>
                        <div className="scenario-protocol-list">
                          {scriptProtocols(script).map(protocol => <span key={protocol} className="protocol-badge">{protocol}</span>)}
                        </div>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <button
                          className="btn btn-sm btn-secondary"
                          type="button"
                          onClick={() => selectScriptFromLibrary(script)}
                          disabled={scriptSelectionTargetIndex === null && !canAddScenarioGroup}
                          title={scriptSelectionTargetIndex === null && !canAddScenarioGroup
                            ? addScenarioGroupBlockedReason
                            : undefined}
                        >
                          {scriptSelectionTargetIndex !== null ? `Use for Group ${scriptSelectionTargetIndex + 1}` : 'Add Group'}
                        </button>
                      </td>
                    </tr>
                  )
                })}
                {visibleScriptLibrary.length === 0 && (
                  <tr>
                    <td colSpan={7} className="cell-muted">
                      {scriptLibraryQuery.trim() ? 'No scripts match this search.' : 'No existing scripts are available for this project.'}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {showScenarioLibrary && (
      <div className="page-section">
        <LibraryFederationControls
          kind="scenario"
          federation={scenarioFederation}
          projectName={activeProject?.name}
          onConfigureRemotes={openRemoteSettings}
          summaryText={scenarioQuery.trim() || scenarioReleaseFilter !== 'all' ? `${sorted.length} matching scenario${sorted.length === 1 ? '' : 's'}` : 'Search by scenario, script, data pool, release, host, or browser profile.'}
          filterControls={(
            <>
              <label className="project-redaction-rule-field">
                <span>Associated Release</span>
                <select
                  className="settings-input library-source-select library-release-filter"
                  value={scenarioReleaseFilter}
                  onChange={(event) => setScenarioReleaseFilter(event.target.value)}
                >
                  <option value="all">All releases</option>
                  {scenarioReleaseFilter !== 'all' && !scenarioReleaseOptions.includes(scenarioReleaseFilter) && (
                    <option value={scenarioReleaseFilter}>{scenarioReleaseFilter}</option>
                  )}
                  {scenarioReleaseOptions.map(name => <option key={name} value={name}>{name}</option>)}
                </select>
              </label>
              <label className="project-redaction-rule-field">
                <span>Search Release</span>
                <input
                  className="settings-input"
                  type="search"
                  value={scenarioQuery}
                  onChange={(event) => setScenarioQuery(event.target.value)}
                  placeholder="Find releases or scenarios..."
                />
              </label>
            </>
          )}
        />
        {canManageTests && (
          <div className="library-bulk-selection-toolbar">
            <span>{selectedVisibleScenarioIds.length} selected</span>
            <div className="library-bulk-selection-actions">
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => setSelectedScenarioIds(new Set())}
                disabled={selectedVisibleScenarioIds.length === 0}
              >
                Clear
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => { void deleteSelectedScenarios() }}
                disabled={selectedVisibleScenarioIds.length === 0 || selectedVisibleScenarioIds.some(id => deletingScenarioIds.has(id))}
              >
                Delete Selected ({selectedVisibleScenarioIds.length})
              </button>
            </div>
          </div>
        )}
        <div className="data-table scenario-library-table">
          <table>
            <thead>
              <tr>
                {canManageTests && (
                  <th className="library-selection-column">
                    <SelectAllCheckbox
                      selectedCount={selectedVisibleScenarioIds.length}
                      visibleCount={visibleScenarioIds.length}
                      ariaLabel="Select all visible scenarios"
                      onChange={toggleAllVisibleScenarios}
                    />
                  </th>
                )}
                <th>Source</th>
                <SortHeader label="Scenario" field="name" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <SortHeader label="Test ID" field="last_run_test_id" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <SortHeader label="Workload" field="scripts" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                <SortHeader label="Associated Releases" field="associated_releases" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                <SortHeader label="Create Date" field="created_at" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <SortHeader label="Start" field="last_run_at" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <SortHeader label="Finish" field="last_run_ended_at" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {showLocalScenarios && sorted.map(scenario => {
                const syncStatus = scenarioFederation.syncStatusForLocal(scenario.id)
                const hostItem = scenarioFederation.hostItemForLocal(scenario.id)
                const conflict = syncStatus === 'conflict'
                const assignment = scenarioAssignmentSummary(scenario)
                const showScenarioMeta = Boolean(scenario.ref_key || assignment)
                return (
                <Fragment key={scenario.id}>
                  <tr className={`clickable-row${selectedScenarioIds.has(scenario.id) ? ' selected' : ''}`} onClick={() => toggleScenarioRuns(scenario)}>
                    {canManageTests && (
                      <td className="library-selection-column">
                        <input
                          type="checkbox"
                          aria-label={`Select ${scenario.name}`}
                          checked={selectedScenarioIds.has(scenario.id)}
                          disabled={deletingScenarioIds.has(scenario.id)}
                          onClick={event => event.stopPropagation()}
                          onChange={() => toggleScenarioSelection(scenario.id)}
                        />
                      </td>
                    )}
                    <td>
                      <span className="script-health-token">Local</span>
                    </td>
                    <td className="cell-name">
                      <div>{scenario.name}</div>
                      {showScenarioMeta && (
                        <div className="scenario-table-assignment">
                          <ReferenceKeyBadge value={scenario.ref_key} />
                          {assignment && (
                            <>
                              <span className="scenario-table-assignment-badge manual">{assignment.badge}</span>
                              <span className="scenario-table-assignment-detail">{assignment.detail}</span>
                            </>
                          )}
                        </div>
                      )}
                      {formatBrowserRuntimeSummary(scenario.runtime_settings?.browser) && (
                        <div className="scenario-table-browser">
                          <span className="scenario-table-browser-badge">Browser</span>
                          <span className="scenario-table-assignment-detail">{formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)}</span>
                        </div>
                      )}
                    </td>
                    <td className="cell-mono">{formatScenarioRunTestID(scenario)}</td>
                    <td aria-label="Scenario workload">
                      <div className="scenario-table-workload">
                        <strong>{formatScenarioScripts(scenario)}</strong>
                        <span>{formatScenarioDataPools(scenario)}</span>
                        <span>{formatScenarioResultCount(scenario)}</span>
                      </div>
                    </td>
                    <td aria-label="Scenario releases">
                      <div className="scenario-table-workload">
                        <strong>{releaseList(scenario)}</strong>
                      </div>
                    </td>
                    <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                    <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_at)}</td>
                    <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_ended_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-secondary" onClick={(event) => {
                        event.stopPropagation()
                        void toggleScenarioRuns(scenario)
                      }}>{expandedScenarioId === scenario.id ? 'Hide Runs' : 'View Runs'}</button>
                      {canManageTests ? (
                        <>
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); openEdit(scenario) }}>Edit</button>
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); duplicateScenario(scenario) }}>Duplicate</button>
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); addScenarioToPlan(scenario.id) }} title="Seed a new execution plan with this scenario">Add to plan</button>
                          <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} disabled={deletingScenarioIds.has(scenario.id)} onClick={(event) => { event.stopPropagation(); setConfirmDeleteScenarioId(scenario.id) }}>{deletingScenarioIds.has(scenario.id) ? 'Deleting…' : 'Delete'}</button>
                          <button
                            className="btn btn-sm btn-secondary"
                            onClick={(event) => { event.stopPropagation(); void scenarioFederation.pushLocalItem({ kind: 'scenario', id: scenario.id, name: scenario.name }) }}
                            disabled={scenarioFederation.actionBlocked || conflict}
                            title={conflict ? 'Compare this conflict before transferring.' : scenarioFederation.actionBlockedReason}
                          >Push</button>
                          {hostItem?.local_item_id && (
                            <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); void scenarioFederation.compareItems(scenario.id, hostItem) }}>Compare</button>
                          )}
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                  {expandedScenarioId === scenario.id && (
                    <tr className="no-hover">
                      <td colSpan={canManageTests ? 10 : 9} style={{ background: 'var(--bg-primary)' }}>
                        {renderScenarioRunsContent(scenario)}
                      </td>
                    </tr>
                  )}
                </Fragment>
                )
              })}
              {federationRemoteScenarioRows.map(item => {
                const syncStatus = item.sync_status || 'host only'
                const conflict = syncStatus === 'conflict'
                const href = scenarioFederation.remoteItemHref(item)
                return (
                  <tr key={`remote:${item.kind}:${item.id}`} className="no-hover">
                    {canManageTests && <td className="library-selection-column"><span className="cell-muted">—</span></td>}
                    <td>
                      <span className="script-health-token">Remote</span>
                    </td>
                    <td className="cell-name">
                      <div>{item.name || item.id}</div>
                      <div className="scenario-table-assignment-detail">Project {item.project_id || 'unmapped'} · Hash {shortLibraryHash(item.content_hash)}</div>
                    </td>
                    <td className="cell-mono">—</td>
                    <td aria-label="Library dependencies">
                      <div className="scenario-table-workload">
                        <span>{item.dependencies?.length ? `${item.dependencies.length} dependencies` : 'No referenced scripts'}</span>
                      </div>
                    </td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td style={{ textAlign: 'right' }}>
                      {canManageTests ? (
                        <>
                          <button
                            className="btn btn-sm btn-secondary"
                            onClick={() => void scenarioFederation.pullRemoteItem(item)}
                            disabled={scenarioFederation.actionBlocked || conflict}
                            title={conflict ? 'Compare this conflict before transferring.' : scenarioFederation.actionBlockedReason}
                          >Pull</button>
                          {item.local_item_id && (
                            <button className="btn btn-sm btn-ghost" onClick={() => void scenarioFederation.compareItems(item.local_item_id!, item)}>Compare</button>
                          )}
                          {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer">Open in Remote Workspace</a>}
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                )
              })}
              {(!showLocalScenarios || sorted.length === 0) && federationRemoteScenarioRows.length === 0 && (
                <tr>
                  <td colSpan={canManageTests ? 10 : 9} className="cell-muted">
                    {scenarioQuery.trim() || scenarioReleaseFilter !== 'all' ? 'No scenarios match these filters.' : 'No scenarios yet. Create a scenario from an existing script.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      )}

      {confirmDeleteScenarioId && (
        <div className="modal-overlay" {...dialogBackdropProps(closeDeleteScenarioDialog, 'Close scenario deletion dialog')}>
          <div
            ref={deleteScenarioDialogRef}
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="delete-scenario-dialog-title"
            aria-describedby="delete-scenario-dialog-description"
          >
            <div className="modal-header">
              <h3 id="delete-scenario-dialog-title">Delete Scenario</h3>
              <button ref={deleteScenarioInitialFocusRef} type="button" className="btn btn-secondary btn-sm" onClick={closeDeleteScenarioDialog}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p id="delete-scenario-dialog-description">Permanently delete <strong>{scenarios.find(s => s.id === confirmDeleteScenarioId)?.name || 'this scenario'}</strong>? Scenarios with linked results cannot be deleted.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={closeDeleteScenarioDialog}>Cancel</button>
                <button className="btn btn-danger" type="button" disabled={deletingScenarioIds.has(confirmDeleteScenarioId)} onClick={() => deleteScenario(confirmDeleteScenarioId)}>Delete Scenario</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default memo(TestHub)
