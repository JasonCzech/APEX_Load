import { useId, useRef, type ReactNode } from 'react'

import { useModalDialog } from '../hooks/useModalDialog'
import DialogLayer from './ux/DialogLayer'

export interface SettingsConfirmDialogProps {
  open: boolean
  title: string
  children: ReactNode
  confirmLabel: string
  confirmClassName?: string
  confirmDisabled?: boolean
  onCancel: () => void
  onConfirm: () => void
}

/** A confirmation for a destructive settings action. */
export default function SettingsConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  confirmClassName = 'btn btn-danger',
  confirmDisabled = false,
  onCancel,
  onConfirm,
}: SettingsConfirmDialogProps) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  useModalDialog({ dialogRef, enabled: open, onClose: onCancel })

  if (!open) return null

  return (
    <DialogLayer><div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onCancel()
      }}
    >
      <div
        ref={dialogRef}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <h3 id={titleId}>{title}</h3>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel} aria-label="Close dialog">Close</button>
        </div>
        <div className="monitor-body" id={descriptionId}>
          {children}
          <div className="settings-inline-actions">
            <button className="btn btn-secondary" type="button" onClick={onCancel}>Cancel</button>
            <button className={confirmClassName} type="button" disabled={confirmDisabled} onClick={onConfirm}>{confirmLabel}</button>
          </div>
        </div>
      </div>
    </div></DialogLayer>
  )
}
