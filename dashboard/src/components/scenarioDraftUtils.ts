import {
  GeneratorAssignmentSettings,
  RuntimeSettingsOverride,
  ScenarioDraft,
  ScenarioDraftGroup,
  ScenarioRuntimeSettingsDraft,
  StoredScript,
  TestSummary,
} from '../types'

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

function cloneGeneratorAssignment(assignment?: GeneratorAssignmentSettings): GeneratorAssignmentSettings {
  return {
    mode: assignment?.mode || 'auto',
    ...(assignment?.pool_id ? { pool_id: assignment.pool_id } : {}),
    ...(assignment?.selected_targets ? { selected_targets: [...assignment.selected_targets] } : { selected_targets: [] }),
    ...(assignment?.region_weights ? { region_weights: { ...assignment.region_weights } } : {}),
    ...(assignment?.load_zone_weights ? { load_zone_weights: { ...assignment.load_zone_weights } } : {}),
    ...(assignment?.shard_strategy ? { shard_strategy: assignment.shard_strategy } : {}),
    ...(assignment?.required_labels ? { required_labels: { ...assignment.required_labels } } : {}),
    ...(assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}),
    ...(assignment?.failover_policy === 'continue' ? { failover_policy: 'continue' as const } : {}),
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
  return {
    ...settings,
    iterations: hasRunLogic ? settings.run_logic?.iterations : settings.iterations,
    auto_transactions: hasRunLogic ? (settings.run_logic?.auto_transactions ?? '') : (settings.auto_transactions ?? ''),
    log_level: hasLog ? (settings.log?.level ?? '') : (settings.log_level ?? ''),
    browser: hasBrowserConfig ? compactBrowserFromCanonical(settings) : settings.browser,
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
  return hydrateCanonicalRuntimeControls({
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
    snapshot_on_error: errorHandling == null ? undefined : (errorHandling.snapshot_on_error ?? false),
    max_retries: errorHandling?.max_retries,
    retry_delay_ms: errorHandling?.retry_delay_ms,
    retry_backoff: errorHandling?.retry_backoff,
    log: cloneValue(settings?.log),
    run_logic: cloneValue(settings?.run_logic),
    http: cloneValue(settings?.http),
    results: cloneValue(settings?.results),
    web_runtime: cloneValue(settings?.web_runtime),
    browser_config: cloneValue(settings?.browser_config),
    browser_emulation: cloneValue(settings?.browser_emulation),
    java_vuser: cloneValue(settings?.java_vuser),
    base_url: settings?.base_url,
    env: cloneValue(settings?.env),
    redaction_rules: cloneValue(settings?.redaction_rules),
  })
}

function withoutUndefinedValues<T extends object>(value: T): T | undefined {
  const entries = Object.entries(value).filter(([, fieldValue]) => fieldValue !== undefined && fieldValue !== '')
  return entries.length > 0 ? Object.fromEntries(entries) as T : undefined
}

function updateRuntimeSetting(
  settings: ScenarioRuntimeSettingsDraft,
  field: ScenarioRuntimeSettingField,
  value: ScenarioRuntimeSettings[ScenarioRuntimeSettingField],
): ScenarioRuntimeSettingsDraft {
  const next: ScenarioRuntimeSettingsDraft = { ...settings, [field]: value }
  if (field === 'iterations' && settings.run_logic !== undefined) {
    next.run_logic = withoutUndefinedValues({ ...settings.run_logic, iterations: value as number | undefined })
  }
  if (field === 'auto_transactions' && settings.run_logic !== undefined) {
    next.run_logic = withoutUndefinedValues({
      ...settings.run_logic,
      auto_transactions: (value as string) || undefined,
    })
  }
  if (field === 'log_level' && settings.log !== undefined) {
    next.log = withoutUndefinedValues({ ...settings.log, level: (value as string) || undefined })
  }
  if (field === 'browser' && settings.browser_config !== undefined) {
    const browser = value as ScenarioRuntimeSettingsDraft['browser']
    next.browser_config = withoutUndefinedValues({
      ...settings.browser_config,
      headless: browser?.headless,
      device_profile: browser?.device_profile,
      touch_mode: browser?.touch_mode,
      orientation: browser?.orientation,
    })
  }
  return next
}

function cloneScenarioGroup(group: ScenarioDraftGroup): ScenarioDraftGroup {
  const runtimeSettings = group.runtimeSettings ? cloneRuntimeSettingsDraft(group.runtimeSettings) : undefined
  return {
    ...group,
    stages: cloneValue(group.stages),
    iterations: runtimeSettings?.run_logic !== undefined ? runtimeSettings.run_logic.iterations : group.iterations,
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
  if (!Object.prototype.hasOwnProperty.call(patch, 'iterations')) {
    return { ...group, ...patch }
  }

  const iterations = patch.iterations
  const runtimeSettings = group.runtimeSettings
  const hasCanonicalRuntimeIteration = runtimeSettings?.run_logic?.iterations !== undefined
  const hasLegacyRuntimeIteration = runtimeSettings?.run_logic === undefined && runtimeSettings?.iterations !== undefined
  const runtimeOwnsIterations = group.iterationPayloadSource === 'runtime'
    || hasCanonicalRuntimeIteration
    || hasLegacyRuntimeIteration
  if (!runtimeOwnsIterations) {
    return { ...group, ...patch }
  }

  return {
    ...group,
    ...patch,
    iterationPayloadSource: 'runtime',
    runtimeSettings: {
      log_level: runtimeSettings?.log_level ?? '',
      auto_transactions: runtimeSettings?.auto_transactions ?? '',
      ...runtimeSettings,
      // Any edit promotes the legacy alias into the canonical section. Clearing
      // removes both representations so a stale legacy value cannot survive.
      iterations: undefined,
      run_logic: withoutUndefinedValues({ ...runtimeSettings?.run_logic, iterations }),
    },
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
    duration: initialScenario?.duration || '1m',
    startDelay: initialScenario?.startDelay || '',
    scenarioMode: initialScenario?.scenarioMode || 'vuser_driven',
    scriptShakeout: Boolean(initialScenario?.scriptShakeout),
    resourceBaseline: Boolean(initialScenario?.resourceBaseline),
    assignment: cloneGeneratorAssignment(initialScenario?.assignment),
    goalConfig: {
      ...initialScenario?.goalConfig,
			transaction_rules: initialScenario?.goalConfig?.transaction_rules?.map(rule => ({ ...rule })),
      stop_on_sla_breach: initialScenario?.goalConfig?.stop_on_sla_breach ?? false,
      stop_on_max_errors: initialScenario?.goalConfig?.stop_on_max_errors ?? false,
      sla_breach_window: initialScenario?.goalConfig?.sla_breach_window || '30s',
    },
    runtimeSettings: cloneRuntimeSettingsDraft(initialScenario?.runtimeSettings),
    groups: initialScenario?.groups.length
      ? initialScenario.groups.map(cloneScenarioGroup)
      : [buildDefaultScenarioGroup(scripts)],
    revisionContext: initialScenario?.revisionContext
      ? {
        ...initialScenario.revisionContext,
        dataPools: initialScenario.revisionContext.dataPools.map(binding => ({ ...binding })),
      }
      : undefined,
  }
}

export function buildScenarioDraftFromTest(test: TestSummary): ScenarioDraft {
  const groups = test.groups?.length
    ? test.groups.map((group, groupIndex) => {
      const runtimeSettings = group.runtime_settings ? buildRuntimeSettingsDraft(group.runtime_settings) : undefined
      const runtimeIteration = runtimeSettings?.run_logic !== undefined
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
    startDelay: test.start_delay || '',
    scenarioMode: test.scenario_mode || 'vuser_driven',
    scriptShakeout: Boolean(test.script_shakeout),
    resourceBaseline: Boolean(test.resource_baseline),
    assignment: cloneGeneratorAssignment(test.assignment),
    goalConfig: {
      ...test.goal_config,
			transaction_rules: test.goal_config?.transaction_rules?.map(rule => ({ ...rule })),
      sla_breach_window: test.goal_config?.sla_breach_window || '',
    },
    runtimeSettings: buildRuntimeSettingsDraft(test.runtime_settings),
    groups,
    revisionContext: {
      sourceTestId: test.id,
      applicationId: test.application_id,
      releaseId: test.release_id,
      scenarioId: test.scenario_id,
      projectId: test.project_id,
      folderId: test.folder_id,
      // Historical queued runs may retain access-mode labels whose write/consume
      // semantics never existed. Revisions preserve the immutable row snapshot,
      // but submit the only executable contract supported today.
      dataPools: (test.data_pools || []).map(binding => ({ ...binding, access_mode: 'read_only' as const })),
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
    default:
      return state
  }
}
