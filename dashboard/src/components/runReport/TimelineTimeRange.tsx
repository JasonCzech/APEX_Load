import { useId, useMemo, useState } from 'react'
import type { MetricsHistoryPoint } from '../../types'
import { parseTimelineTime, timelineTimeBounds, timelineTimeWindow } from './timelineSampling'

interface RangeDraft { selection: string; start: string; end: string; error: string }

export default function TimelineTimeRange({ data, startedAt, startIndex, endIndex, onChange }: {
  data: MetricsHistoryPoint[]
  startedAt?: string
  startIndex: number
  endIndex: number
  onChange: (start: number, end: number) => void
}) {
  const helpId = useId()
  const [mode, setMode] = useState<'relative' | 'clock'>('relative')
  const [draft, setDraft] = useState<RangeDraft | null>(null)
  const bounds = useMemo(() => timelineTimeBounds(data), [data])
  // A known run start alone cannot supply the timestamps missing from legacy
  // history. Keep that history usable through its existing index sliders.
  if (!bounds) return null
  const startedTimestamp = startedAt ? Date.parse(startedAt) : NaN
  const hasRunStart = Number.isFinite(startedTimestamp)
  const origin = hasRunStart ? startedTimestamp : bounds[0]
  const relativeOriginLabel = hasRunStart ? 'run start' : 'first sample'
  const startTime = data[startIndex].timestampMs!
  const endTime = data[endIndex].timestampMs!
  const selection = `${mode}:${origin}:${startTime}:${endTime}`
  const format = (timestamp: number) => mode === 'clock' ? new Date(timestamp).toISOString() : `${(timestamp - origin) / 1000}s`
  // Reset drafts when the slider, reset button, or time format changes. Derive
  // this synchronously so stale form values never describe a different window.
  const current = draft?.selection === selection ? draft : { selection, start: format(startTime), end: format(endTime), error: '' }
  const edit = (changes: Partial<RangeDraft>) => setDraft({ ...current, error: '', ...changes })

  return (
    <form className="run-report-timeline-time-form" aria-label="Manual timeline window" onSubmit={event => {
      event.preventDefault()
      const from = parseTimelineTime(current.start, mode, origin)
      const to = parseTimelineTime(current.end, mode, origin)
      const window = from === null || to === null ? null : timelineTimeWindow(data, from, to, origin)
      if (!window) {
        edit({ error: 'Enter a valid start and end within the recorded range, with start at or before end.' })
        return
      }
      // Normalize to the actual snapped bounds, even if applying the current
      // window leaves the parent selection unchanged.
      setDraft(null)
      onChange(...window)
    }}>
      <label>
        Time format
        <select aria-label="Time format" value={mode} onChange={event => { setMode(event.target.value as typeof mode); setDraft(null) }}>
          <option value="relative">Relative to {relativeOriginLabel}</option>
          <option value="clock">Clock time (UTC)</option>
        </select>
      </label>
      <label>
        Start time
        <input type="text" value={current.start} required aria-describedby={helpId} aria-invalid={Boolean(current.error)} onChange={event => edit({ start: event.target.value })} placeholder={mode === 'relative' ? '1m or 01:00' : '14:30:00'} />
      </label>
      <label>
        End time
        <input type="text" value={current.end} required aria-describedby={helpId} aria-invalid={Boolean(current.error)} onChange={event => edit({ end: event.target.value })} placeholder={mode === 'relative' ? '5m or 05:00' : '14:35:00'} />
      </label>
      <button type="submit" className="btn btn-sm btn-secondary">Apply time range</button>
      <p id={helpId} className="run-report-muted">
        {mode === 'relative' ? `Seconds, ms/s/m/h, MM:SS or HH:MM:SS from ${relativeOriginLabel}.` : `UTC clock on ${new Date(origin).toISOString().slice(0, 10)}, or an ISO timestamp with timezone for another date.`}{' '}
        Bounds snap outward to recorded samples. Recorded range: {new Date(bounds[0]).toISOString()} – {new Date(bounds[1]).toISOString()}.
      </p>
      {current.error && <p role="alert" className="run-report-error">{current.error}</p>}
    </form>
  )
}
