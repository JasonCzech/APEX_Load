import { describe, expect, it } from 'vitest'
import {
  analysisCSV,
  comparisonEligibility,
  loadLatency,
  numericInput,
  signedChange,
  validLoadPoint,
} from './dashboardAnalysisModel'
import { compareTrendMeasurements, trendMeasurement } from './releaseTrendModel'
import type {
  LoadComparisonPoint,
  ReleaseTrendPoint,
  RunArchiveReport,
  TestSummary,
} from '../types'

const stats = {
  transactions: 1000,
  errors: 10,
  error_pct: 1,
  avg_response_ms: 100,
  min_response_ms: 1,
  max_response_ms: 200,
  p95_ms: 180,
}
const point = {
  run_id: 'a',
  metric_population: 'transaction',
  transactions: 1000,
  error_pct: 1,
  avg_response_ms: 100,
} as ReleaseTrendPoint
const run = {
  id: 'a',
  application_id: 'app',
  project_id: 'p',
  scenario_id: 's',
  duration: '60s',
} as TestSummary
const report = {
  manifest: {
    provenance: {
      groups: [{ name: 'users', vusers: 10 }],
      runtime_settings: {},
    },
  },
  change_attribution: {
    scenario_revision: '1',
    script_revisions: ['1'],
    environment: 'qa',
    data_revision: '1',
  },
  completeness: { score_pct: 100 },
  overview: { peak_active_vusers: 10 },
  metric_populations: {
    transaction: { ...stats, average_rate_per_second: 10 },
  },
  by_transaction: { Checkout: stats },
} as unknown as RunArchiveReport

describe('dashboard comparison evidence', () => {
  it('never turns a missing denominator or percentile into zero', () => {
    const p = {
      transactions: 0,
      errors: 77,
      avg_response_ms: 17,
    } as LoadComparisonPoint
    expect(validLoadPoint(p)).toBe(false)
    expect(loadLatency(p, 'average_ms', 'all')).toBeUndefined()
    expect(
      loadLatency({ ...p, transactions: 100, errors: 0 }, 'p95_ms', 'all'),
    ).toBeUndefined()
    expect(numericInput('')).toBeUndefined()
    expect(numericInput('NaN')).toBeUndefined()
    expect(numericInput('0')).toBe(0)
    expect(signedChange(10, 0)).toContain('baseline is zero')
  })
  it('qualifies cross-release runs while keeping missing metadata unknown', () => {
    expect(
      comparisonEligibility(
        { ...run, release_id: 'new' },
        { ...run, release_id: 'old' },
        report,
        report,
      ).status,
    ).toBe('Comparable')
    expect(comparisonEligibility(run, run, null, null).status).toBe(
      'Unverified',
    )
    expect(
      comparisonEligibility(
        { ...run, scenario_id: 'other' },
        run,
        report,
        report,
      ).status,
    ).toBe('Not comparable')
  })
  it('compares stable workload settings, excluding execution timestamps and normalizing load for curves', () => {
    const changed = structuredClone(report)
    changed.manifest!.provenance!.groups![0].vusers = 20
    changed.manifest!.provenance!.groups![0].effective_end_at =
      '2026-09-20T12:00:00Z'
    expect(comparisonEligibility(run, run, changed, report, false).status).toBe(
      'Comparable',
    )
    expect(comparisonEligibility(run, run, changed, report).status).toBe(
      'Not comparable',
    )
    changed.manifest!.provenance!.groups![0].ramp_up = '2m'
    expect(comparisonEligibility(run, run, changed, report, false).status).toBe(
      'Not comparable',
    )
  })
  it('keeps null provenance and invalid observed load unverified', () => {
    const invalid = {...report, overview: {...report.overview, peak_active_vusers: -1}, change_attribution: {...report.change_attribution, environment: null}} as unknown as RunArchiveReport
    expect(comparisonEligibility(run, run, invalid, invalid).status).toBe('Unverified')
  })
  it('calculates selected-population deltas from the actual baseline, ignoring server deltas', () => {
    const base = trendMeasurement(
      point,
      report,
      'transaction',
      'Checkout',
      'p95_ms',
    )
    const currentReport = {
      ...report,
      by_transaction: {
        Checkout: { ...stats, p95_ms: 270, error_pct: 3, errors: 30 },
      },
    }
    const current = trendMeasurement(
      {
        ...point,
        run_id: 'b',
        deltas: { avg_response_ms_delta: 9999 } as ReleaseTrendPoint['deltas'],
      },
      currentReport,
      'transaction',
      'Checkout',
      'p95_ms',
    )
    const result = compareTrendMeasurements(
      current,
      base,
      { a: run, b: { ...run, id: 'b' } },
      { a: report, b: currentReport },
      { latencyPct: 10, errorPoints: 1 },
    )
    expect(result.latencyDelta).toBe(90)
    expect(result.errorDelta).toBe(2)
    expect(result.regressions).toHaveLength(2)
    expect(result.eligibility.status).toBe('Comparable')
  })
  it('keeps absence, baseline zero, and incompatible contexts distinct', () => {
    const a = trendMeasurement(point, report, 'transaction', '', 'p95_ms')
    const b = trendMeasurement(
      { ...point, run_id: 'b' },
      report,
      'action',
      '',
      'p95_ms',
    )
    expect(
      compareTrendMeasurements(b, a, {}, {}, { latencyPct: 10, errorPoints: 1 })
        .reason,
    ).toContain('baseline exists')
    expect(
      compareTrendMeasurements(
        a,
        undefined,
        {},
        {},
        { latencyPct: 10, errorPoints: 1 },
      ).reason,
    ).toContain('No baseline')
    const invalid = compareTrendMeasurements(
      { ...a, point: { ...point, run_id: 'b' } },
      a,
      { a: run, b: { ...run, id: 'b', scenario_id: 'different' } },
      { a: report, b: report },
      { latencyPct: 10, errorPoints: 1 },
    )
    expect(invalid.latencyDelta).toBeUndefined()
    expect(invalid.eligibility.status).toBe('Not comparable')
  })
  it('does not use request data as named transaction evidence', () => {
    const legacy = {
      ...report,
      metric_populations: undefined,
      by_action: { Checkout: stats },
      by_transaction: undefined,
    }
    expect(
      trendMeasurement(point, legacy, 'transaction', 'Checkout', 'p95_ms')
        .stats,
    ).toBeUndefined()
    expect(
      trendMeasurement(point, legacy, 'unknown', 'Checkout', 'p95_ms').latency,
    ).toBe(180)
  })
  it('uses the matching population duration for transaction successful throughput', () => {
    const selected = {
      ...report,
      by_transaction: {
        Checkout: { ...stats, transactions: 200, errors: 20, error_pct: 10 },
      },
    }
    expect(
      trendMeasurement(point, selected, 'transaction', 'Checkout', 'p95_ms')
        .successfulRate,
    ).toBe(1.8)
    expect(
      trendMeasurement(
        point,
        { ...selected, metric_populations: undefined },
        'transaction',
        'Checkout',
        'p95_ms',
      ).successfulRate,
    ).toBeUndefined()
  })
  it('exports exact context safely for spreadsheets', () => {
    const csv = analysisCSV([
      ['=HYPERLINK("bad")', 'a,b', '+formula', -3, 'line\nbreak'],
    ])
    expect(csv).toContain("'=HYPERLINK")
    expect(csv).toContain('"a,b"')
    expect(csv).toContain('"-3"')
    expect(csv).toContain("'+formula")
  })
})
