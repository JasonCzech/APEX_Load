import type {
  BrowserConfig,
  BrowserEmulationConfig,
  ErrorHandlingConfig,
  HTTPConfig,
  JavaVuserConfig,
  LogConfig,
  PacingConfig,
  RunLogicConfig,
  ThinkTimeConfig,
  WebRuntimeConfig,
  WebSocketConfig,
  ScriptResultsConfig,
  SOAPConfig,
} from './scripts'
import type { JsonRequestBody } from './generated/openapi'

export interface RedactionRule {
  name?: string
  pattern: string
  replacement?: string
}

export type RuntimeBrowserSettings = Pick<BrowserConfig, 'headless' | 'device_profile' | 'touch_mode' | 'orientation'>

/** Script-owned Java Env settings overridable per scenario group. Operator
 * runner fields (enablement, runner command, harness jar, work dir, capacity)
 * are intentionally excluded — they cannot be steered by a scenario. */
export type RuntimeJavaVuserSettings = Pick<
  JavaVuserConfig,
  | 'jdk_selection'
  | 'jdk_home'
  | 'additional_vm_parameters'
  | 'classpath'
  | 'use_xbootclasspath'
  | 'dedicated_class_loader'
  | 'skip_parameter_evaluation'
  | 'jvm_sharing'
  | 'vusers_per_jvm'
  | 'startup_timeout_ms'
  | 'action_timeout_ms'
  | 'param_left_brace'
  | 'param_right_brace'
>

/** The API's RuntimeSettingsOverride as the generated OpenAPI contract describes it. */
type GeneratedRuntimeSettingsOverride = NonNullable<JsonRequestBody<'handleUpdateScenario'>['runtime_settings']>
type GeneratedRuntimeSection<K extends keyof GeneratedRuntimeSettingsOverride> = NonNullable<GeneratedRuntimeSettingsOverride[K]>

/**
 * The native protocol override sections (pkg/api runtimeProtocolOverrideSections),
 * each the dsl section ScriptConfig holds under the same key. Without
 * override_fields a section replaces the script's whole section (v1); with
 * override_fields its leaves are addressable (`kafka.sasl_mechanism`,
 * `kafka.properties.KEY`). Taken from the generated OpenAPI contract so a Go
 * field change reaches these types.
 */
export type RuntimeKafkaSettings = GeneratedRuntimeSection<'kafka'>
export type RuntimeJMSSettings = GeneratedRuntimeSection<'jms'>
export type RuntimeEventHubsSettings = GeneratedRuntimeSection<'eventhubs'>
export type RuntimeGRPCSettings = GeneratedRuntimeSection<'grpc'>
export type RuntimeDatabaseSettings = GeneratedRuntimeSection<'database'>
export type RuntimeNetworkSettings = GeneratedRuntimeSection<'network'>
export type RuntimeAppiumSettings = GeneratedRuntimeSection<'appium'>
export type RuntimeJavaHTTPSettings = GeneratedRuntimeSection<'java_http'>

export interface RuntimeProtocolOverrideSections {
  kafka?: RuntimeKafkaSettings
  jms?: RuntimeJMSSettings
  eventhubs?: RuntimeEventHubsSettings
  grpc?: RuntimeGRPCSettings
  database?: RuntimeDatabaseSettings
  network?: RuntimeNetworkSettings
  appium?: RuntimeAppiumSettings
  java_http?: RuntimeJavaHTTPSettings
}

/** The protocol section keys, in the backend's order. */
export type RuntimeProtocolSectionKey = keyof RuntimeProtocolOverrideSections

/**
 * `null` on a runtime section is a run-only TOMBSTONE, never a wire value. A per-group run
 * override is overlaid back onto the group's stored runtime_settings when the create payload is
 * built (mergeRuntimeSettingsForRun), so an absent key can only mean "unchanged" — it can never
 * mean "cleared". An explicit null tells that overlay to REMOVE the stored section, which is how
 * the group dialog's inherit options ("Script timing", "Script think time", "Script default",
 * and the browser block's "Standard") stop the group pinning a section at all. The overlay
 * strips the tombstone, so no null ever reaches the API.
 *
 * The set of sections that can carry a tombstone is newTestRunModel.CLEARABLE_RUNTIME_SECTIONS,
 * and that one list is what both the clear path and the read path consume — widening one
 * without the other is what silently discarded the browser clear. `browser` and
 * `browser_config` are two wire shapes of one logical section and both carry the tombstone at
 * runtime, but they are NOT typed nullable here: dozens of read-only display sites consume them
 * and would each need null handling for a value only the run-editor patch can hold. The model
 * writes those two through stampClearedRuntimeSections and reads them back through
 * mergeRuntimeSettingsForRun, which are the only places that see the null.
 *
 * stampClearedRuntimeSections keys off the section being ABSENT from the patch, so that only
 * holds because setRuntimeBrowser removes an emptied section in BOTH shapes. It deliberately
 * does not treat a present-but-empty section as a clear: an empty `browser_config` is a
 * whole-section replace, which is the only way a group can override an ancestor's pinned
 * browser section.
 */
export interface RuntimeSettingsOverride {
  results?: ScriptResultsConfig
  iterations?: number
  pacing?: PacingConfig | null
  think_time?: ThinkTimeConfig | null
  browser?: RuntimeBrowserSettings
  log_level?: string
  auto_transactions?: string
  error_handling?: ErrorHandlingConfig | null
  log?: LogConfig
  run_logic?: RunLogicConfig
  http?: HTTPConfig
  websocket?: WebSocketConfig
  web_runtime?: WebRuntimeConfig
  browser_config?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  java_vuser?: RuntimeJavaVuserSettings
  base_url?: string
  env?: Record<string, string>
  redaction_rules?: RedactionRule[]
}

/**
 * The versioned partial-override contract (pkg/api RuntimeSettingsOverride).
 *
 * `override_fields` lists the canonical field paths a row overrides
 * (normalizeRuntimeOverrideFields: section paths such as `http`, leaf paths
 * such as `http.request_timeout_ms`, `env.KEY`, `java_vuser.FIELD`, and map or
 * list fields as whole leaves). A row with a non-empty list applies only those
 * paths onto the inherited config; a row without one keeps the exact v1
 * whole-section semantics. `soap` is the SOAP override section, and the native
 * protocol sections (RuntimeProtocolOverrideSections) sit beside it.
 *
 * These live beside RuntimeSettingsOverride rather than on it because the
 * Command Center dialog's field-coverage guard (groupRuntimeSettingsModel)
 * enumerates RuntimeSettingsOverride keys as replace-style editable fields.
 */
export interface RuntimeOverrideFieldsExtension extends RuntimeProtocolOverrideSections {
  override_fields?: string[]
  soap?: SOAPConfig
}

/** A runtime override row as the API stores it, including the partial-override fields. */
export type PartialRuntimeSettingsOverride = RuntimeSettingsOverride & RuntimeOverrideFieldsExtension

export interface RuntimeSettingsFinding {
  id: string
  scope: string
  group?: string
  script_id?: string
  script_name?: string
  protocol?: string
  area?: string
  concept?: string
  status: string
  severity: string
  summary: string
  recommended_action?: string
  evidence?: string[]
}

export interface RuntimeSettingsReview {
  status: string
  summary?: string
  status_counts?: Record<string, number>
  findings?: RuntimeSettingsFinding[]
  blocking_findings?: RuntimeSettingsFinding[]
  warnings?: string[]
}

export interface ScenarioRuntimeSettingsDraft extends RuntimeProtocolOverrideSections {
  results?: ScriptResultsConfig
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
  browser?: RuntimeBrowserSettings
  log_level: string
  auto_transactions: string
  /** Undefined inherits the script/default setting; false is an explicit override. */
  continue_on_error?: boolean
  /** Undefined inherits the script/default setting; false is an explicit override. */
  snapshot_on_error?: boolean
  fail_transaction_on_error_message?: boolean
  max_retries?: number
  retry_delay_ms?: number
  retry_backoff?: string
  retry_on?: string[]
  honor_retry_after?: boolean
  retry_after_max_ms?: number
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  run_logic?: RunLogicConfig
  http?: HTTPConfig
  websocket?: WebSocketConfig
  web_runtime?: WebRuntimeConfig
  browser_config?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  java_vuser?: RuntimeJavaVuserSettings
  base_url?: string
  env?: Record<string, string>
  redaction_rules?: RedactionRule[]
  soap?: SOAPConfig
  /**
   * Canonical paths this draft overrides. Undefined keeps the stored row's v1
   * whole-section semantics (the row is untouched); any edit through the
   * runtime settings model converts the draft to a partial row first.
   */
  override_fields?: string[]
}
