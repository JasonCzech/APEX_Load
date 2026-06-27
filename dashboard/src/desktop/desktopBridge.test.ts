// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  desktopCapabilities,
  getDesktopBridge,
  isDesktopBridgeAvailable,
  isDesktopSurface,
  isWailsLocation,
  type WailsDesktopBridge,
} from './desktopBridge'

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

function setBridge(bridge: WailsDesktopBridge | undefined) {
  const w = window as BridgeWindow
  if (bridge) {
    w.go = { main: { App: bridge } }
  } else {
    delete w.go
  }
}

afterEach(() => {
  setBridge(undefined)
  vi.restoreAllMocks()
})

describe('isWailsLocation', () => {
  it('matches the macOS wails:// origin and *.localhost', () => {
    expect(isWailsLocation({ protocol: 'wails:', hostname: 'wails' })).toBe(true)
    expect(isWailsLocation({ protocol: 'http:', hostname: 'wails.localhost' })).toBe(true)
  })

  it('rejects ordinary web origins and nullish input', () => {
    expect(isWailsLocation({ protocol: 'https:', hostname: 'apexload.example.com' })).toBe(false)
    expect(isWailsLocation(undefined)).toBe(false)
    expect(isWailsLocation(null)).toBe(false)
  })
})

describe('bridge detection', () => {
  it('reports the bound bridge when present', () => {
    expect(isDesktopBridgeAvailable()).toBe(false)
    expect(getDesktopBridge()).toBeUndefined()

    const bridge: WailsDesktopBridge = { OpenInBrowser: vi.fn() }
    setBridge(bridge)

    expect(getDesktopBridge()).toBe(bridge)
    expect(isDesktopBridgeAvailable()).toBe(true)
    expect(isDesktopSurface()).toBe(true)
  })
})

describe('desktopCapabilities', () => {
  it('returns an all-false map in the browser', () => {
    const caps = desktopCapabilities(undefined)
    expect(caps).toEqual({
      isDesktop: false,
      nativeDialogs: false,
      fileDrop: false,
      notifications: false,
      reveal: false,
      recents: false,
      diagnostics: false,
      externalLinks: false,
    })
  })

  it('derives capabilities from the bound method surface', () => {
    const bridge: WailsDesktopBridge = {
      OpenFileDialogNative: vi.fn(),
      Notify: vi.fn(),
      RevealInFolder: vi.fn(),
    }
    const caps = desktopCapabilities(bridge)
    expect(caps.isDesktop).toBe(true)
    expect(caps.fileDrop).toBe(true) // shell-level, always on in desktop
    expect(caps.nativeDialogs).toBe(true)
    expect(caps.notifications).toBe(true)
    expect(caps.reveal).toBe(true)
    expect(caps.recents).toBe(false)
    expect(caps.diagnostics).toBe(false)
    expect(caps.externalLinks).toBe(false)
  })
})
