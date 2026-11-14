import { type ReactNode } from 'react'

import { type ScenarioRuntimeSettingsDraft } from '../../types'
import { inheritedPlaceholder as inh } from '../runtimeFieldParsers'
import { resetOverrideField, setOverrideField } from '../runtimeSettingsModel'
import { ClearableTextOverrideInput, MaskedTextOverrideInput, SecretOverrideInput, TriStateOverrideSelect } from '../runtime-settings/ScenarioOverrideFields'
import { REDACTED_SECRET } from '../runtime-settings/scenarioOverrideValues'
import ListOverrideTextarea from './ListOverrideTextarea'
import MapOverrideEditor from './MapOverrideEditor'
import { secretNameFieldIssue } from '../runtime-settings/protocolCredentialLeaves'
import {
  PROTOCOL_OVERRIDE_FIELD_SPECS,
  PROTOCOL_OVERRIDE_OPTION_LABELS,
  PROTOCOL_OVERRIDE_PAIRED_PATHS,
  type ProtocolOverrideFieldSpec,
  type ProtocolOverrideSpecSection,
} from '../runtime-settings/protocolOverrideFieldSpecs'

type Draft = ScenarioRuntimeSettingsDraft | undefined

export interface ProtocolOverrideTabProps {
  section: ProtocolOverrideSpecSection
  /**
   * The value this layer runs with at a path, and the value the layers below
   * supply (scenario default, script, engine default). The host resolves both
   * through the shared runtime resolver (effectiveRuntimeValue), the same one
   * that picks each field's provenance badge.
   */
  effectiveAt: (path: string) => unknown
  inheritedAt: (path: string) => unknown
  /** Whether this layer overrides a path, and the value it holds there. */
  overridden: (path: string) => boolean
  overrideValue: (path: string) => unknown
  /** Applies an edit through the runtime settings model (setOverrideField / resetOverrideField). */
  edit: (update: (draft: Draft) => ScenarioRuntimeSettingsDraft) => void
  /**
   * Wraps a control with its provenance badge and reset action. `ownsChildren`
   * marks a map control, which also renders the per-key paths under it.
   */
  renderField: (path: string, label: string, control: ReactNode, wide?: boolean, onReset?: () => void, ownsChildren?: boolean) => ReactNode
  /** Limits the tab to these paths (the Java Env tab shows only the fields its own editor lacks). */
  paths?: readonly string[]
}

function fieldLabel(spec: ProtocolOverrideFieldSpec): string {
  return spec.path.endsWith('_ms') ? `${spec.label} (ms)` : spec.label
}

function optionLabel(path: string, value: string): string {
  return PROTOCOL_OVERRIDE_OPTION_LABELS[`${path}:${value}`] ?? (value === '' ? 'Not set' : value)
}

/**
 * The select value of the catalog's "" choice. The select's own "" is Inherit,
 * so the explicit none (the path listed with no value) needs a value of its own.
 */
const NONE_OPTION_VALUE = '__apex_none__'

function hasText(value: unknown): boolean {
  return value !== undefined && value !== null && String(value) !== ''
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

/**
 * Override controls for one runtime section, driven by the catalog field specs
 * (protocolOverrideFieldSpecs). Every control writes only its own path through
 * the runtime settings model, so a group pins exactly the leaves it edits;
 * maps are edited key by key (`kafka.properties.KEY`), so editing one property
 * never copies the script's other properties into the row. Credentials use the
 * override secret lifecycle: a stored one comes back as "[REDACTED]" and is
 * sent back unchanged, "" clears it.
 */
/**
 * The tab's content as a plain render function: the host calls it during its
 * own render, so every renderField call has run before the host lists the
 * section's remaining overrides.
 */
export function renderProtocolOverrideFields({ section, effectiveAt, inheritedAt, overridden, overrideValue, edit, renderField, paths }: ProtocolOverrideTabProps): ReactNode {
  const specs = PROTOCOL_OVERRIDE_FIELD_SPECS[section].filter(spec => !paths || paths.includes(spec.path))
  const setPath = (path: string, value: unknown) => edit(draft => setOverrideField(draft, path, value))
  const resetPath = (path: string) => edit(draft => resetOverrideField(draft, path))
  const setOrReset = (path: string, value: unknown) => (value === undefined ? resetPath(path) : setPath(path, value))
  /** This layer's string at `path`: undefined inherits; a covered path with no value is an explicit "". */
  const stringOverride = (path: string): string | undefined => {
    if (!overridden(path)) return undefined
    const value = overrideValue(path)
    return value === undefined || value === null ? '' : String(value)
  }
  const placeholderFor = (spec: ProtocolOverrideFieldSpec) => inh(inheritedAt(spec.path), spec.defaultValue === undefined ? 'Inherit' : String(spec.defaultValue))

  // A paired field (the parameter delimiters) applies only together with its
  // partner, so setting one pins the other at its effective value.
  const setPaired = (path: string, value: unknown) => {
    const partner = PROTOCOL_OVERRIDE_PAIRED_PATHS[path]
    if (!partner) {
      setOrReset(path, value)
      return
    }
    if (value === undefined) {
      edit(draft => resetOverrideField(resetOverrideField(draft, path), partner))
      return
    }
    // "" on one delimiter ("Override with no value") clears the pair: the run
    // accepts both unset (the engine default) but refuses one empty and one set.
    if (value === '') {
      edit(draft => setOverrideField(setOverrideField(draft, path, ''), partner, ''))
      return
    }
    // A value on one delimiter pins the partner at its effective value, or, when
    // this layer cleared the partner, at the value below it (else the default).
    const partnerSpec = specs.find(spec => spec.path === partner)
    const partnerCleared = overridden(partner) && !hasText(overrideValue(partner))
    const partnerValue = partnerCleared
      ? (hasText(inheritedAt(partner)) ? inheritedAt(partner) : partnerSpec?.defaultValue)
      : effectiveAt(partner) ?? partnerSpec?.defaultValue
    edit(draft => {
      const next = setOverrideField(draft, path, value)
      return overridden(partner) && !partnerCleared ? next : setOverrideField(next, partner, partnerValue)
    })
  }
  const resetFor = (path: string) => {
    const partner = PROTOCOL_OVERRIDE_PAIRED_PATHS[path]
    return partner ? () => edit(draft => resetOverrideField(resetOverrideField(draft, path), partner)) : undefined
  }

  const control = (spec: ProtocolOverrideFieldSpec): ReactNode => {
    const label = fieldLabel(spec)
    const { path } = spec
    switch (spec.kind) {
      case 'bool': {
        // Every boolean in these sections is a plain Go bool: the server drops
        // false (omitempty) and keeps the path, so a covered path with no value
        // is an explicit Disabled.
        const value = overridden(path) ? Boolean(overrideValue(path)) : undefined
        const below = inheritedAt(path)
        return (
          <TriStateOverrideSelect
            label={label}
            value={value}
            inheritedValue={below === undefined ? Boolean(spec.defaultValue) : Boolean(below)}
            onChange={next => setOrReset(path, next)}
          />
        )
      }
      case 'select': {
        // "" in the catalog options is an explicit none, distinct from Inherit:
        // it lists the path with no value. A stored row listing a path with no
        // value reads as that choice even where the catalog offers none.
        const current = stringOverride(path)
        const options = spec.options ?? []
        const offersNone = options.includes('') || current === ''
        const below = String(inheritedAt(path) ?? spec.defaultValue ?? '')
        return (
          <label>
            <span>{label}</span>
            <select
              className="settings-input"
              aria-label={label}
              value={current === undefined ? '' : current === '' ? NONE_OPTION_VALUE : current}
              onChange={event => {
                const next = event.target.value
                if (next === '') resetPath(path)
                else setPath(path, next === NONE_OPTION_VALUE ? '' : next)
              }}
            >
              <option value="">Inherit ({below ? optionLabel(path, below) : 'Default'})</option>
              {offersNone && <option value={NONE_OPTION_VALUE}>{optionLabel(path, '')}</option>}
              {options.filter(option => option !== '').map(option => <option key={option} value={option}>{optionLabel(path, option)}</option>)}
            </select>
          </label>
        )
      }
      case 'number':
        return (
          <label>
            <span>{label}</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              aria-label={label}
              value={stringOverride(path) ?? ''}
              placeholder={placeholderFor(spec)}
              onChange={event => setPaired(path, event.target.value === '' ? undefined : Math.max(0, Number(event.target.value) || 0))}
            />
          </label>
        )
      case 'secret':
        return (
          <SecretOverrideInput
            label={label}
            value={stringOverride(path)}
            inheritsValue={Boolean(inheritedAt(path))}
            onChange={next => setOrReset(path, next)}
          />
        )
      case 'identity':
      case 'url':
        return (
          <MaskedTextOverrideInput
            label={label}
            value={stringOverride(path)}
            inheritPlaceholder={placeholderFor(spec)}
            onChange={next => setOrReset(path, next || undefined)}
          />
        )
      case 'secret_name': {
        const value = stringOverride(path) ?? ''
        const issue = value && value !== REDACTED_SECRET ? secretNameFieldIssue(value) : undefined
        return (
          <label>
            <span>{label}</span>
            <input
              className="settings-input"
              autoComplete="off"
              spellCheck={false}
              aria-label={label}
              aria-invalid={issue ? true : undefined}
              value={value === REDACTED_SECRET ? '' : value}
              placeholder={value === REDACTED_SECRET ? 'Stored value (unchanged)' : inh(inheritedAt(path), 'Inherit (secret name or {{secret.NAME}})')}
              onChange={event => setOrReset(path, event.target.value || undefined)}
            />
            {issue && <span className="form-error" role="alert">{issue}</span>}
          </label>
        )
      }
      case 'list':
      case 'brokers': {
        const listed = overridden(path) ? (overrideValue(path) as string[] | undefined) ?? [] : undefined
        const below = (inheritedAt(path) as string[] | undefined) ?? []
        const masked = spec.kind === 'brokers' && (listed ?? []).some(broker => broker.includes(REDACTED_SECRET))
        return (
          <label>
            <span>{label}</span>
            <ListOverrideTextarea
              label={label}
              listed={listed}
              placeholder={below.length > 0 ? below.join('\n') : 'Inherit (one per line)'}
              onList={next => setOrReset(path, next.length === 0 ? undefined : next)}
            />
            {masked && (
              <span className="form-hint">
                Masked brokers keep their stored credentials only while the rest of the list is unchanged. After any other edit, re-enter the credential brokers.
              </span>
            )}
          </label>
        )
      }
      case 'map':
      case 'capabilities':
        return <MapOverrideEditor spec={spec} effective={asRecord(effectiveAt(path))} inherited={asRecord(inheritedAt(path))} overridden={overridden} setPath={setPath} resetPath={resetPath} />
      case 'textarea':
      default:
        // Three states: inherit, a value, or listed with no value (the clear).
        return (
          <ClearableTextOverrideInput
            label={label}
            value={stringOverride(path)}
            inheritsValue={hasText(inheritedAt(path))}
            inheritPlaceholder={placeholderFor(spec)}
            multiline={spec.kind === 'textarea'}
            onChange={next => setPaired(path, next)}
          />
        )
    }
  }

  const groups: Array<{ name: string; specs: ProtocolOverrideFieldSpec[] }> = []
  for (const spec of specs) {
    const name = spec.group ?? ''
    const existing = groups.find(group => group.name === name)
    if (existing) existing.specs.push(spec)
    else groups.push({ name, specs: [spec] })
  }

  return (
    <div className="runtime-protocol-overrides" data-testid={`runtime-${section}-overrides`}>
      {groups.map(group => (
        <section key={group.name || 'fields'}>
          {group.name && <h5>{group.name}</h5>}
          <div className="scenario-browser-grid">
            {group.specs.map(spec => {
              const owns = spec.kind === 'map' || spec.kind === 'capabilities'
              const wide = owns || spec.kind === 'textarea' || spec.kind === 'list' || spec.kind === 'brokers'
              return renderField(spec.path, fieldLabel(spec), control(spec), wide, resetFor(spec.path), owns)
            })}
          </div>
        </section>
      ))}
    </div>
  )
}
