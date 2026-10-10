// Root-cause v2 contract (apexload.root_cause.v2), as the controller returns
// it from /tests/{id}/archive/root-cause/v2 and .../analyses/{analysis_id}.
// The controller may send null for an empty list; normalizeRcaAssessment
// (components/rca/rcaModel) makes every list present.

export type RcaOutcome = 'supported' | 'insufficient_evidence' | 'no_supported_hypothesis'
export type RcaConfidence = 'high' | 'medium' | 'low'
export type RcaValidityVerdict = 'valid' | 'suspect' | 'invalid' | 'unknown'
export type RcaAnalysisState = 'none' | 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'
export type RcaOrigin = 'requested' | 'automatic'

export interface RcaInputDigests {
  archive: string
  evidence_revision: number
  baseline?: string
  mapping?: string
  policy?: string
  engine: string
  signature_set?: string
}

export interface RcaEvidenceRef {
  id: string
  kind: string
  locator: string
  digest?: string
}

export interface RcaAnomaly {
  id: string
  series: string
  onset: string
  end: string
  onset_uncertainty_ns: number
  scope: 'time' | 'load'
  direction: 'increase' | 'decrease'
  recovered: boolean
  level: string
  effect: number
  unit: string
  peak_z: number
  baseline: { test_id?: string; qualification?: string }
  evidence: RcaEvidenceRef[]
}

export interface RcaChainStep {
  from: string
  to: string
  relation: 'precedes' | 'unordered' | 'load_coincident' | string
}

export interface RcaHypothesis {
  id: string
  signature: string
  symptoms: string[]
  rank: number
  score: number
  confidence: RcaConfidence
  confidence_reasons: string[]
  chain: RcaChainStep[]
  supporting: RcaEvidenceRef[]
  contradicting: RcaEvidenceRef[]
  competing: string[]
  falsification: string
  limitations: string[]
  nodes: string[]
}

export interface RcaCoverage {
  evidence: string
  status: 'complete' | 'partial' | 'missing'
  detail?: string
  // sources name the monitor sources behind the monitor_sources row,
  // incomplete ones first.
  sources?: RcaCoverageSource[]
}

// RcaCoverageSource is one monitor source's outcome for the run: id is the
// project's monitor source, reason why its coverage is incomplete.
export interface RcaCoverageSource {
  id: string
  pack?: string
  state?: string
  reason?: string
  required?: boolean
}

export interface RcaLimitation {
  code: string
  detail?: string
}

export interface RcaValidityWindow {
  start: string
  end: string
  verdict: RcaValidityVerdict
  findings?: { rule: string; generator?: string; value: number; threshold: number }[]
}

export interface RcaValidity {
  rules_version: string
  verdict: RcaValidityVerdict
  reasons: string[]
  windows: RcaValidityWindow[]
  limitations: string[]
}

export interface RcaLoadLevel {
  start: string
  end: string
  vusers?: number
  rate?: number
}

export interface RcaDetection {
  status: 'complete' | 'partial' | 'skipped' | 'unavailable'
  params_version: string
  // params.level_settle_ns is how much of each level's start was skipped.
  params?: { level_settle_ns?: number }
  series_evaluated: number
  series_excluded: number
  levels: RcaLoadLevel[]
  spikes: { series: string; at: string; value: number; z: number; scope: string; level: string }[]
}

export interface RcaGraphNode {
  id: string
  kind: string
  label: string
  provider?: string
  type?: string
}

export interface RcaGraphEdge {
  id: string
  kind: string
  from: string
  to: string
  confidence: string
  provenance: string
  used: boolean
  ambiguous?: boolean
}

export interface RcaGraph {
  schema: string
  nodes: RcaGraphNode[]
  edges: RcaGraphEdge[]
  cycles: string[][]
}

export interface RcaBudgetUse {
  limit: string
  allowed: number
  offered: number
}

export interface RcaLittlesLaw {
  queue: string
  start?: string
  end?: string
  eligible: boolean
  reason?: string
  depth?: number
  arrival_rate_per_s?: number
  residence_s?: number
  predicted_depth?: number
  ratio?: number
}

export interface RcaCapacity {
  series: string
  load_unit: string
  levels: { load: number; p95_ms: number }[]
  knee?: number
  slope_before: number
  slope_after: number
  reason?: string
}

export interface RcaAssessment {
  schema_version: string
  analysis_id: string
  revision: number
  inputs: RcaInputDigests
  acceptance: { status: string; details: string[] }
  validity: RcaValidity
  anomalies: RcaAnomaly[]
  hypotheses: RcaHypothesis[]
  outcome: RcaOutcome
  coverage: RcaCoverage[]
  limitations: RcaLimitation[]
  detection?: RcaDetection
  graph?: RcaGraph
  capacity?: RcaCapacity[]
  littles_law?: RcaLittlesLaw[]
  // Browser document cohorts against the baseline run (browser_baseline.v1).
  browser_baseline?: RcaBrowserBaseline
}

export interface RcaBrowserBaselineResult {
  id: string
  series: string
  metric: string
  unit: string
  level: string
  // Tells apart two steady levels at one load.
  level_start?: string
  state: 'anomaly' | 'within_range' | 'unscored' | string
  reason?: string
  detail?: string
  direction?: 'increase' | 'decrease' | string
  current: number
  current_buckets: number
  current_samples: number
  baseline_mean?: number
  baseline_stddev?: number
  baseline_buckets: number
  baseline_samples: number
  expected_low?: number
  expected_high?: number
  z?: number
  effect?: number
  effect_floor?: number
  caveats?: string[]
  baseline: { test_id?: string; qualification?: string }
  evidence: { id?: string; kind: string; locator: string }[]
}

export interface RcaBrowserBaseline {
  version: string
  status: string
  params: { min_baseline_buckets: number; sigma: number; timing_floor_ms: number; timing_floor_fraction: number; cls_floor: number; level_tolerance: number; max_results: number }
  // The run's pinned browser policy (id@revision), when its floors and
  // baseline mode applied.
  policy?: string
  baseline: { test_id?: string; qualification?: string; reasons?: string[] }
  results: RcaBrowserBaselineResult[]
  excluded?: Record<string, number>
  limitations: RcaLimitation[]
}

export interface RcaAnalysisResponse {
  analysis_id?: string
  revision?: number
  state: RcaAnalysisState
  created_at?: string
  completed_at?: string
  inputs?: RcaInputDigests
  assessment?: RcaAssessment
  error?: string
  // presented says whether the project presents v2 in Summary and
  // Diagnostics.
  presented: boolean
  // analysis_enabled says whether the project computes new analyses; saved
  // ones stay readable when it does not.
  analysis_enabled?: boolean
  // auto_analyze says whether the project analyzes every archived run.
  auto_analyze?: boolean
  // origin is requested (asked for) or automatic (queued when the run's
  // archive was finalized).
  origin?: RcaOrigin
  // archive_changed says the run's archive changed after the analysis read
  // it; evidence archived since is not in the analysis.
  archive_changed?: boolean
}

// RcaCritique is a review of an analysis that passed the narration check,
// stored apart from it.
export interface RcaCritique {
  id: string
  analysis_id: string
  revision: number
  created_at: string
  created_by?: string
  author_kind: 'agent' | 'analyst'
  model?: string
  narrative: string
}

// ReportRootCause is the generated root cause as every report format
// presents it (Go ReportRootCause): from v2 where the project presents it,
// else from v1. Analyst findings are never part of it.
export interface ReportRootCause {
  source: 'v1' | 'v2'
  schema_version: string
  analysis_id?: string
  revision?: number
  completed_at?: string
  engine?: string
  outcome: string
  summary: string
  confidence?: string
  validity?: string
  validity_reasons?: string[]
  acceptance?: string
  hypotheses: ReportRootCauseHypothesis[]
  coverage?: RcaCoverage[]
  limitations?: string[]
  next_actions?: string[]
  notes?: string[]
  // v2's comparison of the run's browser document cohorts with the baseline
  // run's (browser_baseline.v1), when the run had document series.
  browser_baseline?: ReportBrowserBaseline
}

// ReportBrowserBaseline presents the assessment's browser baseline (Go
// ReportBrowserBaseline): cohorts scored, anomalies and the ones that moved.
export interface ReportBrowserBaseline {
  version: string
  status: string
  baseline?: string
  compared: number
  anomalies: number
  unscored: number
  // summary is the one-line account every report format prints.
  summary: string
  findings?: string[]
  limitations?: string[]
}

export interface ReportRootCauseHypothesis {
  id?: string
  rank: number
  title: string
  confidence?: string
  severity?: string
  detail?: string
  explains?: string[]
  reasons?: string[]
  chain?: string[]
  contradicting?: string[]
  competing?: string[]
  falsification?: string
  limitations?: string[]
  evidence?: string[]
}

// ArchivePhaseEvent is one archived load-phase transition
// (apexload.phase_event.v1).
export interface ArchivePhaseEvent {
  schema?: string
  kind: string
  level?: number
  target?: string
  at: string
}

export interface ExemplarLink {
  provider: string
  connection_id: string
  url: string
}

export interface LinkedExemplar {
  population: string
  group?: string
  name: string
  interval_start: string
  interval_end: string
  reason: string
  trace_id: string
  span_id?: string
  duration_ms: number
  success: boolean
  status_code?: number
  error_class?: string
  timestamp: string
  links: ExemplarLink[]
}

export interface ExemplarPage {
  exemplars: LinkedExemplar[]
  total: number
  capture_truncated?: boolean
  links_unavailable?: boolean
}

// ReportRootCauseSummary is a run's root cause in brief (Go
// ReportRootCauseSummary), carried by every archive report response, single
// or batch: whether the project presents root cause, the state of the run's
// newest analysis and the verdict of its newest completed one.
export interface ReportRootCauseSummary {
  // not_enabled, none, or the newest analysis's state.
  state: 'not_enabled' | RcaAnalysisState
  presented: boolean
  analysis_enabled: boolean
  auto_analyze?: boolean
  // The verdict fields describe the newest completed analysis.
  analysis_id?: string
  revision?: number
  origin?: RcaOrigin
  outcome?: RcaOutcome
  validity?: RcaValidityVerdict
  top_hypothesis?: ReportRootCauseHypothesis
  hypothesis_count: number
  anomaly_count: number
  completed_at?: string
  archive_changed?: boolean
  // latest is the newest analysis when it is not the completed one.
  latest?: { analysis_id: string; revision: number; state: RcaAnalysisState; origin?: RcaOrigin; created_at: string }
}

// RcaWindowLink opens a time window of the run in one of its providers.
export interface RcaWindowLink {
  provider: string
  connection_id: string
  url: string
}

export interface RcaWindowLinks {
  links: RcaWindowLink[]
  links_unavailable?: boolean
}

// ProjectAnalysisGates are a project's analysis gates (Go AnalysisGates).
export interface ProjectAnalysisGates {
  root_cause: { enabled: boolean; present: boolean; auto_analyze: boolean; auto_enrich: boolean }
  business_keys: { enabled: boolean }
}

// ProjectAnalysisSettings are what a project set, stamped per save.
export interface ProjectAnalysisSettings extends ProjectAnalysisGates {
  revision: number
  updated_at?: string
  updated_by?: string
}

// ProjectAnalysisSettingsResponse is GET/PUT
// /projects/{id}/analysis-settings: the project's own settings, the flags the
// deployment forces on, and the gates in effect.
export interface ProjectAnalysisSettingsResponse {
  project_id: string
  settings: ProjectAnalysisSettings
  forced_by_env: ProjectAnalysisGates
  effective: ProjectAnalysisGates
}
