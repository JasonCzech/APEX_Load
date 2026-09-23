import type { ThinkTimeConfig } from '../types/scripts'

export default function ThinkTimePercentageInputs({ value, onChange, disabled = false }: {
  value?: Partial<ThinkTimeConfig>
  onChange: (next: Partial<ThinkTimeConfig>) => void
  disabled?: boolean
}) {
  return <>{(['min_percent', 'max_percent'] as const).map((key, index) => <label key={key}>
    <span>{index === 0 ? 'Minimum recorded think time (%)' : 'Maximum recorded think time (%)'}</span>
    <input className="settings-input" type="number" min={0} max={10000} step="any" disabled={disabled}
      value={value?.[key] ?? 0} onChange={event => onChange({ [key]: Number(event.target.value) })} />
  </label>)}</>
}
