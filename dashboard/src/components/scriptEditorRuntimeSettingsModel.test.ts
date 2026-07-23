import { describe, expect, it } from 'vitest'

import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import {
  buildScriptRuntimeSummary,
  cloneScriptRuntimeConfig,
  formatBrowserEmulationSummary,
  formatContentChecks,
  formatDFEChain,
  formatKeyValueLines,
  formatLineList,
  formatRuntimeSettingStatusLabel,
  formatScriptProtocolLabel,
  getImportedRuntimeSectionId,
  getRuntimeProtocolCapabilities,
  httpTransportTabIsAvailable,
  parseContentChecks,
  parseDFEChain,
  parseExactLineList,
  parseKeyValueLines,
  resetScriptRuntimeConfig,
  runtimeOverrideFromScriptConfig,
  runtimeSettingsSectionIsAvailable,
  scriptRuntimeConfigIsDirty,
  summarizeRuntimeSettingsReview,
  syncSOAPEnvelopeNamespace,
  transitionScriptProtocol,
} from './scriptEditorRuntimeSettingsModel'

function runtimeConfig(): ScriptConfig {
  return {
    base_url: 'https://example.test',
    think_time: { type: 'fixed', mean_ms: 0, ignore: false },
    pacing: { type: 'fixed_delay', delay_ms: 0 },
    run_logic: { iterations: 0, global_iterations: 0, max_transactions: 0 },
    error_handling: {
      continue_on_error: false,
      snapshot_on_error: false,
      max_retries: 0,
      retry_delay_ms: 0,
    },
    log: { level: 'standard', on_error_only: false },
    http: {
      connect_timeout_ms: 0,
      keep_alive: false,
      tls: { insecure_skip_verify: false },
    },
    browser: {
      headless: false,
      touch_mode: false,
      client_metrics: false,
      viewport: { width: 0, height: 0 },
    },
    web_runtime: {
      content_checks: [{ text: 'Access Denied', scope: 'any_response', fail_if_found: false }],
      network_simulation: { mode: 'custom', latency_ms: 0, packet_loss_pct: 0 },
      data_formatting: { auto_detect: false, chain: [{ type: 'gzip', options: { mode: 'decode' } }] },
    },
  }
}

describe('scriptEditorRuntimeSettingsModel', () => {
  it('derives protocol capabilities and section availability from one registry', () => {
    expect(getRuntimeProtocolCapabilities('browser')).toMatchObject({ browser: true, httpTransport: true, http: false })
    expect(getRuntimeProtocolCapabilities('java_http')).toMatchObject({ javaHttp: true, httpTransport: true })
    expect(getRuntimeProtocolCapabilities('java_vuser')).toMatchObject({ javaVuser: true, httpTransport: false, requiresBaseURL: false })
    expect(getRuntimeProtocolCapabilities('jms-solace')).toMatchObject({ jms: true, httpTransport: false })
    expect(runtimeSettingsSectionIsAvailable('http', 'soap')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('web_runtime', 'https')).toBe(false)
    expect(runtimeSettingsSectionIsAvailable('web_runtime', 'http')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('grpc', 'http')).toBe(false)
    expect(runtimeSettingsSectionIsAvailable('java_vuser', 'java_vuser')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('java_vuser', 'http')).toBe(false)
    expect(runtimeSettingsSectionIsAvailable('errors', 'grpc')).toBe(true)
  })

  it('gates browser emulation to HTTP-transport protocols (never the browser protocol)', () => {
    expect(getRuntimeProtocolCapabilities('http')).toMatchObject({ browserEmulation: true })
    expect(getRuntimeProtocolCapabilities('soap')).toMatchObject({ browserEmulation: true })
    expect(getRuntimeProtocolCapabilities('java_http')).toMatchObject({ browserEmulation: true })
    expect(getRuntimeProtocolCapabilities('browser')).toMatchObject({ browserEmulation: false })
    expect(getRuntimeProtocolCapabilities('grpc')).toMatchObject({ browserEmulation: false })
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'http')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'soap')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'java_http')).toBe(true)
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'browser')).toBe(false)
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'grpc')).toBe(false)
    expect(runtimeSettingsSectionIsAvailable('browser_emulation', 'java_vuser')).toBe(false)
  })

  it('shows the Playwright browser sub-tab only for browser-protocol scripts', () => {
    expect(httpTransportTabIsAvailable('browser', 'browser')).toBe(true)
    expect(httpTransportTabIsAvailable('browser', 'http')).toBe(false)
    expect(httpTransportTabIsAvailable('browser', 'soap')).toBe(false)
    expect(httpTransportTabIsAvailable('connection', 'http')).toBe(true)
    expect(httpTransportTabIsAvailable('timeouts', 'browser')).toBe(true)
  })

  it('summarizes browser emulation for the nav button', () => {
    expect(formatBrowserEmulationSummary(undefined)).toBe('Engine default')
    expect(formatBrowserEmulationSummary({})).toBe('Engine default')
    expect(formatBrowserEmulationSummary({ mode: 'custom', custom_user_agent: 'X/1' })).toBe('Custom UA')
    expect(formatBrowserEmulationSummary({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'windows' })).toBe('chrome 126 · windows')
    expect(formatBrowserEmulationSummary({ mode: 'specific', browser_type: 'safari' })).toBe('safari')
  })

  it('preserves explicit false and zero values instead of treating them as inheritance', () => {
    const override = runtimeOverrideFromScriptConfig(runtimeConfig())
    expect(override.iterations).toBe(0)
    expect(override.think_time).toMatchObject({ mean_ms: 0, ignore: false })
    expect(override.error_handling).toMatchObject({
      continue_on_error: false,
      snapshot_on_error: false,
      max_retries: 0,
      retry_delay_ms: 0,
    })
    expect(override.http).toMatchObject({ connect_timeout_ms: 0, keep_alive: false })
    expect(override.browser_config).toMatchObject({ headless: false, touch_mode: false })

    const summary = buildScriptRuntimeSummary({ config: runtimeConfig() })
    expect(summary.runLogicLabel).toBe('0 shared iterations')
    expect(summary.thinkTimeLabel).toBe('fixed / 0s')
  })

  it('normalizes protocol defaults without overwriting explicit false and zero settings', () => {
    const script: ScriptDefinition = {
      name: 'Protocol draft',
      version: '1.0.0',
      protocol: 'http',
      config: {
        base_url: '',
        network: { port: 0, timeout_ms: 0, tls: false },
        websocket: {
          read_timeout_ms: 0,
          write_timeout_ms: 0,
          max_message_size: 0,
          compression: false,
          reconnect: false,
        },
      },
      actions: [],
    }

    const network = transitionScriptProtocol(script, 'network')
    expect(network.script.config.network).toMatchObject({ port: 0, timeout_ms: 0, tls: false })
    expect(network.script.actions[0]).toMatchObject({ name: 'Network_Health' })
    expect(network.selectedFirstAction).toBe(true)

    const websocket = transitionScriptProtocol(script, 'websocket')
    expect(websocket.script.config.websocket).toMatchObject({
      read_timeout_ms: 0,
      write_timeout_ms: 0,
      max_message_size: 0,
      compression: false,
      reconnect: false,
    })
    expect(script.actions).toEqual([])
  })

  it('round-trips advanced text sections without losing false flags or DFE options', () => {
    const checks = runtimeConfig().web_runtime?.content_checks
    expect(parseContentChecks(formatContentChecks(checks))).toEqual(checks)

    const chain = runtimeConfig().web_runtime?.data_formatting?.chain
    expect(parseDFEChain(formatDFEChain(chain))).toEqual(chain)

    const values = { env: 'perf', empty: '', region: 'east' }
    expect(parseKeyValueLines(formatKeyValueLines(values))).toEqual(values)

    const exactTokens = ['-Xmx512m', '-Dlabel=hello world ']
    expect(parseExactLineList(formatLineList(exactTokens))).toEqual(exactTokens)

    expect(syncSOAPEnvelopeNamespace('1.2', {
      soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
      custom: 'urn:custom',
    }, '1.1')).toEqual({
      soapenv: 'http://www.w3.org/2003/05/soap-envelope',
      custom: 'urn:custom',
    })
    expect(syncSOAPEnvelopeNamespace('1.2', { soapenv: 'urn:owned' }, '1.1')).toEqual({ soapenv: 'urn:owned' })
  })

  it('clones, detects dirty runtime edits, and resets to an isolated baseline', () => {
    const baseline = runtimeConfig()
    const working = cloneScriptRuntimeConfig(baseline)
    expect(scriptRuntimeConfigIsDirty(working, baseline)).toBe(false)

    working.http = { ...working.http, keep_alive: true }
    expect(scriptRuntimeConfigIsDirty(working, baseline)).toBe(true)

    const reset = resetScriptRuntimeConfig(baseline)
    expect(reset).toEqual(baseline)
    expect(reset).not.toBe(baseline)
    reset.error_handling!.continue_on_error = true
    expect(baseline.error_handling?.continue_on_error).toBe(false)

    const serialized = JSON.parse(JSON.stringify(reset)) as ScriptConfig
    expect(serialized.error_handling?.continue_on_error).toBe(true)
    expect(serialized.error_handling?.snapshot_on_error).toBe(false)
    expect(serialized.run_logic?.iterations).toBe(0)
  })

  it('normalizes validation findings and import-review presentation', () => {
    const summary = summarizeRuntimeSettingsReview({
      status: 'review_required',
      findings: [
        { id: 'http', scope: 'script', status: 'partial', severity: 'warning', summary: 'Proxy needs review' },
        { id: 'jms', scope: 'script', status: 'blocked', severity: 'error', summary: 'Broker unavailable' },
      ],
      warnings: ['Imported timeout was clamped'],
    })
    expect(summary.status).toBe('review_required')
    expect(summary.blocking.map(finding => finding.id)).toEqual(['jms'])
    expect(summary.summary).toBe('1 blocking runtime finding')
    expect(summary.warnings).toEqual(['Imported timeout was clamped'])

    expect(getImportedRuntimeSectionId('Java over HTTP')).toBe('java_http')
    expect(getImportedRuntimeSectionId('Java VM')).toBe('java_vuser')
    expect(getImportedRuntimeSectionId('Java Environment Settings')).toBe('java_vuser')
    expect(getImportedRuntimeSectionId('Web HTTP/HTML')).toBe('web_runtime')
    expect(getImportedRuntimeSectionId('unknown')).toBeNull()
    expect(formatRuntimeSettingStatusLabel('compatibility_only')).toBe('Compatibility-only')
    expect(formatScriptProtocolLabel('java_vuser')).toBe('Java Vuser')
  })
})
