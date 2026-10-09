const fallbackTimezones = ['UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Asia/Tokyo', 'Asia/Kolkata', 'Australia/Sydney']

const browserTimezones = (() => {
  const extendedIntl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  try {
    return extendedIntl.supportedValuesOf?.('timeZone') || fallbackTimezones
  } catch {
    return fallbackTimezones
  }
})()

function timezoneOptions(current: string): string[] {
  return Array.from(new Set(['UTC', current, ...browserTimezones].filter(Boolean)))
}

/** IANA time zone picker: a list, never free text that can be mistyped. */
export default function TimezoneSelect({ value, onChange, ariaLabel, className }: { value: string; onChange: (value: string) => void; ariaLabel?: string; className?: string }) {
  return <select className={className} aria-label={ariaLabel} value={value} onChange={event => onChange(event.target.value)}>{timezoneOptions(value).map(zone => <option key={zone} value={zone}>{zone}</option>)}</select>
}
