import type { LoadModelValidationReport } from './loadModels'
import type { ScenarioUpsertPayload } from '../components/scenarioPayloadUtils'

export type TrafficProfileValueSemantics = 'transactions_per_bucket' | 'cumulative_counter' | 'tps' | 'vusers'
export type TrafficProfileGapPolicy = 'hold' | 'zero' | 'drop'

export interface TrafficProfilePoint { timestamp: string; value: number }
export interface TrafficProfileRuntimePoint { offset_ms: number; target: number }
export interface TrafficProfileARTPoint { offset_ms: number; art_ms: number }
export interface TrafficProfileSeries { key: string; name: string; labels?: Record<string, string>; points: TrafficProfilePoint[] }
export interface TrafficProfileStats { count: number; start: string; end: string; min: number; max: number; mean: number; detected_bucket: number; gaps: number }
export interface TrafficProfileQueryRange { mode: 'absolute' | 'relative'; start?: string; end?: string; lookback?: string; step_seconds: number }

export interface TrafficProfileSourceEntry {
  kind: 'file' | 'prometheus' | 'opensearch'
  name?: string
  file?: { filename: string; sheet?: string; timestamp_column: string; timestamp_mode?: 'absolute' | 'relative'; relative_start_time?: string; value_columns: string[]; art_columns?: string[]; series_column?: string; timezone?: string }
  prometheus?: { query: string; range: TrafficProfileQueryRange; label_fields?: string[] }
  opensearch?: { index: string; timestamp_field: string; filter?: unknown; interval: string; metric: 'count' | 'sum' | 'avg'; metric_field?: string; range: TrafficProfileQueryRange }
}

export interface TrafficProfileSource extends Omit<TrafficProfileSourceEntry, 'kind'> {
  kind: TrafficProfileSourceEntry['kind'] | 'multiple'
  sources?: TrafficProfileSourceEntry[]
}

export interface TrafficProfileMapping {
  series_key: string
  series_name: string
  group_name: string
  source?: TrafficProfileSourceEntry
  source_start?: string
  source_end?: string
  source_duration?: string
  points: TrafficProfileRuntimePoint[]
  peak_target: number
  art?: {
    basis: 'source_series' | 'operator_peak'
    series_key?: string
    series_name?: string
    source_unit?: 'milliseconds' | 'seconds'
    points?: TrafficProfileARTPoint[]
    min_art_ms?: number
    average_art_ms?: number
    peak_art_ms: number
    estimated_peak_vusers: number
    fallback_to_peak?: boolean
  }
}

export interface TrafficProfile {
  version: number
  source: TrafficProfileSource
  value_semantics: TrafficProfileValueSemantics
  runtime_target: 'tps' | 'vusers'
  interpolation: 'step'
  source_duration: string
  target_duration: string
  gap_policy?: TrafficProfileGapPolicy
  window?: { mode?: 'full' | 'explicit' | 'peak'; peak_scope?: 'per_group' | 'overall'; start?: string; end?: string; start_offset?: string; end_offset?: string; duration?: string }
  amplitude?: { mode?: 'factor' | 'peak'; value?: number }
  mappings: TrafficProfileMapping[]
  sizing?: {
    complete: boolean
    profile_peak_vusers?: number
    static_peak_vusers?: number
    estimated_peak_vusers?: number
    suggested_min_vusers?: number
    suggested_max_vusers?: number
    recommended_generators?: number
  }
  warnings?: string[]
  refreshed_at: string
}

export interface TrafficProfileSourcesResponse {
  sources: Array<{ kind: string; enabled: boolean; configured: boolean; formats?: string[] }>
  limits: { upload_bytes: number; derive_bytes: number; upstream_bytes: number; points_per_series: number; points_per_profile: number }
}

export interface TrafficProfileAnalyzeResponse {
  source: TrafficProfileSourceEntry
  sheets?: Array<{ name: string; columns: string[]; samples?: string[][] }>
  series?: Array<{ series: TrafficProfileSeries; stats: TrafficProfileStats }>
  art_series?: Array<{ series: TrafficProfileSeries; stats: TrafficProfileStats }>
  warnings?: string[]
}

export interface TrafficProfileDeriveRequest {
  project_id?: string
  source: TrafficProfileSource
  series: TrafficProfileSeries[]
  art_series?: TrafficProfileSeries[]
  mappings: Array<{ series_key: string; group_name: string; source?: TrafficProfileSourceEntry; art?: { basis: 'source_series' | 'operator_peak'; series_key?: string; value?: number; unit?: 'milliseconds' | 'seconds' } }>
  value_semantics: TrafficProfileValueSemantics
  gap_policy: TrafficProfileGapPolicy
  window: { mode: 'full' | 'explicit' | 'peak'; peak_scope?: 'per_group' | 'overall'; start?: string; end?: string; start_offset?: string; end_offset?: string; duration?: string }
  target_duration?: string
  amplitude: { mode: 'factor' | 'peak'; value: number }
  scenario_draft: ScenarioUpsertPayload
}

export interface TrafficProfileDeriveResponse {
  traffic_profile: TrafficProfile
  preview: TrafficProfileMapping[]
  warnings?: string[]
  scenario_draft: ScenarioUpsertPayload
  load_model_validation?: LoadModelValidationReport
}
