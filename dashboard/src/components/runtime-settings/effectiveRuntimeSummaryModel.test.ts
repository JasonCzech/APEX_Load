import { describe, expect, it } from 'vitest'

import type { PartialRuntimeSettingsOverride } from '../../types/runtimeSettings'
import type { ScriptConfig } from '../../types/scripts'
import { effectiveRuntimeGroupSources, effectiveRuntimeSourcesFromManifest } from './effectiveRuntimeSources'
import { buildEffectiveRuntimeSummary, formatEffectiveRuntimeValue, type EffectiveRuntimeSummary } from './effectiveRuntimeSummaryModel'

function entry(summary: EffectiveRuntimeSummary, path: string) {
  for (const category of summary.categories) {
    const found = category.entries.find(item => item.path === path)
    if (found) return { ...found, category: category.id, categoryLabel: category.label }
  }
  return undefined
}

function allText(summary: EffectiveRuntimeSummary): string {
  return summary.categories.flatMap(category => category.entries.map(item => `${item.label} ${item.text} ${item.secretName ?? ''}`)).join('\n')
}

describe('buildEffectiveRuntimeSummary', () => {
  it('lays a partial group row over a v1 scenario section path by path', () => {
    const group: PartialRuntimeSettingsOverride = {
      http: { tls: { max_version: '1.2' } },
      override_fields: ['http.tls.max_version'],
    }
    const scenario: PartialRuntimeSettingsOverride = { http: { request_timeout_ms: 45000, tls: { min_version: '1.1' } } }
    const summary = buildEffectiveRuntimeSummary({ name: 'Checkout', protocol: 'http', row: group }, scenario)

    expect(entry(summary, 'http.tls.max_version')).toMatchObject({ provenance: 'group_override', text: '1.2', label: 'TLS max version', category: 'security_proxy_auth', categoryLabel: 'Security, proxy & auth' })
    expect(entry(summary, 'http.tls.min_version')).toMatchObject({ provenance: 'scenario_default', text: '1.1' })
    expect(entry(summary, 'http.request_timeout_ms')).toMatchObject({ provenance: 'scenario_default', text: '45000', category: 'transport_connections' })
    // The v1 scenario section replaces the script's http section whole.
    expect(entry(summary, 'http')).toMatchObject({ kind: 'section', provenance: 'scenario_default' })
    expect(summary.overrideCount).toBe(4)
  })

  it('keeps v1 whole-section semantics: a v1 group section hides the scenario leaves it omits', () => {
    const group: PartialRuntimeSettingsOverride = { http: { request_timeout_ms: 10000 } }
    const scenario: PartialRuntimeSettingsOverride = { http: { tls: { max_version: '1.3' } }, override_fields: ['http.tls.max_version'] }
    const summary = buildEffectiveRuntimeSummary({ name: 'Checkout', row: group }, scenario)

    expect(entry(summary, 'http')).toMatchObject({ kind: 'section', provenance: 'group_override' })
    expect(entry(summary, 'http.request_timeout_ms')).toMatchObject({ provenance: 'group_override', text: '10000' })
    expect(entry(summary, 'http.tls.max_version')).toMatchObject({ provenance: 'group_override', kind: 'cleared' })
  })

  it('shows an explicit partial clear as the engine default, and an empty v1 section as a section', () => {
    const summary = buildEffectiveRuntimeSummary(
      { name: 'Checkout', row: { override_fields: ['http.connect_timeout_ms'] } },
      { think_time: {} } as PartialRuntimeSettingsOverride,
    )
    expect(entry(summary, 'http.connect_timeout_ms')).toMatchObject({ kind: 'cleared', provenance: 'group_override', text: 'Not set (engine default applies)' })
    expect(entry(summary, 'think_time')).toMatchObject({ kind: 'section', provenance: 'scenario_default', category: 'think_time' })
  })

  it('shows SOAP version, fault handling and WS-Security with the masked credentials hidden', () => {
    const group: PartialRuntimeSettingsOverride = {
      soap: {
        version: '1.2',
        fault_handling: { fail_on_fault: false },
        ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]', password_type: 'PasswordDigest', nonce: true } },
      },
      override_fields: ['soap.version', 'soap.fault_handling.fail_on_fault', 'soap.ws_security'],
    }
    const summary = buildEffectiveRuntimeSummary({ name: 'Orders', protocol: 'soap', row: group })

    expect(entry(summary, 'soap.version')).toMatchObject({ text: '1.2', label: 'SOAP version', category: 'protocol_behavior_session' })
    expect(entry(summary, 'soap.fault_handling.fail_on_fault')).toMatchObject({ text: 'Disabled', label: 'Fail on SOAP fault' })
    expect(entry(summary, 'soap.ws_security.username_token.username')).toMatchObject({ kind: 'stored_secret', category: 'security_proxy_auth' })
    expect(entry(summary, 'soap.ws_security.username_token.password')).toMatchObject({ kind: 'stored_secret', text: 'Stored secret (hidden)' })
    expect(entry(summary, 'soap.ws_security.username_token.password_type')).toMatchObject({ text: 'PasswordDigest' })
    expect(entry(summary, 'soap.ws_security.username_token.nonce')).toMatchObject({ text: 'Enabled' })
    expect(entry(summary, 'soap.ws_security')).toMatchObject({ kind: 'section' })
    expect(allText(summary)).not.toContain('[REDACTED]')
  })

  it('never reads the mask as a value: masked URLs keep their shape and credential leaves stay hidden', () => {
    const scenario: PartialRuntimeSettingsOverride = {
      base_url: 'https://[REDACTED]:[REDACTED]@api.example.test/v1?token=[REDACTED]',
      http: { auth: { type: 'bearer', token: 'typed-token-value' }, default_headers: { Authorization: '[REDACTED]', 'X-Trace': 'on' } },
      override_fields: ['base_url', 'http.auth', 'http.default_headers'],
    }
    const summary = buildEffectiveRuntimeSummary({ name: 'Api' }, scenario)

    expect(entry(summary, 'base_url')).toMatchObject({ kind: 'value', label: 'Base URL', category: 'target_identity', provenance: 'scenario_default' })
    expect(entry(summary, 'base_url')?.text).toBe('https://***@api.example.test/v1?token=***')
    expect(entry(summary, 'http.auth.token')).toMatchObject({ kind: 'hidden', text: 'Set (hidden)' })
    expect(entry(summary, 'http.auth.type')).toMatchObject({ text: 'bearer' })
    expect(entry(summary, 'http.default_headers.Authorization')).toMatchObject({ kind: 'stored_secret' })
    expect(entry(summary, 'http.default_headers.X-Trace')).toMatchObject({ text: 'on', label: 'HTTP default headers X-Trace' })
    const text = allText(summary)
    expect(text).not.toContain('[REDACTED]')
    expect(text).not.toContain('typed-token-value')
  })

  it('shows project-secret references as references and controller-owned ones as stored secrets', () => {
    const scenario: PartialRuntimeSettingsOverride = {
      env: { API_HOST: '{{secret.CHECKOUT_HOST}}', RUN_ID: '{{ secret.APEX_RUNTIME_OVERRIDE_ENV_RUN_ID }}', REGION: 'east' },
    }
    const summary = buildEffectiveRuntimeSummary({ name: 'Api' }, scenario)

    expect(entry(summary, 'env.API_HOST')).toMatchObject({ kind: 'secret_reference', secretName: 'CHECKOUT_HOST', label: 'Environment variable API_HOST', category: 'other' })
    expect(entry(summary, 'env.RUN_ID')).toMatchObject({ kind: 'stored_secret' })
    expect(entry(summary, 'env.REGION')).toMatchObject({ kind: 'value', text: 'east' })
    expect(formatEffectiveRuntimeValue('env.API_HOST', '{{secret.CHECKOUT_HOST}}')).toBe('Secret reference {{secret.CHECKOUT_HOST}}')
  })

  it('falls back to the script for key fields no override covers, and stamps the group iteration count', () => {
    const script = { base_url: 'https://shop.example.test', http: { tls: { max_version: '1.3' }, version: 'h2' }, run_logic: { iterations: 1 } } as ScriptConfig
    const summary = buildEffectiveRuntimeSummary(
      { name: 'Checkout', protocol: 'http', iterations: 9, iterationOverride: 9, iterationOverrideKnown: true, scriptConfig: script, row: { base_url: 'https://override.example.test' } },
    )

    expect(entry(summary, 'base_url')).toMatchObject({ provenance: 'group_override', text: 'https://override.example.test' })
    expect(entry(summary, 'http.tls.max_version')).toMatchObject({ provenance: 'script', text: '1.3' })
    expect(entry(summary, 'http.version')).toMatchObject({ provenance: 'script', text: 'h2' })
    expect(entry(summary, 'run_logic.iterations')).toMatchObject({ provenance: 'group_override', text: '9' })
    expect(entry(summary, 'soap.version')).toBeUndefined()
    expect(summary.scriptKnown).toBe(true)
  })

  it('does not treat a listed java_vuser block as a whole-section replacement', () => {
    const summary = buildEffectiveRuntimeSummary({ name: 'Java', row: { java_vuser: { jdk_selection: 'specified' }, override_fields: ['java_vuser'] } })
    expect(entry(summary, 'java_vuser')).toBeUndefined()
    expect(entry(summary, 'java_vuser.jdk_selection')).toMatchObject({ provenance: 'group_override', text: 'specified' })
  })

  it('orders categories by the canonical taxonomy and returns none without overrides', () => {
    const summary = buildEffectiveRuntimeSummary({ name: 'Mixed' }, { log_level: 'debug', base_url: 'https://a.example.test', think_time: { type: 'fixed', mean_ms: 100 } })
    expect(summary.categories.map(category => category.id)).toEqual(['target_identity', 'think_time', 'errors_logging_diagnostics'])
    expect(entry(summary, 'log.level')).toMatchObject({ text: 'debug', label: 'Log level' })
    expect(buildEffectiveRuntimeSummary({ name: 'Plain' }).categories).toEqual([])
  })

  it('attributes iterations to the layer that supplies them, not to the group, when the group sets none', () => {
    const scenario: PartialRuntimeSettingsOverride = { run_logic: { iterations: 5 }, override_fields: ['run_logic.iterations'] }
    const summary = buildEffectiveRuntimeSummary({ name: 'Checkout', iterations: 5, iterationOverrideKnown: true }, scenario)
    expect(entry(summary, 'run_logic.iterations')).toMatchObject({ provenance: 'scenario_default', text: '5' })

    // A legacy row (no iteration_override_known): the effective count without a layer claim,
    // unless a row supplies that same count.
    expect(entry(buildEffectiveRuntimeSummary({ name: 'Legacy', iterations: 7 }), 'run_logic.iterations')).toMatchObject({ text: '7', provenance: undefined })
    expect(entry(buildEffectiveRuntimeSummary({ name: 'Legacy', iterations: 5 }, scenario), 'run_logic.iterations')).toMatchObject({ text: '5', provenance: 'scenario_default' })
    // A count no layer sets is the script cap an iteration-terminated run kept.
    expect(entry(buildEffectiveRuntimeSummary({ name: 'Kept', iterations: 3, iterationOverrideKnown: true }), 'run_logic.iterations')).toMatchObject({ text: '3', provenance: 'script' })
  })

  it('shows continue on error as the API resolved it', () => {
    const resolution = (source: string, effective: boolean) => ({ effective, source, scenario_value: true, scenario_explicit: false })
    const v1Group: PartialRuntimeSettingsOverride = { error_handling: { max_retries: 2 } }
    const cases: Array<[string, boolean, string]> = [['group', false, 'group_override'], ['scenario', false, 'scenario_default'], ['script', false, 'script'], ['scenario_default', true, 'scenario_default']]
    for (const [source, effective, provenance] of cases) {
      const summary = buildEffectiveRuntimeSummary({ name: 'Checkout', row: v1Group, continueOnError: resolution(source, effective) })
      expect(entry(summary, 'error_handling.continue_on_error')).toMatchObject({ provenance, text: effective ? 'Enabled' : 'Disabled' })
    }
  })

  it('resolves continue on error without the API resolution: a section that omits it states nothing', () => {
    // A v1 scenario section without the field: the scenario's enabled-by-default policy applies.
    const v1Scenario: PartialRuntimeSettingsOverride = { error_handling: { max_retries: 2 } }
    const plain = buildEffectiveRuntimeSummary({ name: 'Checkout', scriptConfig: { run_logic: {} } as ScriptConfig }, v1Scenario)
    expect(entry(plain, 'error_handling.continue_on_error')).toMatchObject({ provenance: 'scenario_default', text: 'Enabled' })
    expect(entry(plain, 'error_handling')?.text).toContain('except continue on error')
    // The script's own value applies when no layer states one.
    const scripted = buildEffectiveRuntimeSummary({ name: 'Checkout', scriptConfig: { error_handling: { continue_on_error: false } } as ScriptConfig }, v1Scenario)
    expect(entry(scripted, 'error_handling.continue_on_error')).toMatchObject({ provenance: 'script', text: 'Disabled' })
    // A v1 group section that omits it does not hide the scenario's explicit false.
    const layered = buildEffectiveRuntimeSummary(
      { name: 'Checkout', row: { error_handling: { max_retries: 1 } } },
      { error_handling: { continue_on_error: false }, override_fields: ['error_handling.continue_on_error'] },
    )
    expect(entry(layered, 'error_handling.continue_on_error')).toMatchObject({ provenance: 'scenario_default', text: 'Disabled' })
  })

  it('shows a closed-model script iteration cap as overridden by a duration schedule', () => {
    const script = { run_logic: { iterations: 1 } } as ScriptConfig
    const duration = buildEffectiveRuntimeSummary({ name: 'Checkout', iterationOverrideKnown: true, scriptConfig: script, scheduleTerminationMode: 'duration' })
    expect(entry(duration, 'run_logic.iterations')).toMatchObject({ provenance: 'script', text: 'Script cap 1 (overridden by the schedule)' })
    const iterationSchedule = buildEffectiveRuntimeSummary({ name: 'Checkout', scriptConfig: script, scheduleTerminationMode: 'iterations' })
    expect(entry(iterationSchedule, 'run_logic.iterations')).toMatchObject({ provenance: 'script', text: '1' })
    const open = buildEffectiveRuntimeSummary({ name: 'Checkout', scriptConfig: { run_logic: { iterations: 1, workload_model: 'open' } } as ScriptConfig })
    expect(entry(open, 'run_logic.iterations')).toMatchObject({ text: '1' })
    const iterationScript = buildEffectiveRuntimeSummary({ name: 'Checkout', scriptConfig: { run_logic: { iterations: 1, termination_mode: 'iterations' } } as ScriptConfig })
    expect(entry(iterationScript, 'run_logic.iterations')).toMatchObject({ text: '1' })
  })

  it('carries the API iteration override and continue-on-error resolution from stored groups and manifest scripts', () => {
    const resolution = { effective: false, source: 'scenario', scenario_value: false, scenario_explicit: true }
    const [group] = effectiveRuntimeGroupSources([{ name: 'Checkout', script_id: 's', script_name: 'S', protocol: 'http', vusers: 1, iterations: 5, iteration_override_known: true, continue_on_error: resolution }])
    const summary = buildEffectiveRuntimeSummary(group, { run_logic: { iterations: 5 }, override_fields: ['run_logic.iterations'] })
    expect(entry(summary, 'run_logic.iterations')).toMatchObject({ provenance: 'scenario_default', text: '5' })
    expect(entry(summary, 'error_handling.continue_on_error')).toMatchObject({ provenance: 'scenario_default', text: 'Disabled' })
    const manifest = { scripts: [{ group_name: 'Checkout', iterations: 5, continue_on_error: resolution }] } as unknown as Parameters<typeof effectiveRuntimeSourcesFromManifest>[0]
    expect(effectiveRuntimeSourcesFromManifest(manifest).groups[0].continueOnError).toEqual(resolution)
  })
})
