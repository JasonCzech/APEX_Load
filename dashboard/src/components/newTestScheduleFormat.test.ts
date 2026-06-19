import { describe, it, expect } from 'vitest'
import type { LaunchConflict, LaunchPreflightResponse } from '../types'
import {
  RESERVATION_INCREMENT_MINUTES,
  COMMON_TIMEZONES,
  ceilDateToReservationIncrement,
  datetimeLocalToISO,
  describeCron,
  firstLaunchConflict,
  formatDatetimeLocalValue,
  formatLaunchConflict,
  formatLaunchDecision,
  formatLaunchWindow,
  formatReservationWindow,
  formatScheduleToken,
  formatScheduleWindow,
  formatTimeZoneOffset,
  formatTimeZoneOptionLabel,
  scheduleStatusTone,
} from './newTestScheduleFormat'

const preflight = (overrides: Partial<LaunchPreflightResponse>): LaunchPreflightResponse =>
  ({ ...overrides } as LaunchPreflightResponse)

describe('describeCron', () => {
  it('returns empty for malformed expressions', () => {
    expect(describeCron('')).toBe('')
    expect(describeCron('* * *')).toBe('')
  })

  it('describes a daily morning schedule', () => {
    expect(describeCron('30 9 * * *')).toBe('At 9:30 AM')
  })

  it('describes weekday ranges and months', () => {
    expect(describeCron('0 14 * * 1-5')).toBe('At 2:00 PM, Mon–Fri')
    expect(describeCron('0 0 1 1 *')).toBe('At 12:00 AM, on day 1, in Jan')
  })

  it('describes every-minute wildcards', () => {
    expect(describeCron('* * * * *')).toBe('Every minute')
  })
})

describe('datetimeLocalToISO', () => {
  it('returns empty for blank or invalid input', () => {
    expect(datetimeLocalToISO('')).toBe('')
    expect(datetimeLocalToISO('not-a-date')).toBe('')
  })

  it('round-trips a valid local datetime to ISO', () => {
    const iso = datetimeLocalToISO('2026-06-18T10:30')
    expect(iso).toMatch(/^2026-06-18T\d{2}:30:00\.000Z$/)
  })
})

describe('formatScheduleToken / formatScheduleWindow', () => {
  it('replaces underscores in tokens', () => {
    expect(formatScheduleToken('approval_required')).toBe('approval required')
    expect(formatScheduleToken(undefined)).toBe('')
  })

  it('formats a schedule window with optional name and timezone', () => {
    expect(formatScheduleWindow({ start_time: '08:00', end_time: '17:00' })).toBe('08:00-17:00')
    expect(formatScheduleWindow({ name: 'Peak', start_time: '08:00', end_time: '17:00', timezone: 'UTC' }))
      .toBe('Peak: 08:00-17:00 UTC')
  })
})

describe('scheduleStatusTone', () => {
  it('maps statuses to tones', () => {
    expect(scheduleStatusTone('blocked')).toBe('danger')
    expect(scheduleStatusTone('pending')).toBe('warning')
    expect(scheduleStatusTone('completed')).toBe('healthy')
    expect(scheduleStatusTone('mystery')).toBe('')
    expect(scheduleStatusTone(undefined)).toBe('')
  })
})

describe('formatLaunchDecision', () => {
  it('prioritizes loading, then allow flags, then blocked', () => {
    expect(formatLaunchDecision(null, true)).toBe('Checking')
    expect(formatLaunchDecision(null)).toBe('Pending preflight')
    expect(formatLaunchDecision(preflight({ start_now_allowed: true }))).toBe('Reserve')
    expect(formatLaunchDecision(preflight({ queue_allowed: true }))).toBe('Queue')
    expect(formatLaunchDecision(preflight({ schedule_allowed: true }))).toBe('Schedule')
    expect(formatLaunchDecision(preflight({}))).toBe('Blocked')
  })
})

describe('launch conflict helpers', () => {
  it('prefers blocking conflicts then warnings', () => {
    const blocking = { message: 'no capacity' } as LaunchConflict
    const warning = { message: 'tight window' } as LaunchConflict
    expect(firstLaunchConflict(preflight({ blocking_conflicts: [blocking], warnings: [warning] }))).toBe(blocking)
    expect(firstLaunchConflict(preflight({ warnings: [warning] }))).toBe(warning)
    expect(formatLaunchConflict(preflight({ blocking_conflicts: [blocking] }))).toBe('no capacity')
    expect(formatLaunchConflict(preflight({}))).toBe('None')
  })
})

describe('window formatters return pending without preflight', () => {
  it('handles null preflight', () => {
    expect(formatLaunchWindow(null)).toBe('Pending preflight')
    expect(formatReservationWindow(null)).toBe('Pending preflight')
  })
})

describe('ceilDateToReservationIncrement', () => {
  it('keeps a date already on a 15-minute boundary', () => {
    const onBoundary = new Date(2026, 5, 18, 10, 15, 0, 0)
    expect(ceilDateToReservationIncrement(onBoundary).getMinutes()).toBe(15)
  })

  it('rounds up to the next 15-minute boundary', () => {
    const rounded = ceilDateToReservationIncrement(new Date(2026, 5, 18, 10, 7, 30))
    expect(rounded.getMinutes()).toBe(15)
    expect(rounded.getSeconds()).toBe(0)
  })

  it('rolls over to the next hour', () => {
    const rounded = ceilDateToReservationIncrement(new Date(2026, 5, 18, 10, 52, 0))
    expect(rounded.getHours()).toBe(11)
    expect(rounded.getMinutes()).toBe(0)
  })

  it('uses a 15-minute increment', () => {
    expect(RESERVATION_INCREMENT_MINUTES).toBe(15)
  })
})

describe('formatDatetimeLocalValue', () => {
  it('formats a local date as a datetime-local input value', () => {
    expect(formatDatetimeLocalValue(new Date(2026, 0, 5, 9, 4))).toBe('2026-01-05T09:04')
  })
})

describe('timezone helpers', () => {
  it('reports zero offset for UTC', () => {
    expect(formatTimeZoneOffset(new Date('2026-06-18T00:00:00Z'), 'UTC')).toBe('UTC+00:00')
  })

  it('builds a readable option label with spaces', () => {
    const label = formatTimeZoneOptionLabel('America/New_York', new Date('2026-06-18T00:00:00Z'))
    expect(label).toContain('America/New York')
    expect(label).toContain('UTC-')
  })

  it('exposes a UTC-first timezone list', () => {
    expect(COMMON_TIMEZONES[0]).toBe('UTC')
    expect(COMMON_TIMEZONES).toContain('Asia/Tokyo')
  })
})
