import { useCallback, useEffect, useMemo, useState } from 'react'

import { useCapacityApi } from '../hooks/api/useCapacityApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { CapacityTimeslot, FleetPool } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import './parityPanels.css'

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

function fmt(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

// datetime-local value (no tz) -> RFC3339 in the browser's local zone.
function toISO(local: string): string {
  if (!local) return ''
  const date = new Date(local)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

type Draft = {
  scenario_id: string
  start_local: string
  end_local: string
  timezone: string
  generators: string
  vusers: string
  pool_id: string
}

function emptyDraft(): Draft {
  let tz = 'UTC'
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    tz = 'UTC'
  }
  return { scenario_id: '', start_local: '', end_local: '', timezone: tz, generators: '', vusers: '', pool_id: '' }
}

export default function TimeslotsPanel({ projectId, canManage = false }: Props) {
  const api = useCapacityApi()
  const fleetApi = useFleetApi()
  const [timeslots, setTimeslots] = useState<CapacityTimeslot[]>([])
  const [pools, setPools] = useState<FleetPool[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [availability, setAvailability] = useState<string | null>(null)
  const [bindFor, setBindFor] = useState<{ slot: CapacityTimeslot; testId: string } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CapacityTimeslot | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!projectId) {
      setTimeslots([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await api.listTimeslots({ project_id: projectId })
      setTimeslots(res.timeslots || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load timeslots.')
      setTimeslots([])
    } finally {
      setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => {
    void load()
  }, [load])

  // Fleet pools power the optional pool-scoping selector (reserve within one pool's
  // capacity envelope instead of the global pool).
  useEffect(() => {
    let cancelled = false
    fleetApi.listFleetPools().then(res => {
      if (!cancelled) setPools(res.fleet_pools || [])
    }).catch(() => {
      if (!cancelled) setPools([])
    })
    return () => { cancelled = true }
  }, [fleetApi])

  const poolName = useCallback((id?: string) => {
    if (!id) return ''
    return pools.find(p => p.id === id)?.name || id
  }, [pools])

  const draftValid = useMemo(
    () => Boolean(draft && draft.start_local && draft.end_local && new Date(draft.start_local) < new Date(draft.end_local)),
    [draft],
  )
  const reservationActionDisabled = !canManage || !projectId
  const reservationActionTitle = !canManage
    ? 'Admin access required to create reservations'
    : projectId ? undefined : 'Select a project'

  const checkAvailability = useCallback(async () => {
    if (!draft || !projectId || !draftValid) return
    setBusy(true)
    setAvailability(null)
    try {
      const res = await api.checkTimeslotAvailability({ project_id: projectId, start_at: toISO(draft.start_local), end_at: toISO(draft.end_local) })
      setAvailability(res.available ? 'Window is available — no conflicting reservations.' : res.summary || `${res.count} conflicting reservation(s) in this window.`)
    } catch (err) {
      setAvailability(err instanceof ApiError ? err.message : 'Availability check failed.')
    } finally {
      setBusy(false)
    }
  }, [api, draft, draftValid, projectId])

  const submitDraft = useCallback(async () => {
    if (!draft || !projectId) return
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
      await api.createTimeslot({
        project_id: projectId,
        scenario_id: draft.scenario_id.trim() || undefined,
        start_at: toISO(draft.start_local),
        end_at: toISO(draft.end_local),
        timezone: draft.timezone.trim() || undefined,
        requested_capacity: requested,
      })
      setDraft(null)
      setAvailability(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create timeslot.')
    } finally {
      setBusy(false)
    }
  }, [api, draft, load, projectId])

  const act = useCallback(
    async (fn: () => Promise<unknown>, failMsg: string) => {
      setBusy(true)
      setError(null)
      try {
        await fn()
        await load()
      } catch (err) {
        setError(err instanceof ApiError ? err.message : failMsg)
      } finally {
        setBusy(false)
      }
    },
    [load],
  )

  const approveSlot = useCallback(async (slot: CapacityTimeslot) => {
    setBusy(true)
    setError(null)
    try {
      await api.approveTimeslot(slot.id)
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.message.includes('conflict_override')) {
        const reason = window.prompt('This reservation overlaps existing capacity. Enter an override reason to approve anyway:')
        if (reason?.trim()) {
          try {
            await api.approveTimeslot(slot.id, { conflict_override: true, reason: reason.trim() })
            await load()
            return
          } catch (overrideErr) {
            setError(overrideErr instanceof ApiError ? overrideErr.message : 'Failed to approve with override.')
            return
          }
        }
      }
      setError(err instanceof ApiError ? err.message : 'Failed to approve.')
    } finally {
      setBusy(false)
    }
  }, [api, load])

  const submitBind = useCallback(async () => {
    if (!bindFor || !bindFor.testId.trim()) return
    await act(() => api.bindTimeslot(bindFor.slot.id, bindFor.testId.trim()), 'Failed to bind timeslot.')
    setBindFor(null)
  }, [act, api, bindFor])

  return (
    <div className="pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {timeslots.length} reservation{timeslots.length === 1 ? '' : 's'} — forward-booked capacity windows with approval.
        </span>
        <button
          type="button"
          className="btn btn-primary btn-sm"
          disabled={reservationActionDisabled}
          title={reservationActionTitle}
          onClick={() => { setDraft(emptyDraft()); setAvailability(null) }}
        >
          New Reservation
        </button>
      </div>

      {error && <div className="pp-error">{error}</div>}

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
                  {fmt(slot.start_at)} → {fmt(slot.end_at)}
                  <span className={approvalPill(slot.approval_state)}>{slot.approval_state}</span>
                  {slot.bound_test_id ? <span className="pp-pill pp-pill-info">bound</span> : null}
                </div>
                <div className="pp-row-meta">
                  {slot.timezone ? <span>{slot.timezone}</span> : null}
                  {slot.scenario_id ? <span>scenario: {slot.scenario_id}</span> : null}
                  {slot.requested_capacity?.generators ? <span>{slot.requested_capacity.generators} gen</span> : null}
                  {slot.requested_capacity?.pool_id ? <span className="pp-pill pp-pill-muted">pool: {poolName(slot.requested_capacity.pool_id)}</span> : null}
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
                      <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void act(() => api.rejectTimeslot(slot.id), 'Failed to reject.')}>
                        Reject
                      </button>
                    </>
                  )}
                  {slot.approval_state === 'approved' && !slot.bound_test_id && (
                    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setBindFor({ slot, testId: '' })}>
                      Bind test
                    </button>
                  )}
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(slot)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {draft && (
        <div className="pp-modal-overlay" onClick={() => setDraft(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>New Capacity Reservation</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setDraft(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Start</span>
                  <input type="datetime-local" value={draft.start_local} onChange={e => { setDraft(d => (d ? { ...d, start_local: e.target.value } : d)); setAvailability(null) }} />
                </label>
                <label className="pp-field">
                  <span>End</span>
                  <input type="datetime-local" value={draft.end_local} onChange={e => { setDraft(d => (d ? { ...d, end_local: e.target.value } : d)); setAvailability(null) }} />
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Timezone (IANA)</span>
                  <input value={draft.timezone} onChange={e => setDraft(d => (d ? { ...d, timezone: e.target.value } : d))} placeholder="e.g. UTC" />
                </label>
                <label className="pp-field">
                  <span>Scenario id (optional)</span>
                  <input value={draft.scenario_id} onChange={e => setDraft(d => (d ? { ...d, scenario_id: e.target.value } : d))} placeholder="optional" />
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Generators (optional)</span>
                  <input type="number" min={0} value={draft.generators} onChange={e => setDraft(d => (d ? { ...d, generators: e.target.value } : d))} />
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
                <button type="button" className="btn btn-ghost btn-sm" disabled={!draftValid || busy} onClick={() => void checkAvailability()}>
                  Check availability
                </button>
                {availability && <span className="pp-muted">{availability}</span>}
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setDraft(null)}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                {busy ? 'Reserving…' : 'Reserve'}
              </button>
            </div>
          </div>
        </div>
      )}

      {bindFor && (
        <div className="pp-modal-overlay" onClick={() => setBindFor(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Bind a test to this reservation</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setBindFor(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Test id</span>
                <input value={bindFor.testId} autoFocus onChange={e => setBindFor(b => (b ? { ...b, testId: e.target.value } : b))} placeholder="test id to bind" />
              </label>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setBindFor(null)}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={busy || !bindFor.testId.trim()} onClick={() => void submitBind()}>
                {busy ? 'Binding…' : 'Bind'}
              </button>
            </div>
          </div>
        </div>
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete capacity reservation"
          description={`This permanently deletes the reservation for ${fmt(deleteTarget.start_at)} → ${fmt(deleteTarget.end_at)}.`}
          target={deleteTarget.id}
          targetLabel="Reservation id"
          confirmLabel="Delete reservation"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void act(() => api.deleteTimeslot(deleteTarget.id), 'Failed to delete.').then(() => setDeleteTarget(null))}
        />
      )}
    </div>
  )
}
