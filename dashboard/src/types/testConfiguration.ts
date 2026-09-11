import type { LoadStageDraft } from './loadModels'
import type { RuntimeSettingsOverride } from './runtimeSettings'
import type { ActiveLoadLevel } from './metrics'

/** Shared load-group and generator-assignment contracts. */
export interface TestGroup {
  name: string
  script_id: string
  script_name: string
  protocol: string
  role?: 'load' | 'observer'
  vusers: number
  target_duration?: string
  effective_end_at?: string
  runtime_state?: 'scheduled' | 'running' | 'stopping' | 'ended' | string
  ended_at?: string
  ramp_up?: string
	ramp_down?: string
	start_offset?: string
  stages?: LoadStageDraft[]
  iterations?: number
  iteration_override?: number
  iteration_override_known?: boolean
  runtime_settings?: RuntimeSettingsOverride
  initialization?: 'at_runtime' | 'at_once'
  assignment?: GeneratorAssignmentSettings
	transaction_response_time_sla_ms?: number
  /** How the scenario's enabled-by-default continue-on-error policy resolved
   *  for this group, and which layer supplied the winning value. */
  continue_on_error?: ContinueOnErrorResolution
	active_stage?: ActiveLoadLevel
}

/** ContinueOnErrorResolution explains how the scenario's enabled-by-default
 *  continue-on-error policy resolved for one script group. `conflict` marks a
 *  group whose effective value diverges from the scenario-level value. */
export interface ContinueOnErrorResolution {
  effective: boolean
  source: 'group' | 'scenario' | 'script' | 'scenario_default' | string
  scenario_value: boolean
  scenario_explicit: boolean
  conflict?: boolean
}

export interface GeneratorAssignmentSettings {
  mode?: 'auto' | 'specific_hosts' | 'region_weighted' | 'load_zone_weighted'
  /** Restrict root-level automatic placement to one managed fleet pool. */
  pool_id?: string
  selected_targets?: string[]
  region_weights?: Record<string, number>
  load_zone_weights?: Record<string, number>
  shard_strategy?: 'capacity_weighted'
  required_labels?: Record<string, string>
  tolerate_generator_loss?: boolean
}

export interface GeneratorShardSummary {
  group: string
  target: string
  vusers: number
  shard_strategy?: 'capacity_weighted'
}

export interface ScenarioFootprint {
  total_vusers: number
  duration: string
  complexity_score: number
  estimated_cpu_cores: number
  estimated_memory_mb: number
  estimated_network_mbps: number
}

export interface AssignmentRecommendation {
  mode: 'auto' | 'specific_hosts' | 'region_weighted' | 'load_zone_weighted'
  available_targets?: string[]
  recommended_targets?: string[]
  available_regions?: string[]
  available_labels?: Record<string, string[]>
  region_weights?: Record<string, number>
  summary?: string
  warnings?: string[]
}
