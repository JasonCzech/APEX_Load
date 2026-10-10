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
 it('shows a judged validity and the root cause beside acceptance', async () => {
  let visible!: IntersectionObserverCallback
  vi.stubGlobal('IntersectionObserver', class { constructor(callback: IntersectionObserverCallback) { visible = callback } observe() {} disconnect() {} })
  const render = async (report: Record<string, unknown>) => {
   api.getTestArchiveReport.mockResolvedValue({ verdict: { status: 'Passed' }, metric_populations: { action: { transactions: 100, p95_ms: 120, error_pct: 0 } }, ...report })
   const div = document.createElement('div'); const root = createRoot(div)
   await act(async () => root.render(<ResultHeadline testId="run" />))
   await act(async () => { visible([{ isIntersecting: true }] as IntersectionObserverEntry[], {} as IntersectionObserver) })
   const text = div.textContent || ''
   const badge = div.querySelector('.rca-badge')?.className || ''
   act(() => root.unmount())
   return { text, badge }
  }
  const supported = await render({ validity: { verdict: 'suspect' }, root_cause_summary: { state: 'completed', presented: true, analysis_enabled: true, outcome: 'supported', top_hypothesis: { rank: 1, title: 'Pool exhaustion', confidence: 'high' }, hypothesis_count: 1, anomaly_count: 3 } })
  expect(supported.text).toContain('Suspect run')
  expect(supported.badge).toContain('rca-badge-warning')
  expect(supported.text).toContain('Root cause: #1 Pool exhaustion (high confidence)')
  const queued = await render({ validity: { verdict: 'unknown' }, root_cause_summary: { state: 'queued', presented: true, analysis_enabled: true, hypothesis_count: 0, anomaly_count: 0 } })
  expect(queued.text).toContain('Root cause: Analysis queued')
  expect(queued.text).not.toContain('Validity unknown')
  const hidden = await render({ root_cause_summary: { state: 'not_enabled', presented: false, analysis_enabled: false, hypothesis_count: 0, anomaly_count: 0 } })
  expect(hidden.text).not.toContain('Root cause')
 })
})
