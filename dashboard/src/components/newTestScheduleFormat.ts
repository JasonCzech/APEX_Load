import { CapacityTimeslot, LaunchConflict, LaunchPreflightResponse } from '../types'
import type { ScheduleClockChange } from '../types/executionPlans'
import { formatDateTime } from '../utils/dateTime'

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

/** A schedule instant in a zone with the zone's short name, e.g. "Oct 12, 2026, 1:30 AM CDT". */
function formatScheduleTimestampWithZone(value: string, timeZone?: string) {
  try {
    return new Date(value).toLocaleString(undefined, { timeZone: timeZone || 'UTC', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' })
  } catch {
    return formatScheduleTimestamp(value)
  }
}

let cachedViewerTimeZone: string | undefined

/** The viewer's own timezone, as the browser reports it (read once). */
export function viewerTimeZone(): string {
  if (cachedViewerTimeZone === undefined) {
    try {
      cachedViewerTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
    } catch {
      cachedViewerTimeZone = 'UTC'
    }
  }
  return cachedViewerTimeZone
}

/**
 * A schedule instant in the schedule's zone, and in the viewer's beside it
 * when that differs: "Oct 12, 2026, 1:30 AM CDT (2:30 AM EDT your time)".
 */
export function formatScheduleInstantForViewer(value: string | undefined, timeZone?: string, viewerZone = viewerTimeZone()) {
  if (!value) return 'Not scheduled'
  const inSchedule = formatScheduleTimestampWithZone(value, timeZone)
  if ((timeZone || 'UTC') === viewerZone) return inSchedule
  let mine: string
  try {
    // The calendar date in a zone, as YYYY-MM-DD.
    const zonedDate = (zone: string) => new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value))
    mine = new Date(value).toLocaleString(undefined, zonedDate(timeZone || 'UTC') === zonedDate(viewerZone)
      ? { timeZone: viewerZone, hour: 'numeric', minute: '2-digit', timeZoneName: 'short' }
      : { timeZone: viewerZone, month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short' })
  } catch {
    return inSchedule
  }
  return `${inSchedule} (${mine} your time)`
}

/** "Sun, Mar 14, 2027" for a YYYY-MM-DD calendar date. */
function formatCalendarDate(date: string) {
  const parsed = new Date(`${date}T12:00:00Z`)
  return Number.isNaN(parsed.getTime()) ? date : parsed.toLocaleDateString(undefined, { timeZone: 'UTC', weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' })
}

/** "2:30 AM" for a HH:MM wall-clock time. */
function formatWallClock(time: string) {
  const [hours, minutes] = time.split(':').map(Number)
  if (!Number.isInteger(hours) || !Number.isInteger(minutes)) return time
  return new Date(Date.UTC(2000, 0, 1, hours, minutes)).toLocaleTimeString(undefined, { timeZone: 'UTC', hour: 'numeric', minute: '2-digit' })
}

/**
 * One sentence naming a daylight-saving change a schedule falls into, e.g.
 * "Sun, Mar 14, 2027: 2:30 AM doesn't exist (clocks go forward), so that run is skipped."
 */
export function formatScheduleClockChange(change: ScheduleClockChange, timeZone?: string) {
  const times = change.local_times.map(formatWallClock)
  const more = change.matches - times.length
  const list = more > 0 ? `${times.join(', ')} and ${more} more`
    : times.length > 1 ? `${times.slice(0, -1).join(', ')} and ${times[times.length - 1]}`
      : times[0] || ''
  const one = change.matches === 1
  const date = formatCalendarDate(change.date)
  if (change.kind === 'skipped') {
    return `${date}: ${list} ${one ? "doesn't" : "don't"} exist (clocks go forward), so ${one ? 'that run is' : `those ${change.matches} runs are`} skipped.`
  }
  const first = one && change.runs_at ? ` (${formatScheduleTimestampWithZone(change.runs_at, timeZone)})` : ''
  return `${date}: ${list} ${one ? 'happens' : 'happen'} twice (clocks go back); ${one ? 'it runs' : 'each runs'} once, at the first${first}.`
}

/** A schedule instant in the schedule's own timezone, so it matches the zone shown beside it. */
export function formatScheduleInstant(value: string | undefined, timeZone?: string) {
  return value ? formatScheduleTimestampInZone(value, timeZone) : 'Not scheduled'
}

export function formatLaunchTimestamp(value?: string) {
  if (!value) return 'Pending'
  const date = new Date(value)
  // The year is shown whenever it is not this year, so a window can't be misread.
  const year = date.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' as const } : {}
  return date.toLocaleString(undefined, { month: 'short', day: 'numeric', ...year, hour: 'numeric', minute: '2-digit' })
}

export function formatLaunchWindow(preflight?: LaunchPreflightResponse | null) {
  const window = preflight?.candidate_window
  if (!window) return 'Pending preflight'
  if (window.open_ended) return `${formatLaunchTimestamp(window.start_at)} — until all iterations complete`
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

// formatDateTime falls back to the viewer's clock, still labelled, for an
// unknown zone name.
function formatTimeslotInstant(date: Date, timeZone?: string) {
  return formatDateTime(date, { timeZone, seconds: true })
}

export function formatTimeslotLabel(slot: CapacityTimeslot) {
  const start = new Date(slot.start_at)
  const end = new Date(slot.end_at)
  // Render the window in the reservation's own IANA zone and name that zone, so
  // the label never shows a browser-local time beside a foreign zone.
  const window = Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())
    ? `${slot.start_at} → ${slot.end_at}`
    : `${formatTimeslotInstant(start, slot.timezone)} → ${formatTimeslotInstant(end, slot.timezone)}`
  const zone = slot.timezone ? ` ${slot.timezone}` : ''
  const gen = slot.requested_capacity?.generators ? ` · ${slot.requested_capacity.generators} gen` : ''
  return `${window}${zone}${gen}`
}

export function formatReservationWindow(preflight?: LaunchPreflightResponse | null) {
  const window = preflight?.candidate_window
  if (!window) return 'Pending preflight'
  if (window.open_ended) return 'Iteration runs cannot reserve a duration-based timeslot'
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

/** Run-size limits a run exceeds on its own; it can never start under them. */
const OVER_CAP_CONFLICTS = new Set(['exceeds_project_vuser_cap', 'exceeds_pool_vuser_cap'])

/**
 * Why Start now is unavailable, to say beside it, or null while it is
 * available or not yet checked. overCap marks a run larger than a VUser cap,
 * which no amount of waiting fixes; queueable marks a run that will start
 * once other runs free capacity.
 */
export function startNowBlocker(preflight?: LaunchPreflightResponse | null, options: { queueAvailable?: boolean } = {}): { message: string; overCap: boolean; queueable: boolean } | null {
  if (!preflight || preflight.start_now_allowed) return null
  const blocking = preflight.blocking_conflicts?.[0]
  if (blocking) return { message: blocking.message, overCap: OVER_CAP_CONFLICTS.has(blocking.code), queueable: false }
  if (preflight.queue_allowed) {
    const reason = (preflight.queue_reason || '').trim().replace(/\bvusers\b/gi, 'VUsers').replace(/[.\s]+$/, '')
    const wait = reason ? `Not enough capacity right now: ${reason}.` : 'Other runs are using the capacity this run needs.'
    return {
      // Only point at Create queued run while that button can be used.
      message: options.queueAvailable === false ? wait : `${wait} Create queued run starts it as soon as capacity frees up.`,
      overCap: false,
      queueable: true,
    }
  }
  return { message: preflight.warnings?.[0]?.message || 'This run can\'t start right now.', overCap: false, queueable: false }
}

export function formatLaunchConflict(preflight?: LaunchPreflightResponse | null) {
  const conflict = firstLaunchConflict(preflight)
  if (!conflict) return 'None'
  return conflict.message
}

/** Every launch conflict's message as a sentence, blocking ones first, or None. */
export function formatLaunchConflicts(preflight?: LaunchPreflightResponse | null) {
  const messages = [...(preflight?.blocking_conflicts || []), ...(preflight?.warnings || [])]
    .map(conflict => (conflict.message || '').trim())
    .filter(Boolean)
    .map(message => (/[.!?]$/.test(message) ? message : `${message}.`))
  return messages.length ? messages.join(' ') : 'None'
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

// One formatter per zone and purpose: zone pickers label every IANA zone.
const zoneFormatterCache = new Map<string, Intl.DateTimeFormat>()
function zoneFormatter(kind: 'clock' | 'parts', timeZone: string) {
  const key = `${kind}:${timeZone}`
  let formatter = zoneFormatterCache.get(key)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-US', kind === 'clock'
      ? { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
      : { timeZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
    zoneFormatterCache.set(key, formatter)
  }
  return formatter
}

export function formatTimeZoneClock(date: Date, timeZone: string) {
  try {
    return zoneFormatter('clock', timeZone).format(date)
  } catch {
    return formatUtcScheduleClock(date)
  }
}

export function formatTimeZoneOffset(date: Date, timeZone: string) {
  try {
    const parts = zoneFormatter('parts', timeZone).formatToParts(date)
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
  // An invalid field means no description; the form reports the error itself.
  const outOfRange = (field: string, min: number, max: number) => /^\d+$/.test(field) && (Number(field) < min || Number(field) > max)
  if (outOfRange(minute, 0, 59) || outOfRange(hour, 0, 23) || outOfRange(dom, 1, 31) || outOfRange(month, 1, 12) || outOfRange(dow, 0, 7)) return ''

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
    // An out-of-range field is an invalid expression, never "87:99 PM".
    if (isNaN(hNum) || isNaN(mNum) || hNum < 0 || hNum > 23 || mNum < 0 || mNum > 59) return null
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
