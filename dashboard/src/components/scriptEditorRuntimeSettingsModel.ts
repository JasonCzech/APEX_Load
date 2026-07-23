import type {
  RuntimeSettingsFinding,
  RuntimeSettingsOverride,
  RuntimeSettingsReview,
} from '../types/runtimeSettings'
import type {
  BrowserEmulationCatalog,
  BrowserEmulationConfig,
  ContentCheckConfig,
  DFETransform,
  ScriptConfig,
  ScriptDefinition,
} from '../types/scripts'
import { jmsConfigForProtocol, jmsProviderForProtocol } from './scriptEditorHelpers'
import {
  EMPTY_ACTION,
  EMPTY_BROWSER_ACTION,
  EMPTY_DATABASE_ACTION,
  EMPTY_GRPC_ACTION,
  EMPTY_JMS_ACTION,
  EMPTY_NETWORK_ACTION,
  EMPTY_SOAP_ACTION,
  EMPTY_WEBSOCKET_ACTION,
} from './scriptEditorUtils'

export const SCRIPT_PROTOCOL_OPTIONS = [
  { value: 'http', label: 'HTTP API' },
  { value: 'https', label: 'HTTPS API' },
  { value: 'java_http', label: 'Java over HTTP' },
  { value: 'soap', label: 'Web Services (SOAP)' },
  { value: 'websocket', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'database', label: 'Database' },
  { value: 'network', label: 'Network' },
  { value: 'browser', label: 'Web/HTML Browser' },
  { value: 'jms-solace', label: 'JMS Solace' },
  { value: 'jms-servicebus', label: 'JMS Azure Service Bus' },
  { value: 'jms-ibmmq', label: 'JMS IBM MQ' },
  { value: 'kafka', label: 'Kafka' },
] as const

export const RUNTIME_SETTINGS_SECTION_IDS = [
  'script_config',
  'think',
  'pacing',
  'runlogic',
  'errors',
  'jms',
  'websocket',
  'grpc',
  'database',
  'network',
  'http',
  'browser_emulation',
  'soap',
  'java_http',
  'java_vuser',
  'web_runtime',
  'log',
] as const

export type RuntimeSettingsSectionId = typeof RUNTIME_SETTINGS_SECTION_IDS[number]

export const HTTP_TRANSPORT_TAB_OPTIONS = [
  { id: 'timeouts', label: 'Timeouts' },
  { id: 'connection', label: 'Connection' },
  { id: 'tls', label: 'TLS / SSL' },
  { id: 'proxy', label: 'Proxy' },
  { id: 'authentication', label: 'Authentication' },
  { id: 'browser', label: 'Browser Emulation' },
] as const

export type HTTPTransportTabId = typeof HTTP_TRANSPORT_TAB_OPTIONS[number]['id']

export interface RuntimeProtocolCapabilities {
  browser: boolean
  browserEmulation: boolean
  database: boolean
  grpc: boolean
  http: boolean
  httpTransport: boolean
  javaHttp: boolean
  javaVuser: boolean
  jms: boolean
  network: boolean
  requiresBaseURL: boolean
  soap: boolean
  webSocket: boolean
}

export interface ScriptRuntimeSummary {
  httpProxyMode: string
  httpProxySummary: string
  logLevelLabel: string
  runLogicLabel: string
  thinkTimeLabel: string
}

export interface RuntimeReviewSummary {
  blocking: RuntimeSettingsFinding[]
  findings: RuntimeSettingsFinding[]
  status: string
  summary: string
  warnings: string[]
}

export function getRuntimeProtocolCapabilities(protocol: string): RuntimeProtocolCapabilities {
  const normalized = protocol.trim().toLowerCase()
  const browser = normalized === 'browser'
  const http = normalized === 'http'
  const javaHttp = normalized === 'java_http'
  const javaVuser = normalized === 'java_vuser'
  const soap = normalized === 'soap'
  const database = normalized === 'database'
  const jms = normalized.startsWith('jms')
  const network = normalized === 'network'
  const httpTransport = http || normalized === 'https' || javaHttp || soap || browser
  return {
    browser,
    // Browser emulation drives the HTTP replay engine; the browser protocol's
    // emulation home stays the browser sub-tab (config.browser).
    browserEmulation: httpTransport && !browser,
    database,
    grpc: normalized === 'grpc',
    http,
    httpTransport,
    javaHttp,
    javaVuser,
    jms,
    network,
    requiresBaseURL: !jms && !database && !network && !javaVuser,
    soap,
    webSocket: normalized === 'websocket',
  }
}

export function runtimeSettingsSectionIsAvailable(section: RuntimeSettingsSectionId, protocol: string): boolean {
  const capability = getRuntimeProtocolCapabilities(protocol)
  switch (section) {
    case 'jms': return capability.jms
    case 'websocket': return capability.webSocket
    case 'grpc': return capability.grpc
    case 'database': return capability.database
    case 'network': return capability.network
    case 'http': return capability.httpTransport
    case 'browser_emulation': return capability.browserEmulation
    case 'soap': return capability.soap
    case 'java_http': return capability.javaHttp
    case 'java_vuser': return capability.javaVuser
    case 'web_runtime': return capability.http
    default: return true
  }
}

// FALLBACK_BROWSER_EMULATION_CATALOG seeds the Browser Emulation dropdowns
// until GET /api/v1/dsl/browser-emulation-catalog resolves (and in tests). It
// mirrors the controller catalog in pkg/dsl/catalog_browser_emulation.go.
export const FALLBACK_BROWSER_EMULATION_CATALOG: BrowserEmulationCatalog = {
  browsers: [
    { type: 'chrome', label: 'Google Chrome', versions: ['126', '120', '109'], platforms: ['windows', 'macos', 'linux', 'android'] },
    { type: 'edge', label: 'Microsoft Edge', versions: ['126', '120'], platforms: ['windows', 'macos'] },
    { type: 'firefox', label: 'Mozilla Firefox', versions: ['127', '115'], platforms: ['windows', 'macos', 'linux'] },
    { type: 'safari', label: 'Apple Safari', versions: ['17', '16'], platforms: ['macos', 'ios'] },
    { type: 'ie', label: 'Microsoft Internet Explorer', versions: ['11', '10'], platforms: ['windows'] },
  ],
  platforms: [
    { id: 'windows', label: 'Windows' },
    { id: 'macos', label: 'macOS' },
    { id: 'linux', label: 'Linux' },
    { id: 'android', label: 'Android' },
    { id: 'ios', label: 'iOS' },
  ],
  languages: [
    { tag: 'en-US', label: 'English (United States)' },
    { tag: 'en-GB', label: 'English (United Kingdom)' },
    { tag: 'de-DE', label: 'German (Germany)' },
    { tag: 'fr-FR', label: 'French (France)' },
    { tag: 'es-ES', label: 'Spanish (Spain)' },
    { tag: 'pt-BR', label: 'Portuguese (Brazil)' },
    { tag: 'it-IT', label: 'Italian (Italy)' },
    { tag: 'nl-NL', label: 'Dutch (Netherlands)' },
    { tag: 'ja-JP', label: 'Japanese (Japan)' },
    { tag: 'zh-CN', label: 'Chinese (Simplified, China)' },
    { tag: 'ko-KR', label: 'Korean (Korea)' },
    { tag: 'hi-IN', label: 'Hindi (India)' },
  ],
}

// httpTransportTabIsAvailable gates the HTTP Transport sub-tabs per protocol.
// The 'browser' sub-tab edits config.browser.* (Playwright runtime) and is
// shown only for browser-protocol scripts; HTTP-transport scripts use the
// top-level Browser Emulation section instead so no field has two editors.
export function httpTransportTabIsAvailable(tab: HTTPTransportTabId, protocol: string): boolean {
  if (tab === 'browser') return getRuntimeProtocolCapabilities(protocol).browser
  return true
}

export function formatBrowserEmulationSummary(emulation?: BrowserEmulationConfig | null): string {
  if (!emulation || !emulation.mode) return 'Engine default'
  if (emulation.mode === 'custom') return 'Custom UA'
  const parts = [emulation.browser_type || 'browser']
  if (emulation.browser_version) parts.push(emulation.browser_version)
  const label = parts.join(' ')
  return emulation.platform ? `${label} · ${emulation.platform}` : label
}

export function formatScriptProtocolLabel(protocol: string): string {
  if (protocol.trim().toLowerCase() === 'java_vuser') return 'Java Vuser'
  return SCRIPT_PROTOCOL_OPTIONS.find(option => option.value === protocol)?.label || protocol.toUpperCase()
}

export function formatMillisecondsAsSecondsValue(
  valueMs: number | null | undefined,
  fallbackMs = 0,
  emptyWhenUndefined = false,
): string {
  if ((valueMs === undefined || valueMs === null) && emptyWhenUndefined) return ''
  const safeMs = valueMs ?? fallbackMs
  if (!Number.isFinite(safeMs)) return '0'
  const roundedSeconds = Math.round((safeMs / 1000) * 1000) / 1000
  return roundedSeconds.toFixed(3).replace(/\.?0+$/, '')
}

export function formatMillisecondsAsSecondsLabel(valueMs: number | null | undefined, fallbackMs = 0): string {
  return `${formatMillisecondsAsSecondsValue(valueMs, fallbackMs)}s`
}

export function parseSecondsInputAsMilliseconds(value: string, minSeconds = 0): number | null | undefined {
  if (value.trim() === '') return undefined
  const seconds = Number(value)
  if (!Number.isFinite(seconds)) return null
  return Math.round(Math.max(minSeconds, seconds) * 1000)
}

export function buildScriptRuntimeSummary(script: Pick<ScriptDefinition, 'config'>): ScriptRuntimeSummary {
  const httpProxyMode = script.config.http?.proxy_mode || 'none'
  const httpProxySummary = httpProxyMode === 'system'
    ? 'System proxy'
    : httpProxyMode === 'custom' && script.config.http?.proxy?.url
      ? 'Proxy configured'
      : 'Default'
  const thinkTimeLabel = script.config.think_time?.ignore
    ? 'Disabled'
    : `${script.config.think_time?.type || 'fixed'} / ${formatMillisecondsAsSecondsLabel(script.config.think_time?.mean_ms)}`
  const runLogic = script.config.run_logic
  const runLogicLabel = runLogic?.global_iterations !== undefined
    ? `${runLogic.global_iterations} shared iterations`
    : runLogic?.max_transactions !== undefined
      ? `${runLogic.max_transactions} max transactions`
      : runLogic?.iterations !== undefined
        ? `${runLogic.iterations} iterations`
        : 'Run until duration'
  return {
    httpProxyMode,
    httpProxySummary,
    logLevelLabel: script.config.log?.level || 'standard',
    runLogicLabel,
    thinkTimeLabel,
  }
}

export function formatKeyValueLines(values?: Record<string, string>): string {
  return Object.entries(values || {}).map(([key, value]) => `${key}=${value}`).join('\n')
}

export function parseKeyValueLines(value: string): Record<string, string> {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .reduce<Record<string, string>>((acc, line) => {
      const separator = line.indexOf('=')
      if (separator === -1) {
        acc[line] = ''
        return acc
      }
      const key = line.slice(0, separator).trim()
      const itemValue = line.slice(separator + 1).trim()
      if (key) acc[key] = itemValue
      return acc
    }, {})
}

export function formatLineList(values?: string[]): string {
  return (values || []).join('\n')
}

export function parseLineList(value: string): string[] {
  return value.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}

// Java VM arguments are already exact argv tokens. Preserve every byte within
// a non-empty line; trimming here would silently change a property value.
export function parseExactLineList(value: string): string[] {
  return value.split(/\r?\n/).filter(line => line.length > 0)
}

export function formatContentChecks(value?: ContentCheckConfig[]): string {
  return (value || [])
    .map(check => `${check.text || ''}|${check.scope || 'any_response'}|${check.fail_if_found === false ? 'false' : 'true'}`)
    .join('\n')
}

export function parseContentChecks(value: string): ContentCheckConfig[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [text, scope, failIfFound] = line.split('|').map(part => part.trim())
      return {
        text,
        scope: scope || 'any_response',
        fail_if_found: failIfFound ? failIfFound !== 'false' : true,
      }
    })
}

export function formatDFEChain(value?: DFETransform[]): string {
  return (value || [])
    .map((transform) => {
      const type = transform.type?.trim()
      if (!type) return ''
      const options = Object.entries(transform.options || {})
        .filter(([key, optionValue]) => key.trim() && optionValue)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, optionValue]) => `${key}=${optionValue}`)
      return [type, ...options].join(' ')
    })
    .filter(Boolean)
    .join('\n')
}

export function parseDFEChain(value: string): DFETransform[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [type, ...optionTokens] = line.split(/\s+/)
      const options = optionTokens.reduce<Record<string, string>>((acc, token) => {
        const separator = token.indexOf('=')
        if (separator <= 0 || separator === token.length - 1) return acc
        const key = token.slice(0, separator).trim()
        const optionValue = token.slice(separator + 1).trim()
        if (key && optionValue) acc[key] = optionValue
        return acc
      }, {})
      return Object.keys(options).length > 0 ? { type, options } : { type }
    })
}

export const SOAP11_ENVELOPE_NAMESPACE = 'http://schemas.xmlsoap.org/soap/envelope/'
export const SOAP12_ENVELOPE_NAMESPACE = 'http://www.w3.org/2003/05/soap-envelope'

export function defaultSOAPEnvelopeNamespace(version?: string): string {
  return version === '1.2' ? SOAP12_ENVELOPE_NAMESPACE : SOAP11_ENVELOPE_NAMESPACE
}

export function syncSOAPEnvelopeNamespace(
  version: string,
  namespaces?: Record<string, string>,
  previousVersion?: string,
): Record<string, string> {
  const nextNamespaces = { ...(namespaces || {}) }
  const currentEnvelope = nextNamespaces.soapenv?.trim()
  const previousEnvelope = defaultSOAPEnvelopeNamespace(previousVersion)
  if (!currentEnvelope || currentEnvelope === previousEnvelope) {
    nextNamespaces.soapenv = defaultSOAPEnvelopeNamespace(version)
  }
  return nextNamespaces
}

export function getImportedRuntimeSectionId(area?: string | null): RuntimeSettingsSectionId | null {
  switch (area?.trim().toLowerCase()) {
    case 'think time': return 'think'
    case 'pacing': return 'pacing'
    case 'run logic': return 'runlogic'
    case 'error handling': return 'errors'
    case 'logging': return 'log'
    case 'http': return 'http'
    case 'browser emulation': return 'browser_emulation'
    case 'web http/html':
    case 'web runtime': return 'web_runtime'
    case 'soap':
    case 'soap settings': return 'soap'
    case 'java http':
    case 'java over http': return 'java_http'
    case 'java vm':
    case 'java environment':
    case 'java environment settings': return 'java_vuser'
    case 'jms': return 'jms'
    case 'websocket': return 'websocket'
    case 'grpc': return 'grpc'
    case 'database':
    case 'database runtime': return 'database'
    case 'network':
    case 'network runtime': return 'network'
    default: return null
  }
}

export function formatRuntimeSettingStatusLabel(status: string): string {
  switch (status) {
    case 'executed': return 'Executed'
    case 'partial': return 'Partial'
    case 'compatibility_only': return 'Compatibility-only'
    case 'blocked': return 'Blocked'
    case 'non_applicable': return 'Non-applicable'
    default: return status.replace(/_/g, ' ')
  }
}

export function summarizeRuntimeSettingsReview(review?: RuntimeSettingsReview | null): RuntimeReviewSummary {
  const findings = review?.findings ? review.findings.map(finding => ({ ...finding })) : []
  const blocking = review?.blocking_findings
    ? review.blocking_findings.map(finding => ({ ...finding }))
    : findings.filter(finding => finding.severity === 'error' || finding.status === 'blocked')
  return {
    blocking,
    findings,
    status: review?.status || (blocking.length > 0 ? 'blocked' : findings.length > 0 ? 'review_required' : 'ready'),
    summary: review?.summary || (blocking.length > 0
      ? `${blocking.length} blocking runtime finding${blocking.length === 1 ? '' : 's'}`
      : findings.length > 0
        ? `${findings.length} runtime finding${findings.length === 1 ? '' : 's'} to review`
        : 'Runtime settings are ready'),
    warnings: [...(review?.warnings || [])],
  }
}

export function runtimeOverrideFromScriptConfig(config: ScriptConfig): RuntimeSettingsOverride {
  return {
    iterations: config.run_logic?.iterations,
    pacing: config.pacing ? structuredClone(config.pacing) : undefined,
    think_time: config.think_time ? structuredClone(config.think_time) : undefined,
    browser: config.browser
      ? {
          headless: config.browser.headless,
          device_profile: config.browser.device_profile,
          touch_mode: config.browser.touch_mode,
          orientation: config.browser.orientation,
        }
      : undefined,
    log_level: config.log?.level,
    auto_transactions: config.run_logic?.auto_transactions,
    error_handling: config.error_handling ? structuredClone(config.error_handling) : undefined,
    log: config.log ? structuredClone(config.log) : undefined,
    run_logic: config.run_logic ? structuredClone(config.run_logic) : undefined,
    http: config.http ? structuredClone(config.http) : undefined,
    web_runtime: config.web_runtime ? structuredClone(config.web_runtime) : undefined,
    browser_config: config.browser ? structuredClone(config.browser) : undefined,
    browser_emulation: config.browser_emulation ? structuredClone(config.browser_emulation) : undefined,
    base_url: config.base_url,
  }
}

export function cloneScriptRuntimeConfig(config: ScriptConfig): ScriptConfig {
  return structuredClone(config)
}

export function resetScriptRuntimeConfig(baseline: ScriptConfig): ScriptConfig {
  return cloneScriptRuntimeConfig(baseline)
}

export function scriptRuntimeConfigIsDirty(current: ScriptConfig, baseline: ScriptConfig): boolean {
  return JSON.stringify(current) !== JSON.stringify(baseline)
}

export interface ScriptProtocolTransition {
  script: ScriptDefinition
  selectedFirstAction: boolean
}

/**
 * Applies the protocol-specific runtime defaults used by the editor. The transform
 * is pure so protocol changes can be characterized without mounting React, and it
 * deliberately uses nullish defaults for numeric/boolean fields so explicit zero
 * and false values survive a round trip.
 */
export function transitionScriptProtocol(script: ScriptDefinition, protocol: string): ScriptProtocolTransition {
  const next = structuredClone(script)
  let selectedFirstAction = false
  next.protocol = protocol
  next.config = next.config || { base_url: '' }

  if (protocol === 'soap') {
    next.config.soap = {
      version: next.config.soap?.version || '1.1',
      namespaces: Object.keys(next.config.soap?.namespaces || {}).length > 0
        ? next.config.soap?.namespaces
        : { soapenv: defaultSOAPEnvelopeNamespace(next.config.soap?.version || '1.1') },
      ws_security: next.config.soap?.ws_security,
      fault_handling: {
        fail_on_fault: next.config.soap?.fault_handling?.fail_on_fault ?? true,
      },
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_SOAP_ACTION), name: 'SOAP_Request' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'java_http') {
    next.config.java_http = {
      codec: next.config.java_http?.codec || 'java_serialized_object_base64',
      helper_mode: next.config.java_http?.helper_mode || 'embedded',
      content_type: next.config.java_http?.content_type || 'application/x-java-serialized-object',
      accept: next.config.java_http?.accept || 'application/x-java-serialized-object',
      body_encoding: next.config.java_http?.body_encoding || 'base64',
      object_schema: next.config.java_http?.object_schema,
      request_mapping: next.config.java_http?.request_mapping,
      response_mapping: next.config.java_http?.response_mapping,
      extractor_type: next.config.java_http?.extractor_type || 'opaque',
      preserve_binary_result: next.config.java_http?.preserve_binary_result ?? true,
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_ACTION), name: 'Java_HTTP_Request', type: 'POST', method: 'POST' }]
      selectedFirstAction = true
    }
  }

  if (jmsProviderForProtocol(protocol)) {
    next.config.jms = jmsConfigForProtocol(protocol, next.config.jms || {}) || undefined
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_JMS_ACTION), name: 'Publish_Message' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'websocket') {
    next.config.websocket = {
      read_timeout_ms: next.config.websocket?.read_timeout_ms ?? 30000,
      write_timeout_ms: next.config.websocket?.write_timeout_ms ?? 10000,
      max_message_size: next.config.websocket?.max_message_size ?? 1048576,
      subprotocol: next.config.websocket?.subprotocol || '',
      origin: next.config.websocket?.origin || '',
      compression: next.config.websocket?.compression ?? false,
      reconnect: next.config.websocket?.reconnect ?? true,
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_WEBSOCKET_ACTION), name: 'Connect_WebSocket' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'grpc') {
    next.config.grpc = {
      use_reflection: next.config.grpc?.use_reflection ?? true,
      plain_text: next.config.grpc?.plain_text ?? false,
      timeout_ms: next.config.grpc?.timeout_ms ?? 30000,
      max_recv_msg_size: next.config.grpc?.max_recv_msg_size ?? 4194304,
      metadata: next.config.grpc?.metadata || {},
      authority: next.config.grpc?.authority || '',
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_GRPC_ACTION), name: 'Unary_Call' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'database') {
    next.config.database = {
      driver: next.config.database?.driver || 'sqlite',
      dsn: next.config.database?.dsn || ':memory:',
      dsn_secret: next.config.database?.dsn_secret || '',
      query_timeout_ms: next.config.database?.query_timeout_ms ?? 30000,
      max_open_conns: next.config.database?.max_open_conns ?? 1,
      max_idle_conns: next.config.database?.max_idle_conns,
      conn_max_lifetime_ms: next.config.database?.conn_max_lifetime_ms,
      max_rows: next.config.database?.max_rows ?? 100,
      read_only: next.config.database?.read_only ?? true,
      rollback_on_teardown: next.config.database?.rollback_on_teardown ?? true,
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_DATABASE_ACTION), name: 'Query_Database' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'network') {
    next.config.network = {
      subprotocol: next.config.network?.subprotocol || 'tcp',
      host: next.config.network?.host || 'localhost',
      address: next.config.network?.address || '',
      port: next.config.network?.port ?? 80,
      timeout_ms: next.config.network?.timeout_ms ?? 30000,
      tls: next.config.network?.tls ?? false,
      username: next.config.network?.username || '',
      password: next.config.network?.password || '',
      from: next.config.network?.from || '',
      to: next.config.network?.to || [],
      base_dn: next.config.network?.base_dn || '',
    }
    if (next.actions.length === 0) {
      next.actions = [{ ...structuredClone(EMPTY_NETWORK_ACTION), name: 'Network_Health' }]
      selectedFirstAction = true
    }
  }

  if (protocol === 'browser' && next.actions.length === 0) {
    next.actions = [{ ...structuredClone(EMPTY_BROWSER_ACTION), name: 'Navigate' }]
    selectedFirstAction = true
  }

  return { script: next, selectedFirstAction }
}
