import { describe, expect, it } from 'vitest'

import type { ExecutionTargetInput } from '../types/compute'
import { cloudConnectionError, executionTargetError, parseComputeRegistrationManifest } from './computeInfrastructureModel'

describe('compute infrastructure validation', () => {
  it('requires provider-bound generator identities', () => {
    const target: ExecutionTargetInput = {
      name: 'burst', kind: 'aws_ecs_fargate', cloud_connection_id: 'aws-1', connectivity_mode: 'reverse',
      standing_owner: 'apex', burst_owner: 'apex', archive_mode: 'controller_pull', status: 'active',
      aws_ecs_fargate: { cluster: 'load', burst_task_definition: 'generator:1', subnets: ['subnet-1'], security_groups: ['sg-1'] },
    }
    expect(executionTargetError(target)).toContain('task role ARN')
  })

  it('keeps ambient identity manifests credential-free', () => {
    const manifest = parseComputeRegistrationManifest(JSON.stringify({
      cloud_connection: { name: 'prod', provider: 'azure', auth_mode: 'azure_identity', azure_subscription_id: 'sub-1' },
      execution_targets: [{ name: 'jobs', kind: 'azure_container_apps' }],
    }))
    expect(cloudConnectionError(manifest.cloud_connection)).toBeNull()
    expect(JSON.stringify(manifest)).not.toContain('client_secret')
  })

  it('requires the provider-specific static credential value or env reference', () => {
    expect(cloudConnectionError({
      name: 'aws', provider: 'aws', auth_mode: 'aws_static_keys', aws_region: 'us-east-1', aws_access_key_id: 'AKIAEXAMPLE',
    })).toContain('Secret access key')
    expect(cloudConnectionError({
      name: 'azure', provider: 'azure', auth_mode: 'azure_service_principal', azure_subscription_id: 'sub', azure_tenant_id: 'tenant', azure_client_id: 'client', credential_env_var: 'AZURE_CLIENT_SECRET',
    })).toBeNull()
		expect(cloudConnectionError({
			name: 'assume', provider: 'aws', auth_mode: 'aws_assume_role', aws_region: 'us-east-1', aws_role_arn: 'arn:aws:iam::123456789012:role/load', credential_env_var: 'AWS_EXTERNAL_ID',
		})).toContain('not supported')
  })
})
