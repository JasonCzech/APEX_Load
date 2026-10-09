import { useEffect, useId, type ReactNode } from 'react'
import './ux.css'
import { useAnchoredPopover } from './useAnchoredPopover'

/**
 * A compact badge that opens a small details panel beside it. The panel is a
 * non-modal dialog: it takes focus when it opens, closes on Escape (focus
 * returns to the badge), an outside click, or once the badge scrolls away.
 */
export default function InfoPopover({ label, title, triggerClassName = '', trigger, align = 'start', children }: {
  /** Accessible name of the badge, e.g. "Details for Checkout_Flow". */
  label: string
  /** Heading of the panel. */
  title: string
  triggerClassName?: string
  /** Visible badge content (icon and short text). */
  trigger: ReactNode
  align?: 'start' | 'end'
  children: ReactNode
}) {
  const { open, position, toggle, containerRef, triggerRef, panelRef } = useAnchoredPopover({ align })
  const panelId = useId()
  const titleId = useId()

  useEffect(() => {
    if (open) panelRef.current?.focus()
  }, [open, panelRef])

  // The badge and its panel sit inside clickable rows: their clicks must not select or expand the row.
  return <div ref={element => { containerRef.current = element }} className="ux-info-popover" role="presentation" onClick={event => event.stopPropagation()}>
    <button
      ref={triggerRef}
      type="button"
      className={`ux-info-popover-trigger ${triggerClassName}`.trim()}
      aria-label={label}
      aria-expanded={open}
      aria-controls={open ? panelId : undefined}
      title={label}
      onClick={toggle}
    >{trigger}</button>
    {open && <div
      ref={panelRef}
      id={panelId}
      className="ux-info-popover-panel"
      role="dialog"
      aria-labelledby={titleId}
      tabIndex={-1}
      style={position || undefined}
    >
      <p id={titleId} className="ux-info-popover-title">{title}</p>
      {children}
    </div>}
  </div>
}
