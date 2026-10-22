import type { TelemetryHealth } from '../types'

// Local receipt identity never comes from wire JSON or retained history. A
// weak key keeps it alive only while the corresponding live telemetry exists.
export interface ClientTelemetryReceipt {
  readonly testId: string
  readonly monotonicMs: number
  readonly visible: boolean
  readonly visibilityEpoch: object
  // The live publication received (TEL-A), when the controller names it.
  readonly publication?: ClientTelemetryPublication
  // repeat marks a re-delivered publication whose first copy was already
  // reported: it is neither logged nor reported again.
  readonly repeat?: boolean
}

export interface ClientTelemetryPublication {
  readonly epoch: string
  readonly sequence: number
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

// Receipts naming a publication, in arrival order, until the render reporter
// resolves them; bounded so a page no reporter reads holds no more.
const RECEIPT_LOG_LIMIT = 128
let receiptLog: ClientTelemetryReceipt[] = []
// The newest publication each run's reporter resolved.
const resolvedNewest = new Map<string, ClientTelemetryPublication>()
const samePublication = (a?: ClientTelemetryPublication, b?: ClientTelemetryPublication) => Boolean(a && b && a.epoch === b.epoch && a.sequence === b.sequence)

function receivedPublication(telemetry: TelemetryHealth): ClientTelemetryPublication | undefined {
  const publication = telemetry.publication
  if (!publication || typeof publication.epoch !== 'string' || publication.epoch.length === 0 || publication.epoch.length > 64
    || !Number.isSafeInteger(publication.sequence) || publication.sequence <= 0) return undefined
  return Object.freeze({ epoch: publication.epoch, sequence: publication.sequence })
}

export function recordClientTelemetryReceipt(telemetry: TelemetryHealth, testId: string): void {
  observeVisibility()
  const publication = receivedPublication(telemetry)
  const repeat = samePublication(publication, resolvedNewest.get(testId))
  const receipt: ClientTelemetryReceipt = Object.freeze({
    testId,
    monotonicMs: performance.now(),
    visible: typeof document !== 'undefined' && document.visibilityState === 'visible',
    visibilityEpoch,
    ...(publication ? { publication } : {}),
    ...(repeat ? { repeat } : {}),
  })
  receipts.set(telemetry, receipt)
  if (!publication || repeat) return
  // The same publication delivered again (a re-broadcast) replaces its
  // unresolved receipt, so it is never reported superseded by itself.
  let duplicate = receiptLog.length - 1
  while (duplicate >= 0 && receiptLog[duplicate].testId !== testId) duplicate--
  if (duplicate >= 0 && samePublication(receiptLog[duplicate].publication, publication)) {
    receiptLog = receiptLog.map((logged, index) => index === duplicate ? receipt : logged)
    return
  }
  receiptLog = [...receiptLog, receipt].slice(-RECEIPT_LOG_LIMIT)
}

// discardLoggedReceipts drops a run's unresolved receipts that arrived before
// current, without reporting them: no reporter for the run was active then,
// so whether they were rendered is unknown (the run accounts them as
// unreported). The current receipt, on screen as reporting starts, and any
// logged after it are kept to be resolved; an unlogged current one is placed
// by its receipt time.
export function discardLoggedReceipts(testId: string, current?: ClientTelemetryReceipt): void {
  const position = current ? receiptLog.indexOf(current) : -1
  receiptLog = receiptLog.filter((logged, index) => logged.testId !== testId
    || (position >= 0 ? index >= position : current !== undefined && logged.monotonicMs > current.monotonicMs))
}

// takeSupersededReceipts resolves receipt: it and the same run's receipts
// logged before it leave the log, and those earlier ones are returned (a newer
// publication reached the page before they were rendered).
export function takeSupersededReceipts(receipt: ClientTelemetryReceipt): ClientTelemetryReceipt[] {
  if (receipt.publication) resolvedNewest.set(receipt.testId, receipt.publication)
  let index = receiptLog.indexOf(receipt)
  if (index < 0 && receipt.publication) {
    // A re-delivered copy took this receipt's place in the log: it resolves
    // with this one.
    index = receiptLog.findIndex(logged => logged.testId === receipt.testId && samePublication(logged.publication, receipt.publication))
  }
  if (index < 0) return []
  const earlier = receiptLog.slice(0, index)
  receiptLog = [...earlier.filter(candidate => candidate.testId !== receipt.testId), ...receiptLog.slice(index + 1)]
  return earlier.filter(candidate => candidate.testId === receipt.testId)
}

export function getClientTelemetryReceipt(telemetry?: TelemetryHealth): ClientTelemetryReceipt | undefined {
  return telemetry ? receipts.get(telemetry) : undefined
}

// clientTelemetryReceiptResolved reports whether a receipt's publication was
// already reported through another copy of it.
export function clientTelemetryReceiptResolved(receipt: ClientTelemetryReceipt): boolean {
  return receipt.repeat === true || samePublication(receipt.publication, resolvedNewest.get(receipt.testId))
}

export function clientTelemetryReceiptVisible(receipt: ClientTelemetryReceipt): boolean {
  return receipt.visible && receipt.visibilityEpoch === visibilityEpoch
    && typeof document !== 'undefined' && document.visibilityState === 'visible'
}
