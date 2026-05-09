import { useState, useEffect, useMemo } from 'react'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { useApi } from '../hooks/useApi'
import { TestSummary, MetricsHistoryPoint } from '../types'
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
}

export default function DashboardPage({ testStatuses, metricsHistory, connected, onSelectTest }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [health, setHealth] = useState<HealthData | null>(null)
  const [loadTime] = useState(() => new Date())

  useEffect(() => {
    const load = async () => {
      try {
        const [testRes, healthRes] = await Promise.all([
          api.listTests(),
          api.getHealth().catch(() => null),
        ])
        setTests(testRes.tests || [])
        if (healthRes) setHealth(healthRes)
      } catch { /* ignore */ }
    }
    load()
    const interval = setInterval(load, 3000)
    return () => clearInterval(interval)
  }, [api])

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

  const recentTests = useMemo(() =>
    resolvedTests
      .filter(t => t.status === 'COMPLETE' || t.status === 'FAILED' || t.status === 'ABORTED')
      .slice(0, 10),
    [resolvedTests],
  )

  const stats = useMemo(() => {
    const completed = resolvedTests.filter(t => t.status === 'COMPLETE').length
    const failed = resolvedTests.filter(t => t.status === 'FAILED').length
    const aborted = resolvedTests.filter(t => t.status === 'ABORTED').length
    return { total: resolvedTests.length, completed, failed, aborted, active: activeTests.length }
  }, [resolvedTests, activeTests])

  const uptimeStr = useMemo(() => {
    const diff = Date.now() - loadTime.getTime()
    const mins = Math.floor(diff / 60000)
    if (mins < 60) return `${mins}m`
    const hrs = Math.floor(mins / 60)
    return `${hrs}h ${mins % 60}m`
  }, [loadTime])

  const statusColor = connected && health?.status === 'healthy' ? 'healthy' : connected ? 'degraded' : 'offline'
  const statusLabel = statusColor === 'healthy' ? 'Healthy' : statusColor === 'degraded' ? 'Degraded' : 'Offline'

  return (
    <div className="page dash">
      <div className="page-header">
        <div>
          <h2>Dashboard</h2>
          <p className="page-subtitle">System overview and active test monitoring</p>
        </div>
      </div>

      {/* System Health Row */}
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
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
            </svg>
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{health?.version || '—'}</span>
            <span className="dhc-label">Version</span>
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
            <span className="dhc-value">{health?.ws_clients ?? 0}</span>
            <span className="dhc-label">WebSocket Clients</span>
          </div>
        </div>

        <div className="dash-health-card">
          <div className="dhc-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--chart-3)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
            </svg>
          </div>
          <div className="dhc-content">
            <span className="dhc-value">{stats.total}</span>
            <span className="dhc-label">Total Tests</span>
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
            <span className="dhc-value">{uptimeStr}</span>
            <span className="dhc-label">Session Uptime</span>
          </div>
        </div>
      </div>

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
          <div className="dash-active-grid">
            {activeTests.map(test => {
              const history = metricsHistory[test.id] || []
              const latest = test.live_metrics
              return (
                <div key={test.id} className="dash-active-card" onClick={() => onSelectTest(test.id)}>
                  <div className="dac-header">
                    <span className="dac-name">{test.name}</span>
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
                  {history.length > 1 && (
                    <div className="dac-sparkline">
                      <ResponsiveContainer width="100%" height={60}>
                        <AreaChart data={history.slice(-60)}>
                          <defs>
                            <linearGradient id={`spark-${test.id}`} x1="0" y1="0" x2="0" y2="1">
                              <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.3} />
                              <stop offset="100%" stopColor="var(--accent)" stopOpacity={0} />
                            </linearGradient>
                          </defs>
                          <Area
                            type="monotone"
                            dataKey="tps"
                            stroke="var(--accent)"
                            fill={`url(#spark-${test.id})`}
                            strokeWidth={1.5}
                            isAnimationActive={false}
                          />
                        </AreaChart>
                      </ResponsiveContainer>
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
                      {test.total_vusers} VUsers · {test.duration}
                      {test.ended_at && ` · ${new Date(test.ended_at).toLocaleTimeString()}`}
                    </span>
                  </div>
                  <StatusBadge status={test.status} />
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
