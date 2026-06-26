import { type CronModel, cronModelToExpr, exprToCronModel, DEFAULT_CRON_MODEL } from './cronBuilder'

const WEEKDAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

const FREQUENCIES: { value: CronModel['frequency']; label: string }[] = [
  { value: 'minutes', label: 'Every N minutes' },
  { value: 'hourly', label: 'Hourly' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
]

// CronBuilder is a visual layer over the raw cron string: the string stays the
// source of truth (passed in as `value`), and every control re-derives the
// structured model from it. When the expression is too complex to represent
// (ranges in odd places, L/W/#, month restrictions, aliases) it shows a
// custom-expression notice and the raw input below remains the way to edit it.
export function CronBuilder({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (expr: string) => void
  disabled?: boolean
}) {
  const model = exprToCronModel(value)
  if (!model) {
    return (
      <div className="cron-builder cron-builder-custom">
        <span className="setting-desc">
          Custom expression — edit the raw cron field below.{' '}
          <button
            type="button"
            className="cron-builder-link"
            disabled={disabled}
            onClick={() => onChange(cronModelToExpr(DEFAULT_CRON_MODEL))}
          >
            Use the builder instead
          </button>
        </span>
      </div>
    )
  }

  const update = (patch: Partial<CronModel>) => onChange(cronModelToExpr({ ...model, ...patch }))
  const toggleDay = (day: number) => {
    const set = new Set(model.daysOfWeek)
    if (set.has(day)) {
      // Keep at least one day selected: an empty weekly set serializes to
      // "* * *" and would silently snap the builder back to a daily schedule.
      if (set.size === 1) return
      set.delete(day)
    } else {
      set.add(day)
    }
    update({ daysOfWeek: [...set].sort((a, b) => a - b) })
  }
  const timeValue = `${String(model.hour).padStart(2, '0')}:${String(model.minute).padStart(2, '0')}`

  return (
    <div className="cron-builder">
      <select
        aria-label="Frequency"
        className="settings-input cron-builder-freq"
        disabled={disabled}
        value={model.frequency}
        onChange={(event) => update({ frequency: event.target.value as CronModel['frequency'] })}
      >
        {FREQUENCIES.map((f) => (
          <option key={f.value} value={f.value}>
            {f.label}
          </option>
        ))}
      </select>

      {model.frequency === 'minutes' && (
        <label className="cron-builder-field">
          every
          <input
            type="number"
            min={1}
            max={59}
            className="settings-input cron-builder-num"
            disabled={disabled}
            value={model.everyN}
            onChange={(event) => update({ everyN: parseInt(event.target.value, 10) || 1 })}
          />
          min
        </label>
      )}

      {model.frequency === 'hourly' && (
        <>
          <label className="cron-builder-field">
            every
            <input
              type="number"
              min={1}
              max={23}
              className="settings-input cron-builder-num"
              disabled={disabled}
              value={model.everyN}
              onChange={(event) => update({ everyN: parseInt(event.target.value, 10) || 1 })}
            />
            hr
          </label>
          <label className="cron-builder-field">
            at minute
            <input
              type="number"
              min={0}
              max={59}
              className="settings-input cron-builder-num"
              disabled={disabled}
              value={model.minute}
              onChange={(event) => update({ minute: parseInt(event.target.value, 10) || 0 })}
            />
          </label>
        </>
      )}

      {(model.frequency === 'daily' || model.frequency === 'weekly' || model.frequency === 'monthly') && (
        <label className="cron-builder-field">
          at
          <input
            type="time"
            className="settings-input cron-builder-time"
            disabled={disabled}
            value={timeValue}
            onChange={(event) => {
              const [h, m] = event.target.value.split(':').map((n) => parseInt(n, 10))
              update({ hour: Number.isNaN(h) ? 0 : h, minute: Number.isNaN(m) ? 0 : m })
            }}
          />
        </label>
      )}

      {model.frequency === 'monthly' && (
        <label className="cron-builder-field">
          on day
          <input
            type="number"
            min={1}
            max={31}
            className="settings-input cron-builder-num"
            disabled={disabled}
            value={model.dayOfMonth}
            onChange={(event) => update({ dayOfMonth: parseInt(event.target.value, 10) || 1 })}
          />
        </label>
      )}

      {model.frequency === 'weekly' && (
        <div className="cron-builder-days" role="group" aria-label="Days of week">
          {WEEKDAY_LABELS.map((label, day) => (
            <button
              type="button"
              key={day}
              disabled={disabled}
              className={`cron-day-chip${model.daysOfWeek.includes(day) ? ' active' : ''}`}
              aria-pressed={model.daysOfWeek.includes(day)}
              onClick={() => toggleDay(day)}
            >
              {label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
