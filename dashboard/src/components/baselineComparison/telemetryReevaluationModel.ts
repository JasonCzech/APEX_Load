import { request } from '../../hooks/apiClient'

// Telemetry re-evaluation (C6): a telemetry_acceptance evaluation job that
// re-classifies runs' retained interval evidence under one project
// telemetry_policy revision, as a derived result.

export interface TelemetryPolicySummary { id: string; name: string; revision: number; deleted?: boolean }
export interface TelemetryClassificationCounts { intervals: number; healthy: number; degraded: number; breached: number; unscored: number; unknown: number; breached_classified_pct?: number }
export interface TelemetryReevaluationRun {
  run_id: string; manifest_sha256: string; original_policy_hash?: string
  original: TelemetryClassificationCounts; answerable: boolean; unanswerable?: string[]
  reevaluated?: TelemetryClassificationCounts
  stage_conditions?: Record<string, number>; stage_unscored?: Record<string, number>
}
export interface TelemetryReevaluationResult {
  schema_version: string; algorithm: string; input_fingerprint: string
  policy: { id: string; name: string; revision: number; sha256: string; sustained_breach_window_ms: number; budgets: { stage: string; enabled: boolean; limit_ms: number; source: string; advisory?: boolean }[] }
  runs: TelemetryReevaluationRun[]; all_answerable: boolean; notes: string[]
}
export interface EvaluationJob {
  id: string; state: string; failure?: string; result_sha256?: string; input_fingerprint: string
  result?: TelemetryReevaluationResult
}

export const telemetryReevaluationTerminal = (state: string) => ['ready', 'failed', 'cancelled', 'expired'].includes(state)

const scope = (projectId: string) => `project_id=${encodeURIComponent(projectId)}`

export const telemetryReevaluationApi = {
  policies: (projectId: string, signal?: AbortSignal) =>
    request<TelemetryPolicySummary[]>(`/analysis/policies?kind=telemetry_policy&${scope(projectId)}`, { signal }),
  create: (projectId: string, runs: string[], policy: { id: string; revision: number }) =>
    request<EvaluationJob>(`/analysis/evaluations?${scope(projectId)}`, { method: 'POST', body: JSON.stringify({ kind: 'telemetry_acceptance', request: { runs, policy } }) }),
  get: (projectId: string, id: string, signal?: AbortSignal) =>
    request<EvaluationJob>(`/analysis/evaluations/${encodeURIComponent(id)}?${scope(projectId)}`, { signal }),
}
