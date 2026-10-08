// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { LiveMetrics } from '../types'

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

const { CLIENT_RENDER_FLUSH_MS, CLIENT_RENDER_MAX_SAMPLES, enqueueRenderSample, renderSample, useClientRenderReporter, visibleThroughout } = await import('./clientRenderReporter')

function Harness({ testId, enabled, metrics }: { testId: string; enabled: boolean; metrics?: LiveMetrics }) {
  useClientRenderReporter(testId, enabled, metrics)
  return null
}

function metricsReceivedAt(at: number): LiveMetrics {
  return { telemetry: { client_received_at: new Date(at).toISOString() } } as LiveMetrics
}

describe('client render samples', () => {
  it('measures receive to render and marks a hidden tab as background', () => {
    const received = Date.parse('2026-10-07T12:00:00.000Z')
    expect(renderSample(received, received + 16.4, true)).toEqual({ receive_to_render_ms: 16.4, state: 'visible', observed_at: '2026-10-07T12:00:00.016Z' })
    expect(renderSample(received, received + 40, false)?.state).toBe('background')
    expect(renderSample(received, received - 5, true)?.receive_to_render_ms).toBe(0)
    expect(renderSample(Number.NaN, received, true)).toBeNull()
  })

  it('counts a render as visible only if the tab stayed visible since receipt', () => {
    expect(visibleThroughout(1000, 0, true)).toBe(true)
    expect(visibleThroughout(1000, 999, true)).toBe(true)
    expect(visibleThroughout(1000, 1000, true)).toBe(false)
    expect(visibleThroughout(1000, 1500, true)).toBe(false)
    expect(visibleThroughout(1000, 0, false)).toBe(false)
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
  })

  async function publish(testId: string, enabled: boolean, at: number) {
    await act(async () => {
      root.render(<Harness testId={testId} enabled={enabled} metrics={metricsReceivedAt(at)} />)
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
      root.render(<Harness testId="run-4" enabled metrics={metricsReceivedAt(Date.now())} />)
    })
    // Hidden before the pending frame runs, visible again when it does.
    await act(async () => {
      state.value = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(1)
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
})
