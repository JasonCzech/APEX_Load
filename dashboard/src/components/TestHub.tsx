import MonitorRuleEditor from './MonitorRuleEditor'
import ObjectHistory, { ActivityAttribution } from './ObjectHistory'
import ObjectArchiveControls from './ObjectArchiveControls'
import LibraryAssignmentFilters from './LibraryAssignmentFilters'
import { matchesAssignmentFilter } from './libraryAssignmentModel'
import LibraryAssignmentDialog from './LibraryAssignmentDialog'
import { assignmentSummary } from './libraryAssignmentModel'
import { useLibraryFilter } from './useLibraryFilter'
import LibraryPagination from './LibraryPagination'
import { useLibraryPagination } from './useLibraryPagination'
import RowActions from './ux/RowActions'
import BulkActions from './ux/BulkActions'
import FormField from './ux/FormField'
import FormErrorSummary from './ux/FormErrorSummary'
import TabList from './ux/TabList'
import { confirmAction } from './ux/confirmAction'
import { handleTabKey } from './tabKeyboard'
import DialogLayer from './ux/DialogLayer'
import ScriptsPage from './ScriptsPage'
import { assertDraftHasNoInlineCredentials, clearScenarioAuthoringDraft, readScenarioAuthoringDraft, saveScenarioAuthoringDraft, type ScenarioAuthoringDraft } from './scenarioAuthoringDraft'
import { scenarioScriptReadiness } from './scenarioScriptReadiness'
import { Fragment, memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { ApplicationRepository, DashboardAuthEffectiveAccessResource, DraftCapacityEstimate, GeneratorAssignmentSettings, GoalConfig, LoadModelSupportEntry, LoadModelTemplate, LoadModelValidationReport, LoadStageDraft, RunArchiveReport, RuntimeSettingsOverride, ScenarioDataPoolBinding, ScenarioMode, ScenarioRuntimeSettingsDraft, SharedDataPool, StoredScenario, StoredScript, SystemInventoryGenerator, TestSummary, TrafficProfile, TrafficProfileDeriveResponse } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatTimeCompact, isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import './TestHub.css'
import './CreateTestModal.css'
import { goalConfigFromForm, maxStageVUsers, scenarioDraftFromForm, scenarioDraftGroupFromForm, scenarioEstimateRequest, scenarioTimelineGroups, scenarioUpsertPayload } from './scenarioAssembly'
import { SortHeader } from './SortHeader'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import RuntimeSettingsGroupEditor from './RuntimeSettingsGroupEditor'
import { readImportedJavaVMParameters } from './scriptEditorImportModel'
import GroupPlacementEditor from './GroupPlacementEditor'
import { formatPacing, formatThinkTime, formatErrorHandling, overrideFromRuntimeDraft } from './runtimeSettingsModel'
import { applyEffectiveRuntimeSettingsToGroups } from './runtimeSettingsApply'
import { adaptScenarioShapeDefaults, availableScenarioModelFamilies, shapeFamilyFor, type ScenarioModelFamilyId,buildClosedVUserPlan, buildScenarioModelPreview, buildScenarioShapeCatalog, buildShapeStages, buildSteppedRampPlan, formatModelTimeLabel, getScenarioShapePreset, isGuidedClosedVUserOwned, MAX_STEPPED_RAMP_STEPS, remapClosedRampDownHistory, remapGoalDrivenIndexedState, ScenarioModelPreview, ScenarioSeed, ScenarioShapePreset, ScenarioSourceMode, ScenarioWizardStep, shapeStageStrategyFor, validateStageTimeline } from './scenarioWizardUtils'
import ReplaceScenarioScriptsModal from './ReplaceScenarioScriptsModal'
import type { ClosedVUserConfig, ClosedVUserDefinition, GoalDrivenARTConfig, GoalDrivenConfig, GoalDrivenGroupDefinition, SteppedRampConfig, SteppedRampDefinition } from '../types/goals'
import { BaselineGroupOption, baselineGroupOptions, littlesLawVUsers } from './goalSizingUtils'
import DurationField from './DurationField'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import TransactionResponseTimeSLASettings from './TransactionResponseTimeSLASettings'
import { hasGoalModeConfiguration, validateGoalConfig, validateTransactionResponseTimeSLAOverrides } from './goalConfigValidation'
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
import AuthoringRevisionConflict from './AuthoringRevisionConflict'
import { authoringChangedPaths, reapplyAuthoringDraft } from '../utils/authoringRevision'
import { adoptableAfterBindingsSave, ScenarioChangedSinceObserved, scenarioBaselineFromRead, scenarioRevisionUnsupported, scenarioSaveNeedsReconciliation } from '../utils/scenarioAuthoringRevision'
import type { ScenarioAuthoringBaseline } from '../types/storedContent'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardRunnerContext, useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { clampVUsersForRole, maxSharedVUsersForRoles, maxVUsersForRole, OBSERVER_MAX_VUSERS, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'
import {
  scenarioScriptConflict,
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
import { ScenarioCompositionTimeline, type CompositionTimelineGroup } from './ScenarioCompositionTimeline'
import TrafficProfileSourcePanel from './TrafficProfileSourcePanel'
import CSVDrivenSourcePanel from './CSVDrivenSourcePanel'
import ScenarioDataBindings from './ScenarioDataBindings'
import { ScenarioMonitorBindings } from './RunMonitoring'
import { dataBindingErrors } from './scenarioDataBindingModel'
import NewTestReadiness from './NewTestReadiness'
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
  onScriptWorkflowChange?: (active: boolean) => void
}

export interface ScenarioFormState {
  termination_mode?: 'duration' | 'iterations'
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
  runtime_think_time_type?: '' | 'none' | 'fixed' | 'recorded' | 'random_percentage' | 'gaussian' | 'uniform'
  runtime_think_time_ms?: number
  runtime_think_time_std_ms?: number
  runtime_think_time_min_ms?: number
  runtime_think_time_max_ms?: number
  runtime_think_time_min_percent?: number
  runtime_think_time_max_percent?: number
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
  /** Canonical data-pool bindings shared by editing, validation and submission. */
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
	transactionResponseTimeSlaMs?: number
}

type ScenarioSortField = 'updated_at' | 'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_test_id' | 'last_run_at' | 'last_run_ended_at' | 'result_count'
type ScriptLibrarySortField = 'name' | 'created_at' | 'last_run_at' | 'associated_scenarios' | 'associated_releases' | 'protocol'
export type ScenarioStartChoice = ScenarioSourceMode | 'goal' | ''

// Workload shape backing the production traffic-profile workflow. Its domain
// feature gates the workflow's entry points and its analyze/derive endpoints.
const PRODUCTION_REPLAY_SHAPE_ID = 'production-replay'

const scenarioBaselineMissing = 'This draft has no current scenario baseline. Load the current baseline, review the differences and reapply your draft before saving.'

// A scenario save that needs reconciliation keeps the draft. current holds
// the reviewed latest scenario once the user loads it.
interface ScenarioConflictState {
  reason: string
  loading: boolean
  current: { baseline: ScenarioAuthoringBaseline; scenario: StoredScenario; form: ScenarioFormState } | null
}

function scenarioConflictReason(error: unknown): string {
  if (error instanceof ApiError && error.status === 412) return 'Another change was saved to this scenario after your draft was loaded. Load the current baseline, review the differences and reapply your draft.'
  if (error instanceof ApiError && error.status === 428) return 'This project requires revision-checked scenario saves. Load the current baseline before saving your draft.'
  return getErrorMessage(error, 'The save outcome could not be verified. Your draft is retained; load the current baseline before saving again.')
}

function scenarioShapeId(scenario: StoredScenario): string {
  return scenario.workload_shape_id || (scenario.traffic_profile ? PRODUCTION_REPLAY_SHAPE_ID : '')
}

function parseScenarioForm(serialized: string): ScenarioFormState | null {
  try { return serialized ? JSON.parse(serialized) as ScenarioFormState : null } catch { return null }
}
const CSV_DRIVEN_SHAPE_ID = 'csv-driven'
type RunSettingsTab = 'scenario' | 'runtime' | 'infrastructure'
type ScenarioSettingsSection = 'composition' | 'stages' | 'phases' | 'advanced'

const scenarioWizardSteps: Array<{ id: ScenarioWizardStep, label: string }> = [
  { id: 'scripts', label: 'Start' },
  { id: 'source', label: 'Model & Shape' },
  { id: 'run', label: 'Run Settings' },
  { id: 'data', label: 'Data' },
  { id: 'review', label: 'Review' },
]

const SCENARIO_AUTOSAVE_DELAY_MS = 1500

const RUN_SETTINGS_STEP_INDEX = scenarioWizardSteps.findIndex(step => step.id === 'run')

const runSettingsTabs: Array<{ id: RunSettingsTab, label: string }> = [
  { id: 'scenario', label: 'Scenario Settings' },
  { id: 'runtime', label: 'Browser/Script Runtime' },
  { id: 'infrastructure', label: 'Controller/Generator' },
]

const scenarioSettingsSections: Array<{ id: ScenarioSettingsSection, label: string }> = [
  { id: 'composition', label: 'Target Load' },
  { id: 'stages', label: 'Ramp Pattern' },
  { id: 'phases', label: 'Run Phases' },
  { id: 'advanced', label: 'Advanced Settings' },
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
    // These are defaults for a new owned draft, not an edit-time replacement.
    goal_config: { early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m' } },
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

const DEFAULT_STEPPED_RAMP_DEFINITION: SteppedRampDefinition = {
  start_vusers: 0,
  end_vusers: 60,
  increment_by: 'step_count',
  step_count: 6,
  vusers_per_step: 10,
}

const DEFAULT_CLOSED_VUSER_DEFINITION: ClosedVUserDefinition = {
  peak_vusers: 5,
  ramp_up: '2m',
  sustain: '3m',
  ramp_down_enabled: false,
  ramp_down: '2m',
}

function cloneSteppedRampDefinition(value?: SteppedRampDefinition): SteppedRampDefinition {
  return { ...DEFAULT_STEPPED_RAMP_DEFINITION, ...(value || {}) }
}

function steppedRampGroupKey(_group: ScenarioFormGroup, index: number) {
  return `group_${index}`
}

function steppedRampDefinitionForGroup(config: SteppedRampConfig | undefined, group: ScenarioFormGroup, index: number) {
  return config?.groups?.[steppedRampGroupKey(group, index)] || config?.groups?.[group.name.trim()]
}

function closedVUserGroupKey(_group: ScenarioFormGroup, index: number) {
  return `group_${index}`
}

function closedVUserDefinitionFromGroup(group: ScenarioFormGroup, scenarioDuration: string): ClosedVUserDefinition {
  const rampStage = group.stages.find(stage => stage.type === 'ramp_up')
  const holdStage = group.stages.find(stage => stage.type === 'hold')
  const downStage = group.stages.find(stage => stage.type === 'ramp_down')
  const rampUp = rampStage?.duration || group.ramp_up || DEFAULT_CLOSED_VUSER_DEFINITION.ramp_up
  const rampDownEnabled = Boolean(downStage || group.ramp_down)
  const rampDown = downStage?.duration || group.ramp_down || rampUp
  const inferredSustainSeconds = Math.max(
    1,
    parseDurationToSeconds(scenarioDuration) - parseDurationToSeconds(rampUp) - (rampDownEnabled ? parseDurationToSeconds(rampDown) : 0),
  )
  return {
    peak_vusers: Math.max(1, Number(group.vusers) || DEFAULT_CLOSED_VUSER_DEFINITION.peak_vusers),
    ramp_up: rampUp,
    sustain: holdStage?.duration || formatTimeCompact(inferredSustainSeconds),
    ramp_down_enabled: rampDownEnabled,
    ramp_down: rampDown,
  }
}

function cloneClosedVUserDefinition(value?: ClosedVUserDefinition, fallback?: ClosedVUserDefinition): ClosedVUserDefinition {
  return { ...DEFAULT_CLOSED_VUSER_DEFINITION, ...(fallback || {}), ...(value || {}) }
}

function closedVUserDefinitionForGroup(config: ClosedVUserConfig | undefined, group: ScenarioFormGroup, index: number, scenarioDuration: string) {
  return config?.groups?.[closedVUserGroupKey(group, index)]
    || config?.groups?.[group.name.trim()]
    || closedVUserDefinitionFromGroup(group, scenarioDuration)
}

function goalDrivenGroupKey(index: number) {
  return `group_${index}`
}

function reconstructedGoalTarget(groups: ScenarioFormGroup[], index: number, totalTarget = 25) {
  const totalSeed = groups.reduce((sum, group) => sum + Math.max(0, Number(group.vusers) || 0), 0)
  if (totalSeed <= 0) return totalTarget / Math.max(1, groups.length)
  return totalTarget * Math.max(0, Number(groups[index]?.vusers) || 0) / totalSeed
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
      min_percent: form.runtime_think_time_min_percent,
      max_percent: form.runtime_think_time_max_percent,
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
    case 'updated_at':
      return new Date(scenario.updated_at || scenario.created_at || 0).getTime()
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
  compositionGroups,
}: {
  model: ScenarioModelPreview
  shapeLabel: string
  dataPoolLabel: string
  validationStatus: string
  compositionGroups?: CompositionTimelineGroup[]
}) {
  const groupSeries = model.series.filter(series => series.kind === 'group')
  const totalSeries = model.series.find(series => series.kind === 'total')
  const tpsSeries = model.series.filter(series => series.kind === 'tps')
  const includesTps = tpsSeries.length > 0
  const visibleSeries = model.series.filter(series => model.showVUsers || series.kind === 'tps')
  const formatTps = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 2 })

  return (
    <section className={`scenario-model-preview-panel ${compositionGroups ? 'composition-preview' : ''}`}>
      <div className="scenario-model-preview-header">
        <div>
          <h4>{compositionGroups ? 'Live scenario preview' : 'Model Preview'}</h4>
          <p>{compositionGroups ? 'The load curve updates as you edit group timing and demand.' : includesTps ? (model.showVUsers ? 'Planned TPS targets and VUsers over time, calculated from the current wizard draft.' : model.estimatedPeakVusers !== undefined ? 'Planned adaptive TPS targets. VUser demand is controlled at runtime; the ART-backed VUser figure is a Little’s Law planning estimate.' : 'Planned adaptive TPS targets. VUser demand is determined by the goal controller at runtime.') : (model.showVUsers ? 'Planned VUsers over time by script group, calculated from the current wizard draft.' : 'VUser demand is determined by the goal controller at runtime and cannot be modeled ahead of the run.')}</p>
        </div>
        <div className="scenario-model-summary">
          {model.showVUsers && <span><strong>{model.peakVusers}</strong> peak VUsers</span>}
          {model.peakTps !== undefined && <span><strong>{formatTps(model.peakTps)}</strong> peak TPS</span>}
          {model.minTps !== undefined && <span><strong>{formatTps(model.minTps)}</strong> minimum TPS</span>}
          {model.averageTps !== undefined && <span><strong>{formatTps(model.averageTps)}</strong> average TPS</span>}
          {model.estimatedPeakVusers !== undefined && <span><strong>{model.estimatedPeakVusers.toLocaleString()}</strong> estimated peak VUsers</span>}
          {model.recommendedGenerators !== undefined && model.recommendedGenerators > 0 && <span><strong>{model.recommendedGenerators}</strong> recommended generators</span>}
          <span><strong>{formatModelTimeLabel(model.durationSeconds)}</strong> duration</span>
          <span><strong>{groupSeries.length}</strong> groups</span>
          <span>{shapeLabel}</span>
          <span>{dataPoolLabel}</span>
          <span>{validationStatus}</span>
        </div>
      </div>
      {(model.showVUsers || includesTps) ? <div className="scenario-model-chart">
        <ResponsiveContainer width="100%" height="100%">
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
      {compositionGroups && <div className="scenario-composition-summary" aria-label="Live group summary">
        {compositionGroups.map((group, index) => <div className="scenario-composition-summary-row" key={`${group.script_id || 'group'}-${index}`}>
          <span className="scenario-composition-summary-name"><i style={{ background: groupSeries[index]?.color || 'var(--accent)' }} />{group.name || `Group ${index + 1}`}</span>
          <span>{group.start_offset || '0s'} start</span>
          <strong>{group.vusers} VUsers</strong>
        </div>)}
      </div>}
    </section>
  )
}

function TestHub({ onScriptWorkflowChange, applicationRepository, onSelectTest, onOpenCommandCenter, onCreateTestFromScenario, onNavigate, onScenarioViewChange, initialView = 'library', featurePolicySignal = 0 }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const [engineDefaults, setEngineDefaults] = useState<ScriptConfig | undefined>(undefined)
  const [archiveState, setArchiveState] = useState<'active' | 'archived' | 'all'>('active')
  const archiveStateRef = useRef(archiveState)
  archiveStateRef.current = archiveState
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
  useEffect(() => {
    setShowForm(initialView === 'builder')
    setShowPlans(initialView === 'plans')
  }, [initialView])
  const [planSeedScenarioId, setPlanSeedScenarioId] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  // The durable baseline the edit form was loaded from, and a save's pending
  // reconciliation. Every conditioned save presents this baseline's tag.
  const [scenarioBaseline, setScenarioBaseline] = useState<ScenarioAuthoringBaseline | null>(null)
  const [scenarioConflict, setScenarioConflict] = useState<ScenarioConflictState | null>(null)
  const [openingScenarioId, setOpeningScenarioId] = useState('')
  const scenarioOpenGenerationRef = useRef(0)
  const originalShapeIdRef = useRef('')
  const wizardPanelRef = useRef<HTMLDivElement>(null)
  const [wizardStep, setWizardStep] = useState<ScenarioWizardStep>('scripts')
  const [maxVisitedWizardIndex, setMaxVisitedWizardIndex] = useState(0)
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const navigationRunner = useNavigationGuardRunner()
  const { runGuarded } = navigationRunner
  const [scenarioSourceMode, setScenarioSourceMode] = useState<ScenarioSourceMode>('shape')
  const [scenarioStartChoice, setScenarioStartChoice] = useState<ScenarioStartChoice>('')
  const [scenarioCloneQuery, setScenarioCloneQuery] = useState('')
  const [selectedCloneScenarioId, setSelectedCloneScenarioId] = useState('')
  // A family clicked before one of its shapes is applied. Once a model is applied the
  // highlighted family is derived from it, so this is cleared on every model change.
  const [modelFamilyChoice, setModelFamilyChoice] = useState<ScenarioModelFamilyId | ''>('')
  const [pendingCloneScenario, setPendingCloneScenario] = useState<StoredScenario | null>(null)
  // Step 1 is a fork: build a new scenario from scripts, or copy a saved scenario.
  const [scenarioStartPath, setScenarioStartPath] = useState<'new' | 'copy'>('new')
  const [selectedShapeId, setSelectedShapeId] = useState('')
  const [prerequisiteScript, setPrerequisiteScript] = useState<'start-new' | 'source' | null>(null)
  const [newPoolRow, setNewPoolRow] = useState('')
  const [createdPoolId, setCreatedPoolId] = useState<string | null>(null)
  const [newPoolName, setNewPoolName] = useState('')
  const [poolCreating, setPoolCreating] = useState(false)
  const [authoringDraft, setAuthoringDraft] = useState<ScenarioAuthoringDraft | null>(() => readScenarioAuthoringDraft(activeProjectId || ''))
  const checkpointBaselineRef = useRef('')
  const activeCheckpointSavedAtRef = useRef('')
  // Set when the current browser checkpoint was written by autosave rather
  // than by an explicit Save draft, so Discard can undo only autosaves.
  const autosaveSavedAtRef = useRef('')
  // The checkpoint the first autosave replaced (a resumed or explicitly saved
  // draft of this same work), restored when the builder work is discarded.
  const autosaveReplacedRef = useRef<ScenarioAuthoringDraft | null>(null)
  const buildBrowserDraftRef = useRef<(() => ScenarioAuthoringDraft) | null>(null)
  const saveAuthoringRef = useRef<() => Promise<boolean>>(async () => false)
  const [form, setForm] = useState<ScenarioFormState>(() => emptyScenarioForm())
  const currentDraftRef = useRef({ form, activeProjectId, showForm })
  currentDraftRef.current = { form, activeProjectId, showForm }
  const markWizardStepVisited = useCallback((stepId: ScenarioWizardStep) => {
    const stepIndex = scenarioWizardSteps.findIndex(step => step.id === stepId)
    if (stepIndex >= 0) {
      setMaxVisitedWizardIndex(current => Math.max(current, stepIndex))
    }
  }, [])
  const [error, setError] = useState('')
  const [monitorEditorError, setMonitorEditorError] = useState('')
  const [savePending, setSavePending] = useState(false)
  const savingRef = useRef(false)
  const [savedNotice, setSavedNotice] = useState('')
  const [runtimeScope, setRuntimeScope] = useState('defaults')
  const [reviewScriptId, setReviewScriptId] = useState<string | null>(null)
  useEffect(() => {
    onScriptWorkflowChange?.(Boolean(prerequisiteScript || reviewScriptId))
    return () => onScriptWorkflowChange?.(false)
  }, [onScriptWorkflowChange, prerequisiteScript, reviewScriptId])
  const errorRef = useRef<HTMLDivElement>(null)
  useEffect(() => { if (error) errorRef.current?.focus() }, [error])
  const filterScope = `scenarios:${activeProject?.domain_id || ''}:${activeProjectId || ''}`
  const [scenarioQuery, setScenarioQuery] = useLibraryFilter<string>(filterScope, 'query', '')
  const [assignedApplicationFilter, setAssignedApplicationFilter] = useLibraryFilter<string>(filterScope, 'assignedApplication', 'all')
  const [assignedReleaseFilter, setAssignedReleaseFilter] = useLibraryFilter<string>(filterScope, 'assignedRelease', 'all')
  const [scenarioReleaseFilter, setScenarioReleaseFilter] = useLibraryFilter<string>(filterScope, 'release', 'all')
  const [scriptLibraryQuery, setScriptLibraryQuery] = useState('')
  const [scriptReadinessFilter, setScriptReadinessFilter] = useState('all')
  const [scriptProtocolFilter, setScriptProtocolFilter] = useState('all')
  const [pickedScriptIds, setPickedScriptIds] = useState<string[]>([])
  const [scriptLibrarySortField, setScriptLibrarySortField] = useState<ScriptLibrarySortField>('name')
  const [scriptLibrarySortDirection, setScriptLibrarySortDirection] = useState<'asc' | 'desc'>('asc')
  const [scriptSelectionTargetIndex, setScriptSelectionTargetIndex] = useState<number | null>(null)
  const [sortField, setSortField] = useState<ScenarioSortField>('updated_at')
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
  const [assignmentScenarios, setAssignmentScenarios] = useState<StoredScenario[] | null>(null)
  const [assigningScenarioIds, setAssigningScenarioIds] = useState<Set<string>>(new Set())
  const [assignmentNotice, setAssignmentNotice] = useState('')
  const [deletingScenarioIds, setDeletingScenarioIds] = useState<Set<string>>(new Set())
  // Goal-Driven worksheet: completed-baseline import for per-group ART.
  const [goalBaselineTests, setGoalBaselineTests] = useState<TestSummary[]>([])
  const [goalBaselineId, setGoalBaselineId] = useState('')
  const [goalBaselineReports, setGoalBaselineReports] = useState<Record<string, RunArchiveReport>>({})
  const [goalBaselineLoadingId, setGoalBaselineLoadingId] = useState('')
  const [goalSizingError, setGoalSizingError] = useState('')
  const [goalArtModes, setGoalArtModes] = useState<Record<string, 'manual' | 'baseline'>>({})
  const [goalArtUnits, setGoalArtUnits] = useState<Record<string, 'ms' | 's'>>({})
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [stageGroupIndex, setStageGroupIndex] = useState(0)
  const [runtimeGroupIndex, setRuntimeGroupIndex] = useState(0)
  const [runSettingsTab, setRunSettingsTab] = useState<RunSettingsTab>('scenario')
  const [scenarioSettingsSection, setScenarioSettingsSection] = useState<ScenarioSettingsSection>('composition')
  const scriptLibraryDialogRef = useRef<HTMLDivElement | null>(null)
  const scriptLibrarySearchRef = useRef<HTMLInputElement | null>(null)
  const scriptGroupRefs = useRef<Array<HTMLDivElement | null>>([])
  const deleteScenarioDialogRef = useRef<HTMLDivElement | null>(null)
  const deleteScenarioInitialFocusRef = useRef<HTMLButtonElement | null>(null)
  const editingBaselineFormRef = useRef('')
  // The tag a resumed edit recorded, kept until a baseline is adopted or the
  // edit is replaced, so a re-checkpoint while blocked still carries it.
  const resumedBaselineTagRef = useRef('')
  // The update time of the scenario body the edit baseline was built from: a
  // checkpoint without a tag records it, never a later list row's.
  const editingBaselineUpdatedAtRef = useRef('')
  const closedRampDownEnabledOnceRef = useRef<Set<string>>(new Set())
  const goalGuardrailsEditedRef = useRef(false)
  const resetScenarioBuilderRef = useRef<() => void>(() => undefined)
  const refreshRef = useRef<() => Promise<void>>(async () => undefined)
  const isScenarioBuilderDirtyRef = useRef<() => boolean>(() => false)
  const currentProjectIdRef = useRef(activeProjectId)
  const refreshGenerationRef = useRef(0)
  const scenarioDraftGenerationRef = useRef(0)
  const goalBaselineSelectionGenerationRef = useRef(0)
  const effectiveAccessScopeKey = `${activeProjectId || ''}\u0000${activeProject?.domain_id || ''}\u0000${applicationRepository?.id || ''}`
  const effectiveAccessScopeKeyRef = useRef(effectiveAccessScopeKey)
  effectiveAccessScopeKeyRef.current = effectiveAccessScopeKey
  if (currentProjectIdRef.current !== activeProjectId) {
    setPrerequisiteScript(null)
    setCreatedPoolId(null)
    setNewPoolName('')
    setNewPoolRow('')
    currentProjectIdRef.current = activeProjectId
    refreshGenerationRef.current += 1
    scenarioDraftGenerationRef.current += 1
    goalBaselineSelectionGenerationRef.current += 1
  }
  const closeDeleteScenarioDialog = useCallback(() => setConfirmDeleteScenarioId(null), [])
  const closeScriptLibraryDialog = useCallback(() => setScriptSelectionTargetIndex(null), [])
  const scriptLibraryDialogOpen = scriptSelectionTargetIndex !== null && prerequisiteScript === null && reviewScriptId === null
  useModalDialog({
    dialogRef: deleteScenarioDialogRef,
    enabled: Boolean(confirmDeleteScenarioId),
    initialFocusRef: deleteScenarioInitialFocusRef,
    onClose: closeDeleteScenarioDialog,
  })
  useModalDialog({
    dialogRef: scriptLibraryDialogRef,
    enabled: scriptLibraryDialogOpen,
    initialFocusRef: scriptLibrarySearchRef,
    onClose: closeScriptLibraryDialog,
  })

  useLayoutEffect(() => {
    setAuthoringDraft(readScenarioAuthoringDraft(activeProjectId || ''))
    setPickedScriptIds([]); setScriptReadinessFilter('all'); setScriptProtocolFilter('all'); setScriptLibraryQuery('')
    checkpointBaselineRef.current = ''
    activeCheckpointSavedAtRef.current = ''
    setScenarios([])
    setAssignmentScenarios(null)
    setAssigningScenarioIds(new Set())
    setAssignmentNotice('')
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
    setGoalBaselineReports({})
    setGoalBaselineLoadingId('')
    setGoalSizingError('')
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
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = ''
    setScenarioBaseline(null)
    setScenarioConflict(null)
    setForm(emptyScenarioForm())
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setModelFamilyChoice('')
    setPendingCloneScenario(null)
    setScenarioStartPath('new')
    setScenarioCloneQuery('')
    setWizardStep('scripts')
    setMaxVisitedWizardIndex(0)
    setStageGroupIndex(0)
    setRuntimeGroupIndex(0)
    setRuntimeScope('defaults')
    setRunSettingsTab('scenario')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setPlanSeedScenarioId(null)
  }, [activeProjectId])
  const formGroups = useMemo(
    () => form.groups.length > 0 ? form.groups : [emptyScenarioGroup()],
    [form.groups],
  )
  const compositionTimelineGroups = useMemo(() => scenarioTimelineGroups(formGroups.map(scenarioDraftGroupFromForm)), [formGroups])
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
	const isSteppedRamp = selectedShapeId === 'stepped-ramp'
	const isClosedVUser = selectedShapeId === 'closed-vuser'
	const isGoalDriven = selectedShapeId === 'open-arrival-rate' || selectedShapeId === 'goal-based'
	const goalDrivenStoredConfig = form.goal_config?.workload_shape_config?.goal_driven
	const goalDrivenConfig: GoalDrivenConfig = useMemo(() => {
		if (goalDrivenStoredConfig) return goalDrivenStoredConfig
		return {
			groups: Object.fromEntries(formGroups.map((_, index) => [goalDrivenGroupKey(index), {
				target_tps: reconstructedGoalTarget(formGroups, index, form.goal_target_average_tps || 25),
			}]))
		}
	}, [form.goal_target_average_tps, formGroups, goalDrivenStoredConfig])
	const activeGoalDrivenDefinition: GoalDrivenGroupDefinition = goalDrivenConfig.groups[goalDrivenGroupKey(activeGroupIndex)] || {
		target_tps: reconstructedGoalTarget(formGroups, activeGroupIndex, form.goal_target_average_tps || 25),
	}
	const goalDrivenEstimatedVUsers = formGroups.map((group, index) => {
		const definition = goalDrivenConfig.groups[goalDrivenGroupKey(index)]
		const artMs = Number(definition?.art?.art_ms)
		return artMs > 0
			? littlesLawVUsers(definition.target_tps, artMs)
			: Math.max(1, Number(group.vusers) || 1)
	})
	const totalGoalDrivenEstimatedVUsers = goalDrivenEstimatedVUsers.reduce((sum, value) => sum + value, 0)
	const goalDrivenBlockingMessage = useMemo(() => {
		if (!isGoalDriven) return ''
		for (let index = 0; index < formGroups.length; index += 1) {
			const definition = goalDrivenConfig.groups[goalDrivenGroupKey(index)]
			const label = formGroups[index].name.trim() || `Group ${index + 1}`
			if (!definition || !Number.isFinite(definition.target_tps) || definition.target_tps <= 0) return `${label}: Target TPS must be greater than zero.`
			if (definition.art && (!Number.isFinite(definition.art.art_ms) || definition.art.art_ms <= 0)) return `${label}: ART must be greater than zero.`
		}
		if (form.goal_min_vusers !== undefined && form.goal_min_vusers < 1) return 'Minimum VUsers must be at least 1.'
		if (form.goal_max_vusers !== undefined && form.goal_max_vusers < (form.goal_min_vusers || 1)) return 'Maximum VUsers must be at least the minimum.'
		return ''
	}, [form.goal_max_vusers, form.goal_min_vusers, formGroups, goalDrivenConfig.groups, isGoalDriven])
	const closedVUserStoredConfig = form.goal_config?.workload_shape_config?.closed_vuser
	const isGuidedClosedVUser = isClosedVUser && isGuidedClosedVUserOwned(Boolean(closedVUserStoredConfig), formGroups)
	const steppedRampConfig: SteppedRampConfig = useMemo(
		() => form.goal_config?.workload_shape_config?.stepped_ramp || {
			scope: 'scenario',
			scenario: cloneSteppedRampDefinition(),
		},
		[form.goal_config?.workload_shape_config?.stepped_ramp],
	)
	const activeSteppedRampDefinition = steppedRampConfig.scope === 'group'
		? cloneSteppedRampDefinition(steppedRampDefinitionForGroup(steppedRampConfig, activeStageGroup, activeGroupIndex))
		: cloneSteppedRampDefinition(steppedRampConfig.scenario)
	const activeSteppedRampPlan = buildSteppedRampPlan({
		durationSeconds: parseDurationToSeconds(form.duration),
		startVusers: activeSteppedRampDefinition.start_vusers,
		endVusers: activeSteppedRampDefinition.end_vusers,
		incrementBy: activeSteppedRampDefinition.increment_by,
		stepCount: activeSteppedRampDefinition.step_count,
		vusersPerStep: activeSteppedRampDefinition.vusers_per_step,
	})
	const steppedRampBlockingMessage = useMemo(() => {
		if (!isSteppedRamp) return ''
		const definitions = steppedRampConfig.scope === 'group'
			? formGroups.map((group, index) => ({
				label: group.name.trim() || `Group ${index + 1}`,
				definition: cloneSteppedRampDefinition(steppedRampDefinitionForGroup(steppedRampConfig, group, index)),
			}))
			: [{ label: 'Scenario', definition: cloneSteppedRampDefinition(steppedRampConfig.scenario) }]
		for (const { label, definition } of definitions) {
			const plan = buildSteppedRampPlan({
				durationSeconds: parseDurationToSeconds(form.duration),
				startVusers: definition.start_vusers,
				endVusers: definition.end_vusers,
				incrementBy: definition.increment_by,
				stepCount: definition.step_count,
				vusersPerStep: definition.vusers_per_step,
			})
			if (plan.error) return `${label}: ${plan.error}`
		}
		return ''
	}, [form.duration, formGroups, isSteppedRamp, steppedRampConfig])
	const closedVUserConfig: ClosedVUserConfig = useMemo(
		() => closedVUserStoredConfig || {
			scope: 'scenario',
			scenario: cloneClosedVUserDefinition(undefined, closedVUserDefinitionFromGroup(formGroups[0], form.duration)),
		},
		[closedVUserStoredConfig, form.duration, formGroups],
	)
	const activeClosedVUserDefinition = closedVUserConfig.scope === 'group'
		? cloneClosedVUserDefinition(closedVUserDefinitionForGroup(closedVUserConfig, activeStageGroup, activeGroupIndex, form.duration))
		: cloneClosedVUserDefinition(closedVUserConfig.scenario, closedVUserDefinitionFromGroup(activeStageGroup, form.duration))
	const activeClosedVUserPlan = buildClosedVUserPlan({
		peakVusers: activeClosedVUserDefinition.peak_vusers,
		rampUp: activeClosedVUserDefinition.ramp_up,
		sustain: activeClosedVUserDefinition.sustain,
		rampDownEnabled: activeClosedVUserDefinition.ramp_down_enabled,
		rampDown: activeClosedVUserDefinition.ramp_down,
	})
	const closedVUserBlockingMessage = useMemo(() => {
		if (!isGuidedClosedVUser) return ''
		const definitions = closedVUserConfig.scope === 'group'
			? formGroups.map((group, index) => ({
				label: group.name.trim() || `Group ${index + 1}`,
				definition: cloneClosedVUserDefinition(closedVUserDefinitionForGroup(closedVUserConfig, group, index, form.duration)),
			}))
			: [{ label: 'Scenario', definition: cloneClosedVUserDefinition(closedVUserConfig.scenario) }]
		for (const { label, definition } of definitions) {
			const plan = buildClosedVUserPlan({
				peakVusers: definition.peak_vusers,
				rampUp: definition.ramp_up,
				sustain: definition.sustain,
				rampDownEnabled: definition.ramp_down_enabled,
				rampDown: definition.ramp_down,
			})
			if (plan.error) return `${label}: ${plan.error}`
		}
		return ''
	}, [closedVUserConfig, form.duration, formGroups, isGuidedClosedVUser])
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
  const canManageScripts = isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_scripts')
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
  const modelFamilies = useMemo(
    () => availableScenarioModelFamilies(loadModelSupportMatrix.length === 0 ? null : new Set(loadModelSupportMatrix.map(shape => shape.id))),
    [loadModelSupportMatrix],
  )
  const derivedModelFamily: ScenarioModelFamilyId | null = scenarioSourceMode === 'traffic_profile' || scenarioSourceMode === 'csv_driven'
    ? 'traffic-profile'
    : scenarioStartChoice === 'goal' ? 'goal' : shapeFamilyFor(selectedShapeId)
  const activeModelFamily = modelFamilies.find(family => family.id === (modelFamilyChoice || derivedModelFamily)) || null
  const selectedTemplate = loadModelTemplates.find(template => template.id === selectedLoadModelTemplateId)
  const selectedCloneScenario = useMemo(
    () => scenarios.find(scenario => scenario.id === selectedCloneScenarioId) || null,
    [scenarios, selectedCloneScenarioId],
  )
  const scenarioModel = useMemo(
    () => buildScenarioModelPreview({
      duration: form.duration,
      scenarioMode: form.scenario_mode,
      groups: formGroups.map((group, index) => ({
        name: group.name || findScriptById(scripts, group.script_id)?.name || undefined,
        vusers: Number(group.vusers) || 0,
        ramp_up: group.ramp_up,
        ramp_down: group.ramp_down,
        stages: group.stages,
				stop_after_stages: isGuidedClosedVUser,
				target_tps: isGoalDriven ? goalDrivenConfig.groups[goalDrivenGroupKey(index)]?.target_tps : undefined,
      })),
      goalTargetTps: form.goal_target_average_tps,
      trafficProfile: form.traffic_profile,
    }),
	[form.duration, form.goal_target_average_tps, form.scenario_mode, form.traffic_profile, formGroups, goalDrivenConfig.groups, isGoalDriven, isGuidedClosedVUser, scripts],
  )
  const filteredCloneScenarios = useMemo(() => {
    const query = scenarioCloneQuery.trim().toLowerCase()
    if (!query) return scenarios
    return scenarios.filter(scenario => scenarioSearchText(scenario).includes(query))
  }, [scenarioCloneQuery, scenarios])
  const visibleScriptLibrary = useMemo(() => {
    const query = scriptLibraryQuery.trim().toLowerCase()
    const filtered = scripts.filter(script => !script.archived_at && (!query || scriptLibrarySearchText(script).includes(query))
      && (scriptProtocolFilter === 'all' || scriptProtocols(script).includes(scriptProtocolFilter))
      && (scriptReadinessFilter === 'all' || scenarioScriptReadiness(script).blocked === (scriptReadinessFilter === 'blocked')))
    return [...filtered].sort((left, right) => {
      const leftValue = getScriptLibrarySortValue(left, scriptLibrarySortField)
      const rightValue = getScriptLibrarySortValue(right, scriptLibrarySortField)
      const direction = scriptLibrarySortDirection === 'asc' ? 1 : -1
      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * direction
      }
      return String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true, sensitivity: 'base' }) * direction
    })
  }, [scriptLibraryQuery, scriptLibrarySortDirection, scriptLibrarySortField, scripts, scriptReadinessFilter, scriptProtocolFilter])
  const showScenarioLibrary = !showForm && !showPlans
  const sourcePending = scenarioSourceMode === 'csv_driven'
    ? selectedShapeId !== CSV_DRIVEN_SHAPE_ID || !form.traffic_profile
    : scenarioSourceMode === 'traffic_profile'
      ? selectedShapeId !== PRODUCTION_REPLAY_SHAPE_ID || !form.traffic_profile
      : scenarioSourceMode === 'existing'
        ? !selectedCloneScenarioId
        : Boolean(scenarioStartChoice && !selectedShapeId && !selectedLoadModelTemplateId)
  const sourcePendingMessage = 'Choose and apply a workload source before continuing. Previous settings are retained until you apply a replacement.'
  const scriptReadiness = useMemo(() => new Map(scripts.map(script => [script.id, scenarioScriptReadiness(script)])), [scripts])
  const blockedScriptGroups = formGroups.filter(group => group.script_id && (!scriptReadiness.has(group.script_id) || scriptReadiness.get(group.script_id)?.blocked))
  useEffect(() => {
    if (!showForm) return
    const panel = wizardPanelRef.current
    panel?.focus({ preventScroll: true })
    panel?.scrollIntoView?.({ block: 'start' })
  }, [wizardStep, showForm])

  const showScriptLibrary = showForm && wizardStep === 'scripts'
  const showModelPreview = showForm && wizardStep !== 'scripts' && !sourcePending && (wizardStep !== 'source' || Boolean(scenarioStartChoice))
  const showCompositionPreview = wizardStep === 'run' && runSettingsTab === 'scenario' && form.scenario_mode === 'vuser_driven' && !isSteppedRamp && !isGuidedClosedVUser && !isGoalDriven

  const scenarioBindings: ScenarioDataPoolBinding[] = (form.data_pools || []).map(binding => ({ ...binding, access_mode: 'read_only' }))
  const bindingErrors = dataBindingErrors(scenarioBindings)
  const updateDataBindings = (bindings: ScenarioDataPoolBinding[]) => setForm(current => ({ ...current, data_pools: bindings.map(binding => ({...binding})) }))
  const buildScenarioDraftPayload = useCallback((draftForm: ScenarioFormState = form, shapeId: string = selectedShapeId) => (
    scenarioUpsertPayload(scenarioDraftFromForm(draftForm, { includeBrowser: showBrowserRuntimeSettings }), { projectId: activeProjectId, workloadShapeId: shapeId, fallbackName: 'Draft scenario' })
  ), [activeProjectId, form, selectedShapeId, showBrowserRuntimeSettings])

  const refresh = useCallback(async () => {
    const requestedArchiveState = archiveStateRef.current
    const projectId = activeProjectId
    const requestGeneration = ++refreshGenerationRef.current
    if (!projectId) return
    try {
      const [scenarioRes, scriptRes, dataPoolRes, templateRes, supportRes] = await Promise.all([
        requestedArchiveState === 'active' ? api.listScenarios(projectId) : api.listScenarios(projectId, requestedArchiveState),
        api.listScripts(projectId, undefined, { archiveState: 'all' }),
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

  useEffect(() => { void refresh() }, [featurePolicySignal, refresh, archiveState])

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
  const csvDrivenShapeEnabled = useMemo(
    () => loadModelSupportMatrix.length === 0 || loadModelSupportMatrix.some(shape => shape.id === CSV_DRIVEN_SHAPE_ID),
    [loadModelSupportMatrix],
  )

  useEffect(() => {
    // A scenario saved before the shape was disabled keeps its profile and stays
    // editable; only an in-progress draft is moved off the hidden workflow.
    if (trafficProfileShapeEnabled || form.traffic_profile) return
    setScenarioSourceMode(current => (current === 'traffic_profile' ? 'shape' : current))
    setScenarioStartChoice(current => (current === 'traffic_profile' ? 'shape' : current))
  }, [form.traffic_profile, trafficProfileShapeEnabled])
  useEffect(() => {
    if (csvDrivenShapeEnabled || selectedShapeId === CSV_DRIVEN_SHAPE_ID) return
    setScenarioSourceMode(current => (current === 'csv_driven' ? 'shape' : current))
    setScenarioStartChoice(current => (current === 'csv_driven' ? 'shape' : current))
  }, [csvDrivenShapeEnabled, selectedShapeId])
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
    setRuntimeGroupIndex(current => Math.min(current, Math.max(0, form.groups.length - 1)))
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
      api.estimateTest(scenarioEstimateRequest(scenarioDraftFromForm(form, { includeBrowser: showBrowserRuntimeSettings }), { projectId: activeProjectId }))
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
  }, [activeProjectId, api, form, showBrowserRuntimeSettings, showForm])

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
      if (!matchesAssignmentFilter(scenario.library_assignment, assignedApplicationFilter, assignedReleaseFilter)) return false
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
  }, [scenarios, scenarioQuery, scenarioReleaseFilter, sortField, sortDirection, assignedApplicationFilter, assignedReleaseFilter])

  const scenarioFederation = useLibraryFederation({
    kind: 'scenario',
    projectId: activeProjectId,
    onRefreshLocal: refresh,
    requireProjectScope: true,
  })

  const knownScenarioIds = useMemo(() => new Set(scenarios.map(scenario => scenario.id)), [scenarios])

  const federationRemoteScenarioRows = useMemo(() => {
    if (scenarioFederation.sourceFilter === 'local') return []
    if (scenarioReleaseFilter !== 'all' || assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all') return []
    const query = scenarioQuery.trim().toLowerCase()
    return scenarioFederation.remoteItems.filter(item => {
      if (scenarioFederation.sourceFilter === 'all' && item.local_item_id && knownScenarioIds.has(item.local_item_id)) return false
      if (!query) return true
      return [item.name, item.protocol, item.project_id].filter(Boolean).join(' ').toLowerCase().includes(query)
    })
  }, [scenarioFederation.remoteItems, scenarioFederation.sourceFilter, knownScenarioIds, scenarioQuery, scenarioReleaseFilter, assignedApplicationFilter, assignedReleaseFilter])

  const showLocalScenarios = scenarioFederation.sourceFilter !== 'host'
  const visibleScenarioIds = useMemo(
    () => showLocalScenarios ? sorted.map(scenario => scenario.id) : [],
    [showLocalScenarios, sorted],
  )
  const scenarioPagination = useLibraryPagination(visibleScenarioIds.length + federationRemoteScenarioRows.length, JSON.stringify([activeProjectId, visibleScenarioIds, federationRemoteScenarioRows.map(item => item.id)]))
  const pageScenarios = showLocalScenarios ? sorted.slice(scenarioPagination.start, scenarioPagination.end) : []
  const pageRemoteScenarios = federationRemoteScenarioRows.slice(Math.max(0, scenarioPagination.start - visibleScenarioIds.length), Math.max(0, scenarioPagination.end - visibleScenarioIds.length))
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
    activeCheckpointSavedAtRef.current = ''
    autosaveSavedAtRef.current = ''
    autosaveReplacedRef.current = null
    scenarioDraftGenerationRef.current += 1
    setGoalSizingError('')
    setGoalBaselineLoadingId('')
    setCreatedPoolId(null)
    setNewPoolName('')
    setNewPoolRow('')
    setPoolCreating(false)
    goalGuardrailsEditedRef.current = false
    setEditingId(null)
    editingBaselineFormRef.current = ''
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = ''
    scenarioOpenGenerationRef.current += 1
    setOpeningScenarioId('')
    setScenarioBaseline(null)
    setScenarioConflict(null)
    setForm(emptyScenarioForm())
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setModelFamilyChoice('')
    setPendingCloneScenario(null)
    setScenarioStartPath('new')
    setGoalArtModes({})
    setGoalArtUnits({})
    setGoalBaselineId('')
    setWizardStep('scripts')
    setMaxVisitedWizardIndex(0)
    setStageGroupIndex(0)
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setRuntimeScope('defaults')
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
    setSavedNotice('')
    setShowForm(true)
    setShowPlans(false)
    onScenarioViewChange?.('builder')
  }, [canManageTests, onScenarioViewChange, resetBuilderToBlank])

  // Discard the in-progress draft and start a fresh blank scenario (stays in the builder).
  // Discarding builder work also undoes its autosaves, or it would come back as
  // "Resume draft": the checkpoint autosave replaced (an explicit Save draft
  // or a resumed draft) is put back, otherwise the autosave is removed.
  const discardAutosavedCheckpoint = useCallback(() => {
    const project = currentProjectIdRef.current || ''
    const saved = autosaveSavedAtRef.current
    const replaced = autosaveReplacedRef.current
    autosaveSavedAtRef.current = ''
    autosaveReplacedRef.current = null
    if (!saved) return
    const latest = readScenarioAuthoringDraft(project)
    if (!latest || latest.savedAt !== saved) return
    try {
      if (replaced) saveScenarioAuthoringDraft(replaced)
      else clearScenarioAuthoringDraft(project)
      setAuthoringDraft(replaced)
    } catch { /* best effort */ }
  }, [])

  const startBlankScenario = useCallback(async () => {
    if (!(await confirmAction('Discard this in-progress scenario draft and start over with a blank builder?'))) return
    clearScenarioBuilderDraft()
    discardAutosavedCheckpoint()
    resetBuilderToBlank()
  }, [discardAutosavedCheckpoint, resetBuilderToBlank])

  const openScenarioLibrary = useCallback(() => {
    resetBuilderToBlank()
    setShowForm(false)
    setShowPlans(false)
    setConfirmDeleteScenarioId(null)
    onScenarioViewChange?.('library')
  }, [onScenarioViewChange, resetBuilderToBlank])

  const openExecutionPlans = useCallback(() => {
    resetBuilderToBlank()
    setShowForm(false)
    setShowPlans(true)
    onScenarioViewChange?.('plans')
  }, [onScenarioViewChange, resetBuilderToBlank])

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

  // openEditFrom binds the builder to scenario as loaded with baseline, the
  // durable revision that scenario body describes.
  const openEditFrom = (scenario: StoredScenario, baseline: ScenarioAuthoringBaseline | null) => {
    resetBuilderToBlank()
    const fallbackScriptId = scripts[0]?.id || ''
    const nextForm = scenarioFormStateFromStoredScenario(scenario, fallbackScriptId)
    const storedClosed = nextForm.goal_config?.workload_shape_config?.closed_vuser
    const storedGoalDriven = nextForm.goal_config?.workload_shape_config?.goal_driven
    goalGuardrailsEditedRef.current = nextForm.scenario_mode === 'goal_oriented'
      && (nextForm.goal_min_vusers !== undefined || nextForm.goal_max_vusers !== undefined)
    setGoalArtModes(Object.fromEntries(Object.entries(storedGoalDriven?.groups || {}).map(([key, definition]) => [key, definition.art?.basis || 'manual'])))
    setGoalArtUnits({})
    const storedBaselineId = Object.values(storedGoalDriven?.groups || {}).find(definition => definition.art?.basis === 'baseline')?.art?.baseline_test_id || ''
    setGoalBaselineId(storedBaselineId)
    if (storedBaselineId) void loadCompletedGoalBaselines()
    closedRampDownEnabledOnceRef.current = new Set(storedClosed
      ? storedClosed.scope === 'group' ? Object.keys(storedClosed.groups || {}) : ['scenario']
      : [])
    setEditingId(scenario.id)
    editingBaselineFormRef.current = JSON.stringify(nextForm)
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = scenario.updated_at || ''
    setScenarioBaseline(baseline)
    setForm(nextForm)
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    originalShapeIdRef.current = scenarioShapeId(scenario)
    setSelectedShapeId(originalShapeIdRef.current)
    setSelectedCloneScenarioId(scenario.id)
    const storedSourceMode: ScenarioSourceMode = scenario.workload_shape_id === CSV_DRIVEN_SHAPE_ID ? 'csv_driven' : scenario.traffic_profile ? 'traffic_profile' : 'existing'
    setScenarioSourceMode(storedSourceMode)
    setScenarioStartChoice(storedSourceMode)
    setModelFamilyChoice('')
    setScenarioStartPath('new')
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setRuntimeScope('defaults')
    setRunSettingsTab('scenario')
    setWizardStep('scripts')
    setMaxVisitedWizardIndex(scenarioWizardSteps.findIndex(step => step.id === 'source'))
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(true)
    onScenarioViewChange?.('builder')
  }

  // Editing reads the scenario first: the form is built from the same durable
  // body as the baseline whose tag its save presents, never from a list row
  // that may be stale.
  const openEdit = async (scenario: StoredScenario) => {
    if (assigningScenarioIds.has(scenario.id) || openingScenarioId) return
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    const generation = ++scenarioOpenGenerationRef.current
    const scopeProjectId = activeProjectId
    setOpeningScenarioId(scenario.id)
    try {
      const fresh = await api.getScenario(scenario.id)
      if (generation !== scenarioOpenGenerationRef.current || currentProjectIdRef.current !== scopeProjectId) return
      if (!fresh?.id || fresh.id !== scenario.id) throw new Error('The controller returned an incomplete scenario. Refresh the library and try again.')
      let baseline: ScenarioAuthoringBaseline | null = null
      try { baseline = scenarioBaselineFromRead(fresh, scopeProjectId || undefined) } catch { baseline = null }
      openEditFrom(fresh, baseline)
      if (!baseline) setScenarioConflict({ reason: scenarioRevisionUnsupported, loading: false, current: null })
    } catch (err: unknown) {
      if (generation === scenarioOpenGenerationRef.current && currentProjectIdRef.current === scopeProjectId) setError(getErrorMessage(err, 'Unable to load the scenario for editing'))
    } finally {
      if (generation === scenarioOpenGenerationRef.current) setOpeningScenarioId('')
    }
  }

  const duplicateScenario = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    openEditFrom(scenario, null)
    setEditingId(null)
    editingBaselineFormRef.current = ''
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = ''
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
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    if (mode !== scenarioSourceMode) {
      setSelectedCloneScenarioId('')
      setSelectedLoadModelTemplateId('')
      setSelectedShapeId('')
    }
    setModelFamilyChoice('')
    setWizardStep(mode === 'existing' ? 'scripts' : 'source')
  }

  // Script and group fixes live on the New Scenario path of the Start step.
  const openScriptComposition = () => {
    setScenarioStartPath('new')
    setWizardStep('scripts')
  }

  const selectModelFamily = (familyId: ScenarioModelFamilyId) => {
    if (familyId === 'traffic-profile' && scenarioSourceMode !== 'traffic_profile' && scenarioSourceMode !== 'csv_driven') {
      selectScenarioSourceMode(trafficProfileShapeEnabled ? 'traffic_profile' : 'csv_driven')
    }
    setModelFamilyChoice(familyId)
  }

  // Copying brings the source scenario's scripts. When the draft already has scripts,
  // the operator decides whether they are replaced or kept.
  const requestCloneScenario = (scenario: StoredScenario) => {
    const incoming = scenarioFormStateFromStoredScenario(scenario, scripts[0]?.id || '', true)
    if (scenarioScriptConflict(form.groups, incoming.groups)) {
      setPendingCloneScenario(scenario)
      return
    }
    cloneScenarioDraft(scenario, 'replace')
  }

  const cloneScenarioDraft = (scenario: StoredScenario, scriptChoice: 'replace' | 'keep') => {
    setPendingCloneScenario(null)
    const fallbackScriptId = scripts[0]?.id || ''
    const clonedForm = scenarioFormStateFromStoredScenario(scenario, fallbackScriptId, true)
    const storedClosed = clonedForm.goal_config?.workload_shape_config?.closed_vuser
    const storedGoalDriven = clonedForm.goal_config?.workload_shape_config?.goal_driven
    goalGuardrailsEditedRef.current = clonedForm.scenario_mode === 'goal_oriented'
      && (clonedForm.goal_min_vusers !== undefined || clonedForm.goal_max_vusers !== undefined)
    setGoalArtModes(Object.fromEntries(Object.entries(storedGoalDriven?.groups || {}).map(([key, definition]) => [key, definition.art?.basis || 'manual'])))
    setGoalArtUnits({})
    const storedBaselineId = Object.values(storedGoalDriven?.groups || {}).find(definition => definition.art?.basis === 'baseline')?.art?.baseline_test_id || ''
    setGoalBaselineId(storedBaselineId)
    if (storedBaselineId) void loadCompletedGoalBaselines()
    closedRampDownEnabledOnceRef.current = new Set(storedClosed
      ? storedClosed.scope === 'group' ? Object.keys(storedClosed.groups || {}) : ['scenario']
      : [])
    setEditingId(null)
    editingBaselineFormRef.current = ''
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = ''
    setForm(current => scriptChoice === 'keep'
      ? mergeCloneWithScriptSelections(current, clonedForm)
      : { ...clonedForm, name: current.name.trim() ? current.name : clonedForm.name })
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId(scenario.workload_shape_id || (scenario.traffic_profile ? PRODUCTION_REPLAY_SHAPE_ID : ''))
    setSelectedCloneScenarioId(scenario.id)
    const clonedSourceMode: ScenarioSourceMode = scenario.workload_shape_id === CSV_DRIVEN_SHAPE_ID ? 'csv_driven' : scenario.traffic_profile ? 'traffic_profile' : 'existing'
    setScenarioSourceMode(clonedSourceMode)
    setScenarioStartChoice(clonedSourceMode)
    setScriptSelectionTargetIndex(null)
    setRuntimeScope('defaults')
    setRunSettingsTab('scenario')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setModelFamilyChoice('')
    markWizardStepVisited('source')
    void loadScenarioRuns(scenario)
  }

  // Single form-seeding path shared by the starter-template button and the workload-shape
  // catalog. Shape-controlled fields are refreshed while authored per-group script,
  // role, initialization, iteration, runtime, and placement settings are retained.
  const seedFormFromScenario = (seed: ScenarioSeed) => {
    setForm(prev => {
      const replacesRuntime = seed.runtime_settings !== undefined
      // A shape owns its demand bounds and workload configuration. Other authored
      // guardrails survive unless the seed explicitly replaces that field.
      const authoredGuardrails = goalConfigFromForm(prev)
      delete authoredGuardrails.target_average_tps
      delete authoredGuardrails.min_vusers
      delete authoredGuardrails.max_vusers
      delete authoredGuardrails.workload_shape_config
      const seededGoal: GoalConfig = { ...authoredGuardrails, ...structuredClone(seed.goal || {}) }
      return {
      ...prev,
      duration: seed.duration || prev.duration,
      scenario_mode: seed.scenario_mode || 'vuser_driven',
      script_shakeout: seed.script_shakeout ?? prev.script_shakeout,
      resource_baseline: seed.resource_baseline ?? prev.resource_baseline,
      groups: reseedScenarioGroups(seed.groups, prev.groups),
      goal_target_average_tps: seededGoal.target_average_tps,
      goal_min_vusers: seededGoal.min_vusers,
      goal_max_vusers: seededGoal.max_vusers,
      goal_p95_latency_ms: seededGoal.p95_latency_ms,
      goal_max_error_pct: seededGoal.max_error_pct,
      goal_config: Object.keys(seededGoal).length ? seededGoal : undefined,
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
      runtime_think_time_min_percent: seed.think_time ? seed.think_time.min_percent : (replacesRuntime ? undefined : prev.runtime_think_time_min_percent),
      runtime_think_time_max_percent: seed.think_time ? seed.think_time.max_percent : (replacesRuntime ? undefined : prev.runtime_think_time_max_percent),
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
    setModelFamilyChoice('')
    if (shapeId === PRODUCTION_REPLAY_SHAPE_ID) {
      setSelectedShapeId('')
      setSelectedCloneScenarioId('')
      setSelectedLoadModelTemplateId('')
      setLoadModelValidation(null)
      setLoadModelValidationError('')
      setScenarioSourceMode('traffic_profile')
      setScenarioStartChoice('traffic_profile')
      setScriptSelectionTargetIndex(null)
      markWizardStepVisited('source')
      setWizardStep('source')
      return
    }
    if (shapeId === CSV_DRIVEN_SHAPE_ID) {
      setSelectedShapeId('')
      setSelectedCloneScenarioId('')
      setSelectedLoadModelTemplateId('')
      setLoadModelValidation(null)
      setLoadModelValidationError('')
      setScenarioSourceMode('csv_driven')
      setScenarioStartChoice('csv_driven')
      setScriptSelectionTargetIndex(null)
      markWizardStepVisited('source')
      setWizardStep('source')
      return
    }
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
      if (shapeId === 'closed-vuser') closedRampDownEnabledOnceRef.current.clear()
	  if (shapeId === 'open-arrival-rate' || shapeId === 'goal-based') goalGuardrailsEditedRef.current = false
      seedFormFromScenario(seed)
			if (shapeId === 'stepped-ramp') updateSteppedRamp(undefined, 'scenario')
			if (shapeId === 'closed-vuser') updateClosedVUser(undefined, 'scenario')
			if (shapeId === 'open-arrival-rate' || shapeId === 'goal-based') updateGoalDriven()
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
    if (template.shape_id === 'closed-vuser') closedRampDownEnabledOnceRef.current.clear()
	if (template.shape_id === 'open-arrival-rate' || template.shape_id === 'goal-based') goalGuardrailsEditedRef.current = false
    seedFormFromScenario(seed)
		if (template.shape_id === 'stepped-ramp') updateSteppedRamp(undefined, 'scenario')
		if (template.shape_id === 'closed-vuser') updateClosedVUser(undefined, 'scenario')
		if (template.shape_id === 'open-arrival-rate' || template.shape_id === 'goal-based') updateGoalDriven()
  }

  async function loadCompletedGoalBaselines() {
	const projectId = activeProjectId
	const scopeGeneration = scenarioDraftGenerationRef.current
	const requestGeneration = ++goalBaselineSelectionGenerationRef.current
	try {
	  const response = await loadAllTestSummaries(api.listTests, { project_id: projectId, summary: true })
	  if (currentProjectIdRef.current !== projectId || scenarioDraftGenerationRef.current !== scopeGeneration || goalBaselineSelectionGenerationRef.current !== requestGeneration) return
	  setGoalBaselineTests((response.tests || []).filter(test => test.status === 'COMPLETE'))
	} catch (err: unknown) {
	  if (currentProjectIdRef.current !== projectId || scenarioDraftGenerationRef.current !== scopeGeneration || goalBaselineSelectionGenerationRef.current !== requestGeneration) return
	  setGoalSizingError(getErrorMessage(err, 'Unable to load completed runs'))
	}
  }

  const openGoalSizing = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
	selectShapeOption('open-arrival-rate')
	setGoalArtModes({ group_0: 'manual' })
    setScenarioStartChoice('goal')
    setGoalBaselineId('')
    setGoalBaselineReports({})
    setGoalBaselineLoadingId('')
    setGoalSizingError('')
	setWizardStep('run')
	// Reached from the Model & Shape step, so an edit must stay bound to its scenario.
	await loadCompletedGoalBaselines()
  }

  const loadGoalBaselineReport = async (testId: string): Promise<RunArchiveReport> => {
    const cached = goalBaselineReports[testId]
    if (cached) return cached
    const projectId = activeProjectId
    const scopeGeneration = scenarioDraftGenerationRef.current
    const isCurrentScope = () => (
      currentProjectIdRef.current === projectId
      && scenarioDraftGenerationRef.current === scopeGeneration
    )
    if (isCurrentScope()) {
      setGoalBaselineLoadingId(testId)
    }
    try {
      const report = await api.getTestArchiveReport(testId)
      if (isCurrentScope()) {
        setGoalBaselineReports(prev => ({ ...prev, [testId]: report }))
      }
      return report
    } catch (err: unknown) {
      throw new Error(getErrorMessage(err, 'Unable to load the baseline run report'))
    } finally {
      if (isCurrentScope()) {
        setGoalBaselineLoadingId(current => current === testId ? '' : current)
      }
    }
  }

  const selectGoalBaseline = async (testId: string) => {
    const projectId = activeProjectId
    const scopeGeneration = scenarioDraftGenerationRef.current
    const selectionGeneration = ++goalBaselineSelectionGenerationRef.current
    const isCurrentSelection = () => (
      currentProjectIdRef.current === projectId
      && scenarioDraftGenerationRef.current === scopeGeneration
      && goalBaselineSelectionGenerationRef.current === selectionGeneration
    )
    setGoalBaselineId(testId)
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
	  applyGoalBaselineOptions(testId, options)
    } catch (err: unknown) {
      if (!isCurrentSelection()) return
      setGoalSizingError(getErrorMessage(err, 'Unable to load the baseline run report'))
    }
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

	const updateSteppedRamp = (definitionPatch?: Partial<SteppedRampDefinition>, scope?: 'scenario' | 'group', durationOverride?: string) => {
		setForm(previous => {
			const otherShapeConfig = { ...(previous.goal_config?.workload_shape_config || {}) }
			delete otherShapeConfig.closed_vuser
			const currentConfig = previous.goal_config?.workload_shape_config?.stepped_ramp
			const nextScope = scope || currentConfig?.scope || 'scenario'
			const scenarioDefinition = cloneSteppedRampDefinition(currentConfig?.scenario)
			const groupDefinitions: Record<string, SteppedRampDefinition> = {}
			previous.groups.forEach((group, index) => {
				const key = steppedRampGroupKey(group, index)
				groupDefinitions[key] = cloneSteppedRampDefinition(steppedRampDefinitionForGroup(currentConfig, group, index) || scenarioDefinition)
			})
			if (definitionPatch) {
				if (nextScope === 'scenario') Object.assign(scenarioDefinition, definitionPatch)
				else Object.assign(groupDefinitions[steppedRampGroupKey(previous.groups[activeGroupIndex], activeGroupIndex)], definitionPatch)
			}
			const nextConfig: SteppedRampConfig = {
				scope: nextScope,
				scenario: scenarioDefinition,
				...(nextScope === 'group' ? { groups: groupDefinitions } : {}),
			}
			const nextDuration = durationOverride ?? previous.duration
			const durationSeconds = parseDurationToSeconds(nextDuration)
			const nextGroups = previous.groups.map((group, index) => {
				const definition = nextScope === 'group'
					? groupDefinitions[steppedRampGroupKey(group, index)]
					: scenarioDefinition
				const plan = buildSteppedRampPlan({
					durationSeconds,
					startVusers: definition.start_vusers,
					endVusers: definition.end_vusers,
					incrementBy: definition.increment_by,
					stepCount: definition.step_count,
					vusersPerStep: definition.vusers_per_step,
				})
				if (plan.error) return group
				return { ...group, stages: plan.stages, vusers: definition.end_vusers, ramp_up: '', ramp_down: '' }
			})
			const goalConfig: GoalConfig = {
				...(previous.goal_config || {}),
				workload_shape_config: { ...otherShapeConfig, stepped_ramp: nextConfig },
			}
			return { ...previous, duration: nextDuration, scenario_mode: 'vuser_driven', groups: nextGroups, goal_config: goalConfig }
		})
		setLoadModelValidation(null)
		setLoadModelValidationError('')
	}

	const updateClosedVUser = (definitionPatch?: Partial<ClosedVUserDefinition>, scope?: 'scenario' | 'group') => {
		setForm(previous => {
			const otherShapeConfig = { ...(previous.goal_config?.workload_shape_config || {}) }
			delete otherShapeConfig.stepped_ramp
			const currentConfig = previous.goal_config?.workload_shape_config?.closed_vuser
			const nextScope = scope || currentConfig?.scope || 'scenario'
			const scenarioFallback = closedVUserDefinitionFromGroup(previous.groups[0], previous.duration)
			const scenarioDefinition = cloneClosedVUserDefinition(currentConfig?.scenario, scenarioFallback)
			const groupDefinitions: Record<string, ClosedVUserDefinition> = {}
			previous.groups.forEach((group, index) => {
				groupDefinitions[closedVUserGroupKey(group, index)] = cloneClosedVUserDefinition(
					closedVUserDefinitionForGroup(currentConfig, group, index, previous.duration),
					scenarioDefinition,
				)
			})
			if (definitionPatch) {
				if (nextScope === 'scenario') Object.assign(scenarioDefinition, definitionPatch)
				else Object.assign(groupDefinitions[closedVUserGroupKey(previous.groups[activeGroupIndex], activeGroupIndex)], definitionPatch)
			}
			const nextConfig: ClosedVUserConfig = {
				scope: nextScope,
				scenario: scenarioDefinition,
				...(nextScope === 'group' ? { groups: groupDefinitions } : {}),
			}
			const nextGroups = previous.groups.map((group, index) => {
				const definition = nextScope === 'group'
					? groupDefinitions[closedVUserGroupKey(group, index)]
					: scenarioDefinition
				const plan = buildClosedVUserPlan({
					peakVusers: definition.peak_vusers,
					rampUp: definition.ramp_up,
					sustain: definition.sustain,
					rampDownEnabled: definition.ramp_down_enabled,
					rampDown: definition.ramp_down,
				})
				if (plan.error) return group
				return {
					...group,
					vusers: definition.peak_vusers,
					ramp_up: definition.ramp_up,
					ramp_down: definition.ramp_down_enabled ? definition.ramp_down : '',
					stages: plan.stages,
				}
			})
			const plans = (nextScope === 'group' ? Object.values(groupDefinitions) : [scenarioDefinition])
				.map(definition => buildClosedVUserPlan({
					peakVusers: definition.peak_vusers,
					rampUp: definition.ramp_up,
					sustain: definition.sustain,
					rampDownEnabled: definition.ramp_down_enabled,
					rampDown: definition.ramp_down,
				}))
			const longestDuration = plans.reduce((max, plan) => plan.error ? max : Math.max(max, plan.totalSeconds), 0)
			const goalConfig: GoalConfig = {
				...(previous.goal_config || {}),
				workload_shape_config: { ...otherShapeConfig, closed_vuser: nextConfig },
			}
			return {
				...previous,
				duration: longestDuration > 0 ? formatTimeCompact(longestDuration) : previous.duration,
				scenario_mode: 'vuser_driven',
				groups: nextGroups,
				goal_config: goalConfig,
			}
		})
		setLoadModelValidation(null)
		setLoadModelValidationError('')
	}

	const toggleClosedVUserRampDown = (enabled: boolean) => {
		const key = closedVUserConfig.scope === 'group'
			? closedVUserGroupKey(activeStageGroup, activeGroupIndex)
			: 'scenario'
		const firstEnable = enabled && !closedRampDownEnabledOnceRef.current.has(key)
		if (enabled) closedRampDownEnabledOnceRef.current.add(key)
		updateClosedVUser({
			ramp_down_enabled: enabled,
			...(firstEnable ? { ramp_down: activeClosedVUserDefinition.ramp_up } : {}),
		})
	}

	const updateGuidedEarlyStop = (patch: Partial<NonNullable<GoalConfig['early_stop']>>) => {
		setForm(previous => ({
			...previous,
			goal_config: {
				...(previous.goal_config || {}),
				early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m', ...(previous.goal_config?.early_stop || {}), ...patch },
				workload_shape_config: previous.goal_config?.workload_shape_config,
			},
		}))
		setLoadModelValidation(null)
	}

	function updateGoalDriven(definitionPatch?: Partial<GoalDrivenGroupDefinition>, groupIndex = activeGroupIndex) {
		setForm(previous => {
			const current = previous.goal_config?.workload_shape_config?.goal_driven
			const definitions: Record<string, GoalDrivenGroupDefinition> = {}
			previous.groups.forEach((group, index) => {
				const existing = current?.groups?.[goalDrivenGroupKey(index)]
				definitions[goalDrivenGroupKey(index)] = existing
					? { ...existing, art: existing.art ? { ...existing.art } : undefined }
					: { target_tps: reconstructedGoalTarget(previous.groups, index, previous.goal_target_average_tps || 25) }
			})
			if (definitionPatch && definitions[goalDrivenGroupKey(groupIndex)]) {
				const hasARTPatch = Object.prototype.hasOwnProperty.call(definitionPatch, 'art')
				definitions[goalDrivenGroupKey(groupIndex)] = {
					...definitions[goalDrivenGroupKey(groupIndex)],
					...definitionPatch,
					art: hasARTPatch ? definitionPatch.art : definitions[goalDrivenGroupKey(groupIndex)].art,
				}
			}
			const totalTarget = Object.values(definitions).reduce((sum, definition) => sum + (Number(definition.target_tps) || 0), 0)
			const groups = previous.groups.map((group, index) => {
				const definition = definitions[goalDrivenGroupKey(index)]
				const artMs = Number(definition.art?.art_ms)
				const vusers = artMs > 0
					? littlesLawVUsers(definition.target_tps, artMs)
					: group.vusers
				return { ...group, vusers, stages: [] }
			})
			const totalEstimate = groups.reduce((sum, group) => sum + Math.max(1, Number(group.vusers) || 1), 0)
			const otherShapeConfig = { ...(previous.goal_config?.workload_shape_config || {}) }
			delete otherShapeConfig.stepped_ramp
			delete otherShapeConfig.closed_vuser
			const goalConfig: GoalConfig = {
				...(previous.goal_config || {}),
				target_average_tps: totalTarget,
				min_vusers: goalGuardrailsEditedRef.current ? previous.goal_min_vusers : totalEstimate,
				max_vusers: goalGuardrailsEditedRef.current ? previous.goal_max_vusers : Math.max(2, totalEstimate * 2),
				workload_shape_config: { ...otherShapeConfig, goal_driven: { groups: definitions } },
			}
			return {
				...previous,
				duration: previous.duration || '60m',
				scenario_mode: 'goal_oriented',
				groups,
				goal_target_average_tps: totalTarget,
				goal_min_vusers: goalConfig.min_vusers,
				goal_max_vusers: goalConfig.max_vusers,
				goal_config: goalConfig,
			}
		})
		setLoadModelValidation(null)
		setLoadModelValidationError('')
	}

	function updateGoalDrivenGuardrail<K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) {
		if (field === 'min_vusers' || field === 'max_vusers') goalGuardrailsEditedRef.current = true
		setForm(previous => scenarioFormWithGoalField(previous, field, value))
	}

	function applyGoalBaselineOptions(testId: string, options: BaselineGroupOption[]) {
		const normalizeName = (value: string) => value.trim().toLowerCase().replace(/[^a-z0-9]+/g, ' ')
		const modes = Object.fromEntries(form.groups.map((group, index) => {
			const matched = options.some(option => (option.script_id && option.script_id === group.script_id) || normalizeName(option.group) === normalizeName(group.name))
			return [goalDrivenGroupKey(index), matched ? 'baseline' : 'manual']
		})) as Record<string, 'manual' | 'baseline'>
		setGoalArtModes(modes)
		setForm(previous => {
			const current = previous.goal_config?.workload_shape_config?.goal_driven
			const definitions: Record<string, GoalDrivenGroupDefinition> = {}
			const groups = previous.groups.map((group, index) => {
				const key = goalDrivenGroupKey(index)
				const existing = current?.groups?.[key] || { target_tps: reconstructedGoalTarget(previous.groups, index, previous.goal_target_average_tps || 25) }
				const match = options.find(option => option.script_id && option.script_id === group.script_id)
					|| options.find(option => normalizeName(option.group) === normalizeName(group.name))
				if (!match) {
					definitions[key] = { ...existing, art: existing.art ? { ...existing.art } : undefined }
					return group
				}
				const art: GoalDrivenARTConfig = { basis: 'baseline', art_ms: match.baseline_art_ms, baseline_test_id: testId, baseline_group: match.group }
				definitions[key] = { ...existing, art }
				return { ...group, vusers: littlesLawVUsers(existing.target_tps, art.art_ms), stages: [] }
			})
			const totalTarget = Object.values(definitions).reduce((sum, definition) => sum + definition.target_tps, 0)
			const totalEstimate = groups.reduce((sum, group) => sum + Math.max(1, group.vusers), 0)
			const goalConfig: GoalConfig = {
				...(previous.goal_config || {}),
				target_average_tps: totalTarget,
				min_vusers: goalGuardrailsEditedRef.current ? previous.goal_min_vusers : totalEstimate,
				max_vusers: goalGuardrailsEditedRef.current ? previous.goal_max_vusers : Math.max(2, totalEstimate * 2),
				workload_shape_config: { ...(previous.goal_config?.workload_shape_config || {}), goal_driven: { groups: definitions } },
			}
			return { ...previous, groups, scenario_mode: 'goal_oriented', goal_target_average_tps: totalTarget, goal_min_vusers: goalConfig.min_vusers, goal_max_vusers: goalConfig.max_vusers, goal_config: goalConfig }
		})
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
		if (isSteppedRamp) updateSteppedRamp()
		if (isGuidedClosedVUser) updateClosedVUser()
		if (isGoalDriven) updateGoalDriven(undefined, form.groups.length)
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
		if (isSteppedRamp) updateSteppedRamp()
		if (isGuidedClosedVUser) updateClosedVUser()
		if (isGoalDriven) updateGoalDriven(undefined, form.groups.length)
    setStageGroupIndex(form.groups.length)
    setLoadModelValidation(null)
  }

  const chooseScriptForGroup = (index: number) => {
    setPickedScriptIds([])
    setScriptSelectionTargetIndex(index)
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
        scriptGroupRefs.current[targetIndex]?.querySelector<HTMLButtonElement>('.scenario-script-select-button')?.focus()
      }, 0)
      return
    }
    addScenarioGroupFromScript(script)
  }

  const removeScenarioGroup = (index: number) => {
    // Keep focus on the same surviving group, rather than its former position.
    if (runtimeScope !== 'defaults' && runtimeGroupIndex === index) setRuntimeScope('defaults')
    setRuntimeGroupIndex(current => current > index ? current - 1 : current)
		closedRampDownEnabledOnceRef.current = remapClosedRampDownHistory(closedRampDownEnabledOnceRef.current, index)
    setGoalArtModes(previous => remapGoalDrivenIndexedState(previous, index))
    setGoalArtUnits(previous => remapGoalDrivenIndexedState(previous, index))
    setForm(prev => {
      const nextGroups = prev.groups
        .filter((_, groupIndex) => groupIndex !== index)
        .map((group, nextIndex) => {
          const previousIndex = nextIndex >= index ? nextIndex + 1 : nextIndex
          return group.name.trim() === `Group ${previousIndex + 1}`
            ? { ...group, name: `Group ${nextIndex + 1}` }
            : group
        })
			const ensuredGroups = ensureScenarioGroups(nextGroups)
			const stepped = prev.goal_config?.workload_shape_config?.stepped_ramp
			const closed = prev.goal_config?.workload_shape_config?.closed_vuser
			const goalDriven = prev.goal_config?.workload_shape_config?.goal_driven
			let steppedRamp = stepped
			let closedVUser = closed
			if (stepped?.scope === 'group') {
				const definitions: Record<string, SteppedRampDefinition> = {}
				ensuredGroups.forEach((group, nextIndex) => {
					const previousIndex = nextIndex >= index ? nextIndex + 1 : nextIndex
					const previousGroup = prev.groups[previousIndex]
					definitions[steppedRampGroupKey(group, nextIndex)] = cloneSteppedRampDefinition(
						previousGroup ? steppedRampDefinitionForGroup(stepped, previousGroup, previousIndex) : stepped.scenario,
					)
				})
				steppedRamp = { ...stepped, groups: definitions }
			}
			if (closed?.scope === 'group') {
				const definitions: Record<string, ClosedVUserDefinition> = {}
				ensuredGroups.forEach((group, nextIndex) => {
					const previousIndex = nextIndex >= index ? nextIndex + 1 : nextIndex
					const previousGroup = prev.groups[previousIndex]
					definitions[closedVUserGroupKey(group, nextIndex)] = cloneClosedVUserDefinition(
						previousGroup ? closedVUserDefinitionForGroup(closed, previousGroup, previousIndex, prev.duration) : closed.scenario,
					)
				})
				closedVUser = { ...closed, groups: definitions }
			}
			const closedDuration = closedVUser?.scope === 'group'
				? Object.values(closedVUser.groups || {}).reduce((max, definition) => {
					const plan = buildClosedVUserPlan({
						peakVusers: definition.peak_vusers,
						rampUp: definition.ramp_up,
						sustain: definition.sustain,
						rampDownEnabled: definition.ramp_down_enabled,
						rampDown: definition.ramp_down,
					})
					return plan.error ? max : Math.max(max, plan.totalSeconds)
				}, 0)
				: 0
			let nextGoalDriven = goalDriven
			if (goalDriven) {
				const definitions: Record<string, GoalDrivenGroupDefinition> = {}
				ensuredGroups.forEach((_, nextIndex) => {
					const previousIndex = nextIndex >= index ? nextIndex + 1 : nextIndex
					const existing = goalDriven.groups[goalDrivenGroupKey(previousIndex)]
					if (existing) definitions[goalDrivenGroupKey(nextIndex)] = { ...existing, art: existing.art ? { ...existing.art } : undefined }
				})
				nextGoalDriven = { groups: definitions }
			}
			if (steppedRamp === stepped && closedVUser === closed && nextGoalDriven === goalDriven) return { ...prev, groups: ensuredGroups }
			return {
				...prev,
				duration: closedDuration > 0 ? formatTimeCompact(closedDuration) : prev.duration,
				groups: ensuredGroups,
				goal_config: {
					...(prev.goal_config || {}),
					workload_shape_config: {
						...(prev.goal_config?.workload_shape_config || {}),
						stepped_ramp: steppedRamp,
						closed_vuser: closedVUser,
						goal_driven: nextGoalDriven,
					},
				},
			}
    })
	if (isGoalDriven) updateGoalDriven(undefined, Math.max(0, index - 1))
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

  // Semantic revisions survive a library refresh but invalidate changed inputs or scripts.
  const validationFingerprint = JSON.stringify([form, selectedShapeId, scenarioSourceMode,
    form.groups.map(group => { const script = scripts.find(script => script.id === group.script_id); return script ? [script.id, script.version, script.protocol, script.authoring_health, script.script] : null })])
  const validationRevision = useRef(0)
  useEffect(() => {
    validationRevision.current += 1
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setLoadModelValidationLoading(false)
  }, [validationFingerprint])

  const validateLoadModel = async () => {
    if (bindingErrors.length) { setLoadModelValidationError(bindingErrors.join(' ')); setWizardStep('data'); return }
    if (sourcePending) { setLoadModelValidationError(sourcePendingMessage); setWizardStep('source'); return }
    const revision = ++validationRevision.current
    setLoadModelValidation(null)
    if (form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setLoadModelValidationError('Select a script for every group before validating the load model.')
      return
    }
    if (!(form.termination_mode === 'iterations' || isValidDurationInput(form.duration))) {
      setLoadModelValidationError('Set a positive scenario duration before validating the load model.')
      return
    }
    if (scenarioGroupNameError) {
      setLoadModelValidationError(scenarioGroupNameError)
      openScriptComposition()
      return
    }
    if (blockingStageTimelineMessage) {
      setLoadModelValidationError(blockingStageTimelineMessage)
      return
    }
		if (steppedRampBlockingMessage) {
			setLoadModelValidationError(steppedRampBlockingMessage)
			setWizardStep('run')
			return
		}
		if (closedVUserBlockingMessage) {
			setLoadModelValidationError(closedVUserBlockingMessage)
			setWizardStep('run')
			return
		}
		if (goalDrivenBlockingMessage) {
			setLoadModelValidationError(goalDrivenBlockingMessage)
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
      if (revision !== validationRevision.current) return
      setLoadModelValidation(report)
      setWizardStep('review')
    } catch (err: unknown) {
      if (revision !== validationRevision.current) return
      setLoadModelValidation(null)
      setLoadModelValidationError(getErrorMessage(err, 'Unable to validate load model'))
    } finally {
      if (revision === validationRevision.current) setLoadModelValidationLoading(false)
    }
  }

  const persistScenario = async () => {
    if (savingRef.current) return null
    setError('')
    if (sourcePending) { setError(sourcePendingMessage); setWizardStep('source'); return null }
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
      openScriptComposition()
      return null
    }
    if (!(form.termination_mode === 'iterations' || isValidDurationInput(form.duration))) {
      setError('Scenario duration is required.')
      return null
    }
    if (blockingStageTimelineMessage) {
      setError(blockingStageTimelineMessage)
      return null
    }
		if (steppedRampBlockingMessage) {
			setError(steppedRampBlockingMessage)
			setWizardStep('run')
			return null
		}
		if (closedVUserBlockingMessage) {
			setError(closedVUserBlockingMessage)
			setWizardStep('run')
			return null
		}
		if (goalDrivenBlockingMessage) {
			setError(goalDrivenBlockingMessage)
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
    if (bindingErrors.length) { setError(bindingErrors.join(' ')); setWizardStep('data'); return null }
    if (assignmentValidationError) {
      setError(assignmentValidationError)
      return null
    }
    const transactionSLAError = monitorEditorError || validateGoalConfig(goalConfigFromForm(form)) || validateTransactionResponseTimeSLAOverrides(form.groups)
    if (transactionSLAError) {
      setError(transactionSLAError)
      return null
    }
    if (form.scenario_mode === 'goal_oriented') {
      const goalConfig = goalConfigFromForm(form)
      if (!hasGoalModeConfiguration(goalConfig)) {
        setError('Goal-oriented scenarios require at least one goal or guardrail setting')
        return null
      }
    }
    // An edit saves only against the baseline it was loaded from; a pending
    // reconciliation must be resolved with a reloaded baseline first.
    if (editingId && scenarioConflict) {
      setError('Resolve the scenario revision review above before saving: load the current baseline, then reapply or reload.')
      return null
    }
    const baseline = scenarioBaseline
    if (editingId && !baseline) {
      setScenarioConflict({ reason: scenarioBaselineMissing, loading: false, current: null })
      return null
    }
    const payload = buildScenarioDraftPayload()
    const submittedDraft = currentDraftRef.current
    const stillCurrent = () => currentDraftRef.current.form === submittedDraft.form && currentDraftRef.current.activeProjectId === submittedDraft.activeProjectId && currentDraftRef.current.showForm === submittedDraft.showForm
    savingRef.current = true
    setSavePending(true)
    try {
      // An edit never falls back to a create; updateScenario refuses a missing baseline.
      const saved = editingId ? await api.updateScenario(editingId, payload, baseline as ScenarioAuthoringBaseline) : await api.createScenario(payload)
      if (!stillCurrent()) return null
      if (!saved?.id) {
        setError('The controller returned an incomplete save response. Check the scenario library before retrying.')
        return null
      }
      let committed: ScenarioAuthoringBaseline | null = null
      try { committed = scenarioBaselineFromRead(saved, activeProjectId || undefined) } catch { committed = null }
      setScenarioBaseline(committed)
      originalShapeIdRef.current = selectedShapeId
      setSavedNotice(`Saved ${saved.name || form.name}${saved.ref_key ? ` (${saved.ref_key})` : ''}.`)
      return saved
    } catch (err: unknown) {
      if (!stillCurrent()) return null
      if (editingId && scenarioSaveNeedsReconciliation(err)) setScenarioConflict({ reason: scenarioConflictReason(err), loading: false, current: null })
      else setError(getErrorMessage(err, 'Unable to save scenario'))
      return null
    } finally {
      savingRef.current = false
      setSavePending(false)
    }
  }

  const resetScenarioBuilder = () => {
    clearScenarioBuilderDraft()
    resetBuilderToBlank()
    setShowForm(false)
    onScenarioViewChange?.('library')
  }

  const checkpointFingerprint = JSON.stringify([form, scenarioSourceMode, scenarioStartChoice, selectedShapeId, selectedLoadModelTemplateId, selectedCloneScenarioId])
  // The baseline an edit checkpoint records, unless its original holds values
  // a browser checkpoint must not keep (the checkpoint then falls back to the
  // listed update time).
  const recordedScenarioBaseline = (): Pick<ScenarioAuthoringDraft, 'baselineTag' | 'baselineForm' | 'baselineShapeId'> => {
    const baselineForm = parseScenarioForm(editingBaselineFormRef.current)
    const baselineTag = scenarioBaseline?.etag || resumedBaselineTagRef.current
    if (!editingId || !baselineTag || !baselineForm) return {}
    try { assertDraftHasNoInlineCredentials(baselineForm) } catch { return {} }
    return { baselineTag, baselineForm, baselineShapeId: originalShapeIdRef.current }
  }
  const buildBrowserDraft = (): ScenarioAuthoringDraft => ({
    version: 2, projectId: activeProjectId || '', savedAt: new Date().toISOString(),
    editingId, editingUpdatedAt: editingBaselineUpdatedAtRef.current || scenarios.find(item => item.id === editingId)?.updated_at || '',
    ...recordedScenarioBaseline(),
    form, source: scenarioSourceMode, startChoice: scenarioStartChoice,
    shapeId: selectedShapeId, templateId: selectedLoadModelTemplateId, cloneId: selectedCloneScenarioId,
    step: wizardStep, visited: maxVisitedWizardIndex,
  })
  buildBrowserDraftRef.current = buildBrowserDraft
  const saveBrowserProgress = () => {
    if (!canManageTests) return false
    if (monitorEditorError) { setError(monitorEditorError); return false }
    try {
      const draft = buildBrowserDraft()
      saveScenarioAuthoringDraft(draft)
      autosaveSavedAtRef.current = ''
      autosaveReplacedRef.current = null
      setAuthoringDraft(draft)
      activeCheckpointSavedAtRef.current = draft.savedAt
      checkpointBaselineRef.current = checkpointFingerprint
      setError('')
      setSavedNotice('Draft progress saved in this browser for this project. It is not a runnable scenario. Resume it here to finish authoring and validate.')
      return true
    } catch (cause) {
      setError(getErrorMessage(cause, 'Unable to save browser progress. Your edits remain here.'))
      return false
    }
  }
  const clearResolvedCheckpoint = () => {
    // Saving another scenario must not erase a different checkpoint, including
    // a newer draft saved by another tab during this request.
    const latest = readScenarioAuthoringDraft(activeProjectId || '')
    autosaveSavedAtRef.current = ''
    autosaveReplacedRef.current = null
    if (latest && latest.savedAt === activeCheckpointSavedAtRef.current) {
      clearScenarioAuthoringDraft(activeProjectId || '')
      setAuthoringDraft(null)
      activeCheckpointSavedAtRef.current = ''
    } else setAuthoringDraft(latest)
  }
  const saveScenario = async (): Promise<boolean> => {
    if (monitorEditorError) { setError(monitorEditorError); return false }
    if (newScenarioLoadUnconfigured || sourcePending || goalSizingIncomplete || blockedScriptGroups.length || !form.name.trim() || !(form.termination_mode === 'iterations' || isValidDurationInput(form.duration))
      || form.groups.some(group => !group.script_id) || blockingStageTimelineMessage || steppedRampBlockingMessage
      || closedVUserBlockingMessage || goalDrivenBlockingMessage || observerEnvelopeError || aggregateEnvelopeError
      || assignmentValidationError || scenarioGroupNameError || bindingErrors.length) return saveBrowserProgress()
    const saved = await persistScenario()
    if (!saved) return false
    setEditingId(saved.id)
    editingBaselineFormRef.current = JSON.stringify(form)
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = saved.updated_at || ''
    checkpointBaselineRef.current = checkpointFingerprint
    clearScenarioBuilderDraft()
    try { clearResolvedCheckpoint() } catch { /* Server save succeeded; a local checkpoint may remain. */ }
    void refresh()
    return true
  }
  saveAuthoringRef.current = saveScenario

  // A resumed edit adopts the durable baseline only when it is the revision
  // the draft was loaded from (its tag; older checkpoints: the listed update
  // time). Otherwise the editor opens reconciliation against the draft's own
  // original, so a stale draft can never present a newer tag.
  const bindResumedScenarioBaseline = async (id: string, draft: ScenarioAuthoringDraft) => {
    const generation = scenarioDraftGenerationRef.current
    const scopeProjectId = activeProjectId
    try {
      const fresh = await api.getScenario(id)
      if (generation !== scenarioDraftGenerationRef.current || currentProjectIdRef.current !== scopeProjectId) return
      const baseline = fresh?.id === id ? scenarioBaselineFromRead(fresh, scopeProjectId || undefined) : null
      const unchanged = baseline && (draft.baselineTag ? baseline.etag === draft.baselineTag : fresh.updated_at === draft.editingUpdatedAt)
      if (baseline && unchanged) {
        resumedBaselineTagRef.current = ''
        setScenarioBaseline(baseline)
      } else if (draft.baselineTag) setScenarioConflict(conflict => conflict || { reason: 'The scenario changed after this draft was saved. Load the current baseline, review the differences, then reapply your draft.', loading: false, current: null })
    } catch { /* The next save asks for a reviewed baseline. */ }
  }

  const loadScenarioConflictBaseline = async () => {
    const id = editingId
    if (!id || !scenarioConflict || scenarioConflict.loading) return
    const generation = scenarioDraftGenerationRef.current
    const scopeProjectId = activeProjectId
    const current = () => generation === scenarioDraftGenerationRef.current && currentProjectIdRef.current === scopeProjectId
    setScenarioConflict(conflict => conflict && { ...conflict, loading: true })
    try {
      const fresh = await api.getScenario(id)
      if (!current()) return
      const baseline = scenarioBaselineFromRead(fresh, scopeProjectId || undefined)
      const latestForm = scenarioFormStateFromStoredScenario(fresh, scripts[0]?.id || '')
      setScenarioConflict(conflict => conflict && { ...conflict, loading: false, current: { baseline, scenario: fresh, form: latestForm } })
    } catch (err: unknown) {
      if (current()) setScenarioConflict(conflict => conflict && { ...conflict, loading: false, reason: getErrorMessage(err, 'Unable to load the current scenario baseline. Your draft is retained.') })
    }
  }

  // Reapply carries the draft's changes (from the form it was loaded with)
  // onto the reviewed scenario; the reviewed baseline becomes the one the
  // next save presents.
  const reapplyScenarioConflictDraft = () => {
    const current = scenarioConflict?.current
    if (!current) return
    const original = parseScenarioForm(editingBaselineFormRef.current) || current.form
    let merged: ScenarioFormState
    try {
      merged = reapplyAuthoringDraft(original, form, current.form)
    } catch (err: unknown) {
      setScenarioConflict(conflict => conflict && { ...conflict, reason: getErrorMessage(err, 'Unable to reapply the draft.') })
      return
    }
    const latestShapeId = scenarioShapeId(current.scenario)
    if (selectedShapeId === originalShapeIdRef.current) setSelectedShapeId(latestShapeId)
    originalShapeIdRef.current = latestShapeId
    editingBaselineFormRef.current = JSON.stringify(current.form)
    resumedBaselineTagRef.current = ''
    editingBaselineUpdatedAtRef.current = current.scenario.updated_at || ''
    setForm(merged)
    setScenarioBaseline(current.baseline)
    setScenarioConflict(null)
    setError('')
    setSavedNotice('Draft reapplied onto the current scenario. Locally changed fields and group lists took precedence; review them, then save.')
  }

  const reloadScenarioFromConflict = () => {
    const current = scenarioConflict?.current
    if (!current) return
    const step = wizardStep
    const visited = maxVisitedWizardIndex
    openEditFrom(current.scenario, current.baseline)
    setWizardStep(step)
    setMaxVisitedWizardIndex(visited)
    setSavedNotice('Reloaded the current scenario. Your draft was discarded.')
  }

  // Diff lists what changed on the server since the draft's baseline (over
  // the canonical authoring document) and what the draft changed locally.
  const scenarioConflictDiff = () => {
    const current = scenarioConflict?.current
    if (!current) return []
    const original = parseScenarioForm(editingBaselineFormRef.current) || current.form
    const canonical = (baseline: ScenarioAuthoringBaseline) => ({ scenario: baseline.projection.scenario, library_placement: baseline.projection.library_placement, monitor_selection: baseline.projection.monitor_selection })
    const originalShape = originalShapeIdRef.current
    const serverPaths = scenarioBaseline
      ? authoringChangedPaths(canonical(scenarioBaseline), canonical(current.baseline), 'Scenario')
      : authoringChangedPaths(buildScenarioDraftPayload(original, originalShape), buildScenarioDraftPayload(current.form, scenarioShapeId(current.scenario)), 'Scenario')
    return [
      { label: 'Changed in the current scenario since your draft was loaded', paths: serverPaths, empty: 'No authored-field differences.' },
      { label: 'Changed in your draft', paths: authoringChangedPaths(buildScenarioDraftPayload(original, originalShape), buildScenarioDraftPayload(form, selectedShapeId), 'Scenario'), empty: 'No local changes.' },
    ]
  }

  // Saving monitor bindings advances the scenario revision. When nothing else
  // changed, the editor adopts the new baseline so its next save is not a
  // self-inflicted conflict.
  const adoptBaselineAfterBindingsSave = async () => {
    const id = editingId
    const previous = scenarioBaseline
    if (!id || !previous) return
    const generation = scenarioDraftGenerationRef.current
    try {
      const fresh = await api.getScenario(id)
      if (generation !== scenarioDraftGenerationRef.current) return
      const next = adoptableAfterBindingsSave(previous, scenarioBaselineFromRead(fresh, activeProjectId || undefined))
      if (next) setScenarioBaseline(current => current === previous ? next : current)
    } catch { /* The next save reconciles. */ }
  }

  const resumeAuthoringDraft = () => {
    if (!authoringDraft || authoringDraft.projectId !== activeProjectId || !canManageTests) return
    const draft = authoringDraft
    resetBuilderToBlank()
    activeCheckpointSavedAtRef.current = draft.savedAt
    setForm(draft.form)
    const storedGoal = draft.form.goal_config?.workload_shape_config?.goal_driven
    setGoalArtModes(Object.fromEntries(Object.entries(storedGoal?.groups || {}).map(([key, group]) => [key, group.art?.basis || 'manual'])))
    goalGuardrailsEditedRef.current = draft.form.goal_min_vusers !== undefined || draft.form.goal_max_vusers !== undefined
    const storedClosed = draft.form.goal_config?.workload_shape_config?.closed_vuser
    closedRampDownEnabledOnceRef.current = new Set(storedClosed ? storedClosed.scope === 'group' ? Object.keys(storedClosed.groups || {}) : ['scenario'] : [])
    const existing = scenarios.find(item => item.id === draft.editingId)
    // A draft that recorded its baseline stays an edit of that scenario and
    // reconciles against its own original; an older checkpoint resumes as an
    // edit only while the listed scenario is unchanged.
    const recorded = Boolean(existing && draft.baselineTag && draft.baselineForm)
    const canResumeEdit = recorded || (existing && existing.updated_at === draft.editingUpdatedAt)
    setEditingId(canResumeEdit ? draft.editingId : null)
    editingBaselineFormRef.current = !canResumeEdit || !existing ? '' : JSON.stringify(recorded ? draft.baselineForm : scenarioFormStateFromStoredScenario(existing, '', false))
    originalShapeIdRef.current = !canResumeEdit || !existing ? '' : recorded ? draft.baselineShapeId || '' : scenarioShapeId(existing)
    resumedBaselineTagRef.current = recorded ? draft.baselineTag || '' : ''
    editingBaselineUpdatedAtRef.current = !canResumeEdit || !existing ? '' : recorded ? draft.editingUpdatedAt : existing.updated_at || ''
    if (canResumeEdit && draft.editingId) void bindResumedScenarioBaseline(draft.editingId, draft)
    setScenarioSourceMode(draft.source); setScenarioStartChoice(draft.startChoice); setModelFamilyChoice('')
    setScenarioStartPath(draft.source === 'existing' && draft.cloneId && !draft.editingId ? 'copy' : 'new')
    setSelectedShapeId(draft.shapeId); setSelectedLoadModelTemplateId(draft.templateId); setSelectedCloneScenarioId(draft.cloneId)
    setWizardStep(draft.step); setMaxVisitedWizardIndex(draft.visited)
    setShowForm(true); onScenarioViewChange?.('builder')
    checkpointBaselineRef.current = JSON.stringify([draft.form, draft.source, draft.startChoice, draft.shapeId, draft.templateId, draft.cloneId])
    setSavedNotice(draft.editingId && !canResumeEdit ? 'Resumed as a new scenario because the original changed or is unavailable. Review before saving.' : 'Resumed browser draft. Validate the current scripts before configuring a run.')
  }

  const saveScenarioAndCreateTest = async () => {
    const saved = await persistScenario()
    if (!saved) return
    try { clearResolvedCheckpoint() } catch { /* The server save is authoritative. */ }
    // Publish the saved state before the parent evaluates navigation guards.
    flushSync(resetScenarioBuilder)
    onCreateTestFromScenario?.(saved.id)
    void refresh()
  }

  const deleteScenario = async (id: string) => {
    if (assigningScenarioIds.has(id)) return
    setConfirmDeleteScenarioId(null)
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot delete them.')
      return
    }
    const scopeProjectId = activeProjectId
    setDeletingScenarioIds(current => new Set(current).add(id))
    try {
      await api.deleteScenario(id, scenarios.find(scenario => scenario.id === id)?.updated_at)
      if (currentProjectIdRef.current !== scopeProjectId) return
      setScenarios(current => current.filter(scenario => scenario.id !== id))
      setSelectedScenarioIds(current => {
        const next = new Set(current)
        next.delete(id)
        return next
      })
      await refresh()
    } catch (err: unknown) {
      if (currentProjectIdRef.current === scopeProjectId) {
        setError(getErrorMessage(err, 'Unable to delete scenario'))
        if (err instanceof ScenarioChangedSinceObserved) void refresh()
      }
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
    if (!canManageTests || ids.length === 0 || !(await confirmAction(`Delete ${ids.length} selected scenario${ids.length === 1 ? '' : 's'}? Scenarios with linked results cannot be deleted.`))) return
    const scopeProjectId = activeProjectId
    setDeletingScenarioIds(current => new Set([...current, ...ids]))
    setError('')
    const observed = new Map(scenarios.map(scenario => [scenario.id, scenario.updated_at]))
    const outcomes = await boundedAllSettled(ids, id => api.deleteScenario(id, observed.get(id)))
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
  const scenarioBuilderDirty = showForm && (Boolean(monitorEditorError) || checkpointBaselineRef.current !== checkpointFingerprint && (
    editingId
      ? Boolean(editingBaselineFormRef.current) && (JSON.stringify(form) !== editingBaselineFormRef.current || sourcePending)
      : hasScenarioBuilderProgress({ form, scenarioStartChoice, maxVisitedWizardIndex })
  ))
  resetScenarioBuilderRef.current = resetScenarioBuilder
  refreshRef.current = refresh
  isScenarioBuilderDirtyRef.current = () => scenarioBuilderDirty
  const scenarioBuilderGuard = useMemo<NavigationGuard>(() => ({
    id: 'scenario-builder',
    label: 'Scenario Builder',
    isDirty: () => isScenarioBuilderDirtyRef.current(),
    save: async () => {
      const saved = await saveAuthoringRef.current()
      if (!saved) return false
      resetScenarioBuilderRef.current()
      void refreshRef.current()
      return true
    },
    discard: () => {
      clearScenarioBuilderDraft()
      discardAutosavedCheckpoint()
      resetScenarioBuilderRef.current()
    },
  }), [discardAutosavedCheckpoint])
  useNavigationGuard(showForm ? scenarioBuilderGuard : null, scenarioBuilderDirty)
  // Checkpoint unsaved builder progress in this browser (debounced), so a
  // reload or restart offers "Resume draft" instead of losing the work. This
  // does not move the dirty baseline: leaving still asks to save or discard.
  // The browser keeps one checkpoint per project, so autosave never replaces
  // a checkpoint of other work (an unresumed draft, another tab's save).
  useEffect(() => {
    if (!showForm || !canManageTests || !activeProjectId || !scenarioBuilderDirty || monitorEditorError) return undefined
    const timer = window.setTimeout(() => {
      const build = buildBrowserDraftRef.current
      if (!build) return
      try {
        const latest = readScenarioAuthoringDraft(activeProjectId)
        if (latest && latest.savedAt !== activeCheckpointSavedAtRef.current) return
        if (!autosaveSavedAtRef.current) autosaveReplacedRef.current = latest
        const draft = build()
        saveScenarioAuthoringDraft(draft)
        activeCheckpointSavedAtRef.current = draft.savedAt
        autosaveSavedAtRef.current = draft.savedAt
      } catch { /* best effort; an explicit Save draft reports errors */ }
    }, SCENARIO_AUTOSAVE_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [activeProjectId, canManageTests, checkpointFingerprint, maxVisitedWizardIndex, monitorEditorError, scenarioBuilderDirty, showForm, wizardStep])
  // Script-local navigation retains the scenario mounted underneath it. Global
  // navigation outside this provider must still resolve both dirty editors.
  const prerequisiteNavigationRunner = useMemo(() => ({
    ...navigationRunner,
    runGuarded: (proceed: () => void, retainedGuardIds: readonly string[] = []) =>
      runGuarded(proceed, [...retainedGuardIds, scenarioBuilderGuard.id]),
  }), [navigationRunner, runGuarded, scenarioBuilderGuard.id])


  useEffect(() => {
    if (currentWizardIndex >= 0) {
      setMaxVisitedWizardIndex(current => Math.max(current, currentWizardIndex))
    }
  }, [currentWizardIndex])

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

  const dataPoolLabel = scenarioBindings.length ? scenarioBindings.map(binding => `${binding.alias || 'Unnamed alias'}: ${dataPools.find(pool => pool.id === binding.pool_id)?.name || 'Unavailable pool'}`).join(', ') : 'No data pool'
  const shapeLabel = selectedTemplate?.name || selectedShape?.support.name || (scenarioSourceMode === 'existing' ? 'Modeled from scenario' : scenarioSourceMode === 'csv_driven' ? 'CSV Driven' : 'Manual shape')
  const goalSizingIncomplete = isGoalDriven && formGroups.some((_, index) => !(Number(goalDrivenConfig.groups[goalDrivenGroupKey(index)]?.art?.art_ms) > 0))
  const validationLabel = loadModelValidationLoading ? 'Checking' : loadModelValidationError ? 'Validation failed' : loadModelValidation ? formatLoadModelDimension(loadModelValidation.status) : 'Not validated'
  const requiredDraftReady = Boolean(form.name.trim() && (form.termination_mode === 'iterations' || isValidDurationInput(form.duration)) && formGroups.every(group => group.script_id) && !scenarioGroupNameError)
  const sourceStepComplete = !sourcePending && (scenarioStartChoice === 'goal'
    ? isGoalDriven
    : scenarioSourceMode === 'existing'
      ? Boolean(selectedCloneScenarioId)
      : Boolean(selectedShapeId || selectedLoadModelTemplateId))
  const wizardStepCompletion: Record<ScenarioWizardStep, boolean> = {
    source: sourceStepComplete,
    scripts: Boolean(form.name.trim() && formGroups.length > 0 && formGroups.every(group => group.script_id) && !scenarioGroupNameError),
    run: Boolean(sourceStepComplete && (!isGoalDriven || formGroups.every((_, index) => goalDrivenConfig.groups[goalDrivenGroupKey(index)]?.art)) && (form.termination_mode === 'iterations' || isValidDurationInput(form.duration)) && !blockingStageTimelineMessage && !steppedRampBlockingMessage && !closedVUserBlockingMessage && !goalDrivenBlockingMessage),
    data: bindingErrors.length === 0,
    review: Boolean(requiredDraftReady && !sourcePending && loadModelValidation && loadModelValidation.status !== 'blocked' && !loadModelValidationError && !blockingStageTimelineMessage),
  }
  // A new scenario's VUsers and duration start from builder defaults. Until the
  // author has chosen a model (shape, template, goal or cloned scenario) or
  // reached Run Settings, Save draft keeps a browser checkpoint rather than
  // creating a launchable project scenario with load the author never set.
  const newScenarioLoadUnconfigured = !editingId && !sourceStepComplete && maxVisitedWizardIndex < RUN_SETTINGS_STEP_INDEX
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
            <table data-table-pagination="on">
              <thead>
                <tr>
                  <SortHeader label="Run Name" field="name" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Status" field="status" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Release ID" field="release_id" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Started" field="start_time" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
                  <SortHeader label="Configured duration" field="duration" sort={{ field: innerSortField, direction: innerSortDirection }} onSort={handleInnerSort} />
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
    if (scenarioSourceMode !== 'existing' || !selectedCloneScenario) return null
    // Copies show their source's runs beside the copy table; edits show their own runs on Model & Shape.
    const copying = wizardStep === 'scripts' && scenarioStartPath === 'copy'
    const editing = wizardStep === 'source' && Boolean(editingId)
    if (!copying && !editing) return null
    return (
      <section className="scenario-source-runs-panel" aria-live="polite">
        {renderScenarioRunsContent(selectedCloneScenario)}
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

  const renderPreRunAssurance = () => (
    <div className="scenario-subpanel" data-testid="pre-run-assurance">
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
  )

  const renderProtectRun = () => (
    <div className="scenario-subpanel" data-testid="protect-the-run">
      <div className="scenario-assignment-header">
        <div>
          <h4>Protect the Run</h4>
          <p>Optionally stop when the error rate remains at or above a threshold for a sustained active-runtime window.</p>
        </div>
      </div>
      <div className="stepped-ramp-protection">
        <label className="stepped-ramp-toggle"><input type="checkbox" checked={Boolean(form.goal_config?.early_stop?.enabled)} onChange={event => updateGuidedEarlyStop({ enabled: event.target.checked })} /><span><strong>Stop early on excessive errors</strong><small>The active load level and partial interval evidence are retained in alerts and results.</small></span></label>
        <label className="scenario-field"><span>Error rate threshold</span><div className="stepped-ramp-unit-input"><input aria-label="Protect the Run error rate threshold" className="settings-input" type="number" min={0} max={100} step="0.1" disabled={!form.goal_config?.early_stop?.enabled} value={form.goal_config?.early_stop?.error_rate_pct ?? 5} onChange={event => updateGuidedEarlyStop({ error_rate_pct: Number(event.target.value) })} /><span>%</span></div></label>
        <DurationField className="scenario-field" label="Protect the Run sustained for" compact disabled={!form.goal_config?.early_stop?.enabled} value={form.goal_config?.early_stop?.breach_window || '1m'} onChange={value => updateGuidedEarlyStop({ breach_window: value })} />
      </div>
    </div>
  )

  const renderScenarioSettingsTabs = () => (
    <TabList className="scenario-settings-sections" role="tablist" aria-label="Scenario settings sections" onKeyDown={handleTabKey}>
      {scenarioSettingsSections.map(section => <button
        key={section.id}
        type="button"
        id={`scenario-settings-${section.id}-tab`}
        className={`scenario-settings-section-tab ${scenarioSettingsSection === section.id ? 'active' : ''}`}
        role="tab"
        aria-selected={scenarioSettingsSection === section.id}
        aria-controls={`scenario-settings-${section.id}-panel`}
        onClick={() => setScenarioSettingsSection(section.id)}
        tabIndex={scenarioSettingsSection === section.id ? 0 : -1}
      >{section.label}</button>)}
    </TabList>
  )

  const renderScriptPicker = () => {
    if (!showScriptLibrary || !scriptLibraryDialogOpen || scriptSelectionTargetIndex === null) return null
    const targetGroup = formGroups[scriptSelectionTargetIndex]
    const targetLabel = targetGroup?.name || `Group ${scriptSelectionTargetIndex + 1}`
    return (
      <DialogLayer>
        <div className="modal-overlay" {...dialogBackdropProps(closeScriptLibraryDialog, 'Close script library dialog')}>
          <div
            ref={scriptLibraryDialogRef}
            className="modal-content modal-wide scenario-existing-script-library scenario-script-library-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="scenario-script-library-dialog-title"
            aria-describedby="scenario-script-library-dialog-description"
          >
            <div className="modal-header">
              <div>
                <h3 id="scenario-script-library-dialog-title">Choose Script</h3>
                <p id="scenario-script-library-dialog-description" className="modal-subtitle">
                  Select a script for {targetLabel}.
                </p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={closeScriptLibraryDialog}>Cancel</button>
            </div>
            <div className="scenario-script-library-dialog-body">
              <div className="library-toolbar">
                <div>
                  <h4 className="section-title">Existing Scripts Library ({scripts.length})</h4>
                  {!canManageScripts && <p>Script creation requires script-management permission. Ask a project administrator to add a script to this library.</p>}
                  <button className="btn btn-secondary" disabled={!canManageScripts} title={!canManageScripts ? 'Ask a script author or administrator to create a script in this project.' : undefined} onClick={() => { setPrerequisiteScript('start-new') }}>Create script</button>
                  <button className="btn btn-secondary" disabled={!canManageScripts} title={!canManageScripts ? 'Ask a script author or administrator to create a script in this project.' : undefined} onClick={() => { setPrerequisiteScript('source') }}>Import script</button>
                  <p className="library-toolbar-subtitle">
                    {scriptLibraryQuery.trim() ? `${visibleScriptLibrary.length} matching script${visibleScriptLibrary.length === 1 ? '' : 's'}` : 'Pick from existing scripts while assembling scenario groups.'}
                  </p>
                  {targetGroup && (
                    <p className="scenario-script-selection-status">
                      Choosing for {targetLabel}
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
              <div className="settings-grid scenario-picker-filters">
                <label className="scenario-field">Readiness<select className="settings-input" value={scriptReadinessFilter} onChange={event => setScriptReadinessFilter(event.target.value)}><option value="all">All readiness states</option><option value="unblocked">No known authoring blockers</option><option value="blocked">Blocked</option></select></label>
                <label className="scenario-field">Protocol<select className="settings-input" value={scriptProtocolFilter} onChange={event => setScriptProtocolFilter(event.target.value)}><option value="all">All protocols</option>{[...new Set(scripts.flatMap(scriptProtocols))].sort().map(protocol => <option key={protocol}>{protocol}</option>)}</select></label>
                <button type="button" className="btn btn-secondary" disabled={!pickedScriptIds.length} onClick={() => {
                  const selected = pickedScriptIds.map(id => scripts.find(script => script.id === id)).filter((script): script is StoredScript => !!script)
                  setForm(current => {
                    const groups = [...current.groups]
                    for (const script of selected) {
                      const emptyIndex = groups.findIndex(group => !group.script_id)
                      const index = emptyIndex >= 0 ? emptyIndex : groups.length
                      const group = emptyIndex >= 0 ? groups[index] : emptyScenarioGroup('', index)
                      groups[index] = { ...group, script_id: script.id, name: scenarioGroupNameAfterScriptSelection(group.name, index, script.name, '', groups.map(item => item.name)) }
                    }
                    return { ...current, groups: ensureScenarioGroups(groups) }
                  })
                  setScriptSelectionTargetIndex(null); setPickedScriptIds([])
                }}>Add selected scripts ({pickedScriptIds.length})</button>
              </div>
              <div className="data-table">
                <table>
                  <thead>
                    <tr>
                      <SortHeader label="Script" field="name" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                      <SortHeader label="Protocol(s)" field="protocol" sort={{ field: scriptLibrarySortField, direction: scriptLibrarySortDirection }} onSort={handleScriptLibrarySort} />
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleScriptLibrary.map(script => {
                      const targetScriptId = formGroups[scriptSelectionTargetIndex]?.script_id || ''
                      const selectedForTarget = Boolean(targetScriptId) && targetScriptId === script.id
                      return (
                        <tr key={script.id} className={selectedForTarget ? 'selected' : ''}>
                          <td className="cell-name">
                            <label className="scenario-picker-selection"><input type="checkbox" aria-label={`Select ${script.name} for bulk addition`} checked={pickedScriptIds.includes(script.id)} onChange={event => setPickedScriptIds(current => event.target.checked ? [...current, script.id] : current.filter(id => id !== script.id))} />{script.name}</label>
                            <div className="scenario-table-assignment-detail">v{script.version || '—'} · {scriptReadiness.get(script.id)?.label}</div><small>{scriptReadiness.get(script.id)?.summary}</small><details><summary>Usage and history</summary><p>Created {formatDate(script.created_at)} · Last run {formatDate(script.last_run_at)}</p><p aria-label="Script scenario usage">{formatScriptScenarioCount(script)} · {associatedScenarioList(script)}</p><p>Releases: {scriptReleaseList(script)}</p></details>
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
                            >
                              {`Use for Group ${scriptSelectionTargetIndex + 1}`}
                            </button>
                          </td>
                        </tr>
                      )
                    })}
                    {visibleScriptLibrary.length === 0 && (
                      <tr>
                        <td colSpan={3} className="cell-muted">
                          {scriptLibraryQuery.trim() ? 'No scripts match this search.' : 'No existing scripts are available for this project.'}
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </DialogLayer>
    )
  }

  const renderShapeCard = (option: (typeof shapeCatalog)[number], onUse?: () => void) => (
    <div key={option.id} className={`scenario-shape-catalog-card ${selectedShapeId === option.id ? 'selected' : ''}`}>
                  <div className="scenario-support-card-header">
                    <div>
                      <strong>{option.support.name}</strong>{option.support.name === 'Closed VUser' && <small>Recommended for a first fixed-user test</small>}
                      <span>{option.support.category}</span>
                    </div>
                    <span className={`scenario-load-status ${getLoadModelStatusClass(option.support.status)}`}>{option.support.status === 'executed' ? 'Supported' : formatLoadModelDimension(option.support.status)}</span>
                  </div>
                  <p>{option.support.apexload_shape}</p>
                  <details><summary>Technical fields</summary><div className="scenario-support-fields">
                    {option.support.fields.slice(0, 3).map(field => <code key={field}>{field}</code>)}
                    {option.support.fields.length > 3 && <code>+{option.support.fields.length - 3} fields</code>}
                  </div></details>
                  <WorkloadShapeVisual entry={option.support} />
                  <div className="scenario-shape-actions">
                    <button type="button" className="btn btn-sm btn-secondary" aria-label={`Use ${option.support.name}`} onClick={() => onUse ? onUse() : selectShapeOption(option.id)}>{`Use ${option.support.name}`}</button>
                    {option.templates.length > 0 && <details open={option.templates.some(template => template.id === selectedLoadModelTemplateId)}><summary>Starter templates</summary>{option.templates.map(template => (
                      <button key={template.id} type="button" className={`btn btn-sm ${selectedLoadModelTemplateId === template.id ? 'btn-primary' : 'btn-ghost'}`} onClick={() => applyLoadModelTemplate(template.id)}>
                        {template.name}
                      </button>
                    ))}</details>}
                  </div>
                </div>
  )

  const renderTrafficProfileSource = () => (
    <>
      {trafficProfileShapeEnabled && csvDrivenShapeEnabled && (
        <div className="scenario-source-toggle" role="group" aria-label="Traffic profile source">
          <button type="button" className={`mode-chip ${scenarioSourceMode !== 'csv_driven' ? 'active' : ''}`} aria-pressed={scenarioSourceMode !== 'csv_driven'} onClick={() => selectScenarioSourceMode('traffic_profile')}>Production data</button>
          <button type="button" className={`mode-chip ${scenarioSourceMode === 'csv_driven' ? 'active' : ''}`} aria-pressed={scenarioSourceMode === 'csv_driven'} onClick={() => selectScenarioSourceMode('csv_driven')}>Exact CSV schedule</button>
        </div>
      )}
      <p className="scenario-assignment-copy secondary">{scenarioSourceMode === 'csv_driven' ? 'Map an exact VUser or TPS schedule from a CSV without production-source transformations.' : 'Analyze a file or production query, map series to groups, and preview the exact runtime targets before applying.'}</p>
      {scenarioSourceMode === 'csv_driven' ? (
            <CSVDrivenSourcePanel
              projectId={activeProjectId}
              groupNames={form.groups.map((group, index) => group.name.trim() || findScriptById(scripts, group.script_id)?.name || `Group ${index + 1}`)}
              scenarioDraft={buildScenarioDraftPayload()}
              existingProfile={selectedShapeId === CSV_DRIVEN_SHAPE_ID ? form.traffic_profile : undefined}
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
                setSelectedShapeId(CSV_DRIVEN_SHAPE_ID)
                setLoadModelValidation(result.load_model_validation || null)
              }}
            />
      ) : (
            <TrafficProfileSourcePanel
              projectId={activeProjectId}
              groupNames={form.groups.map((group, index) => group.name.trim() || findScriptById(scripts, group.script_id)?.name || `Group ${index + 1}`)}
              scenarioDraft={buildScenarioDraftPayload()}
              existingProfile={selectedShapeId === PRODUCTION_REPLAY_SHAPE_ID ? form.traffic_profile : undefined}
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
      )}
    </>
  )

  const renderScenarioNameField = () => (
    <div className="settings-grid scenario-core-grid">
      <label className="scenario-field">
        <span>Scenario Name</span>
        <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
      </label>
    </div>
  )

  const renderCloneScenarioPanel = () => (
    <div className="scenario-clone-path">
      <div className="scenario-assignment-header">
        <div>
          <h4>Choose A Scenario To Copy</h4>
          <p>Its scripts, load model, run settings, data pools, and generator defaults are copied into this draft. The original scenario is not changed.</p>
        </div>
      </div>
      {scenarioSourceMode === 'existing' && selectedCloneScenario && !editingId && (
        <>
          <div className="scenario-shape-guidance" role="note">
            <div className="scenario-shape-guidance-header">
              <span className="scenario-shape-guidance-name">{`Copied ${selectedCloneScenario.name}`}</span>
              <span className="scenario-shape-guidance-category">{`${formGroups.length} script group${formGroups.length === 1 ? '' : 's'}`}</span>
            </div>
            <p className="scenario-shape-guidance-copy">This draft now uses its scripts and settings. Name the new scenario below, review the copied scripts, or continue to Model & Shape.</p>
            <div className="scenario-shape-actions">
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => setScenarioStartPath('new')}>Review copied scripts</button>
            </div>
          </div>
          {renderScenarioNameField()}
        </>
      )}
      {scenarios.length === 0 ? (
        <p className="scenario-assignment-copy secondary">No saved scenarios in this workspace yet.</p>
      ) : (
        <>
              <label className="library-search scenario-clone-search">
                <span>Search existing scenarios</span>
                <input type="search" value={scenarioCloneQuery} onChange={event => setScenarioCloneQuery(event.target.value)} placeholder="Scenario, script, data pool, release..." />
              </label>
              <div className="data-table scenario-clone-table">
                <table>
                  <thead>
                    <tr>
                      <th>Scenario</th>

                      <th>Workload</th>


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
                          onClick={() => requestCloneScenario(scenario)}
                          onKeyDown={(event) => {
                            if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
                              event.preventDefault()
                              requestCloneScenario(scenario)
                            }
                          }}
                        >
                          <td className="cell-name">
                            <div>{scenario.name}{scenario.archived_at && <span> · Archived</span>}</div>
                      <ActivityAttribution activity={scenario.activity} />
                            <details onClick={event => event.stopPropagation()}><summary>Run releases / Updated</summary><p>Source: Local · {releaseList(scenario)} · Updated {formatDate(scenario.updated_at || scenario.created_at)}</p></details>
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
                          <td aria-label="Scenario workload">
                            <div className="scenario-table-workload">
                              <strong>{formatScenarioScripts(scenario)} · {scenario.duration}</strong><span>{scenario.scripts.reduce((total, group) => total + Math.max(group.vusers || 0, ...((group.stages || []).map(stage => stage.target_vusers || 0))), 0)} VUsers (sum of group peaks) · {scenario.workload_shape_id || "Custom model"}</span>
                              <span>{formatScenarioDataPools(scenario)}</span>
                              <span>{formatScenarioResultCount(scenario)}</span><span>{scenario.scripts.some(group => scriptReadiness.get(group.script_id)?.blocked) ? 'Script review required' : 'Validate before creating a run'}</span>
                            </div>
                          </td>
                          <td style={{ textAlign: 'right' }}>
                            <button
                              className={`btn btn-sm ${selected ? 'btn-primary' : 'btn-secondary'}`}
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation()
                                requestCloneScenario(scenario)
                              }}
                            >
                              {selected ? 'Copied' : 'Copy scenario'}
                            </button>
                          </td>
                        </tr>
                      )
                    })}
                    {filteredCloneScenarios.length === 0 && (
                      <tr>
                        <td colSpan={6} className="cell-muted">No scenarios match this search.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
        </>
      )}
    </div>
  )

  const renderWizardStep = () => {
    if (wizardStep === 'source') {
      const familyShapeOptions = activeModelFamily && activeModelFamily.id !== 'traffic-profile'
        ? activeModelFamily.shapeIds
          .map(id => shapeCatalog.find(option => option.id === id))
          .filter((option): option is (typeof shapeCatalog)[number] => Boolean(option))
        : []
      const copiedModelScenario = scenarioSourceMode === 'existing' ? selectedCloneScenario : null
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>Choose A Load Model</h4>
              <p>Pick how load is generated for the selected scripts. Each model offers variants and optional starter templates.</p>
            </div>
          </div>
          {copiedModelScenario && (
            <div className="scenario-shape-guidance" role="note">
              <div className="scenario-shape-guidance-header">
                <span className="scenario-shape-guidance-name">{editingId ? 'Saved model' : 'Copied model'}</span>
                <span className="scenario-shape-guidance-category">{shapeLabel}</span>
              </div>
              <p className="scenario-shape-guidance-copy">{editingId ? `Using the load model saved with ${copiedModelScenario.name}.` : `Using the load model copied from ${copiedModelScenario.name}.`} Choose a model below only to replace it.</p>
            </div>
          )}
          <div className="scenario-source-grid">
            {modelFamilies.map(family => (
              <button
                key={family.id}
                type="button"
                aria-pressed={activeModelFamily?.id === family.id}
                className={`scenario-source-card ${activeModelFamily?.id === family.id ? 'selected' : ''}`}
                onClick={() => selectModelFamily(family.id)}
              >
                <span>{family.label}</span>
                <strong>{family.eyebrow}</strong>
                <small>{family.description}</small>
              </button>
            ))}
          </div>
          {activeModelFamily?.id === 'traffic-profile' ? renderTrafficProfileSource() : activeModelFamily && (
            <>
              {shapeFamilyFor(selectedShapeId) === activeModelFamily.id && renderShapeGuidance()}
              <div className="scenario-shape-catalog-grid">
                {familyShapeOptions.map(option => renderShapeCard(option, activeModelFamily.id === 'goal' ? () => void openGoalSizing() : undefined))}
                {familyShapeOptions.length === 0 && (activeModelFamily.id === 'goal'
                  // Goal setup does not depend on the catalog, so it stays reachable while it loads or is unavailable.
                  ? <div className="scenario-shape-actions"><button type="button" className="btn btn-sm btn-secondary" onClick={() => void openGoalSizing()}>Use Goal-Driven Scenario</button></div>
                  : <p className="scenario-assignment-copy secondary">No workload shape catalog is available from this controller.</p>)}
              </div>
            </>
          )}
        </section>
      )
    }

    if (wizardStep === 'scripts') {
      const selectedScriptProtocols = [...new Set(formGroups.flatMap(group => {
        const script = findScriptById(scripts, group.script_id)
        return script ? scriptProtocols(script) : []
      }))]
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>Start Your Scenario</h4>
              <p>Build a new scenario from scripts, or copy a saved scenario and adjust it. Either way, choose the load model on the next step.</p>
            </div>
          </div>
          <div className="scenario-source-grid" role="group" aria-label="How to start this scenario">
            <button type="button" aria-pressed={scenarioStartPath === 'new'} className={`scenario-source-card ${scenarioStartPath === 'new' ? 'selected' : ''}`} onClick={() => setScenarioStartPath('new')}>
              <span>New Scenario</span>
              <strong>Add scripts</strong>
              <small>Choose the scripts that participate in this scenario and set up their groups.</small>
            </button>
            <button type="button" disabled={scenarios.length === 0} aria-pressed={scenarioStartPath === 'copy'} className={`scenario-source-card ${scenarioStartPath === 'copy' ? 'selected' : ''}`} onClick={() => setScenarioStartPath('copy')}>
              <span>Copy Existing Scenario</span>
              <strong>Start from a saved scenario</strong>
              <small>{scenarios.length === 0 ? 'No saved scenarios in this workspace yet.' : 'Reuse its scripts, load model, run settings, and data pools. The original is not changed.'}</small>
            </button>
          </div>
          {scenarioStartPath === 'copy' ? renderCloneScenarioPanel() : (<>
          <div className="scenario-assignment-header">
            <div>
              <h4>Scripts And Groups</h4>
              <p>Add each script participating in the scenario and set its initialization scope. Tune VUsers and ramp on the Run Settings step.</p>
            </div>
          </div>
          {renderScenarioNameField()}
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
                          {selectedScript && <small>v{selectedScript.version || '—'} · {selectedScript.archived_at ? 'Archived' : scriptReadiness.get(selectedScript.id)?.label}</small>}
                        </span>
                        {selectedScript && <span className="protocol-badge">{selectedScript.protocol || 'http'}</span>}
                      </button>
                    </div>
                    {selectedScript && <div className="scenario-script-readiness"><p>{scriptReadiness.get(selectedScript.id)?.summary}</p><button type="button" className="btn btn-sm btn-secondary" onClick={() => setReviewScriptId(selectedScript.id)}>Review script</button></div>}
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
          {selectedScriptProtocols.length > 1 && <p className="scenario-assignment-copy secondary" role="note">This scenario mixes {selectedScriptProtocols.join(', ')} scripts. Capacity advice accounts for each protocol's density.</p>}
          {renderScriptPicker()}
          </>)}
          {pendingCloneScenario && (
            <ReplaceScenarioScriptsModal
              sourceLabel={pendingCloneScenario.name}
              onReplace={() => cloneScenarioDraft(pendingCloneScenario, 'replace')}
              onKeep={() => cloneScenarioDraft(pendingCloneScenario, 'keep')}
              onCancel={() => setPendingCloneScenario(null)}
            />
          )}
        </section>
      )
    }

	    if (wizardStep === 'run') {
	      const activeGroupScript = findScriptById(scripts, activeStageGroup.script_id)
	      const activeGroupVUserMax = maxVUsersForRole(activeStageGroup.role)
	      const closedVUserPeakMax = closedVUserConfig.scope === 'scenario'
	        ? maxSharedVUsersForRoles(formGroups.map(group => group.role))
	        : activeGroupVUserMax
	      const activeGroupLabel = activeStageGroup.name || `Group ${activeGroupIndex + 1}`

      const renderGuidedScenarioExtras = () => <>
        {scenarioSettingsSection === 'stages' && !isSteppedRamp && <div className="scenario-subpanel">
          <h4>Ramp Pattern</h4>
          <p className="scenario-assignment-copy secondary">This model manages its load curve automatically. Select Stepped Ramp in Model &amp; Shape to configure equal load steps.</p>
        </div>}
        {scenarioSettingsSection === 'phases' && <div className="scenario-subpanel">
          {!isGuidedClosedVUser && <div className="scenario-assignment-header"><div><h4>Run Phases</h4><p>Set the run window and an optional delay before load begins.</p></div></div>}
          <div className="settings-grid scenario-core-grid">
            {!isGuidedClosedVUser && <DurationField className="scenario-field" label="Duration" value={form.duration} disabled={form.termination_mode === 'iterations'} required showPresets onChange={value => isSteppedRamp ? updateSteppedRamp(undefined, undefined, value) : setForm(previous => ({ ...previous, duration: value, scenario_mode: 'goal_oriented' }))} />}
            <DurationField className="scenario-field" label="Start Delay" value={form.start_delay} allowEmpty compact onChange={value => setForm(previous => ({ ...previous, start_delay: value }))} />
          </div>
        </div>}
        {scenarioSettingsSection === 'advanced' && <>
          {renderProtectRun()}
          {renderPreRunAssurance()}
          <TransactionResponseTimeSLASettings
            defaultMs={goalConfigFromForm(form).transaction_response_time_sla_ms}
            groups={formGroups.map((group, index) => ({
              name: group.name || `Group ${index + 1}`,
              scriptName: findScriptById(scripts, group.script_id)?.name,
              overrideMs: group.transactionResponseTimeSlaMs,
            }))}
            onDefaultChange={value => setForm(previous => scenarioFormWithGoalField(previous, 'transaction_response_time_sla_ms', value))}
            onGroupOverrideChange={(index, value) => updateScenarioGroup(index, { transactionResponseTimeSlaMs: value })}
          />
        </>}
      </>

      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Run Settings</h4>
            <p>Configure duration, group load, stage modeling, protection, SLAs, browser defaults, and generator assignment.</p>
          </div>
          {renderShapeGuidance()}
          <TabList className="scenario-run-settings-tabs" role="tablist" aria-label="Run settings sections" onKeyDown={handleTabKey}>
            {runSettingsTabs.map(tab => (
              <button
                key={tab.id}
                type="button"
                id={`scenario-run-settings-${tab.id}-tab`}
                className={`scenario-run-settings-tab ${runSettingsTab === tab.id ? 'active' : ''}`}
                role="tab"
                aria-selected={runSettingsTab === tab.id}
                aria-controls={`scenario-run-settings-${tab.id}-panel`}
                onClick={() => setRunSettingsTab(tab.id)} tabIndex={(runSettingsTab === tab.id) ? 0 : -1}
              >
                {tab.label}
              </button>
            ))}
          </TabList>
          <div
            className="scenario-run-settings-tab-panel"
            id={`scenario-run-settings-${runSettingsTab}-panel`}
            role="tabpanel"
            aria-labelledby={`scenario-run-settings-${runSettingsTab}-tab`}
          >
            {runSettingsTab === 'scenario' && (<>
					{(isGoalDriven || isGuidedClosedVUser || isSteppedRamp) && renderScenarioSettingsTabs()}
					{isGoalDriven ? (
					<>
						<div className="stepped-ramp-worksheet goal-driven-worksheet" data-settings-tab={scenarioSettingsSection} id={`scenario-settings-${scenarioSettingsSection}-panel`} role="tabpanel" aria-labelledby={`scenario-settings-${scenarioSettingsSection}-tab`}>
							<div className="stepped-ramp-heading">
								<span>Guided worksheet</span>
								<h3>Goal-Driven Scenario Setup</h3>
								<p>Define requested throughput, estimate the adaptive seed from response time, and set safe operating guardrails.</p>
							</div>
							<section className="stepped-ramp-section" data-tab="composition" aria-labelledby="goal-throughput-title">
								<div className="stepped-ramp-number">1</div>
								<div className="stepped-ramp-section-body">
									<h4 id="goal-throughput-title">Throughput goal</h4>
									<p>Set the requested transaction rate for each load group. The scenario target is their sum.</p>
									<div className="settings-grid scenario-core-grid">
										<label className="scenario-field"><span>Target TPS for {activeGroupLabel}</span><div className="stepped-ramp-unit-input"><input aria-label={`Target TPS for ${activeGroupLabel}`} className="settings-input" type="number" min={0.01} step="any" value={activeGoalDrivenDefinition.target_tps} onChange={event => updateGoalDriven({ target_tps: Number(event.target.value) })} /><span>TPS</span></div></label>
									</div>
									{formGroups.length > 1 && <TabList className="scenario-group-tabs" role="tablist" aria-label="Goal-driven groups" onKeyDown={handleTabKey}>{formGroups.map((group, index) => <button key={`goal-group-${index}`} type="button" className={`scenario-group-tab ${activeGroupIndex === index ? 'active' : ''}`} role="tab" aria-selected={activeGroupIndex === index} onClick={() => setStageGroupIndex(index)} tabIndex={(activeGroupIndex === index) ? 0 : -1}><strong>{group.name.trim() || `Group ${index + 1}`}</strong><span>{goalDrivenConfig.groups[goalDrivenGroupKey(index)]?.target_tps || 0} TPS</span></button>)}</TabList>}
									<div className="stepped-ramp-equation" aria-live="polite"><strong>{form.goal_target_average_tps || 0} aggregate TPS</strong><span>Constant requested throughput for {formatModelTimeLabel(parseDurationToSeconds(form.duration))}</span></div>
								</div>
							</section>
							<section className="stepped-ramp-section" data-tab="composition" aria-labelledby="goal-demand-title">
								<div className="stepped-ramp-number">2</div>
								<div className="stepped-ramp-section-body">
									<h4 id="goal-demand-title">Estimate adaptive demand</h4>
									<p>Use expected average response time (ART) to calculate a planning seed. Runtime VUsers still adapt within guardrails.</p>
									<div className="stepped-ramp-choice" role="radiogroup" aria-label={`ART source for ${activeGroupLabel}`}>
										<button type="button" role="radio" aria-checked={(goalArtModes[goalDrivenGroupKey(activeGroupIndex)] || activeGoalDrivenDefinition.art?.basis || 'manual') === 'manual'} className={(goalArtModes[goalDrivenGroupKey(activeGroupIndex)] || activeGoalDrivenDefinition.art?.basis || 'manual') === 'manual' ? 'active' : ''} onClick={() => { const key = goalDrivenGroupKey(activeGroupIndex); setGoalArtModes(previous => ({ ...previous, [key]: 'manual' })); updateGoalDriven({ art: { basis: 'manual', art_ms: activeGoalDrivenDefinition.art?.art_ms || 400 } }) }}>Enter ART manually</button>
										<button type="button" role="radio" aria-checked={(goalArtModes[goalDrivenGroupKey(activeGroupIndex)] || activeGoalDrivenDefinition.art?.basis) === 'baseline'} className={(goalArtModes[goalDrivenGroupKey(activeGroupIndex)] || activeGoalDrivenDefinition.art?.basis) === 'baseline' ? 'active' : ''} onClick={() => { setGoalArtModes(previous => ({ ...previous, [goalDrivenGroupKey(activeGroupIndex)]: 'baseline' })); if (activeGoalDrivenDefinition.art?.basis !== 'baseline') updateGoalDriven({ art: undefined }); if (goalBaselineTests.length === 0) void loadCompletedGoalBaselines() }}>Import completed baseline</button>
									</div>
									{(goalArtModes[goalDrivenGroupKey(activeGroupIndex)] || activeGoalDrivenDefinition.art?.basis || 'manual') === 'manual' ? (
										<label className="scenario-field stepped-ramp-primary-input"><span>Average response time</span><div className="stepped-ramp-unit-input"><input aria-label={`Average response time for ${activeGroupLabel}`} className="settings-input" type="number" min={0.001} step="any" value={activeGoalDrivenDefinition.art ? ((goalArtUnits[goalDrivenGroupKey(activeGroupIndex)] || 'ms') === 's' ? activeGoalDrivenDefinition.art.art_ms / 1000 : activeGoalDrivenDefinition.art.art_ms) : ''} onChange={event => { const raw = Number(event.target.value); const artMs = (goalArtUnits[goalDrivenGroupKey(activeGroupIndex)] || 'ms') === 's' ? raw * 1000 : raw; updateGoalDriven({ art: { basis: 'manual', art_ms: artMs } }) }} /><select aria-label="ART unit" value={goalArtUnits[goalDrivenGroupKey(activeGroupIndex)] || 'ms'} onChange={event => setGoalArtUnits(previous => ({ ...previous, [goalDrivenGroupKey(activeGroupIndex)]: event.target.value as 'ms' | 's' }))}><option value="ms">ms</option><option value="s">seconds</option></select></div></label>
									) : (
										<div className="goal-driven-baseline-picker">
											<label className="scenario-field"><span>Completed baseline run</span><select aria-label="Completed baseline run" className="settings-input" value={goalBaselineId} onChange={event => void selectGoalBaseline(event.target.value)}><option value="">Choose a completed run</option>{goalBaselineTests.map(test => <option key={test.id} value={test.id}>{test.name} · {test.id}</option>)}</select><small>{goalBaselineTests.length} completed run{goalBaselineTests.length === 1 ? '' : 's'} available</small></label>
											{goalBaselineLoadingId && <small>Loading baseline response times…</small>}
											{goalSizingError && <div className="settings-error-inline" role="alert">{goalSizingError}</div>}
											{goalBaselineId && activeGoalDrivenDefinition.art?.basis !== 'baseline' && <div className="settings-error-inline">No baseline group matched this script ID or normalized group name. Enter ART manually for {activeGroupLabel}.</div>}
										</div>
									)}
									<div className="stepped-ramp-equation" aria-live="polite"><strong>{activeGoalDrivenDefinition.art ? `${activeGoalDrivenDefinition.target_tps} TPS × ${(activeGoalDrivenDefinition.art.art_ms / 1000).toLocaleString()}s = ${goalDrivenEstimatedVUsers[activeGroupIndex]} seed VUsers` : 'Seed sizing unavailable — set ART'}</strong><span>{goalSizingIncomplete ? 'Complete response-time inputs for every group to estimate total seed VUsers.' : `${totalGoalDrivenEstimatedVUsers} total estimated seed VUsers · planning metadata, not a promised runtime curve`}</span></div>
								</div>
							</section>
							<section className="stepped-ramp-section" data-tab="advanced" aria-labelledby="goal-guardrails-title">
								<div className="stepped-ramp-number">3</div>
								<div className="stepped-ramp-section-body">
									<h4 id="goal-guardrails-title">Guardrails &amp; protection</h4>
									<p>Bound adaptive demand and optionally stop after a sustained SLA breach.</p>
									<div className="settings-grid scenario-core-grid">
										<label className="scenario-field"><span>Min VUsers</span><input className="settings-input" type="number" min={1} value={form.goal_min_vusers ?? totalGoalDrivenEstimatedVUsers} onChange={event => updateGoalDrivenGuardrail('min_vusers', Number(event.target.value))} /></label>
										<label className="scenario-field"><span>Max VUsers</span><input className="settings-input" type="number" min={1} value={form.goal_max_vusers ?? Math.max(2, totalGoalDrivenEstimatedVUsers * 2)} onChange={event => updateGoalDrivenGuardrail('max_vusers', Number(event.target.value))} /></label>
										<label className="scenario-field"><span>P95 latency ceiling</span><div className="stepped-ramp-unit-input"><input className="settings-input" type="number" min={0} step="any" value={form.goal_p95_latency_ms ?? ''} onChange={event => updateGoalDrivenGuardrail('p95_latency_ms', event.target.value === '' ? undefined : Number(event.target.value))} /><span>ms</span></div></label>
										<label className="scenario-field"><span>Max error rate</span><div className="stepped-ramp-unit-input"><input className="settings-input" type="number" min={0} max={100} step="any" value={form.goal_max_error_pct ?? ''} onChange={event => updateGoalDrivenGuardrail('max_error_pct', event.target.value === '' ? undefined : Number(event.target.value))} /><span>%</span></div></label>
										<DurationField className="scenario-field" label="Breach window" value={form.goal_config?.sla_breach_window || '1m'} compact onChange={value => updateGoalDrivenGuardrail('sla_breach_window', value)} />
									</div>
									<label className="stepped-ramp-toggle"><input type="checkbox" checked={Boolean(form.goal_config?.stop_on_sla_breach)} onChange={event => updateGoalDrivenGuardrail('stop_on_sla_breach', event.target.checked)} /><span><strong>Stop on sustained guardrail breach</strong><small>Stop only after the configured breach window.</small></span></label>
									<div className="goal-driven-advanced"><h5>Advanced Guardrails</h5><CreateTestGoalSettings advancedOnly goalConfig={goalConfigFromForm(form)} onGoalConfigChange={updateGoalDrivenGuardrail} /></div>
								</div>
							</section>
							{renderGuidedScenarioExtras()}
							<p className="scenario-assignment-copy secondary">Changes apply immediately. Review the total demand and duration in the preview, then continue.</p>
						</div>
						</>
				) : isGuidedClosedVUser ? (
					<>
						<div className="stepped-ramp-worksheet closed-vuser-worksheet" data-settings-tab={scenarioSettingsSection} id={`scenario-settings-${scenarioSettingsSection}-panel`} role="tabpanel" aria-labelledby={`scenario-settings-${scenarioSettingsSection}-tab`}>
							<div className="stepped-ramp-heading">
								<span>Guided worksheet</span>
								<h3>Closed VUser Setup</h3>
								<p>Ramp continuously to a fixed load, measure steady-state performance, and optionally ramp down before the run ends.</p>
							</div>
							<section className="stepped-ramp-section" data-tab="composition" aria-labelledby="closed-target-title">
								<div className="stepped-ramp-number">1</div>
								<div className="stepped-ramp-section-body">
									<h4 id="closed-target-title">Target load</h4>
									<p>Choose the peak load and whether the same profile applies to every group.</p>
									<label className="scenario-field stepped-ramp-primary-input">
										<span>Peak VUsers</span>
											<div className="stepped-ramp-unit-input"><input className="settings-input" type="number" aria-label="Peak VUsers" min={1} max={closedVUserPeakMax} value={activeClosedVUserDefinition.peak_vusers} onChange={event => updateClosedVUser({ peak_vusers: Math.min(closedVUserPeakMax, clampVUsersForRole(Number(event.target.value), activeStageGroup.role, 1)) })} /><span>VUsers</span></div>
									</label>
									<div className="stepped-ramp-scope" role="radiogroup" aria-label="Apply Closed VUser profile to">
										<button type="button" role="radio" aria-checked={closedVUserConfig.scope === 'scenario'} className={closedVUserConfig.scope === 'scenario' ? 'active' : ''} onClick={() => updateClosedVUser(undefined, 'scenario')}><strong>Each group (same profile)</strong><span>Repeat this profile for every group</span></button>
										<button type="button" role="radio" aria-checked={closedVUserConfig.scope === 'group'} className={closedVUserConfig.scope === 'group' ? 'active' : ''} onClick={() => updateClosedVUser(undefined, 'group')}><strong>Configure each group</strong><span>Set independent peaks and phase lengths</span></button>
									</div>
									{closedVUserConfig.scope === 'group' && formGroups.length > 1 && <TabList className="scenario-group-tabs" role="tablist" aria-label="Closed VUser groups" onKeyDown={handleTabKey}>{formGroups.map((group, index) => <button key={`${group.script_id}-${index}-closed`} type="button" className={`scenario-group-tab ${activeGroupIndex === index ? 'active' : ''}`} role="tab" aria-selected={activeGroupIndex === index} onClick={() => setStageGroupIndex(index)} tabIndex={(activeGroupIndex === index) ? 0 : -1}><strong>{group.name.trim() || `Group ${index + 1}`}</strong><span>{group.vusers} peak VUsers</span></button>)}</TabList>}
								</div>
							</section>
							<section className="stepped-ramp-section" data-tab="phases" aria-labelledby="closed-phases-title">
								<div className="stepped-ramp-number">2</div>
								<div className="stepped-ramp-section-body">
									<h4 id="closed-phases-title">Run phases</h4>
									<p>Ramp-up is continuous. Sustained time begins after the peak is reached.</p>
									<div className="stepped-ramp-fields three">
										<DurationField className="scenario-field" label="Ramp-up" value={activeClosedVUserDefinition.ramp_up} compact onChange={value => updateClosedVUser({ ramp_up: value })} />
										<DurationField className="scenario-field" label="Sustained load" value={activeClosedVUserDefinition.sustain} compact onChange={value => updateClosedVUser({ sustain: value })} />
										<DurationField className="scenario-field" label="Ramp-down" value={activeClosedVUserDefinition.ramp_down} compact disabled={!activeClosedVUserDefinition.ramp_down_enabled} onChange={value => updateClosedVUser({ ramp_down: value })} />
									</div>
									<label className="stepped-ramp-toggle closed-vuser-ramp-toggle"><input type="checkbox" checked={activeClosedVUserDefinition.ramp_down_enabled} onChange={event => toggleClosedVUserRampDown(event.target.checked)} /><span><strong>Ramp down before stopping</strong><small>Continuously return this group to zero VUsers.</small></span></label>
									{activeClosedVUserPlan.error ? <div className="settings-error-inline">{activeClosedVUserPlan.error}</div> : <div className="stepped-ramp-equation" aria-live="polite"><strong>{activeClosedVUserDefinition.peak_vusers} peak VUsers · {formatModelTimeLabel(activeClosedVUserPlan.totalSeconds)} total</strong><span>{formatModelTimeLabel(activeClosedVUserPlan.rampUpSeconds)} ramp-up · {formatModelTimeLabel(activeClosedVUserPlan.sustainSeconds)} sustained{activeClosedVUserPlan.rampDownSeconds > 0 ? ` · ${formatModelTimeLabel(activeClosedVUserPlan.rampDownSeconds)} ramp-down` : ' · immediate stop'}</span></div>}
								</div>
							</section>
							{renderGuidedScenarioExtras()}
							<p className="scenario-assignment-copy secondary">Changes apply immediately. Review the total demand and duration in the preview, then continue.</p>
						</div>
					</>
				) : isSteppedRamp ? (
					<div className="stepped-ramp-worksheet" data-settings-tab={scenarioSettingsSection} id={`scenario-settings-${scenarioSettingsSection}-panel`} role="tabpanel" aria-labelledby={`scenario-settings-${scenarioSettingsSection}-tab`}>
						<div className="stepped-ramp-heading">
							<span>Guided worksheet</span>
							<h3>Stepped Ramp Setup</h3>
							<p>Define equal-duration load levels to compare throughput, latency, and errors as demand increases.</p>
						</div>
						<section className="stepped-ramp-section" data-tab="composition" aria-labelledby="stepped-range-title">
							<div className="stepped-ramp-number">1</div>
							<div className="stepped-ramp-section-body">
								<h4 id="stepped-range-title">Load range</h4>
								<p>Set the initial and final load levels. Run Phases controls total duration.</p>
								<div className="stepped-ramp-fields three">
									<label className="scenario-field"><span>Starting load level</span><div className="stepped-ramp-unit-input"><input className="settings-input" type="number" min={0} value={activeSteppedRampDefinition.start_vusers} onChange={event => updateSteppedRamp({ start_vusers: Math.max(0, Number(event.target.value) || 0) })} /><span>VUsers</span></div></label>
									<label className="scenario-field"><span>Ending load level</span><div className="stepped-ramp-unit-input"><input className="settings-input" type="number" min={1} value={activeSteppedRampDefinition.end_vusers} onChange={event => updateSteppedRamp({ end_vusers: Math.max(0, Number(event.target.value) || 0) })} /><span>VUsers</span></div></label>
								</div>
							</div>
						</section>
						<section className="stepped-ramp-section" data-tab="stages" aria-labelledby="stepped-increments-title">
							<div className="stepped-ramp-number">2</div>
							<div className="stepped-ramp-section-body">
								<h4 id="stepped-increments-title">Create equal steps</h4>
								<p>Choose how to define the load increase. The final increment may be smaller when the range is uneven.</p>
								<div className="stepped-ramp-choice" role="radiogroup" aria-label="Define ramp increments by">
									<button type="button" role="radio" aria-checked={activeSteppedRampDefinition.increment_by === 'step_count'} className={activeSteppedRampDefinition.increment_by === 'step_count' ? 'active' : ''} onClick={() => updateSteppedRamp({ increment_by: 'step_count' })}>Choose number of steps</button>
									<button type="button" role="radio" aria-checked={activeSteppedRampDefinition.increment_by === 'vusers_per_step'} className={activeSteppedRampDefinition.increment_by === 'vusers_per_step' ? 'active' : ''} onClick={() => updateSteppedRamp({ increment_by: 'vusers_per_step' })}>Choose VUsers per step</button>
								</div>
								<label className="scenario-field stepped-ramp-primary-input">
									<span>{activeSteppedRampDefinition.increment_by === 'step_count' ? 'Number of steps' : 'VUsers per step'}</span>
									<input className="settings-input" type="number" min={1} max={activeSteppedRampDefinition.increment_by === 'step_count' ? Math.min(MAX_STEPPED_RAMP_STEPS, Math.max(1, activeSteppedRampDefinition.end_vusers - activeSteppedRampDefinition.start_vusers)) : undefined} value={activeSteppedRampDefinition.increment_by === 'step_count' ? activeSteppedRampDefinition.step_count || 1 : activeSteppedRampDefinition.vusers_per_step || 1} onChange={event => updateSteppedRamp(activeSteppedRampDefinition.increment_by === 'step_count' ? { step_count: Math.max(1, Number(event.target.value) || 1) } : { vusers_per_step: Math.max(1, Number(event.target.value) || 1) })} />
								</label>
								{activeSteppedRampPlan.error ? <div className="settings-error-inline">{activeSteppedRampPlan.error}</div> : (
									<div className="stepped-ramp-equation" aria-live="polite">
										<strong>{activeSteppedRampDefinition.start_vusers} → {activeSteppedRampDefinition.end_vusers} VUsers</strong>
										<span>{activeSteppedRampPlan.stages.length} steps · +{activeSteppedRampPlan.increment} VUsers per step · {formatTimeCompact(activeSteppedRampPlan.intervalSeconds)} each</span>
										{activeSteppedRampPlan.partialFinalStep && <em>Final step uses a smaller increment to land exactly on {activeSteppedRampDefinition.end_vusers} VUsers.</em>}
									</div>
								)}
							</div>
						</section>
						<section className="stepped-ramp-section" data-tab="stages" aria-labelledby="stepped-protect-title">
							<div className="stepped-ramp-number">3</div>
							<div className="stepped-ramp-section-body">
								<h4 id="stepped-protect-title">Apply the model</h4>
								<p>Choose whether the same step model applies to every group or each group is configured independently.</p>
								<div className="stepped-ramp-scope" role="radiogroup" aria-label="Apply stepped ramp to">
									<button type="button" role="radio" aria-checked={steppedRampConfig.scope === 'scenario'} className={steppedRampConfig.scope === 'scenario' ? 'active' : ''} onClick={() => updateSteppedRamp(undefined, 'scenario')}><strong>Each group (same profile)</strong><span>Apply the same ramp to every group</span></button>
									<button type="button" role="radio" aria-checked={steppedRampConfig.scope === 'group'} className={steppedRampConfig.scope === 'group' ? 'active' : ''} onClick={() => updateSteppedRamp(undefined, 'group')}><strong>Configure each group</strong><span>Set a different ramp for each group</span></button>
								</div>
								{steppedRampConfig.scope === 'group' && formGroups.length > 1 && <TabList className="scenario-group-tabs" role="tablist" aria-label="Stepped ramp groups" onKeyDown={handleTabKey}>{formGroups.map((group, index) => <button key={`${group.script_id}-${index}-ramp`} type="button" className={`scenario-group-tab ${activeGroupIndex === index ? 'active' : ''}`} role="tab" aria-selected={activeGroupIndex === index} onClick={() => setStageGroupIndex(index)} tabIndex={(activeGroupIndex === index) ? 0 : -1}><strong>{group.name.trim() || `Group ${index + 1}`}</strong><span>{group.vusers} peak VUsers</span></button>)}</TabList>}
							</div>
						</section>
						{renderGuidedScenarioExtras()}
						<p className="scenario-assignment-copy secondary">Changes apply immediately. Review the total demand and duration in the preview, then continue.</p>
					</div>
				) : <>
          {renderScenarioSettingsTabs()}
          <div className="scenario-settings-section-panel" id={`scenario-settings-${scenarioSettingsSection}-panel`} role="tabpanel" aria-labelledby={`scenario-settings-${scenarioSettingsSection}-tab`}>
          {scenarioSettingsSection === 'composition' && <>
		  <ScenarioCompositionTimeline
			duration={form.duration}
			groups={compositionTimelineGroups}
			onOffsetChange={(index, value) => updateScenarioGroup(index, { start_offset: value })}
            selectedGroupIndex={activeGroupIndex}
            onGroupSelect={setStageGroupIndex}
          >
            <div className="scenario-assignment-header">
              <div>
                <h4>Load Per Group</h4>
                <p>{form.scenario_mode === 'goal_oriented' ? 'Review the current runtime seed estimate. Goal-oriented demand is controlled by throughput and guardrails.' : 'Set the VUser count and simple ramp for each script group participating in this scenario.'}</p>
              </div>
            </div>
            <div className="scenario-script-group-list scenario-load-group-panel">
                <div key={`${activeStageGroup.script_id || 'group'}-load-${activeGroupIndex}`} className="scenario-script-group-card">
                  <div className="scenario-script-group-title">
                    <strong>{activeGroupLabel}</strong>
                    <span className="scenario-assignment-copy secondary">{activeGroupScript?.name || 'Unassigned script'}</span>
                  </div>
                  <div className="settings-grid scenario-group-grid">
                    {form.scenario_mode === 'goal_oriented' ? <div className="scenario-field"><span>Current seed estimate</span><strong>{activeStageGroup.vusers} VUsers</strong><small>Read-only compatibility seed; adaptive runtime demand is governed by the goal controller.</small></div> : <label className="scenario-field">
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
                          aria-label={`${activeGroupLabel} VUsers`}
                          type="number"
                          min={1}
                          max={activeGroupVUserMax}
                          value={activeStageGroup.vusers}
                          onChange={event => updateScenarioGroup(activeGroupIndex, {
                            vusers: clampVUsersForRole(Number(event.target.value), activeStageGroup.role, 1),
                          })}
                        />
                      </div>
					</label>}
                  </div>
                </div>
            </div>
          </ScenarioCompositionTimeline>
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
		  </>}
          {scenarioSettingsSection === 'phases' && <div className="scenario-subpanel">
            <div className="scenario-assignment-header"><div><h4>Run Phases</h4><p>Set the run window and simple ramp timing. Use Ramp Pattern when you need explicit load stages.</p></div></div>
            <div className="settings-grid scenario-core-grid">
              <DurationField className="scenario-field" label="Duration" value={form.duration} disabled={form.termination_mode === 'iterations'} required showPresets onChange={value => {
                setForm(previous => ({ ...previous, duration: value }))
                setLoadModelValidation(null)
                setLoadModelValidationError('')
              }} />
              <DurationField className="scenario-field" label="Start Delay" value={form.start_delay} allowEmpty compact onChange={value => setForm(previous => ({ ...previous, start_delay: value }))} />
            </div>
            {formGroups.length > 1 && <label className="scenario-field">
              <span>Load group</span>
              <select className="settings-input" aria-label="Load group for run phases" value={activeGroupIndex} onChange={event => setStageGroupIndex(Number(event.target.value))}>
                {formGroups.map((group, index) => <option key={`${group.script_id || 'group'}-${index}`} value={index}>{group.name || `Group ${index + 1}`}</option>)}
              </select>
            </label>}
            <div className="settings-grid scenario-group-grid">
              <DurationField className="scenario-field" label="Start Offset" value={activeStageGroup.start_offset || ''} allowEmpty compact onChange={value => updateScenarioGroup(activeGroupIndex, { start_offset: value })} />
              <DurationField className="scenario-field" label="Ramp Up" value={activeStageGroup.ramp_up} allowEmpty compact disabled={activeStageGroup.stages.length > 0 || Boolean(activeProfileMapping)} onChange={value => updateScenarioGroup(activeGroupIndex, { ramp_up: value })} />
              <DurationField className="scenario-field" label="Ramp Down" value={activeStageGroup.ramp_down} allowEmpty compact disabled={activeStageGroup.stages.length > 0 || Boolean(activeProfileMapping)} onChange={value => updateScenarioGroup(activeGroupIndex, { ramp_down: value })} />
            </div>
            {(activeStageGroup.stages.length > 0 || activeProfileMapping) && <p className="scenario-assignment-copy secondary" role="note">{activeProfileMapping ? 'The traffic profile controls this group’s load changes.' : 'Explicit stages control this group’s load changes.'} Simple ramp timing is available when that pattern is removed.</p>}
          </div>}
          {scenarioSettingsSection === 'stages' && (
          <div className="scenario-subpanel">
            {formGroups.length > 1 && <label className="scenario-field">
              <span>Load group</span>
              <select className="settings-input" aria-label="Load group for advanced stages" value={activeGroupIndex} onChange={event => setStageGroupIndex(Number(event.target.value))}>
                {formGroups.map((group, index) => <option key={`${group.script_id || 'group'}-${index}`} value={index}>{group.name || `Group ${index + 1}`}</option>)}
              </select>
            </label>}
            {activeProfileMapping && (
              <div className="scenario-shape-guidance">
                <strong>{selectedShapeId === CSV_DRIVEN_SHAPE_ID ? 'Managed by CSV schedule' : 'Managed by production traffic profile'}</strong>
                <p>{activeProfileMapping.points.length.toLocaleString()} {form.traffic_profile?.runtime_target.toUpperCase()} target changes · peak {activeProfileMapping.peak_target}</p>
                <button type="button" className="btn btn-secondary" onClick={() => { setForm(previous => ({ ...previous, traffic_profile: undefined })); setSelectedShapeId('') }}>Remove profile</button>
              </div>
            )}
            <div hidden={Boolean(activeProfileMapping)}>
            <div className="scenario-assignment-header">
              <div>
                <h4>Ramp Pattern</h4>
                <p>Use simple ramp timing in Run Phases for common runs, or enable explicit stages when each interval needs its own VUser/TPS target.</p>
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
		  )}
            {scenarioSettingsSection === 'advanced' && <>
              {renderProtectRun()}
              {renderPreRunAssurance()}
              <TransactionResponseTimeSLASettings
                defaultMs={goalConfigFromForm(form).transaction_response_time_sla_ms}
                groups={formGroups.map((group, index) => ({
                  name: group.name || `Group ${index + 1}`,
                  scriptName: findScriptById(scripts, group.script_id)?.name,
                  overrideMs: group.transactionResponseTimeSlaMs,
                }))}
                onDefaultChange={value => setForm(previous => scenarioFormWithGoalField(previous, 'transaction_response_time_sla_ms', value))}
                onGroupOverrideChange={(index, value) => updateScenarioGroup(index, { transactionResponseTimeSlaMs: value })}
              />
            </>}
          </div>
						</>}
            </>)}
            {runSettingsTab === 'runtime' && (
              <>
              <label className="scenario-field"><span>Runtime scope</span><select aria-label="Runtime scope" className="settings-input" value={runtimeScope === 'defaults' ? 'defaults' : String(runtimeGroupIndex)} onChange={event => { setRuntimeScope(event.target.value); if (event.target.value !== 'defaults') setRuntimeGroupIndex(Number(event.target.value)) }}><option value="defaults">Scenario defaults</option>{formGroups.map((group, index) => <option key={index} value={index}>{group.name || `Group ${index + 1}`} — group overrides</option>)}</select></label>
              {runtimeScope !== 'defaults' ? <RuntimeSettingsGroupEditor
                hideGroupSelector
                title={`Runtime overrides · ${formGroups[runtimeGroupIndex]?.name || `Group ${runtimeGroupIndex + 1}`}`}
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
              /> : <RuntimeSettingsGroupEditor
                draftScope={`${activeProjectId}:${editingId || 'new-scenario'}:defaults`}
                title="Scenario runtime defaults"
                overrideLabel="Scenario default"
                description="These defaults apply to every script group unless that group overrides them. Choose a group in Runtime scope to inspect effective values and reset overrides. Unset fields inherit the script and engine defaults."
                hideApplyPanel
                groups={[{ name: 'Scenario defaults', scriptId: editingId || 'scenario-defaults' }]}
                focusedIndex={0}
                onFocusChange={() => {}}
                engineDefaults={engineDefaults}
                applicableProtocols={scenarioDefaultProtocols}
                overrides={[runtimeSettingsFromForm(form, showBrowserRuntimeSettings)]}
                onChange={(_, next) => applyScenarioDefaultRuntime(next)}
                onApplyToGroups={() => {}}
              />}
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
        <section className="scenario-wizard-panel scenario-data-editor">
          <div className="scenario-wizard-panel-header">
            <h4>Data Pools</h4>
            <p>Attach data pools for scripts that need coordinated parameter data.</p>
          </div>
          <details><summary>Create a data pool without leaving this draft</summary>
            {!canManageScripts && <p>Data pool creation requires editor permission. Ask a project administrator to create it in Settings → Data Pools.</p>}
            <p>Requires permission to manage data pools. Start with a JSON data row, or leave it empty and import CSV after creating the pool.</p>
            <label>New data pool name<input value={newPoolName} disabled={!!createdPoolId} onChange={event => setNewPoolName(event.target.value)} /></label>
            <label>First data row (JSON object)<textarea value={newPoolRow} onChange={event => setNewPoolRow(event.target.value)} /></label>
            <button className="btn btn-secondary" disabled={!canManageScripts || poolCreating || !newPoolName.trim()} onClick={async () => {
              const project = activeProjectId
              const generation = scenarioDraftGenerationRef.current
              const isCurrentDraft = () => currentProjectIdRef.current === project && scenarioDraftGenerationRef.current === generation
              if (!project) return
              setPoolCreating(true)
              try {
                const data: unknown = newPoolRow.trim() ? JSON.parse(newPoolRow) : null
                if (data !== null && (typeof data !== 'object' || Array.isArray(data) || Object.values(data).some(value => typeof value !== 'string'))) throw new Error('Enter a JSON object with string values for the first row.')
                const pool = createdPoolId ? dataPools.find(item => item.id === createdPoolId) : await api.createDataPool({ name: newPoolName.trim(), project_id: project })
                if (!pool || !isCurrentDraft()) return
                setCreatedPoolId(pool.id)
                setDataPools(previous => [...previous.filter(item => item.id !== pool.id), pool])
                if (data !== null) await api.createDataPoolRow(pool.id, { data: data as Record<string, string> })
                if (!isCurrentDraft()) return
                setForm(previous => ({...previous, data_pools: [...(previous.data_pools || []), {pool_id: pool.id, alias: pool.name.toLowerCase().replace(/\W+/g, '_'), access_mode: 'read_only', selection_strategy: 'sequential'}]}))
                setCreatedPoolId(null); setNewPoolName(''); setNewPoolRow(''); setError('')
              } catch (error) { if (isCurrentDraft()) setError(getErrorMessage(error, 'Unable to create data pool. Your scenario draft is retained.')) }
              finally { if (isCurrentDraft()) setPoolCreating(false) }
            }}>{poolCreating ? 'Creating…' : createdPoolId ? 'Retry adding first row' : 'Create and attach data pool'}</button>
          </details>

          {editingId && activeProjectId && <ScenarioMonitorBindings key={editingId} projectId={activeProjectId} scenarioId={editingId} canEdit={canManageTests} scenarioTag={scenarioBaseline?.etag} onSaved={() => void adoptBaselineAfterBindingsSave()} onScenarioChanged={() => setScenarioConflict(conflict => conflict || { reason: scenarioConflictReason(new ApiError('scenario changed', 412)), loading: false, current: null })} />}
          <ScenarioDataBindings bindings={scenarioBindings} pools={dataPools} scripts={scripts.filter(script => form.groups.some(group => group.script_id === script.id))} canManage={canManageScripts} onChange={updateDataBindings} />
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
          <button type="button" className="btn btn-secondary" onClick={validateLoadModel} disabled={sourcePending || savePending || loadModelValidationLoading || Boolean(blockingStageTimelineMessage || goalDrivenBlockingMessage || observerEnvelopeError || aggregateEnvelopeError || assignmentValidationError)}>
            {loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}
          </button>
        </div>
        <FormField className="scenario-field scenario-name-prompt" label="Scenario name" htmlFor="scenario-review-name" hint="Save a reusable draft now; validate it before creating a run.">
          <input className="settings-input" value={form.name} placeholder="Scenario name" aria-label="Scenario name" onChange={event => setForm({ ...form, name: event.target.value })} />
        </FormField>
        {blockedScriptGroups.length > 0 && <div className="scenario-source-pending" role="status"><strong>Script review required</strong>{blockedScriptGroups.map((group, index) => <p key={index}>{group.name}: {scriptReadiness.get(group.script_id)?.summary} <button className="btn btn-sm btn-secondary" onClick={() => setReviewScriptId(group.script_id)}>Review script</button></p>)}</div>}
        <div className="scenario-review-grid">

          <div className="scenario-review-card"><span>Shape</span><strong>{shapeLabel}</strong></div>
          <div className="scenario-review-card"><span>Scripts</span><strong>{formGroups.length} group{formGroups.length === 1 ? '' : 's'}</strong></div>
          <div className="scenario-review-card"><span>{form.termination_mode === 'iterations' ? 'Completion' : 'Duration'}</span><strong>{form.termination_mode === 'iterations' ? 'All per-user iterations' : form.duration}</strong></div>
          <div className="scenario-review-card"><span>Mode</span><strong>{form.scenario_mode === 'goal_oriented' ? 'Goal oriented' : 'VUser driven'}</strong></div>
          <div className="scenario-review-card"><span>Data</span><strong>{dataPoolLabel}</strong></div>
          <div className="scenario-review-card"><span>Shakeout</span><strong>{form.script_shakeout ? 'Enabled' : 'Disabled'}</strong></div>
        </div>
        <section className="scenario-review-details" aria-label="Runtime and placement review">
          <h4>Runtime and placement</h4>
          <p>Load window: {form.duration}, including ramp-up. Start delay: {form.start_delay || 'none'}. Resource baseline: {form.resource_baseline ? 'enabled' : 'disabled'}.</p>
          <p>Default pacing: {formatPacing(runtimeSettingsFromForm(form, true).pacing) || 'inherits script'}. Think time: {formatThinkTime(runtimeSettingsFromForm(form, true).think_time) || 'inherits script'}. Error handling: {formatErrorHandling(overrideFromRuntimeDraft(runtimeSettingsFromForm(form, true))?.error_handling) || 'inherits script'}.</p>
          <p>Scenario placement: {assignmentFromForm(form)?.mode || 'automatic'}{form.assignment_targets.length ? ` · Targets: ${form.assignment_targets.join(', ')}` : ''}.</p>
          <ul>{formGroups.map((group, index) => <li key={index}><strong>{group.name || `Group ${index + 1}`}</strong>: {group.iterations ? `${group.iterations} iterations` : 'duration limited'}; placement {group.assignment?.mode || 'inherits scenario'}; runtime {group.runtimeSettings ? 'group overrides configured' : 'inherits scenario defaults'}.
            <p>Pacing: {formatPacing(group.runtimeSettings?.pacing) || 'inherits defaults'}. Think time: {formatThinkTime(group.runtimeSettings?.think_time) || 'inherits defaults'}. {group.assignment?.selected_targets?.length ? `Selected targets: ${group.assignment.selected_targets.join(', ')}` : ''}</p>
            {group.stages.length > 0 && <p>{group.stages.map(stage => `${stage.name || stage.type}: ${stage.duration} → ${stage.target_vusers} VUsers`).join(' · ')}</p>}
          </li>)}</ul>
          <button type="button" className="btn btn-secondary" onClick={() => setWizardStep('run')}>Edit runtime and placement</button>
          <button type="button" className="btn btn-secondary" onClick={() => setWizardStep('data')}>Edit data bindings</button>
          <button type="button" className="btn btn-secondary" onClick={openScriptComposition}>Edit scripts and acceptance</button>
        </section>
        <NewTestReadiness projectId={activeProjectId || ''} scenario={{ id: editingId || 'authoring-draft', updated_at: '',
          scripts: formGroups.map((group, order) => ({ order, name: group.name, script_id: group.script_id, vusers: group.vusers, transaction_response_time_sla_ms: group.transactionResponseTimeSlaMs })),
          data_pools: scenarioBindings,
        }} compact />
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
        {loadModelValidationError && <div className="settings-error-inline" role="alert"><strong>Validation failed. </strong>{loadModelValidationError}</div>}
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
          <p className="scenario-assignment-copy secondary">{loadModelValidationError ? 'Resolve the reported issues, then validate again.' : 'Run validation to check demand, capacity, data fit, and generator placement.'}</p>
        )}
      </section>
    )
  }

  if (reviewScriptId) return <section aria-label="Review scenario script"><button className="btn btn-secondary" onClick={() => void runGuarded(() => { setReviewScriptId(null); void refresh() }, [scenarioBuilderGuard.id])}>Back to scenario draft</button><p>Your scenario draft is retained while you review this script.</p><NavigationGuardRunnerContext.Provider value={prerequisiteNavigationRunner}><ScriptsPage applicationRepository={applicationRepository} initialMode="edit" initialEditId={reviewScriptId} onScriptSaved={script => { setScripts(previous => previous.map(item => item.id === script.id ? script : item)); setReviewScriptId(null) }} /></NavigationGuardRunnerContext.Provider></section>

  if (prerequisiteScript) return <section aria-label="Create prerequisite script">
    <button className="btn btn-secondary" onClick={() => void runGuarded(() => setPrerequisiteScript(null), [scenarioBuilderGuard.id])}>Back to scenario draft</button>
    <p>Your scenario draft is retained. Saving a script returns it to the selected group.</p>
    <NavigationGuardRunnerContext.Provider value={prerequisiteNavigationRunner}><ScriptsPage applicationRepository={applicationRepository} initialMode="create-options" initialCreateWorkflow={prerequisiteScript} onScriptSaved={script => {
      setScripts(previous => [...previous.filter(item => item.id !== script.id), script])
      selectScriptFromLibrary(script)
      setPrerequisiteScript(null)
    }} /></NavigationGuardRunnerContext.Provider>
  </section>

  return (
    <div className="page">
      {assignmentNotice && <p role="status">{assignmentNotice}</p>}
      {assignmentScenarios && activeProjectId && canManageTests && <LibraryAssignmentDialog key={activeProjectId} projectId={activeProjectId} kind="scenarios" items={assignmentScenarios} onClose={() => setAssignmentScenarios(null)} onBusy={ids => setAssigningScenarioIds(new Set(ids))} onResult={(values, notice) => {
        refreshGenerationRef.current += 1
        setScenarios(current => current.map(scenario => values.has(scenario.id) ? { ...scenario, library_assignment: values.get(scenario.id) } : scenario))
        setSelectedScenarioIds(current => new Set([...current].filter(id => !values.has(id))))
        setAssignmentNotice(notice)
        void refresh()
      }} />}
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

      {!showForm && !showPlans && <label>Scenario archive state <select className="settings-input" value={archiveState} onChange={event => setArchiveState(event.target.value as typeof archiveState)}><option value="active">Active</option><option value="archived">Archived</option><option value="all">All</option></select></label>}
      {error && <div ref={errorRef} tabIndex={-1} className="settings-error-inline page-error-banner" role="alert">{error}</div>}
      {savedNotice && <p className="scenario-save-notice" role="status">{savedNotice}</p>}

      {authoringDraft && canManageTests && <section className="scenario-draft-recovery" aria-label="Saved browser draft">
        <strong>{authoringDraft.form.name || 'Untitled draft'}</strong>
        <p>Saved {new Date(authoringDraft.savedAt).toLocaleString()} on this browser, in this project. This checkpoint cannot run.</p>
        <button type="button" className="btn btn-secondary" onClick={() => { if (scenarioBuilderDirty) runGuarded(resumeAuthoringDraft); else resumeAuthoringDraft() }}>Resume draft</button>
        <button type="button" className="btn btn-ghost" onClick={() => { try { clearScenarioAuthoringDraft(activeProjectId || ''); setAuthoringDraft(null); checkpointBaselineRef.current = '' } catch (cause) { setError(getErrorMessage(cause, 'Unable to clear browser draft.')) } }}>Clear saved draft</button>
      </section>}
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
        <fieldset disabled={savePending} className="page-section scenario-builder">
          <div className="scenario-builder-header">
            <div>
              {form.name.trim()
                ? <p className="scenario-builder-name" aria-label="Scenario name">{form.name.trim()}</p>
                : <p className="page-subtitle">Choose scripts, pick a load model, tune run settings, attach data, and validate the planned model.</p>}
            </div>
            <div className="scenario-builder-progress">
              <span>{editingId ? (scenarioBuilderDirty ? 'Editing · Unsaved changes' : 'Editing · Saved') : (checkpointBaselineRef.current === checkpointFingerprint ? 'Browser draft · Saved locally' : 'New scenario · Unsaved')}</span>
              <details className="ux-builder-readiness"><summary>Readiness</summary><p>{sourceStepComplete ? 'Model configured' : 'Model incomplete'} · {validationLabel}</p></details>
            </div>
          </div>

          <p className="setting-desc">Checkmarks indicate valid fields, including defaults; they do not indicate an operator review.</p><div className="scenario-wizard-stepper" aria-label="Scenario builder steps">
            {scenarioWizardSteps.map((step, index) => {
              const needsAttention = step.id === 'scripts' && blockedScriptGroups.length > 0
              const stepComplete = !needsAttention && index <= maxVisitedWizardIndex && wizardStepCompletion[step.id]
              return (
                <button
                  key={step.id}
                  type="button"
                  className={`scenario-wizard-step ${wizardStep === step.id ? 'active' : ''} ${stepComplete ? 'complete' : ''}`}
                  onClick={() => needsAttention ? openScriptComposition() : setWizardStep(step.id)}
                  aria-current={wizardStep === step.id ? 'step' : undefined}
                  aria-label={`${step.label}${needsAttention ? ' needs attention' : stepComplete ? ' valid' : ''}`}
                >
                  <span className="scenario-wizard-step-marker" aria-hidden="true">{needsAttention ? '!' : stepComplete ? '✓' : index + 1}</span>
                  <span className="scenario-wizard-step-label">{step.label}</span>
                </button>
              )
            })}
          </div>

		  <div className={`scenario-builder-workspace ${showModelPreview ? 'with-preview' : ''} ${isSteppedRamp || isGuidedClosedVUser || isGoalDriven ? 'stepped-ramp-layout' : ''} ${showCompositionPreview ? 'scenario-composition-layout' : ''}`}>
            <div className="scenario-builder-main" ref={wizardPanelRef} tabIndex={-1} role="region" aria-label={`Scenario step: ${wizardStep}`}>
              {sourcePending && wizardStep === 'source' && <p role="status" className="scenario-source-pending">{sourcePendingMessage}</p>}
              {renderWizardStep()}
              <div hidden={wizardStep !== 'data'} style={wizardStep !== 'data' ? { display: 'none' } : undefined}>
                <MonitorRuleEditor key={`${activeProjectId}:${scenarioDraftGenerationRef.current}`} projectId={activeProjectId} value={form.goal_config?.monitor_rules || []} canEdit={canManageTests} onChange={rules => setForm(previous => scenarioFormWithGoalField(previous, 'monitor_rules', rules))} onValidationChange={setMonitorEditorError} />
              </div>
            </div>

            {showModelPreview && (
              <aside className="scenario-builder-preview">
                {goalSizingIncomplete && <p className="scenario-source-pending" role="status">Sizing incomplete: enter average response time for every group or select a completed baseline. This chart shows requested throughput only; VUser sizing is unavailable until those inputs are supplied.</p>}
                <ScenarioModelPreviewPanel
                  model={scenarioModel}
                  shapeLabel={shapeLabel}
                  dataPoolLabel={dataPoolLabel}
                  validationStatus={goalSizingIncomplete && validationLabel === 'Not validated' ? 'Sizing incomplete' : validationLabel}
                  compositionGroups={showCompositionPreview ? compositionTimelineGroups : undefined}
                />
				{isSteppedRamp && !activeSteppedRampPlan.error && (
					<div className="stepped-ramp-stage-preview">
						<div className="stepped-ramp-stage-preview-header"><strong>Load levels</strong><span>Live preview</span></div>
						<table>
							<thead><tr><th>Step</th><th>Time range</th><th>VUsers</th><th>Duration</th></tr></thead>
							<tbody>{activeSteppedRampPlan.stages.map((stage, index) => {
								const start = index * activeSteppedRampPlan.intervalSeconds
								const end = start + activeSteppedRampPlan.intervalSeconds
								return <tr key={`${stage.name}-${index}`}><td>{index + 1}</td><td>{formatModelTimeLabel(start)}–{formatModelTimeLabel(end)}</td><td>{stage.target_vusers}</td><td>{formatModelTimeLabel(activeSteppedRampPlan.intervalSeconds)}</td></tr>
							})}</tbody>
						</table>
						<p>Metrics are tagged with each active load level for live and archived comparison.</p>
					</div>
				)}
				{isGuidedClosedVUser && !activeClosedVUserPlan.error && (
					<div className="stepped-ramp-stage-preview closed-vuser-phase-preview">
						<div className="stepped-ramp-stage-preview-header"><strong>Run phases</strong><span>Live preview</span></div>
						<table>
							<thead><tr><th>Phase</th><th>Time range</th><th>Target</th><th>Duration</th></tr></thead>
							<tbody>{activeClosedVUserPlan.stages.map((stage, index) => {
								const start = activeClosedVUserPlan.stages.slice(0, index).reduce((sum, item) => sum + parseDurationToSeconds(item.duration), 0)
								const duration = parseDurationToSeconds(stage.duration)
								return <tr key={`${stage.name}-${index}`}><td>{stage.name}</td><td>{formatModelTimeLabel(start)}–{formatModelTimeLabel(start + duration)}</td><td>{stage.target_vusers} VUsers</td><td>{formatModelTimeLabel(duration)}</td></tr>
							})}</tbody>
						</table>
						<p>{activeClosedVUserDefinition.ramp_down_enabled ? 'The group returns continuously to zero before the run ends.' : 'The group stops immediately after the sustained-load phase.'}</p>
					</div>
				)}
				{isGoalDriven && (
					<div className="stepped-ramp-stage-preview goal-driven-preview-details">
						<div className="stepped-ramp-stage-preview-header"><strong>Adaptive demand plan</strong><span>Live preview</span></div>
						<table><thead><tr><th>Group</th><th>Target TPS</th><th>ART</th><th>Seed estimate</th></tr></thead><tbody>{formGroups.map((group, index) => { const definition = goalDrivenConfig.groups[goalDrivenGroupKey(index)]; return <tr key={`goal-preview-${index}`}><td>{group.name.trim() || `Group ${index + 1}`}</td><td>{definition?.target_tps || 0}</td><td>{definition?.art ? `${definition.art.art_ms.toLocaleString()} ms` : 'Not set'}</td><td>{definition?.art ? `${goalDrivenEstimatedVUsers[index]} VUsers` : 'Unavailable — set ART'}</td></tr> })}</tbody></table>
						<p>Guardrail range {form.goal_min_vusers ?? totalGoalDrivenEstimatedVUsers}–{form.goal_max_vusers ?? Math.max(2, totalGoalDrivenEstimatedVUsers * 2)} VUsers. Actual demand adapts; the chart represents requested TPS.</p>
					</div>
				)}
              </aside>
            )}
          </div>

          {renderSourceScenarioRunsPanel()}

          <FormErrorSummary issues={[
            ...[blockingStageTimelineMessage, steppedRampBlockingMessage, closedVUserBlockingMessage, goalDrivenBlockingMessage].map(message => ({ message: message || '', onResolve: () => setWizardStep('source') })),
            ...[observerEnvelopeError, aggregateEnvelopeError].map(message => ({ message: message || '', onResolve: () => setWizardStep('run') })),
            ...[assignmentValidationError, scenarioGroupNameError].map(message => ({ message: message || '', onResolve: openScriptComposition })),
          ]} />

          {editingId && scenarioConflict && <AuthoringRevisionConflict kind="scenario" reason={scenarioConflict.reason} hasCurrent={Boolean(scenarioConflict.current)} loading={scenarioConflict.loading}
            diff={scenarioConflictDiff}
            reapplyNote="Reapply carries your draft's changes onto this reviewed scenario. Locally changed fields and group lists take precedence over the current values; review them before saving."
            onLoad={() => void loadScenarioConflictBaseline()} onReload={reloadScenarioFromConflict} onReapply={reapplyScenarioConflictDraft} />}

          <div className="scenario-builder-actions" aria-busy={savePending}><span className="ux-draft-help">{newScenarioLoadUnconfigured
            ? 'Save draft keeps this scenario in this browser until its load is set in Model & Shape or Run Settings; it is not added to the project or launchable yet.'
            : 'Incomplete or blocked drafts are saved in this browser. Configured scenarios are saved to the project; neither launches a run.'}</span>
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
            {wizardStep !== 'review' && <button
              className="btn btn-primary"
              onClick={() => {
                const nextIndex = Math.min(scenarioWizardSteps.length - 1, currentWizardIndex + 1)
                setWizardStep(scenarioWizardSteps[nextIndex].id)
              }}
              disabled={savePending || currentWizardIndex === scenarioWizardSteps.length - 1 || (wizardStep === 'source' && !sourceStepComplete) || (wizardStep === 'run' && !wizardStepCompletion.run) || (wizardStep === 'scripts' && !wizardStepCompletion.scripts)}
            >
              Next
            </button>}
            {wizardStep === 'review' && onCreateTestFromScenario && (
              <button className="btn btn-primary" onClick={saveScenarioAndCreateTest} disabled={savePending || !requiredDraftReady || goalSizingIncomplete || sourcePending || loadModelValidationLoading || !!loadModelValidationError || loadModelValidation?.status === 'blocked' || blockedScriptGroups.length > 0 || bindingErrors.length > 0 || Boolean(blockingStageTimelineMessage || steppedRampBlockingMessage || closedVUserBlockingMessage || goalDrivenBlockingMessage || observerEnvelopeError || aggregateEnvelopeError || assignmentValidationError || scenarioGroupNameError)}>Save and configure run</button>
            )}
            <button className="btn btn-secondary" onClick={saveScenario} disabled={!canManageTests || savePending || (!builderHasProgress && !editingId)}>{savePending ? 'Saving…' : 'Save draft'}</button>
          </div>

        </fieldset>
      )}


      {showScenarioLibrary && (
      <div className="page-section">
        <LibraryFederationControls
          kind="scenario"
            filtersActive={assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all' || Boolean(scenarioQuery.trim()) || scenarioReleaseFilter !== 'all' || scenarioFederation.sourceFilter !== 'all'}
          onResetFilters={() => { setAssignedApplicationFilter('all'); setAssignedReleaseFilter('all'); setScenarioQuery(''); setScenarioReleaseFilter('all'); scenarioFederation.setSourceFilter('all') }}
          primaryAction={canManageTests && <button className="btn btn-primary" onClick={openCreate}>Create scenario</button>}
          federation={scenarioFederation}
          projectName={activeProject?.name}
          onConfigureRemotes={openRemoteSettings}
          summaryText={`${scenarioPagination.total} matching scenario${scenarioPagination.total === 1 ? '' : 's'} · Project library across applications and releases.`}
          searchControl={(<label className="project-redaction-rule-field">
                <span>Search scenarios</span>
                <input
                  className="settings-input"
                  type="search"
                  value={scenarioQuery}
                  onChange={(event) => setScenarioQuery(event.target.value)}
                  placeholder="Find scenarios, scripts, or data pools..."
                />
              </label>)}
            filterControls={(
            <>
              <LibraryAssignmentFilters items={scenarios} application={assignedApplicationFilter} release={assignedReleaseFilter} onApplication={setAssignedApplicationFilter} onRelease={setAssignedReleaseFilter} />
              <label className="project-redaction-rule-field">
                <span>Run release</span>
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

            </>
          )}
        />

        {canManageTests && selectedVisibleScenarioIds.length > 0 && (
          <BulkActions count={selectedVisibleScenarioIds.length}>
              <button className="btn btn-secondary" disabled={selectedVisibleScenarioIds.some(id => deletingScenarioIds.has(id) || assigningScenarioIds.has(id))} onClick={() => setAssignmentScenarios(scenarios.filter(scenario => selectedVisibleScenarioIds.includes(scenario.id)))}>Change application/releases…</button>
              {selectedVisibleScenarioIds.length < visibleScenarioIds.length && <button type="button" className="btn btn-secondary" onClick={() => toggleAllVisibleScenarios(true)}>Select all {visibleScenarioIds.length} matching scenarios</button>}
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
                disabled={selectedVisibleScenarioIds.length === 0 || selectedVisibleScenarioIds.some(id => deletingScenarioIds.has(id) || assigningScenarioIds.has(id))}
              >
                Delete Selected ({selectedVisibleScenarioIds.length})
              </button>
            </BulkActions>
        )}
        <LibraryPagination label="Scenario library" pagination={scenarioPagination} />
        <div className="data-table scenario-library-table" role="region" aria-label="Scenario catalog" tabIndex={0}>
          <table data-table-pagination="off">
            <thead>
              <tr>
                {canManageTests && (
                  <th className="library-selection-column">
                    <SelectAllCheckbox
                      selectedCount={pageScenarios.filter(scenario => selectedScenarioIds.has(scenario.id)).length}
                      visibleCount={pageScenarios.length}
                      ariaLabel="Select all scenarios on this page"
                      onChange={checked => setSelectedScenarioIds(current => { const next = new Set(current); pageScenarios.forEach(scenario => checked ? next.add(scenario.id) : next.delete(scenario.id)); return next })}
                    />
                  </th>
                )}
                <th>Source</th>
                <SortHeader label="Scenario" field="name" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <SortHeader label="Workload" field="scripts" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                <th>Assigned application/releases</th>
                <SortHeader label="Run releases" field="associated_releases" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                <SortHeader label="Updated" field="updated_at" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {pageScenarios.map(scenario => {
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
                          disabled={deletingScenarioIds.has(scenario.id) || assigningScenarioIds.has(scenario.id)}
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
                    <td aria-label="Scenario workload">
                      <div className="scenario-table-workload">
                        <strong>{formatScenarioScripts(scenario)} · {scenario.duration}</strong><span>{scenario.scripts.reduce((total, group) => total + Math.max(group.vusers || 0, ...((group.stages || []).map(stage => stage.target_vusers || 0))), 0)} VUsers (sum of group peaks) · {scenario.workload_shape_id || "Custom model"}</span>
                        <span>{formatScenarioDataPools(scenario)}</span>
                        <span>{formatScenarioResultCount(scenario)}</span><span>{scenario.scripts.some(group => scriptReadiness.get(group.script_id)?.blocked) ? 'Script review required' : 'Validate before creating a run'}</span>
                      </div>
                    </td>
                    <td aria-label="Assigned application/releases">{assignmentSummary(scenario.library_assignment)}</td>
                    <td aria-label="Scenario releases">
                      <div className="scenario-table-workload">
                        <strong>{releaseList(scenario)}</strong>
                      </div>
                    </td>
                    <td className="cell-muted">{formatDate(scenario.updated_at || scenario.created_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-secondary" onClick={(event) => {
                        event.stopPropagation()
                        void toggleScenarioRuns(scenario)
                      }}>{expandedScenarioId === scenario.id ? 'Hide Runs' : 'View Runs'}</button>
                      <ObjectArchiveControls resourceType="scenario" resourceId={scenario.id} archivedAt={scenario.archived_at} updatedAt={scenario.updated_at} canManage={canManageTests} onChanged={refresh} />
                      {canManageTests ? (
                        <>
                          {onCreateTestFromScenario && (
                            <button className="btn btn-sm btn-secondary" disabled={!!scenario.archived_at} aria-label={`New test from ${scenario.name}`} onClick={(event) => { event.stopPropagation(); onCreateTestFromScenario(scenario.id) }}>New test</button>
                          )}
                          <button className="btn btn-sm btn-ghost" disabled={!!scenario.archived_at || openingScenarioId === scenario.id} onClick={(event) => { event.stopPropagation(); if (!assigningScenarioIds.has(scenario.id)) void openEdit(scenario) }}>{openingScenarioId === scenario.id ? 'Opening…' : 'Edit'}</button>
                          <RowActions className="scenario-more-actions"><button className="btn btn-sm btn-ghost" disabled={!!scenario.archived_at || deletingScenarioIds.has(scenario.id) || assigningScenarioIds.has(scenario.id)} onClick={() => setAssignmentScenarios([scenario])}>Change application/releases…</button><button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); duplicateScenario(scenario) }}>Duplicate</button>
                          <button className="btn btn-sm btn-ghost" disabled={!!scenario.archived_at} onClick={(event) => { event.stopPropagation(); addScenarioToPlan(scenario.id) }} title="Seed a new execution plan with this scenario">Add to plan</button>
                          <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} disabled={deletingScenarioIds.has(scenario.id) || assigningScenarioIds.has(scenario.id)} onClick={(event) => { event.stopPropagation(); setConfirmDeleteScenarioId(scenario.id) }}>{deletingScenarioIds.has(scenario.id) ? 'Deleting…' : 'Delete'}</button>
                          <button
                            className="btn btn-sm btn-secondary"
                            onClick={(event) => { event.stopPropagation(); void scenarioFederation.pushLocalItem({ kind: 'scenario', id: scenario.id, name: scenario.name }) }}
                            disabled={scenarioFederation.actionBlocked || conflict}
                            title={conflict ? 'Compare this conflict before transferring.' : scenarioFederation.actionBlockedReason}
                          >Push</button>
                          {hostItem?.local_item_id && (
                            <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); void scenarioFederation.compareItems(scenario.id, hostItem) }}>Compare</button>
                          )}</RowActions>
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                  {expandedScenarioId === scenario.id && (
                    <tr className="no-hover">
                      <td colSpan={canManageTests ? 8 : 7} style={{ background: 'var(--bg-primary)' }}>
                        <ObjectHistory key={scenario.id} projectId={scenario.project_id} resourceType="scenario" resourceId={scenario.id} activity={scenario.activity} />
                        {renderScenarioRunsContent(scenario)}
                      </td>
                    </tr>
                  )}
                </Fragment>
                )
              })}
              {pageRemoteScenarios.map(item => {
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
                    <td aria-label="Library dependencies">
                      <div className="scenario-table-workload">
                        <span>{item.dependencies?.length ? `${item.dependencies.length} dependencies` : 'No referenced scripts'}</span>
                      </div>
                    </td>
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
                  <td colSpan={canManageTests ? 8 : 7} className="cell-muted">
                    {assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all' || scenarioQuery.trim() || scenarioReleaseFilter !== 'all' ? 'No scenarios match these filters.' : 'No scenarios yet. Create a scenario from an existing script.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      )}

      {confirmDeleteScenarioId && (
        <DialogLayer><div className="modal-overlay" {...dialogBackdropProps(closeDeleteScenarioDialog, 'Close scenario deletion dialog')}>
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
        </div></DialogLayer>
      )}
    </div>
  )
}

export default memo(TestHub)
