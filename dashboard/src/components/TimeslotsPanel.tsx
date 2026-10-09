import DialogLayer from './ux/DialogLayer'
import { settingsHref } from './settingsNavigation'
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { useScriptApi } from '../hooks/api/useScriptApi'
import { useCapacityApi } from '../hooks/api/useCapacityApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { CapacityTimeslot, FleetPool } from '../types'
import { confirmAction } from './ux/confirmAction'
import { listAllFleetPools } from './fleetCatalogModel'
import { listAllCapacityTimeslots } from './schedulingCatalogModel'
import { zonedLocalToISO } from './timeslotTimezoneModel'
import './parityPanels.css'
import TimezoneSelect from './ux/TimezoneSelect'
import { formatDateTime as formatDisplayDateTime } from '../utils/dateTime'

interface Props {
  projectId?: string
  canManage?: boolean
}

function approvalPill(state: string): string {
  switch (state) {
    case 'approved':
      return 'pp-pill pp-pill-success'
    case 'rejected':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-warning'
  }
}

// Reservation windows are rendered in the slot's own IANA zone so the printed
// wall-clock agrees with the zone name shown next to it.
function fmt(value?: string, timezone?: string): string {
  return formatDisplayDateTime(value, { timeZone: timezone })
}

type Draft = {
  scenario_id: string
  start_local: string
  end_local: string
  timezone: string
  generators: string
  vusers: string
  pool_id: string
	baseline: string
}

type DraftFields = Omit<Draft, 'baseline'>

function draftSnapshot(draft: DraftFields | Draft): string {
	const fields = { ...draft } as Partial<Draft>
	delete fields.baseline
	return JSON.stringify(fields)
}

function withDraftBaseline(fields: DraftFields): Draft {
	return { ...fields, baseline: draftSnapshot(fields) }
}

function emptyDraft(): Draft {
  let tz = 'UTC'
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    tz = 'UTC'
  }
	return withDraftBaseline({ scenario_id: '', start_local: '', end_local: '', timezone: tz, generators: '', vusers: '', pool_id: '' })
}

export default function TimeslotsPanel({ projectId, canManage = false }: Props) {
  const api = useCapacityApi()
  const { listScenarios } = useScriptApi()
  const [scenarios, setScenarios] = useState<{ id: string; name: string }[]>([])
  const [scenarioError, setScenarioError] = useState('')
  useEffect(() => {
    let current = true
    setScenarios([])
    if (projectId) listScenarios(projectId).then(result => { if (current) { setScenarios(result.scenarios || []); setScenarioError('') } }).catch(() => { if (current) setScenarioError('Scenario catalog unavailable. You can book capacity without a scenario.') })
    return () => { current = false }
  }, [projectId, listScenarios])
  const fleetApi = useFleetApi()
	const { runGuarded } = useNavigationGuardRunner()
  const [timeslots, setTimeslots] = useState<CapacityTimeslot[]>([])
  const [pools, setPools] = useState<FleetPool[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [availability, setAvailability] = useState<string | null>(null)
  const [availabilityError, setAvailabilityError] = useState(false)
  const [bindFor, setBindFor] = useState<{ slot: CapacityTimeslot; testId: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const scopeRef = useRef({ projectId, generation: 0 })
  const loadGenerationRef = useRef(0)
  if (scopeRef.current.projectId !== projectId) {
    scopeRef.current = { projectId, generation: scopeRef.current.generation + 1 }
  }
  const isCurrentScope = useCallback((scope: { projectId?: string; generation: number }) => (
    scopeRef.current.projectId === scope.projectId && scopeRef.current.generation === scope.generation
  ), [])
  const draftDialogRef = useRef<HTMLDivElement | null>(null)
  const startInputRef = useRef<HTMLInputElement | null>(null)
  const bindDialogRef = useRef<HTMLDivElement | null>(null)
  const bindTestInputRef = useRef<HTMLInputElement | null>(null)
  const reservationTitleId = useId()
  const bindTitleId = useId()

	const discardDraft = useCallback(() => {
		setDraft(null)
		setAvailability(null)
		setAvailabilityError(false)
		setError(null)
	}, [])
	const discardBind = useCallback(() => {
		setBindFor(null)
		setError(null)
	}, [])
	const closeDraft = useCallback(() => runGuarded(discardDraft), [discardDraft, runGuarded])
	const closeBind = useCallback(() => runGuarded(discardBind), [discardBind, runGuarded])
  useModalDialog({
    dialogRef: draftDialogRef,
    enabled: Boolean(draft),
    initialFocusRef: startInputRef,
		onClose: busy ? undefined : closeDraft,
  })
  useModalDialog({
    dialogRef: bindDialogRef,
    enabled: Boolean(bindFor),
    initialFocusRef: bindTestInputRef,
		onClose: busy ? undefined : closeBind,
  })

  useLayoutEffect(() => {
    loadGenerationRef.current += 1
    setTimeslots([])
    setLoading(false)
    setError(null)
    setDraft(null)
    setAvailability(null)
    setAvailabilityError(false)
    setBindFor(null)
    setBusy(false)
  }, [projectId])

  const load = useCallback(async () => {
    const scope = { ...scopeRef.current }
    const requestGeneration = ++loadGenerationRef.current
    if (!projectId) {
      if (isCurrentScope(scope)) setTimeslots([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const nextTimeslots = await listAllCapacityTimeslots(api.listTimeslots, projectId)
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      setTimeslots(nextTimeslots)
    } catch (err) {
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      setError(err instanceof ApiError ? err.message : 'Failed to load timeslots.')
    } finally {
      if (isCurrentScope(scope) && requestGeneration === loadGenerationRef.current) setLoading(false)
    }
  }, [api, isCurrentScope, projectId])

  useEffect(() => {
    void load()
  }, [load])

  // Fleet pools power the optional pool-scoping selector (reserve within one pool's
  // capacity envelope instead of the global pool).
  useEffect(() => {
    let cancelled = false
    listAllFleetPools(fleetApi.listFleetPools).then(nextPools => {
      if (!cancelled) setPools(nextPools)
    }).catch(() => {
      if (!cancelled) setPools([])
    })
    return () => { cancelled = true }
  }, [fleetApi])

  const poolName = useCallback((id?: string) => {
    if (!id) return ''
    return pools.find(p => p.id === id)?.name || id
  }, [pools])
	const applyCommittedTimeslot = useCallback((saved: CapacityTimeslot) => {
		setTimeslots(current => {
			const existing = current.find(timeslot => timeslot.id === saved.id)
			const committed = existing ? { ...existing, ...saved } : saved
			return [...current.filter(timeslot => timeslot.id !== saved.id), committed]
		})
	}, [])

  const draftValid = useMemo(
    () => Boolean(draft && (() => {
      const start = zonedLocalToISO(draft.start_local, draft.timezone)
      const end = zonedLocalToISO(draft.end_local, draft.timezone)
      return start && end && new Date(start) < new Date(end)
    })()),
    [draft],
  )
  // Say why booking is unavailable next to the fields, not just by disabling buttons.
  const draftRangeError = draft && draft.start_local && draft.end_local && !draftValid ? 'End must be after Start.' : ''
  const draftGeneratorsError = draft && draft.generators !== '' && !(Number.isInteger(Number(draft.generators)) && Number(draft.generators) >= 1) ? 'Enter a whole number of generators, 1 or more.' : ''
  const reservationActionDisabled = !canManage || !projectId
  const reservationActionTitle = !canManage
    ? 'Admin access required to create reservations'
    : projectId ? undefined : 'Select a project'

  const checkAvailability = useCallback(async () => {
    if (!draft || !projectId || !draftValid) return
    const scope = { ...scopeRef.current }
    setBusy(true)
    setAvailability(null)
    setAvailabilityError(false)
    try {
      const res = await api.checkTimeslotAvailability({ project_id: projectId, start_at: zonedLocalToISO(draft.start_local, draft.timezone), end_at: zonedLocalToISO(draft.end_local, draft.timezone) })
      if (!isCurrentScope(scope)) return
      setAvailability(res.available ? 'Window is available — no conflicting reservations.' : res.summary || `${res.count} conflicting reservation(s) in this window.`)
    } catch (err) {
      if (isCurrentScope(scope)) {
        setAvailability(err instanceof ApiError ? err.message : 'Availability check failed.')
        setAvailabilityError(true)
      }
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
  }, [api, draft, draftValid, isCurrentScope, projectId])

	const submitDraft = useCallback(async (): Promise<boolean> => {
		if (!draft || !projectId) return false
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    const requested =
      draft.generators || draft.vusers || draft.pool_id
        ? {
            generators: draft.generators ? Number(draft.generators) : undefined,
            vusers: draft.vusers ? Number(draft.vusers) : undefined,
            pool_id: draft.pool_id || undefined,
          }
        : undefined
    try {
			const saved = await api.createTimeslot({
        project_id: projectId,
        scenario_id: draft.scenario_id.trim() || undefined,
        start_at: zonedLocalToISO(draft.start_local, draft.timezone),
        end_at: zonedLocalToISO(draft.end_local, draft.timezone),
        timezone: draft.timezone.trim() || undefined,
        requested_capacity: requested,
      })
			if (!isCurrentScope(scope)) return false
			applyCommittedTimeslot(saved)
      setDraft(null)
      setAvailability(null)
      setAvailabilityError(false)
      await load()
			return true
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to create timeslot.')
			return false
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
	}, [api, applyCommittedTimeslot, draft, isCurrentScope, load, projectId])

  const act = useCallback(
		async (fn: () => Promise<unknown>, failMsg: string, applyCommitted?: (result: unknown) => void): Promise<boolean> => {
      const scope = { ...scopeRef.current }
      setBusy(true)
      setError(null)
      try {
				const result = await fn()
        if (!isCurrentScope(scope)) return false
				applyCommitted?.(result)
        await load()
        return true
      } catch (err) {
        if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : failMsg)
        return false
      } finally {
        if (isCurrentScope(scope)) setBusy(false)
      }
    },
    [isCurrentScope, load],
  )

  // Reservations are named by their window: retyping an internal id added
  // friction without protecting anything a named confirmation does not.
  const deleteReservation = async (slot: CapacityTimeslot) => {
    const window = `${fmt(slot.start_at, slot.timezone)} → ${fmt(slot.end_at, slot.timezone)}${slot.timezone ? ` (${slot.timezone})` : ''}`
    if (!(await confirmAction(`Permanently delete the capacity reservation for ${window}?`, { title: 'Delete capacity reservation', confirmLabel: 'Delete reservation', destructive: true }))) return
    await act(
      () => api.deleteTimeslot(slot.id),
      'Failed to delete.',
      () => setTimeslots(current => current.filter(timeslot => timeslot.id !== slot.id)),
    )
  }

  const approveSlot = useCallback(async (slot: CapacityTimeslot) => {
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    try {
			const saved = await api.approveTimeslot(slot.id)
      if (!isCurrentScope(scope)) return
			applyCommittedTimeslot(saved)
      await load()
    } catch (err) {
      if (!isCurrentScope(scope)) return
      if (err instanceof ApiError && err.status === 409 && err.message.includes('conflict_override')) {
        const reason = window.prompt('This reservation overlaps existing capacity. Enter an override reason to approve anyway:')
        if (reason?.trim()) {
          try {
							const saved = await api.approveTimeslot(slot.id, { conflict_override: true, reason: reason.trim() })
            if (!isCurrentScope(scope)) return
							applyCommittedTimeslot(saved)
            await load()
            return
          } catch (overrideErr) {
            if (isCurrentScope(scope)) {
              setError(overrideErr instanceof ApiError ? overrideErr.message : 'Failed to approve with override.')
            }
            return
          }
        }
      }
      setError(err instanceof ApiError ? err.message : 'Failed to approve.')
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
	}, [api, applyCommittedTimeslot, isCurrentScope, load])

	const submitBind = useCallback(async (): Promise<boolean> => {
		if (!bindFor || !bindFor.testId.trim()) return false
		const succeeded = await act(
			() => api.bindTimeslot(bindFor.slot.id, bindFor.testId.trim()),
			'Failed to bind timeslot.',
			result => applyCommittedTimeslot(result as CapacityTimeslot),
		)
    if (succeeded) setBindFor(null)
		return succeeded
	}, [act, api, applyCommittedTimeslot, bindFor])

	const draftDirty = Boolean(draft && draftSnapshot(draft) !== draft.baseline)
	const bindDirty = Boolean(bindFor?.testId.trim())
	const editorDirty = draftDirty || bindDirty
	const editorDirtyRef = useRef(editorDirty)
	editorDirtyRef.current = editorDirty
	const saveEditorRef = useRef<() => Promise<boolean>>(async () => false)
	saveEditorRef.current = async () => {
		if (draftDirty) return submitDraft()
		if (bindDirty) return submitBind()
		return true
	}
	const discardEditor = useCallback(() => {
		discardDraft()
		discardBind()
	}, [discardBind, discardDraft])
	const editorGuard = useMemo<NavigationGuard>(() => ({
		id: 'capacity-timeslot-draft',
		label: 'Capacity reservation editor',
		isDirty: () => editorDirtyRef.current,
		save: () => saveEditorRef.current(),
		discard: discardEditor,
	}), [discardEditor])
	useNavigationGuard(editorGuard, editorDirty)

  return (
    <div className="pp-panel">
      <nav aria-label="Schedule configuration"><a href={settingsHref('scheduling')}>Schedule defaults</a> · <a href={settingsHref('allocations')}>Capacity commitments and standing allocations</a></nav>
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {timeslots.length} reservation{timeslots.length === 1 ? '' : 's'} — scheduled capacity windows with approval. Standing allocations are managed separately in Settings.
        </span>
        <button
          type="button"
          className="btn btn-primary btn-sm"
          disabled={reservationActionDisabled}
          title={reservationActionTitle}
          onClick={() => { setDraft(emptyDraft()); setAvailability(null); setAvailabilityError(false) }}
        >
          New booking
        </button>
      </div>

      {error && <div className="pp-error" role="alert">{error}</div>}

      {!projectId ? (
        <div className="pp-empty">Select a project to view its capacity reservations.</div>
      ) : loading ? (
        <div className="pp-empty">Loading reservations…</div>
      ) : timeslots.length === 0 ? (
        <div className="pp-empty">No capacity reservations. Reserve a future window of generator capacity for a planned run.</div>
      ) : (
        <div className="pp-list">
          {timeslots.map(slot => (
            <div className="pp-row" key={slot.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {fmt(slot.start_at, slot.timezone)} → {fmt(slot.end_at, slot.timezone)}
                  <span className={approvalPill(slot.approval_state)}>{slot.approval_state}</span>
                  {slot.bound_test_id ? <span className="pp-pill pp-pill-info">bound</span> : null}
                </div>
                <div className="pp-row-meta">
                  {slot.timezone ? <span>{slot.timezone}</span> : null}
                  {slot.scenario_id ? <span>scenario: {slot.scenario_id}</span> : null}
                  {slot.requested_capacity?.generators ? <span>{slot.requested_capacity.generators} gen</span> : null}
                  {slot.requested_capacity?.pool_id ? <a className="pp-pill pp-pill-muted" href={settingsHref('pools', window.location.hash, slot.requested_capacity.pool_id)}>pool: {poolName(slot.requested_capacity.pool_id)}</a> : null}
                  {slot.conflict_summary ? <span>{slot.conflict_summary}</span> : null}
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  {slot.approval_state === 'pending' && (
                    <>
                      <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void approveSlot(slot)}>
                        Approve
                      </button>
												<button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void act(
													() => api.rejectTimeslot(slot.id),
													'Failed to reject.',
													result => applyCommittedTimeslot(result as CapacityTimeslot),
												)}>
                        Reject
                      </button>
                    </>
                  )}
                  {slot.approval_state === 'approved' && !slot.bound_test_id && (
                    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setBindFor({ slot, testId: '' })}>
                      Bind test
                    </button>
                  )}
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} aria-label={`Delete reservation ${fmt(slot.start_at, slot.timezone)} → ${fmt(slot.end_at, slot.timezone)}`} onClick={() => void deleteReservation(slot)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {draft && (
        <DialogLayer><div
          className="pp-modal-overlay"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) closeDraft()
          }}
        >
          <div ref={draftDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={reservationTitleId}>
            <div className="pp-modal-header">
              <h3 id={reservationTitleId}>New scheduled booking</h3>
								<button type="button" className="btn btn-ghost btn-sm" onClick={closeDraft} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Start</span>
                  <input ref={startInputRef} type="datetime-local" value={draft.start_local} onChange={e => { setDraft(d => (d ? { ...d, start_local: e.target.value } : d)); setAvailability(null); setAvailabilityError(false) }} />
                </label>
                <label className="pp-field">
                  <span>End</span>
                  <input type="datetime-local" value={draft.end_local} aria-invalid={Boolean(draftRangeError) || undefined} aria-describedby={draftRangeError ? 'timeslot-range-error' : undefined} onChange={e => { setDraft(d => (d ? { ...d, end_local: e.target.value } : d)); setAvailability(null); setAvailabilityError(false) }} />
                </label>
              </div>
              {draftRangeError && <p id="timeslot-range-error" className="field-error" role="alert">{draftRangeError}</p>}
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Time zone</span>
                  <TimezoneSelect ariaLabel="Time zone" value={draft.timezone} onChange={timezone => setDraft(d => (d ? { ...d, timezone } : d))} />
                </label>
                <label className="pp-field">
                  <span>Scenario (optional)</span>
                  <select value={draft.scenario_id} onChange={e => setDraft(d => (d ? { ...d, scenario_id: e.target.value } : d))}><option value="">No scenario</option>{scenarios.map(scenario => <option key={scenario.id} value={scenario.id}>{scenario.name}</option>)}</select>{scenarioError && <span role="status">{scenarioError}</span>}
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Generators (optional)</span>
                  <input type="number" min={1} step={1} value={draft.generators} aria-invalid={Boolean(draftGeneratorsError) || undefined} aria-describedby="timeslot-generators-note" onChange={e => setDraft(d => (d ? { ...d, generators: e.target.value } : d))} />
                  <small id="timeslot-generators-note" className={draftGeneratorsError ? 'field-error' : 'field-advisory'} role={draftGeneratorsError ? 'alert' : undefined}>{draftGeneratorsError || 'Requests above the configured fleet go to an approver; Check availability shows conflicts.'}</small>
                </label>
                <label className="pp-field">
                  <span>VUsers (optional)</span>
                  <input type="number" min={0} value={draft.vusers} onChange={e => setDraft(d => (d ? { ...d, vusers: e.target.value } : d))} />
                </label>
              </div>
              <label className="pp-field">
                <span>Fleet pool (optional)</span>
                <select value={draft.pool_id} onChange={e => setDraft(d => (d ? { ...d, pool_id: e.target.value } : d))}>
                  <option value="">Global capacity (any pool)</option>
                  {pools.map(pool => (
                    <option key={pool.id} value={pool.id}>
                      {pool.name}{pool.status && pool.status !== 'active' ? ` (${pool.status})` : ''}
                    </option>
                  ))}
                </select>
              </label>
              <div className="pp-toolbar">
                <button type="button" className="btn btn-ghost btn-sm" disabled={!draftValid || Boolean(draftGeneratorsError) || busy} onClick={() => void checkAvailability()}>
                  Check availability
                </button>
                {availability && (
                  <span
                    className="pp-muted"
                    role={availabilityError ? 'alert' : 'status'}
                    aria-live={availabilityError ? undefined : 'polite'}
                  >
                    {availability}
                  </span>
                )}
              </div>
            </div>
            <div className="pp-modal-actions">
								<button type="button" className="btn btn-secondary" onClick={closeDraft} disabled={busy}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || Boolean(draftGeneratorsError) || busy} onClick={() => void submitDraft()}>
                {busy ? 'Reserving…' : 'Reserve'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {bindFor && (
        <DialogLayer><div
          className="pp-modal-overlay"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) closeBind()
          }}
        >
          <div ref={bindDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={bindTitleId}>
            <div className="pp-modal-header">
              <h3 id={bindTitleId}>Bind a test to this reservation</h3>
								<button type="button" className="btn btn-ghost btn-sm" onClick={closeBind} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Test id</span>
                <input ref={bindTestInputRef} value={bindFor.testId} onChange={e => setBindFor(b => (b ? { ...b, testId: e.target.value } : b))} placeholder="test id to bind" />
              </label>
            </div>
            <div className="pp-modal-actions">
								<button type="button" className="btn btn-secondary" onClick={closeBind} disabled={busy}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={busy || !bindFor.testId.trim()} onClick={() => void submitBind()}>
                {busy ? 'Binding…' : 'Bind'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

    </div>
  )
}
