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
 it('warns that a suspect or invalid run is not acceptance evidence', () => {
  const warnings = reportEvidenceWarnings(report({ validity: { rules_version: 'validity.v1', verdict: 'invalid', reasons: ['gen-1 was OOM-killed'], windows: [], limitations: [] } }))
  expect(warnings).toEqual([expect.stringContaining('Run validity is invalid: gen-1 was OOM-killed')])
  expect(reportEvidenceWarnings(report({ validity: { rules_version: 'validity.v1', verdict: 'valid', reasons: [], windows: [], limitations: [] } }))).toEqual([])
 })
 it('carries load generator UDP drop warnings, never shared-namespace counts', () => {
  const warnings = reportEvidenceWarnings(report({ host_network: [
   { source: 'gen-0', warning: 'UDP packets were dropped on gen-0 during the run.' },
   { source: 'controller', udp_drops_delta: { receive_buffer_errors: 40, send_buffer_errors: 0, in_errors: 0 } },
  ] }))
  expect(warnings).toEqual(['UDP packets were dropped on gen-0 during the run.'])
 })
 it('warns about failed exact HTTP/3 preflight checks of a run that started', () => {
  const results = [
   { source: 'gen-a', group: 'g', origin: 'https://api.example', policy: '3', ok: false, error_class: 'quic_handshake_timeout' },
   { source: 'gen-a', group: 'g', origin: 'https://cdn.example', policy: 'auto+h3', ok: false, error_class: 'quic_handshake_timeout' },
   { source: 'gen-a', group: 'g', origin: 'https://{{param.host}}', policy: '3', ok: false, skipped: 'dynamic target' },
   { source: 'gen-b', group: 'g', origin: 'https://shop.example', policy: '3', ok: true },
  ]
  expect(reportEvidenceWarnings(report({ http3_preflight: { policy: 'report', results } }))).toEqual(['1 exact HTTP/3 origin check failed before the run started; those requests cannot fall back to TCP.'])
  expect(reportEvidenceWarnings(report({ http3_preflight: { policy: 'block', blocked: true, results } }))).toEqual([])
 })
})
