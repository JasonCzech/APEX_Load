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
  unsupported_function_count: number
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
export type RecorderImportResult = ScriptImportResult<Record<string, unknown>, RecorderCorrelation>

export interface JMeterSupportMatrixResponse {
  support_matrix?: SupportMatrix
}

export interface LoadRunnerSupportMatrixResponse {
  support_matrix?: LoadRunnerSupportMatrixEntry[]
  notes?: string[]
  tested_examples?: LoadRunnerSupportMatrixExample[]
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
