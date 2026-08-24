import './CodeLineWrapToggle.css'

interface Props {
  disabled?: boolean
  onChange: (enabled: boolean) => void
  wrapLines: boolean
}

export default function CodeLineWrapToggle({ disabled = false, onChange, wrapLines }: Props) {
  return (
    <button
      type="button"
      className="btn btn-sm btn-ghost code-line-wrap-toggle"
      aria-pressed={wrapLines}
      disabled={disabled}
      title={disabled ? 'Word wrap is disabled for this large document.' : 'Toggle word wrapping in source-code views'}
      onClick={() => onChange(!wrapLines)}
    >
      Word Wrap: {wrapLines ? 'On' : 'Off'}
    </button>
  )
}
