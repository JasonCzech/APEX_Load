// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import { registerDropZone } from './useDesktopFileDrop'

type DropCb = (x: number, y: number, paths: string[]) => void
type RuntimeWindow = typeof window & {
  runtime?: { OnFileDrop?: (cb: DropCb, useTarget: boolean) => void; OnFileDropOff?: () => void }
}

afterEach(() => {
  const w = window as RuntimeWindow
  delete w.runtime
  vi.restoreAllMocks()
})

describe('registerDropZone', () => {
  it('routes dropped paths to the zone containing the drop point', () => {
    let captured: DropCb | undefined
    const offSpy = vi.fn()
    ;(window as RuntimeWindow).runtime = {
      OnFileDrop: cb => { captured = cb },
      OnFileDropOff: offSpy,
    }

    const zone = document.createElement('div')
    const child = document.createElement('span')
    zone.appendChild(child)
    document.body.appendChild(zone)

    // jsdom does not implement elementFromPoint; stub it.
    const elementFromPoint = vi.fn<(x: number, y: number) => Element | null>().mockReturnValue(child)
    ;(document as unknown as { elementFromPoint: typeof elementFromPoint }).elementFromPoint = elementFromPoint

    const handler = vi.fn()
    const unregister = registerDropZone(zone, handler)

    expect(captured).toBeTypeOf('function')
    captured?.(10, 10, ['/tmp/a.zip'])
    expect(handler).toHaveBeenCalledWith(['/tmp/a.zip'])

    // Drop outside any registered zone is ignored.
    handler.mockClear()
    elementFromPoint.mockReturnValue(document.body)
    captured?.(999, 999, ['/tmp/b.zip'])
    expect(handler).not.toHaveBeenCalled()

    unregister()
    expect(offSpy).toHaveBeenCalled()
    zone.remove()
  })

  it('is inert when the Wails runtime is absent', () => {
    const zone = document.createElement('div')
    const handler = vi.fn()
    // Should not throw without window.runtime.
    const unregister = registerDropZone(zone, handler)
    unregister()
    expect(handler).not.toHaveBeenCalled()
  })
})
