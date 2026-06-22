import { useState } from 'react'

import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { inheritedPlaceholder as inh } from './runtimeFieldParsers'
import {
  EMPTY_RUNTIME_DRAFT,
  formatEffectiveRuntimeField,
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  overrideFromRuntimeDraft,
  runtimeOverrideFromScriptConfig,
  RUNTIME_FIELD_SOURCE_LABEL,
  type RuntimeFieldKey,
} from './runtimeSettingsModel'
import WebHttpTab from './runtimeTabs/WebHttpTab'

export interface RuntimeEditorGroup {
  name: string
  scriptId: string
  protocol?: string
  /** The group's script DSL config, used to resolve actual script-default values. */
  scriptConfig?: ScriptConfig
}

export interface RuntimeSettingsGroupEditorProps {
  groups: RuntimeEditorGroup[]
  focusedIndex: number
  onFocusChange: (index: number) => void
  /** Scenario-wide defaults (the baseline every group inherits). */
  scenarioDefault?: RuntimeSettingsOverride
  /** Engine config defaults (from GET /api/v1/dsl/config-defaults) — the lowest
   *  inherited layer, so the effective value is shown even for unset fields. */
  engineDefaults?: ScriptConfig
  /** Per-group overrides, aligned to `groups`. */
  overrides: Array<ScenarioRuntimeSettingsDraft | undefined>
  onChange: (index: number, next: ScenarioRuntimeSettingsDraft | undefined) => void
  onApplyToGroups: (sourceIndex: number, targetIndexes: number[]) => void
}

const FIELD_LABELS: Record<RuntimeFieldKey, string> = {
  iterations: 'Iterations',
  pacing: 'Pacing',
  think_time: 'Think time',
  browser: 'Browser device',
  log_level: 'Log level',
  auto_transactions: 'Auto-transactions',
  error_handling: 'Error handling',
  log: 'Logging',
  run_logic: 'Run logic',
  http: 'HTTP transport',
  web_runtime: 'Web runtime',
  browser_config: 'Browser config',
}

const SUMMARY_FIELDS: RuntimeFieldKey[] = [
  'iterations',
  'pacing',
  'think_time',
  'log_level',
  'auto_transactions',
  'error_handling',
  'browser',
  'log',
  'run_logic',
  'http',
  'web_runtime',
  'browser_config',
]

/**
 * The horizontal tabs shown below the Focus-group dropdown. Tab 1 (Summary) is the
 * effective-value table; the rest mirror the Script Editor's Runtime Settings tabs.
 * `summary` plus the universal sections always show; `http_transport`/`web_http` are
 * gated by protocol applicability and hidden when not applicable.
 */
type RuntimeTabId =
  | 'summary'
  | 'think_time'
  | 'pacing'
  | 'run_logic'
  | 'error_handling'
  | 'http_transport'
  | 'web_http'
  | 'logging'

/** Nested sub-tabs within the HTTP Transport tab (mirrors the Script Editor). */
type HttpSubTabId = 'timeouts' | 'connection' | 'tls' | 'proxy' | 'authentication' | 'browser_emulation'

function groupLabel(group: RuntimeEditorGroup, index: number): string {
  return group.name || `Group ${index + 1}`
}

export default function RuntimeSettingsGroupEditor({
  groups,
  focusedIndex,
  onFocusChange,
  scenarioDefault,
  engineDefaults,
  overrides,
  onChange,
  onApplyToGroups,
}: RuntimeSettingsGroupEditorProps) {
  const [applyOpen, setApplyOpen] = useState(false)
  const [applyTargets, setApplyTargets] = useState<number[]>([])
  const [rawActiveTab, setRawActiveTab] = useState<RuntimeTabId>('summary')
  const [rawHttpSubTab, setRawHttpSubTab] = useState<HttpSubTabId>('timeouts')

  if (groups.length === 0) {
    return (
      <div className="scenario-subpanel">
        <div className="scenario-assignment-header">
          <div>
            <h4>Per-group runtime overrides</h4>
            <p>Add a script group to layer per-group runtime overrides on top of the scenario defaults.</p>
          </div>
        </div>
      </div>
    )
  }

  const index = Math.min(focusedIndex, groups.length - 1)
  const focusGroup = groups[index]
  const draft = overrides[index] ?? EMPTY_RUNTIME_DRAFT
  const groupOverride = overrideFromRuntimeDraft(overrides[index])
  const scriptDefault = runtimeOverrideFromScriptConfig(focusGroup.scriptConfig)
  const engineDefault = runtimeOverrideFromScriptConfig(engineDefaults)

  // The value each field INHERITS when not overridden at the group level: scenario
  // default over script config over engine default (engine lowest). Used to fill the
  // input placeholders (via `inh`) so the user sees the actual inherited value,
  // never a bare "Inherit".
  const inheritedHttp = {
    ...engineDefault?.http, ...scriptDefault?.http, ...scenarioDefault?.http,
    tls: { ...engineDefault?.http?.tls, ...scriptDefault?.http?.tls, ...scenarioDefault?.http?.tls },
    proxy: { ...engineDefault?.http?.proxy, ...scriptDefault?.http?.proxy, ...scenarioDefault?.http?.proxy },
    auth: { ...engineDefault?.http?.auth, ...scriptDefault?.http?.auth, ...scenarioDefault?.http?.auth },
  }
  const inheritedBrowser = {
    ...engineDefault?.browser_config, ...scriptDefault?.browser_config, ...scenarioDefault?.browser_config,
    viewport: { ...engineDefault?.browser_config?.viewport, ...scriptDefault?.browser_config?.viewport, ...scenarioDefault?.browser_config?.viewport },
  }
  const inheritedThink = { ...engineDefault?.think_time, ...scriptDefault?.think_time, ...scenarioDefault?.think_time }
  const inheritedPacing = { ...engineDefault?.pacing, ...scriptDefault?.pacing, ...scenarioDefault?.pacing }
  const inheritedError = { ...engineDefault?.error_handling, ...scriptDefault?.error_handling, ...scenarioDefault?.error_handling }
  const inheritedRunLogic = { ...engineDefault?.run_logic, ...scriptDefault?.run_logic, ...scenarioDefault?.run_logic }
  const inheritedWeb = { ...engineDefault?.web_runtime, ...scriptDefault?.web_runtime, ...scenarioDefault?.web_runtime }
  const inheritedIterations = scenarioDefault?.iterations ?? scriptDefault?.iterations ?? engineDefault?.iterations ?? inheritedRunLogic.iterations

  const browserApplicable = isRuntimeFieldApplicable('browser', focusGroup.protocol)
  const httpApplicable = isRuntimeFieldApplicable('http', focusGroup.protocol)
  const webRuntimeApplicable = isRuntimeFieldApplicable('web_runtime', focusGroup.protocol)

  const patch = (next: Partial<ScenarioRuntimeSettingsDraft>) => {
    const merged: ScenarioRuntimeSettingsDraft = { ...EMPTY_RUNTIME_DRAFT, ...draft, ...next }
    onChange(index, isRuntimeDraftEmpty(merged) ? undefined : merged)
  }

  const patchHttp = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['http']>>) => {
    patch({ http: { ...draft.http, ...next } })
  }

  // think_time / pacing carry the full DSL config; merge into the section, keeping the
  // required type/mean_ms so the section stays valid. Clearing routes through the type
  // select (Inherit) which sets the whole section undefined.
  const patchThinkTime = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['think_time']>>) => {
    // Seed a brand-new override from the inherited mean rather than 0. The backend
    // replaces the whole think_time section, and mean_ms is required, so editing
    // only the distribution (or a secondary field) on an inheriting group must not
    // silently drop the effective mean to 0 — carry the inherited value instead.
    const base = draft.think_time ?? { type: 'fixed', mean_ms: inheritedThink.mean_ms ?? 0 }
    patch({ think_time: { ...base, ...next } })
  }

  const patchPacing = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['pacing']>>) => {
    const base = draft.pacing ?? { type: 'none' }
    patch({ pacing: { ...base, ...next } })
  }

  const toggleLogOption = (option: string, on: boolean) => {
    const current = draft.log?.extended_options ?? []
    const nextOptions = on ? Array.from(new Set([...current, option])) : current.filter(o => o !== option)
    patchLog({ extended_options: nextOptions.length ? nextOptions : undefined })
  }

  // patchLog/patchRunLogic edit the full section, promoting the legacy single-field
  // values (log_level / iterations / auto_transactions) into the section and clearing
  // them — so a section override always carries every field (section-replace safe) and
  // the legacy shim is never double-emitted alongside it.
  const patchLog = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['log']>>) => {
    const base = { level: draft.log?.level ?? (draft.log_level || undefined) }
    patch({ log: { ...base, ...draft.log, ...next }, log_level: '' })
  }

  const patchRunLogic = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['run_logic']>>) => {
    const base = {
      iterations: draft.run_logic?.iterations ?? draft.iterations,
      auto_transactions: draft.run_logic?.auto_transactions ?? (draft.auto_transactions || undefined),
    }
    patch({ run_logic: { ...base, ...draft.run_logic, ...next }, iterations: undefined, auto_transactions: '' })
  }

  const patchBrowserConfig = (next: Partial<NonNullable<ScenarioRuntimeSettingsDraft['browser_config']>>) => {
    const base = {
      device_profile: draft.browser_config?.device_profile ?? draft.browser?.device_profile,
      orientation: draft.browser_config?.orientation ?? draft.browser?.orientation,
      touch_mode: draft.browser_config?.touch_mode ?? draft.browser?.touch_mode,
      headless: draft.browser_config?.headless ?? draft.browser?.headless,
    }
    patch({ browser_config: { ...base, ...draft.browser_config, ...next }, browser: undefined })
  }

  const browserBoolValue = (a?: boolean, b?: boolean): string => {
    const value = a ?? b
    return value === undefined ? '' : value ? 'true' : 'false'
  }

  const clearField = (field: RuntimeFieldKey) => {
    switch (field) {
      case 'iterations': {
        // fieldHasOverride reports an override when the value lives in the
        // promoted run_logic section, so clearing must strip both the legacy
        // top-level field AND the section copies; drop run_logic only when it
        // becomes fully empty so unrelated section fields are preserved.
        const rl = draft.run_logic ? { ...draft.run_logic, iterations: undefined, global_iterations: undefined } : undefined
        patch({ iterations: undefined, run_logic: rl && Object.values(rl).some(v => v !== undefined) ? rl : undefined })
        break
      }
      case 'pacing':
        patch({ pacing: undefined })
        break
      case 'think_time':
        patch({ think_time: undefined })
        break
      case 'browser':
        patch({ browser: undefined })
        break
      case 'log_level': {
        const lg = draft.log ? { ...draft.log, level: undefined } : undefined
        patch({ log_level: '', log: lg && Object.values(lg).some(v => v !== undefined) ? lg : undefined })
        break
      }
      case 'auto_transactions': {
        const rl = draft.run_logic ? { ...draft.run_logic, auto_transactions: undefined } : undefined
        patch({ auto_transactions: '', run_logic: rl && Object.values(rl).some(v => v !== undefined) ? rl : undefined })
        break
      }
      case 'error_handling':
        patch({ continue_on_error: false, snapshot_on_error: false, max_retries: undefined, retry_delay_ms: undefined, retry_backoff: undefined })
        break
      case 'log':
        patch({ log: undefined, log_level: '' })
        break
      case 'run_logic':
        patch({ run_logic: undefined, iterations: undefined, auto_transactions: '' })
        break
      case 'http':
        patch({ http: undefined })
        break
      case 'web_runtime':
        patch({ web_runtime: undefined })
        break
      case 'browser_config':
        patch({ browser_config: undefined, browser: undefined })
        break
    }
  }

  const fieldHasOverride = (field: RuntimeFieldKey): boolean => {
    const o = groupOverride
    if (!o) return false
    switch (field) {
      case 'iterations':
        // Per-VUser iterations only (legacy top-level or the promoted run_logic
        // section). global_iterations is a distinct fleet-wide shared cap shown via
        // the Run Logic row, not conflated into this per-VUser Iterations row.
        return o.iterations !== undefined || o.run_logic?.iterations !== undefined
      case 'pacing':
        return Boolean(o.pacing?.type)
      case 'think_time':
        return Boolean(o.think_time)
      case 'browser':
        return Boolean(o.browser)
      case 'log_level':
        return Boolean(o.log_level) || Boolean(o.log?.level)
      case 'auto_transactions':
        return Boolean(o.auto_transactions) || Boolean(o.run_logic?.auto_transactions)
      case 'error_handling':
        return Boolean(o.error_handling)
      case 'log':
        return Boolean(o.log)
      case 'run_logic':
        return Boolean(o.run_logic)
      case 'http':
        return Boolean(o.http)
      case 'web_runtime':
        return Boolean(o.web_runtime)
      case 'browser_config':
        return Boolean(o.browser_config)
    }
  }

  const toggleTarget = (target: number) => {
    setApplyTargets(prev => (prev.includes(target) ? prev.filter(t => t !== target) : [...prev, target]))
  }

  const applyNow = () => {
    if (applyTargets.length > 0) {
      onApplyToGroups(index, applyTargets)
    }
    setApplyOpen(false)
    setApplyTargets([])
  }

  const pacingType = draft.pacing?.type || ''

  // Tab set: universal tabs always show; HTTP Transport (browser device folds in here)
  // and Web/HTTP are gated by protocol applicability and hidden when not applicable.
  const tabs: Array<{ id: RuntimeTabId; label: string; show: boolean }> = [
    { id: 'summary', label: 'Summary', show: true },
    { id: 'think_time', label: 'Think Time', show: true },
    { id: 'pacing', label: 'Iteration Pacing', show: true },
    { id: 'run_logic', label: 'Run Logic', show: true },
    { id: 'error_handling', label: 'Error Handling', show: true },
    { id: 'http_transport', label: 'HTTP Transport', show: httpApplicable || browserApplicable },
    { id: 'web_http', label: 'Web/HTTP', show: webRuntimeApplicable },
    { id: 'logging', label: 'Logging', show: true },
  ]
  const visibleTabs = tabs.filter(tab => tab.show)
  // Clamp the active tab to a visible one so switching focus to a group that hides the
  // current tab (e.g. HTTP Transport on a non-HTTP group) falls back to Summary.
  const activeTab: RuntimeTabId = visibleTabs.some(tab => tab.id === rawActiveTab) ? rawActiveTab : 'summary'

  // HTTP Transport nested sub-tabs; Browser emulation only for browser groups.
  const httpSubTabs: Array<{ id: HttpSubTabId; label: string; show: boolean }> = [
    { id: 'timeouts', label: 'Timeouts', show: true },
    { id: 'connection', label: 'Connection', show: true },
    { id: 'tls', label: 'TLS / SSL', show: true },
    { id: 'proxy', label: 'Proxy', show: true },
    { id: 'authentication', label: 'Authentication', show: true },
    { id: 'browser_emulation', label: 'Browser emulation', show: browserApplicable },
  ]
  const visibleHttpSubTabs = httpSubTabs.filter(st => st.show)
  const httpSubTab: HttpSubTabId = visibleHttpSubTabs.some(st => st.id === rawHttpSubTab) ? rawHttpSubTab : 'timeouts'

  const patchProxy = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['proxy']>>) => {
    patchHttp({ proxy: { ...draft.http?.proxy, ...next } })
  }
  const patchTls = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['tls']>>) => {
    patchHttp({ tls: { ...draft.http?.tls, ...next } })
  }
  const patchAuth = (next: Partial<NonNullable<NonNullable<ScenarioRuntimeSettingsDraft['http']>['auth']>>) => {
    patchHttp({ auth: { ...draft.http?.auth, ...next } })
  }

  return (
    <div className="scenario-subpanel">
      <div className="scenario-assignment-header">
        <div>
          <h4>Per-group runtime overrides</h4>
          <p>Set runtime for one group; unset fields inherit the script default. Use “Apply to groups” to copy a group’s settings to the others.</p>
        </div>
      </div>

      <div className="scenario-browser-grid">
        <label>
          <span>Focus group</span>
          <select
            className="settings-input"
            value={index}
            onChange={event => onFocusChange(Number(event.target.value))}
            aria-label="Focus group"
          >
            {groups.map((group, groupIndex) => (
              <option key={`${group.scriptId}-${groupIndex}`} value={groupIndex}>
                {groupLabel(group, groupIndex)}
                {group.protocol ? ` · ${group.protocol}` : ''}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="scenario-run-settings-tabs runtime-override-tabs" role="tablist" aria-label="Per-group runtime sections">
        {visibleTabs.map(tab => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`runtime-tab-${tab.id}`}
            aria-selected={activeTab === tab.id}
            aria-controls={`runtime-tab-panel-${tab.id}`}
            className={`scenario-run-settings-tab ${activeTab === tab.id ? 'active' : ''}`}
            onClick={() => setRawActiveTab(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      <div
        className="scenario-run-settings-tab-panel runtime-override-tab-panel"
        role="tabpanel"
        id={`runtime-tab-panel-${activeTab}`}
        aria-labelledby={`runtime-tab-${activeTab}`}
      >
        {activeTab === 'summary' && (
          <table className="runtime-override-summary">
            <thead>
              <tr>
                <th>Setting</th>
                <th>Effective value</th>
                <th>Source</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {SUMMARY_FIELDS.map(field => {
                const applicable = isRuntimeFieldApplicable(field, focusGroup.protocol)
                if (!applicable) {
                  return (
                    <tr key={field} className="runtime-override-row not-applicable">
                      <td>{FIELD_LABELS[field]}</td>
                      <td colSpan={3}><span className="runtime-source-badge muted">Not applicable for {focusGroup.protocol || 'this'} protocol</span></td>
                    </tr>
                  )
                }
                const effective = formatEffectiveRuntimeField(field, groupOverride, scenarioDefault, scriptDefault, engineDefault)
                return (
                  <tr key={field} className="runtime-override-row">
                    <td>{FIELD_LABELS[field]}</td>
                    <td>{effective.value}</td>
                    <td><span className={`runtime-source-badge source-${effective.source}`}>{RUNTIME_FIELD_SOURCE_LABEL[effective.source]}</span></td>
                    <td>
                      {fieldHasOverride(field) && (
                        <button type="button" className="link-button" onClick={() => clearField(field)} aria-label={`Clear ${FIELD_LABELS[field]} override`}>
                          Clear
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}

        {activeTab === 'think_time' && (
          <>
            <div className="scenario-browser-grid">
              <label>
                <span>Distribution</span>
                <select
                  className="settings-input"
                  value={draft.think_time?.ignore ? '' : (draft.think_time?.type || '')}
                  disabled={Boolean(draft.think_time?.ignore)}
                  onChange={event => {
                    const type = event.target.value
                    if (!type) {
                      patch({ think_time: undefined })
                      return
                    }
                    patchThinkTime({ type })
                  }}
                >
                  <option value="">Inherit</option>
                  <option value="fixed">Fixed</option>
                  <option value="gaussian">Gaussian</option>
                  <option value="uniform">Uniform</option>
                </select>
              </label>
              <label>
                <span>Mean (ms)</span>
                <input className="settings-input" type="number" min={0} value={draft.think_time?.mean_ms ?? ''} disabled={Boolean(draft.think_time?.ignore)} onChange={event => patchThinkTime({ mean_ms: event.target.value === '' ? (inheritedThink.mean_ms ?? 0) : Number(event.target.value) })} placeholder={inh(inheritedThink.mean_ms)} />
              </label>
              {draft.think_time?.type === 'gaussian' && (
                <label>
                  <span>Std dev (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.think_time?.std_ms ?? ''} onChange={event => patchThinkTime({ std_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.std_ms)} />
                </label>
              )}
              {(draft.think_time?.type === 'uniform' || draft.think_time?.type === 'gaussian') && (
                <>
                  <label>
                    <span>Min (ms)</span>
                    <input className="settings-input" type="number" min={0} value={draft.think_time?.min_ms ?? ''} onChange={event => patchThinkTime({ min_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.min_ms)} />
                  </label>
                  <label>
                    <span>Max (ms)</span>
                    <input className="settings-input" type="number" min={0} value={draft.think_time?.max_ms ?? ''} onChange={event => patchThinkTime({ max_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.max_ms)} />
                  </label>
                </>
              )}
              <label>
                <span>Multiplier</span>
                <input className="settings-input" type="number" min={0} step="0.1" value={draft.think_time?.multiplier ?? ''} onChange={event => patchThinkTime({ multiplier: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.multiplier)} />
              </label>
              <label>
                <span>Limit (ms)</span>
                <input className="settings-input" type="number" min={0} value={draft.think_time?.limit_ms ?? ''} onChange={event => patchThinkTime({ limit_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedThink.limit_ms)} />
              </label>
            </div>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${draft.think_time?.ignore ? 'selected' : ''}`}>
                <input
                  type="checkbox"
                  checked={Boolean(draft.think_time?.ignore)}
                  onChange={event => patchThinkTime({ ignore: event.target.checked || undefined })}
                />
                <div><strong>Ignore think time</strong><span>Bypass scripted think times for this group.</span></div>
              </label>
            </div>
          </>
        )}

        {activeTab === 'pacing' && (
          <div className="scenario-browser-grid">
            <label>
              <span>Pacing</span>
              <select
                className="settings-input"
                value={pacingType}
                onChange={event => {
                  const type = event.target.value as NonNullable<ScenarioRuntimeSettingsDraft['pacing']>['type'] | ''
                  patch({ pacing: type ? { type } : undefined })
                }}
              >
                <option value="">Inherit</option>
                <option value="none">None</option>
                <option value="fixed_delay">Fixed delay</option>
                <option value="random_delay">Random delay</option>
                <option value="fixed_interval">Fixed interval</option>
                <option value="random_interval">Random interval</option>
                <option value="tps">Target TPS</option>
              </select>
            </label>
            {pacingType === 'fixed_delay' && (
              <label>
                <span>Pacing delay (ms)</span>
                <input className="settings-input" type="number" min={0} value={draft.pacing?.delay_ms ?? ''} onChange={event => patchPacing({ delay_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.delay_ms, '0')} />
              </label>
            )}
            {pacingType === 'random_delay' && (
              <>
                <label>
                  <span>Min delay (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.pacing?.min_delay_ms ?? ''} onChange={event => patchPacing({ min_delay_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.min_delay_ms, '0')} />
                </label>
                <label>
                  <span>Max delay (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.pacing?.max_delay_ms ?? ''} onChange={event => patchPacing({ max_delay_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.max_delay_ms, '0')} />
                </label>
              </>
            )}
            {pacingType === 'fixed_interval' && (
              <label>
                <span>Pacing interval (ms)</span>
                <input className="settings-input" type="number" min={0} value={draft.pacing?.interval_ms ?? ''} onChange={event => patchPacing({ interval_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.interval_ms, '0')} />
              </label>
            )}
            {pacingType === 'random_interval' && (
              <>
                <label>
                  <span>Min interval (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.pacing?.min_interval_ms ?? ''} onChange={event => patchPacing({ min_interval_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.min_interval_ms, '0')} />
                </label>
                <label>
                  <span>Max interval (ms)</span>
                  <input className="settings-input" type="number" min={0} value={draft.pacing?.max_interval_ms ?? ''} onChange={event => patchPacing({ max_interval_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.max_interval_ms, '0')} />
                </label>
              </>
            )}
            {pacingType === 'tps' && (
              <label>
                <span>Target TPS</span>
                <input className="settings-input" type="number" min={0} value={draft.pacing?.tps ?? ''} onChange={event => patchPacing({ tps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedPacing.tps, '0')} />
              </label>
            )}
          </div>
        )}

        {activeTab === 'run_logic' && (
          <div className="scenario-browser-grid">
            <label>
              <span>Iterations</span>
              <input
                className="settings-input"
                type="number"
                min={0}
                value={draft.run_logic?.iterations ?? draft.iterations ?? ''}
                onChange={event => patchRunLogic({ iterations: event.target.value === '' ? undefined : Number(event.target.value) })}
                placeholder={inh(inheritedIterations, 'Duration-driven')}
              />
            </label>
            <label>
              <span>Auto-transactions</span>
              <select className="settings-input" value={draft.run_logic?.auto_transactions ?? draft.auto_transactions ?? ''} onChange={event => patchRunLogic({ auto_transactions: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="none">None</option>
                <option value="per_action">Per action</option>
                <option value="per_iteration">Per iteration</option>
              </select>
            </label>
            <label>
              <span>Workload model</span>
              <select className="settings-input" value={draft.run_logic?.workload_model || ''} onChange={event => patchRunLogic({ workload_model: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="closed">Closed</option>
                <option value="open">Open</option>
              </select>
            </label>
            <label>
              <span>Block scheduling</span>
              <select className="settings-input" value={draft.run_logic?.block_scheduling || ''} onChange={event => patchRunLogic({ block_scheduling: event.target.value || undefined })}>
                <option value="">Inherit</option>
                <option value="sequential">Sequential</option>
                <option value="random">Random</option>
                <option value="percentage">Percentage</option>
              </select>
            </label>
            <label>
              <span>Global iterations</span>
              <input className="settings-input" type="number" min={0} value={draft.run_logic?.global_iterations ?? ''} onChange={event => patchRunLogic({ global_iterations: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedRunLogic.global_iterations)} />
            </label>
            <label>
              <span>Max transactions</span>
              <input className="settings-input" type="number" min={0} value={draft.run_logic?.max_transactions ?? ''} onChange={event => patchRunLogic({ max_transactions: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedRunLogic.max_transactions)} />
            </label>
            <label>
              <span>Action sequence</span>
              <input
                className="settings-input"
                type="text"
                value={draft.run_logic?.action_sequence?.join(', ') || ''}
                onChange={event => {
                  const sequence = event.target.value.split(',').map(value => value.trim()).filter(Boolean)
                  patchRunLogic({ action_sequence: sequence.length ? sequence : undefined })
                }}
                placeholder="login, browse, checkout"
              />
            </label>
          </div>
        )}

        {activeTab === 'error_handling' && (
          <>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${draft.continue_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={draft.continue_on_error} onChange={event => patch({ continue_on_error: event.target.checked })} />
                <div><strong>Continue on error</strong><span>Keep iterating after a failed step.</span></div>
              </label>
              <label className={`scenario-option-card ${draft.snapshot_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={draft.snapshot_on_error} onChange={event => patch({ snapshot_on_error: event.target.checked })} />
                <div><strong>Snapshot on error</strong><span>Capture diagnostics when a step fails.</span></div>
              </label>
            </div>
            <div className="scenario-browser-grid">
              <label>
                <span>Max retries</span>
                <input
                  className="settings-input"
                  type="number"
                  min={0}
                  value={draft.max_retries ?? ''}
                  onChange={event => patch({ max_retries: event.target.value === '' ? undefined : Number(event.target.value) })}
                  placeholder={inh(inheritedError.max_retries, '0')}
                />
              </label>
              <label>
                <span>Retry delay (ms)</span>
                <input
                  className="settings-input"
                  type="number"
                  min={0}
                  value={draft.retry_delay_ms ?? ''}
                  onChange={event => patch({ retry_delay_ms: event.target.value === '' ? undefined : Number(event.target.value) })}
                  placeholder={inh(inheritedError.retry_delay_ms)}
                />
              </label>
              <label>
                <span>Retry backoff</span>
                <select
                  className="settings-input"
                  value={draft.retry_backoff || ''}
                  onChange={event => patch({ retry_backoff: event.target.value || undefined })}
                >
                  <option value="">Inherit</option>
                  <option value="constant">Constant</option>
                  <option value="linear">Linear</option>
                  <option value="exponential">Exponential</option>
                </select>
              </label>
            </div>
          </>
        )}

        {activeTab === 'http_transport' && (
          <>
            <div className="http-transport-tabs" role="tablist" aria-label="HTTP transport sections">
              {visibleHttpSubTabs.map(st => (
                <button
                  key={st.id}
                  type="button"
                  role="tab"
                  aria-selected={httpSubTab === st.id}
                  className={`http-transport-tab ${httpSubTab === st.id ? 'active' : ''}`}
                  onClick={() => setRawHttpSubTab(st.id)}
                >
                  {st.label}
                </button>
              ))}
            </div>

            <div className="http-transport-tab-panel">
              {httpSubTab === 'timeouts' && (
                <div className="scenario-browser-grid">
                  <label><span>Connect timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.connect_timeout_ms ?? ''} onChange={event => patchHttp({ connect_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.connect_timeout_ms)} /></label>
                  <label><span>Request timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.request_timeout_ms ?? ''} onChange={event => patchHttp({ request_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.request_timeout_ms)} /></label>
                  <label><span>TLS handshake timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.tls_timeout_ms ?? ''} onChange={event => patchHttp({ tls_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.tls_timeout_ms)} /></label>
                  <label><span>Idle timeout (ms)</span><input className="settings-input" type="number" min={0} value={draft.http?.idle_timeout_ms ?? ''} onChange={event => patchHttp({ idle_timeout_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.idle_timeout_ms)} /></label>
                </div>
              )}

              {httpSubTab === 'connection' && (
                <div className="scenario-browser-grid">
                  <label>
                    <span>HTTP version</span>
                    <select className="settings-input" value={draft.http?.version || ''} onChange={event => patchHttp({ version: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      <option value="1.0">1.0</option>
                      <option value="1.1">1.1</option>
                      <option value="2">2</option>
                    </select>
                  </label>
                  <label><span>Max connections / host</span><input className="settings-input" type="number" min={0} value={draft.http?.max_conns_per_host ?? ''} onChange={event => patchHttp({ max_conns_per_host: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.max_conns_per_host)} /></label>
                  <label><span>Max redirects</span><input className="settings-input" type="number" min={0} value={draft.http?.max_redirects ?? ''} onChange={event => patchHttp({ max_redirects: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedHttp.max_redirects)} /></label>
                  <label>
                    <span>Keep-alive</span>
                    <select className="settings-input" value={browserBoolValue(draft.http?.keep_alive)} onChange={event => patchHttp({ keep_alive: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                      <option value="">Inherit</option>
                      <option value="true">Enabled</option>
                      <option value="false">Disabled</option>
                    </select>
                  </label>
                  <label><span>User agent</span><input className="settings-input" type="text" value={draft.http?.user_agent || ''} onChange={event => patchHttp({ user_agent: event.target.value || undefined })} placeholder={inh(inheritedHttp.user_agent)} /></label>
                  <label><span>Accept-Encoding</span><input className="settings-input" type="text" value={draft.http?.accept_encoding || ''} onChange={event => patchHttp({ accept_encoding: event.target.value || undefined })} placeholder={inh(inheritedHttp.accept_encoding)} /></label>
                  <label><span>Accept-Language</span><input className="settings-input" type="text" value={draft.http?.accept_language || ''} onChange={event => patchHttp({ accept_language: event.target.value || undefined })} placeholder={inh(inheritedHttp.accept_language)} /></label>
                </div>
              )}

              {httpSubTab === 'tls' && (
                <>
                  <div className="scenario-browser-grid">
                    <label>
                      <span>TLS min version</span>
                      <select className="settings-input" value={draft.http?.tls?.min_version || ''} onChange={event => patchTls({ min_version: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="1.0">1.0</option>
                        <option value="1.1">1.1</option>
                        <option value="1.2">1.2</option>
                        <option value="1.3">1.3</option>
                      </select>
                    </label>
                    <label><span>Client cert file</span><input className="settings-input" type="text" value={draft.http?.tls?.client_cert_file || ''} onChange={event => patchTls({ client_cert_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.client_cert_file)} /></label>
                    <label><span>Client key file</span><input className="settings-input" type="text" value={draft.http?.tls?.client_key_file || ''} onChange={event => patchTls({ client_key_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.client_key_file)} /></label>
                    <label><span>CA cert file</span><input className="settings-input" type="text" value={draft.http?.tls?.ca_cert_file || ''} onChange={event => patchTls({ ca_cert_file: event.target.value || undefined })} placeholder={inh(inheritedHttp.tls?.ca_cert_file)} /></label>
                  </div>
                  <div className="scenario-option-grid">
                    <label className={`scenario-option-card ${draft.http?.tls?.insecure_skip_verify ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.http?.tls?.insecure_skip_verify)} onChange={event => patchTls({ insecure_skip_verify: event.target.checked || undefined })} />
                      <div><strong>Skip TLS verification</strong><span>Do not validate the server certificate (test environments only).</span></div>
                    </label>
                  </div>
                </>
              )}

              {httpSubTab === 'proxy' && (
                <div className="scenario-browser-grid">
                  <label>
                    <span>Proxy mode</span>
                    <select className="settings-input" value={draft.http?.proxy_mode || ''} onChange={event => patchHttp({ proxy_mode: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      <option value="none">None</option>
                      <option value="system">System</option>
                      <option value="custom">Custom</option>
                    </select>
                  </label>
                  <label><span>Proxy URL</span><input className="settings-input" type="text" value={draft.http?.proxy?.url || ''} onChange={event => patchProxy({ url: event.target.value || undefined })} placeholder={inh(inheritedHttp.proxy?.url, 'http://proxy:8080')} /></label>
                  <label><span>Proxy username</span><input className="settings-input" type="text" autoComplete="off" value={draft.http?.proxy?.username || ''} onChange={event => patchProxy({ username: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label><span>Proxy password</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.proxy?.password || ''} onChange={event => patchProxy({ password: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label>
                    <span>No-proxy hosts</span>
                    <input className="settings-input" type="text" value={draft.http?.proxy?.no_proxy?.join(', ') || ''} onChange={event => {
                      const hosts = event.target.value.split(',').map(value => value.trim()).filter(Boolean)
                      patchProxy({ no_proxy: hosts.length ? hosts : undefined })
                    }} placeholder={inh(inheritedHttp.proxy?.no_proxy?.join(', '), 'localhost, 127.0.0.1')} />
                  </label>
                </div>
              )}

              {httpSubTab === 'authentication' && (
                <div className="scenario-browser-grid">
                  <label>
                    <span>Auth type</span>
                    <select className="settings-input" value={draft.http?.auth?.type || ''} onChange={event => patchAuth({ type: event.target.value || undefined })}>
                      <option value="">Inherit</option>
                      <option value="basic">Basic</option>
                      <option value="bearer">Bearer</option>
                      <option value="ntlm">NTLM</option>
                      <option value="digest">Digest</option>
                    </select>
                  </label>
                  <label><span>Auth username</span><input className="settings-input" type="text" value={draft.http?.auth?.username || ''} onChange={event => patchAuth({ username: event.target.value || undefined })} placeholder={inh(inheritedHttp.auth?.username)} /></label>
                  <label><span>Auth password</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.auth?.password || ''} onChange={event => patchAuth({ password: event.target.value || undefined })} placeholder="Inherit" /></label>
                  <label><span>Auth token</span><input className="settings-input" type="password" autoComplete="off" value={draft.http?.auth?.token || ''} onChange={event => patchAuth({ token: event.target.value || undefined })} placeholder="Inherit" /></label>
                </div>
              )}

              {httpSubTab === 'browser_emulation' && browserApplicable && (
                <>
                  <div className="scenario-browser-grid">
                    <label>
                      <span>Device profile</span>
                      <select className="settings-input" value={draft.browser_config?.device_profile ?? draft.browser?.device_profile ?? ''} onChange={event => patchBrowserConfig({ device_profile: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="desktop_1080p">desktop_1080p</option>
                        <option value="galaxy_s24">galaxy_s24</option>
                        <option value="pixel_8">pixel_8</option>
                        <option value="ipad_air">ipad_air (WebKit)</option>
                        <option value="iphone_15_pro">iphone_15_pro (WebKit)</option>
                        <option value="iphone_se">iphone_se (WebKit)</option>
                      </select>
                    </label>
                    <label>
                      <span>Orientation</span>
                      <select className="settings-input" value={draft.browser_config?.orientation ?? draft.browser?.orientation ?? ''} onChange={event => patchBrowserConfig({ orientation: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="portrait">Portrait</option>
                        <option value="landscape">Landscape</option>
                      </select>
                    </label>
                    <label>
                      <span>Headless</span>
                      <select className="settings-input" value={browserBoolValue(draft.browser_config?.headless, draft.browser?.headless)} onChange={event => patchBrowserConfig({ headless: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                        <option value="">Inherit</option>
                        <option value="true">Headless</option>
                        <option value="false">Headed</option>
                      </select>
                    </label>
                    <label>
                      <span>Touch mode</span>
                      <select className="settings-input" value={browserBoolValue(draft.browser_config?.touch_mode, draft.browser?.touch_mode)} onChange={event => patchBrowserConfig({ touch_mode: event.target.value === '' ? undefined : event.target.value === 'true' })}>
                        <option value="">Inherit</option>
                        <option value="true">Enabled</option>
                        <option value="false">Disabled</option>
                      </select>
                    </label>
                    <label>
                      <span>Browser engine</span>
                      <select className="settings-input" value={draft.browser_config?.engine || ''} onChange={event => patchBrowserConfig({ engine: event.target.value || undefined })}>
                        <option value="">Inherit</option>
                        <option value="chromium">Chromium</option>
                        <option value="firefox">Firefox</option>
                        <option value="webkit">WebKit</option>
                      </select>
                    </label>
                    <label><span>Browser user agent</span><input className="settings-input" type="text" value={draft.browser_config?.user_agent || ''} onChange={event => patchBrowserConfig({ user_agent: event.target.value || undefined })} placeholder={inh(inheritedBrowser.user_agent)} /></label>
                    <label><span>Slow-mo (ms)</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.slow_mo_ms ?? ''} onChange={event => patchBrowserConfig({ slow_mo_ms: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder={inh(inheritedBrowser.slow_mo_ms)} /></label>
                    <label><span>Viewport width</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.viewport?.width ?? ''} onChange={event => patchBrowserConfig({ viewport: { ...draft.browser_config?.viewport, width: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder={inh(inheritedBrowser.viewport?.width)} /></label>
                    <label><span>Viewport height</span><input className="settings-input" type="number" min={0} value={draft.browser_config?.viewport?.height ?? ''} onChange={event => patchBrowserConfig({ viewport: { ...draft.browser_config?.viewport, height: event.target.value === '' ? undefined : Number(event.target.value) } })} placeholder={inh(inheritedBrowser.viewport?.height)} /></label>
                    <label><span>Capture screenshots</span><input className="settings-input" type="text" value={draft.browser_config?.capture_screenshots || ''} onChange={event => patchBrowserConfig({ capture_screenshots: event.target.value || undefined })} placeholder={inh(inheritedBrowser.capture_screenshots)} /></label>
                    <label><span>Download resources</span><input className="settings-input" type="text" value={draft.browser_config?.download_resources || ''} onChange={event => patchBrowserConfig({ download_resources: event.target.value || undefined })} placeholder={inh(inheritedBrowser.download_resources)} /></label>
                  </div>
                  <div className="scenario-option-grid">
                    <label className={`scenario-option-card ${draft.browser_config?.client_metrics ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.client_metrics)} onChange={event => patchBrowserConfig({ client_metrics: event.target.checked || undefined })} />
                      <div><strong>Client metrics</strong><span>Collect browser client-side metrics.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.cache_enabled ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.cache_enabled)} onChange={event => patchBrowserConfig({ cache_enabled: event.target.checked || undefined })} />
                      <div><strong>Cache enabled</strong><span>Allow the browser cache during replay.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.cache_content_urls ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.cache_content_urls)} onChange={event => patchBrowserConfig({ cache_content_urls: event.target.checked || undefined })} />
                      <div><strong>Cache content URLs</strong><span>Cache static content URLs.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.check_newer_versions ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.check_newer_versions)} onChange={event => patchBrowserConfig({ check_newer_versions: event.target.checked || undefined })} />
                      <div><strong>Check newer versions</strong><span>Check for newer resource versions.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.new_user_per_iteration ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.new_user_per_iteration)} onChange={event => patchBrowserConfig({ new_user_per_iteration: event.target.checked || undefined })} />
                      <div><strong>New user per iteration</strong><span>Start each iteration with a fresh browser profile.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.review_policy?.allow_reviewed_javascript ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.review_policy?.allow_reviewed_javascript)} onChange={event => patchBrowserConfig({ review_policy: { ...draft.browser_config?.review_policy, allow_reviewed_javascript: event.target.checked || undefined } })} />
                      <div><strong>Allow reviewed JavaScript</strong><span>Permit JavaScript steps that passed review.</span></div>
                    </label>
                    <label className={`scenario-option-card ${draft.browser_config?.review_policy?.allow_review_required ? 'selected' : ''}`}>
                      <input type="checkbox" checked={Boolean(draft.browser_config?.review_policy?.allow_review_required)} onChange={event => patchBrowserConfig({ review_policy: { ...draft.browser_config?.review_policy, allow_review_required: event.target.checked || undefined } })} />
                      <div><strong>Allow review-required</strong><span>Permit steps still pending review.</span></div>
                    </label>
                  </div>
                </>
              )}
            </div>
          </>
        )}

        {activeTab === 'web_http' && (
          <WebHttpTab web={draft.web_runtime} inheritedWeb={inheritedWeb} setWeb={next => patch({ web_runtime: next })} />
        )}

        {activeTab === 'logging' && (
          <>
            <div className="scenario-browser-grid">
              <label>
                <span>Log level</span>
                <select className="settings-input" value={draft.log?.level ?? draft.log_level ?? ''} onChange={event => patchLog({ level: event.target.value || undefined })}>
                  <option value="">Inherit</option>
                  <option value="disabled">Disabled</option>
                  <option value="standard">Standard</option>
                  <option value="extended">Extended</option>
                </select>
              </label>
            </div>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${draft.log?.on_error_only ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(draft.log?.on_error_only)} onChange={event => patchLog({ on_error_only: event.target.checked || undefined })} />
                <div><strong>Extended logging on error only</strong><span>Use extended logging only when a step fails.</span></div>
              </label>
              <label className={`scenario-option-card ${draft.log?.extended_options?.includes('parameter_substitution') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(draft.log?.extended_options?.includes('parameter_substitution'))} onChange={event => toggleLogOption('parameter_substitution', event.target.checked)} />
                <div><strong>Parameter substitution</strong><span>Log each parameter substitution.</span></div>
              </label>
              <label className={`scenario-option-card ${draft.log?.extended_options?.includes('data_returned') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(draft.log?.extended_options?.includes('data_returned'))} onChange={event => toggleLogOption('data_returned', event.target.checked)} />
                <div><strong>Data returned</strong><span>Log the data returned by the server.</span></div>
              </label>
              <label className={`scenario-option-card ${draft.log?.extended_options?.includes('advanced_trace') ? 'selected' : ''}`}>
                <input type="checkbox" checked={Boolean(draft.log?.extended_options?.includes('advanced_trace'))} onChange={event => toggleLogOption('advanced_trace', event.target.checked)} />
                <div><strong>Advanced trace</strong><span>Capture full advanced trace detail.</span></div>
              </label>
            </div>
          </>
        )}
      </div>

      <div className="runtime-apply-row">
        <button type="button" className="secondary-button" onClick={() => setApplyOpen(open => !open)} disabled={groups.length < 2}>
          Apply to groups…
        </button>
        {applyOpen && (
          <div className="runtime-apply-panel">
            <p>Copy {groupLabel(focusGroup, index)}’s effective runtime to:</p>
            {groups.map((group, groupIndex) => {
              if (groupIndex === index) return null
              return (
                <label key={`apply-${group.scriptId}-${groupIndex}`} className="runtime-apply-target">
                  <input type="checkbox" checked={applyTargets.includes(groupIndex)} onChange={() => toggleTarget(groupIndex)} />
                  <span>{groupLabel(group, groupIndex)}{group.protocol ? ` · ${group.protocol}` : ''}</span>
                </label>
              )
            })}
            <button type="button" className="primary-button" onClick={applyNow} disabled={applyTargets.length === 0}>
              Apply
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
