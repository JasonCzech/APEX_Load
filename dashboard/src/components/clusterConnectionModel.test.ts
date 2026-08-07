import { describe, expect, it } from 'vitest'

import type { ClusterConnection, ClusterTestResult } from '../types/cluster'
import {
  ClusterDraft,
  clusterDraftFrom,
  connectionBaseline,
  connectionDraftError,
  connectionIsDirty,
  connectionPayload,
  describeProbe,
  emptyClusterDraft,
} from './clusterConnectionModel'

const VALID_PEM = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----'

function aksDraft(overrides: Partial<ClusterDraft> = {}): ClusterDraft {
  return {
    ...emptyClusterDraft(),
    name: 'eastus-perf',
    namespace: 'apex',
    apiServerUrl: 'https://apex-aks.hcp.eastus.azmk8s.io',
    caCertificatePem: VALID_PEM,
    azureTenantId: 'tenant-1',
    azureClientId: 'client-1',
    clientSecret: 'super-secret',
    ...overrides,
  }
}

function connection(overrides: Partial<ClusterConnection> = {}): ClusterConnection {
  return {
    id: 'cluster-1',
    name: 'eastus-perf',
    kind: 'aks',
    api_server_url: 'https://apex-aks.hcp.eastus.azmk8s.io',
    namespace: 'apex',
    tls_mode: 'custom_ca',
    auth_mode: 'azure_service_principal',
    azure_tenant_id: 'tenant-1',
    azure_client_id: 'client-1',
    status: 'active',
    has_client_secret: true,
    credential_source: 'inline_encrypted',
    created_at: '2026-08-06T00:00:00Z',
    updated_at: '2026-08-06T01:00:00Z',
    ...overrides,
  }
}

describe('connectionDraftError', () => {
  it('accepts a complete AKS draft', () => {
    expect(connectionDraftError(aksDraft(), 'create')).toBeNull()
  })

  it('requires a secret on create but not on edit', () => {
    const noSecret = aksDraft({ clientSecret: '' })
    expect(connectionDraftError(noSecret, 'create')).toMatch(/client secret is required/)
    // On edit a blank field means "keep the stored secret".
    expect(connectionDraftError(noSecret, 'edit')).toBeNull()
  })

  it('requires an https API server URL for remote kinds only', () => {
    expect(connectionDraftError(aksDraft({ apiServerUrl: '' }), 'create')).toMatch(/API server URL is required/)
    expect(connectionDraftError(aksDraft({ apiServerUrl: 'http://cluster' }), 'create')).toMatch(/https/)
    // An in-cluster connection derives its endpoint from the controller.
    const inCluster = emptyClusterDraft()
    inCluster.kind = 'in_cluster'
    inCluster.name = 'local'
    inCluster.namespace = 'apex'
    expect(connectionDraftError(inCluster, 'create')).toBeNull()
  })

  it('always requires a namespace', () => {
    expect(connectionDraftError(aksDraft({ namespace: '' }), 'create')).toMatch(/namespace is required/)
  })

  it('requires a PEM-shaped CA unless system roots are selected', () => {
    expect(connectionDraftError(aksDraft({ caCertificatePem: '' }), 'create')).toMatch(/CA certificate is required/)
    expect(connectionDraftError(aksDraft({ caCertificatePem: 'not a cert' }), 'create')).toMatch(/PEM-encoded/)
    expect(connectionDraftError(aksDraft({ caCertificatePem: '', tlsMode: 'system_roots' }), 'create')).toBeNull()
  })

  it('requires the Azure directory identifiers for service-principal auth', () => {
    expect(connectionDraftError(aksDraft({ azureTenantId: '' }), 'create')).toMatch(/tenant ID/)
    expect(connectionDraftError(aksDraft({ azureClientId: '' }), 'create')).toMatch(/client ID/)
  })

  it('rejects supplying both a secret and an environment variable', () => {
    expect(connectionDraftError(aksDraft({ clientSecretEnvVar: 'AKS_SECRET' }), 'create')).toMatch(/not both/)
  })

  it('validates the static-token path for generic clusters', () => {
    const generic = aksDraft({ kind: 'generic', authMode: 'static_token', clientSecret: '', staticToken: '' })
    expect(connectionDraftError(generic, 'create')).toMatch(/service-account token is required/)
    expect(connectionDraftError({ ...generic, staticToken: 'token' }, 'create')).toBeNull()
  })
})

describe('connectionPayload', () => {
  it('OMITS the credential key when blank on edit', () => {
    const payload = connectionPayload(aksDraft({ clientSecret: '' }), 'edit')
    // Omitted and undefined differ over the wire: an empty string would clear
    // the stored credential server-side.
    expect('client_secret' in payload).toBe(false)
    expect(payload.updated_at).toBeDefined()
  })

  it('includes the credential when the operator typed one', () => {
    const payload = connectionPayload(aksDraft(), 'create')
    expect(payload.client_secret).toBe('super-secret')
  })

  it('omits Azure identifiers when the auth mode is not service principal', () => {
    const payload = connectionPayload(aksDraft({ kind: 'generic', authMode: 'static_token', staticToken: 't' }), 'create')
    expect('azure_tenant_id' in payload).toBe(false)
    expect(payload.static_token).toBe('t')
  })

  it('always sends the env-var reference for a remote connection so it can be cleared', () => {
    // The server keeps an omitted reference (it is a credential reference), so
    // omitting a blank field would make clearing it in the form a no-op.
    const cleared = connectionPayload(aksDraft({ clientSecret: '', clientSecretEnvVar: '' }), 'edit')
    expect('client_secret_env_var' in cleared).toBe(true)
    expect(cleared.client_secret_env_var).toBe('')

    const set = connectionPayload(aksDraft({ clientSecret: '', clientSecretEnvVar: 'AKS_SECRET' }), 'edit')
    expect(set.client_secret_env_var).toBe('AKS_SECRET')
  })

  it('omits remote fields entirely for an in-cluster connection', () => {
    const draft = emptyClusterDraft()
    draft.kind = 'in_cluster'
    draft.name = 'local'
    draft.namespace = 'apex'
    const payload = connectionPayload(draft, 'create')
    expect('api_server_url' in payload).toBe(false)
    expect('client_secret' in payload).toBe(false)
    expect(payload.auth_mode).toBe('in_cluster')
  })
})

describe('dirty tracking', () => {
  it('excludes credential fields from the baseline but still flags a typed secret', () => {
    const draft = aksDraft({ clientSecret: '' })
    const baseline = connectionBaseline(draft)
    expect(connectionIsDirty(draft, baseline)).toBe(false)
    // Typing a secret is a real edit even though it is not part of the baseline.
    expect(connectionIsDirty({ ...draft, clientSecret: 'new' }, baseline)).toBe(true)
    expect(connectionIsDirty({ ...draft, description: 'changed' }, baseline)).toBe(true)
  })
})

describe('clusterDraftFrom', () => {
  it('never populates the credential inputs from a loaded connection', () => {
    const draft = clusterDraftFrom(connection())
    expect(draft.clientSecret).toBe('')
    expect(draft.staticToken).toBe('')
    // The card uses this to render "leave blank to keep the stored secret".
    expect(draft.hasStoredCredential).toBe(true)
    expect(draft.updatedAt).toBe('2026-08-06T01:00:00Z')
  })

  it('round-trips the env-var reference, which is a name and not a secret', () => {
    // Without this, an unrelated edit would save a blank reference and detach
    // the connection's credential.
    const draft = clusterDraftFrom(
      connection({ has_client_secret: false, credential_source: 'env_var', client_secret_env_var: 'AKS_SECRET' }),
    )
    expect(draft.clientSecretEnvVar).toBe('AKS_SECRET')
    expect(connectionPayload(draft, 'edit').client_secret_env_var).toBe('AKS_SECRET')
  })
})

describe('describeProbe', () => {
  const base: ClusterTestResult = { status: 'ok', summary: '', probes: [], checked_at: '2026-08-06T00:00:00Z' }

  it('reports success with the server version', () => {
    expect(describeProbe({ ...base, server_version: 'v1.29.4' })).toEqual({
      tone: 'success',
      text: 'All checks passed (Kubernetes v1.29.4).',
    })
  })

  it('treats a missing optional capability as a warning, not a failure', () => {
    // Degraded is the expected state without the opt-in node ClusterRole.
    const result = describeProbe({
      ...base,
      status: 'degraded',
      probes: [{ probe: 'nodes', ok: false, optional: true, message: 'node read permission is not granted' }],
    })
    expect(result.tone).toBe('warning')
    expect(result.text).toMatch(/node read permission/)
  })

  it('surfaces the failing probe message', () => {
    const result = describeProbe({
      ...base,
      status: 'failed',
      summary: 'required checks failed: api_server',
      probes: [{ probe: 'api_server', ok: false, message: 'the Kubernetes API server rejected the credential (401 Unauthorized)' }],
    })
    expect(result.tone).toBe('danger')
    expect(result.text).toMatch(/401/)
  })
})
