import { useCallback, useEffect, useId, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import { ExecutionPlan, ExecutionPlanApproval } from '../types'

interface Props {
  plan: ExecutionPlan
  canAdmin?: boolean
  onClose: () => void
  // Called after a pending approval is approved/rejected so the parent can
  // refresh any blocked-run state it is holding.
  onResolved?: () => void
}

function approvalStatusPill(status: string): string {
  switch (status) {
    case 'approved':
      return 'pp-pill pp-pill-success'
    case 'rejected':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-info' // pending
  }
}

function formatTimestamp(iso?: string): string {
  if (!iso) return '—'
  const parsed = new Date(iso)
  return Number.isNaN(parsed.getTime()) ? iso : parsed.toLocaleString()
}

export default function ExecutionPlanApprovalsModal({ plan, canAdmin = false, onClose, onResolved }: Props) {
  const api = useExecutionPlansApi()
  const [approvals, setApprovals] = useState<ExecutionPlanApproval[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [reasons, setReasons] = useState<Record<string, string>>({})
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()

  useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose })

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.listExecutionPlanApprovals(plan.id)
      setApprovals(res.approvals || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load approvals.')
      setApprovals([])
    } finally {
      setLoading(false)
    }
  }, [api, plan.id])

  useEffect(() => {
    void load()
  }, [load])

  const resolve = useCallback(
    async (approval: ExecutionPlanApproval, approve: boolean) => {
      setBusyId(approval.id)
      setError(null)
      try {
        const reason = reasons[approval.id]?.trim() || undefined
        if (approve) {
          await api.approveExecutionPlanApproval(plan.id, approval.id, reason)
        } else {
          await api.rejectExecutionPlanApproval(plan.id, approval.id, reason)
        }
        await load()
        onResolved?.()
      } catch (err) {
        setError(err instanceof ApiError ? err.message : 'Failed to resolve approval.')
      } finally {
        setBusyId(null)
      }
    },
    [api, load, onResolved, plan.id, reasons],
  )

  const pendingCount = approvals.filter(a => a.status === 'pending').length

  return (
    <div
      className="pp-modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <div ref={dialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="pp-modal-header">
          <h3 id={titleId}>Approvals — “{plan.name}”</h3>
          <button ref={closeButtonRef} type="button" className="btn btn-ghost btn-sm" onClick={onClose}>
            Close
          </button>
        </div>
        <div className="pp-modal-form">
          <div className="pp-row-meta">
            <span>
              Plan runs exceeding the project VUser quota require project-admin approval.
              {pendingCount > 0 ? ` ${pendingCount} pending.` : ''}
            </span>
          </div>
          {error && <div className="pp-error" role="alert">{error}</div>}

          {loading ? (
            <div className="pp-empty">Loading approvals…</div>
          ) : approvals.length === 0 ? (
            <div className="pp-empty">No approval requests. Runs within quota launch without approval.</div>
          ) : (
            <div className="pp-list">
              {approvals.map(approval => (
                <div className="pp-row" key={approval.id}>
                  <div className="pp-row-main">
                    <div className="pp-row-title">
                      {approval.policy}
                      <span className={approvalStatusPill(approval.status)}>{approval.status}</span>
                      {approval.required_role ? <span className="pp-pill pp-pill-muted">{approval.required_role}</span> : null}
                    </div>
                    {approval.decision_reason ? (
                      <div className="pp-row-meta">
                        <span>{approval.decision_reason}</span>
                      </div>
                    ) : null}
                    <div className="pp-row-meta">
                      {approval.requested_by ? <span>requested by {approval.requested_by}</span> : null}
                      <span>requested {formatTimestamp(approval.requested_at)}</span>
                      {approval.resolved_by ? <span>resolved by {approval.resolved_by}</span> : null}
                      {approval.run_id ? <span>consumed by run {approval.run_id}</span> : null}
                    </div>
                  </div>
                  {canAdmin && approval.status === 'pending' && (
                    <div className="pp-row-actions pp-approval-actions">
                      <input
                        className="pp-approval-reason"
                        value={reasons[approval.id] || ''}
                        placeholder="reason (optional)"
                        aria-label="Decision reason"
                        onChange={e => setReasons(prev => ({ ...prev, [approval.id]: e.target.value }))}
                      />
                      <button
                        type="button"
                        className="btn btn-primary btn-sm"
                        disabled={busyId === approval.id}
                        onClick={() => void resolve(approval, true)}
                      >
                        Approve
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        disabled={busyId === approval.id}
                        onClick={() => void resolve(approval, false)}
                      >
                        Reject
                      </button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
        <div className="pp-modal-actions">
          <button type="button" className="btn btn-primary" onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </div>
  )
}
