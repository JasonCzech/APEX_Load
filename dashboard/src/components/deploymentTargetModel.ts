import { ApiError } from '../hooks/apiClient'
import type { ClusterConnection, ClusterKind } from '../types/cluster'
import type {
  ConflictingFleetPool,
  DeploymentProvider,
  DomainDeploymentTarget,
  DomainDeploymentTargetInput,
} from '../types/deploymentTarget'
import type { FleetPool } from '../types/fleet'

/**
 * The single place the deployment-provider taxonomy lives on the frontend.
 *
 * Both the domain-target editor and the fleet-pool form import from here so a
 * cluster kind can never map to two different providers depending on which
 * screen the operator happens to be on.
 */

export const DEPLOYMENT_PROVIDER_LABELS: Record<DeploymentProvider, string> = {
  kubernetes: 'Kubernetes',
  azure_aks: 'Azure Kubernetes Service',
  aws_eks: 'Amazon EKS',
}

export const SELECTABLE_DEPLOYMENT_PROVIDERS: DeploymentProvider[] = [
  'kubernetes',
  'azure_aks',
  'aws_eks',
]

/** Mirrors storage.ProviderForClusterKind. Returns null for an unknown kind so
 *  callers fail closed rather than treating it as unconstrained. */
export function providerForClusterKind(kind: ClusterKind | string): DeploymentProvider | null {
  switch (kind) {
    case 'in_cluster':
    case 'generic':
      return 'kubernetes'
    case 'aks':
      return 'azure_aks'
    case 'eks':
      return 'aws_eks'
    default:
      return null
  }
}

export function providerLabel(provider: string | undefined | null): string {
  if (!provider) return 'Unconstrained'
  return DEPLOYMENT_PROVIDER_LABELS[provider as DeploymentProvider] ?? provider
}

/** The clusters an administrator may bind to a pool under this provider. */
export function clustersForProvider(
  clusters: ClusterConnection[],
  provider: DeploymentProvider | undefined | null,
): ClusterConnection[] {
  if (!provider) return clusters
  return clusters.filter((cluster) => providerForClusterKind(cluster.kind) === provider)
}

/**
 * Pools that a proposed provider would invalidate. This mirrors the server's
 * refusal so the operator sees the problem before pressing Save, but the server
 * check remains authoritative.
 */
export function conflictingPools(
  pools: FleetPool[],
  clusters: ClusterConnection[],
  provider: DeploymentProvider,
): ConflictingFleetPool[] {
  const byId = new Map(clusters.map((cluster) => [cluster.id, cluster]))
  const conflicts: ConflictingFleetPool[] = []
  for (const pool of pools) {
    const clusterId = (pool.cluster_id ?? '').trim()
    if (clusterId === '') {
      // An unbound pool conflicts with ANY constraint: it would scale in the
      // controller's own cluster.
      conflicts.push({
        pool_id: pool.id,
        pool_name: pool.name,
        reason: 'the pool is not bound to a cluster connection',
      })
      continue
    }
    const cluster = byId.get(clusterId)
    if (!cluster) {
      conflicts.push({
        pool_id: pool.id,
        pool_name: pool.name,
        cluster_id: clusterId,
        reason: "the pool's cluster connection could not be loaded",
      })
      continue
    }
    const poolProvider = providerForClusterKind(cluster.kind)
    if (poolProvider !== provider) {
      conflicts.push({
        pool_id: pool.id,
        pool_name: pool.name,
        cluster_id: clusterId,
        cluster_name: cluster.name,
        provider: poolProvider ?? cluster.kind,
        reason: `the pool is bound to a ${providerLabel(poolProvider ?? cluster.kind)} cluster`,
      })
    }
  }
  return conflicts
}

export interface DeploymentTargetDraft {
  domainId: string
  provider: DeploymentProvider | ''
  defaultClusterId: string
  updatedAt: string
}

export function deploymentTargetDraftFrom(target: DomainDeploymentTarget): DeploymentTargetDraft {
  return {
    domainId: target.domain_id,
    provider: target.configured ? (target.provider ?? '') : '',
    defaultClusterId: target.default_cluster_id ?? '',
    // Omitted for an unconfigured domain: the server requires a zero token on a
    // first write so a stale client cannot clobber a target someone else made.
    updatedAt: target.configured ? (target.updated_at ?? '') : '',
  }
}

export function deploymentTargetDraftError(
  draft: DeploymentTargetDraft,
  clusters: ClusterConnection[],
): string | null {
  if (draft.provider === '') return 'Choose a deployment provider.'
  if (!SELECTABLE_DEPLOYMENT_PROVIDERS.includes(draft.provider)) {
    return 'That deployment provider is not selectable.'
  }
  const clusterId = draft.defaultClusterId.trim()
  if (clusterId !== '') {
    const cluster = clusters.find((candidate) => candidate.id === clusterId)
    if (!cluster) return 'The selected default cluster no longer exists.'
    // A default that contradicts the provider would hand every new pool an
    // immediately-invalid binding.
    if (providerForClusterKind(cluster.kind) !== draft.provider) {
      return `The default cluster is ${providerLabel(providerForClusterKind(cluster.kind))}, which does not match the selected provider.`
    }
  }
  return null
}

export function deploymentTargetPayload(draft: DeploymentTargetDraft): DomainDeploymentTargetInput {
  const payload: DomainDeploymentTargetInput = {
    provider: draft.provider as DeploymentProvider,
    default_cluster_id: draft.defaultClusterId.trim(),
  }
  if (draft.updatedAt !== '') payload.updated_at = draft.updatedAt
  return payload
}

/**
 * The cloud_provider a pool should carry. It is derived rather than free text
 * once a domain constrains the pool, which is what stops the field from being
 * decorative.
 */
export function cloudProviderForPool(
  domainProvider: DeploymentProvider | undefined | null,
  fallback: string,
): string {
  return domainProvider ?? fallback
}

/**
 * Extracts the conflicting pools the server reported on a 409.
 *
 * writeJSONError sends `{error, detail}`, and apiClient stores that WHOLE body
 * on ApiError.payload — so the conflicts live at `payload.detail`, not at a
 * top-level `detail` the error object never carries. Reading the wrong one
 * yields undefined silently and the conflict list simply never renders.
 */
export function conflictingPoolsFromError(error: unknown): ConflictingFleetPool[] {
  if (!(error instanceof ApiError)) return []
  const payload = error.payload as { detail?: { conflicting_pools?: ConflictingFleetPool[] } } | undefined
  return payload?.detail?.conflicting_pools ?? []
}
