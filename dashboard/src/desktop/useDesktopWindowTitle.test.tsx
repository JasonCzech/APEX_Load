// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useDesktopWindowTitle } from './useDesktopWindowTitle'
import type { WailsEventRuntimeWindow } from './wailsRuntime'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function Harness() {
  useDesktopWindowTitle()
  return null
}

describe('useDesktopWindowTitle', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { host = document.createElement('div'); document.body.append(host); root = createRoot(host); document.title = 'Dashboard | APEX Load' })
  afterEach(() => { act(() => root.unmount()); host.remove(); delete (window as WailsEventRuntimeWindow).runtime })

  it('follows document.title in the desktop shell', async () => {
    const WindowSetTitle = vi.fn()
    ;(window as WailsEventRuntimeWindow).runtime = { WindowSetTitle }
    act(() => root.render(<Harness />))
    expect(WindowSetTitle).toHaveBeenLastCalledWith('Dashboard | APEX Load')
    document.title = 'Results | APEX Load'
    await act(async () => { await Promise.resolve() })
    expect(WindowSetTitle).toHaveBeenLastCalledWith('Results | APEX Load')
    expect(WindowSetTitle).toHaveBeenCalledTimes(2)
  })

  it('does nothing in the browser', () => {
    const observe = vi.spyOn(MutationObserver.prototype, 'observe')
    // A runtime without WindowSetTitle (or none at all) must not be called or watched.
    ;(window as WailsEventRuntimeWindow).runtime = { Quit: vi.fn() }
    expect(() => act(() => root.render(<Harness />))).not.toThrow()
    delete (window as WailsEventRuntimeWindow).runtime
    act(() => root.render(<><Harness /><Harness /></>))
    expect(observe).not.toHaveBeenCalled()
    observe.mockRestore()
  })
})
