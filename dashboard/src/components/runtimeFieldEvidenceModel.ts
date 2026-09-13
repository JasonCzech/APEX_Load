import type { ScriptConfig } from '../types/scripts'
import { isSensitiveCredentialPreviewKey } from './credentialPreviewPolicy'

export function readRuntimePath(config: ScriptConfig | undefined, path: string): unknown {
  return path.replace(/^config\./, '').split('.').reduce<unknown>((value, key) => value && typeof value === 'object' ? (value as Record<string, unknown>)[key] : undefined, config)
}

export function formatRuntimeEvidenceValue(value: unknown, path: string): string {
  if (value === undefined || value === null) return 'Not specified'
  if (path.split('.').some(isSensitiveCredentialPreviewKey)) return 'Protected value'
  if (typeof value === 'object') return Array.isArray(value) ? `${value.length} entries — inspect the dedicated editor` : 'Configured — inspect the dedicated editor'
  if (typeof value === 'boolean') return value ? 'On' : 'Off'
  // Free-form strings can embed credentials even when the setting name is benign.
  if (typeof value === 'string' && (/[:/@?=]/.test(value) || value.length > 80)) return 'Configured — inspect the dedicated editor'
  return String(value) || '(empty)'
}
