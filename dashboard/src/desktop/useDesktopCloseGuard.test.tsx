// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { reloadPage } from './reloadPage'
import { DESKTOP_CLOSE_REQUESTED_EVENT, DESKTOP_RELOAD_REQUESTED_EVENT, useDesktopCloseGuard } from './useDesktopCloseGuard'
import type { WailsDesktopBridge } from './desktopBridge'
import type { WailsEventRuntime } from './wailsRuntime'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('./reloadPage', () => ({ reloadPage: vi.fn() }))

type DesktopWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } }; runtime?: WailsEventRuntime }

function Harness({ runGuarded, enabled }: { runGuarded: (proceed: () => void) => void; enabled?: boolean }) {
  useDesktopCloseGuard(runGuarded, enabled)
  return null
}

describe('useDesktopCloseGuard', () => {
  let container: HTMLDivElement
  let root: Root
  let listeners: Map<string, (...data: unknown[]) => void>
  let windowShow: ReturnType<typeof vi.fn<() => void>>
  let windowUnminimise: ReturnType<typeof vi.fn<() => void>>
  let bridge: Required<Pick<WailsDesktopBridge, 'EnableCloseGuard' | 'DisableCloseGuard' | 'AcknowledgeCloseRequest' | 'ConfirmClose'>>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    listeners = new Map()
    bridge = {
      EnableCloseGuard: vi.fn().mockResolvedValue('guard-token'),
      DisableCloseGuard: vi.fn().mockResolvedValue(undefined),
      AcknowledgeCloseRequest: vi.fn().mockResolvedValue(undefined),
      ConfirmClose: vi.fn().mockResolvedValue(undefined),
    }
    ;(window as DesktopWindow).go = { main: { App: bridge } }
    windowShow = vi.fn<() => void>()
    windowUnminimise = vi.fn<() => void>()
    ;(window as DesktopWindow).runtime = {
      EventsOn: (name, callback) => {
        listeners.set(name, callback)
        return () => listeners.delete(name)
      },
      WindowShow: windowShow,
      WindowUnminimise: windowUnminimise,
    }
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    delete (window as DesktopWindow).go
    delete (window as DesktopWindow).runtime
    vi.restoreAllMocks()
  })

  it('takes a native close request and quits only once the unsaved-work prompt proceeds', async () => {
    let pendingProceed: (() => void) | undefined
    const runGuarded = vi.fn((proceed: () => void) => { pendingProceed = proceed })
    await act(async () => { root.render(<Harness runGuarded={runGuarded} />) })
    expect(bridge.EnableCloseGuard).toHaveBeenCalledTimes(1)

    await act(async () => { listeners.get(DESKTOP_CLOSE_REQUESTED_EVENT)?.('3') })
    expect(bridge.AcknowledgeCloseRequest).toHaveBeenCalledWith('guard-token', '3')
    expect(runGuarded).toHaveBeenCalledTimes(1)
    expect(bridge.ConfirmClose).not.toHaveBeenCalled()
    // The quit asks, so the window comes forward (a Dock Quit while minimized).
    expect(windowUnminimise).toHaveBeenCalledTimes(1)
    expect(windowShow).toHaveBeenCalledTimes(1)

    await act(async () => { pendingProceed?.() })
    expect(bridge.ConfirmClose).toHaveBeenCalledWith('guard-token', '3')
  })

  // View > Reload asks the same prompt; the page reloads itself once the work
  // is resolved, and Keep editing (no proceed) leaves it as it is.
  it('reloads only once the unsaved-work prompt proceeds', async () => {
    vi.mocked(reloadPage).mockClear()
    let pendingProceed: (() => void) | undefined
    const runGuarded = vi.fn((proceed: () => void) => { pendingProceed = proceed })
    await act(async () => { root.render(<Harness runGuarded={runGuarded} />) })

    await act(async () => { listeners.get(DESKTOP_RELOAD_REQUESTED_EVENT)?.('4') })
    expect(bridge.AcknowledgeCloseRequest).toHaveBeenCalledWith('guard-token', '4')
    expect(reloadPage).not.toHaveBeenCalled()
    expect(bridge.ConfirmClose).not.toHaveBeenCalled()

    await act(async () => { pendingProceed?.() })
    expect(reloadPage).toHaveBeenCalledTimes(1)
    expect(bridge.ConfirmClose).not.toHaveBeenCalled()
  })

  it('quits without raising the window when nothing needs an answer', async () => {
    await act(async () => { root.render(<Harness runGuarded={proceed => proceed()} />) })
    await act(async () => { listeners.get(DESKTOP_CLOSE_REQUESTED_EVENT)?.('1') })
    expect(bridge.ConfirmClose).toHaveBeenCalledWith('guard-token', '1')
    expect(windowShow).not.toHaveBeenCalled()
  })

  it('registers nothing while disabled', async () => {
    await act(async () => { root.render(<Harness runGuarded={vi.fn()} enabled={false} />) })
    expect(bridge.EnableCloseGuard).not.toHaveBeenCalled()
    expect(listeners.size).toBe(0)
  })

  it('ignores malformed requests and releases the guard on unmount', async () => {
    const runGuarded = vi.fn()
    await act(async () => { root.render(<Harness runGuarded={runGuarded} />) })
    await act(async () => { listeners.get(DESKTOP_CLOSE_REQUESTED_EVENT)?.('0') })
    await act(async () => { listeners.get(DESKTOP_CLOSE_REQUESTED_EVENT)?.(3) })
    expect(runGuarded).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
    root = createRoot(container)
    expect(bridge.DisableCloseGuard).toHaveBeenCalledWith('guard-token')
    expect(listeners.has(DESKTOP_CLOSE_REQUESTED_EVENT)).toBe(false)
  })

  it('is inert in the browser', async () => {
    delete (window as DesktopWindow).go
    const runGuarded = vi.fn()
    await act(async () => { root.render(<Harness runGuarded={runGuarded} />) })
    expect(listeners.size).toBe(0)
    expect(bridge.EnableCloseGuard).not.toHaveBeenCalled()
  })
})
