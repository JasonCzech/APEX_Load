import { BrowserDeviceLiveMetrics, BrowserLiveMetrics, GoalStatus, GroupLiveMetrics, TelemetryHealth, TestSummary } from '../types'
import { formatTimeCompact } from '../utils/timeUtils'

interface LiveMetricsSummary {
  tps?: number
  active_vusers?: number
  telemetry?: TelemetryHealth
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
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

function formatSampleAge(value?: number): string {
  if (value === undefined) return 'unknown'
  if (value < 2000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(2)} s`
}

function formatTelemetryTime(value?: string): string {
  if (!value) return 'unknown'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'unknown'
  return date.toLocaleTimeString()
}

function formatBrowserMetric(value: number | undefined, suffix = '', digits = 1): string {
  if (value === undefined) return 'n/a'
  return `${value.toFixed(digits)}${suffix}`
}

function formatBrowserScore(value: number | undefined): string {
  if (value === undefined) return 'n/a'
  return value.toFixed(3)
}

function formatBrowserEngine(value?: string): string {
  if (value === 'webkit') return 'WebKit'
  if (value === 'chromium') return 'Chromium'
  return value || 'browser'
}

function formatDevicePlatform(value?: string): string {
  if (value === 'ios') return 'iOS'
  if (value === 'ipados') return 'iPadOS'
  if (value === 'android') return 'Android'
  if (value === 'desktop') return 'Desktop'
  return value || 'unknown'
}

function telemetryBadgeClass(metrics?: LiveMetricsSummary): 'healthy' | 'warning' | 'danger' {
  const telemetry = metrics?.telemetry
  if (!telemetry) return 'healthy'
  if ((telemetry.websocket_dropped_messages ?? 0) > 0 || (telemetry.websocket_dropped_events ?? 0) > 0) {
    return 'danger'
  }
  if ((telemetry.end_to_end_age_ms ?? telemetry.sample_age_ms ?? 0) > 2000) {
    return 'warning'
  }
  return 'healthy'
}

function telemetryBadgeLabel(metrics?: LiveMetricsSummary): string {
  const telemetry = metrics?.telemetry
  if (!telemetry) return 'Unknown'
  if ((telemetry.websocket_dropped_messages ?? 0) > 0 || (telemetry.websocket_dropped_events ?? 0) > 0) {
    return 'Degraded'
  }
  if ((telemetry.end_to_end_age_ms ?? telemetry.sample_age_ms ?? 0) > 2000) {
    return 'Stale'
  }
  return 'Healthy'
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
  const browserDevices = Object.entries(metrics?.browser_by_device ?? {})

  return (
    <>
      {metrics?.telemetry && (
        <div className="goal-status-card telemetry-status-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Telemetry Health</h3>
              <p className="goal-status-subtitle">Freshness and delivery pressure for the live metrics pipeline.</p>
            </div>
            <div className={`goal-status-badge ${telemetryBadgeClass(metrics)}`}>
              {telemetryBadgeLabel(metrics)}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Sample Age</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.sample_age_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Generator Lag</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.generator_processing_lag_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Transport Lag</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.transport_lag_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Fan-out Lag</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.controller_fanout_lag_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Delivery Lag</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.delivery_lag_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">End-to-End Age</span>
              <span className="goal-status-value">{formatSampleAge(metrics.telemetry.end_to_end_age_ms)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Collected At</span>
              <span className="goal-status-value">{formatTelemetryTime(metrics.telemetry.collected_at)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Generator Publish</span>
              <span className="goal-status-value">{formatTelemetryTime(metrics.telemetry.generator_published_at)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Controller Receive</span>
              <span className="goal-status-value">{formatTelemetryTime(metrics.telemetry.controller_received_at)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Published At</span>
              <span className="goal-status-value">{formatTelemetryTime(metrics.telemetry.published_at)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Received At</span>
              <span className="goal-status-value">{formatTelemetryTime(metrics.telemetry.client_received_at)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">History Window</span>
              <span className="goal-status-value">{metrics.telemetry.history_points ?? 0}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">WS Clients</span>
              <span className="goal-status-value">{metrics.telemetry.websocket_clients ?? 0}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">WS Drops</span>
              <span className="goal-status-value">{(metrics.telemetry.websocket_dropped_messages ?? 0) + (metrics.telemetry.websocket_dropped_events ?? 0)}</span>
            </div>
          </div>
        </div>
      )}

      {metrics?.browser && (
        <div className="goal-status-card browser-metrics-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Browser UX Signals</h3>
              <p className="goal-status-subtitle">Client-side timings captured from browser actions and normalized by device profile.</p>
            </div>
            <div className="goal-status-badge healthy">
              {metrics.browser.samples} samples
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg FCP</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_fcp_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg LCP</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_lcp_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg CLS</span>
              <span className="goal-status-value">{formatBrowserScore(metrics.browser.avg_cls_score)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg INP</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_inp_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg Long Tasks</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_long_task_count, '', 1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg Blocking Resources</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_blocking_resources, '', 1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg JS Heap</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_js_heap_used_mb, ' MB')}</span>
            </div>
          </div>

          {browserDevices.length > 0 && (
            <div className="browser-device-grid">
              {browserDevices.map(([profile, deviceMetrics]) => (
                <div key={profile} className="browser-device-card">
                  <div className="browser-device-header">
                    <span className="browser-device-name">{profile}</span>
                    <span className="browser-device-tier">{formatDevicePlatform(deviceMetrics.device_platform)} · {formatBrowserEngine(deviceMetrics.browser_engine)} · {deviceMetrics.device_tier || 'unknown'} tier</span>
                  </div>
                  <div className="browser-device-stats">
                    <span>FCP {formatBrowserMetric(deviceMetrics.avg_fcp_ms, ' ms')}</span>
                    <span>LCP {formatBrowserMetric(deviceMetrics.avg_lcp_ms, ' ms')}</span>
                    <span>CLS {formatBrowserScore(deviceMetrics.avg_cls_score)}</span>
                    <span>INP {formatBrowserMetric(deviceMetrics.avg_inp_ms, ' ms')}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

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