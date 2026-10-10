// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import type { RunArchiveReport } from '../types'
import RunOutcomeSummary from './RunOutcomeSummary'

it('separates early termination, unevaluated acceptance, and complete captured evidence', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const report = {
    manifest: {termination_reason:'operator_stopped', completed_at:'2026-09-06T00:00:38Z', provenance:{duration:'5m',started_at:'2026-09-06T00:00:00Z'}},
    overview:{transactions:506}, completeness:{score_pct:100},
    verdict:{status:'Not assessed',explanation:'No acceptance criteria were configured.'},
    metric_populations:{action:{transactions:253,errors:0,p95_ms:87},transaction:{transactions:253,errors:0,p95_ms:87},unknown:{transactions:0,errors:0}},
  } as unknown as RunArchiveReport
  act(() => root.render(<RunOutcomeSummary report={report} />))
  expect(container.textContent).toContain('Stopped by operator')
  expect(container.textContent).toContain('Planned 5m · elapsed 38s')
  expect(container.textContent).toContain('Not assessed')
  expect(container.textContent).toContain('Archive records506')
  expect(container.textContent).not.toContain('Pass')
  act(() => root.render(<RunOutcomeSummary report={{...report, manifest:{...report.manifest, termination_reason:undefined}, metric_populations:undefined, verdict:undefined}} />))
  expect(container.textContent).toContain('Unknown termination reason')
  expect(container.textContent).toContain('Requests / actionsUnknown')
  expect(container.textContent).toContain('Acceptance assessmentUnknown')
  act(() => root.unmount())
})

it('renders missing legacy metadata and null rates without crashing or inventing a rate', () => {
 const container = document.createElement('div')
 const root = createRoot(container)
 const report = { metric_populations: { action: { transactions: 1, errors: 0, average_rate_per_second: null } } } as unknown as RunArchiveReport
 act(() => root.render(<RunOutcomeSummary report={report} />))
 expect(container.textContent).toContain('Elapsed-run average unavailable')
 expect(container.textContent).toContain('Unknown termination reason')
 act(() => root.unmount())
})

it('uses the shared computed assessment instead of a contradictory legacy verdict', () => {
 const container = document.createElement('div')
 const root = createRoot(container)
 const report = { manifest: { test_id: 'run-1' }, verdict: { status: 'Pass' }, outcome: {
   schema_version: 1, execution_id: 'run-1', observed_at: '2026-09-09T12:00:00Z',
   assessment: { status: 'not_assessed', explanation: 'No criteria configured', reason_code: 'no_criteria', criteria_revision: null },
   execution: { termination_reason: 'completed', planned_duration_seconds: null, observed_duration_seconds: null },
   evidence: { status: 'unavailable', coverage_percent: null },
 } } as unknown as RunArchiveReport
 act(() => root.render(<RunOutcomeSummary report={report} />))
 expect(container.textContent).toContain('Acceptance assessmentNot assessed')
 expect(container.textContent).toContain('No criteria configured')
 expect(container.textContent).not.toContain('Acceptance assessmentPass')
 act(() => root.unmount())
})

it('ignores computed outcomes when the manifest cannot establish their execution identity', () => {
 const container = document.createElement('div')
 const root = createRoot(container)
 for (const manifest of [undefined, {}]) {
   const report = { manifest, outcome: { schema_version: 1, execution_id: undefined, assessment: { status: 'passed' } } } as unknown as RunArchiveReport
   act(() => root.render(<RunOutcomeSummary report={report} />))
   expect(container.textContent).toContain('Acceptance assessmentUnknown')
   expect(container.textContent).toContain('Unknown termination reason')
 }
 act(() => root.unmount())
})

it('states the run validity and root cause, and opens it with the cell action', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const base = {
    manifest: { termination_reason: 'duration_reached', completed_at: '2026-09-06T00:05:00Z', provenance: { duration: '5m', started_at: '2026-09-06T00:00:00Z' } },
    overview: { transactions: 506 }, completeness: { score_pct: 100 }, verdict: { status: 'Pass', explanation: '' },
    validity: { rules_version: 'validity.v1', verdict: 'suspect', reasons: ['gen-1 CPU at or above 85% for 3 intervals'], windows: [], limitations: [] },
    root_cause_summary: { state: 'completed', presented: true, analysis_enabled: true, analysis_id: 'a-1', outcome: 'supported', top_hypothesis: { rank: 1, title: 'Pool exhaustion', confidence: 'high' }, hypothesis_count: 1, anomaly_count: 3 },
  } as unknown as RunArchiveReport
  const opened: string[] = []
  act(() => root.render(<RunOutcomeSummary report={base} onRootCause={action => opened.push(action)} />))
  const cell = container.querySelector('.run-outcome-root-cause')
  expect(cell?.textContent).toContain('Suspect run')
  expect(cell?.textContent).toContain('#1 Pool exhaustion (high confidence)')
  act(() => (Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Investigate root cause') as HTMLButtonElement).click())
  expect(opened).toEqual(['investigate'])
  // A newer copy of the summary wins over the report's.
  act(() => root.render(<RunOutcomeSummary report={base} rootCauseSummary={{ state: 'running', presented: true, analysis_enabled: true, hypothesis_count: 0, anomaly_count: 0 }} />))
  expect(container.querySelector('.run-outcome-root-cause')?.textContent).toContain('Analysis running')
  expect(container.querySelector('.run-outcome-root-cause button')).toBeNull()
  // A project without root cause shows only the judged validity.
  act(() => root.render(<RunOutcomeSummary report={{ ...base, root_cause_summary: { state: 'not_enabled', presented: false, analysis_enabled: false, hypothesis_count: 0, anomaly_count: 0 } } as RunArchiveReport} />))
  expect(container.querySelector('.run-outcome-root-cause')?.textContent).toContain('Suspect run')
  expect(container.querySelector('.run-outcome-root-cause')?.textContent).not.toContain('not enabled')
  // The cell then holds validity alone and is named for it.
  expect(container.querySelector('.run-outcome-root-cause dt')?.textContent).toBe('Run validity')
  act(() => root.render(<RunOutcomeSummary report={{ ...base, validity: undefined, root_cause_summary: { state: 'not_enabled', presented: false, analysis_enabled: false, hypothesis_count: 0, anomaly_count: 0 } } as unknown as RunArchiveReport} />))
  expect(container.querySelector('.run-outcome-root-cause')).toBeNull()
  // The exported report's preview has its own root-cause section.
  act(() => root.render(<RunOutcomeSummary report={base} showRootCause={false} />))
  expect(container.querySelector('.run-outcome-root-cause')).toBeNull()
  act(() => root.unmount())
})

it('leads with the HTTP version mix only when HTTP/3 was in play', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const mix = {
    actions: 40, by_version: { '2': 10, '3': 30 }, http3_policies: 40, upgrade_eligible: 36, fell_back: 6, quic_attempted: 36, quic_failed: 6,
    fallback_ratio: { numerator: 6, denominator: 36, value: 6 / 36 }, quic_failure_ratio: { numerator: 6, denominator: 36, value: 6 / 36 },
    finding: { breaches: [], summary: 'above' },
  }
  const report = { network_phases: { version_mix: mix } } as unknown as RunArchiveReport
  act(() => root.render(<RunOutcomeSummary report={report} />))
  expect(container.textContent).toContain('HTTP versionsHTTP/2 25% · HTTP/3 75%')
  expect(container.textContent).toContain('Fell back to TCP: 16.67% (6 of 36 upgrade-eligible auto+h3 actions)')
  expect(container.textContent).toContain('Protocol Mix: above the expected ratio. See Diagnostics.')
  const tcpOnly = { actions: 10, by_version: { '2': 10 }, fallback_ratio: { numerator: 0, denominator: 0, value: 0 }, quic_failure_ratio: { numerator: 0, denominator: 0, value: 0 } }
  act(() => root.render(<RunOutcomeSummary report={{ network_phases: { version_mix: tcpOnly } } as unknown as RunArchiveReport} />))
  expect(container.textContent).not.toContain('HTTP versions')
  act(() => root.unmount())
})
