import type { ScenarioDataPoolBinding } from './dataPools'
import type { GoalConfig, ScenarioMode } from './goals'
import type { LoadStageDraft } from './loadModels'
import type { RuntimeSettingsOverride, ScenarioRuntimeSettingsDraft } from './runtimeSettings'
import type { GeneratorAssignmentSettings } from './testConfiguration'
import type { JointEvidenceProjection } from './jointEvidence'
import type { TrafficProfile } from './trafficProfiles'

export type ScenarioWizardStep = 'scripts' | 'source' | 'run' | 'data' | 'review'
export type ScenarioSourceMode = 'shape' | 'existing' | 'traffic_profile' | 'csv_driven'
export type ScenarioStartChoice = ScenarioSourceMode | 'goal' | ''

export interface ScenarioFormState {
  termination_mode?: 'duration' | 'iterations'
  name: string
  duration: string
  /** Preserved even though start delay is not currently exposed by this editor. */
  start_delay: string
  groups: ScenarioFormGroup[]
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  goal_config?: GoalConfig
  traffic_profile?: TrafficProfile
  /** The joint evidence every run of the scenario captures; absent is none. */
  joint_evidence?: JointEvidenceProjection
  /**
   * The scenario-wide runtime defaults, as stored. Every editor writes this
   * canonical override directly (AUTH-3): there are no flat display mirrors.
   */
  runtime_settings?: RuntimeSettingsOverride
  assignment?: GeneratorAssignmentSettings
  /** Canonical data-pool bindings shared by editing, validation and submission. */
  data_pools?: ScenarioDataPoolBinding[]
}

export interface ScenarioFormGroup {
  name: string
  script_id: string
  role?: 'load' | 'observer'
  vusers: number
  ramp_up: string
	ramp_down: string
	start_offset?: string
  stages: LoadStageDraft[]
  iterations?: number
  initialization: 'at_runtime' | 'at_once'
  /** Per-group runtime overrides layered over the scenario-wide defaults. */
  runtimeSettings?: ScenarioRuntimeSettingsDraft
  assignment?: GeneratorAssignmentSettings
	transactionResponseTimeSlaMs?: number
}
