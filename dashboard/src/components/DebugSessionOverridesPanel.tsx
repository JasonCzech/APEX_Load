import { useMemo, useState } from 'react'
import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import { RuntimeSettingsEditor } from './runtime-settings/RuntimeSettingsEditor'
import type { CanonicalRuntimeGroupId } from './runtime-settings/canonicalRuntimeTaxonomy'
import {
  MaskedTextOverrideInput,
  RuntimeOverrideField,
  RuntimeOverridePanelList,
  SecretOverrideInput,
  TriStateOverrideSelect,
} from './runtime-settings/ScenarioOverrideFields'
import { formatOverrideValue } from './runtime-settings/scenarioOverrideValues'
import {
  DEBUG_PACING_SKIPPED_LABEL,
  DEBUG_SESSION_PRECEDENCE,
  NATIVE_REPLAY_SESSION_OVERRIDES_NOTE,
  inheritedDebugValue,
  isSessionOverridden,
  resetSessionOverride,
  sessionOverrideCount,
  sessionOverrideProvenance,
  sessionOverrideValue,
  setSessionOverride,
  type DebugSessionOverrideDraft,
} from './debugSessionOverrides'
import {
  DEBUG_HEADER_MAP_PATH,
  debugOverrideFieldsFor,
  isDebugHttpLikeProtocol,
  sessionHeaderOverrides,
  unrenderedSessionOverrides,
  type DebugOverrideFieldSpec,
} from './debugSessionOverrideFields'
import { ENGINE_DEFAULTS_FALLBACK } from './debugEngineDefaults'

// The debugger's session runtime overrides, in the shared runtime settings
// editor (debug mode): temporary values for the next debug run only. Edits stay
// in the debugger's session options; script.config is never touched until the
// user promotes them through the Author edit path.

export interface DebugSessionOverridesPanelProps {
  script: ScriptDefinition
  overrides?: DebugSessionOverrideDraft
  onChange: (next: DebugSessionOverrideDraft | undefined) => void
  /** A run is live: overrides apply to the next run, so they stay read-only now. */
  disabled: boolean
  /**
   * The run sends no script body: the script is saved, has no unsaved edits and
   * its stored revision is known (see storedSessionSendsScriptBody).
   */
  storedUnmodified: boolean
  /** Debug options skip think time, so think-time overrides have no effect. */
  ignoreThinkTime: boolean
  engineDefaults?: ScriptConfig
  /** Writes the overrides into script.config (marks the script modified), then clears them. */
  onPromote?: () => number
}

export default function DebugSessionOverridesPanel({
  script,
  overrides,
  onChange,
  disabled,
  storedUnmodified,
  ignoreThinkTime,
  engineDefaults,
  onPromote,
}: DebugSessionOverridesPanelProps) {
  const [groupId, setGroupId] = useState<CanonicalRuntimeGroupId>('target_identity')
  const [headerName, setHeaderName] = useState('')
  const [headerValue, setHeaderValue] = useState('')
  const [notice, setNotice] = useState('')
  const protocol = script.protocol || 'http'
  const httpLike = isDebugHttpLikeProtocol(protocol)
  const count = sessionOverrideCount(overrides)
  const provenance = useMemo(() => sessionOverrideProvenance(overrides), [overrides])
  const layers = { script: script.config, engine: engineDefaults ?? ENGINE_DEFAULTS_FALLBACK }
  const fields = debugOverrideFieldsFor(groupId, protocol)
  const leftover = unrenderedSessionOverrides(overrides, protocol)

  const set = (path: string, value: unknown) => { setNotice(''); onChange(setSessionOverride(overrides, path, value)) }
  const reset = (path: string) => { setNotice(''); onChange(resetSessionOverride(overrides, path)) }

  const inheritedLabel = (path: string) => {
    const inherited = inheritedDebugValue(path, layers)
    const fallback = inherited.value === undefined ? inheritedDebugValue(path, { engine: ENGINE_DEFAULTS_FALLBACK }) : inherited
    return fallback.value === undefined || fallback.value === '' ? 'Inherit (not set)' : `Inherit (${formatOverrideValue(fallback.value)})`
  }

  const renderControl = (spec: DebugOverrideFieldSpec) => {
    const overridden = isSessionOverridden(overrides, spec.path)
    const value = sessionOverrideValue(overrides, spec.path)
    const inherited = inheritedDebugValue(spec.path, layers)
    switch (spec.kind) {
      case 'tri':
        return (
          <TriStateOverrideSelect
            label={spec.label}
            value={overridden ? Boolean(value) : undefined}
            inheritedValue={Boolean(inherited.value)}
            disabled={disabled}
            onChange={next => (next === undefined ? reset(spec.path) : set(spec.path, next))}
          />
        )
      case 'secret':
        return (
          <SecretOverrideInput
            label={spec.label}
            value={overridden ? (value === undefined || value === null ? '' : String(value)) : undefined}
            inheritsValue={Boolean(inherited.value)}
            onChange={next => (next === undefined ? reset(spec.path) : set(spec.path, next))}
          />
        )
      case 'username':
        return (
          <MaskedTextOverrideInput
            label={spec.label}
            value={overridden ? String(value ?? '') : undefined}
            inheritPlaceholder={inheritedLabel(spec.path)}
            onChange={next => (next === undefined ? reset(spec.path) : set(spec.path, next))}
          />
        )
      case 'select':
        return (
          <label>
            <span>{spec.label}</span>
            <select
              className="settings-input"
              aria-label={spec.label}
              value={overridden ? String(value ?? '') : ''}
              onChange={event => (event.target.value === '' ? reset(spec.path) : set(spec.path, event.target.value))}
            >
              <option value="">{inheritedLabel(spec.path)}</option>
              {(spec.options ?? []).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
        )
      case 'number':
        return (
          <label>
            <span>{spec.label}{spec.unit ? ` (${spec.unit})` : ''}</span>
            <input
              className="settings-input"
              type="number"
              aria-label={spec.label}
              min={spec.min}
              max={spec.max}
              step={spec.decimal ? 'any' : 1}
              value={overridden && value !== undefined && value !== null ? String(value) : ''}
              placeholder={inheritedLabel(spec.path)}
              onChange={event => {
                const raw = event.target.value.trim()
                if (raw === '') return reset(spec.path)
                const parsed = Number(raw)
                if (!Number.isFinite(parsed)) return
                const bounded = Math.min(spec.max ?? Number.POSITIVE_INFINITY, Math.max(spec.min ?? Number.NEGATIVE_INFINITY, spec.decimal ? parsed : Math.trunc(parsed)))
                set(spec.path, bounded)
              }}
            />
          </label>
        )
      default:
        return (
          <label>
            <span>{spec.label}</span>
            <input
              className="settings-input"
              type={spec.kind === 'url' ? 'url' : 'text'}
              aria-label={spec.label}
              autoComplete="off"
              value={overridden ? String(value ?? '') : ''}
              placeholder={inheritedLabel(spec.path)}
              onChange={event => (event.target.value.trim() === '' ? reset(spec.path) : set(spec.path, event.target.value))}
            />
          </label>
        )
    }
  }

  const headerRows = sessionHeaderOverrides(overrides)
  const addHeader = () => {
    const name = headerName.trim()
    if (!name) return
    set(`${DEBUG_HEADER_MAP_PATH}.${name}`, headerValue)
    setHeaderName('')
    setHeaderValue('')
  }

  const groupNote = (() => {
    if (groupId === 'run_logic_pacing') return `Pacing: ${DEBUG_PACING_SKIPPED_LABEL}. Iterations come from the Debug run mode, and the action sequence follows the script.`
    if (groupId === 'think_time' && ignoreThinkTime) return 'Ignore think time is on in Debug options, so this run skips think time. These overrides apply once you turn it off.'
    if (groupId === 'errors_logging_diagnostics') return 'Log level and what follows a failure are Debug options; they win over these settings.'
    return ''
  })()
  const showHeaders = groupId === 'protocol_behavior_session' && httpLike

  return (
    <section className="debug-session-runtime-overrides" aria-label="Session runtime overrides" data-testid="debug-session-runtime-overrides">
      <div className="debugger-settings-head">
        <strong>Session runtime overrides{count > 0 ? ` (${count})` : ''}</strong>
      </div>
      <p className="replay-panel-subtitle" data-testid="debug-session-overrides-source">
        {storedUnmodified
          ? 'Runs the saved script with these overrides applied by the controller. The script itself is not sent, so imported credentials and Java VUser approval stay valid.'
          : 'The run sends the editor’s copy of the script (it has unsaved edits, or its saved revision is not loaded), and these overrides apply on top of it. Overrides never change the script; promote them to keep them.'}
      </p>
      {protocol === 'browser' && <p className="replay-panel-subtitle" data-testid="debug-session-overrides-native-replay">{NATIVE_REPLAY_SESSION_OVERRIDES_NOTE}</p>}
      <p className="replay-panel-subtitle">{DEBUG_SESSION_PRECEDENCE}</p>
      <RuntimeSettingsEditor mode="debug" protocol={protocol} script={script} activeGroupId={groupId} onSelectGroup={setGroupId} provenance={provenance}>
        <fieldset className="replay-options-grid debug-session-override-fields" disabled={disabled} data-canonical-group={groupId}>
          {groupNote && <p className="form-hint">{groupNote}</p>}
          {fields.map(spec => (
            <RuntimeOverrideField
              key={spec.path}
              path={spec.path}
              label={spec.label}
              provenance={isSessionOverridden(overrides, spec.path) ? 'session_override' : inheritedDebugValue(spec.path, layers).provenance}
              overridden={isSessionOverridden(overrides, spec.path)}
              onReset={() => reset(spec.path)}
            >
              {renderControl(spec)}
            </RuntimeOverrideField>
          ))}
          {showHeaders && (
            <div className="full-width debug-session-header-overrides">
              <span>Default headers</span>
              {headerRows.map(row => (
                <RuntimeOverrideField key={row.path} path={row.path} label={`Header ${row.name}`} provenance="session_override" overridden onReset={() => reset(row.path)}>
                  <label>
                    <span>{row.name}</span>
                    <input className="settings-input" aria-label={`Header ${row.name}`} autoComplete="off" value={String(row.value ?? '')} onChange={event => set(row.path, event.target.value)} />
                  </label>
                </RuntimeOverrideField>
              ))}
              <div className="debug-session-header-add">
                <input className="settings-input" aria-label="Header name" placeholder="Header name" autoComplete="off" value={headerName} onChange={event => setHeaderName(event.target.value)} />
                <input className="settings-input" aria-label="Header value" placeholder="Value" autoComplete="off" value={headerValue} onChange={event => setHeaderValue(event.target.value)} />
                <button type="button" className="btn btn-sm btn-secondary" disabled={!headerName.trim()} onClick={addHeader}>Add header</button>
              </div>
              <small>Adds to or replaces the script’s default header of the same name for this session.</small>
            </div>
          )}
          {fields.length === 0 && !showHeaders && groupId !== 'run_logic_pacing' && (
            <p className="form-hint">No session overrides for this group and protocol. Change these settings in Runtime settings.</p>
          )}
          <RuntimeOverridePanelList
            entries={leftover.map(path => ({ path, value: sessionOverrideValue(overrides, path) }))}
            provenance="session_override"
            onReset={reset}
          />
        </fieldset>
      </RuntimeSettingsEditor>
      <div className="debug-session-overrides-actions">
        <button type="button" className="btn btn-sm btn-ghost" disabled={disabled || count === 0} onClick={() => { setNotice(''); onChange(undefined) }}>
          Clear session runtime overrides
        </button>
        <button
          type="button"
          className="btn btn-sm btn-secondary"
          disabled={disabled || count === 0 || !onPromote}
          title="Writes these values into the script’s runtime settings as an unsaved edit, then clears them here."
          onClick={() => {
            const promoted = onPromote?.() ?? 0
            setNotice(promoted > 0 ? `Promoted ${promoted} ${promoted === 1 ? 'setting' : 'settings'} to the script’s runtime settings. Save the script to keep ${promoted === 1 ? 'it' : 'them'}.` : '')
          }}
        >
          Promote session overrides to script
        </button>
      </div>
      {notice && <p role="status" className="form-hint">{notice}</p>}
    </section>
  )
}
