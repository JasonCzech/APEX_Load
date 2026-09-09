import { describe, expect, it } from 'vitest'
import { reportEvidenceWarnings } from './reportEvidenceWarnings'
import type { RunArchiveReport } from '../types'

const report = (overrides: Partial<RunArchiveReport> = {}) => ({
 manifest: { termination_reason: 'duration_reached', completed_at: '2026-09-09T10:05:00Z', provenance: { started_at: '2026-09-09T10:00:00Z', total_vusers: 10 } },
 overview: { transactions: 1000, peak_active_vusers: 10 },
 ...overrides,
} as RunArchiveReport)
describe('report evidence qualifications', () => {
 it('does not invent warnings for a sufficiently sampled, fully captured workload', () => {
  expect(reportEvidenceWarnings(report())).toEqual([])
 })
 it('retains critical telemetry findings even when there are no errors', () => {
  expect(reportEvidenceWarnings(report({ completeness: { score_pct: 85, status: 'needs_review', findings: [{ dimension: 'telemetry', severity: 'critical', penalty: 15, message: 'Telemetry gaps' }] } }))).toContain('Telemetry gaps')
 })
 it('qualifies missing timing, sparse data and incomplete observed load', () => {
  const value = report(); value.manifest.termination_reason = ''; value.overview.transactions = 9; value.overview.peak_active_vusers = 1
  expect(reportEvidenceWarnings(value)).toEqual(expect.arrayContaining([expect.stringContaining('termination'),expect.stringContaining('Only 9'),expect.stringContaining('1 of 10')]))
 })
})
