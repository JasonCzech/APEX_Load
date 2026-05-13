// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useWebSocket } from './useWebSocket'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

function Harness() {
  const { connected, connectionState } = useWebSocket()

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

  beforeEach(() => {
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
    vi.restoreAllMocks()
  })

  it('starts in connecting state before the websocket handshake completes', async () => {
    await act(async () => {
      root.render(<Harness />)
    })

    const stateNode = container.querySelector('[data-testid="state"]')
    expect(stateNode?.getAttribute('data-connection-state')).toBe('connecting')
    expect(stateNode?.getAttribute('data-connected')).toBe('false')
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('marks the hook connected once the websocket opens', async () => {
    await act(async () => {
      root.render(<Harness />)
    })

    await act(async () => {
      MockWebSocket.instances[0]?.onopen?.(new Event('open'))
    })

    const stateNode = container.querySelector('[data-testid="state"]')
    expect(stateNode?.getAttribute('data-connection-state')).toBe('connected')
    expect(stateNode?.getAttribute('data-connected')).toBe('true')
  })
})