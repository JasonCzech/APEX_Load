import { describe, expect, it } from 'vitest'
import type { RunArchiveReport } from '../../types'
import { defaultReportPopulation, reportPopulationActions, formatReportPercent, reportAcceptance, reportPopulationOverview } from './reportPresentation'
import { buildBaselineOverviewDiff, effectiveReportVerdict, alignBaselineTimeline } from './runReportModel'
import type { MetricsHistoryPoint } from '../../types'

const report = {
 overview: { transactions: 1000, p95_ms: 800, avg_response_ms: 500, peak_tps: 100 },
 metric_populations: { action: { transactions: 90, errors: 1, error_pct: 1, avg_response_ms: 100 }, transaction: { transactions: 0, error_pct: 0, avg_response_ms: 0, p95_ms: 0 } },
 verdict: { status: 'Not assessed', failed: false, conditional: true, explanation: 'No criteria configured.' },
} as unknown as RunArchiveReport

describe('report presentation', () => {
 it('does not mix same-name populations or relabel legacy records', () => {
  const mixed = { ...report, by_request: { Shared: { transactions: 10 } }, by_transaction: { Shared: { transactions: 3 } }, by_unknown: { Shared: { transactions: 2 } } } as unknown as RunArchiveReport
  expect(reportPopulationActions(mixed, 'action').Shared.transactions).toBe(10)
  expect(reportPopulationActions(mixed, 'transaction').Shared.transactions).toBe(3)
  expect(reportPopulationActions(mixed, 'unknown').Shared.transactions).toBe(2)
  const legacy = { by_action: { Shared: { transactions: 15 } } } as unknown as RunArchiveReport
  expect(defaultReportPopulation(legacy)).toBe('unknown')
  expect(reportPopulationActions(legacy, 'unknown').Shared.transactions).toBe(15)
  expect(reportPopulationActions(legacy, 'action')).toEqual({})
  expect(reportPopulationActions(null, 'transaction')).toEqual({})
 })
 it('keeps measured action metrics separate from mixed archive totals and missing percentiles', () => {
  expect(defaultReportPopulation(report)).toBe('action')
  expect(reportPopulationOverview(report, 'action')?.avg_response_ms).toBe(100)
  expect(reportPopulationOverview(report, 'action')?.p95_ms).toBeUndefined()
  expect(reportPopulationOverview(report, 'transaction')).toBeUndefined()
  expect(buildBaselineOverviewDiff(reportPopulationOverview(report, 'transaction'), report.overview)).toEqual({})
 })
 it('does not substitute analyst signoff or an absent SLA breach for acceptance', () => {
  expect(reportAcceptance(null).status).toBe('Not available')
  expect(effectiveReportVerdict(report, { verdict_override: 'Pass' } as never).status).toBe('Not assessed')
 })
 it.each([[0, '0.00%'], [0.0001, '<0.01%'], [0.01, '0.01%'], [undefined, '—']])('formats %s without hiding nonzero errors', (value, expected) => {
  expect(formatReportPercent(value as number | undefined)).toBe(expected)
 })
 it('aligns separate dates by elapsed time and stops at the end of baseline coverage', () => {
  const current = [100000, 101000, 102000].map(timestampMs => ({ timestampMs } as MetricsHistoryPoint))
  const baseline = [2000, 3000].map((timestampMs, i) => ({ timestampMs, tps: i + 10, p95: i + 20 } as MetricsHistoryPoint))
  expect(alignBaselineTimeline(current, baseline).map(point => point.baselineTps)).toEqual([10, 11, undefined])
 })
})
