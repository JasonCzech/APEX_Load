// Shared, framework-agnostic model for Browser/Script runtime overrides.
//
// These pure helpers back both the Script Editor-style summary formatting and the
// Scenario Builder's per-group runtime editor: they format an override field for
// display, resolve which layer an effective value comes from (group override →
// scenario default → script default), and mirror the backend's field-to-protocol
// applicability rule (see pkg/api fieldApplicableForProtocol).

import { type RuntimeBrowserSettings, type RuntimeSettingsOverride } from '../types'
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
export function isRuntimeFieldApplicable(field: RuntimeFieldKey, protocol?: string): boolean {
  if (field === 'browser') {
    return (protocol || '').trim().toLowerCase() === 'browser'
  }
  return true
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
  }
  const format = formatters[field]
  const groupValue = format(group)
  if (groupValue !== null) return { value: groupValue, source: 'group' }
  const scenarioValue = format(scenario)
  if (scenarioValue !== null) return { value: scenarioValue, source: 'scenario' }
  return { value: scriptDefaultLabel, source: 'script' }
}
