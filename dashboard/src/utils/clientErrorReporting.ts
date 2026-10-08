// Browser-side failures never reach the controller's request log on their own:
// render crashes, uncaught exceptions, and API requests that failed before (or
// without) a controller response. This module forwards a bounded, deduplicated
// sample of them to POST /api/v1/system/client-errors so they land in the same
// structured log as server failures. It is inert until installed, so tests and
// embedded surfaces never send reports, and reporting can never throw into the
// code path that observed the failure.

export type ClientErrorKind = 'error' | 'unhandledrejection' | 'render' | 'network' | 'gateway'

export interface ClientErrorReport {
  kind: ClientErrorKind
  message: string
  name?: string
  stack?: string
  component_stack?: string
  source?: string
  page?: string
  request_method?: string
  request_path?: string
  request_status?: number
  request_id?: string
  app_version?: string
}

export type ClientErrorDetails = Omit<Partial<ClientErrorReport>, 'kind' | 'message'>

// The sender resolves to the report response's HTTP status (or undefined when
// it never got one) so the reporter can back off when the controller refuses.
export type ClientErrorSender = (report: ClientErrorReport) => Promise<number | undefined>

export const CLIENT_ERRORS_PATH = '/system/client-errors'

const MAX_REPORTS_PER_MINUTE = 10
const MAX_REPORTS_PER_PAGE_LOAD = 50
const DUPLICATE_WINDOW_MS = 60_000
const REFUSED_BACKOFF_MS = 60_000
const MAX_MESSAGE_CHARS = 1_000
const MAX_STACK_CHARS = 8_000
const MAX_COMPONENT_STACK_CHARS = 4_000
const MAX_LOCATION_CHARS = 500

// Browser noise with no diagnostic value: the ResizeObserver loop notice is
// benign, and "Script error." is a cross-origin error the browser has already
// stripped of everything useful.
const IGNORED_MESSAGES = [/^ResizeObserver loop/i, /^Script error\.?$/i]

interface ReporterState {
  sender: ClientErrorSender
  appVersion?: string
  now: () => number
  location: () => string
  sentTotal: number
  windowStartedAt: number
  sentInWindow: number
  suspendedUntil: number
  lastSentAt: Map<string, number>
}

let state: ReporterState | null = null
let uninstallCurrent: (() => void) | null = null

function truncate(value: string | undefined, max: number): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return trimmed.length > max ? trimmed.slice(0, max) : trimmed
}

// withoutQuery drops query strings, where dashboard and API URLs carry
// one-time tokens (desktop capabilities, OAuth state). Hash routes keep their
// path. The controller strips them again; this keeps them off the wire.
export function withoutQuery(value: string | undefined): string | undefined {
  if (!value) return undefined
  const [beforeHash, ...hashParts] = value.split('#')
  let result = beforeHash.split('?')[0]
  const hashRoute = hashParts.join('#').split('?')[0]
  if (hashRoute.startsWith('/')) result += `#${hashRoute}`
  return truncate(result, MAX_LOCATION_CHARS)
}

// A cancelled request or unmounted view rejects with AbortError; that is the
// caller giving up, not a failure.
function isAbort(error: unknown): boolean {
  return (error instanceof DOMException || error instanceof Error) && error.name === 'AbortError'
}

// An ApiError (duck-typed: importing it would make apiClient's own import of
// this module circular) carries the controller request ID and status, which
// join the report to the controller's access line.
function apiFailureDetails(error: unknown): ClientErrorDetails {
  if (!error || typeof error !== 'object') return {}
  const { requestId, status } = error as { requestId?: unknown; status?: unknown }
  return {
    request_id: typeof requestId === 'string' ? requestId : undefined,
    request_status: typeof status === 'number' ? status : undefined,
  }
}

function describe(error: unknown): { message: string; name?: string; stack?: string } {
  if (error instanceof Error) {
    return { message: error.message || error.name || 'Error', name: error.name, stack: error.stack }
  }
  if (typeof error === 'string') return { message: error }
  if (error && typeof error === 'object' && 'message' in error && typeof (error as { message: unknown }).message === 'string') {
    return { message: (error as { message: string }).message }
  }
  try {
    return { message: JSON.stringify(error) ?? String(error) }
  } catch {
    return { message: String(error) }
  }
}

export function reportClientError(kind: ClientErrorKind, error: unknown, details: ClientErrorDetails = {}): void {
  const current = state
  if (!current || isAbort(error)) return
  try {
    const described = describe(error)
    const message = truncate(described.message, MAX_MESSAGE_CHARS)
    if (!message || IGNORED_MESSAGES.some(pattern => pattern.test(message))) return
    details = { ...apiFailureDetails(error), ...details }
    const requestPath = withoutQuery(details.request_path)
    const requestMethod = truncate(details.request_method?.toUpperCase(), 16)

    const now = current.now()
    if (now < current.suspendedUntil || current.sentTotal >= MAX_REPORTS_PER_PAGE_LOAD) return
    const stack = truncate(details.stack ?? described.stack, MAX_STACK_CHARS)
    const fingerprint = [kind, message, requestMethod ?? '', requestPath ?? '', stack?.split('\n', 2).join('\n') ?? ''].join('|')
    const previous = current.lastSentAt.get(fingerprint)
    if (previous !== undefined && now - previous < DUPLICATE_WINDOW_MS) return
    if (now - current.windowStartedAt >= 60_000) {
      current.windowStartedAt = now
      current.sentInWindow = 0
    }
    if (current.sentInWindow >= MAX_REPORTS_PER_MINUTE) return

    current.lastSentAt.set(fingerprint, now)
    current.sentInWindow += 1
    current.sentTotal += 1
    const report: ClientErrorReport = {
      kind,
      message,
      name: truncate(details.name ?? described.name, 128),
      stack,
      component_stack: truncate(details.component_stack, MAX_COMPONENT_STACK_CHARS),
      source: withoutQuery(details.source),
      page: withoutQuery(details.page ?? current.location()),
      request_method: requestMethod,
      request_path: requestPath,
      request_status: details.request_status,
      request_id: truncate(details.request_id, 128),
      app_version: current.appVersion,
    }
    const undelivered = () => {
      if (state !== current) return
      // Not delivered: offline, controller unreachable, not signed in, or over
      // the controller's limit. Give the report back to the page budget so a
      // sleep/wake cycle cannot exhaust it, and pause instead of hammering.
      current.sentTotal = Math.max(0, current.sentTotal - 1)
      current.lastSentAt.delete(fingerprint)
      current.suspendedUntil = current.now() + REFUSED_BACKOFF_MS
    }
    void current.sender(report).then(status => {
      if (status === undefined || status < 200 || status >= 300) undelivered()
    }, undelivered)
  } catch {
    // Reporting is best-effort and must never disturb the caller.
  }
}

export interface ClientErrorReportingOptions {
  appVersion?: string
  target?: Window
  now?: () => number
}

// installClientErrorReporting starts forwarding failures through send and
// returns an uninstall function. Calling it again replaces the previous install.
export function installClientErrorReporting(send: ClientErrorSender, options: ClientErrorReportingOptions = {}): () => void {
  const target = options.target ?? window
  const onError = (event: ErrorEvent) => {
    // Strip the script URL's query before appending the position, or the
    // query removal would take the line and column with it.
    const source = event.filename ? `${withoutQuery(event.filename)}:${event.lineno}:${event.colno}` : undefined
    reportClientError('error', event.error ?? event.message, { source })
  }
  const onRejection = (event: PromiseRejectionEvent) => {
    reportClientError('unhandledrejection', event.reason)
  }
  uninstallCurrent?.()
  state = {
    sender: send,
    appVersion: options.appVersion,
    now: options.now ?? Date.now,
    location: () => target.location?.href ?? '',
    sentTotal: 0,
    windowStartedAt: 0,
    sentInWindow: 0,
    suspendedUntil: 0,
    lastSentAt: new Map(),
  }
  const installed = state
  target.addEventListener('error', onError)
  target.addEventListener('unhandledrejection', onRejection)
  const uninstall = () => {
    target.removeEventListener('error', onError)
    target.removeEventListener('unhandledrejection', onRejection)
    if (state === installed) state = null
    if (uninstallCurrent === uninstall) uninstallCurrent = null
  }
  uninstallCurrent = uninstall
  return uninstall
}
