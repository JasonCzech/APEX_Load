import DialogLayer from './ux/DialogLayer'
import { useCallback, useId, useRef } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'

interface Props {
  label: string
  saving?: boolean
  error?: string | null
  onSave: () => void
  onDiscard: () => void
  onCancel: () => void
}

export default function UnsavedChangesModal({
  label,
  saving = false,
  error,
  onSave,
  onDiscard,
  onCancel,
}: Props) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const keepEditingRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const requestCancel = useCallback(() => {
    if (!saving) onCancel()
  }, [onCancel, saving])
  useModalDialog({
    dialogRef,
    initialFocusRef: keepEditingRef,
    onClose: saving ? undefined : requestCancel,
  })

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
        className="modal unsaved-changes-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <div>
            <h3 id={titleId}>Unsaved Changes</h3>
            <p id={descriptionId} className="page-subtitle">
              {label} has unsaved changes. Save them before leaving, discard them, or keep editing.
            </p>
          </div>
          <button type="button" className="btn btn-secondary btn-sm" aria-label="Close unsaved changes dialog" onClick={requestCancel} disabled={saving}>Close</button>
        </div>

        {error && <p className="form-error" role="alert">{error}</p>}

        <div className="modal-actions">
          <button ref={keepEditingRef} type="button" className="btn btn-secondary" onClick={requestCancel} disabled={saving}>
            Keep editing
          </button>
          <button type="button" className="btn btn-danger-outline" onClick={onDiscard} disabled={saving}>
            Discard changes
          </button>
          <button type="button" className="btn btn-primary" onClick={onSave} disabled={saving}>
            {saving ? 'Saving...' : 'Save changes'}
          </button>
        </div>
      </div>
    </div></DialogLayer>
  )
}
