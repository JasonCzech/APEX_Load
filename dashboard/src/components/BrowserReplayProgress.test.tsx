// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserReplayProgress from './BrowserReplayProgress'
import { ApiError } from '../hooks/apiClient'
import { type WSMessage } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { requestMock, bridgeRequestMock, apiFetchMock, downloadBlobMock } = vi.hoisted(() => ({
  requestMock: vi.fn(),
  bridgeRequestMock: vi.fn(),
  apiFetchMock: vi.fn(),
  downloadBlobMock: vi.fn(async () => 'saved'),
}))

vi.mock('../hooks/apiClient', async importOriginal => ({
  ...await importOriginal<typeof import('../hooks/apiClient')>(),
  request: requestMock,
  apiFetch: apiFetchMock,
}))

vi.mock('../utils/fileUtils', async importOriginal => ({
  ...await importOriginal<typeof import('../utils/fileUtils')>(),
  downloadBlob: downloadBlobMock,
}))

vi.mock('../hooks/api/browserRecorderBridge', () => ({
  bridgeRequest: bridgeRequestMock,
}))

// Capture the onMessage handler the component registers so the test can push
// browser_replay_* events through the real reducer logic.
let lastOnMessage: ((msg: WSMessage) => void) | undefined
vi.mock('../hooks/useWebSocket', () => ({
  useWebSocket: ({ onMessage }: { onMessage?: (msg: WSMessage) => void }) => {
    lastOnMessage = onMessage
    return { connectionState: 'open' }
  },
}))

describe('BrowserReplayProgress', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    lastOnMessage = undefined
    requestMock.mockImplementation(async (path: string) => ({
      session: {
        id: decodeURIComponent(path.split('/').pop() || ''),
        status: 'running',
        group: 'Replay verification',
        protocol: 'browser',
        current_event_index: -1,
      },
    }))
    bridgeRequestMock.mockImplementation(async (_bridge: unknown, path: string) => ({
      session: {
        id: decodeURIComponent(path.split('/').pop() || ''),
        status: 'running',
        group: 'Replay verification',
        protocol: 'browser',
        current_event_index: -1,
      },
    }))
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  async function send(msg: WSMessage) {
    await act(async () => { lastOnMessage?.(msg) })
  }

  it('renders per-step pass/fail and ignores other replay sessions', async () => {
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-1" />)
    })
    expect(container.textContent).toContain('Verifying…')

    // An event for a different session must be ignored.
    await send({ type: 'browser_replay_event', replay_session_id: 'other', event: { event_index: 0, action_name: 'X', success: true } } as any)
    expect(container.querySelectorAll('.brp-replay-event').length).toBe(0)

    // Two events for our session: one pass, one fail with a repair suggestion.
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-1', event: { event_index: 0, action_name: 'Click login', action_type: 'browser_click', success: true, duration_ms: 12 } } as any)
    await send({
      type: 'browser_replay_event', replay_session_id: 'rep-1',
      event: {
        event_index: 1, action_name: 'Fill email', action_type: 'browser_fill', success: false, duration_ms: 30,
        error_message: 'selector not found',
        repair_suggestions: [{ id: 's1', title: 'Use data-testid', description: 'Switch to #email', confidence: 0.9 }],
      },
    } as any)

    expect(container.querySelectorAll('.brp-replay-event').length).toBe(2)
    expect(container.textContent).toContain('1 passed · 1 failed')
    expect(container.textContent).toContain('Click login')
    expect(container.textContent).toContain('1 fix')
  })

  it('reports the steps replayed and the latest failure as events arrive', async () => {
    const onProgress = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-1" onProgress={onProgress} />)
    })
    expect(onProgress).toHaveBeenLastCalledWith({ steps: 0, lastError: undefined })

    await send({ type: 'browser_replay_event', replay_session_id: 'rep-1', event: { event_index: 0, action_name: 'Open', action_type: 'browser_navigate', success: false, duration_ms: 40, error_message: 'the site refused the page' } } as any)
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-1', event: { event_index: 1, action_name: 'Click', action_type: 'browser_click', success: false, duration_ms: 1, error_message: 'skipped: the site refused the page' } } as any)
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-1', event: { event_index: 2, action_name: 'Wait', action_type: 'browser_wait', success: true, duration_ms: 5 } } as any)
    expect(onProgress).toHaveBeenLastCalledWith({ steps: 3, lastError: 'skipped: the site refused the page' })
  })

  it('shows the completed state with report-level self-heal suggestions', async () => {
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-2" />)
    })
    await send({
      type: 'browser_replay_completed', replay_session_id: 'rep-2',
      report: { events: [], repair_suggestions: [{ id: 'r1', title: 'Stabilize selector', description: 'prefer role', confidence: 0.7 }] },
    } as any)

    expect(container.textContent).toContain('Verified ✓')
    expect(container.textContent).toContain('Self-heal suggestions')
    expect(container.textContent).toContain('Stabilize selector')
  })

  it('offers a finished controller replay\'s HAR and evidence, never a bridge replay\'s', async () => {
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-har" />)
    })
    const downloads = () => container.querySelector('[aria-label="Replay HAR downloads"]')
    expect(downloads(), 'not while the replay runs').toBeNull()
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-har', report: { events: [] } } as any)
    const button = (text: string) => Array.from(downloads()?.querySelectorAll('button') || []).find(element => element.textContent === text)
    apiFetchMock.mockResolvedValue({ ok: true, status: 200, headers: new Headers(), blob: async () => new Blob(['{}']), json: async () => ({}) })
    await act(async () => { button('Download replay HAR')!.click() })
    await act(async () => { button('Download replay evidence')!.click() })
    expect(apiFetchMock.mock.calls.map(call => call[0])).toEqual(['/browser-replay/sessions/rep-har/har', '/browser-replay/sessions/rep-har/evidence'])
    expect(downloadBlobMock).toHaveBeenCalledTimes(2)

    await act(async () => { root.unmount() })
    root = createRoot(container)
    const bridge = { origin: 'http://127.0.0.1:27777', token: 'bridge-token', session_id: 'bridge-session', paired_at: '2026-06-22T00:00:00Z' }
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-bridge-har" bridge={bridge} />)
    })
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-bridge-har', report: { events: [] } } as any)
    expect(container.textContent).toContain('Verified ✓')
    expect(downloads(), 'a bridge replay is not on the controller').toBeNull()
  })

  it('shows the failed state', async () => {
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-3" />)
    })
    await send({ type: 'browser_replay_failed', replay_session_id: 'rep-3', report: {} } as any)
    expect(container.textContent).toContain('Issues found')
  })

  it('ignores malformed replay events and calls onComplete with the final report', async () => {
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-4" onComplete={onComplete} />)
    })
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-4', event: { success: true } } as any)
    expect(container.querySelectorAll('.brp-replay-event').length).toBe(0)

    const report = {
      success: true,
      group: 'Recorder',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      events: [],
    }
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-4', report } as any)
    expect(onComplete).toHaveBeenCalledWith(report, [])
  })

  it('marks inactive verification as waiting without completing it', async () => {
    vi.useFakeTimers()
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-timeout" onComplete={onComplete} />)
    })
    await act(async () => {
      vi.advanceTimersByTime(30000)
    })
    expect(container.textContent).toContain('Still waiting for replay events')
    expect(container.textContent).toContain('Verifying…')
    expect(onComplete).not.toHaveBeenCalled()
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-timeout', event: { event_index: 0, action_name: 'Click', action_type: 'browser_click', target: '#go', phase: 'action', success: true, started_at: '2026-06-22T00:00:00Z', completed_at: '2026-06-22T00:00:01Z', duration_ms: 20 } } as any)
    expect(container.textContent).not.toContain('Still waiting for replay events')
  })

  it('only completes once and synthesizes a report from observed events when needed', async () => {
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-once" onComplete={onComplete} />)
    })
    await send({ type: 'browser_replay_event', replay_session_id: 'rep-once', event: { event_index: 0, action_name: 'Click', action_type: 'browser_click', target: '#go', phase: 'action', success: true, started_at: '2026-06-22T00:00:00Z', completed_at: '2026-06-22T00:00:01Z', duration_ms: 20 } } as any)
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-once' } as any)
    await send({ type: 'browser_replay_failed', replay_session_id: 'rep-once', report: { events: [], success: false } } as any)

    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      replay_session_id: 'rep-once',
      success: true,
      events: [expect.objectContaining({ action_name: 'Click' })],
    }), [expect.objectContaining({ action_name: 'Click' })])
  })

  it('includes the final event when event and completion messages arrive in one React batch', async () => {
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-batched" onComplete={onComplete} />)
    })

    await act(async () => {
      lastOnMessage?.({
        type: 'browser_replay_event',
        replay_session_id: 'rep-batched',
        event: {
          event_index: 0,
          action_name: 'Final click',
          action_type: 'browser_click',
          success: true,
          started_at: '2026-06-22T00:00:00Z',
          completed_at: '2026-06-22T00:00:01Z',
        },
      } as any)
      lastOnMessage?.({ type: 'browser_replay_completed', replay_session_id: 'rep-batched' } as any)
    })

    expect(onComplete).toHaveBeenCalledWith(
      expect.objectContaining({ events: [expect.objectContaining({ action_name: 'Final click' })] }),
      [expect.objectContaining({ action_name: 'Final click' })],
    )
  })

  it('replaces provisional WebSocket events with the authoritative terminal report', async () => {
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-authoritative" onComplete={onComplete} />)
    })
    await send({
      type: 'browser_replay_event',
      replay_session_id: 'rep-authoritative',
      event: {
        event_index: 0,
        phase: 'action',
        action_name: 'Provisional click',
        action_type: 'browser_click',
        target: '#old',
        success: true,
      },
    } as any)
    await send({
      type: 'browser_replay_event',
      replay_session_id: 'rep-authoritative',
      event: {
        event_index: 1,
        phase: 'action',
        action_name: 'Stale trailing event',
        action_type: 'browser_click',
        target: '#stale',
        success: false,
      },
    } as any)

    const report = {
      replay_session_id: 'rep-authoritative',
      group: 'Recorder replay',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      success: true,
      events: [{
        event_index: 0,
        phase: 'action',
        action_name: 'Final click',
        action_type: 'browser_click',
        target: '#final',
        success: true,
      }],
    }
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-authoritative', report } as any)

    expect(container.querySelectorAll('.brp-replay-event')).toHaveLength(1)
    expect(container.textContent).toContain('Final click')
    expect(container.textContent).not.toContain('Provisional click')
    expect(container.textContent).not.toContain('Stale trailing event')
    expect(onComplete).toHaveBeenCalledWith(report, report.events)
  })

  it('accepts an empty terminal report as authoritative', async () => {
    const onComplete = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-authoritative-empty" onComplete={onComplete} />)
    })
    await send({
      type: 'browser_replay_event',
      replay_session_id: 'rep-authoritative-empty',
      event: {
        event_index: 0,
        phase: 'action',
        action_name: 'Provisional event',
        action_type: 'browser_click',
        target: '#old',
        success: true,
      },
    } as any)
    const report = {
      replay_session_id: 'rep-authoritative-empty',
      group: 'Recorder replay',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      success: true,
      events: [],
    }

    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-authoritative-empty', report } as any)

    expect(container.querySelectorAll('.brp-replay-event')).toHaveLength(0)
    expect(container.textContent).not.toContain('Provisional event')
    expect(onComplete).toHaveBeenCalledWith(report, [])
  })

  it('backfills a replay that completed before the WebSocket subscribed and stops polling', async () => {
    vi.useFakeTimers()
    const onComplete = vi.fn()
    const report = {
      replay_session_id: 'rep-rest-terminal',
      group: 'Recorder replay',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      success: true,
      events: [{
        event_index: 0,
        phase: 'action',
        action_name: 'Fast click',
        action_type: 'browser_click',
        target: '#go',
        success: true,
        started_at: '2026-06-22T00:00:00Z',
        completed_at: '2026-06-22T00:00:01Z',
        duration_ms: 10,
      }],
    }
    requestMock.mockResolvedValueOnce({
      session: {
        id: 'rep-rest-terminal',
        status: 'completed',
        group: 'Recorder replay',
        protocol: 'browser',
        current_event_index: 0,
        completed_at: report.completed_at,
        events: report.events,
        report,
      },
    })

    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-rest-terminal" onComplete={onComplete} />)
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Verified ✓')
    expect(container.textContent).toContain('Fast click')
    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledWith(report, report.events)
    expect(requestMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(
        <BrowserReplayProgress
          replaySessionId="rep-rest-terminal"
          onComplete={(nextReport, events) => onComplete(nextReport, events)}
        />,
      )
      vi.advanceTimersByTime(10_000)
      await Promise.resolve()
    })
    expect(requestMock).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledTimes(1)
  })

  it('fails a permanently missing client-selected replay after a bounded reconciliation window', async () => {
    vi.useFakeTimers()
    const onComplete = vi.fn()
    requestMock.mockRejectedValue(new ApiError('not found', 404))

    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-never-accepted" onComplete={onComplete} />)
      await Promise.resolve()
    })

    expect(onComplete).not.toHaveBeenCalled()
    for (let attempt = 0; attempt < 4; attempt += 1) {
      await act(async () => {
        root.render(
          <BrowserReplayProgress
            replaySessionId="rep-never-accepted"
            onComplete={(nextReport, events) => onComplete(nextReport, events)}
          />,
        )
        vi.advanceTimersByTime(1500)
        await Promise.resolve()
      })
    }

    expect(requestMock).toHaveBeenCalledTimes(5)
    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledWith(null, [])
    expect(container.textContent).toContain('Replay session could not be confirmed')
    expect(container.textContent).toContain('Issues found')
    expect(container.querySelector('[aria-label="Replay HAR downloads"]'), 'no downloads for a session the controller could not confirm').toBeNull()
  })

  it('does not let an older status response overwrite a newer terminal WebSocket result', async () => {
    const onComplete = vi.fn()
    let resolveStatus: ((value: unknown) => void) | undefined
    requestMock.mockReturnValueOnce(new Promise(resolve => { resolveStatus = resolve }))

    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-ws-wins" onComplete={onComplete} />)
    })

    const wsReport = {
      replay_session_id: 'rep-ws-wins',
      group: 'Recorder replay',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      success: true,
      events: [],
    }
    await send({ type: 'browser_replay_completed', replay_session_id: 'rep-ws-wins', report: wsReport } as any)
    await act(async () => {
      resolveStatus?.({
        session: {
          id: 'rep-ws-wins',
          status: 'failed',
          group: 'Recorder replay',
          protocol: 'browser',
          current_event_index: -1,
          error: 'stale status',
          report: { ...wsReport, success: false, error: 'stale status' },
        },
      })
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Verified ✓')
    expect(container.textContent).not.toContain('Issues found')
    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledWith(wsReport, [])
  })

  it('keeps polling an unfinalized bridge stop until the final WebSocket result arrives', async () => {
    vi.useFakeTimers()
    const onComplete = vi.fn()
    const bridge = {
      origin: 'http://127.0.0.1:27777',
      token: 'bridge-token',
      session_id: 'bridge-session',
      paired_at: '2026-06-22T00:00:00Z',
    }
    bridgeRequestMock.mockResolvedValueOnce({
      session: {
        id: 'rep-bridge',
        status: 'stopped',
        group: 'Recorder replay',
        protocol: 'browser',
        current_event_index: -1,
      },
    })

    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-bridge" bridge={bridge} onComplete={onComplete} />)
      await Promise.resolve()
    })

    expect(bridgeRequestMock).toHaveBeenCalledWith(
      bridge,
      '/browser-replay/sessions/rep-bridge',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(requestMock).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Verifying…')
    expect(onComplete).not.toHaveBeenCalled()

    await act(async () => {
      vi.advanceTimersByTime(1500)
      await Promise.resolve()
    })
    expect(bridgeRequestMock).toHaveBeenCalledTimes(2)

    const finalReport = {
      replay_session_id: 'rep-bridge',
      group: 'Recorder replay',
      protocol: 'browser',
      started_at: '2026-06-22T00:00:00Z',
      completed_at: '2026-06-22T00:00:01Z',
      success: false,
      events: [],
    }
    await send({
      type: 'browser_replay_stopped',
      replay_session_id: 'rep-bridge',
      report: finalReport,
    } as any)

    expect(container.textContent).toContain('Stopped')
    expect(onComplete).toHaveBeenCalledWith(finalReport, [])
  })

  it('accepts and rejects replay repair suggestions', async () => {
    const onApplySuggestion = vi.fn()
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-5" onApplySuggestion={onApplySuggestion} />)
    })
    await send({
      type: 'browser_replay_event',
      replay_session_id: 'rep-5',
      event: {
        event_index: 0,
        action_name: 'Click checkout',
        action_type: 'browser_click',
        success: true,
        duration_ms: 10,
        repair_suggestions: [{
          id: 'promote-1',
          title: 'Promote selector',
          description: 'Use healed selector',
          confidence: 0.95,
          draft_patch_type: 'promote_selector',
          fields: { selector: '#new', recorder_step_seq: '2' },
        }],
      },
    } as any)

    const fixButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === '1 fix')
    expect(fixButton).toBeTruthy()
    await act(async () => {
      fixButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const accept = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Accept')
    expect(accept).toBeTruthy()
    await act(async () => {
      accept?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onApplySuggestion).toHaveBeenCalledWith(expect.objectContaining({ id: 'promote-1' }))

    const reject = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Reject')
    expect(reject).toBeTruthy()
    await act(async () => {
      reject?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).not.toContain('Promote selector')
  })

  it('deduplicates rapid accepts for the same replay repair suggestion', async () => {
    let resolveApply!: () => void
    const onApplySuggestion = vi.fn().mockReturnValue(new Promise<void>(resolve => { resolveApply = resolve }))
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="rep-rapid-apply" onApplySuggestion={onApplySuggestion} />)
    })
    await send({
      type: 'browser_replay_event',
      replay_session_id: 'rep-rapid-apply',
      event: {
        event_index: 0,
        action_name: 'Click checkout',
        action_type: 'browser_click',
        success: true,
        repair_suggestions: [{
          id: 'promote-rapid',
          title: 'Promote selector',
          description: 'Use healed selector',
          confidence: 0.95,
        }],
      },
    } as any)
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent === '1 fix')?.click()
    })
    const accept = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Accept')
    expect(accept).toBeTruthy()
    act(() => {
      accept?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      accept?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onApplySuggestion).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveApply()
      await Promise.resolve()
    })
  })

  it('resets terminal replay evidence when the project scope changes', async () => {
    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="shared-replay-id" projectId="project-1" />)
    })
    await send({
      type: 'browser_replay_completed',
      replay_session_id: 'shared-replay-id',
      report: {
        replay_session_id: 'shared-replay-id',
        success: true,
        events: [{
          event_index: 0,
          action_name: 'Project one action',
          action_type: 'browser_click',
          success: true,
        }],
      },
    } as any)
    expect(container.textContent).toContain('Verified ✓')
    expect(container.textContent).toContain('Project one action')

    await act(async () => {
      root.render(<BrowserReplayProgress replaySessionId="shared-replay-id" projectId="project-2" />)
    })
    expect(container.textContent).toContain('Verifying…')
    expect(container.textContent).not.toContain('Project one action')
  })
})
