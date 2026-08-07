import type {
  AWSCredentialMode,
  ClusterAuthMode,
  ClusterConnection,
  ClusterConnectionInput,
  ClusterKind,
  ClusterTestResult,
  ClusterTLSMode,
} from '../types/cluster'

/**
 * Pure validation and payload construction for the cluster registry form.
 *
 * The secret handling rule that everything else follows: on edit, a blank
 * credential field means "keep the stored one", and the payload must OMIT the
 * key entirely. Sending an empty string would clear the credential server-side.
 */

export interface ClusterDraft {
  id: string
  name: string
  description: string
  kind: ClusterKind
  apiServerUrl: string
  namespace: string
  generatorSelector: string
  generatorStatefulSet: string
  generatorContainer: string
  caCertificatePem: string
  tlsMode: ClusterTLSMode
  authMode: ClusterAuthMode
  azureTenantId: string
  azureClientId: string
  awsRegion: string
  awsClusterName: string
  awsCredentialMode: AWSCredentialMode
  awsAccessKeyId: string
  awsRoleArn: string
  awsStsEndpoint: string
  awsSecretAccessKey: string
  awsExternalId: string
  clientSecret: string
  staticToken: string
  clientSecretEnvVar: string
  status: string
  /** True when the server already holds a credential for this connection. */
  hasStoredCredential: boolean
  /** True when an AWS role external ID is stored, so the form can render
   *  "leave blank to keep" without ever receiving the value. */
  hasAwsExternalId: boolean
  updatedAt: string
}

export function emptyClusterDraft(): ClusterDraft {
  return {
    id: '',
    name: '',
    description: '',
    kind: 'aks',
    apiServerUrl: '',
    namespace: '',
    generatorSelector: '',
    generatorStatefulSet: '',
    generatorContainer: '',
    caCertificatePem: '',
    tlsMode: 'custom_ca',
    authMode: 'azure_service_principal',
    azureTenantId: '',
    azureClientId: '',
    awsRegion: '',
    awsClusterName: '',
    awsCredentialMode: 'ambient',
    awsAccessKeyId: '',
    awsRoleArn: '',
    awsStsEndpoint: '',
    awsSecretAccessKey: '',
    awsExternalId: '',
    clientSecret: '',
    staticToken: '',
    clientSecretEnvVar: '',
    status: 'active',
    hasStoredCredential: false,
    hasAwsExternalId: false,
    updatedAt: '',
  }
}

export function clusterDraftFrom(connection: ClusterConnection): ClusterDraft {
  return {
    id: connection.id,
    name: connection.name,
    description: connection.description ?? '',
    kind: connection.kind,
    apiServerUrl: connection.api_server_url ?? '',
    namespace: connection.namespace,
    generatorSelector: connection.generator_selector ?? '',
    generatorStatefulSet: connection.generator_stateful_set ?? '',
    generatorContainer: connection.generator_container ?? '',
    caCertificatePem: connection.ca_certificate_pem ?? '',
    tlsMode: connection.tls_mode,
    authMode: connection.auth_mode,
    azureTenantId: connection.azure_tenant_id ?? '',
    azureClientId: connection.azure_client_id ?? '',
    awsRegion: connection.aws_region ?? '',
    awsClusterName: connection.aws_cluster_name ?? '',
    awsCredentialMode: connection.aws_credential_mode ?? 'ambient',
    awsAccessKeyId: connection.aws_access_key_id ?? '',
    awsRoleArn: connection.aws_role_arn ?? '',
    awsStsEndpoint: connection.aws_sts_endpoint ?? '',
    // Credential VALUES always start empty: the read shape carries no secret.
    awsSecretAccessKey: '',
    awsExternalId: '',
    clientSecret: '',
    staticToken: '',
    // The env-var NAME is not a secret and must round-trip, or saving an
    // unrelated edit would clear the reference.
    clientSecretEnvVar: connection.client_secret_env_var ?? '',
    status: connection.status,
    hasStoredCredential: connection.has_client_secret,
    hasAwsExternalId: connection.has_aws_external_id ?? false,
    updatedAt: connection.updated_at,
  }
}

/**
 * The authentication mode each cluster kind implies. Exported so the form and
 * the payload builder cannot drift: an EKS connection saved with the wrong auth
 * mode is rejected by the server, but only after the operator has typed
 * everything in.
 */
export const AUTH_MODE_FOR_KIND: Record<ClusterKind, ClusterAuthMode> = {
  in_cluster: 'in_cluster',
  generic: 'static_token',
  aks: 'azure_service_principal',
  eks: 'aws_iam',
}

/** Whether the connection reaches a cluster other than the controller's own. */
export function isRemoteKind(kind: ClusterKind): boolean {
  return kind !== 'in_cluster'
}

/** The single validator called during render. */
export function connectionDraftError(draft: ClusterDraft, mode: 'create' | 'edit'): string | null {
  if (draft.name.trim() === '') return 'A cluster name is required.'
  if (draft.namespace.trim() === '') return 'A namespace is required.'

  if (!isRemoteKind(draft.kind)) {
    // The in-cluster connection derives its endpoint and credential from the
    // controller's own environment, so nothing else is required.
    return null
  }

  const url = draft.apiServerUrl.trim()
  if (url === '') return 'An API server URL is required.'
  if (!url.startsWith('https://')) return 'The API server URL must start with https://.'

  const pem = draft.caCertificatePem.trim()
  if (draft.tlsMode === 'custom_ca' && pem === '') {
    return 'A CA certificate is required unless the TLS mode is set to system roots.'
  }
  // Only check for the PEM marker; parsing X.509 in the browser buys nothing.
  if (pem !== '' && !pem.includes('-----BEGIN CERTIFICATE-----')) {
    return 'The CA certificate must be PEM-encoded (it should contain a BEGIN CERTIFICATE block).'
  }

  const usesEnvVar = draft.clientSecretEnvVar.trim() !== ''

  if (draft.authMode === 'azure_service_principal') {
    if (draft.azureTenantId.trim() === '') return 'An Azure tenant ID is required.'
    if (draft.azureClientId.trim() === '') return 'An Azure client ID is required.'
    if (mode === 'create' && draft.clientSecret.trim() === '' && !usesEnvVar) {
      return 'An Azure client secret is required.'
    }
    if (usesEnvVar && draft.clientSecret.trim() !== '') {
      return 'Provide either a client secret or an environment variable name, not both.'
    }
  }

  if (draft.authMode === 'static_token') {
    if (mode === 'create' && draft.staticToken.trim() === '' && !usesEnvVar) {
      return 'A service-account token is required.'
    }
    if (usesEnvVar && draft.staticToken.trim() !== '') {
      return 'Provide either a token or an environment variable name, not both.'
    }
  }

  if (draft.authMode === 'aws_iam') {
    if (draft.kind !== 'eks') {
      return 'AWS IAM authentication is only valid for an Amazon EKS connection.'
    }
    if (draft.awsRegion.trim() === '') return 'An AWS region is required.'
    if (draft.awsClusterName.trim() === '') return 'An EKS cluster name is required.'
    // The cluster name is written into a SIGNED HTTP header, so its charset is
    // a header-injection control rather than cosmetics. Mirrors the server.
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$/.test(draft.awsClusterName.trim())) {
      return 'The EKS cluster name may only contain letters, numbers, hyphens, and underscores.'
    }
    if (draft.awsCredentialMode === 'static_keys') {
      if (draft.awsAccessKeyId.trim() === '') return 'An AWS access key ID is required.'
      if (mode === 'create' && draft.awsSecretAccessKey.trim() === '' && !usesEnvVar) {
        return 'An AWS secret access key is required.'
      }
      if (usesEnvVar && draft.awsSecretAccessKey.trim() !== '') {
        return 'Provide either a secret access key or an environment variable name, not both.'
      }
    }
    if (draft.awsCredentialMode === 'assume_role') {
      if (!/^arn:aws[a-z-]*:iam::\d{12}:role\/.+$/.test(draft.awsRoleArn.trim())) {
        return 'A role ARN of the form arn:aws:iam::123456789012:role/name is required.'
      }
    }
    if (draft.awsCredentialMode === 'ambient') {
      // An ambiguous configuration must fail loudly rather than silently
      // preferring one credential over the other.
      if (draft.awsAccessKeyId.trim() !== '' || draft.awsSecretAccessKey.trim() !== '') {
        return 'Workload identity cannot be combined with a static AWS access key.'
      }
      if (draft.awsExternalId.trim() !== '') {
        return 'An external ID only applies when assuming a role.'
      }
    }
    const endpoint = draft.awsStsEndpoint.trim()
    if (endpoint !== '') {
      // A presigned STS URL is a replayable bearer credential, so an endpoint
      // that is not real STS would exfiltrate it. Mirrors the server allow-list.
      if (!/^https:\/\/sts(-fips)?(\.[a-z0-9-]{1,32})?\.amazonaws\.com(\.cn)?\/?$/.test(endpoint)) {
        return 'The STS endpoint must be an AWS STS host such as https://sts.us-east-1.amazonaws.com.'
      }
    }
  }

  return null
}

/**
 * Builds the write payload. Credential keys are omitted when blank on edit —
 * assert with `'client_secret' in payload`, since omitted and undefined differ
 * over the wire.
 */
export function connectionPayload(draft: ClusterDraft, mode: 'create' | 'edit'): ClusterConnectionInput {
  const remote = isRemoteKind(draft.kind)
  const payload: ClusterConnectionInput = {
    name: draft.name.trim(),
    description: draft.description.trim(),
    kind: draft.kind,
    namespace: draft.namespace.trim(),
    generator_selector: draft.generatorSelector.trim(),
    generator_stateful_set: draft.generatorStatefulSet.trim(),
    generator_container: draft.generatorContainer.trim(),
    tls_mode: remote ? draft.tlsMode : 'custom_ca',
    auth_mode: remote ? draft.authMode : 'in_cluster',
    status: draft.status || 'active',
  }
  if (remote) {
    payload.api_server_url = draft.apiServerUrl.trim()
    payload.ca_certificate_pem = draft.caCertificatePem.trim()
    if (draft.authMode === 'azure_service_principal') {
      payload.azure_tenant_id = draft.azureTenantId.trim()
      payload.azure_client_id = draft.azureClientId.trim()
    }
    if (draft.authMode === 'aws_iam') {
      payload.aws_region = draft.awsRegion.trim()
      payload.aws_cluster_name = draft.awsClusterName.trim()
      payload.aws_credential_mode = draft.awsCredentialMode
      payload.aws_sts_endpoint = draft.awsStsEndpoint.trim()
      // Ambient credentials carry no key or role, so send them empty rather
      // than leaving stale values from a previous mode on the record.
      payload.aws_access_key_id =
        draft.awsCredentialMode === 'ambient' ? '' : draft.awsAccessKeyId.trim()
      payload.aws_role_arn =
        draft.awsCredentialMode === 'assume_role' ? draft.awsRoleArn.trim() : ''
    }
    // Always sent for a remote connection, including when blank. The server
    // treats an omitted reference as "keep the stored one" (it is a credential
    // reference, like the secret itself), so omitting it on edit would make
    // clearing the field in this form silently do nothing.
    payload.client_secret_env_var = draft.clientSecretEnvVar.trim()
    // Only include a credential the operator actually typed. On create the
    // validator has already required one; on edit a blank field means keep.
    if (draft.clientSecret.trim() !== '') payload.client_secret = draft.clientSecret
    if (draft.staticToken.trim() !== '') payload.static_token = draft.staticToken
    if (draft.awsSecretAccessKey.trim() !== '') payload.aws_secret_access_key = draft.awsSecretAccessKey
    if (draft.awsExternalId.trim() !== '') payload.aws_external_id = draft.awsExternalId
  }
  if (mode === 'edit') payload.updated_at = draft.updatedAt
  return payload
}

/**
 * The dirty baseline excludes the credential fields: they always start blank on
 * edit, so including them would make dirty-state reasoning confusing. The card
 * tracks credential edits as a separate boolean.
 */
export function connectionBaseline(draft: ClusterDraft): string {
  const credentialFields = new Set<keyof ClusterDraft>([
    'clientSecret',
    'staticToken',
    'awsSecretAccessKey',
    'awsExternalId',
  ])
  return JSON.stringify(
    Object.fromEntries(Object.entries(draft).filter(([key]) => !credentialFields.has(key as keyof ClusterDraft))),
  )
}

export function connectionIsDirty(draft: ClusterDraft, baseline: string): boolean {
  if (connectionBaseline(draft) !== baseline) return true
  return (
    draft.clientSecret.trim() !== '' ||
    draft.staticToken.trim() !== '' ||
    draft.awsSecretAccessKey.trim() !== '' ||
    draft.awsExternalId.trim() !== ''
  )
}

/** Renders a test result for display. */
export function describeProbe(result: ClusterTestResult): { tone: 'success' | 'warning' | 'danger'; text: string } {
  const failures = result.probes.filter((probe) => !probe.ok)
  if (result.status === 'ok') {
    const version = result.server_version ? ` (Kubernetes ${result.server_version})` : ''
    return { tone: 'success', text: `All checks passed${version}.` }
  }
  if (result.status === 'degraded') {
    // A degraded result is the expected state without the optional node
    // ClusterRole, so it must not read as a failure.
    const optional = failures.filter((probe) => probe.optional).map((probe) => probe.message || probe.probe)
    return { tone: 'warning', text: `Connected, with limited permissions. ${optional.join(' ')}`.trim() }
  }
  const detail = failures.map((probe) => probe.message || probe.probe).join(' ')
  return { tone: 'danger', text: detail || result.summary || 'The connection test failed.' }
}

/** Short label for a connection's credential source. */
export function describeCredentialSource(connection: ClusterConnection): string {
  switch (connection.credential_source) {
    case 'in_cluster':
      return 'Controller service account'
    case 'env_var':
      return 'Environment variable'
    case 'inline_encrypted':
      return 'Stored (encrypted)'
    case 'workload_identity':
      return 'Workload identity (IRSA / Pod Identity)'
    default:
      return 'Not set'
  }
}
