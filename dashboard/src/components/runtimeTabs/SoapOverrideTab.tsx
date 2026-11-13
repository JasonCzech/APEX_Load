import { useState, type ReactNode } from 'react'

import { type ScenarioRuntimeSettingsDraft } from '../../types'
import { type SOAPConfig } from '../../types/scripts'
import { inheritedPlaceholder as inh } from '../runtimeFieldParsers'
import { resetOverrideField, setOverrideField } from '../runtimeSettingsModel'
import { defaultSOAPEnvelopeNamespace, syncSOAPEnvelopeNamespace } from '../scriptEditorRuntimeSettingsModel'
import { SecretOverrideInput, TriStateOverrideSelect } from '../runtime-settings/ScenarioOverrideFields'
import { useValueBeforeTyping } from '../runtime-settings/useValueBeforeTyping'
import { REDACTED_SECRET } from '../runtime-settings/scenarioOverrideValues'
import { SOAP_PASSWORD_TYPE_OPTIONS, SOAP_VERSION_OPTIONS } from '../runtime-settings/scenarioOverrideOptions'

type Draft = ScenarioRuntimeSettingsDraft | undefined

export interface SoapOverrideTabProps {
  /** The SOAP section this layer runs with (inherited, with this layer's paths laid over). */
  effective: SOAPConfig
  /** The SOAP section the layers below supply. */
  inherited: SOAPConfig
  /** Whether this layer overrides a path, and the value it holds there. */
  overridden: (path: string) => boolean
  overrideValue: (path: string) => unknown
  /** Applies an edit through the runtime settings model (setOverrideField / resetOverrideField). */
  edit: (update: (draft: Draft) => ScenarioRuntimeSettingsDraft) => void
  /** Wraps a control with its provenance badge and reset action (onReset replaces the plain path reset). */
  renderField: (path: string, label: string, control: ReactNode, wide?: boolean, onReset?: () => void) => ReactNode
}

const VERSION = 'soap.version'
const FAIL_ON_FAULT = 'soap.fault_handling.fail_on_fault'
const NAMESPACES = 'soap.namespaces'
const TOKEN = 'soap.ws_security.username_token'
const TTL = 'soap.ws_security.timestamp.ttl_seconds'

const sameNamespaces = (a: Record<string, string>, b: Record<string, string>) => {
  const keys = Object.keys(a)
  return keys.length === Object.keys(b).length && keys.every(key => Object.prototype.hasOwnProperty.call(b, key) && a[key] === b[key])
}

/**
 * SOAP overrides for a group (or scenario default) whose scripts use the soap
 * protocol: envelope version, fault handling, namespaces and WS-Security. A
 * WS-Security password typed here follows the override secret lifecycle: a
 * stored one comes back as "[REDACTED]" and is sent back unchanged, "" clears it.
 */
export default function SoapOverrideTab({ effective, inherited, overridden, overrideValue, edit, renderField }: SoapOverrideTabProps) {
  const [newPrefix, setNewPrefix] = useState('')
  const [newUri, setNewUri] = useState('')
  // A prefix edit that would be empty or collide with another row stays local
  // (shown with an inline error) instead of rewriting the map and losing a row.
  const [prefixDrafts, setPrefixDrafts] = useState<Record<number, string>>({})

  const boolOverride = (path: string): boolean | undefined =>
    overridden(path) ? Boolean(overrideValue(path)) : undefined
  const setPath = (path: string, value: unknown) => edit(draft => setOverrideField(draft, path, value))
  const resetPath = (path: string) => edit(draft => resetOverrideField(draft, path))
  const setOrReset = (path: string, value: unknown) => (value === undefined ? resetPath(path) : setPath(path, value))

  const inheritedToken = inherited.ws_security?.username_token ?? {}
  const namespaces = Object.entries(effective.namespaces ?? {})

  // Namespaces are one override leaf: an edit writes the whole effective map, and
  // removing the last entry over inherited namespaces writes an explicit empty map.
  const writeNamespaces = (next: Record<string, string>) => {
    if (Object.keys(next).length === 0 && Object.keys(inherited.namespaces ?? {}).length === 0) {
      resetPath(NAMESPACES)
      return
    }
    setPath(NAMESPACES, next)
  }
  const replaceNamespace = (index: number, prefix: string, uri: string) => {
    const next: Record<string, string> = {}
    namespaces.forEach(([key, value], position) => {
      if (position === index) next[prefix] = uri
      else next[key] = value
    })
    writeNamespaces(next)
  }
  const prefixError = (index: number, prefix: string): string | undefined => {
    const trimmed = prefix.trim()
    if (!trimmed) return 'A namespace prefix is required.'
    if (namespaces.some(([key], position) => position !== index && key === trimmed)) return `Prefix "${trimmed}" is already used.`
    return undefined
  }
  const editPrefix = (index: number, prefix: string, uri: string) => {
    if (prefixError(index, prefix)) {
      setPrefixDrafts(current => ({ ...current, [index]: prefix }))
      return
    }
    setPrefixDrafts(current => {
      const rest = { ...current }
      delete rest[index]
      return rest
    })
    replaceNamespace(index, prefix.trim(), uri)
  }
  const removeNamespace = (index: number) => {
    setPrefixDrafts({})
    writeNamespaces(Object.fromEntries(namespaces.filter((_, position) => position !== index)))
  }

  const usernameValue = overridden(`${TOKEN}.username`) ? String(overrideValue(`${TOKEN}.username`) ?? '') : undefined
  const passwordValue = overridden(`${TOKEN}.password`) ? String(overrideValue(`${TOKEN}.password`) ?? '') : undefined
  const usernameEmitFor = useValueBeforeTyping(usernameValue)
  const inheritedVersion = inherited.version || '1.1'
  // Inheriting the version again also undoes the soapenv sync the version pick
  // made: soapenv goes back to the inherited version's envelope namespace, and
  // the namespaces override is dropped when that leaves exactly the inherited map.
  const inheritVersion = () => {
    const namespacesOverridden = overridden(NAMESPACES)
    const inheritedNamespaces = inherited.namespaces ?? {}
    const resynced = syncSOAPEnvelopeNamespace(inheritedVersion, effective.namespaces, effective.version || '1.1')
    // A soapenv entry the sync added over an inherited map without one goes too.
    const withoutAddedEnvelope = { ...resynced }
    if (!('soapenv' in inheritedNamespaces) && withoutAddedEnvelope.soapenv === defaultSOAPEnvelopeNamespace(inheritedVersion)) delete withoutAddedEnvelope.soapenv
    edit(draft => {
      const next = resetOverrideField(draft, VERSION)
      if (!namespacesOverridden) return next
      if (sameNamespaces(resynced, inheritedNamespaces) || sameNamespaces(withoutAddedEnvelope, inheritedNamespaces)) return resetOverrideField(next, NAMESPACES)
      return sameNamespaces(resynced, effective.namespaces ?? {}) ? next : setOverrideField(next, NAMESPACES, resynced)
    })
  }

  return (
    <div className="runtime-soap-overrides" data-testid="runtime-soap-overrides">
      <h5>Envelope</h5>
      <div className="scenario-browser-grid">
        {renderField(VERSION, 'SOAP version', (
          <label>
            <span>SOAP version</span>
            <select
              className="settings-input"
              aria-label="SOAP version"
              value={overridden(VERSION) ? String(overrideValue(VERSION) ?? '') : ''}
              onChange={event => {
                const version = event.target.value
                if (!version) {
                  inheritVersion()
                  return
                }
                // Keep the soapenv prefix on the envelope namespace of the new
                // version when it still holds the previous version's default.
                const synced = syncSOAPEnvelopeNamespace(version, effective.namespaces, effective.version || '1.1')
                const namespacesChanged = JSON.stringify(synced) !== JSON.stringify(effective.namespaces ?? {})
                edit(draft => {
                  const next = setOverrideField(draft, VERSION, version)
                  return namespacesChanged && effective.namespaces && Object.keys(effective.namespaces).length > 0
                    ? setOverrideField(next, NAMESPACES, synced)
                    : next
                })
              }}
            >
              <option value="">Inherit ({SOAP_VERSION_OPTIONS.find(option => option.value === inheritedVersion)?.label ?? inheritedVersion})</option>
              {SOAP_VERSION_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
        ), false, inheritVersion)}
        {renderField(FAIL_ON_FAULT, 'Fail on SOAP fault', (
          <TriStateOverrideSelect
            label="Fail on SOAP fault"
            value={boolOverride(FAIL_ON_FAULT)}
            // An unset fail_on_fault is false (the action passes on a SOAP Fault).
            inheritedValue={Boolean(inherited.fault_handling?.fail_on_fault)}
            onChange={next => setOrReset(FAIL_ON_FAULT, next)}
          />
        ))}
      </div>

      <h5>Namespaces</h5>
      {renderField(NAMESPACES, 'Namespaces', (
        <div className="runtime-override-kv-table" role="group" aria-label="SOAP namespaces">
          {namespaces.length === 0 && <p className="form-hint">No namespaces. Generated envelopes use the version default.</p>}
          {namespaces.map(([prefix, uri], position) => {
            const draftPrefix = prefixDrafts[position]
            const error = draftPrefix === undefined ? undefined : prefixError(position, draftPrefix)
            return (
              <div key={position} className="runtime-override-kv-row">
                <input
                  className="settings-input"
                  aria-label={`Namespace ${position + 1} prefix`}
                  aria-invalid={error ? true : undefined}
                  value={draftPrefix ?? prefix}
                  onChange={event => editPrefix(position, event.target.value, uri)}
                />
                <input className="settings-input" aria-label={`Namespace ${position + 1} URI`} value={uri} onChange={event => replaceNamespace(position, prefix, event.target.value)} />
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  aria-label={`Remove namespace ${prefix || position + 1}`}
                  onClick={() => removeNamespace(position)}
                >
                  Remove
                </button>
                {error && <p className="form-error" role="alert" data-testid={`soap-namespace-prefix-error-${position}`}>{error} The saved prefix stays "{prefix}" until this is fixed.</p>}
              </div>
            )
          })}
          <div className="runtime-override-kv-row">
            <input className="settings-input" aria-label="New namespace prefix" placeholder="Namespace prefix" value={newPrefix} onChange={event => setNewPrefix(event.target.value)} />
            <input className="settings-input" aria-label="New namespace URI" placeholder="Namespace URI" value={newUri} onChange={event => setNewUri(event.target.value)} />
            <button
              type="button"
              className="btn btn-sm btn-secondary"
              disabled={!newPrefix.trim()}
              onClick={() => {
                writeNamespaces({ ...(effective.namespaces ?? {}), [newPrefix.trim()]: newUri.trim() })
                setNewPrefix('')
                setNewUri('')
              }}
            >
              Add namespace
            </button>
          </div>
        </div>
      ), true)}

      <h5>WS-Security username token</h5>
      <div className="scenario-browser-grid">
        {renderField(`${TOKEN}.username`, 'WS-Security username', (
          <label>
            <span>WS-Security username</span>
            <input
              className="settings-input"
              autoComplete="off"
              // A masked stored username is sent back unchanged until replaced.
              value={usernameValue === REDACTED_SECRET ? '' : usernameValue ?? ''}
              placeholder={usernameValue === REDACTED_SECRET ? 'Stored username (unchanged)' : inh(inheritedToken.username)}
              // Erasing typed text over a masked stored username sends the mask back.
              onChange={event => setOrReset(`${TOKEN}.username`, usernameEmitFor(event.target.value))}
            />
          </label>
        ))}
        {renderField(`${TOKEN}.password`, 'WS-Security password', (
          <SecretOverrideInput
            label="WS-Security password"
            value={passwordValue}
            inheritsValue={Boolean(inheritedToken.password)}
            onChange={next => setOrReset(`${TOKEN}.password`, next)}
          />
        ))}
        {renderField(`${TOKEN}.password_type`, 'Password type', (
          <label>
            <span>Password type</span>
            <select
              className="settings-input"
              value={overridden(`${TOKEN}.password_type`) ? String(overrideValue(`${TOKEN}.password_type`) ?? '') : ''}
              onChange={event => setOrReset(`${TOKEN}.password_type`, event.target.value || undefined)}
            >
              <option value="">Inherit ({SOAP_PASSWORD_TYPE_OPTIONS.find(option => option.value === (inheritedToken.password_type || 'text'))?.label ?? inheritedToken.password_type})</option>
              {SOAP_PASSWORD_TYPE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
        ))}
        {renderField(`${TOKEN}.nonce`, 'Include nonce', (
          <TriStateOverrideSelect label="Include nonce" value={boolOverride(`${TOKEN}.nonce`)} inheritedValue={Boolean(inheritedToken.nonce)} onChange={next => setOrReset(`${TOKEN}.nonce`, next)} />
        ))}
        {renderField(`${TOKEN}.created`, 'Include created timestamp', (
          <TriStateOverrideSelect label="Include created timestamp" value={boolOverride(`${TOKEN}.created`)} inheritedValue={Boolean(inheritedToken.created)} onChange={next => setOrReset(`${TOKEN}.created`, next)} />
        ))}
      </div>
      <h5>WS-Security timestamp</h5>
      <div className="scenario-browser-grid">
        {renderField(TTL, 'Timestamp TTL (seconds)', (
          <label>
            <span>Timestamp TTL (seconds)</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              value={overridden(TTL) ? String(overrideValue(TTL) ?? '') : ''}
              placeholder={inh(inherited.ws_security?.timestamp?.ttl_seconds, '0')}
              onChange={event => setOrReset(TTL, event.target.value === '' ? undefined : Math.max(0, Number(event.target.value) || 0))}
            />
          </label>
        ))}
      </div>
    </div>
  )
}
