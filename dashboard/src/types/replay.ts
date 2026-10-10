import type { ClientMetrics } from './metrics'
import type { RedactionRule } from './runtimeSettings'
import type { BrowserConfig, HTTPActionVersion, ReplayPreflight, ScriptDefinition } from './scripts'
import type { JsonRequestBody } from './generated/openapi'

/** Transport and view contracts for browser replay and script debugging. */
export interface BrowserReplayConsoleEntry {
  level?: string
  text?: string
  source?: string
  timestamp?: string
}

export interface BrowserReplayNetworkEntry {
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  duration_ms?: number
  bytes_received?: number
}

export interface BrowserReplayDomSummary {
  title?: string
  ready_state?: string
  visible_text?: string[]
}

export interface ReplayRuntimeProfile {
  name?: string
  capture_mode?: string
  log_level?: string
  ignore_think_time?: boolean
  browser_engine?: string
  device_profile?: string
  headless?: boolean
  failure_policy?: string
  experience_canary?: boolean
  har_capture_policy?: string
}

export interface ReplayArtifactManifest {
  replay_id?: string
  script_hash?: string
  script_revision?: string
  created_at?: string
  runtime_profile?: ReplayRuntimeProfile
  data_sources?: string[]
  seed_variables?: string[]
  screenshots?: number
  network_entries?: number
  console_entries?: number
  snapshots?: number
  html_captured?: boolean
  browser_evidence?: boolean
  protocol_artifacts?: Record<string, number>
}

export interface ProtocolEvidence {
  response_content_state?: 'complete' | 'collection_window' | 'unavailable'
  response_content_basis?: 'decoded' | 'identity'
  response_content_source?: 'wire' | 'cache'
  http_version_policy?: HTTPActionVersion
  http_version_negotiated?: '1.1' | '2' | '3'
  /** Failed HTTP/3 request: whether it may have reached the target. */
  http3_failure_certainty?: 'not_sent' | 'possibly_sent' | 'rejected_unprocessed'
  http_version_source?: 'wire' | 'cache'
  alt_svc_h3_advertised?: boolean
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

export interface ReplayDiagnosis {
  category: string
  severity: string
  cause: string
  evidence?: string
  action?: string
  signals?: string[]
  frame_context?: string
  suggested_locator?: string
}

export interface ReplayRepairSuggestion {
  id: string
  title: string
  description: string
  draft_patch_type: string
  action?: string
  confidence?: number
  fields?: Record<string, string>
}

export interface ReplayDiagnosisSummary {
  failure_count: number
  categories?: Record<string, number>
  first_failure_event_id?: string
  first_failure_action?: string
  first_failure_cause?: string
}

export interface BrowserReplaySnapshot {
  resources?: import('./scripts').ScriptResourceSnapshot[]
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
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
  client_metrics?: ClientMetrics
}

export interface BrowserReplayEvent {
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
  status_code?: number
  success: boolean
  error_message?: string
  started_at: string
  completed_at: string
  duration_ms: number
  extractions?: Record<string, string>
  page_url?: string
  page_title?: string
  html?: string
  screenshot_base64?: string
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
  client_metrics?: ClientMetrics
  response_body_state?: string
  snapshot?: BrowserReplaySnapshot
  protocol_evidence?: ProtocolEvidence
  diagnosis?: ReplayDiagnosis
  repair_suggestions?: ReplayRepairSuggestion[]
  // Sub-request / sub-step identity for leaves of a container action
	// (http_sequence / control_if / control_switch / control_loop). Absent for a plain action.
  parent_action_name?: string
  request_index?: number
  request_count?: number
  leaf_ordinal?: number
  step_path?: string[]
  iteration?: number
  iteration_path?: number[]
}

export interface BrowserReplayReport {
  runtime_evidence_unavailable?: boolean
  dropped_event_count?: number
  truncated_event_evidence_count?: number
  stopped?: boolean
  replay_session_id?: string
  script_hash?: string
  script_revision?: string
  runtime_profile?: ReplayRuntimeProfile
  artifact_manifest?: ReplayArtifactManifest
  diagnosis_summary?: ReplayDiagnosisSummary
  repair_suggestions?: ReplayRepairSuggestion[]
  group: string
  protocol: string
  started_at: string
  completed_at: string
  success: boolean
  error?: string
  events: BrowserReplayEvent[]
  final_variables?: Record<string, string>
}

export interface BrowserReplaySession {
  runtime_evidence_unavailable?: boolean
  dropped_event_count?: number
  id: string
  mode?: string
  script_id?: string
  project_id?: string
  status: 'queued' | 'running' | 'paused' | 'completed' | 'failed' | 'stopped'
  group: string
  protocol: string
  runtime_profile?: ReplayRuntimeProfile
  preflight?: ReplayPreflight
  started_at?: string
  completed_at?: string
  current_event_index: number
  current_event?: BrowserReplayEvent
  events?: BrowserReplayEvent[]
  report?: BrowserReplayReport
  error?: string
}

export interface BrowserReplayStartRequest {
  group_name?: string
  project_id?: string
  replay_session_id?: string
  variables?: Record<string, string>
  redaction_rules?: RedactionRule[]
  start_paused?: boolean
  breakpoints?: number[]
  browser?: BrowserConfig
  ignore_think_time?: boolean
  /** Record a native HAR of the replay (opt-in; recording runs under the
   * HAR size and memory limits). Otherwise the replay serves the native HAR
   * its script's own capture policy recorded, if any, or else the synthetic
   * HAR built from the report's network entries. */
  record_har?: boolean
  script?: ScriptDefinition
}

export interface BrowserReplayStartResponse {
  script_id: string
  replay_session_id: string
  session: BrowserReplaySession
  preflight?: ReplayPreflight
  warnings?: string[]
}

export interface BrowserReplaySessionResponse {
  session: BrowserReplaySession
  preflight?: ReplayPreflight
  warnings?: string[]
}

export type ScriptDebugLogLevel = 'error' | 'warn' | 'info' | 'debug' | 'trace'

export interface ScriptDebugBreakpoint {
  disabled?: boolean
  hit_count?: number
  iteration?: number
  once?: boolean
  phase: 'init' | 'action' | 'teardown' | string
  action_index: number
  // Optional sub-request / sub-step targeting within a container action. Absent
  // = whole-action breakpoint (stops once at the first executed leaf).
  request_index?: number
  step_path?: string[]
}

export interface ScriptDebugLogEntry {
  id: string
  timestamp: string
  level: ScriptDebugLogLevel
  category: string
  message: string
  raw?: string
  phase?: string
  action_index?: number
  action_name?: string
}

export interface ScriptDebugAction {
  event_id?: string
  phase: string
  action_index: number
  event_index: number
  action_name: string
  action_type: string
  method?: string
  target?: string
  selector?: string
  source_step_id?: string
  // Sub-request / sub-step identity when paused inside a container action.
  parent_action_name?: string
  request_index?: number
  request_count?: number
  leaf_ordinal?: number
  step_path?: string[]
  iteration?: number
  iteration_path?: number[]
}

// ConditionEvidence captures the evaluated operands and outcome of a control_if,
// control_switch, or control_loop predicate so the debugger can explain routing.
export interface ConditionEvidence {
  expr?: string
  left?: string
  op?: string
  right?: string
  result: boolean
  branch?: string
}

// Step modes accepted by POST /script-debug/sessions/{id}/step.
export type ScriptDebugStepMode = 'into' | 'over' | 'loop_end'

export type DebugRuntimeUnavailableReason = 'boundary_held' | 'not_paused' | 'pause_pending' | 'snapshot_unavailable' | 'context_canceled' | 'integrity_fence_lost' | 'runtime_evidence_unavailable' | 'session_terminal' | 'session_retired' | 'revision_exhausted'

export interface ReplayRuntimeSnapshot {
  state: 'complete' | 'bounded' | 'unavailable'
  boundary: 'before_action' | 'after_action'
  location: ScriptDebugAction
  variables: Array<{ name: string; value_state: 'assigned' | 'redacted' | 'omitted'; value?: string }>
  parameters: Array<{ name: string; state: 'not_evaluated' | 'prepared' | 'observed' | 'exhausted' | 'unavailable'; update_policy: 'once' | 'each_iteration' | 'each_occurrence' | 'unavailable'; value_state: 'observed' | 'assigned' | 'redacted' | 'omitted'; value?: string; observed_event_id?: string }>
  rows: Array<{ kind: 'datasource' | 'parameter' | 'foreach'; source: string; name: string; row_ordinal?: number; selection: 'sequential' | 'random' | 'unique' | 'pop' | 'same_line' | 'foreach' | 'fixed' | 'unavailable'; state: 'selected' | 'exhausted' | 'unavailable'; observed_event_id?: string }>
  frames: Array<{ kind: 'phase' | 'http_sequence' | 'control_if' | 'control_switch' | 'control_loop' | 'foreach' | 'parallel' | 'parallel_branch'; phase: 'init' | 'action' | 'teardown'; action_index: number; step_path?: string[]; iteration_path?: number[]; branch_index?: number }>
  omissions: { variables: number; parameters: number; rows: number; frames: number; values: number }
  runtime_evidence_unavailable?: boolean
  unavailable_reason?: 'publication_unavailable' | 'work_limit'
}

export interface ScriptDebugRuntimeState {
  version: 1
  kind: 'full'
  session_id: string
  session_incarnation: string
  state_revision: string
  state: 'paused' | 'unavailable'
  reason: DebugRuntimeUnavailableReason
  snapshot: ReplayRuntimeSnapshot | null
}

export interface ScriptDebugSession {
  session_incarnation?: string
  state_revision?: string
  runtime_state?: ScriptDebugRuntimeState
  runtime_evidence_unavailable?: boolean
  dropped_event_count?: number
  id: string
  mode?: string
  script_id?: string
  project_id?: string
  status: BrowserReplaySession['status']
  group: string
  protocol: string
  log_level: ScriptDebugLogLevel
  runtime_profile?: ReplayRuntimeProfile
  preflight?: ReplayPreflight
  started_at?: string
  completed_at?: string
  current_event_index: number
  current_action?: ScriptDebugAction
  current_event?: BrowserReplayEvent
  breakpoints?: ScriptDebugBreakpoint[]
  break_on_failure?: boolean
  events?: BrowserReplayEvent[]
  logs?: ScriptDebugLogEntry[]
  raw_output?: string[]
  report?: BrowserReplayReport
  error?: string
  // Condition evidence keyed by `${phase}#${action_index}#${step_path.join('/')}`.
  condition_evidence?: Record<string, ConditionEvidence>
}

/**
 * A debug session start request: the generated transport body (stored-script
 * route), with the script typed by the DSL's own definition. The generated
 * contract carries a script's open maps (assertions, conditions, control
 * steps) as open objects, which ScriptDefinition narrows; every other field is
 * the transport's own type.
 */
export type ScriptDebugStartRequest = Omit<JsonRequestBody<'handleStartScriptDebugScript'>, 'script'> & { script?: ScriptDefinition }

export interface ScriptDebugStartResponse {
  script_id: string
  replay_session_id: string
  debug_session_id: string
  session: ScriptDebugSession
  preflight?: ReplayPreflight
  warnings?: string[]
}

export interface ScriptDebugSessionResponse {
  session: ScriptDebugSession
  preflight?: ReplayPreflight
  warnings?: string[]
}
