// Cluster-connection and host-allocation transport contracts. UI-only state
// (draft rows, dirty flags) belongs with the consuming feature rather than here.

/** Kubernetes control planes an administrator can register. */
export type ClusterKind = 'in_cluster' | 'generic' | 'aks' | 'eks'

export type ClusterAuthMode = 'in_cluster' | 'static_token' | 'azure_service_principal' | 'aws_iam'

/**
 * How the controller obtains the AWS identity it presigns EKS tokens with.
 * Always explicit — never inferred from which fields are filled in, because
 * inference is how a connection silently authenticates as the controller's own
 * role against a cluster the administrator did not intend.
 */
export type AWSCredentialMode = 'static_keys' | 'assume_role' | 'ambient'

export type ClusterTLSMode = 'custom_ca' | 'system_roots'

/**
 * The read shape. It deliberately carries NO credential field — not even a
 * redacted one — so "the secret is never rendered back" is guaranteed by the
 * type rather than by discipline. Only ClusterConnectionInput can carry one.
 */
export interface ClusterConnection {
  id: string
  name: string
  description?: string
  kind: ClusterKind
  api_server_url?: string
  namespace: string
  generator_selector?: string
  generator_stateful_set?: string
  generator_container?: string
  ca_certificate_pem?: string
  tls_mode: ClusterTLSMode
  auth_mode: ClusterAuthMode
  azure_tenant_id?: string
  azure_client_id?: string
  /** AWS identifiers for an EKS control plane. The secret access key and the
   *  role external ID are NEVER returned; only their presence is. */
  aws_region?: string
  aws_cluster_name?: string
  aws_credential_mode?: AWSCredentialMode
  aws_access_key_id?: string
  aws_role_arn?: string
  aws_sts_endpoint?: string
  has_aws_external_id?: boolean
  labels?: Record<string, string>
  status: string
  has_client_secret: boolean
  /** The NAME of an environment variable holding the credential, never its
   *  value. Returned so an edit form can round-trip the reference. */
  client_secret_env_var?: string
  credential_source: string
  auth_description?: string
  last_checked_at?: string
  last_check_status?: string
  last_check_message?: string
  created_at: string
  /** Optimistic-concurrency token; echo it back when updating. */
  updated_at: string
}

export interface ClusterConnectionListResponse {
  clusters: ClusterConnection[]
  total: number
}

/**
 * The write shape. client_secret / static_token are write-only and must be
 * OMITTED (not sent as an empty string) to keep the stored credential — an
 * explicit empty string clears it server-side.
 */
export interface ClusterConnectionInput {
  name: string
  description?: string
  kind: ClusterKind
  api_server_url?: string
  namespace: string
  generator_selector?: string
  generator_stateful_set?: string
  generator_container?: string
  ca_certificate_pem?: string
  tls_mode: ClusterTLSMode
  auth_mode: ClusterAuthMode
  azure_tenant_id?: string
  azure_client_id?: string
  aws_region?: string
  aws_cluster_name?: string
  aws_credential_mode?: AWSCredentialMode
  aws_access_key_id?: string
  aws_role_arn?: string
  aws_sts_endpoint?: string
  client_secret?: string
  static_token?: string
  /** Write-only, omit-to-keep — same rule as client_secret. */
  aws_secret_access_key?: string
  aws_external_id?: string
  client_secret_env_var?: string
  labels?: Record<string, string>
  status?: string
  updated_at?: string
}

export interface ClusterProbe {
  probe: string
  ok: boolean
  optional?: boolean
  status_code?: number
  message?: string
}

export interface ClusterTestResult {
  status: 'ok' | 'degraded' | 'failed' | string
  summary: string
  probes: ClusterProbe[]
  checked_at: string
  server_version?: string
}

export interface ClusterNodePool {
  name: string
  /**
   * The node label KEY that supplied `name`. Echo it back as
   * PoolAllocation.node_pool_label: pinning to an EKS managed node group needs
   * eks.amazonaws.com/nodegroup, and the generic "agentpool" key matches no
   * node at all, leaving the generator pods Pending forever.
   */
  label?: string
  zone?: string
  node_count: number
  schedulable_nodes: number
  allocatable_cpu_millis: number
  allocatable_memory_bytes: number
  taints?: string[]
}

/**
 * Node reads require a cluster-scoped ClusterRole that ships opt-in, so
 * `available: false` with `unavailable_reason: 'rbac_forbidden'` is an expected
 * state, not an error.
 */
export interface ClusterNodeCapacity {
  available: boolean
  unavailable_reason?: string
  node_pools?: ClusterNodePool[]
  label_values?: Record<string, string[]>
}

export interface ClusterQuota {
  name: string
  hard?: Record<string, string>
  used?: Record<string, string>
}

export interface ClusterWorkload {
  name: string
  replicas: number
  ready_replicas: number
  container?: string
  container_names?: string[]
  update_strategy?: string
  current_requests?: Record<string, string>
  current_limits?: Record<string, string>
  node_selector?: Record<string, string>
  warnings?: string[]
}

export interface ClusterCapacityResponse {
  cluster_id: string
  namespace: string
  node_capacity: ClusterNodeCapacity
  quotas: ClusterQuota[]
  generator_workload?: ClusterWorkload
}

export interface Toleration {
  key?: string
  operator?: 'Equal' | 'Exists' | string
  value?: string
  effect?: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute' | string
}

/** Host-resource budget and placement for a fleet pool's generator pods. */
export interface PoolAllocation {
  cpu_request?: string
  cpu_limit?: string
  memory_request?: string
  memory_limit?: string
  node_pool?: string
  /**
   * The node label KEY that `node_pool` is matched against. Empty means the
   * default "agentpool", which is what every allocation written before EKS
   * support carries. An EKS managed node group needs
   * eks.amazonaws.com/nodegroup; the generic key matches no EKS node.
   */
  node_pool_label?: string
  zone?: string
  node_selector?: Record<string, string>
  tolerations?: Toleration[]
}

export interface PoolAllocationInput extends PoolAllocation {
  cluster_id?: string
  updated_at: string
}

export interface AllocationPreflight {
  allowed: boolean
  quota_enforced: boolean
  reasons?: string[]
  warnings?: string[]
  cpu_millis_delta: number
  memory_bytes_delta: number
  pods_delta: number
}

export interface PoolAllocationResponse {
  pool_id: string
  cluster_id?: string
  allocation?: PoolAllocation
  preflight?: AllocationPreflight
  applied: boolean
  container?: string
  warnings?: string[]
}
