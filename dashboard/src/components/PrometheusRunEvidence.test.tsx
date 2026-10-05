// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import PrometheusRunEvidence, { PrometheusDiagnosticTable } from './PrometheusRunEvidence'
import type { RunMonitorJob } from '../types/appdynamics'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('distinguishes reconciliation, API coverage, source freshness and warnings', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  const job: RunMonitorJob = { id: 'r:s', source: { id: 's', name: 'Target CPU', pack: 'prometheus-query', revision: 1, collector_id: '', interval_seconds: 30, enabled: true }, state: 'reconciling', next_sequence: 2, coverage: { cpu: { status: 'partial', freshness: 'unknown', expected_points: 10, points: 8, warnings: ['partial response'], infos: ['native histogram omitted'] } } }
  await act(async () => root.render(<PrometheusRunEvidence jobs={[job]} />))
  expect(container.textContent).toContain('collecting delayed Prometheus data'); expect(container.textContent).toContain('8/10 evaluation slots'); expect(container.textContent).toContain('freshness unknown'); expect(container.textContent).toContain('partial response'); expect(container.textContent).toContain('native histogram omitted')
  act(() => root.unmount())
})
it('shows observed sightings without inventing a resolution time or displaying malformed properties', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  const first = '2026-10-03T12:00:00Z'; const last = '2026-10-03T12:00:30Z'
  await act(async () => root.render(<PrometheusDiagnosticTable evidence={[
    { id: 'alert', kind: 'alert', status: 'no_longer_observed', timestamp_utc: '2026-10-03T12:01:00Z', properties: { first_observed_utc: first, last_observed_utc: last, state_observed_utc: '2026-10-03T12:01:00Z' } },
    { id: 'target', kind: 'target', status: 'down', properties: { first_observed_utc: 'unsafe arbitrary value', last_observed_utc: '2026-10-03T12:00:30+00:00' } },
  ]} />))
  expect(container.textContent).toContain('First seen'); expect(container.textContent).toContain('Last seen')
  expect(container.textContent).toContain(first); expect(container.textContent).toContain(last); expect(container.textContent).toContain('no_longer_observed')
  expect(container.textContent).toContain('does not establish historical resolution')
  expect(container.textContent).not.toContain('unsafe arbitrary value'); expect(container.textContent).not.toContain('+00:00')
  const rows = container.querySelectorAll('tbody tr')
  expect(rows[0].querySelectorAll('td')[3].textContent).toBe(first); expect(rows[0].querySelectorAll('td')[4].textContent).toBe(last)
  expect(rows[1].querySelectorAll('td')[3].textContent).toBe('—'); expect(rows[1].querySelectorAll('td')[4].textContent).toBe('—')
  act(() => root.unmount())
})
