import { useId, useMemo } from 'react'

import { DurationUnit, isValidDurationInput, serializeDurationFieldValue, splitDurationInput } from '../utils/timeUtils'
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
}: DurationFieldProps) {
  const generatedId = useId()
  const inputId = id || generatedId
  const parts = useMemo(() => splitDurationInput(value), [value])
  const isEmpty = value.trim() === ''
  const isValid = isValidDurationInput(value, allowEmpty)
  const error = !showValidation || isValid
    ? ''
    : isEmpty
      ? 'Duration is required.'
      : 'Enter a positive duration.'

  const emitValue = (nextValue: string, nextUnit: DurationUnit) => {
    onChange(serializeDurationFieldValue(nextValue, nextUnit))
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
          value={parts.value}
          placeholder={placeholder}
          disabled={disabled}
          required={required}
          aria-label={label || 'Duration value'}
          aria-invalid={Boolean(error)}
          onChange={(event) => {
            emitValue(event.target.value, parts.unit)
          }}
        />
        <select
          className="duration-field-unit"
          value={parts.unit}
          disabled={disabled}
          aria-label={label ? `${label} ${unitLabel}` : unitLabel}
          onChange={(event) => emitValue(parts.value, event.target.value as DurationUnit)}
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
