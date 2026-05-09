import { useState, useEffect } from 'react'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, Legend,
} from 'recharts'
import { useApi } from '../hooks/useApi'
import { TestSummary, MetricsHistoryPoint, TxMetrics, GroupLiveMetrics } from '../types'
import StatusBadge from './StatusBadge'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]

interface Props {
  testId: string
  history: MetricsHistoryPoint[]
  onBack: () => void
}

export default function TestExecution({ testId, history, onBack }: Props) {
  const api = useApi()
  const [test, setTest] = useState<TestSummary | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = async () => {
      try {
        const data = await api.getTest(testId)
        setTest(data)
      } catch (e: any) {
        setError(e.message)
      }
    }
    load()
    const interval = setInterval(load, 2000)
    return () => clearInterval(interval)
  }, [api, testId])

  if (error) {
    return (
      <div className="execution">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <p className="error-msg">{error}</p>
      </div>
    )
  }

  if (!test) {
    return <div className="execution"><p className="loading">Loading...</p></div>
  }

  const metrics = test.live_metrics
  const isRunning = test.status === 'RUNNING' || test.status === 'INITIALIZING'
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []
  const isMultiGroup = (test.groups?.length || 0) > 1
  const groupNames = test.groups?.map((g) => g.name) || []

  return (
    <div className="execution">
      <div className="exec-header">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <div className="exec-title-area">
          <h2>{test.name}</h2>
          <div className="exec-meta">
            <StatusBadge status={test.status} />
            <span className="meta-item">{test.total_vusers} VUsers</span>
            <span className="meta-item">{test.duration}</span>
            {isMultiGroup && (
              <span className="meta-item group-count">{test.groups.length} groups</span>
            )}
          </div>
        </div>
        <div className="exec-actions">
          {test.status === 'QUEUED' && (
            <button className="btn btn-success" onClick={() => api.startTest(testId)}>
              Start
            </button>
          )}
          {isRunning && (
            <>
              <button className="btn btn-secondary" onClick={() => api.stopTest(testId)}>
                Stop
              </button>
              <button className="btn btn-danger" onClick={() => api.abortTest(testId)}>
                Abort
              </button>
            </>
          )}
        </div>
      </div>

      {/* Group config summary */}
      {isMultiGroup && (
        <div className="group-config-bar">
          {test.groups.map((g, i) => (
            <div key={g.name} className="group-chip" style={{ borderLeftColor: GROUP_COLORS[i % GROUP_COLORS.length] }}>
              <span className="group-chip-name">{g.name}</span>
              <span className="group-chip-detail">{g.script_name} · {g.vusers} VUsers</span>
            </div>
          ))}
        </div>
      )}

      {/* Hero metrics */}
      <div className="hero-metrics">
        <div className="hero-card">
          <span className="hero-value">{metrics?.active_vusers ?? 0}</span>
          <span className="hero-label">Active VUsers</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{metrics?.tps?.toFixed(1) ?? '0'}</span>
          <span className="hero-label">TPS</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{metrics?.avg_response_ms?.toFixed(0) ?? '0'}</span>
          <span className="hero-label">Avg Response (ms)</span>
        </div>
        <div className="hero-card">
          <span className="hero-value error-value">
            {metrics?.error_pct?.toFixed(2) ?? '0'}%
          </span>
          <span className="hero-label">Error Rate</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{metrics?.total_transactions ?? 0}</span>
          <span className="hero-label">Total Tx</span>
        </div>
      </div>

      {/* Per-group live metrics */}
      {isMultiGroup && groupMetrics.length > 0 && (
        <div className="group-metrics-grid">
          {groupMetrics.map((gm: GroupLiveMetrics, i: number) => (
            <div
              key={gm.name}
              className="group-metric-card"
              style={{ borderTopColor: GROUP_COLORS[i % GROUP_COLORS.length] }}
            >
              <div className="gm-header">
                <span className="gm-name">{gm.name}</span>
                <span className="gm-vusers">{gm.vusers} VUsers</span>
              </div>
              <div className="gm-stats">
                <div className="gm-stat">
                  <span className="gm-value">{gm.tps?.toFixed(1)}</span>
                  <span className="gm-label">TPS</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.avg_response_ms?.toFixed(0)}</span>
                  <span className="gm-label">Avg ms</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.p90_ms?.toFixed(0)}</span>
                  <span className="gm-label">P90</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.errors}</span>
                  <span className="gm-label">Errors</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Charts */}
      {history.length > 1 && (
        <div className="charts-grid">
          <div className="chart-card">
            <h3 className="chart-title">Virtual Users</h3>
            <ResponsiveContainer width="100%" height={200}>
              <AreaChart data={history} syncId="metrics">
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6 }}
                  labelStyle={{ color: 'var(--text-secondary)' }}
                />
                <Area type="monotone" dataKey="vusers" name="Total" stroke="var(--chart-1)" fill="var(--chart-1)" fillOpacity={0.15} />
              </AreaChart>
            </ResponsiveContainer>
          </div>

          <div className="chart-card">
            <h3 className="chart-title">Transactions Per Second</h3>
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={history} syncId="metrics">
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6 }}
                  labelStyle={{ color: 'var(--text-secondary)' }}
                />
                {isMultiGroup && <Legend />}
                <Line type="monotone" dataKey="tps" name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} />
                {isMultiGroup && groupNames.map((name, i) => (
                  <Line
                    key={name}
                    type="monotone"
                    dataKey={`${name}_tps`}
                    name={name}
                    stroke={GROUP_COLORS[i % GROUP_COLORS.length]}
                    dot={false}
                    strokeWidth={2}
                  />
                ))}
              </LineChart>
            </ResponsiveContainer>
          </div>

          <div className="chart-card">
            <h3 className="chart-title">Response Time (ms)</h3>
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={history} syncId="metrics">
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6 }}
                  labelStyle={{ color: 'var(--text-secondary)' }}
                />
                <Legend />
                <Line type="monotone" dataKey="p50" name="P50" stroke="var(--chart-5)" dot={false} strokeWidth={2} />
                <Line type="monotone" dataKey="p90" name="P90" stroke="var(--chart-3)" dot={false} strokeWidth={2} />
                <Line type="monotone" dataKey="p99" name="P99" stroke="var(--chart-4)" dot={false} strokeWidth={1.5} strokeDasharray="4 2" />
                {isMultiGroup && groupNames.map((name, i) => (
                  <Line
                    key={name}
                    type="monotone"
                    dataKey={`${name}_avgMs`}
                    name={`${name} avg`}
                    stroke={GROUP_COLORS[i % GROUP_COLORS.length]}
                    dot={false}
                    strokeWidth={1.5}
                    strokeDasharray="2 2"
                  />
                ))}
              </LineChart>
            </ResponsiveContainer>
          </div>

          <div className="chart-card">
            <h3 className="chart-title">Error Rate (%)</h3>
            <ResponsiveContainer width="100%" height={200}>
              <AreaChart data={history} syncId="metrics">
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} domain={[0, 'auto']} />
                <Tooltip
                  contentStyle={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 6 }}
                  labelStyle={{ color: 'var(--text-secondary)' }}
                />
                <Area type="monotone" dataKey="errorPct" stroke="var(--chart-4)" fill="var(--chart-4)" fillOpacity={0.15} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}

      {/* Transaction breakdown */}
      {transactions.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Transaction Breakdown</h3>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Transaction</th>
                <th>Count</th>
                <th>Errors</th>
                <th>Avg (ms)</th>
                <th>P50 (ms)</th>
                <th>P90 (ms)</th>
                <th>P99 (ms)</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map((tx: TxMetrics) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.count}</td>
                  <td className={tx.errors > 0 ? 'error-cell' : ''}>{tx.errors}</td>
                  <td>{tx.avg_ms?.toFixed(1)}</td>
                  <td>{tx.p50_ms?.toFixed(1)}</td>
                  <td>{tx.p90_ms?.toFixed(1)}</td>
                  <td>{tx.p99_ms?.toFixed(1)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Percentile summary */}
      {metrics && (
        <div className="percentile-bar">
          <div className="pct-item">
            <span className="pct-label">P50</span>
            <span className="pct-value">{metrics.p50_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P90</span>
            <span className="pct-value">{metrics.p90_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P95</span>
            <span className="pct-value">{metrics.p95_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P99</span>
            <span className="pct-value">{metrics.p99_ms?.toFixed(1)} ms</span>
          </div>
        </div>
      )}
    </div>
  )
}
