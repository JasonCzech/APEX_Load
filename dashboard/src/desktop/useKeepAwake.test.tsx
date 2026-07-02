// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useKeepAwakeDuringRuns } from './useKeepAwake'
import type { WailsDesktopBridge } from './desktopBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

function Harness({ count }: { count: number }) {
  useKeepAwakeDuringRuns(count)
  return null
}

describe('useKeepAwakeDuringRuns', () => {
  let container: HTMLDivElement
  let root: Root
  let setKeepAwake: ReturnType<typeof vi.fn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    setKeepAwake = vi.fn().mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { SetKeepAwake: setKeepAwake } } }
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    delete (window as BridgeWindow).go
    vi.restoreAllMocks()
  })

  it('engages on first active run and only calls on transitions', async () => {
    await act(async () => { root.render(<Harness count={0} />) })
    expect(setKeepAwake).toHaveBeenLastCalledWith(false)

    await act(async () => { root.render(<Harness count={1} />) })
    expect(setKeepAwake).toHaveBeenLastCalledWith(true)

    // 1 → 2 active runs: still awake, no redundant bridge call.
    await act(async () => { root.render(<Harness count={2} />) })
    expect(setKeepAwake).toHaveBeenCalledTimes(2)

    // Back to 0: release.
    await act(async () => { root.render(<Harness count={0} />) })
    expect(setKeepAwake).toHaveBeenLastCalledWith(false)
    expect(setKeepAwake).toHaveBeenCalledTimes(3)
  })

  it('is inert without the bridge', async () => {
    delete (window as BridgeWindow).go
    await act(async () => { root.render(<Harness count={3} />) })
    expect(setKeepAwake).not.toHaveBeenCalled()
  })
})
