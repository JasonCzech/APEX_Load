import { useEffect, useRef, useState, type ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'

import DialogLayer from './DialogLayer'

const UNDO_WINDOW_MS = 10_000
let current: { host: HTMLDivElement; root: Root } | null = null

function close() {
  if (!current) return
  const { host, root } = current
  current = null
  root.unmount()
  host.remove()
}

/** Replaces any open notice with this one. */
function mount(notice: ReactElement) {
  close()
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  current = { host, root }
  root.render(notice)
}

// eslint-disable-next-line react-refresh/only-export-components
function UndoNotice({ message, undoLabel, onUndo }: { message: string; undoLabel: string; onUndo?: () => Promise<void> }) {
  const [state, setState] = useState<{ busy: boolean; error: string }>({ busy: false, error: '' })
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => {
    timer.current = window.setTimeout(close, UNDO_WINDOW_MS)
    return () => window.clearTimeout(timer.current)
  }, [])
  const undo = async () => {
    if (!onUndo) return
    window.clearTimeout(timer.current)
    setState({ busy: true, error: '' })
    try {
      await onUndo()
      close()
    } catch (cause: unknown) {
      setState({ busy: false, error: cause instanceof Error ? cause.message : 'Undo failed.' })
    }
  }
  // Without an undo it reports a failure: announced at once, in the error colour.
  return <DialogLayer><div className={`ux-undo-notice${onUndo ? '' : ' error'}`} role={onUndo ? 'status' : 'alert'}>
    <span>{message}</span>
    {onUndo && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void undo() }} disabled={state.busy}>{state.busy ? 'Undoing…' : undoLabel}</button>}
    <button type="button" className="btn btn-ghost btn-sm" onClick={close} aria-label="Dismiss">×</button>
    {state.error && <span role="alert" className="ux-undo-notice-error">{state.error}</span>}
  </div></DialogLayer>
}

/**
 * A short-lived "done — Undo" notice that outlives the row it came from (an
 * archived row disappears from the list). One notice at a time.
 */
export function showUndoNotice({ message, undoLabel = 'Undo', onUndo }: { message: string; undoLabel?: string; onUndo: () => Promise<void> }) {
  mount(<UndoNotice message={message} undoLabel={undoLabel} onUndo={onUndo} />)
}

/**
 * The same short-lived notice for a failed action whose control has gone (a
 * row menu that closed when the action was chosen).
 */
export function showErrorNotice(message: string) {
  mount(<UndoNotice message={message} undoLabel="" />)
}

/** Test hook: remove any open notice. */
export function closeUndoNoticeForTests() {
  close()
}
