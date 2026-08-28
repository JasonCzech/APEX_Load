import type { AuthoringHealth } from './authoring'
import type { ScenarioDataPoolBinding } from './dataPools'
import type { GoalConfig, ScenarioMode } from './goals'
import type { LoadStageDraft } from './loadModels'
import type { RuntimeSettingsOverride } from './runtimeSettings'
import type { ScenarioSchedule } from './scheduling'
import type { ScriptDefinition } from './scripts'
import type { GeneratorAssignmentSettings } from './testConfiguration'
import type { TrafficProfile } from './trafficProfiles'

export interface ReleaseRef {
  id: string
  name: string
  application_id: string
}

export interface ScenarioRef {
  id: string
  name: string
}

export interface StoredScript {
  id: string
  ref_key?: string
  name: string
  protocol: string
  version: string
  project_id?: string
  folder_id?: string | null
  created_at: string
  scenario_count?: number
  associated_scenarios?: ScenarioRef[]
  associated_releases?: ReleaseRef[]
  scenario_run_releases?: ReleaseRef[]
  last_run_at?: string
  authoring_health?: AuthoringHealth
  script?: ScriptDefinition
}

export interface StoredScenario {
  id: string
  ref_key?: string
  name: string
  workload_shape_id?: string
  project_id?: string
  duration: string
  start_delay?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  traffic_profile?: TrafficProfile
  runtime_settings?: RuntimeSettingsOverride
  scripts: Array<{
    order: number
    name?: string
    script_id: string
    script_name?: string
    protocol?: string
    role?: 'load' | 'observer'
    vusers: number
    ramp_up?: string
		ramp_down?: string
		start_offset?: string
    stages?: LoadStageDraft[]
    iterations?: number
    runtime_settings?: RuntimeSettingsOverride
    initialization?: 'at_runtime' | 'at_once'
    assignment?: GeneratorAssignmentSettings
  }>
  schedules?: ScenarioSchedule[]
  data_pools?: ScenarioDataPoolBinding[]
  created_at: string
  updated_at: string
  script_count: number
  result_count?: number
  associated_releases?: ReleaseRef[]
  last_run_test_id?: string
  last_run_at?: string
  last_run_ended_at?: string
}
