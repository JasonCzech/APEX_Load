import { ScenarioDraft } from '../types'

type RuntimeSettingsState = ScenarioDraft['runtimeSettings']

interface Props {
  runtimeSettings: RuntimeSettingsState
  onRuntimeSettingChange: <K extends keyof RuntimeSettingsState>(field: K, value: RuntimeSettingsState[K]) => void
}

export default function CreateTestRuntimeOverrides({ runtimeSettings, onRuntimeSettingChange }: Props) {
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
            checked={runtimeSettings.snapshot_on_error}
            onChange={(event) => onRuntimeSettingChange('snapshot_on_error', event.target.checked)}
          />
          <span>Snapshot on error</span>
        </label>
      </div>
    </div>
  )
}