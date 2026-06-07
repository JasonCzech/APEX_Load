import { useState, useEffect, useLayoutEffect, useMemo, useRef, type CSSProperties, type DependencyList, type DragEvent } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { useApi } from '../hooks/useApi'
import { useApplication } from '../hooks/useApplication'
import { useProject } from '../hooks/useProject'
import { TestSummary, MetricsHistoryPoint, Release, RunArchiveReport } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { ReleaseTrendPanel } from './ReleaseTrendPanel'
import { chartTooltipContentStyle } from './chartTooltipTheme'
import { type NavPage } from './Sidebar'
import StatusBadge from './StatusBadge'
import './DashboardPage.css'

interface Props {
  testStatuses: Record<string, string>
  metricsHistory: Record<string, MetricsHistoryPoint[]>
  connectionState: WebSocketConnectionState
  onSelectTest: (id: string) => void
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

interface GraphCardSettings {
  metric: CardMetricKey
  showGraph: boolean
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
  onToggle: () => void
  onReleaseChange: (release: Release) => void
  onLatestResult: () => void
  onPrimaryActiveRun: () => void
  onResults: () => void
  onMonitoring: () => void
  onNewTest: () => void
  onSelectTest: (id: string) => void
}

const defaultGraphCardSettings: GraphCardSettings = {
  metric: 'tps',
  showGraph: true,
}

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

function formatOptionalNumber(value?: number, digits = 0) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(digits) : '—'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never'
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

function SortIcon({ active, direction }: { active: boolean; direction: SortDirection }) {
  if (!active) return <span className="sort-icon">↕</span>
  return <span className="sort-icon active">{direction === 'asc' ? '↑' : '↓'}</span>
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

function moveId(ids: string[], draggedId: string, targetId: string) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}

function loadJson<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') return fallback
  try {
    const stored = window.localStorage.getItem(key)
    if (!stored) return fallback
    const parsed = JSON.parse(stored)
    if (Array.isArray(fallback)) {
      return (Array.isArray(parsed) ? parsed : fallback) as T
    }
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return { ...(fallback as Record<string, unknown>), ...parsed } as T
    }
    return fallback
  } catch {
    return fallback
  }
}

function useBottomBoundedTable(dependencies: DependencyList) {
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
  }, dependencies)

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
  onToggle,
  onReleaseChange,
  onLatestResult,
  onPrimaryActiveRun,
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

        <div className="release-panel-stat-grid shared">
          <SnapshotStat label="Latest Result" value={latestReleaseRun ? latestReleaseRun.name : 'No runs'} tone="accent" onClick={onLatestResult} />
          <SnapshotStat label="Failed Runs" value={releaseStats.failed} tone={releaseStats.failed > 0 ? 'danger' : 'success'} onClick={onResults} />
          <SnapshotStat label="Archives Ready" value={archivesReady} onClick={onResults} />
          <SnapshotStat label="Archived Transactions" value={releaseStats.totalTx.toLocaleString()} onClick={onResults} />
          <SnapshotStat label="Current TPS" value={formatNumber(dashboardStats.activeTps, 1)} onClick={onPrimaryActiveRun} />
          <SnapshotStat label="Current Error Rate" value={`${formatNumber(dashboardStats.avgError, 2)}%`} tone={dashboardStats.avgError > 0 ? 'danger' : 'neutral'} onClick={onPrimaryActiveRun} />
        </div>

        <div className="release-panel-status-strip" aria-label="Run status summary">
          <SnapshotStat label="Completed" value={dashboardStats.completed} tone="success" onClick={onResults} />
          <SnapshotStat label="Aborted" value={dashboardStats.aborted} tone="warning" onClick={onResults} />
          <SnapshotStat label="Active Now" value={dashboardStats.active} tone="accent" onClick={onPrimaryActiveRun} />
        </div>

        {audience === 'admin' ? (
          <div className="release-panel-section" aria-label="Operations activity">
            <div className="release-panel-section-header">
              <h4>Operations Activity</h4>
              <span>{activeTests.length} live · {pendingTests.length} queued</span>
            </div>
            <div className="release-panel-detail-grid">
              <SnapshotStat label="API Health" value={statusLabel} tone={statusColor === 'healthy' ? 'success' : statusColor === 'offline' ? 'danger' : 'warning'} onClick={onMonitoring} />
              <SnapshotStat label="Generators Ready" value={generatorAvailability} onClick={onMonitoring} />
              <SnapshotStat label="WebSocket Clients" value={health?.ws_clients ?? 0} />
              <SnapshotStat label="Pending Runs" value={pendingTests.length} tone={pendingTests.length > 0 ? 'warning' : 'neutral'} onClick={onPrimaryActiveRun} />
            </div>
            <div className="release-panel-feed">
              {visibleAttentionItems.length === 0 ? (
                <div className="release-panel-feed-empty">No active attention items for this release.</div>
              ) : visibleAttentionItems.map(item => (
                <button
                  type="button"
                  key={`${item.title}-${item.detail}`}
                  className={`release-panel-feed-item tone-${item.tone}`}
                  onClick={() => item.testId ? onSelectTest(item.testId) : onMonitoring()}
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
        )}
      </section>
    </aside>
  )
}

export default function DashboardPage({ testStatuses, metricsHistory, connectionState, onSelectTest, onNavigate, snapshotAudience = 'admin' }: Props) {
  const api = useApi()
  const { activeApplication, activeRelease, releases, setActiveRelease } = useApplication()
  const { activeProject } = useProject()
  const dashboardScopeReady = Boolean(activeProject?.id && activeApplication?.id)
  const [tests, setTests] = useState<TestSummary[]>([])
  const [health, setHealth] = useState<HealthData | null>(null)
  const [archiveReports, setArchiveReports] = useState<Record<string, RunArchiveReport | null>>({})
  const [customizingCards, setCustomizingCards] = useState(false)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
  const [releasePanelOpen, setReleasePanelOpen] = useState(true)
  const [activeDashboardTab, setActiveDashboardTab] = useState<DashboardTabKey>('activity')
  const [performanceSort, setPerformanceSort] = useState<{ field: PerformanceSortField; direction: SortDirection }>({ field: 'completed', direction: 'desc' })
  const [activitySort, setActivitySort] = useState<{ field: ActivitySortField; direction: SortDirection }>({ field: 'date', direction: 'desc' })
  const [activeCardOrder, setActiveCardOrder] = useState<string[]>(() => loadJson<string[]>('apexload.dashboard.activeCardOrder', []))
  const [activeCardSettings, setActiveCardSettings] = useState<Record<string, GraphCardSettings>>(() =>
    loadJson<Record<string, GraphCardSettings>>('apexload.dashboard.activeCardSettings', {}),
  )

  useEffect(() => {
    if (!dashboardScopeReady) {
      return
    }

    const load = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      try {
        const [testRes, healthRes] = await Promise.all([
          api.listTests({ application_id: activeApplication!.id, project_id: activeProject!.id }),
          api.getHealth().catch(() => null),
        ])
        setTests(testRes.tests || [])
        if (healthRes) setHealth(healthRes)
      } catch { /* ignore */ }
    }

    load()
    const interval = setInterval(load, 3000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        load()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api, activeApplication?.id, activeProject?.id, dashboardScopeReady])

  const resolvedTests = useMemo(() =>
    tests.map(t => ({
      ...t,
      status: (testStatuses[t.id] || t.status) as TestSummary['status'],
    })),
    [tests, testStatuses],
  )

  const activeTests = useMemo(() =>
    resolvedTests.filter(t => t.status === 'RUNNING' || t.status === 'INITIALIZING' || t.status === 'STOPPING'),
    [resolvedTests],
  )

  const pendingTests = useMemo(() =>
    resolvedTests
      .filter(t => t.status === 'PENDING')
      .sort((a, b) => {
        const aPosition = a.queue_position ?? Number.MAX_SAFE_INTEGER
        const bPosition = b.queue_position ?? Number.MAX_SAFE_INTEGER
        if (aPosition !== bPosition) return aPosition - bPosition
        return Date.parse(a.pending_since || a.created_at || '') - Date.parse(b.pending_since || b.created_at || '')
      }),
    [resolvedTests],
  )

  useEffect(() => {
    const activeIds = activeTests.map(test => test.id)
    setActiveCardOrder(prev => {
      const retained = prev.filter(id => activeIds.includes(id))
      const added = activeIds.filter(id => !retained.includes(id))
      return [...retained, ...added]
    })
  }, [activeTests])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.dashboard.activeCardOrder', JSON.stringify(activeCardOrder))
  }, [activeCardOrder])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('apexload.dashboard.activeCardSettings', JSON.stringify(activeCardSettings))
  }, [activeCardSettings])

  const orderedActiveTests = useMemo(() => {
    const byId = new Map(activeTests.map(test => [test.id, test]))
    return activeCardOrder
      .map(id => byId.get(id))
      .filter((test): test is TestSummary => Boolean(test))
  }, [activeTests, activeCardOrder])

  const recentTests = useMemo(() =>
    resolvedTests
      .filter(t => t.status === 'COMPLETE' || t.status === 'FAILED' || t.status === 'ABORTED')
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
      .filter(t => t.status === 'COMPLETE' || t.status === 'FAILED' || t.status === 'ABORTED')
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

    Promise.all(
      missingReports.map(async test => {
        try {
          const report = await api.getTestArchiveReport(test.id)
          return [test.id, report as RunArchiveReport] as const
        } catch {
          return [test.id, null] as const
        }
      }),
    ).then(entries => {
      if (cancelled) return
      setArchiveReports(prev => {
        const next = { ...prev }
        entries.forEach(([id, report]) => {
          next[id] = report
        })
        return next
      })
    })

    return () => {
      cancelled = true
    }
  }, [api, archivedReleaseRuns, archiveReports])

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
  const attentionStatusClass = attentionItems.some(item => item.tone === 'danger')
    ? 'status-offline'
    : attentionItems.some(item => item.tone === 'warning')
      ? 'status-degraded'
      : attentionItems.length > 0
        ? 'status-idle'
        : 'status-healthy'
  const attentionIconColor = attentionItems.some(item => item.tone === 'danger')
    ? 'var(--danger)'
    : attentionItems.some(item => item.tone === 'warning')
      ? 'var(--chart-3)'
      : attentionItems.length > 0
        ? 'var(--accent)'
        : 'var(--success)'

  const updateCardSettings = (testId: string, patch: Partial<GraphCardSettings>) => {
    setActiveCardSettings(prev => ({
      ...prev,
      [testId]: {
        ...defaultGraphCardSettings,
        ...prev[testId],
        ...patch,
      },
    }))
  }

  const handleCardDragStart = (event: DragEvent<HTMLDivElement>, testId: string) => {
    if (!customizingCards) return
    setDraggedCardId(testId)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', testId)
  }

  const handleCardDragOver = (event: DragEvent<HTMLDivElement>, targetId: string) => {
    if (!customizingCards || !draggedCardId || draggedCardId === targetId) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  const handleCardDrop = (event: DragEvent<HTMLDivElement>, targetId: string) => {
    if (!customizingCards) return
    event.preventDefault()
    const sourceId = event.dataTransfer.getData('text/plain') || draggedCardId
    if (!sourceId) return
    setActiveCardOrder(prev => moveId(prev, sourceId, targetId))
    setDraggedCardId(null)
  }

  const resetCardLayout = () => {
    setActiveCardOrder(activeTests.map(test => test.id))
    setActiveCardSettings({})
  }

  const navigateToMonitoring = () => onNavigate('monitoring')
  const navigateToTests = () => onNavigate('tests')
  const navigateToNewTest = () => onNavigate('new-test')
  const navigateToResults = () => onNavigate('results')
  const navigateToApiGuide = () => onNavigate('docs')
  const downloadTrendCsv = async () => {
    if (!activeRelease?.id) return
    const blob = await api.downloadReleaseTrend(activeRelease.id)
    const filename = `${(activeRelease.name || 'release').replace(/\s+/g, '-').toLowerCase()}-${activeRelease.id}-trend.csv`
    downloadBlob(blob, filename)
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
  const navigateToAttention = () => {
    const firstAttentionItem = attentionItems[0]
    if (firstAttentionItem?.testId) {
      onSelectTest(firstAttentionItem.testId)
      return
    }
    onNavigate('monitoring')
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
  const dashboardTabCounts: Record<DashboardTabKey, number> = {
    activity: recentTests.length,
    active: stats.active,
    performance: performanceTrend.length,
    releaseTrend: performanceTrend.length,
  }
  const activeDashboardTabLabel = dashboardTabs.find(tab => tab.id === activeDashboardTab)?.label || 'Dashboard'
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
  const performanceTableBounds = useBottomBoundedTable([activeDashboardTab, releasePanelOpen, sortedPerformanceTrend.length])
  const activityTableBounds = useBottomBoundedTable([activeDashboardTab, releasePanelOpen, sortedRecentTests.length])
  const aggregateHistory = Object.values(metricsHistory).flat().slice(-60)

  return (
    <div className="page dash">
      <div className="page-header">
        <div>
          <h2>Dashboard</h2>
          <p className="page-subtitle">
            {activeApplication?.name || 'Application'} / {activeRelease?.name || 'All releases'} performance overview
          </p>
        </div>
        <div className="dash-customize-actions">
          <button className="btn btn-secondary btn-sm btn-accent-action" onClick={navigateToApiGuide} type="button">
            API Guide
          </button>
          {customizingCards && (
            <button className="btn btn-secondary btn-sm" onClick={resetCardLayout} type="button">
              Reset Cards
            </button>
          )}
          <button
            className="btn btn-sm btn-accent-action"
            onClick={() => setCustomizingCards(value => !value)}
            type="button"
          >
            {customizingCards ? 'Done' : 'Customize Cards'}
          </button>
        </div>
      </div>

      <div className={`dash-dashboard-layout ${releasePanelOpen ? 'snapshot-open' : 'snapshot-collapsed'}`}>
        <div className="dash-main-column">
          <div className="dash-primary-top">
            <div className="dash-health-row">
              <button type="button" className={`dash-health-card dash-card-button status-${statusColor}`} onClick={navigateToMonitoring}>
                <div className="dhc-icon">
                  <span className={`health-dot ${statusColor}`} />
                </div>
                <div className="dhc-content">
                  <span className="dhc-value">{statusLabel}</span>
                  <span className="dhc-label">API Server</span>
                </div>
              </button>

              <button type="button" className={`dash-health-card dash-card-button ${stats.active > 0 ? 'status-active' : 'status-idle'}`} onClick={navigateToTests}>
                <div className="dhc-icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-2)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
                    <circle cx="9" cy="7" r="4" />
                    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
                    <path d="M16 3.13a4 4 0 0 1 0 7.75" />
                  </svg>
                </div>
                <div className="dhc-content">
                  <span className="dhc-value">{stats.active}</span>
                  <span className="dhc-label">Active Runs</span>
                </div>
              </button>

              <button type="button" className={`dash-health-card dash-card-button ${stats.activeVUsers > 0 ? 'status-active' : 'status-idle'}`} onClick={navigateToTests}>
                <div className="dhc-icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-3)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
                  </svg>
                </div>
                <div className="dhc-content">
                  <span className="dhc-value">{stats.activeVUsers}</span>
                  <span className="dhc-label">Active VUsers</span>
                </div>
              </button>

              <button type="button" className={`dash-health-card dash-card-button ${attentionStatusClass}`} onClick={navigateToAttention}>
                <div className="dhc-icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={attentionIconColor} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 9v4" />
                    <path d="M12 17h.01" />
                    <path d="M10.3 3.9 2.5 18a2 2 0 0 0 1.7 3h15.6a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
                  </svg>
                </div>
                <div className="dhc-content">
                  <span className="dhc-value">{attentionItems.length}</span>
                  <span className="dhc-label">Attention Needed</span>
                </div>
              </button>
            </div>
          </div>

          <div className="dash-primary-content">
            <div className="dash-tab-row" role="tablist" aria-label="Dashboard sections">
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

            <div className="dash-tab-panel" role="tabpanel" aria-label={`${activeDashboardTabLabel} dashboard section`}>
        {activeDashboardTab === 'performance' && (
          <section className="dash-section dash-performance-trend">
            <div className="dash-section-heading-row">
              <h3 className="dash-section-title">Performance Trend</h3>
              <div className="dash-section-heading-actions">
                {activeRelease?.id && performanceTrend.length > 0 && (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={downloadTrendCsv}>
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
            {performanceTrend.length === 0 ? (
              <div className="dash-empty small">
                <span>No archived performance reports yet</span>
              </div>
            ) : (
              <>
                <div className="trend-signal-grid">
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{releaseTrendStats.slaBreaches}</span>
                    <span className="trend-signal-label">SLA Breaches</span>
                  </div>
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{formatNumber(releaseTrendStats.avgCompleteness, 0)}%</span>
                    <span className="trend-signal-label">Avg Completeness</span>
                  </div>
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{releaseTrendStats.peakVUsers.toLocaleString()}</span>
                    <span className="trend-signal-label">Peak VUsers</span>
                  </div>
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{formatNumber(releaseTrendStats.maxCPU, 1)}%</span>
                    <span className="trend-signal-label">Max CPU</span>
                  </div>
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{formatNumber(releaseTrendStats.maxMemoryMB, 0)} MB</span>
                    <span className="trend-signal-label">Max Memory</span>
                  </div>
                  <div className="trend-signal-card">
                    <span className="trend-signal-value">{releaseTrendStats.telemetryBreached}</span>
                    <span className="trend-signal-label">Telemetry Breaches</span>
                  </div>
                </div>
                <div className="trend-table-wrap" ref={performanceTableBounds.ref} style={performanceTableBounds.style}>
                  <table className="trend-table">
                    <thead>
                      <tr>
                        {renderPerformanceSortHeader('run', 'Run')}
                        {renderPerformanceSortHeader('completed', 'Completed')}
                        {renderPerformanceSortHeader('peak_tps', 'Peak TPS', 'num')}
                        {renderPerformanceSortHeader('avg_ms', 'Avg ms', 'num')}
                        {renderPerformanceSortHeader('errors', 'Errors', 'num')}
                        {renderPerformanceSortHeader('sla', 'SLA', 'num')}
                        {renderPerformanceSortHeader('complete', 'Complete', 'num')}
                      </tr>
                    </thead>
                    <tbody>
                      {sortedPerformanceTrend.map(({ test, report, runtimeSummary }) => (
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
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </section>
        )}

        {activeDashboardTab === 'active' && (
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
              </div>
            ) : (
              <div className={`dash-active-grid ${customizingCards ? 'is-customizing' : ''}`}>
                {orderedActiveTests.map(test => {
                  const history = metricsHistory[test.id] || []
                  const latest = test.live_metrics
                  const runtimeSummary = formatBrowserRuntimeSummary(test.runtime_settings?.browser)
                  const cardSettings = { ...defaultGraphCardSettings, ...activeCardSettings[test.id] }
                  const metricOption = graphMetricOptions.find(option => option.key === cardSettings.metric) || graphMetricOptions[0]
                  const chartData = history.slice(-60).map(point => ({
                    ...point,
                    selectedMetric: getHistoryMetric(point, cardSettings.metric),
                  }))
                  return (
                    <div
                      key={test.id}
                      className={`dash-active-card ${customizingCards ? 'editable' : ''} ${draggedCardId === test.id ? 'dragging' : ''}`}
                      draggable={customizingCards}
                      onDragStart={event => handleCardDragStart(event, test.id)}
                      onDragOver={event => handleCardDragOver(event, test.id)}
                      onDrop={event => handleCardDrop(event, test.id)}
                      onDragEnd={() => setDraggedCardId(null)}
                      onClick={() => !customizingCards && onSelectTest(test.id)}
                    >
                      <div className="dac-header">
                        <div className="dac-title-wrap">
                          {customizingCards && <span className="dac-drag-handle" title="Drag to reorder">::</span>}
                          <span className="dac-name">{test.name}</span>
                        </div>
                        <StatusBadge status={test.status} />
                      </div>
                      {customizingCards && (
                        <div className="dac-editor" onClick={event => event.stopPropagation()}>
                          <label>
                            Graph
                            <select
                              value={cardSettings.metric}
                              onChange={event => updateCardSettings(test.id, { metric: event.target.value as CardMetricKey })}
                            >
                              {graphMetricOptions.map(option => (
                                <option key={option.key} value={option.key}>{option.label}</option>
                              ))}
                            </select>
                          </label>
                          <label className="dac-checkbox">
                            <input
                              type="checkbox"
                              checked={cardSettings.showGraph}
                              onChange={event => updateCardSettings(test.id, { showGraph: event.target.checked })}
                            />
                            Show graph
                          </label>
                        </div>
                      )}
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
                      {cardSettings.showGraph && chartData.length > 1 && (
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

        {activeDashboardTab === 'activity' && (
          <section className="dash-section dash-activity">
            <h3 className="dash-section-title">Recent Activity</h3>
            {recentTests.length === 0 ? (
              <div className="dash-empty small">
                <span>No completed tests yet</span>
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
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        )}

        {activeDashboardTab === 'releaseTrend' && (
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
          onToggle={() => setReleasePanelOpen(value => !value)}
          onReleaseChange={setActiveRelease}
          onLatestResult={navigateToLatestResult}
          onPrimaryActiveRun={navigateToPrimaryActiveRun}
          onResults={navigateToResults}
          onMonitoring={navigateToMonitoring}
          onNewTest={navigateToNewTest}
          onSelectTest={onSelectTest}
        />
      </div>
    </div>
  )
}
