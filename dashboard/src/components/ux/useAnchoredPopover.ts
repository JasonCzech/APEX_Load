import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'

const isSet = (value: string) => Boolean(value) && value !== 'none'

/**
 * The box a fixed-position panel is placed in: the nearest ancestor that
 * creates a containing block for fixed descendants (a transformed dialog, or a
 * glass panel or modal backdrop with a backdrop filter), or the viewport.
 */
function fixedOrigin(element: HTMLElement) {
  for (let node = element.parentElement; node && node !== document.body; node = node.parentElement) {
    const style = getComputedStyle(node)
    const backdrop = style.backdropFilter || style.getPropertyValue('-webkit-backdrop-filter')
    if (isSet(style.transform) || isSet(style.filter) || isSet(backdrop) || isSet(style.perspective)
      || /paint|layout|strict|content/.test(style.contain || '') || /transform|filter|perspective/.test(style.willChange || '')) {
      const rect = node.getBoundingClientRect()
      return { left: rect.left + node.clientLeft, top: rect.top + node.clientTop, right: rect.left + node.clientLeft + node.clientWidth, bottom: rect.top + node.clientTop + node.clientHeight }
    }
  }
  return { left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight }
}

/**
 * A panel anchored to its trigger: fixed position (so a scrolling table cannot
 * clip it), opening upward for triggers in the lower part of the viewport,
 * following the trigger as the page scrolls and closing once it leaves the
 * viewport, on an outside pointer press, or on Escape (which returns focus to
 * the trigger and goes no further, so an enclosing dialog stays open).
 * `align` lines the panel up with the trigger's start or end edge (start flips
 * to end in the right half of the viewport, so the panel stays on screen).
 */
export function useAnchoredPopover<Trigger extends HTMLElement = HTMLButtonElement>({ align = 'end' }: { align?: 'start' | 'end' } = {}) {
  const [position, setPosition] = useState<CSSProperties | null>(null)
  const open = position !== null
  const containerRef = useRef<HTMLElement | null>(null)
  const triggerRef = useRef<Trigger | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)

  const close = useCallback((returnFocus: boolean) => {
    setPosition(null)
    if (returnFocus) triggerRef.current?.focus()
  }, [])

  const placement = useCallback((): CSSProperties | null => {
    const trigger = triggerRef.current
    const rect = trigger?.getBoundingClientRect()
    if (!trigger || !rect) return null
    const above = rect.bottom > window.innerHeight * 0.6
    // A start-aligned panel whose trigger sits in the right half would run off
    // screen, so it lines up with the trigger's end edge instead.
    const fromEnd = align === 'end' || rect.left > window.innerWidth / 2
    // Offsets are measured from the panel's containing block, which is the
    // viewport unless an ancestor (such as an animated dialog) creates one.
    const origin = fixedOrigin(trigger)
    return {
      ...(fromEnd
        ? { right: Math.max(8, window.innerWidth - rect.right) - (window.innerWidth - origin.right) }
        : { left: Math.max(8, rect.left) - origin.left }),
      ...(above ? { bottom: origin.bottom - rect.top + 4 } : { top: rect.bottom + 4 - origin.top }),
    }
  }, [align])

  const toggle = useCallback(() => {
    setPosition(current => current ? null : placement())
  }, [placement])

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) close(false)
    }
    // Capture phase, and the event stops here: Escape closes only this panel,
    // not a dialog or row underneath that also listens for it.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      close(true)
    }
    let frame = 0
    const follow = () => {
      if (frame) return
      // One measurement per frame, however many scroll events arrive.
      frame = window.requestAnimationFrame(() => {
        frame = 0
        const rect = triggerRef.current?.getBoundingClientRect()
        if (!rect || rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) return close(false)
        setPosition(placement())
      })
    }
    const onScroll = (event: Event) => {
      if (!panelRef.current?.contains(event.target as Node)) follow()
    }
    window.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('keydown', onKeyDown, true)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', follow)
    return () => {
      window.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('keydown', onKeyDown, true)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', follow)
      if (frame) window.cancelAnimationFrame(frame)
    }
  }, [close, open, placement])

  return { open, position, toggle, close, containerRef, triggerRef, panelRef }
}
