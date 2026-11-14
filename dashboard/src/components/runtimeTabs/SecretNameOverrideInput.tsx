import { secretNameFieldIssue } from '../runtime-settings/protocolCredentialLeaves'
import { REDACTED_SECRET } from '../runtime-settings/scenarioOverrideValues'
import { useValueBeforeTyping } from '../runtime-settings/useValueBeforeTyping'

/**
 * A secret-name field. A stored name the server masks comes back as
 * REDACTED_SECRET and shows empty; erasing text typed over it sends the mask
 * back (the server keeps the stored value), and erasing text typed over an
 * inherited field goes back to inheriting.
 */
export default function SecretNameOverrideInput({ label, value, inheritPlaceholder, onChange }: {
  label: string
  /** This layer's value: undefined inherits, REDACTED_SECRET is a stored masked value, "" is an explicit clear. */
  value: string | undefined
  inheritPlaceholder: string
  onChange: (value: string | undefined) => void
}) {
  const emitFor = useValueBeforeTyping(value)
  const shown = value ?? ''
  const issue = shown && shown !== REDACTED_SECRET ? secretNameFieldIssue(shown) : undefined
  return (
    <label>
      <span>{label}</span>
      <input
        className="settings-input"
        autoComplete="off"
        spellCheck={false}
        aria-label={label}
        aria-invalid={issue ? true : undefined}
        value={shown === REDACTED_SECRET ? '' : shown}
        placeholder={shown === REDACTED_SECRET ? 'Stored value (unchanged)' : inheritPlaceholder}
        onChange={event => onChange(emitFor(event.target.value))}
      />
      {issue && <span className="form-error" role="alert">{issue}</span>}
    </label>
  )
}
