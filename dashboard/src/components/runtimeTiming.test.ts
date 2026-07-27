import { describe, expect, it } from 'vitest'

import {
  formatMillisecondsAsSecondsDuration,
  formatMillisecondsAsSecondsValue,
  formatMillisecondsRangeAsSecondsDuration,
  formatPacingTiming,
  parseSecondsInputAsMilliseconds,
} from './runtimeTiming'

describe('runtimeTiming', () => {
  it('formats millisecond values as decimal seconds without losing millisecond precision', () => {
    expect(formatMillisecondsAsSecondsValue(5000)).toBe('5')
    expect(formatMillisecondsAsSecondsValue(2500)).toBe('2.5')
    expect(formatMillisecondsAsSecondsValue(1)).toBe('0.001')
    expect(formatMillisecondsAsSecondsValue(undefined, 0, true)).toBe('')
    expect(formatMillisecondsAsSecondsDuration(1000)).toBe('1 second')
    expect(formatMillisecondsAsSecondsDuration(5000)).toBe('5 seconds')
    expect(formatMillisecondsRangeAsSecondsDuration(2500, 7500)).toBe('2.5–7.5 seconds')
  })

  it('converts decimal second input back to integer milliseconds', () => {
    expect(parseSecondsInputAsMilliseconds('5')).toBe(5000)
    expect(parseSecondsInputAsMilliseconds('2.5')).toBe(2500)
    expect(parseSecondsInputAsMilliseconds('0.001')).toBe(1)
    expect(parseSecondsInputAsMilliseconds('0.0005')).toBe(1)
    expect(parseSecondsInputAsMilliseconds('')).toBeUndefined()
    expect(parseSecondsInputAsMilliseconds('not-a-number')).toBeNull()
    expect(parseSecondsInputAsMilliseconds('-2')).toBe(0)
    expect(parseSecondsInputAsMilliseconds('1e308')).toBeNull()
    expect(parseSecondsInputAsMilliseconds(String(Number.MAX_SAFE_INTEGER))).toBeNull()
  })

  it('formats every pacing timing mode in seconds while leaving TPS unchanged', () => {
    expect(formatPacingTiming({ type: 'none' })).toBe('None')
    expect(formatPacingTiming({ type: 'fixed_delay', delay_ms: 250 })).toBe('Fixed delay · 0.25 seconds')
    expect(formatPacingTiming({ type: 'fixed_interval', interval_ms: 5000 })).toBe('Fixed interval · 5 seconds')
    expect(formatPacingTiming({ type: 'random_delay', min_delay_ms: 100, max_delay_ms: 300 })).toBe('Random delay · 0.1–0.3 seconds')
    expect(formatPacingTiming({ type: 'random_interval', min_interval_ms: 2500, max_interval_ms: 7500 })).toBe('Random interval · 2.5–7.5 seconds')
    expect(formatPacingTiming({ type: 'tps', tps: 50 })).toBe('Target TPS · 50')
  })
})
