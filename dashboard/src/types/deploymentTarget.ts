// Per-domain deployment targets: which cloud a domain's fleet pools may scale
// into, and the cluster connection new pools inherit.
//
// A target is both a DEFAULT and a HARD CONSTRAINT. The absence of a target
// means the domain is unconstrained, which is the state of every domain until a
// superuser configures one.

/**
 * The canonical provider vocabulary. It is deliberately more precise than the
 * legacy free-text FleetPool.cloud_provider values, and maps 1:1 onto
 * ClusterConnection.kind: in_cluster/generic → kubernetes, aks → azure_aks,
 * eks → aws_eks.
 */
export type DeploymentProvider = 'kubernetes' | 'azure_aks' | 'aws_eks' | 'azure_container_apps' | 'aws_ecs_fargate' | 'static'

export interface DomainDeploymentTarget {
  domain_id: string
  domain_name?: string
  /** False means the domain is unconstrained. The API returns 200 with this
   *  flag rather than 404, because "no constraint" is a normal state. */
  configured: boolean
  provider?: DeploymentProvider
  default_cluster_id?: string
  default_cluster_name?: string
  default_execution_target_id?: string
  default_execution_target_name?: string
  allowed_execution_target_ids?: string[]
  scoped_pool_count: number
  updated_by_subject?: string
  created_at?: string
  /** Optimistic-concurrency token; echo it back when updating. Omit it when
   *  configuring a domain for the first time. */
  updated_at?: string
}

export interface DomainDeploymentTargetInput {
  provider: DeploymentProvider
  default_cluster_id?: string
  default_execution_target_id?: string
  allowed_execution_target_ids?: string[]
  updated_at?: string
}

export interface DomainDeploymentTargetListResponse {
  targets: DomainDeploymentTarget[]
  total: number
  /** Served by the API so the UI never hardcodes the vocabulary. */
  selectable_providers: DeploymentProvider[]
}

/** A pool that a proposed target would invalidate. */
export interface ConflictingFleetPool {
  pool_id: string
  pool_name: string
  cluster_id?: string
  cluster_name?: string
  provider?: string
  reason: string
}

/** The 409 body returned when a retarget would strand existing pools. */
export interface DomainDeploymentTargetConflict {
  provider: DeploymentProvider
  conflicting_pools: ConflictingFleetPool[]
}
