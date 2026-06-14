import { useEffect, useMemo, useState } from 'react'
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
  error,
  loading = false,
  onCancel,
  onConfirm,
}: Props) {
  const [reason, setReason] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [force, setForce] = useState(false)

  useEffect(() => {
    setReason('')
    setConfirmation('')
    setForce(false)
  }, [target])

  const canSubmit = useMemo(() => reason.trim().length > 0 && confirmation.trim() === target && !loading, [confirmation, loading, reason, target])

  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal destructive-confirm-modal" role="dialog" aria-modal="true" aria-labelledby="destructive-confirm-title" onClick={event => event.stopPropagation()}>
        <div className="modal-header">
          <div>
            <h3 id="destructive-confirm-title">{title}</h3>
            <p className="page-subtitle">{description}</p>
          </div>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel} disabled={loading}>Close</button>
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
          {error && <p className="form-error">{error}</p>}

          <label>
            <span>Reason</span>
            <input
              value={reason}
              onChange={event => setReason(event.target.value)}
              placeholder="Operational reason"
              disabled={loading}
              autoFocus
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

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onCancel} disabled={loading}>Cancel</button>
            <button type="submit" className="btn btn-primary" disabled={!canSubmit}>
              {loading ? 'Working...' : confirmLabel}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
