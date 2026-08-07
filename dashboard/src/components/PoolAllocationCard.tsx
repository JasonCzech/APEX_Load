import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useClustersApi } from '../hooks/api/useClustersApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { FleetPool } from '../types'
import { ClusterCapacityResponse, ClusterConnection } from '../types/cluster'
import { getErrorMessage } from '../utils/errorUtils'
import { listAllFleetPools } from './fleetCatalogModel'
import './parityPanels.css'
import {
  AllocationDraft,
  HeadroomDimension,
  allocationBaseline,
  allocationError,
  allocationFromTransport,
  allocationToTransport,
  formatCpuMillis,
  formatMemoryBytes,
  normalizeCpuQuantity,
  normalizeMemoryQuantity,
  quotaHeadroom,
} from './poolAllocationModel'
import { headroomTone } from './resourceHeadroomModel'

interface Props {
  canManage?: boolean
}

const TOLERATION_EFFECTS = ['', 'NoSchedule', 'PreferNoSchedule', 'NoExecute']

let rowIdCounter = 0
const nextRowId = () => `alloc-row-${(rowIdCounter += 1)}`

/**
 * Host-resource allocation for a fleet pool: CPU/memory requests and limits
 * plus node placement, with the namespace quota projection shown BEFORE saving
 * so an operator sees a breach while they can still change it.
 */
export default function PoolAllocationCard({ canManage = false }: Props) {
  const clustersApi = useClustersApi()
  const fleetApi = useFleetApi()

  const [pools, setPools] = useState<FleetPool[]>([])
  const [clusters, setClusters] = useState<ClusterConnection[]>([])
  const [selectedPoolId, setSelectedPoolId] = useState('')
  const [capacity, setCapacity] = useState<ClusterCapacityResponse | null>(null)
  const [draft, setDraft] = useState<AllocationDraft | null>(null)
  const [baseline, setBaseline] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  const loadRequestRef = useRef(0)

  const load = useCallback(async () => {
    const requestId = (loadRequestRef.current += 1)
    setLoading(true)
    const [poolResult, clusterResult] = await Promise.allSettled([
      listAllFleetPools(fleetApi.listFleetPools),
      clustersApi.listClusters(),
    ])
    // A late response from a superseded load must not overwrite fresher data.
    if (requestId !== loadRequestRef.current) return

    const messages: string[] = []
    if (poolResult.status === 'fulfilled') setPools(poolResult.value)
    else messages.push(poolResult.reason instanceof ApiError ? poolResult.reason.message : 'Failed to load fleet pools.')
    if (clusterResult.status === 'fulfilled') setClusters(clusterResult.value.clusters ?? [])
    else messages.push('Failed to load cluster connections.')

    setError(messages.length > 0 ? messages.join(' ') : null)
    setLoadError(messages.length > 0)
    setLoading(false)
  }, [clustersApi, fleetApi])

  useEffect(() => {
    void load()
  }, [load])

  const selectedPool = useMemo(
    () => pools.find(pool => pool.id === selectedPoolId) ?? null,
    [pools, selectedPoolId],
  )

  // Load the pool's stored allocation and its cluster's live capacity together.
  useEffect(() => {
    if (!selectedPool) {
      setDraft(null)
      setCapacity(null)
      return
    }
    const next = allocationFromTransport(selectedPool.allocation, selectedPool.cluster_id ?? '', nextRowId)
    setDraft(next)
    setBaseline(allocationBaseline(next))
    setNotice(null)

    const clusterId = selectedPool.cluster_id
    if (!clusterId) {
      setCapacity(null)
      return
    }
    let cancelled = false
    void clustersApi
      .getClusterCapacity(clusterId)
      .then(result => {
        if (!cancelled) setCapacity(result)
      })
      .catch(() => {
        // Capacity is advisory here; the server enforces quota on save.
        if (!cancelled) setCapacity(null)
      })
    return () => {
      cancelled = true
    }
  }, [clustersApi, selectedPool])

  const draftError = draft ? allocationError(draft) : null
  const dirty = draft ? allocationBaseline(draft) !== baseline : false

  const updateDraft = useCallback((patch: Partial<AllocationDraft>) => {
    setDraft(current => (current ? { ...current, ...patch } : current))
    setNotice(null)
  }, [])

  const workload = capacity?.generator_workload
  const replicas = workload?.replicas ?? 0

  const headroom = useMemo(() => {
    if (!draft || !capacity) return { enforced: false as const }
    return quotaHeadroom(capacity.quotas ?? [], draft, replicas, replicas, workload?.current_requests)
  }, [capacity, draft, replicas, workload])

  // Which registered cluster this pool's allocation is applied to. The
  // allocation patches a StatefulSet in that cluster, so naming it removes any
  // doubt about where the change lands.
  const selectedClusterName = useMemo(() => {
    const clusterId = (selectedPool?.cluster_id ?? '').trim()
    if (clusterId === '') return ''
    return clusters.find(cluster => cluster.id === clusterId)?.name ?? ''
  }, [clusters, selectedPool])

  const nodePoolOptions = useMemo(
    () => Array.from(new Set((capacity?.node_capacity.node_pools ?? []).map(pool => pool.name).filter(Boolean))),
    [capacity],
  )
  const zoneOptions = useMemo(
    () => Array.from(new Set((capacity?.node_capacity.node_pools ?? []).map(pool => pool.zone ?? '').filter(Boolean))),
    [capacity],
  )

  const save = useCallback(async () => {
    if (!draft || !selectedPool || draftError) return
    setBusy(true)
    try {
      const response = await clustersApi.updatePoolAllocation(selectedPool.id, {
        ...allocationToTransport(draft),
        cluster_id: draft.clusterId || selectedPool.cluster_id,
        updated_at: selectedPool.updated_at,
      })
      setBaseline(allocationBaseline(draft))
      setError(null)
      const warnings = response.warnings ?? []
      setNotice(
        warnings.length > 0
          ? `Allocation applied. ${warnings.join(' ')}`
          : `Allocation applied to container “${response.container ?? 'generator'}”.`,
      )
      await load()
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to apply the allocation.'))
    } finally {
      setBusy(false)
    }
  }, [clustersApi, draft, draftError, load, selectedPool])

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">Host resource allocation</span>
      </div>

      {/* The fleet-pool and cluster-capacity reads behind this card are also
          system-admin only, so a non-admin sees the requirement, not a
          read-only view. */}
      {!canManage && <div className="pp-muted">Host allocation requires the manage_system permission.</div>}

      {error && (
        <div>
          <div className="pp-error" role="alert">{error}</div>
          {loadError && (
            <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
              Retry allocation data
            </button>
          )}
        </div>
      )}
      {notice && <div className="pp-muted" data-testid="allocation-notice">{notice}</div>}

      {loading ? (
        <div className="pp-empty">Loading pools…</div>
      ) : pools.length === 0 ? (
        <div className="pp-empty">No fleet pools yet. Create a pool and bind it to a cluster to allocate host resources.</div>
      ) : (
        <>
          <label className="pp-field">
            <span>Fleet pool</span>
            <select value={selectedPoolId} onChange={e => setSelectedPoolId(e.target.value)}>
              <option value="">Select a pool…</option>
              {pools.map(pool => (
                <option key={pool.id} value={pool.id}>{pool.name}</option>
              ))}
            </select>
          </label>

          {selectedPool && !selectedPool.cluster_id && (
            <div className="pp-empty">
              This pool is not bound to a cluster connection. Bind it to a registered cluster before allocating host resources.
            </div>
          )}

          {draft && selectedPool?.cluster_id && (
            <>
              {capacity?.node_capacity.available === false && (
                <div className="pp-muted">
                  Node capacity is unavailable ({capacity.node_capacity.unavailable_reason}); node pool and zone suggestions
                  are empty and pod-fit is not verified. Quota limits are still enforced.
                </div>
              )}

              <div className="pp-section">
                <div className="pp-section-header">
                  <span>Per-pod resources</span>
                  {selectedClusterName && (
                    <span className="pp-muted" data-testid="allocation-cluster-name">
                      {selectedClusterName}
                    </span>
                  )}
                </div>
                <div className="pp-field-row">
                  <label className="pp-field">
                    <span>CPU request</span>
                    <input
                      value={draft.cpuRequest}
                      placeholder="500m"
                      onChange={e => updateDraft({ cpuRequest: e.target.value })}
                      // Normalizing on blur only: a mid-typed "4G" must not
                      // rewrite itself under the caret.
                      onBlur={e => updateDraft({ cpuRequest: normalizeCpuQuantity(e.target.value) })}
                    />
                  </label>
                  <label className="pp-field">
                    <span>CPU limit</span>
                    <input
                      value={draft.cpuLimit}
                      placeholder="2"
                      onChange={e => updateDraft({ cpuLimit: e.target.value })}
                      onBlur={e => updateDraft({ cpuLimit: normalizeCpuQuantity(e.target.value) })}
                    />
                  </label>
                </div>
                <div className="pp-field-row">
                  <label className="pp-field">
                    <span>Memory request</span>
                    <input
                      value={draft.memoryRequest}
                      placeholder="512Mi"
                      onChange={e => updateDraft({ memoryRequest: e.target.value })}
                      onBlur={e => updateDraft({ memoryRequest: normalizeMemoryQuantity(e.target.value) })}
                    />
                  </label>
                  <label className="pp-field">
                    <span>Memory limit</span>
                    <input
                      value={draft.memoryLimit}
                      placeholder="4Gi"
                      onChange={e => updateDraft({ memoryLimit: e.target.value })}
                      onBlur={e => updateDraft({ memoryLimit: normalizeMemoryQuantity(e.target.value) })}
                    />
                  </label>
                </div>
              </div>

              <div className="pp-section">
                <div className="pp-section-header"><span>Node targeting</span></div>
                <div className="pp-field-row">
                  <label className="pp-field">
                    <span>Node pool</span>
                    <input
                      list="cluster-agentpools"
                      value={draft.nodePool}
                      onChange={e => {
                        const name = e.target.value
                        // Carry the label KEY the cluster reported for this
                        // pool. Writing the generic "agentpool" key against an
                        // EKS node group produces a selector that matches no
                        // node, leaving the generator pods Pending forever.
                        const match = (capacity?.node_capacity.node_pools ?? []).find(pool => pool.name === name)
                        updateDraft({ nodePool: name, nodePoolLabel: match?.label ?? '' })
                      }}
                    />
                    <datalist id="cluster-agentpools">
                      {nodePoolOptions.map(name => <option key={name} value={name} />)}
                    </datalist>
                  </label>
                  <label className="pp-field">
                    <span>Zone</span>
                    <input list="cluster-zones" value={draft.zone} onChange={e => updateDraft({ zone: e.target.value })} />
                    <datalist id="cluster-zones">
                      {zoneOptions.map(zone => <option key={zone} value={zone} />)}
                    </datalist>
                  </label>
                </div>

                <div className="pp-entry-list">
                  {draft.selectors.map((row, index) => (
                    <div className="pp-entry" key={row.id}>
                      <input
                        placeholder="key"
                        value={row.key}
                        onChange={e => {
                          const selectors = [...draft.selectors]
                          selectors[index] = { ...row, key: e.target.value }
                          updateDraft({ selectors })
                        }}
                      />
                      <input
                        placeholder="value"
                        value={row.value}
                        onChange={e => {
                          const selectors = [...draft.selectors]
                          selectors[index] = { ...row, value: e.target.value }
                          updateDraft({ selectors })
                        }}
                      />
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => updateDraft({ selectors: draft.selectors.filter(entry => entry.id !== row.id) })}
                      >
                        Remove
                      </button>
                    </div>
                  ))}
                </div>
                {canManage && (
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => updateDraft({ selectors: [...draft.selectors, { id: nextRowId(), key: '', value: '' }] })}
                  >
                    Add selector
                  </button>
                )}
              </div>

              <div className="pp-section">
                <div className="pp-section-header"><span>Tolerations</span></div>
                <div className="pp-entry-list">
                  {draft.tolerations.map((row, index) => (
                    <div className="pp-entry" key={row.id}>
                      <input
                        placeholder="key"
                        value={row.key}
                        onChange={e => {
                          const tolerations = [...draft.tolerations]
                          tolerations[index] = { ...row, key: e.target.value }
                          updateDraft({ tolerations })
                        }}
                      />
                      <select
                        value={row.operator}
                        onChange={e => {
                          const tolerations = [...draft.tolerations]
                          const operator = e.target.value as 'Equal' | 'Exists'
                          // Kubernetes forbids a value alongside Exists, so
                          // switching operators clears it.
                          tolerations[index] = { ...row, operator, value: operator === 'Exists' ? '' : row.value }
                          updateDraft({ tolerations })
                        }}
                      >
                        <option value="Equal">Equal</option>
                        <option value="Exists">Exists</option>
                      </select>
                      <input
                        placeholder="value"
                        value={row.value}
                        disabled={row.operator === 'Exists'}
                        onChange={e => {
                          const tolerations = [...draft.tolerations]
                          tolerations[index] = { ...row, value: e.target.value }
                          updateDraft({ tolerations })
                        }}
                      />
                      <select
                        value={row.effect}
                        onChange={e => {
                          const tolerations = [...draft.tolerations]
                          tolerations[index] = { ...row, effect: e.target.value }
                          updateDraft({ tolerations })
                        }}
                      >
                        {TOLERATION_EFFECTS.map(effect => (
                          <option key={effect || 'any'} value={effect}>{effect || '(any effect)'}</option>
                        ))}
                      </select>
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => updateDraft({ tolerations: draft.tolerations.filter(entry => entry.id !== row.id) })}
                      >
                        Remove
                      </button>
                    </div>
                  ))}
                </div>
                {canManage && (
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() =>
                      updateDraft({
                        tolerations: [...draft.tolerations, { id: nextRowId(), key: '', operator: 'Equal', value: '', effect: '' }],
                      })
                    }
                  >
                    Add toleration
                  </button>
                )}
              </div>

              {headroom.enforced && (
                <div className="pp-section">
                  <div className="pp-section-header">
                    <span>Namespace quota</span>
                    <span className="pp-muted">{replicas} generator pod{replicas === 1 ? '' : 's'}</span>
                  </div>
                  <div className="pp-stat-grid">
                    <HeadroomBar label="CPU" dimension={headroom.cpu} format={formatCpuMillis} />
                    <HeadroomBar label="Memory" dimension={headroom.memory} format={formatMemoryBytes} />
                    <HeadroomBar label="Pods" dimension={headroom.pods} format={value => String(value)} />
                  </div>
                </div>
              )}

              {draftError && <div className="pp-error" role="alert">{draftError}</div>}

              {canManage && (
                <div className="pp-modal-actions">
                  <button
                    type="button"
                    className="btn btn-primary btn-sm"
                    disabled={!!draftError || busy || !dirty}
                    onClick={() => { void save() }}
                  >
                    {busy ? 'Applying…' : 'Apply allocation'}
                  </button>
                </div>
              )}
            </>
          )}
        </>
      )}
    </div>
  )
}

/**
 * Renders current usage plus the projected usage after this change, so an
 * operator sees a quota breach before saving rather than as a 409.
 */
function HeadroomBar({
  label,
  dimension,
  format,
}: {
  label: string
  dimension?: HeadroomDimension
  format: (value: number) => string
}) {
  if (!dimension) return null
  const current = headroomTone(dimension.used, dimension.limit)
  const projected = headroomTone(dimension.projected, dimension.limit)
  return (
    <>
      <div className="pp-stat pp-stat-wide">
        <span className="pp-stat-label">{label} in use</span>
        <span className="pp-stat-value">{format(dimension.used)} / {dimension.limit ? format(dimension.limit) : '∞'}</span>
        <div className="pp-bar" aria-hidden="true">
          <div className={`pp-bar-fill ${current.cls}`} style={{ width: `${current.pct}%` }} />
        </div>
      </div>
      <div className="pp-stat pp-stat-wide">
        <span className="pp-stat-label">{label} after this change</span>
        <span className="pp-stat-value">{format(dimension.projected)} / {dimension.limit ? format(dimension.limit) : '∞'}</span>
        <div className="pp-bar" aria-hidden="true">
          <div className={`pp-bar-fill ${projected.cls}`} style={{ width: `${projected.pct}%` }} />
        </div>
      </div>
    </>
  )
}
