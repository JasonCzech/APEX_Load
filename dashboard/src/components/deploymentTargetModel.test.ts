import { describe, expect, it } from 'vitest'

import type { ClusterConnection } from '../types/cluster'
import type { DomainDeploymentTarget } from '../types/deploymentTarget'
import type { FleetPool } from '../types/fleet'
import {
  SELECTABLE_DEPLOYMENT_PROVIDERS,
  clustersForProvider,
  conflictingPools,
  deploymentTargetDraftError,
  deploymentTargetDraftFrom,
  deploymentTargetPayload,
  providerForClusterKind,
  providerLabel,
} from './deploymentTargetModel'

function cluster(id: string, kind: ClusterConnection['kind']): ClusterConnection {
  return {
    id,
    name: `${id}-name`,
    kind,
    namespace: 'apex',
    tls_mode: 'system_roots',
    auth_mode: kind === 'eks' ? 'aws_iam' : kind === 'aks' ? 'azure_service_principal' : 'static_token',
    status: 'active',
    has_client_secret: false,
    credential_source: 'unset',
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T00:00:00Z',
  }
}

function pool(id: string, clusterId?: string): FleetPool {
  return {
    id,
    name: `${id}-name`,
    cloud_provider: 'kubernetes',
    status: 'active',
    cluster_id: clusterId,
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T00:00:00Z',
  } as FleetPool
}

describe('providerForClusterKind', () => {
  it('maps every supported cluster kind', () => {
    expect(providerForClusterKind('in_cluster')).toBe('kubernetes')
    expect(providerForClusterKind('generic')).toBe('kubernetes')
    expect(providerForClusterKind('aks')).toBe('azure_aks')
    expect(providerForClusterKind('eks')).toBe('aws_eks')
  })

  it('fails closed on an unknown kind rather than reading as unconstrained', () => {
    expect(providerForClusterKind('gke')).toBeNull()
    expect(providerForClusterKind('')).toBeNull()
  })

  it('agrees with the selectable provider list', () => {
    for (const kind of ['in_cluster', 'aks', 'eks'] as const) {
      const provider = providerForClusterKind(kind)
      expect(provider).not.toBeNull()
      expect(SELECTABLE_DEPLOYMENT_PROVIDERS).toContain(provider!)
    }
  })
})

describe('clustersForProvider', () => {
  const clusters = [cluster('c-aks', 'aks'), cluster('c-eks', 'eks'), cluster('c-k8s', 'generic')]

  it('offers only the clusters the provider permits', () => {
    expect(clustersForProvider(clusters, 'aws_eks').map(c => c.id)).toEqual(['c-eks'])
    expect(clustersForProvider(clusters, 'azure_aks').map(c => c.id)).toEqual(['c-aks'])
    expect(clustersForProvider(clusters, 'kubernetes').map(c => c.id)).toEqual(['c-k8s'])
  })

  it('offers everything when the domain is unconstrained', () => {
    expect(clustersForProvider(clusters, null)).toHaveLength(3)
  })
})

describe('conflictingPools', () => {
  const clusters = [cluster('c-aks', 'aks'), cluster('c-eks', 'eks')]

  it('reports a pool bound to another provider', () => {
    const conflicts = conflictingPools([pool('p1', 'c-aks')], clusters, 'aws_eks')
    expect(conflicts).toHaveLength(1)
    expect(conflicts[0].pool_id).toBe('p1')
    expect(conflicts[0].reason).toContain('Azure')
  })

  it('reports an unbound pool as conflicting with ANY constraint', () => {
    // An unbound pool scales through the controller's own cluster, which is
    // exactly what the constraint exists to prevent.
    const conflicts = conflictingPools([pool('p2')], clusters, 'aws_eks')
    expect(conflicts).toHaveLength(1)
    expect(conflicts[0].reason).toContain('not bound')
  })

  it('reports a pool whose cluster no longer exists', () => {
    const conflicts = conflictingPools([pool('p3', 'c-gone')], clusters, 'aws_eks')
    expect(conflicts[0].reason).toContain('could not be loaded')
  })

  it('reports nothing when every pool matches', () => {
    expect(conflictingPools([pool('p4', 'c-eks')], clusters, 'aws_eks')).toEqual([])
  })
})

describe('deploymentTargetDraftFrom', () => {
  it('omits the concurrency token for an unconfigured domain', () => {
    // The server requires a zero token on a first write, so a stale client
    // cannot clobber a target someone else just created.
    const draft = deploymentTargetDraftFrom({
      domain_id: 'd1',
      configured: false,
      scoped_pool_count: 0,
      updated_at: '2026-08-06T00:00:00Z',
    })
    expect(draft.provider).toBe('')
    expect(draft.updatedAt).toBe('')
  })

  it('carries the token for a configured domain', () => {
    const target: DomainDeploymentTarget = {
      domain_id: 'd1',
      configured: true,
      provider: 'aws_eks',
      default_cluster_id: 'c-eks',
      scoped_pool_count: 2,
      updated_at: '2026-08-06T00:00:00Z',
    }
    const draft = deploymentTargetDraftFrom(target)
    expect(draft).toEqual({
      domainId: 'd1',
      provider: 'aws_eks',
      defaultClusterId: 'c-eks',
      updatedAt: '2026-08-06T00:00:00Z',
    })
  })
})

describe('deploymentTargetDraftError', () => {
  const clusters = [cluster('c-aks', 'aks'), cluster('c-eks', 'eks')]

  it('requires a provider', () => {
    expect(
      deploymentTargetDraftError({ domainId: 'd1', provider: '', defaultClusterId: '', updatedAt: '' }, clusters),
    ).toMatch(/provider/i)
  })

  it('rejects a default cluster from a different provider', () => {
    const error = deploymentTargetDraftError(
      { domainId: 'd1', provider: 'aws_eks', defaultClusterId: 'c-aks', updatedAt: '' },
      clusters,
    )
    expect(error).toMatch(/does not match/i)
  })

  it('rejects a default cluster that no longer exists', () => {
    expect(
      deploymentTargetDraftError(
        { domainId: 'd1', provider: 'aws_eks', defaultClusterId: 'c-gone', updatedAt: '' },
        clusters,
      ),
    ).toMatch(/no longer exists/i)
  })

  it('accepts a matching default, and no default at all', () => {
    expect(
      deploymentTargetDraftError(
        { domainId: 'd1', provider: 'aws_eks', defaultClusterId: 'c-eks', updatedAt: '' },
        clusters,
      ),
    ).toBeNull()
    expect(
      deploymentTargetDraftError({ domainId: 'd1', provider: 'aws_eks', defaultClusterId: '', updatedAt: '' }, clusters),
    ).toBeNull()
  })
})

describe('deploymentTargetPayload', () => {
  it('omits updated_at on a first write', () => {
    const payload = deploymentTargetPayload({
      domainId: 'd1',
      provider: 'aws_eks',
      defaultClusterId: '',
      updatedAt: '',
    })
    expect('updated_at' in payload).toBe(false)
    expect(payload.provider).toBe('aws_eks')
  })

  it('sends updated_at on an edit', () => {
    const payload = deploymentTargetPayload({
      domainId: 'd1',
      provider: 'azure_aks',
      defaultClusterId: 'c-aks',
      updatedAt: '2026-08-06T00:00:00Z',
    })
    expect(payload.updated_at).toBe('2026-08-06T00:00:00Z')
    expect(payload.default_cluster_id).toBe('c-aks')
  })
})

describe('providerLabel', () => {
  it('renders an unset provider as unconstrained rather than blank', () => {
    expect(providerLabel(undefined)).toBe('Unconstrained')
    expect(providerLabel('')).toBe('Unconstrained')
  })

  it('labels every selectable provider', () => {
    for (const provider of SELECTABLE_DEPLOYMENT_PROVIDERS) {
      expect(providerLabel(provider)).not.toBe(provider)
    }
  })
})
