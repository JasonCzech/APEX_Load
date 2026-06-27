// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { canSaveBlobViaBridge, MAX_NATIVE_SAVE_BYTES, saveBlobViaBridge } from './desktopFiles'
import type { WailsDesktopBridge } from './desktopBridge'

type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

function setBridge(bridge: WailsDesktopBridge | undefined) {
  const w = window as BridgeWindow
  if (bridge) w.go = { main: { App: bridge } }
  else delete w.go
}

afterEach(() => {
  setBridge(undefined)
  vi.restoreAllMocks()
})

describe('saveBlobViaBridge', () => {
  it('reports unavailable without the bridge', async () => {
    const blob = new Blob(['hello'], { type: 'text/plain' })
    expect(canSaveBlobViaBridge(blob)).toBe(false)
    expect(await saveBlobViaBridge(blob, 'out.txt')).toBe(false)
  })

  it('writes the chosen path via the bridge with an extension filter', async () => {
    const save = vi.fn().mockResolvedValue('/Users/x/report.csv')
    const write = vi.fn().mockResolvedValue(undefined)
    setBridge({ SaveFileDialogNative: save, WriteFileBase64: write })

    const blob = new Blob(['a,b,c\n1,2,3\n'], { type: 'text/csv' })
    expect(canSaveBlobViaBridge(blob)).toBe(true)
    expect(await saveBlobViaBridge(blob, 'report.csv')).toBe(true)

    expect(save).toHaveBeenCalledWith('Save', 'report.csv', JSON.stringify([{ displayName: 'CSV file', pattern: '*.csv' }]))
    expect(write).toHaveBeenCalledTimes(1)
    const [path, base64] = write.mock.calls[0]
    expect(path).toBe('/Users/x/report.csv')
    expect(atob(base64)).toBe('a,b,c\n1,2,3\n')
  })

  it('treats a cancelled dialog as handled (no browser fallback)', async () => {
    const save = vi.fn().mockResolvedValue('')
    const write = vi.fn()
    setBridge({ SaveFileDialogNative: save, WriteFileBase64: write })
    expect(await saveBlobViaBridge(new Blob(['x']), 'a.json')).toBe(true)
    expect(write).not.toHaveBeenCalled()
  })

  it('declines oversized blobs so the browser can stream them', () => {
    setBridge({ SaveFileDialogNative: vi.fn(), WriteFileBase64: vi.fn() })
    const big = { size: MAX_NATIVE_SAVE_BYTES + 1 } as Blob
    expect(canSaveBlobViaBridge(big)).toBe(false)
  })
})
