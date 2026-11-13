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
import { effectiveRuntimeFieldLabel } from './runtime-settings/effectiveRuntimeSummaryModel'

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
const AUTHORED_DEBUG_OVERRIDE_FIELDS: readonly Omit<DebugOverrideFieldSpec, 'label'>[] = [
  { path: 'base_url', kind: 'url', group: 'target_identity' },
  { path: 'http.user_agent', kind: 'text', group: 'target_identity', scope: 'http' },
  { path: 'think_time.type', kind: 'select', group: 'think_time', options: THINK_TIME_TYPE_OPTIONS },
  { path: 'think_time.mean_ms', kind: 'number', group: 'think_time', min: 0 },
  { path: 'think_time.min_ms', kind: 'number', group: 'think_time', min: 0 },
  { path: 'think_time.max_ms', kind: 'number', group: 'think_time', min: 0 },
  { path: 'think_time.multiplier', kind: 'number', group: 'think_time', min: 0, decimal: true },
  { path: 'think_time.limit_ms', kind: 'number', group: 'think_time', min: 0 },
  { path: 'http.version', kind: 'select', group: 'transport_connections', scope: 'http', options: HTTP_VERSION_OVERRIDE_OPTIONS },
  { path: 'http.request_timeout_ms', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.connect_timeout_ms', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.tls_timeout_ms', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.idle_timeout_ms', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.max_conns_per_host', kind: 'number', group: 'transport_connections', scope: 'http', min: 1 },
  { path: 'http.keep_alive', kind: 'tri', group: 'transport_connections', scope: 'http' },
  { path: 'http.tls.min_version', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: TLS_VERSION_OPTIONS },
  { path: 'http.tls.max_version', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: TLS_VERSION_OPTIONS },
  { path: 'http.tls.insecure_skip_verify', kind: 'tri', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy_mode', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: PROXY_MODE_OPTIONS },
  { path: 'http.proxy.url', kind: 'text', group: 'security_proxy_auth', scope: 'http' },
  // Proxy mode offers PAC script, which validation refuses without a PAC URL.
  { path: 'http.proxy.pac_url', kind: 'url', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy.username', kind: 'username', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.proxy.password', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.type', kind: 'select', group: 'security_proxy_auth', scope: 'http', options: AUTH_TYPE_OPTIONS },
  { path: 'http.auth.username', kind: 'username', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.password', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.token', kind: 'secret', group: 'security_proxy_auth', scope: 'http' },
  { path: 'http.auth.domain', kind: 'text', group: 'security_proxy_auth', scope: 'http' },
  { path: 'soap.ws_security.username_token.username', kind: 'username', group: 'security_proxy_auth', scope: 'soap' },
  { path: 'soap.ws_security.username_token.password', kind: 'secret', group: 'security_proxy_auth', scope: 'soap' },
  { path: 'soap.ws_security.username_token.password_type', kind: 'select', group: 'security_proxy_auth', scope: 'soap', options: SOAP_PASSWORD_TYPE_OPTIONS },
  { path: 'soap.ws_security.timestamp.ttl_seconds', kind: 'number', group: 'security_proxy_auth', scope: 'soap', min: 0 },
  { path: 'http.max_redirects', kind: 'number', group: 'protocol_behavior_session', scope: 'http', min: 0 },
  { path: 'http.cookies_enabled', kind: 'tri', group: 'protocol_behavior_session', scope: 'http' },
  { path: 'soap.version', kind: 'select', group: 'protocol_behavior_session', scope: 'soap', options: SOAP_VERSION_OPTIONS },
  { path: 'soap.fault_handling.fail_on_fault', kind: 'tri', group: 'protocol_behavior_session', scope: 'soap' },
  { path: 'error_handling.max_retries', kind: 'number', group: 'errors_logging_diagnostics', min: 0 },
  { path: 'error_handling.retry_delay_ms', kind: 'number', group: 'errors_logging_diagnostics', min: 0 },
  { path: 'error_handling.retry_backoff', kind: 'select', group: 'errors_logging_diagnostics', options: RETRY_BACKOFF_OPTIONS },
  { path: 'error_handling.snapshot_on_error', kind: 'tri', group: 'errors_logging_diagnostics' },
  { path: 'log.on_error_only', kind: 'tri', group: 'errors_logging_diagnostics' },
  { path: 'log.max_response_bytes', kind: 'number', group: 'errors_logging_diagnostics', min: 0, max: LOG_MAX_RESPONSE_BYTES },
]

// Each field takes the shared read-only view's name (effectiveRuntimeFieldLabel), unit
// included, so the debugger, the group editor and the run report call one field the same thing.
export const DEBUG_OVERRIDE_FIELDS: readonly DebugOverrideFieldSpec[] = AUTHORED_DEBUG_OVERRIDE_FIELDS.map(spec => ({ ...spec, label: effectiveRuntimeFieldLabel(spec.path) }))

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
