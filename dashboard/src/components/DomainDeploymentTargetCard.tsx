import { useCallback, useEffect, useMemo, useState } from 'react'

import { useClustersApi } from '../hooks/api/useClustersApi'
import { useDeploymentTargetsApi } from '../hooks/api/useDeploymentTargetsApi'
import { useFleetApi } from '../hooks/api/useFleetApi'
import type { ClusterConnection } from '../types/cluster'
import type {
  ConflictingFleetPool,
  DeploymentProvider,
  DomainDeploymentTarget,
} from '../types/deploymentTarget'
import type { FleetPool } from '../types/fleet'
import {
  DeploymentTargetDraft,
  SELECTABLE_DEPLOYMENT_PROVIDERS,
  clustersForProvider,
  conflictingPools,
  conflictingPoolsFromError,
  deploymentTargetDraftError,
  deploymentTargetDraftFrom,
  deploymentTargetPayload,
  providerLabel,
} from './deploymentTargetModel'

interface Props {
  domainId: string
  domainName?: string
  canManage?: boolean
}

/**
 * Superuser control for which cloud a domain's fleet pools deploy into.
 *
 * The target is both a default that new pools inherit and a hard constraint.
 * The server refuses a retarget that would strand existing pools; this card
 * surfaces the same conflicts up front so the operator is not surprised by a
 * 409 after filling the form in.
 */
export default function DomainDeploymentTargetCard({ domainId, domainName, canManage = false }: Props) {
  const api = useDeploymentTargetsApi()
  const clustersApi = useClustersApi()
  const fleetApi = useFleetApi()

  const [target, setTarget] = useState<DomainDeploymentTarget | null>(null)
  const [clusters, setClusters] = useState<ClusterConnection[]>([])
  const [pools, setPools] = useState<FleetPool[]>([])
  const [draft, setDraft] = useState<DeploymentTargetDraft | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [serverConflicts, setServerConflicts] = useState<ConflictingFleetPool[]>([])
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!domainId) return
    setError(null)
    try {
      const [loadedTarget, clusterList, poolList] = await Promise.all([
        api.getDeploymentTarget(domainId),
        clustersApi.listClusters(),
        fleetApi.listFleetPools(),
      ])
      setTarget(loadedTarget)
      setDraft(deploymentTargetDraftFrom(loadedTarget))
      setClusters(clusterList.clusters ?? [])
      setPools((poolList.fleet_pools ?? []).filter((pool) => (pool.domain_id ?? '') === domainId))
      setServerConflicts([])
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : 'Could not load the deployment target.')
    }
  }, [api, clustersApi, fleetApi, domainId])

  useEffect(() => {
    void load()
  }, [load])

  const provider = draft?.provider === '' ? null : (draft?.provider as DeploymentProvider | undefined)

  // Conflicts computed locally so Save can be disabled before the round trip.
  // The server check remains authoritative.
  const localConflicts = useMemo(
    () => (provider ? conflictingPools(pools, clusters, provider) : []),
    [pools, clusters, provider],
  )
  const conflicts = serverConflicts.length > 0 ? serverConflicts : localConflicts

  const eligibleClusters = useMemo(
    () => clustersForProvider(clusters, provider),
    [clusters, provider],
  )

  const validationError = draft ? deploymentTargetDraftError(draft, clusters) : null
  const canSave = canManage && !busy && draft !== null && !validationError && conflicts.length === 0

  const save = useCallback(async () => {
    if (!draft || !canSave) return
    setBusy(true)
    setError(null)
    setStatus(null)
    setServerConflicts([])
    try {
      const saved = await api.updateDeploymentTarget(domainId, deploymentTargetPayload(draft))
      setTarget(saved)
      setDraft(deploymentTargetDraftFrom(saved))
      setStatus(`This domain now deploys into ${providerLabel(saved.provider)}.`)
    } catch (saveError) {
      // The server's 409 body is {error, detail}, and apiClient puts the WHOLE
      // body on ApiError.payload — so the conflicts live at payload.detail,
      // not at a top-level `detail` the error object never carries.
      setServerConflicts(conflictingPoolsFromError(saveError))
      setError(saveError instanceof Error ? saveError.message : 'Could not save the deployment target.')
    } finally {
      setBusy(false)
    }
  }, [api, domainId, draft, canSave])

  const clear = useCallback(async () => {
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      const cleared = await api.clearDeploymentTarget(domainId)
      setTarget(cleared)
      setDraft(deploymentTargetDraftFrom(cleared))
      setServerConflicts([])
      setStatus('This domain is no longer constrained to one cloud.')
    } catch (clearError) {
      setError(clearError instanceof Error ? clearError.message : 'Could not clear the deployment target.')
    } finally {
      setBusy(false)
    }
  }, [api, domainId])

  if (!draft) {
    return (
      <div className="settings-subsection" data-testid="domain-deployment-target">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Deployment target</span>
            <span className="setting-desc">Loading…</span>
          </div>
        </div>
        {error && <div className="settings-error-inline" role="alert">{error}</div>}
      </div>
    )
  }

  return (
    <div className="settings-subsection" data-testid="domain-deployment-target">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">Deployment target</span>
          <span className="setting-desc">
            Which cloud {domainName || 'this domain'}&rsquo;s fleet pools deploy into. New pools inherit
            the default cluster; a pool bound to another cloud is refused.
          </span>
        </div>
        <strong>{target?.configured ? providerLabel(target.provider) : 'Unconstrained'}</strong>
      </div>

      <label className="pp-field">
        <span>Provider</span>
        <select
          value={draft.provider}
          disabled={!canManage || busy}
          onChange={(e) =>
            setDraft({
              ...draft,
              provider: e.target.value as DeploymentProvider | '',
              // A default cluster from the previous provider would be
              // immediately invalid, so drop it on a provider change.
              defaultClusterId: '',
            })
          }
        >
          <option value="">Select a provider…</option>
          {SELECTABLE_DEPLOYMENT_PROVIDERS.map((candidate) => (
            <option key={candidate} value={candidate}>
              {providerLabel(candidate)}
            </option>
          ))}
        </select>
      </label>

      <label className="pp-field">
        <span>Default cluster for new pools</span>
        <select
          value={draft.defaultClusterId}
          disabled={!canManage || busy || draft.provider === ''}
          onChange={(e) => setDraft({ ...draft, defaultClusterId: e.target.value })}
        >
          <option value="">No default — choose per pool</option>
          {eligibleClusters.map((cluster) => (
            <option key={cluster.id} value={cluster.id}>
              {cluster.name}
            </option>
          ))}
        </select>
      </label>

      <div className="pp-muted">
        {pools.length === 0
          ? 'No fleet pools are scoped to this domain yet.'
          : `${pools.length} fleet pool${pools.length === 1 ? '' : 's'} scoped to this domain.`}
      </div>

      {conflicts.length > 0 && (
        <div className="settings-inline-actions" role="alert">
          <span className="settings-error-inline">
            {conflicts.length} fleet pool{conflicts.length === 1 ? '' : 's'} would conflict with this
            provider. Rebind or remove them first:
          </span>
          <ul data-testid="deployment-target-conflicts">
            {conflicts.map((conflict) => (
              <li key={conflict.pool_id}>
                {conflict.pool_name} — {conflict.reason}
              </li>
            ))}
          </ul>
        </div>
      )}

      {validationError && <div className="settings-error-inline">{validationError}</div>}
      {error && <div className="settings-error-inline" role="alert">{error}</div>}
      {status && <div className="pp-muted">{status}</div>}

      <div className="settings-inline-actions">
        <button className="btn btn-primary btn-sm" type="button" disabled={!canSave} onClick={() => void save()}>
          Save deployment target
        </button>
        <button
          className="btn btn-secondary btn-sm"
          type="button"
          disabled={!canManage || busy || !target?.configured}
          onClick={() => void clear()}
        >
          Clear constraint
        </button>
      </div>
    </div>
  )
}
