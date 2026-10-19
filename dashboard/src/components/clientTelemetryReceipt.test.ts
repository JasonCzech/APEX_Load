// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { getClientTelemetryReceipt, recordClientTelemetryReceipt } from './clientTelemetryReceipt'

afterEach(() => vi.restoreAllMocks())

it('keeps local receipt identity outside serialized telemetry and history', () => {
  vi.spyOn(performance, 'now').mockReturnValue(0)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  const telemetry = { client_received_at: '2026-10-19T12:00:00Z' }
  recordClientTelemetryReceipt(telemetry, 'run-one')
  expect(getClientTelemetryReceipt(telemetry)).toMatchObject({ testId: 'run-one', monotonicMs: 0, visible: true })
  expect(Object.isFrozen(getClientTelemetryReceipt(telemetry))).toBe(true)
  expect(getClientTelemetryReceipt(JSON.parse(JSON.stringify(telemetry)))).toBeUndefined()
  expect(getClientTelemetryReceipt()).toBeUndefined()
  expect(JSON.stringify(telemetry)).toBe('{"client_received_at":"2026-10-19T12:00:00Z"}')
})
