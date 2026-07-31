export type PageViewToggleOption<T extends string> = {
  value: T
  label: string
  ariaLabel?: string
  disabled?: boolean
  title?: string
  // Small numeric chip after the label (e.g. validation-issue count).
  badge?: number
}

interface PageViewToggleProps<T extends string> {
  value: T
  options: Array<PageViewToggleOption<T>>
  ariaLabel: string
  className?: string
  onChange: (value: T) => void
}

export default function PageViewToggle<T extends string>({
  value,
  options,
  ariaLabel,
  className = '',
  onChange,
}: PageViewToggleProps<T>) {
  const classes = ['page-view-toggle', className].filter(Boolean).join(' ')

  return (
    <div className={classes} role="group" aria-label={ariaLabel}>
      {options.map(option => {
        const active = option.value === value
        // aria-label replaces the button text for assistive tech, so a visible
        // badge must be folded into it to stay announced.
        const ariaLabel = option.badge
          ? `${option.ariaLabel || option.label} (${option.badge} issue${option.badge === 1 ? '' : 's'})`
          : option.ariaLabel
        return (
          <button
            key={option.value}
            type="button"
            className={`mode-chip ${active ? 'active' : ''}`}
            aria-label={ariaLabel}
            aria-pressed={active}
            disabled={option.disabled}
            title={option.title}
            onClick={() => {
              if (!active) onChange(option.value)
            }}
          >
            {option.label}
            {option.badge ? <span className="repository-status-pill danger mode-chip-badge">{option.badge}</span> : null}
          </button>
        )
      })}
    </div>
  )
}
