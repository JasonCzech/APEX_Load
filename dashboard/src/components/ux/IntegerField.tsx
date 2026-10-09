import { useEffect, useId, useState } from 'react'

/**
 * A whole-number input that never rewrites what the user typed. Out-of-range,
 * blank or fractional entries are reported next to the field (and not
 * committed) instead of being silently clamped to the nearest valid value.
 */
export default function IntegerField({ value, min, max, label, unit, onCommit, className = 'settings-input', advisory }: {
  value: number
  min: number
  max?: number
  label: string
  unit?: string
  onCommit: (value: number) => void
  className?: string
  /** A soft warning shown for a valid value (for example, above known capacity). */
  advisory?: (value: number) => string | undefined
}) {
  const errorId = useId()
  const [text, setText] = useState(String(value))
  // Follow outside changes (templates, undo) unless the field holds an invalid draft for them.
  useEffect(() => { setText(current => (Number(current) === value && current.trim() !== '' ? current : String(value))) }, [value])
  const parsed = Number(text)
  const error = text.trim() === ''
    ? `Enter ${label.toLowerCase()}.`
    : !Number.isInteger(parsed)
      ? 'Enter a whole number.'
      : parsed < min
        ? `Must be at least ${min.toLocaleString()}.`
        : max !== undefined && parsed > max
          ? `Must be at most ${max.toLocaleString()}.`
          : ''
  const note = !error ? advisory?.(parsed) : undefined
  return <>
    <input
      className={className}
      type="number"
      inputMode="numeric"
      aria-label={label}
      min={min}
      max={max}
      step={1}
      value={text}
      aria-invalid={Boolean(error) || undefined}
      aria-describedby={error || note ? errorId : undefined}
      onChange={event => {
        const next = event.target.value
        setText(next)
        const number = Number(next)
        if (next.trim() !== '' && Number.isInteger(number) && number >= min && (max === undefined || number <= max)) onCommit(number)
      }}
    />
    {unit && <span>{unit}</span>}
    {(error || note) && <small id={errorId} className={error ? 'field-error' : 'field-advisory'} role={error ? 'alert' : 'status'}>{error || note}</small>}
  </>
}
