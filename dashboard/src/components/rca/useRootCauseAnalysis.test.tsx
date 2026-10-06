// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { rcaTestResponse } from './rcaTestFixture'
import { useRootCauseAnalysis, type RootCauseAnalysisModel } from './useRootCauseAnalysis'

const mocks = vi.hoisted(() => ({ api: { getRootCauseAssessment: vi.fn(), getRootCauseAnalysis: vi.fn(), requestRootCauseAnalysis: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

describe('useRootCauseAnalysis', () => {
  let container: HTMLDivElement
  let root: Root
  let current: RootCauseAnalysisModel

  function Probe({ testId, analysisId, enabled }: { testId: string; analysisId?: string; enabled: boolean }) {
    current = useRootCauseAnalysis(testId, analysisId, enabled)
    return null
  }

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    root = createRoot(container)
    Object.values(mocks.api).forEach(fn => fn.mockReset())
  })

  afterEach(() => {
    act(() => root.unmount())
    vi.useRealTimers()
  })

  it('loads the latest completed analysis with its lists filled', async () => {
    mocks.api.getRootCauseAssessment.mockResolvedValue(rcaTestResponse())
    await act(async () => root.render(<Probe testId="run-1" enabled />))
    expect(mocks.api.getRootCauseAssessment).toHaveBeenCalledWith('run-1', expect.objectContaining({ signal: expect.anything() }))
    expect(current.response?.assessment?.capacity).toEqual([])
    expect(current.loading).toBe(false)
  })

  it('polls a routed analysis until it ends, backing off', async () => {
    mocks.api.getRootCauseAnalysis
      .mockResolvedValueOnce({ analysis_id: 'rca-2', revision: 3, state: 'queued', presented: true })
      .mockResolvedValueOnce({ analysis_id: 'rca-2', revision: 3, state: 'running', presented: true })
      .mockResolvedValueOnce(rcaTestResponse({ analysis_id: 'rca-2', revision: 3 }))
    await act(async () => root.render(<Probe testId="run-1" analysisId="rca-2" enabled />))
    expect(current.response?.state).toBe('queued')
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(current.response?.state).toBe('running')
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(current.response?.state).toBe('running')
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(current.response?.state).toBe('completed')
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(mocks.api.getRootCauseAnalysis).toHaveBeenCalledTimes(3)
  })

  it('keeps polling through a failed poll', async () => {
    mocks.api.getRootCauseAnalysis
      .mockResolvedValueOnce({ analysis_id: 'rca-2', revision: 3, state: 'running', presented: true })
      .mockRejectedValueOnce(new Error('controller restarting'))
      .mockResolvedValueOnce(rcaTestResponse({ analysis_id: 'rca-2', revision: 3 }))
    await act(async () => root.render(<Probe testId="run-1" analysisId="rca-2" enabled />))
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(current.error).toBe('controller restarting')
    expect(current.response?.state).toBe('running')
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(current.response?.state).toBe('completed')
    expect(current.error).toBe('')
  })

  it('stops polling when the route moves on', async () => {
    mocks.api.getRootCauseAnalysis.mockResolvedValue({ analysis_id: 'rca-2', revision: 3, state: 'running', presented: true })
    await act(async () => root.render(<Probe testId="run-1" analysisId="rca-2" enabled />))
    await act(async () => root.render(<Probe testId="run-1" enabled={false} />))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(mocks.api.getRootCauseAnalysis).toHaveBeenCalledTimes(1)
    expect(current.response).toBeNull()
  })

  it('keeps the shown analysis while reloading it', async () => {
    let release!: (value: unknown) => void
    mocks.api.getRootCauseAnalysis.mockResolvedValueOnce(rcaTestResponse()).mockReturnValueOnce(new Promise(resolve => { release = resolve }))
    await act(async () => root.render(<Probe testId="run-1" analysisId="rca-1" enabled />))
    expect(current.response?.analysis_id).toBe('rca-1')
    await act(async () => current.reload())
    expect(current.response?.analysis_id).toBe('rca-1')
    await act(async () => release(rcaTestResponse({ revision: 2, completed_at: '2026-10-05T12:30:00Z' })))
    expect(current.response?.completed_at).toBe('2026-10-05T12:30:00Z')
    // Another analysis starts empty.
    let releaseOther!: (value: unknown) => void
    mocks.api.getRootCauseAnalysis.mockReturnValueOnce(new Promise(resolve => { releaseOther = resolve }))
    await act(async () => root.render(<Probe testId="run-1" analysisId="rca-2" enabled />))
    expect(current.response).toBeNull()
    await act(async () => releaseOther(rcaTestResponse({ analysis_id: 'rca-2' })))
    expect(current.response?.analysis_id).toBe('rca-2')
  })

  it('reports load and request failures', async () => {
    mocks.api.getRootCauseAssessment.mockRejectedValue(new Error('archive unavailable'))
    mocks.api.requestRootCauseAnalysis.mockRejectedValue(new Error('root-cause v2 is not enabled for this project'))
    await act(async () => root.render(<Probe testId="run-1" enabled />))
    expect(current.error).toBe('archive unavailable')
    let result: unknown
    await act(async () => { result = await current.request({ enrich: true }) })
    expect(result).toBeNull()
    expect(mocks.api.requestRootCauseAnalysis).toHaveBeenCalledWith('run-1', { enrich: true })
    expect(current.requestError).toBe('root-cause v2 is not enabled for this project')
  })
})
