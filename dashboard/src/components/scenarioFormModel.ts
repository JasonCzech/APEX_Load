import type {
  GeneratorAssignmentSettings,
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
    stages: [],
    initialization: 'at_runtime',
  }
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
      stages: group.stages ? structuredClone(group.stages) : [],
      iterations: group.iterations ?? previous?.iterations,
      initialization: previous?.initialization || 'at_runtime',
      runtimeSettings: previous?.runtimeSettings ? structuredClone(previous.runtimeSettings) : undefined,
      assignment: cloneAssignment(previous?.assignment),
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

function browserRuntimeFromForm(form: ScenarioFormState) {
  const browser = {
    ...(form.browser_device_profile ? { device_profile: form.browser_device_profile } : {}),
    ...(form.browser_orientation ? { orientation: form.browser_orientation } : {}),
    ...(form.browser_touch_mode ? { touch_mode: form.browser_touch_mode === 'true' } : {}),
    ...(form.browser_headless ? { headless: form.browser_headless === 'true' } : {}),
  }
  return Object.keys(browser).length > 0 ? browser : undefined
}

function pacingFromForm(form: ScenarioFormState): RuntimeSettingsOverride['pacing'] | undefined {
  const preserved = form.runtime_settings?.pacing
  if (!form.runtime_pacing_type) return preserved ? { ...preserved } : undefined
  const sameType = preserved?.type === form.runtime_pacing_type
  if (form.runtime_pacing_type === 'none') {
    return sameType ? { ...preserved, type: 'none' } : { type: 'none' }
  }
  if (form.runtime_pacing_type === 'fixed_interval') {
    return { ...(sameType ? preserved : {}), type: 'fixed_interval', interval_ms: form.runtime_pacing_interval_ms }
  }
  if (form.runtime_pacing_type === 'random_interval') {
    return {
      ...(sameType ? preserved : {}),
      type: 'random_interval',
      min_interval_ms: form.runtime_pacing_min_interval_ms,
      max_interval_ms: form.runtime_pacing_max_interval_ms,
    }
  }
  if (form.runtime_pacing_type === 'tps') {
    return { ...(sameType ? preserved : {}), type: 'tps', tps: form.runtime_pacing_tps }
  }
  if (form.runtime_pacing_type === 'fixed_delay') {
    return { ...(sameType ? preserved : {}), type: 'fixed_delay', delay_ms: form.runtime_pacing_delay_ms }
  }
  if (form.runtime_pacing_type === 'random_delay') {
    return {
      ...(sameType ? preserved : {}),
      type: 'random_delay',
      min_delay_ms: form.runtime_pacing_min_delay_ms,
      max_delay_ms: form.runtime_pacing_max_delay_ms,
    }
  }
  return undefined
}

function thinkTimeFromForm(form: ScenarioFormState): RuntimeSettingsOverride['think_time'] | undefined {
  const preserved = form.runtime_settings?.think_time
  const type = canonicalThinkTimeType(form.runtime_think_time_type || preserved?.type)
  if (!type) return undefined
  const sameType = canonicalThinkTimeType(preserved?.type) === type
  const next = {
    ...(sameType ? preserved : {}),
    type,
    multiplier: form.runtime_think_time_multiplier,
    limit_ms: form.runtime_think_time_limit_ms,
    ignore: form.runtime_ignore_think_time,
    ...(type === 'recorded' ? {} : { mean_ms: form.runtime_think_time_ms ?? 0 }),
    ...(type === 'gaussian' ? {
      std_ms: form.runtime_think_time_std_ms,
      min_ms: form.runtime_think_time_min_ms,
      max_ms: form.runtime_think_time_max_ms,
    } : {}),
    ...(type === 'uniform' ? {
      min_ms: form.runtime_think_time_min_ms,
      max_ms: form.runtime_think_time_max_ms,
    } : {}),
  }
  return thinkTimeConfigForType(next, type)
}

export function runtimeSettingsFromForm(
  form: ScenarioFormState,
  includeBrowser: boolean,
): ScenarioRuntimeSettingsDraft {
  const preserved = runtimeDraftFromOverride(form.runtime_settings)
  const hasCanonicalRunLogic = form.runtime_settings?.run_logic !== undefined
  const hasCanonicalLog = form.runtime_settings?.log !== undefined
  const hasCanonicalBrowser = form.runtime_settings?.browser_config !== undefined
  const runLogic = hasCanonicalRunLogic
    ? {
        ...preserved?.run_logic,
        iterations: form.runtime_iterations,
        auto_transactions: form.runtime_auto_transactions || undefined,
      }
    : preserved?.run_logic
  const log = hasCanonicalLog
    ? { ...preserved?.log, level: form.runtime_log_level || undefined }
    : preserved?.log
  const browserConfig = includeBrowser && hasCanonicalBrowser
    ? {
        ...preserved?.browser_config,
        device_profile: form.browser_device_profile || undefined,
        orientation: form.browser_orientation || undefined,
        touch_mode: form.browser_touch_mode ? form.browser_touch_mode === 'true' : undefined,
        headless: form.browser_headless ? form.browser_headless === 'true' : undefined,
      }
    : preserved?.browser_config
  return {
    ...preserved,
    iterations: hasCanonicalRunLogic ? undefined : form.runtime_iterations,
    pacing: pacingFromForm(form),
    think_time: thinkTimeFromForm(form),
    browser: includeBrowser ? (hasCanonicalBrowser ? undefined : browserRuntimeFromForm(form)) : preserved?.browser,
    browser_config: browserConfig,
    log_level: hasCanonicalLog ? '' : form.runtime_log_level,
    log,
    auto_transactions: hasCanonicalRunLogic ? '' : form.runtime_auto_transactions,
    run_logic: runLogic,
    continue_on_error: form.continue_on_error,
    snapshot_on_error: form.snapshot_on_error,
  }
}

/**
 * Replaces the scenario-default runtime profile while keeping the legacy flat
 * form mirrors synchronized. runtimeSettingsFromForm still reads those mirrors
 * when producing the save payload, so leaving them blank would otherwise
 * overwrite canonical pacing/think-time values during an unrelated edit.
 */
export function replaceScenarioRuntimeDefaults(
  form: ScenarioFormState,
  next?: ScenarioRuntimeSettingsDraft,
): ScenarioFormState {
  const pacing = next?.pacing
  const thinkTime = next?.think_time
  return {
    ...form,
    runtime_settings: overrideFromRuntimeDraft(next),
    runtime_pacing_type: (pacing?.type || '') as ScenarioFormState['runtime_pacing_type'],
    runtime_pacing_delay_ms: pacing?.delay_ms,
    runtime_pacing_min_delay_ms: pacing?.min_delay_ms,
    runtime_pacing_max_delay_ms: pacing?.max_delay_ms,
    runtime_pacing_interval_ms: pacing?.interval_ms,
    runtime_pacing_min_interval_ms: pacing?.min_interval_ms,
    runtime_pacing_max_interval_ms: pacing?.max_interval_ms,
    runtime_pacing_tps: pacing?.tps,
    runtime_think_time_type: canonicalThinkTimeType(thinkTime?.type) as ScenarioFormState['runtime_think_time_type'],
    runtime_think_time_ms: thinkTime?.mean_ms,
    runtime_think_time_std_ms: thinkTime?.std_ms,
    runtime_think_time_min_ms: thinkTime?.min_ms,
    runtime_think_time_max_ms: thinkTime?.max_ms,
    runtime_think_time_multiplier: thinkTime?.multiplier,
    runtime_think_time_limit_ms: thinkTime?.limit_ms,
    runtime_ignore_think_time: thinkTime?.ignore ?? false,
    runtime_iterations: next?.run_logic?.iterations ?? next?.iterations,
    runtime_log_level: next?.log?.level ?? next?.log_level ?? '',
    runtime_auto_transactions: next?.run_logic?.auto_transactions ?? next?.auto_transactions ?? '',
    continue_on_error: next?.continue_on_error,
    snapshot_on_error: next?.snapshot_on_error,
    browser_device_profile: next?.browser_config?.device_profile ?? next?.browser?.device_profile ?? '',
    browser_orientation: next?.browser_config?.orientation ?? next?.browser?.orientation ?? '',
    browser_touch_mode: booleanSelectValue(next?.browser_config?.touch_mode ?? next?.browser?.touch_mode),
    browser_headless: booleanSelectValue(next?.browser_config?.headless ?? next?.browser?.headless),
  }
}

export function assignmentFromForm(form: ScenarioFormState): GeneratorAssignmentSettings {
  const preserved = cloneAssignment(form.assignment)
  const common = {
    ...(preserved?.pool_id ? { pool_id: preserved.pool_id } : {}),
    ...(preserved?.required_labels ? { required_labels: { ...preserved.required_labels } } : {}),
    ...(preserved?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}),
  }
  if (form.assignment_mode === 'specific_hosts') {
    return { ...common, mode: 'specific_hosts', selected_targets: [...form.assignment_targets] }
  }
  if (form.assignment_mode === 'region_weighted' && preserved?.region_weights) {
    return { ...common, mode: 'region_weighted', region_weights: { ...preserved.region_weights } }
  }
  if (form.assignment_mode === 'load_zone_weighted' && preserved?.load_zone_weights) {
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
    vusers: group.vusers || 10,
    ramp_up: group.ramp_up || '',
    ramp_down: group.ramp_down || '',
    stages: group.stages ? [...group.stages] : [],
    iterations: undefined,
    initialization: group.initialization || 'at_runtime',
    runtimeSettings: scenarioGroupRuntimeDraftFromStored(group),
    assignment: cloneAssignment(group.assignment),
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
  const firstPool = scenario.data_pools?.[0]
  const runtimeSettings = scenario.runtime_settings
  const pacing = runtimeSettings?.pacing
  const thinkTime = runtimeSettings?.think_time

  return {
    name: cloneDraft ? `${scenario.name} (Copy)` : scenario.name,
    duration: scenario.duration,
    start_delay: scenario.start_delay || '',
    groups: ensureScenarioGroups(groups),
    scenario_mode: scenario.scenario_mode || 'vuser_driven',
    script_shakeout: Boolean(scenario.script_shakeout),
    resource_baseline: Boolean(scenario.resource_baseline),
    goal_target_average_tps: scenario.goal_config?.target_average_tps,
    goal_min_vusers: scenario.goal_config?.min_vusers,
    goal_max_vusers: scenario.goal_config?.max_vusers,
    goal_p95_latency_ms: scenario.goal_config?.p95_latency_ms,
    goal_max_error_pct: scenario.goal_config?.max_error_pct,
    goal_config: scenario.goal_config ? { ...scenario.goal_config } : undefined,
    continue_on_error: runtimeSettings?.error_handling?.continue_on_error,
    snapshot_on_error: runtimeSettings?.error_handling?.snapshot_on_error,
    runtime_iterations: runtimeSettings?.run_logic?.iterations ?? runtimeSettings?.iterations,
    runtime_pacing_type: (pacing?.type || '') as ScenarioFormState['runtime_pacing_type'],
    runtime_pacing_delay_ms: pacing?.delay_ms,
    runtime_pacing_min_delay_ms: pacing?.min_delay_ms,
    runtime_pacing_max_delay_ms: pacing?.max_delay_ms,
    runtime_pacing_interval_ms: pacing?.interval_ms,
    runtime_pacing_min_interval_ms: pacing?.min_interval_ms,
    runtime_pacing_max_interval_ms: pacing?.max_interval_ms,
    runtime_pacing_tps: pacing?.tps,
    runtime_think_time_type: canonicalThinkTimeType(thinkTime?.type) as ScenarioFormState['runtime_think_time_type'],
    runtime_think_time_ms: thinkTime?.mean_ms,
    runtime_think_time_std_ms: thinkTime?.std_ms,
    runtime_think_time_min_ms: thinkTime?.min_ms,
    runtime_think_time_max_ms: thinkTime?.max_ms,
    runtime_think_time_multiplier: thinkTime?.multiplier,
    runtime_think_time_limit_ms: thinkTime?.limit_ms,
    runtime_ignore_think_time: Boolean(thinkTime?.ignore),
    runtime_log_level: runtimeSettings?.log?.level ?? runtimeSettings?.log_level ?? '',
    runtime_auto_transactions: runtimeSettings?.run_logic?.auto_transactions ?? runtimeSettings?.auto_transactions ?? '',
    runtime_settings: runtimeSettings,
    browser_device_profile: runtimeSettings?.browser_config?.device_profile ?? runtimeSettings?.browser?.device_profile ?? '',
    browser_orientation: runtimeSettings?.browser_config?.orientation ?? runtimeSettings?.browser?.orientation ?? '',
    browser_touch_mode: booleanSelectValue(runtimeSettings?.browser_config?.touch_mode ?? runtimeSettings?.browser?.touch_mode),
    browser_headless: booleanSelectValue(runtimeSettings?.browser_config?.headless ?? runtimeSettings?.browser?.headless),
    assignment_mode: scenario.assignment?.mode || 'auto',
    assignment_targets: scenario.assignment?.selected_targets ? [...scenario.assignment.selected_targets] : [],
    assignment: cloneAssignment(scenario.assignment),
    data_pool_id: firstPool?.pool_id || '',
    data_pool_alias: firstPool?.alias || '',
    data_pool_access_mode: 'read_only',
    data_pool_selection_strategy: firstPool?.selection_strategy || 'sequential',
    data_pools: scenario.data_pools?.map(binding => cloneDraft ? {
      pool_id: binding.pool_id,
      pool_name: binding.pool_name,
      alias: binding.alias,
      access_mode: 'read_only',
      selection_strategy: binding.selection_strategy,
    } : { ...binding, access_mode: 'read_only' }),
  }
}
