import { RUNTIME_VALUE_PROVENANCE_LABELS, type RuntimeValueProvenance } from './canonicalRuntimeTaxonomy'

// Value helpers behind the scenario and group override field controls.

/** What the API sends back in place of a stored secret; sending it back keeps the stored value. */
export const REDACTED_SECRET = '[REDACTED]'

export function provenanceLabel(provenance: RuntimeValueProvenance, overrideLabel?: string): string {
  if (overrideLabel && (provenance === 'group_override' || provenance === 'scenario_default')) return overrideLabel
  return RUNTIME_VALUE_PROVENANCE_LABELS[provenance]
}

export function triStateSelectValue(value: boolean | undefined): '' | 'true' | 'false' {
  return value === undefined ? '' : value ? 'true' : 'false'
}

export function parseTriStateSelectValue(raw: string): boolean | undefined {
  return raw === '' ? undefined : raw === 'true'
}

/** Leaf names that hold credentials (auth password/token, proxy and WS-Security passwords...). */
const SECRET_LEAF_PATTERN = /(password|passphrase|secret|token|api[_-]?key|authorization|credential|private[_-]?key)/i

/** Drops user:password from a URL-looking string so credentials never reach the screen. */
function withoutUrlCredentials(text: string): string {
  return text.replace(/([a-z][a-z0-9+.-]*:\/\/)[^/\s@]+@/gi, '$1***@')
}

/**
 * A value-free reading of an override value: objects (a v1 section, an env
 * map, a headers row) are never echoed, since they can carry typed
 * credentials; only their size is shown.
 */
export function formatOverrideValue(value: unknown): string {
  if (value === undefined || value === null || value === '') return 'Empty'
  if (value === REDACTED_SECRET) return 'Stored secret'
  if (typeof value === 'boolean') return value ? 'Enabled' : 'Disabled'
  if (Array.isArray(value)) return value.length === 0 ? 'None' : value.map(item => formatOverrideValue(item)).join(', ')
  if (typeof value === 'object') {
    const count = Object.keys(value as Record<string, unknown>).length
    return count === 0 ? 'None' : `${count} ${count === 1 ? 'field' : 'fields'} set`
  }
  return withoutUrlCredentials(String(value))
}

/**
 * How the panel list shows one overridden path: a whole v1 section by a label,
 * a credential leaf without its value, anything else through formatOverrideValue.
 */
export function formatOverrideEntryValue(path: string, value: unknown): string {
  if (!path.includes('.') && path !== 'env' && value !== null && typeof value === 'object' && !Array.isArray(value)) return 'Whole section (v1)'
  const leaf = path.slice(path.lastIndexOf('.') + 1)
  if (SECRET_LEAF_PATTERN.test(leaf) && typeof value === 'string' && value !== '' && value !== REDACTED_SECRET) return 'Set (hidden)'
  return formatOverrideValue(value)
}

