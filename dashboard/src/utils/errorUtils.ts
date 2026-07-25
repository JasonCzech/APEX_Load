export type ErrorDiagnosticSeverity = 'info' | 'warning' | 'error' | 'critical'

export type ErrorDiagnosticCategory =
  | 'api_unreachable'
  | 'api_route'
  | 'auth'
  | 'permission'
  | 'validation'
  | 'capacity'
  | 'script_shakeout'
  | 'import'
  | 'download'
  | 'telemetry'
  | 'network'
  | 'unknown'

export type ErrorDiagnostic = {
  severity: ErrorDiagnosticSeverity
  category: ErrorDiagnosticCategory
  summary: string
  likelyCause?: string
  nextSteps: string[]
  evidence?: string[]
  rawMessage?: string
  status?: number
  code?: string
}

type ErrorDetails = {
  message: string
  status?: number
  code?: string
  payload?: unknown
  name?: string
}

const UNKNOWN_NEXT_STEPS = [
  'Retry the action once.',
  'If it repeats, capture this message and the action you were performing.',
]

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim().length > 0 ? value.trim() : undefined
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  if (typeof value === 'string' && /^\d+$/.test(value.trim())) {
    return Number(value.trim())
  }
  return undefined
}

// SENSITIVE_EVIDENCE_PATTERNS masks credential-shaped values in free-form server
// text before it is shown as diagnostic evidence. The server should not echo
// secrets, but error/response previews are surfaced verbatim to the user, so this
// is defense in depth against an upstream leak. The scheme pattern runs first so
// "Authorization: Bearer <tok>" loses the token before the key/value pass.
const SENSITIVE_EVIDENCE_PATTERNS: { re: RegExp; replace: (...args: string[]) => string }[] = [
  // Bearer / Basic credential following an auth scheme keyword.
  {
    re: /\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}/gi,
    replace: (_match, scheme) => `${scheme} [redacted]`,
  },
  // name: value / name=value, with an optionally JSON-quoted name, for known
  // sensitive field names. The value may be quoted or an unquoted run.
  {
    re: /(["']?)(authorization|password|passwd|secret|client[_-]?secret|token|access[_-]?token|refresh[_-]?token|api[_-]?key|apikey|x-api-key|private[_-]?key)\1\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,}&]+)/gi,
    replace: (_match, _quote, name) => `${name}=[redacted]`,
  },
]

function redactSensitiveEvidence(value: string): string {
  let out = value
  for (const { re, replace } of SENSITIVE_EVIDENCE_PATTERNS) {
    out = out.replace(re, replace as (substring: string, ...args: unknown[]) => string)
  }
  return out
}

function truncateEvidence(value: string, maxLength = 160): string {
  const compact = redactSensitiveEvidence(value.replace(/\s+/g, ' ').trim())
  return compact.length > maxLength ? `${compact.slice(0, maxLength - 1)}...` : compact
}

function extractPayloadCode(payload: unknown): string | undefined {
  return isRecord(payload) ? stringValue(payload.code) : undefined
}

function extractErrorDetails(error: unknown, fallback: string): ErrorDetails {
  const record = isRecord(error) ? error : undefined
  const payload = record && 'payload' in record ? record.payload : record
  const payloadRecord = isRecord(payload) ? payload : undefined
  const message = error instanceof Error
    ? stringValue(error.message) || fallback
    : typeof error === 'string'
      ? stringValue(error) || fallback
      : record
        ? stringValue(record.message) || stringValue(record.error) || stringValue(record.detail) || stringValue(record.title) || fallback
        : fallback

  return {
    message,
    status: numberValue(record?.status) ?? numberValue(payloadRecord?.status),
    code: stringValue(record?.code) ?? extractPayloadCode(payload),
    payload,
    name: error instanceof Error ? error.name : stringValue(record?.name),
  }
}

function genericMessage(message: string): boolean {
  return /^(bad request|conflict|failed to fetch|forbidden|internal server error|load failed|network error|networkerror|not found|too many requests|unauthorized|unprocessable entity)$/i.test(message.trim())
}

function summaryOr(details: ErrorDetails, replacement: string): string {
  // The raw server message is surfaced to the user when it is non-generic, so
  // redact credential-shaped values (Bearer/Basic, secret key:value) the same way
  // evidence previews are, in case an upstream error echoes a token.
  return genericMessage(details.message) ? replacement : redactSensitiveEvidence(details.message)
}

function collectEvidence(details: ErrorDetails): string[] {
  const evidence: string[] = []
  if (details.status) {
    evidence.push(`HTTP status: ${details.status}`)
  }
  if (details.code) {
    evidence.push(`Error code: ${details.code}`)
  }

  if (!isRecord(details.payload)) {
    return evidence
  }

  const contentType = stringValue(details.payload.content_type)
  if (contentType) {
    evidence.push(`Response content type: ${contentType}`)
  }

  const reason = stringValue(details.payload.reason)
  if (reason) {
    evidence.push(`Reason: ${truncateEvidence(reason)}`)
  }

  const detail = stringValue(details.payload.details) || stringValue(details.payload.detail)
  if (detail) {
    evidence.push(`Details: ${truncateEvidence(detail)}`)
  }

  const field = stringValue(details.payload.field)
  if (field) {
    evidence.push(`Field: ${field}`)
  }

  const body = stringValue(details.payload.body)
  if (body && !body.trim().startsWith('<')) {
    evidence.push(`Response preview: ${truncateEvidence(body)}`)
  }

  const advisory = details.payload.capacity_advisory
  if (isRecord(advisory)) {
    const available = numberValue(advisory.available_generators)
    const recommended = numberValue(advisory.recommended_generators)
    if (available !== undefined && recommended !== undefined) {
      evidence.push(`Generators: ${available} available, ${recommended} recommended`)
    }
  }

  const shakeout = details.payload.script_shakeout
  if (isRecord(shakeout)) {
    const shakeoutSummary = stringValue(shakeout.summary)
    if (shakeoutSummary) {
      evidence.push(`Shakeout: ${truncateEvidence(shakeoutSummary)}`)
    }
  }

  return evidence
}

function diagnostic(
  details: ErrorDetails,
  category: ErrorDiagnosticCategory,
  severity: ErrorDiagnosticSeverity,
  summary: string,
  likelyCause: string,
  nextSteps: string[],
): ErrorDiagnostic {
  const evidence = collectEvidence(details)
  return {
    category,
    severity,
    summary,
    likelyCause,
    nextSteps,
    evidence: evidence.length > 0 ? evidence : undefined,
    // Redact like evidence: the raw error message can echo a Bearer/Basic token
    // or key:value secret that must not reach the user-visible diagnostic.
    rawMessage: redactSensitiveEvidence(details.message),
    status: details.status,
    code: details.code,
  }
}

function isApiUnreachable(details: ErrorDetails): boolean {
  const text = `${details.name || ''} ${details.message}`.toLowerCase()
  return /failed to fetch|networkerror|network error|load failed|err_connection_refused|connection refused|connection reset|network request failed|controller is unreachable/.test(text)
}

function isDashboardHtmlFallback(details: ErrorDetails): boolean {
  const text = details.message.toLowerCase()
  const contentType = isRecord(details.payload) ? stringValue(details.payload.content_type)?.toLowerCase() : undefined
  return text.includes('dashboard html instead of json') || Boolean(contentType?.includes('text/html'))
}

export function diagnoseError(error: unknown, fallback: string): ErrorDiagnostic {
  const details = extractErrorDetails(error, fallback)
  const message = details.message.toLowerCase()
  const code = details.code

  if (code === 'capacity_shortfall' || /capacity shortfall|need more generator|not enough generator/.test(message)) {
    return diagnostic(
      details,
      'capacity',
      'warning',
      summaryOr(details, 'Generator capacity is below the recommended level'),
      'The run needs more available load generator capacity than the controller currently sees.',
      [
        'Review the capacity advisory on the run setup or execution screen.',
        'Add or enable generators, lower the VUser target, or explicitly continue with the shortfall when that is acceptable.',
      ],
    )
  }

  if (code === 'script_shakeout_failed' || /script shakeout|shakeout review/.test(message)) {
    return diagnostic(
      details,
      'script_shakeout',
      'warning',
      summaryOr(details, 'Script shakeout needs review'),
      'The pre-run script check found evidence that may fail or distort the full load test.',
      [
        'Open the script shakeout evidence and fix the failing action, assertion, selector, or target dependency.',
        'Continue only when the failure is expected and you want the full load test to proceed anyway.',
      ],
    )
  }

  if (isApiUnreachable(details)) {
    return diagnostic(
      details,
      'api_unreachable',
      'critical',
      'API controller is unreachable',
      'The browser could not reach the controller API origin.',
      [
        'Confirm the Docker Compose stack or controller process is running.',
        'Check that the dashboard API origin and published ports match the controller address.',
        'Reload the dashboard after the controller health check responds.',
      ],
    )
  }

  if (isDashboardHtmlFallback(details)) {
    return diagnostic(
      details,
      'api_route',
      'error',
      summaryOr(details, 'Dashboard and controller API are out of sync'),
      'The dashboard received HTML where it expected JSON, usually because a backend route is missing or the dev proxy points at an older controller.',
      [
        'Restart or rebuild the controller so it serves the routes expected by this dashboard.',
        'Verify the Vite proxy or API origin points at the active controller.',
      ],
    )
  }

  if (details.status === 401 || /\bunauthorized\b/.test(message)) {
    return diagnostic(
      details,
      'auth',
      'warning',
      summaryOr(details, 'Sign-in is required'),
      'The controller rejected the request because the dashboard does not have an authenticated session.',
      [
        'Sign in again or refresh the upstream access gateway session.',
        'If you are in local development, verify the intended auth mode is configured.',
      ],
    )
  }

  if (/request origin is not allowed|credentialed requests require an explicit allowed origin/.test(message)) {
    return diagnostic(
      details,
      'network',
      'warning',
      summaryOr(details, 'Dashboard origin is not allowed by the controller'),
      'The request reached the controller, but its browser Origin does not match the API host or an explicitly allowed origin.',
      [
        'When using the local Vite dashboard, restart Vite so its API proxy rewrites the request origin.',
        'For a separately hosted dashboard, add its exact origin to APEXLOAD_ALLOWED_ORIGINS and restart the controller.',
        'Do not change project roles for this error; authorization is evaluated after the origin policy passes.',
      ],
    )
  }

  if (details.status === 403 || /\bforbidden\b|access denied|not allowed|permission denied/.test(message)) {
    return diagnostic(
      details,
      'permission',
      'warning',
      summaryOr(details, 'Current session is not allowed to perform this action'),
      'The session is authenticated, but it does not have the project or system permission required for this action.',
      [
        'Switch to a project where you have the required role, or ask an administrator to update your access.',
        'If you are testing local role behavior, check the active dev view mode.',
      ],
    )
  }

  if (details.status === 404 || /\bnot found\b/.test(message)) {
    return diagnostic(
      details,
      'api_route',
      'error',
      summaryOr(details, 'Requested API route or resource was not found'),
      'The dashboard asked for a route or record that the controller could not find.',
      [
        'Refresh the page to clear stale IDs.',
        'If the route itself is missing, rebuild the controller and confirm the dashboard API origin.',
      ],
    )
  }

  if (details.status === 429 || /rate limit|too many requests/.test(message)) {
    return diagnostic(
      details,
      'network',
      'warning',
      summaryOr(details, 'Request rate limit reached'),
      'The controller or upstream gateway throttled the request.',
      [
        'Wait a moment and retry the action.',
        'If this repeats during normal use, review gateway or controller rate-limit settings.',
      ],
    )
  }

  if (details.status === 400 || details.status === 422 || /bad request|unprocessable entity|invalid|validation failed|required field|missing required|schema|json is invalid|cannot parse/.test(message)) {
    return diagnostic(
      details,
      'validation',
      'warning',
      summaryOr(details, 'The request needs valid input before it can continue'),
      'The controller rejected the payload because a field, script, JSON body, or schedule value did not pass validation.',
      [
        'Review the highlighted fields or script validation output.',
        'Fix missing, invalid, or incompatible values and submit again.',
      ],
    )
  }

  if (/import|conversion|convert|har|jmeter|postman|openapi|swagger|wsdl|loadrunner|devweb|truclient/.test(message)) {
    return diagnostic(
      details,
      'import',
      'error',
      summaryOr(details, 'Import or conversion failed'),
      'The uploaded asset could not be parsed or mapped into the APEX Load script model.',
      [
        'Check that the file type and archive contents match the selected importer.',
        'Review any conversion warnings, then retry with a smaller or corrected source artifact.',
      ],
    )
  }

  if (/download|export|bundle|artifact/.test(message)) {
    return diagnostic(
      details,
      'download',
      'error',
      summaryOr(details, 'Download or export failed'),
      'The controller could not prepare or stream the requested artifact.',
      [
        'Retry after confirming the run, archive, or report still exists.',
        'Check controller logs if the export repeatedly fails for the same artifact.',
      ],
    )
  }

  if (/prometheus|metrics|telemetry|websocket|web socket|stream|latency|generator inventory|generator status/.test(message)) {
    return diagnostic(
      details,
      'telemetry',
      'error',
      summaryOr(details, 'Telemetry data is unavailable'),
      'The dashboard could not load live or historical monitoring data from the controller or telemetry backend.',
      [
        'Confirm the controller and generator services are reachable.',
        'For Prometheus-backed views, verify the Prometheus URL and scrape targets.',
      ],
    )
  }

  if ((details.status && details.status >= 500) || /internal server error|bad gateway|service unavailable|gateway timeout/.test(message)) {
    return diagnostic(
      details,
      'unknown',
      'error',
      summaryOr(details, 'Controller reported a server error'),
      'The controller accepted the request but failed while processing it.',
      [
        'Retry once in case the failure was transient.',
        'Check controller logs with the action, route, and timestamp if it repeats.',
      ],
    )
  }

  if (/auth|authentication|session|sign-?in|login|oidc|trusted header/.test(message)) {
    return diagnostic(
      details,
      'auth',
      'warning',
      summaryOr(details, 'Authentication needs attention'),
      'The dashboard or controller reported an authentication/session problem.',
      [
        'Refresh the dashboard or sign in again.',
        'Verify the configured auth mode matches the environment.',
      ],
    )
  }

  return diagnostic(
    details,
    'unknown',
    'error',
    redactSensitiveEvidence(details.message) || fallback,
    'The dashboard does not recognize this error pattern yet.',
    UNKNOWN_NEXT_STEPS,
  )
}

export function getErrorMessage(error: unknown, fallback: string): string {
  return diagnoseError(error, fallback).summary
}
