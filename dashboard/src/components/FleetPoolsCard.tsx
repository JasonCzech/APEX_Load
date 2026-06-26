import { useCallback, useEffect, useMemo, useState } from 'react'

import { useFleetApi } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { FleetPool, FleetProvisioningRequest, ProvisionerInfo } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
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
}

function emptyDraft(): PoolDraft {
  return { name: '', cloud_provider: 'kubernetes', region: '', min_size: 0, max_size: 0, desired_size: 0, status: 'active' }
}

export default function FleetPoolsCard({ canManage = false }: Props) {
  const api = useFleetApi()
  const [pools, setPools] = useState<FleetPool[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<PoolDraft | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FleetPool | null>(null)
  const [provisionFor, setProvisionFor] = useState<{ pool: FleetPool; count: number } | null>(null)
  const [provisionResult, setProvisionResult] = useState<FleetProvisioningRequest | null>(null)
  const [provisionRequests, setProvisionRequests] = useState<FleetProvisioningRequest[]>([])
  const [provisioner, setProvisioner] = useState<ProvisionerInfo | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [res, prov, requests] = await Promise.all([
        api.listFleetPools(),
        api.getProvisioner().catch(() => null),
        api.listProvisionRequests({ limit: 10 }).catch(() => ({ provisioning_requests: [], count: 0 })),
      ])
      setPools(res.fleet_pools || [])
      setProvisioner(prov)
      setProvisionRequests(requests.provisioning_requests || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load fleet pools.')
      setPools([])
      setProvisionRequests([])
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const submitDraft = useCallback(async () => {
    if (!draft) return
    setBusy(true)
    setError(null)
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
      if (draft.id) await api.updateFleetPool(draft.id, body)
      else await api.createFleetPool(body)
      setDraft(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save fleet pool.')
    } finally {
      setBusy(false)
    }
  }, [api, draft, load])

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteFleetPool(deleteTarget.id)
      setDeleteTarget(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete fleet pool.')
    } finally {
      setBusy(false)
    }
  }, [api, deleteTarget, load])

  const submitProvision = useCallback(async () => {
    if (!provisionFor) return
    setBusy(true)
    setError(null)
    try {
      const res = await api.createProvisionRequest({ fleet_pool_id: provisionFor.pool.id, desired_count: provisionFor.count })
      setProvisionFor(null)
      setProvisionResult(res)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to request provisioning.')
    } finally {
      setBusy(false)
    }
  }, [api, provisionFor, load])

  const runProvisionOperation = useCallback(async (request: FleetProvisioningRequest, action: 'cancel' | 'retry') => {
    setBusy(true)
    setError(null)
    try {
      const result = action === 'cancel'
        ? await api.cancelProvisionRequest(request.id)
        : await api.retryProvisionRequest(request.id)
      setProvisionResult(result)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : `Failed to ${action} provisioning request.`)
    } finally {
      setBusy(false)
    }
  }, [api, load])

  const draftValid = useMemo(
    () => Boolean(draft && draft.name.trim() && draft.cloud_provider.trim() && (draft.max_size === 0 || draft.min_size <= draft.max_size)),
    [draft],
  )

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
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setDraft(emptyDraft())}>
            New Pool
          </button>
        )}
      </div>

      {error && <div className="pp-error">{error}</div>}

      {loading ? (
        <div className="pp-empty">Loading pools…</div>
      ) : pools.length === 0 ? (
        <div className="pp-empty">No managed generator pools. Pools group load generators for cloud-backed scaling.</div>
      ) : (
        <div className="pp-list">
          {pools.map(pool => (
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
                  <span>{pool.members?.length ?? 0} member{(pool.members?.length ?? 0) === 1 ? '' : 's'}</span>
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setProvisionFor({ pool, count: pool.desired_size || 1 })}>
                    Provision
                  </button>
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setDraft({ id: pool.id, name: pool.name, cloud_provider: pool.cloud_provider, region: pool.region || '', min_size: pool.min_size ?? 0, max_size: pool.max_size ?? 0, desired_size: pool.desired_size ?? 0, status: pool.status })}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(pool)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
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
        <div className="pp-modal-overlay" onClick={() => setDraft(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>{draft.id ? 'Edit Pool' : 'New Pool'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setDraft(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input value={draft.name} autoFocus onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))} placeholder="e.g. east-http" />
                </label>
                <label className="pp-field">
                  <span>Cloud provider</span>
                  <select value={draft.cloud_provider} onChange={e => setDraft(d => (d ? { ...d, cloud_provider: e.target.value } : d))}>
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
                  <input value={draft.region} onChange={e => setDraft(d => (d ? { ...d, region: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field">
                  <span>Status</span>
                  <select value={draft.status} onChange={e => setDraft(d => (d ? { ...d, status: e.target.value } : d))}>
                    <option value="active">active</option>
                    <option value="suspended">suspended</option>
                    <option value="error">error</option>
                  </select>
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Min size</span>
                  <input type="number" min={0} value={draft.min_size} onChange={e => setDraft(d => (d ? { ...d, min_size: Number(e.target.value) } : d))} />
                </label>
                <label className="pp-field">
                  <span>Desired</span>
                  <input type="number" min={0} value={draft.desired_size} onChange={e => setDraft(d => (d ? { ...d, desired_size: Number(e.target.value) } : d))} />
                </label>
                <label className="pp-field">
                  <span>Max size</span>
                  <input type="number" min={0} value={draft.max_size} onChange={e => setDraft(d => (d ? { ...d, max_size: Number(e.target.value) } : d))} />
                </label>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setDraft(null)}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                {busy ? 'Saving…' : draft.id ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {provisionFor && (
        <div className="pp-modal-overlay" onClick={() => setProvisionFor(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Provision “{provisionFor.pool.name}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setProvisionFor(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Desired generator count</span>
                <input type="number" min={1} value={provisionFor.count} onChange={e => setProvisionFor(p => (p ? { ...p, count: Number(e.target.value) } : p))} />
              </label>
              {provisioner?.can_provision ? (
                <p className="pp-muted">Scales the pool&rsquo;s generator workload via the <code>{provisioner.name}</code> provisioner.</p>
              ) : (
                <p className="pp-muted">Provisioning resolves to <code>not_configured</code> until a cloud/Kubernetes provider is wired.</p>
              )}
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setProvisionFor(null)}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={busy || provisionFor.count < 1} onClick={() => void submitProvision()}>
                {busy ? 'Requesting…' : 'Request provisioning'}
              </button>
            </div>
          </div>
        </div>
      )}

      {provisionResult && (
        <div className="pp-modal-overlay" onClick={() => setProvisionResult(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Provisioning request</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setProvisionResult(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-title">
                Status
                <span className={provisionStatusPill(provisionResult.status)}>{provisionResult.status}</span>
              </div>
              <div className="pp-row-meta"><span>desired: {provisionResult.desired_count}</span></div>
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
              <button type="button" className="btn btn-primary" onClick={() => setProvisionResult(null)}>Done</button>
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
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
