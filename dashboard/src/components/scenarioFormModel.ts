import type {
  GeneratorAssignmentSettings,
  GoalConfig,
  RuntimeSettingsOverride,
  ScenarioRuntimeSettingsDraft,
  StoredScenario,
} from '../types'
import type { ScenarioFormGroup, ScenarioFormState } from './TestHub'
import type { ScenarioSeed } from './scenarioWizardUtils'
import { overrideFromRuntimeDraft, runtimeDraftFromOverride } from './runtimeSettingsModel'
import { canonicalThinkTimeType, thinkTimeConfigForType } from './thinkTimeConfig'

export function cloneAssignment(
  assignment?: GeneratorAssignmentSettings,
): GeneratorAssignmentSettings | undefined {
  if (!assignment) return undefined
  return {
    ...assignment,
    java_host: assignment.java_host ? { ...assignment.java_host } : undefined,
    selected_targets: assignment.selected_targets ? [...assignment.selected_targets] : undefined,
    region_weights: assignment.region_weights ? { ...assignment.region_weights } : undefined,
    load_zone_weights: assignment.load_zone_weights ? { ...assignment.load_zone_weights } : undefined,
    required_labels: assignment.required_labels ? { ...assignment.required_labels } : undefined,
  }
}

function emptyScenarioGroup(): ScenarioFormGroup {
  return {
    name: '',
    script_id: '',
    vusers: 10,
    ramp_up: '',
		ramp_down: '',
		start_offset: '',
    stages: [],
    initialization: 'at_runtime',
  }
}

/**
 * True when applying `incomingGroups` would replace or drop a script the operator
 * already chose. Groups without a script are free to be filled.
 */
export function scenarioScriptConflict(
  currentGroups: Array<Pick<ScenarioFormGroup, 'script_id'>>,
  incomingGroups: Array<Pick<ScenarioFormGroup, 'script_id'>>,
): boolean {
  return currentGroups.some((group, index) => Boolean(group.script_id) && incomingGroups[index]?.script_id !== group.script_id)
}

export function ensureScenarioGroups(groups: ScenarioFormGroup[]): ScenarioFormGroup[] {
  const nextGroups = groups.length > 0 ? groups : [emptyScenarioGroup()]
  return nextGroups.map((group, index) => ({
    ...group,
    name: group.name || (index === 0 ? '' : `Group ${index + 1}`),
    script_id: group.script_id || '',
    vusers: Number(group.vusers) || 10,
    ramp_up: group.ramp_up || '',
		ramp_down: group.ramp_down || '',
		start_offset: group.start_offset || '',
    stages: group.stages || [],
    initialization: group.initialization || 'at_runtime',
    assignment: group.assignment,
  }))
}

/**
 * Rebuild the shape-controlled group fields without discarding advanced group
 * configuration already authored by the operator.
 */
export function reseedScenarioGroups(
  seedGroups: ScenarioSeed['groups'],
  previousGroups: ScenarioFormGroup[],
): ScenarioFormGroup[] {
  const groups = seedGroups && seedGroups.length > 0 ? seedGroups : [{ name: '', vusers: 10 }]
  const seededGroups = groups.map((group, index) => {
    const previous = previousGroups[index]
    return {
      name: group.name || previous?.name || (index === 0 ? '' : `Group ${index + 1}`),
      // A newly introduced shape group has no safe script mapping. Reusing the
      // first group's script silently turns an incomplete shape into an
      // executable duplicate workload.
      script_id: previous?.script_id || '',
      role: previous?.role,
      vusers: group.vusers || previous?.vusers || 10,
      ramp_up: group.ramp_up || '',
			ramp_down: group.ramp_down || '',
			start_offset: previous?.start_offset || '',
      stages: group.stages ? structuredClone(group.stages) : [],
      iterations: group.iterations ?? previous?.iterations,
      initialization: previous?.initialization || 'at_runtime',
      runtimeSettings: previous?.runtimeSettings ? structuredClone(previous.runtimeSettings) : undefined,
      assignment: cloneAssignment(previous?.assignment),
      transactionResponseTimeSlaMs: previous?.transactionResponseTimeSlaMs,
    }
  })
  const preservedExtraGroups = previousGroups.slice(groups.length).map(group => ({
    ...group,
    stages: structuredClone(group.stages || []),
    runtimeSettings: group.runtimeSettings ? structuredClone(group.runtimeSettings) : undefined,
    assignment: cloneAssignment(group.assignment),
  }))
  return ensureScenarioGroups([...seededGroups, ...preservedExtraGroups])
}

/**
 * The scenario-wide runtime defaults a form authors, as the runtime editor's
 * draft. The form keeps only the canonical override (runtime_settings): every
 * editor writes it directly, so nothing is reconstructed from display fields.
 * A legacy alias is dropped where its canonical section is authored (the
 * controller refuses the two disagreeing): top-level iterations and
 * auto_transactions under run_logic, log_level under log, and the legacy
 * browser block under browser_config.
 */
export function runtimeSettingsFromForm(form: ScenarioFormState): ScenarioRuntimeSettingsDraft {
  const draft = runtimeDraftFromOverride(form.runtime_settings)
  if (!draft) return { log_level: '', auto_transactions: '' }
  return {
    ...draft,
    ...(draft.run_logic !== undefined ? { iterations: undefined, auto_transactions: '' } : {}),
    ...(draft.log !== undefined ? { log_level: '' } : {}),
    ...(draft.browser_config !== undefined ? { browser: undefined } : {}),
  }
}

/** Replaces the scenario-wide runtime defaults with the runtime editor's draft. */
export function replaceScenarioRuntimeDefaults(
  form: ScenarioFormState,
  next?: ScenarioRuntimeSettingsDraft,
): ScenarioFormState {
  return { ...form, runtime_settings: overrideFromRuntimeDraft(next) }
}

export type ScenarioErrorHandlingFlag = 'continue_on_error' | 'snapshot_on_error'

/** An error-handling default as authored: undefined inherits the script's. */
export function scenarioErrorHandlingFlag(form: ScenarioFormState, flag: ScenarioErrorHandlingFlag): boolean | undefined {
  return form.runtime_settings?.error_handling?.[flag]
}

/** Sets an error-handling default (undefined clears it back to inherit). */
export function withScenarioErrorHandlingFlag(form: ScenarioFormState, flag: ScenarioErrorHandlingFlag, value: boolean | undefined): ScenarioFormState {
  const runtime = form.runtime_settings ? structuredClone(form.runtime_settings) : {}
  const errorHandling = { ...(runtime.error_handling ?? {}) }
  if (value === undefined) delete errorHandling[flag]
  else errorHandling[flag] = value
  runtime.error_handling = Object.keys(errorHandling).length > 0 ? errorHandling : undefined
  return { ...form, runtime_settings: Object.values(runtime).some(entry => entry !== undefined) ? runtime : undefined }
}

const GOAL_BOUNDS = ['target_average_tps', 'min_vusers', 'max_vusers', 'p95_latency_ms', 'max_error_pct'] as const

/**
 * The goal after a load-model adaptation: each throughput, VUser, latency or
 * error bound the adaptation sets replaces the authored one; every other goal
 * field is kept.
 */
export function withAdaptedGoalBounds(current: GoalConfig | undefined, adapted: GoalConfig | undefined): GoalConfig | undefined {
  if (!adapted) return current
  const next: GoalConfig = { ...(current || {}) }
  for (const key of GOAL_BOUNDS) {
    if (adapted[key] !== undefined) next[key] = adapted[key] as never
  }
  return Object.keys(next).length > 0 ? next : undefined
}

/** The runtime fragments a starter template, workload shape or adapted load model seeds. */
export type ScenarioSeedRuntime = Pick<ScenarioSeed, 'pacing' | 'think_time' | 'runtime_settings' | 'iterations' | 'log_level' | 'auto_transactions' | 'continue_on_error' | 'snapshot_on_error'>

/**
 * The scenario runtime defaults after a seed: a seed's runtime_settings
 * replaces the current defaults, except the authored iterations, log level,
 * auto transactions and error-handling flags it does not set itself, and its
 * pacing, think time, iterations, logging, auto-transaction and error-handling
 * fragments are then applied over them. A fragment of the same pacing or think-time type keeps the current
 * section's other fields; a different type replaces the section. Iterations,
 * log level and auto transactions go where the defaults already author them
 * (run_logic and log), else to their top-level fields.
 */
export function seededScenarioRuntime(current: RuntimeSettingsOverride | undefined, seed: ScenarioSeedRuntime): RuntimeSettingsOverride | undefined {
  const runtime: RuntimeSettingsOverride = structuredClone(seed.runtime_settings ?? current ?? {})
  if (seed.runtime_settings && current) carryAuthoredRuntime(runtime, current)
  if (seed.pacing?.type) {
    const sameType = runtime.pacing?.type === seed.pacing.type
    runtime.pacing = { ...(sameType ? runtime.pacing : {}), ...seed.pacing, type: seed.pacing.type } as NonNullable<RuntimeSettingsOverride['pacing']>
  }
  const thinkTimeType = canonicalThinkTimeType(seed.think_time?.type)
  if (seed.think_time && thinkTimeType) {
    const sameType = canonicalThinkTimeType(runtime.think_time?.type ?? undefined) === thinkTimeType
    runtime.think_time = thinkTimeConfigForType({ ...(sameType ? runtime.think_time : {}), ...seed.think_time }, thinkTimeType)
  }
  if (seed.iterations !== undefined) {
    if (runtime.run_logic) runtime.run_logic = { ...runtime.run_logic, iterations: seed.iterations }
    else runtime.iterations = seed.iterations
  }
  if (seed.log_level !== undefined) {
    if (runtime.log) runtime.log = { ...runtime.log, level: seed.log_level || undefined }
    else runtime.log_level = seed.log_level || undefined
  }
  if (seed.auto_transactions !== undefined) {
    if (runtime.run_logic) runtime.run_logic = { ...runtime.run_logic, auto_transactions: seed.auto_transactions || undefined }
    else runtime.auto_transactions = seed.auto_transactions || undefined
  }
  for (const flag of ['continue_on_error', 'snapshot_on_error'] as const) {
    if (seed[flag] === undefined) continue
    runtime.error_handling = { ...(runtime.error_handling ?? {}), [flag]: seed[flag] }
  }
  return Object.values(runtime).some(entry => entry !== undefined) ? runtime : undefined
}

// carryAuthoredRuntime keeps the current iterations, log level, auto
// transactions, error-handling flags and browser settings that a seed's
// runtime_settings leaves unset (a template never resets a continue-on-error
// opt-out or a scenario's device profile), where runtime authors them.
function carryAuthoredRuntime(runtime: RuntimeSettingsOverride, current: RuntimeSettingsOverride) {
  const iterations = current.run_logic?.iterations ?? current.iterations
  if (iterations !== undefined && runtime.run_logic?.iterations === undefined && runtime.iterations === undefined) {
    if (runtime.run_logic) runtime.run_logic = { ...runtime.run_logic, iterations }
    else runtime.iterations = iterations
  }
  const level = current.log?.level ?? current.log_level
  if (level !== undefined && runtime.log?.level === undefined && runtime.log_level === undefined) {
    if (runtime.log) runtime.log = { ...runtime.log, level }
    else runtime.log_level = level
  }
  const autoTransactions = current.run_logic?.auto_transactions ?? current.auto_transactions
  if (autoTransactions !== undefined && runtime.run_logic?.auto_transactions === undefined && runtime.auto_transactions === undefined) {
    if (runtime.run_logic) runtime.run_logic = { ...runtime.run_logic, auto_transactions: autoTransactions }
    else runtime.auto_transactions = autoTransactions
  }
  for (const flag of ['continue_on_error', 'snapshot_on_error'] as const) {
    const value = current.error_handling?.[flag]
    if (value !== undefined && runtime.error_handling?.[flag] === undefined) runtime.error_handling = { ...(runtime.error_handling ?? {}), [flag]: value }
  }
  if (runtime.browser_config === undefined && runtime.browser === undefined) {
    if (current.browser_config !== undefined) runtime.browser_config = structuredClone(current.browser_config)
    else if (current.browser !== undefined) runtime.browser = structuredClone(current.browser)
  }
}

export function assignmentFromForm(form: ScenarioFormState): GeneratorAssignmentSettings {
  const preserved = cloneAssignment(form.assignment)
  const common = {
    ...(preserved?.pool_id ? { pool_id: preserved.pool_id } : {}),
    ...(preserved?.required_labels ? { required_labels: { ...preserved.required_labels } } : {}),
    ...(preserved?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}),
    ...(preserved?.failover_policy === 'continue' ? { failover_policy: 'continue' as const } : {}),
    ...(preserved?.recovery_spare_vusers !== undefined ? { recovery_spare_vusers: preserved?.recovery_spare_vusers } : {}),
  }
  // The form's assignment is its only placement store (AUTH-3); only the
  // fields its mode uses are sent.
  const mode = preserved?.mode || 'auto'
  if (mode === 'specific_hosts') {
    return { ...common, mode: 'specific_hosts', selected_targets: [...(preserved?.selected_targets || [])] }
  }
  if (mode === 'region_weighted' && preserved?.region_weights) {
    return { ...common, mode: 'region_weighted', region_weights: { ...preserved.region_weights } }
  }
  if (mode === 'load_zone_weighted' && preserved?.load_zone_weights) {
    return { ...common, mode: 'load_zone_weighted', load_zone_weights: { ...preserved.load_zone_weights } }
  }
  return { ...common, mode: 'auto' }
}

function scenarioGroupRuntimeDraftFromStored(
  group: StoredScenario['scripts'][number],
): ScenarioRuntimeSettingsDraft | undefined {
  const runtimeDraft = runtimeDraftFromOverride(group.runtime_settings)
  const runtimeOwnsIterations = group.runtime_settings?.run_logic !== undefined
    || group.runtime_settings?.iterations !== undefined

  if (runtimeOwnsIterations || group.iterations === undefined) return runtimeDraft
  return {
    ...runtimeDraft,
    iterations: group.iterations,
    log_level: runtimeDraft?.log_level ?? '',
    auto_transactions: runtimeDraft?.auto_transactions ?? '',
  }
}

function scenarioFormGroupFromStored(
  group: StoredScenario['scripts'][number],
  fallbackScriptId: string,
): ScenarioFormGroup {
  return {
    name: group.name || '',
    script_id: group.script_id || fallbackScriptId,
    role: group.role,
    vusers: group.vusers ?? 10,
    ramp_up: group.ramp_up || '',
		ramp_down: group.ramp_down || '',
		start_offset: group.start_offset || '',
    stages: group.stages ? [...group.stages] : [],
    iterations: undefined,
    initialization: group.initialization || 'at_runtime',
    runtimeSettings: scenarioGroupRuntimeDraftFromStored(group),
    assignment: cloneAssignment(group.assignment),
		transactionResponseTimeSlaMs: group.transaction_response_time_sla_ms,
  }
}

export function booleanSelectValue(value?: boolean): '' | 'true' | 'false' {
  if (value === undefined) return ''
  return value ? 'true' : 'false'
}

export function scenarioFormStateFromStoredScenario(
  scenario: StoredScenario,
  fallbackScriptId: string,
  cloneDraft = false,
): ScenarioFormState {
  const groups = [...(scenario.scripts || [])]
    .sort((left, right) => (left.order ?? 0) - (right.order ?? 0))
    .map(group => scenarioFormGroupFromStored(group, fallbackScriptId))

  return {
    name: cloneDraft ? `${scenario.name} (Copy)` : scenario.name,
    duration: scenario.duration,
    termination_mode: scenario.termination_mode,
    start_delay: scenario.start_delay || '',
    groups: ensureScenarioGroups(groups),
    scenario_mode: scenario.scenario_mode || 'vuser_driven',
    script_shakeout: Boolean(scenario.script_shakeout),
    resource_baseline: Boolean(scenario.resource_baseline),
    goal_config: scenario.goal_config ? {
      ...scenario.goal_config,
      transaction_rules: scenario.goal_config.transaction_rules?.map(rule => ({ ...rule })),
		early_stop: scenario.goal_config.early_stop ? { ...scenario.goal_config.early_stop } : undefined,
		workload_shape_config: scenario.goal_config.workload_shape_config
			? structuredClone(scenario.goal_config.workload_shape_config)
			: undefined,
    } : undefined,
    traffic_profile: scenario.traffic_profile ? structuredClone(scenario.traffic_profile) : undefined,
    runtime_settings: scenario.runtime_settings ? structuredClone(scenario.runtime_settings) : undefined,
    assignment: cloneAssignment(scenario.assignment),
    data_pools: scenario.data_pools?.map(binding => cloneDraft ? {
      pool_id: binding.pool_id,
      pool_name: binding.pool_name,
      alias: binding.alias,
      access_mode: 'read_only',
      selection_strategy: binding.selection_strategy,
    } : { ...binding, access_mode: 'read_only' }),
  }
}
