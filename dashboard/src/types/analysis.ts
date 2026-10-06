export interface AnalysisCapabilities { imported_identity_version?: number; diagnostics_enabled?: boolean; native_parquet: boolean; enabled: boolean; chunk_bytes: number; monitoring_enabled: boolean; preparation_enabled: boolean; summary_statistics?: string[]; histogram_bound_limit?: number }
export interface AnalysisColumn { path: string; path_components: string[]; physical_type: string; repeated: boolean }
export interface AnalysisPreview { schema: { rows: number; columns: AnalysisColumn[] }; samples: Record<string, unknown>[] }
export interface AnalysisHistogram { bounds: string[]; bucket_counts: string; temporality: string; window_seconds?: number; bound_column?: string[]; count_columns?: string[][] }
export interface AnalysisMeasure { value: string[]; name_column?: string[]; name: string; kind: string; unit: string; statistic?: string; window_seconds?: number; sample_count_column?: string[]; sample_sum_column?: string[]; histogram?: AnalysisHistogram }
export interface AnalysisDimensionMapping { name: string; value: string[] }
export interface AnalysisIdentityMapping { source?: string[]; host?: string[]; service?: string[]; dimensions?: AnalysisDimensionMapping[] }
export interface AnalysisMapping { version: number; timestamp: string[]; timestamp_encoding: string; timezone: string; measures: AnalysisMeasure[]; identity?: AnalysisIdentityMapping }
export interface AnalysisIdentityFilterRow { name: string; value: string }
export interface AnalysisIdentityFilters { source_id: string; host: string; service: string; dimensions: AnalysisIdentityFilterRow[] }
export interface AnalysisIdentitySelection { source_id?: string; host?: string; service?: string; dimensions?: Record<string, string> }
export interface AnalysisImport {
  id: string; project_id: string; name: string; revision: number; state: string
  files: { id: string; relative_path: string; size: number; uploaded: number }[]
  rejections?: { file: number; measure: number; count: number; rows: number[] }[]; rejection_artifacts?: string[];
  mapping?: AnalysisMapping; accepted: number; rejected: number; error?: string; run_id?: string
  /** Normalized partitions with their actual time ranges; their starts give each metric's source
   * window grid. */
  partition_manifest?: { partitions?: { stats?: { kind?: string; metric?: string; min_time?: string; identity?: { source: string; host: string; service: string; dimensions: Record<string, string> } } }[] }
}
export interface AnalysisProviderMeasurement {
  name: 'appdynamics' | 'dynatrace' | 'prometheus'; mode?: string; backend_profile?: string; query_id?: string; definition_hash?: string; evaluation_step_seconds?: number; analysis_statistic?: 'avg' | 'min' | 'max'; underlying_sample_time?: string; freshness_known?: boolean; metric_id: string; catalog_path?: string; entity_ids?: Record<string, string>; display_labels?: Record<string, string>
  backend?: string; synthetic?: boolean
  bucket_start: string; bucket_end: string; resolution_seconds: number; statistic: string
  count?: number; sum?: number; min?: number; max?: number; settled: boolean; aggregation_eligible: boolean
  query_window?: boolean
}
export interface AnalysisPoint { source?: string; run_id?: string; provider?: AnalysisProviderMeasurement; kind?: string; input_kind?: string; missing_reason?: string; estimate_bin?: { lower: number; upper: number }; host?: string; service?: string; dimensions?: Record<string, string>; timestamp_utc: string; metric_name: string; value?: number; unit: string; quality: string }
export interface Preparation { allocation?: import('./testConfiguration').GeneratorShardSummary[]; capacity?: PreparationCapacityPolicy; id: string; revision: number; state: string; execution_revision: string; expires_at: string; failure?: string }

export interface PreparationCapacityPolicy { selection?: 'existing' | 'ephemeral'; spot?: 'off' | 'preferred' | 'required'; on_demand_floor?: number; spot_ceiling_percent?: number }
export interface StatisticalFinding { metric_name: string; current: number; baseline_mean: number; baseline_stddev: number; baseline_buckets: number; z_score: number; threshold: number; timestamp: string; baseline_start: string; baseline_end: string; advisory: boolean }
