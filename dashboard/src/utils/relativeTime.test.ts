import { describe, expect, it } from 'vitest'

import { formatRelativeTime } from './relativeTime'

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-06-27T12:00:00Z')

  it('returns empty for missing/invalid input', () => {
    expect(formatRelativeTime(undefined, now)).toBe('')
    expect(formatRelativeTime('', now)).toBe('')
    expect(formatRelativeTime('not-a-date', now)).toBe('')
  })

  it('formats recent and older timestamps coarsely', () => {
    expect(formatRelativeTime('2026-06-27T11:59:50Z', now)).toBe('just now')
    expect(formatRelativeTime('2026-06-27T11:55:00Z', now)).toBe('5m ago')
    expect(formatRelativeTime('2026-06-27T09:00:00Z', now)).toBe('3h ago')
    expect(formatRelativeTime('2026-06-24T12:00:00Z', now)).toBe('3d ago')
    expect(formatRelativeTime('2026-06-10T12:00:00Z', now)).toBe('2w ago')
  })

  it('treats future timestamps as just now', () => {
    expect(formatRelativeTime('2026-06-27T12:05:00Z', now)).toBe('just now')
  })
})
