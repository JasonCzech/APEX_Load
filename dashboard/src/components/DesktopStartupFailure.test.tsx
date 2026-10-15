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
    Reflect.deleteProperty(navigator, 'clipboard')
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

  // The bridge returns the bundle's JSON: it is saved where the person
  // chooses, or copied when the shell has no Save dialog.
  it('saves the diagnostics bundle where the person chooses', async () => {
    const bundle = '{"product":"APEX Load Desktop"}'
    const exportDiagnostics = vi.fn().mockResolvedValue(bundle)
    const saveDialog = vi.fn().mockResolvedValue('/Users/me/apexload-diagnostics.json')
    const write = vi.fn().mockResolvedValue(undefined)
    ;(window as BridgeWindow).go = { main: { App: { ExportDiagnostics: exportDiagnostics, SaveFileDialogNative: saveDialog, WriteFileBase64: write } } }
    await act(async () => root.render(<DesktopStartupFailure message="bind: address already in use" />))
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Export diagnostics…')
    await act(async () => button?.click())
    expect(saveDialog).toHaveBeenCalledWith('Save', 'apexload-diagnostics.json', expect.any(String))
    expect(atob(write.mock.calls[0][1] as string)).toBe(bundle)
    expect(container.textContent).toContain('Diagnostics saved.')
    expect(container.textContent).not.toContain('product')
  })

  it('copies the diagnostics bundle when no Save dialog is available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    ;(window as BridgeWindow).go = { main: { App: { ExportDiagnostics: vi.fn().mockResolvedValue('{"os":"darwin"}') } } }
    await act(async () => root.render(<DesktopStartupFailure message="bind: address already in use" />))
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Export diagnostics…')
    await act(async () => button?.click())
    expect(writeText).toHaveBeenCalledWith('{"os":"darwin"}')
    expect(container.textContent).toContain('Diagnostics copied to the clipboard.')
  })
})
