import { request } from '../../hooks/apiClient'

// Browser intelligence (E1/BI-A3): server-side findings (browser_findings.v2) and
// the qualified score (browser_score.v1) of a run, from a browser_intelligence
// evaluation job. Runs without a server result keep the legacy summary card.

export interface BrowserFindingCohort { engine: string; device_profile: string; frame_scope: string; lifecycle_phase: string }
export interface BrowserFindingQuality {
  eligible_final: number; unqualified_final: number; unavailable_final: number; censored: number; provisional: number
  excluded_reasons?: Record<string, number>; incomplete?: boolean
}
export interface BrowserFindingDocument { document_id: string; source_generation: string; value: number; resources: string }
export interface RunPolicyPin {
  kind: string; id: string; name?: string; revision: number; sha256: string; source?: string
}
export interface BrowserCandidateRejection {
  test_id: string; digest?: string; reasons: string[]
}
export interface BrowserFindingBaseline {
  mode: string
  status: 'regressed' | 'within_baseline' | 'insufficient_evidence' | 'unqualified' | 'not_selected' | 'not_evaluated' | 'not_evaluated_live' | 'not_applicable' | string
  reason?: string
  detail?: string
  analysis_id?: string
  baseline_run_id?: string
  baseline_archive_sha256?: string
  selection_receipt_sha256?: string
  selection_status?: string
  rejected_candidates?: BrowserCandidateRejection[]
  policy_pins?: RunPolicyPin[]
}
export interface BrowserFinding {
  id: string; key: string; rule_set: string; rule_id: string; rule_version: number
  category: 'application_outcome' | 'collection_health' | string
  metric?: string; algorithm?: string; unit?: string; cohort?: BrowserFindingCohort
  window: { start: string; end: string }; applicable: boolean
  statistic?: string; observed?: number; good?: number; poor?: number; severity?: string
  quality: BrowserFindingQuality; baseline: BrowserFindingBaseline
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
  provisional?: boolean; live_truncated?: boolean
  policy: { id: string; name: string; revision: number; sha256: string; source: string }
  policy_pins?: RunPolicyPin[]
  window: { start: string; end: string }; score: BrowserScore; findings: BrowserFinding[]
  counts: Record<string, number>; notes: string[]
}
export interface RunBrowserFindings {
  run_id: string; status: 'ready' | 'pending' | 'failed' | 'not_evaluated' | string
  provisional?: boolean; live_truncated?: boolean
  job_id?: string; completed_at?: string; pending?: boolean; failure?: string; result?: BrowserIntelligenceResult
}

/** How often a pending evaluation is checked again. */
export const BROWSER_FINDINGS_POLL_MS = 3000

export const browserFindingsApi = {
  get: (runId: string, signalOrOptions?: AbortSignal | { signal?: AbortSignal; projectId?: string }, options?: { projectId?: string }) => {
    const signal = signalOrOptions instanceof AbortSignal ? signalOrOptions : signalOrOptions?.signal
    const projectId = (signalOrOptions instanceof AbortSignal ? options?.projectId : signalOrOptions?.projectId)?.trim()
    return request<RunBrowserFindings>(`/tests/${encodeURIComponent(runId)}/archive/browser-findings`, {
      signal,
      ...(projectId ? { projectId } as Record<string, unknown> : {}),
    })
  },
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

export function browserFindingBaselineLabel(baseline?: BrowserFindingBaseline): string {
  if (!baseline || baseline.status === 'not_applicable') return ''
  if (baseline.status === 'not_evaluated_live') {
    return `Baseline (${baseline.mode || 'none'}): not evaluated live (provisional).`
  }
  const status = (baseline.status || 'not_selected').replace(/_/g, ' ')
  const reason = baseline.reason ? ` (${baseline.reason.replace(/_/g, ' ')})` : ''
  const detail = baseline.detail ? ` — ${baseline.detail}` : ''
  return `Baseline (${baseline.mode || 'none'}): ${status}${reason}${detail}.`
}
