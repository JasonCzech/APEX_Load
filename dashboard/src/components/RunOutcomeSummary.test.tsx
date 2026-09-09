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
