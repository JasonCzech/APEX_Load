import { describe, expect, it } from 'vitest'

import { TelemetryDiagnosticEntry } from '../types'
import { appendTelemetryDiagnostic, MAX_TELEMETRY_DIAGNOSTICS, telemetryDiagnosticSeverityClass } from './monitoringTelemetryDiagnostics'

function buildEntry(index: number): TelemetryDiagnosticEntry {
  return {
    test_id: `test-${index}`,
    test_name: `Test ${index}`,
    timestamp: `2026-05-12T20:05:${String(index).padStart(2, '0')}Z`,
    diagnostic: {
      severity: index % 2 === 0 ? 'warning' : 'danger',
      summary: `Diagnostic ${index}`,
      conditions: ['stale_sample'],
    },
  }
}

describe('monitoringTelemetryDiagnostics', () => {
  it('prepends newest diagnostics and caps the retained list', () => {
    let entries: TelemetryDiagnosticEntry[] = []

    for (let index = 0; index < MAX_TELEMETRY_DIAGNOSTICS + 2; index += 1) {
      entries = appendTelemetryDiagnostic(entries, buildEntry(index))
    }

    expect(entries).toHaveLength(MAX_TELEMETRY_DIAGNOSTICS)
    expect(entries[0]?.test_id).toBe(`test-${MAX_TELEMETRY_DIAGNOSTICS + 1}`)
    expect(entries[MAX_TELEMETRY_DIAGNOSTICS - 1]?.test_id).toBe('test-2')
  })

  it('maps diagnostic severities to monitoring classes', () => {
    expect(telemetryDiagnosticSeverityClass('danger')).toBe('danger')
    expect(telemetryDiagnosticSeverityClass('warning')).toBe('warning')
    expect(telemetryDiagnosticSeverityClass('healthy')).toBe('healthy')
  })
})