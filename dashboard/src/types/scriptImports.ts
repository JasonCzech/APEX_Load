import { type StoredScenario, type StoredScript } from '../types'
import { type ReplayPreflight, type ScriptDataSource, type SourceReview, type ScriptDefinition } from './scripts'

export interface SupportMatrix {
  supported: string[]
  partial: string[]
  unsupported: string[]
  notes?: string[]
}

export interface AuthoringImportMode {
  id: string
  label: string
  description: string
  default: boolean
}

export interface AuthoringImportSurface {
  id: string
  title: string
  description: string
  badge: string
  category: string
  order: number
  launcher: string
  visible: boolean
  modes?: AuthoringImportMode[]
}

export interface AuthoringCapabilitiesResponse {
  contract_version: string
  draft_lifecycle: string[]
  health_inputs: string[]
  sources: unknown[]
  source_map: Record<string, unknown>
  import_surfaces?: AuthoringImportSurface[]
}

export interface ImportReviewSection {
  key: string
  title: string
  severity: 'info' | 'warning' | 'blocker' | string
  summary: string
  items: string[]
}

export interface ImportRepairSuggestion {
  id: string
  category: string
  severity: 'info' | 'warning' | 'blocker' | string
  title: string
  description: string
  action: string
  target_action?: string
  selector_stability_score?: number
  selector_signals?: string[]
}

export type ParameterizationKind = 'correlation' | 'data_param' | 'secret'

export interface ParameterizationLocation {
  section: string
  action_index: number
  action_name?: string
  field: string
  search_text: string
  replace_text: string
}

export interface ParameterizationSuggestion {
  id: string
  kind: ParameterizationKind | string
  script_name?: string
  var_name: string
  replacement: string
  literal_value: string
  sample_value: string
  locations?: ParameterizationLocation[]
  confidence: number
  rule_id: string
  rationale: string
  auto_apply: boolean
  high_risk: boolean
  pre_applied?: boolean
}

export interface ParameterizationSummary {
  total: number
  by_kind: Record<string, number>
  auto_apply_count: number
  review_count: number
  high_risk_count: number
  pre_applied_count: number
}

export interface Parameterization {
  suggestions: ParameterizationSuggestion[]
  summary: ParameterizationSummary
}

export interface ParameterizationDiff {
  suggestion_id?: string
  kind: string
  path: string
  before?: string
  after?: string
}

export interface ApplyParameterizationRequest {
  script: ScriptDefinition
  suggestions: ParameterizationSuggestion[]
  selected_ids: string[]
  confirm_high_risk?: boolean
}

export interface ApplyParameterizationResponse {
  script: ScriptDefinition
  diffs: ParameterizationDiff[]
  applied_count: number
  blocked?: boolean
  blocked_reason?: string
  warnings?: string[]
}

export interface ScriptImportResult<
  TStats = Record<string, unknown>,
  TCorrelation = unknown,
  TScript extends ScriptDefinition = ScriptDefinition,
  TScenarioDraft = unknown,
  TSummary = Record<string, unknown>,
> {
  scripts: TScript[]
  script?: TScript
  scenario_draft?: TScenarioDraft
  warnings: string[]
  unsupported_elements: string[]
  assumptions: string[]
  review_sections: ImportReviewSection[]
  repair_suggestions: ImportRepairSuggestion[]
  correlations: TCorrelation[]
  stats: TStats
  summary: TSummary
  detected_protocol?: DetectedSourceProtocol
  fidelity_report?: EnterpriseArchiveFidelityReport
  review_artifact?: EnterpriseArchiveReviewArtifact
  source_review?: SourceReview
  trueclient2_compat_report?: TrueClient2CompatReport
  parameterization?: Parameterization
  preflight?: ReplayPreflight
}

export interface ImportAssetUploadResponse {
  import_id: string
  name: string
  file_name: string
  content_type?: string
  size_bytes: number
  sha256: string
  storage_ref: string
  data_source: ScriptDataSource
  uploaded_at: string
}

export interface DetectedSourceProtocol {
  canonical_source_protocol: string
  apexload_protocol?: string
  bundle?: string
  import_mode: 'executable' | 'review_only' | 'external_runner_required' | 'unsupported' | string
  confidence: 'high' | 'medium' | 'low' | string
  display_name?: string
  source_type?: string
  signals?: string[]
  conflicts?: string[]
  warnings?: string[]
  required_evidence?: string[]
  runner_requirement?: string
}

export interface EnterpriseArchiveReviewArtifact {
  id: string
  title: string
  status: string
  protocol: DetectedSourceProtocol
  summary: string
  source_files?: string[]
  preserved_functions?: string[]
  required_evidence?: string[]
  next_actions?: string[]
  metadata?: Record<string, unknown>
}

export interface EnterpriseArchiveFidelitySourceFile {
  path: string
  phase?: string
  language?: string
  role?: string
  bytes?: number
}

export interface EnterpriseArchiveFidelityReport {
  source_files?: EnterpriseArchiveFidelitySourceFile[]
  expected_functions?: number
  converted_functions?: number
  converted_actions?: number
  dropped_bodies?: number
  secret_gated?: number
  runtime_parity?: Record<string, number>
  parameter_parity?: Record<string, number>
  promotion_status?: string
  blockers?: string[]
  warnings?: string[]
}

export interface EnterpriseArchiveConvertStats {
  total_functions: number
  converted_actions: number
  logical_actions?: number
  raw_request_actions?: number
  collapsed_requests?: number
  transaction_markers?: number
  contributor_functions?: number
  extractors: number
  think_times: number
  transactions: number
  skipped: number
  unsupported_funcs?: string[]
}

export interface EnterpriseArchiveImportSummary {
  project_file?: string
  mapped_runtime_file?: string
  project_sections?: string[]
  runtime_files?: string[]
  parameter_files?: string[]
  runtime_settings_parity?: EnterpriseArchiveRuntimeSettingParity[]
  runtime_settings_status_counts?: Record<string, number>
  migration_readiness?: EnterpriseArchiveMigrationReadiness
  detected_protocol?: DetectedSourceProtocol
  fidelity_report?: EnterpriseArchiveFidelityReport
  review_artifact?: EnterpriseArchiveReviewArtifact
  source_review?: SourceReview
  salesforce_review?: SalesforceReview
  trueclient2_compat_report?: TrueClient2CompatReport
  import_mode?: string
  unsupported_function_count: number
}

export interface TrueClient2CompatReport {
  source_script_type?: string
  source_profile?: string
  import_mode?: string
  promotion_status?: string
  promotion_allowed?: boolean
  imported_steps?: number
  converted_actions?: number
  skipped_review_steps?: number
  supported_step_families?: string[]
  review_only_step_families?: string[]
  unsupported_step_families?: string[]
  selector_confidence_pct?: number
  selector_candidate_count?: number
  runtime_parity_status?: string
  replay_status?: string
  javascript_review_gate?: string
  frame_window_blockers?: number
  http_conversion_readiness?: string
  fixture_contract_status?: string
  findings?: string[]
  next_actions?: string[]
}

export interface SalesforceDomainObservation {
  host: string
  kind: string
  count: number
}

export interface SalesforceEndpointObservation {
  method?: string
  path: string
  host?: string
  count: number
}

export interface SalesforceTransactionCandidate {
  name: string
  source: string
  request_count: number
  aura_actions?: number
  ui_api_calls?: number
  flow_kinds?: string[]
}

export interface SalesforceHARMatchCoverage {
  har_provided: boolean
  imported_requests: number
  matched_requests: number
  coverage_pct: number
}

export interface SalesforceCorrelationCandidate {
  name: string
  source: string
  expression: string
  found_in_seq: number
  used_in_seqs?: number[]
  rule_id?: string
  confidence?: number
  reasons?: string[]
}

export interface SalesforceNativePreview {
  script?: ScriptDefinition
  correlations?: SalesforceCorrelationCandidate[]
}

export interface SalesforceParameterizationCandidate {
  name: string
  source: string
  expression: string
  action_seq?: number
  param_key: string
  sample_value?: string
  kind: string
  column: string
  template: string
}

export interface SalesforceReview {
  detected: boolean
  domains?: SalesforceDomainObservation[]
  aura_endpoints?: SalesforceEndpointObservation[]
  lightning_endpoints?: SalesforceEndpointObservation[]
  login_flow?: SalesforceEndpointObservation[]
  ui_api_endpoints?: SalesforceEndpointObservation[]
  rest_endpoints?: SalesforceEndpointObservation[]
  apex_rest_endpoints?: SalesforceEndpointObservation[]
  composite_endpoints?: SalesforceEndpointObservation[]
  graphql_endpoints?: SalesforceEndpointObservation[]
  bulk_endpoints?: SalesforceEndpointObservation[]
  agentforce_endpoints?: SalesforceEndpointObservation[]
  transactions?: SalesforceTransactionCandidate[]
  parameterizations?: SalesforceParameterizationCandidate[]
  correlation_confidence?: string
  har_match_coverage: SalesforceHARMatchCoverage
  blockers?: string[]
  next_actions?: string[]
  native_preview?: SalesforceNativePreview
}

export interface MulesoftDomainObservation {
  host: string
  kind: string
  count: number
}

export interface MulesoftPolicyFinding {
  policy: string
  detail: string
  evidence?: string
}

export interface MulesoftAuthFinding {
  type: string
  detail: string
}

export interface MulesoftReview {
  detected: boolean
  domains?: MulesoftDomainObservation[]
  policy_findings?: MulesoftPolicyFinding[]
  auth_findings?: MulesoftAuthFinding[]
  blockers?: string[]
  next_actions?: string[]
}

export interface AgentforceLifecycleStep {
  step: string
  action_name?: string
  method?: string
  path?: string
}

export interface AgentforceSessionVar {
  name: string
  source: string
  type: string
  expression: string
  detail?: string
}

export interface AgentforceAssertionHint {
  step: string
  detail: string
}

export interface AgentforceReview {
  detected: boolean
  streaming?: boolean
  lifecycle?: AgentforceLifecycleStep[]
  session_vars?: AgentforceSessionVar[]
  assertion_hints?: AgentforceAssertionHint[]
  blockers?: string[]
  next_actions?: string[]
}

// AgentforceTransformChange records one replay-readiness rewrite applied by the
// opt-in Agentforce transform, preserved on the script under the
// `agentforce_transform` import metadata key.
export interface AgentforceTransformChange {
  step: string
  action_name?: string
  detail: string
}

export interface EnterpriseArchiveProtocolSupportEntry {
  id: string
  bundle?: string
  bundle_id?: string
  bundle_name?: string
  protocol?: string
  display_name?: string
  status?: string
  execution_posture?: string
  apexload_runtime?: string
  import_mode?: string
  runner_class?: string
  runner_requirement?: string
  recommended_workflow?: string
  notes?: string
}

export interface EnterpriseArchiveSupportMatrixEntry {
  id: string
  area: string
  concept: string
  fields?: string[]
  status: string
  notes?: string
}

export interface EnterpriseArchiveRuntimeSettingParity extends EnterpriseArchiveSupportMatrixEntry {
  runtime_files?: string[]
}

export interface EnterpriseArchiveMigrationPolicy {
  block_statuses?: string[]
}

export interface EnterpriseArchiveMigrationReadiness {
  score_pct: number
  blocked: boolean
  summary?: string
  blocking_statuses?: string[]
  blocking_settings?: EnterpriseArchiveRuntimeSettingParity[]
  policy?: EnterpriseArchiveMigrationPolicy
}

export interface EnterpriseArchiveSupportMatrixExample {
  title: string
  coverage: string
  test: string
}

export interface JMeterConvertStats {
  thread_groups: number
  samplers: number
  total_samplers?: number
  skipped_samplers?: number
  converted_actions: number
  headers: number
  variables: number
  csv_sources: number
  assertions: number
  timers: number
  unsupported: number
  mapped?: number
  review_required?: number
  adapter_required?: number
}

export interface JMeterImportSummary {
  file_name?: string
  thread_group_count: number
  sampler_count: number
  variables_discovered?: string[]
  csv_sources_discovered?: string[]
}

export interface JMeterScenarioDraftGroup {
  name: string
  script_name: string
  vusers: number
  ramp_up?: string
  initialization: string
}

export interface JMeterScenarioDraft {
  name: string
  duration: string
  scenario_mode: string
  groups: JMeterScenarioDraftGroup[]
  metadata?: Record<string, unknown>
}

export interface SampleImportStats {
  format: string
  total_requests: number
  converted_actions: number
  filtered_assets?: number
  assertions: number
  extractors: number
  correlations_detected: number
}

export interface DevWebImportStats {
  total_steps: number
  converted_actions: number
  http_requests: number
  websocket_actions: number
  grpc_calls: number
  think_times: number
  skipped: number
  unsupported_calls?: string[]
}

export type HARImportStats = SampleImportStats

export type HARArtifactMode = 'design_har' | 'design_har_pair' | 'runtime_canary_har'

export interface HARArtifactGovernance {
  artifact_mode: HARArtifactMode | string
  sensitivity: string
  retention_days: number
  redaction_enabled: boolean
  body_capture_required: boolean
  scope: string
  warnings?: string[]
}

export interface HARDiffClassification {
  name: string
  source?: string
  expression?: string
  extract_type?: string
  sample_value?: string
  baseline_value?: string
  comparison_value?: string
  found_in_step?: number
  used_in_steps?: number[]
  classification: 'dynamic' | 'static' | 'candidate' | string
  diff_confidence: number
  reason: string
}

export interface HARDiffReport {
  artifact_mode: HARArtifactMode | string
  compared: boolean
  baseline_correlations: number
  comparison_correlations: number
  dynamic_candidates: number
  static_candidates: number
  unmatched_candidates: number
  classifications?: HARDiffClassification[]
  warnings?: string[]
}

export interface HARImportSummary {
  artifact_mode: HARArtifactMode | string
  artifact_sensitivity?: string
  retention_days?: number
  governance?: HARArtifactGovernance
  har_diff?: HARDiffReport
  salesforce_review?: SalesforceReview
  generation_mode?: string
  protocol_scope?: string
  llm_role?: string
  non_http_protocols?: string[]
}

export interface OpenAPIImportStats {
  format: string
  openapi_version?: string
  total_paths: number
  total_operations: number
  converted_actions: number
  request_bodies: number
  assertions: number
  extractors?: number
  correlations?: number
  parameters_detected: number
  auth_schemes_detected?: string[]
  skipped_operations?: string[]
}

export interface OpenAPICorrelation {
  name: string
  source: string
  expression: string
  extract_type: string
  sample_value?: string
  found_in_step: number
  used_in_steps: number[]
}

export interface PostmanImportStats {
  format: string
  collection_version?: string
  folders: number
  total_requests: number
  converted_actions: number
  variables_detected: number
  environment_values?: number
  auth_schemes?: string[]
  unsupported_bodies: number
  assertions: number
  skipped_items?: string[]
}

export interface WSDLImportStats {
  format: string
  services: number
  port_types: number
  bindings: number
  operations_detected: number
  converted_actions: number
  soap_actions_detected?: string[]
}

export interface AsyncAPIImportStats {
  format: string
  asyncapi_version?: string
  total_channels: number
  total_operations: number
  converted_actions: number
  produce_actions: number
  consume_actions: number
  request_reply_actions: number
  messages_with_correlation: number
  server_protocols?: string[]
  brokers?: string[]
  schema_formats_detected?: string[]
  skipped_operations?: string[]
}

export interface SampleCorrelation {
  name: string
  source: string
  expression: string
  sample_value: string
  found_in_step: number
  used_in_steps: number[]
}

export interface RecorderCorrelation {
  name: string
  source: string
  expression: string
  extract_type: string
  found_in_seq: number
  used_in_seqs: number[]
  sample_value: string
  rule_id?: string
  confidence?: number
  reasons?: string[]
}

export type EnterpriseArchiveImportResult = ScriptImportResult<EnterpriseArchiveConvertStats, unknown, ScriptDefinition, unknown, EnterpriseArchiveImportSummary>
export type JMeterImportResult = ScriptImportResult<JMeterConvertStats, unknown, ScriptDefinition, JMeterScenarioDraft, JMeterImportSummary>
export type SampleImportResult = ScriptImportResult<SampleImportStats, SampleCorrelation>
export type DevWebImportResult = ScriptImportResult<DevWebImportStats, unknown>
export type HARImportResult = ScriptImportResult<HARImportStats, SampleCorrelation, ScriptDefinition, unknown, HARImportSummary>
export type SalesforceImportResult = ScriptImportResult<HARImportStats, SalesforceCorrelationCandidate, ScriptDefinition, unknown, HARImportSummary & { salesforce_review?: SalesforceReview }>
export type OpenAPIImportResult = ScriptImportResult<OpenAPIImportStats, OpenAPICorrelation>
export type PostmanImportResult = ScriptImportResult<PostmanImportStats, unknown>
export type WSDLImportResult = ScriptImportResult<WSDLImportStats, unknown>
export type AsyncAPIImportResult = ScriptImportResult<AsyncAPIImportStats, unknown>
export type RecorderImportResult = ScriptImportResult<Record<string, unknown>, RecorderCorrelation>
export type K6ImportResult = ScriptImportResult<Record<string, unknown>, unknown>
export type EventHubsCaptureImportResult = ScriptImportResult<Record<string, unknown>, unknown>
export type BrokerConfigImportResult = ScriptImportResult<Record<string, unknown>, unknown>

export interface RAMLImportStats {
  format: string
  total_resources: number
  total_methods: number
  converted_actions: number
  request_bodies: number
  auth_schemes_detected?: string[]
}

export interface RAMLPolicyHint {
  policy: string
  detail: string
  scheme?: string
}

export interface RAMLImportSummary {
  title?: string
  version?: string
  base_url?: string
  resources?: string[]
  auth_schemes?: string[]
  policies?: RAMLPolicyHint[]
}

export interface RAMLSupportMatrixEntry {
  id: string
  area: string
  construct: string
  status: string
  execution_status?: string
  review_required?: boolean
  fields?: string[]
  notes?: string
}

export interface RAMLSupportMatrixResponse {
  support_matrix?: RAMLSupportMatrixEntry[]
  notes?: string[]
}

export type OpenAPISupportMatrixEntry = RAMLSupportMatrixEntry
export type AsyncAPISupportMatrixEntry = RAMLSupportMatrixEntry

export interface OpenAPISupportMatrixResponse {
  support_matrix?: OpenAPISupportMatrixEntry[]
  notes?: string[]
}

export interface AsyncAPISupportMatrixResponse {
  support_matrix?: AsyncAPISupportMatrixEntry[]
  notes?: string[]
}

export type DevWebSupportMatrixEntry = RAMLSupportMatrixEntry
export type PostmanSupportMatrixEntry = RAMLSupportMatrixEntry
export type K6SupportMatrixEntry = RAMLSupportMatrixEntry
export type EventHubsCaptureSupportMatrixEntry = RAMLSupportMatrixEntry
export type BrokerConfigSupportMatrixEntry = RAMLSupportMatrixEntry

export interface DevWebSupportMatrixResponse {
  support_matrix?: DevWebSupportMatrixEntry[]
  notes?: string[]
}

export interface PostmanSupportMatrixResponse {
  support_matrix?: PostmanSupportMatrixEntry[]
  notes?: string[]
}

export interface K6SupportMatrixResponse {
  support_matrix?: K6SupportMatrixEntry[]
  notes?: string[]
}

export interface EventHubsCaptureSupportMatrixResponse {
  support_matrix?: EventHubsCaptureSupportMatrixEntry[]
  notes?: string[]
}

export interface BrokerConfigSupportMatrixResponse {
  support_matrix?: BrokerConfigSupportMatrixEntry[]
  notes?: string[]
}

export type RAMLImportResult = ScriptImportResult<RAMLImportStats, unknown, ScriptDefinition, unknown, RAMLImportSummary>

export interface BrowserRecordingImportStats {
  format: string
  total_steps: number
  converted_actions: number
  skipped_steps: number
}

export type BrowserRecordingImportResult = ScriptImportResult<BrowserRecordingImportStats, unknown>

export interface JMeterSupportMatrixEntry {
  id: string
  area: string
  construct: string
  status: string
  execution_status: string
  review_required?: boolean
  fields?: string[]
  notes?: string
}

export interface JMeterSupportMatrixResponse {
  support_matrix?: SupportMatrix
  support_matrix_entries?: JMeterSupportMatrixEntry[]
  notes?: string[]
}

export interface EnterpriseArchiveSupportMatrixResponse {
  support_matrix?: EnterpriseArchiveSupportMatrixEntry[]
  notes?: string[]
  tested_examples?: EnterpriseArchiveSupportMatrixExample[]
  protocol_support_matrix?: EnterpriseArchiveProtocolSupportEntry[]
}

export interface OracleEBusinessReviewFile {
  name?: string
  content?: string
}

export interface OracleEBusinessReviewRequest {
  project_id?: string
  name?: string
  package?: string
  source_text?: string
  files?: OracleEBusinessReviewFile[]
  script?: ScriptDefinition
  metadata?: Record<string, unknown>
  options?: {
    require_replay_evidence?: boolean
    block_nca_execution?: boolean
  }
}

export interface OracleEBusinessReviewResponse {
  package: string
  status: string
  project_id?: string
  name?: string
  source_summary?: {
    files?: number
    bytes?: number
    actions?: number
    detected_hosts?: string[]
    source_profiles?: string[]
  }
  review_sections?: ImportReviewSection[]
  repair_suggestions?: ImportRepairSuggestion[]
  findings?: Array<{ id: string; severity: string; category: string; summary: string; detail?: string; evidence?: string[] }>
  next_actions?: string[]
  required_evidence?: string[]
  support_matrix?: RAMLSupportMatrixEntry[]
  scenario_templates?: Array<{ id: string; package: string; name: string; description: string; flow?: string[]; parameters?: string[]; evidence?: string[] }>
  execution_posture?: {
    posture: string
    runner_requirement: string
    can_execute_natively: boolean
    blocks_execution: boolean
    reasons?: string[]
  }
}

export interface OracleEBusinessSupportMatrixResponse {
  support_matrix?: RAMLSupportMatrixEntry[]
  notes?: string[]
  correlation_packs?: unknown[]
  scenario_templates?: OracleEBusinessReviewResponse['scenario_templates']
}

export interface SaveJMeterImportRequest {
  scripts: ScriptDefinition[]
  scenario_draft?: JMeterScenarioDraft
}

export interface SaveJMeterImportResponse {
  scripts?: StoredScript[]
  count?: number
  scenario?: StoredScenario
}

export interface SavePostmanImportRequest {
  script: ScriptDefinition
  mode?: 'create' | 'replace'
  target_script_id?: string
}

export interface SavePostmanImportResponse {
  scripts?: StoredScript[]
  script?: ScriptDefinition
  stored_script?: StoredScript
  replaced?: boolean
  preflight?: ReplayPreflight
}

export interface OpenAPIImportDiff {
  mode: 'create' | 'replace' | string
  replace_mode?: 'full' | 'merge_by_operation_id' | string
  added: number
  updated: number
  removed: number
  preserved: number
  items?: string[]
}

export interface CommitOpenAPIImportRequest {
  script: ScriptDefinition
  mode?: 'create' | 'replace'
  target_script_id?: string
  replace_mode?: 'full' | 'merge_by_operation_id'
  dry_run?: boolean
}

export interface CommitOpenAPIImportResponse {
  scripts?: StoredScript[]
  script?: ScriptDefinition
  stored_script?: StoredScript
  replaced?: boolean
  dry_run?: boolean
  diff?: OpenAPIImportDiff
  preflight?: ReplayPreflight
}

export type CommitAsyncAPIImportRequest = CommitOpenAPIImportRequest
export type CommitAsyncAPIImportResponse = CommitOpenAPIImportResponse
