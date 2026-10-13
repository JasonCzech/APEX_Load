import { COMMON_TIMEZONES, formatTimeZoneClock, formatTimeZoneOffset } from '../newTestScheduleFormat'

const FALLBACK_TIMEZONES = ['UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Asia/Tokyo', 'Asia/Kolkata', 'Australia/Sydney']

/** Every IANA zone the browser knows, or a short list when it cannot say. */
export const ALL_TIMEZONES: readonly string[] = (() => {
  const extendedIntl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  try {
    return extendedIntl.supportedValuesOf?.('timeZone') || FALLBACK_TIMEZONES
  } catch {
    return FALLBACK_TIMEZONES
  }
})()

const SORTED_TIMEZONES = [...ALL_TIMEZONES].sort()

/**
 * The zones a picker offers, in order: the current value, the viewer's own
 * zone, UTC and the common zones, then every other zone alphabetically.
 */
export function timezoneChoices(current: string, viewer: string): string[] {
  return Array.from(new Set([current, viewer, 'UTC', ...COMMON_TIMEZONES, ...SORTED_TIMEZONES].filter(Boolean)))
}

/** The zone typed text names exactly, ignoring case and with spaces for underscores; null when none. */
export function resolveTimezone(text: string, choices: readonly string[]): string | null {
  const wanted = text.trim().replace(/ /g, '_').toLowerCase()
  if (!wanted) return null
  return choices.find(zone => zone.toLowerCase() === wanted) ?? null
}

// Every picker on a page labels the same zones for the same minute: compute
// them once per minute and viewer. Two minutes are kept, since a picker
// driven by a schedule clock can trail the others by one.
const labelCaches = new Map<string, Map<string, string>>()

/** "UTC-05:00 · 01:30 now", and "your zone" for the viewer's. */
export function timezoneChoiceLabel(zone: string, at: Date, viewer: string): string {
  const key = `${Math.floor(at.getTime() / 60_000)}|${viewer}`
  let labels = labelCaches.get(key)
  if (!labels) {
    labels = new Map()
    labelCaches.set(key, labels)
    if (labelCaches.size > 2) labelCaches.delete(labelCaches.keys().next().value as string)
  }
  let label = labels.get(zone)
  if (label === undefined) {
    label = `${formatTimeZoneOffset(at, zone)} · ${formatTimeZoneClock(at, zone)} now${zone === viewer ? ' · your zone' : ''}`
    labels.set(zone, label)
  }
  return label
}
