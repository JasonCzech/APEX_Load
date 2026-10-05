import {
  browserDocumentEvidenceVersion, browserVitalsAlgorithm, browserVitalsAlgorithmSHA256,
  type BrowserDocumentMetricSummary, type BrowserDocumentObservation, type BrowserDocumentSummaryMode,
  type BrowserMetricDistribution, type BrowserRuntimeCohort,
} from '../types/browserDocumentEvidence'

const maxCount = 1e12
const maxValue = 86_400_000
const cohortFields = ['version', 'browser_version', 'platform', 'viewport_width', 'viewport_height', 'headless', 'mobile', 'touch',
  'device_scale_factor', 'cpu_throttle_rate', 'network_latency_ms', 'network_bandwidth_kbps', 'shaping_applied',
  'cache_enabled', 'cache_content_urls', 'check_newer_versions', 'new_user_policy', 'new_user_policy_applied', 'unavailable_reason'] as const
const reasons = new Set(['not_exposed', 'unsupported', 'no_observation', 'no_interaction', 'hidden_before_paint', 'not_loaded',
  'frame_unavailable', 'document_unavailable', 'identity_capacity', 'source_censored', 'source_incomplete', 'measurement_capacity',
  'malformed', 'cohort_unavailable', 'shaping_unavailable', 'new_user_policy_unavailable', 'scope_unqualified'])
const metrics = new Set(['fcp', 'lcp', 'inp', 'cls', 'ttfb'])
const engines = new Set(['chromium', 'chrome', 'webkit', 'firefox'])
const platforms = new Set(['', 'linux', 'darwin', 'windows', 'freebsd', 'openbsd', 'netbsd'])
const policies = new Set(['', 'persistent_context', 'context_each_iteration', 'cookies_each_iteration', 'cookies_and_cache_each_iteration'])
const boundaries = new Set(['progress', 'first_input', 'hidden', 'pagehide', 'navigation', 'graceful_end', 'context_closed',
  'frame_detached', 'crash', 'cancelled', 'resource_limit', 'bfcache_restore', 'metric_observed'])
function record(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === 'object' && !Array.isArray(value) }
function own(value: Record<string, unknown>, key: string): unknown { return Object.prototype.hasOwnProperty.call(value, key) ? value[key] : undefined }
class EvidenceAdmissionError extends Error {}
function assert(condition: boolean, message: string): asserts condition { if (!condition) throw new EvidenceAdmissionError(message) }
function text(value: unknown, max = 128): value is string { return typeof value === 'string' && value.length <= max && new TextEncoder().encode(value).length <= max && !value.includes('\0') && !value.includes('\r') && !value.includes('\n') && !/[\uD800-\uDFFF]/u.test(value) }
function finite(value: unknown, max = maxValue): value is number { return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= max }
function count(value: unknown, max = maxCount): value is number { return finite(value, max) && Number.isSafeInteger(value) }
function optionalReason(value: unknown): boolean { return value === undefined || value === '' || typeof value === 'string' && reasons.has(value) }

function cohort(value: unknown): BrowserRuntimeCohort | null {
  if (value === undefined || value === null) return null
  assert(record(value), 'Malformed actual runtime cohort.')
  assert(value.version === 1 && text(value.browser_version) && typeof value.platform === 'string' && platforms.has(value.platform), 'Unsupported actual runtime cohort.')
  assert(count(value.viewport_width, 8192) && count(value.viewport_height, 8192) && value.viewport_width * value.viewport_height <= 16 * 1024 * 1024,
    'Malformed actual viewport.')
  assert(finite(value.device_scale_factor, 4) && finite(value.cpu_throttle_rate, 100) && count(value.network_latency_ms, 3_600_000)
    && count(value.network_bandwidth_kbps, 100_000_000), 'Malformed applied runtime measurements.')
  assert(typeof value.shaping_applied === 'boolean' && typeof value.new_user_policy_applied === 'boolean'
    && typeof value.new_user_policy === 'string' && policies.has(value.new_user_policy) && optionalReason(value.unavailable_reason), 'Malformed runtime policy availability.')
  for (const key of ['headless', 'mobile', 'touch', 'cache_enabled', 'cache_content_urls', 'check_newer_versions']) {
    assert(own(value, key) === undefined || typeof own(value, key) === 'boolean', 'Malformed observed runtime policy.')
  }
  return Object.fromEntries(cohortFields.filter(key => own(value, key) !== undefined).map(key => [key, own(value, key)])) as unknown as BrowserRuntimeCohort
}
export function browserCohortEligible(c: BrowserRuntimeCohort | null): boolean {
  return c !== null && c.browser_version !== '' && c.platform !== '' && c.viewport_width > 0 && c.viewport_height > 0
    && c.headless !== undefined && c.mobile !== undefined && c.touch !== undefined && c.device_scale_factor > 0 && c.cpu_throttle_rate >= 1
    && c.shaping_applied && c.cache_enabled !== undefined && c.cache_content_urls !== undefined && c.check_newer_versions !== undefined
    && c.new_user_policy !== '' && c.new_user_policy_applied && !c.unavailable_reason
}
function tuple(value: Record<string, unknown>): Omit<BrowserDocumentMetricSummary, 'eligible_final' | 'unavailable_final' | 'unqualified_final' | 'censored' | 'provisional' | 'excluded_reasons' | 'distribution'> {
  assert(typeof value.metric === 'string' && metrics.has(value.metric), 'Unsupported document metric.')
  assert(value.algorithm === browserVitalsAlgorithm && value.algorithm_sha256 === browserVitalsAlgorithmSHA256, 'Unsupported document measurement algorithm.')
  assert(value.unit === (value.metric === 'cls' ? 'score' : 'ms'), 'Incompatible document metric unit.')
  assert(typeof value.engine === 'string' && engines.has(value.engine) && text(value.device_profile), 'Malformed browser identity.')
  assert((value.group === undefined || text(value.group, 256)) && (value.region === undefined || text(value.region, 256)), 'Malformed workload identity.')
  assert(value.frame_scope === 'main_frame' || value.frame_scope === 'child_frame', 'Unsupported frame scope.')
  assert(value.lifecycle_phase === 'navigation' || value.lifecycle_phase === 'bfcache_restore', 'Unsupported document lifecycle.')
  return { metric: value.metric, algorithm: value.algorithm, algorithm_sha256: value.algorithm_sha256, unit: value.unit,
    engine: value.engine, group: value.group ?? '', region: value.region ?? '', device_profile: value.device_profile,
    cohort: cohort(value.cohort), frame_scope: value.frame_scope, lifecycle_phase: value.lifecycle_phase } as BrowserDocumentMetricSummary
}
function distribution(value: unknown, eligible: number): BrowserMetricDistribution {
  assert(record(value) && count(value.count) && value.count === eligible && finite(value.sum, maxValue * maxCount)
    && finite(value.min) && finite(value.max) && value.max >= value.min, 'Malformed qualified final distribution.')
  assert(Array.isArray(value.buckets) && value.buckets.length === 192, 'Unsupported document distribution buckets.')
  let total = 0
  for (const [i, bucket] of value.buckets.entries()) {
    assert(count(bucket) && bucket <= value.count - total, 'Malformed document distribution counts.')
    total += bucket
    if (bucket > 0) {
      const lower = i === 0 ? 0 : 0.000001 * Math.pow(1.2, i - 1)
      const upper = i === 191 ? Infinity : 0.000001 * Math.pow(1.2, i)
      const rounding = Math.max(1e-15, (Number.isFinite(upper) ? Math.max(lower, upper) : lower) * 1e-12)
      assert(value.max >= lower - rounding && value.min <= upper + rounding, 'Distribution buckets contradict observed values.')
    }
  }
  assert(total === value.count && (value.count !== 0 || value.sum === 0 && value.min === 0 && value.max === 0), 'Inconsistent document distribution population.')
  const tolerance = Math.max(1e-9, value.max * value.count * Math.min(1e-5, Math.max(64 * Number.EPSILON, value.count * 4 * Number.EPSILON)))
  assert(value.count === 0 || value.sum >= value.min * value.count - tolerance && value.sum <= value.max * value.count + tolerance,
    'Inconsistent observed document distribution values.')
  return { count: value.count, sum: value.sum, min: value.min, max: value.max, buckets: [...value.buckets] as number[] }
}
export interface BrowserDocumentMetricRow {
  key: string
  summary: BrowserDocumentMetricSummary | null
  issue: string | null
}
function metricRow(input: unknown, index: number): BrowserDocumentMetricRow {
  try {
    assert(record(input), 'Malformed document metric tuple.')
    const identity = tuple(input)
    for (const key of ['eligible_final', 'unavailable_final', 'unqualified_final', 'censored', 'provisional']) assert(count(input[key]), 'Missing or invalid document population count.')
    assert(input.eligible_final === 0 || browserCohortEligible(identity.cohort ?? null), 'Qualified final population lacks an eligible actual cohort.')
    const d = distribution(input.distribution, input.eligible_final as number)
    let excluded: Record<string, number> | undefined
    if (input.excluded_reasons !== undefined) {
      assert(record(input.excluded_reasons) && Object.keys(input.excluded_reasons).length <= 16, 'Malformed exclusion reasons.')
      let total = 0
      const pairs: [string, number][] = []
      for (const [reason, n] of Object.entries(input.excluded_reasons)) {
        assert(reasons.has(reason) && count(n) && n <= maxCount - total, 'Unsupported or invalid exclusion reason.')
        total += n
        pairs.push([reason, n])
      }
      assert(total <= (input.unavailable_final as number) + (input.unqualified_final as number) + (input.censored as number), 'Exclusion reasons exceed excluded observations.')
      excluded = Object.fromEntries(pairs.sort(([a], [b]) => a.localeCompare(b)))
    }
    const summary = { ...identity, eligible_final: input.eligible_final, unavailable_final: input.unavailable_final, unqualified_final: input.unqualified_final,
      censored: input.censored, provisional: input.provisional, excluded_reasons: excluded, distribution: d } as BrowserDocumentMetricSummary
    // Identity is independent of labels and input property order. Cohort fields
    // are projected above in their fixed contract order; unknowns never merge.
    const key = JSON.stringify([identity.metric, identity.algorithm, identity.algorithm_sha256, identity.unit, identity.engine,
      identity.group, identity.region, identity.device_profile, identity.frame_scope, identity.lifecycle_phase, identity.cohort])
    return { key, summary, issue: null }
  } catch (error) { return { key: `unavailable-${index}`, summary: null, issue: error instanceof EvidenceAdmissionError ? error.message : 'Malformed document metric tuple.' } }
}
function utc(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) return false
  const ms = Date.parse(value)
  return Number.isFinite(ms) && new Date(ms).toISOString().slice(0, 19) === value.slice(0, 19)
}
function utcOrder(value: string): string { return `${value.slice(0, 19)}.${(value.match(/\.(\d+)Z$/)?.[1] ?? '').padEnd(9, '0')}Z` }
export interface BrowserDocumentObservationRow { key: string; observation: BrowserDocumentObservation | null; issue: string | null }
function observationRow(input: unknown, index: number): BrowserDocumentObservationRow {
  try {
    assert(record(input) && input.version === browserDocumentEvidenceVersion, 'Unsupported document observation version.')
    const identity = tuple(input)
    assert(typeof input.source_generation === 'string' && /^[a-f0-9]{32}$/.test(input.source_generation), 'Malformed observation source generation.')
    const generation = input.source_generation
    for (const key of ['document_id', 'lifecycle_id', 'frame_id', 'navigation_id']) {
      const id = input[key]
      assert(typeof id === 'string' && id.length <= 128 && id.startsWith(`${generation}-`) && /^[a-z0-9_./-]+$/.test(id), 'Malformed opaque document identity.')
    }
    assert(input.measurement_id === `${input.document_id}/${input.lifecycle_id}/${input.metric}` && count(input.revision, Number.MAX_SAFE_INTEGER) && input.revision > 0,
      'Missing or unsafe document observation revision.')
    assert(input.state === 'provisional' || input.state === 'final' || input.state === 'censored', 'Unsupported document observation state.')
    assert(typeof input.boundary === 'string' && boundaries.has(input.boundary), 'Unsupported document lifecycle boundary.')
    assert(utc(input.window_start) && utc(input.window_end) && utc(input.captured_at) && utcOrder(input.window_end) >= utcOrder(input.window_start)
      && !/^0001-01-01T00:00:00(?:\.0+)?Z$/.test(input.window_start) && !/^0001-01-01T00:00:00(?:\.0+)?Z$/.test(input.captured_at), 'Missing or invalid source UTC window.')
    if (input.state === 'final') {
      const finalBoundaries = input.metric === 'cls' || input.metric === 'inp' ? ['pagehide', 'graceful_end']
        : input.metric === 'lcp' ? ['first_input', 'hidden', 'pagehide', 'graceful_end'] : ['metric_observed', 'pagehide', 'graceful_end']
      assert(typeof input.boundary === 'string' && finalBoundaries.includes(input.boundary), 'Metric has not reached its qualified final lifecycle boundary.')
    }
    assert(input.state !== 'censored' || ['context_closed', 'frame_detached', 'crash', 'cancelled', 'resource_limit'].includes(input.boundary as string),
      'Censored observation lacks a source termination boundary.')
    assert(typeof input.available === 'boolean' && typeof input.qualified === 'boolean' && optionalReason(input.unavailable_reason), 'Missing document measurement availability.')
    assert(input.available === (input.value !== undefined && input.value !== null), 'Document value contradicts availability.')
    assert(input.available ? finite(input.value) && !input.unavailable_reason : typeof input.unavailable_reason === 'string' && reasons.has(input.unavailable_reason),
      'Missing or invalid document measurement value.')
    assert(!input.qualified || input.available && input.state === 'final' && browserCohortEligible(identity.cohort ?? null), 'Observation cannot qualify its final population.')
    assert(input.state !== 'censored' || !input.available && !input.qualified, 'Censored document observation contains a qualified value.')
    assert(input.interaction_count === undefined || count(input.interaction_count), 'Invalid observed interaction count.')
    assert(input.metric !== 'inp' || !input.available || typeof input.interaction_count === 'number' && input.interaction_count > 0, 'INP value lacks an observed interaction.')
    const observation = { ...identity, version: browserDocumentEvidenceVersion, source_generation: generation,
      document_id: input.document_id, lifecycle_id: input.lifecycle_id, frame_id: input.frame_id, navigation_id: input.navigation_id,
      measurement_id: input.measurement_id, revision: input.revision, state: input.state, boundary: input.boundary,
      window_start: input.window_start, window_end: input.window_end, captured_at: input.captured_at,
      available: input.available, qualified: input.qualified, unavailable_reason: input.unavailable_reason,
      value: input.value, interaction_count: input.interaction_count } as BrowserDocumentObservation
    return { key: `${generation}/${observation.measurement_id}/${observation.revision}`, observation, issue: null }
  } catch (error) { return { key: `unavailable-observation-${index}`, observation: null, issue: error instanceof EvidenceAdmissionError ? error.message : 'Malformed document observation.' } }
}
export interface BrowserDocumentPanelModel {
  state: 'absent' | 'ready' | 'invalid' | 'unsupported'
  issue: string | null
  mode: BrowserDocumentSummaryMode | null
  incomplete: boolean | null
  droppedObservations: number | null
  rows: BrowserDocumentMetricRow[]
  observations: BrowserDocumentObservationRow[]
  observationIssue: string | null
  observationsTruncated: boolean | null
}
export function buildBrowserDocumentPanelModel(summary: unknown, observations: unknown, mode: unknown, truncated?: unknown): BrowserDocumentPanelModel {
  const model: BrowserDocumentPanelModel = { state: 'absent', issue: null, mode: null, incomplete: null, droppedObservations: null,
    rows: [], observations: [], observationIssue: null, observationsTruncated: typeof truncated === 'boolean' ? truncated : null }
  if (mode === 'live-history' || mode === 'archive-latest') model.mode = mode
  if (summary !== undefined && summary !== null) {
    if (!record(summary) || summary.version !== browserDocumentEvidenceVersion) { model.state = 'unsupported'; model.issue = 'This peer or archive did not supply a supported document evidence version.' }
    else if (!model.mode) { model.state = 'invalid'; model.issue = 'Document summary population semantics were not supplied.' }
    else if (summary.metrics !== null && !Array.isArray(summary.metrics) || Array.isArray(summary.metrics) && summary.metrics.length > 64 || !count(summary.dropped_observations) || typeof summary.incomplete !== 'boolean') {
      model.state = 'invalid'; model.issue = 'Document summary exceeds its bound or omits evidence completeness.'
    } else {
      model.state = 'ready'; model.incomplete = summary.incomplete; model.droppedObservations = summary.dropped_observations
      model.rows = (summary.metrics === null ? [] : summary.metrics as unknown[]).map(metricRow)
      const seen = new Set<string>()
      model.rows = model.rows.map((row, index) => {
        if (row.summary && seen.has(row.key)) return { ...row, key: `${row.key}/duplicate-${index}`, summary: null, issue: 'Duplicate document metric tuple; no distributions were combined.' }
        if (row.summary) seen.add(row.key)
        return row
      })
    }
  }
  if (observations !== undefined && observations !== null) {
    if (!Array.isArray(observations) || observations.length > 256) model.observationIssue = 'Retained document observations exceed the display bound or are malformed.'
    else model.observations = observations.map(observationRow)
  }
  return model
}
export function formatBrowserDocumentValue(value: number | null | undefined, unit: 'ms' | 'score'): string {
  return finite(value) ? `${value.toLocaleString('en-US', { maximumSignificantDigits: 6 })}${unit === 'ms' ? ' ms' : ' score'}` : 'Unavailable'
}
export function browserDocumentDistributionBins(d: BrowserMetricDistribution): { index: number; lower: number; upper: number | null; count: number }[] {
  return d.buckets.flatMap((n, i) => n > 0 ? [{ index: i, lower: i === 0 ? 0 : 0.000001 * Math.pow(1.2, i - 1),
    upper: i === 191 ? null : 0.000001 * Math.pow(1.2, i), count: n }] : [])
}
