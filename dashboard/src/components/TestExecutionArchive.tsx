import { Fragment, useState } from 'react'

import { BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserLiveMetrics, BrowserPlatformLiveMetrics, RunArchiveActionStats, RunArchiveReport, RunArchiveSOAPFaultTimelineBucket, RunArchiveSOAPPayloadTimelineBucket, TestSummary, TransactionEvent } from '../types'
import BrowserBreakdownChart from './BrowserBreakdownChart'
import {
  BrowserBreakdown,
  buildBrowserBreakdownData,
  buildArchiveBrowserBreakdownSeries,
  formatBrowserEngineLabel,
  formatDevicePlatformLabel,
  getBrowserBreakdownChartCopy,
  getBrowserBreakdownSegmentLabel,
  getBrowserBreakdownTitle,
} from './browserBreakdownModel'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import {
  formatCapturedHeaders,
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'

interface Props {
  hasArchive: boolean
  test: TestSummary
  archiveError: string
  archiveLoading: boolean
  archiveReport: RunArchiveReport | null
  onDownloadBrowserDiagnostics: () => void
  onDownloadArchiveArtifact: (artifactName: string) => void
  archiveActionEntries: Array<[string, RunArchiveActionStats]>
  archiveSOAPOperationEntries: Array<[string, RunArchiveActionStats]>
  selectedArchiveAction: string
  onSelectedArchiveActionChange: (value: string) => void
  selectedArchiveSOAPOperation: string
  onSelectedArchiveSOAPOperationChange: (value: string) => void
  archiveGroups: string[]
  selectedArchiveGroup: string
  onSelectedArchiveGroupChange: (value: string) => void
  archiveErrorsOnly: boolean
  onArchiveErrorsOnlyChange: (value: boolean) => void
  archiveTransactionsLoading: boolean
  archiveTransactionsTotal: number
  archivePageStart: number
  archivePageEnd: number
  archiveTransactionsOffset: number
  archivePageSize: number
  onPreviousPage: () => void
  onNextPage: () => void
  archiveTransactions: TransactionEvent[]
  expandedArchiveEvent: string | null
  onExpandedArchiveEventChange: (value: string | null) => void
}

function formatArchiveTimestamp(value?: string): string {
  if (!value) return 'n/a'
  return new Date(value).toLocaleString()
}

function formatBrowserMetric(value: number | undefined, suffix = '', digits = 1): string {
  if (value === undefined) return 'n/a'
  return `${value.toFixed(digits)}${suffix}`
}

function formatBrowserScore(value: number | undefined): string {
  if (value === undefined) return 'n/a'
  return value.toFixed(3)
}

function formatBrowserWaterfall(metrics?: TransactionEvent['client_metrics']): string | null {
  if (!metrics) return null
  const hasTiming =
    metrics.dom_content_loaded_ms !== undefined ||
    metrics.dom_complete_ms !== undefined ||
    metrics.load_event_ms !== undefined ||
    metrics.total_resource_count !== undefined ||
    metrics.resource_transfer_kb !== undefined
  if (!hasTiming) return null

  const lines = [
    `DOM content loaded: ${formatBrowserMetric(metrics.dom_content_loaded_ms, ' ms')}`,
    `DOM complete: ${formatBrowserMetric(metrics.dom_complete_ms, ' ms')}`,
    `Load event: ${formatBrowserMetric(metrics.load_event_ms, ' ms')}`,
    `Resources: ${formatBrowserMetric(metrics.total_resource_count, '', 0)} total / ${formatBrowserMetric(metrics.blocking_resource_count, '', 0)} blocking`,
    `Transfer: ${formatBrowserMetric(metrics.resource_transfer_kb, ' KB', 2)}`,
  ]

  if (metrics.dom_complete_ms !== undefined && metrics.dom_content_loaded_ms !== undefined && metrics.dom_complete_ms >= metrics.dom_content_loaded_ms) {
    lines.push(`DOM completion gap: ${formatBrowserMetric(metrics.dom_complete_ms - metrics.dom_content_loaded_ms, ' ms')}`)
  }
  if (metrics.load_event_ms !== undefined && metrics.dom_complete_ms !== undefined && metrics.load_event_ms >= metrics.dom_complete_ms) {
    lines.push(`Load-event gap: ${formatBrowserMetric(metrics.load_event_ms - metrics.dom_complete_ms, ' ms')}`)
  }
  if (metrics.total_resource_count !== undefined && metrics.total_resource_count > 0 && metrics.blocking_resource_count !== undefined) {
    const blockingPct = (metrics.blocking_resource_count / metrics.total_resource_count) * 100
    lines.push(`Blocking-resource pressure: ${blockingPct.toFixed(1)}%`)
  }

  return lines.join('\n')
}

function buildArchiveStatsEntries(stats?: Record<string, RunArchiveActionStats>) {
  return Object.entries(stats || {}).sort((left, right) => right[1].transactions - left[1].transactions)
}

function buildArchiveCountEntries(counts?: Record<string, number>) {
  return Object.entries(counts || {}).sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
}

function formatArchiveBucketTimestamp(value?: string): string {
  if (!value) return 'n/a'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'n/a'
  return `${parsed.toISOString().slice(0, 16).replace('T', ' ')}Z`
}

function formatArchiveCountSummary(counts?: Record<string, number>) {
  const entries = buildArchiveCountEntries(counts)
  if (entries.length === 0) return 'n/a'
  return entries.map(([name, count]) => `${name} (${count})`).join(', ')
}

function formatArchiveFlag(enabled: boolean | undefined, truthy = 'Enabled', falsy = 'Disabled') {
  return enabled ? truthy : falsy
}

function formatGeneratorInventoryLabel(target?: string) {
  return target || 'generator'
}

function buildArchiveFaultTimelineEntries(buckets?: RunArchiveSOAPFaultTimelineBucket[]) {
  return [...(buckets || [])].sort((left, right) => left.timestamp.localeCompare(right.timestamp))
}

function buildArchivePayloadTimelineEntries(buckets?: RunArchiveSOAPPayloadTimelineBucket[]) {
  return [...(buckets || [])].sort((left, right) => left.timestamp.localeCompare(right.timestamp))
}

function buildArchiveSummaryTimelineEntries(points?: RunArchiveReport['summary_timeline']) {
  return [...(points || [])].sort((left, right) => left.timestamp.localeCompare(right.timestamp))
}

function buildReportHref(testId: string) {
  return `/api/v1/tests/${encodeURIComponent(testId)}/report`
}

function renderBrowserSummary(
  summary: BrowserLiveMetrics,
  devices?: Record<string, BrowserDeviceLiveMetrics>,
  platforms?: Record<string, BrowserPlatformLiveMetrics>,
  engines?: Record<string, BrowserEngineLiveMetrics>,
  breakdown: BrowserBreakdown = 'device',
  onBreakdownChange?: (value: BrowserBreakdown) => void,
) {
  const engineEntries = Object.values(engines ?? {}).sort((left, right) => formatBrowserEngineLabel(left.browser_engine).localeCompare(formatBrowserEngineLabel(right.browser_engine)))
  const platformEntries = Object.values(platforms ?? {}).sort((left, right) => formatDevicePlatformLabel(left.device_platform).localeCompare(formatDevicePlatformLabel(right.device_platform)))
  const deviceEntries = Object.entries(devices ?? {})
  const breakdownChartData = buildBrowserBreakdownData(breakdown, { summary, devices, platforms, engines })
  const breakdownSeries = buildArchiveBrowserBreakdownSeries()
  const chartTitle = getBrowserBreakdownTitle(breakdown, 'Breakdown', 'LCP / INP / CLS')
  const chartCopy = getBrowserBreakdownChartCopy(breakdownSeries)
  const segmentLabel = getBrowserBreakdownSegmentLabel(breakdown)

  return (
    <div className="archive-browser-section">
      <div className="archive-section-header archive-section-header-stack">
        <div>
          <h3 className="chart-title">Browser Metrics</h3>
          <p className="archive-section-copy">Client-side rendering and interaction timings captured during browser actions.</p>
        </div>
        <span className="archive-chip">{summary.samples} samples</span>
      </div>

      <div className="archive-overview-grid">
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg FCP</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_fcp_ms, ' ms')}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg LCP</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_lcp_ms, ' ms')}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg CLS</span>
          <span className="scenario-script-detail-value">{formatBrowserScore(summary.avg_cls_score)}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg INP</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_inp_ms, ' ms')}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg Long Tasks</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_long_task_count, '', 1)}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg Blocking Resources</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_blocking_resources, '', 1)}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg Browser CPU</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_browser_process_cpu_pct, '%')}</span>
        </div>
        <div className="scenario-script-detail-card">
          <span className="scenario-script-detail-label">Avg Browser Memory</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_browser_process_mem_mb, ' MB')}</span>
        </div>
      </div>

      {onBreakdownChange && breakdownChartData.length > 0 && (
        <>
          <div className="results-breakdown-toggle" role="tablist" aria-label="Archive browser breakdown mode">
            {(['overall', 'platform', 'engine', 'device'] as BrowserBreakdown[]).map(mode => (
              <button
                key={mode}
                type="button"
                role="tab"
                aria-selected={breakdown === mode}
                className={`results-breakdown-button${breakdown === mode ? ' active' : ''}`}
                onClick={() => onBreakdownChange(mode)}
              >
                {mode === 'overall' ? 'Overall' : mode === 'platform' ? 'Platform' : mode === 'engine' ? 'Engine' : 'Device'}
              </button>
            ))}
          </div>
          <BrowserBreakdownChart title={chartTitle} copy={chartCopy} data={breakdownChartData} series={breakdownSeries} segmentLabel={segmentLabel} />
        </>
      )}

      {engineEntries.length > 0 && (
        <>
          <span className="results-compare-label">Engine Rollup</span>
          <div className="browser-device-grid archive-browser-device-grid">
            {engineEntries.map(metrics => (
              <div key={metrics.browser_engine || 'browser'} className="browser-device-card">
                <div className="browser-device-header">
                  <span className="browser-device-name">{formatBrowserEngineLabel(metrics.browser_engine)}</span>
                  <span className="browser-device-tier">{metrics.samples} samples</span>
                </div>
                <div className="browser-device-stats">
                  <span>FCP {formatBrowserMetric(metrics.avg_fcp_ms, ' ms')}</span>
                  <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                  <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                  <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}

      {platformEntries.length > 0 && (
        <>
          <span className="results-compare-label">Platform Rollup</span>
          <div className="browser-device-grid archive-browser-device-grid">
            {platformEntries.map(metrics => (
              <div key={metrics.device_platform || 'unknown'} className="browser-device-card">
                <div className="browser-device-header">
                  <span className="browser-device-name">{formatDevicePlatformLabel(metrics.device_platform)}</span>
                  <span className="browser-device-tier">{formatBrowserEngineLabel(metrics.browser_engine)} · {metrics.samples} samples</span>
                </div>
                <div className="browser-device-stats">
                  <span>FCP {formatBrowserMetric(metrics.avg_fcp_ms, ' ms')}</span>
                  <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                  <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                  <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}

      {deviceEntries.length > 0 && (
        <>
          <span className="results-compare-label">Devices</span>
          <div className="browser-device-grid archive-browser-device-grid">
            {deviceEntries.map(([profile, metrics]) => (
              <div key={profile} className="browser-device-card">
                <div className="browser-device-header">
                  <span className="browser-device-name">{profile}</span>
                  <span className="browser-device-tier">{formatDevicePlatformLabel(metrics.device_platform)} · {formatBrowserEngineLabel(metrics.browser_engine)} · {metrics.device_tier || 'unknown'} tier</span>
                </div>
                <div className="browser-device-stats">
                  <span>FCP {formatBrowserMetric(metrics.avg_fcp_ms, ' ms')}</span>
                  <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                  <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                  <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

export default function TestExecutionArchive({
  hasArchive,
  test,
  archiveError,
  archiveLoading,
  archiveReport,
  onDownloadBrowserDiagnostics,
  onDownloadArchiveArtifact,
  archiveActionEntries,
  archiveSOAPOperationEntries,
  selectedArchiveAction,
  onSelectedArchiveActionChange,
  selectedArchiveSOAPOperation,
  onSelectedArchiveSOAPOperationChange,
  archiveGroups,
  selectedArchiveGroup,
  onSelectedArchiveGroupChange,
  archiveErrorsOnly,
  onArchiveErrorsOnlyChange,
  archiveTransactionsLoading,
  archiveTransactionsTotal,
  archivePageStart,
  archivePageEnd,
  archiveTransactionsOffset,
  archivePageSize,
  onPreviousPage,
  onNextPage,
  archiveTransactions,
  expandedArchiveEvent,
  onExpandedArchiveEventChange,
}: Props) {
  const [copiedSection, setCopiedSection] = useState<string | null>(null)
  const [expandedSnapshotSections, setExpandedSnapshotSections] = useState<Record<string, boolean>>({})
  const [browserBreakdown, setBrowserBreakdown] = useState<BrowserBreakdown>('device')
  const soapOperationEntries = archiveSOAPOperationEntries.length > 0 ? archiveSOAPOperationEntries : buildArchiveStatsEntries(archiveReport?.by_soap_operation)
  const soapFaultCodeEntries = buildArchiveCountEntries(archiveReport?.by_soap_fault_code)
  const soapFaultCategoryEntries = buildArchiveCountEntries(archiveReport?.by_soap_fault_category)
  const soapFaultTimelineEntries = buildArchiveFaultTimelineEntries(archiveReport?.soap_fault_timeline)
  const soapPayloadTimelineEntries = buildArchivePayloadTimelineEntries(archiveReport?.soap_payload_timeline)
  const summaryTimelineEntries = buildArchiveSummaryTimelineEntries(archiveReport?.summary_timeline)
  const runtimeBrowserSummary = formatBrowserRuntimeSummary(archiveReport?.manifest.provenance?.runtime_settings?.browser)

  const handleCopySection = async (key: string, value: string) => {
    if (!navigator.clipboard?.writeText) return
    await navigator.clipboard.writeText(value)
    setCopiedSection(key)
  }

  const toggleSnapshotSection = (key: string, expanded: boolean) => {
    setExpandedSnapshotSections(prev => ({
      ...prev,
      [key]: expanded,
    }))
  }

  if (!hasArchive) {
    return null
  }

  return (
    <div className="archive-report-card">
      <div className="archive-report-header">
        <div>
          <h3 className="archive-report-title">Archive Report</h3>
          <p className="archive-report-subtitle">
            Post-run summary derived from the saved JSONL bundle. Completed {formatArchiveTimestamp(test.archive?.completed_at)}.
          </p>
        </div>
        <div className="archive-report-badges">
          <span className="archive-chip">{test.archive?.transaction_count || 0} transactions</span>
          <span className="archive-chip">{test.archive?.resource_sample_count || 0} resource samples</span>
          <span className="archive-chip">{test.archive?.interval_summary_count || 0} summaries</span>
        </div>
      </div>

      {archiveError && <p className="scenario-script-status error">{archiveError}</p>}
      {archiveLoading && <p className="scenario-script-status">Loading archive report…</p>}

      {archiveReport && (
        <>
          <div className="archive-overview-grid">
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Peak TPS</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.peak_tps.toFixed(1)}</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Peak Active VUsers</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.peak_active_vusers}</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Response Range</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.min_response_ms.toFixed(1)} - {archiveReport.overview.max_response_ms.toFixed(1)} ms</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">CPU Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.cpu_process_pct.toFixed(1)}%</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Memory Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.mem_process_mb.toFixed(1)} MB</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Network Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.net_sent_kbps.toFixed(1)} / {archiveReport.resource_peaks.net_recv_kbps.toFixed(1)} KB/s</span>
            </div>
          </div>

          {archiveReport.manifest.provenance && (
            <div className="tx-section archive-summary-section">
              <div className="archive-section-header archive-section-header-stack">
                <div>
                  <h3 className="chart-title">Run Provenance</h3>
                  <p className="archive-section-copy">Immutable execution inputs captured when the archive bundle was created.</p>
                </div>
              </div>
              <div className="archive-overview-grid">
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Execution Profile</span>
                  <span className="scenario-script-detail-value">
                    {archiveReport.manifest.provenance.scenario_mode || 'vuser_driven'} · {archiveReport.manifest.provenance.total_vusers || test.total_vusers} VUsers · {archiveReport.manifest.provenance.duration || test.duration}
                  </span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Scope</span>
                  <span className="scenario-script-detail-value">
                    {(archiveReport.manifest.provenance.project_id || 'unassigned')}
                    {archiveReport.manifest.provenance.release_id ? ` / ${archiveReport.manifest.provenance.release_id}` : ''}
                    {archiveReport.manifest.provenance.scenario_id ? ` / ${archiveReport.manifest.provenance.scenario_id}` : ''}
                  </span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Runtime Flags</span>
                  <span className="scenario-script-detail-value">
                    Shakeout {formatArchiveFlag(archiveReport.manifest.provenance.script_shakeout)} · Baseline {formatArchiveFlag(archiveReport.manifest.provenance.resource_baseline)}
                  </span>
                </div>
                {runtimeBrowserSummary && (
                  <div className="scenario-script-detail-card">
                    <span className="scenario-script-detail-label">Browser Runtime</span>
                    <span className="scenario-script-detail-value">{runtimeBrowserSummary}</span>
                  </div>
                )}
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Metrics Interval</span>
                  <span className="scenario-script-detail-value">{archiveReport.manifest.provenance.execution_settings.general.metrics_interval_seconds}s</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Start Delay</span>
                  <span className="scenario-script-detail-value">{archiveReport.manifest.provenance.start_delay || 'None'}</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Pinned Baseline</span>
                  <span className="scenario-script-detail-value">{archiveReport.manifest.provenance.baseline_pinned ? 'Pinned' : 'Not pinned'}</span>
                </div>
              </div>

              {archiveReport.manifest.provenance.groups && archiveReport.manifest.provenance.groups.length > 0 && (
                <div>
                  <span className="results-compare-label">Scenario Groups</span>
                  <div className="archive-report-badges">
                    {archiveReport.manifest.provenance.groups.map(group => (
                      <span key={`${group.script_id}-${group.name}`} className="archive-chip">
                        {group.name || group.script_name || group.script_id} · {group.vusers} VUsers
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {archiveReport.manifest.provenance.controller_env && Object.keys(archiveReport.manifest.provenance.controller_env).length > 0 && (
                <div>
                  <span className="results-compare-label">Controller Environment</span>
                  <div className="archive-report-badges">
                    {Object.entries(archiveReport.manifest.provenance.controller_env).map(([key, value]) => (
                      <span key={key} className="archive-chip">{key}={value}</span>
                    ))}
                  </div>
                </div>
              )}

              {archiveReport.manifest.provenance.generator_inventory && archiveReport.manifest.provenance.generator_inventory.length > 0 && (
                <div>
                  <span className="results-compare-label">Generator Inventory</span>
                  <div className="archive-report-badges">
                    {archiveReport.manifest.provenance.generator_inventory.map(entry => (
                      <span key={`${entry.target}-${entry.generator_id || entry.status}`} className="archive-chip">
                        {formatGeneratorInventoryLabel(entry.target)} · {entry.status} · {entry.available_capacity} available
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          {archiveReport.error_patterns && archiveReport.error_patterns.length > 0 && (
            <div className="tx-section archive-summary-section">
              <div className="archive-section-header">
                <div>
                  <h3 className="chart-title">Error Patterns</h3>
                  <p className="archive-section-copy">Grouped failure signatures with the first retained snapshot preserved for each cluster.</p>
                </div>
              </div>
              <table className="tx-table">
                <thead>
                  <tr>
                    <th>Pattern</th>
                    <th>Occurrences</th>
                    <th>First Seen</th>
                    <th>Latest Seen</th>
                    <th>Actions</th>
                    <th>Snapshot</th>
                  </tr>
                </thead>
                <tbody>
                  {archiveReport.error_patterns.map(pattern => (
                    <tr key={`${pattern.signature}-${pattern.first_seen}`}>
                      <td className="tx-name">{pattern.sample}</td>
                      <td>{pattern.count}</td>
                      <td>{formatArchiveTimestamp(pattern.first_seen)}</td>
                      <td>{formatArchiveTimestamp(pattern.latest_seen)}</td>
                      <td>{pattern.actions && pattern.actions.length > 0 ? pattern.actions.join(', ') : 'n/a'}</td>
                      <td>{pattern.snapshot ? 'retained' : 'n/a'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {archiveReport.overview.browser && renderBrowserSummary(
            archiveReport.overview.browser,
            archiveReport.browser_by_device,
            archiveReport.browser_by_platform,
            archiveReport.browser_by_engine,
            browserBreakdown,
            setBrowserBreakdown,
          )}

          <div className="archive-artifact-row">
            {archiveReport.overview.browser && (
              <div className="archive-artifact-group">
                <span className="archive-artifact-label">Browser</span>
                <button className="btn btn-secondary btn-sm" onClick={onDownloadBrowserDiagnostics}>
                  Download Browser Diagnostics ZIP
                </button>
              </div>
            )}
            <div className="archive-artifact-group">
              <span className="archive-artifact-label">Report</span>
              <a
                className="btn btn-secondary btn-sm"
                href={buildReportHref(test.id)}
                target="_blank"
                rel="noreferrer"
              >
                Download Report
              </a>
            </div>
            <div className="archive-artifact-group">
              <span className="archive-artifact-label">JSONL</span>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_file || 'transactions.jsonl')}>
                Download Transactions JSONL
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.resource_samples_file || 'resources.jsonl')}>
                Download Resources JSONL
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_file || 'interval_summaries.jsonl')}>
                Download Summaries JSONL
              </button>
            </div>
            <div className="archive-artifact-group">
              <span className="archive-artifact-label">Parquet</span>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_parquet_file || 'transactions.parquet')}>
                Download Transactions Parquet
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.resource_samples_parquet_file || 'resources.parquet')}>
                Download Resources Parquet
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_parquet_file || 'interval_summaries.parquet')}>
                Download Summaries Parquet
              </button>
            </div>
          </div>

          {summaryTimelineEntries.length > 0 && (
            <div className="tx-section archive-summary-section">
              <div className="archive-section-header">
                <div>
                  <h3 className="chart-title">Summary Timeline</h3>
                  <p className="archive-section-copy">Archived interval snapshots for throughput, latency, and error progression across the run.</p>
                </div>
              </div>
              <table className="tx-table">
                <thead>
                  <tr>
                    <th>Bucket</th>
                    <th>Active VUsers</th>
                    <th>TPS</th>
                    <th>P95</th>
                    <th>Error %</th>
                    <th>Total Tx</th>
                    <th>Total Errors</th>
                  </tr>
                </thead>
                <tbody>
                  {summaryTimelineEntries.map((point) => (
                    <tr key={point.timestamp}>
                      <td className="tx-name">{formatArchiveBucketTimestamp(point.timestamp)}</td>
                      <td>{point.active_vusers}</td>
                      <td>{point.tps.toFixed(1)}</td>
                      <td>{point.p95_ms.toFixed(1)} ms</td>
                      <td>{point.error_pct.toFixed(2)}%</td>
                      <td>{point.total_transactions}</td>
                      <td>{point.total_errors}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <div className="archive-report-layout">
            <div className="tx-section archive-action-section">
              <div className="archive-section-header">
                <div>
                  <h3 className="chart-title">Action Summary</h3>
                  <p className="archive-section-copy">Archived totals by transaction name.</p>
                </div>
              </div>
              <table className="tx-table">
                <thead>
                  <tr>
                    <th>Action</th>
                    <th>Count</th>
                    <th>Errors</th>
                    <th>Error %</th>
                    <th>Avg (ms)</th>
                    <th>Max (ms)</th>
                  </tr>
                </thead>
                <tbody>
                  {archiveActionEntries.slice(0, 8).map(([name, stats]) => (
                    <tr key={name}>
                      <td className="tx-name">{name}</td>
                      <td>{stats.transactions}</td>
                      <td className={stats.errors > 0 ? 'error-cell' : ''}>{stats.errors}</td>
                      <td>{stats.error_pct.toFixed(2)}%</td>
                      <td>{stats.avg_response_ms.toFixed(1)}</td>
                      <td>{stats.max_response_ms.toFixed(1)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>

              {soapOperationEntries.length > 0 && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">SOAP Operation Summary</h3>
                      <p className="archive-section-copy">Archived totals grouped by extracted SOAP operation tags.</p>
                    </div>
                  </div>
                  <table className="tx-table">
                    <thead>
                      <tr>
                        <th>Operation</th>
                        <th>Count</th>
                        <th>Errors</th>
                        <th>Error %</th>
                        <th>Avg (ms)</th>
                        <th>Max (ms)</th>
                      </tr>
                    </thead>
                    <tbody>
                      {soapOperationEntries.slice(0, 8).map(([name, stats]) => (
                        <tr key={name}>
                          <td className="tx-name">{name}</td>
                          <td>{stats.transactions}</td>
                          <td className={stats.errors > 0 ? 'error-cell' : ''}>{stats.errors}</td>
                          <td>{stats.error_pct.toFixed(2)}%</td>
                          <td>{stats.avg_response_ms.toFixed(1)}</td>
                          <td>{stats.max_response_ms.toFixed(1)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}

              {(soapFaultCodeEntries.length > 0 || soapFaultCategoryEntries.length > 0) && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">SOAP Fault Summary</h3>
                      <p className="archive-section-copy">Archived SOAP fault distribution grouped by reported code and category.</p>
                    </div>
                  </div>
                  <div className="archive-overview-grid">
                    {soapFaultCodeEntries.length > 0 && (
                      <div>
                        <span className="results-compare-label">Fault Codes</span>
                        <table className="tx-table">
                          <thead>
                            <tr>
                              <th>Code</th>
                              <th>Count</th>
                            </tr>
                          </thead>
                          <tbody>
                            {soapFaultCodeEntries.slice(0, 8).map(([name, count]) => (
                              <tr key={name}>
                                <td className="tx-name">{name}</td>
                                <td>{count}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                    {soapFaultCategoryEntries.length > 0 && (
                      <div>
                        <span className="results-compare-label">Fault Categories</span>
                        <table className="tx-table">
                          <thead>
                            <tr>
                              <th>Category</th>
                              <th>Count</th>
                            </tr>
                          </thead>
                          <tbody>
                            {soapFaultCategoryEntries.slice(0, 8).map(([name, count]) => (
                              <tr key={name}>
                                <td className="tx-name">{name}</td>
                                <td>{count}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </div>
                </>
              )}

              {soapFaultTimelineEntries.length > 0 && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">SOAP Fault Trend</h3>
                      <p className="archive-section-copy">Minute buckets of archived SOAP faults, including the fault-code and category mix seen in each bucket.</p>
                    </div>
                  </div>
                  <table className="tx-table">
                    <thead>
                      <tr>
                        <th>Bucket</th>
                        <th>Total Faults</th>
                        <th>Codes</th>
                        <th>Categories</th>
                      </tr>
                    </thead>
                    <tbody>
                      {soapFaultTimelineEntries.map((bucket) => (
                        <tr key={bucket.timestamp}>
                          <td className="tx-name">{formatArchiveBucketTimestamp(bucket.timestamp)}</td>
                          <td>{bucket.total}</td>
                          <td>{formatArchiveCountSummary(bucket.by_code)}</td>
                          <td>{formatArchiveCountSummary(bucket.by_category)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}

              {soapPayloadTimelineEntries.length > 0 && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">SOAP Payload Trend</h3>
                      <p className="archive-section-copy">Minute buckets of archived SOAP payload sizes, highlighting average wire sizes and XML envelope overhead to spot bloat.</p>
                    </div>
                  </div>
                  <table className="tx-table">
                    <thead>
                      <tr>
                        <th>Bucket</th>
                        <th>Samples</th>
                        <th>Avg Request</th>
                        <th>Avg Response</th>
                        <th>Avg XML Overhead</th>
                        <th>Max XML Overhead</th>
                      </tr>
                    </thead>
                    <tbody>
                      {soapPayloadTimelineEntries.map((bucket) => (
                        <tr key={bucket.timestamp}>
                          <td className="tx-name">{formatArchiveBucketTimestamp(bucket.timestamp)}</td>
                          <td>{bucket.samples}</td>
                          <td>{formatBrowserMetric(bucket.avg_request_payload_kb, ' KB', 2)}</td>
                          <td>{formatBrowserMetric(bucket.avg_response_payload_kb, ' KB', 2)}</td>
                          <td>{formatBrowserMetric(bucket.avg_envelope_overhead_kb, ' KB', 2)}</td>
                          <td>{formatBrowserMetric(bucket.max_envelope_overhead_kb, ' KB', 2)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}
            </div>

            <div className="tx-section archive-drilldown-section">
              <div className="archive-section-header archive-section-header-stack">
                <div>
                  <h3 className="chart-title">Transaction Drill-Down</h3>
                  <p className="archive-section-copy">Sample archived events for a selected action.</p>
                </div>
                <div className="archive-filter-bar">
                  <select
                    className="archive-action-select"
                    value={selectedArchiveAction}
                    onChange={(event) => onSelectedArchiveActionChange(event.target.value)}
                  >
                    {archiveActionEntries.map(([name]) => (
                      <option key={name} value={name}>{name}</option>
                    ))}
                  </select>
                  {soapOperationEntries.length > 0 && (
                    <select
                      className="archive-action-select archive-filter-select"
                      value={selectedArchiveSOAPOperation}
                      onChange={(event) => onSelectedArchiveSOAPOperationChange(event.target.value)}
                    >
                      <option value="">All SOAP operations</option>
                      {soapOperationEntries.map(([name]) => (
                        <option key={name} value={name}>{name}</option>
                      ))}
                    </select>
                  )}
                  <select
                    className="archive-action-select archive-filter-select"
                    value={selectedArchiveGroup}
                    onChange={(event) => onSelectedArchiveGroupChange(event.target.value)}
                  >
                    <option value="">All groups</option>
                    {archiveGroups.map((group) => (
                      <option key={group} value={group}>{group}</option>
                    ))}
                  </select>
                  <label className="archive-checkbox">
                    <input
                      type="checkbox"
                      checked={archiveErrorsOnly}
                      onChange={(event) => onArchiveErrorsOnlyChange(event.target.checked)}
                    />
                    Errors only
                  </label>
                </div>
              </div>
              <div className="archive-pagination-bar">
                <span className="archive-pagination-copy">
                  {archiveTransactionsLoading ? 'Loading events…' : archiveTransactionsTotal > 0 ? `${archivePageStart}-${archivePageEnd} of ${archiveTransactionsTotal}` : 'No archived events'}
                </span>
                <div className="archive-pagination-actions">
                  <button
                    className="btn btn-sm btn-secondary"
                    disabled={archiveTransactionsOffset === 0 || archiveTransactionsLoading}
                    onClick={onPreviousPage}
                  >
                    Prev
                  </button>
                  <button
                    className="btn btn-sm btn-secondary"
                    disabled={archiveTransactionsOffset + archivePageSize >= archiveTransactionsTotal || archiveTransactionsLoading}
                    onClick={onNextPage}
                  >
                    Next
                  </button>
                </div>
              </div>
              {!archiveTransactionsLoading && archiveTransactions.length === 0 ? (
                <p className="scenario-script-status">No archived transactions loaded for this action.</p>
              ) : (
                <table className="tx-table archive-events-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Group</th>
                      <th>Status</th>
                      <th>Duration</th>
                      <th>Bytes</th>
                      <th>Error</th>
                      <th>Details</th>
                    </tr>
                  </thead>
                  <tbody>
                    {archiveTransactions.map((event, index) => {
                      const rowKey = `${event.timestamp}-${index}`
                      const hasSnapshot = Boolean(event.snapshot)
                      const hasInspectDetails = hasSnapshot || Boolean(event.client_metrics) || Boolean(event.server_telemetry)
                      const browserWaterfall = formatBrowserWaterfall(event.client_metrics)
                      const isExpanded = expandedArchiveEvent === rowKey
                      const soapFault = extractSOAPFaultSummaryFromBody(event.snapshot?.response_body)
                      const snapshotSections = [
                        event.snapshot?.request_headers && Object.keys(event.snapshot.request_headers).length > 0 ? 'request-headers' : null,
                        event.snapshot?.request_body ? 'request-body' : null,
                        event.snapshot?.response_headers && Object.keys(event.snapshot.response_headers).length > 0 ? 'response-headers' : null,
                        event.snapshot?.response_body ? 'response-body' : null,
                      ].filter((section): section is 'request-headers' | 'request-body' | 'response-headers' | 'response-body' => Boolean(section))
                      const snapshotSectionKey = (section: 'request-headers' | 'request-body' | 'response-headers' | 'response-body') => `${rowKey}:${section}`
                      const allSnapshotSectionsExpanded = snapshotSections.length > 0 && snapshotSections.every(section => expandedSnapshotSections[snapshotSectionKey(section)])

                      return (
                        <Fragment key={rowKey}>
                          <tr>
                            <td>{formatArchiveTimestamp(event.timestamp)}</td>
                            <td>{event.group || 'n/a'}</td>
                            <td className={event.success ? '' : 'error-cell'}>{event.status_code || (event.success ? 'OK' : 'ERR')}</td>
                            <td>{event.duration_ms.toFixed(1)} ms</td>
                            <td>{event.bytes_received || 0}</td>
                            <td className={!event.success ? 'error-cell' : ''}>{event.error_message || 'None'}</td>
                            <td>
                              {hasInspectDetails ? (
                                <button
                                  className="btn btn-sm btn-secondary archive-detail-toggle"
                                  onClick={() => onExpandedArchiveEventChange(isExpanded ? null : rowKey)}
                                >
                                  {isExpanded ? 'Hide' : 'Inspect'}
                                </button>
                              ) : (
                                <span className="cell-muted">n/a</span>
                              )}
                            </td>
                          </tr>
                          {isExpanded && hasInspectDetails && (
                            <tr className="archive-event-detail-row">
                              <td colSpan={7}>
                                <div className="archive-event-detail-grid">
                                  {hasSnapshot && snapshotSections.length > 0 && (
                                    <div className="archive-event-detail-grid-header">
                                      <span className="scenario-script-detail-label">Captured Snapshot</span>
                                      <button
                                        type="button"
                                        className="btn btn-sm btn-ghost"
                                        onClick={() => {
                                          snapshotSections.forEach(section => {
                                            toggleSnapshotSection(snapshotSectionKey(section), !allSnapshotSectionsExpanded)
                                          })
                                        }}
                                      >
                                        {allSnapshotSectionsExpanded ? 'Collapse snapshots' : 'Expand snapshots'}
                                      </button>
                                    </div>
                                  )}
                                  {soapFault && (
                                    <div className="archive-event-detail-card">
                                      <div className="archive-event-detail-header">
                                        <span className="scenario-script-detail-label">SOAP Fault</span>
                                        <button
                                          type="button"
                                          className="btn btn-sm btn-ghost"
                                          onClick={() => void handleCopySection(`${rowKey}:soap-fault`, formatSOAPFaultSummary(soapFault))}
                                        >
                                          {copiedSection === `${rowKey}:soap-fault` ? 'Copied' : 'Copy fault'}
                                        </button>
                                      </div>
                                      <span className="scenario-script-detail-value">SOAP {soapFault.version}</span>
                                      <pre className="archive-event-detail-pre">Code: {soapFault.code}{'\n'}Reason: {soapFault.reason}</pre>
                                    </div>
                                  )}
                                  {event.client_metrics && (
                                    <div className="archive-event-detail-card">
                                      <div className="archive-event-detail-header">
                                        <span className="scenario-script-detail-label">Client Metrics</span>
                                      </div>
                                      <span className="scenario-script-detail-value">{event.client_metrics.device_profile || 'browser'} · {formatDevicePlatformLabel(event.client_metrics.device_platform)} · {formatBrowserEngineLabel(event.client_metrics.browser_engine)} · {event.client_metrics.device_tier || 'unknown'} tier</span>
                                      <pre className="archive-event-detail-pre">
                                        FCP: {formatBrowserMetric(event.client_metrics.fcp_ms, ' ms')}{'\n'}
                                        LCP: {formatBrowserMetric(event.client_metrics.lcp_ms, ' ms')}{'\n'}
                                        CLS: {formatBrowserScore(event.client_metrics.cls_score)}{'\n'}
                                        INP: {formatBrowserMetric(event.client_metrics.inp_ms, ' ms')}{'\n'}
                                        Blocking resources: {formatBrowserMetric(event.client_metrics.blocking_resource_count, '', 0)}{'\n'}
                                        Long tasks: {formatBrowserMetric(event.client_metrics.long_task_count, '', 0)}{'\n'}
                                        Browser CPU: {formatBrowserMetric(event.client_metrics.browser_process_cpu_pct, '%')}{'\n'}
                                        Browser memory: {formatBrowserMetric(event.client_metrics.browser_process_mem_mb, ' MB')}
                                      </pre>
                                      {browserWaterfall && (
                                        <>
                                          <span className="scenario-script-detail-label">Navigation Waterfall</span>
                                          <pre className="archive-event-detail-pre">{browserWaterfall}</pre>
                                        </>
                                      )}
                                    </div>
                                  )}
                                  {event.server_telemetry && (
                                    <div className="archive-event-detail-card">
                                      <div className="archive-event-detail-header">
                                        <span className="scenario-script-detail-label">Server Telemetry</span>
                                      </div>
                                      <pre className="archive-event-detail-pre">
                                        Server processing: {formatBrowserMetric(event.server_telemetry.server_processing_ms, ' ms')}{'\n'}
                                        Transfer: {formatBrowserMetric(event.server_telemetry.transfer_ms, ' ms')}{'\n'}
                                        Serialization: {formatBrowserMetric(event.server_telemetry.serialization_ms, ' ms')}{'\n'}
                                        Deserialization: {formatBrowserMetric(event.server_telemetry.deserialization_ms, ' ms')}{'\n'}
                                        Request payload: {formatBrowserMetric(event.server_telemetry.request_payload_kb, ' KB', 2)}{'\n'}
                                        Request size class: {event.server_telemetry.request_size_class || 'n/a'}{'\n'}
                                        Response payload: {formatBrowserMetric(event.server_telemetry.response_payload_kb, ' KB', 2)}{'\n'}
                                        Response size class: {event.server_telemetry.response_size_class || 'n/a'}{'\n'}
                                        Envelope overhead: {formatBrowserMetric(event.server_telemetry.envelope_overhead_kb, ' KB', 2)}{'\n'}
                                        SOAP fault: {event.server_telemetry.soap_fault_code || 'n/a'} / {event.server_telemetry.soap_fault_category || 'n/a'}{'\n'}
                                        SOAP namespace conflict: {event.server_telemetry.soap_namespace_conflict || 'n/a'}{'\n'}
                                        Redirects: {event.server_telemetry.redirect_count ?? 0} hops / {formatBrowserMetric(event.server_telemetry.redirect_ms, ' ms')}{'\n'}
                                        Message latency: {formatBrowserMetric(event.server_telemetry.message_latency_ms, ' ms')}{'\n'}
                                        Messages: {event.server_telemetry.messages_sent ?? 0} sent / {event.server_telemetry.messages_received ?? 0} received{'\n'}
                                        Message throughput: {formatBrowserMetric(event.server_telemetry.messages_sent_per_sec, ' sent/s')} / {formatBrowserMetric(event.server_telemetry.messages_received_per_sec, ' received/s')}{'\n'}
                                        Consumer lag: {formatBrowserMetric(event.server_telemetry.consumer_lag_ms, ' ms')}{'\n'}
                                        Connection: {event.server_telemetry.connection_reused === undefined ? 'n/a' : event.server_telemetry.connection_reused ? 'reused' : 'new'}
                                      </pre>
                                    </div>
                                  )}
                                  {hasSnapshot && (
                                    <>
                                      <div className="archive-event-detail-card">
                                        <div className="archive-event-detail-header">
                                          <span className="scenario-script-detail-label">Request</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopySection(`${rowKey}:request`, formatSnapshotRequestSection(event.snapshot))}
                                          >
                                            {copiedSection === `${rowKey}:request` ? 'Copied' : 'Copy request'}
                                          </button>
                                        </div>
                                        <span className="scenario-script-detail-value">{event.snapshot?.request_method || 'n/a'} {event.snapshot?.request_url || ''}</span>
                                        <SnapshotTextPreview
                                          text={formatCapturedHeaders(event.snapshot?.request_headers)}
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('request-headers')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('request-headers'), expanded)}
                                        />
                                        <SnapshotBodyPreview
                                          body={event.snapshot?.request_body}
                                          headers={event.snapshot?.request_headers}
                                          emptyMessage="No request body captured"
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('request-body')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('request-body'), expanded)}
                                        />
                                      </div>
                                      <div className="archive-event-detail-card">
                                        <div className="archive-event-detail-header">
                                          <span className="scenario-script-detail-label">Response</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopySection(`${rowKey}:response`, formatSnapshotResponseSection(event.snapshot, event.status_code))}
                                          >
                                            {copiedSection === `${rowKey}:response` ? 'Copied' : 'Copy response'}
                                          </button>
                                        </div>
                                        <span className="scenario-script-detail-value">{event.snapshot?.response_status || event.status_code || 'n/a'}</span>
                                        <SnapshotTextPreview
                                          text={formatCapturedHeaders(event.snapshot?.response_headers)}
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('response-headers')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('response-headers'), expanded)}
                                        />
                                        <SnapshotBodyPreview
                                          body={event.snapshot?.response_body}
                                          headers={event.snapshot?.response_headers}
                                          emptyMessage="No response body captured"
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('response-body')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('response-body'), expanded)}
                                        />
                                      </div>
                                    </>
                                  )}
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      )
                    })}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
