import TelemetryPolicy from './TelemetryPolicy'
import TelemetryStageDistributions from './TelemetryStageDistributions'
import TelemetryAcceptanceVerdict from './TelemetryAcceptanceVerdict'
import { BrowserDeviceLiveMetrics, BrowserLiveMetrics, CapacityAdvisory, GoalStatus, GroupLiveMetrics, LiveCapacityStatus, RunArchiveHTTPVersionMix, TelemetryHealth, TestSummary, TxMetrics } from '../types'
import { formatTimeCompact } from '../utils/timeUtils'
import { browserMetricRecord, finiteBrowserMetric, formatBrowserSampleCount, formatBrowserEngineLabel, formatDevicePlatformLabel } from './browserBreakdownModel'
import BrowserUXAssessment from './BrowserUXAssessment'
import BrowserDocumentEvidencePanel from './BrowserDocumentEvidencePanel'
import BrowserComparisonTable from './runReport/BrowserComparisonTable'
import type { BrowserDocumentSummary } from '../types/browserDocumentEvidence'
import { MISSING_VALUE } from './runReport/reportPresentation'
import { ratioParts, usedHTTP3, versionMixHeadline } from './runReport/httpVersionMixModel'

interface LiveMetricsSummary {
  browser_documents?: BrowserDocumentSummary | null
  tps?: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  active_vusers?: number
  telemetry_policy?: import('../types/telemetryPolicy').TelemetryPolicyEvidence
  telemetry?: TelemetryHealth
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  capacity_status?: LiveCapacityStatus
  by_region?: Record<string, TxMetrics>
  http_versions?: RunArchiveHTTPVersionMix
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
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return 'unknown'
  if (value < 2000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(2)} s`
}

function formatTelemetryTime(value?: string): string {
  if (!value) return 'unknown'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'unknown'
  return date.toLocaleString()
}

function formatBrowserMetric(value: number | undefined, suffix = '', digits = 1): string {
  const observed = finiteBrowserMetric(value)
  return observed === undefined ? MISSING_VALUE : `${observed.toFixed(digits)}${suffix}`
}

function formatBrowserScore(value: number | undefined): string {
  const observed = finiteBrowserMetric(value)
  return observed === undefined ? MISSING_VALUE : observed.toFixed(3)
}

function formatThroughputGap(value?: number): string {
  if (value === undefined) return '0.0%'
  return `${value.toFixed(1)}%`
}

function formatCapacityNumber(value?: number): string {
  if (value === undefined || Number.isNaN(value)) return '0'
  return Math.round(value).toLocaleString()
}

function capacityBadgeClass(advisory?: CapacityAdvisory): 'healthy' | 'warning' | 'danger' {
  if (!advisory) return 'healthy'
  if (advisory.requires_override || (advisory.shortfall_generators ?? 0) > 0) return 'danger'
  if (advisory.recommended_generators >= advisory.available_generators) return 'warning'
  return 'healthy'
}

function capacityStatusBadgeClass(status?: LiveCapacityStatus, advisory?: CapacityAdvisory): 'healthy' | 'warning' | 'danger' {
  if (status?.rejected_vusers) return 'danger'
  if (status?.saturated) return 'warning'
  return capacityBadgeClass(advisory)
}

function capacityBadgeLabel(status?: LiveCapacityStatus, advisory?: CapacityAdvisory): string {
  if (!status && !advisory) return 'Untracked'
  if ((status?.rejected_vusers ?? 0) > 0 || advisory?.requires_override || (advisory?.shortfall_generators ?? 0) > 0) return 'Rejected Demand'
  if (status?.saturated || (advisory && advisory.recommended_generators >= advisory.available_generators)) return 'Saturated'
  return 'Available'
}

function demandForStatus(test: TestSummary, status: 'queued' | 'rejected'): number {
  const normalized = test.status?.toUpperCase()
  if (status === 'queued') {
    return normalized === 'QUEUED' || normalized === 'PENDING' || normalized === 'RESERVED' ? test.total_vusers : 0
  }
  return test.capacity_advisory?.requires_override ? test.total_vusers : 0
}

function protocolDemand(groups: TestSummary['groups']): Array<[string, number]> {
  const demand = new Map<string, number>()
  for (const group of groups || []) {
    const protocol = group.protocol || 'unknown'
    demand.set(protocol, (demand.get(protocol) || 0) + group.vusers)
  }
  return Array.from(demand.entries()).sort((left, right) => left[0].localeCompare(right[0]))
}

function capacityProtocolRows(test: TestSummary, status?: LiveCapacityStatus): Array<[string, number]> {
  if (status?.by_protocol) {
    return Object.entries(status.by_protocol)
      .map(([protocol, demand]) => [protocol, (demand.allocated_vusers || 0) + (demand.queued_vusers || 0) + (demand.rejected_vusers || 0)] as [string, number])
      .sort((left, right) => left[0].localeCompare(right[0]))
  }
  return protocolDemand(test.groups)
}

function capacityRegionRows(test: TestSummary, status?: LiveCapacityStatus): Array<[string, number]> {
  if (status?.by_region) {
    return Object.entries(status.by_region)
      .map(([region, demand]) => [region, (demand.allocated_vusers || 0) + (demand.queued_vusers || 0) + (demand.rejected_vusers || 0)] as [string, number])
      .sort((left, right) => left[0].localeCompare(right[0]))
  }
  return regionDemand(test)
}

function regionDemand(test: TestSummary): Array<[string, number]> {
  const weights = test.assignment?.region_weights
  if (test.assignment?.mode === 'region_weighted' && weights && Object.keys(weights).length > 0) {
    const totalWeight = Object.values(weights).reduce((sum, value) => sum + Math.max(0, value), 0)
    if (totalWeight > 0) {
      return Object.entries(weights)
        .map(([region, weight]) => [region, Math.round((Math.max(0, weight) / totalWeight) * test.total_vusers)] as [string, number])
        .sort((left, right) => left[0].localeCompare(right[0]))
    }
  }
  if (test.assignment?.mode === 'specific_hosts' && test.assignment.selected_targets?.length) {
    return [['Selected hosts', test.total_vusers]]
  }
  return [['Auto pool', test.total_vusers]]
}

function throughputBadgeClass(metrics?: LiveMetricsSummary): 'healthy' | 'warning' | 'danger' {
  if (!metrics?.target_tps) return 'healthy'
  if (metrics.throughput_target_missed) return 'danger'
  if ((metrics.throughput_gap_pct ?? 0) > 0) return 'warning'
  return 'healthy'
}

function throughputBadgeLabel(metrics?: LiveMetricsSummary): string {
  if (!metrics?.target_tps) return 'Untracked'
  if (metrics.throughput_target_missed) return 'Behind Target'
  if ((metrics.throughput_gap_pct ?? 0) > 0) return 'Catching Up'
  return 'On Target'
}

function observedTelemetryCount(value?: number): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined
}

function formatObservedTelemetryCount(value?: number): string {
  const count = observedTelemetryCount(value)
  return count === undefined ? 'unknown' : count.toLocaleString()
}

function telemetryObservationLabel(metrics?: LiveMetricsSummary): string {
  const telemetry = metrics?.telemetry
  if (!telemetry) return 'Unknown'
  if ((observedTelemetryCount(telemetry.websocket_dropped_messages) ?? 0) > 0 || (observedTelemetryCount(telemetry.websocket_dropped_events) ?? 0) > 0) return 'Observed drop evidence'
  const age = [telemetry.end_to_end_age_ms, telemetry.sample_age_ms].find(value => typeof value === 'number' && Number.isFinite(value) && value >= 0)
  return age === undefined ? 'Unknown' : 'Age observed'
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
  const browserDevices = Object.entries(metrics?.browser_by_device ?? {}).map(([profile, value]) => [profile, { ...browserMetricRecord(value), malformed: !browserMetricRecord(value) }] as const)
  const capacityAdvisory = test.capacity_advisory
  const capacityStatus = metrics?.capacity_status
  const protocolRows = capacityProtocolRows(test, capacityStatus)
  const regionRows = capacityRegionRows(test, capacityStatus)
  const regionalActivity = Object.entries(metrics?.by_region ?? {}).sort((left, right) => (
    (right[1].count ?? 0) - (left[1].count ?? 0) || left[0].localeCompare(right[0])
  ))
  const regionalActivityMax = Math.max(1, ...regionalActivity.map(([, value]) => value.count ?? 0))
  const httpVersions = usedHTTP3(metrics?.http_versions) ? metrics?.http_versions : undefined
  const fallback = ratioParts(httpVersions?.fallback_ratio, 'upgrade-eligible actions')
  const quicFailures = ratioParts(httpVersions?.quic_failure_ratio, 'QUIC attempts')

  return (
    <>
      {(capacityAdvisory || capacityStatus) && (
        <div className="goal-status-card capacity-live-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Live Capacity</h3>
              <p className="goal-status-subtitle">Generator demand, saturation, queue pressure, and protocol mix for this run.</p>
            </div>
            <div className={`goal-status-badge ${capacityStatusBadgeClass(capacityStatus, capacityAdvisory)}`}>
              {capacityBadgeLabel(capacityStatus, capacityAdvisory)}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Allocated VUsers</span>
              <span className="goal-status-value">{formatCapacityNumber(capacityStatus?.allocated_vusers ?? metrics?.active_vusers ?? (test.status === 'RUNNING' ? test.total_vusers : 0))}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Available Generators</span>
              <span className="goal-status-value">{formatCapacityNumber(capacityStatus?.available_generators ?? capacityAdvisory?.available_generators)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Recommended</span>
              <span className="goal-status-value">{formatCapacityNumber(capacityStatus?.recommended_generators ?? capacityAdvisory?.recommended_generators)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Saturated</span>
              <span className="goal-status-value">{(capacityStatus?.saturated ?? capacityBadgeClass(capacityAdvisory) !== 'healthy') ? 'Yes' : 'No'}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Queued Demand</span>
              <span className="goal-status-value">{formatCapacityNumber(capacityStatus?.queued_vusers ?? demandForStatus(test, 'queued'))}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Rejected Demand</span>
              <span className="goal-status-value">{formatCapacityNumber(capacityStatus?.rejected_vusers ?? demandForStatus(test, 'rejected'))}</span>
            </div>
          </div>

          <div className="capacity-live-breakdown">
            <div>
              <span className="capacity-live-heading">By Protocol</span>
              {protocolRows.map(([protocol, vusers]) => <span key={protocol}>{protocol}: {formatCapacityNumber(vusers)} VUsers</span>)}
            </div>
            <div>
              <span className="capacity-live-heading">By Region</span>
              {regionRows.map(([region, vusers]) => <span key={region}>{region}: {formatCapacityNumber(vusers)} VUsers</span>)}
            </div>
          </div>
        </div>
      )}

      {regionalActivity.length > 0 && (
        <div className="goal-status-card regional-activity-card" data-testid="live-regional-activity">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Regional Activity</h3>
              <p className="goal-status-subtitle">The same bounded load-zone aggregation used by live metrics and terminal archives.</p>
            </div>
            <div className="goal-status-badge healthy">{regionalActivity.length} active</div>
          </div>
          <div className="regional-activity-list">
            {regionalActivity.map(([region, value]) => {
              const attempts = value.count ?? 0
              const errors = value.errors ?? 0
              return (
                <div className="regional-activity-row" key={region}>
                  <div><strong>{region}</strong><span>{attempts.toLocaleString()} tx · {errors.toLocaleString()} errors · {(value.p95_ms ?? 0).toFixed(1)} ms p95</span></div>
                  <div className="regional-activity-track" aria-label={`${region} activity ${attempts} transactions`}>
                    <span style={{ width: `${Math.max(2, attempts / regionalActivityMax * 100)}%` }} />
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {metrics?.target_tps ? (
        <div className="goal-status-card throughput-status-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Throughput Tracking</h3>
              <p className="goal-status-subtitle">Configured arrival target versus delivered live throughput for this run.</p>
            </div>
            <div className={`goal-status-badge ${throughputBadgeClass(metrics)}`}>
              {throughputBadgeLabel(metrics)}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Target TPS</span>
              <span className="goal-status-value">{metrics.target_tps.toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Actual TPS</span>
              <span className="goal-status-value">{(metrics.tps ?? 0).toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Gap</span>
              <span className="goal-status-value">{formatThroughputGap(metrics.throughput_gap_pct)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Status</span>
              <span className="goal-status-value">{throughputBadgeLabel(metrics)}</span>
            </div>
          </div>
        </div>
      ) : null}

      {httpVersions && (
        <div className="goal-status-card http-versions-card" data-testid="live-http-versions">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">HTTP versions</h3>
              <p className="goal-status-subtitle">Every HTTP action since the run started, by the version its response used.</p>
            </div>
            <div className={`goal-status-badge ${httpVersions.finding ? 'warning' : 'healthy'}`}>
              {httpVersions.finding ? 'Protocol Mix above expectation' : 'Within expectation'}
            </div>
          </div>
          <p className="http-versions-headline">{versionMixHeadline(httpVersions) || 'No response version observed yet'}</p>
          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">HTTP actions</span>
              <span className="goal-status-value">{httpVersions.actions.toLocaleString()}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Fell back to TCP</span>
              <span className="goal-status-value">{fallback.value}</span>
              <small>{fallback.detail}</small>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">QUIC failures</span>
              <span className="goal-status-value">{quicFailures.value}</span>
              <small>{quicFailures.detail}</small>
            </div>
          </div>
          {httpVersions.finding && <p className="goal-status-subtitle" role="note">{httpVersions.finding.summary}</p>}
        </div>
      )}

      {metrics && <TelemetryPolicy evidence={metrics.telemetry_policy} />}

      {metrics?.telemetry && (
        <div className="goal-status-card telemetry-status-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Telemetry observations</h3>
              <p className="goal-status-subtitle">{['COMPLETE', 'FAILED', 'ABORTED'].includes(test.status) ? 'Retained historical pipeline observations, not current connection health.' : 'Observed ages and delivery counters for the metrics pipeline.'}</p>
            </div>
            <div className="goal-status-badge neutral">
              {telemetryObservationLabel(metrics)}
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
              <span className="goal-status-label">Retained history points</span>
              <span className="goal-status-value">{formatObservedTelemetryCount(metrics.telemetry.history_points)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Observed WS clients</span>
              <span className="goal-status-value">{formatObservedTelemetryCount(metrics.telemetry.websocket_clients)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Observed WS message drops</span>
              <span className="goal-status-value">{formatObservedTelemetryCount(metrics.telemetry.websocket_dropped_messages)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Observed WS event drops</span>
              <span className="goal-status-value">{formatObservedTelemetryCount(metrics.telemetry.websocket_dropped_events)}</span>
            </div>
          </div>
          <p>Drop counters are cumulative controller observations, not an exact run loss total. Age observations do not establish synchronized clocks, complete time coverage, or a telemetry verdict.</p>
          <TelemetryStageDistributions stages={metrics.telemetry.stage_distributions} startedAt={test.started_at} />
          <TelemetryAcceptanceVerdict verdict={metrics.telemetry.acceptance} />
        </div>
      )}

      {metrics?.browser && (
        <div className="goal-status-card browser-metrics-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Browser UX Signals</h3>
              <p className="goal-status-subtitle">Client-side timings captured from browser actions and normalized by device profile.</p>
            </div>
            <div className="goal-status-badge neutral">
              {formatBrowserSampleCount(metrics.browser.samples)}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg FCP</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_fcp_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg TTFB</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_ttfb_ms, ' ms')}</span>
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
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg Browser CPU</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_browser_process_cpu_pct, '%')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Avg Browser Memory</span>
              <span className="goal-status-value">{formatBrowserMetric(metrics.browser.avg_browser_process_mem_mb, ' MB')}</span>
            </div>
          </div>

          <BrowserUXAssessment summary={metrics.browser} devices={metrics.browser_by_device} />

          {browserDevices.length > 0 && (
            <div className="browser-device-grid">
              {browserDevices.map(([profile, deviceMetrics]) => (
                <div key={profile} className="browser-device-card">
                  <div className="browser-device-header">
                    <span className="browser-device-name">{profile}</span>
                    <span className="browser-device-tier">{formatDevicePlatformLabel(deviceMetrics.device_platform)} · {formatBrowserEngineLabel(deviceMetrics.browser_engine)} · {typeof deviceMetrics.device_tier === 'string' && deviceMetrics.device_tier ? deviceMetrics.device_tier : 'unknown'} tier</span>
                  </div>
                  {deviceMetrics.malformed && <p className="archive-section-copy">Malformed summary: expected an object.</p>}
                  <div className="browser-device-stats">
                    <span>TTFB {formatBrowserMetric(deviceMetrics.avg_ttfb_ms, ' ms')}</span>
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

      {(metrics?.browser_documents != null || metrics?.browser || test.groups?.some(group => group.protocol === 'browser')) && (<>
        {/* Live browser intelligence (BI-4): documents compared by engine, device and group. Findings, interaction patterns and journeys read the finalized archive. */}
        <BrowserComparisonTable summary={metrics?.browser_documents} mode="live-history" />
        <BrowserDocumentEvidencePanel summary={metrics?.browser_documents} summaryMode="live-history" />
      </>)}

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
              <span className="goal-status-label">Server Processing Ceiling</span>
              <span className="goal-status-value">{formatGoalThreshold(test.goal_config?.max_avg_server_processing_ms, ' ms')}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Transfer Ceiling</span>
              <span className="goal-status-value">{formatGoalThreshold(test.goal_config?.max_avg_transfer_ms, ' ms')}</span>
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
            <span className={`goal-flag ${goalStatus?.server_processing_breached ? 'alert' : ''}`}>
              Server processing {goalStatus?.server_processing_breached ? 'breached' : 'healthy'}
            </span>
            <span className={`goal-flag ${goalStatus?.transfer_breached ? 'alert' : ''}`}>
              Transfer {goalStatus?.transfer_breached ? 'breached' : 'healthy'}
            </span>
            <span className={`goal-flag ${goalStatus?.error_count_breached ? 'alert' : ''}`}>
              Error count {goalStatus?.error_count_breached ? 'breached' : 'healthy'}
            </span>
            {test.goal_config?.max_lcp_ms !== undefined && (
              <span className={`goal-flag ${goalStatus?.lcp_breached ? 'alert' : ''}`}>
                LCP {goalStatus?.lcp_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_inp_ms !== undefined && (
              <span className={`goal-flag ${goalStatus?.inp_breached ? 'alert' : ''}`}>
                INP {goalStatus?.inp_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_cls !== undefined && (
              <span className={`goal-flag ${goalStatus?.cls_breached ? 'alert' : ''}`}>
                CLS {goalStatus?.cls_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_throttled_requests !== undefined && (
              <span className={`goal-flag ${goalStatus?.throttled_requests_breached ? 'alert' : ''}`}>
                Throttled requests {goalStatus?.throttled_requests_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_end_to_end_latency_ms !== undefined && (
              <span className={`goal-flag ${goalStatus?.end_to_end_latency_breached ? 'alert' : ''}`}>
                End-to-end latency {goalStatus?.end_to_end_latency_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_partition_skew_pct !== undefined && (
              <span className={`goal-flag ${goalStatus?.partition_skew_breached ? 'alert' : ''}`}>
                Partition skew {goalStatus?.partition_skew_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_redelivery_rate_pct !== undefined && (
              <span className={`goal-flag ${goalStatus?.redelivery_rate_breached ? 'alert' : ''}`}>
                Redelivery rate {goalStatus?.redelivery_rate_breached ? 'breached' : 'healthy'}
              </span>
            )}
            {test.goal_config?.max_dead_letter_rate_pct !== undefined && (
              <span className={`goal-flag ${goalStatus?.dead_letter_rate_breached ? 'alert' : ''}`}>
                Dead-letter rate {goalStatus?.dead_letter_rate_breached ? 'breached' : 'healthy'}
              </span>
            )}
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
              {groupMetric.target_tps ? (
                <div className={`gm-throughput-status ${groupMetric.throughput_target_missed ? 'danger' : 'healthy'}`}>
                  <span>Target {groupMetric.target_tps.toFixed(1)} TPS</span>
                  <span>Gap {formatThroughputGap(groupMetric.throughput_gap_pct)}</span>
                </div>
              ) : null}
            </div>
          ))}
        </div>
      )}
    </>
  )
}
