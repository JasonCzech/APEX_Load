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

  // TEL-A: the publications the controller names are reported by identity:
  // one replaced before it rendered is superseded, a rendered one carries its
  // measurement, and one received while hidden is background once the tab
  // has measured a visible render.
  function named(sequence: number, testId = 'run-9') {
    const telemetry = { client_received_at: new Date().toISOString(), publication: { epoch: 'epoch-1', sequence, eligible: true } }
    recordClientTelemetryReceipt(telemetry, testId)
    return { telemetry } as LiveMetrics
  }
  function reportedOutcomes() {
    const samples = request.mock.calls.flatMap(call => JSON.parse(call[1].body).samples as Array<{ state: string; receive_to_render_ms: number; publication?: { epoch: string; sequence: number } }>)
    return samples.map(sample => [sample.state, sample.publication?.sequence])
  }

  it('reports each named publication as superseded, rendered or background', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const state = { value: 'visible' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    // The first callback is cancelled when the second publication commits.
    await act(async () => { root.render(<Harness testId="run-9" enabled metrics={named(1)} />) })
    await act(async () => { root.render(<Harness testId="run-9" enabled metrics={named(2)} />) })
    await act(async () => { vi.advanceTimersByTime(5) })
    state.value = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { root.render(<Harness testId="run-9" enabled metrics={named(3)} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(1)
    const samples = JSON.parse(request.mock.calls[0][1].body).samples as Array<{ state: string; receive_to_render_ms: number; publication?: { epoch: string; sequence: number } }>
    expect(reportedOutcomes()).toEqual([['superseded', 1], ['visible', 2], ['background', 3]])
    expect(samples.every(sample => sample.publication?.epoch === 'epoch-1')).toBe(true)
    expect(samples[0].receive_to_render_ms).toBe(0)
    expect(samples[2].receive_to_render_ms).toBe(0)
  })

  it('resolves publications batched into one render, and never reports one twice', async () => {
    request.mockResolvedValue({ accepted: 1 })
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    await act(async () => { root.render(<Harness testId="run-10" enabled metrics={named(1, 'run-10')} />) })
    await act(async () => { vi.advanceTimersByTime(5) })
    // Two publications arrive before React renders: only the second commits.
    named(2, 'run-10')
    const third = named(3, 'run-10')
    // Another run's receipt on the same page is left alone.
    named(1, 'run-other')
    await act(async () => { root.render(<Harness testId="run-10" enabled metrics={third} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    // Toggling reporting does not report the same receipt again.
    await act(async () => { root.render(<Harness testId="run-10" enabled={false} metrics={third} />) })
    await act(async () => { root.render(<Harness testId="run-10" enabled metrics={third} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS * 2) })
    const outcomes = reportedOutcomes()
    expect(outcomes).toEqual([['visible', 1], ['superseded', 2], ['visible', 3]])
    expect(outcomes.filter(([state, sequence]) => state === 'superseded' && sequence === 2)).toHaveLength(1)
    expect(outcomes.filter(([state, sequence]) => state === 'visible' && sequence === 3)).toHaveLength(1)
  })

  it('labels a publication received while hidden background, even when a newer one superseded it', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const state = { value: 'visible' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    await act(async () => { root.render(<Harness testId="run-12" enabled metrics={named(1, 'run-12')} />) })
    await act(async () => { vi.advanceTimersByTime(5) })
    state.value = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    named(2, 'run-12') // received while hidden, never rendered on its own
    state.value = 'visible'
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { root.render(<Harness testId="run-12" enabled metrics={named(3, 'run-12')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(reportedOutcomes()).toEqual([['visible', 1], ['background', 2], ['visible', 3]])
  })

  it('reports publications received while hidden before its first measurement alongside that measurement', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const state = { value: 'hidden' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    await act(async () => { root.render(<Harness testId="run-13" enabled metrics={named(1, 'run-13')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
    state.value = 'visible'
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { root.render(<Harness testId="run-13" enabled metrics={named(2, 'run-13')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(reportedOutcomes()).toEqual([['background', 1], ['visible', 2]])
  })

  it('leaves a publication hidden before it painted, in a tab that measured nothing yet, for its first measurement', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const state = { value: 'visible' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    await act(async () => { root.render(<Harness testId="run-14" enabled metrics={named(1, 'run-14')} />) })
    await act(async () => {
      state.value = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      state.value = 'visible'
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS)
    })
    expect(request).not.toHaveBeenCalled()
    await act(async () => { root.render(<Harness testId="run-14" enabled metrics={named(2, 'run-14')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(reportedOutcomes()).toEqual([['background', 1], ['visible', 2]])
  })

  it('reports nothing it did not see while reporting was off, and no unmeasured outcomes before a visible render', async () => {
    request.mockResolvedValue({ accepted: 1 })
    const state = { value: 'hidden' as DocumentVisibilityState }
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state.value)
    // A hidden tab that never measured a render reports no outcomes.
    await act(async () => { root.render(<Harness testId="run-11" enabled metrics={named(1, 'run-11')} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).not.toHaveBeenCalled()
    // Publications that arrived while reporting was off are dropped, not
    // reported superseded, when it turns on.
    state.value = 'visible'
    document.dispatchEvent(new Event('visibilitychange'))
    await act(async () => { root.render(<Harness testId="run-11" enabled={false} metrics={named(2, 'run-11')} />) })
    named(3, 'run-11')
    const current = named(4, 'run-11')
    await act(async () => { root.render(<Harness testId="run-11" enabled metrics={current} />) })
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(reportedOutcomes()).toEqual([['visible', 4]])
  })

  it('cycles browser clock calibration probes every 30s without re-sending stale refinements', async () => {
    let mono = 1000
    vi.spyOn(performance, 'now').mockImplementation(() => mono)
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')

    // 1. Initial flush at mono=1020 sends client_send_mono_ms and receives clock_calibration echo.
    request.mockResolvedValueOnce({ accepted: 1, clock_calibration: [1020, 1700000001020, 1700000001021] })
    await act(async () => { root.render(<Harness testId="run-cal" enabled metrics={named(1, 'run-cal')} />) })
    mono = 1015
    await act(async () => { vi.advanceTimersByTime(5) })
    mono = 1020
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(1)
    const body1 = JSON.parse(request.mock.calls[0][1].body)
    expect(body1.client_send_mono_ms).toBe(1020)
    expect(body1.calibration).toBeUndefined()

    // 2. Second flush within 30s sends the refinement once and clears it.
    request.mockResolvedValueOnce({ accepted: 1 })
    mono = 5000
    await act(async () => { root.render(<Harness testId="run-cal" enabled metrics={named(2, 'run-cal')} />) })
    mono = 5010
    await act(async () => { vi.advanceTimersByTime(5) })
    mono = 5020
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(2)
    const body2 = JSON.parse(request.mock.calls[1][1].body)
    expect(body2.client_send_mono_ms).toBeUndefined()
    expect(body2.calibration).toEqual({
      client_send_mono_ms: 1020,
      client_recv_mono_ms: 1020,
      controller_recv_unix_ms: 1700000001020,
      controller_send_unix_ms: 1700000001021,
    })

    // 3. Third flush within 30s sends neither client_send_mono_ms nor stale calibration.
    request.mockResolvedValueOnce({ accepted: 1 })
    mono = 10000
    await act(async () => { root.render(<Harness testId="run-cal" enabled metrics={named(3, 'run-cal')} />) })
    mono = 10010
    await act(async () => { vi.advanceTimersByTime(5) })
    mono = 10020
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(3)
    const body3 = JSON.parse(request.mock.calls[2][1].body)
    expect(body3.client_send_mono_ms).toBeUndefined()
    expect(body3.calibration).toBeUndefined()

    // 4. Fourth flush after >= 30s sends a fresh client_send_mono_ms without stale calibration.
    request.mockResolvedValueOnce({ accepted: 1, clock_calibration: [32020, 1700000032020, 1700000032021] })
    mono = 32000
    await act(async () => { root.render(<Harness testId="run-cal" enabled metrics={named(4, 'run-cal')} />) })
    mono = 32010
    await act(async () => { vi.advanceTimersByTime(5) })
    mono = 32020
    await act(async () => { vi.advanceTimersByTime(CLIENT_RENDER_FLUSH_MS) })
    expect(request).toHaveBeenCalledTimes(4)
    const body4 = JSON.parse(request.mock.calls[3][1].body)
    expect(body4.client_send_mono_ms).toBe(32020)
    expect(body4.calibration).toBeUndefined()
  })

})
