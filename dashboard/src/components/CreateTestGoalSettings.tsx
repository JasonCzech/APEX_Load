import { GoalConfig } from '../types'

interface Props {
  goalConfig: GoalConfig
  onGoalConfigChange: <K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) => void
}

export default function CreateTestGoalSettings({ goalConfig, onGoalConfigChange }: Props) {
  return (
    <div className="goal-settings">
      <div className="goal-settings-header">
        <h4>Goal Settings</h4>
        <p>Define the target and guardrails. The script groups below still provide the starting seed load and script mix for the scenario.</p>
      </div>
      <div className="goal-settings-grid">
        <label>
          <span>Target Avg TPS</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.target_average_tps || ''}
            onChange={(event) => onGoalConfigChange('target_average_tps', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="e.g. 300"
          />
        </label>
        <label>
          <span>P95 Latency Ceiling (ms)</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.p95_latency_ms || ''}
            onChange={(event) => onGoalConfigChange('p95_latency_ms', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="e.g. 800"
          />
        </label>
        <label>
          <span>Max Error %</span>
          <input
            type="number"
            min={0}
            max={100}
            step="0.1"
            value={goalConfig.max_error_pct || ''}
            onChange={(event) => onGoalConfigChange('max_error_pct', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="e.g. 2.5"
          />
        </label>
        <label>
          <span>Min VUsers</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.min_vusers || ''}
            onChange={(event) => onGoalConfigChange('min_vusers', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="e.g. 10"
          />
        </label>
        <label>
          <span>Max VUsers</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.max_vusers || ''}
            onChange={(event) => onGoalConfigChange('max_vusers', event.target.value ? Number(event.target.value) : undefined)}
            placeholder="e.g. 300"
          />
        </label>
        <label>
          <span>SLA Breach Window</span>
          <input
            type="text"
            value={goalConfig.sla_breach_window || ''}
            onChange={(event) => onGoalConfigChange('sla_breach_window', event.target.value)}
            placeholder="e.g. 30s"
          />
        </label>
        <label className="checkbox-label goal-checkbox-label">
          <input
            type="checkbox"
            checked={goalConfig.stop_on_sla_breach || false}
            onChange={(event) => onGoalConfigChange('stop_on_sla_breach', event.target.checked)}
          />
          <span>Stop on sustained SLA breach</span>
        </label>
      </div>
    </div>
  )
}