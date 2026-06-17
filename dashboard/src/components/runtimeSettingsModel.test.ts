import { describe, expect, it } from 'vitest'

import {
  formatAutoTransactions,
  formatEffectiveRuntimeField,
  formatErrorHandling,
  formatPacing,
  formatThinkTime,
  isRuntimeFieldApplicable,
  resolveRuntimeFieldSource,
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
    // falls back to script default label
    expect(formatEffectiveRuntimeField('think_time', group, scenario)).toEqual({ value: 'Script default', source: 'script' })
    expect(formatEffectiveRuntimeField('iterations', undefined, undefined, 'Duration driven')).toEqual({ value: 'Duration driven', source: 'script' })
  })
})
