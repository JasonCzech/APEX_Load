import { useEffect, useId, type ReactNode } from 'react'
import './ux.css'
import { useAnchoredPopover } from './useAnchoredPopover'

/**
 * A row's secondary actions behind a "More" toggle. The list overlays the
 * table (fixed position, so a scrolling table cannot clip it), follows its row
 * as the page scrolls, and closes on Escape, an outside click or choosing an
 * action, handing focus back to the toggle after Escape or an action.
 *
 * label names the row, so each "More" toggle announces which row it belongs to.
 */
export default function RowActions({ children, className = '', label }: { children: ReactNode; className?: string; label?: string }) {
  const { open, position, toggle, close, containerRef, triggerRef, panelRef } = useAnchoredPopover()
  const menuId = useId()

  useEffect(() => {
    if (!open) return
    // Capture phase: row actions stop propagation of their own clicks. Focus
    // goes back to the toggle first, so a dialog the action opens returns there.
    const onActionClick = (event: MouseEvent) => {
      if ((event.target as HTMLElement).closest('button:not(:disabled)')) close(true)
    }
    const menu = panelRef.current
    menu?.addEventListener('click', onActionClick, true)
    return () => menu?.removeEventListener('click', onActionClick, true)
  }, [close, open, panelRef])

  return <div ref={element => { containerRef.current = element }} className={`ux-row-actions ${className}`} data-open={open || undefined} role="presentation" onClick={event => event.stopPropagation()}>
    <button
      ref={triggerRef}
      type="button"
      className="ux-row-actions-toggle"
      aria-expanded={open}
      aria-controls={menuId}
      aria-label={label ? `More actions for ${label}` : undefined}
      onClick={toggle}
    >More</button>
    <div
      ref={panelRef}
      id={menuId}
      className="ux-row-actions-menu"
      role="group"
      aria-label={label ? `Actions for ${label}` : 'More actions'}
      hidden={!open}
      style={position || undefined}
    >{children}</div>
  </div>
}
