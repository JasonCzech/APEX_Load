import { GeneratorAssignmentSettings, GoalConfig, ScenarioDataPoolBinding, ScenarioDraftGroup, ScenarioMode } from '../types'
import { normalizeRequiredLabels } from './assignmentLabels'
import { ScenarioRuntimeSettings } from './scenarioDraftUtils'
import { BrowserConfig, ErrorHandlingConfig, HTTPConfig, LogConfig, PacingConfig, RunLogicConfig, ThinkTimeConfig, WebRuntimeConfig } from '../types/scripts'

export type ScenarioGroupPayload = {
  name?: string
  script_id: string
  role?: 'load' | 'observer'
  vusers: number
  ramp_up?: string
  ramp_down?: string
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
  }
  data_pools?: ScenarioDataPoolBinding[]
}

export type CreateTestPayload = {
  name?: string
  duration: string
  start_delay?: string
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
  projectId?: string
  folderId?: string | null
}

export function buildScenarioGroupPayloads(groups: ScenarioDraftGroup[]): ScenarioGroupPayload[] {
  return groups.map((group) => {
    const payload: ScenarioGroupPayload = {
      script_id: group.scriptId,
      vusers: group.vusers,
      initialization: group.initialization,
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

    if (group.iterations !== undefined) {
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
  if (!assignment?.mode && !assignment?.required_labels) return undefined
  const requiredLabels = normalizeRequiredLabels(assignment.required_labels)
  if (assignment.mode === 'specific_hosts') {
    const selectedTargets = normalizeSelectedTargets(assignment.selected_targets)
    if (selectedTargets.length === 0) return undefined
    return {
      mode: 'specific_hosts',
      selected_targets: selectedTargets,
      ...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
      ...(requiredLabels ? { required_labels: requiredLabels } : {}),
    }
  }
  if (assignment.mode === 'auto' || requiredLabels) {
    return {
      mode: 'auto',
      ...(requiredLabels ? { required_labels: requiredLabels } : {}),
    }
  }
  return undefined
}

function buildRootAssignmentPayload(assignment?: GeneratorAssignmentSettings): GeneratorAssignmentSettings {
  const requiredLabels = normalizeRequiredLabels(assignment?.required_labels)
  const lossTolerance = assignment?.tolerate_generator_loss ? { tolerate_generator_loss: true } : {}
  if (assignment?.mode === 'specific_hosts') {
    const selectedTargets = normalizeSelectedTargets(assignment.selected_targets)
    if (selectedTargets.length > 0) {
      return {
        mode: 'specific_hosts',
        selected_targets: selectedTargets,
        ...(selectedTargets.length > 1 ? { shard_strategy: 'capacity_weighted' as const } : {}),
        ...(requiredLabels ? { required_labels: requiredLabels } : {}),
        ...lossTolerance,
      }
    }
  }
  if (assignment?.mode === 'region_weighted' && assignment.region_weights) {
    return { mode: 'region_weighted', region_weights: assignment.region_weights, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...lossTolerance }
  }
  if (assignment?.mode === 'load_zone_weighted' && assignment.load_zone_weights) {
    return { mode: 'load_zone_weighted', load_zone_weights: assignment.load_zone_weights, ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...lossTolerance }
  }
  return { mode: 'auto', ...(requiredLabels ? { required_labels: requiredLabels } : {}), ...lossTolerance }
}

function normalizeSelectedTargets(targets?: string[]) {
  return Array.from(new Set((targets || []).map(target => target.trim()).filter(Boolean)))
}


export function buildGoalConfigPayload(scenarioMode: ScenarioMode, goalConfig: GoalConfig): GoalConfig | undefined {
  if (scenarioMode !== 'goal_oriented') {
    return undefined
  }

  const payload: GoalConfig = {
    ...(goalConfig.target_average_tps ? { target_average_tps: goalConfig.target_average_tps } : {}),
    ...(goalConfig.p95_latency_ms ? { p95_latency_ms: goalConfig.p95_latency_ms } : {}),
    ...(goalConfig.max_error_pct ? { max_error_pct: goalConfig.max_error_pct } : {}),
    ...(goalConfig.max_avg_server_processing_ms ? { max_avg_server_processing_ms: goalConfig.max_avg_server_processing_ms } : {}),
    ...(goalConfig.max_avg_transfer_ms ? { max_avg_transfer_ms: goalConfig.max_avg_transfer_ms } : {}),
    ...(goalConfig.max_error_count ? { max_error_count: goalConfig.max_error_count } : {}),
    ...(goalConfig.min_vusers ? { min_vusers: goalConfig.min_vusers } : {}),
    ...(goalConfig.max_vusers ? { max_vusers: goalConfig.max_vusers } : {}),
    ...(goalConfig.stop_on_sla_breach ? { stop_on_sla_breach: true } : {}),
    ...(goalConfig.stop_on_max_errors ? { stop_on_max_errors: true } : {}),
    ...(goalConfig.sla_breach_window ? { sla_breach_window: goalConfig.sla_breach_window } : {}),
  }

  return Object.keys(payload).length > 0 ? payload : undefined
}

export function buildRuntimeSettingsPayload(runtimeSettings: ScenarioRuntimeSettings): ScenarioUpsertPayload['runtime_settings'] | undefined {
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
  const browser = runtimeSettings.browser && (
    runtimeSettings.browser.headless !== undefined
    || runtimeSettings.browser.device_profile
    || runtimeSettings.browser.touch_mode !== undefined
    || runtimeSettings.browser.orientation
  )
    ? { ...runtimeSettings.browser }
    : undefined

  if (!runtimeSettings.iterations && !pacing && !thinkTime && !browser && !runtimeSettings.log_level && !runtimeSettings.auto_transactions && !runtimeSettings.continue_on_error && !runtimeSettings.snapshot_on_error && runtimeSettings.max_retries === undefined && runtimeSettings.retry_delay_ms === undefined && !runtimeSettings.retry_backoff && !runtimeSettings.log && !runtimeSettings.run_logic && !runtimeSettings.http && !runtimeSettings.web_runtime && !runtimeSettings.browser_config) {
    return undefined
  }

  return {
    iterations: runtimeSettings.iterations ? Number(runtimeSettings.iterations) : undefined,
    pacing,
    think_time: thinkTime,
    browser,
    log_level: runtimeSettings.log_level || undefined,
    auto_transactions: runtimeSettings.auto_transactions || undefined,
    error_handling: runtimeSettings.continue_on_error || runtimeSettings.snapshot_on_error || runtimeSettings.max_retries !== undefined || runtimeSettings.retry_delay_ms !== undefined || runtimeSettings.retry_backoff
      ? {
        continue_on_error: runtimeSettings.continue_on_error || undefined,
        snapshot_on_error: runtimeSettings.snapshot_on_error || undefined,
        max_retries: runtimeSettings.max_retries,
        retry_delay_ms: runtimeSettings.retry_delay_ms,
        retry_backoff: runtimeSettings.retry_backoff || undefined,
      }
      : undefined,
    log: runtimeSettings.log,
    run_logic: runtimeSettings.run_logic,
    http: runtimeSettings.http,
    web_runtime: runtimeSettings.web_runtime,
    browser_config: runtimeSettings.browser_config,
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
  projectId,
  folderId,
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

  if (folderId !== undefined) {
    payload.folder_id = folderId
  }

  const normalizedGroups = buildScenarioGroupPayloads(groups)
  if (normalizedGroups.length === 1 && !normalizedGroups[0].name && normalizedGroups[0].iterations === undefined && !normalizedGroups[0].stages?.length && !normalizedGroups[0].assignment) {
    payload.script_id = normalizedGroups[0].script_id
    payload.vusers = normalizedGroups[0].vusers
    payload.ramp_up = normalizedGroups[0].ramp_up
    payload.ramp_down = normalizedGroups[0].ramp_down
    payload.initialization = normalizedGroups[0].initialization
    return payload
  }

  payload.groups = normalizedGroups
  return payload
}
