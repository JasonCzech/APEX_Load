// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import RunManagementEvidence from './RunManagementEvidence'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const observed = (value: string) => ({ state: 'observed', value })
const absent = { state: 'missing', reason: 'not_observed' }
function managementPage(unavailable = false) {
  return { version: 1, records: [{ job_id: 'job', sequence: '0', received_at: '2026-10-03T00:00:01Z', evidence: {
    version: 2, points: [], messaging_management: {
      version: 1, kind: 'kafka_offsets', provider: 'broker', source_id: 'source', source_revision: 1, generation: '9007199254740993', poll_sequence: '1', poll_id: 'poll', captured_at: '2026-10-03T00:00:00Z', expected_entities: 1, interval_seconds: 10, missed_ticks: '0', inventory_complete: !unavailable, incomplete: unavailable,
      ...(unavailable ? { unavailable_reason: 'permission_denied' } : {}), rows: unavailable ? [] : [{ entity: { kafka: { topic: 'orders', group: 'workers', partition: 0 } }, state: 'observed', observed_at: '2026-10-03T00:00:00Z', kafka_offsets: { committed_next: observed('0'), high_watermark_next: observed('9007199254740993'), last_stable_next: absent, log_end_next: { state: 'unsupported', reason: 'unsupported' }, leader_epoch: observed('0'), committed_leader_epoch: { state: 'stale', reason: 'stale' } } }],
    },
  } }], has_more: false }
}
async function readPage() {
  await act(async () => root.render(<RunManagementEvidence projectId="project" testId="run" />))
  await act(async () => container.querySelector('button')!.click())
}
it('renders exact zero, large counts, absent and stale values independently', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { messaging_management_version: 1, native_parquet: false, messaging_management_enabled: false } : managementPage())
  await readPage()
  const rows = container.querySelectorAll('tbody tr')
  expect(rows[0].querySelector('td')?.textContent).toBe('0')
  expect(rows[1].querySelector('td')?.textContent).toBe('9007199254740993')
  expect(rows[2].querySelector('td')?.textContent).toBe('—')
  expect(rows[5].textContent).toContain('stale')
  expect(container.textContent).toContain('generation 9007199254740993')
  expect(container.textContent).toContain('source age unqualified')
  expect(request).toHaveBeenCalledWith('/tests/run/monitor-management?limit=10', expect.objectContaining({ signal: expect.any(AbortSignal) }))
})
it('retains denied inventory without manufacturing table rows or zero', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { messaging_management_version: 1 } : managementPage(true))
  await readPage()
  expect(container.textContent).toContain('Incomplete coverage: 0 / 1 entities')
  expect(container.textContent).toContain('permission_denied')
  expect(container.querySelector('table')).toBeNull()
})
it('pages with an opaque cursor and resets the selection without stale replies', async () => {
  let resolvePending: (value: unknown) => void = () => {}
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { messaging_management_version: 1 }
    if (path.includes('cursor=')) return new Promise(resolve => { resolvePending = resolve })
    return { ...managementPage(), has_more: true, cursor: 'opaque+cursor' }
  })
  await readPage()
  await act(async () => (Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Next management page'))!.click())
  expect(request).toHaveBeenCalledWith('/tests/run/monitor-management?limit=10&cursor=opaque%2Bcursor', expect.anything())
  await act(async () => root.render(<RunManagementEvidence projectId="other" testId="other-run" />))
  await act(async () => resolvePending(managementPage()))
  expect(container.textContent).not.toContain('9007199254740993')
})
it('keeps unsupported peers explicit and reports bounded read errors', async () => {
  request.mockResolvedValue({ messaging_management_version: 0 })
  await act(async () => root.render(<RunManagementEvidence projectId="project" testId="run" />))
  expect(container.textContent).toBe('')
  request.mockImplementation(async (path: string) => { if (path.includes('capabilities')) return { messaging_management_version: 1 }; throw new Error('management evidence unavailable') })
  await act(async () => root.render(<RunManagementEvidence projectId="other" testId="run" />))
  await act(async () => container.querySelector('button')!.click())
  expect(container.querySelector('[role=alert]')?.textContent).toContain('management evidence unavailable')
})
