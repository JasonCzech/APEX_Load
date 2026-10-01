// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import NativeEvidencePanel from './NativeEvidencePanel'
import { nativeEvidencePlotPoints } from './nativeEvidenceModel'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('./ExportableChart', () => ({ default: ({ children }: { children: React.ReactNode }) => <div>{children}</div> }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
const panel: DashboardDefinitionPanel = { id: 'p', title: 'Actions', kind: 'table', x: 0, y: 0, w: 24, h: 8, query: { evidence_mode: 'archive', metric: 'response_time', aggregation: 'p95', population: 'action', dimensions: ['action'], max_series: 10, end_seconds: 20, bucket_seconds: 10 } }
const result = { mode: 'archive', start: '2026-10-01T00:00:00Z', warnings: ['Coverage incomplete'], series: [{ name: 'Checkout', unit: 'ms', points: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 2, value: 30 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 0, value: null }] }], overlay: [{ timestamp_utc: '2026-10-01T00:00:00Z', unit: '%', quality: 'stale', value: 70 }], correlation: { Checkout: [{ lag_seconds: 0, pairs: 0, coverage: 0 }] } }
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers() })
it('preserves gaps, units, quality and unscored correlation', async () => {
  request.mockResolvedValue(result)
  await act(async () => root.render(<NativeEvidencePanel panel={panel} testId="run" />))
  expect(container.textContent).toContain('Checkout (ms)')
  expect(container.textContent).toContain('Overlay (%)')
  expect(container.textContent).toContain('Missing')
  expect(container.textContent).not.toContain('70')
  expect(container.textContent).toContain('Unscored')
  expect(container.textContent).toContain('Coverage incomplete')
})
it('polls live selections without overlapping requests and cancels on unmount', async () => {
  vi.useFakeTimers(); request.mockResolvedValue(result)
  const live = { ...panel, query: { ...panel.query!, evidence_mode: 'live' as const } }
  await act(async () => root.render(<NativeEvidencePanel panel={live} testId="run" />))
  expect(request).toHaveBeenCalledTimes(1)
  await act(async () => vi.advanceTimersByTimeAsync(10000))
  expect(request).toHaveBeenCalledTimes(2)
  const signal = request.mock.calls[1][1].signal as AbortSignal
  await act(async () => root.render(<div />))
  expect(signal.aborted).toBe(true)
  await act(async () => vi.advanceTimersByTimeAsync(20000))
  expect(request).toHaveBeenCalledTimes(2)
})
it('makes query limits visible with retry', async () => {
  request.mockRejectedValue(new Error('series limit exceeded'))
  await act(async () => root.render(<NativeEvidencePanel panel={panel} testId="run" />))
  expect(container.querySelector('[role=alert]')?.textContent).toContain('series limit exceeded')
  request.mockResolvedValue(result)
  await act(async () => container.querySelector('button')!.click())
  expect(container.textContent).toContain('Checkout (ms)')
})
it('resets a property grouping when the population changes', async () => {
  const { NativePanelAuthoring } = await import('./NativeAnalyticsPanel')
  const save = vi.fn().mockResolvedValue(undefined)
  await act(async () => root.render(<NativePanelAuthoring properties={['kind=paid']} onSave={save} />))
  const change = async (label: string, value: string) => {
    const select = container.querySelector<HTMLSelectElement>(`select[aria-label="${label}"]`)!
    await act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
  }
  await change('Evidence workflow', 'live')
  await change('Group series by', 'property')
  await change('Property category', 'kind=paid')
  await change('Evidence population', 'transaction')
  expect(container.textContent).toContain('Live business transactions')
  await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ tabs: [expect.objectContaining({ panels: [expect.objectContaining({ query: expect.objectContaining({ evidence_mode: 'live', population: 'transaction', dimensions: ['action'] }) })] })] }))
})

it('breaks live lines across uncovered publication time without inventing zeros', () => {
  const points = [
    { from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:01Z', offset_seconds: 1, count: 1, value: 10 },
    { from: '2026-10-01T00:00:04Z', to: '2026-10-01T00:00:05Z', offset_seconds: 5, count: 1, value: 20 },
  ]
  const plotted = nativeEvidencePlotPoints(points, result.start, true)
  expect(plotted.map(point => point.value)).toEqual([10, null, 20])
  expect(plotted[1].offset_seconds).toBe(2.5)
  expect(nativeEvidencePlotPoints(points, result.start, false)).toBe(points)
})
