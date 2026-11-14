import fieldSchema from '../../../pkg/api/import_settings_fields.json'
import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ScriptDefinition } from '../types/scripts'
import { getImportSummary } from './scriptEditorImportModel'
import { readImportedRuntimeFiles, readImportedRuntimeSettingDispositions } from './scriptEditorRuntimeSettingsModel'
import { draftRuntimeSettingNote, runtimeSettingEffects } from './importRuntimeSettingNotes'
import { getCanonicalGroupForPath } from './runtime-settings/canonicalRuntimeTaxonomy'
import { canonicalPacingType } from './runtimeWorkspaceModel'
import { canonicalThinkTimeType } from './thinkTimeConfig'

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

// Where a setting's draft value comes from: every row, rows edited in this
// review, rows with source evidence from the importer, and rows left at the
// engine default. Mapping status has its own checkboxes.
export const PROVENANCE_FILTERS = ['All settings', 'Modified', 'Imported', 'Default'] as const
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
 /** The import POST refuses a typed literal here; a new value must be a Credentials reference. */
 credentialReference?: boolean
 /**
  * Validation rejects any value here, so the editor shows a stored value
  * with a remove action and offers no entry (IMPORT_CLEAR_ONLY_PATHS).
  */
 clearOnly?: boolean
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
 'config.run_logic.termination_mode': 'Chooses whether the run duration or the iteration count stops execution.',
 'config.run_logic.block_scheduling': 'Runs top-level run logic blocks in sequence, in random order, or by percentage weight.',
 'config.run_logic.action_sequence': 'Orders the lifecycle sections each VUser executes.',
 'config.pacing.arrival_process': 'Selects the inter-arrival distribution used when pacing to a target TPS.',
 'config.pacing.burstiness': 'Sets the gamma shape parameter that controls how bursty gamma arrivals are.',
 'config.pacing.seed': 'Seeds random pacing and arrival sampling so runs repeat the same schedule.',
 'config.pacing.quota.tpm': 'Caps LLM token consumption per minute when pacing to a quota.',
 'config.pacing.quota.rpm': 'Caps requests per minute when pacing to a quota.',
 'config.pacing.quota.estimated_tokens_per_request': 'Reserves this many tokens per request before actual usage is observed.',
 'config.pacing.quota.burst_tokens': 'Sets the token bucket burst capacity for quota pacing.',
 'config.pacing.quota.adaptive_aimd': 'Backs off and probes the request rate when rate-limit responses arrive.',
 'config.pacing.quota.default_tokens': 'Counts this many tokens for a response that omits token usage.',
 'config.pacing.quota.headroom_ratio': 'Targets this fraction of the configured TPM or RPM quota.',
 // The runtime reads only an action's own think_time.force (pkg/dsl/config_catalog.go).
 'config.think_time.force': 'Has no effect at script level; only an action\'s own think time with force set waits its delay verbatim, regardless of ignore or scaling rules.',
 'config.http.auth.hosts': 'Limits default HTTP credentials to these hosts or host:port values.',
 'config.web_runtime.download_filters.mode': 'Allows or blocks embedded resource downloads by host.',
 'config.web_runtime.download_filters.hosts': 'Lists the host patterns the resource download filter matches.',
 'config.web_runtime.source_ips': 'Binds VUser connections to these local source IP addresses.',
 'config.error_handling.retry_on': 'Lists the failure classes eligible for retry, such as 429, 5xx, timeout, or network.',
 'config.results.protocol_mix.max_fallback_ratio': 'Fails the protocol mix check when more auto+h3 requests than this share fall back to TCP.',
 'config.results.protocol_mix.max_quic_failure_ratio': 'Fails the protocol mix check when more QUIC attempts than this share fail.',
 'config.results.protocol_mix.min_samples': 'Waits for this many eligible requests before evaluating protocol mix thresholds.',
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
 'config.http.proxy.password': 'Authenticates to the outbound HTTP proxy with this password. A new value must be a Credentials reference.',
 'config.http.auth.password': 'Supplies the default password for HTTP authentication challenges. A new value must be a Credentials reference.',
 'config.http.auth.token': 'Sends this bearer token for HTTP bearer authentication. A new value must be a Credentials reference.',
 'config.soap.ws_security.username_token.password': 'Injects this password into the SOAP WS-Security UsernameToken header. A new value must be a Credentials reference.',
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
 'config.http.user_agent': 'Sends this default User-Agent header on HTTP requests. A specific or custom browser emulation mode replaces it during replay.',
 'config.http.accept_language': 'Sends a language preference that can change localized server responses. A browser emulation language replaces it during replay.',
 'config.http.traceparent': 'Injects a W3C traceparent header on outbound HTTP requests for distributed tracing.',
 'config.http.traceparent_sampled': 'Sets the sampled flag on generated W3C traceparent headers.',
 'config.http.cookies_enabled': 'Maintains a per-VUser HTTP cookie jar across requests.',
 'config.http.clear_cookies_per_iteration': 'Flushes the per-VUser cookie jar at the start of every iteration.',
 'config.http.cache_enabled': 'Simulates per-VUser HTTP response caching using ETag and Last-Modified headers. A set browser emulation "Simulate browser cache" value replaces it during replay.',
 'config.http.aura_recorded_failures': 'Controls whether recorded Salesforce Aura action failures fail, warn, or are ignored.',
 'config.browser_emulation.mode': 'Selects preset browser profile emulation, a custom User-Agent, or disables emulation.',
 'config.browser_emulation.cache.simulate_cache': 'Enables per-VUser HTTP response caching under browser emulation rules.',
 'config.browser_emulation.cache.cache_content_urls': 'Restricts simulated browser cache storage to HTML content responses.',
 'config.browser_emulation.cache.check_newer_versions': 'Revalidates cached responses with the origin server on every visit.',
 'config.browser_emulation.cache.download_non_html_resources': 'Parses HTML responses and automatically fetches embedded images, stylesheets, and scripts.',
 'config.browser_emulation.cache.new_user_each_iteration': 'Resets emulated browser session state and cookies at the start of each iteration.',
 'config.browser_emulation.cache.simulate_prefetch': 'Fetches HTML prefetch, preload, and prerender resource hints during replay.',
 'config.web_runtime.network_simulation.mode': 'Selects maximum throughput (no throttling), a preset profile, or custom network throttling parameters.',
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
  const byPath = canonicalRuntimeGroupForPath(path)
  if (byPath) return byPath
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

// Subgroup buckets for resolveRuntimeSubgroup. The canonical group of a path
// comes from getCanonicalGroupForPath (runtime-settings/canonicalRuntimeTaxonomy),
// the one path resolver parity-tested against pkg/dsl/config_catalog.go.
const TRANSPORT_HTTP_PATHS = new Set([
 'config.http.version',
 'config.http.connect_timeout_ms',
 'config.http.request_timeout_ms',
 'config.http.tls_timeout_ms',
 'config.http.idle_timeout_ms',
 'config.http.page_timeout_ms',
 'config.http.max_conns_per_host',
 'config.http.max_idle_conns',
 'config.http.keep_alive',
 'config.http.ip_version',
 'config.http.dns_cache',
 'config.http.receive_buffer_bytes',
])
const DIAGNOSTIC_HTTP_PATHS = new Set([
 'config.http.http_errors_as_warnings',
 'config.http.fail_noncritical_resources',
 'config.http.resource_timeout_as_warning',
 'config.http.content_check_error_limit',
 'config.http.traceparent',
 'config.http.traceparent_sampled',
])
const IDENTITY_PATHS = new Set([
 'config.base_url',
 'config.http.user_agent',
 'config.http.accept_language',
 'config.browser_emulation.mode',
 'config.browser_emulation.browser_type',
 'config.browser_emulation.browser_version',
 'config.browser_emulation.platform',
 'config.browser_emulation.language',
 'config.browser_emulation.custom_user_agent',
])

// The canonical group label for a config path, from the shared taxonomy.
function canonicalRuntimeGroupForPath(path: string): CanonicalRuntimeGroup | undefined {
 const id = getCanonicalGroupForPath(path)
 if (!id) return undefined
 return Object.entries(CANONICAL_RUNTIME_GROUP_IDS).find(([, groupId]) => groupId === id)?.[0] as CanonicalRuntimeGroup | undefined
}

export function resolveRuntimeSubgroup(path?: string, fallbackCategory?: string): string {
 if (!path) return fallbackCategory || 'General'
 if (path === 'config.base_url') return 'Target & identity'
 if (IDENTITY_PATHS.has(path)) return 'Browser identity'
 if (path.startsWith('config.run_logic.')) return 'Run logic'
 if (path.startsWith('config.pacing.quota.')) return 'Quota pacing'
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
  ].includes(path)
 ) {
  return 'Timeouts'
 }
 if (TRANSPORT_HTTP_PATHS.has(path)) return 'Protocol & connection pool'
 if (path.startsWith('config.web_runtime.network_simulation.') || path === 'config.web_runtime.source_ips') return 'Network simulation & source IPs'
 if (path.startsWith('config.http.tls.')) return 'TLS'
 if (path === 'config.http.proxy_mode' || path.startsWith('config.http.proxy.')) return 'Proxy'
 if (path.startsWith('config.http.auth.') || path === 'config.http.challenge_delay_ms') return 'HTTP authentication'
 if (path.startsWith('config.soap.ws_security.')) return 'SOAP WS-Security'
 if (path.startsWith('config.soap.')) return 'SOAP envelope & faults'
 if (DIAGNOSTIC_HTTP_PATHS.has(path)) return 'HTTP error policies & tracing'
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
 { category: 'Target & identity', path: 'config.base_url', label: 'Base URL', type: 'text' },

 // 2. Run logic & pacing
 { category: 'Run logic', path: 'config.run_logic.iterations', label: 'Iterations per VUser', type: 'number' },
 { category: 'Run logic', path: 'config.run_logic.global_iterations', label: 'Shared iterations', type: 'number' },
 { category: 'Run logic', path: 'config.run_logic.max_transactions', label: 'Transaction limit', type: 'number' },
 { category: 'Run logic', path: 'config.run_logic.workload_model', label: 'Workload model', type: 'select', options: ['closed', 'open'] },
 { category: 'Run logic', path: 'config.run_logic.max_in_flight', label: 'Maximum in-flight VUsers', type: 'number' },
 { category: 'Run logic', path: 'config.run_logic.auto_transactions', label: 'Automatic action transactions', type: 'select', options: ['none', 'per_action', 'per_iteration'] },
 { category: 'Run logic', path: 'config.run_logic.termination_mode', label: 'Default termination policy', type: 'select', options: ['duration', 'iterations'] },
 { category: 'Run logic', path: 'config.run_logic.block_scheduling', label: 'Action block scheduling', type: 'select', options: ['sequential', 'random', 'percentage'] },
 { category: 'Run logic', path: 'config.run_logic.action_sequence', label: 'Lifecycle section sequence', type: 'lines' },
 { category: 'Pacing', path: 'config.pacing.type', label: 'Mode', type: 'select', options: ['none', 'fixed_delay', 'random_delay', 'fixed_interval', 'random_interval', 'tps', 'quota'] },
 { category: 'Pacing', path: 'config.pacing.delay_ms', label: 'Delay (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.min_delay_ms', label: 'Minimum delay (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.max_delay_ms', label: 'Maximum delay (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.interval_ms', label: 'Interval (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.min_interval_ms', label: 'Minimum interval (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.max_interval_ms', label: 'Maximum interval (ms)', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.tps', label: 'Target TPS', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.arrival_process', label: 'Arrival process', type: 'select', options: ['uniform', 'poisson', 'gamma'] },
 { category: 'Pacing', path: 'config.pacing.burstiness', label: 'Gamma burstiness shape', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.seed', label: 'Arrival random seed', type: 'signed' },
 { category: 'Pacing', path: 'config.pacing.quota.tpm', label: 'Tokens per minute budget', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.quota.rpm', label: 'Requests per minute budget', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.quota.estimated_tokens_per_request', label: 'Estimated tokens per request', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.quota.burst_tokens', label: 'Token bucket burst capacity', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.quota.adaptive_aimd', label: 'Adaptive rate probing (AIMD)', type: 'boolean' },
 { category: 'Pacing', path: 'config.pacing.quota.default_tokens', label: 'Fallback tokens per request', type: 'number' },
 { category: 'Pacing', path: 'config.pacing.quota.headroom_ratio', label: 'Quota headroom ratio', type: 'number' },

 // 3. Think time
 { category: 'Think time', path: 'config.think_time.ignore', label: 'Ignore think time', type: 'boolean' },
 { category: 'Think time', path: 'config.think_time.type', label: 'Mode', type: 'select', options: ['none', 'fixed', 'recorded', 'random_percentage', 'gaussian', 'normal', 'uniform', 'exponential'] },
 { category: 'Think time', path: 'config.think_time.distribution', label: 'Distribution override', type: 'select', options: ['uniform', 'exponential'] },
 { category: 'Think time', path: 'config.think_time.mean_ms', label: 'Mean delay (ms)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.std_ms', label: 'Standard deviation (ms)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.min_ms', label: 'Minimum delay (ms)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.max_ms', label: 'Maximum delay (ms)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.min_percent', label: 'Minimum recorded delay (%)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.max_percent', label: 'Maximum recorded delay (%)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.multiplier', label: 'Multiplier', type: 'number' },
 { category: 'Think time', path: 'config.think_time.limit_ms', label: 'Delay cap (ms)', type: 'number' },
 { category: 'Think time', path: 'config.think_time.force', label: 'Force exact think time (script level, no effect)', type: 'boolean' },

 // 4. Transport & connections
 { category: 'HTTP', path: 'config.http.version', label: 'HTTP version', type: 'select', options: ['1.1', '2', 'auto+h3', '3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.quic.handshake_timeout_ms', label: 'QUIC handshake timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.quic.max_idle_timeout_ms', label: 'QUIC max idle timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.quic.keep_alive_ms', label: 'QUIC keep-alive ping interval (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.quic.broken_ttl_ms', label: 'QUIC fallback cooldown (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.quic.alt_svc_cache', label: 'Cache Alt-Svc HTTP/3 announcements', type: 'select', options: ['per_vuser', 'per_iteration'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.connect_timeout_ms', label: 'Connect timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.request_timeout_ms', label: 'Request timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls_timeout_ms', label: 'TLS handshake timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.idle_timeout_ms', label: 'Keep-alive idle timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.page_timeout_ms', label: 'Whole-page timeout (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.challenge_delay_ms', label: 'Digest authentication delay (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.max_conns_per_host', label: 'Connections per host', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.max_idle_conns', label: 'Maximum idle connections', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.keep_alive', label: 'Keep connections alive', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.max_redirects', label: 'Maximum redirects', type: 'signed', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.ip_version', label: 'IP family', type: 'select', options: ['auto', 'ipv4', 'ipv6'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.dns_cache', label: 'Per-VUser DNS cache', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.receive_buffer_bytes', label: 'Response read buffer (bytes)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },

 // 5. Security, proxy & auth
 { category: 'HTTP', path: 'config.http.tls.min_version', label: 'Minimum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls.max_version', label: 'Maximum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls.insecure_skip_verify', label: 'Skip TLS certificate verification', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls.client_cert_file', label: 'Client certificate file (mTLS)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls.client_key_file', label: 'Client private key file (mTLS)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.tls.ca_cert_file', label: 'Custom CA certificate bundle', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy_mode', label: 'Proxy mode', type: 'select', options: ['none', 'system', 'custom', 'pac'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy.url', label: 'Proxy URL', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy.pac_url', label: 'PAC URL (bounded evaluator)', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy.username', label: 'Proxy username', type: 'text', credentialReference: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy.password', label: 'Proxy password', type: 'text', credentialReference: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.proxy.no_proxy', label: 'Proxy bypass hosts (NO_PROXY)', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.type', label: 'HTTP authentication scheme', type: 'select', options: ['basic', 'bearer', 'digest', 'ntlm', 'negotiate', 'negotiate_any'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.username', label: 'HTTP auth username', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.password', label: 'HTTP auth password', type: 'text', credentialReference: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.token', label: 'Bearer token', type: 'text', credentialReference: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.domain', label: 'NTLM / Windows domain', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.auth.hosts', label: 'Authenticated hosts', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'SOAP', path: 'config.soap.ws_security.username_token.username', label: 'WS-Security username', type: 'text', protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.ws_security.username_token.password', label: 'WS-Security password', type: 'text', credentialReference: true, protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.ws_security.username_token.password_type', label: 'WS-Security password type', type: 'select', options: ['text', 'digest'], protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.ws_security.username_token.nonce', label: 'Include WS-Security nonce', type: 'boolean', protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.ws_security.username_token.created', label: 'Include WS-Security created timestamp', type: 'boolean', protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.ws_security.timestamp.ttl_seconds', label: 'WS-Security timestamp TTL (s)', type: 'number', protocols: ['soap'] },

 // 6. Protocol behavior & session
 { category: 'SOAP', path: 'config.soap.version', label: 'SOAP protocol version', type: 'select', options: ['1.1', '1.2'], protocols: ['soap'] },
 { category: 'SOAP', path: 'config.soap.fault_handling.fail_on_fault', label: 'Fail action on SOAP Fault', type: 'boolean', triState: true, protocols: ['soap'] },
 { category: 'HTTP', path: 'config.http.user_agent', label: 'Default User-Agent header', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.accept_language', label: 'Accept language', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.accept_encoding', label: 'Accept encoding', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.traceparent', label: 'Propagate W3C traceparent header', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.traceparent_sampled', label: 'Set traceparent sampled flag', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.cookies_enabled', label: 'Enable per-VUser cookie jar', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.clear_cookies_per_iteration', label: 'Clear cookies each iteration', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.cache_enabled', label: 'Enable HTTP response cache', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.cache_max_unused_iterations', label: 'Unused iterations before cache eviction', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.cache_excluded_mime_types', label: 'Do not cache MIME types', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.max_meta_refresh', label: 'Meta-refresh limit per URL', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.content_check_error_limit', label: 'Content-check error match limit', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.http_errors_as_warnings', label: 'HTTP errors as warnings', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.fail_noncritical_resources', label: 'Fail on non-critical resource errors', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.resource_timeout_as_warning', label: 'Resource timeouts as warnings', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.legacy_html_checks', label: 'Legacy HTML checks', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.html_parse_mode', label: 'Discover HTML resources in', type: 'select', options: ['html', 'text', 'any'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.html_excluded_mime_types', label: 'Do not discover resources in MIME types', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.http.aura_recorded_failures', label: 'Salesforce Aura recorded failure policy', type: 'select', options: ['require_success', 'accept_recorded'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.mode', label: 'Browser emulation mode', type: 'select', options: ['specific', 'custom'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.browser_type', label: 'Browser type', type: 'select', options: ['chrome', 'edge', 'firefox', 'safari', 'ie'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.browser_version', label: 'Browser version', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.platform', label: 'Platform', type: 'select', options: ['windows', 'macos', 'linux', 'android', 'ios'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.language', label: 'Language', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.custom_user_agent', label: 'User agent', type: 'text', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.clear_cache_for_new_user', label: 'Clear cache for a new user', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.simulate_cache', label: 'Simulate browser cache', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.cache_content_urls', label: 'Cache URLs requiring content (HTML)', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.check_newer_versions', label: 'Check for newer versions every visit', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.download_non_html_resources', label: 'Download non-HTML embedded resources', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.new_user_each_iteration', label: 'Simulate a new user on each iteration', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Browser emulation', path: 'config.browser_emulation.cache.simulate_prefetch', label: 'Simulate browser prefetch and prerender', type: 'boolean', triState: true, protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.mode', label: 'Network simulation mode', type: 'select', options: ['max', 'preset', 'custom'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.preset', label: 'Network simulation preset', type: 'select', options: ['56k', 'dsl', 't1', '10mbps'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.bandwidth_kbps', label: 'Simulated shared bandwidth (kbps)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.latency_ms', label: 'Simulated network latency (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.jitter_ms', label: 'Simulated jitter (ms)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.network_simulation.packet_loss_pct', label: 'Simulated packet loss (%)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.streaming.mode', label: 'Media streaming mode', type: 'select', options: ['adaptive', 'fixed'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.streaming.buffer_size_seconds', label: 'Streaming buffer duration (s)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.streaming.fixed_bitrate_kbps', label: 'Fixed streaming bitrate (kbps)', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.streaming.bandwidth_estimation', label: 'Streaming bandwidth estimation', type: 'select', options: ['rolling', 'instant'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.data_formatting.auto_detect', label: 'Auto-detect data formatting', type: 'boolean', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.download_filters.mode', label: 'Resource download filter mode', type: 'select', options: ['none', 'allow_list', 'block_list'], protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.download_filters.hosts', label: 'Filtered resource hosts', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'HTTP', path: 'config.web_runtime.source_ips', label: 'Local source IP pool', type: 'lines', protocols: HTTP_FAMILY_PROTOCOLS },

 // 7. Errors, logging & diagnostics
 { category: 'Error handling', path: 'config.error_handling.continue_on_error', label: 'Continue on error', type: 'boolean' },
 { category: 'Error handling', path: 'config.error_handling.snapshot_on_error', label: 'Capture snapshot on error', type: 'boolean' },
 { category: 'Error handling', path: 'config.error_handling.fail_transaction_on_error_message', label: 'Fail open transactions on error message', type: 'boolean' },
 { category: 'Error handling', path: 'config.error_handling.max_retries', label: 'Maximum retries', type: 'number' },
 { category: 'Error handling', path: 'config.error_handling.retry_delay_ms', label: 'Retry delay (ms)', type: 'number' },
 { category: 'Error handling', path: 'config.error_handling.retry_on', label: 'Retryable failure conditions', type: 'lines' },
 { category: 'Error handling', path: 'config.error_handling.retry_backoff', label: 'Retry backoff strategy', type: 'select', options: ['fixed', 'exponential', 'retry_after'] },
 { category: 'Error handling', path: 'config.error_handling.honor_retry_after', label: 'Honor HTTP Retry-After header', type: 'boolean' },
 { category: 'Error handling', path: 'config.error_handling.retry_after_max_ms', label: 'Maximum Retry-After delay (ms)', type: 'number' },
 { category: 'Logging', path: 'config.log.level', label: 'Log level', type: 'select', options: ['disabled', 'standard', 'extended'] },
 { category: 'Logging', path: 'config.log.error_buffer_bytes', label: 'Error history buffer (bytes)', type: 'number' },
 { category: 'Logging', path: 'config.log.max_response_bytes', label: 'Response-log limit (bytes; 0 uses engine bound)', type: 'number' },
 { category: 'Logging', path: 'config.log.on_error_only', label: 'Log on error only', type: 'boolean' },
 { category: 'Logging', path: 'config.log.redact_run_evidence', label: 'Redact sensitive values in run evidence', type: 'boolean' },
 { category: 'Logging', path: 'config.log.extended_options', label: 'Extended log options', type: 'lines', options: ['parameter_substitution', 'data_returned', 'advanced_trace'] },
 { category: 'Diagnostics', path: 'config.results.message_detail.mode', label: 'Exchange capture', type: 'select', options: ['off', 'errors', 'sampled', 'adaptive'] },
 { category: 'Diagnostics', path: 'config.results.message_detail.sample_rate', label: 'Capture sample rate', type: 'number' },
 { category: 'Diagnostics', path: 'config.results.message_detail.include_resources', label: 'Include fetched resources', type: 'boolean' },
 { category: 'Diagnostics', path: 'config.results.message_detail.max_items', label: 'Retained exchange limit', type: 'number' },
 { category: 'Diagnostics', path: 'config.results.message_detail.max_item_bytes', label: 'Bytes per exchange', type: 'number' },
 { category: 'Diagnostics', path: 'config.results.message_detail.max_total_bytes', label: 'Total capture bytes', type: 'number' },
 { category: 'Diagnostics', path: 'config.results.protocol_mix.max_fallback_ratio', label: 'Maximum HTTP/3 TCP fallback ratio', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Diagnostics', path: 'config.results.protocol_mix.max_quic_failure_ratio', label: 'Maximum QUIC failure ratio', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
 { category: 'Diagnostics', path: 'config.results.protocol_mix.min_samples', label: 'Minimum protocol mix samples', type: 'number', protocols: HTTP_FAMILY_PROTOCOLS },
]

// The editable import-scope fields of the remaining protocol families
// (pkg/dsl/config_catalog.go), with the options the import POST accepts. They
// stand in for the served list until it arrives; the parity test checks them
// against the catalog. A catalog secret field takes a Credentials reference
// only when a new value is typed; the imported value stays as it is.
const PROTOCOL_FIELDS: Array<Omit<ImportRuntimeField, 'groupId' | 'subgroup'>> = [
 // Kafka
 { category: 'Kafka', group: 'Target & identity', path: 'config.kafka.brokers', label: 'Brokers (one per line)', type: 'lines', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.mock', label: 'Mock broker', type: 'boolean', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.auto_offset_reset', label: 'Offset reset', type: 'select', options: ['earliest', 'latest'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.serialization', label: 'Serialization', type: 'select', options: ['json_schema', 'avro'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Target & identity', path: 'config.kafka.schema_registry_url', label: 'Schema registry URL', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.schema_id', label: 'Schema ID', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.subject', label: 'Subject', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.profile', label: 'Kafka broker profile', type: 'select', options: ['eventhubs'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Target & identity', path: 'config.kafka.client_id', label: 'Kafka client ID', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Target & identity', path: 'config.kafka.topic', label: 'Default Kafka topic', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.consumer_group', label: 'Consumer group ID', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.consumer_transactions', label: 'Transactional consumer-producer commits', type: 'boolean', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.isolation_level', label: 'Consumer transaction isolation level', type: 'select', options: ['read_uncommitted', 'read_committed'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.acks', label: 'Producer acknowledgement level', type: 'select', options: ['none', 'leader', 'all'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.idempotence', label: 'Idempotent producer mode', type: 'select', options: ['enabled', 'disabled'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.transactional_id', label: 'Producer transactional ID', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.compression', label: 'Record batch compression', type: 'select', options: ['none', 'gzip', 'snappy', 'lz4', 'zstd'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.partition_strategy', label: 'Partition routing strategy', type: 'select', options: ['key-hash', 'round-robin', 'explicit'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.partitions', label: 'Mock topic partition count', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.timeout_ms', label: 'Kafka operation timeout (ms)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.max_message_bytes', label: 'Maximum record size (bytes)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.retries', label: 'Producer delivery retries', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.linger_ms', label: 'Producer batch linger delay (ms)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.batch_bytes', label: 'Producer batch size (bytes)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.buffer_bytes', label: 'Producer memory buffer (bytes)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.admission_timeout_ms', label: 'Buffer admission timeout (ms)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.delivery_timeout_ms', label: 'End-to-end delivery timeout (ms)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.request_timeout_ms', label: 'Broker request timeout (ms)', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Transport & connections', path: 'config.kafka.producer.max_in_flight_requests', label: 'Maximum in-flight requests per connection', type: 'number', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.security_protocol', label: 'Kafka security protocol', type: 'select', options: ['plaintext', 'ssl', 'sasl_plaintext', 'sasl_ssl'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.sasl_mechanism', label: 'SASL authentication mechanism', type: 'select', options: ['plain', 'scram-sha-256', 'scram-sha-512', 'oauthbearer'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.sasl_username', label: 'SASL username', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.sasl_password', label: 'SASL password', type: 'text', credentialReference: true, protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.sasl_password_secret', label: 'SASL password secret name', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth_scope', label: 'Managed identity OAuth scope', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.mode', label: 'Kafka OAuth mode', type: 'select', options: ['client_credentials'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.token_url', label: 'OAuth token endpoint URL', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.client_id', label: 'OAuth client ID', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.client_secret', label: 'OAuth client secret', type: 'text', credentialReference: true, protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.client_secret_ref', label: 'OAuth client secret name', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.scope', label: 'OAuth token scope', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.oauth.authentication_method', label: 'OAuth token authentication method', type: 'select', options: ['client_secret_basic', 'client_secret_post', 'kafka_3_3_1_basic'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.min_version', label: 'Kafka minimum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.max_version', label: 'Kafka maximum TLS version', type: 'select', options: ['1.2', '1.3'], protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.insecure_skip_verify', label: 'Skip Kafka TLS certificate verification', type: 'boolean', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.client_cert_file', label: 'Kafka client certificate file', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.client_key_file', label: 'Kafka client private key file', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Security, proxy & auth', path: 'config.kafka.tls.ca_cert_file', label: 'Kafka CA certificate file', type: 'text', protocols: ['kafka'] },
 { category: 'Kafka', group: 'Protocol behavior & session', path: 'config.kafka.schema', label: 'Inline schema definition', type: 'text', protocols: ['kafka'] },
 // Diagnostics
 { category: 'Diagnostics', group: 'Errors, logging & diagnostics', path: 'config.observability.trace_carrier', label: 'Messaging trace context carrier', type: 'select', options: ['none', 'w3c'], protocols: ['kafka', 'jms', 'eventhubs'] },
 // JMS
 { category: 'JMS', group: 'Target & identity', path: 'config.jms.broker_url', label: 'Broker URL', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.destination_type', label: 'Destination type', type: 'select', options: ['queue', 'topic'], protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.subscription', label: 'Subscription', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Transport & connections', path: 'config.jms.timeout_ms', label: 'Timeout (ms)', type: 'number', protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.mock', label: 'Mock broker', type: 'boolean', protocols: ['jms'] },
 { category: 'JMS', group: 'Target & identity', path: 'config.jms.provider', label: 'JMS broker provider', type: 'select', options: ['solace', 'servicebus', 'ibmmq'], protocols: ['jms'] },
 { category: 'JMS', group: 'Target & identity', path: 'config.jms.message_vpn', label: 'Solace message VPN', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Security, proxy & auth', path: 'config.jms.username', label: 'JMS broker username', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Security, proxy & auth', path: 'config.jms.password', label: 'JMS broker password', type: 'text', credentialReference: true, protocols: ['jms'] },
 { category: 'JMS', group: 'Target & identity', path: 'config.jms.client_id', label: 'JMS durable client ID', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Target & identity', path: 'config.jms.destination', label: 'Default JMS destination', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.reply_to', label: 'Default JMSReplyTo destination', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.acknowledgement', label: 'Session acknowledgement mode', type: 'select', options: ['auto', 'dups_ok', 'client', 'transacted'], protocols: ['jms'] },
 { category: 'JMS', group: 'Protocol behavior & session', path: 'config.jms.delivery_mode', label: 'Message delivery mode', type: 'select', options: ['persistent', 'non_persistent'], protocols: ['jms'] },
 { category: 'JMS', group: 'Security, proxy & auth', path: 'config.jms.tls.ca_cert_secret', label: 'JMS CA certificate secret', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Security, proxy & auth', path: 'config.jms.tls.trust_store_ref', label: 'JMS trust store reference', type: 'text', protocols: ['jms'] },
 { category: 'JMS', group: 'Security, proxy & auth', path: 'config.jms.tls.insecure_skip_verify', label: 'Skip JMS TLS certificate verification', type: 'boolean', protocols: ['jms'] },
 // Event Hubs
 { category: 'Event Hubs', group: 'Target & identity', path: 'config.eventhubs.namespace', label: 'Event Hubs namespace', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Target & identity', path: 'config.eventhubs.event_hub', label: 'Event Hub name', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Security, proxy & auth', path: 'config.eventhubs.auth_type', label: 'Event Hubs authentication mode', type: 'select', options: ['connection_string', 'aad'], protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Security, proxy & auth', path: 'config.eventhubs.connection_string', label: 'Event Hubs SAS connection string', type: 'text', credentialReference: true, protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Security, proxy & auth', path: 'config.eventhubs.connection_string_secret', label: 'Connection string secret name', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.consumer_group', label: 'Event Hubs consumer group', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.consumer_mode', label: 'Consumer execution mode', type: 'select', options: ['observer', 'processor', 'exclusive'], protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.checkpoint_container_url', label: 'Checkpoint Blob container URL', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Transport & connections', path: 'config.eventhubs.processor_max_partitions', label: 'Maximum processor partitions', type: 'number', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.owner_level', label: 'Exclusive consumer owner level', type: 'number', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Errors, logging & diagnostics', path: 'config.eventhubs.observe_partition_tail', label: 'Observe partition tail lag', type: 'boolean', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Errors, logging & diagnostics', path: 'config.eventhubs.tail_poll_interval_ms', label: 'Partition tail poll interval (ms)', type: 'number', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.partition_key', label: 'Default partition routing key', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.partition_id', label: 'Default explicit partition ID', type: 'text', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.start_position', label: 'Observer start position', type: 'select', options: ['latest', 'earliest'], protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Transport & connections', path: 'config.eventhubs.timeout_ms', label: 'Event Hubs operation timeout (ms)', type: 'number', protocols: ['eventhubs'] },
 { category: 'Event Hubs', group: 'Protocol behavior & session', path: 'config.eventhubs.mock', label: 'Use in-memory mock Event Hub', type: 'boolean', protocols: ['eventhubs'] },
 // gRPC
 { category: 'gRPC', group: 'Protocol behavior & session', path: 'config.grpc.proto_files', label: 'Protobuf descriptor files', type: 'lines', protocols: ['grpc'] },
 { category: 'gRPC', group: 'Protocol behavior & session', path: 'config.grpc.use_reflection', label: 'Use gRPC server reflection', type: 'boolean', protocols: ['grpc'] },
 { category: 'gRPC', group: 'Security, proxy & auth', path: 'config.grpc.plain_text', label: 'Use plaintext h2c connection', type: 'boolean', protocols: ['grpc'] },
 { category: 'gRPC', group: 'Transport & connections', path: 'config.grpc.max_recv_msg_size', label: 'Maximum receive message size (bytes)', type: 'number', protocols: ['grpc'] },
 { category: 'gRPC', group: 'Transport & connections', path: 'config.grpc.timeout_ms', label: 'Default RPC timeout (ms)', type: 'number', protocols: ['grpc'] },
 { category: 'gRPC', group: 'Target & identity', path: 'config.grpc.authority', label: 'HTTP/2 :authority override', type: 'text', protocols: ['grpc'] },
 // WebSocket
 { category: 'WebSocket', group: 'Protocol behavior & session', path: 'config.websocket.callback_interval_ms', label: 'Callback dispatch interval (ms)', type: 'number', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Transport & connections', path: 'config.websocket.read_timeout_ms', label: 'WebSocket read timeout (ms)', type: 'number', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Transport & connections', path: 'config.websocket.write_timeout_ms', label: 'WebSocket write timeout (ms)', type: 'number', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Transport & connections', path: 'config.websocket.max_message_size', label: 'Maximum WebSocket message size (bytes)', type: 'number', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Protocol behavior & session', path: 'config.websocket.subprotocol', label: 'WebSocket subprotocol', type: 'text', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Target & identity', path: 'config.websocket.origin', label: 'WebSocket Origin header', type: 'text', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Transport & connections', path: 'config.websocket.compression', label: 'Enable per-message deflate compression', type: 'boolean', protocols: ['websocket'] },
 { category: 'WebSocket', group: 'Transport & connections', path: 'config.websocket.reconnect', label: 'Reconnect on unexpected close', type: 'boolean', protocols: ['websocket'] },
 // Database
 { category: 'Database', group: 'Target & identity', path: 'config.database.driver', label: 'Database driver', type: 'select', options: ['sqlite', 'postgres'], protocols: ['database'] },
 { category: 'Database', group: 'Security, proxy & auth', path: 'config.database.dsn', label: 'Database connection string (DSN)', type: 'text', credentialReference: true, protocols: ['database'] },
 { category: 'Database', group: 'Security, proxy & auth', path: 'config.database.dsn_secret', label: 'DSN project secret name', type: 'text', protocols: ['database'] },
 { category: 'Database', group: 'Transport & connections', path: 'config.database.max_open_conns', label: 'Maximum open database connections', type: 'number', protocols: ['database'] },
 { category: 'Database', group: 'Transport & connections', path: 'config.database.max_idle_conns', label: 'Maximum idle database connections', type: 'number', protocols: ['database'] },
 { category: 'Database', group: 'Transport & connections', path: 'config.database.conn_max_lifetime_ms', label: 'Connection maximum lifetime (ms)', type: 'number', protocols: ['database'] },
 { category: 'Database', group: 'Transport & connections', path: 'config.database.query_timeout_ms', label: 'Query execution timeout (ms)', type: 'number', protocols: ['database'] },
 { category: 'Database', group: 'Protocol behavior & session', path: 'config.database.max_rows', label: 'Maximum rows fetched per query', type: 'number', protocols: ['database'] },
 { category: 'Database', group: 'Protocol behavior & session', path: 'config.database.read_only', label: 'Read-only session mode', type: 'boolean', protocols: ['database'] },
 { category: 'Database', group: 'Protocol behavior & session', path: 'config.database.rollback_on_teardown', label: 'Rollback uncommitted transactions on teardown', type: 'boolean', protocols: ['database'] },
 // Network
 { category: 'Network', group: 'Target & identity', path: 'config.network.subprotocol', label: 'Network subprotocol', type: 'select', options: ['dns', 'tcp', 'udp', 'ftp', 'smtp', 'pop3', 'imap', 'ldap'], protocols: ['network'] },
 { category: 'Network', group: 'Target & identity', path: 'config.network.host', label: 'Target host', type: 'text', protocols: ['network'] },
 { category: 'Network', group: 'Target & identity', path: 'config.network.port', label: 'Target port', type: 'number', protocols: ['network'] },
 { category: 'Network', group: 'Target & identity', path: 'config.network.address', label: 'Target host:port address', type: 'text', protocols: ['network'] },
 { category: 'Network', group: 'Transport & connections', path: 'config.network.timeout_ms', label: 'Network operation timeout (ms)', type: 'number', protocols: ['network'] },
 { category: 'Network', group: 'Security, proxy & auth', path: 'config.network.tls', label: 'Enable TLS encryption', type: 'boolean', protocols: ['network'] },
 { category: 'Network', group: 'Security, proxy & auth', path: 'config.network.username', label: 'Network username or bind DN', type: 'text', protocols: ['network'] },
 { category: 'Network', group: 'Security, proxy & auth', path: 'config.network.password', label: 'Network password', type: 'text', credentialReference: true, protocols: ['network'] },
 { category: 'Network', group: 'Protocol behavior & session', path: 'config.network.from', label: 'SMTP sender address', type: 'text', protocols: ['network'] },
 { category: 'Network', group: 'Protocol behavior & session', path: 'config.network.to', label: 'SMTP recipient addresses', type: 'lines', protocols: ['network'] },
 { category: 'Network', group: 'Protocol behavior & session', path: 'config.network.base_dn', label: 'LDAP base DN', type: 'text', protocols: ['network'] },
 // Appium
 { category: 'Appium', group: 'Target & identity', path: 'config.appium.server_url', label: 'Appium WebDriver server URL', type: 'text', protocols: ['appium'] },
 { category: 'Appium', group: 'Transport & connections', path: 'config.appium.session_timeout_ms', label: 'Session provisioning timeout (ms)', type: 'number', protocols: ['appium'] },
 { category: 'Appium', group: 'Transport & connections', path: 'config.appium.element_timeout_ms', label: 'Element wait timeout (ms)', type: 'number', protocols: ['appium'] },
 // Java HTTP
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.codec', label: 'Java payload codec', type: 'select', options: ['java_serialized_object_base64'], protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.helper_mode', label: 'Java serializer helper mode', type: 'select', options: ['embedded'], protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.content_type', label: 'Default Content-Type header', type: 'text', protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.accept', label: 'Default Accept header', type: 'text', protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.body_encoding', label: 'Request body encoding', type: 'select', options: ['base64'], protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.object_schema', label: 'Java object class or schema', type: 'text', protocols: ['java_http'] },
 { category: 'Java HTTP', group: 'Protocol behavior & session', path: 'config.java_http.preserve_binary_result', label: 'Preserve binary response payload', type: 'boolean', protocols: ['java_http'] },
 // Java VUser
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.enabled', label: 'Enable Java VUser execution', type: 'boolean', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.runner_command', label: 'Runner command', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.harness_jar', label: 'Harness JAR path', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.classpath', label: 'JVM classpath entries', type: 'lines', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.work_dir', label: 'Working directory', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Transport & connections', path: 'config.java_vuser.max_processes', label: 'Maximum JVM processes', type: 'number', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Transport & connections', path: 'config.java_vuser.startup_timeout_ms', label: 'JVM startup timeout (ms)', type: 'number', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Transport & connections', path: 'config.java_vuser.action_timeout_ms', label: 'Java action timeout (ms)', type: 'number', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.jdk_selection', label: 'JDK selection mode', type: 'select', options: ['internal', 'specified'], protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.jdk_home', label: 'Specified JDK home path', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.additional_vm_parameters', label: 'Additional JVM arguments', type: 'lines', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.use_xbootclasspath', label: 'Prepend classpath to -Xbootclasspath', type: 'boolean', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.dedicated_class_loader', label: 'Use dedicated class loader per VUser', type: 'boolean', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.skip_parameter_evaluation', label: 'Skip automatic parameter evaluation', type: 'boolean', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.param_left_brace', label: 'Parameter left delimiter', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Protocol behavior & session', path: 'config.java_vuser.param_right_brace', label: 'Parameter right delimiter', type: 'text', protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Transport & connections', path: 'config.java_vuser.jvm_sharing', label: 'JVM sharing mode', type: 'select', options: ['dedicated', 'shared', 'threads'], protocols: ['java_vuser'] },
 { category: 'Java VUser', group: 'Transport & connections', path: 'config.java_vuser.vusers_per_jvm', label: 'VUsers per shared JVM', type: 'number', protocols: ['java_vuser'] },
 // Browser
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.system_language', label: 'Emulated system language', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.available_screen_width', label: 'Available screen width', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.available_screen_height', label: 'Available screen height', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.engine', label: 'Browser engine', type: 'select', options: ['chromium', 'webkit'], protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.headless', label: 'Run browser headless', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.slow_mo_ms', label: 'Slow-motion step delay (ms)', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.user_agent', label: 'Browser User-Agent override', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.locale', label: 'Browser locale', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.timezone', label: 'Browser timezone', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.device_profile', label: 'Emulated device profile', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.touch_mode', label: 'Enable touch input emulation', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.orientation', label: 'Screen orientation', type: 'select', options: ['portrait', 'landscape'], protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.viewport.width', label: 'Viewport width', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.viewport.height', label: 'Viewport height', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.geolocation.latitude', label: 'Geolocation latitude', type: 'signed', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.geolocation.longitude', label: 'Geolocation longitude', type: 'signed', protocols: ['browser'] },
 { category: 'Browser', group: 'Target & identity', path: 'config.browser.geolocation.accuracy', label: 'Geolocation accuracy', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Transport & connections', path: 'config.browser.quic', label: 'Browser QUIC policy', type: 'select', options: ['auto', 'off'], protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.traceparent', label: 'Inject browser W3C traceparent', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.client_metrics', label: 'Collect web vitals client metrics', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.capture_document_metrics', label: 'Capture document navigation metrics', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.capture_interactions', label: 'Capture interaction evidence', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.experience_canary', label: 'Enable experience canary diagnostics', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.ept_calibration', label: 'Enable Salesforce EPT calibration', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.har_capture', label: 'HAR network capture mode', type: 'select', options: ['off', 'on_error', 'on_sla_breach', 'manual', 'always'], protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.har_capture_policy', label: 'HAR payload retention policy', type: 'select', options: ['headers_only', 'redacted', 'full'], protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.har_sla_threshold_ms', label: 'HAR SLA breach threshold (ms)', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.har_artifact_dir', label: 'HAR artifact directory', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Errors, logging & diagnostics', path: 'config.browser.capture_screenshots', label: 'Screenshot capture policy', type: 'select', options: ['off', 'on_error', 'always'], protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.cache_enabled', label: 'Enable browser cache', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.cache_content_urls', label: 'Cache HTML content URLs', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.check_newer_versions', label: 'Revalidate cached pages every visit', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.new_user_per_iteration', label: 'New browser context each iteration', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Protocol behavior & session', path: 'config.browser.download_resources', label: 'Sub-resource download policy', type: 'select', options: ['all', 'recorded', 'none'], protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.storage_state', label: 'Pre-authenticated storage state secret', type: 'text', credentialReference: true, protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.http_credentials.username', label: 'Browser HTTP auth username', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.http_credentials.password', label: 'Browser HTTP auth password', type: 'text', credentialReference: true, protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.http_credentials.origin', label: 'Browser HTTP auth origin', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.http_credentials.send', label: 'Browser HTTP auth send mode', type: 'select', options: ['unauthorized', 'always'], protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.proxy.server', label: 'Browser proxy server URL', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.proxy.bypass', label: 'Browser proxy bypass list', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.proxy.username', label: 'Browser proxy username', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.proxy.password', label: 'Browser proxy password', type: 'text', credentialReference: true, protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.review_policy.allow_reviewed_javascript', label: 'Allow reviewed custom JavaScript', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.review_policy.allow_review_required', label: 'Allow review-gated browser steps', type: 'boolean', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.strategy', label: 'Observer session sign-in strategy', type: 'select', options: ['storage_state_pool', 'salesforce_jwt', 'login_steps'], protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.preflight_url', label: 'Session preflight check URL', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Run logic & pacing', path: 'config.browser.session.start_gap_ms', label: 'Observer sign-in start gap (ms)', type: 'number', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.jwt.login_url', label: 'Salesforce JWT login URL', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.jwt.client_id', label: 'Salesforce connected app client ID', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.jwt.private_key', label: 'Salesforce JWT private key secret', type: 'text', credentialReference: true, protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.jwt.audience', label: 'Salesforce JWT audience URL', type: 'text', protocols: ['browser'] },
 { category: 'Browser', group: 'Security, proxy & auth', path: 'config.browser.session.jwt.start_url', label: 'Salesforce frontdoor landing path', type: 'text', protocols: ['browser'] },
]

// Settings DSL validation rejects whenever they hold a value
// (pkg/dsl/catalog_java_http_validation.go: "is not supported by embedded
// java_http"). The served catalog still lists them in import scope, so they
// stay clear-only after a merge: a stored value can be removed, not typed.
export const IMPORT_CLEAR_ONLY_PATHS: ReadonlySet<string> = new Set([
 'config.java_http.object_schema',
])

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
 for (const entry of PROTOCOL_FIELDS) {
  const normalized = withCanonicalMetadata(entry)
  if (!runtimeSettingEffects[normalized.path]) {
   runtimeSettingEffects[normalized.path] = `Configures ${normalized.label.toLowerCase()} (${normalized.path.replace(/^config\./, '')}) during replay.`
  }
  const existing = byPath.get(normalized.path)
  byPath.set(normalized.path, existing ? { ...existing, ...normalized } : normalized)
 }
 for (const path of IMPORT_CLEAR_ONLY_PATHS) {
  const field = byPath.get(path)
  if (field) byPath.set(path, { ...field, clearOnly: true })
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

// The editor renders its own Default (unset) entry, and the import POST accepts
// "" as unset, so a listed blank option would only render a second blank row.
const selectableOptions = (options?: string[]) => options?.filter(option => option.trim() !== '')

// Selects whose offline option list is deliberately narrower than the served
// catalog list. An imported value outside the list still shows via
// preservedOption and resubmits unchanged; it just cannot be newly chosen.
export const IMPORT_NARROWED_SELECT_PATHS: ReadonlySet<string> = new Set([
 'config.http.tls.min_version',
 'config.http.tls.max_version',
 'config.database.driver',
])

export function mergeImportRuntimeFields(
 remoteFields?: RemoteImportRuntimeField[] | null,
 baseFields: ImportRuntimeField[] = importRuntimeFields,
): ImportRuntimeField[] {
 if (!remoteFields?.length) return baseFields
 const byPath = new Map<string, ImportRuntimeField>(baseFields.map(field => [field.path, { ...field }]))
 for (const remote of remoteFields) {
  // A secret field stays editable: its imported value is script-sourced and
  // resubmits unchanged, and the import POST takes a new value only as a
  // Credentials reference ({{secret.*}}), so it is offered as one.
  if (!remote?.path) continue
  if (remote.type && ['key_value_map', 'object_list', 'object'].includes(remote.type)) continue
  const existing = byPath.get(remote.path)
  const remoteOptions = selectableOptions(remote.options)
  if (existing) {
   // The server validates a select against exactly the options it serves, so
   // they replace the offline list instead of widening it. TLS stays narrowed
   // to the versions the guided migration supports, and the database driver to
   // the drivers the runtime registers (pkg/protocol/database: pgx and sqlite).
   const keepRestrictedOptions = IMPORT_NARROWED_SELECT_PATHS.has(remote.path)
   const mergedOptions = keepRestrictedOptions || !remoteOptions?.length ? existing.options : remoteOptions
   const remoteGroup = remote.group || remote.group_id
   const resolvedGroup = remoteGroup ? resolveCanonicalRuntimeGroup(remoteGroup, remote.path) : existing.group
   byPath.set(remote.path, {
    ...existing,
    group: resolvedGroup,
    groupId: CANONICAL_RUNTIME_GROUP_IDS[resolvedGroup],
    options: mergedOptions,
    protocols: existing.protocols || remote.protocols,
    triState: existing.triState ?? remote.tri_state,
    credentialReference: existing.credentialReference || remote.secret || undefined,
    clearOnly: existing.clearOnly || IMPORT_CLEAR_ONLY_PATHS.has(remote.path) || undefined,
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
   options: remoteOptions,
   protocols: remote.protocols,
   triState: remote.tri_state,
   credentialReference: remote.secret || undefined,
   clearOnly: IMPORT_CLEAR_ONLY_PATHS.has(remote.path) || undefined,
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

const remoteFieldsResponse = (remote: unknown): RemoteImportRuntimeField[] | null => Array.isArray(remote)
 ? remote as RemoteImportRuntimeField[]
 : remote && typeof remote === 'object' && Array.isArray((remote as RemoteImportRuntimeFieldsEnvelope).fields)
   ? (remote as RemoteImportRuntimeFieldsEnvelope).fields!
   : null

const importFieldsEndpoint = (protocol?: string) => `/scripts/import/settings/fields${protocol ? `?protocol=${encodeURIComponent(protocol)}` : ''}`

export async function fetchImportRuntimeFields(
 fetcher: (endpoint: string) => Promise<RemoteImportRuntimeField[] | RemoteImportRuntimeFieldsEnvelope> = endpoint =>
  request<RemoteImportRuntimeField[] | RemoteImportRuntimeFieldsEnvelope>(endpoint),
): Promise<ImportRuntimeField[]> {
 try {
  const remoteFields = remoteFieldsResponse(await fetcher(importFieldsEndpoint()))
  if (remoteFields) {
   return registerRemoteImportRuntimeFields(remoteFields)
  }
 } catch {
  // Fall back to the static client-side catalog when offline or unmocked.
 }
 return getActiveImportRuntimeFields()
}

// One request per protocol per page load. A failed or malformed response
// resolves to null, and the parity-tested offline catalog stays in use.
const protocolFieldRequests = new Map<string, Promise<RemoteImportRuntimeField[] | null>>()

export function loadImportRuntimeFieldsForProtocol(
 protocol: string,
 fetcher: (endpoint: string) => Promise<unknown> = endpoint => request<unknown>(endpoint),
): Promise<RemoteImportRuntimeField[] | null> {
 const key = protocol.trim().toLowerCase()
 let pending = protocolFieldRequests.get(key)
 if (!pending) {
  pending = Promise.resolve()
   .then(() => fetcher(importFieldsEndpoint(key)))
   .then(remoteFieldsResponse, () => null)
  protocolFieldRequests.set(key, pending)
 }
 return pending
}

export function resetImportRuntimeFieldRequests(): void {
 protocolFieldRequests.clear()
}

/** Field metadata the server serves for this protocol, or undefined while loading or offline. */
export function useImportRuntimeFields(protocol?: string): RemoteImportRuntimeField[] | undefined {
 const [loaded, setLoaded] = useState<{ protocol: string; fields: RemoteImportRuntimeField[] } | null>(null)
 useEffect(() => {
  if (!protocol) return
  let active = true
  void loadImportRuntimeFieldsForProtocol(protocol).then(fields => {
   if (active && fields?.length) setLoaded({ protocol, fields })
  })
  return () => { active = false }
 }, [protocol])
 return loaded && loaded.protocol === protocol ? loaded.fields : undefined
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
 // Secret rows take only their own keys, never the parent proxy/auth
 // concept's whole key set. The enterprise-archive importer reads the proxy
 // password from ProxyPassword or Password in the proxy section, and the basic
 // auth password from [Authentication] Password; no RTS key carries a bearer token.
 'config.http.proxy.password': ['ProxyPassword', 'Password', 'ProxyPasswordIsEncrypted'],
 'config.http.auth.password': ['Password'],
 'config.http.auth.token': [],
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
  ...(PROTOCOL_CATEGORIES[script.protocol] ? [PROTOCOL_CATEGORIES[script.protocol]] : []),
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
 for (const rows of Object.values(groups)) for (const row of rows) {
  const override = row.path ? browserEmulationOverrideNote(script, row.path) : undefined
  if (override) row.note = `${row.note}\n${override}`
 }
 return groups
}

const formatOverrideValue = (value: unknown) => typeof value === 'boolean' ? (value ? 'Enabled' : 'Disabled') : String(value)

/**
 * The engine applies browser emulation after the HTTP settings
 * (pkg/engine/config_protocols.go appendBrowserEmulationHTTPConfig), so these
 * draft values replace config.http.* at replay. Say so on the overridden row.
 */
export function browserEmulationOverrideNote(script: ScriptDefinition, path: string): string | undefined {
 const emulation = script.config?.browser_emulation
 if (!emulation) return undefined
 if (path === 'config.http.cache_enabled' && emulation.cache?.simulate_cache != null) {
  return `Replay uses the browser emulation "Simulate browser cache" value (${formatOverrideValue(emulation.cache.simulate_cache)}) instead of this setting.`
 }
 const mode = String(emulation.mode || '').trim().toLowerCase()
 if (path === 'config.http.user_agent' && ((mode === 'custom' && String(emulation.custom_user_agent || '').trim()) || mode === 'specific')) {
  return mode === 'custom'
   ? 'Replay sends the browser emulation custom User-Agent instead of this header.'
   : 'Replay sends the User-Agent of the emulated browser instead of this header.'
 }
 if (path === 'config.http.accept_language' && String(emulation.language || '').trim()) {
  return `Replay sends the Accept-Language for the browser emulation language (${emulation.language}) instead of this header.`
 }
 return undefined
}

// The runtime category of each protocol family's own settings section.
const PROTOCOL_CATEGORIES: Record<string, string> = {
 kafka: 'Kafka',
 eventhubs: 'Event Hubs',
 grpc: 'gRPC',
 websocket: 'WebSocket',
 database: 'Database',
 network: 'Network',
 appium: 'Appium',
 java_http: 'Java HTTP',
 java_vuser: 'Java VUser',
 browser: 'Browser',
}

/** The runtime category a review of this protocol opens on when nothing needs a decision. */
export function preferredRuntimeCategory(protocol?: string): string {
 if (protocol?.startsWith('jms')) return 'JMS'
 return (protocol && protocol !== 'java_http' && PROTOCOL_CATEGORIES[protocol]) || 'HTTP'
}

// Protocol-specific sections an unconfigured setting is offered for. A
// configured value is always shown, whatever the protocol.
const PROTOCOL_SECTION_PREFIXES: Array<[string, (protocol: string) => boolean]> = [
 ['config.browser.', protocol => protocol === 'browser'],
 ['config.websocket.', protocol => protocol === 'websocket'],
 ['config.kafka.', protocol => protocol === 'kafka'],
 ['config.jms.', protocol => protocol.startsWith('jms')],
 ['config.eventhubs.', protocol => protocol === 'eventhubs'],
 ['config.grpc.', protocol => protocol === 'grpc'],
 ['config.database.', protocol => protocol === 'database'],
 ['config.network.', protocol => protocol === 'network'],
 ['config.appium.', protocol => protocol === 'appium'],
 ['config.java_http.', protocol => protocol === 'java_http'],
 ['config.java_vuser.', protocol => protocol === 'java_vuser'],
 ['config.observability.trace_carrier', protocol => protocol === 'kafka' || protocol === 'eventhubs' || protocol.startsWith('jms')],
]

export function settingIsRelevant(script: ScriptDefinition, path: string, isConfigured = false): boolean {
 if (path.startsWith('config.soap.') && script.protocol !== 'soap') return false
 const nativeReplayPolicies = ['http_errors_as_warnings', 'fail_noncritical_resources', 'resource_timeout_as_warning', 'legacy_html_checks', 'dns_cache', 'challenge_delay_ms']
 if (nativeReplayPolicies.some(key => path === `config.http.${key}`) && !['http', 'http/1.1', 'http/2', 'http/3', 'soap', 'java_http'].includes(script.protocol)) return false
 if (!isConfigured) {
  if ((path.startsWith('config.http.') || path.startsWith('config.browser_emulation.') || path.startsWith('config.web_runtime.')) && !HTTP_FAMILY_PROTOCOLS.includes(script.protocol)) return false
  const section = PROTOCOL_SECTION_PREFIXES.find(([prefix]) => path.startsWith(prefix))
  if (section && !section[1](script.protocol || '')) return false
 }
 // Runtime spellings: stored "TPS" is tps pacing, "Normal" is gaussian think time,
 // " Gamma " is the gamma arrival process (as Author and the engine read it).
 const think = canonicalThinkTimeType(script.config?.think_time?.type) || 'fixed'
 const pacing = canonicalPacingType(script.config?.pacing?.type)
 const pacingModes: Record<string, string[]> = { delay_ms: ['fixed_delay'], interval_ms: ['fixed_interval'], min_delay_ms: ['random_delay'], max_delay_ms: ['random_delay'], min_interval_ms: ['random_interval'], max_interval_ms: ['random_interval'], tps: ['tps'] }
 if (path.startsWith('config.pacing.quota.')) return isConfigured || pacing === 'quota'
 if (path === 'config.pacing.arrival_process') return isConfigured || pacing === 'tps'
 if (path === 'config.pacing.burstiness') return isConfigured || (pacing === 'tps' && String(readSetting(script, 'config.pacing.arrival_process') ?? '').trim().toLowerCase() === 'gamma')
 if (path.startsWith('config.pacing.')) return !pacingModes[path.split('.').pop()!] || pacingModes[path.split('.').pop()!].includes(pacing)
 if (path === 'config.think_time.min_percent' || path === 'config.think_time.max_percent') return think === 'random_percentage'
 if (path === 'config.think_time.mean_ms') return ['fixed', 'gaussian', 'normal', 'exponential'].includes(think)
 if (path === 'config.think_time.std_ms') return ['gaussian', 'normal'].includes(think)
 if (path === 'config.think_time.min_ms' || path === 'config.think_time.max_ms') return ['random', 'uniform', 'gaussian', 'normal'].includes(think)
 return true
}
