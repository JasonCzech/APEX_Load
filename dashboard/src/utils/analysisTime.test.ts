import { expect, it } from 'vitest'
import { formatAnalysisTimeTick } from './analysisTime'

it.each([
  ['2026-01-01T00:00:01Z', '01-01 00:00:01'],
  ['2026-01-01T00:00:01-05:00', '01-01 05:00:01'],
  ['2026-12-31T23:59:59-01:00', '01-01 00:59:59'],
  ['invalid', '—'],
  [null, '—'],
])('formats %s as a compact UTC chart label', (value, expected) => {
  expect(formatAnalysisTimeTick(value)).toBe(expected)
})
