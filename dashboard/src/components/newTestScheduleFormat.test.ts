import { describe, it, expect } from 'vitest'
import type { CapacityTimeslot, LaunchConflict, LaunchPreflightResponse } from '../types'
import { formatDateTime } from '../utils/dateTime'
import {
  RESERVATION_INCREMENT_MINUTES,
  formatScheduleInstant,
  formatScheduleClockChange,
  formatScheduleInstantForViewer,
  COMMON_TIMEZONES,
  ceilDateToReservationIncrement,
  datetimeLocalToISO,
  describeCron,
  firstLaunchConflict,
  startNowBlocker,
  formatDatetimeLocalValue,
  formatLaunchConflict,
  formatLaunchDecision,
  formatLaunchWindow,
  formatReservationWindow,
  formatScheduleToken,
  formatScheduleTimestampInZone,
  formatScheduleWindow,
  formatTimeslotLabel,
  formatTimeZoneOffset,
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

  it('describes POSIX day-of-month and weekday matching as OR semantics', () => {
    expect(describeCron('0 9 1 * 1')).toBe('At 9:00 AM, on day 1 or Mon')
  })

  it('describes every-minute wildcards', () => {
    expect(describeCron('* * * * *')).toBe('Every minute')
  })

  it('describes stepped minute and hour schedules', () => {
    expect(describeCron('*/15 * * * *')).toBe('Every 15 minutes')
    expect(describeCron('5 */4 * * *')).toBe('Every 4 hours at minute 5')
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

describe('formatTimeslotLabel', () => {
  const startAt = '2026-07-01T13:00:00Z'
  const endAt = '2026-07-01T17:00:00Z'
  // Pick a zone whose wall-clock differs from the runner's local zone so the
  // assertions stay meaningful wherever the suite runs.
  const zone = ['Asia/Tokyo', 'America/Los_Angeles']
    .find(candidate => new Date(startAt).toLocaleString(undefined, { timeZone: candidate }) !== new Date(startAt).toLocaleString()) as string

  it('renders the window in the reservation timezone and names that zone', () => {
    const label = formatTimeslotLabel({ start_at: startAt, end_at: endAt, timezone: zone } as CapacityTimeslot)
    expect(label).toContain(formatDateTime(startAt, { timeZone: zone, seconds: true }))
    expect(label).toContain(formatDateTime(endAt, { timeZone: zone, seconds: true }))
    expect(label).toContain(zone)
    expect(label).not.toContain(formatDateTime(startAt, { seconds: true }))
  })

  it('falls back to the browser zone when the slot carries none', () => {
    const label = formatTimeslotLabel({ start_at: startAt, end_at: endAt } as CapacityTimeslot)
    expect(label).toBe(`${formatDateTime(startAt, { seconds: true })} → ${formatDateTime(endAt, { seconds: true })}`)
  })

  it('keeps raw values and capacity for an unparseable window', () => {
    const label = formatTimeslotLabel({ start_at: 'nope', end_at: 'nope', timezone: zone, requested_capacity: { generators: 3 } } as CapacityTimeslot)
    expect(label).toBe(`nope → nope ${zone} · 3 gen`)
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

  it('exposes a UTC-first timezone list', () => {
    expect(COMMON_TIMEZONES[0]).toBe('UTC')
    expect(COMMON_TIMEZONES).toContain('Asia/Tokyo')
  })
})

it('keeps recurring wall-clock labels stable across the daylight-saving boundary', () => {
  const zone = 'America/New_York'
  expect(formatTimeZoneOffset(new Date('2026-03-07T14:00:00Z'), zone)).toBe('UTC-05:00')
  expect(formatTimeZoneOffset(new Date('2026-03-08T13:00:00Z'), zone)).toBe('UTC-04:00')
  expect(formatScheduleTimestampInZone('2026-03-07T14:00:00Z', zone)).toContain('9:00')
  expect(formatScheduleTimestampInZone('2026-03-08T13:00:00Z', zone)).toContain('9:00')
})

it('shows iteration completion without inventing a capacity deadline', () => {
  const result = preflight({ candidate_window: { start_at: '2026-09-23T12:00:00Z', open_ended: true, duration_seconds: 0 } })
  expect(formatLaunchWindow(result)).toContain('until all iterations complete')
  expect(formatReservationWindow(result)).toContain('cannot reserve')
})

describe('formatScheduleInstant', () => {
  it('formats in the schedule timezone, not the browser timezone', () => {
    // 06:00Z is 2:00 AM in New York (EDT), whatever the browser zone is.
    expect(formatScheduleInstant('2026-10-08T06:00:00Z', 'America/New_York')).toMatch(/2:00\s?AM/)
    expect(formatScheduleInstant(undefined, 'America/New_York')).toBe('Not scheduled')
  })
})

describe('describeCron out-of-range fields', () => {
  it('describes nothing rather than a garbage time', () => {
    expect(describeCron('99 87 * * *')).toBe('')
  })
})

describe('startNowBlocker', () => {
  const base: LaunchPreflightResponse = { decision: 'blocked', start_now_allowed: false, schedule_allowed: false, queue_allowed: false, candidate_window: { start_at: '', end_at: '', duration_seconds: 60 } } as LaunchPreflightResponse
  it('names the first blocking conflict and marks run-size caps', () => {
    expect(startNowBlocker({ ...base, blocking_conflicts: [{ code: 'exceeds_project_vuser_cap', severity: 'blocking', message: 'Too big.' } as LaunchConflict] })).toEqual({ message: 'Too big.', overCap: true, queueable: false })
    expect(startNowBlocker({ ...base, blocking_conflicts: [{ code: 'capacity_shortfall', severity: 'blocking', message: 'Short.' } as LaunchConflict] })).toEqual({ message: 'Short.', overCap: false, queueable: false })
  })
  it('explains a queueable wait and is silent when Start now is available', () => {
    expect(startNowBlocker({ ...base, queue_allowed: true, queue_reason: 'waiting for project VUser capacity (5 max vusers)' })?.message).toBe('Not enough capacity right now: waiting for project VUser capacity (5 max VUsers). Create queued run starts it as soon as capacity frees up.')
    // No pointer to Create queued run while that button can't be used.
    expect(startNowBlocker({ ...base, queue_allowed: true, queue_reason: 'waiting for a global test slot.' }, { queueAvailable: false })?.message).toBe('Not enough capacity right now: waiting for a global test slot.')
    expect(startNowBlocker({ ...base, start_now_allowed: true })).toBeNull()
    expect(startNowBlocker(null)).toBeNull()
  })
})


describe('schedule times across zones and clock changes', () => {
  it('names a skipped spring-forward time and a repeated fall-back one', () => {
    expect(formatScheduleClockChange({ date: '2027-03-14', kind: 'skipped', local_times: ['02:30'], matches: 1, at: '2027-03-14T08:00:00Z' }, 'America/Chicago'))
      .toBe("Sun, Mar 14, 2027: 2:30 AM doesn't exist (clocks go forward), so that run is skipped.")
    expect(formatScheduleClockChange({ date: '2027-03-14', kind: 'skipped', local_times: ['02:00', '02:10', '02:20'], matches: 6, at: '2027-03-14T08:00:00Z' }, 'America/Chicago'))
      .toBe("Sun, Mar 14, 2027: 2:00 AM, 2:10 AM, 2:20 AM and 3 more don't exist (clocks go forward), so those 6 runs are skipped.")
    expect(formatScheduleClockChange({ date: '2026-11-01', kind: 'repeated', local_times: ['01:30'], matches: 1, runs_at: '2026-11-01T06:30:00Z', at: '2026-11-01T07:00:00Z' }, 'America/Chicago'))
      .toBe('Sun, Nov 1, 2026: 1:30 AM happens twice (clocks go back); it runs once, at the first (Nov 1, 2026, 1:30 AM CDT).')
    expect(formatScheduleClockChange({ date: '2026-11-01', kind: 'repeated', local_times: ['01:00', '01:30'], matches: 2, runs_at: '2026-11-01T06:00:00Z', at: '2026-11-01T07:00:00Z' }, 'America/Chicago'))
      .toBe('Sun, Nov 1, 2026: 1:00 AM and 1:30 AM happen twice (clocks go back); each runs once, at the first.')
  })

  it("shows a run in the schedule's zone, with the viewer's beside it when that differs", () => {
    expect(formatScheduleInstantForViewer('2026-10-12T06:30:00Z', 'America/Chicago', 'America/Chicago')).toBe('Oct 12, 2026, 1:30 AM CDT')
    expect(formatScheduleInstantForViewer('2026-10-12T06:30:00Z', 'America/Chicago', 'America/New_York')).toBe('Oct 12, 2026, 1:30 AM CDT (2:30 AM EDT your time)')
    expect(formatScheduleInstantForViewer('2026-10-12T04:30:00Z', 'America/Chicago', 'UTC')).toBe('Oct 11, 2026, 11:30 PM CDT (Oct 12, 4:30 AM UTC your time)')
    expect(formatScheduleInstantForViewer(undefined, 'UTC', 'UTC')).toBe('Not scheduled')
  })
})
