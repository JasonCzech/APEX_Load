import type { ScriptConfig } from '../types/scripts'

export function readRuntimePath(config: ScriptConfig | undefined, path: string): unknown {
  return path.replace(/^config\./, '').split('.').reduce<unknown>((value, key) => value && typeof value === 'object' ? (value as Record<string, unknown>)[key] : undefined, config)
}

export function formatRuntimeEvidenceValue(value: unknown): string {
  if (value === undefined || value === null) return 'Not specified'
  if (typeof value === 'object') return Array.isArray(value) ? `${value.length} entries — inspect the dedicated editor` : 'Configured — inspect the dedicated editor'
  if (typeof value === 'boolean') return value ? 'On' : 'Off'
  // Summarize complex URLs or long strings in the compact table.
  if (typeof value === 'string' && (/[:/@?=]/.test(value) || value.length > 80)) return 'Configured — inspect the dedicated editor'
  return String(value) || '(empty)'
}
