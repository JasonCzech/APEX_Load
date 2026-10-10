import type { HTTPActionVersion, ScriptActionDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types/replay'
import { formatHTTPVersion, inheritedHTTPActionVersion } from './httpVersionModel'
export { inheritedHTTPActionVersion } from './httpVersionModel'
import { dslSpecForActionType } from './dslActionCatalog'

export const HTTP_ACTION_VERSION_OPTIONS = [
  { value: 'auto', label: 'Auto-negotiate (HTTP/2 or HTTP/1.1)' },
  { value: '1.1', label: 'Require HTTP/1.1' },
  { value: '2', label: 'Require HTTP/2 (no fallback)' },
  { value: 'auto+h3', label: 'Upgrade to HTTP/3 when advertised' },
  { value: '3', label: 'Require HTTP/3 (no fallback)' },
] as const
const HTTP_FAMILIES = new Set(['http', 'http/1.1', 'http/2', 'http/3', 'soap', 'java_http'])
const HTTP_METHODS = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'])
const NON_REQUEST_TYPES = new Set(['http_sequence', 'http_oauth2_token', 'cookie_clear', 'cookie_set', 'http_async_start', 'http_async_stop', 'http_sync', 'cache_clear', 'http_connections_close', 'rendezvous', 'error_message', 'review_required'])
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)

export function isHTTPActionVersion(value: unknown): value is HTTPActionVersion {
  return value === 'auto' || value === '1.1' || value === '2' || value === 'auto+h3' || value === '3'
}

export function supportsHTTPActionVersion(action: ScriptActionDefinition, protocol: string): boolean {
  if (typeof protocol !== 'string' || typeof action.type !== 'string' || !HTTP_FAMILIES.has(protocol.trim().toLowerCase())) return false
  const type = action.type.trim().toLowerCase()
  if (!type || NON_REQUEST_TYPES.has(type) || type.startsWith('control_') || type.startsWith('source_') || type.startsWith('loadrunner_')
    || dslSpecForActionType(type)?.protocols.includes('*')) return false
  // Match the engine's explicit-method precedence, including legacy request-shaped leaves.
  if (action.method !== undefined && typeof action.method !== 'string') return false
  const method = action.method?.trim() || type
  return HTTP_METHODS.has(method.toUpperCase())
}

export function formatHTTPActionVersion(policy: HTTPActionVersion): string {
  return formatHTTPVersion(policy === 'auto' ? undefined : policy)
}

export function httpActionVersionSettings(action: ScriptActionDefinition, protocol: string, scriptVersion?: string) {
  if (!supportsHTTPActionVersion(action, protocol)) return null
  const present = own(action, 'http_version')
  const raw: unknown = action.http_version
  const valid = !present || isHTTPActionVersion(raw)
  const inherited = inheritedHTTPActionVersion(protocol, scriptVersion)
  const policy = present ? isHTTPActionVersion(raw) ? raw : null : inherited
  return {
    present, valid, inherited, policy,
    selection: !present ? 'inherit' : isHTTPActionVersion(raw) ? raw : 'invalid',
    inheritedLabel: inherited ? formatHTTPActionVersion(inherited) : 'Unsupported script HTTP version',
    effectiveLabel: policy ? formatHTTPActionVersion(policy) : 'Unsupported HTTP version policy',
  }
}

/** An explicit control edit preserves every other field, including unknown keys. */
export function setHTTPActionVersion(action: ScriptActionDefinition, policy: HTTPActionVersion | undefined): ScriptActionDefinition {
  if (policy !== undefined && !isHTTPActionVersion(policy)) return action
  const next = { ...action }
  if (policy === undefined) delete next.http_version
  else next.http_version = policy
  return next
}

/** Observations retain their original metadata; unknown strings are not inferred as facts. */
export function capturedHTTPVersionLabel(value: unknown, state?: unknown): string {
  if (state === 'unavailable') return 'Not observed'
  if (state === 'draft') {
    // A draft token (h3-29) is kept as the raw observation; show it only
    // when it has the bounded token shape.
    return typeof value === 'string' && /^h3-[A-Za-z0-9]{1,16}$/.test(value.trim())
      ? `Draft HTTP/3 (${value.trim()}; replays on the inherited policy)`
      : 'Draft HTTP/3 (replays on the inherited policy)'
  }
  if (state === 'unsupported') return 'Unsupported captured HTTP version (source value not retained)'
  if (state === 'malformed') return 'Malformed captured HTTP version (source value not retained)'
  if (state === 'available' && (value === undefined || value === null || value === '')) return 'Captured version not retained'
  if (typeof value !== 'string' || !value.trim() || value.trim().toLowerCase() === 'unknown') return 'Not observed'
  switch (value.trim().toUpperCase()) {
    case '1.0': case 'HTTP/1.0': return 'HTTP/1.0 (captured; unsupported for replay)'
    case '1.1': case 'HTTP/1.1': return 'HTTP/1.1'
    case '2': case 'HTTP/2': case 'HTTP/2.0': return 'HTTP/2'
    case '3': case 'H3': case 'HTTP/3': case 'HTTP/3.0': return 'HTTP/3'
    default: return 'Unrecognized captured version'
  }
}

const CAPTURE_STATES = new Set(['available', 'unavailable', 'malformed', 'unsupported', 'draft'])
const IMPORT_MODE_LABELS: Record<string, string> = {
  upgrade: 'Upgrade captured HTTP/3 (auto+h3)', exact: 'Keep captured versions exactly', inherit: 'Inherit the script setting',
}
const record = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null

/** Bounded captured observations are independent of authored and replay policy. */
export function httpCapturedVersionEvidence(metadata: ScriptActionDefinition['metadata']) {
  const source = metadata || {}
  const candidate = record(source.http_version_capture)
  const capture = candidate?.version === 1 && CAPTURE_STATES.has(candidate.request_state as string) && CAPTURE_STATES.has(candidate.response_state as string) ? candidate : null
  const legacyProvenance = record(source['har.http_version_provenance'])
  const legacyProxyLegs = legacyProvenance?.version === 1 && Object.keys(legacyProvenance).length === 3
    && legacyProvenance.request_leg === 'client_to_proxy' && legacyProvenance.response_leg === 'proxy_to_origin'
  const proxyLegs = capture?.provenance === 'proxy_legs' && capture.proxy === true
    || (!capture?.provenance && legacyProxyLegs)
  const browser = capture?.provenance === 'browser' && capture.proxy === false
  const rawRequest = own(source, 'har.request.http_version') ? source['har.request.http_version'] : source.http_version
  const observation = (key: 'request' | 'response', raw: unknown) => {
    const bounded = capture?.[`${key}_version`]
    const value = bounded === '1.1' || bounded === '2' || bounded === '3' ? bounded : raw
    return capturedHTTPVersionLabel(value, capture?.[`${key}_state`])
  }
  return {
    proxyLegs: Boolean(proxyLegs),
    requestLabel: proxyLegs ? 'Captured request HTTP version (client to proxy)' : browser ? 'Captured request HTTP version (browser)' : 'Captured request HTTP version',
    responseLabel: proxyLegs ? 'Captured response HTTP version (proxy to origin)' : browser ? 'Captured response HTTP version (browser)' : 'Captured response HTTP version',
    request: observation('request', rawRequest),
    response: observation('response', source['har.response.http_version']),
    // The http_version_mode that turned the observation into this request's
    // imported policy; later edits change the policy, not this record.
    importMode: typeof capture?.mode === 'string' && own(IMPORT_MODE_LABELS, capture.mode) ? IMPORT_MODE_LABELS[capture.mode] : null,
  }
}

export function httpReplayVersionEvidence(evidence: ProtocolEvidence | undefined) {
  if (!evidence || !['http_version_policy', 'http_version_negotiated', 'http_version_source'].some(key => own(evidence, key))) return null
  const source: unknown = evidence.http_version_source
  const negotiated: unknown = evidence.http_version_negotiated
  const policy: unknown = evidence.http_version_policy
  return {
    policy: isHTTPActionVersion(policy) ? formatHTTPActionVersion(policy) : 'Not retained',
    actual: source === 'wire' && (negotiated === '1.1' || negotiated === '2' || negotiated === '3')
      ? `HTTP/${negotiated}` : source === 'cache' ? 'Not measured (cached response)' : 'Not observed',
    source: source === 'wire' ? 'Wire response' : source === 'cache' ? 'Cached response' : 'Not observed',
    // Observation only: the wire response advertised final HTTP/3 in Alt-Svc.
    // An auto+h3 request routed to the alternative acted on it; its receipt
    // rows say what happened instead.
    advertisesHTTP3: source === 'wire' && evidence.alt_svc_h3_advertised === true && negotiated !== '3' &&
      !(policy === 'auto+h3' && evidence.http_version_selection_reason === 'upgraded'),
    // auto+h3 caches a same-host advertisement from this response for later
    // requests to the origin.
    upgradesLater: policy === 'auto+h3' && evidence.http_version_selection_reason === 'not_advertised',
    http3: http3ReceiptRows(evidence),
  }
}

const quicConnectionLabels: Record<string, string> = {
  new: 'New connection', reused: 'Reused connection', shared_pending: 'Joined a connection still being opened',
  dial_failed: 'Could not open a connection',
}
const certaintyLabels: Record<string, string> = {
  not_sent: 'Not sent', possibly_sent: 'Possibly sent (the target may have processed it)', rejected_unprocessed: 'Rejected unprocessed by the server',
}
const selectionLabels: Record<string, string> = {
  exact_policy: 'Exact HTTP/3 policy', proxy_route: 'Refused: the route is a proxy', integrated_auth: 'Refused: integrated authentication runs on TCP',
}
// auto+h3 never refuses: a request that cannot use HTTP/3 runs on TCP.
const upgradeSelectionLabels: Record<string, string> = {
  upgraded: 'Upgraded to the advertised HTTP/3 alternative',
  not_advertised: 'TCP: the origin has not advertised HTTP/3 yet',
  broken_cache: 'TCP: HTTP/3 to this origin failed recently',
  tls_policy: 'TCP: the TLS maximum is below 1.3',
  proxy_route: 'TCP: the route is a proxy',
  integrated_auth: 'TCP: integrated authentication runs on TCP',
}
const fallbackLabels: Record<string, string> = {
  quic_connect_failed: 'Ran on TCP: QUIC could not connect',
  h3_version_fallback: 'Ran on HTTP/1.1: the server asked for an earlier HTTP version',
  misdirected_request: 'Ran on TCP: the HTTP/3 alternative answered 421 Misdirected Request',
}

/** HTTP/3 receipt rows; only known enum values are shown, never raw strings. */
function http3ReceiptRows(evidence: ProtocolEvidence): Array<{ label: string; value: string }> {
  if (evidence.quic_attempted === undefined && evidence.http_version_selection_reason === undefined) return []
  const rows: Array<{ label: string; value: string }> = []
  const selection = evidence.http_version_selection_reason
  const labels = evidence.http_version_policy === 'auto+h3' ? upgradeSelectionLabels : selectionLabels
  if (typeof selection === 'string' && own(labels, selection)) rows.push({ label: 'HTTP/3 routing', value: labels[selection] })
  const fallback = evidence.http_version_fallback
  if (typeof fallback === 'string' && own(fallbackLabels, fallback)) rows.push({ label: 'HTTP/3 fallback', value: fallbackLabels[fallback] })
  const connection = evidence.quic_connection
  if (typeof connection === 'string' && quicConnectionLabels[connection]) {
    rows.push({ label: 'QUIC connection', value: quicConnectionLabels[connection] + (evidence.tls_resumed === true ? ' (TLS resumed)' : '') })
  }
  if (typeof evidence.attempt_count === 'number' && Number.isSafeInteger(evidence.attempt_count) && evidence.attempt_count >= 0) {
    rows.push({ label: 'HTTP attempts', value: String(evidence.attempt_count) })
  }
  const certainty = evidence.http3_failure_certainty
  if (typeof certainty === 'string' && certaintyLabels[certainty]) rows.push({ label: 'Failure certainty', value: certaintyLabels[certainty] })
  if (evidence.tls_in_transport_handshake === true) rows.push({ label: 'TLS timing', value: 'Part of the QUIC handshake (counted in connect time)' })
  return rows
}
