// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  acceptToFiltersJson,
  canUseNativeDialogs,
  fileFromBase64,
  fileNameFromPath,
  pickFilesViaBridge,
} from './desktopFiles'
import type { WailsDesktopBridge } from './desktopBridge'

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

describe('fileNameFromPath', () => {
  it('handles POSIX and Windows separators', () => {
    expect(fileNameFromPath('/Users/x/order-flow.json')).toBe('order-flow.json')
    expect(fileNameFromPath('C:\\Users\\x\\archive.zip')).toBe('archive.zip')
    expect(fileNameFromPath('bare.json')).toBe('bare.json')
  })
})

describe('acceptToFiltersJson', () => {
  it('builds a semicolon pattern from extension tokens', () => {
    expect(acceptToFiltersJson('.zip')).toBe(JSON.stringify([{ displayName: 'Supported files', pattern: '*.zip' }]))
    expect(acceptToFiltersJson('.har,.json,application/json')).toBe(
      JSON.stringify([{ displayName: 'Supported files', pattern: '*.har;*.json' }]),
    )
  })

  it('returns empty when no extension tokens are present', () => {
    expect(acceptToFiltersJson('')).toBe('')
    expect(acceptToFiltersJson('application/json')).toBe('')
  })
})

describe('fileFromBase64', () => {
  it('reconstructs a File with the path basename and decoded content', async () => {
    const base64 = btoa('{"name":"x"}')
    const file = fileFromBase64('/tmp/order.json', base64)
    expect(file.name).toBe('order.json')
    expect(await file.text()).toBe('{"name":"x"}')
  })
})

describe('canUseNativeDialogs', () => {
  it('reflects the bound dialog method', () => {
    expect(canUseNativeDialogs()).toBe(false)
    setBridge({ OpenFileDialogNative: vi.fn() })
    expect(canUseNativeDialogs()).toBe(true)
  })
})

describe('pickFilesViaBridge', () => {
  it('returns [] when the bridge is unavailable', async () => {
    expect(await pickFilesViaBridge('.zip')).toEqual([])
  })

  it('reads a single picked file through the bridge', async () => {
    setBridge({
      OpenFileDialogNative: vi.fn().mockResolvedValue('/tmp/order.json'),
      ReadFileBase64: vi.fn().mockResolvedValue(btoa('hello')),
    })
    const files = await pickFilesViaBridge('.json')
    expect(files).toHaveLength(1)
    expect(files[0].name).toBe('order.json')
    expect(await files[0].text()).toBe('hello')
  })

  it('returns [] when the user cancels (empty path)', async () => {
    setBridge({
      OpenFileDialogNative: vi.fn().mockResolvedValue(''),
      ReadFileBase64: vi.fn(),
    })
    expect(await pickFilesViaBridge('.json')).toEqual([])
  })
})
