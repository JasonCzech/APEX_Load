import { useCallback, useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import './ux.css'

/**
 * A row's secondary actions behind a "More" toggle. The list overlays the
 * table (fixed position, so a scrolling table cannot clip it), follows its row
 * as the page scrolls, and closes on Escape, an outside click or choosing an
 * action, handing focus back to the toggle after Escape or an action.
 *
 * label names the row, so each "More" toggle announces which row it belongs to.
 */
export default function RowActions({ children, className = '', label }: { children: ReactNode; className?: string; label?: string }) {
  const [position, setPosition] = useState<CSSProperties | null>(null)
  const open = position !== null
  const containerRef = useRef<HTMLDivElement>(null)
  const toggleRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const menuId = useId()

  const close = useCallback((returnFocus: boolean) => {
    setPosition(null)
    if (returnFocus) toggleRef.current?.focus()
  }, [])

  // Anchor the list to the toggle; rows in the lower part of the viewport open upward.
  const placement = useCallback((): CSSProperties | null => {
    const rect = toggleRef.current?.getBoundingClientRect()
    if (!rect) return null
    const above = rect.bottom > window.innerHeight * 0.6
    return {
      right: Math.max(8, window.innerWidth - rect.right),
      ...(above ? { bottom: window.innerHeight - rect.top + 4 } : { top: rect.bottom + 4 }),
    }
  }, [])

  const toggle = () => {
    if (open) return close(false)
    setPosition(placement())
  }

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) close(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close(true)
    }
    // A fixed list follows its row while the page or table scrolls (including
    // scroll-into-view when an action is reached), and closes once the row
    // leaves the viewport.
    let frame = 0
    const follow = () => {
      if (frame) return
      // One measurement per frame, however many scroll events arrive.
      frame = window.requestAnimationFrame(() => {
        frame = 0
        const rect = toggleRef.current?.getBoundingClientRect()
        if (!rect || rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) return close(false)
        setPosition(placement())
      })
    }
    const onScroll = (event: Event) => {
      if (!menuRef.current?.contains(event.target as Node)) follow()
    }
    const onResize = follow
    // Capture phase: row actions stop propagation of their own clicks. Focus
    // goes back to the toggle first, so a dialog the action opens returns there.
    const onActionClick = (event: MouseEvent) => {
      if ((event.target as HTMLElement).closest('button:not(:disabled)')) close(true)
    }
    const menu = menuRef.current
    menu?.addEventListener('click', onActionClick, true)
    window.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('keydown', onKeyDown)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onResize)
      menu?.removeEventListener('click', onActionClick, true)
      if (frame) window.cancelAnimationFrame(frame)
    }
  }, [close, open, placement])

  return <div ref={containerRef} className={`ux-row-actions ${className}`} data-open={open || undefined} onClick={event => event.stopPropagation()}>
    <button
      ref={toggleRef}
      type="button"
      className="ux-row-actions-toggle"
      aria-expanded={open}
      aria-controls={menuId}
      aria-label={label ? `More actions for ${label}` : undefined}
      onClick={toggle}
    >More</button>
    <div
      ref={menuRef}
      id={menuId}
      className="ux-row-actions-menu"
      role="group"
      aria-label={label ? `Actions for ${label}` : 'More actions'}
      hidden={!open}
      style={position || undefined}
    >{children}</div>
  </div>
}
