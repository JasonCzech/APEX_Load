// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useDesktopShellEvents } from './useDesktopShellEvents'
import type { WailsDesktopBridge } from './desktopBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type DesktopWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } }
  runtime?: { EventsOn?: (event: string, cb: (arg?: unknown) => void) => () => void }
}

function Harness({ onMenuAbout, onRecentsChanged }: { onMenuAbout?: () => void; onRecentsChanged?: () => void }) {
  useDesktopShellEvents({ onMenuAbout, onRecentsChanged })
  return null
}

describe('useDesktopShellEvents', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    delete (window as DesktopWindow).go
    delete (window as DesktopWindow).runtime
    vi.restoreAllMocks()
  })

  it('routes external links to the system browser', async () => {
    const openURL = vi.fn().mockResolvedValue(undefined)
    ;(window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } }

    await act(async () => { root.render(<Harness />) })

    const anchor = document.createElement('a')
    anchor.href = 'https://docs.example.com/guide'
    anchor.textContent = 'docs'
    container.appendChild(anchor)

    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })
    await act(async () => { anchor.dispatchEvent(event) })

    expect(openURL).toHaveBeenCalledWith('https://docs.example.com/guide')
    expect(event.defaultPrevented).toBe(true)
  })

  it('ignores internal hash links', async () => {
    const openURL = vi.fn().mockResolvedValue(undefined)
    ;(window as DesktopWindow).go = { main: { App: { OpenURL: openURL } } }
    await act(async () => { root.render(<Harness />) })

    const anchor = document.createElement('a')
    anchor.setAttribute('href', '#/scripts')
    container.appendChild(anchor)
    await act(async () => { anchor.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })) })

    expect(openURL).not.toHaveBeenCalled()
  })

  it('invokes onMenuAbout when the native menu emits menu:about', async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {}
    ;(window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => { listeners[event] = cb; return () => { delete listeners[event] } },
    }
    const onMenuAbout = vi.fn()
    await act(async () => { root.render(<Harness onMenuAbout={onMenuAbout} />) })

    await act(async () => { listeners['menu:about']?.() })
    expect(onMenuAbout).toHaveBeenCalledTimes(1)
  })

  it('invokes onRecentsChanged when the native menu mutates recents', async () => {
    const listeners: Record<string, (arg?: unknown) => void> = {}
    ;(window as DesktopWindow).runtime = {
      EventsOn: (event, cb) => { listeners[event] = cb; return () => { delete listeners[event] } },
    }
    const onRecentsChanged = vi.fn()
    await act(async () => { root.render(<Harness onRecentsChanged={onRecentsChanged} />) })

    await act(async () => { listeners['desktop:recents-changed']?.() })
    expect(onRecentsChanged).toHaveBeenCalledTimes(1)
  })
})
