import type { MetricsHistoryPoint, RunArchiveActionStats, RunArchiveErrorPattern, RunArchiveReport } from '../../types'
import type { TimelineOverlay } from '../runReport/timelineOverlays'
import { formatDateTime, formatTimeOfDay } from '../../utils/dateTime'
import type {
  RcaAnomaly,
  RcaAssessment,
  RcaConfidence,
  RcaHypothesis,
  RcaLoadLevel,
  RcaOrigin,
  RcaOutcome,
  RcaValidityVerdict,
  RcaValidityWindow,
  ReportRootCause,
  ReportRootCauseSummary,
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
    browser_selection: a.browser_selection ? {
      ...a.browser_selection,
      reasons: list(a.browser_selection.reasons),
      rejected: list(a.browser_selection.rejected).map(r => ({ ...r, reasons: list(r.reasons) })),
    } : undefined,
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

export type RcaTone = 'success' | 'warning' | 'danger' | 'info'

// rcaTone maps a verdict, confidence or coverage status to a badge tone.
export function rcaTone(value: string): RcaTone {
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
  // Two significant digits under 1, so a small change never reads as zero
  // (Go rcaEffectText).
  const digits = magnitude >= 100 ? 0 : magnitude >= 10 ? 1 : magnitude >= 1 || magnitude === 0 ? 2 : Math.min(Math.ceil(-Math.log10(magnitude)) + 1, 6)
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

// An anomaly's role on a timeline: a hypothesis's symptom or cause, or one
// of the run's changes that no selected hypothesis cites.
export type RcaAnomalyRole = 'symptom' | 'cause' | 'anomaly'

export interface RcaHypothesisAnomaly {
  anomaly: RcaAnomaly
  role: RcaAnomalyRole
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

// formatRcaTime states a moment the way the rest of the app does, to the
// second (onsets are judged to the second).
export function formatRcaTime(value: string | number): string {
  const at = typeof value === 'number' ? value : Date.parse(value)
  return Number.isFinite(at) ? formatDateTime(at, { seconds: true }) : 'Not recorded'
}

// formatRcaClock is a time of day within the run, for labels that sit beside
// a chart whose axis shows times of day.
export function formatRcaClock(value: string | number): string {
  return formatTimeOfDay(value, { fallback: 'Not recorded' })
}

// rcaCollapseLabels keeps each label once, in first-seen order; a series that
// changed more than once is one entry with its count ("Login · error rate
// (2 changes)"), not the same label repeated. The exports do the same.
export function rcaCollapseLabels(labels: string[]): string[] {
  const counts = new Map<string, number>()
  for (const label of labels) counts.set(label, (counts.get(label) || 0) + 1)
  return [...counts].map(([label, count]) => count > 1 ? `${label} (${count} changes)` : label)
}

// rcaSentence capitalizes the first letter of an engine phrase.
export function rcaSentence(text: string): string {
  const trimmed = text.trim()
  return trimmed ? trimmed[0].toUpperCase() + trimmed.slice(1) : trimmed
}

// rcaLimitationText presents an engine limitation for people: the engine
// names competing signatures by identifier ("competing resource_pressure
// could not be judged"), which become their titles. Stored analyses keep the
// engine's wording, so this covers old ones too.
export function rcaLimitationText(text: string): string {
  return rcaSentence(text.replace(/competing ([a-z][a-z0-9_]*) could not be judged/g, (_match, name: string) => `the competing cause ${rcaSignatureLabel(name)} could not be judged`))
}

export function formatRcaDuration(ns: number): string {
  const seconds = ns / 1e9
  if (!Number.isFinite(seconds) || seconds <= 0) return '0 s'
  return seconds >= 120 ? `${Math.round(seconds / 60)} min` : `${Math.round(seconds)} s`
}

export const rcaOriginLabel = (origin?: RcaOrigin | string) => (origin === 'automatic' ? 'Automatic' : 'Requested')

// What the root-cause line on a summary surface invites: investigating the
// leading hypothesis, reviewing the changes when none is supported, or
// opening the analysis; none when there is nothing to open.
export type RcaSummaryAction = 'investigate' | 'review' | 'open' | 'none'

export interface RcaSummaryLine {
  text: string
  tone: RcaTone
  action: RcaSummaryAction
  actionLabel: string
  // pending says an analysis is queued or running.
  pending: boolean
}

const RCA_ACTION_LABELS: Record<RcaSummaryAction, string> = {
  investigate: 'Investigate root cause',
  review: 'Review changes',
  open: 'Open root cause',
  none: '',
}

const plural = (count: number, one: string, many: string) => `${count.toLocaleString()} ${count === 1 ? one : many}`

// rcaSummaryLine is the one line every surface that lists or headlines runs
// shows for a run's root cause: the leading hypothesis, that no cause is
// supported, that evidence was too thin, or the state of the analysis.
// Validity is shown beside it, from the report's own verdict.
export function rcaSummaryLine(summary?: ReportRootCauseSummary | null): RcaSummaryLine | null {
  if (!summary) return null
  const line = (text: string, tone: RcaTone, action: RcaSummaryAction, pending = false): RcaSummaryLine => ({ text, tone, action, actionLabel: RCA_ACTION_LABELS[action], pending })
  if (summary.state === 'not_enabled') return line('Root-cause analysis is not enabled for this project', 'info', 'none')
  const latest = summary.latest
  const pending = summary.state === 'queued' || summary.state === 'running'
  let verdict: RcaSummaryLine | null = null
  if (summary.outcome === 'supported' && summary.top_hypothesis) {
    const top = summary.top_hypothesis
    verdict = line(`#${top.rank || 1} ${top.title}${top.confidence ? ` (${top.confidence} confidence)` : ''}`, 'warning', 'investigate')
  } else if (summary.outcome === 'no_supported_hypothesis') {
    verdict = line(`No cause supported · ${plural(summary.anomaly_count || 0, 'change', 'changes')} detected`, 'info', 'review')
  } else if (summary.outcome === 'insufficient_evidence') {
    verdict = line('Too little evidence to judge a cause', 'warning', 'open')
  }
  if (verdict) {
    const notes: string[] = []
    if (latest && (latest.state === 'queued' || latest.state === 'running')) notes.push(`re-analysis ${latest.state}`)
    else if (latest?.state === 'failed') notes.push('latest analysis failed')
    if (summary.archive_changed) notes.push('archive changed since')
    return { ...verdict, text: [verdict.text, ...notes].join(' · '), pending }
  }
  switch (summary.state) {
    case 'queued':
      return line('Analysis queued', 'info', 'open', true)
    case 'running':
      return line('Analysis running', 'info', 'open', true)
    case 'failed':
      return line('Analysis failed', 'danger', 'open')
    case 'cancelled':
      return line('Analysis cancelled', 'info', 'open')
    default:
      return line('No analysis yet', 'info', 'open')
  }
}

// rcaLargestChanges are an assessment's strongest anomalies by peak z, the
// effect measured against each series' own spread, so changes in different
// units compare; earlier onsets first among equals.
export function rcaLargestChanges(assessment: RcaAssessment, limit = 5): RcaAnomaly[] {
  return [...assessment.anomalies]
    .sort((a, b) => Math.abs(b.peak_z || 0) - Math.abs(a.peak_z || 0) || Date.parse(a.onset) - Date.parse(b.onset))
    .slice(0, Math.max(0, limit))
}

// rcaPatternHypotheses are the hypotheses an error pattern overlaps: a
// symptom of the hypothesis is a transaction or action the pattern names,
// and the hypothesis's span touches the pattern's first to latest sighting.
// A pattern that names no action is not attributed to any hypothesis.
export function rcaPatternHypotheses(assessment: RcaAssessment, pattern: RunArchiveErrorPattern): RcaHypothesis[] {
  if (!pattern.actions?.length) return []
  const first = Date.parse(pattern.first_seen)
  const latest = Date.parse(pattern.latest_seen)
  const byId = new Map(assessment.anomalies.map(a => [a.id, a]))
  return assessment.hypotheses.filter(h => {
    const names = h.symptoms.flatMap(id => {
      const anomaly = byId.get(id)
      const population = anomaly ? rcaSeriesPopulation(anomaly.series) : null
      return population ? [population.name] : []
    })
    if (!names.some(name => pattern.actions?.includes(name))) return false
    const span = rcaHypothesisWindow(h, assessment.anomalies)
    if (!span) return false
    return !(Number.isFinite(first) && first > span.end) && !(Number.isFinite(latest) && latest < span.start)
  })
}

// rcaTimelineOverlays are an assessment's marks for the run timeline: every
// anomaly a hypothesis cites (its symptoms and causes, under the
// highest-ranked hypothesis citing it), the largest anomalies no hypothesis
// cites, the spans the run was not valid and its load steps. Each anomaly's
// ref opens its hypothesis ("hypothesis:<id>") or its window
// ("window:<start>~<end>").
export function rcaTimelineOverlays(assessment: RcaAssessment, phases: RcaPhaseMarker[] = [], uncitedLimit = 5): TimelineOverlay[] {
  const out: TimelineOverlay[] = []
  const cited = new Set<string>()
  for (const h of [...assessment.hypotheses].sort((a, b) => a.rank - b.rank)) {
    for (const { anomaly, role } of rcaHypothesisAnomalies(h, assessment.anomalies)) {
      if (cited.has(anomaly.id)) continue
      cited.add(anomaly.id)
      const w = rcaAnomalyWindow(anomaly)
      out.push({ id: `anomaly-${anomaly.id}`, kind: 'anomaly', start: Date.parse(anomaly.onset), end: w.end, tone: role === 'symptom' ? 'danger' : 'warning',
        label: `${role === 'symptom' ? 'Symptom' : 'Cause'}: ${rcaSeriesLabel(anomaly.series)} ${formatRcaEffect(anomaly)} at ${formatRcaClock(anomaly.onset)} (#${h.rank} ${rcaSignatureLabel(h.signature)})`, ref: `hypothesis:${h.id}` })
    }
  }
  const uncited = rcaLargestChanges({ ...assessment, anomalies: assessment.anomalies.filter(a => !cited.has(a.id)) }, uncitedLimit)
  for (const anomaly of uncited) {
    const w = rcaAnomalyWindow(anomaly)
    out.push({ id: `anomaly-${anomaly.id}`, kind: 'anomaly', start: Date.parse(anomaly.onset), end: w.end, tone: 'accent',
      label: `Change: ${rcaSeriesLabel(anomaly.series)} ${formatRcaEffect(anomaly)} at ${formatRcaClock(anomaly.onset)}`, ref: `window:${formatRcaWindow(w)}` })
  }
  for (const concern of rcaValidityConcerns(assessment.validity.windows)) {
    const start = Date.parse(concern.start)
    const end = Date.parse(concern.end)
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue
    out.push({ id: `validity-${concern.start}`, kind: 'validity', start, end, tone: 'muted', label: `${rcaValidityLabel(concern.verdict)} ${formatRcaTime(start)} to ${formatRcaTime(end)}` })
  }
  phases.forEach((phase, i) => out.push({ id: `phase-${i}-${phase.at}`, kind: 'phase', start: phase.at, tone: 'muted', label: phase.label }))
  return out.filter(overlay => Number.isFinite(overlay.start))
}

// rcaErrorPatternAnchor is the element ID of an error pattern's row in the
// report's error-pattern table: a short stable hash of its signature
// (FNV-1a), so the root-cause panel can scroll to it.
export function rcaErrorPatternAnchor(signature: string): string {
  let hash = 0x811c9dc5
  for (let i = 0; i < signature.length; i += 1) {
    hash ^= signature.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return `error-pattern-${hash.toString(16).padStart(8, '0')}`
}

// rcaRevealElement scrolls an element of the page into view and marks it
// for a moment, for links within one report section.
export function rcaRevealElement(id: string): boolean {
  const element = typeof document === 'undefined' ? null : document.getElementById(id)
  if (!element) return false
  element.scrollIntoView?.({ block: 'center' })
  element.classList.add('rca-revealed')
  window.setTimeout(() => element.classList.remove('rca-revealed'), 2000)
  return true
}
