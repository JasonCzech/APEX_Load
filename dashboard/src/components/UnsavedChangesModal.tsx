import { useRef } from 'react'
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
  useModalDialog({ dialogRef, onClose: saving ? undefined : onCancel })

  return (
    <div className="modal-overlay" onClick={saving ? undefined : onCancel}>
      <div
        ref={dialogRef}
        className="modal unsaved-changes-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="unsaved-changes-title"
        onClick={event => event.stopPropagation()}
      >
        <div className="modal-header">
          <div>
            <h3 id="unsaved-changes-title">Unsaved Changes</h3>
            <p className="page-subtitle">
              {label} has unsaved changes. Save them before leaving, discard them, or keep editing.
            </p>
          </div>
        </div>

        {error && <p className="form-error">{error}</p>}

        <div className="modal-actions">
          <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={saving}>
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
    </div>
  )
}
