// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppDynamicsRunEvidence from './AppDynamicsRunEvidence'
import { monitorJobState } from './appDynamicsConnectionModel'
import type { RunMonitorJob } from '../types/appdynamics'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
const job: RunMonitorJob = { id: 'run:source', source: { id: 'source', name: 'APM', revision: 1, collector_id: '', pack: 'appdynamics', interval_seconds: 60, enabled: true }, state: 'reconciling', next_sequence: 3, drain_deadline: '2026-10-03T20:05:00Z', coverage: { metrics: { status: 'partial', message: 'Late source bucket' } } }
beforeEach(() => { request.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('shows drainage and partial capability evidence without claiming completeness', async () => {
  request.mockResolvedValue({ evidence: [{ id: '1', kind: 'health_rule_violation', severity: 'ERROR', summary: 'Checkout latency', entity_ids: { tier: '5' }, timestamp_utc: '2026-10-03T20:00:00Z' }], has_more: false, after_key: 'key-1' })
  await act(async () => root.render(<AppDynamicsRunEvidence testId="run" jobs={[job]} />))
  expect(container.textContent).toContain('Load finished; collecting delayed AppDynamics data')
  expect(container.textContent).toContain('Final collection deadline:')
  expect(container.textContent).toContain('Late source bucket'); expect(container.textContent).toContain('Checkout latency'); expect(container.textContent).toContain('tier: 5')
  expect(container.textContent).toContain('Required incomplete capture makes the test inconclusive.')
})
it('retains pagination and reports missing evidence separately from zero errors', async () => {
  request.mockResolvedValueOnce({ evidence: [], has_more: true, after_key: 'key-9' }).mockResolvedValueOnce({ evidence: [], has_more: false, after_key: 'key-10' })
  await act(async () => root.render(<AppDynamicsRunEvidence testId="run" jobs={[job]} sourceId="source" />))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Next evidence page')!.click())
  expect(request.mock.calls[1][0]).toContain('after_key=key-9'); expect(request.mock.calls[1][0]).toContain('source_id=source')
  expect(container.textContent).toContain('No retained matching AppDynamics evidence.')
  expect(monitorJobState({ ...job, state: 'incomplete' })).toBe('Collection incomplete')
})
