export interface AnalysisCapabilities { diagnostics_enabled?: boolean; native_parquet: boolean; enabled: boolean; chunk_bytes: number; monitoring_enabled: boolean; preparation_enabled: boolean }
export interface AnalysisColumn { path: string; path_components: string[]; physical_type: string; repeated: boolean }
export interface AnalysisPreview { schema: { rows: number; columns: AnalysisColumn[] }; samples: Record<string, unknown>[] }
export interface AnalysisMeasure { value: string[]; name_column?: string[]; name: string; kind: string; unit: string }
export interface AnalysisMapping { version: number; timestamp: string[]; timestamp_encoding: string; timezone: string; measures: AnalysisMeasure[] }
export interface AnalysisImport {
  id: string; project_id: string; name: string; revision: number; state: string
  files: { id: string; relative_path: string; size: number; uploaded: number }[]
  rejections?: { file: number; measure: number; count: number; rows: number[] }[]; rejection_artifacts?: string[];
  mapping?: AnalysisMapping; accepted: number; rejected: number; error?: string; run_id?: string
}
export interface AnalysisPoint { kind?: string; host?: string; dimensions?: Record<string, string>; timestamp_utc: string; metric_name: string; value?: number; unit: string; quality: string }
export interface Preparation { allocation?: import('./testConfiguration').GeneratorShardSummary[]; capacity?: PreparationCapacityPolicy; id: string; revision: number; state: string; execution_revision: string; expires_at: string; failure?: string }

export interface PreparationCapacityPolicy { selection?: 'existing' | 'ephemeral'; spot?: 'off' | 'preferred' | 'required'; on_demand_floor?: number; spot_ceiling_percent?: number }
export interface StatisticalFinding { metric_name: string; current: number; baseline_mean: number; baseline_stddev: number; baseline_buckets: number; z_score: number; threshold: number; timestamp: string; baseline_start: string; baseline_end: string; advisory: boolean }
