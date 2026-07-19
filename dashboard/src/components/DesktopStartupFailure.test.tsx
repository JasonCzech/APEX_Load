// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WailsDesktopBridge } from '../desktop/desktopBridge'
import type { WailsEventRuntime } from '../desktop/wailsRuntime'
import DesktopStartupFailure from './DesktopStartupFailure'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }
type RuntimeWindow = typeof window & { runtime?: WailsEventRuntime }

describe('DesktopStartupFailure', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    delete (window as BridgeWindow).go
    delete (window as RuntimeWindow).runtime
    vi.restoreAllMocks()
  })

  it('shows the native startup detail and API-independent recovery actions', async () => {
    const openLogs = vi.fn().mockResolvedValue(undefined)
    const openData = vi.fn().mockResolvedValue(undefined)
    const quit = vi.fn()
    ;(window as BridgeWindow).go = { main: { App: { OpenLogsFolder: openLogs, OpenAppDataFolder: openData } } }
    ;(window as RuntimeWindow).runtime = { Quit: quit }
    await act(async () => root.render(<DesktopStartupFailure message="bind: address already in use" />))

    expect(container.querySelector('[role="alert"]')).toBeTruthy()
    expect(container.textContent).toContain('Local engine failed to start')
    expect(container.textContent).toContain('bind: address already in use')
    expect(container.textContent).toContain('Quit and reopen APEX Load to retry the local engine.')
    expect(container.textContent).not.toContain('Retry startup')
    const buttons = Array.from(container.querySelectorAll('button'))
    await act(async () => buttons.find(button => button.textContent === 'Quit APEX Load')?.click())
    await act(async () => buttons.find(button => button.textContent === 'Open logs')?.click())
    await act(async () => buttons.find(button => button.textContent === 'Open app data')?.click())
    expect(quit).toHaveBeenCalledTimes(1)
    expect(openLogs).toHaveBeenCalledTimes(1)
    expect(openData).toHaveBeenCalledTimes(1)
  })
})
