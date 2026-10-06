// Versioned document-lifecycle evidence is independent of legacy action Samples.
export const browserDocumentEvidenceVersion = 2
export const browserVitalsAlgorithm = 'web-vitals/5.1.0'
export const browserVitalsAlgorithmSHA256 = '8890ec954757ea8e4801f49958afb76e2b8837c47fec5b16598fc20cbd49c220'
export type BrowserDocumentMetric = 'fcp' | 'lcp' | 'inp' | 'cls' | 'ttfb'
export type BrowserDocumentSummaryMode = 'live-history' | 'archive-latest'
export interface BrowserRuntimeCohort {
  version: 1
  browser_version: string
  platform: '' | 'linux' | 'darwin' | 'windows' | 'freebsd' | 'openbsd' | 'netbsd'
  viewport_width: number
  viewport_height: number
  headless?: boolean
  mobile?: boolean
  touch?: boolean
  device_scale_factor: number
  cpu_throttle_rate: number
  network_latency_ms: number
  network_bandwidth_kbps: number
  shaping_applied: boolean
  cache_enabled?: boolean
  cache_content_urls?: boolean
  check_newer_versions?: boolean
  new_user_policy: '' | 'persistent_context' | 'context_each_iteration' | 'cookies_each_iteration' | 'cookies_and_cache_each_iteration'
  new_user_policy_applied: boolean
  unavailable_reason?: string
}
export interface BrowserMetricDistribution {
  count: number
  sum: number
  min: number
  max: number
  buckets: number[]
}
export interface BrowserDocumentMetricSummary {
  metric: BrowserDocumentMetric
  algorithm: string
  algorithm_sha256: string
  unit: 'ms' | 'score'
  engine: 'chromium' | 'chrome' | 'webkit' | 'firefox'
  group?: string
  region?: string
  device_profile: string
  cohort?: BrowserRuntimeCohort | null
  frame_scope: 'main_frame' | 'child_frame'
  lifecycle_phase: 'navigation' | 'bfcache_restore'
  eligible_final: number
  unavailable_final: number
  unqualified_final: number
  censored: number
  provisional: number
  excluded_reasons?: Record<string, number>
  distribution: BrowserMetricDistribution
}
export interface BrowserDocumentSummary {
  version: 2
  metrics: BrowserDocumentMetricSummary[] | null
  dropped_observations: number
  incomplete: boolean
}
export interface BrowserDocumentObservation extends Omit<BrowserDocumentMetricSummary,
  'eligible_final' | 'unavailable_final' | 'unqualified_final' | 'censored' | 'provisional' | 'excluded_reasons' | 'distribution'> {
  version: 2
  source_generation: string
  document_id: string
  lifecycle_id: string
  frame_id: string
  navigation_id: string
  measurement_id: string
  revision: number
  state: 'provisional' | 'final' | 'censored'
  boundary: string
  window_start: string
  window_end: string
  captured_at: string
  available: boolean
  qualified: boolean
  unavailable_reason?: string
  value?: number | null
  interaction_count?: number
}
