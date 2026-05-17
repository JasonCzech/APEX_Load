export interface ScriptWaitCondition {
  type?: string
  value?: string
  timeout_ms?: number
}

export interface BrowserCaptureOptions {
  screenshot?: boolean
  full_page?: boolean
  html?: boolean
}

export interface ScriptExtractorDefinition {
  name: string
  source: string
  type: string
  expression: string
}

export interface ScriptAssertionDefinition {
  type: string
  expected?: unknown
  value?: string
  max?: number
}

export interface ScriptActionDefinition {
  name: string
  type: string
  method?: string
  target: string
  selector?: string
  value?: string
  headers?: Record<string, string>
  body?: string
  params?: Record<string, unknown>
  metadata?: Record<string, unknown>
  wait_for?: ScriptWaitCondition
  capture?: BrowserCaptureOptions
  extractors?: ScriptExtractorDefinition[]
  assertions?: ScriptAssertionDefinition[]
  think_time_after?: boolean
  soap?: {
    action?: string
    operation?: string
    body_mode?: string
  }
}

export interface ThinkTimeConfig {
  type: string
  mean_ms: number
  std_ms?: number
  min_ms?: number
  max_ms?: number
  multiplier?: number
  limit_ms?: number
  ignore?: boolean
}

export interface PacingConfig {
  type: string
  delay_ms?: number
  min_delay_ms?: number
  max_delay_ms?: number
  interval_ms?: number
  min_interval_ms?: number
  max_interval_ms?: number
  tps?: number
}

export interface RunLogicConfig {
  iterations?: number
  auto_transactions?: string
  block_scheduling?: string
  action_sequence?: string[]
}

export interface ErrorHandlingConfig {
  continue_on_error?: boolean
  max_retries?: number
  retry_delay_ms?: number
  retry_backoff?: string
  snapshot_on_error?: boolean
}

export interface LogConfig {
  level?: string
  on_error_only?: boolean
  extended_options?: string[]
}

export interface HTTPConfig {
  connect_timeout_ms?: number
  request_timeout_ms?: number
  tls_timeout_ms?: number
  idle_timeout_ms?: number
  version?: string
  max_conns_per_host?: number
  keep_alive?: boolean
  user_agent?: string
  accept_encoding?: string
  accept_language?: string
  max_redirects?: number
  proxy_mode?: string
  default_headers?: Record<string, string>
  tls?: {
    min_version?: string
    insecure_skip_verify?: boolean
    client_cert_file?: string
    client_key_file?: string
    ca_cert_file?: string
  }
  proxy?: {
    url?: string
    username?: string
    password?: string
  }
  auth?: {
    type?: string
    username?: string
    password?: string
    token?: string
  }
}

export interface BrowserConfig {
  engine?: string
  headless?: boolean
  slow_mo_ms?: number
  user_agent?: string
  device_profile?: string
  touch_mode?: boolean
  orientation?: string
  client_metrics?: boolean
  capture_screenshots?: string
  cache_enabled?: boolean
  cache_content_urls?: boolean
  check_newer_versions?: boolean
  new_user_per_iteration?: boolean
  download_resources?: string
  viewport?: {
    width?: number
    height?: number
  }
}

export interface DownloadFiltersConfig {
  mode?: string
  hosts?: string[]
}

export interface ContentCheckConfig {
  text?: string
  scope?: string
  fail_if_found?: boolean
}

export interface NetworkSimulationConfig {
  mode?: string
  preset?: string
  bandwidth_kbps?: number
  latency_ms?: number
  jitter_ms?: number
  packet_loss_pct?: number
}

export interface StreamingConfig {
  mode?: string
  buffer_size_seconds?: number
  fixed_bitrate_kbps?: number
  bandwidth_estimation?: string
}

export interface DFETransform {
  type?: string
  options?: Record<string, string>
}

export interface DataFormattingConfig {
  auto_detect?: boolean
  chain?: DFETransform[]
}

export interface WebRuntimeConfig {
  additional_attributes?: Record<string, string>
  download_filters?: DownloadFiltersConfig
  content_checks?: ContentCheckConfig[]
  network_simulation?: NetworkSimulationConfig
  streaming?: StreamingConfig
  data_formatting?: DataFormattingConfig
}

export interface JavaHTTPConfig {
  codec?: string
  helper_mode?: string
  content_type?: string
  accept?: string
  body_encoding?: string
  object_schema?: string
  request_mapping?: Record<string, string>
  response_mapping?: Record<string, string>
  extractor_type?: string
  preserve_binary_result?: boolean
}

export interface JMSConfig {
  provider?: string
  broker_url?: string
  message_vpn?: string
  username?: string
  password?: string
  client_id?: string
  destination?: string
  destination_type?: string
  reply_to?: string
  acknowledgement?: string
  delivery_mode?: string
  timeout_ms?: number
  properties?: Record<string, string>
  mock?: boolean
}

export interface SOAPConfig {
  version?: string
  namespaces?: Record<string, string>
  ws_security?: {
    username_token?: {
      username?: string
      password?: string
      password_type?: string
      nonce?: boolean
      created?: boolean
    }
    timestamp?: {
      ttl_seconds?: number
    }
  }
  fault_handling?: {
    fail_on_fault?: boolean
  }
}

export interface ScriptImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

export interface ScriptMetadata {
  import?: ScriptImportMetadata
}

export interface ScriptConfig {
  base_url: string
  think_time?: ThinkTimeConfig
  pacing?: PacingConfig
  run_logic?: RunLogicConfig
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  http?: HTTPConfig
  web_runtime?: WebRuntimeConfig
  java_http?: JavaHTTPConfig
  soap?: SOAPConfig
  browser?: BrowserConfig
  jms?: JMSConfig
}

export interface ScriptDataSource {
  name: string
  type: string
  path: string
  inline_data?: string
  strategy: string
  delimiter?: string
  first_row_header?: boolean
  columns?: string[]
}

export interface ScriptParameterDefinition {
  name: string
  type: string
  update?: string
  selection?: string
  on_exhaustion?: string
  data_source?: string
  column?: string
  format?: string
  timezone?: string
  offset_seconds?: number
  min?: number
  max?: number
  start?: number
  end?: number
  step?: number
  length?: number
  charset?: string
  pad_width?: number
  pad_char?: string
  values?: string[]
  seed?: number
}

export interface ScriptDefinition {
  id?: string
  name: string
  version: string
  protocol: string
  project_id?: string
  folder_id?: string | null
  metadata?: ScriptMetadata
  config: ScriptConfig
  actions: ScriptActionDefinition[]
  data_sources?: ScriptDataSource[]
  parameters?: ScriptParameterDefinition[]
  created_at?: string
  updated_at?: string
}

export interface ScriptReplayConsoleEntry {
  level?: string
  text?: string
  source?: string
  timestamp?: string
}

export interface ScriptReplayNetworkEntry {
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  duration_ms?: number
  bytes_received?: number
}

export interface ScriptReplayDOMSummary {
  title?: string
  ready_state?: string
  visible_text?: string[]
}

export interface ScriptReplaySnapshot {
  request_method?: string
  request_url?: string
  request_headers?: Record<string, string>
  request_body?: string
  response_status?: number
  response_headers?: Record<string, string>
  response_body?: string
  page_url?: string
  page_title?: string
  html?: string
  screenshot_base64?: string
  console_entries?: ScriptReplayConsoleEntry[]
  network_entries?: ScriptReplayNetworkEntry[]
  dom_summary?: ScriptReplayDOMSummary
}

export interface ScriptReplayActionEvent {
  phase: string
  action_name: string
  action_type: string
  method?: string
  target: string
  selector?: string
  value?: string
  headers?: Record<string, string>
  body?: string
  started_at: string
  completed_at: string
  duration_ms: number
  status_code?: number
  success: boolean
  error_message?: string
  extractions?: Record<string, string>
  page_url?: string
  page_title?: string
  html?: string
  screenshot_base64?: string
  console_entries?: ScriptReplayConsoleEntry[]
  network_entries?: ScriptReplayNetworkEntry[]
  dom_summary?: ScriptReplayDOMSummary
  snapshot?: ScriptReplaySnapshot
}

export interface ScriptReplayReport {
  group: string
  protocol: string
  started_at: string
  completed_at: string
  success: boolean
  error?: string
  events: ScriptReplayActionEvent[]
  final_variables?: Record<string, string>
  snapshots?: ScriptReplaySnapshot[]
}

export interface ScriptReplayResponse {
  script_id: string
  replay_session_id: string
  report: ScriptReplayReport
}

export interface ScriptValidationRuntimeSettingParity {
  id: string
  area: string
  concept: string
  fields?: string[]
  status: string
  notes?: string
  runtime_files?: string[]
}

export interface ScriptValidationMigrationPolicy {
  block_statuses?: string[]
}

export interface ScriptValidationMigrationReadiness {
  score_pct: number
  blocked: boolean
  summary?: string
  blocking_statuses?: string[]
  blocking_settings?: ScriptValidationRuntimeSettingParity[]
  policy?: ScriptValidationMigrationPolicy
}

export interface ScriptValidationResponse {
  script_id: string
  valid: boolean
  issues: string[]
  warnings?: string[]
  runtime_settings_parity?: ScriptValidationRuntimeSettingParity[]
  runtime_settings_status_counts?: Record<string, number>
  migration_readiness?: ScriptValidationMigrationReadiness
}

export interface ScriptExportResponse {
  exported: boolean
  path: string
  filename: string
  size: number
}
