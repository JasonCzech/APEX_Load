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

function okResponse(): ImportedDashboardGraphsResponse {
  return {
    enabled: true,
    configured: true,
    generated_at: '2026-06-12T12:00:00Z',
    dashboards: [{ dashboard_id: 'd1', dashboard_title: 'Infra', panels: [] }],
  }
}

function Harness({ test, filteredData, projectId = 'proj-a' }: { test: TestSummary | null; filteredData: MetricsHistoryPoint[]; projectId?: string }) {
  useImportedDashboardGraphs(test, filteredData, projectId)
  return null
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
})
