import type { RcaAnalysisResponse, RcaAssessment, ReportRootCause, ReportRootCauseSummary } from '../../types/rca'

// A completed assessment for tests: Checkout's p95 rose at 12:03:20 after the
// cart service's CPU rose at 12:02:30, right after a load step to 20 VUsers.
// Lists the controller leaves empty are null, as Go sends them.
export function rcaTestAssessment(): RcaAssessment {
  return {
    schema_version: 'apexload.root_cause.v2',
    analysis_id: 'rca-1',
    revision: 2,
    inputs: { archive: 'sha256:archive', evidence_revision: 1, engine: 'apexload.rca.engine.v2+test', signature_set: 'signatures.v1' },
    acceptance: { status: 'breached', details: ['Checkout p95 3,100 ms exceeds 2,000 ms'] },
    validity: { rules_version: 'validity.v1', verdict: 'unknown', reasons: ['no load generator statistics were archived'], windows: [
      { start: '2026-10-05T12:04:30Z', end: '2026-10-05T12:04:40Z', verdict: 'suspect', findings: [{ rule: 'generator_cpu', generator: 'gen-1', value: 92, threshold: 85 }] },
      { start: '2026-10-05T12:04:40Z', end: '2026-10-05T12:04:50Z', verdict: 'valid', findings: [] },
    ], limitations: null as never },
    anomalies: [
      {
        id: 'anomaly-cpu', series: 'monitor/dynatrace/dt-src/builtin:service.cpu.time/abcdef012345',
        onset: '2026-10-05T12:02:30Z', end: '2026-10-05T12:06:00Z', onset_uncertainty_ns: 10e9,
        scope: 'time', direction: 'increase', recovered: false, level: '', effect: 60, unit: '%', peak_z: 8,
        baseline: {}, evidence: [{ id: 'evidence-cpu', kind: 'monitor_series', locator: 'monitor_points.jsonl#monitor/dynatrace/dt-src/builtin:service.cpu.time/abcdef012345?from=2026-10-05T12:02:30Z' }],
      },
      {
        id: 'anomaly-sym', series: 'transaction/Checkout/p95_ms',
        onset: '2026-10-05T12:03:20Z', end: '2026-10-05T12:06:00Z', onset_uncertainty_ns: 10e9,
        scope: 'time', direction: 'increase', recovered: false, level: '', effect: 300, unit: 'ms', peak_z: 9,
        baseline: {}, evidence: [{ id: 'evidence-sym', kind: 'interval', locator: 'interval_distributions.jsonl#transaction=Checkout?from=2026-10-05T12:03:20Z' }],
      },
    ],
    hypotheses: [
      {
        id: 'hypothesis-cpu', signature: 'resource_pressure', symptoms: ['anomaly-sym'], rank: 1, score: 0.82, confidence: 'medium',
        confidence_reasons: ['the evidence is connected to the symptom through the run\'s graph', 'the evidence began before the symptom by more than both onsets\' uncertainty'],
        chain: [{ from: 'anomaly-cpu', to: 'anomaly-sym', relation: 'precedes' }],
        supporting: [{ id: 'evidence-cpu', kind: 'monitor_series', locator: 'monitor_points.jsonl#monitor/dynatrace/dt-src/builtin:service.cpu.time/abcdef012345' }],
        contradicting: [], competing: ['garbage_collection'],
        falsification: 'CPU stays below saturation while latency rises',
        limitations: ['competing database_wait could not be judged: db_wait not measured'],
        nodes: ['service:SERVICE-1'],
      },
      {
        id: 'hypothesis-gc', signature: 'garbage_collection', symptoms: ['anomaly-sym'], rank: 2, score: 0.4, confidence: 'low',
        confidence_reasons: ['no used graph path connects the evidence to the symptom; approve the dependency\'s mapping to connect it'],
        chain: null as never, supporting: [], contradicting: [{ id: 'evidence-gc', kind: 'monitor_series', locator: 'monitor_points.jsonl#monitor/prometheus/prom/jvm_gc_seconds_rate/0123456789ab' }],
        competing: ['resource_pressure'], falsification: 'GC pauses stay flat while latency rises', limitations: [], nodes: [],
      },
    ],
    outcome: 'supported',
    coverage: [
      { evidence: 'transactions', status: 'complete' },
      { evidence: 'monitor_sources', status: 'partial', detail: '1 of 2 sources reported' },
    ],
    limitations: [{ code: 'validity_caps_confidence', detail: 'run validity is unknown, so confidence is at most medium' }],
    detection: {
      status: 'complete', params_version: 'detect.v1', params: { level_settle_ns: 30e9 }, series_evaluated: 12, series_excluded: 0,
      levels: [
        { start: '2026-10-05T12:00:30Z', end: '2026-10-05T12:02:00Z', vusers: 10 },
        { start: '2026-10-05T12:02:30Z', end: '2026-10-05T12:08:00Z', vusers: 20 },
      ],
      spikes: null as never,
    },
    graph: { schema: 'apexload.rca.graph.v1', nodes: [{ id: 'service:SERVICE-1', kind: 'service', label: 'cart-service' }], edges: null as never, cycles: null as never },
    capacity: null as never,
    littles_law: null as never,
  }
}

export function rcaTestResponse(overrides: Partial<RcaAnalysisResponse> = {}): RcaAnalysisResponse {
  return {
    analysis_id: 'rca-1', revision: 2, state: 'completed', created_at: '2026-10-05T12:10:00Z', completed_at: '2026-10-05T12:10:05Z',
    presented: true, assessment: rcaTestAssessment(), ...overrides,
  }
}

// rcaTestReportView is the report's root-cause model for the assessment
// above, as the controller's reportRootCauseFromV2 builds it.
export function rcaTestReportView(overrides: Partial<ReportRootCause> = {}): ReportRootCause {
  return {
    source: 'v2', schema_version: 'apexload.root_cause.v2', analysis_id: 'rca-1', revision: 2, engine: 'apexload.rca.engine.v2+test',
    outcome: 'supported', summary: 'Resource pressure is the leading hypothesis for Checkout · p95 latency (medium confidence).',
    validity: 'unknown', validity_reasons: ['no load generator statistics were archived'], acceptance: 'breached',
    hypotheses: [
      { id: 'hypothesis-cpu', rank: 1, title: 'Resource pressure', confidence: 'medium', explains: ['Checkout · p95 latency'],
        chain: ['builtin:service.cpu.time · Dynatrace (+60 pts) began before Checkout · p95 latency'], competing: ['Garbage collection'],
        falsification: 'CPU stays below saturation while latency rises' },
      { id: 'hypothesis-gc', rank: 2, title: 'Garbage collection', confidence: 'low', explains: ['Checkout · p95 latency'],
        contradicting: ['monitor_points.jsonl#monitor/prometheus/prom/jvm_gc_seconds_rate/0123456789ab'], falsification: 'GC pauses stay flat while latency rises' },
    ],
    limitations: ['Validity caps confidence: run validity is unknown, so confidence is at most medium'],
    ...overrides,
  }
}

// rcaTestSummary is the report's root-cause summary for the assessment
// above, as the controller's rootCauseSummaries builds it.
export function rcaTestSummary(overrides: Partial<ReportRootCauseSummary> = {}): ReportRootCauseSummary {
  return {
    state: 'completed', presented: true, analysis_enabled: true, analysis_id: 'rca-1', revision: 2, origin: 'automatic', outcome: 'supported', validity: 'unknown',
    top_hypothesis: { id: 'hypothesis-cpu', rank: 1, title: 'Resource pressure', confidence: 'medium', explains: ['Checkout · p95 latency'] },
    hypothesis_count: 2, anomaly_count: 2, completed_at: '2026-10-05T12:10:05Z', ...overrides,
  }
}
