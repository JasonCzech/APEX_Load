import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import { ExecutionPlan, ExecutionPlanApproval } from '../types'
import LoadingState from './ux/LoadingState'
import Notice from './ux/Notice'
import { formatDateTime } from '../utils/dateTime'
import { approvalActorLabel, approvalPolicyLabel, approvalRoleLabel, approvalStatusLabel } from './executionPlanApprovalLabels'

interface Props {
  plan: ExecutionPlan
  canAdmin?: boolean
  onClose: () => void
  // Called after a pending approval is approved/rejected so the parent can
  // refresh any blocked-run state it is holding.
  onResolved?: () => void
  // Why the dialog opened, e.g. a run that was just held for approval.
  notice?: string
  // Starts the plan with the approval its next run uses (readyApprovalId).
  onRunNow?: () => void
  readyApprovalId?: string
  // Why Run now cannot start the plan just now, e.g. a run still in progress.
  runNowBlocked?: string
}

function approvalStatusPill(status: string): string {
  switch (status) {
    case 'approved':
      return 'pp-pill pp-pill-label pp-pill-success'
    case 'rejected':
      return 'pp-pill pp-pill-label pp-pill-danger'
    default:
      return 'pp-pill pp-pill-label pp-pill-info' // pending
  }
}

// Shared formatter: dates read the same, zone included, as the rest of the app.
function formatTimestamp(iso?: string): string {
  return formatDateTime(iso, { seconds: true, fallback: '—' })
}

export default function ExecutionPlanApprovalsModal({ plan, canAdmin = false, onClose, onResolved, notice, onRunNow, readyApprovalId, runNowBlocked }: Props) {
  const api = useExecutionPlansApi()
  const [approvals, setApprovals] = useState<ExecutionPlanApproval[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [reasons, setReasons] = useState<Record<string, string>>({})
  const [loadFailed, setLoadFailed] = useState(false)
  // Which plan's list has arrived: until then the dialog shows it is loading,
  // not that there are no requests.
  const [loadedPlanId, setLoadedPlanId] = useState<string | null>(null)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const activePlanIdRef = useRef(plan.id)
  const loadGenerationRef = useRef(0)
  activePlanIdRef.current = plan.id
  const titleId = useId()

  const noticeId = useId()
  const runNowBlockedId = useId()
  // Opened to say why (a held run), the dialog itself takes focus and is
  // described by the notice, so it is read first and nothing scrolls it away.
  useModalDialog({ dialogRef, initialFocusRef: notice ? dialogRef : closeButtonRef, onClose })

  useEffect(() => {
    loadGenerationRef.current += 1
    setApprovals([])
    setLoading(false)
    setError(null)
    setBusyId(null)
    setReasons({})
    setLoadFailed(false)
  }, [plan.id])

  const load = useCallback(async () => {
    const requestedPlanId = plan.id
    const requestGeneration = ++loadGenerationRef.current
    const isCurrentRequest = () => (
      activePlanIdRef.current === requestedPlanId
      && loadGenerationRef.current === requestGeneration
    )
    setLoading(true)
    setError(null)
    setLoadFailed(false)
    try {
      const res = await api.listExecutionPlanApprovals(requestedPlanId)
      if (!isCurrentRequest()) return
      setApprovals(res.approvals || [])
      setLoadedPlanId(requestedPlanId)
      setLoadFailed(false)
    } catch (err) {
      if (!isCurrentRequest()) return
      setError(err instanceof ApiError ? err.message : 'Failed to load approvals.')
      setLoadFailed(true)
    } finally {
      if (isCurrentRequest()) setLoading(false)
    }
  }, [api, plan.id])

  useEffect(() => {
    void load()
    return () => {
      loadGenerationRef.current += 1
    }
  }, [load])

  const resolve = useCallback(
    async (approval: ExecutionPlanApproval, approve: boolean) => {
      setBusyId(approval.id)
      setError(null)
      try {
        const reason = reasons[approval.id]?.trim() || undefined
        let updated: ExecutionPlanApproval
        if (approve) {
          updated = await api.approveExecutionPlanApproval(plan.id, approval.id, reason)
        } else {
          updated = await api.rejectExecutionPlanApproval(plan.id, approval.id, reason)
        }
        if (activePlanIdRef.current !== plan.id) return
        setApprovals(prev => prev.map(candidate => candidate.id === updated.id ? updated : candidate))
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
  const loaded = loadedPlanId === plan.id

  return (
    <DialogLayer><div
      className="pp-modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <div ref={dialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={notice ? noticeId : undefined}>
        <div className="pp-modal-header">
          <h3 id={titleId}>Approvals — “{plan.name}”</h3>
        </div>
        <div className="pp-modal-form">
          {notice ? <div id={noticeId}><Notice tone="warning">{notice}</Notice></div> : null}
          <div className="pp-row-meta">
            <span>
              A run whose peak is over the project&apos;s VUser limit waits until a project admin approves it. Each approval lets one run start.
              {pendingCount > 0 ? ` ${pendingCount} waiting for a decision.` : ''}
            </span>
          </div>
          {error && (
            <div className="pp-error" role="alert">
              {error}
              {loadFailed && (
                <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busyId !== null} onClick={() => { void load() }}>
                  Retry approvals
                </button>
              )}
            </div>
          )}

          {!loaded ? (
            loadFailed ? null : <LoadingState what="approvals" className="pp-loading" />
          ) : approvals.length === 0 ? (
            <div className="pp-empty">No approval requests. Runs within the project&apos;s VUser limit start without one.</div>
          ) : (
            <div className="pp-list">
              {approvals.map(approval => (
                <div className="pp-row" key={approval.id}>
                  <div className="pp-row-main">
                    <div className="pp-row-title">
                      {approvalPolicyLabel(approval.policy)}
                      <span className={approvalStatusPill(approval.status)}>{approvalStatusLabel(approval)}</span>
                      {approval.required_role && approval.status === 'pending' ? <span className="pp-pill pp-pill-label pp-pill-muted">{approvalRoleLabel(approval.required_role)}</span> : null}
                    </div>
                    {approval.decision_reason ? (
                      <div className="pp-row-meta">
                        <span>{approval.decision_reason}</span>
                      </div>
                    ) : null}
                    <div className="pp-row-meta">
                      <span>Requested {formatTimestamp(approval.requested_at)}{approval.requested_by ? ` by ${approvalActorLabel(approval.requested_by)}` : ''}</span>
                      {approval.resolved_by || approval.resolved_at ? (
                        <span>{approval.status === 'rejected' ? 'Rejected' : 'Approved'}{approval.resolved_at ? ` ${formatTimestamp(approval.resolved_at)}` : ''}{approval.resolved_by ? ` by ${approvalActorLabel(approval.resolved_by)}` : ''}</span>
                      ) : null}
                    </div>
                  </div>
                  {/* One approval lets one run start: the one the plan's next run uses. */}
                  {onRunNow && approval.id === readyApprovalId && approval.status === 'approved' && !approval.run_id ? (
                    <div className="pp-row-actions">
                      <button type="button" className="btn btn-primary btn-sm" disabled={Boolean(runNowBlocked)} aria-describedby={runNowBlocked ? runNowBlockedId : undefined} onClick={onRunNow}>
                        Run now
                      </button>
                      {runNowBlocked ? <span id={runNowBlockedId} className="setting-desc">{runNowBlocked}</span> : null}
                    </div>
                  ) : null}
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
        <div className="pp-modal-actions pp-modal-actions-sticky">
          <button ref={closeButtonRef} type="button" className="btn btn-secondary" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div></DialogLayer>
  )
}
