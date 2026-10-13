import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import './ConfirmDestructiveModal.css'

export interface DestructiveConfirmation {
  reason: string
  confirmation: string
  force: boolean
}

interface Props {
  title: string
  description: string
  target: string
  targetLabel?: string
  confirmLabel?: string
  allowForce?: boolean
  forceLabel?: string
  forceDescription?: string
  warning?: string
  /** What the action affects, shown before the reason is asked for. */
  affects?: ReactNode
  /** Why the action cannot run now; the confirm stays disabled while set. */
  blocked?: string
  error?: string | null
  loading?: boolean
  onCancel: () => void
  onConfirm: (confirmation: DestructiveConfirmation) => void
}

export default function ConfirmDestructiveModal({
  title,
  description,
  target,
  targetLabel = 'Target',
  confirmLabel = 'Confirm',
  allowForce = false,
  forceLabel = 'Force',
  forceDescription,
  warning,
  affects,
  blocked,
  error,
  loading = false,
  onCancel,
  onConfirm,
}: Props) {
  const [reason, setReason] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [force, setForce] = useState(false)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const reasonInputRef = useRef<HTMLInputElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const requestCancel = useCallback(() => {
    if (!loading) onCancel()
  }, [loading, onCancel])
  useModalDialog({
    dialogRef,
    initialFocusRef: reasonInputRef,
    onClose: loading ? undefined : requestCancel,
  })

  useEffect(() => {
    setReason('')
    setConfirmation('')
    setForce(false)
  }, [target])

  const canSubmit = useMemo(() => !blocked && reason.trim().length > 0 && confirmation.trim() === target && !loading, [blocked, confirmation, loading, reason, target])

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
        className="modal destructive-confirm-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <div>
            <h3 id={titleId}>{title}</h3>
            <p id={descriptionId} className="page-subtitle">{description}</p>
          </div>
        </div>

        <form
          className="modal-form destructive-confirm-form"
          onSubmit={event => {
            event.preventDefault()
            if (canSubmit) {
              onConfirm({ reason: reason.trim(), confirmation: confirmation.trim(), force })
            }
          }}
        >
          <div className="destructive-target">
            <span>{targetLabel}</span>
            <strong className="cell-mono">{target}</strong>
          </div>

          {warning && <p className="destructive-warning">{warning}</p>}
          {blocked && <p className="destructive-warning" role="status">{blocked}</p>}
          {affects}
          {error && <p className="form-error" role="alert">{error}</p>}

          {/* Nothing to confirm while the action is blocked. */}
          {!blocked && <>
            <label>
              <span>Reason</span>
              <input
                ref={reasonInputRef}
                value={reason}
                onChange={event => setReason(event.target.value)}
                placeholder="Operational reason"
                disabled={loading}
              />
            </label>

            <label>
              <span>Type target to confirm</span>
              <input
                value={confirmation}
                onChange={event => setConfirmation(event.target.value)}
                placeholder={target}
                disabled={loading}
              />
            </label>

            {allowForce && (
              <label className="destructive-force-row">
                <input
                  type="checkbox"
                  checked={force}
                  onChange={event => setForce(event.target.checked)}
                  disabled={loading}
                />
                <span>
                  <strong>{forceLabel}</strong>
                  {forceDescription && <small>{forceDescription}</small>}
                </span>
              </label>
            )}
          </>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={requestCancel} disabled={loading}>Cancel</button>
            <button type="submit" className="btn btn-danger" disabled={!canSubmit}>
              {loading ? 'Working…' : confirmLabel}
            </button>
          </div>
        </form>
      </div>
    </div></DialogLayer>
  )
}
