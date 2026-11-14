import { describe, expect, it } from 'vitest'

import {
  RUNTIME_OVERRIDE_SCHEMA,
  RUNTIME_PROTOCOL_OVERRIDE_SECTIONS,
  isRuntimeProtocolSectionApplicable,
  applyRuntimeOverrideDiff,
  applyRuntimeOverrideEdit,
  effectiveRuntimeValue,
  formatAutoTransactions,
  formatBrowserEmulation,
  formatEffectiveRuntimeField,
  formatErrorHandling,
  formatHTTP,
  formatHTTPVersion,
  formatJavaVuserSummary,
  formatLog,
  formatPacing,
  formatThinkTime,
  isFieldOverridden,
  isPartialRuntimeDraft,
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  legacyRuntimeOverrideFields,
  legacyRuntimeOverridePayload,
  normalizeRuntimeOverrideFieldPath,
  normalizeRuntimeOverrideFields,
  overlayRuntimeOverrideSection,
  overrideFromRuntimeDraft,
  partialRuntimeOverrideFromDraft,
  resetOverrideField,
  resolveRuntimeFieldSource,
  resolveRuntimeRowsValue,
  runtimeBrowserSettings,
  resolveRuntimeOverrideFields,
  runtimeDraftFromOverride,
  runtimeOverrideFromScriptConfig,
  setOverrideField,
  effectiveRuntimeSection,
  overrideFieldValue,
  runtimeOverrideFieldPaths,
  runtimeRowCoversPath,
  runtimeSectionSource,
  upgradeRuntimeDraftToPartial,
} from './runtimeSettingsModel'
import { type PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { materializeRuntimeSettingsForApply } from './runtimeSettingsApply'

describe('runtimeSettingsModel', () => {
  it('resolves the source group → scenario → script', () => {
    expect(resolveRuntimeFieldSource('debug', 'standard')).toEqual({ source: 'group', hasGroupOverride: true })
    expect(resolveRuntimeFieldSource(undefined, 'standard')).toEqual({ source: 'scenario', hasGroupOverride: false })
    expect(resolveRuntimeFieldSource(undefined, undefined)).toEqual({ source: 'script', hasGroupOverride: false })
    // empty string is treated as absent (not an override)
    expect(resolveRuntimeFieldSource('', 'standard')).toEqual({ source: 'scenario', hasGroupOverride: false })
  })

  it('mirrors the backend protocol applicability (browser only for browser)', () => {
    expect(isRuntimeFieldApplicable('browser', 'browser')).toBe(true)
    expect(isRuntimeFieldApplicable('browser', 'BROWSER')).toBe(true)
    expect(isRuntimeFieldApplicable('browser', 'http')).toBe(false)
    expect(isRuntimeFieldApplicable('browser', undefined)).toBe(false)
    expect(isRuntimeFieldApplicable('pacing', 'http')).toBe(true)
    expect(isRuntimeFieldApplicable('log_level', 'jms')).toBe(true)
    expect(isRuntimeFieldApplicable('java_vuser', 'java_vuser')).toBe(true)
    expect(isRuntimeFieldApplicable('java_vuser', 'http')).toBe(false)
    expect(isRuntimeFieldApplicable('java_vuser', undefined)).toBe(false)
  })

  it('applies browser_emulation to HTTP-transport protocols only', () => {
    expect(isRuntimeFieldApplicable('browser_emulation', 'http')).toBe(true)
    expect(isRuntimeFieldApplicable('browser_emulation', 'soap')).toBe(true)
    expect(isRuntimeFieldApplicable('browser_emulation', 'java_http')).toBe(true)
    expect(isRuntimeFieldApplicable('browser_emulation', 'browser')).toBe(false)
    expect(isRuntimeFieldApplicable('browser_emulation', 'grpc')).toBe(false)
    expect(isRuntimeFieldApplicable('browser_emulation', undefined)).toBe(false)
  })

  it('formats and round-trips a browser emulation override', () => {
    expect(formatBrowserEmulation(undefined)).toBeNull()
    expect(formatBrowserEmulation({ mode: 'custom', custom_user_agent: 'X/1' })).toBe('Custom UA')
    expect(formatBrowserEmulation({
      mode: 'specific',
      browser_type: 'chrome',
      browser_version: '126',
      platform: 'windows',
      language: 'en-US',
      cache: { simulate_cache: true, new_user_each_iteration: true, simulate_prefetch: true },
    })).toBe('chrome 126 · windows · en-US · cache · new user/iter · prefetch')
    expect(formatBrowserEmulation({})).toBe('configured')

    const draft = runtimeDraftFromOverride({
      browser_emulation: { mode: 'specific', browser_type: 'firefox', cache: { download_non_html_resources: true } },
    })
    expect(draft?.browser_emulation?.browser_type).toBe('firefox')
    const roundTripped = overrideFromRuntimeDraft(draft)
    expect(roundTripped?.browser_emulation).toEqual({
      mode: 'specific',
      browser_type: 'firefox',
      cache: { download_non_html_resources: true },
    })
    // A draft carrying only browser_emulation is not empty.
    expect(overrideFromRuntimeDraft({
      log_level: '',
      auto_transactions: '',
      browser_emulation: { mode: 'custom', custom_user_agent: 'Y/2' },
    })).toBeDefined()
  })

  it('resolves the effective browser emulation across layers', () => {
    const scriptLayer = runtimeOverrideFromScriptConfig({
      base_url: 'https://example.test',
      browser_emulation: { mode: 'custom', custom_user_agent: 'Script/1.0' },
    })
    const resolved = formatEffectiveRuntimeField('browser_emulation', undefined, undefined, scriptLayer)
    expect(resolved).toEqual({ value: 'Custom UA', source: 'script' })
    const groupWins = formatEffectiveRuntimeField(
      'browser_emulation',
      { browser_emulation: { mode: 'specific', browser_type: 'edge' } },
      undefined,
      scriptLayer,
    )
    expect(groupWins).toEqual({ value: 'edge', source: 'group' })
    const engineFallback = formatEffectiveRuntimeField('browser_emulation', undefined, undefined, undefined)
    expect(engineFallback).toEqual({ value: 'None', source: 'engine' })
  })

  it('attributes a v1 layer\'s whole section to that layer even when it formats empty', () => {
    type Row = Parameters<typeof formatEffectiveRuntimeField>[1]
    const script = { pacing: { type: 'fixed_delay', delay_ms: 500 }, error_handling: { continue_on_error: true }, think_time: { type: 'fixed', mean_ms: 750 } } as Row
    const group = { pacing: { delay_ms: 0 }, error_handling: {} } as unknown as Row
    expect(formatEffectiveRuntimeField('pacing', group, undefined, script)).toEqual({ value: 'None', source: 'group' })
    expect(formatEffectiveRuntimeField('error_handling', group, undefined, script)).toEqual({ value: 'Stop on error', source: 'group' })
    // A scenario v1 layer carrying the section wins over the script below it.
    expect(formatEffectiveRuntimeField('pacing', undefined, group, script)).toEqual({ value: 'None', source: 'scenario' })
    // A field the layer does not carry still falls through.
    expect(formatEffectiveRuntimeField('think_time', group, undefined, script)).toEqual({ value: 'fixed · 750ms', source: 'script' })
  })

  it('formats a partial group\'s listed leaves over the inherited section in the Summary', () => {
    type Row = Parameters<typeof formatEffectiveRuntimeField>[1] & { override_fields?: string[] }
    const group = { http: { request_timeout_ms: 5000 }, override_fields: ['http.request_timeout_ms'] } as Row
    const scenario = { http: { version: '2', proxy_mode: 'custom' } } as Row
    const http = formatEffectiveRuntimeField('http', group, scenario)
    expect(http.value).toContain('5000ms timeout')
    expect(http.value).toContain('proxy')
    expect(http.source).toBe('mixed')
    // A group listing the whole section still owns it.
    const whole = formatEffectiveRuntimeField('http', { http: { request_timeout_ms: 5000 }, override_fields: ['http'] } as Row, scenario)
    expect(whole).toEqual({ value: '5000ms timeout', source: 'group' })
    // A compact leaf reads its canonical path.
    expect(formatEffectiveRuntimeField('iterations', { run_logic: { iterations: 5 }, override_fields: ['run_logic.iterations'] } as Row, { iterations: 2 } as Row).source).toBe('group')
    const retries = formatEffectiveRuntimeField('error_handling', { error_handling: { max_retries: 3 }, override_fields: ['error_handling.max_retries'] } as Row, { error_handling: { continue_on_error: true } } as Row)
    expect(retries.source).toBe('mixed')
    expect(retries.value).toMatch(/continue/i)
  })

  it('resolves stored rows per path, partial rows by what they list and v1 rows by section', () => {
    const partial = { error_handling: { continue_on_error: true }, override_fields: ['error_handling.continue_on_error'] }
    const v1 = { error_handling: {} }
    expect(resolveRuntimeRowsValue('error_handling.snapshot_on_error', [partial, { error_handling: { snapshot_on_error: true } }])).toEqual({ index: 1, value: true })
    expect(resolveRuntimeRowsValue('error_handling.snapshot_on_error', [v1, { error_handling: { snapshot_on_error: true } }])).toEqual({ index: 0, value: undefined })
    expect(resolveRuntimeRowsValue('log.level', [{ log: { level: 'debug' }, override_fields: ['log.level'] }])).toEqual({ index: 0, value: 'debug' })
    expect(runtimeBrowserSettings({ browser_config: { device_profile: 'iphone' }, override_fields: ['browser.device_profile'] })).toEqual({ device_profile: 'iphone' })
    expect(runtimeBrowserSettings({ browser: { headless: true } })).toEqual({ headless: true })
  })

  it('formats and round-trips a Java Env override', () => {
    expect(formatJavaVuserSummary(undefined)).toBeNull()
    expect(
      formatJavaVuserSummary({
        jdk_selection: 'specified',
        additional_vm_parameters: ['-Xmx512m', '-Denv=perf'],
        classpath: ['/a.jar'],
        skip_parameter_evaluation: true,
      }),
    ).toBe('Specified JDK · 2 VM params · 1 classpath · skip param eval')

    const override = {
      java_vuser: {
        jdk_selection: 'internal' as const,
        additional_vm_parameters: ['-Xmx1g'],
        skip_parameter_evaluation: true,
      },
    }
    const draft = runtimeDraftFromOverride(override)
    expect(draft?.java_vuser).toEqual(override.java_vuser)
    expect(overrideFromRuntimeDraft(draft)?.java_vuser).toEqual(override.java_vuser)
    // An all-empty java_vuser draft collapses to no override.
    expect(overrideFromRuntimeDraft({ log_level: '', auto_transactions: '', java_vuser: {} })?.java_vuser).toBeUndefined()
  })

  it('formats and round-trips the shared-JVM choice', () => {
    expect(formatJavaVuserSummary({ jvm_sharing: 'shared', vusers_per_jvm: 200 })).toBe('shared JVMs (200 per JVM)')
    expect(formatJavaVuserSummary({ jvm_sharing: 'shared' })).toBe('shared JVMs')
    expect(formatJavaVuserSummary({ jvm_sharing: 'dedicated' })).toBe('one JVM per VUser')
    expect(formatJavaVuserSummary({ jvm_sharing: 'threads', vusers_per_jvm: 50 })).toBe('LoadRunner threads (50 per JVM)')
    const override = { java_vuser: { jvm_sharing: 'shared' as const, vusers_per_jvm: 300 } }
    const draft = runtimeDraftFromOverride(override)
    expect(draft?.java_vuser).toEqual(override.java_vuser)
    expect(overrideFromRuntimeDraft(draft)?.java_vuser).toEqual(override.java_vuser)
  })

  it('formats pacing variants', () => {
    expect(formatPacing({ type: 'none' })).toBe('None')
    expect(formatPacing({ type: 'fixed_delay', delay_ms: 250 })).toBe('Fixed delay · 0.25 seconds')
    expect(formatPacing({ type: 'fixed_interval', interval_ms: 1000 })).toBe('Fixed interval · 1 second')
    expect(formatPacing({ type: 'random_delay', min_delay_ms: 100, max_delay_ms: 300 })).toBe('Random delay · 0.1–0.3 seconds')
    expect(formatPacing({ type: 'random_interval', min_interval_ms: 500, max_interval_ms: 900 })).toBe('Random interval · 0.5–0.9 seconds')
    expect(formatPacing({ type: 'tps', tps: 50 })).toBe('Target TPS · 50')
    expect(formatPacing(undefined)).toBeNull()
  })

  it('formats think time, auto-transactions, and error handling', () => {
    expect(formatThinkTime({ type: 'fixed', mean_ms: 500 })).toBe('fixed · 500ms')
    expect(formatThinkTime({ type: 'recorded', mean_ms: 0, multiplier: 1 })).toBe('Replay recorded think time')
    expect(formatThinkTime({ type: 'fixed', mean_ms: 500, ignore: true })).toBe('Disabled')
    expect(formatAutoTransactions('per_action')).toBe('per action')
    expect(formatErrorHandling({ continue_on_error: true, snapshot_on_error: false })).toBe('Continue on error · No snapshot')
    expect(formatErrorHandling(undefined)).toBeNull()
  })

  it('formats HTTP version selection as auto-negotiated or exact', () => {
    expect(formatHTTPVersion()).toBe('Auto-negotiate (HTTP/2 or HTTP/1.1)')
    expect(formatHTTPVersion('')).toBe('Auto-negotiate (HTTP/2 or HTTP/1.1)')
    expect(formatHTTPVersion('1.1')).toBe('HTTP/1.1 required')
    expect(formatHTTPVersion('2')).toBe('HTTP/2 required (no fallback)')
    expect(formatHTTPVersion('1.0')).toBe('Unsupported HTTP version 1.0')
    expect(formatHTTP({ version: '2', request_timeout_ms: 5000 })).toBe('HTTP/2 required (no fallback) · 5000ms timeout')
  })

  it('formats the effective field value with the resolved source', () => {
    const group = { pacing: { type: 'tps' as const, tps: 100 } }
    const scenario = { pacing: { type: 'fixed_delay' as const, delay_ms: 250 }, log_level: 'standard' }

    // group wins
    expect(formatEffectiveRuntimeField('pacing', group, scenario)).toEqual({ value: 'Target TPS · 100', source: 'group' })
    // falls back to scenario default
    expect(formatEffectiveRuntimeField('log_level', group, scenario)).toEqual({ value: 'standard', source: 'scenario' })
    // when no group/scenario/script/engine layer carries the field, a descriptive
    // engine-default label is shown — never a blank placeholder.
    expect(formatEffectiveRuntimeField('think_time', group, scenario)).toEqual({ value: 'None', source: 'engine' })
    expect(formatEffectiveRuntimeField('iterations', undefined, undefined)).toEqual({ value: 'Duration-driven', source: 'engine' })
  })

  it('attributes section-backed iterations/auto_transactions/log_level to the group override', () => {
    // The Run Logic / Error Handling tab handlers promote these into run_logic/log and
    // clear the legacy top-level field. The summary must still credit the group
    // override rather than fall through to a stale script default.
    const group = {
      run_logic: { iterations: 5, auto_transactions: 'per_action' },
      log: { level: 'extended' },
    }
    const scriptDefault = runtimeOverrideFromScriptConfig({
      base_url: 'https://example.test',
      run_logic: { iterations: 9 },
      log: { level: 'standard' },
    })

    expect(formatEffectiveRuntimeField('iterations', group, undefined, scriptDefault)).toEqual({ value: '5', source: 'group' })
    expect(formatEffectiveRuntimeField('auto_transactions', group, undefined, scriptDefault)).toEqual({ value: 'per action', source: 'group' })
    expect(formatEffectiveRuntimeField('log_level', group, undefined, scriptDefault)).toEqual({ value: 'extended', source: 'group' })
  })

  it('resolves the script-config layer to the actual value below group/scenario', () => {
    const scriptDefault = runtimeOverrideFromScriptConfig({
      base_url: 'https://example.test',
      pacing: { type: 'fixed_delay', delay_ms: 500 },
      think_time: { type: 'fixed', mean_ms: 750 },
      run_logic: { iterations: 12, auto_transactions: 'per_action' },
      log: { level: 'extended' },
    })

    // No group, no scenario → the script's actual config value is shown (not a placeholder).
    expect(formatEffectiveRuntimeField('pacing', undefined, undefined, scriptDefault)).toEqual({ value: 'Fixed delay · 0.5 seconds', source: 'script' })
    expect(formatEffectiveRuntimeField('think_time', undefined, undefined, scriptDefault)).toEqual({ value: 'fixed · 750ms', source: 'script' })
    expect(formatEffectiveRuntimeField('iterations', undefined, undefined, scriptDefault)).toEqual({ value: '12', source: 'script' })
    expect(formatEffectiveRuntimeField('auto_transactions', undefined, undefined, scriptDefault)).toEqual({ value: 'per action', source: 'script' })
    expect(formatEffectiveRuntimeField('log_level', undefined, undefined, scriptDefault)).toEqual({ value: 'extended', source: 'script' })

    // Group/scenario still win over the script layer.
    const group = { pacing: { type: 'tps' as const, tps: 40 } }
    expect(formatEffectiveRuntimeField('pacing', group, undefined, scriptDefault)).toEqual({ value: 'Target TPS · 40', source: 'group' })
    // A field neither the script config nor engine default sets falls through to a
    // descriptive engine-default label (never a blank placeholder).
    expect(formatEffectiveRuntimeField('web_runtime', undefined, undefined, scriptDefault)).toEqual({ value: 'None', source: 'engine' })
  })

  it('resolves the engine-default layer below the script layer (never blank)', () => {
    const engineDefault = runtimeOverrideFromScriptConfig({
      base_url: '',
      http: { request_timeout_ms: 60000, tls: { min_version: '1.2' } },
      pacing: { type: 'none' },
      log: { level: 'standard' },
    })
    // Engine config value shows when group/scenario/script are absent, attributed to 'engine'.
    expect(formatEffectiveRuntimeField('http', undefined, undefined, undefined, engineDefault)).toEqual({ value: expect.stringContaining('TLS 1.2'), source: 'engine' })
    expect(formatEffectiveRuntimeField('pacing', undefined, undefined, undefined, engineDefault)).toEqual({ value: 'None', source: 'engine' })
    expect(formatEffectiveRuntimeField('log_level', undefined, undefined, undefined, engineDefault)).toEqual({ value: 'standard', source: 'engine' })
    // error_handling has no concrete engine value (omitempty) → descriptive fallback.
    expect(formatEffectiveRuntimeField('error_handling', undefined, undefined, undefined, engineDefault)).toEqual({ value: 'Stop on error', source: 'engine' })
  })

  it('projects a script config into the override shape (browser + run_logic mapping)', () => {
    expect(runtimeOverrideFromScriptConfig(undefined)).toBeUndefined()

    const projected = runtimeOverrideFromScriptConfig({
      base_url: 'https://example.test',
      run_logic: { global_iterations: 7 },
      browser: { headless: true, device_profile: 'pixel_8', orientation: 'portrait', engine: 'chromium' },
    })
    // global_iterations is a fleet-wide shared cap, not the per-VUser iteration
    // count, so it is NOT projected into the iterations field; it stays in run_logic
    // (surfaced via the Run Logic row, not conflated into Iterations).
    expect(projected!.iterations).toBeUndefined()
    expect(projected!.run_logic?.global_iterations).toBe(7)
    // browser device summary picks the 4-field subset; browser_config carries the full config.
    expect(projected!.browser).toEqual({ headless: true, device_profile: 'pixel_8', touch_mode: undefined, orientation: 'portrait' })
    expect(projected!.browser_config?.engine).toBe('chromium')
    // web_runtime has no script-config equivalent.
    expect(projected!.web_runtime).toBeUndefined()
  })

  it('round-trips full error_handling and preserves log/run_logic sections', () => {
    const override = {
      error_handling: { continue_on_error: true, max_retries: 3, retry_delay_ms: 250, retry_backoff: 'exponential' },
      log: { level: 'extended', on_error_only: true, extended_options: ['advanced_trace'] },
      run_logic: { workload_model: 'open', global_iterations: 10 },
    }
    const draft = runtimeDraftFromOverride(override)
    expect(draft).toBeDefined()
    expect(draft!.max_retries).toBe(3)
    expect(draft!.retry_delay_ms).toBe(250)
    expect(draft!.retry_backoff).toBe('exponential')
    expect(draft!.log).toEqual(override.log)
    expect(draft!.run_logic).toEqual(override.run_logic)

    const back = overrideFromRuntimeDraft(draft)
    expect(back).toBeDefined()
    expect(back!.error_handling).toEqual({ continue_on_error: true, max_retries: 3, retry_delay_ms: 250, retry_backoff: 'exponential' })
    // log/run_logic survive a UI round-trip even though there is no editor for them yet.
    expect(back!.log).toEqual(override.log)
    expect(back!.run_logic).toEqual(override.run_logic)
  })

  it('formats redact_run_evidence as a tri-state so an explicit false is never an empty layer', () => {
    // Absent means "engine default (on)" — nothing was overridden, so nothing is summarized.
    expect(formatLog({ level: 'extended' })).toBe('extended')
    expect(formatLog({})).toBeNull()
    // Explicit true AND explicit false both format. A truthiness guard would make a
    // log layer whose only content is `redact_run_evidence: false` format to null.
    expect(formatLog({ redact_run_evidence: true })).toBe('redact run evidence')
    expect(formatLog({ redact_run_evidence: false })).toBe('no run-evidence redaction')
    expect(formatLog({ level: 'extended', redact_run_evidence: false })).toBe('extended · no run-evidence redaction')
  })

  it('attributes an explicit redact_run_evidence=false to the layer that set it', () => {
    // The group turned run-evidence redaction OFF; the script only pins a log level.
    // If formatLog dropped the false, the group layer would read as empty and the
    // effective value would be mis-attributed to the script.
    expect(formatEffectiveRuntimeField(
      'log',
      { log: { redact_run_evidence: false } },
      undefined,
      { log: { level: 'extended' } },
    )).toEqual({ value: 'no run-evidence redaction', source: 'group' })
  })

  it('round-trips an explicit redact_run_evidence=false through the draft model', () => {
    const override = { log: { level: 'standard', redact_run_evidence: false } }
    const draft = runtimeDraftFromOverride(override)
    expect(draft!.log).toEqual({ level: 'standard', redact_run_evidence: false })
    // The explicit false must survive as false — not be dropped back to undefined,
    // which would silently re-enable redaction on save.
    expect(overrideFromRuntimeDraft(draft)!.log).toEqual({ level: 'standard', redact_run_evidence: false })
  })

  it('distinguishes inherited error handling from explicit false overrides', () => {
    expect(runtimeDraftFromOverride({ error_handling: { max_retries: 2 } })).toEqual(expect.objectContaining({
      continue_on_error: undefined,
      snapshot_on_error: undefined,
      max_retries: 2,
    }))
    expect(overrideFromRuntimeDraft({
      log_level: '',
      auto_transactions: '',
      continue_on_error: false,
      snapshot_on_error: false,
    })).toEqual({
      error_handling: { continue_on_error: false, snapshot_on_error: false },
    })
    expect(overrideFromRuntimeDraft({ log_level: '', auto_transactions: '' })).toBeUndefined()
  })

  it('preserves all 9 ErrorHandlingConfig fields across runtimeDraftFromOverride and overrideFromRuntimeDraft', () => {
    const fullErrorHandling = {
      continue_on_error: false,
      snapshot_on_error: true,
      fail_transaction_on_error_message: true,
      max_retries: 4,
      retry_delay_ms: 500,
      retry_backoff: 'exponential',
      retry_on: ['5xx', '429'],
      honor_retry_after: true,
      retry_after_max_ms: 15000,
    }
    const draft = runtimeDraftFromOverride({ error_handling: fullErrorHandling })
    expect(draft).toEqual(expect.objectContaining(fullErrorHandling))
    expect(isRuntimeDraftEmpty({
      log_level: '',
      auto_transactions: '',
      fail_transaction_on_error_message: true,
    })).toBe(false)
    expect(isRuntimeDraftEmpty({
      log_level: '',
      auto_transactions: '',
      retry_on: ['5xx'],
    })).toBe(false)
    expect(isRuntimeDraftEmpty({
      log_level: '',
      auto_transactions: '',
      honor_retry_after: false,
    })).toBe(false)
    expect(isRuntimeDraftEmpty({
      log_level: '',
      auto_transactions: '',
      retry_after_max_ms: 5000,
    })).toBe(false)
    expect(overrideFromRuntimeDraft(draft)).toEqual({
      error_handling: fullErrorHandling,
    })
  })

  it('materializes data_formatting.auto_detect=false over a base with auto_detect=true', () => {
    const materialized = materializeRuntimeSettingsForApply(
      undefined,
      { base_url: '', web_runtime: { data_formatting: { auto_detect: true } } },
      runtimeDraftFromOverride({ web_runtime: { data_formatting: { auto_detect: false } } }),
    )
    expect(materialized?.web_runtime?.data_formatting?.auto_detect).toBe(false)
  })
})

describe('partial runtime overrides (override_fields)', () => {
  const REDACTED = '[REDACTED]'

  it('keeps the override-section structure in step with docs/dsl.schema.json', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const schema = JSON.parse(readFileSync(`${cwd}/../docs/dsl.schema.json`, 'utf8') as string) as {
      $defs: Record<string, { type?: string; properties?: Record<string, { $ref?: string; type?: string }> }>
    }
    const defs = schema.$defs
    const kind = (property: { $ref?: string; type?: string }): string => {
      if (property.$ref) {
        const name = property.$ref.split('/').pop() as string
        const def = defs[name]
        if (def.type === 'object' && def.properties) return name
        return def.type === 'object' ? '*' : ''
      }
      return property.type === 'object' ? '*' : ''
    }
    const expected: Record<string, string> = {}
    const walk = (name: string) => {
      if (name in expected) return
      expected[name] = ''
      const properties = defs[name].properties ?? {}
      expected[name] = Object.keys(properties).sort().map(field => {
        const child = kind(properties[field])
        if (child && child !== '*') walk(child)
        return child ? `${field}:${child}` : field
      }).join(' ')
    }
    const sections = ['base_url', 'results', 'websocket', 'pacing', 'think_time', 'error_handling', 'log', 'run_logic', 'http', 'web_runtime', 'soap', 'browser', 'browser_emulation', 'kafka', 'jms', 'eventhubs', 'grpc', 'database', 'network', 'appium', 'java_http']
    const root = sections.map(section => {
      const child = kind(defs.ScriptConfig.properties![section])
      if (child && child !== '*') walk(child)
      return child ? `${section}:${child}` : section
    })
    expect(RUNTIME_OVERRIDE_SCHEMA).toEqual({ ScriptConfig: root.join(' '), ...expected })
  })

  it('normalizes paths exactly as normalizeRuntimeOverrideFields does', () => {
    const cases: Array<[string, string | undefined]> = [
      ['iterations', 'run_logic.iterations'],
      ['log_level', 'log.level'],
      ['auto_transactions', 'run_logic.auto_transactions'],
      ['browser_config', 'browser'],
      ['browser_config.headless', 'browser.headless'],
      ['config.http.request_timeout_ms', 'http.request_timeout_ms'],
      [' HTTP.TLS.Min_Version ', 'http.tls.min_version'],
      ['http', 'http'],
      ['env', 'env'],
      ['env.API_Key', 'env.API_Key'],
      ['redaction_rules', 'redaction_rules'],
      ['java_vuser', 'java_vuser'],
      ['java_vuser.classpath', 'java_vuser.classpath'],
      ['http.default_headers', 'http.default_headers'],
      // A map key keeps its case and any dots, and is not walked.
      ['http.default_headers.X-Trace.Id', 'http.default_headers.X-Trace.Id'],
      ['soap.ws_security.username_token.password', 'soap.ws_security.username_token.password'],
      ['web_runtime.network_simulation.latency_ms', 'web_runtime.network_simulation.latency_ms'],
      // A list is a whole leaf: no index paths below it.
      ['http.proxy.no_proxy', 'http.proxy.no_proxy'],
      ['http.proxy.no_proxy.0', undefined],
      ['http.request_timeout_ms.x', undefined],
      ['java_vuser.jdk_home.x', undefined],
      ['java_vuser.runner_command', undefined],
      ['jmeter', undefined],
      ['http.nope', undefined],
      ['env.', undefined],
      ['config.', undefined],
      ['', undefined],
    ]
    for (const [raw, canonical] of cases) {
      expect([raw, normalizeRuntimeOverrideFieldPath(raw)]).toEqual([raw, canonical])
    }
    expect(normalizeRuntimeOverrideFields(['iterations', 'run_logic.iterations', 'log_level'])).toEqual(['run_logic.iterations', 'log.level'])
  })

  it('lists what a v1 row applies, letting a section beat its compact alias', () => {
    expect(legacyRuntimeOverrideFields({
      log: { on_error_only: true },
      log_level: 'debug',
      run_logic: { workload_model: 'open' },
      iterations: 4,
      browser_config: { engine: 'webkit' },
      browser: { headless: true },
      http: { request_timeout_ms: 1 },
      env: { A: '1' },
    })).toEqual(['log', 'run_logic', 'http', 'browser', 'env'])
    expect(legacyRuntimeOverrideFields({
      log_level: 'debug',
      iterations: 4,
      auto_transactions: 'per_action',
      browser: { headless: false, device_profile: 'pixel_8' },
      web_runtime: { network_simulation: { latency_ms: 0 }, content_checks: [{ text: 'ok' }], data_formatting: { auto_detect: false } },
      java_vuser: { use_xbootclasspath: false, vusers_per_jvm: 0 },
    })).toEqual([
      'log.level',
      'run_logic.iterations',
      'run_logic.auto_transactions',
      'web_runtime.content_checks',
      'browser.headless',
      'browser.device_profile',
      'java_vuser.use_xbootclasspath',
    ])
  })

  it('lists one HTTP leaf for an edit on an empty draft and sends only that leaf', () => {
    const draft = setOverrideField(undefined, 'http.request_timeout_ms', 5000)
    expect(draft.override_fields).toEqual(['http.request_timeout_ms'])
    expect(partialRuntimeOverrideFromDraft(draft)).toEqual({
      http: { request_timeout_ms: 5000 },
      override_fields: ['http.request_timeout_ms'],
    })
  })

  it('converts a stored v1 row on first edit without changing what it applies', () => {
    const stored = runtimeDraftFromOverride({
      http: { request_timeout_ms: 100, tls: { min_version: '1.2' } },
      log: { level: 'debug' },
      log_level: 'standard',
      iterations: 3,
      pacing: { type: 'fixed_delay', delay_ms: 250 },
    })
    const edited = setOverrideField(stored, 'error_handling.max_retries', 2)
    expect(edited.override_fields).toEqual(['pacing', 'log', 'run_logic.iterations', 'http', 'error_handling.max_retries'])
    // The shadowed log_level alias is dropped: v1 let the log section win.
    expect(partialRuntimeOverrideFromDraft(edited)).toEqual({
      pacing: { type: 'fixed_delay', delay_ms: 250 },
      log: { level: 'debug' },
      run_logic: { iterations: 3 },
      http: { request_timeout_ms: 100, tls: { min_version: '1.2' } },
      error_handling: { max_retries: 2 },
      override_fields: ['pacing', 'log', 'run_logic.iterations', 'http', 'error_handling.max_retries'],
    })
    // A path inside an already-listed section is covered, not listed twice.
    expect(setOverrideField(edited, 'http.request_timeout_ms', 9).override_fields).toEqual(edited.override_fields)
  })

  it('leaves a v1 row nobody edits as v1', () => {
    const stored = runtimeDraftFromOverride({ http: { request_timeout_ms: 100 }, iterations: 3 })
    expect(isPartialRuntimeDraft(stored)).toBe(false)
    expect(overrideFromRuntimeDraft(stored)).not.toHaveProperty('override_fields')
    expect(legacyRuntimeOverridePayload(stored!)).toEqual({ http: { request_timeout_ms: 100 }, iterations: 3 })
  })

  it('resets one field to inherited while keeping every other override in its section', () => {
    let draft = setOverrideField(undefined, 'http.request_timeout_ms', 5000)
    draft = setOverrideField(draft, 'http.tls.min_version', '1.3')
    const reset = resetOverrideField(draft, 'http.request_timeout_ms')
    expect(reset.override_fields).toEqual(['http.tls.min_version'])
    expect(partialRuntimeOverrideFromDraft(reset)).toEqual({ http: { tls: { min_version: '1.3' } }, override_fields: ['http.tls.min_version'] })
    // Resetting the last field leaves nothing overridden.
    expect(isRuntimeDraftEmpty(resetOverrideField(reset, 'http.tls.min_version'))).toBe(true)
  })

  it('splits a listed section into its other fields when one field inside it is reset', () => {
    const stored = runtimeDraftFromOverride({ error_handling: { continue_on_error: false, max_retries: 2 } })
    const reset = resetOverrideField(stored, 'error_handling.continue_on_error')
    expect(reset.override_fields).toEqual([
      'error_handling.fail_transaction_on_error_message',
      'error_handling.honor_retry_after',
      'error_handling.max_retries',
      'error_handling.retry_after_max_ms',
      'error_handling.retry_backoff',
      'error_handling.retry_delay_ms',
      'error_handling.retry_on',
      'error_handling.snapshot_on_error',
    ])
    expect(partialRuntimeOverrideFromDraft(reset)?.error_handling).toEqual({ max_retries: 2 })
    expect(reset.continue_on_error).toBeUndefined()
    // Nested: resetting a TLS leaf of a listed http section keeps http's other
    // fields and TLS's other fields listed.
    const http = resetOverrideField(runtimeDraftFromOverride({ http: { tls: { min_version: '1.2', ca_cert_file: 'ca.pem' } } }), 'http.tls.min_version')
    expect(http.override_fields).toContain('http.request_timeout_ms')
    expect(http.override_fields).toContain('http.tls.ca_cert_file')
    expect(http.override_fields).not.toContain('http.tls')
    expect(http.override_fields).not.toContain('http.tls.min_version')
    // env and map keys split into the other keys present.
    const env = resetOverrideField(runtimeDraftFromOverride({ env: { A: '1', B: '2' } }), 'env.A')
    expect(env.override_fields).toEqual(['env.B'])
    expect(partialRuntimeOverrideFromDraft(env)).toEqual({ env: { B: '2' }, override_fields: ['env.B'] })
  })

  it('lists explicit true and false and treats an unset tri-state as inherited', () => {
    const disabled = setOverrideField(undefined, 'error_handling.continue_on_error', false)
    expect(disabled.continue_on_error).toBe(false)
    expect(isFieldOverridden(disabled, 'error_handling.continue_on_error')).toBe(true)
    expect(partialRuntimeOverrideFromDraft(disabled)).toEqual({ error_handling: { continue_on_error: false }, override_fields: ['error_handling.continue_on_error'] })
    const inherited = resetOverrideField(disabled, 'error_handling.continue_on_error')
    expect(isFieldOverridden(inherited, 'error_handling.continue_on_error')).toBe(false)
    expect(inherited.continue_on_error).toBeUndefined()
    // A listed path with no value overrides to empty (the backend's zero value).
    const cleared = setOverrideField(undefined, 'http.keep_alive', undefined)
    expect(isFieldOverridden(cleared, 'http.keep_alive')).toBe(true)
    expect(partialRuntimeOverrideFromDraft(cleared)).toEqual({ override_fields: ['http.keep_alive'] })
  })

  it('keeps a stored masked secret listed and sent unchanged so the backend restores it', () => {
    // v1 row: the whole http section is listed on conversion, so the masked
    // password is sent as "[REDACTED]" and reconcileHTTPSecrets restores it.
    const v1 = runtimeDraftFromOverride({ http: { auth: { type: 'basic', username: REDACTED, password: REDACTED } } })
    const v1Edited = setOverrideField(v1, 'http.request_timeout_ms', 5000)
    expect(partialRuntimeOverrideFromDraft(v1Edited)).toEqual({
      http: { auth: { type: 'basic', username: REDACTED, password: REDACTED }, request_timeout_ms: 5000 },
      override_fields: ['http'],
    })
    // Partial row: a listed secret leaf stays listed and masked when a sibling changes.
    const partial = runtimeDraftFromOverride({
      http: { auth: { password: REDACTED }, proxy: { url: 'http://proxy' } },
      override_fields: ['http.auth.password', 'http.proxy.url'],
    })
    const edited = setOverrideField(partial, 'http.request_timeout_ms', 5000)
    expect(partialRuntimeOverrideFromDraft(edited)).toEqual({
      http: { auth: { password: REDACTED }, proxy: { url: 'http://proxy' }, request_timeout_ms: 5000 },
      override_fields: ['http.auth.password', 'http.proxy.url', 'http.request_timeout_ms'],
    })
    // A value at an unlisted path is never applied (mergeRuntimeSettingsPartial
    // drops it before sealing), so it is not sent.
    const unlisted = runtimeDraftFromOverride({ http: { auth: { password: REDACTED } }, override_fields: ['http.request_timeout_ms'] })
    expect(partialRuntimeOverrideFromDraft(unlisted)).toEqual({ override_fields: ['http.request_timeout_ms'] })
    // A typed credential is an ordinary listed value; the backend seals it.
    const typed = setOverrideField(undefined, 'soap.ws_security.username_token.password', 'hunter2')
    expect(partialRuntimeOverrideFromDraft(typed)).toEqual({
      soap: { ws_security: { username_token: { password: 'hunter2' } } },
      override_fields: ['soap.ws_security.username_token.password'],
    })
  })

  it('reads a stored partial row the way resolveEffectiveOverrideFields does', () => {
    expect(resolveRuntimeOverrideFields({
      env: { A: '1' },
      iterations: 3,
      browser: { headless: true },
      override_fields: ['http.request_timeout_ms'],
    })).toEqual(['http.request_timeout_ms', 'env', 'run_logic.iterations', 'browser.headless'])
    // A compact alias fills its section's zero value under partial semantics.
    const draft = runtimeDraftFromOverride({ iterations: 3, run_logic: { workload_model: 'open' }, override_fields: ['run_logic.workload_model'] })
    expect(partialRuntimeOverrideFromDraft(draft)).toEqual({
      run_logic: { workload_model: 'open', iterations: 3 },
      override_fields: ['run_logic.workload_model', 'run_logic.iterations'],
    })
  })

  it('applies a section patch leaf by leaf and a wholesale section by its changed leaves', () => {
    const patched = applyRuntimeOverrideEdit(undefined, 'http', { tls: { min_version: '1.3' }, keep_alive: false, default_headers: { 'X-A': '1' } })
    expect(patched.override_fields).toEqual(['http.tls.min_version', 'http.keep_alive', 'http.default_headers'])
    const reverted = applyRuntimeOverrideEdit(patched, 'http', { keep_alive: undefined })
    expect(reverted.override_fields).toEqual(['http.tls.min_version', 'http.default_headers'])
    const diffed = applyRuntimeOverrideDiff(undefined, 'java_vuser', { classpath: ['a.jar'], jvm_sharing: 'shared' }, { classpath: ['a.jar'], jvm_sharing: 'dedicated' })
    expect(partialRuntimeOverrideFromDraft(diffed)).toEqual({ java_vuser: { jvm_sharing: 'dedicated' }, override_fields: ['java_vuser.jvm_sharing'] })
  })

  it('resolves effective values with group override, scenario default, script and engine provenance', () => {
    const script = { base_url: '', http: { request_timeout_ms: 300, default_headers: { 'X-Script': '1' } } }
    const engine = { base_url: '', http: { request_timeout_ms: 60000, connect_timeout_ms: 30000, keep_alive: true } }
    const scenario = setOverrideField(undefined, 'http.connect_timeout_ms', 1000)
    const group = setOverrideField(undefined, 'http.keep_alive', false)
    const layers = { group, scenario, script, engine }
    expect(effectiveRuntimeValue('http.keep_alive', layers)).toEqual({ value: false, provenance: 'group_override' })
    expect(effectiveRuntimeValue('http.connect_timeout_ms', layers)).toEqual({ value: 1000, provenance: 'scenario_default' })
    expect(effectiveRuntimeValue('http.default_headers', layers)).toEqual({ value: { 'X-Script': '1' }, provenance: 'script' })
    expect(effectiveRuntimeValue('http.request_timeout_ms', { script: { base_url: '' }, engine })).toEqual({ value: 60000, provenance: 'engine_default' })
    // A v1 layer applies every path its sections cover, even one it leaves empty.
    expect(effectiveRuntimeValue('http.request_timeout_ms', { scenario: { http: { version: '2' } }, script, engine })).toEqual({ value: undefined, provenance: 'scenario_default' })
  })

  it('overlays a section for display: listed leaves over the inherited value', () => {
    const inherited = { request_timeout_ms: 300, tls: { min_version: '1.2', ca_cert_file: 'ca.pem' } }
    const draft = setOverrideField(undefined, 'http.tls.min_version', '1.3')
    expect(overlayRuntimeOverrideSection(inherited, draft, 'http')).toEqual({ request_timeout_ms: 300, tls: { min_version: '1.3', ca_cert_file: 'ca.pem' } })
    // A v1 section replaces the inherited one whole.
    expect(overlayRuntimeOverrideSection(inherited, { log_level: '', auto_transactions: '', http: { version: '2' } }, 'http')).toEqual({ version: '2' })
  })

  it('carries SOAP and override_fields through the draft and the v1 payload', () => {
    const stored = { soap: { version: '1.2' } }
    const draft = runtimeDraftFromOverride(stored)
    expect(draft?.soap).toEqual({ version: '1.2' })
    expect(isRuntimeDraftEmpty(draft)).toBe(false)
    expect(legacyRuntimeOverridePayload(draft!)).toEqual({ soap: { version: '1.2' } })
    expect(overrideFromRuntimeDraft(draft)).toMatchObject({ soap: { version: '1.2' } })
  })
})

describe('v1 rows keep whole-section semantics for empty or untyped sections', () => {
  // Wire rows as the API returns them: omitempty drops a zero `type`.
  const wire = (row: object) => row as PartialRuntimeSettingsOverride
  it('lists an empty v1 error_handling, think_time or untyped pacing whole', () => {
    expect(upgradeRuntimeDraftToPartial(runtimeDraftFromOverride({ error_handling: {} })).override_fields).toContain('error_handling')
    expect(runtimeOverrideFieldPaths({ error_handling: {} })).toEqual(['error_handling'])
    expect(runtimeOverrideFieldPaths(wire({ think_time: {} }))).toEqual(['think_time'])
    expect(runtimeOverrideFieldPaths(wire({ pacing: { delay_ms: 500 } }))).toEqual(['pacing'])
    expect(runtimeOverrideFieldPaths(wire({ pacing: {} }))).toEqual(['pacing'])
    expect(upgradeRuntimeDraftToPartial(runtimeDraftFromOverride(wire({ think_time: {} }))).override_fields).toContain('think_time')
    expect(upgradeRuntimeDraftToPartial(runtimeDraftFromOverride(wire({ pacing: { delay_ms: 500 } }))).override_fields).toContain('pacing')
  })

  it('agrees with resolveRuntimeRowsValue and displays the zero section', () => {
    expect(effectiveRuntimeSection('think_time', [wire({ think_time: {} })])).toEqual({})
    expect(effectiveRuntimeSection('think_time', [wire({ think_time: {} }), { think_time: { type: 'random', mean_ms: 100 } }])).toEqual({})
    expect(effectiveRuntimeSection('error_handling', [{ error_handling: {} }], { snapshot_on_error: true })).toEqual({})
    for (const row of [{ error_handling: {} }, { think_time: {} }, { pacing: { delay_ms: 500 } }].map(wire)) {
      const section = Object.keys(row)[0]
      expect(resolveRuntimeRowsValue(section, [row]).index).toBe(0)
      expect(runtimeRowCoversPath(row, section)).toBe(true)
      expect(isFieldOverridden(row, section)).toBe(true)
      expect(runtimeSectionSource(section, [row])).toEqual({ layer: 0, whole: true })
    }
  })

  it('keeps the empty section pinned through an unrelated partial edit', () => {
    const edited = setOverrideField(runtimeDraftFromOverride({ error_handling: {}, log: { level: 'debug' } }), 'log.level', 'trace')
    const row = partialRuntimeOverrideFromDraft(edited)
    expect(row?.override_fields).toEqual(expect.arrayContaining(['error_handling', 'log']))
    expect(row?.error_handling).toEqual({})
    const reset = resetOverrideField(runtimeDraftFromOverride(wire({ think_time: {}, log: { level: 'debug' } })), 'log')
    expect(partialRuntimeOverrideFromDraft(reset)).toEqual({ think_time: {}, override_fields: ['think_time'] })
  })

  it('folds flat draft error-handling controls over the v1 section', () => {
    const draft = { ...runtimeDraftFromOverride({ error_handling: {} })!, continue_on_error: true }
    expect(overrideFieldValue(draft, 'error_handling')).toEqual({ continue_on_error: true })
  })

  it('does not treat a null run tombstone as a v1 section', () => {
    expect(runtimeOverrideFieldPaths({ error_handling: null, think_time: null, pacing: null } as PartialRuntimeSettingsOverride)).toEqual([])
  })
})

describe('listed java_vuser and env overlay the lower layer', () => {
  const lower = { java_vuser: { jvm_sharing: 'shared', vusers_per_jvm: 4, classpath: ['lib.jar'] }, env: { BASE: 'b', OTHER: 'o' } }
  const row = { java_vuser: { jdk_home: '/a' }, override_fields: ['java_vuser'] } as PartialRuntimeSettingsOverride

  it('displays only the non-empty leaves over the lower layer', () => {
    expect(effectiveRuntimeSection('java_vuser', [row], lower.java_vuser)).toEqual({ ...lower.java_vuser, jdk_home: '/a' })
    expect(effectiveRuntimeSection('java_vuser', [row, lower as PartialRuntimeSettingsOverride])).toEqual({ ...lower.java_vuser, jdk_home: '/a' })
    expect(resolveRuntimeRowsValue('java_vuser.jvm_sharing', [row, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: 'shared' })
    expect(resolveRuntimeRowsValue('java_vuser.jdk_home', [row, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: '/a' })
    expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [row])).toEqual({ index: -1, value: undefined })
    const script = { java_vuser: lower.java_vuser } as unknown as Parameters<typeof effectiveRuntimeValue>[1]['script']
    expect(effectiveRuntimeValue('java_vuser.vusers_per_jvm', { group: row, script })).toEqual({ value: 4, provenance: 'script' })
    expect(effectiveRuntimeValue('java_vuser', { group: row, script })).toEqual({ value: { ...lower.java_vuser, jdk_home: '/a' }, provenance: 'group_override' })
  })

  it('a non-shared jvm_sharing in the listed section clears the per-JVM count', () => {
    const dedicated = { java_vuser: { jvm_sharing: 'dedicated' }, override_fields: ['java_vuser'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [dedicated, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: undefined })
    expect(effectiveRuntimeSection('java_vuser', [dedicated], lower.java_vuser)).toEqual({ jvm_sharing: 'dedicated', classpath: ['lib.jar'] })
  })

  it('a listed non-shared jvm_sharing leaf clears the per-JVM count (partial and v1 rows)', () => {
    const partialLeaf = { java_vuser: { jvm_sharing: 'dedicated' }, override_fields: ['java_vuser.jvm_sharing'] } as PartialRuntimeSettingsOverride
    const v1Leaf = { java_vuser: { jvm_sharing: 'dedicated' } } as PartialRuntimeSettingsOverride
    for (const row of [partialLeaf, v1Leaf]) {
      expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [row, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: undefined })
      expect(overlayRuntimeOverrideSection(lower.java_vuser, row, 'java_vuser')).toEqual({ jvm_sharing: 'dedicated', classpath: ['lib.jar'] })
      expect(effectiveRuntimeSection('java_vuser', [row], lower.java_vuser)).toEqual({ jvm_sharing: 'dedicated', classpath: ['lib.jar'] })
      const script = { java_vuser: lower.java_vuser } as unknown as Parameters<typeof effectiveRuntimeValue>[1]['script']
      expect(effectiveRuntimeValue('java_vuser.vusers_per_jvm', { scenario: row, script })).toEqual({ value: undefined, provenance: 'scenario_default' })
      // A higher row re-sharing JVMs without a count keeps the cleared count.
      const higher = { java_vuser: { jvm_sharing: 'shared' } } as PartialRuntimeSettingsOverride
      expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [higher, row, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: undefined })
      expect(effectiveRuntimeSection('java_vuser', [higher, row], lower.java_vuser)).toEqual({ jvm_sharing: 'shared', classpath: ['lib.jar'] })
    }
    // A shared leaf, or a row that also lists its own count, keeps the count.
    const sharedLeaf = { java_vuser: { jvm_sharing: 'threads' }, override_fields: ['java_vuser.jvm_sharing'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [sharedLeaf, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: 4 })
    expect(overlayRuntimeOverrideSection(lower.java_vuser, sharedLeaf, 'java_vuser')).toEqual({ ...lower.java_vuser, jvm_sharing: 'threads' })
    const withCount = { java_vuser: { jvm_sharing: 'dedicated', vusers_per_jvm: 7 }, override_fields: ['java_vuser.jvm_sharing', 'java_vuser.vusers_per_jvm'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('java_vuser.vusers_per_jvm', [withCount, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: 7 })
    expect(overlayRuntimeOverrideSection(lower.java_vuser, withCount, 'java_vuser')).toEqual({ jvm_sharing: 'dedicated', vusers_per_jvm: 7, classpath: ['lib.jar'] })
  })

  it('reset of one leaf splits the listed section into its set leaves only', () => {
    const both = { java_vuser: { jdk_home: '/a', jdk_selection: 'specified' }, override_fields: ['java_vuser'] } as PartialRuntimeSettingsOverride
    const reset = resetOverrideField(runtimeDraftFromOverride(both), 'java_vuser.jdk_home')
    expect(reset.override_fields).toEqual(['java_vuser.jdk_selection'])
    expect(partialRuntimeOverrideFromDraft(reset)).toEqual({ java_vuser: { jdk_selection: 'specified' }, override_fields: ['java_vuser.jdk_selection'] })
    // The run keeps the lower layer's sharing mode and per-JVM count.
    expect(resolveRuntimeRowsValue('java_vuser.jvm_sharing', [reset, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: 'shared' })
  })

  it('a listed env with keys merges them over the lower layer', () => {
    const envRow = { env: { BASE: 'x' }, override_fields: ['env'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('env.OTHER', [envRow, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: 'o' })
    expect(resolveRuntimeRowsValue('env.BASE', [envRow, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: 'x' })
    expect(effectiveRuntimeSection('env', [envRow], lower.env)).toEqual({ BASE: 'x', OTHER: 'o' })
    // An implicit env (a partial row carrying env without listing it) reads the same way.
    const implicit = { env: { BASE: 'x' }, http: { version: '2' }, override_fields: ['http.version'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('env.OTHER', [implicit, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 1, value: 'o' })
    // A listed env with no keys still clears the lower layers (mergeRuntimeSettingsPartial).
    const cleared = { override_fields: ['env'] } as PartialRuntimeSettingsOverride
    expect(resolveRuntimeRowsValue('env.OTHER', [cleared, lower as PartialRuntimeSettingsOverride])).toEqual({ index: 0, value: undefined })
  })
})

describe('v1 null section tombstones beside their alias', () => {
  it.each([
    [{ browser_config: null, browser: { headless: true } }, 'browser.headless', true],
    [{ run_logic: null, iterations: 3 }, 'run_logic.iterations', 3],
    [{ log: null, log_level: 'debug' }, 'log.level', 'debug'],
  ])('%j: both resolvers read the alias', (row, path, value) => {
    const stored = row as unknown as PartialRuntimeSettingsOverride
    expect(runtimeOverrideFieldPaths(stored)).toEqual([path])
    expect(resolveRuntimeRowsValue(path, [stored])).toEqual({ index: 0, value })
    expect(overrideFieldValue(stored, path)).toEqual(value)
  })
})

describe('native protocol sections (Phase 4)', () => {
  it('lists the backend sections in its order', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const source = readFileSync(`${cwd}/../pkg/api/runtime_protocol_sections.go`, 'utf8') as string
    const match = /var runtimeProtocolOverrideSections = \[\]string\{([^}]*)\}/.exec(source)
    expect(match).not.toBeNull()
    const goSections = Array.from(match![1].matchAll(/"([a-z_]+)"/g), entry => entry[1])
    expect([...RUNTIME_PROTOCOL_OVERRIDE_SECTIONS]).toEqual(goSections)
  })

  it('applies a section only to its own protocol family (runtimeProtocolFieldApplies)', () => {
    expect(isRuntimeProtocolSectionApplicable('kafka', 'kafka')).toBe(true)
    expect(isRuntimeProtocolSectionApplicable('jms', 'jms-solace')).toBe(true)
    expect(isRuntimeProtocolSectionApplicable('jms', ' JMS-IBMMQ ')).toBe(true)
    expect(isRuntimeProtocolSectionApplicable('java_http', 'java_http')).toBe(true)
    expect(isRuntimeProtocolSectionApplicable('kafka', 'http')).toBe(false)
    expect(isRuntimeProtocolSectionApplicable('grpc', undefined)).toBe(false)
  })

  it('normalizes protocol leaf, nested, map-key and java_vuser paths as the API does', () => {
    const cases: Array<[string, string | undefined]> = [
      ['kafka', 'kafka'],
      ['config.kafka.SASL_Mechanism', 'kafka.sasl_mechanism'],
      ['kafka.oauth.client_secret', 'kafka.oauth.client_secret'],
      ['kafka.producer.linger_ms', 'kafka.producer.linger_ms'],
      // A map key keeps its case and dots.
      ['kafka.properties.sasl.jaas.config', 'kafka.properties.sasl.jaas.config'],
      ['appium.capabilities.browserstack.key', 'appium.capabilities.browserstack.key'],
      ['jms.tls.ca_cert_secret', 'jms.tls.ca_cert_secret'],
      ['eventhubs.owner_level', 'eventhubs.owner_level'],
      ['java_http.request_mapping.a', 'java_http.request_mapping.a'],
      // Lists are whole leaves.
      ['kafka.brokers.0', undefined],
      ['kafka.nope', undefined],
      ['java_vuser.startup_timeout_ms', 'java_vuser.startup_timeout_ms'],
      ['java_vuser.param_left_brace', 'java_vuser.param_left_brace'],
      ['websocket.reconnect', 'websocket.reconnect'],
    ]
    for (const [raw, canonical] of cases) {
      expect([raw, normalizeRuntimeOverrideFieldPath(raw)]).toEqual([raw, canonical])
    }
  })

  it('lists each protocol section a v1 row carries whole, and the new java_vuser leaves', () => {
    expect(legacyRuntimeOverrideFields({
      kafka: { topic: 'orders' },
      grpc: {},
      java_vuser: { startup_timeout_ms: 5000, action_timeout_ms: 0, param_left_brace: '<', param_right_brace: '>' },
    })).toEqual(['kafka', 'grpc', 'java_vuser.startup_timeout_ms', 'java_vuser.param_left_brace', 'java_vuser.param_right_brace'])
  })

  it('round-trips a v1 protocol row unchanged, and converts it on first edit without changing what it applies', () => {
    const stored = runtimeDraftFromOverride({ kafka: { topic: 'orders', sasl_password: '[REDACTED]', properties: { 'client.rack': 'a' } } })!
    expect(isRuntimeDraftEmpty(stored)).toBe(false)
    expect(legacyRuntimeOverridePayload(stored)).toEqual({ kafka: { topic: 'orders', sasl_password: '[REDACTED]', properties: { 'client.rack': 'a' } } })
    const edited = setOverrideField(stored, 'kafka.topic', 'payments')
    // The whole section stays listed (it replaced the script's section whole),
    // the masked password is sent back for the server to reconcile.
    expect(partialRuntimeOverrideFromDraft(edited)).toEqual({
      kafka: { topic: 'payments', sasl_password: '[REDACTED]', properties: { 'client.rack': 'a' } },
      override_fields: ['kafka'],
    })
    // Resetting one leaf splits the listed section into its remaining leaves.
    const reset = resetOverrideField(edited, 'kafka.topic')
    expect(runtimeOverrideFieldPaths(reset)).not.toContain('kafka')
    expect(runtimeOverrideFieldPaths(reset)).toContain('kafka.sasl_password')
    expect(runtimeOverrideFieldPaths(reset)).toContain('kafka.properties')
    expect(runtimeOverrideFieldPaths(reset)).not.toContain('kafka.topic')
  })

  it('writes one protocol leaf or map key per edit, never the whole section', () => {
    let draft = setOverrideField(undefined, 'jms.destination_type', 'topic')
    draft = setOverrideField(draft, 'jms.properties.vpn.region', 'eu')
    expect(partialRuntimeOverrideFromDraft(draft)).toEqual({
      jms: { destination_type: 'topic', properties: { 'vpn.region': 'eu' } },
      override_fields: ['jms.destination_type', 'jms.properties.vpn.region'],
    })
    // A listed map key with no value removes the inherited key.
    const removed = setOverrideField(undefined, 'grpc.metadata.x-tenant', undefined)
    expect(partialRuntimeOverrideFromDraft(removed)).toEqual({ override_fields: ['grpc.metadata.x-tenant'] })
    expect(overlayRuntimeOverrideSection({ metadata: { 'x-tenant': 'a', keep: 'b' } }, removed, 'grpc')).toEqual({ metadata: { keep: 'b' } })
  })

  it('keeps lower-layer keys cleared when resetting one key of a map listed whole', () => {
    // A group row lists grpc.metadata whole with {a}, which clears the key b the
    // script supplies. Resetting a must not bring b back (backend
    // overrideFieldPathsExcludingValue also lists the target layers' keys).
    const script = { base_url: '', grpc: { metadata: { b: '2' } } }
    const group = runtimeDraftFromOverride({ grpc: { metadata: { a: '1' } }, override_fields: ['grpc.metadata'] })
    expect(effectiveRuntimeValue('grpc.metadata', { group, script }).value).toEqual({ a: '1' })
    const reset = resetOverrideField(group, 'grpc.metadata.a', { lowerMapKeys: () => ['b'] })
    expect(runtimeOverrideFieldPaths(reset)).toEqual(['grpc.metadata.b'])
    expect(effectiveRuntimeValue('grpc.metadata', { group: reset, script }).value ?? {}).toEqual({})
    // A whole section listed splits the same way down to the map.
    const section = runtimeDraftFromOverride({ grpc: { metadata: { a: '1' }, timeout_ms: 10 }, override_fields: ['grpc'] })
    const sectionReset = resetOverrideField(section, 'grpc.metadata.a', { lowerMapKeys: path => (path === 'grpc.metadata' ? ['b'] : []) })
    expect(runtimeOverrideFieldPaths(sectionReset)).toContain('grpc.metadata.b')
    expect(runtimeOverrideFieldPaths(sectionReset)).not.toContain('grpc.metadata.a')
  })

  it('resolves a map edited key by key at any layer, each layer laid over the ones below', () => {
    const script = { base_url: '', kafka: { properties: { b: '2', c: '3' } } }
    const scenario = setOverrideField(setOverrideField(undefined, 'kafka.properties.a', '1'), 'kafka.properties.c', undefined)
    expect(effectiveRuntimeValue('kafka.properties', { scenario, script })).toEqual({ value: { a: '1', b: '2' }, provenance: 'scenario_default' })
    const group = setOverrideField(undefined, 'kafka.properties.d.e', '4')
    expect(effectiveRuntimeValue('kafka.properties', { group, scenario, script })).toEqual({ value: { a: '1', b: '2', 'd.e': '4' }, provenance: 'group_override' })
    // The run-level stack (resolveRuntimeRowsValue) applies the same overlay.
    expect(resolveRuntimeRowsValue('kafka.properties', [group, scenario])).toEqual({ index: 0, value: { a: '1', 'd.e': '4' } })
    // A row that lists the whole map still replaces it.
    const whole = setOverrideField(undefined, 'kafka.properties', { z: '9' })
    expect(effectiveRuntimeValue('kafka.properties', { group: whole, scenario, script }).value).toEqual({ z: '9' })
  })

  it('resolves a protocol leaf through group, scenario, script and engine', () => {
    const group = setOverrideField(undefined, 'database.max_rows', 50)
    const scenario = setOverrideField(undefined, 'database.query_timeout_ms', 900)
    const script = { base_url: '', database: { driver: 'sqlite', max_rows: 10, query_timeout_ms: 100 } }
    expect(effectiveRuntimeValue('database.max_rows', { group, scenario, script })).toEqual({ value: 50, provenance: 'group_override' })
    expect(effectiveRuntimeValue('database.query_timeout_ms', { group, scenario, script })).toEqual({ value: 900, provenance: 'scenario_default' })
    expect(effectiveRuntimeValue('database.driver', { group, scenario, script })).toEqual({ value: 'sqlite', provenance: 'script' })
  })

  it('projects only the non-zero protocol sections of a script config', () => {
    const projected = runtimeOverrideFromScriptConfig({
      base_url: '',
      kafka: { topic: 'orders' },
      grpc: { use_reflection: false },
      java_vuser: { startup_timeout_ms: 1000, param_left_brace: '<<', param_right_brace: '>>' },
    })
    expect(projected?.kafka).toEqual({ topic: 'orders' })
    expect(projected && 'grpc' in projected).toBe(false)
    expect(projected?.java_vuser).toMatchObject({ startup_timeout_ms: 1000, param_left_brace: '<<', param_right_brace: '>>' })
  })

  it('upgrades a v1 java_vuser row by listing the new leaves it sets', () => {
    const stored = runtimeDraftFromOverride({ java_vuser: { action_timeout_ms: 9000 } })!
    expect(upgradeRuntimeDraftToPartial(stored).override_fields).toEqual(['java_vuser.action_timeout_ms'])
    expect(overrideFromRuntimeDraft(stored)).toEqual({ java_vuser: { action_timeout_ms: 9000 } })
  })
})
