import { GoalConfig, ScenarioDataPoolBinding, ScenarioDraftGroup, ScenarioMode } from '../types'
import { ScenarioRuntimeSettings } from './scenarioDraftUtils'

export type ScenarioGroupPayload = {
  name?: string
  script_id: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  initialization: ScenarioDraftGroup['initialization']
}

export type ScenarioUpsertPayload = {
  name: string
  duration: string
  start_delay?: string
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  groups: ScenarioGroupPayload[]
  goal_config?: GoalConfig
  runtime_settings?: {
    iterations?: number
    log_level?: string
    auto_transactions?: string
    error_handling?: {
      snapshot_on_error: true
    }
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

    if (group.rampUp) {
      payload.ramp_up = group.rampUp
    }

    if (group.rampDown) {
      payload.ramp_down = group.rampDown
    }

    return payload
  })
}

export function buildGoalConfigPayload(scenarioMode: ScenarioMode, goalConfig: GoalConfig): GoalConfig | undefined {
  if (scenarioMode !== 'goal_oriented') {
    return undefined
  }

  const payload: GoalConfig = {
    ...(goalConfig.target_average_tps ? { target_average_tps: goalConfig.target_average_tps } : {}),
    ...(goalConfig.p95_latency_ms ? { p95_latency_ms: goalConfig.p95_latency_ms } : {}),
    ...(goalConfig.max_error_pct ? { max_error_pct: goalConfig.max_error_pct } : {}),
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
  if (!runtimeSettings.iterations && !runtimeSettings.log_level && !runtimeSettings.auto_transactions && !runtimeSettings.snapshot_on_error) {
    return undefined
  }

  return {
    iterations: runtimeSettings.iterations ? Number(runtimeSettings.iterations) : undefined,
    log_level: runtimeSettings.log_level || undefined,
    auto_transactions: runtimeSettings.auto_transactions || undefined,
    error_handling: runtimeSettings.snapshot_on_error ? { snapshot_on_error: true } : undefined,
  }
}

export function buildScenarioUpsertPayload({
  name,
  duration,
  startDelay,
  scenarioMode,
  scriptShakeout,
  resourceBaseline,
  groups,
  goalConfig,
  runtimeSettings,
  dataPools,
}: ScenarioUpsertPayloadInput): ScenarioUpsertPayload {
  const payload: ScenarioUpsertPayload = {
    name: name.trim(),
    duration,
    scenario_mode: scenarioMode,
    script_shakeout: scriptShakeout,
    resource_baseline: resourceBaseline,
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
  groups,
  goalConfig,
  runtimeSettings,
  projectId,
  folderId,
}: CreateTestPayloadInput): CreateTestPayload {
  const payload: CreateTestPayload = {
    duration,
    scenario_mode: scenarioMode,
    script_shakeout: scriptShakeout,
    resource_baseline: resourceBaseline,
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
  if (normalizedGroups.length === 1 && !normalizedGroups[0].name) {
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