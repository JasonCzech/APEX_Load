// @vitest-environment jsdom

import type { PointerEvent as ReactPointerEvent } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { startPointerDrag } from './pointerDrag'

function pointerDown(handle: HTMLElement, { button = 0, pointerId = 1 } = {}) {
  return { button, pointerId, clientX: 100, clientY: 100, currentTarget: handle, preventDefault: vi.fn() } as unknown as ReactPointerEvent<HTMLElement>
}

const pointer = (type: string, pointerId = 1, clientX = 0) => new PointerEvent(type, { pointerId, clientX, bubbles: true })

describe('startPointerDrag', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  })

  it('ends the drag on the release of the pointer that started it', () => {
    const handle = document.body.appendChild(document.createElement('div'))
    const onMove = vi.fn()
    const onEnd = vi.fn()
    const down = pointerDown(handle)

    expect(startPointerDrag(down, { cursor: 'col-resize', onMove, onEnd })).toBe(true)
    expect(down.preventDefault).toHaveBeenCalled()
    expect(document.body.style.cursor).toBe('col-resize')

    window.dispatchEvent(pointer('pointermove', 1, 140))
    window.dispatchEvent(pointer('pointermove', 2, 900))
    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onMove.mock.calls[0][0].clientX).toBe(140)

    // A cancelled pointerdown suppresses the compatibility mouseup, so the
    // release has to come from pointerup, and only from the dragging pointer.
    window.dispatchEvent(pointer('pointerup', 2))
    expect(onEnd).not.toHaveBeenCalled()
    window.dispatchEvent(pointer('pointerup', 1))
    expect(onEnd).toHaveBeenCalledTimes(1)
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')

    // Once released, the panel no longer follows the cursor.
    window.dispatchEvent(pointer('pointermove', 1, 300))
    handle.dispatchEvent(pointer('lostpointercapture', 1))
    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('ends the drag when the pointer is cancelled, capture is lost or the window loses focus', () => {
    for (const end of [
      () => window.dispatchEvent(pointer('pointercancel', 1)),
      (handle: HTMLElement) => handle.dispatchEvent(pointer('lostpointercapture', 1)),
      () => window.dispatchEvent(new Event('blur')),
    ]) {
      const handle = document.body.appendChild(document.createElement('div'))
      const onEnd = vi.fn()
      startPointerDrag(pointerDown(handle), { cursor: 'row-resize', onMove: vi.fn(), onEnd })
      end(handle)
      expect(onEnd).toHaveBeenCalledTimes(1)
    }
  })

  it('starts nothing for a secondary button', () => {
    const handle = document.body.appendChild(document.createElement('div'))
    const onMove = vi.fn()
    const down = pointerDown(handle, { button: 2 })

    expect(startPointerDrag(down, { cursor: 'col-resize', onMove, onEnd: vi.fn() })).toBe(false)
    expect(down.preventDefault).not.toHaveBeenCalled()
    window.dispatchEvent(pointer('pointermove', 1, 140))
    expect(onMove).not.toHaveBeenCalled()
  })
})
