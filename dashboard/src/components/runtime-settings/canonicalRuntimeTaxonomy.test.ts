import { describe, expect, it } from 'vitest'
import {
  CANONICAL_RUNTIME_GROUPS,
  CANONICAL_RUNTIME_GROUP_IDS,
  getCanonicalGroupDestination,
  getCanonicalGroupForPath,
  getCanonicalGroupForSection,
  getCanonicalRuntimeGroup,
  getCanonicalRuntimeGroupsForProtocol,
  getTlsMaxVersionHttp3Warning,
  hasIgnoredSoapConfigOnHttpScript,
  hasReadOnlyScheduleCompatibility,
  isCanonicalGroupSupportedForProtocol,
  isTlsMaxVersionIncompatibleWithHttp3,
} from './canonicalRuntimeTaxonomy'

describe('canonicalRuntimeTaxonomy', () => {
  it('defines the canonical 7-group taxonomy in exact order with sentence-case labels', () => {
    expect(CANONICAL_RUNTIME_GROUP_IDS).toEqual([
      'target_identity',
      'run_logic_pacing',
      'think_time',
      'transport_connections',
      'security_proxy_auth',
      'protocol_behavior_session',
      'errors_logging_diagnostics',
    ])
    expect(CANONICAL_RUNTIME_GROUPS.map(group => ({ id: group.id, label: group.label }))).toEqual([
      { id: 'target_identity', label: 'Target & identity' },
      { id: 'run_logic_pacing', label: 'Run logic & pacing' },
      { id: 'think_time', label: 'Think time' },
      { id: 'transport_connections', label: 'Transport & connections' },
      { id: 'security_proxy_auth', label: 'Security, proxy & auth' },
      { id: 'protocol_behavior_session', label: 'Protocol behavior & session' },
      { id: 'errors_logging_diagnostics', label: 'Errors, logging & diagnostics' },
    ])
  })

  it('includes all required config paths across the 7 canonical groups', () => {
    const targetIdentity = getCanonicalRuntimeGroup('target_identity')
    expect(targetIdentity.configPaths).toEqual(
      expect.arrayContaining(['base_url', 'native_services', 'jmeter', 'sampler_schedules']),
    )

    const runLogicPacing = getCanonicalRuntimeGroup('run_logic_pacing')
    expect(runLogicPacing.configPaths).toEqual(
      expect.arrayContaining([
        'run_logic.termination_mode',
        'run_logic.blocks',
        'pacing.quota',
        'pacing.arrival_process',
        'pacing.rate_stages',
      ]),
    )

    const thinkTime = getCanonicalRuntimeGroup('think_time')
    expect(thinkTime.configPaths).toEqual(
      expect.arrayContaining(['think_time.type', 'think_time.distribution']),
    )

    const transport = getCanonicalRuntimeGroup('transport_connections')
    expect(transport.configPaths).toEqual(
      expect.arrayContaining([
        'http.version',
        'http.quic',
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
      ]),
    )

    const security = getCanonicalRuntimeGroup('security_proxy_auth')
    expect(security.configPaths).toEqual(
      expect.arrayContaining([
        'http.tls.max_version',
        'http.proxy_mode',
        'http.proxy.pac_url',
        'http.auth.type',
        'http.auth.domain',
        'http.auth.hosts',
        'soap.ws_security',
      ]),
    )

    const protocolBehavior = getCanonicalRuntimeGroup('protocol_behavior_session')
    expect(protocolBehavior.configPaths).toEqual(
      expect.arrayContaining([
        'http.default_headers',
        'http.cookies',
        'soap.version',
        'soap.namespaces',
        'soap.fault_handling',
        'browser',
        'web_runtime',
      ]),
    )

    const errorsLogging = getCanonicalRuntimeGroup('errors_logging_diagnostics')
    expect(errorsLogging.configPaths).toEqual(
      expect.arrayContaining([
        'error_handling.continue_on_error',
        'error_handling.retry_on',
        'error_handling.honor_retry_after',
        'error_handling.retry_after_max_ms',
        'error_handling.fail_transaction_on_error_message',
        'log.max_response_bytes',
        'log.redact_run_evidence',
        'results.message_detail',
        'results.property_analytics',
      ]),
    )
  })

  it('filters groups by protocol capability while keeping all 7 groups for HTTP aliases and SOAP', () => {
    expect(getCanonicalRuntimeGroupsForProtocol('http', 'author')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('http/1.1', 'author')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('http/2', 'author')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('http/3', 'author')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('https', 'author')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('soap', 'import')).toHaveLength(7)
    expect(getCanonicalRuntimeGroupsForProtocol('browser', 'debug')).toHaveLength(7)

    const databaseGroups = getCanonicalRuntimeGroupsForProtocol('database', 'author').map(g => g.id)
    expect(databaseGroups).toEqual([
      'target_identity',
      'run_logic_pacing',
      'think_time',
      'transport_connections',
      'errors_logging_diagnostics',
    ])
    expect(isCanonicalGroupSupportedForProtocol('security_proxy_auth', 'database')).toBe(false)
    expect(isCanonicalGroupSupportedForProtocol('protocol_behavior_session', 'database')).toBe(false)
  })

  it('maps sections, HTTP transport sub-tabs, and config paths to canonical groups', () => {
    expect(getCanonicalGroupForSection('script_config')).toBe('target_identity')
    expect(getCanonicalGroupForSection('imported_source')).toBe('target_identity')
    expect(getCanonicalGroupForSection('runlogic')).toBe('run_logic_pacing')
    expect(getCanonicalGroupForSection('pacing')).toBe('run_logic_pacing')
    expect(getCanonicalGroupForSection('think')).toBe('think_time')
    expect(getCanonicalGroupForSection('http', 'connection')).toBe('transport_connections')
    expect(getCanonicalGroupForSection('http', 'timeouts')).toBe('transport_connections')
    expect(getCanonicalGroupForSection('http', 'tls')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForSection('http', 'proxy')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForSection('http', 'authentication')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForSection('http', 'request_defaults')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForSection('http', 'session_cache')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForSection('soap')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForSection('browser_emulation')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForSection('web_runtime')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForSection('errors')).toBe('errors_logging_diagnostics')
    expect(getCanonicalGroupForSection('log')).toBe('errors_logging_diagnostics')
    expect(getCanonicalGroupForSection('database')).toBe('transport_connections')

    expect(getCanonicalGroupDestination('transport_connections', 'http')).toEqual({
      section: 'http',
      httpTransportTab: 'connection',
    })
    expect(getCanonicalGroupDestination('transport_connections', 'database')).toEqual({
      section: 'database',
    })
    expect(getCanonicalGroupDestination('security_proxy_auth', 'soap')).toEqual({
      section: 'http',
      httpTransportTab: 'tls',
    })

    expect(getCanonicalGroupForPath('config.base_url')).toBe('target_identity')
    expect(getCanonicalGroupForPath('config.run_logic.termination_mode')).toBe('run_logic_pacing')
    expect(getCanonicalGroupForPath('config.pacing.quota.tpm')).toBe('run_logic_pacing')
    expect(getCanonicalGroupForPath('config.think_time.distribution')).toBe('think_time')
    expect(getCanonicalGroupForPath('config.http.tls.max_version')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.http.proxy_mode')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.http.auth.type')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.soap.ws_security.username_token')).toBe('security_proxy_auth')
    expect(getCanonicalGroupForPath('config.soap.namespaces')).toBe('protocol_behavior_session')
    expect(getCanonicalGroupForPath('config.error_handling.continue_on_error')).toBe('errors_logging_diagnostics')
    expect(getCanonicalGroupForPath('config.log.max_response_bytes')).toBe('errors_logging_diagnostics')
  })

  it('detects TLS max_version < 1.3 incompatibility with HTTP/3 and cross-protocol notices', () => {
    expect(isTlsMaxVersionIncompatibleWithHttp3('3', '1.2')).toBe(true)
    expect(isTlsMaxVersionIncompatibleWithHttp3('auto+h3', '1.1')).toBe(true)
    expect(isTlsMaxVersionIncompatibleWithHttp3('3', '1.3')).toBe(false)
    expect(isTlsMaxVersionIncompatibleWithHttp3('3', '')).toBe(false)
    expect(isTlsMaxVersionIncompatibleWithHttp3('2', '1.2')).toBe(false)
    expect(getTlsMaxVersionHttp3Warning('3', '1.2')).toContain('HTTP/3 (QUIC) requires TLS 1.3')
    expect(getTlsMaxVersionHttp3Warning('2', '1.2')).toBeNull()

    expect(
      hasIgnoredSoapConfigOnHttpScript('http', {
        base_url: 'https://api.example.com',
        soap: { version: '1.2' },
      }),
    ).toBe(true)
    expect(
      hasIgnoredSoapConfigOnHttpScript('http/2', {
        base_url: 'https://api.example.com',
        soap: { version: '1.2' },
      }),
    ).toBe(true)
    expect(
      hasIgnoredSoapConfigOnHttpScript('https', {
        base_url: 'https://api.example.com',
        soap: { version: '1.1' },
      }),
    ).toBe(true)
    expect(
      hasIgnoredSoapConfigOnHttpScript('soap', {
        base_url: 'https://api.example.com',
        soap: { version: '1.2' },
      }),
    ).toBe(false)
    expect(
      hasIgnoredSoapConfigOnHttpScript('http', {
        base_url: 'https://api.example.com',
      }),
    ).toBe(false)

    expect(
      hasReadOnlyScheduleCompatibility({
        base_url: 'https://api.example.com',
        sampler_schedules: [{ id: 'sched-1', segments: [{ start_rps: 1, end_rps: 5, duration_seconds: 10 }] }],
      }),
    ).toBe(true)
    expect(
      hasReadOnlyScheduleCompatibility({
        base_url: 'https://api.example.com',
        jmeter: { profile: 'default', source_plan_id: 'plan-1', source_group_id: 'tg-1' },
      }),
    ).toBe(true)
    expect(
      hasReadOnlyScheduleCompatibility({
        base_url: 'https://api.example.com',
      }),
    ).toBe(false)
  })
})
