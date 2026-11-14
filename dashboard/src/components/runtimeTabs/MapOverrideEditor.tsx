import { useState } from 'react'

import { isSensitiveCredentialPreviewHeader } from '../credentialPreviewPolicy'
import { isCredentialCapabilityKey, isSensitiveSettingName } from '../runtime-settings/protocolCredentialLeaves'
import { REDACTED_SECRET } from '../runtime-settings/scenarioOverrideValues'
import { type ProtocolOverrideFieldSpec } from '../runtime-settings/protocolOverrideFieldSpecs'
import { useValueBeforeTyping } from '../runtime-settings/useValueBeforeTyping'

const NOT_PARSED = Symbol('not parsed')

/**
 * Capability text as a value of the given type. Text is always a string (an
 * Appium platformVersion "17.0" or a numeric udid stays a string); a number
 * needs number text; JSON takes any JSON value. Text that does not fit the
 * type is not written.
 */
function parseCapability(text: string, type: 'text' | 'number' | 'json'): unknown {
  if (type === 'text') return text
  const trimmed = text.trim()
  if (type === 'number') return trimmed !== '' && Number.isFinite(Number(trimmed)) ? Number(trimmed) : NOT_PARSED
  try { return JSON.parse(trimmed) } catch { return NOT_PARSED }
}

/** The type an existing capability value keeps while it is edited. */
function capabilityType(value: unknown): 'text' | 'number' | 'json' {
  if (typeof value === 'string') return 'text'
  if (typeof value === 'number') return 'number'
  return 'json'
}

/** A capability value as the text field shows it: strings as written, anything else as JSON. */
function capabilityText(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value)
}

/**
 * Whether a map key names a credential, the way the backend classifies the
 * leaf (runtimeCredentialFreeText / runtimeCredentialHeader), so its value is
 * typed hidden.
 */
function credentialKey(spec: ProtocolOverrideFieldSpec, key: string): boolean {
  const name = key.trim()
  if (!name) return false
  switch (spec.credentialKeys) {
    case 'setting': return isSensitiveSettingName(name)
    case 'header': return isSensitiveCredentialPreviewHeader(name)
    case 'capability': return isSensitiveSettingName(name) || isCredentialCapabilityKey(name)
    default: return false
  }
}

const hasKey = (record: Record<string, unknown>, key: string) => Object.prototype.hasOwnProperty.call(record, key)

/**
 * One map value input. A capability keeps the type it has (a string stays a
 * string, a number a number); while a typed value does not parse yet ("12."),
 * the text is kept locally and nothing is written.
 */
function MapValueInput({ label, value, capability, secret, readOnly, onValue }: {
  label: string
  value: unknown
  capability: boolean
  /** A credential-named key: the value is typed hidden. */
  secret: boolean
  /** The value is shown but cannot be edited (only removed). */
  readOnly?: boolean
  onValue: (value: unknown) => void
}) {
  const [pending, setPending] = useState<string | null>(null)
  const masked = value === REDACTED_SECRET
  const type = capability ? capabilityType(value) : 'text'
  // Erasing typed text over a stored (masked) value sends the mask back,
  // however many characters were typed first.
  const emitFor = useValueBeforeTyping(typeof value === 'string' ? value : undefined)
  return (
    <input
      className="settings-input"
      type={secret ? 'password' : 'text'}
      autoComplete="off"
      spellCheck={secret ? false : undefined}
      readOnly={readOnly}
      aria-label={label}
      aria-invalid={pending !== null ? true : undefined}
      value={pending ?? (masked ? '' : capabilityText(value))}
      placeholder={masked ? 'Stored secret (unchanged)' : undefined}
      onBlur={() => setPending(null)}
      onChange={event => {
        if (readOnly) return
        const text = event.target.value
        if (emitFor(text) === REDACTED_SECRET && text === '') {
          setPending(null)
          onValue(REDACTED_SECRET)
          return
        }
        const parsed = parseCapability(text, type)
        if (parsed === NOT_PARSED) {
          setPending(text)
          return
        }
        setPending(null)
        onValue(parsed)
      }}
    />
  )
}

interface MapOverrideEditorProps {
  spec: ProtocolOverrideFieldSpec
  effective: Record<string, unknown>
  inherited: Record<string, unknown>
  overridden: (path: string) => boolean
  setPath: (path: string, value: unknown) => void
  resetPath: (path: string) => void
}

/**
 * A string map (or the Appium capability map) edited key by key: a value
 * edit lists `path.KEY`, removing a key the lower layers supply lists it with
 * no value (the backend deletes it) and shows it struck out with its own
 * Reset to inherited, and removing one only this layer added resets it. A
 * stored credential value comes back masked and is sent back unchanged until
 * replaced.
 */
export default function MapOverrideEditor({ spec, effective, inherited, overridden, setPath, resetPath }: MapOverrideEditorProps) {
  const removeOnly = Boolean(spec.removeOnly)
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')
  const [newType, setNewType] = useState<'text' | 'json'>('text')
  const capability = spec.kind === 'capabilities'
  const entries = Object.entries(effective)
  const keyPath = (key: string) => `${spec.path}.${key}`
  const remove = (key: string) => {
    if (hasKey(inherited, key)) setPath(keyPath(key), undefined)
    else resetPath(keyPath(key))
  }
  // Inherited keys this layer removed: listed with no value, so absent from the effective map.
  const removed = Object.keys(inherited).filter(key => !hasKey(effective, key) && overridden(keyPath(key)))
  const resetButton = (key: string) => (
    <button type="button" className="link-button" aria-label={`Reset ${spec.label} ${key} to inherited`} onClick={() => resetPath(keyPath(key))}>
      Reset to inherited
    </button>
  )
  const newParsed = capability ? parseCapability(newValue, newType) : newValue
  return (
    <div className="runtime-override-kv-table" role="group" aria-label={spec.label}>
      <span>{spec.label}</span>
      {removeOnly && (
        <p className="form-hint">
          Generic properties are not executable, so none can be added or edited here. Use the typed fields instead; this list only removes properties a lower layer supplies.
        </p>
      )}
      {entries.length === 0 && removed.length === 0 && <p className="form-hint">No entries.</p>}
      {entries.map(([key, value]) => (
        <div key={key} className="runtime-override-kv-row" data-override-path={keyPath(key)}>
          <code>{key}</code>
          <MapValueInput
            label={`${spec.label} ${key} value`}
            value={value}
            capability={capability}
            secret={credentialKey(spec, key)}
            readOnly={removeOnly}
            onValue={next => setPath(keyPath(key), next)}
          />
          {overridden(keyPath(key)) && hasKey(inherited, key) && resetButton(key)}
          <button type="button" className="btn btn-sm btn-ghost" aria-label={`Remove ${spec.label} ${key}`} onClick={() => remove(key)}>
            Remove
          </button>
        </div>
      ))}
      {removed.map(key => (
        <div key={key} className="runtime-override-kv-row runtime-override-kv-removed" data-override-path={keyPath(key)}>
          <code><s>{key}</s></code>
          <span className="form-hint">Removed for this layer</span>
          {resetButton(key)}
        </div>
      ))}
      {!removeOnly && <div className="runtime-override-kv-row">
        <input className="settings-input" aria-label={`New ${spec.label} key`} placeholder="Key" value={newKey} onChange={event => setNewKey(event.target.value)} />
        <input
          className="settings-input"
          type={credentialKey(spec, newKey) ? 'password' : 'text'}
          autoComplete="off"
          aria-label={`New ${spec.label} value`}
          aria-invalid={newParsed === NOT_PARSED ? true : undefined}
          placeholder={newType === 'json' ? 'JSON value' : 'Value'}
          value={newValue}
          onChange={event => setNewValue(event.target.value)}
        />
        {capability && (
          <select className="settings-input" aria-label={`New ${spec.label} value type`} value={newType} onChange={event => setNewType(event.target.value === 'json' ? 'json' : 'text')}>
            <option value="text">Text</option>
            <option value="json">JSON (number, boolean, object or list)</option>
          </select>
        )}
        <button
          type="button"
          className="btn btn-sm btn-secondary"
          disabled={!newKey.trim() || newParsed === NOT_PARSED}
          onClick={() => {
            setPath(keyPath(newKey.trim()), newParsed)
            setNewKey('')
            setNewValue('')
            setNewType('text')
          }}
        >
          Add entry
        </button>
      </div>}
    </div>
  )
}
