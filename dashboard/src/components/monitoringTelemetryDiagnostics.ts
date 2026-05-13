import { TelemetryDiagnosticEntry } from '../types'

export const MAX_TELEMETRY_DIAGNOSTICS = 12

export function appendTelemetryDiagnostic(
  previous: TelemetryDiagnosticEntry[],
  next: TelemetryDiagnosticEntry,
): TelemetryDiagnosticEntry[] {
  const deduped = previous.filter((entry) => !(
    entry.test_id === next.test_id
    && entry.timestamp === next.timestamp
    && entry.diagnostic.summary === next.diagnostic.summary
  ))

  return [next, ...deduped].slice(0, MAX_TELEMETRY_DIAGNOSTICS)
}

export function telemetryDiagnosticSeverityClass(severity: TelemetryDiagnosticEntry['diagnostic']['severity']): string {
  if (severity === 'danger') return 'danger'
  if (severity === 'warning') return 'warning'
  return 'healthy'
}

export function formatTelemetryDiagnosticTime(timestamp: string): string {
  const parsed = new Date(timestamp)
  if (Number.isNaN(parsed.getTime())) return 'unknown'
  return parsed.toLocaleTimeString()
}