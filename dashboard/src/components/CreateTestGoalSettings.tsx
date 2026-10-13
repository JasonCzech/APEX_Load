import { GoalConfig } from '../types'
import DurationField from './DurationField'
import { GOAL_CEILINGS, goalCeilingApplies } from './goalCeilings'

interface Props {
  goalConfig: GoalConfig
  onGoalConfigChange: <K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) => void
  advancedOnly?: boolean
  /** The scenario's group protocols: only ceilings they can produce are offered. Omitted, all are. */
  protocols?: readonly string[]
  /** A traffic profile sets the throughput, so there is no target to enter: only the limits and guardrails. */
  profileManaged?: boolean
}

function optionalNumber(value: string): number | undefined {
  return value === '' ? undefined : Number(value)
}

export default function CreateTestGoalSettings({ goalConfig, onGoalConfigChange, advancedOnly = false, protocols, profileManaged = false }: Props) {
  // A ceiling the groups cannot produce is not offered; one already set stays, so it can be cleared.
  const ceilings = GOAL_CEILINGS.filter(ceiling => goalCeilingApplies(ceiling.scope, protocols) || goalConfig[ceiling.field] !== undefined)
  const renderCeiling = (ceiling: (typeof GOAL_CEILINGS)[number], withPlaceholder: boolean) => (
    <label key={ceiling.field}>
      <span>{ceiling.label}</span>
      <input
        type="number"
        min={ceiling.min ?? 0}
        max={ceiling.max}
        step={ceiling.step}
        value={goalConfig[ceiling.field] ?? ''}
        onChange={event => onGoalConfigChange(ceiling.field, optionalNumber(event.target.value))}
        placeholder={withPlaceholder ? ceiling.placeholder : undefined}
        title={ceiling.title}
      />
    </label>
  )
  if (advancedOnly) {
    return (
      <div className="goal-settings-grid">
        {ceilings.map(ceiling => renderCeiling(ceiling, false))}
        <label className="checkbox-label goal-checkbox-label">
          <input type="checkbox" checked={goalConfig.stop_on_max_errors ?? false} onChange={event => onGoalConfigChange('stop_on_max_errors', event.target.checked)} />
          <span>Stop when total errors exceed max error count</span>
        </label>
      </div>
    )
  }
  return (
    <div className="goal-settings">
      <div className="goal-settings-header">
        <h4>{profileManaged ? 'Limits and guardrails' : 'Goal Settings'}</h4>
        <p>{profileManaged
          ? 'The traffic profile sets the throughput. Set how many VUsers the controller may use and the ceilings it watches.'
          : 'Define the target and guardrails. The script groups below still provide the starting seed load and script mix for the scenario.'}</p>
      </div>
      <div className="goal-settings-grid">
        {!profileManaged && <label>
          <span>Target Avg TPS</span>
          <input
            type="number"
            min={0}
            step="any"
            value={goalConfig.target_average_tps ?? ''}
            onChange={(event) => onGoalConfigChange('target_average_tps', optionalNumber(event.target.value))}
            placeholder="e.g. 300"
          />
        </label>}
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
        {ceilings.filter(ceiling => ceiling.scope === 'any').map(ceiling => renderCeiling(ceiling, true))}
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
        {ceilings.filter(ceiling => ceiling.scope !== 'any').map(ceiling => renderCeiling(ceiling, true))}
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
