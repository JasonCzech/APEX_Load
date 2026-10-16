import type { PointerEvent as ReactPointerEvent } from 'react'

interface PointerDragOptions {
  cursor: string
  onMove: (event: PointerEvent) => void
  onEnd: () => void
}

/**
 * Track a resize handle's drag from its pointerdown until that pointer is
 * released. Returns false, starting nothing, for any button but the primary.
 *
 * The drag stays on pointer events throughout. Cancelling pointerdown (which
 * keeps the drag from selecting text) also cancels the compatibility mousemove
 * and mouseup while the button is held, so a drag followed with mouse listeners
 * never saw its release: the panel kept following the cursor, and clicking to
 * stop it landed on the handle and started another drag.
 */
export function startPointerDrag(event: ReactPointerEvent<HTMLElement>, { cursor, onMove, onEnd }: PointerDragOptions): boolean {
  if (event.button !== 0) return false
  event.preventDefault()
  const handle = event.currentTarget
  const { pointerId } = event
  // Capture keeps the release coming to us when it happens outside the window.
  try { handle.setPointerCapture?.(pointerId) } catch { /* the pointer is already gone */ }
  let ended = false
  const move = (moveEvent: PointerEvent) => {
    if (moveEvent.pointerId === pointerId) onMove(moveEvent)
  }
  const end = (endEvent: Event) => {
    if (ended || ('pointerId' in endEvent && endEvent.pointerId !== pointerId)) return
    ended = true
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', end)
    window.removeEventListener('pointercancel', end)
    window.removeEventListener('blur', end)
    handle.removeEventListener('lostpointercapture', end)
    document.body.style.userSelect = ''
    document.body.style.cursor = ''
    onEnd()
  }
  document.body.style.userSelect = 'none'
  document.body.style.cursor = cursor
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', end)
  window.addEventListener('pointercancel', end)
  window.addEventListener('blur', end)
  handle.addEventListener('lostpointercapture', end)
  return true
}
