import { describe, expect, it } from 'vitest'
import type { RunArchiveReport, TestSummary } from '../types'
import { reportComparisonQualification } from './reportComparisonQualification'
const test = { scenario_id: 's', project_id: 'p', application_id: 'a', release_id: 'r', total_vusers: 10, duration: '5m' } as TestSummary
const report = { manifest: {}, overview: { transactions: 1000, peak_active_vusers: 10 }, metric_populations: { action: { transactions: 800 }, transaction: { transactions: 2 } } } as unknown as RunArchiveReport

describe('baseline qualification', () => {
  it('does not mistake absent baseline metadata for a match', () => {
    const result = reportComparisonQualification(report, report, test, null, 'action')
    expect(result.summary).toContain('cannot be fully verified')
    expect(result.missing).toContain('Scenario')
  })
  it('uses the selected metric population for sample qualifications', () => {
    const result = reportComparisonQualification(report, report, test, test, 'transaction')
    expect(result.currentRecords).toBe(2)
    expect(result.limitedSamples).toBe(true)
  })
  it('checks captured revisions when available and leaves missing revisions unverified', () => {
    const captured = { ...report, change_attribution: { script_revisions: ['b', 'a'], scenario_revision: 'r1', runtime_overrides: false } }
    expect(reportComparisonQualification(captured, { ...captured, change_attribution: { ...captured.change_attribution, script_revisions: ['a', 'b'] } }, test, test, 'action').revisions).toContain('revisions match')
    expect(reportComparisonQualification(captured, { ...captured, change_attribution: { ...captured.change_attribution, scenario_revision: 'r2' } }, test, test, 'action').revisions).toContain('revisions differ')
    expect(reportComparisonQualification(captured, report, test, test, 'action').revisions).toContain('unverified')
  })
  it.each([undefined, null, Number.NaN, -1])('does not report missing or invalid load (%s) as a measured difference', peak => {
    const baseline = { ...report, overview: { ...report.overview, peak_active_vusers: peak } } as unknown as RunArchiveReport
    const result = reportComparisonQualification(report, baseline, test, test, 'action')
    expect(result.observedLoadUnknown).toBe(true)
    expect(result.differentObservedLoad).toBe(false)
  })
  it('distinguishes measured zero load from absent load', () => {
    const baseline = { ...report, overview: { ...report.overview, peak_active_vusers: 0 } }
    const result = reportComparisonQualification(report, baseline, test, test, 'action')
    expect(result.observedLoadUnknown).toBe(false)
    expect(result.differentObservedLoad).toBe(true)
  })
  it('normalizes duration and checks release scope', () => {
    expect(reportComparisonQualification(report, report, test, { ...test, duration: '300s' }, 'action').summary).toBe('Recorded planned context matches.')
    expect(reportComparisonQualification(report, report, test, { ...test, release_id: 'other' }, 'action').summary).toContain('release')
  })
})
