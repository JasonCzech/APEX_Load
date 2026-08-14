import type { CloudConnectionInput, ComputeRegistrationManifest, ExecutionTargetInput } from '../types/compute'

export function cloudConnectionError(connection: CloudConnectionInput): string | null {
  if (!connection.name.trim()) return 'Connection name is required.'
  if (connection.provider === 'azure') {
    if (!connection.azure_subscription_id?.trim()) return 'Azure subscription ID is required.'
    if (connection.auth_mode === 'azure_service_principal' && (!connection.azure_tenant_id?.trim() || !connection.azure_client_id?.trim())) {
      return 'Tenant and client IDs are required for a service principal.'
    }
    if (connection.auth_mode === 'azure_service_principal' && !connection.client_secret?.trim() && !connection.credential_env_var?.trim()) {
      return 'Client secret or credential environment variable is required for a service principal.'
    }
  } else {
    if (!connection.aws_region?.trim()) return 'AWS region is required.'
    if (connection.auth_mode === 'aws_assume_role' && !connection.aws_role_arn?.trim()) return 'Role ARN is required for assume role.'
    if (connection.auth_mode === 'aws_assume_role' && connection.credential_env_var?.trim()) return 'Credential environment variables are not supported for assume role; use the ambient source identity.'
    if (connection.auth_mode === 'aws_static_keys' && !connection.aws_access_key_id?.trim()) return 'Access key ID is required for static keys.'
    if (connection.auth_mode === 'aws_static_keys' && !connection.aws_secret_access_key?.trim() && !connection.credential_env_var?.trim()) return 'Secret access key or credential environment variable is required for static keys.'
  }
  return null
}

export function executionTargetError(target: ExecutionTargetInput): string | null {
  if (!target.name.trim()) return 'Target name is required.'
  if (!target.cloud_connection_id?.trim()) return 'Cloud connection is required.'
  if (target.kind === 'azure_container_apps') {
    const spec = target.azure_container_apps
    if (!spec?.resource_group.trim() || !spec.environment_name.trim()) return 'Resource group and Container Apps environment are required.'
    if (!spec.warm_container_app?.trim() && !spec.burst_job?.trim()) return 'Configure a warm Container App, a burst Job, or both.'
    if (!spec.expected_principal_id?.trim()) return 'Expected managed-identity principal ID is required.'
  }
  if (target.kind === 'aws_ecs_fargate') {
    const spec = target.aws_ecs_fargate
    if (!spec?.cluster.trim()) return 'ECS cluster is required.'
    if (!spec.warm_service?.trim() && !spec.burst_task_definition?.trim()) return 'Configure a warm ECS service, a burst task definition, or both.'
    if (!spec.expected_task_role_arn?.trim()) return 'Expected task role ARN is required.'
    if (spec.burst_task_definition?.trim() && (!spec.subnets?.length || !spec.security_groups?.length)) return 'Burst Fargate tasks require subnets and security groups.'
  }
  return null
}

export function parseComputeRegistrationManifest(value: string): ComputeRegistrationManifest {
  const parsed = JSON.parse(value) as ComputeRegistrationManifest
  if (!parsed || typeof parsed !== 'object' || !parsed.cloud_connection || !Array.isArray(parsed.execution_targets)) {
    throw new Error('Manifest must contain cloud_connection and execution_targets.')
  }
  const connectionError = cloudConnectionError(parsed.cloud_connection)
  if (connectionError) throw new Error(connectionError)
  if (parsed.execution_targets.length === 0) throw new Error('Manifest must contain at least one execution target.')
  return parsed
}
