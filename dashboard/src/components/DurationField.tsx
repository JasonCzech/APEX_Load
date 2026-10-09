import { useEffect, useId, useMemo, useReducer, useRef } from 'react'

import { DurationUnit, isValidDurationInput, isValidNonNegativeDurationInput, serializeDurationFieldValue, splitDurationInput } from '../utils/timeUtils'
import './DurationField.css'

const DEFAULT_PRESETS = ['30s', '1m', '5m', '10m', '30m', '1h']

interface DurationFieldProps {
  label?: string
  value: string
  onChange: (value: string) => void
  allowEmpty?: boolean
  disabled?: boolean
  compact?: boolean
  required?: boolean
  presets?: string[]
  placeholder?: string
  className?: string
  inputClassName?: string
  id?: string
  unitLabel?: string
  showPresets?: boolean
  showValidation?: boolean
  /** Zero is a meaningful value (for example an instant ramp), serialized as 0 in the unit shown. */
  allowZero?: boolean
  /** Reports when the field shows an error, so a parent can block saving. */
  onInvalidChange?: (invalid: boolean) => void
}

const UNIT_LABELS: Record<DurationUnit, string> = {
  s: 'Seconds',
  m: 'Minutes',
  h: 'Hours',
}

export default function DurationField({
  label,
  value,
  onChange,
  allowEmpty = false,
  disabled = false,
  compact = false,
  required = false,
  presets = DEFAULT_PRESETS,
  placeholder = '0',
  className = '',
  inputClassName = '',
  id,
  unitLabel = 'Unit',
  showPresets = false,
  showValidation = true,
  allowZero = false,
  onInvalidChange,
}: DurationFieldProps) {
  const generatedId = useId()
  const inputId = id || generatedId
  const parts = useMemo(() => {
    const zero = allowZero ? /^0+(?:\.0*)?(s|m|h)$/.exec(value.trim()) : null
    return zero ? { value: '0', unit: zero[1] as DurationUnit } : splitDurationInput(value)
  }, [allowZero, value])
  const isEmpty = value.trim() === ''
  const retainedUnitRef = useRef<DurationUnit>(parts.unit)
  // While the value is the one this field just emitted, show exactly what the
  // user typed in the unit they chose: re-splitting it (0.5 m → 30 s) would
  // switch the unit under the cursor, so the next keystroke lands in the
  // wrong unit. A value set from outside (preset, loaded draft) is re-derived.
  const lastEmittedRef = useRef<string | null>(null)
  // Choosing a unit on an empty field emits the same empty value, so the
  // parent does not re-render; render here or the select snaps back.
  const [, renderUnitChoice] = useReducer((count: number) => count + 1, 0)
  const typedRef = useRef('')
  const ownValue = lastEmittedRef.current !== null && value === lastEmittedRef.current
  if (!isEmpty && !ownValue) retainedUnitRef.current = parts.unit
  const displayedUnit = isEmpty || ownValue ? retainedUnitRef.current : parts.unit
  const displayedValue = ownValue ? typedRef.current : parts.value
  // Typed text that is not a duration empties the value; it is still an error, not "empty".
  const typedInvalid = ownValue && isEmpty && typedRef.current.trim() !== ''
  const isValid = !typedInvalid && (allowZero ? isValidNonNegativeDurationInput(value, allowEmpty) : isValidDurationInput(value, allowEmpty))
  const error = !showValidation || isValid
    ? ''
    : isEmpty && !typedInvalid
      ? 'Duration is required.'
      : allowZero ? 'Enter zero or a positive duration.' : 'Enter a positive duration.'
  const onInvalidChangeRef = useRef(onInvalidChange)
  onInvalidChangeRef.current = onInvalidChange
  useEffect(() => { onInvalidChangeRef.current?.(Boolean(error)) }, [error])

  const emitValue = (nextValue: string, nextUnit: DurationUnit) => {
    typedRef.current = nextValue
    retainedUnitRef.current = nextUnit
    // Exponent notation (1e2) is not a duration anyone means to type.
    const zero = allowZero && /^0+(?:\.0*)?$/.test(nextValue.trim())
    const serialized = /e/i.test(nextValue) ? '' : zero ? `0${nextUnit}` : serializeDurationFieldValue(nextValue, nextUnit)
    lastEmittedRef.current = serialized
    onChange(serialized)
  }

  const rootClassName = [
    'duration-field',
    compact ? 'compact' : '',
    disabled ? 'disabled' : '',
    error ? 'invalid' : '',
    className,
  ].filter(Boolean).join(' ')

  return (
    <div className={rootClassName}>
      {label && <label className="duration-field-label" htmlFor={inputId}>{label}</label>}
      <div className="duration-field-control">
        <input
          id={inputId}
          className={`duration-field-number ${inputClassName}`.trim()}
          type="number"
          min={0}
          step="any"
          value={displayedValue}
          placeholder={placeholder}
          disabled={disabled}
          required={required}
          aria-label={label || 'Duration value'}
          aria-invalid={Boolean(error)}
          onChange={(event) => {
            emitValue(event.target.value, displayedUnit)
          }}
        />
        <select
          className="duration-field-unit"
          value={displayedUnit}
          disabled={disabled}
          aria-label={label ? `${label} ${unitLabel}` : unitLabel}
          onChange={(event) => {
            const nextUnit = event.target.value as DurationUnit
            emitValue(displayedValue, nextUnit)
            renderUnitChoice()
          }}
        >
          {Object.entries(UNIT_LABELS).map(([unit, text]) => (
            <option key={unit} value={unit}>{compact ? unit : text}</option>
          ))}
        </select>
      </div>
      {showPresets && presets.length > 0 && (
        <div className="duration-field-presets" aria-label={label ? `${label} presets` : 'Duration presets'}>
          {presets.map((preset) => (
            <button
              key={preset}
              type="button"
              className={`duration-field-preset ${value === preset ? 'active' : ''}`}
              disabled={disabled}
              onClick={() => onChange(preset)}
            >
              {preset}
            </button>
          ))}
        </div>
      )}
      {error && <span className="duration-field-error">{error}</span>}
    </div>
  )
}
