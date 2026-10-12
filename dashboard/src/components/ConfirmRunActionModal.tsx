import DialogLayer from './ux/DialogLayer'
import { useCallback, useId, useRef } from 'react'

import { useModalDialog } from '../hooks/useModalDialog'
import { RunActionConfirmationKind, runActionConfirmationCopy } from './runActionConfirmation'
import './ConfirmRunActionModal.css'

interface Props {
  action: RunActionConfirmationKind
  runName?: string
  runRef?: string
  /** What this action does to this run, such as a load change's impact. */
  detail?: string
  onConfirm: () => void
  onCancel: () => void
}

const TONE_CLASS: Record<'primary' | 'secondary' | 'danger', string> = {
  primary: 'btn-primary',
  secondary: 'btn-secondary',
  danger: 'btn-danger',
}

export default function ConfirmRunActionModal({ action, runName, runRef, detail, onConfirm, onCancel }: Props) {
  const copy = runActionConfirmationCopy(action)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  // Focus Cancel, not the confirm button: Enter on an accidentally-triggered
  // dialog must not carry straight through to stopping a live run.
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const detailId = useId()
  const requestCancel = useCallback(() => onCancel(), [onCancel])
  useModalDialog({ dialogRef, initialFocusRef: cancelRef, onClose: requestCancel })

  return (
    <DialogLayer><div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) requestCancel()
      }}
    >
      <div
        ref={dialogRef}
        className="modal confirm-run-action-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={detail ? `${descriptionId} ${detailId}` : descriptionId}
      >
        <div className="modal-header">
          <div>
            <h3 id={titleId}>{copy.title}</h3>
            <p id={descriptionId} className="page-subtitle">{copy.description}</p>
          </div>
        </div>

        {(runName || runRef) && (
          <div className="confirm-run-action-target">
            <span>Run</span>
            <strong>{runName || runRef}</strong>
            {runName && runRef && <small className="cell-mono">{runRef}</small>}
          </div>
        )}
        {detail && <p id={detailId} className="confirm-run-action-detail">{detail}</p>}

        <div className="modal-actions">
          <button ref={cancelRef} type="button" className="btn btn-secondary" onClick={requestCancel}>
            Cancel
          </button>
          <button type="button" className={`btn ${TONE_CLASS[copy.tone]}`} onClick={onConfirm}>
            {copy.confirmLabel}
          </button>
        </div>
      </div>
    </div></DialogLayer>
  )
}
