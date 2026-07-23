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

export interface RuntimeSettingsOverride {
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
  browser?: RuntimeBrowserSettings
  log_level?: string
  auto_transactions?: string
  error_handling?: ErrorHandlingConfig
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
