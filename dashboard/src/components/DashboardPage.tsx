import { useState, useEffect, useMemo, type DragEvent } from 'react'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { useApi } from '../hooks/useApi'
import { useApplication } from '../hooks/useApplication'
import { TestSummary, MetricsHistoryPoint, RunArchiveReport } from '../types'
import StatusBadge from './StatusBadge'
import './DashboardPage.css'

interface Props {
  testStatuses: Record<string, string>
  metricsHistory: Record<string, MetricsHistoryPoint[]>
  connected: boolean
  onSelectTest: (id: string) => void
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

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never'
}

function completionTime(test: TestSummary) {
  return new Date(test.ended_at || test.start_time || test.created_at || 0).getTime()
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

export default function DashboardPage({ testStatuses, metricsHistory, connected, onSelectTest }: Props) {
  const api = useApi()
  const { activeApplication, activeRelease } = useApplication()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [health, setHealth] = useState<HealthData | null>(null)
  const [archiveReports, setArchiveReports] = useState<Record<string, RunArchiveReport | null>>({})
  const [customizingCards, setCustomizingCards] = useState(false)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
  const [activeCardOrder, setActiveCardOrder] = useState<string[]>(() => loadJson<string[]>('stormrunner.dashboard.activeCardOrder', []))
  const [activeCardSettings, setActiveCardSettings] = useState<Record<string, GraphCardSettings>>(() =>
    loadJson<Record<string, GraphCardSettings>>('stormrunner.dashboard.activeCardSettings', {}),
  )

  useEffect(() => {
    const load = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      try {
        const [testRes, healthRes] = await Promise.all([
          api.listTests(activeApplication?.id ? { application_id: activeApplication.id } : undefined),
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
  }, [api, activeApplication?.id])

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
    window.localStorage.setItem('stormrunner.dashboard.activeCardOrder', JSON.stringify(activeCardOrder))
  }, [activeCardOrder])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.dashboard.activeCardSettings', JSON.stringify(activeCardSettings))
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
        return { test, report }
      })
      .filter((entry): entry is { test: TestSummary; report: RunArchiveReport } => Boolean(entry)),
    [archivedReleaseRuns, archiveReports],
  )

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
  }, [resolvedTests, activeTests])

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
    return items.slice(0, 6)
  }, [activeTests, releaseCompleted, health])

  const statusColor = connected && health?.status === 'healthy' ? 'healthy' : connected ? 'degraded' : 'offline'
  const statusLabel = statusColor === 'healthy' ? 'Healthy' : statusColor === 'degraded' ? 'Degraded' : 'Offline'

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
        <div className={`dash-health-card status-${statusColor}`}>
          <div className="dhc-icon">
            <span className={`health-dot ${statusColor}`} />
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{statusLabel}</span>
            <span className="dhc-label">API Server</span>
          </div>
        </div>

        <div className="dash-health-card">
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
        </div>

        <div className="dash-health-card">
          <div className="dhc-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-3)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
            </svg>
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{stats.activeVUsers}</span>
            <span className="dhc-label">Active VUsers</span>
          </div>
        </div>

        <div className="dash-health-card">
          <div className="dhc-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-5)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <polyline points="12 6 12 12 16 14" />
            </svg>
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{health?.capacity_availability?.effective_available_generators ?? '—'}</span>
            <span className="dhc-label">Generators Available</span>
          </div>
        </div>
      </div>

      <section className="dash-section dash-release-snapshot">
        <div className="dash-section-heading-row">
          <h3 className="dash-section-title">Release Snapshot</h3>
          <span className="dash-context-chip">{activeRelease?.name || 'All releases'}</span>
        </div>
        <div className="release-metric-grid">
          <div className="release-metric">
            <span className="release-metric-value">{latestReleaseRun ? latestReleaseRun.name : 'No runs'}</span>
            <span className="release-metric-label">Latest Result</span>
          </div>
          <div className="release-metric">
            <span className={`release-metric-value ${releaseStats.failed > 0 ? 'text-danger' : 'text-success'}`}>
              {releaseStats.failed}
            </span>
            <span className="release-metric-label">Failed Runs</span>
          </div>
          <div className="release-metric">
            <span className="release-metric-value">{releaseStats.archived}/{releaseStats.completed + releaseStats.failed + releaseStats.aborted}</span>
            <span className="release-metric-label">Archives Ready</span>
          </div>
          <div className="release-metric">
            <span className="release-metric-value">{releaseStats.totalTx.toLocaleString()}</span>
            <span className="release-metric-label">Archived Transactions</span>
          </div>
          <div className="release-metric">
            <span className="release-metric-value">{formatNumber(stats.activeTps, 1)}</span>
            <span className="release-metric-label">Current TPS</span>
          </div>
          <div className="release-metric">
            <span className={`release-metric-value ${stats.avgError > 0 ? 'text-danger' : ''}`}>{formatNumber(stats.avgError, 2)}%</span>
            <span className="release-metric-label">Current Error Rate</span>
          </div>
        </div>
      </section>

      <section className="dash-section dash-performance-trend">
        <div className="dash-section-heading-row">
          <h3 className="dash-section-title">Performance Trend</h3>
          <span className="dash-context-chip">Last {archivedReleaseRuns.length || 0} archived runs</span>
        </div>
        {performanceTrend.length === 0 ? (
          <div className="dash-empty small">
            <span>No archived performance reports yet</span>
          </div>
        ) : (
          <div className="trend-table-wrap">
            <table className="trend-table">
              <thead>
                <tr>
                  <th>Run</th>
                  <th>Completed</th>
                  <th className="num">Peak TPS</th>
                  <th className="num">Avg ms</th>
                  <th className="num">Errors</th>
                  <th className="num">Peak VUsers</th>
                  <th className="num">Transactions</th>
                </tr>
              </thead>
              <tbody>
                {performanceTrend.map(({ test, report }) => (
                  <tr key={test.id} onClick={() => onSelectTest(test.id)}>
                    <td>
                      <span className="trend-run-name">{test.name}</span>
                      <span className="trend-run-meta">{test.script_name || `${test.total_vusers} VUsers`}</span>
                    </td>
                    <td>{formatDateTime(test.ended_at || test.start_time || test.created_at)}</td>
                    <td className="num strong">{formatNumber(report.overview.peak_tps, 1)}</td>
                    <td className="num">{formatNumber(report.overview.avg_response_ms)}</td>
                    <td className={`num ${report.overview.error_pct > 0 ? 'text-danger strong' : ''}`}>
                      {formatNumber(report.overview.error_pct, 2)}%
                    </td>
                    <td className="num">{report.overview.peak_active_vusers.toLocaleString()}</td>
                    <td className="num">{report.overview.transactions.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Active Tests */}
      <section className="dash-section">
        <h3 className="dash-section-title">
          Active Tests
          {stats.active > 0 && <span className="dash-badge pulse">{stats.active}</span>}
        </h3>
        {activeTests.length === 0 ? (
          <div className="dash-empty">
            <span className="dash-empty-icon">
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
              </svg>
            </span>
            <span>No tests currently running</span>
            <span className="dash-empty-hint">Start a scenario from the Scenarios page</span>
          </div>
        ) : (
          <div className={`dash-active-grid ${customizingCards ? 'is-customizing' : ''}`}>
            {orderedActiveTests.map(test => {
              const history = metricsHistory[test.id] || []
              const latest = test.live_metrics
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
                    <span className="dac-detail">{test.duration}</span>
                  </div>
                </div>
              )
            })}
          </div>
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

        {/* Test Summary Breakdown */}
        <section className="dash-section dash-summary">
          <h3 className="dash-section-title">Test Summary</h3>
          <div className="dash-summary-cards">
            <div className="summary-stat">
              <span className="summary-stat-value text-success">{stats.completed}</span>
              <span className="summary-stat-label">Completed</span>
              <div className="summary-bar">
                <div className="summary-bar-fill success" style={{ width: stats.total ? `${(stats.completed / stats.total) * 100}%` : '0%' }} />
              </div>
            </div>
            <div className="summary-stat">
              <span className="summary-stat-value text-danger">{stats.failed}</span>
              <span className="summary-stat-label">Failed</span>
              <div className="summary-bar">
                <div className="summary-bar-fill danger" style={{ width: stats.total ? `${(stats.failed / stats.total) * 100}%` : '0%' }} />
              </div>
            </div>
            <div className="summary-stat">
              <span className="summary-stat-value" style={{ color: 'var(--chart-3)' }}>{stats.aborted}</span>
              <span className="summary-stat-label">Aborted</span>
              <div className="summary-bar">
                <div className="summary-bar-fill warning" style={{ width: stats.total ? `${(stats.aborted / stats.total) * 100}%` : '0%' }} />
              </div>
            </div>
            <div className="summary-stat">
              <span className="summary-stat-value" style={{ color: 'var(--accent)' }}>{stats.active}</span>
              <span className="summary-stat-label">Active Now</span>
              <div className="summary-bar">
                <div className="summary-bar-fill active" style={{ width: stats.total ? `${(stats.active / stats.total) * 100}%` : '0%' }} />
              </div>
            </div>
          </div>

          {/* Aggregate sparkline if we have any active test history */}
          {activeTests.length > 0 && (() => {
            const allHistory = Object.values(metricsHistory).flat()
            if (allHistory.length < 2) return null
            const aggregated = allHistory.slice(-60)
            return (
              <div className="dash-aggregate-chart">
                <span className="aggregate-label">Aggregate TPS (active tests)</span>
                <ResponsiveContainer width="100%" height={80}>
                  <AreaChart data={aggregated}>
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
              </div>
            )
          })()}
        </section>
      </div>
    </div>
  )
}
