import type { GoalConfig } from './goals'

export interface ScriptWaitCondition {
  type?: string
  value?: string
  timeout_ms?: number
}

export interface ScriptBrowserSyncPolicy {
  type?: string
  value?: string
  timeout_ms?: number
  stable_ms?: number
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
  /** "" / "ignore": leave the variable unset on no match; "empty": assign ""; "fail": fail the request. */
  on_miss?: string
  default_value?: string
  /** "" extracts one value; "all" saves every match as name_1..name_N plus name_count. */
  mode?: string
  /** Skip this many leading bytes of each matched value. */
  save_offset?: number
  /** Truncate each matched value to at most this many bytes (0 = entire remainder). */
  save_length?: number
  /** Post-process each value: "url_encode" | "url_decode" | "html_decode" | "html_url_encode". */
  convert?: string
}

export interface ScriptAssertionDefinition {
  legacy_html_check?: boolean
  type: string
  expected?: unknown
  value?: string
  min?: number
  max?: number
}

export interface ScriptAuthConfig {
  type?: string
  username?: string
  password?: string
  token?: string
  domain?: string
}

export interface ScriptHeaderEntry {
  name: string
  value: string
  enabled?: boolean
}

// --- Correlation diagnose (record-vs-replay) -------------------------------

export interface CorrelationStep {
  id: number
  request_url?: string
  request_body?: string
  request_headers?: Record<string, string>
  response_body?: string
  response_headers?: Record<string, string>
  response_status?: number
  response_success?: boolean
  parent_action_name?: string
  request_index?: number
  step_path?: string[]
}

export interface SuggestedExtractor {
  name: string
  source: string
  extract_type: string
  expression: string
  convert?: string
  on_miss?: string
  default_value?: string
}

export interface CorrelationRepairSuggestion {
  rule_id: string
  source_family?: string
  severity: 'error' | 'warning'
  title: string
  detail: string
  source_step_id: number
  failing_step_id?: number
  used_in_ids?: number[]
  usage_count?: number
  affected_fields?: string[]
  repair_locations?: Array<{ field: string; start: number; end: number; editable_surface_exact?: boolean }>
  recorded_value?: string
  replay_value?: string
  suggestion: string
  extractor?: SuggestedExtractor
  confidence: number
  confidence_band: string
  review_action: string
}

export type CorrelationRelationshipState = 'producer_missing' | 'extractor_unavailable' | 'consumer_missing' | 'producer_unchanged' | 'stale_literal' | 'no_stale_literal' | 'manual_encoding' | 'manual_surface'

export interface CorrelationRelationshipCoverage {
  complete: boolean
  producer_count: number
  consumer_count: number
  missing_producers: number
  unavailable_extractors: number
  missing_consumers: number
  state_counts: Partial<Record<CorrelationRelationshipState, number>>
  details_complete: boolean
  details_omitted: number
  detail_limit: number
  relationships: { rule_id?: string; producer_step_id: number; consumer_step_id: number; state: CorrelationRelationshipState; producer_observed: boolean; extractor_available: boolean; consumer_observed: boolean }[]
}

export interface CorrelationDiagnoseResponse {
  relationship_coverage?: CorrelationRelationshipCoverage
  suggestions: CorrelationRepairSuggestion[]
  blocking: boolean
  counts: { errors: number; warnings: number }
  compared_recorded_steps?: number
  total_recorded_steps?: number
  truncated?: boolean
}

export type HTTPActionVersion = 'auto' | '1.1' | '2'

export interface ScriptActionDefinition {
  pacing_ids?: string[]
  name: string
  type: string
  method?: string
  /** Omit to inherit the script HTTP policy; explicit auto overrides an exact default. */
  http_version?: HTTPActionVersion
  target: string
  selector?: string
  // object_ref binds an element-targeting browser action to a named object in a
  // project-scoped object repository ("name" or "repoId:name"). The controller
  // resolves it into a concrete selector + ranked fallbacks at script prep.
  object_ref?: string
  value?: string
  headers?: Record<string, string>
  header_list?: ScriptHeaderEntry[]
  body?: string
  body_file?: string
  body_encoding?: string
  params?: Record<string, unknown>
  metadata?: Record<string, unknown>
  wait_for?: ScriptWaitCondition
  sync?: ScriptBrowserSyncPolicy
  capture?: BrowserCaptureOptions
  control?: ScriptControlFlow
  attribution?: ScriptAnalyticsAttribution
  extractors?: ScriptExtractorDefinition[]
  assertions?: ScriptAssertionDefinition[]
  think_time?: ThinkTimeConfig
  think_time_after?: boolean
  auth?: ScriptAuthConfig | null
  timeout_ms?: number
  max_redirects?: number | null
  soap?: {
    action?: string
    operation?: string
    body_mode?: string
  }
}

export interface ScriptAnalyticsAttribution {
  hierarchy_path?: string
  page_name?: string
}

export interface ScriptCondition {
  /** CEL expression over var/data/env; takes precedence over left/op/right. */
  expr?: string
  left?: string
  op?: string
  right?: string
}

export interface ScriptForEachLoop {
  /** Data source name iterated row-by-row. */
  source?: string
  /** Var-namespace prefix each row binds under: column c → {{var.<var>.c}}. */
  var?: string
  /** Optional per-row early-exit predicate; iteration stops the first row it is false. */
  condition?: ScriptCondition
}

export interface ScriptControlFlow {
	condition?: ScriptCondition
	steps?: ScriptActionDefinition[]
	else?: ScriptActionDefinition[]
	cases?: Array<{ condition: ScriptCondition; steps?: ScriptActionDefinition[] }>
	default?: ScriptActionDefinition[]
	branches?: Array<{ name: string; steps?: ScriptActionDefinition[] }>
	concurrency?: number
	fail_fast?: boolean
  count?: number
  max_iterations?: number
  /** control_loop data-driven mode; takes precedence over count/condition. */
  for_each?: ScriptForEachLoop
  /** Run a condition-driven loop body once before evaluating its predicate. */
  do_while?: boolean
}

export interface ScriptMessageDetailConfig {
	include_resources?: boolean
	mode?: 'off' | 'errors' | 'sampled' | 'adaptive'
	sample_rate?: number
	max_items?: number
	max_item_bytes?: number
	max_total_bytes?: number
}

export interface ScriptResultsConfig {
 property_analytics?: Array<{ name: string; extraction: string; values: string[] }>
  csv_exports?: Array<{ id: string; file_name: string; action_name: string; columns: Array<{ name: string; extraction?: string; metric?: "latency_ms"; default_value?: string }> }>
	message_detail?: ScriptMessageDetailConfig
}

export interface ThinkTimeConfig {
  min_percent?: number
  max_percent?: number
  type: string
  mean_ms?: number
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
  termination_mode?: "duration" | "iterations"
  iterations?: number
  global_iterations?: number
  max_transactions?: number
  workload_model?: string
  max_in_flight?: number
  auto_transactions?: string
  block_scheduling?: string
  action_sequence?: string[]
}

export interface ErrorHandlingConfig {
  fail_transaction_on_error_message?: boolean
  continue_on_error?: boolean
  max_retries?: number
  retry_delay_ms?: number
  retry_backoff?: string
  snapshot_on_error?: boolean
}

export interface LogConfig {
  error_buffer_bytes?: number
  max_response_bytes?: number
  level?: string
  on_error_only?: boolean
  extended_options?: string[]
  /**
   * redact_run_evidence governs RUN OUTPUT only — logs, snapshots, reports,
   * archives and debugger evidence. Default false (read an absent value as OFF).
   * It never alters the script: an imported script keeps its literal credential
   * values so it stays runnable. Platform-managed secrets ({{secret.NAME}}) are
   * redacted regardless of this flag.
   */
  redact_run_evidence?: boolean
}

export interface HTTPConfig {
  receive_buffer_bytes?: number
  max_meta_refresh?: number
  content_check_error_limit?: number
  http_errors_as_warnings?: boolean
  fail_noncritical_resources?: boolean
  resource_timeout_as_warning?: boolean
  legacy_html_checks?: boolean
  dns_cache?: boolean
  challenge_delay_ms?: number
  page_timeout_ms?: number
  ip_version?: 'auto' | 'ipv4' | 'ipv6'
  cache_max_unused_iterations?: number
  cache_excluded_mime_types?: string[]
  html_parse_mode?: 'html' | 'text' | 'any'
  html_excluded_mime_types?: string[]
  connect_timeout_ms?: number
  request_timeout_ms?: number
  tls_timeout_ms?: number
  idle_timeout_ms?: number
  version?: string
  max_conns_per_host?: number
  max_idle_conns?: number
  keep_alive?: boolean
  user_agent?: string
  accept_encoding?: string
  accept_language?: string
  max_redirects?: number
  proxy_mode?: string
  default_headers?: Record<string, string>
  default_header_list?: ScriptHeaderEntry[]
  cookies_enabled?: boolean
  clear_cookies_per_iteration?: boolean
  cache_enabled?: boolean
  traceparent?: boolean
  traceparent_sampled?: boolean
  tls?: {
    min_version?: string
    max_version?: string
    insecure_skip_verify?: boolean
    client_cert_file?: string
    client_key_file?: string
    ca_cert_file?: string
  }
  proxy?: {
    pac_url?: string
    url?: string
    username?: string
    password?: string
    no_proxy?: string[]
  }
  auth?: {
    type?: string
    username?: string
    password?: string
    token?: string
    domain?: string
  }
}

export interface BrowserConfig {
  storage_state?: string
  system_language?: string
  available_screen_width?: number
  available_screen_height?: number
  engine?: string
  headless?: boolean
  slow_mo_ms?: number
  user_agent?: string
  locale?: string
  timezone?: string
  device_profile?: string
  touch_mode?: boolean
  orientation?: string
  client_metrics?: boolean
  experience_canary?: boolean
  traceparent?: boolean
  har_capture?: string
  har_capture_policy?: string
  har_sla_threshold_ms?: number
  har_artifact_dir?: string
  capture_screenshots?: string
  cache_enabled?: boolean
  cache_content_urls?: boolean
  check_newer_versions?: boolean
  new_user_per_iteration?: boolean
  download_resources?: string
  geolocation?: BrowserGeolocation
  http_credentials?: BrowserHTTPCredentials
  proxy?: BrowserProxyConfig
  client_certificates?: BrowserClientCertificate[]
  viewport?: {
    width?: number
    height?: number
  }
  review_policy?: {
    allow_reviewed_javascript?: boolean
    allow_review_required?: boolean
  }
}

export interface BrowserGeolocation {
  latitude?: number
  longitude?: number
  accuracy?: number
}

export interface BrowserHTTPCredentials {
  username?: string
  password?: string
  origin?: string
  send?: string
}

export interface BrowserProxyConfig {
  server?: string
  bypass?: string
  username?: string
  password?: string
}

export interface BrowserClientCertificate {
  origin?: string
  cert_path?: string
  key_path?: string
  pfx_path?: string
  passphrase?: string
}

// BrowserEmulationConfig drives HTTP-transport replay identity (User-Agent /
// Accept-Language) and browser-cache simulation. Distinct from BrowserConfig,
// which configures the Playwright browser protocol.
export interface BrowserEmulationConfig {
  mode?: '' | 'specific' | 'custom'
  browser_type?: string
  browser_version?: string
  platform?: string
  language?: string
  custom_user_agent?: string
  cache?: BrowserCacheSimulationConfig
}

export interface BrowserCacheSimulationConfig {
  clear_cache_for_new_user?: boolean
  simulate_cache?: boolean
  cache_content_urls?: boolean
  check_newer_versions?: boolean
  download_non_html_resources?: boolean
  new_user_each_iteration?: boolean
  simulate_prefetch?: boolean
}

// Browser-emulation catalog served by GET /api/v1/dsl/browser-emulation-catalog.
export interface BrowserEmulationCatalogBrowser {
  type: string
  label: string
  versions: string[]
  platforms: string[]
}

export interface BrowserEmulationCatalog {
  browsers: BrowserEmulationCatalogBrowser[]
  platforms: { id: string; label: string }[]
  languages: { tag: string; label: string }[]
}

export interface DownloadFiltersConfig {
  mode?: string
  hosts?: string[]
}

export interface ContentCheckConfig {
  left_boundary?: string
  right_boundary?: string
  text?: string
  scope?: string
  fail_if_found?: boolean
  case_insensitive?: boolean
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

export interface ExternalMetricConfig {
  name?: string
  url?: string
  format?: 'prometheus'
  metric_name?: string
  labels?: Record<string, string>
}

export interface ObservabilityConfig {
  external_metrics?: ExternalMetricConfig[]
  // Messaging protocols write W3C trace context and archive consumer links when 'w3c'.
  trace_carrier?: 'none' | 'w3c'
}

export interface NetworkProfileWeight extends NetworkSimulationConfig {
  name: string
  weight: number
}

export interface WebRuntimeConfig {
  additional_attributes?: Record<string, string>
  download_filters?: DownloadFiltersConfig
  content_checks?: ContentCheckConfig[]
  network_simulation?: NetworkSimulationConfig
  streaming?: StreamingConfig
  data_formatting?: DataFormattingConfig
  source_ips?: string[]
  network_profiles?: NetworkProfileWeight[]
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

// JavaClasspathAsset binds one exact classpath entry to a user-uploaded JAR by
// content hash. Mirrors the Go dsl.JavaClasspathAsset. The classpath string
// list is unaffected; this is a parallel, entry-keyed record.
export interface JavaClasspathAsset {
  entry: string
  file_name?: string
  sha256: string
  size_bytes?: number
  content_type?: string
  storage_ref?: string
  staged_asset?: string
}

export interface JavaVuserConfig {
  enabled?: boolean
  runner_command?: string
  harness_jar?: string
  classpath?: string[]
  classpath_assets?: JavaClasspathAsset[]
  work_dir?: string
  max_processes?: number
  startup_timeout_ms?: number
  action_timeout_ms?: number
  jdk_selection?: 'internal' | 'specified'
  jdk_home?: string
  additional_vm_parameters?: string[]
  use_xbootclasspath?: boolean
  dedicated_class_loader?: boolean
  skip_parameter_evaluation?: boolean
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
  subscription?: string
  reply_to?: string
  acknowledgement?: string
  delivery_mode?: string
  timeout_ms?: number
  properties?: Record<string, string>
  tls?: JMSTLSConfig
  mock?: boolean
}

export interface JMSTLSConfig {
  ca_cert_secret?: string
  trust_store_ref?: string
  insecure_skip_verify?: boolean
}

export interface SAPGUIConfig {
  connection_string?: string
  client?: string
  language?: string
  username?: string
  password?: string
  auto_logon?: boolean
  session_behavior?: string
  sync_timeout_ms?: number
}

export interface KafkaTLSConfig {
  insecure_skip_verify?: boolean
  client_cert_file?: string
  client_key_file?: string
  ca_cert_file?: string
}

export interface KafkaConfig {
  brokers?: string[]
  profile?: string
  security_protocol?: string
  sasl_mechanism?: string
  sasl_username?: string
  sasl_password?: string
  sasl_password_secret?: string
  oauth_scope?: string
  tls?: KafkaTLSConfig
  client_id?: string
  consumer_group?: string
  topic?: string
  acks?: string
  idempotence?: string
  transactional_id?: string
  compression?: string
  partition_strategy?: string
  auto_offset_reset?: string
  isolation_level?: string
  partitions?: number
  timeout_ms?: number
  max_message_bytes?: number
  serialization?: string
  schema_registry_url?: string
  subject?: string
  schema?: string
  schema_id?: number
  properties?: Record<string, string>
  mock?: boolean
}

export interface EventHubsConfig {
  observe_partition_tail?: boolean
  tail_poll_interval_ms?: number
  processor_max_partitions?: number
  owner_level?: number | null
  consumer_mode?: string
  checkpoint_container_url?: string
  namespace?: string
  event_hub?: string
  connection_string?: string
  connection_string_secret?: string
  consumer_group?: string
  partition_key?: string
  partition_id?: string
  start_position?: string
  auth_type?: string
  timeout_ms?: number
  mock?: boolean
}

export interface DatabaseConfig {
  driver?: string
  dsn?: string
  dsn_secret?: string
  query_timeout_ms?: number
  max_open_conns?: number
  max_idle_conns?: number
  conn_max_lifetime_ms?: number
  max_rows?: number
  read_only?: boolean
  rollback_on_teardown?: boolean
}

export interface NetworkConfig {
  subprotocol?: string
  host?: string
  address?: string
  port?: number
  timeout_ms?: number
  tls?: boolean
  username?: string
  password?: string
  from?: string
  to?: string[]
  base_dn?: string
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

export interface JavaVuserSourceManifestFile {
  path: string
  bytes: number
  sha256: string
}

export interface JavaVuserSourceManifest {
  contract: string
  digest: string
  file_count: number
  total_bytes: number
  files?: JavaVuserSourceManifestFile[]
}

export interface SourceReviewFile {
  path: string
  language?: string
  content: string
  original_bytes?: number
  truncated?: boolean
  redacted?: boolean
}

export interface SourceReviewMapping {
  id: string
  source_file: string
  start_line: number
  end_line?: number
  function?: string
  target_path?: string
  target_name?: string
  target_kind?: string
  status: string
  support_mode?: string
  notes?: string
}

export interface SourceReview {
  /** Nonempty domains describe historical source paths, not current DSL bindings. */
  target_domain?: string
  source_files?: SourceReviewFile[]
  mappings?: SourceReviewMapping[]
  total_source_files?: number
  omitted_source_files?: string[]
}

export type SourceArchiveReviewFile = SourceReviewFile
export type SourceArchiveReviewMapping = SourceReviewMapping
export type SourceArchiveReview = SourceReview

export interface ScriptMetadata {
  import?: ScriptImportMetadata
  runtime_policy?: ScriptRuntimePolicy
}

export interface ScriptRuntimePolicy {
  allowed_target_hosts?: string[]
}

export type ScriptGoalConfig = Pick<GoalConfig,
  | 'target_average_tps'
  | 'p95_latency_ms'
  | 'max_error_pct'
  | 'max_avg_server_processing_ms'
  | 'max_avg_transfer_ms'
  | 'max_error_count'
  | 'min_vusers'
  | 'max_vusers'
  | 'stop_on_sla_breach'
  | 'stop_on_max_errors'
  | 'sla_breach_window'
>

export interface ScriptConfig {
  jmeter?: {
    profile: string
    source_plan_id: string
    source_group_id: string
    timezone?: string
    context_confirmed?: boolean
  }
  sampler_schedules?: Array<{
    id: string
    segments: Array<{ start_rps: number; end_rps: number; duration_seconds: number }>
  }>
  base_url: string
  think_time?: ThinkTimeConfig
  pacing?: PacingConfig
  run_logic?: RunLogicConfig
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  http?: HTTPConfig
  web_runtime?: WebRuntimeConfig
  java_http?: JavaHTTPConfig
  java_vuser?: JavaVuserConfig
  soap?: SOAPConfig
  websocket?: WebSocketConfig
  grpc?: GRPCConfig
  database?: DatabaseConfig
  network?: NetworkConfig
  browser?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  jms?: JMSConfig
  sapgui?: SAPGUIConfig
  kafka?: KafkaConfig
  eventhubs?: EventHubsConfig
	bruno?: BrunoConfig
	observability?: ObservabilityConfig
	results?: ScriptResultsConfig
}

export interface BrunoConfig {
  bundle_ref?: string
  bundle_sha256?: string
  manifest_sha256?: string
  source_format?: string
  source_revision?: string
  compatibility_version?: string
  dependency_snapshot_ref?: string
  default_environment?: string
  default_global_environment?: string
  sandbox?: 'safe' | 'developer' | string
  runner_profile?: string
  persist_session_across_iterations?: boolean
  limits?: {
    max_jumps?: number
    max_run_request_depth?: number
    max_parallel_requests?: number
    max_output_bytes?: number
    default_stream_timeout_ms?: number
  }
  secret_bindings?: Record<string, string>
}

export interface WebSocketConfig {
  callback_interval_ms?: number
  read_timeout_ms?: number
  write_timeout_ms?: number
  max_message_size?: number
  subprotocol?: string
  origin?: string
  compression?: boolean
  reconnect?: boolean
}

export interface GRPCConfig {
  proto_files?: string[]
  use_reflection?: boolean
  plain_text?: boolean
  max_recv_msg_size?: number
  timeout_ms?: number
  metadata?: Record<string, string>
  authority?: string
}

export interface ScriptDataSource {
  name: string
  type: string
  path?: string
  inline_data?: string
  encoding?: string
  file_name?: string
  content_type?: string
  storage_ref?: string
  staged_asset?: string
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
  same_line_as?: string
  data_source?: string
  column?: string
  start_row?: number
  auto_allocate?: boolean
  block_size?: number
  value_for_each_vuser?: boolean
  format?: string
  format_dialect?: 'java_simple_date_format' | 'loadrunner_strftime'
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
  archived_at?: string
  id?: string
  ref_key?: string
  name: string
  version: string
  protocol: string
  goal_config?: ScriptGoalConfig | null
  project_id?: string
  folder_id?: string | null
  generated_by?: string
  generated_from?: string
  metadata?: ScriptMetadata
  config: ScriptConfig
  init?: ScriptActionDefinition[]
  actions: ScriptActionDefinition[]
  teardown?: ScriptActionDefinition[]
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

export interface ScriptResourceSnapshot {
  request_url: string
  request_headers?: Record<string, string>
  response_status: number
  response_headers?: Record<string, string>
  response_body?: string
  response_body_state: string
  cache_hit?: boolean
  failure_kind?: string
  truncated?: boolean
}

export interface ScriptReplaySnapshot {
  resources?: ScriptResourceSnapshot[]
  omitted_resources?: number
  response_body_state?: 'captured' | 'partial' | 'empty' | 'redacted' | 'not_captured' | 'expired'
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

export interface ScriptProtocolEvidence {
  protocol?: string
  operation?: string
  target?: string
  status?: string
  timings?: Record<string, unknown>
  messages?: Record<string, unknown>
  metadata?: Record<string, unknown>
  rows?: Record<string, unknown>
  records?: Record<string, unknown>
  transcript?: unknown[]
  [key: string]: unknown
}

export interface ScriptReplayActionEvent {
  response_body_state?: string
  event_id?: string
  action_index?: number
  event_index?: number
  phase: string
  action_name: string
  action_type: string
  source_profile?: string
  source_step_id?: string
  source_step_index?: number
  source_action?: string
  source_action_name?: string
  source_transaction?: string
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
  protocol_evidence?: ScriptProtocolEvidence
  diagnosis?: {
    category: string
    severity: string
    cause: string
    evidence?: string
    action?: string
    signals?: string[]
    frame_context?: string
    suggested_locator?: string
  }
  repair_suggestions?: Array<{
    id: string
    title: string
    description: string
    draft_patch_type: string
    action?: string
    confidence?: number
    fields?: Record<string, string>
  }>
  parent_action_name?: string
  request_index?: number
  step_path?: string[]
}

export interface ScriptReplayReport {
  replay_session_id?: string
  script_hash?: string
  script_revision?: string
  artifact_manifest?: Record<string, unknown>
  diagnosis_summary?: {
    failure_count: number
    categories?: Record<string, number>
    first_failure_event_id?: string
    first_failure_action?: string
    first_failure_cause?: string
  }
  repair_suggestions?: ScriptReplayActionEvent['repair_suggestions']
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
  preflight?: Record<string, unknown>
  warnings?: string[]
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

export interface DataSourceFitItem {
  data_source?: string
  parameter?: string
  column?: string
  status: string
  required_rows?: number
  available_rows?: number
  summary: string
}

export interface DataSourceFitReport {
  status: string
  status_counts?: Record<string, number>
  items: DataSourceFitItem[]
}

export interface AIGuardrailItem {
  key: string
  status: string
  summary: string
}

export interface AIGuardrailReport {
  status: string
  status_counts?: Record<string, number>
  items: AIGuardrailItem[]
}

export interface ScriptValidationAuthoringHealth {
  score: number
  status: string
  inputs?: Array<{
    key: string
    status: string
    count?: number
    summary?: string
  }>
}

export interface ReplayPreflightCheck {
  id: string
  label: string
  status: 'pass' | 'warn' | 'fail' | string
  detail?: string
}

export interface ReplayPreflight {
  valid: boolean
  action_count: number
  expected_action_count: number
  data_source_count: number
  parameter_count: number
  browser_required?: boolean
  missing_variables?: string[]
  required_secrets?: string[]
  missing_secrets?: string[]
  runtime_prerequisites?: string[]
  issues?: string[]
  warnings?: string[]
  checks?: ReplayPreflightCheck[]
}

export interface ScriptValidationResponse {
  script_id: string
  valid: boolean
  issues: string[]
  warnings?: string[]
  preflight?: ReplayPreflight
  runtime_settings_parity?: ScriptValidationRuntimeSettingParity[]
  runtime_settings_status_counts?: Record<string, number>
  migration_readiness?: ScriptValidationMigrationReadiness
  data_source_fit?: DataSourceFitReport
  ai_guardrails?: AIGuardrailReport
  authoring_health?: ScriptValidationAuthoringHealth
}

export interface ScriptExportResponse {
  exported: boolean
  path: string
  filename: string
  size: number
}
