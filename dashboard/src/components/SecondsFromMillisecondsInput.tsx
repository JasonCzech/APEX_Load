import {
  useEffect,
  useState,
  type FocusEventHandler,
  type InputHTMLAttributes,
} from 'react'

import {
  SECONDS_INPUT_STEP,
  formatMillisecondsAsSecondsValue,
  parseSecondsInputAsMilliseconds,
} from './runtimeTiming'

export interface SecondsFromMillisecondsInputProps
  extends Omit<
    InputHTMLAttributes<HTMLInputElement>,
    'defaultValue' | 'min' | 'onChange' | 'step' | 'type' | 'value'
  > {
  valueMs?: number | null
  fallbackMs?: number
  emptyWhenUndefined?: boolean
  min?: number
  onChangeMs: (valueMs: number | undefined) => void
}

/**
 * Edits millisecond-backed values as seconds without replacing an in-progress
 * decimal draft with the formatted persisted value after every keystroke.
 */
export default function SecondsFromMillisecondsInput({
  valueMs,
  fallbackMs = 0,
  emptyWhenUndefined = false,
  min = 0,
  onChangeMs,
  onFocus,
  onBlur,
  ...inputProps
}: SecondsFromMillisecondsInputProps) {
  const formattedValue = formatMillisecondsAsSecondsValue(valueMs, fallbackMs, emptyWhenUndefined)
  const [draftValue, setDraftValue] = useState(formattedValue)
  const [focused, setFocused] = useState(false)

  useEffect(() => {
    if (!focused) setDraftValue(formattedValue)
  }, [focused, formattedValue])

  const handleFocus: FocusEventHandler<HTMLInputElement> = event => {
    setFocused(true)
    setDraftValue(formattedValue)
    onFocus?.(event)
  }

  const handleBlur: FocusEventHandler<HTMLInputElement> = event => {
    setFocused(false)
    onBlur?.(event)
  }

  return (
    <input
      {...inputProps}
      type="number"
      min={min}
      step={SECONDS_INPUT_STEP}
      value={focused ? draftValue : formattedValue}
      onFocus={handleFocus}
      onBlur={handleBlur}
      onChange={event => {
        const nextValue = event.target.value
        setDraftValue(nextValue)
        const nextMilliseconds = parseSecondsInputAsMilliseconds(nextValue, min)
        if (nextMilliseconds !== null) onChangeMs(nextMilliseconds)
      }}
    />
  )
}
