import { useEffect, useRef } from 'react'

interface Props {
  selectedCount: number
  visibleCount: number
  ariaLabel: string
  onChange: (checked: boolean) => void
  disabled?: boolean
}

export default function SelectAllCheckbox({ selectedCount, visibleCount, ariaLabel, onChange, disabled = false }: Props) {
  const checkboxRef = useRef<HTMLInputElement | null>(null)
  const allSelected = visibleCount > 0 && selectedCount === visibleCount
  const partiallySelected = selectedCount > 0 && selectedCount < visibleCount

  useEffect(() => {
    if (checkboxRef.current) checkboxRef.current.indeterminate = partiallySelected
  }, [partiallySelected])

  return (
    <input
      ref={checkboxRef}
      type="checkbox"
      aria-label={ariaLabel}
      checked={allSelected}
      disabled={disabled || visibleCount === 0}
      onChange={event => onChange(event.target.checked)}
    />
  )
}
