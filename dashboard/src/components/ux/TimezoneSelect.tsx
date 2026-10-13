import { useId, useMemo, useState } from 'react'

import './ux.css'

import { viewerTimeZone } from '../newTestScheduleFormat'
import { resolveTimezone, timezoneChoiceLabel, timezoneChoices } from './timezoneChoices'

/**
 * The one IANA time zone picker: type any part of a zone's name to find it
 * among every zone, each listed with its current offset and time, the
 * viewer's zone, UTC and the common zones first. Focusing the field empties
 * it (the current zone stays as its placeholder) so the whole list can be
 * browsed. Only a real zone is ever chosen; other text is dropped when the
 * field is left.
 */
export default function TimezoneSelect({ value, onChange, ariaLabel, labelledBy, className, disabled, now, note }: {
  value: string
  onChange: (value: string) => void
  ariaLabel?: string
  labelledBy?: string
  className?: string
  disabled?: boolean
  /** The time offsets are shown for; the current minute when not given. */
  now?: Date
  /** What the zone is used for, shown under the field with its offset. */
  note?: string
}) {
  const listId = useId()
  const offsetId = useId()
  // The text typed while the field is focused; null when it is not.
  const [draft, setDraft] = useState<string | null>(null)
  const viewer = viewerTimeZone()
  const at = new Date(Math.floor((now ?? new Date()).getTime() / 60_000) * 60_000)
  const choices = useMemo(() => timezoneChoices(value, viewer), [value, viewer])
  return (
    <span className="timezone-select">
      <input
        className={className}
        list={listId}
        aria-label={labelledBy ? undefined : ariaLabel}
        aria-labelledby={labelledBy}
        aria-describedby={offsetId}
        value={draft ?? value}
        placeholder={draft === null ? 'Search time zones' : value || 'Search time zones'}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        onFocus={() => setDraft('')}
        onChange={event => {
          setDraft(event.target.value)
          const zone = resolveTimezone(event.target.value, choices)
          if (zone && zone !== value) onChange(zone)
        }}
        onBlur={() => setDraft(null)}
      />
      {/* Only the field being edited lists the zones. */}
      {draft !== null && <datalist id={listId}>{choices.map(zone => <option key={zone} value={zone}>{timezoneChoiceLabel(zone, at, viewer)}</option>)}</datalist>}
      <small id={offsetId} className="timezone-select-offset">{[value ? timezoneChoiceLabel(value, at, viewer) : '', note].filter(Boolean).join('. ')}</small>
    </span>
  )
}
