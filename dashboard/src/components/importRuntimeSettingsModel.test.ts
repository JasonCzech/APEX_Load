import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { getCanonicalGroupForPath } from './runtime-settings/canonicalRuntimeTaxonomy'
import { runtimeSettingEffects } from './importRuntimeSettingNotes'
import {
 CANONICAL_RUNTIME_GROUPS,
 CANONICAL_RUNTIME_GROUP_IDS,
 fetchImportRuntimeFields,
 importRuntimeFields,
 mergeImportRuntimeFields,
 preferredRuntimeCategory,
 readSetting,
 resetRegisteredImportRuntimeFields,
 resolveCanonicalRuntimeGroup,
 resolveRuntimeSubgroup,
 runtimeSettingRows,
 settingIsRelevant,
} from './importRuntimeSettingsModel'

const makeScript = (protocol = 'http', config: ScriptDefinition['config'] = { base_url: 'https://api.example.test' }): ScriptDefinition => ({
 name: 'Imported Script',
 version: '1.0',
 protocol,
 config,
 actions: [],
})

describe('importRuntimeSettingsModel', () => {
 it('resolves a path through the shared Author taxonomy and keeps category mapping for rows without a path', () => {
  const paths = [
   'config.http.user_agent',
   'config.http.accept_language',
   'config.browser_emulation.platform',
   'config.http.max_redirects',
   'config.http.challenge_delay_ms',
   'config.http.traceparent',
   'config.http.content_check_error_limit',
   'config.web_runtime.network_simulation.preset',
   'config.web_runtime.source_ips',
   'config.soap.fault_handling.fail_on_fault',
   'config.soap.ws_security.timestamp.ttl_seconds',
   'config.pacing.quota.tpm',
   'config.think_time.force',
  ]
  for (const path of paths) {
   const id = getCanonicalGroupForPath(path)
   expect(id, path).not.toBeNull()
   // A path wins over a legacy row category, whatever the category says.
   expect(CANONICAL_RUNTIME_GROUP_IDS[resolveCanonicalRuntimeGroup('Run logic', path)], path).toBe(id)
  }
  // A group the backend served still wins over the path.
  expect(resolveCanonicalRuntimeGroup('security_proxy_auth', 'config.http.max_redirects')).toBe('Security, proxy & auth')
  // Paths the offline taxonomy does not know fall back to the category.
  expect(resolveCanonicalRuntimeGroup('Run logic', 'config.unknown_section.value')).toBe('Run logic & pacing')
  expect(resolveCanonicalRuntimeGroup('Logging')).toBe('Errors, logging & diagnostics')
 })

 afterEach(() => {
  resetRegisteredImportRuntimeFields()
 })

 it('defines the canonical 7-group taxonomy and resolves legacy category aliases', () => {
  expect(CANONICAL_RUNTIME_GROUPS).toEqual([
   'Target & identity',
   'Run logic & pacing',
   'Think time',
   'Transport & connections',
   'Security, proxy & auth',
   'Protocol behavior & session',
   'Errors, logging & diagnostics',
  ])
  expect(Object.keys(CANONICAL_RUNTIME_GROUP_IDS)).toEqual([...CANONICAL_RUNTIME_GROUPS])
  expect(resolveCanonicalRuntimeGroup('Run logic')).toBe('Run logic & pacing')
  expect(resolveCanonicalRuntimeGroup('Pacing')).toBe('Run logic & pacing')
  expect(resolveCanonicalRuntimeGroup('Think time')).toBe('Think time')
  expect(resolveCanonicalRuntimeGroup('HTTP', 'config.http.request_timeout_ms')).toBe('Transport & connections')
  expect(resolveCanonicalRuntimeGroup('Proxy', 'config.http.proxy.pac_url')).toBe('Security, proxy & auth')
  expect(resolveCanonicalRuntimeGroup('HTTP', 'config.http.tls.min_version')).toBe('Security, proxy & auth')
  expect(resolveCanonicalRuntimeGroup('Browser emulation')).toBe('Protocol behavior & session')
  expect(resolveCanonicalRuntimeGroup('SOAP', 'config.soap.ws_security.username_token.username')).toBe('Security, proxy & auth')
  expect(resolveCanonicalRuntimeGroup('SOAP', 'config.soap.version')).toBe('Protocol behavior & session')
  expect(resolveCanonicalRuntimeGroup('Error handling')).toBe('Errors, logging & diagnostics')
  expect(resolveCanonicalRuntimeGroup('Logging')).toBe('Errors, logging & diagnostics')
  expect(resolveCanonicalRuntimeGroup('Diagnostics')).toBe('Errors, logging & diagnostics')
 })

 it('folds the legacy 1-field Proxy category into Security, proxy & auth without leaving a standalone Proxy category', () => {
  expect(importRuntimeFields.filter(field => field.category === 'Proxy')).toEqual([])
  const proxyPaths = [
   'config.http.proxy_mode',
   'config.http.proxy.url',
   'config.http.proxy.pac_url',
   'config.http.proxy.username',
   'config.http.proxy.no_proxy',
  ]
  for (const path of proxyPaths) {
   const field = importRuntimeFields.find(candidate => candidate.path === path)
   expect(field, `missing ${path}`).toBeDefined()
   expect(field?.group).toBe('Security, proxy & auth')
   expect(field?.subgroup).toBe('Proxy')
   expect(field?.category).toBe('HTTP')
  }
 })

 // Each path's group is checked against pkg/dsl/config_catalog.go in
 // importRuntimeSettingsCatalogParity.test.ts; this list pins the Phase 2 paths.
 it('keeps every Phase 2 HTTP and SOAP runtime setting', () => {
  const expectedByArea: Record<string, string[]> = {
   'Target & identity': ['config.base_url'],
   'Run logic & pacing': [
    'config.run_logic.iterations',
    'config.run_logic.global_iterations',
    'config.run_logic.max_transactions',
    'config.run_logic.workload_model',
    'config.run_logic.max_in_flight',
    'config.run_logic.auto_transactions',
    'config.run_logic.termination_mode',
    'config.pacing.type',
    'config.pacing.delay_ms',
    'config.pacing.min_delay_ms',
    'config.pacing.max_delay_ms',
    'config.pacing.interval_ms',
    'config.pacing.min_interval_ms',
    'config.pacing.max_interval_ms',
    'config.pacing.tps',
   ],
   'Think time': [
    'config.think_time.ignore',
    'config.think_time.type',
    'config.think_time.distribution',
    'config.think_time.mean_ms',
    'config.think_time.std_ms',
    'config.think_time.min_ms',
    'config.think_time.max_ms',
    'config.think_time.min_percent',
    'config.think_time.max_percent',
    'config.think_time.multiplier',
    'config.think_time.limit_ms',
   ],
   'Transport & connections': [
    'config.http.version',
    'config.http.quic.handshake_timeout_ms',
    'config.http.quic.max_idle_timeout_ms',
    'config.http.quic.keep_alive_ms',
    'config.http.quic.broken_ttl_ms',
    'config.http.quic.alt_svc_cache',
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
   ],
   'Security, proxy & auth': [
    'config.http.tls.min_version',
    'config.http.tls.max_version',
    'config.http.tls.insecure_skip_verify',
    'config.http.tls.client_cert_file',
    'config.http.tls.client_key_file',
    'config.http.tls.ca_cert_file',
    'config.http.proxy_mode',
    'config.http.proxy.url',
    'config.http.proxy.pac_url',
    'config.http.proxy.username',
    'config.http.proxy.no_proxy',
    'config.http.auth.type',
    'config.http.auth.username',
    'config.http.auth.domain',
    'config.soap.ws_security.username_token.username',
    'config.soap.ws_security.username_token.password_type',
    'config.soap.ws_security.username_token.nonce',
    'config.soap.ws_security.username_token.created',
    'config.soap.ws_security.timestamp.ttl_seconds',
   ],
   'Protocol behavior & session': [
    'config.soap.version',
    'config.soap.fault_handling.fail_on_fault',
    'config.http.user_agent',
    'config.http.accept_language',
    'config.http.accept_encoding',
    'config.http.traceparent',
    'config.http.traceparent_sampled',
    'config.http.cookies_enabled',
    'config.http.clear_cookies_per_iteration',
    'config.http.cache_enabled',
    'config.http.cache_max_unused_iterations',
    'config.http.cache_excluded_mime_types',
    'config.http.max_meta_refresh',
    'config.http.content_check_error_limit',
    'config.http.http_errors_as_warnings',
    'config.http.fail_noncritical_resources',
    'config.http.resource_timeout_as_warning',
    'config.http.legacy_html_checks',
    'config.http.html_parse_mode',
    'config.http.html_excluded_mime_types',
    'config.http.aura_recorded_failures',
    'config.browser_emulation.mode',
    'config.browser_emulation.browser_type',
    'config.browser_emulation.browser_version',
    'config.browser_emulation.platform',
    'config.browser_emulation.language',
    'config.browser_emulation.custom_user_agent',
    'config.browser_emulation.cache.clear_cache_for_new_user',
    'config.browser_emulation.cache.simulate_cache',
    'config.browser_emulation.cache.cache_content_urls',
    'config.browser_emulation.cache.check_newer_versions',
    'config.browser_emulation.cache.download_non_html_resources',
    'config.browser_emulation.cache.new_user_each_iteration',
    'config.browser_emulation.cache.simulate_prefetch',
    'config.web_runtime.network_simulation.mode',
    'config.web_runtime.network_simulation.preset',
    'config.web_runtime.network_simulation.bandwidth_kbps',
    'config.web_runtime.network_simulation.latency_ms',
    'config.web_runtime.network_simulation.jitter_ms',
    'config.web_runtime.network_simulation.packet_loss_pct',
    'config.web_runtime.streaming.mode',
    'config.web_runtime.streaming.buffer_size_seconds',
    'config.web_runtime.streaming.fixed_bitrate_kbps',
    'config.web_runtime.streaming.bandwidth_estimation',
    'config.web_runtime.data_formatting.auto_detect',
   ],
   'Errors, logging & diagnostics': [
    'config.error_handling.continue_on_error',
    'config.error_handling.snapshot_on_error',
    'config.error_handling.fail_transaction_on_error_message',
    'config.error_handling.max_retries',
    'config.error_handling.retry_delay_ms',
    'config.error_handling.retry_backoff',
    'config.error_handling.honor_retry_after',
    'config.error_handling.retry_after_max_ms',
    'config.log.level',
    'config.log.error_buffer_bytes',
    'config.log.max_response_bytes',
    'config.log.on_error_only',
    'config.log.redact_run_evidence',
    'config.log.extended_options',
    'config.results.message_detail.mode',
    'config.results.message_detail.sample_rate',
    'config.results.message_detail.include_resources',
    'config.results.message_detail.max_items',
    'config.results.message_detail.max_item_bytes',
    'config.results.message_detail.max_total_bytes',
   ],
  }

  for (const path of Object.values(expectedByArea).flat()) {
   const field = importRuntimeFields.find(candidate => candidate.path === path)
   expect(field, `expected ${path} in catalog`).toBeDefined()
   expect(field?.subgroup).toBe(resolveRuntimeSubgroup(path, field?.category))
  }

  const httpVersion = importRuntimeFields.find(field => field.path === 'config.http.version')
  expect(httpVersion?.options).toEqual(['1.1', '2', 'auto+h3', '3'])

  const thinkTimeType = importRuntimeFields.find(field => field.path === 'config.think_time.type')
  expect(thinkTimeType?.options).toEqual([
   'none',
   'fixed',
   'recorded',
   'random_percentage',
   'gaussian',
   'normal',
   'uniform',
   'exponential',
  ])
 })

 it('shows all 7 canonical groups for non-LoadRunner HTTP and SOAP imports and scopes SOAP fields to protocol === soap', () => {
  const httpRows = runtimeSettingRows(makeScript('http'))['Additional APEX settings']
  const httpGroups = new Set(httpRows.map(row => row.group))
  expect([...CANONICAL_RUNTIME_GROUPS].every(group => httpGroups.has(group))).toBe(true)
  expect(httpRows.some(row => row.path?.startsWith('config.soap.'))).toBe(false)

  const soapRows = runtimeSettingRows(makeScript('soap'))['Additional APEX settings']
  const soapPaths = soapRows.map(row => row.path)
  expect(soapPaths).toEqual(
   expect.arrayContaining([
    'config.soap.version',
    'config.soap.fault_handling.fail_on_fault',
    'config.soap.ws_security.username_token.username',
    'config.soap.ws_security.username_token.password_type',
    'config.soap.ws_security.username_token.nonce',
    'config.soap.ws_security.username_token.created',
    'config.soap.ws_security.timestamp.ttl_seconds',
   ]),
  )

  const exponentialScript = makeScript('http', {
   base_url: 'https://api.example.test',
   think_time: { type: 'exponential', mean_ms: 750 },
  })
  expect(settingIsRelevant(exponentialScript, 'config.think_time.mean_ms')).toBe(true)
  expect(settingIsRelevant(exponentialScript, 'config.think_time.std_ms')).toBe(false)
 })

 it('reads extended log options from string arrays and ignores non-array objects', () => {
  const arrayScript = makeScript('http', {
   base_url: 'https://api.example.test',
   log: { extended_options: ['parameter_substitution', 'data_returned'] as unknown as string[] },
  })
  expect(readSetting(arrayScript, 'config.log.extended_options')).toEqual(['parameter_substitution', 'data_returned'])

  const objectScript = makeScript('http', {
   base_url: 'https://api.example.test',
   log: {
    extended_options: {
     parameter_substitution: false,
     data_returned_by_server: true,
     advanced_trace: true,
    } as unknown as string[],
   },
  })
  expect(readSetting(objectScript, 'config.log.extended_options')).toBeUndefined()
 })

 it('merges remote fields from GET /api/v1/scripts/import/settings/fields, offering secrets as Credentials references and skipping non-scalars', async () => {
  const merged = mergeImportRuntimeFields([
   {
    path: 'config.http.tls.min_version',
    category: 'HTTP',
    group: 'Security, proxy & auth',
    type: 'select',
    options: ['1.0', '1.1', '1.2', '1.3'],
   },
   {
    path: 'config.http.auth.password',
    category: 'HTTP',
    group: 'Security, proxy & auth',
    type: 'string',
    secret: true,
   },
   {
    path: 'config.soap.namespaces',
    category: 'SOAP',
    group: 'Protocol behavior & session',
    type: 'key_value_map',
   },
   {
    path: 'config.http.custom_future_secret',
    category: 'HTTP',
    group: 'Security, proxy & auth',
    label: 'Custom future secret',
    type: 'string',
    secret: true,
   },
   {
    path: 'config.http.custom_future_timeout_ms',
    category: 'HTTP',
    group: 'Transport & connections',
    subgroup: 'Timeouts',
    label: 'Custom future timeout (ms)',
    type: 'int',
    protocols: ['http'],
   },
  ])

  expect(merged.find(field => field.path === 'config.http.tls.min_version')?.options).toEqual(['1.2', '1.3'])
  // A secret field is script-sourced on import: it stays editable, and a new
  // value must be a Credentials reference.
  expect(merged.find(field => field.path === 'config.http.auth.password')).toMatchObject({ type: 'text', credentialReference: true })
  expect(merged.find(field => field.path === 'config.http.custom_future_secret')).toMatchObject({ type: 'text', credentialReference: true })
  expect(merged.find(field => field.path === 'config.http.custom_future_secret')).not.toHaveProperty('secret')
  expect(merged.find(field => field.path === 'config.soap.namespaces')).toBeUndefined()
  const futureField = merged.find(field => field.path === 'config.http.custom_future_timeout_ms')
  expect(futureField).toMatchObject({
   category: 'HTTP',
   group: 'Transport & connections',
   groupId: 'transport_connections',
   subgroup: 'Timeouts',
   label: 'Custom future timeout (ms)',
   type: 'number',
  })

  const fetcher = vi.fn().mockResolvedValueOnce({
   groups: [{ id: 'transport_connections', label: 'Transport & connections' }],
   fields: [
    {
     path: 'config.http.dynamic_header_limit',
     category: 'HTTP',
     group: 'transport_connections',
     label: 'Dynamic header limit',
     type: 'int',
    },
   ],
  })
  const loaded = await fetchImportRuntimeFields(fetcher)
  expect(fetcher).toHaveBeenCalledWith('/scripts/import/settings/fields')
  expect(loaded.some(field => field.path === 'config.http.dynamic_header_limit')).toBe(true)
 })
})

describe('browser emulation precedence', () => {
 it('says when browser emulation replaces an HTTP cache, User-Agent or Accept-Language row at replay', () => {
  const rows = (config: ScriptDefinition['config']) => runtimeSettingRows(makeScript('http', config))['Additional APEX settings']
  const note = (config: ScriptDefinition['config'], path: string) => rows(config).find(row => row.path === path)?.note || ''
  const base = { base_url: 'https://api.example.test', http: { cache_enabled: true, user_agent: 'probe/1', accept_language: 'en-US' } }
  expect(note(base, 'config.http.cache_enabled')).not.toContain('Replay uses')
  expect(note({ ...base, browser_emulation: { cache: { simulate_cache: false } } }, 'config.http.cache_enabled')).toContain('"Simulate browser cache" value (Disabled) instead of this setting')
  expect(note({ ...base, browser_emulation: { mode: 'specific', browser_type: 'chrome' } }, 'config.http.user_agent')).toContain('User-Agent of the emulated browser')
  expect(note({ ...base, browser_emulation: { mode: 'custom', custom_user_agent: '' } }, 'config.http.user_agent')).not.toContain('Replay sends')
  expect(note({ ...base, browser_emulation: { language: 'fr-FR' } }, 'config.http.accept_language')).toContain('browser emulation language (fr-FR)')
 })
})

describe('pacing relevance', () => {
 it('shows quota and arrival settings only for the pacing modes that use them', () => {
  const script = (pacing: Record<string, unknown>) => makeScript('http', { base_url: 'https://api.example.test', pacing: pacing as unknown as NonNullable<ScriptDefinition['config']['pacing']> })
  expect(settingIsRelevant(script({ type: 'none' }), 'config.pacing.quota.tpm')).toBe(false)
  expect(settingIsRelevant(script({ type: 'quota' }), 'config.pacing.quota.tpm')).toBe(true)
  expect(settingIsRelevant(script({ type: 'tps' }), 'config.pacing.arrival_process')).toBe(true)
  expect(settingIsRelevant(script({ type: 'tps', arrival_process: 'poisson' }), 'config.pacing.burstiness')).toBe(false)
  expect(settingIsRelevant(script({ type: 'tps', arrival_process: 'gamma' }), 'config.pacing.burstiness')).toBe(true)
 })

 it('reads pacing and think-time modes in their runtime spelling', () => {
  const script = (config: Record<string, unknown>) => makeScript('http', { base_url: 'https://api.example.test', ...config } as ScriptDefinition['config'])
  expect(settingIsRelevant(script({ pacing: { type: ' TPS ' } }), 'config.pacing.arrival_process')).toBe(true)
  expect(settingIsRelevant(script({ pacing: { type: 'Quota' } }), 'config.pacing.quota.tpm')).toBe(true)
  expect(settingIsRelevant(script({ pacing: { type: 'Fixed_Delay' } }), 'config.pacing.delay_ms')).toBe(true)
  expect(settingIsRelevant(script({ think_time: { type: 'Gaussian', mean_ms: 1000 } }), 'config.think_time.std_ms')).toBe(true)
  // The engine and Author read the arrival process trimmed and case-folded.
  expect(settingIsRelevant(script({ pacing: { type: 'tps', tps: 5, arrival_process: ' Gamma ' } }), 'config.pacing.burstiness')).toBe(true)
  expect(settingIsRelevant(script({ pacing: { type: 'tps', tps: 5, arrival_process: 'GAMMA' } }), 'config.pacing.burstiness')).toBe(true)
  expect(settingIsRelevant(script({ pacing: { type: 'tps', tps: 5, arrival_process: ' Poisson ' } }), 'config.pacing.burstiness')).toBe(false)
 })
})

describe('script-level think time force', () => {
 it('does not claim the script-level force applies, matching Author and the catalog', () => {
  const field = importRuntimeFields.find(item => item.path === 'config.think_time.force')!
  expect(field.label).toContain('no effect')
  expect(runtimeSettingEffects['config.think_time.force']).toMatch(/^Has no effect at script level/)
  expect(runtimeSettingEffects['config.think_time.force']).not.toMatch(/^Applies/)
 })
})

describe('import runtime settings contract', () => {
  // SettingValue renders its own "Default" (unset) entry first, so a listed ""
  // option would render a second, unlabeled blank row.
  it('lists no empty select option', () => {
    const blank = importRuntimeFields.filter(field => field.options?.some(option => option.trim() === '')).map(field => field.path)
    expect(blank).toEqual([])
  })
})

describe('remaining protocol families', () => {
 const draft = (protocol: string, config: ScriptDefinition['config'] = {} as ScriptDefinition['config']): ScriptDefinition => ({ name: 'Imported', version: '1.0', protocol, config, actions: [] })

 it('offers a protocol family setting unset only for that protocol, and a configured one for any', () => {
  expect(settingIsRelevant(draft('http'), 'config.grpc.timeout_ms')).toBe(false)
  expect(settingIsRelevant(draft('grpc'), 'config.grpc.timeout_ms')).toBe(true)
  expect(settingIsRelevant(draft('http'), 'config.grpc.timeout_ms', true)).toBe(true)
  for (const [path, protocol] of [['config.eventhubs.namespace', 'eventhubs'], ['config.database.max_rows', 'database'], ['config.network.port', 'network'], ['config.appium.server_url', 'appium'], ['config.java_http.accept', 'java_http'], ['config.java_vuser.classpath', 'java_vuser'], ['config.jms.message_vpn', 'jms-solace']]) {
   expect(settingIsRelevant(draft(protocol), path), path).toBe(true)
   expect(settingIsRelevant(draft('kafka'), path), path).toBe(false)
  }
  expect(settingIsRelevant(draft('jms-ibmmq'), 'config.observability.trace_carrier')).toBe(true)
  expect(settingIsRelevant(draft('eventhubs'), 'config.observability.trace_carrier')).toBe(true)
  expect(settingIsRelevant(draft('http'), 'config.observability.trace_carrier')).toBe(false)
 })

 it('lists the unset settings of the script protocol family under its own category', () => {
  const rows = runtimeSettingRows(draft('database'))['Additional APEX settings']
  expect(rows.filter(row => row.category === 'Database').map(row => row.path)).toEqual(expect.arrayContaining(['config.database.driver', 'config.database.dsn', 'config.database.max_rows']))
  expect(rows.find(row => row.path === 'config.database.dsn')?.field?.credentialReference).toBe(true)
  expect(rows.find(row => row.path === 'config.database.driver')?.field?.options).toEqual(['postgres', 'mysql', 'oracle', 'odbc', 'sqlite', 'sqlite3'])
  expect(rows.some(row => row.path?.startsWith('config.kafka.') || row.path?.startsWith('config.grpc.'))).toBe(false)
 })

 it('keeps an imported setting of another family visible, as Java imports carry LoadRunner web settings', () => {
  const rows = runtimeSettingRows(draft('java_vuser', { http: { connect_timeout_ms: 120000 }, java_vuser: { classpath: ['lib/a.jar'] } } as ScriptDefinition['config']))['Additional APEX settings']
  expect(rows.find(row => row.path === 'config.http.connect_timeout_ms')?.field).toBeDefined()
  expect(rows.find(row => row.path === 'config.java_vuser.classpath')).toMatchObject({ category: 'Java VUser', field: { type: 'lines' } })
 })

 it('opens a review on the protocol family category', () => {
  expect(preferredRuntimeCategory('jms-ibmmq')).toBe('JMS')
  expect(preferredRuntimeCategory('eventhubs')).toBe('Event Hubs')
  expect(preferredRuntimeCategory('java_vuser')).toBe('Java VUser')
  expect(preferredRuntimeCategory('grpc')).toBe('gRPC')
  expect(preferredRuntimeCategory('java_http')).toBe('HTTP')
  expect(preferredRuntimeCategory('http')).toBe('HTTP')
  expect(preferredRuntimeCategory(undefined)).toBe('HTTP')
 })
})

describe('proxy and authentication secret rows', () => {
 it('gives the new secret rows only their own source keys, not every proxy or basic-auth key', () => {
  const script: ScriptDefinition = {
   ...makeScript('http', { base_url: 'https://api.example.test', http: { proxy_mode: 'custom', proxy: { url: 'http://proxy.example.test:8080' } } } as ScriptDefinition['config']),
   metadata: { import: { preserved: {
    runtime_files: { 'default.cfg': {
     Proxy: { ProxyUseProxy: '1', ProxyServer: 'proxy.example.test:8080', ProxyBypass: 'localhost', ProxyUserName: 'proxy-user', ProxyPassword: 'proxy-pass', ProxyPasswordIsEncrypted: '0' },
     Authentication: { UserName: 'basic-user', Password: 'basic-pass' },
    } },
    runtime_setting_dispositions: { 'default.cfg': {
     Proxy: Object.fromEntries(['ProxyUseProxy', 'ProxyServer', 'ProxyBypass', 'ProxyUserName', 'ProxyPassword', 'ProxyPasswordIsEncrypted'].map(key => [key, { status: 'mapped', parity_id: 'proxy', fields: ['config.http.proxy_mode', 'config.http.proxy'] }])),
     Authentication: Object.fromEntries(['UserName', 'Password'].map(key => [key, { status: 'mapped', parity_id: 'authentication', fields: ['config.http.auth'] }])),
    } },
    runtime_settings_parity: [
     { id: 'proxy', area: 'Proxy', concept: 'Proxy', status: 'executed', fields: ['config.http.proxy_mode', 'config.http.proxy'] },
     { id: 'authentication', area: 'Authentication', concept: 'Basic authentication credentials', status: 'executed', fields: ['config.http.auth'] },
    ],
   } } },
  } as ScriptDefinition
  const rows = Object.values(runtimeSettingRows(script)).flat()
  const row = (path: string) => rows.find(r => r.path === path)
  const proxyPassword = row('config.http.proxy.password')
  expect(proxyPassword?.imported).toContain('ProxyPassword:')
  for (const key of ['ProxyServer', 'ProxyUseProxy', 'ProxyBypass', 'ProxyUserName']) expect(proxyPassword?.imported).not.toContain(`${key}:`)
  expect(proxyPassword?.importedSummary).not.toContain('proxy.example.test')
  for (const token of rows.filter(r => r.path === 'config.http.auth.token')) expect(token.imported).not.toMatch(/UserName:|Password:/)
  const authPassword = row('config.http.auth.password')
  expect(authPassword?.imported).toContain('Password: basic-pass')
  expect(authPassword?.imported).not.toContain('UserName:')
 })
})
