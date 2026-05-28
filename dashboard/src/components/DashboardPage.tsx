import { useState, useEffect, useMemo, type DragEvent } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { useApi } from '../hooks/useApi'
import { useApplication } from '../hooks/useApplication'
import { useProject } from '../hooks/useProject'
import { TestSummary, MetricsHistoryPoint, RunArchiveReport, SystemInventoryResponse } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { type NavPage } from './Sidebar'
import StatusBadge from './StatusBadge'
import './DashboardPage.css'

interface Props {
  testStatuses: Record<string, string>
  metricsHistory: Record<string, MetricsHistoryPoint[]>
  connectionState: WebSocketConnectionState
  onSelectTest: (id: string) => void
  onNavigate: (page: NavPage) => void
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

interface GraphCardSettings {
  metric: CardMetricKey
  showGraph: boolean
}

interface PerformanceTrendEntry {
  test: TestSummary
  report: RunArchiveReport
  runtimeSummary: string | null
}

const defaultGraphCardSettings: GraphCardSettings = {
  metric: 'tps',
  showGraph: true,
}

const graphMetricOptions: { key: CardMetricKey; label: string; color: string; digits: number }[] = [
  { key: 'tps', label: 'TPS', color: 'var(--accent)', digits: 1 },
  { key: 'avg_response_ms', label: 'Avg ms', color: 'var(--chart-2)', digits: 0 },
  { key: 'error_pct', label: 'Error %', color: 'var(--danger)', digits: 2 },
  { key: 'active_vusers', label: 'Active VUsers', color: 'var(--chart-3)', digits: 0 },
]

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

function buildReleaseTrendStats(entries: PerformanceTrendEntry[]) {
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

export default function DashboardPage({ testStatuses, metricsHistory, connectionState, onSelectTest, onNavigate }: Props) {
  const api = useApi()
  const { activeApplication, activeRelease } = useApplication()
  const { activeProject } = useProject()
  const dashboardScopeReady = Boolean(activeProject?.id && activeApplication?.id)
  const [tests, setTests] = useState<TestSummary[]>([])
  const [health, setHealth] = useState<HealthData | null>(null)
  const [inventory, setInventory] = useState<SystemInventoryResponse | null>(null)
  const [archiveReports, setArchiveReports] = useState<Record<string, RunArchiveReport | null>>({})
  const [customizingCards, setCustomizingCards] = useState(false)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
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
        const [testRes, healthRes, inventoryRes] = await Promise.all([
          api.listTests({ application_id: activeApplication!.id, project_id: activeProject!.id }),
          api.getHealth().catch(() => null),
          api.getSystemInventory().catch(() => null),
        ])
        setTests(testRes.tests || [])
        if (healthRes) setHealth(healthRes)
        setInventory(inventoryRes)
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
      .sort((a, b) => completionTime(b) - completionTime(a))
      .slice(0, 10),
    [resolvedTests],
  )

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
      .filter(test => Boolean(test.archive))
      .slice(0, 6),
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

  const releaseTrendStats = useMemo(() => buildReleaseTrendStats(performanceTrend), [performanceTrend])

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
    const items: { tone: 'danger' | 'warning' | 'info'; title: string; detail: string; testId?: string }[] = []
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
  const liveAvailableGenerators = inventory
    ? inventory.generators.filter(generator => generator.status === 'connected').length
    : null

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
  const trendPreviewRows = performanceTrend.slice(0, 5)
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
          <button className="btn btn-secondary btn-sm" onClick={navigateToApiGuide} type="button">
            API Guide
          </button>
          {customizingCards && (
            <button className="btn btn-secondary btn-sm" onClick={resetCardLayout} type="button">
              Reset Cards
            </button>
          )}
          <button
            className={`btn btn-sm ${customizingCards ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setCustomizingCards(value => !value)}
            type="button"
          >
            {customizingCards ? 'Done' : 'Customize Cards'}
          </button>
        </div>
      </div>

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

        <button type="button" className="dash-health-card dash-card-button" onClick={navigateToTests}>
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

        <button type="button" className="dash-health-card dash-card-button" onClick={navigateToTests}>
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

        <button type="button" className="dash-health-card dash-card-button" onClick={navigateToMonitoring}>
          <div className="dhc-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-5)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <polyline points="12 6 12 12 16 14" />
            </svg>
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{liveAvailableGenerators ?? health?.capacity_availability?.effective_available_generators ?? '—'}</span>
            <span className="dhc-label">Generators Available</span>
          </div>
        </button>
      </div>

      <section className="dash-section dash-release-snapshot">
        <div className="dash-section-heading-row">
          <h3 className="dash-section-title">Release Snapshot</h3>
          <span className="dash-context-chip">{activeRelease?.name || 'All releases'}</span>
        </div>
        <div className="release-metric-grid">
          <button type="button" className="release-metric dash-card-button" onClick={navigateToLatestResult}>
            <span className="release-metric-value">{latestReleaseRun ? latestReleaseRun.name : 'No runs'}</span>
            <span className="release-metric-label">Latest Result</span>
          </button>
          <button type="button" className="release-metric dash-card-button" onClick={navigateToResults}>
            <span className={`release-metric-value ${releaseStats.failed > 0 ? 'text-danger' : 'text-success'}`}>
              {releaseStats.failed}
            </span>
            <span className="release-metric-label">Failed Runs</span>
          </button>
          <button type="button" className="release-metric dash-card-button" onClick={navigateToResults}>
            <span className="release-metric-value">{releaseStats.archived}/{releaseStats.completed + releaseStats.failed + releaseStats.aborted}</span>
            <span className="release-metric-label">Archives Ready</span>
          </button>
          <button type="button" className="release-metric dash-card-button" onClick={navigateToResults}>
            <span className="release-metric-value">{releaseStats.totalTx.toLocaleString()}</span>
            <span className="release-metric-label">Archived Transactions</span>
          </button>
          <button type="button" className="release-metric dash-card-button" onClick={navigateToPrimaryActiveRun}>
            <span className="release-metric-value">{formatNumber(stats.activeTps, 1)}</span>
            <span className="release-metric-label">Current TPS</span>
          </button>
          <button type="button" className="release-metric dash-card-button" onClick={navigateToPrimaryActiveRun}>
            <span className={`release-metric-value ${stats.avgError > 0 ? 'text-danger' : ''}`}>{formatNumber(stats.avgError, 2)}%</span>
            <span className="release-metric-label">Current Error Rate</span>
          </button>
        </div>
        <div className="release-status-strip" aria-label="Run status summary">
          <button type="button" className="release-status-stat dash-card-button" onClick={navigateToResults}>
            <span className="release-status-value text-success">{stats.completed}</span>
            <span className="release-status-label">Completed</span>
          </button>
          <button type="button" className="release-status-stat dash-card-button" onClick={navigateToResults}>
            <span className="release-status-value" style={{ color: 'var(--chart-3)' }}>{stats.aborted}</span>
            <span className="release-status-label">Aborted</span>
          </button>
          <button type="button" className="release-status-stat dash-card-button" onClick={navigateToTests}>
            <span className="release-status-value" style={{ color: 'var(--accent)' }}>{stats.active}</span>
            <span className="release-status-label">Active Now</span>
          </button>
        </div>
      </section>

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
            <div className="trend-table-wrap">
              <table className="trend-table">
                <thead>
                  <tr>
                    <th>Run</th>
                    <th>Completed</th>
                    <th className="num">Peak TPS</th>
                    <th className="num">Avg ms</th>
                    <th className="num">Errors</th>
                    <th className="num">SLA</th>
                    <th className="num">Complete</th>
                  </tr>
                </thead>
                <tbody>
                  {trendPreviewRows.map(({ test, report, runtimeSummary }) => (
                    <tr key={test.id} onClick={() => onSelectTest(test.id)}>
                      <td>
                        <span className="trend-run-name">{test.name}</span>
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
            {performanceTrend.length > trendPreviewRows.length && (
              <button type="button" className="trend-preview-footer dash-card-button" onClick={navigateToResults}>
                View {performanceTrend.length - trendPreviewRows.length} more archived run{performanceTrend.length - trendPreviewRows.length === 1 ? '' : 's'} in Results
              </button>
            )}
          </>
        )}
      </section>

      {/* Active Tests */}
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
            <span className="dash-empty-hint">{pendingTests.length > 0 ? `${pendingTests.length} test${pendingTests.length === 1 ? '' : 's'} waiting on project capacity.` : 'Start a scenario from the Scenarios page'}</span>
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
                  contentStyle={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 12 }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 600 }}
                />
                <Area type="monotone" dataKey="tps" stroke="var(--chart-2)" fill="var(--chart-2)" fillOpacity={0.15} strokeWidth={1.5} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          </button>
        )}
      </section>

      {/* Bottom row: Activity Feed + Summary Stats */}
      <div className="dash-bottom-row">
        {/* Activity Feed */}
        <section className="dash-section dash-activity">
          <h3 className="dash-section-title">Recent Activity</h3>
          {recentTests.length === 0 ? (
            <div className="dash-empty small">
              <span>No completed tests yet</span>
            </div>
          ) : (
            <div className="dash-feed">
              {recentTests.map(test => (
                <div key={test.id} className="feed-item" onClick={() => onSelectTest(test.id)}>
                  <div className={`feed-dot ${test.status === 'COMPLETE' ? 'success' : test.status === 'FAILED' ? 'danger' : 'warning'}`} />
                  <div className="feed-content">
                    <span className="feed-name">{test.name}</span>
                    <span className="feed-meta">
                      {test.total_vusers} VUsers · {test.duration} · {test.archive ? `${test.archive.transaction_count.toLocaleString()} tx` : 'archive pending'}
                    </span>
                    {formatBrowserRuntimeSummary(test.runtime_settings?.browser) && (
                      <span className="feed-meta">Browser runtime · {formatBrowserRuntimeSummary(test.runtime_settings?.browser)}</span>
                    )}
                    <span className="feed-meta">
                      {formatDateTime(test.ended_at || test.start_time || test.created_at)}
                    </span>
                  </div>
                  <StatusBadge status={test.status} />
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="dash-section dash-attention">
          <h3 className="dash-section-title">Attention Needed</h3>
          {attentionItems.length === 0 ? (
            <div className="dash-empty small">
              <span>No current issues detected</span>
            </div>
          ) : (
            <div className="attention-list">
              {attentionItems.map((item, index) => (
                <div key={`${item.title}-${index}`} className={`attention-item ${item.tone}`} onClick={() => item.testId && onSelectTest(item.testId)}>
                  <span className={`attention-marker ${item.tone}`} />
                  <div className="attention-content">
                    <span className="attention-title">{item.title}</span>
                    <span className="attention-detail">{item.detail}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
