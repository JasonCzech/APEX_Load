import fieldSchema from '../../../pkg/api/import_settings_fields.json'
import { request } from '../hooks/apiClient'
import type { ScriptDefinition } from '../types/scripts'
import { getImportSummary } from './scriptEditorImportModel'
import { readImportedRuntimeFiles, readImportedRuntimeSettingDispositions } from './scriptEditorRuntimeSettingsModel'
import { draftRuntimeSettingNote, runtimeSettingEffects } from './importRuntimeSettingNotes'

export const CANONICAL_RUNTIME_GROUPS = [
 'Target & identity',
 'Run logic & pacing',
 'Think time',
 'Transport & connections',
 'Security, proxy & auth',
 'Protocol behavior & session',
 'Errors, logging & diagnostics',
] as const

export type CanonicalRuntimeGroup = (typeof CANONICAL_RUNTIME_GROUPS)[number]

export type CanonicalRuntimeGroupId =
 | 'target_identity'
 | 'run_logic_pacing'
 | 'think_time'
 | 'transport_connections'
 | 'security_proxy_auth'
 | 'protocol_behavior_session'
 | 'errors_logging_diagnostics'

export const CANONICAL_RUNTIME_GROUP_IDS: Record<CanonicalRuntimeGroup, CanonicalRuntimeGroupId> = {
 'Target & identity': 'target_identity',
 'Run logic & pacing': 'run_logic_pacing',
 'Think time': 'think_time',
 'Transport & connections': 'transport_connections',
 'Security, proxy & auth': 'security_proxy_auth',
 'Protocol behavior & session': 'protocol_behavior_session',
 'Errors, logging & diagnostics': 'errors_logging_diagnostics',
}

export const PROVENANCE_FILTERS = ['All settings', 'Requires decision', 'Mapped', 'Unconfigured'] as const
export type ProvenanceFilterOption = (typeof PROVENANCE_FILTERS)[number]

export const LEGACY_CATEGORY_TO_CANONICAL_GROUP: Record<string, CanonicalRuntimeGroup> = {
 'Target & identity': 'Target & identity',
 target_identity: 'Target & identity',
 'Run logic & pacing': 'Run logic & pacing',
 run_logic_pacing: 'Run logic & pacing',
 'Run logic': 'Run logic & pacing',
 runlogic: 'Run logic & pacing',
 profileactions: 'Run logic & pacing',
 Pacing: 'Run logic & pacing',
 pacing: 'Run logic & pacing',
 'Think time': 'Think time',
 think_time: 'Think time',
 thinktime: 'Think time',
 'Transport & connections': 'Transport & connections',
 transport_connections: 'Transport & connections',
 HTTP: 'Transport & connections',
 web: 'Transport & connections',
 modemspeed: 'Protocol behavior & session',
 networkspeedsimulation: 'Protocol behavior & session',
 'Network speed simulation': 'Protocol behavior & session',
 'Security, proxy & auth': 'Security, proxy & auth',
 security_proxy_auth: 'Security, proxy & auth',
 Proxy: 'Security, proxy & auth',
 proxy: 'Security, proxy & auth',
 TLS: 'Security, proxy & auth',
 'Protocol behavior & session': 'Protocol behavior & session',
 protocol_behavior_session: 'Protocol behavior & session',
 'Browser emulation': 'Protocol behavior & session',
 browseremulation: 'Protocol behavior & session',
 'Web runtime': 'Protocol behavior & session',
 SOAP: 'Protocol behavior & session',
 Browser: 'Protocol behavior & session',
 WebSocket: 'Protocol behavior & session',
 Kafka: 'Protocol behavior & session',
 'Kafka producer (Java source)': 'Protocol behavior & session',
 JMS: 'Protocol behavior & session',
 Filters: 'Protocol behavior & session',
 filters: 'Protocol behavior & session',
 'Errors, logging & diagnostics': 'Errors, logging & diagnostics',
 errors_logging_diagnostics: 'Errors, logging & diagnostics',
 'Error handling': 'Errors, logging & diagnostics',
 Logging: 'Errors, logging & diagnostics',
 log: 'Errors, logging & diagnostics',
 Diagnostics: 'Errors, logging & diagnostics',
}

export type ImportRuntimeField = {
 category: string
 group: CanonicalRuntimeGroup
 groupId?: CanonicalRuntimeGroupId
 subgroup?: string
 path: string
 label: string
 type: string
 options?: string[]
 protocols?: string[]
 secret?: boolean
 triState?: boolean
}

export type RemoteImportRuntimeField = {
 category?: string
 group?: string
 group_id?: string
 subgroup?: string
 path: string
 label?: string
 type?: string
 options?: string[]
 protocols?: string[]
 secret?: boolean
 tri_state?: boolean
}

export type RemoteImportRuntimeFieldsEnvelope = {
 groups?: Array<{ id: string; label: string }>
 fields?: RemoteImportRuntimeField[]
}

type Field = ImportRuntimeField

const HTTP_FAMILY_PROTOCOLS = ['http', 'http/1.1', 'http/2', 'http/3', 'browser', 'soap', 'java_http']

const ADDITIONAL_SETTING_EFFECTS: Record<string, string> = {
 'config.base_url': 'Sets the base target origin prepended to relative request URLs during replay.',
 'config.run_logic.workload_model': 'Selects how virtual users or arrival rates are scheduled across the run.',
 'config.run_logic.max_in_flight': 'Caps concurrent in-flight virtual users when using open arrival-rate scheduling.',
 'config.run_logic.auto_transactions': 'Wraps each top-level action in an automatic transaction for per-action SLA tracking.',
 'config.run_logic.termination_mode': 'Chooses whether duration, iteration count, or whichever finishes first stops execution.',
 'config.think_time.distribution': 'Overrides the statistical distribution used to sample think-time delays between actions.',
 'config.http.quic.handshake_timeout_ms': 'Limits how long an HTTP/3 QUIC handshake may take before failing.',
 'config.http.quic.max_idle_timeout_ms': 'Controls how long an idle HTTP/3 QUIC connection stays open before closing.',
 'config.http.quic.keep_alive_ms': 'Sends periodic QUIC PING frames at this interval to keep HTTP/3 connections alive.',
 'config.http.quic.broken_ttl_ms': 'Sets how long a failed HTTP/3 endpoint cools down before retrying QUIC.',
 'config.http.quic.alt_svc_cache': 'Caches server Alt-Svc headers so subsequent requests can upgrade to HTTP/3.',
 'config.http.tls_timeout_ms': 'Limits how long the TLS handshake phase can take before timing out.',
 'config.http.max_idle_conns': 'Caps the total number of idle keep-alive connections retained across all hosts.',
 'config.http.tls.insecure_skip_verify': 'Skips server TLS certificate chain and hostname verification during handshakes.',
 'config.http.tls.client_cert_file': 'Presents this PEM client certificate during mutual TLS handshakes.',
 'config.http.tls.client_key_file': 'Uses this PEM private key for mutual TLS client authentication.',
 'config.http.tls.ca_cert_file': 'Trusts server certificates signed by this custom CA bundle.',
 'config.http.proxy.username': 'Authenticates to the outbound HTTP proxy with this username.',
 'config.http.proxy.no_proxy': 'Bypasses the configured proxy for matching hostnames, domains, or CIDRs.',
 'config.http.auth.type': 'Selects the default HTTP authentication scheme applied to challenged requests.',
 'config.http.auth.username': 'Supplies the default username for HTTP authentication challenges.',
 'config.http.auth.domain': 'Supplies the Windows or Active Directory domain for NTLM authentication.',
 'config.soap.ws_security.username_token.username': 'Injects this username into the SOAP WS-Security UsernameToken header.',
 'config.soap.ws_security.username_token.password_type': 'Selects cleartext or digest encoding for the WS-Security UsernameToken password.',
 'config.soap.ws_security.username_token.nonce': 'Adds a random base64 Nonce element to the WS-Security UsernameToken header.',
 'config.soap.ws_security.username_token.created': 'Adds a UTC wsu:Created timestamp element to the WS-Security UsernameToken header.',
 'config.soap.ws_security.timestamp.ttl_seconds': 'Sets the time-to-live in seconds for the SOAP wsu:Timestamp security header.',
 'config.soap.version': 'Selects the SOAP envelope namespace and content-type framing version.',
 'config.soap.fault_handling.fail_on_fault': 'Marks the SOAP action failed whenever the response body contains a SOAP Fault element.',
 'config.http.user_agent': 'Sends this default User-Agent header on HTTP requests unless overridden.',
 'config.http.traceparent': 'Injects a W3C traceparent header on outbound HTTP requests for distributed tracing.',
 'config.http.traceparent_sampled': 'Sets the sampled flag on generated W3C traceparent headers.',
 'config.http.cookies_enabled': 'Maintains a per-VUser HTTP cookie jar across requests.',
 'config.http.clear_cookies_per_iteration': 'Flushes the per-VUser cookie jar at the start of every iteration.',
 'config.http.cache_enabled': 'Simulates per-VUser HTTP response caching using ETag and Last-Modified headers.',
 'config.http.aura_recorded_failures': 'Controls whether recorded Salesforce Aura action failures fail, warn, or are ignored.',
 'config.browser_emulation.mode': 'Selects preset browser profile emulation, a custom User-Agent, or disables emulation.',
 'config.browser_emulation.cache.simulate_cache': 'Enables per-VUser HTTP response caching under browser emulation rules.',
 'config.browser_emulation.cache.cache_content_urls': 'Restricts simulated browser cache storage to HTML content responses.',
 'config.browser_emulation.cache.check_newer_versions': 'Revalidates cached responses with the origin server on every visit.',
 'config.browser_emulation.cache.download_non_html_resources': 'Parses HTML responses and automatically fetches embedded images, stylesheets, and scripts.',
 'config.browser_emulation.cache.new_user_each_iteration': 'Resets emulated browser session state and cookies at the start of each iteration.',
 'config.browser_emulation.cache.simulate_prefetch': 'Fetches HTML prefetch, preload, and prerender resource hints during replay.',
 'config.web_runtime.network_simulation.mode': 'Selects disabled, preset profile, or custom network throttling parameters.',
 'config.web_runtime.network_simulation.preset': 'Applies a named network bandwidth and latency profile per virtual user.',
 'config.web_runtime.network_simulation.bandwidth_kbps': 'Throttles combined upstream and downstream per-VUser bandwidth to this rate.',
 'config.web_runtime.network_simulation.latency_ms': 'Adds artificial network round-trip latency to every HTTP connection.',
 'config.web_runtime.network_simulation.jitter_ms': 'Adds randomized latency jitter in milliseconds to simulated network delays.',
 'config.web_runtime.network_simulation.packet_loss_pct': 'Simulates network packet loss at this percentage rate.',
 'config.web_runtime.streaming.mode': 'Selects how media streaming playback buffering and bitrate selection are emulated.',
 'config.web_runtime.streaming.buffer_size_seconds': 'Sets the target media playback buffer duration in seconds.',
 'config.web_runtime.streaming.fixed_bitrate_kbps': 'Pins simulated media streaming playback to this fixed bitrate in kilobits per second.',
 'config.web_runtime.streaming.bandwidth_estimation': 'Chooses the adaptive bitrate bandwidth estimation strategy for streaming actions.',
 'config.web_runtime.data_formatting.auto_detect': 'Automatically detects and applies data format extensions to request and response payloads.',
 'config.error_handling.snapshot_on_error': 'Captures request and response diagnostic snapshots whenever an action fails.',
 'config.error_handling.retry_backoff': 'Selects fixed, exponential, or Retry-After delay progression between action retries.',
 'config.error_handling.honor_retry_after': 'Honors HTTP 429 and 503 Retry-After response headers when scheduling retries.',
 'config.error_handling.retry_after_max_ms': 'Caps the maximum delay waited when honoring an HTTP Retry-After header.',
 'config.log.redact_run_evidence': 'Redacts sensitive headers, cookies, and tokens from retained run evidence.',
}

Object.assign(runtimeSettingEffects, ADDITIONAL_SETTING_EFFECTS)

export function resolveCanonicalRuntimeGroup(categoryOrGroup: string, path?: string): CanonicalRuntimeGroup {
 if ((CANONICAL_RUNTIME_GROUPS as readonly string[]).includes(categoryOrGroup)) {
  return categoryOrGroup as CanonicalRuntimeGroup
 }
 if (categoryOrGroup in CANONICAL_RUNTIME_GROUP_IDS) {
  return categoryOrGroup as CanonicalRuntimeGroup
 }
 const byGroupId = Object.entries(CANONICAL_RUNTIME_GROUP_IDS).find(([, id]) => id === categoryOrGroup)?.[0] as CanonicalRuntimeGroup | undefined
 if (byGroupId) return byGroupId
 if (path) {
  if (path === 'config.base_url') return 'Target & identity'
  if (path.startsWith('config.run_logic.') || path.startsWith('config.pacing.')) return 'Run logic & pacing'
  if (path.startsWith('config.think_time.')) return 'Think time'
  if (
   path === 'config.http.version' ||
   path.startsWith('config.http.quic.') ||
   path.endsWith('_timeout_ms') ||
   [
    'config.http.connect_timeout_ms',
    'config.http.request_timeout_ms',
    'config.http.tls_timeout_ms',
    'config.http.idle_timeout_ms',
    'config.http.page_timeout_ms',
    'config.http.challenge_delay_ms',
    'config.http.max_conns_per_host',
    'config.http.max_idle_conns',
    'config.http.keep_alive',
    'config.http.max_redirects',
    'config.http.ip_version',
    'config.http.dns_cache',
    'config.http.receive_buffer_bytes',
   ].includes(path)
  ) {
   return 'Transport & connections'
  }
  if (
   path.startsWith('config.http.tls.') ||
   path === 'config.http.proxy_mode' ||
   path.startsWith('config.http.proxy.') ||
   path.startsWith('config.http.auth.') ||
   path.startsWith('config.soap.ws_security.')
  ) {
   return 'Security, proxy & auth'
  }
  if (
   path.startsWith('config.soap.') ||
   path.startsWith('config.http.') ||
   path.startsWith('config.browser_emulation.') ||
   path.startsWith('config.web_runtime.') ||
   path.startsWith('config.browser.') ||
   path.startsWith('config.websocket.') ||
   path.startsWith('config.kafka.') ||
   path.startsWith('config.jms.')
  ) {
   return 'Protocol behavior & session'
  }
  if (
   path.startsWith('config.error_handling.') ||
   path.startsWith('config.log.') ||
   path.startsWith('config.results.')
  ) {
   return 'Errors, logging & diagnostics'
  }
 }
 const direct = LEGACY_CATEGORY_TO_CANONICAL_GROUP[categoryOrGroup]
 if (direct) return direct
 const normalized = categoryOrGroup.replace(/\s+/g, '').toLowerCase()
 if (normalized.startsWith('runlogic') || normalized === 'profileactions' || normalized === 'pacing') return 'Run logic & pacing'
 if (normalized.includes('thinktime')) return 'Think time'
 if (normalized.includes('proxy') || normalized.includes('tls') || normalized.includes('security') || normalized.includes('auth')) return 'Security, proxy & auth'
 if (normalized.includes('error') || normalized.includes('log') || normalized.includes('diagnostic')) return 'Errors, logging & diagnostics'
 if (normalized.includes('target') || normalized.includes('identity')) return 'Target & identity'
 if (normalized.includes('transport') || normalized === 'http' || normalized === 'web') return 'Transport & connections'
 return 'Protocol behavior & session'
}

export function resolveRuntimeSubgroup(path?: string, fallbackCategory?: string): string {
 if (!path) return fallbackCategory || 'General'
 if (path === 'config.base_url') return 'Target & identity'
 if (path.startsWith('config.run_logic.')) return 'Run logic'
 if (path.startsWith('config.pacing.')) return 'Pacing'
 if (path.startsWith('config.think_time.')) return 'Think time'
 if (path.startsWith('config.http.quic.')) return 'HTTP/3 & QUIC'
 if (
  [
   'config.http.connect_timeout_ms',
   'config.http.request_timeout_ms',
   'config.http.tls_timeout_ms',
   'config.http.idle_timeout_ms',
   'config.http.page_timeout_ms',
   'config.http.challenge_delay_ms',
  ].includes(path)
 ) {
  return 'Timeouts'
 }
 if (
  [
   'config.http.version',
   'config.http.max_conns_per_host',
   'config.http.max_idle_conns',
   'config.http.keep_alive',
   'config.http.max_redirects',
   'config.http.ip_version',
   'config.http.dns_cache',
   'config.http.receive_buffer_bytes',
  ].includes(path)
 ) {
  return 'Protocol & connection pool'
 }
 if (path.startsWith('config.http.tls.')) return 'TLS'
 if (path === 'config.http.proxy_mode' || path.startsWith('config.http.proxy.')) return 'Proxy'
 if (path.startsWith('config.http.auth.')) return 'HTTP authentication'
 if (path.startsWith('config.soap.ws_security.')) return 'SOAP WS-Security'
 if (path.startsWith('config.soap.')) return 'SOAP envelope & faults'
 if (path.startsWith('config.http.')) return 'HTTP request defaults & cookies'
 if (path.startsWith('config.browser_emulation.') || path.startsWith('config.web_runtime.')) return 'Browser emulation & web runtime'
 if (path.startsWith('config.error_handling.')) return 'Error handling'
 if (path.startsWith('config.log.')) return 'Logging'
 if (path.startsWith('config.results.')) return 'Diagnostics'
 return fallbackCategory || 'Protocol behavior & session'
}

function withCanonicalMetadata(raw: {
 category: string
 path: string
 label: string
 type: string
 options?: string[]
 protocols?: string[]
 secret?: boolean
 triState?: boolean
 group?: CanonicalRuntimeGroup
 subgroup?: string
}): ImportRuntimeField {
 // Fold the legacy 1-field Proxy category into HTTP / Security, proxy & auth.
 const category = raw.category === 'Proxy' ? 'HTTP' : raw.category
 const group = raw.group || resolveCanonicalRuntimeGroup(category, raw.path)
 const subgroup = raw.subgroup || resolveRuntimeSubgroup(raw.path, category)
 return {
  ...raw,
  category,
  group,
  groupId: CANONICAL_RUNTIME_GROUP_IDS[group],
  subgroup,
 }
}

const EXPANDED_HTTP_AND_SOAP_FIELDS: Array<Omit<ImportRuntimeField, 'group' | 'groupId' | 'subgroup'> & { group?: CanonicalRuntimeGroup; subgroup?: string }> = [
 // 1. Target & identity
 { category: 'Target & identity', group: 'Target & identity', subgroup: 'Target & identity', path: 'config.base_url', label: 'Base URL', type: 'text' },

 // 2. Run logic & pacing
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.iterations', label: 'Iterations per VUser', type: 'number' },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.global_iterations', label: 'Shared iterations', type: 'number' },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.max_transactions', label: 'Transaction limit', type: 'number' },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.workload_model', label: 'Workload model', type: 'select', options: ['closed', 'open'] },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.max_in_flight', label: 'Maximum in-flight VUsers', type: 'number' },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.auto_transactions', label: 'Automatic action transactions', type: 'select', options: ['none', 'per_action', 'per_iteration'] },
 { category: 'Run logic', group: 'Run logic & pacing', subgroup: 'Run logic', path: 'config.run_logic.termination_mode', label: 'Default termination policy', type: 'select', options: ['', 'duration', 'iterations', 'whichever_first'] },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.type', label: 'Mode', type: 'select', options: ['none', 'fixed_delay', 'random_delay', 'fixed_interval', 'random_interval', 'tps'] },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.delay_ms', label: 'Delay (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.min_delay_ms', label: 'Minimum delay (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.max_delay_ms', label: 'Maximum delay (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.interval_ms', label: 'Interval (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.min_interval_ms', label: 'Minimum interval (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.max_interval_ms', label: 'Maximum interval (ms)', type: 'number' },
 { category: 'Pacing', group: 'Run logic & pacing', subgroup: 'Pacing', path: 'config.pacing.tps', label: 'Target TPS', type: 'number' },

 // 3. Think time
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.ignore', label: 'Ignore think time', type: 'boolean' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.type', label: 'Mode', type: 'select', options: ['none', 'fixed', 'recorded', 'random_percentage', 'gaussian', 'normal', 'uniform', 'exponential'] },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.distribution', label: 'Distribution override', type: 'select', options: ['uniform', 'exponential'] },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.mean_ms', label: 'Mean delay (ms)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.std_ms', label: 'Standard deviation (ms)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.min_ms', label: 'Minimum delay (ms)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.max_ms', label: 'Maximum delay (ms)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.min_percent', label: 'Minimum recorded delay (%)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.max_percent', label: 'Maximum recorded delay (%)', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.multiplier', label: 'Multiplier', type: 'number' },
 { category: 'Think time', group: 'Think time', subgroup: 'Think time', path: 'config.think_time.limit_ms', label: 'Delay cap (ms)', type: 'number' },

 // 4. Transport & connections
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.version', label: 'HTTP version', type: 'select', options: ['', '1.1', '2', 'auto+h3', '3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'HTTP/3 & QUIC', path: 'config.http.quic.handshake_timeout_ms', label: 'QUIC handshake timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'HTTP/3 & QUIC', path: 'config.http.quic.max_idle_timeout_ms', label: 'QUIC max idle timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'HTTP/3 & QUIC', path: 'config.http.quic.keep_alive_ms', label: 'QUIC keep-alive ping interval (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'HTTP/3 & QUIC', path: 'config.http.quic.broken_ttl_ms', label: 'QUIC fallback cooldown (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'HTTP/3 & QUIC', path: 'config.http.quic.alt_svc_cache', label: 'Cache Alt-Svc HTTP/3 announcements', type: 'select', options: ['per_vuser', 'per_iteration'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.connect_timeout_ms', label: 'Connect timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.request_timeout_ms', label: 'Request timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.tls_timeout_ms', label: 'TLS handshake timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.idle_timeout_ms', label: 'Keep-alive idle timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.page_timeout_ms', label: 'Whole-page timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Timeouts', path: 'config.http.challenge_delay_ms', label: 'Digest authentication delay (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.max_conns_per_host', label: 'Connections per host', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.max_idle_conns', label: 'Maximum idle connections', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.keep_alive', label: 'Keep connections alive', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.max_redirects', label: 'Maximum redirects', type: 'signed', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.ip_version', label: 'IP family', type: 'select', options: ['auto', 'ipv4', 'ipv6'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.dns_cache', label: 'Per-VUser DNS cache', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Transport & connections', subgroup: 'Protocol & connection pool', path: 'config.http.receive_buffer_bytes', label: 'Response read buffer (bytes)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },

 // 5. Security, proxy & auth
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.min_version', label: 'Minimum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.max_version', label: 'Maximum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.insecure_skip_verify', label: 'Skip TLS certificate verification', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.client_cert_file', label: 'Client certificate file (mTLS)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.client_key_file', label: 'Client private key file (mTLS)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'TLS', path: 'config.http.tls.ca_cert_file', label: 'Custom CA certificate bundle', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'Proxy', path: 'config.http.proxy_mode', label: 'Proxy mode', type: 'select', options: ['none', 'system', 'custom', 'pac'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'Proxy', path: 'config.http.proxy.url', label: 'Proxy URL', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'Proxy', path: 'config.http.proxy.pac_url', label: 'PAC URL (bounded evaluator)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'Proxy', path: 'config.http.proxy.username', label: 'Proxy username', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'Proxy', path: 'config.http.proxy.no_proxy', label: 'Proxy bypass hosts (NO_PROXY)', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'HTTP authentication', path: 'config.http.auth.type', label: 'HTTP authentication scheme', type: 'select', options: ['', 'basic', 'bearer', 'digest', 'ntlm', 'negotiate', 'negotiate_any', 'kerberos'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'HTTP authentication', path: 'config.http.auth.username', label: 'HTTP auth username', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Security, proxy & auth', subgroup: 'HTTP authentication', path: 'config.http.auth.domain', label: 'NTLM / Windows domain', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'SOAP', group: 'Security, proxy & auth', subgroup: 'SOAP WS-Security', path: 'config.soap.ws_security.username_token.username', label: 'WS-Security username', type: 'text', protocols: ['soap'] },
 { category: 'SOAP', group: 'Security, proxy & auth', subgroup: 'SOAP WS-Security', path: 'config.soap.ws_security.username_token.password_type', label: 'WS-Security password type', type: 'select', options: ['text', 'digest'], protocols: ['soap'] },
 { category: 'SOAP', group: 'Security, proxy & auth', subgroup: 'SOAP WS-Security', path: 'config.soap.ws_security.username_token.nonce', label: 'Include WS-Security nonce', type: 'boolean', protocols: ['soap'] },
 { category: 'SOAP', group: 'Security, proxy & auth', subgroup: 'SOAP WS-Security', path: 'config.soap.ws_security.username_token.created', label: 'Include WS-Security created timestamp', type: 'boolean', protocols: ['soap'] },
 { category: 'SOAP', group: 'Security, proxy & auth', subgroup: 'SOAP WS-Security', path: 'config.soap.ws_security.timestamp.ttl_seconds', label: 'WS-Security timestamp TTL (s)', type: 'number', protocols: ['soap'] },

 // 6. Protocol behavior & session
 { category: 'SOAP', group: 'Protocol behavior & session', subgroup: 'SOAP envelope & faults', path: 'config.soap.version', label: 'SOAP protocol version', type: 'select', options: ['1.1', '1.2'], protocols: ['soap'] },
 { category: 'SOAP', group: 'Protocol behavior & session', subgroup: 'SOAP envelope & faults', path: 'config.soap.fault_handling.fail_on_fault', label: 'Fail action on SOAP Fault', type: 'boolean', triState: true, protocols: ['soap'] },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.user_agent', label: 'Default User-Agent header', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.accept_language', label: 'Accept language', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.accept_encoding', label: 'Accept encoding', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.traceparent', label: 'Propagate W3C traceparent header', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.traceparent_sampled', label: 'Set traceparent sampled flag', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.cookies_enabled', label: 'Enable per-VUser cookie jar', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.clear_cookies_per_iteration', label: 'Clear cookies each iteration', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.cache_enabled', label: 'Simulate browser response cache', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.cache_max_unused_iterations', label: 'Unused iterations before cache eviction', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.cache_excluded_mime_types', label: 'Do not cache MIME types', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.max_meta_refresh', label: 'Meta-refresh limit per URL', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.content_check_error_limit', label: 'Content-check error match limit', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.http_errors_as_warnings', label: 'HTTP errors as warnings', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.fail_noncritical_resources', label: 'Fail on non-critical resource errors', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.resource_timeout_as_warning', label: 'Resource timeouts as warnings', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.legacy_html_checks', label: 'Legacy HTML checks', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.html_parse_mode', label: 'Discover HTML resources in', type: 'select', options: ['html', 'text', 'any'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.html_excluded_mime_types', label: 'Do not discover resources in MIME types', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'HTTP request defaults & cookies', path: 'config.http.aura_recorded_failures', label: 'Salesforce Aura recorded failure policy', type: 'select', options: ['', 'require_success', 'accept_recorded'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.mode', label: 'Browser emulation mode', type: 'select', options: ['', 'specific', 'custom'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.browser_type', label: 'Browser type', type: 'select', options: ['chrome', 'edge', 'firefox', 'safari', 'ie'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.browser_version', label: 'Browser version', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.platform', label: 'Platform', type: 'select', options: ['windows', 'macos', 'linux', 'android', 'ios'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.language', label: 'Language', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.custom_user_agent', label: 'User agent', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.clear_cache_for_new_user', label: 'Clear cache for a new user', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.simulate_cache', label: 'Simulate browser cache', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.cache_content_urls', label: 'Cache URLs requiring content (HTML)', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.check_newer_versions', label: 'Check for newer versions every visit', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.download_non_html_resources', label: 'Download non-HTML embedded resources', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.new_user_each_iteration', label: 'Simulate a new user on each iteration', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.browser_emulation.cache.simulate_prefetch', label: 'Simulate browser prefetch and prerender', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.mode', label: 'Network simulation mode', type: 'select', options: ['', 'disabled', 'preset', 'custom'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.preset', label: 'Network simulation preset', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.bandwidth_kbps', label: 'Simulated shared bandwidth (kbps)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.latency_ms', label: 'Simulated network latency (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.jitter_ms', label: 'Simulated jitter (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.network_simulation.packet_loss_pct', label: 'Simulated packet loss (%)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.streaming.mode', label: 'Media streaming mode', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.streaming.buffer_size_seconds', label: 'Streaming buffer duration (s)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.streaming.fixed_bitrate_kbps', label: 'Fixed streaming bitrate (kbps)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.streaming.bandwidth_estimation', label: 'Streaming bandwidth estimation', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', group: 'Protocol behavior & session', subgroup: 'Browser emulation & web runtime', path: 'config.web_runtime.data_formatting.auto_detect', label: 'Auto-detect data formatting', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },

 // 7. Errors, logging & diagnostics
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.continue_on_error', label: 'Continue on error', type: 'boolean' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.snapshot_on_error', label: 'Capture snapshot on error', type: 'boolean' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.fail_transaction_on_error_message', label: 'Fail open transactions on error message', type: 'boolean' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.max_retries', label: 'Maximum retries', type: 'number' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.retry_delay_ms', label: 'Retry delay (ms)', type: 'number' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.retry_backoff', label: 'Retry backoff strategy', type: 'select', options: ['fixed', 'exponential', 'retry_after'] },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.honor_retry_after', label: 'Honor HTTP Retry-After header', type: 'boolean' },
 { category: 'Error handling', group: 'Errors, logging & diagnostics', subgroup: 'Error handling', path: 'config.error_handling.retry_after_max_ms', label: 'Maximum Retry-After delay (ms)', type: 'number' },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.level', label: 'Log level', type: 'select', options: ['disabled', 'standard', 'extended'] },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.error_buffer_bytes', label: 'Error history buffer (bytes)', type: 'number' },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.max_response_bytes', label: 'Response-log limit (bytes; 0 uses engine bound)', type: 'number' },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.on_error_only', label: 'Log on error only', type: 'boolean' },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.redact_run_evidence', label: 'Redact sensitive values in run evidence', type: 'boolean' },
 { category: 'Logging', group: 'Errors, logging & diagnostics', subgroup: 'Logging', path: 'config.log.extended_options', label: 'Extended log options', type: 'lines', options: ['parameter_substitution', 'data_returned', 'advanced_trace'] },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.mode', label: 'Exchange capture', type: 'select', options: ['off', 'errors', 'sampled', 'adaptive'] },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.sample_rate', label: 'Capture sample rate', type: 'number' },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.include_resources', label: 'Include fetched resources', type: 'boolean' },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.max_items', label: 'Retained exchange limit', type: 'number' },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.max_item_bytes', label: 'Bytes per exchange', type: 'number' },
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', subgroup: 'Diagnostics', path: 'config.results.message_detail.max_total_bytes', label: 'Total capture bytes', type: 'number' },
]

function buildStaticCatalog(): ImportRuntimeField[] {
 const byPath = new Map<string, ImportRuntimeField>()
 for (const entry of fieldSchema as Array<{ category: string; path: string; label: string; type: string; options?: string[] }>) {
  const normalized = withCanonicalMetadata(entry)
  byPath.set(normalized.path, normalized)
 }
 for (const entry of EXPANDED_HTTP_AND_SOAP_FIELDS) {
  const normalized = withCanonicalMetadata(entry)
  const existing = byPath.get(normalized.path)
  byPath.set(normalized.path, existing ? { ...existing, ...normalized } : normalized)
 }
 return [...byPath.values()]
}

export const importRuntimeFields: Field[] = buildStaticCatalog()

let registeredRemoteFields: RemoteImportRuntimeField[] | null = null

function normalizeRemoteFieldType(type?: string): string {
 switch (type) {
  case 'int':
  case 'float':
  case 'number':
   return 'number'
  case 'signed':
   return 'signed'
  case 'boolean':
   return 'boolean'
  case 'select':
   return 'select'
  case 'string_list':
  case 'lines':
   return 'lines'
  default:
   return 'text'
 }
}

export function mergeImportRuntimeFields(
 remoteFields?: RemoteImportRuntimeField[] | null,
 baseFields: ImportRuntimeField[] = importRuntimeFields,
): ImportRuntimeField[] {
 if (!remoteFields?.length) return baseFields
 const byPath = new Map<string, ImportRuntimeField>(baseFields.map(field => [field.path, { ...field }]))
 for (const remote of remoteFields) {
  if (!remote?.path || remote.secret) continue
  if (remote.type && ['key_value_map', 'object_list'].includes(remote.type)) continue
  const existing = byPath.get(remote.path)
  if (existing) {
   const keepRestrictedTlsOptions = remote.path === 'config.http.tls.min_version' || remote.path === 'config.http.tls.max_version'
   const mergedOptions = keepRestrictedTlsOptions
    ? existing.options
    : existing.options || remote.options
      ? [...new Set([...(existing.options || []), ...(remote.options || [])])]
      : undefined
   const remoteGroup = remote.group || remote.group_id
   const resolvedGroup = remoteGroup ? resolveCanonicalRuntimeGroup(remoteGroup, remote.path) : existing.group
   byPath.set(remote.path, {
    ...existing,
    group: resolvedGroup,
    groupId: CANONICAL_RUNTIME_GROUP_IDS[resolvedGroup],
    subgroup: remote.subgroup || existing.subgroup,
    options: mergedOptions,
    protocols: existing.protocols || remote.protocols,
    triState: existing.triState ?? remote.tri_state,
   })
   continue
  }
  const rawCategory = remote.category === 'Proxy' ? 'HTTP' : remote.category || remote.group || 'HTTP'
  const group = resolveCanonicalRuntimeGroup(remote.group || remote.group_id || rawCategory, remote.path)
  const subgroup = remote.subgroup || resolveRuntimeSubgroup(remote.path, rawCategory)
  const label = remote.label || remote.path.replace(/^config\./, '').replace(/[._]/g, ' ')
  if (!runtimeSettingEffects[remote.path]) {
   runtimeSettingEffects[remote.path] = `Configures ${label.toLowerCase()} (${remote.path.replace(/^config\./, '')}) during replay.`
  }
  byPath.set(remote.path, {
   category: rawCategory,
   group,
   groupId: CANONICAL_RUNTIME_GROUP_IDS[group],
   subgroup,
   path: remote.path,
   label,
   type: normalizeRemoteFieldType(remote.type),
   options: remote.options,
   protocols: remote.protocols,
   secret: remote.secret,
   triState: remote.tri_state,
  })
 }
 return [...byPath.values()]
}

export function registerRemoteImportRuntimeFields(remoteFields: RemoteImportRuntimeField[]): ImportRuntimeField[] {
 registeredRemoteFields = remoteFields
 return getActiveImportRuntimeFields()
}

export function resetRegisteredImportRuntimeFields(): void {
 registeredRemoteFields = null
}

export function getActiveImportRuntimeFields(): ImportRuntimeField[] {
 return mergeImportRuntimeFields(registeredRemoteFields, importRuntimeFields)
}

export async function fetchImportRuntimeFields(
 fetcher: (endpoint: string) => Promise<RemoteImportRuntimeField[] | RemoteImportRuntimeFieldsEnvelope> = endpoint =>
  request<RemoteImportRuntimeField[] | RemoteImportRuntimeFieldsEnvelope>(endpoint),
): Promise<ImportRuntimeField[]> {
 try {
  const remote = await fetcher('/scripts/import/settings/fields')
  const remoteFields = Array.isArray(remote)
   ? remote
   : remote && typeof remote === 'object' && Array.isArray(remote.fields)
     ? remote.fields
     : null
  if (remoteFields) {
   return registerRemoteImportRuntimeFields(remoteFields)
  }
 } catch {
  // Fall back to the static client-side catalog when offline or unmocked.
 }
 return getActiveImportRuntimeFields()
}

export const readSetting = (script: ScriptDefinition, path: string): unknown => {
 if (path === 'config.log.extended_options') {
  const ext = script.config?.log?.extended_options as unknown
  if (ext && typeof ext === 'object' && !Array.isArray(ext)) return undefined
 }
 const value = path.split('.').reduce<unknown>((v, key) => v && typeof v === 'object' ? (v as Record<string, unknown>)[key] : undefined, script)
 // Zero percentage is executable (including an explicit 0–0 range), and Go
 // omits zero-valued bounds from JSON. It is not an unavailable source value.
 if (value === undefined && script.config?.think_time?.type === 'random_percentage' && ['config.think_time.min_percent', 'config.think_time.max_percent'].includes(path)) return 0
 return value
}

const statuses = ['Requires decision', 'Mapped for replay', 'Preserved only', 'Not applicable']
const groupForStatus = (status: string) => status === 'executed' || status === 'mapped' ? statuses[1] : status === 'compatibility_only' ? statuses[2] : status === 'non_applicable' ? statuses[3] : statuses[0]
const parityFallbackNote = (group: string, concept: string, path?: string, label?: string) => {
 const subject = label || concept
 const effect = path && runtimeSettingEffects[path]
 if (group === statuses[1]) return `The importer mapped source ${concept} to ${subject}. ${effect || 'This setting affects replay behavior.'} Verify the draft value against the source before replay.`
 if (group === statuses[2]) return `Source ${concept} is retained for reference but is not applied to ${subject} during replay. ${effect || 'Review whether the draft needs an equivalent setting.'}`
 if (group === statuses[3]) return `Source ${concept} was marked not applicable to this replay. ${effect ? `The draft ${subject} still ${effect.charAt(0).toLowerCase()}${effect.slice(1)}` : 'Confirm this source behavior is not needed.'}`
 return `The importer could not confirm a replay mapping for source ${concept}${path ? ` to ${subject}` : ''}. ${effect || 'The source behavior may need an explicit replacement.'} Review the source behavior and adjust the draft before replay.`
}
const sourceFallbackNote = (file: string, section: string, key: string, group: string) => {
 const source = `${key} in ${file} / ${section}`
 if (group === statuses[3]) return `${source} was marked not applicable to replay; its value is retained for inspection.`
 if (group === statuses[2]) return `${source} is preserved as source evidence, but no editable replay destination was identified. Check whether its behavior is needed.`
 if (group === statuses[1]) return `${source} is reported as mapped, but the importer did not identify an editable destination here. Verify the mapped behavior in the draft.`
 return `${source} has no confirmed replay mapping or editable destination. Check whether the draft needs an equivalent behavior before replay.`
}

export type RuntimeSettingRow = {
 id: string
 category: string
 group: CanonicalRuntimeGroup
 subgroup?: string
 label: string
 path?: string
 imported: string
 importedSummary?: string
 note: string
 field?: Field
}

const sourceAliases: Record<string, string[]> = {
 'config.results.message_detail.mode': ['EnableSnapshotsInReplay'],
 'config.results.message_detail.sample_rate': ['EnableSnapshotsInReplay'],
 'config.results.message_detail.include_resources': ['SaveSnapshotResources'],
 'config.http.receive_buffer_bytes': ['NetBufSize'],
 'config.http.max_meta_refresh': ['MaxSelfMetaRefreshCount'],
 'config.http.content_check_error_limit': ['MaxErrorMatchesAsERRORS'],
 'config.log.error_buffer_bytes': ['AutoLogBufferSize'],
 'config.http.http_errors_as_warnings': ['HttpErrorsAsWarnings'],
 'config.http.fail_noncritical_resources': ['FailNonCriticalItem'],
 'config.http.resource_timeout_as_warning': ['ResourcePageTimeoutIsWarning'],
 'config.http.legacy_html_checks': ['EnableChecks'],
 'config.http.dns_cache': ['EnableIPCache'],
 'config.http.challenge_delay_ms': ['Retry401ThinkTime'],
 'config.error_handling.fail_transaction_on_error_message': ['FailTransOnErrorMsg'],
 'config.browser_emulation.cache.clear_cache_for_new_user': ['ClearCacheForSimulateNewUser'],
 'config.log.max_response_bytes': ['LogMaxResponseSize'],

 'config.http.accept_language': ['AcceptLanguage', 'BrowserAcceptLanguage', 'Language'],
 'config.http.accept_encoding': ['AcceptEncoding', 'BrowserAcceptEncoding', 'UseGzip'],
 'config.browser_emulation.custom_user_agent': ['UseCustomAgent', 'CustomUserAgent', 'UserAgent', 'BrowserUserAgent', 'UseBrowserAgent'],
 'config.browser_emulation.language': ['BrowserAcceptLanguage', 'BrowserLanguageCode', 'Language', 'AcceptLanguage'],
 'config.http.proxy_mode': ['ProxyMode', 'ProxyUseProxy', 'ProxyUseProxyServer', 'ProxyUseBrowser', 'ProxyUseAutoConfigScript'],
 'config.http.proxy.url': ['ProxyURL', 'ProxyHTTPHost', 'ProxyHTTPPort', 'ProxyHTTPSHost', 'ProxyHTTPSPort', 'ProxyUseSame'],
 'config.http.proxy.pac_url': ['ProxyAutoConfigScriptURL', 'ProxyPACURL'],
 'config.http.proxy.username': ['ProxyUserName', 'ProxyUsername'],
 'config.http.proxy.no_proxy': ['ProxyNoLocal', 'ProxyBypass', 'NoProxy'],
 'config.http.version': ['HTTPVersion', 'HTTPVer', 'EnableHTTP2', 'HTTP2Enabled'],
 'config.http.connect_timeout_ms': ['ConnectTimeout'],
 'config.http.request_timeout_ms': ['ReceiveTimeout', 'StepDownloadTimeout'],
 'config.http.idle_timeout_ms': ['KeepAliveTimeout'],
 'config.http.page_timeout_ms': ['PageDownloadTimeout'],
 'config.http.ip_version': ['IPVersionPolicy'],
 'config.http.cache_max_unused_iterations': ['DeleteCacheUnreferencedIterations'],
 'config.http.html_parse_mode': ['ParseHtmlContentType'],
 'config.http.max_redirects': ['MaxRedirections', 'MaxRedirectionDepth'],
 'config.http.keep_alive': ['KeepAlive'],
 'config.http.max_conns_per_host': ['MaxConnectionsPerHost', 'MaxConnections', 'MaxConnectionsPerHostHTTP'],
 'config.log.level': ['Level', 'LogOptions', 'LogLevel'],
 'config.log.on_error_only': ['OnErrorOnly', 'LogOnError', 'LogOnErrorOnly', 'AutoLog'],
 'config.log.extended_options': ['ExtendedOptions', 'ExtendedLogOptions', 'ParameterSubstitution', 'DataReturned', 'AdvancedTrace', 'MsgClassParameters', 'MsgClassData', 'MsgClassFull'],
 'config.think_time.type': ['Options'],
 'config.think_time.ignore': ['Options', 'Ignore', 'IgnoreThinkTime'],
 'config.think_time.multiplier': ['Options', 'Factor', 'Multiplier', 'MultiplyRecordedThinkTimeBy'],
 'config.think_time.limit_ms': ['LimitFlag', 'Limit', 'LimitMs', 'LimitThinkTimeMs'],
 'config.think_time.min_percent': ['ThinkTimeRandomLow'],
 'config.think_time.max_percent': ['ThinkTimeRandomHigh'],
 'config.pacing.type': ['Mode', 'PacingMode', 'IterationPace', 'RunLogicPaceType'],
 'config.pacing.interval_ms': ['IntervalMs', 'Interval', 'StartEvery', 'RunLogicPaceConstTime'],
 'config.pacing.delay_ms': ['DelayMs', 'Delay', 'StartEvery', 'RunLogicPaceConstAfterTime'],
 'config.pacing.min_interval_ms': ['MinIntervalMs', 'MinInterval', 'RandomMin', 'RunLogicRandomPaceMin'],
 'config.pacing.max_interval_ms': ['MaxIntervalMs', 'MaxInterval', 'RandomMax', 'RunLogicRandomPaceMax'],
 'config.pacing.min_delay_ms': ['MinDelayMs', 'MinDelay', 'RandomMin', 'RunLogicAfterPaceMin'],
 'config.pacing.max_delay_ms': ['MaxDelayMs', 'MaxDelay', 'RandomMax', 'RunLogicAfterPaceMax'],
}

export function runtimeSettingRows(
 script: ScriptDefinition,
 customFields?: RemoteImportRuntimeField[],
): Record<string, RuntimeSettingRow[]> {
 const activeFields = customFields ? mergeImportRuntimeFields(customFields, importRuntimeFields) : getActiveImportRuntimeFields()
 const metadata = script.metadata?.import
 const parity = getImportSummary(metadata).runtimeSettingsParity
 const sourceFiles = readImportedRuntimeFiles(metadata)
 const dispositions = readImportedRuntimeSettingDispositions(metadata)
 const groups: Record<string, RuntimeSettingRow[]> = Object.fromEntries(statuses.map(s => [s, []]))
 const covered = new Set<string>()
 for (const entry of parity) {
  if (['runtime_unmapped', 'runtime_non_applicable'].includes(entry.id) && Object.keys(dispositions).length) continue
  const group = groupForStatus(entry.status)
  const paths = entry.fields?.length ? [...new Set(entry.fields.flatMap(path => {
   const exact = activeFields.find(f => f.path === path)
   const descendants = activeFields.filter(f => f.path.startsWith(`${path}.`))
   return exact ? [path] : descendants.length ? descendants.map(f => f.path) : [path]
  }))] : [undefined]
  for (const path of paths) {
   if (path && !settingIsRelevant(script, path, true)) continue
   const field = activeFields.find(f => f.path === path)
   const source: string[] = []
   const values: string[] = []
   for (const [file, sections] of Object.entries(dispositions)) for (const [section, keys] of Object.entries(sections)) for (const [key, disposition] of Object.entries(keys)) {
    if (disposition.status === 'non_applicable' && entry.status !== 'non_applicable') continue
    const aliases = path ? sourceAliases[path] : undefined
    const destinationMatch = path && disposition.fields?.some(destination => path === destination || path.startsWith(`${destination}.`))
    const aliasMatch = aliases?.some(alias => alias.toLowerCase() === key.toLowerCase())
    if (disposition.parity_id === entry.id && (!path || aliasMatch || (!aliases && destinationMatch))) {
     const value = sourceFiles[file]?.[section]?.[key]
     if (value === undefined) continue
     source.push(`${key}: ${value === '' ? '(empty)' : value} (${file} / ${section})`)
     const displayed = value === '' ? '(empty)' : field?.type === 'boolean' && ['0', '1', 'true', 'false'].includes(value.toLowerCase()) ? ['1', 'true'].includes(value.toLowerCase()) ? 'Enabled' : 'Disabled' : value
     // These LoadRunner timeout keys are explicitly seconds; retain raw evidence below.
     values.push(path?.endsWith('_ms') && ['connecttimeout', 'receivetimeout', 'stepdownloadtimeout', 'keepalivetimeout', 'pagedownloadtimeout'].includes(key.toLowerCase()) && /^\d+(\.\d+)?$/.test(value) ? `${Number(value) * 1000} ms` : displayed)
     covered.add(`${file}/${section}/${key}`)
    }
   }
   if (!source.length && path) continue
   const existing = path && groups[group].find(row => row.path === path)
   if (existing) {
    // Several source concepts can map to one destination. Deduplicate the
    // setting control without dropping any of their immutable source evidence.
    existing.imported = [...new Set([...existing.imported.split('\n'), ...source])].join('\n') || 'Source evidence not supplied by this importer'
    existing.importedSummary = [...new Set([existing.importedSummary, ...values])].filter(Boolean).join(' · ')
    if (entry.notes && !existing.note.includes(entry.notes)) existing.note += `\n${entry.notes}`
    continue
   }
   const rowCategory = field?.category || (entry.area === 'Proxy' ? 'HTTP' : entry.area)
   const canonicalGroup = field?.group || resolveCanonicalRuntimeGroup(rowCategory, path)
   const subgroup = field?.subgroup || resolveRuntimeSubgroup(path, rowCategory)
   groups[group].push({
    id: `${entry.id}:${path || ''}`,
    path,
    category: rowCategory,
    group: canonicalGroup,
    subgroup,
    label: field?.label || path?.replace(/^config\./, '').replace(/_/g, ' ') || entry.concept,
    importedSummary: [...new Set(values)].join(' · '),
    imported: source.join('\n') || 'Source evidence not supplied by this importer',
    note: entry.notes || parityFallbackNote(group, entry.concept, path, field?.label),
    field: group === statuses[2] || group === statuses[3] ? undefined : field,
   })
  }
 }
 for (const [file, sections] of Object.entries(sourceFiles)) for (const [section, keys] of Object.entries(sections)) for (const [key, value] of Object.entries(keys)) {
  if (covered.has(`${file}/${section}/${key}`)) continue
  const disposition = dispositions[file]?.[section]?.[key]
  const parityEntry = parity.find(entry => entry.id === disposition?.parity_id)
  const group = dispositions[file] ? groupForStatus(parityEntry && disposition?.status === 'mapped' ? parityEntry.status : disposition?.status || '') : statuses[2]
  const canonicalGroup = resolveCanonicalRuntimeGroup(section)
  groups[group].push({
   id: `${file}/${section}/${key}`,
   category: section,
   group: canonicalGroup,
   subgroup: section,
   label: key,
   importedSummary: value || '(empty)',
   imported: `${value === '' ? '(empty)' : value} (${file})`,
   note: [disposition?.reason?.replace(/_/g, ' '), disposition?.note || sourceFallbackNote(file, section, key, group)].filter(Boolean).join(' — '),
  })
 }
 const kafka = metadata?.preserved?.java_kafka_settings as {
  settings?: Array<{ name: string; value: string; source_file: string; start_line?: number; go_support: string; go_fields?: string[]; notes?: string }>
  native_migration_blockers?: string[]
 } | undefined
 for (const [index, setting] of (kafka?.settings || []).entries()) {
  const group = setting.go_support === 'non_applicable' ? 'Not applicable' : ['partial', 'unsupported', 'unresolved', 'requires_binding'].includes(setting.go_support) ? 'Requires decision' : 'Preserved only'
  groups[group].push({
   id: `java-kafka:${index}:${setting.name}`,
   category: 'Kafka producer (Java source)',
   group: 'Protocol behavior & session',
   subgroup: 'Kafka producer (Java source)',
   label: setting.name,
   importedSummary: setting.value,
   imported: `${setting.value} (${setting.source_file}${setting.start_line ? `:${setting.start_line}` : ''})`,
   note: [`Go support: ${setting.go_support.replace(/_/g, ' ')}.`, setting.go_fields?.length ? `Equivalent Go fields: ${setting.go_fields.join(', ')}.` : '', setting.notes,
    'The imported draft retains its Java lifecycle. These are migration references; they are not applied to a native Go Kafka script.', ...(kafka?.native_migration_blockers || [])].filter(Boolean).join(' '),
  })
 }
 // Importers without parity show actual settings across all 7 canonical groups, never invented mapping evidence.
 const known = new Set(Object.values(groups).flat().map(r => r.path).filter(Boolean))
 const isHttpFamily = HTTP_FAMILY_PROTOCOLS.includes(script.protocol)
 const defaults = [
  'Target & identity',
  'Run logic',
  'Pacing',
  'Think time',
  'Error handling',
  'Logging',
  'Diagnostics',
  ...(isHttpFamily ? ['HTTP', 'Browser emulation'] : []),
  ...(script.protocol === 'soap' ? ['SOAP'] : []),
  ...(script.protocol === 'browser' ? ['Browser'] : []),
  ...(script.protocol === 'websocket' ? ['WebSocket'] : []),
  ...(script.protocol === 'kafka' ? ['Kafka'] : []),
  ...(script.protocol?.startsWith('jms') ? ['JMS'] : []),
 ]
 const current = activeFields.filter(f => {
  if (known.has(f.path)) return false
  const isConfigured = readSetting(script, f.path) !== undefined
  return (isConfigured || defaults.includes(f.category)) && settingIsRelevant(script, f.path, isConfigured)
 })
 groups['Additional APEX settings'] = current.map(f => ({
  id: f.path,
  category: f.category,
  group: f.group,
  subgroup: f.subgroup,
  label: f.label,
  path: f.path,
  imported: 'No source mapping reported',
  note: draftRuntimeSettingNote(f.path, f.label),
  field: f,
 }))
 return groups
}

export function settingIsRelevant(script: ScriptDefinition, path: string, isConfigured = false): boolean {
 if (path.startsWith('config.soap.') && script.protocol !== 'soap') return false
 const nativeReplayPolicies = ['http_errors_as_warnings', 'fail_noncritical_resources', 'resource_timeout_as_warning', 'legacy_html_checks', 'dns_cache', 'challenge_delay_ms']
 if (nativeReplayPolicies.some(key => path === `config.http.${key}`) && !['http', 'http/1.1', 'http/2', 'http/3', 'soap', 'java_http'].includes(script.protocol)) return false
 if (!isConfigured) {
  if ((path.startsWith('config.http.') || path.startsWith('config.browser_emulation.') || path.startsWith('config.web_runtime.')) && !HTTP_FAMILY_PROTOCOLS.includes(script.protocol)) return false
  if (path.startsWith('config.browser.') && script.protocol !== 'browser') return false
  if (path.startsWith('config.websocket.') && script.protocol !== 'websocket') return false
  if (path.startsWith('config.kafka.') && script.protocol !== 'kafka') return false
  if (path.startsWith('config.jms.') && !script.protocol?.startsWith('jms')) return false
 }
 const think = script.config?.think_time?.type || 'fixed'
 const pacing = script.config?.pacing?.type || 'none'
 const pacingModes: Record<string, string[]> = { delay_ms: ['fixed_delay'], interval_ms: ['fixed_interval'], min_delay_ms: ['random_delay'], max_delay_ms: ['random_delay'], min_interval_ms: ['random_interval'], max_interval_ms: ['random_interval'], tps: ['tps'] }
 if (path.startsWith('config.pacing.')) return !pacingModes[path.split('.').pop()!] || pacingModes[path.split('.').pop()!].includes(pacing)
 if (path === 'config.think_time.min_percent' || path === 'config.think_time.max_percent') return think === 'random_percentage'
 if (path === 'config.think_time.mean_ms') return ['fixed', 'gaussian', 'normal', 'exponential'].includes(think)
 if (path === 'config.think_time.std_ms') return ['gaussian', 'normal'].includes(think)
 if (path === 'config.think_time.min_ms' || path === 'config.think_time.max_ms') return ['random', 'uniform', 'gaussian', 'normal'].includes(think)
 return true
}
