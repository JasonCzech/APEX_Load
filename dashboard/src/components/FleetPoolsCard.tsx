import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useFleetApi } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { FleetGeneratorMetadata, FleetPool, FleetProvisioningRequest, ProvisionerInfo } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import { listAllFleetPools, listAllGeneratorMetadata, runtimePoolMemberCount } from './fleetCatalogModel'
import './parityPanels.css'

interface Props {
  canManage?: boolean
}

function poolStatusPill(status: string): string {
  switch (status) {
    case 'active':
      return 'pp-pill pp-pill-success'
    case 'suspended':
      return 'pp-pill pp-pill-warning'
    case 'error':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

function provisionStatusPill(status: string): string {
  switch (status) {
    case 'success':
      return 'pp-pill pp-pill-success'
    case 'failed':
    case 'timed_out':
      return 'pp-pill pp-pill-danger'
    case 'cancelled':
      return 'pp-pill pp-pill-warning'
    case 'accepted':
    case 'scaling':
    case 'waiting_ready':
    case 'provisioning':
      return 'pp-pill pp-pill-info'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

function canCancelProvisionRequest(status: string): boolean {
  return ['', 'pending', 'accepted', 'scaling', 'waiting_ready', 'provisioning'].includes(status.trim().toLowerCase())
}

function canRetryProvisionRequest(status: string): boolean {
  return ['failed', 'not_configured', 'cancelled', 'timed_out'].includes(status.trim().toLowerCase())
}

function latestProvisionEvent(request: FleetProvisioningRequest) {
  const events = request.events || []
  return events.length > 0 ? events[events.length - 1] : null
}

type PoolDraft = {
  id?: string
  name: string
  cloud_provider: string
  region: string
  min_size: number
  max_size: number
  desired_size: number
  status: string
  baseline: string
}

function emptyDraft(): PoolDraft {
  const draft = { name: '', cloud_provider: 'kubernetes', region: '', min_size: 0, max_size: 0, desired_size: 0, status: 'active' }
  return { ...draft, baseline: JSON.stringify(draft) }
}

function poolDraftSnapshot(draft: Omit<PoolDraft, 'baseline'>): string {
  return JSON.stringify({
    id: draft.id,
    name: draft.name,
    cloud_provider: draft.cloud_provider,
    region: draft.region,
    min_size: draft.min_size,
    max_size: draft.max_size,
    desired_size: draft.desired_size,
    status: draft.status,
  })
}

function poolSizingError(draft: PoolDraft | null): string | null {
  if (!draft) return null
  if ([draft.min_size, draft.max_size, draft.desired_size].some(value => !Number.isInteger(value) || value < 0)) {
    return 'Min, desired, and max size must be nonnegative whole numbers.'
  }
  if (draft.max_size > 0 && draft.min_size > draft.max_size) return 'Min size cannot exceed max size.'
  if (draft.desired_size < draft.min_size) return 'Desired size cannot be lower than min size.'
  if (draft.max_size > 0 && draft.desired_size > draft.max_size) return 'Desired size cannot exceed max size.'
  return null
}

function provisionCountError(pool: FleetPool, count: number, memberCount: number): string | null {
  if (!Number.isInteger(count) || count <= 0) {
    return 'Desired generator count must be a positive whole number.'
  }
  const minimum = Math.max(1, pool.min_size ?? 0)
  if (count < minimum) {
    return `Desired generator count cannot be lower than this pool's minimum of ${minimum}.`
  }
  const maximum = pool.max_size ?? 0
  if (maximum > 0 && count > maximum) {
    return `Desired generator count cannot exceed this pool's maximum of ${maximum}.`
  }
  if (memberCount < count) {
    return `This pool has ${memberCount} associated runtime target${memberCount === 1 ? '' : 's'}. Add generator metadata with a target and this pool before requesting ${count} replicas.`
  }
  return null
}

function snapshotCaughtUp(
  listed: { updated_at?: string },
  committed: { updated_at?: string },
): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  return Number.isFinite(listedTimestamp)
    && Number.isFinite(committedTimestamp)
    && listedTimestamp > committedTimestamp
}

export default function FleetPoolsCard({ canManage = false }: Props) {
  const api = useFleetApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [pools, setPools] = useState<FleetPool[]>([])
  const [generatorMetadata, setGeneratorMetadata] = useState<FleetGeneratorMetadata[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<PoolDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FleetPool | null>(null)
  const [provisionFor, setProvisionFor] = useState<{ pool: FleetPool; count: number; baselineCount: number } | null>(null)
  const [provisionResult, setProvisionResult] = useState<FleetProvisioningRequest | null>(null)
  const [provisionRequests, setProvisionRequests] = useState<FleetProvisioningRequest[]>([])
  const [provisioner, setProvisioner] = useState<ProvisionerInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const [loadError, setLoadError] = useState(false)
  const loadRequestRef = useRef(0)
  const loadInFlightRef = useRef<Promise<boolean> | null>(null)
  const actionInFlightRef = useRef(false)
  const committedPoolsRef = useRef(new Map<string, FleetPool>())
  const deletedPoolIdsRef = useRef(new Set<string>())
  const committedProvisionRequestsRef = useRef(new Map<string, FleetProvisioningRequest>())
  const draftDialogRef = useRef<HTMLDivElement | null>(null)
  const draftNameRef = useRef<HTMLInputElement | null>(null)
  const provisionDialogRef = useRef<HTMLDivElement | null>(null)
  const provisionCountRef = useRef<HTMLInputElement | null>(null)
  const resultDialogRef = useRef<HTMLDivElement | null>(null)
  const resultDoneRef = useRef<HTMLButtonElement | null>(null)
  const draftTitleId = useId()
  const provisionTitleId = useId()
  const resultTitleId = useId()

  const discardDraft = useCallback(() => {
    if (!actionInFlightRef.current) setDraft(null)
  }, [])
  const closeDraft = useCallback(() => {
    if (!actionInFlightRef.current) runGuarded(discardDraft)
  }, [discardDraft, runGuarded])
  const discardProvision = useCallback(() => {
    if (!actionInFlightRef.current) setProvisionFor(null)
  }, [])
  const closeProvision = useCallback(() => {
    if (!actionInFlightRef.current) runGuarded(discardProvision)
  }, [discardProvision, runGuarded])
  const closeResult = useCallback(() => {
    if (!actionInFlightRef.current) setProvisionResult(null)
  }, [])
  useModalDialog({
    dialogRef: draftDialogRef,
    enabled: Boolean(draft),
    initialFocusRef: draftNameRef,
    onClose: busy ? undefined : closeDraft,
  })
  useModalDialog({
    dialogRef: provisionDialogRef,
    enabled: Boolean(provisionFor),
    initialFocusRef: provisionCountRef,
    onClose: busy ? undefined : closeProvision,
  })
  useModalDialog({
    dialogRef: resultDialogRef,
    enabled: Boolean(provisionResult),
    initialFocusRef: resultDoneRef,
    onClose: busy ? undefined : closeResult,
  })

  const performLoad = useCallback(async (requestId: number) => {
    const requestIsCurrent = () => loadRequestRef.current === requestId
    setLoading(true)
    setError(null)
    setLoadError(false)
    const [poolResult, metadataResult, provisionerResult, requestResult] = await Promise.allSettled([
      listAllFleetPools(api.listFleetPools),
      listAllGeneratorMetadata(api.listGeneratorMetadata),
      api.getProvisioner(),
      api.listProvisionRequests({ limit: 10 }),
    ])
    if (!requestIsCurrent()) return false

    const errors: string[] = []
    if (poolResult.status === 'fulfilled') {
      const listed = poolResult.value
      const listedIds = new Set(listed.map(pool => pool.id))
      deletedPoolIdsRef.current.forEach(id => {
        if (!listedIds.has(id)) deletedPoolIdsRef.current.delete(id)
      })
      const next = listed.filter(pool => !deletedPoolIdsRef.current.has(pool.id))
      committedPoolsRef.current.forEach((pool, id) => {
        if (deletedPoolIdsRef.current.has(id)) return
        const listedPool = listed.find(candidate => candidate.id === id)
        if (listedPool && snapshotCaughtUp(listedPool, pool)) {
          committedPoolsRef.current.delete(id)
          return
        }
        const index = next.findIndex(candidate => candidate.id === id)
        if (index >= 0) next[index] = pool
        else next.push(pool)
      })
      setPools(next.sort((left, right) => left.name.localeCompare(right.name)))
    } else {
      errors.push(poolResult.reason instanceof Error ? poolResult.reason.message : 'Failed to load fleet pools.')
    }
    if (metadataResult.status === 'fulfilled') {
      setGeneratorMetadata(metadataResult.value)
    } else {
      errors.push(metadataResult.reason instanceof Error ? metadataResult.reason.message : 'Failed to load generator membership metadata.')
    }
    if (provisionerResult.status === 'fulfilled') {
      setProvisioner(provisionerResult.value)
    } else {
      errors.push(provisionerResult.reason instanceof ApiError ? provisionerResult.reason.message : 'Failed to load fleet provisioner status.')
    }
    if (requestResult.status === 'fulfilled') {
      const listed = requestResult.value.provisioning_requests || []
      const next = [...listed]
      committedProvisionRequestsRef.current.forEach((request, id) => {
        const listedRequest = listed.find(candidate => candidate.id === id)
        if (listedRequest && snapshotCaughtUp(listedRequest, request)) {
          committedProvisionRequestsRef.current.delete(id)
          return
        }
        const index = next.findIndex(candidate => candidate.id === id)
        if (index >= 0) next[index] = request
        else next.push(request)
      })
      setProvisionRequests(next)
      setProvisionResult(current => current
        ? next.find(request => request.id === current.id) || current
        : current)
    } else {
      errors.push(requestResult.reason instanceof ApiError ? requestResult.reason.message : 'Failed to load provisioning history.')
    }
    setError(errors.length > 0 ? errors.join(' ') : null)
    setLoadError(errors.length > 0)
    setLoading(false)
    return errors.length === 0
  }, [api])

  const load = useCallback((): Promise<boolean> => {
    // Polling, mutation refreshes, and the manual button share one request.
    // A slow control plane must not let the next interval invalidate the only
    // response that can clear loading and advance provisioning status.
    if (loadInFlightRef.current) return loadInFlightRef.current
    const requestId = ++loadRequestRef.current
    const promise = performLoad(requestId)
    loadInFlightRef.current = promise
    const clearInFlight = () => {
      if (loadInFlightRef.current === promise) loadInFlightRef.current = null
    }
    void promise.then(clearInFlight, clearInFlight)
    return promise
  }, [performLoad])

  useEffect(() => () => {
    loadRequestRef.current += 1
    loadInFlightRef.current = null
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const hasActiveProvisionRequest = useMemo(
    () => provisionRequests.some(request => canCancelProvisionRequest(request.status)),
    [provisionRequests],
  )
  useEffect(() => {
    if (!hasActiveProvisionRequest) return
    const timer = window.setInterval(() => { void load() }, 5_000)
    return () => window.clearInterval(timer)
  }, [hasActiveProvisionRequest, load])

  const submitDraft = useCallback(async (): Promise<boolean> => {
    if (!draft || actionInFlightRef.current) return false
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    setLoadError(false)
    const body: Partial<FleetPool> = {
      name: draft.name.trim(),
      cloud_provider: draft.cloud_provider.trim(),
      region: draft.region.trim() || undefined,
      min_size: draft.min_size,
      max_size: draft.max_size,
      desired_size: draft.desired_size,
      status: draft.status,
    }
    try {
      const committed = draft.id
        ? await api.updateFleetPool(draft.id, body)
        : await api.createFleetPool(body)
      committedPoolsRef.current.set(committed.id, committed)
      deletedPoolIdsRef.current.delete(committed.id)
      setPools(current => [
        ...current.filter(pool => pool.id !== committed.id),
        committed,
      ].sort((left, right) => left.name.localeCompare(right.name)))
      setDraft(null)
      await load()
      return true
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save fleet pool.')
      return false
    } finally {
      actionInFlightRef.current = false
      setBusy(false)
    }
  }, [api, draft, load])

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget || actionInFlightRef.current) return
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    setLoadError(false)
    try {
      await api.deleteFleetPool(deleteTarget.id)
      deletedPoolIdsRef.current.add(deleteTarget.id)
      committedPoolsRef.current.delete(deleteTarget.id)
      setPools(current => current.filter(pool => pool.id !== deleteTarget.id))
      setDeleteTarget(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete fleet pool.')
    } finally {
      actionInFlightRef.current = false
      setBusy(false)
    }
  }, [api, deleteTarget, load])

  const submitProvision = useCallback(async (): Promise<boolean> => {
    if (!provisionFor || actionInFlightRef.current) return false
    const memberCount = runtimePoolMemberCount(provisionFor.pool, generatorMetadata)
    const validationError = provisionCountError(provisionFor.pool, provisionFor.count, memberCount)
    if (validationError) {
      setError(validationError)
      return false
    }
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    setLoadError(false)
    try {
      const res = await api.createProvisionRequest({ fleet_pool_id: provisionFor.pool.id, desired_count: provisionFor.count })
      committedProvisionRequestsRef.current.set(res.id, res)
      setProvisionRequests(current => [res, ...current.filter(request => request.id !== res.id)])
      setProvisionFor(null)
      setProvisionResult(res)
      await load()
      return true
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to request provisioning.')
      return false
    } finally {
      actionInFlightRef.current = false
      setBusy(false)
    }
  }, [api, generatorMetadata, provisionFor, load])

  const runProvisionOperation = useCallback(async (request: FleetProvisioningRequest, action: 'cancel' | 'retry') => {
    if (actionInFlightRef.current) return
    actionInFlightRef.current = true
    setBusy(true)
    setError(null)
    setLoadError(false)
    try {
      const result = action === 'cancel'
        ? await api.cancelProvisionRequest(request.id)
        : await api.retryProvisionRequest(request.id)
      setProvisionResult(result)
      committedProvisionRequestsRef.current.set(result.id, result)
      setProvisionRequests(current => current.map(candidate => candidate.id === result.id ? result : candidate))
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : `Failed to ${action} provisioning request.`)
    } finally {
      actionInFlightRef.current = false
      setBusy(false)
    }
  }, [api, load])

  const sizingError = poolSizingError(draft)
  const draftValid = Boolean(draft && draft.name.trim() && draft.cloud_provider.trim() && !sizingError)
  const provisionValidationError = provisionFor
    ? provisionCountError(
        provisionFor.pool,
        provisionFor.count,
        runtimePoolMemberCount(provisionFor.pool, generatorMetadata),
      )
    : null
  const modalDraftDirty = Boolean(
    (draft && poolDraftSnapshot(draft) !== draft.baseline)
    || (provisionFor && provisionFor.count !== provisionFor.baselineCount),
  )
  const modalDraftDirtyRef = useRef(false)
  modalDraftDirtyRef.current = modalDraftDirty
  const submitDraftRef = useRef(submitDraft)
  submitDraftRef.current = submitDraft
  const submitProvisionRef = useRef(submitProvision)
  submitProvisionRef.current = submitProvision
  const discardModalDraft = useCallback(() => {
    discardDraft()
    discardProvision()
  }, [discardDraft, discardProvision])
  const modalDraftGuard = useMemo<NavigationGuard>(() => ({
    id: 'fleet-pool-modal-draft',
    label: draft ? 'Fleet pool editor' : 'Fleet provisioning request',
    isDirty: () => modalDraftDirtyRef.current,
    save: () => draft ? submitDraftRef.current() : submitProvisionRef.current(),
    discard: discardModalDraft,
  }), [discardModalDraft, draft])
  useNavigationGuard(draft || provisionFor ? modalDraftGuard : null, modalDraftDirty)

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {pools.length} managed pool{pools.length === 1 ? '' : 's'}
          {provisioner ? (
            <span className={provisioner.can_provision ? 'pp-pill pp-pill-success' : 'pp-pill pp-pill-muted'} style={{ marginLeft: 8 }}>
              provisioner: {provisioner.name}
              {provisioner.can_provision ? ' (on-demand)' : ' (static)'}
            </span>
          ) : null}
        </span>
        {canManage && (
          <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={() => setDraft(emptyDraft())}>
            New Pool
          </button>
        )}
      </div>

      {error && (
        <div>
          <div className="pp-error" role="alert">{error}</div>
          {loadError && (
            <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
              Retry fleet data
            </button>
          )}
        </div>
      )}

      {loading ? (
        <div className="pp-empty">Loading pools…</div>
      ) : pools.length === 0 ? (
        <div className="pp-empty">No managed generator pools. Pools group load generators for cloud-backed scaling.</div>
      ) : (
        <div className="pp-list">
          {pools.map(pool => {
            const memberCount = runtimePoolMemberCount(pool, generatorMetadata)
            const initialProvisionCount = pool.desired_size || 1
            return (
            <div className="pp-row" key={pool.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {pool.name}
                  <span className={poolStatusPill(pool.status)}>{pool.status}</span>
                </div>
                <div className="pp-row-meta">
                  <span>{pool.cloud_provider}</span>
                  {pool.region ? <span>{pool.region}</span> : null}
                  <span>size {pool.desired_size ?? 0} ({pool.min_size ?? 0}–{pool.max_size ?? 0})</span>
                  <span>{memberCount} runtime member{memberCount === 1 ? '' : 's'}</span>
                  {memberCount < initialProvisionCount ? <span>associate {initialProvisionCount - memberCount} more target{initialProvisionCount - memberCount === 1 ? '' : 's'} before provisioning</span> : null}
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setProvisionFor({ pool, count: initialProvisionCount, baselineCount: initialProvisionCount })}>
                    Provision
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    disabled={busy}
                    onClick={() => {
                      const next = { id: pool.id, name: pool.name, cloud_provider: pool.cloud_provider, region: pool.region || '', min_size: pool.min_size ?? 0, max_size: pool.max_size ?? 0, desired_size: pool.desired_size ?? 0, status: pool.status }
                      setDraft({ ...next, baseline: poolDraftSnapshot(next) })
                    }}
                  >
                    Edit
                  </button>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(pool)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
            )
          })}
        </div>
      )}

      <div className="pp-section">
        <div className="pp-section-header">
          <span>Recent Provisioning</span>
          <span className="pp-muted">{provisionRequests.length} request{provisionRequests.length === 1 ? '' : 's'}</span>
        </div>
        {provisionRequests.length === 0 ? (
          <div className="pp-empty pp-empty-compact">No provisioning requests recorded yet.</div>
        ) : (
          <div className="pp-list">
            {provisionRequests.map(request => {
              const latest = latestProvisionEvent(request)
              return (
                <div className="pp-row" key={request.id}>
                  <div className="pp-row-main">
                    <div className="pp-row-title">
                      <span className="cell-mono">{request.id}</span>
                      <span className={provisionStatusPill(request.status)}>{request.status}</span>
                    </div>
                    <div className="pp-row-meta">
                      <span>pool {request.fleet_pool_id}</span>
                      <span>desired {request.desired_count}</span>
                      {request.project_id ? <span>project {request.project_id}</span> : null}
                      {latest ? <span>{latest.event_type}{latest.message ? `: ${latest.message}` : ''}</span> : null}
                    </div>
                  </div>
                  {canManage && (
                    <div className="pp-row-actions">
                      {canCancelProvisionRequest(request.status) && (
                        <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void runProvisionOperation(request, 'cancel')}>
                          Cancel
                        </button>
                      )}
                      {canRetryProvisionRequest(request.status) && (
                        <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void runProvisionOperation(request, 'retry')}>
                          Retry
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {draft && (
        <div
          className="pp-modal-overlay"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) closeDraft()
          }}
        >
          <div ref={draftDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={draftTitleId}>
            <div className="pp-modal-header">
              <h3 id={draftTitleId}>{draft.id ? 'Edit Pool' : 'New Pool'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeDraft} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input ref={draftNameRef} value={draft.name} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))} placeholder="e.g. east-http" />
                </label>
                <label className="pp-field">
                  <span>Cloud provider</span>
                  <select value={draft.cloud_provider} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, cloud_provider: e.target.value } : d))}>
                    <option value="kubernetes">kubernetes</option>
                    <option value="aws">aws</option>
                    <option value="azure">azure</option>
                    <option value="gcp">gcp</option>
                    <option value="static">static</option>
                  </select>
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Region</span>
                  <input value={draft.region} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, region: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field">
                  <span>Status</span>
                  <select value={draft.status} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, status: e.target.value } : d))}>
                    <option value="active">active</option>
                    <option value="suspended">suspended</option>
                    <option value="error">error</option>
                  </select>
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Min size</span>
                  <input type="number" min={0} step={1} disabled={busy} value={draft.min_size} onChange={e => setDraft(d => (d ? { ...d, min_size: Number(e.target.value) } : d))} />
                </label>
                <label className="pp-field">
                  <span>Desired</span>
                  <input type="number" min={0} step={1} disabled={busy} value={draft.desired_size} onChange={e => setDraft(d => (d ? { ...d, desired_size: Number(e.target.value) } : d))} />
                </label>
                <label className="pp-field">
                  <span>Max size</span>
                  <input type="number" min={0} step={1} disabled={busy} value={draft.max_size} onChange={e => setDraft(d => (d ? { ...d, max_size: Number(e.target.value) } : d))} />
                </label>
              </div>
              {sizingError ? <div className="pp-error" role="alert">{sizingError}</div> : null}
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={closeDraft} disabled={busy}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                {busy ? 'Saving…' : draft.id ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {provisionFor && (
        <div
          className="pp-modal-overlay"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) closeProvision()
          }}
        >
          <div ref={provisionDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={provisionTitleId}>
            <div className="pp-modal-header">
              <h3 id={provisionTitleId}>Provision “{provisionFor.pool.name}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeProvision} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Desired generator count</span>
                <input
                  ref={provisionCountRef}
                  type="number"
                  min={Math.max(1, provisionFor.pool.min_size ?? 0)}
                  max={(provisionFor.pool.max_size ?? 0) > 0 ? provisionFor.pool.max_size : undefined}
                  step={1}
                  disabled={busy}
                  value={provisionFor.count}
                  onChange={e => setProvisionFor(p => (p ? { ...p, count: Number(e.target.value) } : p))}
                />
              </label>
              {provisionValidationError ? <div className="pp-error" role="alert">{provisionValidationError}</div> : null}
              {provisioner?.can_provision ? (
                <p className="pp-muted">Scales the pool&rsquo;s generator workload via the <code>{provisioner.name}</code> provisioner. Provisioning only succeeds for runtime targets already associated with this pool.</p>
              ) : (
                <p className="pp-muted">Provisioning resolves to <code>not_configured</code> until a cloud/Kubernetes provider is wired.</p>
              )}
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={closeProvision} disabled={busy}>Cancel</button>
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy || Boolean(provisionValidationError)}
                onClick={() => void submitProvision()}
              >
                {busy ? 'Requesting…' : 'Request provisioning'}
              </button>
            </div>
          </div>
        </div>
      )}

      {provisionResult && (
        <div
          className="pp-modal-overlay"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) closeResult()
          }}
        >
          <div ref={resultDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={resultTitleId}>
            <div className="pp-modal-header">
              <h3 id={resultTitleId}>Provisioning request</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeResult} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-title">
                Status
                <span className={provisionStatusPill(provisionResult.status)}>{provisionResult.status}</span>
              </div>
              <div className="pp-row-meta"><span>desired: {provisionResult.desired_count}</span></div>
              {provisionResult.status === 'success' ? (
                <p className="pp-muted">The workload was scaled for targets already associated with this pool; scaling does not create pool membership.</p>
              ) : null}
              <div className="pp-list">
                {(provisionResult.events || []).map(ev => (
                  <div className="pp-row" key={ev.id}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">{ev.event_type}</div>
                      {ev.message ? <div className="pp-row-meta"><span>{ev.message}</span></div> : null}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <div className="pp-modal-actions">
              <button ref={resultDoneRef} type="button" className="btn btn-primary" onClick={closeResult} disabled={busy}>Done</button>
            </div>
          </div>
        </div>
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete fleet pool"
          description={`This permanently deletes the pool “${deleteTarget.name}” and detaches its members.`}
          target={deleteTarget.name}
          targetLabel="Pool"
          confirmLabel="Delete pool"
          error={error}
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
