import { readStoredString } from './browserStorage'

function parseFlag(value: string | undefined): boolean | undefined {
  const normalized = value?.trim().toLowerCase()
  if (!normalized) return undefined
  if (['1', 'true', 'yes', 'on', 'enabled'].includes(normalized)) return true
  if (['0', 'false', 'no', 'off', 'disabled'].includes(normalized)) return false
  return undefined
}

export function isRunReportEnabled(): boolean {
  const stored = parseFlag(readStoredString('apexload.featureFlags.runReport', ''))
  if (stored !== undefined) return stored
  return parseFlag(import.meta.env?.VITE_APEXLOAD_RUN_REPORT) ?? false
}
