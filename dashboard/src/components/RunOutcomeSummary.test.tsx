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
