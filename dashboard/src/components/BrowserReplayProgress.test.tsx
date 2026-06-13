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
      report: { repair_suggestions: [{ id: 'r1', title: 'Stabilize selector', description: 'prefer role', confidence: 0.7 }] },
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
})
