// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ElasticRunEvidence from './ElasticRunEvidence'
import type { RunMonitorJob } from '../types/runMonitoring'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
const job: RunMonitorJob = { id: 'run:source', source: { id: 'source', name: 'Elastic logs', revision: 3, collector_id: 'collector', pack: 'elastic', executor_mode: 'collector', interval_seconds: 10, enabled: true, elastic: { indices: ['logs-*'], logs: true } }, state: 'reconciling', next_sequence: 3, query_watermark: '2026-10-05T00:00:00Z', coverage: { logs: { status: 'truncated', reason: 'record_limit', truncated: true } } }
beforeEach(() => { request.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('uses canonical evidence pagination and keeps per-query coverage visible', async () => {
  request.mockResolvedValueOnce({ evidence: [{ id: 'log', source_id: 'source', provider: 'elasticsearch', kind: 'log', log: { message: 'Scrubbed log' }, correlation: 'run_id' }], has_more: true, after_key: 'cursor-1' }).mockResolvedValueOnce({ evidence: [], has_more: false, after_key: 'cursor-2' })
  await act(async () => root.render(<ElasticRunEvidence testId="run" jobs={[job]} sourceId="source" />))
  expect(container.textContent).toContain('source revision 3 · collector')
  expect(container.textContent).toContain('record_limit')
  expect(container.textContent).toContain('Scrubbed log')
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Next Elastic evidence page')!.click())
  expect(request.mock.calls[1][0]).toContain('after_key=cursor-1')
  expect(request.mock.calls[1][0]).toContain('source_id=source')
  expect(container.textContent).toContain('No retained matching Elastic or OpenSearch records.')
  expect(container.textContent).toContain('Missing evidence is not zero activity.')
})
it('passes signal and correlation filters to the bounded canonical API', async () => {
  request.mockResolvedValue({ evidence: [], has_more: false, after_key: '' })
  await act(async () => root.render(<ElasticRunEvidence testId="run" jobs={[job]} />))
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent === 'Elastic evidence trace id')!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'trace-id'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh Elastic evidence')!.click())
  expect(request.mock.calls[request.mock.calls.length - 1][0]).toContain('trace_id=trace-id')
})

it('rejects invalid time drafts before applying them to the canonical evidence page', async () => {
  request.mockResolvedValue({ evidence: [], has_more: false, after_key: '' })
  await act(async () => root.render(<ElasticRunEvidence testId="run" jobs={[job]} />))
  const calls = request.mock.calls.length
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent === 'Elastic evidence from')!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '2026-02-30T10:00:00Z'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh Elastic evidence')!.click())
  expect(request).toHaveBeenCalledTimes(calls)
  expect(container.textContent).toContain('valid UTC RFC3339')
})

it('filters before canonical pagination and resets the cursor when switching Elastic evidence sources', async () => {
  request.mockResolvedValue({ evidence: [], has_more: true, after_key: 'page-one' })
  const other = { ...job, id: 'run:other', source: { ...job.source, id: 'other', name: 'Other Elastic source' } }
  const unrelated = { ...job, id: 'run:appd', source: { ...job.source, id: 'appd', pack: 'appdynamics' } }
  await act(async () => root.render(<ElasticRunEvidence testId="run" jobs={[unrelated, job, other]} />))
  expect(request.mock.calls[0][0]).toContain('source_id=source')
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Next Elastic evidence page')!.click())
  const select = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Elastic evidence source'))!.querySelector('select')!
  await act(async () => { select.value = 'other'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  const last = request.mock.calls[request.mock.calls.length - 1][0]
  expect(last).toContain('source_id=other')
  expect(last).not.toContain('after_key=page-one')
})
