import { GeneratorAssignmentSettings, GoalConfig, RuntimeJavaVuserSettings, ScenarioDataPoolBinding, ScenarioDraftGroup, ScenarioMode } from '../types'
import { normalizeRequiredLabels } from './assignmentLabels'
import { ScenarioRuntimeSettings } from './scenarioDraftUtils'
import { BrowserConfig, BrowserEmulationConfig, ErrorHandlingConfig, HTTPConfig, LogConfig, PacingConfig, RunLogicConfig, ThinkTimeConfig, WebRuntimeConfig } from '../types/scripts'

export type ScenarioGroupPayload = {
  name?: string
  script_id: string
  revision_source_group_index?: number
  revision_new_group?: boolean
  role?: 'load' | 'observer'
  vusers: number
  ramp_up?: string
	ramp_down?: string
	start_offset?: string
  stages?: Array<{
    name?: string
    type: 'ramp_up' | 'hold' | 'ramp_down' | string
    duration: string
    target_vusers?: number
    target_tps?: number
  }>
  iterations?: number
  initialization: ScenarioDraftGroup['initialization']
  runtime_settings?: ScenarioUpsertPayload['runtime_settings']
  assignment?: GeneratorAssignmentSettings
}

export type ScenarioUpsertPayload = {
  name: string
  duration: string
  start_delay?: string
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  assignment?: GeneratorAssignmentSettings
  groups: ScenarioGroupPayload[]
  goal_config?: GoalConfig
  runtime_settings?: {
    iterations?: number
    pacing?: PacingConfig
    think_time?: ThinkTimeConfig
    browser?: Pick<BrowserConfig, 'headless' | 'device_profile' | 'touch_mode' | 'orientation'>
    log_level?: string
    auto_transactions?: string
    error_handling?: ErrorHandlingConfig
    log?: LogConfig
    run_logic?: RunLogicConfig
    http?: HTTPConfig
    web_runtime?: WebRuntimeConfig
    browser_config?: BrowserConfig
    browser_emulation?: BrowserEmulationConfig
    java_vuser?: RuntimeJavaVuserSettings
    base_url?: string
    env?: Record<string, string>
    redaction_rules?: Array<{ name?: string, pattern: string, replacement?: string }>
  }
  data_pools?: ScenarioDataPoolBinding[]
}

export type CreateTestPayload = {
  name?: string
  duration: string
  start_delay?: string
  application_id?: string
  release_id?: string
  scenario_id?: string
  revision_source_test_id?: string
  project_id?: string
  folder_id?: string | null
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  assignment?: GeneratorAssignmentSettings
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  initialization?: ScenarioDraftGroup['initialization']
  groups?: ScenarioGroupPayload[]
  goal_config?: GoalConfig
  runtime_settings?: ScenarioUpsertPayload['runtime_settings']
  data_pools?: ScenarioDataPoolBinding[]
}

type ScenarioPayloadInput = {
  duration: string
  startDelay: string
  scenarioMode: ScenarioMode
  scriptShakeout: boolean
  resourceBaseline: boolean
  assignment?: GeneratorAssignmentSettings
  groups: ScenarioDraftGroup[]
  goalConfig: GoalConfig
  runtimeSettings: ScenarioRuntimeSettings
}

type ScenarioUpsertPayloadInput = ScenarioPayloadInput & {
  name: string
  dataPools?: ScenarioDataPoolBinding[]
}

type CreateTestPayloadInput = ScenarioPayloadInput & {
  name?: string
  applicationId?: string
  releaseId?: string
  scenarioId?: string
  revisionSourceTestId?: string
  projectId?: string
  folderId?: string | null
  dataPools?: ScenarioDataPoolBinding[]
}

export function buildScenarioGroupPayloads(groups: ScenarioDraftGroup[]): ScenarioGroupPayload[] {
  return groups.map((group) => {
    const payload: ScenarioGroupPayload = {
      script_id: group.scriptId,
      vusers: group.vusers,
      initialization: group.initialization,
    }

    if (group.revisionSourceGroupIndex !== undefined) {
      payload.revision_source_group_index = group.revisionSourceGroupIndex
    }
    if (group.revisionNewGroup) {
      payload.revision_new_group = true
    }

    if (group.name) {
      payload.name = group.name
    }

    if (group.role && group.role !== 'load') {
      payload.role = group.role
    }

    if (group.rampUp) {
      payload.ramp_up = group.rampUp
    }

		if (group.rampDown) {
			payload.ramp_down = group.rampDown
		}

		if (group.startOffset) {
			payload.start_offset = group.startOffset
		}

    if (group.stages && group.stages.length > 0) {
      payload.stages = group.stages
        .map(stage => ({
          ...(stage.name ? { name: stage.name } : {}),
          type: stage.type,
          duration: stage.duration,
          ...(stage.target_vusers !== undefined ? { target_vusers: Number(stage.target_vusers) } : {}),
          ...(stage.target_tps !== undefined ? { target_tps: Number(stage.target_tps) } : {}),
        }))
    }

    if (group.iterations !== undefined && group.iterationPayloadSource !== 'runtime') {
      payload.iterations = Number(group.iterations)
    }

    if (group.runtimeSettings) {
      const runtimeSettings = buildRuntimeSettingsPayload(group.runtimeSettings)
      if (runtimeSettings) {
        payload.runtime_settings = runtimeSettings
      }
    }

    const assignment = buildGroupAssignmentPayload(group.assignment)
    if (assignment) {
      payload.assignment = assignment
    }

    return payload
  })
}

export function buildGroupAssignmentPayload(assignment?: GeneratorAssignmentSettings): GeneratorAssignmentSettings | undefined {
	if (!assignment?.mode && !assignment?.required_labels && !assignment?.tolerate_generator_loss) return undefined
	const requiredLabels = normalizeRequiredLabels(assignment.required_labels)
	const lossTolerance = assignment.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}
	if (assignment.mode === 'specific_hosts') {
		const selectedTargets = normalizeSelectedTargets(assignment.selected_targets)
		return {
			mode: 'specific_hosts',
			selected_targets: selectedTargets,
			...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
			...(requiredLabels ? { required_labels: requiredLabels } : {}),
			...lossTolerance,
		}
	}
	if (assignment.mode === 'region_weighted' && assignment.region_weights) {
		return {
			mode: 'region_weighted',
			region_weights: { ...assignment.region_weights },
			...(requiredLabels ? { required_labels: requiredLabels } : {}),
			...lossTolerance,
		}
	}
	if (assignment.mode === 'load_zone_weighted' && assignment.load_zone_weights) {
		return {
			mode: 'load_zone_weighted',
			load_zone_weights: { ...assignment.load_zone_weights },
			...(requiredLabels ? { required_labels: requiredLabels } : {}),
			...lossTolerance,
		}
	}
	if (assignment.mode === 'auto' || requiredLabels || assignment.tolerate_generator_loss) {
		return {
			mode: 'auto',
			...(requiredLabels ? { required_labels: requiredLabels } : {}),
			...lossTolerance,
		}
	}
  return undefined
}

function buildRootAssignmentPayload(assignment?: GeneratorAssignmentSettings): GeneratorAssignmentSettings {
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels)
  const lossTolerance = assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}
  const pool = assignment?.pool_id?.trim() ? { pool_id: assignment.pool_id.trim() } : {}
  if (assignment?.mode === 'specific_hosts') {
    const selectedTargets = normalizeSelectedTargets(assignment.selected_targets)
    return {
      mode: 'specific_hosts',
      selected_targets: selectedTargets,
      ...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
      ...(requiredLabels ? { required_labels: requiredLabels } : {}),
      ...pool,
      ...lossTolerance,
    }
  }
  if (assignment?.mode === 'region_weighted' && assignment.region_weights) {
    return { mode: 'region_weighted', region_weights: assignment.region_weights, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...pool, ...lossTolerance }
  }
  if (assignment?.mode === 'load_zone_weighted' && assignment.load_zone_weights) {
    return { mode: 'load_zone_weighted', load_zone_weights: assignment.load_zone_weights, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...pool, ...lossTolerance }
  }
  return { mode: 'auto', ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...pool, ...lossTolerance }
}

function normalizeSelectedTargets(targets?: string[]) {
  return Array.from(new Set((targets || []).map(target => target.trim()).filter(Boolean)))
}


export function buildGoalConfigPayload(scenarioMode: ScenarioMode, goalConfig: GoalConfig = {}): GoalConfig | undefined {
	const transactionRules = goalConfig.transaction_rules?.map(rule => ({ ...rule }))
	if (scenarioMode !== 'goal_oriented') {
		return transactionRules?.length ? { transaction_rules: transactionRules } : undefined
	}

  const hasValue = (value: number | undefined) => value !== undefined && value !== null
  const payload: GoalConfig = {
    ...(hasValue(goalConfig.target_average_tps) ? { target_average_tps: goalConfig.target_average_tps } : {}),
    ...(hasValue(goalConfig.p95_latency_ms) ? { p95_latency_ms: goalConfig.p95_latency_ms } : {}),
    ...(hasValue(goalConfig.max_error_pct) ? { max_error_pct: goalConfig.max_error_pct } : {}),
    ...(hasValue(goalConfig.max_avg_server_processing_ms) ? { max_avg_server_processing_ms: goalConfig.max_avg_server_processing_ms } : {}),
    ...(hasValue(goalConfig.max_avg_transfer_ms) ? { max_avg_transfer_ms: goalConfig.max_avg_transfer_ms } : {}),
    ...(hasValue(goalConfig.max_error_count) ? { max_error_count: goalConfig.max_error_count } : {}),
    ...(hasValue(goalConfig.min_vusers) ? { min_vusers: goalConfig.min_vusers } : {}),
    ...(hasValue(goalConfig.max_vusers) ? { max_vusers: goalConfig.max_vusers } : {}),
    ...(hasValue(goalConfig.max_lcp_ms) ? { max_lcp_ms: goalConfig.max_lcp_ms } : {}),
    ...(hasValue(goalConfig.max_inp_ms) ? { max_inp_ms: goalConfig.max_inp_ms } : {}),
    ...(hasValue(goalConfig.max_cls) ? { max_cls: goalConfig.max_cls } : {}),
    ...(hasValue(goalConfig.max_throttled_requests) ? { max_throttled_requests: goalConfig.max_throttled_requests } : {}),
    ...(hasValue(goalConfig.max_end_to_end_latency_ms) ? { max_end_to_end_latency_ms: goalConfig.max_end_to_end_latency_ms } : {}),
    ...(hasValue(goalConfig.max_partition_skew_pct) ? { max_partition_skew_pct: goalConfig.max_partition_skew_pct } : {}),
    ...(hasValue(goalConfig.max_redelivery_rate_pct) ? { max_redelivery_rate_pct: goalConfig.max_redelivery_rate_pct } : {}),
    ...(hasValue(goalConfig.max_dead_letter_rate_pct) ? { max_dead_letter_rate_pct: goalConfig.max_dead_letter_rate_pct } : {}),
		...(transactionRules?.length ? { transaction_rules: transactionRules } : {}),
    ...(goalConfig.stop_on_sla_breach ? { stop_on_sla_breach: true } : {}),
    ...(goalConfig.stop_on_max_errors ? { stop_on_max_errors: true } : {}),
    ...(goalConfig.sla_breach_window ? { sla_breach_window: goalConfig.sla_breach_window } : {}),
  }

  return Object.keys(payload).length > 0 ? payload : undefined
}

// hasJavaVuserOverride mirrors isJavaVuserSettingsEmpty (runtimeSettingsModel):
// a {} or all-empty Java Env section carries no override and is not serialized.
function hasJavaVuserOverride(java?: RuntimeJavaVuserSettings): boolean {
  if (!java) return false
  return Boolean(
    java.jdk_selection
    || java.jdk_home
    || (java.additional_vm_parameters && java.additional_vm_parameters.length > 0)
    || (java.classpath && java.classpath.length > 0)
    || java.use_xbootclasspath !== undefined
    || java.dedicated_class_loader !== undefined
    || java.skip_parameter_evaluation !== undefined,
  )
}

export function buildRuntimeSettingsPayload(runtimeSettings: ScenarioRuntimeSettings): ScenarioUpsertPayload['runtime_settings'] | undefined {
  const hasCanonicalRunLogic = runtimeSettings.run_logic !== undefined
  const hasCanonicalLog = runtimeSettings.log !== undefined
  const hasCanonicalBrowser = runtimeSettings.browser_config !== undefined
  const pacing = runtimeSettings.pacing && runtimeSettings.pacing.type
    ? { ...runtimeSettings.pacing }
    : undefined
  const thinkTime = runtimeSettings.think_time && (
    runtimeSettings.think_time.ignore
    || runtimeSettings.think_time.type
    || runtimeSettings.think_time.mean_ms !== undefined
    || runtimeSettings.think_time.std_ms !== undefined
    || runtimeSettings.think_time.min_ms !== undefined
    || runtimeSettings.think_time.max_ms !== undefined
    || runtimeSettings.think_time.multiplier !== undefined
    || runtimeSettings.think_time.limit_ms !== undefined
  )
    ? { ...runtimeSettings.think_time }
    : undefined
  const browser = !hasCanonicalBrowser && runtimeSettings.browser && (
    runtimeSettings.browser.headless !== undefined
    || runtimeSettings.browser.device_profile
    || runtimeSettings.browser.touch_mode !== undefined
    || runtimeSettings.browser.orientation
  )
    ? { ...runtimeSettings.browser }
    : undefined

  const hasErrorHandling = runtimeSettings.continue_on_error !== undefined
    || runtimeSettings.snapshot_on_error !== undefined
    || runtimeSettings.max_retries !== undefined
    || runtimeSettings.retry_delay_ms !== undefined
    || Boolean(runtimeSettings.retry_backoff)

  const javaVuser = hasJavaVuserOverride(runtimeSettings.java_vuser) ? runtimeSettings.java_vuser : undefined

  if ((!hasCanonicalRunLogic && runtimeSettings.iterations === undefined) && !pacing && !thinkTime && !browser && (!hasCanonicalLog && !runtimeSettings.log_level) && (!hasCanonicalRunLogic && !runtimeSettings.auto_transactions) && !hasErrorHandling && !runtimeSettings.log && !runtimeSettings.run_logic && !runtimeSettings.http && !runtimeSettings.web_runtime && !runtimeSettings.browser_config && !runtimeSettings.browser_emulation && !javaVuser && !runtimeSettings.base_url && !runtimeSettings.env && !runtimeSettings.redaction_rules) {
    return undefined
  }

  return {
    ...(!hasCanonicalRunLogic && runtimeSettings.iterations !== undefined
      ? { iterations: Number(runtimeSettings.iterations) }
      : {}),
    ...(pacing ? { pacing } : {}),
    ...(thinkTime ? { think_time: thinkTime } : {}),
    ...(browser ? { browser } : {}),
    ...(!hasCanonicalLog && runtimeSettings.log_level ? { log_level: runtimeSettings.log_level } : {}),
    ...(!hasCanonicalRunLogic && runtimeSettings.auto_transactions
      ? { auto_transactions: runtimeSettings.auto_transactions }
      : {}),
    ...(hasErrorHandling ? { error_handling: {
        ...(runtimeSettings.continue_on_error !== undefined ? { continue_on_error: runtimeSettings.continue_on_error } : {}),
        ...(runtimeSettings.snapshot_on_error !== undefined ? { snapshot_on_error: runtimeSettings.snapshot_on_error } : {}),
        ...(runtimeSettings.max_retries !== undefined ? { max_retries: runtimeSettings.max_retries } : {}),
        ...(runtimeSettings.retry_delay_ms !== undefined ? { retry_delay_ms: runtimeSettings.retry_delay_ms } : {}),
        ...(runtimeSettings.retry_backoff ? { retry_backoff: runtimeSettings.retry_backoff } : {}),
      } } : {}),
    ...(runtimeSettings.log !== undefined ? { log: runtimeSettings.log } : {}),
    ...(runtimeSettings.run_logic !== undefined ? { run_logic: runtimeSettings.run_logic } : {}),
    ...(runtimeSettings.http !== undefined ? { http: runtimeSettings.http } : {}),
    ...(runtimeSettings.web_runtime !== undefined ? { web_runtime: runtimeSettings.web_runtime } : {}),
    ...(runtimeSettings.browser_config !== undefined ? { browser_config: runtimeSettings.browser_config } : {}),
    ...(runtimeSettings.browser_emulation !== undefined ? { browser_emulation: runtimeSettings.browser_emulation } : {}),
    ...(javaVuser ? { java_vuser: { ...javaVuser } } : {}),
    ...(runtimeSettings.base_url !== undefined ? { base_url: runtimeSettings.base_url } : {}),
    ...(runtimeSettings.env ? { env: { ...runtimeSettings.env } } : {}),
    ...(runtimeSettings.redaction_rules ? { redaction_rules: runtimeSettings.redaction_rules.map(rule => ({ ...rule })) } : {}),
  }
}

export function buildScenarioUpsertPayload({
  name,
  duration,
  startDelay,
  scenarioMode,
  scriptShakeout,
  resourceBaseline,
  assignment,
  groups,
  goalConfig,
  runtimeSettings,
  dataPools,
}: ScenarioUpsertPayloadInput): ScenarioUpsertPayload {
  const resolvedAssignment = buildRootAssignmentPayload(assignment)

  const payload: ScenarioUpsertPayload = {
    name: name.trim(),
    duration,
    scenario_mode: scenarioMode,
    script_shakeout: scriptShakeout,
    resource_baseline: resourceBaseline,
    assignment: resolvedAssignment,
    groups: buildScenarioGroupPayloads(groups),
  }

  if (startDelay) {
    payload.start_delay = startDelay
  }

  const goalConfigPayload = buildGoalConfigPayload(scenarioMode, goalConfig)
  if (goalConfigPayload) {
    payload.goal_config = goalConfigPayload
  }

  const runtimeSettingsPayload = buildRuntimeSettingsPayload(runtimeSettings)
  if (runtimeSettingsPayload) {
    payload.runtime_settings = runtimeSettingsPayload
  }

  if (dataPools) {
    payload.data_pools = dataPools
  }

  return payload
}

export function buildCreateTestPayload({
  name,
  duration,
  startDelay,
  scenarioMode,
  scriptShakeout,
  resourceBaseline,
  assignment,
  groups,
  goalConfig,
  runtimeSettings,
  applicationId,
  releaseId,
  scenarioId,
  revisionSourceTestId,
  projectId,
  folderId,
  dataPools,
}: CreateTestPayloadInput): CreateTestPayload {
  const resolvedAssignment = buildRootAssignmentPayload(assignment)

  const payload: CreateTestPayload = {
    duration,
    scenario_mode: scenarioMode,
    script_shakeout: scriptShakeout,
    resource_baseline: resourceBaseline,
    assignment: resolvedAssignment,
  }

  if (startDelay) {
    payload.start_delay = startDelay
  }

  if (name) {
    payload.name = name
  }

  const goalConfigPayload = buildGoalConfigPayload(scenarioMode, goalConfig)
  if (goalConfigPayload) {
    payload.goal_config = goalConfigPayload
  }

  const runtimeSettingsPayload = buildRuntimeSettingsPayload(runtimeSettings)
  if (runtimeSettingsPayload) {
    payload.runtime_settings = runtimeSettingsPayload
  }

  if (projectId) {
    payload.project_id = projectId
  }

  if (applicationId) {
    payload.application_id = applicationId
  }

  if (releaseId) {
    payload.release_id = releaseId
  }

  if (scenarioId) {
    payload.scenario_id = scenarioId
  }

  if (revisionSourceTestId) {
    payload.revision_source_test_id = revisionSourceTestId
  }

  if (folderId !== undefined) {
    payload.folder_id = folderId
  }

  if (dataPools !== undefined) {
    payload.data_pools = dataPools.map(binding => ({ ...binding }))
  }

  // Always use the canonical group representation. The legacy single-script
  // shorthand cannot represent the complete group contract (role, ramp-down,
  // initialization, stages, iterations, runtime overrides, and placement), so
  // flattening a seemingly-simple group makes later editor additions silently
  // disappear at the API boundary.
  payload.groups = buildScenarioGroupPayloads(groups)
  return payload
}
