// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useWebSocket } from './useWebSocket'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The hook debounces (re)connects; keep this in sync with CONNECT_DEBOUNCE_MS.
const DEBOUNCE_MS = 150

class MockWebSocket {
  static instances: MockWebSocket[] = []

  onopen: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null

  constructor(public readonly url: string) {
    MockWebSocket.instances.push(this)
  }

  close() {
    this.onclose?.(new CloseEvent('close'))
  }
}

function Harness({ projectId }: { projectId?: string }) {
  const { connected, connectionState } = useWebSocket({ projectId })

  return (
    <div
      data-testid="state"
      data-connected={String(connected)}
      data-connection-state={connectionState}
    />
  )
}

describe('useWebSocket', () => {
  let container: HTMLDivElement
  let root: Root
  const originalWebSocket = globalThis.WebSocket
  let visibilityState: DocumentVisibilityState

  function flushDebounce() {
    act(() => {
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
  }

  function state() {
    return container.querySelector('[data-testid="state"]')
  }

  beforeEach(() => {
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    MockWebSocket.instances = []
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket
    visibilityState = 'visible'
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => visibilityState,
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    globalThis.WebSocket = originalWebSocket
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('starts in connecting state and opens a socket after the debounce', async () => {
    await act(async () => {
      root.render(<Harness />)
    })

    // Visible tab: advertises connecting immediately, but defers the socket.
    expect(state()?.getAttribute('data-connection-state')).toBe('connecting')
    expect(MockWebSocket.instances).toHaveLength(0)

    flushDebounce()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('marks the hook connected once the websocket opens', async () => {
    await act(async () => {
      root.render(<Harness />)
    })
    flushDebounce()

    await act(async () => {
      MockWebSocket.instances[0]?.onopen?.(new Event('open'))
    })

    expect(state()?.getAttribute('data-connection-state')).toBe('connected')
    expect(state()?.getAttribute('data-connected')).toBe('true')
  })

  it('adds the active project to the global websocket URL when provided', async () => {
    await act(async () => {
      root.render(<Harness projectId="project-1" />)
    })
    flushDebounce()

    expect(MockWebSocket.instances[0]?.url).toContain('/api/v1/ws?project_id=project-1')
  })

  it('reports disconnected (not stuck connecting) and opens no socket when the tab is hidden', async () => {
    visibilityState = 'hidden'

    await act(async () => {
      root.render(<Harness />)
    })
    flushDebounce()

    expect(state()?.getAttribute('data-connection-state')).toBe('disconnected')
    expect(MockWebSocket.instances).toHaveLength(0)
  })

  it('coalesces rapid projectId changes into a single socket', async () => {
    await act(async () => {
      root.render(<Harness projectId={undefined} />)
    })
    // projectId resolves before the debounce window elapses.
    await act(async () => {
      root.render(<Harness projectId="resolved-project" />)
    })

    flushDebounce()

    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0]?.url).toContain('project_id=resolved-project')
  })
})
