// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ImportedDashboardGraphsResponse, MetricsHistoryPoint, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const getImportedDashboardGraphs = vi.fn<
  (params: { project_id?: string; test_id?: string; start: string; end: string; step_seconds: number }) => Promise<ImportedDashboardGraphsResponse>
>()

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getImportedDashboardGraphs }),
}))

import { useImportedDashboardGraphs } from './useImportedDashboardGraphs'
import { ApiError } from '../hooks/apiClient'

function okResponse(): ImportedDashboardGraphsResponse {
  return {
    enabled: true,
    configured: true,
    generated_at: '2026-06-12T12:00:00Z',
    dashboards: [{ dashboard_id: 'd1', dashboard_title: 'Infra', panels: [{ panel_id: 1, title: 'CPU', kind: 'line', series: [] }] }],
  }
}

let lastResult: ReturnType<typeof useImportedDashboardGraphs> | null = null
const observedResults: ReturnType<typeof useImportedDashboardGraphs>[] = []
function Harness({ test, filteredData, projectId = 'proj-a' }: { test: TestSummary | null; filteredData: MetricsHistoryPoint[]; projectId?: string }) {
  lastResult = useImportedDashboardGraphs(test, filteredData, projectId)
  observedResults.push(lastResult)
  return null
}

function deferredResponse() {
  let resolve!: (response: ImportedDashboardGraphsResponse) => void
  let reject!: (error: Error) => void
  const promise = new Promise<ImportedDashboardGraphsResponse>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

function point(timestampMs: number): MetricsHistoryPoint {
  return { time: new Date(timestampMs).toISOString(), timestampMs, vusers: 10, tps: 5, avgMs: 100, errorPct: 0, p50: 90, p90: 150, p95: 180, p99: 220 }
}

function runningTest(): TestSummary {
  return {
    id: 'test-1', name: 'Run', status: 'RUNNING', total_vusers: 10, duration: '10m',
    created_at: '2026-06-12T12:00:00Z', started_at: '2026-06-12T12:00:00Z',
    groups: [{ name: 'g1', protocol: 'http', script_id: 's1', script_name: 'S1', vusers: 10 }],
  }
}

describe('useImportedDashboardGraphs', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    getImportedDashboardGraphs.mockReset()
    getImportedDashboardGraphs.mockResolvedValue(okResponse())
    lastResult = null
    observedResults.length = 0
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
  })

  const render = async (test: TestSummary | null, filteredData: MetricsHistoryPoint[], projectId: string | undefined = 'proj-a') => {
    await act(async () => {
      root.render(<Harness test={test} filteredData={filteredData} projectId={projectId} />)
      await Promise.resolve()
    })
  }

  it('does not refetch when data ticks within the same step bucket', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    const initial = [point(base), point(base + 2000)]
    await render(runningTest(), initial)
    const after = getImportedDashboardGraphs.mock.calls.length
    for (let i = 1; i <= 4; i++) {
      await render(runningTest(), [...initial, point(base + 2000 + i * 1000)])
    }
    expect(getImportedDashboardGraphs.mock.calls.length).toBe(after)
  })

  it('polls on the 10s interval for a live run', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    expect(getImportedDashboardGraphs).toHaveBeenCalledTimes(1)
    await act(async () => {
      vi.advanceTimersByTime(10_000)
      await Promise.resolve()
    })
    expect(getImportedDashboardGraphs).toHaveBeenCalledTimes(2)
  })

  it('does not fetch when there is no window (null test, no data)', async () => {
    await render(null, [])
    expect(getImportedDashboardGraphs).not.toHaveBeenCalled()
  })

  it('uses the legacy unassigned project when the test has no project id', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)], '')
    expect(getImportedDashboardGraphs).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'unassigned' }))
  })

  it('does not overlap interval polls while a request is in flight', async () => {
    let resolveRequest!: (response: ImportedDashboardGraphsResponse) => void
    getImportedDashboardGraphs.mockImplementation(() => new Promise(resolve => { resolveRequest = resolve }))
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    await act(async () => {
      vi.advanceTimersByTime(20_000)
      await Promise.resolve()
    })
    expect(getImportedDashboardGraphs).toHaveBeenCalledTimes(1)
    await act(async () => { resolveRequest(okResponse()); await Promise.resolve() })
  })

  it('confirms availability from panels and retains it during refreshes and transient errors', async () => {
    const initial = deferredResponse()
    getImportedDashboardGraphs.mockReturnValueOnce(initial.promise)
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    expect(lastResult?.importedDashboardsLoading).toBe(true)
    expect(lastResult?.importedDashboardsAvailable).toBe(false)

    // The panel definition establishes availability even without metric samples.
    await act(async () => initial.resolve(okResponse()))
    expect(lastResult?.importedDashboardsAvailable).toBe(true)

    const refresh = deferredResponse()
    getImportedDashboardGraphs.mockReturnValueOnce(refresh.promise)
    await render(runningTest(), [point(base), point(base + 30_000)])
    expect(lastResult?.importedDashboardsLoading).toBe(true)
    expect(lastResult?.importedDashboardsAvailable).toBe(true)

    await act(async () => refresh.reject(new ApiError('upstream unavailable', 502)))
    expect(lastResult?.importedDashboardsAvailable).toBe(true)
    expect(lastResult?.importedDashboardsError).toBe('upstream unavailable')
    expect(lastResult?.importedDashboards).toEqual([])
  })

  it.each([
    { ...okResponse(), enabled: false },
    { ...okResponse(), configured: false },
    { ...okResponse(), dashboards: [] },
    { ...okResponse(), dashboards: [{ dashboard_id: 'empty', panels: [] }] },
  ])('keeps unavailable dashboard configuration hidden during repeated polls', async response => {
    getImportedDashboardGraphs.mockResolvedValue(response)
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    await render(runningTest(), [point(base), point(base + 30_000)])
    await act(async () => { vi.advanceTimersByTime(10_000); await Promise.resolve() })
    expect(getImportedDashboardGraphs).toHaveBeenCalledTimes(3)
    expect(observedResults.every(result => !result.importedDashboardsAvailable)).toBe(true)
  })

  it.each(['run', 'project'])('hides the prior %s scope immediately and ignores its pending response', async changedScope => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    const initialData = [point(base), point(base + 2000)]
    const refreshedData = [point(base), point(base + 30_000)]
    await render(runningTest(), initialData)
    expect(lastResult?.importedDashboardsAvailable).toBe(true)

    const stale = deferredResponse()
    const next = deferredResponse()
    getImportedDashboardGraphs.mockReturnValueOnce(stale.promise).mockReturnValueOnce(next.promise)
    await render(runningTest(), refreshedData)
    observedResults.length = 0
    await render(changedScope === 'run' ? { ...runningTest(), id: 'test-2' } : runningTest(), refreshedData, changedScope === 'project' ? 'proj-b' : 'proj-a')
    expect(observedResults[0].importedDashboardsAvailable).toBe(false)
    expect(observedResults[0].importedDashboards).toEqual([])

    await act(async () => stale.resolve(okResponse()))
    expect(lastResult?.importedDashboardsAvailable).toBe(false)
    expect(lastResult?.importedDashboards).toEqual([])

    await act(async () => next.resolve({ ...okResponse(), dashboards: [{ ...okResponse().dashboards[0], dashboard_id: 'new' }] }))
    expect(lastResult?.importedDashboardsAvailable).toBe(true)
    expect(lastResult?.importedDashboards[0].dashboard_id).toBe('new')
  })

  it.each(['disabled', 'empty', 'forbidden'])('clears confirmed availability when dashboards become %s', async reason => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    expect(lastResult?.importedDashboardsAvailable).toBe(true)

    if (reason === 'forbidden') getImportedDashboardGraphs.mockRejectedValue(new ApiError('forbidden', 403))
    else getImportedDashboardGraphs.mockResolvedValue({ ...okResponse(), enabled: reason !== 'disabled', dashboards: [] })
    await act(async () => { vi.advanceTimersByTime(10_000); await Promise.resolve() })
    expect(lastResult?.importedDashboardsAvailable).toBe(false)
    expect(lastResult?.importedDashboards).toEqual([])
  })

  it('does not infer configuration from an initial failure', async () => {
    getImportedDashboardGraphs.mockRejectedValue(new ApiError('unavailable', 502))
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render(runningTest(), [point(base), point(base + 2000)])
    expect(lastResult?.importedDashboardsAvailable).toBe(false)
    expect(lastResult?.importedDashboardsError).toBe('unavailable')
  })

  it('loads terminal-run snapshots without current settings and does not poll them', async () => {
    const base = Date.parse('2026-06-12T12:00:00Z')
    await render({ ...runningTest(), status: 'COMPLETE', ended_at: new Date(base + 2000).toISOString() }, [point(base), point(base + 2000)])
    expect(lastResult?.importedDashboardsAvailable).toBe(true)
    expect(getImportedDashboardGraphs).toHaveBeenCalledWith(expect.objectContaining({ test_id: 'test-1', project_id: 'proj-a' }))
    await act(async () => { vi.advanceTimersByTime(30_000); await Promise.resolve() })
    expect(getImportedDashboardGraphs).toHaveBeenCalledTimes(1)

    await render(null, [])
    expect(lastResult?.importedDashboardsAvailable).toBe(false)
    expect(lastResult?.importedDashboards).toEqual([])
  })
})
