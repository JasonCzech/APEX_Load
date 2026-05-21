import { describe, expect, it } from 'vitest'

import { isValidDurationInput, parseDurationToSeconds, serializeDurationFieldValue, splitDurationInput } from './timeUtils'

describe('timeUtils', () => {
  it('parses Go-style millisecond durations without treating ms as minutes', () => {
    expect(parseDurationToSeconds('60ms')).toBe(1)
    expect(parseDurationToSeconds('1500ms')).toBe(2)
    expect(parseDurationToSeconds('1m30s')).toBe(90)
  })

  it('validates full duration strings instead of partial matches', () => {
    expect(isValidDurationInput('1h 30m')).toBe(true)
    expect(isValidDurationInput('90m')).toBe(true)
    expect(isValidDurationInput('example 1m')).toBe(false)
    expect(isValidDurationInput('0s')).toBe(false)
    expect(isValidDurationInput('', true)).toBe(true)
  })

  it('splits and serializes duration field values', () => {
    expect(splitDurationInput('1h30m')).toEqual({ value: 90, unit: 'm' })
    expect(splitDurationInput('45s')).toEqual({ value: 45, unit: 's' })
    expect(splitDurationInput('', 's')).toEqual({ value: '', unit: 's' })
    expect(serializeDurationFieldValue(5, 'm')).toBe('5m')
    expect(serializeDurationFieldValue('', 'm')).toBe('')
  })
})
