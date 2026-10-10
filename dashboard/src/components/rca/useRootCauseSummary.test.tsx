// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ReportRootCauseSummary } from '../../types/rca'
import { rcaTestResponse, rcaTestSummary } from './rcaTestFixture'
import { useRootCauseSummary } from './useRootCauseSummary'

const mocks = vi.hoisted(() => ({ api: { getRootCauseAnalysis: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

afterEach(() => {
  vi.useRealTimers()
  mocks.api.getRootCauseAnalysis.mockReset()
})

function Probe({ summary, onSettled, seen }: { summary?: ReportRootCauseSummary; onSettled: () => void; seen: (s?: ReportRootCauseSummary) => void }) {
  seen(useRootCauseSummary('run-1', summary, onSettled))
  return null
}

describe('useRootCauseSummary', () => {
  it('polls a queued analysis until it ends, then reports it and settles', async () => {
    vi.useFakeTimers()
    const pending = rcaTestSummary({ state: 'queued', analysis_id: undefined, outcome: undefined, top_hypothesis: undefined, latest: { analysis_id: 'rca-1', revision: 2, state: 'queued', created_at: '2026-10-05T12:10:00Z' } })
    mocks.api.getRootCauseAnalysis
      .mockResolvedValueOnce({ analysis_id: 'rca-1', revision: 2, state: 'running', presented: true })
      .mockResolvedValueOnce(rcaTestResponse())
    const onSettled = vi.fn()
    let latest: ReportRootCauseSummary | undefined
    const div = document.createElement('div')
    const root = createRoot(div)
    await act(async () => root.render(<Probe summary={pending} onSettled={onSettled} seen={s => { latest = s }} />))
    expect(latest?.state).toBe('queued')
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(mocks.api.getRootCauseAnalysis).toHaveBeenCalledWith('run-1', 'rca-1', expect.anything())
    expect(latest?.state).toBe('running')
    expect(onSettled).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(4500) })
    expect(latest).toMatchObject({ state: 'completed', analysis_id: 'rca-1', outcome: 'supported' })
    expect(onSettled).toHaveBeenCalledTimes(1)
    act(() => root.unmount())
  })

  it('does not poll a settled summary', async () => {
    vi.useFakeTimers()
    const div = document.createElement('div')
    const root = createRoot(div)
    await act(async () => root.render(<Probe summary={rcaTestSummary()} onSettled={vi.fn()} seen={() => undefined} />))
    await act(async () => { await vi.advanceTimersByTimeAsync(20000) })
    expect(mocks.api.getRootCauseAnalysis).not.toHaveBeenCalled()
    act(() => root.unmount())
  })
})
