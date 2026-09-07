import { Fragment, useMemo, useState } from 'react'

import { BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserLiveMetrics, BrowserPlatformLiveMetrics, MetricsHistoryPoint, RunArchiveAnalysisEdge, RunArchiveAnalysisNode, RunArchiveAnalysisResponse, RunArchiveAnalysisStats, RunArchiveActionStats, RunArchiveReport, RunArchiveSOAPFaultTimelineBucket, RunArchiveSOAPPayloadTimelineBucket, RunArchiveTransactionDrilldown, TestSummary, TransactionEvent } from '../types'
import BrowserBreakdownChart from './BrowserBreakdownChart'
import { SortIndicator as ArchiveSortIndicator } from './SortIndicator'
import LoadImpactHeatmap from './LoadImpactHeatmap'
import {
  BrowserBreakdown,
  buildBrowserBreakdownData,
  buildArchiveBrowserBreakdownSeries,
  buildBrowserUXInsight,
  formatBrowserEngineLabel,
  formatDevicePlatformLabel,
  getBrowserBreakdownChartCopy,
  getBrowserBreakdownSegmentLabel,
  getBrowserBreakdownTitle,
} from './browserBreakdownModel'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotFullDialog from './SnapshotFullDialog'
import SnapshotTextPreview from './SnapshotTextPreview'
import DesktopApiResourceLink from './DesktopApiResourceLink'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import {
  formatSnapshotRequestLineAndHeaders,
  formatSnapshotRequestSection,
  formatSnapshotResponseStatusAndHeaders,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { formatTimeCompact } from '../utils/timeUtils'
import {
  ARCHIVE_PAGE_SIZE_OPTIONS,
  DEFAULT_ARCHIVE_TRANSACTION_FILTERS,
  type ArchiveTransactionFilterField,
  type ArchiveTransactionFilters,
  type ArchiveTransactionSort,
  type ArchiveTransactionSortKey,
} from './useExecutionArchiveModel'

type ArchiveView =
  | 'all'
  | 'report'
  | 'report-overview'
  | 'report-completeness'
  | 'report-metadata'
  | 'report-error-patterns'
  | 'report-browser-summary'
  | 'report-browser-resources'
  | 'report-assets'
  | 'report-profiles'
  | 'report-summary-timeline'
  | 'report-analysis'
  | 'load-impact'
  | 'action-summary'
  | 'transactions'

interface Props {
  view?: ArchiveView
  hasArchive: boolean
  test: TestSummary
  archiveError: string
  archiveLoading: boolean
  archiveReport: RunArchiveReport | null
  archiveAnalysis?: RunArchiveAnalysisResponse | null
  archiveAnalysisLoading?: boolean
  archiveAnalysisError?: string
  loadImpactData?: MetricsHistoryPoint[]
  loadImpactLoading?: boolean
  loadImpactError?: string
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
  archiveTransactionSort?: ArchiveTransactionSort
  onArchiveTransactionSortChange?: (key: ArchiveTransactionSortKey) => void
  archiveTransactionFilters?: ArchiveTransactionFilters
  archiveTransactionFiltersActive?: boolean
  onArchiveTransactionFilterChange?: (field: ArchiveTransactionFilterField, value: string) => void
  onArchiveTransactionFiltersReset?: () => void
  archiveTransactionsLoading: boolean
  archiveTransactionsTotal: number
  archivePageStart: number
  archivePageEnd: number
  archiveTransactionsOffset: number
  archivePageSize: number
  onPageSizeChange: (size: number) => void
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

function formatArchiveTime(value?: string): string {
  if (!value) return 'n/a'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'n/a'
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 } as Intl.DateTimeFormatOptions)
}

function formatBrowserMetric(value: number | undefined, suffix = '', digits = 1): string {
  if (value === undefined) return 'n/a'
  return `${value.toFixed(digits)}${suffix}`
}

function formatBrowserScore(value: number | undefined): string {
  if (value === undefined) return 'n/a'
  return value.toFixed(3)
}

function formatArchiveNumber(value: number | undefined, digits = 1): string {
  if (value === undefined || Number.isNaN(value)) return 'n/a'
  return value.toFixed(digits)
}

function formatArchivePercent(value: number | undefined, digits = 2): string {
  const formatted = formatArchiveNumber(value, digits)
  return formatted === 'n/a' ? formatted : `${formatted}%`
}

function formatArchiveCount(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) return 'n/a'
  return value.toLocaleString()
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

function TransactionNetworkWaterfall({ event }: { event: TransactionEvent }) {
	const phases = [
		{ label: 'DNS', value: event.dns_ms || 0 },
		{ label: 'Connect', value: event.connect_ms || 0 },
		{ label: 'TLS', value: event.tls_ms || 0 },
		{ label: 'TTFB', value: event.ttfb_ms || 0 },
		{ label: 'Transfer', value: Math.max(0, event.duration_ms - (event.ttfb_ms || 0)) },
	].filter(item => item.value > 0)
	const resources = (event.network_entries || []).slice(0, 64)
	if (phases.length === 0 && resources.length === 0) return null
	const maxDuration = Math.max(1, event.duration_ms, ...resources.map(resource => resource.duration_ms || 0))
	return (
		<div className="archive-waterfall" aria-label="Network waterfall">
			{phases.map((phase) => (
				<div className="archive-waterfall-row" key={phase.label}>
					<span className="archive-waterfall-label">{phase.label}</span>
					<span className="archive-waterfall-track"><span className="archive-waterfall-bar phase" style={{ width: `${Math.max(2, (phase.value / maxDuration) * 100)}%` }} /></span>
					<span className="archive-waterfall-value">{phase.value.toFixed(1)} ms</span>
				</div>
			))}
			{resources.map((resource, index) => {
				const duration = resource.duration_ms || 0
				return (
					<div className="archive-waterfall-row" key={`${resource.url || 'resource'}-${index}`}>
						<span className="archive-waterfall-label" title={resource.url}>{resource.resource_type || resource.method || 'Resource'}</span>
						<span className="archive-waterfall-track"><span className="archive-waterfall-bar resource" style={{ width: `${Math.max(2, (duration / maxDuration) * 100)}%` }} /></span>
						<span className="archive-waterfall-value">{duration.toFixed(1)} ms</span>
					</div>
				)
			})}
		</div>
	)
}

function formatBrowserResourceBytes(value?: number): string {
  if (value === undefined) return 'n/a'
  if (value >= 1024 * 1024) return `${(value / 1024 / 1024).toFixed(2)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(2)} KB`
  return `${value} B`
}

function formatBrowserResourceURL(value?: string): string {
  if (!value) return 'n/a'
  try {
    const parsed = new URL(value)
    return `${parsed.host}${parsed.pathname}`
  } catch {
    return value
  }
}

function formatClientMetricsBlock(metrics: NonNullable<TransactionEvent['client_metrics']>): string {
  const lines: string[] = []
  if (metrics.ttfb_ms !== undefined) lines.push(`TTFB: ${formatBrowserMetric(metrics.ttfb_ms, ' ms')}`)
  if (metrics.fcp_ms !== undefined) lines.push(`FCP: ${formatBrowserMetric(metrics.fcp_ms, ' ms')}`)
  if (metrics.lcp_ms !== undefined) lines.push(`LCP: ${formatBrowserMetric(metrics.lcp_ms, ' ms')}`)
  if (metrics.cls_score !== undefined) lines.push(`CLS: ${formatBrowserScore(metrics.cls_score)}`)
  if (metrics.inp_ms !== undefined) lines.push(`INP: ${formatBrowserMetric(metrics.inp_ms, ' ms')}`)
  if (metrics.blocking_resource_count !== undefined) lines.push(`Blocking resources: ${formatBrowserMetric(metrics.blocking_resource_count, '', 0)}`)
  if (metrics.long_task_count !== undefined) lines.push(`Long tasks: ${formatBrowserMetric(metrics.long_task_count, '', 0)}`)
  if (metrics.browser_process_cpu_pct !== undefined) lines.push(`Browser CPU: ${formatBrowserMetric(metrics.browser_process_cpu_pct, '%')}`)
  if (metrics.browser_process_mem_mb !== undefined) lines.push(`Browser memory: ${formatBrowserMetric(metrics.browser_process_mem_mb, ' MB')}`)
  return lines.length > 0 ? lines.join('\n') : 'No client metrics captured'
}

function formatServerTelemetryBlock(telemetry: NonNullable<TransactionEvent['server_telemetry']>): string {
  const lines: string[] = []
  if (telemetry.server_processing_ms !== undefined) lines.push(`Server processing: ${formatBrowserMetric(telemetry.server_processing_ms, ' ms')}`)
  if (telemetry.transfer_ms !== undefined) lines.push(`Transfer: ${formatBrowserMetric(telemetry.transfer_ms, ' ms')}`)
  if (telemetry.first_event_ms !== undefined || telemetry.event_count !== undefined) {
    const streamParts: string[] = []
    if (telemetry.first_event_ms !== undefined) streamParts.push(`first event ${formatBrowserMetric(telemetry.first_event_ms, ' ms')}`)
    if (telemetry.event_count !== undefined) streamParts.push(`${telemetry.event_count} events`)
    lines.push(`Stream: ${streamParts.join(' / ')}`)
  }
  if (telemetry.serialization_ms !== undefined) lines.push(`Serialization: ${formatBrowserMetric(telemetry.serialization_ms, ' ms')}`)
  if (telemetry.deserialization_ms !== undefined) lines.push(`Deserialization: ${formatBrowserMetric(telemetry.deserialization_ms, ' ms')}`)
  if (telemetry.request_payload_kb !== undefined) lines.push(`Request payload: ${formatBrowserMetric(telemetry.request_payload_kb, ' KB', 2)}`)
  if (telemetry.request_size_class) lines.push(`Request size class: ${telemetry.request_size_class}`)
  if (telemetry.response_payload_kb !== undefined) lines.push(`Response payload: ${formatBrowserMetric(telemetry.response_payload_kb, ' KB', 2)}`)
  if (telemetry.response_size_class) lines.push(`Response size class: ${telemetry.response_size_class}`)
  if (telemetry.envelope_overhead_kb !== undefined) lines.push(`Envelope overhead: ${formatBrowserMetric(telemetry.envelope_overhead_kb, ' KB', 2)}`)
  if (telemetry.soap_fault_code) lines.push(`SOAP fault: ${telemetry.soap_fault_code} / ${telemetry.soap_fault_category || 'unknown'}`)
  if (telemetry.soap_namespace_conflict) lines.push(`SOAP namespace conflict: ${telemetry.soap_namespace_conflict}`)
  if (telemetry.redirect_count) lines.push(`Redirects: ${telemetry.redirect_count} hops / ${formatBrowserMetric(telemetry.redirect_ms, ' ms')}`)
  if (telemetry.message_latency_ms !== undefined) lines.push(`Message latency: ${formatBrowserMetric(telemetry.message_latency_ms, ' ms')}`)
  if (telemetry.messages_sent || telemetry.messages_received) lines.push(`Messages: ${telemetry.messages_sent ?? 0} sent / ${telemetry.messages_received ?? 0} received`)
  if (telemetry.messages_sent_per_sec !== undefined || telemetry.messages_received_per_sec !== undefined) lines.push(`Message throughput: ${formatBrowserMetric(telemetry.messages_sent_per_sec, ' sent/s')} / ${formatBrowserMetric(telemetry.messages_received_per_sec, ' received/s')}`)
  if (telemetry.consumer_lag_ms !== undefined) lines.push(`Consumer lag: ${formatBrowserMetric(telemetry.consumer_lag_ms, ' ms')}`)
  if (telemetry.connection_reused !== undefined) lines.push(`Connection: ${telemetry.connection_reused ? 'reused' : 'new'}`)
  return lines.length > 0 ? lines.join('\n') : 'No server telemetry captured'
}

function formatProtocolEvidenceSummary(evidence?: TransactionEvent['protocol_evidence']): string {
  if (!evidence) return 'Protocol Evidence'
  const parts = [
    evidence.protocol,
    evidence.operation,
    evidence.status,
    evidence.target,
  ].filter((value): value is string => typeof value === 'string' && value.trim().length > 0)
  return parts.length > 0 ? parts.join(' / ') : 'Protocol Evidence'
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
  return parsed.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit', second: '2-digit',
  })
}

function formatArchiveCountSummary(counts?: Record<string, number>) {
  const entries = buildArchiveCountEntries(counts)
  if (entries.length === 0) return 'n/a'
  return entries.map(([name, count]) => `${name} (${count})`).join(', ')
}

function formatArchiveFlag(enabled: boolean | undefined, truthy = 'Enabled', falsy = 'Disabled') {
  return enabled ? truthy : falsy
}

function formatArchiveAnalysisKind(value?: string): string {
  const labels: Record<string, string> = {
    action: 'Action',
    dependency: 'Dependency',
    group: 'Group',
    region: 'Region',
    source_transaction: 'Source Transaction',
    action_dependency: 'Action to Dependency',
    group_action: 'Group to Action',
    region_action: 'Region to Action',
    source_transaction_action: 'Source to Action',
  }
  if (!value) return 'Unknown'
  if (labels[value]) return labels[value]
  return value
    .split('_')
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function archiveAnalysisCurrentStats(runs: RunArchiveAnalysisStats[] = [], testID: string): RunArchiveAnalysisStats | undefined {
  return runs.find(run => run.run_id === testID) || runs[runs.length - 1]
}

function archiveAnalysisBaselineStats(runs: RunArchiveAnalysisStats[] = [], testID: string): RunArchiveAnalysisStats | undefined {
  return runs.find(run => run.run_id !== testID)
}

function formatArchiveAnalysisStats(stats?: RunArchiveAnalysisStats): string {
  if (!stats) return 'n/a'
  return `${formatArchiveCount(stats.transactions)} tx · ${formatArchivePercent(stats.error_pct)} err · avg ${formatArchiveNumber(stats.avg_response_ms)} ms`
}

function sortArchiveAnalysisNodes(nodes: RunArchiveAnalysisNode[], testID: string): RunArchiveAnalysisNode[] {
  return [...nodes].sort((left, right) => {
    const leftStats = archiveAnalysisCurrentStats(left.runs, testID)
    const rightStats = archiveAnalysisCurrentStats(right.runs, testID)
    const txDelta = (rightStats?.transactions || 0) - (leftStats?.transactions || 0)
    if (txDelta !== 0) return txDelta
    const errorDelta = (rightStats?.errors || 0) - (leftStats?.errors || 0)
    if (errorDelta !== 0) return errorDelta
    return `${left.kind}:${left.label}`.localeCompare(`${right.kind}:${right.label}`)
  })
}

function sortArchiveAnalysisEdges(edges: RunArchiveAnalysisEdge[], testID: string): RunArchiveAnalysisEdge[] {
  return [...edges].sort((left, right) => {
    const leftStats = archiveAnalysisCurrentStats(left.runs, testID)
    const rightStats = archiveAnalysisCurrentStats(right.runs, testID)
    const txDelta = (rightStats?.transactions || 0) - (leftStats?.transactions || 0)
    if (txDelta !== 0) return txDelta
    const errorDelta = (rightStats?.errors || 0) - (leftStats?.errors || 0)
    if (errorDelta !== 0) return errorDelta
    return left.id.localeCompare(right.id)
  })
}

function formatArchiveAnalysisPath(row: RunArchiveTransactionDrilldown): string {
  if (row.path && row.path.length > 0) return row.path.join(' / ')
  return [row.region, row.group, row.source_transaction, row.action_name, row.dependency_target].filter(Boolean).join(' / ') || 'n/a'
}

function formatArchiveAnalysisDependency(row: RunArchiveTransactionDrilldown): string {
  if (row.dependency_type && row.dependency_target) return `${row.dependency_type}: ${row.dependency_target}`
  return row.dependency_target || row.dependency_type || 'n/a'
}

function formatGeneratorInventoryLabel(target?: string) {
  return target || 'generator'
}

type ArchiveCompactTone = 'healthy' | 'warning' | 'danger' | 'neutral'

interface ArchiveCompactItem {
  label: string
  value: string
}

interface ArchiveCompactPanel {
  id: string
  title: string
  status?: string
  tone: ArchiveCompactTone
  items: ArchiveCompactItem[]
}

function compactValue(value?: string | number | null): string {
  if (value === undefined || value === null || value === '') return 'n/a'
  return String(value)
}

function compactStatus(value?: string): string {
  return compactValue(value).replace(/_/g, ' ')
}

function compactList(values: Array<string | undefined>, limit = 3): string {
  const present = values.filter((value): value is string => Boolean(value))
  if (present.length === 0) return 'n/a'
  const visible = present.slice(0, limit).join(', ')
  return present.length > limit ? `${visible}, +${present.length - limit} more` : visible
}

function compactTimestamp(value?: string): string {
  return value ? formatArchiveTimestamp(value) : 'n/a'
}

function compactDurationMs(value?: number): string {
  if (value === undefined || !Number.isFinite(value)) return 'n/a'
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`
  return `${value.toFixed(0)}ms`
}

function compactToneForStatus(status?: string): ArchiveCompactTone {
  const normalized = (status || '').toLowerCase()
  if (['breached', 'incomplete', 'incompatible', 'failed', 'critical', 'error'].some(term => normalized.includes(term))) {
    return 'danger'
  }
  if (['degraded', 'needs_review', 'warning', 'missing', 'unsupported'].some(term => normalized.includes(term))) {
    return 'warning'
  }
  if (['healthy', 'complete', 'compatible', 'present', 'passed'].some(term => normalized.includes(term))) {
    return 'healthy'
  }
  return 'neutral'
}

function formatCompactGroup(group: NonNullable<NonNullable<RunArchiveReport['manifest']['provenance']>['groups']>[number]) {
  return `${group.name || group.script_name || group.script_id} · ${group.vusers} VUsers${group.initialization === 'at_once' ? ' · init once' : ''}`
}

function buildArchiveCompactPanels(
  report: RunArchiveReport,
  test: TestSummary,
  runtimeBrowserSummary: string | null,
): ArchiveCompactPanel[] {
  const telemetry = report.overview.telemetry
  const provenance = report.manifest.provenance
  const telemetryStatus = telemetry?.status || 'not captured'
  const compatibilityStatus = report.compatibility?.status || 'not captured'
  const evidenceTiers = report.evidence_tiers || []
  const evidenceMissing = evidenceTiers.filter(tier => tier.status !== 'present').length
  const evidenceStatus = evidenceTiers.length === 0
    ? 'not captured'
    : evidenceMissing > 0
      ? `${evidenceMissing} missing`
      : 'present'
  const runtimeOverrides = report.change_attribution?.runtime_overrides
    ? compactList(report.change_attribution.runtime_override_fields || ['Yes'])
    : 'No'

  return [
    {
      id: 'telemetry',
      title: 'Telemetry Health',
      status: compactStatus(telemetryStatus),
      tone: compactToneForStatus(telemetryStatus),
      items: [
        { label: 'Samples', value: compactValue(telemetry?.samples ?? 0) },
        {
          label: 'Intervals',
          value: telemetry
            ? `${telemetry.healthy_intervals || 0} healthy · ${telemetry.degraded_intervals || 0} degraded · ${telemetry.breached_intervals || 0} breached`
            : 'n/a',
        },
        { label: 'Max Sample Age', value: compactDurationMs(telemetry?.max_sample_age_ms) },
        { label: 'Generator Lag', value: compactDurationMs(telemetry?.max_generator_processing_lag_ms) },
        { label: 'Transport Lag', value: compactDurationMs(telemetry?.max_transport_lag_ms) },
        { label: 'Breach Window', value: telemetry?.first_breach_at || telemetry?.last_breach_at ? `${compactTimestamp(telemetry?.first_breach_at)} - ${compactTimestamp(telemetry?.last_breach_at)}` : 'n/a' },
      ],
    },
    {
      id: 'schema',
      title: 'Schema Compatibility',
      status: compactStatus(compatibilityStatus),
      tone: compactToneForStatus(compatibilityStatus),
      items: [
        { label: 'Report Schema', value: compactValue(report.compatibility?.report_schema_version) },
        { label: 'Manifest', value: compactValue(report.compatibility?.manifest_schema_version) },
        {
          label: 'Outputs',
          value: compactList((report.compatibility?.outputs || []).map(output => `${output.format.replace(/_/g, ' ')}: ${output.status}`), 4),
        },
        {
          label: 'Issues',
          value: report.compatibility?.issues?.length
            ? compactList(report.compatibility.issues.map(issue => `${issue.severity}: ${issue.code}`), 3)
            : 'None',
        },
      ],
    },
    {
      id: 'evidence',
      title: 'Evidence Tiers',
      status: evidenceStatus,
      tone: compactToneForStatus(evidenceStatus),
      items: [
        { label: 'Tiers', value: compactList(evidenceTiers.map(tier => `${tier.label}: ${tier.status} · ${tier.count || 0}`), 5) },
        { label: 'Present', value: compactValue(evidenceTiers.filter(tier => tier.status === 'present').length) },
        { label: 'Missing', value: compactValue(evidenceMissing) },
      ],
    },
    {
      id: 'provenance',
      title: 'Run Provenance',
      status: provenance ? 'captured' : 'not captured',
      tone: provenance ? 'healthy' : 'warning',
      items: [
        { label: 'Execution Profile', value: provenance ? `${provenance.scenario_mode || 'vuser_driven'} · ${provenance.total_vusers || test.total_vusers} VUsers · ${provenance.duration || test.duration}` : 'n/a' },
        { label: 'Scope', value: provenance ? `${provenance.project_id || 'unassigned'}${provenance.release_id ? ` / ${provenance.release_id}` : ''}${provenance.scenario_id ? ` / ${provenance.scenario_id}` : ''}` : 'n/a' },
        { label: 'Runtime Flags', value: provenance ? `Shakeout ${formatArchiveFlag(provenance.script_shakeout)} · Baseline ${formatArchiveFlag(provenance.resource_baseline)}` : 'n/a' },
        { label: 'Browser Runtime', value: compactValue(runtimeBrowserSummary) },
        { label: 'Metrics Interval', value: provenance?.execution_settings?.general?.metrics_interval_seconds !== undefined ? `${provenance.execution_settings.general.metrics_interval_seconds}s` : 'n/a' },
        { label: 'Pinned Baseline', value: provenance?.baseline_pinned ? 'Pinned' : 'Not pinned' },
        { label: 'Scenario Groups', value: compactList((provenance?.groups || []).map(formatCompactGroup), 3) },
        { label: 'Controller Environment', value: compactList(Object.entries(provenance?.controller_env || {}).map(([key, value]) => `${key}=${value}`), 2) },
        { label: 'Generator Inventory', value: compactList((provenance?.generator_inventory || []).map(entry => `${formatGeneratorInventoryLabel(entry.target)} · ${entry.status} · ${entry.available_capacity} available`), 3) },
      ],
    },
    {
      id: 'change',
      title: 'Change Attribution',
      status: report.change_attribution ? 'captured' : 'not captured',
      tone: report.change_attribution ? 'healthy' : 'warning',
      items: [
        { label: 'Scripts', value: compactList(report.change_attribution?.script_revisions || []) },
        { label: 'Scenario', value: compactValue(report.change_attribution?.scenario_revision) },
        { label: 'Environment', value: compactValue(report.change_attribution?.environment) },
        { label: 'Generators', value: compactList(report.change_attribution?.generator_versions || []) },
        { label: 'Data', value: compactValue(report.change_attribution?.data_revision) },
        { label: 'Runtime Overrides', value: runtimeOverrides },
      ],
    },
  ]
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

function buildReportHref(testId: string, profile?: string) {
  const base = `/api/v1/tests/${encodeURIComponent(testId)}/report`
  return profile ? `${base}?profile=${encodeURIComponent(profile)}` : base
}

function buildArchiveReportHref(testId: string, format?: string, profile?: string, download = false) {
  const params = new URLSearchParams()
  if (format) params.set('format', format)
  if (profile) params.set('profile', profile)
  if (download) params.set('download', 'true')
  const query = params.toString()
  return `/api/v1/tests/${encodeURIComponent(testId)}/archive/report${query ? `?${query}` : ''}`
}

function reportProfileIcon(profileId: string): string {
  switch (profileId) {
    case 'executive':
      return 'Decision'
    case 'audit':
      return 'Evidence'
    default:
      return 'Engineering'
  }
}

function formatReportSectionLabel(section: string): string {
  const labels: Record<string, string> = {
    verdict: 'Verdict',
    executive_overview: 'Executive overview',
    technical_overview: 'Technical overview',
    sla_rollup: 'SLA rollup',
    sla_detail: 'SLA detail',
    release_trend: 'Release trend',
    risk_summary: 'Risk summary',
    network_phases: 'Network phases',
    audit_identity: 'Run identity',
    authorization: 'Authorization',
    governance: 'Governance',
    schema_compatibility: 'Schema compatibility',
    evidence_tiers: 'Evidence tiers',
    artifact_integrity: 'Artifact integrity',
    report_authorship: 'Report authorship',
    official_outcome: 'Official outcome',
  }
  return labels[section] || section.replace(/_/g, ' ')
}

function formatManualCopyTitle(key: string): string {
  if (key.endsWith(':request')) return 'Full request'
  if (key.endsWith(':response')) return 'Full response'
  if (key.endsWith(':soap-fault')) return 'Full SOAP fault'
  return 'Full text'
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
  const uxInsight = buildBrowserUXInsight(summary, devices)

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
          <span className="scenario-script-detail-label">Avg TTFB</span>
          <span className="scenario-script-detail-value">{formatBrowserMetric(summary.avg_ttfb_ms, ' ms')}</span>
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

      {uxInsight && (
        <div className={`browser-ux-card ${uxInsight.severity}`}>
          <div className="browser-ux-header">
            <div>
              <span className="results-compare-label">UX Intelligence</span>
              <h4 className="browser-ux-title">{uxInsight.label} · {uxInsight.score}/100</h4>
            </div>
            {uxInsight.worstDevice && <span className="archive-chip">Weakest: {uxInsight.worstDevice}</span>}
          </div>
          <p className="archive-section-copy">{uxInsight.summary}</p>
          <div className="browser-ux-signal-grid">
            {uxInsight.signals.map(signal => (
              <div key={signal.label} className={`browser-ux-signal ${signal.severity}`}>
                <div className="browser-ux-signal-row">
                  <span className="browser-ux-signal-label">{signal.label}</span>
                  <span className="browser-ux-signal-value">{signal.value}</span>
                </div>
                <span className="browser-ux-signal-detail">{signal.detail}</span>
              </div>
            ))}
          </div>
        </div>
      )}

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
                  <span>TTFB {formatBrowserMetric(metrics.avg_ttfb_ms, ' ms')}</span>
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
                  <span>TTFB {formatBrowserMetric(metrics.avg_ttfb_ms, ' ms')}</span>
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
                  <span>TTFB {formatBrowserMetric(metrics.avg_ttfb_ms, ' ms')}</span>
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
  view = 'all',
  hasArchive,
  test,
  archiveError,
  archiveLoading,
  archiveReport,
  archiveAnalysis = null,
  archiveAnalysisLoading = false,
  archiveAnalysisError = '',
  loadImpactData = [],
  loadImpactLoading = false,
  loadImpactError = '',
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
  archiveTransactionSort = null,
  onArchiveTransactionSortChange = () => {},
  archiveTransactionFilters = DEFAULT_ARCHIVE_TRANSACTION_FILTERS,
  archiveTransactionFiltersActive = false,
  onArchiveTransactionFilterChange = () => {},
  onArchiveTransactionFiltersReset = () => {},
  archiveTransactionsLoading,
  archiveTransactionsTotal,
  archivePageStart,
  archivePageEnd,
  archiveTransactionsOffset,
  archivePageSize,
  onPageSizeChange,
  onPreviousPage,
  onNextPage,
  archiveTransactions,
  expandedArchiveEvent,
  onExpandedArchiveEventChange,
}: Props) {
  const [copiedSection, setCopiedSection] = useState<string | null>(null)
  const [copyFailedSection, setCopyFailedSection] = useState<string | null>(null)
  const [manualCopySection, setManualCopySection] = useState<{ key: string; title: string; value: string } | null>(null)
  const [expandedSnapshotSections, setExpandedSnapshotSections] = useState<Record<string, boolean>>({})
  const [browserBreakdown, setBrowserBreakdown] = useState<BrowserBreakdown>('device')
  const [summaryTimelineExpanded, setSummaryTimelineExpanded] = useState(false)
  const [summaryTimelineErrorsOnly, setSummaryTimelineErrorsOnly] = useState(false)
  const archiveDerived = useMemo(() => ({
    soapOperationEntries: buildArchiveStatsEntries(archiveReport?.by_soap_operation),
    regionEntries: buildArchiveStatsEntries(archiveReport?.by_region),
    networkProfileEntries: buildArchiveStatsEntries(archiveReport?.by_network_profile),
    soapFaultCodeEntries: buildArchiveCountEntries(archiveReport?.by_soap_fault_code),
    soapFaultCategoryEntries: buildArchiveCountEntries(archiveReport?.by_soap_fault_category),
    soapFaultTimelineEntries: buildArchiveFaultTimelineEntries(archiveReport?.soap_fault_timeline),
    soapPayloadTimelineEntries: buildArchivePayloadTimelineEntries(archiveReport?.soap_payload_timeline),
    summaryTimelineEntries: buildArchiveSummaryTimelineEntries(archiveReport?.summary_timeline),
    runtimeBrowserSummary: formatBrowserRuntimeSummary(archiveReport?.manifest.provenance?.runtime_settings?.browser),
  }), [archiveReport])
  const soapOperationEntries = archiveSOAPOperationEntries.length > 0
    ? archiveSOAPOperationEntries
    : archiveDerived.soapOperationEntries
  const {
    regionEntries,
    networkProfileEntries,
    soapFaultCodeEntries,
    soapFaultCategoryEntries,
    soapFaultTimelineEntries,
    soapPayloadTimelineEntries,
    runtimeBrowserSummary,
  } = archiveDerived
  const summaryTimelineEntriesAll = archiveDerived.summaryTimelineEntries
  const summaryTimelineErrorCount = useMemo(
    () => summaryTimelineEntriesAll.filter(p => p.total_errors > 0).length,
    [summaryTimelineEntriesAll],
  )
  const summaryTimelineFiltered = useMemo(
    () => summaryTimelineErrorsOnly ? summaryTimelineEntriesAll.filter(p => p.total_errors > 0) : summaryTimelineEntriesAll,
    [summaryTimelineEntriesAll, summaryTimelineErrorsOnly],
  )
  const SUMMARY_TIMELINE_COLLAPSED_LIMIT = 12
  const summaryTimelineNeedsCollapse = summaryTimelineFiltered.length > SUMMARY_TIMELINE_COLLAPSED_LIMIT
  const summaryTimelineEntries = summaryTimelineExpanded || !summaryTimelineNeedsCollapse ? summaryTimelineFiltered : summaryTimelineFiltered.slice(0, SUMMARY_TIMELINE_COLLAPSED_LIMIT)
  const compactMetadataPanels = useMemo(
    () => archiveReport ? buildArchiveCompactPanels(archiveReport, test, runtimeBrowserSummary) : [],
    [archiveReport, runtimeBrowserSummary, test],
  )
  const selectedActionStats = (archiveReport?.metric_populations ? archiveReport.by_request : archiveReport?.by_action)?.[selectedArchiveAction]
  const { archiveAnalysisNodes, archiveAnalysisEdges, archiveAnalysisNodeByID } = useMemo(() => ({
    archiveAnalysisNodes: sortArchiveAnalysisNodes(archiveAnalysis?.graph.nodes || [], test.id),
    archiveAnalysisEdges: sortArchiveAnalysisEdges(archiveAnalysis?.graph.edges || [], test.id),
    archiveAnalysisNodeByID: new Map((archiveAnalysis?.graph.nodes || []).map(node => [node.id, node])),
  }), [archiveAnalysis, test.id])
  const showArchiveAnalysis = archiveAnalysisLoading || Boolean(archiveAnalysis) || Boolean(archiveAnalysisError)
  const archiveAnalysisHasBaseline = Boolean(archiveAnalysis?.baseline_id)
  const showAllViews = view === 'all'
  const showFullReportView = showAllViews || view === 'report'
  const showStandaloneReportSection = !showFullReportView
  const showReportOverviewView = showFullReportView || view === 'report-overview'
  const showReportCompletenessView = showFullReportView || view === 'report-completeness'
  const showReportMetadataView = showFullReportView || view === 'report-metadata'
  const showReportErrorPatternsView = showFullReportView || view === 'report-error-patterns'
  const showReportBrowserSummaryView = showFullReportView || view === 'report-browser-summary'
  const showReportBrowserResourcesView = showFullReportView || view === 'report-browser-resources'
  const showReportAssetsView = showFullReportView || view === 'report-assets'
  const showReportProfilesView = showFullReportView || view === 'report-profiles'
  const showReportSummaryTimelineView = showFullReportView || view === 'report-summary-timeline'
  const showReportAnalysisView = showFullReportView || view === 'report-analysis'
  const showLoadImpactView = showAllViews || view === 'load-impact'
  const showActionSummaryView = showAllViews || view === 'action-summary'
  const showTransactionsView = showAllViews || view === 'transactions'
  const renderArchiveSortButton = (key: ArchiveTransactionSortKey, label: string) => {
    const active = archiveTransactionSort?.key === key
    return (
      <button
        type="button"
        className={`archive-sort-button${active ? ' active' : ''}`}
        onClick={() => onArchiveTransactionSortChange(key)}
        aria-pressed={active}
      >
        <span>{label}</span>
        <ArchiveSortIndicator active={active} direction={archiveTransactionSort?.direction} />
      </button>
    )
  }

  const handleCopySection = async (key: string, value: string) => {
    const copied = await copyTextToClipboard(value)
    setCopiedSection(copied ? key : null)
    setCopyFailedSection(copied ? null : key)
    setManualCopySection(copied ? null : { key, title: formatManualCopyTitle(key), value })
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
      <SnapshotFullDialog
        title={manualCopySection?.title ?? 'Full text'}
        open={manualCopySection !== null}
        copied={copiedSection === manualCopySection?.key}
        copyFailed={copyFailedSection === manualCopySection?.key}
        copyDisabled={manualCopySection === null}
        manualCopyText={manualCopySection?.value}
        onCopy={() => {
          if (manualCopySection) {
            void handleCopySection(manualCopySection.key, manualCopySection.value)
          }
        }}
        onClose={() => setManualCopySection(null)}
      >
        <pre>{manualCopySection?.value ?? ''}</pre>
      </SnapshotFullDialog>
      {showFullReportView && (
        <div className="archive-report-header">
          <div>
            <h3 className="archive-report-title">Archive Report</h3>
            <p className="archive-report-subtitle">
              Post-run summary derived from the saved JSONL bundle. Completed {formatArchiveTimestamp(test.archive?.completed_at)}.
            </p>
          </div>
          <div className="archive-report-badges">
            <span className="archive-chip">{test.archive?.transaction_count || 0} archive records</span>
            <span className="archive-chip">{test.archive?.resource_sample_count || 0} resource samples</span>
            <span className="archive-chip">{test.archive?.interval_summary_count || 0} summaries</span>
          </div>
        </div>
      )}

      {archiveError && <p className="scenario-script-status error">{archiveError}</p>}
      {archiveLoading && <p className="scenario-script-status">Loading archive report…</p>}

      {archiveReport && (
        <>
          {showReportProfilesView && (
            archiveReport.report_profiles?.length ? (
              <details><summary>Export report — audience and format</summary><div className="archive-profile-grid">
                {archiveReport.report_profiles.map(profile => (
                  <div key={profile.id} className="archive-profile-card">
                    <div className="archive-profile-heading">
                      <strong>{reportProfileIcon(profile.id)} {profile.label}</strong>
                      <span>{profile.export_formats.join(', ')}</span>
                    </div>
                    <p>{profile.description}</p>
                    {(profile.best_for || profile.audience) && (
                      <p className="archive-section-copy">{profile.best_for || profile.audience}</p>
                    )}
                    <div className="archive-report-badges">
                      {(profile.section_order?.length ? profile.section_order : profile.included_sections).slice(0, 8).map(section => (
                        <span key={section} className="archive-chip">{formatReportSectionLabel(section)}</span>
                      ))}
                    </div>
                    <div className="inline-create-row">
                      {profile.export_formats.includes('html') && (
                        <DesktopApiResourceLink className="btn btn-secondary btn-sm" href={buildReportHref(test.id, profile.id)} target="_blank" rel="noreferrer" mode="open" filename={`${test.id}-${profile.id}-report.html`}>{profile.label} HTML</DesktopApiResourceLink>
                      )}
                      {profile.export_formats.includes('csv') && (
                        <DesktopApiResourceLink className="btn btn-secondary btn-sm" href={buildArchiveReportHref(test.id, 'csv', profile.id)} target="_blank" rel="noreferrer" filename={`${test.id}-${profile.id}-report.csv`}>{profile.label} CSV</DesktopApiResourceLink>
                      )}
                      {profile.export_formats.includes('pdf') && (
                        <DesktopApiResourceLink className="btn btn-secondary btn-sm" href={buildArchiveReportHref(test.id, 'pdf', profile.id)} target="_blank" rel="noreferrer" filename={`${test.id}-${profile.id}-report.pdf`}>{profile.label} PDF</DesktopApiResourceLink>
                      )}
                      {profile.export_formats.includes('docx') && (
                        <DesktopApiResourceLink className="btn btn-secondary btn-sm" href={buildArchiveReportHref(test.id, 'docx', profile.id)} target="_blank" rel="noreferrer" filename={`${test.id}-${profile.id}-report.docx`}>{profile.label} DOCX</DesktopApiResourceLink>
                      )}
                      {profile.export_formats.includes('jsonl') && (
                        <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_file || 'transactions.jsonl')}>{profile.label} JSONL</button>
                      )}
                      {profile.export_formats.includes('parquet') && (
                        <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_parquet_file || 'transactions.parquet')}>{profile.label} Parquet</button>
                      )}
                    </div>
                  </div>
                ))}
              </div></details>
            ) : showStandaloneReportSection ? (
              <p className="scenario-script-status">No report profiles are configured for this archive.</p>
            ) : null
          )}

          {(showReportOverviewView || showReportCompletenessView || showReportMetadataView) && (
            <>
              {showReportOverviewView && (
				<>
				<div className="archive-overview-grid">
                  <div className="scenario-script-detail-card">
                    <span className="scenario-script-detail-label">Peak successful transaction TPS</span>
                    <span className="scenario-script-detail-value">{archiveReport.overview.peak_tps.toFixed(1)}</span>
                    {archiveReport.overview.peak_target_tps ? (
                      <span className="capacity-advisory-detail">Target {archiveReport.overview.peak_target_tps.toFixed(1)} TPS</span>
                    ) : null}
                  </div>
                  {archiveReport.overview.max_throughput_gap_pct !== undefined && (
                    <div className="scenario-script-detail-card">
                      <span className="scenario-script-detail-label">Throughput Gap</span>
                      <span className="scenario-script-detail-value">{archiveReport.overview.max_throughput_gap_pct.toFixed(1)}%</span>
                      <span className="capacity-advisory-detail">{archiveReport.overview.throughput_missed_intervals || 0} missed target interval{archiveReport.overview.throughput_missed_intervals === 1 ? '' : 's'}</span>
                    </div>
                  )}
                  {archiveReport.overview.goal_completion && (
                    <div className="scenario-script-detail-card">
                      <span className="scenario-script-detail-label">Named transaction volume</span>
                      <span className="scenario-script-detail-value">{archiveReport.overview.goal_completion.transaction_completion_pct !== undefined ? `${archiveReport.overview.goal_completion.transaction_completion_pct.toFixed(1)}%` : archiveReport.metric_populations?.transaction?.transactions.toLocaleString() ?? 'Unknown'}</span>
                      <span className="capacity-advisory-detail">
                        {archiveReport.overview.goal_completion.configured_max_transactions
                          ? `${archiveReport.overview.goal_completion.achieved_transactions.toLocaleString()} / ${archiveReport.overview.goal_completion.configured_max_transactions.toLocaleString()} transactions`
                          : archiveReport.overview.goal_completion.configured_global_iterations
                            ? `${archiveReport.overview.goal_completion.configured_global_iterations.toLocaleString()} configured global iterations`
                            : `${archiveReport.overview.goal_completion.throughput_missed_intervals || 0} missed throughput intervals`}
                      </span>
                    </div>
                  )}
                  {archiveReport.overview.load_model_goals?.stage_count ? (
                    <div className="scenario-script-detail-card">
                      <span className="scenario-script-detail-label">Staged Load</span>
                      <span className="scenario-script-detail-value">{archiveReport.overview.load_model_goals.stage_count}</span>
                      <span className="capacity-advisory-detail">{archiveReport.overview.load_model_goals.staged_groups || 0} group{archiveReport.overview.load_model_goals.staged_groups === 1 ? '' : 's'} · {archiveReport.overview.load_model_goals.once_only_groups || 0} once-only</span>
                    </div>
                  ) : null}
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
				{archiveReport.load_level_summaries && archiveReport.load_level_summaries.length > 0 && (
					<div className="tx-section archive-summary-section">
						<div className="archive-section-header archive-section-header-stack">
							<div><h3 className="chart-title">Performance by Load Level</h3><p className="archive-section-copy">Retained interval evidence for each stepped-ramp group and aggregate load level.</p></div>
						</div>
						<div className="scenario-table-scroll"><table className="tx-table"><thead><tr><th>Group</th><th>Level</th><th>VUsers</th><th>Observed</th><th>Requests</th><th>Errors</th><th>Error Rate</th><th>TPS</th><th>P50</th><th>P90</th><th>P95</th><th>P99</th></tr></thead><tbody>
							{archiveReport.load_level_summaries.map(level => <tr key={`${level.group}-${level.stage_index}-${level.target_vusers}`}><td>{level.group === '__total__' ? 'Total' : level.group}</td><td>{level.name}{level.partial ? ' (partial)' : ''}</td><td>{level.target_vusers}</td><td>{formatTimeCompact(level.observed_duration_seconds)}</td><td>{level.requests.toLocaleString()}</td><td>{level.errors.toLocaleString()}</td><td>{level.error_rate_pct.toFixed(2)}%</td><td>{level.throughput.toFixed(1)}</td><td>{level.p50_ms.toFixed(1)}</td><td>{level.p90_ms.toFixed(1)}</td><td>{level.p95_ms.toFixed(1)}</td><td>{level.p99_ms.toFixed(1)}</td></tr>)}
						</tbody></table></div>
					</div>
				)}
				</>
              )}

              {showReportCompletenessView && (
                archiveReport.completeness ? (
                  <div className="tx-section archive-summary-section">
                    <div className="archive-section-header archive-section-header-stack">
                      <div>
                        <h3 className="chart-title">Completeness Score</h3>
                        <p className="archive-section-copy">Archive coverage score for intervals, telemetry, writer health, and artifact availability.</p>
                      </div>
                      <span className={`archive-chip ${archiveReport.completeness.status === 'incomplete' ? 'danger' : ''}`}>
                        {archiveReport.completeness.score_pct.toFixed(0)}% · {archiveReport.completeness.status.replace(/_/g, ' ')}
                      </span>
                    </div>
                    {archiveReport.completeness.findings && archiveReport.completeness.findings.length > 0 && (
                      <div className="archive-overview-grid">
                        {archiveReport.completeness.findings.map((finding, index) => (
                          <div key={`${finding.dimension}-${index}`} className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">{finding.dimension.replace(/_/g, ' ')}</span>
                            <span className="scenario-script-detail-value"><span className={`archive-finding-severity ${finding.severity}`}>{finding.severity}</span> · -{finding.penalty.toFixed(0)}</span>
                            <span className="capacity-advisory-detail">{finding.message}</span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                ) : showStandaloneReportSection ? (
                  <p className="scenario-script-status">Completeness scoring is not available for this archive.</p>
                ) : null
              )}

              {showReportMetadataView && (
                <section className="archive-metadata-summary" aria-label="Archive metadata summary">
                  <div className="archive-metadata-grid">
                    {compactMetadataPanels.map(panel => (
                      <div key={panel.id} className="archive-metadata-box">
                        <div className="archive-metadata-box-header">
                          <h3>{panel.title}</h3>
                          {panel.status && <span className={`archive-compact-status ${panel.tone}`}>{panel.status}</span>}
                        </div>
                        <dl className="archive-compact-list">
                          {panel.items.map(item => (
                            <div key={`${panel.id}-${item.label}`} className="archive-compact-row">
                              <dt>{item.label}</dt>
                              <dd>{item.value}</dd>
                            </div>
                          ))}
                        </dl>
                      </div>
                    ))}
                  </div>
                </section>
              )}
            </>
          )}

          {showLoadImpactView && (
              <LoadImpactHeatmap
                points={loadImpactData}
                loading={loadImpactLoading}
                error={loadImpactError}
                title="Load Impact"
                subtitle="Archived performance and resource response grouped by active VUser load."
                emptyMessage="Load impact appears when archived interval summaries or resource samples include active VUser data."
              />
          )}

          {(showReportErrorPatternsView || showReportBrowserSummaryView || showReportBrowserResourcesView || showReportAssetsView || showReportProfilesView || showReportSummaryTimelineView || showReportAnalysisView) && (
            <>
              {showReportErrorPatternsView && (
                archiveReport.error_patterns && archiveReport.error_patterns.length > 0 ? (
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
                ) : showStandaloneReportSection ? (
                  <p className="scenario-script-status">No grouped error patterns were captured for this archive.</p>
                ) : null
              )}

          {showReportBrowserSummaryView && (
            archiveReport.overview.browser ? renderBrowserSummary(
              archiveReport.overview.browser,
              archiveReport.browser_by_device,
              archiveReport.browser_by_platform,
              archiveReport.browser_by_engine,
              browserBreakdown,
              setBrowserBreakdown,
            ) : showStandaloneReportSection ? (
              <p className="scenario-script-status">Browser metrics were not captured for this archive.</p>
            ) : null
          )}

          {showReportBrowserResourcesView && (
            archiveReport.browser_resources && archiveReport.browser_resources.length > 0 ? (
              <div className="tx-section archive-summary-section">
                <div className="archive-section-header archive-section-header-stack">
                  <div>
                    <h3 className="chart-title">Browser Resource Waterfall</h3>
                    <p className="archive-section-copy">Slowest browser sub-resources captured from archived network entries.</p>
                  </div>
                  <span className="archive-chip">{archiveReport.browser_resources.length} resources</span>
                </div>
                <table className="tx-table">
                  <thead>
                    <tr>
                      <th>Resource</th>
                      <th>Type</th>
                      <th>Status</th>
                      <th>Duration</th>
                      <th>Transfer</th>
                      <th>Action</th>
                      <th>Device</th>
                    </tr>
                  </thead>
                  <tbody>
                    {archiveReport.browser_resources.slice(0, 12).map((resource, index) => (
                      <tr key={`${resource.url}-${resource.action_name}-${index}`}>
                        <td className="tx-name" title={resource.url}>{formatBrowserResourceURL(resource.url)}</td>
                        <td>{resource.resource_type || 'resource'}</td>
                        <td>{resource.status_code || 'n/a'}</td>
                        <td>{formatBrowserMetric(resource.duration_ms, ' ms')}</td>
                        <td>{formatBrowserResourceBytes(resource.bytes_received)}</td>
                        <td>{resource.action_name || 'n/a'}</td>
                        <td>{resource.device_profile || resource.device_platform || resource.browser_engine || 'n/a'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : showStandaloneReportSection ? (
              <p className="scenario-script-status">No browser resource waterfall entries were captured for this archive.</p>
            ) : null
          )}

          {showReportAssetsView && (
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
                <DesktopApiResourceLink
                  className="btn btn-secondary btn-sm"
                  href={buildReportHref(test.id)}
                  target="_blank"
                  rel="noreferrer"
                  mode="open"
                  filename={`${test.id}-report.html`}>
                  Download Report
                </DesktopApiResourceLink>
                <DesktopApiResourceLink
                  className="btn btn-secondary btn-sm"
                  href={buildArchiveReportHref(test.id, undefined, undefined, true)}
                  target="_blank"
                  rel="noreferrer"
                  filename={`${test.id}-report.json`}
                >
                  Download Report JSON
                </DesktopApiResourceLink>
                {archiveReport.report_profiles?.map(profile => (
                  <DesktopApiResourceLink
                    key={profile.id}
                    className="btn btn-secondary btn-sm"
                    href={buildReportHref(test.id, profile.id)}
                    target="_blank"
                    rel="noreferrer"
                    title={profile.description}
                    mode="open"
                    filename={`${test.id}-${profile.id}-report.html`}
                  >
                    {profile.label}
                  </DesktopApiResourceLink>
                ))}
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
          )}

          {showReportSummaryTimelineView && (
            summaryTimelineEntriesAll.length > 0 ? (
              <div className="tx-section archive-summary-section">
                <div className="archive-section-header archive-section-header-stack">
                  <div>
                    <h3 className="chart-title">Summary Timeline</h3>
                    <p className="archive-section-copy">Archived interval snapshots for throughput, latency, and error progression across the run.</p>
                  </div>
                  <div className="archive-filter-bar">
                    <span className="archive-chip">{summaryTimelineEntriesAll.length} intervals</span>
                    {summaryTimelineErrorCount > 0 && (
                      <label className="archive-checkbox">
                        <input
                          type="checkbox"
                          checked={summaryTimelineErrorsOnly}
                          onChange={(event) => { setSummaryTimelineErrorsOnly(event.target.checked); setSummaryTimelineExpanded(false) }}
                        />
                        Errors only ({summaryTimelineErrorCount})
                      </label>
                    )}
                  </div>
                </div>
                {summaryTimelineFiltered.length === 0 ? (
                  <p className="scenario-script-status">No intervals with errors.</p>
                ) : (
                  <>
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
                          <tr key={point.timestamp} className={point.total_errors > 0 ? 'archive-error-row' : ''}>
                            <td className="tx-name">{formatArchiveBucketTimestamp(point.timestamp)}</td>
                            <td>{point.active_vusers}</td>
                            <td>{point.tps.toFixed(1)}</td>
                            <td>{point.p95_ms.toFixed(1)} ms</td>
                            <td className={point.error_pct > 0 ? 'error-cell' : ''}>{point.error_pct.toFixed(2)}%</td>
                            <td>{point.total_transactions}</td>
                            <td className={point.total_errors > 0 ? 'error-cell' : ''}>{point.total_errors}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {summaryTimelineNeedsCollapse && (
                      <button
                        className="btn btn-sm btn-ghost archive-timeline-toggle"
                        onClick={() => setSummaryTimelineExpanded(prev => !prev)}
                      >
                        {summaryTimelineExpanded ? 'Show less' : `Show all ${summaryTimelineFiltered.length} intervals`}
                      </button>
                    )}
                  </>
                )}
              </div>
            ) : showStandaloneReportSection ? (
              <p className="scenario-script-status">No summary timeline intervals were archived for this run.</p>
            ) : null
          )}

              {showReportAnalysisView && showArchiveAnalysis && (
                <div className="tx-section archive-summary-section archive-analysis-section" data-testid="archive-analysis-section">
                  <div className="archive-section-header archive-section-header-stack">
                    <div>
                      <h3 className="chart-title">Archive Analysis</h3>
                      <p className="archive-section-copy">Merged graph and filtered transaction path evidence from the archived run.</p>
                    </div>
                    <div className="archive-filter-bar">
                      {archiveAnalysis && (
                        <>
                          <span className="archive-chip">{archiveAnalysis.graph.nodes.length} nodes</span>
                          <span className="archive-chip">{archiveAnalysis.graph.edges.length} link{archiveAnalysis.graph.edges.length === 1 ? '' : 's'}</span>
                          {archiveAnalysis.baseline_id && <span className="archive-chip">Baseline {archiveAnalysis.baseline_id}</span>}
                        </>
                      )}
                    </div>
                  </div>

                  {archiveAnalysisError && <p className="scenario-script-status error">{archiveAnalysisError}</p>}
                  {archiveAnalysisLoading && <p className="scenario-script-status">Loading archive analysis…</p>}

                  {!archiveAnalysisLoading && archiveAnalysis && archiveAnalysisNodes.length === 0 && archiveAnalysisEdges.length === 0 ? (
                    <p className="scenario-script-status">No archive analysis graph loaded for current filters.</p>
                  ) : null}

                  {archiveAnalysis && (archiveAnalysisNodes.length > 0 || archiveAnalysisEdges.length > 0) && (
                    <div className="archive-analysis-grid">
                      <div className="archive-analysis-panel">
                        <div className="archive-section-header archive-subsection-header">
                          <div>
                            <h3 className="chart-title">Graph Nodes</h3>
                          </div>
                        </div>
                        <table className="tx-table archive-analysis-table" data-testid="archive-analysis-nodes">
                          <thead>
                            <tr>
                              <th>Type</th>
                              <th>Node</th>
                              <th>Current</th>
                              {archiveAnalysisHasBaseline && <th>Baseline</th>}
                            </tr>
                          </thead>
                          <tbody>
                            {archiveAnalysisNodes.slice(0, 10).map(node => {
                              const currentStats = archiveAnalysisCurrentStats(node.runs, test.id)
                              const baselineStats = archiveAnalysisBaselineStats(node.runs, test.id)
                              return (
                                <tr key={node.id}>
                                  <td><span className="archive-analysis-kind">{formatArchiveAnalysisKind(node.kind)}</span></td>
                                  <td className="tx-name">{node.label}</td>
                                  <td>{formatArchiveAnalysisStats(currentStats)}</td>
                                  {archiveAnalysisHasBaseline && <td>{formatArchiveAnalysisStats(baselineStats)}</td>}
                                </tr>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>

                      <div className="archive-analysis-panel">
                        <div className="archive-section-header archive-subsection-header">
                          <div>
                            <h3 className="chart-title">Graph Links</h3>
                          </div>
                        </div>
                        <table className="tx-table archive-analysis-table" data-testid="archive-analysis-edges">
                          <thead>
                            <tr>
                              <th>Link</th>
                              <th>From</th>
                              <th>To</th>
                              <th>Current</th>
                              {archiveAnalysisHasBaseline && <th>Baseline</th>}
                            </tr>
                          </thead>
                          <tbody>
                            {archiveAnalysisEdges.slice(0, 10).map(edge => {
                              const currentStats = archiveAnalysisCurrentStats(edge.runs, test.id)
                              const baselineStats = archiveAnalysisBaselineStats(edge.runs, test.id)
                              const fromNode = archiveAnalysisNodeByID.get(edge.from)
                              const toNode = archiveAnalysisNodeByID.get(edge.to)
                              return (
                                <tr key={edge.id}>
                                  <td><span className="archive-analysis-kind">{formatArchiveAnalysisKind(edge.kind)}</span></td>
                                  <td className="tx-name">{fromNode?.label || edge.from}</td>
                                  <td className="tx-name">{toNode?.label || edge.to}</td>
                                  <td>{formatArchiveAnalysisStats(currentStats)}</td>
                                  {archiveAnalysisHasBaseline && <td>{formatArchiveAnalysisStats(baselineStats)}</td>}
                                </tr>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}

                  {archiveAnalysis && archiveAnalysis.transactions.length > 0 && (
                    <div className="archive-analysis-drilldown">
                      <div className="archive-section-header archive-subsection-header">
                        <div>
                          <h3 className="chart-title">Analysis Drill-Down</h3>
                        </div>
                        <span className="archive-chip">
                          {archiveAnalysis.total > 0 ? `${archiveAnalysis.offset + 1}-${archiveAnalysis.offset + archiveAnalysis.transactions.length} of ${archiveAnalysis.total}` : '0 events'}
                        </span>
                      </div>
                      <div className="archive-events-table-wrap">
                        <table className="tx-table archive-analysis-table" data-testid="archive-analysis-drilldown">
                          <thead>
                            <tr>
                              <th>Time</th>
                              <th>Action</th>
                              <th>Dependency</th>
                              <th>Path</th>
                              <th>Status</th>
                              <th>Duration</th>
                            </tr>
                          </thead>
                          <tbody>
                            {archiveAnalysis.transactions.map(row => (
                              <tr key={`${row.run_id}-${row.index}-${row.timestamp}`} className={!row.success ? 'archive-error-row' : ''}>
                                <td className="archive-time-cell">{formatArchiveTime(row.timestamp)}</td>
                                <td className="tx-name">{row.action_name || row.source_action || 'n/a'}</td>
                                <td>{formatArchiveAnalysisDependency(row)}</td>
                                <td className="archive-analysis-path">{formatArchiveAnalysisPath(row)}</td>
                                <td className={row.success ? '' : 'error-cell'}>{row.status_code || (row.success ? 'OK' : 'ERR')}</td>
                                <td>{row.duration_ms.toFixed(1)} ms</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}
                </div>
              )}
              {showReportAnalysisView && !showArchiveAnalysis && showStandaloneReportSection && (
                <p className="scenario-script-status">Archive analysis is not available for this run.</p>
              )}
            </>
          )}

          {(showActionSummaryView || showTransactionsView) && (
            <div className={`archive-report-layout ${showActionSummaryView && showTransactionsView ? '' : 'single-view'}`}>
              {showActionSummaryView && (
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
                      <td>{formatArchiveCount(stats.transactions)}</td>
                      <td className={(stats.errors || 0) > 0 ? 'error-cell' : ''}>{formatArchiveCount(stats.errors)}</td>
                      <td>{formatArchivePercent(stats.error_pct)}</td>
                      <td>{formatArchiveNumber(stats.avg_response_ms)}</td>
                      <td>{formatArchiveNumber(stats.max_response_ms)}</td>
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
                          <td>{formatArchiveCount(stats.transactions)}</td>
                          <td className={(stats.errors || 0) > 0 ? 'error-cell' : ''}>{formatArchiveCount(stats.errors)}</td>
                          <td>{formatArchivePercent(stats.error_pct)}</td>
                          <td>{formatArchiveNumber(stats.avg_response_ms)}</td>
                          <td>{formatArchiveNumber(stats.max_response_ms)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}

              {regionEntries.length > 0 && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">Region Summary</h3>
                      <p className="archive-section-copy">Archived response time and errors grouped by the generator region that produced each transaction.</p>
                    </div>
                  </div>
                  <table className="tx-table" data-testid="archive-by-region">
                    <thead>
                      <tr>
                        <th>Region</th>
                        <th>Count</th>
                        <th>Errors</th>
                        <th>Error %</th>
                        <th>Avg (ms)</th>
                        <th>Max (ms)</th>
                      </tr>
                    </thead>
                    <tbody>
                      {regionEntries.map(([name, stats]) => (
                        <tr key={name}>
                          <td className="tx-name">{name}</td>
                          <td>{formatArchiveCount(stats.transactions)}</td>
                          <td className={(stats.errors || 0) > 0 ? 'error-cell' : ''}>{formatArchiveCount(stats.errors)}</td>
                          <td>{formatArchivePercent(stats.error_pct)}</td>
                          <td>{formatArchiveNumber(stats.avg_response_ms)}</td>
                          <td>{formatArchiveNumber(stats.max_response_ms)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </>
              )}

              {networkProfileEntries.length > 0 && (
                <>
                  <div className="archive-section-header archive-subsection-header">
                    <div>
                      <h3 className="chart-title">Network Profile Summary</h3>
                      <p className="archive-section-copy">Archived response time and errors grouped by the assigned network condition (population mix).</p>
                    </div>
                  </div>
                  <table className="tx-table" data-testid="archive-by-network-profile">
                    <thead>
                      <tr>
                        <th>Profile</th>
                        <th>Count</th>
                        <th>Errors</th>
                        <th>Error %</th>
                        <th>Avg (ms)</th>
                        <th>Max (ms)</th>
                      </tr>
                    </thead>
                    <tbody>
                      {networkProfileEntries.map(([name, stats]) => (
                        <tr key={name}>
                          <td className="tx-name">{name}</td>
                          <td>{formatArchiveCount(stats.transactions)}</td>
                          <td className={(stats.errors || 0) > 0 ? 'error-cell' : ''}>{formatArchiveCount(stats.errors)}</td>
                          <td>{formatArchivePercent(stats.error_pct)}</td>
                          <td>{formatArchiveNumber(stats.avg_response_ms)}</td>
                          <td>{formatArchiveNumber(stats.max_response_ms)}</td>
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
              )}

              {showTransactionsView && (
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
				<div className="archive-filter-bar archive-dimension-filter-bar" aria-label="Transaction dimensions">
					<input className="archive-column-filter-input" type="search" placeholder="Region" aria-label="Filter transactions by region" value={archiveTransactionFilters.region} onChange={(event) => onArchiveTransactionFilterChange('region', event.target.value)} />
					<input className="archive-column-filter-input" type="search" placeholder="Network profile" aria-label="Filter transactions by network profile" value={archiveTransactionFilters.networkProfile} onChange={(event) => onArchiveTransactionFilterChange('networkProfile', event.target.value)} />
					<input className="archive-column-filter-input" type="search" placeholder="Source transaction" aria-label="Filter transactions by source transaction" value={archiveTransactionFilters.sourceTransaction} onChange={(event) => onArchiveTransactionFilterChange('sourceTransaction', event.target.value)} />
					<input className="archive-column-filter-input" type="search" placeholder="Hierarchy" aria-label="Filter transactions by hierarchy" value={archiveTransactionFilters.hierarchy} onChange={(event) => onArchiveTransactionFilterChange('hierarchy', event.target.value)} />
					<select className="archive-action-select archive-filter-select" aria-label="Filter transactions by phase" value={archiveTransactionFilters.phase} onChange={(event) => onArchiveTransactionFilterChange('phase', event.target.value)}>
						<option value="">All phases</option><option value="init">Init</option><option value="action">Action</option><option value="teardown">Teardown</option>
					</select>
					<select className="archive-action-select archive-filter-select" aria-label="Filter transactions by retained detail" value={archiveTransactionFilters.detail} onChange={(event) => onArchiveTransactionFilterChange('detail', event.target.value)}>
						<option value="">All detail states</option><option value="true">Has detail</option><option value="false">No detail</option>
					</select>
				</div>
              </div>

              {selectedActionStats && (
                <div className="archive-drilldown-stats">
                  <span className="archive-drilldown-stat">{formatArchiveCount(selectedActionStats.transactions)} total</span>
                  <span className="archive-drilldown-stat-sep" />
                  <span className={`archive-drilldown-stat${(selectedActionStats.errors || 0) > 0 ? ' danger' : ''}`}>{formatArchivePercent(selectedActionStats.error_pct)} errors</span>
                  <span className="archive-drilldown-stat-sep" />
                  <span className="archive-drilldown-stat">avg {formatArchiveNumber(selectedActionStats.avg_response_ms)} ms</span>
                  <span className="archive-drilldown-stat-sep" />
                  <span className="archive-drilldown-stat">max {formatArchiveNumber(selectedActionStats.max_response_ms)} ms</span>
                </div>
              )}

              <div className="archive-pagination-bar">
                <span className="archive-pagination-copy">
                  {archiveTransactionsLoading ? 'Loading events…' : archiveTransactionsTotal > 0 ? `${archivePageStart}-${archivePageEnd} of ${archiveTransactionsTotal}` : 'No archived events'}
                </span>
                <div className="archive-pagination-actions">
                  <select
                    className="archive-page-size-select"
                    value={archivePageSize}
                    onChange={(event) => onPageSizeChange(Number(event.target.value))}
                  >
                    {ARCHIVE_PAGE_SIZE_OPTIONS.map(size => (
                      <option key={size} value={size}>{size} / page</option>
                    ))}
                  </select>
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
                <div className="archive-events-table-wrap">
                  <table className="tx-table archive-events-table">
                    <thead>
                      <tr>
                        <th>{renderArchiveSortButton('time', 'Time')}</th>
                        <th>{renderArchiveSortButton('status', 'Status')}</th>
                        <th>{renderArchiveSortButton('duration', 'Duration')}</th>
                        <th>{renderArchiveSortButton('bytes', 'Bytes')}</th>
                        <th>{renderArchiveSortButton('error', 'Error')}</th>
                    <th aria-label="Actions"></th>
                      </tr>
                      <tr className="archive-column-filter-row">
                        <th>
                          <div className="archive-filter-stack archive-filter-stack-wide">
                            <input
                              className="archive-column-filter-input"
                              type="datetime-local"
                              step="1"
                              aria-label="Filter transactions from time"
                              value={archiveTransactionFilters.timeFrom}
                              onChange={(event) => onArchiveTransactionFilterChange('timeFrom', event.target.value)}
                            />
                            <input
                              className="archive-column-filter-input"
                              type="datetime-local"
                              step="1"
                              aria-label="Filter transactions to time"
                              value={archiveTransactionFilters.timeTo}
                              onChange={(event) => onArchiveTransactionFilterChange('timeTo', event.target.value)}
                            />
                          </div>
                        </th>
                        <th>
                          <input
                            className="archive-column-filter-input"
                            type="search"
                            placeholder="Status"
                            aria-label="Filter transactions by status"
                            value={archiveTransactionFilters.status}
                            onChange={(event) => onArchiveTransactionFilterChange('status', event.target.value)}
                          />
                        </th>
                        <th>
                          <div className="archive-filter-stack">
                            <input
                              className="archive-column-filter-input"
                              type="number"
                              min="0"
                              placeholder="Min ms"
                              aria-label="Minimum transaction duration"
                              value={archiveTransactionFilters.minDurationMs}
                              onChange={(event) => onArchiveTransactionFilterChange('minDurationMs', event.target.value)}
                            />
                            <input
                              className="archive-column-filter-input"
                              type="number"
                              min="0"
                              placeholder="Max ms"
                              aria-label="Maximum transaction duration"
                              value={archiveTransactionFilters.maxDurationMs}
                              onChange={(event) => onArchiveTransactionFilterChange('maxDurationMs', event.target.value)}
                            />
                          </div>
                        </th>
                        <th>
                          <div className="archive-filter-stack">
                            <input
                              className="archive-column-filter-input"
                              type="number"
                              min="0"
                              placeholder="Min"
                              aria-label="Minimum received bytes"
                              value={archiveTransactionFilters.minBytes}
                              onChange={(event) => onArchiveTransactionFilterChange('minBytes', event.target.value)}
                            />
                            <input
                              className="archive-column-filter-input"
                              type="number"
                              min="0"
                              placeholder="Max"
                              aria-label="Maximum received bytes"
                              value={archiveTransactionFilters.maxBytes}
                              onChange={(event) => onArchiveTransactionFilterChange('maxBytes', event.target.value)}
                            />
                          </div>
                        </th>
                        <th>
                          <input
                            className="archive-column-filter-input archive-error-filter-input"
                            type="search"
                            placeholder="Contains"
                            aria-label="Filter transactions by error text"
                            value={archiveTransactionFilters.errorContains}
                            onChange={(event) => onArchiveTransactionFilterChange('errorContains', event.target.value)}
                          />
                        </th>
                        <th>
                          <button
                            type="button"
                            className="btn btn-sm btn-secondary archive-clear-filters-button"
                            disabled={!archiveTransactionFiltersActive}
                            onClick={onArchiveTransactionFiltersReset}
                          >
                            Clear
                          </button>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {archiveTransactions.map((event, index) => {
						const rowKey = event.event_id || `${event.timestamp}-${index}`
                        const hasSnapshot = Boolean(event.snapshot)
                        const hasProtocolEvidence = Boolean(event.protocol_evidence)
						const hasInspectDetails = event.has_detail || hasSnapshot || Boolean(event.client_metrics) || Boolean(event.server_telemetry) || hasProtocolEvidence || Boolean(event.network_entries?.length)
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
                            <tr className={!event.success ? 'archive-error-row' : ''}>
                              <td className="archive-time-cell">{formatArchiveTime(event.timestamp)}</td>
                              <td className={event.success ? '' : 'error-cell'}>{event.status_code || (event.success ? 'OK' : 'ERR')}</td>
                              <td>{event.duration_ms.toFixed(1)} ms</td>
                              <td>{event.bytes_received || 0}</td>
                              <td className={!event.success ? 'error-cell' : 'cell-muted'}>{event.error_message || '—'}</td>
                              <td>
                                {hasInspectDetails ? (
                                  <button
                                    className="btn btn-sm btn-secondary archive-detail-toggle"
                                    onClick={() => onExpandedArchiveEventChange(isExpanded ? null : rowKey)}
                                  >
                                    {isExpanded ? 'Hide' : 'Inspect'}
                                  </button>
                                ) : null}
                              </td>
                            </tr>
                            {isExpanded && hasInspectDetails && (
                              <tr className="archive-event-detail-row">
                                <td colSpan={6}>
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
                                          {copyFailedSection === `${rowKey}:soap-fault` ? 'Copy failed' : copiedSection === `${rowKey}:soap-fault` ? 'Copied' : 'Copy fault'}
                                        </button>
                                      </div>
                                      <span className="scenario-script-detail-value">SOAP {soapFault.version}</span>
                                      <pre className="archive-event-detail-pre">Code: {soapFault.code}{'\n'}Reason: {soapFault.reason}</pre>
                                    </div>
                                  )}
									{(event.network_entries?.length || event.dns_ms || event.connect_ms || event.tls_ms || event.ttfb_ms) ? (
										<div className="archive-event-detail-card archive-waterfall-card">
											<div className="archive-event-detail-header"><span className="scenario-script-detail-label">Network Waterfall</span></div>
											<TransactionNetworkWaterfall event={event} />
										</div>
									) : null}
                                  {event.client_metrics && (
                                    <div className="archive-event-detail-card">
                                      <div className="archive-event-detail-header">
                                        <span className="scenario-script-detail-label">Client Metrics</span>
                                      </div>
                                      <span className="scenario-script-detail-value">{event.client_metrics.device_profile || 'browser'} · {formatDevicePlatformLabel(event.client_metrics.device_platform)} · {formatBrowserEngineLabel(event.client_metrics.browser_engine)} · {event.client_metrics.device_tier || 'unknown'} tier</span>
                                      <pre className="archive-event-detail-pre">{formatClientMetricsBlock(event.client_metrics)}</pre>
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
                                      <SnapshotTextPreview
                                        text={formatServerTelemetryBlock(event.server_telemetry)}
                                        preClassName="archive-event-detail-pre"
                                      />
                                    </div>
                                  )}
                                  {event.protocol_evidence && (
                                    <div className="archive-event-detail-card">
                                      <div className="archive-event-detail-header">
                                        <span className="scenario-script-detail-label">Protocol Evidence</span>
                                      </div>
                                      <span className="scenario-script-detail-value">{formatProtocolEvidenceSummary(event.protocol_evidence)}</span>
                                      <SnapshotBodyPreview
                                        body={JSON.stringify(event.protocol_evidence, null, 2)}
                                        headers={{ 'Content-Type': 'application/json' }}
                                        emptyMessage="No protocol evidence captured"
                                        preClassName="archive-event-detail-pre"
                                      />
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
                                            {copyFailedSection === `${rowKey}:request` ? 'Copy failed' : copiedSection === `${rowKey}:request` ? 'Copied' : 'Copy full request'}
                                          </button>
                                        </div>
                                        <span className="scenario-script-detail-label">Request line and headers</span>
                                        <SnapshotTextPreview
                                          text={formatSnapshotRequestLineAndHeaders(event.snapshot)}
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('request-headers')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('request-headers'), expanded)}
                                        />
                                        <span className="scenario-script-detail-label">Request body</span>
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
                                            {copyFailedSection === `${rowKey}:response` ? 'Copy failed' : copiedSection === `${rowKey}:response` ? 'Copied' : 'Copy full response'}
                                          </button>
                                        </div>
                                        <span className="scenario-script-detail-label">Response status and headers</span>
                                        <SnapshotTextPreview
                                          text={formatSnapshotResponseStatusAndHeaders(event.snapshot, event.status_code)}
                                          preClassName="archive-event-detail-pre"
                                          expanded={expandedSnapshotSections[snapshotSectionKey('response-headers')]}
                                          onExpandedChange={(expanded) => toggleSnapshotSection(snapshotSectionKey('response-headers'), expanded)}
                                        />
                                        <span className="scenario-script-detail-label">Response body</span>
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
                </div>
              )}
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
