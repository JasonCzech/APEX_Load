import type { JointEvidenceProjection } from '../types/jointEvidence'
import type { MonitorBinding } from '../types/runMonitoring'
import { GeneratorAssignmentSettings, GoalConfig, RuntimeJavaVuserSettings, ScenarioDataPoolBinding, ScenarioDraftGroup, ScenarioMode, TrafficProfile } from '../types'
import { normalizeRequiredLabels } from './assignmentLabels'
import { ScenarioRuntimeSettings } from './scenarioDraftUtils'
import { BrowserConfig, BrowserEmulationConfig, ErrorHandlingConfig, HTTPConfig, LogConfig, PacingConfig, RunLogicConfig, ThinkTimeConfig, WebRuntimeConfig, WebSocketConfig, ScriptResultsConfig } from '../types/scripts'

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
  termination_mode?: 'duration' | 'iterations'
    target_vusers?: number
    target_tps?: number
  }>
  iterations?: number
  initialization: ScenarioDraftGroup['initialization']
  runtime_settings?: ScenarioUpsertPayload['runtime_settings']
  assignment?: GeneratorAssignmentSettings
	transaction_response_time_sla_ms?: number
}

export type ScenarioUpsertPayload = {
  name: string
  project_id?: string
  workload_shape_id?: string
  duration: string
  termination_mode?: 'duration' | 'iterations'
  start_delay?: string
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  assignment?: GeneratorAssignmentSettings
  groups: ScenarioGroupPayload[]
  goal_config?: GoalConfig
  traffic_profile?: TrafficProfile
  joint_evidence?: JointEvidenceProjection
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
    results?: ScriptResultsConfig
    websocket?: WebSocketConfig
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
  monitor_bindings?: MonitorBinding[]
  name?: string
  duration: string
  termination_mode?: 'duration' | 'iterations'
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
  traffic_profile?: TrafficProfile
  joint_evidence?: JointEvidenceProjection
  runtime_settings?: ScenarioUpsertPayload['runtime_settings']
  data_pools?: ScenarioDataPoolBinding[]
  elk_capture?: { connection_id: string }
  dynatrace_capture?: { connection_id: string }
}

type ScenarioPayloadInput = {
  duration: string
  termination_mode?: 'duration' | 'iterations'
  startDelay: string
  scenarioMode: ScenarioMode
  scriptShakeout: boolean
  resourceBaseline: boolean
  assignment?: GeneratorAssignmentSettings
  groups: ScenarioDraftGroup[]
  goalConfig: GoalConfig
  runtimeSettings: ScenarioRuntimeSettings
  trafficProfile?: TrafficProfile
  jointEvidence?: JointEvidenceProjection
}

type ScenarioUpsertPayloadInput = ScenarioPayloadInput & {
  name: string
  projectId?: string
  workloadShapeId?: string
  dataPools?: ScenarioDataPoolBinding[]
}

type CreateTestPayloadInput = ScenarioPayloadInput & {
  monitorBindings?: MonitorBinding[]
  name?: string
  applicationId?: string
  releaseId?: string
  scenarioId?: string
  revisionSourceTestId?: string
  /** Private source presence; never emitted as an API field. */
  sourceGoalConfigPresent?: boolean
  sourceRuntimeSettingsPresent?: boolean
  sourceStartDelayPresent?: boolean
  projectId?: string
  folderId?: string | null
  dataPools?: ScenarioDataPoolBinding[]
  elkConnectionId?: string
  dynatraceConnectionId?: string
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

		if (group.transactionResponseTimeSlaMs !== undefined) {
			payload.transaction_response_time_sla_ms = group.transactionResponseTimeSlaMs
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
	if (!assignment?.mode && !assignment?.required_labels && !assignment?.tolerate_generator_loss && !assignment?.java_host) return undefined
	const requiredLabels = normalizeRequiredLabels(assignment.required_labels)
	if (assignment.mode === 'inherit') {
		return assignment.java_host ? { mode: 'inherit', java_host: { ...assignment.java_host } } : undefined
	}
	const lossTolerance = { ...(assignment.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}), ...(assignment.java_host ? { java_host: { ...assignment.java_host } } : {}) }
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
	if (assignment.mode === 'auto' || requiredLabels || assignment.tolerate_generator_loss || assignment.java_host) {
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
  const lossTolerance = {
    ...(assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}),
    // Only the non-default value is sent, so fail-policy payloads are unchanged.
    ...(assignment?.failover_policy === 'continue' ? { failover_policy: 'continue' as const } : {}),
    ...(assignment?.recovery_spare_vusers !== undefined ? { recovery_spare_vusers: assignment?.recovery_spare_vusers } : {}),
  }
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
  const hasValue = (value: number | undefined) => value !== undefined && value !== null
  // Guardrails are authored configuration in both modes. Runtime mode decides
  // their execution behavior; changing the workload must not erase the receipt.
  const payload: GoalConfig = {
    ...(goalConfig.monitor_rules !== undefined ? { monitor_rules: structuredClone(goalConfig.monitor_rules) } : {}),
    ...(hasValue(goalConfig.transaction_response_time_sla_ms) ? { transaction_response_time_sla_ms: goalConfig.transaction_response_time_sla_ms } : {}),
    ...(hasValue(goalConfig.p95_latency_ms) ? { p95_latency_ms: goalConfig.p95_latency_ms } : {}),
    ...(hasValue(goalConfig.max_error_pct) ? { max_error_pct: goalConfig.max_error_pct } : {}),
    ...(hasValue(goalConfig.max_avg_server_processing_ms) ? { max_avg_server_processing_ms: goalConfig.max_avg_server_processing_ms } : {}),
    ...(hasValue(goalConfig.max_avg_transfer_ms) ? { max_avg_transfer_ms: goalConfig.max_avg_transfer_ms } : {}),
    ...(hasValue(goalConfig.max_error_count) ? { max_error_count: goalConfig.max_error_count } : {}),
    ...(hasValue(goalConfig.max_lcp_ms) ? { max_lcp_ms: goalConfig.max_lcp_ms } : {}),
    ...(hasValue(goalConfig.max_inp_ms) ? { max_inp_ms: goalConfig.max_inp_ms } : {}),
    ...(hasValue(goalConfig.max_cls) ? { max_cls: goalConfig.max_cls } : {}),
    ...(hasValue(goalConfig.max_throttled_requests) ? { max_throttled_requests: goalConfig.max_throttled_requests } : {}),
    ...(hasValue(goalConfig.max_end_to_end_latency_ms) ? { max_end_to_end_latency_ms: goalConfig.max_end_to_end_latency_ms } : {}),
    ...(hasValue(goalConfig.max_partition_skew_pct) ? { max_partition_skew_pct: goalConfig.max_partition_skew_pct } : {}),
    ...(hasValue(goalConfig.max_redelivery_rate_pct) ? { max_redelivery_rate_pct: goalConfig.max_redelivery_rate_pct } : {}),
    ...(hasValue(goalConfig.max_dead_letter_rate_pct) ? { max_dead_letter_rate_pct: goalConfig.max_dead_letter_rate_pct } : {}),
    ...(goalConfig.transaction_rules !== undefined ? { transaction_rules: structuredClone(goalConfig.transaction_rules) } : {}),
    ...(goalConfig.early_stop !== undefined ? { early_stop: structuredClone(goalConfig.early_stop) } : {}),
    ...(goalConfig.workload_shape_config !== undefined ? { workload_shape_config: structuredClone(goalConfig.workload_shape_config) } : {}),
    ...(goalConfig.stop_on_sla_breach !== undefined ? { stop_on_sla_breach: goalConfig.stop_on_sla_breach } : {}),
    ...(goalConfig.stop_on_max_errors !== undefined ? { stop_on_max_errors: goalConfig.stop_on_max_errors } : {}),
    ...(goalConfig.sla_breach_window !== undefined ? { sla_breach_window: goalConfig.sla_breach_window } : {}),
    ...(scenarioMode === 'goal_oriented' && hasValue(goalConfig.target_average_tps) ? { target_average_tps: goalConfig.target_average_tps } : {}),
    ...(scenarioMode === 'goal_oriented' && hasValue(goalConfig.min_vusers) ? { min_vusers: goalConfig.min_vusers } : {}),
    ...(scenarioMode === 'goal_oriented' && hasValue(goalConfig.max_vusers) ? { max_vusers: goalConfig.max_vusers } : {}),
  }
  return Object.keys(payload).length ? payload : undefined
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
    || java.skip_parameter_evaluation !== undefined
    || java.jvm_sharing
    || java.vusers_per_jvm,
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

  if ((!hasCanonicalRunLogic && runtimeSettings.iterations === undefined) && !pacing && !thinkTime && !browser && (!hasCanonicalLog && !runtimeSettings.log_level) && (!hasCanonicalRunLogic && !runtimeSettings.auto_transactions) && !hasErrorHandling && !runtimeSettings.log && !runtimeSettings.run_logic && !runtimeSettings.http && runtimeSettings.results === undefined && runtimeSettings.websocket === undefined && !runtimeSettings.web_runtime && !runtimeSettings.browser_config && !runtimeSettings.browser_emulation && !javaVuser && !runtimeSettings.base_url && !runtimeSettings.env && !runtimeSettings.redaction_rules) {
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
    ...(runtimeSettings.results !== undefined ? { results: structuredClone(runtimeSettings.results) } : {}),
    ...(runtimeSettings.websocket !== undefined ? { websocket: structuredClone(runtimeSettings.websocket) } : {}),
    ...(runtimeSettings.web_runtime !== undefined ? { web_runtime: runtimeSettings.web_runtime } : {}),
    ...(runtimeSettings.browser_config !== undefined ? { browser_config: runtimeSettings.browser_config } : {}),
    ...(runtimeSettings.browser_emulation !== undefined ? { browser_emulation: runtimeSettings.browser_emulation } : {}),
    ...(javaVuser ? { java_vuser: { ...javaVuser } } : {}),
    ...(runtimeSettings.base_url !== undefined ? { base_url: runtimeSettings.base_url } : {}),
    ...(runtimeSettings.env ? { env: { ...runtimeSettings.env } } : {}),
    ...(runtimeSettings.redaction_rules ? { redaction_rules: runtimeSettings.redaction_rules.map(rule => ({ ...rule })) } : {}),
  }
}


// One canonical execution contract for scenario saves, new runs and revisions.
// Snapshot nested values so edits cannot mutate a submitted or validated payload.
export function buildScenarioExecutionPayload(input: ScenarioPayloadInput & { dataPools?: ScenarioDataPoolBinding[] }): Omit<ScenarioUpsertPayload, 'name' | 'project_id' | 'workload_shape_id'> {
 const goal = buildGoalConfigPayload(input.scenarioMode, input.goalConfig)
 const runtime = buildRuntimeSettingsPayload(input.runtimeSettings)
 return structuredClone({
  duration: input.duration,
  ...(input.termination_mode ? { termination_mode: input.termination_mode } : {}),
  scenario_mode: input.scenarioMode,
  script_shakeout: input.scriptShakeout,
  resource_baseline: input.resourceBaseline,
  assignment: buildRootAssignmentPayload(input.assignment),
  groups: buildScenarioGroupPayloads(input.groups),
  ...(input.startDelay ? {start_delay: input.startDelay} : {}),
  ...(goal ? {goal_config: goal} : {}),
  ...(runtime ? {runtime_settings: runtime} : {}),
  ...(input.trafficProfile ? {traffic_profile: input.trafficProfile} : {}),
  ...(input.jointEvidence ? {joint_evidence: input.jointEvidence} : {}),
  ...(input.dataPools !== undefined ? {data_pools: input.dataPools} : {}),
 })
}

export function buildScenarioUpsertPayload(input: ScenarioUpsertPayloadInput): ScenarioUpsertPayload {
 return {
  ...buildScenarioExecutionPayload(input), name: input.name.trim(),
  ...(input.projectId ? {project_id: input.projectId} : {}),
  ...(input.workloadShapeId !== undefined ? {workload_shape_id: input.workloadShapeId} : {}),
 }
}

export function buildCreateTestPayload(input: CreateTestPayloadInput): CreateTestPayload {
 const execution = buildScenarioExecutionPayload(input)
 return {
  ...execution,
  // The create API uses absent goal_config to inherit a revision source. A
  // source-owned config that was explicitly emptied must instead replace it.
  ...(input.revisionSourceTestId && input.sourceGoalConfigPresent && execution.goal_config === undefined ? { goal_config: {} } : {}),
  ...(input.revisionSourceTestId && input.sourceRuntimeSettingsPresent && execution.runtime_settings === undefined ? { runtime_settings: {} } : {}),
  ...(input.revisionSourceTestId && input.sourceStartDelayPresent && execution.start_delay === undefined ? { start_delay: '0s' } : {}),
  ...(input.monitorBindings !== undefined ? {monitor_bindings: structuredClone(input.monitorBindings)} : {}),
  ...(input.name ? {name: input.name} : {}),
  ...(input.projectId ? {project_id: input.projectId} : {}),
  ...(input.applicationId ? {application_id: input.applicationId} : {}),
  ...(input.releaseId ? {release_id: input.releaseId} : {}),
  ...(input.scenarioId ? {scenario_id: input.scenarioId} : {}),
  ...(input.revisionSourceTestId ? {revision_source_test_id: input.revisionSourceTestId} : {}),
  ...(input.folderId !== undefined ? {folder_id: input.folderId} : {}),
  ...(input.elkConnectionId ? {elk_capture: {connection_id: input.elkConnectionId}} : {}),
  ...(input.dynatraceConnectionId ? {dynatrace_capture: {connection_id: input.dynatraceConnectionId}} : {}),
 }
}
