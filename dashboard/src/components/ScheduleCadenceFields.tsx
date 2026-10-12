import {
  COMMON_TIMEZONES,
  formatScheduleTimestampInZone,
  formatTimeZoneOptionLabel,
  formatUtcScheduleClock,
} from './newTestScheduleFormat'

// The cadence pieces every schedule editor shares — scenario schedules in New
// run and execution-plan schedules — so both show the same zone offsets and the
// same server-evaluated next runs.

/** The candidate schedule's next runs, as the server's own evaluator sees them. */
export interface SchedulePreviewState {
  occurrences: string[]
  error: string
  conflictSummary?: string
}

export function ScheduleTimezoneSelect({ value, onChange, now, labelledBy, ariaLabel, className, disabled }: {
  value: string
  onChange: (timezone: string) => void
  now: Date
  labelledBy?: string
  ariaLabel?: string
  className?: string
  disabled?: boolean
}) {
  const zones = value && !COMMON_TIMEZONES.includes(value) ? [value, ...COMMON_TIMEZONES] : COMMON_TIMEZONES
  return (
    <select className={className} aria-labelledby={labelledBy} aria-label={labelledBy ? undefined : ariaLabel} value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>
      {zones.map(zone => <option key={zone} value={zone}>{formatTimeZoneOptionLabel(zone, now)}</option>)}
    </select>
  )
}

export function ScheduleCurrentUtc({ now }: { now: Date }) {
  return <span className="setting-desc schedule-current-utc">Current UTC: {formatUtcScheduleClock(now)} UTC</span>
}

export function ScheduleNextRuns({ preview, pending, timezone, errorId }: {
  preview: SchedulePreviewState | null
  pending: boolean
  timezone: string
  errorId?: string
}) {
  return (
    <>
      {pending && <p role="status">Refreshing next runs…</p>}
      {preview && (
        preview.error ? (
          <span id={errorId} className="setting-desc cron-preview-error" role="alert">{preview.error}</span>
        ) : preview.occurrences.length > 0 ? (
          <div className="cron-next-runs">
            <span className="setting-desc">Next runs ({timezone || 'UTC'}):</span>
            <ul>
              {preview.occurrences.map((iso, index) => (
                <li key={index}>{formatScheduleTimestampInZone(iso, timezone)}</li>
              ))}
            </ul>
          </div>
        ) : (
          <span className="setting-desc">No upcoming runs match this schedule within the next year.</span>
        )
      )}
      {preview && !preview.error && preview.conflictSummary && (
        <span className="setting-desc cron-preview-error" role="alert">
          ⚠ This schedule will be blocked until resolved: {preview.conflictSummary}
        </span>
      )}
    </>
  )
}
