import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useClustersApi } from '../hooks/api/useClustersApi'
import { useComputeApi } from '../hooks/api/useComputeApi'
import { useDeploymentTargetsApi } from '../hooks/api/useDeploymentTargetsApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { FleetGeneratorMetadata, FleetPool, FleetProvisioningInstance, FleetProvisioningRequest, ProvisionerInfo } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import type { ClusterConnection } from '../types/cluster'
import type { ExecutionTarget } from '../types/compute'
import type { DeploymentProvider, DomainDeploymentTarget } from '../types/deploymentTarget'
import { clustersForProvider, providerLabel } from './deploymentTargetModel'
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
  domain_id: string
  cluster_id: string
  execution_target_id: string
  region: string
  min_size: number
  max_size: number
  desired_size: number
  status: string
  lifecycle_mode: '' | 'standing' | 'on_demand' | 'warm_burst'
  warm_floor: number
  readiness_timeout_seconds: number
  idle_grace_seconds: number
  capacity_admission: 'strict' | 'best_effort'
  baseline: string
}

function emptyDraft(): PoolDraft {
  const draft = {
    name: '',
    cloud_provider: 'kubernetes',
    domain_id: '',
    cluster_id: '',
    execution_target_id: '',
    region: '',
    min_size: 0,
    max_size: 0,
    desired_size: 0,
    status: 'active',
    lifecycle_mode: '' as const,
    warm_floor: 0,
    readiness_timeout_seconds: 300,
    idle_grace_seconds: 300,
    capacity_admission: 'strict' as const,
  }
  return { ...draft, baseline: JSON.stringify(draft) }
}

function poolDraftSnapshot(draft: Omit<PoolDraft, 'baseline'>): string {
  return JSON.stringify({
    id: draft.id,
    name: draft.name,
    cloud_provider: draft.cloud_provider,
    domain_id: draft.domain_id,
    cluster_id: draft.cluster_id,
    execution_target_id: draft.execution_target_id,
    region: draft.region,
    min_size: draft.min_size,
    max_size: draft.max_size,
    desired_size: draft.desired_size,
    status: draft.status,
    lifecycle_mode: draft.lifecycle_mode,
    warm_floor: draft.warm_floor,
    readiness_timeout_seconds: draft.readiness_timeout_seconds,
    idle_grace_seconds: draft.idle_grace_seconds,
    capacity_admission: draft.capacity_admission,
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
  const canScaleToZero = Boolean(pool.execution_target_id)
    && ['azure_container_apps', 'aws_ecs_fargate'].includes(pool.cloud_provider)
  if (!Number.isInteger(count) || count < 0 || (count === 0 && !canScaleToZero)) {
    return canScaleToZero
      ? 'Desired generator count must be a nonnegative whole number.'
      : 'Desired generator count must be a positive whole number.'
	}
  const minimum = Math.max(canScaleToZero ? 0 : 1, pool.min_size ?? 0)
  if (count < minimum) {
    return `Desired generator count cannot be lower than this pool's minimum of ${minimum}.`
  }
  const maximum = pool.max_size ?? 0
  if (maximum > 0 && count > maximum) {
    return `Desired generator count cannot exceed this pool's maximum of ${maximum}.`
  }
  if (!pool.execution_target_id && memberCount < count) {
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
  const clustersApi = useClustersApi()
  const computeApi = useComputeApi()
  const deploymentTargetsApi = useDeploymentTargetsApi()
  const repositoryApi = useRepositoryApi()
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
  const [provisionInstances, setProvisionInstances] = useState<FleetProvisioningInstance[]>([])
  const [provisioner, setProvisioner] = useState<ProvisionerInfo | null>(null)
  const [clusters, setClusters] = useState<ClusterConnection[]>([])
  const [executionTargets, setExecutionTargets] = useState<ExecutionTarget[]>([])
  const [deploymentTargets, setDeploymentTargets] = useState<DomainDeploymentTarget[]>([])
  const [domains, setDomains] = useState<{ id: string; name: string }[]>([])
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

  useEffect(() => {
    const requestID = provisionResult?.id
    if (!requestID) {
      setProvisionInstances([])
      return
    }
    let active = true
    void api.listProvisionRequestInstances(requestID)
      .then(result => { if (active) setProvisionInstances(result.instances || []) })
      .catch(() => { if (active) setProvisionInstances([]) })
    return () => { active = false }
  }, [api, provisionResult?.id])
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

  // Cluster connections and domain targets change rarely and only by
  // administrator action, so they are loaded once rather than joining the
  // pool polling loop. A failure here is non-fatal: the selects simply fall
  // back to being unconstrained, and the server still enforces the rule.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [clusterList, executionTargetList, targetList, domainList] = await Promise.all([
          clustersApi.listClusters(),
          computeApi.listExecutionTargets(),
          deploymentTargetsApi.listDeploymentTargets(),
          repositoryApi.listDomains(),
        ])
        if (cancelled) return
        setClusters(clusterList.clusters ?? [])
        setExecutionTargets(executionTargetList.execution_targets ?? [])
        setDeploymentTargets(targetList.targets ?? [])
        setDomains((domainList.domains ?? []).map(domain => ({ id: domain.id, name: domain.name })))
      } catch {
        if (!cancelled) {
          setClusters([])
          setExecutionTargets([])
          setDeploymentTargets([])
          setDomains([])
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [clustersApi, computeApi, deploymentTargetsApi, repositoryApi])

  // The constraint that applies to the pool being edited, if any.
  const draftTarget = useMemo(() => {
    const domainId = (draft?.domain_id ?? '').trim()
    if (domainId === '') return null
    return deploymentTargets.find(target => target.domain_id === domainId && target.configured) ?? null
  }, [draft?.domain_id, deploymentTargets])

  const draftProvider = (draftTarget?.provider ?? null) as DeploymentProvider | null

  // Only clusters the domain permits may be bound, so an invalid choice is
  // impossible to make rather than merely rejected after Save.
  const eligibleClusters = useMemo(
    () => clustersForProvider(clusters, draftProvider),
    [clusters, draftProvider],
  )

  const eligibleExecutionTargets = useMemo(() => {
    const allowed = new Set(draftTarget?.allowed_execution_target_ids || [])
    return executionTargets.filter(target => {
      if (allowed.size > 0 && !allowed.has(target.id)) return false
      if (!draftProvider) return true
      if (draftProvider.startsWith('azure')) return target.kind === 'azure_container_apps' || target.kind === 'kubernetes'
      if (draftProvider.startsWith('aws')) return target.kind === 'aws_ecs_fargate' || target.kind === 'kubernetes'
      return target.kind === 'kubernetes'
    })
  }, [draftProvider, draftTarget?.allowed_execution_target_ids, executionTargets])

  // Every domain, not only the constrained ones. Listing just the domains that
  // have a target would (a) force an admin to configure a constraint before a
  // pool could be scoped at all, and (b) render a pool whose domain target was
  // later cleared as "Unscoped" while it is in fact still scoped.
  //
  // The pool's own domain is always included even if the domain list could not
  // be loaded, so the select can never misrepresent the stored binding.
  const domainOptions = useMemo(() => {
    const byId = new Map(domains.map(domain => [domain.id, domain.name]))
    for (const target of deploymentTargets) {
      if (!byId.has(target.domain_id)) byId.set(target.domain_id, target.domain_name || target.domain_id)
    }
    const current = (draft?.domain_id ?? '').trim()
    if (current !== '' && !byId.has(current)) byId.set(current, current)
    return Array.from(byId, ([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name))
  }, [domains, deploymentTargets, draft?.domain_id])

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
      domain_id: draft.domain_id.trim(),
      cluster_id: draft.cluster_id.trim(),
      execution_target_id: draft.execution_target_id.trim(),
      region: draft.region.trim() || undefined,
      min_size: draft.min_size,
      max_size: draft.max_size,
      desired_size: draft.desired_size,
      status: draft.status,
      lifecycle_mode: draft.lifecycle_mode || undefined,
      warm_floor: draft.warm_floor,
      readiness_timeout_seconds: draft.readiness_timeout_seconds,
      idle_grace_seconds: draft.idle_grace_seconds,
      capacity_admission: draft.capacity_admission,
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
                  {pool.lifecycle_mode ? <span>{pool.lifecycle_mode.replace('_', ' ')}</span> : null}
                  {pool.execution_target_id ? <span>target {pool.execution_target_id}</span> : null}
                  <span>size {pool.desired_size ?? 0} ({pool.min_size ?? 0}–{pool.max_size ?? 0})</span>
                  <span>{memberCount} runtime member{memberCount === 1 ? '' : 's'}</span>
                  {!pool.execution_target_id && memberCount < initialProvisionCount ? <span>associate {initialProvisionCount - memberCount} more target{initialProvisionCount - memberCount === 1 ? '' : 's'} before provisioning</span> : null}
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
                      const next = { id: pool.id, name: pool.name, cloud_provider: pool.cloud_provider, domain_id: pool.domain_id || '', cluster_id: pool.cluster_id || '', execution_target_id: pool.execution_target_id || '', region: pool.region || '', min_size: pool.min_size ?? 0, max_size: pool.max_size ?? 0, desired_size: pool.desired_size ?? 0, status: pool.status, lifecycle_mode: pool.lifecycle_mode || 'standing', warm_floor: pool.warm_floor ?? 0, readiness_timeout_seconds: pool.readiness_timeout_seconds ?? 300, idle_grace_seconds: pool.idle_grace_seconds ?? 300, capacity_admission: pool.capacity_admission || 'strict' }
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
                      <span>ready {request.ready_count ?? 0} / observed {request.observed_count ?? 0}</span>
                      {(request.pending_count ?? 0) > 0 ? <span>pending {request.pending_count}</span> : null}
                      {(request.draining_count ?? 0) > 0 ? <span>draining {request.draining_count}</span> : null}
                      {(request.failed_count ?? 0) > 0 ? <span>failed {request.failed_count}</span> : null}
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
                  {draftProvider ? (
                    // Derived, not chosen: once a domain constrains the pool the
                    // provider follows from the domain's deployment target, so
                    // the field stops being decorative free text.
                    <input value={providerLabel(draftProvider)} disabled readOnly data-testid="pool-derived-provider" />
                  ) : (
                    <select value={draft.cloud_provider} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, cloud_provider: e.target.value } : d))}>
                      <option value="kubernetes">kubernetes</option>
                      <option value="aws">aws</option>
                      <option value="azure">azure</option>
                      <option value="gcp">gcp</option>
                      <option value="static">static</option>
                    </select>
                  )}
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Domain</span>
                  <select
                    value={draft.domain_id}
                    disabled={busy}
                    data-testid="pool-domain"
                    onChange={e =>
                      setDraft(d => {
                        if (!d) return d
                        const domainId = e.target.value
                        const target = deploymentTargets.find(
                          candidate => candidate.domain_id === domainId && candidate.configured,
                        )
                        return {
                          ...d,
                          domain_id: domainId,
                          // Adopt the domain's default cluster, and drop a
                          // binding the new domain would refuse anyway.
                          cluster_id: target ? (target.default_cluster_id ?? '') : d.cluster_id,
                          execution_target_id: target ? (target.default_execution_target_id ?? '') : d.execution_target_id,
                          cloud_provider: target?.provider ?? d.cloud_provider,
                        }
                      })
                    }
                  >
                    <option value="">Unscoped (no domain constraint)</option>
                    {domainOptions.map(option => (
                      <option key={option.id} value={option.id}>
                        {option.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="pp-field">
                  <span>Execution target</span>
                  <select
                    value={draft.execution_target_id}
                    disabled={busy}
                    data-testid="pool-execution-target"
                    onChange={e => setDraft(d => (d ? { ...d, execution_target_id: e.target.value, cluster_id: e.target.value ? '' : d.cluster_id } : d))}
                  >
                    <option value="">Legacy cluster/controller placement</option>
                    {eligibleExecutionTargets.map(target => <option key={target.id} value={target.id}>{target.name} — {target.kind}</option>)}
                  </select>
                </label>
              </div>
              {!draft.execution_target_id ? <div className="pp-field-row">
                <label className="pp-field">
                  <span>Legacy cluster connection</span>
                  <select
                    value={draft.cluster_id}
                    disabled={busy}
                    data-testid="pool-cluster"
                    onChange={e => setDraft(d => (d ? { ...d, cluster_id: e.target.value } : d))}
                  >
                    <option value="">
                      {draftProvider ? 'Select a cluster…' : 'Controller default (env-configured)'}
                    </option>
                    {eligibleClusters.map(cluster => (
                      <option key={cluster.id} value={cluster.id}>
                        {cluster.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div> : null}
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
              <div className="pp-field-row">
                <label className="pp-field"><span>Capacity lifecycle</span><select value={draft.lifecycle_mode} disabled={busy} onChange={e => setDraft(d => d ? { ...d, lifecycle_mode: e.target.value as PoolDraft['lifecycle_mode'] } : d)}><option value="">Target default</option><option value="standing">Standing</option><option value="warm_burst">Warm floor + burst</option><option value="on_demand">On demand</option></select></label>
                <label className="pp-field"><span>Warm floor</span><input type="number" min={0} step={1} value={draft.warm_floor} disabled={busy || draft.lifecycle_mode === 'standing'} onChange={e => setDraft(d => d ? { ...d, warm_floor: Number(e.target.value) } : d)} /></label>
                <label className="pp-field"><span>Admission</span><select value={draft.capacity_admission} disabled={busy} onChange={e => setDraft(d => d ? { ...d, capacity_admission: e.target.value as PoolDraft['capacity_admission'] } : d)}><option value="strict">Require all requested capacity</option><option value="best_effort">Proceed with partial capacity</option></select></label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field"><span>Readiness timeout (seconds)</span><input type="number" min={1} step={1} value={draft.readiness_timeout_seconds} disabled={busy} onChange={e => setDraft(d => d ? { ...d, readiness_timeout_seconds: Number(e.target.value) } : d)} /></label>
                <label className="pp-field"><span>Idle grace (seconds)</span><input type="number" min={0} step={1} value={draft.idle_grace_seconds} disabled={busy} onChange={e => setDraft(d => d ? { ...d, idle_grace_seconds: Number(e.target.value) } : d)} /></label>
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
                  min={Boolean(provisionFor.pool.execution_target_id) && ['azure_container_apps', 'aws_ecs_fargate'].includes(provisionFor.pool.cloud_provider)
                    ? Math.max(0, provisionFor.pool.min_size ?? 0)
                    : Math.max(1, provisionFor.pool.min_size ?? 0)}
                  max={(provisionFor.pool.max_size ?? 0) > 0 ? provisionFor.pool.max_size : undefined}
                  step={1}
                  disabled={busy}
                  value={provisionFor.count}
                  onChange={e => setProvisionFor(p => (p ? { ...p, count: Number(e.target.value) } : p))}
                />
              </label>
              {provisionValidationError ? <div className="pp-error" role="alert">{provisionValidationError}</div> : null}
              {provisioner?.can_provision ? (
                <p className="pp-muted">Reconciles requested capacity through <code>{provisioner.name}</code>. PaaS targets launch registered templates asynchronously; Kubernetes retains its existing member-aware behavior.</p>
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
              {provisionInstances.length > 0 ? <div className="pp-section">
                <div className="pp-section-header"><span>Provider instances</span><span className="pp-muted">{provisionInstances.length}</span></div>
                <div className="pp-list">{provisionInstances.map(instance => <div className="pp-row" key={instance.id}>
                  <div className="pp-row-main"><div className="pp-row-title"><span className="cell-mono">{instance.generator_id}</span><span className={provisionStatusPill(instance.state)}>{instance.state}</span></div>
                    <div className="pp-row-meta">{instance.provider_id ? <span>{instance.provider_id}</span> : <span>provider identity pending</span>}<span>archive {instance.archive_state}</span></div>
                  </div>
                </div>)}</div>
              </div> : null}
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
