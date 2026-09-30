import { Children, isValidElement, type ReactNode, forwardRef, useEffect, useId, useRef, useState, type ButtonHTMLAttributes } from 'react'
import { createPortal } from 'react-dom'
import './TooltipButton.css'

function hasLabel(children: ReactNode): boolean {
  return Children.toArray(children).some(child => typeof child === 'string' ? /[\p{L}\p{N}]/u.test(child) : typeof child === 'number' || (isValidElement<{ children?: ReactNode }>(child) && hasLabel(child.props.children)))
}

/** A native button with theme-aware help available to both pointer and keyboard users. */
const TooltipButton = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement>>(function TooltipButton({ title, children, disabled, 'aria-describedby': describedBy, ...props }, ref) {
  const id = useId()
  const anchor = useRef<HTMLButtonElement | null>(null)
  const popup = useRef<HTMLDivElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>()
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState({ left: 0, top: 0 })
  const cancel = () => clearTimeout(timer.current)
  const show = () => { cancel(); if (title) setOpen(true) }
  const hide = () => { cancel(); if (document.activeElement === anchor.current || document.activeElement === anchor.current?.parentElement) return; timer.current = setTimeout(() => setOpen(false), 150) }
  useEffect(() => () => clearTimeout(timer.current), [])
  useEffect(() => {
    if (!open) return
    const place = () => {
      const box = anchor.current?.getBoundingClientRect()
      const tip = popup.current?.getBoundingClientRect()
      if (!box || !tip) return
      setPosition({ left: Math.max(8, Math.min(box.left + (box.width - tip.width) / 2, window.innerWidth - tip.width - 8)), top: box.bottom + tip.height + 8 < window.innerHeight ? box.bottom + 8 : Math.max(8, box.top - tip.height - 8) })
    }
    const dismiss = (event: KeyboardEvent) => { if (event.key === 'Escape') { clearTimeout(timer.current); setOpen(false) } }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    document.addEventListener('keydown', dismiss)
    return () => { window.removeEventListener('resize', place); window.removeEventListener('scroll', place, true); document.removeEventListener('keydown', dismiss) }
  }, [open, title])
  const description = [describedBy, open && title ? id : undefined].filter(Boolean).join(' ') || undefined
  const button = <button {...props} ref={node => { anchor.current = node; if (typeof ref === 'function') ref(node); else if (ref) ref.current = node }} disabled={disabled}
    data-tooltip={title} aria-describedby={description} aria-label={props['aria-label'] || (hasLabel(children) ? undefined : title)}
    onMouseEnter={event => { props.onMouseEnter?.(event); show() }} onMouseLeave={event => { props.onMouseLeave?.(event); if (document.activeElement !== event.currentTarget) hide() }}
    onFocus={event => { props.onFocus?.(event); show() }} onBlur={event => { props.onBlur?.(event); cancel(); setOpen(false) }}
    onClick={event => { cancel(); setOpen(false); props.onClick?.(event) }}>{children}</button>
  return <>
    <span className="ux-tooltip-anchor" onMouseEnter={show} onMouseLeave={hide} onFocus={show} onBlur={() => { cancel(); setOpen(false) }}
      tabIndex={disabled && title ? 0 : undefined} role={disabled && title ? 'group' : undefined} aria-label={disabled ? title : undefined} aria-describedby={disabled ? description : undefined}>{button}</span>
    {open && title && createPortal(<div ref={popup} id={id} role="tooltip" className="ux-tooltip" style={position} onMouseEnter={cancel} onMouseLeave={hide}>{title}</div>, document.body)}
  </>
})
export default TooltipButton
