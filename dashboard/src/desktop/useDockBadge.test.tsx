// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

import { useDockBadge } from './useDockBadge'
import type { WailsDesktopBridge } from './desktopBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

function Harness({ count }: { count: number }) {
  useDockBadge(count)
  return null
}

describe('useDockBadge', () => {
  let container: HTMLDivElement
  let root: Root
  let setDockBadge: Mock<(label: string) => Promise<void>>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    setDockBadge = vi.fn<(label: string) => Promise<void>>().mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { SetDockBadge: setDockBadge } } }
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    delete (window as BridgeWindow).go
    vi.restoreAllMocks()
  })

  it('sets the badge to the active-run count and clears at zero, only on change', async () => {
    await act(async () => { root.render(<Harness count={0} />) })
    expect(setDockBadge).toHaveBeenLastCalledWith('')

    await act(async () => { root.render(<Harness count={2} />) })
    expect(setDockBadge).toHaveBeenLastCalledWith('2')

    await act(async () => { root.render(<Harness count={3} />) })
    expect(setDockBadge).toHaveBeenLastCalledWith('3')

    await act(async () => { root.render(<Harness count={0} />) })
    expect(setDockBadge).toHaveBeenLastCalledWith('')
    expect(setDockBadge).toHaveBeenCalledTimes(4)
  })

  it('is inert without the bridge', async () => {
    delete (window as BridgeWindow).go
    await act(async () => { root.render(<Harness count={5} />) })
    expect(setDockBadge).not.toHaveBeenCalled()
  })
})
