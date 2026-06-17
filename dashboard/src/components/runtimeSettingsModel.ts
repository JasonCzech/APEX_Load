// Shared, framework-agnostic model for Browser/Script runtime overrides.
//
// These pure helpers back both the Script Editor-style summary formatting and the
// Scenario Builder's per-group runtime editor: they format an override field for
// display, resolve which layer an effective value comes from (group override →
// scenario default → script default), and mirror the backend's field-to-protocol
// applicability rule (see pkg/api fieldApplicableForProtocol).

import { type RuntimeBrowserSettings, type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'

export { formatBrowserRuntimeSummary }

/** The layer an effective runtime value resolves to, most specific first. */
export type RuntimeFieldSource = 'group' | 'scenario' | 'script'

export const RUNTIME_FIELD_SOURCE_LABEL: Record<RuntimeFieldSource, string> = {
  group: 'Group override',
  scenario: 'Scenario default',
  script: 'Script default',
}

/** Override fields the Scenario Builder can manage per group (Phase 1 surface). */
export type RuntimeFieldKey =
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
  | 'web_runtime'
  | 'browser_config'

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
  if (!pacing?.type) return null
  if (pacing.type === 'none') return 'None'
  if (pacing.type === 'fixed_delay') return `Fixed delay · ${pacing.delay_ms || 0}ms`
  if (pacing.type === 'fixed_interval') return `Fixed interval · ${pacing.interval_ms || 0}ms`
  if (pacing.type === 'tps') return `Target TPS · ${pacing.tps || 0}`
  return pacing.type.replace(/_/g, ' ')
}

export function formatThinkTime(thinkTime?: RuntimeSettingsOverride['think_time']): string | null {
  if (!thinkTime) return null
  if (thinkTime.ignore) return 'Disabled'
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

/**
 * isRuntimeFieldApplicable mirrors the backend fieldApplicableForProtocol (Phase 1):
 * browser device/runtime settings apply only to the browser protocol; every other
 * field is universal. The Scenario Builder uses this to render "Not applicable" rows
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
      return isHTTPProtocol(protocol)
    case 'web_runtime':
      return isBrowser || isHTTPProtocol(protocol)
    default:
      return true
  }
}

/** An override draft with no fields set — i.e. "inherit everything". */
export const EMPTY_RUNTIME_DRAFT: ScenarioRuntimeSettingsDraft = {
  log_level: '',
  auto_transactions: '',
  continue_on_error: false,
  snapshot_on_error: false,
}

/** isRuntimeDraftEmpty reports whether a draft carries no actual override. */
export function isRuntimeDraftEmpty(draft?: ScenarioRuntimeSettingsDraft): boolean {
  if (!draft) return true
  return (
    draft.iterations === undefined &&
    !draft.pacing?.type &&
    !draft.think_time &&
    !draft.browser &&
    !draft.log_level &&
    !draft.auto_transactions &&
    !draft.continue_on_error &&
    !draft.snapshot_on_error &&
    draft.max_retries === undefined &&
    draft.retry_delay_ms === undefined &&
    !draft.retry_backoff &&
    !draft.log &&
    !draft.run_logic &&
    !draft.http &&
    !draft.web_runtime &&
    !draft.browser_config
  )
}

/**
 * runtimeDraftFromOverride converts a stored API override into the editor's draft
 * shape, so a saved scenario's per-group overrides load back into the editor.
 */
export function runtimeDraftFromOverride(override?: RuntimeSettingsOverride): ScenarioRuntimeSettingsDraft | undefined {
  if (!override) return undefined
  return {
    iterations: override.iterations,
    pacing: override.pacing,
    think_time: override.think_time,
    browser: override.browser,
    log_level: override.log_level || '',
    auto_transactions: override.auto_transactions || '',
    continue_on_error: Boolean(override.error_handling?.continue_on_error),
    snapshot_on_error: Boolean(override.error_handling?.snapshot_on_error),
    max_retries: override.error_handling?.max_retries,
    retry_delay_ms: override.error_handling?.retry_delay_ms,
    retry_backoff: override.error_handling?.retry_backoff,
    log: override.log,
    run_logic: override.run_logic,
    http: override.http,
    web_runtime: override.web_runtime,
    browser_config: override.browser_config,
  }
}

/**
 * overrideFromRuntimeDraft converts the editor's draft shape (flat
 * continue/snapshot booleans) into the API RuntimeSettingsOverride shape used by
 * the formatters and the scenario payload. Returns undefined for an empty draft.
 */
export function overrideFromRuntimeDraft(draft?: ScenarioRuntimeSettingsDraft): RuntimeSettingsOverride | undefined {
  if (isRuntimeDraftEmpty(draft) || !draft) return undefined
  const hasErrorHandling =
    draft.continue_on_error ||
    draft.snapshot_on_error ||
    draft.max_retries !== undefined ||
    draft.retry_delay_ms !== undefined ||
    Boolean(draft.retry_backoff)
  const errorHandling = hasErrorHandling
    ? {
        continue_on_error: draft.continue_on_error || undefined,
        snapshot_on_error: draft.snapshot_on_error || undefined,
        max_retries: draft.max_retries,
        retry_delay_ms: draft.retry_delay_ms,
        retry_backoff: draft.retry_backoff || undefined,
      }
    : undefined
  return {
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
    web_runtime: draft.web_runtime,
    browser_config: draft.browser_config,
  }
}

export function formatLog(log?: RuntimeSettingsOverride['log']): string | null {
  if (!log) return null
  const parts: string[] = []
  if (log.level) parts.push(log.level)
  if (log.on_error_only) parts.push('on-error only')
  if (log.extended_options?.length) parts.push(`${log.extended_options.length} option${log.extended_options.length === 1 ? '' : 's'}`)
  return parts.length > 0 ? parts.join(' · ') : null
}

export function formatRunLogic(runLogic?: RuntimeSettingsOverride['run_logic']): string | null {
  if (!runLogic) return null
  const parts: string[] = []
  if (runLogic.workload_model) parts.push(runLogic.workload_model)
  if (runLogic.iterations) parts.push(`${runLogic.iterations} iters`)
  if (runLogic.auto_transactions) parts.push(runLogic.auto_transactions.replace(/_/g, ' '))
  if (runLogic.block_scheduling) parts.push(runLogic.block_scheduling)
  return parts.length > 0 ? parts.join(' · ') : null
}

export function formatHTTP(http?: RuntimeSettingsOverride['http']): string | null {
  if (!http) return null
  const parts: string[] = []
  if (http.version) parts.push(`HTTP/${http.version}`)
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

export function formatBrowserConfig(browser?: RuntimeSettingsOverride['browser_config']): string | null {
  if (!browser) return null
  const parts: string[] = []
  if (browser.device_profile) parts.push(browser.device_profile)
  if (browser.orientation) parts.push(browser.orientation)
  if (browser.headless !== undefined) parts.push(browser.headless ? 'Headless' : 'Headed')
  return parts.length > 0 ? parts.join(' · ') : 'configured'
}

/**
 * formatEffectiveRuntimeField renders a single field's effective value with a source
 * label, resolving group → scenario → script. Returns the display value plus the
 * resolved source so callers can badge it. When no override applies, value is the
 * supplied scriptDefaultLabel (or 'Script default').
 */
export function formatEffectiveRuntimeField(
  field: RuntimeFieldKey,
  group: RuntimeSettingsOverride | undefined,
  scenario: RuntimeSettingsOverride | undefined,
  scriptDefaultLabel = 'Script default',
): { value: string; source: RuntimeFieldSource } {
  const formatters: Record<RuntimeFieldKey, (o?: RuntimeSettingsOverride) => string | null> = {
    iterations: (o) => formatIterations(o?.iterations),
    pacing: (o) => formatPacing(o?.pacing),
    think_time: (o) => formatThinkTime(o?.think_time),
    browser: (o) => formatBrowserSummary(o?.browser),
    log_level: (o) => formatLogLevel(o?.log_level),
    auto_transactions: (o) => formatAutoTransactions(o?.auto_transactions),
    error_handling: (o) => formatErrorHandling(o?.error_handling),
    log: (o) => formatLog(o?.log),
    run_logic: (o) => formatRunLogic(o?.run_logic),
    http: (o) => formatHTTP(o?.http),
    web_runtime: (o) => formatWebRuntime(o?.web_runtime),
    browser_config: (o) => formatBrowserConfig(o?.browser_config),
  }
  const format = formatters[field]
  const groupValue = format(group)
  if (groupValue !== null) return { value: groupValue, source: 'group' }
  const scenarioValue = format(scenario)
  if (scenarioValue !== null) return { value: scenarioValue, source: 'scenario' }
  return { value: scriptDefaultLabel, source: 'script' }
}
