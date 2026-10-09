import { useEffect, useRef, useState } from 'react'
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

// eslint-disable-next-line react-refresh/only-export-components
function UndoNotice({ message, undoLabel, onUndo }: { message: string; undoLabel: string; onUndo: () => Promise<void> }) {
  const [state, setState] = useState<{ busy: boolean; error: string }>({ busy: false, error: '' })
  const timer = useRef<number>()
  useEffect(() => {
    timer.current = window.setTimeout(close, UNDO_WINDOW_MS)
    return () => window.clearTimeout(timer.current)
  }, [])
  const undo = async () => {
    window.clearTimeout(timer.current)
    setState({ busy: true, error: '' })
    try {
      await onUndo()
      close()
    } catch (cause: unknown) {
      setState({ busy: false, error: cause instanceof Error ? cause.message : 'Undo failed.' })
    }
  }
  return <DialogLayer><div className="ux-undo-notice" role="status">
    <span>{message}</span>
    <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void undo() }} disabled={state.busy}>{state.busy ? 'Undoing…' : undoLabel}</button>
    <button type="button" className="btn btn-ghost btn-sm" onClick={close} aria-label="Dismiss">×</button>
    {state.error && <span role="alert" className="ux-undo-notice-error">{state.error}</span>}
  </div></DialogLayer>
}

/**
 * A short-lived "done — Undo" notice that outlives the row it came from (an
 * archived row disappears from the list). One notice at a time.
 */
export function showUndoNotice({ message, undoLabel = 'Undo', onUndo }: { message: string; undoLabel?: string; onUndo: () => Promise<void> }) {
  close()
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  current = { host, root }
  root.render(<UndoNotice message={message} undoLabel={undoLabel} onUndo={onUndo} />)
}

/** Test hook: remove any open notice. */
export function closeUndoNoticeForTests() {
  close()
}
