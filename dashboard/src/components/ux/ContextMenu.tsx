import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { dashboardZoom, placeAtPoint } from './floatingPlacement'
import './ux.css'
import './TooltipButton.css'

export interface ContextMenuItem {
  id: string
  label: string
  /** Shown, but does nothing; `reason` says why (on hover and focus, and to assistive technology). */
  disabled?: boolean
  reason?: string
  /** The command's keyboard shortcut, as shown ("⌘D", "Delete"). */
  shortcut?: string
  /** A destructive command (drawn in the danger colour). */
  danger?: boolean
  /** Starts a new group: a separator is drawn above it. */
  separatorBefore?: boolean
}

interface Props {
  /** Names the menu for assistive technology ("Actions for Login"). */
  label: string
  /** Where it opens, in viewport pixels: the pointer, or a row's bottom-left for the keyboard. */
  point: { x: number; y: number }
  items: ContextMenuItem[]
  /** A choice: the command decides where focus goes (a dialog it opens, or back to the opener). */
  onSelect: (id: string) => void
  /** `returnFocus` is true when Escape or Tab closed the menu, so the opener takes focus back. */
  onClose: (returnFocus: boolean) => void
}


/**
 * A context menu at a point: on document.body (so no scrolling or filtered
 * panel clips or offsets it), kept inside the viewport, keyboard operable as
 * the WAI-ARIA menu pattern (arrows, Home/End, a first letter, Enter or Space,
 * Escape and Tab close) and closed by an outside press, a scroll or a resize.
 * Disabled items stay focusable so their reason can be read.
 */
export default function ContextMenu({ label, point, items, onSelect, onClose }: Props) {
  const id = useId()
  const menuRef = useRef<HTMLDivElement>(null)
  const [zoom] = useState(dashboardZoom)
  const [place, setPlace] = useState<{ left: number; top: number } | null>(null)
  const [tip, setTip] = useState<{ index: number; left: number; top: number } | null>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  // Opens down and to the right of the point, flipped where that would leave the viewport.
  useLayoutEffect(() => {
    const menu = menuRef.current
    if (menu) setPlace(placeAtPoint(point, menu.getBoundingClientRect()))
  }, [point, items.length])

  const buttons = () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') || [])
  // The first command takes focus once the menu is placed (a hidden element cannot).
  const focused = useRef(false)
  useEffect(() => {
    if (!place || focused.current) return
    focused.current = true
    const first = buttons().find(button => button.getAttribute('aria-disabled') !== 'true') || buttons()[0]
    first?.focus({ preventScroll: true })
  }, [place])

  useEffect(() => {
    // An event from outside the menu (a window event's target is no element).
    const outside = (event: Event) => !(event.target instanceof Node) || !menuRef.current?.contains(event.target)
    const onPointerDown = (event: PointerEvent) => { if (outside(event)) onCloseRef.current(false) }
    const onScroll = (event: Event) => { if (outside(event)) onCloseRef.current(false) }
    const onResize = () => onCloseRef.current(false)
    // Escape closes the menu wherever focus is; it goes no further.
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key !== 'Escape' || !outside(event)) return
      event.preventDefault()
      event.stopPropagation()
      onCloseRef.current(true)
    }
    window.addEventListener('keydown', onKeyDown, true)
    // Capture phase: a pointer press on another row closes this menu before
    // that row's own handler runs (which may open a new one).
    window.addEventListener('pointerdown', onPointerDown, true)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onResize)
    window.addEventListener('blur', onResize)
    return () => {
      window.removeEventListener('pointerdown', onPointerDown, true)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onResize)
      window.removeEventListener('blur', onResize)
      window.removeEventListener('keydown', onKeyDown, true)
    }
  }, [])

  const choose = (item: ContextMenuItem) => {
    if (item.disabled) return
    onClose(false)
    onSelect(item.id)
  }

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // Keys stay in the menu: an editor shortcut (Undo) must not change what it acts on.
    event.stopPropagation()
    const list = buttons()
    const current = list.indexOf(document.activeElement as HTMLButtonElement)
    const focusAt = (index: number) => { list[(index + list.length) % list.length]?.focus({ preventScroll: true }) }
    switch (event.key) {
      case 'ArrowDown': focusAt(current + 1); break
      case 'ArrowUp': focusAt(current < 0 ? list.length - 1 : current - 1); break
      case 'Home': focusAt(0); break
      case 'End': focusAt(list.length - 1); break
      case 'Escape':
        onClose(true)
        break
      case 'Tab':
        onClose(true)
        break
      default: {
        if (event.metaKey || event.ctrlKey || event.altKey) { event.preventDefault(); return }
        if (event.key.length !== 1 || !/\S/.test(event.key)) return
        const letter = event.key.toLowerCase()
        const order = [...list.slice(current + 1), ...list.slice(0, current + 1)]
        order.find(button => (button.textContent || '').trim().toLowerCase().startsWith(letter))?.focus({ preventScroll: true })
      }
    }
    event.preventDefault()
  }

  const showTip = (index: number, element: HTMLElement) => {
    if (!items[index]?.disabled || !items[index].reason) { setTip(null); return }
    const box = element.getBoundingClientRect()
    setTip({ index, left: box.right + 6, top: box.top })
  }

  return createPortal(
    <>
      <div
        ref={menuRef}
        role="menu"
        aria-label={label}
        tabIndex={-1}
        className="ux-context-menu"
        style={{ left: (place?.left ?? point.x) / zoom, top: (place?.top ?? point.y) / zoom, zoom, visibility: place ? undefined : 'hidden' }}
        onKeyDown={onKeyDown}
        onContextMenu={event => event.preventDefault()}
      >
        {items.map((item, index) => (
          <div key={item.id} role="none" className={item.separatorBefore && index > 0 ? 'ux-context-menu-group' : undefined}>
            <button
              type="button"
              role="menuitem"
              tabIndex={-1}
              data-menu-item={item.id}
              aria-disabled={item.disabled || undefined}
              aria-describedby={item.disabled && item.reason ? `${id}-reason-${index}` : undefined}
              aria-keyshortcuts={item.shortcut || undefined}
              className={`ux-context-menu-item${item.danger ? ' danger' : ''}`}
              onClick={() => choose(item)}
              onMouseEnter={event => { event.currentTarget.focus({ preventScroll: true }); showTip(index, event.currentTarget) }}
              onMouseLeave={() => setTip(null)}
              onFocus={event => showTip(index, event.currentTarget)}
              onBlur={() => setTip(null)}
            >
              <span className="ux-context-menu-label">{item.label}</span>
              {item.shortcut && <kbd className="ux-context-menu-shortcut" aria-hidden="true">{item.shortcut}</kbd>}
            </button>
            {item.disabled && item.reason && <span id={`${id}-reason-${index}`} hidden>{item.reason}</span>}
          </div>
        ))}
      </div>
      {tip && items[tip.index]?.reason && (
        <div role="presentation" className="ux-tooltip ux-context-menu-tip" style={{ left: Math.min(tip.left, (window.innerWidth || 1024) - 268), top: tip.top }}>{items[tip.index].reason}</div>
      )}
    </>,
    document.body,
  )
}
