import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { transactionNameProblem, type TransactionTiming } from './outlineTransactionEdits'
import { dashboardZoom, placeAtPoint } from './ux/floatingPlacement'

interface Props {
  title: string
  /** Where it opens, in viewport pixels (the menu's or the button's position). */
  point: { x: number; y: number }
  initialName: string
  submitLabel: string
  /** Wrapping offers a timing choice; "Grouped" only when `perStep` is possible. */
  timing?: { perStep: boolean; summary: (timing: TransactionTiming) => string }
  /** Whether another transaction already uses a name (a warning, not a block). */
  nameInUse: (name: string) => boolean
  /** Why a name cannot be used here (it would nest inside one of the same name), or ''. */
  nameConflict?: (name: string) => string
  onSubmit: (name: string, timing: TransactionTiming) => void
  /** `returnFocus`: closed by Escape or Cancel, so the opener takes focus back. */
  onCancel: (returnFocus: boolean) => void
}

/**
 * A small dialog beside the outline for naming a transaction: the name
 * (pre-filled and selected), how it is timed when wrapping, and what it will
 * time. Enter applies, Escape and Cancel close, and focus stays inside.
 */
export default function TransactionNameDialog({ title, point, initialName, submitLabel, timing, nameInUse, nameConflict, onSubmit, onCancel }: Props) {
  const id = useId()
  const panelRef = useRef<HTMLFormElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState(initialName)
  const [mode, setMode] = useState<TransactionTiming>('span')
  const [touched, setTouched] = useState(false)
  const [zoom] = useState(dashboardZoom)
  const [place, setPlace] = useState<{ left: number; top: number } | null>(null)
  const onCancelRef = useRef(onCancel)
  onCancelRef.current = onCancel

  useLayoutEffect(() => {
    const place = () => { const panel = panelRef.current; if (panel) setPlace(placeAtPoint(point, panel.getBoundingClientRect())) }
    place()
    window.addEventListener('resize', place)
    return () => window.removeEventListener('resize', place)
  }, [point])
  // The name takes focus once the dialog is placed (a hidden element cannot).
  const focused = useRef(false)
  useEffect(() => {
    if (!place || focused.current) return
    focused.current = true
    inputRef.current?.focus({ preventScroll: true })
    inputRef.current?.select()
  }, [place])
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!panelRef.current?.contains(event.target as Node)) onCancelRef.current(false)
    }
    window.addEventListener('pointerdown', onPointerDown, true)
    return () => window.removeEventListener('pointerdown', onPointerDown, true)
  }, [])

  const problem = transactionNameProblem(name) || nameConflict?.(name) || ''
  const shownProblem = touched || name !== initialName ? problem : ''
  const clash = !problem && name.trim() !== initialName.trim() && nameInUse(name.trim())
  const effective: TransactionTiming = timing?.perStep ? mode : 'span'

  // Escape closes (and goes no further); Tab keeps focus inside.
  useEffect(() => {
    const panel = panelRef.current
    if (!panel) return undefined
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        onCancelRef.current(true)
        return
      }
      if (event.key !== 'Tab') return
      const focusable = Array.from(panel.querySelectorAll<HTMLElement>('input, button:not(:disabled)'))
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    panel.addEventListener('keydown', onKeyDown)
    return () => panel.removeEventListener('keydown', onKeyDown)
  }, [])

  return createPortal(
    <form
      ref={panelRef}
      role="dialog"
      aria-modal="false"
      aria-labelledby={`${id}-title`}
      className="txn-name-dialog"
      style={{ left: (place?.left ?? point.x) / zoom, top: (place?.top ?? point.y) / zoom, zoom, visibility: place ? undefined : 'hidden' }}
      onSubmit={event => {
        event.preventDefault()
        setTouched(true)
        if (!problem) onSubmit(name.trim(), effective)
      }}
    >
      <h3 id={`${id}-title`}>{title}</h3>
      <label className="txn-name-field">
        <span>Transaction name</span>
        <input
          ref={inputRef}
          value={name}
          aria-label="Transaction name"
          aria-invalid={Boolean(shownProblem)}
          aria-describedby={[shownProblem ? `${id}-problem` : '', clash ? `${id}-clash` : ''].filter(Boolean).join(' ') || undefined}
          spellCheck={false}
          autoComplete="off"
          onChange={event => setName(event.target.value)}
        />
      </label>
      {shownProblem && <small id={`${id}-problem`} className="txn-name-problem" role="alert">{shownProblem}</small>}
      {clash && <small id={`${id}-clash`} className="txn-name-warning">Another transaction already uses this name; their times are reported together.</small>}
      {timing && (
        <>
          {timing.perStep && (
            <div className="txn-name-timing" role="group" aria-label="Transaction timing">
              <button type="button" className={`btn btn-sm ${mode === 'span' ? 'btn-secondary' : 'btn-ghost'}`} aria-pressed={mode === 'span'} onClick={() => setMode('span')}>Timed span</button>
              <button type="button" className={`btn btn-sm ${mode === 'per-step' ? 'btn-secondary' : 'btn-ghost'}`} aria-pressed={mode === 'per-step'} onClick={() => setMode('per-step')}>Grouped</button>
            </div>
          )}
          <p className="txn-name-summary">{timing.summary(effective)}</p>
        </>
      )}
      <div className="txn-name-actions">
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onCancel(true)}>Cancel</button>
        <button type="submit" className="btn btn-primary btn-sm" disabled={Boolean(problem)}>{submitLabel}</button>
      </div>
    </form>,
    document.body,
  )
}
