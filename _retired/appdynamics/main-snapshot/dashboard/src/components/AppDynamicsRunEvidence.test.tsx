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
it('renders Dynatrace reconciliation, problem resolution and explicit correlation through the canonical evidence UI', async () => {
  request.mockResolvedValue({ evidence: [{ id: 'problem-1', kind: 'problem', status: 'CLOSED', summary: 'Recovered service', entity_ids: { service: 'SERVICE-0123456789ABCDEF' }, properties: { correlation: 'entity_time' }, deep_link: 'https://tenant.live.dynatrace.com/#problems/problem-1' }], has_more: false })
  await act(async () => root.render(<AppDynamicsRunEvidence testId="run" jobs={[{ ...job, source: { ...job.source, pack: 'dynatrace' } }]} />))
  expect(container.textContent).toContain('Load finished; collecting delayed Dynatrace data')
  expect(container.textContent).toContain('Recovered service'); expect(container.textContent).toContain('CLOSED'); expect(container.textContent).toContain('entity_time')
  expect(container.querySelector('a')?.textContent).toBe('Open in Dynatrace')
  const options = [...container.querySelectorAll('option')].map(option => option.value)
  expect(options).toContain('span'); expect(options).not.toContain('snapshot')
})
it('labels sampled trace summaries with trace identity independently from request GUIDs', async () => {
  request.mockResolvedValue({ evidence: [{ id: 'trace-1', kind: 'trace', status: 'ERROR', summary: 'Checkout span', properties: { correlation: 'run_header', duration_ms: '18', evidence_scope: 'sampled_span', trace_id: '0123456789abcdef0123456789abcdef', span_id: '0123456789abcdef' } }], has_more: false })
  await act(async () => root.render(<AppDynamicsRunEvidence testId="run" jobs={[{ ...job, source: { ...job.source, pack: 'dynatrace' } }]} />))
  expect(container.textContent).toContain('18 ms')
  expect(container.textContent).toContain('Sampled span summary')
  expect(container.textContent).toContain('Trace ID: 0123456789abcdef0123456789abcdef')
  expect(container.textContent).toContain('Span ID: 0123456789abcdef')
  expect(container.textContent).not.toContain('Request GUID:')
  expect([...container.querySelectorAll('option')].map(option => option.value)).toContain('trace')
})
