// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WailsDesktopBridge } from '../desktop/desktopBridge'
import { MAX_NATIVE_SAVE_BYTES } from '../desktop/desktopFiles'
import { downloadBlob, downloadTextFile } from './fileUtils'

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

let originalCreateObjectURL: typeof URL.createObjectURL | undefined
let originalRevokeObjectURL: typeof URL.revokeObjectURL | undefined

function setBridge(bridge: WailsDesktopBridge | undefined) {
  const bridgeWindow = window as BridgeWindow
  if (bridge) bridgeWindow.go = { main: { App: bridge } }
  else delete bridgeWindow.go
}

beforeEach(() => {
  originalCreateObjectURL = URL.createObjectURL
  originalRevokeObjectURL = URL.revokeObjectURL
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: vi.fn().mockReturnValue('blob:test-download'),
  })
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: vi.fn(),
  })
})

afterEach(() => {
  setBridge(undefined)
  if (originalCreateObjectURL) {
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: originalCreateObjectURL })
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL')
  }
  if (originalRevokeObjectURL) {
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: originalRevokeObjectURL })
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL')
  }
  vi.restoreAllMocks()
})

describe('downloadBlob', () => {
  it('starts a browser download when the native bridge is unavailable', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await expect(downloadBlob(new Blob(['hello']), 'report.txt')).resolves.toBe('browser_started')

    expect(click).toHaveBeenCalledTimes(1)
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:test-download')
  })

  it('retains the hosted-browser fallback for an oversized Blob', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    const oversized = { size: MAX_NATIVE_SAVE_BYTES + 1 } as Blob

    await expect(downloadBlob(oversized, 'large-report.zip')).resolves.toBe('browser_started')

    expect(click).toHaveBeenCalledTimes(1)
    expect(URL.createObjectURL).toHaveBeenCalledWith(oversized)
  })

  it('awaits a completed native text save', async () => {
    const write = vi.fn().mockResolvedValue(undefined)
    setBridge({
      SaveFileDialogNative: vi.fn().mockResolvedValue('/tmp/report.txt'),
      WriteFileBase64: write,
    })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await expect(downloadTextFile('hello', 'report.txt')).resolves.toBe('saved')

    expect(write).toHaveBeenCalledTimes(1)
    expect(click).not.toHaveBeenCalled()
  })

  it('reports native cancellation without starting a browser fallback', async () => {
    setBridge({
      SaveFileDialogNative: vi.fn().mockResolvedValue(''),
      WriteFileBase64: vi.fn(),
    })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await expect(downloadBlob(new Blob(['hello']), 'report.txt')).resolves.toBe('cancelled')

    expect(click).not.toHaveBeenCalled()
  })

  it('propagates native write errors without starting a browser fallback', async () => {
    const error = new Error('disk is full')
    setBridge({
      SaveFileDialogNative: vi.fn().mockResolvedValue('/tmp/report.txt'),
      WriteFileBase64: vi.fn().mockRejectedValue(error),
    })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await expect(downloadBlob(new Blob(['hello']), 'report.txt')).rejects.toBe(error)

    expect(click).not.toHaveBeenCalled()
  })

  it('rejects an oversized generated Blob on Wails without starting an anchor download', async () => {
    const save = vi.fn()
    const write = vi.fn()
    setBridge({ SaveFileDialogNative: save, WriteFileBase64: write })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    const oversized = { size: MAX_NATIVE_SAVE_BYTES + 1 } as Blob

    await expect(downloadBlob(oversized, 'large-report.zip')).rejects.toThrow('too large for the desktop save bridge')

    expect(save).not.toHaveBeenCalled()
    expect(write).not.toHaveBeenCalled()
    expect(click).not.toHaveBeenCalled()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('fails closed on an older partial Wails bridge instead of using an anchor download', async () => {
    setBridge({ OpenURL: vi.fn().mockResolvedValue(undefined) })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await expect(downloadBlob(new Blob(['report']), 'report.txt')).rejects.toThrow(
      'native save bridge is unavailable',
    )

    expect(click).not.toHaveBeenCalled()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })
})
