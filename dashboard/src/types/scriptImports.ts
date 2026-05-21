import { type StoredScenario, type StoredScript } from '../types'
import { type ScriptDefinition } from './scripts'

export interface SupportMatrix {
  supported: string[]
  partial: string[]
  unsupported: string[]
  notes?: string[]
}

export interface ImportReviewSection {
  key: string
  title: string
  severity: 'info' | 'warning' | 'blocker' | string
  summary: string
  items: string[]
}

export interface ImportRepairSuggestion {
  id: string
  category: string
  severity: 'info' | 'warning' | 'blocker' | string
  title: string
  description: string
  action: string
  target_action?: string
  selector_stability_score?: number
  selector_signals?: string[]
}

export interface ScriptImportResult<
  TStats = Record<string, unknown>,
  TCorrelation = unknown,
  TScript extends ScriptDefinition = ScriptDefinition,
  TScenarioDraft = unknown,
  TSummary = Record<string, unknown>,
> {
  scripts: TScript[]
  script?: TScript
  scenario_draft?: TScenarioDraft
  warnings: string[]
  unsupported_elements: string[]
  assumptions: string[]
  review_sections: ImportReviewSection[]
  repair_suggestions: ImportRepairSuggestion[]
  correlations: TCorrelation[]
  stats: TStats
  summary: TSummary
  detected_protocol?: DetectedLoadRunnerProtocol
  review_artifact?: LoadRunnerReviewArtifact
}

export interface DetectedLoadRunnerProtocol {
  canonical_loadrunner_protocol: string
  apexload_protocol?: string
  bundle?: string
  import_mode: 'executable' | 'review_only' | 'external_runner_required' | 'unsupported' | string
  confidence: 'high' | 'medium' | 'low' | string
  display_name?: string
  source_type?: string
  signals?: string[]
  conflicts?: string[]
  warnings?: string[]
  required_evidence?: string[]
  runner_requirement?: string
}

export interface LoadRunnerReviewArtifact {
  id: string
  title: string
  status: string
  protocol: DetectedLoadRunnerProtocol
  summary: string
  source_files?: string[]
  preserved_functions?: string[]
  required_evidence?: string[]
  next_actions?: string[]
  metadata?: Record<string, unknown>
}

export interface LoadRunnerConvertStats {
  total_functions: number
  converted_actions: number
  extractors: number
  think_times: number
  transactions: number
  skipped: number
  unsupported_funcs?: string[]
}

export interface LoadRunnerImportSummary {
  project_file?: string
  mapped_runtime_file?: string
  project_sections?: string[]
  runtime_files?: string[]
  parameter_files?: string[]
  runtime_settings_parity?: LoadRunnerRuntimeSettingParity[]
  runtime_settings_status_counts?: Record<string, number>
  migration_readiness?: LoadRunnerMigrationReadiness
  detected_protocol?: DetectedLoadRunnerProtocol
  review_artifact?: LoadRunnerReviewArtifact
  import_mode?: string
  unsupported_function_count: number
}

export interface LoadRunnerProtocolSupportEntry {
  id: string
  bundle?: string
  bundle_id?: string
  bundle_name?: string
  protocol?: string
  display_name?: string
  status?: string
  execution_posture?: string
  apexload_runtime?: string
  import_mode?: string
  runner_class?: string
  runner_requirement?: string
  recommended_workflow?: string
  notes?: string
}

export interface LoadRunnerSupportMatrixEntry {
  id: string
  area: string
  concept: string
  fields?: string[]
  status: string
  notes?: string
}

export interface LoadRunnerRuntimeSettingParity extends LoadRunnerSupportMatrixEntry {
  runtime_files?: string[]
}

export interface LoadRunnerMigrationPolicy {
  block_statuses?: string[]
}

export interface LoadRunnerMigrationReadiness {
  score_pct: number
  blocked: boolean
  summary?: string
  blocking_statuses?: string[]
  blocking_settings?: LoadRunnerRuntimeSettingParity[]
  policy?: LoadRunnerMigrationPolicy
}

export interface LoadRunnerSupportMatrixExample {
  title: string
  coverage: string
  test: string
}

export interface JMeterConvertStats {
  thread_groups: number
  samplers: number
  converted_actions: number
  headers: number
  variables: number
  csv_sources: number
  assertions: number
  timers: number
  unsupported: number
}

export interface JMeterImportSummary {
  file_name?: string
  thread_group_count: number
  sampler_count: number
  variables_discovered?: string[]
  csv_sources_discovered?: string[]
}

export interface JMeterScenarioDraftGroup {
  name: string
  script_name: string
  vusers: number
  ramp_up?: string
  initialization: string
}

export interface JMeterScenarioDraft {
  name: string
  duration: string
  scenario_mode: string
  groups: JMeterScenarioDraftGroup[]
  metadata?: Record<string, unknown>
}

export interface SampleImportStats {
  format: string
  total_requests: number
  converted_actions: number
  assertions: number
  extractors: number
  correlations_detected: number
}

export interface DevWebImportStats {
  total_steps: number
  converted_actions: number
  http_requests: number
  websocket_actions: number
  grpc_calls: number
  think_times: number
  skipped: number
  unsupported_calls?: string[]
}

export type HARImportStats = SampleImportStats

export interface OpenAPIImportStats {
  format: string
  openapi_version?: string
  total_paths: number
  total_operations: number
  converted_actions: number
  request_bodies: number
  assertions: number
  parameters_detected: number
  auth_schemes_detected?: string[]
  skipped_operations?: string[]
}

export interface PostmanImportStats {
  format: string
  collection_version?: string
  folders: number
  total_requests: number
  converted_actions: number
  variables_detected: number
  auth_schemes?: string[]
  unsupported_bodies: number
  assertions: number
  skipped_items?: string[]
}

export interface WSDLImportStats {
  format: string
  services: number
  port_types: number
  bindings: number
  operations_detected: number
  converted_actions: number
  soap_actions_detected?: string[]
}

export interface SampleCorrelation {
  name: string
  source: string
  expression: string
  sample_value: string
  found_in_step: number
  used_in_steps: number[]
}

export interface RecorderCorrelation {
  name: string
  source: string
  expression: string
  extract_type: string
  found_in_seq: number
  used_in_seqs: number[]
  sample_value: string
}

export type LoadRunnerImportResult = ScriptImportResult<LoadRunnerConvertStats, unknown, ScriptDefinition, unknown, LoadRunnerImportSummary>
export type JMeterImportResult = ScriptImportResult<JMeterConvertStats, unknown, ScriptDefinition, JMeterScenarioDraft, JMeterImportSummary>
export type SampleImportResult = ScriptImportResult<SampleImportStats, SampleCorrelation>
export type DevWebImportResult = ScriptImportResult<DevWebImportStats, unknown>
export type HARImportResult = ScriptImportResult<HARImportStats, SampleCorrelation>
export type OpenAPIImportResult = ScriptImportResult<OpenAPIImportStats, unknown>
export type PostmanImportResult = ScriptImportResult<PostmanImportStats, unknown>
export type WSDLImportResult = ScriptImportResult<WSDLImportStats, unknown>
export type RecorderImportResult = ScriptImportResult<Record<string, unknown>, RecorderCorrelation>

export interface JMeterSupportMatrixResponse {
  support_matrix?: SupportMatrix
}

export interface LoadRunnerSupportMatrixResponse {
  support_matrix?: LoadRunnerSupportMatrixEntry[]
  notes?: string[]
  tested_examples?: LoadRunnerSupportMatrixExample[]
  protocol_support_matrix?: LoadRunnerProtocolSupportEntry[]
}

export interface SaveJMeterImportRequest {
  scripts: ScriptDefinition[]
  scenario_draft?: JMeterScenarioDraft
}

export interface SaveJMeterImportResponse {
  scripts?: StoredScript[]
  count?: number
  scenario?: StoredScenario
}
