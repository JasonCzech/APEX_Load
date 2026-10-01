// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import DependencyEvidence from './DependencyEvidence'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('shows explicit dependency associations and selects monitor evidence', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { diagnostics_enabled: true } : { timestamp: '2026-01-01T00:00:00Z', interval_seconds: 1, coverage: 'observed', graph: { nodes: [{ id: 'rpc', label: 'orders', kind: 'dependency', dependency_type: 'grpc', p95_ms: 25 }] }, monitors: [{ source_id: 'jvm', name: 'JVM', dependency: 'orders', state: 'collecting', coverage: 'observed', metrics: ['jvm.heap.used'] }] })
  const node = document.createElement('div'); const root = createRoot(node); const select = vi.fn()
  try {
    await act(async () => root.render(<DependencyEvidence projectId="p" testId="run" select={select} />))
    expect(node.textContent).toContain('orders (grpc)')
    expect(node.textContent).toContain('JVM: observed')
    const button = [...node.querySelectorAll('button')].find(b => b.textContent === 'Inspect jvm.heap.used')!
    await act(async () => button.click())
    expect(select).toHaveBeenCalledWith('jvm', 'jvm.heap.used', '2025-12-31T23:59:59.000Z', '2026-01-01T00:00:00Z')
  } finally { act(() => root.unmount()) }
})

it('refreshes latest dependency evidence while mounted', async () => {
  vi.useFakeTimers(); request.mockClear()
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { diagnostics_enabled: true } : { timestamp: '2026-01-01T00:00:00Z', interval_seconds: 1, coverage: 'missing', monitors: [] })
  const node = document.createElement('div'); const root = createRoot(node)
  try {
    await act(async () => root.render(<DependencyEvidence projectId="p" testId="run" select={() => {}} />))
    expect(request).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(request).toHaveBeenCalledTimes(3)
  } finally { act(() => root.unmount()); vi.useRealTimers() }
})
