interface Props {
  id: string
  name: string
  value: string
  onNameChange?: (name: string) => void
  onValueChange: (value: string) => void
  nameLabel?: string
  valueLabel?: string
  nameReadOnly?: boolean
  nameDisabled?: boolean
  valueDisabled?: boolean
  nameDescribedBy?: string
  nameInvalid?: boolean
  valueAriaLabel?: string
  valuePlaceholder?: string
  referenceKind?: string
}

// Shared by import review and saved-script configuration. Keeping the
// reference preview beside the write-only value field makes it explicit which
// logical vault entry will receive the entered value.
export default function SecretReferenceValueControl({
  id,
  name,
  value,
  onNameChange,
  onValueChange,
  nameLabel = 'Secret reference name',
  valueLabel = 'Secret value (optional)',
  nameReadOnly = false,
  nameDisabled = false,
  valueDisabled = false,
  nameDescribedBy,
  nameInvalid = false,
  valueAriaLabel,
  valuePlaceholder,
  referenceKind = 'Script reference',
}: Props) {
  const valueId = `${id}-value`
  const referenceId = `${id}-reference`
  return (
    <>
      <label htmlFor={id}>{nameLabel}</label>
      <input
        id={id}
        type="text"
        value={name}
        onChange={event => onNameChange?.(event.target.value)}
        readOnly={nameReadOnly}
        disabled={nameDisabled}
        aria-describedby={[referenceId, nameDescribedBy].filter(Boolean).join(' ')}
        aria-invalid={nameInvalid ? 'true' : 'false'}
        autoComplete="off"
        spellCheck={false}
      />
      <span id={referenceId} className="secret-binding-review-reference">
        {referenceKind}: <code>{name.trim() ? `{{secret.${name.trim()}}}` : '{{secret.NAME}}'}</code>
      </span>
      <label htmlFor={valueId}>{valueLabel}</label>
      <input
        id={valueId}
        type="password"
        value={value}
        onChange={event => onValueChange(event.target.value)}
        disabled={valueDisabled}
        autoComplete="new-password"
        aria-label={valueAriaLabel}
        placeholder={valuePlaceholder}
      />
    </>
  )
}
