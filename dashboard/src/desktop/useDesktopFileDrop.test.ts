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
    expect(offSpy).not.toHaveBeenCalled()
    zone.remove()
  })

  it('keeps the shared Wails listener across unmount and remount', () => {
    let captured: DropCb | undefined
    const onSpy = vi.fn((cb: DropCb) => { captured = cb })
    const offSpy = vi.fn()
    ;(window as RuntimeWindow).runtime = {
      OnFileDrop: onSpy,
      OnFileDropOff: offSpy,
    }

    const first = document.createElement('div')
    const second = document.createElement('div')
    document.body.append(first, second)
    const elementFromPoint = vi.fn<(x: number, y: number) => Element | null>().mockReturnValue(first)
    ;(document as unknown as { elementFromPoint: typeof elementFromPoint }).elementFromPoint = elementFromPoint

    const firstHandler = vi.fn()
    const unregisterFirst = registerDropZone(first, firstHandler)
    unregisterFirst()

    const secondHandler = vi.fn()
    const unregisterSecond = registerDropZone(second, secondHandler)
    elementFromPoint.mockReturnValue(second)
    captured?.(5, 5, ['/tmp/remounted.har'])

    expect(onSpy).toHaveBeenCalledTimes(1)
    expect(offSpy).not.toHaveBeenCalled()
    expect(firstHandler).not.toHaveBeenCalled()
    expect(secondHandler).toHaveBeenCalledWith(['/tmp/remounted.har'])

    unregisterSecond()
    first.remove()
    second.remove()
  })

  it('routes to the innermost zone when zones are nested and ignores empty paths', () => {
    let captured: DropCb | undefined
    ;(window as RuntimeWindow).runtime = { OnFileDrop: cb => { captured = cb }, OnFileDropOff: vi.fn() }

    const outer = document.createElement('div')
    const inner = document.createElement('div')
    const leaf = document.createElement('span')
    outer.appendChild(inner)
    inner.appendChild(leaf)
    document.body.appendChild(outer)

    const elementFromPoint = vi.fn<(x: number, y: number) => Element | null>().mockReturnValue(leaf)
    ;(document as unknown as { elementFromPoint: typeof elementFromPoint }).elementFromPoint = elementFromPoint

    const outerHandler = vi.fn()
    const innerHandler = vi.fn()
    const offOuter = registerDropZone(outer, outerHandler)
    const offInner = registerDropZone(inner, innerHandler)

    // Drop over the leaf → innermost registered ancestor (inner) wins.
    captured?.(5, 5, ['/tmp/a.zip'])
    expect(innerHandler).toHaveBeenCalledWith(['/tmp/a.zip'])
    expect(outerHandler).not.toHaveBeenCalled()

    // Empty path array is ignored.
    innerHandler.mockClear()
    captured?.(5, 5, [])
    expect(innerHandler).not.toHaveBeenCalled()

    offInner()
    offOuter()
    outer.remove()
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
