import type { ScheduleClockChange } from '../types/executionPlans'
import {
  formatScheduleClockChange,
  formatScheduleInstantForViewer,
  formatUtcScheduleClock,
} from './newTestScheduleFormat'

// The cadence pieces every schedule editor shares — scenario schedules in New
// run and execution-plan schedules — so both show the same server-evaluated
// next runs and the same daylight-saving notes (both pick zones with
// ux/TimezoneSelect).

/** The candidate schedule's next runs, as the server's own evaluator sees them. */
export interface SchedulePreviewState {
  occurrences: string[]
  error: string
  conflictSummary?: string
  /** Daylight-saving changes the schedule's times fall into. */
  clockChanges?: ScheduleClockChange[]
}

export function ScheduleCurrentUtc({ now }: { now: Date }) {
  return <span className="setting-desc schedule-current-utc">Current UTC: {formatUtcScheduleClock(now)} UTC</span>
}

/** The skipped and repeated times of a schedule's daylight-saving changes, one sentence each. */
export function ScheduleClockChanges({ changes, timezone }: { changes?: ScheduleClockChange[]; timezone: string }) {
  if (!changes?.length) return null
  return (
    <div className="schedule-clock-changes">
      <span className="setting-desc">Daylight saving in {timezone || 'UTC'}:</span>
      <ul>{changes.map(change => <li key={`${change.kind}:${change.at}`}>{formatScheduleClockChange(change, timezone)}</li>)}</ul>
    </div>
  )
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
                <li key={index}>{formatScheduleInstantForViewer(iso, timezone)}</li>
              ))}
            </ul>
          </div>
        ) : (
          <span className="setting-desc">No upcoming runs match this schedule within the next year.</span>
        )
      )}
      {preview && !preview.error && <ScheduleClockChanges changes={preview.clockChanges} timezone={timezone} />}
      {preview && !preview.error && preview.conflictSummary && (
        <span className="setting-desc cron-preview-error" role="alert">
          ⚠ This schedule will be blocked until resolved: {preview.conflictSummary}
        </span>
      )}
    </>
  )
}
