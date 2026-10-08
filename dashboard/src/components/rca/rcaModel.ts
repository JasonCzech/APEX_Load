import type { MetricsHistoryPoint, RunArchiveActionStats, RunArchiveErrorPattern, RunArchiveReport } from '../../types'
import type {
  RcaAnomaly,
  RcaAssessment,
  RcaConfidence,
  RcaHypothesis,
  RcaLoadLevel,
  RcaOutcome,
  RcaValidityVerdict,
  RcaValidityWindow,
  ReportRootCause,
} from '../../types/rca'

const list = <T,>(value: T[] | null | undefined): T[] => (Array.isArray(value) ? value : [])

// normalizeRcaAssessment makes every list in an assessment present: the
// controller sends null for an empty Go slice.
export function normalizeRcaAssessment(a: RcaAssessment): RcaAssessment {
  return {
    ...a,
    acceptance: { status: a.acceptance?.status || 'unknown', details: list(a.acceptance?.details) },
    validity: {
      rules_version: a.validity?.rules_version || '',
      verdict: a.validity?.verdict || 'unknown',
      reasons: list(a.validity?.reasons),
      windows: list(a.validity?.windows),
      limitations: list(a.validity?.limitations),
    },
    anomalies: list(a.anomalies).map(x => ({ ...x, evidence: list(x.evidence), baseline: x.baseline || {} })),
    hypotheses: list(a.hypotheses).map(h => ({
      ...h,
      symptoms: list(h.symptoms),
      confidence_reasons: list(h.confidence_reasons),
      chain: list(h.chain),
      supporting: list(h.supporting),
      contradicting: list(h.contradicting),
      competing: list(h.competing),
      limitations: list(h.limitations),
      nodes: list(h.nodes),
    })),
    coverage: list(a.coverage),
    limitations: list(a.limitations),
    detection: a.detection ? { ...a.detection, levels: list(a.detection.levels), spikes: list(a.detection.spikes) } : undefined,
    graph: a.graph ? { ...a.graph, nodes: list(a.graph.nodes), edges: list(a.graph.edges), cycles: list(a.graph.cycles) } : undefined,
    capacity: list(a.capacity),
    littles_law: list(a.littles_law),
    browser_baseline: a.browser_baseline ? { ...a.browser_baseline, results: list(a.browser_baseline.results).map(r => ({ ...r, evidence: list(r.evidence), caveats: list(r.caveats) })), limitations: list(a.browser_baseline.limitations) } : undefined,
  }
}

const humanize = (value: string) => {
  const text = value.replace(/_/g, ' ').trim()
  return text ? text[0].toUpperCase() + text.slice(1) : value
}

const SIGNATURE_LABELS: Record<string, string> = {
  cold_start: 'Cold start',
  consumer_lag: 'Consumer lag',
  database_wait: 'Database wait',
  dead_letters: 'Dead letters',
  garbage_collection: 'Garbage collection',
  network_phase: 'Network phase delay',
  pool_exhaustion: 'Pool exhaustion',
  rebalance: 'Consumer rebalance',
  resource_pressure: 'Resource pressure',
  retries: 'Retries',
}

// rcaStatusLabel sentence-cases a status or evidence code.
export const rcaStatusLabel = (value: string) => humanize(value)

export const rcaSignatureLabel = (signature: string) => SIGNATURE_LABELS[signature] || humanize(signature)

export const RCA_OUTCOME_LABELS: Record<RcaOutcome, string> = {
  supported: 'Supported hypotheses',
  insufficient_evidence: 'Insufficient evidence',
  no_supported_hypothesis: 'No supported hypothesis',
}

export const RCA_VALIDITY_LABELS: Record<RcaValidityVerdict, string> = {
  valid: 'Valid run',
  suspect: 'Suspect run',
  invalid: 'Invalid run',
  unknown: 'Validity unknown',
}

export const rcaOutcomeLabel = (outcome: string) => RCA_OUTCOME_LABELS[outcome as RcaOutcome] || humanize(outcome)
export const rcaValidityLabel = (verdict: string) => RCA_VALIDITY_LABELS[verdict as RcaValidityVerdict] || humanize(verdict)

// rcaTone maps a verdict, confidence or coverage status to a badge tone.
export function rcaTone(value: string): 'success' | 'warning' | 'danger' | 'info' {
  switch (value) {
    case 'valid':
    case 'high':
    case 'complete':
    case 'passed':
      return 'success'
    case 'invalid':
    case 'missing':
    case 'breached':
      return 'danger'
    case 'suspect':
    case 'medium':
    case 'partial':
      return 'warning'
    default:
      return 'info'
  }
}

export const rcaConfidenceLabel = (confidence: RcaConfidence | string) => `${humanize(confidence)} confidence`

// RcaSeriesRef is a parsed series ID: transaction/<name>/<metric>,
// action/<name>/<metric>, dependency/<label>/<phase>,
// monitor/<provider>/<source>/<metric>/<digest>,
// imported/<dataset>/<metric>/<digest> or
// action_dimensions/<JSON tuple>/<metric>.
export interface RcaSeriesRef {
  dimension: string
  name: string
  metric: string
  provider?: string
}

export function parseRcaSeries(id: string): RcaSeriesRef {
  const parts = id.split('/')
  if (parts[0] === 'monitor' && parts.length >= 5) {
    return { dimension: 'monitor', provider: parts[1], name: parts.slice(3, -1).join('/'), metric: parts.slice(3, -1).join('/') }
  }
  if (parts[0] === 'imported' && parts.length >= 4) {
    return { dimension: 'imported', provider: parts[1], name: parts.slice(2, -1).join('/'), metric: parts.slice(2, -1).join('/') }
  }
  if (parts.length >= 3) return { dimension: parts[0], name: parts.slice(1, -1).join('/'), metric: parts[parts.length - 1] }
  return { dimension: '', name: id, metric: '' }
}

const METRIC_LABELS: Record<string, string> = {
  p95_ms: 'p95 latency',
  error_rate: 'error rate',
  throughput: 'throughput',
  dns_ms: 'DNS time',
  connect_ms: 'connect time',
  tls_ms: 'TLS time',
}

const PROVIDER_LABELS: Record<string, string> = {
  dynatrace: 'Dynatrace',
  prometheus: 'Prometheus',
  appdynamics: 'AppDynamics',
  elasticsearch: 'Elasticsearch',
}

export const rcaProviderLabel = (provider: string) => PROVIDER_LABELS[provider] || provider

const DOCUMENT_METRIC_LABELS: Record<string, string> = {
  fcp: 'FCP',
  lcp: 'LCP',
  cls: 'CLS',
  inp: 'INP',
}

// jointTupleLabel reads an action-dimension tuple ([action, network
// profile, hierarchy, phase]) as its non-empty parts.
function jointTupleLabel(name: string): string {
  try {
    const tuple: unknown = JSON.parse(name)
    if (Array.isArray(tuple)) return tuple.filter(part => typeof part === 'string' && part).join(' · ') || name
  } catch { /* Not a tuple: shown as recorded. */ }
  return name
}

export function rcaSeriesLabel(id: string): string {
  const s = parseRcaSeries(id)
  if (s.dimension === 'monitor') return `${s.name} · ${rcaProviderLabel(s.provider || '')}`
  if (s.dimension === 'imported') return `${s.name} · imported dataset ${(s.provider || '').slice(0, 8)}`
  if (!s.metric) return id
  if (s.dimension === 'action_dimensions') return `${jointTupleLabel(s.name)} · ${METRIC_LABELS[s.metric] || s.metric.replace(/_/g, ' ')}`
  if (s.dimension === 'browser_document') {
    // browser_document/<metric>/<tuple digest>/<statistic>: one measurement
    // tuple (engine, device profile, cohort, phase, group) of a document metric.
    // The digest names the whole tuple, not the runtime cohort a panel's
    // cohort filter takes, so it is labelled as a tuple.
    const [metric, digest] = s.name.split('/')
    return `${DOCUMENT_METRIC_LABELS[metric] || metric} ${s.metric} · tuple ${(digest || '').slice(0, 8)}`
  }
  return `${s.name} · ${METRIC_LABELS[s.metric] || s.metric.replace(/_/g, ' ')}`
}

// rcaSeriesPopulation is the report population a transaction or action
// series belongs to, or null for dependency and monitor series.
export function rcaSeriesPopulation(id: string): { population: 'transaction' | 'action'; name: string } | null {
  const s = parseRcaSeries(id)
  return s.dimension === 'transaction' || s.dimension === 'action' ? { population: s.dimension, name: s.name } : null
}

export function formatRcaEffect(anomaly: Pick<RcaAnomaly, 'effect' | 'unit'>): string {
  const value = anomaly.effect
  if (!Number.isFinite(value)) return 'Not recorded'
  const sign = value > 0 ? '+' : value < 0 ? '−' : ''
  const magnitude = Math.abs(value)
  const digits = magnitude >= 100 ? 0 : magnitude >= 10 ? 1 : 2
  const amount = magnitude.toLocaleString(undefined, { maximumFractionDigits: digits })
  switch (anomaly.unit) {
    case 'ms':
      return `${sign}${amount} ms`
    case '%':
      return `${sign}${amount} pts`
    case '1/s':
      return `${sign}${amount}/s`
    default:
      return anomaly.unit ? `${sign}${amount} ${anomaly.unit}` : `${sign}${amount}`
  }
}

// RcaWindow is a time window in epoch milliseconds.
export interface RcaWindow {
  start: number
  end: number
}

export function parseRcaWindow(value?: string): RcaWindow | null {
  if (!value) return null
  const [from, to] = value.split('~')
  const start = Date.parse(from)
  const end = Date.parse(to)
  return Number.isFinite(start) && Number.isFinite(end) && end > start ? { start, end } : null
}

export const formatRcaWindow = (w: RcaWindow) => `${new Date(w.start).toISOString()}~${new Date(w.end).toISOString()}`

export const sameRcaWindow = (a: RcaWindow | null, b: RcaWindow | null) => Boolean(a && b && a.start === b.start && a.end === b.end)

// rcaAnomalyWindow spans an anomaly from the earliest its onset may have been
// to its end.
export function rcaAnomalyWindow(a: RcaAnomaly): RcaWindow {
  const onset = Date.parse(a.onset)
  const uncertainty = (a.onset_uncertainty_ns || 0) / 1e6
  const end = Date.parse(a.end)
  return { start: onset - uncertainty, end: Math.max(Number.isFinite(end) ? end : onset, onset + uncertainty) }
}

export interface RcaHypothesisAnomaly {
  anomaly: RcaAnomaly
  role: 'symptom' | 'cause'
}

// rcaHypothesisAnomalies lists a hypothesis's symptoms and then the causes its
// chain cites, each once, skipping any the assessment no longer carries.
export function rcaHypothesisAnomalies(h: RcaHypothesis, anomalies: RcaAnomaly[]): RcaHypothesisAnomaly[] {
  const byId = new Map(anomalies.map(a => [a.id, a]))
  const out: RcaHypothesisAnomaly[] = []
  const seen = new Set<string>()
  const add = (id: string, role: 'symptom' | 'cause') => {
    const anomaly = byId.get(id)
    if (!anomaly || seen.has(id)) return
    seen.add(id)
    out.push({ anomaly, role })
  }
  h.symptoms.forEach(id => add(id, 'symptom'))
  h.chain.forEach(step => add(step.from, 'cause'))
  return out
}

// rcaHypothesisWindow spans a hypothesis's anomalies with a quarter of the
// span (at least 30 s) of context on each side.
export function rcaHypothesisWindow(h: RcaHypothesis, anomalies: RcaAnomaly[]): RcaWindow | null {
  const windows = rcaHypothesisAnomalies(h, anomalies).map(({ anomaly }) => rcaAnomalyWindow(anomaly)).filter(w => Number.isFinite(w.start) && Number.isFinite(w.end))
  if (!windows.length) return null
  const start = Math.min(...windows.map(w => w.start))
  const end = Math.max(...windows.map(w => w.end))
  const pad = Math.max(30_000, (end - start) / 4)
  return { start: Math.round(start - pad), end: Math.round(end + pad) }
}

// RcaPhaseMarker is a load-phase transition on the timeline. kind is the
// archived event's kind; inferred steps have none.
export interface RcaPhaseMarker {
  at: number
  label: string
  kind?: string
}

// Archived kinds that mark a load level, as opposed to pause, resume and stop.
const LEVEL_PHASE_KINDS = new Set(['ramp_start', 'level_reached', 'level_change'])

// rcaTimelinePhases chooses the timeline's load steps: the archived events
// when they mark the run's levels, otherwise the steps inferred from its
// steady levels together with any archived pause, resume or stop. A run that
// held one level archives only its stop. inferred says the steps were
// inferred.
export function rcaTimelinePhases(archived: RcaPhaseMarker[] | null, inferredSteps: RcaPhaseMarker[]): { phases: RcaPhaseMarker[]; inferred: boolean } {
  if (archived?.some(p => p.kind && LEVEL_PHASE_KINDS.has(p.kind))) return { phases: archived, inferred: false }
  const phases = [...inferredSteps, ...(archived || [])].sort((a, b) => a.at - b.at)
  return { phases, inferred: inferredSteps.length > 0 }
}

const MAX_PHASE_EVENTS = 1000

// rcaPhaseMarkers reads an archived phase_events.jsonl
// (apexload.phase_event.v1), skipping lines it cannot read.
export function rcaPhaseMarkers(jsonl: string): RcaPhaseMarker[] {
  const out: RcaPhaseMarker[] = []
  for (const line of jsonl.split('\n').slice(0, MAX_PHASE_EVENTS)) {
    if (!line.trim()) continue
    let event: { kind?: unknown; level?: unknown; target?: unknown; at?: unknown }
    try {
      event = JSON.parse(line)
    } catch {
      continue
    }
    const at = typeof event.at === 'string' ? Date.parse(event.at) : Number.NaN
    if (!Number.isFinite(at) || typeof event.kind !== 'string') continue
    const who = typeof event.target === 'string' && event.target ? event.target : 'The run'
    const level = typeof event.level === 'number' ? `${formatLoad(event.level)} VUsers` : 'its target'
    const labels: Record<string, string> = {
      ramp_start: `${who} began ramping to ${level}`,
      level_reached: `${who} reached ${level}`,
      level_change: `${who} stepped to ${level}`,
      pause: 'The run paused',
      resume: 'The run resumed',
      stop: 'The run stopped',
    }
    out.push({ at, label: labels[event.kind] || humanize(event.kind), kind: event.kind })
  }
  return out.sort((a, b) => a.at - b.at)
}

// rcaLoadSteps infers where each load level began, for runs that archived no
// phase events. A level's span starts after its settle period, so the step
// itself came that much earlier.
export function rcaLoadSteps(levels: RcaLoadLevel[], settleNs = 30e9): RcaPhaseMarker[] {
  return levels.flatMap(level => {
    const start = Date.parse(level.start)
    if (!Number.isFinite(start)) return []
    const load = [level.vusers ? `${formatLoad(level.vusers)} VUsers` : '', level.rate ? `${formatLoad(level.rate)}/s` : ''].filter(Boolean).join(', ')
    return [{ at: start - settleNs / 1e6, label: load ? `Load step to ${load}` : 'Load step' }]
  })
}

const formatLoad = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: 1 })

export interface RcaTimelineRow {
  t: number
  tps?: number
  p95?: number
  vusers?: number
  errorPct?: number
}

// rcaTimelineRows keeps the run's interval history inside a window. History
// points carry their interval's end; onsets carry its start, so each point
// moves back by the run's typical interval to line up with the onsets.
export function rcaTimelineRows(history: MetricsHistoryPoint[], window: RcaWindow | null): RcaTimelineRow[] {
  const ends = history.flatMap(point => (typeof point.timestampMs === 'number' && Number.isFinite(point.timestampMs) ? [point.timestampMs] : [])).sort((a, b) => a - b)
  const gaps = ends.slice(1).map((end, i) => end - ends[i]).filter(gap => gap > 0).sort((a, b) => a - b)
  const interval = gaps.length ? gaps[Math.floor(gaps.length / 2)] : 0
  return history
    .filter(point => typeof point.timestampMs === 'number' && Number.isFinite(point.timestampMs))
    .map(point => ({ t: (point.timestampMs as number) - interval, tps: point.tps, p95: point.p95 ?? point.p90, vusers: point.vusers, errorPct: point.errorPct }))
    .filter(row => !window || (row.t >= window.start && row.t <= window.end))
    .sort((a, b) => a.t - b.t)
}

// rcaValidityConcerns are the judged spans that were not valid.
export const rcaValidityConcerns = (windows: RcaValidityWindow[]) => windows.filter(w => w.verdict === 'suspect' || w.verdict === 'invalid')

// rcaErrorPatterns are the run's error patterns that touched the window and,
// when a pattern names its actions, one of the given names.
export function rcaErrorPatterns(report: RunArchiveReport | null, names: string[], window: RcaWindow | null): RunArchiveErrorPattern[] {
  return (report?.error_patterns || []).filter(p => {
    if (p.actions?.length && names.length && !p.actions.some(a => names.includes(a))) return false
    if (!window) return true
    const first = Date.parse(p.first_seen)
    const latest = Date.parse(p.latest_seen)
    return !(Number.isFinite(first) && first > window.end) && !(Number.isFinite(latest) && latest < window.start)
  })
}

export function rcaPopulationStats(report: RunArchiveReport | null, population: 'transaction' | 'action', name: string): RunArchiveActionStats | undefined {
  return population === 'transaction' ? report?.by_transaction?.[name] : report?.by_action?.[name]
}

// rcaLimitationLabel names a limitation code for people.
export function rcaLimitationLabel(code: string): string {
  return humanize(code)
}

export function rcaGraphNodeLabel(assessment: RcaAssessment, id: string): string {
  return assessment.graph?.nodes.find(node => node.id === id)?.label || id
}

// rootCauseSourceLabel says where a report's generated root cause came from
// (Go reportRootCauseSourceLabel).
export function rootCauseSourceLabel(view: ReportRootCause): string {
  return view.source === 'v2'
    ? `Root-cause v2, analysis revision ${view.revision ?? 0} (${view.engine || 'engine not recorded'})`
    : `Root-cause v1 summary (${view.schema_version})`
}

export function formatRcaTime(value: string | number): string {
  const at = typeof value === 'number' ? value : Date.parse(value)
  return Number.isFinite(at) ? new Date(at).toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z').replace(/Z$/, ' UTC') : 'Not recorded'
}

export function formatRcaDuration(ns: number): string {
  const seconds = ns / 1e9
  if (!Number.isFinite(seconds) || seconds <= 0) return '0 s'
  return seconds >= 120 ? `${Math.round(seconds / 60)} min` : `${Math.round(seconds)} s`
}
