import { GoalStatus, GroupLiveMetrics, TestSummary } from '../types'
import { formatTimeCompact } from '../utils/timeUtils'

interface LiveMetricsSummary {
  tps?: number
  active_vusers?: number
}

interface Props {
  isGoalOriented: boolean
  goalDescription: string
  goalStatus?: GoalStatus
  test: TestSummary
  metrics?: LiveMetricsSummary
  isMultiGroup: boolean
  groupMetrics: GroupLiveMetrics[]
  groupColors: string[]
}

function formatGoalThreshold(value: number | undefined, suffix = ''): string {
  if (value === undefined) return 'disabled'
  return `${Number.isInteger(value) ? value.toString() : value.toFixed(1)}${suffix}`
}

function formatGoalReason(value?: string): string {
  if (!value) return 'none'
  return value.replace(/_/g, ' ')
}

export default function TestExecutionStatusPanels({
  isGoalOriented,
  goalDescription,
  goalStatus,
  test,
  metrics,
  isMultiGroup,
  groupMetrics,
  groupColors,
}: Props) {
  return (
    <>
      {isGoalOriented && (
        <div className="goal-status-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Goal Controller</h3>
              <p className="goal-status-subtitle">{goalDescription}</p>
            </div>
            <div className={`goal-status-badge ${goalStatus?.stop_triggered ? 'danger' : goalStatus?.breach_active ? 'warning' : 'healthy'}`}>
              {goalStatus?.stop_triggered ? 'Stopping' : goalStatus?.breach_active ? 'SLA Breach' : 'Active'}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Target TPS</span>
              <span className="goal-status-value">{(goalStatus?.target_average_tps ?? test.goal_config?.target_average_tps ?? 0).toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Current TPS</span>
              <span className="goal-status-value">{(goalStatus?.current_tps ?? metrics?.tps ?? 0).toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Current VUsers</span>
              <span className="goal-status-value">{goalStatus?.current_total_vusers ?? metrics?.active_vusers ?? test.total_vusers}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Suggested VUsers</span>
              <span className="goal-status-value">{goalStatus?.suggested_total_vusers ?? goalStatus?.current_total_vusers ?? test.total_vusers}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Guardrails</span>
              <span className="goal-status-value">{(test.goal_config?.min_vusers ?? 0)} - {test.goal_config?.max_vusers ?? 'unbounded'}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">SLA Window</span>
              <span className="goal-status-value">{test.goal_config?.sla_breach_window || '30s'}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Max Error Count</span>
              <span className="goal-status-value">{goalStatus?.max_error_count ?? test.goal_config?.max_error_count ?? 'disabled'}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">P95 Latency Ceiling</span>
              <span className="goal-status-value">{formatGoalThreshold(test.goal_config?.p95_latency_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Max Error %</span>
              <span className="goal-status-value">{formatGoalThreshold(test.goal_config?.max_error_pct, '%')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Breach Duration</span>
              <span className="goal-status-value">{goalStatus?.breach_active ? formatTimeCompact(goalStatus?.breach_duration_sec ?? 0) : 'inactive'}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Stop Reason</span>
              <span className="goal-status-value">{formatGoalReason(goalStatus?.stop_reason)}</span>
            </div>
          </div>

          <div className="goal-status-flags">
            <span className={`goal-flag ${goalStatus?.latency_breached ? 'alert' : ''}`}>
              P95 {goalStatus?.latency_breached ? 'breached' : 'healthy'}
            </span>
            <span className={`goal-flag ${goalStatus?.error_breached ? 'alert' : ''}`}>
              Error rate {goalStatus?.error_breached ? 'breached' : 'healthy'}
            </span>
            <span className={`goal-flag ${goalStatus?.error_count_breached ? 'alert' : ''}`}>
              Error count {goalStatus?.error_count_breached ? 'breached' : 'healthy'}
            </span>
            <span className="goal-flag neutral">
              Stop on breach {test.goal_config?.stop_on_sla_breach ? 'enabled' : 'disabled'}
            </span>
            <span className="goal-flag neutral">
              Stop on max errors {test.goal_config?.stop_on_max_errors ? 'enabled' : 'disabled'}
            </span>
          </div>
        </div>
      )}

      {isMultiGroup && groupMetrics.length > 0 && (
        <div className="group-metrics-grid">
          {groupMetrics.map((groupMetric, index) => (
            <div
              key={groupMetric.name}
              className="group-metric-card"
              style={{ borderTopColor: groupColors[index % groupColors.length] }}
            >
              <div className="gm-header">
                <span className="gm-name">{groupMetric.name}</span>
                <span className="gm-vusers">{groupMetric.vusers} VUsers</span>
              </div>
              <div className="gm-stats">
                <div className="gm-stat">
                  <span className="gm-value">{groupMetric.tps?.toFixed(1)}</span>
                  <span className="gm-label">TPS</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{groupMetric.avg_response_ms?.toFixed(0)}</span>
                  <span className="gm-label">Avg ms</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{groupMetric.p90_ms?.toFixed(0)}</span>
                  <span className="gm-label">P90</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{groupMetric.errors}</span>
                  <span className="gm-label">Errors</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}