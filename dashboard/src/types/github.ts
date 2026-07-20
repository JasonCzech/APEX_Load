// Transport contracts for GitHub script synchronization and automation.
// Keep these shapes isolated from dashboard view state so they can be replaced
// by generated OpenAPI aliases without changing feature imports.

export interface GitHubConfigResponse {
  enabled: boolean
  oauth_configured: boolean
  app_configured?: boolean
  webhook_configured?: boolean
  pat_configured?: boolean
  encryption_available: boolean
  app_id?: number
  app_slug?: string
  app_private_key_env_var?: string
  app_private_key_file?: string
  webhook_secret_env_var?: string
  pat_env_var?: string
  automation_mode?: string
  checks_enabled?: boolean
  api_base_url: string
  web_base_url: string
  scopes: string[]
  default_repo: string
  default_branch: string
  path_template: string
  tls_insecure_skip_verify: boolean
}

export interface GitHubConnectionResponse {
  connected: boolean
  login?: string
  github_user_id?: string
  scopes?: string
  connected_at?: string
  expires_at?: string
}

export interface GitHubRepo {
  name: string
  full_name: string
  owner: string
  private: boolean
  default_branch: string
  html_url: string
}

export interface GitHubBranch {
  name: string
  commit_sha: string
}

export interface GitHubContentEntry {
  type: string
  name: string
  path: string
  sha: string
  size: number
}

export interface GitHubContentsResponse {
  type: 'dir' | 'file'
  path?: string
  name?: string
  sha?: string
  size?: number
  html_url?: string
  entries?: GitHubContentEntry[]
}

export interface GitHubPushResponse {
  pushed: boolean
  strategy: string
  owner: string
  repo: string
  branch: string
  path: string
  commit_sha: string
  file_sha: string
  remote_blob_sha?: string
  remote_commit_sha?: string
  auth_mode?: string
  html_url: string
  pull_request_url?: string
  pull_request_number?: number
}

export interface GitHubScriptStatus {
  linked: boolean
  status: string
  owner?: string
  repo?: string
  branch?: string
  path?: string
  last_commit_sha?: string
  remote_blob_sha?: string
  remote_commit_sha?: string
  synced_at?: string
  local_drift?: boolean
  remote_drift?: boolean
  remote_missing?: boolean
  remote_checked?: boolean
  html_url?: string
}

export interface GitHubDiagnosticsResponse {
  enabled: boolean
  oauth_configured: boolean
  app_configured: boolean
  webhook_configured: boolean
  pat_configured: boolean
  encryption_available: boolean
  automation_mode: string
  checks_enabled: boolean
  reachable?: boolean
  ghes_version?: string
  known_installations?: number
  reachability_error?: string
}

export interface GitHubAppInstallation {
  installation_id?: number
  account_login?: string
  account_type?: string
  repository_selection?: string
  html_url?: string
}

export interface GitHubSyncJob {
  id?: string
  project_id?: string
  kind?: string
  status?: string
  owner?: string
  repo?: string
  branch?: string
  path?: string
  total?: number
  processed?: number
  created?: number
  updated?: number
  conflicts?: number
  errors?: number
  summary_json?: string
  created_at?: string
  updated_at?: string
  completed_at?: string
}

export interface GitHubPushRequest {
  owner: string
  repo: string
  branch?: string
  path?: string
  message?: string
  strategy?: 'commit' | 'pr'
  pr_branch?: string
  pr_title?: string
  pr_body?: string
}

export interface GitHubImportRequest {
  project_id: string
  owner: string
  repo: string
  path: string
  ref?: string
  name?: string
}

export interface GitHubSyncJobRequest {
  project_id: string
  kind: 'import' | 'push'
  owner?: string
  repo?: string
  branch?: string
  path?: string
  script_ids?: string[]
  strategy?: 'commit' | 'pr'
}
