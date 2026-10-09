import { expect, it } from 'vitest'
import { formatDateOnly, formatDateTime } from './dateTime'

it('renders one consistent, zone-labelled date-time', () => {
  const value = '2026-07-01T13:05:09.123456Z'
  expect(formatDateTime(value, { timeZone: 'UTC' })).toBe('Jul 1, 2026, 1:05 PM UTC')
  expect(formatDateTime(value, { timeZone: 'UTC', seconds: true })).toBe('Jul 1, 2026, 1:05:09 PM UTC')
  expect(formatDateTime(value, { timeZone: 'America/Chicago' })).toBe('Jul 1, 2026, 8:05 AM CDT')
  expect(formatDateTime(value, { timeZone: 'UTC', zone: false })).toBe('Jul 1, 2026, 1:05 PM')
  expect(formatDateTime(value, { timeZone: 'UTC', milliseconds: true })).toBe('Jul 1, 2026, 1:05:09.123 PM UTC')
  // No raw ISO with microseconds reaches the reader.
  expect(formatDateTime(value)).not.toContain('T13:05:09')
})

it('keeps missing and unparseable values visible', () => {
  expect(formatDateTime(undefined)).toBe('—')
  expect(formatDateTime('', { fallback: 'Never' })).toBe('Never')
  expect(formatDateTime('not a date')).toBe('not a date')
  expect(formatDateTime('2026-07-01T00:00:00Z', { timeZone: 'Not/AZone' })).toMatch(/2026/)
  expect(formatDateOnly('2026-07-01T13:00:00Z', { timeZone: 'UTC' })).toBe('Jul 1, 2026')
})
