import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import {
  ExecutionPlan,
  ExecutionPlanSchedule,
  ExecutionPlanScheduleOccurrence,
  ExecutionPlanScheduleRequest,
} from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import { CronBuilder } from './ScheduleCronBuilder'
import LoadingState from './ux/LoadingState'
import { formatDateTime } from '../utils/dateTime'
import {
  datetimeLocalToISO,
  describeCron,
  formatDatetimeLocalValue,
  formatScheduleInstantForViewer,
  formatScheduleToken,
} from './newTestScheduleFormat'
import { ScheduleClockChanges, ScheduleCurrentUtc, ScheduleNextRuns, type SchedulePreviewState } from './ScheduleCadenceFields'
import TimezoneSelect from './ux/TimezoneSelect'
import type { ScheduleClockChange } from '../types/executionPlans'
import { useScheduleClock } from '../hooks/useScheduleClock'
import { occurrenceStatusLabel, overlapPolicyLabel, scheduleHoldSentence } from './executionPlanActivityModel'

interface Props {
  plan: ExecutionPlan
  canManage: boolean
  canAdmin?: boolean
  onClose: () => void
  /** Where the plan files its runs, e.g. "Storefront › R2026.11". */
  planReleaseLabel?: string
  /** Releases a schedule may file its runs under instead of the plan's. */
  releaseOptions?: { id: string; name: string }[]
  /** Opens the plan's approvals, for a scheduled run waiting on one. */
  onReviewApproval?: () => void
}

type ScheduleDraft = {
  id?: string
	revision?: number
  name: string
  cron_expr: string
  timezone: string
  release_id: string
  run_name_template: string
  enabled: boolean
  missed_run_policy: string
  disabled_from: string
  disabled_until: string
	baseline: string
}

type ScheduleDraftFields = Omit<ScheduleDraft, 'baseline'>

function scheduleDraftSnapshot(draft: ScheduleDraftFields | ScheduleDraft): string {
	const fields = { ...draft } as Partial<ScheduleDraft>
	delete fields.baseline
	return JSON.stringify(fields)
}

function withScheduleDraftBaseline(fields: ScheduleDraftFields): ScheduleDraft {
	return { ...fields, baseline: scheduleDraftSnapshot(fields) }
}

function emptyDraft(): ScheduleDraft {
	return withScheduleDraftBaseline({
    name: '',
    cron_expr: '0 2 * * *',
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    release_id: '',
    run_name_template: '',
    enabled: true,
    missed_run_policy: 'skip',
    disabled_from: '',
    disabled_until: '',
	})
}

// Same wording as Settings › Schedule defaults and the select below.
const MISSED_RUN_POLICY_LABELS: Record<string, string> = {
  skip: 'Skip missed runs',
  catch_up_once: 'Catch up once',
  catch_up_all: 'Catch up all',
  require_operator_decision: 'Require operator decision',
}

function isoToLocalInput(iso?: string): string {
  if (!iso) return ''
  const parsed = new Date(iso)
  return Number.isNaN(parsed.getTime()) ? '' : formatDatetimeLocalValue(parsed)
}

// Shared formatter: dates read the same, zone included, as the rest of the app.
function formatTimestamp(iso?: string): string {
  return formatDateTime(iso, { seconds: true, fallback: '—' })
}

function occurrenceStatusPill(status: string, reasonCode?: string): string {
  switch (status) {
    case 'materialized':
      return 'pp-pill pp-pill-success'
    case 'blocked':
      return reasonCode === 'approval_required' || reasonCode === 'waiting_for_previous_run' ? 'pp-pill pp-pill-warning' : 'pp-pill pp-pill-danger'
    case 'failed':
      return 'pp-pill pp-pill-danger'
    case 'materializing':
      return 'pp-pill pp-pill-info'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

export default function ExecutionPlanSchedulesModal({ plan, canManage, canAdmin = false, onClose, planReleaseLabel, releaseOptions = [], onReviewApproval }: Props) {
  const api = useExecutionPlansApi()
	const { runGuarded } = useNavigationGuardRunner()
  const [schedules, setSchedules] = useState<ExecutionPlanSchedule[]>([])
  // Each schedule's daylight-saving changes, by schedule ID.
  const [clockChanges, setClockChanges] = useState<Record<string, ScheduleClockChange[]>>({})
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ExecutionPlanSchedule | null>(null)
  const [occurrencesFor, setOccurrencesFor] = useState<string | null>(null)
  const [occurrences, setOccurrences] = useState<ExecutionPlanScheduleOccurrence[]>([])
  const [occLoading, setOccLoading] = useState(false)
  const [scheduleLoadFailed, setScheduleLoadFailed] = useState(false)
  const [occurrenceReconcileTarget, setOccurrenceReconcileTarget] = useState<{ scheduleId: string; message: string } | null>(null)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const nameInputRef = useRef<HTMLInputElement | null>(null)
  const activePlanIdRef = useRef(plan.id)
  const scheduleLoadGenerationRef = useRef(0)
  const occurrenceLoadGenerationRef = useRef(0)
  const resolveGenerationRef = useRef(0)
  const resolveInFlightRef = useRef(false)
  activePlanIdRef.current = plan.id
  const titleId = useId()
  const releaseHelpId = useId()
  const draftOpen = Boolean(draft)
	const discardScheduleDraft = useCallback(() => {
		setDraft(null)
		setError(null)
	}, [])
	const requestDraftClose = useCallback(() => runGuarded(discardScheduleDraft), [discardScheduleDraft, runGuarded])
	const requestClose = useCallback(() => runGuarded(onClose), [onClose, runGuarded])

	useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose: busy ? undefined : requestClose })

  useEffect(() => {
    scheduleLoadGenerationRef.current += 1
    occurrenceLoadGenerationRef.current += 1
    resolveGenerationRef.current += 1
    resolveInFlightRef.current = false
    setSchedules([])
    setClockChanges({})
    setLoading(false)
    setError(null)
    setBusy(false)
    setDraft(null)
    setDeleteTarget(null)
    setOccurrencesFor(null)
    setOccurrences([])
    setOccLoading(false)
    setScheduleLoadFailed(false)
    setOccurrenceReconcileTarget(null)
  }, [plan.id])

  const load = useCallback(async () => {
    const requestedPlanId = plan.id
    const requestGeneration = ++scheduleLoadGenerationRef.current
    const isCurrentRequest = () => (
      activePlanIdRef.current === requestedPlanId
      && scheduleLoadGenerationRef.current === requestGeneration
    )
    setLoading(true)
    setError(null)
    try {
      const res = await api.listExecutionPlanSchedules(requestedPlanId)
      if (!isCurrentRequest()) return
      setSchedules(res.schedules || [])
      setClockChanges(res.clock_changes || {})
      setScheduleLoadFailed(false)
    } catch (err) {
      if (!isCurrentRequest()) return
      setError(err instanceof ApiError ? err.message : 'Failed to load schedules.')
      setScheduleLoadFailed(true)
    } finally {
      if (isCurrentRequest()) setLoading(false)
    }
  }, [api, plan.id])

  useEffect(() => {
    void load()
    return () => {
      scheduleLoadGenerationRef.current += 1
    }
  }, [load])

  useEffect(() => {
    if (!draftOpen) return
    const timeout = window.setTimeout(() => nameInputRef.current?.focus(), 0)
    return () => window.clearTimeout(timeout)
  }, [draftOpen])

  const startEdit = (schedule: ExecutionPlanSchedule) =>
		setDraft(withScheduleDraftBaseline({
      id: schedule.id,
	  revision: schedule.revision,
      name: schedule.name,
      cron_expr: schedule.cron_expr,
      timezone: schedule.timezone || 'UTC',
      release_id: schedule.release_id || '',
      run_name_template: schedule.run_name_template || '',
      enabled: schedule.enabled,
      missed_run_policy: schedule.missed_run_policy || 'skip',
      disabled_from: isoToLocalInput(schedule.disabled_from),
      disabled_until: isoToLocalInput(schedule.disabled_until),
		}))

	const submitDraft = useCallback(async (): Promise<boolean> => {
		if (!draft) return false
    const payload: ExecutionPlanScheduleRequest = {
      name: draft.name.trim(),
      cron_expr: draft.cron_expr.trim(),
      timezone: draft.timezone.trim() || 'UTC',
      release_id: draft.release_id,
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
      let saved: ExecutionPlanSchedule
      if (draft.id) {
        saved = await api.updateExecutionPlanSchedule(plan.id, draft.id, payload)
      } else {
        saved = await api.createExecutionPlanSchedule(plan.id, payload)
      }
			if (activePlanIdRef.current !== plan.id) return false
      setSchedules(prev => prev.some(schedule => schedule.id === saved.id)
        ? prev.map(schedule => schedule.id === saved.id ? saved : schedule)
        : [...prev, saved])
      setDraft(null)
      await load()
			return true
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save schedule.')
			return false
    } finally {
      setBusy(false)
    }
  }, [api, draft, load, plan.id])

	const draftDirty = Boolean(draft && scheduleDraftSnapshot(draft) !== draft.baseline)
	const draftDirtyRef = useRef(draftDirty)
	draftDirtyRef.current = draftDirty
	const submitDraftRef = useRef(submitDraft)
	submitDraftRef.current = submitDraft
	const draftGuard = useMemo<NavigationGuard>(() => ({
		id: 'execution-plan-schedule-draft',
		label: 'Execution plan schedule editor',
		isDirty: () => draftDirtyRef.current,
		save: () => submitDraftRef.current(),
		discard: discardScheduleDraft,
	}), [discardScheduleDraft])
	useNavigationGuard(draftGuard, draftDirty)

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteExecutionPlanSchedule(plan.id, deleteTarget.id)
      if (activePlanIdRef.current !== plan.id) return
      setSchedules(prev => prev.filter(schedule => schedule.id !== deleteTarget.id))
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
        occurrenceLoadGenerationRef.current += 1
        setOccurrencesFor(null)
        setOccurrences([])
        setOccLoading(false)
        return
      }
      const requestedPlanId = plan.id
      const requestGeneration = ++occurrenceLoadGenerationRef.current
      const isCurrentRequest = () => (
        activePlanIdRef.current === requestedPlanId
        && occurrenceLoadGenerationRef.current === requestGeneration
      )
      setOccurrencesFor(schedule.id)
      setOccurrences([])
      setOccLoading(true)
      try {
        const res = await api.listExecutionPlanScheduleOccurrences(requestedPlanId, schedule.id)
        if (!isCurrentRequest()) return
        setOccurrences(res.occurrences || [])
      } catch (err) {
        if (!isCurrentRequest()) return
        setError(err instanceof ApiError ? err.message : 'Failed to load occurrences.')
      } finally {
        if (isCurrentRequest()) setOccLoading(false)
      }
    },
    [api, occurrencesFor, plan.id],
  )

  const reconcileOccurrenceViews = useCallback(async (requestedPlanId: string, scheduleId: string) => {
    const occurrenceGeneration = ++occurrenceLoadGenerationRef.current
    const scheduleGeneration = ++scheduleLoadGenerationRef.current
    const isCurrentRequest = () => (
      activePlanIdRef.current === requestedPlanId
      && occurrenceLoadGenerationRef.current === occurrenceGeneration
      && scheduleLoadGenerationRef.current === scheduleGeneration
    )
    setOccLoading(true)
    const [occurrenceResult, scheduleResult] = await Promise.allSettled([
      api.listExecutionPlanScheduleOccurrences(requestedPlanId, scheduleId),
      api.listExecutionPlanSchedules(requestedPlanId),
    ])
    if (!isCurrentRequest()) return false

    if (occurrenceResult.status === 'fulfilled') {
      setOccurrencesFor(scheduleId)
      setOccurrences(occurrenceResult.value.occurrences || [])
    }
    if (scheduleResult.status === 'fulfilled') {
      setSchedules(scheduleResult.value.schedules || [])
      setClockChanges(scheduleResult.value.clock_changes || {})
      setScheduleLoadFailed(false)
    }

    const failure = occurrenceResult.status === 'rejected'
      ? occurrenceResult.reason
      : scheduleResult.status === 'rejected'
        ? scheduleResult.reason
        : null
    if (failure) {
      const detail = failure instanceof ApiError ? failure.message : 'Latest schedule state is unavailable.'
      setOccurrenceReconcileTarget({
        scheduleId,
        message: `The occurrence was resolved, but the latest schedule state could not be refreshed. ${detail}`,
      })
      if (scheduleResult.status === 'rejected') setScheduleLoadFailed(true)
    } else {
      setOccurrenceReconcileTarget(null)
      setError(null)
    }
    setLoading(false)
    setOccLoading(false)
    return failure === null
  }, [api])

  const resolveOccurrence = useCallback(async (schedule: ExecutionPlanSchedule, occurrence: ExecutionPlanScheduleOccurrence, action: 'retry' | 'catch_up' | 'skip') => {
    if (!canAdmin || occurrence.reason_code === 'approval_required' || resolveInFlightRef.current) return
    const requestedPlanId = plan.id
    const requestGeneration = ++resolveGenerationRef.current
    const isCurrentRequest = () => (
      activePlanIdRef.current === requestedPlanId
      && resolveGenerationRef.current === requestGeneration
    )
    resolveInFlightRef.current = true
    setBusy(true)
    setError(null)
    setOccurrenceReconcileTarget(null)
    try {
      const result = await api.resolveExecutionPlanScheduleOccurrence(requestedPlanId, schedule.id, occurrence.id, {
        action,
        expected_revision: occurrence.revision,
        reason: `Operator selected ${action.replace('_', ' ')} from the schedule dashboard.`,
      })
      if (!isCurrentRequest()) return
      setOccurrencesFor(schedule.id)
      setOccurrences(prev => prev.some(candidate => candidate.id === result.occurrence.id)
        ? prev.map(candidate => candidate.id === result.occurrence.id ? result.occurrence : candidate)
        : [...prev, result.occurrence])
      setSchedules(prev => prev.some(candidate => candidate.id === result.schedule.id)
        ? prev.map(candidate => candidate.id === result.schedule.id ? result.schedule : candidate)
        : [...prev, result.schedule])
      await reconcileOccurrenceViews(requestedPlanId, schedule.id)
    } catch (err) {
      if (!isCurrentRequest()) return
      setError(err instanceof ApiError ? err.message : 'Failed to resolve occurrence.')
    } finally {
      if (isCurrentRequest()) {
        resolveInFlightRef.current = false
        setBusy(false)
      }
    }
  }, [api, canAdmin, plan.id, reconcileOccurrenceViews])

  const cronGloss = useMemo(
    () => (draft?.cron_expr.trim() ? describeCron(draft.cron_expr) : ''),
    [draft?.cron_expr],
  )
  const clock = useScheduleClock()
  const previewKey = draft ? JSON.stringify([plan.id, draft.cron_expr.trim(), draft.timezone, draft.disabled_from, draft.disabled_until]) : ''
  const [preview, setPreview] = useState<(SchedulePreviewState & { key: string }) | null>(null)
  const currentPreview = preview?.key === previewKey ? preview : null
  const previewPending = Boolean(draft?.cron_expr.trim() && !currentPreview)
  useEffect(() => {
    if (!previewKey) return undefined
    const [planId, cron, timezone, disabledFrom, disabledUntil] = JSON.parse(previewKey) as string[]
    if (!cron || typeof api.previewExecutionPlanSchedule !== 'function') return undefined
    let cancelled = false
    const handle = window.setTimeout(() => {
      api.previewExecutionPlanSchedule(planId, {
        name: 'schedule-preview',
        cron_expr: cron,
        timezone: timezone || 'UTC',
        disabled_from: datetimeLocalToISO(disabledFrom) || undefined,
        disabled_until: datetimeLocalToISO(disabledUntil) || undefined,
      }, 5).then(result => {
        if (!cancelled) setPreview({ key: previewKey, occurrences: result.occurrences || [], error: '', conflictSummary: result.conflict_summary || '', clockChanges: result.clock_changes || [] })
      }).catch(err => {
        if (!cancelled) setPreview({ key: previewKey, occurrences: [], error: err instanceof ApiError ? err.message : 'Invalid schedule' })
      })
    }, 400)
    return () => { cancelled = true; window.clearTimeout(handle) }
  }, [api, previewKey])
  const releaseLabel = (releaseId?: string) => releaseId
    ? releaseOptions.find(release => release.id === releaseId)?.name || `Release ${releaseId}`
    : planReleaseLabel ? `the plan's release (${planReleaseLabel})` : "Imported Legacy (the plan has no release)"
  const draftValid = Boolean(draft && draft.name.trim() && draft.cron_expr.trim())

  return (
    <DialogLayer><div
      className="pp-modal-overlay"
      role="presentation"
      onPointerDown={event => {
				if (event.target === event.currentTarget) requestClose()
      }}
    >
      <div ref={dialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="pp-modal-header">
          <h3 id={titleId}>Schedules — “{plan.name}”</h3>
					<button ref={closeButtonRef} type="button" className="btn btn-ghost btn-sm" onClick={requestClose} disabled={busy}>
            Close
          </button>
        </div>
        <div className="pp-modal-form">
          <div className="pp-row-meta">
            <span>Recurring triggers materialize a plan run on each cron occurrence.</span>
          </div>
          {error && (
            <div className="pp-error" role="alert">
              {error}
              {scheduleLoadFailed && (
                <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
                  Retry schedules
                </button>
              )}
            </div>
          )}
          {occurrenceReconcileTarget && (
            <div className="pp-error" role="alert">
              {occurrenceReconcileTarget.message}
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={busy || occLoading}
                onClick={() => { void reconcileOccurrenceViews(plan.id, occurrenceReconcileTarget.scheduleId) }}
              >
                Retry occurrence refresh
              </button>
            </div>
          )}

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
                <div className="pp-field">
                  <span id={`${titleId}-timezone`}>Timezone</span>
                  <TimezoneSelect labelledBy={`${titleId}-timezone`} value={draft.timezone} now={clock} onChange={timezone => setDraft(d => (d ? { ...d, timezone } : d))} />
                  <ScheduleCurrentUtc now={clock} />
                </div>
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
                <ScheduleNextRuns preview={currentPreview} pending={previewPending} timezone={draft.timezone} />
              </div>

              <label className="pp-field">
                <span>Release</span>
                <select
                  aria-label="Release this schedule's runs are filed under"
                  aria-describedby={plan.application_id ? undefined : releaseHelpId}
                  value={draft.release_id}
                  onChange={e => setDraft(d => (d ? { ...d, release_id: e.target.value } : d))}
                >
                  <option value="">{planReleaseLabel ? `Use ${releaseLabel('')}` : releaseLabel('')}</option>
                  {draft.release_id && !releaseOptions.some(release => release.id === draft.release_id) && <option value={draft.release_id}>{releaseLabel(draft.release_id)}</option>}
                  {releaseOptions.map(release => <option key={release.id} value={release.id}>{release.name}</option>)}
                </select>
                {!plan.application_id && (
                  <span id={releaseHelpId} className="setting-desc pp-field-help">
                    To file scheduled runs under a release, edit the plan and choose its application and release first.
                  </span>
                )}
              </label>

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
                    {Object.entries(MISSED_RUN_POLICY_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
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

              <section className="new-test-schedule-policy" aria-label="What this schedule's runs follow">
                <dl>
                  <dt>Runs are filed under</dt><dd>{releaseLabel(draft.release_id)}</dd>
                  <dt>Missed runs</dt><dd>{MISSED_RUN_POLICY_LABELS[draft.missed_run_policy] || formatScheduleToken(draft.missed_run_policy)}</dd>
                  <dt>If the plan is still running</dt><dd>{overlapPolicyLabel(plan.overlap_policy)} (set on the plan)</dd>
                  <dt>Over the project quota</dt><dd>The run waits for a project admin's approval</dd>
                </dl>
              </section>

              <label className="pp-entry-enabled" title="Enabled">
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={e => setDraft(d => (d ? { ...d, enabled: e.target.checked } : d))}
                />
                enabled
              </label>

              <div className="pp-modal-actions">
								<button type="button" className="btn btn-secondary" onClick={requestDraftClose} disabled={busy}>
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
            <LoadingState what="schedules" className="pp-loading" />
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
                        <span className="pp-pill pp-pill-warning">Waiting for approval</span>
                      )}
                      {schedule.next_required_action === 'waiting_for_previous_run' && (
                        <span className="pp-pill pp-pill-info">Queued</span>
                      )}
                    </div>
                    <div className="pp-row-meta">
                      <span>
                        {schedule.cron_expr}
                        {schedule.timezone ? ` / ${schedule.timezone}` : ''}
                      </span>
                      {scheduleHoldSentence(schedule.next_required_action, schedule.next_run_at)
                        ? <span>{scheduleHoldSentence(schedule.next_required_action, schedule.next_run_at)}</span>
                        : <span>next: {formatScheduleInstantForViewer(schedule.next_run_at, schedule.timezone || 'UTC')}</span>}
                      {schedule.last_run_at ? <span>last: {formatTimestamp(schedule.last_run_at)}</span> : null}
                      <span>files runs under {releaseLabel(schedule.release_id)}</span>
                      {schedule.next_required_action === 'approval_required' && onReviewApproval ? (
                        <button type="button" className="btn btn-ghost btn-sm" onClick={onReviewApproval}>
                          {canAdmin ? 'Review approval' : 'View approval'}
                        </button>
                      ) : null}
                    </div>
                    <ScheduleClockChanges changes={clockChanges[schedule.id]} timezone={schedule.timezone || 'UTC'} />
                    {schedule.last_error && schedule.next_required_action !== 'approval_required' && schedule.next_required_action !== 'waiting_for_previous_run' ? (
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
                        <LoadingState what="runs" className="pp-loading" />
                      ) : occurrences.length === 0 ? (
                        <div className="pp-empty">No runs materialized yet.</div>
                      ) : (
						occurrences.map(occ => (
						  <div className="pp-row-meta pp-occurrence-row" key={occ.id}>
                            <span className={occurrenceStatusPill(occ.status, occ.reason_code)}>{occurrenceStatusLabel(occ.status, occ.reason_code)}</span>
                            <span>{formatTimestamp(occ.planned_at)}</span>
                            {occ.conflict_summary ? <span>{occ.conflict_summary}</span> : null}
                            {occ.status === 'blocked' && occ.reason_code === 'approval_required' && onReviewApproval ? (
                              <button type="button" className="btn btn-ghost btn-sm" onClick={onReviewApproval}>{canAdmin ? 'Review approval' : 'View approval'}</button>
                            ) : null}
							{canAdmin && occ.reason_code !== 'approval_required' && occ.reason_code !== 'waiting_for_previous_run' && (occ.status === 'blocked' || occ.status === 'failed') ? (
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
					<button type="button" className="btn btn-primary" onClick={requestClose} disabled={busy}>
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
    </div></DialogLayer>
  )
}
