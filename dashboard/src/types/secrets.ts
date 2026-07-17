import type { ScriptDefinition } from './scripts'
import type { SecretBindingRequirement } from './scriptImports'

export interface ProjectSecretMetadata {
  name: string
  provider: string
  writable: boolean
  created_at?: string
  updated_at?: string
  created_by?: string
}

export interface ProjectSecretStatus {
  name: string
  configured: boolean
  provider?: string
  writable?: boolean
}

export interface ProjectSecretCapabilities {
  provider: string
  writable: boolean
  create_supported: boolean
  replace_supported: boolean
  source_secret_migration_policy_enabled: boolean
}

export interface ProjectSecretsResponse {
  project_id: string
  capabilities: ProjectSecretCapabilities
  secrets: ProjectSecretMetadata[]
  count: number
}

export interface ProjectSecretStatusesResponse {
  project_id: string
  capabilities: ProjectSecretCapabilities
  secrets: ProjectSecretStatus[]
}

export interface ProjectSecretWrite {
  name: string
  value: string
}

export interface ProjectSecretsWriteResponse {
  project_id: string
  secrets?: ProjectSecretMetadata[]
  secret?: ProjectSecretMetadata
  count?: number
}

export interface ScriptSecretStatus {
  script_id: string
  project_id: string
  required: string[]
  configured: string[]
  missing: string[]
  secrets: ProjectSecretStatus[]
  capabilities: ProjectSecretCapabilities
}

export interface CloneScriptDraftResponse {
  script: ScriptDefinition
  secret_binding_requirements: SecretBindingRequirement[]
  source_secret_session_id?: string
  source_secret_session_expires_at?: string
  source_value_requirement_ids?: string[]
  secret_provider_capabilities: ProjectSecretCapabilities
}

export interface MigrateImportSecretsRequest {
  project_id: string
  source_secret_session_id: string
  secrets: Array<{
    requirement_id: string
    secret_name: string
  }>
}

export interface MigrateImportSecretsResponse {
  project_id: string
  secrets: ProjectSecretMetadata[]
  count: number
}

export interface DiscardImportSecretSessionRequest {
  project_id: string
  source_secret_session_id: string
}
