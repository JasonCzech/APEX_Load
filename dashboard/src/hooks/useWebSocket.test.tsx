// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useWebSocket } from './useWebSocket'
import { DevViewProvider } from './DevViewProvider'
import { writeStoredDevViewMode } from './apiClient'

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

function Harness({ projectId, bridge, onOpen, onMessage }: { projectId?: string; bridge?: any; onOpen?: () => void; onMessage?: (message: any) => void }) {
  const { connected, connectionState } = useWebSocket({ projectId, bridge, onOpen, onMessage })

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
    window.localStorage.removeItem('apexload.devViewMode')
    window.localStorage.removeItem('apexload.activeProjectId')
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
    window.localStorage.removeItem('apexload.devViewMode')
    window.localStorage.removeItem('apexload.activeProjectId')
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

  it('reconnects with the narrowed preview identity when the mode changes', async () => {
    window.localStorage.setItem('apexload.activeProjectId', 'project-a')
    await act(async () => {
      root.render(<DevViewProvider><Harness projectId="project-a" /></DevViewProvider>)
    })
    await openSocket()
    expect(MockWebSocket.instances[0]?.url).not.toContain('apexload_dev_view')
    await act(async () => { writeStoredDevViewMode('user') })
    expect(MockWebSocket.instances[0]?.closed).toBe(true)
    flushDebounce()
    const url = new URL(MockWebSocket.instances[1]!.url)
    expect(url.searchParams.get('project_id')).toBe('project-a')
    expect(url.searchParams.get('apexload_dev_view')).toBe('user')
    expect(url.searchParams.get('apexload_dev_project')).toBe('project-a')
    await act(async () => { writeStoredDevViewMode('admin') })
    flushDebounce()
    expect(MockWebSocket.instances[1]?.closed).toBe(true)
    expect(MockWebSocket.instances[2]?.url).not.toContain('apexload_dev_view')
  })

  it('does not send preview hints to a recorder bridge', async () => {
    writeStoredDevViewMode('user')
    await act(async () => {
      root.render(<DevViewProvider><Harness bridge={{ origin: 'http://127.0.0.1:49152', token: 'tok', session_id: 'ws-1' }} /></DevViewProvider>)
    })
    flushDebounce()
    expect(MockWebSocket.instances[0]?.url).toBe('ws://127.0.0.1:49152/api/v1/ws?bridge_session_id=ws-1')
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
      // 3s base backoff plus the full 1s reconnect jitter window.
      vi.advanceTimersByTime(4000)
    })
    await act(async () => {
      MockWebSocket.instances[1]?.onopen?.(new Event('open'))
    })
    expect(onOpen).toHaveBeenCalledTimes(2)
  })

  it('backs off exponentially with jitter and caps the reconnect delay', async () => {
    // Pin the jitter to its maximum so the assertions can use exact boundaries:
    // the socket must not reappear at delay - 1, and must reappear at delay.
    vi.spyOn(Math, 'random').mockReturnValue(0.999999)

    await act(async () => {
      root.render(<Harness />)
    })
    await openSocket()

    // 3s, 6s, 12s, 24s, then clamped to the 30s ceiling — each with the full
    // 1s jitter window on top. A flat delay would fire every attempt at 3s.
    const expected = [3000, 6000, 12000, 24000, 30000, 30000]
    expected.forEach((base, attempt) => {
      const socketCount = MockWebSocket.instances.length
      act(() => {
        MockWebSocket.instances[socketCount - 1]?.onclose?.(new CloseEvent('close'))
      })
      expect(state()?.getAttribute('data-connection-state')).toBe('disconnected')

      act(() => {
        vi.advanceTimersByTime(base + 999 - 1)
      })
      expect(MockWebSocket.instances, `attempt ${attempt + 1} reconnected before its backoff elapsed`).toHaveLength(socketCount)

      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(MockWebSocket.instances, `attempt ${attempt + 1} did not reconnect after its backoff`).toHaveLength(socketCount + 1)
    })
  })

  it('resets the backoff once a reconnect succeeds', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(0)

    await act(async () => {
      root.render(<Harness />)
    })
    await openSocket()

    // Escalate to the second backoff step…
    act(() => {
      MockWebSocket.instances[0]?.onclose?.(new CloseEvent('close'))
    })
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    act(() => {
      MockWebSocket.instances[1]?.onclose?.(new CloseEvent('close'))
    })
    act(() => {
      vi.advanceTimersByTime(6000)
    })
    expect(MockWebSocket.instances).toHaveLength(3)

    // …then let the third socket actually open. The next drop must start over
    // at the base delay rather than continuing to 12s.
    await act(async () => {
      MockWebSocket.instances[2]?.onopen?.(new Event('open'))
    })
    act(() => {
      MockWebSocket.instances[2]?.onclose?.(new CloseEvent('close'))
    })
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(MockWebSocket.instances).toHaveLength(4)
  })

  it('cancels a pending reconnect timer on teardown', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(0)

    await act(async () => {
      root.render(<Harness />)
    })
    await openSocket()
    act(() => {
      MockWebSocket.instances[0]?.onclose?.(new CloseEvent('close'))
    })

    await act(async () => {
      root.unmount()
    })
    act(() => {
      vi.advanceTimersByTime(60000)
    })

    // The unmounted hook must not resurrect a socket from its queued backoff.
    expect(MockWebSocket.instances).toHaveLength(1)

    // Re-create the root so the shared afterEach unmount stays valid.
    root = createRoot(container)
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

  it('drops already-queued messages from the previous project socket', async () => {
    const onMessage = vi.fn()
    await act(async () => {
      root.render(<Harness projectId="project-1" onMessage={onMessage} />)
    })
    await openSocket()
    const oldMessageHandler = MockWebSocket.instances[0]?.onmessage

    await act(async () => {
      root.render(<Harness projectId="project-2" onMessage={onMessage} />)
    })
    await act(async () => {
      oldMessageHandler?.(new MessageEvent('message', { data: JSON.stringify({ type: 'test_update', test_id: 'old-test' }) }))
    })
    expect(onMessage).not.toHaveBeenCalled()

    flushDebounce()
    const currentSocket = MockWebSocket.instances[1]
    await act(async () => {
      currentSocket?.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'test_update', test_id: 'new-test' }) }))
    })
    expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ test_id: 'new-test' }))
  })
})
