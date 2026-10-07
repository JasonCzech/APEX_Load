import type { ObjectActivitySummary } from './objectActivity'
import type { AuthoringHealth } from './authoring'
import type { ScenarioDataPoolBinding } from './dataPools'
import type { GoalConfig, ScenarioMode } from './goals'
import type { LoadStageDraft } from './loadModels'
import type { RuntimeSettingsOverride } from './runtimeSettings'
import type { ScenarioSchedule } from './scheduling'
import type { ScriptDefinition } from './scripts'
import type { GeneratorAssignmentSettings } from './testConfiguration'
import type { TrafficProfile } from './trafficProfiles'

export interface LibraryAssignment {
  application: { id: string; name: string } | null
  releases: Array<ReleaseRef & { archived_at?: string }>
  revision: number
}

export interface ReleaseRef {
  id: string
  name: string
  application_id: string
}

export interface ScenarioRef {
  id: string
  name: string
}

export interface ScriptAuthoringProjection {
  projection_version: 'apexload.script_authoring.v1'
  id: string
  project_id: string
  folder_id: string | null
  content_revision: string
  script: ScriptDefinition
  library_placement: { application_id: string | null; release_ids: string[] }
}

// The immutable server document and its actual strong ETag are one authority.
// Editable drafts must never receive a freshly fetched token on their own.
export interface ScriptAuthoringBaseline {
  projection: ScriptAuthoringProjection
  etag: string
}

// The canonical scenario authoring document: persisted authored fields only,
// with groups and data-pool bindings in the update request's field names.
export interface ScenarioAuthoringDocument {
  id: string
  ref_key?: string
  name: string
  project_id: string
  duration: string
  groups: Array<Record<string, unknown> & { script_id: string; vusers: number }>
  data_pools: Array<Record<string, unknown> & { pool_id: string; alias: string }>
  [field: string]: unknown
}

export interface ScenarioAuthoringProjection {
  projection_version: 'apexload.scenario_authoring.v1'
  id: string
  project_id: string
  content_revision: string
  scenario: ScenarioAuthoringDocument
  library_placement: { application_id: string | null; release_ids: string[] }
  monitor_selection: { sources: Array<{ source_id: string; required: boolean }> }
}

// A scenario baseline pairs the immutable server document with its strong
// ETag, exactly as for scripts.
export interface ScenarioAuthoringBaseline {
  projection: ScenarioAuthoringProjection
  etag: string
}

export interface ScriptSaveOptions {
  configureSecrets?: boolean
  retainedDraft?: ScriptDefinition
  retainedJsonText?: string
  retainedCreatedSecretNames?: string[]
}

export interface StoredScript {
  content_revision?: string
  authoring_projection?: ScriptAuthoringProjection
  archived_at?: string
  activity?: ObjectActivitySummary
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
  library_assignment?: LibraryAssignment
  associated_releases?: ReleaseRef[]
  scenario_run_releases?: ReleaseRef[]
  last_run_at?: string
  authoring_health?: AuthoringHealth
  script?: ScriptDefinition
}

export interface StoredScenario {
  content_revision?: string
  authoring_projection?: ScenarioAuthoringProjection
  archived_at?: string
  activity?: ObjectActivitySummary
  termination_mode?: 'duration' | 'iterations'
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
		transaction_response_time_sla_ms?: number
  }>
  schedules?: ScenarioSchedule[]
  data_pools?: ScenarioDataPoolBinding[]
  created_at: string
  updated_at: string
  script_count: number
  result_count?: number
  library_assignment?: LibraryAssignment
  associated_releases?: ReleaseRef[]
  last_run_test_id?: string
  last_run_at?: string
  last_run_ended_at?: string
}
