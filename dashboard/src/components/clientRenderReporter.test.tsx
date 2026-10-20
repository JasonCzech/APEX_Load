// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { LiveMetrics } from '../types'
import { recordClientTelemetryReceipt } from './clientTelemetryReceipt'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const request = vi.fn()
class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}
vi.mock('../hooks/apiClient', () => ({ request, ApiError }))

const { CLIENT_RENDER_FLUSH_MS, CLIENT_RENDER_MAX_SAMPLES, enqueueRenderSample, renderSample, useClientRenderReporter } = await import('./clientRenderReporter')

function Harness({ testId, enabled, metrics }: { testId: string; enabled: boolean; metrics?: LiveMetrics }) {
  useClientRenderReporter(testId, enabled, metrics)
  return null
}

function metricsReceivedAt(at: number, testId = 'run-1'): LiveMetrics {
  const telemetry = { client_received_at: new Date(at).toISOString() }
  recordClientTelemetryReceipt(telemetry, testId)
  return { telemetry } as LiveMetrics
}

describe('client render samples', () => {
  it('measures receive to render and marks a hidden tab as background', () => {
    const received = Date.parse('2026-10-07T12:00:00.000Z')
    expect(renderSample(100, 116.4, true, received + 16)).toEqual({ receive_to_render_ms: 16.4, state: 'visible', observed_at: '2026-10-07T12:00:00.016Z' })
    expect(renderSample(100, 140, false, received)?.state).toBe('background')
    expect(renderSample(100, 95, true, received)).toBeNull()
    expect(renderSample(Number.NaN, 100, true, received)).toBeNull()
    expect(renderSample(100, Number.POSITIVE_INFINITY, true, received)).toBeNull()
    expect(renderSample(0, Number.MAX_VALUE, true, received)).toBeNull()
    expect(renderSample(100, 120, true, Number.NaN)).toBeNull()
    expect(renderSample(-1, 120, true, received)).toBeNull()
    expect(renderSample(0, 0, true, received)?.receive_to_render_ms).toBe(0)
  })

  it('keeps at most one report of the newest samples', () => {
    let queue = [] as ReturnType<typeof enqueueRenderSample>
    for (let i = 0; i < CLIENT_RENDER_MAX_SAMPLES + 5; i++) {
      queue = enqueueRenderSample(queue, { receive_to_render_ms: i, state: 'visible', observed_at: '2026-10-07T12:00:00.000Z' })
    }
    expect(queue).toHaveLength(CLIENT_RENDER_MAX_SAMPLES)
    expect(queue[0].receive_to_render_ms).toBe(5)
  })
})

describe('useClientRenderReporter', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    request.mockReset()
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 1) as unknown as number)
    vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle))
    container = document.createElement('div')
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    vi.unstubAllGlobals()
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  async function publish(testId: string, enabled: boolean, at: number) {
    await act(async () => {
      root.render(<Harness testId={testId} enabled={enabled} metrics={metricsReceivedAt(at, testId)} />)
    })
    await act(async () => {
      vi.advanceTimersByTime(5)
    })
  }

  it('reports measured renders in batches only while enabled', async () => {
    request.mockResolvedValue({ accepted: 2 })
    await publish('run-1', true, Date.now())
    await publish('run-1', true, Date.now() + 1)
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS)
    })
    expect(request).toHaveBeenCalledTimes(1)
    const [path, options] = request.mock.calls[0]
    expect(path).toBe('/tests/run-1/telemetry/client-render')
    const body = JSON.parse(options.body)
    expect(body.client_id).toBeTruthy()
    expect(body.samples).toHaveLength(2)
    expect(body.samples.every((sample: { state: string }) => sample.state === 'visible')).toBe(true)

    request.mockClear()
    await publish('run-1', false, Date.now() + 2)
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS * 2)
    })
    expect(request).not.toHaveBeenCalled()
  })

  it('stops reporting for a run the controller refuses with 409', async () => {
    request.mockRejectedValue(new ApiError('not enabled', 409))
    await publish('run-2', true, Date.now())
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS)
    })
    expect(request).toHaveBeenCalledTimes(1)
    await publish('run-2', true, Date.now() + 1)
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS * 2)
    })
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('reports a render whose tab was hidden before it painted as background', async () => {
    request.mockResolvedValue({ accepted: 0 })
    const state = { value: 'visible' as DocumentVisibilityState }
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    await act(async () => {
      root.render(<Harness testId="run-4" enabled metrics={metricsReceivedAt(Date.now(), "run-4")} />)
    })
    // Hidden before the pending frame runs (a hidden tab runs no frames),
    // and visible again by the time it does: both callbacks execute while
    // visible, but the receipt still includes a hidden interval.
    await act(async () => {
      state.value = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      state.value = 'visible'
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(5)
    })
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS)
    })
    expect(request).toHaveBeenCalledTimes(1)
    expect(JSON.parse(request.mock.calls[0][1].body).samples.map((sample: { state: string }) => sample.state)).toEqual(['background'])
    visibility.mockRestore()
  })

  it('does not measure a publication received while the tab is hidden', async () => {
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    await publish('run-3', true, Date.now())
    await act(async () => {
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS)
    })
    expect(request).not.toHaveBeenCalled()
    visibility.mockRestore()
  })

  it('uses monotonic elapsed time across forward and backward wall-clock steps', async () => {
    request.mockResolvedValue({ accepted: 1 })
    let monotonic = 0
    vi.spyOn(performance, 'now').mockImplementation(() => monotonic)
    for (const step of [-120_000, 120_000]) {
      request.mockClear()
      monotonic += 100
      await act(async () => { root.render(<Harness testId="run-1" enabled metrics={metricsReceivedAt(Date.now())} />) })
      vi.setSystemTime(Date.now() + step)
      monotonic += 25
      await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
      expect(JSON.parse(request.mock.calls[0][1].body).samples[0].receive_to_render_ms).toBe(25)
    }
  })

  it('requires a local receipt bound to this run, not a wire timestamp', async () => {
    const wire = { telemetry: { client_received_at: new Date().toISOString() } } as LiveMetrics
    await act(async () => { root.render(<Harness testId="run-1" enabled metrics={wire} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    await act(async () => { root.render(<Harness testId="run-2" enabled metrics={metricsReceivedAt(Date.now(), 'run-1')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
  })

  it('distinguishes receipts sharing a wall timestamp without resampling a latency update', async () => {
    request.mockResolvedValue({ accepted: 2 })
    const wall = Date.now()
    await publish('run-1', true, wall)
    const metrics = metricsReceivedAt(wall)
    await act(async () => { root.render(<Harness testId="run-1" enabled metrics={metrics} />) })
    await act(async () => { vi.advanceTimersByTime(5) })
    await act(async () => { root.render(<Harness testId="run-1" enabled metrics={{ ...metrics }} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(JSON.parse(request.mock.calls[0][1].body).samples).toHaveLength(2)
  })

  it('clears disabled samples and cannot sample the same receipt twice', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const metrics = metricsReceivedAt(Date.now())
    const render = async (enabled: boolean) => {
      await act(async () => { root.render(<Harness testId="run-1" enabled={enabled} metrics={metrics} />) })
      await act(async () => { vi.advanceTimersByTime(5) })
    }
    await render(true)
    await render(false)
    await render(true)
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
    await publish('run-1', true, Date.now())
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(JSON.parse(request.mock.calls[0][1].body).samples).toHaveLength(1)
  })

  it('ignores a late refusal from a disabled reporting session', async () => {
    let reject!: (error: Error) => void
    request.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail })).mockResolvedValue({ accepted: 1 })
    await publish('run-1', true, Date.now())
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    await publish('run-1', false, Date.now())
    await publish('run-1', true, Date.now())
    await act(async () => { reject(new ApiError('old session', 409)) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(2)
  })

  it('excludes a hidden receipt even when visible before React processes it', async () => {
    const state = { value: 'hidden' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    const metrics = metricsReceivedAt(Date.now())
    state.value = 'visible'
    await act(async () => { root.render(<Harness testId="run-1" enabled metrics={metrics} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
  })


  it('remembers a hidden interval before the reporter is enabled', async () => {
    const state = { value: 'visible' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    vi.spyOn(performance, 'now').mockReturnValue(0)
    const metrics = metricsReceivedAt(Date.now())
    await act(async () => { root.render(<Harness testId="run-1" enabled={false} metrics={metrics} />) })
    state.value = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    state.value = 'visible'
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { root.render(<Harness testId="run-1" enabled metrics={metrics} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
  })

})
