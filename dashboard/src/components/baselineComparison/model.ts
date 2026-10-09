import { apiFetch, request } from '../../hooks/apiClient'
import type { MessagingSummary, TransactionResponseTimeSLAResult } from '../../types'

export interface ComparisonPolicy {
  revision: number
  load_tolerance_pct: number
  latency_pct: number
  latency_ms: number
  throughput_pct: number
  error_points: number
  resource_pct: number
  group_mappings?: Record<string, string>
  resource_mappings?: ResourceMapping[]
}
export interface ResourceMapping {
  run_id: string; keys: string[]; identity: string; unit: string
  kind: 'gauge' | 'counter'; aggregation: 'sum' | 'mean' | 'max'
  group?: string; absolute_threshold?: number; confirmed: boolean
}
export interface RunIdentity {
  id: string; name: string; release_id: string; release_name: string; scenario_id: string
  status: string; vusers: number; peak_vusers: number; completed_at?: string; pinned: boolean; archive_available: boolean
}
export interface MeasurementWindow { start_seconds: number; end_seconds: number }
export interface LoadWindow extends MeasurementWindow { load: number; total_load: number; group: string; direction: string; mix: Record<string, number> }
export interface Measurement {
  mean_statistic?: 'evaluation_grid_mean'
  available_statistics?: string[]
  messaging?: MessagingSummary
  samples: number; successes: number; errors: number; mean: number; min: number; max: number
  p50: number; p90: number; p95: number; p99: number; throughput: number; successful_throughput: number
  error_pct: number; coverage_pct: number; instances: number
  points?: { seconds: number; value: number; load: number }[]
  sla?: TransactionResponseTimeSLAResult[]
}
export interface ComparisonCell { measurement?: Measurement; window?: LoadWindow; qualification: string[]; approximate: boolean; delta?: number; delta_pct?: number }
export interface ComparisonRow {
  provider?: { name: 'prometheus'; backend_profile: string; query_id: string; definition_hash: string; statistic: string; evaluation_step_seconds: number; dimensions: Record<string, string>; entity_ids: Record<string, string>; display_labels?: Record<string, string> }
  id: string; name: string; group: string; protocol?: string; source: string; unit: string
  current?: Measurement; window?: LoadWindow; baselines: Record<string, ComparisonCell>
}
export interface ComparisonRequest { regression_identities?: string[]; regression_metrics?: string[]; regression_protocols?: string[]; regression_groups?: string[]; minimum_load?: number; maximum_load?: number; baseline_ids: string[]; policy?: ComparisonPolicy; windows?: Record<string, MeasurementWindow>; population: 'transaction' | 'action' | 'unknown'; allow_other_scenarios: boolean }
export interface TelemetryEvidence {
 metric_intervals: number; dropped_results?: number; max_sample_age_ms?: number
 run_id: string; retained_intervals: number; health_intervals: number; health_coverage_pct?: number; breached_interval_pct?: number
 summary?: { status: string; dropped_results?: number; max_sample_age_ms?: number }
 warnings: string[]
}
// One final-document metric tuple compared with one baseline: values only
// when algorithm, unit, lifecycle phase, frame scope, engine, device profile
// and runtime cohort all match, otherwise the first difference.
export interface BrowserDocumentComparison {
 baseline_id: string; metric: string; unit: string; group?: string; region?: string
 frame_scope: string; lifecycle_phase: string; engine: string; device_profile: string; cohort?: string
 comparable: boolean; reason?: string; current_final: number; baseline_final: number
 current_p75?: number; baseline_p75?: number; delta_p75?: number
}
// What decided how a run's telemetry was classified (C6).
export interface TelemetryRunProfile {
 run_id: string; policy_availability: 'available' | 'unavailable' | string; policy_hash?: string
 budgets?: { stage: string; enabled: boolean; limit_ms: number }[]; sustained_breach_window_ms?: number
 cadence_ms?: number; cadence_scope?: string
 policy_pin?: { kind: string; id: string; name: string; revision: number; sha256: string }
 source_capabilities: string[]; workload_version?: number
 // The run's own telemetry verdict under its pinned acceptance policy (C7)
 // and its loss totals; verdicts are shown side by side, never compared.
 acceptance?: import('../../types').TelemetryAcceptanceVerdict
 loss?: { dropped_results?: { state: string; value: number; reason?: string } }
}
// Whether one baseline's whole-run telemetry health compares with the
// current run's, and the deltas (current minus baseline) when it does.
export interface TelemetryComparison {
 baseline_run_id: string; scope: 'whole_run' | string; comparable: boolean; reasons: string[]; notes?: string[]
 current: TelemetryRunProfile; baseline: TelemetryRunProfile
 breached_interval_pct_delta?: number; health_coverage_pct_delta?: number; breached_known_fraction_delta?: number
}
export interface ComparisonSnapshot {
 telemetry?: TelemetryEvidence[]
 telemetry_comparisons?: TelemetryComparison[]
 browser_documents?: BrowserDocumentComparison[]
 browser_soft_navigation?: 'unsupported'
  verdict?: { status: 'passed' | 'regressed' | 'not_evaluated'; evaluated: number; unavailable: number; metrics: string[]; evidence: { row_id: string; baseline_id: string; metric: string; status: string; reason?: string }[] }
  id: string; project_id: string; current: RunIdentity; baselines: RunIdentity[]; created_at: string; rule_version: string
  request: ComparisonRequest; policy: ComparisonPolicy; transactions: ComparisonRow[]; resources: ComparisonRow[]
  setup: { baseline_id: string; field: string; current: string; baseline: string; status: string }[]
  findings: { baseline_id: string; row_id: string; category: string; metric: string; message: string; status: string }[]
  warnings: string[]
}
export const defaultPolicy: ComparisonPolicy = { revision: 0, load_tolerance_pct: 5, latency_pct: 10, latency_ms: 10, throughput_pct: 10, error_points: 1, resource_pct: 20 }
const runPath = (id: string) => `/tests/${encodeURIComponent(id)}`
/** A saved comparison for a run, listed so the report can reopen it. */
export interface SavedComparisonSummary {
  id: string
  created_at: string
  baselines: RunIdentity[]
  verdict?: string
}
export function comparisonVerdictLabel(status?: string): string {
  if (!status) return 'Not evaluated'
  const text = status.replace(/_/g, ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}
export const comparisonApi = {
  candidates: (id: string, other: boolean, q: string, offset: number, signal?: AbortSignal) => request<{ runs: RunIdentity[]; total: number; suggested_baseline?: RunIdentity | null }>(`${runPath(id)}/baseline-candidates?${new URLSearchParams({ other_scenarios: String(other), q, offset: String(offset), limit: '50' })}`, { signal }),
  evaluate: (id: string, body: ComparisonRequest, signal?: AbortSignal) => request<ComparisonSnapshot>(`${runPath(id)}/baseline-comparisons`, { method: 'POST', body: JSON.stringify(body), signal, timeoutMs: 120000 }),
  saved: (id: string, signal?: AbortSignal) => request<{ comparisons: SavedComparisonSummary[] }>(`${runPath(id)}/baseline-comparisons`, { signal }),
  snapshot: (id: string, comparison: string, signal?: AbortSignal) => request<ComparisonSnapshot>(`${runPath(id)}/baseline-comparisons/${encodeURIComponent(comparison)}`, { signal }),
  policy: (scenario: string, signal?: AbortSignal) => request<ComparisonPolicy>(`/scenarios/${encodeURIComponent(scenario)}/comparison-policy`, { signal }),
  savePolicy: (scenario: string, policy: ComparisonPolicy) => request<ComparisonPolicy>(`/scenarios/${encodeURIComponent(scenario)}/comparison-policy`, { method: 'PUT', body: JSON.stringify(policy) }),
  download: async (id: string, comparison: string, format: string, profile = 'engineer') => {
    const params = new URLSearchParams({ comparison_id: comparison, profile })
    let path: string
    if (format === 'json' || format === 'evidence_csv') path = `${runPath(id)}/baseline-comparisons/${encodeURIComponent(comparison)}?format=${format === 'json' ? 'json' : 'csv'}`
    else if (format === 'html') path = `${runPath(id)}/report?${params}`
    else { params.set('format', format); path = `${runPath(id)}/archive/report?${params}` }
    const response = await apiFetch(path)
    if (!response.ok) throw new Error((await response.json()).error || 'Export failed')
    return response.blob()
  },
}
export function number(value?: number): string { return value === undefined || !Number.isFinite(value) ? 'Unavailable' : value.toLocaleString(undefined, { maximumFractionDigits: 2 }) }
export function difference(current?: number, baseline?: number): string {
  if (current === undefined || baseline === undefined) return 'Unavailable'
  const delta = current - baseline
  return `${delta > 0 ? '+' : ''}${number(delta)}${baseline === 0 ? ' · baseline zero' : ` (${delta > 0 ? '+' : ''}${number(delta / Math.abs(baseline) * 100)}%)`}`
}
export const metricOptions = [ ['p95', 'Successful P95'], ['mean', 'Successful mean'], ['p50', 'Successful P50'], ['p90', 'Successful P90'], ['p99', 'Successful P99'], ['min', 'Successful minimum'], ['max', 'Successful maximum'], ['throughput', 'Attempts/s'], ['successful_throughput', 'Successful TPS'], ['error_pct', 'Error rate'], ['samples', 'Sample count'] ] as const
export type Metric = typeof metricOptions[number][0]

export const comparisonColors = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5, #ab69c6)', 'var(--chart-6, #bc7731)']

export function measurementValue(measurement: Measurement | undefined, key: keyof Measurement, resource = false): number | undefined {
  if (!measurement || (!resource && measurement.successes === 0 && ['mean', 'min', 'max', 'p50', 'p90', 'p95', 'p99'].includes(key))) return undefined
  if (measurement.available_statistics && !measurement.available_statistics.includes(key)) return undefined
  const value = measurement[key]
  return typeof value === 'number' ? value : undefined
}

export function resourceMeanLabel(row: ComparisonRow): string {
  return row.provider?.name === 'prometheus' ? row.provider.statistic === 'quantile' ? 'Mean of grid quantile estimates' : 'Mean of evaluation grid' : 'Time-weighted mean'
}

export function resourceProviderLabel(row: ComparisonRow): string {
  const provider = row.provider
  if (!provider) return ''
  const labels = Object.entries(provider.display_labels || provider.dimensions).map(([key, value]) => `${key}: ${value}`).join(', ')
  return `Prometheus · ${provider.query_id} · ${provider.evaluation_step_seconds}s grid${labels ? ` · ${labels}` : ''}`
}

// JSON objects returned by Go may have different key ordering from browser drafts.
// Compare semantic settings, preserving array order but sorting object keys.
export function comparisonRequestSignature(request: ComparisonRequest): string {
  const canonical = (value: unknown): unknown => {
    if (Array.isArray(value)) return value.map(canonical)
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).filter(([, child]) => child !== undefined).sort(([a], [b]) => a.localeCompare(b)).map(([key, child]) => [key, canonical(child)]))
    return value
  }
  const policy = request.policy && {
    ...request.policy,
    group_mappings: Object.keys(request.policy.group_mappings || {}).length ? request.policy.group_mappings : undefined,
    resource_mappings: request.policy.resource_mappings?.length ? request.policy.resource_mappings.map(mapping => ({ ...mapping, group: mapping.group || undefined, absolute_threshold: mapping.absolute_threshold || undefined })) : undefined,
  }
  return JSON.stringify(canonical([request.baseline_ids, policy, request.windows || {}, request.population, request.allow_other_scenarios, request.minimum_load || 0, request.maximum_load || 0, request.regression_metrics || [], request.regression_protocols || [], request.regression_groups || [], request.regression_identities || []]))
}

// Missing successful samples and incompatible resource units are not numeric changes.
export function comparisonMagnitude(row: ComparisonRow, metric: Metric, resource = false): number {
  const current = measurementValue(row.current, metric, resource)
  if (current === undefined || !Number.isFinite(current)) return 0
  return Math.max(0, ...Object.values(row.baselines).map(cell => {
    const baseline = measurementValue(cell.measurement, metric, resource)
    if (baseline === undefined || !Number.isFinite(baseline) || (resource && cell.delta === undefined)) return 0
    return Math.abs(current - baseline) / (Math.abs(baseline) || 1)
  }))
}
