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
} from './scripts'

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
>

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
 */
export interface RuntimeSettingsOverride {
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
  web_runtime?: WebRuntimeConfig
  browser_config?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  java_vuser?: RuntimeJavaVuserSettings
  base_url?: string
  env?: Record<string, string>
  redaction_rules?: RedactionRule[]
}

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

export interface ScenarioRuntimeSettingsDraft {
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
  max_retries?: number
  retry_delay_ms?: number
  retry_backoff?: string
  log?: LogConfig
  run_logic?: RunLogicConfig
  http?: HTTPConfig
  web_runtime?: WebRuntimeConfig
  browser_config?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  java_vuser?: RuntimeJavaVuserSettings
  base_url?: string
  env?: Record<string, string>
  redaction_rules?: RedactionRule[]
}
