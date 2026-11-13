import { useRef } from 'react'
import { REDACTED_SECRET } from './scenarioOverrideValues'

/**
 * Typing over a stored secret (REDACTED_SECRET) or a cleared value and then
 * erasing the text returns the field to what it held before the typing began,
 * so the server keeps the stored secret; a field that inherited goes back to
 * inheriting. Returns the value to emit for the input's new text.
 */
export function useValueBeforeTyping(value: string | undefined): (next: string) => string | undefined {
  const typedRef = useRef<string | undefined>(undefined)
  const beforeRef = useRef<string | undefined>(value)
  return (next: string) => {
    // A value this field did not type (first keystroke, or a draft swapped in
    // from outside) is the new "before": only a mask or a clear is restorable.
    const typedHere = typedRef.current !== undefined && value === typedRef.current
    if (!typedHere) beforeRef.current = value === REDACTED_SECRET || value === '' ? value : undefined
    typedRef.current = next === '' ? undefined : next
    return next === '' ? beforeRef.current : next
  }
}
