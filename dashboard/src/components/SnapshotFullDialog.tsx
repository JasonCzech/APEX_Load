import { ReactNode, useEffect, useId, useRef } from 'react'
import { createPortal } from 'react-dom'

import { useModalDialog } from '../hooks/useModalDialog'
import './SnapshotBodyPreview.css'

interface Props {
  title: string
  open: boolean
  copied: boolean
  copyFailed?: boolean
  copyDisabled: boolean
  manualCopyText?: string
  additionalActions?: ReactNode
  onCopy: () => void
  onClose: () => void
  children: ReactNode
}

export default function SnapshotFullDialog({
  title,
  open,
  copied,
  copyFailed = false,
  copyDisabled,
  manualCopyText,
  additionalActions,
  onCopy,
  onClose,
  children,
}: Props) {
  const dialogRef = useRef<HTMLElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const manualCopyRef = useRef<HTMLTextAreaElement | null>(null)
  const titleId = useId()

  useModalDialog({
    dialogRef,
    enabled: open,
    initialFocusRef: closeButtonRef,
    onClose,
  })

  useEffect(() => {
    if (!open || !copyFailed || !manualCopyText || typeof window === 'undefined') return

    const selectManualCopyText = () => {
      manualCopyRef.current?.focus()
      manualCopyRef.current?.select()
    }

    selectManualCopyText()

    if (typeof window.requestAnimationFrame === 'function') {
      window.requestAnimationFrame(selectManualCopyText)
    } else {
      window.setTimeout(selectManualCopyText, 0)
    }
  }, [copyFailed, manualCopyText, open])

  if (!open || typeof document === 'undefined') {
    return null
  }

  return createPortal(
    <div
      className="snapshot-full-dialog-backdrop"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section
        ref={dialogRef}
        className="snapshot-full-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header className="snapshot-full-dialog-header">
          <h3 id={titleId}>{title}</h3>
          <div className="snapshot-full-dialog-actions">
            {additionalActions}
            <button
              type="button"
              className="btn btn-sm btn-secondary"
              onClick={onCopy}
              disabled={copyDisabled}
            >
              {copyFailed ? manualCopyText ? 'Text selected' : 'Copy failed' : copied ? 'Copied' : 'Copy'}
            </button>
            <button ref={closeButtonRef} type="button" className="btn btn-sm btn-ghost" onClick={onClose}>
              Close
            </button>
          </div>
        </header>
        <div className="snapshot-full-dialog-body">
          {copyFailed && manualCopyText ? (
            <textarea
              ref={manualCopyRef}
              className="snapshot-full-dialog-copy-source"
              aria-label={`${title} copy text`}
              readOnly
              value={manualCopyText}
            />
          ) : children}
        </div>
      </section>
    </div>,
    document.body,
  )
}
