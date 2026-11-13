// Shared, framework-agnostic model for Browser/Script runtime overrides.
//
// These pure helpers back both the Script Editor-style summary formatting and the
// Scenario builder's per-group runtime editor: they format an override field for
// display, resolve which layer an effective value comes from (group override →
// scenario default → script default), and mirror the backend's field-to-protocol
// applicability rule (see pkg/api fieldApplicableForProtocol).

import { type RuntimeBrowserSettings, type RuntimeJavaVuserSettings, type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { type PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { type BrowserConfig, type BrowserEmulationConfig, type ErrorHandlingConfig, type HTTPConfig, type LogConfig, type PacingConfig, type RunLogicConfig, type ScriptConfig, type ScriptResultsConfig, type SOAPConfig, type ThinkTimeConfig, type WebRuntimeConfig, type WebSocketConfig } from '../types/scripts'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { formatHTTPVersion } from './httpVersionModel'
import { formatPacingTiming } from './runtimeTiming'

export { formatBrowserRuntimeSummary }
export { formatHTTPVersion, HTTP_VERSION_OPTIONS } from './httpVersionModel'

/** The layer an effective runtime value resolves to, most specific first. */
export type RuntimeFieldSource = 'group' | 'scenario' | 'mixed' | 'script' | 'engine'

export const RUNTIME_FIELD_SOURCE_LABEL: Record<RuntimeFieldSource, string> = {
  group: 'Group override',
  scenario: 'Scenario default',
  // A partial row lists only some fields of the section; the rest are inherited.
  mixed: 'Partly overridden',
  script: 'Script default',
  engine: 'Engine default',
}

/**
 * Descriptive labels for fields whose engine default is a behavior rather than a
 * concrete value (or whose false/zero default is dropped by JSON omitempty), so the
 * effective-value display always reads as a real value — never a blank placeholder.
 */
const ENGINE_FALLBACK_LABEL: Partial<Record<RuntimeFieldKey, string>> = {
  iterations: 'Duration-driven',
  error_handling: 'Stop on error',
  web_runtime: 'None',
  think_time: 'None',
  pacing: 'None',
  browser_emulation: 'None',
}

/** Override fields the scenario builder can manage per group (Phase 1 surface). */
export type RuntimeFieldKey =
  | 'results'
  | 'iterations'
  | 'pacing'
  | 'think_time'
  | 'browser'
  | 'log_level'
  | 'auto_transactions'
  | 'error_handling'
  | 'log'
  | 'run_logic'
  | 'http'
  | 'websocket'
  | 'web_runtime'
  | 'browser_config'
  | 'browser_emulation'
  | 'java_vuser'

function isPresent(value: unknown): boolean {
  return value !== undefined && value !== null && value !== ''
}

/**
 * resolveRuntimeFieldSource returns which layer supplies the effective value for a
 * field: the group override if present, else the scenario default, else the script
 * default. `hasGroupOverride` lets the editor show a clear/revert affordance.
 */
export function resolveRuntimeFieldSource(
  groupValue: unknown,
  scenarioValue: unknown,
): { source: RuntimeFieldSource; hasGroupOverride: boolean } {
  if (isPresent(groupValue)) return { source: 'group', hasGroupOverride: true }
  if (isPresent(scenarioValue)) return { source: 'scenario', hasGroupOverride: false }
  return { source: 'script', hasGroupOverride: false }
}

export function formatPacing(pacing?: RuntimeSettingsOverride['pacing']): string | null {
  return formatPacingTiming(pacing)
}

export function formatThinkTime(thinkTime?: RuntimeSettingsOverride['think_time']): string | null {
  if (!thinkTime) return null
  if (thinkTime.ignore) return 'Disabled'
  if (thinkTime.type === 'none' || (!thinkTime.type && !thinkTime.mean_ms)) return 'None'
  if (thinkTime.type === 'random_percentage') return `${thinkTime.min_percent ?? 0}–${thinkTime.max_percent ?? 0}% of recorded think time`
  if (thinkTime.type === 'recorded') return 'Replay recorded think time'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

export function formatLogLevel(level?: string): string | null {
  return level ? level : null
}

export function formatAutoTransactions(value?: string): string | null {
  if (!value) return null
  return value.replace(/_/g, ' ')
}

export function formatIterations(iterations?: number): string | null {
  return iterations === undefined || iterations === null ? null : `${iterations}`
}

export function formatErrorHandling(errorHandling?: RuntimeSettingsOverride['error_handling']): string | null {
  if (!errorHandling) return null
  const parts: string[] = []
  if (errorHandling.continue_on_error !== undefined) {
    parts.push(errorHandling.continue_on_error ? 'Continue on error' : 'Stop on error')
  }
  if (errorHandling.snapshot_on_error !== undefined) {
    parts.push(errorHandling.snapshot_on_error ? 'Snapshot on error' : 'No snapshot')
  }
  if (errorHandling.max_retries) {
    parts.push(`${errorHandling.max_retries} retries`)
  }
  return parts.length > 0 ? parts.join(' · ') : null
}

/** formatBrowserSummary is an alias for the shared browser-runtime formatter. */
export function formatBrowserSummary(browser?: RuntimeBrowserSettings): string | null {
  return formatBrowserRuntimeSummary(browser)
}

export function formatJavaVuserSummary(java?: RuntimeJavaVuserSettings): string | null {
  if (!java) return null
  const parts: string[] = []
  if (java.jdk_selection) parts.push(java.jdk_selection === 'specified' ? 'Specified JDK' : 'Internal JDK')
  if (java.additional_vm_parameters?.length) parts.push(`${java.additional_vm_parameters.length} VM param${java.additional_vm_parameters.length === 1 ? '' : 's'}`)
  if (java.classpath?.length) parts.push(`${java.classpath.length} classpath`)
  // Render the tri-state toggles when explicitly set (including false), so an
  // override whose only content is a false boolean still formats to a non-null
  // string. Otherwise formatEffectiveRuntimeField would treat the layer as empty
  // and mis-attribute the effective value to the script default.
  if (java.dedicated_class_loader !== undefined) parts.push(java.dedicated_class_loader ? 'dedicated loader' : 'no dedicated loader')
  if (java.skip_parameter_evaluation !== undefined) parts.push(java.skip_parameter_evaluation ? 'skip param eval' : 'no skip param eval')
  if (java.use_xbootclasspath !== undefined) parts.push(java.use_xbootclasspath ? 'xboot' : 'no xboot')
  if (java.jvm_sharing) {
    const perJVM = java.vusers_per_jvm ? ` (${java.vusers_per_jvm} per JVM)` : ''
    parts.push(java.jvm_sharing === 'shared'
      ? `shared JVMs${perJVM}`
      : java.jvm_sharing === 'threads'
        ? `LoadRunner threads${perJVM}`
        : 'one JVM per VUser')
  }
  return parts.length > 0 ? parts.join(' · ') : null
}

/**
 * isRuntimeFieldApplicable mirrors the backend fieldApplicableForProtocol (Phase 1):
 * browser device/runtime settings apply only to the browser protocol; every other
 * field is universal. The scenario builder uses this to render "Not applicable" rows
 * and to gate apply-to-groups against a destination group's protocol.
 */
function isHTTPProtocol(protocol?: string): boolean {
  switch ((protocol || '').trim().toLowerCase()) {
    case 'http':
    case 'https':
    case 'soap':
    case 'java_http':
    case 'web_http':
    case 'web':
      return true
    default:
      return false
  }
}

export function isRuntimeFieldApplicable(field: RuntimeFieldKey, protocol?: string): boolean {
  const isBrowser = (protocol || '').trim().toLowerCase() === 'browser'
  switch (field) {
    case 'browser':
    case 'browser_config':
      return isBrowser
    case 'http':
      // HTTP transport (which now also hosts the browser-emulation settings in the
      // editor) applies to HTTP-family protocols AND the browser protocol.
      return isHTTPProtocol(protocol) || isBrowser
    case 'results':
      return isHTTPProtocol(protocol)
    case 'websocket':
      return (protocol || '').trim().toLowerCase() === 'websocket'
    case 'web_runtime':
      return isBrowser || isHTTPProtocol(protocol)
    case 'browser_emulation':
      // Browser emulation drives the HTTP replay engine only; the browser
      // protocol's emulation home stays browser_config.
      return isHTTPProtocol(protocol)
    case 'java_vuser':
      return (protocol || '').trim().toLowerCase() === 'java_vuser'
    default:
      return true
  }
}

/** An override draft with no fields set — i.e. "inherit everything". */
export const EMPTY_RUNTIME_DRAFT: ScenarioRuntimeSettingsDraft = {
  log_level: '',
  auto_transactions: '',
}

/**
 * errorHandlingFromDraft extracts all 9 ErrorHandlingConfig fields from a
 * ScenarioRuntimeSettingsDraft (resolving flat fields with fallback to
 * draft.error_handling). Returns undefined when no field is set.
 */
export function errorHandlingFromDraft(draft?: ScenarioRuntimeSettingsDraft): ErrorHandlingConfig | undefined {
  if (!draft) return undefined
  const base = draft.error_handling
  const continueOnError = draft.continue_on_error !== undefined ? draft.continue_on_error : base?.continue_on_error
  const snapshotOnError = draft.snapshot_on_error !== undefined ? draft.snapshot_on_error : base?.snapshot_on_error
  const failTransactionOnErrorMessage =
    draft.fail_transaction_on_error_message !== undefined
      ? draft.fail_transaction_on_error_message
      : base?.fail_transaction_on_error_message
  const maxRetries = draft.max_retries !== undefined ? draft.max_retries : base?.max_retries
  const retryDelayMs = draft.retry_delay_ms !== undefined ? draft.retry_delay_ms : base?.retry_delay_ms
  const rawBackoff = draft.retry_backoff !== undefined ? draft.retry_backoff : base?.retry_backoff
  const retryBackoff = rawBackoff || undefined
  const rawRetryOn = draft.retry_on !== undefined ? draft.retry_on : base?.retry_on
  const retryOn = rawRetryOn && rawRetryOn.length > 0 ? [...rawRetryOn] : undefined
  const honorRetryAfter = draft.honor_retry_after !== undefined ? draft.honor_retry_after : base?.honor_retry_after
  const retryAfterMaxMs = draft.retry_after_max_ms !== undefined ? draft.retry_after_max_ms : base?.retry_after_max_ms

  if (
    continueOnError === undefined &&
    snapshotOnError === undefined &&
    failTransactionOnErrorMessage === undefined &&
    maxRetries === undefined &&
    retryDelayMs === undefined &&
    retryBackoff === undefined &&
    retryOn === undefined &&
    honorRetryAfter === undefined &&
    retryAfterMaxMs === undefined
  ) {
    return undefined
  }
  const out: ErrorHandlingConfig = {}
  if (continueOnError !== undefined) out.continue_on_error = continueOnError
  if (snapshotOnError !== undefined) out.snapshot_on_error = snapshotOnError
  if (failTransactionOnErrorMessage !== undefined) out.fail_transaction_on_error_message = failTransactionOnErrorMessage
  if (maxRetries !== undefined) out.max_retries = maxRetries
  if (retryDelayMs !== undefined) out.retry_delay_ms = retryDelayMs
  if (retryBackoff !== undefined) out.retry_backoff = retryBackoff
  if (retryOn !== undefined) out.retry_on = retryOn
  if (honorRetryAfter !== undefined) out.honor_retry_after = honorRetryAfter
  if (retryAfterMaxMs !== undefined) out.retry_after_max_ms = retryAfterMaxMs
  return out
}

/** isRuntimeDraftEmpty reports whether a draft carries no actual override. */
export function isRuntimeDraftEmpty(draft?: ScenarioRuntimeSettingsDraft): boolean {
  if (!draft) return true
  return (
    (draft.override_fields?.length ?? 0) === 0 &&
    draft.soap === undefined &&
    draft.iterations === undefined &&
    !draft.pacing?.type &&
    !draft.think_time &&
    !draft.browser &&
    !draft.log_level &&
    !draft.auto_transactions &&
    errorHandlingFromDraft(draft) === undefined &&
    !draft.log &&
    !draft.run_logic &&
    !draft.http &&
    !draft.results &&
    !draft.websocket &&
    !draft.web_runtime &&
    !draft.browser_config &&
    !draft.browser_emulation &&
    isJavaVuserSettingsEmpty(draft.java_vuser) &&
    !draft.base_url &&
    !draft.env &&
    !draft.redaction_rules
  )
}

/** A Java Env override carries no value when every field is unset/empty. */
function isJavaVuserSettingsEmpty(java?: RuntimeJavaVuserSettings): boolean {
  if (!java) return true
  return (
    !java.jdk_selection &&
    !java.jdk_home &&
    !(java.additional_vm_parameters && java.additional_vm_parameters.length > 0) &&
    !(java.classpath && java.classpath.length > 0) &&
    java.use_xbootclasspath === undefined &&
    java.dedicated_class_loader === undefined &&
    java.skip_parameter_evaluation === undefined &&
    !java.jvm_sharing &&
    !java.vusers_per_jvm
  )
}

/**
 * runtimeDraftFromOverride converts a stored API override into the editor's draft
 * shape, so a saved scenario's per-group overrides load back into the editor.
 */
export function runtimeDraftFromOverride(override?: PartialRuntimeSettingsOverride): ScenarioRuntimeSettingsDraft | undefined {
  if (!override) return undefined
  const extension = override
  // A partial row's compact controls mirror its canonical sections, and the
  // flat error-handling controls carry only what it lists.
  const partial = isPartialRuntimeDraft(extension)
  const compactBrowser = partial && override.browser_config
    ? withoutUndefinedBrowserFields({
        headless: override.browser_config.headless ?? override.browser?.headless,
        device_profile: override.browser_config.device_profile || override.browser?.device_profile,
        touch_mode: override.browser_config.touch_mode ?? override.browser?.touch_mode,
        orientation: override.browser_config.orientation || override.browser?.orientation,
      })
    : override.browser
  return {
    iterations: partial ? (override.run_logic?.iterations || override.iterations) : override.iterations,
    // A null section is the run-only clear tombstone; an authoring draft has no such concept,
    // so it reads as the section simply being absent.
    pacing: override.pacing ?? undefined,
    think_time: override.think_time ?? undefined,
    browser: compactBrowser,
    log_level: (partial ? (override.log?.level || override.log_level) : override.log_level) || '',
    auto_transactions: (partial ? (override.run_logic?.auto_transactions || override.auto_transactions) : override.auto_transactions) || '',
    // A section applied whole may be `{}` with no field set (v1 applies any non-nil
    // section whole; a partial row may list it). The flat controls carry every set
    // field, so only a section with none needs the empty marker to keep its pin.
    ...(override.error_handling && errorHandlingFromDraft({ ...EMPTY_RUNTIME_DRAFT, error_handling: override.error_handling }) === undefined
      ? { error_handling: {} }
      : {}),
    continue_on_error: override.error_handling?.continue_on_error,
    snapshot_on_error: override.error_handling?.snapshot_on_error,
    fail_transaction_on_error_message: override.error_handling?.fail_transaction_on_error_message,
    max_retries: override.error_handling?.max_retries,
    retry_delay_ms: override.error_handling?.retry_delay_ms,
    retry_backoff: override.error_handling?.retry_backoff,
    retry_on: override.error_handling?.retry_on ? [...override.error_handling.retry_on] : undefined,
    honor_retry_after: override.error_handling?.honor_retry_after,
    retry_after_max_ms: override.error_handling?.retry_after_max_ms,
    log: override.log,
    run_logic: override.run_logic,
    http: override.http,
    results: override.results,
    websocket: override.websocket,
    web_runtime: override.web_runtime,
    browser_config: override.browser_config,
    browser_emulation: override.browser_emulation,
    java_vuser: override.java_vuser ? { ...override.java_vuser } : undefined,
    base_url: override.base_url,
    env: override.env ? { ...override.env } : undefined,
    redaction_rules: override.redaction_rules ? override.redaction_rules.map(rule => ({ ...rule })) : undefined,
    ...(extension.soap !== undefined ? { soap: structuredClone(extension.soap) } : {}),
    ...(partial ? { override_fields: normalizeRuntimeOverrideFields(extension.override_fields) } : {}),
  }
}

function withoutUndefinedBrowserFields(browser: RuntimeBrowserSettings): RuntimeBrowserSettings | undefined {
  const entries = Object.entries(browser).filter(([, value]) => value !== undefined && value !== '')
  return entries.length > 0 ? Object.fromEntries(entries) as RuntimeBrowserSettings : undefined
}

/**
 * overrideFromRuntimeDraft converts the editor's draft shape (flat
 * continue/snapshot booleans) into the API RuntimeSettingsOverride shape used by
 * the formatters and the scenario payload. Returns undefined for an empty draft.
 */
export function overrideFromRuntimeDraft(draft?: ScenarioRuntimeSettingsDraft): RuntimeSettingsOverride | undefined {
  if (isRuntimeDraftEmpty(draft) || !draft) return undefined
  // A partial draft is the row it will be stored as: only its listed paths.
  if (isPartialRuntimeDraft(draft)) return partialRuntimeOverrideFromDraft(draft)
  const errorHandling = errorHandlingFromDraft(draft)
  const legacy: PartialRuntimeSettingsOverride = {
    iterations: draft.iterations,
    pacing: draft.pacing?.type ? draft.pacing : undefined,
    think_time: draft.think_time,
    browser: draft.browser,
    log_level: draft.log_level || undefined,
    auto_transactions: draft.auto_transactions || undefined,
    error_handling: errorHandling,
    log: draft.log,
    run_logic: draft.run_logic,
    http: draft.http,
    results: draft.results,
    websocket: draft.websocket,
    web_runtime: draft.web_runtime,
    browser_config: draft.browser_config,
    browser_emulation: draft.browser_emulation,
    java_vuser: isJavaVuserSettingsEmpty(draft.java_vuser) ? undefined : { ...draft.java_vuser },
    base_url: draft.base_url,
    env: draft.env ? { ...draft.env } : undefined,
    redaction_rules: draft.redaction_rules ? draft.redaction_rules.map(rule => ({ ...rule })) : undefined,
  }
  if (draft.soap !== undefined) legacy.soap = structuredClone(draft.soap)
  return legacy
}

/** The override payload shape the scenario and create-test APIs accept. */
export type RuntimeOverridePayload = {
  iterations?: number
  pacing?: PacingConfig
  think_time?: ThinkTimeConfig
  browser?: Pick<BrowserConfig, 'headless' | 'device_profile' | 'touch_mode' | 'orientation'>
  log_level?: string
  auto_transactions?: string
  error_handling?: ErrorHandlingConfig
  log?: LogConfig
  run_logic?: RunLogicConfig
  http?: HTTPConfig
  results?: ScriptResultsConfig
  websocket?: WebSocketConfig
  web_runtime?: WebRuntimeConfig
  browser_config?: BrowserConfig
  browser_emulation?: BrowserEmulationConfig
  java_vuser?: RuntimeJavaVuserSettings
  soap?: SOAPConfig
  base_url?: string
  env?: Record<string, string>
  redaction_rules?: Array<{ name?: string, pattern: string, replacement?: string }>
  override_fields?: string[]
}

// hasJavaVuserOverride mirrors isJavaVuserSettingsEmpty: a {} or all-empty
// Java Env section carries no override and is not serialized.
function hasJavaVuserOverride(java?: RuntimeJavaVuserSettings): java is RuntimeJavaVuserSettings {
  return !isJavaVuserSettingsEmpty(java)
}

/**
 * legacyRuntimeOverridePayload is the v1 (no override_fields) request body for
 * a draft: compact aliases are dropped where their canonical section is
 * authored. v1 applies every non-nil section whole (applyRuntimeOverrides), so
 * an untyped pacing, a field-less think_time and an error_handling section with
 * no field set (the draft's empty marker) are kept: a stored v1 row the user
 * does not edit round-trips through it unchanged. A null section is the
 * run-only clear tombstone; it does not shadow its alias (JSON null is nil).
 */
export function legacyRuntimeOverridePayload(runtimeSettings: ScenarioRuntimeSettingsDraft): RuntimeOverridePayload | undefined {
  const hasCanonicalRunLogic = runtimeSettings.run_logic != null
  const hasCanonicalLog = runtimeSettings.log != null
  const hasCanonicalBrowser = runtimeSettings.browser_config != null
  const pacing = runtimeSettings.pacing != null ? { ...runtimeSettings.pacing } : undefined
  const thinkTime = runtimeSettings.think_time != null ? { ...runtimeSettings.think_time } : undefined
  const browser = !hasCanonicalBrowser && runtimeSettings.browser && (
    runtimeSettings.browser.headless !== undefined
    || runtimeSettings.browser.device_profile
    || runtimeSettings.browser.touch_mode !== undefined
    || runtimeSettings.browser.orientation
  )
    ? { ...runtimeSettings.browser }
    : undefined

  const errorHandling = errorHandlingFromDraft(runtimeSettings) ?? (runtimeSettings.error_handling != null ? {} : undefined)
  const hasErrorHandling = errorHandling !== undefined

  const javaVuser = hasJavaVuserOverride(runtimeSettings.java_vuser) ? runtimeSettings.java_vuser : undefined

  if ((!hasCanonicalRunLogic && runtimeSettings.iterations === undefined) && !pacing && !thinkTime && !browser && (!hasCanonicalLog && !runtimeSettings.log_level) && (!hasCanonicalRunLogic && !runtimeSettings.auto_transactions) && !hasErrorHandling && !runtimeSettings.log && !runtimeSettings.run_logic && !runtimeSettings.http && runtimeSettings.results === undefined && runtimeSettings.websocket === undefined && !runtimeSettings.web_runtime && !runtimeSettings.browser_config && !runtimeSettings.browser_emulation && !javaVuser && runtimeSettings.soap === undefined && !runtimeSettings.base_url && !runtimeSettings.env && !runtimeSettings.redaction_rules) {
    return undefined
  }

  return {
    ...(!hasCanonicalRunLogic && runtimeSettings.iterations !== undefined
      ? { iterations: Number(runtimeSettings.iterations) }
      : {}),
    ...(pacing ? { pacing } : {}),
    ...(thinkTime ? { think_time: thinkTime } : {}),
    ...(browser ? { browser } : {}),
    ...(!hasCanonicalLog && runtimeSettings.log_level ? { log_level: runtimeSettings.log_level } : {}),
    ...(!hasCanonicalRunLogic && runtimeSettings.auto_transactions
      ? { auto_transactions: runtimeSettings.auto_transactions }
      : {}),
    ...(errorHandling ? { error_handling: errorHandling } : {}),
    ...(runtimeSettings.log !== undefined ? { log: runtimeSettings.log } : {}),
    ...(runtimeSettings.run_logic !== undefined ? { run_logic: runtimeSettings.run_logic } : {}),
    ...(runtimeSettings.http !== undefined ? { http: runtimeSettings.http } : {}),
    ...(runtimeSettings.results !== undefined ? { results: structuredClone(runtimeSettings.results) } : {}),
    ...(runtimeSettings.websocket !== undefined ? { websocket: structuredClone(runtimeSettings.websocket) } : {}),
    ...(runtimeSettings.web_runtime !== undefined ? { web_runtime: runtimeSettings.web_runtime } : {}),
    ...(runtimeSettings.browser_config !== undefined ? { browser_config: runtimeSettings.browser_config } : {}),
    ...(runtimeSettings.browser_emulation !== undefined ? { browser_emulation: runtimeSettings.browser_emulation } : {}),
    ...(javaVuser ? { java_vuser: { ...javaVuser } } : {}),
    ...(runtimeSettings.soap !== undefined ? { soap: structuredClone(runtimeSettings.soap) } : {}),
    ...(runtimeSettings.base_url !== undefined ? { base_url: runtimeSettings.base_url } : {}),
    ...(runtimeSettings.env ? { env: { ...runtimeSettings.env } } : {}),
    ...(runtimeSettings.redaction_rules ? { redaction_rules: runtimeSettings.redaction_rules.map(rule => ({ ...rule })) } : {}),
  }
}

export function formatLog(log?: RuntimeSettingsOverride['log']): string | null {
  if (!log) return null
  const parts: string[] = []
  if (log.level) parts.push(log.level)
  if (log.on_error_only) parts.push('on-error only')
  if (log.extended_options?.length) parts.push(`${log.extended_options.length} option${log.extended_options.length === 1 ? '' : 's'}`)
  // Tri-state: render whenever explicitly set (including false), so a log layer
  // whose only content is `redact_run_evidence: false` still formats to a non-null
  // string. A truthiness guard would make that layer look empty and
  // formatEffectiveRuntimeField would mis-attribute the effective value upward.
  if (log.redact_run_evidence !== undefined) {
    parts.push(log.redact_run_evidence ? 'redact run evidence' : 'no run-evidence redaction')
  }
  return parts.length > 0 ? parts.join(' · ') : null
}

export function formatRunLogic(runLogic?: RuntimeSettingsOverride['run_logic']): string | null {
  if (!runLogic) return null
  const parts: string[] = []
  if (runLogic.workload_model) parts.push(runLogic.workload_model)
  if (runLogic.iterations) parts.push(`${runLogic.iterations} iters`)
  // global_iterations is a fleet-wide shared cap, distinct from per-VUser
  // iterations — surface it here (rather than conflated into the Iterations row).
  if (runLogic.global_iterations) parts.push(`${runLogic.global_iterations} shared-iter cap`)
  if (runLogic.auto_transactions) parts.push(runLogic.auto_transactions.replace(/_/g, ' '))
  if (runLogic.block_scheduling) parts.push(runLogic.block_scheduling)
  return parts.length > 0 ? parts.join(' · ') : null
}

export function formatHTTP(http?: RuntimeSettingsOverride['http']): string | null {
  if (!http) return null
  const parts: string[] = []
  if (http.version) parts.push(formatHTTPVersion(http.version))
  if (http.tls?.min_version) parts.push(`TLS ${http.tls.min_version}`)
  if (http.proxy_mode && http.proxy_mode !== 'none') parts.push('proxy')
  if (http.request_timeout_ms) parts.push(`${http.request_timeout_ms}ms timeout`)
  if (http.default_headers && Object.keys(http.default_headers).length) parts.push(`${Object.keys(http.default_headers).length} headers`)
  return parts.length > 0 ? parts.join(' · ') : 'configured'
}

export function formatWebRuntime(webRuntime?: RuntimeSettingsOverride['web_runtime']): string | null {
  if (!webRuntime) return null
  const parts: string[] = []
  if (webRuntime.source_ips?.length) parts.push(`${webRuntime.source_ips.length} source IPs`)
  if (webRuntime.network_profiles?.length) parts.push(`${webRuntime.network_profiles.length} profiles`)
  if (webRuntime.content_checks?.length) parts.push(`${webRuntime.content_checks.length} checks`)
  if (webRuntime.additional_attributes && Object.keys(webRuntime.additional_attributes).length) parts.push('attributes')
  return parts.length > 0 ? parts.join(' · ') : 'configured'
}

export function formatBrowserEmulation(emulation?: RuntimeSettingsOverride['browser_emulation']): string | null {
  if (!emulation) return null
  const parts: string[] = []
  if (emulation.mode === 'custom') {
    parts.push('Custom UA')
  } else if (emulation.mode === 'specific') {
    const browser = [emulation.browser_type, emulation.browser_version].filter(Boolean).join(' ')
    if (browser) parts.push(browser)
    if (emulation.platform) parts.push(emulation.platform)
  }
  if (emulation.language) parts.push(emulation.language)
  const cache = emulation.cache
  if (cache?.simulate_cache) parts.push('cache')
  if (cache?.download_non_html_resources) parts.push('resources')
  if (cache?.new_user_each_iteration) parts.push('new user/iter')
  if (cache?.simulate_prefetch) parts.push('prefetch')
  return parts.length > 0 ? parts.join(' · ') : 'configured'
}

export function formatBrowserConfig(browser?: RuntimeSettingsOverride['browser_config']): string | null {
  if (!browser) return null
  const parts: string[] = []
  if (browser.device_profile) parts.push(browser.device_profile)
  if (browser.orientation) parts.push(browser.orientation)
  if (browser.headless !== undefined) parts.push(browser.headless ? 'Headless' : 'Headed')
  return parts.length > 0 ? parts.join(' · ') : 'configured'
}

/**
 * runtimeOverrideFromScriptConfig projects a script's DSL config into the override
 * shape so the existing formatters can render the script's *actual* values as the
 * lowest resolution layer (below scenario default and group override). This is what
 * lets the Effective value column show the real script value (e.g. "Fixed delay ·
 * 250ms") instead of a generic "Script default" placeholder. Fields with no
 * script-config equivalent are left unset and fall through to the engine-default
 * label.
 */
export function runtimeOverrideFromScriptConfig(config?: ScriptConfig): PartialRuntimeSettingsOverride | undefined {
  if (!config) return undefined
  const browser = config.browser
  return {
    // runtimeSettingsFromScriptConfig projects a non-zero SOAP section too, so
    // Apply to groups and the inherited view see the script's SOAP settings.
    ...(hasNonZeroValue(config.soap) ? { soap: structuredClone(config.soap) } : {}),
    iterations: config.run_logic?.iterations,
    pacing: config.pacing,
    think_time: config.think_time,
    browser: browser
      ? {
          headless: browser.headless,
          device_profile: browser.device_profile,
          touch_mode: browser.touch_mode,
          orientation: browser.orientation,
        }
      : undefined,
    log_level: config.log?.level,
    auto_transactions: config.run_logic?.auto_transactions,
    error_handling: config.error_handling,
    log: config.log,
    run_logic: config.run_logic,
    http: config.http,
    results: config.results,
    websocket: config.websocket,
    web_runtime: config.web_runtime,
    browser_config: config.browser,
    browser_emulation: config.browser_emulation,
    java_vuser: config.java_vuser
      ? {
          jdk_selection: config.java_vuser.jdk_selection,
          jdk_home: config.java_vuser.jdk_home,
          additional_vm_parameters: config.java_vuser.additional_vm_parameters,
          classpath: config.java_vuser.classpath,
          use_xbootclasspath: config.java_vuser.use_xbootclasspath,
          dedicated_class_loader: config.java_vuser.dedicated_class_loader,
          skip_parameter_evaluation: config.java_vuser.skip_parameter_evaluation,
          jvm_sharing: config.java_vuser.jvm_sharing,
          vusers_per_jvm: config.java_vuser.vusers_per_jvm,
        }
      : undefined,
    base_url: config.base_url || undefined,
  }
}

/**
 * formatEffectiveRuntimeField renders a single field's effective value with a source
 * label, resolving group override → scenario default → script default. Pass `script`
 * (project a ScriptConfig via runtimeOverrideFromScriptConfig) so the script's actual
 * value is shown when no override applies. Only when no layer carries a concrete value
 * does it fall back to scriptDefaultLabel.
 */
export function formatEffectiveRuntimeField(
  field: RuntimeFieldKey,
  group: RuntimeSettingsOverride | undefined,
  scenario: RuntimeSettingsOverride | undefined,
  script?: RuntimeSettingsOverride,
  engineDefault?: RuntimeSettingsOverride,
  fallbackLabel = 'Engine default',
): { value: string; source: RuntimeFieldSource } {
  const formatters: Record<RuntimeFieldKey, (o?: RuntimeSettingsOverride) => string | null> = {
    // iterations/log_level/auto_transactions can be set either as the legacy
    // top-level field or promoted into the run_logic/log section (the section tab
    // handlers clear the top-level field when they do). Resolve both so a value
    // edited in its section tab is still attributed to this layer in the summary.
    iterations: (o) => formatIterations(o?.iterations ?? o?.run_logic?.iterations),
    pacing: (o) => formatPacing(o?.pacing),
    think_time: (o) => formatThinkTime(o?.think_time),
    browser: (o) => formatBrowserSummary(o?.browser),
    log_level: (o) => formatLogLevel(o?.log_level ?? o?.log?.level),
    auto_transactions: (o) => formatAutoTransactions(o?.auto_transactions ?? o?.run_logic?.auto_transactions),
    error_handling: (o) => formatErrorHandling(o?.error_handling),
    log: (o) => formatLog(o?.log),
    run_logic: (o) => formatRunLogic(o?.run_logic),
    http: (o) => formatHTTP(o?.http),
    results: (o) => o?.results?.message_detail ? `Capture: ${o.results.message_detail.mode || 'default'}` : null,
    websocket: (o) => o?.websocket ? `Callbacks ${o.websocket.callback_interval_ms || 500} ms` : null,
    web_runtime: (o) => formatWebRuntime(o?.web_runtime),
    browser_config: (o) => formatBrowserConfig(o?.browser_config),
    browser_emulation: (o) => formatBrowserEmulation(o?.browser_emulation),
    java_vuser: (o) => formatJavaVuserSummary(o?.java_vuser),
  }
  const format = formatters[field]
  if (isPartialRuntimeDraft(group) || isPartialRuntimeDraft(scenario)) {
    const partial = formatPartialEffectiveRuntimeField(field, format, group, scenario, script, engineDefault)
    if (partial) return partial.value === null ? { value: ENGINE_FALLBACK_LABEL[field] ?? fallbackLabel, source: partial.source } : partial as { value: string; source: RuntimeFieldSource }
  }
  for (const [layer, source] of [[group, 'group'], [scenario, 'scenario']] as const) {
    const layerValue = format(layer)
    if (layerValue !== null) return { value: layerValue, source }
    // A v1 layer applies a section it carries whole (applyRuntimeOverrides), `{}` and
    // an untyped pacing included, so it supplies the field even when it formats empty.
    if (v1LayerCarriesSection(layer, field)) return { value: ENGINE_FALLBACK_LABEL[field] ?? fallbackLabel, source }
  }
  const scriptValue = format(script)
  if (scriptValue !== null) return { value: scriptValue, source: 'script' }
  // Engine default (served by /api/v1/dsl/config-defaults). When even the engine
  // config formats to null — e.g. a false/zero default dropped by JSON omitempty, or
  // a purely behavioral default — fall back to a descriptive label so the effective
  // value is never a blank placeholder.
  const engineValue = format(engineDefault)
  if (engineValue !== null) return { value: engineValue, source: 'engine' }
  return { value: ENGINE_FALLBACK_LABEL[field] ?? fallbackLabel, source: 'engine' }
}

/** The v1 wire section a summary field lives in, when v1 applies it whole. */
const V1_WHOLE_SECTION_KEY: Partial<Record<RuntimeFieldKey, keyof PartialRuntimeSettingsOverride>> = {
  results: 'results',
  iterations: 'run_logic',
  pacing: 'pacing',
  think_time: 'think_time',
  log_level: 'log',
  auto_transactions: 'run_logic',
  error_handling: 'error_handling',
  log: 'log',
  run_logic: 'run_logic',
  http: 'http',
  websocket: 'websocket',
  browser_config: 'browser_config',
  browser_emulation: 'browser_emulation',
}

function v1LayerCarriesSection(layer: RuntimeSettingsOverride | undefined, field: RuntimeFieldKey): boolean {
  const key = V1_WHOLE_SECTION_KEY[field]
  if (!layer || !key || isPartialRuntimeDraft(layer)) return false
  const value = (layer as PartialRuntimeSettingsOverride)[key]
  return value !== undefined && value !== null
}

/** The canonical leaf a compact summary field reads. */
const SUMMARY_FIELD_LEAF_PATH: Partial<Record<RuntimeFieldKey, string>> = {
  iterations: 'run_logic.iterations',
  log_level: 'log.level',
  auto_transactions: 'run_logic.auto_transactions',
}

/**
 * The Summary row for a field when a group or scenario layer is partial: each layer
 * contributes only the paths it lists, laid over the script (else engine) section, so a
 * group listing one HTTP timeout shows the inherited version and proxy beside it. The
 * source is the layer that lists the whole field, 'mixed' when the topmost layer that
 * lists anything in it lists only some of it, or undefined when no override layer
 * touches it (the caller falls back to the script/engine chain).
 */
function formatPartialEffectiveRuntimeField(
  field: RuntimeFieldKey,
  format: (o?: RuntimeSettingsOverride) => string | null,
  group: RuntimeSettingsOverride | undefined,
  scenario: RuntimeSettingsOverride | undefined,
  script: RuntimeSettingsOverride | undefined,
  engineDefault: RuntimeSettingsOverride | undefined,
): { value: string | null; source: RuntimeFieldSource } | undefined {
  const leaf = SUMMARY_FIELD_LEAF_PATH[field]
  if (leaf) {
    const { index, value } = resolveRuntimeRowsValue(leaf, [group, scenario])
    if (index < 0) return undefined
    const [section, key] = leaf.split('.')
    return { value: format({ [section]: { [key]: value } } as RuntimeSettingsOverride), source: index === 0 ? 'group' : 'scenario' }
  }
  const path = field === 'browser' || field === 'browser_config' ? 'browser' : field
  const wireKey = path === 'browser' ? 'browser_config' : path
  const source = runtimeSectionSource(path, [group, scenario])
  if (!source) return undefined
  const lower = [script, engineDefault].find(layer => format(layer) !== null)
  const base = (lower as Record<string, unknown> | undefined)?.[wireKey]
    ?? (path === 'browser' ? (lower as RuntimeSettingsOverride | undefined)?.browser : undefined)
  const merged = effectiveRuntimeSection<unknown>(path, [group, scenario], base)
  const row = (field === 'browser' ? { browser: merged } : { [wireKey]: merged }) as RuntimeSettingsOverride
  const layerSource: RuntimeFieldSource = source.layer === 0 ? 'group' : 'scenario'
  return { value: format(row), source: source.whole ? layerSource : 'mixed' }
}

// ---------------------------------------------------------------------------
// Partial overrides (override_fields)
//
// Mirrors the backend's versioned partial-override contract in pkg/api
// runtime_overrides.go: a row with a non-empty override_fields list applies
// only the listed canonical paths (applyRuntimeOverridesPartial /
// mergeRuntimeSettingsPartial); a row without one keeps the exact v1
// whole-section semantics. Every edit made through this model converts the
// draft to a partial row first — upgrading a stored v1 row by listing every
// path it effectively overrides (effectiveLegacyOverrideFields) so its meaning
// does not change — and then lists the edited path. A row nobody edits is
// never converted.
// ---------------------------------------------------------------------------

/**
 * Field structure of the override sections, generated from docs/dsl.schema.json
 * (which is generated from dsl.ScriptConfig). Each entry lists a struct's JSON
 * fields; `name:Struct` recurses, `name:*` is a map (a whole leaf whose keys may
 * be addressed as `path.KEY`), and a bare name is a leaf (scalars and lists).
 * runtimeSettingsModel.test.ts ('keeps the override-section structure in step
 * with docs/dsl.schema.json') regenerates it from the schema, so a DSL change
 * that is not mirrored here fails that test.
 */
export const RUNTIME_OVERRIDE_SCHEMA: Readonly<Record<string, string>> = {
  ScriptConfig: 'base_url results:ResultsConfig websocket:WebSocketConfig pacing:PacingConfig think_time:ThinkTimeConfig error_handling:ErrorHandlingConfig log:LogConfig run_logic:RunLogicConfig http:HTTPConfig web_runtime:WebRuntimeConfig soap:SOAPConfig browser:BrowserConfig browser_emulation:BrowserEmulationConfig',
  AuthConfig: 'domain hosts password token type username',
  BrowserCacheSimulationConfig: 'cache_content_urls check_newer_versions clear_cache_for_new_user download_non_html_resources new_user_each_iteration simulate_cache simulate_prefetch',
  BrowserConfig: 'available_screen_height available_screen_width cache_content_urls cache_enabled capture_document_metrics capture_interactions capture_screenshots check_newer_versions client_certificates client_metrics device_profile download_resources engine ept_calibration experience_canary geolocation:BrowserGeolocation har_artifact_dir har_capture har_capture_policy har_sla_threshold_ms headless http_credentials:BrowserHTTPCredentials locale new_user_per_iteration orientation proxy:BrowserProxyConfig quic review_policy:BrowserReviewPolicy session:BrowserSessionConfig slow_mo_ms storage_state system_language timezone touch_mode traceparent user_agent viewport:BrowserViewport',
  BrowserEmulationConfig: 'browser_type browser_version cache:BrowserCacheSimulationConfig custom_user_agent language mode platform',
  BrowserGeolocation: 'accuracy latitude longitude',
  BrowserHTTPCredentials: 'origin password send username',
  BrowserProxyConfig: 'bypass password server username',
  BrowserReviewPolicy: 'allow_review_required allow_reviewed_javascript',
  BrowserSessionConfig: 'identities jwt:BrowserSessionJWT preflight_url start_gap_ms strategy',
  BrowserSessionJWT: 'audience client_id login_url private_key start_url',
  BrowserViewport: 'height width',
  DataFormattingConfig: 'auto_detect chain',
  DownloadFiltersConfig: 'hosts mode',
  ErrorHandlingConfig: 'continue_on_error fail_transaction_on_error_message honor_retry_after max_retries retry_after_max_ms retry_backoff retry_delay_ms retry_on snapshot_on_error',
  HTTPConfig: 'accept_encoding accept_language aura_recorded_failures auth:AuthConfig cache_enabled cache_excluded_mime_types cache_max_unused_iterations challenge_delay_ms clear_cookies_per_iteration connect_timeout_ms content_check_error_limit cookies_enabled default_header_list default_headers:* dns_cache fail_noncritical_resources html_excluded_mime_types html_parse_mode http_errors_as_warnings idle_timeout_ms ip_version keep_alive legacy_html_checks max_conns_per_host max_idle_conns max_meta_refresh max_redirects page_timeout_ms proxy:ProxyConfig proxy_mode quic:HTTPQUICConfig receive_buffer_bytes request_timeout_ms resource_timeout_as_warning tls:TLSConfig tls_timeout_ms traceparent traceparent_sampled user_agent version',
  HTTPQUICConfig: 'alt_svc_cache broken_ttl_ms handshake_timeout_ms keep_alive_ms max_idle_timeout_ms',
  LogConfig: 'error_buffer_bytes extended_options level max_response_bytes on_error_only redact_run_evidence',
  MessageDetailConfig: 'include_resources max_item_bytes max_items max_total_bytes mode sample_rate',
  NetworkSimulationConfig: 'bandwidth_kbps jitter_ms latency_ms mode packet_loss_pct preset',
  PacingConfig: 'arrival_process burstiness delay_ms interval_ms max_delay_ms max_interval_ms min_delay_ms min_interval_ms quota:QuotaPacingConfig rate_stages seed tps type',
  ProtocolMixExpectation: 'max_fallback_ratio max_quic_failure_ratio min_samples',
  ProxyConfig: 'no_proxy pac_url password url username',
  QuotaPacingConfig: 'adaptive_aimd burst_tokens default_tokens estimated_tokens_per_request headroom_ratio rpm tpm',
  ResultsConfig: 'csv_exports message_detail:MessageDetailConfig property_analytics protocol_mix:ProtocolMixExpectation',
  RunLogicConfig: 'action_sequence auto_transactions block_scheduling blocks global_iterations iterations max_in_flight max_transactions termination_mode workload_model',
  SOAPConfig: 'fault_handling:SOAPFaultHandlingConfig namespaces:* version ws_security:SOAPWSSecurityConfig',
  SOAPFaultHandlingConfig: 'fail_on_fault',
  SOAPTimestampConfig: 'ttl_seconds',
  SOAPUsernameTokenConfig: 'created nonce password password_type username',
  SOAPWSSecurityConfig: 'timestamp:SOAPTimestampConfig username_token:SOAPUsernameTokenConfig',
  StreamingConfig: 'bandwidth_estimation buffer_size_seconds fixed_bitrate_kbps mode',
  TLSConfig: 'ca_cert_file client_cert_file client_key_file insecure_skip_verify max_version min_version',
  ThinkTimeConfig: 'distribution force ignore limit_ms max_ms max_percent mean_ms min_ms min_percent multiplier std_ms type',
  WebRuntimeConfig: 'additional_attributes:* content_checks data_formatting:DataFormattingConfig download_filters:DownloadFiltersConfig network_profiles network_simulation:NetworkSimulationConfig source_ips streaming:StreamingConfig',
  WebSocketConfig: 'callback_interval_ms compression max_message_size origin read_timeout_ms reconnect subprotocol write_timeout_ms',
}

/** The script-owned Java Env fields a runtime override may set (RuntimeJavaVuserOverride). */
export const JAVA_VUSER_OVERRIDE_FIELDS = [
  'jdk_selection',
  'jdk_home',
  'additional_vm_parameters',
  'classpath',
  'use_xbootclasspath',
  'dedicated_class_loader',
  'skip_parameter_evaluation',
  'jvm_sharing',
  'vusers_per_jvm',
] as const

const schemaStructCache = new Map<string, Map<string, string>>()

function schemaStruct(name: string): Map<string, string> | undefined {
  const cached = schemaStructCache.get(name)
  if (cached) return cached
  const spec = RUNTIME_OVERRIDE_SCHEMA[name]
  if (spec === undefined) return undefined
  const fields = new Map<string, string>()
  for (const entry of spec.split(' ')) {
    if (!entry) continue
    const [field, kind = ''] = entry.split(':')
    fields.set(field, kind)
  }
  schemaStructCache.set(name, fields)
  return fields
}

/** What a canonical override path addresses. */
type OverridePathShape =
  | { kind: 'struct'; struct: string; segments: string[] }
  | { kind: 'map'; segments: string[] }
  | { kind: 'map_key'; segments: string[] }
  | { kind: 'leaf'; segments: string[] }
  | { kind: 'env' }
  | { kind: 'env_key'; key: string }
  | { kind: 'redaction_rules' }
  | { kind: 'java_vuser' }
  | { kind: 'java_vuser_field'; field: string }

/**
 * normalizeRuntimeOverrideFieldPath mirrors normalizeOneRuntimeOverrideField:
 * it accepts the `config.` prefix, the compact aliases (iterations, log_level,
 * auto_transactions, browser_config), `env.KEY`, `java_vuser.FIELD`, and
 * section/leaf paths that walk the DSL struct tags, with a map field's key
 * taken verbatim from the rest of the path. It returns undefined for a path the
 * API would reject.
 */
export function normalizeRuntimeOverrideFieldPath(raw: string): string | undefined {
  let trimmed = (raw ?? '').trim()
  if (!trimmed) return undefined
  if (trimmed.toLowerCase().startsWith('config.')) {
    trimmed = trimmed.slice('config.'.length).trim()
    if (!trimmed) return undefined
  }
  let lower = trimmed.toLowerCase()
  switch (lower) {
    case 'iterations':
      return 'run_logic.iterations'
    case 'log_level':
      return 'log.level'
    case 'auto_transactions':
      return 'run_logic.auto_transactions'
    case 'browser_config':
      return 'browser'
    case 'env':
    case 'redaction_rules':
      return lower
  }
  if (lower.startsWith('browser_config.')) {
    trimmed = 'browser.' + trimmed.slice('browser_config.'.length)
    lower = trimmed.toLowerCase()
  }
  if (lower.startsWith('env.')) {
    const key = trimmed.slice('env.'.length).trim()
    return key ? `env.${key}` : undefined
  }
  if (lower === 'java_vuser') return 'java_vuser'
  if (lower.startsWith('java_vuser.')) {
    const sub = lower.slice('java_vuser.'.length).trim()
    if (!sub || sub.includes('.')) return undefined
    return (JAVA_VUSER_OVERRIDE_FIELDS as readonly string[]).includes(sub) ? `java_vuser.${sub}` : undefined
  }
  const segments = trimmed.split('.')
  const root = schemaStruct('ScriptConfig')
  if (!root?.has(segments[0].trim().toLowerCase())) return undefined
  const canonical: string[] = []
  let current = 'ScriptConfig'
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i].trim()
    if (!seg) return undefined
    const fields = schemaStruct(current)
    const kind = fields?.get(seg.toLowerCase())
    if (kind === undefined) return undefined
    canonical.push(seg.toLowerCase())
    if (i === segments.length - 1) break
    if (kind === '*') {
      const mapKey = segments.slice(i + 1).join('.').trim()
      if (!mapKey) return undefined
      canonical.push(mapKey)
      break
    }
    if (kind === '') return undefined
    current = kind
  }
  return canonical.join('.')
}

/** Normalizes and de-duplicates a stored override_fields list, keeping order. An
 * entry the model cannot parse is kept verbatim: the API accepted the stored row. */
export function normalizeRuntimeOverrideFields(fields?: readonly string[]): string[] {
  const out: string[] = []
  for (const raw of fields ?? []) {
    const canonical = normalizeRuntimeOverrideFieldPath(raw) ?? (raw ?? '').trim()
    if (canonical && !out.includes(canonical)) out.push(canonical)
  }
  return out
}

function overridePathShape(path: string): OverridePathShape | undefined {
  if (path === 'env') return { kind: 'env' }
  if (path.startsWith('env.')) return { kind: 'env_key', key: path.slice('env.'.length) }
  if (path === 'redaction_rules') return { kind: 'redaction_rules' }
  if (path === 'java_vuser') return { kind: 'java_vuser' }
  if (path.startsWith('java_vuser.')) return { kind: 'java_vuser_field', field: path.slice('java_vuser.'.length) }
  const parts = path.split('.')
  const segments: string[] = []
  let current = 'ScriptConfig'
  for (let i = 0; i < parts.length; i++) {
    const kind = schemaStruct(current)?.get(parts[i])
    if (kind === undefined) return undefined
    segments.push(parts[i])
    if (i === parts.length - 1) {
      if (kind === '*') return { kind: 'map', segments }
      if (kind === '') return { kind: 'leaf', segments }
      return { kind: 'struct', struct: kind, segments }
    }
    if (kind === '*') return { kind: 'map_key', segments: [...segments, parts.slice(i + 1).join('.')] }
    if (kind === '') return undefined
    current = kind
  }
  return undefined
}

/** True when `path` is `field` itself or lies under it. */
function pathCoveredBy(path: string, field: string): boolean {
  return path === field || path.startsWith(`${field}.`)
}

function fieldsCoverPath(fields: readonly string[], path: string): boolean {
  return fields.some(field => pathCoveredBy(path, field))
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/** Deep equality of two runtime values (plain objects compared key by key, lists in order). */
export function sameRuntimeValue(left: unknown, right: unknown): boolean {
  if (left === right) return true
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false
    return left.every((value, index) => sameRuntimeValue(value, right[index]))
  }
  if (isPlainObject(left) && isPlainObject(right)) {
    const keys = new Set([...Object.keys(left), ...Object.keys(right)])
    for (const key of keys) {
      if (!sameRuntimeValue(left[key], right[key])) return false
    }
    return true
  }
  return false
}

function cloneRuntimeValue<T>(value: T): T {
  return value === undefined ? value : structuredClone(value)
}

/** The wire key of a top-level override section (`browser` paths live in browser_config). */
function sectionWireKey(section: string): string {
  return section === 'browser' ? 'browser_config' : section
}

function wireSegments(shape: OverridePathShape): string[] | undefined {
  switch (shape.kind) {
    case 'struct':
    case 'map':
    case 'map_key':
    case 'leaf':
      return [sectionWireKey(shape.segments[0]), ...shape.segments.slice(1)]
    case 'env':
      return ['env']
    case 'env_key':
      return ['env', shape.key]
    case 'redaction_rules':
      return ['redaction_rules']
    case 'java_vuser':
      return ['java_vuser']
    case 'java_vuser_field':
      return ['java_vuser', shape.field]
  }
}

function readSegments(source: unknown, segments: readonly string[]): unknown {
  let current: unknown = source
  for (const segment of segments) {
    if (!isPlainObject(current)) return undefined
    current = current[segment]
  }
  return current === null ? undefined : current
}

function writeSegments(target: Record<string, unknown>, segments: readonly string[], value: unknown) {
  let current: Record<string, unknown> = target
  for (let i = 0; i < segments.length - 1; i++) {
    const next = current[segments[i]]
    if (!isPlainObject(next)) current[segments[i]] = {}
    current = current[segments[i]] as Record<string, unknown>
  }
  const leaf = segments[segments.length - 1]
  if (value === undefined) delete current[leaf]
  else current[leaf] = cloneRuntimeValue(value)
}

/** The wire-shape segments of a canonical path (`browser.*` lives under
 * browser_config; a map key stays one segment even when it contains dots). */
export function runtimeOverridePathWireSegments(path: string): string[] | undefined {
  const shape = overridePathShape(path)
  return shape ? wireSegments(shape) : undefined
}

function readOverridePath(wire: PartialRuntimeSettingsOverride | undefined, path: string): unknown {
  const shape = overridePathShape(path)
  const segments = shape ? wireSegments(shape) : undefined
  return segments ? readSegments(wire, segments) : undefined
}

function writeOverridePath(wire: PartialRuntimeSettingsOverride, path: string, value: unknown) {
  const shape = overridePathShape(path)
  const segments = shape ? wireSegments(shape) : undefined
  if (segments) writeSegments(wire as Record<string, unknown>, segments, value)
}

/** One level of child paths under `path`, used to split a listed ancestor when one
 * of its descendants is reset (backend overrideFieldPathsExcluding). */
function childOverridePaths(path: string, wire: PartialRuntimeSettingsOverride): string[] {
  const shape = overridePathShape(path)
  if (!shape) return []
  switch (shape.kind) {
    case 'struct':
      return Array.from(schemaStruct(shape.struct)?.keys() ?? []).map(field => `${path}.${field}`)
    case 'map': {
      const value = readOverridePath(wire, path)
      return isPlainObject(value) ? Object.keys(value).map(key => `${path}.${key}`) : []
    }
    case 'env':
      return Object.keys(wire.env ?? {}).map(key => `env.${key}`)
    case 'java_vuser': {
      // A listed java_vuser overlays only its set leaves (applyJavaVuserRuntimeOverride),
      // so it splits into those leaves: listing an unset leaf would zero it. With no
      // section at all it zeroes every leaf (applyJavaVuserOverrideField), so all split.
      const java = wire.java_vuser as Record<string, unknown> | undefined
      if (!java) return JAVA_VUSER_OVERRIDE_FIELDS.map(field => `java_vuser.${field}`)
      return JAVA_VUSER_OVERRIDE_FIELDS
        .filter(field => javaVuserLeafSet(java[field]) || (field === 'vusers_per_jvm' && javaVuserClearsPerJVM(java)))
        .map(field => `java_vuser.${field}`)
    }
    default:
      return []
  }
}

/** A Java Env leaf applyJavaVuserRuntimeOverride applies (non-empty string or list,
 * non-nil bool, non-zero count). */
function javaVuserLeafSet(value: unknown): boolean {
  if (value === undefined || value === null) return false
  if (Array.isArray(value)) return value.length > 0
  if (typeof value === 'string') return value !== ''
  if (typeof value === 'number') return value !== 0
  return true
}

/** dsl.JavaVuserSharesJVMs */
function javaVuserSharesJVMs(mode: unknown): boolean {
  return mode === 'shared' || mode === 'threads'
}

/** The section's jvm_sharing leaves shared JVMs without setting its own per-JVM count,
 * which drops the inherited one (applyJavaVuserRuntimeOverride). */
function javaVuserClearsPerJVM(java: Record<string, unknown>): boolean {
  return javaVuserLeafSet(java.jvm_sharing) && !javaVuserSharesJVMs(java.jvm_sharing) && !javaVuserLeafSet(java.vusers_per_jvm)
}

/** The row lists java_vuser.jvm_sharing as a leaf with a non-sharing value and does
 * not list its own per-JVM count, so the lower layers' count is dropped. */
function javaVuserLeafClearsPerJVM(state: PartialRuntimeState): boolean {
  return state.fields.includes('java_vuser.jvm_sharing')
    && !state.fields.some(field => pathCoveredBy('java_vuser.vusers_per_jvm', field))
    && !javaVuserSharesJVMs(readOverridePath(state.wire, 'java_vuser.jvm_sharing'))
}

/** A listed java_vuser section laid over the lower layer's (applyJavaVuserOverrideField). */
function overlayJavaVuserSection(lower: unknown, src: unknown): Record<string, unknown> {
  if (!isPlainObject(src)) return {}
  const out: Record<string, unknown> = isPlainObject(lower) ? cloneRuntimeValue(lower) : {}
  for (const field of JAVA_VUSER_OVERRIDE_FIELDS) {
    if (javaVuserLeafSet(src[field])) out[field] = cloneRuntimeValue(src[field])
  }
  if (javaVuserClearsPerJVM(src)) delete out.vusers_per_jvm
  return out
}

/**
 * What one override row supplies at `path`, given the value below it (`lower`). A path
 * the row lists (or an ancestor of it) is the row's value, except under the two
 * sections the backend overlays: a listed `java_vuser` applies only its set leaves
 * (applyJavaVuserRuntimeOverride), and a listed `env` carrying keys merges them over
 * the lower layers (mergeRuntimeSettingsPartial; an `env` with no keys clears them).
 * A leaf such a section leaves unset is supplied by the layer below.
 */
function layerPathValue(state: PartialRuntimeState, path: string, lower: () => unknown): { supplies: boolean; value?: unknown } {
  const covering = state.fields.filter(field => pathCoveredBy(path, field))
  if (covering.length === 0) {
    // A listed jvm_sharing leaf leaving shared JVMs drops the per-JVM count below it
    // (applyJavaVuserOverrideField / applyJavaVuserRuntimeOverride).
    if (path === 'java_vuser.vusers_per_jvm' && javaVuserLeafClearsPerJVM(state)) return { supplies: true, value: undefined }
    return { supplies: false }
  }
  const overlay = (field: string) => field === 'java_vuser' || (field === 'env' && nonEmptyRecord(state.wire.env))
  if (covering.some(field => !overlay(field))) {
    return { supplies: true, value: cloneRuntimeValue(readOverridePath(state.wire, path)) }
  }
  if (path === 'java_vuser') return { supplies: true, value: overlayJavaVuserSection(lower(), state.wire.java_vuser) }
  if (path === 'env') {
    const below = lower()
    return { supplies: true, value: { ...(isPlainObject(below) ? cloneRuntimeValue(below) : {}), ...cloneRuntimeValue(state.wire.env) } }
  }
  if (path.startsWith('env.')) {
    const key = path.slice('env.'.length)
    const env = state.wire.env ?? {}
    return Object.prototype.hasOwnProperty.call(env, key) ? { supplies: true, value: env[key] } : { supplies: false }
  }
  const java = state.wire.java_vuser as Record<string, unknown> | undefined
  if (!java) return { supplies: true, value: undefined }
  const field = path.slice('java_vuser.'.length)
  if (javaVuserLeafSet(java[field])) return { supplies: true, value: cloneRuntimeValue(java[field]) }
  if (field === 'vusers_per_jvm' && javaVuserClearsPerJVM(java)) return { supplies: true, value: undefined }
  return { supplies: false }
}

function overridePathsExcluding(ancestor: string, excluded: string, wire: PartialRuntimeSettingsOverride): string[] {
  const out: string[] = []
  for (const child of childOverridePaths(ancestor, wire)) {
    if (child === excluded) continue
    if (excluded.startsWith(`${child}.`)) out.push(...overridePathsExcluding(child, excluded, wire))
    else out.push(child)
  }
  return out
}

function hasNonZeroValue(value: unknown): boolean {
  if (value === undefined || value === null || value === '' || value === 0 || value === false) return false
  if (Array.isArray(value)) return value.length > 0
  if (isPlainObject(value)) return Object.values(value).some(hasNonZeroValue)
  return true
}

function nonEmptyRecord(value?: Record<string, unknown> | null): boolean {
  return Boolean(value && Object.keys(value).length > 0)
}

/**
 * legacyRuntimeOverrideFields mirrors effectiveLegacyOverrideFields: the paths a
 * v1 row (no override_fields) effectively overrides. A section beats its compact
 * alias (log over log_level, run_logic over iterations/auto_transactions,
 * browser_config over the compact browser block), exactly as v1 applies them.
 */
export function legacyRuntimeOverrideFields(o?: PartialRuntimeSettingsOverride): string[] {
  if (!o) return []
  const fields: string[] = []
  const present = (value: unknown) => value !== undefined && value !== null
  if (o.base_url) fields.push('base_url')
  if (present(o.results)) fields.push('results')
  if (present(o.websocket)) fields.push('websocket')
  if (present(o.pacing)) fields.push('pacing')
  if (present(o.think_time)) fields.push('think_time')
  if (present(o.error_handling)) fields.push('error_handling')
  if (present(o.log)) fields.push('log')
  else if (o.log_level) fields.push('log.level')
  if (present(o.run_logic)) {
    fields.push('run_logic')
  } else {
    if (o.iterations !== undefined && o.iterations !== null) fields.push('run_logic.iterations')
    if (o.auto_transactions) fields.push('run_logic.auto_transactions')
  }
  if (present(o.http)) fields.push('http')
  if (present(o.soap)) fields.push('soap')
  const web = o.web_runtime
  if (web) {
    if (nonEmptyRecord(web.additional_attributes)) fields.push('web_runtime.additional_attributes')
    if (web.download_filters?.mode || (web.download_filters?.hosts?.length ?? 0) > 0) fields.push('web_runtime.download_filters')
    if ((web.content_checks?.length ?? 0) > 0) fields.push('web_runtime.content_checks')
    if (hasNonZeroValue(web.network_simulation)) fields.push('web_runtime.network_simulation')
    if (hasNonZeroValue(web.streaming)) fields.push('web_runtime.streaming')
    if (web.data_formatting?.auto_detect || (web.data_formatting?.chain?.length ?? 0) > 0) fields.push('web_runtime.data_formatting')
    if ((web.source_ips?.length ?? 0) > 0) fields.push('web_runtime.source_ips')
    if ((web.network_profiles?.length ?? 0) > 0) fields.push('web_runtime.network_profiles')
  }
  if (present(o.browser_emulation)) fields.push('browser_emulation')
  if (present(o.browser_config)) {
    fields.push('browser')
  } else if (o.browser) {
    if (o.browser.headless !== undefined && o.browser.headless !== null) fields.push('browser.headless')
    if (o.browser.device_profile) fields.push('browser.device_profile')
    if (o.browser.touch_mode !== undefined && o.browser.touch_mode !== null) fields.push('browser.touch_mode')
    if (o.browser.orientation) fields.push('browser.orientation')
  }
  const java = o.java_vuser
  if (java) {
    if (java.jdk_selection) fields.push('java_vuser.jdk_selection')
    if (java.jdk_home) fields.push('java_vuser.jdk_home')
    if ((java.additional_vm_parameters?.length ?? 0) > 0) fields.push('java_vuser.additional_vm_parameters')
    if ((java.classpath?.length ?? 0) > 0) fields.push('java_vuser.classpath')
    if (java.use_xbootclasspath !== undefined && java.use_xbootclasspath !== null) fields.push('java_vuser.use_xbootclasspath')
    if (java.dedicated_class_loader !== undefined && java.dedicated_class_loader !== null) fields.push('java_vuser.dedicated_class_loader')
    if (java.skip_parameter_evaluation !== undefined && java.skip_parameter_evaluation !== null) fields.push('java_vuser.skip_parameter_evaluation')
    if (java.jvm_sharing) fields.push('java_vuser.jvm_sharing')
    if (java.vusers_per_jvm) fields.push('java_vuser.vusers_per_jvm')
  }
  if (nonEmptyRecord(o.env)) fields.push('env')
  if ((o.redaction_rules?.length ?? 0) > 0) fields.push('redaction_rules')
  return fields
}

function compactBrowserPaths(browser?: RuntimeBrowserSettings): string[] {
  if (!browser) return []
  const paths: string[] = []
  if (browser.headless !== undefined && browser.headless !== null) paths.push('browser.headless')
  if (browser.device_profile) paths.push('browser.device_profile')
  if (browser.touch_mode !== undefined && browser.touch_mode !== null) paths.push('browser.touch_mode')
  if (browser.orientation) paths.push('browser.orientation')
  return paths
}

/** The partial branch of resolveEffectiveOverrideFields for `fields`. */
function resolvePartialOverrideFields(o: PartialRuntimeSettingsOverride, listed?: readonly string[]): string[] {
  const fields = normalizeRuntimeOverrideFields(listed)
  if (nonEmptyRecord(o.env) && !fields.some(field => pathCoveredBy(field, 'env'))) fields.push('env')
  if ((o.redaction_rules?.length ?? 0) > 0 && !fields.includes('redaction_rules')) fields.push('redaction_rules')
  // appendImplicitCompactOverrideFields
  const hasRunLogicSubfield = fields.some(field => field.startsWith('run_logic.'))
  const iterationsAlias = o.iterations !== undefined && o.iterations !== null
  if ((iterationsAlias || ((o.run_logic?.iterations ?? 0) > 0 && hasRunLogicSubfield))
    && !fields.includes('run_logic') && !fields.includes('run_logic.iterations')) {
    fields.push('run_logic.iterations')
  }
  if (o.log_level && !fields.includes('log') && !fields.includes('log.level')) fields.push('log.level')
  if (o.auto_transactions && !fields.includes('run_logic') && !fields.includes('run_logic.auto_transactions')) {
    fields.push('run_logic.auto_transactions')
  }
  if (o.browser && !fields.includes('browser')) {
    // compactBrowserOverridePathsFromOverride reads the compact block (Browser
    // wins over BrowserConfig there).
    for (const path of compactBrowserPaths(o.browser)) {
      if (!fields.includes(path)) fields.push(path)
    }
  }
  return fields
}

/**
 * resolveRuntimeOverrideFields mirrors resolveEffectiveOverrideFields: for a
 * partial row, the normalized list plus what the backend lists implicitly (env
 * and redaction_rules when present, and the compact iterations / log_level /
 * auto_transactions / browser aliases); for a v1 row, the legacy fields.
 */
export function resolveRuntimeOverrideFields(o?: PartialRuntimeSettingsOverride): string[] {
  if (!o) return []
  if (!o.override_fields || o.override_fields.length === 0) return legacyRuntimeOverrideFields(o)
  return resolvePartialOverrideFields(o, o.override_fields)
}

/** A draft whose edits already use partial semantics. */
export function isPartialRuntimeDraft(draft?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride): boolean {
  return Array.isArray(draft?.override_fields)
}

function compactBrowserFill(target: NonNullable<PartialRuntimeSettingsOverride['browser_config']>, compact?: RuntimeBrowserSettings) {
  if (!compact) return
  if (target.headless === undefined && compact.headless !== undefined) target.headless = compact.headless
  if (!target.device_profile && compact.device_profile) target.device_profile = compact.device_profile
  if (target.touch_mode === undefined && compact.touch_mode !== undefined) target.touch_mode = compact.touch_mode
  if (!target.orientation && compact.orientation) target.orientation = compact.orientation
}

/**
 * The canonical (alias-free) override a partial draft describes. Mirrors
 * overrideToScriptConfig: a compact alias fills its section's zero value
 * (iterations, log level, auto transactions, the compact browser block), and
 * the flat error-handling controls win over the error_handling section.
 */
function canonicalPartialOverride(draft: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride): PartialRuntimeSettingsOverride {
  const source = draft as ScenarioRuntimeSettingsDraft
  const out: PartialRuntimeSettingsOverride = {}
  const copy = <K extends keyof PartialRuntimeSettingsOverride>(key: K, value: PartialRuntimeSettingsOverride[K] | null | undefined) => {
    if (value !== undefined && value !== null) out[key] = cloneRuntimeValue(value) as PartialRuntimeSettingsOverride[K]
  }
  copy('base_url', source.base_url || undefined)
  copy('results', source.results)
  copy('websocket', source.websocket)
  copy('pacing', source.pacing)
  copy('think_time', source.think_time)
  copy('error_handling', errorHandlingFromDraft(source) ?? (source.error_handling ? {} : undefined))
  copy('http', source.http)
  copy('web_runtime', source.web_runtime)
  copy('soap', source.soap)
  copy('browser_emulation', source.browser_emulation)
  copy('java_vuser', source.java_vuser)
  copy('env', source.env)
  copy('redaction_rules', source.redaction_rules)
  const runLogic = source.run_logic ? cloneRuntimeValue(source.run_logic) : undefined
  if (source.iterations !== undefined && source.iterations !== null && !runLogic?.iterations) {
    out.run_logic = { ...runLogic, iterations: source.iterations }
  } else if (runLogic) {
    out.run_logic = runLogic
  }
  if (source.auto_transactions && !out.run_logic?.auto_transactions) {
    out.run_logic = { ...out.run_logic, auto_transactions: source.auto_transactions }
  }
  const log = source.log ? cloneRuntimeValue(source.log) : undefined
  if (source.log_level && !log?.level) out.log = { ...log, level: source.log_level }
  else if (log) out.log = log
  if (source.browser_config || source.browser) {
    const browser = cloneRuntimeValue(source.browser_config) ?? {}
    compactBrowserFill(browser, source.browser)
    out.browser_config = browser
  }
  return out
}

/** The canonical override a v1 row applies: its sections, with each compact
 * alias folded into its section only when that section is absent (v1 lets the
 * section win). */
function canonicalLegacyOverride(legacy?: PartialRuntimeSettingsOverride): PartialRuntimeSettingsOverride {
  if (!legacy) return {}
  const out = cloneRuntimeValue(legacy)
  delete out.override_fields
  if (out.run_logic == null) {
    const runLogic: NonNullable<PartialRuntimeSettingsOverride['run_logic']> = {}
    if (out.iterations !== undefined && out.iterations !== null) runLogic.iterations = out.iterations
    if (out.auto_transactions) runLogic.auto_transactions = out.auto_transactions
    if (Object.keys(runLogic).length > 0) out.run_logic = runLogic
  }
  delete out.iterations
  delete out.auto_transactions
  if (out.log == null && out.log_level) out.log = { level: out.log_level }
  delete out.log_level
  if (out.browser_config == null && out.browser) {
    const browser: NonNullable<PartialRuntimeSettingsOverride['browser_config']> = {}
    compactBrowserFill(browser, out.browser)
    out.browser_config = browser
  }
  delete out.browser
  if (out.pacing === null) delete out.pacing
  if (out.think_time === null) delete out.think_time
  if (out.error_handling === null) delete out.error_handling
  return out
}

interface PartialRuntimeState {
  wire: PartialRuntimeSettingsOverride
  fields: string[]
}

/** The canonical override and effective field list of a draft (or stored row),
 * converting a v1 row without changing what it applies. */
function partialRuntimeState(draft?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride): PartialRuntimeState {
  if (!draft) return { wire: {}, fields: [] }
  if (isPartialRuntimeDraft(draft)) {
    // The implicit fields come from the draft as stored (its compact aliases), so
    // a stored partial row reads exactly as resolveEffectiveOverrideFields reads it.
    return {
      wire: canonicalPartialOverride(draft),
      fields: resolvePartialOverrideFields(draft as PartialRuntimeSettingsOverride, draft.override_fields),
    }
  }
  const legacy = legacyRuntimeRow(draft as ScenarioRuntimeSettingsDraft)
  return { wire: canonicalLegacyOverride(legacy), fields: legacyRuntimeOverrideFields(legacy) }
}

/**
 * The row a v1 draft (or stored v1 row) applies: its v1 request body, which keeps
 * every non-nil section the backend (effectiveLegacyOverrideFields) applies whole,
 * `{}` included, matching resolveRuntimeRowsValue. A null section is the run-only
 * tombstone.
 */
function legacyRuntimeRow(draft: ScenarioRuntimeSettingsDraft): PartialRuntimeSettingsOverride | undefined {
  return legacyRuntimeOverridePayload(draft)
}

/**
 * runtimeOverrideRowFromDraft is the stored row a draft describes, in the wire shape
 * the backend applies: a partial draft's listed paths (override_fields), or a v1
 * draft's v1 row with every whole section it carries (an untyped pacing, `{}`
 * think_time or error_handling included). Use it, not overrideFromRuntimeDraft,
 * wherever a draft is laid over lower layers, so every surface reads the row the
 * way partialRuntimeState does.
 */
export function runtimeOverrideRowFromDraft(draft?: ScenarioRuntimeSettingsDraft): PartialRuntimeSettingsOverride | undefined {
  if (!draft) return undefined
  if (isPartialRuntimeDraft(draft)) return partialRuntimeOverrideFromDraft(draft)
  return legacyRuntimeRow(draft)
}

/**
 * projectRuntimeOverride keeps only what the listed paths apply, as
 * mergeRuntimeSettingsPartial(nil, row) does before the backend stores or seals
 * a partial row. Values at unlisted paths are never applied, so they are not
 * sent. A masked secret ("[REDACTED]") at a listed path is sent unchanged, so
 * the backend's reconcile restores the stored value.
 */
function projectRuntimeOverride(wire: PartialRuntimeSettingsOverride, fields: readonly string[]): PartialRuntimeSettingsOverride {
  const out: PartialRuntimeSettingsOverride = {}
  for (const path of fields) {
    if (path === 'env' || path.startsWith('env.') || path === 'redaction_rules') continue
    const value = readOverridePath(wire, path)
    if (value !== undefined) writeOverridePath(out, path, value)
  }
  const envKeys = fields.filter(field => field.startsWith('env.')).map(field => field.slice('env.'.length))
  if (wire.env && Object.keys(wire.env).length > 0) {
    if (fields.includes('env') || envKeys.length === 0) {
      out.env = { ...wire.env }
    } else {
      const env: Record<string, string> = {}
      for (const key of envKeys) {
        if (wire.env[key] !== undefined) env[key] = wire.env[key]
      }
      if (Object.keys(env).length > 0) out.env = env
    }
  }
  if ((wire.redaction_rules?.length ?? 0) > 0) out.redaction_rules = cloneRuntimeValue(wire.redaction_rules)
  return out
}

/** The partial override row (wire shape) a partial draft describes. */
export function partialRuntimeOverrideFromDraft(draft?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride): PartialRuntimeSettingsOverride | undefined {
  if (!draft) return undefined
  const { wire, fields } = partialRuntimeState(draft)
  const projected = projectRuntimeOverride(wire, fields)
  if (fields.length > 0) return { ...projected, override_fields: fields }
  return Object.keys(projected).length > 0 ? projected : undefined
}

function draftFromPartialState(state: PartialRuntimeState): ScenarioRuntimeSettingsDraft {
  const projected = projectRuntimeOverride(state.wire, state.fields)
  return runtimeDraftFromOverride({ ...projected, override_fields: [...state.fields] }) as ScenarioRuntimeSettingsDraft
}

/**
 * upgradeRuntimeDraftToPartial converts a v1 draft to a partial one that
 * applies exactly what the v1 row applied: every path it effectively overrides
 * is listed (a section beats its compact alias, as dropV1ShadowedRuntimeAliases
 * keeps it). A partial draft is returned unchanged.
 */
export function upgradeRuntimeDraftToPartial(draft?: ScenarioRuntimeSettingsDraft): ScenarioRuntimeSettingsDraft {
  if (draft && isPartialRuntimeDraft(draft)) return draft
  return draftFromPartialState(partialRuntimeState(draft))
}

function setStateField(state: PartialRuntimeState, path: string, value: unknown) {
  writeOverridePath(state.wire, path, value)
  if (!fieldsCoverPath(state.fields, path)) {
    state.fields = [...state.fields.filter(field => !pathCoveredBy(field, path)), path]
  }
  if (path === 'java_vuser.jvm_sharing' && value !== 'shared' && value !== 'threads'
    && fieldsCoverPath(state.fields, 'java_vuser.vusers_per_jvm')) {
    // Mirrors applyJavaVuserOverrideField: leaving shared JVMs drops the per-JVM count.
    writeOverridePath(state.wire, 'java_vuser.vusers_per_jvm', undefined)
  }
}

function resetStateField(state: PartialRuntimeState, path: string) {
  let fields = state.fields.filter(field => !pathCoveredBy(field, path))
  for (;;) {
    const ancestor = fields.find(field => path.startsWith(`${field}.`))
    if (!ancestor) break
    fields = [...fields.filter(field => field !== ancestor), ...overridePathsExcluding(ancestor, path, state.wire)]
  }
  state.fields = fields
  writeOverridePath(state.wire, path, undefined)
}

/**
 * setOverrideField overrides one path with an explicit value (including
 * false, 0 and ""); `undefined` overrides it to empty, which the backend
 * applies as the zero value. A v1 draft is converted first. A path the API
 * would reject leaves the draft unchanged.
 */
export function setOverrideField(draft: ScenarioRuntimeSettingsDraft | undefined, path: string, value: unknown): ScenarioRuntimeSettingsDraft {
  const canonical = normalizeRuntimeOverrideFieldPath(path)
  if (!canonical) return draft ?? { ...EMPTY_RUNTIME_DRAFT }
  const state = partialRuntimeState(draft)
  setStateField(state, canonical, value)
  return draftFromPartialState(state)
}

/**
 * resetOverrideField returns one path to its inherited value: the path (and
 * anything under it) leaves override_fields and the draft, every other
 * overridden field stays, and a listed ancestor is split into its remaining
 * children (overrideFieldPathsExcluding) so siblings keep their override.
 */
export function resetOverrideField(draft: ScenarioRuntimeSettingsDraft | undefined, path: string): ScenarioRuntimeSettingsDraft {
  const canonical = normalizeRuntimeOverrideFieldPath(path)
  if (!canonical) return draft ?? { ...EMPTY_RUNTIME_DRAFT }
  const state = partialRuntimeState(draft)
  resetStateField(state, canonical)
  return draftFromPartialState(state)
}

/** The paths a draft or stored row effectively overrides. */
export function runtimeOverrideFieldPaths(draft?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride): string[] {
  return partialRuntimeState(draft).fields
}

/** True when the draft overrides `path`, through the path itself, an ancestor
 * section, or (for a section path) any field inside it. */
export function isFieldOverridden(draft: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined, path: string): boolean {
  const canonical = normalizeRuntimeOverrideFieldPath(path) ?? path
  const fields = partialRuntimeState(draft).fields
  return fields.some(field => pathCoveredBy(canonical, field) || pathCoveredBy(field, canonical))
}

/** The value a draft holds at `path` (undefined when it does not override it). */
export function overrideFieldValue(draft: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined, path: string): unknown {
  const canonical = normalizeRuntimeOverrideFieldPath(path) ?? path
  const { wire, fields } = partialRuntimeState(draft)
  if (!fieldsCoverPath(fields, canonical)) return undefined
  return cloneRuntimeValue(readOverridePath(wire, canonical))
}

/** Leaf entries of a nested patch under `base`, recursing only into struct fields
 * (maps and lists are whole leaves). */
function flattenOverridePatch(base: string, patch: unknown, out: Array<[string, unknown]>) {
  const shape = overridePathShape(base)
  const recurse = isPlainObject(patch) && (shape?.kind === 'struct' || shape?.kind === 'java_vuser')
  if (!recurse) {
    out.push([base, patch])
    return
  }
  for (const [key, value] of Object.entries(patch as Record<string, unknown>)) {
    flattenOverridePatch(`${base}.${key}`, value, out)
  }
}

/**
 * applyRuntimeOverrideEdit applies a control's patch to the section at `base`:
 * each leaf the patch names is overridden with its value, and a leaf set to
 * `undefined` is reset to inherited. Leaves the patch does not name are left
 * alone, so editing one HTTP timeout never pins the rest of the section.
 */
export function applyRuntimeOverrideEdit(
  draft: ScenarioRuntimeSettingsDraft | undefined,
  base: string,
  patch: Record<string, unknown>,
): ScenarioRuntimeSettingsDraft {
  const root = normalizeRuntimeOverrideFieldPath(base)
  if (!root) return draft ?? { ...EMPTY_RUNTIME_DRAFT }
  const entries: Array<[string, unknown]> = []
  flattenOverridePatch(root, patch, entries)
  const state = partialRuntimeState(draft)
  for (const [path, value] of entries) {
    const canonical = normalizeRuntimeOverrideFieldPath(path)
    if (!canonical) continue
    if (value === undefined) resetStateField(state, canonical)
    else setStateField(state, canonical, value)
  }
  return draftFromPartialState(state)
}

function flattenOverrideValue(base: string, value: unknown, out: Map<string, unknown>) {
  const shape = overridePathShape(base)
  if (isPlainObject(value) && (shape?.kind === 'struct' || shape?.kind === 'java_vuser')) {
    for (const [key, child] of Object.entries(value)) flattenOverrideValue(`${base}.${key}`, child, out)
    return
  }
  out.set(base, value)
}

/**
 * applyRuntimeOverrideDiff compares a section before and after an editor
 * replaced it wholesale and lists only the leaves that changed: a changed leaf
 * is overridden, a removed one is reset to inherited.
 */
export function applyRuntimeOverrideDiff(
  draft: ScenarioRuntimeSettingsDraft | undefined,
  base: string,
  before: unknown,
  after: unknown,
): ScenarioRuntimeSettingsDraft {
  const root = normalizeRuntimeOverrideFieldPath(base)
  if (!root) return draft ?? { ...EMPTY_RUNTIME_DRAFT }
  const beforeLeaves = new Map<string, unknown>()
  const afterLeaves = new Map<string, unknown>()
  flattenOverrideValue(root, before, beforeLeaves)
  flattenOverrideValue(root, after, afterLeaves)
  const state = partialRuntimeState(draft)
  let changed = false
  for (const path of new Set([...beforeLeaves.keys(), ...afterLeaves.keys()])) {
    const next = afterLeaves.get(path)
    if (sameRuntimeValue(beforeLeaves.get(path), next)) continue
    const canonical = normalizeRuntimeOverrideFieldPath(path)
    if (!canonical) continue
    changed = true
    if (next === undefined) resetStateField(state, canonical)
    else setStateField(state, canonical, next)
  }
  return changed ? draftFromPartialState(state) : (draft ?? { ...EMPTY_RUNTIME_DRAFT })
}

/**
 * overlayRuntimeOverrideSection resolves the effective value of one section for
 * display: the inherited section with each path the draft overrides inside it
 * laid over (a whole-section override replaces it, as v1 does; a listed java_vuser
 * or keyed env overlays it instead, as the backend applies them).
 */
export function overlayRuntimeOverrideSection<T>(
  inherited: T | undefined,
  draft: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined,
  section: string,
): T | undefined {
  const root = normalizeRuntimeOverrideFieldPath(section) ?? section
  const state = partialRuntimeState(draft)
  const { wire, fields } = state
  if (fields.some(field => pathCoveredBy(root, field))) {
    const supplied = layerPathValue(state, root, () => inherited)
    if (!supplied.supplies) return cloneRuntimeValue(inherited)
    return supplied.value as T | undefined ?? ({} as T)
  }
  const inside = fields.filter(field => field.startsWith(`${root}.`))
  if (inside.length === 0) return cloneRuntimeValue(inherited)
  const result: Record<string, unknown> = isPlainObject(inherited) ? cloneRuntimeValue(inherited) as Record<string, unknown> : {}
  for (const field of inside) {
    const shape = overridePathShape(field)
    const rootShape = overridePathShape(root)
    const fieldSegments = shape ? wireSegments(shape) : undefined
    const rootSegments = rootShape ? wireSegments(rootShape) : undefined
    if (!fieldSegments || !rootSegments) continue
    writeSegments(result, fieldSegments.slice(rootSegments.length), readOverridePath(wire, field))
  }
  if (root === 'java_vuser' && javaVuserLeafClearsPerJVM(state)) delete result.vusers_per_jvm
  return result as T
}

/** Which layer a runtime value resolves to; see RuntimeValueProvenance. */
export type RuntimeOverrideProvenance = 'group_override' | 'scenario_default' | 'script' | 'engine_default'

export interface RuntimeOverrideLayers {
  group?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride
  scenario?: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride
  script?: ScriptConfig
  engine?: ScriptConfig
}

function readScriptConfigPath(config: ScriptConfig | undefined, path: string): unknown {
  if (!config) return undefined
  const shape = overridePathShape(path)
  if (!shape) return undefined
  switch (shape.kind) {
    case 'struct':
    case 'map':
    case 'map_key':
    case 'leaf':
      return readSegments(config, shape.segments)
    case 'java_vuser':
      return readSegments(config, ['java_vuser'])
    case 'java_vuser_field':
      return readSegments(config, ['java_vuser', shape.field])
    default:
      return undefined
  }
}

/**
 * effectiveRuntimeValue resolves one path through the layers execution uses —
 * group override, then scenario default, then the script, then the engine
 * default — and reports which layer supplied it. An override layer supplies a
 * path it lists even when its value is empty (an explicit clear).
 */
export function effectiveRuntimeValue(path: string, layers: RuntimeOverrideLayers): { value: unknown; provenance: RuntimeOverrideProvenance } {
  const canonical = normalizeRuntimeOverrideFieldPath(path) ?? path
  const rows = [[layers.group, 'group_override'], [layers.scenario, 'scenario_default']] as const
  const resolveFrom = (start: number): { value: unknown; provenance: RuntimeOverrideProvenance } => {
    for (let index = start; index < rows.length; index++) {
      const [layer, provenance] = rows[index]
      if (!layer) continue
      const supplied = layerPathValue(partialRuntimeState(layer), canonical, () => resolveFrom(index + 1).value)
      if (supplied.supplies) return { value: supplied.value, provenance }
    }
    const scriptValue = readScriptConfigPath(layers.script, canonical)
    if (scriptValue !== undefined) return { value: cloneRuntimeValue(scriptValue), provenance: 'script' }
    return { value: cloneRuntimeValue(readScriptConfigPath(layers.engine, canonical)), provenance: 'engine_default' }
  }
  return resolveFrom(0)
}

/**
 * True when the row lists `path` itself or an ancestor of it, so the row (not a
 * lower layer) supplies the value at `path`. Unlike isFieldOverridden, a row that
 * lists only fields INSIDE `path` does not cover it. A v1 row covers every path in
 * a section it carries (whole-section semantics).
 */
export function runtimeRowCoversPath(row: ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined, path: string): boolean {
  if (!row) return false
  const canonical = normalizeRuntimeOverrideFieldPath(path) ?? path
  return fieldsCoverPath(partialRuntimeState(row).fields, canonical)
}

/**
 * resolveRuntimeRowsValue reads one path through stored override rows ordered
 * highest first (group, then scenario/run) the way the backend applies them:
 * a partial row supplies only the paths it lists (an ancestor included), and a v1
 * wire row supplies every path inside a section it carries — even `{}`, which the
 * API transmits as a non-nil section applied whole. Returns the index of the row
 * that supplies the path (its value may be empty: an explicit clear, or a v1
 * section omitting the field) or -1 when no row lists it and the script applies.
 * A listed java_vuser, and a listed env carrying keys, overlay the rows below them
 * (layerPathValue): a leaf they leave unset resolves through the lower rows.
 */
export function resolveRuntimeRowsValue(
  path: string,
  rows: ReadonlyArray<ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined | null>,
): { index: number; value: unknown } {
  const canonical = normalizeRuntimeOverrideFieldPath(path) ?? path
  const resolveFrom = (start: number): { index: number; value: unknown } => {
    for (let index = start; index < rows.length; index++) {
      const row = rows[index]
      if (!row) continue
      const legacy = row as PartialRuntimeSettingsOverride
      const state: PartialRuntimeState = isPartialRuntimeDraft(row)
        ? partialRuntimeState(row)
        : { wire: canonicalLegacyOverride(legacy), fields: legacyRuntimeOverrideFields(legacy) }
      const supplied = layerPathValue(state, canonical, () => resolveFrom(index + 1).value)
      if (supplied.supplies) return { index, value: supplied.value }
    }
    return { index: -1, value: undefined }
  }
  return resolveFrom(0)
}

const BROWSER_SUMMARY_FIELDS = ['headless', 'device_profile', 'touch_mode', 'orientation'] as const

/**
 * The four browser fields a stored runtime row (or a stack of rows, highest first)
 * applies, in either wire shape: the compact `browser` block, a v1 `browser_config`
 * section, or the browser.* paths a partial row lists in browser_config.
 */
export function runtimeBrowserSettings(...rows: Array<ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined | null>): RuntimeBrowserSettings | undefined {
  const browser: Record<string, unknown> = {}
  for (const field of BROWSER_SUMMARY_FIELDS) {
    const { index, value } = resolveRuntimeRowsValue(`browser.${field}`, rows)
    if (index >= 0 && value !== undefined && value !== null && value !== '') browser[field] = value
  }
  return Object.keys(browser).length > 0 ? browser as RuntimeBrowserSettings : undefined
}

/**
 * effectiveRuntimeSection resolves one section for display through override layers
 * ordered highest first (group, then scenario/run), laid over `base` (the script
 * section, when known): each layer contributes only the paths it lists, and a layer
 * that lists the whole section replaces it (v1 rows, and partial rows listing it).
 */
export function effectiveRuntimeSection<T>(
  section: string,
  layers: ReadonlyArray<ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined>,
  base?: T,
): T | undefined {
  let value: T | undefined = cloneRuntimeValue(base)
  for (const layer of [...layers].reverse()) {
    if (!layer) continue
    value = overlayRuntimeOverrideSection<T>(value, layer, section)
  }
  return value
}

/**
 * Where a section's displayed value comes from: 'group' (the top layer) when it
 * lists the whole section, 'mixed' when it lists only some paths inside it,
 * otherwise the index of the first lower layer that lists anything in it, or
 * 'inherited' when none does.
 */
export function runtimeSectionSource(
  section: string,
  layers: ReadonlyArray<ScenarioRuntimeSettingsDraft | PartialRuntimeSettingsOverride | undefined>,
): { layer: number; whole: boolean } | undefined {
  for (let index = 0; index < layers.length; index++) {
    const layer = layers[index]
    if (!layer || !isFieldOverridden(layer, section)) continue
    return { layer: index, whole: runtimeRowCoversPath(layer, section) }
  }
  return undefined
}

/** Whether a draft's runtime settings carry the group's iteration count (so the
 * compact group iterations field must not be sent beside them). */
export function runtimeDraftOwnsIterations(draft?: ScenarioRuntimeSettingsDraft): boolean {
  if (!draft) return false
  if (isPartialRuntimeDraft(draft)) return fieldsCoverPath(partialRuntimeState(draft).fields, 'run_logic.iterations')
  return draft.run_logic !== undefined || draft.iterations !== undefined
}
