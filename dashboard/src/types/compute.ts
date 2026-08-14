import type { ClusterProbe } from './cluster'

export type CloudProvider = 'azure' | 'aws'
export type CloudAuthMode = 'azure_identity' | 'azure_service_principal' | 'aws_ambient' | 'aws_assume_role' | 'aws_static_keys'
export type ExecutionTargetKind = 'kubernetes' | 'azure_container_apps' | 'aws_ecs_fargate' | 'static'

export interface CloudConnection {
  id: string
  name: string
  description?: string
  provider: CloudProvider
  auth_mode: CloudAuthMode
  azure_cloud?: 'public' | 'us_government' | 'china'
  azure_tenant_id?: string
  azure_subscription_id?: string
  azure_resource_group?: string
  azure_client_id?: string
  aws_partition?: 'aws' | 'aws-us-gov' | 'aws-cn'
  aws_account_id?: string
  aws_region?: string
  aws_access_key_id?: string
  aws_role_arn?: string
  credential_env_var?: string
  credential_source: string
  has_client_secret: boolean
  has_aws_external_id: boolean
  status: string
  last_checked_at?: string
  last_check_status?: string
  last_check_message?: string
  labels?: Record<string, string>
  created_at: string
  updated_at: string
}

export interface CloudConnectionInput {
  id?: string
  name: string
  description?: string
  provider: CloudProvider
  auth_mode: CloudAuthMode
  azure_cloud?: string
  azure_tenant_id?: string
  azure_subscription_id?: string
  azure_resource_group?: string
  azure_client_id?: string
  aws_partition?: string
  aws_account_id?: string
  aws_region?: string
  aws_access_key_id?: string
  aws_role_arn?: string
  credential_env_var?: string
  client_secret?: string
  aws_secret_access_key?: string
  aws_external_id?: string
  status?: string
  labels?: Record<string, string>
  updated_at?: string
}

export interface RuntimeProfile {
  id: string
  name: string
  cpu_millis?: number
  memory_mib?: number
  ephemeral_storage_gib?: number
  architecture?: string
  image_digest?: string
  template_revision?: string
  capacity_type?: string
  sidecars?: string[]
  protocols?: string[]
}

export interface TargetCapabilities {
  can_scale: boolean
  can_launch: boolean
  can_reach_directly: boolean
  supports_reverse: boolean
  supports_spot: boolean
  resource_shapes?: string[]
  details?: Record<string, string>
}

export interface ExecutionTarget {
  id: string
  name: string
  description?: string
  kind: ExecutionTargetKind
  cloud_connection_id?: string
  cluster_connection_id?: string
  region?: string
  connectivity_mode: 'direct' | 'reverse'
  standing_owner: 'apex' | 'external'
  burst_owner: 'apex' | 'disabled'
  archive_mode: 'controller_pull' | 'object_direct'
  enrollment_token?: string
  enrollment_token_configured: boolean
  enrollment_token_expires_at?: string
  azure_container_apps?: {
    resource_group: string
    environment_name: string
    warm_container_app?: string
    burst_job?: string
    expected_principal_id?: string
  }
  aws_ecs_fargate?: {
    cluster: string
    warm_service?: string
    burst_task_definition?: string
    subnets?: string[]
    security_groups?: string[]
    assign_public_ip?: 'enabled' | 'disabled'
    expected_task_role_arn?: string
  }
  runtime_profiles?: RuntimeProfile[]
  capabilities: TargetCapabilities
  status: string
  last_checked_at?: string
  last_check_status?: string
  last_check_message?: string
  labels?: Record<string, string>
  created_at: string
  updated_at: string
}

export type ExecutionTargetInput = Omit<ExecutionTarget,
  'id' | 'capabilities' | 'enrollment_token_configured' | 'created_at' | 'updated_at' | 'last_checked_at' | 'last_check_status' | 'last_check_message'
> & { id?: string; updated_at?: string }

export interface ExecutionTargetTestResult {
  status: string
  summary: string
  probes: ClusterProbe[]
  capabilities: TargetCapabilities
  checked_at: string
}

export interface ComputeRegistrationManifest {
  cloud_connection: CloudConnectionInput
  execution_targets: Array<Omit<ExecutionTargetInput, 'cloud_connection_id'> & { cloud_connection_id?: string }>
}
