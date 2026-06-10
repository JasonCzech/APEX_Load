export type PageViewToggleOption<T extends string> = {
  value: T
  label: string
  ariaLabel?: string
  disabled?: boolean
  title?: string
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
        return (
          <button
            key={option.value}
            type="button"
            className={`mode-chip ${active ? 'active' : ''}`}
            aria-label={option.ariaLabel}
            aria-pressed={active}
            disabled={option.disabled}
            title={option.title}
            onClick={() => {
              if (!active) onChange(option.value)
            }}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}
