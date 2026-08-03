import { useState, useEffect, useLayoutEffect, useMemo, useRef, useCallback, type CSSProperties } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { useApi } from '../hooks/useApi'
import { useApplication } from '../hooks/useApplication'
import { useProject } from '../hooks/useProject'
import { TestSummary, MetricsHistoryPoint, Release, RunArchiveReport, GroupLiveMetrics } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { isCompletedTestStatus, isLiveTestStatus, isPendingTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { SortIndicator as SortIcon } from './SortIndicator'
import { LoadComparisonPanel, type LoadComparisonTestOption } from './LoadComparisonPanel'
import { ReleaseTrendPanel } from './ReleaseTrendPanel'
import { chartTooltipContentStyle } from './chartTooltipTheme'
import { type NavPage } from './Sidebar'
import StatusBadge from './StatusBadge'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useSharedTestList } from '../hooks/useSharedTestList'
import { DEFAULT_TEST_CATALOG_PAGE_SIZE, loadTestSummaryTail } from './testCatalogPagination'
import './DashboardPage.css'

interface Props {
  testStatuses: Record<string, string>
  metricsHistory: Record<string, MetricsHistoryPoint[]>
  connectionState: WebSocketConnectionState
  onSelectTest: (id: string) => void
  onOpenTestArchive?: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onNavigate: (page: NavPage) => void
  snapshotAudience?: DashboardSnapshotAudience
}

interface HealthData {
  status: string
  version: string
  ws_clients: number
  test_count: number
  capacity_availability?: {
    effective_available_generators?: number
    configured_available_generators?: number
    source?: string
    warnings?: string[]
  }
}

type CardMetricKey = 'tps' | 'avg_response_ms' | 'error_pct' | 'active_vusers'
type DashboardTabKey = 'activity' | 'active' | 'performance' | 'releaseTrend'
type SortDirection = 'asc' | 'desc'
type PerformanceSortField = 'run' | 'completed' | 'peak_tps' | 'avg_ms' | 'errors' | 'sla' | 'complete'
type ActivitySortField = 'run' | 'status' | 'vusers' | 'duration' | 'transactions' | 'date'
export type DashboardSnapshotAudience = 'admin' | 'user'

interface AttentionItem {
  tone: 'danger' | 'warning' | 'info'
  title: string
  detail: string
  testId?: string
}

interface ActiveGroupMetricRow {
  name: string
  vusers: number
  tps?: number
  avgResponseMs?: number
  errorPct?: number
  passed?: number
  failed?: number
  hasLiveMetrics: boolean
}

interface PerformanceTrendEntry {
  test: TestSummary
  report: RunArchiveReport
  runtimeSummary: string | null
}

interface ReleaseTrendStats {
  slaBreaches: number
  avgCompleteness: number
  peakVUsers: number
  maxCPU: number
  maxMemoryMB: number
  telemetryBreached: number
}

interface SnapshotStatProps {
  label: string
  value: string | number
  tone?: 'neutral' | 'accent' | 'success' | 'warning' | 'danger'
  onClick?: () => void
}

interface ReleaseSnapshotPanelProps {
  open: boolean
  audience: DashboardSnapshotAudience
  activeRelease: Release | null | undefined
  releaseOptions: Release[]
  releaseStats: {
    completed: number
    failed: number
    aborted: number
    archived: number
    totalTx: number
  }
  dashboardStats: {
    completed: number
    aborted: number
    active: number
    activeTps: number
    avgError: number
  }
  latestReleaseRun: TestSummary | undefined
  releaseTrendStats: ReleaseTrendStats
  health: HealthData | null
  statusLabel: string
  statusColor: string
  activeTests: TestSummary[]
  pendingTests: TestSummary[]
  attentionItems: AttentionItem[]
  testDataReady: boolean
  onToggle: () => void
  onReleaseChange: (release: Release) => void
  onLatestResult: () => void
  onPrimaryActiveRun: () => void
  onPendingRun: (id?: string) => void
  onResults: () => void
  onMonitoring: () => void
  onNewTest: () => void
  onSelectTest: (id: string) => void
}

interface DashboardTestListState {
  scopeReady: boolean
  ready: boolean
  loading: boolean
  refreshing: boolean
  error: string
  onRefresh: () => Promise<void>
}

const ARCHIVE_REPORT_BATCH_LIMIT = 50
const EMPTY_DASHBOARD_TESTS: TestSummary[] = []

const tableBottomGutterPx = 24
const tableMinimumViewportHeightPx = 80

const graphMetricOptions: { key: CardMetricKey; label: string; color: string; digits: number }[] = [
  { key: 'tps', label: 'TPS', color: 'var(--accent)', digits: 1 },
  { key: 'avg_response_ms', label: 'Avg ms', color: 'var(--chart-2)', digits: 0 },
  { key: 'error_pct', label: 'Error %', color: 'var(--danger)', digits: 2 },
  { key: 'active_vusers', label: 'Active VUsers', color: 'var(--chart-3)', digits: 0 },
]

const dashboardTabs: { id: DashboardTabKey; label: string }[] = [
  { id: 'activity', label: 'Recent Activity' },
  { id: 'active', label: 'Active Tests' },
  { id: 'performance', label: 'Performance Trend' },
  { id: 'releaseTrend', label: 'Release Trend Analysis' },
]

const performanceSortDefaults: Record<PerformanceSortField, SortDirection> = {
  run: 'asc',
  completed: 'desc',
  peak_tps: 'desc',
  avg_ms: 'desc',
  errors: 'desc',
  sla: 'desc',
  complete: 'desc',
}

const activitySortDefaults: Record<ActivitySortField, SortDirection> = {
  run: 'asc',
  status: 'asc',
  vusers: 'desc',
  duration: 'desc',
  transactions: 'desc',
  date: 'desc',
}

function formatNumber(value: number, digits = 0) {
  return Number.isFinite(value) ? value.toFixed(digits) : digits > 0 ? '0.0' : '0'
}

function orderDashboardTestCatalog(tests: TestSummary[]) {
  return [...tests].sort((left, right) => {
    const leftTime = Date.parse(left.created_at || '')
    const rightTime = Date.parse(right.created_at || '')
    const normalizedLeftTime = Number.isFinite(leftTime) ? leftTime : 0
    const normalizedRightTime = Number.isFinite(rightTime) ? rightTime : 0
    if (normalizedLeftTime !== normalizedRightTime) return normalizedRightTime - normalizedLeftTime
    return left.id.localeCompare(right.id)
  })
}

function formatOptionalNumber(value?: number, digits = 0) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(digits) : '—'
}

function formatOptionalPercent(value?: number, digits = 2) {
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(digits)}%` : '—'
}

function formatOptionalCount(value?: number) {
  return typeof value === 'number' && Number.isFinite(value) ? Math.round(value).toLocaleString() : '—'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never'
}

function finiteMetric(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function groupErrorPct(metric?: GroupLiveMetrics): number | undefined {
  if (!metric) return undefined
  const passed = finiteMetric(metric.transactions) ?? 0
  const failed = finiteMetric(metric.errors) ?? 0
  const attempts = passed + failed
  return attempts > 0 ? (failed / attempts) * 100 : 0
}

function buildActiveGroupMetricRows(test: TestSummary): ActiveGroupMetricRow[] {
  const liveGroups = test.live_metrics?.groups ?? {}
  const configuredGroups = test.groups || []
  const configuredNames = new Set<string>()

  const rowForGroup = (name: string, metric?: GroupLiveMetrics, configuredVusers = 0): ActiveGroupMetricRow => ({
    name,
    vusers: finiteMetric(metric?.vusers) ?? configuredVusers,
    tps: finiteMetric(metric?.tps),
    avgResponseMs: finiteMetric(metric?.avg_response_ms),
    errorPct: groupErrorPct(metric),
    passed: metric ? finiteMetric(metric.transactions) ?? 0 : undefined,
    failed: metric ? finiteMetric(metric.errors) ?? 0 : undefined,
    hasLiveMetrics: Boolean(metric),
  })

  const configuredRows = configuredGroups.map((group, index) => {
    const name = (group.name || group.script_name || group.script_id || `Group ${index + 1}`).trim()
    configuredNames.add(name)
    return rowForGroup(name, liveGroups[name], group.vusers)
  })

  const liveOnlyRows = Object.keys(liveGroups)
    .filter(name => name && !configuredNames.has(name))
    .sort((left, right) => left.localeCompare(right))
    .map(name => rowForGroup(name, liveGroups[name]))

  return [...configuredRows, ...liveOnlyRows]
}

function completionTime(test: TestSummary) {
  return new Date(test.ended_at || test.start_time || test.created_at || 0).getTime()
}

function releaseTime(release: Release) {
  return new Date(release.updated_at || release.created_at || 0).getTime()
}

function parseDurationValue(value?: string) {
  if (!value) return 0
  const match = value.trim().match(/^(\d+(?:\.\d+)?)(ms|s|m|h)?$/i)
  if (!match) return 0
  const amount = Number(match[1])
  const unit = (match[2] || 's').toLowerCase()
  if (!Number.isFinite(amount)) return 0
  if (unit === 'h') return amount * 3600
  if (unit === 'm') return amount * 60
  if (unit === 'ms') return amount / 1000
  return amount
}

function compareValues(left: string | number, right: string | number, direction: SortDirection) {
  const multiplier = direction === 'asc' ? 1 : -1
  if (typeof left === 'string' || typeof right === 'string') {
    return String(left).localeCompare(String(right)) * multiplier
  }
  if (left < right) return -1 * multiplier
  if (left > right) return 1 * multiplier
  return 0
}

function setEquals(left: Set<string>, right: Set<string>): boolean {
  if (left.size !== right.size) return false
  for (const value of left) {
    if (!right.has(value)) return false
  }
  return true
}

function deriveSelectedIds(current: Set<string>, previousIds: string[], validIds: string[]): Set<string> {
  if (validIds.length === 0) return new Set()
  const valid = new Set(validIds)
  const retained = new Set(Array.from(current).filter(id => valid.has(id)))
  const hadNoPreviousIds = previousIds.length === 0
  const hadAllPreviousIds = previousIds.length > 0 && previousIds.every(id => current.has(id))
  const hasOverlap = previousIds.some(id => valid.has(id))
  return hadNoPreviousIds || hadAllPreviousIds || !hasOverlap ? valid : retained
}

function buildReleaseTrendStats(entries: PerformanceTrendEntry[]): ReleaseTrendStats {
  if (entries.length === 0) {
    return {
      slaBreaches: 0,
      avgCompleteness: 0,
      peakVUsers: 0,
      maxCPU: 0,
      maxMemoryMB: 0,
      telemetryBreached: 0,
    }
  }
  const completenessScores = entries
    .map(entry => entry.report.completeness?.score_pct)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  const avgCompleteness = completenessScores.length
    ? completenessScores.reduce((sum, value) => sum + value, 0) / completenessScores.length
    : 0
  return {
    slaBreaches: entries.filter(entry => entry.report.manifest?.sla_breached).length,
    avgCompleteness,
    peakVUsers: Math.max(...entries.map(entry => entry.report.overview.peak_active_vusers || 0)),
    maxCPU: Math.max(...entries.map(entry => entry.report.resource_peaks?.cpu_process_pct || 0)),
    maxMemoryMB: Math.max(...entries.map(entry => entry.report.resource_peaks?.mem_process_mb || 0)),
    telemetryBreached: entries.filter(entry => entry.report.overview.telemetry?.status === 'breached').length,
  }
}

function getHistoryMetric(point: MetricsHistoryPoint, metric: CardMetricKey) {
  switch (metric) {
    case 'avg_response_ms':
      return Number(point.avgMs || 0)
    case 'error_pct':
      return Number(point.errorPct || 0)
    case 'active_vusers':
      return Number(point.vusers || 0)
    case 'tps':
    default:
      return Number(point.tps || 0)
  }
}

function useBottomBoundedTable(dependencySignature: string) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [maxHeight, setMaxHeight] = useState<number | null>(null)

  useLayoutEffect(() => {
    if (typeof window === 'undefined') return

    const updateMaxHeight = () => {
      const node = ref.current
      if (!node) return
      const rect = node.getBoundingClientRect()
      const availableHeight = Math.floor(window.innerHeight - rect.top - tableBottomGutterPx)
      const nextMaxHeight = Math.max(tableMinimumViewportHeightPx, availableHeight)
      setMaxHeight(current => current === nextMaxHeight ? current : nextMaxHeight)
    }

    updateMaxHeight()
    window.addEventListener('resize', updateMaxHeight)
    window.addEventListener('scroll', updateMaxHeight, { passive: true })

    return () => {
      window.removeEventListener('resize', updateMaxHeight)
      window.removeEventListener('scroll', updateMaxHeight)
    }
  }, [dependencySignature])

  const style = maxHeight === null ? undefined : ({ '--dashboard-table-max-height': `${maxHeight}px` } as CSSProperties)
  return { ref, style }
}

function SnapshotStat({ label, value, tone = 'neutral', onClick }: SnapshotStatProps) {
  const className = `release-panel-stat tone-${tone}${onClick ? ' is-actionable' : ''}`
  const content = (
    <>
      <span className="release-panel-stat-value">{value}</span>
      <span className="release-panel-stat-label">{label}</span>
    </>
  )

  if (onClick) {
    return (
      <button type="button" className={className} onClick={onClick}>
        {content}
      </button>
    )
  }

  return <div className={className}>{content}</div>
}

function DashboardTestListStatus({ scopeReady, ready, loading, refreshing, error, onRefresh }: DashboardTestListState) {
  if (!scopeReady) {
    return (
      <div className="dash-empty small" role="status">
        <span>Select a project and application to load dashboard runs.</span>
      </div>
    )
  }

  if (!ready) {
    return (
      <div className="dash-empty small" role={error ? 'alert' : 'status'} aria-live={error ? 'assertive' : 'polite'}>
        <span>{error ? `Unable to load dashboard runs. ${error}` : 'Loading dashboard runs…'}</span>
        {error && (
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void onRefresh() }}>
            Retry
          </button>
        )}
      </div>
    )
  }

  if (error) {
    return (
      <div className="dash-empty small" role="alert">
        <span>{`Dashboard run data may be stale. ${error}`}</span>
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => { void onRefresh() }}>
          Retry
        </button>
      </div>
    )
  }

  if (refreshing || loading) {
    return (
      <div className="dash-empty small" role="status" aria-live="polite">
        <span>Refreshing dashboard runs…</span>
      </div>
    )
  }

  return null
}

function ReleaseSnapshotPanel({
  open,
  audience,
  activeRelease,
  releaseOptions,
  releaseStats,
  dashboardStats,
  latestReleaseRun,
  releaseTrendStats,
  health,
  statusLabel,
  statusColor,
  activeTests,
  pendingTests,
  attentionItems,
  testDataReady,
  onToggle,
  onReleaseChange,
  onLatestResult,
  onPrimaryActiveRun,
  onPendingRun,
  onResults,
  onMonitoring,
  onNewTest,
  onSelectTest,
}: ReleaseSnapshotPanelProps) {
  const panelId = 'dashboard-release-snapshot-panel'
  const completedRunCount = releaseStats.completed + releaseStats.failed + releaseStats.aborted
  const archivesReady = `${releaseStats.archived}/${completedRunCount}`
  const generatorCapacity = health?.capacity_availability
  const effectiveGenerators = generatorCapacity?.effective_available_generators
  const configuredGenerators = generatorCapacity?.configured_available_generators ?? effectiveGenerators
  const generatorAvailability = typeof effectiveGenerators === 'number'
    ? `${effectiveGenerators}/${configuredGenerators ?? effectiveGenerators}`
    : '—'
  const releaseName = activeRelease?.name || 'All releases'
  const panelModeLabel = audience === 'admin' ? 'Operations Snapshot' : 'Release Quality'
  const latestStatus = latestReleaseRun ? `${latestReleaseRun.name} · ${latestReleaseRun.status}` : 'No completed runs'
  const qualityTone = releaseStats.failed > 0 || releaseTrendStats.slaBreaches > 0 ? 'warning' : 'success'
  const visibleAttentionItems = attentionItems.slice(0, 3)

  return (
    <aside className={`dash-release-panel ${open ? 'is-open' : 'is-collapsed'}`} aria-label="Release snapshot panel">
      <button
        type="button"
        className="release-panel-rail"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={onToggle}
        hidden={open}
      >
        <span className="release-panel-rail-kicker">Snapshot</span>
        <strong>{releaseName}</strong>
        <span>Open</span>
      </button>

      <section id={panelId} className="release-panel-card" hidden={!open}>
        <div className="release-panel-header">
          <div>
            <span className="release-panel-eyebrow">{panelModeLabel}</span>
            <h3>Release Snapshot</h3>
            <p>{releaseName}</p>
          </div>
          <button
            type="button"
            className="release-panel-toggle"
            aria-expanded={open}
            aria-controls={panelId}
            onClick={onToggle}
          >
            Collapse
          </button>
        </div>

        <div className="dash-release-selector release-panel-selector" role="list" aria-label="Dashboard release selector">
          {releaseOptions.length === 0 ? (
            <span className="dash-release-selector-empty">No releases</span>
          ) : releaseOptions.map(release => (
            <button
              key={release.id}
              type="button"
              className={`dash-release-option ${activeRelease?.id === release.id ? 'active' : ''}`}
              aria-current={activeRelease?.id === release.id ? 'true' : undefined}
              onClick={() => onReleaseChange(release)}
            >
              {release.name}
            </button>
          ))}
        </div>

        {testDataReady && (
          <div className="release-panel-stat-grid shared">
            <SnapshotStat label="Latest Result" value={latestReleaseRun ? latestReleaseRun.name : 'No runs'} tone="accent" onClick={onLatestResult} />
            <SnapshotStat label="Failed Runs" value={releaseStats.failed} tone={releaseStats.failed > 0 ? 'danger' : 'success'} onClick={onResults} />
            <SnapshotStat label="Archives Ready" value={archivesReady} onClick={onResults} />
            <SnapshotStat label="Archived Transactions" value={releaseStats.totalTx.toLocaleString()} onClick={onResults} />
            <SnapshotStat label="Current TPS" value={formatNumber(dashboardStats.activeTps, 1)} onClick={onPrimaryActiveRun} />
            <SnapshotStat label="Current Error Rate" value={`${formatNumber(dashboardStats.avgError, 2)}%`} tone={dashboardStats.avgError > 0 ? 'danger' : 'neutral'} onClick={onPrimaryActiveRun} />
          </div>
        )}

        {testDataReady && (
          <div className="release-panel-status-strip" aria-label="Run status summary">
            <SnapshotStat label="Completed" value={dashboardStats.completed} tone="success" onClick={onResults} />
            <SnapshotStat label="Aborted" value={dashboardStats.aborted} tone="warning" onClick={onResults} />
            <SnapshotStat label="Active Now" value={dashboardStats.active} tone="accent" onClick={onPrimaryActiveRun} />
          </div>
        )}

        {testDataReady && (
          audience === 'admin' ? (
            <div className="release-panel-section" aria-label="Operations activity">
              <div className="release-panel-section-header">
                <h4>Operations Activity</h4>
                <span>{activeTests.length} live · {pendingTests.length} queued</span>
              </div>
              <div className="release-panel-detail-grid">
                <SnapshotStat label="API Health" value={statusLabel} tone={statusColor === 'healthy' ? 'success' : statusColor === 'offline' ? 'danger' : 'warning'} onClick={onMonitoring} />
                <SnapshotStat label="Generators Ready" value={generatorAvailability} onClick={onMonitoring} />
                <SnapshotStat label="WebSocket Clients" value={health?.ws_clients ?? 0} />
                <SnapshotStat label="Pending Runs" value={pendingTests.length} tone={pendingTests.length > 0 ? 'warning' : 'neutral'} onClick={() => onPendingRun(pendingTests[0]?.id)} />
              </div>
              <div className="release-panel-feed">
                {visibleAttentionItems.length === 0 ? (
                  <div className="release-panel-feed-empty">No active attention items for this release.</div>
                ) : visibleAttentionItems.map((item, index) => (
                  <button
                    type="button"
                    key={`attention-${index}-${item.testId ?? item.title}`}
                    className={`release-panel-feed-item tone-${item.tone}`}
                    onClick={() => item.testId
                      ? pendingTests.some(test => test.id === item.testId) ? onPendingRun(item.testId) : onSelectTest(item.testId)
                      : onMonitoring()}
                  >
                    <strong>{item.title}</strong>
                    <span>{item.detail}</span>
                  </button>
                ))}
              </div>
              <div className="release-panel-actions" aria-label="Release operations links">
                <button type="button" className="btn btn-secondary btn-sm" onClick={onMonitoring}>Monitoring</button>
                <button type="button" className="btn btn-secondary btn-sm" onClick={onNewTest}>New Test</button>
                <button type="button" className="btn btn-secondary btn-sm" onClick={onResults}>Results</button>
              </div>
            </div>
          ) : (
            <div className="release-panel-section" aria-label="Release quality">
              <div className="release-panel-section-header">
                <h4>Release Quality</h4>
                <span>{latestReleaseRun ? latestReleaseRun.status : 'No result'}</span>
              </div>
              <div className="release-panel-detail-grid">
                <SnapshotStat label="SLA Breaches" value={releaseTrendStats.slaBreaches} tone={releaseTrendStats.slaBreaches > 0 ? 'danger' : 'success'} onClick={onResults} />
                <SnapshotStat label="Avg Completeness" value={`${formatNumber(releaseTrendStats.avgCompleteness, 0)}%`} tone={qualityTone} onClick={onResults} />
                <SnapshotStat label="Peak VUsers" value={releaseTrendStats.peakVUsers.toLocaleString()} />
                <SnapshotStat label="Telemetry Breaches" value={releaseTrendStats.telemetryBreached} tone={releaseTrendStats.telemetryBreached > 0 ? 'warning' : 'success'} onClick={onResults} />
                <SnapshotStat label="Max CPU" value={`${formatNumber(releaseTrendStats.maxCPU, 1)}%`} />
                <SnapshotStat label="Max Memory" value={`${formatNumber(releaseTrendStats.maxMemoryMB, 0)} MB`} />
              </div>
              <button type="button" className="release-panel-quality-callout dash-card-button" onClick={onLatestResult}>
                <span>Latest Outcome</span>
                <strong>{latestStatus}</strong>
              </button>
              <div className="release-panel-actions" aria-label="Release quality links">
                <button type="button" className="btn btn-secondary btn-sm" onClick={onResults}>View Results</button>
              </div>
            </div>
          )
        )}
      </section>
    </aside>
  )
}

export default function DashboardPage({ testStatuses, metricsHistory, connectionState, onSelectTest, onOpenTestArchive, onOpenCommandCenter, onNavigate, snapshotAudience = 'admin' }: Props) {
  const api = useApi()
  const {
    activeApplication,
    activeRelease,
    releases,
    releasesLoading,
    releasesError,
    setActiveRelease,
    refreshReleases,
  } = useApplication()
  const { activeProject } = useProject()
  const activeApplicationId = activeApplication?.id
  const activeProjectId = activeProject?.id
  const dashboardScopeReady = Boolean(activeProjectId && activeApplicationId && !releasesLoading && !releasesError)
  const testListParams = useMemo(() => dashboardScopeReady ? {
    project_id: activeProjectId,
    application_id: activeApplicationId,
    summary: true,
    limit: DEFAULT_TEST_CATALOG_PAGE_SIZE,
    offset: 0,
  } : null, [activeApplicationId, activeProjectId, dashboardScopeReady])
  const {
    tests: firstDashboardTestPage,
    total: dashboardTestTotal,
    error: testListError,
    loading: testListLoading,
    refreshing: testListRefreshing,
    updatedAt: testListUpdatedAt,
    refresh: refreshTestList,
  } = useSharedTestList(testListParams, { loader: api.listTests, pollIntervalMs: 3000 })
  const dashboardCatalogScopeKey = useMemo(() => JSON.stringify(testListParams || {}), [testListParams])
  const dashboardCatalogScopeKeyRef = useRef(dashboardCatalogScopeKey)
  dashboardCatalogScopeKeyRef.current = dashboardCatalogScopeKey
  const [dashboardCatalog, setDashboardCatalog] = useState<{
    scopeKey: string
    tests: TestSummary[]
    total: number
  }>({ scopeKey: '', tests: [], total: 0 })
  const dashboardCatalogRef = useRef(dashboardCatalog)
  const dashboardTailGenerationRef = useRef(0)
  const dashboardTailScopeRef = useRef('')
  const [dashboardTailLoading, setDashboardTailLoading] = useState(false)
  const [dashboardTailError, setDashboardTailError] = useState('')
  const commitDashboardCatalog = useCallback((next: typeof dashboardCatalog) => {
    dashboardCatalogRef.current = next
    setDashboardCatalog(next)
  }, [])

  useEffect(() => {
    dashboardTailGenerationRef.current += 1
    dashboardTailScopeRef.current = ''
    commitDashboardCatalog({ scopeKey: dashboardCatalogScopeKey, tests: [], total: 0 })
    setDashboardTailLoading(false)
    setDashboardTailError('')
  }, [commitDashboardCatalog, dashboardCatalogScopeKey])

  useEffect(() => () => {
    dashboardTailGenerationRef.current += 1
  }, [])

  useEffect(() => {
    if (!testListParams || testListUpdatedAt === null) return
    const current = dashboardCatalogRef.current
    const shouldReset = current.scopeKey !== dashboardCatalogScopeKey || dashboardTestTotal < current.total
    const testsByID = new Map<string, TestSummary>()
    if (!shouldReset) current.tests.forEach(test => testsByID.set(test.id, test))
    firstDashboardTestPage.forEach(test => testsByID.set(test.id, test))
    if (testsByID.size > dashboardTestTotal) {
      testsByID.clear()
      firstDashboardTestPage.forEach(test => testsByID.set(test.id, test))
    }
    const next = {
      scopeKey: dashboardCatalogScopeKey,
      tests: orderDashboardTestCatalog(Array.from(testsByID.values())),
      total: dashboardTestTotal,
    }
    commitDashboardCatalog(next)
    if (next.tests.length >= dashboardTestTotal || dashboardTailScopeRef.current === dashboardCatalogScopeKey) {
      if (next.tests.length >= dashboardTestTotal) {
        setDashboardTailLoading(false)
        setDashboardTailError('')
      }
      return
    }

    const requestGeneration = ++dashboardTailGenerationRef.current
    dashboardTailScopeRef.current = dashboardCatalogScopeKey
    setDashboardTailLoading(true)
    setDashboardTailError('')
    void loadTestSummaryTail(api.listTests, testListParams, {
      startOffset: firstDashboardTestPage.length,
      total: dashboardTestTotal,
      seenTestIds: firstDashboardTestPage.map(test => test.id),
      onPage: (pageTests, total) => {
        if (
          requestGeneration !== dashboardTailGenerationRef.current
          || dashboardCatalogScopeKeyRef.current !== dashboardCatalogScopeKey
        ) return
        const latest = dashboardCatalogRef.current
        const merged = new Map(latest.tests.map(test => [test.id, test]))
        pageTests.forEach(test => merged.set(test.id, test))
        commitDashboardCatalog({
          scopeKey: dashboardCatalogScopeKey,
          tests: orderDashboardTestCatalog(Array.from(merged.values())),
          total,
        })
      },
    }).catch(error => {
      if (
        requestGeneration === dashboardTailGenerationRef.current
        && dashboardCatalogScopeKeyRef.current === dashboardCatalogScopeKey
      ) setDashboardTailError(getErrorMessage(error, 'Unable to load older dashboard runs'))
    }).finally(() => {
      if (
        requestGeneration === dashboardTailGenerationRef.current
        && dashboardCatalogScopeKeyRef.current === dashboardCatalogScopeKey
      ) {
        dashboardTailScopeRef.current = ''
        setDashboardTailLoading(false)
      }
    })
  }, [api.listTests, commitDashboardCatalog, dashboardCatalogScopeKey, dashboardTestTotal, firstDashboardTestPage, testListParams, testListUpdatedAt])

  const tests = dashboardCatalog.scopeKey === dashboardCatalogScopeKey ? dashboardCatalog.tests : EMPTY_DASHBOARD_TESTS
  const dashboardCatalogReady = testListUpdatedAt !== null
    && dashboardCatalog.scopeKey === dashboardCatalogScopeKey
    && tests.length >= dashboardTestTotal
    && !dashboardTailLoading
  const refreshDashboardCatalog = useCallback(async () => {
    dashboardTailGenerationRef.current += 1
    dashboardTailScopeRef.current = ''
    commitDashboardCatalog({ scopeKey: dashboardCatalogScopeKey, tests: [], total: 0 })
    setDashboardTailLoading(false)
    setDashboardTailError('')
    await refreshTestList()
  }, [commitDashboardCatalog, dashboardCatalogScopeKey, refreshTestList])
  const [health, setHealth] = useState<HealthData | null>(null)
  const [archiveReports, setArchiveReports] = useState<Record<string, RunArchiveReport | null>>({})
  const [releasePanelOpen, setReleasePanelOpen] = useState(true)
  const [activeDashboardTab, setActiveDashboardTab] = useState<DashboardTabKey>('activity')
  const [performanceSort, setPerformanceSort] = useState<{ field: PerformanceSortField; direction: SortDirection }>({ field: 'completed', direction: 'desc' })
  const [activitySort, setActivitySort] = useState<{ field: ActivitySortField; direction: SortDirection }>({ field: 'date', direction: 'desc' })
  const [trendDownloadError, setTrendDownloadError] = useState('')
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [selectedPerformanceTestIds, setSelectedPerformanceTestIds] = useState<Set<string>>(new Set())
  const performanceSelectAllRef = useRef<HTMLInputElement | null>(null)
  const previousPerformanceTestIdsRef = useRef<string[]>([])

  useLayoutEffect(() => {
    setArchiveReports({})
    setSelectedPerformanceTestIds(new Set())
    previousPerformanceTestIdsRef.current = []
  }, [activeApplicationId, activeProjectId])

  const testListState = useMemo<DashboardTestListState>(() => ({
    scopeReady: dashboardScopeReady,
    ready: dashboardCatalogReady,
    loading: testListLoading || dashboardTailLoading,
    refreshing: testListRefreshing || dashboardTailLoading,
    error: testListError || dashboardTailError,
    onRefresh: refreshDashboardCatalog,
  }), [dashboardCatalogReady, dashboardScopeReady, dashboardTailError, dashboardTailLoading, refreshDashboardCatalog, testListError, testListLoading, testListRefreshing])

  useEffect(() => {
    if (!dashboardScopeReady) {
      return
    }

    let cancelled = false
    let inFlight = false
    const load = async () => {
      if (inFlight) {
        return
      }
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      inFlight = true
      try {
        const healthRes = await api.getHealth()
        if (cancelled) return
        setHealth(healthRes)
      } catch { /* ignore */ } finally {
        inFlight = false
      }
    }

    load()
    const interval = setInterval(load, 10000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        load()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      cancelled = true
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api, activeApplicationId, activeProjectId, dashboardScopeReady])

  const resolvedTests = useMemo(() =>
    tests.map(t => ({
      ...t,
      status: resolveTestStatus(t, testStatuses) as TestSummary['status'],
    })),
    [tests, testStatuses],
  )

  const activeTests = useMemo(() =>
    resolvedTests.filter(t => isLiveTestStatus(t.status)),
    [resolvedTests],
  )

  const pendingTests = useMemo(() =>
    resolvedTests
      .filter(t => isPendingTestStatus(t.status))
      .sort((a, b) => {
        const aPosition = a.queue_position ?? Number.MAX_SAFE_INTEGER
        const bPosition = b.queue_position ?? Number.MAX_SAFE_INTEGER
        if (aPosition !== bPosition) return aPosition - bPosition
        return Date.parse(a.pending_since || a.created_at || '') - Date.parse(b.pending_since || b.created_at || '')
      }),
    [resolvedTests],
  )

  const recentTests = useMemo(() =>
    resolvedTests
      .filter(t => isCompletedTestStatus(t.status))
      .sort((a, b) => completionTime(b) - completionTime(a)),
    [resolvedTests],
  )

  const dashboardReleaseOptions = useMemo(() => {
    const byId = new Map<string, Release>()
    if (activeRelease && !activeRelease.archived_at) {
      byId.set(activeRelease.id, activeRelease)
    }
    releases.forEach(release => {
      if (!release.archived_at) {
        byId.set(release.id, release)
      }
    })

    const sorted = Array.from(byId.values()).sort((left, right) => releaseTime(right) - releaseTime(left))
    const visible = sorted.slice(0, 6)
    if (activeRelease && !activeRelease.archived_at && byId.has(activeRelease.id) && !visible.some(release => release.id === activeRelease.id)) {
      return [...visible.slice(0, 5), byId.get(activeRelease.id)!]
    }
    return visible
  }, [activeRelease, releases])

  const releaseTests = useMemo(() => {
    if (!activeRelease?.id) return resolvedTests
    return resolvedTests.filter(t => t.release_id === activeRelease.id)
  }, [resolvedTests, activeRelease?.id])

  const releaseCompleted = useMemo(() =>
    releaseTests
      .filter(t => isCompletedTestStatus(t.status))
      .sort((a, b) => completionTime(b) - completionTime(a)),
    [releaseTests],
  )

  const latestReleaseRun = releaseCompleted[0]
  const activeLiveMetrics = activeTests.map(test => test.live_metrics).filter(Boolean)

  const archivedReleaseRuns = useMemo(() =>
    releaseCompleted
      .filter(test => Boolean(test.archive)),
    [releaseCompleted],
  )

  useEffect(() => {
    const missingReports = archivedReleaseRuns.filter(test => !(test.id in archiveReports))
    if (missingReports.length === 0) return

    let cancelled = false

    const loadReports = async () => {
      const entries: Array<readonly [string, RunArchiveReport | null]> = []

      if (activeProjectId) {
        const batches: TestSummary[][] = []
        for (let index = 0; index < missingReports.length; index += ARCHIVE_REPORT_BATCH_LIMIT) {
          batches.push(missingReports.slice(index, index + ARCHIVE_REPORT_BATCH_LIMIT))
        }
        const results = await Promise.allSettled(batches.map(batch => (
          api.getTestArchiveReports(batch.map(test => test.id), activeProjectId)
        )))
        results.forEach((result, index) => {
          const batch = batches[index]
          if (result.status === 'rejected') {
            batch.forEach(test => {
              entries.push([test.id, null] as const)
            })
            return
          }
          batch.forEach(test => {
            const report = result.value.reports[test.id] ?? null
            entries.push([test.id, result.value.errors?.[test.id] ? null : report] as const)
          })
        })
      } else {
        const results = await Promise.all(missingReports.map(async test => {
          try {
            const report = await api.getTestArchiveReport(test.id)
            return [test.id, report as RunArchiveReport] as const
          } catch {
            return [test.id, null] as const
          }
        }))
        entries.push(...results)
      }

      if (cancelled) return
      setArchiveReports(prev => {
        const next = { ...prev }
        entries.forEach(([id, report]) => {
          next[id] = report
        })
        return next
      })
    }

    void loadReports().catch(() => {
      if (cancelled) return
      // If report loading fails unexpectedly, mark these ids null so the
      // release trend degrades gracefully instead of retrying every render.
      setArchiveReports(prev => {
        const next = { ...prev }
        missingReports.forEach(test => {
          if (!(test.id in next)) next[test.id] = null
        })
        return next
      })
    })

    return () => {
      cancelled = true
    }
  }, [activeProjectId, api, archivedReleaseRuns, archiveReports])

  const failedArchiveReportCount = useMemo(
    () => Object.values(archiveReports).filter(report => report === null).length,
    [archiveReports],
  )
  const archiveReportsError = failedArchiveReportCount > 0
    ? `${failedArchiveReportCount} archived performance report${failedArchiveReportCount === 1 ? '' : 's'} could not be loaded.`
    : ''

  const retryArchiveReports = () => {
    setArchiveReports(current => Object.fromEntries(
      Object.entries(current).filter(([, report]) => report !== null),
    ))
  }

  const performanceTrend = useMemo(() =>
    archivedReleaseRuns
      .map(test => {
        const report = archiveReports[test.id]
        if (!report) return null
        return {
          test,
          report,
          runtimeSummary: formatBrowserRuntimeSummary(report.manifest?.provenance?.runtime_settings?.browser ?? test.runtime_settings?.browser),
        }
      })
      .filter((entry): entry is PerformanceTrendEntry => Boolean(entry)),
    [archivedReleaseRuns, archiveReports],
  )

  const releaseNameById = useMemo(() => {
    const byId = new Map<string, string>()
    releases.forEach(release => byId.set(release.id, release.name || release.id))
    if (activeRelease) byId.set(activeRelease.id, activeRelease.name || activeRelease.id)
    return byId
  }, [activeRelease, releases])

  const loadComparisonTestOptions = useMemo<LoadComparisonTestOption[]>(() =>
    performanceTrend.map(({ test, report }) => ({
      id: test.id,
      name: test.name,
      releaseId: test.release_id,
      releaseName: test.release_id ? releaseNameById.get(test.release_id) || test.release_id : 'Unassigned release',
      completedAt: test.ended_at || test.start_time || test.created_at,
      vusers: report.overview.peak_active_vusers || test.total_vusers,
      transactionCount: report.overview.transactions || test.archive?.transaction_count || 0,
    })),
    [performanceTrend, releaseNameById],
  )
  const loadComparisonTestOptionIdsKey = useMemo(
    () => loadComparisonTestOptions.map(option => option.id).join('\u001f'),
    [loadComparisonTestOptions],
  )

  useLayoutEffect(() => {
    const validIds = loadComparisonTestOptionIdsKey ? loadComparisonTestOptionIdsKey.split('\u001f').filter(Boolean) : []
    setSelectedPerformanceTestIds(current => {
      const previousIds = previousPerformanceTestIdsRef.current
      const next = deriveSelectedIds(current, previousIds, validIds)
      return setEquals(current, next) ? current : next
    })
    previousPerformanceTestIdsRef.current = validIds
  }, [loadComparisonTestOptionIdsKey])

  const sortedPerformanceTrend = useMemo(() => {
    return [...performanceTrend].sort((left, right) => {
      let leftValue: string | number
      let rightValue: string | number
      switch (performanceSort.field) {
        case 'run':
          leftValue = left.test.name.toLowerCase()
          rightValue = right.test.name.toLowerCase()
          break
        case 'peak_tps':
          leftValue = left.report.overview.peak_tps || 0
          rightValue = right.report.overview.peak_tps || 0
          break
        case 'avg_ms':
          leftValue = left.report.overview.avg_response_ms || 0
          rightValue = right.report.overview.avg_response_ms || 0
          break
        case 'errors':
          leftValue = left.report.overview.error_pct || 0
          rightValue = right.report.overview.error_pct || 0
          break
        case 'sla':
          leftValue = left.report.manifest?.sla_breached ? 1 : 0
          rightValue = right.report.manifest?.sla_breached ? 1 : 0
          break
        case 'complete':
          leftValue = left.report.completeness?.score_pct ?? -1
          rightValue = right.report.completeness?.score_pct ?? -1
          break
        case 'completed':
        default:
          leftValue = completionTime(left.test)
          rightValue = completionTime(right.test)
          break
      }
      const compared = compareValues(leftValue, rightValue, performanceSort.direction)
      return compared || compareValues(completionTime(left.test), completionTime(right.test), 'desc')
    })
  }, [performanceSort, performanceTrend])
  const effectiveSelectedPerformanceTestIds = useMemo(() => {
    const validIds = loadComparisonTestOptionIdsKey ? loadComparisonTestOptionIdsKey.split('\u001f').filter(Boolean) : []
    const previousIds = previousPerformanceTestIdsRef.current
    const previousIdsKey = previousIds.join('\u001f')
    if (previousIdsKey === loadComparisonTestOptionIdsKey) {
      return selectedPerformanceTestIds
    }
    return deriveSelectedIds(selectedPerformanceTestIds, previousIds, validIds)
  }, [loadComparisonTestOptionIdsKey, selectedPerformanceTestIds])
  const selectedPerformanceTestIdList = useMemo(
    () => loadComparisonTestOptions.map(option => option.id).filter(id => effectiveSelectedPerformanceTestIds.has(id)),
    [effectiveSelectedPerformanceTestIds, loadComparisonTestOptions],
  )
  const visiblePerformanceTestIds = useMemo(
    () => sortedPerformanceTrend.map(({ test }) => test.id),
    [sortedPerformanceTrend],
  )
  const selectedVisiblePerformanceTestCount = useMemo(
    () => visiblePerformanceTestIds.filter(id => effectiveSelectedPerformanceTestIds.has(id)).length,
    [effectiveSelectedPerformanceTestIds, visiblePerformanceTestIds],
  )
  const allVisiblePerformanceTestsSelected = visiblePerformanceTestIds.length > 0
    && selectedVisiblePerformanceTestCount === visiblePerformanceTestIds.length
  const hasPartialVisiblePerformanceSelection = selectedVisiblePerformanceTestCount > 0
    && selectedVisiblePerformanceTestCount < visiblePerformanceTestIds.length

  useEffect(() => {
    if (performanceSelectAllRef.current) {
      performanceSelectAllRef.current.indeterminate = hasPartialVisiblePerformanceSelection
    }
  }, [hasPartialVisiblePerformanceSelection])

  const setPerformanceTestSelected = (testId: string, checked: boolean) => {
    setSelectedPerformanceTestIds(current => {
      const next = new Set(current)
      if (checked) {
        next.add(testId)
      } else {
        next.delete(testId)
      }
      return setEquals(current, next) ? current : next
    })
  }

  const setVisiblePerformanceTestsSelected = (checked: boolean) => {
    setSelectedPerformanceTestIds(current => {
      const next = new Set(current)
      visiblePerformanceTestIds.forEach(testId => {
        if (checked) {
          next.add(testId)
        } else {
          next.delete(testId)
        }
      })
      return setEquals(current, next) ? current : next
    })
  }

  const releaseTrendStats = useMemo(() => buildReleaseTrendStats(performanceTrend), [performanceTrend])

  const sortedRecentTests = useMemo(() => {
    return [...recentTests].sort((left, right) => {
      let leftValue: string | number
      let rightValue: string | number
      switch (activitySort.field) {
        case 'run':
          leftValue = left.name.toLowerCase()
          rightValue = right.name.toLowerCase()
          break
        case 'status':
          leftValue = left.status.toLowerCase()
          rightValue = right.status.toLowerCase()
          break
        case 'vusers':
          leftValue = left.total_vusers || 0
          rightValue = right.total_vusers || 0
          break
        case 'duration':
          leftValue = parseDurationValue(left.duration)
          rightValue = parseDurationValue(right.duration)
          break
        case 'transactions':
          leftValue = left.archive?.transaction_count ?? -1
          rightValue = right.archive?.transaction_count ?? -1
          break
        case 'date':
        default:
          leftValue = completionTime(left)
          rightValue = completionTime(right)
          break
      }
      const compared = compareValues(leftValue, rightValue, activitySort.direction)
      return compared || compareValues(completionTime(left), completionTime(right), 'desc')
    })
  }, [activitySort, recentTests])

  const stats = useMemo(() => {
    const completed = resolvedTests.filter(t => t.status === 'COMPLETE').length
    const failed = resolvedTests.filter(t => t.status === 'FAILED').length
    const aborted = resolvedTests.filter(t => t.status === 'ABORTED').length
    const activeVUsers = activeTests.reduce((sum, test) => sum + (test.live_metrics?.active_vusers ?? test.total_vusers ?? 0), 0)
    const activeTps = activeTests.reduce((sum, test) => sum + (test.live_metrics?.tps || 0), 0)
    const avgLatency = activeLiveMetrics.length
      ? activeLiveMetrics.reduce((sum, metrics) => sum + (metrics?.avg_response_ms || 0), 0) / activeLiveMetrics.length
      : 0
    const avgError = activeLiveMetrics.length
      ? activeLiveMetrics.reduce((sum, metrics) => sum + (metrics?.error_pct || 0), 0) / activeLiveMetrics.length
      : 0
    return { total: resolvedTests.length, completed, failed, aborted, active: activeTests.length, activeVUsers, activeTps, avgLatency, avgError }
  }, [resolvedTests, activeTests, activeLiveMetrics])

  const releaseStats = useMemo(() => {
    const completed = releaseCompleted.filter(t => t.status === 'COMPLETE').length
    const failed = releaseCompleted.filter(t => t.status === 'FAILED').length
    const aborted = releaseCompleted.filter(t => t.status === 'ABORTED').length
    const archived = releaseCompleted.filter(t => t.archive).length
    const totalTx = releaseCompleted.reduce((sum, test) => sum + (test.archive?.transaction_count || 0), 0)
    return { total: releaseTests.length, completed, failed, aborted, archived, totalTx }
  }, [releaseTests, releaseCompleted])

  const attentionItems = useMemo(() => {
    const items: AttentionItem[] = []
    activeTests.forEach(test => {
      const errorPct = test.live_metrics?.error_pct || 0
      if (errorPct >= 1) {
        items.push({ tone: 'danger', title: `${test.name} errors`, detail: `${formatNumber(errorPct, 2)}% current error rate`, testId: test.id })
      }
      const avgMs = test.live_metrics?.avg_response_ms || 0
      if (avgMs >= 1000) {
        items.push({ tone: 'warning', title: `${test.name} latency`, detail: `${formatNumber(avgMs)} ms average response`, testId: test.id })
      }
    })
    releaseCompleted.slice(0, 8).forEach(test => {
      if (test.status === 'FAILED') {
        items.push({ tone: 'danger', title: `${test.name} failed`, detail: formatDateTime(test.ended_at || test.created_at), testId: test.id })
      } else if (test.status === 'ABORTED') {
        items.push({ tone: 'warning', title: `${test.name} aborted`, detail: formatDateTime(test.ended_at || test.created_at), testId: test.id })
      } else if (!test.archive) {
        items.push({ tone: 'info', title: `${test.name} archive pending`, detail: 'Result archive is not available yet', testId: test.id })
      }
    })
    if (health?.capacity_availability?.warnings?.length) {
      items.push({ tone: 'warning', title: 'Capacity warning', detail: health.capacity_availability.warnings[0] })
    }
    pendingTests.slice(0, 3).forEach(test => {
      const queuePrefix = test.queue_position ? `Queue #${test.queue_position}` : 'Queued'
      items.push({
        tone: 'info',
        title: `${test.name} waiting`,
        detail: `${queuePrefix} · ${test.pending_reason || 'waiting for capacity'}${test.pending_since ? ` · since ${formatDateTime(test.pending_since)}` : ''}`,
        testId: test.id,
      })
    })
    return items.slice(0, 6)
  }, [activeTests, health, pendingTests, releaseCompleted])

  const statusColor = connectionState === 'connected'
    ? health?.status === 'healthy' ? 'healthy' : 'degraded'
    : connectionState === 'connecting'
      ? 'checking'
      : 'offline'
  const statusLabel = statusColor === 'healthy'
    ? 'Healthy'
    : statusColor === 'degraded'
      ? 'Degraded'
      : statusColor === 'checking'
        ? 'Connecting'
        : 'Offline'

  const navigateToMonitoring = () => onNavigate('monitoring')
  const navigateToTests = () => onNavigate('tests')
  const navigateToNewTest = () => onNavigate('new-test')
  const navigateToResults = () => onNavigate('results')
  const downloadTrendCsv = async () => {
    if (!activeRelease?.id) return
    setTrendDownloadError('')
    try {
      const blob = await api.downloadReleaseTrend(activeRelease.id)
      const filename = `${(activeRelease.name || 'release').replace(/\s+/g, '-').toLowerCase()}-${activeRelease.id}-trend.csv`
      await downloadBlob(blob, filename)
    } catch (cause: unknown) {
      const detail = cause instanceof Error && cause.message ? `: ${cause.message}` : ''
      setTrendDownloadError(`Could not export the release trend${detail}`)
    }
  }
  const navigateToLatestResult = () => {
    if (latestReleaseRun) {
      onSelectTest(latestReleaseRun.id)
      return
    }
    onNavigate('results')
  }
  const navigateToPrimaryActiveRun = () => {
    if (activeTests[0]) {
      onSelectTest(activeTests[0].id)
      return
    }
    onNavigate('tests')
  }
  const navigateToPendingRun = (id?: string) => {
    if (id && onOpenCommandCenter) {
      onOpenCommandCenter(id)
      return
    }
    if (id) {
      onSelectTest(id)
      return
    }
    onNavigate('command-center')
  }
  const requestPerformanceSort = (field: PerformanceSortField) => {
    setPerformanceSort(current => ({
      field,
      direction: current.field === field
        ? current.direction === 'asc' ? 'desc' : 'asc'
        : performanceSortDefaults[field],
    }))
  }
  const requestActivitySort = (field: ActivitySortField) => {
    setActivitySort(current => ({
      field,
      direction: current.field === field
        ? current.direction === 'asc' ? 'desc' : 'asc'
        : activitySortDefaults[field],
    }))
  }
  const dashboardTabCounts = useMemo<Record<DashboardTabKey, number>>(() => ({
    activity: recentTests.length,
    active: stats.active,
    performance: performanceTrend.length,
    releaseTrend: performanceTrend.length,
  }), [performanceTrend.length, recentTests.length, stats.active])
  const activeDashboardTabLabel = dashboardTabs.find(tab => tab.id === activeDashboardTab)?.label || 'Dashboard'
  const dashboardTabControls = useMemo(() => (
    <div className="dash-tab-row topbar-page-section-toggle" role="tablist" aria-label="Dashboard sections">
      {dashboardTabs.map(tab => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          aria-selected={activeDashboardTab === tab.id}
          className={`dash-tab ${activeDashboardTab === tab.id ? 'active' : ''}`}
          onClick={() => setActiveDashboardTab(tab.id)}
        >
          <span>{tab.label}</span>
          <strong>{dashboardTabCounts[tab.id]}</strong>
        </button>
      ))}
    </div>
  ), [activeDashboardTab, dashboardTabCounts])
  const dashboardTopbarContribution = useMemo(() => ({
    id: 'dashboard-page',
    title: 'Dashboard',
    controls: dashboardTabControls,
    priority: 10,
  }), [dashboardTabControls])

  useTopbarContribution(dashboardTopbarContribution)
  const renderPerformanceSortHeader = (field: PerformanceSortField, label: string, className = '') => (
    <th className={`sortable ${className}`.trim()}>
      <button type="button" onClick={() => requestPerformanceSort(field)}>
        <span>{label}</span>
        <SortIcon active={performanceSort.field === field} direction={performanceSort.direction} />
      </button>
    </th>
  )
  const renderActivitySortHeader = (field: ActivitySortField, label: string, className = '') => (
    <th className={`sortable ${className}`.trim()}>
      <button type="button" onClick={() => requestActivitySort(field)}>
        <span>{label}</span>
        <SortIcon active={activitySort.field === field} direction={activitySort.direction} />
      </button>
    </th>
  )
  const performanceTableBounds = useBottomBoundedTable(`${activeDashboardTab}:${releasePanelOpen}:${sortedPerformanceTrend.length}`)
  const activityTableBounds = useBottomBoundedTable(`${activeDashboardTab}:${releasePanelOpen}:${sortedRecentTests.length}`)
  const activeTestIds = useMemo(() => activeTests.map(test => test.id), [activeTests])
  const aggregateHistory = useMemo(() => {
    if (activeTestIds.length === 0) return []
    const rows: MetricsHistoryPoint[] = []
    for (const testId of activeTestIds) {
      const history = metricsHistory[testId]
      if (history?.length) rows.push(...history.slice(-60))
    }
    return rows
      .sort((left, right) => (left.timestampMs ?? 0) - (right.timestampMs ?? 0))
      .slice(-60)
  }, [activeTestIds, metricsHistory])

  if (activeApplicationId && (releasesLoading || releasesError)) {
    return (
      <div className="page dash">
        <div className="page-header">
          <div>
            <h2>Dashboard</h2>
            <p className="page-subtitle">{activeApplication?.name || 'Application'} / Release scope unavailable</p>
          </div>
        </div>
        {releasesLoading ? (
          <div className="empty-card" role="status">
            <p>Loading release scope...</p>
            <p className="empty-hint">Dashboard data will appear after the selected release is confirmed.</p>
          </div>
        ) : (
          <div className="settings-error-inline" role="alert">
            <span>{releasesError}</span>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => void refreshReleases(activeApplicationId).catch(() => undefined)}
            >
              Retry release load
            </button>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="page dash">
      <div className="page-header">
        <div>
          <h2>Dashboard</h2>
          <p className="page-subtitle">
            {activeApplication?.name || 'Application'} / {activeRelease?.name || 'All releases'} performance overview
          </p>
        </div>
      </div>

      <DashboardTestListStatus {...testListState} />

      <div className={`dash-dashboard-layout ${releasePanelOpen ? 'snapshot-open' : 'snapshot-collapsed'}`}>
        <div className="dash-main-column">
          <div className="dash-primary-content">
            {!hasTopbarContributionProvider && dashboardTabControls}

            <div className="dash-tab-panel" role="tabpanel" aria-label={`${activeDashboardTabLabel} dashboard section`}>
        {testListState.ready && activeDashboardTab === 'performance' && (
          <section className="dash-section dash-performance-trend">
            <div className="dash-section-heading-row">
              <h3 className="dash-section-title">Performance Trend</h3>
              <div className="dash-section-heading-actions">
                {activeRelease?.id && performanceTrend.length > 0 && (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={() => void downloadTrendCsv()}>
                    Export CSV
                  </button>
                )}
                {performanceTrend.length > 0 && (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={navigateToResults}>
                    View Results
                  </button>
                )}
                <span className="dash-context-chip">Last {archivedReleaseRuns.length || 0} archived runs</span>
              </div>
            </div>
            {archiveReportsError && (
              <div className="dash-empty small" role="alert">
                <span>{archiveReportsError}</span>
                <button type="button" className="btn btn-secondary btn-sm" onClick={retryArchiveReports}>Retry reports</button>
              </div>
            )}
            {trendDownloadError && (
              <div className="dash-empty small" role="alert">
                <span>{trendDownloadError}</span>
              </div>
            )}
            {performanceTrend.length === 0 ? (
              <div className="dash-empty small">
                <span>No archived performance reports yet</span>
              </div>
            ) : (
              <>
                {activeApplicationId && (
                  <LoadComparisonPanel
                    applicationId={activeApplicationId}
                    getLoadComparison={api.getLoadComparison}
                    testOptions={loadComparisonTestOptions}
                    selectedTestIds={selectedPerformanceTestIdList}
                  />
                )}
                <div className="trend-table-wrap" ref={performanceTableBounds.ref} style={performanceTableBounds.style}>
                  <table className="trend-table">
                    <thead>
                      <tr>
                        <th className="trend-table-checkbox-col">
                          <label className="trend-table-checkbox">
                            <input
                              ref={performanceSelectAllRef}
                              type="checkbox"
                              aria-label="Select all visible runs for load comparison"
                              checked={allVisiblePerformanceTestsSelected}
                              disabled={visiblePerformanceTestIds.length === 0}
                              onChange={event => setVisiblePerformanceTestsSelected(event.currentTarget.checked)}
                            />
                          </label>
                        </th>
                        {renderPerformanceSortHeader('run', 'Run')}
                        {renderPerformanceSortHeader('completed', 'Completed')}
                        {renderPerformanceSortHeader('peak_tps', 'Peak TPS', 'num')}
                        {renderPerformanceSortHeader('avg_ms', 'Avg ms', 'num')}
                        {renderPerformanceSortHeader('errors', 'Errors', 'num')}
                        {renderPerformanceSortHeader('sla', 'SLA', 'num')}
                        {renderPerformanceSortHeader('complete', 'Complete', 'num')}
                        {onOpenTestArchive && <th>Actions</th>}
                      </tr>
                    </thead>
                    <tbody>
                      {sortedPerformanceTrend.map(({ test, report, runtimeSummary }) => (
                        <tr
                          key={test.id}
                          onClick={event => {
                            const target = event.target as HTMLElement
                            if (target.closest('.trend-table-checkbox')) return
                            onSelectTest(test.id)
                          }}
                        >
                          <td className="trend-table-checkbox-col">
                            <label className="trend-table-checkbox">
                              <input
                                type="checkbox"
                                aria-label={`Select ${test.name} for load comparison`}
                                checked={effectiveSelectedPerformanceTestIds.has(test.id)}
                                onChange={event => setPerformanceTestSelected(test.id, event.currentTarget.checked)}
                              />
                            </label>
                          </td>
                          <td aria-label="Trend actions">
                            <button
                              type="button"
                              className="dash-source-link"
                              onClick={event => {
                                event.stopPropagation()
                                onSelectTest(test.id)
                              }}
                            >
                              {test.name}
                            </button>
                            <span className="trend-run-meta">{test.script_name || `${test.total_vusers} VUsers`}</span>
                            {runtimeSummary && <span className="trend-run-meta">{runtimeSummary}</span>}
                          </td>
                          <td>{formatDateTime(test.ended_at || test.start_time || test.created_at)}</td>
                          <td className="num strong">{formatNumber(report.overview.peak_tps, 1)}</td>
                          <td className="num">{formatNumber(report.overview.avg_response_ms)}</td>
                          <td className={`num ${report.overview.error_pct > 0 ? 'text-danger strong' : ''}`}>
                            {formatNumber(report.overview.error_pct, 2)}%
                          </td>
                          <td className={`num ${report.manifest?.sla_breached ? 'text-danger strong' : 'text-success'}`}>
                            {report.manifest?.sla_breached ? 'Breached' : 'Passed'}
                          </td>
                          <td className="num">{formatOptionalNumber(report.completeness?.score_pct, 0)}%</td>
                          {onOpenTestArchive && (
                            <td aria-label="Trend actions">
                              <div className="trend-row-actions">
                                <button
                                  type="button"
                                  className="btn btn-sm btn-primary"
                                  onClick={event => {
                                    event.stopPropagation()
                                    onSelectTest(test.id)
                                  }}
                                >
                                  Run Dashboard
                                </button>
                                <button
                                  type="button"
                                  className="btn btn-sm btn-secondary"
                                  onClick={event => {
                                    event.stopPropagation()
                                    onOpenTestArchive(test.id)
                                  }}
                                >
                                  Run Archive
                                </button>
                              </div>
                            </td>
                          )}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </section>
        )}

        {testListState.ready && activeDashboardTab === 'active' && (
          <section className="dash-section">
            <h3 className="dash-section-title">
              Active Tests
              {stats.active > 0 && <span className="dash-badge pulse">{stats.active}</span>}
            </h3>
            {activeTests.length === 0 ? (
              <div className="dash-empty compact">
                <span className="dash-empty-icon">
                  <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
                  </svg>
                </span>
                <span>No tests currently running</span>
                <span className="dash-empty-hint">{pendingTests.length > 0 ? `${pendingTests.length} test${pendingTests.length === 1 ? '' : 's'} waiting on project capacity.` : 'Start a scenario from the New Test page'}</span>
                <button
                  type="button"
                  className={`btn btn-sm ${pendingTests.length > 0 ? 'btn-secondary' : 'btn-primary'}`}
                  onClick={pendingTests.length > 0 ? navigateToTests : navigateToNewTest}
                >
                  {pendingTests.length > 0 ? 'View queued tests' : 'Start New Test'}
                </button>
              </div>
            ) : (
              <div className="dash-active-grid">
                {activeTests.map(test => {
                  const history = metricsHistory[test.id] || []
                  const latest = test.live_metrics
                  const runtimeSummary = formatBrowserRuntimeSummary(test.runtime_settings?.browser)
                  const metricOption = graphMetricOptions[0]
                  const chartData = history.slice(-60).map(point => ({
                    ...point,
                    selectedMetric: getHistoryMetric(point, metricOption.key),
                  }))
                  const groupRows = buildActiveGroupMetricRows(test)
                  return (
                    <div
                      key={test.id}
                      className="dash-active-card"
                      role="button"
                      tabIndex={0}
                      aria-label={`Open live test ${test.name}`}
                      onClick={() => onSelectTest(test.id)}
                      onKeyDown={event => {
                        if (event.key !== 'Enter' && event.key !== ' ') return
                        event.preventDefault()
                        onSelectTest(test.id)
                      }}
                    >
                      <div className="dac-header">
                        <div className="dac-title-wrap">
                          <span className="dac-name">{test.name}</span>
                        </div>
                        <StatusBadge status={test.status} />
                      </div>
                      <div className="dac-metrics">
                        <div className="dac-metric">
                          <span className="dac-metric-value">{latest?.active_vusers ?? test.total_vusers}</span>
                          <span className="dac-metric-label">VUsers</span>
                        </div>
                        <div className="dac-metric">
                          <span className="dac-metric-value">{latest?.tps?.toFixed(1) ?? '—'}</span>
                          <span className="dac-metric-label">TPS</span>
                        </div>
                        <div className="dac-metric">
                          <span className="dac-metric-value">{latest?.avg_response_ms?.toFixed(0) ?? '—'}</span>
                          <span className="dac-metric-label">Avg ms</span>
                        </div>
                        <div className="dac-metric">
                          <span className={`dac-metric-value ${(latest?.error_pct ?? 0) > 0 ? 'text-danger' : ''}`}>
                            {latest?.error_pct?.toFixed(2) ?? '0'}%
                          </span>
                          <span className="dac-metric-label">Errors</span>
                        </div>
                      </div>
                      {chartData.length > 1 && (
                        <div className="dac-sparkline">
                          <ResponsiveContainer width="100%" height={60}>
                            <AreaChart data={chartData}>
                              <defs>
                                <linearGradient id={`spark-${test.id}`} x1="0" y1="0" x2="0" y2="1">
                                  <stop offset="0%" stopColor={metricOption.color} stopOpacity={0.3} />
                                  <stop offset="100%" stopColor={metricOption.color} stopOpacity={0} />
                                </linearGradient>
                              </defs>
                              <Area
                                type="monotone"
                                dataKey="selectedMetric"
                                stroke={metricOption.color}
                                fill={`url(#spark-${test.id})`}
                                strokeWidth={1.5}
                                isAnimationActive={false}
                              />
                            </AreaChart>
                          </ResponsiveContainer>
                          <span className="dac-chart-label">{metricOption.label}</span>
                        </div>
                      )}
                      {groupRows.length > 0 && (
                        <div className="dac-group-table-wrap">
                          <table className="dac-group-table" aria-label={`${test.name} group metrics`}>
                            <thead>
                              <tr>
                                <th>Group</th>
                                <th className="num">VUsers</th>
                                <th className="num">TPS</th>
                                <th className="num">ART</th>
                                <th className="num">Errors %</th>
                                <th className="num">Passed</th>
                                <th className="num">Failed</th>
                              </tr>
                            </thead>
                            <tbody>
                              {groupRows.map(row => (
                                <tr key={row.name} className={row.hasLiveMetrics ? undefined : 'is-pending'}>
                                  <td>
                                    <span className="dac-group-name">{row.name}</span>
                                  </td>
                                  <td className="num strong">{formatOptionalCount(row.vusers)}</td>
                                  <td className="num">{formatOptionalNumber(row.tps, 1)}</td>
                                  <td className="num">{formatOptionalNumber(row.avgResponseMs, 0)}</td>
                                  <td className={`num ${(row.errorPct ?? 0) > 0 ? 'text-danger strong' : ''}`}>{formatOptionalPercent(row.errorPct)}</td>
                                  <td className="num">{formatOptionalCount(row.passed)}</td>
                                  <td className={`num ${(row.failed ?? 0) > 0 ? 'text-danger strong' : ''}`}>{formatOptionalCount(row.failed)}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      )}
                      <div className="dac-footer">
                        <span className="dac-detail">{test.total_vusers} VUsers · {test.groups?.length || 1} group{(test.groups?.length || 1) > 1 ? 's' : ''}</span>
                        <span className="dac-detail">{test.duration}{runtimeSummary ? ` · ${runtimeSummary}` : ''}</span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
            {activeTests.length > 0 && aggregateHistory.length > 1 && (
              <button type="button" className="dash-aggregate-chart dash-card-button" onClick={navigateToTests}>
                <span className="aggregate-label">Aggregate TPS (active tests)</span>
                <ResponsiveContainer width="100%" height={70}>
                  <AreaChart data={aggregateHistory}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                    <XAxis dataKey="time" hide />
                    <YAxis hide />
                    <Tooltip
                      contentStyle={{ ...chartTooltipContentStyle, borderRadius: 6, fontSize: 12 }}
                      labelStyle={{ color: 'var(--text-primary)', fontWeight: 600 }}
                    />
                    <Area type="monotone" dataKey="tps" stroke="var(--chart-2)" fill="var(--chart-2)" fillOpacity={0.15} strokeWidth={1.5} isAnimationActive={false} />
                  </AreaChart>
                </ResponsiveContainer>
              </button>
            )}
          </section>
        )}

        {testListState.ready && activeDashboardTab === 'activity' && (
          <section className="dash-section dash-activity">
            <h3 className="dash-section-title">Recent Activity</h3>
            {recentTests.length === 0 ? (
              <div className="dash-empty compact">
                <span>No completed tests yet</span>
                <span className="dash-empty-hint">Start a test to build activity, performance, and release history.</span>
                <button type="button" className="btn btn-primary btn-sm" onClick={navigateToNewTest}>Start New Test</button>
              </div>
            ) : (
              <div className="trend-table-wrap" ref={activityTableBounds.ref} style={activityTableBounds.style}>
                <table className="trend-table">
                  <thead>
                    <tr>
                      {renderActivitySortHeader('run', 'Run')}
                      {renderActivitySortHeader('status', 'Status')}
                      {renderActivitySortHeader('vusers', 'VUsers', 'num')}
                      {renderActivitySortHeader('duration', 'Duration', 'num')}
                      {renderActivitySortHeader('transactions', 'Transactions', 'num')}
                      {renderActivitySortHeader('date', 'Date / Time')}
                      {onOpenCommandCenter && <th>Actions</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {sortedRecentTests.map(test => {
                      const runtimeSummary = formatBrowserRuntimeSummary(test.runtime_settings?.browser)
                      return (
                        <tr key={test.id} onClick={() => onSelectTest(test.id)}>
                          <td>
                            <button
                              type="button"
                              className="dash-source-link"
                              onClick={event => {
                                event.stopPropagation()
                                onSelectTest(test.id)
                              }}
                            >
                              {test.name}
                            </button>
                            {(test.script_name || runtimeSummary) && (
                              <span className="trend-run-meta">{[test.script_name, runtimeSummary].filter(Boolean).join(' · ')}</span>
                            )}
                          </td>
                          <td><StatusBadge status={test.status} /></td>
                          <td className="num">{test.total_vusers}</td>
                          <td className="num">{test.duration}</td>
                          <td className="num">{test.archive ? test.archive.transaction_count.toLocaleString() : 'pending'}</td>
                          <td>{formatDateTime(test.ended_at || test.start_time || test.created_at)}</td>
                          {onOpenCommandCenter && (
                            <td>
                              <button
                                type="button"
                                className="btn btn-sm btn-ghost"
                                onClick={event => {
                                  event.stopPropagation()
                                  onOpenCommandCenter(test.id)
                                }}
                              >
                                Command Center
                              </button>
                            </td>
                          )}
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        )}

        {testListState.ready && activeDashboardTab === 'releaseTrend' && (
          <section className="dash-section dash-release-trend-analysis">
            {activeRelease?.id ? (
              <ReleaseTrendPanel releaseId={activeRelease.id} getReleaseTrend={api.getReleaseTrend} />
            ) : (
              <div className="dash-empty small">
                <span>Select a release to view release trend analysis.</span>
              </div>
            )}
          </section>
        )}
          </div>
        </div>
        </div>
        <ReleaseSnapshotPanel
          open={releasePanelOpen}
          audience={snapshotAudience}
          activeRelease={activeRelease}
          releaseOptions={dashboardReleaseOptions}
          releaseStats={releaseStats}
          dashboardStats={stats}
          latestReleaseRun={latestReleaseRun}
          releaseTrendStats={releaseTrendStats}
          health={health}
          statusLabel={statusLabel}
          statusColor={statusColor}
          activeTests={activeTests}
          pendingTests={pendingTests}
          attentionItems={attentionItems}
          testDataReady={testListState.ready}
          onToggle={() => setReleasePanelOpen(value => !value)}
          onReleaseChange={setActiveRelease}
          onLatestResult={navigateToLatestResult}
          onPrimaryActiveRun={navigateToPrimaryActiveRun}
          onPendingRun={navigateToPendingRun}
          onResults={navigateToResults}
          onMonitoring={navigateToMonitoring}
          onNewTest={navigateToNewTest}
          onSelectTest={onSelectTest}
        />
      </div>
    </div>
  )
}
