export interface DateTimeDisplayOptions {
  /** IANA zone to render in; the viewer's zone when omitted. */
  timeZone?: string
  seconds?: boolean
  /** Evidence views: seconds with milliseconds. */
  milliseconds?: boolean
  /** Show the zone abbreviation (default), so a reader never guesses the clock. */
  zone?: boolean
  /** Shown for a missing value. */
  fallback?: string
}

function toDate(value: string | number | Date): Date {
  return value instanceof Date ? value : new Date(value)
}

/**
 * The one date-time display: "Oct 8, 2026, 6:12 PM CDT". Year and minutes are
 * always shown, seconds on request, and the zone abbreviation by default.
 * Unparseable text is returned as-is rather than hidden.
 */
export function formatDateTime(value: string | number | Date | null | undefined, options: DateTimeDisplayOptions = {}): string {
  const { timeZone, seconds = false, milliseconds = false, zone = true, fallback = '—' } = options
  if (value === undefined || value === null || value === '') return fallback
  const date = toDate(value)
  if (Number.isNaN(date.getTime())) return typeof value === 'string' ? value : fallback
  const format: Intl.DateTimeFormatOptions = {
    year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
    ...(seconds || milliseconds ? { second: '2-digit' } : {}),
    ...(milliseconds ? { fractionalSecondDigits: 3 } : {}),
    ...(zone ? { timeZoneName: 'short' } : {}),
  }
  try {
    return date.toLocaleString(undefined, timeZone ? { ...format, timeZone } : format)
  } catch {
    // An unknown zone name falls back to the viewer's clock, still labelled.
    return date.toLocaleString(undefined, format)
  }
}

/** A calendar date in the same style: "Oct 8, 2026". */
export function formatDateOnly(value: string | number | Date | null | undefined, options: Pick<DateTimeDisplayOptions, 'timeZone' | 'fallback'> = {}): string {
  const { timeZone, fallback = '—' } = options
  if (value === undefined || value === null || value === '') return fallback
  const date = toDate(value)
  if (Number.isNaN(date.getTime())) return typeof value === 'string' ? value : fallback
  const format: Intl.DateTimeFormatOptions = { year: 'numeric', month: 'short', day: 'numeric' }
  try {
    return date.toLocaleDateString(undefined, timeZone ? { ...format, timeZone } : format)
  } catch {
    return date.toLocaleDateString(undefined, format)
  }
}
