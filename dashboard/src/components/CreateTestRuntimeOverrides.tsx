import { ScenarioDraft } from '../types'

type RuntimeSettingsState = ScenarioDraft['runtimeSettings']

interface Props {
  runtimeSettings: RuntimeSettingsState
  onRuntimeSettingChange: <K extends keyof RuntimeSettingsState>(field: K, value: RuntimeSettingsState[K]) => void
}

export default function CreateTestRuntimeOverrides({ runtimeSettings, onRuntimeSettingChange }: Props) {
  const pacingType = runtimeSettings.pacing?.type || ''

  return (
    <div className="runtime-overrides">
      <h4>Runtime Settings Overrides</h4>
      <div className="overrides-grid">
        <label>
          <span>Iterations</span>
          <input
            type="number"
            value={runtimeSettings.iterations || ''}
            onChange={(event) => onRuntimeSettingChange('iterations', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="Script default"
          />
        </label>
        <label>
          <span>Pacing</span>
          <select
            value={pacingType}
            onChange={(event) => {
              const type = event.target.value
              if (!type) {
                onRuntimeSettingChange('pacing', undefined)
                return
              }
              if (type === 'none') {
                onRuntimeSettingChange('pacing', { type: 'none' })
                return
              }
              if (type === 'fixed_interval') {
                onRuntimeSettingChange('pacing', { type, interval_ms: runtimeSettings.pacing?.interval_ms ?? 0 })
                return
              }
              if (type === 'tps') {
                onRuntimeSettingChange('pacing', { type, tps: runtimeSettings.pacing?.tps ?? 0 })
                return
              }
              onRuntimeSettingChange('pacing', { type, delay_ms: runtimeSettings.pacing?.delay_ms ?? 0 })
            }}
          >
            <option value="">Script default</option>
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
              type="number"
              value={runtimeSettings.pacing?.delay_ms ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'fixed_delay',
                delay_ms: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'fixed_interval' && (
          <label>
            <span>Pacing interval (ms)</span>
            <input
              type="number"
              value={runtimeSettings.pacing?.interval_ms ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'fixed_interval',
                interval_ms: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        {pacingType === 'tps' && (
          <label>
            <span>Target TPS</span>
            <input
              type="number"
              value={runtimeSettings.pacing?.tps ?? ''}
              onChange={(event) => onRuntimeSettingChange('pacing', {
                type: 'tps',
                tps: event.target.value ? Number(event.target.value) : undefined,
              })}
              placeholder="0"
            />
          </label>
        )}
        <label>
          <span>Think Time Override (ms)</span>
          <input
            type="number"
            value={runtimeSettings.think_time?.ignore ? '' : (runtimeSettings.think_time?.mean_ms ?? '')}
            onChange={(event) => onRuntimeSettingChange('think_time', event.target.value
              ? { type: 'fixed', mean_ms: Number(event.target.value), ignore: false }
              : undefined)}
            placeholder="Script default"
            disabled={runtimeSettings.think_time?.ignore || false}
          />
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.think_time?.ignore || false}
            onChange={(event) => onRuntimeSettingChange('think_time', event.target.checked
              ? { type: 'fixed', mean_ms: runtimeSettings.think_time?.mean_ms ?? 0, ignore: true }
              : undefined)}
          />
          <span>Ignore think time</span>
        </label>
        <label>
          <span>Log Level</span>
          <select
            value={runtimeSettings.log_level}
            onChange={(event) => onRuntimeSettingChange('log_level', event.target.value)}
          >
            <option value="">Script default</option>
            <option value="disabled">Disabled</option>
            <option value="standard">Standard</option>
            <option value="extended">Extended</option>
          </select>
        </label>
        <label>
          <span>Auto-Transactions</span>
          <select
            value={runtimeSettings.auto_transactions}
            onChange={(event) => onRuntimeSettingChange('auto_transactions', event.target.value)}
          >
            <option value="">Script default</option>
            <option value="none">None</option>
            <option value="per_action">Per Action</option>
            <option value="per_iteration">Per Iteration</option>
          </select>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.continue_on_error}
            onChange={(event) => onRuntimeSettingChange('continue_on_error', event.target.checked)}
          />
          <span>Continue on error</span>
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={runtimeSettings.snapshot_on_error}
            onChange={(event) => onRuntimeSettingChange('snapshot_on_error', event.target.checked)}
          />
          <span>Snapshot on error</span>
        </label>
      </div>
    </div>
  )
}