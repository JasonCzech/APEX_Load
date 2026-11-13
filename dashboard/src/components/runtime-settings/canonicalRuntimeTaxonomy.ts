import type { ScriptConfig } from '../../types/scripts'
import { inheritedHTTPActionVersion } from '../httpVersionModel'
import {
  httpTransportTabIsAvailable,
  runtimeSettingsSectionIsAvailable,
} from '../scriptEditorRuntimeSettingsModel'
import type {
  HTTPTransportTabId,
  RuntimeSettingsSectionId,
} from '../scriptEditorRuntimeSettingsModel'

export type CanonicalRuntimeGroupId =
  | 'target_identity'
  | 'run_logic_pacing'
  | 'think_time'
  | 'transport_connections'
  | 'security_proxy_auth'
  | 'protocol_behavior_session'
  | 'errors_logging_diagnostics'

export type RuntimeSettingsEditorMode = 'author' | 'import' | 'debug' | 'scenario'

/** Where an effective runtime value comes from on the scenario and debug surfaces. */
export type RuntimeValueProvenance =
  | 'group_override'
  | 'scenario_default'
  | 'script'
  | 'engine_default'
  | 'session_override'

export const RUNTIME_VALUE_PROVENANCE_LABELS: Readonly<Record<RuntimeValueProvenance, string>> = {
  session_override: 'Session override',
  group_override: 'Group override',
  scenario_default: 'Scenario default',
  script: 'Script value',
  engine_default: 'Engine default',
}

/** Provenance kinds that replace the script's own value. */
export function isOverrideProvenance(provenance: RuntimeValueProvenance): boolean {
  return provenance === 'group_override' || provenance === 'scenario_default' || provenance === 'session_override'
}

/**
 * The slice of a GET /api/v1/dsl/config-catalog field this module consumes.
 * The backend catalog (pkg/dsl/config_catalog.go) is the source of truth for
 * path→group; CANONICAL_RUNTIME_GROUPS[].configPaths is the offline fallback
 * and is parity-tested against it.
 */
export interface CanonicalCatalogFieldRef {
  path: string
  group: string
  label?: string
}

export interface CanonicalRuntimeFieldMeta {
  path: string
  label: string
  groupId: CanonicalRuntimeGroupId
  sectionId: RuntimeSettingsSectionId
  httpTransportTab?: HTTPTransportTabId
  protocols?: readonly string[]
  modes: readonly RuntimeSettingsEditorMode[]
  readOnlyCompatibility?: boolean
}

export interface CanonicalRuntimeGroupDefinition {
  id: CanonicalRuntimeGroupId
  label: string
  description: string
  defaultSectionId: RuntimeSettingsSectionId
  defaultHttpTransportTab?: HTTPTransportTabId
  sectionIds: readonly RuntimeSettingsSectionId[]
  httpTransportTabs?: readonly HTTPTransportTabId[]
  configPaths: readonly string[]
  modes: readonly RuntimeSettingsEditorMode[]
}

const ALL_EDITOR_MODES: readonly RuntimeSettingsEditorMode[] = [
  'author',
  'import',
  'debug',
  'scenario',
]

const HTTP_LIKE_PROTOCOLS: readonly string[] = [
  'http',
  'http/1.1',
  'http/2',
  'http/3',
  'https',
  'soap',
  'browser',
  'java_http',
]

const HTTP_SCRIPT_PROTOCOLS: readonly string[] = [
  'http',
  'http/1.1',
  'http/2',
  'http/3',
  'https',
]

export const CANONICAL_RUNTIME_GROUP_IDS: readonly CanonicalRuntimeGroupId[] = [
  'target_identity',
  'run_logic_pacing',
  'think_time',
  'transport_connections',
  'security_proxy_auth',
  'protocol_behavior_session',
  'errors_logging_diagnostics',
]

export const CANONICAL_RUNTIME_GROUPS: readonly CanonicalRuntimeGroupDefinition[] = [
  {
    id: 'target_identity',
    label: 'Target & identity',
    description:
      'Base URL, protocol identity, user agent and language, browser emulation identity, and script metadata.',
    defaultSectionId: 'script_config',
    sectionIds: ['script_config', 'imported_source'],
    configPaths: [
      'base_url',
      'http.user_agent',
      'http.accept_language',
      'browser_emulation.mode',
      'browser_emulation.browser_type',
      'browser_emulation.browser_version',
      'browser_emulation.platform',
      'browser_emulation.language',
      'browser_emulation.custom_user_agent',
      'script.name',
      'script.version',
      'script.protocol',
      'script.metadata',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'run_logic_pacing',
    label: 'Run logic & pacing',
    description:
      'VUsers iteration limits, termination mode, block scheduling, action sequences, open or closed iteration pacing, and read-only sampler schedules.',
    defaultSectionId: 'runlogic',
    sectionIds: ['runlogic', 'pacing'],
    configPaths: [
      'run_logic.iterations',
      'run_logic.global_iterations',
      'run_logic.max_transactions',
      'run_logic.max_in_flight',
      'run_logic.workload_model',
      'run_logic.termination_mode',
      'run_logic.auto_transactions',
      'run_logic.block_scheduling',
      'run_logic.action_sequence',
      'run_logic.blocks',
      'pacing.type',
      'pacing.delay_ms',
      'pacing.min_delay_ms',
      'pacing.max_delay_ms',
      'pacing.interval_ms',
      'pacing.min_interval_ms',
      'pacing.max_interval_ms',
      'pacing.tps',
      'pacing.arrival_process',
      'pacing.burstiness',
      'pacing.seed',
      'pacing.rate_stages',
      'pacing.quota',
      'pacing.quota.tpm',
      'pacing.quota.rpm',
      'pacing.quota.estimated_tokens_per_request',
      'pacing.quota.burst_tokens',
      'pacing.quota.adaptive_aimd',
      'pacing.quota.default_tokens',
      'pacing.quota.headroom_ratio',
      'sampler_schedules',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'think_time',
    label: 'Think time',
    description:
      'Pause behavior between VUsers actions across fixed, random, Gaussian (normal), uniform, and exponential distributions.',
    defaultSectionId: 'think',
    sectionIds: ['think'],
    configPaths: [
      'think_time.type',
      'think_time.mean_ms',
      'think_time.std_ms',
      'think_time.min_ms',
      'think_time.max_ms',
      'think_time.min_percent',
      'think_time.max_percent',
      'think_time.multiplier',
      'think_time.limit_ms',
      'think_time.ignore',
      'think_time.force',
      'think_time.distribution',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'transport_connections',
    label: 'Transport & connections',
    description:
      'HTTP version, QUIC transport, IP family, connection pooling, DNS cache, timeouts, network simulation and source IPs, and native protocol transport.',
    defaultSectionId: 'http',
    defaultHttpTransportTab: 'connection',
    sectionIds: [
      'http',
      'jms',
      'eventhubs',
      'websocket',
      'grpc',
      'database',
      'network',
      'appium',
      'java_http',
      'java_vuser',
    ],
    httpTransportTabs: ['connection', 'timeouts'],
    configPaths: [
      'http.version',
      'http.quic',
      'http.quic.handshake_timeout_ms',
      'http.quic.max_idle_timeout_ms',
      'http.quic.keep_alive_ms',
      'http.quic.alt_svc_cache',
      'http.quic.broken_ttl_ms',
      'http.ip_version',
      'http.connect_timeout_ms',
      'http.request_timeout_ms',
      'http.tls_timeout_ms',
      'http.idle_timeout_ms',
      'http.page_timeout_ms',
      'http.max_conns_per_host',
      'http.max_idle_conns',
      'http.keep_alive',
      'http.dns_cache',
      'http.receive_buffer_bytes',
      'web_runtime.network_simulation',
      'web_runtime.source_ips',
      'web_runtime.network_profiles',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'security_proxy_auth',
    label: 'Security, proxy & auth',
    description:
      'TLS versions and certificates, single canonical HTTP proxy policy, HTTP authentication (including digest and NTLM) and challenge delay, and SOAP WS-Security.',
    defaultSectionId: 'http',
    defaultHttpTransportTab: 'tls',
    sectionIds: ['http', 'soap'],
    httpTransportTabs: ['tls', 'proxy', 'authentication'],
    configPaths: [
      'http.tls',
      'http.tls.insecure_skip_verify',
      'http.tls.min_version',
      'http.tls.max_version',
      'http.tls.ca_cert_file',
      'http.tls.client_cert_file',
      'http.tls.client_key_file',
      'http.challenge_delay_ms',
      'http.proxy_mode',
      'http.proxy',
      'http.proxy.url',
      'http.proxy.pac_url',
      'http.proxy.username',
      'http.proxy.password',
      'http.proxy.no_proxy',
      'http.auth',
      'http.auth.type',
      'http.auth.username',
      'http.auth.password',
      'http.auth.token',
      'http.auth.domain',
      'http.auth.hosts',
      'soap.ws_security',
      'soap.ws_security.username_token',
      'soap.ws_security.username_token.username',
      'soap.ws_security.username_token.password',
      'soap.ws_security.username_token.password_type',
      'soap.ws_security.username_token.nonce',
      'soap.ws_security.username_token.created',
      'soap.ws_security.timestamp',
      'soap.ws_security.timestamp.ttl_seconds',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'protocol_behavior_session',
    label: 'Protocol behavior & session',
    description:
      'Request defaults, redirects, cookie and session cache policies, SOAP envelope and faults, browser emulation cache, Web HTTP/HTML compatibility, native service bindings, and JMeter import context.',
    defaultSectionId: 'http',
    defaultHttpTransportTab: 'request_defaults',
    sectionIds: ['http', 'soap', 'browser_emulation', 'web_runtime'],
    httpTransportTabs: ['request_defaults', 'session_cache', 'browser'],
    configPaths: [
      'http.accept_encoding',
      'http.max_redirects',
      'http.max_meta_refresh',
      'http.default_headers',
      'http.default_header_list',
      'http.cookies_enabled',
      'http.clear_cookies_per_iteration',
      'http.cache_enabled',
      'http.cache_max_unused_iterations',
      'http.cache_excluded_mime_types',
      'http.html_parse_mode',
      'http.html_excluded_mime_types',
      'http.legacy_html_checks',
      'http.aura_recorded_failures',
      'soap.version',
      'soap.namespaces',
      'soap.fault_handling',
      'soap.fault_handling.fail_on_fault',
      'browser_emulation.cache',
      'web_runtime.additional_attributes',
      'web_runtime.download_filters',
      'web_runtime.streaming',
      'web_runtime.data_formatting',
      'native_services',
      'jmeter',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'errors_logging_diagnostics',
    label: 'Errors, logging & diagnostics',
    description:
      'Tri-state continue-on-error policy, HTTP retry rules, Retry-After handling, runtime log verbosity, evidence redaction, and result diagnostics.',
    defaultSectionId: 'errors',
    sectionIds: ['errors', 'log'],
    configPaths: [
      'error_handling.continue_on_error',
      'error_handling.max_retries',
      'error_handling.retry_backoff',
      'error_handling.retry_delay_ms',
      'error_handling.retry_on',
      'error_handling.honor_retry_after',
      'error_handling.retry_after_max_ms',
      'error_handling.snapshot_on_error',
      'error_handling.fail_transaction_on_error_message',
      'http.http_errors_as_warnings',
      'http.fail_noncritical_resources',
      'http.resource_timeout_as_warning',
      'http.content_check_error_limit',
      'http.traceparent',
      'http.traceparent_sampled',
      'web_runtime.content_checks',
      'log.level',
      'log.on_error_only',
      'log.extended_options',
      'log.error_buffer_bytes',
      'log.max_response_bytes',
      'log.redact_run_evidence',
      'results.message_detail',
      'results.property_analytics',
      'results.csv_exports',
      'results.protocol_mix',
      'results.protocol_mix.max_fallback_ratio',
      'results.protocol_mix.max_quic_failure_ratio',
      'results.protocol_mix.min_samples',
      'observability.external_metrics',
      'observability.trace_carrier',
    ],
    modes: ALL_EDITOR_MODES,
  },
]

export function normalizeRuntimeProtocol(protocol?: string): string {
  return (protocol || 'http').trim().toLowerCase() || 'http'
}

export function isHttpLikeRuntimeProtocol(protocol?: string): boolean {
  return HTTP_LIKE_PROTOCOLS.includes(normalizeRuntimeProtocol(protocol))
}

export function getCanonicalRuntimeGroup(
  groupId: CanonicalRuntimeGroupId,
): CanonicalRuntimeGroupDefinition {
  const found = CANONICAL_RUNTIME_GROUPS.find(group => group.id === groupId)
  return found ?? CANONICAL_RUNTIME_GROUPS[0]
}

export function isCanonicalGroupSupportedForProtocol(
  groupId: CanonicalRuntimeGroupId,
  protocol?: string,
  mode: RuntimeSettingsEditorMode = 'author',
): boolean {
  const group = getCanonicalRuntimeGroup(groupId)
  if (!group.modes.includes(mode)) {
    return false
  }
  const normalized = normalizeRuntimeProtocol(protocol)
  if (isHttpLikeRuntimeProtocol(normalized)) {
    return true
  }
  if (groupId === 'security_proxy_auth' || groupId === 'protocol_behavior_session') {
    return false
  }
  if (groupId === 'transport_connections') {
    // A protocol without its own runtime transport section (kafka today) has
    // nowhere for this pill to land, so the rail hides it.
    return getNativeTransportSectionForProtocol(normalized) !== null
  }
  return true
}

export function getCanonicalRuntimeGroupsForProtocol(
  protocol?: string,
  mode: RuntimeSettingsEditorMode = 'author',
): CanonicalRuntimeGroupDefinition[] {
  return CANONICAL_RUNTIME_GROUPS.filter(group =>
    isCanonicalGroupSupportedForProtocol(group.id, protocol, mode),
  )
}

export function getCanonicalGroupForSection(
  section: RuntimeSettingsSectionId,
  httpTransportTab?: HTTPTransportTabId,
): CanonicalRuntimeGroupId {
  switch (section) {
    case 'script_config':
    case 'imported_source':
      return 'target_identity'
    case 'runlogic':
    case 'pacing':
      return 'run_logic_pacing'
    case 'think':
      return 'think_time'
    case 'errors':
    case 'log':
      return 'errors_logging_diagnostics'
    // The SOAP section holds the envelope, namespaces and faults (protocol
    // behavior) and WS-Security (security). Arriving from the Security pill
    // keeps that pill active (see isLocationInCanonicalGroup).
    case 'browser_emulation':
    case 'web_runtime':
    case 'soap':
      return 'protocol_behavior_session'
    case 'jms':
    case 'eventhubs':
    case 'websocket':
    case 'grpc':
    case 'database':
    case 'network':
    case 'appium':
    case 'java_http':
    case 'java_vuser':
      return 'transport_connections'
    case 'http':
      switch (httpTransportTab) {
        case 'tls':
        case 'proxy':
        case 'authentication':
          return 'security_proxy_auth'
        case 'request_defaults':
        case 'session_cache':
        case 'browser':
          return 'protocol_behavior_session'
        case 'connection':
        case 'timeouts':
        default:
          return 'transport_connections'
      }
    default:
      return 'target_identity'
  }
}

/**
 * The runtime section that edits a non-HTTP protocol's own transport, or null
 * when the protocol has none. Mirrors getRuntimeProtocolCapabilities (JMS
 * protocols are hyphenated: jms-solace, jms-ibmmq, jms-servicebus); the
 * parity test checks every SCRIPT_PROTOCOL_OPTIONS value.
 */
export function getNativeTransportSectionForProtocol(
  protocol?: string,
): RuntimeSettingsSectionId | null {
  const normalized = normalizeRuntimeProtocol(protocol)
  if (normalized.startsWith('jms')) return 'jms'
  switch (normalized) {
    case 'eventhubs':
      return 'eventhubs'
    case 'websocket':
      return 'websocket'
    case 'grpc':
      return 'grpc'
    case 'database':
      return 'database'
    case 'network':
      return 'network'
    case 'appium':
      return 'appium'
    case 'java_http':
      return 'java_http'
    case 'java_vuser':
      return 'java_vuser'
    default:
      return null
  }
}

export function getCanonicalGroupDestination(
  groupId: CanonicalRuntimeGroupId,
  protocol?: string,
): { section: RuntimeSettingsSectionId; httpTransportTab?: HTTPTransportTabId } {
  const normalized = normalizeRuntimeProtocol(protocol)
  switch (groupId) {
    case 'target_identity':
      return { section: 'script_config' }
    case 'run_logic_pacing':
      return { section: 'runlogic' }
    case 'think_time':
      return { section: 'think' }
    case 'transport_connections': {
      if (isHttpLikeRuntimeProtocol(normalized)) {
        return { section: 'http', httpTransportTab: 'connection' }
      }
      const nativeSection = getNativeTransportSectionForProtocol(normalized)
      return nativeSection ? { section: nativeSection } : { section: 'script_config' }
    }
    case 'security_proxy_auth':
      // WS-Security exists only in the SOAP section; HTTP TLS, proxy and auth
      // stay one tab away in the HTTP section.
      if (normalized === 'soap') {
        return { section: 'soap' }
      }
      return { section: 'http', httpTransportTab: 'tls' }
    case 'protocol_behavior_session':
      return { section: 'http', httpTransportTab: 'request_defaults' }
    case 'errors_logging_diagnostics':
      return { section: 'errors' }
  }
}

/**
 * Whether a section (and HTTP transport tab) belongs to a canonical group.
 * A section shared by several groups (soap) belongs to each of them.
 */
export function isLocationInCanonicalGroup(
  groupId: CanonicalRuntimeGroupId,
  section: RuntimeSettingsSectionId,
  httpTransportTab?: HTTPTransportTabId,
): boolean {
  const group = getCanonicalRuntimeGroup(groupId)
  if (!group.sectionIds.includes(section)) {
    return false
  }
  if (section !== 'http') {
    return true
  }
  return getCanonicalGroupForSection('http', httpTransportTab) === groupId
}

const CANONICAL_GROUP_ID_SET: ReadonlySet<string> = new Set(CANONICAL_RUNTIME_GROUP_IDS)

function isCanonicalRuntimeGroupId(value: string): value is CanonicalRuntimeGroupId {
  return CANONICAL_GROUP_ID_SET.has(value)
}

function normalizeConfigPath(path: string): string {
  return path.trim().replace(/^config\./, '')
}

/** Longest entry equal to the path or a dot-separated ancestor of it. */
function longestSegmentPrefix<T>(
  path: string,
  entries: Iterable<[string, T]>,
): T | null {
  let best: { length: number; value: T } | null = null
  for (const [prefix, value] of entries) {
    if ((path === prefix || path.startsWith(`${prefix}.`)) && (!best || prefix.length > best.length)) {
      best = { length: prefix.length, value }
    }
  }
  return best ? best.value : null
}

function offlineConfigPathEntries(): Array<[string, CanonicalRuntimeGroupId]> {
  return CANONICAL_RUNTIME_GROUPS.flatMap(group =>
    group.configPaths.map(path => [path, group.id] as [string, CanonicalRuntimeGroupId]),
  )
}

/**
 * Resolve the canonical group for a config path. Backend catalog fields win
 * when given; otherwise the offline configPaths decide. Matching is by whole
 * dot segments, so http.tls never captures http.tls_timeout_ms. Returns null
 * for paths the offline taxonomy does not cover (native protocol, config.browser
 * and Bruno blocks belong to the catalog) rather than guessing a group.
 */
export function getCanonicalGroupForPath(
  path: string,
  catalogFields?: readonly CanonicalCatalogFieldRef[],
): CanonicalRuntimeGroupId | null {
  const normalized = normalizeConfigPath(path)
  if (catalogFields && catalogFields.length > 0) {
    const fromCatalog = longestSegmentPrefix(
      normalized,
      catalogFields
        .filter(field => isCanonicalRuntimeGroupId(field.group))
        .map(field => [normalizeConfigPath(field.path), field.group as CanonicalRuntimeGroupId] as [string, CanonicalRuntimeGroupId]),
    )
    if (fromCatalog) {
      return fromCatalog
    }
  }
  return longestSegmentPrefix(normalized, offlineConfigPathEntries())
}

/** The runtime section (and HTTP transport tab) whose panel renders a setting. */
export interface RuntimeFieldLocation {
  section: RuntimeSettingsSectionId
  httpTransportTab?: HTTPTransportTabId
}

/**
 * Where ScriptRuntimeSettingsView renders each config path, matched by whole
 * dot segments (longest prefix wins). A field's canonical group follows the
 * Go catalog, while its panel follows the existing section layout, so the two
 * can differ (http.max_redirects is protocol behavior but sits on the
 * Connection tab). Search hits and override badges route through this map so
 * a pill opens the panel that actually holds the matched field. Paths with no
 * panel (results.csv_exports, observability) fall back to the group default.
 */
const RUNTIME_FIELD_LOCATIONS: ReadonlyArray<[string, RuntimeFieldLocation]> = [
  ['base_url', { section: 'script_config' }],
  ['script', { section: 'script_config' }],
  ['native_services', { section: 'script_config' }],
  ['jmeter', { section: 'script_config' }],
  ['sampler_schedules', { section: 'script_config' }],
  ['run_logic', { section: 'runlogic' }],
  ['pacing', { section: 'pacing' }],
  ['think_time', { section: 'think' }],
  ['error_handling', { section: 'errors' }],
  ['results.message_detail', { section: 'errors' }],
  ['results.property_analytics', { section: 'errors' }],
  ['log', { section: 'log' }],
  ['soap', { section: 'soap' }],
  ['browser_emulation', { section: 'browser_emulation' }],
  ['web_runtime', { section: 'web_runtime' }],
  ['browser', { section: 'http', httpTransportTab: 'browser' }],
  // HTTP transport tabs (HttpRequestDefaultsEditor, HTTPReplayPolicyFields).
  ['http.user_agent', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.accept_encoding', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.accept_language', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.traceparent', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.traceparent_sampled', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.default_headers', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.default_header_list', { section: 'http', httpTransportTab: 'request_defaults' }],
  ['http.cookies_enabled', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.clear_cookies_per_iteration', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.cache_enabled', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.cache_max_unused_iterations', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.cache_excluded_mime_types', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.html_parse_mode', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.html_excluded_mime_types', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.legacy_html_checks', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.aura_recorded_failures', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.max_meta_refresh', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.receive_buffer_bytes', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.dns_cache', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.http_errors_as_warnings', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.fail_noncritical_resources', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.resource_timeout_as_warning', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.content_check_error_limit', { section: 'http', httpTransportTab: 'session_cache' }],
  ['http.version', { section: 'http', httpTransportTab: 'connection' }],
  ['http.quic', { section: 'http', httpTransportTab: 'connection' }],
  ['http.ip_version', { section: 'http', httpTransportTab: 'connection' }],
  ['http.max_conns_per_host', { section: 'http', httpTransportTab: 'connection' }],
  ['http.max_idle_conns', { section: 'http', httpTransportTab: 'connection' }],
  ['http.max_redirects', { section: 'http', httpTransportTab: 'connection' }],
  ['http.keep_alive', { section: 'http', httpTransportTab: 'connection' }],
  ['http.connect_timeout_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.request_timeout_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.tls_timeout_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.idle_timeout_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.page_timeout_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.challenge_delay_ms', { section: 'http', httpTransportTab: 'timeouts' }],
  ['http.tls', { section: 'http', httpTransportTab: 'tls' }],
  ['http.proxy_mode', { section: 'http', httpTransportTab: 'proxy' }],
  ['http.proxy', { section: 'http', httpTransportTab: 'proxy' }],
  ['http.auth', { section: 'http', httpTransportTab: 'authentication' }],
]

function isRuntimeFieldLocationAvailable(location: RuntimeFieldLocation, protocol: string): boolean {
  if (!runtimeSettingsSectionIsAvailable(location.section, protocol)) {
    return false
  }
  return !location.httpTransportTab || httpTransportTabIsAvailable(location.httpTransportTab, protocol)
}

/**
 * The panel that renders a config path for this protocol, or null when the
 * path has no panel or its section is hidden for the protocol (browser
 * emulation on a browser script, for example).
 */
export function getRuntimeFieldLocation(
  path: string,
  protocol?: string,
): RuntimeFieldLocation | null {
  const location = longestSegmentPrefix(normalizeConfigPath(path), RUNTIME_FIELD_LOCATIONS)
  if (!location || !isRuntimeFieldLocationAvailable(location, normalizeRuntimeProtocol(protocol))) {
    return null
  }
  return location
}

function sameRuntimeFieldLocation(a: RuntimeFieldLocation, b: RuntimeFieldLocation): boolean {
  return a.section === b.section && (a.section !== 'http' || a.httpTransportTab === b.httpTransportTab)
}

/**
 * Where a pill opens when it stands for specific fields (search hits or
 * overridden paths). The group default wins when it already renders one of
 * the fields; otherwise the first field with a panel decides. With no fields,
 * or none with a panel, this is getCanonicalGroupDestination.
 */
export function getCanonicalGroupDestinationForPaths(
  groupId: CanonicalRuntimeGroupId,
  protocol: string | undefined,
  paths: readonly string[],
): RuntimeFieldLocation {
  const fallback = getCanonicalGroupDestination(groupId, protocol)
  const locations = paths
    .map(path => getRuntimeFieldLocation(path, protocol))
    .filter((location): location is RuntimeFieldLocation => location !== null)
  if (locations.length === 0 || locations.some(location => sameRuntimeFieldLocation(location, fallback))) {
    return fallback
  }
  return locations[0]
}

/** Plan 6.2 names for the shared primitives. */
export const getCanonicalGroupsForProtocol = getCanonicalRuntimeGroupsForProtocol
export const mapLegacySectionToCanonicalGroup = getCanonicalGroupForSection

/**
 * True when TLS max version is below 1.3 while the script's effective HTTP
 * policy uses HTTP/3. An empty httpVersion inherits the protocol alias default
 * (script.protocol http/3 means HTTP/3, as dsl.InheritedHTTPVersion does), so
 * callers pass script.protocol to catch the unset-version case.
 */
export function isTlsMaxVersionIncompatibleWithHttp3(
  httpVersion?: string,
  tlsMaxVersion?: string,
  protocol?: string,
): boolean {
  const normalizedTlsMax = (tlsMaxVersion || '').trim()
  const effective = inheritedHTTPActionVersion(
    normalizeRuntimeProtocol(protocol),
    httpVersion === undefined || httpVersion.trim() === '' ? undefined : httpVersion,
  )
  if (effective !== '3' && effective !== 'auto+h3') {
    return false
  }
  return (
    normalizedTlsMax === '1.0' ||
    normalizedTlsMax === '1.1' ||
    normalizedTlsMax === '1.2'
  )
}

export function getTlsMaxVersionHttp3Warning(
  httpVersion?: string,
  tlsMaxVersion?: string,
  protocol?: string,
): string | null {
  if (!isTlsMaxVersionIncompatibleWithHttp3(httpVersion, tlsMaxVersion, protocol)) {
    return null
  }
  return `HTTP/3 (QUIC) requires TLS 1.3; setting TLS max version to ${tlsMaxVersion} (< 1.3) will prevent HTTP/3 handshakes.`
}

export function hasIgnoredSoapConfigOnHttpScript(
  protocol?: string,
  config?: ScriptConfig,
): boolean {
  if (!HTTP_SCRIPT_PROTOCOLS.includes(normalizeRuntimeProtocol(protocol))) {
    return false
  }
  const soap = config?.soap
  if (!soap) {
    return false
  }
  const hasNamespaces = Boolean(soap.namespaces && Object.keys(soap.namespaces).length > 0)
  const hasWsSecurity = Boolean(
    soap.ws_security &&
      (soap.ws_security.username_token || soap.ws_security.timestamp),
  )
  const hasFaultHandling = Boolean(
    soap.fault_handling && soap.fault_handling.fail_on_fault !== undefined,
  )
  return Boolean(soap.version || hasNamespaces || hasWsSecurity || hasFaultHandling)
}

export function hasReadOnlyScheduleCompatibility(config?: ScriptConfig): boolean {
  if (!config) {
    return false
  }
  const hasJmeter = Boolean(
    config.jmeter && Object.keys(config.jmeter).length > 0,
  )
  const hasSchedules = Boolean(
    config.sampler_schedules && config.sampler_schedules.length > 0,
  )
  return hasJmeter || hasSchedules
}
