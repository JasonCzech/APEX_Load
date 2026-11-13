import type { ReactNode } from 'react'
import './ScenarioOverrideFields.css'
import { RUNTIME_VALUE_PROVENANCE_LABELS, type RuntimeValueProvenance } from './canonicalRuntimeTaxonomy'
import { formatOverrideEntryValue, parseTriStateSelectValue, REDACTED_SECRET, triStateSelectValue } from './scenarioOverrideValues'
import { useValueBeforeTyping } from './useValueBeforeTyping'

// Field-level building blocks for the scenario and group override editor: a
// provenance badge and "Reset to inherited" action per field, a three-state
// boolean select, the masked-secret input, and a panel list of the overridden
// fields a panel's own controls do not show.

export function RuntimeFieldProvenanceBadge({ provenance, label }: { provenance: RuntimeValueProvenance; label?: string }) {
  return (
    <span className={`repository-status-pill runtime-field-provenance provenance-${provenance}`} data-provenance={provenance}>
      {label ?? RUNTIME_VALUE_PROVENANCE_LABELS[provenance]}
    </span>
  )
}

export function ResetToInheritedButton({ fieldLabel, onReset }: { fieldLabel: string; onReset: () => void }) {
  return (
    <button type="button" className="link-button runtime-override-reset" aria-label={`Reset ${fieldLabel} to inherited`} onClick={onReset}>
      Reset to inherited
    </button>
  )
}

export interface RuntimeOverrideFieldProps {
  /** Canonical override path the control edits. */
  path: string
  /** Field label, used in the reset action's accessible name. */
  label: string
  provenance: RuntimeValueProvenance
  /** Label shown for this layer's own provenance (e.g. "Scenario default" on the scenario mount). */
  badgeLabel?: string
  /** True when this layer overrides the path, so it can be reset. */
  overridden: boolean
  onReset: () => void
  wide?: boolean
  children: ReactNode
}

/** A control with its provenance badge and, when this layer overrides it, a reset action. */
export function RuntimeOverrideField({ path, label, provenance, badgeLabel, overridden, onReset, wide, children }: RuntimeOverrideFieldProps) {
  return (
    <div className={`runtime-override-field${wide ? ' scenario-browser-grid-wide' : ''}`} data-override-path={path} data-provenance={provenance}>
      {children}
      <div className="runtime-override-field-meta">
        <RuntimeFieldProvenanceBadge provenance={provenance} label={badgeLabel} />
        {overridden && <ResetToInheritedButton fieldLabel={label} onReset={onReset} />}
      </div>
    </div>
  )
}

export interface TriStateOverrideSelectProps {
  label: string
  ariaLabel?: string
  /** This layer's override, or undefined when it inherits. */
  value: boolean | undefined
  /** What the field resolves to when this layer inherits. */
  inheritedValue: boolean
  /** undefined returns the field to inherited. */
  onChange: (next: boolean | undefined) => void
  enabledLabel?: string
  disabledLabel?: string
  disabled?: boolean
}

/**
 * Inherit / Enabled / Disabled: an override can turn a field the lower layers
 * enable off (or the reverse), and Inherit returns it to the lower layers.
 */
export function TriStateOverrideSelect({
  label,
  ariaLabel,
  value,
  inheritedValue,
  onChange,
  enabledLabel = 'Enabled',
  disabledLabel = 'Disabled',
  disabled,
}: TriStateOverrideSelectProps) {
  return (
    <label>
      <span>{label}</span>
      <select
        className="settings-input"
        aria-label={ariaLabel ?? label}
        value={triStateSelectValue(value)}
        disabled={disabled}
        onChange={event => onChange(parseTriStateSelectValue(event.target.value))}
      >
        <option value="">Inherit ({inheritedValue ? enabledLabel : disabledLabel})</option>
        <option value="true">{enabledLabel}</option>
        <option value="false">{disabledLabel}</option>
      </select>
    </label>
  )
}

export interface SecretOverrideInputProps {
  label: string
  ariaLabel?: string
  /** This layer's value: undefined inherits, REDACTED_SECRET is a stored secret, "" is an explicit clear. */
  value: string | undefined
  /** Whether a lower layer supplies a value this field would inherit. */
  inheritsValue: boolean
  /** undefined returns to inherited, "" clears, REDACTED_SECRET keeps the stored secret. */
  onChange: (next: string | undefined) => void
}

/**
 * A credential typed into an override. A stored one comes back masked and is
 * sent back as the mask (the server keeps it); typing replaces it, and Clear
 * sends "" so the server removes it.
 */
export function SecretOverrideInput({ label, ariaLabel, value, inheritsValue, onChange }: SecretOverrideInputProps) {
  const stored = value === REDACTED_SECRET
  const cleared = value === ''
  const emitFor = useValueBeforeTyping(value)
  const placeholder = stored
    ? 'Stored secret (unchanged)'
    : cleared
      ? 'Cleared'
      : inheritsValue
        ? 'Inherit (set by a lower layer)'
        : 'Inherit'
  return (
    <label>
      <span>{label}</span>
      <input
        className="settings-input"
        type="password"
        autoComplete="off"
        spellCheck={false}
        aria-label={ariaLabel ?? label}
        value={stored || cleared ? '' : value ?? ''}
        placeholder={placeholder}
        // Emptying a typed value returns to what was there before typing: the
        // stored secret, the cleared state the user chose, or inherit.
        onChange={event => onChange(emitFor(event.target.value))}
      />
      {(stored || (inheritsValue && value === undefined)) && (
        <button type="button" className="link-button runtime-override-secret-clear" onClick={() => onChange('')}>
          {stored ? 'Clear stored secret' : 'Override with no value'}
        </button>
      )}
    </label>
  )
}

export interface MaskedTextOverrideInputProps {
  label: string
  /** This layer's value: undefined inherits, REDACTED_SECRET is a stored masked value, "" is an explicit clear. */
  value: string | undefined
  /** Placeholder while the field inherits. */
  inheritPlaceholder?: string
  /** undefined returns to inherited, REDACTED_SECRET keeps the stored value. */
  onChange: (next: string | undefined) => void
}

/**
 * A visible text field (a username) the server may return masked. A masked
 * stored value shows as a placeholder and is sent back as the mask until the
 * user types a replacement; erasing typed text restores the mask.
 */
export function MaskedTextOverrideInput({ label, value, inheritPlaceholder, onChange }: MaskedTextOverrideInputProps) {
  const stored = value === REDACTED_SECRET
  const emitFor = useValueBeforeTyping(value)
  return (
    <label>
      <span>{label}</span>
      <input
        className="settings-input"
        type="text"
        autoComplete="off"
        aria-label={label}
        value={stored ? '' : value ?? ''}
        placeholder={stored ? 'Stored value (unchanged)' : value === '' ? 'Cleared' : inheritPlaceholder}
        onChange={event => onChange(emitFor(event.target.value))}
      />
    </label>
  )
}

export interface RuntimeOverridePanelListProps {
  entries: ReadonlyArray<{ path: string; value: unknown }>
  provenance: RuntimeValueProvenance
  badgeLabel?: string
  onReset: (path: string) => void
}

/** Overridden fields of a panel that its own controls do not show, each with a reset action. */
export function RuntimeOverridePanelList({ entries, provenance, badgeLabel, onReset }: RuntimeOverridePanelListProps) {
  if (entries.length === 0) return null
  return (
    <div className="runtime-override-panel-list" data-testid="runtime-override-panel-list">
      <strong>Other overrides in this section</strong>
      <ul>
        {entries.map(entry => (
          <li key={entry.path} data-override-path={entry.path}>
            <code>{entry.path}</code>
            <span className="runtime-override-panel-value">{formatOverrideEntryValue(entry.path, entry.value)}</span>
            <RuntimeFieldProvenanceBadge provenance={provenance} label={badgeLabel} />
            <ResetToInheritedButton fieldLabel={entry.path} onReset={() => onReset(entry.path)} />
          </li>
        ))}
      </ul>
    </div>
  )
}
