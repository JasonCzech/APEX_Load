import DialogLayer from './ux/DialogLayer'
import { useId, useRef } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'

interface Props {
  sourceLabel: string
  onReplace: () => void
  onKeep: () => void
  onCancel: () => void
}

export default function ReplaceScenarioScriptsModal({ sourceLabel, onReplace, onKeep, onCancel }: Props) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  useModalDialog({
    dialogRef,
    initialFocusRef: cancelRef,
    onClose: onCancel,
  })

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
        className="modal replace-scenario-scripts-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <div>
            <h3 id={titleId}>Replace Selected Scripts?</h3>
            <p id={descriptionId} className="page-subtitle">
              {sourceLabel} has its own scripts. Use them in place of the scripts already chosen for this draft, or keep your scripts and copy only its load model and settings.
            </p>
          </div>
          <button type="button" className="btn btn-secondary btn-sm" aria-label="Close replace scripts dialog" onClick={onCancel}>Close</button>
        </div>

        <div className="modal-actions">
          <button ref={cancelRef} type="button" className="btn btn-secondary" onClick={onCancel}>
            Cancel
          </button>
          <button type="button" className="btn btn-secondary" onClick={onKeep}>
            Keep my scripts
          </button>
          <button type="button" className="btn btn-primary" onClick={onReplace}>
            Use scenario's scripts
          </button>
        </div>
      </div>
    </div></DialogLayer>
  )
}
