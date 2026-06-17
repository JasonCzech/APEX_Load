import { useState } from 'react'

import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import {
  EMPTY_RUNTIME_DRAFT,
  formatEffectiveRuntimeField,
  isRuntimeDraftEmpty,
  isRuntimeFieldApplicable,
  overrideFromRuntimeDraft,
  RUNTIME_FIELD_SOURCE_LABEL,
  type RuntimeFieldKey,
} from './runtimeSettingsModel'

export interface RuntimeEditorGroup {
  name: string
  scriptId: string
  protocol?: string
}

export interface RuntimeSettingsGroupEditorProps {
  groups: RuntimeEditorGroup[]
  focusedIndex: number
  onFocusChange: (index: number) => void
  /** Scenario-wide defaults (the baseline every group inherits). */
  scenarioDefault?: RuntimeSettingsOverride
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
}

const SUMMARY_FIELDS: RuntimeFieldKey[] = [
  'iterations',
  'pacing',
  'think_time',
  'log_level',
  'auto_transactions',
  'error_handling',
  'browser',
]

function groupLabel(group: RuntimeEditorGroup, index: number): string {
  return group.name || `Group ${index + 1}`
}

export default function RuntimeSettingsGroupEditor({
  groups,
  focusedIndex,
  onFocusChange,
  scenarioDefault,
  overrides,
  onChange,
  onApplyToGroups,
}: RuntimeSettingsGroupEditorProps) {
  const [applyOpen, setApplyOpen] = useState(false)
  const [applyTargets, setApplyTargets] = useState<number[]>([])

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
  const browserApplicable = isRuntimeFieldApplicable('browser', focusGroup.protocol)

  const patch = (next: Partial<ScenarioRuntimeSettingsDraft>) => {
    const merged: ScenarioRuntimeSettingsDraft = { ...EMPTY_RUNTIME_DRAFT, ...draft, ...next }
    onChange(index, isRuntimeDraftEmpty(merged) ? undefined : merged)
  }

  const clearField = (field: RuntimeFieldKey) => {
    switch (field) {
      case 'iterations':
        patch({ iterations: undefined })
        break
      case 'pacing':
        patch({ pacing: undefined })
        break
      case 'think_time':
        patch({ think_time: undefined })
        break
      case 'browser':
        patch({ browser: undefined })
        break
      case 'log_level':
        patch({ log_level: '' })
        break
      case 'auto_transactions':
        patch({ auto_transactions: '' })
        break
      case 'error_handling':
        patch({ continue_on_error: false, snapshot_on_error: false })
        break
    }
  }

  const fieldHasOverride = (field: RuntimeFieldKey): boolean => {
    const o = groupOverride
    if (!o) return false
    switch (field) {
      case 'iterations':
        return o.iterations !== undefined
      case 'pacing':
        return Boolean(o.pacing?.type)
      case 'think_time':
        return Boolean(o.think_time)
      case 'browser':
        return Boolean(o.browser)
      case 'log_level':
        return Boolean(o.log_level)
      case 'auto_transactions':
        return Boolean(o.auto_transactions)
      case 'error_handling':
        return Boolean(o.error_handling)
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

  return (
    <div className="scenario-subpanel">
      <div className="scenario-assignment-header">
        <div>
          <h4>Per-group runtime overrides</h4>
          <p>Override the scenario defaults for one group. Unset fields inherit the scenario default, then the script default.</p>
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
            const effective = formatEffectiveRuntimeField(field, groupOverride, scenarioDefault)
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

      <div className="scenario-browser-grid">
        <label>
          <span>Iterations</span>
          <input
            className="settings-input"
            type="number"
            min={0}
            value={draft.iterations ?? ''}
            onChange={event => patch({ iterations: event.target.value === '' ? undefined : Number(event.target.value) })}
            placeholder="Inherit"
          />
        </label>
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
            <option value="fixed_interval">Fixed interval</option>
            <option value="tps">Target TPS</option>
          </select>
        </label>
        {pacingType === 'fixed_delay' && (
          <label>
            <span>Pacing delay (ms)</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              value={draft.pacing?.delay_ms ?? ''}
              onChange={event => patch({ pacing: { type: 'fixed_delay', delay_ms: event.target.value === '' ? undefined : Number(event.target.value) } })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'fixed_interval' && (
          <label>
            <span>Pacing interval (ms)</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              value={draft.pacing?.interval_ms ?? ''}
              onChange={event => patch({ pacing: { type: 'fixed_interval', interval_ms: event.target.value === '' ? undefined : Number(event.target.value) } })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'tps' && (
          <label>
            <span>Target TPS</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              value={draft.pacing?.tps ?? ''}
              onChange={event => patch({ pacing: { type: 'tps', tps: event.target.value === '' ? undefined : Number(event.target.value) } })}
              placeholder="0"
            />
          </label>
        )}
        <label>
          <span>Think time (ms)</span>
          <input
            className="settings-input"
            type="number"
            min={0}
            value={draft.think_time?.ignore ? '' : (draft.think_time?.mean_ms ?? '')}
            disabled={Boolean(draft.think_time?.ignore)}
            onChange={event => patch({ think_time: event.target.value === '' ? undefined : { type: 'fixed', mean_ms: Number(event.target.value), ignore: false } })}
            placeholder="Inherit"
          />
        </label>
        <label>
          <span>Log level</span>
          <select className="settings-input" value={draft.log_level} onChange={event => patch({ log_level: event.target.value })}>
            <option value="">Inherit</option>
            <option value="disabled">Disabled</option>
            <option value="standard">Standard</option>
            <option value="extended">Extended</option>
          </select>
        </label>
        <label>
          <span>Auto-transactions</span>
          <select className="settings-input" value={draft.auto_transactions} onChange={event => patch({ auto_transactions: event.target.value })}>
            <option value="">Inherit</option>
            <option value="none">None</option>
            <option value="per_action">Per action</option>
            <option value="per_iteration">Per iteration</option>
          </select>
        </label>
      </div>

      <div className="scenario-option-grid">
        <label className={`scenario-option-card ${draft.think_time?.ignore ? 'selected' : ''}`}>
          <input
            type="checkbox"
            checked={Boolean(draft.think_time?.ignore)}
            onChange={event => patch({ think_time: event.target.checked ? { type: 'fixed', mean_ms: draft.think_time?.mean_ms ?? 0, ignore: true } : undefined })}
          />
          <div><strong>Ignore think time</strong><span>Bypass scripted think times for this group.</span></div>
        </label>
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
            placeholder="Inherit"
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
            placeholder="Inherit"
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

      {browserApplicable && (
        <div className="scenario-browser-grid">
          <label>
            <span>Device profile</span>
            <select
              className="settings-input"
              value={draft.browser?.device_profile || ''}
              onChange={event => patch({ browser: { ...draft.browser, device_profile: event.target.value || undefined } })}
            >
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
            <select
              className="settings-input"
              value={draft.browser?.orientation || ''}
              onChange={event => patch({ browser: { ...draft.browser, orientation: event.target.value || undefined } })}
            >
              <option value="">Inherit</option>
              <option value="portrait">Portrait</option>
              <option value="landscape">Landscape</option>
            </select>
          </label>
        </div>
      )}

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
