import type { TelemetryHealth } from '../types'

// Local receipt identity never comes from wire JSON or retained history. A
// weak key keeps it alive only while the corresponding live telemetry exists.
export interface ClientTelemetryReceipt {
  readonly testId: string
  readonly monotonicMs: number
  readonly visible: boolean
  readonly visibilityEpoch: object
}
// One page-lifetime listener starts at the first local receipt, before React
// can defer mounting/enabling the reporter. Opaque epochs preserve event order
// even when clock precision rounds receipt and visibility change to one tick.
let visibilityEpoch = {}
let observingVisibility = false
function observeVisibility(): void {
  if (observingVisibility || typeof document === 'undefined') return
  observingVisibility = true
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible') visibilityEpoch = {}
  })
}

const receipts = new WeakMap<TelemetryHealth, ClientTelemetryReceipt>()

export function recordClientTelemetryReceipt(telemetry: TelemetryHealth, testId: string): void {
  observeVisibility()
  receipts.set(telemetry, Object.freeze({
    testId,
    monotonicMs: performance.now(),
    visible: typeof document !== 'undefined' && document.visibilityState === 'visible',
    visibilityEpoch,
  }))
}

export function getClientTelemetryReceipt(telemetry?: TelemetryHealth): ClientTelemetryReceipt | undefined {
  return telemetry ? receipts.get(telemetry) : undefined
}

export function clientTelemetryReceiptVisible(receipt: ClientTelemetryReceipt): boolean {
  return receipt.visible && receipt.visibilityEpoch === visibilityEpoch
    && typeof document !== 'undefined' && document.visibilityState === 'visible'
}
