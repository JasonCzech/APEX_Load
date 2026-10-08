import { request } from '../../hooks/apiClient'

// Browser intelligence (E1): server-side findings (browser_findings.v1) and
// the qualified score (browser_score.v1) of a run, from a browser_intelligence
// evaluation job. Runs without a server result keep the legacy summary card.

export interface BrowserFindingCohort { engine: string; device_profile: string; frame_scope: string; lifecycle_phase: string }
export interface BrowserFindingQuality {
  eligible_final: number; unqualified_final: number; unavailable_final: number; censored: number; provisional: number
  excluded_reasons?: Record<string, number>; incomplete?: boolean
}
export interface BrowserFindingDocument { document_id: string; source_generation: string; value: number; resources: string }
export interface BrowserFinding {
  id: string; key: string; rule_set: string; rule_id: string; rule_version: number
  category: 'application_outcome' | 'collection_health' | string
  metric?: string; algorithm?: string; unit?: string; cohort?: BrowserFindingCohort
  window: { start: string; end: string }; applicable: boolean
  statistic?: string; observed?: number; good?: number; poor?: number; severity?: string
  quality: BrowserFindingQuality; baseline: { mode: string; status: string }
  evidence: { documents?: BrowserFindingDocument[] }
  state: 'active' | 'suppressed' | 'unscored' | string; reason?: string; summary: string
}
export interface BrowserScoreComponent {
  metric: string; weight: number; required: boolean; good: number; poor: number
  observed?: number; samples: number; score?: number; contribution?: number; reason?: string
}
export interface BrowserScore {
  version: string; score?: number; severity: string; unscored_reason?: string; missing?: string[]
  components: BrowserScoreComponent[]; coverage: { observed: number; required: number }
}
export interface BrowserIntelligenceResult {
  schema_version: string; rule_set: string; input_fingerprint: string; run_id: string
  policy: { id: string; name: string; revision: number; sha256: string; source: string }
  window: { start: string; end: string }; score: BrowserScore; findings: BrowserFinding[]
  counts: Record<string, number>; notes: string[]
}
export interface RunBrowserFindings {
  run_id: string; status: 'ready' | 'pending' | 'not_evaluated' | string
  job_id?: string; completed_at?: string; pending?: boolean; result?: BrowserIntelligenceResult
}

export const browserFindingsApi = {
  get: (runId: string, signal?: AbortSignal) =>
    request<RunBrowserFindings>(`/tests/${encodeURIComponent(runId)}/archive/browser-findings`, { signal }),
  evaluate: (projectId: string, runId: string) =>
    request<{ id: string; state: string }>(`/analysis/evaluations?project_id=${encodeURIComponent(projectId)}`, { method: 'POST', body: JSON.stringify({ kind: 'browser_intelligence', request: { run: runId } }) }),
}

const unscoredReasons: Record<string, string> = {
  missing_required_metric: 'No overall score: a required metric has no qualified final observation.',
  no_document_evidence: 'No overall score: the run retained no document evidence.',
  no_required_metric: 'No overall score: the policy weighs no metric.',
}

export function browserScoreUnscoredText(reason?: string): string {
  return unscoredReasons[reason || ''] || 'No overall score.'
}

const findingReasons: Record<string, string> = {
  engine_not_applicable: 'outside the policy’s engines',
  device_profile_not_applicable: 'outside the policy’s device profiles',
  metric_not_observed: 'metric not observed',
  no_qualified_final_observations: 'no qualified final observation',
  distribution_invalid: 'distribution could not be summarized',
}

export function browserFindingReasonText(reason?: string): string {
  return findingReasons[reason || ''] || reason || ''
}

export function formatBrowserMetric(metric: string | undefined, value: number | undefined): string {
  if (value === undefined || value === null || !Number.isFinite(value)) return '—'
  return metric === 'cls' ? value.toFixed(3) : `${Math.round(value)} ms`
}

export function browserCohortLabel(cohort?: BrowserFindingCohort): string {
  return cohort ? `${cohort.engine} · ${cohort.device_profile}` : 'Run'
}
