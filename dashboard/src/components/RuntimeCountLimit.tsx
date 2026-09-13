import { useState } from 'react'

export function RuntimeCountLimit({ label, value, onChange, help }: { label: string; value?: number; onChange: (value: number) => void; help: string }) {
  const [lastCap, setLastCap] = useState(value && value > 0 ? value : 1)
  return <div className="runtime-count-limit">
    <label><span>{label}</span><input aria-label={label} type="number" min="0" step="1" value={value ?? 0} onChange={event => { const next = Number(event.target.value); if (next > 0) setLastCap(next); onChange(next) }} /></label>
    <label className="checkbox-label"><input type="checkbox" aria-label={`No cap for ${label}`} checked={!value} onChange={event => { if (value && value > 0) setLastCap(value); onChange(event.target.checked ? 0 : lastCap) }} />No cap</label>
    <small>{help}</small>
  </div>
}
