import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import {
  ExecutionPlan,
  ExecutionPlanSchedule,
  ExecutionPlanScheduleOccurrence,
  ExecutionPlanScheduleRequest,
} from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import { CronBuilder } from './ScheduleCronBuilder'
import {
  COMMON_TIMEZONES,
  datetimeLocalToISO,
  describeCron,
  formatDatetimeLocalValue,
} from './newTestScheduleFormat'

interface Props {
  plan: ExecutionPlan
  canManage: boolean
  canAdmin?: boolean
  onClose: () => void
}

type ScheduleDraft = {
  id?: string
	revision?: number
  name: string
  cron_expr: string
  timezone: string
  run_name_template: string
  enabled: boolean
  missed_run_policy: string
  disabled_from: string
  disabled_until: string
}

function emptyDraft(): ScheduleDraft {
  return {
    name: '',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    run_name_template: '',
    enabled: true,
    missed_run_policy: 'skip',
    disabled_from: '',
    disabled_until: '',
  }
}

function isoToLocalInput(iso?: string): string {
  if (!iso) return ''
  const parsed = new Date(iso)
  return Number.isNaN(parsed.getTime()) ? '' : formatDatetimeLocalValue(parsed)
}

function formatTimestamp(iso?: string): string {
  if (!iso) return '—'
  const parsed = new Date(iso)
  return Number.isNaN(parsed.getTime()) ? iso : parsed.toLocaleString()
}

function occurrenceStatusPill(status: string): string {
  switch (status) {
    case 'materialized':
      return 'pp-pill pp-pill-success'
    case 'blocked':
    case 'failed':
      return 'pp-pill pp-pill-danger'
    case 'materializing':
      return 'pp-pill pp-pill-info'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

export default function ExecutionPlanSchedulesModal({ plan, canManage, canAdmin = false, onClose }: Props) {
  const api = useExecutionPlansApi()
  const [schedules, setSchedules] = useState<ExecutionPlanSchedule[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ExecutionPlanSchedule | null>(null)
  const [occurrencesFor, setOccurrencesFor] = useState<string | null>(null)
  const [occurrences, setOccurrences] = useState<ExecutionPlanScheduleOccurrence[]>([])
  const [occLoading, setOccLoading] = useState(false)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const nameInputRef = useRef<HTMLInputElement | null>(null)
  const titleId = useId()
  const draftOpen = Boolean(draft)

  useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose })

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.listExecutionPlanSchedules(plan.id)
      setSchedules(res.schedules || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load schedules.')
      setSchedules([])
    } finally {
      setLoading(false)
    }
  }, [api, plan.id])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!draftOpen) return
    const timeout = window.setTimeout(() => nameInputRef.current?.focus(), 0)
    return () => window.clearTimeout(timeout)
  }, [draftOpen])

  const startEdit = (schedule: ExecutionPlanSchedule) =>
    setDraft({
      id: schedule.id,
	  revision: schedule.revision,
      name: schedule.name,
      cron_expr: schedule.cron_expr,
      timezone: schedule.timezone || 'UTC',
      run_name_template: schedule.run_name_template || '',
      enabled: schedule.enabled,
      missed_run_policy: schedule.missed_run_policy || 'skip',
      disabled_from: isoToLocalInput(schedule.disabled_from),
      disabled_until: isoToLocalInput(schedule.disabled_until),
    })

  const submitDraft = useCallback(async () => {
    if (!draft) return
    const payload: ExecutionPlanScheduleRequest = {
      name: draft.name.trim(),
      cron_expr: draft.cron_expr.trim(),
      timezone: draft.timezone.trim() || 'UTC',
      run_name_template: draft.run_name_template.trim() || undefined,
      enabled: draft.enabled,
      missed_run_policy: draft.missed_run_policy || undefined,
      disabled_from: datetimeLocalToISO(draft.disabled_from) || undefined,
      disabled_until: datetimeLocalToISO(draft.disabled_until) || undefined,
	  expected_revision: draft.id ? draft.revision : undefined,
    }
    setBusy(true)
    setError(null)
    try {
      if (draft.id) {
        await api.updateExecutionPlanSchedule(plan.id, draft.id, payload)
      } else {
        await api.createExecutionPlanSchedule(plan.id, payload)
      }
      setDraft(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save schedule.')
    } finally {
      setBusy(false)
    }
  }, [api, draft, load, plan.id])

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteExecutionPlanSchedule(plan.id, deleteTarget.id)
      setDeleteTarget(null)
      if (occurrencesFor === deleteTarget.id) {
        setOccurrencesFor(null)
        setOccurrences([])
      }
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete schedule.')
    } finally {
      setBusy(false)
    }
  }, [api, deleteTarget, load, occurrencesFor, plan.id])

  const toggleOccurrences = useCallback(
    async (schedule: ExecutionPlanSchedule) => {
      if (occurrencesFor === schedule.id) {
        setOccurrencesFor(null)
        setOccurrences([])
        return
      }
      setOccurrencesFor(schedule.id)
      setOccurrences([])
      setOccLoading(true)
      try {
        const res = await api.listExecutionPlanScheduleOccurrences(plan.id, schedule.id)
        setOccurrences(res.occurrences || [])
      } catch (err) {
        setError(err instanceof ApiError ? err.message : 'Failed to load occurrences.')
      } finally {
        setOccLoading(false)
      }
    },
    [api, occurrencesFor, plan.id],
  )

	const resolveOccurrence = useCallback(async (schedule: ExecutionPlanSchedule, occurrence: ExecutionPlanScheduleOccurrence, action: 'retry' | 'catch_up' | 'skip') => {
		if (!canAdmin || occurrence.reason_code === 'approval_required') return
		setBusy(true)
		setError(null)
		try {
			await api.resolveExecutionPlanScheduleOccurrence(plan.id, schedule.id, occurrence.id, {
				action,
				expected_revision: occurrence.revision,
				reason: `Operator selected ${action.replace('_', ' ')} from the schedule dashboard.`,
			})
			const res = await api.listExecutionPlanScheduleOccurrences(plan.id, schedule.id)
			setOccurrences(res.occurrences || [])
			await load()
		} catch (err) {
			setError(err instanceof ApiError ? err.message : 'Failed to resolve occurrence.')
		} finally {
			setBusy(false)
		}
	}, [api, canAdmin, load, plan.id])

  const cronGloss = useMemo(
    () => (draft?.cron_expr.trim() ? describeCron(draft.cron_expr) : ''),
    [draft?.cron_expr],
  )
  const draftValid = Boolean(draft && draft.name.trim() && draft.cron_expr.trim())

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
          <h3 id={titleId}>Schedules — “{plan.name}”</h3>
          <button ref={closeButtonRef} type="button" className="btn btn-ghost btn-sm" onClick={onClose}>
            Close
          </button>
        </div>
        <div className="pp-modal-form">
          <div className="pp-row-meta">
            <span>Recurring triggers materialize a plan run on each cron occurrence.</span>
          </div>
          {error && <div className="pp-error" role="alert">{error}</div>}

          {!draft && canManage && (
            <button type="button" className="btn btn-primary btn-sm" onClick={() => setDraft(emptyDraft())}>
              New schedule
            </button>
          )}

          {/* Create / edit form */}
          {draft && (
            <div className="pp-entry-block">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input
                    ref={nameInputRef}
                    value={draft.name}
                    placeholder="e.g. nightly"
                    onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))}
                  />
                </label>
                <label className="pp-field">
                  <span>Timezone</span>
                  <select value={draft.timezone} onChange={e => setDraft(d => (d ? { ...d, timezone: e.target.value } : d))}>
                    {COMMON_TIMEZONES.map(tz => (
                      <option key={tz} value={tz}>
                        {tz}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <div className="pp-field">
                <span>Cron schedule</span>
                {cronGloss && (
                  <span className="setting-desc cron-preview">
                    {cronGloss}
                    {draft.timezone ? ` (${draft.timezone})` : ''}
                  </span>
                )}
                <CronBuilder
                  value={draft.cron_expr}
                  disabled={!canManage}
                  onChange={expr => setDraft(d => (d ? { ...d, cron_expr: expr } : d))}
                />
                <input
                  className="cron-raw-input"
                  value={draft.cron_expr}
                  disabled={!canManage}
                  placeholder="0 2 * * *"
                  aria-label="Raw cron expression"
                  onChange={e => setDraft(d => (d ? { ...d, cron_expr: e.target.value } : d))}
                />
              </div>

              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Run name template</span>
                  <input
                    value={draft.run_name_template}
                    placeholder="optional, e.g. {{plan}} {{date}}"
                    onChange={e => setDraft(d => (d ? { ...d, run_name_template: e.target.value } : d))}
                  />
                </label>
                <label className="pp-field">
                  <span>Missed-run policy</span>
                  <select
                    value={draft.missed_run_policy}
                    onChange={e => setDraft(d => (d ? { ...d, missed_run_policy: e.target.value } : d))}
                  >
                    <option value="skip">skip</option>
                    <option value="catch_up_once">catch_up_once</option>
                    <option value="catch_up_all">catch_up_all</option>
					<option value="require_operator_decision">require_operator_decision</option>
                  </select>
                </label>
              </div>

              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Pause from</span>
                  <input
                    type="datetime-local"
                    value={draft.disabled_from}
                    onChange={e => setDraft(d => (d ? { ...d, disabled_from: e.target.value } : d))}
                  />
                </label>
                <label className="pp-field">
                  <span>Pause until</span>
                  <input
                    type="datetime-local"
                    value={draft.disabled_until}
                    onChange={e => setDraft(d => (d ? { ...d, disabled_until: e.target.value } : d))}
                  />
                </label>
              </div>

              <label className="pp-entry-enabled" title="Enabled">
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={e => setDraft(d => (d ? { ...d, enabled: e.target.checked } : d))}
                />
                enabled
              </label>

              <div className="pp-modal-actions">
                <button type="button" className="btn btn-secondary" onClick={() => setDraft(null)}>
                  Cancel
                </button>
                <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                  {busy ? 'Saving…' : draft.id ? 'Save' : 'Create'}
                </button>
              </div>
            </div>
          )}

          {/* Schedule list */}
          {loading ? (
            <div className="pp-empty">Loading schedules…</div>
          ) : schedules.length === 0 ? (
            <div className="pp-empty">No schedules yet. Add one to run this plan on a recurring cadence.</div>
          ) : (
            <div className="pp-list">
              {schedules.map(schedule => (
                <div className="pp-schedule-item" key={schedule.id}>
                  <div className="pp-row">
                  <div className="pp-row-main">
                    <div className="pp-row-title">
                      {schedule.name}
                      <span className={schedule.enabled ? 'pp-pill pp-pill-success' : 'pp-pill pp-pill-muted'}>
                        {schedule.enabled ? 'enabled' : 'paused'}
                      </span>
                      {schedule.next_required_action === 'approval_required' && (
                        <span className="pp-pill pp-pill-danger">approval required</span>
                      )}
                    </div>
                    <div className="pp-row-meta">
                      <span>
                        {schedule.cron_expr}
                        {schedule.timezone ? ` / ${schedule.timezone}` : ''}
                      </span>
                      <span>next: {formatTimestamp(schedule.next_run_at)}</span>
                      {schedule.last_run_at ? <span>last: {formatTimestamp(schedule.last_run_at)}</span> : null}
                    </div>
                    {schedule.last_error ? (
                      <div className="pp-row-meta">
                        <span className="pp-pill pp-pill-danger">last error</span>
                        <span>{schedule.last_error}</span>
                      </div>
                    ) : null}
                  </div>
                  <div className="pp-row-actions">
                    <button type="button" className="btn btn-ghost btn-sm" onClick={() => void toggleOccurrences(schedule)}>
                      {occurrencesFor === schedule.id ? 'Hide runs' : 'Runs'}
                    </button>
                    {canManage && (
                      <>
                        <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => startEdit(schedule)}>
                          Edit
                        </button>
                        <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(schedule)}>
                          Delete
                        </button>
                      </>
                    )}
                  </div>
                  </div>
                  {occurrencesFor === schedule.id && (
                    <div className="pp-occurrences">
                      {occLoading ? (
                        <div className="pp-empty">Loading runs…</div>
                      ) : occurrences.length === 0 ? (
                        <div className="pp-empty">No runs materialized yet.</div>
                      ) : (
						occurrences.map(occ => (
						  <div className="pp-row-meta pp-occurrence-row" key={occ.id}>
                            <span className={occurrenceStatusPill(occ.status)}>{occ.status}</span>
                            <span>{formatTimestamp(occ.planned_at)}</span>
                            {occ.reason_code ? <span>{occ.reason_code}</span> : null}
                            {occ.run_id ? <span>run {occ.run_id}</span> : null}
                            {occ.conflict_summary ? <span>{occ.conflict_summary}</span> : null}
							{canAdmin && occ.reason_code !== 'approval_required' && (occ.status === 'blocked' || occ.status === 'failed') ? (
							  <span className="pp-row-actions" aria-label="Resolve occurrence">
								<button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void resolveOccurrence(schedule, occ, 'retry')}>Retry</button>
								<button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void resolveOccurrence(schedule, occ, 'catch_up')}>Catch up</button>
								<button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void resolveOccurrence(schedule, occ, 'skip')}>Skip</button>
							  </span>
							) : null}
                          </div>
                        ))
                      )}
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

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete schedule"
          description={`This stops recurring runs for “${deleteTarget.name}”.`}
          target={deleteTarget.name}
          targetLabel="Schedule"
          confirmLabel="Delete schedule"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
