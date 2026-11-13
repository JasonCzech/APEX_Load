import {
  GeneratorAssignmentSettings,
  RuntimeSettingsOverride,
  ScenarioDraft,
  ScenarioDraftGroup,
  ScenarioRuntimeSettingsDraft,
  StoredScript,
  TestSummary,
} from '../types'
import { type PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import {
  isFieldOverridden,
  isPartialRuntimeDraft,
  normalizeRuntimeOverrideFields,
  overrideFieldValue,
  resetOverrideField,
  setOverrideField,
} from './runtimeSettingsModel'

export type ScenarioRuntimeSettings = ScenarioDraft['runtimeSettings']
export type ScenarioDraftState = ScenarioDraft
export type ScenarioDraftField = keyof Omit<ScenarioDraftState, 'groups' | 'goalConfig' | 'runtimeSettings' | 'revisionContext'>
export type ScenarioGoalConfigField = keyof ScenarioDraftState['goalConfig']
export type ScenarioRuntimeSettingField = keyof ScenarioRuntimeSettings

export type ScenarioDraftAction =
  | { type: 'setField'; field: ScenarioDraftField; value: ScenarioDraftState[ScenarioDraftField] }
  | { type: 'setGoalConfigField'; field: ScenarioGoalConfigField; value: ScenarioDraftState['goalConfig'][ScenarioGoalConfigField] }
  | { type: 'setRuntimeSettingField'; field: ScenarioRuntimeSettingField; value: ScenarioRuntimeSettings[ScenarioRuntimeSettingField] }
  | { type: 'replaceRuntimeSettings'; value: ScenarioRuntimeSettings }
  | { type: 'updateGroup'; index: number; patch: Partial<ScenarioDraftGroup> }
  | { type: 'addGroup'; group: ScenarioDraftGroup }
  | { type: 'removeGroup'; index: number }
  | { type: 'moveGroup'; from: number; to: number }

function cloneGeneratorAssignment(assignment?: GeneratorAssignmentSettings): GeneratorAssignmentSettings {
  return {
    mode: assignment?.mode || 'auto',
    ...(assignment?.java_host ? { java_host: { ...assignment.java_host } } : {}),
    ...(assignment?.pool_id ? { pool_id: assignment.pool_id } : {}),
    ...(assignment?.selected_targets ? { selected_targets: [...assignment.selected_targets] } : { selected_targets: [] }),
    ...(assignment?.region_weights ? { region_weights: { ...assignment.region_weights } } : {}),
    ...(assignment?.load_zone_weights ? { load_zone_weights: { ...assignment.load_zone_weights } } : {}),
    ...(assignment?.shard_strategy ? { shard_strategy: assignment.shard_strategy } : {}),
    ...(assignment?.required_labels ? { required_labels: { ...assignment.required_labels } } : {}),
    ...(assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}),
    ...(assignment?.failover_policy === 'continue' ? { failover_policy: 'continue' as const } : {}),
    ...(assignment?.recovery_spare_vusers !== undefined ? { recovery_spare_vusers: assignment?.recovery_spare_vusers } : {}),
  }
}

function cloneGroupAssignment(assignment?: GeneratorAssignmentSettings) {
  if (!assignment) return undefined
  return cloneGeneratorAssignment(assignment)
}

function cloneValue<T>(value: T | undefined): T | undefined {
  return value === undefined ? undefined : structuredClone(value)
}

function compactBrowserFromCanonical(settings: ScenarioRuntimeSettingsDraft): ScenarioRuntimeSettingsDraft['browser'] {
  const source = settings.browser_config !== undefined ? settings.browser_config : settings.browser
  if (!source) return undefined
  const browser = {
    headless: source.headless,
    device_profile: source.device_profile,
    touch_mode: source.touch_mode,
    orientation: source.orientation,
  }
  return Object.values(browser).some(value => value !== undefined && value !== '') ? browser : undefined
}

/** Hydrate compact controls from canonical sections, which win over legacy aliases. */
function hydrateCanonicalRuntimeControls(settings: ScenarioRuntimeSettingsDraft): ScenarioRuntimeSettingsDraft {
  const hasRunLogic = settings.run_logic !== undefined
  const hasLog = settings.log !== undefined
  const hasBrowserConfig = settings.browser_config !== undefined
  const eh = settings.error_handling
  return {
    ...settings,
    iterations: hasRunLogic ? settings.run_logic?.iterations : settings.iterations,
    auto_transactions: hasRunLogic ? (settings.run_logic?.auto_transactions ?? '') : (settings.auto_transactions ?? ''),
    log_level: hasLog ? (settings.log?.level ?? '') : (settings.log_level ?? ''),
    browser: hasBrowserConfig ? compactBrowserFromCanonical(settings) : settings.browser,
    ...(eh !== undefined
      ? {
          continue_on_error: settings.continue_on_error ?? eh.continue_on_error,
          snapshot_on_error: settings.snapshot_on_error ?? eh.snapshot_on_error,
          fail_transaction_on_error_message:
            settings.fail_transaction_on_error_message ?? eh.fail_transaction_on_error_message,
          max_retries: settings.max_retries ?? eh.max_retries,
          retry_delay_ms: settings.retry_delay_ms ?? eh.retry_delay_ms,
          retry_backoff: settings.retry_backoff ?? eh.retry_backoff,
          retry_on: settings.retry_on ?? (eh.retry_on ? [...eh.retry_on] : undefined),
          honor_retry_after: settings.honor_retry_after ?? eh.honor_retry_after,
          retry_after_max_ms: settings.retry_after_max_ms ?? eh.retry_after_max_ms,
        }
      : {}),
  }
}

function cloneRuntimeSettingsDraft(settings?: ScenarioRuntimeSettingsDraft): ScenarioRuntimeSettingsDraft {
  return hydrateCanonicalRuntimeControls({
    ...cloneValue(settings),
    log_level: settings?.log_level ?? '',
    auto_transactions: settings?.auto_transactions ?? '',
  })
}

export function buildRuntimeSettingsDraft(settings?: RuntimeSettingsOverride): ScenarioRuntimeSettingsDraft {
  const errorHandling = settings?.error_handling
  const stored = settings as PartialRuntimeSettingsOverride | undefined
  // A partial row lists exactly what it overrides, so its flat error-handling
  // controls carry only what its section holds (never a materialized false).
  const partial = isPartialRuntimeDraft(stored)
  return hydrateCanonicalRuntimeControls({
    ...(stored?.soap !== undefined ? { soap: cloneValue(stored.soap) } : {}),
    ...(partial ? { override_fields: normalizeRuntimeOverrideFields(stored?.override_fields) } : {}),
    iterations: settings?.iterations,
    pacing: cloneValue(settings?.pacing) ?? undefined,
    think_time: cloneValue(settings?.think_time) ?? undefined,
    browser: cloneValue(settings?.browser),
    log_level: settings?.log_level ?? '',
    auto_transactions: settings?.auto_transactions ?? '',
    // continue_on_error is tri-state at the field level, not the section level:
    // continue-on-error is enabled for every scenario unless explicitly disabled,
    // so a present error_handling section that says nothing about it must stay
    // undefined rather than collapse to false. An explicit false still overrides
    // a script-level true. A null section is the run-only clear tombstone, which
    // is an absent section by definition.
    continue_on_error: errorHandling == null ? undefined : errorHandling.continue_on_error,
    snapshot_on_error: errorHandling == null ? undefined : (partial ? errorHandling.snapshot_on_error : (errorHandling.snapshot_on_error ?? false)),
    fail_transaction_on_error_message:
      errorHandling == null ? undefined : errorHandling.fail_transaction_on_error_message,
    max_retries: errorHandling?.max_retries,
    retry_delay_ms: errorHandling?.retry_delay_ms,
    retry_backoff: errorHandling?.retry_backoff,
    retry_on: errorHandling?.retry_on ? [...errorHandling.retry_on] : undefined,
    honor_retry_after: errorHandling == null ? undefined : errorHandling.honor_retry_after,
    retry_after_max_ms: errorHandling?.retry_after_max_ms,
    log: cloneValue(settings?.log),
    run_logic: cloneValue(settings?.run_logic),
    http: cloneValue(settings?.http),
    results: cloneValue(settings?.results),
    websocket: cloneValue(settings?.websocket),
    web_runtime: cloneValue(settings?.web_runtime),
    browser_config: cloneValue(settings?.browser_config),
    browser_emulation: cloneValue(settings?.browser_emulation),
    java_vuser: cloneValue(settings?.java_vuser),
    base_url: settings?.base_url,
    env: cloneValue(settings?.env),
    redaction_rules: cloneValue(settings?.redaction_rules),
  })
}

const FLAT_ERROR_HANDLING_FIELDS = new Set<string>([
  'continue_on_error',
  'snapshot_on_error',
  'fail_transaction_on_error_message',
  'max_retries',
  'retry_delay_ms',
  'retry_backoff',
  'retry_on',
  'honor_retry_after',
  'retry_after_max_ms',
])

/**
 * One runtime control edit, through the runtime settings model: the draft
 * becomes a partial row (a stored v1 row is converted without changing its
 * meaning) and only the edited path is listed in override_fields, so the
 * backend applies just that path instead of replacing the whole section.
 * An empty value ('' or undefined) returns the field to inherited.
 */
function updateRuntimeSetting(
  settings: ScenarioRuntimeSettingsDraft,
  field: ScenarioRuntimeSettingField,
  value: ScenarioRuntimeSettings[ScenarioRuntimeSettingField],
): ScenarioRuntimeSettingsDraft {
  if (field === 'override_fields') {
    return { ...settings, override_fields: value === undefined ? undefined : normalizeRuntimeOverrideFields(value as string[]) }
  }
  const empty = value === undefined || value === ''
  const edit = (path: string, next: unknown, clear = next === undefined || next === '') =>
    (current: ScenarioRuntimeSettingsDraft) => clear ? resetOverrideField(current, path) : setOverrideField(current, path, next)
  if (field === 'browser') {
    const browser = (value ?? {}) as NonNullable<ScenarioRuntimeSettingsDraft['browser']>
    return (['headless', 'device_profile', 'touch_mode', 'orientation'] as const)
      .map(key => edit(`browser.${key}`, browser[key]))
      .reduce((current, apply) => apply(current), settings)
  }
  if (FLAT_ERROR_HANDLING_FIELDS.has(field)) {
    const list = Array.isArray(value) ? value : undefined
    return edit(`error_handling.${field}`, value, empty || (list !== undefined && list.length === 0))(settings)
  }
  const path = field === 'browser_config' ? 'browser' : field
  return edit(path, value)(settings)
}

/** The group's iteration count as its runtime settings state it, else the compact field. */
function runtimeOwnedIterations(runtimeSettings: ScenarioRuntimeSettingsDraft | undefined, fallback: number | undefined): number | undefined {
  if (isPartialRuntimeDraft(runtimeSettings)) {
    return isFieldOverridden(runtimeSettings, 'run_logic.iterations')
      ? overrideFieldValue(runtimeSettings, 'run_logic.iterations') as number | undefined
      : fallback
  }
  return runtimeSettings?.run_logic !== undefined ? runtimeSettings.run_logic.iterations : fallback
}

function cloneScenarioGroup(group: ScenarioDraftGroup): ScenarioDraftGroup {
  const runtimeSettings = group.runtimeSettings ? cloneRuntimeSettingsDraft(group.runtimeSettings) : undefined
  return {
    ...group,
    stages: cloneValue(group.stages),
    iterations: runtimeOwnedIterations(runtimeSettings, group.iterations),
    runtimeSettings,
    assignment: cloneGroupAssignment(group.assignment),
  }
}

function updateScenarioGroup(group: ScenarioDraftGroup, patch: Partial<ScenarioDraftGroup>): ScenarioDraftGroup {
  const replacesSourceScript = patch.scriptId !== undefined
    && patch.scriptId !== group.scriptId
    && group.revisionSourceGroupIndex !== undefined
  if (replacesSourceScript) {
    return {
      ...group,
      ...patch,
      revisionSourceGroupIndex: undefined,
      revisionNewGroup: true,
      // Masked credentials and canonical runtime fields belong to the old
      // source script identity. A replacement starts clean unless the same
      // action explicitly supplies a new profile.
      runtimeSettings: patch.runtimeSettings,
      iterations: patch.iterations,
      iterationPayloadSource: undefined,
    }
  }
  // A patch that supplies its own runtime row (the group runtime drawer) is authoritative: it has
  // already reconciled the iteration between the row and the compact field, so rewriting the
  // iteration into the old row would discard the new row and its edits.
  if (!Object.prototype.hasOwnProperty.call(patch, 'iterations')
    || Object.prototype.hasOwnProperty.call(patch, 'runtimeSettings')) {
    return { ...group, ...patch }
  }

  const iterations = patch.iterations
  const runtimeSettings = group.runtimeSettings
  const runtimeOwnsIterations = group.iterationPayloadSource === 'runtime'
    || (isPartialRuntimeDraft(runtimeSettings)
      ? isFieldOverridden(runtimeSettings, 'run_logic.iterations')
      : runtimeSettings?.run_logic?.iterations !== undefined
        || (runtimeSettings?.run_logic === undefined && runtimeSettings?.iterations !== undefined))
  if (!runtimeOwnsIterations) {
    return { ...group, ...patch }
  }

  // The runtime settings own the iteration count: edit run_logic.iterations
  // through the model, so the edit is listed on its own (a cleared value
  // returns it to inherited) and never replaces the rest of run_logic.
  const base: ScenarioRuntimeSettingsDraft = runtimeSettings ?? { log_level: '', auto_transactions: '' }
  return {
    ...group,
    ...patch,
    iterationPayloadSource: 'runtime',
    runtimeSettings: iterations === undefined
      ? resetOverrideField(base, 'run_logic.iterations')
      : setOverrideField(base, 'run_logic.iterations', iterations),
  }
}

export function buildDefaultScenarioGroup(scripts: StoredScript[]): ScenarioDraftGroup {
  return {
    name: '',
    scriptId: scripts[0]?.id || '',
    vusers: 10,
    rampUp: '',
		rampDown: '',
		startOffset: '',
    iterations: undefined,
    initialization: 'at_runtime',
    assignment: undefined,
  }
}

export function buildInitialScenarioDraft(initialScenario: ScenarioDraft | undefined, scripts: StoredScript[]): ScenarioDraftState {
  return {
    name: initialScenario?.name || '',
    duration: initialScenario?.duration ?? '1m',
    ...(initialScenario?.terminationMode ? { terminationMode: initialScenario.terminationMode } : {}),
    startDelay: initialScenario?.startDelay || '',
    scenarioMode: initialScenario?.scenarioMode || 'vuser_driven',
    scriptShakeout: Boolean(initialScenario?.scriptShakeout),
    resourceBaseline: Boolean(initialScenario?.resourceBaseline),
    assignment: cloneGeneratorAssignment(initialScenario?.assignment),
    // Defaults belong to a new draft, never to an existing authored snapshot.
    goalConfig: initialScenario
      ? structuredClone(initialScenario.goalConfig)
      : { stop_on_sla_breach: false, stop_on_max_errors: false, sla_breach_window: '30s' },
    runtimeSettings: cloneRuntimeSettingsDraft(initialScenario?.runtimeSettings),
    ...(initialScenario?.trafficProfile ? { trafficProfile: structuredClone(initialScenario.trafficProfile) } : {}),
    ...(initialScenario?.jointEvidence ? { jointEvidence: structuredClone(initialScenario.jointEvidence) } : {}),
    ...(initialScenario?.telemetryAcceptancePolicyId ? { telemetryAcceptancePolicyId: initialScenario.telemetryAcceptancePolicyId } : {}),
    ...(initialScenario?.telemetryBudgetPolicyId ? { telemetryBudgetPolicyId: initialScenario.telemetryBudgetPolicyId } : {}),
    ...(initialScenario?.dataPools ? { dataPools: initialScenario.dataPools.map(binding => structuredClone(binding)) } : {}),
    groups: initialScenario?.groups.length
      ? initialScenario.groups.map(cloneScenarioGroup)
      : [buildDefaultScenarioGroup(scripts)],
    revisionContext: initialScenario?.revisionContext
      ? {
        ...initialScenario.revisionContext,
        ...(initialScenario.revisionContext.analysisPolicySelections ? { analysisPolicySelections: structuredClone(initialScenario.revisionContext.analysisPolicySelections) } : {}),
        ...(initialScenario.revisionContext.monitorBindings !== undefined ? { monitorBindings: initialScenario.revisionContext.monitorBindings.map(binding => ({ ...binding })) } : {}),
        dataPools: initialScenario.revisionContext.dataPools.map(binding => structuredClone(binding)),
      }
      : undefined,
  }
}

export function buildScenarioDraftFromTest(test: TestSummary): ScenarioDraft {
  const groups = test.groups?.length
    ? test.groups.map((group, groupIndex) => {
      const runtimeSettings = group.runtime_settings ? buildRuntimeSettingsDraft(group.runtime_settings) : undefined
      const runtimeIteration = isPartialRuntimeDraft(runtimeSettings)
        ? (isFieldOverridden(runtimeSettings, 'run_logic.iterations')
            ? (overrideFieldValue(runtimeSettings, 'run_logic.iterations') as number | undefined) ?? 0
            : undefined)
        : runtimeSettings?.run_logic !== undefined
          ? runtimeSettings.run_logic.iterations
          : runtimeSettings?.iterations
      const explicitIteration = runtimeIteration !== undefined
        ? runtimeIteration
        : (group.iteration_override_known ? group.iteration_override : undefined)
      const iterationPayloadSource = runtimeIteration !== undefined
        ? 'runtime' as const
        : (explicitIteration !== undefined && explicitIteration > 0 ? 'top_level' as const : undefined)
      return {
        name: group.name,
        scriptId: group.script_id,
        revisionSourceGroupIndex: groupIndex,
        ...(group.role ? { role: group.role } : {}),
        vusers: group.vusers,
        rampUp: group.ramp_up || '',
			rampDown: group.ramp_down || '',
			startOffset: group.start_offset || '',
        ...(group.stages ? { stages: cloneValue(group.stages) } : {}),
        // Never turn the effective summary value into a group override. New
        // snapshots identify group-owned values explicitly; legacy snapshots
        // remain unknown and the API fails closed if a root edit is ambiguous.
        iterations: explicitIteration !== undefined && explicitIteration > 0 ? explicitIteration : undefined,
        ...(iterationPayloadSource ? { iterationPayloadSource } : {}),
        initialization: group.initialization || 'at_runtime',
        ...(runtimeSettings ? { runtimeSettings } : {}),
        assignment: cloneGroupAssignment(group.assignment),
		transactionResponseTimeSlaMs: group.transaction_response_time_sla_ms,
      }
    })
    : [{
      name: '',
      scriptId: test.script_id || '',
      vusers: test.total_vusers || 10,
      rampUp: '',
		rampDown: '',
		startOffset: '',
      iterations: undefined,
      initialization: 'at_runtime' as const,
    }]

  return {
    name: test.name,
    duration: test.duration,
    ...(test.termination_mode ? { terminationMode: test.termination_mode } : {}),
    startDelay: test.start_delay || '',
    scenarioMode: test.scenario_mode || 'vuser_driven',
    scriptShakeout: Boolean(test.script_shakeout),
    resourceBaseline: Boolean(test.resource_baseline),
    assignment: cloneGeneratorAssignment(test.assignment),
    goalConfig: structuredClone(test.goal_config ?? {}),
    runtimeSettings: buildRuntimeSettingsDraft(test.runtime_settings),
    groups,
    revisionContext: {
      ...(test.goal_config != null ? { sourceGoalConfigPresent: true } : {}),
      ...(test.runtime_settings != null ? { sourceRuntimeSettingsPresent: true } : {}),
      ...(test.start_delay?.trim() ? { sourceStartDelayPresent: true } : {}),
      ...(test.elk_capture?.connection_id ? { elkConnectionId: test.elk_capture.connection_id } : {}),
      ...(test.dynatrace_capture?.connection_id ? { dynatraceConnectionId: test.dynatrace_capture.connection_id } : {}),
      ...(test.monitor_bindings ? { monitorBindings: test.monitor_bindings.sources.map(binding => ({ ...binding })) } : {}),
      ...(test.joint_evidence ? { jointEvidence: structuredClone(test.joint_evidence) } : {}),
      ...(test.telemetry_acceptance_policy_id ? { telemetryAcceptancePolicyId: test.telemetry_acceptance_policy_id } : {}),
      ...(test.telemetry_budget_policy_id ? { telemetryBudgetPolicyId: test.telemetry_budget_policy_id } : {}),
      ...(test.analysis_policy_selections ? { analysisPolicySelections: structuredClone(test.analysis_policy_selections) } : {}),
      sourceTestId: test.id,
      applicationId: test.application_id,
      releaseId: test.release_id,
      scenarioId: test.scenario_id,
      projectId: test.project_id,
      folderId: test.folder_id,
      // Legacy write/consume labels retain their read-only snapshot behavior.
      // Explicit live bindings keep their seed policy for a fresh isolated run.
      dataPools: (test.data_pools || []).map(binding => ({ ...binding, access_mode: binding.access_mode === 'live' ? 'live' as const : 'read_only' as const, ...(binding.live_options ? { live_options: structuredClone(binding.live_options) } : {}) })),
    },
  }
}

export function scenarioDraftReducer(state: ScenarioDraftState, action: ScenarioDraftAction): ScenarioDraftState {
  switch (action.type) {
    case 'setField':
      return {
        ...state,
        [action.field]: action.value,
      }
    case 'setGoalConfigField':
      return {
        ...state,
        goalConfig: {
          ...state.goalConfig,
          [action.field]: action.value,
        },
      }
    case 'setRuntimeSettingField':
      return {
        ...state,
        runtimeSettings: updateRuntimeSetting(state.runtimeSettings, action.field, action.value),
      }
    case 'replaceRuntimeSettings':
      return {
        ...state,
        runtimeSettings: cloneRuntimeSettingsDraft(action.value),
      }
    case 'updateGroup':
      return {
        ...state,
        groups: state.groups.map((group, index) => (
          index === action.index ? updateScenarioGroup(group, action.patch) : group
        )),
      }
    case 'addGroup':
      return {
        ...state,
        groups: [...state.groups, state.revisionContext
          ? { ...action.group, revisionSourceGroupIndex: undefined, revisionNewGroup: true }
          : action.group],
      }
    case 'removeGroup':
      if (state.groups.length <= 1) {
        return state
      }

      return {
        ...state,
        groups: state.groups.filter((_, index) => index !== action.index),
      }
    case 'moveGroup': {
      // A moved group keeps its own identity (and a revision's source group
      // index), so a reorder never re-points an override at another group.
      const { from, to } = action
      if (from === to || from < 0 || to < 0 || from >= state.groups.length || to >= state.groups.length) return state
      const groups = [...state.groups]
      const [moved] = groups.splice(from, 1)
      groups.splice(to, 0, moved)
      return { ...state, groups }
    }
    default:
      return state
  }
}
