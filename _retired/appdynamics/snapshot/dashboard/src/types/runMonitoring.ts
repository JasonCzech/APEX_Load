import type { AnalysisPoint } from './analysis'
import type { ElasticCapability, ElasticSelection, ElasticMonitoringMetadata } from './elastic'
import type { AppDynamicsSelection, AppDynamicsEntity, AppDynamicsCapability, AppDynamicsEvidence } from './appdynamics'

export interface MonitorSource {
  id: string; name: string; project_id?: string; revision: number; collector_id: string
  pack: string; destination?: string; interval_seconds: number; enabled: boolean
  credential_ref?: string; collector_public_key?: string; service?: string
  connection_id?: string; executor_mode?: 'direct' | 'collector'; appdynamics?: AppDynamicsSelection
  elastic?: ElasticSelection
}
export interface MonitorBinding { source_id: string; required: boolean }
export interface MonitorRule {
  source_id: string; metric: string; dimensions?: Record<string, string>; unit: string
  statistic: 'avg' | 'min' | 'max' | 'sum' | 'value'; comparator: 'lt' | 'lte' | 'gt' | 'gte' | 'eq' | 'neq'
  threshold: number; severity: 'fail' | 'warning'; min_coverage_pct: number
  start_offset_seconds: number; end_offset_seconds?: number
}
export interface MonitorRuleResult { rule: MonitorRule; status: string; observed?: number; coverage_pct: number; eligible_buckets: number; message: string }

export interface RunMonitorJob {
  id: string; source: MonitorSource; state: string; next_sequence: number; coverage_reason?: string
  started_at?: string; load_ended_at?: string; drain_deadline?: string; last_success_at?: string; query_watermark?: string
  coverage?: Record<string, AppDynamicsCapability | ElasticCapability>; points?: AnalysisPoint[]; topology?: AppDynamicsEntity[]
}
export interface RunMonitorCoverage {
  job_id: string; source_id: string; source_revision: number; binding_revision: number
  connection_id?: string; connection_revision?: number; executor_mode?: string
  appdynamics?: AppDynamicsSelection; required: boolean; state: string; reason?: string; batches: number
  elastic?: ElasticMonitoringMetadata
  provider_coverage?: Record<string, AppDynamicsCapability>; topology?: AppDynamicsEntity[]
}
export interface RunMonitorDiagnostic { source_id: string; diagnostic: AppDynamicsEvidence }
