import { CapacityTimeslot, LaunchConflict, LaunchPreflightResponse } from '../types'

// Pure schedule / launch-window / timezone / cron formatters extracted from
// NewTestPage. These are stateless helpers with their own unit-test surface in
// newTestScheduleFormat.test.ts.

export const RESERVATION_INCREMENT_MINUTES = 15

export function formatScheduleTimestamp(value?: string) {
  return value ? new Date(value).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : 'Not scheduled'
}

export function formatScheduleTimestampInZone(value: string, timeZone?: string) {
  try {
    return new Date(value).toLocaleString(undefined, { timeZone: timeZone || 'UTC', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })
  } catch {
    return formatScheduleTimestamp(value)
  }
}

export function formatLaunchTimestamp(value?: string) {
  return value ? new Date(value).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : 'Pending'
}

export function formatLaunchWindow(preflight?: LaunchPreflightResponse | null) {
  const window = preflight?.candidate_window
  if (!window) return 'Pending preflight'
  return `${formatLaunchTimestamp(window.start_at)} - ${formatLaunchTimestamp(window.end_at)}`
}

export function ceilDateToReservationIncrement(value: Date) {
  const rounded = new Date(value)
  const atBoundary = rounded.getMinutes() % RESERVATION_INCREMENT_MINUTES === 0 && rounded.getSeconds() === 0 && rounded.getMilliseconds() === 0
  if (atBoundary) return rounded
  const nextMinute = Math.floor(rounded.getMinutes() / RESERVATION_INCREMENT_MINUTES) * RESERVATION_INCREMENT_MINUTES + RESERVATION_INCREMENT_MINUTES
  rounded.setSeconds(0, 0)
  if (nextMinute >= 60) {
    rounded.setHours(rounded.getHours() + 1, 0, 0, 0)
  } else {
    rounded.setMinutes(nextMinute)
  }
  return rounded
}

export function reservationStartISO(preflight?: LaunchPreflightResponse | null) {
  const candidate = preflight?.candidate_window?.start_at
  const candidateDate = candidate ? new Date(candidate) : new Date()
  const sourceDate = Number.isFinite(candidateDate.getTime()) ? candidateDate : new Date()
  return ceilDateToReservationIncrement(sourceDate).toISOString()
}

export function formatTimeslotLabel(slot: CapacityTimeslot) {
  const start = new Date(slot.start_at)
  const end = new Date(slot.end_at)
  const window = Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())
    ? `${slot.start_at} → ${slot.end_at}`
    : `${start.toLocaleString()} → ${end.toLocaleString()}`
  const gen = slot.requested_capacity?.generators ? ` · ${slot.requested_capacity.generators} gen` : ''
  return `${window}${gen}`
}

export function formatReservationWindow(preflight?: LaunchPreflightResponse | null) {
  const window = preflight?.candidate_window
  if (!window) return 'Pending preflight'
  const startISO = reservationStartISO(preflight)
  const start = new Date(startISO)
  const end = new Date(start.getTime() + window.duration_seconds * 1000)
  return `${formatLaunchTimestamp(startISO)} - ${formatLaunchTimestamp(end.toISOString())}`
}

export function formatLaunchDecision(preflight?: LaunchPreflightResponse | null, loading = false) {
  if (loading) return 'Checking'
  if (!preflight) return 'Pending preflight'
  if (preflight.start_now_allowed) return 'Reserve'
  if (preflight.queue_allowed) return 'Queue'
  if (preflight.schedule_allowed) return 'Schedule'
  return 'Blocked'
}

export function firstLaunchConflict(preflight?: LaunchPreflightResponse | null): LaunchConflict | undefined {
  return preflight?.blocking_conflicts?.[0] || preflight?.warnings?.[0]
}

export function formatLaunchConflict(preflight?: LaunchPreflightResponse | null) {
  const conflict = firstLaunchConflict(preflight)
  if (!conflict) return 'None'
  return conflict.message
}

export function formatNextBlockingEvent(preflight?: LaunchPreflightResponse | null) {
  const conflicts = preflight?.blocking_conflicts || []
  const dated = conflicts
    .filter(conflict => conflict.starts_at)
    .sort((left, right) => String(left.starts_at).localeCompare(String(right.starts_at)))
  const first = dated[0]
  return first ? `${formatLaunchTimestamp(first.starts_at)} - ${first.message}` : 'None'
}

export function formatDatetimeLocalValue(date: Date) {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function datetimeLocalToISO(value: string) {
  if (!value) return ''
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '' : parsed.toISOString()
}

export function formatUtcScheduleClock(date: Date) {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(date)
}

export function formatTimeZoneClock(date: Date, timeZone: string) {
  try {
    return new Intl.DateTimeFormat('en-US', {
      timeZone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(date)
  } catch {
    return formatUtcScheduleClock(date)
  }
}

export function formatTimeZoneOffset(date: Date, timeZone: string) {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(date)
    const partValue = (type: string) => Number(parts.find(part => part.type === type)?.value || 0)
    const zonedTime = Date.UTC(
      partValue('year'),
      partValue('month') - 1,
      partValue('day'),
      partValue('hour'),
      partValue('minute'),
      partValue('second'),
    )
    const offsetMinutes = Math.round((zonedTime - date.getTime()) / 60000)
    const sign = offsetMinutes >= 0 ? '+' : '-'
    const absoluteMinutes = Math.abs(offsetMinutes)
    const hours = String(Math.floor(absoluteMinutes / 60)).padStart(2, '0')
    const minutes = String(absoluteMinutes % 60).padStart(2, '0')
    return `UTC${sign}${hours}:${minutes}`
  } catch {
    return 'UTC+00:00'
  }
}

export function formatTimeZoneOptionLabel(timeZone: string, date: Date) {
  return `${timeZone.replace(/_/g, ' ')} - ${formatTimeZoneClock(date, timeZone)} ${formatTimeZoneOffset(date, timeZone)}`
}

export function formatScheduleToken(value?: string) {
  return value ? value.replace(/_/g, ' ') : ''
}

export function formatScheduleWindow(window: { name?: string, start_time: string, end_time: string, timezone?: string }) {
  const label = window.name ? `${window.name}: ` : ''
  return `${label}${window.start_time}-${window.end_time}${window.timezone ? ` ${window.timezone}` : ''}`
}

export function scheduleStatusTone(value?: string) {
  if (!value) return ''
  if (['blocked', 'failed', 'rejected'].includes(value)) return 'danger'
  if (['approval_required', 'operator_decision_required', 'pending', 'skipped'].includes(value)) return 'warning'
  if (['materialized', 'queued', 'approved', 'completed', 'enabled'].includes(value)) return 'healthy'
  return ''
}

export const COMMON_TIMEZONES = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Phoenix',
  'America/Anchorage',
  'Pacific/Honolulu',
  'America/Toronto',
  'America/Vancouver',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Amsterdam',
  'Europe/Stockholm',
  'Europe/Moscow',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Australia/Sydney',
  'Australia/Melbourne',
  'Pacific/Auckland',
]

const CRON_MACROS: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
}

export function describeCron(expr: string): string {
  const trimmed = expr.trim()
  const expanded = CRON_MACROS[trimmed.toLowerCase()] ?? trimmed
  const parts = expanded.split(/\s+/)
  if (parts.length !== 5) return ''
  const [minute, hour, dom, month, dow] = parts

  const minuteStep = /^\*\/(\d+)$/.exec(minute)
  if (minuteStep && Number(minuteStep[1]) > 0 && hour === '*' && dom === '*' && month === '*' && dow === '*') {
    return `Every ${Number(minuteStep[1])} minutes`
  }
  const hourStep = /^\*\/(\d+)$/.exec(hour)
  if (hourStep && Number(hourStep[1]) > 0 && /^\d+$/.test(minute) && dom === '*' && month === '*' && dow === '*') {
    return `Every ${Number(hourStep[1])} hours at minute ${Number(minute)}`
  }

  const dowNames: Record<string, string> = { '0': 'Sun', '1': 'Mon', '2': 'Tue', '3': 'Wed', '4': 'Thu', '5': 'Fri', '6': 'Sat', '7': 'Sun' }
  const monthNames: Record<string, string> = { '1': 'Jan', '2': 'Feb', '3': 'Mar', '4': 'Apr', '5': 'May', '6': 'Jun', '7': 'Jul', '8': 'Aug', '9': 'Sep', '10': 'Oct', '11': 'Nov', '12': 'Dec' }

  const formatTime = (h: string, m: string) => {
    const hNum = parseInt(h, 10)
    const mNum = parseInt(m, 10)
    if (isNaN(hNum) || isNaN(mNum)) return null
    const suffix = hNum >= 12 ? 'PM' : 'AM'
    const displayH = hNum === 0 ? 12 : hNum > 12 ? hNum - 12 : hNum
    return `${displayH}:${String(mNum).padStart(2, '0')} ${suffix}`
  }

  const describeDow = (field: string) => {
    if (field === '*') return ''
    return field.split(',').map(part => {
      if (part.includes('-')) {
        const [start, end] = part.split('-')
        return `${dowNames[start] || start}–${dowNames[end] || end}`
      }
      return dowNames[part] || part
    }).join(', ')
  }

  const describeMonth = (field: string) => {
    if (field === '*') return ''
    return field.split(',').map(part => monthNames[part] || part).join(', ')
  }

  const time = formatTime(hour, minute)
  const dowDesc = describeDow(dow)
  const monthDesc = describeMonth(month)
  const domDesc = dom !== '*' ? `on day ${dom}` : ''

  const segments: string[] = []
  if (time) segments.push(`At ${time}`)
  else if (hour === '*' && minute === '*') segments.push('Every minute')
  else if (hour === '*') segments.push(`At minute ${minute} of every hour`)
  else segments.push(`At ${hour}:${String(minute).padStart(2, '0')}`)

  if (dowDesc && domDesc) segments.push(`${domDesc} or ${dowDesc}`)
  else {
    if (dowDesc) segments.push(dowDesc)
    if (domDesc) segments.push(domDesc)
  }
  if (monthDesc) segments.push(`in ${monthDesc}`)

  return segments.join(', ')
}
