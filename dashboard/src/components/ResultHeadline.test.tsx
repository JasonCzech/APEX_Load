// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ResultHeadline from './ResultHeadline'
const api = vi.hoisted(() => ({ getTestArchiveReport: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })
describe('ResultHeadline', () => {
 it('waits for visibility and displays authoritative population metrics', async () => {
  let visible!: IntersectionObserverCallback
  vi.stubGlobal('IntersectionObserver', class { constructor(callback: IntersectionObserverCallback) { visible = callback } observe() {} disconnect() {} })
  api.getTestArchiveReport.mockResolvedValue({ verdict: { status: 'Not assessed' }, overview: { p95_ms: 999 }, metric_populations: { action: { transactions: 100, p95_ms: 120, error_pct: 0.001 } } })
  const div = document.createElement('div'); const root = createRoot(div)
  try {
   await act(async () => root.render(<ResultHeadline testId="run" />))
   expect(api.getTestArchiveReport).not.toHaveBeenCalled()
   await act(async () => { visible([{ isIntersecting: true }] as IntersectionObserverEntry[], {} as IntersectionObserver) })
   expect(api.getTestArchiveReport).toHaveBeenCalledWith('run', { includeReleaseTrend: false, timeoutMs: 30000 })
   expect(div.textContent).toContain('Not assessed')
   expect(div.textContent).toContain('P95 120 ms')
   expect(div.textContent).toContain('<0.01%')
   expect(div.textContent).not.toContain('999')
  } finally { act(() => root.unmount()) }
 })
})
