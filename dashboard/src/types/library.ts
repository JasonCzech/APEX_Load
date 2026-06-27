export type LibraryKind = 'script' | 'scenario' | 'result'
export type LibrarySourceFilter = 'all' | 'local' | 'host'

export type LibraryDependency = {
  kind: string
  id: string
  name?: string
  content_hash?: string
}

export type LibraryCatalogItem = {
  kind: LibraryKind
  id: string
  name: string
  project_id?: string
  release_id?: string
  release_name?: string
  protocol?: string
  version?: string
  status?: string
  content_hash?: string
  execution_run_date?: string
  updated_at?: string
  has_archive?: boolean
  dependencies?: LibraryDependency[]
  sync_status?: string
  local_item_id?: string
  remote_item_id?: string
}

export type LibraryCatalogResponse = {
  items?: LibraryCatalogItem[]
  count?: number
}

export type LibraryCapabilitiesResponse = {
  schema_version?: string
  instance_id?: string
  item_kinds?: string[]
  operations?: string[]
}

export type LibraryRemote = {
  id: string
  name: string
  base_url: string
  default_project_id?: string
  auth_method?: string
  credential_ref?: string
  credential_configured?: boolean
  status?: string
  last_health_check?: string
  last_error?: string
  capabilities?: LibraryCapabilitiesResponse
  created_at?: string
  updated_at?: string
}

export type LibraryRemoteRequest = {
  name?: string
  base_url?: string
  default_project_id?: string
  auth_method?: string
  credential_ref?: string
  api_key?: string
}

export type LibraryRemoteListResponse = {
  remotes?: LibraryRemote[]
  count?: number
}

export type LibraryRemoteCatalogResponse = {
  remote: LibraryRemote
  catalog: LibraryCatalogResponse
}

export type LibraryImportResultItem = {
  kind: string
  source_instance_id?: string
  source_item_id?: string
  local_item_id: string
  content_hash?: string
  action: string
}

export type LibraryConflict = {
  kind: string
  source_instance_id?: string
  source_item_id?: string
  local_item_id?: string
  reason: string
}

export type LibraryImportResponse = {
  schema_version?: string
  imported_at?: string
  items?: LibraryImportResultItem[]
  conflicts?: LibraryConflict[]
}

export type LibraryTransferResponse = {
  remote?: LibraryRemote
  import?: LibraryImportResponse
}

export type LibraryDiffResponse = {
  kind: string
  local_id: string
  remote_id: string
  local_hash: string
  remote_hash: string
  status: string
}

export type LibraryTransferRequest = {
  kind: LibraryKind
  id?: string
  local_id?: string
  remote_id?: string
  project_id?: string
  include_dependencies?: boolean
}

export type DesktopStatusResponse = {
  desktop_mode: boolean
  os: string
  api_address: string
  browser_url: string
  app_data_path?: string
  log_path?: string
  version?: string
  commit?: string
  build_time?: string
  storage?: Record<string, unknown>
  cache?: Record<string, unknown>
  recorder_proxy?: Record<string, unknown>
  remote_libraries?: {
    count?: number
    statuses?: Record<string, number>
  }
}
