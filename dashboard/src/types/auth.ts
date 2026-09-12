import type { UsageAccountingBucket, UsageAccountingResponse } from './system'

// Authentication, authorization, identity-administration, and audit transport
// contracts. These are intentionally independent of React view state.

export interface DashboardAuthSettings {
  mode: 'dev' | 'disabled' | 'local' | 'trusted_headers' | 'oidc'
  provider_name?: string
  login_url?: string
  logout_url?: string
  issuer_url?: string
  client_id?: string
  client_secret_env_var?: string
  redirect_url?: string
  scopes?: string[]
  roles_claim?: string
  project_roles_claim?: string
  domain_roles_claim?: string
  application_repository_roles_claim?: string
  session_secret_env_var?: string
  trusted_proxy_cidrs?: string[]
  trusted_proxy_secret_env_var?: string
  user_header: string
  name_header: string
  email_header: string
  roles_header: string
  project_roles_header: string
  domain_roles_header?: string
  application_repository_roles_header?: string
}

export interface DashboardAuthenticatedUser {
  id?: string
  subject: string
  display_name?: string
  email?: string
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  domain_roles?: Record<string, string[]>
  application_repository_roles?: Record<string, string[]>
  scopes?: string[]
}

export interface DashboardAuthStatus {
  enabled: boolean
  mode: DashboardAuthSettings['mode']
  provider_name?: string
  login_url?: string
  logout_url?: string
  logout_csrf_token?: string
  authenticated: boolean
  session_status?: 'active' | 'expired' | 'invalid' | 'missing' | 'unavailable' | string
  session_expires_at?: string
  session_ttl_seconds?: number
  user?: DashboardAuthenticatedUser
  dev_view_mode?: 'admin' | 'user'
  simulated?: boolean
}

export interface DashboardAuthDiagnosticsCheck {
  id: string
  label: string
  status: 'pass' | 'warn' | 'fail' | 'info' | string
  summary: string
  detail?: string
}

export interface DashboardAuthDiagnostics {
  enabled: boolean
  mode: DashboardAuthSettings['mode']
  provider_name?: string
  provider_model: string
  ready: boolean
  summary: string
  checks: DashboardAuthDiagnosticsCheck[]
}

export interface DashboardAuthRoleDefinition {
  id: string
  name: string
  scope: string
  description: string
  capabilities?: string[]
}

export interface DashboardAuthRoleCatalog {
  provider_model: string
  roles: DashboardAuthRoleDefinition[]
}

export interface DashboardAuthAccessOperation {
  id: string
  label: string
  global_roles?: string[]
  project_roles?: string[]
  domain_roles?: string[]
  application_repository_roles?: string[]
}

export interface DashboardAuthAccessResource {
  id: string
  name: string
  description: string
  operations: DashboardAuthAccessOperation[]
}

export interface DashboardAuthAccessMatrix {
  version: string
  resources: DashboardAuthAccessResource[]
}

export interface DashboardAuthPolicy {
  provider_model: string
  version: string
  roles: DashboardAuthRoleDefinition[]
  resources: DashboardAuthAccessResource[]
}

export interface DashboardAuthEffectiveAccessOperation {
  id: string
  label: string
  allowed: boolean
  grant_source?: string
}

export interface DashboardAuthEffectiveAccessResource {
  id: string
  name: string
  description: string
  operations: DashboardAuthEffectiveAccessOperation[]
}

export interface DashboardAuthEffectiveAccess {
  enabled: boolean
  authenticated: boolean
  project_id?: string
  domain_id?: string
  application_repository_id?: string
  resources: DashboardAuthEffectiveAccessResource[]
  dev_view_mode?: 'admin' | 'user'
  simulated?: boolean
  principal_type?: 'user' | 'service_account'
  principal_id?: string
  principal_name?: string
  scopes_narrow_api_access?: boolean
}

export interface DashboardServiceAccount {
  id: string
  name: string
  description?: string
  owner_id?: string
  contact_email?: string
  active: boolean
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  domain_roles?: Record<string, string[]>
  application_repository_roles?: Record<string, string[]>
  scopes?: string[]
  last_used_at?: string
  expires_at?: string
  key_rotated_at?: string
  rotation_interval_days?: number
  rotation_due_at?: string
  created_at: string
  updated_at: string
}

export interface DashboardServiceAccountListResponse {
  service_accounts: DashboardServiceAccount[]
  count: number
}

export type DashboardUserRoleScopeType = 'global' | 'domain' | 'project' | 'application_repository'

export interface DashboardUserRoleBinding {
  id: string
  user_id: string
  scope_type: DashboardUserRoleScopeType
  scope_id?: string
  role: string
  created_at: string
  updated_at: string
}

export interface DashboardUser {
  id: string
  subject: string
  email?: string
  display_name?: string
  active: boolean
  created_at: string
  updated_at: string
  last_seen_at?: string
  disabled_at?: string
  bindings?: DashboardUserRoleBinding[]
}

export interface DashboardUserListResponse {
  users: DashboardUser[]
  count: number
  total?: number
  limit?: number
  offset?: number
  has_more?: boolean
}

export interface DashboardAccessReviewUser {
  id: string
  subject: string
  display_name?: string
  active: boolean
  flags: string[]
  global_roles?: string[]
  last_seen_at?: string
}

export interface DashboardAccessReviewServiceAccount {
  id: string
  name: string
  active: boolean
  flags: string[]
  scopes?: string[]
  last_used_at?: string
  expires_at?: string
  rotation_due_at?: string
}

export interface DashboardAccessReviewResponse {
  generated_at: string
  stale_after_days: number
  expiring_within_days: number
  users: DashboardAccessReviewUser[]
  service_accounts: DashboardAccessReviewServiceAccount[]
  summary: Record<string, number>
  recent_denials?: number
  recent_denial_window_hours?: number
}

export interface DashboardCreateUserRequest {
  subject: string
  email?: string
  display_name?: string
  active?: boolean
}

export interface DashboardUpdateUserRequest {
  subject?: string
  email?: string
  display_name?: string
  active?: boolean
}

export interface DashboardCreateUserBindingRequest {
  scope_type: DashboardUserRoleScopeType
  scope_id?: string
  role: string
}

export interface DashboardOnboardUserRequest extends DashboardCreateUserRequest {
  bindings: DashboardCreateUserBindingRequest[]
}

export type DashboardUserUsageBucket = UsageAccountingBucket

export interface DashboardUserActivityResponse {
  has_more?: boolean
  usage_runs?: { id: string; name: string; project_id: string; actor: string; created_at: string; status: string }[]
  events: DashboardAuditEvent[]
  usage: UsageAccountingResponse
  count: number
  limit?: number
  offset?: number
}

export interface DashboardCreateServiceAccountRequest {
  name: string
  description?: string
  owner_id?: string
  contact_email?: string
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  domain_roles?: Record<string, string[]>
  application_repository_roles?: Record<string, string[]>
  scopes?: string[]
  expires_at?: string
  rotation_interval_days?: number
}

export interface DashboardCreateServiceAccountResponse {
  service_account: DashboardServiceAccount
  api_key: string
}

export interface DashboardUpdateServiceAccountRequest {
  name?: string
  description?: string
  owner_id?: string
  contact_email?: string
  active?: boolean
  global_roles?: string[]
  project_roles?: Record<string, string[]>
  domain_roles?: Record<string, string[]>
  application_repository_roles?: Record<string, string[]>
  scopes?: string[]
  expires_at?: string
  rotation_interval_days?: number
}

export interface DashboardAuditEvent {
  id: string
  action: string
  actor_type: string
  actor_subject: string
  actor_display_name?: string
  domain_id?: string
  project_id?: string
  resource_type: string
  resource_id?: string
  status: string
  details?: Record<string, unknown>
  created_at: string
}

export interface DashboardAuditEventListResponse {
  events: DashboardAuditEvent[]
  count: number
  limit?: number
  offset?: number
}

export interface DashboardAuditEventFilter {
  category?: 'fleet'
  all?: 'true'
  report?: 'usage'
  limit?: number
  offset?: number
  action?: string
  actor_type?: string
  actor_subject?: string
  domain_id?: string
  project_id?: string
  resource_type?: string
  resource_id?: string
  status?: string
  created_after?: string
  created_before?: string
}
