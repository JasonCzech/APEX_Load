import { describe, expect, it } from 'vitest'

import {
  formatAutoTransactions,
  formatEffectiveRuntimeField,
  formatErrorHandling,
  formatPacing,
  formatThinkTime,
  isRuntimeFieldApplicable,
  overrideFromRuntimeDraft,
  resolveRuntimeFieldSource,
  runtimeDraftFromOverride,
  runtimeOverrideFromScriptConfig,
} from './runtimeSettingsModel'

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
  })

  it('formats pacing variants', () => {
    expect(formatPacing({ type: 'none' })).toBe('None')
    expect(formatPacing({ type: 'fixed_delay', delay_ms: 250 })).toBe('Fixed delay · 250ms')
    expect(formatPacing({ type: 'fixed_interval', interval_ms: 1000 })).toBe('Fixed interval · 1000ms')
    expect(formatPacing({ type: 'tps', tps: 50 })).toBe('Target TPS · 50')
    expect(formatPacing(undefined)).toBeNull()
  })

  it('formats think time, auto-transactions, and error handling', () => {
    expect(formatThinkTime({ type: 'fixed', mean_ms: 500 })).toBe('fixed · 500ms')
    expect(formatThinkTime({ type: 'fixed', mean_ms: 500, ignore: true })).toBe('Disabled')
    expect(formatAutoTransactions('per_action')).toBe('per action')
    expect(formatErrorHandling({ continue_on_error: true, snapshot_on_error: false })).toBe('Continue on error · No snapshot')
    expect(formatErrorHandling(undefined)).toBeNull()
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
    // The Run Logic / Logging tab handlers promote these into run_logic/log and
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
    expect(formatEffectiveRuntimeField('pacing', undefined, undefined, scriptDefault)).toEqual({ value: 'Fixed delay · 500ms', source: 'script' })
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
})
