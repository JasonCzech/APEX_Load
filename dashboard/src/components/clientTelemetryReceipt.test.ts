// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { clientTelemetryReceiptResolved, discardLoggedReceipts, getClientTelemetryReceipt, recordClientTelemetryReceipt, takeSupersededReceipts } from './clientTelemetryReceipt'

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

it('logs named publications in arrival order and resolves earlier ones as superseded', () => {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  const named = (testId: string, sequence: unknown, epoch: unknown = 'e1') => {
    const telemetry = { publication: { epoch, sequence, eligible: true } } as never
    recordClientTelemetryReceipt(telemetry, testId)
    return getClientTelemetryReceipt(telemetry)!
  }
  const one = named('run-a', 1)
  const other = named('run-b', 1)
  const two = named('run-a', 2)
  // Malformed identities are not publications: never logged, never reported.
  for (const [sequence, epoch] of [[0, 'e1'], [1.5, 'e1'], [3, ''], [4, 'x'.repeat(65)], ['5', 'e1']] as Array<[unknown, unknown]>) {
    expect(named('run-a', sequence, epoch).publication).toBeUndefined()
  }
  const three = named('run-a', 3)
  expect(one.publication).toEqual({ epoch: 'e1', sequence: 1 })
  expect(takeSupersededReceipts(three)).toEqual([one, two])
  // Resolved receipts leave the log; another run's stay.
  expect(takeSupersededReceipts(two)).toEqual([])
  expect(takeSupersededReceipts(other)).toEqual([])
  // The log is bounded.
  const many = Array.from({ length: 200 }, (_, i) => named('run-c', i + 1))
  expect(takeSupersededReceipts(many[199])).toHaveLength(127)
})

it('replaces a re-delivered publication and discards receipts no reporter saw', () => {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  const named = (testId: string, sequence: number) => {
    const telemetry = { publication: { epoch: 'e2', sequence, eligible: true } } as never
    recordClientTelemetryReceipt(telemetry, testId)
    return getClientTelemetryReceipt(telemetry)!
  }
  const first = named('run-d', 1)
  const again = named('run-d', 1)
  const next = named('run-d', 2)
  // The re-delivered copy took the first's place: it is never superseded by itself.
  expect(takeSupersededReceipts(next)).toEqual([again])
  expect(takeSupersededReceipts(first)).toEqual([])
  const stale = named('run-e', 1)
  const current = named('run-e', 2)
  const otherRun = named('run-f', 1)
  discardLoggedReceipts('run-e', current)
  expect(takeSupersededReceipts(current)).toEqual([])
  expect(takeSupersededReceipts(stale)).toEqual([])
  expect(takeSupersededReceipts(named('run-f', 2))).toEqual([otherRun])
  // A publication re-delivered after its first copy was resolved is a repeat:
  // never logged, never reported again.
  const resolved = named('run-g', 7)
  takeSupersededReceipts(resolved)
  const repeat = named('run-g', 7)
  expect(repeat.repeat).toBe(true)
  expect(takeSupersededReceipts(named('run-g', 8))).toEqual([])
  // A copy rendered after its re-delivered twin replaced it in the log
  // resolves the twin, which is then never reported again.
  const copyA = named('run-i', 4)
  const copyB = named('run-i', 4)
  expect(takeSupersededReceipts(copyA)).toEqual([])
  expect(clientTelemetryReceiptResolved(copyB)).toBe(true)
  expect(takeSupersededReceipts(named('run-i', 5))).toEqual([])
  // Receipts that arrived after the one on screen survive a discard.
  const onScreen = named('run-h', 1)
  const later = named('run-h', 2)
  discardLoggedReceipts('run-h', onScreen)
  expect(takeSupersededReceipts(named('run-h', 3))).toEqual([onScreen, later])
})
