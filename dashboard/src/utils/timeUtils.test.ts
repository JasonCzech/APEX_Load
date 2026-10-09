import { describe, expect, it } from 'vitest'

import { formatRunClock, formatTimeLong, isValidDurationInput, isValidNonNegativeDurationInput, parseDurationToNanoseconds, parseDurationToSeconds, serializeDurationFieldValue, splitDurationInput } from './timeUtils'

describe('timeUtils', () => {
  it.each([
    ['+1s', 1],
    ['.5s', 0.5],
    ['1.s', 1],
    ['60ms', 0.06],
    ['1500ms', 1.5],
    ['1m30s', 90],
    ['1us', 0.000001],
    ['2µs', 0.000002],
    ['2μs', 0.000002],
    ['3ns', 0.000000003],
    ['1ms250us500ns', 0.0012505],
    ['0.1ns1ns', 0.000000001],
    ['9223372036854775807ns', 9_223_372_036.854776],
  ])('accepts positive time.ParseDuration form %s', (input, seconds) => {
    expect(isValidDurationInput(input)).toBe(true)
    expect(parseDurationToSeconds(input)).toBe(seconds)
  })

  it.each([
    '',
    ' ',
    ' 1s',
    '1s ',
    '1 s',
    '1h 30m',
    '1S',
    '1MS',
    '1Us',
    '1μS',
    'example1m',
    '0',
    '+0',
    '-0',
    '0s',
    '+0s',
    '-1us',
    '.s',
    '1',
    '1e3s',
    '1xs',
    '--1s',
    '+-1s',
    '0.1ns',
    '9223372036854775808ns',
    '2562048h',
  ])('rejects non-Go or nonpositive duration form %j', (input) => {
    expect(isValidDurationInput(input)).toBe(false)
    expect(parseDurationToSeconds(input)).toBe(0)
  })

  it('only treats an actually empty field as optional', () => {
    expect(isValidDurationInput('', true)).toBe(true)
    expect(isValidDurationInput(undefined, true)).toBe(true)
    expect(isValidDurationInput(' ', true)).toBe(false)
  })

  it('accepts explicit zero only for non-negative duration fields', () => {
    expect(isValidDurationInput('0s')).toBe(false)
    expect(isValidNonNegativeDurationInput('0')).toBe(true)
    expect(isValidNonNegativeDurationInput('+0')).toBe(true)
    expect(isValidNonNegativeDurationInput('0s')).toBe(true)
    expect(isValidNonNegativeDurationInput('+0s')).toBe(true)
    expect(isValidNonNegativeDurationInput('-1s')).toBe(false)
    expect(isValidNonNegativeDurationInput('later')).toBe(false)
  })

  it('splits and serializes duration field values', () => {
    expect(splitDurationInput('1h30m')).toEqual({ value: '90', unit: 'm' })
    expect(splitDurationInput('+1.s')).toEqual({ value: '1', unit: 's' })
    expect(splitDurationInput('45s')).toEqual({ value: '45', unit: 's' })
    expect(splitDurationInput('1500ms')).toEqual({ value: '1.5', unit: 's' })
    expect(splitDurationInput('1us')).toEqual({ value: '0.000001', unit: 's' })
    expect(splitDurationInput('1ns')).toEqual({ value: '0.000000001', unit: 's' })
    expect(splitDurationInput('', 's')).toEqual({ value: '', unit: 's' })
    expect(serializeDurationFieldValue(5, 'm')).toBe('5m')
    expect(serializeDurationFieldValue(0.000001, 's')).toBe('0.000001s')
    expect(serializeDurationFieldValue(0.000000001, 's')).toBe('0.000000001s')
    expect(serializeDurationFieldValue(0.00000025, 's')).toBe('0.00000025s')
    expect(parseDurationToSeconds(serializeDurationFieldValue(0.000000001, 's'))).toBe(0.000000001)
    expect(serializeDurationFieldValue('', 'm')).toBe('')
  })

  it.each([
    ['17280000000000001ns', '17280000.000000001', '17280000.000000001s'],
    ['9223372036854775807ns', '9223372036.854775807', '9223372036.854775807s'],
  ])('preserves exact nanoseconds while normalizing %s for the editor', (source, value, serialized) => {
    const split = splitDurationInput(source)
    expect(split).toEqual({ value, unit: 's' })
    expect(serializeDurationFieldValue(split.value, split.unit)).toBe(serialized)
    expect(isValidDurationInput(serialized)).toBe(true)
    expect(parseDurationToNanoseconds(serialized)).toBe(BigInt(source.slice(0, -2)))
  })

  it('returns no exact value for invalid or nonpositive durations', () => {
    expect(parseDurationToNanoseconds('0s')).toBeNull()
    expect(parseDurationToNanoseconds('0.1ns')).toBeNull()
    expect(parseDurationToNanoseconds('later')).toBeNull()
  })

  it('parses 400-digit fractions without Number overflow and honors the final significant digit', () => {
    // 1 / 3.6e12 hours is exactly one nanosecond. These two 400-digit decimal
    // fractions straddle that recurring-decimal boundary only at their last digit.
    const fractionPrefix = `${'0'.repeat(12)}2${'7'.repeat(386)}`
    const belowOneNanosecond = `0.${fractionPrefix}6h1ns`
    const aboveOneNanosecond = `0.${fractionPrefix}8h1ns`

    expect(fractionPrefix.length + 1).toBe(400)
    expect(parseDurationToNanoseconds(belowOneNanosecond)).toBe(1n)
    expect(parseDurationToNanoseconds(aboveOneNanosecond)).toBe(2n)
    expect(isValidDurationInput(belowOneNanosecond)).toBe(true)
    expect(isValidDurationInput(aboveOneNanosecond)).toBe(true)
  })

  it.each([
    ['1.5ms', 1_500_000n],
    ['1.9999999999ns', 1n],
    ['0.0000000019s', 1n],
    ['2.0000001s', 2_000_000_100n],
  ])('truncates ordinary fractional duration %s exactly at nanoseconds', (input, nanoseconds) => {
    expect(parseDurationToNanoseconds(input)).toBe(nanoseconds)
  })
})

describe('formatRunClock', () => {
  it.each([
    [0, '0s'],
    [41, '41s'],
    [61.9, '1m 01s'],
    [2 * 3600 + 41, '2h 00m 41s'],
    [3 * 3600 + 5 * 60, '3h 05m 00s'],
    [-5, '0s'],
    [Number.NaN, '0s'],
  ])('formats %s seconds as %s', (seconds, expected) => {
    expect(formatRunClock(seconds)).toBe(expected)
  })
})

describe('formatTimeLong', () => {
  it.each([
    [41, '41s'],
    [360, '6m'],
    [363, '6m 3s'],
    [7200, '2h'],
    [7241, '2h 0m 41s'],
    [4612, '1h 16m 52s'],
    [5400, '1h 30m'],
    [41.6, '42s'],
  ])('formats %s seconds as %s', (seconds, expected) => {
    expect(formatTimeLong(seconds)).toBe(expected)
  })
})
