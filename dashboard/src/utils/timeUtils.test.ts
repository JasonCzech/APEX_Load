import { describe, expect, it } from 'vitest'

import { parseDurationToSeconds } from './timeUtils'

describe('timeUtils', () => {
  it('parses Go-style millisecond durations without treating ms as minutes', () => {
    expect(parseDurationToSeconds('60ms')).toBe(1)
    expect(parseDurationToSeconds('1500ms')).toBe(2)
    expect(parseDurationToSeconds('1m30s')).toBe(90)
  })
})
