// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { addRecent, clearRecents, getRecents } from './desktopRecents'
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

describe('desktopRecents', () => {
  it('returns [] without a bridge', async () => {
    expect(await getRecents()).toEqual([])
  })

  it('parses recent items from the bridge', async () => {
    setBridge({
      GetRecentItems: vi.fn().mockResolvedValue('[{"kind":"script","id":"s1","name":"Order","route":"#/scripts/s1"}]'),
    })
    const items = await getRecents()
    expect(items).toHaveLength(1)
    expect(items[0].name).toBe('Order')
  })

  it('returns [] on malformed bridge output', async () => {
    setBridge({ GetRecentItems: vi.fn().mockResolvedValue('not json') })
    expect(await getRecents()).toEqual([])
  })

  it('serializes items to AddRecentItem and ignores incomplete ones', async () => {
    const add = vi.fn().mockResolvedValue(undefined)
    setBridge({ AddRecentItem: add })
    const scopedRoute = '#/scripts?mode=edit&scriptId=s1&projectId=project-b'
    await addRecent({ kind: 'script', id: 's1', name: 'Order', route: scopedRoute })
    expect(add).toHaveBeenCalledWith(JSON.stringify({ kind: 'script', id: 's1', name: 'Order', route: scopedRoute }))

    add.mockClear()
    await addRecent({ kind: '', id: '', name: '', route: '' })
    expect(add).not.toHaveBeenCalled()
  })

  it('clears via the bridge', async () => {
    const clear = vi.fn().mockResolvedValue(undefined)
    setBridge({ ClearRecentItems: clear })
    await clearRecents()
    expect(clear).toHaveBeenCalled()
  })
})
