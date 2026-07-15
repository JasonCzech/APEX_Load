// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserReplayProgress from './BrowserReplayProgress'
import { type WSMessage } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
})
