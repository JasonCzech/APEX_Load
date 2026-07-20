import { GoalConfig } from '../types'
import DurationField from './DurationField'

interface Props {
  goalConfig: GoalConfig
  onGoalConfigChange: <K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) => void
}

function optionalNumber(value: string): number | undefined {
  return value === '' ? undefined : Number(value)
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
            min={0}
            step="any"
            value={goalConfig.target_average_tps ?? ''}
            onChange={(event) => onGoalConfigChange('target_average_tps', optionalNumber(event.target.value))}
            placeholder="e.g. 300"
          />
        </label>
        <label>
          <span>P95 Latency Ceiling (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.p95_latency_ms ?? ''}
            onChange={(event) => onGoalConfigChange('p95_latency_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 800"
          />
        </label>
        <label>
          <span>Max Error %</span>
          <input
            type="number"
            min={0}
            max={100}
            step="any"
            value={goalConfig.max_error_pct ?? ''}
            onChange={(event) => onGoalConfigChange('max_error_pct', optionalNumber(event.target.value))}
            placeholder="e.g. 2.5"
          />
        </label>
        <label>
          <span>Avg Server Processing Ceiling (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_avg_server_processing_ms ?? ''}
            onChange={(event) => onGoalConfigChange('max_avg_server_processing_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 450"
          />
        </label>
        <label>
          <span>Avg Transfer Ceiling (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_avg_transfer_ms ?? ''}
            onChange={(event) => onGoalConfigChange('max_avg_transfer_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 120"
          />
        </label>
        <label>
          <span>Max Error Count</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.max_error_count ?? ''}
            onChange={(event) => onGoalConfigChange('max_error_count', optionalNumber(event.target.value))}
            placeholder="e.g. 25"
          />
        </label>
        <label>
          <span>Min VUsers</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.min_vusers ?? ''}
            onChange={(event) => onGoalConfigChange('min_vusers', optionalNumber(event.target.value))}
            placeholder="e.g. 10"
          />
        </label>
        <label>
          <span>Max VUsers</span>
          <input
            type="number"
            min={1}
            step="1"
            value={goalConfig.max_vusers ?? ''}
            onChange={(event) => onGoalConfigChange('max_vusers', optionalNumber(event.target.value))}
            placeholder="e.g. 300"
          />
        </label>
        <label>
          <span>Max LCP (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_lcp_ms ?? ''}
            onChange={(event) => onGoalConfigChange('max_lcp_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 2500"
            title="Largest Contentful Paint ceiling, evaluated against browser canary metrics."
          />
        </label>
        <label>
          <span>Max INP (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_inp_ms ?? ''}
            onChange={(event) => onGoalConfigChange('max_inp_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 200"
            title="Interaction to Next Paint ceiling, evaluated against browser canary metrics."
          />
        </label>
        <label>
          <span>Max CLS</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_cls ?? ''}
            onChange={(event) => onGoalConfigChange('max_cls', optionalNumber(event.target.value))}
            placeholder="e.g. 0.1"
            title="Cumulative Layout Shift ceiling, evaluated against browser canary metrics."
          />
        </label>
        <label>
          <span>Max Throttled Requests</span>
          <input
            type="number"
            min={0}
            step="1"
            value={goalConfig.max_throttled_requests ?? ''}
            onChange={(event) => onGoalConfigChange('max_throttled_requests', optionalNumber(event.target.value))}
            placeholder="e.g. 0"
            title="Maximum total throttled, quota, or server-busy responses during the run."
          />
        </label>
        <label>
          <span>Max End-to-End Latency (ms)</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.max_end_to_end_latency_ms ?? ''}
            onChange={(event) => onGoalConfigChange('max_end_to_end_latency_ms', optionalNumber(event.target.value))}
            placeholder="e.g. 500"
            title="Maximum average producer-to-observed-consumer latency."
          />
        </label>
        <label>
          <span>Max Partition Skew %</span>
          <input
            type="number"
            min={0}
            max={100}
            step="any"
            value={goalConfig.max_partition_skew_pct ?? ''}
            onChange={(event) => onGoalConfigChange('max_partition_skew_pct', optionalNumber(event.target.value))}
            placeholder="e.g. 10"
          />
        </label>
        <label>
          <span>Max Redelivery Rate %</span>
          <input
            type="number"
            min={0}
            max={100}
            step="any"
            value={goalConfig.max_redelivery_rate_pct ?? ''}
            onChange={(event) => onGoalConfigChange('max_redelivery_rate_pct', optionalNumber(event.target.value))}
            placeholder="e.g. 1"
          />
        </label>
        <label>
          <span>Max Dead-Letter Rate %</span>
          <input
            type="number"
            min={0}
            max={100}
            step="any"
            value={goalConfig.max_dead_letter_rate_pct ?? ''}
            onChange={(event) => onGoalConfigChange('max_dead_letter_rate_pct', optionalNumber(event.target.value))}
            placeholder="e.g. 0.5"
          />
        </label>
        <DurationField
          label="SLA Breach Window"
          value={goalConfig.sla_breach_window || ''}
          allowEmpty
          compact
          onChange={(value) => onGoalConfigChange('sla_breach_window', value)}
        />
        <label className="checkbox-label goal-checkbox-label">
          <input
            type="checkbox"
            checked={goalConfig.stop_on_sla_breach ?? false}
            onChange={(event) => onGoalConfigChange('stop_on_sla_breach', event.target.checked)}
          />
          <span>Stop on sustained SLA breach</span>
        </label>
        <label className="checkbox-label goal-checkbox-label">
          <input
            type="checkbox"
            checked={goalConfig.stop_on_max_errors ?? false}
            onChange={(event) => onGoalConfigChange('stop_on_max_errors', event.target.checked)}
          />
          <span>Stop when total errors exceed max error count</span>
        </label>
      </div>
    </div>
  )
}
