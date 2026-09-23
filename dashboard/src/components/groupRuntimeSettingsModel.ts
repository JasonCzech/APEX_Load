import type { RuntimeSettingsOverride } from '../types/runtimeSettings'

/** Field keys the Command Center dialog may send, mirroring the backend set. */
export const EDITABLE_RUNTIME_FIELD_KEYS = [
  'results',
  'iterations',
  'pacing',
  'think_time',
  'error_handling',
  'browser',
  'log_level',
  'auto_transactions',
  'base_url',
  'log',
  'run_logic',
  'http',
  'websocket',
  'web_runtime',
  'browser_config',
  'browser_emulation',
  'java_vuser',
] as const

// Compile-time coverage guard: every RuntimeSettingsOverride field except the
// deliberately non-editable env/redaction_rules must appear in the editable
// field list. Adding an override section without widening the dialog's model
// fails tsc instead of silently omitting the field from change detection.
type NonEditableOverrideKey = 'env' | 'redaction_rules'
type CoverageGap = Exclude<
  keyof RuntimeSettingsOverride,
  NonEditableOverrideKey | (typeof EDITABLE_RUNTIME_FIELD_KEYS)[number]
>
const editableFieldCoverageCheck: CoverageGap extends never ? true : CoverageGap = true
void editableFieldCoverageCheck

/** Effective value of one override field, resolving legacy aliases the way
 * execution does: when the canonical section is present it wins outright, even
 * if its value for the aliased field is unset. */
export function effectiveRuntimeFieldValue(settings: RuntimeSettingsOverride | undefined, key: string): unknown {
  if (!settings) return undefined
  switch (key) {
    case 'iterations':
      return settings.run_logic !== undefined ? settings.run_logic.iterations : settings.iterations
    case 'log_level':
      return settings.log !== undefined ? (settings.log.level || undefined) : (settings.log_level || undefined)
    case 'auto_transactions':
      return settings.run_logic !== undefined
        ? (settings.run_logic.auto_transactions || undefined)
        : (settings.auto_transactions || undefined)
    case 'browser':
      return settings.browser_config ?? settings.browser
    case 'base_url':
      return settings.base_url || undefined
    default:
      return (settings as unknown as Record<string, unknown>)[key]
  }
}

/** Field keys whose effective value differs between two override payloads. */
export function changedRuntimeFieldKeys(
  before: RuntimeSettingsOverride | undefined,
  after: RuntimeSettingsOverride | undefined,
): string[] {
  return EDITABLE_RUNTIME_FIELD_KEYS.filter(key => {
    const fromValue = effectiveRuntimeFieldValue(before, key)
    const toValue = effectiveRuntimeFieldValue(after, key)
    return JSON.stringify(fromValue ?? null) !== JSON.stringify(toValue ?? null)
  })
}
