import { useId } from 'react'
import { HTTP_VERSION_IMPORT_MODES, isHTTPVersionImportMode, type HTTPVersionImportMode } from './httpVersionModel'

type Props = {
  value: HTTPVersionImportMode
  onChange: (value: HTTPVersionImportMode) => void
  disabled?: boolean
  className?: string
}

/** The http_version_mode import option: how captured HTTP versions become request policies. */
export function HTTPVersionModeField({ value, onChange, disabled, className = 'sample-import-field' }: Props) {
  const labelId = useId()
  const helpId = useId()
  const selected = HTTP_VERSION_IMPORT_MODES.find(mode => mode.value === value) ?? HTTP_VERSION_IMPORT_MODES[0]
  return <label className={className}>
    <span id={labelId}>HTTP version in generated script</span>
    <select aria-labelledby={labelId} aria-describedby={helpId} value={selected.value} disabled={disabled}
      onChange={event => onChange(isHTTPVersionImportMode(event.target.value) ? event.target.value : 'upgrade')}>
      {HTTP_VERSION_IMPORT_MODES.map(mode => <option key={mode.value} value={mode.value}>{mode.label}</option>)}
    </select>
    <small id={helpId}>{selected.help}</small>
  </label>
}
