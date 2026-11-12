import type { ScriptConfig } from '../../types/scripts'
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
      'Base URL, protocol identity, native service bindings, script metadata, and read-only schedule compatibility notices.',
    defaultSectionId: 'script_config',
    sectionIds: ['script_config', 'imported_source'],
    configPaths: [
      'base_url',
      'native_services',
      'jmeter',
      'sampler_schedules',
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
      'VUsers iteration limits, termination mode, block scheduling, action sequences, and open or closed iteration pacing.',
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
      'HTTP version, QUIC transport, IP family, connection pooling, redirects, DNS cache, timeouts, and native protocol transport.',
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
      'http.quic.enable_0rtt',
      'http.quic.session_resumption',
      'http.quic.alt_svc_cache',
      'http.quic.migration_mode',
      'http.quic.migrate_after_requests',
      'http.quic.broken_ttl_ms',
      'http.ip_version',
      'http.connect_timeout_ms',
      'http.request_timeout_ms',
      'http.tls_timeout_ms',
      'http.idle_timeout_ms',
      'http.page_timeout_ms',
      'http.challenge_delay_ms',
      'http.max_conns_per_host',
      'http.max_idle_conns',
      'http.keep_alive',
      'http.max_redirects',
      'http.dns_cache',
      'http.receive_buffer_bytes',
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
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'security_proxy_auth',
    label: 'Security, proxy & auth',
    description:
      'TLS versions and certificates, single canonical HTTP proxy policy, HTTP authentication (including digest and NTLM), and SOAP WS-Security.',
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
      'soap.ws_security.timestamp',
      'soap.ws_security.timestamp.ttl_seconds',
    ],
    modes: ALL_EDITOR_MODES,
  },
  {
    id: 'protocol_behavior_session',
    label: 'Protocol behavior & session',
    description:
      'Request defaults, cookie and session cache policies, SOAP envelope and faults, browser emulation, and Web HTTP/HTML compatibility.',
    defaultSectionId: 'http',
    defaultHttpTransportTab: 'request_defaults',
    sectionIds: ['http', 'soap', 'browser_emulation', 'web_runtime'],
    httpTransportTabs: ['request_defaults', 'session_cache', 'browser'],
    configPaths: [
      'http.user_agent',
      'http.accept_encoding',
      'http.compression',
      'http.follow_redirects',
      'http.max_redirects',
      'http.max_meta_refresh',
      'http.default_headers',
      'http.default_header_list',
      'http.cookies',
      'http.cookies.enabled',
      'http.cookies.clear_per_iteration',
      'http.cookies.prepopulated',
      'http.cookies_enabled',
      'http.clear_cookies_per_iteration',
      'http.pinned_cookies',
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
      'browser',
      'browser_emulation',
      'web_runtime',
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

export function getNativeTransportSectionForProtocol(
  protocol?: string,
): RuntimeSettingsSectionId {
  const normalized = normalizeRuntimeProtocol(protocol)
  if (normalized.startsWith('jms_')) return 'jms'
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
      return 'http'
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
    case 'transport_connections':
      if (isHttpLikeRuntimeProtocol(normalized)) {
        return { section: 'http', httpTransportTab: 'connection' }
      }
      return { section: getNativeTransportSectionForProtocol(normalized) }
    case 'security_proxy_auth':
      return { section: 'http', httpTransportTab: 'tls' }
    case 'protocol_behavior_session':
      return { section: 'http', httpTransportTab: 'request_defaults' }
    case 'errors_logging_diagnostics':
      return { section: 'errors' }
  }
}

export function getCanonicalGroupForPath(path: string): CanonicalRuntimeGroupId {
  const normalized = path.trim().replace(/^config\./, '')
  if (
    normalized === 'base_url' ||
    normalized.startsWith('native_services') ||
    normalized.startsWith('jmeter') ||
    normalized.startsWith('sampler_schedules') ||
    normalized.startsWith('script.')
  ) {
    return 'target_identity'
  }
  if (normalized.startsWith('run_logic') || normalized.startsWith('pacing')) {
    return 'run_logic_pacing'
  }
  if (normalized.startsWith('think_time')) {
    return 'think_time'
  }
  if (
    normalized.startsWith('error_handling') ||
    normalized.startsWith('log') ||
    normalized.startsWith('results') ||
    normalized.startsWith('observability') ||
    normalized === 'web_runtime.content_checks' ||
    normalized === 'http.http_errors_as_warnings' ||
    normalized === 'http.fail_noncritical_resources' ||
    normalized === 'http.resource_timeout_as_warning' ||
    normalized === 'http.content_check_error_limit' ||
    normalized === 'http.traceparent' ||
    normalized === 'http.traceparent_sampled'
  ) {
    return 'errors_logging_diagnostics'
  }
  if (
    normalized.startsWith('http.tls') ||
    normalized === 'http.proxy_mode' ||
    normalized.startsWith('http.proxy') ||
    normalized.startsWith('http.auth') ||
    normalized.startsWith('soap.ws_security')
  ) {
    return 'security_proxy_auth'
  }
  if (
    normalized === 'http.user_agent' ||
    normalized === 'http.accept_encoding' ||
    normalized === 'http.compression' ||
    normalized === 'http.follow_redirects' ||
    normalized === 'http.max_meta_refresh' ||
    normalized.startsWith('http.default_headers') ||
    normalized.startsWith('http.default_header_list') ||
    normalized.startsWith('http.cookies') ||
    normalized === 'http.clear_cookies_per_iteration' ||
    normalized.startsWith('http.pinned_cookies') ||
    normalized.startsWith('http.cache_') ||
    normalized.startsWith('http.html_') ||
    normalized === 'http.legacy_html_checks' ||
    normalized === 'http.aura_recorded_failures' ||
    normalized.startsWith('soap') ||
    normalized.startsWith('browser') ||
    normalized.startsWith('web_runtime')
  ) {
    return 'protocol_behavior_session'
  }
  return 'transport_connections'
}

export function isTlsMaxVersionIncompatibleWithHttp3(
  httpVersion?: string,
  tlsMaxVersion?: string,
): boolean {
  const normalizedHttp = (httpVersion || '').trim().toLowerCase()
  const normalizedTlsMax = (tlsMaxVersion || '').trim()
  if (normalizedHttp !== '3' && normalizedHttp !== 'auto+h3') {
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
): string | null {
  if (!isTlsMaxVersionIncompatibleWithHttp3(httpVersion, tlsMaxVersion)) {
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
