import { ReactNode, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

import './SnapshotBodyPreview.css'

interface Props {
  title: string
  open: boolean
  copied: boolean
  copyFailed?: boolean
  copyDisabled: boolean
  manualCopyText?: string
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
  onCopy,
  onClose,
  children,
}: Props) {
  const manualCopyRef = useRef<HTMLTextAreaElement | null>(null)

  useEffect(() => {
    if (!open || typeof window === 'undefined') return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [open, onClose])

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
    <div className="snapshot-full-dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className="snapshot-full-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="snapshot-full-dialog-header">
          <h3>{title}</h3>
          <div className="snapshot-full-dialog-actions">
            <button
              type="button"
              className="btn btn-sm btn-secondary"
              onClick={onCopy}
              disabled={copyDisabled}
            >
              {copyFailed ? manualCopyText ? 'Text selected' : 'Copy failed' : copied ? 'Copied' : 'Copy'}
            </button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={onClose}>
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
