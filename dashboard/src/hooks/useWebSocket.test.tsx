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
  closed = false

  constructor(public readonly url: string) {
    MockWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
    this.onclose?.(new CloseEvent('close'))
  }
}

function Harness({ projectId, bridge, onOpen }: { projectId?: string; bridge?: any; onOpen?: () => void }) {
  const { connected, connectionState } = useWebSocket({ projectId, bridge, onOpen })

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

  function dispatchVisibility(value: DocumentVisibilityState) {
    act(() => {
      visibilityState = value
      document.dispatchEvent(new Event('visibilitychange'))
    })
  }

  async function openSocket() {
    flushDebounce()
    await act(async () => {
      MockWebSocket.instances[MockWebSocket.instances.length - 1]?.onopen?.(new Event('open'))
    })
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

  it('uses the paired bridge websocket session when provided', async () => {
    await act(async () => {
      root.render(<Harness bridge={{ origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }} />)
    })
    flushDebounce()

    expect(MockWebSocket.instances[0]?.url).toBe('ws://127.0.0.1:49152/api/v1/ws?bridge_session_id=ws-1')
  })

  it('still connects when the tab is hidden so the live stream is not interrupted by tab blur', async () => {
    visibilityState = 'hidden'

    await act(async () => {
      root.render(<Harness />)
    })

    // Hidden or visible, we advertise connecting and open a socket so metrics
    // keep streaming while the dashboard tab is backgrounded.
    expect(state()?.getAttribute('data-connection-state')).toBe('connecting')
    flushDebounce()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('keeps the open socket alive when the tab is backgrounded', async () => {
    await act(async () => {
      root.render(<Harness />)
    })
    await openSocket()
    expect(state()?.getAttribute('data-connection-state')).toBe('connected')

    dispatchVisibility('hidden')

    // The socket must NOT be torn down on blur — that is exactly what produced
    // the multi-minute graph gaps.
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0]?.closed).toBe(false)
    expect(state()?.getAttribute('data-connection-state')).toBe('connected')
  })

  it('reconnects immediately when the tab becomes visible and the socket had dropped', async () => {
    await act(async () => {
      root.render(<Harness />)
    })
    await openSocket()

    // Simulate the browser closing the frozen/backgrounded tab's socket.
    await act(async () => {
      MockWebSocket.instances[0]?.onclose?.(new CloseEvent('close'))
    })
    expect(state()?.getAttribute('data-connection-state')).toBe('disconnected')
    expect(MockWebSocket.instances).toHaveLength(1)

    // Returning to the tab reconnects right away, without waiting out the 3s
    // backoff (which the browser may have throttled while hidden).
    dispatchVisibility('visible')
    expect(MockWebSocket.instances).toHaveLength(2)
  })

  it('invokes onOpen each time a socket opens so consumers can backfill', async () => {
    const onOpen = vi.fn()
    await act(async () => {
      root.render(<Harness onOpen={onOpen} />)
    })
    await openSocket()
    expect(onOpen).toHaveBeenCalledTimes(1)

    // A drop + reconnect fires onOpen again.
    await act(async () => {
      MockWebSocket.instances[0]?.onclose?.(new CloseEvent('close'))
    })
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    await act(async () => {
      MockWebSocket.instances[1]?.onopen?.(new Event('open'))
    })
    expect(onOpen).toHaveBeenCalledTimes(2)
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
