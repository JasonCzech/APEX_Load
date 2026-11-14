import { useState, type ReactNode } from 'react'

import {
  parseLines,
  parseOptionalInteger,
  secretReferenceNames,
  selectOptionsWithStored,
  type ProtocolRuntimeSelectPath,
} from './protocolRuntimeOptions'

/**
 * Small field controls shared by the native protocol sections of the Author
 * Runtime Settings view. Every control carries data-catalog-path (the
 * pkg/dsl/config_catalog.go path it edits) so parity tests can check what the
 * view renders against the catalog.
 */

interface FieldShellProps {
  label: string
  path: string
  fullWidth?: boolean
  hint?: ReactNode
  children: ReactNode
}

function FieldShell({ label, path, fullWidth, hint, children }: FieldShellProps) {
  return (
    <label className={fullWidth ? 'full-width' : undefined} data-catalog-path={path}>
      <span>{label}</span>
      {children}
      {hint}
    </label>
  )
}

export function TextField({ label, path, value, onChange, placeholder, fullWidth }: {
  label: string
  path: string
  value: string | undefined
  onChange: (value: string) => void
  placeholder?: string
  fullWidth?: boolean
}) {
  return (
    <FieldShell label={label} path={path} fullWidth={fullWidth}>
      <input value={value || ''} placeholder={placeholder} onChange={event => onChange(event.target.value)} />
    </FieldShell>
  )
}

/**
 * A credential the script itself holds. The value stays readable and editable
 * (an imported script's credential is not a secret), and a {{secret.NAME}}
 * reference is labelled as a reference to the named project secret: the
 * secret's value never reaches this view.
 */
export function CredentialField({ label, path, value, onChange, placeholder, fullWidth }: {
  label: string
  path: string
  value: string | undefined
  onChange: (value: string) => void
  placeholder?: string
  fullWidth?: boolean
}) {
  const references = secretReferenceNames(value)
  return (
    <FieldShell
      label={label}
      path={path}
      fullWidth={fullWidth}
      hint={references.length > 0 && (
        <small className="field-hint" data-secret-reference-note>
          References project secret {references.join(', ')}. The secret value is resolved at run time and never shown here.
        </small>
      )}
    >
      <input
        type="text"
        aria-label={label}
        autoComplete="off"
        spellCheck={false}
        value={value || ''}
        placeholder={placeholder}
        onChange={event => onChange(event.target.value)}
      />
    </FieldShell>
  )
}

/** A field whose value is the logical name of a project secret (dsn_secret, sasl_password_secret, ...). */
export function SecretNameField({ label, path, value, onChange, placeholder }: {
  label: string
  path: string
  value: string | undefined
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <FieldShell
      label={label}
      path={path}
      hint={value?.trim() ? <small className="field-hint" data-secret-reference-note>Project secret {value.trim()} is resolved at run time.</small> : undefined}
    >
      <input
        aria-label={label}
        autoComplete="off"
        spellCheck={false}
        value={value || ''}
        placeholder={placeholder}
        onChange={event => onChange(event.target.value)}
      />
    </FieldShell>
  )
}

export function SelectField({ label, path, value, defaultValue, unsetLabel, onChange }: {
  label: string
  path: ProtocolRuntimeSelectPath
  value: string | undefined
  /** Shown when the script stores no value (the backend default). */
  defaultValue?: string
  /**
   * For a required field with no backend default: while unset, the select
   * shows this blank choice instead of a value the script does not store.
   */
  unsetLabel?: string
  onChange: (value: string) => void
}) {
  const options = selectOptionsWithStored(path, value)
  const unset = value === undefined || value === ''
  return (
    <FieldShell label={label} path={path}>
      <select aria-label={label} value={unset && unsetLabel ? '' : value ?? defaultValue ?? ''} onChange={event => onChange(event.target.value)}>
        {unset && unsetLabel && <option value="">{unsetLabel}</option>}
        {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </FieldShell>
  )
}

export function CheckboxField({ label, path, checked, onChange }: {
  label: string
  path: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="checkbox-label" data-catalog-path={path}>
      <input type="checkbox" checked={checked} onChange={event => onChange(event.target.checked)} />
      <span>{label}</span>
    </label>
  )
}

/** An optional whole number: empty means unset (the backend default applies). */
export function IntegerField({ label, path, value, onChange, placeholder, min, max }: {
  label: string
  path: string
  value: number | null | undefined
  onChange: (value: number | undefined) => void
  placeholder?: string
  min?: number
  max?: number
}) {
  return (
    <FieldShell label={label} path={path}>
      <input
        type="number"
        aria-label={label}
        min={min}
        max={max}
        step={1}
        value={value ?? ''}
        placeholder={placeholder}
        onChange={event => onChange(parseOptionalInteger(event.target.value))}
      />
    </FieldShell>
  )
}

/**
 * One entry per line. The author's raw text is kept while it still parses to
 * the stored list, so pressing Enter at the end of a line keeps the new line
 * instead of the re-render dropping it.
 */
export function LineListField({ label, path, value, onChange, placeholder, rows = 3 }: {
  label: string
  path: string
  value: string[] | undefined
  onChange: (value: string[]) => void
  placeholder?: string
  rows?: number
}) {
  const stored = (value || []).join('\n')
  const [draft, setDraft] = useState<string | null>(null)
  const shown = draft !== null && parseLines(draft).join('\n') === stored ? draft : stored
  return (
    <FieldShell label={label} path={path} fullWidth>
      <textarea
        aria-label={label}
        rows={rows}
        spellCheck={false}
        value={shown}
        placeholder={placeholder}
        onChange={event => {
          setDraft(event.target.value)
          onChange(parseLines(event.target.value))
        }}
        onBlur={() => setDraft(null)}
      />
    </FieldShell>
  )
}

/**
 * A string map edited as rows. Keys are trimmed and unique: while a row's
 * typed key is empty, whitespace-only or (after trimming) another row's key,
 * the row keeps its stored key and the typed text stays as a draft with an
 * error, so no entry is silently overwritten and no empty or near-duplicate
 * key reaches the backend. Values stay readable; a {{secret.NAME}} value is
 * labelled as a project secret reference.
 */
export function KeyValueMapField({ label, path, value, onChange, keyPlaceholder, valuePlaceholder, addLabel }: {
  label: string
  path: string
  value: Record<string, string> | undefined
  onChange: (value: Record<string, string> | undefined) => void
  keyPlaceholder: string
  valuePlaceholder: string
  addLabel: string
}) {
  const stored = value || {}
  // Rows keep the order the author sees. A JS object lists integer-like keys
  // first, so after renaming a row to "1" Object.entries would move it; the
  // local order (with the renamed key put back in place) keeps every row, and
  // the in-progress draft, on the entry the author is editing.
  const [order, setOrder] = useState<string[]>([])
  const has = (key: string) => Object.prototype.hasOwnProperty.call(stored, key)
  const orderedKeys = [...order.filter(has), ...Object.keys(stored).filter(key => !order.includes(key))]
  const entries: [string, string][] = orderedKeys.map(key => [key, stored[key]])
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')
  const commit = (next: [string, string][]) => {
    setOrder(next.map(([key]) => key))
    onChange(next.length > 0 ? Object.fromEntries(next) : undefined)
  }
  // The draft follows the stored key of the row being edited, not its index.
  const [keyDraft, setKeyDraft] = useState<{ key: string; text: string } | null>(null)
  const trimmedNewKey = newKey.trim()
  const keyTaken = (candidate: string, exceptIndex: number) =>
    entries.some(([other], otherIndex) => otherIndex !== exceptIndex && other.trim() === candidate)
  const duplicate = trimmedNewKey !== '' && keyTaken(trimmedNewKey, -1)
  const renameIssue = (index: number, text: string): string => {
    const candidate = text.trim()
    if (candidate === '') return 'Enter a key, or remove the entry.'
    if (keyTaken(candidate, index)) return `${candidate} is already defined.`
    return ''
  }
  const references = entries.flatMap(([, entryValue]) => secretReferenceNames(entryValue))
  return (
    <div className="settings-sub-group full-width" data-catalog-path={path} role="group" aria-label={label}>
      <h5>{label}</h5>
      {entries.length === 0 && <div className="cell-muted">No entries defined.</div>}
      {entries.map(([key, entryValue], index) => {
        const draft = keyDraft?.key === key ? keyDraft.text : null
        const issue = draft === null ? '' : renameIssue(index, draft)
        const shownKey = draft !== null && (issue !== '' || draft.trim() === key) ? draft : key
        const issueId = `${path}-key-issue-${index}`
        return (
        <div key={`${path}-${index}`} className="kv-row">
          <input
            className="kv-key"
            aria-label={`${label} key ${index + 1}`}
            aria-invalid={issue !== '' || undefined}
            aria-describedby={issue ? issueId : undefined}
            value={shownKey}
            placeholder={keyPlaceholder}
            onChange={event => {
              const typed = event.target.value
              if (renameIssue(index, typed) !== '') {
                setKeyDraft({ key, text: typed })
                return
              }
              const renamed = typed.trim()
              setKeyDraft({ key: renamed, text: typed })
              if (renamed === key) return
              commit(entries.map(([entryKey, item], entryIndex) => entryIndex === index ? [renamed, item] : [entryKey, item]))
            }}
            onBlur={() => setKeyDraft(null)}
          />
          <input
            className="kv-val"
            aria-label={`${label} value ${index + 1}`}
            autoComplete="off"
            spellCheck={false}
            value={entryValue}
            placeholder={valuePlaceholder}
            onChange={event => commit(entries.map(([entryKey, item], entryIndex) => entryIndex === index ? [entryKey, event.target.value] : [entryKey, item]))}
          />
          <button type="button" className="btn btn-sm btn-ghost" aria-label={`Remove ${label} entry ${key || index + 1}`} onClick={() => {
            setKeyDraft(null)
            commit(entries.filter((_, entryIndex) => entryIndex !== index))
          }}>
            Remove
          </button>
          {issue && <small id={issueId} className="form-error" role="alert">{issue}</small>}
        </div>
        )
      })}
      <div className="kv-row">
        <input className="kv-key" aria-label={`New ${label} key`} value={newKey} placeholder={keyPlaceholder} onChange={event => setNewKey(event.target.value)} />
        <input className="kv-val" aria-label={`New ${label} value`} autoComplete="off" spellCheck={false} value={newValue} placeholder={valuePlaceholder} onChange={event => setNewValue(event.target.value)} />
        <button
          type="button"
          className="btn btn-sm btn-ghost"
          disabled={!trimmedNewKey || duplicate}
          title={duplicate ? `${trimmedNewKey} is already defined` : undefined}
          onClick={() => {
            commit([...entries, [trimmedNewKey, newValue]])
            setNewKey('')
            setNewValue('')
          }}
        >
          {addLabel}
        </button>
      </div>
      {references.length > 0 && (
        <small className="field-hint" data-secret-reference-note>
          References project secret {[...new Set(references)].join(', ')}. Secret values are resolved at run time and never shown here.
        </small>
      )}
    </div>
  )
}

/**
 * A stored value the backend rejects for this protocol. It is shown (never
 * hidden while it still blocks validation) with a way to remove it.
 */
export function UnsupportedStoredValue({ label, path, value, reason, onClear }: {
  label: string
  path: string
  value: string
  reason: string
  onClear: () => void
}) {
  return (
    <div className="parameter-empty-state full-width" role="status" data-unsupported-stored-value={path}>
      <strong>{label}</strong> is set to <code>{value}</code>. {reason}
      <button type="button" className="btn btn-sm btn-ghost" onClick={onClear}>Remove {label.toLowerCase()}</button>
    </div>
  )
}
