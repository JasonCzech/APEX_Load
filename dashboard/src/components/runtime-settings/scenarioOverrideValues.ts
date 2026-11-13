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

export function formatOverrideValue(value: unknown): string {
  if (value === undefined || value === null || value === '') return 'Empty'
  if (value === REDACTED_SECRET) return 'Stored secret'
  if (typeof value === 'boolean') return value ? 'Enabled' : 'Disabled'
  if (Array.isArray(value)) return value.length === 0 ? 'None' : value.map(item => formatOverrideValue(item)).join(', ')
  if (typeof value === 'object') {
    const text = JSON.stringify(value)
    return text.length > 80 ? `${text.slice(0, 77)}…` : text
  }
  return String(value)
}

