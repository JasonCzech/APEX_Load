import type { MonitorBinding } from '../types/runMonitoring'
import type { GoalConfig, LoadStageDraft, ScenarioDraft, ScenarioDraftGroup, StoredScenario } from '../types'
import type { ScenarioFormGroup, ScenarioFormState } from './TestHub'
import type { CompositionTimelineGroup } from './ScenarioCompositionTimeline'
import { assignmentFromForm, runtimeSettingsFromForm, scenarioFormStateFromStoredScenario } from './scenarioFormModel'
import { buildCreateTestPayload, buildScenarioUpsertPayload, type CreateTestPayload, type ScenarioUpsertPayload } from './scenarioPayloadUtils'
import { buildRunLoadProfilePreview, type RunLoadProfilePreview, type ScenarioScriptGroup } from './newTestRunModel'
import { scenarioDraftReducer } from './scenarioDraftUtils'

// Scenario assembly (A6). ScenarioDraft is the one owner of an authored
// scenario: every surface that creates or edits one (CreateTestModal, the
// queued-run revision editor, TestHub and NewTestPage) builds its draft with a
// constructor here and derives every request and view from the projections
// here, so a field is assembled one way everywhere. A draft keeps authored
// absence (inherit), explicit false/zero/clear and legacy unknown values
// apart; effective values a surface displays are never written back as
// authored overrides.

// ---- Constructors ----------------------------------------------------------

export function maxStageVUsers(stages: LoadStageDraft[]) {
  return stages.reduce((max, stage) => Math.max(max, Number(stage.target_vusers) || 0), 0)
}

function normalizeStage(stage: LoadStageDraft): LoadStageDraft {
  return {
    ...(stage.name ? { name: stage.name } : {}),
    type: stage.type,
    duration: stage.duration,
    ...(stage.target_vusers !== undefined ? { target_vusers: Number(stage.target_vusers) || 0 } : {}),
    ...(stage.target_tps !== undefined ? { target_tps: Number(stage.target_tps) || 0 } : {}),
  }
}

/** A TestHub form group as a draft group. */
export function scenarioDraftGroupFromForm(group: ScenarioFormGroup): ScenarioDraftGroup {
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
    stages: group.stages.map(normalizeStage),
    // A runtime iteration override and the compact legacy field are alternate
    // representations of the same setting. Never serialize both: the backend
    // rejects mismatched values and the runtime editor promotes edits into
    // run_logic.
    iterations: runtimeOwnsIterations ? undefined : group.iterations,
    // Says which representation owns the iteration, so a draft that later
    // displays the runtime value as a group field never serializes it twice.
    ...(runtimeOwnsIterations ? { iterationPayloadSource: 'runtime' as const } : group.iterations !== undefined ? { iterationPayloadSource: 'top_level' as const } : {}),
    initialization: group.initialization,
    runtimeSettings: group.runtimeSettings,
    assignment: group.assignment,
    transactionResponseTimeSlaMs: group.transactionResponseTimeSlaMs,
  }
}

/** The goal configuration a TestHub form authors. */
export function goalConfigFromForm(form: ScenarioFormState): GoalConfig {
  // Preserve authored absence and explicit nested values during unrelated edits.
  const goal: GoalConfig = structuredClone(form.goal_config || {})
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

/**
 * The draft a TestHub form authors. TestHub keeps its flat form as the
 * editing view; this is the only way its values become a scenario.
 * includeBrowser says whether the browser runtime controls apply.
 */
export function scenarioDraftFromForm(form: ScenarioFormState, options: { includeBrowser: boolean }): ScenarioDraft {
  return {
    name: form.name,
    duration: form.termination_mode === 'iterations' ? '' : form.duration || '1m',
    ...(form.termination_mode ? { terminationMode: form.termination_mode } : {}),
    startDelay: form.start_delay,
    scenarioMode: form.scenario_mode,
    scriptShakeout: form.script_shakeout,
    resourceBaseline: form.resource_baseline,
    assignment: assignmentFromForm(form),
    goalConfig: goalConfigFromForm(form),
    runtimeSettings: runtimeSettingsFromForm(form, options.includeBrowser),
    groups: form.groups.map(scenarioDraftGroupFromForm),
    ...(form.traffic_profile ? { trafficProfile: form.traffic_profile } : {}),
    dataPools: (form.data_pools || []).map(binding => ({ ...binding, access_mode: 'read_only' as const })),
  }
}

/**
 * The draft of a saved scenario, as TestHub and NewTestPage load it. A saved
 * scenario without groups stays without groups (the editor's starter group is
 * an editing aid, not part of the scenario).
 */
export function scenarioDraftFromStoredScenario(scenario: StoredScenario, options: { fallbackScriptId?: string; includeBrowser: boolean }): ScenarioDraft {
  const draft = scenarioDraftFromForm(scenarioFormStateFromStoredScenario(scenario, options.fallbackScriptId || ''), options)
  return scenario.scripts?.length ? draft : { ...draft, groups: [] }
}

// ---- Group identity --------------------------------------------------------

/** Moves a group; see scenarioDraftReducer's moveGroup. */
export function moveScenarioDraftGroup(draft: ScenarioDraft, from: number, to: number): ScenarioDraft {
  return scenarioDraftReducer(draft, { type: 'moveGroup', from, to })
}

// ---- Projections -----------------------------------------------------------

export type ScenarioUpsertContext = {
  projectId?: string
  workloadShapeId?: string
  /** Replaces the draft's name when it is blank (an unnamed draft still saves). */
  fallbackName?: string
}

/** The scenario save request. */
export function scenarioUpsertPayload(draft: ScenarioDraft, context: ScenarioUpsertContext = {}): ScenarioUpsertPayload {
  return buildScenarioUpsertPayload({
    name: draft.name || context.fallbackName || '',
    projectId: context.projectId,
    workloadShapeId: context.workloadShapeId,
    duration: draft.duration,
    termination_mode: draft.terminationMode,
    startDelay: draft.startDelay,
    scenarioMode: draft.scenarioMode,
    scriptShakeout: draft.scriptShakeout,
    resourceBaseline: draft.resourceBaseline,
    assignment: draft.assignment,
    groups: draft.groups,
    goalConfig: draft.goalConfig,
    runtimeSettings: draft.runtimeSettings,
    trafficProfile: draft.trafficProfile,
    dataPools: draft.dataPools,
  })
}

export type ScenarioCreateContext = {
  projectId?: string
  folderId?: string | null
  monitorBindings?: MonitorBinding[]
  elkConnectionId?: string
  dynatraceConnectionId?: string
}

/**
 * The run creation request. A revision draft carries its source run's
 * context: ownership fields come from it, and source-owned values the draft
 * explicitly emptied are sent as clears rather than omitted (omission
 * inherits).
 */
export function scenarioCreatePayload(draft: ScenarioDraft, context: ScenarioCreateContext = {}): CreateTestPayload {
  const revision = draft.revisionContext
  return buildCreateTestPayload({
    name: draft.name,
    duration: draft.duration,
    termination_mode: draft.terminationMode,
    startDelay: draft.startDelay,
    scenarioMode: draft.scenarioMode,
    scriptShakeout: draft.scriptShakeout,
    resourceBaseline: draft.resourceBaseline,
    assignment: draft.assignment,
    groups: draft.groups,
    goalConfig: draft.goalConfig,
    runtimeSettings: draft.runtimeSettings,
    trafficProfile: draft.trafficProfile,
    applicationId: revision?.applicationId,
    releaseId: revision?.releaseId,
    scenarioId: revision?.scenarioId,
    revisionSourceTestId: revision?.sourceTestId,
    sourceGoalConfigPresent: revision?.sourceGoalConfigPresent,
    sourceRuntimeSettingsPresent: revision?.sourceRuntimeSettingsPresent,
    sourceStartDelayPresent: revision?.sourceStartDelayPresent,
    projectId: revision?.projectId ?? context.projectId,
    folderId: revision ? revision.folderId : context.folderId,
    dataPools: revision ? revision.dataPools : draft.dataPools,
    monitorBindings: context.monitorBindings,
    elkConnectionId: context.elkConnectionId,
    dynatraceConnectionId: context.dynatraceConnectionId,
  })
}

/**
 * The capacity estimate request: the run the draft would create. A host
 * selection without hosts is still being authored, so it is estimated as
 * automatic placement rather than refused.
 */
export function scenarioEstimateRequest(draft: ScenarioDraft, context: ScenarioCreateContext = {}): CreateTestPayload {
  const pending = draft.assignment.mode === 'specific_hosts' && (draft.assignment.selected_targets || []).length === 0
  return scenarioCreatePayload(pending ? { ...draft, assignment: { mode: 'auto', selected_targets: [] } } : draft, context)
}

/**
 * The groups of the live composition timeline: the draft groups exactly as
 * they would be saved.
 */
export function scenarioTimelineGroups(groups: ScenarioDraftGroup[]): CompositionTimelineGroup[] {
  return groups.map(group => ({
    name: group.name,
    script_id: group.scriptId,
    ...(group.startOffset ? { start_offset: group.startOffset } : {}),
    ...(group.rampUp ? { ramp_up: group.rampUp } : {}),
    ...(group.rampDown ? { ramp_down: group.rampDown } : {}),
    vusers: group.vusers,
    ...(group.stages?.length ? { stages: group.stages } : {}),
    ...(group.role ? { role: group.role } : {}),
  }))
}

/** The planned load profile of a draft (VUsers over time, baseline phases). */
export function scenarioLoadPreview(draft: ScenarioDraft): RunLoadProfilePreview {
  const groups: ScenarioScriptGroup[] = draft.groups.map((group, order) => ({
    script_id: group.scriptId,
    name: group.name,
    order,
    vusers: group.vusers,
    ramp_up: group.rampUp,
    ramp_down: group.rampDown,
    start_offset: group.startOffset,
    stages: group.stages,
    initialization: group.initialization,
    ...(group.role ? { role: group.role } : {}),
  }))
  return buildRunLoadProfilePreview(groups, draft.duration, draft.startDelay, draft.resourceBaseline, draft.trafficProfile, draft.scenarioMode, draft.goalConfig.target_average_tps)
}
