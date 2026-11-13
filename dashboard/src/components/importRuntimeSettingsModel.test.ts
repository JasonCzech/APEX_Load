import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import {
 CANONICAL_RUNTIME_GROUPS,
 CANONICAL_RUNTIME_GROUP_IDS,
 fetchImportRuntimeFields,
 importRuntimeFields,
 mergeImportRuntimeFields,
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

 it('includes 100% of HTTP and SOAP runtime settings across all 7 canonical groups', () => {
  const expectedByGroup: Record<string, string[]> = {
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

  for (const [group, paths] of Object.entries(expectedByGroup)) {
   for (const path of paths) {
    const field = importRuntimeFields.find(candidate => candidate.path === path)
    expect(field, `expected ${path} in catalog`).toBeDefined()
    expect(field?.group).toBe(group)
    expect(field?.subgroup).toBe(resolveRuntimeSubgroup(path, field?.category))
   }
  }

  const httpVersion = importRuntimeFields.find(field => field.path === 'config.http.version')
  expect(httpVersion?.options).toEqual(['', '1.1', '2', 'auto+h3', '3'])

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

 it('merges remote fields from GET /api/v1/scripts/import/settings/fields while skipping secrets and non-scalars', async () => {
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
  expect(merged.find(field => field.path === 'config.http.auth.password')).toBeUndefined()
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

describe('import runtime settings contract', () => {
  // SettingValue renders its own "Default" (unset) entry first, so a listed ""
  // option would render a second, unlabeled blank row.
  it('lists no empty select option', () => {
    const blank = importRuntimeFields.filter(field => field.options?.some(option => option.trim() === '')).map(field => field.path)
    expect(blank).toEqual([])
  })
})
