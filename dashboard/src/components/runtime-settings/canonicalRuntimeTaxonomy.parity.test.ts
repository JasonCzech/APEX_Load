import { describe, expect, it } from 'vitest'
import {
  HTTP_TRANSPORT_TAB_OPTIONS,
  SCRIPT_PROTOCOL_OPTIONS,
  httpTransportTabIsAvailable,
  runtimeSettingsSectionIsAvailable,
} from '../scriptEditorRuntimeSettingsModel'
import {
  CANONICAL_RUNTIME_GROUPS,
  CANONICAL_RUNTIME_GROUP_IDS,
  getCanonicalGroupDestination,
  getCanonicalGroupForPath,
  getCanonicalGroupForSection,
  getCanonicalGroupsForProtocol,
  getCanonicalRuntimeGroupsForProtocol,
  getNativeTransportSectionForProtocol,
  getTlsMaxVersionHttp3Warning,
  isCanonicalGroupSupportedForProtocol,
  isLocationInCanonicalGroup,
  isTlsMaxVersionIncompatibleWithHttp3,
  mapLegacySectionToCanonicalGroup,
  type CanonicalCatalogFieldRef,
} from './canonicalRuntimeTaxonomy'

const GO_GROUP_CONSTANTS: Record<string, string> = {
  ConfigGroupTargetIdentity: 'target_identity',
  ConfigGroupRunLogicPacing: 'run_logic_pacing',
  ConfigGroupThinkTime: 'think_time',
  ConfigGroupTransportConnections: 'transport_connections',
  ConfigGroupSecurityProxyAuth: 'security_proxy_auth',
  ConfigGroupProtocolBehaviorSession: 'protocol_behavior_session',
  ConfigGroupErrorsLoggingDiagnostics: 'errors_logging_diagnostics',
}

// Roots the offline taxonomy must resolve without the backend catalog: the
// HTTP & SOAP surface plus the protocol-neutral blocks. Native protocol blocks,
// config.browser (kept apart from config.http) and Bruno come from the catalog.
const OFFLINE_ROOTS = [
  'base_url',
  'native_services',
  'jmeter',
  'sampler_schedules',
  'run_logic',
  'pacing',
  'think_time',
  'http',
  'soap',
  'web_runtime',
  'browser_emulation',
  'error_handling',
  'log',
  'results',
  'observability',
]

// The Go catalog is read at run time, so the dashboard build needs no copy of it.
async function loadGoCatalog(): Promise<CanonicalCatalogFieldRef[]> {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  const source = readFileSync(new URL('../../../../pkg/dsl/config_catalog.go', import.meta.url), 'utf8') as string
  const fields: CanonicalCatalogFieldRef[] = []
  for (const match of source.matchAll(/Path:\s*"([^"]+)",\s*\n\s*Group:\s*(\w+),/g)) {
    const group = GO_GROUP_CONSTANTS[match[2]]
    if (!group) throw new Error(`unknown Go config group constant ${match[2]} for ${match[1]}`)
    fields.push({ path: match[1], group })
  }
  // Guard the regex: every Path: entry in the catalog must have been read.
  expect(fields.length).toBe((source.match(/\bPath:\s*"/g) || []).length)
  return fields
}

function rootOf(path: string): string {
  return path.replace(/^config\./, '').split('.')[0]
}

describe('canonicalRuntimeTaxonomy parity with pkg/dsl/config_catalog.go', () => {
  it('resolves every HTTP/SOAP and shared catalog path to the backend group offline', async () => {
    const catalog = await loadGoCatalog()
    expect(catalog.length).toBeGreaterThan(300)
    const mismatches: string[] = []
    for (const field of catalog) {
      const offline = getCanonicalGroupForPath(field.path)
      if (OFFLINE_ROOTS.includes(rootOf(field.path))) {
        if (offline !== field.group) mismatches.push(`${field.path}: catalog=${field.group} offline=${offline}`)
      } else if (offline !== null) {
        mismatches.push(`${field.path}: not an offline root but resolved to ${offline}`)
      }
    }
    expect(mismatches).toEqual([])
  })

  it('prefers backend catalog groups for every catalog path, native blocks included', async () => {
    const catalog = await loadGoCatalog()
    const mismatches = catalog
      .filter(field => getCanonicalGroupForPath(field.path, catalog) !== field.group)
      .map(field => field.path)
    expect(mismatches).toEqual([])
    expect(getCanonicalGroupForPath('config.kafka.sasl_password', catalog)).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.jms.broker_url', catalog)).toBe('target_identity')
    // Unknown groups from a newer backend are ignored in favor of the offline map.
    expect(getCanonicalGroupForPath('config.http.version', [{ path: 'config.http.version', group: 'future_group' }])).toBe('transport_connections')
  })

  it('lists each offline config path in exactly one group and only real catalog paths', async () => {
    const catalog = await loadGoCatalog()
    const catalogPaths = catalog.map(field => field.path.replace(/^config\./, ''))
    const seen = new Map<string, string>()
    const duplicates: string[] = []
    const phantoms: string[] = []
    for (const group of CANONICAL_RUNTIME_GROUPS) {
      for (const path of group.configPaths) {
        if (seen.has(path)) duplicates.push(`${path}: ${seen.get(path)} and ${group.id}`)
        seen.set(path, group.id)
        if (path.startsWith('script.')) continue
        if (!catalogPaths.some(candidate => candidate === path || candidate.startsWith(`${path}.`))) phantoms.push(path)
      }
    }
    expect(duplicates).toEqual([])
    expect(phantoms).toEqual([])
  })
})

describe('canonicalRuntimeTaxonomy path resolution', () => {
  it('matches whole dot segments so http.tls does not capture http.tls_timeout_ms', () => {
    expect(getCanonicalGroupForPath('config.http.tls_timeout_ms')).toBe('transport_connections')
    expect(getCanonicalGroupForPath('config.http.tls.max_version')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.http.tls')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.http.challenge_delay_ms')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.http.user_agent')).toBe('target_identity')
    expect(getCanonicalGroupForPath('config.http.accept_language')).toBe('target_identity')
    expect(getCanonicalGroupForPath('config.http.max_redirects')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForPath('config.sampler_schedules')).toBe('run_logic_pacing')
    expect(getCanonicalGroupForPath('config.web_runtime.network_simulation.latency_ms')).toBe('transport_connections')
    expect(getCanonicalGroupForPath('config.http.default_headers.X-Trace')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForPath('config.soap.ws_security.username_token.password')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.browser.user_agent')).toBeNull()
    // A catalog ancestor entry must not capture a sibling that only shares its text prefix.
    expect(getCanonicalGroupForPath('config.http.tls_timeout_ms', [{ path: 'config.http.tls', group: 'security_proxy_auth' }])).toBe('transport_connections')
    expect(getCanonicalGroupForPath('config.http.proxy_mode', [{ path: 'config.http.proxy', group: 'transport_connections' }])).toBe('security_proxy_auth')
  })

  it('exports the plan 6.2 primitive names', () => {
    expect(getCanonicalGroupsForProtocol).toBe(getCanonicalRuntimeGroupsForProtocol)
    expect(mapLegacySectionToCanonicalGroup('soap')).toBe(getCanonicalGroupForSection('soap'))
  })
})

describe('canonicalRuntimeTaxonomy navigation parity with section availability', () => {
  const protocols = [
    ...SCRIPT_PROTOCOL_OPTIONS.map(option => option.value as string),
    'http/1.1',
    'http/2',
    'http/3',
    'java_vuser',
  ]

  it.each(protocols)('sends every visible %s pill to a section the Author view renders', protocol => {
    for (const group of getCanonicalRuntimeGroupsForProtocol(protocol, 'author')) {
      const destination = getCanonicalGroupDestination(group.id, protocol)
      expect(runtimeSettingsSectionIsAvailable(destination.section, protocol), `${group.id} -> ${destination.section}`).toBe(true)
      if (destination.httpTransportTab) {
        expect(httpTransportTabIsAvailable(destination.httpTransportTab, protocol)).toBe(true)
      }
      expect(isLocationInCanonicalGroup(group.id, destination.section, destination.httpTransportTab)).toBe(true)
    }
  })

  it.each(['jms-solace', 'jms-ibmmq', 'jms-servicebus'])('routes Transport & connections to the JMS section for %s', protocol => {
    expect(getNativeTransportSectionForProtocol(protocol)).toBe('jms')
    expect(getCanonicalGroupDestination('transport_connections', protocol)).toEqual({ section: 'jms' })
  })

  it('hides Transport & connections for a protocol without a transport section', () => {
    expect(getNativeTransportSectionForProtocol('kafka')).toBeNull()
    expect(isCanonicalGroupSupportedForProtocol('transport_connections', 'kafka')).toBe(false)
    expect(getCanonicalRuntimeGroupsForProtocol('kafka').map(group => group.id)).toEqual([
      'target_identity',
      'run_logic_pacing',
      'think_time',
      'errors_logging_diagnostics',
    ])
  })

  it('places every HTTP transport tab in exactly one group', () => {
    for (const tab of HTTP_TRANSPORT_TAB_OPTIONS) {
      const owners = CANONICAL_RUNTIME_GROUP_IDS.filter(groupId => isLocationInCanonicalGroup(groupId, 'http', tab.id))
      expect(owners, tab.id).toHaveLength(1)
    }
    // The SOAP section is shared by Security (WS-Security) and Protocol behavior (envelope).
    expect(isLocationInCanonicalGroup('security_proxy_auth', 'soap')).toBe(true)
    expect(isLocationInCanonicalGroup('protocol_behavior_session', 'soap')).toBe(true)
  })
})

describe('HTTP/3 TLS max version warning', () => {
  it('uses the protocol alias default when config.http.version is unset', () => {
    expect(isTlsMaxVersionIncompatibleWithHttp3('', '1.2', 'http/3')).toBe(true)
    expect(isTlsMaxVersionIncompatibleWithHttp3(undefined, '1.2', 'http/3')).toBe(true)
    expect(getTlsMaxVersionHttp3Warning(undefined, '1.2', 'http/3')).toContain('HTTP/3 (QUIC) requires TLS 1.3')
    expect(isTlsMaxVersionIncompatibleWithHttp3('', '1.3', 'http/3')).toBe(false)
    expect(isTlsMaxVersionIncompatibleWithHttp3('', '1.2', 'http/2')).toBe(false)
    expect(isTlsMaxVersionIncompatibleWithHttp3('', '1.2', 'http')).toBe(false)
  })

  it('lets an explicit version override the protocol alias, like dsl.InheritedHTTPVersion', () => {
    expect(isTlsMaxVersionIncompatibleWithHttp3('2', '1.2', 'http/3')).toBe(false)
    expect(isTlsMaxVersionIncompatibleWithHttp3('HTTP/3', '1.2', 'http')).toBe(true)
    expect(isTlsMaxVersionIncompatibleWithHttp3('existing-auto', '1.2', 'http/3')).toBe(false)
  })
})
