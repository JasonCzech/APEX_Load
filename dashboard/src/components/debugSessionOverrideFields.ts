import type { CanonicalRuntimeGroupId } from './runtime-settings/canonicalRuntimeTaxonomy'
import {
  LOG_MAX_RESPONSE_BYTES,
  RETRY_BACKOFF_OPTIONS,
  SOAP_PASSWORD_TYPE_OPTIONS,
  SOAP_VERSION_OPTIONS,
  THINK_TIME_TYPE_OPTIONS,
  TLS_VERSION_OPTIONS,
  type OverrideOption,
} from './runtime-settings/scenarioOverrideOptions'
import { HTTP_VERSION_OPTIONS } from './httpVersionModel'
import { overrideFieldValue } from './runtimeSettingsModel'
import { sessionOverridePaths, type DebugSessionOverrideDraft } from './debugSessionOverrides'

// The fields the debugger's session runtime overrides editor offers, by
// canonical group. Kept apart from debugSessionOverrides.ts so this table and
// its option lists load with the lazy editor, not with the ScriptEditor chunk.

export type DebugOverrideFieldKind = 'text' | 'url' | 'number' | 'select' | 'tri' | 'secret' | 'username'

export interface DebugOverrideFieldSpec {
  path: string
  label: string
  kind: DebugOverrideFieldKind
  group: CanonicalRuntimeGroupId
  /** 'http' = HTTP-like protocols only; 'soap' = SOAP only; unset = every protocol. */
  scope?: 'http' | 'soap'
  options?: readonly OverrideOption[]
  min?: number
  max?: number
  /** Accepts decimals (think-time multiplier). */
  decimal?: boolean
  unit?: string
}

const PROXY_MODE_OPTIONS: readonly OverrideOption[] = [
  { value: 'none', label: 'None' },
  { value: 'system', label: 'System' },
  { value: 'pac', label: 'PAC script' },
  { value: 'custom', label: 'Custom' },
]

const AUTH_TYPE_OPTIONS: readonly OverrideOption[] = [
  { value: 'basic', label: 'Basic' },
  { value: 'bearer', label: 'Bearer' },
  { value: 'ntlm', label: 'NTLM' },
  { value: 'negotiate', label: 'Negotiate (NTLM)' },
  { value: 'negotiate_any', label: 'Any challenge (Basic, Digest, NTLM, Negotiate)' },
  { value: 'digest', label: 'Digest' },
]

const HTTP_VERSION_OVERRIDE_OPTIONS: readonly OverrideOption[] = HTTP_VERSION_OPTIONS
  .filter(option => option.value !== '')
  .map(option => ({ value: option.value, label: option.label }))

// Timeouts and max connections per host are omitempty ints where 0 means the
// engine default (dsl.HTTPConfig), so an override starts at 1.
export const DEBUG_OVERRIDE_FIELDS: readonly DebugOverrideFieldSpec[] = [
  { path: 'base_url', label: 'Base URL', kind: 'url', group: 'target_identity' },
  { path: 'http.user_agent', label: 'User agent', kind: 'text', group: 'target_identity', scope: 'http' },
  { path: 'think_time.type', label: 'Think time type', kind: 'select', group: 'think_time', options: THINK_TIME_TYPE_OPTIONS },
  { path: 'think_time.mean_ms', label: 'Think time', kind: 'number', group: 'think_time', min: 0, unit: 'ms' },
  { path: 'think_time.min_ms', label: 'Minimum think time', kind: 'number', group: 'think_time', min: 0, unit: 'ms' },
  { path: 'think_time.max_ms', label: 'Maximum think time', kind: 'number', group: 'think_time', min: 0, unit: 'ms' },
  { path: 'think_time.multiplier', label: 'Think time multiplier', kind: 'number', group: 'think_time', min: 0, decimal: true },
  { path: 'think_time.limit_ms', label: 'Think time limit', kind: 'number', group: 'think_time', min: 0, unit: 'ms' },
  { path: 'http.version', label: 'HTTP version', kind: 'select', group: 'transport_connections', scope: 'http', options: HTTP_VERSION_OVERRIDE_OPTIONS },
  { path: 'http.request_timeout_ms', label: 'Request timeout', kind: 'number', group: 'transport_connections', scope: 'http', min: 1, unit: 'ms' },
  { path: 'http.connect_timeout_ms', label: 'Connect timeout', kind: 'number', group: 'transport_connections', scope: 'http', min: 1, unit: 'ms' },
  { path: 'http.tls_timeout_ms', label: 'TLS handshake timeout', kind: 'number', group: 'transport_connections', scope: 'http', min: 1, unit: 'ms' },
  { path: 'http.idle_timeout_ms', label: 'Idle connection timeout', kind: 'number', group: 'transport_connections', scope: 'http', min: 1, unit: 'ms' },
  { path: 'http.max_conns_per_host', label: 'Max connections per host', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.keep_alive', label: 'Keep-alive', kind: 'tri', group: 'transport_connections', scope: 'http' },
  { path: 'http.tls.min_version', label: 'Minimum TLS version', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: TLS_VERSION_OPTIONS },
  { path: 'http.tls.max_version', label: 'Maximum TLS version', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: TLS_VERSION_OPTIONS },
  { path: 'http.tls.insecure_skip_verify', label: 'Skip TLS verification', kind: 'tri', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy_mode', label: 'Proxy mode', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: PROXY_MODE_OPTIONS },
  { path: 'http.proxy.url', label: 'Proxy URL', kind: 'text', group: 'security_proxy_auth', scope: 'http' },
  // Proxy mode offers PAC script, which validation refuses without a PAC URL.
  { path: 'http.proxy.pac_url', label: 'PAC script URL', kind: 'url', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy.username', label: 'Proxy username', kind: 'username', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy.password', label: 'Proxy password', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.type', label: 'Auth type', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: AUTH_TYPE_OPTIONS },
  { path: 'http.auth.username', label: 'Auth username', kind: 'username', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.password', label: 'Auth password', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.token', label: 'Auth token', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.domain', label: 'Auth domain', kind: 'text', group: 'security_proxy_auth', scope: 'http' },
  { path: 'soap.ws_security.username_token.username', label: 'WS-Security username', kind: 'username', group: 'security_proxy_auth', scope: 'soap' },
  { path: 'soap.ws_security.username_token.password', label: 'WS-Security password', kind: 'secret', group: 'security_proxy_auth', scope: 'soap' },
  { path: 'soap.ws_security.username_token.password_type', label: 'WS-Security password type', kind: 'select', group: 'security_proxy_auth', scope: 'soap', options: SOAP_PASSWORD_TYPE_OPTIONS },
  { path: 'soap.ws_security.timestamp.ttl_seconds', label: 'WS-Security timestamp TTL', kind: 'number', group: 'security_proxy_auth', scope: 'soap', min: 0, unit: 's' },
  { path: 'http.max_redirects', label: 'Max redirects', kind: 'number', group: 'protocol_behavior_session', scope: 'http', min: 0 },
  { path: 'http.cookies_enabled', label: 'Cookies', kind: 'tri', group: 'protocol_behavior_session', scope: 'http' },
  { path: 'soap.version', label: 'SOAP version', kind: 'select', group: 'protocol_behavior_session', scope: 'soap', options: SOAP_VERSION_OPTIONS },
  { path: 'soap.fault_handling.fail_on_fault', label: 'Fail the action on a SOAP fault', kind: 'tri', group: 'protocol_behavior_session', scope: 'soap' },
  { path: 'error_handling.max_retries', label: 'Max retries', kind: 'number', group: 'errors_logging_diagnostics', min: 0 },
  { path: 'error_handling.retry_delay_ms', label: 'Retry delay', kind: 'number', group: 'errors_logging_diagnostics', min: 0, unit: 'ms' },
  { path: 'error_handling.retry_backoff', label: 'Retry backoff', kind: 'select', group: 'errors_logging_diagnostics', options: RETRY_BACKOFF_OPTIONS },
  { path: 'error_handling.snapshot_on_error', label: 'Snapshot on error', kind: 'tri', group: 'errors_logging_diagnostics' },
  { path: 'log.on_error_only', label: 'Extended log only on errors', kind: 'tri', group: 'errors_logging_diagnostics' },
  { path: 'log.max_response_bytes', label: 'Max logged response size', kind: 'number', group: 'errors_logging_diagnostics', min: 0, max: LOG_MAX_RESPONSE_BYTES, unit: 'bytes' },
]

/** The header map whose keys the editor overrides one at a time (http.default_headers.NAME). */
export const DEBUG_HEADER_MAP_PATH = 'http.default_headers'

const HTTP_LIKE = new Set(['http', 'soap', 'java_http'])

export function isDebugHttpLikeProtocol(protocol?: string): boolean {
  const normalized = (protocol || 'http').trim().toLowerCase()
  return HTTP_LIKE.has(normalized) || normalized.startsWith('http/')
}

/** The fields a group offers for a protocol. */
export function debugOverrideFieldsFor(group: CanonicalRuntimeGroupId, protocol?: string): DebugOverrideFieldSpec[] {
  const httpLike = isDebugHttpLikeProtocol(protocol)
  const soap = (protocol || '').trim().toLowerCase() === 'soap'
  return DEBUG_OVERRIDE_FIELDS.filter(field => field.group === group
    && (field.scope === undefined || (field.scope === 'http' ? httpLike : soap)))
}

/** The session's per-header overrides (name → value; undefined = explicit removal). */
export function sessionHeaderOverrides(draft?: DebugSessionOverrideDraft): Array<{ name: string; path: string; value: unknown }> {
  const prefix = `${DEBUG_HEADER_MAP_PATH}.`
  return sessionOverridePaths(draft)
    .filter(path => path.startsWith(prefix))
    .map(path => ({ name: path.slice(prefix.length), path, value: overrideFieldValue(draft, path) }))
}

/** Every overridden path no field control renders (so each still has a reset). */
export function unrenderedSessionOverrides(draft: DebugSessionOverrideDraft | undefined, protocol?: string): string[] {
  const httpLike = isDebugHttpLikeProtocol(protocol)
  const rendered = new Set(DEBUG_OVERRIDE_FIELDS.filter(field => field.scope === undefined || (field.scope === 'http' ? httpLike : (protocol || '').toLowerCase() === 'soap')).map(field => field.path))
  return sessionOverridePaths(draft).filter(path => !rendered.has(path) && !(httpLike && path.startsWith(`${DEBUG_HEADER_MAP_PATH}.`)))
}
