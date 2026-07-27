import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
} from 'react'

import {
  MAX_THINK_TIME_MULTIPLIER,
  MIN_THINK_TIME_MULTIPLIER,
  THINK_TIME_MULTIPLIER_STEP,
  normalizeThinkTimeMultiplier,
} from './thinkTimeConfig'

type ThinkTimeMultiplierInputProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'defaultValue' | 'max' | 'min' | 'onChange' | 'step' | 'type' | 'value'
> & {
  /**
   * Stable identity for the resource currently being edited. A parent can
   * reuse this input while switching projects, scripts, or groups without
   * changing the controlled multiplier value; that scope change must still
   * discard an in-progress local draft.
   */
  draftScope?: string | number
  label?: ReactNode
  wrapperClassName?: string
  value?: number
  fallbackValue?: number | ''
  onValueChange: (value: number | undefined) => void
}

function displayValue(value: number | undefined, fallbackValue: number | ''): string {
  const normalized = normalizeThinkTimeMultiplier(value)
  if (normalized !== undefined) return String(normalized)
  return fallbackValue === '' ? '' : String(fallbackValue)
}

/**
 * Number inputs need a string draft while the operator is typing. Committing
 * every keystroke makes a valid value such as 0.05 impossible to enter because
 * its transient "0" prefix is not a valid persisted multiplier.
 */
export default function ThinkTimeMultiplierInput({
  draftScope,
  label,
  wrapperClassName,
  value,
  fallbackValue = 1,
  onValueChange,
  onBlur,
  onFocus,
  ...inputProps
}: ThinkTimeMultiplierInputProps) {
  const generatedID = useId()
  const inputID = inputProps.id ?? generatedID
  const externalValue = displayValue(value, fallbackValue)
  const [draft, setDraft] = useState(externalValue)
  const [editing, setEditing] = useState(false)
  const dirty = useRef(false)
  const committedDraft = useRef<{ hasValue: boolean; value?: number }>({ hasValue: false })
  const previousExternalValue = useRef(externalValue)
  const previousDraftScope = useRef(draftScope)

  useEffect(() => {
    if (!editing) setDraft(externalValue)
  }, [editing, externalValue])

  useLayoutEffect(() => {
    const changed = previousExternalValue.current !== externalValue
    const scopeChanged = previousDraftScope.current !== draftScope
    previousExternalValue.current = externalValue
    previousDraftScope.current = draftScope
    if (!editing || (!changed && !scopeChanged)) return

    const normalizedExternal = normalizeThinkTimeMultiplier(externalValue)
    const isControlledEcho = committedDraft.current.hasValue
      && committedDraft.current.value === normalizedExternal
    if (!scopeChanged && isControlledEcho) return

    // A different script, group, project, or server refresh can replace this
    // controlled value while the field still owns focus. Drop the old local
    // edit immediately so its later blur cannot write into the new scope.
    setDraft(externalValue)
    dirty.current = false
    committedDraft.current = { hasValue: false }
  }, [draftScope, editing, externalValue])

  const input = (
    <input
      {...inputProps}
      id={inputID}
      type="number"
      min={MIN_THINK_TIME_MULTIPLIER}
      max={MAX_THINK_TIME_MULTIPLIER}
      step={THINK_TIME_MULTIPLIER_STEP}
      value={draft}
      onFocus={(event) => {
        setEditing(true)
        dirty.current = false
        committedDraft.current = { hasValue: false }
        onFocus?.(event)
      }}
      onChange={(event) => {
        const nextDraft = event.target.value
        setDraft(nextDraft)
        dirty.current = true
        const normalized = normalizeThinkTimeMultiplier(nextDraft)
        if (normalized !== undefined) {
          committedDraft.current = { hasValue: true, value: normalized }
          onValueChange(normalized)
        }
      }}
      onBlur={(event) => {
        if (dirty.current) {
          const normalized = normalizeThinkTimeMultiplier(draft)
          if (!committedDraft.current.hasValue || committedDraft.current.value !== normalized) {
            onValueChange(normalized)
          }
          setDraft(displayValue(normalized, fallbackValue))
        }
        setEditing(false)
        dirty.current = false
        committedDraft.current = { hasValue: false }
        onBlur?.(event)
      }}
    />
  )
  if (label === undefined) return input
  return (
    <label className={wrapperClassName} htmlFor={inputID}>
      <span>{label}</span>
      {input}
    </label>
  )
}
